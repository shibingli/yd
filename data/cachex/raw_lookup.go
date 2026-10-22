package cachex

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/data/cachex/local"
)

// This file owns the read path, single-flight loading and L1/L2 write helpers. | 本文件负责读路径、单飞加载与 L1/L2 写入辅助。

// lookup applies L1 then L2, invoking fn with a zero-copy value view on a hit. | lookup 依次查询 L1 与 L2，命中时以零拷贝视图调用 fn。
func (r *Raw) lookup(ctx context.Context, key string, fn func(data []byte, version uint64) error) (Status, uint64, error) {
	if err := r.check(); err != nil {
		return StatusMiss, 0, err
	}
	if err := r.checkKey(key); err != nil {
		return StatusMiss, 0, err
	}
	full := r.full(key)
	var version uint64
	found, lerr := r.manager.local.View(full, func(raw []byte) error {
		ver, flags, data, ok := WireDecode(raw)
		if !ok {
			return errCorruptEntry
		}
		version = ver
		switch {
		case flags&FlagTombstone != 0:
			return errTombstoneEntry
		case flags&FlagNegative != 0:
			return errNegativeEntry
		}
		return fn(data, ver)
	})
	switch {
	case errors.Is(lerr, errNegativeEntry) && found:
		r.stats.negatives.Add(1)
		return StatusNegative, version, nil
	case errors.Is(lerr, errTombstoneEntry) && found:
		r.manager.local.Delete(full)
	case errors.Is(lerr, errCorruptEntry) && found:
		r.manager.local.Delete(full)
	case found:
		r.stats.hits.Add(1)
		return StatusHit, version, lerr
	case lerr != nil:
		return StatusMiss, 0, lerr
	}

	if r.manager.remote == nil {
		r.stats.misses.Add(1)
		return StatusMiss, 0, nil
	}
	item, ok, err := r.manager.remote.Load(ctx, full)
	if err != nil {
		r.stats.remoteErrors.Add(1)
		if r.manager.opts.FailOpen {
			r.manager.warn("cachex: remote load failed | cachex：远端读取失败", err)
			r.stats.misses.Add(1)
			return StatusMiss, 0, nil
		}
		return StatusMiss, 0, errx.Wrap(err, ErrRemote)
	}
	if !ok {
		r.stats.remoteMisses.Add(1)
		r.stats.misses.Add(1)
		return StatusMiss, 0, nil
	}
	ver, flags, data, ok := WireDecode(item.Raw)
	if !ok {
		r.manager.warn("cachex: corrupt remote envelope | cachex：远端信封损坏", errCorruptEntry)
		_, _ = r.manager.remote.Remove(context.WithoutCancel(ctx), full, FlagTombstone, r.manager.opts.TombstoneTTL)
		r.stats.misses.Add(1)
		return StatusMiss, 0, nil
	}
	if flags&FlagTombstone != 0 {
		r.stats.misses.Add(1)
		return StatusMiss, ver, nil
	}
	r.storeLocalRaw(full, item.Raw, item.TTL)
	if flags&FlagNegative != 0 {
		r.stats.negatives.Add(1)
		return StatusNegative, ver, nil
	}
	r.stats.remoteHits.Add(1)
	r.stats.hits.Add(1)
	if err := fn(data, ver); err != nil {
		return StatusHit, ver, err
	}
	return StatusHit, ver, nil
}

// lookupFresh reads L2 even when L1 has an entry. | lookupFresh 即使 L1 有条目也直接读取 L2。
//
// It is the strict-read path behind GetFresh/ViewFresh; with no L2 it delegates to lookup. | 它是 GetFresh/ViewFresh 背后的严格读路径；没有 L2 时委托给 lookup。
func (r *Raw) lookupFresh(ctx context.Context, key string, fn func(data []byte, version uint64) error) (Status, uint64, error) {
	if err := r.check(); err != nil {
		return StatusMiss, 0, err
	}
	if err := r.checkKey(key); err != nil {
		return StatusMiss, 0, err
	}
	if r.manager.remote == nil {
		return r.lookup(ctx, key, fn)
	}
	full := r.full(key)
	item, ok, err := r.manager.remote.Load(ctx, full)
	if err != nil {
		r.stats.remoteErrors.Add(1)
		if r.manager.opts.FailOpen {
			r.manager.warn("cachex: fresh remote load failed | cachex：严格读远端加载失败", err)
			r.stats.misses.Add(1)
			return StatusMiss, 0, nil
		}
		return StatusMiss, 0, errx.Wrap(err, ErrRemote)
	}
	if !ok {
		r.manager.local.Delete(full)
		r.stats.remoteMisses.Add(1)
		r.stats.misses.Add(1)
		return StatusMiss, 0, nil
	}
	version, flags, data, valid := WireDecode(item.Raw)
	if !valid {
		r.manager.warn("cachex: corrupt remote envelope | cachex：远端信封损坏", errCorruptEntry)
		_, _ = r.manager.remote.Remove(context.WithoutCancel(ctx), full, FlagTombstone, r.manager.opts.TombstoneTTL)
		r.manager.local.Delete(full)
		r.stats.misses.Add(1)
		return StatusMiss, 0, nil
	}
	if flags&FlagTombstone != 0 {
		r.manager.local.Delete(full)
		r.stats.misses.Add(1)
		return StatusMiss, version, nil
	}
	r.storeLocalRaw(full, item.Raw, item.TTL)
	if flags&FlagNegative != 0 {
		r.stats.negatives.Add(1)
		return StatusNegative, version, nil
	}
	r.stats.remoteHits.Add(1)
	r.stats.hits.Add(1)
	if err := fn(data, version); err != nil {
		return StatusHit, version, err
	}
	return StatusHit, version, nil
}

// fetch copies the value for a loader that needs it beyond the view lifetime. | fetch 复制值，供视图生命周期之后仍需使用它的加载路径。
func (r *Raw) fetch(ctx context.Context, key string) ([]byte, uint64, Status, error) {
	var out []byte
	status, version, err := r.lookup(ctx, key, func(data []byte, _ uint64) error {
		out = append(out, data...)
		return nil
	})
	return out, version, status, err
}

// loadShared collapses concurrent misses and performs the CAS write-back. | loadShared 收敛并发未命中并执行 CAS 回填。
func (r *Raw) loadShared(ctx context.Context, key string, ttl time.Duration, loader func(context.Context) ([]byte, error)) ([]byte, error) {
	ch := r.group.DoChan(r.full(key), func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.manager.opts.LoadTimeout)
		defer cancel()
		data, version, status, err := r.fetch(loadCtx, key)
		switch {
		case err != nil:
			return nil, err
		case status == StatusHit:
			// Another caller filled the key while this call waited. | 等待期间其他调用方已回填该键。
			return data, nil
		case status == StatusNegative:
			return nil, ErrNotFound
		}
		r.stats.loads.Add(1)
		value, loadErr := loader(loadCtx)
		if loadErr != nil {
			r.stats.loadErrors.Add(1)
			if errors.Is(loadErr, ErrNotFound) && r.negativeTTL > 0 {
				if werr := r.storeNegative(loadCtx, key); werr != nil {
					return nil, errors.Join(loadErr, werr)
				}
			}
			return nil, loadErr
		}
		stored, storeErr := r.storeLoaded(loadCtx, key, value, r.effectiveTTL(ttl), version)
		if storeErr != nil {
			if r.manager.opts.FailOpen {
				r.manager.warn("cachex: write-back failed | cachex：回填失败", storeErr)
				return value, nil
			}
			return nil, storeErr
		}
		return stored, nil
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		data, _ := res.Val.([]byte)
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// storeLoaded writes a loader result, honoring the version observed on the miss. | storeLoaded 写入 loader 结果，并遵守未命中时观察到的版本。
func (r *Raw) storeLoaded(ctx context.Context, key string, value []byte, ttl time.Duration, expect uint64) ([]byte, error) {
	full := r.full(key)
	if r.manager.remote == nil {
		if err := r.setLocal(key, value, 0, FlagNone, ttl); err != nil {
			return nil, err
		}
		return value, nil
	}
	version, stored, err := r.manager.remote.StoreIf(ctx, full, value, FlagNone, ttl, expect)
	if err != nil {
		return nil, errx.Wrap(err, ErrRemote)
	}
	if !stored {
		r.stats.casLost.Add(1)
		if fresh, _, status, ferr := r.fetch(ctx, key); ferr == nil && status == StatusHit {
			return fresh, nil
		}
		return value, nil
	}
	if err := r.setLocal(key, value, version, FlagNone, ttl); err != nil {
		return nil, err
	}
	return value, nil
}

// storeNegative writes a negative entry when negative caching is enabled. | storeNegative 在启用负缓存时写入负缓存条目。
func (r *Raw) storeNegative(ctx context.Context, key string) error {
	if r.negativeTTL <= 0 {
		return nil
	}
	full := r.full(key)
	var version uint64
	if r.manager.remote != nil {
		v, err := r.manager.remote.Store(ctx, full, nil, FlagNegative, r.negativeTTL)
		if err != nil {
			return errx.Wrap(err, ErrRemote)
		}
		version = v
	}
	return r.setLocal(key, nil, version, FlagNegative, r.negativeTTL)
}

// setLocal writes the wire envelope into L1. | setLocal 把线路信封写入 L1。
func (r *Raw) setLocal(key string, data []byte, version uint64, flags uint8, ttl time.Duration) error {
	bp := wirePool.Get().(*[]byte)
	raw := WireAppend((*bp)[:0], version, flags, data)
	err := r.manager.local.Set(r.full(key), raw, r.localTTL(ttl))
	if cap(raw) <= maxPooledWire {
		*bp = raw[:0]
		wirePool.Put(bp)
	}
	if err != nil {
		if errors.Is(err, local.ErrEntryTooLarge) {
			// With an L2 the value is still durable; local-only callers must learn it was not stored.
			// | 有 L2 时值仍然持久；仅本地模式必须让调用方知道它没有被存下。
			if r.manager.remote == nil {
				return err
			}
			return nil
		}
		return err
	}
	return nil
}

// storeLocalRaw copies an L2 envelope into L1. | storeLocalRaw 把 L2 信封复制进 L1。
func (r *Raw) storeLocalRaw(full string, raw []byte, valueTTL time.Duration) {
	if err := r.manager.local.Set(full, raw, r.localTTL(valueTTL)); err != nil {
		if errors.Is(err, local.ErrEntryTooLarge) || errors.Is(err, local.ErrClosed) {
			return
		}
		r.manager.warn("cachex: local store failed | cachex：本地写入失败", err)
	}
}

// publish emits one key event. | publish 发布一条键事件。
func (r *Raw) publish(ctx context.Context, op Op, key string) error {
	ev := Event{
		Op:        op,
		Namespace: r.namespace,
		Keys:      []string{r.full(key)},
		Source:    r.manager.opts.NodeID,
		At:        time.Now().UTC(),
	}
	return r.manager.Publish(ctx, ev)
}

// effectiveTTL applies the default and jitter. | effectiveTTL 施加默认值与抖动。
func (r *Raw) effectiveTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		ttl = r.ttl
	}
	return jittered(ttl, r.jitter)
}

// localTTL caps a value TTL by the lease. | localTTL 用租约限制值 TTL。
func (r *Raw) localTTL(valueTTL time.Duration) time.Duration {
	lease := r.lease
	if lease <= 0 {
		lease = r.manager.opts.Lease
	}
	return minDuration(valueTTL, lease)
}

// full returns the logical key including the namespace. | full 返回含命名空间的逻辑键。
func (r *Raw) full(key string) string { return r.prefix + key }

// check reports whether the namespace is still open. | check 报告命名空间是否仍打开。
func (r *Raw) check() error {
	if r.closed.Load() || r.manager.closed.Load() {
		return ErrClosed
	}
	return nil
}

// checkKey validates one key against the ring limits. | checkKey 按键上限校验一个键。
func (r *Raw) checkKey(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if len(r.prefix)+len(key) > local.MaxKeyLength {
		return errx.Detail(ErrKeyTooLong, "length %d exceeds %d | 长度 %d 超过 %d",
			len(r.prefix)+len(key), local.MaxKeyLength, len(r.prefix)+len(key), local.MaxKeyLength)
	}
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return ErrInvalidKey
		}
	}
	return nil
}

// maxPooledWire is the largest wire buffer kept in the pool. | maxPooledWire 是池中保留的最大线路缓冲区。
const maxPooledWire = 64 << 10

// wirePool reuses envelope buffers on the write path. | wirePool 在写路径复用信封缓冲区。
var wirePool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 256)
		return &b
	},
}
