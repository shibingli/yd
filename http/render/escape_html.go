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

// htmlEscapeMode selects one of html/template's replacement tables. | htmlEscapeMode
// 选择 html/template 的一张替换表。
type htmlEscapeMode uint8

const (
	// htmlModeText escapes text and quoted attribute values. | htmlModeText 转义文本与带引号属性值。
	htmlModeText htmlEscapeMode = iota
	// htmlModeTextNorm is htmlModeText without '&'. | htmlModeTextNorm 是不转义 '&' 的 htmlModeText。
	htmlModeTextNorm
	// htmlModeNoSpace escapes unquoted attribute values. | htmlModeNoSpace 转义无引号属性值。
	htmlModeNoSpace
	// htmlModeNoSpaceNorm is htmlModeNoSpace without '&'. | htmlModeNoSpaceNorm 是不转义 '&' 的 htmlModeNoSpace。
	htmlModeNoSpaceNorm
	htmlModeCount
)

// Replacement codes shared by the mode tables. | 各模式表共用的替换码。
const (
	htmlCodeNUL = iota + 1
	htmlCodeNULNoSpace
	htmlCodeQuote
	htmlCodeAmp
	htmlCodeApos
	htmlCodePlus
	htmlCodeLT
	htmlCodeGT
	htmlCodeTab
	htmlCodeLF
	htmlCodeVT
	htmlCodeFF
	htmlCodeCR
	htmlCodeSpace
	htmlCodeEq
	htmlCodeBacktick
)

// htmlEscapeReplacements maps a replacement code to its literal. | htmlEscapeReplacements
// 把替换码映射为字面量。
var htmlEscapeReplacements = [...]string{
	htmlCodeNUL:        "�",
	htmlCodeNULNoSpace: "&#xfffd;",
	htmlCodeQuote:      "&#34;",
	htmlCodeAmp:        "&amp;",
	htmlCodeApos:       "&#39;",
	htmlCodePlus:       "&#43;",
	htmlCodeLT:         "&lt;",
	htmlCodeGT:         "&gt;",
	htmlCodeTab:        "&#9;",
	htmlCodeLF:         "&#10;",
	htmlCodeVT:         "&#11;",
	htmlCodeFF:         "&#12;",
	htmlCodeCR:         "&#13;",
	htmlCodeSpace:      "&#32;",
	htmlCodeEq:         "&#61;",
	htmlCodeBacktick:   "&#96;",
}

// htmlEscapeCodes maps each ASCII byte to its replacement code, 0 meaning
// "append unchanged". A byte-wide table keeps the hot scan to one load and one
// branch per byte instead of three lookups into a string table. | htmlEscapeCodes
// 把每个 ASCII 字节映射为替换码，0 表示原样追加。按字节查表让热扫描每字节只需
// 一次加载与一次分支，而不是在字符串表中做三次查找。
var htmlEscapeCodes [htmlModeCount][256]uint8

func init() {
	text := &htmlEscapeCodes[htmlModeText]
	text[0] = htmlCodeNUL
	text['"'] = htmlCodeQuote
	text['&'] = htmlCodeAmp
	text['\''] = htmlCodeApos
	text['+'] = htmlCodePlus
	text['<'] = htmlCodeLT
	text['>'] = htmlCodeGT

	textNorm := &htmlEscapeCodes[htmlModeTextNorm]
	*textNorm = *text
	textNorm['&'] = 0

	noSpace := &htmlEscapeCodes[htmlModeNoSpace]
	noSpace[0] = htmlCodeNULNoSpace
	noSpace['\t'] = htmlCodeTab
	noSpace['\n'] = htmlCodeLF
	noSpace['\v'] = htmlCodeVT
	noSpace['\f'] = htmlCodeFF
	noSpace['\r'] = htmlCodeCR
	noSpace[' '] = htmlCodeSpace
	noSpace['"'] = htmlCodeQuote
	noSpace['&'] = htmlCodeAmp
	noSpace['\''] = htmlCodeApos
	noSpace['+'] = htmlCodePlus
	noSpace['<'] = htmlCodeLT
	noSpace['='] = htmlCodeEq
	noSpace['>'] = htmlCodeGT
	noSpace['`'] = htmlCodeBacktick

	noSpaceNorm := &htmlEscapeCodes[htmlModeNoSpaceNorm]
	*noSpaceNorm = *noSpace
	noSpaceNorm['&'] = 0
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
	if t == contentTypeHTML {
		return append(dst, s...)
	}
	return appendHTMLText(dst, s, htmlModeText)
}

// appendAttrEscaped appends the quoted-attribute escaped form of (s, t). | appendAttrEscaped 追加带引号属性的转义结果。
func appendAttrEscaped(dst []byte, s string, t contentType) []byte {
	if t == contentTypeHTML {
		return appendHTMLText(dst, stripTags(s), htmlModeTextNorm)
	}
	return appendHTMLText(dst, s, htmlModeText)
}

// appendRCDATAEscaped appends the RCDATA escaped form of (s, t). | appendRCDATAEscaped 追加 RCDATA 的转义结果。
func appendRCDATAEscaped(dst []byte, s string, t contentType) []byte {
	if t == contentTypeHTML {
		return appendHTMLText(dst, s, htmlModeTextNorm)
	}
	return appendHTMLText(dst, s, htmlModeText)
}

// appendNoSpaceEscaped appends the unquoted-attribute escaped form of (s, t).
// | appendNoSpaceEscaped 追加无引号属性的转义结果。
func appendNoSpaceEscaped(dst []byte, s string, t contentType) []byte {
	if s == "" {
		return append(dst, filterFailsafe...)
	}
	if t == contentTypeHTML {
		return appendHTMLNoSpace(dst, stripTags(s), htmlModeNoSpaceNorm)
	}
	return appendHTMLNoSpace(dst, s, htmlModeNoSpace)
}

// appendHTMLText appends s with the text/attribute replacements of mode. | appendHTMLText
// 按 mode 的文本/属性替换表追加 s。
//
// Non-ASCII bytes never need a replacement in these modes: html/template passes
// them through when badRunes is true, so the scan does not decode them. | 这些模式下
// 非 ASCII 字节无需替换：badRunes 为真时 html/template 原样通过，因此扫描不必解码。
func appendHTMLText(dst []byte, s string, mode htmlEscapeMode) []byte {
	codes := &htmlEscapeCodes[mode]
	written := 0
	for i := 0; i < len(s); i++ {
		if code := codes[s[i]]; code != 0 {
			dst = append(dst, s[written:i]...)
			dst = append(dst, htmlEscapeReplacements[code]...)
			written = i + 1
		}
	}
	return append(dst, s[written:]...)
}

// appendHTMLNoSpace appends s with the unquoted-attribute replacements of mode.
// | appendHTMLNoSpace 按 mode 的无引号属性替换表追加 s。
//
// Unlike the textual modes this one replaces noncharacters and invalid UTF-8,
// matching html/template's badRunes=false behaviour (IE compatibility). | 与文本模式
// 不同，本函数会替换非字符与非法 UTF-8，对齐 html/template 的 badRunes=false 行为
// （IE 兼容）。
func appendHTMLNoSpace(dst []byte, s string, mode htmlEscapeMode) []byte {
	codes := &htmlEscapeCodes[mode]
	written := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if code := codes[c]; code != 0 {
				dst = append(dst, s[written:i]...)
				dst = append(dst, htmlEscapeReplacements[code]...)
				written = i + 1
			}
			i++
			continue
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		if 0xfdd0 <= r && r <= 0xfdef || 0xfff0 <= r && r <= 0xffff {
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

// appendHTMLTextBytes is appendHTMLText for a byte segment. | appendHTMLTextBytes 是字节片段版本的 appendHTMLText。
func appendHTMLTextBytes(dst, s []byte, mode htmlEscapeMode) []byte {
	codes := &htmlEscapeCodes[mode]
	written := 0
	for i := 0; i < len(s); i++ {
		if code := codes[s[i]]; code != 0 {
			dst = append(dst, s[written:i]...)
			dst = append(dst, htmlEscapeReplacements[code]...)
			written = i + 1
		}
	}
	return append(dst, s[written:]...)
}

// appendHTMLNoSpaceBytes is appendHTMLNoSpace for a byte segment. | appendHTMLNoSpaceBytes 是字节片段版本的 appendHTMLNoSpace。
func appendHTMLNoSpaceBytes(dst, s []byte, mode htmlEscapeMode) []byte {
	codes := &htmlEscapeCodes[mode]
	written := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if code := codes[c]; code != 0 {
				dst = append(dst, s[written:i]...)
				dst = append(dst, htmlEscapeReplacements[code]...)
				written = i + 1
			}
			i++
			continue
		}
		r, w := utf8.DecodeRune(s[i:])
		if 0xfdd0 <= r && r <= 0xfdef || 0xfff0 <= r && r <= 0xffff {
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
// the content type of a lower-case attribute name. | attrType 对齐 html/template
// 的 attr.go：对全小写属性名给出保守的内容类型判断。
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
