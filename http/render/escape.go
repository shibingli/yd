package render

// Escaping core. | 转义核心。
//
// The compiled executor consumes parse trees that html/template has already
// context-rewritten: every dynamic action ends in one of the _html_template_*
// escapers. This file and its siblings implement exactly those escapers, so the
// output is byte-identical to html/template while the execution path no longer
// pays for text/template's reflection machinery. | 编译执行器消费的是
// html/template 已完成上下文重写的解析树：每个动态动作都以 _html_template_*
// 转义器结尾。本文件及其同级文件精确实现这些转义器，因此输出与 html/template
// 逐字节一致，而执行路径不再为 text/template 的反射机制买单。

import (
	"fmt"
	"html/template"
	"reflect"
	"strings"
)

// filterFailsafe is the inert token emitted when a value fails a contextual
// filter. It matches html/template's spelling so differential tests stay exact.
// | filterFailsafe 是值未通过上下文过滤器时输出的惰性标记；拼写与 html/template
// 一致，以保证差分测试逐字节相同。
const filterFailsafe = "ZgotmplZ"

// contentType records the trusted-content flavor of an escaper argument. | contentType 记录转义参数的受信内容种类。
type contentType uint8

const (
	contentTypePlain contentType = iota
	contentTypeCSS
	contentTypeHTML
	contentTypeHTMLAttr
	contentTypeJS
	contentTypeJSStr
	contentTypeURL
	contentTypeSrcset
	// contentTypeUnsafe marks attributes that affect embedding, credentials or
	// interpretation; a dynamic attribute name of this type is rejected. | contentTypeUnsafe
	// 标记影响嵌入、凭据或解释方式的属性；该类型的动态属性名会被拒绝。
	contentTypeUnsafe
)

// jsonMarshaler matches json.Marshaler without importing encoding/json, which
// the repository forbids outside core/jsonx. | jsonMarshaler 匹配 json.Marshaler
// 而不导入 encoding/json —— 仓库禁止在 core/jsonx 之外直接导入该包。
type jsonMarshaler interface {
	MarshalJSON() ([]byte, error)
}

// stringer matches fmt.Stringer. | stringer 匹配 fmt.Stringer。
type stringer interface {
	String() string
}

// indirect dereferences pointers until a non-pointer or nil. | indirect 逐层解引用指针，直到非指针或 nil。
func indirect(a any) any {
	if a == nil {
		return nil
	}
	if t := reflect.TypeOf(a); t.Kind() != reflect.Pointer {
		return a
	}
	v := reflect.ValueOf(a)
	for v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	return v.Interface()
}

// indirectToStringerOrError dereferences pointers while the value does not
// implement fmt.Stringer or error, matching html/template's stringify rules.
// | indirectToStringerOrError 在值未实现 fmt.Stringer 或 error 时逐层解引用指针，
// 与 html/template 的 stringify 规则一致。
func indirectToStringerOrError(a any) any {
	if a == nil {
		return nil
	}
	v := reflect.ValueOf(a)
	for !v.Type().Implements(reflect.TypeFor[fmt.Stringer]()) &&
		!v.Type().Implements(reflect.TypeFor[error]()) &&
		v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	return v.Interface()
}

// stringify converts escaper arguments to text plus their trusted-content
// type, exactly as html/template does. | stringify 按 html/template 的规则把转义
// 参数转换为文本及受信内容类型。
func stringify(args ...any) (string, contentType) {
	if len(args) == 1 {
		if isNilMarker(args[0]) {
			return "", contentTypePlain
		}
		switch s := indirect(args[0]).(type) {
		case string:
			return s, contentTypePlain
		case template.CSS:
			return string(s), contentTypeCSS
		case template.HTML:
			return string(s), contentTypeHTML
		case template.HTMLAttr:
			return string(s), contentTypeHTMLAttr
		case template.JS:
			return string(s), contentTypeJS
		case template.JSStr:
			return string(s), contentTypeJSStr
		case template.URL:
			return string(s), contentTypeURL
		case template.Srcset:
			return string(s), contentTypeSrcset
		}
	}
	i := 0
	for _, arg := range args {
		// Untyped nil arguments are skipped, as html/template documents. | 与 html/template 一致，跳过无类型 nil 参数。
		if arg == nil || isNilMarker(arg) {
			continue
		}
		args[i] = indirectToStringerOrError(arg)
		i++
	}
	return fmt.Sprint(args[:i]...), contentTypePlain
}

// escKind enumerates the contextual escapers inserted by html/template. | escKind 枚举 html/template 注入的上下文转义器。
type escKind uint8

const (
	escNone escKind = iota
	escAttr
	escComment
	escCSS
	escCSSValue
	escHTMLName
	escHTML
	escJSRegexp
	escJSStr
	escJSTmplLit
	escJSVal
	escNoSpace
	escRCDATA
	escSrcset
	escURL
	escURLFilter
	escURLNormalizer
	escEvalArgs
	// Composite URL chains collapse three scans and two scratch copies into
	// one normalized pass plus one attribute pass. | 组合 URL 链把三次扫描与两次
	// 暂存拷贝合并为一次规范化加一次属性转义。
	escURLAttr
	escURLPartAttr
	escURLQueryAttr
)

// isCompositeKind reports a collapsed URL chain. | isCompositeKind 报告是否为折叠后的 URL 链。
func isCompositeKind(kind escKind) bool {
	return kind == escURLAttr || kind == escURLPartAttr || kind == escURLQueryAttr
}

// collapseEscapers folds the common URL → attribute chains into one kind. | collapseEscapers
// 把常见的 URL → 属性链折叠为单个种类。
func collapseEscapers(in []escKind) []escKind {
	switch {
	case len(in) == 3 && in[0] == escURLFilter && in[1] == escURLNormalizer && in[2] == escAttr:
		return []escKind{escURLAttr}
	case len(in) == 2 && in[0] == escURLNormalizer && in[1] == escAttr:
		return []escKind{escURLPartAttr}
	case len(in) == 2 && in[0] == escURL && in[1] == escAttr:
		return []escKind{escURLQueryAttr}
	}
	return in
}

// appendComposite applies a collapsed URL chain using scratch for the
// intermediate segment. | appendComposite 使用暂存缓冲应用折叠后的 URL 链。
func appendComposite(dst, scratch []byte, v any, kind escKind) ([]byte, []byte) {
	s, ct := stringify(v)
	return appendCompositeString(dst, scratch, s, ct, kind)
}

// appendCompositeString applies a collapsed URL chain to a typed string. | appendCompositeString
// 对带类型的字符串应用折叠 URL 链。
//
// Normalization, percent-encoding and the attribute escape happen in one pass,
// so the intermediate segment and the second scan disappear. | 规范化、百分号编码与
// 属性转义在一次扫描中完成，因此不再产生中间片段与第二次扫描。
func appendCompositeString(dst, scratch []byte, s string, ct contentType, kind escKind) ([]byte, []byte) {
	return appendCompositeField(dst, s, ct, kind), scratch
}

// appendCompositeField applies a collapsed URL chain in one pass, without a
// scratch buffer or a second function call. | appendCompositeField 用一遍扫描应用
// 折叠 URL 链，不产生暂存缓冲，也不再经第二次函数调用。
//
// After normalization the only bytes that can still need an HTML replacement
// are '&' and '+': every other special byte is outside the keep table and is
// percent-encoded. The raw URL-part table keeps no HTML-special byte at all, so
// that branch only percent-encodes. | 规范化之后仍可能需要 HTML 替换的字节只有
// '&' 与 '+'：其余特殊字节都不在 keep 表内，会被百分号编码。原始 URL 部件表
// 不保留任何 HTML 特殊字节，因此该分支只做百分号编码。
func appendCompositeField(dst []byte, s string, ct contentType, kind escKind) []byte {
	norm := true
	switch kind {
	case escURLAttr:
		if ct != contentTypeURL && !isSafeURL(s) {
			s = "#" + filterFailsafe
		}
	case escURLPartAttr:
	case escURLQueryAttr:
		norm = ct == contentTypeURL
	}
	written := 0
	if norm {
		for i := 0; i < len(s); i++ {
			c := s[i]
			code := urlNormCodes[c]
			if code == urlCodeKeep {
				continue
			}
			if code == urlCodePercent && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
				continue
			}
			dst = append(dst, s[written:i]...)
			switch code {
			case urlCodeAmp:
				dst = append(dst, "&amp;"...)
			case urlCodePlus:
				dst = append(dst, "&#43;"...)
			default:
				dst = append(dst, '%', hexDigit(c>>4), hexDigit(c&0xf))
			}
			written = i + 1
		}
		return append(dst, s[written:]...)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if urlKeepRaw[c] {
			continue
		}
		dst = append(dst, s[written:i]...)
		dst = append(dst, '%', hexDigit(c>>4), hexDigit(c&0xf))
		written = i + 1
	}
	return append(dst, s[written:]...)
}

// escapers maps the internal names found in escaped parse trees to kinds. | escapers 把已转义解析树中的内部名字映射为种类。
var escapers = map[string]escKind{
	"_html_template_attrescaper":      escAttr,
	"_html_template_commentescaper":   escComment,
	"_html_template_cssescaper":       escCSS,
	"_html_template_cssvaluefilter":   escCSSValue,
	"_html_template_htmlnamefilter":   escHTMLName,
	"_html_template_htmlescaper":      escHTML,
	"_html_template_jsregexpescaper":  escJSRegexp,
	"_html_template_jsstrescaper":     escJSStr,
	"_html_template_jstmpllitescaper": escJSTmplLit,
	"_html_template_jsvalescaper":     escJSVal,
	"_html_template_nospaceescaper":   escNoSpace,
	"_html_template_rcdataescaper":    escRCDATA,
	"_html_template_srcsetescaper":    escSrcset,
	"_html_template_urlescaper":       escURL,
	"_html_template_urlfilter":        escURLFilter,
	"_html_template_urlnormalizer":    escURLNormalizer,
}

// appendEscaped applies one escaper to v and appends the result to dst. | appendEscaped 对 v 应用一个转义器并把结果追加到 dst。
func appendEscaped(dst []byte, v any, kind escKind) []byte {
	// Plain strings take the allocation-free typed path. Safe string types keep
	// their dynamic type and therefore fall through to stringify. | 普通字符串走无分配
	// 的类型化路径；安全字符串类型保留动态类型，因此落到 stringify。
	if s, ok := v.(string); ok {
		return appendEscapedString(dst, s, kind)
	}
	switch kind {
	case escAttr:
		s, t := stringify(v)
		return appendAttrEscaped(dst, s, t)
	case escComment:
		return dst
	case escCSS:
		return appendString(dst, cssEscaper(v))
	case escCSSValue:
		return appendString(dst, cssValueFilter(v))
	case escHTMLName:
		return appendString(dst, htmlNameFilter(v))
	case escHTML:
		s, t := stringify(v)
		return appendHTMLEscaped(dst, s, t)
	case escJSRegexp:
		return appendString(dst, jsRegexpEscaper(v))
	case escJSStr:
		return appendString(dst, jsStrEscaper(v))
	case escJSTmplLit:
		return appendString(dst, jsTmplLitEscaper(v))
	case escJSVal:
		return appendString(dst, jsValEscaper(v))
	case escNoSpace:
		s, t := stringify(v)
		return appendNoSpaceEscaped(dst, s, t)
	case escRCDATA:
		s, t := stringify(v)
		return appendRCDATAEscaped(dst, s, t)
	case escSrcset:
		return appendString(dst, srcsetFilterAndEscaper(v))
	case escURL:
		return appendString(dst, urlEscaper(v))
	case escURLFilter:
		return appendString(dst, urlFilter(v))
	case escURLNormalizer:
		return appendString(dst, urlNormalizer(v))
	case escEvalArgs:
		return appendString(dst, formatEvalArgs(v))
	default:
		return dst
	}
}

// appendEscapedString applies one escaper to a plain string without boxing it
// into an interface value, covering the hot text and attribute contexts. | appendEscapedString
// 对普通字符串应用一个转义器且不做接口装箱，覆盖文本与属性热路径。
func appendEscapedString(dst []byte, s string, kind escKind) []byte {
	switch kind {
	case escNone:
		return dst
	case escComment:
		return dst
	case escHTML, escAttr, escRCDATA:
		return appendHTMLText(dst, s, htmlModeText)
	case escNoSpace:
		return appendNoSpaceEscaped(dst, s, contentTypePlain)
	case escURL:
		return appendURLString(dst, s, false)
	case escURLNormalizer:
		return appendURLString(dst, s, true)
	case escURLFilter:
		if !isSafeURL(s) {
			return append(dst, "#"+filterFailsafe...)
		}
		return append(dst, s...)
	default:
		return appendString(dst, escapeStringSlow(s, kind))
	}
}

// escapeStringSlow routes the rare escapers through their string form. | escapeStringSlow 让罕见转义器走字符串形式。
func escapeStringSlow(s string, kind escKind) string {
	switch kind {
	case escCSS:
		return cssEscaper(s)
	case escCSSValue:
		return cssValueFilter(s)
	case escHTMLName:
		return htmlNameFilter(s)
	case escJSRegexp:
		return jsRegexpEscaper(s)
	case escJSStr:
		return jsStrEscaper(s)
	case escJSTmplLit:
		return jsTmplLitEscaper(s)
	case escJSVal:
		return jsValEscaper(s)
	case escSrcset:
		return srcsetFilterAndEscaper(s)
	default:
		return s
	}
}

// appendEscapedBytes applies one escaper to an already materialized segment
// without converting it back to a string, which is what chained escapers such
// as URL → attribute need. | appendEscapedBytes 对已实体化的片段应用一个转义器而不转回
// 字符串，这正是 URL → 属性这类链式转义所需要的。
func appendEscapedBytes(dst, b []byte, kind escKind) []byte {
	switch kind {
	case escHTML, escAttr, escRCDATA:
		return appendHTMLTextBytes(dst, b, htmlModeText)
	case escNoSpace:
		if len(b) == 0 {
			return append(dst, filterFailsafe...)
		}
		return appendHTMLNoSpaceBytes(dst, b, htmlModeNoSpace)
	case escURL:
		return appendURLEscapedBytes(dst, b, false)
	case escURLNormalizer:
		return appendURLEscapedBytes(dst, b, true)
	default:
		return appendEscapedString(dst, string(b), kind)
	}
}

// appendString appends s to dst. | appendString 把 s 追加到 dst。
func appendString(dst []byte, s string) []byte {
	return append(dst, s...)
}

// formatEvalArgs formats arguments as fmt.Sprint does after dereferencing
// pointers, and exists because html/template rewrites {{ esc a b }} into
// {{ _eval_args_ a b | esc }}. | formatEvalArgs 按 fmt.Sprint 的规则格式化参数并
// 解引用指针；它存在是因为 html/template 会把 {{ esc a b }} 重写为
// {{ _eval_args_ a b | esc }}。
func formatEvalArgs(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if args, ok := v.([]any); ok {
		return evalArgsSlice(args)
	}
	return evalArgsSlice([]any{v})
}

// evalArgsSlice is evalArgs for an explicit argument list. | evalArgsSlice 是显式参数列表版本的 evalArgs。
func evalArgsSlice(args []any) string {
	for i, arg := range args {
		args[i] = indirectToStringerOrError(arg)
	}
	return fmt.Sprint(args...)
}

// htmlSpaceAndASCIIAlnumBytes is html/template's bitmap of HTML whitespace and
// ASCII alphanumerics, used by the srcset scanner. | htmlSpaceAndASCIIAlnumBytes 是
// html/template 中 HTML 空白与 ASCII 字母数字的位图，供 srcset 扫描使用。
const htmlSpaceAndASCIIAlnumBytes = "\x00\x36\x00\x00\x01\x00\xff\x03\xfe\xff\xff\x07\xfe\xff\xff\x07"

// isHTMLSpace reports whether c is ASCII whitespace. | isHTMLSpace 报告 c 是否为 ASCII 空白。
func isHTMLSpace(c byte) bool {
	return c <= 0x20 && htmlSpaceAndASCIIAlnumBytes[c>>3]&(1<<uint(c&0x7)) != 0
}

// isHTMLSpaceOrASCIIAlnum reports whether c is whitespace or alphanumeric. | isHTMLSpaceOrASCIIAlnum 报告 c 是否为空白或字母数字。
func isHTMLSpaceOrASCIIAlnum(c byte) bool {
	return c < 0x80 && htmlSpaceAndASCIIAlnumBytes[c>>3]&(1<<uint(c&0x7)) != 0
}

// isASCIIIdent reports whether s consists only of lowercase ASCII letters and
// digits, the shape html/template accepts for a dynamic attribute name. | isASCIIIdent 报告
// s 是否仅由小写 ASCII 字母与数字构成，即 html/template 接受的动态属性名形状。
func isASCIIIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// trimLower lowercases and trims an attribute name. | trimLower 把属性名转小写并去除空白。
func trimLower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
