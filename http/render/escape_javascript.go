package render

// JavaScript escapers. | JavaScript 转义器。
//
// jsValEscaper serializes a value as a side-effect-free JS expression; the
// string escapers encode into the replacement sets html/template uses so the
// result can be embedded in script bodies, event handlers and template
// literals. | jsValEscaper 把值序列化为无副作用的 JS 表达式；字符串转义器按
// html/template 的替换集编码，使结果可安全嵌入脚本正文、事件处理器与模板字面量。

import (
	"fmt"
	"html/template"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/shibingli/yd/core/jsonx"
)

// indirectToJSONMarshaler dereferences pointers up to the first
// json.Marshaler or non-pointer. | indirectToJSONMarshaler 逐层解引用指针，直到
// 遇到 json.Marshaler 或非指针。
func indirectToJSONMarshaler(a any) any {
	if a == nil {
		return nil
	}
	v := reflect.ValueOf(a)
	for !v.Type().Implements(reflect.TypeFor[jsonMarshaler]()) && v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	return v.Interface()
}

// jsValEscaper escapes its input to a side-effect-free JavaScript expression.
// | jsValEscaper 把输入转义为无副作用的 JavaScript 表达式。
func jsValEscaper(args ...any) string {
	var a any
	if len(args) == 1 {
		a = indirectToJSONMarshaler(args[0])
		switch t := a.(type) {
		case template.JS:
			return string(t)
		case template.JSStr:
			return `"` + string(t) + `"`
		case jsonMarshaler:
			// Do not treat a marshaler as a Stringer. | 不把编组器当作 Stringer。
		case fmt.Stringer:
			a = t.String()
		}
	} else {
		for i, arg := range args {
			args[i] = indirectToJSONMarshaler(arg)
		}
		a = fmt.Sprint(args...)
	}
	b, err := jsonx.Marshal(a)
	if err != nil {
		return " /* " + sanitizeJSError(err.Error()) + " */null "
	}
	if len(b) == 0 {
		return " null "
	}
	first, _ := utf8.DecodeRune(b)
	last, _ := utf8.DecodeLastRune(b)
	pad := isJSIdentPart(first) || isJSIdentPart(last)
	var buf strings.Builder
	if pad {
		buf.WriteByte(' ')
	}
	written := 0
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		repl := ""
		if r == 0x2028 {
			repl = "\\u2028"
		} else if r == 0x2029 {
			repl = "\\u2029"
		}
		if repl != "" {
			buf.Write(b[written:i])
			buf.WriteString(repl)
			written = i + n
		}
		i += n
	}
	if buf.Len() != 0 {
		buf.Write(b[written:])
		if pad {
			buf.WriteByte(' ')
		}
		return buf.String()
	}
	return string(b)
}

// sanitizeJSError prevents a marshal error message from terminating a comment
// or the enclosing script element. | sanitizeJSError 防止编组错误信息终结注释或所在的脚本元素。
func sanitizeJSError(s string) string {
	s = strings.ReplaceAll(s, "*/", "* /")
	s = replaceFoldScript(s)
	s = strings.ReplaceAll(s, "<!--", `\x3C!--`)
	return s
}

// replaceFoldScript rewrites "<script" and "</script" case-insensitively to a
// form that cannot close the script element. | replaceFoldScript 以大小写不敏感方式
// 改写 "<script" 与 "</script"，使其无法闭合脚本元素。
func replaceFoldScript(s string) string {
	lower := strings.ToLower(s)
	var b strings.Builder
	written := 0
	for i := 0; i+6 <= len(lower); i++ {
		if lower[i] == '<' && strings.HasPrefix(lower[i:], "<script") {
			b.WriteString(s[written:i])
			b.WriteString(`\x3C`)
			b.WriteString(s[i+1 : i+7])
			written = i + 7
			i += 6
			continue
		}
		if lower[i] == '<' && i+8 <= len(lower) && strings.HasPrefix(lower[i:], "</script") {
			b.WriteString(s[written:i])
			b.WriteString(`\x3C`)
			b.WriteString(s[i+1 : i+8])
			written = i + 8
			i += 7
		}
	}
	if written == 0 {
		return s
	}
	b.WriteString(s[written:])
	return b.String()
}

// jsStrEscaper escapes a value for a JS quoted string literal. | jsStrEscaper 把值转义为 JS 引号字符串字面量内容。
func jsStrEscaper(args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeJSStr {
		return replaceJS(s, jsStrNormReplacementTable)
	}
	return replaceJS(s, jsStrReplacementTable)
}

// jsTmplLitEscaper escapes a value for a JS template literal. | jsTmplLitEscaper 把值转义为 JS 模板字面量内容。
func jsTmplLitEscaper(args ...any) string {
	s, _ := stringify(args...)
	return replaceJS(s, jsBqStrReplacementTable)
}

// jsRegexpEscaper escapes a value for a JS regular expression literal. | jsRegexpEscaper 把值转义为 JS 正则字面量内容。
func jsRegexpEscaper(args ...any) string {
	s, _ := stringify(args...)
	s = replaceJS(s, jsRegexpReplacementTable)
	if s == "" {
		return "(?:)"
	}
	return s
}

// replaceJS replaces runes according to table and encodes U+2028/U+2029. | replaceJS 按 table 替换字符并编码 U+2028/U+2029。
func replaceJS(s string, table []string) string {
	var b strings.Builder
	written := 0
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		var repl string
		switch {
		case int(r) < len(lowUnicodeReplacementTable):
			repl = lowUnicodeReplacementTable[r]
		case int(r) < len(table) && table[r] != "":
			repl = table[r]
		case r == ' ':
			repl = "\\u2028"
		case r == ' ':
			repl = "\\u2029"
		default:
			i += w
			continue
		}
		if written == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[written:i])
		b.WriteString(repl)
		i += w
		written = i
	}
	if written == 0 {
		return s
	}
	b.WriteString(s[written:])
	return b.String()
}

// isJSIdentPart reports whether r can appear in a JS identifier or numeric
// literal. | isJSIdentPart 报告 r 是否可出现在 JS 标识符或数字字面量中。
func isJSIdentPart(r rune) bool {
	switch {
	case r == '$':
		return true
	case '0' <= r && r <= '9':
		return true
	case 'A' <= r && r <= 'Z':
		return true
	case r == '_':
		return true
	case 'a' <= r && r <= 'z':
		return true
	}
	return false
}

// lowUnicodeReplacementTable escapes control characters for JS strings. | lowUnicodeReplacementTable 转义 JS 字符串中的控制字符。
var lowUnicodeReplacementTable = []string{
	0: `\u0000`, 1: `\u0001`, 2: `\u0002`, 3: `\u0003`, 4: `\u0004`, 5: `\u0005`, 6: `\u0006`,
	'\a': `\u0007`,
	'\b': `\u0008`,
	'\t': `\t`,
	'\n': `\n`,
	'\v': `\u000b`,
	'\f': `\f`,
	'\r': `\r`,
	0xe:  `\u000e`, 0xf: `\u000f`, 0x10: `\u0010`, 0x11: `\u0011`, 0x12: `\u0012`, 0x13: `\u0013`,
	0x14: `\u0014`, 0x15: `\u0015`, 0x16: `\u0016`, 0x17: `\u0017`, 0x18: `\u0018`, 0x19: `\u0019`,
	0x1a: `\u001a`, 0x1b: `\u001b`, 0x1c: `\u001c`, 0x1d: `\u001d`, 0x1e: `\u001e`, 0x1f: `\u001f`,
}

// jsStrReplacementTable escapes characters in a JS quoted string. | jsStrReplacementTable 转义 JS 引号字符串中的字符。
var jsStrReplacementTable = []string{
	0:    "\\u0000",
	'\t': "\\t",
	'\n': "\\n",
	'\v': "\\u000b",
	'\f': "\\f",
	'\r': "\\r",
	'"':  "\\u0022",
	'`':  "\\u0060",
	'&':  "\\u0026",
	'\'': "\\u0027",
	'+':  "\\u002b",
	'/':  "\\/",
	'<':  "\\u003c",
	'>':  "\\u003e",
	'\\': "\\\\",
}

// jsBqStrReplacementTable is jsStrReplacementTable plus template-literal
// specials. | jsBqStrReplacementTable 在 jsStrReplacementTable 基础上加入模板字面量特殊字符。
var jsBqStrReplacementTable = []string{
	0:    "\\u0000",
	'\t': "\\t",
	'\n': "\\n",
	'\v': "\\u000b",
	'\f': "\\f",
	'\r': "\\r",
	'"':  "\\u0022",
	'`':  "\\u0060",
	'&':  "\\u0026",
	'\'': "\\u0027",
	'+':  "\\u002b",
	'/':  "\\/",
	'<':  "\\u003c",
	'>':  "\\u003e",
	'\\': "\\\\",
	'$':  "\\u0024",
	'{':  "\\u007b",
	'}':  "\\u007d",
}

// jsStrNormReplacementTable is jsStrReplacementTable without over-encoding
// existing backslash escapes. | jsStrNormReplacementTable 是不过度编码既有反斜杠转义的版本。
var jsStrNormReplacementTable = []string{
	0:    "\\u0000",
	'\t': "\\t",
	'\n': "\\n",
	'\v': "\\u000b",
	'\f': "\\f",
	'\r': "\\r",
	'"':  "\\u0022",
	'&':  "\\u0026",
	'\'': "\\u0027",
	'`':  "\\u0060",
	'+':  "\\u002b",
	'/':  "\\/",
	'<':  "\\u003c",
	'>':  "\\u003e",
}

// jsRegexpReplacementTable escapes regular-expression metacharacters. | jsRegexpReplacementTable 转义正则元字符。
var jsRegexpReplacementTable = []string{
	0:    "\\u0000",
	'\t': "\\t",
	'\n': "\\n",
	'\v': "\\u000b",
	'\f': "\\f",
	'\r': "\\r",
	'"':  "\\u0022",
	'$':  "\\$",
	'&':  "\\u0026",
	'\'': "\\u0027",
	'(':  "\\(",
	')':  "\\)",
	'*':  "\\*",
	'+':  "\\u002b",
	'-':  "\\-",
	'.':  "\\.",
	'/':  "\\/",
	'<':  "\\u003c",
	'>':  "\\u003e",
	'?':  "\\?",
	'[':  "\\[",
	'\\': "\\\\",
	']':  "\\]",
	'^':  "\\^",
	'{':  "\\{",
	'|':  "\\|",
	'}':  "\\}",
}
