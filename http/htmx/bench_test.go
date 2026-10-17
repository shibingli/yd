package htmx

import (
	"net/http"
	"strings"
	"testing"
)

// benchResponseWriter is a minimal ResponseWriter whose header map is preallocated, so the
// benchmarks measure header assembly rather than map growth. | benchResponseWriter 是最小的
// ResponseWriter，头部映射已预分配，使基准测量头部组装而不是映射扩容。
type benchResponseWriter struct {
	header http.Header
}

func newBenchResponseWriter() *benchResponseWriter {
	return &benchResponseWriter{header: make(http.Header, 4)}
}

// Header implements http.ResponseWriter. | Header 实现 http.ResponseWriter。
func (w *benchResponseWriter) Header() http.Header { return w.header }

// Write implements http.ResponseWriter. | Write 实现 http.ResponseWriter。
func (w *benchResponseWriter) Write(p []byte) (int, error) { return len(p), nil }

// WriteHeader implements http.ResponseWriter. | WriteHeader 实现 http.ResponseWriter。
func (w *benchResponseWriter) WriteHeader(int) {}

func BenchmarkTriggerSingle(b *testing.B) {
	w := newBenchResponseWriter()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Trigger(w, Event{Name: "refresh"})
	}
}

func BenchmarkTriggerMulti(b *testing.B) {
	w := newBenchResponseWriter()
	events := []Event{
		{Name: "users:changed", Detail: map[string]int{"id": 1}},
		{Name: "stats:refresh"},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Trigger(w, events...)
	}
}

func BenchmarkRenderPartials(b *testing.B) {
	parts := make([]Partial, 8)
	for i := range parts {
		parts[i] = Partial{
			Target:  "#region-" + strings.Repeat("x", i),
			Swap:    "innerMorph",
			Content: `<div class="card">payload</div>`,
		}
	}
	w := newBenchResponseWriter()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := RenderPartials(w, parts...); err != nil {
			b.Fatalf("RenderPartials failed | RenderPartials 失败: %v", err)
		}
	}
}

// benchSink keeps the fragment observable so the compiler cannot elide it. | benchSink 让片段保持可观测，避免编译器消除。
var benchSink string

func BenchmarkErrorFragment(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSink = string(ErrorFragment(`<invalid> "input" & more`))
	}
}
