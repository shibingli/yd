package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shibingli/yd/b64x"
	"github.com/shibingli/yd/errx"
)

// DialOptions holds the client handshake parameters. | DialOptions 是客户端握手参数。
type DialOptions struct {
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
	_ = netConn.SetDeadline(time.Time{})

	maxSize := o.MaxMessageSize
	if maxSize <= 0 {
		maxSize = defaultMaxMessageSize
	}
	c := newConn(netConn, Options{ReadBufferSize: o.ReadBufferSize, MaxMessageSize: maxSize}, true)
	c.subprotocol = resp.Header.Get("Sec-WebSocket-Protocol")
	// the bufio.Reader used by ReadResponse may already have buffered the peer's first frame | ReadResponse 的 bufio.Reader 可能已缓冲了对端的第一帧
	if br.Buffered() > 0 || o.ReadBufferSize > 0 {
		c.br = br
	}
	return c, nil
}
