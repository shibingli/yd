package wsbench

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gobwas/ws"
	"github.com/gorilla/websocket"
	"github.com/lxzan/gws"
	ourws "github.com/shibingli/ydx/websocket"
)

// benchSink keeps read results observable. | benchSink 让读取结果保持可观测。
var benchSink int

// benchSelected reports whether name passes the YD_BENCH_ONLY filter. Set it to
// one comma-separated list of implementation names to run each library in its
// own process and remove same-process ordering and GC bias. | benchSelected
// 判断 name 是否通过 YD_BENCH_ONLY 过滤。设为逗号分隔的实现名可让每个库独立
// 进程运行，消除同进程顺序与 GC 偏差。
func benchSelected(name string) bool {
	want := os.Getenv("YD_BENCH_ONLY")
	if want == "" {
		return true
	}
	for _, part := range strings.Split(want, ",") {
		if strings.TrimSpace(part) == name {
			return true
		}
	}
	return false
}

// errMessageTooLarge reports a message that does not fit the benchmark buffer. | errMessageTooLarge
// 表示消息超出基准缓冲区。
var errMessageTooLarge = errors.New("wsbench: message exceeds buffer | wsbench: 消息超出缓冲区")

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
			b.Fatalf("read: %v | 读取失败", err)
		}
		if n != size {
			b.Fatalf("read %d bytes, want %d | 读取字节数不符", n, size)
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
			if _, err := io.ReadFull(r, extra[:]); err == io.EOF {
				return n, nil
			} else if err != nil {
				return n, err
			}
			return 0, errMessageTooLarge
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

// startOurServer starts our echo server behind a plain HTTP handler. | startOurServer 在普通 HTTP 处理器后启动本项目回显服务端。
func startOurServer(b *testing.B, size int) (string, func()) {
	b.Helper()
	bufSize := bufSizeFor(size)
	return startHTTP(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ourws.Upgrade(w, r, ourws.Options{ReadBufferSize: bufSize})
		if err != nil {
			// The client observes the failed handshake; reporting from this
			// goroutine could outlive the benchmark. | 客户端会观察到握手失败；
			// 从这个协程上报可能晚于基准结束。
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
	}))
}

// ---------------------------------------------------------------- ours | 本项目

func benchmarkOur(b *testing.B, size int) {
	b.Helper()
	bufSize := bufSizeFor(size)
	addr, stop := startOurServer(b, size)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := ourws.Dial(ctx, "ws://"+addr, ourws.DialOptions{ReadBufferSize: bufSize})
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
			// The client observes the failed handshake. | 客户端会观察到握手失败。
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

	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := coderws.Dial(dialCtx, "ws://"+addr, nil)
	if err != nil {
		b.Fatalf("coder dial: %v | 连接失败", err)
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	// Message operations must not inherit the short dial deadline. | 消息操作不得
	// 继承短暂的拨号截止时间。
	ctx := context.Background()
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
			// The client observes the failed handshake. | 客户端会观察到握手失败。
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
				return 0, errMessageTooLarge
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
	_ = m.Close()
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
		size := size
		run := func(name string, fn func(*testing.B)) {
			if benchSelected(name) {
				b.Run(fmt.Sprintf("%s/%d", name, size), fn)
			}
		}
		run("our", func(b *testing.B) { benchmarkOur(b, size) })
		run("coder", func(b *testing.B) { benchmarkCoder(b, size) })
		run("gorilla", func(b *testing.B) { benchmarkGorilla(b, size) })
		run("gobwas", func(b *testing.B) { benchmarkGobwas(b, size) })
		run("gws", func(b *testing.B) { benchmarkGWS(b, size) })
	}
}

// benchmarkOurConcurrent measures our echo with the synchronous client or the
// read-ahead client. | benchmarkOurConcurrent 用同步客户端或预读客户端测量本项目回显。
func benchmarkOurConcurrent(b *testing.B, readAhead bool) {
	b.Helper()
	const size = 1 << 10
	payload := bytes.Repeat([]byte("x"), size)
	bufSize := bufSizeFor(size)
	addr, stop := startOurServer(b, size)
	defer stop()
	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		c, err := ourws.Dial(context.Background(), "ws://"+addr, ourws.DialOptions{ReadBufferSize: bufSize, ReadAhead: readAhead})
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
			_, got, err := c.ReadMessage()
			if err != nil {
				b.Error(err)
				return
			}
			if len(got) != size {
				b.Errorf("read %d bytes, want %d | 读取字节数不符", len(got), size)
				return
			}
		}
	})
}

// BenchmarkEchoConcurrent measures aggregate throughput with one connection per
// parallel goroutine. | BenchmarkEchoConcurrent 测量每个并行协程一条连接时的聚合吞吐。
func BenchmarkEchoConcurrent(b *testing.B) {
	if benchSelected("our") {
		b.Run("our", func(b *testing.B) { benchmarkOurConcurrent(b, false) })
	}
	if benchSelected("our-readahead") {
		b.Run("our-readahead", func(b *testing.B) { benchmarkOurConcurrent(b, true) })
	}

	const size = 1 << 10
	payload := bytes.Repeat([]byte("x"), size)
	bufSize := bufSizeFor(size)

	if benchSelected("gorilla") {
		b.Run("gorilla", func(b *testing.B) {
			up := &websocket.Upgrader{ReadBufferSize: bufSize, WriteBufferSize: bufSize}
			addr, stop := startHTTP(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := up.Upgrade(w, r, nil)
				if err != nil {
					// The client observes the failed handshake. | 客户端会观察到握手失败。
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
					if n, err := readToEOF(rd, buf); err != nil {
						b.Error(err)
						return
					} else if n != size {
						b.Errorf("read %d bytes, want %d | 读取字节数不符", n, size)
						return
					}
				}
			})
		})
	}

	if benchSelected("coder") {
		b.Run("coder", func(b *testing.B) {
			addr, stop := startHTTP(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := coderws.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer c.CloseNow()
				c.SetReadLimit(1 << 20)
				buf := make([]byte, bufSize)
				for {
					typ, rd, err := c.Reader(context.Background())
					if err != nil {
						return
					}
					n, err := readToEOF(rd, buf)
					if err != nil {
						return
					}
					wr, err := c.Writer(context.Background(), typ)
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
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				c, _, err := coderws.Dial(dialCtx, "ws://"+addr, nil)
				cancel()
				if err != nil {
					b.Error(err)
					return
				}
				defer c.CloseNow()
				c.SetReadLimit(1 << 20)
				ctx := context.Background()
				buf := make([]byte, bufSize)
				for pb.Next() {
					wr, err := c.Writer(ctx, coderws.MessageBinary)
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
					_, rd, err := c.Reader(ctx)
					if err != nil {
						b.Error(err)
						return
					}
					if n, err := readToEOF(rd, buf); err != nil {
						b.Error(err)
						return
					} else if n != size {
						b.Errorf("read %d bytes, want %d | 读取字节数不符", n, size)
						return
					}
				}
			})
		})
	}

	if benchSelected("gobwas") {
		b.Run("gobwas", func(b *testing.B) {
			addr, stop := startGobwasServer(b, bufSize)
			defer stop()
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				conn, br, _, err := ws.Dial(context.Background(), "ws://"+addr)
				if err != nil {
					b.Error(err)
					return
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
				for pb.Next() {
					copy(send, payload)
					frame := ws.NewFrame(ws.OpBinary, true, send[:len(payload)])
					frame = ws.MaskFrameInPlaceWith(frame, mask)
					if err := ws.WriteFrame(bw, frame); err != nil {
						b.Error(err)
						return
					}
					if err := bw.Flush(); err != nil {
						b.Error(err)
						return
					}
					hdr, err := ws.ReadHeader(rd)
					if err != nil {
						b.Error(err)
						return
					}
					if int(hdr.Length) > len(buf) {
						b.Error(errMessageTooLarge)
						return
					}
					p := buf[:hdr.Length]
					if _, err := io.ReadFull(rd, p); err != nil {
						b.Error(err)
						return
					}
					if hdr.Masked {
						ws.Cipher(p, hdr.Mask, 0)
					}
					if int(hdr.Length) != size {
						b.Errorf("read %d bytes, want %d | 读取字节数不符", hdr.Length, size)
						return
					}
				}
			})
		})
	}

	if benchSelected("gws") {
		b.Run("gws", func(b *testing.B) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				b.Fatalf("listen: %v | 监听失败", err)
			}
			srv := gws.NewServer(&gwsEcho{}, &gws.ServerOption{
				ReadBufferSize:     bufSize,
				WriteBufferSize:    bufSize,
				ReadMaxPayloadSize: 16 << 20,
				CheckUtf8Enabled:   false,
				Logger:             nopLogger{},
			})
			ln = newGateListener(ln)
			go func() { _ = srv.RunListener(ln) }()
			defer func() { _ = ln.Close() }()
			addr := "ws://" + ln.Addr().String()
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				handler := &gwsClient{received: make(chan int, 1)}
				c, _, err := gws.NewClient(handler, &gws.ClientOption{
					Addr:               addr,
					ReadBufferSize:     bufSize,
					WriteBufferSize:    bufSize,
					ReadMaxPayloadSize: 16 << 20,
					CheckUtf8Enabled:   false,
					Logger:             nopLogger{},
				})
				if err != nil {
					b.Error(err)
					return
				}
				readDone := make(chan struct{})
				go func() { c.ReadLoop(); close(readDone) }()
				defer func() {
					_ = c.NetConn().Close()
					select {
					case <-readDone:
					case <-time.After(2 * time.Second):
					}
				}()
				for pb.Next() {
					if err := c.WriteMessage(gws.OpcodeBinary, payload); err != nil {
						b.Error(err)
						return
					}
					if n := <-handler.received; n != size {
						b.Errorf("read %d bytes, want %d | 读取字节数不符", n, size)
						return
					}
				}
			})
		})
	}
}

// BenchmarkEchoPipelined measures a client that sends a batch of messages
// before reading the echoes. It compares one flush per message with the
// buffered/batched write API on both ends. Only this project is measured: the
// comparison libraries expose no multi-message batch API. | BenchmarkEchoPipelined
// 测量客户端先发送一批消息再读取回显。它对比逐条刷新与两端使用缓冲/批量写 API
// 的差异。这里只测本项目：对比库均未提供多条消息的批量 API。
func BenchmarkEchoPipelined(b *testing.B) {
	if !benchSelected("our") {
		return
	}
	const size = 1 << 10
	const batch = 8
	payload := bytes.Repeat([]byte("x"), size)
	bufSize := 64 << 10
	msgs := make([]ourws.Message, batch)
	for i := range msgs {
		msgs[i] = ourws.Message{Op: ourws.BinaryMessage, Payload: payload}
	}

	run := func(name string, batchedClient, batchedServer bool) {
		b.Run(name, func(b *testing.B) {
			addr, stop := startHTTP(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := ourws.Upgrade(w, r, ourws.Options{ReadBufferSize: bufSize, WriteBufferSize: bufSize})
				if err != nil {
					return
				}
				defer func() { _ = c.CloseNow() }()
				for {
					op, p, err := c.ReadMessage()
					if err != nil {
						return
					}
					if batchedServer {
						if err := c.WriteMessageBuffered(op, p); err != nil {
							return
						}
						if c.BufferedRead() == 0 {
							if err := c.Flush(); err != nil {
								return
							}
						}
						continue
					}
					if err := c.WriteMessage(op, p); err != nil {
						return
					}
				}
			}))
			defer stop()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := ourws.Dial(ctx, "ws://"+addr, ourws.DialOptions{ReadBufferSize: bufSize, WriteBufferSize: bufSize})
			if err != nil {
				b.Fatalf("dial: %v", err)
			}
			defer func() { _ = c.CloseNow() }()

			b.SetBytes(int64(size * batch))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if batchedClient {
					if err := c.WriteBatch(msgs); err != nil {
						b.Fatalf("WriteBatch: %v", err)
					}
				} else {
					for j := 0; j < batch; j++ {
						if err := c.WriteMessage(ourws.BinaryMessage, payload); err != nil {
							b.Fatalf("WriteMessage: %v", err)
						}
					}
				}
				for j := 0; j < batch; j++ {
					if _, _, err := c.ReadMessage(); err != nil {
						b.Fatalf("ReadMessage: %v", err)
					}
				}
			}
		})
	}

	run("baseline", false, false)
	run("client-batched", true, false)
	run("server-batched", false, true)
	run("batched", true, true)
}
