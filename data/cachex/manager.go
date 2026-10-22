package cachex

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/data/cachex/local"
)

// managerStats holds manager-wide counters. | managerStats 保存管理器级计数器。
type managerStats struct {
	invalidations atomic.Uint64
	eventsDropped atomic.Uint64
}

// ManagerStats is a point-in-time view of manager-wide activity. | ManagerStats 是管理器级活动的时间点视图。
type ManagerStats struct {
	// Invalidations counts L1 keys invalidated by remote events. | Invalidations 统计被远端事件失效的 L1 键数。
	Invalidations uint64
	// EventsDropped counts events dropped because the subscriber queue was full. | EventsDropped 统计因订阅队列满而丢弃的事件数。
	EventsDropped uint64
	// Local is the shared L1 snapshot. | Local 是共享 L1 快照。
	Local local.Stats
}

// Manager owns the shared L1 cache, the optional L2 store and the event bus. | Manager 拥有共享 L1 缓存、可选 L2 存储与事件总线。
type Manager struct {
	remote     Remote
	subCtx     context.Context
	local      *local.Cache
	log        *logx.Logger
	bus        *eventBus
	subCancel  context.CancelFunc
	namespaces map[string]struct{}
	opts       Options
	stats      managerStats
	wg         sync.WaitGroup
	warnAt     atomic.Int64
	closeOnce  sync.Once
	nsMu       sync.Mutex
	closed     atomic.Bool
}

// NewManager builds a manager. | NewManager 构造一个管理器。
//
// The zero value of Options is usable: the prefix, lease and timeouts fall back to the | Options 的零值可用：前缀、租约与超时回退到框架默认值，
// framework defaults, and the L1 budget is derived from the memory visible to the process. | L1 预算由进程可见内存推导。不传 Remote 时只使用本地缓存。
// Without a Remote the manager is a local-only cache.
func NewManager(opts ...Option) (*Manager, error) {
	o := Options{FailOpen: true}
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	o = o.withDefaults()
	lc, err := local.New(o.Local)
	if err != nil {
		return nil, errx.Wrap(err, ErrInvalidOptions)
	}
	m := &Manager{
		local:      lc,
		remote:     o.Remote,
		opts:       o,
		log:        o.Logger,
		bus:        newEventBus(o.EventBuffer, o.Logger),
		namespaces: make(map[string]struct{}),
	}
	m.subCtx, m.subCancel = context.WithCancel(context.Background())
	if m.remote != nil {
		m.wg.Add(1)
		go m.subscribeLoop()
	}
	return m, nil
}

// Raw opens a byte-oriented namespace. | Raw 打开一个面向字节的命名空间。
//
// The namespace must be unique within the manager and must not contain ':'. Options control | 命名空间在管理器内必须唯一，且不得包含 ':'。选项控制
// the default TTL, negative TTL, jitter and L1 lease for this namespace. | 该命名空间的默认 TTL、负缓存 TTL、抖动与 L1 租约。
func (m *Manager) Raw(namespace string, opts ...CacheOption) (*Raw, error) {
	if m.closed.Load() {
		return nil, ErrClosed
	}
	if !validNamespace(namespace) {
		return nil, errx.Detail(ErrInvalidNamespace, "%q", namespace)
	}
	m.nsMu.Lock()
	if _, exists := m.namespaces[namespace]; exists {
		m.nsMu.Unlock()
		return nil, errx.Detail(ErrNamespaceInUse, "%q", namespace)
	}
	m.namespaces[namespace] = struct{}{}
	m.nsMu.Unlock()

	cfg := defaultCacheConfig()
	cfg.lease = m.opts.Lease
	for _, fn := range opts {
		if fn != nil {
			fn(&cfg)
		}
	}
	return &Raw{
		manager:     m,
		namespace:   namespace,
		prefix:      namespace + ":",
		ttl:         cfg.ttl,
		negativeTTL: cfg.negativeTTL,
		jitter:      cfg.jitter,
		lease:       cfg.lease,
	}, nil
}

// Publish broadcasts ev to L2 (when present) and to local subscribers. | Publish 把 ev 广播到 L2（若存在）并投递给本地订阅者。
//
// The caller is responsible for applying the mutation to its own L1 before publishing; | 调用方负责在发布前把变更应用到自己的 L1；
// an event whose Source is this manager's node ID is not replayed to it by the subscriber. | 来源为本管理器节点 ID 的事件不会被订阅者回放给它。
func (m *Manager) Publish(ctx context.Context, ev Event) error {
	if m.closed.Load() {
		return ErrClosed
	}
	if ev.Op < OpSet || ev.Op > OpResync {
		return errx.Detail(ErrInvalidOptions, "unknown op %d | 未知操作 %d", ev.Op, ev.Op)
	}
	if ev.Source == "" {
		ev.Source = m.opts.NodeID
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	if !validEvent(&ev) {
		return ErrInvalidOptions
	}
	if m.remote != nil {
		if err := m.remote.Publish(ctx, ev); err != nil {
			m.stats.eventsDropped.Add(1)
			m.warn("cachex: publish failed | cachex：事件发布失败", err)
			if !m.opts.FailOpen {
				return errx.Wrap(err, ErrRemote)
			}
		}
	}
	if !m.bus.publish(ev) {
		m.stats.eventsDropped.Add(1)
	}
	return nil
}

// Subscribe registers a manager-wide event handler. | Subscribe 注册管理器级事件处理器。
func (m *Manager) Subscribe(fn EventHandler) (func(), error) {
	if m.closed.Load() {
		return nil, ErrClosed
	}
	return m.bus.subscribe(fn), nil
}

// Clear drops every L1 entry on every node and leaves L2 entries to expire or be | Clear 丢弃所有节点上的 L1 条目，L2 条目则等待过期或被
// overwritten. It is a broadcast invalidation, not an L2 purge. | 覆盖。它是广播失效，不是 L2 清空。
func (m *Manager) Clear(ctx context.Context) error {
	if m.closed.Load() {
		return ErrClosed
	}
	m.local.Clear()
	return m.Publish(ctx, Event{Op: OpClear, Source: m.opts.NodeID})
}

// Stats returns manager-wide counters plus the shared L1 snapshot. | Stats 返回管理器级计数器与共享 L1 快照。
func (m *Manager) Stats() ManagerStats {
	return ManagerStats{
		Invalidations: m.stats.invalidations.Load(),
		EventsDropped: m.stats.eventsDropped.Load(),
		Local:         m.local.Stats(),
	}
}

// NodeID returns the identifier this manager stamps on its events. | NodeID 返回本管理器在事件上标记的标识。
func (m *Manager) NodeID() string { return m.opts.NodeID }

// Close stops the subscription loop, closes the local cache and releases the bus. | Close 停止订阅循环、关闭本地缓存并释放事件总线。
// It is idempotent. | 它是幂等的。
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.closed.Store(true)
		m.subCancel()
		m.wg.Wait()
		if m.remote != nil {
			if err := m.remote.Close(); err != nil {
				m.log.Warn("cachex: remote close failed | cachex：远端关闭失败", logx.Err(err))
			}
		}
		if err := m.local.Close(); err != nil {
			m.log.Warn("cachex: local close failed | cachex：本地缓存关闭失败", logx.Err(err))
		}
		m.bus.close()
	})
	return nil
}

// subscribeLoop keeps a subscription alive and resynchronizes after every gap. | subscribeLoop 维持订阅存活，并在每次缺口后重新同步。
func (m *Manager) subscribeLoop() {
	defer m.wg.Done()
	first := true
	backoff := 100 * time.Millisecond
	for {
		if m.subCtx.Err() != nil {
			return
		}
		if !first {
			m.resync()
		}
		first = false
		err := m.remote.Subscribe(m.subCtx, m.onRemoteEvent)
		if m.subCtx.Err() != nil {
			return
		}
		if err != nil {
			m.warn("cachex: subscription interrupted | cachex：订阅中断", err)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-m.subCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 5*time.Second {
			backoff *= 2
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
		}
	}
}

// resync discards L1 after a subscription gap and notifies local handlers. | resync 在订阅缺口后丢弃 L1 并通知本地处理器。
func (m *Manager) resync() {
	m.local.Clear()
	ev := Event{Op: OpResync, Source: m.opts.NodeID, At: time.Now().UTC()}
	if !m.bus.publish(ev) {
		m.stats.eventsDropped.Add(1)
	}
}

// onRemoteEvent applies one event received from L2. | onRemoteEvent 应用一条从 L2 收到的事件。
func (m *Manager) onRemoteEvent(ev Event) {
	if !validEvent(&ev) {
		m.stats.eventsDropped.Add(1)
		return
	}
	if ev.Source == m.opts.NodeID {
		// This manager already applied its own mutation; the Pub/Sub echo is a duplicate.
		// | 本管理器已应用自己的变更；Pub/Sub 回声是重复的。
		return
	}
	switch ev.Op {
	case OpSet, OpDelete:
		for _, k := range ev.Keys {
			m.local.Delete(k)
		}
		m.stats.invalidations.Add(uint64(len(ev.Keys)))
	case OpClear:
		if ev.Namespace == "" {
			m.local.Clear()
		} else {
			m.local.DeletePrefix(ev.Namespace + ":")
		}
		m.stats.invalidations.Add(1)
	case OpInvalidate:
		// Table generations are owned by consumers such as the GORM plugin. | 表代际由 GORM 插件等消费者维护。
	case OpResync:
		m.local.Clear()
	}
	if !m.bus.publish(ev) {
		m.stats.eventsDropped.Add(1)
	}
}

// unregisterNamespace releases ns. | unregisterNamespace 释放命名空间。
func (m *Manager) unregisterNamespace(ns string) {
	m.nsMu.Lock()
	delete(m.namespaces, ns)
	m.nsMu.Unlock()
}

// validNamespace rejects empty, overlong or separator-containing namespaces. | validNamespace 拒绝空、过长或含分隔符的命名空间。
func validNamespace(ns string) bool {
	if ns == "" || len(ns) > 128 {
		return false
	}
	for i := 0; i < len(ns); i++ {
		c := ns[i]
		if c <= 0x20 || c == 0x7f || c == ':' {
			return false
		}
	}
	return true
}

// validEvent bounds an event so a hostile publisher cannot exhaust memory. | validEvent 限制事件大小，防止恶意发布者耗尽内存。
func validEvent(ev *Event) bool {
	if ev.Op < OpSet || ev.Op > OpResync {
		return false
	}
	if len(ev.Namespace) > 128 {
		return false
	}
	if len(ev.Keys) > 1024 || len(ev.Tables) > 256 {
		return false
	}
	for _, k := range ev.Keys {
		if k == "" || len(k) > local.MaxKeyLength {
			return false
		}
	}
	for _, t := range ev.Tables {
		if t == "" || len(t) > 128 {
			return false
		}
	}
	return true
}

// warn logs at most once per five seconds per manager to keep a failing L2 from flooding logs.
// | warn 每管理器至少五秒才记录一次，避免故障 L2 刷爆日志。
func (m *Manager) warn(msg string, err error) {
	now := time.Now().UnixNano()
	last := m.warnAt.Load()
	if last != 0 && now-last < int64(5*time.Second) {
		return
	}
	if !m.warnAt.CompareAndSwap(last, now) {
		return
	}
	m.log.Warn(msg, logx.Err(err))
}
