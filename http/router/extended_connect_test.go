package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// extendedConnectRequest builds an RFC 8441 or RFC 9220 CONNECT request. | extendedConnectRequest 构造 RFC 8441 或 RFC 9220 的 CONNECT 请求。
func extendedConnectRequest(path string, major int, proto string, body string) *http.Request {
	req := httptest.NewRequest(http.MethodConnect, path, strings.NewReader(body))
	req.ProtoMajor = major
	req.Proto = proto
	return req
}

// TestRouterRoutesExtendedConnectToGET proves a GET-only WebSocket route also serves H2/H3. | TestRouterRoutesExtendedConnectToGET 证明只注册 GET 的 WebSocket 路由同样服务 H2/H3。
func TestRouterRoutesExtendedConnectToGET(t *testing.T) {
	r := New()
	r.GET("/ws", func(c *Ctx) { _ = c.String(http.StatusOK, "ok") })

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"h2 | HTTP/2", func() *http.Request {
			req := extendedConnectRequest("/ws", 2, "HTTP/2.0", "")
			req.Header.Set(":protocol", "websocket")
			return req
		}()},
		{"h3 | HTTP/3", extendedConnectRequest("/ws", 3, "websocket", "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, tc.req)
			if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
				t.Fatalf("code=%d body=%q, want 200 ok | code=%d body=%q，期望 200 ok", rec.Code, rec.Body.String(), rec.Code, rec.Body.String())
			}
		})
	}

	// The extended CONNECT must also populate the GET route cache. | 扩展连接也必须填充 GET 路由缓存。
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("GET after extended CONNECT: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestRouterDoesNotRedirectPlainConnectToGET proves the fallback is narrow. | TestRouterDoesNotRedirectPlainConnectToGET 证明回退范围是窄的。
func TestRouterDoesNotRedirectPlainConnectToGET(t *testing.T) {
	r := New()
	called := false
	r.GET("/ws", func(c *Ctx) {
		called = true
		_ = c.String(http.StatusOK, "ok")
	})

	req := extendedConnectRequest("/ws", 2, "HTTP/2.0", "")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if called {
		t.Fatal("plain CONNECT reached the GET handler | 普通 CONNECT 命中了 GET 处理器")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code=%d, want 405 | code=%d，期望 405", rec.Code, rec.Code)
	}
}

// TestConnectBodyIsNotLimited proves extended CONNECT streams bypass MaxBytesReader. | TestConnectBodyIsNotLimited 证明扩展连接流绕过 MaxBytesReader。
func TestConnectBodyIsNotLimited(t *testing.T) {
	const payload = "12345678"
	r := New(WithMaxBodySize(4))
	var (
		gotErr  error
		gotBody []byte
	)
	r.GET("/stream", func(c *Ctx) {
		gotBody, gotErr = io.ReadAll(c.Body())
	})

	req := extendedConnectRequest("/stream", 2, "HTTP/2.0", payload)
	req.Header.Set(":protocol", "websocket")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if gotErr != nil {
		t.Fatalf("read body: %v, want nil (CONNECT must not be limited) | 读取正文失败: %v，期望不受限", gotErr, gotErr)
	}
	if string(gotBody) != payload {
		t.Fatalf("body = %q, want %q", gotBody, payload)
	}
}
