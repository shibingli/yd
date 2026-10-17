package router

import "net/url"

// requestPath returns the path used for routing. | requestPath 返回用于路由的路径。
//
// When the request carries a non-empty RawPath, the original escaped form is used, which keeps
// encoded slashes (for example %2F) inside one segment; otherwise the decoded URL.Path is used.
// This matches the behaviour of the mainstream routers and avoids the generic escape scan that
// url.URL.EscapedPath performs on every request. | 请求带有非空 RawPath 时使用原始转义形式，
// 使编码斜杠（例如 %2F）保留在同一个分段内；否则使用解码后的 URL.Path。这与主流路由的行为一致，
// 并避免 url.URL.EscapedPath 在每个请求上执行通用转义扫描。
func requestPath(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.RawPath != "" {
		return u.RawPath
	}
	return u.Path
}
