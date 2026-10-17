package ws

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// extendedWriter records the response of an extended CONNECT handshake. | extendedWriter 记录 extended CONNECT 握手的响应。
type extendedWriter struct {
	header http.Header
	body   []byte
	status int
	read   time.Time
	write  time.Time
}

func (w *extendedWriter) Header() http.Header { return w.header }
func (w *extendedWriter) Write(p []byte) (int, error) {
	w.body = append(w.body, p...)
	return len(p), nil
}
func (w *extendedWriter) WriteHeader(status int)  { w.status = status }
func (w *extendedWriter) FlushError() error       { return nil }
func (w *extendedWriter) Flush()                  {}
func (w *extendedWriter) EnableFullDuplex() error { return nil }
func (w *extendedWriter) SetReadDeadline(t time.Time) error {
	w.read = t
	return nil
}
func (w *extendedWriter) SetWriteDeadline(t time.Time) error {
	w.write = t
	return nil
}

// newExtendedRequest builds a CONNECT request in the given HTTP/2 or HTTP/3 shape. | newExtendedRequest 构造指定 HTTP/2 或 HTTP/3 形态的 CONNECT 请求。
func newExtendedRequest(t *testing.T, major int, proto string, headers map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodConnect, "/ws", io.NopCloser(strings.NewReader("")))
	r.ProtoMajor = major
	r.Proto = proto
	r.Header.Set("Sec-WebSocket-Version", "13")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}))
	r.RemoteAddr = "127.0.0.1:54321"
	return r
}

// TestUpgradeExtendedConnectHTTP2 pins the RFC 8441 response shape. | TestUpgradeExtendedConnectHTTP2 固定 RFC 8441 的响应形态。
func TestUpgradeExtendedConnectHTTP2(t *testing.T) {
	r := newExtendedRequest(t, 2, "HTTP/2.0", map[string]string{":protocol": "websocket"})
	w := &extendedWriter{header: make(http.Header)}

	c, err := Upgrade(w, r, Options{})
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if w.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 | 状态 = %d，期望 200", w.status, w.status)
	}
	if len(w.body) != 0 {
		t.Fatalf("handshake wrote a body: %q | 握手写出了正文: %q", w.body, w.body)
	}
	for _, forbidden := range []string{"Sec-WebSocket-Accept", "Upgrade", "Connection"} {
		if got := w.header.Get(forbidden); got != "" {
			t.Errorf("response header %s = %q, must be absent | 响应头 %s = %q，不得出现", forbidden, got, forbidden, got)
		}
	}
	if c.TransportKind() != TransportHTTP2 {
		t.Fatalf("TransportKind = %v, want TransportHTTP2", c.TransportKind())
	}
	if !w.read.IsZero() || !w.write.IsZero() {
		t.Fatalf("deadlines were not cleared: read=%v write=%v | 截止时间未被清除", w.read, w.write)
	}
}

// TestUpgradeExtendedConnectHTTP3 pins the quic-go request shape. | TestUpgradeExtendedConnectHTTP3 固定 quic-go 的请求形态。
func TestUpgradeExtendedConnectHTTP3(t *testing.T) {
	r := newExtendedRequest(t, 3, "websocket", nil)
	w := &extendedWriter{header: make(http.Header)}

	c, err := Upgrade(w, r, Options{})
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if w.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.status)
	}
	if c.TransportKind() != TransportHTTP3 {
		t.Fatalf("TransportKind = %v, want TransportHTTP3", c.TransportKind())
	}
}

// TestUpgradeExtendedConnectRejectsBadVersion proves the version gate still applies. | TestUpgradeExtendedConnectRejectsBadVersion 证明版本门槛仍然生效。
func TestUpgradeExtendedConnectRejectsBadVersion(t *testing.T) {
	r := newExtendedRequest(t, 2, "HTTP/2.0", map[string]string{":protocol": "websocket"})
	r.Header.Del("Sec-WebSocket-Version")
	w := &extendedWriter{header: make(http.Header)}

	if _, err := Upgrade(w, r, Options{}); !errors.Is(err, ErrHandshake) {
		t.Fatalf("err = %v, want ErrHandshake | err = %v，期望 ErrHandshake", err, err)
	}
	if w.status != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want 426", w.status)
	}
	if got := w.header.Get("Sec-WebSocket-Version"); got != "13" {
		t.Fatalf("Sec-WebSocket-Version = %q, want 13", got)
	}
}

// TestUpgradeExtendedConnectOrigin proves the origin policy is shared with HTTP/1.1. | TestUpgradeExtendedConnectOrigin 证明 Origin 策略与 HTTP/1.1 共用。
func TestUpgradeExtendedConnectOrigin(t *testing.T) {
	r := newExtendedRequest(t, 2, "HTTP/2.0", map[string]string{":protocol": "websocket", "Origin": "http://evil.example"})
	w := &extendedWriter{header: make(http.Header)}

	_, err := Upgrade(w, r, Options{CheckOrigin: func(*http.Request) bool { return false }})
	if !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("err = %v, want ErrOriginNotAllowed", err)
	}
	if w.status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.status)
	}
}

// TestUpgradeExtendedConnectSubprotocolCaseSensitive proves RFC 7936 applies here too. | TestUpgradeExtendedConnectSubprotocolCaseSensitive 证明 RFC 7936 在此同样生效。
func TestUpgradeExtendedConnectSubprotocolCaseSensitive(t *testing.T) {
	r := newExtendedRequest(t, 2, "HTTP/2.0", map[string]string{
		":protocol":              "websocket",
		"Sec-WebSocket-Protocol": "Chat",
	})
	w := &extendedWriter{header: make(http.Header)}

	c, err := Upgrade(w, r, Options{Subprotocols: []string{"chat"}})
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if got := w.header.Get("Sec-WebSocket-Protocol"); got != "" {
		t.Fatalf("Sec-WebSocket-Protocol = %q, want none | 期望不选择子协议", got)
	}
}

// TestUpgradeExtendedConnectNoKeyRequired proves the RFC 6455 key exchange is absent. | TestUpgradeExtendedConnectNoKeyRequired 证明 RFC 6455 的 key 交换不再使用。
func TestUpgradeExtendedConnectNoKeyRequired(t *testing.T) {
	r := newExtendedRequest(t, 2, "HTTP/2.0", map[string]string{":protocol": "websocket"})
	if r.Header.Get("Sec-WebSocket-Key") != "" {
		t.Fatal("test request unexpectedly carries Sec-WebSocket-Key | 测试请求意外携带 Sec-WebSocket-Key")
	}
	w := &extendedWriter{header: make(http.Header)}

	c, err := Upgrade(w, r, Options{})
	if err != nil {
		t.Fatalf("Upgrade required a key: %v | Upgrade 要求了 key: %v", err, err)
	}
	_ = c.CloseNow()
}

// TestUpgradeExtendedConnectCompression proves RFC 7692 negotiation is reused. | TestUpgradeExtendedConnectCompression 证明 RFC 7692 协商被复用。
func TestUpgradeExtendedConnectCompression(t *testing.T) {
	r := newExtendedRequest(t, 2, "HTTP/2.0", map[string]string{
		":protocol":                "websocket",
		"Sec-WebSocket-Extensions": "permessage-deflate; client_max_window_bits",
	})
	w := &extendedWriter{header: make(http.Header)}

	c, err := Upgrade(w, r, Options{EnableCompression: true})
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if got := w.header.Get("Sec-WebSocket-Extensions"); got == "" {
		t.Fatal("Sec-WebSocket-Extensions missing | 缺少 Sec-WebSocket-Extensions")
	}
}
