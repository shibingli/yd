//go:build http2legacy

package ws

// h2ClientTransportAvailable reports whether golang.org/x/net/http2 uses the
// legacy transport, which sends the :protocol pseudo-header. | h2ClientTransportAvailable
// 报告 golang.org/x/net/http2 是否使用旧版传输，从而能发送 :protocol 伪头。
const h2ClientTransportAvailable = true
