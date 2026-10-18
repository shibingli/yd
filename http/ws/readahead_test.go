package ws

import (
	"bytes"
	"errors"
	"net"
	"testing"
	"time"
)

// readAheadPair builds a client with read-ahead enabled and a plain server conn. | readAheadPair
// 构造启用预读的客户端与普通服务端连接。
func readAheadPair(t *testing.T) (client, server *Conn) {
	t.Helper()
	clientNC, serverNC := net.Pipe()
	client = newConn(clientNC, Options{WriteBufferSize: 4096, MaxMessageSize: 4 << 20}, true)
	server = newConn(serverNC, Options{ReadBufferSize: 4096, MaxMessageSize: 4 << 20}, false)
	client.enableReadAhead()
	t.Cleanup(func() {
		_ = client.CloseNow()
		_ = server.CloseNow()
	})
	return client, server
}

// TestReadAheadEchoRoundTrip pins that a read-ahead connection still exchanges
// messages correctly and rejects the streaming reader. | TestReadAheadEchoRoundTrip
// 固定「启用预读的连接仍能正确收发消息，并拒绝流式读取器」。
func TestReadAheadEchoRoundTrip(t *testing.T) {
	client, server := readAheadPair(t)

	done := make(chan error, 1)
	go func() {
		for {
			op, p, err := server.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			if err := server.WriteMessage(op, p); err != nil {
				done <- err
				return
			}
		}
	}()

	for i := 0; i < 20; i++ {
		want := bytes.Repeat([]byte{byte('a' + i%26)}, 1+i*37)
		if err := client.WriteMessage(BinaryMessage, want); err != nil {
			t.Fatalf("write %d: %v | 写入第 %d 条：%v", i, err, i, err)
		}
		op, got, err := client.ReadMessage()
		if err != nil {
			t.Fatalf("read %d: %v | 读取第 %d 条：%v", i, err, i, err)
		}
		if op != BinaryMessage || !bytes.Equal(got, want) {
			t.Fatalf("message %d mismatch | 第 %d 条消息不匹配", i, i)
		}
	}

	if _, _, err := client.NextReader(); !errors.Is(err, ErrReadAheadActive) {
		t.Fatalf("NextReader error = %v, want ErrReadAheadActive | NextReader 错误 = %v，期望 ErrReadAheadActive", err, err)
	}

	_ = client.CloseNow()
	_ = server.CloseNow()
}

// TestReadAheadPayloadSurvivesNextRead pins the double-buffer contract: the
// payload returned by ReadMessage stays intact across the following read. | TestReadAheadPayloadSurvivesNextRead
// 固定双缓冲契约：ReadMessage 返回的载荷在随后一次读取后仍然完整。
func TestReadAheadPayloadSurvivesNextRead(t *testing.T) {
	client, server := readAheadPair(t)

	first := bytes.Repeat([]byte("first"), 64)
	second := bytes.Repeat([]byte("second"), 64)
	done := make(chan error, 1)
	go func() {
		if err := server.WriteMessage(BinaryMessage, first); err != nil {
			done <- err
			return
		}
		done <- server.WriteMessage(BinaryMessage, second)
	}()

	_, gotFirst, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("first read: %v | 首次读取：%v", err, err)
	}
	_, gotSecond, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("second read: %v | 第二次读取：%v", err, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("server write: %v | 服务端写入：%v", err, err)
	}
	if !bytes.Equal(gotFirst, first) {
		t.Fatalf("first payload was mutated by the pre-armed read | 首个载荷被预挂起读破坏")
	}
	if !bytes.Equal(gotSecond, second) {
		t.Fatalf("second payload mismatch | 第二个载荷不匹配")
	}
}

// TestReadAheadLargeMessageBorrowed pins the bounded-memory fallback: a message
// larger than a slot is handed over without copying and does not break the
// following exchange. | TestReadAheadLargeMessageBorrowed 固定有界内存回退：
// 大于槽位的消息不做复制直接移交，且不影响后续收发。
func TestReadAheadLargeMessageBorrowed(t *testing.T) {
	client, server := readAheadPair(t)

	large := bytes.Repeat([]byte("L"), readAheadSlotLimit+1)
	small := []byte("after-large")
	done := make(chan error, 1)
	go func() {
		if err := server.WriteMessage(BinaryMessage, large); err != nil {
			done <- err
			return
		}
		done <- server.WriteMessage(BinaryMessage, small)
	}()

	_, got, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("large read: %v | 大消息读取：%v", err, err)
	}
	if !bytes.Equal(got, large) {
		t.Fatalf("large payload mismatch | 大载荷不匹配")
	}
	if _, gotSmall, err := client.ReadMessage(); err != nil || !bytes.Equal(gotSmall, small) {
		t.Fatalf("read after large = (%q, %v) | 大消息后的读取 = (%q, %v)", gotSmall, err, gotSmall, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("server write: %v | 服务端写入：%v", err, err)
	}
	if cap(client.ra.slots[0]) > readAheadSlotLimit || cap(client.ra.slots[1]) > readAheadSlotLimit {
		t.Fatalf("slots grew past the limit: %d/%d | 槽位超过上限：%d/%d",
			cap(client.ra.slots[0]), cap(client.ra.slots[1]), cap(client.ra.slots[0]), cap(client.ra.slots[1]))
	}
}

// TestReadAheadCloseUnblocksReader pins that CloseNow wakes both the blocked
// ReadMessage and the background reader. | TestReadAheadCloseUnblocksReader 固定
// 「CloseNow 同时唤醒阻塞的 ReadMessage 与后台读协程」。
func TestReadAheadCloseUnblocksReader(t *testing.T) {
	client, _ := readAheadPair(t)

	errCh := make(chan error, 1)
	go func() {
		_, _, err := client.ReadMessage()
		errCh <- err
	}()

	// Let the background reader park on the empty pipe. | 让后台读协程停在空管道上。
	time.Sleep(10 * time.Millisecond)
	_ = client.CloseNow()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("ReadMessage succeeded after CloseNow | CloseNow 后 ReadMessage 仍成功")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReadMessage did not return after CloseNow | CloseNow 后 ReadMessage 未返回")
	}
	select {
	case <-client.ra.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("read-ahead goroutine did not exit | 预读协程未退出")
	}
}

// TestReadAheadPeerClosePropagates pins that a peer close frame surfaces as
// *CloseError through the read-ahead path. | TestReadAheadPeerClosePropagates 固定
// 「预读路径把对端关闭帧以 *CloseError 返回」。
func TestReadAheadPeerClosePropagates(t *testing.T) {
	client, server := readAheadPair(t)

	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = server.WriteControl(CloseMessage, closePayload(CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = server.CloseNow()
	}()

	_, _, err := client.ReadMessage()
	var ce *CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("ReadMessage error = %v, want *CloseError | ReadMessage 错误 = %v，期望 *CloseError", err, err)
	}
}
