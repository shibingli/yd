package wsrouter

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gobwas/ws"
	"github.com/gorilla/websocket"
	"github.com/lxzan/gws"
	ydrouter "github.com/shibingli/yd/http/router"
	ourws "github.com/shibingli/yd/http/ws"
)

// benchSink keeps read results observable. | benchSink 让读取结果保持可观测。
var benchSink int

// bufSizeFor returns a buffer large enough for the payload without over-allocating
// tiny messages. | bufSizeFor 返回足以容纳载荷且不会为小消息过度分配的缓冲大小。
func bufSizeFor(size int) int {
	if size < 4096 {
		return 4096
	}
	return size
}

// benchEcho measures one sequential round trip per iteration. | benchEcho 每次迭代测量一次顺序往返。
func benchEcho(b *testing.B, size int, write func([]byte) error, read func() (int, error)) {
	b.Helper()
	payload := bytes.Repeat([]byte{'x'}, size)

	if err := write(payload); err != nil {
		b.Fatalf("warmup write: %v", err)
	}
	if _, err := read(); err != nil {
		b.Fatalf("warmup read: %v", err)
	}

	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := write(payload); err != nil {
			b.Fatalf("write: %v", err)
		}
		n, err := read()
		if err != nil {
			b.Fatalf("read: %v", err)
		}
		benchSink += n
	}
}

// startHTTP starts an HTTP server on loopback and returns its address and a stop function. | startHTTP 在回环上启动 HTTP 服务并返回地址与停止函数。
func startHTTP(b *testing.B, h http.Handler) (string, func()) {
	b.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), func() { _ = srv.Close() }
}

// readToEOF drains one message into buf; coder and gorilla require the message
// reader to reach EOF before the next message. | readToEOF 把一条消息读入 buf；
// coder 与 gorilla 要求消息读取器到达 EOF 后才能开始下一条消息。
func readToEOF(r io.Reader, buf []byte) (int, error) {
	n := 0
	for {
		if n == len(buf) {
			var extra [1]byte
			if _, err := r.Read(extra[:]); err == io.EOF {
				return n, nil
			} else if err != nil {
				return n, err
			}
			return 0, fmt.Errorf("message exceeds buffer")
		}
		nn, err := r.Read(buf[n:])
		n += nn
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
	}
}

// startOurServer starts our echo server in one integration mode. | startOurServer 以指定集成模式启动本项目回显服务端。
func startOurServer(b *testing.B, mode string, size int) (string, func()) {
	b.Helper()
	bufSize := bufSizeFor(size)
	echo := func(w http.ResponseWriter, r *http.Request) {
		c, err := ourws.Upgrade(w, r, ourws.Options{ReadBufferSize: bufSize})
		if err != nil {
			b.Errorf("our upgrade: %v", err)
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
	}
	switch mode {
	case "raw-http":
		return startHTTP(b, http.HandlerFunc(echo))
	case "router-get":
		r := ydrouter.New()
		r.GET("/ws", func(c *ydrouter.Ctx) { echo(c.Writer, c.Request) })
		return startHTTP(b, r)
	case "router-getraw":
		r := ydrouter.New()
		r.GETRaw("/ws", echo)
		return startHTTP(b, r)
	default:
		b.Fatalf("unknown integration mode %q", mode)
		return "", nil
	}
}

// ---------------------------------------------------------------- ours | 本项目

func benchmarkOur(b *testing.B, size int) {
	benchmarkOurMode(b, size, "router-get")
}

func benchmarkOurMode(b *testing.B, size int, mode string) {
	b.Helper()
	bufSize := bufSizeFor(size)
	addr, stop := startOurServer(b, mode, size)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := ourws.Dial(ctx, "ws://"+addr+"/ws", ourws.DialOptions{ReadBufferSize: bufSize})
	if err != nil {
		b.Fatalf("our dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()

	benchEcho(b, size,
		func(p []byte) error { return c.WriteMessage(ourws.BinaryMessage, p) },
		func() (int, error) {
			_, got, err := c.ReadMessage()
			return len(got), err
		},
	)
}

// ---------------------------------------------------------------- coder/websocket

func benchmarkCoder(b *testing.B, size int) {
	b.Helper()
	addr, stop := startHTTP(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := coderws.Accept(w, r, nil)
		if err != nil {
			b.Errorf("coder accept: %v", err)
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(1 << 20)
		ctx := context.Background()
		buf := make([]byte, bufSizeFor(size))
		for {
			typ, rd, err := c.Reader(ctx)
			if err != nil {
				return
			}
			n, err := readToEOF(rd, buf)
			if err != nil {
				return
			}
			wr, err := c.Writer(ctx, typ)
			if err != nil {
				return
			}
			if _, err := wr.Write(buf[:n]); err != nil {
				_ = wr.Close()
				return
			}
			if err := wr.Close(); err != nil {
				return
			}
		}
	}))
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := coderws.Dial(ctx, "ws://"+addr, nil)
	if err != nil {
		b.Fatalf("coder dial: %v", err)
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	buf := make([]byte, bufSizeFor(size))

	benchEcho(b, size,
		func(p []byte) error {
			wr, err := c.Writer(ctx, coderws.MessageBinary)
			if err != nil {
				return err
			}
			if _, err := wr.Write(p); err != nil {
				_ = wr.Close()
				return err
			}
			return wr.Close()
		},
		func() (int, error) {
			_, rd, err := c.Reader(ctx)
			if err != nil {
				return 0, err
			}
			return readToEOF(rd, buf)
		},
	)
}

// ---------------------------------------------------------------- gorilla/websocket

func benchmarkGorilla(b *testing.B, size int) {
	b.Helper()
	bufSize := bufSizeFor(size)
	up := &websocket.Upgrader{ReadBufferSize: bufSize, WriteBufferSize: bufSize}
	addr, stop := startHTTP(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			b.Errorf("gorilla upgrade: %v", err)
			return
		}
		defer func() { _ = c.Close() }()
		buf := make([]byte, bufSize)
		for {
			mt, rd, err := c.NextReader()
			if err != nil {
				return
			}
			n, err := readToEOF(rd, buf)
			if err != nil {
				return
			}
			wr, err := c.NextWriter(mt)
			if err != nil {
				return
			}
			if _, err := wr.Write(buf[:n]); err != nil {
				_ = wr.Close()
				return
			}
			if err := wr.Close(); err != nil {
				return
			}
		}
	}))
	defer stop()

	dialer := &websocket.Dialer{ReadBufferSize: bufSize, WriteBufferSize: bufSize}
	c, _, err := dialer.Dial("ws://"+addr, nil)
	if err != nil {
		b.Fatalf("gorilla dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	buf := make([]byte, bufSize)

	benchEcho(b, size,
		func(p []byte) error {
			wr, err := c.NextWriter(websocket.BinaryMessage)
			if err != nil {
				return err
			}
			if _, err := wr.Write(p); err != nil {
				_ = wr.Close()
				return err
			}
			return wr.Close()
		},
		func() (int, error) {
			_, rd, err := c.NextReader()
			if err != nil {
				return 0, err
			}
			return readToEOF(rd, buf)
		},
	)
}

// ---------------------------------------------------------------- gobwas/ws (zero-copy upgrade)

func startGobwasServer(b *testing.B, bufSize int) (string, func()) {
	b.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				if _, err := (ws.Upgrader{ReadBufferSize: bufSize, WriteBufferSize: bufSize}).Upgrade(conn); err != nil {
					return
				}
				bw := bufio.NewWriterSize(conn, bufSize)
				buf := make([]byte, bufSize)
				for {
					hdr, err := ws.ReadHeader(conn)
					if err != nil {
						return
					}
					if int(hdr.Length) > len(buf) {
						buf = make([]byte, hdr.Length)
					}
					payload := buf[:hdr.Length]
					if _, err := io.ReadFull(conn, payload); err != nil {
						return
					}
					if hdr.Masked {
						ws.Cipher(payload, hdr.Mask, 0)
					}
					if err := ws.WriteFrame(bw, ws.NewFrame(hdr.OpCode, true, payload)); err != nil {
						return
					}
					if err := bw.Flush(); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func benchmarkGobwas(b *testing.B, size int) {
	b.Helper()
	bufSize := bufSizeFor(size)
	addr, stop := startGobwasServer(b, bufSize)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, br, _, err := ws.Dial(ctx, "ws://"+addr)
	if err != nil {
		b.Fatalf("gobwas dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	bw := bufio.NewWriterSize(conn, bufSize)
	var rd io.Reader = conn
	if br != nil {
		rd = br
	}
	buf := make([]byte, bufSize)
	send := make([]byte, bufSize)
	mask := [4]byte{1, 2, 3, 4}

	benchEcho(b, size,
		func(p []byte) error {
			copy(send, p)
			frame := ws.NewFrame(ws.OpBinary, true, send[:len(p)])
			frame = ws.MaskFrameInPlaceWith(frame, mask)
			if err := ws.WriteFrame(bw, frame); err != nil {
				return err
			}
			return bw.Flush()
		},
		func() (int, error) {
			hdr, err := ws.ReadHeader(rd)
			if err != nil {
				return 0, err
			}
			if int(hdr.Length) > len(buf) {
				return 0, fmt.Errorf("payload %d exceeds buffer", hdr.Length)
			}
			payload := buf[:hdr.Length]
			if _, err := io.ReadFull(rd, payload); err != nil {
				return 0, err
			}
			if hdr.Masked {
				ws.Cipher(payload, hdr.Mask, 0)
			}
			return len(payload), nil
		},
	)
}

// ---------------------------------------------------------------- lxzan/gws (event-driven)

// gateListener parks gws's accept loop after Close. gws v1.10's RunListener
// retries on every Accept error, so a plain closed listener becomes a busy
// loop that allocates and distorts later benchmarks. | gateListener 在 Close 后
// 停住 gws 的 accept 循环：gws v1.10 的 RunListener 对每次 Accept 错误都会重试，
// 直接关闭监听器会变成不断分配的忙循环，污染后续基准。
type gateListener struct {
	net.Listener
	stop chan struct{}
	once sync.Once
}

// newGateListener wraps ln with the park-on-close behaviour. | newGateListener 用「关闭即停住」的行为包装 ln。
func newGateListener(ln net.Listener) *gateListener {
	return &gateListener{Listener: ln, stop: make(chan struct{})}
}

// Accept parks the caller once the listener has been closed. | Accept 在监听器关闭后停住调用方。
func (l *gateListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		select {
		case <-l.stop:
			select {} // Park forever: cleanup only, never reached during a measured run. | 永久停住：只用于清理，测量期间不会走到。
		default:
		}
	}
	return c, err
}

// Close signals Accept to park and closes the underlying listener. | Close 通知 Accept 停住并关闭底层监听器。
func (l *gateListener) Close() error {
	l.once.Do(func() { close(l.stop) })
	return l.Listener.Close()
}

// nopLogger silences gws's accept-error logging during shutdown. | nopLogger 在关闭时静默 gws 的 accept 错误日志。
type nopLogger struct{}

func (nopLogger) Error(v ...any) {}

// gwsEcho echoes every message back to the peer. | gwsEcho 把每条消息回显给对端。
type gwsEcho struct{ gws.BuiltinEventHandler }

func (e *gwsEcho) OnMessage(s *gws.Conn, m *gws.Message) {
	_ = s.WriteMessage(m.Opcode, m.Bytes())
}

// gwsClient delivers each echoed message length through a channel. | gwsClient 通过通道交付每条回显消息的长度。
type gwsClient struct {
	gws.BuiltinEventHandler
	received chan int
}

func (c *gwsClient) OnMessage(_ *gws.Conn, m *gws.Message) {
	n := len(m.Bytes())
	_ = m.Close()
	c.received <- n
}

func benchmarkGWS(b *testing.B, size int) {
	b.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	bufSize := bufSizeFor(size)
	opts := &gws.ServerOption{
		ReadBufferSize:     bufSize,
		WriteBufferSize:    bufSize,
		ReadMaxPayloadSize: 16 << 20,
		CheckUtf8Enabled:   false,
		Logger:             nopLogger{},
	}
	srv := gws.NewServer(&gwsEcho{}, opts)
	ln = newGateListener(ln)
	go func() { _ = srv.RunListener(ln) }()
	defer func() { _ = ln.Close() }()

	handler := &gwsClient{received: make(chan int, 1)}
	c, _, err := gws.NewClient(handler, &gws.ClientOption{
		Addr:               "ws://" + ln.Addr().String(),
		ReadBufferSize:     bufSize,
		WriteBufferSize:    bufSize,
		ReadMaxPayloadSize: 16 << 20,
		CheckUtf8Enabled:   false,
		Logger:             nopLogger{},
	})
	if err != nil {
		b.Fatalf("gws dial: %v", err)
	}
	readDone := make(chan struct{})
	go func() {
		c.ReadLoop()
		close(readDone)
	}()
	defer func() {
		_ = c.NetConn().Close()
		select {
		case <-readDone:
		case <-time.After(2 * time.Second):
		}
	}()

	benchEcho(b, size,
		func(p []byte) error { return c.WriteMessage(gws.OpcodeBinary, p) },
		func() (int, error) { return <-handler.received, nil },
	)
}

// ---------------------------------------------------------------- benchmark entry

// BenchmarkEcho compares the sequential round-trip cost of the mainstream
// libraries and this project over TCP loopback, each using its recommended
// low-allocation API. | BenchmarkEcho 在 TCP 回环上对比主流库与本项目的顺序往返开销，
// 每个库都使用其推荐的低分配 API。
func BenchmarkEcho(b *testing.B) {
	sizes := []int{32, 256, 1 << 10, 4 << 10, 64 << 10}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("our/%d", size), func(b *testing.B) { benchmarkOur(b, size) })
		b.Run(fmt.Sprintf("coder/%d", size), func(b *testing.B) { benchmarkCoder(b, size) })
		b.Run(fmt.Sprintf("gorilla/%d", size), func(b *testing.B) { benchmarkGorilla(b, size) })
		b.Run(fmt.Sprintf("gobwas/%d", size), func(b *testing.B) { benchmarkGobwas(b, size) })
		b.Run(fmt.Sprintf("gws/%d", size), func(b *testing.B) { benchmarkGWS(b, size) })
	}
}

// BenchmarkRouterModes compares steady-state echo through the raw HTTP handler,
// the context-free GETRaw route and the full GET/Ctx route. The upgrade happens
// outside the timed loop, so the differences are noise; the benchmark verifies
// that the integration mode does not affect the per-message path. | BenchmarkRouterModes
// 对比原始 HTTP 处理器、无上下文 GETRaw 路由与完整 GET/Ctx 路由的稳态回显。握手在计时循环之外，
// 因此差异属于噪声；该基准用于确认接入方式不影响每消息路径。
func BenchmarkRouterModes(b *testing.B) {
	for _, size := range []int{32, 1 << 10, 64 << 10} {
		for _, mode := range []string{"raw-http", "router-getraw", "router-get"} {
			b.Run(fmt.Sprintf("%s/%d", mode, size), func(b *testing.B) { benchmarkOurMode(b, size, mode) })
		}
	}
}

// BenchmarkEchoConcurrent measures aggregate throughput with one connection per
// parallel goroutine. | BenchmarkEchoConcurrent 测量每个并行协程一条连接时的聚合吞吐。
func BenchmarkEchoConcurrent(b *testing.B) {
	const size = 1 << 10
	payload := bytes.Repeat([]byte("x"), size)
	bufSize := bufSizeFor(size)

	b.Run("our", func(b *testing.B) {
		addr, stop := startOurServer(b, "router-get", size)
		defer stop()
		b.SetBytes(int64(size))
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			c, err := ourws.Dial(context.Background(), "ws://"+addr+"/ws", ourws.DialOptions{ReadBufferSize: bufSize})
			if err != nil {
				b.Error(err)
				return
			}
			defer func() { _ = c.CloseNow() }()
			for pb.Next() {
				if err := c.WriteMessage(ourws.BinaryMessage, payload); err != nil {
					b.Error(err)
					return
				}
				if _, _, err := c.ReadMessage(); err != nil {
					b.Error(err)
					return
				}
			}
		})
	})

	b.Run("gorilla", func(b *testing.B) {
		up := &websocket.Upgrader{ReadBufferSize: bufSize, WriteBufferSize: bufSize}
		addr, stop := startHTTP(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := up.Upgrade(w, r, nil)
			if err != nil {
				b.Error(err)
				return
			}
			defer func() { _ = c.Close() }()
			buf := make([]byte, bufSize)
			for {
				mt, rd, err := c.NextReader()
				if err != nil {
					return
				}
				n, err := readToEOF(rd, buf)
				if err != nil {
					return
				}
				wr, err := c.NextWriter(mt)
				if err != nil {
					return
				}
				if _, err := wr.Write(buf[:n]); err != nil {
					_ = wr.Close()
					return
				}
				if err := wr.Close(); err != nil {
					return
				}
			}
		}))
		defer stop()
		dialer := &websocket.Dialer{ReadBufferSize: bufSize, WriteBufferSize: bufSize}
		b.SetBytes(int64(size))
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			c, _, err := dialer.Dial("ws://"+addr, nil)
			if err != nil {
				b.Error(err)
				return
			}
			defer func() { _ = c.Close() }()
			buf := make([]byte, bufSize)
			for pb.Next() {
				wr, err := c.NextWriter(websocket.BinaryMessage)
				if err != nil {
					b.Error(err)
					return
				}
				if _, err := wr.Write(payload); err != nil {
					_ = wr.Close()
					b.Error(err)
					return
				}
				if err := wr.Close(); err != nil {
					b.Error(err)
					return
				}
				_, rd, err := c.NextReader()
				if err != nil {
					b.Error(err)
					return
				}
				if _, err := readToEOF(rd, buf); err != nil {
					b.Error(err)
					return
				}
			}
		})
	})
}

// benchConn discards writes and reports EOF on reads. | benchConn 丢弃写入并在读取时返回 EOF。
type benchConn struct{ written int }

// Read reports EOF. | Read 返回 EOF。
func (c *benchConn) Read([]byte) (int, error) { return 0, io.EOF }

// Write counts and discards the bytes. | Write 计数并丢弃字节。
func (c *benchConn) Write(p []byte) (int, error) { c.written += len(p); return len(p), nil }

// Close is a no-op. | Close 为空操作。
func (c *benchConn) Close() error { return nil }

// LocalAddr returns nil. | LocalAddr 返回 nil。
func (c *benchConn) LocalAddr() net.Addr { return nil }

// RemoteAddr returns nil. | RemoteAddr 返回 nil。
func (c *benchConn) RemoteAddr() net.Addr { return nil }

// SetDeadline is a no-op. | SetDeadline 为空操作。
func (c *benchConn) SetDeadline(time.Time) error { return nil }

// SetReadDeadline is a no-op. | SetReadDeadline 为空操作。
func (c *benchConn) SetReadDeadline(time.Time) error { return nil }

// SetWriteDeadline is a no-op. | SetWriteDeadline 为空操作。
func (c *benchConn) SetWriteDeadline(time.Time) error { return nil }

// benchHijacker is a ResponseWriter+Hijacker backed by a discard connection. | benchHijacker 是由丢弃连接支撑的 ResponseWriter+Hijacker。
type benchHijacker struct {
	header http.Header
	conn   net.Conn
	brw    *bufio.ReadWriter
}

// Header returns the response header map. | Header 返回响应头映射。
func (w *benchHijacker) Header() http.Header { return w.header }

// Write discards the bytes. | Write 丢弃字节。
func (w *benchHijacker) Write(p []byte) (int, error) { return len(p), nil }

// WriteHeader is a no-op. | WriteHeader 为空操作。
func (w *benchHijacker) WriteHeader(int) {}

// Hijack returns the pre-built connection and buffers. | Hijack 返回预构造的连接与缓冲。
func (w *benchHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.conn, w.brw, nil }

// BenchmarkUpgradeRouter measures the per-connection upgrade cost through a
// raw HTTP handler and through the router. | BenchmarkUpgradeRouter 测量原始 HTTP 处理器
// 与 router 两条路径的每连接握手开销。
func BenchmarkUpgradeRouter(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	sink := &benchConn{}
	brw := bufio.NewReadWriter(bufio.NewReaderSize(sink, 4096), bufio.NewWriterSize(sink, 4096))
	hw := &benchHijacker{header: make(http.Header), conn: sink, brw: brw}
	upgrade := func(w http.ResponseWriter, r *http.Request) {
		c, err := ourws.Upgrade(w, r, ourws.Options{})
		if err == nil {
			_ = c.CloseNow()
		}
	}
	r := ydrouter.New()
	r.GET("/ws", func(c *ydrouter.Ctx) { upgrade(c.Writer, c.Request) })
	handlers := map[string]http.Handler{
		"raw-http": http.HandlerFunc(upgrade),
		"router":   r,
	}
	for name, h := range handlers {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.ServeHTTP(hw, req)
			}
		})
	}
}
