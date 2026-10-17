package router

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

func do(r *Router, method, target string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestDynamicRoutesWithSuffixThroughHTTP(t *testing.T) {
	r := New()
	r.GET("/report/:id-xx.png", func(c *Ctx) {
		_ = c.String(http.StatusOK, c.Param("id"))
	})

	rec := do(r, http.MethodGet, "/report/42-xx.png", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "42" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}

	rec = do(r, http.MethodGet, "/report/42-yy.png", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("非匹配后缀应 404, 实际 %d", rec.Code)
	}
}

func TestMethodNotAllowedHasAllowHeader(t *testing.T) {
	r := New()
	r.GET("/users", func(c *Ctx) { _ = c.NoContent() })

	rec := do(r, http.MethodPost, "/users", nil, "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, 期望 405", rec.Code)
	}
	allow := rec.Header().Get("Allow")
	if !strings.Contains(allow, http.MethodGet) || !strings.Contains(allow, http.MethodOptions) {
		t.Fatalf("Allow = %q, 期望包含 GET 与 OPTIONS", allow)
	}
}

func TestAutoOptionsResponse(t *testing.T) {
	r := New()
	r.GET("/users", func(c *Ctx) { _ = c.NoContent() })
	r.POST("/users", func(c *Ctx) { _ = c.NoContent() })

	rec := do(r, http.MethodOptions, "/users", nil, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, 期望 204", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, http.MethodPost) {
		t.Fatalf("Allow = %q", allow)
	}
}

func TestGroupMiddlewareOrder(t *testing.T) {
	var order []string
	mw := func(tag string) Middleware {
		return func(c *Ctx) {
			order = append(order, tag)
			c.Next()
			order = append(order, tag+":out")
		}
	}

	r := New()
	api := r.Group("/api", mw("outer"))
	v1 := api.Group("/v1", mw("inner"))
	v1.GET("/ping", func(c *Ctx) {
		order = append(order, "handler")
		_ = c.NoContent()
	})

	rec := do(r, http.MethodGet, "/api/v1/ping", nil, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d", rec.Code)
	}
	want := []string{"outer", "inner", "handler", "inner:out", "outer:out"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("中间件顺序 = %v, 期望 %v", order, want)
	}
}

func TestGroupAbortStopsChain(t *testing.T) {
	r := New()
	g := r.Group("/api", func(c *Ctx) {
		_ = c.String(http.StatusUnauthorized, "denied")
		c.Abort()
	})
	g.GET("/secret", func(c *Ctx) { _ = c.String(http.StatusOK, "leaked") })

	rec := do(r, http.MethodGet, "/api/secret", nil, "")
	if rec.Code != http.StatusUnauthorized || rec.Body.String() != "denied" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestTrailingSlashRedirects(t *testing.T) {
	r := New()
	r.GET("/users/", func(c *Ctx) { _ = c.NoContent() })

	rec := do(r, http.MethodGet, "/users", nil, "")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("code = %d, 期望 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/users/" {
		t.Fatalf("Location = %q, 期望 /users/", loc)
	}

	r2 := New()
	r2.POST("/profile", func(c *Ctx) { _ = c.NoContent() })
	rec = do(r2, http.MethodPost, "/profile/", nil, "")
	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("非 GET 方法应使用 308, 实际 %d", rec.Code)
	}
}

func TestCtxPoolResetNoParamLeak(t *testing.T) {
	r := New()
	r.GET("/users/:id", func(c *Ctx) { _ = c.String(http.StatusOK, c.Param("id")) })
	r.GET("/ping", func(c *Ctx) {
		_ = c.String(http.StatusOK, "id="+c.Param("id")+" n="+c.Query("n"))
	})

	if body := do(r, http.MethodGet, "/users/42", nil, "").Body.String(); body != "42" {
		t.Fatalf("首次请求 body = %q", body)
	}
	rec := do(r, http.MethodGet, "/ping?n=7", nil, "")
	if got := rec.Body.String(); got != "id= n=7" {
		t.Fatalf("复用后泄漏了上一次请求的状态: %q", got)
	}
}

func TestStaticFileServedFromAferoFS(t *testing.T) {
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "css/app.css", []byte("body{color:red}"), 0o644); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}

	r := New()
	r.StaticFS("/static", mem)

	rec := do(r, http.MethodGet, "/static/css/app.css", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "body{color:red}" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Last-Modified") == "" {
		t.Error("the static file response is missing Last-Modified | 静态文件响应缺少 Last-Modified")
	}

	rec = do(r, http.MethodGet, "/static/css/missing.css", nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("缺失文件 code = %d, 期望 404", rec.Code)
	}
}

func TestBindJSONFormAndQuery(t *testing.T) {
	type payload struct {
		Name string `json:"name" form:"name"`
		Age  int    `json:"age" form:"age"`
	}

	r := New()
	r.POST("/json", func(c *Ctx) {
		var p payload
		if err := c.Bind(&p); err != nil {
			_ = c.String(http.StatusBadRequest, err.Error())
			return
		}
		_ = c.JSON(http.StatusOK, p)
	})
	r.POST("/form", func(c *Ctx) {
		var p payload
		if err := c.Bind(&p); err != nil {
			_ = c.String(http.StatusBadRequest, err.Error())
			return
		}
		_ = c.JSON(http.StatusOK, p)
	})
	r.GET("/query", func(c *Ctx) {
		var p payload
		if err := c.Bind(&p); err != nil {
			_ = c.String(http.StatusBadRequest, err.Error())
			return
		}
		_ = c.JSON(http.StatusOK, p)
	})

	rec := do(r, http.MethodPost, "/json", map[string]string{"Content-Type": "application/json"}, `{"name":"yd","age":3}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"yd"`) {
		t.Fatalf("JSON 绑定失败: code=%d body=%q", rec.Code, rec.Body.String())
	}

	form := url.Values{"name": {"form-name"}, "age": {"9"}}.Encode()
	rec = do(r, http.MethodPost, "/form", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, form)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"form-name"`) {
		t.Fatalf("表单绑定失败: code=%d body=%q", rec.Code, rec.Body.String())
	}

	rec = do(r, http.MethodGet, "/query?name=q&age=5", nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"q"`) || !strings.Contains(rec.Body.String(), `"age":5`) {
		t.Fatalf("query 绑定失败: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestCustomNotFoundAndPanicHandler(t *testing.T) {
	r := New(
		WithNotFound(func(c *Ctx) { _ = c.String(http.StatusNotFound, "自定义 404") }),
		WithPanicHandler(func(c *Ctx, rec any) { _ = c.String(http.StatusInternalServerError, "panic handled") }),
	)
	r.GET("/boom", func(c *Ctx) { panic("炸了") })

	rec := do(r, http.MethodGet, "/nope", nil, "")
	if rec.Code != http.StatusNotFound || rec.Body.String() != "自定义 404" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}

	rec = do(r, http.MethodGet, "/boom", nil, "")
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "panic handled" {
		t.Fatalf("panic 处理失败: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestRegisterBadPatternIsRecorded 要求非法模式只被登记，不在注册时 panic。
// TestRegisterBadPatternIsRecorded requires an invalid pattern to be recorded rather than panicking at registration.
func TestRegisterBadPatternIsRecorded(t *testing.T) {
	r := New()
	r.GET("bad-pattern", func(c *Ctx) {}) // must not panic | 不得 panic

	if err := r.Validate(); !errors.Is(err, ErrBadPattern) {
		t.Fatalf("Validate() = %v，期望携带 ErrBadPattern", err)
	}
	if rec := do(r, http.MethodGet, "/any", nil, ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("接线错误下 ServeHTTP = %d，期望 500", rec.Code)
	}
}

// TestRegisterNilHandlerIsRecorded 要求 nil 处理器只被登记，不在注册时 panic。
// TestRegisterNilHandlerIsRecorded requires a nil handler to be recorded rather than panicking at registration.
func TestRegisterNilHandlerIsRecorded(t *testing.T) {
	r := New()
	r.GET("/nil", nil) // must not panic | 不得 panic

	if err := r.Validate(); !errors.Is(err, ErrNilHandler) {
		t.Fatalf("Validate() = %v，期望携带 ErrNilHandler", err)
	}
	if rec := do(r, http.MethodGet, "/nil", nil, ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("接线错误下 ServeHTTP = %d，期望 500", rec.Code)
	}
}

// TestDuplicateRouteIsRecorded 要求重复注册被登记并由 Validate 报告。
// TestDuplicateRouteIsRecorded requires a duplicate registration to be recorded and reported by Validate.
func TestDuplicateRouteIsRecorded(t *testing.T) {
	r := New()
	h := func(c *Ctx) {}
	r.GET("/dup", h)
	r.GET("/dup", h) // must not panic | 不得 panic

	if err := r.Validate(); !errors.Is(err, ErrConflict) {
		t.Fatalf("Validate() = %v，期望携带 ErrConflict", err)
	}
	if rec := do(r, http.MethodGet, "/dup", nil, ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("接线错误下 ServeHTTP = %d，期望 500", rec.Code)
	}
}

// TestInvalidChainIsReportedNotPanicked 要求树中的非调用链处理器被 Validate 报告，且 ServeHTTP 返回 500。
// TestInvalidChainIsReportedNotPanicked requires a non-chain handler in the tree to be reported by Validate and answered with 500.
func TestInvalidChainIsReportedNotPanicked(t *testing.T) {
	r := New()
	if err := r.tree.Add(http.MethodGet, "/broken", "not-a-chain"); err != nil {
		t.Fatalf("tree.Add 失败: %v", err)
	}

	if err := r.Validate(); !errors.Is(err, ErrInvalidHandler) {
		t.Fatalf("Validate() = %v，期望携带 ErrInvalidHandler", err)
	}
	if rec := do(r, http.MethodGet, "/broken", nil, ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("内部不变量被违反时 ServeHTTP = %d，期望 500", rec.Code)
	}
}

// TestValidateCleanRouter 要求接线正确的路由器 Validate 返回 nil。
// TestValidateCleanRouter requires Validate to return nil for a correctly wired router.
func TestValidateCleanRouter(t *testing.T) {
	r := New()
	r.GET("/ok", func(c *Ctx) { _ = c.NoContent() })
	r.Use(func(c *Ctx) { c.Next() })
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate() = %v，期望 nil", err)
	}
}

func TestRoutesSnapshotSorted(t *testing.T) {
	r := New()
	r.POST("/b", func(c *Ctx) {})
	r.GET("/b", func(c *Ctx) {})
	r.GET("/a/:id", func(c *Ctx) {})

	routes := r.Routes()
	if len(routes) != 3 {
		t.Fatalf("路由数 = %d, 期望 3", len(routes))
	}
	if routes[0].Pattern != "/a/:id" {
		t.Fatalf("排序错误: %+v", routes)
	}
	if routes[1].Pattern != "/b" || routes[1].Method != http.MethodGet {
		t.Fatalf("排序错误: %+v", routes)
	}
	if routes[2].Pattern != "/b" || routes[2].Method != http.MethodPost {
		t.Fatalf("排序错误: %+v", routes)
	}
}

func TestStreamAndFlush(t *testing.T) {
	r := New()
	r.GET("/stream", func(c *Ctx) {
		_ = c.Stream("text/event-stream", func(w io.Writer) error {
			if _, err := w.Write([]byte("data: hi\n\n")); err != nil {
				return err
			}
			c.Flush()
			return nil
		})
	})

	rec := do(r, http.MethodGet, "/stream", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "data: hi\n\n" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
}
