//go:build !loong64

package b64x

import (
	stdb64 "encoding/base64"
	"fmt"
	"testing"

	cwb64 "github.com/cloudwego/base64x"
	crb64 "github.com/cristalhq/base64"
)

// BenchmarkBackendsEncode compares the standard library, cloudwego/base64x and
// cristalhq/base64 on the same buffers. | BenchmarkBackendsEncode 在同一缓冲上对比标准库、
// cloudwego/base64x 与 cristalhq/base64。
func BenchmarkBackendsEncode(b *testing.B) {
	for _, size := range []int{16, 1 << 10, 64 << 10} {
		src := benchBuf(size)
		dst := make([]byte, stdb64.StdEncoding.EncodedLen(size))
		b.Run(fmt.Sprintf("stdlib/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				stdb64.StdEncoding.Encode(dst, src)
			}
			benchSinkBytes = dst
		})
		b.Run(fmt.Sprintf("cloudwego/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cwb64.StdEncoding.Encode(dst, src)
			}
			benchSinkBytes = dst
		})
		b.Run(fmt.Sprintf("cristalhq/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				crb64.StdEncoding.Encode(dst, src)
			}
			benchSinkBytes = dst
		})
	}
}

// BenchmarkBackendsDecode compares the three decoders with a reused destination. | BenchmarkBackendsDecode 在复用目标缓冲的情况下对比三个解码器。
func BenchmarkBackendsDecode(b *testing.B) {
	for _, size := range []int{16, 1 << 10, 64 << 10} {
		src := benchBuf(size)
		encoded := make([]byte, stdb64.StdEncoding.EncodedLen(size))
		stdb64.StdEncoding.Encode(encoded, src)
		dst := make([]byte, stdb64.StdEncoding.DecodedLen(len(encoded)))
		b.Run(fmt.Sprintf("stdlib/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := stdb64.StdEncoding.Decode(dst, encoded); err != nil {
					b.Fatal(err)
				}
			}
			benchSinkBytes = dst
		})
		b.Run(fmt.Sprintf("cloudwego/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := cwb64.StdEncoding.Decode(dst, encoded); err != nil {
					b.Fatal(err)
				}
			}
			benchSinkBytes = dst
		})
		b.Run(fmt.Sprintf("cristalhq/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := crb64.StdEncoding.Decode(dst, encoded); err != nil {
					b.Fatal(err)
				}
			}
			benchSinkBytes = dst
		})
	}
}

// BenchmarkBackendsEncodeToString compares the allocating string encoders. | BenchmarkBackendsEncodeToString 对比分配式的字符串编码入口。
func BenchmarkBackendsEncodeToString(b *testing.B) {
	for _, size := range []int{16, 1 << 10} {
		src := benchBuf(size)
		b.Run(fmt.Sprintf("stdlib/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchSinkString = stdb64.StdEncoding.EncodeToString(src)
			}
		})
		b.Run(fmt.Sprintf("cloudwego/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchSinkString = cwb64.StdEncoding.EncodeToString(src)
			}
		})
		b.Run(fmt.Sprintf("cristalhq/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchSinkString = crb64.StdEncoding.EncodeToString(src)
			}
		})
	}
}

// BenchmarkBackendsDecodeString compares the allocating string decoders. | BenchmarkBackendsDecodeString 对比分配式的字符串解码入口。
func BenchmarkBackendsDecodeString(b *testing.B) {
	for _, size := range []int{16, 1 << 10} {
		encoded := stdb64.StdEncoding.EncodeToString(benchBuf(size))
		b.Run(fmt.Sprintf("stdlib/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := stdb64.StdEncoding.DecodeString(encoded)
				if err != nil {
					b.Fatal(err)
				}
				benchSinkBytes = out
			}
		})
		b.Run(fmt.Sprintf("cloudwego/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := cwb64.StdEncoding.DecodeString(encoded)
				if err != nil {
					b.Fatal(err)
				}
				benchSinkBytes = out
			}
		})
		b.Run(fmt.Sprintf("cristalhq/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := crb64.StdEncoding.DecodeString(encoded)
				if err != nil {
					b.Fatal(err)
				}
				benchSinkBytes = out
			}
		})
	}
}

// BenchmarkBackendsCrossover finds the size where cloudwego overtakes cristalhq
// for each direction, so the runtime thresholds can be set from data. | BenchmarkBackendsCrossover
// 找出每个方向上 cloudwego 超过 cristalhq 的尺寸，用数据确定运行期阈值。
func BenchmarkBackendsCrossover(b *testing.B) {
	sizes := []int{8, 16, 32, 64, 128, 256, 512, 1024}
	for _, size := range sizes {
		src := benchBuf(size)
		dst := make([]byte, stdb64.StdEncoding.EncodedLen(size))
		b.Run(fmt.Sprintf("encode/cloudwego/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cwb64.StdEncoding.Encode(dst, src)
			}
		})
		b.Run(fmt.Sprintf("encode/cristalhq/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				crb64.StdEncoding.Encode(dst, src)
			}
		})
		encoded := make([]byte, stdb64.StdEncoding.EncodedLen(size))
		stdb64.StdEncoding.Encode(encoded, src)
		out := make([]byte, stdb64.StdEncoding.DecodedLen(len(encoded)))
		b.Run(fmt.Sprintf("decode/cloudwego/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := cwb64.StdEncoding.Decode(out, encoded); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("decode/cristalhq/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := crb64.StdEncoding.Decode(out, encoded); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkBackendsCrossoverString finds the string-form crossover. | BenchmarkBackendsCrossoverString 找出字符串入口的交叉点。
func BenchmarkBackendsCrossoverString(b *testing.B) {
	for _, size := range []int{16, 32, 64, 128, 256, 512} {
		src := benchBuf(size)
		b.Run(fmt.Sprintf("encode_string/cloudwego/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchSinkString = cwb64.StdEncoding.EncodeToString(src)
			}
		})
		b.Run(fmt.Sprintf("encode_string/cristalhq/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchSinkString = crb64.StdEncoding.EncodeToString(src)
			}
		})
		encoded := stdb64.StdEncoding.EncodeToString(src)
		b.Run(fmt.Sprintf("decode_string/cloudwego/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := cwb64.StdEncoding.DecodeString(encoded)
				if err != nil {
					b.Fatal(err)
				}
				benchSinkBytes = out
			}
		})
		b.Run(fmt.Sprintf("decode_string/cristalhq/%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := crb64.StdEncoding.DecodeString(encoded)
				if err != nil {
					b.Fatal(err)
				}
				benchSinkBytes = out
			}
		})
	}
}
