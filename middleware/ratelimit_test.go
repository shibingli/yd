package middleware

import (
	"context"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/http/router"
)

// rateLimitRouter builds a router limited by o with a counter handler. | rateLimitRouter 构造一个由 o 限流的路由器，处理器返回计数。
func rateLimitRouter(o RateLimitOptions, hit *int) *router.Router {
	r := router.New()
	r.Use(RateLimit(o))
	r.GET("/api", func(c *router.Ctx) {
		*hit++
		_ = c.String(http.StatusOK, "ok")
	})
	return r
}

func TestRateLimitBurstThen429WithHeaders(t *testing.T) {
	hits := 0
	r := rateLimitRouter(RateLimitOptions{Rate: 1, Burst: 3}, &hits)

	// The burst allowance must be fully usable, with a decreasing remaining counter. | 突发额度必须可用完，且剩余数逐个递减。
	wantRemaining := []string{"2", "1", "0"}
	for i := 0; i < 3; i++ {
		rec := doReq(r, http.MethodGet, "/api", nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次请求 code = %d, 期望 200", i+1, rec.Code)
		}
		if got := rec.Header().Get("X-RateLimit-Limit"); got != "3" {
			t.Errorf("第 %d 次 X-RateLimit-Limit = %q, 期望 3", i+1, got)
		}
		if got := rec.Header().Get("X-RateLimit-Remaining"); got != wantRemaining[i] {
			t.Errorf("第 %d 次 X-RateLimit-Remaining = %q, 期望 %q", i+1, got, wantRemaining[i])
		}
		if rec.Header().Get("X-RateLimit-Reset") == "" {
			t.Errorf("第 %d 次缺少 X-RateLimit-Reset", i+1)
		}
	}

	rec := doReq(r, http.MethodGet, "/api", nil, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超限 code = %d, 期望 429", rec.Code)
	}
	if hits != 3 {
		t.Fatalf("处理器命中 %d 次, 期望 3（超限请求不得进入处理器）", hits)
	}
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retry < 1 {
		t.Fatalf("Retry-After = %q, 期望 >= 1 的整数秒", rec.Header().Get("Retry-After"))
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got != "3" {
		t.Errorf("X-RateLimit-Limit = %q, 期望 3", got)
	}
	if got := rec.Header().Get("X-RateLimit-Remaining"); got != "0" {
		t.Errorf("X-RateLimit-Remaining = %q, 期望 0", got)
	}
	if got := rec.Header().Get("X-RateLimit-Reset"); got == "" {
		t.Error("missing X-RateLimit-Reset | 缺少 X-RateLimit-Reset")
	}
}

func TestRateLimitKeyFuncIsolation(t *testing.T) {
	hits := 0
	r := rateLimitRouter(RateLimitOptions{
		Rate:    1,
		Burst:   1,
		KeyFunc: func(c *router.Ctx) string { return c.Header("X-Api-Key") },
	}, &hits)

	if rec := doReq(r, http.MethodGet, "/api", map[string]string{"X-Api-Key": "a"}, ""); rec.Code != http.StatusOK {
		t.Fatalf("键 a 首次请求 code = %d, 期望 200", rec.Code)
	}
	if rec := doReq(r, http.MethodGet, "/api", map[string]string{"X-Api-Key": "a"}, ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("键 a 二次请求 code = %d, 期望 429", rec.Code)
	}
	if rec := doReq(r, http.MethodGet, "/api", map[string]string{"X-Api-Key": "b"}, ""); rec.Code != http.StatusOK {
		t.Fatalf("键 b 首次请求 code = %d, 期望 200（桶相互隔离）", rec.Code)
	}
	if hits != 2 {
		t.Fatalf("处理器命中 %d 次, 期望 2", hits)
	}
}

// Without a KeyFunc the client IP from RealIP is used. | 未提供 KeyFunc 时使用 RealIP 解析出的客户端地址。
func TestRateLimitDefaultKeyUsesRealIP(t *testing.T) {
	hits := 0
	r := rateLimitRouter(RateLimitOptions{Rate: 1, Burst: 1}, &hits)

	if rec := doReqFrom(r, http.MethodGet, "/api", "10.0.0.1:1111", nil); rec.Code != http.StatusOK {
		t.Fatalf("首个 IP code = %d, 期望 200", rec.Code)
	}
	if rec := doReqFrom(r, http.MethodGet, "/api", "10.0.0.1:2222", nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("同一 IP 不同端口 code = %d, 期望 429（按 IP 聚合）", rec.Code)
	}
	if rec := doReqFrom(r, http.MethodGet, "/api", "10.0.0.2:1111", nil); rec.Code != http.StatusOK {
		t.Fatalf("另一 IP code = %d, 期望 200", rec.Code)
	}
}

func TestRateLimitRefillsOverTime(t *testing.T) {
	hits := 0
	r := rateLimitRouter(RateLimitOptions{Rate: 200, Burst: 1}, &hits)

	if rec := doReq(r, http.MethodGet, "/api", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("首次 code = %d, 期望 200", rec.Code)
	}
	if rec := doReq(r, http.MethodGet, "/api", nil, ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("立即重试 code = %d, 期望 429", rec.Code)
	}
	time.Sleep(60 * time.Millisecond) // 200 tokens/s → 60 ms refills more than one token | 200 tokens/s → 60ms 补充 >1 个令牌
	if rec := doReq(r, http.MethodGet, "/api", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("等待补充后 code = %d, 期望 200", rec.Code)
	}
}

// Lazy cleanup must not panic nor lose the accounting when many keys churn. | 惰性清理在大量键更替时不得 panic 或丢失计数。
func TestRateLimitLazyCleanupAndSharding(t *testing.T) {
	r := router.New()
	r.Use(RateLimit(RateLimitOptions{
		Rate:            10, // 1 token per 100ms: three rapid calls cannot refill. | 每 100ms 补 1 个令牌，连续三次调用来不及补充。
		Burst:           2,
		CleanupInterval: time.Millisecond,
		KeyFunc:         func(c *router.Ctx) string { return c.Query("k") },
	}))
	r.GET("/api", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })

	for i := 0; i < 200; i++ {
		key := "key-" + strconv.Itoa(i)
		if rec := doReq(r, http.MethodGet, "/api?k="+key, nil, ""); rec.Code != http.StatusOK {
			t.Fatalf("第 %d 个键首次请求 code = %d", i, rec.Code)
		}
		if rec := doReq(r, http.MethodGet, "/api?k="+key, nil, ""); rec.Code != http.StatusOK {
			t.Fatalf("第 %d 个键二次请求 code = %d", i, rec.Code)
		}
		if rec := doReq(r, http.MethodGet, "/api?k="+key, nil, ""); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("第 %d 个键三次请求 code = %d, 期望 429", i, rec.Code)
		}
	}
}

// errorStore fails every decision, for exercising the failure policy. | errorStore 让每次判定都失败，用于验证故障策略。
type errorStore struct{ err error }

// Allow implements LimitStore. | Allow 实现 LimitStore。
func (s errorStore) Allow(context.Context, string, float64, int) (Decision, error) {
	return Decision{}, s.err
}

// TestRateLimitDefaultIgnoresSpoofedXFF is the adversarial case: the same peer cannot create a new
// bucket by changing a header. | TestRateLimitDefaultIgnoresSpoofedXFF 是对抗性用例：同一对端不能靠改头部换一个新桶。
func TestRateLimitDefaultIgnoresSpoofedXFF(t *testing.T) {
	hits := 0
	r := rateLimitRouter(RateLimitOptions{Rate: 1, Burst: 1}, &hits)
	if rec := doReqFrom(r, http.MethodGet, "/api", "203.0.113.9:1111", map[string]string{"X-Forwarded-For": "1.2.3.4"}); rec.Code != http.StatusOK {
		t.Fatalf("first code = %d, want 200", rec.Code)
	}
	if rec := doReqFrom(r, http.MethodGet, "/api", "203.0.113.9:2222", map[string]string{"X-Forwarded-For": "5.6.7.8"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed XFF code = %d, want 429 (same peer, same bucket)", rec.Code)
	}
	if hits != 1 {
		t.Fatalf("handler hits = %d, want 1", hits)
	}
}

// TestRateLimitTrustedProxySeparatesForwardedClients proves the trusted-proxy path still keys by
// the real client. | TestRateLimitTrustedProxySeparatesForwardedClients 证明可信代理路径仍按真实客户端取键。
func TestRateLimitTrustedProxySeparatesForwardedClients(t *testing.T) {
	hits := 0
	r := rateLimitRouter(RateLimitOptions{
		Rate:           1,
		Burst:          1,
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	}, &hits)
	if rec := doReqFrom(r, http.MethodGet, "/api", "10.0.0.5:1", map[string]string{"X-Forwarded-For": "1.2.3.4"}); rec.Code != http.StatusOK {
		t.Fatalf("client A first code = %d, want 200", rec.Code)
	}
	if rec := doReqFrom(r, http.MethodGet, "/api", "10.0.0.5:2", map[string]string{"X-Forwarded-For": "5.6.7.8"}); rec.Code != http.StatusOK {
		t.Fatalf("client B first code = %d, want 200 (separate bucket)", rec.Code)
	}
	if rec := doReqFrom(r, http.MethodGet, "/api", "10.0.0.5:3", map[string]string{"X-Forwarded-For": "1.2.3.4"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("client A second code = %d, want 429", rec.Code)
	}
	if hits != 2 {
		t.Fatalf("handler hits = %d, want 2", hits)
	}
}

// TestRateLimitStoreFailurePolicies pins fail-open and fail-closed. | TestRateLimitStoreFailurePolicies 钉住失败开放与失败关闭。
func TestRateLimitStoreFailurePolicies(t *testing.T) {
	boom := errx.New("boom | 故障")
	openHits := 0
	open := rateLimitRouter(RateLimitOptions{Rate: 1, Burst: 1, Store: errorStore{err: boom}}, &openHits)
	if rec := doReq(open, http.MethodGet, "/api", nil, ""); rec.Code != http.StatusOK || openHits != 1 {
		t.Fatalf("fail-open code = %d hits = %d, want 200/1", rec.Code, openHits)
	}

	closedHits := 0
	closed := rateLimitRouter(RateLimitOptions{Rate: 1, Burst: 1, Store: errorStore{err: boom}, FailClosed: true}, &closedHits)
	if rec := doReq(closed, http.MethodGet, "/api", nil, ""); rec.Code != http.StatusServiceUnavailable || closedHits != 0 {
		t.Fatalf("fail-closed code = %d hits = %d, want 503/0", rec.Code, closedHits)
	}
}

// TestRateLimitSharedStorePrefixIsolatesRules proves that one backend can serve several rules.
// | TestRateLimitSharedStorePrefixIsolatesRules 证明同一后端可服务多条互不干扰的规则。
func TestRateLimitSharedStorePrefixIsolatesRules(t *testing.T) {
	store := NewMemoryStore(0)
	newRouter := func(prefix string, hits *int) *router.Router {
		r := router.New()
		r.Use(RateLimit(RateLimitOptions{Rate: 1, Burst: 1, Store: store, KeyPrefix: prefix}))
		r.GET("/api", func(c *router.Ctx) {
			*hits++
			_ = c.String(http.StatusOK, "ok")
		})
		return r
	}
	hitsA, hitsB := 0, 0
	a := newRouter("rule-a:", &hitsA)
	b := newRouter("rule-b:", &hitsB)
	if rec := doReq(a, http.MethodGet, "/api", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("rule A first = %d, want 200", rec.Code)
	}
	if rec := doReq(b, http.MethodGet, "/api", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("rule B first = %d, want 200 (isolated from A)", rec.Code)
	}
	if rec := doReq(a, http.MethodGet, "/api", nil, ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rule A second = %d, want 429", rec.Code)
	}
}

// TestMemoryStoreConcurrentExactBurst proves the atomicity contract under contention. | TestMemoryStoreConcurrentExactBurst 在高并发下证明原子性约定。
func TestMemoryStoreConcurrentExactBurst(t *testing.T) {
	store := NewMemoryStore(0)
	const burst = 5
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dec, err := store.Allow(context.Background(), "shared-key", 0.000001, burst)
			if err != nil {
				t.Errorf("Allow: %v", err)
				return
			}
			if dec.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != burst {
		t.Fatalf("allowed = %d, want exactly %d", got, burst)
	}
}

// TestMemoryStoreZeroValueUsable keeps the exported type safe without a constructor. | TestMemoryStoreZeroValueUsable 保证导出类型不依赖构造函数也可用。
func TestMemoryStoreZeroValueUsable(t *testing.T) {
	var store MemoryStore
	first, err := store.Allow(context.Background(), "k", 1, 1)
	if err != nil || !first.Allowed {
		t.Fatalf("first = %+v err=%v, want allowed", first, err)
	}
	second, err := store.Allow(context.Background(), "k", 1, 1)
	if err != nil || second.Allowed {
		t.Fatalf("second = %+v err=%v, want denied", second, err)
	}
}

// TestMemoryStoreExtremeRateDoesNotOverflow keeps duration math positive at absurd rates. | TestMemoryStoreExtremeRateDoesNotOverflow 在极端速率下保持时长非负。
func TestMemoryStoreExtremeRateDoesNotOverflow(t *testing.T) {
	store := NewMemoryStore(0)
	ctx := context.Background()
	if dec, err := store.Allow(ctx, "slow", 1e-12, 1); err != nil || !dec.Allowed {
		t.Fatalf("first = %+v err=%v, want allowed", dec, err)
	}
	dec, err := store.Allow(ctx, "slow", 1e-12, 1)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if dec.Allowed || dec.RetryAfter <= 0 || dec.ResetAfter <= 0 {
		t.Fatalf("second = %+v, want denied with positive durations", dec)
	}
}
