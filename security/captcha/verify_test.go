package captcha

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	ydcaptcha "github.com/shibingli/ydx/captcha"
	"github.com/shibingli/ydx/router"
)

// customTestType is a deterministic visual family used to mint real tokens in tests. |
// customTestType 是测试里用来生成真实令牌的确定性视觉题型。
const customTestType = ydcaptcha.Type("custom_signal_test")

// customTestProvider generates and checks one deterministic challenge. | customTestProvider 生成并校验一道确定性挑战。
type customTestProvider struct{}

// Type implements ydcaptcha.Provider. | Type 实现 ydcaptcha.Provider。
func (customTestProvider) Type() ydcaptcha.Type { return customTestType }

// Generate implements ydcaptcha.Provider. | Generate 实现 ydcaptcha.Provider。
func (customTestProvider) Generate(_ context.Context, _ ydcaptcha.GenerateRequest) (*ydcaptcha.Generated, error) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.SetRGBA(1, 1, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return nil, err
	}
	return &ydcaptcha.Generated{
		Prompt: ydcaptcha.Prompt{Text: "custom challenge"},
		Images: []ydcaptcha.ImageData{{
			Name: "challenge", MIME: "image/png", Data: encoded.Bytes(), Width: 8, Height: 8,
		}},
		Answer: []byte(`{"text":"custom"}`),
	}, nil
}

// Verify implements ydcaptcha.Provider. | Verify 实现 ydcaptcha.Provider。
func (customTestProvider) Verify(_ context.Context, _ *ydcaptcha.Record, ans ydcaptcha.Answer) (ydcaptcha.CheckResult, error) {
	return ydcaptcha.CheckResult{Passed: ans.Text == "custom"}, nil
}

// newTokenManager builds a manager with the deterministic provider above. | newTokenManager 用上面的确定性生成器构建管理器。
func newTokenManager(t *testing.T) *ydcaptcha.Manager {
	t.Helper()
	manager, err := ydcaptcha.New(ydcaptcha.Options{
		Secret:        []byte("0123456789abcdef0123456789abcdef"),
		KeyPrefix:     "yd:captcha:test:",
		DefaultLocale: "zh-CN",
		Types:         []ydcaptcha.Type{customTestType},
		Providers:     []ydcaptcha.Provider{customTestProvider{}},
		PoW:           ydcaptcha.PoWOptions{Mode: ydcaptcha.PoWOff},
	})
	if err != nil {
		t.Fatalf("ydcaptcha.New() failed: %v", err)
	}
	return manager
}

// mintToken creates a challenge and exchanges the deterministic answer for a one-time token. |
// mintToken 创建挑战并把确定性答案换成一次性令牌。
func mintToken(t *testing.T, manager *ydcaptcha.Manager, action, ip string) string {
	t.Helper()
	meta := ydcaptcha.Meta{IP: ip, UserAgent: "Mozilla/5.0 captcha-test"}
	challenge, err := manager.Create(context.Background(), ydcaptcha.CreateRequest{
		Type:   customTestType,
		Action: action,
		Locale: "zh-CN",
		Meta:   meta,
	})
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	result, err := manager.Verify(context.Background(), ydcaptcha.VerifyRequest{
		ID:     challenge.ID,
		Answer: ydcaptcha.Answer{Text: "custom"},
		Meta:   meta,
	})
	if err != nil {
		t.Fatalf("manager.Verify() failed: %v", err)
	}
	if result.Token == "" {
		t.Fatal("manager.Verify() returned an empty token")
	}
	return result.Token
}

// TestCaptchaVerifySuccessConsumesOnceAndBindsAction proves the wrapper's success path: a real token verifies, a
// replay is refused as used, and a token minted for one action cannot verify another. |
// TestCaptchaVerifySuccessConsumesOnceAndBindsAction 证明包装层的成功路径：真实令牌校验通过、重放被拒为已使用、
// 为一个动作签发的令牌不能校验另一个动作。
func TestCaptchaVerifySuccessConsumesOnceAndBindsAction(t *testing.T) {
	manager := newTokenManager(t)
	svc := &Service{manager: manager, login: true, passwordChange: true, log: testLogger()}
	t.Cleanup(func() { _ = svc.Close() })

	token := mintToken(t, manager, ActionLogin, "203.0.113.8")
	if err := svc.Verify(context.Background(), token, ActionLogin); err != nil {
		t.Fatalf("Verify(valid token) = %v, want nil", err)
	}
	replay := svc.Verify(context.Background(), token, ActionLogin)
	if !errors.Is(replay, ErrInvalid) {
		t.Fatalf("Verify(replay) = %v, want ErrInvalid", replay)
	}
	if !errors.Is(replay, ydcaptcha.ErrTokenUsed) {
		t.Errorf("Verify(replay) lost the ErrTokenUsed cause: %v", replay)
	}

	bound := mintToken(t, manager, ActionLogin, "203.0.113.9")
	if err := svc.Verify(context.Background(), bound, ActionPasswordChange); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Verify(token from another action) = %v, want ErrInvalid", err)
	}
	if err := svc.Verify(context.Background(), bound, ActionLogin); err != nil {
		t.Fatalf("an action-mismatch refusal must not consume the token: %v", err)
	}
}

// TestCaptchaUnknownActionFailsClosed proves a wiring typo cannot silently skip verification while captcha is on. |
// TestCaptchaUnknownActionFailsClosed 证明验证码开启时，接线拼错不会静默跳过校验。
func TestCaptchaUnknownActionFailsClosed(t *testing.T) {
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

	if err := svc.Verify(context.Background(), "token", "signup"); !errors.Is(err, ErrUnknownAction) {
		t.Errorf("Verify(unknown action) = %v, want ErrUnknownAction", err)
	}
	if err := svc.Verify(context.Background(), "", ActionPasswordChange); err != nil {
		t.Errorf("Verify(known disabled action) = %v, want nil", err)
	}

	var disabled *Service
	if err := disabled.Verify(context.Background(), "token", "signup"); err != nil {
		t.Errorf("Verify(unknown action on a nil service) = %v, want nil", err)
	}
}

// TestCaptchaVerifyFailsClosedAfterClose proves a shutdown manager rejects tokens instead of returning nil. |
// TestCaptchaVerifyFailsClosedAfterClose 证明已关闭的管理器拒绝令牌，而不是返回 nil。
func TestCaptchaVerifyFailsClosedAfterClose(t *testing.T) {
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
	if err := svc.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	if err := svc.Verify(context.Background(), "token", ActionLogin); !errors.Is(err, ErrInvalid) {
		t.Errorf("Verify(after Close) = %v, want ErrInvalid", err)
	}
}

// TestCaptchaConcurrentCloseAndVerify exercises the shutdown race: a request already inside Verify must fail closed,
// not panic or return nil, while Close runs. It is meaningful under -race. |
// TestCaptchaConcurrentCloseAndVerify 覆盖关停竞态：已经进入 Verify 的请求在 Close 运行期间必须失败关闭，不得 panic
// 或返回 nil。在 -race 下才有完整意义。
func TestCaptchaConcurrentCloseAndVerify(t *testing.T) {
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

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers + 1)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			if err := svc.Verify(context.Background(), "forged-token", ActionLogin); err == nil {
				t.Error("Verify(forged token) returned nil during shutdown")
			}
		}()
	}
	go func() {
		defer wg.Done()
		if err := svc.Close(); err != nil {
			t.Errorf("Close() failed: %v", err)
		}
	}()
	wg.Wait()
}

// TestCaptchaRegisterMountsOnlyWhenEnabled proves the SDK assets are mounted exactly while the service is on. |
// TestCaptchaRegisterMountsOnlyWhenEnabled 证明 SDK 资产只在服务开启时挂载。
func TestCaptchaRegisterMountsOnlyWhenEnabled(t *testing.T) {
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

	r := router.New()
	svc.Register(r)
	req := httptest.NewRequest(http.MethodGet, Endpoint+"/assets/yd-captcha.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET SDK asset = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "YdCaptcha") {
		t.Error("SDK asset body does not look like the captcha SDK")
	}

	disabledCfg := newTestConfig(t, map[string]any{"app.env": "development"})
	disabled, err := New(disabledCfg, testLogger())
	if err != nil {
		t.Fatalf("New(disabled) failed: %v", err)
	}
	t.Cleanup(func() { _ = disabled.Close() })
	disabledRouter := router.New()
	disabled.Register(disabledRouter)
	rec = httptest.NewRecorder()
	disabledRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET captcha asset while disabled = %d, want 404", rec.Code)
	}
}
