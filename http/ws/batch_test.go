package ws

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// testMaxMessageSize is large enough for every payload used by the batch tests. | testMaxMessageSize
// 足以容纳批量测试使用的全部载荷。
const testMaxMessageSize = 1 << 20

// readMessages reads n messages from c and reports their payloads. | readMessages 从 c 读取 n 条消息并回报其载荷。
func readMessages(c *Conn, n int) ([][]byte, error) {
	payloads := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		_, p, err := c.ReadMessage()
		if err != nil {
			return payloads, err
		}
		payloads = append(payloads, append([]byte(nil), p...))
	}
	return payloads, nil
}

// TestWriteBatchCoalescesIntoOneTransportWrite pins that one WriteBatch call
// reaches the transport as a single write. | TestWriteBatchCoalescesIntoOneTransportWrite
// 固定「一次 WriteBatch 只产生一次传输层写入」。
func TestWriteBatchCoalescesIntoOneTransportWrite(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	wc := &countingConn{Conn: server}
	c := newConn(wc, Options{WriteBufferSize: 4096}, false)
	defer func() { _ = c.CloseNow() }()

	msgs := []Message{
		{Op: BinaryMessage, Payload: bytes.Repeat([]byte("a"), 10)},
		{Op: TextMessage, Payload: []byte("hello")},
		{Op: BinaryMessage, Payload: bytes.Repeat([]byte("b"), 100)},
	}

	type result struct {
		payloads [][]byte
		err      error
	}
	done := make(chan result, 1)
	go func() {
		rc := newConn(client, Options{ReadBufferSize: 4096, MaxMessageSize: testMaxMessageSize}, true)
		payloads, err := readMessages(rc, len(msgs))
		done <- result{payloads: payloads, err: err}
	}()

	if err := c.WriteBatch(msgs); err != nil {
		t.Fatalf("WriteBatch: %v | WriteBatch：%v", err, err)
	}
	got := <-done
	if got.err != nil {
		t.Fatalf("peer read: %v | 对端读取：%v", got.err, got.err)
	}
	for i := range msgs {
		if !bytes.Equal(got.payloads[i], msgs[i].Payload) {
			t.Fatalf("message %d = %q, want %q | 第 %d 条消息 = %q，期望 %q", i, got.payloads[i], msgs[i].Payload, i, got.payloads[i], msgs[i].Payload)
		}
	}
	if wc.writes != 1 {
		t.Fatalf("transport writes = %d, want 1 | 传输层写入次数 = %d，期望 1", wc.writes, wc.writes)
	}
}

// TestWriteMessageBufferedDefersUntilFlush pins that buffered writes stay in
// the connection until Flush and then leave as one transport write.
// | TestWriteMessageBufferedDefersUntilFlush 固定「缓冲写入在 Flush 前不落到传输层，
// Flush 后只产生一次传输层写入」。
func TestWriteMessageBufferedDefersUntilFlush(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	wc := &countingConn{Conn: server}
	c := newConn(wc, Options{WriteBufferSize: 4096}, false)
	defer func() { _ = c.CloseNow() }()

	first := bytes.Repeat([]byte("x"), 32)
	second := bytes.Repeat([]byte("y"), 64)
	if err := c.WriteMessageBuffered(BinaryMessage, first); err != nil {
		t.Fatalf("WriteMessageBuffered: %v | WriteMessageBuffered：%v", err, err)
	}
	if err := c.WriteMessageBuffered(BinaryMessage, second); err != nil {
		t.Fatalf("WriteMessageBuffered: %v | WriteMessageBuffered：%v", err, err)
	}
	if wc.writes != 0 {
		t.Fatalf("transport writes before Flush = %d, want 0 | Flush 前传输层写入次数 = %d，期望 0", wc.writes, wc.writes)
	}

	done := make(chan error, 1)
	go func() {
		rc := newConn(client, Options{ReadBufferSize: 4096, MaxMessageSize: testMaxMessageSize}, true)
		payloads, err := readMessages(rc, 2)
		if err != nil {
			done <- err
			return
		}
		if !bytes.Equal(payloads[0], first) || !bytes.Equal(payloads[1], second) {
			done <- errors.New("payload mismatch | 载荷不匹配")
			return
		}
		done <- nil
	}()

	if err := c.Flush(); err != nil {
		t.Fatalf("Flush: %v | Flush：%v", err, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("peer read: %v | 对端读取：%v", err, err)
	}
	if wc.writes != 1 {
		t.Fatalf("transport writes = %d, want 1 | 传输层写入次数 = %d，期望 1", wc.writes, wc.writes)
	}
}

// TestWriteBatchFlushesWhenBufferFills pins that a batch larger than the write
// buffer is emitted in several transport writes without corrupting frames.
// | TestWriteBatchFlushesWhenBufferFills 固定「超出写缓冲的批次会分成多次传输层写入，
// 且帧内容不受影响」。
func TestWriteBatchFlushesWhenBufferFills(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	wc := &countingConn{Conn: server}
	c := newConn(wc, Options{WriteBufferSize: 64}, false)
	defer func() { _ = c.CloseNow() }()

	const count = 10
	msgs := make([]Message, count)
	for i := range msgs {
		msgs[i] = Message{Op: BinaryMessage, Payload: bytes.Repeat([]byte{byte('a' + i)}, 20)}
	}

	done := make(chan error, 1)
	go func() {
		rc := newConn(client, Options{ReadBufferSize: 256, MaxMessageSize: testMaxMessageSize}, true)
		payloads, err := readMessages(rc, count)
		if err != nil {
			done <- err
			return
		}
		for i := range msgs {
			if !bytes.Equal(payloads[i], msgs[i].Payload) {
				done <- errors.New("payload mismatch | 载荷不匹配")
				return
			}
		}
		done <- nil
	}()

	if err := c.WriteBatch(msgs); err != nil {
		t.Fatalf("WriteBatch: %v | WriteBatch：%v", err, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("peer read: %v | 对端读取：%v", err, err)
	}
	if wc.writes < 2 {
		t.Fatalf("transport writes = %d, want >= 2 | 传输层写入次数 = %d，期望 >= 2", wc.writes, wc.writes)
	}
}

// TestWriteBatchValidatesBeforeWriting pins that an invalid opcode rejects the
// whole batch before any byte reaches the wire. | TestWriteBatchValidatesBeforeWriting
// 固定「批次中存在非法操作码时，整批在任何字节上线前被拒绝」。
func TestWriteBatchValidatesBeforeWriting(t *testing.T) {
	valid := bytes.Repeat([]byte("v"), 8)
	cases := []struct {
		name string
		op   OpCode
		want error
	}{
		{name: "continuation", op: ContinuationMessage, want: ErrWriteContinuation},
		{name: "ping", op: PingMessage, want: ErrWriteMessageOpcode},
		{name: "reserved", op: OpCode(0x3), want: ErrWriteMessageOpcode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer func() { _ = client.Close() }()
			wc := &countingConn{Conn: server}
			c := newConn(wc, Options{WriteBufferSize: 4096}, false)
			defer func() { _ = c.CloseNow() }()

			err := c.WriteBatch([]Message{
				{Op: BinaryMessage, Payload: valid},
				{Op: tc.op, Payload: valid},
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("WriteBatch error = %v, want %v | WriteBatch 错误 = %v，期望 %v", err, tc.want, err, tc.want)
			}
			if wc.writes != 0 {
				t.Fatalf("transport writes = %d, want 0 | 传输层写入次数 = %d，期望 0", wc.writes, wc.writes)
			}
		})
	}
}

// TestWriteBatchLargeFrameKeepsOrder pins that a frame larger than the write
// buffer flushes the pending small frames first and keeps wire order.
// | TestWriteBatchLargeFrameKeepsOrder 固定「超出写缓冲的大帧会先刷出待发小帧，
// 线序保持不变」。
func TestWriteBatchLargeFrameKeepsOrder(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	wc := &countingConn{Conn: server}
	c := newConn(wc, Options{WriteBufferSize: 128}, false)
	defer func() { _ = c.CloseNow() }()

	small := bytes.Repeat([]byte("s"), 16)
	large := bytes.Repeat([]byte("L"), 1<<10)
	msgs := []Message{
		{Op: BinaryMessage, Payload: small},
		{Op: BinaryMessage, Payload: small},
		{Op: BinaryMessage, Payload: large},
		{Op: BinaryMessage, Payload: small},
	}

	done := make(chan error, 1)
	go func() {
		rc := newConn(client, Options{ReadBufferSize: 4096, MaxMessageSize: testMaxMessageSize}, true)
		payloads, err := readMessages(rc, len(msgs))
		if err != nil {
			done <- err
			return
		}
		for i := range msgs {
			if !bytes.Equal(payloads[i], msgs[i].Payload) {
				done <- errors.New("payload mismatch | 载荷不匹配")
				return
			}
		}
		done <- nil
	}()

	if err := c.WriteBatch(msgs); err != nil {
		t.Fatalf("WriteBatch: %v | WriteBatch：%v", err, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("peer read: %v | 对端读取：%v", err, err)
	}
	if wc.writes < 2 {
		// Pending small frames must be flushed before the scatter write; the
		// transport may split the scatter list into several Write calls (net.Pipe
		// does), so only the lower bound is asserted here. | 待发小帧必须先于分散写
		// 刷出；传输层可能把分散列表拆成多次 Write（net.Pipe 就是如此），因此这里
		// 只断言下界。
		t.Fatalf("transport writes = %d, want >= 2 | 传输层写入次数 = %d，期望 >= 2", wc.writes, wc.writes)
	}
}

// TestWriteBatchClientMasksMessages pins that batched client frames are masked
// correctly and still arrive as one transport write. | TestWriteBatchClientMasksMessages
// 固定「客户端批量帧掩码正确，且仍只产生一次传输层写入」。
func TestWriteBatchClientMasksMessages(t *testing.T) {
	clientNC, serverNC := net.Pipe()
	defer func() { _ = serverNC.Close() }()
	wc := &countingConn{Conn: clientNC}
	client := newConn(wc, Options{WriteBufferSize: 4096}, true)
	defer func() { _ = client.CloseNow() }()

	msgs := []Message{
		{Op: TextMessage, Payload: []byte("first")},
		{Op: BinaryMessage, Payload: bytes.Repeat([]byte("z"), 200)},
	}
	done := make(chan error, 1)
	go func() {
		server := newConn(serverNC, Options{ReadBufferSize: 4096, MaxMessageSize: testMaxMessageSize}, false)
		payloads, err := readMessages(server, len(msgs))
		if err != nil {
			done <- err
			return
		}
		for i := range msgs {
			if !bytes.Equal(payloads[i], msgs[i].Payload) {
				done <- errors.New("payload mismatch | 载荷不匹配")
				return
			}
		}
		done <- nil
	}()

	if err := client.WriteBatch(msgs); err != nil {
		t.Fatalf("WriteBatch: %v | WriteBatch：%v", err, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("server read: %v | 服务端读取：%v", err, err)
	}
	if wc.writes != 1 {
		t.Fatalf("transport writes = %d, want 1 | 传输层写入次数 = %d，期望 1", wc.writes, wc.writes)
	}
}

// TestBufferedReadReportsPipelinedBytes pins that BufferedRead exposes bytes of
// the next message already sitting in the read buffer. | TestBufferedReadReportsPipelinedBytes
// 固定「BufferedRead 能报告读缓冲中已到达的下一条消息字节」。
func TestBufferedReadReportsPipelinedBytes(t *testing.T) {
	clientNC, serverNC := net.Pipe()
	defer func() { _ = serverNC.Close() }()
	client := newConn(clientNC, Options{WriteBufferSize: 4096}, true)
	defer func() { _ = client.CloseNow() }()
	server := newConn(serverNC, Options{ReadBufferSize: 4096, MaxMessageSize: testMaxMessageSize}, false)
	defer func() { _ = server.CloseNow() }()

	first := []byte("first")
	second := bytes.Repeat([]byte("second"), 16)

	writeDone := make(chan error, 1)
	go func() {
		if err := client.WriteMessageBuffered(BinaryMessage, first); err != nil {
			writeDone <- err
			return
		}
		if err := client.WriteMessageBuffered(BinaryMessage, second); err != nil {
			writeDone <- err
			return
		}
		writeDone <- client.Flush()
	}()

	op, got, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v | ReadMessage：%v", err, err)
	}
	if op != BinaryMessage || !bytes.Equal(got, first) {
		t.Fatalf("first message = (%v, %q), want (%v, %q) | 首条消息 = (%v, %q)，期望 (%v, %q)", op, got, BinaryMessage, first, op, got, BinaryMessage, first)
	}
	if server.BufferedRead() <= 0 {
		t.Fatalf("BufferedRead = %d, want > 0 | BufferedRead = %d，期望 > 0", server.BufferedRead(), server.BufferedRead())
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("client write: %v | 客户端写入：%v", err, err)
	}
}

// TestFlushWithoutPendingWritesIsNoop pins that Flush performs no transport
// write when nothing is buffered. | TestFlushWithoutPendingWritesIsNoop 固定
// 「没有待发数据时 Flush 不产生传输层写入」。
func TestFlushWithoutPendingWritesIsNoop(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	wc := &countingConn{Conn: server}
	c := newConn(wc, Options{WriteBufferSize: 4096}, false)
	defer func() { _ = c.CloseNow() }()

	if err := c.Flush(); err != nil {
		t.Fatalf("Flush: %v | Flush：%v", err, err)
	}
	if wc.writes != 0 {
		t.Fatalf("transport writes = %d, want 0 | 传输层写入次数 = %d，期望 0", wc.writes, wc.writes)
	}
}

// TestWriteBatchCompressedRoundTrip pins that every batched message keeps its
// own permessage-deflate frame and round-trips correctly. | TestWriteBatchCompressedRoundTrip
// 固定「批量中的每条消息各自成帧并正确完成 permessage-deflate 往返」。
func TestWriteBatchCompressedRoundTrip(t *testing.T) {
	clientNC, serverNC := net.Pipe()
	defer func() { _ = serverNC.Close() }()
	client := newConn(clientNC, Options{WriteBufferSize: 4096}, true)
	server := newConn(serverNC, Options{ReadBufferSize: 4096, MaxMessageSize: testMaxMessageSize}, false)
	defer func() { _ = client.CloseNow() }()
	defer func() { _ = server.CloseNow() }()
	cfg := newCompressionConfig()
	client.setCompression(cfg)
	server.setCompression(cfg)
	client.EnableWriteCompression(true)

	msgs := []Message{
		{Op: TextMessage, Payload: bytes.Repeat([]byte(`{"k":"v"},`), 64)},
		{Op: TextMessage, Payload: bytes.Repeat([]byte("compressible "), 32)},
		{Op: BinaryMessage, Payload: bytes.Repeat([]byte("binary "), 40)},
	}
	done := make(chan error, 1)
	go func() {
		payloads, err := readMessages(server, len(msgs))
		if err != nil {
			done <- err
			return
		}
		for i := range msgs {
			if !bytes.Equal(payloads[i], msgs[i].Payload) {
				done <- errors.New("payload mismatch | 载荷不匹配")
				return
			}
		}
		done <- nil
	}()

	if err := client.WriteBatch(msgs); err != nil {
		t.Fatalf("WriteBatch: %v | WriteBatch：%v", err, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("server read: %v | 服务端读取：%v", err, err)
	}
	if client.BufferedRead() != 0 {
		t.Fatalf("client BufferedRead = %d, want 0 | 客户端 BufferedRead = %d，期望 0", client.BufferedRead(), client.BufferedRead())
	}
}

// TestWriteMessageBufferedConcurrentWriters pins that concurrent buffered
// writers never interleave bytes inside a frame. | TestWriteMessageBufferedConcurrentWriters
// 固定「并发缓冲写不会在帧内部交错字节」。
func TestWriteMessageBufferedConcurrentWriters(t *testing.T) {
	clientNC, serverNC := net.Pipe()
	defer func() { _ = serverNC.Close() }()
	client := newConn(clientNC, Options{WriteBufferSize: 64 << 10}, true)
	server := newConn(serverNC, Options{ReadBufferSize: 64 << 10, MaxMessageSize: testMaxMessageSize}, false)
	defer func() { _ = client.CloseNow() }()
	defer func() { _ = server.CloseNow() }()

	const (
		writers = 8
		each    = 25
	)
	payloadFor := func(w, i int) []byte {
		return bytes.Repeat([]byte{byte('A' + w)}, 1+i)
	}
	done := make(chan error, 1)
	go func() {
		for n := 0; n < writers*each; n++ {
			_, p, err := server.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			if len(p) == 0 || len(p) > each {
				done <- errors.New("unexpected payload length | 载荷长度异常")
				return
			}
			for _, b := range p {
				if b != p[0] {
					done <- errors.New("frame bytes interleaved | 帧字节交错")
					return
				}
			}
		}
		done <- nil
	}()

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := client.WriteMessageBuffered(BinaryMessage, payloadFor(w, i)); err != nil {
					t.Errorf("WriteMessageBuffered: %v | WriteMessageBuffered：%v", err, err)
					return
				}
			}
			if err := client.Flush(); err != nil {
				t.Errorf("Flush: %v | Flush：%v", err, err)
			}
		}(w)
	}
	wg.Wait()
	if err := <-done; err != nil {
		t.Fatalf("server read: %v | 服务端读取：%v", err, err)
	}
}

// TestCloseFlushesBufferedMessages pins that Close emits pending buffered data
// before the close frame. | TestCloseFlushesBufferedMessages 固定「Close 会在关闭帧之前
// 发出待发的缓冲消息」。
func TestCloseFlushesBufferedMessages(t *testing.T) {
	rec := &recordConn{}
	c := newConn(rec, Options{WriteBufferSize: 4096}, false)

	payload := bytes.Repeat([]byte("p"), 40)
	if err := c.WriteMessageBuffered(BinaryMessage, payload); err != nil {
		t.Fatalf("WriteMessageBuffered: %v | WriteMessageBuffered：%v", err, err)
	}
	if err := c.Close(CloseNormalClosure, ""); err != nil {
		t.Fatalf("Close: %v | Close：%v", err, err)
	}

	dataAt := bytes.Index(rec.buf.Bytes(), payload)
	if dataAt < 0 {
		t.Fatalf("close dropped the buffered message | Close 丢弃了缓冲消息")
	}
	closeAt := bytes.Index(rec.buf.Bytes(), []byte{0x88})
	if closeAt < 0 {
		t.Fatalf("close frame not written | 未写出关闭帧")
	}
	if closeAt < dataAt {
		t.Fatalf("close frame at %d precedes data at %d | 关闭帧位置 %d 早于数据 %d", closeAt, dataAt, closeAt, dataAt)
	}
}

// recordConn records all bytes written to it. | recordConn 记录写入它的全部字节。
type recordConn struct {
	buf bytes.Buffer
}

// Read reports EOF. | Read 返回 EOF。
func (c *recordConn) Read([]byte) (int, error) { return 0, io.EOF }

// Write appends the bytes. | Write 追加字节。
func (c *recordConn) Write(p []byte) (int, error) { return c.buf.Write(p) }

// Close is a no-op. | Close 为空操作。
func (c *recordConn) Close() error { return nil }

// LocalAddr returns nil. | LocalAddr 返回 nil。
func (c *recordConn) LocalAddr() net.Addr { return nil }

// RemoteAddr returns nil. | RemoteAddr 返回 nil。
func (c *recordConn) RemoteAddr() net.Addr { return nil }

// SetDeadline is a no-op. | SetDeadline 为空操作。
func (c *recordConn) SetDeadline(time.Time) error { return nil }

// SetReadDeadline is a no-op. | SetReadDeadline 为空操作。
func (c *recordConn) SetReadDeadline(time.Time) error { return nil }

// SetWriteDeadline is a no-op. | SetWriteDeadline 为空操作。
func (c *recordConn) SetWriteDeadline(time.Time) error { return nil }

// TestWriteBatchEmptyIsNoop pins that an empty batch performs no transport write. | TestWriteBatchEmptyIsNoop
// 固定「空批次不产生任何传输层写入」。
func TestWriteBatchEmptyIsNoop(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	wc := &countingConn{Conn: server}
	c := newConn(wc, Options{WriteBufferSize: 4096}, false)
	defer func() { _ = c.CloseNow() }()

	if err := c.WriteBatch(nil); err != nil {
		t.Fatalf("WriteBatch(nil): %v | WriteBatch(nil)：%v", err, err)
	}
	if wc.writes != 0 {
		t.Fatalf("transport writes = %d, want 0 | 传输层写入次数 = %d，期望 0", wc.writes, wc.writes)
	}
}
