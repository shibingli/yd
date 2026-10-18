package render

// CSS escapers. | CSS 转义器。
//
// cssValueFilter validates a value in a style attribute or declaration, and
// cssEscaper encodes CSS metacharacters with hex escapes so the result cannot
// terminate the declaration or the enclosing attribute. | cssValueFilter 校验
// style 属性或声明中的值，cssEscaper 用十六进制转义编码 CSS 元字符，使结果无法
// 终结声明或所在的属性。

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// isCSSNmchar reports whether r may appear in a CSS identifier. | isCSSNmchar 报告 r 是否可出现在 CSS 标识符中。
func isCSSNmchar(r rune) bool {
	return 'a' <= r && r <= 'z' ||
		'A' <= r && r <= 'Z' ||
		'0' <= r && r <= '9' ||
		r == '-' || r == '_' ||
		0x80 <= r && r <= 0xd7ff ||
		0xe000 <= r && r <= 0xfffd ||
		0x10000 <= r && r <= 0x10ffff
}

// isHex reports whether c is a hexadecimal digit. | isHex 报告 c 是否为十六进制数字。
func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// hexDecode decodes a short hexadecimal sequence. | hexDecode 解码短十六进制序列。
func hexDecode(s []byte) rune {
	var n rune
	for _, c := range s {
		n <<= 4
		switch {
		case '0' <= c && c <= '9':
			n |= rune(c - '0')
		case 'a' <= c && c <= 'f':
			n |= rune(c-'a') + 10
		case 'A' <= c && c <= 'F':
			n |= rune(c-'A') + 10
		}
	}
	return n
}

// skipCSSSpace skips one CSS whitespace character (CRLF counts as one). | skipCSSSpace 跳过一个 CSS 空白字符（CRLF 算一个）。
func skipCSSSpace(c []byte) []byte {
	if len(c) == 0 {
		return c
	}
	switch c[0] {
	case '\t', '\n', '\f', ' ':
		return c[1:]
	case '\r':
		if len(c) >= 2 && c[1] == '\n' {
			return c[2:]
		}
		return c[1:]
	}
	return c
}

// isCSSSpace reports whether b is CSS whitespace. | isCSSSpace 报告 b 是否为 CSS 空白。
func isCSSSpace(b byte) bool {
	switch b {
	case '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// decodeCSS decodes CSS3 backslash escapes. | decodeCSS 解码 CSS3 反斜杠转义。
func decodeCSS(s []byte) []byte {
	if indexByte(s, '\\') < 0 {
		return s
	}
	out := make([]byte, 0, len(s))
	for len(s) != 0 {
		i := indexByte(s, '\\')
		if i < 0 {
			out = append(out, s...)
			break
		}
		out = append(out, s[:i]...)
		s = s[i:]
		if len(s) < 2 {
			break
		}
		if isHex(s[1]) {
			j := 2
			for j < len(s) && j < 7 && isHex(s[j]) {
				j++
			}
			r := hexDecode(s[1:j])
			if r > unicode.MaxRune {
				r, j = r/16, j-1
			}
			out = utf8.AppendRune(out, r)
			s = skipCSSSpace(s[j:])
			continue
		}
		_, n := utf8.DecodeRune(s[1:])
		out = append(out, s[1:1+n]...)
		s = s[1+n:]
	}
	return out
}

// indexByte returns the first index of c in s, or -1. | indexByte 返回 c 在 s 中的首个下标，未找到返回 -1。
func indexByte(s []byte, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// cssEscaper escapes HTML and CSS special characters with hex escapes. | cssEscaper 用十六进制转义编码 HTML 与 CSS 特殊字符。
func cssEscaper(args ...any) string {
	s, _ := stringify(args...)
	var b strings.Builder
	written := 0
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		var repl string
		if int(r) < len(cssReplacementTable) {
			repl = cssReplacementTable[r]
		}
		if repl == "" {
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
		if repl != `\\` && (written == len(s) || isHex(s[written]) || isCSSSpace(s[written])) {
			b.WriteByte(' ')
		}
	}
	if written == 0 {
		return s
	}
	b.WriteString(s[written:])
	return b.String()
}

// cssReplacementTable escapes CSS metacharacters. | cssReplacementTable 转义 CSS 元字符。
var cssReplacementTable = []string{
	0:    `\0`,
	'\t': `\9`,
	'\n': `\a`,
	'\f': `\c`,
	'\r': `\d`,
	'"':  `\22`,
	'&':  `\26`,
	'\'': `\27`,
	'(':  `\28`,
	')':  `\29`,
	'+':  `\2b`,
	'/':  `\2f`,
	':':  `\3a`,
	';':  `\3b`,
	'<':  `\3c`,
	'>':  `\3e`,
	'\\': `\\`,
	'{':  `\7b`,
	'}':  `\7d`,
}

// cssValueFilter filters unsafe CSS values, returning filterFailsafe on
// anything that can affect token boundaries or execute script. | cssValueFilter
// 过滤不安全的 CSS 值；任何可能影响词法边界或执行脚本的值都返回 filterFailsafe。
func cssValueFilter(args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeCSS {
		return s
	}
	b := decodeCSS([]byte(s))
	id := make([]byte, 0, 64)
	for i, c := range b {
		switch c {
		case 0, '"', '\'', '(', ')', '/', ';', '@', '[', '\\', ']', '`', '{', '}', '<', '>':
			return filterFailsafe
		case '-':
			if i != 0 && b[i-1] == '-' {
				return filterFailsafe
			}
		default:
			if c < utf8.RuneSelf && isCSSNmchar(rune(c)) {
				id = append(id, c)
			}
		}
	}
	id = []byte(strings.ToLower(string(id)))
	if strings.Contains(string(id), "expression") || strings.Contains(string(id), "mozbinding") {
		return filterFailsafe
	}
	return string(b)
}
