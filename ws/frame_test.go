package ws

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestFrameHeaderLengths(t *testing.T) {
	cases := []struct {
		payload int64
		want    []byte
	}{
		{payload: 0, want: []byte{0x81, 0x00}},
		{payload: 125, want: []byte{0x81, 0x7d}},
		{payload: 126, want: []byte{0x81, 0x7e, 0x00, 0x7e}},
		{payload: 65535, want: []byte{0x81, 0x7e, 0xff, 0xff}},
		{payload: 65536, want: []byte{0x81, 0x7f, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00}},
	}
	for _, tc := range cases {
		var dst [maxHeaderSize]byte
		n := writeFrameHeader(dst[:], true, TextMessage, false, tc.payload)
		if n != len(tc.want) {
			t.Errorf("payload=%d 头长度 = %d, 期望 %d", tc.payload, n, len(tc.want))
			continue
		}
		if !bytes.Equal(dst[:n], tc.want) {
			t.Errorf("payload=%d 头字节 = % x, 期望 % x", tc.payload, dst[:n], tc.want)
		}
	}
}

func TestFrameHeaderMaskBitAndControlOpcode(t *testing.T) {
	var dst [maxHeaderSize]byte
	n := writeFrameHeader(dst[:], true, CloseMessage, true, 2)
	if n != 2 || dst[0] != 0x88 || dst[1] != 0x82 {
		t.Fatalf("关闭帧掩码头 = % x", dst[:n])
	}
	n = writeFrameHeader(dst[:], false, ContinuationMessage, false, 5)
	if n != 2 || dst[0] != 0x00 || dst[1] != 0x05 {
		t.Fatalf("续帧头 = % x", dst[:n])
	}
}

func TestReadFrameHeaderRoundTrip(t *testing.T) {
	for _, payload := range []int64{0, 1, 125, 126, 1000, 65535, 65536, 1 << 20} {
		var hdr [maxHeaderSize]byte
		n := writeFrameHeader(hdr[:], true, BinaryMessage, true, payload)
		var key [4]byte
		copy(key[:], []byte{0x11, 0x22, 0x33, 0x44})
		copy(hdr[n:], key[:])
		n += 4

		var buf [8]byte
		h, err := readFrameHeader(bytes.NewReader(hdr[:n]), buf[:])
		if err != nil {
			t.Fatalf("payload=%d 解析失败: %v", payload, err)
		}
		if h.length != payload || !h.fin || !h.masked || h.opcode != BinaryMessage || h.maskKey != key {
			t.Fatalf("payload=%d 解析结果 %+v", payload, h)
		}
	}
}

func TestReadFrameHeaderRejectsHugeLength(t *testing.T) {
	// RFC 6455 §5.2：64 位长度的最高位必须为 0
	hdr := []byte{0x82, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	var buf [8]byte
	if _, err := readFrameHeader(bytes.NewReader(hdr), buf[:]); err != ErrMessageTooBig {
		t.Fatalf("err = %v, 期望 ErrMessageTooBig", err)
	}
}

func TestReadFrameHeaderShortInput(t *testing.T) {
	var buf [8]byte
	if _, err := readFrameHeader(bytes.NewReader([]byte{0x81}), buf[:]); err != io.ErrUnexpectedEOF {
		t.Fatalf("err = %v, 期望 ErrUnexpectedEOF", err)
	}
}

// TestMaskBytesRFCExample 使用 RFC 6455 §5.7 的官方掩码示例。
func TestMaskBytesRFCExample(t *testing.T) {
	key := [4]byte{0x37, 0xfa, 0x21, 0x3d}
	payload := []byte("Hello")
	maskBytes(key, 0, payload)
	want := []byte{0x7f, 0x9f, 0x4d, 0x51, 0x58}
	if !bytes.Equal(payload, want) {
		t.Fatalf("掩码结果 = % x, 期望 % x", payload, want)
	}
	// XOR 自反：再次掩码即还原
	maskBytes(key, 0, payload)
	if string(payload) != "Hello" {
		t.Fatalf("还原失败: %q", payload)
	}
}

func TestMaskBytesAllOffsetsAndLengths(t *testing.T) {
	key := [4]byte{0x01, 0x02, 0x03, 0x04}
	original := make([]byte, 300)
	for i := range original {
		original[i] = byte(i * 7)
	}
	for offset := 0; offset < 4; offset++ {
		for _, length := range []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 63, 64, 65, 255, 300} {
			buf := make([]byte, length)
			copy(buf, original[:length])
			// 模拟「偏移 offset 处的切片」
			pre := make([]byte, length)
			copy(pre, buf)
			maskBytes(key, offset, pre)
			for i := range pre {
				if pre[i] != buf[i]^key[(offset+i)%4] {
					t.Fatalf("offset=%d len=%d 第 %d 字节掩码错误", offset, length, i)
				}
			}
			maskBytes(key, offset, pre)
			if !bytes.Equal(pre, buf) {
				t.Fatalf("offset=%d len=%d 掩码不可逆", offset, length)
			}
		}
	}
}

func TestUtf8IncrementalValidation(t *testing.T) {
	// "中" = E4 B8 AD，跨帧拆分也必须判定为合法
	var s utf8State
	if !s.write([]byte{0xE4}) {
		t.Fatal("首字节不应立即判非法")
	}
	if s.complete() {
		t.Fatal("半个字符时不应视为完整")
	}
	if !s.write([]byte{0xB8}) {
		t.Fatal("中间字节不应判非法")
	}
	if !s.write([]byte{0xAD}) {
		t.Fatal("末字节不应判非法")
	}
	if !s.complete() {
		t.Fatal("序列完成后应视为完整")
	}

	// 非法续字节
	var bad utf8State
	if bad.write([]byte{0xE4, 0x28}) {
		t.Fatal("非法续字节必须判失败")
	}
	// 过长编码 C0 80
	var overlong utf8State
	if overlong.write([]byte{0xC0, 0x80}) {
		t.Fatal("过长编码必须判失败")
	}
	// 代理区码点 ED A0 80（U+D800）
	var surrogate utf8State
	if surrogate.write([]byte{0xED, 0xA0, 0x80}) {
		t.Fatal("代理区码点必须判失败")
	}
	// 超范围 F5
	var tooBig utf8State
	if tooBig.write([]byte{0xF5, 0x80, 0x80, 0x80}) {
		t.Fatal("超出 U+10FFFF 必须判失败")
	}
}

func TestClosePayloadRoundTrip(t *testing.T) {
	payload := closePayload(CloseGoingAway, "再见")
	code, text, err := parseClosePayload(payload)
	if err != nil || code != CloseGoingAway || text != "再见" {
		t.Fatalf("解析结果 code=%d text=%q err=%v", code, text, err)
	}

	if code, _, err := parseClosePayload(nil); err != nil || code != CloseNoStatusReceived {
		t.Fatalf("空载荷应解析为 1005: code=%d err=%v", code, err)
	}
	if _, _, err := parseClosePayload([]byte{0x03}); err == nil {
		t.Fatal("单字节载荷必须判协议错误")
	}
	// 保留码 1005 不得出现在线上
	reserved := make([]byte, 2)
	binary.BigEndian.PutUint16(reserved, uint16(CloseNoStatusReceived))
	if _, _, err := parseClosePayload(reserved); err == nil {
		t.Fatal("收到保留码 1005 必须判协议错误")
	}
}

func TestValidCloseCode(t *testing.T) {
	for _, c := range []CloseCode{1000, 1001, 1002, 1003, 1007, 1008, 1009, 1010, 1011, 1012, 1013, 3000, 4999} {
		if !validCloseCode(c) {
			t.Errorf("%d 应为合法关闭码", c)
		}
	}
	for _, c := range []CloseCode{0, 999, 1004, 1005, 1006, 1015, 1016, 2999, 5000} {
		if validCloseCode(c) {
			t.Errorf("%d 应为非法关闭码", c)
		}
	}
}
