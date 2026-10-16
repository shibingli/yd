package logx

import (
	"errors"
	"math"
	"time"
)

// FieldType identifies the kind of value a Field carries, letting the encoder choose a reflection-free encoding branch. | FieldType 标识 Field 承载的值类型，供编码器选择零反射的编码分支。
type FieldType uint8

const (
	// UnknownType marks an empty field, skipped entirely when encoding (for example the result of Err(nil)). | UnknownType 表示空字段，编码时整条跳过（例如 Err(nil) 的结果）。
	UnknownType FieldType = iota
	// StringType is a string value (Str / Any(string)). | StringType 是 string 值（Str / Any(string)）。
	StringType
	// StringsType is a []string value (Strs / Any([]string)). | StringsType 是 []string 值（Strs / Any([]string)）。
	StringsType
	// IntType is an int value (Int / Any(int)), carried internally as an int64 bit pattern. | IntType 是 int 值（Int / Any(int)），内部以 int64 位模式承载。
	IntType
	// Int64Type is an int64 value. | Int64Type 是 int64 值。
	Int64Type
	// Uint64Type is a uint64 value. | Uint64Type 是 uint64 值。
	Uint64Type
	// Float64Type is a float64 value, carried internally as an IEEE-754 bit pattern. | Float64Type 是 float64 值，内部以 IEEE-754 位模式承载。
	Float64Type
	// BoolType is a bool value, carried internally as 0/1. | BoolType 是 bool 值，内部以 0/1 承载。
	BoolType
	// TimeType is a time.Time value, carried internally as Unix nanoseconds (always rendered as UTC). | TimeType 是 time.Time 值，内部以 Unix 纳秒承载（一律按 UTC 输出）。
	TimeType
	// DurationType is a time.Duration value, carried internally as nanoseconds; | DurationType 是 time.Duration 值，内部以纳秒承载；
	// JSON outputs milliseconds while console outputs a human-readable string. | JSON 输出毫秒数，console 输出人类可读字符串。
	DurationType
	// ErrorType is an error value, carrying the already expanded error-chain message internally (the key name is chosen by the constructor). | ErrorType 是 error 值，内部承载已展开的错误链消息（键名由构造方指定）。
	ErrorType
	// BytesType is a []byte value, output as base64 in JSON (RFC 4648 standard encoding), consistent with the encoding/json convention for []byte. | BytesType 是 []byte 值，JSON 按 base64（RFC 4648 标准编码）输出，与 encoding/json 对 []byte 的约定一致。
	BytesType
	// AnyType is the slow path of Any: unknown types are boxed and handed to encoding/json (reflection allowed). | AnyType 是 Any 的慢路径：未知类型装箱后交给 encoding/json（允许反射）。
	AnyType
	// RawType is a JSON fragment inlined directly by Raw / RawField (the caller guarantees its validity). | RawType 是 Raw / RawField 直接内联的 JSON 片段（调用方保证其合法性）。
	RawType
)

// errorKey is the default key name used by Err. | errorKey 是 Err 使用的默认键名。
const errorKey = "error"

// Field is a structured log field. | Field 是结构化日志字段。
//
// The value is carried in a union representation: strings go into str, integers/floats/booleans/durations/times into num, | 值以联合表示承载：字符串进 str、整型/浮点/布尔/时长/时间进 num、
// byte slices and Raw fragments into bs, and cold-path values (Any, []string) into obj. | 字节切片与 Raw 片段进 bs、冷路径值（Any、[]string）进 obj。
// Hot-path fields (Str/Int/Int64/Uint64/Float64/Bool/Dur/Err/Time/Bytes) never go through any boxing, | 热路径字段（Str/Int/Int64/Uint64/Float64/Bool/Dur/Err/Time/Bytes）不经过 any 装箱，
// nor do they rely on reflection; Field is a value type, so constructing one causes no heap allocation. | 也不借助反射；Field 是值类型，构造它不会产生堆分配。
//
// Field can only be created through this package's constructors; the zero value (UnknownType) is skipped when encoding. | Field 只能通过本包的构造函数创建，零值（UnknownType）在编码时被跳过。
type Field struct {
	// Key is the field name; when it is empty and the type is RawType it means a keyless fragment injection. | Key 是字段名；为空且类型为 RawType 时表示无键的片段注入。
	Key string
	// Type is the type tag of the field value. | Type 是字段值的类型标签。
	Type FieldType
	// num carries the bit pattern or numeric value of Int/Int64/Uint64/Float64/Bool/Dur/Time. | num 承载 Int/Int64/Uint64/Float64/Bool/Dur/Time 的位模式或数值。
	num uint64
	// str carries the text of StringType and ErrorType. | str 承载 StringType 与 ErrorType 的文本。
	str string
	// bs carries the raw bytes of BytesType and the JSON fragment of RawType (the caller's data is not copied). | bs 承载 BytesType 的原始字节与 RawType 的 JSON 片段（不复制调用方数据）。
	bs []byte
	// obj carries cold-path values (the boxed value of AnyType, the []string of StringsType). | obj 承载冷路径值（AnyType 装箱值、StringsType 的 []string）。
	obj any
}

// Str returns a string field. The string is not copied, so the caller must not modify its underlying memory before the log is written. | Str 返回字符串字段。该字符串不被复制，调用方不得在日志写入前改写其底层内存。
func Str(k, v string) Field {
	return Field{Key: k, Type: StringType, str: v}
}

// Strs returns a string slice field (a JSON array; in console it is a JSON array literal). | Strs 返回字符串切片字段（JSON 数组，console 为 JSON 数组字面量）。
// The slice is not copied, so the caller must not modify its elements before the log is written. | 该切片不被复制，调用方不得在日志写入前改写其元素。
func Strs(k string, v []string) Field {
	return Field{Key: k, Type: StringsType, obj: v}
}

// Int returns an int field (carried as 64 bits internally, so it is lossless on 32-bit platforms too). | Int 返回 int 字段（内部按 64 位承载，32 位平台同样无损）。
func Int(k string, v int) Field {
	return Field{Key: k, Type: IntType, num: uint64(int64(v))}
}

// Int64 returns an int64 field. | Int64 返回 int64 字段。
func Int64(k string, v int64) Field {
	return Field{Key: k, Type: Int64Type, num: uint64(v)}
}

// Uint64 returns a uint64 field. | Uint64 返回 uint64 字段。
func Uint64(k string, v uint64) Field {
	return Field{Key: k, Type: Uint64Type, num: v}
}

// Float64 returns a float64 field; NaN and ±Inf are output as null in JSON (JSON has no matching literal). | Float64 返回 float64 字段；NaN 与 ±Inf 在 JSON 中输出为 null（JSON 无对应字面量）。
func Float64(k string, v float64) Field {
	return Field{Key: k, Type: Float64Type, num: math.Float64bits(v)}
}

// Bool returns a bool field. | Bool 返回 bool 字段。
func Bool(k string, v bool) Field {
	var n uint64
	if v {
		n = 1
	}
	return Field{Key: k, Type: BoolType, num: n}
}

// Time returns a time field. | Time 返回时间字段。
//
// The time is carried as Unix nanoseconds (no boxing, no allocation) and is always rendered as UTC on output; | 时间以 Unix 纳秒承载（不装箱、不分配），输出时一律按 UTC 渲染；
// for times outside the range representable by Unix nanoseconds (roughly the years 1678-2262) use Str or Any instead. | 超出 Unix 纳秒表示范围（约 1678—2262 年）的时间请改用 Str 或 Any。
func Time(k string, v time.Time) Field {
	return Field{Key: k, Type: TimeType, num: uint64(v.UnixNano())}
}

// Dur returns a duration field. | Dur 返回时长字段。
//
// In JSON it is output as milliseconds (float64, possibly fractional), which makes machine aggregation easy; | JSON 中以毫秒数（float64，可含小数）输出，便于机器聚合；
// in console it is output as a human-readable string, for example "1.5ms", "300µs", "2s". | console 中以人类可读字符串输出，例如 "1.5ms"、"300µs"、"2s"。
func Dur(k string, v time.Duration) Field {
	return Field{Key: k, Type: DurationType, num: uint64(v)}
}

// Err returns an error field whose key name is fixed to "error". | Err 返回错误字段，键名固定为 "error"。
//
// The error chain is expanded with errors.Unwrap (including the multi-branch expansion of errors.Join) and then joined with ": "; | 错误链会被 errors.Unwrap（含 errors.Join 的多路展开）展开后按 ": " 拼接；
// if a layer's message is already contained in an outer message (as with the "%w" form of fmt.Errorf), it is skipped to avoid duplication. | 若某层消息已被上层消息包含（如 fmt.Errorf 的 "%w" 形式），则跳过以免重复。
// When err is nil it returns the zero-value field, which is skipped entirely when encoding. | err 为 nil 时返回零值字段，编码时整条跳过。
func Err(err error) Field {
	return errField(errorKey, err)
}

// Bytes returns a byte slice field, output as base64 in JSON (RFC 4648 standard encoding). | Bytes 返回字节切片字段，JSON 按 base64（RFC 4648 标准编码）输出。
// The slice is not copied, so the caller must not modify its contents before the log is written. | 该切片不被复制，调用方不得在日志写入前改写其内容。
func Bytes(k string, v []byte) Field {
	return Field{Key: k, Type: BytesType, bs: v}
}

// Any returns a field holding an arbitrary value and belongs to the slow path. | Any 返回任意值字段，属于慢路径。
//
// Common types (string, bool, each integer kind, float, time.Time, time.Duration, error, | 常见类型（string、bool、各整型、float、time.Time、time.Duration、error、
// []byte, []string, nil) are normalized in place to the corresponding reflection-free representation; | []byte、[]string、nil）会被就地归一到对应的无反射表示；
// all other types are boxed and handed to encoding/json (which uses reflection internally and allocates extra), so they should not appear on hot paths. | 其余类型装箱后交给 encoding/json（内部使用反射且有额外分配），不应出现在热路径上。
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
		// Allows an already constructed field to be renamed. | 允许把已构造好的字段重新命名。
		x.Key = k
		return x
	}
	return Field{Key: k, Type: AnyType, obj: v}
}

// Raw returns a keyless raw JSON fragment field; the fragment is injected into the output verbatim. | Raw 返回无键的原始 JSON 片段字段，片段被原样注入输出。
// json must be a complete JSON member (of the form `"key":value`); the caller is responsible for its validity. | json 必须是一个完整的 JSON 成员（形如 `"key":value`），调用方负责其合法性。
func Raw(json []byte) Field {
	return Field{Type: RawType, bs: json}
}

// RawField returns a keyed raw JSON value field; the key name is JSON-escaped and the value is injected verbatim. | RawField 返回带键的原始 JSON 值字段，键名经 JSON 转义、值被原样注入。
// json must be a complete JSON value (object, array, number, string, and so on); the caller is responsible for its validity. | json 必须是一个完整的 JSON 值（对象、数组、数字、字符串等），调用方负责其合法性。
func RawField(key string, json []byte) Field {
	return Field{Key: key, Type: RawType, bs: json}
}

// errField constructs an error field with the given key and expands the error chain. | errField 以指定键构造错误字段，并展开错误链。
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

// hasWrapped reports whether err wraps any other error. | hasWrapped 报告 err 是否包裹了其它错误。
func hasWrapped(err error) bool {
	if errors.Unwrap(err) != nil {
		return true
	}
	_, ok := err.(interface{ Unwrap() []error })
	return ok
}

// appendErrChain expands the error chain in pre-order and joins the messages of each layer, skipping duplicates that are already contained. | appendErrChain 先序展开错误链并拼接各层消息，跳过已被包含的重复消息。
// Multi-branch errors produced by errors.Join are expanded recursively, branch by branch. | errors.Join 产生的多路错误按分支递归展开。
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

// containsSub reports whether hay contains the substring needle (without allocating). | containsSub 报告 hay 中是否包含子串 needle（不产生分配）。
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
