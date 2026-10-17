package router

import (
	"encoding/binary"
	"math/bits"
	"net/http"
	"sort"
	"strings"
	"unsafe"

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
	// staticIndex maps the first byte of a segment to its static child (index+1, zero means
	// none). It removes the linear scan for small branches; duplicate first bytes fall back to
	// the scan, because the index is a hint and never changes match semantics. | staticIndex 把
	// 分段首字节映射到静态子节点（下标 + 1，零表示没有）。它消除小分支上的线性扫描；首字节重复时
	// 回退到扫描，因为该索引只是提示，绝不改变匹配语义。
	staticIndex *[256]uint8
	pattern     string
	// patternShape is the precompiled static/param shape of a safe parameterized route, shared
	// by every request context; nil means the route is not eligible for pattern caching. | patternShape
	// 是安全参数化路由预编译的静态/参数形状，由所有请求上下文共享；nil 表示该路由不参与模式缓存。
	patternShape *patternShape
	static       []staticChild

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
	if len(n.handlers) == 1 {
		h := &n.handlers[0]
		if key.known {
			if h.id == key.id {
				return h.handler, true
			}
			return nil, false
		}
		if h.method == key.name {
			return h.handler, true
		}
		return nil, false
	}
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

// patternSegment is one segment of a compiled route pattern. | patternSegment 是已编译路由模式的一个分段。
type patternSegment struct {
	literal string
	name    string
	param   bool
}

// patternShape is a precompiled route shape. When fast is set, the route has exactly one
// parameter and the whole path is prefix + parameter + suffix, so matching is two string
// comparisons and one slash scan instead of a segment loop. | patternShape 是预编译的路由形状。
// fast 置位时路由恰好只有一个参数，整个路径就是「前缀 + 参数 + 后缀」，匹配因此是两次字符串比较
// 加一次斜杠扫描，而不是分段循环。
type patternShape struct {
	prefix   string
	suffix   string
	param    string
	segments []patternSegment
	fast     bool
}

// compilePatternShape turns a parsed route pattern into its match shape when the route is
// eligible for pattern caching: only static and parameter segments, at least one parameter and
// no trailing slash. Mixed and wildcard segments are rejected because their priority rules
// cannot be verified for a different path value by a linear walk. | compilePatternShape 在路由
// 符合模式缓存条件时把已解析的路由模式转成匹配形状：只允许静态段与参数段，至少一个参数且没有
// 尾斜杠。混合段与通配段被拒绝，因为线性遍历无法为不同的路径值验证它们的优先级规则。
func compilePatternShape(segs []Segment, paramNames []string) *patternShape {
	params := 0
	for i := range segs {
		switch segs[i].Kind {
		case SegStatic:
			if segs[i].TrailingSlash {
				return nil
			}
		case SegParam:
			params++
		default:
			return nil
		}
	}
	if params == 0 || len(paramNames) != params {
		return nil
	}
	shape := &patternShape{segments: make([]patternSegment, len(segs))}
	pi := 0
	for i := range segs {
		if segs[i].Kind == SegParam {
			shape.segments[i] = patternSegment{name: paramNames[pi], param: true}
			pi++
		} else {
			shape.segments[i] = patternSegment{literal: segs[i].Literal}
		}
	}
	if params != 1 {
		return shape
	}
	// Exactly one parameter: the path is prefix + value + suffix, with the surrounding slashes
	// guaranteeing the value is a single segment. | 恰好一个参数：路径就是「前缀 + 值 + 后缀」，
	// 两侧的斜杠保证该值本身是一个分段。
	paramIdx := -1
	for i := range segs {
		if segs[i].Kind == SegParam {
			paramIdx = i
			shape.param = paramNames[0]
			break
		}
	}
	var pre strings.Builder
	pre.WriteByte('/')
	for i := 0; i < paramIdx; i++ {
		if i > 0 {
			pre.WriteByte('/')
		}
		pre.WriteString(segs[i].Literal)
	}
	if paramIdx > 0 {
		pre.WriteByte('/')
	}
	var suf strings.Builder
	for i := paramIdx + 1; i < len(segs); i++ {
		suf.WriteByte('/')
		suf.WriteString(segs[i].Literal)
	}
	shape.prefix = pre.String()
	shape.suffix = suf.String()
	shape.fast = true
	return shape
}

// staticSliceLimit is the fan-out up to which a linear scan beats a map lookup. | staticSliceLimit
// 是线性扫描优于映射查找的扇出上限。
const staticSliceLimit = 8

func newNode(seg *Segment) *node { return &node{seg: seg} }

// staticChild returns the static child named name. | staticChild 返回名为 name 的静态子节点。
func (n *node) staticChild(name string) (*node, bool) {
	if idx := n.staticIndex; idx != nil && name != "" {
		if i := idx[name[0]]; i != 0 {
			c := &n.static[i-1]
			if segEqual(c.name, name) {
				return c.node, true
			}
		}
		return nil, false
	}
	if len(n.static) == 1 {
		c := &n.static[0]
		if segEqual(c.name, name) {
			return c.node, true
		}
		return nil, false
	}
	return n.staticChildSlow(name)
}

// staticChildSlow handles the map fallback, the single child and the unindexed scan. Keeping it
// out of staticChild lets the compiler inline the indexed fast path into the matcher. | staticChildSlow
// 处理映射回退、单子节点与无索引扫描。把它移出 staticChild，编译器才能把带索引的快路径内联进匹配器。
func (n *node) staticChildSlow(name string) (*node, bool) {
	if n.staticMap != nil {
		c, ok := n.staticMap[name]
		return c, ok
	}
	switch len(n.static) {
	case 0:
		return nil, false
	case 1:
		if segEqual(n.static[0].name, name) {
			return n.static[0].node, true
		}
		return nil, false
	}
	for i := range n.static {
		if segEqual(n.static[i].name, name) {
			return n.static[i].node, true
		}
	}
	return nil, false
}

// rebuildStaticIndex refreshes the first-byte index after a static child is added. | rebuildStaticIndex
// 在新增静态子节点后刷新首字节索引。
func (n *node) rebuildStaticIndex() {
	n.staticIndex = nil
	if len(n.static) < 2 {
		return
	}
	idx := new([256]uint8)
	for i := range n.static {
		name := n.static[i].name
		if name == "" {
			return
		}
		b := name[0]
		// Two children sharing a first byte cannot be indexed; keep the linear scan so
		// correctness never depends on the index. | 首字节相同的两个子节点无法建索引；保留线性
		// 扫描，使正确性绝不依赖该索引。
		if idx[b] != 0 {
			return
		}
		idx[b] = uint8(i + 1)
	}
	n.staticIndex = idx
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
		n.rebuildStaticIndex()
		return c
	}
	n.staticMap = make(map[string]*node, len(n.static)*2)
	for _, child := range n.static {
		n.staticMap[child.name] = child.node
	}
	n.staticMap[name] = c
	n.static = nil
	n.staticIndex = nil
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
	// rootAllowed marks the first byte of every root static edge; rootOpen disables the rejection
	// shortcut when a param, wildcard or mixed edge could match any first byte. Both are rebuilt
	// during wiring and only read on the request path. A byte table beats a bitmap here because
	// the hot check is a single indexed load. | rootAllowed 标记根节点每条静态边的首字节；当参数、
	// 通配或混合边可能匹配任意首字节时，rootOpen 关闭拒绝短路。两者都在装配期重建，请求路径上只读。
	// 这里用字节表而不是位图，因为热路径检查因此只是一次带下标的加载。
	rootAllowed [256]bool
	rootOpen    bool
}

// nextSlash returns the index of the next '/' at or after start, or len(path). Short suffixes
// use an inlinable byte loop; long ones scan eight bytes at a time. | nextSlash 返回 start 处或
// 之后的下一个 '/' 下标，没有则返回 len(path)。短后缀使用可内联的字节循环，长后缀每次扫描八个字节。
func nextSlash(path string, start int) int {
	n := len(path)
	if n-start < 8 {
		for start < n && path[start] != '/' {
			start++
		}
		return start
	}
	return nextSlashLong(path, start)
}

// nextSlashLong scans eight bytes at a time. The classic zero-byte test's lowest set bit is the
// first '/' in the little-endian window. | nextSlashLong 每次扫描八个字节。经典零字节检测的最低
// 置位即小端窗口中的第一个 '/'。
func nextSlashLong(path string, start int) int {
	n := len(path)
	b := unsafe.Slice(unsafe.StringData(path), n)
	for start+8 <= n {
		w := binary.LittleEndian.Uint64(b[start:])
		x := w ^ 0x2f2f2f2f2f2f2f2f
		if m := (x - 0x0101010101010101) &^ x & 0x8080808080808080; m != 0 {
			return start + bits.TrailingZeros64(m)>>3
		}
		start += 8
	}
	for start < n && path[start] != '/' {
		start++
	}
	return start
}

// segEqual compares two path segments. Registered static segments are short; comparing the
// first and last four bytes covers every string up to eight bytes without the memeq call that a
// plain string comparison makes for non-constant operands. Longer strings use the runtime
// compare, which is already vectorized. | segEqual 比较两个路径分段。注册的静态分段通常较短；
// 比较首尾各四个字节即可覆盖八字节以内的所有字符串，避免普通字符串比较对非常量操作数发起的
// memeq 调用。更长的字符串交给运行时已经向量化的比较。
func segEqual(a, b string) bool {
	n := len(a)
	if n != len(b) {
		return false
	}
	if n >= 4 && n <= 8 {
		if load4(a, 0) != load4(b, 0) {
			return false
		}
		if n > 4 {
			return load4(a, n-4) == load4(b, n-4)
		}
		return true
	}
	if n > 8 {
		return a == b
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// load4 reads four bytes of s without copying. | load4 在不复制的情况下读取 s 的四个字节。
func load4(s string, off int) uint32 {
	return *(*uint32)(unsafe.Add(unsafe.Pointer(unsafe.StringData(s)), off))
}

// rebuildRootMask refreshes the first-byte rejection mask after a route is added. | rebuildRootMask
// 在新增路由后刷新首字节拒绝掩码。
func (t *Tree) rebuildRootMask() {
	t.rootAllowed = [256]bool{}
	t.rootOpen = t.root.param != nil || t.root.wildcard != nil || len(t.root.mixed) > 0
	if t.root.staticMap != nil {
		for name := range t.root.staticMap {
			if name != "" {
				t.rootAllowed[name[0]] = true
			}
		}
		return
	}
	for i := range t.root.static {
		if name := t.root.static[i].name; name != "" {
			t.rootAllowed[name[0]] = true
		}
	}
}

// patternUsable reports whether shape still routes identically for every matching path: each
// parameter segment must have no static or mixed sibling, because another value could take that
// sibling. The check runs once per pattern installation (per context and generation), not per
// request. | patternUsable 报告 shape 对每个匹配路径是否仍路由到同一位置：每个参数段都不得有
// 静态或混合兄弟，否则其它值可能走该兄弟。该检查在每次模式安装时执行（每上下文、每代际一次），
// 而不是每请求。
func (t *Tree) patternUsable(shape *patternShape) bool {
	n := t.root
	for i := range shape.segments {
		seg := &shape.segments[i]
		if seg.param {
			if n.param == nil || len(n.static) > 0 || n.staticMap != nil || len(n.mixed) > 0 {
				return false
			}
			n = n.param
			continue
		}
		c, ok := n.staticChild(seg.literal)
		if !ok {
			return false
		}
		n = c
	}
	return true
}

// rootReject reports that no route of any method can match path, because its first segment's
// first byte is absent from every root static edge and no root param, wildcard or mixed edge
// exists. ServeHTTP uses it to answer a structural 404 without taking a context from the pool. | rootReject
// 报告任何方法的路由都无法匹配 path：其第一个分段的首字节不在任何根静态边上，且根节点没有参数、
// 通配或混合边。ServeHTTP 据此在不从池中取上下文的情况下应答结构性 404。
func (t *Tree) rootReject(path string) bool {
	if t.rootOpen || len(path) < 2 || path[0] != '/' || path[1] == '/' {
		return false
	}
	return !t.rootAllowed[path[1]]
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
	paramNames := make([]string, 0, len(segs))
	for _, seg := range segs {
		next, err := n.child(seg)
		if err != nil {
			return err
		}
		// Capture the name stored on the actual node: two methods may register the same path
		// position with different parameter names, and the tree keeps the first one. | 捕获实际
		// 节点上保存的名称：两个方法可能用不同参数名注册同一路径位置，而路由树保留第一个名称。
		if seg.Kind == SegParam {
			paramNames = append(paramNames, next.seg.Name)
		}
		n = next
	}
	if !n.addHandler(method, handler) {
		return errx.Detail(ErrConflict, "%s %s", method, pattern)
	}
	n.pattern = pattern
	n.patternShape = compilePatternShape(segs, paramNames)
	t.rebuildRootMask()
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
	return t.findKey(newMethodKey(method), path, ps)
}

// findKey is find with a precomputed method key, so callers on the request path resolve the
// method only once. | findKey 是使用预计算方法键的 find，使请求路径上的调用方只解析一次方法。
func (t *Tree) findKey(key methodKey, path string, ps *Params) (any, bool, bool) {
	h, _, tsr, noPath := t.findKeyNode(key, path, ps)
	return h, tsr, noPath
}

// findKeyNode is findKey plus the terminal node, so the caller can install the route's
// precompiled pattern shape after a successful match. | findKeyNode 是 findKey 加上终止节点，
// 使调用方能在匹配成功后安装该路由预编译的模式形状。
func (t *Tree) findKeyNode(key methodKey, path string, ps *Params) (any, *node, bool, bool) {
	if path == "" || path[0] != '/' {
		return nil, nil, false, true
	}
	if path == "/" {
		handler, _ := t.root.handlerFor(key)
		// A root registered for another method still needs the 405 path; only a completely
		// empty root can be answered as a structural miss. | 根节点注册了其它方法时仍需走 405 判定；
		// 只有完全空的根节点才算结构性未命中。
		return handler, t.root, false, len(t.root.handlers) == 0
	}
	if h, tn, ok := matchNodeFast(t.root, path, 1, key, ps); ok {
		return h, tn, false, false
	}
	if !t.rootCanMatch(firstSegment(path), ps) {
		return nil, nil, false, true
	}
	return nil, nil, t.tsr(key, path), false
}

// firstSegment returns the first path segment without its leading slash. | firstSegment 返回去掉
// 前导斜杠的第一个路径分段。
func firstSegment(path string) string {
	return path[1:nextSlash(path, 1)]
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
	h, _, ok := matchNodeFast(t.root, path, 1, key, ps)
	return h, ok
}

// matchNodeFast is the non-backtracking fast path for the overwhelmingly common route shape: a
// chain of static segments with at most one forced param branch per node. It walks iteratively
// and delegates to matchNode as soon as a node has competing edges whose failure would require
// backtracking. | matchNodeFast 是绝大多数路由形态的非回溯快路径：静态分段链，每个节点最多
// 一个被迫走的参数分支。它迭代下探，一旦节点存在失败后需要回溯的竞争边就委托给 matchNode。
func matchNodeFast(n *node, path string, start int, key methodKey, ps *Params) (any, *node, bool) {
	mark := ps.Len()
	for {
		// The segment scan is inlined: a function call per segment costs more than the scan
		// itself on short paths. Long suffixes use the inlinable word scanner. | 分段扫描内联化：
		// 短路径上每分段一次函数调用比扫描本身还贵。长后缀使用可内联的字扫描器。
		end := start
		if len(path)-start >= 8 {
			end = nextSlashLong(path, start)
		} else {
			for end < len(path) && path[end] != '/' {
				end++
			}
		}
		val := path[start:end]
		last := end == len(path)

		// Static child lookup is inlined: the indexed branch and the single-child branch cover
		// every ordinary node; maps and duplicate-first-byte scans stay in the slow helper. | 静态
		// 子节点查找内联化：带索引的分支与单子节点分支覆盖所有普通节点；映射与首字节重复的扫描
		// 留在慢速辅助函数里。
		var c *node
		ok := false
		if idx := n.staticIndex; idx != nil && val != "" {
			if i := idx[val[0]]; i != 0 {
				sc := &n.static[i-1]
				c, ok = sc.node, segEqual(sc.name, val)
			}
		} else if len(n.static) == 1 {
			sc := &n.static[0]
			c, ok = sc.node, segEqual(sc.name, val)
		} else if len(n.static) > 1 || n.staticMap != nil {
			c, ok = n.staticChildSlow(val)
		}
		if ok {
			// A param/mixed/wildcard sibling could still win if this branch fails deeper, so
			// only a node without such siblings can be descended without a backtrack frame. | 若
			// 更深层失败后参数/混合/通配兄弟仍可能命中，则只有无此类兄弟的节点才能无回溯下探。
			if len(n.mixed) > 0 || n.param != nil || n.wildcard != nil {
				h, tn, ok := matchNode(n, path, start, key, ps)
				if !ok {
					ps.Truncate(mark)
				}
				return h, tn, ok
			}
			if last {
				var h any
				var ok bool
				if len(c.handlers) == 1 {
					h, ok = handlerForOne(c, key)
				} else {
					h, ok = c.handlerFor(key)
				}
				if !ok {
					ps.Truncate(mark)
				}
				return h, c, ok
			}
			n = c
			start = end + 1
			continue
		}

		// No static child matched. With a single param edge and no mixed/wildcard edge, the
		// param branch is forced and cannot be backtracked. | 没有静态子节点命中。仅有一个参数
		// 边且没有混合/通配边时，参数分支是被迫的，无需回溯。
		if n.param != nil && len(n.mixed) == 0 && n.wildcard == nil && val != "" {
			ps.Append(n.param.seg.Name, val)
			if last {
				var h any
				var ok bool
				if len(n.param.handlers) == 1 {
					h, ok = handlerForOne(n.param, key)
				} else {
					h, ok = n.param.handlerFor(key)
				}
				if !ok {
					ps.Truncate(mark)
				}
				return h, n.param, ok
			}
			n = n.param
			start = end + 1
			continue
		}
		h, tn, ok := matchNode(n, path, start, key, ps)
		if !ok {
			ps.Truncate(mark)
		}
		return h, tn, ok
	}
}

// handlerForOne compares the only handler of n with key. | handlerForOne 用 key 比较 n 唯一的处理器。
func handlerForOne(n *node, key methodKey) (any, bool) {
	h := &n.handlers[0]
	if key.known {
		if h.id == key.id {
			return h.handler, true
		}
		return nil, false
	}
	if h.method == key.name {
		return h.handler, true
	}
	return nil, false
}

// matchNode matches the segment of path that starts at start, beginning from node n. | matchNode 从节点 n 开始匹配 path 中起始于 start 的那个分段。
//
// start is the index of the current segment's first character in path; segments are separated by '/', and the trailing empty segment | start 为当前分段首字符在 path 中的下标；分段以 '/' 分隔，末尾空段
// (a path ending with "/") is represented as a segment with an empty value. | （路径以 "/" 结尾）会被表示为一个空值分段。
func matchNode(n *node, path string, start int, key methodKey, ps *Params) (any, *node, bool) {
	end := nextSlash(path, start)
	val := path[start:end] // the substring shares the backing array, so no allocation | 子串共享底层数组，不产生分配
	last := end == len(path)

	// 1) static segment: an exact match comes first | 1) 静态段：完全匹配优先
	// descend is inlined at every call site: the extra wrapper would cost one call per segment
	// on the hottest path, and the recursion below is the only real branch. | 每个调用点都内联
	// descend：额外包装会在最热路径上为每个分段多一次调用，真正的分支只有下面的递归。
	if c, ok := n.staticChild(val); ok {
		if last {
			if h, ok := c.handlerFor(key); ok {
				return h, c, true
			}
		} else if h, tn, ok := matchNode(c, path, end+1, key, ps); ok {
			return h, tn, true
		}
	}

	// 2) mixed segment: the more specific the literal anchor, the higher the priority | 2) 混合段：字面量锚点越具体越优先
	for _, c := range n.mixed {
		mark := ps.Len()
		if matchSegmentInto(c.seg, val, ps) {
			if last {
				if h, ok := c.handlerFor(key); ok {
					return h, c, true
				}
			} else if h, tn, ok := matchNode(c, path, end+1, key, ps); ok {
				return h, tn, true
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
				return h, n.param, true
			}
		} else if h, tn, ok := matchNode(n.param, path, end+1, key, ps); ok {
			return h, tn, true
		}
		ps.Truncate(mark)
	}

	// 4) wildcard segment: captures all remaining raw text (keeping the original encoding and inner slashes). | 4) 通配段：捕获剩余全部原文（保留原始编码与内部斜杠）。
	// A wildcard segment swallows all remaining segments at once, so termination is decided on that node directly. | 通配段一次性吃掉剩余所有分段，因此直接在该节点上判定终止。
	if n.wildcard != nil {
		mark := ps.Len()
		ps.Append(n.wildcard.seg.Name, path[start:])
		if h, ok := n.wildcard.handlerFor(key); ok {
			return h, n.wildcard, true
		}
		ps.Truncate(mark)
	}

	return nil, nil, false
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
	end := nextSlash(path, start)
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
	end := nextSlash(path, start)
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

// sameMethod compares two method keys without invoking a string comparison for standard
// methods, whose identifiers are unique. | sameMethod 比较两个方法键；标准方法的标识唯一，
// 因此不会触发字符串比较。
func sameMethod(a, b methodKey) bool {
	if a.known != b.known || a.id != b.id {
		return false
	}
	if a.known {
		return true
	}
	return a.name == b.name
}
