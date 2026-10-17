package openapi

import (
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
	for _, want := range []string{
		"renderOverview", "renderSecuritySchemes", "renderSecurityRequirement",
		"renderCallbacks", "renderLinks", "renderEncoding", "renderExamples",
		"renderSchema", "renderExtensions", "webhooks", "prefixItems", "allOf", "oneOf", "anyOf",
	} {
		if !strings.Contains(viewerTemplate, want) {
			t.Errorf("viewer template lacks %q | 查看器模板缺少 %q", want, want)
		}
	}
	lower := strings.ToLower(viewerTemplate)
	for _, bad := range []string{"<script src=", "<link ", "url(http", "@import", "javascript:"} {
		if strings.Contains(lower, bad) {
			t.Errorf("viewer template references external or unsafe content %q | 查看器模板引用了外部或不安全内容 %q", bad, bad)
		}
	}
}
