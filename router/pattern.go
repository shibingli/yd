// Package router provides a self-developed high-performance radix tree (Radix Tree) router. | Package router 提供自研的高性能基数树（Radix Tree）路由。
//
// Besides the usual static and parameter segments, this router natively supports **mixed static/dynamic matching within a single segment**, | 除常规静态段与参数段外，本路由原生支持**单段内的混合静态/动态匹配**，
// that is, it allows literals and dynamic captures to be mixed inside the same path segment separated by "/": | 即允许在非 "/" 分隔的同一路径段内混合字面量与动态捕获：
//
//	/avatar/:id.jpg        捕获 id，且必须以 .jpg 结尾
//	/report/:id-xx.png     捕获 id，剥离静态后缀 "-xx.png"
//	/archive/:y-:m-:d.html 单段内捕获三个参数
//	/static/*filepath      末段通配，捕获含 "/" 的剩余全部内容
//
// Special characters in literals can be escaped with a backslash (`\:`, `\*`, `\\`). | 字面量中的特殊字符可用反斜杠转义（`\:`、`\*`、`\\`）。
package router

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	// ErrBadPattern indicates an invalid route pattern syntax. | ErrBadPattern 表示路由模式语法非法。
	ErrBadPattern = errors.New("router: 非法的路由模式")
	// ErrDuplicateParam indicates a duplicate parameter name inside the same pattern. | ErrDuplicateParam 表示同一模式内出现重复的参数名。
	ErrDuplicateParam = errors.New("router: 重复的参数名")
	// ErrWildcardPosition indicates the wildcard is not in the last segment of the pattern. | ErrWildcardPosition 表示通配符未出现在模式的最后一段。
	ErrWildcardPosition = errors.New("router: 通配符必须位于模式末尾")
	// ErrConflict indicates a conflicting route pattern was registered under the same method. | ErrConflict 表示同一方法下注册了冲突的路由模式。
	ErrConflict = errors.New("router: 路由模式冲突")
)

// TokenKind distinguishes the type of a lexical token inside a segment. | TokenKind 区分段内词法单元的类型。
type TokenKind uint8

const (
	// TokenLiteral represents a piece of literal text. | TokenLiteral 表示一段字面量文本。
	TokenLiteral TokenKind = iota
	// TokenParam represents a dynamic parameter capture. | TokenParam 表示一个动态参数捕获。
	TokenParam
)

// Token is the smallest matching unit inside a path segment. | Token 是路径段内的最小匹配单元。
type Token struct {
	// Kind is the token kind. | Kind 为词法单元类型。
	Kind TokenKind
	// Text is the literal text for TokenLiteral and the parameter name for TokenParam. | Text 在 TokenLiteral 时为字面量文本，在 TokenParam 时为参数名。
	Text string
}

// SegmentKind distinguishes the match category of a path segment. | SegmentKind 区分路径段的匹配类别。
type SegmentKind uint8

const (
	// SegStatic represents a purely static segment, such as "users". | SegStatic 表示纯静态段，例如 "users"。
	SegStatic SegmentKind = iota
	// SegParam represents a purely parameter segment, such as ":id". | SegParam 表示纯参数段，例如 ":id"。
	SegParam
	// SegMixed represents a segment mixing literals and parameters, such as ":id-xx.png". | SegMixed 表示字面量与参数混合的段，例如 ":id-xx.png"。
	SegMixed
	// SegWildcard represents a wildcard in the last segment, such as "*filepath". | SegWildcard 表示末段通配，例如 "*filepath"。
	SegWildcard
)

// Segment is a parsed path segment. | Segment 是一个已解析的路径段。
type Segment struct {
	// Raw is the raw text of the segment (without the "/" separator). | Raw 为该段的原始文本（不含分隔符 "/"）。
	Raw string
	// Kind is the match category of the segment. | Kind 为该段的匹配类别。
	Kind SegmentKind
	// Tokens is the token sequence inside the segment (a single literal token for SegStatic). | Tokens 为段内的词法单元序列（SegStatic 时为单个字面量单元）。
	Tokens []Token
	// Name is the parameter name for SegParam and SegWildcard. | Name 在 SegParam 与 SegWildcard 时为参数名。
	Name string
	// Literal is the **unescaped** literal text for SegStatic, used for exact matching and indexing. | Literal 在 SegStatic 时为**反转义后**的字面量文本，用于精确匹配与建索引。
	// For example the pattern `/a\:b` has Literal "a:b", while Raw is `a\:b`. | 例如模式 `/a\:b` 的 Literal 为 "a:b"，而 Raw 为 `a\:b`。
	Literal string
	// LiteralLen is the total number of literal characters in the segment, used for match priority (the more specific, the higher the priority). | LiteralLen 为该段内字面量字符总数，用于匹配优先级（越具体越优先）。
	LiteralLen int
	// TrailingSlash marks this segment as the empty segment at the end of the pattern, meaning the pattern ends with "/". | TrailingSlash 标记该段是模式末尾的空段，表示模式以 "/" 结尾。
	TrailingSlash bool
}

// ParsePattern parses a route pattern into a sequence of path segments. | ParsePattern 将路由模式解析为路径段序列。
//
// The pattern must start with "/"; the root path "/" parses into an empty segment list (matching the root of any method). | 模式必须以 "/" 开头；根路径 "/" 解析为空段序列（匹配任意 method 的根）。
// A pattern ending with "/" is terminated by a TrailingSlash segment, used to distinguish /users from /users/. | 以 "/" 结尾的模式会以一个 TrailingSlash 段收尾，用于区分 /users 与 /users/。
func ParsePattern(p string) ([]Segment, error) {
	if p == "" || p[0] != '/' {
		return nil, fmt.Errorf("%w: %q 必须以 / 开头", ErrBadPattern, p)
	}
	raws := splitPath(p)
	segs := make([]Segment, 0, len(raws))
	seen := make(map[string]struct{}, 4)
	for i, raw := range raws {
		if raw == "" {
			if i != len(raws)-1 {
				return nil, fmt.Errorf("%w: %q 含有空路径段", ErrBadPattern, p)
			}
			segs = append(segs, Segment{Raw: "", Kind: SegStatic, TrailingSlash: true})
			continue
		}
		seg, err := parseSegment(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %q 中的 %q: %v", ErrBadPattern, p, raw, err)
		}
		if seg.Kind == SegWildcard && i != len(raws)-1 {
			return nil, fmt.Errorf("%w: %q", ErrWildcardPosition, p)
		}
		for _, tk := range seg.Tokens {
			if tk.Kind != TokenParam {
				continue
			}
			if _, dup := seen[tk.Text]; dup {
				return nil, fmt.Errorf("%w: %q 中的 %q", ErrDuplicateParam, p, tk.Text)
			}
			seen[tk.Text] = struct{}{}
		}
		segs = append(segs, seg)
	}
	return segs, nil
}

// parseSegment parses a single path segment. | parseSegment 解析单个路径段。
func parseSegment(raw string) (Segment, error) {
	if raw[0] == '*' {
		name := raw[1:]
		if !validParamName(name) {
			return Segment{}, fmt.Errorf("通配符名称 %q 非法", name)
		}
		return Segment{
			Raw:    raw,
			Kind:   SegWildcard,
			Name:   name,
			Tokens: []Token{{Kind: TokenParam, Text: name}},
		}, nil
	}

	var (
		toks     []Token
		lit      strings.Builder
		hasParam bool
	)
	flushLit := func() {
		if lit.Len() > 0 {
			toks = append(toks, Token{Kind: TokenLiteral, Text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(raw); {
		c := raw[i]
		switch c {
		case '\\':
			if i+1 >= len(raw) {
				return Segment{}, errors.New("末尾存在悬空转义符")
			}
			next := raw[i+1]
			if next != ':' && next != '\\' && next != '*' {
				return Segment{}, fmt.Errorf("不支持的转义序列 %q", raw[i:i+2])
			}
			lit.WriteByte(next)
			i += 2
		case ':':
			j := i + 1
			for j < len(raw) && isParamChar(raw[j]) {
				j++
			}
			if !validParamName(raw[i+1 : j]) {
				return Segment{}, fmt.Errorf("参数名 %q 非法（须为 [A-Za-z_][A-Za-z0-9_]*）", raw[i+1:j])
			}
			flushLit()
			toks = append(toks, Token{Kind: TokenParam, Text: raw[i+1 : j]})
			hasParam = true
			i = j
		case '*':
			return Segment{}, errors.New("通配符 * 只能独占整个路径段")
		default:
			lit.WriteByte(c)
			i++
		}
	}
	flushLit()

	if !hasParam {
		lit := raw
		if len(toks) == 1 {
			lit = toks[0].Text
		}
		return Segment{Raw: raw, Kind: SegStatic, Tokens: toks, Literal: lit, LiteralLen: len(lit)}, nil
	}
	if len(toks) == 1 {
		return Segment{Raw: raw, Kind: SegParam, Name: toks[0].Text, Tokens: toks}, nil
	}
	litLen := 0
	for _, t := range toks {
		if t.Kind == TokenLiteral {
			litLen += len(t.Text)
		}
	}
	return Segment{Raw: raw, Kind: SegMixed, Tokens: toks, LiteralLen: litLen}, nil
}

// matchSegment matches one segment of the request path against a compiled segment and returns the captured parameters. | matchSegment 把请求路径的单个分段与已编译段做匹配，返回捕获到的参数。
//
// This function is a convenience wrapper around matchSegmentInto; on hot paths use matchSegmentInto directly | 该函数为 matchSegmentInto 的便捷包装；热路径请直接使用 matchSegmentInto
// to avoid allocating a slice for the capture result. | 以避免为捕获结果分配切片。
func matchSegment(seg *Segment, value string) ([]Param, bool) {
	ps := acquireParams()
	defer releaseParams(ps)
	if !matchSegmentInto(seg, value, ps) {
		return nil, false
	}
	out := make([]Param, ps.Len())
	copy(out, ps.ps)
	return out, true
}

// matchSegmentInto matches one segment of the request path against a compiled segment, | matchSegmentInto 把请求路径的单个分段与已编译段做匹配，
// appending the capture result **directly** to ps; ps is left unchanged on a mismatch (rolled back internally). | 捕获结果**直接追加**到 ps；不匹配时 ps 保持不变（内部已回滚）。
//
// SegMixed uses memoized backtracking: once the state (tokenIdx, pos) is memoized the worst case is | SegMixed 使用带记忆化的回溯：状态 (tokenIdx, pos) 记忆化后最坏复杂度为
// O(tokens × len), never exponential; parameter captures follow "shortest first" (left-to-right non-greedy), | O(tokens × len)，不会指数爆炸；参数捕获遵循「最短优先」（左到右非贪婪），
// so /:id-xx.png matching "a-b-xx.png" yields id="a-b". | 因此 /:id-xx.png 匹配 "a-b-xx.png" 时会得到 id="a-b"。
func matchSegmentInto(seg *Segment, value string, ps *Params) bool {
	switch seg.Kind {
	case SegStatic:
		return seg.Literal == value
	case SegParam:
		if value == "" {
			return false
		}
		ps.Append(seg.Name, value)
		return true
	case SegWildcard:
		ps.Append(seg.Name, value)
		return true
	case SegMixed:
		mark := ps.Len()
		mm := acquireMixMatcher(seg, value)
		ok := mm.match(0, 0, ps)
		releaseMixMatcher(mm)
		if !ok {
			ps.Truncate(mark)
		}
		return ok
	default:
		return false
	}
}

// mixMemo is a reusable memoization buffer that avoids repeated allocations in mixed segment matching on hot paths. | mixMemo 是可复用的记忆化缓冲，避免混合段匹配在热路径上反复分配。
type mixMemo struct {
	buf []int32
	gen int32
}

var mixMemoPool = sync.Pool{New: func() any { return &mixMemo{} }}

const mixMemoMaxRetain = 4096

// mixMatcher performs memoized backtracking matching inside a single mixed segment. | mixMatcher 在单个混合段内执行带记忆化的回溯匹配。
type mixMatcher struct {
	seg   *Segment
	value string
	memo  []int32
	gen   int32
	owner *mixMemo
}

func acquireMixMatcher(seg *Segment, value string) *mixMatcher {
	memo := mixMemoPool.Get().(*mixMemo)
	need := len(seg.Tokens) * (len(value) + 1)
	if cap(memo.buf) < need {
		memo.buf = make([]int32, need)
		memo.gen = 0
	}
	memo.buf = memo.buf[:need]
	memo.gen++
	if memo.gen == 0 { // overflow: clear and restart | 溢出：清零后重新开始
		clear(memo.buf)
		memo.gen = 1
	}
	return &mixMatcher{seg: seg, value: value, memo: memo.buf, gen: memo.gen, owner: memo}
}

func releaseMixMatcher(m *mixMatcher) {
	if m == nil || m.owner == nil {
		return
	}
	if cap(m.owner.buf) > mixMemoMaxRetain {
		return
	}
	m.owner.buf = m.owner.buf[:0]
	mixMemoPool.Put(m.owner)
	m.owner = nil
}

// match recursively matches the ti-th lexical token against value[pos:]. | match 递归匹配第 ti 个词法单元与 value[pos:]。
//
// Memoization uses (ti,pos) as the state key; once a failure state is recorded it is never recomputed, | 记忆化以 (ti,pos) 为状态键；失败状态记录后不再重复计算，
// so the overall complexity is polynomial. | 因而整体复杂度为多项式级别。
func (m *mixMatcher) match(ti, pos int, ps *Params) bool {
	if ti == len(m.seg.Tokens) {
		return pos == len(m.value)
	}
	idx := ti*(len(m.value)+1) + pos
	if m.memo[idx] == m.gen {
		return false
	}
	tok := m.seg.Tokens[ti]
	switch tok.Kind {
	case TokenLiteral:
		if strings.HasPrefix(m.value[pos:], tok.Text) && m.match(ti+1, pos+len(tok.Text), ps) {
			return true
		}
	case TokenParam:
		for l := 1; pos+l <= len(m.value); l++ {
			n := ps.Len()
			ps.Append(tok.Text, m.value[pos:pos+l])
			if m.match(ti+1, pos+l, ps) {
				return true
			}
			ps.Truncate(n)
		}
	}
	m.memo[idx] = m.gen
	return false
}

// segKey generates a unique identity for a segment, used for equivalence checks and conflict detection on insert. | segKey 生成段的唯一标识，用于插入时的等价判定与冲突检测。
func segKey(seg Segment) string {
	if seg.Kind == SegStatic {
		return "s:" + seg.Literal
	}
	var b strings.Builder
	b.WriteByte(byte('0' + seg.Kind))
	b.WriteByte('|')
	for _, t := range seg.Tokens {
		if t.Kind == TokenLiteral {
			b.WriteString("l")
		} else {
			b.WriteString("p")
		}
		b.WriteString(t.Text)
		b.WriteByte(0x1f)
	}
	return b.String()
}

func validParamName(s string) bool {
	if s == "" || !isNameStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isParamChar(s[i]) {
			return false
		}
	}
	return true
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isParamChar(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9')
}

// splitPath splits a path into path segments; the root path returns nil. | splitPath 把路径切成路径段；根路径返回 nil。
//
// "/users"  → ["users"]
// "/users/" → ["users", ""] (the trailing empty segment distinguishes the trailing slash) | "/users/" → ["users", ""]（尾随空段用于区分尾斜杠）
// "/"       → nil
func splitPath(p string) []string {
	if p == "" || p == "/" {
		return nil
	}
	if p[0] == '/' {
		p = p[1:]
	}
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// SplitPath splits a request path into path segments, for diagnostics and tests. | SplitPath 把请求路径切分为路径段，供诊断与测试使用。
//
// "/users"  → ["users"]
// "/users/" → ["users", ""]
// "/"       → nil
//
// Note: the route matching hot path does **not** use this function (it produces a slice allocation), | 注意：路由匹配热路径**不**使用本函数（它会产生切片分配），
// but instead scans the raw path string segment by segment using offsets. | 而是在原始路径字符串上以偏移量逐段扫描。
func SplitPath(p string) []string { return splitPath(p) }
