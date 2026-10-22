package cachex

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/shibingli/yd/core/logx"
)

// Op identifies the kind of a cache event. | Op 标识缓存事件的类型。
type Op uint8

// Event operations. | 事件操作。
const (
	// OpSet means one or more keys were written. | OpSet 表示一个或多个键被写入。
	OpSet Op = iota + 1
	// OpDelete means one or more keys were deleted. | OpDelete 表示一个或多个键被删除。
	OpDelete
	// OpClear means an entire namespace (or the whole cache when Namespace is empty) was cleared. | OpClear 表示清空了整个命名空间（Namespace 为空时表示整个缓存）。
	OpClear
	// OpInvalidate means a table generation advanced; consumers decide what to drop. | OpInvalidate 表示表代际推进，由消费者决定丢弃什么。
	OpInvalidate
	// OpResync means local state must be discarded after a subscription gap. | OpResync 表示订阅出现缺口后必须丢弃本地状态。
	OpResync
)

// Event is one cache lifecycle notification. | Event 是一条缓存生命周期通知。
//
// Keys are logical keys without the deployment prefix; Set and Delete events carry them | Keys 是不含部署前缀的逻辑键；Set 与 Delete 事件携带它们，
// so every node can delete its own L1 entry. Tables is used by the GORM plugin. | 使每个节点都能删除自己的 L1 条目。Tables 由 GORM 插件使用。
type Event struct {
	// At is the publish time in UTC. | At 是发布时间的 UTC 值。
	At time.Time `json:"at,omitempty"`
	// Namespace is the cache namespace; empty with OpClear means every namespace. | Namespace 是缓存命名空间；与 OpClear 搭配时为空表示所有命名空间。
	Namespace string `json:"ns,omitempty"`
	// Source identifies the publishing process. | Source 标识发布进程。
	Source string `json:"src,omitempty"`
	// Keys are the affected logical keys. | Keys 是受影响的逻辑键。
	Keys []string `json:"keys,omitempty"`
	// Tables are the affected database tables (OpInvalidate). | Tables 是受影响的数据库表（OpInvalidate）。
	Tables []string `json:"tables,omitempty"`
	// Version is a monotonically increasing generation for OpInvalidate. | Version 是 OpInvalidate 的单调递增代际。
	Version uint64 `json:"ver,omitempty"`
	// Op is the operation kind. | Op 是操作类型。
	Op Op `json:"op"`
}

// EventHandler receives cache events in publish order. | EventHandler 按发布顺序接收缓存事件。
//
// Handlers run on a single dispatcher goroutine: a slow handler delays later events but | 处理器在单个调度协程上运行：慢处理器会延迟后续事件，但
// never blocks the cache read or write path. A panicking handler is recovered and logged. | 永远不会阻塞缓存的读写路径。发生 panic 的处理器会被恢复并记录日志。
// A handler must return promptly and must not call Manager.Close: Close waits for this | 处理器必须尽快返回，且不得调用 Manager.Close：Close 等待的正是
// dispatcher, so closing from inside a handler deadlocks. | 该调度协程，从处理器内部关闭会造成死锁。
type EventHandler func(Event)

// eventBus fans events out to subscribers without blocking the hot path. | eventBus 在不阻塞热路径的前提下把事件分发给订阅者。
type eventBus struct {
	log      *logx.Logger
	queue    chan Event
	stop     chan struct{}
	done     chan struct{}
	handlers map[uint64]EventHandler
	mu       sync.RWMutex
	next     atomic.Uint64
	closed   atomic.Bool
	once     sync.Once
}

// newEventBus builds a bus with a buffered queue. | newEventBus 构造带缓冲队列的事件总线。
func newEventBus(size int, log *logx.Logger) *eventBus {
	if size <= 0 {
		size = DefaultEventBuffer
	}
	b := &eventBus{
		log:      log,
		queue:    make(chan Event, size),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		handlers: make(map[uint64]EventHandler),
	}
	go b.dispatch()
	return b
}

// subscribe registers fn and returns an idempotent unsubscribe function. | subscribe 注册 fn 并返回幂等的取消订阅函数。
func (b *eventBus) subscribe(fn EventHandler) func() {
	if fn == nil {
		return func() {}
	}
	id := b.next.Add(1)
	b.mu.Lock()
	b.handlers[id] = fn
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.handlers, id)
		b.mu.Unlock()
	}
}

// publish enqueues ev; it reports false when the queue is full or the bus is closed. | publish 入队 ev；队列已满或总线已关闭时返回 false。
func (b *eventBus) publish(ev Event) bool {
	if b.closed.Load() {
		return false
	}
	select {
	case b.queue <- ev:
		return true
	default:
		return false
	}
}

// close stops the dispatcher after draining nothing; in-flight handlers finish. | close 停止调度器（不等待清空队列）；进行中的处理器会完成。
func (b *eventBus) close() {
	b.once.Do(func() {
		b.closed.Store(true)
		close(b.stop)
		<-b.done
	})
}

// dispatch delivers one event at a time. | dispatch 一次投递一个事件。
func (b *eventBus) dispatch() {
	defer close(b.done)
	for {
		select {
		case <-b.stop:
			return
		case ev := <-b.queue:
			b.mu.RLock()
			handlers := make([]EventHandler, 0, len(b.handlers))
			for _, h := range b.handlers {
				handlers = append(handlers, h)
			}
			b.mu.RUnlock()
			for _, h := range handlers {
				b.call(h, ev)
			}
		}
	}
}

// call invokes one handler and contains its panic. | call 调用一个处理器并隔离其 panic。
func (b *eventBus) call(h EventHandler, ev Event) {
	defer func() {
		if rec := recover(); rec != nil {
			b.log.Error("cachex: event handler panicked | cachex：事件处理器发生 panic", logx.Any("panic", rec))
		}
	}()
	h(ev)
}
