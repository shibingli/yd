//go:build b64x_native

package b64x

import (
	"github.com/shibingli/yd/core/errx"
)

// errCorrupt is the underlying error for malformed Base64 input. | errCorrupt 是非法 Base64 输入的底层错误。
var errCorrupt = errx.New("b64x: corrupt Base64 input | b64x：损坏的 Base64 输入")

// Encoding is the self-developed Base64 codec used when the b64x_native build tag is set. | Encoding 是
// 指定 b64x_native 构建标签时使用的自研 Base64 编解码器。
type Encoding struct {
	alphabet [64]byte
	decode   [256]uint8
	pad      byte
	raw      bool
	simd     uint8
}

// simdEncode and simdDecode are installed by the optional SIMD build. | simdEncode 与 simdDecode
// 由可选的 SIMD 构建安装；未启用时保持 nil 并走标量路径。
var (
	simdEncode func(dst, src []byte, url, padded bool, pad byte) (int, int)
	simdDecode func(dst, src []byte, url, raw bool) (int, int)
)

const (
	stdAlpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	urlAlpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
)

// newEncoding builds one encoding from its alphabet. | newEncoding 根据字母表构造一个编码器。
func newEncoding(alpha string, padded bool) *Encoding {
	e := &Encoding{raw: !padded}
	switch alpha {
	case stdAlpha:
		e.simd = 1
	case urlAlpha:
		e.simd = 2
	}
	copy(e.alphabet[:], alpha)
	for i := range e.decode {
		e.decode[i] = 0xFF
	}
	for i := 0; i < len(alpha); i++ {
		e.decode[alpha[i]] = uint8(i)
	}
	if padded {
		e.pad = '='
	}
	return e
}

var (
	// StdEncoding is the standard Base64 encoding defined by RFC 4648. | StdEncoding 是 RFC 4648 定义的标准 Base64 编码。
	StdEncoding = newEncoding(stdAlpha, true)
	// RawStdEncoding is the standard Base64 encoding without padding. | RawStdEncoding 是省略填充的标准 Base64 编码。
	RawStdEncoding = newEncoding(stdAlpha, false)
	// URLEncoding is the URL-safe Base64 encoding with padding. | URLEncoding 是带填充的 URL 安全 Base64 编码。
	URLEncoding = newEncoding(urlAlpha, true)
	// RawURLEncoding is the URL-safe Base64 encoding without padding. | RawURLEncoding 是省略填充的 URL 安全 Base64 编码。
	RawURLEncoding = newEncoding(urlAlpha, false)
)

// Backend returns the active backend name for this build. | Backend 返回当前构建使用的后端名称。
func Backend() string { return "yd-native" }

// EncodedLen returns the encoded length of an n-byte source. | EncodedLen 返回 n 字节源码编码后的长度。
func (e *Encoding) EncodedLen(n int) int {
	if e.raw {
		return (n*8 + 5) / 6
	}
	return (n + 2) / 3 * 4
}

// DecodedLen returns the maximum decoded length of n bytes of Base64 text. | DecodedLen 返回 n 字节 Base64 文本解码后的最大长度。
func (e *Encoding) DecodedLen(n int) int {
	if e.raw {
		return n * 6 / 8
	}
	return n / 4 * 3
}

// Encode writes the encoding of src into dst. It writes nothing when dst is too short. | Encode 把 src 编码写入 dst；dst 过短时不写入任何内容。
func (e *Encoding) Encode(dst, src []byte) {
	if len(dst) < e.EncodedLen(len(src)) {
		return
	}
	di := 0
	if simdEncode != nil && e.simd != 0 {
		n, out := simdEncode(dst, src, e.simd == 2, !e.raw, e.pad)
		di = out
		src = src[n:]
	}
	for len(src) >= 12 {
		for j := 0; j < 12; j += 3 {
			v := uint(src[j])<<16 | uint(src[j+1])<<8 | uint(src[j+2])
			o := di + j/3*4
			dst[o] = e.alphabet[v>>18&63]
			dst[o+1] = e.alphabet[v>>12&63]
			dst[o+2] = e.alphabet[v>>6&63]
			dst[o+3] = e.alphabet[v&63]
		}
		src, di = src[12:], di+16
	}
	for len(src) >= 3 {
		v := uint(src[0])<<16 | uint(src[1])<<8 | uint(src[2])
		dst[di] = e.alphabet[v>>18&63]
		dst[di+1] = e.alphabet[v>>12&63]
		dst[di+2] = e.alphabet[v>>6&63]
		dst[di+3] = e.alphabet[v&63]
		src, di = src[3:], di+4
	}
	switch len(src) {
	case 1:
		v := uint(src[0]) << 16
		dst[di] = e.alphabet[v>>18&63]
		dst[di+1] = e.alphabet[v>>12&63]
		if !e.raw {
			dst[di+2], dst[di+3] = e.pad, e.pad
		}
	case 2:
		v := uint(src[0])<<16 | uint(src[1])<<8
		dst[di] = e.alphabet[v>>18&63]
		dst[di+1] = e.alphabet[v>>12&63]
		dst[di+2] = e.alphabet[v>>6&63]
		if !e.raw {
			dst[di+3] = e.pad
		}
	}
}

// AppendEncode appends the encoding of src to dst. | AppendEncode 把 src 的编码结果追加到 dst。
func (e *Encoding) AppendEncode(dst, src []byte) []byte {
	n := e.EncodedLen(len(src))
	old := len(dst)
	if cap(dst)-old < n {
		dst = append(dst, make([]byte, n)...)
	} else {
		dst = dst[:old+n]
	}
	e.Encode(dst[old:], src)
	return dst
}

// EncodeToString returns the Base64-encoded string of src. | EncodeToString 返回 src 的 Base64 编码字符串。
func (e *Encoding) EncodeToString(src []byte) string {
	b := make([]byte, e.EncodedLen(len(src)))
	e.Encode(b, src)
	return string(b)
}

// Decode decodes Base64 text into dst and returns the number of bytes written. | Decode 把 Base64 文本解码到 dst 并返回写入字节数。
func (e *Encoding) Decode(dst, src []byte) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	if len(dst) < e.DecodedLen(len(src)) {
		return 0, ErrShortBuffer
	}
	out, i := 0, 0
	if simdDecode != nil && e.simd != 0 {
		i, out = simdDecode(dst, src, e.simd == 2, e.raw)
	}
	for i+4 <= len(src) {
		a := e.decode[src[i]]
		b := e.decode[src[i+1]]
		c := e.decode[src[i+2]]
		d := e.decode[src[i+3]]
		if (a|b|c|d)&0x80 != 0 {
			break
		}
		dst[out] = a<<2 | b>>4
		dst[out+1] = b<<4 | c>>2
		dst[out+2] = c<<6 | d
		i += 4
		out += 3
	}
	if i == len(src) {
		return out, nil
	}
	n, err := e.decodeSlow(dst[out:], src[i:])
	if err != nil {
		return 0, err
	}
	return out + n, nil
}

// decodeSlow handles padding, CR/LF and invalid input after the fast path. | decodeSlow 处理快路径之后的填充、CR/LF 与非法输入。
func (e *Encoding) decodeSlow(dst, src []byte) (int, error) {
	var quad [4]uint8
	q, out, padAt := 0, 0, -1
	padDone := false
	for _, c := range src {
		if c == '\r' || c == '\n' {
			continue
		}
		if padDone {
			return 0, errx.Wrap(errCorrupt, ErrInvalidData)
		}
		if c == e.pad {
			if e.raw {
				return 0, errx.Wrap(errCorrupt, ErrInvalidData)
			}
			if padAt >= 0 {
				if padAt != 2 || q != 3 {
					return 0, errx.Wrap(errCorrupt, ErrInvalidData)
				}
				quad[3] = 64
				q = 4
			} else {
				if q < 2 {
					return 0, errx.Wrap(errCorrupt, ErrInvalidData)
				}
				padAt = q
				quad[q] = 64
				q++
			}
		} else {
			if padAt >= 0 {
				return 0, errx.Wrap(errCorrupt, ErrInvalidData)
			}
			v := e.decode[c]
			if v == 0xFF {
				return 0, errx.Wrap(errCorrupt, ErrInvalidData)
			}
			quad[q] = v
			q++
		}
		if q == 4 {
			real := 4
			switch padAt {
			case 2:
				real = 2
			case 3:
				real = 3
			}
			n, ok := e.decodeQuad(dst[out:], quad, real)
			if !ok {
				return 0, errx.Wrap(errCorrupt, ErrInvalidData)
			}
			out += n
			q = 0
			if padAt >= 0 {
				padDone, padAt = true, -1
			}
		}
	}
	if q != 0 {
		if e.raw {
			if q == 1 {
				return 0, errx.Wrap(errCorrupt, ErrInvalidData)
			}
			n, ok := e.decodeQuad(dst[out:], quad, q)
			if !ok {
				return 0, errx.Wrap(errCorrupt, ErrInvalidData)
			}
			out += n
		} else {
			return 0, errx.Wrap(errCorrupt, ErrInvalidData)
		}
	}
	return out, nil
}

// decodeQuad writes the decoded bytes of one quartet into dst. | decodeQuad 把一个四元组的解码结果写入 dst。
func (e *Encoding) decodeQuad(dst []byte, quad [4]uint8, real int) (int, bool) {
	switch real {
	case 2:
		if len(dst) < 1 {
			return 0, false
		}
		dst[0] = quad[0]<<2 | quad[1]>>4
		return 1, true
	case 3:
		if len(dst) < 2 {
			return 0, false
		}
		dst[0] = quad[0]<<2 | quad[1]>>4
		dst[1] = quad[1]<<4 | quad[2]>>2
		return 2, true
	case 4:
		if len(dst) < 3 {
			return 0, false
		}
		dst[0] = quad[0]<<2 | quad[1]>>4
		dst[1] = quad[1]<<4 | quad[2]>>2
		dst[2] = quad[2]<<6 | quad[3]
		return 3, true
	default:
		return 0, false
	}
}

// DecodeString returns the decoded bytes of Base64 text s. | DecodeString 返回 Base64 文本 s 的解码字节。
func (e *Encoding) DecodeString(s string) ([]byte, error) {
	if len(s) == 0 {
		return []byte{}, nil
	}
	out := make([]byte, e.DecodedLen(len(s)))
	n, err := e.Decode(out, []byte(s))
	if err != nil {
		return nil, err
	}
	return out[:n], nil
}

// AppendDecode appends the decoded bytes of src to dst. | AppendDecode 把 src 的解码结果追加到 dst。
func (e *Encoding) AppendDecode(dst, src []byte) ([]byte, error) {
	if len(src) == 0 {
		return dst, nil
	}
	need := e.DecodedLen(len(src))
	old := len(dst)
	if cap(dst)-old < need {
		buf := make([]byte, old+need)
		copy(buf, dst)
		n, err := e.Decode(buf[old:], src)
		if err != nil {
			return dst, err
		}
		return buf[:old+n], nil
	}
	grown := dst[:old+need]
	n, err := e.Decode(grown[old:], src)
	if err != nil {
		return dst, err
	}
	return grown[:old+n], nil
}
