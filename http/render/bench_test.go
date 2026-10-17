package render

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
)

// benchTranslator is a translator with a stable cache key. | benchTranslator 是带稳定缓存键的译者。
type benchTranslator struct{ lang string }

// T renders lang:key. | T 渲染 lang:key。
func (t benchTranslator) T(key string, _ ...any) string { return t.lang + ":" + key }

// CacheKey identifies the translator. | CacheKey 标识译者。
func (t benchTranslator) CacheKey() string { return t.lang }

// benchAuthz is an authority with a stable decision key. | benchAuthz 是带稳定判定键的判定主体。
type benchAuthz struct {
	key   string
	allow bool
}

// Can reports the fixed answer. | Can 返回固定答案。
func (a benchAuthz) Can(string) bool { return a.allow }

// HasRole reports the fixed answer. | HasRole 返回固定答案。
func (a benchAuthz) HasRole(string) bool { return a.allow }

// Perm returns the reference unchanged. | Perm 原样返回引用。
func (a benchAuthz) Perm(ref string) string { return ref }

// Role returns the reference unchanged. | Role 原样返回引用。
func (a benchAuthz) Role(ref string) string { return ref }

// DecisionKey identifies the decision set. | DecisionKey 标识判定集合。
func (a benchAuthz) DecisionKey() string { return a.key }

// newBenchEngine builds a small template set that exercises both bindings. | newBenchEngine 构建同时使用两种绑定的小模板集。
func newBenchEngine(tb testing.TB) *Engine {
	tb.Helper()
	fsys := afero.NewMemMapFs()
	files := map[string]string{
		"templates/layouts/base.html": `<!doctype html><html><body>{{template "content" .}}</body></html>`,
		"templates/pages/home.html":   `<h1>{{tr "home.title"}}</h1>{{if can "user:create"}}<b>yes</b>{{else}}<i>no</i>{{end}}{{range .Items}}<li>{{tr "item.name" .}}</li>{{end}}`,
	}
	for name, body := range files {
		_ = fsys.MkdirAll(filepath.Dir(name), 0o755)
		if err := afero.WriteFile(fsys, name, []byte(body), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	e, err := New(fsys, Options{Root: "templates"})
	if err != nil {
		tb.Fatal(err)
	}
	return e
}

func BenchmarkRender(b *testing.B) {
	e := newBenchEngine(b)
	items := []string{"a", "b", "c", "d"}
	withTr := map[string]any{TranslatorKey: benchTranslator{lang: "en"}, "Items": items}
	withBoth := map[string]any{
		TranslatorKey: benchTranslator{lang: "en"},
		AuthzKey:      benchAuthz{key: "admin", allow: true},
		"Items":       items,
	}
	// Warm the bound-set cache so the benchmark measures steady-state renders. | 预热绑定集缓存，使基准测量稳态渲染。
	_ = e.Render(io.Discard, "pages/home.html", withBoth)

	b.Run("with_translator", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := e.Render(io.Discard, "pages/home.html", withTr); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("without_bindings", func(b *testing.B) {
		b.ReportAllocs()
		plain := map[string]any{"Items": items}
		for i := 0; i < b.N; i++ {
			if err := e.Render(io.Discard, "pages/home.html", plain); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("with_translator_and_authz", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := e.Render(io.Discard, "pages/home.html", withBoth); err != nil {
				b.Fatal(err)
			}
		}
	})
}
