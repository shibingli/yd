package web_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/web"
)

// captchaJSAssetName is the shared glue script served under /static/js. | captchaJSAssetName 是 /static/js 下提供的共享胶水脚本。
const captchaJSAssetName = "static/js/yd-captcha.js"

// captchaJSTestScript is the node:test suite for the glue. | captchaJSTestScript 是胶水层的 node:test 套件。
const captchaJSTestScript = "web/static/js/yd-captcha.test.mjs"

// TestCaptchaJSAssetMountsSameOriginSDK pins the integration points the templates rely on. |
// TestCaptchaJSAssetMountsSameOriginSDK 钉住模板依赖的集成点。
func TestCaptchaJSAssetMountsSameOriginSDK(t *testing.T) {
	raw, err := afero.ReadFile(web.FS(), captchaJSAssetName)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", captchaJSAssetName, err)
	}
	source := string(raw)

	for _, want := range []string{
		"data-yd-captcha", "data-yd-captcha-dialog", "data-yd-captcha-host", "captcha_token",
		"/captcha/assets/yd-captcha.js", "/captcha/assets/yd-captcha.css",
		"YdCaptcha.mount", ".setTheme(", ".setLocale(",
		"data-yd-captcha-required", "data-yd-captcha-unavailable",
		"addEventListener('submit'", ", true)",
		"showModal", "requestSubmit", "data-yd-captcha-cancel",
		"htmx:after:request", "htmx:after:swap",
		"htmx:history:cache:after:restore", "aria-invalid", "checkValidity",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("%s 缺少 %q", captchaJSAssetName, want)
		}
	}
}

// TestCaptchaJSAssetStaysCSPCompatible proves the glue runs under script-src 'self'. |
// TestCaptchaJSAssetStaysCSPCompatible 证明胶水脚本能在 script-src 'self' 下运行。
func TestCaptchaJSAssetStaysCSPCompatible(t *testing.T) {
	raw, err := afero.ReadFile(web.FS(), captchaJSAssetName)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", captchaJSAssetName, err)
	}
	source := string(raw)

	for _, forbidden := range []string{"new Function", "eval(", "setTimeout('", "setInterval('", "innerHTML"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s 含有 %q，被 CSP 或项目约定禁止", captchaJSAssetName, forbidden)
		}
	}
	for _, ref := range []string{"http://", "https://", "//cdn.", "unpkg.com", "cdnjs.cloudflare.com"} {
		if strings.Contains(source, ref) {
			t.Errorf("%s 引用了外部地址 %q", captchaJSAssetName, ref)
		}
	}
}

// TestCaptchaJSSuite runs the behaviour suite against the shipped glue. Node is optional, exactly as it is for the
// other JavaScript suites. | TestCaptchaJSSuite 对随包发布的胶水层运行行为套件；Node 与其它 JS 套件一样是可选的。
func TestCaptchaJSSuite(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("skipping: node is not available on PATH (%v); install Node.js to run the captcha glue suite | 跳过：PATH 中没有 node（%v），请安装 Node.js 以运行验证码胶水套件", err, err)
	}
	script := ""
	for _, candidate := range []string{captchaJSTestScript, "static/js/yd-captcha.test.mjs"} {
		if _, err := os.Stat(candidate); err == nil {
			script = candidate
			break
		}
	}
	if script == "" {
		t.Fatalf("missing %s | 缺少 %s", captchaJSTestScript, captchaJSTestScript)
	}
	out, err := exec.Command("node", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", script, err, out)
	}
}
