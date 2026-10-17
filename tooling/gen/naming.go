package gen

import (
	"strings"
	"unicode"
)

// initialisms 是 Go 惯例中保持全大写的常见缩写；导出命名时按这些词整体大写。
// initialisms lists the common abbreviations that Go convention keeps fully upper-case; exported names upper-case them as a whole.
var initialisms = map[string]bool{
	"id": true, "uid": true, "uuid": true, "url": true, "uri": true, "api": true,
	"http": true, "https": true, "json": true, "sql": true, "db": true, "ip": true,
	"html": true, "css": true, "jwt": true, "ui": true, "sms": true, "oss": true,
	"cdn": true, "ttl": true, "dto": true,
}

// separators 是切词时视为边界的字符。
// separators are the characters treated as word boundaries while splitting.
const separators = "_- ./\\\t"

// splitWords 把标识符拆成小写单词：支持分隔符与驼峰边界（含连续缩写）。
// splitWords splits an identifier into lower-case words: it understands separators and camel-case boundaries (including runs of initialisms).
func splitWords(s string) []string {
	runes := []rune(s)
	words := make([]string, 0, 4)
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}

	for i, r := range runes {
		switch {
		case strings.ContainsRune(separators, r):
			flush()
		case unicode.IsUpper(r):
			if len(cur) > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					flush()
				}
			}
			cur = append(cur, unicode.ToLower(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur = append(cur, unicode.ToLower(r))
		default:
			flush()
		}
	}
	flush()
	return words
}

// snakeCase 把标识符转换为小写下划线形式：GenTaggedUser → gen_tagged_user。
// snakeCase converts an identifier to lower snake case: GenTaggedUser → gen_tagged_user.
func snakeCase(s string) string { return strings.Join(splitWords(s), "_") }

// exportedName 把标识符转换为导出形式：gen_tagged_user → GenTaggedUser，id → ID。
// exportedName converts an identifier to its exported form: gen_tagged_user → GenTaggedUser, id → ID.
func exportedName(s string) string {
	var b strings.Builder
	for _, w := range splitWords(s) {
		if initialisms[w] {
			b.WriteString(strings.ToUpper(w))
			continue
		}
		r := []rune(w)
		b.WriteString(strings.ToUpper(string(r[0])))
		b.WriteString(string(r[1:]))
	}
	return b.String()
}

// unexportedName 把导出标识符转为首字母小写；整体全大写（缩写）时整体小写。
// unexportedName lower-cases the first letter of an exported identifier; a fully upper-case (initialism) name is lower-cased as a whole.
func unexportedName(s string) string {
	if s == "" {
		return s
	}
	if s == strings.ToUpper(s) {
		return strings.ToLower(s)
	}
	r := []rune(s)
	return strings.ToLower(string(r[0])) + string(r[1:])
}

// pluralize 返回名词的简单复数形式（s/x/z/ch/sh 加 es，辅音 + y 变 ies，其余加 s）。
// pluralize returns a simple plural form (es after s/x/z/ch/sh, ies after a consonant + y, s otherwise).
func pluralize(s string) string {
	switch {
	case s == "":
		return s
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "z"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	case strings.HasSuffix(s, "y") && len(s) > 1 && !isVowel(rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	default:
		return s + "s"
	}
}

// singularize 返回名词的简单单数形式，用于表名到模型名的推导；不认识的形态原样返回。
// singularize returns a simple singular form used to derive a model name from a table name; unknown shapes are returned unchanged.
func singularize(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "sses"), strings.HasSuffix(s, "shes"), strings.HasSuffix(s, "ches"),
		strings.HasSuffix(s, "xes"), strings.HasSuffix(s, "zes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") && !strings.HasSuffix(s, "us") && !strings.HasSuffix(s, "is"):
		return s[:len(s)-1]
	default:
		return s
	}
}

// isVowel 报告 r 是否为元音字母。
// isVowel reports whether r is a vowel.
func isVowel(r rune) bool {
	switch unicode.ToLower(r) {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	default:
		return false
	}
}

// goKeywords 是禁止作为标识符的关键字。
// goKeywords are the keywords that must not be used as identifiers.
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true, "default": true,
	"defer": true, "else": true, "fallthrough": true, "for": true, "func": true, "go": true,
	"goto": true, "if": true, "import": true, "interface": true, "map": true, "package": true,
	"range": true, "return": true, "select": true, "struct": true, "switch": true, "type": true,
	"var": true,
}

// validGoIdent 报告 s 是否为合法的、非关键字的 Go 标识符。
// validGoIdent reports whether s is a legal Go identifier that is not a keyword.
func validGoIdent(s string) bool {
	if s == "" || goKeywords[s] {
		return false
	}
	for i, r := range s {
		switch {
		case unicode.IsLetter(r) || r == '_':
		case unicode.IsDigit(r) && i > 0:
		default:
			return false
		}
	}
	return true
}

// sanitizeText 把一段自由文本压成单行，供生成注释使用；换行与连续空白归一为单个空格。
// sanitizeText flattens free text into a single line for generated comments; newlines and runs of spaces collapse into one space.
func sanitizeText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// sanitizeTagValue 清理写入结构体标签值的文本：先压成单行，再去掉会破坏标签字面量的字符。
// sanitizeTagValue cleans text written into a struct-tag value: it flattens the
// text and removes the characters that would break the tag literal.
//
// 分号是 gorm 标签自身的分隔符，必须保留；反引号、双引号与换行会截断标签，一律剔除。
// Semicolons separate gorm settings and must survive; backticks, double quotes
// and newlines would cut the tag short and are dropped.
func sanitizeTagValue(s string) string {
	return strings.NewReplacer("`", "", `"`, "").Replace(sanitizeText(s))
}
