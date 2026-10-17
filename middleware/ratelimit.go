package middleware

import (
	"math"
	"net/http"
	"net/netip"
	"net/textproto"
	"strconv"
	"time"

	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/http/router"
)

// rateLimitHeaderKeys holds the canonical keys of the rate-limit headers, computed once. | rateLimitHeaderKeys 保存限流响应头的规范 key，只计算一次。
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

// RateLimitOptions configures the token-bucket rate limiter. | RateLimitOptions 配置令牌桶限流器。
type RateLimitOptions struct {
	// Store is the limiting backend; nil means a private MemoryStore. Share one store across
	// middlewares only together with distinct KeyPrefix values. | Store 是限流后端；nil 表示私有的
	// MemoryStore。跨中间件共享同一存储时必须配合不同的 KeyPrefix。
	Store LimitStore
	// KeyFunc derives the bucket key; nil means KeyRemoteIP with TrustedProxies. | KeyFunc 派生桶的键；nil 表示使用 TrustedProxies 的 KeyRemoteIP。
	KeyFunc KeyFunc
	// Logger, when non-nil, records every throttled request and every store failure. | Logger 非 nil 时记录每一次被限流的请求与每一次存储故障。
	Logger *logx.Logger
	// KeyPrefix is prepended to every derived key, separating rules that share a store. | KeyPrefix
	// 会加到每个派生键之前，用于隔离共享同一存储的多条规则。
	KeyPrefix string
	// TrustedProxies lists the reverse proxies allowed to supply X-Forwarded-For. Empty means the
	// peer address is the client address and proxy headers are ignored. | TrustedProxies 列出允许提供
	// X-Forwarded-For 的反向代理；为空表示以对端地址作为客户端地址并忽略代理头。
	TrustedProxies []netip.Prefix
	// Rate is the number of tokens replenished per second. | Rate 是每秒补充的令牌数。
	Rate float64
	// Burst is the bucket capacity, i.e. how many requests may pass at once. | Burst 是桶容量，即一次性允许通过的请求数。
	Burst int
	// CleanupInterval is the minimum delay between two lazy sweeps of a private MemoryStore. | CleanupInterval 是私有 MemoryStore 两次惰性清理之间的最小间隔。
	CleanupInterval time.Duration
	// FailClosed rejects requests when the store fails; the default (false) fails open. | FailClosed 在存储故障时拒绝请求；默认（false）失败开放。
	//
	// Fail-open keeps a Redis outage from taking the whole service down; fail-closed is the choice
	// when the limit protects a scarce resource that must not be oversubscribed during an outage.
	// | 失败开放避免 Redis 故障拖垮整个服务；当限流保护的是故障期间绝不可超卖的资源时，选择失败关闭。
	FailClosed bool
}

// rateLimiter holds the normalized options shared by every request. | rateLimiter 持有每个请求共享的归一化选项。
type rateLimiter struct {
	store      LimitStore
	keyFunc    KeyFunc
	logger     *logx.Logger
	limitValue string
	keyPrefix  string
	trusted    []netip.Prefix
	rate       float64
	burst      int
	failClosed bool
}

// newRateLimiter normalizes o into a usable limiter. | newRateLimiter 把 o 归一化为可用的限流器。
func newRateLimiter(o RateLimitOptions) *rateLimiter {
	rate, burst := normalizeLimit(o.Rate, o.Burst)
	store := o.Store
	if store == nil {
		store = NewMemoryStore(o.CleanupInterval)
	}
	keyFunc := o.KeyFunc
	if keyFunc == nil {
		keyFunc = KeyRemoteIP(o.TrustedProxies)
	}
	return &rateLimiter{
		rate:       rate,
		burst:      burst,
		store:      store,
		keyFunc:    keyFunc,
		logger:     o.Logger,
		trusted:    o.TrustedProxies,
		keyPrefix:  o.KeyPrefix,
		limitValue: strconv.Itoa(burst),
		failClosed: o.FailClosed,
	}
}

// joinNumbers renders a and b into one string and returns the two halves. | joinNumbers 把 a 与 b
// 渲染进同一个字符串，并返回两半。
//
// Both returned strings share one allocation, which is why a pair of numeric header values costs
// a single formatter allocation. | 两个返回的字符串共享同一次分配，因此一对数值头部值只需一次
// 格式化分配。
func joinNumbers(a, b int64) (string, string) {
	var scratch [41]byte
	out := strconv.AppendInt(scratch[:0], a, 10)
	split := len(out)
	out = append(out, '|')
	out = strconv.AppendInt(out, b, 10)
	s := string(out)
	return s[:split], s[split+1:]
}

// writeRateLimitHeaders writes the three X-RateLimit-* headers shared by every outcome. | writeRateLimitHeaders 写出所有结果共有的三个 X-RateLimit-* 头部。
func writeRateLimitHeaders(h http.Header, limitValue string, dec Decision) {
	remaining, reset := joinNumbers(int64(dec.Remaining), int64(math.Ceil(dec.ResetAfter.Seconds())))
	vals := new([3]string)
	vals[0], vals[1], vals[2] = limitValue, remaining, reset
	h[rateLimitHeaderKeys[0]] = vals[0:1:1]
	h[rateLimitHeaderKeys[1]] = vals[1:2:2]
	h[rateLimitHeaderKeys[2]] = vals[2:3:3]
}

// RateLimit returns a middleware backed by the configured LimitStore. | RateLimit 返回由配置的 LimitStore 支撑的限流中间件。
//
// Over-limit requests are answered with 429 plus Retry-After and the X-RateLimit-Limit,
// X-RateLimit-Remaining and X-RateLimit-Reset headers. The same three headers are also set on
// allowed responses. A store failure follows FailClosed: by default the request is allowed and a
// warning is logged; with FailClosed it is answered with 503. | 超限请求返回 429，并带上 Retry-After
// 与 X-RateLimit-Limit、X-RateLimit-Remaining、X-RateLimit-Reset；被放行的响应同样携带这三个头部。
// 存储故障遵循 FailClosed：默认放行并记录告警；开启 FailClosed 时返回 503。
func RateLimit(o RateLimitOptions) router.Middleware {
	lim := newRateLimiter(o)
	return func(c *router.Ctx) {
		key := lim.keyFunc(c)
		if lim.keyPrefix != "" {
			key = lim.keyPrefix + key
		}
		dec, err := lim.store.Allow(c.Request.Context(), key, lim.rate, lim.burst)
		if err != nil {
			lim.logStoreFailure(c, key, err)
			if lim.failClosed {
				c.Abort()
				_ = c.String(http.StatusServiceUnavailable, "rate limit store unavailable | 限流存储不可用")
				return
			}
			c.Next()
			return
		}

		writeRateLimitHeaders(c.Writer.Header(), lim.limitValue, dec)
		if dec.Allowed {
			c.Next()
			return
		}

		wait := int(math.Ceil(dec.RetryAfter.Seconds()))
		if wait < 1 {
			wait = 1
		}
		c.Writer.Header()[rateLimitRetryKey] = []string{strconv.Itoa(wait)}
		if lim.logger != nil {
			lim.logger.Warn("rate limit exceeded | 触发限流",
				logx.Str("key", key),
				logx.Int("limit", lim.burst),
				logx.Float64("rate", lim.rate),
				logx.Str("path", c.Request.URL.Path),
				logx.Str("remote_ip", c.ClientIP(lim.trusted)),
			)
		}
		c.Abort()
		_ = c.String(http.StatusTooManyRequests, "too many requests | 请求过于频繁")
	}
}

// logStoreFailure records a backend failure without ever panicking. | logStoreFailure 记录后端故障，且绝不 panic。
func (lim *rateLimiter) logStoreFailure(c *router.Ctx, key string, err error) {
	if lim.logger == nil {
		return
	}
	lim.logger.Warn("rate limit store failed | 限流存储故障",
		logx.Str("key", key),
		logx.Str("path", c.Request.URL.Path),
		logx.Str("remote_ip", c.ClientIP(lim.trusted)),
		logx.Str("error", err.Error()),
	)
}
