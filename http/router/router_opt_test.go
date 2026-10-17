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
	c.reset(nil, nil, r)
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
