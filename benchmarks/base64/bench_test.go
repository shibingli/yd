package b64bench

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	cwb64 "github.com/cloudwego/base64x"
	crb64 "github.com/cristalhq/base64"

	ydbase64 "github.com/shibingli/ydx/base64"
)

// benchSink keeps results observable. | benchSink 让结果保持可观测。
var benchSink int

// benchSinkAtomic is the concurrency-safe sink used by parallel benchmarks. | benchSinkAtomic
// 是并行基准使用的并发安全 sink。
var benchSinkAtomic atomic.Int64

// benchSelected reports whether name passes the YD_BENCH_ONLY filter. Set it to
// one comma-separated list of codec names to run each implementation in its own
// process and remove same-process ordering and GC bias. An empty value selects
// every codec. | benchSelected 判断 name 是否通过 YD_BENCH_ONLY 过滤。把它设为
// 逗号分隔的编解码器名，可让每个实现独立进程运行，消除同进程顺序与 GC 偏差；
// 空值表示全部选中。
func benchSelected(name string) bool {
	want := os.Getenv("YD_BENCH_ONLY")
	if want == "" {
		return true
	}
	for _, part := range strings.Split(want, ",") {
		if strings.TrimSpace(part) == name {
			return true
		}
	}
	return false
}

// codec is one Base64 implementation under comparison. | codec 是参与对比的一个 Base64 实现。
type codec struct {
	encode func(dst, src []byte)
	decode func(dst, src []byte) (int, error)
	encLen func(int) int
	decLen func(int) int
	name   string
}

// codecs is the comparison set. | codecs 是参与对比的集合。
var codecs = []codec{
	{
		name:   "stdlib",
		encode: base64.StdEncoding.Encode,
		decode: base64.StdEncoding.Decode,
		encLen: base64.StdEncoding.EncodedLen,
		decLen: base64.StdEncoding.DecodedLen,
	},
	{
		name:   "cloudwego/base64x",
		encode: cwb64.StdEncoding.Encode,
		decode: cwb64.StdEncoding.Decode,
		encLen: cwb64.StdEncoding.EncodedLen,
		decLen: cwb64.StdEncoding.DecodedLen,
	},
	{
		name:   "cristalhq/base64",
		encode: crb64.StdEncoding.Encode,
		decode: crb64.StdEncoding.Decode,
		encLen: crb64.StdEncoding.EncodedLen,
		decLen: crb64.StdEncoding.DecodedLen,
	},
	{
		name:   "yd-b64x",
		encode: ydbase64.StdEncoding.Encode,
		decode: ydbase64.StdEncoding.Decode,
		encLen: ydbase64.StdEncoding.EncodedLen,
		decLen: ydbase64.StdEncoding.DecodedLen,
	},
}

// payload builds deterministic bytes of the given size. | payload 构造指定大小的确定性字节。
func payload(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte(i*31 + 7)
	}
	return out
}

// BenchmarkEncode compares Base64 encoding into a preallocated destination. | BenchmarkEncode 对比写入预分配目标缓冲的 Base64 编码。
func BenchmarkEncode(b *testing.B) {
	for _, size := range []int{32, 1 << 10, 64 << 10, 1 << 20} {
		src := payload(size)
		for _, c := range codecs {
			c := c
			if !benchSelected(c.name) {
				continue
			}
			b.Run(fmt.Sprintf("%d/%s", size, c.name), func(b *testing.B) {
				dst := make([]byte, c.encLen(len(src)))
				b.SetBytes(int64(len(src)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					c.encode(dst, src)
				}
				benchSink += len(dst)
			})
		}
	}
}

// BenchmarkEncodeParallel compares aggregate encoding throughput when every
// parallel goroutine uses its own destination buffer. | BenchmarkEncodeParallel
// 对比每个并行协程使用各自目标缓冲时的聚合编码吞吐。
func BenchmarkEncodeParallel(b *testing.B) {
	for _, size := range []int{32, 1 << 10, 64 << 10, 1 << 20} {
		src := payload(size)
		for _, c := range codecs {
			c := c
			if !benchSelected(c.name) {
				continue
			}
			b.Run(fmt.Sprintf("%d/%s", size, c.name), func(b *testing.B) {
				b.SetBytes(int64(len(src)))
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					dst := make([]byte, c.encLen(len(src)))
					local := 0
					for pb.Next() {
						c.encode(dst, src)
						local += len(dst)
					}
					benchSinkAtomic.Add(int64(local))
				})
			})
		}
	}
}

// BenchmarkDecodeParallel compares aggregate decoding throughput when every
// parallel goroutine decodes into its own destination buffer. | BenchmarkDecodeParallel
// 对比每个并行协程解码到各自目标缓冲时的聚合吞吐。
func BenchmarkDecodeParallel(b *testing.B) {
	for _, size := range []int{32, 1 << 10, 64 << 10, 1 << 20} {
		src := payload(size)
		for _, c := range codecs {
			c := c
			if !benchSelected(c.name) {
				continue
			}
			b.Run(fmt.Sprintf("%d/%s", size, c.name), func(b *testing.B) {
				encoded := make([]byte, c.encLen(len(src)))
				c.encode(encoded, src)
				b.SetBytes(int64(len(src)))
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					dst := make([]byte, c.decLen(len(encoded)))
					local := 0
					for pb.Next() {
						n, err := c.decode(dst, encoded)
						if err != nil {
							b.Errorf("%s decode: %v | 解码失败", c.name, err)
							return
						}
						local += n
					}
					benchSinkAtomic.Add(int64(local))
				})
			})
		}
	}
}

// BenchmarkEncodeAutoParallel measures one self-developed EncodeParallel call
// whose worker count is chosen automatically from the current GOMAXPROCS. | BenchmarkEncodeAutoParallel
// 测量一次自研 EncodeParallel 调用，其工作协程数由当前 GOMAXPROCS 自动决定。
func BenchmarkEncodeAutoParallel(b *testing.B) {
	if !benchSelected("yd-b64x") {
		return
	}
	for _, size := range []int{1 << 20, 8 << 20} {
		src := payload(size)
		b.Run(fmt.Sprintf("%d/yd-b64x-auto", size), func(b *testing.B) {
			dst := make([]byte, ydbase64.StdEncoding.EncodedLen(len(src)))
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ydbase64.StdEncoding.EncodeParallel(dst, src)
			}
			benchSink += len(dst)
		})
	}
}

// BenchmarkDecodeAutoParallel measures one self-developed DecodeParallel call
// whose worker count is chosen automatically from the current GOMAXPROCS. | BenchmarkDecodeAutoParallel
// 测量一次自研 DecodeParallel 调用，其工作协程数由当前 GOMAXPROCS 自动决定。
func BenchmarkDecodeAutoParallel(b *testing.B) {
	if !benchSelected("yd-b64x") {
		return
	}
	for _, size := range []int{1 << 20, 8 << 20} {
		src := payload(size)
		encoded := make([]byte, ydbase64.StdEncoding.EncodedLen(len(src)))
		ydbase64.StdEncoding.Encode(encoded, src)
		b.Run(fmt.Sprintf("%d/yd-b64x-auto", size), func(b *testing.B) {
			dst := make([]byte, ydbase64.StdEncoding.DecodedLen(len(encoded)))
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				n, err := ydbase64.StdEncoding.DecodeParallel(dst, encoded)
				if err != nil {
					b.Fatalf("decode: %v | 解码失败", err)
				}
				benchSink += n
			}
		})
	}
}

// TestCodecFixtures proves every compared implementation encodes the benchmark
// payload identically and decodes every peer's output back to the source. | TestCodecFixtures
// 证明每个参与对比的实现对基准载荷产生完全相同的编码，并能把任意对端的输出解回原文。
func TestCodecFixtures(t *testing.T) {
	sizes := []int{0, 1, 2, 3, 4, 5, 31, 32, 33, 1023, 1024, 1025, 65536}
	for _, size := range sizes {
		src := payload(size)
		want := make([]byte, base64.StdEncoding.EncodedLen(len(src)))
		base64.StdEncoding.Encode(want, src)
		for _, c := range codecs {
			if got := c.encLen(len(src)); got != len(want) {
				t.Fatalf("%s/%d EncodedLen = %d, want %d | EncodedLen 不一致", c.name, size, got, len(want))
			}
			encoded := make([]byte, c.encLen(len(src)))
			c.encode(encoded, src)
			if !bytes.Equal(encoded, want) {
				t.Fatalf("%s/%d encoding differs from stdlib | 编码与标准库不一致", c.name, size)
			}
			for _, peer := range codecs {
				dst := make([]byte, peer.decLen(len(want)))
				n, err := peer.decode(dst, want)
				if err != nil || n != len(src) || !bytes.Equal(dst[:n], src) {
					t.Fatalf("%s/%d cannot decode %s output: n=%d err=%v | 无法解码对端输出",
						peer.name, size, c.name, n, err)
				}
			}
		}
	}
}

// BenchmarkDecode compares Base64 decoding into a preallocated destination. | BenchmarkDecode 对比写入预分配目标缓冲的 Base64 解码。
func BenchmarkDecode(b *testing.B) {
	for _, size := range []int{32, 1 << 10, 64 << 10, 1 << 20} {
		src := payload(size)
		for _, c := range codecs {
			c := c
			if !benchSelected(c.name) {
				continue
			}
			b.Run(fmt.Sprintf("%d/%s", size, c.name), func(b *testing.B) {
				encoded := make([]byte, c.encLen(len(src)))
				c.encode(encoded, src)
				dst := make([]byte, c.decLen(len(encoded)))
				if n, err := c.decode(dst, encoded); err != nil || n != len(src) {
					b.Fatalf("%s decode fixture: n=%d err=%v", c.name, n, err)
				}
				if !bytes.Equal(dst[:len(src)], src) {
					b.Fatalf("%s decode mismatch", c.name)
				}
				b.SetBytes(int64(len(src)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					n, err := c.decode(dst, encoded)
					if err != nil {
						b.Fatalf("%s decode: %v", c.name, err)
					}
					benchSink += n
				}
			})
		}
	}
}
