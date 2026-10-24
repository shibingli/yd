package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"

	"github.com/shibingli/ydx/realtime"
	"github.com/shibingli/ydx/realtime/adapter"
	"github.com/shibingli/ydx/sse"
	"github.com/shibingli/ydx/websocket"
	"github.com/shibingli/ydx/webtransport"
)

// realtimeMux builds the handler shared by every transport in the matrix. | realtimeMux
// 构造矩阵中所有传输共用的处理器。
func realtimeMux(srv **Server, hub *realtime.Hub) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/proto", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Upgrade(w, r, websocket.Options{})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		if topic := r.URL.Query().Get("topic"); topic != "" {
			sub, err := hub.Subscribe(r.Context(), topic, adapter.WebSocket(conn))
			if err != nil {
				return
			}
			defer sub.Close()
			// Drain inbound frames until the peer leaves; broadcasts go out through
			// the subscription. | 排空入站帧直到对端离开；广播经订阅写出。
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}
		for {
			op, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(op, append([]byte("echo:"), msg...)); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("/wt", func(w http.ResponseWriter, r *http.Request) {
		sess, err := (*srv).WebTransport().Upgrade(w, r)
		if err != nil {
			return
		}
		ch, err := sess.Accept(r.Context())
		if err != nil {
			return
		}
		defer ch.Close()
		if topic := r.URL.Query().Get("topic"); topic != "" {
			sub, err := hub.Subscribe(r.Context(), topic, adapter.WebTransport(ch))
			if err != nil {
				return
			}
			defer sub.Close()
			for {
				if _, err := ch.Receive(); err != nil {
					return
				}
			}
		}
		for {
			msg, err := ch.Receive()
			if err != nil {
				return
			}
			if err := ch.Send(append([]byte("echo:"), msg...)); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		sw, err := sse.NewWriter(w, sse.Options{})
		if err != nil {
			return
		}
		defer sw.Close()
		if topic := r.URL.Query().Get("topic"); topic != "" {
			sub, err := hub.Subscribe(r.Context(), topic, adapter.SSE(sw))
			if err != nil {
				return
			}
			defer sub.Close()
			<-r.Context().Done()
			return
		}
		if err := sw.WriteData("sse-ok"); err != nil {
			return
		}
		<-r.Context().Done()
	})
	return mux
}

// startRealtime starts one TLS server exposing WebSocket (H1 + H3),
// WebTransport, SSE and an ordinary HTTP/3 endpoint on the same port pair. |
// startRealtime 启动一个 TLS 服务器，在同一对端口上同时提供 WebSocket（H1 + H3）、
// WebTransport、SSE 与普通 HTTP/3 端点。
func startRealtime(t *testing.T) (*Server, *realtime.Hub) {
	t.Helper()
	var srv *Server
	hub := realtime.NewHub(realtime.Options{NodeID: "matrix-node"})
	t.Cleanup(func() { _ = hub.Close() })
	handler := realtimeMux(&srv, hub)
	srv = New(Options{
		Host:    "127.0.0.1",
		Port:    0,
		Handler: handler,
		HTTP3:   HTTP3Config{Enabled: true, WebTransport: true},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv, hub
}

// insecureTLS is the client TLS configuration for the ephemeral dev certificate. |
// insecureTLS 是临时开发证书对应的客户端 TLS 配置。
func insecureTLS() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true}
}

// wsEcho dials one WebSocket endpoint and asserts an echo round trip. | wsEcho
// 对一个 WebSocket 端点拨号并断言回显往返。
func wsEcho(t *testing.T, url string, o websocket.DialOptions, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := websocket.Dial(ctx, url, o)
	if err != nil {
		t.Fatalf("ws dial %s: %v", url, err)
	}
	defer func() { _ = conn.CloseNow() }()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(want)); err != nil {
		t.Fatalf("ws write: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if string(msg) != "echo:"+want {
		t.Fatalf("ws echo = %q, want %q", msg, "echo:"+want)
	}
}

// TestRealtimeTransportMatrix proves one server serves every supported
// transport and that they all round-trip the same payload. | TestRealtimeTransportMatrix
// 证明同一个服务器服务全部受支持的传输，且它们都能往返同一载荷。
func TestRealtimeTransportMatrix(t *testing.T) {
	srv, _ := startRealtime(t)
	tcp, h3 := srv.Addr(), srv.HTTP3Addr()
	if h3 == "" {
		t.Fatal("HTTP/3 address is empty | HTTP/3 地址为空")
	}

	t.Run("websocket-h1 | WebSocket HTTP/1.1", func(t *testing.T) {
		wsEcho(t, "wss://"+tcp+"/ws", websocket.DialOptions{TLSConfig: insecureTLS()}, "h1")
	})
	t.Run("websocket-h3 | WebSocket HTTP/3", func(t *testing.T) {
		wsEcho(t, "wss://"+h3+"/ws", websocket.DialOptions{
			TLSConfig:       insecureTLS(),
			Transport:       &http3.Transport{TLSClientConfig: insecureTLS()},
			ExtendedConnect: websocket.ExtendedConnectHTTP3,
		}, "h3")
	})
	t.Run("webtransport | WebTransport", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sess, err := webtransport.Dial(ctx, "https://"+h3+"/wt", webtransport.DialOptions{TLSClientConfig: insecureTLS()})
		if err != nil {
			t.Fatalf("webtransport dial: %v", err)
		}
		defer func() { _ = sess.Close(0, "test done | 测试结束") }()
		ch, err := sess.Open(ctx)
		if err != nil {
			t.Fatalf("webtransport open: %v", err)
		}
		defer ch.Close()
		if err := ch.Send([]byte("wt")); err != nil {
			t.Fatalf("webtransport send: %v", err)
		}
		msg, err := ch.Receive()
		if err != nil {
			t.Fatalf("webtransport receive: %v", err)
		}
		if string(msg) != "echo:wt" {
			t.Fatalf("webtransport echo = %q", msg)
		}
	})
	t.Run("sse | SSE", func(t *testing.T) {
		tr := &http.Transport{TLSClientConfig: insecureTLS()}
		client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
		resp, err := client.Get("https://" + tcp + "/sse")
		if err != nil {
			t.Fatalf("sse get: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		line, err := bufio.NewReader(resp.Body).ReadString('\n')
		if err != nil {
			t.Fatalf("sse read: %v", err)
		}
		if !strings.HasPrefix(line, "data: sse-ok") {
			t.Fatalf("sse frame = %q", line)
		}
	})
	t.Run("http3-get | HTTP/3 GET", func(t *testing.T) {
		if body, err := h3Get(t, h3); err != nil {
			t.Fatalf("h3 get: %v", err)
		} else if body != "HTTP/3.0" {
			t.Fatalf("h3 handler saw %q", body)
		}
	})
	t.Run("origin-rejected | Origin 被拒", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := websocket.Dial(ctx, "wss://"+tcp+"/ws", websocket.DialOptions{
			TLSConfig: insecureTLS(),
			Headers:   http.Header{"Origin": {"https://evil.example"}},
		})
		if err == nil {
			t.Fatal("cross-origin WebSocket handshake succeeded | 跨源 WebSocket 握手成功")
		}
	})
}

// TestRealtimeShutdownClosesEveryTransport proves graceful shutdown leaves no
// transport behind: WebSocket over H1 and H3, WebTransport and SSE all end. |
// TestRealtimeShutdownClosesEveryTransport 证明优雅退出不会遗留任何传输：
// HTTP/1.1 与 HTTP/3 上的 WebSocket、WebTransport 与 SSE 全部结束。
func TestRealtimeShutdownClosesEveryTransport(t *testing.T) {
	srv, _ := startRealtime(t)
	tcp, h3 := srv.Addr(), srv.HTTP3Addr()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h1, err := websocket.Dial(ctx, "wss://"+tcp+"/ws", websocket.DialOptions{TLSConfig: insecureTLS()})
	if err != nil {
		t.Fatalf("h1 dial: %v", err)
	}
	defer func() { _ = h1.CloseNow() }()
	h3c, err := websocket.Dial(ctx, "wss://"+h3+"/ws", websocket.DialOptions{
		TLSConfig:       insecureTLS(),
		Transport:       &http3.Transport{TLSClientConfig: insecureTLS()},
		ExtendedConnect: websocket.ExtendedConnectHTTP3,
	})
	if err != nil {
		t.Fatalf("h3 dial: %v", err)
	}
	defer func() { _ = h3c.CloseNow() }()
	wt, err := webtransport.Dial(ctx, "https://"+h3+"/wt", webtransport.DialOptions{TLSClientConfig: insecureTLS()})
	if err != nil {
		t.Fatalf("wt dial: %v", err)
	}
	defer func() { _ = wt.Close(0, "test done | 测试结束") }()
	if _, err := wt.Open(ctx); err != nil {
		t.Fatalf("wt open: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Every transport must observe the end; reads return an error instead of blocking. |
	// 每种传输都必须观察到结束；读取应返回错误而不是继续阻塞。
	_ = h1.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := h1.ReadMessage(); err == nil {
		t.Fatal("HTTP/1.1 WebSocket survived shutdown | HTTP/1.1 WebSocket 在关闭后仍存活")
	}
	_ = h3c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := h3c.ReadMessage(); err == nil {
		t.Fatal("HTTP/3 WebSocket survived shutdown | HTTP/3 WebSocket 在关闭后仍存活")
	}
	select {
	case <-wt.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("WebTransport session survived shutdown | WebTransport 会话在关闭后仍存活")
	}
}

// TestRealtimeHubFanOutAcrossTransports proves single-node fan-out reaches
// WebSocket over HTTP/1.1 and HTTP/3, WebTransport and SSE through the same
// realtime hub and the same topic. | TestRealtimeHubFanOutAcrossTransports 证明
// 单机扇出通过同一个 realtime hub 与同一个主题同时到达 HTTP/1.1/HTTP/3 上的
// WebSocket、WebTransport 与 SSE。
func TestRealtimeHubFanOutAcrossTransports(t *testing.T) {
	srv, hub := startRealtime(t)
	tcp, h3 := srv.Addr(), srv.HTTP3Addr()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h1, err := websocket.Dial(ctx, "wss://"+tcp+"/ws?topic=matrix", websocket.DialOptions{TLSConfig: insecureTLS()})
	if err != nil {
		t.Fatalf("h1 dial: %v", err)
	}
	defer func() { _ = h1.CloseNow() }()
	h3c, err := websocket.Dial(ctx, "wss://"+h3+"/ws?topic=matrix", websocket.DialOptions{
		TLSConfig:       insecureTLS(),
		Transport:       &http3.Transport{TLSClientConfig: insecureTLS()},
		ExtendedConnect: websocket.ExtendedConnectHTTP3,
	})
	if err != nil {
		t.Fatalf("h3 dial: %v", err)
	}
	defer func() { _ = h3c.CloseNow() }()
	wt, err := webtransport.Dial(ctx, "https://"+h3+"/wt?topic=matrix", webtransport.DialOptions{TLSClientConfig: insecureTLS()})
	if err != nil {
		t.Fatalf("wt dial: %v", err)
	}
	defer func() { _ = wt.Close(0, "test done | 测试结束") }()
	wtCh, err := wt.Open(ctx)
	if err != nil {
		t.Fatalf("wt open: %v", err)
	}
	defer wtCh.Close()

	type sseResult struct {
		resp *http.Response
		err  error
	}
	sseCh := make(chan sseResult, 1)
	go func() {
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: insecureTLS()}, Timeout: 10 * time.Second}
		resp, err := client.Get("https://" + tcp + "/sse?topic=matrix")
		sseCh <- sseResult{resp: resp, err: err}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for hub.SubscriberCount("matrix") < 4 && time.Now().Before(deadline) {
		select {
		case res := <-sseCh:
			if res.err != nil {
				t.Fatalf("sse get early: %v", res.err)
			}
			t.Logf("sse response started early: status=%d", res.resp.StatusCode)
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := hub.SubscriberCount("matrix"); got != 4 {
		select {
		case res := <-sseCh:
			t.Fatalf("subscribers = %d, want 4; sse early err=%v status=%v", got, res.err, res.resp)
		default:
			t.Fatalf("subscribers = %d, want 4; sse request not started", got)
		}
	}

	if err := hub.Publish(ctx, "matrix", []byte("fan")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for name, conn := range map[string]*websocket.Conn{"h1": h1, "h3": h3c} {
		if _, msg, err := conn.ReadMessage(); err != nil {
			t.Fatalf("%s read: %v", name, err)
		} else if string(msg) != "fan" {
			t.Fatalf("%s got %q, want fan", name, msg)
		}
	}
	if msg, err := wtCh.Receive(); err != nil {
		t.Fatalf("wt receive: %v", err)
	} else if string(msg) != "fan" {
		t.Fatalf("wt got %q, want fan", msg)
	}

	var res sseResult
	select {
	case res = <-sseCh:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE response did not start | SSE 响应未开始")
	}
	if res.err != nil {
		t.Fatalf("sse get: %v", res.err)
	}
	defer func() { _ = res.resp.Body.Close() }()
	reader := bufio.NewReader(res.resp.Body)
	var idLine, dataLine string
	for i := 0; i < 4 && (idLine == "" || dataLine == ""); i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("sse read: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "id: ") {
			idLine = line
		}
		if strings.HasPrefix(line, "data: ") {
			dataLine = line
		}
	}
	if dataLine != "data: fan" || idLine == "" {
		t.Fatalf("SSE frame id=%q data=%q", idLine, dataLine)
	}

	// Exactly once on the WebSocket transports: the next read must time out. |
	// WebSocket 传输上恰好一次：下一次读取必须超时。
	for name, conn := range map[string]*websocket.Conn{"h1": h1, "h3": h3c} {
		_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		if _, msg, err := conn.ReadMessage(); err == nil {
			t.Fatalf("%s received a duplicate %q", name, msg)
		}
	}
}
