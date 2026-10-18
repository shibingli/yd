package ws

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
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
	var keyBuf [maxHeaderSize]byte
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

// sinkConn discards writes, isolating encode and mask cost from kernel I/O. | sinkConn 丢弃写入，把编码与掩码开销同内核 I/O 隔离开。
type sinkConn struct{ written int }

// Read reports EOF. | Read 返回 EOF。
func (c *sinkConn) Read([]byte) (int, error) { return 0, io.EOF }

// Write counts and discards the bytes. | Write 计数并丢弃字节。
func (c *sinkConn) Write(p []byte) (int, error) { c.written += len(p); return len(p), nil }

// Close is a no-op. | Close 为空操作。
func (c *sinkConn) Close() error { return nil }

// LocalAddr returns nil. | LocalAddr 返回 nil。
func (c *sinkConn) LocalAddr() net.Addr { return nil }

// RemoteAddr returns nil. | RemoteAddr 返回 nil。
func (c *sinkConn) RemoteAddr() net.Addr { return nil }

// SetDeadline is a no-op. | SetDeadline 为空操作。
func (c *sinkConn) SetDeadline(time.Time) error { return nil }

// SetReadDeadline is a no-op. | SetReadDeadline 为空操作。
func (c *sinkConn) SetReadDeadline(time.Time) error { return nil }

// SetWriteDeadline is a no-op. | SetWriteDeadline 为空操作。
func (c *sinkConn) SetWriteDeadline(time.Time) error { return nil }

// BenchmarkWriteMessageClient measures client-side encoding: header, fresh mask, masking and the
// configured write strategy, without kernel I/O. | BenchmarkWriteMessageClient 测量客户端侧编码：
// 帧头、随机掩码、掩码变换与写策略，不含内核 I/O。
func BenchmarkWriteMessageClient(b *testing.B) {
	for _, size := range []int{32, 1 << 10, 4 << 10, 64 << 10} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			c := newConn(&sinkConn{}, Options{WriteBufferSize: defaultWriteBufferSize}, true)
			payload := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := c.WriteMessage(BinaryMessage, payload); err != nil {
					b.Fatalf("write failed | 写入失败: %v", err)
				}
			}
		})
	}
}

// ---------------------------------------------------------------- permessage-deflate | permessage-deflate

// benchDeflateSizes are the payload sizes used by the compression benchmarks. | benchDeflateSizes 是压缩基准使用的载荷大小。
var benchDeflateSizes = []int{1 << 10, 16 << 10, 64 << 10}

// benchCompressible returns a size-byte, JSON-like payload that deflate compresses well. | benchCompressible
// 返回 size 字节、类 JSON、可被 deflate 有效压缩的载荷。
func benchCompressible(size int) []byte {
	const unit = `{"id":12345,"name":"bench-user","active":true,"score":98.5},`
	out := bytes.Repeat([]byte(unit), size/len(unit)+1)
	return out[:size]
}

// BenchmarkCompressMessage measures the encoder half alone: one message compressed with and without
// context takeover. | BenchmarkCompressMessage 单独测量编码端：一条消息在启用/禁用上下文接管下的压缩。
func BenchmarkCompressMessage(b *testing.B) {
	for _, size := range benchDeflateSizes {
		payload := benchCompressible(size)
		for _, noTakeover := range []bool{false, true} {
			name := fmt.Sprintf("size=%d/context=takeover", size)
			if noTakeover {
				name = fmt.Sprintf("size=%d/context=reset", size)
			}
			b.Run(name, func(b *testing.B) {
				mc := newMessageCompressor(defaultWindowBits, noTakeover)
				b.SetBytes(int64(size))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := mc.compress(payload); err != nil {
						b.Fatalf("compress failed | 压缩失败: %v", err)
					}
				}
			})
		}
	}
}

// BenchmarkCompressedRoundTrip measures the full compressed path over net.Pipe: compress, frame,
// (client: mask), read, unmask and inflate. | BenchmarkCompressedRoundTrip 测量 net.Pipe 上的完整压缩
// 路径：压缩、成帧、（客户端）掩码、读取、去掩码与解压。
func BenchmarkCompressedRoundTrip(b *testing.B) {
	const size = 4 << 10
	payload := benchCompressible(size)
	for _, serverSends := range []bool{false, true} {
		name := "client_to_server"
		if serverSends {
			name = "server_to_client"
		}
		b.Run(name, func(b *testing.B) {
			cliNC, srvNC := net.Pipe()
			opts := Options{}.normalize()
			srv := newConn(srvNC, opts, false)
			cli := newConn(cliNC, opts, true)
			cfg := newCompressionConfig()
			srv.setCompression(cfg)
			cli.setCompression(cfg)
			defer func() { _ = srv.CloseNow() }()
			defer func() { _ = cli.CloseNow() }()

			sender, receiver := cli, srv
			if serverSends {
				sender, receiver = srv, cli
			}
			done := make(chan error, 1)
			go func() {
				for i := 0; i < b.N; i++ {
					op, data, err := receiver.ReadMessage()
					if err != nil {
						done <- err
						return
					}
					if op != TextMessage || len(data) != size || data[0] != payload[0] || data[size-1] != payload[size-1] {
						done <- fmt.Errorf("unexpected message: op=%v len=%d", op, len(data))
						return
					}
				}
				done <- nil
			}()

			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := sender.WriteMessage(TextMessage, payload); err != nil {
					b.Fatalf("write message failed: %v", err)
				}
			}
			if err := <-done; err != nil {
				b.Fatalf("read message failed: %v", err)
			}
		})
	}
}

// BenchmarkFrameDecodeBufioSmall measures parsing a masked 32-byte frame from a
// bufio.Reader, the reader type used by established connections. | BenchmarkFrameDecodeBufioSmall
// 测量从已建立连接所用的 bufio.Reader 解析 32 字节掩码帧。
func BenchmarkFrameDecodeBufioSmall(b *testing.B) {
	const size = 32
	key := [4]byte{1, 2, 3, 4}
	var hdr [maxHeaderSize]byte
	n := writeFrameHeader(hdr[:], true, BinaryMessage, true, size)
	frame := make([]byte, 0, n+4+size)
	frame = append(frame, hdr[:n]...)
	frame = append(frame, key[:]...)
	frame = append(frame, make([]byte, size)...)

	var src bytes.Reader
	br := bufio.NewReaderSize(&src, 64)
	out := make([]byte, size)
	hdrBuf := make([]byte, maxHeaderSize)

	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src.Reset(frame)
		br.Reset(&src)
		h, err := readFrameHeader(br, hdrBuf)
		if err != nil {
			b.Fatalf("decode header failed | 解码帧头失败: %v", err)
		}
		if _, err := io.ReadFull(br, out); err != nil {
			b.Fatalf("decode payload failed | 解码载荷失败: %v", err)
		}
		maskBytes(h.maskKey, 0, out)
	}
	benchSinkBytes = out
}

// BenchmarkWriteBatch measures client-side encode cost for batched messages on
// a sink transport, isolating framing and masking from kernel I/O. | BenchmarkWriteBatch
// 测量客户端批量消息的编码开销（不含内核 I/O），隔离成帧与掩码成本。
func BenchmarkWriteBatch(b *testing.B) {
	payload := bytes.Repeat([]byte("b"), 1<<10)
	for _, batch := range []int{1, 4, 16, 64} {
		b.Run(fmt.Sprintf("batch=%d", batch), func(b *testing.B) {
			c := newConn(&sinkConn{}, Options{WriteBufferSize: 64 << 10}, true)
			msgs := make([]Message, batch)
			for i := range msgs {
				msgs[i] = Message{Op: BinaryMessage, Payload: payload}
			}
			b.SetBytes(int64(batch * len(payload)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := c.WriteBatch(msgs); err != nil {
					b.Fatalf("WriteBatch failed | WriteBatch 失败: %v", err)
				}
			}
		})
	}
}
