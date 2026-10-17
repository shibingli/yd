// Package regexpx is the repository's single regular-expression engine. | Package regexpx 是本仓唯一的正则表达式引擎。
//
// It wraps github.com/dlclark/regexp2 in RE2 compatibility mode, so patterns keep the semantics of
// the standard library ($ anchors at end of text, \d \s \w match the same characters, unknown
// escapes fall back to literals) while the .NET-style constructs (lookaround, backreferences,
// named groups) stay available for future patterns. | 它在 RE2 兼容模式下包装
// github.com/dlclark/regexp2：模式保持标准库语义（$ 只匹配文本末尾，\d \s \w 匹配同一字符集，
// 未知转义退化为字面量），同时保留 .NET 风格构造（环视、反向引用、命名分组）供后续模式使用。
//
// Two properties of the underlying engine are handled here rather than at every call site: | 底层引擎的
// 两个特性在本包内统一处理，而不是散落到每个调用点：
//
//   - A match timeout is mandatory. regexp2 backtracks, so a hostile input can trigger catastrophic
//     backtracking; a match that exceeds MatchTimeout is reported as "no match", which makes every
//     validation call fail closed. The timeout clock is global to the process and stops on its own
//     once no match is live. | 匹配超时是强制的。regexp2 会回溯，恶意输入可能触发灾难性回溯；
//     超过 MatchTimeout 的匹配按「未匹配」处理，使所有校验调用失败关闭。超时时钟是进程级全局的，
//     没有存活的匹配后自行停止。
//   - Indices are byte offsets. regexp2 works on runes, while every Go API and this repository's
//     scanners slice UTF-8 source by byte offset; all ...Index methods convert rune positions to byte
//     positions, so mixed Chinese/English source never panics or slices mid-rune. | 索引是字节偏移。
//     regexp2 以符文为单位工作，而 Go 的 API 与本仓的扫描器都按字节偏移切分 UTF-8 源码；所有
//     ...Index 方法都把符文位置换算成字节位置，中英混排的源码因此不会被切在半个符文上。
//
// Usage: | 用法：
//
//	var nameRe = regexpx.MustCompile(`^[a-z][a-z0-9_-]*$`)
//
//	if nameRe.MatchString(name) { ... }
package regexpx

import (
	"time"
	"unicode/utf8"

	"github.com/dlclark/regexp2"

	"github.com/shibingli/yd/core/errx"
)

// MatchTimeout is the per-match wall-clock budget. | MatchTimeout 是单次匹配的实际耗时预算。
//
// Legitimate patterns in this repository finish in microseconds; the budget only fires on
// catastrophic backtracking. regexp2 checks the deadline periodically, so the observed time to
// abort can exceed this value by one check period. | 本仓的合法模式都在微秒级完成，该预算只会被
// 灾难性回溯触发。regexp2 周期性检查截止时间，因此实际中止时间可能比该值多一个检查周期。
const MatchTimeout = 100 * time.Millisecond

var (
	// ErrCompile is returned when a pattern cannot be compiled. | ErrCompile 表示模式无法编译。
	ErrCompile = errx.New("compile regular expression failed | 编译正则表达式失败")
)

// Regexp is a compiled pattern. It is safe for concurrent use. | Regexp 是已编译的模式，可安全并发使用。
type Regexp struct {
	re  *regexp2.Regexp
	src string
}

// Compile parses expr and returns a compiled pattern. | Compile 解析 expr 并返回已编译的模式。
//
// The pattern is compiled in RE2 compatibility mode; a syntax error wraps ErrCompile so callers can
// test it with errors.Is. | 模式以 RE2 兼容模式编译；语法错误包装 ErrCompile，调用方可用 errors.Is 判定。
func Compile(expr string) (*Regexp, error) {
	re, err := regexp2.Compile(expr, regexp2.RE2)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrCompile, "pattern %[1]q | 模式 %[1]q", expr)
	}
	re.MatchTimeout = MatchTimeout
	return &Regexp{re: re, src: expr}, nil
}

// MustCompile is like Compile but panics when expr is invalid. | MustCompile 与 Compile 相同，但 expr 非法时 panic。
//
// It is meant for package-level initialization, where an invalid pattern is a programming error, and
// mirrors the contract of the standard library's MustCompile. | 它用于包级初始化：非法模式属于编程错误，
// 与标准库 MustCompile 的约定一致。
func MustCompile(expr string) *Regexp {
	re := regexp2.MustCompile(expr, regexp2.RE2)
	re.MatchTimeout = MatchTimeout
	return &Regexp{re: re, src: expr}
}

// QuoteMeta escapes every regular-expression metacharacter in s. | QuoteMeta 转义 s 中的全部正则元字符。
func QuoteMeta(s string) string { return regexp2.Escape(s) }

// String returns the source pattern. | String 返回模式原文。
func (r *Regexp) String() string { return r.src }

// MatchString reports whether s contains a match. | MatchString 报告 s 中是否存在匹配。
//
// A match that exceeds MatchTimeout is reported as no match; validation callers therefore fail
// closed. | 超过 MatchTimeout 的匹配按未匹配处理，因此校验类调用失败关闭。
func (r *Regexp) MatchString(s string) bool {
	ok, err := r.re.MatchString(s)
	return err == nil && ok
}

// Match reports whether b contains a match. | Match 报告 b 中是否存在匹配。
func (r *Regexp) Match(b []byte) bool { return r.MatchString(string(b)) }

// FindString returns the text of the first match, or the empty string when nothing matches. | FindString 返回首个匹配的文本，没有匹配时返回空串。
func (r *Regexp) FindString(s string) string {
	m, err := r.re.FindStringMatch(s)
	if err != nil || m == nil {
		return ""
	}
	starts := runeStarts(s)
	return s[starts[m.Index]:starts[m.Index+m.Length]]
}

// FindAllString returns the text of every non-overlapping match. | FindAllString 返回每个不重叠匹配的文本。
//
// n limits the number of matches; a negative n means all matches. | n 限制匹配数量，负数表示全部。
func (r *Regexp) FindAllString(s string, n int) []string {
	starts := runeStarts(s)
	var out []string
	r.eachMatch(s, n, func(m *regexp2.Match) bool {
		out = append(out, s[starts[m.Index]:starts[m.Index+m.Length]])
		return true
	})
	return out
}

// FindStringIndex returns the byte offset pair of the first match, or nil when nothing matches. | FindStringIndex 返回首个匹配的字节偏移对，没有匹配时返回 nil。
func (r *Regexp) FindStringIndex(s string) []int {
	m, err := r.re.FindStringMatch(s)
	if err != nil || m == nil {
		return nil
	}
	starts := runeStarts(s)
	return []int{starts[m.Index], starts[m.Index+m.Length]}
}

// ReplaceAllString replaces every match with repl and returns the result. | ReplaceAllString 用 repl 替换每个匹配并返回结果。
//
// The replacement follows .NET syntax ($1, $2, ${name}); a match that exceeds MatchTimeout leaves
// src unchanged, so a sanitizer never emits a partially processed string. | 替换串遵循 .NET 语法
// （$1、$2、${name}）；超过 MatchTimeout 的匹配使 src 原样返回，因此清洗逻辑绝不会输出只处理一半的字符串。
func (r *Regexp) ReplaceAllString(src, repl string) string {
	out, err := r.re.Replace(src, repl, 0, -1)
	if err != nil {
		return src
	}
	return out
}

// FindStringSubmatch returns the first match and its submatches, or nil when nothing matches. | FindStringSubmatch 返回首个匹配及其子匹配，没有匹配时返回 nil。
func (r *Regexp) FindStringSubmatch(s string) []string {
	m, err := r.re.FindStringMatch(s)
	if err != nil || m == nil {
		return nil
	}
	starts := runeStarts(s)
	groups := m.Groups()
	out := make([]string, len(groups))
	for i := range groups {
		out[i] = captureString(s, starts, &groups[i])
	}
	return out
}

// FindStringSubmatchIndex returns the byte offset pairs of the first match and its submatches. | FindStringSubmatchIndex 返回首个匹配及其子匹配的字节偏移对。
//
// Each group contributes two ints, and an unmatched group contributes -1, -1, matching the standard
// library's contract. | 每个分组贡献两个整数；未参与匹配的分组贡献 -1, -1，与标准库约定一致。
func (r *Regexp) FindStringSubmatchIndex(s string) []int {
	m, err := r.re.FindStringMatch(s)
	if err != nil || m == nil {
		return nil
	}
	return appendGroupIndices(nil, m.Groups(), runeStarts(s))
}

// FindAllStringIndex returns the byte offset pairs of every non-overlapping match. | FindAllStringIndex 返回每个不重叠匹配的字节偏移对。
//
// n limits the number of matches; a negative n means all matches. | n 限制匹配数量，负数表示全部。
func (r *Regexp) FindAllStringIndex(s string, n int) [][]int {
	starts := runeStarts(s)
	var out [][]int
	r.eachMatch(s, n, func(m *regexp2.Match) bool {
		out = append(out, []int{starts[m.Index], starts[m.Index+m.Length]})
		return true
	})
	return out
}

// FindAllStringSubmatch returns every non-overlapping match together with its submatches. | FindAllStringSubmatch 返回每个不重叠匹配及其子匹配。
func (r *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	starts := runeStarts(s)
	var out [][]string
	r.eachMatch(s, n, func(m *regexp2.Match) bool {
		groups := m.Groups()
		row := make([]string, len(groups))
		for i := range groups {
			row[i] = captureString(s, starts, &groups[i])
		}
		out = append(out, row)
		return true
	})
	return out
}

// FindAllStringSubmatchIndex returns the byte offset pairs of every match and its submatches. | FindAllStringSubmatchIndex 返回每个匹配及其子匹配的字节偏移对。
func (r *Regexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	starts := runeStarts(s)
	var out [][]int
	r.eachMatch(s, n, func(m *regexp2.Match) bool {
		out = append(out, appendGroupIndices(nil, m.Groups(), starts))
		return true
	})
	return out
}

// eachMatch walks the non-overlapping matches, stopping after n when n is positive. | eachMatch 遍历不重叠的匹配，n 为正数时在 n 个之后停止。
func (r *Regexp) eachMatch(s string, n int, fn func(*regexp2.Match) bool) {
	m, err := r.re.FindStringMatch(s)
	count := 0
	for err == nil && m != nil {
		if !fn(m) {
			return
		}
		count++
		if n > 0 && count >= n {
			return
		}
		m, err = r.re.FindNextMatch(m)
	}
}

// runeStarts returns a rune-index-to-byte-offset table whose final entry is len(s). | runeStarts 返回符文下标到字节偏移的映射表，最后一项是 len(s)。
func runeStarts(s string) []int {
	starts := make([]int, 0, utf8.RuneCountInString(s)+1)
	for i := range s {
		starts = append(starts, i)
	}
	return append(starts, len(s))
}

// captureString slices s for one group; an unmatched group yields the empty string. | captureString 为单个分组切出 s 的文本；未参与匹配的分组返回空串。
func captureString(s string, starts []int, g *regexp2.Group) string {
	if len(g.Captures) == 0 {
		return ""
	}
	start, end := starts[g.Index], starts[g.Index+g.Length]
	return s[start:end]
}

// appendGroupIndices appends one byte offset pair per group; an unmatched group yields -1, -1. | appendGroupIndices 为每个分组追加一对字节偏移；未参与匹配的分组追加 -1, -1。
func appendGroupIndices(dst []int, groups []regexp2.Group, starts []int) []int {
	for i := range groups {
		g := &groups[i]
		if len(g.Captures) == 0 {
			dst = append(dst, -1, -1)
			continue
		}
		dst = append(dst, starts[g.Index], starts[g.Index+g.Length])
	}
	return dst
}
