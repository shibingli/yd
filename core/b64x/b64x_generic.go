//go:build !amd64 || b64x_std

package b64x

import (
	"encoding/base64"

	"github.com/shibingli/yd/core/errx"
)

// Encoding is a Base64 codec that delegates to the standard library encoding/base64 on non-amd64 platforms | Encoding 是 Base64 编解码器，在非 amd64 平台或指定 b64x_std 标签时委托给标准库
// or when the b64x_std tag is specified. Its method set is exactly the same as the base64x backend of b64x, and the two are directly interchangeable. | encoding/base64。方法集与 b64x 的 base64x 后端完全一致，可直接互换。
type Encoding struct {
	enc *base64.Encoding // underlying standard library codec | 底层标准库编解码器
}

var (
	// StdEncoding is the standard Base64 encoding defined by RFC 4648 (alphabet A-Za-z0-9+/, '=' padding). | StdEncoding 是 RFC 4648 定义的标准 Base64 编码（字母表 A-Za-z0-9+/，'=' 填充）。
	StdEncoding = &Encoding{enc: base64.StdEncoding}
	// RawStdEncoding is the standard Base64 encoding with '=' padding omitted. | RawStdEncoding 是省略 '=' 填充的标准 Base64 编码。
	RawStdEncoding = &Encoding{enc: base64.RawStdEncoding}
	// URLEncoding is the URL- and filename-safe Base64 encoding (the alphabet replaces '+' and '/' with '-' and '_', with '=' padding). | URLEncoding 是 URL 与文件名安全的 Base64 编码（字母表把 '+'、'/' 换成 '-'、'_'，'=' 填充）。
	URLEncoding = &Encoding{enc: base64.URLEncoding}
	// RawURLEncoding is the URL-safe Base64 encoding with '=' padding omitted. | RawURLEncoding 是省略 '=' 填充的 URL 安全 Base64 编码。
	RawURLEncoding = &Encoding{enc: base64.RawURLEncoding}
)

// Backend returns the name of the backend used by the current build. On non-amd64 platforms or with the b64x_std tag specified it is "encoding/base64". | Backend 返回当前构建使用的后端名称。非 amd64 平台或指定 b64x_std 标签时为 "encoding/base64"。
func Backend() string { return "encoding/base64" }

// EncodedLen returns the encoded length of an n-byte source, exactly matching encoding/base64.Encoding.EncodedLen. | EncodedLen 返回 n 字节源码编码后的长度，与 encoding/base64.Encoding.EncodedLen 完全一致。
func (e *Encoding) EncodedLen(n int) int { return e.enc.EncodedLen(n) }

// DecodedLen returns the maximum number of bytes after decoding Base64 text of length n, exactly matching | DecodedLen 返回长度 n 的 Base64 文本解码后的最大字节数，与
// encoding/base64.Encoding.DecodedLen. | encoding/base64.Encoding.DecodedLen 完全一致。
func (e *Encoding) DecodedLen(n int) int { return e.enc.DecodedLen(n) }

// Encode writes the encoding of src into dst; it requires len(dst) >= EncodedLen(len(src)), otherwise it panics (consistent with the standard library). | Encode 把 src 编码写入 dst，要求 len(dst) >= EncodedLen(len(src))，否则 panic（与标准库一致）。
func (e *Encoding) Encode(dst, src []byte) { e.enc.Encode(dst, src) }

// AppendEncode appends the encoding of src to dst and returns the extended slice. | AppendEncode 把 src 的编码结果追加到 dst 并返回扩展后的切片。
// When cap(dst)-len(dst) >= EncodedLen(len(src)) it reuses the underlying array of dst, producing no new allocation. | 当 cap(dst)-len(dst) >= EncodedLen(len(src)) 时复用 dst 的底层数组，不产生新的分配。
func (e *Encoding) AppendEncode(dst, src []byte) []byte { return e.enc.AppendEncode(dst, src) }

// EncodeToString returns the Base64-encoded string of src. | EncodeToString 返回 src 的 Base64 编码字符串。
func (e *Encoding) EncodeToString(src []byte) string { return e.enc.EncodeToString(src) }

// Decode decodes the Base64 text src into dst and returns the number of bytes written. | Decode 把 Base64 文本 src 解码写入 dst，返回写入的字节数。
// It requires len(dst) >= DecodedLen(len(src)), otherwise it returns ErrShortBuffer without writing any data. | 要求 len(dst) >= DecodedLen(len(src))，否则返回 ErrShortBuffer 且不写入任何数据。
// On decode failure it returns (0, err); as with encoding/base64, dst may then hold partial output. | 解码失败时返回 (0, err)；与 encoding/base64 一致，dst 可能留有部分输出。
// As with encoding/base64, newline characters '\r' and '\n' in the input are ignored. | 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
func (e *Encoding) Decode(dst, src []byte) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	if len(dst) < e.enc.DecodedLen(len(src)) {
		return 0, ErrShortBuffer
	}
	n, err := e.enc.Decode(dst, src)
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
	out, err := e.enc.AppendDecode(dst, src)
	if err != nil {
		return dst, errx.Wrap(err, ErrInvalidData)
	}
	return out, nil
}
