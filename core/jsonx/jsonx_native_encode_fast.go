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

// Fast field kinds let the struct encoder inline primitive bodies instead of calling through a
// function value. | 快速字段类别让结构体编码器内联基础类型编码体，而不是经函数值调用。
const (
	fieldKindNone uint8 = iota
	fieldKindString
	fieldKindBool
	fieldKindInt
	fieldKindInt8
	fieldKindInt16
	fieldKindInt32
	fieldKindInt64
	fieldKindUint
	fieldKindUint8
	fieldKindUint16
	fieldKindUint32
	fieldKindUint64
	fieldKindUintptr
	fieldKindFloat32
	fieldKindFloat64
)

// fastFieldKind maps a primitive kind to its inline dispatch id; composite and special types keep
// fieldKindNone and use the regular fast closure. | fastFieldKind 把基础类型映射为内联分派标识；
// 复合类型与特殊类型保持 fieldKindNone，走常规快速闭包。
func fastFieldKind(t reflect.Type) uint8 {
	switch t.Kind() {
	case reflect.String:
		return fieldKindString
	case reflect.Bool:
		return fieldKindBool
	case reflect.Int:
		return fieldKindInt
	case reflect.Int8:
		return fieldKindInt8
	case reflect.Int16:
		return fieldKindInt16
	case reflect.Int32:
		return fieldKindInt32
	case reflect.Int64:
		return fieldKindInt64
	case reflect.Uint:
		return fieldKindUint
	case reflect.Uint8:
		return fieldKindUint8
	case reflect.Uint16:
		return fieldKindUint16
	case reflect.Uint32:
		return fieldKindUint32
	case reflect.Uint64:
		return fieldKindUint64
	case reflect.Uintptr:
		return fieldKindUintptr
	case reflect.Float32:
		return fieldKindFloat32
	case reflect.Float64:
		return fieldKindFloat64
	}
	return fieldKindNone
}

// buildFastFieldEncoder returns a direct-memory encoder for primitive fields. | buildFastFieldEncoder
// 为原生基础类型字段返回直接读写内存的编码器。
func buildFastFieldEncoder(t reflect.Type) func(*encoder, unsafe.Pointer) error {
	// json.Number needs its own validation/zero handling, and any other type with a custom
	// marshal method must go through the generic encoder so the method is called. time.Time and
	// []string keep their dedicated fast encoders. | json.Number 需要自身的校验与零值处理；其余带
	// 自定义编码方法的类型必须走通用编码器以调用该方法；time.Time 与 []string 保留专用快速编码器。
	if t == numberType {
		return nil
	}
	if t != timeType && t != stringSliceType {
		tr := cachedTraits(t)
		if tr.marshaler || tr.textMarshaler {
			return nil
		}
		if t.Kind() != reflect.Pointer &&
			(reflect.PointerTo(t).Implements(marshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)) {
			return nil
		}
	}
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
			e.buf = appendIntFast(e.buf, int64(*(*int)(p)))
			return nil
		}
	case reflect.Int8:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = appendIntFast(e.buf, int64(*(*int8)(p)))
			return nil
		}
	case reflect.Int16:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = appendIntFast(e.buf, int64(*(*int16)(p)))
			return nil
		}
	case reflect.Int32:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = appendIntFast(e.buf, int64(*(*int32)(p)))
			return nil
		}
	case reflect.Int64:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = appendIntFast(e.buf, *(*int64)(p))
			return nil
		}
	case reflect.Uint:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = appendUintFast(e.buf, uint64(*(*uint)(p)))
			return nil
		}
	case reflect.Uint8:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = appendUintFast(e.buf, uint64(*(*uint8)(p)))
			return nil
		}
	case reflect.Uint16:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = appendUintFast(e.buf, uint64(*(*uint16)(p)))
			return nil
		}
	case reflect.Uint32:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = appendUintFast(e.buf, uint64(*(*uint32)(p)))
			return nil
		}
	case reflect.Uint64:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = appendUintFast(e.buf, *(*uint64)(p))
			return nil
		}
	case reflect.Uintptr:
		return func(e *encoder, p unsafe.Pointer) error {
			e.buf = appendUintFast(e.buf, uint64(*(*uintptr)(p)))
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
	var cerr error
	e.buf, cerr = appendCompactJSON(e.buf, b, e.escapeHTML)
	return cerr
}

// appendCompactJSON appends src after removing insignificant whitespace, validating the value and
// applying the same HTML / line-separator escaping as encoding/json does for Marshaler output.
// | appendCompactJSON 去除无意义空白后追加 src，同时校验值的合法性，并按 encoding/json 对
// Marshaler 输出的口径做 HTML 与行分隔符转义。
func appendCompactJSON(dst, src []byte, escapeHTML bool) ([]byte, error) {
	if !Valid(src) {
		return dst, syntaxErr("invalid JSON returned by Marshaler | Marshaler 返回了非法 JSON")
	}
	inString := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			switch {
			case c == '"':
				inString = false
				dst = append(dst, c)
			case c == '\\':
				dst = append(dst, c)
				i++
				if i < len(src) {
					dst = append(dst, src[i])
				}
			case escapeHTML && (c == '<' || c == '>' || c == '&'):
				dst = append(dst, '\\', 'u', '0', '0', hexDigit(c>>4), hexDigit(c))
			case escapeHTML && c == 0xE2 && i+2 < len(src) && src[i+1] == 0x80 && src[i+2]&^1 == 0xA8:
				dst = append(dst, '\\', 'u', '2', '0', '2', hexDigit(src[i+2]&0xF))
				i += 2
			default:
				dst = append(dst, c)
			}
			continue
		}
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '"':
			inString = true
		}
		dst = append(dst, c)
	}
	return dst, nil
}

func (e *encoder) float(f float64, bits int) error {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return errx.Detail(errUnsupported, "unsupported float value %v | 不支持的浮点值 %v", f, f)
	}
	// Integral values within the exact-integer range of the type print as plain decimal digits.
	// The bound is 2^24 for float32 because above it the shortest round-trip decimal no longer
	// equals the exact integer value (e.g. float32 1e15 prints as 1000000000000000, while its
	// exact value is 999999986991104). | 类型精确整数范围内的整值输出为十进制数字；float32 的
	// 边界取 2^24，因为超过后最短往返十进制不再等于精确整数值（例如 float32 1e15 输出
	// 1000000000000000，而精确值为 999999986991104）。
	limit := float64(1 << 53)
	if bits == 32 {
		limit = 1 << 24
	}
	if f == math.Trunc(f) && f >= -limit && f <= limit {
		if f == 0 {
			if math.Signbit(f) {
				e.buf = append(e.buf, '-', '0')
			} else {
				e.buf = append(e.buf, '0')
			}
			return nil
		}
		e.buf = appendIntFast(e.buf, int64(f))
		return nil
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
	wordLessThan32  uint32 = 0x3c3c3c3c
	wordGreater32   uint32 = 0x3e3e3e3e
	wordAmpersand32 uint32 = 0x26262626
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

// escapeTableHTML / escapeTablePlain classify bytes that need escaping in the two modes. The tail
// scan is at most three bytes, where a table load beats a chain of comparisons. | escapeTableHTML /
// escapeTablePlain 按两种模式标记需要转义的字节。尾部扫描最多三个字节，查表比一串比较更快。
var (
	escapeTableHTML  [256]bool
	escapeTablePlain [256]bool
)

func init() {
	for i := 0; i < 256; i++ {
		c := byte(i)
		plain := c < 0x20 || c == '"' || c == '\\' || c >= 0x80
		escapeTablePlain[i] = plain
		escapeTableHTML[i] = plain || c == '<' || c == '>' || c == '&'
	}
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
	for len(s)-i >= 4 {
		x := binary.LittleEndian.Uint32(unsafe.Slice(unsafe.StringData(s[i:]), 4))
		if x&0x80808080 != 0 || wordHasControl32(x) ||
			wordHasZero32(x^wordQuote32) || wordHasZero32(x^wordBackslash32) ||
			(e.escapeHTML && (wordHasZero32(x^wordLessThan32) || wordHasZero32(x^wordGreater32) || wordHasZero32(x^wordAmpersand32))) {
			goto slow
		}
		i += 4
	}
	if e.escapeHTML {
		for ; i < len(s); i++ {
			if escapeTableHTML[s[i]] {
				goto slow
			}
		}
	} else {
		for ; i < len(s); i++ {
			if escapeTablePlain[s[i]] {
				goto slow
			}
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
		case '\b':
			e.buf = append(e.buf, '\\', 'b')
		case '\f':
			e.buf = append(e.buf, '\\', 'f')
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

// dec2 packs two ASCII decimal digits per entry. | dec2 每项打包两个 ASCII 十进制数字。
var dec2 [100]uint16

func init() {
	for i := 0; i < 100; i++ {
		dec2[i] = uint16('0'+i/10) | uint16('0'+i%10)<<8
	}
}

// appendIntFast appends a decimal integer, using a table for small non-negative values.
// | appendIntFast 追加十进制整数；小的非负值走查表路径。
func appendIntFast(buf []byte, n int64) []byte {
	if n >= 0 && n < 10000 {
		switch {
		case n < 10:
			return append(buf, byte('0'+n))
		case n < 100:
			v := dec2[n]
			return append(buf, byte(v), byte(v>>8))
		case n < 1000:
			v := dec2[n%100]
			return append(buf, byte('0'+n/100), byte(v), byte(v>>8))
		default:
			v, w := dec2[n/100], dec2[n%100]
			return append(buf, byte(v), byte(v>>8), byte(w), byte(w>>8))
		}
	}
	return strconv.AppendInt(buf, n, 10)
}

// appendUintFast appends a decimal unsigned integer with the same small-value fast path.
// | appendUintFast 追加无符号十进制整数，小值走同一快速路径。
func appendUintFast(buf []byte, n uint64) []byte {
	if n < 10000 {
		return appendIntFast(buf, int64(n))
	}
	return strconv.AppendUint(buf, n, 10)
}
