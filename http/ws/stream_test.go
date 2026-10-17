package ws

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// newTestClientStream builds a client-side streamConn over two pipes. | newTestClientStream 基于两对管道构造客户端 streamConn。
func newTestClientStream(t *testing.T) (*streamConn, *io.PipeReader, *io.PipeWriter, *io.PipeReader, *io.PipeWriter) {
	t.Helper()
	srvToCliR, srvToCliW := io.Pipe()
	cliToSrvR, cliToSrvW := io.Pipe()
	_, cancel := context.WithCancel(context.Background())
	c := newStreamConnClient(srvToCliR, cliToSrvW, cancel, nil, nil)
	t.Cleanup(func() {
		_ = c.Close()
		_ = srvToCliW.Close()
		_ = cliToSrvR.Close()
	})
	return c, srvToCliR, srvToCliW, cliToSrvR, cliToSrvW
}

// TestStreamConnClientRoundTrip proves both directions map onto the two pipes. | TestStreamConnClientRoundTrip 证明两个方向分别映射到两对管道。
func TestStreamConnClientRoundTrip(t *testing.T) {
	c, _, srvToCliW, cliToSrvR, _ := newTestClientStream(t)

	writeDone := make(chan error, 1)
	go func() {
		_, err := c.Write([]byte("ping"))
		writeDone <- err
	}()
	got := make([]byte, 4)
	if _, err := io.ReadFull(cliToSrvR, got); err != nil {
		t.Fatalf("read client→server: %v | 读取客户端→服务端失败: %v", err, err)
	}
	if string(got) != "ping" {
		t.Fatalf("client→server = %q, want %q", got, "ping")
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("Write: %v", err)
	}

	go func() { _, _ = srvToCliW.Write([]byte("pong")) }()
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read server→client: %v | 读取服务端→客户端失败: %v", err, err)
	}
	if string(got) != "pong" {
		t.Fatalf("server→client = %q, want %q", got, "pong")
	}
}

// TestStreamConnCloseIsIdempotent proves Close is safe to repeat and writes fail afterwards. | TestStreamConnCloseIsIdempotent 证明 Close 可重复调用且之后写入失败。
func TestStreamConnCloseIsIdempotent(t *testing.T) {
	c, _, _, _, _ := newTestClientStream(t)
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := c.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write after Close = %v, want net.ErrClosed | Close 后 Write = %v，期望 net.ErrClosed", err, err)
	}
}

// TestStreamConnWriteDeadlineInPast proves a past write deadline fails the next write. | TestStreamConnWriteDeadlineInPast 证明写截止时间已过时下一次写入失败。
func TestStreamConnWriteDeadlineInPast(t *testing.T) {
	c, _, _, _, _ := newTestClientStream(t)
	if err := c.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	if _, err := c.Write([]byte("x")); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Write = %v, want os.ErrDeadlineExceeded | Write = %v，期望 os.ErrDeadlineExceeded", err, err)
	}
}

// TestStreamConnReadDeadlineInPast proves a past read deadline fails the next read. | TestStreamConnReadDeadlineInPast 证明读截止时间已过时下一次读取失败。
func TestStreamConnReadDeadlineInPast(t *testing.T) {
	c, _, _, _, _ := newTestClientStream(t)
	if err := c.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 1)
	if _, err := c.Read(buf); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read = %v, want os.ErrDeadlineExceeded | Read = %v，期望 os.ErrDeadlineExceeded", err, err)
	}
}

// TestStreamConnFlushIsNoopForClient proves the client side has nothing to flush. | TestStreamConnFlushIsNoopForClient 证明客户端没有可刷新的缓冲。
func TestStreamConnFlushIsNoopForClient(t *testing.T) {
	c, _, _, _, _ := newTestClientStream(t)
	if err := c.FlushError(); err != nil {
		t.Fatalf("FlushError = %v, want nil", err)
	}
}

// TestStreamConnAddressesNeverNil proves callers can always log the addresses. | TestStreamConnAddressesNeverNil 证明调用方始终可以记录地址。
func TestStreamConnAddressesNeverNil(t *testing.T) {
	c, _, _, _, _ := newTestClientStream(t)
	if c.LocalAddr() == nil || c.RemoteAddr() == nil {
		t.Fatalf("addresses must not be nil | 地址不得为 nil")
	}
}

// deadlineWriter is a minimal http.ResponseWriter exposing ResponseController hooks. | deadlineWriter 是最小的 http.ResponseWriter，暴露 ResponseController 所需钩子。
type deadlineWriter struct {
	readDeadline  time.Time
	writeDeadline time.Time
	flushed       bool
}

func (w *deadlineWriter) Header() http.Header         { return http.Header{} }
func (w *deadlineWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *deadlineWriter) WriteHeader(int)             {}
func (w *deadlineWriter) FlushError() error           { w.flushed = true; return nil }
func (w *deadlineWriter) SetReadDeadline(t time.Time) error {
	w.readDeadline = t
	return nil
}
func (w *deadlineWriter) SetWriteDeadline(t time.Time) error {
	w.writeDeadline = t
	return nil
}

// TestStreamConnServerForwardsDeadlines proves the server side delegates to ResponseController. | TestStreamConnServerForwardsDeadlines 证明服务端把截止时间与刷新委托给 ResponseController。
func TestStreamConnServerForwardsDeadlines(t *testing.T) {
	w := &deadlineWriter{}
	c := newStreamConnServer(io.NopCloser(strings.NewReader("")), w, http.NewResponseController(w), nil, nil)
	deadline := time.Now().Add(time.Minute)
	if err := c.SetReadDeadline(deadline); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if !w.readDeadline.Equal(deadline) {
		t.Fatalf("read deadline = %v, want %v", w.readDeadline, deadline)
	}
	if err := c.SetWriteDeadline(deadline); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	if !w.writeDeadline.Equal(deadline) {
		t.Fatalf("write deadline = %v, want %v", w.writeDeadline, deadline)
	}
	if err := c.FlushError(); err != nil {
		t.Fatalf("FlushError: %v", err)
	}
	if !w.flushed {
		t.Fatal("FlushError was not forwarded | FlushError 未被转发")
	}
}
