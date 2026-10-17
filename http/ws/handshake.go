package ws

import (
	"crypto/sha1"
	"net/http"
	"net/url"
	"strings"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/core/errx"
)

// acceptKey computes the Sec-WebSocket-Accept value as specified by RFC 6455 §4.2.2. | acceptKey 按 RFC 6455 §4.2.2 计算 Sec-WebSocket-Accept。
//
// Accept = base64( SHA1( Sec-WebSocket-Key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11" ) )
// The computation reuses the framework's b64x adaptive Base64 layer (the amd64 path uses a SIMD-accelerated implementation). | 计算复用框架的 b64x 自适应 Base64 层（amd64 走 SIMD 加速实现）。
func acceptKey(key string) string {
	h := sha1.New()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte(websocketGUID))
	sum := h.Sum(nil)
	return b64x.StdEncoding.EncodeToString(sum)
}

// headerContainsToken reports whether a comma-separated header contains the given token (case-insensitive). | headerContainsToken 报告以逗号分隔的头部中是否包含指定 token（大小写不敏感）。
func headerContainsToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// Upgrade turns an HTTP request into a WebSocket connection. | Upgrade 把 HTTP 请求升级为 WebSocket 连接。
//
// On failure the corresponding HTTP error response has already been written, and the returned error can be used for logging. | 失败时已写出相应的 HTTP 错误响应，返回的错误可用于日志。
// Note: the upgrade relies on http.Hijacker, so it only applies to HTTP/1.1 connections; | 注意：升级依赖 http.Hijacker，因此仅适用于 HTTP/1.1 连接；
// HTTP/2 and HTTP/3 return ErrHijackUnsupported (use SSE instead). | HTTP/2 与 HTTP/3 会返回 ErrHijackUnsupported（请改用 SSE）。
func Upgrade(w http.ResponseWriter, r *http.Request, o Options) (*Conn, error) {
	o = o.normalize()

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "WebSocket handshake requires GET | WebSocket 握手必须使用 GET", http.StatusMethodNotAllowed)
		return nil, errx.Detail(ErrHandshake, "method %[1]s | 方法 %[1]s", r.Method)
	}
	if !headerContainsToken(r.Header, "Connection", "upgrade") {
		http.Error(w, "Connection header lacks upgrade | Connection 头缺少 upgrade", http.StatusBadRequest)
		return nil, errx.Detail(ErrHandshake, "Connection header lacks upgrade | Connection 头缺少 upgrade")
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		http.Error(w, "Upgrade header must be websocket | Upgrade 头必须为 websocket", http.StatusBadRequest)
		return nil, errx.Detail(ErrHandshake, "Upgrade header is invalid | Upgrade 头非法")
	}
	if v := r.Header.Get("Sec-WebSocket-Version"); v != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "only WebSocket version 13 is supported | 仅支持 WebSocket 版本 13", http.StatusUpgradeRequired)
		return nil, errx.Detail(ErrHandshake, "unsupported version %[1]q | 版本 %[1]q 不受支持", v)
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	decoded, err := b64x.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 16 {
		http.Error(w, "Sec-WebSocket-Key must be 16 bytes of base64 | Sec-WebSocket-Key 必须是 16 字节的 base64", http.StatusBadRequest)
		return nil, errx.Detail(ErrHandshake, "invalid Sec-WebSocket-Key | Sec-WebSocket-Key 非法")
	}

	if !checkOrigin(r, o.CheckOrigin) {
		http.Error(w, "Origin check failed | Origin 校验未通过", http.StatusForbidden)
		return nil, ErrOriginNotAllowed
	}

	sub := selectSubprotocol(r, o.Subprotocols)

	// RFC 7692 §5: the server may only activate the extension when the client offered it; an offer it cannot honour | RFC 7692 §5：只有客户端提议过扩展，服务端才能启用；无法接受的提议
	// is declined, which leaves the connection uncompressed instead of failing the handshake. | 会被拒绝，连接保持不压缩，而不是让握手失败。
	var (
		compCfg  *compressionConfig
		extReply string
	)
	if o.EnableCompression {
		compCfg, extReply = negotiateServerCompression(r.Header)
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "the current connection does not support protocol upgrade | 当前连接不支持协议升级", http.StatusInternalServerError)
		return nil, ErrHijackUnsupported
	}
	netConn, brw, err := hj.Hijack()
	if err != nil {
		return nil, errx.WrapDetail(err, ErrHandshake, "Hijack failed | Hijack 失败")
	}

	var sb strings.Builder
	sb.Grow(160)
	sb.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	sb.WriteString("Upgrade: websocket\r\n")
	sb.WriteString("Connection: Upgrade\r\n")
	sb.WriteString("Sec-WebSocket-Accept: ")
	sb.WriteString(acceptKey(key))
	sb.WriteString("\r\n")
	if sub != "" {
		sb.WriteString("Sec-WebSocket-Protocol: ")
		sb.WriteString(sub)
		sb.WriteString("\r\n")
	}
	if extReply != "" {
		sb.WriteString("Sec-WebSocket-Extensions: ")
		sb.WriteString(extReply)
		sb.WriteString("\r\n")
	}
	sb.WriteString("\r\n")

	if _, err := netConn.Write([]byte(sb.String())); err != nil {
		_ = netConn.Close()
		return nil, errx.WrapDetail(err, ErrHandshake, "failed to write handshake response | 写入握手响应失败")
	}

	c := newConn(netConn, o, false)
	c.subprotocol = sub
	c.setCompression(compCfg)
	// Registration must happen after the handshake response is written: only then is the connection a usable WebSocket that a close frame can address. | 登记必须在握手响应写出之后：此刻连接已是可用的 WebSocket，关闭帧才有意义。
	c.setTracker(o.Tracker)
	// a client may pipeline data right after the handshake, so the read buffer returned by Hijack may already hold content; | 客户端可能在握手后立刻流水线发送数据，Hijack 返回的读缓冲里可能已有内容，
	// in that case the buffer must keep being used, or frames would be lost. | 此时必须继续使用该缓冲，否则会丢帧。
	if brw != nil && brw.Reader.Buffered() > 0 {
		c.br = brw.Reader
	}
	return c, nil
}

// checkOrigin performs the Origin check; when custom is nil, the same-origin policy is used. | checkOrigin 执行 Origin 校验；custom 为 nil 时使用同源策略。
func checkOrigin(r *http.Request, custom func(*http.Request) bool) bool {
	if custom != nil {
		return custom(r)
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser clients (no Origin) are allowed through | 非浏览器客户端（无 Origin）放行
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// selectSubprotocol picks one of the client-offered subprotocols according to server priority. | selectSubprotocol 从客户端提供的子协议中按服务端优先级挑选一个。
//
// RFC 7936 requires a case-sensitive string match: "Chat" and "chat" are different
// subprotocols even though IANA refuses to register both. | RFC 7936 要求大小写敏感的字符串
// 比较："Chat" 与 "chat" 是两个不同的子协议，即便 IANA 会拒绝同时注册它们。
func selectSubprotocol(r *http.Request, supported []string) string {
	if len(supported) == 0 {
		return ""
	}
	offered := make([]string, 0, 4)
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				offered = append(offered, p)
			}
		}
	}
	for _, want := range supported {
		for _, got := range offered {
			if got == want {
				return got
			}
		}
	}
	return ""
}

// errNotSwitchingProtocols indicates that the peer did not return 101. | errNotSwitchingProtocols 表示对端未返回 101。
var errNotSwitchingProtocols = errx.New("ws: peer did not return 101 Switching Protocols | ws：对端未返回 101 Switching Protocols")
