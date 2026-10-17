//go:build jsonx_native

package jsonx

import (
	"encoding/binary"
	stdjson "encoding/json"
	"math"
	"reflect"
	"strconv"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/shibingli/yd/core/errx"
)

func fieldOffset(t reflect.Type, index []int) (uintptr, bool) {
	var off uintptr
	for pos, i := range index {
		if t.Kind() != reflect.Struct || i >= t.NumField() {
			return 0, false
		}
		f := t.Field(i)
		off += f.Offset
		t = f.Type
		if pos < len(index)-1 && t.Kind() == reflect.Pointer {
			return 0, false
		}
	}
	return off, true
}

// buildFastFieldEncoder returns a direct-memory encoder for primitive fields. | buildFastFieldEncoder
// 为原生基础类型字段返回直接读写内存的编码器。
func buildFastFieldEncoder(t reflect.Type) func(*encoder, unsafe.Pointer) error {
	if t == stringSliceType {
		return func(e *encoder, p unsafe.Pointer) error {
			ss := *(*[]string)(p)
			if ss == nil {
				encodeNull(e)
				return nil
			}
			e.buf = append(e.buf, '[')
			for i, s := range ss {
				if i > 0 {
					e.buf = append(e.buf, ',')
				}
				e.str(s)
			}
			e.buf = append(e.buf, ']')
			return nil
		}
	}
	if t == timeType {
		return func(e *encoder, p unsafe.Pointer) error {
			tt := (*time.Time)(p)
			if y := tt.Year(); y < 0 || y >= 10000 {
				return errx.Detail(errUnsupported, "time year %d out of range | 时间年份 %d 超出范围", y, y)
			}
			entry := &e.timeCache[uint64(tt.UnixNano())&3]
			if entry.n != 0 && entry.k == *tt {
				e.buf = append(e.buf, '"')
				e.buf = append(e.buf, entry.b[:entry.n]...)
				e.buf = append(e.buf, '"')
				return nil
			}
			start := len(e.buf)
			e.buf = append(e.buf, '"')
			e.buf = tt.AppendFormat(e.buf, time.RFC3339Nano)
			e.buf = append(e.buf, '"')
			formatted := e.buf[start+1 : len(e.buf)-1]
			if len(formatted) <= len(entry.b) {
				copy(entry.b[:], formatted)
				entry.n = uint8(len(formatted))
				entry.k = *tt
			}
			return nil
		}
	}
	switch t.Kind() {
	case reflect.String:
		return func(e *encoder, p unsafe.Pointer) error {
			e.str(*(*string)(p))
			return nil
		}
	case reflect.Bool:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendBool(e.buf, *(*bool)(p))
			return nil
		}
	case reflect.Int:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendInt(e.buf, int64(*(*int)(p)), 10)
			return nil
		}
	case reflect.Int8:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendInt(e.buf, int64(*(*int8)(p)), 10)
			return nil
		}
	case reflect.Int16:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendInt(e.buf, int64(*(*int16)(p)), 10)
			return nil
		}
	case reflect.Int32:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendInt(e.buf, int64(*(*int32)(p)), 10)
			return nil
		}
	case reflect.Int64:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendInt(e.buf, *(*int64)(p), 10)
			return nil
		}
	case reflect.Uint:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendUint(e.buf, uint64(*(*uint)(p)), 10)
			return nil
		}
	case reflect.Uint8:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendUint(e.buf, uint64(*(*uint8)(p)), 10)
			return nil
		}
	case reflect.Uint16:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendUint(e.buf, uint64(*(*uint16)(p)), 10)
			return nil
		}
	case reflect.Uint32:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendUint(e.buf, uint64(*(*uint32)(p)), 10)
			return nil
		}
	case reflect.Uint64:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendUint(e.buf, *(*uint64)(p), 10)
			return nil
		}
	case reflect.Uintptr:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = strconv.AppendUint(e.buf, uint64(*(*uintptr)(p)), 10)
			return nil
		}
	case reflect.Float32:
		return func(e *encoder, p unsafe.Pointer) error {
			return e.float(float64(*(*float32)(p)), 32)
		}
	case reflect.Float64:
		return func(e *encoder, p unsafe.Pointer) error {
			return e.float(*(*float64)(p), 64)
		}
	}
	return nil
}

// buildFastEmpty returns a direct emptiness check for primitive fields. | buildFastEmpty
// 为基础类型字段返回直接判空函数。
func buildFastEmpty(t reflect.Type) func(unsafe.Pointer) bool {
	switch t.Kind() {
	case reflect.String:
		return func(p unsafe.Pointer) bool { return len(*(*string)(p)) == 0 }
	case reflect.Bool:
		return func(p unsafe.Pointer) bool { return !*(*bool)(p) }
	case reflect.Int:
		return func(p unsafe.Pointer) bool { return *(*int)(p) == 0 }
	case reflect.Int8:
		return func(p unsafe.Pointer) bool { return *(*int8)(p) == 0 }
	case reflect.Int16:
		return func(p unsafe.Pointer) bool { return *(*int16)(p) == 0 }
	case reflect.Int32:
		return func(p unsafe.Pointer) bool { return *(*int32)(p) == 0 }
	case reflect.Int64:
		return func(p unsafe.Pointer) bool { return *(*int64)(p) == 0 }
	case reflect.Uint:
		return func(p unsafe.Pointer) bool { return *(*uint)(p) == 0 }
	case reflect.Uint8:
		return func(p unsafe.Pointer) bool { return *(*uint8)(p) == 0 }
	case reflect.Uint16:
		return func(p unsafe.Pointer) bool { return *(*uint16)(p) == 0 }
	case reflect.Uint32:
		return func(p unsafe.Pointer) bool { return *(*uint32)(p) == 0 }
	case reflect.Uint64:
		return func(p unsafe.Pointer) bool { return *(*uint64)(p) == 0 }
	case reflect.Uintptr:
		return func(p unsafe.Pointer) bool { return *(*uintptr)(p) == 0 }
	case reflect.Float32:
		return func(p unsafe.Pointer) bool { return *(*float32)(p) == 0 }
	case reflect.Float64:
		return func(p unsafe.Pointer) bool { return *(*float64)(p) == 0 }
	}
	return nil
}

func (e *encoder) marshaler(m stdjson.Marshaler) error {
	b, err := m.MarshalJSON()
	if err != nil {
		return err
	}
	e.buf = append(e.buf, b...)
	return nil
}

func (e *encoder) float(f float64, bits int) error {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return errx.Detail(errUnsupported, "unsupported float value %v | 不支持的浮点值 %v", f, f)
	}
	fmt := byte('f')
	if abs := math.Abs(f); abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		fmt = 'e'
	}
	e.buf = strconv.AppendFloat(e.buf, f, fmt, -1, bits)
	if fmt == 'e' {
		if n := len(e.buf); n >= 4 && e.buf[n-4] == 'e' && e.buf[n-3] == '-' && e.buf[n-2] == '0' {
			e.buf[n-2] = e.buf[n-1]
			e.buf = e.buf[:n-1]
		}
	}
	return nil
}

const (
	wordLo = 0x0101010101010101
	wordHi = 0x8080808080808080

	wordSpace     = 0x2020202020202020
	wordQuote     = 0x2222222222222222
	wordBackslash = 0x5c5c5c5c5c5c5c5c
	wordLessThan  = 0x3c3c3c3c3c3c3c3c
	wordGreater   = 0x3e3e3e3e3e3e3e3e
	wordAmpersand = 0x2626262626262626
)

// wordHasZero reports whether any byte of x is zero. | wordHasZero \u62a5\u544a x \u4e2d\u662f\u5426\u6709\u4efb\u4e00\u5b57\u8282\u4e3a\u96f6\u3002
func wordHasZero(x uint64) bool {
	return (x-wordLo)&^x&wordHi != 0
}

// wordHasControl reports whether any byte of x is below 0x20. | wordHasControl \u62a5\u544a x \u4e2d\u662f\u5426\u6709\u4efb\u4e00
// \u5b57\u8282\u5c0f\u4e8e 0x20\u3002
func wordHasControl(x uint64) bool {
	return (x-wordSpace)&^x&wordHi != 0
}

const (
	wordQuote32     uint32 = 0x22222222
	wordBackslash32 uint32 = 0x5c5c5c5c
)

// wordHasZero32 reports whether any byte of x is zero. | wordHasZero32 \u62a5\u544a x \u4e2d\u662f\u5426\u6709\u4efb\u4e00\u5b57\u8282\u4e3a\u96f6\u3002
func wordHasZero32(x uint32) bool {
	return (x-0x01010101)&^x&0x80808080 != 0
}

// wordHasControl32 reports whether any byte of x is below 0x20. | wordHasControl32 \u62a5\u544a x \u4e2d\u662f\u5426\u6709
// \u4efb\u4e00\u5b57\u8282\u5c0f\u4e8e 0x20\u3002
func wordHasControl32(x uint32) bool {
	return (x-0x20202020)&^x&0x80808080 != 0
}

func (e *encoder) str(s string) {
	i := 0
	for len(s)-i >= 8 {
		x := binary.LittleEndian.Uint64(unsafe.Slice(unsafe.StringData(s[i:]), 8))
		if x&wordHi != 0 || wordHasControl(x) ||
			wordHasZero(x^wordQuote) || wordHasZero(x^wordBackslash) ||
			(e.escapeHTML && (wordHasZero(x^wordLessThan) || wordHasZero(x^wordGreater) || wordHasZero(x^wordAmpersand))) {
			goto slow
		}
		i += 8
	}
	for ; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == '"' || c == '\\' || c >= 0x80 || (e.escapeHTML && (c == '<' || c == '>' || c == '&')) {
			goto slow
		}
	}
	e.buf = append(e.buf, '"')
	e.buf = append(e.buf, s...)
	e.buf = append(e.buf, '"')
	return
slow:
	e.buf = append(e.buf, '"')
	for _, r := range s {
		switch r {
		case '"':
			e.buf = append(e.buf, '\\', '"')
		case '\\':
			e.buf = append(e.buf, '\\', '\\')
		case '\n':
			e.buf = append(e.buf, '\\', 'n')
		case '\r':
			e.buf = append(e.buf, '\\', 'r')
		case '\t':
			e.buf = append(e.buf, '\\', 't')
		case '<', '>', '&':
			if e.escapeHTML {
				e.buf = append(e.buf, '\\', 'u', '0', '0', hexDigit(byte(r)>>4), hexDigit(byte(r)))
			} else {
				e.buf = utf8.AppendRune(e.buf, r)
			}
		case '\u2028', '\u2029':
			e.buf = append(e.buf, '\\', 'u', '2', '0', '2', hexDigit(byte(r)))
		default:
			if r < 0x20 {
				e.buf = append(e.buf, '\\', 'u', '0', '0', hexDigit(byte(r)>>4), hexDigit(byte(r)))
			} else {
				e.buf = utf8.AppendRune(e.buf, r)
			}
		}
	}
	e.buf = append(e.buf, '"')
}

func hexDigit(b byte) byte {
	const hex = "0123456789abcdef"
	return hex[b&0xF]
}

func (e *encoder) quotedValue(v reflect.Value) error {
	tmp := &encoder{escapeHTML: e.escapeHTML}
	if err := tmp.value(v); err != nil {
		return err
	}
	e.str(string(tmp.buf))
	return nil
}

// decoder parses one JSON value at a time. | decoder 每次解析一个 JSON 值。
