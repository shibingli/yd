/*
render_engine_test.go — integration guard between web/templates and the render package.

render_engine_test.go — web/templates 与 render 包之间的集成守卫。

The framework templates call {{ tr "key" }} for every user-facing string, so the render
package has to provide that helper and the engine has to resolve it with the translator of
the request. This file renders the shipped admin templates through the real render.Engine —
not through a local stub — and pins both promises:

  - the whole tree parses and executes, and the chrome pages carry the workspace shell;
  - the {{ tr "..." }} positions print the injected translator, and fall back to the key
    when none is injected.

框架模板的每条面向用户的文案都调用 {{ tr "key" }}，因此 render 包必须提供该辅助函数，
且引擎必须用请求的译者解析它。本文件用真实的 render.Engine（而非本地替身）渲染自带的
管理后台模板，并固定两点契约：

  - 整棵目录树可解析、可执行，带外壳页面都含中央工作区；
  - {{ tr "..." }} 位置输出注入译者的文案，未注入时回退为键本身。
*/
package web

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/shibingli/ydx/render"
)

// engineWorkspaceID is the shell marker every chrome page must carry, and the one the
// dashboard layout defines. | engineWorkspaceID 是每个带外壳页面都必须带的标记，由管理后台布局定义。
const engineWorkspaceID = `id="yd-workspace"`

// engineTranslator is the injected render.Translator of this file: catalog hits are
// translated, every other key is wrapped in brackets so a fallback stays distinguishable
// from a real message. | engineTranslator 是本文件注入的 render.Translator：命中目录的键被翻译，
// 其余键用方括号包裹，使回退结果与真实文案可区分。
type engineTranslator struct {
	catalog map[string]string // key -> message. | 键 -> 消息。
}

// T renders the catalog message of key, or "[key]" when the catalog has none.
// | T 渲染 key 的目录消息，目录中没有时返回 "[key]"。
func (e engineTranslator) T(key string, args ...any) string {
	if msg, ok := e.catalog[key]; ok {
		return msg
	}
	return "[" + key + "]"
}

// engineCatalog is the message set this file pins; the values are unique markers so they
// can never be confused with the keys the templates carry. | engineCatalog 是本文件固定的消息集合；
// 取值都是唯一标记，绝不会与模板携带的键混淆。
var engineCatalog = map[string]string{
	"app.name":             "TR-APP-NAME",
	"dashboard.title":      "TR-DASHBOARD-TITLE",
	"dashboard.subtitle":   "TR-DASHBOARD-SUBTITLE",
	"users.title":          "TR-USERS-TITLE",
	"a11y.skip_to_content": "TR-SKIP-TO-CONTENT",
}

// renderThroughEngine renders name through engine and fails the test on error.
// | renderThroughEngine 通过引擎渲染 name，出错即让测试失败。
func renderThroughEngine(t *testing.T, engine *render.Engine, name string, data any) string {
	t.Helper()

	var buf bytes.Buffer
	if err := engine.Render(&buf, name, data); err != nil {
		t.Fatalf("Render(%q) 失败: %v | Render(%q) failed: %v", name, err, name, err)
	}
	return buf.String()
}

// TestFrameworkTemplatesRenderThroughEngine is the guard of the tr integration: it builds a
// render.Engine over web.FS() and renders the shipped admin templates, which fails at parse
// time as long as no `tr` function exists. | TestFrameworkTemplatesRenderThroughEngine 是 tr
// 集成的守卫：它在 web.FS() 之上构建 render.Engine 并渲染自带的管理后台模板；
// 只要缺少 `tr` 函数，这一步就会在解析期失败。
func TestFrameworkTemplatesRenderThroughEngine(t *testing.T) {
	engine, err := render.New(FS(), render.Options{})
	if err != nil {
		t.Fatalf("render.New(web.FS()) 失败，框架模板无法被自带渲染器加载: %v | render.New(web.FS()) failed, the framework templates do not load: %v", err, err)
	}

	// The layout file is part of the loaded set; it only holds {{define}} blocks, so
	// executing it prints nothing and not failing is the whole promise here. | 布局文件属于已加载的集合；
	// 它只含 {{define}} 块，执行它不输出内容，此处要的正是「不报错」。
	if !engine.Exists("layouts/dashboard.html") {
		t.Fatal("layouts/dashboard.html 未进入模板集合 | layouts/dashboard.html is missing from the template set")
	}
	if err := engine.Render(io.Discard, "layouts/dashboard.html", nil); err != nil {
		t.Fatalf("渲染 layouts/dashboard.html 失败: %v | rendering layouts/dashboard.html failed: %v", err, err)
	}

	data := templateData()
	data[render.TranslatorKey] = engineTranslator{catalog: engineCatalog}

	// "dashboard" composes the layout's open/close defines around pages/dashboard.content,
	// so the chrome of layouts/dashboard.html is part of these outputs. | "dashboard" 用布局的
	// open/close define 包裹 pages/dashboard.content，因此这些输出都含有 layouts/dashboard.html 的外壳。
	pages := []string{"dashboard", "pages/dashboard.html", "pages/users.html", "pages/settings.html"}
	for _, name := range pages {
		html := renderThroughEngine(t, engine, name, data)
		if !strings.Contains(html, engineWorkspaceID) {
			t.Errorf("%s: 缺少中央工作区 %s | %s misses the workspace shell %s", name, engineWorkspaceID, name, engineWorkspaceID)
			continue
		}
		if got := strings.Count(html, engineWorkspaceID); got != 1 {
			t.Errorf("%s: 工作区出现 %d 次, 期望 1 次 | %s renders the workspace %d times, want 1", name, got, name, got)
		}
	}

	// The {{ tr "..." }} positions must print the injected translator: one from the page
	// fragment and one from the dashboard layout. | {{ tr "..." }} 位置必须输出注入译者的文案：
	// 一条来自页面片段，一条来自管理后台布局。
	dashboard := renderThroughEngine(t, engine, "pages/dashboard.html", data)
	for _, want := range []string{
		`data-yd-i18n="dashboard.title">TR-DASHBOARD-TITLE</h1>`,
		`data-yd-i18n="dashboard.subtitle">TR-DASHBOARD-SUBTITLE</p>`,
		`data-yd-i18n="a11y.skip_to_content">TR-SKIP-TO-CONTENT</a>`,
	} {
		if !strings.Contains(dashboard, want) {
			t.Errorf("pages/dashboard.html: 缺少注入译者的输出 %s | pages/dashboard.html misses the injected translation %s", want, want)
		}
	}
	if !strings.Contains(dashboard, "<title>Dashboard · TR-APP-NAME</title>") {
		t.Errorf("pages/dashboard.html: 布局标题未按译者渲染 | pages/dashboard.html does not translate the layout title")
	}

	// Without the injected translator the very same positions print the key, which is what
	// proves the translations above came from the injection. | 未注入译者时同一位置打印键本身，
	// 这正说明上面的翻译来自注入。
	plain := renderThroughEngine(t, engine, "pages/dashboard.html", templateData())
	if !strings.Contains(plain, `data-yd-i18n="dashboard.title">dashboard.title</h1>`) {
		t.Error("未注入译者时 {{ tr }} 必须回退为键本身 | without a translator {{ tr }} must fall back to the key")
	}
}

// TestFrameworkTemplatesUseTheCompiledEngine guards the fast executor: every framework template
// loaded from web.FS() must run through the compiled program, never the html/template fallback. |
// TestFrameworkTemplatesUseTheCompiledEngine 守卫快速执行器：从 web.FS() 加载的每个框架模板都必须
// 走编译后的执行器，绝不能回退到 html/template。
func TestFrameworkTemplatesUseTheCompiledEngine(t *testing.T) {
	engine, err := render.New(FS(), render.Options{})
	if err != nil {
		t.Fatalf("render.New(web.FS()) 失败: %v | render.New(web.FS()) failed: %v", err, err)
	}
	names := engine.Names()
	if len(names) == 0 {
		t.Fatal("模板集合为空，无法验证执行器 | the template set is empty")
	}
	for _, name := range names {
		if engine.UsesFallback(name) {
			t.Errorf("%s 回退到了 html/template 执行器 | %s fell back to the html/template executor", name, name)
		}
	}
}
