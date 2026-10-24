package server

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/shibingli/ydx/webtransport"
)

// insecureWT starts a server with HTTP/3 and WebTransport on the same UDP
// socket. Its handler owns /wt sessions: it accepts the framed channel, echoes
// every message and blocks until the session ends. | insecureWT 启动一个在同一个
// UDP 套接字上同时提供 HTTP/3 与 WebTransport 的服务器。处理器拥有 /wt 会话：
// 接受带帧通道、回显每条消息并阻塞到会话结束。
func insecureWT(t *testing.T) *Server {
	t.Helper()
	var srv *Server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wt" {
			_, _ = w.Write([]byte(r.Proto))
			return
		}
		sess, err := srv.WebTransport().Upgrade(w, r)
		if err != nil {
			return
		}
		ch, err := sess.Accept(r.Context())
		if err != nil {
			return
		}
		defer ch.Close()
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
	return srv
}

// dialWT opens one WebTransport session and channel against srv. | dialWT 对 srv 打开
// 一个 WebTransport 会话与通道。
func dialWT(t *testing.T, srv *Server) (*webtransport.Session, *webtransport.Channel) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := "https://" + srv.HTTP3Addr() + "/wt"
	sess, err := webtransport.Dial(ctx, url, webtransport.DialOptions{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close(0, "test done | 测试结束") })
	ch, err := sess.Open(ctx)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return sess, ch
}

// TestWebTransportSharesHTTP3Listener proves one UDP socket serves ordinary
// HTTP/3 requests and WebTransport sessions at the same time. |
// TestWebTransportSharesHTTP3Listener 证明同一个 UDP 套接字同时服务普通 HTTP/3 请求与
// WebTransport 会话。
func TestWebTransportSharesHTTP3Listener(t *testing.T) {
	srv := insecureWT(t)
	if srv.WebTransport() == nil {
		t.Fatal("WebTransport() is nil with server.http3.webtransport on | 开启后 WebTransport() 仍为 nil")
	}
	// Ordinary HTTP/3 still reaches the handler. | 普通 HTTP/3 仍能到达处理器。
	if body, err := h3Get(t, srv.HTTP3Addr()); err != nil {
		t.Fatalf("HTTP/3 GET: %v", err)
	} else if body != "HTTP/3.0" {
		t.Fatalf("HTTP/3 handler saw %q", body)
	}

	_, ch := dialWT(t, srv)
	defer ch.Close()
	if err := ch.Send([]byte("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := ch.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(got) != "echo:hello" {
		t.Fatalf("echo = %q, want echo:hello", got)
	}
}

// TestWebTransportRequiresHTTP3 proves the configuration fails closed. |
// TestWebTransportRequiresHTTP3 证明配置会失败关闭。
func TestWebTransportRequiresHTTP3(t *testing.T) {
	srv := New(Options{Host: "127.0.0.1", Port: 0, Handler: protoHandler(), HTTP3: HTTP3Config{WebTransport: true}})
	if err := srv.Start(); !errors.Is(err, ErrWebTransportRequiresHTTP3) {
		t.Fatalf("Start error = %v, want ErrWebTransportRequiresHTTP3", err)
	}
}

// TestWebTransportShutdownEndsSessions proves graceful shutdown terminates live
// WebTransport sessions before the QUIC listener goes away. |
// TestWebTransportShutdownEndsSessions 证明优雅退出会在 QUIC 监听消失前终止活跃的
// WebTransport 会话。
func TestWebTransportShutdownEndsSessions(t *testing.T) {
	srv := insecureWT(t)
	sess, ch := dialWT(t, srv)
	defer ch.Close()

	if err := ch.Send([]byte("ping")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := ch.Receive(); err != nil {
		t.Fatalf("Receive: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case <-sess.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session survived shutdown | 会话在关闭后仍存活")
	}
}

// TestWebTransportOffByDefault proves the feature stays off unless configured. |
// TestWebTransportOffByDefault 证明未配置时该特性保持关闭。
func TestWebTransportOffByDefault(t *testing.T) {
	srv := newTestServer(t, func(o *Options) { o.HTTP3 = HTTP3Config{Enabled: true} })
	if srv.WebTransport() != nil {
		t.Fatal("WebTransport() should be nil without server.http3.webtransport | 未开启时 WebTransport() 应为 nil")
	}
}
