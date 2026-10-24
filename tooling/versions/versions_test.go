package versions

import (
	"context"
	"errors"
	"go/format"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/tooling/assets"
)

// testGoMod is a fixture covering block and single-line requirements plus an indirect entry. | testGoMod 是覆盖块状与单行依赖以及间接依赖的夹具。
const testGoMod = `module github.com/yun-dev/demo

go 1.27.3

require (
	github.com/shibingli/yd v1.2.3
	github.com/shibingli/ydx/errs v0.1.0
	github.com/shibingli/ydx/log v0.1.0
	github.com/spf13/viper v1.21.0 // indirect
	github.com/klauspost/compress v1.20.3
)

require golang.org/x/sync v0.10.0

replace github.com/example/old => ../old
`

// testManifest is a minimal valid asset lock with one htmx entry. | testManifest 是只含一条 htmx 记录的最小合法资产锁。
const testManifest = `{
  "version": 1,
  "assets": [
    {
      "name": "htmx",
      "version": "4.0.0",
      "url": "https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js",
      "path": "static/js/vendor/htmx.min.js",
      "sha256": "0000000000000000000000000000000000000000000000000000000000000000",
      "size": 1
    },
    {
      "name": "alpinejs",
      "version": "3.17.4",
      "url": "https://cdn.jsdelivr.net/npm/alpinejs@3.17.4/dist/cdn.min.js",
      "path": "static/js/vendor/alpine.min.js",
      "sha256": "0000000000000000000000000000000000000000000000000000000000000000",
      "size": 1
    }
  ]
}`

// newFixture builds a memory file system with go.mod and the asset lock. | newFixture 构建含 go.mod 与资产锁的内存文件系统。
func newFixture(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeFile(t, fsys, GoModPath, testGoMod)
	writeFile(t, fsys, assets.DefaultManifestPath, testManifest)
	return fsys
}

// writeFile writes one fixture file, creating its parent directories. | writeFile 写入一个夹具文件并创建其父目录。
func writeFile(t *testing.T, fsys afero.Fs, path, content string) {
	t.Helper()
	dir := path
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			dir = path[:i]
			break
		}
	}
	if dir != path {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}
	if err := afero.WriteFile(fsys, path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

// TestLoadDerivesScaffoldVersions verifies the two derived values and the direct-module filter. | TestLoadDerivesScaffoldVersions 验证两个推导值与直接依赖过滤。
func TestLoadDerivesScaffoldVersions(t *testing.T) {
	inv, err := Load(newFixture(t), ".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if inv.ModulePath != "github.com/yun-dev/demo" {
		t.Errorf("ModulePath = %q", inv.ModulePath)
	}
	if inv.GoDirective != "1.27.3" {
		t.Errorf("GoDirective = %q", inv.GoDirective)
	}
	if inv.HtmxVersion != "4.0.0" {
		t.Errorf("HtmxVersion = %q", inv.HtmxVersion)
	}
	for _, modulePath := range []string{"github.com/shibingli/ydx/errs", "github.com/shibingli/ydx/log"} {
		if got := inv.YdxVersions[modulePath]; got != "v0.1.0" {
			t.Errorf("YdxVersions[%q] = %q, want v0.1.0", modulePath, got)
		}
	}
	if len(inv.YdxVersions) != 2 {
		t.Errorf("YdxVersions = %v, want exactly the two required ydx modules", inv.YdxVersions)
	}
	if len(inv.YdxModules) != len(scaffoldYdxModules) {
		t.Errorf("YdxModules = %d, want %d", len(inv.YdxModules), len(scaffoldYdxModules))
	}
	joinedYdx := strings.Join(inv.YdxModules, ",")
	for _, modulePath := range []string{"github.com/shibingli/ydx/cache", "github.com/shibingli/ydx/errs", "github.com/shibingli/ydx/log"} {
		if !strings.Contains(joinedYdx, modulePath) {
			t.Errorf("YdxModules missing %q: %v", modulePath, inv.YdxModules)
		}
	}
	want := []string{"github.com/klauspost/compress", "github.com/shibingli/yd", "github.com/shibingli/ydx/errs", "github.com/shibingli/ydx/log", "golang.org/x/sync"}
	if len(inv.Modules) != len(want) {
		t.Fatalf("Modules = %+v, want %v", inv.Modules, want)
	}
	for i := range want {
		if inv.Modules[i].Path != want[i] {
			t.Errorf("Modules[%d] = %q, want %q", i, inv.Modules[i].Path, want[i])
		}
	}
}

// TestLoadExcludesBenchmarkModules keeps benchmark-only modules out of the scaffold. |
// TestLoadExcludesBenchmarkModules 让仅用于基准的模块不进入脚手架常量。
func TestLoadExcludesBenchmarkModules(t *testing.T) {
	fsys := afero.NewMemMapFs()
	mod := strings.Replace(testGoMod,
		"github.com/shibingli/ydx/log v0.1.0",
		"github.com/shibingli/ydx/log v0.1.0\n\tgithub.com/shibingli/ydx/benchmarks/json v0.1.0", 1)
	writeFile(t, fsys, GoModPath, mod)
	writeFile(t, fsys, assets.DefaultManifestPath, testManifest)
	inv, err := Load(fsys, ".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := inv.YdxVersions["github.com/shibingli/ydx/benchmarks/json"]; ok {
		t.Errorf("benchmark module leaked into YdxVersions: %v", inv.YdxVersions)
	}
	for _, modulePath := range inv.YdxModules {
		if strings.Contains(modulePath, "/benchmarks") {
			t.Errorf("benchmark module leaked into YdxModules: %q", modulePath)
		}
	}
}

// TestLoadKeepsPerModuleVersions keeps each ydx module at its own tag. | TestLoadKeepsPerModuleVersions 让每个 ydx 模块保留自己的标签版本。
//
// ydx 的每个组件都是独立模块，按子目录标签发布，永远可能处于不同版本。把「所有模块共用
// 一个版本」当成不变量会让任何一次独立升级都表现为版本冲突，因此这里必须按模块记录。
// Every ydx component is an independent module released under its own subdirectory tag, so the
// versions can always differ. Treating one shared version as an invariant would make every
// independent upgrade look like a conflict, so the inventory records one version per module.
func TestLoadKeepsPerModuleVersions(t *testing.T) {
	fsys := afero.NewMemMapFs()
	mod := strings.Replace(testGoMod, "github.com/shibingli/ydx/log v0.1.0", "github.com/shibingli/ydx/log v0.1.1", 1)
	writeFile(t, fsys, GoModPath, mod)
	writeFile(t, fsys, assets.DefaultManifestPath, testManifest)
	inv, err := Load(fsys, ".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := inv.YdxVersions["github.com/shibingli/ydx/errs"]; got != "v0.1.0" {
		t.Errorf("errs version = %q, want v0.1.0", got)
	}
	if got := inv.YdxVersions["github.com/shibingli/ydx/log"]; got != "v0.1.1" {
		t.Errorf("log version = %q, want v0.1.1", got)
	}
}

// TestLoadIgnoresLocalReplacePlaceholders accepts the pseudo-version `yd new --local-replace` writes. |
// TestLoadIgnoresLocalReplacePlaceholders 接受 `yd new --local-replace` 写出的占位伪版本。
//
// A local-replace project starts with every ydx requirement at the zero pseudo-version. A later
// `go build -mod=mod` may resolve only the imported ones to the real tagged version, leaving the project
// with a mix of `v0.1.0` and placeholders. That mixture says nothing about a real version conflict: the
// replace directives decide the code, and the tagged requirement is the only meaningful version. |
// local-replace 项目初始时全部 ydx 依赖都是零伪版本；随后 `go build -mod=mod` 只会把实际 import 的模块
// 解析成真实标签版本，于是项目里同时存在 `v0.1.0` 与占位版本。这种混合并不代表真实的版本冲突：代码由
// replace 决定，真实标签版本才是唯一有意义的版本。
func TestLoadIgnoresLocalReplacePlaceholders(t *testing.T) {
	fsys := afero.NewMemMapFs()
	mod := strings.Replace(testGoMod, "github.com/shibingli/ydx/log v0.1.0",
		"github.com/shibingli/ydx/log v0.0.0-00010101000000-000000000000", 1)
	writeFile(t, fsys, GoModPath, mod)
	writeFile(t, fsys, assets.DefaultManifestPath, testManifest)
	inv, err := Load(fsys, ".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := inv.YdxVersions["github.com/shibingli/ydx/errs"]; got != "v0.1.0" {
		t.Errorf("errs version = %q, want v0.1.0（占位版本应被忽略）", got)
	}
	if _, ok := inv.YdxVersions["github.com/shibingli/ydx/log"]; ok {
		t.Errorf("log version = %q, want the placeholder to be ignored", inv.YdxVersions["github.com/shibingli/ydx/log"])
	}
	if len(inv.YdxModules) != len(scaffoldYdxModules) {
		t.Errorf("YdxModules = %d, want %d", len(inv.YdxModules), len(scaffoldYdxModules))
	}
}

// TestLoadAllPlaceholdersHasNoYdxVersion keeps a pure local-replace project valid. |
// TestLoadAllPlaceholdersHasNoYdxVersion 让纯 local-replace 项目保持合法。
//
// With no tagged requirement at all there is no real version to pin; returning an empty version keeps
// `yd versions check` usable in a project that has only local replacements. | 完全没有标签版本时没有可锁定
// 的真实版本；返回空版本可让只有本地 replace 的项目继续使用 `yd versions check`。
func TestLoadAllPlaceholdersHasNoYdxVersion(t *testing.T) {
	fsys := afero.NewMemMapFs()
	mod := testGoMod
	for _, path := range []string{"github.com/shibingli/ydx/errs", "github.com/shibingli/ydx/log"} {
		mod = strings.Replace(mod, path+" v0.1.0", path+" v0.0.0-00010101000000-000000000000", 1)
	}
	writeFile(t, fsys, GoModPath, mod)
	writeFile(t, fsys, assets.DefaultManifestPath, testManifest)
	inv, err := Load(fsys, ".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(inv.YdxVersions) != 0 {
		t.Errorf("YdxVersions = %v, want empty", inv.YdxVersions)
	}
}

// TestGenerateSyncCheckRoundTrip pins the generated content and the drift detection. | TestGenerateSyncCheckRoundTrip 钉住生成内容与漂移检测。
func TestGenerateSyncCheckRoundTrip(t *testing.T) {
	fsys := newFixture(t)
	if err := Check(fsys, "."); !errors.Is(err, ErrGeneratedDrift) {
		t.Fatalf("Check before Sync = %v, want ErrGeneratedDrift", err)
	}
	path, err := Sync(fsys, ".")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if path != GeneratedPath {
		t.Errorf("Sync path = %q, want %q", path, GeneratedPath)
	}
	data, err := afero.ReadFile(fsys, GeneratedPath)
	if err != nil {
		t.Fatalf("read generated: %v", err)
	}
	text := string(data)
	// gofmt aligns map values, so collapse whitespace runs before matching entries. |
	// gofmt 会对齐映射值，匹配条目前先折叠连续空白。
	flat := strings.Join(strings.Fields(text), " ")
	for _, want := range []string{
		"// Code generated by yd versions sync; DO NOT EDIT.",
		"package scaffold",
		`const GoVersion = "1.27.3"`,
		`const HtmxVersion = "4.0.0"`,
		`var YdxVersions = map[string]string{`,
		`"github.com/shibingli/ydx/errs": "v0.1.0",`,
		`"github.com/shibingli/ydx/log": "v0.1.0",`,
		`"github.com/shibingli/ydx/errs"`,
		`"github.com/shibingli/ydx/log"`,
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("generated file missing %q:\n%s", want, text)
		}
	}
	if err := Check(fsys, "."); err != nil {
		t.Fatalf("Check after Sync: %v", err)
	}
	// A source change that is not followed by Sync must be caught. | 真源变化后未执行 Sync 必须被发现。
	writeFile(t, fsys, GoModPath, strings.Replace(testGoMod, "go 1.27.3", "go 1.28.0", 1))
	if err := Check(fsys, "."); !errors.Is(err, ErrGeneratedDrift) {
		t.Fatalf("Check after drift = %v, want ErrGeneratedDrift", err)
	}
}

// TestGenerateIsGofmtStable keeps the generated file byte-identical after gofmt, so a formatting
// hook can never make `yd versions check` report spurious drift. | TestGenerateIsGofmtStable
// 保证生成文件经 gofmt 后逐字节不变，避免格式化钩子让 `yd versions check` 误报漂移。
func TestGenerateIsGofmtStable(t *testing.T) {
	inv, err := Load(newFixture(t), ".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	raw := Generate(inv)
	formatted, err := format.Source(raw)
	if err != nil {
		t.Fatalf("generated file is not valid Go: %v", err)
	}
	if string(formatted) != string(raw) {
		t.Errorf("generated file differs from gofmt:\n%s", formatted)
	}
}

// TestParseGoModInvalid rejects a module file without the mandatory directives. | TestParseGoModInvalid 拒绝缺少必需指令的模块文件。
func TestParseGoModInvalid(t *testing.T) {
	if _, _, _, _, err := parseGoMod("module x\n"); !errors.Is(err, ErrGoModInvalid) {
		t.Fatalf("err = %v, want ErrGoModInvalid", err)
	}
	if _, _, _, _, err := parseGoMod("go 1.27.3\n"); !errors.Is(err, ErrGoModInvalid) {
		t.Fatalf("err = %v, want ErrGoModInvalid", err)
	}
	if _, _, _, _, err := parseGoMod("module x\ngo 1.27.3\nrequire (\n"); !errors.Is(err, ErrGoModInvalid) {
		t.Fatalf("unclosed require block: err = %v, want ErrGoModInvalid", err)
	}
}

// TestParseNPMURL covers both mirrors and the scoped-package form. | TestParseNPMURL 覆盖两个镜像与带作用域的包形式。
func TestParseNPMURL(t *testing.T) {
	cases := []struct {
		url, pkg, ver string
		ok            bool
	}{
		{"https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js", "htmx.org", "4.0.0", true},
		{"https://unpkg.com/alpinejs@3.17.4/dist/cdn.min.js", "alpinejs", "3.17.4", true},
		{"https://cdn.jsdelivr.net/npm/@scope/pkg@1.2.3/dist/x.js", "@scope/pkg", "1.2.3", true},
		{"https://cdn.jsdelivr.net/npm/htmx.org/dist/htmx.min.js", "", "", false},
		{"https://example.com/pkg@1.0.0/x.js", "", "", false},
	}
	for _, c := range cases {
		pkg, ver, ok := ParseNPMURL(c.url)
		if ok != c.ok || pkg != c.pkg || ver != c.ver {
			t.Errorf("ParseNPMURL(%q) = (%q,%q,%v), want (%q,%q,%v)", c.url, pkg, ver, ok, c.pkg, c.ver, c.ok)
		}
	}
}

// stubDoer answers registry requests from a package-to-metadata map. | stubDoer 从包到元数据的映射应答仓库请求。
type stubDoer struct {
	calls map[string]int
	meta  map[string]string
}

// Do implements Doer. | Do 实现 Doer。
func (s *stubDoer) Do(r *http.Request) (*http.Response, error) {
	pkg := strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/"), "%2F", "/")
	s.calls[pkg]++
	body, ok := s.meta[pkg]
	if !ok {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
}

// npmMeta builds an abbreviated registry document from stable versions. | npmMeta 由稳定版本构造精简仓库文档。
func npmMeta(versions ...string) string {
	var b strings.Builder
	b.WriteString(`{"dist-tags":{"latest":"`)
	b.WriteString(versions[len(versions)-1])
	b.WriteString(`"},"versions":{`)
	for i, v := range versions {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + v + `":{}`)
	}
	b.WriteString("}}")
	return b.String()
}

// TestPlanAssetUpgradesQueriesEachPackageOnce verifies grouping, rewriting and skip reasons. | TestPlanAssetUpgradesQueriesEachPackageOnce 验证分组、改写与跳过原因。
func TestPlanAssetUpgradesQueriesEachPackageOnce(t *testing.T) {
	inv, err := Load(newFixture(t), ".")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Two htmx-family assets sharing one package exercise the query cache. | 两个同属 htmx 包的资产用于验证查询缓存。
	inv.Assets = append(inv.Assets, assets.Asset{
		Name: "hx-ws", Version: "4.0.0",
		URL: "https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-ws.min.js",
	})
	inv.Assets = append(inv.Assets, assets.Asset{Name: "local", Version: "1", URL: "https://example.com/local.js"})
	doer := &stubDoer{calls: map[string]int{}, meta: map[string]string{
		"htmx.org": npmMeta("4.0.0", "4.1.0"),
		"alpinejs": npmMeta("3.17.4"),
	}}
	plan, err := PlanAssetUpgrades(context.Background(), doer, inv)
	if err != nil {
		t.Fatalf("PlanAssetUpgrades: %v", err)
	}
	if doer.calls["htmx.org"] != 1 {
		t.Errorf("htmx.org queried %d times, want 1", doer.calls["htmx.org"])
	}
	byName := map[string]AssetUpgrade{}
	for _, up := range plan {
		byName[up.Name] = up
	}
	if up := byName["htmx"]; !up.Changed || up.To != "4.1.0" ||
		up.NewURL != "https://cdn.jsdelivr.net/npm/htmx.org@4.1.0/dist/htmx.min.js" {
		t.Errorf("htmx upgrade = %+v", up)
	}
	if up := byName["alpinejs"]; up.Changed || up.Reason != "up to date | 已是最新" {
		t.Errorf("alpinejs upgrade = %+v", up)
	}
	if up := byName["local"]; up.Changed || !strings.Contains(up.Reason, "npm") {
		t.Errorf("local upgrade = %+v", up)
	}
	manifest := &assets.Manifest{Assets: inv.Assets}
	if n := ApplyAssetUpgrades(manifest, plan); n != 2 {
		t.Fatalf("ApplyAssetUpgrades = %d, want 2", n)
	}
	if a, _ := manifest.Find("hx-ws"); a.URL != "https://cdn.jsdelivr.net/npm/htmx.org@4.1.0/dist/ext/hx-ws.min.js" {
		t.Errorf("hx-ws URL = %q", a.URL)
	}
}

// TestResolveLatestNeverDowngrades pins the htmx track: `latest` sits on 2.x while the project
// follows the 4.x line published under `next`. | TestResolveLatestNeverDowngrades 钉住 htmx 轨道：
// `latest` 停在 2.x，而项目跟随发布在 `next` 下的 4.x 线。
func TestResolveLatestNeverDowngrades(t *testing.T) {
	doer := &stubDoer{calls: map[string]int{}, meta: map[string]string{
		"htmx.org": `{"dist-tags":{"latest":"2.0.11","next":"4.0.0"},"versions":{"2.0.11":{},"4.0.0":{}}}`,
	}}
	got, err := ResolveLatest(context.Background(), doer, "htmx.org", "4.0.0")
	if err != nil {
		t.Fatalf("ResolveLatest: %v", err)
	}
	if got != "4.0.0" {
		t.Fatalf("ResolveLatest = %q, want 4.0.0 (never downgrade to latest 2.0.11)", got)
	}
	// A newer stable release on the same major upgrades, and a prerelease is ignored. | 同大版本上的
	// 新稳定版会升级，预发布被忽略。
	doer.meta["htmx.org"] = `{"dist-tags":{"latest":"2.0.11","next":"4.2.0-beta1"},"versions":{"2.0.11":{},"4.0.0":{},"4.1.0":{},"4.2.0-beta1":{}}}`
	got, err = ResolveLatest(context.Background(), doer, "htmx.org", "4.0.0")
	if err != nil {
		t.Fatalf("ResolveLatest: %v", err)
	}
	if got != "4.1.0" {
		t.Fatalf("ResolveLatest = %q, want 4.1.0", got)
	}
}

// TestResolveLatestRejectsErrors covers the non-200 path. | TestResolveLatestRejectsErrors 覆盖非 200 路径。
func TestResolveLatestRejectsErrors(t *testing.T) {
	doer := &stubDoer{calls: map[string]int{}, meta: map[string]string{}}
	if _, err := ResolveLatest(context.Background(), doer, "missing", "1.0.0"); !errors.Is(err, ErrRegistry) {
		t.Fatalf("err = %v, want ErrRegistry", err)
	}
}

// TestCompareSemver covers the ordering used by the resolver. | TestCompareSemver 覆盖解析器使用的排序。
func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.4", "1.2.3", 1},
		{"1.3.0", "1.2.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"4.0.0", "2.0.11", 1},
		{"4.0.0", "4.0.0-beta1", 1},
		{"4.0.0-beta1", "4.0.0-beta2", -1},
		{"v1.2.3", "1.2.3", 0},
	}
	for _, c := range cases {
		a, ok := parseSemver(c.a)
		if !ok {
			t.Fatalf("parseSemver(%q) failed", c.a)
		}
		b, ok := parseSemver(c.b)
		if !ok {
			t.Fatalf("parseSemver(%q) failed", c.b)
		}
		if got := compareSemver(a, b); got != c.want {
			t.Errorf("compareSemver(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
