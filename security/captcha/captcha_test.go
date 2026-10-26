package captcha

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/shibingli/ydx/afs"
	ydcaptcha "github.com/shibingli/ydx/captcha"
	"github.com/shibingli/ydx/config"
	ydlog "github.com/shibingli/ydx/log"
	"github.com/shibingli/ydx/middleware/ratelimit"
	"github.com/shibingli/ydx/router"
)

// newTestConfig builds an in-memory configuration and applies the given overrides. | newTestConfig 构造内存配置并应用给定覆盖值。
func newTestConfig(t *testing.T, values map[string]any) *config.Config {
	t.Helper()

	cfg, err := config.Load(config.Options{FS: afs.NewMem(), ConfigDir: "."})
	if err != nil {
		t.Fatalf("加载测试配置失败: %v", err)
	}
	for key, value := range values {
		cfg.Set(key, value)
	}
	return cfg
}

// testLogger discards everything, so a fallback warning does not pollute the test output. | testLogger 丢弃全部输出，使降级告警不污染测试输出。
func testLogger() *ydlog.Logger {
	return ydlog.New(ydlog.Options{Writer: io.Discard})
}

// TestCaptchaDisabledNeedsNoSecret proves that a project with both switches off neither requires a secret
// nor builds a manager. | TestCaptchaDisabledNeedsNoSecret 证明两个开关都关闭时既不要求密钥也不构建管理器。
func TestCaptchaDisabledNeedsNoSecret(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":                 "production",
		"captcha.login":           false,
		"captcha.password_change": false,
	})

	svc, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("disabled captcha must not require a secret | 关闭的验证码不应要求密钥: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	for _, action := range []string{ActionLogin, ActionPasswordChange} {
		if svc.Enabled(action) {
			t.Errorf("Enabled(%q) = true, want false", action)
		}
		if err := svc.Verify(context.Background(), "", action); err != nil {
			t.Errorf("Verify(%q) on a disabled action = %v, want nil", action, err)
		}
	}
}

// TestCaptchaProductionRequiresSecret proves a production deployment cannot start captcha without a key.
// | TestCaptchaProductionRequiresSecret 证明生产环境启用验证码但没有密钥时拒绝启动。
func TestCaptchaProductionRequiresSecret(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":       "production",
		"captcha.login": true,
		"captcha.store": StoreMemory,
	})

	svc, err := New(cfg, testLogger())
	if svc != nil {
		_ = svc.Close()
	}
	if !errors.Is(err, ErrSecret) {
		t.Fatalf("New() error = %v, want ErrSecret", err)
	}
}

// TestCaptchaDevelopmentFallsBackToDevSecret proves development still starts with the documented key.
// | TestCaptchaDevelopmentFallsBackToDevSecret 证明开发环境仍可用文档化密钥启动。
func TestCaptchaDevelopmentFallsBackToDevSecret(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":       "development",
		"captcha.login": true,
		"captcha.store": StoreMemory,
	})

	svc, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("development fallback failed | 开发环境回退失败: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	if !svc.Enabled(ActionLogin) {
		t.Error("Enabled(login) = false, want true")
	}
	if err := svc.Verify(context.Background(), "", ActionLogin); !errors.Is(err, ErrRequired) {
		t.Errorf("Verify(empty token) = %v, want ErrRequired", err)
	}
}

// TestCaptchaMemoryStoreAndInvalidToken builds a real memory-backed manager and proves a forged token is refused.
// | TestCaptchaMemoryStoreAndInvalidToken 构建真实的内存管理器，并证明伪造令牌会被拒绝。
func TestCaptchaMemoryStoreAndInvalidToken(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":                 "development",
		"captcha.login":           true,
		"captcha.password_change": true,
		"captcha.secret":          "0123456789abcdef0123456789abcdef",
		"captcha.store":           StoreMemory,
		"captcha.types":           []string{"text"},
	})

	svc, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	if !svc.Enabled(ActionLogin) || !svc.Enabled(ActionPasswordChange) {
		t.Fatal("both enabled actions must report true | 两个已启用的动作都应返回 true")
	}
	if svc.Enabled("unknown") {
		t.Error("an unknown action must stay disabled | 未知动作必须保持关闭")
	}
	if err := svc.Verify(context.Background(), "forged-token", ActionLogin); !errors.Is(err, ErrInvalid) {
		t.Errorf("Verify(forged) = %v, want ErrInvalid", err)
	}
	if err := svc.Verify(context.Background(), "", ActionPasswordChange); !errors.Is(err, ErrRequired) {
		t.Errorf("Verify(empty) = %v, want ErrRequired", err)
	}
}

// TestCaptchaRejectsUnknownType proves an unknown challenge type is a startup error rather than a silent fallback.
// | TestCaptchaRejectsUnknownType 证明未知题型属于启动错误，而不是静默回退。
func TestCaptchaRejectsUnknownType(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":        "development",
		"captcha.login":  true,
		"captcha.secret": "0123456789abcdef0123456789abcdef",
		"captcha.store":  StoreMemory,
		"captcha.types":  []string{"no-such-type"},
	})

	svc, err := New(cfg, testLogger())
	if svc != nil {
		_ = svc.Close()
	}
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("New() error = %v, want ErrConfig", err)
	}
}

// TestCaptchaRedisStrictModeFails proves fallback=fail refuses to start when the shared store is unreachable.
// | TestCaptchaRedisStrictModeFails 证明 fallback=fail 在共享存储不可达时拒绝启动。
func TestCaptchaRedisStrictModeFails(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":            "development",
		"captcha.login":      true,
		"captcha.secret":     "0123456789abcdef0123456789abcdef",
		"captcha.store":      StoreRedis,
		"captcha.fallback":   FallbackFail,
		"redis.mode":         "single",
		"redis.addr":         "127.0.0.1:1",
		"redis.dial_timeout": 100 * time.Millisecond,
	})

	svc, err := New(cfg, testLogger())
	if svc != nil {
		_ = svc.Close()
	}
	if !errors.Is(err, ErrStore) {
		t.Fatalf("New() error = %v, want ErrStore", err)
	}
}

// TestCaptchaRedisDefaultFallbackUsesMemory proves the default fallback keeps a single node working.
// | TestCaptchaRedisDefaultFallbackUsesMemory 证明默认降级让单机仍可工作。
func TestCaptchaRedisDefaultFallbackUsesMemory(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":            "development",
		"captcha.login":      true,
		"captcha.secret":     "0123456789abcdef0123456789abcdef",
		"captcha.store":      StoreRedis,
		"captcha.fallback":   FallbackMemory,
		"redis.mode":         "single",
		"redis.addr":         "127.0.0.1:1",
		"redis.dial_timeout": 100 * time.Millisecond,
	})

	svc, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("fallback to memory failed | 回退到内存失败: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	if !svc.Enabled(ActionLogin) {
		t.Error("Enabled(login) = false, want true")
	}
}

// recordingLimiter captures the key the shared limiter would receive. | recordingLimiter 记录共享限流器收到的键。
type recordingLimiter struct {
	key string
}

// Allow implements ratelimit.LimitStore. | Allow 实现 ratelimit.LimitStore。
func (r *recordingLimiter) Allow(_ context.Context, key string, _ float64, _ int) (ratelimit.Decision, error) {
	r.key = key
	return ratelimit.Decision{Allowed: true}, nil
}

// TestPrefixedLimiterStripsManagerPrefix keeps one namespace instead of stacking it twice. | TestPrefixedLimiterStripsManagerPrefix
// 保持单一命名空间，而不是叠两层。
func TestPrefixedLimiterStripsManagerPrefix(t *testing.T) {
	base := &recordingLimiter{}
	limiter := prefixedLimiter{base: base, prefix: "yd:captcha:"}
	if _, err := limiter.Allow(context.Background(), "yd:captcha:create:ip:203.0.113.7", 1, 1); err != nil {
		t.Fatalf("Allow() failed: %v", err)
	}
	if want := "create:ip:203.0.113.7"; base.key != want {
		t.Errorf("forwarded key = %q, want %q", base.key, want)
	}
}

// TestCaptchaSharedClientIsNotClosed proves a caller-owned Redis client survives Service.Close: the generated project
// opens one client for the cache, the message bus and captcha. |
// TestCaptchaSharedClientIsNotClosed 证明调用方持有的 Redis 客户端在 Service.Close 后仍然可用：生成项目让缓存、
// 消息总线与验证码共用同一个客户端。
func TestCaptchaSharedClientIsNotClosed(t *testing.T) {
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	cfg := newTestConfig(t, map[string]any{
		"app.env":          "development",
		"captcha.login":    true,
		"captcha.secret":   "0123456789abcdef0123456789abcdef",
		"captcha.store":    StoreRedis,
		"captcha.fallback": FallbackFail,
	})

	svc, err := NewWithClient(cfg, testLogger(), client)
	if err != nil {
		t.Fatalf("NewWithClient() failed: %v", err)
	}
	if !svc.Enabled(ActionLogin) {
		t.Fatal("Enabled(login) = false, want true")
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("the captcha service closed the shared Redis client: %v", err)
	}
}

// TestCaptchaSharedClientUnavailableDoesNotReopen proves a nil shared client is treated as "Redis is already known to
// be unavailable": the service applies fallback directly instead of opening a second connection. |
// TestCaptchaSharedClientUnavailableDoesNotReopen 证明 nil 共享客户端被视为「已确认 Redis 不可用」：服务直接应用
// 降级策略，而不是再打开第二个连接。
func TestCaptchaSharedClientUnavailableDoesNotReopen(t *testing.T) {
	fallbackCfg := newTestConfig(t, map[string]any{
		"app.env":          "development",
		"captcha.login":    true,
		"captcha.secret":   "0123456789abcdef0123456789abcdef",
		"captcha.store":    StoreRedis,
		"captcha.fallback": FallbackMemory,
	})
	svc, err := NewWithClient(fallbackCfg, testLogger(), nil)
	if err != nil {
		t.Fatalf("NewWithClient(nil, fallback=memory) failed: %v", err)
	}
	if !svc.Enabled(ActionLogin) {
		t.Error("Enabled(login) = false, want true")
	}
	if err := svc.Close(); err != nil {
		t.Errorf("Close() failed: %v", err)
	}

	// A live Redis address proves the nil shared client is not reopened: if the service ignored the caller's
	// "Redis is unavailable" verdict and opened a client anyway, this strict configuration would succeed. |
	// 可连通的 Redis 地址证明 nil 共享客户端不会被重新打开：若服务无视调用方「Redis 不可用」的判定自行建连，
	// 这份严格配置就会成功。
	liveRedis := miniredis.RunT(t)
	strictCfg := newTestConfig(t, map[string]any{
		"app.env":          "development",
		"captcha.login":    true,
		"captcha.secret":   "0123456789abcdef0123456789abcdef",
		"captcha.store":    StoreRedis,
		"captcha.fallback": FallbackFail,
		"redis.mode":       "single",
		"redis.addr":       liveRedis.Addr(),
	})
	strict, err := NewWithClient(strictCfg, testLogger(), nil)
	if strict != nil {
		_ = strict.Close()
	}
	if !errors.Is(err, ErrStore) {
		t.Fatalf("NewWithClient(nil, fallback=fail) error = %v, want ErrStore", err)
	}
}

// TestCaptchaTokenPrefersFormAndIgnoresAuthorization pins the token sources: the hidden form field wins, the explicit
// captcha header is the fallback, and the Authorization header (the session on authenticated endpoints) is ignored.
// | TestCaptchaTokenPrefersFormAndIgnoresAuthorization 固定令牌来源：隐藏表单字段优先，显式验证码头是回退，
// Authorization 头（已认证端点上的会话）被忽略。
func TestCaptchaTokenPrefersFormAndIgnoresAuthorization(t *testing.T) {
	newRouter := func(got *string) *router.Router {
		r := router.New()
		r.POST("/test", func(c *router.Ctx) {
			*got = Token(c)
			_ = c.String(http.StatusOK, "ok")
		})
		return r
	}

	t.Run("form wins", func(t *testing.T) {
		var got string
		req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("captcha_token=form-token"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Captcha-Token", "header-token")
		req.Header.Set("Authorization", "Bearer session-token")
		newRouter(&got).ServeHTTP(httptest.NewRecorder(), req)
		if got != "form-token" {
			t.Errorf("Token() = %q, want form-token", got)
		}
	})

	t.Run("header fallback", func(t *testing.T) {
		var got string
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("X-Captcha-Token", "header-token")
		req.Header.Set("Authorization", "Bearer session-token")
		newRouter(&got).ServeHTTP(httptest.NewRecorder(), req)
		if got != "header-token" {
			t.Errorf("Token() = %q, want header-token", got)
		}
	})

	t.Run("authorization ignored", func(t *testing.T) {
		var got string
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.Header.Set("Authorization", "Bearer session-token")
		newRouter(&got).ServeHTTP(httptest.NewRecorder(), req)
		if got != "" {
			t.Errorf("Token() = %q, want empty", got)
		}
	})

	t.Run("query ignored", func(t *testing.T) {
		var got string
		req := httptest.NewRequest(http.MethodPost, "/test?captcha_token=query-token", nil)
		newRouter(&got).ServeHTTP(httptest.NewRecorder(), req)
		if got != "" {
			t.Errorf("Token() = %q, want empty: the URL query is not a token source", got)
		}
	})
}

// TestCaptchaIndependentActionSwitches proves the two switches are independent: enabling sign-in only must leave the
// password-change path untouched. | TestCaptchaIndependentActionSwitches 证明两个开关彼此独立：只开启登录时，
// 改密路径保持不变。
func TestCaptchaIndependentActionSwitches(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":                 "development",
		"captcha.login":           true,
		"captcha.password_change": false,
		"captcha.secret":          "0123456789abcdef0123456789abcdef",
		"captcha.store":           StoreMemory,
	})
	svc, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	if !svc.Enabled(ActionLogin) {
		t.Error("Enabled(login) = false, want true")
	}
	if svc.Enabled(ActionPasswordChange) {
		t.Error("Enabled(password_change) = true, want false")
	}
	if err := svc.Verify(context.Background(), "", ActionPasswordChange); err != nil {
		t.Errorf("Verify(password_change) on a disabled action = %v, want nil", err)
	}
	if err := svc.Verify(context.Background(), "", ActionLogin); !errors.Is(err, ErrRequired) {
		t.Errorf("Verify(login, empty token) = %v, want ErrRequired", err)
	}
}

// TestCaptchaTrustedProxies proves CIDR and bare-IP entries are accepted and a malformed entry stops startup. |
// TestCaptchaTrustedProxies 证明 CIDR 与裸 IP 条目被接受，非法条目阻断启动。
func TestCaptchaTrustedProxies(t *testing.T) {
	proxies, err := parseTrustedProxies([]string{"10.0.0.0/8", "192.0.2.7"})
	if err != nil {
		t.Fatalf("parseTrustedProxies() failed: %v", err)
	}
	if len(proxies) != 2 {
		t.Fatalf("len(proxies) = %d, want 2", len(proxies))
	}
	if proxies[0].String() != "10.0.0.0/8" || proxies[1].String() != "192.0.2.7/32" {
		t.Errorf("proxies = %v, want [10.0.0.0/8 192.0.2.7/32]", proxies)
	}
	if _, err := parseTrustedProxies([]string{"not-a-cidr"}); !errors.Is(err, ErrConfig) {
		t.Errorf("parseTrustedProxies(invalid) = %v, want ErrConfig", err)
	}

	cfg := newTestConfig(t, map[string]any{
		"app.env":                 "development",
		"captcha.login":           true,
		"captcha.secret":          "0123456789abcdef0123456789abcdef",
		"captcha.store":           StoreMemory,
		"captcha.trusted_proxies": []string{"not-a-cidr"},
	})
	svc, err := New(cfg, testLogger())
	if svc != nil {
		_ = svc.Close()
	}
	if !errors.Is(err, ErrConfig) {
		t.Errorf("New(invalid trusted proxy) = %v, want ErrConfig", err)
	}
}

// TestCaptchaCloseIsIdempotent proves the shutdown hook can run twice without closing the owned Redis client twice.
// | TestCaptchaCloseIsIdempotent 证明关停钩子可以运行两次，而不会重复关闭自有的 Redis 客户端。
func TestCaptchaCloseIsIdempotent(t *testing.T) {
	server := miniredis.RunT(t)
	cfg := newTestConfig(t, map[string]any{
		"app.env":          "development",
		"captcha.login":    true,
		"captcha.secret":   "0123456789abcdef0123456789abcdef",
		"captcha.store":    StoreRedis,
		"captcha.fallback": FallbackFail,
		"redis.mode":       "single",
		"redis.addr":       server.Addr(),
	})

	svc, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("first Close() failed: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("second Close() failed: %v", err)
	}
}

// TestCaptchaDisabledIgnoresInvalidConfig proves a disabled feature does not reject stale or invalid captcha keys.
// | TestCaptchaDisabledIgnoresInvalidConfig 证明关闭的功能不会因陈旧或非法的验证码配置而拒绝启动。
func TestCaptchaDisabledIgnoresInvalidConfig(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":                 "production",
		"captcha.login":           false,
		"captcha.password_change": false,
		"captcha.store":           "no-such-store",
		"captcha.fallback":        "no-such-fallback",
		"captcha.secret":          "short",
		"captcha.trusted_proxies": []string{"not-a-cidr"},
	})

	svc, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("disabled captcha must ignore invalid keys: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	if svc.Enabled(ActionLogin) || svc.Enabled(ActionPasswordChange) {
		t.Error("disabled captcha must report both actions off")
	}
}

// TestCaptchaRejectsOversizedToken proves an absurd token is refused before the manager parses it. |
// TestCaptchaRejectsOversizedToken 证明超长令牌在管理器解析之前就被拒绝。
func TestCaptchaRejectsOversizedToken(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":        "development",
		"captcha.login":  true,
		"captcha.secret": "0123456789abcdef0123456789abcdef",
		"captcha.store":  StoreMemory,
	})
	svc, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	huge := strings.Repeat("a", maxTokenBytes+1)
	if err := svc.Verify(context.Background(), huge, ActionLogin); !errors.Is(err, ErrInvalid) {
		t.Errorf("Verify(oversized) = %v, want ErrInvalid", err)
	}
}

// TestCaptchaRejectsDevSecretOutsideDevelopment proves the documented development secret cannot be copied into a
// production-like environment. | TestCaptchaRejectsDevSecretOutsideDevelopment 证明文档化开发密钥不能被复制到类生产环境。
func TestCaptchaRejectsDevSecretOutsideDevelopment(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":        "production",
		"captcha.login":  true,
		"captcha.secret": DevSecret,
		"captcha.store":  StoreMemory,
	})
	svc, err := New(cfg, testLogger())
	if svc != nil {
		_ = svc.Close()
	}
	if !errors.Is(err, ErrSecret) {
		t.Fatalf("New() error = %v, want ErrSecret", err)
	}
}

// TestCaptchaParsesCommaSeparatedTypes covers environment-variable style lists and de-duplication. |
// TestCaptchaParsesCommaSeparatedTypes 覆盖环境变量风格的列表与去重。
func TestCaptchaParsesCommaSeparatedTypes(t *testing.T) {
	types, err := parseTypes([]string{"text, slide", "TEXT", "rotate"})
	if err != nil {
		t.Fatalf("parseTypes() failed: %v", err)
	}
	if len(types) != 3 {
		t.Fatalf("len(types) = %d, want 3 (%v)", len(types), types)
	}
	if types[0] != ydcaptcha.TypeText || types[1] != ydcaptcha.TypeSlide || types[2] != ydcaptcha.TypeRotate {
		t.Errorf("types = %v, want [text slide rotate]", types)
	}
	if types, err := parseTypes(nil); err != nil || types != nil {
		t.Errorf("parseTypes(nil) = (%v, %v), want (nil, nil)", types, err)
	}
	if types, err := parseTypes([]string{"", "  "}); err != nil || types != nil {
		t.Errorf("parseTypes(blank) = (%v, %v), want (nil, nil)", types, err)
	}
}
