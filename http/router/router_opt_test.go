package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBorrowedChainIsDroppedOnReset protects the borrowed route-chain fast path: the slice
// belongs to the tree and must never become an append target for a later request. | TestBorrowedChainIsDroppedOnReset
// 保护借用的路由链快路径：该切片属于路由树，绝不能成为后续请求的追加目标。
func TestBorrowedChainIsDroppedOnReset(t *testing.T) {
	r := New()
	chain := []HandlerFunc{func(*Ctx) {}}
	c := &Ctx{}
	r.setChain(c, nil, chain, nil)
	if !c.borrowed || len(c.handlers) != 1 || &c.handlers[0] != &chain[0] {
		t.Fatalf("chain was not borrowed | 未按借用方式设置调用链: %+v", c.handlers)
	}
	c.reset(nil, nil)
	if c.borrowed || c.handlers != nil {
		t.Fatalf("borrowed chain survived reset | 借用的调用链在 reset 后仍然存在")
	}

	mw := []Middleware{func(*Ctx) {}, func(*Ctx) {}}
	r.setChain(c, mw, chain, nil)
	if len(c.handlers) != 3 {
		t.Fatalf("chain length = %d, want 3 | 调用链长度应为 3", len(c.handlers))
	}
	if len(chain) != 1 || chain[0] == nil {
		t.Fatalf("tree chain was corrupted | 路由树调用链被覆写: %+v", chain)
	}
}

// TestCachedParamsAreCopyOnWrite proves a handler that appends to Params cannot corrupt the
// cached captures seen by later requests. | TestCachedParamsAreCopyOnWrite 证明处理器向 Params
// 追加参数不会破坏后续请求看到的缓存捕获。
func TestCachedParamsAreCopyOnWrite(t *testing.T) {
	r := New()
	r.GET("/u/:id", func(c *Ctx) {
		initial := c.Params.Len()
		id := c.Param("id")
		c.Params.Append("extra", "x")
		_ = c.String(http.StatusOK, fmt.Sprintf("%s/%d", id, initial))
	})
	get := func(path string) string {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Body.String()
	}
	if got := get("/u/1"); got != "1/1" {
		t.Fatalf("first request = %q, want 1/1 | 首次请求结果不正确", got)
	}
	if got := get("/u/2"); got != "2/1" {
		t.Fatalf("second request = %q, want 2/1 | 缓存参数被处理器污染", got)
	}
}

// TestRouteCacheInvalidatedByAdd proves that adding a route after a cached match invalidates the
// entry, so a newly registered static route immediately takes priority. | TestRouteCacheInvalidatedByAdd
// 证明缓存命中后新增路由会使缓存失效，新注册的静态路由立即获得优先级。
func TestRouteCacheInvalidatedByAdd(t *testing.T) {
	r := New()
	r.GET("/users/:id", func(c *Ctx) { _ = c.String(http.StatusOK, "param") })
	serve := func() string {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/me", nil))
		return rec.Body.String()
	}
	if got := serve(); got != "param" {
		t.Fatalf("before add = %q, want param | 新增前应命中参数路由", got)
	}
	r.GET("/users/me", func(c *Ctx) { _ = c.String(http.StatusOK, "static") })
	if got := serve(); got != "static" {
		t.Fatalf("after add = %q, want static | 新增静态路由后缓存未失效", got)
	}
}

// TestCachedRouteKeepsGlobalMiddleware proves a cached route chain still runs middleware added
// after the entry was cached. | TestCachedRouteKeepsGlobalMiddleware 证明缓存的路由链仍会执行
// 缓存之后新增的全局中间件。
func TestCachedRouteKeepsGlobalMiddleware(t *testing.T) {
	r := New()
	r.GET("/cached", func(c *Ctx) { _ = c.NoContent() })
	serve := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cached", nil))
		return rec
	}
	if rec := serve(); rec.Header().Get("X-MW") != "" {
		t.Fatalf("middleware ran before registration | 中间件在注册前执行")
	}
	r.Use(func(c *Ctx) {
		c.SetHeader("X-MW", "1")
		c.Next()
	})
	if rec := serve(); rec.Header().Get("X-MW") != "1" {
		t.Fatalf("cached route skipped global middleware | 缓存路由跳过了全局中间件")
	}
}

// TestCachedHeadFallsBackToGet proves HEAD uses the cached GET chain. | TestCachedHeadFallsBackToGet
// 证明 HEAD 使用缓存的 GET 调用链。
func TestCachedHeadFallsBackToGet(t *testing.T) {
	r := New()
	calls := 0
	r.GET("/head", func(c *Ctx) {
		calls++
		_ = c.String(http.StatusOK, "get")
	})
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/head", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("HEAD request %d = %d | HEAD 回退结果不正确", i, rec.Code)
		}
	}
	if calls != 3 {
		t.Fatalf("GET handler calls = %d, want 3 | HEAD 缓存未命中 GET 链", calls)
	}
}

// TestCacheIsMethodScoped proves a cached GET entry is never reused for another method. | TestCacheIsMethodScoped
// 证明缓存的 GET 条目不会被其它方法复用。
func TestCacheIsMethodScoped(t *testing.T) {
	r := New()
	r.GET("/m/:id", func(c *Ctx) { _ = c.String(http.StatusOK, "get:"+c.Param("id")) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/m/1", nil))
	if rec.Body.String() != "get:1" {
		t.Fatalf("GET body = %q | GET 结果不正确", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/m/1", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /m/1 = %d, want 405 | 缓存的 GET 条目被错误复用", rec.Code)
	}
}

// TestCacheSupportsCustomMethods proves non-standard methods round-trip through the cache by name. | TestCacheSupportsCustomMethods
// 证明非标准方法通过名称在缓存中正确往返。
func TestCacheSupportsCustomMethods(t *testing.T) {
	r := New()
	r.Handle("PROPFIND", "/dav/:name", func(c *Ctx) { _ = c.String(http.StatusOK, "dav:"+c.Param("name")) })
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("PROPFIND", "/dav/file", nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "dav:file" {
			t.Fatalf("PROPFIND request %d = %d %q | 自定义方法缓存失败", i, rec.Code, rec.Body.String())
		}
	}
}

// TestCachedNotFoundUsesCustomHandler proves a cached structural 404 still runs the custom
// NotFound handler. | TestCachedNotFoundUsesCustomHandler 证明缓存的结构性 404 仍会执行自定义
// NotFound 处理器。
func TestCachedNotFoundUsesCustomHandler(t *testing.T) {
	r := New(WithNotFound(func(c *Ctx) { _ = c.String(http.StatusNotFound, "custom") }))
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))
		if rec.Code != http.StatusNotFound || rec.Body.String() != "custom" {
			t.Fatalf("request %d = %d %q | 自定义 404 未生效", i, rec.Code, rec.Body.String())
		}
	}
}

// TestMethodNotAllowedOnRoot protects the structural-miss shortcut: "/" registered for GET must
// still answer POST with 405, not 404. | TestMethodNotAllowedOnRoot 保护结构性未命中短路：根路径
// 注册了 GET 时，POST 仍须返回 405 而不是 404。
func TestMethodNotAllowedOnRoot(t *testing.T) {
	r := New()
	r.GET("/", func(c *Ctx) { _ = c.NoContent() })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / = %d, want 405 | 根路径未注册方法应返回 405", rec.Code)
	}
}

// TestSingleHandlerNextDoesNotReenter protects the fast path's index semantics: Next from the
// only handler must end the chain, exactly as it did through the generic loop. | TestSingleHandlerNextDoesNotReenter
// 保护快路径的索引语义：唯一处理器内部调用 Next 必须结束调用链，与通用循环一致。
func TestSingleHandlerNextDoesNotReenter(t *testing.T) {
	r := New()
	calls := 0
	r.GET("/one", func(c *Ctx) {
		calls++
		c.Next()
		_ = c.NoContent()
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/one", nil))
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1 | 单处理器被重复调用", calls)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 | 状态码不正确", rec.Code)
	}
}

// TestStaticChildrenMapFallback exercises the switch from the linear static-child slice to the
// map used above staticSliceLimit. | TestStaticChildrenMapFallback 覆盖静态子节点从线性切片切换到
// 映射的阈值路径。
func TestStaticChildrenMapFallback(t *testing.T) {
	r := New()
	const routes = 24
	for i := 0; i < routes; i++ {
		i := i
		r.GET(fmt.Sprintf("/p%d", i), func(c *Ctx) {
			_ = c.String(http.StatusOK, fmt.Sprintf("p%d", i))
		})
	}
	for i := 0; i < routes; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/p%d", i), nil))
		if rec.Code != http.StatusOK || rec.Body.String() != fmt.Sprintf("p%d", i) {
			t.Fatalf("route p%d = %d %q | 第 %d 条路由匹配错误", i, rec.Code, rec.Body.String(), i)
		}
	}
}

// TestMultipleMethodsPerNode exercises the method slice at a terminal node. | TestMultipleMethodsPerNode
// 覆盖同一个终止节点上的多方法切片。
func TestMultipleMethodsPerNode(t *testing.T) {
	r := New()
	r.GET("/multi", func(c *Ctx) { _ = c.String(http.StatusOK, "get") })
	r.POST("/multi", func(c *Ctx) { _ = c.String(http.StatusOK, "post") })
	r.PUT("/multi", func(c *Ctx) { _ = c.String(http.StatusOK, "put") })

	for _, tc := range []struct {
		method string
		want   string
	}{
		{http.MethodGet, "get"},
		{http.MethodPost, "post"},
		{http.MethodPut, "put"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(tc.method, "/multi", nil))
		if rec.Body.String() != tc.want {
			t.Fatalf("%s /multi = %q, want %q | 方法匹配错误", tc.method, rec.Body.String(), tc.want)
		}
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/multi", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /multi = %d, want 405 | 未注册方法应返回 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow == "" {
		t.Fatalf("405 response lacks Allow | 405 响应缺少 Allow 头")
	}
}

// TestCachedParamsSurviveFollowingMisses proves a miss after a cached hit cannot overwrite the
// cached captures through the parameter buffer reused by reset. | TestCachedParamsSurviveFollowingMisses
// 证明缓存命中之后的未命中请求不会通过 reset 复用的参数缓冲覆写缓存捕获。
func TestCachedParamsSurviveFollowingMisses(t *testing.T) {
	r := New()
	r.GET("/u/:id", func(c *Ctx) { _ = c.String(http.StatusOK, c.Param("id")) })
	serve := func(path string) string {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Body.String()
	}
	// Warm the exact entry: tree, pattern, pattern promotion, exact hit. | 预热精确缓存项：树、
	// 模式、模式提升、精确命中。
	for i := 0; i < 4; i++ {
		if got := serve("/u/1"); got != "1" {
			t.Fatalf("warm request %d = %q | 预热请求结果不正确", i, got)
		}
	}
	// This miss shares the context; before the fix it wrote id=2 into the cached array. | 该未命中
	// 复用同一上下文；修复前会把 id=2 写进缓存数组。
	if got := serve("/u/2"); got != "2" {
		t.Fatalf("interleaved miss = %q | 交错的未命中结果不正确", got)
	}
	if got := serve("/u/1"); got != "1" {
		t.Fatalf("cached params were corrupted: %q | 缓存参数被覆写", got)
	}
}

// TestCachedParamsResetStaysCopyOnWrite proves Reset on a borrowed cache view keeps the shared
// mark, so a later request cannot write through the cache's backing array. | TestCachedParamsResetStaysCopyOnWrite
// 证明借用缓存视图上的 Reset 会保留共享标记，后续请求因此无法写穿缓存底层数组。
func TestCachedParamsResetStaysCopyOnWrite(t *testing.T) {
	r := New()
	r.GET("/v/:id", func(c *Ctx) {
		id := c.Param("id")
		c.Params.Reset()
		_ = c.String(http.StatusOK, id)
	})
	serve := func(path string) string {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Body.String()
	}
	for i := 0; i < 4; i++ {
		if got := serve("/v/1"); got != "1" {
			t.Fatalf("warm request %d = %q | 预热请求结果不正确", i, got)
		}
	}
	if got := serve("/v/2"); got != "2" {
		t.Fatalf("interleaved miss = %q | 交错的未命中结果不正确", got)
	}
	if got := serve("/v/1"); got != "1" {
		t.Fatalf("cached params were corrupted after Reset: %q | Reset 后缓存参数被覆写", got)
	}
}
