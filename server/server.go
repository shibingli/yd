package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"

	"github.com/quic-go/quic-go/http3"

	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/lifecycle"
	"github.com/shibingli/yd/logx"
)

// Sentinel errors of this package, all testable with errors.Is. | 本包的哨兵错误，均可通过 errors.Is 判定。
var (
	// ErrNoHandler is returned when Start is called without a handler. | ErrNoHandler 表示未提供处理器就调用 Start。
	ErrNoHandler = errx.New("server: no handler configured | server：未设置 Handler")

	// ErrAlreadyStarted is returned by a second Start call. | ErrAlreadyStarted 表示重复调用 Start。
	ErrAlreadyStarted = errx.New("server: server already started | server：服务器已启动")

	// ErrListen reports a TCP listener that could not be bound. | ErrListen 表示 TCP 监听绑定失败。
	ErrListen = errx.New("server: failed to listen | server：监听失败")

	// ErrHTTP3RequiresTLS reports HTTP/3 enabled without any TLS configuration (QUIC mandates TLS 1.3). | ErrHTTP3RequiresTLS 表示启用 HTTP/3 却没有 TLS 配置（QUIC 强制 TLS 1.3）。
	ErrHTTP3RequiresTLS = errx.New("server: HTTP/3 requires a TLS configuration (QUIC mandates TLS 1.3) | server：启用 HTTP/3 需要 TLS 配置（QUIC 强制 TLS 1.3）")

	// ErrHTTPServerExited reports the TCP (HTTP/1.1 + h2c) server stopping unexpectedly. | ErrHTTPServerExited 表示 TCP（HTTP/1.1 + h2c）服务异常退出。
	ErrHTTPServerExited = errx.New("server: HTTP server exited | server：HTTP 服务退出")

	// ErrHTTP3ServerExited reports the HTTP/3 server stopping unexpectedly. | ErrHTTP3ServerExited 表示 HTTP/3 服务异常退出。
	ErrHTTP3ServerExited = errx.New("server: HTTP/3 server exited | server：HTTP/3 服务退出")

	// ErrTLSFilesMissing reports TLS enabled without cert_file and key_file. | ErrTLSFilesMissing 表示启用 TLS 却缺少 cert_file 与 key_file。
	ErrTLSFilesMissing = errx.New("server: enabling TLS requires cert_file and key_file | server：启用 TLS 必须提供 cert_file 与 key_file")

	// ErrCertLoadFailed reports a certificate pair that could not be loaded. | ErrCertLoadFailed 表示证书对加载失败。
	ErrCertLoadFailed = errx.New("server: failed to load the certificate | server：加载证书失败")

	// ErrGracefulShutdown reports that in-flight requests did not drain within the grace period. | ErrGracefulShutdown 表示宽限期内仍有进行中的请求未排空。
	ErrGracefulShutdown = errx.New("server: graceful shutdown did not finish before the deadline | server：宽限期内未能完成优雅退出")

	// ErrShutdownHook reports a failure in a registered lifecycle shutdown hook. | ErrShutdownHook 表示已注册的生命周期关闭钩子执行失败。
	ErrShutdownHook = errx.New("server: shutdown hook failed | server：关闭钩子执行失败")
)

// Server multiplexes HTTP/1.1, h2c and HTTP/3 over one handler. | Server 在同一处理器上复用 HTTP/1.1、h2c 与 HTTP/3。
//
// HTTP/1.1 and h2c share the TCP listener (Go 1.24+ supports cleartext HTTP/2
// natively through http.Protocols, so no external h2c package is needed).
// HTTP/3 runs on a QUIC (UDP) listener and is advertised through the Alt-Svc
// header. | HTTP/1.1 与 h2c 共用 TCP 监听（Go 1.24+ 通过 http.Protocols 原生支持明文
// HTTP/2，无需外部 h2c 包）；HTTP/3 运行在 QUIC（UDP）监听上，并通过 Alt-Svc 头对外通告。
type Server struct {
	ln        net.Listener
	logger    *logx.Logger
	httpSrv   *http.Server
	h3Srv     *http3.Server
	acme      *acmeManager
	lifecycle *lifecycle.Registry

	serveErr chan error
	done     chan struct{}

	// hijacked holds every connection a handler took over with http.Hijacker;
	// http.Server.Shutdown does not know about them, so the server must close
	// them itself. | hijacked 保存处理器通过 http.Hijacker 接管的全部连接；
	// http.Server.Shutdown 对它们一无所知，因此服务器必须自行关闭。
	hijacked map[*trackedConn]struct{}

	altSvc string

	opts Options

	tcpPort int
	h3Port  int

	// mu guards the mutable state above: started, the listeners and hijacked. | mu 保护上述可变状态：started、监听器与 hijacked。
	mu      sync.Mutex
	started bool
}

// New creates a Server from options. | New 依据参数创建服务器。
func New(o Options) *Server {
	o = o.normalize()
	l := o.Logger
	if l == nil {
		l = logx.Default()
	}
	return &Server{
		opts:      o,
		logger:    l,
		serveErr:  make(chan error, 4),
		done:      make(chan struct{}),
		lifecycle: lifecycle.NewRegistry(),
		hijacked:  make(map[*trackedConn]struct{}, 4),
	}
}

// Options returns the normalized options. | Options 返回规范化后的参数。
func (s *Server) Options() Options { return s.opts }

// Lifecycle returns the shutdown registry that runs before the server drains. | Lifecycle 返回在服务器排空之前运行的关闭注册表。
//
// Long-lived endpoints register their teardown here (for example a ws.ConnSet
// closing every WebSocket with 1001, or an sse.WriterSet terminating every
// event stream): the registry is closed first so those connections start
// winding down while ordinary requests are still being drained, and their
// errors are aggregated into Shutdown's result. | 长连接端点在此登记拆除逻辑
// （例如用 1001 关闭全部 WebSocket 的 ws.ConnSet，或终止全部事件流的 sse.WriterSet）：
// 注册表最先被关闭，使长连接在普通请求仍在排空时就开始收尾，其错误也会聚合进 Shutdown 的结果。
func (s *Server) Lifecycle() *lifecycle.Registry { return s.lifecycle }

// Addr returns the bound TCP address (valid after Start). | Addr 返回已绑定的 TCP 地址（Start 之后有效）。
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return s.opts.addr()
	}
	return s.ln.Addr().String()
}

// HTTP3Addr returns the bound UDP address; empty when HTTP/3 is off. | HTTP3Addr 返回已绑定的 UDP 地址，未启用 HTTP/3 时为空。
func (s *Server) HTTP3Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.h3Port == 0 {
		return ""
	}
	return net.JoinHostPort(s.opts.Host, strconv.Itoa(s.h3Port))
}

// AltSvc returns the Alt-Svc header value; empty when HTTP/3 is off. | AltSvc 返回 Alt-Svc 头取值，未启用 HTTP/3 时为空。
func (s *Server) AltSvc() string { return s.altSvc }

// Start binds the listeners and serves in background goroutines. | Start 绑定监听并在后台协程中提供服务。
//
// It returns as soon as the listeners are bound, so binding errors surface
// synchronously instead of being hidden in a goroutine. | 监听绑定完成即返回，
// 因此绑定错误会同步暴露，而不会隐藏在协程里。
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrAlreadyStarted
	}
	if s.opts.Handler == nil {
		return ErrNoHandler
	}

	tlsConf, err := s.buildTLSConfig()
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", s.opts.addr())
	if err != nil {
		return errx.WrapDetail(err, ErrListen, "addr %s | 地址 %s", s.opts.addr(), s.opts.addr())
	}
	s.ln = ln
	s.tcpPort = ln.Addr().(*net.TCPAddr).Port

	// HTTP/3 必须在构建处理器**之前**确定：Alt-Svc 通告需要注入处理器链，
	// 而通告内容依赖最终绑定的 UDP 端口。
	if s.opts.HTTP3.Enabled {
		port := s.opts.HTTP3.Port
		if port == 0 {
			port = s.tcpPort
		}
		s.h3Port = port
		if tlsConf == nil {
			_ = ln.Close()
			return ErrHTTP3RequiresTLS
		}
		// QUIC 需要独立的 ALPN 集合，必须克隆后修改，避免影响 TCP 侧的协商
		h3TLS := tlsConf.Clone()
		h3TLS.NextProtos = []string{"h3"}
		s.h3Srv = &http3.Server{
			Addr:      s.opts.h3Addr(s.tcpPort),
			Port:      port,
			TLSConfig: h3TLS,
			Handler:   nil, // 在处理器构建完成后回填
		}
		s.altSvc = fmt.Sprintf(`h3=":%d"; ma=86400`, port)
	}

	// 最外层登记 hijack 连接：http.Server.Shutdown 不会关闭它们，只有服务器自己知道它们的存在。| Outermost layer registers hijacked connections: http.Server.Shutdown never closes them, and only the server knows about them.
	handler := s.trackHijack(s.buildHandler())
	if s.h3Srv != nil {
		s.h3Srv.Handler = handler
	}

	s.httpSrv = &http.Server{
		Handler:           handler,
		ReadTimeout:       s.opts.ReadTimeout,
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		WriteTimeout:      s.opts.WriteTimeout,
		IdleTimeout:       s.opts.IdleTimeout,
		ErrorLog:          nil,
	}
	if c := s.loggerConfig(); c != nil {
		s.httpSrv.ErrorLog = c
	}
	if s.opts.H2C && !s.tlsActive() {
		// 明文 HTTP/2：Go 1.24+ 内建支持，无需 golang.org/x/net/http2/h2c
		p := new(http.Protocols)
		p.SetHTTP1(true)
		p.SetUnencryptedHTTP2(true)
		s.httpSrv.Protocols = p
	}
	if tlsConf != nil {
		s.httpSrv.TLSConfig = tlsConf
	}

	s.started = true

	// TCP 侧
	go func() {
		var err error
		if tlsConf != nil {
			err = s.httpSrv.ServeTLS(ln, "", "")
		} else {
			err = s.httpSrv.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.reportErr(errx.Wrap(err, ErrHTTPServerExited))
		}
	}()

	// UDP 侧
	if s.h3Srv != nil {
		go func() {
			if err := s.h3Srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.reportErr(errx.Wrap(err, ErrHTTP3ServerExited))
			}
		}()
	}

	// 注意：此处仍持有 s.mu，不能调用会再次加锁的 Addr()/HTTP3Addr()
	h3Addr := ""
	if s.h3Port != 0 {
		h3Addr = net.JoinHostPort(s.opts.Host, strconv.Itoa(s.h3Port))
	}
	s.logger.Info("server started | 服务器已启动",
		logx.Str("addr", ln.Addr().String()),
		logx.Str("http3", h3Addr),
		logx.Bool("h2c", s.opts.H2C && !s.tlsActive()),
		logx.Bool("tls", s.tlsActive()),
	)
	return nil
}

// reportErr pushes a fatal serve error to the error channel. | reportErr 把致命的服务错误推入错误通道。
func (s *Server) reportErr(err error) {
	select {
	case s.serveErr <- err:
	default:
		s.logger.Error("server error channel is full | 服务器错误通道已满", logx.Err(err))
	}
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

// Errors exposes fatal serve errors. | Errors 暴露致命的服务错误。
func (s *Server) Errors() <-chan error { return s.serveErr }

// Done is closed when a fatal serve error occurs. | Done 在出现致命服务错误时关闭。
func (s *Server) Done() <-chan struct{} { return s.done }

// Shutdown gracefully stops both listeners in five ordered steps. | Shutdown 按五个有序步骤优雅停止两个监听。
//
// The order matters, because http.Server.Shutdown only knows about ordinary
// requests: it neither closes hijacked connections (WebSocket) nor interrupts a
// response that is still streaming (SSE), so those two would otherwise hold the
// process until it is killed. | 顺序很重要：http.Server.Shutdown 只认识普通请求——
// 它既不关闭被 hijack 的连接（WebSocket），也不打断仍在流式输出的响应（SSE），
// 因此这两类连接会把进程一直拖到被外部强杀。 Steps | 步骤：
//
//  1. the lifecycle registry is closed: Done() closes immediately so long-lived
//     connections start winding down, and its hooks run in parallel with step 3
//     under the same grace period; | 关闭 lifecycle 注册表：Done() 立即关闭，
//     长连接开始收尾，其钩子与第 3 步在同一个宽限期内并行执行；
//  2. HTTP/3 is closed outright (QUIC cannot drain); | 直接关闭 HTTP/3（QUIC 无法排空）；
//  3. the HTTP server drains in-flight requests until ctx expires; | HTTP 服务器排空进行中的请求，直到 ctx 到期；
//  4. every connection still hijacked by a handler is force-closed; | 强制关闭所有仍被处理器 hijack 的连接；
//  5. the errors of all steps are joined. | 汇总全部步骤的错误。
//
// The caller's ctx is the grace period for the whole sequence, so Shutdown
// returns on time even when a peer refuses to go away. A nil ctx is treated as
// context.Background. | 调用方的 ctx 是整个流程的宽限期，因此即使对端拒绝离开，
// Shutdown 也会按时返回。ctx 为 nil 时按 context.Background 处理。
func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	h3 := s.h3Srv
	hs := s.httpSrv
	s.mu.Unlock()

	// 注册表若已被更早的调用方关闭（例如 app 先关长连接），其错误由该调用方汇报，这里不重复聚合；
	// 无论谁先调用，Shutdown 都保证注册表已关闭且钩子已结束。
	// If the registry was already shut down by an earlier caller (for example the app closing long
	// connections first), that caller reports its error and it is not aggregated twice here; either way
	// this call guarantees the registry is closed and its hooks have finished.
	ownsLifecycle := !s.lifecycle.ShuttingDown()

	// ① 关闭 lifecycle 的 Done() 信号并执行钩子，与 ③ 的请求排空并行。| ① Close the lifecycle Done() signal and run its hooks, in parallel with the drain of ③.
	hooks := make(chan error, 1)
	go func() {
		hookErr := s.lifecycle.Shutdown(ctx)
		if !ownsLifecycle {
			hookErr = nil
		}
		hooks <- hookErr
	}()

	var errs []error

	// ② HTTP/3 无法排空，直接关闭。| ② HTTP/3 cannot drain, so it is closed outright.
	if h3 != nil {
		if err := h3.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	// ③ 排空进行中的普通请求；hijack 出去的连接不在 http.Server 的追踪范围内。| ③ Drain in-flight ordinary requests; hijacked connections are outside http.Server's tracking.
	if hs != nil {
		if err := hs.Shutdown(ctx); err != nil {
			errs = append(errs, errx.Wrap(err, ErrGracefulShutdown))
		}
	}

	// ④ 强制关闭仍然存活的 hijack 连接（WebSocket 等），否则对端会一直挂着。| ④ Force-close surviving hijacked connections (WebSocket and friends), otherwise the peer stays hanging.
	errs = append(errs, s.closeHijacked()...)

	// ⑤ 汇总钩子与以上各步的错误。| ⑤ Aggregate the hook errors with those of the steps above.
	if err := <-hooks; err != nil {
		errs = append(errs, errx.Wrap(err, ErrShutdownHook))
	}

	s.mu.Lock()
	s.started = false
	s.mu.Unlock()
	s.logger.Info("server stopped | 服务器已停止")
	return errors.Join(errs...)
}

// Run starts the server and blocks until ctx is cancelled, a signal arrives,
// or a fatal serve error occurs; it then shuts down gracefully. | Run 启动服务器并阻塞，
// 直到 ctx 被取消、收到信号或出现致命服务错误，随后优雅退出。
func (s *Server) Run(ctx context.Context) error {
	if err := s.Start(); err != nil {
		return err
	}

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-sigCtx.Done():
	case <-s.done:
	case err := <-s.serveErr:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.opts.ShutdownTimeout)
		defer cancel()
		_ = s.Shutdown(shutdownCtx)
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.opts.ShutdownTimeout)
	defer cancel()
	return s.Shutdown(shutdownCtx)
}

// buildHandler wires ACME challenge handling and Alt-Svc advertisement. | buildHandler 装配 ACME 质询处理与 Alt-Svc 通告。
func (s *Server) buildHandler() http.Handler {
	h := s.opts.Handler

	if s.opts.ACME.Enabled {
		mgr, err := newACMEManager(s.opts.ACME)
		if err != nil {
			s.logger.Error("ACME initialization failed | ACME 初始化失败", logx.Err(err))
		} else {
			s.acme = mgr
			challenge := mgr.manager.HTTPHandler(nil)
			next := h
			h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if len(r.URL.Path) >= len(acmeChallengePrefix) && r.URL.Path[:len(acmeChallengePrefix)] == acmeChallengePrefix {
					challenge.ServeHTTP(w, r)
					return
				}
				next.ServeHTTP(w, r)
			})
		}
	}

	if s.altSvc != "" {
		next := h
		alt := s.altSvc
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 已经跑在 HTTP/3 上的请求无需再通告
			if r.ProtoMajor < 3 {
				w.Header().Set("Alt-Svc", alt)
			}
			next.ServeHTTP(w, r)
		})
	}
	return h
}

// tlsActive reports whether TLS is enabled for the TCP listener. | tlsActive 报告 TCP 监听是否启用 TLS。
func (s *Server) tlsActive() bool { return s.opts.TLS.Enabled || s.opts.ACME.Enabled }

// buildTLSConfig resolves the certificate source. | buildTLSConfig 解析证书来源。
//
// Order: ACME manager > certificate files > self-signed (HTTP/3 dev mode). | 顺序：
// ACME 管理器 > 证书文件 > 自签证书（HTTP/3 开发模式）。
func (s *Server) buildTLSConfig() (*tls.Config, error) {
	minVersion := s.opts.TLS.MinVersion
	if minVersion == 0 {
		minVersion = tls.VersionTLS12
	}
	switch {
	case s.opts.ACME.Enabled:
		mgr, err := newACMEManager(s.opts.ACME)
		if err != nil {
			return nil, err
		}
		s.acme = mgr
		return &tls.Config{
			GetCertificate: mgr.manager.GetCertificate,
			MinVersion:     minVersion,
			NextProtos:     []string{"h2", "http/1.1"},
		}, nil

	case s.opts.TLS.Enabled:
		if s.opts.TLS.CertFile == "" || s.opts.TLS.KeyFile == "" {
			return nil, ErrTLSFilesMissing
		}
		cert, err := tls.LoadX509KeyPair(s.opts.TLS.CertFile, s.opts.TLS.KeyFile)
		if err != nil {
			return nil, errx.Wrap(err, ErrCertLoadFailed)
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   minVersion,
			NextProtos:   []string{"h2", "http/1.1"},
		}, nil

	case s.opts.HTTP3.Enabled:
		// QUIC 强制 TLS，开发环境下自动生成一次性自签证书
		cert, err := selfSignedCertForDev()
		if err != nil {
			return nil, err
		}
		s.logger.Warn("HTTP/3 enabled without certificates, using an ephemeral self-signed one (development only) | HTTP/3 已启用但未配置证书，使用临时自签证书（仅限开发）")
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"h2", "http/1.1"},
		}, nil

	default:
		return nil, nil
	}
}

// loggerConfig adapts logx to the stdlib http.Server error logger. | loggerConfig 把 logx 适配为标准库 http.Server 的错误日志器。
func (s *Server) loggerConfig() *log.Logger {
	if s.logger == nil {
		return nil
	}
	return log.New(logxWriter{s.logger}, "", 0)
}

// logxWriter forwards stdlib server errors into logx. | logxWriter 把标准库服务器错误转发给 logx。
type logxWriter struct{ l *logx.Logger }

func (w logxWriter) Write(p []byte) (int, error) {
	msg := string(p)
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	w.l.Warn("http server message | HTTP 服务器消息", logx.Str("msg", msg))
	return len(p), nil
}
