package ws

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yun-dev/yd/b64x"
)

// acceptKey 按 RFC 6455 §4.2.2 计算 Sec-WebSocket-Accept。
//
// Accept = base64( SHA1( Sec-WebSocket-Key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11" ) )
// 计算复用框架的 b64x 自适应 Base64 层（amd64 走 SIMD 加速实现）。
func acceptKey(key string) string {
	h := sha1.New()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte(websocketGUID))
	sum := h.Sum(nil)
	return b64x.StdEncoding.EncodeToString(sum)
}

// headerContainsToken 报告以逗号分隔的头部中是否包含指定 token（大小写不敏感）。
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

// Upgrade 把 HTTP 请求升级为 WebSocket 连接。
//
// 失败时已写出相应的 HTTP 错误响应，返回的错误可用于日志。
// 注意：升级依赖 http.Hijacker，因此仅适用于 HTTP/1.1 连接；
// HTTP/2 与 HTTP/3 会返回 ErrHijackUnsupported（请改用 SSE）。
func Upgrade(w http.ResponseWriter, r *http.Request, o Options) (*Conn, error) {
	o = o.normalize()

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "WebSocket 握手必须使用 GET", http.StatusMethodNotAllowed)
		return nil, fmt.Errorf("%w: 方法 %s", ErrHandshake, r.Method)
	}
	if !headerContainsToken(r.Header, "Connection", "upgrade") {
		http.Error(w, "Connection 头缺少 upgrade", http.StatusBadRequest)
		return nil, fmt.Errorf("%w: Connection 头缺少 upgrade", ErrHandshake)
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		http.Error(w, "Upgrade 头必须为 websocket", http.StatusBadRequest)
		return nil, fmt.Errorf("%w: Upgrade 头非法", ErrHandshake)
	}
	if v := r.Header.Get("Sec-WebSocket-Version"); v != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "仅支持 WebSocket 版本 13", http.StatusUpgradeRequired)
		return nil, fmt.Errorf("%w: 版本 %q 不受支持", ErrHandshake, v)
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	decoded, err := b64x.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 16 {
		http.Error(w, "Sec-WebSocket-Key 必须是 16 字节的 base64", http.StatusBadRequest)
		return nil, fmt.Errorf("%w: Sec-WebSocket-Key 非法", ErrHandshake)
	}

	if !checkOrigin(r, o.CheckOrigin) {
		http.Error(w, "Origin 校验未通过", http.StatusForbidden)
		return nil, ErrOriginNotAllowed
	}

	sub := selectSubprotocol(r, o.Subprotocols)

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "当前连接不支持协议升级", http.StatusInternalServerError)
		return nil, ErrHijackUnsupported
	}
	netConn, brw, err := hj.Hijack()
	if err != nil {
		return nil, fmt.Errorf("%w: Hijack 失败: %v", ErrHandshake, err)
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
	sb.WriteString("\r\n")

	if _, err := netConn.Write([]byte(sb.String())); err != nil {
		_ = netConn.Close()
		return nil, fmt.Errorf("%w: 写入握手响应失败: %v", ErrHandshake, err)
	}

	c := newConn(netConn, o, false)
	c.subprotocol = sub
	// 客户端可能在握手后立刻流水线发送数据，Hijack 返回的读缓冲里可能已有内容，
	// 此时必须继续使用该缓冲，否则会丢帧。
	if brw != nil && brw.Reader.Buffered() > 0 {
		c.br = brw.Reader
	}
	return c, nil
}

// checkOrigin 执行 Origin 校验；custom 为 nil 时使用同源策略。
func checkOrigin(r *http.Request, custom func(*http.Request) bool) bool {
	if custom != nil {
		return custom(r)
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // 非浏览器客户端（无 Origin）放行
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// selectSubprotocol 从客户端提供的子协议中按服务端优先级挑选一个。
func selectSubprotocol(r *http.Request, supported []string) string {
	if len(supported) == 0 {
		return ""
	}
	offered := map[string]string{}
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				offered[strings.ToLower(p)] = p
			}
		}
	}
	for _, want := range supported {
		if got, ok := offered[strings.ToLower(want)]; ok {
			return got
		}
	}
	return ""
}

// errNotSwitchingProtocols 表示对端未返回 101。
var errNotSwitchingProtocols = errors.New("ws: 对端未返回 101 Switching Protocols")
