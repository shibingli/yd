// Package middleware provides the yd HTTP middleware set: panic recovery, request ids, access logs,
// CORS, rate limiting and negotiation-based response compression. | Package middleware 提供 yd 的 HTTP 中间件集合：panic 恢复、请求 ID、访问日志、CORS、限流与基于协商的响应压缩。
// Every middleware has the router.Middleware shape — a func(*router.Ctx) that may call c.Next() to
// continue the chain — so they compose with router.Use and router.HandlerFunc. | 所有中间件都是 router.Middleware 形态 —— func(*router.Ctx)，可通过 c.Next() 继续执行链，因此能与 router.Use 和 router.HandlerFunc 自由组合。
package middleware

import (
	"net/http"
	"strings"

	"github.com/shibingli/yd/http/router"
)

// Chain combines middlewares into a single middleware executed in the given order. | Chain 把多个中间件按传入顺序组合为一个中间件。
// The combined middleware runs each child in turn and finally calls c.Next() exactly once. | 组合中间件依次执行每个子中间件，最后统一调用一次 c.Next()。
// Children must therefore only perform their own work: a child that calls c.Next() itself would jump
// into the router chain early, so such wrapping middlewares belong in router.Use directly. | 因此子中间件只应完成自身逻辑：子中间件内部调用 c.Next() 会提前进入路由器链，这类「环绕式」中间件请直接用 router.Use 注册。
// nil entries are skipped, and an aborted context stops the sequence. | nil 项会被跳过；上下文被 Abort 后立即停止后续执行。
func Chain(mw ...router.Middleware) router.Middleware {
	// Copy the slice so later mutations by the caller cannot change the chain. | 复制切片，避免调用方后续修改影响已构建的链。
	list := make([]router.Middleware, 0, len(mw))
	for _, m := range mw {
		if m != nil {
			list = append(list, m)
		}
	}
	return func(c *router.Ctx) {
		for _, m := range list {
			if c.Aborted() {
				return
			}
			m(c)
		}
		if c.Aborted() {
			return
		}
		c.Next()
	}
}

// isUpgradeRequest reports whether r asks for a protocol upgrade (WebSocket, h2c, ...). | isUpgradeRequest 报告 r 是否请求协议升级（WebSocket、h2c 等）。
func isUpgradeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if headerValue(r.Header, "Upgrade") != "" {
		return true
	}
	return strings.Contains(strings.ToLower(headerValue(r.Header, "Connection")), "upgrade")
}

// headerValue returns the first value of an already-canonical header key. | headerValue 返回已规范化头部键的首个值。
//
// http.Header.Get runs textproto.CanonicalMIMEHeaderKey on every call, which is a per-byte scan of
// the key; the middleware hot path performs several of these per request. A direct lookup with the
// canonical spelling is equivalent (Set/Add always store the canonical form) and skips the scan. | http.Header.Get
// 每次都会调用 textproto.CanonicalMIMEHeaderKey，对键逐字节扫描；中间件热路径每个请求要做多次。
// 用规范化拼写直接查表与之等价（Set/Add 总是存规范化形式），并省掉这次扫描。
func headerValue(h http.Header, canonicalKey string) string {
	if v := h[canonicalKey]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// appendVary adds value to the Vary response header unless it is already listed. | appendVary 把 value 追加到 Vary 响应头，若已列出则不做任何事。
func appendVary(h http.Header, value string) {
	for _, existing := range h["Vary"] {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	h.Add("Vary", value)
}
