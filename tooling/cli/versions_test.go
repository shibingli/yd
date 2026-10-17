package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/tooling/versions"
)

// versionsGoMod is the go.mod fixture of the versions command tests. | versionsGoMod 是 versions 命令测试的 go.mod 夹具。
const versionsGoMod = `module github.com/yun-dev/demo

go 1.27.2

require github.com/shibingli/yd v1.2.3
`

// versionsManifest is the asset lock fixture with one upgradable and one current asset. | versionsManifest 是含一个可升级与一个已最新资产的资产锁夹具。
const versionsManifest = `{
  "version": 1,
  "assets": [
    {"name":"htmx","version":"4.0.0","url":"https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js","path":"static/js/vendor/htmx.min.js","sha256":"97702a55839da4ec5879c4eec4030c905e074b71d398cf763f40321de1e04c13","size":1},
    {"name":"alpinejs","version":"3.17.4","url":"https://cdn.jsdelivr.net/npm/alpinejs@3.17.4/dist/cdn.min.js","path":"static/js/vendor/alpine.min.js","sha256":"97702a55839da4ec5879c4eec4030c905e074b71d398cf763f40321de1e04c13","size":1}
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
	for _, want := range []string{"1.27.2", "4.0.0", "up to date | 已同步"} {
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
	if len(calls) != 2 || strings.Join(calls[0], " ") != "get -u ./..." || strings.Join(calls[1], " ") != "mod tidy" {
		t.Fatalf("go calls = %v", calls)
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
	if !strings.Contains(te.out.String(), "not present") {
		t.Errorf("check output does not announce the skip: %s", te.out.String())
	}
	err := Execute(context.Background(), []string{"versions", "sync"}, te.env)
	if !errors.Is(err, ErrNoScaffold) {
		t.Fatalf("sync err = %v, want ErrNoScaffold", err)
	}
	if _, statErr := fsys.Stat("tooling/scaffold"); statErr == nil {
		t.Error("sync created tooling/scaffold in a plain project")
	}
}
