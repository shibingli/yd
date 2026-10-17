package logx

import (
	"math"
	"strconv"
	"time"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/core/jsonx"
)

// hexDigits is the hexadecimal character table used by \u00XX escapes. | hexDigits 是 \u00XX 转义使用的十六进制字符表。
const hexDigits = "0123456789abcdef"

// appendJSONRecord appends one complete record (including the trailing newline) to dst as JSON. | appendJSONRecord 将一条完整记录（含结尾换行）按 JSON 追加到 dst。
//
// The record shape is fixed: | 记录形状固定为：
//
//	{"level":..,"ts":..[,"service":..][,"logger":..][,"caller":..],"pid":..[,"host":..],"msg":..,<字段...>}
//
// The pid and host identify the emitting process, which makes records from several processes | pid 与 host 标识产生该记录的进程，使多个进程写同一目的地时的记录
// sharing one destination attributable; both are captured once in New, so this stays allocation-free. | 可以归属到具体进程；两者都在 New 时取一次，因此这里仍保持零分配。
// Persistent fields (accumulated by With) are written before the fields of the current call. | 持久字段（With 累积）先于本次调用的字段输出。
func appendJSONRecord(dst []byte, l *Logger, lvl Level, t time.Time, msg, caller string, extra []Field) []byte {
	dst = append(dst, `{"level":`...)
	dst = appendJSONLevel(dst, lvl)
	dst = append(dst, `,"ts":"`...)
	// The time layout is controlled by the caller and inlined directly (the Options.TimeFormat documentation already restricts layout contents). | 时间布局由使用方控制，直接内联（Options.TimeFormat 文档已限制布局内容）。
	dst = appendTime(dst, t, l.timeFormat)
	dst = append(dst, '"')
	if l.serviceJSON != "" {
		dst = append(dst, l.serviceJSON...)
	}
	if l.nameJSON != "" {
		dst = append(dst, l.nameJSON...)
	}
	if caller != "" {
		dst = append(dst, `,"caller":`...)
		dst = appendJSONString(dst, caller)
	}
	dst = append(dst, `,"pid":`...)
	dst = append(dst, l.pidStr...)
	if l.hostJSON != "" {
		dst = append(dst, l.hostJSON...)
	}
	dst = append(dst, `,"msg":`...)
	dst = appendJSONString(dst, msg)
	dst = appendJSONFields(dst, l.fields, l.timeFormat)
	dst = appendJSONFields(dst, extra, l.timeFormat)
	return append(dst, '}', '\n')
}

// levelJSON holds the pre-escaped JSON form of every valid level, indexed by Level+1. | levelJSON 保存每个合法级别的预转义 JSON 形式，下标为 Level+1。
var levelJSON = [...]string{`"debug"`, `"info"`, `"warn"`, `"error"`, `"fatal"`}

// appendJSONLevel appends the level literal without re-escaping it. | appendJSONLevel 追加级别字面量，不再重复转义。
func appendJSONLevel(dst []byte, lvl Level) []byte {
	if i := int(lvl) + 1; i >= 0 && i < len(levelJSON) {
		return append(dst, levelJSON[i]...)
	}
	return appendJSONString(dst, lvl.String())
}

// jsonStringMember builds `,<key>:<escaped value>` once at construction, so persistent members
// (service, logger, host) are escaped once per Logger instead of once per record. | jsonStringMember
// 在构造期构建 `,<key>:<转义值>`，使持久成员（service、logger、host）每个 Logger 只转义一次，
// 而不是每条记录一次。
func jsonStringMember(key, value string) string {
	if value == "" {
		return ""
	}
	buf := make([]byte, 0, len(key)+len(value)+8)
	buf = append(buf, ',')
	buf = appendJSONString(buf, key)
	buf = append(buf, ':')
	buf = appendJSONString(buf, value)
	return string(buf)
}

// appendJSONFields encodes a group of fields in order. | appendJSONFields 依次编码一组字段。
func appendJSONFields(dst []byte, fields []Field, timeFormat string) []byte {
	for i := range fields {
		dst = appendJSONField(dst, &fields[i], timeFormat)
	}
	return dst
}

// appendJSONField encodes a single field as a JSON member (including the leading comma) and appends it to dst. | appendJSONField 将单个字段编码为 JSON 成员（含前导逗号）追加到 dst。
//
// UnknownType fields (including the result of Err(nil)) are skipped entirely; when Key is empty and the type is RawType the | UnknownType 字段（含 Err(nil) 的结果）整条跳过；Key 为空且类型为 RawType 时
// fragment is injected verbatim (Raw); other keyless fields are skipped as well. | 原样注入片段（Raw），其余无键字段同样跳过。
func appendJSONField(dst []byte, f *Field, timeFormat string) []byte {
	switch {
	case f.Type == UnknownType:
		return dst
	case f.Key != "":
		dst = append(dst, ',')
		dst = appendJSONString(dst, f.Key)
		dst = append(dst, ':')
	case f.Type == RawType:
		dst = append(dst, ',')
	default:
		return dst
	}
	return appendJSONValue(dst, f, timeFormat)
}

// appendJSONValue encodes only the value part of a field (without the key or leading comma). | appendJSONValue 仅编码字段的值部分（不含键与前导逗号）。
func appendJSONValue(dst []byte, f *Field, timeFormat string) []byte {
	switch f.Type {
	case StringType, ErrorType:
		return appendJSONString(dst, f.str)
	case StringsType:
		if ss, ok := f.obj.([]string); ok {
			return appendJSONStrings(dst, ss)
		}
		return append(dst, "null"...)
	case IntType, Int64Type:
		return strconv.AppendInt(dst, int64(f.num), 10)
	case Uint64Type:
		return strconv.AppendUint(dst, f.num, 10)
	case Float64Type:
		return appendJSONFloat(dst, math.Float64frombits(f.num))
	case BoolType:
		return strconv.AppendBool(dst, f.num != 0)
	case TimeType:
		dst = append(dst, '"')
		dst = appendTime(dst, time.Unix(0, int64(f.num)), timeFormat)
		return append(dst, '"')
	case DurationType:
		// Milliseconds as a JSON number, which makes machine aggregation easy. | 毫秒数（JSON 数字），便于机器聚合。
		return appendJSONMillis(dst, int64(f.num))
	case BytesType:
		dst = append(dst, '"')
		dst = b64x.StdEncoding.AppendEncode(dst, f.bs)
		return append(dst, '"')
	case AnyType:
		return appendJSONAny(dst, f.obj)
	case RawType:
		// The caller guarantees that the fragment is valid JSON. | 调用方保证片段是合法 JSON。
		return append(dst, f.bs...)
	}
	return append(dst, "null"...)
}

// appendJSONStrings appends the JSON array of a []string. | appendJSONStrings 追加 []string 的 JSON 数组。
func appendJSONStrings(dst []byte, ss []string) []byte {
	dst = append(dst, '[')
	for i, s := range ss {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendJSONString(dst, s)
	}
	return append(dst, ']')
}

// appendJSONFloat appends a float64; NaN and ±Inf are output as null (JSON has no matching literal). | appendJSONFloat 追加 float64；NaN 与 ±Inf 输出 null（JSON 无对应字面量）。
func appendJSONFloat(dst []byte, v float64) []byte {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return append(dst, "null"...)
	}
	return strconv.AppendFloat(dst, v, 'g', -1, 64)
}

// appendJSONMillis appends nanoseconds as a millisecond JSON number with exact decimal notation.
// | appendJSONMillis 把纳秒追加为毫秒 JSON 数字，使用精确的十进制写法。
//
// Durations are integral nanoseconds, so the float path (ftoa64) is unnecessary: integer
// milliseconds plus a trimmed six-digit fraction produce the identical value without the rounding
// and exponent forms of strconv.AppendFloat (1ns becomes 0.000001 instead of 1e-06). | 时长是整数纳秒，
// 无需走浮点路径（ftoa64）：整数毫秒加截断的六位小数得到相同的数值，且不会出现
// strconv.AppendFloat 的舍入与指数形式（1ns 输出 0.000001 而不是 1e-06）。
func appendJSONMillis(dst []byte, ns int64) []byte {
	var u uint64
	if ns < 0 {
		dst = append(dst, '-')
		u = uint64(-(ns + 1)) + 1 // handles math.MinInt64 without overflow | 无溢出地处理 math.MinInt64
	} else {
		u = uint64(ns)
	}
	ms := u / uint64(time.Millisecond)
	frac := u % uint64(time.Millisecond)
	dst = strconv.AppendUint(dst, ms, 10)
	if frac == 0 {
		return dst
	}
	var digits [6]byte
	for i := len(digits) - 1; i >= 0; i-- {
		digits[i] = byte('0' + frac%10)
		frac /= 10
	}
	end := len(digits)
	for end > 0 && digits[end-1] == '0' {
		end--
	}
	dst = append(dst, '.')
	return append(dst, digits[:end]...)
}

// appendJSONAny encodes the boxed value of the Any slow path: it uses encoding/json (reflection inside, plus extra allocations). | appendJSONAny 编码 Any 慢路径的装箱值：使用 encoding/json（内部反射 + 额外分配）。
// When encoding fails it outputs a JSON string describing the error, so the result is always valid JSON. | 编码失败时输出描述该错误的 JSON 字符串，保证结果始终是合法 JSON。
func appendJSONAny(dst []byte, v any) []byte {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return appendJSONString(dst, "!json: "+err.Error())
	}
	return append(dst, b...)
}

// appendJSONString escapes s according to JSON string rules and appends it to dst (including the quotes on both sides). | appendJSONString 把 s 按 JSON 字符串规则转义并追加到 dst（含两侧引号）。
//
// Escapes '"', '\\' and all C0 control characters (\b \f \n \r \t use the short escapes, the rest use \u00XX); | 转义 '"'、'\\' 与全部 C0 控制字符（\b \f \n \r \t 用短转义，其余用 \u00XX）；
// The input is assumed to be valid UTF-8 and multi-byte sequences pass through verbatim, so Chinese text is never converted to \uXXXX. | 输入假定为合法 UTF-8，多字节序列原样透传，因此中文不会被转成 \uXXXX。
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		dst = append(dst, s[start:i]...)
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0x0F])
		}
		start = i + 1
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}
