package ws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- Test scaffolding | 测试脚手架

// tcpPair establishes a pair of connected TCP connections and returns (server side, client side). | tcpPair 建立一对已连接的 TCP 连接，返回 (服务端侧, 客户端侧)。
//
// Real TCP is used rather than net.Pipe: TCP has kernel buffers, so a write does not block just because the peer has not read yet, | 使用真实 TCP 而非 net.Pipe：TCP 有内核缓冲，写入不会因对端尚未读取而阻塞，
// which makes deterministic "write a full frame, then read" test sequences easy to build. | 便于构造「先写完整帧、再读取」的确定性测试序列。
func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := ln.Accept()
		ch <- res{c, err}
	}()
	cli, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatalf("接受连接失败: %v", r.err)
	}
	t.Cleanup(func() {
		_ = r.c.Close()
		_ = cli.Close()
	})
	return r.c, cli
}

// connPair returns (server Conn, client Conn). | connPair 返回 (服务端 Conn, 客户端 Conn)。
func connPair(t *testing.T, o Options) (*Conn, *Conn) {
	t.Helper()
	ss, cs := tcpPair(t)
	oo := o.normalize()
	return newConn(ss, oo, false), newConn(cs, oo, true)
}

// dialPair returns (server Conn, raw client connection); the raw connection is for hand-crafting invalid frames. | dialPair 返回 (服务端 Conn, 客户端裸连接)，裸连接供手工构造非法帧。
func dialPair(t *testing.T, o Options) (*Conn, net.Conn) {
	t.Helper()
	ss, cs := tcpPair(t)
	return newConn(ss, o.normalize(), false), cs
}

// rawFrame hand-crafts a frame (with an optional RSV1 bit) for building invalid input. | rawFrame 手工构造一个帧（含可选 RSV1），用于构造非法输入。
func rawFrame(fin, rsv1 bool, op OpCode, masked bool, key [4]byte, payload []byte) []byte {
	var hdr [maxHeaderSize]byte
	n := writeFrameHeader(hdr[:], fin, op, masked, int64(len(payload)))
	out := make([]byte, 0, n+4+len(payload))
	out = append(out, hdr[:n]...)
	if masked {
		out = append(out, key[:]...)
	}
	body := append([]byte(nil), payload...)
	if masked {
		maskBytes(key, 0, body)
	}
	out = append(out, body...)
	if rsv1 {
		out[0] |= 0x40
	}
	return out
}

var testKey = [4]byte{0x37, 0xfa, 0x21, 0x3d}

// expectCloseCode reads the close frame the server sent from the client-side connection and checks the status code. | expectCloseCode 从客户端侧连接读取服务端发来的关闭帧并校验状态码。
func expectCloseCode(t *testing.T, cli *Conn) CloseCode {
	t.Helper()
	_ = cli.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err := cli.ReadMessage()
	var ce *CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("期望收到关闭帧，实际 err = %v", err)
	}
	return ce.Code
}

// ---------------------------------------------------------------- Handshake | 握手

func TestAcceptKeyRFCExample(t *testing.T) {
	// official example from RFC 6455 §1.3 | RFC 6455 §1.3 官方示例
	got := acceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	if got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("Sec-WebSocket-Accept = %q, 期望 s3pPLMBiTxaQ9kYGzzhZRbK+xOo=", got)
	}
}

func TestUpgradeEndToEndWithDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, Options{Subprotocols: []string{"yd.v1"}})
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
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
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := Dial(ctx, wsURL, DialOptions{Subprotocols: []string{"yd.v1", "other"}})
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}
	defer func() { _ = cli.CloseNow() }()

	if cli.Subprotocol() != "yd.v1" {
		t.Fatalf("子协议 = %q, 期望 yd.v1", cli.Subprotocol())
	}

	for _, msg := range []string{"hello", strings.Repeat("x", 70000)} {
		if err := cli.WriteMessage(TextMessage, []byte(msg)); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		op, got, err := cli.ReadMessage()
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if op != TextMessage || string(got) != msg {
			t.Fatalf("回显不一致: op=%v len=%d", op, len(got))
		}
	}
}

func TestUpgradeRejectsBadRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := Upgrade(w, r, Options{}); err != nil {
			return
		}
	}))
	defer srv.Close()

	base := map[string]string{
		"Connection":            "Upgrade",
		"Upgrade":               "websocket",
		"Sec-WebSocket-Version": "13",
		"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
	}

	cases := []struct {
		name   string
		method string
		mutate func(map[string]string)
		want   int
	}{
		{name: "方法非 GET", method: http.MethodPost, want: http.StatusMethodNotAllowed},
		{name: "缺少 Key", method: http.MethodGet, mutate: func(h map[string]string) { delete(h, "Sec-WebSocket-Key") }, want: http.StatusBadRequest},
		{name: "Key 非 16 字节", method: http.MethodGet, mutate: func(h map[string]string) { h["Sec-WebSocket-Key"] = "YWJj" }, want: http.StatusBadRequest},
		{name: "版本错误", method: http.MethodGet, mutate: func(h map[string]string) { h["Sec-WebSocket-Version"] = "8" }, want: http.StatusUpgradeRequired},
		{name: "缺少 Upgrade 头", method: http.MethodGet, mutate: func(h map[string]string) { delete(h, "Upgrade") }, want: http.StatusBadRequest},
		{name: "跨域 Origin", method: http.MethodGet, mutate: func(h map[string]string) { h["Origin"] = "https://evil.example.com" }, want: http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hdr := map[string]string{}
			for k, v := range base {
				hdr[k] = v
			}
			if tc.mutate != nil {
				tc.mutate(hdr)
			}
			req, err := http.NewRequest(tc.method, srv.URL, nil)
			if err != nil {
				t.Fatalf("构造请求失败: %v", err)
			}
			for k, v := range hdr {
				req.Header.Set(k, v)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("请求失败: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.want {
				t.Fatalf("状态码 = %d, 期望 %d", resp.StatusCode, tc.want)
			}
			if tc.want == http.StatusUpgradeRequired {
				if got := resp.Header.Get("Sec-WebSocket-Version"); got != "13" {
					t.Fatalf("426 响应必须带 Sec-WebSocket-Version: 13, 实际 %q", got)
				}
			}
		})
	}
}

func TestDialRejectsBadAccept(t *testing.T) {
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
		_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: WRONGACCEPTVALUE\r\n\r\n"))
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := Dial(ctx, wsURL, DialOptions{}); !errors.Is(err, ErrHandshake) {
		t.Fatalf("错误 = %v, 期望 ErrHandshake（Accept 校验必须生效）", err)
	}
}

func TestUpgradeOnHTTP2ReturnsTypedError(t *testing.T) {
	// when the ResponseWriter does not implement Hijacker (HTTP/2 / HTTP/3 semantics) a clear error must be returned, | ResponseWriter 不实现 Hijacker（HTTP/2 / HTTP/3 语义）时必须返回明确错误，
	// rather than failing silently or panicking. | 而不是静默失败或 panic。
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	_, err := Upgrade(nonHijackWriter{}, req, Options{})
	if !errors.Is(err, ErrHijackUnsupported) {
		t.Fatalf("错误 = %v, 期望 ErrHijackUnsupported", err)
	}
}

type nonHijackWriter struct{ h http.Header }

func (w nonHijackWriter) Header() http.Header {
	if w.h == nil {
		return http.Header{}
	}
	return w.h
}
func (w nonHijackWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w nonHijackWriter) WriteHeader(int)             {}

// ---------------------------------------------------------------- Protocol violations | 协议违规

func TestReadUnmaskedClientFrameFails1002(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	if _, err := cli.conn.Write(rawFrame(true, false, TextMessage, false, testKey, []byte("hi"))); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}
	if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrMaskMismatch) {
		t.Fatalf("服务端错误 = %v, 期望 ErrMaskMismatch", err)
	}
	if code := expectCloseCode(t, cli); code != CloseProtocolError {
		t.Fatalf("关闭码 = %d, 期望 1002", code)
	}
}

func TestOversizedMessageFails1009(t *testing.T) {
	srv, cli := connPair(t, Options{MaxMessageSize: 64})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	big := make([]byte, 128)
	if _, err := cli.conn.Write(rawFrame(true, false, BinaryMessage, true, testKey, big)); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}
	if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrMessageTooBig) {
		t.Fatalf("服务端错误 = %v, 期望 ErrMessageTooBig", err)
	}
	if code := expectCloseCode(t, cli); code != CloseMessageTooBig {
		t.Fatalf("关闭码 = %d, 期望 1009", code)
	}
}

func TestInvalidUTF8TextFails1007(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	if _, err := cli.conn.Write(rawFrame(true, false, TextMessage, true, testKey, []byte{'h', 'i', 0xff, 0xfe})); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}
	if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("服务端错误 = %v, 期望 ErrInvalidUTF8", err)
	}
	if code := expectCloseCode(t, cli); code != CloseInvalidFramePayloadData {
		t.Fatalf("关闭码 = %d, 期望 1007", code)
	}
}

func TestMultiByteRuneSplitAcrossFramesIsValid(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	// "中" = E4 B8 AD: the first two bytes go into the first frame and the last byte into a continuation frame | "中" = E4 B8 AD，前两个字节放第一帧，末字节放续帧
	part1 := []byte{0xE4, 0xB8}
	part2 := []byte{0xAD, '!'}
	var wire []byte
	wire = append(wire, rawFrame(false, false, TextMessage, true, testKey, part1)...)
	wire = append(wire, rawFrame(true, false, ContinuationMessage, true, testKey, part2)...)
	if _, err := cli.conn.Write(wire); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}

	op, data, err := srv.ReadMessage()
	if err != nil {
		t.Fatalf("跨帧多字节字符不应判非法: %v", err)
	}
	if op != TextMessage || string(data) != "中!" {
		t.Fatalf("消息 = %q (op=%v)", data, op)
	}
}

func TestProtocolViolationsTable(t *testing.T) {
	cases := []struct {
		name  string
		write func(cli *Conn) error
		want  error
	}{
		{
			name: "控制帧载荷超 125",
			write: func(cli *Conn) error {
				_, err := cli.conn.Write(rawFrame(true, false, PingMessage, true, testKey, make([]byte, 126)))
				return err
			},
			want: ErrControlTooLarge,
		},
		{
			name: "控制帧分片",
			write: func(cli *Conn) error {
				_, err := cli.conn.Write(rawFrame(false, false, PingMessage, true, testKey, []byte("x")))
				return err
			},
			want: ErrFragmentedControl,
		},
		{
			name: "未协商却设置 RSV1",
			write: func(cli *Conn) error {
				_, err := cli.conn.Write(rawFrame(true, true, TextMessage, true, testKey, []byte("x")))
				return err
			},
			want: ErrReservedBits,
		},
		{
			name: "保留操作码 0x3",
			write: func(cli *Conn) error {
				_, err := cli.conn.Write(rawFrame(true, false, OpCode(0x3), true, testKey, []byte("x")))
				return err
			},
			want: ErrBadOpCode,
		},
		{
			name: "无起始帧的续帧",
			write: func(cli *Conn) error {
				_, err := cli.conn.Write(rawFrame(true, false, ContinuationMessage, true, testKey, []byte("x")))
				return err
			},
			want: ErrContinuationWithoutStart,
		},
		{
			name: "分片未结束即开新消息",
			write: func(cli *Conn) error {
				var wire []byte
				wire = append(wire, rawFrame(false, false, TextMessage, true, testKey, []byte("a"))...)
				wire = append(wire, rawFrame(true, false, TextMessage, true, testKey, []byte("b"))...)
				_, err := cli.conn.Write(wire)
				return err
			},
			want: ErrNewDataDuringFragment,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, cli := connPair(t, Options{})
			defer func() { _ = srv.CloseNow() }()
			defer func() { _ = cli.CloseNow() }()

			if err := tc.write(cli); err != nil {
				t.Fatalf("写入原始帧失败: %v", err)
			}
			if _, _, err := srv.ReadMessage(); !errors.Is(err, tc.want) {
				t.Fatalf("错误 = %v, 期望 %v", err, tc.want)
			}
			if code := expectCloseCode(t, cli); code != CloseProtocolError {
				t.Fatalf("关闭码 = %d, 期望 1002", code)
			}
		})
	}
}

// ---------------------------------------------------------------- Fragmentation and control frames | 分片与控制帧

func TestFragmentedMessageWithInterleavedPing(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	var wire []byte
	wire = append(wire, rawFrame(false, false, TextMessage, true, testKey, []byte("Hel"))...)
	wire = append(wire, rawFrame(true, false, PingMessage, true, testKey, []byte("hb"))...)
	wire = append(wire, rawFrame(true, false, ContinuationMessage, true, testKey, []byte("lo"))...)
	if _, err := cli.conn.Write(wire); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}

	op, data, err := srv.ReadMessage()
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if op != TextMessage || string(data) != "Hello" {
		t.Fatalf("消息 = %q (op=%v), 期望 Hello", data, op)
	}

	// the server should have replied with a pong automatically (raw frame: 0x8A + length + "hb") | 服务端应已自动回复 pong（原始帧：0x8A + 长度 + "hb"）
	_ = cli.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var head [2]byte
	if _, err := io.ReadFull(cli.conn, head[:]); err != nil {
		t.Fatalf("读取自动 pong 失败: %v", err)
	}
	if OpCode(head[0]&0x0f) != PongMessage {
		t.Fatalf("自动回复的操作码 = %v, 期望 pong", OpCode(head[0]&0x0f))
	}
	payload := make([]byte, head[1]&0x7f)
	if _, err := io.ReadFull(cli.conn, payload); err != nil {
		t.Fatalf("读取 pong 载荷失败: %v", err)
	}
	if string(payload) != "hb" {
		t.Fatalf("pong 载荷 = %q, 期望 hb", payload)
	}
}

func TestPingAndPongHandlers(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	var pinged string
	srv.SetPingHandler(func(s string) error {
		pinged = s
		return nil // handled here, so no automatic pong is sent | 自行处理后不再自动回复 pong
	})

	// control frames are handled while reading onward toward the next data message, | 控制帧在「寻找下一条数据消息」的读取过程中被处理，
	// so one more data message is sent afterwards, letting the server consume the ping in between. | 因此再补一条数据消息，让服务端把中间的 ping 读掉。
	if err := cli.WriteMessage(TextMessage, []byte("before")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := cli.WriteControl(PingMessage, []byte("beat"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("发送 ping 失败: %v", err)
	}
	if err := cli.WriteMessage(TextMessage, []byte("after")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	if _, _, err := srv.ReadMessage(); err != nil {
		t.Fatalf("读取第一条消息失败: %v", err)
	}
	if _, _, err := srv.ReadMessage(); err != nil {
		t.Fatalf("读取第二条消息失败: %v", err)
	}
	if pinged != "beat" {
		t.Fatalf("ping 回调 = %q, 期望 beat", pinged)
	}
}

func TestCloseHandshakeEchoesCode(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	if err := cli.WriteControl(CloseMessage, closePayload(CloseGoingAway, "bye"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("发送关闭帧失败: %v", err)
	}

	_, _, err := srv.ReadMessage()
	var ce *CloseError
	if !errors.As(err, &ce) || ce.Code != CloseGoingAway || ce.Text != "bye" {
		t.Fatalf("服务端关闭错误 = %v", err)
	}

	_ = cli.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := cli.ReadMessage(); !errors.As(err, &ce) || ce.Code != CloseGoingAway {
		t.Fatalf("客户端应收到相同状态码的回送, err = %v", err)
	}
}

func TestWriteMessageAfterCloseReturnsErrCloseSent(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	if err := cli.WriteControl(CloseMessage, closePayload(CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("发送关闭帧失败: %v", err)
	}
	if err := cli.WriteMessage(TextMessage, []byte("x")); !errors.Is(err, ErrCloseSent) {
		t.Fatalf("错误 = %v, 期望 ErrCloseSent", err)
	}
}

// ---------------------------------------------------------------- Memory and resources | 内存与资源

func TestReadMessageReusesBuffer(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	send := func(msg string) {
		t.Helper()
		if err := cli.WriteMessage(BinaryMessage, []byte(msg)); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		_, got, err := srv.ReadMessage()
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if string(got) != msg {
			t.Fatalf("消息 = %q, 期望 %q", got, msg)
		}
	}

	// warm up once so the internal buffer gets allocated | 预热一次，让内部缓冲分配到位
	send("warm-up-payload-0000000000")

	// the next two reads must reuse the same underlying buffer (same address for the first byte) | 随后两次读取必须复用同一底层缓冲（首字节地址一致）
	if err := cli.WriteMessage(BinaryMessage, []byte("aaaaaaaaaa")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	_, b1, err := srv.ReadMessage()
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if err := cli.WriteMessage(BinaryMessage, []byte("bbbbbbbbbb")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	_, b2, err := srv.ReadMessage()
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if &b1[0] != &b2[0] {
		t.Fatal("ReadMessage did not reuse the internal buffer (every read re-allocates) | ReadMessage 未复用内部缓冲（每次读取都重新分配）")
	}
}

func TestNoGoroutineLeakAfterConnectCloseCycles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, Options{})
		if err != nil {
			return
		}
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				break
			}
		}
		_ = c.CloseNow()
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx := context.Background()

	baseline := runtime.NumGoroutine()
	for i := 0; i < 25; i++ {
		c, err := Dial(ctx, wsURL, DialOptions{HandshakeTimeout: 3 * time.Second})
		if err != nil {
			t.Fatalf("第 %d 次 Dial 失败: %v", i, err)
		}
		if err := c.WriteMessage(TextMessage, []byte("x")); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, _ = c.ReadMessage() // the server closes after reading EOF, so the client read reports an error | 服务端读到 EOF 后关闭，客户端读取会报错
		_ = c.CloseNow()
	}
	srv.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+3 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("疑似 goroutine 泄漏：baseline=%d 当前=%d", baseline, runtime.NumGoroutine())
}

func TestConcurrentWritersAreSerialized(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	const writers, perWriter = 8, 20
	done := make(chan error, writers)
	for w := 0; w < writers; w++ {
		go func(id int) {
			payload := []byte(fmt.Sprintf("writer-%d-", id) + strings.Repeat("z", 200))
			for i := 0; i < perWriter; i++ {
				if err := cli.WriteMessage(BinaryMessage, payload); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}(w)
	}

	received := 0
	for received < writers*perWriter {
		_ = srv.SetReadDeadline(time.Now().Add(10 * time.Second))
		op, data, err := srv.ReadMessage()
		if err != nil {
			t.Fatalf("读取第 %d 条消息失败: %v", received, err)
		}
		if op != BinaryMessage || len(data) != 209 {
			t.Fatalf("消息被撕裂：op=%v len=%d", op, len(data))
		}
		received++
	}
	for w := 0; w < writers; w++ {
		if err := <-done; err != nil {
			t.Fatalf("写协程失败: %v", err)
		}
	}
}

func TestUpgradeRequiresGETAndOriginPolicy(t *testing.T) {
	custom := func(r *http.Request) bool { return r.Header.Get("Origin") == "https://allowed.example.com" }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, Options{CheckOrigin: custom})
		if err != nil {
			return
		}
		_ = c.CloseNow()
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Origin", "https://denied.example.com")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("自定义 Origin 策略应拒绝，实际 %d", resp.StatusCode)
	}
}

// TestProtocolNamesAreBilingual pins that user-visible protocol names and close
// errors keep the English | Chinese format, including unknown values. | TestProtocolNamesAreBilingual
// 固定「面向用户的协议名称与关闭错误保持 English | 中文 格式」，未知取值也不例外。
func TestProtocolNamesAreBilingual(t *testing.T) {
	check := func(name, got string) {
		t.Helper()
		sep := strings.Index(got, " | ")
		if sep <= 0 || sep == len(got)-3 {
			t.Fatalf("%s = %q, want English | Chinese | %s = %q，期望 English | 中文", name, got, name, got)
		}
		if !strings.Contains(got[sep+3:], " ") && !containsNonASCII(got[sep+3:]) {
			t.Fatalf("%s = %q, Chinese half is missing | %s = %q，缺少中文", name, got, name, got)
		}
	}

	for _, op := range []OpCode{ContinuationMessage, TextMessage, BinaryMessage, CloseMessage, PingMessage, PongMessage, OpCode(0x3)} {
		check("OpCode.String", op.String())
	}
	for _, code := range []CloseCode{CloseNormalClosure, CloseGoingAway, CloseProtocolError, CloseUnsupportedData,
		CloseNoStatusReceived, CloseAbnormalClosure, CloseInvalidFramePayloadData, ClosePolicyViolation,
		CloseMessageTooBig, CloseMandatoryExtension, CloseInternalServerErr, CloseServiceRestart,
		CloseTryAgainLater, CloseTLSHandshake, CloseCode(9999)} {
		check("CloseCode.String", code.String())
	}
	check("CloseError.Error", (&CloseError{Code: CloseNormalClosure}).Error())
	check("CloseError.Error", (&CloseError{Code: ClosePolicyViolation, Text: "policy"}).Error())
}

// containsNonASCII reports whether s contains a non-ASCII byte. | containsNonASCII 报告 s 是否包含非 ASCII 字节。
func containsNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}
