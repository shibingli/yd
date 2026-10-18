package render

// HTML escapers. | HTML 转义器。
//
// The replacement tables and the strip-tags behaviour mirror html/template;
// the hot HTML and attribute paths append straight into the caller's byte
// buffer, so a template render does not allocate one string per interpolation.
// | 替换表与去标签行为对齐 html/template；HTML 与属性热路径直接追加到调用方
// 的字节缓冲，因此一次模板渲染不会为每个插值分配一个字符串。

import (
	"strings"
	"unicode/utf8"
)

// htmlReplacementTable holds the runes escaped in text and quoted attribute
// values. | htmlReplacementTable 保存文本与带引号属性值中需要转义的字符。
var htmlReplacementTable = []string{
	0:    "�",
	'"':  "&#34;",
	'&':  "&amp;",
	'\'': "&#39;",
	'+':  "&#43;",
	'<':  "&lt;",
	'>':  "&gt;",
}

// htmlNormReplacementTable is htmlReplacementTable without '&', so existing
// entities are not double encoded. | htmlNormReplacementTable 是去掉 '&' 的
// htmlReplacementTable，避免已有实体被二次编码。
var htmlNormReplacementTable = []string{
	0:    "�",
	'"':  "&#34;",
	'\'': "&#39;",
	'+':  "&#43;",
	'<':  "&lt;",
	'>':  "&gt;",
}

// htmlNoSpaceReplacementTable escapes unquoted attribute values. | htmlNoSpaceReplacementTable 转义无引号属性值。
var htmlNoSpaceReplacementTable = []string{
	0:    "&#xfffd;",
	'\t': "&#9;",
	'\n': "&#10;",
	'\v': "&#11;",
	'\f': "&#12;",
	'\r': "&#13;",
	' ':  "&#32;",
	'"':  "&#34;",
	'&':  "&amp;",
	'\'': "&#39;",
	'+':  "&#43;",
	'<':  "&lt;",
	'=':  "&#61;",
	'>':  "&gt;",
	'`':  "&#96;",
}

// htmlNoSpaceNormReplacementTable is the unquoted table without '&'. | htmlNoSpaceNormReplacementTable 是无 '&' 的无引号属性表。
var htmlNoSpaceNormReplacementTable = []string{
	0:    "&#xfffd;",
	'\t': "&#9;",
	'\n': "&#10;",
	'\v': "&#11;",
	'\f': "&#12;",
	'\r': "&#13;",
	' ':  "&#32;",
	'"':  "&#34;",
	'\'': "&#39;",
	'+':  "&#43;",
	'<':  "&lt;",
	'=':  "&#61;",
	'>':  "&gt;",
	'`':  "&#96;",
}

// htmlSpecialTable marks every ASCII byte that any HTML/attribute replacement
// table may escape, so the hot scan needs one table lookup per byte instead of
// three checks. | htmlSpecialTable 标记任一 HTML/属性替换表可能转义的 ASCII 字节，
// 使热扫描每字节只需一次表查找而不是三次判断。
var htmlSpecialTable [256]bool

func init() {
	for _, table := range [][]string{
		htmlReplacementTable, htmlNormReplacementTable,
		htmlNoSpaceReplacementTable, htmlNoSpaceNormReplacementTable,
	} {
		for i, repl := range table {
			if repl != "" && i < len(htmlSpecialTable) {
				htmlSpecialTable[i] = true
			}
		}
	}
}

// htmlEscaper returns the HTML-text escaped form of its argument. | htmlEscaper 返回参数的 HTML 文本转义结果。
func htmlEscaper(args ...any) string {
	s, t := stringify(args...)
	return string(appendHTMLEscaped(nil, s, t))
}

// attrEscaper returns the quoted-attribute escaped form of its argument. | attrEscaper 返回带引号属性的转义结果。
func attrEscaper(args ...any) string {
	s, t := stringify(args...)
	return string(appendAttrEscaped(nil, s, t))
}

// rcdataEscaper returns the RCDATA escaped form of its argument. | rcdataEscaper 返回 RCDATA 的转义结果。
func rcdataEscaper(args ...any) string {
	s, t := stringify(args...)
	return string(appendRCDATAEscaped(nil, s, t))
}

// htmlNoSpaceEscaper returns the unquoted-attribute escaped form of its argument.
// | htmlNoSpaceEscaper 返回无引号属性的转义结果。
func htmlNoSpaceEscaper(args ...any) string {
	s, t := stringify(args...)
	return string(appendNoSpaceEscaped(nil, s, t))
}

// commentEscaper drops interpolated comment content entirely. | commentEscaper 完全丢弃插入注释的内容。
func commentEscaper(...any) string { return "" }

// htmlNameFilter validates a dynamic attribute or tag name. | htmlNameFilter 校验动态属性名或标签名。
func htmlNameFilter(args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeHTMLAttr {
		return s
	}
	if s == "" {
		return filterFailsafe
	}
	s = trimLower(s)
	if attrType(s) != contentTypePlain {
		return filterFailsafe
	}
	if !isASCIIIdent(s) {
		return filterFailsafe
	}
	return s
}

// appendHTMLEscaped appends the HTML-text escaped form of (s, t) to dst. | appendHTMLEscaped 把 (s, t) 的 HTML 文本转义结果追加到 dst。
func appendHTMLEscaped(dst []byte, s string, t contentType) []byte {
	if t == contentTypeHTML || !hasHTMLSpecial(s) {
		return append(dst, s...)
	}
	return appendHTMLReplacer(dst, s, htmlReplacementTable, true)
}

// appendAttrEscaped appends the quoted-attribute escaped form of (s, t). | appendAttrEscaped 追加带引号属性的转义结果。
func appendAttrEscaped(dst []byte, s string, t contentType) []byte {
	if t == contentTypeHTML {
		return appendHTMLReplacer(dst, stripTags(s), htmlNormReplacementTable, true)
	}
	if !hasHTMLSpecial(s) {
		return append(dst, s...)
	}
	return appendHTMLReplacer(dst, s, htmlReplacementTable, true)
}

// appendRCDATAEscaped appends the RCDATA escaped form of (s, t). | appendRCDATAEscaped 追加 RCDATA 的转义结果。
func appendRCDATAEscaped(dst []byte, s string, t contentType) []byte {
	if t == contentTypeHTML {
		return appendHTMLReplacer(dst, s, htmlNormReplacementTable, true)
	}
	if !hasHTMLSpecial(s) {
		return append(dst, s...)
	}
	return appendHTMLReplacer(dst, s, htmlReplacementTable, true)
}

// appendNoSpaceEscaped appends the unquoted-attribute escaped form of (s, t).
// | appendNoSpaceEscaped 追加无引号属性的转义结果。
func appendNoSpaceEscaped(dst []byte, s string, t contentType) []byte {
	if s == "" {
		return append(dst, filterFailsafe...)
	}
	if t == contentTypeHTML {
		return appendHTMLReplacer(dst, stripTags(s), htmlNoSpaceNormReplacementTable, false)
	}
	if !hasHTMLSpecial(s) {
		return append(dst, s...)
	}
	return appendHTMLReplacer(dst, s, htmlNoSpaceReplacementTable, false)
}

// hasHTMLSpecial reports whether s contains a byte any HTML table escapes. | hasHTMLSpecial 报告 s 是否含任一 HTML 表会转义的字节。
func hasHTMLSpecial(s string) bool {
	for i := 0; i < len(s); i++ {
		if htmlSpecialTable[s[i]] {
			return true
		}
	}
	return false
}

// hasHTMLSpecialBytes is hasHTMLSpecial for a byte slice. | hasHTMLSpecialBytes 是字节切片版本的 hasHTMLSpecial。
func hasHTMLSpecialBytes(s []byte) bool {
	for _, c := range s {
		if htmlSpecialTable[c] {
			return true
		}
	}
	return false
}

// appendHTMLReplacer appends s with runes replaced according to table. | appendHTMLReplacer 按 table 替换 s 中的字符后追加。
//
// badRunes mirrors html/template's flag: when false, characters in the
// surrogate/private ranges are always emitted as numeric references. | badRunes
// 对应 html/template 的同名开关：为 false 时，代理区与私用区的字符一律输出为数字引用。
func appendHTMLReplacer(dst []byte, s string, table []string, badRunes bool) []byte {
	written, i := 0, 0
	for i < len(s) {
		c := s[i]
		if c < utf8.RuneSelf {
			if htmlSpecialTable[c] {
				if int(c) < len(table) {
					if repl := table[c]; repl != "" {
						dst = append(dst, s[written:i]...)
						dst = append(dst, repl...)
						written = i + 1
					}
				}
			}
			i++
			continue
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		if !badRunes && (0xfdd0 <= r && r <= 0xfdef || 0xfff0 <= r && r <= 0xffff) {
			dst = append(dst, s[written:i]...)
			dst = append(dst, "&#x"...)
			dst = appendHexRune(dst, r)
			dst = append(dst, ';')
			written = i + w
		}
		i += w
	}
	return append(dst, s[written:]...)
}

// appendHTMLReplacerBytes is appendHTMLReplacer for a byte segment. | appendHTMLReplacerBytes 是字节片段版本的 appendHTMLReplacer。
func appendHTMLReplacerBytes(dst, s []byte, table []string, badRunes bool) []byte {
	written, i := 0, 0
	for i < len(s) {
		c := s[i]
		if c < utf8.RuneSelf {
			if htmlSpecialTable[c] {
				if int(c) < len(table) {
					if repl := table[c]; repl != "" {
						dst = append(dst, s[written:i]...)
						dst = append(dst, repl...)
						written = i + 1
					}
				}
			}
			i++
			continue
		}
		r, w := utf8.DecodeRune(s[i:])
		if !badRunes && (0xfdd0 <= r && r <= 0xfdef || 0xfff0 <= r && r <= 0xffff) {
			dst = append(dst, s[written:i]...)
			dst = append(dst, "&#x"...)
			dst = appendHexRune(dst, r)
			dst = append(dst, ';')
			written = i + w
		}
		i += w
	}
	return append(dst, s[written:]...)
}

// appendHexRune appends the lowercase hexadecimal form of r. | appendHexRune 追加 r 的小写十六进制形式。
func appendHexRune(dst []byte, r rune) []byte {
	const hex = "0123456789abcdef"
	var buf [8]byte
	i := len(buf)
	for r > 0 {
		i--
		buf[i] = hex[r&0xf]
		r >>= 4
	}
	return append(dst, buf[i:]...)
}

// stripTags returns only the text content of an HTML snippet, following
// html/template's transition scanner closely enough for the escapers that use
// it. It is deliberately conservative: anything that looks like markup is
// removed, which can only reduce what reaches the page. | stripTags 只保留 HTML
// 片段的文本内容，其行为与 html/template 的转移扫描足够接近；它刻意保守：
// 形似标记的片段一律移除，这只会减少进入页面的内容。
func stripTags(html string) string {
	if !strings.ContainsAny(html, "<>") {
		return html
	}
	var b strings.Builder
	b.Grow(len(html))
	inTag := false
	quote := byte(0)
	for i := 0; i < len(html); i++ {
		c := html[i]
		if inTag {
			switch {
			case quote != 0:
				if c == quote {
					quote = 0
				}
			case c == '"' || c == '\'':
				quote = c
			case c == '>':
				inTag = false
			}
			continue
		}
		if c == '<' {
			inTag = true
			quote = 0
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// attrType mirrors html/template's attr.go: it returns a conservative guess at
// the content type of a lower-case attribute name. | attrType 对齐
// html/template 的 attr.go：对全小写属性名给出保守的内容类型判断。
func attrType(name string) contentType {
	if strings.HasPrefix(name, "data-") {
		name = name[5:]
	} else if prefix, short, ok := strings.Cut(name, ":"); ok {
		if prefix == "xmlns" {
			return contentTypeURL
		}
		name = short
	}
	if t, ok := attrTypeMap[name]; ok {
		return t
	}
	if strings.HasPrefix(name, "on") {
		return contentTypeJS
	}
	if strings.Contains(name, "src") || strings.Contains(name, "uri") || strings.Contains(name, "url") {
		return contentTypeURL
	}
	return contentTypePlain
}

// attrTypeMap classifies HTML attribute names by content type. | attrTypeMap 按内容类型归类 HTML 属性名。
//
// The table is the same one html/template uses; names that affect embedding,
// credentials or interpretation are marked unsafe, URL attributes are marked
// as URLs, and event handlers are detected by their "on" prefix. | 该表与
// html/template 相同：影响嵌入、凭据或解释方式的属性标为不安全，URL 类属性标为 URL，
// 事件处理器按 "on" 前缀识别。
var attrTypeMap = map[string]contentType{
	"accept":          contentTypePlain,
	"accept-charset":  contentTypeUnsafe,
	"action":          contentTypeURL,
	"alt":             contentTypePlain,
	"archive":         contentTypeURL,
	"async":           contentTypeUnsafe,
	"autocomplete":    contentTypePlain,
	"autofocus":       contentTypePlain,
	"autoplay":        contentTypePlain,
	"background":      contentTypeURL,
	"border":          contentTypePlain,
	"checked":         contentTypePlain,
	"cite":            contentTypeURL,
	"challenge":       contentTypeUnsafe,
	"charset":         contentTypeUnsafe,
	"class":           contentTypePlain,
	"classid":         contentTypeURL,
	"codebase":        contentTypeURL,
	"cols":            contentTypePlain,
	"colspan":         contentTypePlain,
	"content":         contentTypeUnsafe,
	"contenteditable": contentTypePlain,
	"contextmenu":     contentTypePlain,
	"controls":        contentTypePlain,
	"coords":          contentTypePlain,
	"crossorigin":     contentTypeUnsafe,
	"data":            contentTypeURL,
	"datetime":        contentTypePlain,
	"default":         contentTypePlain,
	"defer":           contentTypeUnsafe,
	"dir":             contentTypePlain,
	"dirname":         contentTypePlain,
	"disabled":        contentTypePlain,
	"draggable":       contentTypePlain,
	"dropzone":        contentTypePlain,
	"enctype":         contentTypeUnsafe,
	"for":             contentTypePlain,
	"form":            contentTypeUnsafe,
	"formaction":      contentTypeURL,
	"formenctype":     contentTypeUnsafe,
	"formmethod":      contentTypeUnsafe,
	"formnovalidate":  contentTypeUnsafe,
	"formtarget":      contentTypePlain,
	"headers":         contentTypePlain,
	"height":          contentTypePlain,
	"hidden":          contentTypePlain,
	"high":            contentTypePlain,
	"href":            contentTypeURL,
	"hreflang":        contentTypePlain,
	"http-equiv":      contentTypeUnsafe,
	"icon":            contentTypeURL,
	"id":              contentTypePlain,
	"ismap":           contentTypePlain,
	"keytype":         contentTypeUnsafe,
	"kind":            contentTypePlain,
	"label":           contentTypePlain,
	"lang":            contentTypePlain,
	"language":        contentTypeUnsafe,
	"list":            contentTypePlain,
	"longdesc":        contentTypeURL,
	"loop":            contentTypePlain,
	"low":             contentTypePlain,
	"manifest":        contentTypeURL,
	"max":             contentTypePlain,
	"maxlength":       contentTypePlain,
	"media":           contentTypePlain,
	"mediagroup":      contentTypePlain,
	"method":          contentTypeUnsafe,
	"min":             contentTypePlain,
	"multiple":        contentTypePlain,
	"name":            contentTypePlain,
	"novalidate":      contentTypeUnsafe,
	"open":            contentTypePlain,
	"optimum":         contentTypePlain,
	"pattern":         contentTypeUnsafe,
	"placeholder":     contentTypePlain,
	"poster":          contentTypeURL,
	"profile":         contentTypeURL,
	"preload":         contentTypePlain,
	"pubdate":         contentTypePlain,
	"radiogroup":      contentTypePlain,
	"readonly":        contentTypePlain,
	"rel":             contentTypeUnsafe,
	"required":        contentTypePlain,
	"reversed":        contentTypePlain,
	"rows":            contentTypePlain,
	"rowspan":         contentTypePlain,
	"sandbox":         contentTypeUnsafe,
	"spellcheck":      contentTypePlain,
	"scope":           contentTypePlain,
	"scoped":          contentTypePlain,
	"seamless":        contentTypePlain,
	"selected":        contentTypePlain,
	"shape":           contentTypePlain,
	"size":            contentTypePlain,
	"sizes":           contentTypePlain,
	"span":            contentTypePlain,
	"src":             contentTypeURL,
	"srcdoc":          contentTypeHTML,
	"srclang":         contentTypePlain,
	"srcset":          contentTypeSrcset,
	"start":           contentTypePlain,
	"step":            contentTypePlain,
	"style":           contentTypeCSS,
	"tabindex":        contentTypePlain,
	"target":          contentTypePlain,
	"title":           contentTypePlain,
	"type":            contentTypeUnsafe,
	"usemap":          contentTypeURL,
	"value":           contentTypeUnsafe,
	"width":           contentTypePlain,
	"wrap":            contentTypePlain,
	"xmlns":           contentTypeURL,
}
