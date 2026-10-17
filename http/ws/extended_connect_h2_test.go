package ws

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/yd/http/protocol"
	"golang.org/x/net/http2"
)

// h2ChildEnv marks the re-executed test process. | h2ChildEnv 标记被重新执行的测试进程。
const h2ChildEnv = "YD_WS_H2_CHILD"

// requireH2Client skips client tests when the x/net transport cannot send
// :protocol. | requireH2Client 在 x/net 传输无法发送 :protocol 时跳过客户端测试。
func requireH2Client(t *testing.T) {
	t.Helper()
	if !h2ClientTransportAvailable {
		t.Skip("HTTP/2 client requires -tags http2legacy on Go 1.27: x/net/http2 wraps http.Transport, whose header validation rejects :protocol | Go 1.27 下 HTTP/2 客户端需要 -tags http2legacy：x/net/http2 包装了 http.Transport，其头部校验拒绝 :protocol")
	}
}

// runHTTP2Child re-executes this test binary with GODEBUG=http2xconnect=1.
//
// The standard library reads GODEBUG in its package init, so the only way to
// exercise the server path from a test is to restart the process with the
// variable already set. | 标准库在包 init 时读取 GODEBUG，因此测试服务端路径的唯一方式
// 是在进程启动前设置变量并重新执行测试二进制。
func runHTTP2Child(t *testing.T, name string) bool {
	t.Helper()
	if protocol.H2ExtendedConnectEnabled() {
		return false
	}
	if os.Getenv(h2ChildEnv) == "1" {
		t.Fatal("child process still lacks GODEBUG=http2xconnect=1 | 子进程仍缺少 GODEBUG=http2xconnect=1")
	}

	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "GODEBUG") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"GODEBUG="+protocol.MergeGODEBUG(os.Getenv("GODEBUG"), "http2xconnect=1"),
		h2ChildEnv+"=1",
	)
	cmd := exec.Command(os.Args[0], "-test.run=^"+regexp.QuoteMeta(name)+"$", "-test.v", "-test.count=1")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("HTTP/2 child test failed: %v\n%s", err, out)
	}
	t.Logf("HTTP/2 child test output:\n%s", out)
	return true
}

// h2EchoHandler upgrades, then echoes every message until the peer closes. | h2EchoHandler 升级后持续回显消息，直到对端关闭。
func h2EchoHandler(t *testing.T, opts Options) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, opts)
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
}

// startH2TestServer starts an httptest TLS server with HTTP/2 enabled. | startH2TestServer 启动启用 HTTP/2 的 httptest TLS 服务端。
func startH2TestServer(t *testing.T, handler http.Handler, writeTimeout time.Duration) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.WriteTimeout = writeTimeout
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, "wss" + strings.TrimPrefix(srv.URL, "https") + "/ws"
}

// h2ClientTransport returns a client transport that trusts the test certificate. | h2ClientTransport 返回信任测试证书的客户端传输。
func h2ClientTransport(t *testing.T) *http2.Transport {
	t.Helper()
	tr := &http2.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	t.Cleanup(tr.CloseIdleConnections)
	return tr
}

// TestHTTP2ExtendedConnect proves text, binary, compression and the close handshake work over H2. | TestHTTP2ExtendedConnect 证明 H2 上文本、二进制、压缩与关闭握手均可用。
func TestHTTP2ExtendedConnect(t *testing.T) {
	requireH2Client(t)
	if runHTTP2Child(t, "TestHTTP2ExtendedConnect") {
		return
	}
	_, wsURL := startH2TestServer(t, h2EchoHandler(t, Options{
		EnableCompression: true,
		Subprotocols:      []string{"chat"},
	}), 0)

	c, err := Dial(context.Background(), wsURL, DialOptions{
		Transport:              h2ClientTransport(t),
		ExtendedConnect:        ExtendedConnectHTTP2,
		Subprotocols:           []string{"chat"},
		EnableWriteCompression: true,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if c.Subprotocol() != "chat" {
		t.Fatalf("Subprotocol = %q, want chat", c.Subprotocol())
	}
	if c.TransportKind() != TransportHTTP2 {
		t.Fatalf("TransportKind = %v, want TransportHTTP2", c.TransportKind())
	}

	if err := c.WriteMessage(TextMessage, []byte("hello")); err != nil {
		t.Fatalf("write text: %v", err)
	}
	op, p, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read text: %v", err)
	}
	if op != TextMessage || string(p) != "hello" {
		t.Fatalf("echo = %v %q, want text hello", op, p)
	}

	binary := []byte{0, 1, 2, 3, 0xff}
	if err := c.WriteMessage(BinaryMessage, binary); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	op, p, err = c.ReadMessage()
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if op != BinaryMessage || string(p) != string(binary) {
		t.Fatalf("echo = %v %v, want binary %v", op, p, binary)
	}

	// A repetitive payload exercises permessage-deflate in both directions. | 重复载荷同时检验两个方向的 permessage-deflate。
	large := []byte(strings.Repeat("yd-websocket-compression-", 256))
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

// TestHTTP2SurvivesServerWriteTimeout proves the handshake clears the per-stream write timeout. | TestHTTP2SurvivesServerWriteTimeout 证明握手清除了按流的写超时。
func TestHTTP2SurvivesServerWriteTimeout(t *testing.T) {
	requireH2Client(t)
	if runHTTP2Child(t, "TestHTTP2SurvivesServerWriteTimeout") {
		return
	}
	_, wsURL := startH2TestServer(t, h2EchoHandler(t, Options{}), 200*time.Millisecond)

	c, err := Dial(context.Background(), wsURL, DialOptions{
		Transport:       h2ClientTransport(t),
		ExtendedConnect: ExtendedConnectHTTP2,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()

	time.Sleep(300 * time.Millisecond)
	if err := c.WriteMessage(TextMessage, []byte("after-timeout")); err != nil {
		t.Fatalf("write after WriteTimeout: %v", err)
	}
	_, p, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read after WriteTimeout: %v", err)
	}
	if string(p) != "after-timeout" {
		t.Fatalf("echo = %q", p)
	}
}

// TestHTTP2HandlerReturnEndsStream pins the lifetime rule: returning from the
// handler closes the stream. | TestHTTP2HandlerReturnEndsStream 固定生命周期规则：
// 处理器返回即关闭流。
func TestHTTP2HandlerReturnEndsStream(t *testing.T) {
	requireH2Client(t)
	if runHTTP2Child(t, "TestHTTP2HandlerReturnEndsStream") {
		return
	}
	returned := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, Options{})
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			close(returned)
			return
		}
		_ = c.CloseNow()
		close(returned)
	})
	_, wsURL := startH2TestServer(t, handler, 0)

	c, err := Dial(context.Background(), wsURL, DialOptions{
		Transport:       h2ClientTransport(t),
		ExtendedConnect: ExtendedConnectHTTP2,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return | 处理器未返回")
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("ReadMessage succeeded after the handler returned | 处理器返回后 ReadMessage 仍成功")
	}
}

// TestH2CExtendedConnect proves cleartext HTTP/2 (h2c) is a supported transport. | TestH2CExtendedConnect 证明明文 HTTP/2（h2c）是受支持的传输。
func TestH2CExtendedConnect(t *testing.T) {
	requireH2Client(t)
	if runHTTP2Child(t, "TestH2CExtendedConnect") {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: h2EchoHandler(t, Options{}), Protocols: protocols}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	tr := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
	t.Cleanup(tr.CloseIdleConnections)

	c, err := Dial(context.Background(), "ws://"+ln.Addr().String()+"/ws", DialOptions{
		Transport:       tr,
		ExtendedConnect: ExtendedConnectHTTP2,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()

	if err := c.WriteMessage(TextMessage, []byte("h2c")); err != nil {
		t.Fatalf("write: %v", err)
	}
	op, p, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if op != TextMessage || string(p) != "h2c" {
		t.Fatalf("echo = %v %q", op, p)
	}
}
