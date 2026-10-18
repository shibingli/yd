package ws

import (
	"bufio"
	"crypto/sha1"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/http/protocol"
)

// acceptSum computes SHA1( Sec-WebSocket-Key + GUID ) without allocating. | acceptSum 无分配地计算 SHA1( Sec-WebSocket-Key + GUID )。
//
// A normal Sec-WebSocket-Key is 24 bytes, so the concatenation fits on the
// stack and sha1.Sum needs no hash object or output slice. | 正常的
// Sec-WebSocket-Key 为 24 字节，拼接后可放在栈上，sha1.Sum 不需要哈希对象或输出切片。
func acceptSum(key string) [sha1.Size]byte {
	var buf [128]byte
	if len(key)+len(websocketGUID) <= len(buf) {
		n := copy(buf[:], key)
		n += copy(buf[n:], websocketGUID)
		return sha1.Sum(buf[:n])
	}
	return sha1.Sum([]byte(key + websocketGUID))
}

// acceptKey computes the Sec-WebSocket-Accept value as specified by RFC 6455 §4.2.2. | acceptKey 按 RFC 6455 §4.2.2 计算 Sec-WebSocket-Accept。
//
// Accept = base64( SHA1( Sec-WebSocket-Key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11" ) )
// The computation reuses the framework's b64x adaptive Base64 layer (the amd64 path uses a SIMD-accelerated implementation). | 计算复用框架的 b64x 自适应 Base64 层（amd64 走 SIMD 加速实现）。
// acceptScratch holds the digest and encoded accept value of one handshake.
// Reusing it through a pool keeps the only per-call allocation the returned
// string, even though b64x.Encode may let the scratch escape. | acceptScratch
// 保存一次握手的摘要与编码结果；经池复用后，每次调用只剩返回字符串这一次分配，
// 即使 b64x.Encode 会让暂存对象逃逸。
type acceptScratch struct {
	sum [sha1.Size]byte
	b64 [28]byte
}

// acceptScratchPool reuses handshake scratch buffers across connections. | acceptScratchPool
// 在连接之间复用握手暂存缓冲。
var acceptScratchPool = sync.Pool{New: func() any { return new(acceptScratch) }}

func acceptKey(key string) string {
	p := acceptScratchPool.Get().(*acceptScratch)
	p.sum = acceptSum(key)
	b64x.StdEncoding.Encode(p.b64[:], p.sum[:])
	out := string(p.b64[:])
	acceptScratchPool.Put(p)
	return out
}

// headerContainsToken reports whether a comma-separated header contains the given token (case-insensitive). | headerContainsToken 报告以逗号分隔的头部中是否包含指定 token（大小写不敏感）。
func headerContainsToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for v != "" {
			var part string
			part, v = nextToken(v)
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// nextToken cuts the first comma-separated element off s and returns it with the remainder. | nextToken 从 s 中切出第一个逗号分隔元素及剩余部分。
func nextToken(s string) (token, rest string) {
	if i := strings.IndexByte(s, ','); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// Upgrade turns an HTTP request into a WebSocket connection. | Upgrade 把 HTTP 请求升级为 WebSocket 连接。
//
// On failure the corresponding HTTP error response has already been written, and the returned error can be used for logging. | 失败时已写出相应的 HTTP 错误响应，返回的错误可用于日志。
//
// HTTP/1.1 uses the RFC 6455 Upgrade handshake and hijacks the connection; the
// handler may hand the Conn to another goroutine and return. HTTP/2 and HTTP/3
// use RFC 8441 / RFC 9220 extended CONNECT: the Conn is bound to the request
// stream, so the handler **must stay alive until the connection ends** —
// returning closes the stream and makes the Conn unusable. | HTTP/1.1 使用 RFC 6455
// Upgrade 握手并劫持连接，处理器可以把 Conn 交给其它协程后返回；HTTP/2 与 HTTP/3 使用
// RFC 8441 / RFC 9220 extended CONNECT，Conn 绑定在请求流上，因此处理器**必须阻塞到连接结束**——
// 返回即关闭流，Conn 随即不可用。
func Upgrade(w http.ResponseWriter, r *http.Request, o Options) (*Conn, error) {
	o = o.normalize()

	if protocol.IsExtendedConnect(r) {
		return upgradeExtendedConnect(w, r, o)
	}

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
	if v := r.Header.Get(headerSecWebSocketVersion); v != "13" {
		w.Header().Set(headerSecWebSocketVersion, "13")
		http.Error(w, "only WebSocket version 13 is supported | 仅支持 WebSocket 版本 13", http.StatusUpgradeRequired)
		return nil, errx.Detail(ErrHandshake, "unsupported version %[1]q | 版本 %[1]q 不受支持", v)
	}

	key := r.Header.Get(headerSecWebSocketKey)
	// Decode into a stack array: the key is exactly 16 bytes, so no result
	// allocation is needed for the validation. | 解码到栈上数组：key 恰好 16 字节，
	// 校验不需要为结果分配内存。
	var keyRaw [24]byte
	n, err := b64x.StdEncoding.Decode(keyRaw[:], []byte(key))
	if err != nil || n != 16 {
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

	// The HTTP server hands over its bufio pair with the hijacked connection.
	// Reuse it when the sizes match (the common case), avoiding a second pair of
	// 4 KiB buffers per connection. Pending writer bytes must be flushed before
	// the 101 response goes directly to netConn. | HTTP 服务器随劫持连接交出它的
	// bufio 对；尺寸匹配（常见情况）时复用，避免每条连接再分配一对 4 KiB 缓冲。
	// 写缓冲中的待发字节必须在 101 响应直接写 netConn 之前刷出。
	var br *bufio.Reader
	var bw *bufio.Writer
	if brw != nil {
		if brw.Writer != nil {
			if brw.Writer.Buffered() > 0 {
				if err := brw.Writer.Flush(); err != nil {
					_ = netConn.Close()
					return nil, errx.WrapDetail(err, ErrHandshake,
						"failed to flush hijacked buffers | 刷新劫持缓冲失败")
				}
			}
			if brw.Writer.Size() == o.WriteBufferSize {
				brw.Writer.Reset(netConn)
				bw = brw.Writer
			}
		}
		if brw.Reader != nil {
			if brw.Reader.Buffered() > 0 {
				// Pipelined bytes must survive: use the reader as-is. | 流水线字节必须保留：原样使用该读取器。
				br = brw.Reader
			} else if brw.Reader.Size() == o.ReadBufferSize {
				brw.Reader.Reset(netConn)
				br = brw.Reader
			}
		}
	}

	// Build the 101 response directly in a byte slice: the previous
	// strings.Builder path copied the whole response one more time when it was
	// converted for Write. | 直接在字节切片中构造 101 响应：原先的
	// strings.Builder 路径在写给 Write 时还要把整个响应再复制一次。
	resp := make([]byte, 0, 160)
	resp = append(resp, "HTTP/1.1 101 Switching Protocols\r\n"...)
	resp = append(resp, "Upgrade: websocket\r\n"...)
	resp = append(resp, "Connection: Upgrade\r\n"...)
	resp = append(resp, "Sec-WebSocket-Accept: "...)
	sum := acceptSum(key)
	resp = b64x.StdEncoding.AppendEncode(resp, sum[:])
	resp = append(resp, "\r\n"...)
	if sub != "" {
		resp = append(resp, "Sec-WebSocket-Protocol: "...)
		resp = append(resp, sub...)
		resp = append(resp, "\r\n"...)
	}
	if extReply != "" {
		resp = append(resp, "Sec-WebSocket-Extensions: "...)
		resp = append(resp, extReply...)
		resp = append(resp, "\r\n"...)
	}
	resp = append(resp, "\r\n"...)

	if _, err := netConn.Write(resp); err != nil {
		_ = netConn.Close()
		return nil, errx.WrapDetail(err, ErrHandshake, "failed to write handshake response | 写入握手响应失败")
	}

	c := newConnWithBuffers(netConn, o, false, br, bw)
	c.subprotocol = sub
	c.setCompression(compCfg)
	// Registration must happen after the handshake response is written: only then is the connection a usable WebSocket that a close frame can address. | 登记必须在握手响应写出之后：此刻连接已是可用的 WebSocket，关闭帧才有意义。
	c.setTracker(o.Tracker)
	return c, nil
}

// upgradeExtendedConnect performs the RFC 8441 / RFC 9220 server handshake. | upgradeExtendedConnect 执行 RFC 8441 / RFC 9220 服务端握手。
//
// It never uses Sec-WebSocket-Key/Accept (those are superseded by :protocol),
// never writes Connection or Upgrade, and responds with 200 instead of 101. | 它不使用
// Sec-WebSocket-Key/Accept（已由 :protocol 取代），不写 Connection 或 Upgrade，并以 200 而非 101 响应。
func upgradeExtendedConnect(w http.ResponseWriter, r *http.Request, o Options) (*Conn, error) {
	if v := r.Header.Get(headerSecWebSocketVersion); v != "13" {
		w.Header().Set(headerSecWebSocketVersion, "13")
		http.Error(w, "only WebSocket version 13 is supported | 仅支持 WebSocket 版本 13", http.StatusUpgradeRequired)
		return nil, errx.Detail(ErrHandshake, "unsupported version %[1]q | 版本 %[1]q 不受支持", v)
	}
	if !checkOrigin(r, o.CheckOrigin) {
		http.Error(w, "Origin check failed | Origin 校验未通过", http.StatusForbidden)
		return nil, ErrOriginNotAllowed
	}

	sub := selectSubprotocol(r, o.Subprotocols)
	var (
		compCfg  *compressionConfig
		extReply string
	)
	if o.EnableCompression {
		compCfg, extReply = negotiateServerCompression(r.Header)
	}

	rc := http.NewResponseController(w)
	// HTTP/2 requires full duplex before the response starts; quic-go's HTTP/3
	// writer is inherently duplex and reports ErrNotSupported. | HTTP/2 必须在响应
	// 开始前启用全双工；quic-go 的 HTTP/3 写入器天然全双工，会返回 ErrNotSupported。
	if err := rc.EnableFullDuplex(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		http.Error(w, "full duplex is unavailable | 当前连接不支持全双工", http.StatusInternalServerError)
		return nil, errx.WrapDetail(err, ErrHandshake, "failed to enable full duplex | 启用全双工失败")
	}
	// The server arms a WriteTimeout timer for every HTTP/2 stream. A WebSocket
	// is long-lived, so the handshake takes ownership of both deadlines. | 服务端会为
	// 每个 HTTP/2 流设置 WriteTimeout 定时器；WebSocket 是长连接，因此握手接管两个截止时间。
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	if sub != "" {
		w.Header().Set(headerSecWebSocketProtocol, sub)
	}
	if extReply != "" {
		w.Header().Set(headerSecWebSocketExtensions, extReply)
	}
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return nil, errx.WrapDetail(err, ErrHandshake, "failed to flush the handshake response | 刷新握手响应失败")
	}

	body := r.Body
	if body == nil {
		body = http.NoBody
	}
	sc := newStreamConnServer(body, w, rc, extendedLocalAddr(r), extendedRemoteAddr(r))
	c := newConn(sc, o, false)
	if r.ProtoMajor == 3 {
		c.kind = TransportHTTP3
	} else {
		c.kind = TransportHTTP2
	}
	c.subprotocol = sub
	c.setCompression(compCfg)
	c.setTracker(o.Tracker)
	return c, nil
}

// extendedLocalAddr returns the local address recorded on the request context. | extendedLocalAddr 返回记录在请求上下文中的本地地址。
func extendedLocalAddr(r *http.Request) net.Addr {
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		return a
	}
	return nil
}

// extendedRemoteAddr returns the peer address with the transport-specific network name. | extendedRemoteAddr 返回带传输网络名的对端地址。
func extendedRemoteAddr(r *http.Request) net.Addr {
	if r.RemoteAddr == "" {
		return nil
	}
	network := "tcp"
	if r.ProtoMajor == 3 {
		network = "udp"
	}
	return streamAddr{network: network, addr: r.RemoteAddr}
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
	for _, want := range supported {
		for _, v := range r.Header.Values(headerSecWebSocketProtocol) {
			for v != "" {
				var p string
				p, v = nextToken(v)
				if strings.TrimSpace(p) == want {
					return want
				}
			}
		}
	}
	return ""
}

// errNotSwitchingProtocols indicates that the peer did not return 101. | errNotSwitchingProtocols 表示对端未返回 101。
var errNotSwitchingProtocols = errx.New("ws: peer did not return 101 Switching Protocols | ws：对端未返回 101 Switching Protocols")
