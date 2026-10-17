package router

import (
	"testing"
)

// patternBenchPath is the concrete path matched by the pattern benchmarks. | patternBenchPath 是模式基准测试匹配的具体路径。
const patternBenchPath = "/users/1234/posts"

// BenchmarkPatternMatch measures the compiled pattern matcher in isolation. | BenchmarkPatternMatch
// 单独测量已编译模式匹配器。
func BenchmarkPatternMatch(b *testing.B) {
	p := &routePattern{shape: &patternShape{
		segments: []patternSegment{
			{literal: "users"},
			{name: "id", param: true},
			{literal: "posts"},
		},
		prefix: "/users/",
		suffix: "/posts",
		param:  "id",
		fast:   true,
	}}
	var ps Params
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ps.Reset()
		if !p.match(patternBenchPath, &ps) {
			b.Fatal("pattern did not match | 模式未命中")
		}
	}
}

// BenchmarkTreeFastMatch measures the tree matcher for the same route shape. | BenchmarkTreeFastMatch
// 测量相同路由形状下的树匹配器。
func BenchmarkTreeFastMatch(b *testing.B) {
	tree := NewTree()
	if err := tree.Add("GET", "/users/:id/posts", "handler"); err != nil {
		b.Fatal(err)
	}
	var ps Params
	key := newMethodKey("GET")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ps.Reset()
		if h, _, _, _ := tree.findKeyNode(key, patternBenchPath, &ps); h == nil {
			b.Fatal("tree did not match | 路由树未命中")
		}
	}
}
