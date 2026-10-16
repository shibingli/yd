package ws

import (
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/shibingli/yd/b64x"
)

// ---------------------------------------------------------------- Interop scaffolding | 互操作脚手架

// wsURL converts an httptest server URL into the ws:// URL of a given path. | wsURL 把 httptest 服务端地址转换为指定路径的 ws:// 地址。
func wsURL(t *testing.T, srv *httptest.Server, path string) string {
	t.Helper()
	if path == "" {
		path = "/"
	}
	return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

// recvOrTimeout reads one value from ch and fails the test when d elapses. | recvOrTimeout 从 ch 读取一个值，超过 d 即判定测试失败。
func recvOrTimeout[T any](t *testing.T, ch <-chan T, d time.Duration, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(d):
		var zero T
		t.Fatalf("等待 %s 超时", what)
		return zero
	}
}

// closeFramePayload hand-builds a close payload (2-byte big-endian status code followed by the UTF-8 reason), | closeFramePayload 手工拼装关闭载荷（2 字节大端状态码 + UTF-8 原因），
// deliberately independent of this package's own encoder so the test does not check itself. | 刻意独立于本包的编码实现，避免测试自证。
func closeFramePayload(code CloseCode, reason string) []byte {
	p := make([]byte, 0, 2+len(reason))
	p = append(p, byte(code>>8), byte(code))
	return append(p, reason...)
}

// echoEvent is one read result observed by the echo server built on this package. | echoEvent 是基于本包构建的回显服务端观察到的一次读取结果。
type echoEvent struct {
	op  OpCode
	n   int
	err error
}

// startOurEchoServer starts an echo server built on this package's Upgrade. | startOurEchoServer 启动基于本包 Upgrade 的回显服务端。
//
// Each read result is pushed into the returned buffered channel before the echo is written, so a test that has | 每次读取结果都在写出回显之前推入返回的带缓冲通道，
// already read the echo is guaranteed to observe the corresponding event. The payload slice is handed straight to | 因此已读到回显的测试必定能观察到对应事件。载荷切片直接交给
// WriteMessage because the read buffer is only reused by the next read. | WriteMessage，因为读缓冲只在下一次读取时才被复用。
func startOurEchoServer(t *testing.T) (*httptest.Server, <-chan echoEvent) {
	t.Helper()
	events := make(chan echoEvent, 256)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, Options{})
		if err != nil {
			events <- echoEvent{err: err}
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			op, data, err := conn.ReadMessage()
			if err != nil {
				events <- echoEvent{err: err}
				return
			}
			events <- echoEvent{op: op, n: len(data)}
			if err := conn.WriteMessage(op, data); err != nil {
				events <- echoEvent{err: err}
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, events
}

// startCoderEchoServer starts an echo server built on github.com/coder/websocket, an independent implementation. | startCoderEchoServer 启动基于 github.com/coder/websocket（独立实现）的回显服务端。
//
// Every successful read reports a nil error and every failure reports the terminating error, so tests can | 每次成功读取报告 nil，任何失败报告终止错误，
// synchronize on the channel instead of sleeping. | 测试可据此在通道上同步而不必睡眠等待。
func startCoderEchoServer(t *testing.T, o *websocket.AcceptOptions) (*httptest.Server, <-chan error) {
	t.Helper()
	events := make(chan error, 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, o)
		if err != nil {
			events <- err
			return
		}
		defer func() { _ = c.CloseNow() }()
		// the peer implementation defaults to a 32KB read limit, which the >64KB cases must raise | 对端实现默认读上限为 32KB，>64KB 的用例必须放开
		c.SetReadLimit(8 << 20)

		ctx := r.Context()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				events <- err
				return
			}
			events <- nil
			if err := c.Write(ctx, typ, data); err != nil {
				events <- err
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, events
}

// ---------------------------------------------------------------- Interop: our server, peer client | 互操作：我方服务端、对端客户端

func TestInteropServerOurImplClientCoder(t *testing.T) {
	srv, events := startOurEchoServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL(t, srv, "/"), &websocket.DialOptions{})
	if err != nil {
		t.Fatalf("对端客户端 Dial 我方服务端失败: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(4 << 20)

	t.Run("ping 由我方服务端自动应答", func(t *testing.T) {
		// coder/websocket 的 Ping 自身不读连接，必须由另一个协程读取才能处理 pong； | coder/websocket's Ping does not read the connection itself, so another goroutine must read for the pong to be handled;
		// 我方服务端在读路径上自动回送 pong，这里用「写入 + 读取回显」的往返驱动客户端读循环。 | our server answers the pong automatically on its read path, so round trips of write + read drive the client's read loop.
		type pingResult struct{ err error }
		ping := make(chan pingResult, 1)
		go func() { ping <- pingResult{err: c.Ping(ctx)} }()

		var (
			pr    pingResult
			done  bool
			wrote int
		)
		for i := 0; i < 20 && !done; i++ {
			if err := c.Write(ctx, websocket.MessageText, []byte("ping driver")); err != nil {
				t.Fatalf("写入驱动消息失败: %v", err)
			}
			wrote++
			if _, _, err := c.Read(ctx); err != nil {
				t.Fatalf("读取驱动回显失败: %v", err)
			}
			select {
			case pr = <-ping:
				done = true
			default:
			}
		}
		if !done {
			t.Fatal("ping 未收到 pong（我方服务端必须在读路径自动应答）")
		}
		if pr.err != nil {
			t.Fatalf("ping 未收到 pong: %v", pr.err)
		}
		// 排空驱动消息的读取事件，保持后续子用例看到的事件序列从零开始 | drain the driver events so later sub-tests observe an event stream starting at zero
		for i := 0; i < wrote; i++ {
			ev := recvOrTimeout(t, events, 5*time.Second, "我方服务端读取结果")
			if ev.err != nil || ev.op != TextMessage || ev.n != len("ping driver") {
				t.Fatalf("驱动消息读取 = (%v, %d, %v)", ev.op, ev.n, ev.err)
			}
		}
	})

	t.Run("文本消息", func(t *testing.T) {
		payloads := []string{"hello", "双语消息 | bilingual", strings.Repeat("汉", 300)}
		for _, want := range payloads {
			if err := c.Write(ctx, websocket.MessageText, []byte(want)); err != nil {
				t.Fatalf("写入文本失败: %v", err)
			}
			typ, got, err := c.Read(ctx)
			if err != nil {
				t.Fatalf("读取文本回显失败: %v", err)
			}
			if typ != websocket.MessageText {
				t.Fatalf("消息类型 = %v, 期望 TextMessage", typ)
			}
			if string(got) != want {
				t.Fatalf("文本回显不一致: 实际 %q, 期望 %q", got, want)
			}
		}

		for i, want := range payloads {
			ev := recvOrTimeout(t, events, 5*time.Second, "我方服务端读取结果")
			if ev.err != nil {
				t.Fatalf("我方服务端第 %d 次读取失败: %v", i, ev.err)
			}
			if ev.op != TextMessage || ev.n != len(want) {
				t.Fatalf("我方服务端第 %d 次读取 = (%v, %d), 期望 (text, %d)", i, ev.op, ev.n, len(want))
			}
		}
	})

	t.Run("二进制消息", func(t *testing.T) {
		want := make([]byte, 4096)
		for i := range want {
			want[i] = byte(i) // 覆盖 0x00 与 0xff | covers both 0x00 and 0xff
		}
		if err := c.Write(ctx, websocket.MessageBinary, want); err != nil {
			t.Fatalf("写入二进制失败: %v", err)
		}
		typ, got, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("读取二进制回显失败: %v", err)
		}
		if typ != websocket.MessageBinary {
			t.Fatalf("消息类型 = %v, 期望 BinaryMessage", typ)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("二进制回显不一致: 长度 %d, 期望 %d", len(got), len(want))
		}
		ev := recvOrTimeout(t, events, 5*time.Second, "我方服务端读取结果")
		if ev.err != nil || ev.op != BinaryMessage || ev.n != len(want) {
			t.Fatalf("我方服务端读取 = (%v, %d, %v), 期望 (binary, %d, nil)", ev.op, ev.n, ev.err, len(want))
		}
	})

	t.Run("大消息与分片", func(t *testing.T) {
		t.Run("单帧 100KB 二进制（64 位长度）", func(t *testing.T) {
			want := make([]byte, 100_000) // > 65535，强制 8 字节长度 | > 65535 forces the 8-byte length form
			for i := range want {
				want[i] = byte(i*31 + 7)
			}
			if err := c.Write(ctx, websocket.MessageBinary, want); err != nil {
				t.Fatalf("写入大消息失败: %v", err)
			}
			typ, got, err := c.Read(ctx)
			if err != nil {
				t.Fatalf("读取大消息回显失败: %v", err)
			}
			if typ != websocket.MessageBinary || !bytes.Equal(got, want) {
				t.Fatalf("大消息回显不一致: 类型 %v, 长度 %d, 期望 %d", typ, len(got), len(want))
			}
			ev := recvOrTimeout(t, events, 10*time.Second, "我方服务端读取结果")
			if ev.err != nil || ev.op != BinaryMessage || ev.n != len(want) {
				t.Fatalf("我方服务端读取 = (%v, %d, %v), 期望 (binary, %d, nil)", ev.op, ev.n, ev.err, len(want))
			}
		})

		t.Run("120KB 文本（多字节字符跨 TCP 段与 64 位长度）", func(t *testing.T) {
			want := strings.Repeat("汉字 | hanzi ", 8000) // 120000 字节，多字节字符必然被 TCP 分段切开 | 120000 bytes; multi-byte runes get split across TCP segments
			if err := c.Write(ctx, websocket.MessageText, []byte(want)); err != nil {
				t.Fatalf("写入大文本失败: %v", err)
			}
			typ, got, err := c.Read(ctx)
			if err != nil {
				t.Fatalf("读取大文本回显失败: %v", err)
			}
			if typ != websocket.MessageText || string(got) != want {
				t.Fatalf("大文本回显不一致: 类型 %v, 长度 %d, 期望 %d", typ, len(got), len(want))
			}
			ev := recvOrTimeout(t, events, 10*time.Second, "我方服务端读取结果")
			if ev.err != nil || ev.op != TextMessage || ev.n != len(want) {
				t.Fatalf("我方服务端读取 = (%v, %d, %v), 期望 (text, %d, nil)", ev.op, ev.n, ev.err, len(want))
			}
		})

		t.Run("对端分片发送（我方重组）", func(t *testing.T) {
			// coder/websocket 的 Writer 每次 Write 产生一个非 FIN 数据帧，Close 写出收尾帧， | each Write of coder/websocket's Writer emits one non-FIN data frame and Close writes the final frame,
			// 因此这里真正走的是我方服务端的续帧重组路径。 | so this genuinely exercises our server's continuation-frame reassembly.
			const parts = 4
			chunk := strings.Repeat("分片 | fragment ", 64)
			w, err := c.Writer(ctx, websocket.MessageText)
			if err != nil {
				t.Fatalf("获取对端 Writer 失败: %v", err)
			}
			var sb strings.Builder
			for i := 0; i < parts; i++ {
				sb.WriteString(chunk)
				if _, err := w.Write([]byte(chunk)); err != nil {
					t.Fatalf("写入分片 %d 失败: %v", i, err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatalf("关闭对端 Writer 失败: %v", err)
			}
			want := sb.String()
			typ, got, err := c.Read(ctx)
			if err != nil {
				t.Fatalf("读取分片回显失败: %v", err)
			}
			if typ != websocket.MessageText || string(got) != want {
				t.Fatalf("分片回显不一致: 类型 %v, 长度 %d, 期望 %d", typ, len(got), len(want))
			}
			ev := recvOrTimeout(t, events, 10*time.Second, "我方服务端读取结果")
			if ev.err != nil || ev.op != TextMessage || ev.n != len(want) {
				t.Fatalf("我方服务端读取 = (%v, %d, %v), 期望 (text, %d, nil)（分片必须重组为一条消息）", ev.op, ev.n, ev.err, len(want))
			}
		})
	})

	t.Run("并发双向收发", func(t *testing.T) {
		const n = 64
		writeErr := make(chan error, 1)
		go func() {
			for i := 0; i < n; i++ {
				msg := fmt.Sprintf("msg-%03d | 并发", i)
				if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
					writeErr <- err
					return
				}
			}
			writeErr <- nil
		}()

		// 读与写并发进行；服务端逐条回显，因此顺序必须保持 | reads run concurrently with writes; the server echoes one by one, so the order must hold
		for i := 0; i < n; i++ {
			want := fmt.Sprintf("msg-%03d | 并发", i)
			typ, got, err := c.Read(ctx)
			if err != nil {
				t.Fatalf("并发读取第 %d 条失败: %v", i, err)
			}
			if typ != websocket.MessageText || string(got) != want {
				t.Fatalf("并发回显第 %d 条不一致: 类型 %v, 实际 %q, 期望 %q", i, typ, got, want)
			}
		}
		if err := <-writeErr; err != nil {
			t.Fatalf("并发写入失败: %v", err)
		}
	})
}

// ---------------------------------------------------------------- Interop: peer server, our client | 互操作：对端服务端、我方客户端

func TestInteropServerCoderClientOurImpl(t *testing.T) {
	const proto = "yd.chat"
	srv, events := startCoderEchoServer(t, &websocket.AcceptOptions{Subprotocols: []string{proto}})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c, err := Dial(ctx, wsURL(t, srv, "/"), DialOptions{Subprotocols: []string{proto, "other"}})
	if err != nil {
		t.Fatalf("我方客户端 Dial 对端服务端失败: %v", err)
	}
	defer func() { _ = c.CloseNow() }()

	t.Run("子协议协商", func(t *testing.T) {
		if got := c.Subprotocol(); got != proto {
			t.Fatalf("子协议 = %q, 期望 %q", got, proto)
		}
	})

	t.Run("文本与二进制往返", func(t *testing.T) {
		if err := c.WriteMessage(TextMessage, []byte("hello from our impl | 来自我方实现")); err != nil {
			t.Fatalf("写入文本失败: %v", err)
		}
		op, got, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("读取文本回显失败: %v", err)
		}
		if op != TextMessage || string(got) != "hello from our impl | 来自我方实现" {
			t.Fatalf("文本回显不一致: 类型 %v, 实际 %q", op, got)
		}

		bin := []byte{0x00, 0x01, 0xfe, 0xff, 0x7f, 0x80}
		if err := c.WriteMessage(BinaryMessage, bin); err != nil {
			t.Fatalf("写入二进制失败: %v", err)
		}
		op, got, err = c.ReadMessage()
		if err != nil {
			t.Fatalf("读取二进制回显失败: %v", err)
		}
		if op != BinaryMessage || !bytes.Equal(got, bin) {
			t.Fatalf("二进制回显不一致: 类型 %v, 实际 %v", op, got)
		}
	})

	t.Run("大消息（客户端掩码 + 64 位长度）", func(t *testing.T) {
		want := make([]byte, 100_000)
		for i := range want {
			want[i] = byte(i*17 + 3)
		}
		if err := c.WriteMessage(BinaryMessage, want); err != nil {
			t.Fatalf("写入大消息失败: %v", err)
		}
		op, got, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("读取大消息回显失败: %v", err)
		}
		if op != BinaryMessage || !bytes.Equal(got, want) {
			t.Fatalf("大消息回显不一致: 类型 %v, 长度 %d, 期望 %d", op, len(got), len(want))
		}
	})

	t.Run("NextReader 流式读取大消息", func(t *testing.T) {
		want := strings.Repeat("流式读取 | streaming ", 8000) // 约 152KB | roughly 152KB
		if err := c.WriteMessage(TextMessage, []byte(want)); err != nil {
			t.Fatalf("写入大文本失败: %v", err)
		}
		op, r, err := c.NextReader()
		if err != nil {
			t.Fatalf("NextReader 失败: %v", err)
		}
		if op != TextMessage {
			t.Fatalf("消息类型 = %v, 期望 TextMessage", op)
		}
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("流式读取失败: %v", err)
		}
		if string(got) != want {
			t.Fatalf("流式读取内容不一致: 长度 %d, 期望 %d", len(got), len(want))
		}
	})

	// 三段读写都成功即证明对端服务端完整收到了我方客户端（含掩码）的帧 | three successful round trips prove the peer server fully accepted our (masked) frames
	for i := 0; i < 3; i++ {
		if err := recvOrTimeout(t, events, 10*time.Second, "对端服务端读取结果"); err != nil {
			t.Fatalf("对端服务端第 %d 次读取失败: %v", i, err)
		}
	}
}

// ---------------------------------------------------------------- Interop: close handshake | 互操作：关闭握手

func TestInteropCloseHandshake(t *testing.T) {
	t.Run("对端客户端发起关闭（我方服务端）", func(t *testing.T) {
		const reason = "peer client going away | 对端客户端离开"
		srv, events := startOurEchoServer(t)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		c, _, err := websocket.Dial(ctx, wsURL(t, srv, "/"), &websocket.DialOptions{})
		if err != nil {
			t.Fatalf("对端客户端 Dial 我方服务端失败: %v", err)
		}
		defer func() { _ = c.CloseNow() }()

		// coder/websocket 的 Close 会等待对端回送同一关闭码；返回 nil 即证明我方服务端的回送完全正确 | coder/websocket's Close waits for the peer to echo the same code; nil proves our echo is correct
		if err := c.Close(websocket.StatusGoingAway, reason); err != nil {
			t.Fatalf("关闭握手失败（我方服务端未正确回送关闭帧）: %v", err)
		}

		ev := recvOrTimeout(t, events, 5*time.Second, "我方服务端关闭结果")
		var ce *CloseError
		if !errors.As(ev.err, &ce) {
			t.Fatalf("我方服务端错误 = %v, 期望 *CloseError", ev.err)
		}
		if ce.Code != CloseGoingAway || ce.Text != reason {
			t.Fatalf("我方服务端观察到关闭 = (%d, %q), 期望 (%d, %q)", ce.Code, ce.Text, CloseGoingAway, reason)
		}
	})

	t.Run("我方客户端发起关闭（对端服务端）", func(t *testing.T) {
		const reason = "our client done | 我方客户端结束"
		srv, events := startCoderEchoServer(t, nil)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		c, err := Dial(ctx, wsURL(t, srv, "/"), DialOptions{})
		if err != nil {
			t.Fatalf("我方客户端 Dial 对端服务端失败: %v", err)
		}
		defer func() { _ = c.CloseNow() }()

		if err := c.WriteControl(CloseMessage, closeFramePayload(CloseNormalClosure, reason), time.Now().Add(5*time.Second)); err != nil {
			t.Fatalf("写出关闭帧失败: %v", err)
		}

		// 对端服务端必须观察到完全一致的关闭码与原因 | the peer server must observe exactly the same close code and reason
		peerErr := recvOrTimeout(t, events, 5*time.Second, "对端服务端关闭结果")
		var ce websocket.CloseError
		if !errors.As(peerErr, &ce) {
			t.Fatalf("对端服务端错误 = %v, 期望 websocket.CloseError", peerErr)
		}
		if ce.Code != websocket.StatusNormalClosure || ce.Reason != reason {
			t.Fatalf("对端服务端观察到关闭 = (%v, %q), 期望 (%v, %q)", ce.Code, ce.Reason, websocket.StatusNormalClosure, reason)
		}
		if got := websocket.CloseStatus(peerErr); got != websocket.StatusNormalClosure {
			t.Fatalf("CloseStatus = %v, 期望 %v", got, websocket.StatusNormalClosure)
		}

		// 我方客户端读到的回送关闭帧同样一致（对端按 RFC 回送同一码与原因） | the echo our client reads carries the same code and reason the peer echoed per the RFC
		_, _, readErr := c.ReadMessage()
		var ours *CloseError
		if !errors.As(readErr, &ours) {
			t.Fatalf("我方客户端错误 = %v, 期望 *CloseError", readErr)
		}
		if ours.Code != CloseNormalClosure || ours.Text != reason {
			t.Fatalf("我方客户端读到关闭 = (%d, %q), 期望 (%d, %q)", ours.Code, ours.Text, CloseNormalClosure, reason)
		}
	})

	t.Run("我方服务端发起关闭（对端客户端）", func(t *testing.T) {
		const reason = "server restarting | 服务端重启中"
		observed := make(chan error, 2)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := Upgrade(w, r, Options{})
			if err != nil {
				observed <- err
				return
			}
			defer func() { _ = conn.CloseNow() }()
			if _, _, err := conn.ReadMessage(); err != nil {
				observed <- err
				return
			}
			// 服务端主动关闭：先发送关闭帧，再读取对端回送，完成一次完整的关闭握手 | server-initiated close: send the close frame, then read the peer's echo, completing a full handshake
			if err := conn.WriteControl(CloseMessage, closeFramePayload(CloseServiceRestart, reason), time.Now().Add(5*time.Second)); err != nil {
				observed <- err
				return
			}
			if _, _, err := conn.ReadMessage(); err != nil {
				observed <- err
				return
			}
			observed <- nil
		}))
		t.Cleanup(srv.Close)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		c, _, err := websocket.Dial(ctx, wsURL(t, srv, "/"), &websocket.DialOptions{})
		if err != nil {
			t.Fatalf("对端客户端 Dial 我方服务端失败: %v", err)
		}
		defer func() { _ = c.CloseNow() }()

		if err := c.Write(ctx, websocket.MessageText, []byte("trigger")); err != nil {
			t.Fatalf("写入触发消息失败: %v", err)
		}

		// 对端客户端必须读到与我方服务端发出的关闭码、原因完全一致的关闭帧 | the peer client must read a close frame identical to what our server sent
		_, _, readErr := c.Read(ctx)
		var ce websocket.CloseError
		if !errors.As(readErr, &ce) {
			t.Fatalf("对端客户端错误 = %v, 期望 websocket.CloseError", readErr)
		}
		if ce.Code != websocket.StatusServiceRestart || ce.Reason != reason {
			t.Fatalf("对端客户端读到关闭 = (%v, %q), 期望 (%v, %q)", ce.Code, ce.Reason, websocket.StatusServiceRestart, reason)
		}

		// 我方服务端读到的回送也必须一致 | the echo our server reads must match as well
		if ev := recvOrTimeout(t, observed, 5*time.Second, "我方服务端关闭回送"); ev != nil {
			var ours *CloseError
			if !errors.As(ev, &ours) {
				t.Fatalf("我方服务端错误 = %v, 期望 *CloseError", ev)
			}
			if ours.Code != CloseServiceRestart || ours.Text != reason {
				t.Fatalf("我方服务端读到回送 = (%d, %q), 期望 (%d, %q)", ours.Code, ours.Text, CloseServiceRestart, reason)
			}
		}
	})
}

// ---------------------------------------------------------------- Interop: handshake validation | 互操作：握手校验

// rawHandshakeServer answers the upgrade with the Accept value produced by accept, computed over the client's key. | rawHandshakeServer 用 accept 依据客户端 key 计算出的 Accept 值应答升级。
func rawHandshakeServer(t *testing.T, accept func(key string) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
			accept(r.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n"))
		// 保持连接直到客户端关闭，避免成功路径上被提前断开 | keep the connection until the client closes so the success path is not cut short
		_, _ = io.Copy(io.Discard, conn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestInteropDialRejectsBadAccept is the interop-flavoured twin of TestDialRejectsBadAccept: the same case under a | TestInteropDialRejectsBadAccept 是 TestDialRejectsBadAccept 的互操作版同名场景：
// different name, extended with the control case that a correctly computed Accept must be accepted, proving the | 以不同名字再覆盖一次，并补上「正确计算的 Accept 必须放行」的对照，证明校验收敛于
// check is an exact comparison instead of a blanket rejection of any 101 response. | 逐字节比对，而不是「任何 101 都拒绝」。
func TestInteropDialRejectsBadAccept(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	t.Run("错误的 Accept 被拒绝", func(t *testing.T) {
		srv := rawHandshakeServer(t, func(string) string { return "WRONGACCEPTVALUE" })
		if _, err := Dial(ctx, wsURL(t, srv, "/"), DialOptions{}); !errors.Is(err, ErrHandshake) {
			t.Fatalf("错误 = %v, 期望 ErrHandshake（Accept 校验必须生效）", err)
		}
	})

	t.Run("正确的 Accept 被接受", func(t *testing.T) {
		// 用标准库 SHA1 与 b64x 独立计算 Accept，不依赖本包的 acceptKey | compute the Accept independently with the standard SHA1 and b64x, not with this package's acceptKey
		srv := rawHandshakeServer(t, func(key string) string {
			sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
			return b64x.StdEncoding.EncodeToString(sum[:])
		})
		conn, err := Dial(ctx, wsURL(t, srv, "/"), DialOptions{})
		if err != nil {
			t.Fatalf("Accept 正确时 Dial 不应失败: %v", err)
		}
		defer func() { _ = conn.CloseNow() }()
		if got := conn.Subprotocol(); got != "" {
			t.Fatalf("未协商子协议时 Subprotocol = %q, 期望空串", got)
		}
	})
}
