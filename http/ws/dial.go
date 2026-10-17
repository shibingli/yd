package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/core/errx"
)

// ExtendedConnectMode selects the pseudo-header convention used by
// DialOptions.Transport. | ExtendedConnectMode 选择 DialOptions.Transport 使用的伪头约定。
type ExtendedConnectMode uint8

// Extended CONNECT modes. | Extended CONNECT 模式。
const (
	// ExtendedConnectDisabled uses the HTTP/1.1 Upgrade handshake. | ExtendedConnectDisabled 使用 HTTP/1.1 Upgrade 握手。
	ExtendedConnectDisabled ExtendedConnectMode = iota
	// ExtendedConnectHTTP2 uses RFC 8441: CONNECT plus the :protocol header. | ExtendedConnectHTTP2 使用 RFC 8441：CONNECT 加 :protocol 头。
	ExtendedConnectHTTP2
	// ExtendedConnectHTTP3 uses RFC 9220: CONNECT plus req.Proto, as quic-go expects. | ExtendedConnectHTTP3 使用 RFC 9220：CONNECT 加 req.Proto（quic-go 的约定）。
	ExtendedConnectHTTP3
)

// DialOptions holds the client handshake parameters. | DialOptions 是客户端握手参数。
type DialOptions struct {
	// Transport, when non-nil, carries the handshake over HTTP/2 or HTTP/3 using extended CONNECT | Transport 非 nil 时通过 HTTP/2 或 HTTP/3 的 extended CONNECT
	// (RFC 8441 / RFC 9220). It must be paired with ExtendedConnect; the caller supplies the | （RFC 8441 / RFC 9220）完成握手。它必须与 ExtendedConnect 成对出现；调用方按部署形态
	// appropriate transport (x/net/http2.Transport or quic-go's http3.Transport). | 注入合适的传输（x/net/http2.Transport 或 quic-go 的 http3.Transport）。
	Transport http.RoundTripper
	// Headers are extra request headers. | Headers 为附加请求头。
	Headers http.Header
	// TLSConfig is the TLS configuration used for wss. | TLSConfig 为 wss 场景的 TLS 配置。
	TLSConfig *tls.Config
	// Subprotocols are the subprotocols the client wants. | Subprotocols 为客户端期望的子协议。
	Subprotocols []string
	// HandshakeTimeout is the connect and handshake timeout, 10s by default. | HandshakeTimeout 为建连与握手超时，默认 10s。
	HandshakeTimeout time.Duration
	// ReadBufferSize is the read buffer size; <=0 means no intermediate buffer is introduced (read the connection directly). | ReadBufferSize 为读缓冲大小；<=0 表示不引入中间缓冲（直读连接）。
	ReadBufferSize int
	// MaxMessageSize is the per-message limit, 32MB by default. | MaxMessageSize 为单条消息上限，默认 32MB。
	MaxMessageSize int64
	// EnableWriteCompression offers permessage-deflate (RFC 7692) during the handshake and enables | EnableWriteCompression 在握手时提议 permessage-deflate（RFC 7692），并在服务端接受后
	// outbound compression once the server accepts. When the server declines, the connection simply | 启用出站压缩。服务端拒绝时连接保持不压缩，行为与未开启该选项完全一致。
	// stays uncompressed, identical to leaving the option off. It can be toggled later with | 之后可用 Conn.EnableWriteCompression 切换；入站压缩消息在协商成功后始终会被解压。
	// Conn.EnableWriteCompression; inbound compressed messages are always inflated after negotiation.
	EnableWriteCompression bool
	// ExtendedConnect selects the pseudo-header convention of Transport. | ExtendedConnect 选择 Transport 的伪头约定。
	ExtendedConnect ExtendedConnectMode
}

// Dial initiates a WebSocket handshake as a client and returns the connection. | Dial 作为客户端发起 WebSocket 握手并返回连接。
//
// It supports ws:// and wss:// and also verifies the Sec-WebSocket-Accept returned by the server, | 支持 ws:// 与 wss://；同时校验服务端返回的 Sec-WebSocket-Accept，
// so it can likewise be used for interop testing and self-checks. | 因此也可用于互操作测试与自检。
func Dial(ctx context.Context, rawURL string, o DialOptions) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrHandshake, "URL parsing failed | URL 解析失败")
	}
	switch u.Scheme {
	case "ws", "wss":
	default:
		return nil, errx.Detail(ErrHandshake, "only ws/wss are supported, got %[1]q | 仅支持 ws/wss，得到 %[1]q", u.Scheme)
	}

	timeout := o.HandshakeTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if err := validateExtendedConnect(u, o); err != nil {
		return nil, err
	}
	if o.Transport != nil {
		return dialExtended(ctx, u, o, timeout)
	}
	dialer := &net.Dialer{Timeout: timeout}
	host := u.Host
	if u.Port() == "" {
		if u.Scheme == "wss" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	var netConn net.Conn
	if u.Scheme == "wss" {
		cfg := o.TLSConfig
		if cfg == nil {
			cfg = &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}
		}
		netConn, err = tls.DialWithDialer(dialer, "tcp", host, cfg)
	} else {
		netConn, err = dialer.DialContext(ctx, "tcp", host)
	}
	if err != nil {
		return nil, errx.WrapDetail(err, ErrHandshake, "connect failed | 建连失败")
	}

	// generate a 16-byte random key | 生成 16 字节随机 key
	var keyRaw [16]byte
	if _, err := rand.Read(keyRaw[:]); err != nil {
		_ = netConn.Close()
		return nil, err
	}
	key := b64x.StdEncoding.EncodeToString(keyRaw[:])

	reqPath := u.RequestURI()
	if reqPath == "" {
		reqPath = "/"
	}

	var sb strings.Builder
	sb.WriteString("GET ")
	sb.WriteString(reqPath)
	sb.WriteString(" HTTP/1.1\r\nHost: ")
	sb.WriteString(u.Host)
	sb.WriteString("\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: ")
	sb.WriteString(key)
	sb.WriteString("\r\nSec-WebSocket-Version: 13\r\n")
	if len(o.Subprotocols) > 0 {
		sb.WriteString("Sec-WebSocket-Protocol: ")
		sb.WriteString(strings.Join(o.Subprotocols, ", "))
		sb.WriteString("\r\n")
	}
	if o.EnableWriteCompression {
		// The bare client_max_window_bits parameter announces that we can honour a server-chosen window size; | 无值的 client_max_window_bits 参数表示我们能够履行服务端选定的窗口大小；
		// the server's reply is validated by negotiateClientCompression. | 服务端的回复由 negotiateClientCompression 校验。
		sb.WriteString("Sec-WebSocket-Extensions: permessage-deflate; client_max_window_bits\r\n")
	}
	for k, vs := range o.Headers {
		for _, v := range vs {
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(v)
			sb.WriteString("\r\n")
		}
	}
	sb.WriteString("\r\n")

	deadline := time.Now().Add(timeout)
	_ = netConn.SetDeadline(deadline)
	if _, err := netConn.Write([]byte(sb.String())); err != nil {
		_ = netConn.Close()
		return nil, errx.WrapDetail(err, ErrHandshake, "failed to send handshake request | 发送握手请求失败")
	}

	br := bufio.NewReader(netConn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = netConn.Close()
		return nil, errx.WrapDetail(err, ErrHandshake, "failed to read handshake response | 读取握手响应失败")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = netConn.Close()
		return nil, errx.Detail(errNotSwitchingProtocols, "status %[1]d | 状态 %[1]d", resp.StatusCode)
	}
	if !headerContainsToken(resp.Header, "Connection", "upgrade") {
		_ = netConn.Close()
		return nil, errx.Detail(ErrHandshake, "response Connection header is invalid | 响应 Connection 头非法")
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Header.Get("Upgrade")), "websocket") {
		_ = netConn.Close()
		return nil, errx.Detail(ErrHandshake, "response Upgrade header is invalid | 响应 Upgrade 头非法")
	}
	if got, want := resp.Header.Get("Sec-WebSocket-Accept"), acceptKey(key); got != want {
		_ = netConn.Close()
		return nil, errx.Detail(ErrHandshake, "Sec-WebSocket-Accept mismatch, got %[1]q | Sec-WebSocket-Accept 不匹配（收到 %[1]q）", got)
	}
	// RFC 6455 §4.1: the server may only select one of the subprotocols the client offered; | RFC 6455 §4.1：服务端只能从客户端提议的子协议中选择一个；
	// an empty selection is legal (the connection then has no subprotocol). | 服务端不选择也是合法的（此时连接没有子协议）。
	selected, err := parseSelectedSubprotocol(resp.Header, o.Subprotocols)
	if err != nil {
		_ = netConn.Close()
		return nil, errx.Wrap(err, ErrHandshake)
	}
	// RFC 6455 §4.1: an extension may only be activated when the client offered it. | RFC 6455 §4.1：只有客户端提议过的扩展才能被启用。
	// negotiateClientCompression rejects an unrequested or malformed activation instead of continuing; | negotiateClientCompression 会拒绝未提议或非法的扩展启用，而不是继续；
	// silently ignoring it would make the two sides disagree about every following frame. | 静默忽略会让两侧对后续每一帧的理解都不一致。
	compCfg, err := negotiateClientCompression(resp.Header, o.EnableWriteCompression)
	if err != nil {
		_ = netConn.Close()
		return nil, err
	}
	_ = netConn.SetDeadline(time.Time{})

	maxSize := o.MaxMessageSize
	if maxSize <= 0 {
		maxSize = defaultMaxMessageSize
	}
	c := newConn(netConn, Options{ReadBufferSize: o.ReadBufferSize, MaxMessageSize: maxSize}, true)
	c.subprotocol = selected
	c.setCompression(compCfg)
	// the bufio.Reader used by ReadResponse may already have buffered the peer's first frame | ReadResponse 的 bufio.Reader 可能已缓冲了对端的第一帧
	if br.Buffered() > 0 || o.ReadBufferSize > 0 {
		c.br = br
	}
	return c, nil
}

// validateExtendedConnect checks the option pairing and the forbidden headers. | validateExtendedConnect 校验选项配对与禁用头部。
func validateExtendedConnect(u *url.URL, o DialOptions) error {
	if (o.Transport == nil) != (o.ExtendedConnect == ExtendedConnectDisabled) {
		return ErrExtendedConnectOptions
	}
	if o.Transport != nil && o.ExtendedConnect != ExtendedConnectHTTP2 && o.ExtendedConnect != ExtendedConnectHTTP3 {
		return errx.Detail(ErrExtendedConnectOptions, "unknown extended CONNECT mode %[1]d | 未知的 extended CONNECT 模式 %[1]d", o.ExtendedConnect)
	}
	if o.Transport == nil {
		return nil
	}
	if o.ExtendedConnect == ExtendedConnectHTTP3 && u.Scheme != "wss" {
		return errx.Detail(ErrExtendedConnectScheme, "scheme %[1]q | 协议 %[1]q", u.Scheme)
	}
	for name := range o.Headers {
		switch {
		case strings.EqualFold(name, "Connection"),
			strings.EqualFold(name, "Upgrade"),
			strings.EqualFold(name, "Host"),
			strings.EqualFold(name, "Sec-WebSocket-Key"),
			strings.HasPrefix(name, ":"):
			return errx.Detail(ErrExtendedConnectHeader, "header %[1]q | 头部 %[1]q", name)
		}
	}
	return nil
}

// parseSelectedSubprotocol validates the server's subprotocol selection. | parseSelectedSubprotocol 校验服务端的子协议选择。
//
// RFC 6455 §4.1 allows at most one selected subprotocol and requires it to be
// one the client offered; RFC 7936 makes the comparison case-sensitive. | RFC 6455 §4.1
// 最多允许一个被选中的子协议，且必须是客户端提议过的；RFC 7936 要求比较大小写敏感。
func parseSelectedSubprotocol(h http.Header, offered []string) (string, error) {
	values := h.Values("Sec-WebSocket-Protocol")
	if len(values) > 1 {
		return "", errx.Detail(ErrUnrequestedSubprotocol,
			"server sent %[1]d Sec-WebSocket-Protocol headers, at most one is allowed | 服务端发送了 %[1]d 个 Sec-WebSocket-Protocol 头，最多允许一个", len(values))
	}
	if len(values) == 0 {
		return "", nil
	}
	selected := strings.TrimSpace(values[0])
	if selected != "" && !slices.Contains(offered, selected) {
		return "", errx.Detail(ErrUnrequestedSubprotocol,
			"server selected %[1]q, which the client did not offer | 服务端选择了客户端未提议的 %[1]q", selected)
	}
	return selected, nil
}

// dialExtended performs the RFC 8441 / RFC 9220 client handshake through a
// caller-supplied RoundTripper. | dialExtended 通过调用方注入的 RoundTripper 执行
// RFC 8441 / RFC 9220 客户端握手。
func dialExtended(ctx context.Context, u *url.URL, o DialOptions, timeout time.Duration) (*Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, errx.WrapDetail(err, ErrHandshake, "context is already done | 上下文已结束")
	}

	scheme := "https"
	if u.Scheme == "ws" {
		scheme = "http"
	}
	target := &url.URL{Scheme: scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}

	pr, pw := io.Pipe()
	// The request context must outlive Dial; the timer only bounds the handshake. | 请求上下文必须比 Dial 活得更久；
	// 定时器只约束握手阶段。
	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	req, err := http.NewRequestWithContext(streamCtx, http.MethodConnect, target.String(), pr)
	if err != nil {
		cancel()
		_ = pw.CloseWithError(err)
		return nil, errx.WrapDetail(err, ErrHandshake, "failed to build the handshake request | 构造握手请求失败")
	}
	req.Host = u.Host
	req.Header.Set("Sec-WebSocket-Version", "13")
	if len(o.Subprotocols) > 0 {
		req.Header.Set("Sec-WebSocket-Protocol", strings.Join(o.Subprotocols, ", "))
	}
	if o.EnableWriteCompression {
		req.Header.Set("Sec-WebSocket-Extensions", "permessage-deflate; client_max_window_bits")
	}
	for k, vs := range o.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	switch o.ExtendedConnect {
	case ExtendedConnectHTTP2:
		req.Header.Set(":protocol", "websocket")
	case ExtendedConnectHTTP3:
		req.Proto = "websocket"
	}

	timer := time.AfterFunc(timeout, cancel)
	resp, err := o.Transport.RoundTrip(req)
	if err != nil {
		timer.Stop()
		cancel()
		_ = pw.CloseWithError(err)
		return nil, errx.WrapDetail(err, ErrHandshake, "extended CONNECT failed | extended CONNECT 失败")
	}
	if resp.Body == nil {
		resp.Body = http.NoBody
	}
	if !timer.Stop() {
		_ = resp.Body.Close()
		cancel()
		_ = pw.CloseWithError(context.DeadlineExceeded)
		return nil, errx.WrapDetail(context.DeadlineExceeded, ErrHandshake, "handshake timed out | 握手超时")
	}
	fail := func(err error) (*Conn, error) {
		_ = resp.Body.Close()
		cancel()
		_ = pw.CloseWithError(err)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return fail(errx.WrapDetail(errNotSwitchingProtocols, ErrHandshake, "status %[1]d | 状态 %[1]d", resp.StatusCode))
	}
	selected, err := parseSelectedSubprotocol(resp.Header, o.Subprotocols)
	if err != nil {
		return fail(errx.Wrap(err, ErrHandshake))
	}
	compCfg, err := negotiateClientCompression(resp.Header, o.EnableWriteCompression)
	if err != nil {
		return fail(err)
	}

	network := "ws-h2"
	kind := TransportHTTP2
	if o.ExtendedConnect == ExtendedConnectHTTP3 {
		network, kind = "ws-h3", TransportHTTP3
	}
	maxSize := o.MaxMessageSize
	if maxSize <= 0 {
		maxSize = defaultMaxMessageSize
	}
	sc := newStreamConnClient(resp.Body, pw, cancel,
		streamAddr{network: network, addr: ""},
		streamAddr{network: network, addr: u.Host})
	c := newConn(sc, Options{ReadBufferSize: o.ReadBufferSize, MaxMessageSize: maxSize}, true)
	c.kind = kind
	c.subprotocol = selected
	c.setCompression(compCfg)
	return c, nil
}
