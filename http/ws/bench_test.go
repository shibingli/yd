package ws

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
)

// benchSinkBytes and benchSinkBool keep results observable so the compiler cannot elide the measured work. | benchSinkBytes 与 benchSinkBool 让结果可观测，避免编译器消除被测量的工作。
var (
	benchSinkBytes []byte
	benchSinkBool  bool
)

// ---------------------------------------------------------------- Frame codec | 帧编解码

// BenchmarkFrameEncodeSmall measures assembling a masked 32-byte text frame: header, mask key and payload copy. | BenchmarkFrameEncodeSmall 测量组装 32 字节掩码文本帧：帧头、掩码键与载荷复制。
func BenchmarkFrameEncodeSmall(b *testing.B) {
	benchmarkFrameEncode(b, TextMessage, bytes.Repeat([]byte("a"), 32))
}

// BenchmarkFrameEncodeLarge measures assembling a masked 64 KiB frame, which forces the 8-byte length form. | BenchmarkFrameEncodeLarge 测量组装 64 KiB 掩码帧，触发 8 字节长度形式。
func BenchmarkFrameEncodeLarge(b *testing.B) {
	benchmarkFrameEncode(b, BinaryMessage, bytes.Repeat([]byte("a"), 64<<10))
}

// benchmarkFrameEncode is the shared body of the encode benchmarks; the mask key is fixed to isolate encoding cost. | benchmarkFrameEncode 是编码基准的共用主体；固定掩码键以隔离编码开销。
func benchmarkFrameEncode(b *testing.B, op OpCode, payload []byte) {
	b.Helper()
	dst := make([]byte, maxHeaderSize+len(payload))
	key := [4]byte{0x37, 0xFA, 0x21, 0x3D}

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := writeFrameHeader(dst, true, op, true, int64(len(payload)))
		copy(dst[n:], key[:])
		n += 4
		copy(dst[n:], payload)
		maskBytes(key, 0, dst[n:n+len(payload)])
	}
	benchSinkBytes = dst
}

// BenchmarkFrameDecodeSmall measures parsing a masked 32-byte frame: header read, payload read and unmasking. | BenchmarkFrameDecodeSmall 测量解析 32 字节掩码帧：读帧头、读载荷与去掩码。
func BenchmarkFrameDecodeSmall(b *testing.B) {
	benchmarkFrameDecode(b, 32)
}

// BenchmarkFrameDecodeLarge measures parsing a masked 64 KiB frame, which carries an 8-byte length. | BenchmarkFrameDecodeLarge 测量解析 64 KiB 掩码帧，携带 8 字节长度。
func BenchmarkFrameDecodeLarge(b *testing.B) {
	benchmarkFrameDecode(b, 64<<10)
}

// benchmarkFrameDecode is the shared body of the decode benchmarks; the frame and reader are reused across iterations. | benchmarkFrameDecode 是解码基准的共用主体；帧与读取器在迭代间复用。
func benchmarkFrameDecode(b *testing.B, size int) {
	b.Helper()
	key := [4]byte{0x11, 0x22, 0x33, 0x44}
	frame := make([]byte, 0, maxHeaderSize+size)
	var hdr [maxHeaderSize]byte
	n := writeFrameHeader(hdr[:], true, BinaryMessage, true, int64(size))
	frame = append(frame, hdr[:n]...)
	frame = append(frame, key[:]...)
	body := bytes.Repeat([]byte("a"), size)
	maskBytes(key, 0, body)
	frame = append(frame, body...)

	var reader bytes.Reader
	var keyBuf [8]byte
	out := make([]byte, size)

	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader.Reset(frame)
		h, err := readFrameHeader(&reader, keyBuf[:])
		if err != nil || h.length != int64(size) {
			b.Fatalf("decode header failed: length=%d err=%v", h.length, err)
		}
		if _, err := io.ReadFull(&reader, out); err != nil {
			b.Fatalf("decode payload failed: %v", err)
		}
		maskBytes(h.maskKey, 0, out)
	}
	benchSinkBytes = out
}

// ---------------------------------------------------------------- Masking | 掩码

// BenchmarkMaskBytes measures the XOR mask transform across the payload sizes that matter on the wire. | BenchmarkMaskBytes 测量线上关键载荷长度下的 XOR 掩码变换。
func BenchmarkMaskBytes(b *testing.B) {
	key := [4]byte{0x37, 0xFA, 0x21, 0x3D}
	for _, size := range []int{0, 1, 125, 4096, 64 << 10} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			buf := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				maskBytes(key, 0, buf)
			}
			benchSinkBytes = buf
		})
	}
}

// ---------------------------------------------------------------- UTF-8 validation | UTF-8 校验

// BenchmarkUtf8Validate measures the incremental UTF-8 validator on ASCII, Chinese and mixed chunks. | BenchmarkUtf8Validate 测量增量 UTF-8 校验器在纯 ASCII、中文与混合分块上的表现。
func BenchmarkUtf8Validate(b *testing.B) {
	cases := []struct {
		name string
		data []byte
	}{
		{name: "ascii-4KiB", data: bytes.Repeat([]byte("a"), 4096)},
		{name: "chinese-12KiB", data: []byte(strings.Repeat("中文校验", 1024))},
		{name: "mixed-6KiB", data: append(bytes.Repeat([]byte("ascii-"), 512), []byte(strings.Repeat("中文", 512))...)},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.data)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var s utf8State
				if !s.write(tc.data) || !s.complete() {
					b.Fatal("valid UTF-8 was rejected")
				}
			}
			benchSinkBool = true
		})
	}
}

// ---------------------------------------------------------------- Real socket round trip | 真实套接字往返

// BenchmarkMessageRoundTrip measures a client-to-server 32-byte message over net.Pipe, including masking and unmasking. | BenchmarkMessageRoundTrip 测量经 net.Pipe 的 32 字节客户端到服务端消息，含掩码与去掩码。
func BenchmarkMessageRoundTrip(b *testing.B) {
	cliNC, srvNC := net.Pipe()
	opts := Options{}.normalize()
	srv := newConn(srvNC, opts, false)
	cli := newConn(cliNC, opts, true)
	defer func() { _ = srv.CloseNow() }()
	defer func() { _ = cli.CloseNow() }()

	payload := bytes.Repeat([]byte("x"), 32)
	done := make(chan error, 1)
	go func() {
		for i := 0; i < b.N; i++ {
			op, data, err := srv.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			if op != TextMessage || len(data) != len(payload) {
				done <- fmt.Errorf("unexpected message: op=%v len=%d", op, len(data))
				return
			}
		}
		done <- nil
	}()

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cli.WriteMessage(TextMessage, payload); err != nil {
			b.Fatalf("write message failed: %v", err)
		}
	}
	if err := <-done; err != nil {
		b.Fatalf("read message failed: %v", err)
	}
}
