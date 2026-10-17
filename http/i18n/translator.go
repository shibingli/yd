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

// T translates key and fills the placeholders with args in order. | T 翻译 key，并按顺序用 args 填充占位符。
// Both placeholder styles are supported: `{{.Name}}` (the field name is cosmetic, arguments are positional) and printf verbs such as `%s` / `%d`. | 两种占位风格都支持：`{{.Name}}`（字段名仅作提示，参数按位置填充）与 `%s` / `%d` 等 printf 动词。
// Named placeholders are consumed first, then the remaining args feed fmt.Sprintf only when the message still contains a verb, so a message without placeholders is returned verbatim and a plain percent sign never produces Sprintf noise. | 先消费 `{{.Name}}` 占位符，剩余参数仅在消息仍含动词时交给 fmt.Sprintf，因此不含占位符的消息原样返回，普通百分号也不会产生 Sprintf 噪声。
// When args run out the leftover placeholders are kept as-is. | 参数不足时保留未填充的占位符。
// Unknown keys return the key itself, and a nil translator returns the key as well, so a missing middleware or a typo never panics a handler. | 未知 key 返回 key 本身，nil 译者同样返回 key，因此中间件缺失或键名笔误都不会让处理器 panic。
func (t *Translator) T(key string, args ...any) string {
	message, ok := t.lookup(key)
	if !ok {
		return key
	}
	return interpolate(message, args)
}

// lookup resolves key through the bundle with default-language fallback. | lookup 通过 Bundle 解析 key，并在缺失时回退默认语言。
func (t *Translator) lookup(key string) (string, bool) {
	if t == nil || t.bundle == nil {
		return "", false
	}
	return t.bundle.lookup(t.lang, key)
}

// interpolate fills placeholders of both supported styles. | interpolate 填充两种受支持风格的占位符。
func interpolate(message string, args []any) string {
	if message == "" || len(args) == 0 {
		return message
	}

	used := 0
	if strings.Contains(message, "{{") {
		message = fillNamed(message, args, &used)
	}
	if used < len(args) && hasVerbs(message) {
		return fmt.Sprintf(message, args[used:]...)
	}
	return message
}

// fillNamed replaces `{{.Field}}` placeholders left to right with the next arg. | fillNamed 从左到右用下一个参数替换 `{{.Field}}` 占位符。
// Placeholders left without a matching arg, and `{{...}}` blocks that are not field placeholders, are copied through unchanged. | 没有对应参数的占位符，以及不是字段占位符的 `{{...}}` 片段，都原样保留。
func fillNamed(message string, args []any, used *int) string {
	var sb strings.Builder
	sb.Grow(len(message))

	for i := 0; i < len(message); {
		start := strings.Index(message[i:], "{{")
		if start < 0 {
			sb.WriteString(message[i:])
			break
		}
		start += i

		end := strings.Index(message[start:], "}}")
		if end < 0 {
			sb.WriteString(message[i:])
			break
		}
		end += start + len("}}")

		if !isNamedPlaceholder(message[start:end]) || *used >= len(args) {
			sb.WriteString(message[i:end])
			i = end
			continue
		}

		sb.WriteString(message[i:start])
		sb.WriteString(formatArg(args[*used]))
		*used++
		i = end
	}
	return sb.String()
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
func formatArg(arg any) string {
	if s, ok := arg.(string); ok {
		return s
	}
	return fmt.Sprint(arg)
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
		for j < len(format) && strings.IndexByte("#0+- .123456789", format[j]) >= 0 {
			j++
		}
		if j < len(format) && strings.IndexByte("vTtbcdoOqxXUeEfFgGsp", format[j]) >= 0 {
			return true
		}
	}
	return false
}
