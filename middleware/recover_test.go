package middleware

import (
	"net/http"
	"strings"
	"testing"

	"github.com/shibingli/yd/http/router"
)

func TestRecoverReturns500AndLogs(t *testing.T) {
	log, buf := newTestLogger()
	r := router.New()
	r.Use(RequestID(""), Recover(log))
	r.GET("/boom", func(*router.Ctx) { panic("kaboom") })

	rec := doReq(r, http.MethodGet, "/boom", nil, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, 期望 500", rec.Code)
	}
	// Internal details must not leak to the client. | 内部错误细节不得泄漏给客户端。
	if body := rec.Body.String(); strings.Contains(body, "kaboom") {
		t.Fatalf("响应体泄漏了 panic 细节: %q", body)
	}

	entry := onlyRecord(t, buf)
	if entry["level"] != "error" {
		t.Errorf("level = %v, 期望 error", entry["level"])
	}
	if entry["msg"] != "panic recovered | 捕获到 panic" {
		t.Errorf("msg = %v, 期望中英同步的 panic 文案", entry["msg"])
	}
	if entry["panic"] != "kaboom" {
		t.Errorf("panic 字段 = %v, 期望 kaboom", entry["panic"])
	}
	if entry["method"] != http.MethodGet || entry["path"] != "/boom" {
		t.Errorf("method/path = %v/%v", entry["method"], entry["path"])
	}
	stack, _ := entry["stack"].(string)
	if !strings.Contains(stack, "middleware") || !strings.Contains(stack, "goroutine") {
		t.Errorf("stack 字段未包含调用栈: %q", stack)
	}
	if id, _ := entry["request_id"].(string); id == "" {
		t.Errorf("request_id 缺失: %v", entry)
	}
}

// A response whose header is already written must not be overwritten by 500. | 响应头已写出时不得再写 500 覆盖它。
func TestRecoverDoesNotDoubleWrite(t *testing.T) {
	log, buf := newTestLogger()
	r := router.New()
	r.Use(Recover(log))
	r.GET("/partial", func(c *router.Ctx) {
		_ = c.String(http.StatusOK, "partial-body")
		panic("late panic")
	})

	rec := doReq(r, http.MethodGet, "/partial", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, 期望保持 200", rec.Code)
	}
	if rec.Body.String() != "partial-body" {
		t.Fatalf("body = %q, 期望 partial-body", rec.Body.String())
	}
	entry := onlyRecord(t, buf)
	if entry["panic"] != "late panic" {
		t.Errorf("panic 字段 = %v, 期望 late panic", entry["panic"])
	}
}

// Handlers after the panicking one must not run after recovery. | 恢复之后，panic 之后的处理器不得继续执行。
func TestRecoverAbortsRemainingHandlers(t *testing.T) {
	log, _ := newTestLogger()
	ran := false
	r := router.New()
	// Middlewares run before the route handler, so the panicking one is followed by the terminal handler. | 中间件先于路由处理器执行，因此 panic 中间件之后紧跟终结处理器。
	r.GET("/boom",
		func(*router.Ctx) { ran = true },
		func(*router.Ctx) { panic("stop here") },
	)
	r.Use(Recover(log))

	_ = doReq(r, http.MethodGet, "/boom", nil, "")
	if ran {
		t.Fatal("a handler after the panic ran | panic 之后的处理器被执行了")
	}
}

// Recover must stay usable without a logger. | Recover 在日志器为 nil 时仍必须可用。
func TestRecoverWithNilLogger(t *testing.T) {
	r := router.New()
	r.Use(Recover(nil))
	r.GET("/boom", func(*router.Ctx) { panic("no logger") })

	if rec := doReq(r, http.MethodGet, "/boom", nil, ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, 期望 500", rec.Code)
	}
}

// Normal requests pass through untouched and produce no log record. | 正常请求原样透传，且不产生任何日志记录。
func TestRecoverPassesThrough(t *testing.T) {
	log, buf := newTestLogger()
	r := router.New()
	r.Use(Recover(log))
	r.GET("/ok", func(c *router.Ctx) { _ = c.String(http.StatusTeapot, "fine") })

	rec := doReq(r, http.MethodGet, "/ok", nil, "")
	if rec.Code != http.StatusTeapot || rec.Body.String() != "fine" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if buf.Len() != 0 {
		t.Fatalf("正常请求不应记录日志: %q", buf.String())
	}
}
