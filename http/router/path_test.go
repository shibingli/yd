package router

import (
	"net/url"
	"testing"
)

// TestFastEscapedPathMatchesStdlib proves the byte table and the fast path agree with
// net/url on every shape that matters for routing. | TestFastEscapedPathMatchesStdlib 证明字节表与
// 快路径在所有影响路由的形态上都与 net/url 一致。
func TestFastEscapedPathMatchesStdlib(t *testing.T) {
	targets := []string{
		"",
		"/",
		"/users/me",
		"/users/42/posts",
		"/a b",
		"/a%20b",
		"/a%2Fb",
		"/a%3Fb",
		"/a%25b",
		"/中文/路径",
		"/control%00byte",
		"*",
	}
	for _, target := range targets {
		u, err := url.Parse(target)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", target, err)
		}
		if got, want := fastEscapedPath(u), u.EscapedPath(); got != want {
			t.Errorf("fastEscapedPath(%q) = %q, want %q | 快路径与标准库不一致", target, got, want)
		}
	}

	manual := []*url.URL{
		{Path: "/a b", RawPath: "/a%20b"},
		{Path: "/a b", RawPath: "/bad%ZZ"},
		{Path: "/a/b", RawPath: "/a%2Fb"},
		{Path: "*"},
	}
	for _, u := range manual {
		if got, want := fastEscapedPath(u), u.EscapedPath(); got != want {
			t.Errorf("fastEscapedPath(%+v) = %q, want %q | 手工 URL 与标准库不一致", u, got, want)
		}
	}

	if got := fastEscapedPath(nil); got != "" {
		t.Errorf("fastEscapedPath(nil) = %q, want empty | nil URL 应返回空串", got)
	}
}

// BenchmarkFastEscapedPath compares the fast path with url.URL.EscapedPath for a safe path. | BenchmarkFastEscapedPath
// 对比安全路径下快路径与 url.URL.EscapedPath。
func BenchmarkFastEscapedPath(b *testing.B) {
	u := &url.URL{Path: "/users/42/posts"}
	b.Run("fast", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if fastEscapedPath(u) == "" {
				b.Fatal("empty")
			}
		}
	})
	b.Run("stdlib", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if u.EscapedPath() == "" {
				b.Fatal("empty")
			}
		}
	})
}
