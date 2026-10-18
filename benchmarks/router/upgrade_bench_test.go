package routerbench

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ydrouter "github.com/shibingli/yd/http/router"
	ourws "github.com/shibingli/yd/http/ws"
)

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
// raw HTTP handler and through the router. | BenchmarkUpgradeRouter 测量原始 HTTP
// 处理器与 router 两条路径的每连接握手开销。
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
	if err := r.Validate(); err != nil {
		b.Fatalf("yd router validation: %v | yd 路由校验失败", err)
	}
	handlers := []struct {
		handler http.Handler
		name    string
	}{
		{name: "raw-http", handler: http.HandlerFunc(upgrade)},
		{name: "router", handler: r},
	}
	for _, hc := range handlers {
		hc := hc
		if !benchSelected(hc.name) {
			continue
		}
		b.Run(hc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				hc.handler.ServeHTTP(hw, req)
			}
		})
	}
}
