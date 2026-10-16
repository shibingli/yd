package middleware

import (
	"crypto/rand"
	"sync/atomic"
	"time"

	"github.com/yun-dev/yd/router"
)

// defaultRequestIDHeader is used when RequestID is called with an empty header name.
// | defaultRequestIDHeader 是 RequestID 收到空请求头名时使用的默认名。
const defaultRequestIDHeader = "X-Request-Id"

// RequestIDKey is the key under which RequestID stores the id in the request context.
// | RequestIDKey 是 RequestID 在请求上下文中保存 ID 使用的键。
const RequestIDKey = "request_id"

// hexDigits is the lowercase hexadecimal alphabet. | hexDigits 是小写十六进制字符表。
const hexDigits = "0123456789abcdef"

// RequestID returns a middleware that assigns every request a unique id.
//
// | RequestID 返回为每个请求分配唯一 ID 的中间件。
//
// The id is taken from the header named by header (default X-Request-Id) when present,
// otherwise a fresh UUIDv4 is generated with crypto/rand. The id is stored in the context
// under RequestIDKey and echoed back in the same response header.
//
// | 若请求中带有 header 指定的头（默认 X-Request-Id）则沿用其值，否则用 crypto/rand 生成新的 UUIDv4；
// | ID 以 RequestIDKey 存入上下文，并通过同名响应头回显。
func RequestID(header string) router.Middleware {
	name := header
	if name == "" {
		name = defaultRequestIDHeader
	}
	return func(c *router.Ctx) {
		id := c.Request.Header.Get(name)
		if id == "" {
			id = newUUIDv4()
		}
		c.Set(RequestIDKey, id)
		c.SetHeader(name, id)
		c.Next()
	}
}

// requestID returns the id stored by RequestID, or "" when the middleware is absent.
// | requestID 返回 RequestID 存入的 ID；中间件未启用时返回空串。
func requestID(c *router.Ctx) string {
	if v, ok := c.Get(RequestIDKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// newUUIDv4 returns a random RFC 4122 version 4 UUID in its canonical textual form.
// | newUUIDv4 返回 RFC 4122 版本 4 的随机 UUID 标准文本形式。
func newUUIDv4() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		fallbackRandom(raw[:])
	}
	raw[6] = (raw[6] & 0x0f) | 0x40 // version 4 | 版本位固定为 4
	raw[8] = (raw[8] & 0x3f) | 0x80 // variant RFC 4122 | 变体位固定为 RFC 4122

	var out [36]byte
	j := 0
	for i, b := range raw {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[j] = '-'
			j++
		}
		out[j] = hexDigits[b>>4]
		out[j+1] = hexDigits[b&0x0f]
		j += 2
	}
	return string(out[:])
}

// fallbackSeq feeds fallbackRandom when crypto/rand is unavailable. | fallbackSeq 在 crypto/rand 不可用时为 fallbackRandom 提供序列。
var fallbackSeq atomic.Uint64

// fallbackRandom fills b with xorshift64 output seeded by the clock and a sequence number.
// | fallbackRandom 用时钟与序列号做种子的 xorshift64 填充 b。
func fallbackRandom(b []byte) {
	seed := uint64(time.Now().UnixNano()) ^ (fallbackSeq.Add(1) * 0x9e3779b97f4a7c15)
	if seed == 0 {
		seed = 0x2545f4914f6cdd1d
	}
	for i := range b {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		b[i] = byte(seed)
	}
}
