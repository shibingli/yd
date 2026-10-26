package captcha

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	ydcaptcha "github.com/shibingli/ydx/captcha"
	ydlog "github.com/shibingli/ydx/log"
)

// pingFailClient embeds a UniversalClient so only Ping and Close are exercised by the fallback path. |
// pingFailClient 内嵌 UniversalClient，使降级路径只会调用 Ping 与 Close。
type pingFailClient struct {
	goredis.UniversalClient
	closes atomic.Int32
}

// Ping implements goredis.UniversalClient. | Ping 实现 goredis.UniversalClient。
func (c *pingFailClient) Ping(context.Context) *goredis.StatusCmd {
	cmd := goredis.NewStatusCmd(context.Background())
	cmd.SetErr(errors.New("ping failed"))
	return cmd
}

// Close implements goredis.UniversalClient. | Close 实现 goredis.UniversalClient。
func (c *pingFailClient) Close() error {
	c.closes.Add(1)
	return nil
}

// TestCaptchaLocaleFallsBackWhenUnsupported proves an application language outside the captcha catalog does not stop
// startup. | TestCaptchaLocaleFallsBackWhenUnsupported 证明应用语言超出验证码目录时不会阻断启动。
func TestCaptchaLocaleFallsBackWhenUnsupported(t *testing.T) {
	cfg := newTestConfig(t, map[string]any{
		"app.env":           "development",
		"captcha.login":     true,
		"captcha.secret":    "0123456789abcdef0123456789abcdef",
		"captcha.store":     StoreMemory,
		"i18n.default_lang": "ru",
	})
	svc, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New() with an unsupported i18n.default_lang failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	if got := svc.manager.DefaultLocale(); got != "zh-CN" {
		t.Errorf("manager default locale = %q, want zh-CN", got)
	}
}

// TestCaptchaRejectsInvalidModesAndShortSecret covers the configuration sentinels around mode names and the secret. |
// TestCaptchaRejectsInvalidModesAndShortSecret 覆盖模式名与密钥相关的配置哨兵。
func TestCaptchaRejectsInvalidModesAndShortSecret(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value any
		want  error
	}{
		{name: "unknown store", key: "captcha.store", value: "postgres", want: ErrConfig},
		{name: "unknown fallback", key: "captcha.fallback", value: "abort", want: ErrConfig},
		{name: "short secret", key: "captcha.secret", value: "short", want: ErrSecret},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newTestConfig(t, map[string]any{
				"app.env":        "development",
				"captcha.login":  true,
				"captcha.secret": "0123456789abcdef0123456789abcdef",
				"captcha.store":  StoreMemory,
			})
			cfg.Set(tc.key, tc.value)
			svc, err := New(cfg, testLogger())
			if svc != nil {
				_ = svc.Close()
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("New() error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestCaptchaNormalizeMode pins the case and whitespace normalisation of both mode settings. |
// TestCaptchaNormalizeMode 固定两个模式取值的去空白与小写化。
func TestCaptchaNormalizeMode(t *testing.T) {
	cases := []struct {
		value string
		def   string
		want  string
	}{
		{value: " Redis ", def: "auto", want: StoreRedis},
		{value: " FAIL ", def: "memory", want: FallbackFail},
		{value: "", def: "memory", want: FallbackMemory},
		{value: "  ", def: "auto", want: StoreAuto},
	}
	for _, tc := range cases {
		if got := normalizeMode(tc.value, tc.def); got != tc.want {
			t.Errorf("normalizeMode(%q, %q) = %q, want %q", tc.value, tc.def, got, tc.want)
		}
	}
}

// TestCaptchaNilInputs covers the nil service and nil configuration edges without panicking. |
// TestCaptchaNilInputs 覆盖空服务与空配置边界，且不得 panic。
func TestCaptchaNilInputs(t *testing.T) {
	if svc, err := New(nil, nil); svc != nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("New(nil, nil) = (%v, %v), want (nil, ErrConfig)", svc, err)
	}
	var svc *Service
	if svc.Enabled(ActionLogin) || svc.Enabled(ActionPasswordChange) {
		t.Error("a nil service must report every action disabled")
	}
	if err := svc.Verify(context.Background(), "token", ActionLogin); err != nil {
		t.Errorf("Verify on a nil service = %v, want nil", err)
	}
	if err := svc.Close(); err != nil {
		t.Errorf("Close on a nil service = %v, want nil", err)
	}
	svc.Register(nil)
	if got := Token(nil); got != "" {
		t.Errorf("Token(nil) = %q, want empty", got)
	}
}

// TestCaptchaTrustedProxyForms covers IPv6, masked prefixes, duplicates and comma-separated entries. |
// TestCaptchaTrustedProxyForms 覆盖 IPv6、掩码、去重与逗号分隔条目。
func TestCaptchaTrustedProxyForms(t *testing.T) {
	proxies, err := parseTrustedProxies([]string{
		"2001:db8::/32",
		"2001:db8::/32",
		"10.1.2.3/8",
		"192.0.2.7",
		"10.0.0.0/8,192.0.2.7",
	})
	if err != nil {
		t.Fatalf("parseTrustedProxies() failed: %v", err)
	}
	want := []string{"2001:db8::/32", "10.0.0.0/8", "192.0.2.7/32"}
	if len(proxies) != len(want) {
		t.Fatalf("proxies = %v, want %v", proxies, want)
	}
	for i, prefix := range proxies {
		if prefix.String() != want[i] {
			t.Errorf("proxies[%d] = %s, want %s", i, prefix, want[i])
		}
	}
	single, err := parseTrustedProxies([]string{"2001:db8::1"})
	if err != nil || len(single) != 1 || single[0].String() != "2001:db8::1/128" {
		t.Errorf("parseTrustedProxies(IPv6 host) = (%v, %v), want [2001:db8::1/128]", single, err)
	}
}

// TestCaptchaCloseClosesOwnedClient proves New's own client is actually released, not merely that Close returns nil. |
// TestCaptchaCloseClosesOwnedClient 证明 New 自有的客户端确实被释放，而不只是 Close 返回 nil。
func TestCaptchaCloseClosesOwnedClient(t *testing.T) {
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
		t.Fatalf("Close() failed: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for server.CurrentConnectionCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if count := server.CurrentConnectionCount(); count != 0 {
		t.Errorf("owned Redis client still holds %d connection(s) after Close", count)
	}
}

// TestCaptchaCallerClientSurvivesProbeFailure proves a caller-owned client is never closed on the fallback path. |
// TestCaptchaCallerClientSurvivesProbeFailure 证明调用方持有的客户端在降级路径上绝不会被关闭。
func TestCaptchaCallerClientSurvivesProbeFailure(t *testing.T) {
	client := &pingFailClient{}
	cfg := newTestConfig(t, map[string]any{
		"app.env":          "development",
		"captcha.login":    true,
		"captcha.secret":   "0123456789abcdef0123456789abcdef",
		"captcha.store":    StoreRedis,
		"captcha.fallback": FallbackMemory,
	})
	svc, err := NewWithClient(cfg, testLogger(), client)
	if err != nil {
		t.Fatalf("NewWithClient(ping failure, fallback=memory) failed: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Errorf("Close() failed: %v", err)
	}
	if got := client.closes.Load(); got != 0 {
		t.Errorf("caller-owned client Close calls = %d, want 0", got)
	}
}

// TestCaptchaDevelopmentSecretWarningDoesNotLeakSecret proves the warning names the key without printing the value. |
// TestCaptchaDevelopmentSecretWarningDoesNotLeakSecret 证明告警只点名键而不打印密钥取值。
func TestCaptchaDevelopmentSecretWarningDoesNotLeakSecret(t *testing.T) {
	var buf bytes.Buffer
	logger := ydlog.New(ydlog.Options{Writer: &buf})
	cfg := newTestConfig(t, map[string]any{
		"app.env":       "development",
		"captcha.login": true,
		"captcha.store": StoreMemory,
	})
	svc, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	out := buf.String()
	if !bytes.Contains([]byte(out), []byte("captcha.secret")) {
		t.Errorf("development secret warning does not name the key:\n%s", out)
	}
	if bytes.Contains([]byte(out), []byte(DevSecret)) {
		t.Errorf("the development secret leaked into the log:\n%s", out)
	}
}

var _ ydcaptcha.Provider = customTestProvider{}
