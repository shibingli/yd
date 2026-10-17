package router

import (
	"net/url"
	"testing"
)

// TestRequestPathSemantics pins the routing path rule: a non-empty RawPath wins so encoded
// slashes stay inside one segment, otherwise the decoded Path is used. | TestRequestPathSemantics
// 固化路由路径规则：非空 RawPath 优先，使编码斜杠保留在同一分段内；否则使用解码后的 Path。
func TestRequestPathSemantics(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{target: "/users/me", want: "/users/me"},
		{target: "/users/42/posts", want: "/users/42/posts"},
		{target: "/a b", want: "/a b"},
		{target: "/a%20b", want: "/a b"},
		{target: "/a%2Fb", want: "/a%2Fb"},
		{target: "/a%3Fb", want: "/a?b"},
		{target: "/中文/路径", want: "/中文/路径"},
		{target: "*", want: "*"},
	}
	for _, tc := range tests {
		u, err := url.Parse(tc.target)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", tc.target, err)
		}
		if got := requestPath(u); got != tc.want {
			t.Errorf("requestPath(%q) = %q, want %q | 路由路径规则不正确", tc.target, got, tc.want)
		}
	}

	manual := []*url.URL{
		{Path: "/a b", RawPath: "/a%20b"},
		{Path: "/a/b", RawPath: "/a%2Fb"},
	}
	for _, u := range manual {
		if got, want := requestPath(u), u.RawPath; got != want {
			t.Errorf("requestPath(%+v) = %q, want RawPath %q | RawPath 应优先", u, got, want)
		}
	}
	if got := requestPath(nil); got != "" {
		t.Errorf("requestPath(nil) = %q, want empty | nil URL 应返回空串", got)
	}
}

// BenchmarkRequestPath compares the routing path rule with url.URL.EscapedPath for a safe path. | BenchmarkRequestPath
// 对比安全路径下路由路径规则与 url.URL.EscapedPath。
func BenchmarkRequestPath(b *testing.B) {
	u := &url.URL{Path: "/users/42/posts"}
	b.Run("requestPath", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if requestPath(u) == "" {
				b.Fatal("empty")
			}
		}
	})
	b.Run("escapedPath", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if u.EscapedPath() == "" {
				b.Fatal("empty")
			}
		}
	})
}
