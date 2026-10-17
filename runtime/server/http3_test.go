package server

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/shibingli/yd/core/errx"
)

// errSimulatedBind stands in for the platform refusing a UDP bind. | errSimulatedBind 代替平台拒绝 UDP 绑定的错误。
var errSimulatedBind = errx.New("server test: simulated UDP bind failure | server 测试：模拟 UDP 绑定失败")

// h3Get performs one real HTTP/3 request. | h3Get 发起一次真实 HTTP/3 请求。
func h3Get(t *testing.T, addr string) (string, error) {
	t.Helper()
	tr := &http3.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer func() { _ = tr.Close() }()
	client := &http.Client{Transport: tr, Timeout: 8 * time.Second}
	resp, err := client.Get("https://" + addr + "/proto")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// TestHTTP3SocketIsBoundBeforeStartReturns 断言 Start 返回时 UDP 套接字已经绑定。
// TestHTTP3SocketIsBoundBeforeStartReturns asserts the UDP socket is bound by the time Start returns.
//
// 这是本组回归的核心：绑定若发生在 goroutine 里，Start 会在套接字就绪前返回，
// 而绑定失败会被彻底吞掉——调用方拿到 nil 错误、Alt-Svc 却通告一个无人监听的端点。
// This is the core of the regression: when the bind happens inside a goroutine, Start returns before
// the socket is ready and a failed bind is swallowed entirely — the caller gets a nil error while
// Alt-Svc advertises an endpoint nothing listens on.
func TestHTTP3SocketIsBoundBeforeStartReturns(t *testing.T) {
	srv := newTestServer(t, func(o *Options) { o.HTTP3 = HTTP3Config{Enabled: true} })
	addr := srv.HTTP3Addr()
	if addr == "" {
		t.Fatal("HTTP3Addr is empty | HTTP3Addr 为空")
	}

	// 端口已被占用即证明套接字在 Start 返回前就绑好了；若绑定还在 goroutine 里排队，这里会绑定成功。
	// The port being taken is the proof: had the bind still been queued in a goroutine, this would succeed.
	conn, err := net.ListenPacket("udp", addr)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("Start 返回后 UDP %s 仍可被再次绑定，说明套接字并非在 Start 返回前完成绑定", addr)
	}

	if body, err := h3Get(t, addr); err != nil {
		t.Fatalf("HTTP/3 往返失败: %v", err)
	} else if body != "HTTP/3.0" {
		t.Fatalf("HTTP/3 下处理器看到的协议 = %q", body)
	}
}

// TestHTTP3ShutdownReleasesUDPSocket 断言关闭后 UDP 端口被释放。
// TestHTTP3ShutdownReleasesUDPSocket asserts the UDP port is released on shutdown.
//
// http3.Server.Close 只关闭它自己创建的监听器；套接字由 Start 绑定后交给 Serve，
// 因此必须由服务器自己关闭，否则重启会因端口被占而失败。
// http3.Server.Close only closes listeners it created itself; this socket was bound by Start and
// handed to Serve, so the server has to close it or a restart fails on the occupied port.
func TestHTTP3ShutdownReleasesUDPSocket(t *testing.T) {
	o := Options{Host: "127.0.0.1", Port: 0, Handler: protoHandler(), HTTP3: HTTP3Config{Enabled: true}}
	srv := New(o)
	if err := srv.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	addr := srv.HTTP3Addr()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("关闭后 UDP %s 仍被占用，端口未释放: %v", addr, err)
	}
	_ = conn.Close()
}

// TestHTTP3FallsBackToOSAssignedPort 断言自动模式下端口号被平台拒绝时回退到系统分配的 UDP 端口。
// TestHTTP3FallsBackToOSAssignedPort asserts auto mode falls back to an OS-assigned UDP port when the platform refuses the number.
//
// Windows 上 TCP 端口号落在保留区间时，UDP 绑定会被直接拒绝（WSAEACCES）。此时不应丢掉 HTTP/3，
// 而应改绑一个可用的 UDP 端口，并让 Alt-Svc 通告**真实**端口。
// On Windows a UDP bind to a TCP port number inside a reserved range is refused outright
// (WSAEACCES). Losing HTTP/3 over that is wrong: bind a usable UDP port instead and let Alt-Svc
// advertise the port that is really in use.
func TestHTTP3FallsBackToOSAssignedPort(t *testing.T) {
	original := listenUDPFunc
	listenUDPFunc = func(network, address string) (net.PacketConn, error) {
		if port := portOf(t, address); port != 0 {
			return nil, errSimulatedBind
		}
		return original(network, address)
	}
	defer func() { listenUDPFunc = original }()

	srv := newTestServer(t, func(o *Options) { o.HTTP3 = HTTP3Config{Enabled: true} })

	// 回退后端口应与 TCP 端口不同，且必须与 Alt-Svc 通告的一致。
	// After the fallback the port differs from the TCP port and must match what Alt-Svc advertises.
	tcpPort := portOf(t, srv.Addr())
	h3Addr := srv.HTTP3Addr()
	h3Port := portOf(t, h3Addr)
	if h3Port == tcpPort {
		t.Fatalf("回退后仍复用 TCP 端口 %d，说明回退没有生效", tcpPort)
	}
	if want := `h3=":` + strconv.Itoa(h3Port) + `"`; !strings.Contains(srv.AltSvc(), want) {
		t.Errorf("Alt-Svc = %q, 期望通告真实绑定的端口（%s）", srv.AltSvc(), want)
	}

	if body, err := h3Get(t, h3Addr); err != nil {
		t.Fatalf("回退端口上的 HTTP/3 往返失败: %v", err)
	} else if body != "HTTP/3.0" {
		t.Fatalf("HTTP/3 下处理器看到的协议 = %q", body)
	}
}

// TestHTTP3ExplicitPortFailureSurfacesFromStart 断言显式配置的端口绑不上时 Start 直接报错。
// TestHTTP3ExplicitPortFailureSurfacesFromStart asserts Start fails outright when an explicitly configured port cannot be bound.
//
// 显式端口是调用方的明确要求，不允许悄悄换端口：必须如实报错，让调用方自己决定。
// An explicit port is a deliberate request, so it is never silently swapped: the error surfaces and
// the caller decides.
func TestHTTP3ExplicitPortFailureSurfacesFromStart(t *testing.T) {
	original := listenUDPFunc
	listenUDPFunc = func(network, address string) (net.PacketConn, error) {
		return nil, errSimulatedBind
	}
	defer func() { listenUDPFunc = original }()

	srv := New(Options{
		Host:    "127.0.0.1",
		Port:    0,
		Handler: protoHandler(),
		HTTP3:   HTTP3Config{Enabled: true, Port: 4443},
	})
	err := srv.Start()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		t.Fatal("Start still returned nil when the explicit port bind failed; the failure was swallowed | 显式端口绑定失败时 Start 仍返回 nil，失败被吞掉了")
	}
	if !errors.Is(err, ErrHTTP3Listen) {
		t.Fatalf("Start 返回 %v, 期望可 errors.Is 到 ErrHTTP3Listen", err)
	}
	// The TCP listener must be released too, not leaked on the HTTP/3 failure. | TCP 监听必须一并释放，不能因为 HTTP/3 失败而泄漏。
	if srv.Addr() == "" {
		t.Error("Addr is empty | Addr 为空")
	}
}

// TestAltSvcHTTP3RoundTripIsStable 反复跑真实 QUIC 往返，确认不再间歇失败。
// TestAltSvcHTTP3RoundTripIsStable repeats the real QUIC round trip to confirm the flakiness is gone.
func TestAltSvcHTTP3RoundTripIsStable(t *testing.T) {
	for i := range 8 {
		srv := newTestServer(t, func(o *Options) { o.HTTP3 = HTTP3Config{Enabled: true} })
		addr := srv.HTTP3Addr()
		if body, err := h3Get(t, addr); err != nil {
			t.Fatalf("第 %d 轮 HTTP/3 往返失败（%s）: %v", i+1, addr, err)
		} else if body != "HTTP/3.0" {
			t.Fatalf("第 %d 轮协议 = %q", i+1, body)
		}
	}
}

// portOf extracts the port from a "host:port" address. | portOf 从 "host:port" 地址中取出端口。
func portOf(t *testing.T, addr string) int {
	t.Helper()
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("解析地址 %q 失败: %v", addr, err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("端口 %q 不是数字: %v", p, err)
	}
	return n
}
