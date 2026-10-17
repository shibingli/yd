package ws

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shibingli/yd/core/errx"
)

// maxInt is the largest value an int can hold on this platform. | maxInt 是当前平台 int 能表示的最大值。
const maxInt = int(^uint(0) >> 1)

// Conn is an established WebSocket connection. | Conn 是一条已建立的 WebSocket 连接。
//
// Concurrency contract (consistent with RFC 6455 and mainstream implementations): | 并发约定（与 RFC 6455 及主流实现一致）：
//   - **one** reader goroutine and **one** writer goroutine may run concurrently; |   - 允许**一个**读协程与**一个**写协程并发；
//   - concurrent calls to the write methods from several writer goroutines are serialized by an internal mutex; |   - 多个写协程并发调用写方法由内部互斥锁串行化；
//   - control frames (ping/pong/close) are answered automatically by the read path, with no caller involvement needed. |   - 控制帧（ping/pong/close）由读路径自动应答，无需调用方干预。
//
// Write contract: frames larger than the write buffer are sent through net.Buffers in one writev,
// **without copying the payload**; smaller frames are coalesced into the write buffer and flushed as
// one transport write, so a small message never costs a separate write per buffer. The read side
// reads net.Conn directly by default (no bufio layer) unless ReadBufferSize asks for one, and the
// slice returned by ReadMessage points into the connection's internal reuse buffer; that slice is
// valid **until the next read operation**; copy it yourself if you need to hold it longer. | 写约定：
// 大于写缓冲的帧经 net.Buffers 以一次 writev 发出，**不复制载荷**；较小的帧合并进写缓冲并作为一次
// 传输层写入刷新，因此小消息不会为每个缓冲各付一次写系统调用。读侧默认直接读 net.Conn（无 bufio
// 中间层，除非 ReadBufferSize 要求），ReadMessage 返回的切片指向连接内部复用缓冲；该切片在
// **下一次读操作前**有效，需要长期持有请自行复制。
type Conn struct {
	conn net.Conn

	readErr error
	br      *bufio.Reader // non-nil only when ReadBufferSize > 0 | 仅在 ReadBufferSize > 0 时非空
	bw      *bufio.Writer // coalesces small frames into one transport write | 把小帧合并为一次传输层写入

	pingHandler  func(string) error
	pongHandler  func(string) error
	closeHandler func(CloseCode, string) error
	untrack      func()

	subprotocol string

	// compression is the negotiated RFC 7692 configuration; nil means no extension was negotiated. | compression 是协商出的 RFC 7692 配置；nil 表示未协商扩展。
	compression  *compressionConfig
	compressor   *messageCompressor
	decompressor *messageDecompressor

	readBuf []byte
	maskBuf []byte

	// writeBase is the stable two-element scatter list; writeList is the copy handed to WriteTo,
	// which consumes it, so the base view survives for the next frame. Both live on the connection,
	// which keeps the per-frame buffer list allocation-free. | writeBase 是稳定的两元素分散列表；
	// writeList 是交给 WriteTo 的副本（WriteTo 会消费它），基视图因此能留给下一帧。两者都存放在
	// 连接上，使每帧的缓冲列表保持零分配。
	writeBase net.Buffers
	writeList net.Buffers

	// msgReader and payloadReader are the connection-owned reader state reused by NextReader, so a
	// steady-state read performs no per-message allocation. | msgReader 与 payloadReader 是
	// NextReader 复用的连接自有读取状态，因此稳态读取不产生按消息的分配。
	msgReader     MessageReader
	payloadReader framePayloadReader

	opts Options

	utf8 utf8State

	msgLen      int64 // bytes accumulated for the current message | 当前消息已累积字节数
	frameLen    int64 // total payload length of the current frame | 当前帧载荷总长度
	frameRemain int64 // bytes of the current frame still unread | 当前帧剩余未读字节
	closeMu     sync.Mutex

	writeMu sync.Mutex

	// trackMu guards untrack, which Close / CloseNow consume exactly once. | trackMu 保护 untrack，Close / CloseNow 恰好消费一次。
	trackMu sync.Mutex

	headerBuf [maxHeaderSize]byte

	// controlBuf stages control-frame payloads on the read goroutine; control
	// frames are at most 125 bytes, so reusing it removes a per-frame allocation. | controlBuf
	// 在读协程暂存控制帧载荷；控制帧至多 125 字节，复用它可去掉每帧一次分配。
	controlBuf [maxControlPayload]byte

	// writeHeaderBuf is the outbound counterpart of headerBuf; keeping it on the connection means a
	// frame header never escapes to the heap, and read/write can still run concurrently. | writeHeaderBuf
	// 是 headerBuf 的出方向对应物；把它放在连接上，帧头因此不会上堆，同时读写仍可并发。
	writeHeaderBuf [maxHeaderSize]byte

	maskKey  [4]byte
	isClient bool

	// kind identifies the transport; it decides whether Close may leave the
	// transport to the peer (HTTP/1.1 client) or must close it (streams). | kind
	// 标识传输类型；它决定 Close 是否把传输留给对端（HTTP/1.1 客户端）还是必须自行关闭（流）。
	kind TransportKind

	msgOp         OpCode // type of the current fragmented message; 0 means no message in progress | 当前分片消息的类型，0 表示无进行中消息
	frameFin      bool
	frameMasked   bool
	closeSent     bool
	closeReceived bool
	msgCompressed bool
	msgFirstFrame bool // current frame starts a message rather than continuing one | 当前帧是消息首帧而不是续帧

	// writeCompression toggles outbound compression after negotiation; the inbound direction always honours the negotiation. | writeCompression 在协商后切换出站压缩；入站方向始终遵循协商结果。
	writeCompression atomic.Bool
	// closeSentFlag mirrors closeSent for the lock-free rejection in WriteMessage;
	// it is set under closeMu and never cleared. | closeSentFlag 是 closeSent 的镜像，
	// 供 WriteMessage 无锁拒绝；它在 closeMu 下置位且从不清除。
	closeSentFlag atomic.Bool
}

// newConn constructs the connection object. | newConn 构造连接对象。
func newConn(nc net.Conn, o Options, isClient bool) *Conn {
	writeBufs := make(net.Buffers, 2)
	c := &Conn{conn: nc, opts: o, isClient: isClient, kind: TransportTCP, writeBase: writeBufs, writeList: writeBufs}
	if o.ReadBufferSize > 0 {
		c.br = bufio.NewReaderSize(nc, o.ReadBufferSize)
	}
	writeSize := o.WriteBufferSize
	if writeSize <= 0 {
		writeSize = defaultWriteBufferSize
	}
	c.bw = bufio.NewWriterSize(nc, writeSize)
	return c
}

// ---------------------------------------------------------------- Tracking | 追踪

// setTracker registers c with t and arms automatic untracking. | setTracker 把 c 登记到 t，并装配自动取消登记。
//
// The untrack function returned by the tracker is idempotent, so it is safe for
// several close paths to race for it. | 追踪器返回的取消登记函数是幂等的，因此多条关闭路径竞争它也是安全的。
func (c *Conn) setTracker(t Tracker) {
	if t == nil {
		return
	}
	untrack := t.Track(c)
	c.trackMu.Lock()
	c.untrack = untrack
	c.trackMu.Unlock()
}

// detach removes c from its tracker. | detach 把 c 从追踪表中移除。
//
// It is idempotent and consumes the untrack function, so a connection that has
// already been detached (or was never tracked) is left untouched. | 它幂等且会消费取消登记函数，
// 因此已经取消登记（或从未登记）的连接不受影响。
func (c *Conn) detach() {
	c.trackMu.Lock()
	untrack := c.untrack
	c.untrack = nil
	c.trackMu.Unlock()
	if untrack != nil {
		untrack()
	}
}

// setReadErr records a terminating read error and detaches the connection. | setReadErr 记录终止性读错误并取消连接登记。
//
// A connection that hit a terminating read error can never serve another
// message, so leaving it in the tracker would only leak memory until shutdown. | 遇到终止性读错误的连接不可能再服务任何消息，
// 继续留在追踪表里只会在退出前持续占用内存。
func (c *Conn) setReadErr(err error) error {
	c.readErr = err
	c.detach()
	return err
}

// ---------------------------------------------------------------- Basic accessors | 基本访问

// LocalAddr returns the local address. | LocalAddr 返回本地地址。
func (c *Conn) LocalAddr() net.Addr { return c.conn.LocalAddr() }

// RemoteAddr returns the remote address. | RemoteAddr 返回对端地址。
func (c *Conn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// Subprotocol returns the subprotocol negotiated during the handshake, or an empty string when none was negotiated. | Subprotocol 返回握手协商出的子协议，未协商时为空串。
func (c *Conn) Subprotocol() string { return c.subprotocol }

// TransportKind reports the transport this connection runs on. | TransportKind 报告该连接所在的传输。
func (c *Conn) TransportKind() TransportKind { return c.kind }

// UnderlyingConn returns the transport as a net.Conn. | UnderlyingConn 把传输作为 net.Conn 返回。
//
// On HTTP/1.1 it is the real TCP connection. On HTTP/2 and HTTP/3 it is a
// stream adapter, **not** a *net.TCPConn: type-asserting *net.TCPConn will fail.
// Use TransportKind to branch before applying socket options. Reading or
// writing the returned value directly would break frame boundaries. | HTTP/1.1 下它是真实
// TCP 连接；HTTP/2 与 HTTP/3 下它是流适配器，**不是** *net.TCPConn——对它做 *net.TCPConn 断言会失败。
// 设置套接字选项前请先用 TransportKind 分支。直接读写该值会破坏帧边界。
func (c *Conn) UnderlyingConn() net.Conn { return c.conn }

// SetReadDeadline sets the read deadline. | SetReadDeadline 设置读超时。
func (c *Conn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

// SetWriteDeadline sets the write deadline. | SetWriteDeadline 设置写超时。
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

// SetPingHandler sets the callback invoked when a ping is received; returning an error terminates the read loop. | SetPingHandler 设置收到 ping 时的回调；返回错误会终止读循环。
// When it is not set, a pong is sent back automatically. | 未设置时内部自动回复 pong。
func (c *Conn) SetPingHandler(h func(string) error) { c.pingHandler = h }

// SetPongHandler sets the callback invoked when a pong is received. | SetPongHandler 设置收到 pong 时的回调。
func (c *Conn) SetPongHandler(h func(string) error) { c.pongHandler = h }

// SetCloseHandler sets the callback invoked when a close frame is received (called after the close frame has been echoed back). | SetCloseHandler 设置收到关闭帧时的回调（在回送关闭帧之后调用）。
func (c *Conn) SetCloseHandler(h func(CloseCode, string) error) { c.closeHandler = h }

// EnableWriteCompression turns outbound permessage-deflate compression on or off. | EnableWriteCompression 开启或关闭出站 permessage-deflate 压缩。
//
// The setting only has an effect when the handshake negotiated the extension; without negotiation | 该设置仅在握手协商出扩展时生效；未协商时
// it is a no-op, because RFC 7692 §6.1 forbids setting RSV1 on a frame the peer would not expect it on. | 调用无任何效果，因为 RFC 7692 §6.1 禁止在对端意料之外的帧上设置 RSV1。
// Disabling it does not disable decompression: inbound compressed messages are always inflated. | 关闭它不影响解压：入站压缩消息始终会被解压。
func (c *Conn) EnableWriteCompression(enable bool) {
	if c.compression == nil {
		return
	}
	c.writeCompression.Store(enable)
}

// setCompression installs the negotiated configuration and arms both directions. | setCompression 装配协商结果并启用两个方向。
//
// It must be called once, before the connection is handed to concurrent readers and writers. | 它必须在连接交给并发读写者之前调用一次。
func (c *Conn) setCompression(cfg *compressionConfig) {
	if cfg == nil {
		return
	}
	c.compression = cfg
	compressBits, compressNoTakeover := cfg.serverWindowBits, cfg.serverNoContextTakeover
	decompressNoTakeover := cfg.clientNoContextTakeover
	if c.isClient {
		compressBits, compressNoTakeover = cfg.clientWindowBits, cfg.clientNoContextTakeover
		decompressNoTakeover = cfg.serverNoContextTakeover
	}
	c.compressor = newMessageCompressor(compressBits, compressNoTakeover)
	c.decompressor = newMessageDecompressor(decompressNoTakeover)
	c.writeCompression.Store(true)
}

// ---------------------------------------------------------------- Reading | 读取

// readFull reads b fully from the connection, preferring bufio when it is enabled. | readFull 从连接读满 b，优先走 bufio（如启用）。
func (c *Conn) readFull(b []byte) error {
	if c.br != nil {
		_, err := io.ReadFull(c.br, b)
		return err
	}
	_, err := io.ReadFull(c.conn, b)
	return err
}

// ReadMessage reads one complete message. | ReadMessage 读取一条完整消息。
//
// The returned payload slice points into the connection's internal reuse buffer and is valid only until the next read operation. | 返回的载荷切片指向连接内部复用缓冲，仅在下一次读操作前有效。
// An error of type *CloseError means the peer ended the connection with a close frame. | 返回错误为 *CloseError 时表示对端以关闭帧结束连接。
func (c *Conn) ReadMessage() (OpCode, []byte, error) {
	op, r, err := c.NextReader()
	if err != nil {
		return 0, nil, err
	}
	c.readBuf = c.readBuf[:0]
	// Fast path: a single uncompressed frame that is already fully buffered can
	// be returned straight from the bufio buffer, avoiding a payload copy. The
	// returned slice is valid until the next read, exactly like readBuf. | 快路径：
	// 已完整缓冲的单帧未压缩消息直接从 bufio 缓冲返回，省去一次载荷复制。返回切片
	// 与 readBuf 一样只在下一次读操作前有效。
	if !r.compressed && c.msgFirstFrame && c.frameFin && r.remaining > 0 &&
		c.br != nil && r.remaining <= int64(c.br.Buffered()) {
		if payload, perr := c.br.Peek(int(r.remaining)); perr == nil {
			if c.frameMasked {
				maskBytes(c.maskKey, 0, payload)
			}
			if op == TextMessage && (!c.utf8.write(payload) || !c.utf8.complete()) {
				return 0, nil, c.fail(CloseInvalidFramePayloadData, ErrInvalidUTF8)
			}
			if _, derr := c.br.Discard(len(payload)); derr != nil {
				return 0, nil, c.setReadErr(derr)
			}
			c.frameRemain = 0
			r.done = true
			c.msgOp = 0
			c.msgLen = 0
			c.msgCompressed = false
			return op, payload, nil
		}
	}
	buf := c.readBuf
	for {
		// A finished uncompressed frame needs no room: the next read only
		// reports EOF. Compressed messages and unfinished frames still do. | 已结束的
		// 未压缩帧不需要再扩容：下一次读取只返回 EOF；压缩消息与未结束的帧仍需要。
		needRoom := r.compressed || r.remaining > 0 || !c.frameFin
		if needRoom && int64(cap(buf))-int64(len(buf)) < 512 {
			grow := int64(512)
			switch {
			case r.compressed:
				// The decompressed size is unknown, so keep doubling. | 解压后大小未知，继续按倍数增长。
				grow = int64(len(buf) + 512)
			case r.remaining > grow:
				// Size the buffer to the bytes still in this frame instead of
				// doubling past them. | 按当前帧剩余字节精确扩容，而不是翻倍越过它。
				grow = r.remaining
			}
			want := int64(len(buf)) + grow
			if want > int64(maxInt) {
				want = int64(maxInt)
			}
			grown := make([]byte, len(buf), int(want))
			copy(grown, buf)
			buf = grown
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err == io.EOF {
			c.readBuf = buf
			return op, buf, nil
		}
		if err != nil {
			c.readBuf = buf
			return 0, nil, err
		}
	}
}

// MessageReader streams one message; control frames (ping/pong/close) are handled automatically underneath. | MessageReader 流式读取一条消息；控件帧（ping/pong/close）在底层自动处理。
//
// The reader points at connection-owned state and is valid only until the next call to NextReader or
// ReadMessage; retaining it across messages is not supported (and was never safe, since the
// connection has a single read stream). | 读取器指向连接自有状态，只在下次调用 NextReader 或
// ReadMessage 之前有效；跨消息保留它不受支持（由于连接只有一条读流，这本来也从不安全）。
type MessageReader struct {
	c          *Conn
	inflate    io.Reader // non-nil when the message is permessage-deflate compressed | 消息经 permessage-deflate 压缩时非空
	remaining  int64
	produced   int64 // decompressed bytes emitted for the current message | 当前消息已输出的解压字节数
	op         OpCode
	done       bool
	compressed bool
}

// OpCode returns the type of this message. | OpCode 返回该消息的类型。
func (r *MessageReader) OpCode() OpCode { return r.op }

// Read implements io.Reader; it returns io.EOF when the message ends. | Read 实现 io.Reader；消息结束时返回 io.EOF。
//
// Reading switches the masking key and resets the masking offset automatically at frame boundaries (RFC 6455 §5.3). | 读取会在帧边界处自动切换掩码键并重置掩码偏移（RFC 6455 §5.3）。
// Compressed messages are inflated on the fly and capped at MaxMessageSize, so a decompression bomb | 压缩消息会被即时解压并受 MaxMessageSize 限制，解压炸弹因此
// is closed with 1009 instead of being buffered in memory. | 会以 1009 关闭，而不会在内存中被缓冲放大。
func (r *MessageReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	if r.compressed {
		return r.readCompressed(p)
	}
	for r.remaining == 0 {
		if r.c.frameFin {
			if r.op == TextMessage {
				if !r.c.utf8.complete() {
					return 0, r.c.fail(CloseInvalidFramePayloadData, ErrInvalidUTF8)
				}
			}
			r.done = true
			r.c.msgOp = 0
			r.c.msgLen = 0
			r.c.msgCompressed = false
			return 0, io.EOF
		}
		if err := r.c.nextDataFrame(&r.remaining); err != nil {
			r.done = true
			return 0, err
		}
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := io.ReadFull(r.reader(), p)
	if n > 0 {
		if r.c.frameMasked {
			maskBytes(r.c.maskKey, int(r.c.frameOffset()), p[:n])
		}
		if r.op == TextMessage && !r.c.utf8.write(p[:n]) {
			// the n bytes already read are still returned to the caller, but the error marks an invalid payload | 已读出的 n 字节仍返回给调用方，但错误标记为非法载荷
			return n, r.c.fail(CloseInvalidFramePayloadData, ErrInvalidUTF8)
		}
		r.remaining -= int64(n)
		r.c.msgLen += int64(n)
	}
	if err != nil {
		r.done = true
		final := err
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			final = ErrUnexpectedEOF
		}
		return n, r.c.setReadErr(final)
	}
	return n, nil
}

// readCompressed inflates one message and enforces the decompressed size limit. | readCompressed 解压一条消息并执行解压后大小限制。
//
// A flate stream terminated by the RFC 7692 tail reports io.ErrUnexpectedEOF instead of io.EOF; both | 以 RFC 7692 尾部结束的 flate 流会返回 io.ErrUnexpectedEOF 而非 io.EOF；
// are normal end-of-message signals here. Any error already recorded by the frame layer (protocol | 二者在此都表示消息正常结束。帧层已记录的错误（协议错误、对端关闭等）
// violation, peer close, truncated connection) takes precedence over flate's own decoding error. | 优先于 flate 自身的解码错误，直接原样返回。
func (r *MessageReader) readCompressed(p []byte) (int, error) {
	n, err := r.inflate.Read(p)
	if n > 0 {
		r.produced += int64(n)
		if r.produced > r.limit() {
			return n, r.c.fail(CloseMessageTooBig, ErrMessageTooBig)
		}
		if r.op == TextMessage && !r.c.utf8.write(p[:n]) {
			return n, r.c.fail(CloseInvalidFramePayloadData, ErrInvalidUTF8)
		}
		r.c.decompressor.record(p[:n])
	}
	if err == nil {
		return n, nil
	}
	if r.c.readErr != nil {
		r.done = true
		return n, r.c.readErr
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		if r.op == TextMessage && !r.c.utf8.complete() {
			r.done = true
			return n, r.c.fail(CloseInvalidFramePayloadData, ErrInvalidUTF8)
		}
		r.done = true
		r.c.msgOp = 0
		r.c.msgLen = 0
		r.c.msgCompressed = false
		return n, io.EOF
	}
	r.done = true
	return n, r.c.fail(CloseInvalidFramePayloadData, errx.WrapDetail(err, ErrInvalidCompressedData,
		"failed to inflate the message | 解压消息失败"))
}

// limit returns the per-message size cap, falling back to the default when the connection was built without normalization. | limit 返回单条消息上限，连接未规范化时回退到默认值。
func (r *MessageReader) limit() int64 {
	if r.c.opts.MaxMessageSize > 0 {
		return r.c.opts.MaxMessageSize
	}
	return defaultMaxMessageSize
}

// reader returns the read source for the current frame payload (bufio or the raw connection). | reader 返回当前帧载荷的读取源（bufio 或裸连接）。
func (r *MessageReader) reader() io.Reader {
	if r.c.br != nil {
		return r.c.br
	}
	return r.c.conn
}

// NextReader starts reading the next message and returns its type along with a streaming reader. | NextReader 开始读取下一条消息，返回消息类型与流式读取器。
//
// The returned reader points at connection-owned state: it is valid only until the next call to
// NextReader or ReadMessage. That keeps a steady-state read path free of per-message allocations
// (the reader and the compressed-payload adapter are reused). | 返回的读取器指向连接自有的状态：
// 它只在下次调用 NextReader 或 ReadMessage 之前有效。这使得稳态读取路径没有按消息产生的分配
// （读取器与压缩载荷适配器均被复用）。
func (c *Conn) NextReader() (OpCode, *MessageReader, error) {
	var remaining int64
	if err := c.nextDataFrame(&remaining); err != nil {
		return 0, nil, err
	}
	r := &c.msgReader
	*r = MessageReader{c: c, op: c.msgOp, remaining: remaining, compressed: c.msgCompressed}
	if r.compressed {
		c.payloadReader.c = c
		inflate, err := c.decompressor.begin(&c.payloadReader)
		if err != nil {
			return 0, nil, err
		}
		r.inflate = inflate
		r.remaining = 0
	}
	return c.msgOp, r, nil
}

// frameOffset returns the number of bytes already read within the current frame, used to continue the masking sequence. | frameOffset 返回当前帧内已读字节数，用于继续掩码序列。
func (c *Conn) frameOffset() int64 {
	if c.frameLen == 0 {
		return 0
	}
	return c.frameLen - c.frameRemain
}

// nextDataFrame advances to the next data frame (skipping and handling control frames) and returns that frame's remaining payload length. | nextDataFrame 推进到下一个数据帧（跳过并处理控制帧），返回该帧剩余载荷长度。
func (c *Conn) nextDataFrame(remaining *int64) error {
	if c.readErr != nil {
		return c.readErr
	}
	for {
		h, err := readFrameHeader(c.reader(), c.headerBuf[:])
		if err != nil {
			switch {
			case errors.Is(err, ErrFrameLengthMSB):
				// an invalid length field is a protocol error, so answer 1002; an intact but oversized length stays 1009 below | 长度字段非法属于协议错误，应答 1002；长度合法但超限则在下面回 1009
				return c.fail(CloseProtocolError, err)
			case err == io.EOF || err == io.ErrUnexpectedEOF:
				return c.setReadErr(ErrUnexpectedEOF)
			default:
				return c.setReadErr(err)
			}
		}
		if err := c.validateHeader(h); err != nil {
			return err
		}

		if h.opcode.IsControl() {
			if err := c.handleControl(h); err != nil {
				return err
			}
			continue
		}

		switch h.opcode {
		case TextMessage, BinaryMessage:
			if c.msgOp != 0 {
				return c.fail(CloseProtocolError, ErrNewDataDuringFragment)
			}
			c.msgOp = h.opcode
			// RSV1 marks a compressed message and is only legal on the first frame of one (RFC 7692 §6.1). | RSV1 标记压缩消息，且只允许出现在消息首帧（RFC 7692 §6.1）。
			c.msgCompressed = h.rsv1
			c.msgFirstFrame = true
			c.utf8.reset()
		case ContinuationMessage:
			if c.msgOp == 0 {
				return c.fail(CloseProtocolError, ErrContinuationWithoutStart)
			}
			c.msgFirstFrame = false
		}

		c.frameLen = h.length
		c.frameRemain = h.length
		c.frameFin = h.fin
		c.frameMasked = h.masked
		c.maskKey = h.maskKey

		if h.length > c.opts.MaxMessageSize-c.msgLen {
			// subtraction avoids int64 overflow when a legal 64-bit length is near the maximum | 相减可避免合法 64 位长度接近最大值时的 int64 溢出
			return c.fail(CloseMessageTooBig, ErrMessageTooBig)
		}
		*remaining = h.length
		return nil
	}
}

// reader returns the frame header read source. | reader 返回帧头读取源。
func (c *Conn) reader() io.Reader {
	if c.br != nil {
		return c.br
	}
	return c.conn
}

// validateHeader checks whether the frame header complies with RFC 6455. | validateHeader 校验帧头是否符合 RFC 6455。
func (c *Conn) validateHeader(h frameHeader) error {
	if h.rsv2 || h.rsv3 {
		return c.fail(CloseProtocolError, ErrReservedBits)
	}
	if h.rsv1 {
		// RSV1 without negotiation is a protocol error (RFC 6455 §5.2). Even with permessage-deflate | 未协商即置 RSV1 属于协议错误（RFC 6455 §5.2）。即便协商了 permessage-deflate，
		// negotiated it is only legal on the first frame of a data message: control frames are | RSV1 也只允许出现在数据消息的首帧：控制帧永不压缩，
		// never compressed and continuations never carry it (RFC 7692 §6.1). | 续帧也从不携带它（RFC 7692 §6.1）。
		if c.compression == nil || h.opcode.IsControl() || h.opcode == ContinuationMessage {
			return c.fail(CloseProtocolError, ErrReservedBits)
		}
	}
	switch h.opcode {
	case ContinuationMessage, TextMessage, BinaryMessage, CloseMessage, PingMessage, PongMessage:
	default:
		return c.fail(CloseProtocolError, ErrBadOpCode)
	}
	if h.opcode.IsControl() {
		if h.length > maxControlPayload {
			return c.fail(CloseProtocolError, ErrControlTooLarge)
		}
		if !h.fin {
			return c.fail(CloseProtocolError, ErrFragmentedControl)
		}
	}
	// frames from a client must be masked; frames from a server must not be masked (RFC 6455 §5.1) | 客户端发来的帧必须掩码；服务端发来的帧必须不掩码（RFC 6455 §5.1）
	if c.isClient && h.masked {
		return c.fail(CloseProtocolError, ErrMaskMismatch)
	}
	if !c.isClient && !h.masked {
		return c.fail(CloseProtocolError, ErrMaskMismatch)
	}
	return nil
}

// handleControl reads and handles a control frame. | handleControl 读取并处理控制帧。
func (c *Conn) handleControl(h frameHeader) error {
	payload := c.controlBuf[:h.length]
	if h.length > 0 {
		if err := c.readFull(payload); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				err = ErrUnexpectedEOF
			}
			return c.setReadErr(err)
		}
		if h.masked {
			maskBytes(h.maskKey, 0, payload)
		}
	}

	switch h.opcode {
	case PingMessage:
		if c.pingHandler != nil {
			if err := c.pingHandler(string(payload)); err != nil {
				return err
			}
			return nil
		}
		return c.WriteControl(PongMessage, payload, time.Now().Add(closeTimeout))
	case PongMessage:
		if c.pongHandler != nil {
			return c.pongHandler(string(payload))
		}
		return nil
	case CloseMessage:
		return c.handleCloseFrame(payload)
	default:
		return nil
	}
}

// handleCloseFrame handles the peer's close frame: it echoes a close frame back as the RFC requires and returns a *CloseError. | handleCloseFrame 处理对端关闭帧：按 RFC 回送关闭帧并返回 *CloseError。
//
// Once both sides have exchanged Close frames the server closes the underlying TCP connection (RFC 6455 §7.1.1); | 双方交换关闭帧后服务端关闭底层 TCP（RFC 6455 §7.1.1）；
// the *CloseError is still recorded and returned, so callers keep observing it on this and later reads. | *CloseError 仍会被记录并返回，调用方在本条及后续读取中依然能观察到它。
func (c *Conn) handleCloseFrame(payload []byte) error {
	c.closeReceived = true
	code, text, perr := parseClosePayload(payload)
	if perr != nil {
		// follow the parser's own classification: 1002 for an invalid code, 1007 for invalid UTF-8 | 采用解析器自身的分类：非法关闭码回 1002，非法 UTF-8 回 1007
		_ = c.writeClose(code, "")
		c.closeTransport()
		return c.setReadErr(perr)
	}

	closeMu := &c.closeMu
	closeMu.Lock()
	alreadySent := c.closeSent
	closeMu.Unlock()

	if !alreadySent {
		// echo the same status code; when no status code was present, echo an empty payload (1005 must never go on the wire) | 回送相同状态码；未带状态码时回送空载荷（1005 不得上线）
		if code == CloseNoStatusReceived {
			_ = c.writeCloseRaw(nil)
		} else {
			_ = c.writeClose(code, "")
		}
	}
	if c.closeHandler != nil {
		if err := c.closeHandler(code, text); err != nil {
			c.closeTransport()
			return c.setReadErr(err)
		}
	}
	c.closeTransport()
	ce := &CloseError{Code: code, Text: text}
	return c.setReadErr(ce)
}

// closeTransport closes the underlying TCP connection after the close frame has been written. | closeTransport 在关闭帧写出后关闭底层 TCP 连接。
//
// RFC 6455 §7.1.1 requires the server to close the underlying TCP connection as soon as the close handshake completes; | RFC 6455 §7.1.1 要求服务端在关闭握手完成后立即关闭底层 TCP；
// the client instead waits for the server's FIN (it MAY close earlier at the caller's discretion). | 客户端则等待服务端的 FIN（也可由调用方提前关闭）。
// The close frame is written synchronously before this call, so the peer reads the status code before the FIN; | 关闭帧在此调用前已同步写出，对端会先读到状态码再收到 FIN；
// closing before the write would surface as a reset and hide the code. | 若在写出之前关闭，对端只会看到 RST 而读不到关闭码。
func (c *Conn) closeTransport() {
	// An HTTP/1.1 client leaves the TCP close to the server, which already sent
	// FIN after the close handshake. A stream has no shared TCP: both sides must
	// close their half, or the request context leaks. | HTTP/1.1 客户端把 TCP 关闭
	// 留给服务端（服务端在关闭握手后已发 FIN）。流没有共享的 TCP：两端都必须关闭自己的
	// 半边，否则请求上下文会泄漏。
	if c.isClient && c.kind == TransportTCP {
		return
	}
	_ = c.conn.Close()
}

// fail records the read error, sends the corresponding close frame, and returns that error. | fail 记录读错误、发送对应关闭帧并返回该错误。
//
// The close frame is written before the transport is closed (server side), so the peer can still read the code. | 关闭帧先于传输层关闭写出（服务端），对端仍能读到关闭码。
func (c *Conn) fail(code CloseCode, err error) error {
	closeMu := &c.closeMu
	closeMu.Lock()
	sent := c.closeSent
	closeMu.Unlock()
	if !sent {
		_ = c.writeClose(code, "")
	}
	c.closeTransport()
	return c.setReadErr(err)
}

// ---------------------------------------------------------------- Writing | 写入

// WriteMessage writes one complete message. | WriteMessage 写入一条完整消息。
//
// Only TextMessage and BinaryMessage are accepted: WriteMessage always emits a single FIN frame, so it cannot | 仅接受 TextMessage 与 BinaryMessage：WriteMessage 总是发出单个 FIN 帧，因此既不能
// continue a fragmented message (a bare continuation would violate RFC 6455 §5.4) and must not emit control | 续接分片消息（无起始帧的续帧违反 RFC 6455 §5.4），也不能发出控制帧
// frames (they have their own API) or reserved opcodes (§5.2). | （控制帧有专用 API）或保留操作码（§5.2）。
//
// A server writes without masking (RFC 6455 §5.1); a client generates a random mask per frame, | 服务端写入不加掩码（RFC 6455 §5.1）；客户端写入会按帧生成随机掩码，
// in which case the payload is copied into an internal masking buffer before it is sent. | 此时载荷需要复制到内部掩码缓冲后再发出。
//
// When permessage-deflate was negotiated and write compression is enabled, the payload is compressed and | 协商了 permessage-deflate 且写压缩开启时，载荷会被压缩并在帧上
// RSV1 is set on the frame (RFC 7692 §6.1); the peer always accepts uncompressed messages too. | 置 RSV1（RFC 7692 §6.1）；对端同样始终接受未压缩消息。
func (c *Conn) WriteMessage(op OpCode, p []byte) error {
	switch op {
	case TextMessage, BinaryMessage:
	case ContinuationMessage:
		return ErrWriteContinuation
	default:
		return ErrWriteMessageOpcode
	}
	if c.closeSentFlag.Load() {
		return ErrCloseSent
	}
	return c.writeMessageFrame(op, p)
}

// writeMessageFrame compresses the payload when permessage-deflate is active and writes it as one FIN frame. | writeMessageFrame 在 permessage-deflate 生效时压缩载荷，并作为单个 FIN 帧写出。
//
// Compression happens under the write lock because the flate writer is shared state; the RFC 7692 | 压缩在写锁内进行，因为 flate 写出器是共享状态；RFC 7692 的
// RSV1 bit is set exactly on this first-and-only frame. | RSV1 位恰好设置在这个唯一（也是首个）帧上。
func (c *Conn) writeMessageFrame(op OpCode, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	rsv1 := false
	body := payload
	if c.compressor != nil && c.writeCompression.Load() {
		compressed, err := c.compressor.compress(payload)
		if err != nil {
			return err
		}
		body = compressed
		rsv1 = true
	}
	return c.writeFrameLocked(rsv1, true, op, body)
}

// WriteControl writes a control frame (close/ping/pong) with an optional write deadline. | WriteControl 写入控制帧（close/ping/pong），可指定写超时。
//
// Only PingMessage, PongMessage and CloseMessage are accepted; reserved control opcodes (0xB-0xF) and data opcodes | 仅接受 PingMessage、PongMessage 与 CloseMessage；保留控制操作码（0xB-0xF）与数据操作码
// have no meaning here (RFC 6455 §5.2/§5.5). A close payload is validated before it goes on the wire: reserved or | 在此没有意义（RFC 6455 §5.2/§5.5）。关闭载荷在写出前会被校验：保留或未分配
// unassigned status codes and non-UTF-8 reasons are rejected with ErrInvalidCloseCode / ErrInvalidCloseReason. | 状态码与非 UTF-8 原因会以 ErrInvalidCloseCode / ErrInvalidCloseReason 拒绝。
//
// Writing a close frame records the "close sent" state, after which any data frame write returns ErrCloseSent, | 写入关闭帧会登记「已发送关闭」状态，此后任何数据帧写入都返回 ErrCloseSent，
// and sending a close frame again likewise returns ErrCloseSent (RFC 6455 §5.5.1: a close frame is sent at most once). | 重复发送关闭帧同样返回 ErrCloseSent（RFC 6455 §5.5.1：关闭帧至多发送一次）。
func (c *Conn) WriteControl(op OpCode, p []byte, deadline time.Time) error {
	switch op {
	case PingMessage, PongMessage, CloseMessage:
	default:
		return ErrWriteControlOpcode
	}
	if len(p) > maxControlPayload {
		return ErrControlTooLarge
	}
	if op == CloseMessage {
		if err := validateOutboundClosePayload(p); err != nil {
			return err
		}
		c.closeMu.Lock()
		if c.closeSent {
			c.closeMu.Unlock()
			return ErrCloseSent
		}
		c.closeSent = true
		c.closeSentFlag.Store(true)
		c.closeMu.Unlock()
	}
	return c.writeControlFrame(op, p, deadline)
}

// writeControlFrame writes a control frame directly (without touching the close state bookkeeping). | writeControlFrame 直接写出控制帧（不涉及关闭状态登记）。
func (c *Conn) writeControlFrame(op OpCode, p []byte, deadline time.Time) error {
	if !deadline.IsZero() {
		if err := c.conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer func() { _ = c.conn.SetWriteDeadline(time.Time{}) }()
	}
	return c.writeFrame(true, op, p)
}

// writeFrame assembles and sends a single uncompressed frame. | writeFrame 组装并发送单个未压缩帧。
func (c *Conn) writeFrame(fin bool, op OpCode, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.writeFrameLocked(false, fin, op, payload)
}

// writeFrameLocked assembles and sends a single frame while the caller already holds c.writeMu. | writeFrameLocked 在调用方已持有 c.writeMu 时组装并发送单个帧。
//
// rsv1 is only ever true for the first frame of a compressed data message; control frames must pass false. | rsv1 仅在压缩数据消息的首帧为 true；控制帧必须传 false。
func (c *Conn) writeFrameLocked(rsv1, fin bool, op OpCode, payload []byte) error {
	// The whole frame — header, mask key and (for clients) the shared mask
	// buffer — must be built **inside** the write lock. Building the masked
	// copy before locking would let two concurrent writers race on c.maskBuf,
	// which the documented "concurrent writers are serialized" contract
	// forbids. | 整个帧（帧头、掩码键，以及客户端共用的掩码缓冲）都必须在写锁**内**
	// 构造：若在加锁前构造掩码副本，两个并发写协程会竞争 c.maskBuf，违背
	// 「并发写被串行化」的契约。
	hdr := c.writeHeaderBuf[:]

	n := writeFrameHeader(hdr, fin, op, c.isClient, int64(len(payload)))
	if rsv1 {
		// writeFrameHeader has no extension knowledge by design (its signature is part of the | writeFrameHeader 按设计不感知扩展（其签名属于
		// tested frame layer), so the RSV1 bit is ORed in here. | 受测帧层接口），因此 RSV1 位在此处按位或入。
		hdr[0] |= 0x40
	}
	body := payload
	if c.isClient {
		// a client must mask; masking requires an in-place XOR, so the payload is copied into an internal buffer | 客户端必须掩码；掩码需要就地异或，因此复制到内部缓冲
		if cap(c.maskBuf) < len(payload) {
			c.maskBuf = make([]byte, len(payload))
		}
		body = c.maskBuf[:len(payload)]
		copy(body, payload)
		var key [4]byte
		if _, err := rand.Read(key[:]); err != nil {
			return err
		}
		copy(hdr[n:], key[:])
		n += 4
		maskBytes(key, 0, body)
	}

	// Small frames take the buffered path: header, mask key and payload are
	// coalesced into one transport write. Large frames keep the scatter list,
	// which sends both buffers without copying the payload. | 小帧走缓冲路径：
	// 帧头、掩码键与载荷合并为一次传输层写入；大帧仍用分散列表，不复制载荷。
	total := n + len(body)
	if total <= c.bw.Available() {
		if _, err := c.bw.Write(hdr[:n]); err != nil {
			return err
		}
		if _, err := c.bw.Write(body); err != nil {
			return err
		}
		if err := c.bw.Flush(); err != nil {
			return err
		}
	} else {
		// The scatter list must not follow buffered bytes on the wire. | 分散列表不能排到缓冲字节之前上线。
		if err := c.bw.Flush(); err != nil {
			return err
		}
		// The connection-owned scatter list keeps the per-frame buffer list off the heap. WriteTo
		// consumes the slice it is given, so it receives the writeList copy while writeBase keeps the
		// stable two-element view for the next frame. | 连接自有的分散列表使每帧的缓冲列表不再上堆。
		// WriteTo 会消费传入的切片，因此交给它的是 writeList 副本，而 writeBase 保留稳定的两元素视图
		// 供下一帧复用。
		if cap(c.writeBase) < 2 {
			bufs := make(net.Buffers, 2)
			c.writeBase, c.writeList = bufs, bufs
		}
		c.writeBase = c.writeBase[:2]
		c.writeBase[0] = hdr[:n]
		c.writeBase[1] = body
		c.writeList = c.writeBase
		if _, err := c.writeList.WriteTo(c.conn); err != nil {
			return err
		}
	}
	// Stream transports buffer DATA until flushed; TCP never implements
	// FlushError, so its hot path is unchanged. | 流传输会把 DATA 缓冲到刷新为止；
	// TCP 从不实现 FlushError，因此其热路径保持不变。
	if f, ok := c.conn.(interface{ FlushError() error }); ok {
		return f.FlushError()
	}
	return nil
}

// writeClose sends a close frame and marks it as sent. | writeClose 发送关闭帧并标记已发送。
func (c *Conn) writeClose(code CloseCode, reason string) error {
	return c.writeCloseRaw(closePayload(code, reason))
}

func (c *Conn) writeCloseRaw(payload []byte) error {
	c.closeMu.Lock()
	if c.closeSent {
		c.closeMu.Unlock()
		return ErrCloseSent
	}
	c.closeSent = true
	c.closeSentFlag.Store(true)
	c.closeMu.Unlock()
	return c.writeControlFrame(CloseMessage, payload, time.Now().Add(closeTimeout))
}

// Close shuts down gracefully: it leaves the tracker, sends a close frame and then closes the underlying connection. | Close 优雅关闭：先离开追踪表，发送关闭帧后关闭底层连接。
//
// The status code and reason are validated first: reserved/unassigned codes and non-UTF-8 reasons are never | 状态码与原因会先被校验：保留/未分配状态码与非 UTF-8 原因绝不会写上线，
// written; the connection is still terminated with a status-less close frame and the validation error is returned. | 连接仍会以无状态码关闭帧终止，并返回校验错误。
//
// Untracking happens first so that a concurrent CloseAll never sees (and never
// writes to) a connection that is already on its way out. | 先取消登记，
// 使并发的 CloseAll 不会看到（也不会写入）已经在关闭途中的连接。
func (c *Conn) Close(code CloseCode, reason string) error {
	c.detach()
	verr := validateOutboundClose(code, reason)
	var err error
	if verr != nil {
		err = c.writeCloseRaw(nil)
	} else {
		err = c.writeClose(code, reason)
	}
	closeErr := c.conn.Close()
	if verr != nil {
		return verr
	}
	if err != nil && err != ErrCloseSent {
		return err
	}
	return closeErr
}

// CloseNow leaves the tracker and closes the underlying TCP connection immediately, without sending a close frame. | CloseNow 离开追踪表并立即关闭底层 TCP 连接，不发送关闭帧。
func (c *Conn) CloseNow() error {
	c.detach()
	c.closeMu.Lock()
	c.closeSent = true
	c.closeSentFlag.Store(true)
	c.closeMu.Unlock()
	return c.conn.Close()
}

// encodeMaskKey is used only by tests to assert the mask key format. | encodeMaskKey 仅用于测试断言掩码键格式。
func encodeMaskKey(k [4]byte) uint32 { return binary.BigEndian.Uint32(k[:]) }
