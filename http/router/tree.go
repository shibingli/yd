package router

import (
	"net/http"
	"sort"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// methodID is a compact identifier for a standard HTTP method. | methodID 是标准 HTTP 方法的紧凑标识。
// Zero means an uncommon method that must be compared by name. | 零表示必须按名称比较的非标准方法。
type methodID uint8

const (
	methodGet methodID = iota + 1
	methodHead
	methodPost
	methodPut
	methodPatch
	methodDelete
	methodOptions
	methodTrace
	methodConnect
)

// methodKey carries the method name and its precomputed identifier through tree matching. | methodKey
// 在树匹配过程中携带方法名及其预计算标识。
type methodKey struct {
	name  string
	id    methodID
	known bool
}

// newMethodKey resolves method once per request. | newMethodKey 每个请求解析一次方法。
func newMethodKey(method string) methodKey {
	id, known := methodOf(method)
	return methodKey{name: method, id: id, known: known}
}

// methodOf maps a standard method to its identifier. | methodOf 把标准方法映射为标识。
func methodOf(method string) (methodID, bool) {
	switch method {
	case http.MethodGet:
		return methodGet, true
	case http.MethodHead:
		return methodHead, true
	case http.MethodPost:
		return methodPost, true
	case http.MethodPut:
		return methodPut, true
	case http.MethodPatch:
		return methodPatch, true
	case http.MethodDelete:
		return methodDelete, true
	case http.MethodOptions:
		return methodOptions, true
	case http.MethodTrace:
		return methodTrace, true
	case http.MethodConnect:
		return methodConnect, true
	default:
		return 0, false
	}
}

// ErrUnknownSegment indicates a segment kind the tree cannot place. | ErrUnknownSegment 表示树无法安放的段类型。
var ErrUnknownSegment = errx.New("router: unknown segment type | router：未知的段类型")

// node is a single node of the radix tree; each node corresponds to one segment in the path. | node 是基数树的单个节点，每个节点对应路径中的一个分段。
//
// Child nodes are ordered by match priority: static > mixed (more literals first) > pure param > wildcard. | 子节点按匹配优先级排列：静态 > 混合（字面量越多越优先）> 纯参数 > 通配。
type node struct {
	seg      *Segment
	param    *node
	wildcard *node

	// staticMap takes over once a node has more static children than staticSliceLimit; small
	// fan-out stays a linear scan, which avoids the hashing cost on every request. | 静态子节点
	// 超过 staticSliceLimit 后改用 staticMap；小扇出保持线性扫描，避免每个请求都付出哈希开销。
	staticMap map[string]*node
	pattern   string
	static    []staticChild

	handlers []methodHandler
	mixed    []*node
}

// methodHandler is one method-to-handler binding of a terminal node. | methodHandler 是终止节点上
// 的一条「方法到处理器」绑定。
type methodHandler struct {
	handler any
	method  string
	id      methodID
}

// handlerFor returns the handler registered for key. | handlerFor 返回 key 注册的处理器。
//
// Standard methods compare one integer; only uncommon methods fall back to comparing the
// caller-provided name. | 标准方法只比较一个整数；只有非标准方法才回退到比较调用方给出的名称。
func (n *node) handlerFor(key methodKey) (any, bool) {
	if key.known {
		for i := range n.handlers {
			if n.handlers[i].id == key.id {
				return n.handlers[i].handler, true
			}
		}
		return nil, false
	}
	for i := range n.handlers {
		if n.handlers[i].method == key.name {
			return n.handlers[i].handler, true
		}
	}
	return nil, false
}

// handler returns the handler registered for method. | handler 返回 method 注册的处理器。
func (n *node) handler(method string) (any, bool) {
	return n.handlerFor(newMethodKey(method))
}

// addHandler binds method to handler and reports false when it was already bound. | addHandler
// 把 method 绑定到 handler；已绑定时返回 false。
func (n *node) addHandler(method string, handler any) bool {
	id, _ := methodOf(method)
	for i := range n.handlers {
		if n.handlers[i].method == method {
			return false
		}
	}
	n.handlers = append(n.handlers, methodHandler{method: method, handler: handler, id: id})
	return true
}

// staticChild is one static child edge. | staticChild 是一条静态子边。
type staticChild struct {
	node *node
	name string
}

// staticSliceLimit is the fan-out up to which a linear scan beats a map lookup. | staticSliceLimit
// 是线性扫描优于映射查找的扇出上限。
const staticSliceLimit = 8

func newNode(seg *Segment) *node { return &node{seg: seg} }

// staticChild returns the static child named name. | staticChild 返回名为 name 的静态子节点。
func (n *node) staticChild(name string) (*node, bool) {
	if n.staticMap != nil {
		c, ok := n.staticMap[name]
		return c, ok
	}
	switch len(n.static) {
	case 0:
		return nil, false
	case 1:
		if n.static[0].name == name {
			return n.static[0].node, true
		}
		return nil, false
	}
	for i := range n.static {
		if n.static[i].name == name {
			return n.static[i].node, true
		}
	}
	return nil, false
}

// ensureStaticChild returns the static child for name, creating it when necessary. | ensureStaticChild
// 返回 name 对应的静态子节点，必要时创建。
func (n *node) ensureStaticChild(name string, seg Segment) *node {
	if c, ok := n.staticChild(name); ok {
		return c
	}
	c := newNode(&seg)
	if n.staticMap != nil {
		n.staticMap[name] = c
		return c
	}
	if len(n.static) < staticSliceLimit {
		n.static = append(n.static, staticChild{name: name, node: c})
		return c
	}
	n.staticMap = make(map[string]*node, len(n.static)*2)
	for _, child := range n.static {
		n.staticMap[child.name] = child.node
	}
	n.staticMap[name] = c
	n.static = nil
	return c
}

// child returns the child node corresponding to seg (creating it when necessary), or an
// error when the segment kind is unknown. | child 返回（必要时创建）与 seg 对应的子节点；
// 段类型未知时返回错误。
func (n *node) child(seg Segment) (*node, error) {
	switch seg.Kind {
	case SegStatic:
		return n.ensureStaticChild(seg.Literal, seg), nil
	case SegParam:
		if n.param == nil {
			n.param = newNode(&seg)
		}
		return n.param, nil
	case SegWildcard:
		if n.wildcard == nil {
			n.wildcard = newNode(&seg)
		}
		return n.wildcard, nil
	case SegMixed:
		key := segKey(seg)
		for _, c := range n.mixed {
			if segKey(*c.seg) == key {
				return c, nil
			}
		}
		c := newNode(&seg)
		n.mixed = append(n.mixed, c)
		// the more literals, the more specific, the earlier it is tried; equal lengths keep registration order (stable sort) | 字面量越多越具体，越靠前尝试；同长度保持注册顺序（稳定排序）
		sort.SliceStable(n.mixed, func(i, j int) bool {
			return n.mixed[i].seg.LiteralLen > n.mixed[j].seg.LiteralLen
		})
		return c, nil
	default:
		return nil, errx.Detail(ErrUnknownSegment, "kind=%[1]d | 类型=%[1]d", seg.Kind)
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
		next, err := n.child(seg)
		if err != nil {
			return err
		}
		n = next
	}
	if !n.addHandler(method, handler) {
		return errx.Detail(ErrConflict, "%s %s", method, pattern)
	}
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
	handler, tsr, _ := t.find(method, path, ps)
	return handler, tsr
}

// find is Find plus a structural miss flag: noPath reports that the first segment matches no
// root edge at all, so neither a 405 (another method on the same path) nor a trailing slash
// redirect can exist. ServeHTTP uses it to answer unknown top-level paths after one lookup
// instead of walking the tree two more times. | find 是 Find 加上结构性未命中标记：noPath 表示
// 第一个分段在根节点上没有任何边可匹配，因此既不可能有 405（同路径的其它方法），也不可能有
// 尾斜杠重定向。ServeHTTP 据此在一次查找后直接应答未知的顶层路径，而不是再遍历两次树。
func (t *Tree) find(method, path string, ps *Params) (any, bool, bool) {
	key := newMethodKey(method)
	if path == "" || path[0] != '/' {
		return nil, false, true
	}
	if path == "/" {
		handler, _ := t.root.handlerFor(key)
		// A root registered for another method still needs the 405 path; only a completely
		// empty root can be answered as a structural miss. | 根节点注册了其它方法时仍需走 405 判定；
		// 只有完全空的根节点才算结构性未命中。
		return handler, false, len(t.root.handlers) == 0
	}
	if h, ok := matchNode(t.root, path, 1, key, ps); ok {
		return h, false, false
	}
	if !t.rootCanMatch(firstSegment(path), ps) {
		return nil, false, true
	}
	return nil, t.tsr(key, path), false
}

// firstSegment returns the first path segment without its leading slash. | firstSegment 返回去掉
// 前导斜杠的第一个路径分段。
func firstSegment(path string) string {
	end := 1
	for end < len(path) && path[end] != '/' {
		end++
	}
	return path[1:end]
}

// rootCanMatch reports whether any root edge can consume the first segment. | rootCanMatch 报告根
// 节点是否有任一边能够消费第一个分段。
func (t *Tree) rootCanMatch(segment string, ps *Params) bool {
	if _, ok := t.root.staticChild(segment); ok {
		return true
	}
	for _, child := range t.root.mixed {
		mark := ps.Len()
		matched := matchSegmentInto(child.seg, segment, ps)
		ps.Truncate(mark)
		if matched {
			return true
		}
	}
	return t.root.param != nil || t.root.wildcard != nil
}

// lookup ignores the trailing slash redirect and performs the match directly. | lookup 忽略尾斜杠重定向，直接执行匹配。
func (t *Tree) lookup(key methodKey, path string, ps *Params) (any, bool) {
	if path == "" || path[0] != '/' {
		return nil, false
	}
	if path == "/" {
		return t.root.handlerFor(key)
	}
	return matchNode(t.root, path, 1, key, ps)
}

// matchNode matches the segment of path that starts at start, beginning from node n. | matchNode 从节点 n 开始匹配 path 中起始于 start 的那个分段。
//
// start is the index of the current segment's first character in path; segments are separated by '/', and the trailing empty segment | start 为当前分段首字符在 path 中的下标；分段以 '/' 分隔，末尾空段
// (a path ending with "/") is represented as a segment with an empty value. | （路径以 "/" 结尾）会被表示为一个空值分段。
func matchNode(n *node, path string, start int, key methodKey, ps *Params) (any, bool) {
	end := start
	for end < len(path) && path[end] != '/' {
		end++
	}
	val := path[start:end] // the substring shares the backing array, so no allocation | 子串共享底层数组，不产生分配
	last := end == len(path)

	// 1) static segment: an exact match comes first | 1) 静态段：完全匹配优先
	// descend is inlined at every call site: the extra wrapper would cost one call per segment
	// on the hottest path, and the recursion below is the only real branch. | 每个调用点都内联
	// descend：额外包装会在最热路径上为每个分段多一次调用，真正的分支只有下面的递归。
	if c, ok := n.staticChild(val); ok {
		if last {
			if h, ok := c.handlerFor(key); ok {
				return h, true
			}
		} else if h, ok := matchNode(c, path, end+1, key, ps); ok {
			return h, true
		}
	}

	// 2) mixed segment: the more specific the literal anchor, the higher the priority | 2) 混合段：字面量锚点越具体越优先
	for _, c := range n.mixed {
		mark := ps.Len()
		if matchSegmentInto(c.seg, val, ps) {
			if last {
				if h, ok := c.handlerFor(key); ok {
					return h, true
				}
			} else if h, ok := matchNode(c, path, end+1, key, ps); ok {
				return h, true
			}
		}
		ps.Truncate(mark)
	}

	// 3) pure param segment | 3) 纯参数段
	if n.param != nil && val != "" {
		mark := ps.Len()
		ps.Append(n.param.seg.Name, val)
		if last {
			if h, ok := n.param.handlerFor(key); ok {
				return h, true
			}
		} else if h, ok := matchNode(n.param, path, end+1, key, ps); ok {
			return h, true
		}
		ps.Truncate(mark)
	}

	// 4) wildcard segment: captures all remaining raw text (keeping the original encoding and inner slashes). | 4) 通配段：捕获剩余全部原文（保留原始编码与内部斜杠）。
	// A wildcard segment swallows all remaining segments at once, so termination is decided on that node directly. | 通配段一次性吃掉剩余所有分段，因此直接在该节点上判定终止。
	if n.wildcard != nil {
		mark := ps.Len()
		ps.Append(n.wildcard.seg.Name, path[start:])
		if h, ok := n.wildcard.handlerFor(key); ok {
			return h, true
		}
		ps.Truncate(mark)
	}

	return nil, false
}

// tsr reports whether the path would match if only the trailing slash differed. | tsr 判断路径是否只差一个尾斜杠就能命中。
func (t *Tree) tsr(key methodKey, path string) bool {
	if path == "" || path == "/" {
		return false
	}
	if alt, found := strings.CutSuffix(path, "/"); found {
		return t.allowedOnce(alt, key)
	}
	return t.allowedOnce(path+"/", key)
}

// allowedOnce reports whether a handler for method exists on the alt path. | allowedOnce 报告 alt 路径上是否存在 method 的处理器。
func (t *Tree) allowedOnce(path string, key methodKey) bool {
	var ps Params
	_, ok := t.lookup(key, path, &ps)
	return ok
}

// Allowed returns all methods registered on the path (ascending), used for the Allow header of a 405 response. | Allowed 返回该路径上已注册的全部方法（升序），用于 405 响应的 Allow 头。
//
// Unlike Find, it ignores method and collects the method set of every reachable terminal node. | 与 Find 不同，它忽略 method，收集所有可达终止节点上的方法集。
func (t *Tree) Allowed(path string) []string {
	var ps Params
	// A 404 needs only "does any method exist here?"; answering that question allocates nothing,
	// while building the method set (needed only for 405 and Allow) may allocate. | 404 只需要判断
	// 「这里是否存在任一方法」，该判断零分配；只有 405 与 Allow 才需要构建方法集。
	if !t.hasAnyMethod(path, &ps) {
		return nil
	}
	set := make(map[string]struct{}, 8)
	t.collectMethods(path, &ps, set)
	methods := make([]string, 0, len(set))
	for method := range set {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return methods
}

// hasAnyMethod reports whether any method is registered on path, without allocating. | hasAnyMethod
// 报告 path 上是否注册了任一方法，且不产生分配。
func (t *Tree) hasAnyMethod(path string, ps *Params) bool {
	if path == "" || path[0] != '/' {
		return false
	}
	if path == "/" {
		return len(t.root.handlers) > 0
	}
	return hasMethodsNode(t.root, path, 1, ps)
}

// hasMethodsNode mirrors collectNode but stops at the first terminal node. | hasMethodsNode 与
// collectNode 结构相同，但在第一个终止节点处立即返回。
func hasMethodsNode(n *node, path string, start int, ps *Params) bool {
	end := start
	for end < len(path) && path[end] != '/' {
		end++
	}
	val := path[start:end]
	last := end == len(path)

	terminal := func(c *node) bool {
		if last {
			return len(c.handlers) > 0
		}
		return hasMethodsNode(c, path, end+1, ps)
	}

	if c, ok := n.staticChild(val); ok && terminal(c) {
		return true
	}
	for _, c := range n.mixed {
		mark := ps.Len()
		matched := matchSegmentInto(c.seg, val, ps)
		ps.Truncate(mark)
		if matched && terminal(c) {
			return true
		}
	}
	if n.param != nil && val != "" {
		mark := ps.Len()
		ps.Append(n.param.seg.Name, val)
		ok := terminal(n.param)
		ps.Truncate(mark)
		if ok {
			return true
		}
	}
	return n.wildcard != nil && len(n.wildcard.handlers) > 0
}

func (t *Tree) collectMethods(path string, ps *Params, out map[string]struct{}) {
	if path == "" || path[0] != '/' {
		return
	}
	if path == "/" {
		for i := range t.root.handlers {
			out[t.root.handlers[i].method] = struct{}{}
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
			for i := range c.handlers {
				out[c.handlers[i].method] = struct{}{}
			}
			return
		}
		collectNode(c, path, end+1, ps, out)
	}

	if c, ok := n.staticChild(val); ok {
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
		for i := range n.wildcard.handlers {
			out[n.wildcard.handlers[i].method] = struct{}{}
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
		for i := range n.handlers {
			methods = append(methods, n.handlers[i].method)
		}
		sort.Strings(methods)
		for _, method := range methods {
			handler, _ := n.handler(method)
			fn(method, n.pattern, handler)
		}
	}
	if n.staticMap != nil {
		keys := make([]string, 0, len(n.staticMap))
		for name := range n.staticMap {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			walkNode(n.staticMap[name], fn)
		}
	} else {
		children := append([]staticChild(nil), n.static...)
		sort.Slice(children, func(i, j int) bool { return children[i].name < children[j].name })
		for _, child := range children {
			walkNode(child.node, fn)
		}
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
