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

// Encoding 是 Base64 编解码器，内部委托给 SIMD 加速的 github.com/cristalhq/base64。
// 方法集与 encoding/base64.Encoding 对齐，并额外提供零拷贝的 AppendEncode/AppendDecode。
type Encoding struct {
	enc *base64.Encoding // 底层 SIMD 编解码器
	pad bool             // 是否使用 '=' 填充
}

var (
	// StdEncoding 是 RFC 4648 定义的标准 Base64 编码（字母表 A-Za-z0-9+/，'=' 填充）。
	StdEncoding = &Encoding{enc: base64.StdEncoding, pad: true}
	// RawStdEncoding 是省略 '=' 填充的标准 Base64 编码。
	RawStdEncoding = &Encoding{enc: base64.RawStdEncoding}
	// URLEncoding 是 URL 与文件名安全的 Base64 编码（字母表把 '+'、'/' 换成 '-'、'_'，'=' 填充）。
	URLEncoding = &Encoding{enc: base64.URLEncoding, pad: true}
	// RawURLEncoding 是省略 '=' 填充的 URL 安全 Base64 编码。
	RawURLEncoding = &Encoding{enc: base64.RawURLEncoding}
)

// Backend 返回当前构建使用的后端名称。amd64 且未指定 b64x_std 标签时为 "base64x"。
func Backend() string { return "base64x" }

// EncodedLen 返回 n 字节源码编码后的长度，与 encoding/base64.Encoding.EncodedLen 完全一致。
func (e *Encoding) EncodedLen(n int) int { return e.enc.EncodedLen(n) }

// DecodedLen 返回长度 n 的 Base64 文本解码后的最大字节数，与
// encoding/base64.Encoding.DecodedLen 完全一致。
// 底层 SIMD 解码器以 4 字节整块写出，实际解码区域需要 1 字节写余量，
// 该余量由 b64x 内部缓冲区承担（见 Decode 与 AppendDecode），调用方无需关心。
func (e *Encoding) DecodedLen(n int) int {
	if e.pad {
		return n / 4 * 3
	}
	return n/4*3 + n%4*6/8
}

// safeDecodedLen 返回底层 SIMD 解码器一次解码可能触碰的最大目标长度，
// 即 DecodedLen 加上尾部整块写出所需的写余量。
func (e *Encoding) safeDecodedLen(n int) int { return e.enc.DecodedLen(n) }

// Encode 把 src 编码写入 dst，要求 len(dst) >= EncodedLen(len(src))，否则 panic（与标准库一致）。
func (e *Encoding) Encode(dst, src []byte) {
	if len(src) == 0 {
		return
	}
	e.enc.Encode(dst[:e.EncodedLen(len(src))], src)
}

// AppendEncode 把 src 的编码结果追加到 dst 并返回扩展后的切片。
// 当 cap(dst)-len(dst) >= EncodedLen(len(src)) 时复用 dst 的底层数组，不产生新的分配。
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

// EncodeToString 返回 src 的 Base64 编码字符串。
func (e *Encoding) EncodeToString(src []byte) string { return e.enc.EncodeToString(src) }

// Decode 把 Base64 文本 src 解码写入 dst，返回写入的字节数。
// 要求 len(dst) >= DecodedLen(len(src))，否则返回 ErrShortBuffer 且不写入任何数据。
// 解码失败时返回 (0, err)，dst 内容保持为调用前的状态。
// 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
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
	// SIMD 解码器会按 4 字节整块写出，需要 1 字节写余量，故经池化缓冲区中转。
	p := acquireScratch(e.safeDecodedLen(len(plain)))
	defer releaseScratch(p)
	n, err := e.enc.Decode(*p, plain)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	copy(dst, (*p)[:n])
	return n, nil
}

// DecodeString 返回 Base64 文本 s 解码后的字节，解码失败时返回 (nil, err)。
// 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
func (e *Encoding) DecodeString(s string) ([]byte, error) {
	if len(s) == 0 {
		return []byte{}, nil
	}
	if strings.IndexAny(s, "\r\n") < 0 {
		// 快路径：无换行，直接交给底层（其内部零拷贝转换 string，只分配结果缓冲区）。
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

// AppendDecode 把 Base64 文本 src 的解码结果追加到 dst 并返回扩展后的切片。
// 当可用容量足够容纳解码结果时复用 dst 的底层数组，不产生新的分配。
// 解码失败时返回 (dst, err)，dst 内容保持不变。
// 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
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

// maxPooled 是放回池中的缓冲区容量上限，超过该值的缓冲区交给 GC，避免池长期占用大内存。
const maxPooled = 64 << 10

// scratchPool 复用解码与换行剔除所需的临时缓冲区，避免每次调用都产生分配。
var scratchPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 1024)
		return &b
	},
}

// acquireScratch 取出长度恰好为 n 的临时缓冲区，调用方必须通过 releaseScratch 归还。
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

// releaseScratch 归还 acquireScratch 取出的缓冲区；容量超过 maxPooled 时直接丢弃。
func releaseScratch(p *[]byte) {
	if cap(*p) > maxPooled {
		return
	}
	*p = (*p)[:0]
	scratchPool.Put(p)
}

// stripNewlines 剔除 src 中的换行符 '\r'、'\n'，与 encoding/base64 的宽容语义保持一致。
// 输入不含换行时原样返回 src 且 tmp 为 nil；否则使用池化缓冲区，调用方必须 releaseScratch(tmp)。
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
