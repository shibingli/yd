package ws

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- Raw wire helpers | 原始线缆辅助

// readRawFrame reads one frame directly from nc and returns its header and unmasked payload. | readRawFrame 直接从 nc 读取一个帧，返回帧头与去掩码后的载荷。
func readRawFrame(t *testing.T, nc net.Conn) (frameHeader, []byte) {
	t.Helper()
	_ = nc.SetReadDeadline(time.Now().Add(3 * time.Second))
	var buf [8]byte
	h, err := readFrameHeader(nc, buf[:])
	if err != nil {
		t.Fatalf("读取原始帧头失败: %v", err)
	}
	payload := make([]byte, h.length)
	if h.length > 0 {
		if _, err := io.ReadFull(nc, payload); err != nil {
			t.Fatalf("读取原始帧载荷失败: %v", err)
		}
	}
	if h.masked {
		maskBytes(h.maskKey, 0, payload)
	}
	return h, payload
}

// rawFrameWithBits builds a masked frame and then ORs extra bits into its first byte. | rawFrameWithBits 构造掩码帧，并把额外位叠加到首字节。
func rawFrameWithBits(fin bool, bits byte, op OpCode, payload []byte) []byte {
	f := rawFrame(fin, false, op, true, testKey, payload)
	f[0] |= bits
	return f
}

// closeWirePayload encodes only the two-byte status code of a close frame. | closeWirePayload 仅编码关闭帧的两字节状态码。
func closeWirePayload(code CloseCode) []byte {
	p := make([]byte, 2)
	binary.BigEndian.PutUint16(p, uint16(code))
	return p
}

// ---------------------------------------------------------------- RFC 6455 §5.2: RSV and opcodes | RFC 6455 §5.2：RSV 与操作码

// TestReservedBitsTable covers §5.2: RSV1/RSV2/RSV3 must be 0 without a negotiated extension, otherwise 1002. | TestReservedBitsTable 覆盖 §5.2：未协商扩展时 RSV1/RSV2/RSV3 必须为 0，否则 1002。
func TestReservedBitsTable(t *testing.T) {
	cases := []struct {
		name    string
		bits    byte
		op      OpCode
		payload []byte
	}{
		{name: "RSV1 on text", bits: 0x40, op: TextMessage, payload: []byte("x")},
		{name: "RSV2 on text", bits: 0x20, op: TextMessage, payload: []byte("x")},
		{name: "RSV3 on text", bits: 0x10, op: TextMessage, payload: []byte("x")},
		{name: "RSV1 and RSV2 on binary", bits: 0x60, op: BinaryMessage, payload: []byte{0x00}},
		{name: "RSV1 on fragmented text", bits: 0x40, op: TextMessage, payload: []byte("x")},
		{name: "RSV1 on ping", bits: 0x40, op: PingMessage, payload: []byte("p")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, cli := connPair(t, Options{})
			defer func() { _ = srv.CloseNow() }()
			defer func() { _ = cli.CloseNow() }()

			// a fragmented data frame is built with FIN=0 only for the dedicated case | 只有专门的分片用例使用 FIN=0 构造数据帧
			fin := true
			if tc.name == "RSV1 on fragmented text" {
				fin = false
			}
			if _, err := cli.conn.Write(rawFrameWithBits(fin, tc.bits, tc.op, tc.payload)); err != nil {
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

// TestReservedOpCodesTable covers §5.2: the reserved opcodes 0x3-0x7 and 0xB-0xF must fail with 1002. | TestReservedOpCodesTable 覆盖 §5.2：保留操作码 0x3-0x7 与 0xB-0xF 必须以 1002 失败。
func TestReservedOpCodesTable(t *testing.T) {
	opcodes := []OpCode{0x3, 0x4, 0x5, 0x6, 0x7, 0xB, 0xC, 0xD, 0xE, 0xF}
	for _, op := range opcodes {
		t.Run(op.String(), func(t *testing.T) {
			srv, cli := connPair(t, Options{})
			defer func() { _ = srv.CloseNow() }()
			defer func() { _ = cli.CloseNow() }()

			if _, err := cli.conn.Write(rawFrame(true, false, op, true, testKey, []byte("x"))); err != nil {
				t.Fatalf("写入原始帧失败: %v", err)
			}
			if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrBadOpCode) {
				t.Fatalf("错误 = %v, 期望 ErrBadOpCode", err)
			}
			if code := expectCloseCode(t, cli); code != CloseProtocolError {
				t.Fatalf("关闭码 = %d, 期望 1002", code)
			}
		})
	}
}

// ---------------------------------------------------------------- RFC 6455 §5.5: control frames | RFC 6455 §5.5：控制帧

// TestFragmentedControlFramesTable covers §5.5: control frames must not be fragmented, otherwise 1002. | TestFragmentedControlFramesTable 覆盖 §5.5：控制帧不得分片，否则 1002。
func TestFragmentedControlFramesTable(t *testing.T) {
	cases := []struct {
		name    string
		op      OpCode
		payload []byte
	}{
		{name: "fragmented ping", op: PingMessage, payload: []byte("p")},
		{name: "fragmented pong", op: PongMessage, payload: []byte("p")},
		{name: "fragmented close", op: CloseMessage, payload: closeWirePayload(CloseNormalClosure)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, cli := connPair(t, Options{})
			defer func() { _ = srv.CloseNow() }()
			defer func() { _ = cli.CloseNow() }()

			if _, err := cli.conn.Write(rawFrame(false, false, tc.op, true, testKey, tc.payload)); err != nil {
				t.Fatalf("写入原始帧失败: %v", err)
			}
			if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrFragmentedControl) {
				t.Fatalf("错误 = %v, 期望 ErrFragmentedControl", err)
			}
			if code := expectCloseCode(t, cli); code != CloseProtocolError {
				t.Fatalf("关闭码 = %d, 期望 1002", code)
			}
		})
	}
}

// TestControlPayloadLimitBoundary covers §5.5: 125 bytes are allowed and 126 bytes fail with 1002. | TestControlPayloadLimitBoundary 覆盖 §5.5：125 字节合法，126 字节以 1002 失败。
func TestControlPayloadLimitBoundary(t *testing.T) {
	t.Run("125-byte ping is echoed byte for byte", func(t *testing.T) {
		srv, cli := connPair(t, Options{})
		defer func() { _ = srv.CloseNow() }()
		defer func() { _ = cli.CloseNow() }()

		payload := make([]byte, maxControlPayload)
		for i := range payload {
			payload[i] = byte(i)
		}
		wire := append(rawFrame(true, false, PingMessage, true, testKey, payload),
			rawFrame(true, false, TextMessage, true, testKey, []byte("after"))...)
		if _, err := cli.conn.Write(wire); err != nil {
			t.Fatalf("写入原始帧失败: %v", err)
		}

		op, data, err := srv.ReadMessage()
		if err != nil || op != TextMessage || string(data) != "after" {
			t.Fatalf("读取数据消息失败: op=%v data=%q err=%v", op, data, err)
		}

		h, got := readRawFrame(t, cli.conn)
		if h.opcode != PongMessage || !h.fin || h.masked {
			t.Fatalf("pong 头 = %+v, 期望 FIN、未掩码的 pong", h)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("pong 载荷未原样回显: 长度 %d, 期望 %d", len(got), len(payload))
		}
	})

	t.Run("126-byte ping fails 1002", func(t *testing.T) {
		srv, cli := connPair(t, Options{})
		defer func() { _ = srv.CloseNow() }()
		defer func() { _ = cli.CloseNow() }()

		if _, err := cli.conn.Write(rawFrame(true, false, PingMessage, true, testKey, make([]byte, maxControlPayload+1))); err != nil {
			t.Fatalf("写入原始帧失败: %v", err)
		}
		if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrControlTooLarge) {
			t.Fatalf("错误 = %v, 期望 ErrControlTooLarge", err)
		}
		if code := expectCloseCode(t, cli); code != CloseProtocolError {
			t.Fatalf("关闭码 = %d, 期望 1002", code)
		}
	})
}

// TestPongPayloadAndUnsolicitedPong covers §5.5.3: pongs may carry payload and unsolicited pongs are ignored. | TestPongPayloadAndUnsolicitedPong 覆盖 §5.5.3：pong 可携带载荷，未请求的 pong 应被忽略。
func TestPongPayloadAndUnsolicitedPong(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	want := []byte{0x00, 0x7F, 0x80, 0xFF, 'o', 'k'}
	var handled []byte
	srv.SetPongHandler(func(s string) error {
		handled = []byte(s)
		return nil
	})

	wire := append(rawFrame(true, false, PongMessage, true, testKey, want),
		rawFrame(true, false, TextMessage, true, testKey, []byte("first"))...)
	if _, err := cli.conn.Write(wire); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}
	op, data, err := srv.ReadMessage()
	if err != nil || op != TextMessage || string(data) != "first" {
		t.Fatalf("读取第一条数据失败: op=%v data=%q err=%v", op, data, err)
	}
	if !bytes.Equal(handled, want) {
		t.Fatalf("pong 回调载荷 = % x, 期望 % x", handled, want)
	}

	// with no handler installed an unsolicited pong must be consumed silently | 未安装回调时，未请求的 pong 必须被静默消费
	srv.SetPongHandler(nil)
	wire = append(rawFrame(true, false, PongMessage, true, testKey, []byte("unsolicited")),
		rawFrame(true, false, TextMessage, true, testKey, []byte("second"))...)
	if _, err := cli.conn.Write(wire); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}
	op, data, err = srv.ReadMessage()
	if err != nil || op != TextMessage || string(data) != "second" {
		t.Fatalf("未请求的 pong 不应报错: op=%v data=%q err=%v", op, data, err)
	}
}

// ---------------------------------------------------------------- RFC 6455 §5.1: masking direction | RFC 6455 §5.1：掩码方向

// TestServerFramesAreNotMaskedOnWire covers §5.1: a server must never mask the frames it sends. | TestServerFramesAreNotMaskedOnWire 覆盖 §5.1：服务端发出的帧不得掩码。
func TestServerFramesAreNotMaskedOnWire(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	if err := srv.WriteMessage(TextMessage, []byte("hello")); err != nil {
		t.Fatalf("写入消息失败: %v", err)
	}
	h, payload := readRawFrame(t, cli.conn)
	if h.masked || h.opcode != TextMessage || !h.fin || string(payload) != "hello" {
		t.Fatalf("服务端数据帧 = %+v payload=%q, 期望未掩码的 FIN 文本帧", h, payload)
	}

	if err := srv.WriteControl(PingMessage, []byte("hb"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("写入 ping 失败: %v", err)
	}
	h, payload = readRawFrame(t, cli.conn)
	if h.masked || h.opcode != PingMessage || string(payload) != "hb" {
		t.Fatalf("服务端控制帧 = %+v payload=%q, 期望未掩码的 ping", h, payload)
	}
}

// TestClientFramesAreMaskedOnWire covers §5.1/§5.3: a client must mask every frame with a per-frame key. | TestClientFramesAreMaskedOnWire 覆盖 §5.1/§5.3：客户端必须对每个帧掩码。
func TestClientFramesAreMaskedOnWire(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	payload := []byte{0x00, 0x01, 0xFE, 0xFF, 'm', 's', 'g'}
	if err := cli.WriteMessage(BinaryMessage, payload); err != nil {
		t.Fatalf("写入消息失败: %v", err)
	}
	h, got := readRawFrame(t, srv.conn)
	if !h.masked || h.opcode != BinaryMessage || !h.fin || !bytes.Equal(got, payload) {
		t.Fatalf("客户端数据帧 = %+v payload=% x, 期望带掩码的 FIN 二进制帧", h, got)
	}

	if err := cli.WriteControl(PingMessage, []byte("p"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("写入 ping 失败: %v", err)
	}
	h, got = readRawFrame(t, srv.conn)
	if !h.masked || h.opcode != PingMessage || string(got) != "p" {
		t.Fatalf("客户端控制帧 = %+v payload=%q, 期望带掩码的 ping", h, got)
	}
}

// TestClientRejectsMaskedServerFrame covers §5.1: a client must fail with 1002 when the server masks a frame. | TestClientRejectsMaskedServerFrame 覆盖 §5.1：服务端发来掩码帧时客户端必须以 1002 失败。
func TestClientRejectsMaskedServerFrame(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	if _, err := srv.conn.Write(rawFrame(true, false, TextMessage, true, testKey, []byte("hi"))); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}
	if _, _, err := cli.ReadMessage(); !errors.Is(err, ErrMaskMismatch) {
		t.Fatalf("客户端错误 = %v, 期望 ErrMaskMismatch", err)
	}

	// the client's 1002 close frame travels back over the same socket | 客户端的 1002 关闭帧沿同一套接字回传
	_, _, err := srv.ReadMessage()
	var ce *CloseError
	if !errors.As(err, &ce) || ce.Code != CloseProtocolError {
		t.Fatalf("服务端读到客户端关闭 = %v, 期望 1002", err)
	}
}

// ---------------------------------------------------------------- RFC 6455 §5.5.1 / §7.4: close payloads | RFC 6455 §5.5.1 / §7.4：关闭载荷

// TestCloseOneBytePayloadFails1002 covers §5.5.1: a close payload of exactly one byte is a protocol error. | TestCloseOneBytePayloadFails1002 覆盖 §5.5.1：关闭帧载荷恰好 1 字节即协议错误。
func TestCloseOneBytePayloadFails1002(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	if _, err := cli.conn.Write(rawFrame(true, false, CloseMessage, true, testKey, []byte{0x03})); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}
	if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrProtocol) {
		t.Fatalf("错误 = %v, 期望 ErrProtocol", err)
	}
	if code := expectCloseCode(t, cli); code != CloseProtocolError {
		t.Fatalf("关闭码 = %d, 期望 1002", code)
	}
}

// TestReservedCloseCodesOnWireFail1002 covers §7.4: 1005/1006/1015 and other invalid codes must never appear on the wire. | TestReservedCloseCodesOnWireFail1002 覆盖 §7.4：1005/1006/1015 等非法关闭码不得出现在线上。
func TestReservedCloseCodesOnWireFail1002(t *testing.T) {
	codes := []CloseCode{999, 1004, 1005, 1006, 1015, 1016, 2999, 5000}
	for _, code := range codes {
		t.Run(code.String(), func(t *testing.T) {
			srv, cli := connPair(t, Options{})
			defer func() { _ = srv.CloseNow() }()
			defer func() { _ = cli.CloseNow() }()

			if _, err := cli.conn.Write(rawFrame(true, false, CloseMessage, true, testKey, closeWirePayload(code))); err != nil {
				t.Fatalf("写入原始帧失败: %v", err)
			}
			if _, _, err := srv.ReadMessage(); !errors.Is(err, ErrProtocol) {
				t.Fatalf("错误 = %v, 期望 ErrProtocol", err)
			}
			if got := expectCloseCode(t, cli); got != CloseProtocolError {
				t.Fatalf("关闭码 = %d, 期望 1002", got)
			}
		})
	}
}

// TestCloseWithoutStatusEchoesEmptyPayload covers §5.5.1/§7.4: a status-less close is echoed with an empty body, never with 1005. | TestCloseWithoutStatusEchoesEmptyPayload 覆盖 §5.5.1/§7.4：无状态码的关闭帧必须以空载荷回送，绝不回送 1005。
func TestCloseWithoutStatusEchoesEmptyPayload(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	if _, err := cli.conn.Write(rawFrame(true, false, CloseMessage, true, testKey, nil)); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}
	_, _, err := srv.ReadMessage()
	var ce *CloseError
	if !errors.As(err, &ce) || ce.Code != CloseNoStatusReceived {
		t.Fatalf("服务端错误 = %v, 期望本地 1005 语义", err)
	}

	h, payload := readRawFrame(t, cli.conn)
	if h.opcode != CloseMessage || h.masked {
		t.Fatalf("回送帧头 = %+v, 期望未掩码关闭帧", h)
	}
	if len(payload) != 0 {
		t.Fatalf("回送载荷 = % x, 期望空载荷（1005 不得上线）", payload)
	}
}

// ---------------------------------------------------------------- RFC 6455 §4.2.2: handshake | RFC 6455 §4.2.2：握手

// TestUpgradeRequiresConnectionHeader covers §4.2.2: a missing or non-upgrade Connection header is answered with 400. | TestUpgradeRequiresConnectionHeader 覆盖 §4.2.2：缺少 Connection 或值不含 upgrade 时返回 400。
func TestUpgradeRequiresConnectionHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = Upgrade(w, r, Options{})
	}))
	defer srv.Close()

	cases := []struct {
		name       string
		connection string
	}{
		{name: "missing Connection", connection: ""},
		{name: "Connection without upgrade token", connection: "keep-alive"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatalf("构造请求失败: %v", err)
			}
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-WebSocket-Version", "13")
			req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			if tc.connection != "" {
				req.Header.Set("Connection", tc.connection)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("请求失败: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("状态码 = %d, 期望 400", resp.StatusCode)
			}
		})
	}
}

// TestExtensionOfferNotNegotiated documents that permessage-deflate is not implemented: the offer is ignored and RSV1 still fails 1002. | TestExtensionOfferNotNegotiated 说明未实现 permessage-deflate：扩展提议被忽略，RSV1 仍然以 1002 失败。
func TestExtensionOfferNotNegotiated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, Options{})
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		_, _, _ = c.ReadMessage()
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	defer func() { _ = nc.Close() }()

	req := "GET / HTTP/1.1\r\nHost: " + addr +
		"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13" +
		"\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" +
		"\r\nSec-WebSocket-Extensions: permessage-deflate; client_max_window_bits\r\n\r\n"
	if _, err := nc.Write([]byte(req)); err != nil {
		t.Fatalf("写出握手请求失败: %v", err)
	}

	br := bufio.NewReader(nc)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("读取握手响应失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("状态码 = %d, 期望 101", resp.StatusCode)
	}
	if got := resp.Header.Get("Sec-WebSocket-Extensions"); got != "" {
		t.Fatalf("服务端不得协商未实现的扩展, 实际返回 %q", got)
	}

	if _, err := nc.Write(rawFrameWithBits(true, 0x40, TextMessage, []byte("x"))); err != nil {
		t.Fatalf("写入 RSV1 帧失败: %v", err)
	}
	var hbuf [8]byte
	h, err := readFrameHeader(br, hbuf[:])
	if err != nil {
		t.Fatalf("读取关闭帧头失败: %v", err)
	}
	if h.opcode != CloseMessage || h.masked {
		t.Fatalf("响应帧头 = %+v, 期望未掩码关闭帧", h)
	}
	payload := make([]byte, h.length)
	if _, err := io.ReadFull(br, payload); err != nil {
		t.Fatalf("读取关闭载荷失败: %v", err)
	}
	if len(payload) < 2 || CloseCode(binary.BigEndian.Uint16(payload)) != CloseProtocolError {
		t.Fatalf("关闭载荷 = % x, 期望 1002", payload)
	}
}

// ---------------------------------------------------------------- Known RFC gaps (skipped, reported) | 已知 RFC 缺口（跳过并上报）

// TestFramesWithMSBLengthFail1002 covers §5.2: a 64-bit length with the MSB set must fail with 1002, while a legal length above MaxMessageSize must fail with 1009. | TestFramesWithMSBLengthFail1002 覆盖 §5.2：64 位长度最高位为 1 时必须以 1002 失败，而合法但超过 MaxMessageSize 的长度必须以 1009 失败。
func TestFramesWithMSBLengthFail1002(t *testing.T) {
	t.Run("MSB set is a protocol error 1002", func(t *testing.T) {
		srv, cli := connPair(t, Options{})
		defer func() { _ = srv.CloseNow() }()
		defer func() { _ = cli.CloseNow() }()

		hdr := []byte{0x82, 0x7F, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01}
		if _, err := cli.conn.Write(hdr); err != nil {
			t.Fatalf("写入原始帧头失败: %v", err)
		}
		_, _, err := srv.ReadMessage()
		if !errors.Is(err, ErrFrameLengthMSB) {
			t.Fatalf("错误 = %v, 期望 ErrFrameLengthMSB", err)
		}
		if code := expectCloseCode(t, cli); code != CloseProtocolError {
			t.Fatalf("关闭码 = %d, 期望 1002", code)
		}
	})

	t.Run("legal 64-bit length above the limit is 1009", func(t *testing.T) {
		srv, cli := connPair(t, Options{})
		defer func() { _ = srv.CloseNow() }()
		defer func() { _ = cli.CloseNow() }()

		// length = 1<<40: the MSB is clear, so the length field itself is legal, but it exceeds MaxMessageSize | 长度 = 1<<40：最高位为 0，长度字段本身合法，但超过 MaxMessageSize
		hdr := []byte{0x82, 0xFF, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, testKey[0], testKey[1], testKey[2], testKey[3]}
		if _, err := cli.conn.Write(hdr); err != nil {
			t.Fatalf("写入原始帧头失败: %v", err)
		}
		_, _, err := srv.ReadMessage()
		if !errors.Is(err, ErrMessageTooBig) {
			t.Fatalf("错误 = %v, 期望 ErrMessageTooBig", err)
		}
		if code := expectCloseCode(t, cli); code != CloseMessageTooBig {
			t.Fatalf("关闭码 = %d, 期望 1009", code)
		}
	})
}

// TestCloseInvalidUTF8ReasonFails1007 covers §5.5.1/§7.4.1: an invalid UTF-8 close reason is classified as 1007 by the parser and the echoed close frame now uses that same code. | TestCloseInvalidUTF8ReasonFails1007 覆盖 §5.5.1/§7.4.1：非法 UTF-8 关闭原因由解析器分类为 1007，回送的关闭帧现在采用同一分类。
func TestCloseInvalidUTF8ReasonFails1007(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	payload := append(closeWirePayload(CloseNormalClosure), 0xFF, 0xFE)
	if _, err := cli.conn.Write(rawFrame(true, false, CloseMessage, true, testKey, payload)); err != nil {
		t.Fatalf("写入原始帧失败: %v", err)
	}
	_, _, err := srv.ReadMessage()
	if !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("错误 = %v, 期望 ErrInvalidUTF8", err)
	}
	if code := expectCloseCode(t, cli); code != CloseInvalidFramePayloadData {
		t.Fatalf("关闭码 = %d, 期望 1007", code)
	}
}

// TestCloseHandshakeClosesTCP covers §7.1.1: after both sides exchange Close, the server must close the underlying TCP connection, while the caller still observes the *CloseError*. | TestCloseHandshakeClosesTCP 覆盖 §7.1.1：双方交换 Close 后服务端必须关闭底层 TCP，且调用方仍能观察到 *CloseError*。
func TestCloseHandshakeClosesTCP(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	if err := cli.WriteControl(CloseMessage, closePayload(CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("发送关闭帧失败: %v", err)
	}
	_, _, err := srv.ReadMessage()
	var ce *CloseError
	if !errors.As(err, &ce) || ce.Code != CloseNormalClosure {
		t.Fatalf("服务端关闭错误 = %v, 期望 *CloseError(1000)", err)
	}
	if _, _, err := cli.ReadMessage(); !errors.As(err, &ce) || ce.Code != CloseNormalClosure {
		t.Fatalf("客户端应读到回送的关闭帧, err = %v", err)
	}

	_ = cli.conn.SetReadDeadline(time.Now().Add(time.Second))
	var one [1]byte
	if _, err := cli.conn.Read(one[:]); !errors.Is(err, io.EOF) {
		t.Fatalf("关闭握手完成后服务端必须关闭底层 TCP（§7.1.1），实际 err = %v", err)
	}
}

// TestWriteRejectsReservedOpcode covers §5.2/§5.4: the write API rejects reserved opcodes and bare continuations with dedicated sentinels, while the allowed opcodes keep working. | TestWriteRejectsReservedOpcode 覆盖 §5.2/§5.4：写 API 以专用哨兵拒绝保留操作码与无起始帧的续帧，同时允许的操作码仍可写出。
func TestWriteRejectsReservedOpcode(t *testing.T) {
	srv, cli := connPair(t, Options{})
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	// positive cases: text/binary messages and ping/pong/close controls remain writable | 正向用例：文本/二进制消息与 ping/pong/close 控制帧仍可写出
	if err := srv.WriteMessage(TextMessage, []byte("ok")); err != nil {
		t.Fatalf("WriteMessage(TextMessage) 失败: %v", err)
	}
	if err := srv.WriteMessage(BinaryMessage, []byte{0x01}); err != nil {
		t.Fatalf("WriteMessage(BinaryMessage) 失败: %v", err)
	}
	if err := srv.WriteControl(PingMessage, []byte("p"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("WriteControl(PingMessage) 失败: %v", err)
	}
	if err := srv.WriteControl(PongMessage, []byte("p"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("WriteControl(PongMessage) 失败: %v", err)
	}

	if err := srv.WriteMessage(OpCode(0x3), []byte("x")); !errors.Is(err, ErrWriteMessageOpcode) {
		t.Fatalf("WriteMessage(0x3) 错误 = %v, 期望 ErrWriteMessageOpcode", err)
	}
	if err := srv.WriteMessage(ContinuationMessage, []byte("x")); !errors.Is(err, ErrWriteContinuation) {
		t.Fatalf("WriteMessage(Continuation) 错误 = %v, 期望 ErrWriteContinuation", err)
	}
	if err := srv.WriteControl(OpCode(0xB), nil, time.Now().Add(time.Second)); !errors.Is(err, ErrWriteControlOpcode) {
		t.Fatalf("WriteControl(0xB) 错误 = %v, 期望 ErrWriteControlOpcode", err)
	}
}

// TestCloseRejectsReservedCodeAndInvalidUTF8 covers §5.5.1/§7.4: Close never puts 1006/1015 or a non-UTF-8 reason on the wire; it reports the sentinel and terminates with a status-less close. | TestCloseRejectsReservedCodeAndInvalidUTF8 覆盖 §5.5.1/§7.4：Close 绝不让 1006/1015 或非 UTF-8 原因上线；它返回哨兵并以无状态码关闭帧终止连接。
func TestCloseRejectsReservedCodeAndInvalidUTF8(t *testing.T) {
	t.Run("1006", func(t *testing.T) {
		srv, cli := connPair(t, Options{})
		defer func() { _ = cli.CloseNow() }()
		if err := srv.Close(CloseAbnormalClosure, ""); !errors.Is(err, ErrInvalidCloseCode) {
			t.Fatalf("错误 = %v, 期望 ErrInvalidCloseCode", err)
		}
		h, payload := readRawFrame(t, cli.conn)
		if h.opcode != CloseMessage || h.masked {
			t.Fatalf("关闭帧头 = %+v, 期望未掩码关闭帧", h)
		}
		if len(payload) != 0 {
			t.Fatalf("1006 不得上线, 实际载荷 % x", payload)
		}
	})

	t.Run("1015", func(t *testing.T) {
		srv, cli := connPair(t, Options{})
		defer func() { _ = cli.CloseNow() }()
		if err := srv.Close(CloseTLSHandshake, ""); !errors.Is(err, ErrInvalidCloseCode) {
			t.Fatalf("错误 = %v, 期望 ErrInvalidCloseCode", err)
		}
		h, payload := readRawFrame(t, cli.conn)
		if h.opcode != CloseMessage || h.masked {
			t.Fatalf("关闭帧头 = %+v, 期望未掩码关闭帧", h)
		}
		if len(payload) != 0 {
			t.Fatalf("1015 不得上线, 实际载荷 % x", payload)
		}
	})

	t.Run("invalid UTF-8 reason", func(t *testing.T) {
		srv, cli := connPair(t, Options{})
		defer func() { _ = cli.CloseNow() }()
		if err := srv.Close(CloseNormalClosure, string([]byte{0xFF})); !errors.Is(err, ErrInvalidCloseReason) {
			t.Fatalf("错误 = %v, 期望 ErrInvalidCloseReason", err)
		}
		if _, payload := readRawFrame(t, cli.conn); len(payload) != 0 {
			t.Fatalf("非法原因不得上线, 实际载荷 % x", payload)
		}
	})

	t.Run("valid code and reason reach the wire", func(t *testing.T) {
		srv, cli := connPair(t, Options{})
		defer func() { _ = cli.CloseNow() }()
		if err := srv.Close(CloseNormalClosure, "bye"); err != nil {
			t.Fatalf("Close(1000) 失败: %v", err)
		}
		h, payload := readRawFrame(t, cli.conn)
		if h.opcode != CloseMessage {
			t.Fatalf("帧头 = %+v, 期望关闭帧", h)
		}
		if len(payload) < 2 || CloseCode(binary.BigEndian.Uint16(payload)) != CloseNormalClosure || string(payload[2:]) != "bye" {
			t.Fatalf("关闭载荷 = % x, 期望 1000 + bye", payload)
		}
	})
}

// rawWSHandshakeServer starts a server that answers the upgrade with extraHeaders and keeps the socket open, so Dial can complete or fail as the test requires. | rawWSHandshakeServer 启动一个以 extraHeaders 应答升级并保持连接的服务器，使 Dial 按测试要求成功或失败。
func rawWSHandshakeServer(t *testing.T, extraHeaders string) *httptest.Server {
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
			acceptKey(r.Header.Get("Sec-WebSocket-Key")) + "\r\n" + extraHeaders + "\r\n"))
		_, _ = io.Copy(io.Discard, conn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDialRejectsUnrequestedSubprotocol covers §4.1: the client rejects a subprotocol it did not offer, accepts an offered one, and accepts no selection at all. | TestDialRejectsUnrequestedSubprotocol 覆盖 §4.1：客户端拒绝未提议的子协议、接受自己提议过的子协议，也接受服务端不做选择。
func TestDialRejectsUnrequestedSubprotocol(t *testing.T) {
	dial := func(t *testing.T, srv *httptest.Server, offered ...string) (*Conn, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return Dial(ctx, wsURL(t, srv, "/"), DialOptions{Subprotocols: offered})
	}

	t.Run("unrequested selection is rejected", func(t *testing.T) {
		srv := rawWSHandshakeServer(t, "Sec-WebSocket-Protocol: not-requested\r\n")
		c, err := dial(t, srv, "chat")
		if err == nil {
			_ = c.CloseNow()
			t.Fatal("Dial must reject a subprotocol the client did not offer (§4.1) | Dial 必须拒绝服务端单方面选择的子协议（§4.1）")
		}
		if !errors.Is(err, ErrHandshake) || !errors.Is(err, ErrUnrequestedSubprotocol) {
			t.Fatalf("错误 = %v, 期望 ErrHandshake 且 ErrUnrequestedSubprotocol", err)
		}
	})

	t.Run("offered selection is accepted", func(t *testing.T) {
		srv := rawWSHandshakeServer(t, "Sec-WebSocket-Protocol: chat\r\n")
		c, err := dial(t, srv, "superchat", "chat")
		if err != nil {
			t.Fatalf("Dial 不应拒绝自己提议过的子协议: %v", err)
		}
		defer func() { _ = c.CloseNow() }()
		if got := c.Subprotocol(); got != "chat" {
			t.Fatalf("子协议 = %q, 期望 chat", got)
		}
	})

	t.Run("no selection is accepted", func(t *testing.T) {
		srv := rawWSHandshakeServer(t, "")
		c, err := dial(t, srv, "chat")
		if err != nil {
			t.Fatalf("服务端未选择子协议时 Dial 不得失败: %v", err)
		}
		defer func() { _ = c.CloseNow() }()
		if got := c.Subprotocol(); got != "" {
			t.Fatalf("子协议 = %q, 期望空串", got)
		}
	})
}

// TestDialRejectsUnrequestedExtension covers §4.1: the client never offers extensions, so a response that activates one is rejected, while a response without one is accepted. | TestDialRejectsUnrequestedExtension 覆盖 §4.1：客户端从不提议扩展，因此响应启用扩展必须被拒绝，而未启用扩展的响应被接受。
func TestDialRejectsUnrequestedExtension(t *testing.T) {
	dial := func(t *testing.T, srv *httptest.Server) (*Conn, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return Dial(ctx, wsURL(t, srv, "/"), DialOptions{})
	}

	t.Run("unrequested extension is rejected", func(t *testing.T) {
		srv := rawWSHandshakeServer(t, "Sec-WebSocket-Extensions: permessage-deflate\r\n")
		c, err := dial(t, srv)
		if err == nil {
			_ = c.CloseNow()
			t.Fatal("Dial must reject an extension the client did not offer (§4.1) | Dial 必须拒绝服务端单方面启用的扩展（§4.1）")
		}
		if !errors.Is(err, ErrHandshake) || !errors.Is(err, ErrUnrequestedExtension) {
			t.Fatalf("错误 = %v, 期望 ErrHandshake 且 ErrUnrequestedExtension", err)
		}
	})

	t.Run("response without extensions is accepted", func(t *testing.T) {
		srv := rawWSHandshakeServer(t, "")
		c, err := dial(t, srv)
		if err != nil {
			t.Fatalf("未启用扩展时 Dial 不得失败: %v", err)
		}
		_ = c.CloseNow()
	})
}
