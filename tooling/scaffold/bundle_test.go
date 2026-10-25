package scaffold_test

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shibingli/yd/tooling/scaffold"
	"github.com/shibingli/ydx/afs"
)

// TestGeneratedAppServesScriptBundle proves the production shell makes one request for the shared
// scripts, that the bundle route wins over the static wildcard, and that the response is cacheable. |
// TestGeneratedAppServesScriptBundle 证明生产外壳对共享脚本只发一次请求、bundle 路由优先于静态通配，
// 且响应可长期缓存。
func TestGeneratedAppServesScriptBundle(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实服务验证 | skipped in short mode")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("未找到 go 工具链，跳过端到端验证: %v", err)
	}

	dir := tempProbeDir(t)
	o := scaffold.DefaultOptions("yd-bundle-probe")
	o.DB = scaffold.DBSQLite
	o.LocalReplace = filepath.ToSlash(repoRoot(t))
	if _, err := scaffold.Generate(context.Background(), o, afs.NewOS(dir), nil); err != nil {
		t.Fatalf("真实生成失败: %v", err)
	}
	projDir := filepath.Join(dir, o.Name)
	bin := filepath.Join(projDir, "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	output, err := runGo(goBin, projDir, true, "build", "-mod=mod", "-o", bin, "./cmd/app")
	if err != nil {
		output, err = runGo(goBin, projDir, false, "build", "-mod=mod", "-o", bin, "./cmd/app")
	}
	if err != nil {
		t.Fatalf("生成的骨架无法编译: %v\n%s", err, output)
	}

	base, stop := startGeneratedAppWithStopEnv(t, bin, projDir, map[string]string{
		"YD_APP_ENV":             "production",
		"YD_SEED_ADMIN_PASSWORD": probeAdminPassword,
		"YD_AUTH_MASTER_KEY":     "yd-auth-test-master-key-0123456789",
	})
	t.Cleanup(stop)

	client := newProbeClient(t, httpTimeout)
	resp, body := getText(t, client, base+"/login", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登录页状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
	}
	start := strings.Index(body, "/static/js/yd-bundle.js?v=")
	if start < 0 {
		t.Fatalf("生产页面没有引用共享 bundle:\n%s", body)
	}
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		t.Fatalf("bundle 地址没有结束引号:\n%s", body)
	}
	bundlePath := body[start : start+end]
	if strings.Contains(body, `/static/js/vendor/htmx.min.js`) {
		t.Errorf("生产页面仍逐个引用 vendor 脚本:\n%s", body)
	}

	resp, bundle := getText(t, client, base+bundlePath, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bundle 状态码 = %d, 期望 200\n%s", resp.StatusCode, bundle)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Errorf("bundle Content-Type = %q, 期望 javascript", got)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("bundle Cache-Control = %q, 期望 immutable", got)
	}
	if !strings.Contains(bundle, "htmx") || !strings.Contains(bundle, "yd.ready") {
		t.Errorf("bundle 缺少 htmx 或项目胶水层（长度 %d）", len(bundle))
	}
}
