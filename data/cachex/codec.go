package cachex

import (
	"strconv"
	"time"

	"github.com/shibingli/yd/core/jsonx"
)

// JSON returns the default codec for any JSON-serializable type. | JSON 返回适用于任何可 JSON 序列化类型的默认编解码器。
//
// It supports structs, slices, arrays, maps, pointers with string keys and interface values; | 它支持结构体、切片、数组、字符串键的 map、指针与接口值；
// numbers decode into their natural Go types, and interface values decode through jsonx. | 数字解码为自然的 Go 类型，接口值经 jsonx 解码。
func JSON[T any]() Codec[T] {
	return Codec[T]{
		Encode: func(v T) ([]byte, error) { return jsonx.Marshal(v) },
		Decode: func(b []byte) (T, error) {
			var v T
			err := jsonx.Unmarshal(b, &v)
			return v, err
		},
	}
}

// Bytes returns a codec for []byte values. | Bytes 返回 []byte 值的编解码器。
//
// Encode is zero-copy: the cache copies the bytes before Set returns. Decode copies, because | Encode 是零拷贝的：缓存在 Set 返回前复制字节。Decode 会复制，
// the decoded value outlives the cache view. | 因为解码后的值会超出缓存视图的生命周期。
func Bytes() Codec[[]byte] {
	return Codec[[]byte]{
		Encode: func(v []byte) ([]byte, error) { return v, nil },
		Decode: func(b []byte) ([]byte, error) { return append([]byte(nil), b...), nil },
	}
}

// String returns a codec for string values. | String 返回字符串值的编解码器。
func String() Codec[string] {
	return Codec[string]{
		Encode: func(v string) ([]byte, error) { return []byte(v), nil },
		Decode: func(b []byte) (string, error) { return string(b), nil },
	}
}

// Int returns a codec for int values. | Int 返回 int 值的编解码器。
func Int() Codec[int] {
	return Codec[int]{
		Encode: func(v int) ([]byte, error) { return strconv.AppendInt(nil, int64(v), 10), nil },
		Decode: func(b []byte) (int, error) {
			v, err := strconv.ParseInt(string(b), 10, 64)
			return int(v), err
		},
	}
}

// Int64 returns a codec for int64 values. | Int64 返回 int64 值的编解码器。
func Int64() Codec[int64] {
	return Codec[int64]{
		Encode: func(v int64) ([]byte, error) { return strconv.AppendInt(nil, v, 10), nil },
		Decode: func(b []byte) (int64, error) { return strconv.ParseInt(string(b), 10, 64) },
	}
}

// Uint64 returns a codec for uint64 values. | Uint64 返回 uint64 值的编解码器。
func Uint64() Codec[uint64] {
	return Codec[uint64]{
		Encode: func(v uint64) ([]byte, error) { return strconv.AppendUint(nil, v, 10), nil },
		Decode: func(b []byte) (uint64, error) { return strconv.ParseUint(string(b), 10, 64) },
	}
}

// Float64 returns a codec for float64 values. | Float64 返回 float64 值的编解码器。
func Float64() Codec[float64] {
	return Codec[float64]{
		Encode: func(v float64) ([]byte, error) { return strconv.AppendFloat(nil, v, 'g', -1, 64), nil },
		Decode: func(b []byte) (float64, error) { return strconv.ParseFloat(string(b), 64) },
	}
}

// Bool returns a codec for bool values. | Bool 返回 bool 值的编解码器。
func Bool() Codec[bool] {
	return Codec[bool]{
		Encode: func(v bool) ([]byte, error) {
			if v {
				return []byte("1"), nil
			}
			return []byte("0"), nil
		},
		Decode: func(b []byte) (bool, error) { return strconv.ParseBool(string(b)) },
	}
}

// Time returns a codec that stores UTC RFC 3339 timestamps with nanoseconds. | Time 返回以 UTC RFC 3339（含纳秒）存储时间的编解码器。
func Time() Codec[time.Time] {
	return Codec[time.Time]{
		Encode: func(v time.Time) ([]byte, error) { return []byte(v.UTC().Format(time.RFC3339Nano)), nil },
		Decode: func(b []byte) (time.Time, error) { return time.Parse(time.RFC3339Nano, string(b)) },
	}
}
