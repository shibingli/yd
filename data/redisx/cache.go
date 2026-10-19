package redisx

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
)

// Cache-related sentinel errors, testable with errors.Is. | 缓存相关哨兵错误，可用 errors.Is 判定。
var (
	// ErrNotFound is the "value does not exist" contract between a loader and GetOrLoad: | ErrNotFound 是 loader 与 GetOrLoad 之间「值不存在」的约定：
	// when it is reported and CacheOptions.NegativeTTL is positive, GetOrLoad writes a | 上报它且 CacheOptions.NegativeTTL 为正时，GetOrLoad 会写入一条负缓存条目，
	// negative entry so the next call is answered without touching the loader again. | 使下一次调用无需再次唤醒 loader 即可得到答复。
	ErrNotFound = errx.New("redisx: value not found | redisx：值不存在")
	// ErrNilLoader indicates a nil loader function was passed in. | ErrNilLoader 表示传入的 loader 函数为 nil。
	ErrNilLoader = errx.New("redisx: nil loader function | redisx：loader 函数为 nil")
	// ErrCacheEncodeFailed indicates the value could not be serialized by jsonx. | ErrCacheEncodeFailed 表示取值无法由 jsonx 序列化。
	ErrCacheEncodeFailed = errx.New("redisx: cache encode failed | redisx：缓存序列化失败")
	// ErrCacheDecodeFailed indicates the cached payload is not valid JSON for T. | ErrCacheDecodeFailed 表示缓存载荷不是 T 的合法 JSON。
	ErrCacheDecodeFailed = errx.New("redisx: cache decode failed | redisx：缓存反序列化失败")
	// ErrCacheReadFailed indicates a redis read (GET / pipeline) failed. | ErrCacheReadFailed 表示 Redis 读操作（GET / pipeline）失败。
	ErrCacheReadFailed = errx.New("redisx: cache read failed | redisx：缓存读取失败")
	// ErrCacheWriteFailed indicates a redis write (SET / DEL) failed. | ErrCacheWriteFailed 表示 Redis 写操作（SET / DEL）失败。
	ErrCacheWriteFailed = errx.New("redisx: cache write failed | redisx：缓存写入失败")
)

// negativePayload is the marker stored for a value that does not exist. | negativePayload 是「值不存在」时存储的标记。
//
// JSON null is chosen on purpose: it stays a plain JSON document, so a payload written | 刻意选用 JSON null：它仍是一份普通 JSON 文档，
// by Set remains decodable by jsonx.Unmarshal, while being distinguishable from any | 因此 Set 写入的载荷依然可被 jsonx.Unmarshal 解析；
// real value of a comparable T. | 同时又能与 T 的任何真实取值区分开。
const negativePayload = "null"

// loadManyBatch is the number of keys read per pipeline round trip in LoadMany. | loadManyBatch 是 LoadMany 每次流水线往返读取的键数量。
//
// It bounds both the command size and the reply size, so one huge key list cannot | 它同时限制命令体与应答体大小，避免一次超长键列表把单次往返撑爆。
// blow up a single round trip.
const loadManyBatch = 128

// defaultLoadTimeout bounds one shared GetOrLoad body so a detached load cannot run forever.
// | defaultLoadTimeout 限制单次共享 GetOrLoad 加载的时长，避免分离后的加载永远运行。
const defaultLoadTimeout = 30 * time.Second

// CacheOptions configures a Cache decorator. | CacheOptions 配置缓存装饰器。
type CacheOptions struct {
	// Prefix is prepended verbatim to every key, forming the key namespace; it should be | Prefix 原样加在每个键之前形成键命名空间；通常取自 Options.KeyPrefix，
	// Options.KeyPrefix, typically including its trailing separator (for example "app:"). | 并包含结尾分隔符（例如 "app:"）。
	Prefix string
	// TTL is the time-to-live of every entry written by Set, GetOrLoad and LoadMany; | TTL 是 Set、GetOrLoad 与 LoadMany 写入条目的存活时间；
	// <= 0 writes entries without expiration. | <= 0 表示写入永不过期的条目。
	TTL time.Duration
	// Jitter is the extra random spread added on top of TTL, so entries written in the | Jitter 是叠加在 TTL 之上的随机抖动；
	// same instant do not expire in the same instant (anti-avalanche); <= 0 disables it. | 使同一瞬间写入的条目不会在同一瞬间过期（防雪崩）；<= 0 表示关闭。
	Jitter time.Duration
	// NegativeTTL is the TTL of the negative entry written when a loader reports ErrNotFound | NegativeTTL 是 loader 上报 ErrNotFound 时所写负缓存条目的 TTL（防穿透）；
	// (anti-penetration); <= 0 disables negative caching, so a missing value is never remembered. | <= 0 表示关闭负缓存，缺失值不会被记住。
	NegativeTTL time.Duration
}

// Cache is a generic read-through cache backed by redis. | Cache 是构建在 Redis 上的泛型读透缓存。
//
// The zero value is not usable; build one with NewCache. A Cache is safe for concurrent use | 零值不可用，请用 NewCache 构造。Cache 可安全并发使用：
// by multiple goroutines: the underlying go-redis client is concurrency-safe and the | 底层 go-redis 客户端本身并发安全，
// single-flight group serializes the loads of one key. | 单飞组则串行化同一个键的加载。
type Cache[T any] struct {
	cli   redis.UniversalClient
	group singleflight.Group
	opt   CacheOptions
}

// entryState describes what a redis lookup found. | entryState 描述一次 Redis 查询的结果。
type entryState uint8

const (
	// entryMiss means the key is absent. | entryMiss 表示键不存在。
	entryMiss entryState = iota
	// entryHit means a decodable value was found. | entryHit 表示找到了可解码的取值。
	entryHit
	// entryNegative means the negative marker was found. | entryNegative 表示找到了负缓存标记。
	entryNegative
)

// NewCache builds a cache decorator over cli. | NewCache 在 cli 之上构造缓存装饰器。
//
// cli may be nil: every operation then reports ErrNilClient instead of panicking, which keeps | cli 允许为 nil：此时每个操作都返回 ErrNilClient 而不 panic，
// a service that starts without redis observable rather than crashing at the first request. | 使未接入 Redis 的服务可被观测，而不是在首个请求上崩溃。
func NewCache[T any](cli redis.UniversalClient, o CacheOptions) *Cache[T] {
	return &Cache[T]{cli: cli, opt: o}
}

// Prefix returns the key namespace of the decorator. | Prefix 返回装饰器的键命名空间。
func (c *Cache[T]) Prefix() string { return c.opt.Prefix }

// GetOrLoad returns the cached value for key, calling load on a miss. | GetOrLoad 返回 key 的缓存值，未命中时调用 load。
//
// Loads of the same key are collapsed by singleflight, so N concurrent misses produce | 同一个键的加载由 singleflight 收敛，因此 N 个并发未命中只会产生一次 load 调用（防击穿）。
// exactly one load call (anti-stampede). A value found by the loader is written back with | loader 取到的值会以 CacheOptions.TTL（含抖动）回填；负缓存命中直接返回 ErrNotFound，
// CacheOptions.TTL (plus jitter). A negative entry answers with ErrNotFound without waking | 不会唤醒 loader。
// the loader. | 负缓存命中直接返回 ErrNotFound，不会唤醒 loader。
//
// Failure policy: a load error is returned as-is and never written to redis; | 失败策略：load 失败时错误原样返回且绝不写入 Redis；
// a cache read or write error is returned as-is too (fail-closed), because silently | 缓存读写错误同样原样返回（fail-closed），因为静默降级会把一次
// degrading to the loader would hide a redis outage from monitoring. | Redis 故障对监控隐藏起来。
func (c *Cache[T]) GetOrLoad(ctx context.Context, key string, load func(context.Context) (T, error)) (T, error) {
	var zero T
	if load == nil {
		return zero, ErrNilLoader
	}

	switch v, state, err := c.find(ctx, key); {
	case err != nil:
		return zero, err
	case state == entryHit:
		return v, nil
	case state == entryNegative:
		return zero, ErrNotFound
	}

	// The shared load must not inherit the first caller's cancellation: every waiter would fail
	// with that caller's context error even though its own context is alive. The detached context
	// is created inside the singleflight function, so only the goroutine that actually performs the
	// load owns its cancel; a caller that returns early cannot abort the load for the others. Each
	// waiter still selects on its own ctx below.
	// | 共享加载不能继承首个调用方的取消：否则其它等待者会拿到首个调用方的 context 错误，
	// 即使自己的 context 仍然有效。分离出来的 context 在单飞函数内部创建，因此只有真正执行加载的
	// 协程持有它的 cancel；提前返回的调用方不会中断其它等待者的加载。每个等待者仍会在下方监听自己的 ctx。
	ch := c.group.DoChan(c.key(key), func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultLoadTimeout)
		defer cancel()
		// Re-check: another caller may have filled the key while this one waited. | 二次确认：等待单飞期间其它调用者可能已经完成回填。
		switch v, state, err := c.find(loadCtx, key); {
		case err != nil:
			return nil, err
		case state == entryHit:
			return v, nil
		case state == entryNegative:
			return nil, ErrNotFound
		}

		v, loadErr := load(loadCtx)
		if loadErr != nil {
			if errors.Is(loadErr, ErrNotFound) && c.opt.NegativeTTL > 0 {
				if writeErr := c.write(loadCtx, key, []byte(negativePayload), c.ttl(c.opt.NegativeTTL)); writeErr != nil {
					// The negative entry is not what the caller asked for: keep ErrNotFound and still surface the write failure. | 负缓存不是调用方要的结果：保留 ErrNotFound，同时不隐藏写入失败。
					return nil, errors.Join(loadErr, writeErr)
				}
			}
			return nil, loadErr
		}
		if writeErr := c.setValue(loadCtx, key, v); writeErr != nil {
			return nil, writeErr
		}
		return v, nil
	})

	var shared any
	var err error
	select {
	case res := <-ch:
		shared, err = res.Val, res.Err
	case <-ctx.Done():
		// The shared load keeps running for the other waiters under its own bounded context.
		// | 共享加载在自身的受限 context 下继续为其它等待者运行。
		return zero, ctx.Err()
	}
	if err != nil {
		return zero, err
	}
	if v, ok := shared.(T); ok {
		return v, nil
	}
	// Reached only when T is an interface type and the loader returned nil. | 仅当 T 是可空接口类型且 loader 返回 nil 时才会走到这里。
	return zero, nil
}

// Get returns the cached value for key; ok is false on a miss. | Get 返回 key 的缓存值；未命中时 ok 为 false。
//
// A negative entry is reported as a miss (ok == false, err == nil), so a caller never | 负缓存按未命中上报（ok == false、err == nil），
// mistakes the zero value for real data; use GetOrLoad to observe ErrNotFound instead. | 调用方不会把零值误当成真实数据；需要感知 ErrNotFound 时请用 GetOrLoad。
func (c *Cache[T]) Get(ctx context.Context, key string) (T, bool, error) {
	var zero T
	v, state, err := c.find(ctx, key)
	if err != nil {
		return zero, false, err
	}
	if state != entryHit {
		return zero, false, nil
	}
	return v, true, nil
}

// Set stores v under key with CacheOptions.TTL (plus jitter). | Set 以 CacheOptions.TTL（含抖动）把 v 存到 key 下。
//
// A value that serializes to JSON null is stored as a negative entry and is read back as a | 序列化结果为 JSON null 的取值会按负缓存条目存储，并被读取为未命中（例如 T 为指针类型时的 nil）。
// miss, for example a nil pointer when T is a pointer type.
func (c *Cache[T]) Set(ctx context.Context, key string, v T) error {
	return c.setValue(ctx, key, v)
}

// Delete removes the cached entry of key, whether it holds a value or a negative marker. | Delete 删除 key 的缓存条目，无论它是真实值还是负缓存标记。
func (c *Cache[T]) Delete(ctx context.Context, key string) error {
	if c.cli == nil {
		return ErrNilClient
	}
	if err := c.cli.Del(ctx, c.key(key)).Err(); err != nil {
		return errx.Wrap(err, ErrCacheWriteFailed)
	}
	return nil
}

// Invalidate is Delete under the name used when a cached entry went stale. | Invalidate 是 Delete 的语义别名，用于「缓存条目已过期」的场景。
//
// It is deliberately an alias: the decorator cannot cancel loads already in flight, so | 它刻意只是别名：装饰器无法取消已在途的加载，
// the contract is exactly "the next GetOrLoad goes to the loader again". | 其语义恰好是「下一次 GetOrLoad 重新走 loader」。
func (c *Cache[T]) Invalidate(ctx context.Context, key string) error {
	return c.Delete(ctx, key)
}

// LoadMany reads keys in pipelined batches and asks loader for the ones that missed. | LoadMany 以流水线分批读取 keys，并向 loader 索要未命中的部分。
//
// Reads are batched into pipelines of loadManyBatch commands; because each key keeps its own | 读取按 loadManyBatch 个命令组成一批流水线；每个键各占一条命令，因此即使底层是
// command, a cluster client routes every command to the node owning its key (no cross-slot | 集群客户端，命令也会被路由到持有该键的节点（不会出现跨槽失败），
// failure), and a single round trip carries the whole batch. Only the missing keys are passed | 而一次往返即可承载整批。只有缺失的键会传给 loader；loader 返回的取值经一次流水线
// to loader, and the loaded values are written back through a second pipeline. | 统一回填，返回的额外键被忽略。
//
// A loader error is returned as-is and nothing is written back; a negative entry counts as a | loader 失败时错误原样返回且不回填；负缓存标记按未命中处理，交由 loader 定夺。
// miss so the loader decides again. The result contains only the requested keys.
func (c *Cache[T]) LoadMany(ctx context.Context, keys []string, loader func(context.Context, []string) (map[string]T, error)) (map[string]T, error) {
	out := make(map[string]T, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	if loader == nil {
		return nil, ErrNilLoader
	}
	if c.cli == nil {
		return nil, ErrNilClient
	}

	// Dedupe: a repeated key is read once and handed to the loader once. | 去重：重复键只会读一次、也只交给 loader 一次。
	unique := make([]string, 0, len(keys))
	requested := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if _, dup := requested[k]; dup {
			continue
		}
		requested[k] = struct{}{}
		unique = append(unique, k)
	}

	missing := make([]string, 0, len(unique))
	for start := 0; start < len(unique); start += loadManyBatch {
		end := min(start+loadManyBatch, len(unique))
		batch := unique[start:end]

		pipe := c.cli.Pipeline()
		cmds := make([]*redis.StringCmd, 0, len(batch))
		for _, k := range batch {
			cmds = append(cmds, pipe.Get(ctx, c.key(k)))
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, errx.Wrap(err, ErrCacheReadFailed)
		}

		for i, cmd := range cmds {
			raw, err := cmd.Bytes()
			switch {
			case errors.Is(err, redis.Nil):
				missing = append(missing, batch[i])
			case err != nil:
				return nil, errx.Wrap(err, ErrCacheReadFailed)
			case isNegative(raw):
				missing = append(missing, batch[i])
			default:
				var v T
				if err := jsonx.Unmarshal(raw, &v); err != nil {
					return nil, errx.Wrap(err, ErrCacheDecodeFailed)
				}
				out[batch[i]] = v
			}
		}
	}
	if len(missing) == 0 {
		return out, nil
	}

	loaded, err := loader(ctx, missing)
	if err != nil {
		return nil, err
	}
	// Only the keys that actually missed may be written back and returned; a loader that returns a
	// superset (a preloaded map, say) must not overwrite a fresh cache hit or reset its TTL.
	// | 只有真正未命中的键可以回填与返回；loader 返回超集（例如预加载映射）时不得覆盖新鲜的
	// 缓存命中或重置其 TTL。
	missingSet := make(map[string]struct{}, len(missing))
	for _, k := range missing {
		missingSet[k] = struct{}{}
	}

	pipe := c.cli.Pipeline()
	writeBack := 0
	for k, v := range loaded {
		if _, ok := missingSet[k]; !ok {
			continue
		}
		raw, err := jsonx.Marshal(v)
		if err != nil {
			return nil, errx.Wrap(err, ErrCacheEncodeFailed)
		}
		pipe.Set(ctx, c.key(k), raw, c.ttl(c.opt.TTL))
		writeBack++
	}
	if writeBack > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, errx.Wrap(err, ErrCacheWriteFailed)
		}
	}
	for k, v := range loaded {
		if _, ok := missingSet[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

// find reads one key and classifies the reply. | find 读取单个键并对回复分类。
func (c *Cache[T]) find(ctx context.Context, key string) (T, entryState, error) {
	var zero T
	if c.cli == nil {
		return zero, entryMiss, ErrNilClient
	}
	raw, err := c.cli.Get(ctx, c.key(key)).Bytes()
	switch {
	case errors.Is(err, redis.Nil):
		return zero, entryMiss, nil
	case err != nil:
		return zero, entryMiss, errx.Wrap(err, ErrCacheReadFailed)
	case isNegative(raw):
		return zero, entryNegative, nil
	}
	var v T
	if err := jsonx.Unmarshal(raw, &v); err != nil {
		// An undecodable payload (old shape after a deploy, external writer) would otherwise keep
		// failing forever, and a TTL of zero would make the key permanently poisonous. Drop it on a
		// best-effort basis and report the miss so the loader can repair it. | 无法解码的载荷（部署后
		// 的旧结构、外部写入）否则会永远失败，而 TTL 为零会让该键永久中毒。尽力删除并报告未命中，
		// 让 loader 修复它。
		_ = c.cli.Del(ctx, c.key(key)).Err()
		return zero, entryMiss, errx.Wrap(err, ErrCacheDecodeFailed)
	}
	return v, entryHit, nil
}

// setValue serializes v and writes it with CacheOptions.TTL. | setValue 序列化 v 并以 CacheOptions.TTL 写入。
func (c *Cache[T]) setValue(ctx context.Context, key string, v T) error {
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return errx.Wrap(err, ErrCacheEncodeFailed)
	}
	return c.write(ctx, key, raw, c.ttl(c.opt.TTL))
}

// write stores payload under key with the given ttl; ttl <= 0 means no expiration. | write 以给定 ttl 把 payload 存到 key 下；ttl <= 0 表示不过期。
func (c *Cache[T]) write(ctx context.Context, key string, payload []byte, ttl time.Duration) error {
	if c.cli == nil {
		return ErrNilClient
	}
	if err := c.cli.Set(ctx, c.key(key), payload, ttl).Err(); err != nil {
		return errx.Wrap(err, ErrCacheWriteFailed)
	}
	return nil
}

// key applies the configured prefix. | key 施加已配置的前缀。
func (c *Cache[T]) key(key string) string {
	if c.opt.Prefix == "" {
		return key
	}
	return c.opt.Prefix + key
}

// ttl spreads ttl by CacheOptions.Jitter (0 <= jitter < Jitter). | ttl 按 CacheOptions.Jitter 抖动 ttl（0 <= 抖动 < Jitter）。
func (c *Cache[T]) ttl(ttl time.Duration) time.Duration {
	if ttl <= 0 || c.opt.Jitter <= 0 {
		return ttl
	}
	return ttl + jitter(c.opt.Jitter)
}

// isNegative reports whether a payload is the negative marker. | isNegative 判断载荷是否为负缓存标记。
func isNegative(raw []byte) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte(negativePayload))
}

// jitter returns a random duration in [0, d). | jitter 返回 [0, d) 内的随机时长。
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return rand.N(d)
}
