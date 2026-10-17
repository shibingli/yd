package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shibingli/yd/http/router"
)

// benchSinkWriter discards the response body and keeps a reusable header map. | benchSinkWriter
// 丢弃响应正文并复用响应头映射。
type benchSinkWriter struct{ header http.Header }

func (w *benchSinkWriter) Header() http.Header         { return w.header }
func (w *benchSinkWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *benchSinkWriter) WriteHeader(int)             {}

// TestCompressBenchSanity proves the benchmark path really compresses on every request. | TestCompressBenchSanity
// 证明基准路径每次请求都真正执行了压缩。
func TestCompressBenchSanity(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{Algorithms: []Algorithm{Zstd}}))
	calls := 0
	r.GET("/page", func(c *router.Ctx) {
		calls++
		_, _ = c.Writer.Write(compressBenchBig)
	})
	req := httptest.NewRequest(http.MethodGet, "/page", nil)
	req.Header.Set("Accept-Encoding", "zstd")
	var total int
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		total += rec.Body.Len()
		t.Logf("request %d: encoding=%q len=%d", i, rec.Header().Get("Content-Encoding"), rec.Body.Len())
	}
	if calls != 10 {
		t.Fatalf("handler calls = %d, want 10 | 处理器调用次数不正确", calls)
	}
	if total == 0 || total >= 10*len(compressBenchBig) {
		t.Fatalf("written = %d, plain = %d | 响应没有被压缩", total, 10*len(compressBenchBig))
	}
	t.Logf("plain=%d compressed=%d", 10*len(compressBenchBig), total)
}

// The payloads are converted once so the benchmark measures the middleware, not the handler's
// per-request string-to-bytes copy. | 载荷只转换一次，使基准度量中间件而不是处理器每请求的
// 字符串到字节切片复制。
var (
	compressBenchBig   = []byte(bigBody)
	compressBenchSmall = []byte("below the threshold")
)

// BenchmarkCompressMiddleware measures the full middleware path for each encoding, plus the
// below-threshold pass-through. | BenchmarkCompressMiddleware 测量各编码的完整中间件路径，以及
// 低于阈值的直通路径。
func BenchmarkCompressMiddleware(b *testing.B) {
	for _, algo := range []Algorithm{Gzip, Brotli, Zstd} {
		algo := algo
		b.Run(algo.Name(), func(b *testing.B) {
			r := router.New()
			r.Use(Compress(CompressOptions{Algorithms: []Algorithm{algo}}))
			r.GET("/page", func(c *router.Ctx) {
				c.SetHeader("Content-Type", "text/plain; charset=utf-8")
				_, _ = c.Writer.Write(compressBenchBig)
			})
			req := httptest.NewRequest(http.MethodGet, "/page", nil)
			req.Header.Set("Accept-Encoding", algo.Encoding())
			// Warm the encoder pool and prove the benchmark really exercises compression. | 预热
			// 编码器池，并证明基准确实走的是压缩路径。
			warm := httptest.NewRecorder()
			r.ServeHTTP(warm, req)
			if got := warm.Header().Get("Content-Encoding"); got != algo.Encoding() {
				b.Fatalf("Content-Encoding = %q, want %q | 压缩未被触发", got, algo.Encoding())
			}
			if warm.Body.Len() == 0 || warm.Body.Len() >= len(compressBenchBig) {
				b.Fatalf("compressed body = %d, plain = %d | 响应未被压缩", warm.Body.Len(), len(compressBenchBig))
			}
			b.SetBytes(int64(len(compressBenchBig)))
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				w := &benchSinkWriter{header: make(http.Header)}
				for pb.Next() {
					// A real server hands each request a fresh header map; clearing the reused
					// map keeps an earlier Content-Encoding from suppressing later compression. | 真实
					// 服务器每请求都有新的响应头映射；清空复用的映射，避免上一次的 Content-Encoding
					// 抑制后续压缩。
					clear(w.header)
					r.ServeHTTP(w, req)
				}
			})
		})
	}

	b.Run("passthrough", func(b *testing.B) {
		r := router.New()
		r.Use(Compress(CompressOptions{}))
		r.GET("/small", func(c *router.Ctx) {
			_, _ = c.Writer.Write(compressBenchSmall)
		})
		req := httptest.NewRequest(http.MethodGet, "/small", nil)
		req.Header.Set("Accept-Encoding", "gzip, br, zstd")
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			w := &benchSinkWriter{header: make(http.Header)}
			for pb.Next() {
				r.ServeHTTP(w, req)
			}
		})
	})
}
