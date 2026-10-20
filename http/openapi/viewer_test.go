package openapi

import (
	"os"
	"strings"
	"testing"

	"github.com/shibingli/yd/core/jsonx"
)

// TestViewerScriptIslandStaysValid proves that hostile strings survive the JSON island and
// cannot break out of the script element. | TestViewerScriptIslandStaysValid 证明恶意字符串能在
// JSON 数据岛中正确往返，且无法突破 script 元素。
func TestViewerScriptIslandStaysValid(t *testing.T) {
	const hostile = `</script><script>alert("x")</script> & <b>`
	doc := NewBuilder(Info{Title: "Demo", Version: "1.0.0", Description: hostile}).Build()
	item := doc.Paths["/"]
	item.Get = &Operation{
		Summary:     hostile,
		Description: hostile,
		Responses:   map[string]Response{"200": {Description: hostile}},
	}
	doc.Paths["/"] = item
	page, err := renderViewer(doc, Options{})
	if err != nil {
		t.Fatalf("renderViewer: %v", err)
	}
	body := string(page)
	if strings.Contains(body, hostile) {
		t.Fatalf("hostile string leaked verbatim into the page | 恶意字符串原样进入页面")
	}
	if strings.Contains(body, `</script><script>alert`) {
		t.Fatalf("script element can be closed from the data island | 数据岛可以提前关闭 script 元素")
	}

	const marker = `id="openapi-data">`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatal("missing data island | 缺少数据岛")
	}
	start += len(marker)
	end := strings.Index(body[start:], "</script>")
	if end < 0 {
		t.Fatal("unterminated data island | 数据岛未闭合")
	}
	island := body[start : start+end]
	if !jsonx.Valid([]byte(island)) {
		t.Fatalf("data island is not valid JSON | 数据岛不是合法 JSON")
	}
	var decoded Document
	if err := jsonx.Unmarshal([]byte(island), &decoded); err != nil {
		t.Fatalf("decode data island: %v", err)
	}
	if decoded.Info.Description != hostile {
		t.Fatalf("description = %q, want %q | 数据岛未能还原原始字符串", decoded.Info.Description, hostile)
	}
	if decoded.Paths["/"].Get.Summary != hostile {
		t.Fatalf("summary lost through the island | 摘要经过数据岛后丢失")
	}
}

// TestViewerCoversAllObjectKinds checks that the template carries renderers for the extended
// object kinds and no external asset references. | TestViewerCoversAllObjectKinds 检查模板包含
// 扩展对象类型的渲染器，且不引用外部资源。
func TestViewerCoversAllObjectKinds(t *testing.T) {
	raw, err := os.ReadFile("../../web/static/js/yd-openapi-viewer.js")
	if err != nil {
		t.Fatalf("read viewer script: %v | 读取查看器脚本失败：%v", err, err)
	}
	script := string(raw)
	for _, want := range []string{
		"renderOverview", "renderSecuritySchemes", "renderSecurityRequirement",
		"renderCallbacks", "renderLinks", "renderEncoding", "renderExamples",
		"renderSchema", "renderExtensions", "webhooks", "prefixItems", "allOf", "oneOf", "anyOf",
		"route-group", "data-try-send", "sendTry", "theme-toggle",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("viewer script lacks %q | 查看器脚本缺少 %q", want, want)
		}
	}
	// The page must load the script from the same origin and nothing else. | 页面只能从同源加载该脚本，不得引用其它资源。
	if !strings.Contains(viewerTemplate, `<script src="`+viewerScriptPlaceholder+`" defer></script>`) {
		t.Errorf("viewer template does not load the same-origin viewer script | 查看器模板未加载同源查看器脚本")
	}
	if !strings.Contains(viewerTemplate, `id="theme-toggle"`) {
		t.Errorf("viewer template lacks the theme toggle | 查看器模板缺少主题切换按钮")
	}
	for _, want := range []string{`class="try"`, `class="route-group"`} {
		if !strings.Contains(script, want) {
			t.Errorf("viewer script lacks %q | 查看器脚本缺少 %q", want, want)
		}
	}
	lower := strings.ToLower(viewerTemplate)
	for _, bad := range []string{"<link ", "url(http", "@import", "javascript:"} {
		if strings.Contains(lower, bad) {
			t.Errorf("viewer template references external or unsafe content %q | 查看器模板引用了外部或不安全内容 %q", bad, bad)
		}
	}
	lowerScript := strings.ToLower(script)
	for _, bad := range []string{"http://", "https://", "cdn."} {
		if strings.Contains(lowerScript, bad) {
			t.Errorf("viewer script references an external host %q | 查看器脚本引用了外部主机 %q", bad, bad)
		}
	}
}

// TestViewerThemeButtonUsesBundledIcons pins the viewer's stateful theme icon: the three glyphs
// come from the shared sprite, CSS selects the active one, and the script updates only the label
// so it never wipes the icon nodes. | TestViewerThemeButtonUsesBundledIcons 固定查看器的状态主题图标：
// 三枚图标来自共享 sprite，CSS 选出当前一枚，脚本只更新文字，绝不会清掉图标节点。
func TestViewerThemeButtonUsesBundledIcons(t *testing.T) {
	for _, want := range []string{
		`data-theme-glyph="light"`,
		`data-theme-glyph="dark"`,
		`data-theme-glyph="system"`,
		`#i-lucide-sun`,
		`#i-lucide-moon`,
		`#i-lucide-monitor`,
		`id="theme-label"`,
	} {
		if !strings.Contains(viewerTemplate, want) {
			t.Errorf("viewer template is missing %q | 查看器模板缺少 %q", want, want)
		}
	}
	for _, want := range []string{
		`.theme-toggle[data-theme-choice="light"]`,
		`.theme-toggle[data-theme-choice="dark"]`,
		`.theme-toggle[data-theme-choice="auto"]`,
	} {
		if !strings.Contains(viewerTemplate, want) {
			t.Errorf("viewer stylesheet is missing %q | 查看器样式缺少 %q", want, want)
		}
	}

	raw, err := os.ReadFile("../../web/static/js/yd-openapi-viewer.js")
	if err != nil {
		t.Fatalf("read viewer script: %v | 读取查看器脚本失败：%v", err, err)
	}
	script := string(raw)
	if strings.Contains(script, "button.textContent") {
		t.Error("the viewer script wipes the theme button with textContent, removing its icons | 查看器脚本用 textContent 覆盖主题按钮，会清掉图标")
	}
	if !strings.Contains(script, "theme-label") {
		t.Error("the viewer script does not update the theme label node | 查看器脚本没有更新主题文字节点")
	}
}

// TestEscapeJSONForScriptActuallyEscapes is the regression for a no-op sanitizer that relied on
// jsonx's HTML escaping; a raw "</script>" would otherwise terminate the JSON island. | TestEscapeJSONForScriptActuallyEscapes
// 是空操作清洗器的回归——它曾依赖 jsonx 的 HTML 转义；否则原始 "</script>" 会终止 JSON 岛。
func TestEscapeJSONForScriptActuallyEscapes(t *testing.T) {
	out := escapeJSONForScript([]byte("{\"x\":\"</script><script>alert(1)</script>&\"}"))
	if strings.Contains(out, "</script>") {
		t.Fatalf("raw script terminator survived: %s", out)
	}
	for _, want := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if !strings.Contains(out, want) {
			t.Errorf("escape %s missing: %s", want, out)
		}
	}
}
