package router

import "testing"

// newFastTree builds a tree for the non-backtracking matcher tests. | newFastTree 为非回溯匹配器测试构建路由树。
func newFastTree(t *testing.T, routes map[string]string) *Tree {
	t.Helper()
	tree := NewTree()
	for pattern, handler := range routes {
		method, path := splitRoutePattern(t, pattern)
		if err := tree.Add(method, path, handler); err != nil {
			t.Fatalf("Add(%s): %v | 注册路由失败", pattern, err)
		}
	}
	return tree
}

// splitRoutePattern splits "GET /path" into its method and path. | splitRoutePattern 把 "GET /path"
// 拆成方法与路径。
func splitRoutePattern(t *testing.T, pattern string) (string, string) {
	t.Helper()
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == ' ' {
			return pattern[:i], pattern[i+1:]
		}
	}
	t.Fatalf("pattern %q lacks a method | 模式 %q 缺少方法", pattern, pattern)
	return "", ""
}

// TestFastMatcherFallbacks covers the shapes where the fast matcher must delegate to the full
// backtracking matcher, plus the forced param descent it handles itself. | TestFastMatcherFallbacks
// 覆盖快路径必须委托给完整回溯匹配器的形态，以及它自己处理的被迫参数下探。
func TestFastMatcherFallbacks(t *testing.T) {
	tree := newFastTree(t, map[string]string{
		"GET /users/me":  "static-me",
		"GET /users/:id": "param-any",
		"POST /p/:id":    "post-param",
		"GET /p/*rest":   "get-wildcard",
		"GET /m/:id.jpg": "mixed-jpg",
		"GET /m/:name":   "plain",
		"GET /a/b/c":     "chain",
	})

	tests := []struct {
		name      string
		method    string
		path      string
		want      string
		wantParam map[string]string
	}{
		{name: "static", method: "GET", path: "/users/me", want: "static-me"},
		{name: "forced param", method: "GET", path: "/users/42", want: "param-any", wantParam: map[string]string{"id": "42"}},
		{name: "static chain", method: "GET", path: "/a/b/c", want: "chain"},
		{name: "mixed wins", method: "GET", path: "/m/a.jpg", want: "mixed-jpg", wantParam: map[string]string{"id": "a"}},
		{name: "plain fallback", method: "GET", path: "/m/abc", want: "plain", wantParam: map[string]string{"name": "abc"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ps Params
			h, _ := tree.Find(tc.method, tc.path, &ps)
			got, _ := h.(string)
			if got != tc.want {
				t.Fatalf("Find(%s %s) = %q, want %q | 匹配结果不正确", tc.method, tc.path, got, tc.want)
			}
			for key, want := range tc.wantParam {
				if value := ps.Get(key); value != want {
					t.Fatalf("param %q = %q, want %q | 参数捕获不正确", key, value, want)
				}
			}
		})
	}

	// A static child that matches but fails deeper must fall back to the param sibling. | 静态
	// 子节点匹配但更深层失败时，必须回退到参数兄弟。
	tree.Add("GET", "/orgs/me", "org-static")
	tree.Add("GET", "/orgs/:name/x", "param-x")
	var ps Params
	h, _ := tree.Find("GET", "/orgs/me/x", &ps)
	if got, _ := h.(string); got != "param-x" {
		t.Fatalf("fallback = %q, want param-x | 静态分支失败后未回退到参数分支", got)
	}
	if value := ps.Get("name"); value != "me" {
		t.Fatalf("fallback param = %q, want me | 回退后的参数捕获不正确", value)
	}

	// A method mismatch on the forced param branch must not hide the wildcard method set. | 被迫
	// 参数分支上的方法不匹配不得掩盖通配方法的集合。
	allowed := tree.Allowed("/p/abc")
	if !containsString(allowed, "GET") || !containsString(allowed, "POST") {
		t.Fatalf("Allowed(/p/abc) = %v, want GET and POST | 通配与参数方法集合不完整", allowed)
	}
}

// TestFastMatcherKeepsParamsOnFailure proves a failed match leaves pre-existing captures alone. | TestFastMatcherKeepsParamsOnFailure
// 证明匹配失败不会改动已有的参数捕获。
func TestFastMatcherKeepsParamsOnFailure(t *testing.T) {
	tree := newFastTree(t, map[string]string{
		"GET /users/me":    "static",
		"GET /users/:id/x": "param",
	})
	var ps Params
	ps.Append("before", "kept")
	if h, _ := tree.Find("GET", "/users/42", &ps); h != nil {
		t.Fatalf("unexpected match %v | 不应命中", h)
	}
	if ps.Len() != 1 || ps.Get("before") != "kept" {
		t.Fatalf("params after failed match = %v | 失败匹配破坏了已有参数", ps.ps)
	}
}

// TestFastMatcherTrailingSlash proves the fast path does not swallow a trailing slash. | TestFastMatcherTrailingSlash
// 证明快路径不会吞掉尾斜杠。
func TestFastMatcherTrailingSlash(t *testing.T) {
	tree := newFastTree(t, map[string]string{"GET /a/b": "chain"})
	h, tsr := tree.Find("GET", "/a/b/", &Params{})
	if h != nil {
		t.Fatalf("trailing slash matched %v | 尾斜杠不应命中", h)
	}
	if !tsr {
		t.Fatalf("trailing slash did not report TSR | 尾斜杠未报告重定向")
	}
}

// containsString reports whether values contains want. | containsString 报告 values 是否包含 want。
func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
