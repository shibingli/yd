package ws

import (
	"io"
	"net"
	"testing"
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
