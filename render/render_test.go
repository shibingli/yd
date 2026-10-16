package render_test

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/yun-dev/yd/afs"
	"github.com/yun-dev/yd/render"
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

	// 片段可独立渲染（htmx 局部响应）
	var partial bytes.Buffer
	if err := e.Render(&partial, "pages/users.html", map[string]any{"Users": []string{"a", "b"}}); err != nil {
		t.Fatalf("片段渲染失败: %v", err)
	}
	if !strings.Contains(partial.String(), "<li>a</li>") || !strings.Contains(partial.String(), "<li>b</li>") {
		t.Fatalf("片段内容异常: %q", partial.String())
	}

	// 整页渲染：片段作为 Content 传入布局
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
	// 片段内容以 template.HTML 传入时不应被二次转义
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

	// 非 Dev 引擎必须继续使用缓存
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
	if err == nil || !strings.Contains(err.Error(), "模板不存在") {
		t.Fatalf("未知模板应返回哨兵错误，实际 %v", err)
	}
}

func TestNoTemplatesReturnsSentinel(t *testing.T) {
	if _, err := render.New(afs.NewMem(), render.Options{}); err == nil {
		t.Fatal("空文件系统应返回 ErrNoTemplates")
	}
	if _, err := render.New(nil, render.Options{}); err == nil {
		t.Fatal("nil 文件系统应返回 ErrNilFS")
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

	// 恶意数据即使取自 map 也必须被转义为 <，从而无法闭合 </script>
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

	// 同一路径两次渲染必须得到相同哈希（缓存生效）
	var second bytes.Buffer
	if err := e.Render(&second, "a.html", nil); err != nil {
		t.Fatalf("二次渲染失败: %v", err)
	}
	if second.String() != out {
		t.Fatalf("asset 哈希不稳定: %q vs %q", out, second.String())
	}

	// 内容变化后哈希必须变化
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

	// 原引擎不应受影响：缺少 shout 函数，渲染必然失败
	err = base.Render(&bytes.Buffer{}, "a.html", nil)
	if err == nil || !strings.Contains(err.Error(), "模板解析失败") {
		t.Fatalf("原引擎应因缺少函数而报错，实际 %v", err)
	}
}
