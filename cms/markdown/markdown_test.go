package markdown_test

import (
	"strings"
	"testing"

	"github.com/shibingli/yd/cms/markdown"
)

func TestRenderSanitisesRawHTMLAndDangerousLinks(t *testing.T) {
	renderer := markdown.New()
	html, text, err := renderer.Render("<script>alert(1)</script>\n\n[bad](javascript:alert(1))\n\n**safe**")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(html, "<script") {
		t.Fatalf("raw script survived: %q", html)
	}
	if strings.Contains(html, "javascript:") {
		t.Fatalf("dangerous link survived: %q", html)
	}
	if !strings.Contains(html, "<strong>safe</strong>") {
		t.Fatalf("markdown structure missing: %q", html)
	}
	if strings.Contains(text, "<script") {
		t.Fatalf("plain text kept raw HTML: %q", text)
	}
}

func TestRenderGFMTableAndHeadingIDs(t *testing.T) {
	renderer := markdown.New()
	html, _, err := renderer.Render("## Title\n\n| a | b |\n|---|---|\n| 1 | 2 |")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(html, `<h2 id="title">`) {
		t.Fatalf("heading id missing: %q", html)
	}
	if !strings.Contains(html, "<table>") {
		t.Fatalf("GFM table missing: %q", html)
	}
}
