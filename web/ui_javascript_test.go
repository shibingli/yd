// ui_javascript_test.go — the browser side of the UI kit.
// ui_javascript_test.go — UI 组件层的浏览器端。
//
// Two promises are pinned here. First, the shipped asset stays compatible with the generated
// projects' content security policy: `script-src 'self'` blocks `new Function` and `eval`, so a
// single one of those would make every Alpine component fail at runtime while every Go test stays
// green. Second, the China-specific kernels are tested by the node suite, because a wrong ID-card
// checksum or a wrong RMB capitalisation is a silent, expensive defect that a browser test would
// only find by accident.
// 这里钉住两个承诺。第一，随包发布的资产始终兼容生成项目的内容安全策略：`script-src 'self'`
// 会拦截 `new Function` 与 `eval`，只要出现一处，所有 Alpine 组件都会在运行时失败，而 Go 测试
// 仍然全绿。第二，中国常用控件的内核由 node 套件测试，因为身份证校验位或人民币大写算错是安静而
// 昂贵的缺陷，浏览器测试只能碰巧发现。
package web

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

const (
	// uiJSTestScriptName is the Node test entry point relative to the repository root. | uiJSTestScriptName 是相对仓库根目录的 Node 测试入口。
	uiJSTestScriptName = "web/static/js/yd-ui.test.mjs"
	// uiJSAssetName is the shipped script, relative to the embedded tree root. | uiJSAssetName 是随包发布的脚本，相对于内嵌树根目录。
	uiJSAssetName = "static/js/yd-ui.js"
	// uiJSAssetMinName is the minified sibling production serves. | uiJSAssetMinName 是 production 提供的压缩同族文件。
	uiJSAssetMinName = "static/js/yd-ui.min.js"
)

// TestUIJSSuite runs the node:test suite over the very file the browser receives. | TestUIJSSuite 对浏览器收到的那个文件运行 node:test 套件。
func TestUIJSSuite(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("skipping: node is not available on PATH (%v); install Node.js to run the javascript UI-kit suite | 跳过：PATH 中没有 node（%v），请安装 Node.js 以运行 JS UI 组件套件", err, err)
	}

	script := ""
	for _, candidate := range []string{uiJSTestScriptName, "static/js/yd-ui.test.mjs"} {
		if _, err := os.Stat(candidate); err == nil {
			script = candidate
			break
		}
	}
	if script == "" {
		t.Fatalf("the javascript UI-kit test script is missing: %s | 缺少 JS UI 组件测试脚本：%s", uiJSTestScriptName, uiJSTestScriptName)
	}

	out, err := exec.Command("node", script).CombinedOutput()
	if err != nil {
		t.Fatalf("the javascript UI-kit suite failed: node %s: %v\n%s | JS UI 组件套件失败：node %s: %v\n%s", script, err, out, script, err, out)
	}
	t.Logf("node %s output | 输出:\n%s", script, out)
}

// TestEditorInstanceStateStaysOffAlpineState pins the history-cache contract: hx-alpine-compat
// serialises every [x-data] scope to JSON before the shell is cached, so an EasyMDE instance on the
// reactive state (its options point back at the wrapper) aborts the swap with "Converting circular
// structure to JSON". The editor therefore lives in a module-level WeakMap, never on the component.
// TestEditorInstanceStateStaysOffAlpineState 钉住历史缓存契约：hx-alpine-compat 在缓存外壳前会把
// 每个 [x-data] 作用域序列化为 JSON，若把 EasyMDE 实例放进响应式状态（其 options 指回包装元素），
// 换入会以 "Converting circular structure to JSON" 中断。因此编辑器保存在模块级 WeakMap，绝不放进组件。
func TestEditorInstanceStateStaysOffAlpineState(t *testing.T) {
	raw, err := afero.ReadFile(FS(), uiJSAssetName)
	if err != nil {
		t.Fatalf("reading embedded %s failed: %v | 读取内嵌的 %s 失败：%v", uiJSAssetName, err, uiJSAssetName, err)
	}
	source := string(raw)
	if !strings.Contains(source, "var editorInstances = new WeakMap();") {
		t.Fatalf("%s must keep editor instances in a WeakMap | %s 必须把编辑器实例保存在 WeakMap 中", uiJSAssetName, uiJSAssetName)
	}
	start := strings.Index(source, "components.ydEditor = function")
	if start < 0 {
		t.Fatalf("%s no longer defines components.ydEditor | %s 未定义 components.ydEditor", uiJSAssetName, uiJSAssetName)
	}
	rest := source[start:]
	end := strings.Index(rest, "function register(Alpine)")
	if end < 0 {
		t.Fatalf("cannot delimit the ydEditor component in %s | 无法在 %s 中界定 ydEditor 组件", uiJSAssetName, uiJSAssetName)
	}
	block := rest[:end]
	for _, forbidden := range []string{"editor: null", "this.editor"} {
		if strings.Contains(block, forbidden) {
			t.Errorf("ydEditor stores %q on the Alpine state; use rememberEditor/editorFor so history caching stays JSON-safe | ydEditor 把 %q 放进了 Alpine 状态；请使用 rememberEditor/editorFor，保证历史缓存可序列化", forbidden, forbidden)
		}
	}

	// Production serves the minified sibling, so the same promise must hold there. Property
	// names survive this minifier, which is what makes the two checks meaningful.
	// production 提供压缩后的同族文件，因此同一承诺必须在压缩产物里也成立；该压缩器保留属性名，
	// 这两项检查因此有效。
	minified, err := afero.ReadFile(FS(), uiJSAssetMinName)
	if err != nil {
		t.Fatalf("reading embedded %s failed: %v | 读取内嵌的 %s 失败：%v", uiJSAssetMinName, err, uiJSAssetMinName, err)
	}
	if !strings.Contains(string(minified), "WeakMap") {
		t.Errorf("%s lost the WeakMap editor registry | %s 丢失了 WeakMap 编辑器注册表", uiJSAssetMinName, uiJSAssetMinName)
	}
	if strings.Contains(string(minified), "this.editor") {
		t.Errorf("%s stores the editor on Alpine state | %s 把编辑器放进了 Alpine 状态", uiJSAssetMinName, uiJSAssetMinName)
	}
}

// TestEditorRetriesWhenPageAssetsArrive pins the htmx-navigation retry. A partial navigation swaps
// the workspace but never rewrites <head>, and its page scripts arrive after window.load has already
// fired; without this signal the editor stays a bare textarea until a full reload. The event name is
// a contract with the generated project's yd-app.js. |
// TestEditorRetriesWhenPageAssetsArrive 固定 htmx 导航下的重试。局部导航只换工作区、不重写
// <head>，页面脚本在 window.load 早已触发后才到达；没有这个信号，编辑器会一直停在 textarea
// 直到整页刷新。事件名是与生成项目 yd-app.js 的契约。
func TestEditorRetriesWhenPageAssetsArrive(t *testing.T) {
	raw, err := afero.ReadFile(FS(), uiJSAssetName)
	if err != nil {
		t.Fatalf("reading embedded %s failed: %v | 读取内嵌的 %s 失败：%v", uiJSAssetName, err, uiJSAssetName, err)
	}
	source := string(raw)
	start := strings.Index(source, "components.ydEditor = function")
	if start < 0 {
		t.Fatalf("%s no longer defines components.ydEditor | %s 未定义 components.ydEditor", uiJSAssetName, uiJSAssetName)
	}
	rest := source[start:]
	end := strings.Index(rest, "function register(Alpine)")
	if end < 0 {
		t.Fatalf("cannot delimit the ydEditor component in %s | 无法在 %s 中界定 ydEditor 组件", uiJSAssetName, uiJSAssetName)
	}
	block := rest[:end]
	for _, want := range []string{
		"'yd:page-assets:loaded'",
		"_onPageAssets",
		"readyState",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("ydEditor is missing %q; after an htmx navigation the editor never initialises | ydEditor 缺少 %q；htmx 导航后编辑器不会初始化", want, want)
		}
	}

	// Production serves the minified sibling; the event name is a string literal and survives minification. |
	// production 提供压缩后的同族文件；事件名是字符串字面量，压缩后仍然保留。
	minified, err := afero.ReadFile(FS(), uiJSAssetMinName)
	if err != nil {
		t.Fatalf("reading embedded %s failed: %v | 读取内嵌的 %s 失败：%v", uiJSAssetMinName, err, uiJSAssetMinName, err)
	}
	if !strings.Contains(string(minified), "yd:page-assets:loaded") {
		t.Errorf("%s lost the page-assets retry listener | %s 丢失了页面资产重试监听", uiJSAssetMinName, uiJSAssetMinName)
	}
}

// TestUIJSAssetStaysCSPCompatible proves the shipped file can run under `script-src 'self'` and
// still publishes the surface the templates call.
// TestUIJSAssetStaysCSPCompatible 证明随包文件能在 `script-src 'self'` 下运行，并且仍然发布模板
// 所调用的接口。
func TestUIJSAssetStaysCSPCompatible(t *testing.T) {
	raw, err := afero.ReadFile(FS(), uiJSAssetName)
	if err != nil {
		t.Fatalf("reading embedded %s failed: %v | 读取内嵌的 %s 失败：%v", uiJSAssetName, err, uiJSAssetName, err)
	}
	source := string(raw)

	// The CSP build of Alpine is the only reason this file may call Alpine at all; anything that
	// compiles source at runtime would be blocked before our code runs.
	// 本文件之所以能调用 Alpine，全靠 Alpine 的 CSP 构建；任何在运行期编译源码的写法都会在我们的
	// 代码执行之前被拦截。
	for _, forbidden := range []string{"new Function", "eval(", "setTimeout('", "setInterval('"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("%s contains %q, which script-src 'self' blocks | %s 含有 %q，会被 script-src 'self' 拦截", uiJSAssetName, forbidden, uiJSAssetName, forbidden)
		}
	}
	for _, ref := range []string{"http://", "https://", "//cdn.", "unpkg.com", "cdnjs.cloudflare.com"} {
		if strings.Contains(source, ref) {
			t.Errorf("%s references %q; the asset must stay local | %s 引用了 %q：该资产必须保持本地", uiJSAssetName, ref, uiJSAssetName, ref)
		}
	}

	// The public surface is the documented surface. | 文档写定的接口就是实际发布的接口。
	for _, member := range []string{
		"root.ydUI = api", "root.ydCN = api.CN",
		"ydPhone", "ydIDCard", "ydBankCard", "ydCreditCode", "ydAmount", "ydRegion",
		"ydDateRange", "ydSmsCode", "ydPassword", "ydDropdown", "ydTabs", "ydSearch",
	} {
		if !strings.Contains(source, member) {
			t.Errorf("%s does not publish %q | %s 没有发布 %q", uiJSAssetName, member, uiJSAssetName, member)
		}
	}
}
