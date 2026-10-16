//go:build !amd64 || b64x_std

package b64x

import (
	"encoding/base64"
	"fmt"
)

// Encoding 是 Base64 编解码器，在非 amd64 平台或指定 b64x_std 标签时委托给标准库
// encoding/base64。方法集与 b64x 的 base64x 后端完全一致，可直接互换。
type Encoding struct {
	enc *base64.Encoding // 底层标准库编解码器
}

var (
	// StdEncoding 是 RFC 4648 定义的标准 Base64 编码（字母表 A-Za-z0-9+/，'=' 填充）。
	StdEncoding = &Encoding{enc: base64.StdEncoding}
	// RawStdEncoding 是省略 '=' 填充的标准 Base64 编码。
	RawStdEncoding = &Encoding{enc: base64.RawStdEncoding}
	// URLEncoding 是 URL 与文件名安全的 Base64 编码（字母表把 '+'、'/' 换成 '-'、'_'，'=' 填充）。
	URLEncoding = &Encoding{enc: base64.URLEncoding}
	// RawURLEncoding 是省略 '=' 填充的 URL 安全 Base64 编码。
	RawURLEncoding = &Encoding{enc: base64.RawURLEncoding}
)

// Backend 返回当前构建使用的后端名称。非 amd64 平台或指定 b64x_std 标签时为 "encoding/base64"。
func Backend() string { return "encoding/base64" }

// EncodedLen 返回 n 字节源码编码后的长度，与 encoding/base64.Encoding.EncodedLen 完全一致。
func (e *Encoding) EncodedLen(n int) int { return e.enc.EncodedLen(n) }

// DecodedLen 返回长度 n 的 Base64 文本解码后的最大字节数，与
// encoding/base64.Encoding.DecodedLen 完全一致。
func (e *Encoding) DecodedLen(n int) int { return e.enc.DecodedLen(n) }

// Encode 把 src 编码写入 dst，要求 len(dst) >= EncodedLen(len(src))，否则 panic（与标准库一致）。
func (e *Encoding) Encode(dst, src []byte) { e.enc.Encode(dst, src) }

// AppendEncode 把 src 的编码结果追加到 dst 并返回扩展后的切片。
// 当 cap(dst)-len(dst) >= EncodedLen(len(src)) 时复用 dst 的底层数组，不产生新的分配。
func (e *Encoding) AppendEncode(dst, src []byte) []byte { return e.enc.AppendEncode(dst, src) }

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
	if len(dst) < e.enc.DecodedLen(len(src)) {
		return 0, ErrShortBuffer
	}
	n, err := e.enc.Decode(dst, src)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	return n, nil
}

// DecodeString 返回 Base64 文本 s 解码后的字节，解码失败时返回 (nil, err)。
// 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
func (e *Encoding) DecodeString(s string) ([]byte, error) {
	if len(s) == 0 {
		return []byte{}, nil
	}
	out, err := e.enc.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	return out, nil
}

// AppendDecode 把 Base64 文本 src 的解码结果追加到 dst 并返回扩展后的切片。
// 当可用容量足够容纳解码结果时复用 dst 的底层数组，不产生新的分配。
// 解码失败时返回 (dst, err)，dst 内容保持不变。
// 与 encoding/base64 一致，输入中的换行符 '\r'、'\n' 会被忽略。
func (e *Encoding) AppendDecode(dst, src []byte) ([]byte, error) {
	if len(src) == 0 {
		return dst, nil
	}
	out, err := e.enc.AppendDecode(dst, src)
	if err != nil {
		return dst, fmt.Errorf("%w: %w", ErrInvalidData, err)
	}
	return out, nil
}
