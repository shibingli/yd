package cachex

import (
	"context"
	"errors"
	"time"

	"github.com/shibingli/yd/core/errx"
)

// Codec encodes and decodes one cached Go type. | Codec 编码与解码一种被缓存的 Go 类型。
//
// Encode's result may be retained by the cache only until the Set call returns, so a | Encode 的结果最多只会在 Set 调用返回前被缓存持有，因此
// zero-copy Bytes codec is safe. Decode receives a view that is valid only during the call; | 零拷贝 Bytes 编解码器是安全的。Decode 收到的视图仅在调用期间有效；
// it must copy anything it keeps. | 若需保留内容必须自行复制。
type Codec[T any] struct {
	// Encode serializes one value. | Encode 序列化一个值。
	Encode func(T) ([]byte, error)
	// Decode parses one value; it must copy anything it keeps. | Decode 解析一个值；若需保留内容必须自行复制。
	Decode func([]byte) (T, error)
}

// Cache is a typed view over one Raw namespace. | Cache 是一个 Raw 命名空间上的泛型视图。
type Cache[T any] struct {
	raw   *Raw
	codec Codec[T]
}

// New opens a typed cache using the default JSON codec. | New 使用默认 JSON 编解码器打开一个泛型缓存。
func New[T any](m *Manager, namespace string, opts ...CacheOption) (*Cache[T], error) {
	return NewWithCodec(m, namespace, JSON[T](), opts...)
}

// NewWithCodec opens a typed cache with a custom codec. | NewWithCodec 使用自定义编解码器打开一个泛型缓存。
func NewWithCodec[T any](m *Manager, namespace string, codec Codec[T], opts ...CacheOption) (*Cache[T], error) {
	if m == nil {
		return nil, errx.Detail(ErrInvalidOptions, "nil manager | 管理器为 nil")
	}
	if codec.Encode == nil || codec.Decode == nil {
		return nil, errx.Detail(ErrInvalidOptions, "incomplete codec | 编解码器不完整")
	}
	raw, err := m.Raw(namespace, opts...)
	if err != nil {
		return nil, err
	}
	return &Cache[T]{raw: raw, codec: codec}, nil
}

// Raw returns the underlying byte namespace. | Raw 返回底层字节命名空间。
func (c *Cache[T]) Raw() *Raw { return c.raw }

// Namespace returns the namespace name. | Namespace 返回命名空间名称。
func (c *Cache[T]) Namespace() string { return c.raw.Namespace() }

// Get decodes and returns the value stored under key. | Get 解码并返回 key 对应的值。
//
// A negative entry is reported as a miss; use GetOrLoad to observe ErrNotFound. A payload | 负缓存按未命中上报；需要感知 ErrNotFound 时请用 GetOrLoad。无法解码的
// that cannot be decoded is deleted and reported as a miss so the next call repairs it. | 载荷会被删除并按未命中上报，使下一次调用可以修复它。
func (c *Cache[T]) Get(ctx context.Context, key string) (T, bool, error) {
	var zero T
	var out T
	status, _, err := c.raw.lookup(ctx, key, func(data []byte, _ uint64) error {
		v, derr := c.codec.Decode(data)
		if derr != nil {
			return errx.Wrap(derr, ErrDecode)
		}
		out = v
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrDecode) {
			_ = c.raw.Delete(ctx, key)
			return zero, false, nil
		}
		return zero, false, err
	}
	if status != StatusHit {
		return zero, false, nil
	}
	return out, true, nil
}

// View decodes a cached value inside the zero-copy callback. | View 在零拷贝回调内解码缓存值。
//
// The decoded value is passed to fn; it must not be retained if T itself aliases the | 解码后的值交给 fn；若 T 本身会与缓存字节共享底层数组，则 fn
// cache bytes (the Bytes codec copies, so that case is safe). | 不得保留它（Bytes 编解码器会复制，因此该场景是安全的）。
func (c *Cache[T]) View(ctx context.Context, key string, fn func(T) error) (bool, error) {
	if fn == nil {
		return false, ErrNilView
	}
	status, _, err := c.raw.lookup(ctx, key, func(data []byte, _ uint64) error {
		v, derr := c.codec.Decode(data)
		if derr != nil {
			return errx.Wrap(derr, ErrDecode)
		}
		return fn(v)
	})
	if err != nil {
		if errors.Is(err, ErrDecode) {
			_ = c.raw.Delete(ctx, key)
			return false, nil
		}
		return status == StatusHit, err
	}
	return status == StatusHit, nil
}

// GetFresh decodes the value directly from L2, bypassing L1. | GetFresh 绕过 L1，直接从 L2 解码值。
func (c *Cache[T]) GetFresh(ctx context.Context, key string) (T, bool, error) {
	var zero T
	var out T
	status, _, err := c.raw.lookupFresh(ctx, key, func(data []byte, _ uint64) error {
		v, derr := c.codec.Decode(data)
		if derr != nil {
			return errx.Wrap(derr, ErrDecode)
		}
		out = v
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrDecode) {
			_ = c.raw.Delete(ctx, key)
			return zero, false, nil
		}
		return zero, false, err
	}
	if status != StatusHit {
		return zero, false, nil
	}
	return out, true, nil
}

// ViewFresh decodes a fresh L2 value inside the callback while still bypassing L1. | ViewFresh 在回调内解码新的 L2 值，同时绕过 L1。
func (c *Cache[T]) ViewFresh(ctx context.Context, key string, fn func(T) error) (bool, error) {
	if fn == nil {
		return false, ErrNilView
	}
	status, _, err := c.raw.lookupFresh(ctx, key, func(data []byte, _ uint64) error {
		v, derr := c.codec.Decode(data)
		if derr != nil {
			return errx.Wrap(derr, ErrDecode)
		}
		return fn(v)
	})
	if err != nil {
		if errors.Is(err, ErrDecode) {
			_ = c.raw.Delete(ctx, key)
			return false, nil
		}
		return status == StatusHit, err
	}
	return status == StatusHit, nil
}

// Set encodes v and stores it with the namespace default TTL. | Set 编码 v 并以命名空间默认 TTL 存储。
func (c *Cache[T]) Set(ctx context.Context, key string, v T) error {
	return c.SetTTL(ctx, key, v, 0)
}

// SetTTL encodes v and stores it with an explicit TTL; ttl <= 0 means the default. | SetTTL 编码 v 并以显式 TTL 存储；ttl <= 0 表示默认值。
func (c *Cache[T]) SetTTL(ctx context.Context, key string, v T, ttl time.Duration) error {
	data, err := c.codec.Encode(v)
	if err != nil {
		return errx.Wrap(err, ErrEncode)
	}
	return c.raw.SetTTL(ctx, key, data, ttl)
}

// Delete removes key. | Delete 移除 key。
func (c *Cache[T]) Delete(ctx context.Context, key string) error { return c.raw.Delete(ctx, key) }

// Clear invalidates every L1 entry in this namespace. | Clear 使该命名空间的所有 L1 条目失效。
func (c *Cache[T]) Clear(ctx context.Context) error { return c.raw.Clear(ctx) }

// GetOrLoad returns the cached value, calling loader on a miss. | GetOrLoad 返回缓存值，未命中时调用 loader。
func (c *Cache[T]) GetOrLoad(ctx context.Context, key string, loader func(context.Context) (T, error)) (T, error) {
	return c.GetOrLoadTTL(ctx, key, 0, loader)
}

// GetOrLoadTTL is GetOrLoad with an explicit TTL; ttl <= 0 means the default. | GetOrLoadTTL 是带显式 TTL 的 GetOrLoad；ttl <= 0 表示默认值。
func (c *Cache[T]) GetOrLoadTTL(ctx context.Context, key string, ttl time.Duration, loader func(context.Context) (T, error)) (T, error) {
	var zero T
	var out T
	if loader == nil {
		return zero, ErrNilLoader
	}
	err := c.raw.GetOrLoadView(ctx, key, ttl, func(lctx context.Context) ([]byte, error) {
		v, lerr := loader(lctx)
		if lerr != nil {
			return nil, lerr
		}
		data, eerr := c.codec.Encode(v)
		if eerr != nil {
			return nil, errx.Wrap(eerr, ErrEncode)
		}
		return data, nil
	}, func(data []byte) error {
		v, derr := c.codec.Decode(data)
		if derr != nil {
			return errx.Wrap(derr, ErrDecode)
		}
		out = v
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrDecode) {
			_ = c.raw.Delete(ctx, key)
		}
		return zero, err
	}
	return out, nil
}

// GetOrLoadView is read-through with a zero-copy callback for both a hit and a loaded value.
// The callback follows the same lock and aliasing rules as View. | 回调遵循与 View 相同的锁与别名规则。
// | GetOrLoadView 是带零拷贝回调的读透，命中与加载结果都直接交给回调。
func (c *Cache[T]) GetOrLoadView(ctx context.Context, key string, ttl time.Duration, loader func(context.Context) (T, error), fn func(T) error) error {
	if loader == nil {
		return ErrNilLoader
	}
	if fn == nil {
		return ErrNilView
	}
	return c.raw.GetOrLoadView(ctx, key, ttl, func(lctx context.Context) ([]byte, error) {
		v, lerr := loader(lctx)
		if lerr != nil {
			return nil, lerr
		}
		data, eerr := c.codec.Encode(v)
		if eerr != nil {
			return nil, errx.Wrap(eerr, ErrEncode)
		}
		return data, nil
	}, func(data []byte) error {
		v, derr := c.codec.Decode(data)
		if derr != nil {
			return errx.Wrap(derr, ErrDecode)
		}
		return fn(v)
	})
}

// Subscribe registers a handler for this namespace's events. | Subscribe 注册本命名空间事件的处理器。
func (c *Cache[T]) Subscribe(fn EventHandler) (func(), error) { return c.raw.Subscribe(fn) }

// Stats returns this namespace's counters. | Stats 返回本命名空间计数器。
func (c *Cache[T]) Stats() Stats { return c.raw.Stats() }

// Close releases the namespace. | Close 释放命名空间。
func (c *Cache[T]) Close() error { return c.raw.Close() }
