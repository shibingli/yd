package assets

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shibingli/yd/afs"
	"github.com/spf13/afero"
)

const (
	testManifestPath = "web/assets.lock.json"
	testRoot         = "web"
)

// countingTransport counts every RoundTrip and delegates to inner; it proves whether a client was used at all. | countingTransport 统计每次 RoundTrip 并转发给 inner，用于证明客户端是否真的被使用。
type countingTransport struct {
	inner http.RoundTripper
	calls atomic.Int64
}

func (t *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return t.inner.RoundTrip(r)
}

// newFileServer serves fixed bodies by URL path and counts every request that reaches it. | newFileServer 按 URL 路径返回固定内容，并统计到达服务端的请求数。
func newFileServer(t *testing.T, files map[string]string) (*httptest.Server, *atomic.Int64) {
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

// baseManifest builds a manifest with the given assets. | baseManifest 构造带有指定资产的清单。
func baseManifest(assets ...Asset) *Manifest {
	return &Manifest{Version: CurrentManifestVersion, Assets: assets}
}

// lockFixture writes one locked file plus its manifest entry under the test root. | lockFixture 在测试根目录下写入一个已锁定文件及其清单条目。
func lockFixture(t *testing.T, fsys afero.Fs, path, body string) Asset {
	t.Helper()
	rel := filepath.ToSlash(path)
	if _, err := afs.WriteFileIfChanged(fsys, filepath.Join(testRoot, filepath.FromSlash(rel)), []byte(body)); err != nil {
		t.Fatalf("写入夹具文件失败: %v", err)
	}
	return Asset{Name: rel, Version: "1.0.0", Path: rel, SHA256: sha256Hex([]byte(body)), Size: int64(len(body)), Kind: "js"}
}

// TestUpdateDownloadsAndLocksChecksum 首次锁定：下载到内存、校验通过后落盘并回填 SHA256/Size。
// TestUpdateDownloadsAndLocksChecksum locks on first run: download into memory, then write after verification.
func TestUpdateDownloadsAndLocksChecksum(t *testing.T) {
	const body = "console.log('htmx')"
	srv, hits := newFileServer(t, map[string]string{"/htmx.min.js": body})
	fsys := afero.NewMemMapFs()

	a := Asset{Name: "htmx", Version: "4.0.0", URL: srv.URL + "/htmx.min.js", Path: "static/js/vendor/htmx.min.js", Kind: "js", Required: true}
	if err := baseManifest(a).Save(fsys, testManifestPath); err != nil {
		t.Fatalf("保存清单失败: %v", err)
	}

	rep, err := Update(context.Background(), UpdateOptions{FS: fsys, ManifestPath: testManifestPath, Root: testRoot})
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if rep.Checked != 1 || len(rep.Updated) != 1 || rep.Updated[0] != "htmx" {
		t.Fatalf("报告不符合预期: %+v", rep)
	}
	if rep.Offline || len(rep.Failed) != 0 {
		t.Fatalf("报告不符合预期: %+v", rep)
	}

	got, err := afero.ReadFile(fsys, filepath.Join(testRoot, "static", "js", "vendor", "htmx.min.js"))
	if err != nil {
		t.Fatalf("读取落盘文件失败: %v", err)
	}
	if string(got) != body {
		t.Fatalf("落盘内容 = %q，期望 %q", got, body)
	}

	m, err := LoadManifest(fsys, testManifestPath)
	if err != nil {
		t.Fatalf("重新加载清单失败: %v", err)
	}
	locked, ok := m.Find("htmx")
	if !ok {
		t.Fatal("htmx is missing from the manifest | 清单中缺少 htmx")
	}
	if locked.SHA256 != sha256Hex([]byte(body)) {
		t.Errorf("SHA256 = %q，期望 %q", locked.SHA256, sha256Hex([]byte(body)))
	}
	if locked.Size != int64(len(body)) {
		t.Errorf("Size = %d，期望 %d", locked.Size, len(body))
	}
	if hits.Load() == 0 {
		t.Error("the test source received no request | 测试源未收到请求")
	}
}

// TestUpdateRejectsChecksumMismatchWithoutWriting 校验和不符必须报错且绝不落盘、绝不改写清单。
// TestUpdateRejectsChecksumMismatchWithoutWriting fails on mismatch and writes neither the file nor the lock.
func TestUpdateRejectsChecksumMismatchWithoutWriting(t *testing.T) {
	const (
		oldBody = "old content"
		newBody = "new content"
	)
	srv, _ := newFileServer(t, map[string]string{"/app.js": newBody})
	fsys := afero.NewMemMapFs()
	dest := filepath.Join(testRoot, "static", "app.js")
	if _, err := afs.WriteFileIfChanged(fsys, dest, []byte(oldBody)); err != nil {
		t.Fatalf("写入旧文件失败: %v", err)
	}

	a := Asset{Name: "app", Version: "1.0.0", URL: srv.URL + "/app.js", Path: "static/app.js", Kind: "js", SHA256: sha256Hex([]byte(oldBody))}
	if err := baseManifest(a).Save(fsys, testManifestPath); err != nil {
		t.Fatalf("保存清单失败: %v", err)
	}

	rep, err := Update(context.Background(), UpdateOptions{FS: fsys, ManifestPath: testManifestPath, Root: testRoot})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("期望 ErrChecksumMismatch，实际 %v（报告 %+v）", err, rep)
	}
	if len(rep.Failed) != 1 || rep.Failed[0] != "app" {
		t.Errorf("失败列表 = %v，期望 [app]", rep.Failed)
	}

	got, err := afero.ReadFile(fsys, dest)
	if err != nil {
		t.Fatalf("读取文件失败: %v", err)
	}
	if string(got) != oldBody {
		t.Errorf("文件被污染：内容 = %q，期望保持 %q", got, oldBody)
	}

	m, err := LoadManifest(fsys, testManifestPath)
	if err != nil {
		t.Fatalf("重新加载清单失败: %v", err)
	}
	if locked, _ := m.Find("app"); locked.SHA256 != sha256Hex([]byte(oldBody)) {
		t.Errorf("清单被改写：SHA256 = %q", locked.SHA256)
	}
}

// TestOfflineModeNeverPerformsNetwork 离线模式既不落盘也不触网：调用方客户端与服务端计数都必须为 0。
// TestOfflineModeNeverPerformsNetwork never touches the network in offline mode: both the caller's client and the server stay untouched.
func TestOfflineModeNeverPerformsNetwork(t *testing.T) {
	const goodBody = "offline ok"
	srv, hits := newFileServer(t, map[string]string{"/ok.js": goodBody})
	fsys := afero.NewMemMapFs()

	present := lockFixture(t, fsys, "static/ok.js", goodBody)
	missing := Asset{Name: "gone", Version: "1.0.0", URL: srv.URL + "/gone.js", Path: "static/gone.js", Kind: "js", SHA256: sha256Hex([]byte("never downloaded"))}
	if err := baseManifest(present, missing).Save(fsys, testManifestPath); err != nil {
		t.Fatalf("保存清单失败: %v", err)
	}

	ct := &countingTransport{inner: srv.Client().Transport}
	client := &http.Client{Transport: ct}

	rep, err := Update(context.Background(), UpdateOptions{
		FS: fsys, ManifestPath: testManifestPath, Root: testRoot,
		Offline: true, HTTPClient: client,
	})
	if err == nil {
		t.Fatal("offline mode must report a missing file | 离线模式下缺失文件必须报错")
	}
	if !errors.Is(err, ErrOfflineMissing) {
		t.Errorf("期望 ErrOfflineMissing，实际 %v", err)
	}
	if ct.calls.Load() != 0 {
		t.Errorf("离线模式使用了调用方客户端 %d 次", ct.calls.Load())
	}
	if hits.Load() != 0 {
		t.Errorf("离线模式触网 %d 次", hits.Load())
	}
	if !rep.Offline {
		t.Error("the report must be flagged Offline | 报告应标记 Offline")
	}
	if len(rep.Unchanged) != 1 || rep.Unchanged[0] != "static/ok.js" {
		t.Errorf("Unchanged = %v，期望 [static/ok.js]", rep.Unchanged)
	}
	if len(rep.Failed) != 1 || rep.Failed[0] != "gone" {
		t.Errorf("Failed = %v，期望 [gone]", rep.Failed)
	}
	if afs.Exists(fsys, filepath.Join(testRoot, "static", "gone.js")) {
		t.Error("offline mode must not create files | 离线模式不得创建文件")
	}
}

// TestOfflineModeInjectsTripwireThatIsNeverCalled 离线模式注入的传输层必须从未被调用，缺失文件也必须走本地分支。
// TestOfflineModeInjectsTripwireThatIsNeverCalled requires the injected transport to stay untouched, missing files included.
func TestOfflineModeInjectsTripwireThatIsNeverCalled(t *testing.T) {
	srv, hits := newFileServer(t, map[string]string{"/gone.js": "never fetched"})
	fsys := afero.NewMemMapFs()
	a := Asset{Name: "gone", Version: "1.0.0", URL: srv.URL + "/gone.js", Path: "static/gone.js",
		Kind: "js", SHA256: sha256Hex([]byte("never downloaded"))}
	if err := baseManifest(a).Save(fsys, testManifestPath); err != nil {
		t.Fatalf("保存清单失败: %v", err)
	}

	var tripwire *OfflineTransport
	restore := newOfflineClient
	newOfflineClient = func() *http.Client {
		client := NewOfflineClient()
		tripwire, _ = client.Transport.(*OfflineTransport)
		return client
	}
	t.Cleanup(func() { newOfflineClient = restore })

	rep, err := Update(context.Background(), UpdateOptions{
		FS: fsys, ManifestPath: testManifestPath, Root: testRoot, Offline: true,
	})
	if !errors.Is(err, ErrOfflineMissing) {
		t.Fatalf("期望 ErrOfflineMissing，实际 %v（报告 %+v）", err, rep)
	}
	if tripwire == nil {
		t.Fatal("offline mode injected no tripwire | 离线模式未注入任何拦截器")
	}
	if got := tripwire.Calls(); got != 0 {
		t.Errorf("注入的拦截器被调用 %d 次，期望 0 次", got)
	}
	if hits.Load() != 0 {
		t.Errorf("离线模式触网 %d 次", hits.Load())
	}
	if len(rep.Failed) != 1 || rep.Failed[0] != "gone" {
		t.Errorf("Failed = %v，期望 [gone]", rep.Failed)
	}
}

// TestOfflineVerifyPassesWithAllFilesPresent 离线模式下全部文件齐备且校验和一致时通过，且依旧零网络。
// TestOfflineVerifyPassesWithAllFilesPresent passes offline when every file is present and locked, still with zero network.
func TestOfflineVerifyPassesWithAllFilesPresent(t *testing.T) {
	srv, hits := newFileServer(t, map[string]string{"/a.js": "aaa", "/b.css": "bbb"})
	fsys := afero.NewMemMapFs()
	first := lockFixture(t, fsys, "static/a.js", "aaa")
	second := lockFixture(t, fsys, "static/b.css", "bbb")
	if err := baseManifest(first, second).Save(fsys, testManifestPath); err != nil {
		t.Fatalf("保存清单失败: %v", err)
	}

	ct := &countingTransport{inner: srv.Client().Transport}
	rep, err := Update(context.Background(), UpdateOptions{
		FS: fsys, ManifestPath: testManifestPath, Root: testRoot,
		Offline: true, HTTPClient: &http.Client{Transport: ct},
	})
	if err != nil {
		t.Fatalf("离线校验应通过: %v", err)
	}
	if len(rep.Unchanged) != 2 || len(rep.Updated) != 0 || len(rep.Failed) != 0 {
		t.Fatalf("报告不符合预期: %+v", rep)
	}
	if ct.calls.Load() != 0 || hits.Load() != 0 {
		t.Errorf("离线模式触网：client=%d server=%d", ct.calls.Load(), hits.Load())
	}
}

// TestCheckOnlyDoesNotWrite --check 只报告不落盘、不改写清单。
// TestCheckOnlyDoesNotWrite reports only: it writes neither the file nor the manifest.
func TestCheckOnlyDoesNotWrite(t *testing.T) {
	const body = "check only body"
	srv, _ := newFileServer(t, map[string]string{"/c.js": body})
	fsys := afero.NewMemMapFs()

	a := Asset{Name: "c", Version: "2.0.0", URL: srv.URL + "/c.js", Path: "static/c.js", Kind: "js"}
	if err := baseManifest(a).Save(fsys, testManifestPath); err != nil {
		t.Fatalf("保存清单失败: %v", err)
	}

	rep, err := Update(context.Background(), UpdateOptions{FS: fsys, ManifestPath: testManifestPath, Root: testRoot, CheckOnly: true})
	if err != nil {
		t.Fatalf("--check 不应报错: %v", err)
	}
	if len(rep.Updated) != 1 || rep.Updated[0] != "c" {
		t.Fatalf("报告应指出待更新项: %+v", rep)
	}
	if afs.Exists(fsys, filepath.Join(testRoot, "static", "c.js")) {
		t.Error("--check must not write files | --check 不得写入文件")
	}
	m, err := LoadManifest(fsys, testManifestPath)
	if err != nil {
		t.Fatalf("重新加载清单失败: %v", err)
	}
	if locked, _ := m.Find("c"); locked.SHA256 != "" {
		t.Errorf("--check 不得回填校验和，实际 %q", locked.SHA256)
	}
}

// TestSourceFallbackOrder 首个源 404 时必须命中第二个源。
// TestSourceFallbackOrder falls through to the second source when the first one answers 404.
func TestSourceFallbackOrder(t *testing.T) {
	const body = "from the second source"
	broken, brokenHits := newFileServer(t, map[string]string{})
	good, goodHits := newFileServer(t, map[string]string{"/app.js": body})
	fsys := afero.NewMemMapFs()

	a := Asset{Name: "app", Version: "1.0.0", URL: broken.URL + "/app.js", Path: "static/app.js", Kind: "js"}
	if err := baseManifest(a).Save(fsys, testManifestPath); err != nil {
		t.Fatalf("保存清单失败: %v", err)
	}

	rep, err := Update(context.Background(), UpdateOptions{
		FS: fsys, ManifestPath: testManifestPath, Root: testRoot,
		Sources: []string{broken.URL + "/app.js", good.URL + "/app.js"},
	})
	if err != nil {
		t.Fatalf("源回退失败: %v", err)
	}
	if len(rep.Updated) != 1 {
		t.Fatalf("报告不符合预期: %+v", rep)
	}
	if got, err := afero.ReadFile(fsys, filepath.Join(testRoot, "static", "app.js")); err != nil || string(got) != body {
		t.Fatalf("落盘内容 = %q (err=%v)，期望 %q", got, err, body)
	}
	if brokenHits.Load() != 1 {
		t.Errorf("首个源命中次数 = %d，期望 1", brokenHits.Load())
	}
	if goodHits.Load() != 1 {
		t.Errorf("第二个源命中次数 = %d，期望 1", goodHits.Load())
	}
}

// TestUpdateAggregatesFailures 部分失败时返回聚合错误，已成功的保持更新。
// TestUpdateAggregatesFailures returns a joined error while keeping the successful update.
func TestUpdateAggregatesFailures(t *testing.T) {
	const body = "ok body"
	srv, _ := newFileServer(t, map[string]string{"/ok.js": body})
	fsys := afero.NewMemMapFs()

	okAsset := Asset{Name: "ok", Version: "1.0.0", URL: srv.URL + "/ok.js", Path: "static/ok.js", Kind: "js"}
	badAsset := Asset{Name: "bad", Version: "1.0.0", URL: srv.URL + "/nope.js", Path: "static/bad.js", Kind: "js"}
	if err := baseManifest(okAsset, badAsset).Save(fsys, testManifestPath); err != nil {
		t.Fatalf("保存清单失败: %v", err)
	}

	rep, err := Update(context.Background(), UpdateOptions{FS: fsys, ManifestPath: testManifestPath, Root: testRoot})
	if !errors.Is(err, ErrDownloadFailed) {
		t.Fatalf("期望 ErrDownloadFailed，实际 %v", err)
	}
	if len(rep.Updated) != 1 || rep.Updated[0] != "ok" {
		t.Errorf("Updated = %v，期望 [ok]", rep.Updated)
	}
	if len(rep.Failed) != 1 || rep.Failed[0] != "bad" {
		t.Errorf("Failed = %v，期望 [bad]", rep.Failed)
	}
	if !afs.Exists(fsys, filepath.Join(testRoot, "static", "ok.js")) {
		t.Error("a successful asset must already be on disk | 成功的资产应当已落盘")
	}
	m, err := LoadManifest(fsys, testManifestPath)
	if err != nil {
		t.Fatalf("重新加载清单失败: %v", err)
	}
	if locked, _ := m.Find("ok"); locked.SHA256 != sha256Hex([]byte(body)) {
		t.Errorf("成功的资产未回填校验和：%q", locked.SHA256)
	}
}

// TestUpdateConcurrentAssets 并发下载在 -race 下不得出现数据竞争，且报告顺序稳定。
// TestUpdateConcurrentAssets keeps concurrent downloads race-free and the report ordering stable.
func TestUpdateConcurrentAssets(t *testing.T) {
	files := map[string]string{}
	assets := make([]Asset, 0, 24)
	for i := 0; i < 24; i++ {
		name := "f" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		body := "body of " + name
		files["/"+name+".js"] = body
		assets = append(assets, Asset{Name: name, Version: "1.0.0", Path: "static/" + name + ".js", Kind: "js"})
	}
	srv, _ := newFileServer(t, files)
	for i := range assets {
		assets[i].URL = srv.URL + "/" + assets[i].Name + ".js"
	}
	fsys := afero.NewMemMapFs()
	if err := baseManifest(assets...).Save(fsys, testManifestPath); err != nil {
		t.Fatalf("保存清单失败: %v", err)
	}

	rep, err := Update(context.Background(), UpdateOptions{
		FS: fsys, ManifestPath: testManifestPath, Root: testRoot, Concurrency: 4,
	})
	if err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if len(rep.Updated) != len(assets) || rep.Checked != len(assets) {
		t.Fatalf("报告不符合预期: checked=%d updated=%d", rep.Checked, len(rep.Updated))
	}
	for i, name := range rep.Updated {
		if name != assets[i].Name {
			t.Fatalf("报告顺序不稳定：第 %d 项 = %q，期望 %q", i, name, assets[i].Name)
		}
	}
}

// TestAddLocksChecksumAndAppendsToManifest yd assets add 先下载校验再追加并锁定。
// TestAddLocksChecksumAndAppendsToManifest downloads and locks first, then appends to the manifest.
func TestAddLocksChecksumAndAppendsToManifest(t *testing.T) {
	const body = "idiomorph"
	srv, hits := newFileServer(t, map[string]string{"/morph.js": body})
	fsys := afero.NewMemMapFs()

	locked, err := Add(context.Background(), UpdateOptions{
		FS: fsys, ManifestPath: testManifestPath, Root: testRoot,
		Sources: []string{srv.URL + "/{file}"},
	}, Asset{Name: "idiomorph", Version: "0.8.0", URL: srv.URL + "/morph.js", Path: "static/js/vendor/idiomorph.min.js", Kind: "js"})
	if err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if locked.SHA256 != sha256Hex([]byte(body)) || locked.Size != int64(len(body)) {
		t.Fatalf("锁定结果不符合预期: %+v", locked)
	}
	if hits.Load() == 0 {
		t.Error("the test source received no request | 测试源未收到请求")
	}

	m, err := LoadManifest(fsys, testManifestPath)
	if err != nil {
		t.Fatalf("重新加载清单失败: %v", err)
	}
	found, ok := m.Find("idiomorph")
	if !ok || found.SHA256 != locked.SHA256 {
		t.Fatalf("清单未追加锁定条目: %+v", m.Assets)
	}
	if got, err := afero.ReadFile(fsys, filepath.Join(testRoot, "static", "js", "vendor", "idiomorph.min.js")); err != nil || string(got) != body {
		t.Fatalf("落盘内容 = %q (err=%v)", got, err)
	}
}

// TestManifestLoadSaveFindAdd 清单读写往返、查找、追加与校验。
// TestManifestLoadSaveFindAdd covers the manifest round trip, lookup, append and validation.
func TestManifestLoadSaveFindAdd(t *testing.T) {
	fsys := afero.NewMemMapFs()

	if _, err := LoadManifest(fsys, testManifestPath); !errors.Is(err, ErrManifestNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("缺失清单应返回 ErrManifestNotFound 且可判定 fs.ErrNotExist，实际 %v", err)
	}

	m := baseManifest(Asset{Name: "b", Path: "static/b.js", SHA256: strings.Repeat("a", 64)})
	if err := m.Add(Asset{Name: "a", Path: "static/a.js", Kind: "js"}); err != nil {
		t.Fatalf("Add 失败: %v", err)
	}
	if _, ok := m.Find("b"); !ok {
		t.Error("Find missed an existing asset | Find 未命中已存在的资产")
	}
	if _, ok := m.Find("zzz"); ok {
		t.Error("Find must not hit a non-existent asset | Find 不应命中不存在的资产")
	}

	if err := m.Save(fsys, testManifestPath); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	if m.GeneratedAt.IsZero() {
		t.Error("Save must write GeneratedAt | Save 应写入 GeneratedAt")
	}

	reloaded, err := LoadManifest(fsys, testManifestPath)
	if err != nil {
		t.Fatalf("LoadManifest 失败: %v", err)
	}
	if reloaded.Version != CurrentManifestVersion || len(reloaded.Assets) != 2 {
		t.Fatalf("往返结果不符合预期: %+v", reloaded)
	}
	if reloaded.Assets[0].Name != "a" || reloaded.Assets[1].Name != "b" {
		t.Errorf("清单未按名字排序: %v", reloaded.Assets)
	}

	// Re-adding the same name replaces the entry instead of duplicating it. | 同名覆盖而非重复追加。
	if err := reloaded.Add(Asset{Name: "a", Path: "static/a2.js", Kind: "js"}); err != nil {
		t.Fatalf("Add 覆盖失败: %v", err)
	}
	if len(reloaded.Assets) != 2 {
		t.Errorf("同名资产应覆盖，实际 %d 条", len(reloaded.Assets))
	}
	if got, _ := reloaded.Find("a"); got.Path != "static/a2.js" {
		t.Errorf("覆盖后的 Path = %q", got.Path)
	}

	// Invalid entries are rejected. | 非法条目必须被拒绝。
	for _, bad := range []Asset{
		{Path: "static/x.js"},
		{Name: "x"},
		{Name: "x", Path: "../escape.js"},
		{Name: "x", Path: "/abs/x.js"},
	} {
		if err := reloaded.Add(bad); !errors.Is(err, ErrInvalidAsset) {
			t.Errorf("Add(%+v) 期望 ErrInvalidAsset，实际 %v", bad, err)
		}
	}
}

// TestManifestInvalidAndFutureVersion 损坏清单与高版本清单必须被拒绝。
// TestManifestInvalidAndFutureVersion rejects corrupt manifests and future versions.
func TestManifestInvalidAndFutureVersion(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "broken.json", []byte("{not json"), 0o644); err != nil {
		t.Fatalf("写入损坏清单失败: %v", err)
	}
	if _, err := LoadManifest(fsys, "broken.json"); !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("损坏清单应返回 ErrManifestInvalid，实际 %v", err)
	}

	future := &Manifest{Version: CurrentManifestVersion + 1, Assets: []Asset{}}
	if err := future.Save(fsys, "future.json"); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	if _, err := LoadManifest(fsys, "future.json"); !errors.Is(err, ErrManifestVersion) {
		t.Errorf("高版本清单应返回 ErrManifestVersion，实际 %v", err)
	}
}

// TestVerifyReportsMissingMismatchAndOK 逐文件给出 ok/missing/mismatch 且任一不合规即报错。
// TestVerifyReportsMissingMismatchAndOK reports ok/missing/mismatch per file and fails when anything is off.
func TestVerifyReportsMissingMismatchAndOK(t *testing.T) {
	fsys := afero.NewMemMapFs()
	ok := lockFixture(t, fsys, "static/ok.js", "good")
	if _, err := afs.WriteFileIfChanged(fsys, filepath.Join(testRoot, "static", "bad.js"), []byte("tampered")); err != nil {
		t.Fatalf("写入篡改文件失败: %v", err)
	}
	m := baseManifest(
		ok,
		Asset{Name: "bad", Version: "1.0.0", Path: "static/bad.js", SHA256: sha256Hex([]byte("original")), Kind: "js"},
		Asset{Name: "missing", Version: "1.0.0", Path: "static/missing.js", SHA256: sha256Hex([]byte("nope")), Kind: "js"},
		Asset{Name: "unlocked", Version: "1.0.0", Path: "static/ok.js", Kind: "js"},
	)

	rep, err := Verify(fsys, m, testRoot)
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("期望 ErrVerifyFailed，实际 %v", err)
	}
	if len(rep.OK) != 1 || rep.OK[0] != "static/ok.js" {
		t.Errorf("OK = %v", rep.OK)
	}
	if len(rep.Missing) != 1 || rep.Missing[0] != "missing" {
		t.Errorf("Missing = %v", rep.Missing)
	}
	if len(rep.Mismatch) != 2 {
		t.Errorf("Mismatch = %v，期望包含 bad 与 unlocked", rep.Mismatch)
	}

	if _, err := Verify(nil, nil, testRoot); !errors.Is(err, ErrNilManifest) {
		t.Errorf("空清单应返回 ErrNilManifest，实际 %v", err)
	}
}

// TestVerifyAcceptsCleanTree 全部一致时 Verify 返回 nil 错误。
// TestVerifyAcceptsCleanTree returns a nil error when every locked asset matches.
func TestVerifyAcceptsCleanTree(t *testing.T) {
	fsys := afero.NewMemMapFs()
	a := lockFixture(t, fsys, "static/a.js", "aaa")
	m := baseManifest(a)

	rep, err := Verify(fsys, m, testRoot)
	if err != nil {
		t.Fatalf("Verify 失败: %v", err)
	}
	if len(rep.OK) != 1 || len(rep.Missing) != 0 || len(rep.Mismatch) != 0 {
		t.Fatalf("报告不符合预期: %+v", rep)
	}
}

// TestOfflineTransportNeverDelegates 离线传输层直接拒绝且不转发给内层。
// TestOfflineTransportNeverDelegates refuses every request without delegating to any inner transport.
func TestOfflineTransportNeverDelegates(t *testing.T) {
	srv, hits := newFileServer(t, map[string]string{"/x.js": "x"})

	client := NewOfflineClient()
	transport, ok := client.Transport.(*OfflineTransport)
	if !ok {
		t.Fatalf("NewOfflineClient 的 Transport 类型 = %T", client.Transport)
	}

	resp, err := client.Get(srv.URL + "/x.js")
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrOffline) {
		t.Fatalf("期望 ErrOffline，实际 %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("离线传输层触网 %d 次", hits.Load())
	}
	if transport.Calls() != 1 {
		t.Errorf("Calls() = %d，期望 1", transport.Calls())
	}
}

// TestRenderSourceTemplates 源模板按 {pkg}/{ver}/{file} 渲染，无法推导时跳过。
// TestRenderSourceTemplates renders {pkg}/{ver}/{file} and skips templates whose placeholders cannot be resolved.
func TestRenderSourceTemplates(t *testing.T) {
	cases := []struct {
		name   string
		tmpl   string
		asset  Asset
		want   string
		wantOK bool
	}{
		{
			name: "jsdelivr | jsdelivr 源",
			tmpl: "https://cdn.jsdelivr.net/npm/{pkg}@{ver}/{file}",
			asset: Asset{Name: "htmx", Version: "4.0.0",
				URL: "https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"},
			want: "https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js", wantOK: true,
		},
		{
			name: "unpkg mirror | unpkg 镜像",
			tmpl: "https://unpkg.com/{pkg}@{ver}/{file}",
			asset: Asset{Name: "htmx", Version: "4.0.0",
				URL: "https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"},
			want: "https://unpkg.com/htmx.org@4.0.0/dist/htmx.min.js", wantOK: true,
		},
		{
			name: "scoped package | 作用域包",
			tmpl: "{pkg}@{ver}/{file}",
			asset: Asset{Name: "morph", Version: "3.17.4",
				URL: "https://cdn.jsdelivr.net/npm/@alpinejs/morph@3.17.4/dist/cdn.min.js"},
			want: "@alpinejs/morph@3.17.4/dist/cdn.min.js", wantOK: true,
		},
		{
			name:  "literal template | 字面量模板",
			tmpl:  "https://example.com/static/app.js",
			asset: Asset{Name: "app", URL: "https://example.com/static/app.js"},
			want:  "https://example.com/static/app.js", wantOK: true,
		},
		{
			name:   "unresolvable package | 无法推导包名",
			tmpl:   "https://cdn.jsdelivr.net/npm/{pkg}@{ver}/{file}",
			asset:  Asset{Name: "app", Version: "1.0.0", URL: "https://example.com/static/app.js"},
			wantOK: false,
		},
		{
			name:  "version derived from url | 版本取自 URL",
			tmpl:  "{pkg}@{ver}/{file}",
			asset: Asset{Name: "app", URL: "https://example.com/pkg@1.2.3/app.js"},
			want:  "pkg@1.2.3/app.js", wantOK: true,
		},
		{
			name:   "empty url | URL 为空",
			tmpl:   "{url}",
			asset:  Asset{Name: "app", Path: "static/app.js"},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := renderSource(tc.tmpl, tc.asset)
			if ok != tc.wantOK {
				t.Fatalf("renderSource ok = %v，期望 %v（got %q）", ok, tc.wantOK, got)
			}
			if ok && got != tc.want {
				t.Fatalf("renderSource = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestAssetLocalPath 校验 LocalPath 的拼接规则：清单路径始终用 "/"，落盘时按平台转换。
// TestAssetLocalPath keeps manifest paths slash-separated while converting them for the local filesystem.
func TestAssetLocalPath(t *testing.T) {
	a := Asset{Name: "htmx", Path: "static/js/vendor/htmx.min.js"}
	if got, want := a.LocalPath("web"), filepath.Join("web", "static", "js", "vendor", "htmx.min.js"); got != want {
		t.Errorf("LocalPath = %q，期望 %q", got, want)
	}
	if got, want := a.LocalPath(""), filepath.Join("static", "js", "vendor", "htmx.min.js"); got != want {
		t.Errorf("空 root 的 LocalPath = %q，期望 %q", got, want)
	}
}
