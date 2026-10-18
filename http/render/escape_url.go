package render

// URL escapers. | URL 转义器。
//
// urlFilter defangs dangerous schemes, urlEscaper percent-encodes a URL part,
// urlNormalizer normalizes an already valid URL without re-encoding escapes,
// and srcsetFilterAndEscaper applies both to image candidate lists. | urlFilter
// 拦截危险协议，urlEscaper 对 URL 部件做百分号编码，urlNormalizer 规范化已合法
// 的 URL 且不重复编码既有转义，srcsetFilterAndEscaper 对图片候选列表同时应用两者。

import (
	"fmt"
	"strings"
)

// urlFilter returns its input unless it carries an unsafe scheme. | urlFilter 原样返回输入，除非它带有危险协议。
func urlFilter(args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeURL {
		return s
	}
	if !isSafeURL(s) {
		return "#" + filterFailsafe
	}
	return s
}

// isSafeURL reports whether s is relative or uses http, https or mailto. | isSafeURL 报告 s 是否为相对地址或使用 http、https、mailto。
func isSafeURL(s string) bool {
	if protocol, _, ok := strings.Cut(s, ":"); ok && !strings.Contains(protocol, "/") {
		if !strings.EqualFold(protocol, "http") && !strings.EqualFold(protocol, "https") && !strings.EqualFold(protocol, "mailto") {
			return false
		}
	}
	return true
}

// urlEscaper percent-encodes a URL part. | urlEscaper 对 URL 部件做百分号编码。
func urlEscaper(args ...any) string { return urlProcessor(false, args...) }

// urlNormalizer normalizes a URL part without re-encoding valid escapes. | urlNormalizer 规范化 URL 部件且不重复编码合法转义。
func urlNormalizer(args ...any) string { return urlProcessor(true, args...) }

// urlProcessor normalizes or escapes a URL part, appending to a builder. | urlProcessor 规范化或转义 URL 部件并写入构建器。
func urlProcessor(norm bool, args ...any) string {
	s, t := stringify(args...)
	if t == contentTypeURL {
		norm = true
	}
	var b strings.Builder
	if processURLOnto(s, norm, &b) {
		return b.String()
	}
	return s
}

// processURLOnto appends the encoded URL to b and reports whether it changed s.
// | processURLOnto 把编码后的 URL 追加到 b，并报告结果是否与 s 不同。
func processURLOnto(s string, norm bool, b *strings.Builder) bool {
	b.Grow(len(s) + 16)
	written := 0
	for i, n := 0, len(s); i < n; i++ {
		c := s[i]
		switch c {
		case '!', '#', '$', '&', '*', '+', ',', '/', ':', ';', '=', '?', '@', '[', ']':
			if norm {
				continue
			}
		case '-', '.', '_', '~':
			continue
		case '%':
			if norm && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
				continue
			}
		default:
			if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' {
				continue
			}
		}
		b.WriteString(s[written:i])
		fmt.Fprintf(b, "%%%02x", c)
		written = i + 1
	}
	b.WriteString(s[written:])
	return written != 0
}

// appendURLString appends the percent-encoded form of s without a builder. | appendURLString 不经构建器追加 s 的百分号编码结果。
func appendURLString(dst []byte, s string, norm bool) []byte {
	written := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		keep := false
		switch c {
		case '!', '#', '$', '&', '*', '+', ',', '/', ':', ';', '=', '?', '@', '[', ']':
			keep = norm
		case '-', '.', '_', '~':
			keep = true
		case '%':
			keep = norm && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2])
		default:
			keep = 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
		}
		if keep {
			continue
		}
		dst = append(dst, s[written:i]...)
		dst = append(dst, '%', hexDigit(c>>4), hexDigit(c&0xf))
		written = i + 1
	}
	return append(dst, s[written:]...)
}

// appendURLEscapedBytes appends the percent-encoded form of s. | appendURLEscapedBytes 追加 s 的百分号编码结果。
func appendURLEscapedBytes(dst, s []byte, norm bool) []byte {
	written := 0
	for i, c := range s {
		keep := false
		switch c {
		case '!', '#', '$', '&', '*', '+', ',', '/', ':', ';', '=', '?', '@', '[', ']':
			keep = norm
		case '-', '.', '_', '~':
			keep = true
		case '%':
			keep = norm && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2])
		default:
			keep = 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
		}
		if keep {
			continue
		}
		dst = append(dst, s[written:i]...)
		dst = append(dst, '%', hexDigit(c>>4), hexDigit(c&0xf))
		written = i + 1
	}
	return append(dst, s[written:]...)
}

// hexDigit returns the lowercase hexadecimal digit of v. | hexDigit 返回 v 的小写十六进制数字。
func hexDigit(v byte) byte {
	if v < 10 {
		return '0' + v
	}
	return 'a' + v - 10
}

// srcsetFilterAndEscaper filters and normalizes a srcset value. | srcsetFilterAndEscaper 过滤并规范化 srcset 值。
func srcsetFilterAndEscaper(args ...any) string {
	s, t := stringify(args...)
	switch t {
	case contentTypeSrcset:
		return s
	case contentTypeURL:
		var b strings.Builder
		if processURLOnto(s, true, &b) {
			s = b.String()
		}
		return strings.ReplaceAll(s, ",", "%2c")
	}
	var b strings.Builder
	written := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			filterSrcsetElement(s, written, i, &b)
			b.WriteString(",")
			written = i + 1
		}
	}
	filterSrcsetElement(s, written, len(s), &b)
	return b.String()
}

// filterSrcsetElement appends one URL-and-metadata element. | filterSrcsetElement 追加一个 URL 与元数据元素。
func filterSrcsetElement(s string, left, right int, b *strings.Builder) {
	start := left
	for start < right && isHTMLSpace(s[start]) {
		start++
	}
	end := right
	for i := start; i < right; i++ {
		if isHTMLSpace(s[i]) {
			end = i
			break
		}
	}
	if url := s[start:end]; isSafeURL(url) {
		metadataOK := true
		for i := end; i < right; i++ {
			if !isHTMLSpaceOrASCIIAlnum(s[i]) {
				metadataOK = false
				break
			}
		}
		if metadataOK {
			b.WriteString(s[left:start])
			processURLOnto(url, true, b)
			b.WriteString(s[end:right])
			return
		}
	}
	b.WriteString("#")
	b.WriteString(filterFailsafe)
}
