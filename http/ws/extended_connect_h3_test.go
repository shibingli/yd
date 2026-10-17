package ws

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
)

// testTLSConfig builds an in-memory self-signed certificate for 127.0.0.1. | testTLSConfig 为 127.0.0.1 构造内存中的自签证书。
func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "yd-ws-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   []string{"h3"},
	}
}

// TestHTTP3ExtendedConnect proves the full RFC 9220 path with quic-go. | TestHTTP3ExtendedConnect 用 quic-go 证明完整的 RFC 9220 路径。
func TestHTTP3ExtendedConnect(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Proto != "websocket" {
			t.Errorf("server saw Proto = %q, want websocket | 服务端看到 Proto = %q，期望 websocket", r.Proto, r.Proto)
		}
		c, err := Upgrade(w, r, Options{EnableCompression: true})
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		defer func() { _ = c.CloseNow() }()
		for {
			op, p, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(op, p); err != nil {
				return
			}
		}
	})

	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	srv := &http3.Server{TLSConfig: testTLSConfig(t), Handler: handler}
	go func() { _ = srv.Serve(udp) }()
	t.Cleanup(func() { _ = srv.Close() })

	tr := &http3.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	t.Cleanup(func() { _ = tr.Close() })

	c, err := Dial(context.Background(), "wss://"+udp.LocalAddr().String()+"/echo", DialOptions{
		Transport:              tr,
		ExtendedConnect:        ExtendedConnectHTTP3,
		EnableWriteCompression: true,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if c.TransportKind() != TransportHTTP3 {
		t.Fatalf("TransportKind = %v, want TransportHTTP3", c.TransportKind())
	}
	if c.compression == nil {
		t.Fatal("permessage-deflate was not negotiated over HTTP/3 | HTTP/3 上未协商 permessage-deflate")
	}

	if err := c.WriteMessage(TextMessage, []byte("hello-h3")); err != nil {
		t.Fatalf("write text: %v", err)
	}
	op, p, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read text: %v", err)
	}
	if op != TextMessage || string(p) != "hello-h3" {
		t.Fatalf("echo = %v %q", op, p)
	}

	large := []byte(strings.Repeat("yd-http3-compression-", 256))
	if err := c.WriteMessage(BinaryMessage, large); err != nil {
		t.Fatalf("write compressed: %v", err)
	}
	op, p, err = c.ReadMessage()
	if err != nil {
		t.Fatalf("read compressed: %v", err)
	}
	if op != BinaryMessage || string(p) != string(large) {
		t.Fatalf("compressed echo mismatch: op=%v len=%d", op, len(p))
	}

	if err := c.WriteControl(CloseMessage, []byte{0x03, 0xE8}, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("write close: %v", err)
	}
	_, _, err = c.ReadMessage()
	var closeErr *CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != CloseNormalClosure {
		t.Fatalf("read after close = %v, want CloseError(1000) | 关闭后读取 = %v，期望 CloseError(1000)", err, err)
	}
}
