package middleware

import (
	"net/http"
	"testing"

	"github.com/yun-dev/yd/router"
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
	if entry["msg"] != "access" {
		t.Errorf("msg = %v, 期望 access", entry["msg"])
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
		t.Fatal("底层写入器未收到 Flush")
	}
	if rec.Body.String() != "onetwothree" {
		t.Fatalf("body = %q, 期望 onetwothree", rec.Body.String())
	}
	entry := onlyRecord(t, buf)
	if got := numField(t, entry, "bytes"); got != float64(len("onetwothree")) {
		t.Errorf("bytes = %v, 期望 %d", got, len("onetwothree"))
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
		t.Fatal("升级请求的 c.Writer 丢失了 http.Hijacker")
	}
}
