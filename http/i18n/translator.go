package i18n

import (
	"fmt"
	"strings"
)

// Translator renders the messages of one language. | Translator 渲染某一语言的消息。
// It only holds a language tag and its bundle, so it is immutable, safe for concurrent use and always reflects the latest Reload of its bundle. | 它只持有语言标签与所属 Bundle，因此不可变、可并发使用，并始终反映其 Bundle 的最新 Reload 结果。
// `T` itself is a valid template function (`func(string, ...any) string`), so `t.T` can be registered directly in a render.FuncMap. | `T` 本身就是合法的模板函数（`func(string, ...any) string`），因此 `t.T` 可直接注册进 render.FuncMap。
type Translator struct {
	bundle *Bundle // Owning bundle. | 所属 Bundle。
	lang   string  // Resolved language tag. | 已解析的语言标签。
}

// Lang returns the resolved language tag of this translator. | Lang 返回该译者已解析的语言标签。
// A nil translator returns an empty string. | nil 译者返回空串。
func (t *Translator) Lang() string {
	if t == nil {
		return ""
	}
	return t.lang
}

// CacheKey returns a stable identity for this translator: one bundle plus one language. | CacheKey 返回译者的稳定身份：一个 Bundle 加一种语言。
//
// Two translators with the same key answer every T identically until the bundle reloads; Reload keeps
// the identity and swaps the snapshot behind it, so a consumer may cache work against this key. | 相同键的
// 两个译者对每次 T 的答案都相同，直到 Bundle 重载；Reload 保持身份、只替换其后的快照，因此消费方可以
// 据此键缓存结果。
func (t *Translator) CacheKey() string {
	if t == nil || t.bundle == nil {
		return ""
	}
	return t.bundle.cacheKey(t.lang)
}

// T translates key and fills the placeholders with args in order. | T 翻译 key，并按顺序用 args 填充占位符。
// Both placeholder styles are supported: `{{.Name}}` (the field name is cosmetic, arguments are positional) and printf verbs such as `%s` / `%d`. | 两种占位风格都支持：`{{.Name}}`（字段名仅作提示，参数按位置填充）与 `%s` / `%d` 等 printf 动词。
// Named placeholders are consumed first, then the remaining args feed fmt.Sprintf only when the message still contains a verb, so a message without placeholders is returned verbatim and a plain percent sign never produces Sprintf noise. | 先消费 `{{.Name}}` 占位符，剩余参数仅在消息仍含动词时交给 fmt.Sprintf，因此不含占位符的消息原样返回，普通百分号也不会产生 Sprintf 噪声。
// When args run out the leftover placeholders are kept as-is. | 参数不足时保留未填充的占位符。
// A printf argument mismatch is detected, counted by FormatErrors and answered with the raw message instead of Go's `%!d(string=…)` diagnostic syntax; the optional problem handler is notified. | printf 参数不匹配会被检测、计入 FormatErrors，并返回原始消息而不是 Go 的 `%!d(string=…)` 调试语法；可选问题处理器会收到通知。
// Unknown keys return the key itself (and increment Misses), and a nil translator returns the key as well, so a missing middleware or a typo never panics a handler. | 未知键返回键本身（并递增 Misses），nil 译者同样返回 key，因此中间件缺失或键名笔误都不会让处理器 panic。
func (t *Translator) T(key string, args ...any) string {
	if t == nil || t.bundle == nil {
		return key
	}
	msg, lang, ok := t.bundle.lookupMessage(t.lang, key)
	if !ok {
		t.bundle.misses.Add(1)
		t.bundle.report(Problem{Kind: ProblemMiss, Lang: t.lang, Key: key})
		return key
	}
	out, ok := msg.render(args)
	if !ok {
		t.bundle.formatErrs.Add(1)
		t.bundle.report(Problem{Kind: ProblemFormat, Lang: lang, Key: key})
		return msg.raw
	}
	return out
}

// segment is one precompiled piece of a message. | segment 是消息中一段预编译片段。
type segment struct {
	text string // Literal, or the raw `{{.X}}` block for an unfilled placeholder. | 字面量，或未填充占位符的原始 `{{.X}}` 片段。
	arg  int    // Placeholder ordinal; -1 means a literal. | 占位符序号；-1 表示字面量。
}

// message is a precompiled catalog entry. | message 是预编译后的目录条目。
//
// Compilation happens once per Reload; translation then walks the segments without splitting keys or
// parsing the message again. A message with neither arguments nor placeholders is returned as-is. | 编译在每次
// Reload 时做一次；翻译随后只遍历片段，不再拆键或重新解析消息。既无参数也无占位符的消息原样返回。
type message struct {
	raw      string    // Original message, used verbatim and as the format-error fallback. | 原始消息，用于原样返回与格式化失败回退。
	segments []segment // nil when there is no named placeholder. | 没有命名占位符时为 nil。
	named    int       // Number of named placeholders. | 命名占位符数量。
	verbs    bool      // Whether the raw message contains a printf verb. | 原始消息是否含 printf 动词。
}

// compileMessage parses the named placeholders once. | compileMessage 一次性解析命名占位符。
func compileMessage(raw string) *message {
	m := &message{raw: raw, verbs: hasVerbs(raw)}
	if !strings.Contains(raw, "{{") {
		return m
	}

	var segments []segment
	last := 0
	for i := 0; i < len(raw); {
		rel := strings.Index(raw[i:], "{{")
		if rel < 0 {
			break
		}
		start := i + rel
		relEnd := strings.Index(raw[start:], "}}")
		if relEnd < 0 {
			break
		}
		end := start + relEnd + len("}}")
		if isNamedPlaceholder(raw[start:end]) {
			if start > last {
				segments = append(segments, segment{text: raw[last:start], arg: -1})
			}
			segments = append(segments, segment{text: raw[start:end], arg: m.named})
			m.named++
			last = end
		}
		i = end
	}
	if m.named == 0 {
		return m
	}
	if last < len(raw) {
		segments = append(segments, segment{text: raw[last:], arg: -1})
	}
	m.segments = segments
	return m
}

// render fills the message with args. The second result is false when printf reported a type
// mismatch, in which case the caller answers with the raw message. | render 用 args 填充消息。
// 第二个结果为 false 表示 printf 报告了类型不匹配，此时调用方返回原始消息。
func (m *message) render(args []any) (string, bool) {
	if len(args) == 0 {
		return m.raw, true
	}
	if m.named == 0 {
		if !m.verbs {
			return m.raw, true
		}
		out := fmt.Sprintf(m.raw, args...)
		return out, !formatFailed(out)
	}
	// A message that is exactly one placeholder needs no builder at all. | 恰好只有一个占位符的消息无需 Builder。
	if m.named == 1 && len(m.segments) == 1 && !m.verbs {
		return formatArg(args[0]), true
	}

	var b strings.Builder
	b.Grow(len(m.raw) + 16*min(m.named, len(args)))
	used := 0
	for _, seg := range m.segments {
		if seg.arg >= 0 && seg.arg < len(args) {
			used++
		}
	}
	// Percent-escape substituted values only when the assembled string really goes through
	// Sprintf; otherwise a literal "%%" would leak into the output. | 仅当拼接结果确实会交给
	// Sprintf 时才转义替换值；否则字面 "%%" 会漏进输出。
	escapeValues := m.verbs && used < len(args)
	for _, seg := range m.segments {
		if seg.arg < 0 || seg.arg >= len(args) {
			b.WriteString(seg.text)
			continue
		}
		if escapeValues {
			b.WriteString(formatArgEscaped(args[seg.arg]))
		} else {
			b.WriteString(formatArg(args[seg.arg]))
		}
	}
	out := b.String()
	if !m.verbs || used >= len(args) {
		return out, true
	}
	out = fmt.Sprintf(out, args[used:]...)
	return out, !formatFailed(out)
}

// isNamedPlaceholder reports whether s has the exact `{{.Field}}` shape. | isNamedPlaceholder 判断 s 是否严格符合 `{{.Field}}` 形状。
func isNamedPlaceholder(s string) bool {
	if !strings.HasPrefix(s, "{{") || !strings.HasSuffix(s, "}}") {
		return false
	}

	inner := strings.TrimSpace(s[len("{{") : len(s)-len("}}")])
	after, ok := strings.CutPrefix(inner, ".")
	if !ok || after == "" {
		return false
	}
	for i := 0; i < len(after); i++ {
		c := after[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '_', c == '.', c == '-':
		default:
			return false
		}
	}
	return true
}

// formatArg renders one positional argument. | formatArg 渲染一个位置参数。
//
// Every percent sign is doubled: the rendered value is later embedded in a format string handed
// to Sprintf, and an unescaped "%s"/"%2000000000d" in user data would otherwise become a format
// directive (argument theft, huge allocations or diagnostic noise). | 每个百分号都翻倍：渲染结果随后
// 会被嵌入交给 Sprintf 的格式串，用户数据里未转义的 "%s"/"%2000000000d" 否则会变成格式指令
// （偷取后续参数、巨额分配或诊断噪声）。
func formatArg(arg any) string {
	if s, ok := arg.(string); ok {
		return s
	}
	return fmt.Sprint(arg)
}

// formatArgEscaped is formatArg with percent signs doubled for a later Sprintf. | formatArgEscaped
// 是百分号翻倍后的 formatArg，供随后的 Sprintf 使用。
func formatArgEscaped(arg any) string {
	s := formatArg(arg)
	if strings.ContainsRune(s, '%') {
		s = strings.ReplaceAll(s, "%", "%%")
	}
	return s
}

// hasVerbs reports whether format contains a printf verb. | hasVerbs 判断 format 是否含有 printf 动词。
// Thanks to it, plain percent signs in a message never trigger Sprintf noise like `%!d(MISSING)`. | 有了它，消息里的普通百分号不会触发形如 `%!d(MISSING)` 的 Sprintf 噪声。
func hasVerbs(format string) bool {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		j := i + 1
		if j < len(format) && format[j] == '%' {
			i = j
			continue
		}
		// Explicit argument index: %[2]s. | 显式参数下标：%[2]s。
		j = skipArgIndex(format, j)
		// Flags. | 标志位。
		for j < len(format) && strings.IndexByte("#0+- .", format[j]) >= 0 {
			j++
		}
		// Width: digits or * (with an optional [n] index after it). | 宽度：数字或 *（其后可带 [n] 下标）。
		if j < len(format) && format[j] == '*' {
			j = skipArgIndex(format, j+1)
		} else {
			for j < len(format) && format[j] >= '0' && format[j] <= '9' {
				j++
			}
		}
		// Precision. | 精度。
		if j < len(format) && format[j] == '.' {
			j++
			if j < len(format) && format[j] == '*' {
				j = skipArgIndex(format, j+1)
			} else {
				for j < len(format) && format[j] >= '0' && format[j] <= '9' {
					j++
				}
			}
		}
		if j < len(format) && strings.IndexByte("vTtbcdoOqxXUeEfFgGsp", format[j]) >= 0 {
			return true
		}
	}
	return false
}

// skipArgIndex consumes an optional "[n]" explicit argument index. | skipArgIndex 消费可选的 "[n]" 显式参数下标。
func skipArgIndex(format string, j int) int {
	if j >= len(format) || format[j] != '[' {
		return j
	}
	k := j + 1
	for k < len(format) && format[k] >= '0' && format[k] <= '9' {
		k++
	}
	if k < len(format) && format[k] == ']' && k > j+1 {
		return k + 1
	}
	return j
}

// formatFailed reports a Sprintf diagnostic in out. | formatFailed 报告 out 中是否出现 Sprintf 诊断。
//
// Sprintf diagnostics take the shape "%!verb(...)"; a legitimate value such as "100%!" does not
// contain "%!(". | Sprintf 诊断形如 "%!verb(...)"；"100%!" 这类合法取值不含 "%!("。
func formatFailed(out string) bool {
	for i := 0; i+2 < len(out); i++ {
		if out[i] != '%' || out[i+1] != '!' {
			continue
		}
		if out[i+2] == '(' {
			return true // %!(EXTRA ...) | 多余参数
		}
		// Sprintf diagnostics are "%!<verb>(" such as "%!d(string=...)" or "%!s(MISSING)".
		// | Sprintf 诊断形如 "%!<动词>("，例如 "%!d(string=...)" 或 "%!s(MISSING)"。
		if i+3 < len(out) && out[i+3] == '(' {
			return true
		}
	}
	return false
}
