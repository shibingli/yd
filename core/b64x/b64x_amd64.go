//go:build amd64 && !b64x_std

package b64x

import (
	"slices"

	b64 "github.com/cloudwego/base64x"

	"github.com/shibingli/yd/core/errx"
)

// Encoding is a Base64 codec built on the AVX2/SSE-accelerated
// github.com/cloudwego/base64x. | Encoding 是建立在 AVX2/SSE 加速的
// github.com/cloudwego/base64x 之上的 Base64 编解码器。
//
// All methods use the SIMD codec; the append forms use EncodeUnsafe/DecodeUnsafe,
// which extend the destination slice directly and therefore avoid a second copy. | 所有方法
// 都走 SIMD 编解码器；追加式接口使用 EncodeUnsafe/DecodeUnsafe，直接扩展目标切片，避免二次拷贝。
type Encoding struct {
	enc b64.Encoding
}

var (
	// StdEncoding is the standard Base64 encoding defined by RFC 4648 (alphabet A-Za-z0-9+/, '=' padding). | StdEncoding 是 RFC 4648 定义的标准 Base64 编码（字母表 A-Za-z0-9+/，'=' 填充）。
	StdEncoding = &Encoding{enc: b64.StdEncoding}
	// RawStdEncoding is the standard Base64 encoding with '=' padding omitted. | RawStdEncoding 是省略 '=' 填充的标准 Base64 编码。
	RawStdEncoding = &Encoding{enc: b64.RawStdEncoding}
	// URLEncoding is the URL- and filename-safe Base64 encoding (the alphabet replaces '+' and '/' with '-' and '_', with '=' padding). | URLEncoding 是 URL 与文件名安全的 Base64 编码（字母表把 '+'、'/' 换成 '-'、'_'，'=' 填充）。
	URLEncoding = &Encoding{enc: b64.URLEncoding}
	// RawURLEncoding is the URL-safe Base64 encoding with '=' padding omitted. | RawURLEncoding 是省略 '=' 填充的 URL 安全 Base64 编码。
	RawURLEncoding = &Encoding{enc: b64.RawURLEncoding}
)

// Backend returns the name of the backend used by the current build. | Backend 返回当前构建使用的后端名称。
func Backend() string { return "cloudwego/base64x" }

// EncodedLen returns the encoded length of an n-byte source, exactly matching encoding/base64.Encoding.EncodedLen. | EncodedLen 返回 n 字节源码编码后的长度，与 encoding/base64.Encoding.EncodedLen 完全一致。
func (e *Encoding) EncodedLen(n int) int { return e.enc.EncodedLen(n) }

// DecodedLen returns the maximum number of bytes after decoding Base64 text of length n, exactly matching | DecodedLen 返回长度 n 的 Base64 文本解码后的最大字节数，与
// encoding/base64.Encoding.DecodedLen. | encoding/base64.Encoding.DecodedLen 完全一致。
func (e *Encoding) DecodedLen(n int) int { return e.enc.DecodedLen(n) }

// Encode writes the encoding of src into dst; it requires len(dst) >= EncodedLen(len(src)), otherwise it panics (consistent with the standard library). | Encode 把 src 编码写入 dst，要求 len(dst) >= EncodedLen(len(src))，否则 panic（与标准库一致）。
func (e *Encoding) Encode(dst, src []byte) {
	if len(src) == 0 {
		return
	}
	e.enc.Encode(dst, src)
}

// AppendEncode appends the encoding of src to dst and returns the extended slice. | AppendEncode 把 src 的编码结果追加到 dst 并返回扩展后的切片。
// When cap(dst)-len(dst) >= EncodedLen(len(src)) it reuses the underlying array of dst, producing no new allocation. | 当 cap(dst)-len(dst) >= EncodedLen(len(src)) 时复用 dst 的底层数组，不产生新的分配。
func (e *Encoding) AppendEncode(dst, src []byte) []byte {
	if len(src) == 0 {
		return dst
	}
	dst = slices.Grow(dst, e.EncodedLen(len(src)))
	e.enc.EncodeUnsafe(&dst, src)
	return dst
}

// EncodeToString returns the Base64-encoded string of src. | EncodeToString 返回 src 的 Base64 编码字符串。
func (e *Encoding) EncodeToString(src []byte) string { return e.enc.EncodeToString(src) }

// Decode decodes the Base64 text src into dst and returns the number of bytes written. | Decode 把 Base64 文本 src 解码写入 dst，返回写入的字节数。
// It requires len(dst) >= DecodedLen(len(src)), otherwise it returns ErrShortBuffer without writing any data. | 要求 len(dst) >= DecodedLen(len(src))，否则返回 ErrShortBuffer 且不写入任何数据。
// On decode failure it returns (0, err). As with encoding/base64, dst may then hold partial output
// from the failed attempt; use AppendDecode or DecodeString when that must not happen. | 解码失败时
// 返回 (0, err)。与 encoding/base64 一致，失败时 dst 可能留有本次尝试的部分输出；不能接受时请改用
// AppendDecode 或 DecodeString。
// As with encoding/base64, newline characters '\r' and '\n' in the input are ignored. | 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
func (e *Encoding) Decode(dst, src []byte) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	need := e.DecodedLen(len(src))
	if len(dst) < need {
		return 0, ErrShortBuffer
	}
	n, err := e.enc.Decode(dst[:need], src)
	if err != nil {
		return 0, errx.Wrap(err, ErrInvalidData)
	}
	return n, nil
}

// DecodeString returns the decoded bytes of the Base64 text s, or (nil, err) on decode failure. | DecodeString 返回 Base64 文本 s 解码后的字节，解码失败时返回 (nil, err)。
// As with encoding/base64, newline characters '\r' and '\n' in the input are ignored. | 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
func (e *Encoding) DecodeString(s string) ([]byte, error) {
	if len(s) == 0 {
		return []byte{}, nil
	}
	out, err := e.enc.DecodeString(s)
	if err != nil {
		return nil, errx.Wrap(err, ErrInvalidData)
	}
	return out, nil
}

// AppendDecode appends the decode result of the Base64 text src to dst and returns the extended slice. | AppendDecode 把 Base64 文本 src 的解码结果追加到 dst 并返回扩展后的切片。
// When the available capacity can hold the decode result it reuses the underlying array of dst, producing no new allocation. | 当可用容量足够容纳解码结果时复用 dst 的底层数组，不产生新的分配。
// On decode failure it returns (dst, err), and dst keeps its content unchanged. | 解码失败时返回 (dst, err)，dst 内容保持不变。
// As with encoding/base64, newline characters '\r' and '\n' in the input are ignored. | 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
func (e *Encoding) AppendDecode(dst, src []byte) ([]byte, error) {
	if len(src) == 0 {
		return dst, nil
	}
	base := len(dst)
	room := e.DecodedLen(len(src))
	// DecodeUnsafe appends to *dst; keep its length at base and reserve room plus
	// the SIMD write margin in the capacity. | DecodeUnsafe 会向 *dst 追加；保持长度
	// 为 base，并在容量中预留 room 与 SIMD 写余量。
	dst = slices.Grow(dst, room+decodeWriteSlack)
	if _, err := e.enc.DecodeUnsafe(&dst, src); err != nil {
		return dst[:base], errx.Wrap(err, ErrInvalidData)
	}
	return dst, nil
}
