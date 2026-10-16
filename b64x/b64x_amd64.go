//go:build amd64 && !b64x_std

package b64x

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/cristalhq/base64"
)

// Encoding is a Base64 codec that internally delegates to the SIMD-accelerated github.com/cristalhq/base64. | Encoding 是 Base64 编解码器，内部委托给 SIMD 加速的 github.com/cristalhq/base64。
// Its method set aligns with encoding/base64.Encoding, and it additionally offers the zero-copy AppendEncode/AppendDecode. | 方法集与 encoding/base64.Encoding 对齐，并额外提供零拷贝的 AppendEncode/AppendDecode。
type Encoding struct {
	enc *base64.Encoding // underlying SIMD codec | 底层 SIMD 编解码器
	pad bool             // whether to use '=' padding | 是否使用 '=' 填充
}

var (
	// StdEncoding is the standard Base64 encoding defined by RFC 4648 (alphabet A-Za-z0-9+/, '=' padding). | StdEncoding 是 RFC 4648 定义的标准 Base64 编码（字母表 A-Za-z0-9+/，'=' 填充）。
	StdEncoding = &Encoding{enc: base64.StdEncoding, pad: true}
	// RawStdEncoding is the standard Base64 encoding with '=' padding omitted. | RawStdEncoding 是省略 '=' 填充的标准 Base64 编码。
	RawStdEncoding = &Encoding{enc: base64.RawStdEncoding}
	// URLEncoding is the URL- and filename-safe Base64 encoding (the alphabet replaces '+' and '/' with '-' and '_', with '=' padding). | URLEncoding 是 URL 与文件名安全的 Base64 编码（字母表把 '+'、'/' 换成 '-'、'_'，'=' 填充）。
	URLEncoding = &Encoding{enc: base64.URLEncoding, pad: true}
	// RawURLEncoding is the URL-safe Base64 encoding with '=' padding omitted. | RawURLEncoding 是省略 '=' 填充的 URL 安全 Base64 编码。
	RawURLEncoding = &Encoding{enc: base64.RawURLEncoding}
)

// Backend returns the name of the backend used by the current build. On amd64 without the b64x_std tag it is "base64x". | Backend 返回当前构建使用的后端名称。amd64 且未指定 b64x_std 标签时为 "base64x"。
func Backend() string { return "base64x" }

// EncodedLen returns the encoded length of an n-byte source, exactly matching encoding/base64.Encoding.EncodedLen. | EncodedLen 返回 n 字节源码编码后的长度，与 encoding/base64.Encoding.EncodedLen 完全一致。
func (e *Encoding) EncodedLen(n int) int { return e.enc.EncodedLen(n) }

// DecodedLen returns the maximum number of bytes after decoding Base64 text of length n, exactly matching | DecodedLen 返回长度 n 的 Base64 文本解码后的最大字节数，与
// encoding/base64.Encoding.DecodedLen. | encoding/base64.Encoding.DecodedLen 完全一致。
// The underlying SIMD decoder writes out whole 4-byte blocks, so the actual decode region needs a 1-byte write margin; | 底层 SIMD 解码器以 4 字节整块写出，实际解码区域需要 1 字节写余量，
// that margin is covered by b64x's internal buffer (see Decode and AppendDecode), and callers need not care about it. | 该余量由 b64x 内部缓冲区承担（见 Decode 与 AppendDecode），调用方无需关心。
func (e *Encoding) DecodedLen(n int) int {
	if e.pad {
		return n / 4 * 3
	}
	return n/4*3 + n%4*6/8
}

// safeDecodedLen returns the maximum destination length the underlying SIMD decoder may touch in a single decode, | safeDecodedLen 返回底层 SIMD 解码器一次解码可能触碰的最大目标长度，
// that is, DecodedLen plus the write margin required for the trailing whole-block write. | 即 DecodedLen 加上尾部整块写出所需的写余量。
func (e *Encoding) safeDecodedLen(n int) int { return e.enc.DecodedLen(n) }

// Encode writes the encoding of src into dst; it requires len(dst) >= EncodedLen(len(src)), otherwise it panics (consistent with the standard library). | Encode 把 src 编码写入 dst，要求 len(dst) >= EncodedLen(len(src))，否则 panic（与标准库一致）。
func (e *Encoding) Encode(dst, src []byte) {
	if len(src) == 0 {
		return
	}
	e.enc.Encode(dst[:e.EncodedLen(len(src))], src)
}

// AppendEncode appends the encoding of src to dst and returns the extended slice. | AppendEncode 把 src 的编码结果追加到 dst 并返回扩展后的切片。
// When cap(dst)-len(dst) >= EncodedLen(len(src)) it reuses the underlying array of dst, producing no new allocation. | 当 cap(dst)-len(dst) >= EncodedLen(len(src)) 时复用 dst 的底层数组，不产生新的分配。
func (e *Encoding) AppendEncode(dst, src []byte) []byte {
	if len(src) == 0 {
		return dst
	}
	n := e.EncodedLen(len(src))
	base := len(dst)
	dst = slices.Grow(dst, n)[:base+n]
	e.enc.Encode(dst[base:], src)
	return dst
}

// EncodeToString returns the Base64-encoded string of src. | EncodeToString 返回 src 的 Base64 编码字符串。
func (e *Encoding) EncodeToString(src []byte) string { return e.enc.EncodeToString(src) }

// Decode decodes the Base64 text src into dst and returns the number of bytes written. | Decode 把 Base64 文本 src 解码写入 dst，返回写入的字节数。
// It requires len(dst) >= DecodedLen(len(src)), otherwise it returns ErrShortBuffer without writing any data. | 要求 len(dst) >= DecodedLen(len(src))，否则返回 ErrShortBuffer 且不写入任何数据。
// On decode failure it returns (0, err), and dst keeps the content it had before the call. | 解码失败时返回 (0, err)，dst 内容保持为调用前的状态。
// As with encoding/base64, newline characters '\r' and '\n' in the input are ignored. | 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
func (e *Encoding) Decode(dst, src []byte) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	plain, tmp := stripNewlines(src)
	if tmp != nil {
		defer releaseScratch(tmp)
	}
	if len(dst) < e.DecodedLen(len(plain)) {
		return 0, ErrShortBuffer
	}
	// The SIMD decoder writes out whole 4-byte blocks and needs a 1-byte write margin, hence the relay through a pooled buffer. | SIMD 解码器会按 4 字节整块写出，需要 1 字节写余量，故经池化缓冲区中转。
	p := acquireScratch(e.safeDecodedLen(len(plain)))
	defer releaseScratch(p)
	n, err := e.enc.Decode(*p, plain)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	copy(dst, (*p)[:n])
	return n, nil
}

// DecodeString returns the decoded bytes of the Base64 text s, or (nil, err) on decode failure. | DecodeString 返回 Base64 文本 s 解码后的字节，解码失败时返回 (nil, err)。
// As with encoding/base64, newline characters '\r' and '\n' in the input are ignored. | 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
func (e *Encoding) DecodeString(s string) ([]byte, error) {
	if len(s) == 0 {
		return []byte{}, nil
	}
	if strings.IndexAny(s, "\r\n") < 0 {
		// Fast path: no newlines, hand it straight to the underlying layer (which converts the string without copying internally and allocates only the result buffer). | 快路径：无换行，直接交给底层（其内部零拷贝转换 string，只分配结果缓冲区）。
		out, err := e.enc.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidData, err)
		}
		return out, nil
	}
	p := acquireScratch(len(s))
	defer releaseScratch(p)
	plain := (*p)[:0]
	for i := 0; i < len(s); i++ {
		if c := s[i]; c != '\n' && c != '\r' {
			plain = append(plain, c)
		}
	}
	out := make([]byte, e.safeDecodedLen(len(plain)))
	n, err := e.enc.Decode(out, plain)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	return out[:n], nil
}

// AppendDecode appends the decode result of the Base64 text src to dst and returns the extended slice. | AppendDecode 把 Base64 文本 src 的解码结果追加到 dst 并返回扩展后的切片。
// When the available capacity can hold the decode result it reuses the underlying array of dst, producing no new allocation. | 当可用容量足够容纳解码结果时复用 dst 的底层数组，不产生新的分配。
// On decode failure it returns (dst, err), and dst keeps its content unchanged. | 解码失败时返回 (dst, err)，dst 内容保持不变。
// As with encoding/base64, newline characters '\r' and '\n' in the input are ignored. | 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
func (e *Encoding) AppendDecode(dst, src []byte) ([]byte, error) {
	if len(src) == 0 {
		return dst, nil
	}
	plain, tmp := stripNewlines(src)
	if tmp != nil {
		defer releaseScratch(tmp)
	}
	base := len(dst)
	room := e.safeDecodedLen(len(plain))
	dst = slices.Grow(dst, room)[:base+room]
	n, err := e.enc.Decode(dst[base:], plain)
	if err != nil {
		return dst[:base], fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	return dst[:base+n], nil
}

// maxPooled is the capacity limit for buffers returned to the pool; buffers larger than this are left to the GC, so the pool does not hold large memory for long. | maxPooled 是放回池中的缓冲区容量上限，超过该值的缓冲区交给 GC，避免池长期占用大内存。
const maxPooled = 64 << 10

// scratchPool reuses the temporary buffers needed for decoding and newline stripping, avoiding an allocation on every call. | scratchPool 复用解码与换行剔除所需的临时缓冲区，避免每次调用都产生分配。
var scratchPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 1024)
		return &b
	},
}

// acquireScratch takes a temporary buffer of exactly length n; the caller must return it via releaseScratch. | acquireScratch 取出长度恰好为 n 的临时缓冲区，调用方必须通过 releaseScratch 归还。
func acquireScratch(n int) *[]byte {
	p := scratchPool.Get().(*[]byte)
	b := *p
	if cap(b) < n {
		b = make([]byte, n)
	} else {
		b = b[:n]
	}
	*p = b
	return p
}

// releaseScratch returns a buffer obtained from acquireScratch; when its capacity exceeds maxPooled it is simply dropped. | releaseScratch 归还 acquireScratch 取出的缓冲区；容量超过 maxPooled 时直接丢弃。
func releaseScratch(p *[]byte) {
	if cap(*p) > maxPooled {
		return
	}
	*p = (*p)[:0]
	scratchPool.Put(p)
}

// stripNewlines strips the newline characters '\r' and '\n' from src, staying consistent with encoding/base64's lenient semantics. | stripNewlines 剔除 src 中的换行符 '\r'、'\n'，与 encoding/base64 的宽容语义保持一致。
// When the input contains no newline it returns src unchanged with tmp nil; otherwise it uses a pooled buffer, and the caller must releaseScratch(tmp). | 输入不含换行时原样返回 src 且 tmp 为 nil；否则使用池化缓冲区，调用方必须 releaseScratch(tmp)。
func stripNewlines(src []byte) (out []byte, tmp *[]byte) {
	if bytes.IndexAny(src, "\r\n") < 0 {
		return src, nil
	}
	p := acquireScratch(len(src))
	plain := (*p)[:0]
	for _, c := range src {
		if c != '\n' && c != '\r' {
			plain = append(plain, c)
		}
	}
	return plain, p
}
