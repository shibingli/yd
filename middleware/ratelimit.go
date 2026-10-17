package middleware

import (
	"math"
	"net/http"
	"net/textproto"
	"strconv"
	"sync"
	"time"

	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/http/router"
)

// rateLimitShards is the number of independently locked buckets maps.
// | rateLimitShards 是相互独立加锁的桶映射分片数。
const rateLimitShards = 16

// rateLimitHeaderKeys holds the canonical keys of the rate-limit headers, computed once.
// | rateLimitHeaderKeys 保存限流响应头的规范 key，只计算一次。
//
// The public spelling stays X-RateLimit-*; Go's canonical form lowercases the tail of each
// hyphenated word, and precomputing it avoids re-canonicalizing (and heap-copying) the three
// keys on every request. Lookups through http.Header remain case-insensitive. | 对外拼写仍是
// X-RateLimit-*；Go 的规范形式会把每个连字符词的尾部小写，预计算可避免每请求重新规范化
// （并堆复制）这三个 key。通过 http.Header 的读取仍然大小写不敏感。
var rateLimitHeaderKeys = [...]string{
	textproto.CanonicalMIMEHeaderKey("X-RateLimit-Limit"),
	textproto.CanonicalMIMEHeaderKey("X-RateLimit-Remaining"),
	textproto.CanonicalMIMEHeaderKey("X-RateLimit-Reset"),
}

// rateLimitRetryKey is the canonical Retry-After key. | rateLimitRetryKey 是 Retry-After 的规范 key。
var rateLimitRetryKey = textproto.CanonicalMIMEHeaderKey("Retry-After")

// defaultRateLimitCleanup is the lazy sweep interval used when CleanupInterval is zero.
// | defaultRateLimitCleanup 是 CleanupInterval 为零时使用的惰性清理间隔。
const defaultRateLimitCleanup = time.Minute

// RateLimitOptions configures the token-bucket rate limiter.
// | RateLimitOptions 配置令牌桶限流器。
type RateLimitOptions struct {
	// KeyFunc derives the bucket key; nil means the client IP from Ctx.RealIP. | KeyFunc 派生桶的键；nil 表示使用 Ctx.RealIP 得到的客户端地址。
	KeyFunc func(*router.Ctx) string
	// Logger, when non-nil, records every throttled request. | Logger 非 nil 时记录每一次被限流的请求。
	Logger *logx.Logger
	// Rate is the number of tokens replenished per second. | Rate 是每秒补充的令牌数。
	Rate float64
	// Burst is the bucket capacity, i.e. how many requests may pass at once. | Burst 是桶容量，即一次性允许通过的请求数。
	Burst int
	// CleanupInterval is the minimum delay between two lazy sweeps. | CleanupInterval 是两次惰性清理之间的最小间隔。
	CleanupInterval time.Duration
}

// rateLimiter holds the sharded token buckets. | rateLimiter 持有分片令牌桶。
type rateLimiter struct {
	keyFunc    func(*router.Ctx) string
	logger     *logx.Logger
	limitValue string
	shards     [rateLimitShards]rateLimitShard
	rate       float64
	burst      float64
	burstInt   int
	interval   time.Duration
	mask       uint32
}

// rateLimitShard is one independently locked shard. | rateLimitShard 是一个独立加锁的分片。
type rateLimitShard struct {
	lastSweep time.Time
	buckets   map[string]*tokenBucket
	mu        sync.Mutex
}

// tokenBucket is a single client's bucket state. | tokenBucket 是单个客户端的桶状态。
type tokenBucket struct {
	last   time.Time
	tokens float64
}

// newRateLimiter normalizes o into a usable limiter. | newRateLimiter 把 o 归一化为可用的限流器。
func newRateLimiter(o RateLimitOptions) *rateLimiter {
	lim := &rateLimiter{
		rate:     o.Rate,
		burstInt: o.Burst,
		interval: o.CleanupInterval,
		keyFunc:  o.KeyFunc,
		logger:   o.Logger,
		mask:     rateLimitShards - 1,
	}
	if lim.rate <= 0 {
		lim.rate = 1
	}
	if lim.burstInt <= 0 {
		lim.burstInt = int(math.Ceil(lim.rate))
		if lim.burstInt < 1 {
			lim.burstInt = 1
		}
	}
	lim.burst = float64(lim.burstInt)
	lim.limitValue = strconv.Itoa(lim.burstInt)
	if lim.interval <= 0 {
		lim.interval = defaultRateLimitCleanup
	}
	if lim.keyFunc == nil {
		lim.keyFunc = func(c *router.Ctx) string { return c.RealIP() }
	}
	for i := range lim.shards {
		lim.shards[i].buckets = make(map[string]*tokenBucket, 8)
	}
	return lim
}

// shardIndexOf hashes key with FNV-1a into a shard index. | shardIndexOf 用 FNV-1a 把 key 哈希到分片下标。
func (lim *rateLimiter) shardIndexOf(key string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return h & lim.mask
}

// allowable is the outcome of one token acquisition. | allowable 是一次取令牌的结果。
type allowable struct {
	ok        bool
	remaining int
	retry     time.Duration
	reset     time.Duration
}

// allow tries to take one token for key at time now. | allow 尝试为 key 在 now 时刻取走一个令牌。
func (lim *rateLimiter) allow(key string, now time.Time) allowable {
	shard := &lim.shards[lim.shardIndexOf(key)]
	shard.mu.Lock()
	defer shard.mu.Unlock()

	if now.Sub(shard.lastSweep) >= lim.interval {
		lim.sweepLocked(shard, now)
		shard.lastSweep = now
	}

	b, ok := shard.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: lim.burst, last: now}
		shard.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(lim.burst, b.tokens+elapsed*lim.rate)
		b.last = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return allowable{ok: true, remaining: int(b.tokens), reset: lim.resetAfter(b.tokens)}
	}
	need := 1 - b.tokens
	wait := time.Duration(need / lim.rate * float64(time.Second))
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	return allowable{ok: false, retry: wait, reset: wait}
}

// resetAfter is the time until the bucket is full again. | resetAfter 是桶重新装满所需的时间。
func (lim *rateLimiter) resetAfter(tokens float64) time.Duration {
	if tokens >= lim.burst {
		return 0
	}
	return time.Duration((lim.burst - tokens) / lim.rate * float64(time.Second))
}

// joinNumbers renders a and b into one string and returns the two halves. | joinNumbers 把 a 与 b
// 渲染进同一个字符串，并返回两半。
// Both returned strings share one allocation, which is why a pair of numeric header values costs
// a single formatter allocation. | 两个返回的字符串共享同一次分配，因此一对数值头部值只需一次
// 格式化分配。
func joinNumbers(a, b int64) (string, string) {
	// The scratch array is large enough for two full int64 values and the separator.
	// | 暂存数组足以容纳两个完整 int64 值与分隔符。
	var scratch [41]byte
	out := strconv.AppendInt(scratch[:0], a, 10)
	split := len(out)
	out = append(out, '|')
	out = strconv.AppendInt(out, b, 10)
	s := string(out)
	return s[:split], s[split+1:]
}

// sweepLocked drops buckets that have become indistinguishable from a fresh one.
// | sweepLocked 丢弃那些已经与新建桶没有区别的桶。
func (lim *rateLimiter) sweepLocked(shard *rateLimitShard, now time.Time) {
	for key, b := range shard.buckets {
		if b.tokens >= lim.burst {
			delete(shard.buckets, key)
			continue
		}
		if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 && b.tokens+elapsed*lim.rate >= lim.burst {
			delete(shard.buckets, key)
		}
	}
}

// RateLimit returns a middleware backed by sharded token buckets. | RateLimit 返回分片令牌桶限流中间件。
// Over-limit requests are answered with 429 plus Retry-After and the X-RateLimit-Limit,
// X-RateLimit-Remaining and X-RateLimit-Reset headers. | 超限请求返回 429，并带上 Retry-After 与 X-RateLimit-Limit、X-RateLimit-Remaining、X-RateLimit-Reset。
// The same three headers are also set on allowed responses. | 被放行的响应同样携带这三个头部。
// Buckets are swept lazily, so the middleware needs no goroutine of its own. | 桶采用惰性清理，因此不占用任何后台 goroutine。
func RateLimit(o RateLimitOptions) router.Middleware {
	lim := newRateLimiter(o)
	return func(c *router.Ctx) {
		key := lim.keyFunc(c)
		res := lim.allow(key, time.Now())

		// The three numeric values share one backing string and one slice array, so the
		// allowed path costs two allocations instead of the former eight. | 三个数值共享
		// 一个后备字符串与一个切片数组，使放行路径的分配从原来的八次降到两次。
		remaining, reset := joinNumbers(int64(res.remaining), int64(math.Ceil(res.reset.Seconds())))
		vals := new([3]string)
		vals[0], vals[1], vals[2] = lim.limitValue, remaining, reset
		h := c.Writer.Header()
		h[rateLimitHeaderKeys[0]] = vals[0:1:1]
		h[rateLimitHeaderKeys[1]] = vals[1:2:2]
		h[rateLimitHeaderKeys[2]] = vals[2:3:3]

		if res.ok {
			c.Next()
			return
		}

		wait := int(math.Ceil(res.retry.Seconds()))
		if wait < 1 {
			wait = 1
		}
		h[rateLimitRetryKey] = []string{strconv.Itoa(wait)}
		if lim.logger != nil {
			lim.logger.Warn("rate limit exceeded | 触发限流",
				logx.Str("key", key),
				logx.Int("limit", lim.burstInt),
				logx.Float64("rate", lim.rate),
				logx.Str("path", c.Request.URL.Path),
				logx.Str("remote_ip", c.RealIP()),
			)
		}
		c.Abort()
		_ = c.String(http.StatusTooManyRequests, http.StatusText(http.StatusTooManyRequests))
	}
}
