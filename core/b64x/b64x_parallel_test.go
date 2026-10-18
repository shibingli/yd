package b64x

import (
	"bytes"
	"errors"
	"runtime"
	"testing"
)

// TestParallelMatchesSerial verifies every encoding variant and worker count
// produces exactly the serial result. | TestParallelMatchesSerial 验证每个编码变体
// 与工作协程数都产生与串行版本完全一致的结果。
func TestParallelMatchesSerial(t *testing.T) {
	variants := map[string]*Encoding{
		"std": StdEncoding, "raw-std": RawStdEncoding,
		"url": URLEncoding, "raw-url": RawURLEncoding,
	}
	sizes := []int{0, 1, 2, 3, 4, 5, 255, 256 << 10, (256 << 10) + 1, (1 << 20) + 7}
	workers := []int{0, 1, 2, 3, 7, runtime.GOMAXPROCS(0)}
	for name, e := range variants {
		for _, n := range sizes {
			src := randomBytes(n)
			want := make([]byte, e.EncodedLen(len(src)))
			e.Encode(want, src)
			for _, w := range workers {
				got := make([]byte, len(want))
				e.encodeParallel(got, src, w)
				if !bytes.Equal(got, want) {
					t.Fatalf("%s/n=%d/workers=%d encode differs | 编码结果不一致", name, n, w)
				}
				dec := make([]byte, e.DecodedLen(len(want)))
				m, err := e.decodeParallel(dec, want, w)
				if err != nil || m != len(src) || !bytes.Equal(dec[:m], src) {
					t.Fatalf("%s/n=%d/workers=%d decode: n=%d err=%v | 解码结果不一致",
						name, n, w, m, err)
				}
			}
		}
	}
}

// TestParallelDecodeErrors verifies invalid data and short buffers keep the
// serial error contract. | TestParallelDecodeErrors 验证非法输入与短缓冲保持串行
// 版本的错误语义。
func TestParallelDecodeErrors(t *testing.T) {
	src := randomBytes(300 << 10)
	enc := make([]byte, StdEncoding.EncodedLen(len(src)))
	StdEncoding.Encode(enc, src)
	bad := append([]byte(nil), enc...)
	bad[len(bad)/2] = '!'
	out := make([]byte, StdEncoding.DecodedLen(len(bad)))
	if _, err := StdEncoding.decodeParallel(out, bad, 0); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("invalid input error = %v, want ErrInvalidData | 非法输入应返回 ErrInvalidData", err)
	}
	short := make([]byte, StdEncoding.DecodedLen(len(enc))-1)
	if _, err := StdEncoding.decodeParallel(short, enc, 0); !errors.Is(err, ErrShortBuffer) {
		t.Fatalf("short buffer error = %v, want ErrShortBuffer | 短缓冲应返回 ErrShortBuffer", err)
	}
}

// TestParallelDecodeCRLFFallsBack verifies line-wrapped input matches the
// serial decoder, which ignores CR/LF. | TestParallelDecodeCRLFFallsBack 验证按行
// 折行的输入与忽略 CR/LF 的串行解码一致。
func TestParallelDecodeCRLFFallsBack(t *testing.T) {
	src := randomBytes(300 << 10)
	enc := StdEncoding.EncodeToString(src)
	var wrapped bytes.Buffer
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		wrapped.WriteString(enc[i:end])
		wrapped.WriteString("\r\n")
	}
	out := make([]byte, StdEncoding.DecodedLen(wrapped.Len()))
	n, err := StdEncoding.decodeParallel(out, wrapped.Bytes(), 0)
	if err != nil || n != len(src) || !bytes.Equal(out[:n], src) {
		t.Fatalf("CRLF decode: n=%d err=%v | CRLF 解码结果不一致", n, err)
	}
}
