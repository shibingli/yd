package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/shibingli/yd/http/router"
)

// newRedisStore starts an in-process Redis and returns the store and the client. | newRedisStore 启动进程内 Redis，并返回存储与客户端。
func newRedisStore(t *testing.T, prefix string) (*RedisStore, *miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()
	mr := miniredis.RunT(t)
	cli := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = cli.Close() })
	store, err := NewRedisStore(RedisStoreOptions{Client: cli, Prefix: prefix})
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}
	return store, mr, cli
}

// TestRedisStoreBurstAndRefill covers the token bucket against the server clock. | TestRedisStoreBurstAndRefill 用服务器时钟验证令牌桶。
func TestRedisStoreBurstAndRefill(t *testing.T) {
	store, mr, _ := newRedisStore(t, "")
	ctx := context.Background()
	// FastForward only advances TTLs; TIME follows SetTime. | FastForward 只推进 TTL，TIME 跟随 SetTime。
	start := time.Now()
	mr.SetTime(start)
	first, err := store.Allow(ctx, "k", 1, 2)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !first.Allowed || first.Remaining != 1 {
		t.Fatalf("first = %+v, want allowed with remaining 1", first)
	}
	second, err := store.Allow(ctx, "k", 1, 2)
	if err != nil || !second.Allowed || second.Remaining != 0 {
		t.Fatalf("second = %+v err=%v, want allowed with remaining 0", second, err)
	}
	third, err := store.Allow(ctx, "k", 1, 2)
	if err != nil || third.Allowed || third.RetryAfter <= 0 {
		t.Fatalf("third = %+v err=%v, want denied with retry", third, err)
	}
	mr.SetTime(start.Add(time.Second))
	refilled, err := store.Allow(ctx, "k", 1, 2)
	if err != nil || !refilled.Allowed {
		t.Fatalf("after refill = %+v err=%v, want allowed", refilled, err)
	}
}

// TestRedisStoreTwoInstancesShareTheLimit proves that the limit does not multiply with replicas.
// | TestRedisStoreTwoInstancesShareTheLimit 证明限流总额不随副本数放大。
func TestRedisStoreTwoInstancesShareTheLimit(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()
	stores := make([]*RedisStore, 2)
	for i := range stores {
		cli := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
		t.Cleanup(func() { _ = cli.Close() })
		store, err := NewRedisStore(RedisStoreOptions{Client: cli, Prefix: "shared:"})
		if err != nil {
			t.Fatalf("NewRedisStore: %v", err)
		}
		stores[i] = store
	}
	allowed := 0
	for i := 0; i < 10; i++ {
		dec, err := stores[i%2].Allow(ctx, "client", 0.000001, 5)
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if dec.Allowed {
			allowed++
		}
	}
	if allowed != 5 {
		t.Fatalf("allowed = %d across two instances, want exactly 5", allowed)
	}
}

// TestRedisStoreConcurrentExactBurst proves the Lua script is atomic under contention. | TestRedisStoreConcurrentExactBurst 证明 Lua 脚本在高并发下是原子的。
func TestRedisStoreConcurrentExactBurst(t *testing.T) {
	store, _, _ := newRedisStore(t, "")
	const burst = 7
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dec, err := store.Allow(context.Background(), "contended", 0.000001, burst)
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

// TestRateLimitRedisMiddleware covers headers and the 429 path through the middleware. | TestRateLimitRedisMiddleware 覆盖经中间件的响应头与 429 路径。
func TestRateLimitRedisMiddleware(t *testing.T) {
	store, _, _ := newRedisStore(t, "mw:")
	hits := 0
	r := router.New()
	r.Use(RateLimit(RateLimitOptions{Rate: 1, Burst: 2, Store: store}))
	r.GET("/api", func(c *router.Ctx) {
		hits++
		_ = c.String(http.StatusOK, "ok")
	})
	for i := 0; i < 2; i++ {
		rec := doReq(r, http.MethodGet, "/api", nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d code = %d, want 200", i+1, rec.Code)
		}
		if got := rec.Header().Get("X-RateLimit-Limit"); got != "2" {
			t.Errorf("request %d Limit = %q, want 2", i+1, got)
		}
	}
	rec := doReq(r, http.MethodGet, "/api", nil, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third code = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}
	if hits != 2 {
		t.Fatalf("handler hits = %d, want 2", hits)
	}
}

// TestRateLimitRedisFailurePolicies covers both policies when Redis is gone. | TestRateLimitRedisFailurePolicies 覆盖 Redis 不可用时的两种策略。
func TestRateLimitRedisFailurePolicies(t *testing.T) {
	mr := miniredis.RunT(t)
	addr := mr.Addr()
	mr.Close()
	newStore := func(t *testing.T) *RedisStore {
		cli := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
		t.Cleanup(func() { _ = cli.Close() })
		store, err := NewRedisStore(RedisStoreOptions{Client: cli})
		if err != nil {
			t.Fatalf("NewRedisStore: %v", err)
		}
		return store
	}

	openHits := 0
	open := rateLimitRouter(RateLimitOptions{Rate: 1, Burst: 1, Store: newStore(t)}, &openHits)
	if rec := doReq(open, http.MethodGet, "/api", nil, ""); rec.Code != http.StatusOK || openHits != 1 {
		t.Fatalf("fail-open code = %d hits = %d, want 200/1", rec.Code, openHits)
	}

	closedHits := 0
	closed := rateLimitRouter(RateLimitOptions{Rate: 1, Burst: 1, Store: newStore(t), FailClosed: true}, &closedHits)
	if rec := doReq(closed, http.MethodGet, "/api", nil, ""); rec.Code != http.StatusServiceUnavailable || closedHits != 0 {
		t.Fatalf("fail-closed code = %d hits = %d, want 503/0", rec.Code, closedHits)
	}
}

// TestRedisStoreKeyPrefixesAreIsolated proves that two rules can share one Redis. | TestRedisStoreKeyPrefixesAreIsolated 证明两条规则可共享同一个 Redis。
func TestRedisStoreKeyPrefixesAreIsolated(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()
	mk := func(prefix string) *RedisStore {
		cli := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
		t.Cleanup(func() { _ = cli.Close() })
		store, err := NewRedisStore(RedisStoreOptions{Client: cli, Prefix: prefix})
		if err != nil {
			t.Fatalf("NewRedisStore: %v", err)
		}
		return store
	}
	a, b := mk("a:"), mk("b:")
	if dec, _ := a.Allow(ctx, "same", 0.000001, 1); !dec.Allowed {
		t.Fatal("rule A first request denied")
	}
	if dec, _ := b.Allow(ctx, "same", 0.000001, 1); !dec.Allowed {
		t.Fatal("rule B first request denied; prefixes are not isolated")
	}
	if dec, _ := a.Allow(ctx, "same", 0.000001, 1); dec.Allowed {
		t.Fatal("rule A second request allowed; bucket is not shared within a rule")
	}
}

// TestRedisBucketKey keeps derived keys printable and bounded. | TestRedisBucketKey 让派生键保持可打印且有界。
func TestRedisBucketKey(t *testing.T) {
	if got := redisBucketKey("10.0.0.1:/api"); got != "10.0.0.1:/api" {
		t.Errorf("short printable key = %q, want unchanged", got)
	}
	long := strings.Repeat("x", redisKeyMaxLength+1)
	if got := redisBucketKey(long); !strings.HasPrefix(got, "h:") || len(got) > 32 {
		t.Errorf("long key = %q, want a short hash", got)
	}
	if got := redisBucketKey("bad\nkey"); !strings.HasPrefix(got, "h:") {
		t.Errorf("control-character key = %q, want a hash", got)
	}
}

// TestNewRedisStoreRequiresClient rejects a nil client instead of panicking later. | TestNewRedisStoreRequiresClient 拒绝空客户端而不是在后续 panic。
func TestNewRedisStoreRequiresClient(t *testing.T) {
	if _, err := NewRedisStore(RedisStoreOptions{}); err == nil {
		t.Fatal("NewRedisStore(nil client) returned nil error")
	}
}

// TestRedisStoreTTLClamps keeps the derived TTL inside its documented bounds. | TestRedisStoreTTLClamps 让推导出的 TTL 保持在文档化范围内。
func TestRedisStoreTTLClamps(t *testing.T) {
	s := &RedisStore{}
	if got := s.ttlFor(1e-12, 1); got != redisTTLMax {
		t.Errorf("very slow rate ttl = %v, want %v", got, redisTTLMax)
	}
	if got := s.ttlFor(1e9, 1); got != redisTTLMin {
		t.Errorf("very fast rate ttl = %v, want %v", got, redisTTLMin)
	}
}
