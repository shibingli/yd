// Package regexpx is the repository's single regular-expression entry point. | Package regexpx 是本仓唯一的正则表达式入口。
//
// It owns two engines behind one API and chooses per pattern: | 它在一个 API 背后持有两个引擎，并按模式选择：
//
//   - A pattern that the standard library accepts runs on the RE2 engine: linear time, no
//     backtracking, zero allocations on the hot paths, and no timeout needed. This is the path every
//     ordinary pattern takes. | 标准库能接受的模式走 RE2 引擎：线性时间、不回溯、热路径零分配、
//     无需超时。所有普通模式都走这条路。
//   - A pattern that needs .NET constructs (lookaround, backreferences, named groups) does not
//     compile in the standard library, so it runs on github.com/dlclark/regexp2 in RE2 compatibility
//     mode, with a mandatory match timeout. regexp2 backtracks, so a hostile input could otherwise
//     trigger catastrophic backtracking; a match that exceeds MatchTimeout is reported as "no
//     match", which makes validation callers fail closed. | 需要 .NET 构造（环视、反向引用、命名
//     分组）的模式在标准库中无法编译，因此走 github.com/dlclark/regexp2 的 RE2 兼容模式，并强制
//     匹配超时。regexp2 会回溯，恶意输入可能触发灾难性回溯；超过 MatchTimeout 的匹配按「未匹配」
//     处理，使校验类调用失败关闭。
//
// The dual engine is not an optimization detail but a correctness requirement: regexp2's iterator
// re-converts the remaining input to runes on every FindNextMatch, so FindAll over a large file is
// quadratic and can silently truncate under a timeout. Bulk scanners in this repository (CSS
// coverage, authz source scanning) therefore depend on the RE2 fast path. | 双引擎不是优化细节而是
// 正确性要求：regexp2 的迭代器在每次 FindNextMatch 时都会把剩余输入重新转成符文，因此对大文件的
// FindAll 是二次复杂度，并可能在超时后静默截断。本仓的批量扫描器（CSS 覆盖、authz 源码扫描）
// 因此依赖 RE2 快路径。
//
// Indices are always byte offsets, regardless of the engine: regexp2 works on runes, so its results
// are converted before they reach a caller. | 无论走哪个引擎，索引一律是字节偏移：regexp2 以符文
// 工作，因此其结果在返回调用方之前完成换算。
//
// Usage: | 用法：
//
//	var nameRe = regexpx.MustCompile(`^[a-z][a-z0-9_-]*$`)
//
//	if nameRe.MatchString(name) { ... }
package regexpx

import (
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/dlclark/regexp2"

	"github.com/shibingli/yd/core/errx"
)

// MatchTimeout is the per-match wall-clock budget for the regexp2 engine. | MatchTimeout 是 regexp2 引擎单次匹配的实际耗时预算。
//
// The RE2 fast path does not need a timeout: it cannot backtrack, so its cost is linear in the input.
// This budget only fires on catastrophic backtracking in a .NET-only pattern. regexp2 checks the
// deadline periodically, so the observed time to abort can exceed this value by one check period. | RE2
// 快路径不需要超时：它不会回溯，代价与输入长度线性相关。该预算只会被 .NET 专有模式中的灾难性
// 回溯触发。regexp2 周期性检查截止时间，因此实际中止时间可能比该值多一个检查周期。
const MatchTimeout = 100 * time.Millisecond

var (
	// ErrCompile is returned when a pattern cannot be compiled by either engine. | ErrCompile 表示两个引擎都无法编译该模式。
	ErrCompile = errx.New("compile regular expression failed | 编译正则表达式失败")
)

// Regexp is a compiled pattern. It is safe for concurrent use. | Regexp 是已编译的模式，可安全并发使用。
type Regexp struct {
	// re2 is the standard-library engine, set when the pattern is RE2-compatible. | re2 是标准库引擎，模式兼容 RE2 时非空。
	re2 *regexp.Regexp
	// re2x is the compatibility engine, always compiled, used only when re2 is nil. | re2x 是兼容引擎，总是编译，仅在 re2 为空时使用。
	re2x *regexp2.Regexp
	src  string
}

// Compile parses expr and returns a compiled pattern. | Compile 解析 expr 并返回已编译的模式。
//
// The pattern is first compiled with regexp2 in RE2 compatibility mode, which decides whether the
// syntax is valid at all; when the standard library also accepts it, the fast RE2 engine is used for
// every operation. A syntax error wraps ErrCompile so callers can test it with errors.Is. | 模式先用
// regexp2 的 RE2 兼容模式编译，以判定语法是否合法；当标准库也能接受时，所有操作都走 RE2 快引擎。
// 语法错误包装 ErrCompile，调用方可用 errors.Is 判定。
func Compile(expr string) (*Regexp, error) {
	compat, err := regexp2.Compile(expr, regexp2.RE2)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrCompile, "pattern %[1]q | 模式 %[1]q", expr)
	}
	compat.MatchTimeout = MatchTimeout
	r := &Regexp{src: expr, re2x: compat}
	if fast, err := regexp.Compile(expr); err == nil {
		r.re2 = fast
	}
	return r, nil
}

// MustCompile is like Compile but panics when expr is invalid. | MustCompile 与 Compile 相同，但 expr 非法时 panic。
//
// It is meant for package-level initialization, where an invalid pattern is a programming error, and
// mirrors the contract of the standard library's MustCompile. | 它用于包级初始化：非法模式属于编程错误，
// 与标准库 MustCompile 的约定一致。
func MustCompile(expr string) *Regexp {
	compat := regexp2.MustCompile(expr, regexp2.RE2)
	compat.MatchTimeout = MatchTimeout
	r := &Regexp{src: expr, re2x: compat}
	if fast, err := regexp.Compile(expr); err == nil {
		r.re2 = fast
	}
	return r
}

// QuoteMeta escapes every regular-expression metacharacter in s. | QuoteMeta 转义 s 中的全部正则元字符。
//
// The escaping is performed by regexp2 so that a quoted string is safe for either engine. | 转义由 regexp2
// 完成，因此加引号后的字符串对两个引擎都安全。
func QuoteMeta(s string) string { return regexp2.Escape(s) }

// String returns the source pattern. | String 返回模式原文。
func (r *Regexp) String() string { return r.src }

// MatchString reports whether s contains a match. | MatchString 报告 s 中是否存在匹配。
//
// On the regexp2 path, a match that exceeds MatchTimeout is reported as no match; validation callers
// therefore fail closed. | 在 regexp2 路径上，超过 MatchTimeout 的匹配按未匹配处理，因此校验类调用失败关闭。
func (r *Regexp) MatchString(s string) bool {
	if r.re2 != nil {
		return r.re2.MatchString(s)
	}
	ok, err := r.re2x.MatchString(s)
	return err == nil && ok
}

// Match reports whether b contains a match. | Match 报告 b 中是否存在匹配。
func (r *Regexp) Match(b []byte) bool {
	if r.re2 != nil {
		return r.re2.Match(b)
	}
	return r.MatchString(string(b))
}

// FindString returns the text of the first match, or the empty string when nothing matches. | FindString 返回首个匹配的文本，没有匹配时返回空串。
func (r *Regexp) FindString(s string) string {
	if r.re2 != nil {
		return r.re2.FindString(s)
	}
	m, err := r.re2x.FindStringMatch(s)
	if err != nil || m == nil {
		return ""
	}
	starts := runeStarts(s)
	return s[starts[m.Index]:starts[m.Index+m.Length]]
}

// FindStringIndex returns the byte offset pair of the first match, or nil when nothing matches. | FindStringIndex 返回首个匹配的字节偏移对，没有匹配时返回 nil。
func (r *Regexp) FindStringIndex(s string) []int {
	if r.re2 != nil {
		return r.re2.FindStringIndex(s)
	}
	m, err := r.re2x.FindStringMatch(s)
	if err != nil || m == nil {
		return nil
	}
	starts := runeStarts(s)
	return []int{starts[m.Index], starts[m.Index+m.Length]}
}

// FindStringSubmatch returns the first match and its submatches, or nil when nothing matches. | FindStringSubmatch 返回首个匹配及其子匹配，没有匹配时返回 nil。
func (r *Regexp) FindStringSubmatch(s string) []string {
	if r.re2 != nil {
		return r.re2.FindStringSubmatch(s)
	}
	m, err := r.re2x.FindStringMatch(s)
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
	if r.re2 != nil {
		return r.re2.FindStringSubmatchIndex(s)
	}
	m, err := r.re2x.FindStringMatch(s)
	if err != nil || m == nil {
		return nil
	}
	return appendGroupIndices(nil, m.Groups(), runeStarts(s))
}

// FindAllString returns the text of every non-overlapping match. | FindAllString 返回每个不重叠匹配的文本。
//
// n limits the number of matches; a negative n means all matches. | n 限制匹配数量，负数表示全部。
func (r *Regexp) FindAllString(s string, n int) []string {
	if r.re2 != nil {
		return r.re2.FindAllString(s, n)
	}
	starts := runeStarts(s)
	var out []string
	r.eachMatch(s, n, func(m *regexp2.Match) bool {
		out = append(out, s[starts[m.Index]:starts[m.Index+m.Length]])
		return true
	})
	return out
}

// FindAllStringIndex returns the byte offset pairs of every non-overlapping match. | FindAllStringIndex 返回每个不重叠匹配的字节偏移对。
//
// n limits the number of matches; a negative n means all matches. | n 限制匹配数量，负数表示全部。
func (r *Regexp) FindAllStringIndex(s string, n int) [][]int {
	if r.re2 != nil {
		return r.re2.FindAllStringIndex(s, n)
	}
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
	if r.re2 != nil {
		return r.re2.FindAllStringSubmatch(s, n)
	}
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
	if r.re2 != nil {
		return r.re2.FindAllStringSubmatchIndex(s, n)
	}
	starts := runeStarts(s)
	var out [][]int
	r.eachMatch(s, n, func(m *regexp2.Match) bool {
		out = append(out, appendGroupIndices(nil, m.Groups(), starts))
		return true
	})
	return out
}

// ReplaceAllString replaces every match with repl and returns the result. | ReplaceAllString 用 repl 替换每个匹配并返回结果。
//
// The replacement follows the contract of the engine that runs: Go's `$name` rules on the RE2 fast
// path, .NET syntax on the regexp2 path. A regexp2 match that exceeds MatchTimeout leaves src
// unchanged, so a sanitizer never emits a partially processed string. | 替换遵循实际执行引擎的约定：
// RE2 快路径按 Go 的 `$name` 规则，regexp2 路径按 .NET 语法。regexp2 匹配超过 MatchTimeout 时
// src 原样返回，因此清洗逻辑绝不会输出只处理一半的字符串。
func (r *Regexp) ReplaceAllString(src, repl string) string {
	if r.re2 != nil {
		return r.re2.ReplaceAllString(src, repl)
	}
	out, err := r.re2x.Replace(src, repl, 0, -1)
	if err != nil {
		return src
	}
	return out
}

// eachMatch walks the non-overlapping matches, stopping after n when n is positive. | eachMatch 遍历不重叠的匹配，n 为正数时在 n 个之后停止。
func (r *Regexp) eachMatch(s string, n int, fn func(*regexp2.Match) bool) {
	m, err := r.re2x.FindStringMatch(s)
	count := 0
	for err == nil && m != nil {
		if !fn(m) {
			return
		}
		count++
		if n > 0 && count >= n {
			return
		}
		m, err = r.re2x.FindNextMatch(m)
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
