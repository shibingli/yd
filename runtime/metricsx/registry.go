package metricsx

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shibingli/ydx/router"
)

// unnamedMiddleware is used when a descriptor carries an empty Name. | unnamedMiddleware 在描述符未给出 Name 时使用。
const unnamedMiddleware = "unnamed"

// panicSeenKey marks a request whose panic has already been attributed to the innermost observer. | panicSeenKey
// 标记一次 panic 已归给最内层观察者的请求。
//
// The key is namespaced so it cannot collide with an application context value. | 键带命名空间，因此不会与应用的
// 上下文值冲突。
const panicSeenKey = "__yd_metricsx_panic"

// abortSeenKey marks a request whose abort has already been attributed to the innermost observer. | abortSeenKey
// 标记一次 abort 已归给最内层观察者的请求。
const abortSeenKey = "__yd_metricsx_abort"

// Registry observes middleware execution and publishes the readings as a MiddlewareSource. | Registry
// 观察中间件执行，并作为 MiddlewareSource 发布读数。
//
// The hot path uses atomics only: wrapping a middleware costs a few counter updates per request and no
// locks. Custom counters and gauges take a small mutex, so they belong on events rather than on every
// request. | 热路径只使用原子操作：包装一个中间件在每请求上只增加几次计数，且不加锁。自定义计数器与瞬时值会
// 获取一把小锁，因此应当放在事件上而不是每个请求上。
type Registry struct {
	entries map[string]*Entry
	// order preserves registration order, which mirrors middleware execution order. | order 保留注册顺序，
	// 与中间件的执行顺序一致。
	order []*Entry
	mu    sync.RWMutex
	// stopped and started are registry-wide lifecycle flags. | stopped 与 started 是注册表级的生命周期标记。
	stopped bool
	started bool
}

// Entry is one observed middleware and its counters. | Entry 是一个被观察的中间件及其计数。
//
// Entries are created by Registry.Entry or Registry.Wrap and are safe for concurrent use. | 条目由
// Registry.Entry 或 Registry.Wrap 创建，并发安全。
type Entry struct {
	counters map[string]uint64
	gauges   map[string]int64
	name     string
	title    string
	titleKey string
	scope    Scope
	// mu guards counters and gauges. | mu 保护计数器和瞬时值。
	mu sync.Mutex
	// buckets is a bounded latency histogram; the last bucket is the overflow bucket. | buckets 是有限延迟
	// 直方图；最后一个桶是溢出桶。
	buckets   [len(latencyBounds) + 1]atomic.Uint64
	requests  atomic.Uint64
	inFlight  atomic.Int64
	aborted   atomic.Uint64
	errors    atomic.Uint64
	panics    atomic.Uint64
	totalNano atomic.Uint64
	maxNano   atomic.Uint64
	lastSeen  atomic.Int64
	started   atomic.Bool
	stopped   atomic.Bool
}

// NewRegistry creates an empty registry. | NewRegistry 创建一个空注册表。
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*Entry)}
}

// Entry returns the entry for d, creating it on first use. | Entry 返回 d 对应的条目，首次使用时创建。
//
// Descriptors sharing a Name share one entry, so a middleware mounted in several route groups is
// counted once. The first descriptor wins, which keeps the label stable. | Name 相同的描述符共用一条条目，
// 因此挂载在多个路由组里的同一个中间件只统计一次。首个描述符获胜，从而保持标签稳定。
func (r *Registry) Entry(d Descriptor) *Entry {
	if r == nil {
		return newEntry(d)
	}
	if d.Name == "" {
		d.Name = unnamedMiddleware
	}
	if d.Scope == "" {
		d.Scope = ScopeCustom
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.entries[d.Name]; ok {
		return entry
	}
	entry := newEntry(d)
	entry.stopped.Store(r.stopped)
	entry.started.Store(r.started)
	r.entries[d.Name] = entry
	r.order = append(r.order, entry)
	return entry
}

// newEntry builds a detached entry, used only when the registry is nil. | newEntry 构造一个独立条目，仅在注册表为 nil 时使用。
func newEntry(d Descriptor) *Entry {
	return &Entry{
		name:     d.Name,
		title:    d.Title,
		titleKey: d.TitleKey,
		scope:    d.Scope,
		counters: make(map[string]uint64),
		gauges:   make(map[string]int64),
	}
}

// Wrap observes mw and returns a middleware that keeps the original semantics. | Wrap 观察 mw，并返回一个
// 保持原有语义的中间件。
//
// A panic is recorded and re-thrown unchanged, so the outer recovery middleware still owns the
// response; a nil middleware is returned as-is. | panic 会被记录并原样重新抛出，响应仍由外层恢复中间件负责；
// nil 中间件原样返回。
func (r *Registry) Wrap(d Descriptor, mw router.Middleware) router.Middleware {
	if mw == nil {
		return nil
	}
	entry := r.Entry(d)
	return func(c *router.Ctx) {
		start := time.Now()
		entry.begin()
		defer func() {
			rec := recover()
			if rec != nil {
				// A panic unwinds through every observer, so only the innermost one counts it; otherwise one
				// handler fault would inflate the panic counter of every middleware on the chain.
				// | panic 会穿过每一个观察者，因此只由最内层计数；否则一次处理器故障会让链上每个中间件的
				// panic 计数一起膨胀。
				if c == nil {
					entry.panics.Add(1)
				} else if _, seen := c.Get(panicSeenKey); !seen {
					entry.panics.Add(1)
					c.Set(panicSeenKey, true)
				}
				entry.finish(start, c)
				// Re-throw: swallowing the panic would resume the chain and is worse than the panic itself.
				// | 重新抛出：吞掉 panic 会让调用链继续执行，比 panic 本身更危险。
				panic(rec)
			}
			entry.finish(start, c)
		}()
		mw(c)
	}
}

// begin records the start of one invocation. | begin 记录一次调用的开始。
func (e *Entry) begin() {
	e.inFlight.Add(1)
	e.started.Store(true)
}

// finish records the end of one invocation. | finish 记录一次调用的结束。
func (e *Entry) finish(start time.Time, c *router.Ctx) {
	elapsed := time.Since(start)
	e.inFlight.Add(-1)
	e.requests.Add(1)
	e.totalNano.Add(uint64(elapsed))
	e.buckets[bucketIndex(elapsed)].Add(1)
	e.observeMax(uint64(elapsed))
	e.lastSeen.Store(time.Now().UnixNano())
	if c == nil {
		return
	}
	// An aborted flag stays set for every outer middleware, so attribute the abort to the observer closest
	// to the middleware that set it. | abort 标志会留给每个外层中间件，因此把终止归给最靠近设置者的观察者。
	if c.Aborted() {
		if _, seen := c.Get(abortSeenKey); !seen {
			e.aborted.Add(1)
			c.Set(abortSeenKey, true)
		}
	}
	if c.StatusCode() >= 500 {
		e.errors.Add(1)
	}
}

// observeMax updates the maximum with a compare-and-swap loop. | observeMax 通过比较并交换循环更新最大值。
func (e *Entry) observeMax(n uint64) {
	for {
		current := e.maxNano.Load()
		if n <= current {
			return
		}
		if e.maxNano.CompareAndSwap(current, n) {
			return
		}
	}
}

// Incr adds one to a custom counter. | Incr 给一个自定义计数器加一。
func (e *Entry) Incr(name string) { e.Add(name, 1) }

// Add adds delta to a custom counter; an empty name is ignored. | Add 给一个自定义计数器加 delta；空名称会被忽略。
func (e *Entry) Add(name string, delta uint64) {
	if e == nil || name == "" {
		return
	}
	e.mu.Lock()
	e.counters[name] += delta
	e.mu.Unlock()
}

// SetGauge stores a custom instantaneous value; an empty name is ignored. | SetGauge 保存一个自定义瞬时值；空名称会被忽略。
func (e *Entry) SetGauge(name string, value int64) {
	if e == nil || name == "" {
		return
	}
	e.mu.Lock()
	e.gauges[name] = value
	e.mu.Unlock()
}

// Start marks every registered entry active. | Start 把全部已注册条目标记为 active。
//
// It is called when the application starts serving; a request before Start also activates its own
// entry, so tests and embedded routers need no lifecycle call. Start after Stop is ignored. | 它在应用开始
// 提供服务时调用；Start 之前的请求也会激活自己的条目，因此测试与嵌入式路由无需生命周期调用。Stop 之后的
// Start 会被忽略。
func (r *Registry) Start() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.started = true
	for _, entry := range r.order {
		entry.started.Store(true)
	}
}

// Stop marks every entry stopped. It is idempotent and keeps all counters. | Stop 把全部条目标记为 stopped。
// 该操作幂等，并保留全部计数。
func (r *Registry) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.stopped = true
	for _, entry := range r.order {
		entry.stopped.Store(true)
	}
}

// Shutdown implements the app shutdown hook shape. | Shutdown 实现应用关闭钩子的形态。
func (r *Registry) Shutdown(context.Context) error {
	r.Stop()
	return nil
}

// MiddlewareSnapshot returns every entry in registration order. | MiddlewareSnapshot 按注册顺序返回全部条目。
func (r *Registry) MiddlewareSnapshot() []MiddlewareSnapshot {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	order := append([]*Entry(nil), r.order...)
	r.mu.RUnlock()

	out := make([]MiddlewareSnapshot, 0, len(order))
	for _, entry := range order {
		out = append(out, entry.snapshot())
	}
	return out
}

// snapshot copies one entry's counters into an immutable view. | snapshot 把一个条目的计数复制为不可变视图。
func (e *Entry) snapshot() MiddlewareSnapshot {
	requests := e.requests.Load()
	avg, p95, maxLatency := e.latency(requests)

	e.mu.Lock()
	counters := make(map[string]uint64, len(e.counters))
	for key, value := range e.counters {
		counters[key] = value
	}
	gauges := make(map[string]int64, len(e.gauges))
	for key, value := range e.gauges {
		gauges[key] = value
	}
	e.mu.Unlock()

	state := StateRegistered
	switch {
	case e.stopped.Load():
		state = StateStopped
	case e.started.Load() || requests > 0:
		state = StateActive
	}

	var lastSeen time.Time
	if nanos := e.lastSeen.Load(); nanos > 0 {
		lastSeen = time.Unix(0, nanos)
	}
	inFlight := e.inFlight.Load()
	if inFlight < 0 {
		inFlight = 0
	}
	return MiddlewareSnapshot{
		LastSeen:       lastSeen,
		Name:           e.name,
		Title:          e.title,
		TitleKey:       e.titleKey,
		Scope:          e.scope,
		State:          state,
		Counters:       counters,
		Gauges:         gauges,
		Requests:       requests,
		InFlight:       uint64(inFlight),
		Aborted:        e.aborted.Load(),
		Errors:         e.errors.Load(),
		Panics:         e.panics.Load(),
		AverageLatency: avg,
		P95Latency:     p95,
		MaxLatency:     maxLatency,
	}
}
