package cachex

import (
	"context"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/shibingli/yd/core/errx"
)

// Status classifies a lookup. | Status 对一次查询分类。
type Status uint8

const (
	// StatusMiss means no live value exists. | StatusMiss 表示不存在存活值。
	StatusMiss Status = iota
	// StatusHit means a value was found. | StatusHit 表示找到了值。
	StatusHit
	// StatusNegative means a negative entry answered the lookup. | StatusNegative 表示由负缓存条目答复。
	StatusNegative
)

// cacheConfig is the per-namespace knob set. | cacheConfig 是每命名空间的旋钮集合。
type cacheConfig struct {
	ttl         time.Duration
	negativeTTL time.Duration
	jitter      time.Duration
	lease       time.Duration
}

// CacheOption mutates a namespace configuration. | CacheOption 修改命名空间配置。
type CacheOption func(*cacheConfig)

// defaultCacheConfig returns the zero knob set: no default TTL and no negative cache. | defaultCacheConfig 返回零旋钮集：无默认 TTL、无负缓存。
func defaultCacheConfig() cacheConfig { return cacheConfig{} }

// WithTTL sets the default TTL used when a call passes ttl <= 0. | WithTTL 设置调用传入 ttl <= 0 时使用的默认 TTL。
func WithTTL(d time.Duration) CacheOption {
	return func(c *cacheConfig) { c.ttl = d }
}

// WithNegativeTTL enables negative caching for loader ErrNotFound results. | WithNegativeTTL 为 loader 的 ErrNotFound 结果启用负缓存。
func WithNegativeTTL(d time.Duration) CacheOption {
	return func(c *cacheConfig) { c.negativeTTL = d }
}

// WithJitter adds random spread to every TTL written by this namespace. | WithJitter 给该命名空间写入的每个 TTL 加入随机抖动。
func WithJitter(d time.Duration) CacheOption {
	return func(c *cacheConfig) { c.jitter = d }
}

// WithNamespaceLease overrides the L1 revalidation lease for this namespace. | WithNamespaceLease 覆盖该命名空间的 L1 重新确认租约。
func WithNamespaceLease(d time.Duration) CacheOption {
	return func(c *cacheConfig) { c.lease = d }
}

// rawStats holds per-namespace counters. | rawStats 保存每命名空间计数器。
type rawStats struct {
	hits         atomic.Uint64
	misses       atomic.Uint64
	negatives    atomic.Uint64
	remoteHits   atomic.Uint64
	remoteMisses atomic.Uint64
	remoteErrors atomic.Uint64
	loads        atomic.Uint64
	loadErrors   atomic.Uint64
	sets         atomic.Uint64
	deletes      atomic.Uint64
	casLost      atomic.Uint64
}

// Raw is a byte-oriented namespace over a Manager. | Raw 是建立在 Manager 之上的面向字节命名空间。
//
// Raw is safe for concurrent use. Values are opaque bytes; typed caches encode and decode | Raw 可并发使用。值是不透明字节；泛型缓存在其之上完成编解码。
// on top of it.
type Raw struct {
	group       singleflight.Group
	manager     *Manager
	namespace   string
	prefix      string
	stats       rawStats
	ttl         time.Duration
	negativeTTL time.Duration
	jitter      time.Duration
	lease       time.Duration
	closed      atomic.Bool
}

// Namespace returns the namespace name. | Namespace 返回命名空间名称。
func (r *Raw) Namespace() string { return r.namespace }

// Get returns a copy of the value stored under key. | Get 返回 key 对应值的副本。
func (r *Raw) Get(ctx context.Context, key string) ([]byte, bool, error) {
	return r.GetAppend(ctx, nil, key)
}

// GetAppend appends a copy of the value stored under key to dst. | GetAppend 把 key 对应值的副本追加到 dst。
func (r *Raw) GetAppend(ctx context.Context, dst []byte, key string) ([]byte, bool, error) {
	var out = dst
	status, _, err := r.lookup(ctx, key, func(data []byte, _ uint64) error {
		out = append(out, data...)
		return nil
	})
	if err != nil {
		return dst, false, err
	}
	if status != StatusHit {
		return dst, false, nil
	}
	return out, true, nil
}

// View calls fn with a zero-copy view of the value stored under key. | View 以零拷贝视图把 key 对应值交给 fn。
//
// The view is valid only for the duration of fn; fn must not retain or mutate it, and must | 视图仅在 fn 执行期间有效；fn 不得保留或修改它，也
// not call back into the same cache. | 不得回调同一缓存。
func (r *Raw) View(ctx context.Context, key string, fn func([]byte) error) (bool, error) {
	if fn == nil {
		return false, ErrNilView
	}
	status, _, err := r.lookup(ctx, key, func(data []byte, _ uint64) error { return fn(data) })
	if err != nil {
		return status == StatusHit, err
	}
	return status == StatusHit, nil
}

// GetFresh returns a copy read directly from L2, bypassing L1. | GetFresh 绕过 L1，直接从 L2 读取副本。
//
// Use it when the caller cannot tolerate the L1 lease window. Without an L2 the call falls | 当调用方无法容忍 L1 租约窗口时使用。没有 L2 时该调用
// back to the ordinary L1 read. A fresh hit refreshes the L1 entry. | 回退到普通 L1 读取。命中会刷新 L1 条目。
func (r *Raw) GetFresh(ctx context.Context, key string) ([]byte, bool, error) {
	var out []byte
	found, err := r.ViewFresh(ctx, key, func(data []byte) error {
		out = append(out, data...)
		return nil
	})
	if err != nil || !found {
		return nil, false, err
	}
	return out, true, nil
}

// ViewFresh calls fn with a zero-copy L2 view, bypassing L1. | ViewFresh 绕过 L1，以零拷贝 L2 视图调用 fn。
func (r *Raw) ViewFresh(ctx context.Context, key string, fn func([]byte) error) (bool, error) {
	if fn == nil {
		return false, ErrNilView
	}
	status, _, err := r.lookupFresh(ctx, key, func(data []byte, _ uint64) error { return fn(data) })
	if err != nil {
		return status == StatusHit, err
	}
	return status == StatusHit, nil
}

// Set stores data under key using the namespace default TTL. | Set 以命名空间默认 TTL 把 data 存到 key 下。
func (r *Raw) Set(ctx context.Context, key string, data []byte) error {
	return r.SetTTL(ctx, key, data, 0)
}

// SetTTL stores data under key with an explicit TTL; ttl <= 0 means the default. | SetTTL 以显式 TTL 存储 data；ttl <= 0 表示使用默认值。
func (r *Raw) SetTTL(ctx context.Context, key string, data []byte, ttl time.Duration) error {
	if err := r.check(); err != nil {
		return err
	}
	if err := r.checkKey(key); err != nil {
		return err
	}
	ttl = r.effectiveTTL(ttl)
	full := r.full(key)
	var version uint64
	if r.manager.remote != nil {
		v, err := r.manager.remote.Store(ctx, full, data, FlagNone, ttl)
		if err != nil {
			return errx.Wrap(err, ErrRemote)
		}
		version = v
	}
	if err := r.setLocal(key, data, version, FlagNone, ttl); err != nil {
		return err
	}
	r.stats.sets.Add(1)
	return r.publish(ctx, OpSet, key)
}

// Delete removes key from both levels. | Delete 从两层移除 key。
func (r *Raw) Delete(ctx context.Context, key string) error {
	if err := r.check(); err != nil {
		return err
	}
	if err := r.checkKey(key); err != nil {
		return err
	}
	full := r.full(key)
	if r.manager.remote != nil {
		if _, err := r.manager.remote.Remove(ctx, full, FlagTombstone, r.manager.opts.TombstoneTTL); err != nil {
			r.manager.local.Delete(full)
			return errx.Wrap(err, ErrRemote)
		}
	}
	r.manager.local.Delete(full)
	r.stats.deletes.Add(1)
	return r.publish(ctx, OpDelete, key)
}

// Clear invalidates every L1 entry in this namespace on every node. | Clear 使所有节点上该命名空间的 L1 条目失效。
//
// L2 entries are not scanned or deleted; they become unreachable through the L1 lease and | 不会扫描或删除 L2 条目；它们会随 L1 租约与
// overwrite naturally. | 自然覆盖而变得不可达。
func (r *Raw) Clear(ctx context.Context) error {
	if err := r.check(); err != nil {
		return err
	}
	r.manager.local.DeletePrefix(r.prefix)
	ev := Event{Op: OpClear, Namespace: r.namespace, Source: r.manager.opts.NodeID, At: time.Now().UTC()}
	return r.manager.Publish(ctx, ev)
}

// GetOrLoad returns the value for key, calling loader on a miss with the default TTL. | GetOrLoad 返回 key 的值，未命中时以默认 TTL 调用 loader。
//
// Concurrent misses for one key are collapsed per process. A loader returning ErrNotFound | 同一进程内同一键的并发未命中会被收敛。loader 返回 ErrNotFound 且
// with a positive negative TTL writes a negative entry. The loader must not call GetOrLoad | 负缓存 TTL 为正时会写入负缓存条目。loader 不得对同一键再次调用
// for the same key on this Raw: singleflight would wait on itself. | 本 Raw 的 GetOrLoad：单飞会等待自身。
func (r *Raw) GetOrLoad(ctx context.Context, key string, loader func(context.Context) ([]byte, error)) ([]byte, error) {
	return r.GetOrLoadTTL(ctx, key, 0, loader)
}

// GetOrLoadTTL is GetOrLoad with an explicit TTL; ttl <= 0 means the default. | GetOrLoadTTL 是带显式 TTL 的 GetOrLoad；ttl <= 0 表示默认值。
func (r *Raw) GetOrLoadTTL(ctx context.Context, key string, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error) {
	if loader == nil {
		return nil, ErrNilLoader
	}
	data, _, status, err := r.fetch(ctx, key)
	if err != nil {
		return nil, err
	}
	switch status {
	case StatusHit:
		return data, nil
	case StatusNegative:
		return nil, ErrNotFound
	}
	return r.loadShared(ctx, key, ttl, loader)
}

// GetOrLoadView is GetOrLoad with a zero-copy callback for both a cache hit and a loaded value.
// The callback follows the same lock and aliasing rules as View. | 回调遵循与 View 相同的锁与别名规则。
// | GetOrLoadView 是带零拷贝回调的 GetOrLoad，命中与加载结果都直接交给回调。
func (r *Raw) GetOrLoadView(ctx context.Context, key string, ttl time.Duration, loader func(context.Context) ([]byte, error), fn func([]byte) error) error {
	if loader == nil {
		return ErrNilLoader
	}
	if fn == nil {
		return ErrNilView
	}
	status, _, err := r.lookup(ctx, key, func(data []byte, _ uint64) error { return fn(data) })
	if err != nil {
		return err
	}
	switch status {
	case StatusHit:
		return nil
	case StatusNegative:
		return ErrNotFound
	}
	data, err := r.loadShared(ctx, key, ttl, loader)
	if err != nil {
		return err
	}
	return fn(data)
}

// Subscribe registers a handler for events in this namespace and for whole-cache clears. | Subscribe 注册本命名空间事件与全缓存清理事件的处理器。
func (r *Raw) Subscribe(fn EventHandler) (func(), error) {
	if fn == nil {
		return func() {}, nil
	}
	return r.manager.Subscribe(func(ev Event) {
		if ev.Namespace == "" || ev.Namespace == r.namespace {
			fn(ev)
		}
	})
}

// Stats returns this namespace's counters plus the shared L1 snapshot. | Stats 返回本命名空间计数器与共享 L1 快照。
func (r *Raw) Stats() Stats {
	return Stats{
		Hits:          r.stats.hits.Load(),
		Misses:        r.stats.misses.Load(),
		Negatives:     r.stats.negatives.Load(),
		RemoteHits:    r.stats.remoteHits.Load(),
		RemoteMisses:  r.stats.remoteMisses.Load(),
		RemoteErrors:  r.stats.remoteErrors.Load(),
		Loads:         r.stats.loads.Load(),
		LoadErrors:    r.stats.loadErrors.Load(),
		Sets:          r.stats.sets.Load(),
		Deletes:       r.stats.deletes.Load(),
		CASLost:       r.stats.casLost.Load(),
		Invalidations: r.manager.stats.invalidations.Load(),
		EventsDropped: r.manager.stats.eventsDropped.Load(),
		Local:         r.manager.local.Stats(),
	}
}

// Close releases the namespace. It is idempotent. | Close 释放命名空间。它是幂等的。
func (r *Raw) Close() error {
	if r.closed.Swap(true) {
		return nil
	}
	r.manager.unregisterNamespace(r.namespace)
	return nil
}
