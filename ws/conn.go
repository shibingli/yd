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

// Conn 是一条已建立的 WebSocket 连接。
//
// 并发约定（与 RFC 6455 及主流实现一致）：
//   - 允许**一个**读协程与**一个**写协程并发；
//   - 多个写协程并发调用写方法由内部互斥锁串行化；
//   - 控制帧（ping/pong/close）由读路径自动应答，无需调用方干预。
//
// 零拷贝约定：写数据帧时帧头写在栈上数组，与载荷一起通过 net.Buffers
// 以一次 writev 发出，**不复制载荷、不经中间缓冲**。读侧默认直接读
// net.Conn（无 bufio 中间层），ReadMessage 返回的切片指向连接内部复用缓冲；
// 该切片在**下一次读操作前**有效，需要长期持有请自行复制。
type Conn struct {
	conn     net.Conn
	br       *bufio.Reader // 仅在 ReadBufferSize > 0 时非空
	opts     Options
	isClient bool

	readBuf   []byte
	maskBuf   []byte
	headerBuf [maxHeaderSize]byte

	msgOp       OpCode // 当前分片消息的类型，0 表示无进行中消息
	msgLen      int64  // 当前消息已累积字节数
	frameLen    int64  // 当前帧载荷总长度
	frameRemain int64  // 当前帧剩余未读字节
	frameFin    bool
	maskKey     [4]byte
	frameMasked bool
	utf8        utf8State

	readErr       error
	closeSent     bool
	closeReceived bool
	closeMu       sync.Mutex

	writeMu sync.Mutex

	pingHandler  func(string) error
	pongHandler  func(string) error
	closeHandler func(CloseCode, string) error

	subprotocol string
}

// newConn 构造连接对象。
func newConn(nc net.Conn, o Options, isClient bool) *Conn {
	c := &Conn{conn: nc, opts: o, isClient: isClient}
	if o.ReadBufferSize > 0 {
		c.br = bufio.NewReaderSize(nc, o.ReadBufferSize)
	}
	return c
}

// ---------------------------------------------------------------- 基本访问

// LocalAddr 返回本地地址。
func (c *Conn) LocalAddr() net.Addr { return c.conn.LocalAddr() }

// RemoteAddr 返回对端地址。
func (c *Conn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// Subprotocol 返回握手协商出的子协议，未协商时为空串。
func (c *Conn) Subprotocol() string { return c.subprotocol }

// UnderlyingConn 返回底层 TCP 连接。
//
// 仅在确需设置 TCP 层选项（如 TCP_NODELAY、KeepAlive）时使用；
// 直接读写会破坏帧边界。
func (c *Conn) UnderlyingConn() net.Conn { return c.conn }

// SetReadDeadline 设置读超时。
func (c *Conn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

// SetWriteDeadline 设置写超时。
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

// SetPingHandler 设置收到 ping 时的回调；返回错误会终止读循环。
// 未设置时内部自动回复 pong。
func (c *Conn) SetPingHandler(h func(string) error) { c.pingHandler = h }

// SetPongHandler 设置收到 pong 时的回调。
func (c *Conn) SetPongHandler(h func(string) error) { c.pongHandler = h }

// SetCloseHandler 设置收到关闭帧时的回调（在回送关闭帧之后调用）。
func (c *Conn) SetCloseHandler(h func(CloseCode, string) error) { c.closeHandler = h }

// EnableWriteCompression 保留接口：本实现未启用 permessage-deflate，
// 调用为无操作（RFC 7692 未协商即不得设置 RSV1）。
func (c *Conn) EnableWriteCompression(bool) {}

// ---------------------------------------------------------------- 读取

// readFull 从连接读满 b，优先走 bufio（如启用）。
func (c *Conn) readFull(b []byte) error {
	if c.br != nil {
		_, err := io.ReadFull(c.br, b)
		return err
	}
	_, err := io.ReadFull(c.conn, b)
	return err
}

// ReadMessage 读取一条完整消息。
//
// 返回的载荷切片指向连接内部复用缓冲，仅在下一次读操作前有效。
// 返回错误为 *CloseError 时表示对端以关闭帧结束连接。
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

// MessageReader 流式读取一条消息；控件帧（ping/pong/close）在底层自动处理。
type MessageReader struct {
	c         *Conn
	op        OpCode
	remaining int64
	done      bool
}

// OpCode 返回该消息的类型。
func (r *MessageReader) OpCode() OpCode { return r.op }

// Read 实现 io.Reader；消息结束时返回 io.EOF。
//
// 读取会在帧边界处自动切换掩码键并重置掩码偏移（RFC 6455 §5.3）。
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
			// 已读出的 n 字节仍返回给调用方，但错误标记为非法载荷
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
		r.c.readErr = final
		return n, final
	}
	return n, nil
}

// reader 返回当前帧载荷的读取源（bufio 或裸连接）。
func (r *MessageReader) reader() io.Reader {
	if r.c.br != nil {
		return r.c.br
	}
	return r.c.conn
}

// NextReader 开始读取下一条消息，返回消息类型与流式读取器。
func (c *Conn) NextReader() (OpCode, *MessageReader, error) {
	var remaining int64
	if err := c.nextDataFrame(&remaining); err != nil {
		return 0, nil, err
	}
	r := &MessageReader{c: c, op: c.msgOp, remaining: remaining}
	return c.msgOp, r, nil
}

// frameOffset 返回当前帧内已读字节数，用于继续掩码序列。
func (c *Conn) frameOffset() int64 {
	if c.frameLen == 0 {
		return 0
	}
	return c.frameLen - c.frameRemain
}

// nextDataFrame 推进到下一个数据帧（跳过并处理控制帧），返回该帧剩余载荷长度。
func (c *Conn) nextDataFrame(remaining *int64) error {
	if c.readErr != nil {
		return c.readErr
	}
	for {
		h, err := readFrameHeader(c.reader(), c.headerBuf[:])
		if err != nil {
			c.readErr = err
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				c.readErr = ErrUnexpectedEOF
			}
			return c.readErr
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

// reader 返回帧头读取源。
func (c *Conn) reader() io.Reader {
	if c.br != nil {
		return c.br
	}
	return c.conn
}

// validateHeader 校验帧头是否符合 RFC 6455。
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
	// 客户端发来的帧必须掩码；服务端发来的帧必须不掩码（RFC 6455 §5.1）
	if c.isClient && h.masked {
		return c.fail(CloseProtocolError, ErrMaskMismatch)
	}
	if !c.isClient && !h.masked {
		return c.fail(CloseProtocolError, ErrMaskMismatch)
	}
	return nil
}

// handleControl 读取并处理控制帧。
func (c *Conn) handleControl(h frameHeader) error {
	payload := make([]byte, h.length)
	if h.length > 0 {
		if err := c.readFull(payload); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				err = ErrUnexpectedEOF
			}
			c.readErr = err
			return err
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

// handleCloseFrame 处理对端关闭帧：按 RFC 回送关闭帧并返回 *CloseError。
func (c *Conn) handleCloseFrame(payload []byte) error {
	c.closeReceived = true
	code, text, perr := parseClosePayload(payload)
	if perr != nil {
		_ = c.writeClose(CloseProtocolError, "")
		c.readErr = perr
		return perr
	}

	closeMu := &c.closeMu
	closeMu.Lock()
	alreadySent := c.closeSent
	closeMu.Unlock()

	if !alreadySent {
		// 回送相同状态码；未带状态码时回送空载荷（1005 不得上线）
		if code == CloseNoStatusReceived {
			_ = c.writeCloseRaw(nil)
		} else {
			_ = c.writeClose(code, "")
		}
	}
	if c.closeHandler != nil {
		if err := c.closeHandler(code, text); err != nil {
			c.readErr = err
			return err
		}
	}
	ce := &CloseError{Code: code, Text: text}
	c.readErr = ce
	return ce
}

// fail 记录读错误、发送对应关闭帧并返回该错误。
func (c *Conn) fail(code CloseCode, err error) error {
	closeMu := &c.closeMu
	closeMu.Lock()
	sent := c.closeSent
	closeMu.Unlock()
	if !sent {
		_ = c.writeClose(code, "")
	}
	c.readErr = err
	return err
}

// ---------------------------------------------------------------- 写入

// WriteMessage 写入一条完整消息。
//
// 服务端写入不加掩码（RFC 6455 §5.1）；客户端写入会按帧生成随机掩码，
// 此时载荷需要复制到内部掩码缓冲后再发出。
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

// WriteControl 写入控制帧（close/ping/pong），可指定写超时。
//
// 写入关闭帧会登记「已发送关闭」状态，此后任何数据帧写入都返回 ErrCloseSent，
// 重复发送关闭帧同样返回 ErrCloseSent（RFC 6455 §5.5.1：关闭帧至多发送一次）。
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

// writeControlFrame 直接写出控制帧（不涉及关闭状态登记）。
func (c *Conn) writeControlFrame(op OpCode, p []byte, deadline time.Time) error {
	if !deadline.IsZero() {
		if err := c.conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer func() { _ = c.conn.SetWriteDeadline(time.Time{}) }()
	}
	return c.writeFrame(true, op, p)
}

// writeFrame 组装并发送单个帧。
func (c *Conn) writeFrame(fin bool, op OpCode, payload []byte) error {
	var hdr [maxHeaderSize]byte
	n := writeFrameHeader(hdr[:], fin, op, c.isClient, int64(len(payload)))
	body := payload
	if c.isClient {
		// 客户端必须掩码；掩码需要就地异或，因此复制到内部缓冲
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

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	bufs := net.Buffers{hdr[:n], body}
	if _, err := bufs.WriteTo(c.conn); err != nil {
		return err
	}
	return nil
}

// writeClose 发送关闭帧并标记已发送。
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

// Close 优雅关闭：发送关闭帧后关闭底层连接。
func (c *Conn) Close(code CloseCode, reason string) error {
	err := c.writeClose(code, reason)
	closeErr := c.conn.Close()
	if err != nil && err != ErrCloseSent {
		return err
	}
	return closeErr
}

// CloseNow 立即关闭底层 TCP 连接，不发送关闭帧。
func (c *Conn) CloseNow() error {
	c.closeMu.Lock()
	c.closeSent = true
	c.closeMu.Unlock()
	return c.conn.Close()
}

// encodeMaskKey 仅用于测试断言掩码键格式。
func encodeMaskKey(k [4]byte) uint32 { return binary.BigEndian.Uint32(k[:]) }
