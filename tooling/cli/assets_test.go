package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/tooling/assets"
	"github.com/spf13/afero"
)

// countingRT counts every RoundTrip; CLI tests use it to prove that no request was issued. | countingRT 统计每次 RoundTrip，CLI 测试用它证明没有发起请求。
type countingRT struct {
	inner http.RoundTripper
	calls atomic.Int64
}

func (c *countingRT) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return c.inner.RoundTrip(r)
}

// useClient injects an HTTP client into the assets command for the duration of one test. | useClient 在测试期间为 assets 命令注入 HTTP 客户端。
func useClient(t *testing.T, client *http.Client) {
	t.Helper()
	old := assetsHTTPClient
	assetsHTTPClient = client
	t.Cleanup(func() { assetsHTTPClient = old })
}

// newAssetServer serves fixed bodies by URL path. | newAssetServer 按 URL 路径返回固定内容。
func newAssetServer(t *testing.T, files map[string]string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// seedManifest writes a manifest to the CLI's default location under the working directory. | seedManifest 把清单写入工作目录下的 CLI 默认位置。
func seedManifest(t *testing.T, fsys afero.Fs, as ...assets.Asset) {
	t.Helper()
	m := &assets.Manifest{Version: assets.CurrentManifestVersion, Assets: as}
	if err := m.Save(fsys, filepath.Join("web", "assets.lock.json")); err != nil {
		t.Fatalf("写入清单失败: %v", err)
	}
}

// seedFile writes one file under the CLI's default asset root. | seedFile 在 CLI 默认资产根目录下写入一个文件。
func seedFile(t *testing.T, fsys afero.Fs, rel, body string) {
	t.Helper()
	if _, err := afs.WriteFileIfChanged(fsys, filepath.Join("web", filepath.FromSlash(rel)), []byte(body)); err != nil {
		t.Fatalf("写入夹具文件 %s 失败: %v", rel, err)
	}
}

// TestAssetsListPrintsTable list 以表格输出名称/版本/体积/类型/校验和前 8 位。
// TestAssetsListPrintsTable prints name/version/size/kind and the first 8 checksum characters.
func TestAssetsListPrintsTable(t *testing.T) {
	const body = "hello world"
	fsys := afero.NewMemMapFs()
	seedFile(t, fsys, "static/js/vendor/htmx.min.js", body)
	sum, err := afs.Sha256File(fsys, filepath.Join("web", "static", "js", "vendor", "htmx.min.js"))
	if err != nil {
		t.Fatalf("计算校验和失败: %v", err)
	}
	seedManifest(t, fsys, assets.Asset{
		Name: "htmx", Version: "4.0.0", Path: "static/js/vendor/htmx.min.js",
		Kind: "js", SHA256: sum, Size: int64(len(body)), Required: true,
	})

	te := newTestEnv(t, fsys, "", false)
	if err := Execute(context.Background(), []string{"assets", "list"}, te.env); err != nil {
		t.Fatalf("assets list 失败: %v", err)
	}
	out := te.out.String()
	for _, want := range []string{"NAME", "VERSION", "SIZE", "KIND", "SHA256", "htmx", "4.0.0", "11 B", "js", sum[:8]} {
		if !strings.Contains(out, want) {
			t.Errorf("list 输出缺少 %q：\n%s", want, out)
		}
	}
}

// TestAssetsListMissingManifestErrors fails when the manifest is absent. | TestAssetsListMissingManifestErrors 清单缺失时 list 报错。
func TestAssetsListMissingManifestErrors(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	err := Execute(context.Background(), []string{"assets", "list"}, te.env)
	if !errors.Is(err, assets.ErrManifestNotFound) {
		t.Fatalf("期望 assets.ErrManifestNotFound，实际 %v", err)
	}
}

// TestAssetsVerifyOfflinePasses verify --offline 在校验和一致时通过且不触网。
// TestAssetsVerifyOfflinePasses passes with matching checksums and zero network traffic.
func TestAssetsVerifyOfflinePasses(t *testing.T) {
	const body = "verified body"
	fsys := afero.NewMemMapFs()
	seedFile(t, fsys, "static/app.js", body)
	sum, err := afs.Sha256File(fsys, filepath.Join("web", "static", "app.js"))
	if err != nil {
		t.Fatalf("计算校验和失败: %v", err)
	}
	seedManifest(t, fsys, assets.Asset{Name: "app", Version: "1.0.0", Path: "static/app.js", Kind: "js", SHA256: sum, Size: int64(len(body))})

	srv, hits := newAssetServer(t, map[string]string{"/static/app.js": body})
	ct := &countingRT{inner: srv.Client().Transport}
	useClient(t, &http.Client{Transport: ct})

	te := newTestEnv(t, fsys, "", false)
	if err := Execute(context.Background(), []string{"assets", "verify", "--offline"}, te.env); err != nil {
		t.Fatalf("assets verify --offline 失败: %v", err)
	}
	if !strings.Contains(te.out.String(), "app") {
		t.Errorf("verify 输出缺少资产名：\n%s", te.out.String())
	}
	if ct.calls.Load() != 0 || hits.Load() != 0 {
		t.Errorf("verify --offline 触网：client=%d server=%d", ct.calls.Load(), hits.Load())
	}
}

// TestAssetsVerifyReportsProblemsAndFails 缺失与不匹配都要出现在报告中并以非零错误结束。
// TestAssetsVerifyReportsProblemsAndFails reports missing/mismatched assets and ends with a non-zero error.
func TestAssetsVerifyReportsProblemsAndFails(t *testing.T) {
	fsys := afero.NewMemMapFs()
	seedFile(t, fsys, "static/tampered.js", "tampered")
	seedManifest(t, fsys,
		assets.Asset{Name: "tampered", Version: "1.0.0", Path: "static/tampered.js", Kind: "js", SHA256: strings.Repeat("0", 64)},
		assets.Asset{Name: "gone", Version: "1.0.0", Path: "static/gone.js", Kind: "js", SHA256: strings.Repeat("1", 64)},
	)

	te := newTestEnv(t, fsys, "", false)
	err := Execute(context.Background(), []string{"assets", "verify"}, te.env)
	if !errors.Is(err, assets.ErrVerifyFailed) {
		t.Fatalf("期望 assets.ErrVerifyFailed，实际 %v", err)
	}
	out := te.out.String()
	for _, want := range []string{"mismatch", "missing", "tampered", "gone"} {
		if !strings.Contains(out, want) {
			t.Errorf("verify 输出缺少 %q：\n%s", want, out)
		}
	}
}

// TestAssetsNoColorDisablesANSI --no-color 必须关闭 ANSI 转义序列。
// TestAssetsNoColorDisablesANSI turns ANSI escapes off through --no-color.
func TestAssetsNoColorDisablesANSI(t *testing.T) {
	fsys := afero.NewMemMapFs()
	seedFile(t, fsys, "static/tampered.js", "tampered")
	seedManifest(t, fsys, assets.Asset{Name: "tampered", Version: "1.0.0", Path: "static/tampered.js", Kind: "js", SHA256: strings.Repeat("0", 64)})

	te := newTestEnv(t, fsys, "", false)
	if err := Execute(context.Background(), []string{"assets", "verify"}, te.env); err == nil {
		t.Fatal("a mismatch must be an error | 不匹配应当报错")
	}
	if !strings.Contains(te.out.String(), "\x1b[") {
		t.Errorf("默认输出应带 ANSI 颜色：\n%s", te.out.String())
	}

	te = newTestEnv(t, fsys, "", false)
	if err := Execute(context.Background(), []string{"assets", "verify", "--no-color"}, te.env); err == nil {
		t.Fatal("a mismatch must be an error | 不匹配应当报错")
	}
	if strings.Contains(te.out.String(), "\x1b[") {
		t.Errorf("--no-color 后不应出现 ANSI 转义：\n%s", te.out.String())
	}
}

// TestAssetsUpdateWithSourceWritesFileAndLocks update 经 --source 下载、校验后落盘并回填校验和。
// TestAssetsUpdateWithSourceWritesFileAndLocks downloads through --source, verifies and locks the checksum.
func TestAssetsUpdateWithSourceWritesFileAndLocks(t *testing.T) {
	const body = "console.log('app')"
	srv, hits := newAssetServer(t, map[string]string{"/app.min.js": body})
	fsys := afero.NewMemMapFs()
	seedManifest(t, fsys, assets.Asset{
		Name: "app", Version: "2.0.0", URL: srv.URL + "/app.min.js",
		Path: "static/js/vendor/app.min.js", Kind: "js", Required: true,
	})

	te := newTestEnv(t, fsys, "", false)
	err := Execute(context.Background(), []string{"assets", "update", "--source", srv.URL + "/{file}"}, te.env)
	if err != nil {
		t.Fatalf("assets update 失败: %v", err)
	}
	if hits.Load() == 0 {
		t.Error("the test source got no request | 测试源未收到请求")
	}

	got, err := afero.ReadFile(fsys, filepath.Join("web", "static", "js", "vendor", "app.min.js"))
	if err != nil || string(got) != body {
		t.Fatalf("落盘内容 = %q (err=%v)，期望 %q", got, err, body)
	}
	m, err := assets.LoadManifest(fsys, filepath.Join("web", "assets.lock.json"))
	if err != nil {
		t.Fatalf("重新加载清单失败: %v", err)
	}
	locked, ok := m.Find("app")
	if !ok || locked.SHA256 == "" || locked.Size != int64(len(body)) {
		t.Fatalf("清单未回填锁定信息: %+v", locked)
	}
}

// TestAssetsUpdateCheckOnlyDoesNotWrite --check 只报告不落盘。
// TestAssetsUpdateCheckOnlyDoesNotWrite only reports and writes nothing.
func TestAssetsUpdateCheckOnlyDoesNotWrite(t *testing.T) {
	const body = "check body"
	srv, _ := newAssetServer(t, map[string]string{"/c.js": body})
	fsys := afero.NewMemMapFs()
	seedManifest(t, fsys, assets.Asset{Name: "c", Version: "1.0.0", URL: srv.URL + "/c.js", Path: "static/c.js", Kind: "js"})

	te := newTestEnv(t, fsys, "", false)
	if err := Execute(context.Background(), []string{"assets", "update", "--check"}, te.env); err != nil {
		t.Fatalf("assets update --check 失败: %v", err)
	}
	if afs.Exists(fsys, filepath.Join("web", "static", "c.js")) {
		t.Error("--check must not write a file | --check 不得写入文件")
	}
	if out := te.out.String(); !strings.Contains(out, "c") {
		t.Errorf("--check 输出缺少资产名：\n%s", out)
	}
}

// TestAssetsUpdateOfflineNeverTouchesNetwork 离线更新即使文件缺失也绝不发起请求。
// TestAssetsUpdateOfflineNeverTouchesNetwork issues no request even when files are missing.
func TestAssetsUpdateOfflineNeverTouchesNetwork(t *testing.T) {
	srv, hits := newAssetServer(t, map[string]string{"/gone.js": "never fetched"})
	fsys := afero.NewMemMapFs()
	seedManifest(t, fsys, assets.Asset{
		Name: "gone", Version: "1.0.0", URL: srv.URL + "/gone.js",
		Path: "static/gone.js", Kind: "js", SHA256: strings.Repeat("2", 64),
	})

	ct := &countingRT{inner: srv.Client().Transport}
	useClient(t, &http.Client{Transport: ct})

	te := newTestEnv(t, fsys, "", false)
	err := Execute(context.Background(), []string{"assets", "update", "--offline"}, te.env)
	if !errors.Is(err, assets.ErrOfflineMissing) {
		t.Fatalf("期望 assets.ErrOfflineMissing，实际 %v", err)
	}
	if ct.calls.Load() != 0 || hits.Load() != 0 {
		t.Errorf("离线更新触网：client=%d server=%d", ct.calls.Load(), hits.Load())
	}
}

// TestAssetsAddLocksChecksum add 追加资产、下载并锁定校验和。
// TestAssetsAddLocksChecksum appends an asset, downloads it and locks its checksum.
func TestAssetsAddLocksChecksum(t *testing.T) {
	const body = "alpine morph"
	srv, _ := newAssetServer(t, map[string]string{"/morph.min.js": body})
	fsys := afero.NewMemMapFs()

	te := newTestEnv(t, fsys, "", false)
	err := Execute(context.Background(), []string{
		"assets", "add", "idiomorph",
		"--url", srv.URL + "/morph.min.js",
		"--path", "static/js/vendor/idiomorph.min.js",
		"--version", "0.8.0",
		"--source", srv.URL + "/{file}",
	}, te.env)
	if err != nil {
		t.Fatalf("assets add 失败: %v", err)
	}

	m, err := assets.LoadManifest(fsys, filepath.Join("web", "assets.lock.json"))
	if err != nil {
		t.Fatalf("重新加载清单失败: %v", err)
	}
	locked, ok := m.Find("idiomorph")
	if !ok {
		t.Fatalf("清单未包含新资产: %+v", m.Assets)
	}
	if locked.Version != "0.8.0" || locked.SHA256 == "" || locked.Size != int64(len(body)) {
		t.Fatalf("资产未锁定: %+v", locked)
	}
	if got, err := afero.ReadFile(fsys, filepath.Join("web", "static", "js", "vendor", "idiomorph.min.js")); err != nil || string(got) != body {
		t.Fatalf("落盘内容 = %q (err=%v)", got, err)
	}
}

// TestAssetsAddRequiresURLAndPath add 缺少必需标志时报错。
// TestAssetsAddRequiresURLAndPath fails when mandatory flags are missing.
func TestAssetsAddRequiresURLAndPath(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"assets", "add", "x", "--url", "https://example.com/x.js"}, te.env); !errors.Is(err, ErrFlagParse) {
		t.Errorf("缺少 --path 应返回 ErrFlagParse，实际 %v", err)
	}

	te = newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"assets", "add", "--url", "https://example.com/x.js", "--path", "static/x.js"}, te.env); !errors.Is(err, ErrNoCommand) {
		t.Errorf("缺少资产名应返回 ErrNoCommand，实际 %v", err)
	}
}

// TestAssetsSubcommandDispatchErrors 子命令缺失或未知时的用法与错误。
// TestAssetsSubcommandDispatchErrors covers usage output and the error for missing/unknown subcommands.
func TestAssetsSubcommandDispatchErrors(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"assets"}, te.env); !errors.Is(err, ErrNoCommand) {
		t.Errorf("裸 assets 应返回 ErrNoCommand，实际 %v", err)
	}
	for _, want := range []string{"list", "update", "verify", "add"} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("assets 用法缺少 %q：\n%s", want, te.out.String())
		}
	}

	te = newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"assets", "bogus"}, te.env); !errors.Is(err, ErrUnknownCommand) {
		t.Errorf("未知子命令应返回 ErrUnknownCommand，实际 %v", err)
	}
}
