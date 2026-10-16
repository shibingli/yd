package sse

import "sync"

// shutdownComment is the final comment frame every stream receives during shutdown. | shutdownComment 是退出时每条事件流收到的最终注释帧。
//
// It is a comment (not an event) on purpose: every SSE client ignores comments,
// so the frame proves the stream is still healthy without inventing an event
// type the application never declared. | 它刻意使用注释（而非事件）：
// 所有 SSE 客户端都会忽略注释，因此该帧既证明事件流仍然正常，又不会引入应用从未声明的事件类型。
const shutdownComment = "server shutting down | 服务器正在退出"

// WriterSet tracks live event streams so that one call can terminate them all. | WriterSet 追踪活跃事件流，使一次调用即可终止全部事件流。
//
// It is the SSE counterpart of the WebSocket connection set: the shutdown path
// closes the set before the HTTP server drains, which makes the streaming
// handlers return instead of holding the drain until the grace period expires. | 它对应 WebSocket 的连接集合：
// 退出流程在 HTTP 服务器排空之前关闭该集合，使流式处理器返回，而不是把排空拖到宽限期结束。
// Every method is safe for concurrent use. | 所有方法均可安全并发使用。
type WriterSet struct {
	broker  *Broker
	writers map[*Writer]struct{}
	mu      sync.Mutex
}

// NewWriterSet creates an empty writer set. | NewWriterSet 创建一个空写入器集合。
//
// b may be nil when the streams are not fed by a Broker; a non-nil broker is
// closed by Shutdown so that subscriber goroutines stop hanging as well. | 事件流不由 Broker 供给时 b 可为 nil；
// b 非 nil 时 Shutdown 会一并关闭它，避免订阅协程继续悬挂。
func NewWriterSet(b *Broker) *WriterSet {
	return &WriterSet{broker: b, writers: make(map[*Writer]struct{}, 8)}
}

// Track registers w and returns an idempotent untrack function. | Track 登记 w 并返回幂等的取消登记函数。
//
// NewWriter calls it automatically when Options.Tracker is set, and the writer
// calls the returned function from Close, so the set never leaks entries. | Options.Tracker 已设置时
// NewWriter 会自动调用它，写入器会在 Close 中调用返回的函数，因此集合不会泄漏条目。
func (s *WriterSet) Track(w *Writer) (untrack func()) {
	if w == nil {
		return func() {}
	}

	s.mu.Lock()
	s.writers[w] = struct{}{}
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.writers, w)
			s.mu.Unlock()
		})
	}
}

// Len returns the number of tracked writers. | Len 返回已登记的写入器数量。
func (s *WriterSet) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.writers)
}

// Shutdown terminates every tracked stream and closes the attached broker. | Shutdown 终止全部已追踪事件流并关闭关联的广播器。
//
// Each stream stops its heartbeat, receives one final comment frame (which
// flushes immediately), and is closed. | 每条事件流先停止心跳，收到一条最终注释帧（会立即 flush），随后关闭。
// Closing the broker afterwards releases every subscription channel, which is
// what lets the streaming handlers return. | 随后关闭广播器会释放全部订阅通道，这正是流式处理器得以返回的原因。
//
// Shutdown is idempotent; it walks a snapshot without holding the lock, so a
// stall on one peer cannot delay the remaining streams. | Shutdown 幂等；
// 它遍历快照且不持锁，因此单个对端卡住不会拖慢其余事件流。
func (s *WriterSet) Shutdown() {
	s.mu.Lock()
	writers := make([]*Writer, 0, len(s.writers))
	for w := range s.writers {
		writers = append(writers, w)
	}
	clear(s.writers)
	s.mu.Unlock()

	for _, w := range writers {
		w.stopHeartbeat()
		// 注释帧会被立即 flush，保证客户端在连接关闭前看到收尾信号。| The comment frame is flushed at once, so the client sees the wind-down signal before the socket closes.
		_ = w.Comment(shutdownComment)
		_ = w.Close()
	}

	if s.broker != nil {
		s.broker.Close()
	}
}
