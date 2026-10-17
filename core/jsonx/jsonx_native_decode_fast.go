//go:build jsonx_native

package jsonx

import (
	"bytes"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

type fastFieldDec func(*decoder, unsafe.Pointer) error

// buildFastFieldDecoder returns a direct-memory decoder for common field types. | buildFastFieldDecoder
// 为常见字段类型返回直接写内存的解码器。
func buildFastFieldDecoder(t reflect.Type) fastFieldDec {
	switch t.Kind() {
	case reflect.String:
		return func(d *decoder, p unsafe.Pointer) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			if d.data[d.pos] != '"' {
				return typeErr("cannot unmarshal non-string into %s | 无法把非字符串解析进 %s", t, t)
			}
			s, err := d.parseString()
			if err != nil {
				return err
			}
			*(*string)(p) = s
			return nil
		}
	case reflect.Bool:
		return func(d *decoder, p unsafe.Pointer) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			x, err := d.parseBool()
			if err != nil {
				return err
			}
			*(*bool)(p) = x
			return nil
		}
	case reflect.Int:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseIntField(t)
			if err != nil {
				return err
			}
			*(*int)(p) = int(n)
			return nil
		}
	case reflect.Int8:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseIntField(t)
			if err != nil {
				return err
			}
			*(*int8)(p) = int8(n)
			return nil
		}
	case reflect.Int16:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseIntField(t)
			if err != nil {
				return err
			}
			*(*int16)(p) = int16(n)
			return nil
		}
	case reflect.Int32:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseIntField(t)
			if err != nil {
				return err
			}
			*(*int32)(p) = int32(n)
			return nil
		}
	case reflect.Int64:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseIntField(t)
			if err != nil {
				return err
			}
			*(*int64)(p) = n
			return nil
		}
	case reflect.Uint:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uint)(p) = uint(n)
			return nil
		}
	case reflect.Uint8:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uint8)(p) = uint8(n)
			return nil
		}
	case reflect.Uint16:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uint16)(p) = uint16(n)
			return nil
		}
	case reflect.Uint32:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uint32)(p) = uint32(n)
			return nil
		}
	case reflect.Uint64:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uint64)(p) = n
			return nil
		}
	case reflect.Uintptr:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uintptr)(p) = uintptr(n)
			return nil
		}
	case reflect.Float32:
		return func(d *decoder, p unsafe.Pointer) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			b, err := d.parseNumberBytes()
			if err != nil {
				return err
			}
			n, err := strconv.ParseFloat(bytesString(b), 32)
			if err != nil {
				return typeErr("number %s overflows %s | 数字 %s 超出 %s", bytesString(b), t, bytesString(b), t)
			}
			*(*float32)(p) = float32(n)
			return nil
		}
	case reflect.Float64:
		return func(d *decoder, p unsafe.Pointer) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			b, err := d.parseNumberBytes()
			if err != nil {
				return err
			}
			n, err := strconv.ParseFloat(bytesString(b), 64)
			if err != nil {
				return typeErr("number %s overflows %s | 数字 %s 超出 %s", bytesString(b), t, bytesString(b), t)
			}
			*(*float64)(p) = n
			return nil
		}
	}
	if t == timeType {
		return func(d *decoder, p unsafe.Pointer) error {
			raw, err := d.captureValueView()
			if err != nil {
				return err
			}
			if len(raw) == 4 && raw[0] == 'n' {
				return (*time.Time)(p).UnmarshalJSON(raw)
			}
			if int(d.timeLen) == len(raw) && len(raw) <= len(d.timeRaw) && bytes.Equal(d.timeRaw[:len(raw)], raw) {
				*(*time.Time)(p) = d.timeVal
				return nil
			}
			tt := (*time.Time)(p)
			if err := tt.UnmarshalJSON(raw); err != nil {
				return err
			}
			if len(raw) <= len(d.timeRaw) {
				copy(d.timeRaw[:], raw)
				d.timeLen = uint8(len(raw))
				d.timeVal = *tt
			}
			return nil
		}
	}
	if t == stringSliceType {
		return fastDecodeStringSlice
	}
	return nil
}

func (d *decoder) parseIntField(t reflect.Type) (int64, error) {
	if d.data[d.pos] == 'n' {
		return 0, d.literal("null")
	}
	if d.data[d.pos] == '"' {
		return 0, typeErr("cannot unmarshal string into %s | 无法把字符串解析进 %s", t, t)
	}
	b, err := d.parseNumberBytes()
	if err != nil {
		return 0, err
	}
	if len(b) > 0 && b[0] != '-' {
		bits := t.Bits()
		max := uint64(1)<<63 - 1
		if bits < 64 {
			max = uint64(1)<<(bits-1) - 1
		}
		var n uint64
		ok := true
		for _, c := range b {
			if c < '0' || c > '9' {
				ok = false
				break
			}
			digit := uint64(c - '0')
			if n > (max-digit)/10 {
				ok = false
				break
			}
			n = n*10 + digit
		}
		if ok {
			return int64(n), nil
		}
	}
	s := bytesString(b)
	if strings.ContainsAny(s, ".eE") {
		return 0, typeErr("cannot unmarshal number %s into %s | 无法把数字 %s 解析进 %s", s, t, s, t)
	}
	n, err := strconv.ParseInt(s, 10, t.Bits())
	if err != nil {
		return 0, typeErr("number %s overflows %s | 数字 %s 超出 %s", s, t, s, t)
	}
	return n, nil
}

func (d *decoder) parseUintField(t reflect.Type) (uint64, error) {
	if d.data[d.pos] == 'n' {
		return 0, d.literal("null")
	}
	if d.data[d.pos] == '"' {
		return 0, typeErr("cannot unmarshal string into %s | 无法把字符串解析进 %s", t, t)
	}
	b, err := d.parseNumberBytes()
	if err != nil {
		return 0, err
	}
	bits := t.Bits()
	max := ^uint64(0)
	if bits < 64 {
		max = uint64(1)<<bits - 1
	}
	var n uint64
	ok := true
	for _, c := range b {
		if c < '0' || c > '9' {
			ok = false
			break
		}
		digit := uint64(c - '0')
		if n > (max-digit)/10 {
			ok = false
			break
		}
		n = n*10 + digit
	}
	if ok {
		return n, nil
	}
	s := bytesString(b)
	if strings.ContainsAny(s, ".eE-") {
		return 0, typeErr("cannot unmarshal number %s into %s | 无法把数字 %s 解析进 %s", s, t, s, t)
	}
	parsed, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0, typeErr("number %s overflows %s | 数字 %s 超出 %s", s, t, s, t)
	}
	return parsed, nil
}

func fastDecodeStringSlice(d *decoder, p unsafe.Pointer) error {
	sp := (*[]string)(p)
	if d.data[d.pos] == 'n' {
		if err := d.literal("null"); err != nil {
			return err
		}
		*sp = nil
		return nil
	}
	if d.data[d.pos] != '[' {
		return typeErr("cannot unmarshal non-array into []string | 无法把非数组解析进 []string")
	}
	d.pos++
	d.skipSpace()
	if d.pos < len(d.data) && d.data[d.pos] == ']' {
		d.pos++
		*sp = []string{}
		return nil
	}
	*sp = make([]string, 0, 4)
	i := 0
	for {
		if i >= len(*sp) {
			*sp = append(*sp, "")
		}
		if d.pos >= len(d.data) {
			return syntaxErr("unexpected end of array | 数组意外结束")
		}
		switch d.data[d.pos] {
		case '"':
			s, err := d.parseString()
			if err != nil {
				return err
			}
			(*sp)[i] = s
		case 'n':
			if err := d.literal("null"); err != nil {
				return err
			}
			(*sp)[i] = ""
		default:
			return typeErr("cannot unmarshal non-string into string | 无法把非字符串解析进字符串")
		}
		i++
		if d.pos < len(d.data) {
			switch d.data[d.pos] {
			case ',':
				d.pos++
				d.skipSpace()
				continue
			case ']':
				d.pos++
				if i < len(*sp) {
					*sp = (*sp)[:i]
				}
				return nil
			}
		}
		d.skipSpace()
		if d.pos >= len(d.data) {
			return syntaxErr("unexpected end of array | 数组意外结束")
		}
		switch d.data[d.pos] {
		case ',':
			d.pos++
			d.skipSpace()
		case ']':
			d.pos++
			if i < len(*sp) {
				*sp = (*sp)[:i]
			}
			return nil
		default:
			return syntaxErr("expected ',' or ']' in array | 数组中期望 ',' 或 ']'")
		}
	}
}
