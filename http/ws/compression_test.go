package ws

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/flate"
)

// ---------------------------------------------------------------- Helpers | 辅助函数

// mustDeflate compresses p as one permessage-deflate message and strips the RFC 7692 tail. | mustDeflate 把 p 压缩为一条 permessage-deflate 消息并剥离 RFC 7692 尾部。
//
// It is deliberately independent of messageCompressor so the tests do not certify themselves. | 它刻意不依赖 messageCompressor，避免测试自证。
func mustDeflate(t *testing.T, p []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestSpeed)
	if err != nil {
		t.Fatalf("创建测试压缩器失败: %v", err)
	}
	if _, err := w.Write(p); err != nil {
		t.Fatalf("压缩测试数据失败: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("刷出测试压缩数据失败: %v", err)
	}
	out := buf.Bytes()
	if len(out) < len(deflateTail) || !bytes.HasSuffix(out, deflateTail[:]) {
		t.Fatalf("测试压缩输出未以 RFC 7692 尾部结束: %x", out)
	}
	return append([]byte(nil), out[:len(out)-len(deflateTail)]...)
}

// mustInflate decompresses one permessage-deflate payload by appending the RFC 7692 tail back. | mustInflate 通过补回 RFC 7692 尾部来解压一条 permessage-deflate 载荷。
func mustInflate(t *testing.T, p []byte) []byte {
	t.Helper()
	wire := make([]byte, 0, len(p)+len(deflateTail))
	wire = append(wire, p...)
	wire = append(wire, deflateTail[:]...)
	r := flate.NewReader(bytes.NewReader(wire))
	defer func() { _ = r.Close() }()
	got, err := io.ReadAll(r)
	if err != nil && err != io.ErrUnexpectedEOF {
		t.Fatalf("解压测试载荷失败: %v", err)
	}
	return got
}

// compressionPair returns a server/client pair with the same negotiated configuration installed. | compressionPair 返回一对已装好同一协商配置的服务端/客户端连接。
func compressionPair(t *testing.T, o Options, cfg *compressionConfig) (*Conn, *Conn) {
	t.Helper()
	srv, cli := connPair(t, o)
	srv.setCompression(cfg)
	cli.setCompression(cfg)
	t.Cleanup(func() {
		_ = srv.CloseNow()
		_ = cli.CloseNow()
	})
	return srv, cli
}

// ---------------------------------------------------------------- Negotiation | 协商

func TestServerCompressionNegotiationTable(t *testing.T) {
	cases := []struct {
		name            string
		offer           string
		wantNil         bool
		wantReply       string
		wantClientBits  int
		wantServerBits  int
		wantClientNoCtx bool
		wantServerNoCtx bool
	}{
		{
			name: "bare offer", offer: "permessage-deflate",
			wantReply: "permessage-deflate", wantClientBits: 15, wantServerBits: 15,
		},
		{
			name: "bare client_max_window_bits", offer: "permessage-deflate; client_max_window_bits",
			wantReply: "permessage-deflate; client_max_window_bits=15", wantClientBits: 15, wantServerBits: 15,
		},
		{
			name: "client_max_window_bits=10", offer: "permessage-deflate; client_max_window_bits=10",
			wantReply: "permessage-deflate; client_max_window_bits=10", wantClientBits: 10, wantServerBits: 15,
		},
		{
			name: "server_max_window_bits=12", offer: "permessage-deflate; server_max_window_bits=12",
			wantReply: "permessage-deflate; server_max_window_bits=12", wantClientBits: 15, wantServerBits: 12,
		},
		{
			name: "both no-context-takeover", offer: "permessage-deflate; client_no_context_takeover; server_no_context_takeover",
			wantReply:      "permessage-deflate; client_no_context_takeover; server_no_context_takeover",
			wantClientBits: 15, wantServerBits: 15, wantClientNoCtx: true, wantServerNoCtx: true,
		},
		{
			name: "quoted window value", offer: `permessage-deflate; client_max_window_bits="9"`,
			wantReply: "permessage-deflate; client_max_window_bits=9", wantClientBits: 9, wantServerBits: 15,
		},
		{
			name: "case-insensitive names", offer: "PerMessage-Deflate; Client_Max_Window_Bits=11",
			wantReply: "permessage-deflate; client_max_window_bits=11", wantClientBits: 11, wantServerBits: 15,
		},
		{
			name: "second offer accepted", offer: "x-webkit-deflate-frame, permessage-deflate; server_max_window_bits=10",
			wantReply: "permessage-deflate; server_max_window_bits=10", wantClientBits: 15, wantServerBits: 10,
		},
		{
			name: "invalid first offer then valid", offer: "permessage-deflate; server_max_window_bits=99, permessage-deflate; client_max_window_bits=8",
			wantReply: "permessage-deflate; client_max_window_bits=8", wantClientBits: 8, wantServerBits: 15,
		},
		{name: "unknown parameter", offer: "permessage-deflate; foo=bar", wantNil: true},
		{name: "duplicate parameter", offer: "permessage-deflate; server_max_window_bits=10; server_max_window_bits=11", wantNil: true},
		{name: "bare server_max_window_bits", offer: "permessage-deflate; server_max_window_bits", wantNil: true},
		{name: "window too large", offer: "permessage-deflate; server_max_window_bits=16", wantNil: true},
		{name: "window too small", offer: "permessage-deflate; client_max_window_bits=7", wantNil: true},
		{name: "non-numeric window", offer: "permessage-deflate; client_max_window_bits=abc", wantNil: true},
		{name: "valued no-context-takeover", offer: "permessage-deflate; server_no_context_takeover=1", wantNil: true},
		{name: "unrelated extension", offer: "x-custom", wantNil: true},
		{name: "empty header", offer: "", wantNil: true},
		{name: "unbalanced quote", offer: `permessage-deflate; client_max_window_bits="10`, wantNil: true},
		{name: "empty parameter value", offer: "permessage-deflate; client_max_window_bits=", wantNil: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.offer != "" {
				h.Set("Sec-WebSocket-Extensions", tc.offer)
			}
			cfg, reply := negotiateServerCompression(h)
			if tc.wantNil {
				if cfg != nil || reply != "" {
					t.Fatalf("应拒绝提议，实际 cfg=%+v reply=%q", cfg, reply)
				}
				return
			}
			if cfg == nil {
				t.Fatalf("应接受提议 %q，实际拒绝", tc.offer)
			}
			if reply != tc.wantReply {
				t.Fatalf("响应取值 = %q, 期望 %q", reply, tc.wantReply)
			}
			if cfg.clientWindowBits != tc.wantClientBits || cfg.serverWindowBits != tc.wantServerBits {
				t.Fatalf("窗口 = (client %d, server %d), 期望 (%d, %d)",
					cfg.clientWindowBits, cfg.serverWindowBits, tc.wantClientBits, tc.wantServerBits)
			}
			if cfg.clientNoContextTakeover != tc.wantClientNoCtx || cfg.serverNoContextTakeover != tc.wantServerNoCtx {
				t.Fatalf("no-context-takeover = (client %v, server %v), 期望 (%v, %v)",
					cfg.clientNoContextTakeover, cfg.serverNoContextTakeover, tc.wantClientNoCtx, tc.wantServerNoCtx)
			}
		})
	}
}

func TestClientCompressionNegotiationTable(t *testing.T) {
	cases := []struct {
		name            string
		offered         bool
		response        string
		wantErrs        []error
		wantNil         bool
		wantClientBits  int
		wantServerBits  int
		wantClientNoCtx bool
		wantServerNoCtx bool
	}{
		{name: "not offered, no response", offered: false, response: "", wantNil: true},
		{name: "not offered, extension activated", offered: false, response: "permessage-deflate", wantErrs: []error{ErrHandshake, ErrUnrequestedExtension}},
		{name: "not offered, foreign extension", offered: false, response: "x-custom", wantErrs: []error{ErrHandshake, ErrUnrequestedExtension}},
		{name: "offered but declined", offered: true, response: "", wantNil: true},
		{
			name: "offered and accepted", offered: true, response: "permessage-deflate",
			wantClientBits: 15, wantServerBits: 15,
		},
		{
			name: "no-context-takeover echoed", offered: true,
			response:       "permessage-deflate; client_no_context_takeover; server_no_context_takeover",
			wantClientBits: 15, wantServerBits: 15, wantClientNoCtx: true, wantServerNoCtx: true,
		},
		{
			name: "window values", offered: true,
			response:       "permessage-deflate; server_max_window_bits=10; client_max_window_bits=9",
			wantClientBits: 9, wantServerBits: 10,
		},
		{
			name: "bare server_max_window_bits tolerated", offered: true,
			response:       "permessage-deflate; server_max_window_bits",
			wantClientBits: 15, wantServerBits: 15,
		},
		{name: "bare client_max_window_bits rejected", offered: true, response: "permessage-deflate; client_max_window_bits", wantErrs: []error{ErrHandshake}},
		{name: "client window too large", offered: true, response: "permessage-deflate; client_max_window_bits=16", wantErrs: []error{ErrHandshake}},
		{name: "server window too small", offered: true, response: "permessage-deflate; server_max_window_bits=7", wantErrs: []error{ErrHandshake}},
		{name: "unknown parameter", offered: true, response: "permessage-deflate; foo=bar", wantErrs: []error{ErrHandshake}},
		{name: "duplicate parameter", offered: true, response: "permessage-deflate; client_no_context_takeover; client_no_context_takeover", wantErrs: []error{ErrHandshake}},
		{name: "two extension entries", offered: true, response: "permessage-deflate, permessage-deflate", wantErrs: []error{ErrHandshake}},
		{name: "wrong extension", offered: true, response: "x-custom", wantErrs: []error{ErrHandshake, ErrUnrequestedExtension}},
		{name: "valued no-context-takeover", offered: true, response: "permessage-deflate; server_no_context_takeover=1", wantErrs: []error{ErrHandshake}},
		{name: "malformed parameter", offered: true, response: "permessage-deflate; =x", wantErrs: []error{ErrHandshake}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.response != "" {
				h.Set("Sec-WebSocket-Extensions", tc.response)
			}
			cfg, err := negotiateClientCompression(h, tc.offered)
			for _, want := range tc.wantErrs {
				if !errors.Is(err, want) {
					t.Fatalf("错误 = %v, 期望包含 %v", err, want)
				}
			}
			if len(tc.wantErrs) > 0 {
				if cfg != nil {
					t.Fatalf("握手失败时不应返回配置，实际 %+v", cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应失败: %v", err)
			}
			if tc.wantNil {
				if cfg != nil {
					t.Fatalf("应不启用压缩，实际 %+v", cfg)
				}
				return
			}
			if cfg == nil {
				t.Fatal("compression must be enabled | 应启用压缩，实际未启用")
			}
			if cfg.clientWindowBits != tc.wantClientBits || cfg.serverWindowBits != tc.wantServerBits {
				t.Fatalf("窗口 = (client %d, server %d), 期望 (%d, %d)",
					cfg.clientWindowBits, cfg.serverWindowBits, tc.wantClientBits, tc.wantServerBits)
			}
			if cfg.clientNoContextTakeover != tc.wantClientNoCtx || cfg.serverNoContextTakeover != tc.wantServerNoCtx {
				t.Fatalf("no-context-takeover = (client %v, server %v), 期望 (%v, %v)",
					cfg.clientNoContextTakeover, cfg.serverNoContextTakeover, tc.wantClientNoCtx, tc.wantServerNoCtx)
			}
		})
	}
}

// ---------------------------------------------------------------- Wire format | 线缆格式

// TestWritePathCompressionAndRSV1 checks that outbound compressed messages set RSV1 on their only data frame, | TestWritePathCompressionAndRSV1 校验出站压缩消息只在唯一数据帧上置 RSV1，
// while control frames never carry it. | 而控制帧从不携带该位。
func TestWritePathCompressionAndRSV1(t *testing.T) {
	srv, raw := dialPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = raw.Close() }()
	// no-context-takeover on the encode side keeps every message independently decodable, | 编码侧不接管上下文，使每条消息都可独立解码，
	// which lets mustInflate decode each captured frame without replaying earlier messages | 从而让 mustInflate 无需重放先前消息即可解码每个抓到的帧
	srv.setCompression(&compressionConfig{
		clientWindowBits: 15, serverWindowBits: 15, serverNoContextTakeover: true,
	})

	cases := []struct {
		name string
		op   OpCode
		data []byte
	}{
		{name: "empty text", op: TextMessage, data: nil},
		{name: "small text", op: TextMessage, data: []byte("hello")},
		{name: "chinese text", op: TextMessage, data: []byte("中文消息 | chinese message")},
		{name: "binary", op: BinaryMessage, data: []byte{0x00, 0x01, 0xfe, 0xff}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := srv.WriteMessage(tc.op, tc.data); err != nil {
				t.Fatalf("写入失败: %v", err)
			}
			h, body := readRawFrame(t, raw)
			if !h.rsv1 {
				t.Fatal("compressed data frames must set RSV1 | 压缩数据帧必须置 RSV1")
			}
			if h.opcode != tc.op || !h.fin {
				t.Fatalf("帧头 = (op %v, fin %v), 期望 (%v, true)", h.opcode, h.fin, tc.op)
			}
			if got := mustInflate(t, body); !bytes.Equal(got, tc.data) {
				t.Fatalf("解压结果 = %q, 期望 %q", got, tc.data)
			}
		})
	}

	t.Run("control frame never compressed", func(t *testing.T) {
		if err := srv.WriteControl(PingMessage, []byte("hb"), time.Now().Add(time.Second)); err != nil {
			t.Fatalf("发送 ping 失败: %v", err)
		}
		h, body := readRawFrame(t, raw)
		if h.rsv1 {
			t.Fatal("control frames must never set RSV1 | 控制帧不得置 RSV1")
		}
		if h.opcode != PingMessage || string(body) != "hb" {
			t.Fatalf("控制帧 = (op %v, body %q), 期望 (ping, hb)", h.opcode, body)
		}
	})
}

// TestCompressedFragmentedMessageWithInterleavedPing feeds a compressed message split over two frames, | TestCompressedFragmentedMessageWithInterleavedPing 输入跨两帧压缩消息，
// with a ping in between, and checks that RSV1 lives only on the first frame. | 中间夹一个 ping，并校验 RSV1 只出现在首帧。
func TestCompressedFragmentedMessageWithInterleavedPing(t *testing.T) {
	srv, cli := compressionPair(t, Options{}, newCompressionConfig())

	want := "分片压缩消息 | fragmented compressed message " + strings.Repeat("abc", 200)
	compressed := mustDeflate(t, []byte(want))
	mid := len(compressed) / 2

	var wire []byte
	wire = append(wire, rawFrameWithBits(false, 0x40, TextMessage, compressed[:mid])...)
	wire = append(wire, rawFrameWithBits(true, 0, PingMessage, []byte("hb"))...)
	wire = append(wire, rawFrameWithBits(true, 0, ContinuationMessage, compressed[mid:])...)
	if _, err := cli.conn.Write(wire); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}

	op, got, err := srv.ReadMessage()
	if err != nil {
		t.Fatalf("读取压缩分片消息失败: %v", err)
	}
	if op != TextMessage || string(got) != want {
		t.Fatalf("消息 = (%v, %d 字节), 期望 (text, %d 字节)", op, len(got), len(want))
	}

	// the server answers the interleaved ping on its read path | 服务端会在读路径上应答穿插的 ping
	h, body := readRawFrame(t, cli.conn)
	if h.opcode != PongMessage || string(body) != "hb" {
		t.Fatalf("自动 pong = (op %v, body %q), 期望 (pong, hb)", h.opcode, body)
	}
}

// TestCompressionRSV1ViolationsTable covers the RSV1 rules that remain protocol errors after negotiation. | TestCompressionRSV1ViolationsTable 覆盖协商之后仍属协议错误的 RSV1 规则。
func TestCompressionRSV1ViolationsTable(t *testing.T) {
	cases := []struct {
		name  string
		frame []byte
	}{
		{
			name:  "RSV1 on control frame",
			frame: rawFrameWithBits(true, 0x40, PingMessage, []byte("x")),
		},
		{
			name: "RSV1 on continuation frame",
			frame: func() []byte {
				compressed := mustDeflate(t, []byte("hello"))
				mid := len(compressed) / 2
				var wire []byte
				wire = append(wire, rawFrameWithBits(false, 0x40, TextMessage, compressed[:mid])...)
				wire = append(wire, rawFrameWithBits(true, 0x40, ContinuationMessage, compressed[mid:])...)
				return wire
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, cli := compressionPair(t, Options{}, newCompressionConfig())
			if _, err := cli.conn.Write(tc.frame); err != nil {
				t.Fatalf("写入原始帧失败: %v", err)
			}
			if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrReservedBits) {
				t.Fatalf("错误 = %v, 期望 ErrReservedBits", err)
			}
			if code := expectCloseCode(t, cli); code != CloseProtocolError {
				t.Fatalf("关闭码 = %d, 期望 1002", code)
			}
		})
	}
}

// TestRSV1WithoutNegotiationStillFails1002 preserves the RFC 6455 §5.2 rule even when the payload is a | TestRSV1WithoutNegotiationStillFails1002 在载荷本身是合法压缩数据时，
// valid compressed message: without negotiation the bit is still a protocol error. | 依旧保留 RFC 6455 §5.2 规则：未协商时该位仍是协议错误。
func TestRSV1WithoutNegotiationStillFails1002(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()
	// deliberately do not call setCompression on either side | 刻意不在任何一侧调用 setCompression

	payload := mustDeflate(t, []byte("valid compressed payload"))
	if _, err := cli.conn.Write(rawFrameWithBits(true, 0x40, TextMessage, payload)); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}
	if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrReservedBits) {
		t.Fatalf("错误 = %v, 期望 ErrReservedBits", err)
	}
	if code := expectCloseCode(t, cli); code != CloseProtocolError {
		t.Fatalf("关闭码 = %d, 期望 1002", code)
	}
}

// ---------------------------------------------------------------- Decompression safety | 解压安全

// recordingConn captures every byte written through it, so a close frame can still be asserted | recordingConn 捕获写入它的每一个字节，因此即便对端 TCP 栈因中止式关闭
// after the peer's TCP stack has discarded it in an abortive close. | 丢弃了关闭帧，仍可对其校验。
type recordingConn struct {
	net.Conn
	mu  sync.Mutex
	out bytes.Buffer
}

// Write records p and forwards it to the wrapped connection. | Write 记录 p 并转发给被包装的连接。
func (r *recordingConn) Write(p []byte) (int, error) {
	r.mu.Lock()
	_, _ = r.out.Write(p)
	r.mu.Unlock()
	return r.Conn.Write(p)
}

// recorded returns a copy of everything written so far. | recorded 返回目前已写入字节的副本。
func (r *recordingConn) recorded() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.out.Bytes()...)
}

// TestDecompressionBombFails1009 proves that a high-ratio payload is stopped by the output limit: | TestDecompressionBombFails1009 证明高压缩比载荷会被输出上限拦住：
// the read fails with ErrMessageTooBig, a 1009 close frame is emitted, and the read buffer never grows | 读取以 ErrMessageTooBig 失败、发出 1009 关闭帧，且读缓冲远未增长到炸弹体积。
// anywhere near the bomb size. The close frame is checked on the write side because the server aborts | 关闭帧在写侧校验：服务端中止时压缩输入尚未读完，Windows 会让随后的 RST 丢弃它。
// with part of the compressed input unread, and Windows lets the resulting RST discard the frame.
func TestDecompressionBombFails1009(t *testing.T) {
	const limit = 64 << 10
	ss, cs := tcpPair(t)
	rec := &recordingConn{Conn: ss}
	srv := newConn(rec, Options{MaxMessageSize: limit}.normalize(), false)
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cs.Close() }()
	srv.setCompression(newCompressionConfig())

	bomb := bytes.Repeat([]byte("a"), 4<<20) // 4 MiB inflating from tens of KiB | 4 MiB 由数十 KiB 膨胀而来
	compressed := mustDeflate(t, bomb)
	if ratio := len(bomb) / len(compressed); ratio < 100 {
		t.Fatalf("测试载荷压缩比仅 %d:1，不足以构成解压炸弹", ratio)
	}
	if len(compressed) >= limit {
		// the frame-length precheck must not be what fires here; only inflation may exceed the cap | 触发点必须不是帧长预检，只能解压本身超限
		t.Fatalf("压缩后 %d 字节已超过上限 %d，测试无法证明解压限制生效", len(compressed), limit)
	}
	if _, err := cs.Write(rawFrameWithBits(true, 0x40, BinaryMessage, compressed)); err != nil {
		t.Fatalf("写入压缩炸弹失败: %v", err)
	}

	_, _, err := srv.ReadMessage()
	if !errors.Is(err, ErrMessageTooBig) {
		t.Fatalf("错误 = %v, 期望 ErrMessageTooBig", err)
	}
	// growth is geometric with a small constant, so the buffer stays near a few times the cap; | 缓冲按几何级数增长且常数很小，因此只会略高于上限；
	// buffering the whole 4 MiB bomb would fail this assertion | 若把整个 4 MiB 炸弹缓冲下来，此断言必然失败
	if cap(srv.readBuf) >= 512<<10 {
		t.Fatalf("读缓冲膨胀到 %d 字节，解压输出限制未生效", cap(srv.readBuf))
	}

	sent := rec.recorded()
	var hdr [8]byte
	rd := bytes.NewReader(sent)
	fr, err := readFrameHeader(rd, hdr[:])
	if err != nil {
		t.Fatalf("解析服务端写出的关闭帧失败: %v", err)
	}
	if fr.opcode != CloseMessage {
		t.Fatalf("关闭帧操作码 = %v, 期望 close", fr.opcode)
	}
	payload := make([]byte, fr.length)
	if _, err := io.ReadFull(rd, payload); err != nil {
		t.Fatalf("读取服务端关闭载荷失败: %v", err)
	}
	code, _, perr := parseClosePayload(payload)
	if perr != nil || code != CloseMessageTooBig {
		t.Fatalf("关闭 = (%d, %v), 期望 1009", code, perr)
	}
}

// TestCorruptCompressedPayloadFails1007 checks that undecodable DEFLATE data closes with 1007. | TestCorruptCompressedPayloadFails1007 校验无法解码的 DEFLATE 数据以 1007 关闭。
func TestCorruptCompressedPayloadFails1007(t *testing.T) {
	srv, cli := compressionPair(t, Options{}, newCompressionConfig())

	// 0xff starts a final block with the reserved BTYPE=3, which inflate must reject | 0xff 表示 BFINAL=1 且 BTYPE=3（保留值），解压器必须拒绝
	corrupt := []byte{0xff, 0xff, 0xff, 0xff}
	if _, err := cli.conn.Write(rawFrameWithBits(true, 0x40, BinaryMessage, corrupt)); err != nil {
		t.Fatalf("写入损坏载荷失败: %v", err)
	}
	if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrInvalidCompressedData) {
		t.Fatalf("错误 = %v, 期望 ErrInvalidCompressedData", err)
	}
	if code := expectCloseCode(t, cli); code != CloseInvalidFramePayloadData {
		t.Fatalf("关闭码 = %d, 期望 1007", code)
	}
}

// TestCompressedTextInvalidUTF8Fails1007 validates text after inflation, not before. | TestCompressedTextInvalidUTF8Fails1007 校验的是解压之后的文本，而非压缩字节。
func TestCompressedTextInvalidUTF8Fails1007(t *testing.T) {
	srv, cli := compressionPair(t, Options{}, newCompressionConfig())

	payload := mustDeflate(t, []byte{0xff, 0xfe})
	if _, err := cli.conn.Write(rawFrameWithBits(true, 0x40, TextMessage, payload)); err != nil {
		t.Fatalf("写入非法 UTF-8 压缩文本失败: %v", err)
	}
	if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("错误 = %v, 期望 ErrInvalidUTF8", err)
	}
	if code := expectCloseCode(t, cli); code != CloseInvalidFramePayloadData {
		t.Fatalf("关闭码 = %d, 期望 1007", code)
	}
}

// ---------------------------------------------------------------- Round trips | 往返

func TestCompressionRoundTrips(t *testing.T) {
	configs := []struct {
		name string
		cfg  *compressionConfig
	}{
		{name: "context takeover", cfg: &compressionConfig{clientWindowBits: 15, serverWindowBits: 15}},
		{
			name: "no context takeover",
			cfg: &compressionConfig{
				clientNoContextTakeover: true, serverNoContextTakeover: true,
				clientWindowBits: 15, serverWindowBits: 15,
			},
		},
		{
			name: "mixed takeover",
			cfg: &compressionConfig{
				clientNoContextTakeover: true, serverNoContextTakeover: false,
				clientWindowBits: 15, serverWindowBits: 15,
			},
		},
		{name: "window bits 8", cfg: &compressionConfig{clientWindowBits: 8, serverWindowBits: 8}},
		{
			name: "window bits 10 with server no takeover",
			cfg: &compressionConfig{
				serverNoContextTakeover: true, clientWindowBits: 10, serverWindowBits: 10,
			},
		},
	}

	payloads := []struct {
		name string
		op   OpCode
		data []byte
	}{
		{name: "empty", op: TextMessage, data: nil},
		{name: "small text", op: TextMessage, data: []byte("hello")},
		{name: "chinese text", op: TextMessage, data: []byte(strings.Repeat("中文 | 压缩 ", 50))},
		{name: "binary 64KB", op: BinaryMessage, data: bytes.Repeat([]byte{0x00, 0x7f, 0xff, 0x80}, 16<<10)},
	}

	for _, cc := range configs {
		t.Run(cc.name, func(t *testing.T) {
			srv, cli := compressionPair(t, Options{}, cc.cfg)
			for _, p := range payloads {
				t.Run(p.name, func(t *testing.T) {
					if err := cli.WriteMessage(p.op, p.data); err != nil {
						t.Fatalf("客户端写入失败: %v", err)
					}
					op, got, err := srv.ReadMessage()
					if err != nil {
						t.Fatalf("服务端读取失败: %v", err)
					}
					if op != p.op || !bytes.Equal(got, p.data) {
						t.Fatalf("服务端收到 (%v, %d 字节), 期望 (%v, %d 字节)", op, len(got), p.op, len(p.data))
					}
					if err := srv.WriteMessage(op, got); err != nil {
						t.Fatalf("服务端回显失败: %v", err)
					}
					op, got, err = cli.ReadMessage()
					if err != nil {
						t.Fatalf("客户端读取回显失败: %v", err)
					}
					if op != p.op || !bytes.Equal(got, p.data) {
						t.Fatalf("客户端收到 (%v, %d 字节), 期望 (%v, %d 字节)", op, len(got), p.op, len(p.data))
					}
				})
			}

			// a run of consecutive messages exercises the sliding window across message boundaries | 连续多条消息用于覆盖跨消息的滑动窗口
			long := []byte(strings.Repeat("上下文接管 | context takeover ", 4000))
			for i := 0; i < 8; i++ {
				if err := cli.WriteMessage(BinaryMessage, long); err != nil {
					t.Fatalf("第 %d 条长消息写入失败: %v", i, err)
				}
				_, got, err := srv.ReadMessage()
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

// TestCompressionToggleWriteSide verifies that compression can be turned off mid-connection: the peer, | TestCompressionToggleWriteSide 校验连接中途可关闭压缩：对端
// which negotiated the extension, must keep accepting uncompressed messages. | 已协商扩展，仍必须继续接受未压缩消息。
func TestCompressionToggleWriteSide(t *testing.T) {
	srv, cli := compressionPair(t, Options{}, newCompressionConfig())

	roundTrip := func(label string) {
		t.Helper()
		want := "消息 | message " + label
		if err := cli.WriteMessage(TextMessage, []byte(want)); err != nil {
			t.Fatalf("%s: 写入失败: %v", label, err)
		}
		op, got, err := srv.ReadMessage()
		if err != nil {
			t.Fatalf("%s: 读取失败: %v", label, err)
		}
		if op != TextMessage || string(got) != want {
			t.Fatalf("%s: 消息 = (%v, %q)", label, op, got)
		}
	}

	roundTrip("compressed")
	cli.EnableWriteCompression(false)
	roundTrip("uncompressed")
	cli.EnableWriteCompression(true)
	roundTrip("compressed again")
}

// TestEnableWriteCompressionWithoutNegotiationIsNoop checks the RFC 7692 §6.1 guard: without negotiation | TestEnableWriteCompressionWithoutNegotiationIsNoop 校验 RFC 7692 §6.1 的约束：未协商时
// the toggle must not cause RSV1 to be set. | 该开关不得导致 RSV1 被置位。
func TestEnableWriteCompressionWithoutNegotiationIsNoop(t *testing.T) {
	srv, raw := dialPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = raw.Close() }()

	srv.EnableWriteCompression(true)
	if err := srv.WriteMessage(TextMessage, []byte("plain")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	h, body := readRawFrame(t, raw)
	if h.rsv1 {
		t.Fatal("RSV1 must stay clear without negotiation | 未协商扩展时不得置 RSV1")
	}
	if string(body) != "plain" {
		t.Fatalf("载荷 = %q, 期望 plain", body)
	}
}

// ---------------------------------------------------------------- Handshake integration | 握手集成

// startCompressionEchoServer starts an echo server with server-side compression controlled by opts. | startCompressionEchoServer 启动一个回显服务端，服务端压缩由 opts 控制。
func startCompressionEchoServer(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, opts)
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
	t.Cleanup(srv.Close)
	return srv
}

func TestUpgradeCompressionEndToEnd(t *testing.T) {
	srv := startCompressionEchoServer(t, Options{EnableCompression: true})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cli, err := Dial(ctx, wsURL(t, srv, "/"), DialOptions{EnableWriteCompression: true})
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}
	defer func() { _ = cli.CloseNow() }()
	if cli.compression == nil {
		t.Fatal("the client must enable compression once the server accepts | 服务端接受扩展后客户端必须启用压缩")
	}

	payloads := []string{"hello", "中文 | chinese", "", strings.Repeat("压缩往返 | round trip ", 6000)}
	for i, want := range payloads {
		if err := cli.WriteMessage(TextMessage, []byte(want)); err != nil {
			t.Fatalf("第 %d 条消息写入失败: %v", i, err)
		}
		op, got, err := cli.ReadMessage()
		if err != nil {
			t.Fatalf("第 %d 条消息回显失败: %v", i, err)
		}
		if op != TextMessage || string(got) != want {
			t.Fatalf("第 %d 条回显不一致: 类型 %v, 长度 %d, 期望 %d", i, op, len(got), len(want))
		}
	}
}

func TestUpgradeCompressionDeclined(t *testing.T) {
	t.Run("server without compression", func(t *testing.T) {
		srv := startCompressionEchoServer(t, Options{})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cli, err := Dial(ctx, wsURL(t, srv, "/"), DialOptions{EnableWriteCompression: true})
		if err != nil {
			t.Fatalf("Dial 失败: %v", err)
		}
		defer func() { _ = cli.CloseNow() }()
		if cli.compression != nil {
			t.Fatal("the client must stay uncompressed when the server declines | 服务端未启用扩展时客户端不得启用压缩")
		}
		if err := cli.WriteMessage(TextMessage, []byte("plain")); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		if _, got, err := cli.ReadMessage(); err != nil || string(got) != "plain" {
			t.Fatalf("回显 = (%q, %v)", got, err)
		}
	})

	t.Run("client without offer", func(t *testing.T) {
		srv := startCompressionEchoServer(t, Options{EnableCompression: true})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cli, err := Dial(ctx, wsURL(t, srv, "/"), DialOptions{})
		if err != nil {
			t.Fatalf("Dial 失败: %v", err)
		}
		defer func() { _ = cli.CloseNow() }()
		if cli.compression != nil {
			t.Fatal("the client must stay uncompressed without an offer | 客户端未提议扩展时不得启用压缩")
		}
		if err := cli.WriteMessage(TextMessage, []byte("plain")); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		if _, got, err := cli.ReadMessage(); err != nil || string(got) != "plain" {
			t.Fatalf("回显 = (%q, %v)", got, err)
		}
	})
}

// TestUpgradeCompressionResponseHeader checks the exact parameter echo on a raw handshake. | TestUpgradeCompressionResponseHeader 在原始握手上校验精确的参数回写。
func TestUpgradeCompressionResponseHeader(t *testing.T) {
	cases := []struct {
		name  string
		offer string
		want  string
	}{
		{
			name: "bare offer", offer: "permessage-deflate",
			want: "permessage-deflate",
		},
		{
			name: "client window", offer: "permessage-deflate; client_max_window_bits",
			want: "permessage-deflate; client_max_window_bits=15",
		},
		{
			name:  "both windows and no-context-takeover",
			offer: "permessage-deflate; client_max_window_bits=10; server_max_window_bits=12; client_no_context_takeover; server_no_context_takeover",
			want:  "permessage-deflate; client_no_context_takeover; server_no_context_takeover; client_max_window_bits=10; server_max_window_bits=12",
		},
		{
			name: "unknown parameter declined", offer: "permessage-deflate; foo=bar",
			want: "",
		},
		{
			name: "foreign extension declined", offer: "x-custom",
			want: "",
		},
		{
			name: "no offer", offer: "",
			want: "",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, Options{EnableCompression: true})
		if err != nil {
			return
		}
		_ = c.CloseNow()
	}))
	t.Cleanup(srv.Close)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := net.Dial("tcp", srv.Listener.Addr().String())
			if err != nil {
				t.Fatalf("连接测试服务端失败: %v", err)
			}
			defer func() { _ = conn.Close() }()

			var req strings.Builder
			req.WriteString("GET / HTTP/1.1\r\nHost: ")
			req.WriteString(srv.Listener.Addr().String())
			req.WriteString("\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\n")
			req.WriteString("Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n")
			if tc.offer != "" {
				req.WriteString("Sec-WebSocket-Extensions: ")
				req.WriteString(tc.offer)
				req.WriteString("\r\n")
			}
			req.WriteString("\r\n")
			if _, err := conn.Write([]byte(req.String())); err != nil {
				t.Fatalf("写入握手请求失败: %v", err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("读取握手响应失败: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusSwitchingProtocols {
				t.Fatalf("状态码 = %d, 期望 101", resp.StatusCode)
			}
			if got := resp.Header.Get("Sec-WebSocket-Extensions"); got != tc.want {
				t.Fatalf("扩展响应 = %q, 期望 %q", got, tc.want)
			}
		})
	}
}

// TestDialOffersCompressionHeader verifies that the client option really changes the wire request. | TestDialOffersCompressionHeader 校验客户端选项确实改变了线上请求。
func TestDialOffersCompressionHeader(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		want    string
	}{
		{name: "enabled", enabled: true, want: "permessage-deflate; client_max_window_bits"},
		{name: "disabled", enabled: false, want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captured := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured <- r.Header.Get("Sec-WebSocket-Extensions")
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
					acceptKey(r.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n"))
				_, _ = io.Copy(io.Discard, conn)
			}))
			t.Cleanup(srv.Close)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cli, err := Dial(ctx, wsURL(t, srv, "/"), DialOptions{EnableWriteCompression: tc.enabled})
			if err != nil {
				t.Fatalf("Dial 失败: %v", err)
			}
			defer func() { _ = cli.CloseNow() }()

			if got := recvOrTimeout(t, captured, 5*time.Second, "服务端捕获的扩展提议"); got != tc.want {
				t.Fatalf("Sec-WebSocket-Extensions = %q, 期望 %q", got, tc.want)
			}
		})
	}
}

// TestCompressionOfferRejectedAsUnexpectedByDefault preserves the existing contract: a client that did | TestCompressionOfferRejectedAsUnexpectedByDefault 保留既有约定：未提议扩展的客户端
// not offer the extension still rejects a server that activates it. | 仍会拒绝擅自启用扩展的服务端。
func TestCompressionOfferRejectedAsUnexpectedByDefault(t *testing.T) {
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
			acceptKey(r.Header.Get("Sec-WebSocket-Key")) + "\r\nSec-WebSocket-Extensions: permessage-deflate\r\n\r\n"))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := Dial(ctx, wsURL(t, srv, "/"), DialOptions{})
	if !errors.Is(err, ErrHandshake) || !errors.Is(err, ErrUnrequestedExtension) {
		t.Fatalf("错误 = %v, 期望同时匹配 ErrHandshake 与 ErrUnrequestedExtension", err)
	}
}
