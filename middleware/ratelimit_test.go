package middleware

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/shibingli/yd/router"
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
		t.Error("缺少 X-RateLimit-Reset")
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
	time.Sleep(60 * time.Millisecond) // 200 tokens/s → 60ms 补充 >1 个令牌
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
