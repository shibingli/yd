package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/http/router"
)

// The benchmarks in this file measure each middleware on the real request path: a router with the
// middleware installed through r.Use, a live handler and an httptest.ResponseRecorder. Comparing
// against BenchmarkBaselineNoMiddleware isolates the middleware cost from the router's own routing,
// pooling and recorder overhead. Every benchmark shares the same small body unless it explicitly
// exercises the compression path, so the deltas are directly comparable.
// | 本文件的基准在真实请求路径上测量每个中间件：路由器经 r.Use 挂载中间件、真实处理器与
// httptest.ResponseRecorder。与 BenchmarkBaselineNoMiddleware 对比可把中间件成本从路由器自身的
// 路由、对象池与记录器开销中分离出来。除非显式测试压缩路径，所有基准共享同一个小响应体，
// 因此增量可以直接横向比较。

// benchPayloadSize is the exact size of the large compression fixtures (16 KiB).
// | benchPayloadSize 是压缩大样本的精确大小（16 KiB）。
const benchPayloadSize = 16 << 10

// benchSmallBody is the small response shared by the baseline and the lightweight middleware cases;
// it stays below the default compression MinSize so a compression wrapper would skip it.
// | benchSmallBody 是基线与轻量中间件用例共享的小响应；
// 它低于默认压缩阈值 MinSize，因此压缩包装器会跳过它。
const benchSmallBody = "ok: small response body"

// benchHTMLPayload is an exactly 16 KiB, highly compressible text/html fixture.
// | benchHTMLPayload 是恰好 16 KiB、可压缩性很强的 text/html 样本。
var benchHTMLPayload = benchRepeatExact("<p>yd framework compresses well</p>", benchPayloadSize)

// benchJSONPayload is an exactly 16 KiB application/json fixture made of repeated user objects.
// | benchJSONPayload 是由重复用户对象构成、恰好 16 KiB 的 application/json 样本。
var benchJSONPayload = benchJSONPayloadOfSize(benchPayloadSize)

// benchRepeatExact repeats unit and truncates to exactly size bytes.
// | benchRepeatExact 重复 unit 并截断到恰好 size 字节。
func benchRepeatExact(unit string, size int) string {
	var b strings.Builder
	b.Grow(size + len(unit))
	for b.Len() < size {
		b.WriteString(unit)
	}
	return b.String()[:size]
}

// benchJSONPayloadOfSize builds a valid JSON array of user objects padded with spaces to exactly size bytes.
// | benchJSONPayloadOfSize 构造一个由用户对象组成的合法 JSON 数组，并用空格补齐到恰好 size 字节。
func benchJSONPayloadOfSize(size int) string {
	const unit = `{"id":12345,"name":"bench-user","active":true,"score":98.5}`
	var b strings.Builder
	b.Grow(size)
	b.WriteByte('[')
	first := true
	for {
		need := len(unit)
		if !first {
			need++
		}
		if b.Len()+need+1 > size {
			break
		}
		if !first {
			b.WriteByte(',')
		}
		b.WriteString(unit)
		first = false
	}
	b.WriteByte(']')
	for b.Len() < size {
		b.WriteByte(' ')
	}
	return b.String()
}

// benchDiscardLogger returns an Info-level JSON logger writing to io.Discard, so logging middleware
// pays encoding cost without any real I/O polluting the measurement.
// | benchDiscardLogger 返回写入 io.Discard 的 Info 级 JSON 日志器，
// 使日志类中间件只付编码成本，不让真实 I/O 污染测量。
func benchDiscardLogger() *logx.Logger {
	return logx.New(logx.Options{Writer: io.Discard, Level: logx.InfoLevel})
}

// benchRouter builds a router with one GET /bench route writing body as Content-Type contentType.
// An empty contentType leaves the default set by c.String.
// | benchRouter 构建只有一个 GET /bench 路由的路由器，以 contentType 写出 body；
// contentType 为空时沿用 c.String 的默认值。
func benchRouter(body, contentType string, mw ...router.Middleware) *router.Router {
	r := router.New()
	for _, m := range mw {
		r.Use(m)
	}
	r.GET("/bench", func(c *router.Ctx) {
		if contentType != "" {
			c.SetHeader("Content-Type", contentType)
		}
		c.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write([]byte(body))
	})
	return r
}

// benchRequest builds the GET /bench request used by every benchmark; headers are optional.
// | benchRequest 构建所有基准共用的 GET /bench 请求；headers 可选。
func benchRequest(headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/bench", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// benchRecorderSink keeps the last recorder reachable so the compiler cannot elide the request work.
// | benchRecorderSink 持有最近一次记录器，防止编译器把请求处理当作死代码消除。
var benchRecorderSink *httptest.ResponseRecorder

// benchServe runs the router with a fresh ResponseRecorder per iteration.
// A fresh recorder is required because httptest.ResponseRecorder caches its first WriteHeader in
// unexported state; reusing one would silently skip every later header write and measure a
// different code path. check, when non-nil, validates one run before the timer starts.
// | benchServe 每轮用新的 ResponseRecorder 执行一次路由器。
// 必须新建记录器：httptest.ResponseRecorder 会把首次 WriteHeader 缓存在未导出状态中，
// 复用会静默跳过之后所有的响应头写入，从而测到另一条代码路径。check 非 nil 时在计时开始前校验一次。
func benchServe(b *testing.B, r *router.Router, req *http.Request, check func(*testing.B, *httptest.ResponseRecorder)) {
	b.Helper()
	if check != nil {
		check(b, benchDo(r, req))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		benchRecorderSink = rec
	}
}

// benchDo executes one request and returns its recorder.
// | benchDo 执行一次请求并返回其记录器。
func benchDo(r *router.Router, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// benchExpectBody returns a validator asserting a 200 response carrying exactly want.
// | benchExpectBody 返回一个校验器，断言响应为 200 且正文恰好为 want。
func benchExpectBody(want string) func(*testing.B, *httptest.ResponseRecorder) {
	return func(b *testing.B, rec *httptest.ResponseRecorder) {
		b.Helper()
		if rec.Code != http.StatusOK {
			b.Fatalf("benchmark setup: status = %d, want 200 | 基准准备：状态码 = %d，期望 200", rec.Code, rec.Code)
		}
		if got := rec.Body.String(); got != want {
			b.Fatalf("benchmark setup: body = %q, want %q | 基准准备：正文 = %q，期望 %q", got, want, got, want)
		}
	}
}

// BenchmarkBaselineNoMiddleware is the control: routing, pool acquisition and the recorder with no
// middleware at all. Every other benchmark's cost is reported relative to this line.
// | BenchmarkBaselineNoMiddleware 是对照组：只有路由、对象池获取与记录器，不挂任何中间件。
// 其它基准的成本都以这一行为参考。
func BenchmarkBaselineNoMiddleware(b *testing.B) {
	r := benchRouter(benchSmallBody, "text/plain; charset=utf-8")
	req := benchRequest(nil)
	benchServe(b, r, req, benchExpectBody(benchSmallBody))
}

// BenchmarkBaselineLargeBodyNoMiddleware is the control for the compression benchmarks: the same
// 16 KiB HTML body written without any middleware, including the recorder's own body allocation.
// | BenchmarkBaselineLargeBodyNoMiddleware 是压缩基准的对照组：不挂任何中间件写出同样的
// 16 KiB HTML 正文，其中包含记录器自身的正文分配。
func BenchmarkBaselineLargeBodyNoMiddleware(b *testing.B) {
	r := benchRouter(benchHTMLPayload, "text/html; charset=utf-8")
	req := benchRequest(nil)
	benchServe(b, r, req, benchExpectBody(benchHTMLPayload))
}

// BenchmarkRecover measures the normal (no-panic) path of Recover: one deferred closure and one
// recover call around the downstream chain, with no logging and no response rewriting.
// | BenchmarkRecover 测量 Recover 的常态（无 panic）路径：下游链外的一层 defer 闭包与一次 recover 调用，
// 不写日志、不改写响应。
func BenchmarkRecover(b *testing.B) {
	r := benchRouter(benchSmallBody, "text/plain; charset=utf-8", Recover(benchDiscardLogger()))
	req := benchRequest(nil)
	benchServe(b, r, req, benchExpectBody(benchSmallBody))
}

// BenchmarkAccessLog measures the access-log middleware on every request: writer wrapping, status
// and byte counting, plus one full JSON log record (eight fields) encoded into io.Discard.
// The logger is Info-level with a discard writer, so this is CPU cost only, not disk I/O.
// | BenchmarkAccessLog 测量每个请求都会执行的访问日志中间件：包装写入器、统计状态码与字节数，
// 并把一条完整的 JSON 日志（8 个字段）编码进 io.Discard。
// 日志器为 Info 级且写入器是丢弃器，因此这里只有 CPU 成本，不含磁盘 I/O。
func BenchmarkAccessLog(b *testing.B) {
	r := benchRouter(benchSmallBody, "text/plain; charset=utf-8", AccessLog(benchDiscardLogger()))
	req := benchRequest(nil)
	benchServe(b, r, req, benchExpectBody(benchSmallBody))
}

// BenchmarkRateLimit measures the allowed-request path of the token-bucket limiter: FNV-1a key
// hashing, one shard lock, token replenishment, and the three rate-limit response headers.
// Rate and Burst are deliberately enormous so every iteration takes a token and none is throttled.
// | BenchmarkRateLimit 测量令牌桶限流器的放行路径：FNV-1a 键哈希、一次分片加锁、令牌补充，
// 以及三个限流响应头。Rate 与 Burst 取得极大，保证每轮都能取到令牌、不会触发限流。
func BenchmarkRateLimit(b *testing.B) {
	r := benchRouter(benchSmallBody, "text/plain; charset=utf-8", RateLimit(RateLimitOptions{
		Rate:  1e9,
		Burst: 1 << 30,
	}))
	req := benchRequest(nil)
	benchServe(b, r, req, benchExpectBody(benchSmallBody))
}

// BenchmarkCompressNegotiate compares the old map-based Accept-Encoding parse with the
// allocation-free scanner used by Negotiate. The map case is the "before" reference: it is the
// exact path Compress used to run on every request. | BenchmarkCompressNegotiate 对比旧的基于 map 的
// Accept-Encoding 解析与 Negotiate 使用的无分配扫描器。map 用例即「优化前」参考：它正是
// Compress 过去每个请求都会走的路径。
func BenchmarkCompressNegotiate(b *testing.B) {
	const accept = "gzip, deflate, br;q=0.9, zstd;q=0.8, *;q=0.1"
	algos := []Algorithm{Brotli, Zstd, Gzip}
	b.Run("map_parse", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchNegotiateSink = ParseAcceptEncoding(accept)
		}
	})
	b.Run("scan_negotiate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchAlgoSink, benchBoolSink = Negotiate(accept, algos)
		}
	})
}

// benchNegotiateSink and benchAlgoSink keep benchmark results observable. | benchNegotiateSink 与
// benchAlgoSink 让基准结果不被编译器消除。
var (
	benchNegotiateSink map[string]float64
	benchAlgoSink      Algorithm
	benchBoolSink      bool
)

// BenchmarkCompress covers both sides of the compression middleware. The skip cases measure the
// wrapper overhead when the response is too small or already encoded; the 16 KiB cases measure the
// full compression path for HTML and JSON, each pinned to one algorithm so the codecs can be compared.
// The ratio metric is body-size compression ratio (input / output) and SetBytes reports throughput.
// | BenchmarkCompress 覆盖压缩中间件的两侧。跳过用例测量响应过小或已编码时的包装开销；
// 16 KiB 用例测量 HTML 与 JSON 的完整压缩路径，并分别固定到单一算法以便比较编解码器。
// ratio 指标是正文字节压缩比（输入 / 输出），SetBytes 报告吞吐。
func BenchmarkCompress(b *testing.B) {
	b.Run("skip_small_html", func(b *testing.B) {
		r := benchRouter(benchSmallBody, "text/html; charset=utf-8", Compress(CompressOptions{
			Algorithms: []Algorithm{Gzip},
		}))
		req := benchRequest(map[string]string{"Accept-Encoding": "gzip"})
		benchServe(b, r, req, benchExpectBody(benchSmallBody))
	})

	b.Run("skip_already_encoded_json", func(b *testing.B) {
		r := router.New()
		r.Use(Compress(CompressOptions{Algorithms: []Algorithm{Gzip}}))
		r.GET("/bench", func(c *router.Ctx) {
			c.SetHeader("Content-Encoding", "gzip")
			c.SetHeader("Content-Type", "application/json; charset=utf-8")
			c.WriteHeader(http.StatusOK)
			_, _ = c.Writer.Write([]byte(benchJSONPayload))
		})
		req := benchRequest(map[string]string{"Accept-Encoding": "gzip"})
		benchServe(b, r, req, func(b *testing.B, rec *httptest.ResponseRecorder) {
			b.Helper()
			if rec.Code != http.StatusOK || rec.Body.String() != benchJSONPayload {
				b.Fatal("benchmark setup: already-encoded body was rewritten | 基准准备：已编码正文被改写")
			}
			if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
				b.Fatalf("benchmark setup: Content-Encoding = %q, want gzip | 基准准备：Content-Encoding = %q，期望 gzip", got, got)
			}
		})
	})

	b.Run("html_16k_brotli", func(b *testing.B) {
		benchCompressCase(b, Brotli, "br", benchHTMLPayload, "text/html; charset=utf-8")
	})
	b.Run("html_16k_zstd", func(b *testing.B) {
		benchCompressCase(b, Zstd, "zstd", benchHTMLPayload, "text/html; charset=utf-8")
	})
	b.Run("html_16k_gzip", func(b *testing.B) {
		benchCompressCase(b, Gzip, "gzip", benchHTMLPayload, "text/html; charset=utf-8")
	})
	b.Run("json_16k_gzip", func(b *testing.B) {
		benchCompressCase(b, Gzip, "gzip", benchJSONPayload, "application/json; charset=utf-8")
	})
}

// benchCompressCase runs one pinned-algorithm compression case and reports its compression ratio.
// The first request also validates the encoding header and round-trip ratio before timing begins.
// | benchCompressCase 运行一个固定算法的压缩用例并报告压缩比。
// 计时开始前会先用一次请求校验编码头与往返压缩比。
func benchCompressCase(b *testing.B, algo Algorithm, encoding, payload, contentType string) {
	b.Helper()
	r := benchRouter(payload, contentType, Compress(CompressOptions{Algorithms: []Algorithm{algo}}))
	req := benchRequest(map[string]string{"Accept-Encoding": encoding})

	warm := benchDo(r, req)
	if warm.Code != http.StatusOK {
		b.Fatalf("benchmark setup: status = %d, want 200 | 基准准备：状态码 = %d，期望 200", warm.Code, warm.Code)
	}
	if got := warm.Header().Get("Content-Encoding"); got != encoding {
		b.Fatalf("benchmark setup: Content-Encoding = %q, want %q | 基准准备：Content-Encoding = %q，期望 %q", got, encoding, got, encoding)
	}
	if warm.Body.Len() == 0 || warm.Body.Len() >= len(payload) {
		b.Fatalf("benchmark setup: payload was not compressed (%d -> %d bytes) | 基准准备：正文未被压缩（%d -> %d 字节）",
			len(payload), warm.Body.Len(), len(payload), warm.Body.Len())
	}
	b.SetBytes(int64(len(payload)))
	benchServe(b, r, req, nil)
	// ReportMetric must follow the timed loop because ResetTimer clears user metrics.
	// | ReportMetric 必须放在计时循环之后，因为 ResetTimer 会清空用户指标。
	b.ReportMetric(float64(len(payload))/float64(warm.Body.Len()), "ratio")
}
