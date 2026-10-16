package server

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/shibingli/yd/errx"
)

// Sentinel errors of the hijack tracking. | hijack 追踪的哨兵错误。
var (
	// ErrHijackUnsupported reports a hijack attempt on a writer that cannot hijack; HTTP/2 and HTTP/3 have no such notion. | ErrHijackUnsupported 表示在不支持 hijack 的写入器上尝试劫持；HTTP/2 与 HTTP/3 没有该语义。
	ErrHijackUnsupported = errx.New("server: response writer does not support hijacking | server：响应写入器不支持 hijack")

	// ErrForceCloseHijacked reports a hijacked connection that could not be force-closed during shutdown. | ErrForceCloseHijacked 表示退出时未能强制关闭已 hijack 的连接。
	ErrForceCloseHijacked = errx.New("server: failed to force-close a hijacked connection | server：强制关闭已 hijack 的连接失败")
)

// hijackWriter registers hijacked connections with the server so that Shutdown can force-close them. | hijackWriter 把被 hijack 的连接登记到服务器，使 Shutdown 能强制关闭它们。
//
// http.Server.Shutdown never closes a connection a handler has hijacked: the
// connection left the server's tracking the moment it was hijacked. Without
// this registry a WebSocket socket would simply survive the shutdown and keep
// the process's peer hanging. | http.Server.Shutdown 绝不会关闭已被处理器 hijack 的连接：
// 连接在 hijack 的那一刻就脱离了服务器的追踪。没有这个登记表，WebSocket 套接字会直接挺过退出流程，
// 让对端一直挂着。
//
// The wrapper is installed only when the underlying writer can hijack (that is,
// on HTTP/1.1), which keeps HTTP/2 and HTTP/3 behaviour — including
// ws.ErrHijackUnsupported — exactly as before. | 仅当底层写入器支持 hijack（即 HTTP/1.1）时才安装该包装，
// 因此 HTTP/2 与 HTTP/3 的行为（包括 ws.ErrHijackUnsupported）与之前完全一致。
type hijackWriter struct {
	http.ResponseWriter
	srv *Server
}

// Hijack implements http.Hijacker and registers the resulting connection. | Hijack 实现 http.Hijacker 并登记得到的连接。
func (w *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, ErrHijackUnsupported
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	return w.srv.trackHijacked(conn), brw, nil
}

// Flush implements http.Flusher so streaming responses keep flushing through the wrapper. | Flush 实现 http.Flusher，
// 保证流式响应经该包装后仍能 flush。
//
// It cannot be left to embedding: embedding the http.ResponseWriter interface
// promotes only the interface's own methods, so http.Flusher and http.Hijacker
// would silently disappear from the wrapper's method set (the hijack case is
// restored by the method above). | 不能依赖内嵌自动获得：
// 内嵌 http.ResponseWriter 接口只会提升该接口自身的方法，http.Flusher 与 http.Hijacker
// 会从包装的方法集中悄然消失（hijack 由上面的方法补回）。
func (w *hijackWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ReadFrom preserves the underlying writer's fast path for large response bodies. | ReadFrom 保留底层写入器处理大响应体时的快速路径。
//
// net/http's response writer implements io.ReaderFrom so that copying a file or
// a set of buffers can skip the generic copy loop (sendfile / writev). Wrapping
// hides that method — and because the wrapper is installed on **every** HTTP/1.1
// request, losing it would slow down every static asset response. It is restored
// here; when the underlying writer has no fast path, the anonymous struct around
// it exposes only Write, which keeps io.Copy from recursing back into this
// method. | net/http 的响应写入器实现了 io.ReaderFrom，使拷贝文件或一组缓冲区时
// 可以跳过通用拷贝循环（sendfile / writev）。包装会隐藏该方法——而由于该包装会装在
// **每一个** HTTP/1.1 请求上，丢失它会让所有静态资源响应都变慢。这里把它补回；
// 当底层写入器没有快速路径时，外面那层匿名结构体只暴露 Write，
// 从而避免 io.Copy 递归回本方法。
func (w *hijackWriter) ReadFrom(r io.Reader) (int64, error) {
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(r)
	}
	return io.Copy(struct{ io.Writer }{w.ResponseWriter}, r)
}

// Unwrap exposes the wrapped writer to http.ResponseController. | Unwrap 向 http.ResponseController 暴露被包装的写入器。
func (w *hijackWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// trackedConn is the net.Conn handed to a hijacking handler. | trackedConn 是交给 hijack 处理器的 net.Conn。
//
// Closing it — by the handler itself or by the server's forced close — removes
// the registration, so a long-running server never accumulates stale entries
// for connections that are already gone. | 关闭它（无论是处理器自己关闭还是服务器强制关闭）都会取消登记，
// 因此长期运行的服务器不会为已经消失的连接累积过期条目。
type trackedConn struct {
	net.Conn
	srv  *Server
	once sync.Once
}

// Close leaves the registry and closes the underlying connection. | Close 离开登记表并关闭底层连接。
func (c *trackedConn) Close() error {
	c.once.Do(func() { c.srv.untrackHijacked(c) })
	return c.Conn.Close()
}

// trackHijack wraps a handler so that connections it hijacks are registered. | trackHijack 包装处理器，登记它 hijack 出去的连接。
func (s *Server) trackHijack(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Hijacker); ok {
			w = &hijackWriter{ResponseWriter: w, srv: s}
		}
		h.ServeHTTP(w, r)
	})
}

// trackHijacked registers a hijacked connection and returns the wrapper the handler must use. | trackHijacked 登记被 hijack 的连接，
// 并返回处理器必须使用的包装。
func (s *Server) trackHijacked(conn net.Conn) net.Conn {
	tc := &trackedConn{Conn: conn, srv: s}

	s.mu.Lock()
	if s.hijacked == nil {
		s.hijacked = make(map[*trackedConn]struct{}, 4)
	}
	s.hijacked[tc] = struct{}{}
	s.mu.Unlock()

	return tc
}

// untrackHijacked removes one registration; it is idempotent. | untrackHijacked 移除一条登记；该方法是幂等的。
func (s *Server) untrackHijacked(tc *trackedConn) {
	s.mu.Lock()
	delete(s.hijacked, tc)
	s.mu.Unlock()
}

// closeHijacked force-closes every connection that is still hijacked. | closeHijacked 强制关闭所有仍处于 hijack 状态的连接。
//
// It walks a snapshot without holding the lock, so a connection whose Close
// blocks cannot stall the rest of the shutdown. | 它遍历快照且不持锁，
// 因此某条连接的 Close 阻塞不会拖住其余关闭工作。
func (s *Server) closeHijacked() []error {
	s.mu.Lock()
	conns := make([]*trackedConn, 0, len(s.hijacked))
	for tc := range s.hijacked {
		conns = append(conns, tc)
	}
	clear(s.hijacked)
	s.mu.Unlock()

	var errs []error
	for _, tc := range conns {
		// 地址先取出：关闭之后不保证还能读出对端信息。| Read the address first: peer information is not guaranteed to be readable after the close.
		remote := ""
		if addr := tc.Conn.RemoteAddr(); addr != nil {
			remote = addr.String()
		}
		if err := tc.Close(); err != nil {
			errs = append(errs, errx.WrapDetail(err, ErrForceCloseHijacked, "remote=%q", remote))
		}
	}
	return errs
}
