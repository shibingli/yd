package compressbench

import (
	"bytes"
	stdgzip "compress/gzip"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/golang/snappy"
	kgzip "github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"

	"github.com/shibingli/yd/core/jsonx"
)

// writeCloser is a resetable streaming compressor. | writeCloser 是可重置的流式压缩器。
type writeCloser interface {
	io.WriteCloser
	Reset(io.Writer)
}

// readResetter is a resetable streaming decompressor. | readResetter 是可重置的流式解压器。
type readResetter interface {
	io.Reader
	Reset(io.Reader) error
}

// codec is one compressor under comparison. | codec 是参与对比的一个压缩器。
type codec struct {
	name      string
	newWriter func(io.Writer) (writeCloser, error)
	newReader func(io.Reader) (readResetter, error)
}

// brotliReader adapts brotli.Reader to the reset mechanism. | brotliReader 把 brotli.Reader 适配为
// 可重置接口。
type brotliReader struct{ inner *brotli.Reader }

func (b *brotliReader) Read(p []byte) (int, error) { return b.inner.Read(p) }
func (b *brotliReader) Reset(r io.Reader) error    { b.inner = brotli.NewReader(r); return nil }

// zstdReader adapts zstd.Decoder, whose Reset returns an error. | zstdReader 适配 Reset 返回错误的
// zstd.Decoder。
type zstdReader struct{ dec *zstd.Decoder }

func (z *zstdReader) Read(p []byte) (int, error) { return z.dec.Read(p) }
func (z *zstdReader) Reset(r io.Reader) error    { return z.dec.Reset(r) }

// snappyReader adapts snappy.Reader, whose Reset has no return value. | snappyReader 适配 Reset
// 无返回值的 snappy.Reader。
type snappyReader struct{ inner *snappy.Reader }

func (s *snappyReader) Read(p []byte) (int, error) { return s.inner.Read(p) }
func (s *snappyReader) Reset(r io.Reader) error    { s.inner.Reset(r); return nil }

// lz4Reader adapts lz4.Reader, whose Reset has no return value. | lz4Reader 适配 Reset 无返回值的
// lz4.Reader。
type lz4Reader struct{ inner *lz4.Reader }

func (l *lz4Reader) Read(p []byte) (int, error) { return l.inner.Read(p) }
func (l *lz4Reader) Reset(r io.Reader) error    { l.inner.Reset(r); return nil }

// codecs is the comparison set. | codecs 是参与对比的集合。
var codecs = []codec{
	{
		name:      "gzip/stdlib",
		newWriter: func(w io.Writer) (writeCloser, error) { return stdgzip.NewWriter(w), nil },
		newReader: func(r io.Reader) (readResetter, error) { return stdgzip.NewReader(r) },
	},
	{
		name:      "gzip/klauspost",
		newWriter: func(w io.Writer) (writeCloser, error) { return kgzip.NewWriter(w), nil },
		newReader: func(r io.Reader) (readResetter, error) { return kgzip.NewReader(r) },
	},
	{
		name: "gzip/klauspost-fast",
		newWriter: func(w io.Writer) (writeCloser, error) {
			return kgzip.NewWriterLevel(w, kgzip.BestSpeed)
		},
		newReader: func(r io.Reader) (readResetter, error) { return kgzip.NewReader(r) },
	},
	{
		name: "zstd/default",
		newWriter: func(w io.Writer) (writeCloser, error) {
			return zstd.NewWriter(w, zstd.WithEncoderConcurrency(1))
		},
		newReader: func(r io.Reader) (readResetter, error) {
			dec, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
			if err != nil {
				return nil, err
			}
			return &zstdReader{dec: dec}, nil
		},
	},
	{
		name: "zstd/fastest",
		newWriter: func(w io.Writer) (writeCloser, error) {
			return zstd.NewWriter(w, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedFastest))
		},
		newReader: func(r io.Reader) (readResetter, error) {
			dec, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
			if err != nil {
				return nil, err
			}
			return &zstdReader{dec: dec}, nil
		},
	},
	{
		name:      "brotli/default",
		newWriter: func(w io.Writer) (writeCloser, error) { return brotli.NewWriter(w), nil },
		newReader: func(r io.Reader) (readResetter, error) { return &brotliReader{inner: brotli.NewReader(r)}, nil },
	},
	{
		name: "brotli/q1",
		newWriter: func(w io.Writer) (writeCloser, error) {
			return brotli.NewWriterLevel(w, 1), nil
		},
		newReader: func(r io.Reader) (readResetter, error) { return &brotliReader{inner: brotli.NewReader(r)}, nil },
	},
	{
		name: "brotli/q4",
		newWriter: func(w io.Writer) (writeCloser, error) {
			return brotli.NewWriterLevel(w, 4), nil
		},
		newReader: func(r io.Reader) (readResetter, error) { return &brotliReader{inner: brotli.NewReader(r)}, nil },
	},
	{
		name: "brotli/q5",
		newWriter: func(w io.Writer) (writeCloser, error) {
			return brotli.NewWriterLevel(w, 5), nil
		},
		newReader: func(r io.Reader) (readResetter, error) { return &brotliReader{inner: brotli.NewReader(r)}, nil },
	},
	{
		name: "brotli/q9",
		newWriter: func(w io.Writer) (writeCloser, error) {
			return brotli.NewWriterLevel(w, 9), nil
		},
		newReader: func(r io.Reader) (readResetter, error) { return &brotliReader{inner: brotli.NewReader(r)}, nil },
	},

	{
		name: "snappy",
		newWriter: func(w io.Writer) (writeCloser, error) {
			return snappy.NewBufferedWriter(w), nil
		},
		newReader: func(r io.Reader) (readResetter, error) { return &snappyReader{inner: snappy.NewReader(r)}, nil },
	},
	{
		name:      "lz4/default",
		newWriter: func(w io.Writer) (writeCloser, error) { return lz4.NewWriter(w), nil },
		newReader: func(r io.Reader) (readResetter, error) { return &lz4Reader{inner: lz4.NewReader(r)}, nil },
	},
}

// textPayload builds a compressible text payload. | textPayload 构造可压缩的文本载荷。
func textPayload(n int) []byte {
	var b strings.Builder
	for b.Len() < n {
		b.WriteString("The quick brown fox jumps over the lazy dog. | 敏捷的棕色狐狸跃过懒狗。")
		b.WriteString(" 0123456789 abcdefghijklmnopqrstuvwxyz\n")
	}
	return []byte(b.String()[:n])
}

// jsonPayload builds a JSON payload. | jsonPayload 构造 JSON 载荷。
func jsonPayload(n int) []byte {
	type record struct {
		ID    int      `json:"id"`
		Name  string   `json:"name"`
		Email string   `json:"email"`
		Tags  []string `json:"tags"`
		Note  string   `json:"note"`
	}
	var records []record
	for i := 0; len(records) < n/128; i++ {
		records = append(records, record{
			ID:    i,
			Name:  fmt.Sprintf("user-%d", i),
			Email: fmt.Sprintf("user%d@example.com", i),
			Tags:  []string{"alpha", "beta", "gamma"},
			Note:  "repetitive payload for compression benchmarks | 用于压缩基准的重复载荷",
		})
	}
	raw, _ := jsonx.Marshal(records)
	return raw
}

// randomPayload builds deterministic incompressible bytes. | randomPayload 构造确定性的不可压缩字节。
func randomPayload(n int) []byte {
	out := make([]byte, n)
	rng := rand.New(rand.NewSource(42))
	_, _ = rng.Read(out)
	return out
}

var (
	textData   = textPayload(64 << 10)
	jsonData   = jsonPayload(64 << 10)
	randomData = randomPayload(64 << 10)
)

// datasets is the payload set. | datasets 是载荷集合。
var datasets = []struct {
	name string
	data []byte
}{
	{name: "text", data: textData},
	{name: "json", data: jsonData},
	{name: "random", data: randomData},
}

// benchCompress measures streaming compression into io.Discard with a reused encoder. | benchCompress
// 使用复用的编码器测量流式压缩到 io.Discard 的吞吐。
func benchCompress(b *testing.B, c codec, data []byte) {
	enc, err := c.newWriter(io.Discard)
	if err != nil {
		b.Fatalf("%s writer: %v", c.name, err)
	}
	defer func() { _ = enc.Close() }()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		enc.Reset(io.Discard)
		if _, err := enc.Write(data); err != nil {
			b.Fatalf("%s write: %v", c.name, err)
		}
		if err := enc.Close(); err != nil {
			b.Fatalf("%s close: %v", c.name, err)
		}
	}
}

// benchDecompress measures streaming decompression with a reused decoder. | benchDecompress
// 使用复用的解码器测量流式解压吞吐。
func benchDecompress(b *testing.B, c codec, data []byte) {
	var compressed bytes.Buffer
	enc, err := c.newWriter(&compressed)
	if err != nil {
		b.Fatalf("%s writer: %v", c.name, err)
	}
	if _, err := enc.Write(data); err != nil {
		b.Fatalf("%s write: %v", c.name, err)
	}
	if err := enc.Close(); err != nil {
		b.Fatalf("%s close: %v", c.name, err)
	}

	src := bytes.NewReader(compressed.Bytes())
	dec, err := c.newReader(src)
	if err != nil {
		b.Fatalf("%s reader: %v", c.name, err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src.Reset(compressed.Bytes())
		if err := dec.Reset(src); err != nil {
			b.Fatalf("%s reset: %v", c.name, err)
		}
		if _, err := io.Copy(io.Discard, dec); err != nil {
			b.Fatalf("%s read: %v", c.name, err)
		}
	}
}

// BenchmarkCompress compares compression throughput across codecs and payloads. | BenchmarkCompress
// 对比各编解码器与载荷的压缩吞吐。
func BenchmarkCompress(b *testing.B) {
	for _, ds := range datasets {
		for _, c := range codecs {
			ds, c := ds, c
			b.Run(ds.name+"/"+c.name, func(b *testing.B) { benchCompress(b, c, ds.data) })
		}
	}
}

// BenchmarkDecompress compares decompression throughput across codecs and payloads. | BenchmarkDecompress
// 对比各编解码器与载荷的解压吞吐。
func BenchmarkDecompress(b *testing.B) {
	for _, ds := range datasets {
		for _, c := range codecs {
			ds, c := ds, c
			b.Run(ds.name+"/"+c.name, func(b *testing.B) { benchDecompress(b, c, ds.data) })
		}
	}
}

// TestRatioAndRoundTrip reports compression ratios and proves every codec round-trips the
// payloads byte for byte. | TestRatioAndRoundTrip 报告压缩率，并证明每个编解码器都能逐字节往返。
func TestRatioAndRoundTrip(t *testing.T) {
	payloads := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "one-byte", data: []byte("x")},
		{name: "text", data: textData},
		{name: "json", data: jsonData},
		{name: "random", data: randomData},
	}
	for _, c := range codecs {
		for _, p := range payloads {
			var compressed bytes.Buffer
			enc, err := c.newWriter(&compressed)
			if err != nil {
				t.Fatalf("%s writer: %v", c.name, err)
			}
			if _, err := enc.Write(p.data); err != nil {
				t.Fatalf("%s/%s write: %v", c.name, p.name, err)
			}
			if err := enc.Close(); err != nil {
				t.Fatalf("%s/%s close: %v", c.name, p.name, err)
			}
			dec, err := c.newReader(bytes.NewReader(compressed.Bytes()))
			if err != nil {
				t.Fatalf("%s/%s reader: %v", c.name, p.name, err)
			}
			got, err := io.ReadAll(dec)
			if err != nil {
				t.Fatalf("%s/%s read: %v", c.name, p.name, err)
			}
			if !bytes.Equal(got, p.data) {
				t.Fatalf("%s/%s round trip differs | %s/%s 往返结果不一致", c.name, p.name, c.name, p.name)
			}
			if p.name == "text" || p.name == "json" || p.name == "random" {
				ratio := float64(compressed.Len()) / float64(len(p.data))
				t.Logf("%-22s %-7s ratio=%.3f size=%d->%d", c.name, p.name, ratio, len(p.data), compressed.Len())
			}
		}
	}
}
