package ws

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// acceptSink keeps the computed accept key observable. | acceptSink 让计算出的 accept 值保持可观测。
var acceptSink string

// TestAcceptKeyAllocations pins that computing the accept value does not
// allocate beyond the returned string. | TestAcceptKeyAllocations 固定「计算
// accept 值除返回字符串外不产生分配」。
func TestAcceptKeyAllocations(t *testing.T) {
	const key = "dGhlIHNhbXBsZSBub25jZQ=="
	allocs := testing.AllocsPerRun(100, func() { acceptSink = acceptKey(key) })
	if allocs > 1 {
		t.Fatalf("acceptKey allocations = %v, want <= 1 | acceptKey 分配次数 = %v，期望 <= 1", allocs, allocs)
	}
}

// subprotocolSink keeps the selected subprotocol observable. | subprotocolSink 让选中的子协议保持可观测。
var subprotocolSink string

// TestSelectSubprotocolDoesNotAllocate pins that parsing the offered
// subprotocols does not allocate. | TestSelectSubprotocolDoesNotAllocate 固定
// 「解析客户端提议的子协议不产生分配」。
func TestSelectSubprotocolDoesNotAllocate(t *testing.T) {
	r := &http.Request{Header: http.Header{
		"Sec-Websocket-Protocol": {"yd.v1, chat", "other"},
	}}
	supported := []string{"yd.v1", "chat"}
	if got := selectSubprotocol(r, supported); got != "yd.v1" {
		t.Fatalf("selectSubprotocol = %q, want %q | selectSubprotocol = %q，期望 %q", got, "yd.v1", got, "yd.v1")
	}
	allocs := testing.AllocsPerRun(100, func() { subprotocolSink = selectSubprotocol(r, supported) })
	if allocs != 0 {
		t.Fatalf("selectSubprotocol allocations = %v, want 0 | selectSubprotocol 分配次数 = %v，期望 0", allocs, allocs)
	}
}

// hijackWriter is a ResponseWriter+Hijacker backed by a sink connection. | hijackWriter 是由丢弃连接支撑的 ResponseWriter+Hijacker。
type hijackWriter struct {
	header http.Header
	conn   net.Conn
	brw    *bufio.ReadWriter
}

// Header returns the response header map. | Header 返回响应头映射。
func (w *hijackWriter) Header() http.Header { return w.header }

// Write discards the response bytes. | Write 丢弃响应字节。
func (w *hijackWriter) Write(p []byte) (int, error) { return len(p), nil }

// WriteHeader is a no-op. | WriteHeader 为空操作。
func (w *hijackWriter) WriteHeader(int) {}

// Hijack returns the pre-built connection and buffers. | Hijack 返回预构造的连接与缓冲。
func (w *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.conn, w.brw, nil }

// TestUpgradeReusesHijackBuffers pins that the HTTP server's already-allocated
// read/write buffers are reused when their sizes match, instead of allocating a
// second pair per connection. | TestUpgradeReusesHijackBuffers 固定「尺寸匹配时
// 复用 HTTP 服务器已分配的读写缓冲」，而不是为每条连接再分配一对。
func TestUpgradeReusesHijackBuffers(t *testing.T) {
	nc := &sinkConn{}
	brw := bufio.NewReadWriter(
		bufio.NewReaderSize(nc, tunedReadBufferSize()),
		bufio.NewWriterSize(nc, tunedWriteBufferSize()),
	)
	w := &hijackWriter{header: make(http.Header), conn: nc, brw: brw}
	r := httptest.NewRequest(http.MethodGet, "/ws", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	c, err := Upgrade(w, r, Options{})
	if err != nil {
		t.Fatalf("Upgrade: %v | Upgrade：%v", err, err)
	}
	defer func() { _ = c.CloseNow() }()
	if c.br != brw.Reader {
		t.Fatalf("read buffer was not reused | 读缓冲未被复用")
	}
	if c.bw != brw.Writer {
		t.Fatalf("write buffer was not reused | 写缓冲未被复用")
	}
}

// BenchmarkUpgrade measures a full HTTP/1.1 upgrade with reusable hijack
// buffers, isolating handshake allocation and CPU cost. | BenchmarkUpgrade 测量
// 使用可复用劫持缓冲的完整 HTTP/1.1 升级，隔离握手分配与 CPU 开销。
func BenchmarkUpgrade(b *testing.B) {
	nc := &sinkConn{}
	brw := bufio.NewReadWriter(
		bufio.NewReaderSize(nc, tunedReadBufferSize()),
		bufio.NewWriterSize(nc, tunedWriteBufferSize()),
	)
	w := &hijackWriter{header: make(http.Header), conn: nc, brw: brw}
	r := httptest.NewRequest(http.MethodGet, "/ws", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, err := Upgrade(w, r, Options{})
		if err != nil {
			b.Fatalf("Upgrade: %v | Upgrade：%v", err, err)
		}
		_ = c.CloseNow()
	}
}
