package middleware

import (
	"net/http"
	"testing"
	"time"

	"github.com/shibingli/yd/router"
)

// corsRouter builds a router guarded by CORS with one echo handler. | corsRouter 构造一个由 CORS 保护的、带回显处理器的路由器。
func corsRouter(o CORSOptions) *router.Router {
	r := router.New()
	r.Use(CORS(o))
	r.GET("/api", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })
	r.OPTIONS("/api", func(c *router.Ctx) { _ = c.String(http.StatusOK, "should-not-run") })
	return r
}

func TestCORSWildcardSubdomain(t *testing.T) {
	r := corsRouter(CORSOptions{AllowOrigins: []string{"*.example.com"}})

	cases := []struct {
		name   string
		origin string
		want   string
	}{
		{"单层子域", "https://api.example.com", "https://api.example.com"},
		{"多层子域", "https://a.b.example.com", "https://a.b.example.com"},
		{"无协议前缀同样匹配", "http://cdn.example.com", "http://cdn.example.com"},
		{"相似域名不得匹配", "https://evil-example.com", ""},
		{"后缀伪装不得匹配", "https://example.com.evil.com", ""},
		{"裸域名不属于子域", "https://example.com", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doReq(r, http.MethodGet, "/api", map[string]string{"Origin": tc.origin}, "")
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tc.want {
				t.Fatalf("Allow-Origin = %q, 期望 %q", got, tc.want)
			}
			if tc.want != "" {
				if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
					t.Fatalf("实际请求被拦截: code=%d body=%q", rec.Code, rec.Body.String())
				}
			}
		})
	}
}

func TestCORSCredentialsNeverStar(t *testing.T) {
	t.Run("通配来源加凭证时回显具体来源", func(t *testing.T) {
		r := corsRouter(CORSOptions{AllowOrigins: []string{"*"}, AllowCredentials: true})
		rec := doReq(r, http.MethodGet, "/api", map[string]string{"Origin": "https://app.yd.dev"}, "")
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.yd.dev" {
			t.Fatalf("Allow-Origin = %q, 期望回显 https://app.yd.dev", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Fatalf("Allow-Credentials = %q, 期望 true", got)
		}
	})

	t.Run("子域通配加凭证时同样回显", func(t *testing.T) {
		r := corsRouter(CORSOptions{AllowOrigins: []string{"*.example.com"}, AllowCredentials: true})
		rec := doReq(r, http.MethodGet, "/api", map[string]string{"Origin": "https://api.example.com"}, "")
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://api.example.com" {
			t.Fatalf("Allow-Origin = %q, 期望回显具体来源", got)
		}
	})

	t.Run("无凭证时通配来源可回星号", func(t *testing.T) {
		r := corsRouter(CORSOptions{AllowOrigins: []string{"*"}})
		rec := doReq(r, http.MethodGet, "/api", map[string]string{"Origin": "https://any.example"}, "")
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Fatalf("Allow-Origin = %q, 期望 *", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Fatalf("Allow-Credentials = %q, 期望为空", got)
		}
	})

	t.Run("未带 Origin 时不写任何 CORS 头", func(t *testing.T) {
		r := corsRouter(CORSOptions{AllowOrigins: []string{"*"}, AllowCredentials: true})
		rec := doReq(r, http.MethodGet, "/api", nil, "")
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Allow-Origin = %q, 期望为空", got)
		}
	})
}

func TestCORSPreflightShortCircuits204(t *testing.T) {
	r := corsRouter(CORSOptions{
		AllowOrigins:     []string{"https://app.yd.dev"},
		AllowMethods:     []string{http.MethodGet, http.MethodPost},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           time.Hour,
	})

	rec := doReq(r, http.MethodOptions, "/api", map[string]string{
		"Origin":                        "https://app.yd.dev",
		"Access-Control-Request-Method": http.MethodPost,
	}, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, 期望 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("预检响应体 = %q, 期望为空", rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.yd.dev" {
		t.Errorf("Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST" {
		t.Errorf("Allow-Methods = %q, 期望 GET, POST", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, Authorization" {
		t.Errorf("Allow-Headers = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "3600" {
		t.Errorf("Max-Age = %q, 期望 3600", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, 期望 true", got)
	}
}

// Unmatched origins get no CORS headers at all, and no error. | 未匹配的来源不写任何 CORS 头，也不报错。
func TestCORSUnmatchedOriginWritesNoHeaders(t *testing.T) {
	r := corsRouter(CORSOptions{
		AllowOrigins:     []string{"https://app.yd.dev"},
		AllowMethods:     []string{http.MethodGet},
		AllowHeaders:     []string{"Content-Type"},
		ExposeHeaders:    []string{"X-Total"},
		AllowCredentials: true,
	})

	rec := doReq(r, http.MethodGet, "/api", map[string]string{"Origin": "https://evil.example"}, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("未匹配来源不应被拦截: code=%d body=%q", rec.Code, rec.Body.String())
	}
	for _, h := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Credentials",
		"Access-Control-Expose-Headers",
	} {
		if got := rec.Header().Get(h); got != "" {
			t.Errorf("%s = %q, 期望为空", h, got)
		}
	}
}

// An exact origin also gets Expose-Headers and a Vary entry. | 精确匹配的来源还会带上 Expose-Headers 与 Vary。
func TestCORSExactOriginAndExposeHeaders(t *testing.T) {
	r := corsRouter(CORSOptions{
		AllowOrigins:  []string{"https://app.yd.dev"},
		ExposeHeaders: []string{"X-Total", "X-Page"},
	})

	rec := doReq(r, http.MethodGet, "/api", map[string]string{"Origin": "https://app.yd.dev"}, "")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.yd.dev" {
		t.Fatalf("Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "X-Total, X-Page" {
		t.Fatalf("Expose-Headers = %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q, 期望 Origin", got)
	}
}

// Preflight for an unmatched origin still short-circuits with 204 but without CORS headers. | 未匹配来源的预检仍然短路为 204，但不写 CORS 头。
func TestCORSPreflightUnmatchedOrigin(t *testing.T) {
	r := corsRouter(CORSOptions{AllowOrigins: []string{"https://app.yd.dev"}})

	rec := doReq(r, http.MethodOptions, "/api", map[string]string{
		"Origin":                        "https://evil.example",
		"Access-Control-Request-Method": http.MethodGet,
	}, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, 期望 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, 期望为空", got)
	}
}

// Requests without an Origin header pass through untouched. | 未携带 Origin 头的请求原样透传。
func TestCORSRequestWithoutOriginPassesThrough(t *testing.T) {
	r := corsRouter(CORSOptions{AllowOrigins: []string{"*"}, AllowCredentials: true})
	rec := doReq(r, http.MethodGet, "/api", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// A preflight without Access-Control-Request-Method is treated as a normal request. | 缺少 Access-Control-Request-Method 的 OPTIONS 按普通请求处理。
func TestCORSPlainOptionsIsNotPreflight(t *testing.T) {
	r := corsRouter(CORSOptions{AllowOrigins: []string{"*"}})
	rec := doReq(r, http.MethodOptions, "/api", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "should-not-run" {
		t.Fatalf("code=%d body=%q, 期望处理器照常执行", rec.Code, rec.Body.String())
	}
}
