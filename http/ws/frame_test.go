package ws

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"unicode/utf8"
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
	// RFC 6455 §5.2: the most significant bit of a 64-bit length must be 0, which is a protocol error rather than a size error | RFC 6455 §5.2：64 位长度的最高位必须为 0，这属于协议错误而非大小超限
	hdr := []byte{0x82, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	var buf [8]byte
	if _, err := readFrameHeader(bytes.NewReader(hdr), buf[:]); !errors.Is(err, ErrFrameLengthMSB) {
		t.Fatalf("err = %v, 期望 ErrFrameLengthMSB", err)
	}

	// a legal 64-bit length with the MSB clear must parse: the size limit is enforced later, as 1009 | 最高位为 0 的合法 64 位长度必须能解析：大小上限稍后以 1009 判定
	legal := []byte{0x82, 0x7f, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00}
	h, err := readFrameHeader(bytes.NewReader(legal), buf[:])
	if err != nil || h.length != 1<<32 {
		t.Fatalf("合法 64 位长度解析结果 h=%+v err=%v", h, err)
	}
}

func TestReadFrameHeaderShortInput(t *testing.T) {
	var buf [8]byte
	if _, err := readFrameHeader(bytes.NewReader([]byte{0x81}), buf[:]); err != io.ErrUnexpectedEOF {
		t.Fatalf("err = %v, 期望 ErrUnexpectedEOF", err)
	}
}

// TestMaskBytesRFCExample uses the official masking example from RFC 6455 §5.7. | TestMaskBytesRFCExample 使用 RFC 6455 §5.7 的官方掩码示例。
func TestMaskBytesRFCExample(t *testing.T) {
	key := [4]byte{0x37, 0xfa, 0x21, 0x3d}
	payload := []byte("Hello")
	maskBytes(key, 0, payload)
	want := []byte{0x7f, 0x9f, 0x4d, 0x51, 0x58}
	if !bytes.Equal(payload, want) {
		t.Fatalf("掩码结果 = % x, 期望 % x", payload, want)
	}
	// XOR is self-inverse: masking again restores the original | XOR 自反：再次掩码即还原
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
			// simulate "a slice located at offset offset" | 模拟「偏移 offset 处的切片」
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
	// "中" = E4 B8 AD; it must also be judged valid when split across frames | "中" = E4 B8 AD，跨帧拆分也必须判定为合法
	var s utf8State
	if !s.write([]byte{0xE4}) {
		t.Fatal("the first byte must not be judged invalid right away | 首字节不应立即判非法")
	}
	if s.complete() {
		t.Fatal("half a character must not count as complete | 半个字符时不应视为完整")
	}
	if !s.write([]byte{0xB8}) {
		t.Fatal("a middle byte must not be judged invalid | 中间字节不应判非法")
	}
	if !s.write([]byte{0xAD}) {
		t.Fatal("the last byte must not be judged invalid | 末字节不应判非法")
	}
	if !s.complete() {
		t.Fatal("the sequence must count as complete once finished | 序列完成后应视为完整")
	}

	// invalid continuation byte | 非法续字节
	var bad utf8State
	if bad.write([]byte{0xE4, 0x28}) {
		t.Fatal("an invalid continuation byte must fail | 非法续字节必须判失败")
	}
	// overlong encoding C0 80 | 过长编码 C0 80
	var overlong utf8State
	if overlong.write([]byte{0xC0, 0x80}) {
		t.Fatal("an overlong encoding must fail | 过长编码必须判失败")
	}
	// surrogate code point ED A0 80 (U+D800) | 代理区码点 ED A0 80（U+D800）
	var surrogate utf8State
	if surrogate.write([]byte{0xED, 0xA0, 0x80}) {
		t.Fatal("a surrogate code point must fail | 代理区码点必须判失败")
	}
	// out of range F5 | 超范围 F5
	var tooBig utf8State
	if tooBig.write([]byte{0xF5, 0x80, 0x80, 0x80}) {
		t.Fatal("anything beyond U+10FFFF must fail | 超出 U+10FFFF 必须判失败")
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
		t.Fatal("a one-byte payload must be a protocol error | 单字节载荷必须判协议错误")
	}
	// the reserved code 1005 must not appear on the wire | 保留码 1005 不得出现在线上
	reserved := make([]byte, 2)
	binary.BigEndian.PutUint16(reserved, uint16(CloseNoStatusReceived))
	if _, _, err := parseClosePayload(reserved); err == nil {
		t.Fatal("the reserved code 1005 must be a protocol error | 收到保留码 1005 必须判协议错误")
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

// TestReadFrameHeaderDoesNotAllocate 要求帧头解析在命中路径上零堆分配。
//
// This is a regression guard for the escape that occurred when the fixed header and the mask key
// were read into local arrays / through pointers into the returned struct. | 这是对「固定头与掩码键
// 读入局部数组、或经指向返回结构体的指针读取」所导致逃逸的回归守卫。
func TestReadFrameHeaderDoesNotAllocate(t *testing.T) {
	var frame [maxHeaderSize]byte
	n := writeFrameHeader(frame[:], true, BinaryMessage, true, 65536)
	copy(frame[n:], []byte{1, 2, 3, 4})
	var scratch [8]byte
	var reader bytes.Reader
	var failed bool

	allocs := testing.AllocsPerRun(1000, func() {
		reader.Reset(frame[:n+4])
		h, err := readFrameHeader(&reader, scratch[:])
		if err != nil || h.length != 65536 || h.maskKey != [4]byte{1, 2, 3, 4} {
			failed = true
		}
	})
	if failed {
		t.Fatal("frame header decode failed | 帧头解析失败")
	}
	if allocs != 0 {
		t.Fatalf("allocs = %v, want 0 | 分配 = %v，期望 0", allocs, allocs)
	}
}

// TestUtf8IncrementalMatchesStdlib checks the incremental validator against
// unicode/utf8 at every two-way split and byte by byte; the ASCII fast path
// must not change the verdict. | TestUtf8IncrementalMatchesStdlib 在所有二段切分点
// 与逐字节输入上把增量校验器与 unicode/utf8 对照；ASCII 快路径不得改变判定。
func TestUtf8IncrementalMatchesStdlib(t *testing.T) {
	cases := map[string][]byte{
		"ascii":            []byte("hello ascii"),
		"chinese":          []byte("中文校验"),
		"emoji":            []byte("😀🚀"),
		"mixed":            []byte("abc中文def😀"),
		"overlong":         {0xC0, 0xAF},
		"surrogate":        {0xED, 0xA0, 0x80},
		"beyond-max":       {0xF4, 0x90, 0x80, 0x80},
		"lone-cont":        {'a', 0x80},
		"truncated":        {'a', 0xE4, 0xB8},
		"bad-lead":         {0xFF},
		"ascii-then-3byte": append([]byte("abcdefgh"), 0xE4, 0xB8, 0xAD),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			want := utf8.Valid(data)
			for split := 0; split <= len(data); split++ {
				var s utf8State
				got := s.write(data[:split]) && s.write(data[split:]) && s.complete()
				if got != want {
					t.Fatalf("split %d: got %v, want %v | 切分 %d：得到 %v，期望 %v", split, got, want, split, got, want)
				}
			}
			var s utf8State
			got := true
			for i := range data {
				if !s.write(data[i : i+1]) {
					got = false
					break
				}
			}
			if got = got && s.complete(); got != want {
				t.Fatalf("byte-by-byte: got %v, want %v | 逐字节：得到 %v，期望 %v", got, want, got, want)
			}
		})
	}
}

// TestReadFrameHeaderBufio covers every header form through both the bufio fast
// path and the generic fallback used for very small buffers. | TestReadFrameHeaderBufio
// 通过 bufio 快路径与极小缓冲所用的通用回退路径覆盖所有帧头形式。
func TestReadFrameHeaderBufio(t *testing.T) {
	for _, masked := range []bool{false, true} {
		for _, size := range []int64{0, 1, 125, 126, 65535, 65536} {
			var hdr [maxHeaderSize]byte
			n := writeFrameHeader(hdr[:], true, BinaryMessage, masked, size)
			frame := make([]byte, 0, n+4)
			frame = append(frame, hdr[:n]...)
			if masked {
				frame = append(frame, 1, 2, 3, 4)
			}
			for _, bufSize := range []int{64, 8} {
				br := bufio.NewReaderSize(bytes.NewReader(frame), bufSize)
				h, err := readFrameHeader(br, make([]byte, maxHeaderSize))
				if err != nil {
					t.Fatalf("masked=%v size=%d buf=%d: %v | 错误：%v", masked, size, bufSize, err, err)
				}
				if !h.fin || h.opcode != BinaryMessage || h.masked != masked || h.length != size {
					t.Fatalf("masked=%v size=%d buf=%d: got fin=%v op=%v masked=%v len=%d | 得到 fin=%v op=%v masked=%v len=%d",
						masked, size, bufSize, h.fin, h.opcode, h.masked, h.length, h.fin, h.opcode, h.masked, h.length)
				}
				if masked && h.maskKey != [4]byte{1, 2, 3, 4} {
					t.Fatalf("masked=%v size=%d buf=%d: mask=%v | 掩码=%v", masked, size, bufSize, h.maskKey, h.maskKey)
				}
				if br.Buffered() != 0 {
					t.Fatalf("masked=%v size=%d buf=%d: buffered=%d | 剩余缓冲=%d", masked, size, bufSize, br.Buffered(), br.Buffered())
				}
			}
		}
	}
}
