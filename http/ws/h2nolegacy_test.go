//go:build !http2legacy

package ws

// h2ClientTransportAvailable reports whether golang.org/x/net/http2 can send
// the :protocol pseudo-header. On Go 1.27 without the http2legacy build tag,
// x/net/http2.Transport wraps net/http.Transport, whose header validation
// rejects :protocol. | h2ClientTransportAvailable 报告 golang.org/x/net/http2 能否发送
// :protocol 伪头。Go 1.27 未加 http2legacy 构建标签时，x/net/http2.Transport 包装了
// net/http.Transport，其头部校验会拒绝 :protocol。
const h2ClientTransportAvailable = false
