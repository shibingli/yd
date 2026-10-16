//go:build e2e

// Package e2e runs the full yd lifecycle against a real scaffolded project. | Package e2e 在真实脚手架产物上跑通 yd 的完整生命周期。
//
// These tests are behind the "e2e" build tag because they compile, start and
// stop real processes; run them with: | 这些测试位于 "e2e" 构建标签之后，因为它们会编译并启停真实进程；
// 运行方式：
//
//	go test -tags e2e ./e2e/ -v -timeout 30m
package e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// repoRoot returns the repository root directory. | repoRoot 返回仓库根目录。
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(wd)
}

// buildYD compiles the yd CLI into a temporary binary. | buildYD 把 yd CLI 编译到临时二进制。
//
// Running the built binary (instead of `go run`) lets every call choose its own
// working directory: the CLI resolves relative paths against the process cwd, so
// `yd new` must run inside the directory that should receive the project. | 运行编译好的
// 二进制（而不是 `go run`）才能让每次调用自选工作目录：CLI 以进程 cwd 解析相对路径，
// 因此 `yd new` 必须运行在希望接收项目的目录内。
func buildYD(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "yd")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/yd")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译 yd CLI 失败: %v\n%s", err, out)
	}
	return bin
}

// runYD runs a built yd binary in the given working directory. | runYD 在指定工作目录运行已编译的 yd 二进制。
func runYD(t *testing.T, bin, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestScaffoldBuildRun is the end-to-end acceptance walkthrough. | TestScaffoldBuildRun 是端到端验收走查。
func TestScaffoldBuildRun(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	project := filepath.Join(tmp, "demo")

	bin := buildYD(t)
	out, err := runYD(t, bin, tmp, "new", "demo",
		"--module", "example.com/demo",
		"--db", "sqlite",
		"--redis", "--i18n", "--htmx-ext", "sse,ws,preload",
		"--local-replace", root,
		"--yes",
	)
	t.Logf("yd new 输出:\n%s", out)
	if err != nil {
		t.Fatalf("yd new 失败: %v\n%s", err, out)
	}

	t.Run("生成物齐全", func(t *testing.T) {
		required := []string{
			"go.mod", "LICENSE", "README.md", "Dockerfile",
			".gitignore", ".gitattributes", ".env.template", ".yd.json",
			"CLAUDE.md", "AGENTS.md",
			"configs/config.yaml",
			"cmd/app/main.go",
			"docs/developer_guidelines.md",
			"web/assets.lock.json",
		}
		for _, rel := range required {
			if _, err := os.Stat(filepath.Join(project, rel)); err != nil {
				t.Errorf("生成物缺失 %s: %v", rel, err)
			}
		}
		for _, rel := range []string{"internal", "web/templates", "web/static"} {
			if _, err := os.Stat(filepath.Join(project, rel)); err != nil {
				t.Errorf("生成目录缺失 %s: %v", rel, err)
			}
		}
		gi, err := os.ReadFile(filepath.Join(project, ".gitignore"))
		if err != nil {
			t.Fatalf("读取 .gitignore 失败: %v", err)
		}
		for _, want := range []string{"bin/", "dist/", ".env", "node_modules/", ".tailwindcss/"} {
			if !strings.Contains(string(gi), want) {
				t.Errorf(".gitignore 缺少规范行 %q", want)
			}
		}
	})

	t.Run("生成项目可编译", func(t *testing.T) {
		cmd := exec.Command("go", "build", "./...")
		cmd.Dir = filepath.Join(project)
		// 生成的项目是独立模块，go.sum 需在首次构建时补齐；同时禁用工作区，
		// 避免它意外加入本仓库的 go.work。
		// The generated project is its own module: go.sum must be completed on the
		// first build, and the workspace must be disabled so it never joins ours.
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("生成项目编译失败: %v\n%s", err, out)
		}
	})

	// 生成的项目内置多租户鉴权：未登录时不允许看到工作区，
	// 受保护端点的 htmx 请求也不得返回任何数据。
	// The generated project ships multi-tenant auth: without a token the
	// workspace must stay hidden and htmx requests to protected endpoints must
	// not return any data. Authenticated flows (SM2 login, fragment rendering,
	// cross-tenant denial) are asserted in depth by
	// scaffold.TestGeneratedAppSupportsAuth.
	t.Run("未登录被拦截且登录页可达", func(t *testing.T) {
		addr, stop := startApp(t, project)
		defer stop()

		// 不跟随重定向，直接观察服务端的安全决策。
		// Do not follow redirects: assert the server's own security decision.
		full := httpGetRaw(t, "http://"+addr+"/", nil)
		if full.status >= 300 && full.status < 400 {
			if loc := full.header.Get("Location"); !strings.Contains(loc, "/login") {
				t.Errorf("未登录访问 / 应重定向到登录页，实际 Location=%q", loc)
			}
		} else if full.status != http.StatusOK {
			t.Errorf("未登录访问 / 的状态 = %d, 期望 3xx 重定向或 200 登录页", full.status)
		}
		// 无论走哪条分支，响应里都不得出现已登录才有的元素。
		if strings.Contains(full.body, `hx-post="/logout"`) {
			t.Errorf("未登录响应中出现了登出入口: 前 200 字节=%q", head(full.body, 200))
		}

		login := httpGet(t, "http://"+addr+"/login", nil)
		if login.status != http.StatusOK {
			t.Fatalf("登录页状态 = %d", login.status)
		}
		if !strings.Contains(login.body, `type="password"`) {
			t.Errorf("登录页缺少口令输入框: 前 200 字节=%q", head(login.body, 200))
		}
		if strings.Contains(strings.ToLower(login.body), "cdn.") ||
			regexp.MustCompile(`(src|href)="http`).MatchString(login.body) {
			t.Error("登录页引用了外部 CDN（违反零 CDN 约束）")
		}

		// 受保护端点的 htmx 请求在未登录时不得返回 200。
		partial := httpGet(t, "http://"+addr+"/users", map[string]string{"HX-Request": "true"})
		if partial.status == http.StatusOK {
			t.Errorf("未登录的 htmx 请求不应成功: 状态=%d 前 120 字节=%q", partial.status, head(partial.body, 120))
		}
	})

	t.Run("OpenAPI 文档可访问", func(t *testing.T) {
		addr, stop := startApp(t, filepath.Join(project))
		defer stop()

		doc := httpGet(t, "http://"+addr+"/openapi.json", nil)
		if doc.status != http.StatusOK {
			t.Fatalf("openapi.json 状态 = %d", doc.status)
		}
		if !strings.Contains(doc.body, `"3.0`) {
			t.Errorf("OpenAPI 版本字段缺失: %q", head(doc.body, 200))
		}

		viewer := httpGet(t, "http://"+addr+"/docs", nil)
		if viewer.status != http.StatusOK {
			t.Fatalf("/docs 状态 = %d", viewer.status)
		}
		if regexp.MustCompile(`(src|href)="http`).MatchString(viewer.body) {
			t.Error("/docs 查看器引用了外部 CDN（违反零 CDN 约束）")
		}
	})

	t.Run("静态资产本地可用", func(t *testing.T) {
		addr, stop := startApp(t, filepath.Join(project))
		defer stop()

		for _, p := range []string{"/static/js/vendor/htmx.min.js", "/static/css/yd.css"} {
			res := httpGet(t, "http://"+addr+p, nil)
			if res.status != http.StatusOK || len(res.body) == 0 {
				t.Errorf("静态资产 %s 不可用: 状态=%d 长度=%d", p, res.status, len(res.body))
			}
		}
	})
}

// TestCrossCompile verifies cross-compilation for three targets. | TestCrossCompile 验证三个目标平台的交叉编译。
func TestCrossCompile(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()

	targets := []struct{ goos, goarch string }{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"windows", "amd64"},
	}
	for _, tg := range targets {
		t.Run(tg.goos+"/"+tg.goarch, func(t *testing.T) {
			name := "yd-app"
			if tg.goos == "windows" {
				name += ".exe"
			}
			dst := filepath.Join(outDir, name+"_"+tg.goos+"_"+tg.goarch)
			cmd := exec.Command("go", "build", "-trimpath", "-o", dst, "./cmd/yd")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+tg.goos, "GOARCH="+tg.goarch)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("交叉编译 %s/%s 失败: %v\n%s", tg.goos, tg.goarch, err, out)
			}
			fi, err := os.Stat(dst)
			if err != nil || fi.Size() == 0 {
				t.Fatalf("产物缺失或为空: %v", err)
			}
			if tg.goos == "linux" {
				b, _ := os.ReadFile(dst)
				if len(b) < 4 || b[0] != 0x7f || b[1] != 'E' || b[2] != 'L' || b[3] != 'F' {
					t.Error("linux 产物不是 ELF")
				}
			}
			t.Logf("%s/%s → %s (%d 字节)", tg.goos, tg.goarch, filepath.Base(dst), fi.Size())
		})
	}
}

// TestAssetsVerifyOffline verifies offline asset verification. | TestAssetsVerifyOffline 验证离线资产校验。
func TestAssetsVerifyOffline(t *testing.T) {
	root := repoRoot(t)
	out, err := runYD(t, buildYD(t), root, "assets", "verify", "--offline")
	t.Logf("yd assets verify --offline 输出:\n%s", out)
	if err != nil {
		t.Fatalf("离线校验失败: %v", err)
	}
}

// ---------------------------------------------------------------- 测试辅助

type httpResult struct {
	status int
	body   string
	header http.Header
}

// httpGet performs a GET request with optional extra headers. | httpGet 发起带可选附加头的 GET 请求。
func httpGet(t *testing.T, url string, hdr map[string]string) httpResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return httpResult{status: resp.StatusCode, body: string(b), header: resp.Header}
}

// httpGetRaw performs a GET without following redirects, so the caller can
// assert the server's own status code and Location header. | httpGetRaw 发起不跟随
// 重定向的 GET，使调用方能断言服务端自身给出的状态码与 Location 头。
func httpGetRaw(t *testing.T, url string, hdr map[string]string) httpResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return httpResult{status: resp.StatusCode, body: string(b), header: resp.Header}
}

// startApp builds and starts the generated application, returning its address. | startApp 编译并启动生成的应用，返回其地址。
func startApp(t *testing.T, dir string) (string, func()) {
	t.Helper()

	port := freePort(t)
	bin := filepath.Join(t.TempDir(), "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	build := exec.Command("go", "build", "-o", bin, "./cmd/app")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("生成项目编译失败: %v\n%s", err, out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("YD_SERVER_HOST=127.0.0.1"),
		fmt.Sprintf("YD_SERVER_PORT=%d", port),
		"YD_LOG_LEVEL=info",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("获取 stdout 失败: %v", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("启动应用失败: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	stop := func() {
		cancel()
		_ = cmd.Wait()
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return addr, stop
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = stdout
	stop()
	t.Fatalf("应用未在 20 秒内监听 %s", addr)
	return "", func() {}
}

// freePort asks the OS for a free TCP port. | freePort 向操作系统申请一个空闲 TCP 端口。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请端口失败: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// head truncates a string for readable failure messages. | head 截断字符串以便可读的失败信息。
func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
