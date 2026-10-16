package router

import (
	"sort"
	"strings"

	"github.com/yun-dev/yd/errx"
)

// node is a single node of the radix tree; each node corresponds to one segment in the path. | node 是基数树的单个节点，每个节点对应路径中的一个分段。
//
// Child nodes are ordered by match priority: static > mixed (more literals first) > pure param > wildcard. | 子节点按匹配优先级排列：静态 > 混合（字面量越多越优先）> 纯参数 > 通配。
type node struct {
	seg      *Segment
	static   map[string]*node
	mixed    []*node
	param    *node
	wildcard *node

	handlers map[string]any
	pattern  string
}

func newNode(seg *Segment) *node { return &node{seg: seg} }

// child returns the child node corresponding to seg (creating it when necessary). | child 返回（必要时创建）与 seg 对应的子节点。
func (n *node) child(seg Segment) *node {
	switch seg.Kind {
	case SegStatic:
		if n.static == nil {
			n.static = make(map[string]*node, 4)
		}
		if c, ok := n.static[seg.Literal]; ok {
			return c
		}
		c := newNode(&seg)
		n.static[seg.Literal] = c
		return c
	case SegParam:
		if n.param == nil {
			n.param = newNode(&seg)
		}
		return n.param
	case SegWildcard:
		if n.wildcard == nil {
			n.wildcard = newNode(&seg)
		}
		return n.wildcard
	case SegMixed:
		key := segKey(seg)
		for _, c := range n.mixed {
			if segKey(*c.seg) == key {
				return c
			}
		}
		c := newNode(&seg)
		n.mixed = append(n.mixed, c)
		// the more literals, the more specific, the earlier it is tried; equal lengths keep registration order (stable sort) | 字面量越多越具体，越靠前尝试；同长度保持注册顺序（稳定排序）
		sort.SliceStable(n.mixed, func(i, j int) bool {
			return n.mixed[i].seg.LiteralLen > n.mixed[j].seg.LiteralLen
		})
		return c
	default:
		panic("router: 未知的段类型")
	}
}

// Tree is the matching core of the self-developed radix tree, generic over the handler type (handlers are carried as any). | Tree 是自研基数树的匹配内核，泛化于处理器类型（handler 以 any 承载）。
//
// Matching does not modify the tree structure and can be called concurrently by any number of goroutines. | 匹配过程不修改树结构，可被任意多个 goroutine 并发调用。
type Tree struct {
	root *node
}

// NewTree creates an empty route tree. | NewTree 创建一个空的路由树。
func NewTree() *Tree {
	return &Tree{root: &node{}}
}

// Add registers a route. | Add 注册一条路由。
//
// Registering the same pattern twice under one method returns ErrConflict; a pattern syntax error returns an ErrBadPattern-family error. | 同一方法下重复注册相同模式返回 ErrConflict；模式语法错误返回 ErrBadPattern 系列错误。
func (t *Tree) Add(method, pattern string, handler any) error {
	segs, err := ParsePattern(pattern)
	if err != nil {
		return err
	}
	n := t.root
	for _, seg := range segs {
		n = n.child(seg)
	}
	if n.handlers == nil {
		n.handlers = make(map[string]any, 4)
	}
	if _, dup := n.handlers[method]; dup {
		return errx.Detail(ErrConflict, "%s %s", method, pattern)
	}
	n.handlers[method] = handler
	n.pattern = pattern
	return nil
}

// Find looks up the handler matching method and path. | Find 查找匹配 method 与 path 的处理器。
//
// Captured path parameters are appended to ps. A second return value of true means a "trailing slash redirect": | 捕获到的路径参数追加写入 ps。返回的第二个值为 true 表示「尾斜杠重定向」：
// the current path did not match, but adding or removing the trailing "/" would. | 当前路径未命中，但对端加上或去掉末尾 "/" 后可命中。
//
// This function performs no heap allocation on static/param/wildcard segments; path segments are raw string slices | 该函数在静态/参数/通配段上不产生堆分配；路径分段以原始字符串切片表示，
// and are not percent-decoded (for example /a%2Fb.jpg captures "a%2Fb"). | 不做百分号解码（例如 /a%2Fb.jpg 捕获到的是 "a%2Fb"）。
func (t *Tree) Find(method, path string, ps *Params) (any, bool) {
	if h, ok := t.lookup(method, path, ps); ok {
		return h, false
	}
	return nil, t.tsr(method, path)
}

// lookup ignores the trailing slash redirect and performs the match directly. | lookup 忽略尾斜杠重定向，直接执行匹配。
func (t *Tree) lookup(method, path string, ps *Params) (any, bool) {
	if path == "" || path[0] != '/' {
		return nil, false
	}
	if path == "/" {
		return t.root.handlers[method], t.root.handlers[method] != nil
	}
	return matchNode(t.root, path, 1, method, ps)
}

// matchNode matches the segment of path that starts at start, beginning from node n. | matchNode 从节点 n 开始匹配 path 中起始于 start 的那个分段。
//
// start is the index of the current segment's first character in path; segments are separated by '/', and the trailing empty segment | start 为当前分段首字符在 path 中的下标；分段以 '/' 分隔，末尾空段
// (a path ending with "/") is represented as a segment with an empty value. | （路径以 "/" 结尾）会被表示为一个空值分段。
func matchNode(n *node, path string, start int, method string, ps *Params) (any, bool) {
	end := start
	for end < len(path) && path[end] != '/' {
		end++
	}
	val := path[start:end] // the substring shares the backing array, so no allocation | 子串共享底层数组，不产生分配
	last := end == len(path)

	// 1) static segment: an exact match comes first | 1) 静态段：完全匹配优先
	if c, ok := n.static[val]; ok {
		if h, ok := descend(c, path, end, last, method, ps); ok {
			return h, true
		}
	}

	// 2) mixed segment: the more specific the literal anchor, the higher the priority | 2) 混合段：字面量锚点越具体越优先
	for _, c := range n.mixed {
		mark := ps.Len()
		if matchSegmentInto(c.seg, val, ps) {
			if h, ok := descend(c, path, end, last, method, ps); ok {
				return h, true
			}
		}
		ps.Truncate(mark)
	}

	// 3) pure param segment | 3) 纯参数段
	if n.param != nil && val != "" {
		mark := ps.Len()
		ps.Append(n.param.seg.Name, val)
		if h, ok := descend(n.param, path, end, last, method, ps); ok {
			return h, true
		}
		ps.Truncate(mark)
	}

	// 4) wildcard segment: captures all remaining raw text (keeping the original encoding and inner slashes). | 4) 通配段：捕获剩余全部原文（保留原始编码与内部斜杠）。
	// A wildcard segment swallows all remaining segments at once, so termination is decided on that node directly. | 通配段一次性吃掉剩余所有分段，因此直接在该节点上判定终止。
	if n.wildcard != nil {
		mark := ps.Len()
		ps.Append(n.wildcard.seg.Name, path[start:])
		if h, ok := n.wildcard.handlers[method]; ok {
			return h, true
		}
		ps.Truncate(mark)
	}

	return nil, false
}

// descend takes the handler directly when the current segment is the last one, otherwise it continues with the next segment. | descend 在当前分段即为末段时直接取处理器，否则继续匹配下一分段。
func descend(c *node, path string, end int, last bool, method string, ps *Params) (any, bool) {
	if last {
		h, ok := c.handlers[method]
		return h, ok
	}
	return matchNode(c, path, end+1, method, ps)
}

// tsr reports whether the path would match if only the trailing slash differed. | tsr 判断路径是否只差一个尾斜杠就能命中。
func (t *Tree) tsr(method, path string) bool {
	if path == "" || path == "/" {
		return false
	}
	if alt, found := strings.CutSuffix(path, "/"); found {
		return t.allowedOnce(alt, method)
	}
	return t.allowedOnce(path+"/", method)
}

// allowedOnce reports whether a handler for method exists on the alt path. | allowedOnce 报告 alt 路径上是否存在 method 的处理器。
func (t *Tree) allowedOnce(path, method string) bool {
	ps := acquireParams()
	defer releaseParams(ps)
	_, ok := t.lookup(method, path, ps)
	return ok
}

// Allowed returns all methods registered on the path (ascending), used for the Allow header of a 405 response. | Allowed 返回该路径上已注册的全部方法（升序），用于 405 响应的 Allow 头。
//
// Unlike Find, it ignores method and collects the method set of every reachable terminal node. | 与 Find 不同，它忽略 method，收集所有可达终止节点上的方法集。
func (t *Tree) Allowed(path string) []string {
	ps := acquireParams()
	defer releaseParams(ps)
	set := make(map[string]struct{}, 8)
	t.collectMethods(path, ps, set)
	if len(set) == 0 {
		return nil
	}
	methods := make([]string, 0, len(set))
	for m := range set {
		methods = append(methods, m)
	}
	sort.Strings(methods)
	return methods
}

func (t *Tree) collectMethods(path string, ps *Params, out map[string]struct{}) {
	if path == "" || path[0] != '/' {
		return
	}
	if path == "/" {
		for m := range t.root.handlers {
			out[m] = struct{}{}
		}
		return
	}
	collectNode(t.root, path, 1, ps, out)
}

func collectNode(n *node, path string, start int, ps *Params, out map[string]struct{}) {
	end := start
	for end < len(path) && path[end] != '/' {
		end++
	}
	val := path[start:end]
	last := end == len(path)

	harvest := func(c *node) {
		if last {
			for m := range c.handlers {
				out[m] = struct{}{}
			}
			return
		}
		collectNode(c, path, end+1, ps, out)
	}

	if c, ok := n.static[val]; ok {
		harvest(c)
	}
	for _, c := range n.mixed {
		mark := ps.Len()
		if matchSegmentInto(c.seg, val, ps) {
			harvest(c)
		}
		ps.Truncate(mark)
	}
	if n.param != nil && val != "" {
		mark := ps.Len()
		ps.Append(n.param.seg.Name, val)
		harvest(n.param)
		ps.Truncate(mark)
	}
	if n.wildcard != nil {
		mark := ps.Len()
		ps.Append(n.wildcard.seg.Name, path[start:])
		for m := range n.wildcard.handlers {
			out[m] = struct{}{}
		}
		ps.Truncate(mark)
	}
}

// Walk visits every registered (method, pattern, handler) in the tree. | Walk 遍历树中所有已注册的 (method, pattern, handler)。
//
// The traversal follows the tree structure order of registration, with method names ascending inside a node, so results are reproducible. | 遍历顺序为注册时的树形结构顺序，同一节点内方法名升序，保证结果可复现。
func (t *Tree) Walk(fn func(method, pattern string, handler any)) {
	walkNode(t.root, fn)
}

func walkNode(n *node, fn func(method, pattern string, handler any)) {
	if len(n.handlers) > 0 {
		methods := make([]string, 0, len(n.handlers))
		for m := range n.handlers {
			methods = append(methods, m)
		}
		sort.Strings(methods)
		for _, m := range methods {
			fn(m, n.pattern, n.handlers[m])
		}
	}
	keys := make([]string, 0, len(n.static))
	for k := range n.static {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		walkNode(n.static[k], fn)
	}
	for _, c := range n.mixed {
		walkNode(c, fn)
	}
	if n.param != nil {
		walkNode(n.param, fn)
	}
	if n.wildcard != nil {
		walkNode(n.wildcard, fn)
	}
}
