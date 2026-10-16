package logx

import (
	"math"
	"strconv"
	"time"

	"github.com/yun-dev/yd/b64x"
	"github.com/yun-dev/yd/jsonx"
)

// hexDigits 是 \u00XX 转义使用的十六进制字符表。
const hexDigits = "0123456789abcdef"

// appendJSONRecord 将一条完整记录（含结尾换行）按 JSON 追加到 dst。
//
// 记录形状固定为：
//
//	{"level":..,"ts":..[,"service":..][,"logger":..][,"caller":..],"msg":..,<字段...>}
//
// 持久字段（With 累积）先于本次调用的字段输出。
func appendJSONRecord(dst []byte, l *Logger, lvl Level, t time.Time, msg, caller string, extra []Field) []byte {
	dst = append(dst, `{"level":`...)
	dst = appendJSONString(dst, lvl.String())
	dst = append(dst, `,"ts":"`...)
	// 时间布局由使用方控制，直接内联（Options.TimeFormat 文档已限制布局内容）。
	dst = appendTime(dst, t, l.timeFormat)
	dst = append(dst, '"')
	if l.service != "" {
		dst = append(dst, `,"service":`...)
		dst = appendJSONString(dst, l.service)
	}
	if l.name != "" {
		dst = append(dst, `,"logger":`...)
		dst = appendJSONString(dst, l.name)
	}
	if caller != "" {
		dst = append(dst, `,"caller":`...)
		dst = appendJSONString(dst, caller)
	}
	dst = append(dst, `,"msg":`...)
	dst = appendJSONString(dst, msg)
	dst = appendJSONFields(dst, l.fields, l.timeFormat)
	dst = appendJSONFields(dst, extra, l.timeFormat)
	return append(dst, '}', '\n')
}

// appendJSONFields 依次编码一组字段。
func appendJSONFields(dst []byte, fields []Field, timeFormat string) []byte {
	for i := range fields {
		dst = appendJSONField(dst, &fields[i], timeFormat)
	}
	return dst
}

// appendJSONField 将单个字段编码为 JSON 成员（含前导逗号）追加到 dst。
//
// UnknownType 字段（含 Err(nil) 的结果）整条跳过；Key 为空且类型为 RawType 时
// 原样注入片段（Raw），其余无键字段同样跳过。
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

// appendJSONValue 仅编码字段的值部分（不含键与前导逗号）。
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
		// 毫秒数（float64），便于机器聚合。
		return appendJSONFloat(dst, float64(int64(f.num))/float64(time.Millisecond))
	case BytesType:
		dst = append(dst, '"')
		dst = b64x.StdEncoding.AppendEncode(dst, f.bs)
		return append(dst, '"')
	case AnyType:
		return appendJSONAny(dst, f.obj)
	case RawType:
		// 调用方保证片段是合法 JSON。
		return append(dst, f.bs...)
	}
	return append(dst, "null"...)
}

// appendJSONStrings 追加 []string 的 JSON 数组。
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

// appendJSONFloat 追加 float64；NaN 与 ±Inf 输出 null（JSON 无对应字面量）。
func appendJSONFloat(dst []byte, v float64) []byte {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return append(dst, "null"...)
	}
	return strconv.AppendFloat(dst, v, 'g', -1, 64)
}

// appendJSONAny 编码 Any 慢路径的装箱值：使用 encoding/json（内部反射 + 额外分配）。
// 编码失败时输出描述该错误的 JSON 字符串，保证结果始终是合法 JSON。
func appendJSONAny(dst []byte, v any) []byte {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return appendJSONString(dst, "!json: "+err.Error())
	}
	return append(dst, b...)
}

// appendJSONString 把 s 按 JSON 字符串规则转义并追加到 dst（含两侧引号）。
//
// 转义 '"'、'\\' 与全部 C0 控制字符（\b \f \n \r \t 用短转义，其余用 \u00XX）；
// 输入假定为合法 UTF-8，多字节序列原样透传，因此中文不会被转成 \uXXXX。
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
