package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// patternNodeFor returns the terminal node and compiled shape for method and path. | patternNodeFor
// 返回 method 与 path 对应的终止节点及已编译形状。
func patternNodeFor(t *testing.T, tree *Tree, method, path string) (*node, *patternShape) {
	t.Helper()
	var ps Params
	h, n, _, _ := tree.findKeyNode(newMethodKey(method), path, &ps)
	if h == nil || n == nil {
		t.Fatalf("no terminal node for %s %s | 未找到终止节点", method, path)
	}
	if n.patternShape == nil {
		t.Fatalf("route %s %s has no pattern shape | 路由缺少模式形状", method, path)
	}
	return n, n.patternShape
}

// TestCompilePatternShapePicksFastPath checks the compiled anchors for one-parameter routes. | TestCompilePatternShapePicksFastPath
// 检查单参数路由编译出的锚点。
func TestCompilePatternShapePicksFastPath(t *testing.T) {
	tests := []struct {
		pattern string
		prefix  string
		suffix  string
		param   string
	}{
		{pattern: "/users/:id", prefix: "/users/", suffix: "", param: "id"},
		{pattern: "/users/:id/posts", prefix: "/users/", suffix: "/posts", param: "id"},
		{pattern: "/api/v1/users/:id/posts/latest", prefix: "/api/v1/users/", suffix: "/posts/latest", param: "id"},
		{pattern: "/:id", prefix: "/", suffix: "", param: "id"},
	}
	for _, tc := range tests {
		tree := NewTree()
		if err := tree.Add("GET", tc.pattern, "handler"); err != nil {
			t.Fatalf("Add(%s): %v", tc.pattern, err)
		}
		_, shape := patternNodeFor(t, tree, "GET", replaceParamForTest(tc.pattern))
		if !shape.fast || shape.prefix != tc.prefix || shape.suffix != tc.suffix || shape.param != tc.param {
			t.Errorf("shape for %s = %+v, want prefix=%q suffix=%q param=%q | 模式形状不正确",
				tc.pattern, shape, tc.prefix, tc.suffix, tc.param)
		}
	}
}

// replaceParamForTest turns a route pattern into a concrete path with "42" in its parameter. | replaceParamForTest
// 把路由模式转成参数值为 "42" 的具体路径。
func replaceParamForTest(pattern string) string {
	out := make([]byte, 0, len(pattern)+2)
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == ':' {
			out = append(out, '4', '2')
			for i < len(pattern) && pattern[i] != '/' {
				i++
			}
			if i < len(pattern) {
				out = append(out, pattern[i])
			}
			continue
		}
		out = append(out, pattern[i])
	}
	return string(out)
}

// TestPatternFastMatch covers the single-parameter fast matcher, including all the malformed
// shapes it must reject. | TestPatternFastMatch 覆盖单参数快匹配器，包括它必须拒绝的所有畸形形态。
func TestPatternFastMatch(t *testing.T) {
	tree := NewTree()
	if err := tree.Add("GET", "/users/:id/posts", "handler"); err != nil {
		t.Fatal(err)
	}
	_, shape := patternNodeFor(t, tree, "GET", "/users/42/posts")
	p := &routePattern{shape: shape}
	tests := []struct {
		path  string
		want  bool
		value string
	}{
		{path: "/users/42/posts", want: true, value: "42"},
		{path: "/users/abc/posts", want: true, value: "abc"},
		{path: "/users/42/posts/", want: false},
		{path: "/users//posts", want: false},
		{path: "/users/a/b/posts", want: false},
		{path: "/users/42/post", want: false},
		{path: "/users/42", want: false},
		{path: "/", want: false},
	}
	for _, tc := range tests {
		var ps Params
		if got := p.match(tc.path, &ps); got != tc.want {
			t.Errorf("match(%q) = %v, want %v | 模式匹配结果不正确", tc.path, got, tc.want)
		}
		if tc.want && ps.Get("id") != tc.value {
			t.Errorf("match(%q) id = %q, want %q | 参数捕获不正确", tc.path, ps.Get("id"), tc.value)
		}
		if !tc.want && ps.Len() != 0 {
			t.Errorf("match(%q) leaked params %v | 失败匹配泄漏了参数", tc.path, ps.ps)
		}
	}
}

// TestPatternGeneralMatch covers the multi-parameter segment loop. | TestPatternGeneralMatch
// 覆盖多参数分段循环。
func TestPatternGeneralMatch(t *testing.T) {
	tree := NewTree()
	if err := tree.Add("GET", "/a/:x/b/:y", "handler"); err != nil {
		t.Fatal(err)
	}
	_, shape := patternNodeFor(t, tree, "GET", "/a/1/b/2")
	if shape.fast {
		t.Fatalf("two-parameter shape was marked fast | 双参数形状被错误标记为快路径")
	}
	p := &routePattern{shape: shape}
	var ps Params
	if !p.match("/a/1/b/2", &ps) || ps.Get("x") != "1" || ps.Get("y") != "2" {
		t.Fatalf("general match failed: %v | 通用模式匹配失败", ps.ps)
	}
	ps.Reset()
	if p.match("/a/1/c/2", &ps) || ps.Len() != 0 {
		t.Fatalf("mismatched general path matched or leaked | 不匹配的通用路径命中或泄漏参数")
	}
}

// TestPatternRejectsStaticSibling proves a parameter node with a competing static child is never
// used as a pattern: another value could route to the static route. | TestPatternRejectsStaticSibling
// 证明存在竞争静态子节点的参数节点绝不进入模式缓存：其它值可能路由到静态路由。
func TestPatternRejectsStaticSibling(t *testing.T) {
	tree := NewTree()
	if err := tree.Add("GET", "/users/:id", "param"); err != nil {
		t.Fatal(err)
	}
	if err := tree.Add("GET", "/users/me", "static"); err != nil {
		t.Fatal(err)
	}
	_, shape := patternNodeFor(t, tree, "GET", "/users/42")
	if tree.patternUsable(shape) {
		t.Fatalf("shape with a static sibling was considered safe | 存在静态兄弟的形状被误判为安全")
	}
}

// TestPatternCacheServesAllIDs exercises the router-level pattern path across many values. | TestPatternCacheServesAllIDs
// 在路由器层用多个参数值覆盖模式路径。
func TestPatternCacheServesAllIDs(t *testing.T) {
	r := New()
	r.GET("/users/:id/posts", func(c *Ctx) {
		_ = c.String(http.StatusOK, c.Param("id"))
	})
	for i := 0; i < 20; i++ {
		want := fmt.Sprintf("id-%d", i)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/"+want+"/posts", nil))
		if rec.Body.String() != want {
			t.Fatalf("request %d = %q, want %q | 模式路由参数不正确", i, rec.Body.String(), want)
		}
	}
}

// TestPatternInvalidatedByRouteAdd proves adding a static sibling after a pattern was installed
// disables it without breaking routing. | TestPatternInvalidatedByRouteAdd 证明模式安装后新增
// 静态兄弟会使其失效，同时不影响路由正确性。
func TestPatternInvalidatedByRouteAdd(t *testing.T) {
	r := New()
	r.GET("/u/:id", func(c *Ctx) { _ = c.String(http.StatusOK, "p:"+c.Param("id")) })
	serve := func(path string) string {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Body.String()
	}
	for i := 0; i < 3; i++ {
		if got := serve("/u/42"); got != "p:42" {
			t.Fatalf("before add = %q | 新增前结果不正确", got)
		}
	}
	r.GET("/u/static", func(c *Ctx) { _ = c.String(http.StatusOK, "static") })
	if got := serve("/u/static"); got != "static" {
		t.Fatalf("static after add = %q | 新增静态路由未生效", got)
	}
	if got := serve("/u/7"); got != "p:7" {
		t.Fatalf("param after add = %q | 新增后参数路由不正确", got)
	}
}

// TestPatternHEADFallback proves HEAD requests use the installed GET pattern. | TestPatternHEADFallback
// 证明 HEAD 请求使用已安装的 GET 模式。
func TestPatternHEADFallback(t *testing.T) {
	r := New()
	calls := 0
	r.GET("/h/:id", func(c *Ctx) {
		calls++
		_ = c.String(http.StatusOK, c.Param("id"))
	})
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/h/9", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("HEAD %d status = %d | HEAD 模式回退状态不正确", i, rec.Code)
		}
	}
	if calls != 5 {
		t.Fatalf("GET handler calls = %d, want 5 | HEAD 未复用 GET 处理器", calls)
	}
}

// TestPatternMethodIsolation proves GET and POST patterns do not leak into each other. | TestPatternMethodIsolation
// 证明 GET 与 POST 模式不会互相串用。
func TestPatternMethodIsolation(t *testing.T) {
	r := New()
	r.GET("/m/:id", func(c *Ctx) { _ = c.String(http.StatusOK, "get:"+c.Param("id")) })
	r.POST("/m/:id", func(c *Ctx) { _ = c.String(http.StatusOK, "post:"+c.Param("id")) })
	for i := 0; i < 6; i++ {
		method, want := http.MethodGet, "get:1"
		if i%2 == 1 {
			method, want = http.MethodPost, "post:1"
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, "/m/1", nil))
		if rec.Body.String() != want {
			t.Fatalf("request %d %s = %q, want %q | 模式方法隔离失败", i, method, rec.Body.String(), want)
		}
	}
}

// TestPatternGlobalMiddleware proves a pattern hit still runs middleware added later. | TestPatternGlobalMiddleware
// 证明模式命中仍会执行之后新增的中间件。
func TestPatternGlobalMiddleware(t *testing.T) {
	r := New()
	r.GET("/g/:id", func(c *Ctx) { _ = c.NoContent() })
	serve := func() string {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/g/1", nil))
		return rec.Header().Get("X-MW")
	}
	// Two requests install and hit the pattern; the third is promoted to the exact cache. | 前两次
	// 安装并命中模式，第三次提升到精确缓存。
	for i := 0; i < 3; i++ {
		serve()
	}
	r.Use(func(c *Ctx) {
		c.SetHeader("X-MW", "1")
		c.Next()
	})
	for i := 0; i < 2; i++ {
		if got := serve(); got != "1" {
			t.Fatalf("middleware missing on request %d | 第 %d 次请求缺少中间件", i, i)
		}
	}
}

// TestPatternRawEncodedSlash proves a raw encoded slash stays inside the captured parameter. | TestPatternRawEncodedSlash
// 证明原始编码斜杠保留在被捕获的参数内。
func TestPatternRawEncodedSlash(t *testing.T) {
	r := New()
	r.GET("/r/:id", func(c *Ctx) { _ = c.String(http.StatusOK, c.Param("id")) })
	for i := 0; i < 3; i++ {
		u, err := url.Parse("/r/a%2Fb")
		if err != nil {
			t.Fatal(err)
		}
		req := &http.Request{Method: http.MethodGet, URL: u, RequestURI: "/r/a%2Fb"}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Body.String() != "a%2Fb" {
			t.Fatalf("request %d = %q, want a%%2Fb | 编码斜杠被错误拆分", i, rec.Body.String())
		}
	}
}
