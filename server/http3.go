package server

import (
	"net"
	"strconv"

	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/logx"
)

// listenUDPFunc binds a UDP socket from a "host:port" address; it is a variable so tests can drive
// the failure paths of bindHTTP3 without depending on the host's port reservations. | listenUDPFunc
// 按 "host:port" 地址绑定 UDP 套接字；之所以是变量，是为了让测试能驱动 bindHTTP3 的失败分支，
// 而不必依赖宿主机的端口保留策略。
var listenUDPFunc = func(network, address string) (net.PacketConn, error) {
	return net.ListenPacket(network, address)
}

// bindHTTP3 binds the QUIC socket and reports the port that was really bound. | bindHTTP3 绑定 QUIC 套接字，
// 并返回真正绑定到的端口。
//
// The bind is deliberately synchronous, and the caller runs it before Start returns. Letting
// http3.Server.ListenAndServe do it inside a goroutine hides every failure: Start would return nil,
// HTTP3Addr would report a healthy address, Alt-Svc would advertise an endpoint nothing listens on,
// and the first symptom would be clients timing out with "no recent network activity" — exactly the
// silent failure this function exists to prevent. | 绑定刻意做成同步的，并由调用方在 Start 返回前完成。
// 若交给 http3.Server.ListenAndServe 在 goroutine 里绑定，一切失败都会被吞掉：Start 返回 nil、
// HTTP3Addr 报出一个看似健康的地址、Alt-Svc 通告一个无人监听的端点，而第一个症状是客户端以
// "no recent network activity" 超时——正是本函数要杜绝的静默失败。
//
// Auto mode (HTTP3.Port == 0) reuses the TCP port number, which Windows may refuse outright when
// that number falls inside a reserved range (bind: "An attempt was made to access a socket in a way
// forbidden by its access permissions"). Rather than losing HTTP/3 over it, auto mode falls back to
// an OS-assigned UDP port; the advertised Alt-Svc port always comes from the socket that was bound,
// so clients keep reaching it. An explicitly configured port is honoured strictly: if it cannot be
// bound, the error surfaces. | 自动模式（HTTP3.Port == 0）复用 TCP 的端口号，而当该端口号落在
// 保留区间内时 Windows 会直接拒绝（bind: "An attempt was made to access a socket in a way forbidden
// by its access permissions"）。为避免因此丢掉 HTTP/3，自动模式回退到操作系统分配的 UDP 端口；
// Alt-Svc 通告的端口始终取自真正绑定的套接字，因此客户端依然能连上。显式配置的端口则严格遵守：
// 绑不上就如实报错。
func (s *Server) bindHTTP3() (net.PacketConn, int, error) {
	explicit := s.opts.HTTP3.Port
	port := explicit
	if port == 0 {
		port = s.tcpPort
	}

	conn, err := listenUDPFunc("udp", s.opts.h3Addr(port))
	if err != nil {
		if explicit != 0 {
			return nil, 0, errx.WrapDetail(err, ErrHTTP3Listen, "port %d | 端口 %d", port, port)
		}
		// Auto mode: fall back to an OS-assigned UDP port. | 自动模式：退回让操作系统分配 UDP 端口。
		fallback, ferr := listenUDPFunc("udp", s.opts.h3Addr(0))
		if ferr != nil {
			return nil, 0, errx.WrapDetail(ferr, ErrHTTP3Listen, "tcp port %d | TCP 端口 %d", port, port)
		}
		bound := fallback.LocalAddr().(*net.UDPAddr).Port
		s.logger.Warn("HTTP/3 could not reuse the TCP port, falling back to an OS-assigned UDP port | HTTP/3 无法复用 TCP 端口，回退到操作系统分配的 UDP 端口",
			logx.Int("tcp_port", port),
			logx.Int("udp_port", bound),
			logx.Str("reason", err.Error()),
		)
		return fallback, bound, nil
	}
	return conn, conn.LocalAddr().(*net.UDPAddr).Port, nil
}

// h3PublicAddr renders the client-facing HTTP/3 address. | h3PublicAddr 渲染面向客户端的 HTTP/3 地址。
func (s *Server) h3PublicAddr() string {
	return net.JoinHostPort(s.opts.Host, strconv.Itoa(s.h3Port))
}
