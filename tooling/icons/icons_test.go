package icons

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// TestLucideSourceIsLocked proves the vendored JSON is the exact upstream release and still
// carries the metadata the version comment promises. | TestLucideSourceIsLocked 证明随包 JSON 正是
// 上游发布版本，并且仍带有版本注释所承诺的元数据。
func TestLucideSourceIsLocked(t *testing.T) {
	if err := VerifyLucideSource(); err != nil {
		t.Fatal(err)
	}
	set, err := Lucide()
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Icons) < 1900 {
		t.Fatalf("Lucide has %d icons, want at least 1900 | Lucide 只有 %d 个图标，至少应有 1900 个", len(set.Icons), len(set.Icons))
	}
	for _, name := range []string{"log-in", "log-out", "sun", "moon", "monitor", "languages"} {
		icon, ok := set.Icons[name]
		if !ok || !strings.Contains(icon.Body, "<") {
			t.Errorf("Lucide is missing a usable %q icon | Lucide 缺少可用的 %q 图标", name, name)
		}
	}
}

// TestScanCollectsOnlyPartialCalls pins the one supported reference form and the deduplication
// across template roots. | TestScanCollectsOnlyPartialCalls 固定唯一受支持的引用形式以及跨模板根的去重。
func TestScanCollectsOnlyPartialCalls(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeTestFile(t, fsys, "web/templates/partials/icon.html", `{{template "partials/icon" "lucide-user"}}`)
	writeTestFile(t, fsys, "web/templates/pages/a.html", `{{ template "partials/icon" "lucide-log-in" }}`)
	writeTestFile(t, fsys, "web/templates/pages/b.html", `{{- template "partials/icon" "lucide-log-in" -}}`)
	writeTestFile(t, fsys, "tooling/scaffold/templates/_name_/web/templates/pages/c.html", `{{template "partials/icon" "lucide-languages"}}`)
	writeTestFile(t, fsys, "web/templates/pages/d.html", `<svg><use href="#i-lucide-sun"></use></svg>`)
	writeTestFile(t, fsys, "web/templates/portal/pages/e.html", `{{ template "portal/partials/icon" "lucide-sun" }}`)
	writeTestFile(t, fsys, "web/templates/mgr/pages/f.html", `{{template "mgr/partials/icon" "lucide-mail"}}`)

	refs, err := Scan(fsys, []string{"web/templates", "tooling/scaffold/templates/_name_/web/templates"})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 4 {
		t.Fatalf("refs = %[1]v, want 4 | 引用为 %[1]v，期望 4 个", refs)
	}
	want := []string{"lucide-languages", "lucide-log-in", "lucide-mail", "lucide-sun"}
	for i, name := range want {
		if refs[i].Name != name {
			t.Fatalf("refs = %[1]v, want %[2]v | 引用为 %[1]v，期望 %[2]v", refs, want)
		}
	}
	if len(refs[1].Files) != 2 {
		t.Errorf("log-in files = %[1]v, want two templates | log-in 文件为 %[1]v，期望两个模板", refs[1].Files)
	}
}

// TestBuildSpriteEmitsOnlyTheSubset pins the on-demand contract: only referenced icons appear in
// the sprite, and the file carries its provenance and generated marker. | TestBuildSpriteEmitsOnlyTheSubset
// 固定按需契约：只有被引用的图标进入 sprite，文件带来源与生成标记。
func TestBuildSpriteEmitsOnlyTheSubset(t *testing.T) {
	set, err := Lucide()
	if err != nil {
		t.Fatal(err)
	}
	data, err := Build(set, []Reference{{Name: "lucide-log-in"}, {Name: "lucide-languages"}, {Name: "lucide-log-in"}})
	if err != nil {
		t.Fatal(err)
	}
	sprite := string(data)
	for _, want := range []string{
		GeneratedMarker,
		"Lucide " + LucideVersion,
		LucideLicense,
		`id="i-lucide-log-in"`,
		`id="i-lucide-languages"`,
		`viewBox="0 0 24 24"`,
	} {
		if !strings.Contains(sprite, want) {
			t.Errorf("sprite is missing %q | sprite 缺少 %q", want, want)
		}
	}
	for _, unwanted := range []string{`id="i-lucide-user"`, `id="i-lucide-sun"`} {
		if strings.Contains(sprite, unwanted) {
			t.Errorf("sprite contains unreferenced %q | sprite 包含未引用的 %q", unwanted, unwanted)
		}
	}
	if strings.Count(sprite, `id="i-lucide-log-in"`) != 1 {
		t.Error("duplicate reference was emitted twice | 重复引用被输出了两次")
	}
}

// TestBuildRejectsUnknownAndUnsafeIcons keeps a misspelled name or a hostile body out of the
// committed sprite. | TestBuildRejectsUnknownAndUnsafeIcons 阻止拼错的图标名或恶意图标体进入已提交的 sprite。
func TestBuildRejectsUnknownAndUnsafeIcons(t *testing.T) {
	set, err := Lucide()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(set, []Reference{{Name: "lucide-not-a-real-icon"}}); err == nil {
		t.Error("unknown icon was accepted | 未知图标被接受")
	}
	unsafe := &Set{
		Prefix: "lucide",
		Width:  24,
		Height: 24,
		Icons:  map[string]Icon{"bad": {Body: `<script>alert(1)</script>`}},
	}
	if _, err := Build(unsafe, []Reference{{Name: "lucide-bad"}}); err == nil {
		t.Error("unsafe icon body was accepted | 恶意图标体被接受")
	}
}

// TestBuildResolvesAliases proves an untransformed alias renders its parent. | TestBuildResolvesAliases
// 证明无变换的别名会渲染父图标。
func TestBuildResolvesAliases(t *testing.T) {
	set := &Set{
		Prefix: "lucide",
		Width:  24,
		Height: 24,
		Icons:  map[string]Icon{"user": {Body: `<circle cx="12" cy="12" r="4"/>`}},
		Aliases: map[string]Alias{
			"person": {Parent: "user"},
		},
	}
	data, err := Build(set, []Reference{{Name: "lucide-person"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `id="i-lucide-person"`) || !strings.Contains(string(data), `circle`) {
		t.Errorf("alias sprite = %[1]s | 别名 sprite = %[1]s", data)
	}
}

// TestSyncAndCheckRoundTrip exercises the writer and the drift decision. | TestSyncAndCheckRoundTrip
// 覆盖写入与漂移判定。
func TestSyncAndCheckRoundTrip(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeTestFile(t, fsys, "web/templates/a.html", `{{template "partials/icon" "lucide-log-in"}}`)
	opts := Options{FS: fsys, Roots: []string{"web/templates"}, Output: "web/static/img/icons.svg"}

	res, err := Sync(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Names) != 1 || res.Names[0] != "lucide-log-in" || res.Bytes == 0 {
		t.Fatalf("sync result = %[1]v | 同步结果为 %[1]v", res)
	}
	if _, err := Check(opts); err != nil {
		t.Fatalf("fresh sprite failed the check: %[1]v | 新生成的 sprite 未通过校验：%[1]v", err)
	}

	writeTestFile(t, fsys, "web/templates/a.html", `{{template "partials/icon" "lucide-log-out"}}`)
	if _, err := Check(opts); err == nil {
		t.Error("drift was not detected | 未检测到漂移")
	} else if !strings.Contains(err.Error(), "yd icons sync") {
		t.Errorf("drift error does not name the fix: %[1]v | 漂移错误没有给出修复命令：%[1]v", err)
	}
}

// TestRepositorySpriteMatchesTemplates is the repository drift guard: the committed sprite must
// contain exactly the icons the framework and scaffold templates reference. | TestRepositorySpriteMatchesTemplates
// 是仓库漂移守卫：已提交的 sprite 必须与框架及脚手架模板引用的图标完全一致。
func TestRepositorySpriteMatchesTemplates(t *testing.T) {
	opts := Options{
		FS:     afero.NewOsFs(),
		Roots:  []string{"../../web/templates", "../../tooling/scaffold/templates/_name_/web/templates"},
		Output: "../../web/static/img/icons.svg",
	}
	res, err := Check(opts)
	if err != nil {
		t.Fatalf("%[1]v (referenced icons: %[2]v) | %[1]v（引用图标：%[2]v）", err, res.Names)
	}
	if len(res.Names) == 0 {
		t.Fatal("the repository sprite is empty | 仓库 sprite 为空")
	}
}

func writeTestFile(t *testing.T, fsys afero.Fs, name, body string) {
	t.Helper()
	if dir := filepath.Dir(name); dir != "." {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := afero.WriteFile(fsys, name, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
