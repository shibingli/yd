package ws

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// roundTripperFunc adapts a function to http.RoundTripper. | roundTripperFunc 把函数适配为 http.RoundTripper。
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// scriptedResponse builds the minimal response a fake transport returns. | scriptedResponse 构造假传输返回的最小响应。
func scriptedResponse(status int, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(""))}
}

// captureTransport records the request and returns a scripted response. | captureTransport 记录请求并返回脚本化响应。
func captureTransport(t *testing.T, captured **http.Request, status int, header http.Header) http.RoundTripper {
	t.Helper()
	return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		*captured = r
		return scriptedResponse(status, header), nil
	})
}

// TestDialExtendedConnectHTTP2Request pins the RFC 8441 client request shape. | TestDialExtendedConnectHTTP2Request 固定 RFC 8441 客户端请求形态。
func TestDialExtendedConnectHTTP2Request(t *testing.T) {
	var req *http.Request
	tr := captureTransport(t, &req, http.StatusOK, nil)

	c, err := Dial(context.Background(), "wss://example.com/ws", DialOptions{
		Transport:       tr,
		ExtendedConnect: ExtendedConnectHTTP2,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()

	if req.Method != http.MethodConnect {
		t.Errorf("method = %q, want CONNECT", req.Method)
	}
	if req.URL.Scheme != "https" || req.URL.Host != "example.com" || req.URL.Path != "/ws" {
		t.Errorf("URL = %s, want https://example.com/ws", req.URL)
	}
	if got := req.Header.Get(":protocol"); got != "websocket" {
		t.Errorf(":protocol = %q, want websocket", got)
	}
	if got := req.Header.Get("Sec-WebSocket-Version"); got != "13" {
		t.Errorf("Sec-WebSocket-Version = %q, want 13", got)
	}
	for _, forbidden := range []string{"Sec-WebSocket-Key", "Connection", "Upgrade"} {
		if got := req.Header.Get(forbidden); got != "" {
			t.Errorf("%s = %q, must be absent | %s = %q，不得出现", forbidden, got, forbidden, got)
		}
	}
	if c.TransportKind() != TransportHTTP2 {
		t.Fatalf("TransportKind = %v, want TransportHTTP2", c.TransportKind())
	}
}

// TestDialExtendedConnectHTTP3Request pins quic-go's req.Proto convention. | TestDialExtendedConnectHTTP3Request 固定 quic-go 的 req.Proto 约定。
func TestDialExtendedConnectHTTP3Request(t *testing.T) {
	var req *http.Request
	tr := captureTransport(t, &req, http.StatusOK, nil)

	c, err := Dial(context.Background(), "wss://example.com/ws", DialOptions{
		Transport:       tr,
		ExtendedConnect: ExtendedConnectHTTP3,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()

	if req.Proto != "websocket" {
		t.Errorf("Proto = %q, want websocket", req.Proto)
	}
	if got := req.Header.Get(":protocol"); got != "" {
		t.Errorf(":protocol = %q, must be absent for HTTP/3", got)
	}
	if c.TransportKind() != TransportHTTP3 {
		t.Fatalf("TransportKind = %v, want TransportHTTP3", c.TransportKind())
	}
}

// TestDialExtendedConnectOptionErrors pins the option validation sentinels. | TestDialExtendedConnectOptionErrors 固定选项校验哨兵。
func TestDialExtendedConnectOptionErrors(t *testing.T) {
	rt := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return scriptedResponse(http.StatusOK, nil), nil
	})
	cases := []struct {
		name string
		url  string
		opts DialOptions
		want error
	}{
		{
			name: "transport without mode",
			url:  "wss://example.com/ws",
			opts: DialOptions{Transport: rt},
			want: ErrExtendedConnectOptions,
		},
		{
			name: "mode without transport",
			url:  "wss://example.com/ws",
			opts: DialOptions{ExtendedConnect: ExtendedConnectHTTP3},
			want: ErrExtendedConnectOptions,
		},
		{
			name: "h3 cleartext",
			url:  "ws://example.com/ws",
			opts: DialOptions{Transport: rt, ExtendedConnect: ExtendedConnectHTTP3},
			want: ErrExtendedConnectScheme,
		},
		{
			name: "connection header",
			url:  "wss://example.com/ws",
			opts: DialOptions{Transport: rt, ExtendedConnect: ExtendedConnectHTTP2, Headers: http.Header{"Connection": {"Upgrade"}}},
			want: ErrExtendedConnectHeader,
		},
		{
			name: "upgrade header",
			url:  "wss://example.com/ws",
			opts: DialOptions{Transport: rt, ExtendedConnect: ExtendedConnectHTTP2, Headers: http.Header{"Upgrade": {"websocket"}}},
			want: ErrExtendedConnectHeader,
		},
		{
			name: "host header",
			url:  "wss://example.com/ws",
			opts: DialOptions{Transport: rt, ExtendedConnect: ExtendedConnectHTTP2, Headers: http.Header{"Host": {"example.com"}}},
			want: ErrExtendedConnectHeader,
		},
		{
			name: "key header",
			url:  "wss://example.com/ws",
			opts: DialOptions{Transport: rt, ExtendedConnect: ExtendedConnectHTTP2, Headers: http.Header{"Sec-WebSocket-Key": {"x"}}},
			want: ErrExtendedConnectHeader,
		},
		{
			name: "protocol header",
			url:  "wss://example.com/ws",
			opts: DialOptions{Transport: rt, ExtendedConnect: ExtendedConnectHTTP2, Headers: http.Header{":protocol": {"websocket"}}},
			want: ErrExtendedConnectHeader,
		},
		{
			name: "unknown mode",
			url:  "wss://example.com/ws",
			opts: DialOptions{Transport: rt, ExtendedConnect: ExtendedConnectMode(99)},
			want: ErrExtendedConnectOptions,
		},
		{
			name: "mixed-case pseudo header",
			url:  "wss://example.com/ws",
			opts: DialOptions{Transport: rt, ExtendedConnect: ExtendedConnectHTTP2, Headers: http.Header{":Protocol": {"websocket"}}},
			want: ErrExtendedConnectHeader,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Dial(context.Background(), tc.url, tc.opts)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestDialExtendedConnectStatus proves a non-200 response is a handshake failure. | TestDialExtendedConnectStatus 证明非 200 响应属于握手失败。
func TestDialExtendedConnectStatus(t *testing.T) {
	var req *http.Request
	tr := captureTransport(t, &req, http.StatusBadRequest, nil)
	_, err := Dial(context.Background(), "wss://example.com/ws", DialOptions{
		Transport:       tr,
		ExtendedConnect: ExtendedConnectHTTP2,
	})
	if !errors.Is(err, ErrHandshake) {
		t.Fatalf("err = %v, want ErrHandshake", err)
	}
}

// TestDialExtendedConnectSubprotocolCaseSensitive proves RFC 7936 applies to the client too. | TestDialExtendedConnectSubprotocolCaseSensitive 证明 RFC 7936 同样约束客户端。
func TestDialExtendedConnectSubprotocolCaseSensitive(t *testing.T) {
	var req *http.Request
	header := make(http.Header)
	header.Set("Sec-WebSocket-Protocol", "Chat")
	tr := captureTransport(t, &req, http.StatusOK, header)
	_, err := Dial(context.Background(), "wss://example.com/ws", DialOptions{
		Transport:       tr,
		ExtendedConnect: ExtendedConnectHTTP2,
		Subprotocols:    []string{"chat"},
	})
	if !errors.Is(err, ErrUnrequestedSubprotocol) {
		t.Fatalf("err = %v, want ErrUnrequestedSubprotocol", err)
	}
}

// TestDialExtendedConnectCompression proves RFC 7692 negotiation is reused. | TestDialExtendedConnectCompression 证明 RFC 7692 协商被复用。
func TestDialExtendedConnectCompression(t *testing.T) {
	var req *http.Request
	header := make(http.Header)
	header.Set("Sec-WebSocket-Extensions", "permessage-deflate")
	tr := captureTransport(t, &req, http.StatusOK, header)
	c, err := Dial(context.Background(), "wss://example.com/ws", DialOptions{
		Transport:              tr,
		ExtendedConnect:        ExtendedConnectHTTP2,
		EnableWriteCompression: true,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if c.compression == nil {
		t.Fatal("compression was not negotiated | 压缩未被协商")
	}
}

// TestDialExtendedConnectNilBodyDoesNotPanic guards the timeout branch against a
// misbehaving RoundTripper that returns a nil Body. | TestDialExtendedConnectNilBodyDoesNotPanic
// 防止超时分支在返回 nil Body 的异常 RoundTripper 上 panic。
func TestDialExtendedConnectNilBodyDoesNotPanic(t *testing.T) {
	tr := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		time.Sleep(20 * time.Millisecond)
		return &http.Response{StatusCode: http.StatusOK}, nil
	})
	_, err := Dial(context.Background(), "wss://example.com/ws", DialOptions{
		Transport:        tr,
		ExtendedConnect:  ExtendedConnectHTTP2,
		HandshakeTimeout: 2 * time.Millisecond,
	})
	if !errors.Is(err, ErrHandshake) {
		t.Fatalf("err = %v, want ErrHandshake timeout | err = %v，期望握手超时", err, err)
	}
}
