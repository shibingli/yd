package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/tooling/versions"
)

// versionsGoMod is the go.mod fixture of the versions command tests. | versionsGoMod 是 versions 命令测试的 go.mod 夹具。
const versionsGoMod = `module github.com/yun-dev/demo

go 1.27.3

require github.com/shibingli/yd v1.2.3
`

// versionsManifest is the asset lock fixture with one upgradable and one current asset. | versionsManifest 是含一个可升级与一个已最新资产的资产锁夹具。
const versionsManifest = `{
  "version": 1,
  "assets": [
    {"name":"htmx","version":"4.0.0","url":"https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js","path":"static/js/vendored/htmx.min.js","sha256":"97702a55839da4ec5879c4eec4030c905e074b71d398cf763f40321de1e04c13","size":1},
    {"name":"alpinejs","version":"3.17.4","url":"https://cdn.jsdelivr.net/npm/alpinejs@3.17.4/dist/cdn.min.js","path":"static/js/vendored/alpine.min.js","sha256":"97702a55839da4ec5879c4eec4030c905e074b71d398cf763f40321de1e04c13","size":1}
  ]
}`

// versionsFS builds the memory file system the command operates on. | versionsFS 构建命令操作的内存文件系统。
func versionsFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeVersionsFile(t, fsys, "go.mod", versionsGoMod)
	writeVersionsFile(t, fsys, "web/assets.lock.json", versionsManifest)
	if err := fsys.MkdirAll("tooling/scaffold", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return fsys
}

// writeVersionsFile creates one fixture file and its parents. | writeVersionsFile 创建一个夹具文件及其父目录。
func writeVersionsFile(t *testing.T, fsys afero.Fs, path, content string) {
	t.Helper()
	if i := strings.LastIndex(path, "/"); i >= 0 {
		if err := fsys.MkdirAll(path[:i], 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	if err := afero.WriteFile(fsys, path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// versionsTransport answers registry and asset requests without leaving the process. | versionsTransport 在进程内应答仓库与资产请求。
type versionsTransport struct {
	versions map[string][]string
	calls    *[]string
}

// RoundTrip implements http.RoundTripper. | RoundTrip 实现 http.RoundTripper。
func (v versionsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if v.calls != nil {
		*v.calls = append(*v.calls, r.URL.String())
	}
	if r.URL.Host == "registry.npmjs.org" {
		pkg := strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/"), "%2F", "/")
		vs, ok := v.versions[pkg]
		if !ok || len(vs) == 0 {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}
		var b strings.Builder
		b.WriteString(`{"dist-tags":{"latest":"` + vs[len(vs)-1] + `"},"versions":{`)
		for i, ver := range vs {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`"` + ver + `":{}`)
		}
		b.WriteString("}}")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(b.String()))}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("asset body")),
	}, nil
}

// useVersionsHTTP swaps the command's HTTP client for the duration of one test. | useVersionsHTTP 在测试期间替换命令的 HTTP 客户端。
func useVersionsHTTP(t *testing.T, tr http.RoundTripper) {
	t.Helper()
	old := versionsHTTPClient
	versionsHTTPClient = &http.Client{Transport: tr}
	t.Cleanup(func() { versionsHTTPClient = old })
}

// useVersionsGo swaps the external go command for the duration of one test. | useVersionsGo 在测试期间替换外部 go 命令。
func useVersionsGo(t *testing.T, fn func(ctx context.Context, dir string, out io.Writer, args ...string) error) {
	t.Helper()
	old := versionsRunGo
	versionsRunGo = fn
	t.Cleanup(func() { versionsRunGo = old })
}

// TestVersionsSyncThenCheck writes the generated file and verifies it. | TestVersionsSyncThenCheck 写入生成文件并校验它。
func TestVersionsSyncThenCheck(t *testing.T) {
	te := newTestEnv(t, versionsFS(t), "", false)
	if err := Execute(context.Background(), []string{"versions", "sync"}, te.env); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := te.env.FS.Stat(versions.GeneratedPath); err != nil {
		t.Fatalf("generated file missing: %v", err)
	}
	te.out.Reset()
	if err := Execute(context.Background(), []string{"versions", "check"}, te.env); err != nil {
		t.Fatalf("check: %v", err)
	}
	out := te.out.String()
	for _, want := range []string{"1.27.3", "4.0.0", "up to date | 已同步"} {
		if !strings.Contains(out, want) {
			t.Errorf("check output missing %q:\n%s", want, out)
		}
	}
}

// TestVersionsCheckReportsDrift proves that check fails and points at sync. | TestVersionsCheckReportsDrift 证明 check 会失败并指向 sync。
func TestVersionsCheckReportsDrift(t *testing.T) {
	te := newTestEnv(t, versionsFS(t), "", false)
	err := Execute(context.Background(), []string{"versions", "check"}, te.env)
	if !errors.Is(err, versions.ErrGeneratedDrift) {
		t.Fatalf("err = %v, want ErrGeneratedDrift", err)
	}
	if !strings.Contains(te.errs.String(), "yd versions sync") {
		t.Errorf("stderr does not point at sync:\n%s", te.errs.String())
	}
}

// TestVersionsUpgradeDryRunDoesNothing verifies that neither go nor the registry is touched for writes. | TestVersionsUpgradeDryRunDoesNothing 验证干跑不写文件，也不执行 go。
func TestVersionsUpgradeDryRunDoesNothing(t *testing.T) {
	fsys := versionsFS(t)
	te := newTestEnv(t, fsys, "", false)
	useVersionsGo(t, func(context.Context, string, io.Writer, ...string) error {
		t.Fatal("dry run must not execute go")
		return nil
	})
	useVersionsHTTP(t, versionsTransport{versions: map[string][]string{"htmx.org": {"4.0.0", "4.1.0"}, "alpinejs": {"3.17.4"}}})
	if err := Execute(context.Background(), []string{"versions", "upgrade", "--dry-run"}, te.env); err != nil {
		t.Fatalf("upgrade --dry-run: %v", err)
	}
	out := te.out.String()
	for _, want := range []string{"would run | 将执行", "4.0.0 -> 4.1.0", "would regenerate | 将重新生成"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out)
		}
	}
	if _, err := fsys.Stat(versions.GeneratedPath); err == nil {
		t.Error("dry run wrote the generated file")
	}
	data, err := afero.ReadFile(fsys, "web/assets.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version":"4.0.0"`) && !strings.Contains(string(data), `"version": "4.0.0"`) {
		t.Errorf("dry run changed the manifest:\n%s", data)
	}
}

// TestVersionsUpgradeAppliesBothHalves runs the real orchestration against stubs. | TestVersionsUpgradeAppliesBothHalves 用桩运行真实编排。
func TestVersionsUpgradeAppliesBothHalves(t *testing.T) {
	fsys := versionsFS(t)
	te := newTestEnv(t, fsys, "", false)
	var calls [][]string
	useVersionsGo(t, func(_ context.Context, _ string, _ io.Writer, args ...string) error {
		calls = append(calls, args)
		return nil
	})
	useVersionsHTTP(t, versionsTransport{versions: map[string][]string{"htmx.org": {"4.0.0", "4.1.0"}, "alpinejs": {"3.17.4"}}})
	if err := Execute(context.Background(), []string{"versions", "upgrade"}, te.env); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	wantCalls := []string{"mod tidy", "get -u ./...", "mod tidy"}
	if len(calls) != len(wantCalls) {
		t.Fatalf("go calls = %v, want %v", calls, wantCalls)
	}
	for i := range wantCalls {
		if strings.Join(calls[i], " ") != wantCalls[i] {
			t.Fatalf("go call %d = %v, want %q", i, calls[i], wantCalls[i])
		}
	}
	data, err := afero.ReadFile(fsys, "web/assets.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "4.1.0") || !strings.Contains(string(data), "htmx.org@4.1.0") {
		t.Errorf("manifest not upgraded:\n%s", data)
	}
	if _, err := fsys.Stat(versions.GeneratedPath); err != nil {
		t.Errorf("generated file missing after upgrade: %v", err)
	}
	if !strings.Contains(te.out.String(), "updated | 已更新") {
		t.Errorf("upgrade output missing asset summary:\n%s", te.out.String())
	}
}

// TestVersionsUpgradeTidiesBeforeGet proves a newly imported module missing from go.mod is added
// by the initial tidy before `go get -u ./...` runs. | TestVersionsUpgradeTidiesBeforeGet 验证
// 新导入但尚未写入 go.mod 的模块会先由 tidy 补入，再执行 `go get -u ./...`。
func TestVersionsUpgradeTidiesBeforeGet(t *testing.T) {
	fsys := versionsFS(t)
	te := newTestEnv(t, fsys, "", false)
	tidied := false
	var calls []string
	useVersionsGo(t, func(_ context.Context, _ string, _ io.Writer, args ...string) error {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		switch call {
		case "mod tidy":
			tidied = true
		case "get -u ./...":
			if !tidied {
				return errors.New("cannot find module providing package")
			}
		}
		return nil
	})
	if err := Execute(context.Background(), []string{"versions", "upgrade", "--assets=false"}, te.env); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, te.errs.String())
	}
	want := []string{"mod tidy", "get -u ./...", "mod tidy"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("go calls = %v, want %v", calls, want)
	}
}

// TestVersionsPlainProjectSkipsScaffold proves that a generated project never gets a stray
// tooling/scaffold tree. | TestVersionsPlainProjectSkipsScaffold 证明生成项目不会多出 tooling/scaffold。
func TestVersionsPlainProjectSkipsScaffold(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeVersionsFile(t, fsys, "go.mod", versionsGoMod)
	writeVersionsFile(t, fsys, "web/assets.lock.json", versionsManifest)
	te := newTestEnv(t, fsys, "", false)
	if err := Execute(context.Background(), []string{"versions", "check"}, te.env); err != nil {
		t.Fatalf("check in plain project: %v", err)
	}
	for _, want := range []string{"no scaffold sources", "生成文件校验"} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("check output does not announce the friendly skip (%q): %s", want, te.out.String())
		}
	}
	err := Execute(context.Background(), []string{"versions", "sync"}, te.env)
	if !errors.Is(err, ErrNoScaffold) {
		t.Fatalf("sync err = %v, want ErrNoScaffold", err)
	}
	if _, statErr := fsys.Stat("tooling/scaffold"); statErr == nil {
		t.Error("sync created tooling/scaffold in a plain project")
	}
}

// TestVersionsPlainProjectUpgradeAnnouncesScaffoldSkip pins the friendly upgrade notice: a project made
// by yd new has no scaffold sources, and the run must say so instead of looking broken. |
// TestVersionsPlainProjectUpgradeAnnouncesScaffoldSkip 固定 upgrade 的友好提示：yd new 生成的项目没有
// 脚手架源码，运行必须说清楚，而不是看起来像失败。
func TestVersionsPlainProjectUpgradeAnnouncesScaffoldSkip(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeVersionsFile(t, fsys, "go.mod", versionsGoMod)
	writeVersionsFile(t, fsys, "web/assets.lock.json", versionsManifest)
	te := newTestEnv(t, fsys, "", false)
	useVersionsGo(t, func(context.Context, string, io.Writer, ...string) error {
		t.Fatal("dry run must not execute go")
		return nil
	})
	useVersionsHTTP(t, versionsTransport{versions: map[string][]string{"htmx.org": {"4.0.0", "4.1.0"}, "alpinejs": {"3.17.4"}}})
	if err := Execute(context.Background(), []string{"versions", "upgrade", "--dry-run"}, te.env); err != nil {
		t.Fatalf("upgrade --dry-run: %v", err)
	}
	for _, want := range []string{"no scaffold sources", "版本文件同步"} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("upgrade output does not announce the friendly skip (%q): %s", want, te.out.String())
		}
	}
}

// TestVersionsClientUsesConfiguredProxy proves the upgrade run honours http.proxy: the target host
// does not resolve, so only the proxy can answer. | TestVersionsClientUsesConfiguredProxy 证明升级运行
// 遵循 http.proxy：目标主机无法解析，只有代理能应答。
func TestVersionsClientUsesConfiguredProxy(t *testing.T) {
	old := versionsHTTPClient
	versionsHTTPClient = defaultVersionsHTTPClient
	t.Cleanup(func() { versionsHTTPClient = old })

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxy.Close()
	te := newTestEnv(t, nil, "", false)
	te.env.Cfg = loadKoCLIConfig(t, "http:\n  proxy: "+proxy.URL+"\n")

	client, err := versionsClient(te.env)
	if err != nil {
		t.Fatalf("versionsClient 失败 | failed: %v", err)
	}
	resp, err := client.Get("http://example.invalid/registry")
	if err != nil {
		t.Fatalf("经代理请求失败 | request through proxy failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil || string(body) != "proxied" {
		t.Fatalf("响应 = %q, err = %v | response", body, readErr)
	}
}

// TestVersionsUpgradeSelfWiresFlags proves --self-binary/--self-version/--self-force reach the
// updater even without the explicit --self switch, and that the CLI replacement runs after project
// work. | TestVersionsUpgradeSelfWiresFlags 证明即使没有显式 --self 开关，
// --self-binary/--self-version/--self-force 也会传到更新器，并且 CLI 替换发生在项目工作之后。
func TestVersionsUpgradeSelfWiresFlags(t *testing.T) {
	te := newTestEnv(t, versionsFS(t), "", false)
	old := versionsSelfUpdate
	var got versions.SelfUpdateOptions
	versionsSelfUpdate = func(_ context.Context, o versions.SelfUpdateOptions) (*versions.SelfUpdateResult, error) {
		got = o
		return &versions.SelfUpdateResult{
			FromVersion: "v1.0.0",
			ToVersion:   "v9.9.9",
			Binary:      o.BinaryPath,
			Updated:     true,
		}, nil
	}
	t.Cleanup(func() { versionsSelfUpdate = old })

	err := Execute(context.Background(), []string{
		"versions", "upgrade", "--go=false", "--assets=false",
		"--self-binary", "custom/yd", "--self-version", "v9.9.9", "--self-force",
	}, te.env)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if got.BinaryPath != "custom/yd" || got.TargetVersion != "v9.9.9" || !got.Force {
		t.Fatalf("自更新选项 = %+v | self options = %+v", got, got)
	}
	if got.HTTPClient == nil {
		t.Error("自更新缺少 HTTP 客户端 | self update has no HTTP client")
	}
	out := te.out.String()
	for _, want := range []string{"cli | 命令行:", "v1.0.0 -> v9.9.9", "next | 下一步"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %q:\n%s", want, out)
		}
	}
}

// TestVersionsUpgradeSelfDryRunDoesNotDownload keeps --dry-run network-free for the CLI half. |
// TestVersionsUpgradeSelfDryRunDoesNotDownload 保证 CLI 那一半在 --dry-run 下不触网。
func TestVersionsUpgradeSelfDryRunDoesNotDownload(t *testing.T) {
	te := newTestEnv(t, versionsFS(t), "", false)
	old := versionsSelfUpdate
	called := false
	versionsSelfUpdate = func(context.Context, versions.SelfUpdateOptions) (*versions.SelfUpdateResult, error) {
		called = true
		return &versions.SelfUpdateResult{}, nil
	}
	t.Cleanup(func() { versionsSelfUpdate = old })

	err := Execute(context.Background(), []string{
		"versions", "upgrade", "--go=false", "--assets=false", "--self", "--dry-run",
	}, te.env)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if called {
		t.Error("dry-run 不得调用自更新器 | dry-run must not call the updater")
	}
	if !strings.Contains(te.out.String(), "would update | 将更新") {
		t.Errorf("dry-run 未打印自更新计划:\n%s", te.out.String())
	}
}
