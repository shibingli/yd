package middleware

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yun-dev/yd/jsonx"
	"github.com/yun-dev/yd/logx"
	"github.com/yun-dev/yd/router"
)

// ---------------------------------------------------------------- 测试辅助

// newTestLogger returns a JSON logger writing records into buf. | newTestLogger 返回一个把 JSON 记录写入 buf 的日志器。
func newTestLogger() (*logx.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return logx.New(logx.Options{Writer: buf, Level: logx.DebugLevel}), buf
}

// decodeLogRecords parses every JSON line in buf into a record map. | decodeLogRecords 把 buf 中的每一行 JSON 解析为一条记录。
func decodeLogRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		rec := map[string]any{}
		if err := jsonx.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("日志行不是合法 JSON: %v (行内容 %q)", err, line)
		}
		out = append(out, rec)
	}
	return out
}

// onlyRecord asserts that exactly one log record exists and returns it. | onlyRecord 断言日志恰好只有一条并返回它。
func onlyRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	recs := decodeLogRecords(t, buf)
	if len(recs) != 1 {
		t.Fatalf("日志条数 = %d, 期望 1: %v", len(recs), recs)
	}
	return recs[0]
}

// doReq runs one request through r with the given headers and body. | doReq 携带指定请求头与请求体，把请求送入 r 执行一次。
func doReq(r *router.Router, method, target string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// doReqFrom is doReq with an explicit RemoteAddr. | doReqFrom 是 doReq 的变体，可显式指定 RemoteAddr。
func doReqFrom(r *router.Router, method, target, remoteAddr string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remoteAddr
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// newRecorder returns a fresh response recorder. | newRecorder 返回一个新的响应记录器。
func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

// errNoHijack is returned by the fake hijacker below. | errNoHijack 由下面的假 Hijacker 返回。
var errNoHijack = errors.New("test: 不支持 Hijack")

// hijackRecorder adds http.Hijacker to a response recorder. | hijackRecorder 为响应记录器补充 http.Hijacker 能力。
type hijackRecorder struct {
	*httptest.ResponseRecorder
}

// Hijack implements http.Hijacker. | Hijack 实现 http.Hijacker。
func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errNoHijack
}

// newUpgradeRequest builds a WebSocket-style upgrade request. | newUpgradeRequest 构造一个 WebSocket 风格的升级请求。
func newUpgradeRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	return req
}

// ---------------------------------------------------------------- Chain

func TestChainRunsInOrder(t *testing.T) {
	var order []string
	r := router.New()
	r.Use(Chain(
		func(*router.Ctx) { order = append(order, "a") },
		nil, // nil middlewares must be skipped by the combinator. | nil 中间件必须被组合器跳过。
		func(*router.Ctx) { order = append(order, "b") },
	))
	r.GET("/x", func(c *router.Ctx) {
		order = append(order, "h")
		_ = c.String(http.StatusOK, "ok")
	})

	rec := doReq(r, http.MethodGet, "/x", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := strings.Join(order, ","); got != "a,b,h" {
		t.Fatalf("执行顺序 = %q, 期望 %q", got, "a,b,h")
	}
}

func TestChainEmptyPassesThrough(t *testing.T) {
	r := router.New()
	r.Use(Chain())
	r.GET("/x", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })

	if rec := doReq(r, http.MethodGet, "/x", nil, ""); rec.Body.String() != "ok" {
		t.Fatalf("body = %q, 期望 ok", rec.Body.String())
	}
}

func TestChainAbortStopsRemaining(t *testing.T) {
	var order []string
	r := router.New()
	r.Use(Chain(
		func(c *router.Ctx) {
			order = append(order, "a")
			_ = c.String(http.StatusForbidden, "denied")
			c.Abort()
		},
		func(*router.Ctx) { order = append(order, "b") },
	))
	r.GET("/x", func(c *router.Ctx) {
		order = append(order, "h")
		_ = c.String(http.StatusOK, "ok")
	})

	rec := doReq(r, http.MethodGet, "/x", nil, "")
	if rec.Code != http.StatusForbidden || rec.Body.String() != "denied" {
		t.Fatalf("code=%d body=%q, 期望 403 denied", rec.Code, rec.Body.String())
	}
	if got := strings.Join(order, ","); got != "a" {
		t.Fatalf("执行顺序 = %q, 期望 %q", got, "a")
	}
}

func TestChainAsRouteMiddleware(t *testing.T) {
	var order []string
	r := router.New()
	r.GET("/x", func(c *router.Ctx) {
		order = append(order, "h")
		_ = c.String(http.StatusOK, "ok")
	}, Chain(
		func(*router.Ctx) { order = append(order, "a") },
		func(*router.Ctx) { order = append(order, "b") },
	))

	doReq(r, http.MethodGet, "/x", nil, "")
	if got := strings.Join(order, ","); got != "a,b,h" {
		t.Fatalf("执行顺序 = %q, 期望 %q", got, "a,b,h")
	}
}
