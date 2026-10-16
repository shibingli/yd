package jwt

import (
	"math"
	"time"
	"unicode/utf8"
)

// This file is the only JSON reader on the validation hot path. | 本文件是校验热路径上唯一的 JSON 读取器。
//
// Claim validation must never build a map[string]any nor call into reflection: every decision about
// exp / nbf / iat / iss / aud / sub is taken on the decoded payload bytes themselves, so the bytes that
// were signed are exactly the bytes that get validated. | 声明校验绝不构造 map[string]any，也不进入反射：
// exp / nbf / iat / iss / aud / sub 的每一项判定都直接落在解码后的载荷字节上，
// 因此「被签名的字节」与「被校验的字节」始终是同一份。
//
// The scanner is allocation-free and never panics on malformed input: iteration simply stops and the
// error flag reports why. | 扫描器零分配，且面对畸形输入绝不 panic：迭代直接停止，由错误标记说明原因。

// maxJSONDepth caps the nesting the scanner walks so that a hostile payload cannot burn CPU on nesting alone. | maxJSONDepth 限制扫描器遍历的嵌套深度，避免恶意载荷仅靠嵌套耗尽 CPU。
const maxJSONDepth = 64

// minNumericDate and maxNumericDate bound the NumericDate window (about ±30000 years). | minNumericDate 与 maxNumericDate 限定 NumericDate 的取值窗口（约 ±3 万年）。
const (
	minNumericDate = -1e12
	maxNumericDate = 1e12
)

// skipSpace returns the index of the first non-whitespace byte at or after i. | skipSpace 返回 i 起第一个非空白字节的下标。
func skipSpace(b []byte, i int) int {
	for i < len(b) {
		switch b[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return len(b)
}

// skipString skips the JSON string literal starting at b[i] and returns the index just past its closing quote. | skipString 跳过从 b[i] 开始的 JSON 字符串字面量，返回紧贴现引号之后的下标。
func skipString(b []byte, i int) (int, bool) {
	if i >= len(b) || b[i] != '"' {
		return i, false
	}
	for i++; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++
		case '"':
			return i + 1, true
		}
	}
	return len(b), false
}

// skipContainer skips the JSON object or array starting at b[i]. | skipContainer 跳过从 b[i] 开始的 JSON 对象或数组。
func skipContainer(b []byte, i int) (int, bool) {
	depth := 0
	for ; i < len(b); i++ {
		switch b[i] {
		case '"':
			end, ok := skipString(b, i)
			if !ok {
				return len(b), false
			}
			i = end - 1
		case '{', '[':
			depth++
			if depth > maxJSONDepth {
				return len(b), false
			}
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1, true
			}
			if depth < 0 {
				return len(b), false
			}
		}
	}
	return len(b), false
}

// validPrimitive reports whether raw is a JSON literal or number. | validPrimitive 报告 raw 是否为 JSON 字面量或数字。
func validPrimitive(raw []byte) bool {
	if rawEqualString(raw, "true") || rawEqualString(raw, "false") || rawEqualString(raw, "null") {
		return true
	}
	_, ok := scanFloat64(raw)
	return ok
}

// skipPrimitive skips the JSON literal or number starting at b[i]. | skipPrimitive 跳过从 b[i] 开始的 JSON 字面量或数字。
func skipPrimitive(b []byte, i int) (int, bool) {
	start := i
	for i < len(b) {
		switch b[i] {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			return i, validPrimitive(b[start:i])
		}
		i++
	}
	return i, validPrimitive(b[start:i])
}

// skipValue skips any JSON value starting at b[i] and returns the index just past it. | skipValue 跳过从 b[i] 开始的任意 JSON 值，返回紧随其后的下标。
func skipValue(b []byte, i int) (int, bool) {
	if i >= len(b) {
		return i, false
	}
	switch b[i] {
	case '"':
		return skipString(b, i)
	case '{', '[':
		return skipContainer(b, i)
	}
	return skipPrimitive(b, i)
}

// scanner walks the top-level members of a JSON object without reflection or allocation. | scanner 无反射、零分配地遍历 JSON 对象的顶层成员。
//
// key and val hold the raw member bytes (the key without its quotes and still escaped, the value as written). | key 与 val 保存成员的原始字节（键去掉引号但保留转义，值原样保留）。
type scanner struct {
	b         []byte
	i         int
	needComma bool
	err       bool
	key       []byte
	val       []byte
}

// init points the scanner at the first member of b and reports whether b starts a JSON object. | init 把扫描器指向 b 的第一个成员，并报告 b 是否以 JSON 对象开头。
func (s *scanner) init(b []byte) bool {
	s.b, s.i, s.err, s.needComma, s.key, s.val = b, 0, false, false, nil, nil
	i := skipSpace(b, 0)
	if i >= len(b) || b[i] != '{' {
		s.err = true
		return false
	}
	s.i = i + 1
	return true
}

// next advances to the next member and reports whether one was read. | next 前进到下一个成员，并报告是否读到成员。
func (s *scanner) next() bool {
	if s.err {
		return false
	}
	i := skipSpace(s.b, s.i)
	if s.needComma {
		if i < len(s.b) && s.b[i] == ',' {
			i = skipSpace(s.b, i+1)
		} else if i >= len(s.b) || s.b[i] != '}' {
			s.err = true
			return false
		}
	}
	if i >= len(s.b) {
		s.err = true
		return false
	}
	if s.b[i] == '}' {
		if skipSpace(s.b, i+1) != len(s.b) {
			s.err = true
		}
		s.i = len(s.b)
		return false
	}
	if s.b[i] != '"' {
		s.err = true
		return false
	}
	keyEnd, ok := skipString(s.b, i)
	if !ok {
		s.err = true
		return false
	}
	s.key = s.b[i+1 : keyEnd-1]
	i = skipSpace(s.b, keyEnd)
	if i >= len(s.b) || s.b[i] != ':' {
		s.err = true
		return false
	}
	i = skipSpace(s.b, i+1)
	valEnd, ok := skipValue(s.b, i)
	if !ok {
		s.err = true
		return false
	}
	s.val = s.b[i:valEnd]
	s.i = valEnd
	s.needComma = true
	return true
}

// scanField returns the raw value bytes of the last occurrence of key in the top-level JSON object. | scanField 返回顶层 JSON 对象中 key 最后一次出现处的原始值字节。
//
// Last-wins keeps the scanner consistent with jsonx.Unmarshal, which resolves duplicate keys the same way,
// and satisfies RFC 7519 §4 (parsers must use the lexically last duplicate member or reject the token). | 取最后一次
// 出现与 jsonx.Unmarshal 解到 map 的语义一致，也满足 RFC 7519 §4（解析器必须取最后一次出现的重复成员，或直接拒绝）。
//
// The second result is false when obj is not a well-formed JSON object or when the key is absent. | obj 不是结构良好的 JSON 对象、或键不存在时，第二个结果为 false。
func scanField(obj []byte, key string) ([]byte, bool) {
	var s scanner
	if !s.init(obj) {
		return nil, false
	}
	var (
		val   []byte
		found bool
	)
	for s.next() {
		if keyMatches(s.key, key) {
			val, found = s.val, true
		}
	}
	if s.err || !found {
		return nil, false
	}
	return val, true
}

// validJSONObject reports whether b is a well-formed JSON object. | validJSONObject 报告 b 是否为结构良好的 JSON 对象。
func validJSONObject(b []byte) bool {
	var s scanner
	if !s.init(b) {
		return false
	}
	for s.next() {
	}
	return !s.err
}

// scanKeys returns the de-duplicated top-level keys in document order; it is the diagnostic slow path of Claims.Keys. | scanKeys 按文档顺序返回去重后的顶层键；它是 Claims.Keys 的诊断慢路径。
func scanKeys(obj []byte) []string {
	var s scanner
	if !s.init(obj) {
		return nil
	}
	var (
		keys []string
		seen map[string]struct{}
	)
	for s.next() {
		k := jsonKeyString(s.key)
		if seen == nil {
			seen = make(map[string]struct{}, 8)
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	if s.err {
		return nil
	}
	return keys
}

// rawEqualString reports whether raw equals s without converting raw to a string. | rawEqualString 报告 raw 是否等于 s，且不做「字节转字符串」的转换。
func rawEqualString(raw []byte, s string) bool {
	if len(raw) != len(s) {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != s[i] {
			return false
		}
	}
	return true
}

// hasEscape reports whether raw contains a backslash. | hasEscape 报告 raw 中是否存在反斜杠。
func hasEscape(raw []byte) bool {
	for _, c := range raw {
		if c == '\\' {
			return true
		}
	}
	return false
}

// keyMatches reports whether the raw JSON key equals key, decoding JSON escapes when they are present. | keyMatches 报告原始 JSON 键是否等于 key，存在转义时按 JSON 规则解码后比较。
//
// The common path (no escapes) compares bytes directly and allocates nothing. | 常规路径（无转义）直接逐字节比较，不产生分配。
func keyMatches(raw []byte, key string) bool {
	if !hasEscape(raw) {
		return rawEqualString(raw, key)
	}
	return rawEqualString(unescapeKey(make([]byte, 0, len(raw)), raw), key)
}

// jsonStringBody returns the body of the raw JSON string value with its quotes removed. | jsonStringBody 返回原始 JSON 字符串值去掉首尾引号后的主体。
func jsonStringBody(raw []byte) ([]byte, bool) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return nil, false
	}
	return raw[1 : len(raw)-1], true
}

// appendJSONString appends the decoded content of the raw JSON string value to dst. | appendJSONString 把原始 JSON 字符串值的解码内容追加到 dst。
func appendJSONString(dst, raw []byte) ([]byte, bool) {
	body, ok := jsonStringBody(raw)
	if !ok {
		return dst, false
	}
	if !hasEscape(body) {
		return append(dst, body...), true
	}
	return unescapeKey(dst, body), true
}

// decodeJSONString returns the decoded content of the raw JSON string value, or an empty string when raw is not a string. | decodeJSONString 返回原始 JSON 字符串值的解码内容；raw 不是字符串时返回空串。
func decodeJSONString(raw []byte) string {
	s, _ := jsonStringValue(raw)
	return s
}

// jsonStringValue returns the decoded content of a raw JSON string value and whether raw was a string at all. | jsonStringValue 返回原始 JSON 字符串值的解码内容，以及 raw 是否为字符串。
func jsonStringValue(raw []byte) (string, bool) {
	body, ok := jsonStringBody(raw)
	if !ok {
		return "", false
	}
	return jsonStringBodyValue(body), true
}

// jsonStringValueOrEmpty is jsonStringValue for callers that treat a non-string as an empty string. | jsonStringValueOrEmpty 是 jsonStringValue 的变体，供把非字符串视作空串的调用方使用。
func jsonStringValueOrEmpty(raw []byte) string {
	s, _ := jsonStringValue(raw)
	return s
}

// jsonStringBodyValue decodes the body of a JSON string, i.e. the bytes between its quotes. | jsonStringBodyValue 解码 JSON 字符串的主体，即其引号之间的字节。
//
// "JWT" is the overwhelmingly common typ value and is returned as the interned constant, which keeps the
// usual header free of an allocation for it. | "JWT" 是最常见的 typ 取值，这里直接返回驻留常量，
// 使常规头部不必为它产生分配。
func jsonStringBodyValue(body []byte) string {
	if rawEqualString(body, "JWT") {
		return "JWT"
	}
	if !hasEscape(body) {
		return string(body)
	}
	return string(unescapeKey(make([]byte, 0, len(body)), body))
}

// jsonKeyString returns the decoded name of a raw JSON key, that is of the bytes between its quotes. | jsonKeyString 返回原始 JSON 键的解码名，即其引号之间的字节。
func jsonKeyString(key []byte) string {
	if !hasEscape(key) {
		return string(key)
	}
	return string(unescapeKey(make([]byte, 0, len(key)), key))
}

// jsonStringEquals reports whether the raw JSON string value equals want without allocating on the common path. | jsonStringEquals 报告原始 JSON 字符串值是否等于 want，常规路径零分配。
func jsonStringEquals(raw []byte, want string) bool {
	body, ok := jsonStringBody(raw)
	if !ok {
		return false
	}
	if !hasEscape(body) {
		return rawEqualString(body, want)
	}
	return rawEqualString(unescapeKey(make([]byte, 0, len(body)), body), want)
}

// unescapeKey appends the decoded form of a raw JSON string body to dst. | unescapeKey 把原始 JSON 字符串体的解码结果追加到 dst。
//
// Surrogate pairs are decoded code unit by code unit, which yields U+FFFD instead of a paired rune; that is
// acceptable because only claim names and string claim values pass through here and a mismatch simply fails. | 代理对按码元
// 逐个解码，结果为 U+FFFD 而不是合成码点；这里只处理声明名与字符串声明值，不匹配即判定失败，因此可以接受。
func unescapeKey(dst, raw []byte) []byte {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' {
			dst = append(dst, c)
			continue
		}
		i++
		if i >= len(raw) {
			break
		}
		switch raw[i] {
		case '"', '\\', '/':
			dst = append(dst, raw[i])
		case 'b':
			dst = append(dst, '\b')
		case 'f':
			dst = append(dst, '\f')
		case 'n':
			dst = append(dst, '\n')
		case 'r':
			dst = append(dst, '\r')
		case 't':
			dst = append(dst, '\t')
		case 'u':
			r, n := decodeHexRune(raw[i+1:])
			if n == 0 {
				break
			}
			dst = utf8.AppendRune(dst, r)
			i += n
		}
	}
	return dst
}

// decodeHexRune decodes the four hex digits of a \u escape and returns the rune together with the digits consumed. | decodeHexRune 解码 \u 转义后的四位十六进制，返回码点与消耗的位数。
func decodeHexRune(b []byte) (rune, int) {
	if len(b) < 4 {
		return 0, 0
	}
	var r rune
	for i := 0; i < 4; i++ {
		v := hexVal(b[i])
		if v < 0 {
			return 0, 0
		}
		r = r<<4 | rune(v)
	}
	return r, 4
}

// hexVal returns the value of a hex digit, or -1 when c is not one. | hexVal 返回十六进制数字的值，c 不是十六进制数字时返回 -1。
func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// scanInt64 parses a JSON integer, rejecting fractions, exponents, leading zeros and out-of-range values. | scanInt64 解析 JSON 整数，拒绝小数、指数、前导零与越界值。
func scanInt64(b []byte) (int64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	i := 0
	neg := false
	if b[i] == '-' {
		neg = true
		i++
	}
	if i >= len(b) {
		return 0, false
	}
	// JSON forbids a leading zero unless the integer is exactly "0". | JSON 禁止前导零，除非整数恰好是 "0"。
	if b[i] == '0' && i+1 < len(b) {
		return 0, false
	}
	// limit is the largest magnitude allowed: a negative number gains one extra bit. | limit 是允许累加的最大绝对值：负数可以多表示一位。
	limit := uint64(math.MaxInt64)
	if neg {
		limit++
	}
	var n uint64
	for ; i < len(b); i++ {
		d := b[i] - '0'
		if d > 9 {
			return 0, false
		}
		if n > (limit-uint64(d))/10 {
			return 0, false
		}
		n = n*10 + uint64(d)
	}
	if !neg {
		return int64(n), true
	}
	if n == limit {
		return math.MinInt64, true
	}
	return -int64(n), true
}

// scanFloat64 parses a JSON number in integer, fraction or exponent form. | scanFloat64 解析整数、小数或指数形式的 JSON 数字。
//
// The grammar is the JSON one: an optional minus sign, an integer part without a leading zero, an optional
// fraction and an optional exponent. NaN, Infinity, a plus sign on the integer part and a bare word are all
// rejected, so a malformed number can never slip through structural validation. | 语法与 JSON 一致：
// 可选负号、无前导零的整数部分、可选小数与可选指数。NaN、Infinity、整数部分的加号以及裸词一律拒绝，
// 因此非法数值绝无可能通过结构校验。
func scanFloat64(b []byte) (float64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	i := 0
	neg := false
	if b[i] == '-' {
		neg = true
		i++
	}
	if i < len(b) && b[i] == '0' && i+1 < len(b) && b[i+1] >= '0' && b[i+1] <= '9' {
		return 0, false
	}
	var (
		mant   float64
		digits int
	)
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		mant = mant*10 + float64(b[i]-'0')
		i++
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	if i < len(b) && b[i] == '.' {
		i++
		scale := 0.1
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			mant += float64(b[i]-'0') * scale
			scale /= 10
			i++
			digits++
		}
	}
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		i++
		expNeg := false
		if i < len(b) && (b[i] == '+' || b[i] == '-') {
			expNeg = b[i] == '-'
			i++
		}
		exp, eDigits := 0, 0
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			if exp < 400 {
				exp = exp*10 + int(b[i]-'0')
			}
			i++
			eDigits++
		}
		if eDigits == 0 {
			return 0, false
		}
		if expNeg {
			exp = -exp
		}
		mant *= math.Pow(10, float64(exp))
	}
	if i != len(b) {
		return 0, false
	}
	if neg {
		mant = -mant
	}
	return mant, true
}

// numericDate converts a raw JSON number into a UTC time value. | numericDate 把原始 JSON 数字转换为 UTC 时间值。
//
// It mirrors RFC 7519 NumericDate: seconds since the Unix epoch, optionally fractional. | 与 RFC 7519 的 NumericDate 一致：以 Unix 纪元为起点的秒数，允许小数。
func numericDate(raw []byte) (time.Time, bool) {
	if n, ok := scanInt64(raw); ok {
		return time.Unix(n, 0).UTC(), true
	}
	f, ok := scanFloat64(raw)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f < minNumericDate || f > maxNumericDate {
		return time.Time{}, false
	}
	sec := math.Floor(f)
	nsec := int64(math.Round((f - sec) * 1e9))
	if nsec >= int64(time.Second) {
		sec++
		nsec -= int64(time.Second)
	}
	return time.Unix(int64(sec), nsec).UTC(), true
}

// rawAudienceContains reports whether the raw aud value, a string or an array of strings, contains want. | rawAudienceContains 报告原始 aud 值（字符串或字符串数组）是否包含 want。
func rawAudienceContains(raw []byte, want string) bool {
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '"' {
		return jsonStringEquals(raw, want)
	}
	if raw[0] != '[' {
		return false
	}
	i := skipSpace(raw, 1)
	for i < len(raw) && raw[i] != ']' {
		switch raw[i] {
		case ',':
			i = skipSpace(raw, i+1)
		case '"':
			end, ok := skipString(raw, i)
			if !ok {
				return false
			}
			if jsonStringEquals(raw[i:end], want) {
				return true
			}
			i = skipSpace(raw, end)
		default:
			end, ok := skipValue(raw, i)
			if !ok {
				return false
			}
			i = skipSpace(raw, end)
		}
	}
	return false
}
