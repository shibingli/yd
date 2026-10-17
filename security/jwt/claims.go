package jwt

import (
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
)

// Claims is a read-only view over the decoded JWT payload. | Claims 是解码后 JWT 载荷的只读视图。
//
// It holds the raw payload bytes only; every accessor scans those bytes on the fly through the scanner in
// scan.go, so no map is built and no reflection is used on the hot path. | 它只持有原始载荷字节；每个访问器都通过
// scan.go 的扫描器就地读取字节，因此热路径上既不构造 map，也不使用反射。
//
// Accessors that return a string or a slice necessarily allocate that result; the numeric and boolean
// accessors and Has never allocate. | 返回字符串或切片的访问器必然为结果分配内存；数值、布尔访问器与 Has 则零分配。
//
// A key that appears more than once resolves to its last occurrence, matching jsonx.Unmarshal into a map. | 多次出现的键取最后一次出现的值，与 jsonx.Unmarshal 解到 map 的行为一致。
type Claims struct {
	raw []byte
}

// Raw returns the decoded payload bytes; callers must treat them as read-only. | Raw 返回解码后的载荷字节；调用方必须视其为只读。
func (c Claims) Raw() []byte { return c.raw }

// Get returns the string claim key. | Get 返回字符串声明 key。
//
// The second result is false when the claim is absent or is not a JSON string. | 声明缺失或不是 JSON 字符串时，第二个结果为 false。
func (c Claims) Get(key string) (string, bool) {
	raw, ok := scanField(c.raw, key)
	if !ok {
		return "", false
	}
	body, ok := jsonStringBody(raw)
	if !ok {
		return "", false
	}
	if !hasEscape(body) {
		return string(body), true
	}
	return string(unescapeKey(make([]byte, 0, len(body)), body)), true
}

// Has reports whether the claim key exists, whatever its JSON type. | Has 报告声明 key 是否存在，不区分其 JSON 类型。
func (c Claims) Has(key string) bool {
	_, ok := scanField(c.raw, key)
	return ok
}

// Keys returns the claim names in document order with duplicates removed. | Keys 按文档顺序返回去重后的声明名。
//
// This is a diagnostic slow path: it materializes strings and a deduplication set. | 这是诊断用慢路径：它会生成字符串并建立去重集合。
func (c Claims) Keys() []string { return scanKeys(c.raw) }

// Subject returns the sub claim, or an empty string when it is absent. | Subject 返回 sub 声明；不存在时返回空串。
func (c Claims) Subject() string { return c.stringClaim("sub") }

// Issuer returns the iss claim, or an empty string when it is absent. | Issuer 返回 iss 声明；不存在时返回空串。
func (c Claims) Issuer() string { return c.stringClaim("iss") }

// ID returns the jti claim, or an empty string when it is absent. | ID 返回 jti 声明；不存在时返回空串。
func (c Claims) ID() string { return c.stringClaim("jti") }

// Audience returns the aud claim as a slice; a plain string becomes a one-element slice and a missing claim becomes nil. | Audience 以切片返回 aud 声明；字符串形式转为单元素切片，声明缺失时返回 nil。
//
// Non-string array elements are ignored. | 数组中的非字符串元素会被忽略。
func (c Claims) Audience() []string {
	raw, ok := scanField(c.raw, "aud")
	if !ok || len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		if s, ok := jsonStringBody(raw); ok {
			return []string{string(s)}
		}
		return nil
	}
	if raw[0] != '[' {
		return nil
	}
	var out []string
	i := skipSpace(raw, 1)
	for i < len(raw) && raw[i] != ']' {
		switch raw[i] {
		case ',':
			i = skipSpace(raw, i+1)
		case '"':
			end, ok := skipString(raw, i)
			if !ok {
				return out
			}
			out = append(out, decodeJSONString(raw[i:end]))
			i = skipSpace(raw, end)
		default:
			end, ok := skipValue(raw, i)
			if !ok {
				return out
			}
			i = skipSpace(raw, end)
		}
	}
	return out
}

// ExpiresAt returns the exp claim as a UTC time; the boolean reports presence of a parsable NumericDate. | ExpiresAt 以 UTC 时间返回 exp 声明；布尔值报告是否存在可解析的 NumericDate。
func (c Claims) ExpiresAt() (time.Time, bool) { return c.numericDateClaim("exp") }

// NotBefore returns the nbf claim as a UTC time; the boolean reports presence of a parsable NumericDate. | NotBefore 以 UTC 时间返回 nbf 声明；布尔值报告是否存在可解析的 NumericDate。
func (c Claims) NotBefore() (time.Time, bool) { return c.numericDateClaim("nbf") }

// IssuedAt returns the iat claim as a UTC time; the boolean reports presence of a parsable NumericDate. | IssuedAt 以 UTC 时间返回 iat 声明；布尔值报告是否存在可解析的 NumericDate。
func (c Claims) IssuedAt() (time.Time, bool) { return c.numericDateClaim("iat") }

// GetInt64 returns the integer claim key. | GetInt64 返回整数声明 key。
//
// Fractions, exponents and out-of-range values report false; use GetFloat64 for those. | 小数、指数与越界值返回 false；这类取值请用 GetFloat64。
func (c Claims) GetInt64(key string) (int64, bool) {
	raw, ok := scanField(c.raw, key)
	if !ok {
		return 0, false
	}
	return scanInt64(raw)
}

// GetFloat64 returns the numeric claim key, accepting integer, fraction and exponent forms. | GetFloat64 返回数值声明 key，接受整数、小数与指数形式。
func (c Claims) GetFloat64(key string) (float64, bool) {
	raw, ok := scanField(c.raw, key)
	if !ok {
		return 0, false
	}
	return scanFloat64(raw)
}

// GetBool returns the boolean claim key. | GetBool 返回布尔声明 key。
func (c Claims) GetBool(key string) (bool, bool) {
	raw, ok := scanField(c.raw, key)
	if !ok {
		return false, false
	}
	switch {
	case rawEqualString(raw, "true"):
		return true, true
	case rawEqualString(raw, "false"):
		return false, true
	}
	return false, false
}

// GetJSON decodes the claim key into v with jsonx, which is the only path in this package that reflects. | GetJSON 用 jsonx 把声明 key 解码到 v；这是本包唯一会走反射的路径。
//
// It returns ErrClaimNotFound when the claim is absent. | 声明缺失时返回 ErrClaimNotFound。
func (c Claims) GetJSON(key string, v any) error {
	raw, ok := scanField(c.raw, key)
	if !ok {
		return errx.Detail(ErrClaimNotFound, "claim %[1]q | 声明 %[1]q", key)
	}
	if err := jsonx.Unmarshal(raw, v); err != nil {
		return errx.WrapDetail(err, ErrMalformed, "claim %[1]q | 声明 %[1]q", key)
	}
	return nil
}

// stringClaim returns the string value of key, or an empty string when it is absent or not a string. | stringClaim 返回 key 的字符串值；缺失或不是字符串时返回空串。
func (c Claims) stringClaim(key string) string {
	v, _ := c.Get(key)
	return v
}

// numericDateClaim returns the NumericDate value of key. | numericDateClaim 返回 key 的 NumericDate 取值。
func (c Claims) numericDateClaim(key string) (time.Time, bool) {
	raw, ok := scanField(c.raw, key)
	if !ok {
		return time.Time{}, false
	}
	return numericDate(raw)
}

// claimFields holds the raw bytes of the registered claims that Parse validates, located in one pass. | claimFields 保存在一趟扫描中定位到的、Parse 需要校验的登记声明原始值。
type claimFields struct {
	exp, nbf, iss, aud []byte
	hasExp, hasNbf     bool
	hasIss, hasAud     bool
}

// scanRegistered validates the payload structure and locates exp, nbf, iss and aud in a single pass. | scanRegistered 一趟完成载荷结构校验，并定位 exp、nbf、iss 与 aud。
//
// Duplicate claim names resolve to their last occurrence. | 重复的声明名取最后一次出现的值。
func scanRegistered(payload []byte, f *claimFields) bool {
	var s scanner
	if !s.init(payload) {
		return false
	}
	for s.next() {
		switch {
		case keyMatches(s.key, "exp"):
			f.exp, f.hasExp = s.val, true
		case keyMatches(s.key, "nbf"):
			f.nbf, f.hasNbf = s.val, true
		case keyMatches(s.key, "iss"):
			f.iss, f.hasIss = s.val, true
		case keyMatches(s.key, "aud"):
			f.aud, f.hasAud = s.val, true
		}
	}
	return !s.err
}

// checkRegistered applies the time, issuer and audience rules to the located raw claim values. | checkRegistered 在定位到的原始声明值上应用时间、签发方与受众规则。
//
// Every decision reads raw bytes; nothing is decoded into a map. | 所有判定都读取原始字节，不会解码成 map。
func checkRegistered(f *claimFields, o ParseOptions) error {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	at := now()

	if f.hasNbf {
		nbf, ok := numericDate(f.nbf)
		if !ok {
			return errx.Detail(ErrMalformed, "nbf is not a valid NumericDate | nbf 不是合法的 NumericDate")
		}
		if at.Add(o.Leeway).Before(nbf) {
			return errx.Detail(ErrTokenNotValidYet, "nbf=%[1]s | 生效时间=%[1]s", nbf.Format(time.RFC3339))
		}
	}
	if f.hasExp {
		exp, ok := numericDate(f.exp)
		if !ok {
			return errx.Detail(ErrMalformed, "exp is not a valid NumericDate | exp 不是合法的 NumericDate")
		}
		if exp.Add(o.Leeway).Before(at) {
			return errx.Detail(ErrTokenExpired, "exp=%[1]s | 过期时间=%[1]s", exp.Format(time.RFC3339))
		}
	} else if o.RequireExp {
		return ErrMissingExp
	}
	if o.Issuer != "" {
		if !f.hasIss || !jsonStringEquals(f.iss, o.Issuer) {
			return errx.Detail(ErrIssuerMismatch, "want %[1]q | 期望 %[1]q", o.Issuer)
		}
	}
	if o.Audience != "" {
		if !f.hasAud || !rawAudienceContains(f.aud, o.Audience) {
			return errx.Detail(ErrAudienceMismatch, "want %[1]q | 期望 %[1]q", o.Audience)
		}
	}
	return nil
}
