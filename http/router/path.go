package router

import "net/url"

// pathNeedsEscape marks the bytes that net/url escapes under encodePath. It is derived from
// net/url itself at start-up, so the fast path can never diverge from the standard library. | pathNeedsEscape
// 标记 net/url 在 encodePath 模式下需要转义的字节。该表在启动时由 net/url 自身推导，因此快路径
// 不可能与标准库产生分歧。
var pathNeedsEscape [256]bool

func init() {
	for i := 0; i < 256; i++ {
		s := string([]byte{byte(i)})
		pathNeedsEscape[i] = (&url.URL{Path: s}).EscapedPath() != s
	}
}

// fastEscapedPath returns exactly u.EscapedPath() while skipping the generic escape scan for
// the common all-safe path. | fastEscapedPath 返回与 u.EscapedPath() 完全相同的结果，同时对常见的
// 全安全路径跳过通用转义扫描。
//
// A non-empty RawPath always delegates to the standard library, which validates it and falls back
// to Path when it is malformed; paths containing a byte that needs escaping delegate as well, so
// the returned string is byte-for-byte identical to url.URL.EscapedPath. | RawPath 非空时一律交给
// 标准库校验并在畸形时回退到 Path；包含需转义字节的路径也交给标准库，因此返回值与
// url.URL.EscapedPath 逐字节一致。
func fastEscapedPath(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.RawPath != "" {
		return u.EscapedPath()
	}
	p := u.Path
	for i := 0; i < len(p); i++ {
		if pathNeedsEscape[p[i]] {
			return u.EscapedPath()
		}
	}
	return p
}
