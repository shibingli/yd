package render_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/http/render"
)

// evilTranslator returns a quote-bearing value for every key, which is what a catalog entry
// interpolating request data can legitimately contain. | evilTranslator 对每个键返回带引号的值，
// 这正是插入了请求数据的目录条目可能合法包含的内容。
type evilTranslator struct{}

func (evilTranslator) T(string, ...any) string { return `" onmouseover=alert(1) x="` }

// denyAuthz denies every capability. | denyAuthz 拒绝所有能力。
type denyAuthz struct{}

func (denyAuthz) Can(string) bool        { return false }
func (denyAuthz) HasRole(string) bool    { return false }
func (denyAuthz) Perm(ref string) string { return ref }
func (denyAuthz) Role(ref string) string { return ref }

// TestQuickActionAppliesWholeEscaperChain is the regression for a request-reachable XSS: the
// quick path used to apply only the first escaper of the contextual chain. | TestQuickActionAppliesWholeEscaperChain
// 是请求可达 XSS 的回归：快速路径过去只应用上下文转义链的第一级。
func TestQuickActionAppliesWholeEscaperChain(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "templates/t.html", []byte(`<div onclick="{{ tr "k" }}">x</div>`), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := render.New(fsys, render.Options{Root: "templates"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var out bytes.Buffer
	data := map[string]any{render.TranslatorKey: evilTranslator{}}
	if err := engine.Render(&out, "t.html", data); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := out.String()
	// The injected text may remain inside the attribute's value; what must not remain is a literal
	// quote that terminates it. The only literal quotes are the writer's own pair around onclick.
	// | 注入文本可以留在属性值内部；绝不能被留下的是终止属性的字面引号。字面引号只应剩
	// onclick 自身那一对。
	if n := strings.Count(got, `"`); n != 2 {
		t.Fatalf("a literal quote escaped into the markup (%d quotes): %s", n, got)
	}
	if !strings.Contains(got, "&#34;") {
		t.Fatalf("the quote was not escaped: %s", got)
	}
}

// TestQuickActionPreservesBooleanType is the regression for the JS-value bug: a bool converted to
// "false" becomes a truthy string in JavaScript. | TestQuickActionPreservesBooleanType 是 JS 值缺陷的
// 回归：布尔值转成 "false" 后在 JavaScript 里变成真值字符串。
func TestQuickActionPreservesBooleanType(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "templates/t.html", []byte(`<button onclick="if ({{ can "user:delete" }}) del()">x</button>`), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := render.New(fsys, render.Options{Root: "templates"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var out bytes.Buffer
	data := map[string]any{render.AuthzKey: denyAuthz{}}
	if err := engine.Render(&out, "t.html", data); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := out.String()
	if strings.Contains(got, `"false"`) {
		t.Fatalf("bool was rendered as a quoted, always-truthy string: %s", got)
	}
	if !strings.Contains(got, "false") {
		t.Fatalf("bool value missing: %s", got)
	}
}

// TestBuiltinIndexOutOfRangeIsAnError keeps template-controlled reflect panics from escaping. | TestBuiltinIndexOutOfRangeIsAnError
// 阻止模板可控的 reflect panic 逃逸。
func TestBuiltinIndexOutOfRangeIsAnError(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "templates/t.html", []byte(`{{ index .S (len .S) }}`), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := render.New(fsys, render.Options{Root: "templates"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var out bytes.Buffer
	err = engine.Render(&out, "t.html", map[string]any{"S": []int{1, 2}})
	if err == nil {
		t.Fatal("index out of range did not return an error")
	}
	if !strings.Contains(err.Error(), "index out of range") {
		t.Fatalf("err = %v", err)
	}
}
