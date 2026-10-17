//go:build amd64 && !b64x_std

package b64x

import (
	"bytes"
	"encoding/base64"
	"slices"

	cwb64 "github.com/cloudwego/base64x"
	crb64 "github.com/cristalhq/base64"

	"github.com/shibingli/yd/core/errx"
)

// Size thresholds for the amd64 hybrid backend. Small inputs favour cristalhq
// (less SIMD setup), larger inputs favour cloudwego (AVX2/SSE throughput). The
// crossover was measured per direction, so encode and decode tune separately. | amd64
// 混合后端的数据尺寸阈值。小输入用 cristalhq（SIMD 启动开销更低），大输入用 cloudwego
// （AVX2/SSE 吞吐更高）。交叉点按方向分别实测，因此编码与解码使用独立阈值。
const (
	// encodeSwitchBytes is the source size at which encoding switches to cloudwego. | encodeSwitchBytes 是编码切换到 cloudwego 的源数据尺寸。
	encodeSwitchBytes = 32
	// decodeSwitchBytes is the decoded size at which decoding switches to cloudwego. | decodeSwitchBytes 是解码切换到 cloudwego 的解码后尺寸。
	decodeSwitchBytes = 32
)

// Encoding is a Base64 codec that routes by size on amd64: cristalhq for small
// inputs and cloudwego/base64x (AVX2/SSE) for the rest. | Encoding 是 amd64 上按尺寸
// 分流的 Base64 编解码器：小输入走 cristalhq，其余走 cloudwego/base64x（AVX2/SSE）。
type Encoding struct {
	cw  cwb64.Encoding   // large-input SIMD codec | 大输入 SIMD 编解码器
	cr  *crb64.Encoding  // small-input codec | 小输入编解码器
	dec *base64.Encoding // newline fallback for the small path | 小输入路径的换行回退
}

var (
	// StdEncoding is the standard Base64 encoding defined by RFC 4648 (alphabet A-Za-z0-9+/, '=' padding). | StdEncoding 是 RFC 4648 定义的标准 Base64 编码（字母表 A-Za-z0-9+/，'=' 填充）。
	StdEncoding = &Encoding{cw: cwb64.StdEncoding, cr: crb64.StdEncoding, dec: base64.StdEncoding}
	// RawStdEncoding is the standard Base64 encoding with '=' padding omitted. | RawStdEncoding 是省略 '=' 填充的标准 Base64 编码。
	RawStdEncoding = &Encoding{cw: cwb64.RawStdEncoding, cr: crb64.RawStdEncoding, dec: base64.RawStdEncoding}
	// URLEncoding is the URL- and filename-safe Base64 encoding (the alphabet replaces '+' and '/' with '-' and '_', with '=' padding). | URLEncoding 是 URL 与文件名安全的 Base64 编码（字母表把 '+'、'/' 换成 '-'、'_'，'=' 填充）。
	URLEncoding = &Encoding{cw: cwb64.URLEncoding, cr: crb64.URLEncoding, dec: base64.URLEncoding}
	// RawURLEncoding is the URL-safe Base64 encoding with '=' padding omitted. | RawURLEncoding 是省略 '=' 填充的 URL 安全 Base64 编码。
	RawURLEncoding = &Encoding{cw: cwb64.RawURLEncoding, cr: crb64.RawURLEncoding, dec: base64.RawURLEncoding}
)

// Backend returns the name of the backend used by the current build. | Backend 返回当前构建使用的后端名称。
func Backend() string { return "cloudwego/base64x+cristalhq" }

// EncodedLen returns the encoded length of an n-byte source, exactly matching encoding/base64.Encoding.EncodedLen. | EncodedLen 返回 n 字节源码编码后的长度，与 encoding/base64.Encoding.EncodedLen 完全一致。
func (e *Encoding) EncodedLen(n int) int { return e.cw.EncodedLen(n) }

// DecodedLen returns the maximum number of bytes after decoding Base64 text of length n, exactly matching | DecodedLen 返回长度 n 的 Base64 文本解码后的最大字节数，与
// encoding/base64.Encoding.DecodedLen. | encoding/base64.Encoding.DecodedLen 完全一致。
func (e *Encoding) DecodedLen(n int) int { return e.cw.DecodedLen(n) }

// Encode writes the encoding of src into dst; it requires len(dst) >= EncodedLen(len(src)), otherwise it panics (consistent with the standard library). | Encode 把 src 编码写入 dst，要求 len(dst) >= EncodedLen(len(src))，否则 panic（与标准库一致）。
func (e *Encoding) Encode(dst, src []byte) {
	if len(src) == 0 {
		return
	}
	if len(src) < encodeSwitchBytes {
		e.cr.Encode(dst[:e.EncodedLen(len(src))], src)
		return
	}
	e.cw.Encode(dst, src)
}

// AppendEncode appends the encoding of src to dst and returns the extended slice. | AppendEncode 把 src 的编码结果追加到 dst 并返回扩展后的切片。
// When cap(dst)-len(dst) >= EncodedLen(len(src)) it reuses the underlying array of dst, producing no new allocation. | 当 cap(dst)-len(dst) >= EncodedLen(len(src)) 时复用 dst 的底层数组，不产生新的分配。
func (e *Encoding) AppendEncode(dst, src []byte) []byte {
	if len(src) == 0 {
		return dst
	}
	n := e.EncodedLen(len(src))
	if len(src) < encodeSwitchBytes {
		base := len(dst)
		dst = slices.Grow(dst, n)[:base+n]
		e.cr.Encode(dst[base:], src)
		return dst
	}
	dst = slices.Grow(dst, n)
	e.cw.EncodeUnsafe(&dst, src)
	return dst
}

// EncodeToString returns the Base64-encoded string of src. | EncodeToString 返回 src 的 Base64 编码字符串。
func (e *Encoding) EncodeToString(src []byte) string {
	if len(src) < encodeSwitchBytes {
		return e.cr.EncodeToString(src)
	}
	return e.cw.EncodeToString(src)
}

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
	if need < decodeSwitchBytes {
		n, err := e.cr.Decode(dst, src)
		if err != nil && bytes.ContainsAny(src, "\r\n") {
			// cristalhq rejects newlines; encoding/base64 ignores them. | cristalhq
			// 拒绝换行；标准库忽略换行。
			n, err = e.dec.Decode(dst, src)
		}
		if err != nil {
			return 0, errx.Wrap(err, ErrInvalidData)
		}
		return n, nil
	}
	n, err := e.cw.Decode(dst[:need], src)
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
	if e.DecodedLen(len(s)) < decodeSwitchBytes {
		out, err := e.cr.DecodeString(s)
		if err != nil && bytes.ContainsAny([]byte(s), "\r\n") {
			out, err = e.dec.DecodeString(s)
		}
		if err != nil {
			return nil, errx.Wrap(err, ErrInvalidData)
		}
		return out, nil
	}
	out, err := e.cw.DecodeString(s)
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
	if room < decodeSwitchBytes {
		dst = slices.Grow(dst, room+decodeWriteSlack)[:base+room]
		n, err := e.cr.Decode(dst[base:], src)
		if err != nil && bytes.ContainsAny(src, "\r\n") {
			n, err = e.dec.Decode(dst[base:], src)
		}
		if err != nil {
			return dst[:base], errx.Wrap(err, ErrInvalidData)
		}
		return dst[:base+n], nil
	}
	// DecodeUnsafe appends to *dst; keep its length at base and reserve room plus
	// the SIMD write margin in the capacity. | DecodeUnsafe 会向 *dst 追加；保持长度
	// 为 base，并在容量中预留 room 与 SIMD 写余量。
	dst = slices.Grow(dst, room+decodeWriteSlack)
	if _, err := e.cw.DecodeUnsafe(&dst, src); err != nil {
		return dst[:base], errx.Wrap(err, ErrInvalidData)
	}
	return dst, nil
}
