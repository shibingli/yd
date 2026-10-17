package router

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRawStaticHandler exercises the context-free static fast path. | TestRawStaticHandler 覆盖
// 无上下文静态快路径。
func TestRawStaticHandler(t *testing.T) {
	r := New()
	r.GETRaw("/raw/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Raw", "1")
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/raw/ping", nil))
	if rec.Code != http.StatusNoContent || rec.Header().Get("X-Raw") != "1" {
		t.Fatalf("raw response = %d %q | 无上下文路由响应不正确", rec.Code, rec.Header().Get("X-Raw"))
	}
	if len(rec.Body.Bytes()) != 0 {
		t.Fatalf("raw body = %q, want empty | 无上下文路由不应写正文", rec.Body.String())
	}
}

// TestRawRootRoute covers a raw route registered on "/". | TestRawRootRoute 覆盖注册在 "/" 上的
// 无上下文路由。
func TestRawRootRoute(t *testing.T) {
	r := New()
	r.GETRaw("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("root"))
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Body.String() != "root" {
		t.Fatalf("root raw body = %q | 根无上下文路由结果不正确", rec.Body.String())
	}
}

// TestRawHeadFallsBackToGet proves HEAD uses the GET raw handler. | TestRawHeadFallsBackToGet
// 证明 HEAD 复用 GET 无上下文处理器。
func TestRawHeadFallsBackToGet(t *testing.T) {
	r := New()
	calls := 0
	r.GETRaw("/raw/head", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("X-Head", "1")
		w.WriteHeader(http.StatusNoContent)
	})
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/raw/head", nil))
		if rec.Code != http.StatusNoContent || rec.Header().Get("X-Head") != "1" {
			t.Fatalf("HEAD %d = %d | HEAD 无上下文回退失败", i, rec.Code)
		}
	}
	if calls != 3 {
		t.Fatalf("raw GET calls = %d, want 3 | HEAD 未复用 GET 无上下文处理器", calls)
	}
}

// TestRawGlobalMiddlewareFallsBack proves global middleware disables the shortcut but still runs
// through the normal context path. | TestRawGlobalMiddlewareFallsBack 证明全局中间件会禁用捷径，
// 但仍通过正常上下文路径运行。
func TestRawGlobalMiddlewareFallsBack(t *testing.T) {
	r := New()
	r.GETRaw("/raw/mw", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	r.Use(func(c *Ctx) {
		c.SetHeader("X-MW", "1")
		c.Next()
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/raw/mw", nil))
	if rec.Code != http.StatusNoContent || rec.Header().Get("X-MW") != "1" {
		t.Fatalf("middleware fallback = %d %q | 无上下文路由未回退到中间件路径", rec.Code, rec.Header().Get("X-MW"))
	}
}

// TestRawPanicHandlerFallsBack proves a PanicHandler disables the shortcut and receives panics. | TestRawPanicHandlerFallsBack
// 证明 PanicHandler 会禁用捷径并收到 panic。
func TestRawPanicHandlerFallsBack(t *testing.T) {
	var caught any
	r := New(WithPanicHandler(func(c *Ctx, rec any) {
		caught = rec
		_ = c.String(http.StatusInternalServerError, "caught")
	}))
	r.GETRaw("/raw/panic", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/raw/panic", nil))
	if caught == nil || rec.Code != http.StatusInternalServerError || rec.Body.String() != "caught" {
		t.Fatalf("panic fallback = %v %d %q | 无上下文路由未回退到 panic 处理器", caught, rec.Code, rec.Body.String())
	}
}

// TestRawMethodNotAllowed proves a method mismatch reaches the normal 405 logic. | TestRawMethodNotAllowed
// 证明方法不匹配会走正常的 405 逻辑。
func TestRawMethodNotAllowed(t *testing.T) {
	r := New()
	r.GETRaw("/raw/only-get", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/raw/only-get", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST raw route = %d, want 405 | 无上下文路由方法不匹配应返回 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow == "" {
		t.Fatalf("405 lacks Allow | 405 缺少 Allow 头")
	}
}

// TestRawTrailingSlashRedirect proves the raw walk does not swallow a trailing slash. | TestRawTrailingSlashRedirect
// 证明无上下文遍历不会吞掉尾斜杠。
func TestRawTrailingSlashRedirect(t *testing.T) {
	r := New()
	r.GETRaw("/raw/slash", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/raw/slash/", nil))
	if rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusPermanentRedirect && rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("trailing slash = %d, want redirect | 尾斜杠应重定向", rec.Code)
	}
}

// TestRawRejectsDynamicPattern proves a raw route cannot be registered on a parameterized path. | TestRawRejectsDynamicPattern
// 证明无上下文路由不能注册到参数化路径上。
func TestRawRejectsDynamicPattern(t *testing.T) {
	r := New()
	r.GETRaw("/raw/:id", func(http.ResponseWriter, *http.Request) {})
	if err := r.Validate(); !errors.Is(err, ErrRawPatternNotStatic) {
		t.Fatalf("Validate() = %v, want ErrRawPatternNotStatic | 动态模式未被拒绝", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/raw/1", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("broken router = %d, want 500 | 损坏路由表应以 500 拒绝", rec.Code)
	}
}

// TestRawNilHandler proves a nil raw handler is a wiring error. | TestRawNilHandler 证明 nil
// 无上下文处理器属于接线错误。
func TestRawNilHandler(t *testing.T) {
	r := New()
	r.GETRaw("/raw/nil", nil)
	if err := r.Validate(); !errors.Is(err, ErrNilHandler) {
		t.Fatalf("Validate() = %v, want ErrNilHandler | nil 处理器未被拒绝", err)
	}
}

// TestRawCoexistsWithParameterizedRoutes proves the raw walk falls through to the tree for
// parameterized paths and does not disturb them. | TestRawCoexistsWithParameterizedRoutes 证明
// 无上下文遍历对参数化路径会回落至路由树，且不影响其正常工作。
func TestRawCoexistsWithParameterizedRoutes(t *testing.T) {
	r := New()
	r.GETRaw("/static/raw", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("raw"))
	})
	r.GET("/users/:id", func(c *Ctx) { _ = c.String(http.StatusOK, "id:"+c.Param("id")) })
	serve := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := serve("/static/raw"); code != http.StatusOK || body != "raw" {
		t.Fatalf("raw route = %d %q | 无上下文路由结果不正确", code, body)
	}
	if code, body := serve("/users/42"); code != http.StatusOK || body != "id:42" {
		t.Fatalf("param route = %d %q | 参数路由受到无上下文快路径影响", code, body)
	}
	if code, body := serve("/users/none"); code != http.StatusOK || body != "id:none" {
		t.Fatalf("param fallback = %d %q | 参数回退结果不正确", code, body)
	}
}

// TestRawCustomMethod proves a non-standard method round-trips through name comparison. | TestRawCustomMethod
// 证明非标准方法通过名称比较正确往返。
func TestRawCustomMethod(t *testing.T) {
	r := New()
	r.HandleRaw("PROPFIND", "/dav/raw", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PROPFIND", "/dav/raw", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PROPFIND raw = %d | 自定义方法无上下文路由失败", rec.Code)
	}
}

// TestRawRoutesAreListed proves raw routes appear in the route snapshot. | TestRawRoutesAreListed
// 证明无上下文路由会出现在路由快照中。
func TestRawRoutesAreListed(t *testing.T) {
	r := New()
	r.GETRaw("/raw/listed", func(http.ResponseWriter, *http.Request) {})
	routes := r.Routes()
	if len(routes) != 1 || routes[0].Method != http.MethodGet || routes[0].Handler == nil {
		t.Fatalf("routes = %+v | 无上下文路由未列入快照", routes)
	}
}

// TestRawLateRegistration proves a raw route registered after serving is published into the
// immutable lookup table without disturbing existing routes. | TestRawLateRegistration 证明服务开始后
// 注册的无上下文路由会发布进不可变查找表，且不影响既有路由。
func TestRawLateRegistration(t *testing.T) {
	r := New()
	r.GETRaw("/raw/first", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("first"))
	})
	serve := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := serve("/raw/first"); code != http.StatusOK || body != "first" {
		t.Fatalf("first = %d %q | 首条无上下文路由不正确", code, body)
	}
	r.GETRaw("/raw/second", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("second"))
	})
	if code, body := serve("/raw/second"); code != http.StatusOK || body != "second" {
		t.Fatalf("second = %d %q | 后注册的无上下文路由未发布", code, body)
	}
	if code, body := serve("/raw/first"); code != http.StatusOK || body != "first" {
		t.Fatalf("first after add = %d %q | 既有无上下文路由受到后注册影响", code, body)
	}
}

// TestRawMultipleMethodsSamePath exercises open addressing with two methods on one path. | TestRawMultipleMethodsSamePath
// 覆盖同一路径上两个方法的开放寻址。
func TestRawMultipleMethodsSamePath(t *testing.T) {
	r := New()
	r.HandleRaw(http.MethodGet, "/raw/multi", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("get"))
	})
	r.HandleRaw(http.MethodPost, "/raw/multi", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("post"))
	})
	serve := func(method string) (int, string) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, "/raw/multi", nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := serve(http.MethodGet); code != http.StatusOK || body != "get" {
		t.Fatalf("GET raw = %d %q | GET 无上下文方法结果不正确", code, body)
	}
	if code, body := serve(http.MethodPost); code != http.StatusOK || body != "post" {
		t.Fatalf("POST raw = %d %q | POST 无上下文方法结果不正确", code, body)
	}
	if code, _ := serve(http.MethodPut); code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT raw = %d, want 405 | 未注册的无上下文方法应返回 405", code)
	}
}

// TestRawBodyLimit proves a body-carrying raw route still enforces MaxBodySize. | TestRawBodyLimit
// 证明带正文的无上下文路由仍然执行 MaxBodySize 限制。
func TestRawBodyLimit(t *testing.T) {
	r := New(WithMaxBodySize(4))
	var (
		got    []byte
		gotErr error
	)
	r.HandleRaw(http.MethodPost, "/raw/body", func(w http.ResponseWriter, req *http.Request) {
		got, gotErr = io.ReadAll(req.Body)
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/raw/body", strings.NewReader("0123456789"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var maxErr *http.MaxBytesError
	if !errors.As(gotErr, &maxErr) || len(got) > 4 {
		t.Fatalf("raw body = %q err=%v, want MaxBytesError with <=4 bytes | 无上下文路由未执行正文上限", got, gotErr)
	}
}
