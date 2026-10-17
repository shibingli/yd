package ws

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// startOurCompressedEchoServer starts an echo server built on this package with compression enabled, | startOurCompressedEchoServer 启动基于本包、已启用压缩的回显服务端，
// reporting on the returned channel whether the handshake actually negotiated the extension. | 并通过返回的通道报告握手是否真正协商出了扩展。
func startOurCompressedEchoServer(t *testing.T) (*httptest.Server, <-chan bool) {
	t.Helper()
	negotiated := make(chan bool, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, Options{EnableCompression: true})
		if err != nil {
			negotiated <- false
			return
		}
		defer func() { _ = c.CloseNow() }()
		negotiated <- c.compression != nil
		for {
			op, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(op, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, negotiated
}

// compressionInteropPayloads returns the shared message matrix used in both interop directions. | compressionInteropPayloads 返回两个互操作方向共用的消息矩阵。
func compressionInteropPayloads() []struct {
	name string
	typ  websocket.MessageType
	data []byte
} {
	return []struct {
		name string
		typ  websocket.MessageType
		data []byte
	}{
		{name: "small text", typ: websocket.MessageText, data: []byte("hello permessage-deflate")},
		{name: "chinese text", typ: websocket.MessageText, data: []byte(strings.Repeat("中文压缩互操作 | chinese interop ", 20))},
		{name: "empty text", typ: websocket.MessageText, data: nil},
		{name: "binary", typ: websocket.MessageBinary, data: bytes.Repeat([]byte{0x00, 0x7f, 0xff, 0x80}, 16<<10)},
	}
}

// TestCompressionInteropCoderClientOurServer drives this package's compressed server with coder/websocket's | TestCompressionInteropCoderClientOurServer 用 coder/websocket 的
// compressed client in both context-takeover modes, plus a disabled control case. | 压缩客户端（两种上下文接管模式，外加一个关闭压缩的对照）驱动本包的压缩服务端。
func TestCompressionInteropCoderClientOurServer(t *testing.T) {
	cases := []struct {
		name                string
		mode                websocket.CompressionMode
		wantServerNegotiate bool
	}{
		{name: "context takeover", mode: websocket.CompressionContextTakeover, wantServerNegotiate: true},
		{name: "no context takeover", mode: websocket.CompressionNoContextTakeover, wantServerNegotiate: true},
		{name: "disabled", mode: websocket.CompressionDisabled, wantServerNegotiate: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, negotiated := startOurCompressedEchoServer(t)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			c, _, err := websocket.Dial(ctx, wsURL(t, srv, "/"), &websocket.DialOptions{
				CompressionMode: tc.mode,
				// force every non-empty message through the codec | 让所有非空消息都走压缩器
				CompressionThreshold: 1,
			})
			if err != nil {
				t.Fatalf("对端客户端 Dial 我方服务端失败: %v", err)
			}
			defer func() { _ = c.CloseNow() }()
			c.SetReadLimit(8 << 20)

			if got := recvOrTimeout(t, negotiated, 5*time.Second, "我方服务端协商结果"); got != tc.wantServerNegotiate {
				t.Fatalf("服务端协商扩展 = %v, 期望 %v", got, tc.wantServerNegotiate)
			}

			for _, p := range compressionInteropPayloads() {
				t.Run(p.name, func(t *testing.T) {
					if err := c.Write(ctx, p.typ, p.data); err != nil {
						t.Fatalf("对端客户端写入失败: %v", err)
					}
					typ, got, err := c.Read(ctx)
					if err != nil {
						t.Fatalf("对端客户端读取回显失败: %v", err)
					}
					if typ != p.typ || !bytes.Equal(got, p.data) {
						t.Fatalf("回显 = (%v, %d 字节), 期望 (%v, %d 字节)", typ, len(got), p.typ, len(p.data))
					}
				})
			}

			t.Run("fragmented compressed write", func(t *testing.T) {
				// coder's streaming writer emits a compressed first frame (RSV1) plus a final empty continuation, | 对端的流式写出器会发出压缩首帧（RSV1）与末尾空续帧，
				// which exercises our first-frame-only RSV1 rule and cross-fragment inflation | 正好覆盖我们「仅首帧置 RSV1」与跨分片解压的实现
				want := []byte(strings.Repeat("分片压缩 | fragmented compression ", 50))
				w, err := c.Writer(ctx, websocket.MessageText)
				if err != nil {
					t.Fatalf("获取对端流式写出器失败: %v", err)
				}
				half := len(want) / 2
				if _, err := w.Write(want[:half]); err != nil {
					t.Fatalf("写入首段失败: %v", err)
				}
				if _, err := w.Write(want[half:]); err != nil {
					t.Fatalf("写入次段失败: %v", err)
				}
				if err := w.Close(); err != nil {
					t.Fatalf("关闭对端流式写出器失败: %v", err)
				}
				typ, got, err := c.Read(ctx)
				if err != nil {
					t.Fatalf("读取分片回显失败: %v", err)
				}
				if typ != websocket.MessageText || !bytes.Equal(got, want) {
					t.Fatalf("分片回显 = (%v, %d 字节), 期望 (text, %d 字节)", typ, len(got), len(want))
				}
			})
		})
	}
}

// startCoderCompressedFragmentedEchoServer starts a coder/websocket echo server that answers every message | startCoderCompressedFragmentedEchoServer 启动 coder/websocket 回显服务端，
// through a streaming Writer split into two chunks, so the client must reassemble compressed fragments. | 每条回显都经流式写出器分两段发出，客户端必须重组压缩分片。
func startCoderCompressedFragmentedEchoServer(t *testing.T, mode websocket.CompressionMode) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			CompressionMode:      mode,
			CompressionThreshold: 1,
		})
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		c.SetReadLimit(8 << 20)

		ctx := r.Context()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			ew, err := c.Writer(ctx, typ)
			if err != nil {
				return
			}
			if len(data) > 1 {
				half := len(data) / 2
				if _, err := ew.Write(data[:half]); err != nil {
					return
				}
				data = data[half:]
			}
			if _, err := ew.Write(data); err != nil {
				return
			}
			if err := ew.Close(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// coderOp maps coder/websocket's message type to this package's opcode. | coderOp 把 coder/websocket 的消息类型映射为本包的操作码。
func coderOp(t *testing.T, typ websocket.MessageType) OpCode {
	t.Helper()
	switch typ {
	case websocket.MessageText:
		return TextMessage
	case websocket.MessageBinary:
		return BinaryMessage
	default:
		t.Fatalf("未知的对端消息类型: %v", typ)
		return 0
	}
}

// TestCompressionInteropOurClientCoderServer drives coder/websocket's compressed server with this package's | TestCompressionInteropOurClientCoderServer 用本包的压缩客户端
// compressed client and verifies reassembly of the fragmented compressed echoes. | 驱动 coder/websocket 的压缩服务端，并校验分片压缩回显的重组。
func TestCompressionInteropOurClientCoderServer(t *testing.T) {
	cases := []struct {
		name                 string
		serverMode           websocket.CompressionMode
		clientCompression    bool
		wantClientNegotiated bool
	}{
		{name: "context takeover", serverMode: websocket.CompressionContextTakeover, clientCompression: true, wantClientNegotiated: true},
		{name: "no context takeover", serverMode: websocket.CompressionNoContextTakeover, clientCompression: true, wantClientNegotiated: true},
		{name: "server disabled", serverMode: websocket.CompressionDisabled, clientCompression: true, wantClientNegotiated: false},
		{name: "client does not offer", serverMode: websocket.CompressionContextTakeover, clientCompression: false, wantClientNegotiated: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := startCoderCompressedFragmentedEchoServer(t, tc.serverMode)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			c, err := Dial(ctx, wsURL(t, srv, "/"), DialOptions{EnableWriteCompression: tc.clientCompression})
			if err != nil {
				t.Fatalf("我方客户端 Dial 失败: %v", err)
			}
			defer func() { _ = c.CloseNow() }()

			if got := c.compression != nil; got != tc.wantClientNegotiated {
				t.Fatalf("客户端协商扩展 = %v, 期望 %v", got, tc.wantClientNegotiated)
			}

			for _, p := range compressionInteropPayloads() {
				t.Run(p.name, func(t *testing.T) {
					wantOp := coderOp(t, p.typ)
					if err := c.WriteMessage(wantOp, p.data); err != nil {
						t.Fatalf("我方客户端写入失败: %v", err)
					}
					op, got, err := c.ReadMessage()
					if err != nil {
						t.Fatalf("我方客户端读取回显失败: %v", err)
					}
					if op != wantOp || !bytes.Equal(got, p.data) {
						t.Fatalf("回显 = (%v, %d 字节), 期望 (%v, %d 字节)", op, len(got), wantOp, len(p.data))
					}
				})
			}

			// repeated large messages exercise the takeover window in both directions | 连续大消息覆盖两个方向的接管窗口
			long := []byte(strings.Repeat("双向接管 | bidirectional takeover ", 8000))
			for i := 0; i < 6; i++ {
				if err := c.WriteMessage(BinaryMessage, long); err != nil {
					t.Fatalf("第 %d 条长消息写入失败: %v", i, err)
				}
				_, got, err := c.ReadMessage()
				if err != nil {
					t.Fatalf("第 %d 条长消息读取失败: %v", i, err)
				}
				if !bytes.Equal(got, long) {
					t.Fatalf("第 %d 条长消息内容不一致", i)
				}
			}
		})
	}
}
