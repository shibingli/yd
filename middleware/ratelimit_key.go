package middleware

import (
	"hash/fnv"
	"net/netip"
	"strconv"
	"strings"

	"github.com/shibingli/yd/http/router"
)

// missingKeyPart is the fixed marker for a dimension that has no value. | missingKeyPart 是维度无取值时使用的固定标记。
//
// An empty string would merge every unidentifiable request into one bucket, silently turning a
// per-dimension limit into a global one. A visible marker keeps that case separate and debuggable.
// | 空串会把所有无法识别的请求挤进同一个桶，悄悄把按维度的限流变成全局限流；可见标记会让这种情况
// 保持独立且可排查。
const missingKeyPart = "-"

// KeyFunc derives a rate-limit bucket key from a request. | KeyFunc 从请求派生限流桶键。
//
// A KeyFunc must be deterministic and must not return an empty string: use KeyFingerprint or one of
// the built-in extractors, which all normalize missing values to a fixed marker. | KeyFunc 必须确定性，
// 且不得返回空串：请使用 KeyFingerprint 或内置提取器，它们都会把缺失值归一为固定标记。
type KeyFunc func(*router.Ctx) string

// KeyRemoteIP limits by client address under an explicit trusted-proxy model. | KeyRemoteIP 在显式可信代理模型下按客户端地址限流。
//
// Pass the same prefixes as the reverse proxies that actually sit in front of the service; with an
// empty list every request is keyed by its peer address and X-Forwarded-For is ignored, which is the
// only configuration a client cannot forge. | 传入真正位于服务前面的反向代理前缀；列表为空时每个请求
// 按对端地址取键并忽略 X-Forwarded-For，这是客户端无法伪造的唯一配置。
func KeyRemoteIP(trusted []netip.Prefix) KeyFunc {
	return func(c *router.Ctx) string { return keyValue(c.ClientIP(trusted)) }
}

// KeyPath limits by the request path, without the query string. | KeyPath 按请求路径限流，不含查询串。
func KeyPath() KeyFunc {
	return func(c *router.Ctx) string { return keyValue(c.Request.URL.Path) }
}

// KeyQuery limits by one named query parameter. | KeyQuery 按指定的单个 query 参数限流。
//
// The value is trimmed but not lowercased: tokens and identifiers are case-sensitive. | 取值会去除首尾空白但
// 不转小写：令牌与标识符区分大小写。
func KeyQuery(name string) KeyFunc {
	return func(c *router.Ctx) string { return keyValue(c.Query(name)) }
}

// KeyHeader limits by one request header. | KeyHeader 按指定的请求头限流。
//
// The header name is matched case-insensitively by net/http. The value is trimmed but not
// lowercased. | 头部名由 net/http 按大小写不敏感匹配；取值去除首尾空白但不转小写。
func KeyHeader(name string) KeyFunc {
	return func(c *router.Ctx) string { return keyValue(c.Header(name)) }
}

// KeyBasicAuthUser limits by the HTTP Basic Auth username. | KeyBasicAuthUser 按 HTTP Basic Auth 用户名限流。
//
// Requests without a Basic Auth header share the fixed missing marker, so they do not collapse into
// one empty-string bucket. | 没有 Basic Auth 头的请求共享固定缺失标记，因此不会坍缩成一个空串桶。
func KeyBasicAuthUser() KeyFunc {
	return func(c *router.Ctx) string {
		user, _, ok := c.Request.BasicAuth()
		if !ok {
			return missingKeyPart
		}
		return keyValue(user)
	}
}

// KeyFingerprint combines dimensions into a hashed, opaque bucket key. | KeyFingerprint 把多个维度组合成经过哈希的不透明桶键。
//
// Parts are evaluated in the given order and joined with a unit separator, with the fixed missing
// marker for absent values, so the same request shape always produces the same key. The result is
// hashed (FNV-1a) rather than concatenated: raw user agents, IPs and header values must not leak
// into logs or Redis keys. FNV is deliberate — a rate-limit key needs speed, not collision
// resistance, and it is not an identity or a security boundary. | 各部分按给定顺序求值并用单元分隔符
// 拼接，缺失值使用固定标记，因此同一种请求形态始终产生同一个键。结果经过 FNV-1a 哈希而不是直接
// 拼接：原始 UA、IP 与头部值不得进入日志或 Redis key。选择 FNV 是有意的——限流键需要速度而非抗碰撞，
// 它既不是身份，也不是安全边界。
//
// A fingerprint is not an identity: user agents and headers are forgeable, so fingerprint-based
// limiting only raises the bar and never replaces authentication. | 指纹不是身份：UA 与头部可被伪造，
// 指纹限流只提高门槛，永远不能替代认证。
func KeyFingerprint(parts ...KeyFunc) KeyFunc {
	if len(parts) == 0 {
		return func(*router.Ctx) string { return missingKeyPart }
	}
	return func(c *router.Ctx) string {
		h := fnv.New64a()
		for i, part := range parts {
			if i > 0 {
				_, _ = h.Write([]byte{0x1f})
			}
			value := missingKeyPart
			if part != nil {
				value = keyValue(part(c))
			}
			_, _ = h.Write([]byte(value))
		}
		return "fp:" + strconv.FormatUint(h.Sum64(), 16)
	}
}

// keyValue trims a dimension value and maps an empty result to the fixed marker. | keyValue 去除维度值首尾空白，并把空结果映射为固定标记。
func keyValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return missingKeyPart
	}
	return v
}
