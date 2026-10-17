package ws

import (
	"io"
	"net"
	"testing"
	"time"
)

// countingConn records how many Write calls reached the transport. | countingConn 记录真正到达传输层的 Write 调用次数。
type countingConn struct {
	net.Conn
	writes int
	bytes  int
}

// Write delegates to the wrapped connection while counting the call. | Write 在计数后转交包装的连接。
func (c *countingConn) Write(p []byte) (int, error) {
	c.writes++
	c.bytes += len(p)
	return c.Conn.Write(p)
}

// TestWriteMessageCoalescesSmallFrame pins that a small frame leaves in one
// transport write instead of one write per buffer. | TestWriteMessageCoalescesSmallFrame
// 固定小帧只产生一次传输层写入，而不是每个缓冲一次。
func TestWriteMessageCoalescesSmallFrame(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	wc := &countingConn{Conn: server}
	c := newConn(wc, Options{WriteBufferSize: 4096}, false)
	defer func() { _ = c.CloseNow() }()

	const payload = "hello"
	read := make(chan error, 1)
	go func() {
		_, err := io.CopyN(io.Discard, client, int64(2+len(payload)))
		read <- err
	}()

	if err := c.WriteMessage(BinaryMessage, []byte(payload)); err != nil {
		t.Fatalf("WriteMessage: %v | WriteMessage：%v", err, err)
	}
	if err := <-read; err != nil {
		t.Fatalf("read frame: %v | 读取帧：%v", err, err)
	}
	if wc.writes != 1 {
		t.Fatalf("transport writes = %d, want 1 | 传输层写入次数 = %d，期望 1", wc.writes, wc.writes)
	}
	if wc.bytes != 2+len(payload) {
		t.Fatalf("transport bytes = %d, want %d | 传输层字节数 = %d，期望 %d", wc.bytes, 2+len(payload), wc.bytes, 2+len(payload))
	}
}

// TestReadMessageGrowsToMessageSize pins that one large frame does not roughly
// double the reusable read buffer; the buffer should track the message size.
// | TestReadMessageGrowsToMessageSize 固定单个大帧不会把复用读缓冲放大到约两倍，
// 缓冲应贴合消息大小。
func TestReadMessageGrowsToMessageSize(t *testing.T) {
	const size = 64 << 10
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	c := newConn(server, Options{MaxMessageSize: size}, true)
	defer func() { _ = c.CloseNow() }()

	go func() {
		hdr := make([]byte, maxHeaderSize)
		n := writeFrameHeader(hdr, true, BinaryMessage, false, size)
		if _, err := client.Write(hdr[:n]); err != nil {
			return
		}
		_, _ = client.Write(make([]byte, size))
	}()

	op, payload, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v | ReadMessage：%v", err, err)
	}
	if op != BinaryMessage || len(payload) != size {
		t.Fatalf("message = (%v, %d), want (%v, %d) | 消息 = (%v, %d)，期望 (%v, %d)", op, len(payload), BinaryMessage, size, op, len(payload), BinaryMessage, size)
	}
	if cap(c.readBuf) > size+1024 {
		t.Fatalf("read buffer capacity = %d, want <= %d | 读缓冲容量 = %d，期望 <= %d", cap(c.readBuf), size+1024, cap(c.readBuf), size+1024)
	}
}

// controlConn feeds a fixed control-frame payload and discards writes. | controlConn 提供固定的控制帧载荷并丢弃写入。
type controlConn struct {
	src [maxControlPayload]byte
	off int
}

// Read copies the remaining payload. | Read 复制剩余载荷。
func (c *controlConn) Read(p []byte) (int, error) {
	if c.off >= len(c.src) {
		return 0, io.EOF
	}
	n := copy(p, c.src[c.off:])
	c.off += n
	return n, nil
}

// Write discards the frame. | Write 丢弃帧。
func (c *controlConn) Write(p []byte) (int, error) { return len(p), nil }

// Close is a no-op. | Close 为空操作。
func (c *controlConn) Close() error { return nil }

// LocalAddr returns nil. | LocalAddr 返回 nil。
func (c *controlConn) LocalAddr() net.Addr { return nil }

// RemoteAddr returns nil. | RemoteAddr 返回 nil。
func (c *controlConn) RemoteAddr() net.Addr { return nil }

// SetDeadline is a no-op. | SetDeadline 为空操作。
func (c *controlConn) SetDeadline(time.Time) error { return nil }

// SetReadDeadline is a no-op. | SetReadDeadline 为空操作。
func (c *controlConn) SetReadDeadline(time.Time) error { return nil }

// SetWriteDeadline is a no-op. | SetWriteDeadline 为空操作。
func (c *controlConn) SetWriteDeadline(time.Time) error { return nil }

// TestHandleControlDoesNotAllocate pins that answering a ping with a full
// payload does not allocate. | TestHandleControlDoesNotAllocate 固定「应答携带完整
// 载荷的 ping 不产生分配」。
func TestHandleControlDoesNotAllocate(t *testing.T) {
	nc := &controlConn{}
	c := newConn(nc, Options{WriteBufferSize: 1024}, false)
	defer func() { _ = c.CloseNow() }()
	h := frameHeader{fin: true, opcode: PingMessage, length: maxControlPayload}

	allocs := testing.AllocsPerRun(100, func() {
		nc.off = 0
		if err := c.handleControl(h); err != nil {
			t.Fatalf("handleControl: %v | handleControl：%v", err, err)
		}
	})
	if allocs != 0 {
		t.Fatalf("handleControl allocations = %v, want 0 | handleControl 分配次数 = %v，期望 0", allocs, allocs)
	}
}
