package ws

import (
	"sync"
)

// Tracker tracks live connections so that an external lifecycle owner can
// terminate every one of them in a single call. | Tracker 追踪活跃连接，
// 使外部生命周期持有者能一次性终止全部连接。
//
// The interface is deliberately defined here (rather than importing the
// framework's lifecycle package) so that ws keeps no dependency on the
// shutdown registry: the registry implements this contract, not the other way
// round. | 该接口刻意定义在此处（而不是引入框架的 lifecycle 包），使 ws 不依赖关闭注册表：
// 是注册表实现本契约，而不是反过来。
type Tracker interface {
	// Track registers a live connection and returns an idempotent untrack
	// function; the connection calls it automatically from Close / CloseNow. | Track 登记一条活跃连接
	// 并返回幂等的取消登记函数；连接会在 Close / CloseNow 中自动调用它。
	Track(c *Conn) (untrack func())
	// CloseAll terminates every tracked connection with the given close code
	// and reason, sending a close frame before closing the socket. | CloseAll 以指定的关闭码与原因
	// 终止全部已登记连接，并在关闭套接字之前发出关闭帧。
	CloseAll(code CloseCode, reason string)
}

// ConnSet is the default in-process Tracker. | ConnSet 是默认的进程内 Tracker。
//
// Every method is safe for concurrent use; CloseAll walks a snapshot and never
// holds the lock while a connection is being written to, so a slow peer cannot
// block the shutdown of the remaining connections. | 所有方法均可安全并发使用；
// CloseAll 遍历快照，且在不写连接时绝不持锁，因此慢对端无法阻塞其余连接的关闭。
type ConnSet struct {
	conns map[*Conn]struct{}
	mu    sync.Mutex
}

// NewConnSet creates an empty connection set. | NewConnSet 创建一个空连接集合。
func NewConnSet() *ConnSet {
	return &ConnSet{conns: make(map[*Conn]struct{}, 8)}
}

// Track implements Tracker. | Track 实现 Tracker。
//
// A nil connection is ignored, and the returned untrack function is idempotent. | nil 连接会被忽略，返回的取消登记函数是幂等的。
func (s *ConnSet) Track(c *Conn) (untrack func()) {
	if c == nil {
		return func() {}
	}

	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.conns, c)
			s.mu.Unlock()
		})
	}
}

// CloseAll implements Tracker. | CloseAll 实现 Tracker。
//
// Every connection leaves the set before its close frame is written, so a
// connection that is already closing is never closed twice by this call, and
// the set cannot leak entries while the loop runs. | 每条连接在写出关闭帧之前先离开集合，
// 因此正在关闭的连接不会被本调用重复关闭，循环期间集合也不会泄漏条目。
func (s *ConnSet) CloseAll(code CloseCode, reason string) {
	s.mu.Lock()
	conns := make([]*Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	clear(s.conns)
	s.mu.Unlock()

	for _, c := range conns {
		// Close removes the connection from the set (now a no-op) and then sends the close frame. | Close 会把连接从集合中移除（此时已是无操作），再发送关闭帧。
		_ = c.Close(code, reason)
	}
}

// Len returns the number of tracked connections. | Len 返回已登记的连接数量。
func (s *ConnSet) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}
