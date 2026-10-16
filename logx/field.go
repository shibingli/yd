package logx

import (
	"errors"
	"math"
	"time"
)

// FieldType 标识 Field 承载的值类型，供编码器选择零反射的编码分支。
type FieldType uint8

const (
	// UnknownType 表示空字段，编码时整条跳过（例如 Err(nil) 的结果）。
	UnknownType FieldType = iota
	// StringType 是 string 值（Str / Any(string)）。
	StringType
	// StringsType 是 []string 值（Strs / Any([]string)）。
	StringsType
	// IntType 是 int 值（Int / Any(int)），内部以 int64 位模式承载。
	IntType
	// Int64Type 是 int64 值。
	Int64Type
	// Uint64Type 是 uint64 值。
	Uint64Type
	// Float64Type 是 float64 值，内部以 IEEE-754 位模式承载。
	Float64Type
	// BoolType 是 bool 值，内部以 0/1 承载。
	BoolType
	// TimeType 是 time.Time 值，内部以 Unix 纳秒承载（一律按 UTC 输出）。
	TimeType
	// DurationType 是 time.Duration 值，内部以纳秒承载；
	// JSON 输出毫秒数，console 输出人类可读字符串。
	DurationType
	// ErrorType 是 error 值，内部承载已展开的错误链消息（键名由构造方指定）。
	ErrorType
	// BytesType 是 []byte 值，JSON 按 base64（RFC 4648 标准编码）输出，与 encoding/json 对 []byte 的约定一致。
	BytesType
	// AnyType 是 Any 的慢路径：未知类型装箱后交给 encoding/json（允许反射）。
	AnyType
	// RawType 是 Raw / RawField 直接内联的 JSON 片段（调用方保证其合法性）。
	RawType
)

// errorKey 是 Err 使用的默认键名。
const errorKey = "error"

// Field 是结构化日志字段。
//
// 值以联合表示承载：字符串进 str、整型/浮点/布尔/时长/时间进 num、
// 字节切片与 Raw 片段进 bs、冷路径值（Any、[]string）进 obj。
// 热路径字段（Str/Int/Int64/Uint64/Float64/Bool/Dur/Err/Time/Bytes）不经过 any 装箱，
// 也不借助反射；Field 是值类型，构造它不会产生堆分配。
//
// Field 只能通过本包的构造函数创建，零值（UnknownType）在编码时被跳过。
type Field struct {
	// Key 是字段名；为空且类型为 RawType 时表示无键的片段注入。
	Key string
	// Type 是字段值的类型标签。
	Type FieldType
	// num 承载 Int/Int64/Uint64/Float64/Bool/Dur/Time 的位模式或数值。
	num uint64
	// str 承载 StringType 与 ErrorType 的文本。
	str string
	// bs 承载 BytesType 的原始字节与 RawType 的 JSON 片段（不复制调用方数据）。
	bs []byte
	// obj 承载冷路径值（AnyType 装箱值、StringsType 的 []string）。
	obj any
}

// Str 返回字符串字段。该字符串不被复制，调用方不得在日志写入前改写其底层内存。
func Str(k, v string) Field {
	return Field{Key: k, Type: StringType, str: v}
}

// Strs 返回字符串切片字段（JSON 数组，console 为 JSON 数组字面量）。
// 该切片不被复制，调用方不得在日志写入前改写其元素。
func Strs(k string, v []string) Field {
	return Field{Key: k, Type: StringsType, obj: v}
}

// Int 返回 int 字段（内部按 64 位承载，32 位平台同样无损）。
func Int(k string, v int) Field {
	return Field{Key: k, Type: IntType, num: uint64(int64(v))}
}

// Int64 返回 int64 字段。
func Int64(k string, v int64) Field {
	return Field{Key: k, Type: Int64Type, num: uint64(v)}
}

// Uint64 返回 uint64 字段。
func Uint64(k string, v uint64) Field {
	return Field{Key: k, Type: Uint64Type, num: v}
}

// Float64 返回 float64 字段；NaN 与 ±Inf 在 JSON 中输出为 null（JSON 无对应字面量）。
func Float64(k string, v float64) Field {
	return Field{Key: k, Type: Float64Type, num: math.Float64bits(v)}
}

// Bool 返回 bool 字段。
func Bool(k string, v bool) Field {
	var n uint64
	if v {
		n = 1
	}
	return Field{Key: k, Type: BoolType, num: n}
}

// Time 返回时间字段。
//
// 时间以 Unix 纳秒承载（不装箱、不分配），输出时一律按 UTC 渲染；
// 超出 Unix 纳秒表示范围（约 1678—2262 年）的时间请改用 Str 或 Any。
func Time(k string, v time.Time) Field {
	return Field{Key: k, Type: TimeType, num: uint64(v.UnixNano())}
}

// Dur 返回时长字段。
//
// JSON 中以毫秒数（float64，可含小数）输出，便于机器聚合；
// console 中以人类可读字符串输出，例如 "1.5ms"、"300µs"、"2s"。
func Dur(k string, v time.Duration) Field {
	return Field{Key: k, Type: DurationType, num: uint64(v)}
}

// Err 返回错误字段，键名固定为 "error"。
//
// 错误链会被 errors.Unwrap（含 errors.Join 的多路展开）展开后按 ": " 拼接；
// 若某层消息已被上层消息包含（如 fmt.Errorf 的 "%w" 形式），则跳过以免重复。
// err 为 nil 时返回零值字段，编码时整条跳过。
func Err(err error) Field {
	return errField(errorKey, err)
}

// Bytes 返回字节切片字段，JSON 按 base64（RFC 4648 标准编码）输出。
// 该切片不被复制，调用方不得在日志写入前改写其内容。
func Bytes(k string, v []byte) Field {
	return Field{Key: k, Type: BytesType, bs: v}
}

// Any 返回任意值字段，属于慢路径。
//
// 常见类型（string、bool、各整型、float、time.Time、time.Duration、error、
// []byte、[]string、nil）会被就地归一到对应的无反射表示；
// 其余类型装箱后交给 encoding/json（内部使用反射且有额外分配），不应出现在热路径上。
func Any(k string, v any) Field {
	switch x := v.(type) {
	case nil:
		return Field{}
	case string:
		return Str(k, x)
	case bool:
		return Bool(k, x)
	case int:
		return Int(k, x)
	case int8:
		return Int64(k, int64(x))
	case int16:
		return Int64(k, int64(x))
	case int32:
		return Int64(k, int64(x))
	case int64:
		return Int64(k, x)
	case uint:
		return Uint64(k, uint64(x))
	case uint8:
		return Uint64(k, uint64(x))
	case uint16:
		return Uint64(k, uint64(x))
	case uint32:
		return Uint64(k, uint64(x))
	case uint64:
		return Uint64(k, x)
	case float32:
		return Float64(k, float64(x))
	case float64:
		return Float64(k, x)
	case time.Time:
		return Time(k, x)
	case time.Duration:
		return Dur(k, x)
	case error:
		return errField(k, x)
	case []byte:
		return Bytes(k, x)
	case []string:
		return Strs(k, x)
	case Field:
		// 允许把已构造好的字段重新命名。
		x.Key = k
		return x
	}
	return Field{Key: k, Type: AnyType, obj: v}
}

// Raw 返回无键的原始 JSON 片段字段，片段被原样注入输出。
// json 必须是一个完整的 JSON 成员（形如 `"key":value`），调用方负责其合法性。
func Raw(json []byte) Field {
	return Field{Type: RawType, bs: json}
}

// RawField 返回带键的原始 JSON 值字段，键名经 JSON 转义、值被原样注入。
// json 必须是一个完整的 JSON 值（对象、数组、数字、字符串等），调用方负责其合法性。
func RawField(key string, json []byte) Field {
	return Field{Key: key, Type: RawType, bs: json}
}

// errField 以指定键构造错误字段，并展开错误链。
func errField(key string, err error) Field {
	if err == nil {
		return Field{}
	}
	msg := err.Error()
	if hasWrapped(err) {
		buf := make([]byte, 0, len(msg)+32)
		buf = appendErrChain(buf, err)
		msg = string(buf)
	}
	return Field{Key: key, Type: ErrorType, str: msg}
}

// hasWrapped 报告 err 是否包裹了其它错误。
func hasWrapped(err error) bool {
	if errors.Unwrap(err) != nil {
		return true
	}
	_, ok := err.(interface{ Unwrap() []error })
	return ok
}

// appendErrChain 先序展开错误链并拼接各层消息，跳过已被包含的重复消息。
// errors.Join 产生的多路错误按分支递归展开。
func appendErrChain(dst []byte, err error) []byte {
	if err == nil {
		return dst
	}
	if msg := err.Error(); !containsSub(dst, msg) {
		if len(dst) > 0 {
			dst = append(dst, ':', ' ')
		}
		dst = append(dst, msg...)
	}
	if u := errors.Unwrap(err); u != nil {
		return appendErrChain(dst, u)
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range joined.Unwrap() {
			dst = appendErrChain(dst, e)
		}
	}
	return dst
}

// containsSub 报告 hay 中是否包含子串 needle（不产生分配）。
func containsSub(hay []byte, needle string) bool {
	if needle == "" {
		return true
	}
	if len(needle) > len(hay) {
		return false
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		if string(hay[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
