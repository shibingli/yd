package app

import (
	"bytes"
	"errors"
	"html/template"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/afs"
	"github.com/shibingli/yd/i18n"
	"github.com/shibingli/yd/render"
	"github.com/shibingli/yd/router"
)

// newTemplateFS writes the given files into a fresh in-memory asset filesystem
// and returns it, so "templates/..." paths sit at its root. | newTemplateFS 把给定
// 文件写入全新的内存资产文件系统并返回，使 "templates/..." 路径位于其根下。
func newTemplateFS(t *testing.T, files map[string]string) afero.Fs {
	t.Helper()

	fsys := afs.NewMem()
	for path, content := range files {
		if err := afero.WriteFile(fsys, path, []byte(content), 0o644); err != nil {
			t.Fatalf("写入 %s 失败: %v", path, err)
		}
	}
	return fsys
}

// renderString renders name through r and returns the output. | renderString 通过 r 渲染 name 并返回输出。
func renderString(t *testing.T, r router.Renderer, name string, data any) string {
	t.Helper()

	var buf bytes.Buffer
	if err := r.Render(&buf, name, data); err != nil {
		t.Fatalf("Render(%q) 失败: %v", name, err)
	}
	return buf.String()
}

func TestDefaultRendererRendersByRelativePath(t *testing.T) {
	r, err := newRenderer(newTemplateFS(t, map[string]string{
		"templates/layouts/base.html": testLayout,
		"templates/pages/home.html":   testPage,
	}), false)
	if err != nil {
		t.Fatalf("构建渲染器失败: %v", err)
	}

	if got := renderString(t, r, "pages/home.html", testData()); got != wantFragment {
		t.Errorf("片段输出 = %q, 期望 %q", got, wantFragment)
	}

	// The layout receives the fragment as template.HTML, so its markup reaches
	// the page verbatim instead of being escaped. | 布局以 template.HTML 接收片段，
	// 因此其标记原样进入页面而不是被转义。
	data := map[string]any{ContentKey: template.HTML("<b>x</b>"), "Title": "yd"}
	if got := renderString(t, r, DefaultLayout, data); !strings.Contains(got, "<b>x</b>") {
		t.Errorf("布局输出 = %q, 期望 template.HTML 型 Content 原样输出", got)
	}
}

func TestDefaultRendererCachesUnlessDev(t *testing.T) {
	fsys := newTemplateFS(t, map[string]string{"templates/one.html": "<p>one</p>"})

	cached, err := newRenderer(fsys, false)
	if err != nil {
		t.Fatalf("构建渲染器失败: %v", err)
	}
	if got := renderString(t, cached, "one.html", nil); got != "<p>one</p>" {
		t.Fatalf("首次输出 = %q", got)
	}

	// Rewriting the template must not affect an already cached renderer. | 改写模板不得影响已缓存的渲染器。
	if err := afero.WriteFile(fsys, "templates/one.html", []byte("<p>two</p>"), 0o644); err != nil {
		t.Fatalf("改写模板失败: %v", err)
	}
	if got := renderString(t, cached, "one.html", nil); got != "<p>one</p>" {
		t.Errorf("缓存输出 = %q, 期望仍为旧内容", got)
	}

	dev, err := newRenderer(fsys, true)
	if err != nil {
		t.Fatalf("构建 dev 渲染器失败: %v", err)
	}
	if got := renderString(t, dev, "one.html", nil); got != "<p>two</p>" {
		t.Errorf("dev 输出 = %q, 期望热生效的新内容", got)
	}
}

func TestDefaultRendererWithoutTemplates(t *testing.T) {
	// A filesystem without a templates directory: the renderer still builds,
	// and rendering reports the missing template. | 没有模板目录的文件系统：
	// 渲染器仍可构建，渲染时报告模板缺失。
	r, err := newRenderer(afs.NewMem(), false)
	if err != nil {
		t.Fatalf("缺少模板目录时不应报错: %v", err)
	}
	if err := r.Render(io.Discard, "pages/home.html", nil); err == nil {
		t.Error("Render must fail when no template exists | 没有模板时 Render 必须报错")
	}

	// An existing but empty templates directory behaves the same. | 存在但为空的模板目录行为一致。
	mem := afs.NewMem()
	if err := mem.MkdirAll(assetsTemplatesDir, 0o755); err != nil {
		t.Fatalf("创建模板目录失败: %v", err)
	}
	empty, err := newRenderer(mem, false)
	if err != nil {
		t.Fatalf("空模板目录不应报错: %v", err)
	}
	if err := empty.Render(io.Discard, "pages/home.html", nil); !errors.Is(err, render.ErrNoTemplates) {
		t.Errorf("空模板目录 Render 错误 = %v, 期望 render.ErrNoTemplates", err)
	}
}

func TestDefaultRendererReportsParseErrors(t *testing.T) {
	broken := func() afero.Fs {
		return newTemplateFS(t, map[string]string{"templates/bad.html": "{{ if }}"})
	}

	// Eager mode fails fast at construction. | 立即模式在构造期快速失败。
	if _, err := newRenderer(broken(), false); !errors.Is(err, render.ErrParseTemplate) {
		t.Errorf("立即模式构造错误 = %v, 期望 render.ErrParseTemplate", err)
	}

	// Hot-reload mode surfaces the error on render instead. | 热重载模式在渲染时暴露错误。
	dev, err := newRenderer(broken(), true)
	if err != nil {
		t.Fatalf("dev 模式构造不应报错: %v", err)
	}
	if err := dev.Render(io.Discard, "bad.html", nil); !errors.Is(err, render.ErrParseTemplate) {
		t.Errorf("dev 模式 Render 错误 = %v, 期望 render.ErrParseTemplate", err)
	}
}

func TestAssetDirResolvesEmbeddedSubtrees(t *testing.T) {
	// defaultAssetsFS re-roots the embedded tree at assets/, so templates/,
	// static/ and langs/ sit at the filesystem root. | defaultAssetsFS 把内嵌资产树
	// 重新定根到 assets/，因此 templates/、static/ 与 langs/ 位于文件系统根下。
	fsys, err := defaultAssetsFS()
	if err != nil {
		t.Fatalf("defaultAssetsFS 失败: %v", err)
	}

	tpl, err := assetDir(fsys, assetsTemplatesDir)
	if err != nil {
		t.Fatalf("assetDir(%q) 失败: %v", assetsTemplatesDir, err)
	}
	body, err := afero.ReadFile(tpl, DefaultLayout)
	if err != nil {
		t.Fatalf("读取内嵌布局失败: %v", err)
	}
	if !strings.Contains(string(body), ContentKey) {
		t.Errorf("内嵌布局必须使用 %s 键: %q", ContentKey, body)
	}

	static, err := assetDir(fsys, assetsStaticDir)
	if err != nil {
		t.Fatalf("assetDir(%q) 失败: %v", assetsStaticDir, err)
	}
	if !afs.Exists(static, "app.css") {
		t.Error("the static subtree must contain app.css | 静态子树必须包含 app.css")
	}

	// A missing subtree degrades to an empty filesystem instead of an error. | 缺失的子树降级为空文件系统而非报错。
	missing, err := assetDir(fsys, "nope")
	if err != nil {
		t.Fatalf("缺失子树不应报错: %v", err)
	}
	if afs.Exists(missing, "app.css") {
		t.Error("the missing subtree must be empty | 缺失子树必须是空的")
	}
}

// translatorTemplates is the fixture of the tr integration tests: a layout and two
// fragments that translate through {{ tr "..." }}, plus the two catalogs the i18n
// middleware negotiates. | translatorTemplates 是 tr 集成测试的夹具：一个布局与两个经
// `{{ tr "..." }}` 翻译的片段，以及 i18n 中间件参与协商的两份语言目录。
func translatorTemplates() map[string]string {
	return map[string]string{
		"templates/layouts/base.html": `<!DOCTYPE html><html><head><title>{{ tr "app.name" }}</title></head><body>{{ .Content }}</body></html>`,
		"templates/pages/home.html":   `<h1>{{ tr "page.title" }}</h1>`,
		"langs/en.yaml":               "app:\n  name: yd-en\npage:\n  title: Home\n",
		"langs/zh-CN.yaml":            "app:\n  name: yd-zh\npage:\n  title: 首页\n",
	}
}

// TestRenderFollowsRequestLanguage proves the whole chain on a real request: the i18n
// middleware negotiates the language, Render injects the translator into the data and the
// engine resolves {{ tr "..." }} through it. | TestRenderFollowsRequestLanguage 证明整条链路
// 在真实请求上成立：i18n 中间件协商语言，Render 把译者注入数据，引擎经由它解析 `{{ tr "..." }}`。
func TestRenderFollowsRequestLanguage(t *testing.T) {
	a := newTestApp(t, translatorTemplates())
	bundle, err := a.i18nBundle()
	if err != nil {
		t.Fatalf("加载语言目录失败: %v | loading the catalogs failed: %v", err, err)
	}
	a.Use(i18n.Middleware(bundle, i18n.Options{}))
	a.GET("/home", renderHandler(t, a, http.StatusOK, "pages/home.html", testData()))

	cases := []struct {
		target string // Request target. | 请求地址。
		title  string // Expected fragment title. | 期望的片段标题。
		app    string // Expected layout title. | 期望的布局标题。
	}{
		{"/home?lang=en", "Home", "yd-en"},
		{"/home?lang=zh-CN", "首页", "yd-zh"},
		{"/home", "首页", "yd-zh"}, // No hint at all resolves to the default language. | 没有任何线索时解析为默认语言。
	}
	for _, tc := range cases {
		body := doRequest(t, a, http.MethodGet, tc.target, nil).Body.String()
		if !strings.Contains(body, "<h1>"+tc.title+"</h1>") {
			t.Errorf("GET %s: 片段未按请求语言渲染: %q | GET %s: the fragment is not translated: %q", tc.target, body, tc.target, body)
		}
		if !strings.Contains(body, "<title>"+tc.app+"</title>") {
			t.Errorf("GET %s: 布局未按请求语言渲染: %q | GET %s: the layout is not translated: %q", tc.target, body, tc.target, body)
		}
	}
}

// TestRenderWithoutI18nKeepsKeys proves the fallback end of the contract: an application
// that never installed the middleware renders the keys, so a missing catalog stays visible
// instead of breaking the page. | TestRenderWithoutI18nKeepsKeys 证明契约的另一端：
// 从未安装中间件的应用渲染键本身，缺失的语言目录因此可见，而不是让页面出错。
func TestRenderWithoutI18nKeepsKeys(t *testing.T) {
	a := newTestApp(t, translatorTemplates())
	a.GET("/home", renderHandler(t, a, http.StatusOK, "pages/home.html", testData()))

	body := doRequest(t, a, http.MethodGet, "/home", nil).Body.String()
	for _, want := range []string{"<h1>page.title</h1>", "<title>app.name</title>"} {
		if !strings.Contains(body, want) {
			t.Errorf("未启用 i18n 时必须回退为键 %s: %q | without i18n the rendered output must carry the key %s: %q", want, body, want, body)
		}
	}
}

// TestRenderWrapsNonMapDataForTranslator pins the data convention for non-map data: the
// original value stays reachable as .Data and the translator is injected next to it, exactly
// as the layout already receives it. | TestRenderWrapsNonMapDataForTranslator 固定非 map
// 数据的数据约定：原值仍可通过 .Data 访问，译者与它并列注入，与布局既有的接收方式一致。
func TestRenderWrapsNonMapDataForTranslator(t *testing.T) {
	files := translatorTemplates()
	files["templates/pages/profile.html"] = `<h1>{{ .Data.Name }}</h1><p>{{ tr "page.title" }}</p>`

	a := newTestApp(t, files)
	bundle, err := a.i18nBundle()
	if err != nil {
		t.Fatalf("加载语言目录失败: %v | loading the catalogs failed: %v", err, err)
	}
	a.Use(i18n.Middleware(bundle, i18n.Options{}))
	a.GET("/profile", renderHandler(t, a, http.StatusOK, "pages/profile.html", struct{ Name string }{Name: "Yun"}))

	body := doRequest(t, a, http.MethodGet, "/profile?lang=en", nil).Body.String()
	if !strings.Contains(body, "<h1>Yun</h1>") {
		t.Errorf("结构体数据必须以 .Data 可达: %q | struct data must stay reachable as .Data: %q", body, body)
	}
	if !strings.Contains(body, "<p>Home</p>") {
		t.Errorf("结构体数据下 {{ tr }} 仍须按请求语言渲染: %q | with struct data {{ tr }} must still follow the request language: %q", body, body)
	}
}
