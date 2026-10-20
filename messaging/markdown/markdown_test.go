package markdown_test

import (
	"strings"
	"testing"

	"github.com/shibingli/yd/messaging/markdown"
)

// TestRenderSanitisesRawHTMLAndDangerousLinks proves the renderer never emits executable markup. |
// TestRenderSanitisesRawHTMLAndDangerousLinks 证明渲染器绝不输出可执行标记。
func TestRenderSanitisesRawHTMLAndDangerousLinks(t *testing.T) {
	renderer := markdown.New()
	html, text, err := renderer.Render("# Title\n\n<script>alert('x')</script>\n\n[bad](javascript:alert(1))\n\n**bold**")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	lower := strings.ToLower(html)
	if strings.Contains(lower, "<script") {
		t.Fatalf("raw HTML survived: %s | 原始 HTML 未移除: %s", html, html)
	}
	if strings.Contains(lower, "javascript:") {
		t.Fatalf("dangerous link survived: %s | 危险链接未移除: %s", html, html)
	}
	if !strings.Contains(html, "<h1>Title</h1>") || !strings.Contains(html, "<strong>bold</strong>") {
		t.Fatalf("Markdown structure missing: %s | Markdown 结构缺失: %s", html, html)
	}
	if !strings.Contains(text, "Title") || !strings.Contains(text, "bold") {
		t.Fatalf("plain text missing: %q | 纯文本缺失: %q", text, text)
	}
}

// TestRenderGFMTable covers the GitHub-flavoured extensions. | TestRenderGFMTable 覆盖 GitHub 风格扩展。
func TestRenderGFMTable(t *testing.T) {
	renderer := markdown.New()
	html, _, err := renderer.Render("| A | B |\n|---|---|\n| 1 | 2 |")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "<table>") || !strings.Contains(html, "<td>1</td>") {
		t.Fatalf("GFM table missing: %s | GFM 表格缺失: %s", html, html)
	}
}
