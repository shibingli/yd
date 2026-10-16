package middleware

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/yun-dev/yd/errx"
	"github.com/yun-dev/yd/router"
)

// bigBody is a compressible payload well above the default MinSize. | bigBody 是一段远大于默认 MinSize 的可压缩负载。
var bigBody = strings.Repeat("<p>yd framework compresses well</p>", 200)

// errRoundTripMismatch reports a response that did not survive the compression round trip. | errRoundTripMismatch 表示响应未能通过压缩往返校验。
var errRoundTripMismatch = errx.New("compression round trip mismatch | 压缩往返不一致")

// wrappedByCompress reports whether w is the compression wrapper. | wrappedByCompress 报告 w 是否为压缩包装器。
func wrappedByCompress(w http.ResponseWriter) bool {
	_, ok := w.(*compressWriter)
	return ok
}

// decompressBody decodes a compressed payload for round-trip assertions. | decompressBody 解码压缩负载，用于往返断言。
func decompressBody(t *testing.T, encoding string, b []byte) string {
	t.Helper()
	switch encoding {
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("gzip.NewReader: %v", err)
		}
		defer zr.Close()
		out, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("读取 gzip 流: %v", err)
		}
		return string(out)
	case "br":
		out, err := io.ReadAll(brotli.NewReader(bytes.NewReader(b)))
		if err != nil {
			t.Fatalf("读取 brotli 流: %v", err)
		}
		return string(out)
	case "zstd":
		zr, err := zstd.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("zstd.NewReader: %v", err)
		}
		defer zr.Close()
		out, err := io.ReadAll(zr.IOReadCloser())
		if err != nil {
			t.Fatalf("读取 zstd 流: %v", err)
		}
		return string(out)
	default:
		return string(b)
	}
}

// ---------------------------------------------------------------- encoding negotiation | 编码协商

func TestNegotiateQValues(t *testing.T) {
	def := []Algorithm{Brotli, Zstd, Gzip}
	cases := []struct {
		name   string
		accept string
		algos  []Algorithm
		want   Algorithm
		ok     bool
	}{
		{"q 值优先于列表顺序", "br;q=0.5, gzip;q=1.0", def, Gzip, true},
		{"q=0 禁用 gzip", "gzip;q=0", def, 0, false},
		{"q=0 禁用 gzip 但保留 br", "gzip;q=0, br", def, Brotli, true},
		{"无请求头不压缩", "", def, 0, false},
		{"仅空白不压缩", "   ", def, 0, false},
		{"同权重按列表顺序", "gzip, br, zstd", def, Brotli, true},
		{"自定义顺序按列表顺序", "gzip, br, zstd", []Algorithm{Gzip, Zstd, Brotli}, Gzip, true},
		{"同 q 值按列表顺序", "gzip;q=0.5, br;q=0.5", def, Brotli, true},
		{"大小写不敏感", "GZIP", def, Gzip, true},
		{"不支持的编码不选", "deflate, identity", def, 0, false},
		{"通配取列表首位", "*", def, Brotli, true},
		{"星号 q=0 只放行显式编码", "*;q=0, gzip", def, Gzip, true},
		{"星号 q=0 且无显式编码则不压缩", "*;q=0", def, 0, false},
		{"空算法列表退回默认优先级", "gzip, br, zstd", nil, Brotli, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Negotiate(tc.accept, tc.algos)
			if ok != tc.ok {
				t.Fatalf("Negotiate(%q) ok = %v, 期望 %v", tc.accept, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("Negotiate(%q) = %v(%s), 期望 %v(%s)", tc.accept, got, got.Encoding(), tc.want, tc.want.Encoding())
			}
		})
	}
}

func TestParseAcceptEncoding(t *testing.T) {
	got := ParseAcceptEncoding("gzip;q=0.8, br;q=0.5, *;q=0.1, zstd;q=0")
	want := map[string]float64{"gzip": 0.8, "br": 0.5, "*": 0.1, "zstd": 0}
	if len(got) != len(want) {
		t.Fatalf("解析结果 = %v, 期望 %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, 期望 %v", k, got[k], v)
		}
	}
	// Missing q, extra whitespace and uppercase names default to 1. | 缺少 q、多余空白与大写名称一律按 1 处理。
	if q := ParseAcceptEncoding(" GZIP "); q["gzip"] != 1 {
		t.Errorf("ParseAcceptEncoding(\" GZIP \") = %v, 期望 gzip:1", q)
	}
	if q := ParseAcceptEncoding(""); len(q) != 0 {
		t.Errorf("ParseAcceptEncoding(\"\") = %v, 期望空", q)
	}
}

func TestAlgorithmNameAndEncoding(t *testing.T) {
	cases := []struct {
		algo     Algorithm
		name     string
		encoding string
	}{
		{Gzip, "gzip", "gzip"},
		{Brotli, "brotli", "br"},
		{Zstd, "zstd", "zstd"},
	}
	for _, tc := range cases {
		if got := tc.algo.Name(); got != tc.name {
			t.Errorf("%d.Name() = %q, 期望 %q", tc.algo, got, tc.name)
		}
		if got := tc.algo.Encoding(); got != tc.encoding {
			t.Errorf("%d.Encoding() = %q, 期望 %q", tc.algo, got, tc.encoding)
		}
	}
	if got := Algorithm(99).Encoding(); got != "" {
		t.Errorf("未知算法的 Encoding() = %q, 期望空", got)
	}
}

// ---------------------------------------------------------------- compression round trip | 压缩往返

func TestCompressGzipRoundTrip(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{Algorithms: []Algorithm{Gzip}}))
	r.GET("/page", func(c *router.Ctx) {
		c.SetHeader("X-Custom", "kept")
		c.SetHeader("Content-Type", "text/html; charset=utf-8")
		c.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write([]byte(bigBody))
	})

	rec := doReq(r, http.MethodGet, "/page", map[string]string{"Accept-Encoding": "gzip"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, 期望 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, 期望 gzip", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("Vary = %q, 期望包含 Accept-Encoding", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Fatalf("压缩后必须移除 Content-Length, 实际 %q", got)
	}
	if rec.Body.Len() >= len(bigBody) {
		t.Fatalf("压缩后体积 %d 未小于原文 %d", rec.Body.Len(), len(bigBody))
	}
	if got := decompressBody(t, "gzip", rec.Body.Bytes()); got != bigBody {
		t.Fatalf("往返结果与原文不一致, 长度 %d vs %d", len(got), len(bigBody))
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, 处理器设置的其它头部必须保留", got)
	}
	if got := rec.Header().Get("X-Custom"); got != "kept" {
		t.Fatalf("X-Custom = %q, 处理器设置的其它头部必须保留", got)
	}
}

func TestBrotliAndZstdPreferredOverGzip(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{}))
	r.GET("/page", func(c *router.Ctx) { _ = c.String(http.StatusOK, bigBody) })

	cases := []struct {
		accept   string
		encoding string
	}{
		{"gzip, br, zstd", "br"},
		{"gzip", "gzip"},
		{"zstd", "zstd"},
		{"br;q=0.1, zstd;q=0.9", "zstd"},
		{"br, gzip;q=0.9", "br"},
	}
	for _, tc := range cases {
		t.Run(tc.accept, func(t *testing.T) {
			rec := doReq(r, http.MethodGet, "/page", map[string]string{"Accept-Encoding": tc.accept}, "")
			if got := rec.Header().Get("Content-Encoding"); got != tc.encoding {
				t.Fatalf("Content-Encoding = %q, 期望 %q", got, tc.encoding)
			}
			if got := decompressBody(t, tc.encoding, rec.Body.Bytes()); got != bigBody {
				t.Fatalf("往返结果与原文不一致, 长度 %d vs %d", len(got), len(bigBody))
			}
		})
	}
}

// ---------------------------------------------------------------- skip rules | 跳过规则

// requireUncompressed asserts the response kept its plain body and no encoding headers. | requireUncompressed 断言响应保持明文正文且没有编码相关头部。
func requireUncompressed(t *testing.T, rec *httptest.ResponseRecorder, wantBody string) {
	t.Helper()
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, 期望为空", got)
	}
	if got := rec.Header().Get("Vary"); strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, 未压缩时不应追加 Accept-Encoding", got)
	}
	if got := rec.Body.String(); got != wantBody {
		t.Errorf("body = %q, 期望 %q", got, wantBody)
	}
}

func TestCompressSkipsSmallBody(t *testing.T) {
	small := "tiny body"
	r := router.New()
	r.Use(Compress(CompressOptions{}))
	r.GET("/small", func(c *router.Ctx) { _ = c.String(http.StatusOK, small) })

	rec := doReq(r, http.MethodGet, "/small", map[string]string{"Accept-Encoding": "gzip, br, zstd"}, "")
	requireUncompressed(t, rec, small)
}

// A known Content-Length below MinSize must skip compression before writing the header. | 已知的 Content-Length 小于 MinSize 时必须在写出响应头前就跳过压缩。
func TestCompressSkipsSmallContentLength(t *testing.T) {
	small := "tiny body"
	r := router.New()
	r.Use(Compress(CompressOptions{}))
	r.GET("/small", func(c *router.Ctx) {
		c.SetHeader("Content-Length", fmt.Sprint(len(small)))
		c.SetHeader("Content-Type", "text/plain; charset=utf-8")
		c.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write([]byte(small))
	})

	rec := doReq(r, http.MethodGet, "/small", map[string]string{"Accept-Encoding": "gzip"}, "")
	requireUncompressed(t, rec, small)
	if got := rec.Header().Get("Content-Length"); got != fmt.Sprint(len(small)) {
		t.Errorf("Content-Length = %q, 期望保留", got)
	}
}

func TestCompressSkipsNoAcceptEncoding(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{}))
	r.GET("/page", func(c *router.Ctx) { _ = c.String(http.StatusOK, bigBody) })

	rec := doReq(r, http.MethodGet, "/page", nil, "")
	requireUncompressed(t, rec, bigBody)
}

func TestCompressSkipsGzipDisabledByQValue(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{}))
	r.GET("/page", func(c *router.Ctx) { _ = c.String(http.StatusOK, bigBody) })

	rec := doReq(r, http.MethodGet, "/page", map[string]string{"Accept-Encoding": "gzip;q=0"}, "")
	requireUncompressed(t, rec, bigBody)
}

func TestCompressSkipsAlreadyEncoded(t *testing.T) {
	payload := "already-encoded-payload"
	r := router.New()
	r.Use(Compress(CompressOptions{}))
	r.GET("/pre", func(c *router.Ctx) {
		c.SetHeader("Content-Encoding", "br")
		c.SetHeader("Content-Type", "text/plain; charset=utf-8")
		_ = c.String(http.StatusOK, strings.Repeat(payload, 200))
	})

	rec := doReq(r, http.MethodGet, "/pre", map[string]string{"Accept-Encoding": "gzip"}, "")
	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding = %q, 期望保持 br", got)
	}
	if rec.Body.String() != strings.Repeat(payload, 200) {
		t.Fatal("已压缩响应不得被二次压缩")
	}
}

func TestCompressSkipsSSE(t *testing.T) {
	const chunk = "data: tick\n\n"
	r := router.New()
	r.Use(Compress(CompressOptions{}))
	rec := httptest.NewRecorder()

	var visibleAfterFlush []int
	r.GET("/events", func(c *router.Ctx) {
		c.SetHeader("Content-Type", "text/event-stream; charset=utf-8")
		c.WriteHeader(http.StatusOK)
		for i := 0; i < 3; i++ {
			_, _ = c.Writer.Write([]byte(chunk))
			c.Flush()
			visibleAfterFlush = append(visibleAfterFlush, rec.Body.Len())
		}
		if wrappedByCompress(c.Writer) {
			t.Error("SSE 响应在判定后仍被包装")
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	r.ServeHTTP(rec, req)

	for i, n := range visibleAfterFlush {
		if n != (i+1)*len(chunk) {
			t.Fatalf("第 %d 次 flush 后可见 %d 字节, 期望 %d（流式写入被缓冲）", i+1, n, (i+1)*len(chunk))
		}
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, 期望为空", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Errorf("Content-Type = %q, 期望保持 text/event-stream", got)
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q, SSE 不应追加", got)
	}
	if rec.Body.String() != strings.Repeat(chunk, 3) {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestCompressSkipsNoContentAndNotModified(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{}))
	r.GET("/empty", func(c *router.Ctx) { _ = c.NoContent() })
	r.GET("/cached", func(c *router.Ctx) { _ = c.NoContent(http.StatusNotModified) })

	rec := doReq(r, http.MethodGet, "/empty", map[string]string{"Accept-Encoding": "gzip"}, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, 期望 204", rec.Code)
	}
	requireUncompressed(t, rec, "")

	rec = doReq(r, http.MethodGet, "/cached", map[string]string{"Accept-Encoding": "gzip"}, "")
	if rec.Code != http.StatusNotModified {
		t.Fatalf("code = %d, 期望 304", rec.Code)
	}
	requireUncompressed(t, rec, "")
}

func TestCompressSkipsHeadRequest(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{}))
	r.HEAD("/page", func(c *router.Ctx) {
		if wrappedByCompress(c.Writer) {
			t.Error("HEAD 请求不得被包装")
		}
		c.SetHeader("Content-Length", fmt.Sprint(len(bigBody)))
		c.SetHeader("Content-Type", "text/html; charset=utf-8")
		c.WriteHeader(http.StatusOK)
	})

	rec := doReq(r, http.MethodHead, "/page", map[string]string{"Accept-Encoding": "gzip"}, "")
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, 期望为空", got)
	}
	if got := rec.Header().Get("Content-Length"); got != fmt.Sprint(len(bigBody)) {
		t.Errorf("Content-Length = %q, 期望保留", got)
	}
}

func TestCompressSkipsUpgradeRequest(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{}))
	r.GET("/ws", func(c *router.Ctx) {
		if wrappedByCompress(c.Writer) {
			t.Error("升级请求不得被包装")
		}
		if _, ok := c.Writer.(http.Hijacker); !ok {
			t.Error("升级请求的写入器必须保留 http.Hijacker")
		}
		_ = c.String(http.StatusOK, "upgraded")
	})

	rec := &hijackRecorder{ResponseRecorder: newRecorder()}
	req := newUpgradeRequest("/ws")
	req.Header.Set("Accept-Encoding", "gzip")
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, 期望为空", got)
	}
	if rec.Body.String() != "upgraded" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// A configured list replaces the built-in defaults. | 显式配置的列表会替换内置默认值。
func TestCompressExcludePatternsReplaceDefaults(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{ExcludePatterns: []string{"/assets"}}))
	r.GET("/ws/socket", func(c *router.Ctx) { _ = c.String(http.StatusOK, bigBody) })

	rec := doReq(r, http.MethodGet, "/ws/socket", map[string]string{"Accept-Encoding": "gzip"}, "")
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, 自定义排除列表应替换默认前缀表", got)
	}
}

func TestCompressExcludePatterns(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{ExcludePatterns: []string{"/assets", "/ws", "/sse", "/events"}}))
	for _, p := range []string{"/assets/app.js", "/ws/socket", "/sse/stream", "/events/feed", "/page"} {
		path := p
		r.GET(path, func(c *router.Ctx) { _ = c.String(http.StatusOK, path+bigBody) })
	}

	excluded := []string{"/assets/app.js", "/ws/socket", "/sse/stream", "/events/feed"}
	for _, path := range excluded {
		rec := doReq(r, http.MethodGet, path, map[string]string{"Accept-Encoding": "gzip"}, "")
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("%s 命中排除规则, Content-Encoding = %q", path, got)
		}
		if rec.Body.String() != path+bigBody {
			t.Errorf("%s 正文被改写", path)
		}
	}

	rec := doReq(r, http.MethodGet, "/page", map[string]string{"Accept-Encoding": "gzip"}, "")
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("/page 应被压缩, Content-Encoding = %q", got)
	}
}

func TestCompressMinSizeOption(t *testing.T) {
	body := strings.Repeat("x", 64)
	r := router.New()
	r.Use(Compress(CompressOptions{MinSize: 32, Algorithms: []Algorithm{Gzip}}))
	r.GET("/page", func(c *router.Ctx) { _ = c.String(http.StatusOK, body) })

	rec := doReq(r, http.MethodGet, "/page", map[string]string{"Accept-Encoding": "gzip"}, "")
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, 期望 gzip（阈值已下调到 32）", got)
	}
	if got := decompressBody(t, "gzip", rec.Body.Bytes()); got != body {
		t.Fatalf("往返结果 = %q", got)
	}
}

func TestCompressLevelOption(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{Level: 1, Algorithms: []Algorithm{Gzip, Brotli, Zstd}}))
	r.GET("/page", func(c *router.Ctx) { _ = c.String(http.StatusOK, bigBody) })

	for _, enc := range []string{"gzip", "br", "zstd"} {
		rec := doReq(r, http.MethodGet, "/page", map[string]string{"Accept-Encoding": enc}, "")
		if got := rec.Header().Get("Content-Encoding"); got != enc {
			t.Fatalf("%s: Content-Encoding = %q", enc, got)
		}
		if got := decompressBody(t, enc, rec.Body.Bytes()); got != bigBody {
			t.Fatalf("%s: 往返结果与原文不一致", enc)
		}
	}
}

// ---------------------------------------------------------------- pooling and concurrency | 复用与并发

func TestEncoderPoolReuse(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{Algorithms: []Algorithm{Gzip, Brotli, Zstd}}))
	r.GET("/page", func(c *router.Ctx) { _ = c.String(http.StatusOK, bigBody) })

	const rounds = 100
	encodings := []string{"gzip", "br", "zstd"}
	var wg sync.WaitGroup
	errs := make(chan error, rounds)
	for i := 0; i < rounds; i++ {
		enc := encodings[i%len(encodings)]
		wg.Add(1)
		go func(i int, enc string) {
			defer wg.Done()
			rec := doReq(r, http.MethodGet, "/page", map[string]string{"Accept-Encoding": enc}, "")
			if got := rec.Header().Get("Content-Encoding"); got != enc {
				errs <- errx.Detail(errRoundTripMismatch, "第 %d 轮 Content-Encoding = %q, 期望 %q", i, got, enc)
				return
			}
			if got := decompressBody(t, enc, rec.Body.Bytes()); got != bigBody {
				errs <- errx.Detail(errRoundTripMismatch, "第 %d 轮往返结果不一致", i)
			}
		}(i, enc)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// Compressing a response repeatedly must keep the pooled encoders usable. | 反复压缩响应必须保证池化编码器始终可复用。
func TestEncoderPoolSequentialReuse(t *testing.T) {
	r := router.New()
	r.Use(Compress(CompressOptions{Algorithms: []Algorithm{Gzip}}))
	r.GET("/page", func(c *router.Ctx) { _ = c.String(http.StatusOK, bigBody) })

	for i := 0; i < 50; i++ {
		rec := doReq(r, http.MethodGet, "/page", map[string]string{"Accept-Encoding": "gzip"}, "")
		if got := decompressBody(t, "gzip", rec.Body.Bytes()); got != bigBody {
			t.Fatalf("第 %d 轮往返结果不一致", i)
		}
	}
}
