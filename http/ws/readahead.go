package ws

import "sync"

// readAheadSlotLimit is the largest message copied into a read-ahead slot.
// Larger messages are handed over without a copy and forfeit the pre-armed
// read for that round, which keeps the per-connection slot memory bounded. | readAheadSlotLimit
// 是会复制进预读槽的最大消息；更大的消息直接移交而不复制，并放弃该轮的预挂起读，
// 从而把每连接的槽内存限制在可控范围。
const readAheadSlotLimit = 1 << 20

// readAheadResult is one message handed from the reader goroutine to ReadMessage. | readAheadResult
// 是从读协程交给 ReadMessage 的一条消息。
type readAheadResult struct {
	err      error
	payload  []byte
	op       OpCode
	borrowed bool // payload aliases the connection read buffer and must be released before the next read | payload 指向连接读缓冲，下次读取前必须释放
}

// readAhead holds the state of a ReadAhead-enabled connection. | readAhead 保存启用预读的连接状态。
//
// One goroutine reads messages and keeps at most one result ready; the next read
// is already pending while ReadMessage's caller processes the current message.
// That removes the syscall-entry gap between a write and the following read,
// which dominates request-response latency on Linux. | 一个协程读取消息并最多保持一条
// 就绪结果；当 ReadMessage 的调用方处理当前消息时，下一次读取已经挂起。这消除了写与随后
// 读取之间的系统调用入口空档，而该空档在 Linux 上主导请求-响应延迟。
type readAhead struct {
	err error

	ready    chan readAheadResult
	ack      chan struct{}
	done     chan struct{}
	finished chan struct{}

	// slots are the two copy buffers. The reader fills one while the consumer
	// still holds the other, so a returned payload survives the pre-armed read. | slots
	// 是两个复制缓冲：读协程填充一个时消费者仍持有另一个，因此返回的载荷能挺过预挂起读。
	slots [2][]byte

	doneOnce  sync.Once
	startOnce sync.Once

	mu sync.Mutex
}

// setErr records the terminal read error exactly once. | setErr 记录终止性读错误（只记录一次）。
func (ra *readAhead) setErr(err error) {
	ra.mu.Lock()
	if ra.err == nil {
		ra.err = err
	}
	ra.mu.Unlock()
}

// terminalError returns the recorded error, or ErrUnexpectedEOF when the
// connection was closed locally. | terminalError 返回已记录的错误；本地关闭时返回
// ErrUnexpectedEOF。
func (ra *readAhead) terminalError() error {
	ra.mu.Lock()
	err := ra.err
	ra.mu.Unlock()
	if err != nil {
		return err
	}
	return ErrUnexpectedEOF
}

// enableReadAhead arms the background reader. The goroutine itself starts on
// the first ReadMessage call, so callers can install handlers and deadlines
// before any read begins, exactly as in synchronous mode. | enableReadAhead 装配
// 后台读协程；协程本身在首次 ReadMessage 时启动，因此调用方可以像同步模式一样，
// 在任何读取开始前安装处理器与截止时间。
func (c *Conn) enableReadAhead() {
	if c.ra != nil {
		return
	}
	c.ra = &readAhead{
		ready:    make(chan readAheadResult, 1),
		ack:      make(chan struct{}),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
}

// stopReadAhead wakes the background reader so that Close and CloseNow can
// reclaim it even when it is parked on the ready or ack channel. | stopReadAhead
// 唤醒后台读协程，使 Close 与 CloseNow 即使它停在 ready/ack 通道上也能回收。
func (c *Conn) stopReadAhead() {
	if c.ra == nil {
		return
	}
	c.ra.doneOnce.Do(func() { close(c.ra.done) })
}

// readAheadLoop is the background reader. It exits on a terminal read error or
// when stopReadAhead closes done; finished is closed in every case. | readAheadLoop
// 是后台读协程。遇到终止性读错误或 stopReadAhead 关闭 done 时退出；无论哪种情况都会
// 关闭 finished。
func (c *Conn) readAheadLoop() {
	ra := c.ra
	defer close(ra.finished)
	slot := 0
	for {
		op, payload, err := c.readMessageSync()
		if err != nil {
			ra.setErr(err)
			select {
			case ra.ready <- readAheadResult{err: err}:
			case <-ra.done:
			}
			return
		}
		res := readAheadResult{op: op, payload: payload}
		if len(payload) <= readAheadSlotLimit {
			buf := append(ra.slots[slot][:0], payload...)
			ra.slots[slot] = buf
			res.payload = buf
			slot ^= 1
		} else {
			// A large message stays in the connection read buffer; the consumer
			// releases it at the start of the next ReadMessage. | 大消息保留在连接读
			// 缓冲中；消费者在下次 ReadMessage 开始时释放它。
			res.borrowed = true
		}
		select {
		case ra.ready <- res:
		case <-ra.done:
			return
		}
		select {
		case <-ra.ack:
		case <-ra.done:
			return
		}
	}
}

// readMessageAhead consumes one pre-read result. | readMessageAhead 消费一条预读结果。
func (c *Conn) readMessageAhead() (OpCode, []byte, error) {
	ra := c.ra
	// Do not start a reader for a connection that has already been closed. | 已关闭
	// 的连接不再启动读协程。
	select {
	case <-ra.done:
		return 0, nil, ra.terminalError()
	default:
	}
	ra.startOnce.Do(func() { go c.readAheadLoop() })
	if c.raBorrowed {
		c.raBorrowed = false
		if !c.releaseReadAhead(ra) {
			return 0, nil, ra.terminalError()
		}
	}
	var res readAheadResult
	select {
	case res = <-ra.ready:
	case <-ra.finished:
		return 0, nil, ra.terminalError()
	case <-ra.done:
		// Close/CloseNow is already unwinding the connection; the reader will
		// observe the closed transport and exit on its own. | Close/CloseNow 已在
		// 收尾连接；读协程会观察到传输关闭并自行退出。
		return 0, nil, ra.terminalError()
	}
	if res.err != nil {
		return 0, nil, res.err
	}
	c.raBorrowed = res.borrowed
	if !res.borrowed && !c.releaseReadAhead(ra) {
		// The reader is gone, but the copied payload is still valid; the next
		// call reports the terminating error. | 读协程已退出，但复制出的载荷仍有效；
		// 下次调用会报告终止错误。
	}
	return res.op, res.payload, nil
}

// releaseReadAhead acknowledges a consumed result so the reader can arm the next
// read. It reports false when the reader has already exited. | releaseReadAhead
// 确认已消费一条结果，使读协程可以挂起下一次读取；读协程已退出时返回 false。
func (c *Conn) releaseReadAhead(ra *readAhead) bool {
	select {
	case ra.ack <- struct{}{}:
		return true
	case <-ra.finished:
		return false
	case <-ra.done:
		return false
	}
}
