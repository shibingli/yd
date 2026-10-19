//go:build amd64 && !jsonx_std

package jsonx

import (
	"encoding"
	"reflect"
	"strconv"
	"strings"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/core/errx"
)

func buildKindDecoder(t reflect.Type) decodeFunc {
	switch t.Kind() {
	case reflect.Bool:
		return func(d *decoder, v reflect.Value) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			x, err := d.parseBool()
			if err != nil {
				return err
			}
			v.SetBool(x)
			return nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		bits := t.Bits()
		return func(d *decoder, v reflect.Value) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			if d.data[d.pos] == '"' {
				return typeErr("cannot unmarshal string into %s | 无法把字符串解析进 %s", t, t)
			}
			b, err := d.parseNumberBytes()
			if err != nil {
				return err
			}
			s := bytesString(b)
			if strings.ContainsAny(s, ".eE") {
				return typeErr("cannot unmarshal number %s into %s | 无法把数字 %s 解析进 %s", s, t, s, t)
			}
			n, err := strconv.ParseInt(s, 10, bits)
			if err != nil {
				return typeErr("number %s overflows %s | 数字 %s 超出 %s", s, t, s, t)
			}
			v.SetInt(n)
			return nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		bits := t.Bits()
		return func(d *decoder, v reflect.Value) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			if d.data[d.pos] == '"' {
				return typeErr("cannot unmarshal string into %s | 无法把字符串解析进 %s", t, t)
			}
			b, err := d.parseNumberBytes()
			if err != nil {
				return err
			}
			s := bytesString(b)
			if strings.ContainsAny(s, ".eE-") {
				return typeErr("cannot unmarshal number %s into %s | 无法把数字 %s 解析进 %s", s, t, s, t)
			}
			n, err := strconv.ParseUint(s, 10, bits)
			if err != nil {
				return typeErr("number %s overflows %s | 数字 %s 超出 %s", s, t, s, t)
			}
			v.SetUint(n)
			return nil
		}
	case reflect.Float32, reflect.Float64:
		bits := t.Bits()
		return func(d *decoder, v reflect.Value) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			b, err := d.parseNumberBytes()
			if err != nil {
				return err
			}
			s := bytesString(b)
			n, err := strconv.ParseFloat(s, bits)
			if err != nil {
				return typeErr("number %s overflows %s | 数字 %s 超出 %s", s, t, s, t)
			}
			v.SetFloat(n)
			return nil
		}
	case reflect.String:
		return func(d *decoder, v reflect.Value) error {
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
			v.SetString(s)
			return nil
		}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return func(d *decoder, v reflect.Value) error {
				if d.data[d.pos] == 'n' {
					if err := d.literal("null"); err != nil {
						return err
					}
					v.SetZero()
					return nil
				}
				if d.data[d.pos] != '"' {
					return typeErr("cannot unmarshal non-string into %s | 无法把非字符串解析进 %s", t, t)
				}
				s, err := d.parseString()
				if err != nil {
					return err
				}
				out, err := b64x.StdEncoding.DecodeString(s)
				if err != nil {
					return errx.Wrap(err, ErrTypeMismatch)
				}
				v.SetBytes(out)
				return nil
			}
		}
		return buildSliceDecoder(t)
	case reflect.Array:
		return buildArrayDecoder(t)
	case reflect.Map:
		return buildMapDecoder(t)
	case reflect.Struct:
		return buildStructDecoder(t)
	default:
		return func(d *decoder, v reflect.Value) error {
			return typeErr("cannot unmarshal into %s | 无法解析进 %s", t, t)
		}
	}
}

func buildSliceDecoder(t reflect.Type) decodeFunc {
	elem := lazyDecoder(t.Elem())
	return func(d *decoder, v reflect.Value) error {
		if d.data[d.pos] == 'n' {
			if err := d.literal("null"); err != nil {
				return err
			}
			v.SetZero()
			return nil
		}
		if err := d.enter(); err != nil {
			return err
		}
		defer d.leave()
		if d.data[d.pos] != '[' {
			return typeErr("cannot unmarshal non-array into %s | 无法把非数组解析进 %s", t, t)
		}
		enc := elem()
		n := -1
		if d.skipLen {
			d.skipLen = false
			n = v.Len()
		}
		d.pos++
		d.skipSpace()
		if d.pos < len(d.data) && d.data[d.pos] == ']' {
			d.pos++
			if v.IsNil() {
				v.Set(reflect.MakeSlice(t, 0, 0))
			} else {
				v.SetLen(0)
			}
			return nil
		}
		if n >= 0 {
			if v.IsNil() || v.Cap() < n {
				v.Set(reflect.MakeSlice(t, n, n))
			} else {
				v.SetLen(n)
			}
		} else if v.IsNil() {
			v.Set(reflect.MakeSlice(t, 0, 4))
		} else {
			v.SetLen(0)
		}
		i := 0
		for {
			if i >= v.Len() {
				v.Grow(1)
				v.SetLen(i + 1)
			}
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of array | 数组意外结束")
			}
			if err := enc(d, v.Index(i)); err != nil {
				return err
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
					if i < v.Len() {
						v.SetLen(i)
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
				if i < v.Len() {
					v.SetLen(i)
				}
				return nil
			default:
				return syntaxErr("expected ',' or ']' in array | 数组中期望 ',' 或 ']'")
			}
		}
	}
}

func buildArrayDecoder(t reflect.Type) decodeFunc {
	elem := lazyDecoder(t.Elem())
	return func(d *decoder, v reflect.Value) error {
		if d.data[d.pos] == 'n' {
			return d.literal("null")
		}
		if err := d.enter(); err != nil {
			return err
		}
		defer d.leave()
		if d.data[d.pos] != '[' {
			return typeErr("cannot unmarshal non-array into %s | 无法把非数组解析进 %s", t, t)
		}
		enc := elem()
		d.pos++
		d.skipSpace()
		if d.pos < len(d.data) && d.data[d.pos] == ']' {
			d.pos++
			return nil
		}
		i := 0
		for {
			d.skipSpace()
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of array | 数组意外结束")
			}
			if i < v.Len() {
				if err := enc(d, v.Index(i)); err != nil {
					return err
				}
			} else if err := d.skipValue(); err != nil {
				return err
			}
			i++
			d.skipSpace()
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of array | 数组意外结束")
			}
			switch d.data[d.pos] {
			case ',':
				d.pos++
			case ']':
				d.pos++
				return nil
			default:
				return syntaxErr("expected ',' or ']' in array | 数组中期望 ',' 或 ']'")
			}
		}
	}
}

// mapKeySetter returns a function that converts one JSON object key to a map key, mirroring
// encoding/json: string keys (honouring encoding.TextUnmarshaler) and every integer kind.
// | mapKeySetter 返回把 JSON 对象键转换为 map 键的函数，语义对齐 encoding/json：字符串键
// （支持 encoding.TextUnmarshaler）与全部整数种类。
func mapKeySetter(keyType reflect.Type) (func(string, reflect.Value) error, bool) {
	textUnmarshaler := reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	switch {
	case keyType.Implements(textUnmarshaler):
		return func(key string, v reflect.Value) error {
			if err := v.Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(key)); err != nil {
				return typeErr("cannot unmarshal object key %[1]q into %[2]s: %[3]v | 无法把对象键 %[1]q 解析进 %[2]s：%[3]v", key, keyType, err)
			}
			return nil
		}, true
	case reflect.PointerTo(keyType).Implements(textUnmarshaler):
		return func(key string, v reflect.Value) error {
			ptr := reflect.New(keyType)
			if err := ptr.Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(key)); err != nil {
				return typeErr("cannot unmarshal object key %[1]q into %[2]s: %[3]v | 无法把对象键 %[1]q 解析进 %[2]s：%[3]v", key, keyType, err)
			}
			v.Set(ptr.Elem())
			return nil
		}, true
	}
	switch keyType.Kind() {
	case reflect.String:
		return func(key string, v reflect.Value) error { v.SetString(key); return nil }, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		bits := keyType.Bits()
		return func(key string, v reflect.Value) error {
			n, err := strconv.ParseInt(key, 10, bits)
			if err != nil {
				return typeErr("cannot unmarshal object key %q into %s | 无法把对象键 %q 解析进 %s", key, keyType, key, keyType)
			}
			v.SetInt(n)
			return nil
		}, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		bits := keyType.Bits()
		return func(key string, v reflect.Value) error {
			n, err := strconv.ParseUint(key, 10, bits)
			if err != nil {
				return typeErr("cannot unmarshal object key %q into %s | 无法把对象键 %q 解析进 %s", key, keyType, key, keyType)
			}
			v.SetUint(n)
			return nil
		}, true
	}
	return nil, false
}

func buildMapDecoder(t reflect.Type) decodeFunc {
	setKey, ok := mapKeySetter(t.Key())
	if !ok {
		return func(d *decoder, v reflect.Value) error {
			return typeErr("unsupported map key type %s | 不支持的 map 键类型 %s", t.Key(), t.Key())
		}
	}
	elem := lazyDecoder(t.Elem())
	keyType := t.Key()
	return func(d *decoder, v reflect.Value) error {
		if d.data[d.pos] == 'n' {
			if err := d.literal("null"); err != nil {
				return err
			}
			v.SetZero()
			return nil
		}
		if err := d.enter(); err != nil {
			return err
		}
		defer d.leave()
		if d.data[d.pos] != '{' {
			return typeErr("cannot unmarshal non-object into %s | 无法把非对象解析进 %s", t, t)
		}
		enc := elem()
		if v.IsNil() {
			v.Set(reflect.MakeMap(t))
		}
		d.pos++
		d.skipSpace()
		if d.pos < len(d.data) && d.data[d.pos] == '}' {
			d.pos++
			return nil
		}
		for {
			d.skipSpace()
			if d.pos >= len(d.data) || d.data[d.pos] != '"' {
				return syntaxErr("expected object key | 期望对象键")
			}
			key, err := d.parseString()
			if err != nil {
				return err
			}
			d.skipSpace()
			if d.pos >= len(d.data) || d.data[d.pos] != ':' {
				return syntaxErr("expected ':' after object key | 对象键之后期望 ':'")
			}
			d.pos++
			elemValue := reflect.New(t.Elem()).Elem()
			d.skipSpace()
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of object | 对象意外结束")
			}
			if err := enc(d, elemValue); err != nil {
				return err
			}
			mapKey := reflect.New(keyType).Elem()
			if err := setKey(key, mapKey); err != nil {
				return err
			}
			v.SetMapIndex(mapKey, elemValue)
			d.skipSpace()
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of object | 对象意外结束")
			}
			switch d.data[d.pos] {
			case ',':
				d.pos++
			case '}':
				d.pos++
				return nil
			default:
				return syntaxErr("expected ',' or '}' in object | 对象中期望 ',' 或 '}'")
			}
		}
	}
}

// fastFieldDec parses a JSON value directly into a field's memory. | fastFieldDec 直接把 JSON 值
// 解析进字段内存。
