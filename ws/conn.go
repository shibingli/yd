package ws

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"
)

// Conn is an established WebSocket connection. | Conn 是一条已建立的 WebSocket 连接。
//
// Concurrency contract (consistent with RFC 6455 and mainstream implementations): | 并发约定（与 RFC 6455 及主流实现一致）：
//   - **one** reader goroutine and **one** writer goroutine may run concurrently; |   - 允许**一个**读协程与**一个**写协程并发；
//   - concurrent calls to the write methods from several writer goroutines are serialized by an internal mutex; |   - 多个写协程并发调用写方法由内部互斥锁串行化；
//   - control frames (ping/pong/close) are answered automatically by the read path, with no caller involvement needed. |   - 控制帧（ping/pong/close）由读路径自动应答，无需调用方干预。
//
// Zero-copy contract: when writing a data frame the header goes into a stack array and is sent together with the payload | 零拷贝约定：写数据帧时帧头写在栈上数组，与载荷一起通过 net.Buffers
// in a single writev through net.Buffers, **without copying the payload or passing through an intermediate buffer**. The read side reads | 以一次 writev 发出，**不复制载荷、不经中间缓冲**。读侧默认直接读
// net.Conn directly by default (no bufio layer), and the slice returned by ReadMessage points into the connection's internal reuse buffer; | net.Conn（无 bufio 中间层），ReadMessage 返回的切片指向连接内部复用缓冲；
// that slice is valid **until the next read operation**; copy it yourself if you need to hold it longer. | 该切片在**下一次读操作前**有效，需要长期持有请自行复制。
type Conn struct {
	conn net.Conn

	readErr error
	br      *bufio.Reader // non-nil only when ReadBufferSize > 0 | 仅在 ReadBufferSize > 0 时非空

	pingHandler  func(string) error
	pongHandler  func(string) error
	closeHandler func(CloseCode, string) error
	untrack      func()

	subprotocol string

	readBuf []byte
	maskBuf []byte

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

	maskKey  [4]byte
	isClient bool

	msgOp         OpCode // type of the current fragmented message; 0 means no message in progress | 当前分片消息的类型，0 表示无进行中消息
	frameFin      bool
	frameMasked   bool
	closeSent     bool
	closeReceived bool
}

// newConn constructs the connection object. | newConn 构造连接对象。
func newConn(nc net.Conn, o Options, isClient bool) *Conn {
	c := &Conn{conn: nc, opts: o, isClient: isClient}
	if o.ReadBufferSize > 0 {
		c.br = bufio.NewReaderSize(nc, o.ReadBufferSize)
	}
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

// UnderlyingConn returns the underlying TCP connection. | UnderlyingConn 返回底层 TCP 连接。
//
// Use it only when TCP-level options (such as TCP_NODELAY, KeepAlive) really must be set; | 仅在确需设置 TCP 层选项（如 TCP_NODELAY、KeepAlive）时使用；
// reading or writing it directly would break frame boundaries. | 直接读写会破坏帧边界。
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

// EnableWriteCompression is a reserved interface: this implementation does not enable permessage-deflate, | EnableWriteCompression 保留接口：本实现未启用 permessage-deflate，
// so the call is a no-op (RFC 7692 forbids setting RSV1 without negotiation). | 调用为无操作（RFC 7692 未协商即不得设置 RSV1）。
func (c *Conn) EnableWriteCompression(bool) {}

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
	buf := c.readBuf
	for {
		if int64(cap(buf))-int64(len(buf)) < 512 {
			grown := make([]byte, len(buf), (len(buf)+512)*2)
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
type MessageReader struct {
	c         *Conn
	remaining int64
	op        OpCode
	done      bool
}

// OpCode returns the type of this message. | OpCode 返回该消息的类型。
func (r *MessageReader) OpCode() OpCode { return r.op }

// Read implements io.Reader; it returns io.EOF when the message ends. | Read 实现 io.Reader；消息结束时返回 io.EOF。
//
// Reading switches the masking key and resets the masking offset automatically at frame boundaries (RFC 6455 §5.3). | 读取会在帧边界处自动切换掩码键并重置掩码偏移（RFC 6455 §5.3）。
func (r *MessageReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
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

// reader returns the read source for the current frame payload (bufio or the raw connection). | reader 返回当前帧载荷的读取源（bufio 或裸连接）。
func (r *MessageReader) reader() io.Reader {
	if r.c.br != nil {
		return r.c.br
	}
	return r.c.conn
}

// NextReader starts reading the next message and returns its type along with a streaming reader. | NextReader 开始读取下一条消息，返回消息类型与流式读取器。
func (c *Conn) NextReader() (OpCode, *MessageReader, error) {
	var remaining int64
	if err := c.nextDataFrame(&remaining); err != nil {
		return 0, nil, err
	}
	r := &MessageReader{c: c, op: c.msgOp, remaining: remaining}
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
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				err = ErrUnexpectedEOF
			}
			return c.setReadErr(err)
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
			c.utf8.reset()
		case ContinuationMessage:
			if c.msgOp == 0 {
				return c.fail(CloseProtocolError, ErrContinuationWithoutStart)
			}
		}

		c.frameLen = h.length
		c.frameRemain = h.length
		c.frameFin = h.fin
		c.frameMasked = h.masked
		c.maskKey = h.maskKey

		if c.msgLen+h.length > c.opts.MaxMessageSize {
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
	if h.rsv1 || h.rsv2 || h.rsv3 {
		return c.fail(CloseProtocolError, ErrReservedBits)
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
	payload := make([]byte, h.length)
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
func (c *Conn) handleCloseFrame(payload []byte) error {
	c.closeReceived = true
	code, text, perr := parseClosePayload(payload)
	if perr != nil {
		_ = c.writeClose(CloseProtocolError, "")
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
			return c.setReadErr(err)
		}
	}
	ce := &CloseError{Code: code, Text: text}
	return c.setReadErr(ce)
}

// fail records the read error, sends the corresponding close frame, and returns that error. | fail 记录读错误、发送对应关闭帧并返回该错误。
func (c *Conn) fail(code CloseCode, err error) error {
	closeMu := &c.closeMu
	closeMu.Lock()
	sent := c.closeSent
	closeMu.Unlock()
	if !sent {
		_ = c.writeClose(code, "")
	}
	return c.setReadErr(err)
}

// ---------------------------------------------------------------- Writing | 写入

// WriteMessage writes one complete message. | WriteMessage 写入一条完整消息。
//
// A server writes without masking (RFC 6455 §5.1); a client generates a random mask per frame, | 服务端写入不加掩码（RFC 6455 §5.1）；客户端写入会按帧生成随机掩码，
// in which case the payload is copied into an internal masking buffer before it is sent. | 此时载荷需要复制到内部掩码缓冲后再发出。
func (c *Conn) WriteMessage(op OpCode, p []byte) error {
	if op.IsControl() {
		return ErrProtocol
	}
	c.closeMu.Lock()
	sent := c.closeSent
	c.closeMu.Unlock()
	if sent {
		return ErrCloseSent
	}
	return c.writeFrame(true, op, p)
}

// WriteControl writes a control frame (close/ping/pong) with an optional write deadline. | WriteControl 写入控制帧（close/ping/pong），可指定写超时。
//
// Writing a close frame records the "close sent" state, after which any data frame write returns ErrCloseSent, | 写入关闭帧会登记「已发送关闭」状态，此后任何数据帧写入都返回 ErrCloseSent，
// and sending a close frame again likewise returns ErrCloseSent (RFC 6455 §5.5.1: a close frame is sent at most once). | 重复发送关闭帧同样返回 ErrCloseSent（RFC 6455 §5.5.1：关闭帧至多发送一次）。
func (c *Conn) WriteControl(op OpCode, p []byte, deadline time.Time) error {
	if !op.IsControl() {
		return ErrProtocol
	}
	if len(p) > maxControlPayload {
		return ErrControlTooLarge
	}
	if op == CloseMessage {
		c.closeMu.Lock()
		if c.closeSent {
			c.closeMu.Unlock()
			return ErrCloseSent
		}
		c.closeSent = true
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

// writeFrame assembles and sends a single frame. | writeFrame 组装并发送单个帧。
func (c *Conn) writeFrame(fin bool, op OpCode, payload []byte) error {
	// The whole frame — header, mask key and (for clients) the shared mask
	// buffer — must be built **inside** the write lock. Building the masked
	// copy before locking would let two concurrent writers race on c.maskBuf,
	// which the documented "concurrent writers are serialized" contract
	// forbids. | 整个帧（帧头、掩码键，以及客户端共用的掩码缓冲）都必须在写锁**内**
	// 构造：若在加锁前构造掩码副本，两个并发写协程会竞争 c.maskBuf，违背
	// 「并发写被串行化」的契约。
	var hdr [maxHeaderSize]byte

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	n := writeFrameHeader(hdr[:], fin, op, c.isClient, int64(len(payload)))
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

	bufs := net.Buffers{hdr[:n], body}
	if _, err := bufs.WriteTo(c.conn); err != nil {
		return err
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
	c.closeMu.Unlock()
	return c.writeControlFrame(CloseMessage, payload, time.Now().Add(closeTimeout))
}

// Close shuts down gracefully: it leaves the tracker, sends a close frame and then closes the underlying connection. | Close 优雅关闭：先离开追踪表，发送关闭帧后关闭底层连接。
//
// Untracking happens first so that a concurrent CloseAll never sees (and never
// writes to) a connection that is already on its way out. | 先取消登记，
// 使并发的 CloseAll 不会看到（也不会写入）已经在关闭途中的连接。
func (c *Conn) Close(code CloseCode, reason string) error {
	c.detach()
	err := c.writeClose(code, reason)
	closeErr := c.conn.Close()
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
	c.closeMu.Unlock()
	return c.conn.Close()
}

// encodeMaskKey is used only by tests to assert the mask key format. | encodeMaskKey 仅用于测试断言掩码键格式。
func encodeMaskKey(k [4]byte) uint32 { return binary.BigEndian.Uint32(k[:]) }
