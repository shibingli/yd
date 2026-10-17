package b64x

import (
	"encoding/base64"
	"fmt"
	"testing"
)

// benchSinkBytes keeps encoded/decoded results observable. | benchSinkBytes 让编解码结果保持可观测。
var benchSinkBytes []byte

// benchSinkString keeps string results observable. | benchSinkString 让字符串结果保持可观测。
var benchSinkString string

// benchBuf returns n deterministic bytes. | benchBuf 返回 n 个确定性字节。
func benchBuf(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i%251 + 1)
	}
	return b
}

// BenchmarkEncode compares the adaptive encoder with encoding/base64 on the same buffers. | BenchmarkEncode
// 在同一缓冲上对比自适应编码器与 encoding/base64。
func BenchmarkEncode(b *testing.B) {
	for _, size := range []int{16, 1 << 10, 64 << 10} {
		src := benchBuf(size)
		dst := make([]byte, StdEncoding.EncodedLen(size))
		b.Run(fmt.Sprintf("b64x/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				StdEncoding.Encode(dst, src)
			}
			benchSinkBytes = dst
		})
		b.Run(fmt.Sprintf("stdlib/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				base64.StdEncoding.Encode(dst, src)
			}
			benchSinkBytes = dst
		})
	}
}

// BenchmarkEncodeToString compares the allocating encoders. | BenchmarkEncodeToString 对比会分配结果的编码入口。
func BenchmarkEncodeToString(b *testing.B) {
	for _, size := range []int{16, 1 << 10} {
		src := benchBuf(size)
		b.Run(fmt.Sprintf("b64x/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchSinkString = StdEncoding.EncodeToString(src)
			}
		})
		b.Run(fmt.Sprintf("stdlib/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchSinkString = base64.StdEncoding.EncodeToString(src)
			}
		})
	}
}

// BenchmarkRawURLEncode measures the unpadded URL-safe variant used by JWTs. | BenchmarkRawURLEncode
// 测量 JWT 使用的无填充 URL 安全变体。
func BenchmarkRawURLEncode(b *testing.B) {
	for _, size := range []int{16, 1 << 10, 64 << 10} {
		src := benchBuf(size)
		dst := make([]byte, RawURLEncoding.EncodedLen(size))
		b.Run(fmt.Sprintf("b64x/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				RawURLEncoding.Encode(dst, src)
			}
		})
		b.Run(fmt.Sprintf("stdlib/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				base64.RawURLEncoding.Encode(dst, src)
			}
		})
	}
}

// BenchmarkDecode compares the decoders; dst is reused, so this measures pure codec cost. | BenchmarkDecode
// 对比解码器；dst 复用，因此只测量编解码本身。
func BenchmarkDecode(b *testing.B) {
	for _, size := range []int{16, 1 << 10, 64 << 10} {
		src := benchBuf(size)
		encoded := make([]byte, StdEncoding.EncodedLen(size))
		StdEncoding.Encode(encoded, src)
		dst := make([]byte, StdEncoding.DecodedLen(len(encoded)))
		b.Run(fmt.Sprintf("b64x/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := StdEncoding.Decode(dst, encoded); err != nil {
					b.Fatalf("Decode failed | Decode 失败: %v", err)
				}
			}
		})
		b.Run(fmt.Sprintf("stdlib/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := base64.StdEncoding.Decode(dst, encoded); err != nil {
					b.Fatalf("Decode failed | Decode 失败: %v", err)
				}
			}
		})
	}
}

// BenchmarkDecodeString compares the allocating string decoders (the JWT path). | BenchmarkDecodeString
// 对比会分配结果的字符串解码入口（JWT 路径）。
func BenchmarkDecodeString(b *testing.B) {
	for _, size := range []int{16, 256, 1 << 10, 64 << 10} {
		src := benchBuf(size)
		encoded := RawURLEncoding.EncodeToString(src)
		b.Run(fmt.Sprintf("b64x/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := RawURLEncoding.DecodeString(encoded)
				if err != nil {
					b.Fatalf("DecodeString failed | DecodeString 失败: %v", err)
				}
				benchSinkBytes = out
			}
		})
		b.Run(fmt.Sprintf("stdlib/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := base64.RawURLEncoding.DecodeString(encoded)
				if err != nil {
					b.Fatalf("DecodeString failed | DecodeString 失败: %v", err)
				}
				benchSinkBytes = out
			}
		})
	}
}

// BenchmarkAppendEncodeDecode measures the zero-copy append APIs. | BenchmarkAppendEncodeDecode 测量零拷贝追加接口。
func BenchmarkAppendEncodeDecode(b *testing.B) {
	src := benchBuf(1 << 10)
	encoded := make([]byte, 0, StdEncoding.EncodedLen(len(src)))
	b.Run("append_encode", func(b *testing.B) {
		b.SetBytes(int64(len(src)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			encoded = StdEncoding.AppendEncode(encoded[:0], src)
		}
		benchSinkBytes = encoded
	})
	decoded := make([]byte, 0, len(src))
	b.Run("append_decode", func(b *testing.B) {
		b.SetBytes(int64(len(src)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var err error
			decoded, err = StdEncoding.AppendDecode(decoded[:0], encoded)
			if err != nil {
				b.Fatalf("AppendDecode failed | AppendDecode 失败: %v", err)
			}
		}
		benchSinkBytes = decoded
	})
}
