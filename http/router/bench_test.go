package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// benchRouterRequest builds a request once so the benchmark measures routing, not request
// construction. | benchRouterRequest 只构建一次请求，使基准度量的是路由而非请求构造。
func benchRouterRequest(target string) *http.Request {
	return httptest.NewRequest(http.MethodGet, target, nil)
}

func BenchmarkRouterStatic(b *testing.B) {
	r := New()
	r.GET("/api/v1/users", func(c *Ctx) { _ = c.NoContent() })
	req := benchRouterRequest("/api/v1/users")
	rec := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec.Body.Reset()
		r.ServeHTTP(rec, req)
	}
}

func BenchmarkRouterParam(b *testing.B) {
	r := New()
	r.GET("/api/v1/users/:id/profile", func(c *Ctx) { _ = c.Param("id"); _ = c.NoContent() })
	req := benchRouterRequest("/api/v1/users/42/profile")
	rec := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec.Body.Reset()
		r.ServeHTTP(rec, req)
	}
}

func BenchmarkRouterMixedSegment(b *testing.B) {
	r := New()
	r.GET("/report/:id-xx.png", func(c *Ctx) { _ = c.Param("id"); _ = c.NoContent() })
	req := benchRouterRequest("/report/2026-10-xx.png")
	rec := httptest.NewRecorder()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec.Body.Reset()
		r.ServeHTTP(rec, req)
	}
}

func BenchmarkDownloadBytes(b *testing.B) {
	content := make([]byte, 4096)
	r := New()
	r.GET("/file", func(c *Ctx) {
		_ = c.DownloadBytes(content, DownloadOptions{ContentType: "application/octet-stream"})
	})
	req := benchRouterRequest("/file")
	rec := httptest.NewRecorder()

	b.SetBytes(int64(len(content)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec.Body.Reset()
		r.ServeHTTP(rec, req)
	}
}

func BenchmarkDownloadReader(b *testing.B) {
	content := make([]byte, 4096)
	r := New()
	src := newBenchReader(content)
	r.GET("/file", func(c *Ctx) {
		src.off = 0
		_ = c.DownloadReader(src, int64(len(content)), DownloadOptions{
			ContentType: "application/octet-stream",
		})
	})
	req := benchRouterRequest("/file")
	rec := httptest.NewRecorder()

	b.SetBytes(int64(len(content)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec.Body.Reset()
		r.ServeHTTP(rec, req)
	}
}

// benchReader hides io.WriterTo (which bytes.Reader implements) to exercise the copy path. | benchReader 隐藏 io.WriterTo
// （bytes.Reader 实现了它），以覆盖拷贝路径。
type benchReader struct {
	data []byte
	off  int
}

func newBenchReader(data []byte) *benchReader { return &benchReader{data: data} }

func (r *benchReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}
