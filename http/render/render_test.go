package render_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/http/render"
)

// writeTemplates 把若干模板文件写入内存文件系统。
// writeTemplates writes template files into an in-memory filesystem.
func writeTemplates(t *testing.T, files map[string]string) afero.Fs {
	t.Helper()
	mem := afs.NewMem()
	for name, body := range files {
		if err := afero.WriteFile(mem, name, []byte(body), 0o644); err != nil {
			t.Fatalf("写入模板 %s 失败: %v", name, err)
		}
	}
	return mem
}

// TestRenderLayoutWithPartial 验证「片段即文件内容、布局单独渲染」的约定。
// TestRenderLayoutWithPartial verifies the "a fragment is the file content, the layout renders separately" convention.
//
// 页面文件只写片段本身（不写 {{ define }}），这样多个页面不会在同一个模板集里
// 争抢同名的 define 块；整页渲染时由调用方先渲染片段，再把它作为 .Content 传给布局。
// A page file holds the fragment itself (no {{ define }}), so several pages never
// fight over the same define block in one template set; for a full page the caller
// renders the fragment first and passes it to the layout as .Content.
func TestRenderLayoutWithPartial(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		"templates/layouts/base.html": `<!doctype html><title>{{ default "yd" .Title }}</title><body>{{ .Content }}</body>`,
		"templates/pages/users.html":  `<ul>{{ range .Users }}<li>{{ . }}</li>{{ end }}</ul>`,
	})

	e, err := render.New(mem, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}

	// The fragment renders standalone (htmx partial response). | 片段可独立渲染（htmx 局部响应）
	var partial bytes.Buffer
	if err := e.Render(&partial, "pages/users.html", map[string]any{"Users": []string{"a", "b"}}); err != nil {
		t.Fatalf("片段渲染失败: %v", err)
	}
	if !strings.Contains(partial.String(), "<li>a</li>") || !strings.Contains(partial.String(), "<li>b</li>") {
		t.Fatalf("片段内容异常: %q", partial.String())
	}

	// Full-page render: the fragment is passed into the layout as Content. | 整页渲染：片段作为 Content 传入布局
	var page bytes.Buffer
	if err := e.Render(&page, "layouts/base.html", map[string]any{
		"Title":   "用户",
		"Content": template.HTML(partial.String()),
	}); err != nil {
		t.Fatalf("整页渲染失败: %v", err)
	}
	if !strings.Contains(page.String(), "<!doctype html>") || !strings.Contains(page.String(), "<li>a</li>") {
		t.Fatalf("整页内容异常: %q", page.String())
	}
	// Fragment content passed in as template.HTML must not be escaped twice. | 片段内容以 template.HTML 传入时不应被二次转义
	if strings.Contains(page.String(), "&lt;li&gt;") {
		t.Fatalf("片段被二次转义: %q", page.String())
	}
}

// TestEscapesUserDataByDefault 锁定 html/template 的转义语义。
// TestEscapesUserDataByDefault pins the escaping semantics of html/template.
//
// 这是安全回归测试：若有人把引擎换回 text/template，用户数据会被原样写入页面，
// 直接构成 XSS。只有显式转换的 template.HTML 才允许绕过转义。
// This is a security regression test: switching the engine back to text/template
// would write user data into the page verbatim, i.e. a direct XSS. Only values
// explicitly typed template.HTML may bypass escaping.
func TestEscapesUserDataByDefault(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		"templates/a.html": `<h1>{{ .Title }}</h1><div>{{ .Trusted }}</div><a href="{{ .Link }}">x</a>`,
	})
	e, err := render.New(mem, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}

	var buf bytes.Buffer
	if err := e.Render(&buf, "a.html", map[string]any{
		"Title":   `<script>alert(1)</script>`,
		"Trusted": template.HTML(`<b>ok</b>`),
		"Link":    `javascript:alert(2)`,
	}); err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	out := buf.String()

	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Fatalf("用户数据未被转义（XSS 风险）: %q", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatalf("期望出现转义后的实体: %q", out)
	}
	if !strings.Contains(out, "<b>ok</b>") {
		t.Fatalf("显式 template.HTML 应原样输出: %q", out)
	}
	if strings.Contains(out, `href="javascript:alert(2)"`) {
		t.Fatalf("危险 URL 方案未被拦截: %q", out)
	}
}

func TestNamesAreRelativeSlashPaths(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		"templates/layouts/base.html": `base`,
		"templates/pages/a.html":      `a`,
		"templates/partials/_x.html":  `partial`,
		"templates/readme.txt":        `not a template`,
	})
	e, err := render.New(mem, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	got := e.Names()
	want := []string{"layouts/base.html", "pages/a.html"}
	if len(got) != len(want) {
		t.Fatalf("模板名 = %v, 期望 %v（下划线前缀应视为局部片段、非模板扩展名应跳过）", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("模板名 = %v, 期望 %v", got, want)
		}
	}
}

func TestDevModePicksUpTemplateChange(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		"templates/pages/a.html": `v1`,
	})
	dev, err := render.New(mem, render.Options{Dev: true})
	if err != nil {
		t.Fatalf("New(dev) 失败: %v", err)
	}

	var first bytes.Buffer
	if err := dev.Render(&first, "pages/a.html", nil); err != nil {
		t.Fatalf("首次渲染失败: %v", err)
	}
	if first.String() != "v1" {
		t.Fatalf("首次渲染 = %q", first.String())
	}

	if err := afero.WriteFile(mem, "templates/pages/a.html", []byte("v2"), 0o644); err != nil {
		t.Fatalf("改写模板失败: %v", err)
	}
	var second bytes.Buffer
	if err := dev.Render(&second, "pages/a.html", nil); err != nil {
		t.Fatalf("二次渲染失败: %v", err)
	}
	if second.String() != "v2" {
		t.Fatalf("Dev 模式未拾取模板改动: %q", second.String())
	}

	// A non-Dev engine must keep using the cache. | 非 Dev 引擎必须继续使用缓存
	cached, err := render.New(mem, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err := afero.WriteFile(mem, "templates/pages/a.html", []byte("v3"), 0o644); err != nil {
		t.Fatalf("改写模板失败: %v", err)
	}
	var third bytes.Buffer
	if err := cached.Render(&third, "pages/a.html", nil); err != nil {
		t.Fatalf("缓存引擎渲染失败: %v", err)
	}
	if third.String() != "v2" {
		t.Fatalf("非 Dev 引擎不应重新加载: %q", third.String())
	}
}

func TestUnknownTemplateReturnsSentinel(t *testing.T) {
	mem := writeTemplates(t, map[string]string{"templates/a.html": `a`})
	e, err := render.New(mem, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	var buf bytes.Buffer
	err = e.Render(&buf, "pages/missing.html", nil)
	if err == nil || !strings.Contains(err.Error(), "template not found | 模板不存在") {
		t.Fatalf("未知模板应返回哨兵错误，实际 %v", err)
	}
}

func TestNoTemplatesReturnsSentinel(t *testing.T) {
	if _, err := render.New(afs.NewMem(), render.Options{}); err == nil {
		t.Fatal("an empty filesystem must return ErrNoTemplates | 空文件系统应返回 ErrNoTemplates")
	}
	if _, err := render.New(nil, render.Options{}); err == nil {
		t.Fatal("a nil filesystem must return ErrNilFS | nil 文件系统应返回 ErrNilFS")
	}
}

func TestFuncsDictDefaultAndJSON(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		// json 辅助函数返回 template.JS，只在 <script> 上下文原样输出 ——
		// 放在 HTML 文本上下文里 html/template 会按文本转义，这是正确的安全行为。
		// The json helper returns template.JS, which is emitted verbatim only in a
		// <script> context; in HTML text context html/template escapes it, and that
		// escaping is the correct, safe behaviour.
		"templates/a.html": `{{ $d := dict "Title" "T" "N" 3 }}{{ $d.Title }}|{{ default "fallback" "" }}|<script>const d = {{ json $d }};</script>`,
	})
	e, err := render.New(mem, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "a.html", nil); err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "T|fallback|") {
		t.Fatalf("dict/default 输出异常: %q", out)
	}
	if !strings.Contains(out, `"Title":"T"`) {
		t.Fatalf("json 辅助函数在 script 上下文应原样输出: %q", out)
	}

	// Hostile data taken from a map must still be escaped to &lt; so it cannot close </script>. | 恶意数据即使取自 map 也必须被转义为 <，从而无法闭合 </script>
	mem2 := writeTemplates(t, map[string]string{
		"templates/b.html": `<script>const x = {{ json .Evil }};</script>`,
	})
	e2, err := render.New(mem2, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	var buf2 bytes.Buffer
	if err := e2.Render(&buf2, "b.html", map[string]any{"Evil": "</script><img src=x onerror=alert(1)>"}); err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if strings.Contains(buf2.String(), "</script><img") {
		t.Fatalf("json 辅助函数未阻止脚本闭合注入: %q", buf2.String())
	}
	if !strings.Contains(buf2.String(), `<`) {
		t.Fatalf("json 辅助函数应把 < 转义为 \\u003c: %q", buf2.String())
	}
}

func TestAssetFuncAppendsContentHash(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		"templates/a.html":  `{{ asset "/static/css/yd.css" }}`,
		"static/css/yd.css": `body{color:blue}`,
	})
	e, err := render.New(mem, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "a.html", nil); err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "/static/css/yd.css?v=") {
		t.Fatalf("asset 输出异常: %q", out)
	}

	// Two renders of the same path must produce the same hash (the cache is in effect). | 同一路径两次渲染必须得到相同哈希（缓存生效）
	var second bytes.Buffer
	if err := e.Render(&second, "a.html", nil); err != nil {
		t.Fatalf("二次渲染失败: %v", err)
	}
	if second.String() != out {
		t.Fatalf("asset 哈希不稳定: %q vs %q", out, second.String())
	}

	// The hash must change once the content changes. | 内容变化后哈希必须变化
	if err := afero.WriteFile(mem, "static/css/yd.css", []byte("body{color:red}"), 0o644); err != nil {
		t.Fatalf("改写资源失败: %v", err)
	}
	fresh, err := render.New(mem, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	var third bytes.Buffer
	if err := fresh.Render(&third, "a.html", nil); err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if third.String() == out {
		t.Fatalf("资源内容变化后哈希应变化: %q", third.String())
	}
}

func TestHxHelperBuildsAttributes(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		"templates/a.html": `<button {{ hx "get" "/users" "target" "#list" }}>x</button>`,
	})
	e, err := render.New(mem, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "a.html", nil); err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`hx-get="/users"`, `hx-target="#list"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("hx 属性缺失 %s: %q", want, out)
		}
	}
}

func TestAddFuncReturnsIndependentEngine(t *testing.T) {
	mem := writeTemplates(t, map[string]string{"templates/a.html": `{{ shout "hi" }}`})

	// 使用 Dev 模式：解析推迟到渲染时，因此可以在模板已引用新函数之后再注册它。
	// Dev mode defers parsing to render time, so a new function can be registered
	// after the template already references it.
	base, err := render.New(mem, render.Options{Dev: true})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	withFunc := base.AddFunc("shout", strings.ToUpper)

	var buf bytes.Buffer
	if err := withFunc.Render(&buf, "a.html", nil); err != nil {
		t.Fatalf("派生引擎渲染失败: %v", err)
	}
	if buf.String() != "HI" {
		t.Fatalf("派生引擎输出 = %q", buf.String())
	}

	// The original engine must stay unaffected: the shout function is missing, so the render must fail. | 原引擎不应受影响：缺少 shout 函数，渲染必然失败
	err = base.Render(&bytes.Buffer{}, "a.html", nil)
	if err == nil || !strings.Contains(err.Error(), "cannot parse template | 模板解析失败") {
		t.Fatalf("原引擎应因缺少函数而报错，实际 %v", err)
	}
	if _, ok := base.FuncMap()["shout"]; ok {
		t.Fatal("AddFunc 不得修改原引擎的函数表 | AddFunc must not mutate the source engine's function map")
	}
}

// renderAsset executes one template with no data. | renderAsset 在无数据情况下执行一个模板。
func renderAsset(t *testing.T, e *render.Engine, name string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := e.Render(&buf, name, nil); err != nil {
		t.Fatalf("Render(%q) 失败: %v", name, err)
	}
	return buf.String()
}

// assetHash returns the first four sha256 bytes as hex, matching assetFunc. | assetHash
// 返回 sha256 前四字节的十六进制，与 assetFunc 一致。
func assetHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:4])
}

// TestAssetFuncPrefersMinifiedSiblingInProduction pins the production resolution:
// a .min.js/.min.css sibling wins, and its content is what the hash covers. | TestAssetFuncPrefersMinifiedSiblingInProduction
// 固定生产环境解析：优先使用 .min.js/.min.css 同族文件，哈希覆盖的是压缩内容。
func TestAssetFuncPrefersMinifiedSiblingInProduction(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		"templates/js.html":            `{{ asset "/static/js/app.js" }}`,
		"templates/css.html":           `{{ asset "/static/css/yd.css" }}`,
		"templates/already.html":       `{{ asset "/static/js/vendor/htmx.min.js" }}`,
		"static/js/app.js":             `console.log("source")`,
		"static/js/app.min.js":         `console.log("min")`,
		"static/css/yd.css":            `body{color:blue}`,
		"static/css/yd.min.css":        `body{color:red}`,
		"static/js/vendor/htmx.min.js": `/*min*/`,
	})
	e, err := render.New(mem, render.Options{Production: true})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}

	if got, want := renderAsset(t, e, "js.html"), "/static/js/app.min.js?v="+assetHash(`console.log("min")`); got != want {
		t.Errorf("js asset = %q，期望 %q", got, want)
	}
	if got, want := renderAsset(t, e, "css.html"), "/static/css/yd.min.css?v="+assetHash(`body{color:red}`); got != want {
		t.Errorf("css asset = %q，期望 %q", got, want)
	}
	if got, want := renderAsset(t, e, "already.html"), "/static/js/vendor/htmx.min.js?v="+assetHash(`/*min*/`); got != want {
		t.Errorf("already-minified asset = %q，期望 %q（不得二次加 .min）", got, want)
	}
}

// TestAssetFuncKeepsSourceOutsideProduction pins development behavior: the source
// path is served and hashed even when a .min sibling exists. | TestAssetFuncKeepsSourceOutsideProduction
// 固定开发行为：即使存在 .min 同族文件，也仍使用并哈希源码。
func TestAssetFuncKeepsSourceOutsideProduction(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		"templates/a.html":     `{{ asset "/static/js/app.js" }}`,
		"static/js/app.js":     `console.log("source")`,
		"static/js/app.min.js": `console.log("min")`,
	})
	e, err := render.New(mem, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if got, want := renderAsset(t, e, "a.html"), "/static/js/app.js?v="+assetHash(`console.log("source")`); got != want {
		t.Errorf("asset = %q，期望 %q", got, want)
	}
}

// TestAssetFuncFallsBackWhenMinifiedSiblingIsMissing pins the Tailwind-skip path:
// production without yd.min.css must still serve yd.css. | TestAssetFuncFallsBackWhenMinifiedSiblingIsMissing
// 固定 Tailwind 跳过时的回退：production 没有 yd.min.css 时仍使用 yd.css。
func TestAssetFuncFallsBackWhenMinifiedSiblingIsMissing(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		"templates/a.html":  `{{ asset "/static/css/yd.css" }}`,
		"static/css/yd.css": `body{color:blue}`,
	})
	e, err := render.New(mem, render.Options{Production: true})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if got, want := renderAsset(t, e, "a.html"), "/static/css/yd.css?v="+assetHash(`body{color:blue}`); got != want {
		t.Errorf("asset = %q，期望 %q", got, want)
	}
}

// TestAssetFuncUsesAssetFSForStaticPrefix pins the merged static tree lookup:
// /static/... is read from AssetFS, which is what the scaffold passes as web.Static(). | TestAssetFuncUsesAssetFSForStaticPrefix
// 固定合并静态树查找：/static/... 从 AssetFS 读取，正是脚手架传入的 web.Static()。
func TestAssetFuncUsesAssetFSForStaticPrefix(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		"templates/a.html": `{{ asset "/static/js/app.js" }}`,
		"static/js/app.js": `console.log("fs source")`,
	})
	assetFS := writeTemplates(t, map[string]string{
		"js/app.js":     `console.log("asset source")`,
		"js/app.min.js": `console.log("asset min")`,
	})
	e, err := render.New(mem, render.Options{Production: true, AssetFS: assetFS})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if got, want := renderAsset(t, e, "a.html"), "/static/js/app.min.js?v="+assetHash(`console.log("asset min")`); got != want {
		t.Errorf("asset = %q，期望 %q", got, want)
	}
}
