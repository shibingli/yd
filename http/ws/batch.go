package ws

// Message is one complete message for Conn.WriteBatch. | Message 是 Conn.WriteBatch 使用的单条完整消息。
type Message struct {
	// Payload is the message body. WriteBatch never retains it after returning,
	// so the caller may reuse the slice immediately. | Payload 为消息体。WriteBatch
	// 返回后不再引用它，调用方可以立即复用该切片。
	Payload []byte
	// Op is TextMessage or BinaryMessage. | Op 为 TextMessage 或 BinaryMessage。
	Op OpCode
}

// validateOutboundDataOpcode checks the opcodes accepted by the message write methods. | validateOutboundDataOpcode
// 校验消息写方法接受的操作码。
func validateOutboundDataOpcode(op OpCode) error {
	switch op {
	case TextMessage, BinaryMessage:
		return nil
	case ContinuationMessage:
		return ErrWriteContinuation
	default:
		return ErrWriteMessageOpcode
	}
}

// WriteMessageBuffered writes one complete message into the connection's write
// buffer **without** flushing it. | WriteMessageBuffered 把一条完整消息写入连接的写缓冲，
// **不**刷新。
//
// It is the pipelining counterpart of WriteMessage: a caller that writes several
// messages in a row can amortise the transport write by appending them and
// calling Flush once. The frame is fully copied into the connection before the
// call returns, so the payload slice is never retained. A frame that does not
// fit in the remaining buffer space is preceded by an automatic flush; a frame
// larger than the whole write buffer is sent with a synchronous scatter write.
// | 它是 WriteMessage 的流水线对应物：连续写多条消息的调用方可以把它们追加进缓冲，
// 最后只调用一次 Flush，从而摊薄传输层写入。帧在调用返回前已完整复制进连接，
// 因此绝不会保留 payload 切片。剩余缓冲空间不足时会先自动刷新；大于整个写缓冲的帧
// 则以同步分散写直接发出。
//
// The error only covers validation, close state and compression; a transport
// failure surfaces from the later Flush. Only TextMessage and BinaryMessage are
// accepted, exactly like WriteMessage. | 返回的错误只覆盖校验、关闭状态与压缩；
// 传输层失败会在之后的 Flush 中返回。与 WriteMessage 一样，仅接受 TextMessage
// 与 BinaryMessage。
func (c *Conn) WriteMessageBuffered(op OpCode, p []byte) error {
	if err := validateOutboundDataOpcode(op); err != nil {
		return err
	}
	if c.closeSentFlag.Load() {
		return ErrCloseSent
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.writeMessageLocked(op, p)
}

// WriteBatch writes several complete messages and flushes them together. | WriteBatch
// 写入多条完整消息并统一刷新。
//
// All opcodes are validated before anything is written, so a batch containing a
// control frame or a reserved opcode sends nothing at all. The messages are then
// appended under one write-lock acquisition and emitted with one Flush; when the
// batch does not fit in the write buffer, complete frames are flushed as needed
// and a large frame may use a synchronous scatter write. Payloads are never
// retained after the call returns. | 所有操作码在写出任何字节之前统一校验，因此含
// 控制帧或保留操作码的批次不会发出任何内容。随后在一次加锁内追加全部消息，最后用
// 一次 Flush 发出；批次超出写缓冲时会按完整帧分批刷新，大帧可走同步分散写。
// 调用返回后不再引用任何载荷切片。
//
// Transport errors leave an unspecified prefix of the batch on the wire; the
// returned error is the first failure. An empty batch is a no-op. | 传输错误发生时，
// 批次可能已有前缀上线；返回值为首个错误。空批次是空操作。
func (c *Conn) WriteBatch(msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	for i := range msgs {
		if err := validateOutboundDataOpcode(msgs[i].Op); err != nil {
			return err
		}
	}
	if c.closeSentFlag.Load() {
		return ErrCloseSent
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	for i := range msgs {
		if err := c.writeMessageLocked(msgs[i].Op, msgs[i].Payload); err != nil {
			return err
		}
	}
	return c.flushLocked()
}

// Flush writes all frames buffered by WriteMessageBuffered or WriteBatch to the
// transport. | Flush 把 WriteMessageBuffered 或 WriteBatch 缓存的全部帧写到传输层。
//
// It is safe to call it from any writer goroutine; like the other write methods
// it is serialized by the connection's write lock. Calling it with nothing
// buffered performs no transport write. | 可以从任意写协程调用；与其它写方法一样由
// 连接的写锁串行化。没有待发数据时调用不会产生传输层写入。
func (c *Conn) Flush() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.flushLocked()
}

// flushLocked flushes the write buffer while the caller holds writeMu. | flushLocked
// 在调用方持有 writeMu 时刷新写缓冲。
func (c *Conn) flushLocked() error {
	if c.bw.Buffered() == 0 {
		return nil
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}
	if c.flush != nil {
		return c.flush()
	}
	return nil
}

// BufferedRead reports how many bytes have already arrived and are waiting in
// the connection's read buffer. | BufferedRead 报告已经到达并等待在连接读缓冲中的字节数。
//
// The value is a hint for coalescing output: an echo loop can append the reply
// with WriteMessageBuffered and defer Flush while BufferedRead is positive,
// because at least one more message is already available locally. It must only
// be called by the connection's single reader goroutine; bufio.Reader is not
// safe for concurrent use. Zero is returned on transports without a read
// buffer. | 该值是合并输出的提示：回显循环可以用 WriteMessageBuffered 追加应答，并在
// BufferedRead 为正时推迟 Flush，因为本地至少还有一条消息待处理。它只能由连接唯一的
// 读协程调用；bufio.Reader 不支持并发使用。没有读缓冲的传输始终返回 0。
func (c *Conn) BufferedRead() int {
	if c.br == nil {
		return 0
	}
	return c.br.Buffered()
}

// writeMessageLocked compresses the payload when needed and appends one FIN
// frame without flushing. The caller must hold writeMu. | writeMessageLocked
// 在需要时压缩载荷并追加一个 FIN 帧而不刷新。调用方必须持有 writeMu。
func (c *Conn) writeMessageLocked(op OpCode, payload []byte) error {
	body, rsv1, err := c.compressOutboundLocked(payload)
	if err != nil {
		return err
	}
	return c.appendFrameLocked(rsv1, true, op, body)
}
