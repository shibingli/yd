package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/http/router"
)

// numField reads a numeric log field encoded as JSON number. | numField 读取以 JSON 数字编码的数值日志字段。
func numField(t *testing.T, entry map[string]any, key string) float64 {
	t.Helper()
	v, ok := entry[key].(float64)
	if !ok {
		t.Fatalf("字段 %s = %#v, 期望 JSON 数字", key, entry[key])
	}
	return v
}

func TestAccessLogRecordsFields(t *testing.T) {
	log, buf := newTestLogger()
	r := router.New()
	r.Use(RequestID(""), AccessLog(log))
	r.GET("/hello", func(c *router.Ctx) { _ = c.String(http.StatusCreated, "hello world") })

	rec := doReq(r, http.MethodGet, "/hello", nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, 期望 201", rec.Code)
	}

	entry := onlyRecord(t, buf)
	if entry["msg"] != "access | 访问日志" {
		t.Errorf("msg = %v, 期望中英同步的访问日志文案", entry["msg"])
	}
	if entry["method"] != http.MethodGet {
		t.Errorf("method = %v, 期望 GET", entry["method"])
	}
	if entry["path"] != "/hello" {
		t.Errorf("path = %v, 期望 /hello", entry["path"])
	}
	if got := numField(t, entry, "status"); got != http.StatusCreated {
		t.Errorf("status = %v, 期望 201", got)
	}
	if got := numField(t, entry, "bytes"); got != float64(len("hello world")) {
		t.Errorf("bytes = %v, 期望 %d", got, len("hello world"))
	}
	// logx renders Dur as milliseconds (float64) in JSON. | logx 在 JSON 中把时长渲染为毫秒（float64）。
	if got := numField(t, entry, "duration"); got < 0 {
		t.Errorf("duration = %v, 期望 >= 0", got)
	}
	if got, _ := entry["request_id"].(string); got != rec.Header().Get("X-Request-Id") {
		t.Errorf("request_id = %v, 期望 %q", entry["request_id"], rec.Header().Get("X-Request-Id"))
	}
}

// A handler that never writes is logged as 200 with zero bytes. | 从未写入的处理器按 200、零字节记录。
func TestAccessLogDefaultsToOK(t *testing.T) {
	log, buf := newTestLogger()
	r := router.New()
	r.Use(AccessLog(log))
	r.GET("/silent", func(*router.Ctx) {})

	doReq(r, http.MethodGet, "/silent", nil, "")

	entry := onlyRecord(t, buf)
	if got := numField(t, entry, "status"); got != http.StatusOK {
		t.Errorf("status = %v, 期望 200", got)
	}
	if got := numField(t, entry, "bytes"); got != 0 {
		t.Errorf("bytes = %v, 期望 0", got)
	}
}

// Byte counting must not break chunked flushing. | 字节计数不得破坏分块 flush。
func TestAccessLogCountsChunksAndKeepsFlush(t *testing.T) {
	log, buf := newTestLogger()
	r := router.New()
	r.Use(AccessLog(log))
	r.GET("/stream", func(c *router.Ctx) {
		c.SetHeader("Content-Type", "text/plain; charset=utf-8")
		c.WriteHeader(http.StatusOK)
		for _, chunk := range []string{"one", "two", "three"} {
			_, _ = c.Writer.Write([]byte(chunk))
			c.Flush()
		}
	})

	rec := doReq(r, http.MethodGet, "/stream", nil, "")
	if !rec.Flushed {
		t.Fatal("the underlying writer never saw a Flush | 底层写入器未收到 Flush")
	}
	if rec.Body.String() != "onetwothree" {
		t.Fatalf("body = %q, 期望 onetwothree", rec.Body.String())
	}
	entry := onlyRecord(t, buf)
	if got := numField(t, entry, "bytes"); got != float64(len("onetwothree")) {
		t.Errorf("bytes = %v, 期望 %d", got, len("onetwothree"))
	}
}

// syncBuffer is a mutex-guarded log sink for parallel access-log tests.
// | syncBuffer 是并行访问日志测试所使用的互斥锁保护日志接收器。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p under the lock. | Write 在锁内追加 p。
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns the collected output under the lock. | String 在锁内返回已收集的输出。
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestAccessLogConcurrentReuseIsIsolated drives many requests through a pooled countingWriter and
// asserts every log record still carries its own status and byte count. A stale field surviving
// the pool would show up here as a mismatched record under -race and in the counts.
// | TestAccessLogConcurrentReuseIsIsolated 让大量请求复用池化的 countingWriter，
// 断言每条日志仍携带自身的状态码与字节数。若有字段在归还后残留，这里会在 -race 下或计数校验中暴露。
func TestAccessLogConcurrentReuseIsIsolated(t *testing.T) {
	type routeCase struct {
		path   string
		body   string
		status int
	}
	routes := []routeCase{
		{"/r0", "", http.StatusOK},
		{"/r1", "a", http.StatusOK},
		{"/r2", "bb", http.StatusCreated},
		{"/r3", strings.Repeat("c", 37), http.StatusAccepted},
		{"/r4", strings.Repeat("d", 129), http.StatusNonAuthoritativeInfo},
	}
	byPath := make(map[string]routeCase, len(routes))

	sink := &syncBuffer{}
	log := logx.New(logx.Options{Writer: sink, Level: logx.DebugLevel})
	r := router.New()
	r.Use(AccessLog(log))
	for _, rt := range routes {
		rt := rt
		byPath[rt.path] = rt
		r.GET(rt.path, func(c *router.Ctx) { _ = c.String(rt.status, rt.body) })
	}

	const (
		workers    = 16
		iterations = 100
	)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				rt := routes[(w+i)%len(routes)]
				rec := doReq(r, http.MethodGet, rt.path, nil, "")
				if rec.Code != rt.status {
					t.Errorf("%s: code = %d, 期望 %d", rt.path, rec.Code, rt.status)
				}
				if rec.Body.String() != rt.body {
					t.Errorf("%s: body = %q, 期望 %q", rt.path, rec.Body.String(), rt.body)
				}
			}
		}(w)
	}
	wg.Wait()

	counts := make(map[string]int, len(routes))
	for _, line := range strings.Split(strings.TrimSpace(sink.String()), "\n") {
		if line == "" {
			continue
		}
		entry := map[string]any{}
		if err := jsonx.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("日志行不是合法 JSON: %v (行内容 %q)", err, line)
		}
		path, _ := entry["path"].(string)
		want, ok := byPath[path]
		if !ok {
			t.Fatalf("日志中的 path = %q 不属于任何测试路由", path)
		}
		counts[path]++
		if got := numField(t, entry, "status"); got != float64(want.status) {
			t.Errorf("%s: 日志 status = %v, 期望 %d", path, got, want.status)
		}
		if got := numField(t, entry, "bytes"); got != float64(len(want.body)) {
			t.Errorf("%s: 日志 bytes = %v, 期望 %d", path, got, len(want.body))
		}
	}
	for _, rt := range routes {
		if counts[rt.path] == 0 {
			t.Errorf("%s: 没有对应的日志记录", rt.path)
		}
	}
}

// Upgrade requests must keep a writable Hijacker. | 升级请求必须保持可用的 Hijacker。
func TestAccessLogKeepsHijackerOnUpgrade(t *testing.T) {
	log, _ := newTestLogger()
	hijackable := false
	r := router.New()
	r.Use(AccessLog(log))
	r.GET("/ws", func(c *router.Ctx) {
		_, hijackable = c.Writer.(http.Hijacker)
		_ = c.NoContent()
	})

	hj := &hijackRecorder{ResponseRecorder: newRecorder()}
	req := newUpgradeRequest("/ws")
	r.ServeHTTP(hj, req)
	if !hijackable {
		t.Fatal("the upgraded request lost http.Hijacker on c.Writer | 升级请求的 c.Writer 丢失了 http.Hijacker")
	}
}

// TestAccessLogRemoteIPFollowsClientIPTrust 要求 remote_ip 采用可信代理模型：
// 默认忽略代理头，只有对端落在 TrustedProxies 内时才采信 X-Forwarded-For。 | TestAccessLogRemoteIPFollowsClientIPTrust
// requires remote_ip to follow the trusted-proxy model: proxy headers are ignored by default and
// honored only when the peer is inside TrustedProxies.
func TestAccessLogRemoteIPFollowsClientIPTrust(t *testing.T) {
	const peer = "203.0.113.9:443"
	const forwarded = "198.51.100.7, 203.0.113.10"
	tests := []struct {
		name    string
		trusted []netip.Prefix
		want    string
	}{
		{name: "default ignores the header", want: "203.0.113.9"},
		{
			name:    "trusted peer walks the chain from the right",
			trusted: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")},
			want:    "198.51.100.7",
		},
		{
			name:    "untrusted peer ignores the header",
			trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
			want:    "203.0.113.9",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, buf := newTestLogger()
			r := router.New()
			r.Use(AccessLog(log, AccessLogOptions{TrustedProxies: tt.trusted}))
			r.GET("/ip", func(c *router.Ctx) { _ = c.NoContent() })

			doReqFrom(r, http.MethodGet, "/ip", peer, map[string]string{"X-Forwarded-For": forwarded})
			if got := onlyRecord(t, buf)["remote_ip"]; got != tt.want {
				t.Fatalf("remote_ip = %v, want %q | remote_ip = %v，期望 %q", got, tt.want, got, tt.want)
			}
		})
	}
}

// readFromRecorder exposes io.ReaderFrom and records that the fast path was taken. | readFromRecorder
// 暴露 io.ReaderFrom 并记录快路径确实被使用。
type readFromRecorder struct {
	*httptest.ResponseRecorder
	used bool
}

// ReadFrom implements io.ReaderFrom. | ReadFrom 实现 io.ReaderFrom。
func (w *readFromRecorder) ReadFrom(r io.Reader) (int64, error) {
	w.used = true
	return io.Copy(w.ResponseRecorder, r)
}

// TestCountingWriterReadFromCountsBytes 要求包装器实现 io.ReaderFrom：
// 有底层快路径时委托给它，没有时回退缓冲拷贝，两者都正确计数。 | TestCountingWriterReadFromCountsBytes
// requires the wrapper to implement io.ReaderFrom: delegate when the underlying writer has the fast
// path and fall back to a buffered copy otherwise, counting bytes in both cases.
func TestCountingWriterReadFromCountsBytes(t *testing.T) {
	const body = "reader-from-body"

	plain := httptest.NewRecorder()
	cw := &countingWriter{ResponseWriter: plain}
	n, err := cw.ReadFrom(strings.NewReader(body))
	if err != nil || n != int64(len(body)) {
		t.Fatalf("ReadFrom = (%d, %v), want (%d, nil) | ReadFrom = (%d, %v)，期望 (%d, nil)", n, err, len(body), n, err, len(body))
	}
	if cw.bytes != n || cw.status != http.StatusOK || plain.Body.String() != body {
		t.Fatalf("bytes=%d status=%d body=%q | bytes=%d status=%d body=%q", cw.bytes, cw.status, plain.Body.String(), cw.bytes, cw.status, plain.Body.String())
	}

	fast := &readFromRecorder{ResponseRecorder: httptest.NewRecorder()}
	cw = &countingWriter{ResponseWriter: fast}
	n, err = cw.ReadFrom(strings.NewReader(body))
	if err != nil || n != int64(len(body)) || !fast.used || cw.bytes != n {
		t.Fatalf("fast path: n=%d err=%v used=%v bytes=%d | 快路径：n=%d err=%v used=%v bytes=%d", n, err, fast.used, cw.bytes, n, err, fast.used, cw.bytes)
	}
}
