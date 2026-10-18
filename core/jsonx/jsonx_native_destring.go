//go:build jsonx_native

package jsonx

import (
	"encoding"
	stdjson "encoding/json"
	"reflect"
	"strconv"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/core/errx"
)

// quotedUseErr reports a misuse of the ,string option, matching the standard library's wording
// class. | quotedUseErr 报告 ,string 选项的使用错误，措辞类别与标准库一致。
func quotedUseErr(t reflect.Type) error {
	return typeErr("jsonx: invalid use of ,string struct tag, trying to unmarshal into %s | jsonx：,string 结构体标签用法非法，无法解析进 %s", t, t)
}

// decodeQuotedField decodes one struct field carrying the ,string option. The outer JSON value
// must be null or a string; the decoded string content is then parsed as a JSON literal and stored
// with encoding/json's literalStore semantics. | decodeQuotedField 解码带 ,string 选项的结构体字段。
// 外层 JSON 值必须是 null 或字符串；字符串解码后的内容再作为 JSON 字面量解析，并按
// encoding/json 的 literalStore 语义写入。
func (d *decoder) decodeQuotedField(fv reflect.Value) error {
	d.skipSpace()
	if d.pos >= len(d.data) {
		return syntaxErr("unexpected end of JSON input | JSON 输入意外结束")
	}
	switch d.data[d.pos] {
	case 'n':
		if err := d.literal("null"); err != nil {
			return err
		}
		return d.storeNullField(fv)
	case '"':
		rawStart := d.pos
		s, err := d.parseString()
		if err != nil {
			return err
		}
		if err := d.storeQuotedLiteral([]byte(s), fv); err != nil {
			return err
		}
		// The pointer wrapper still applies the legacy post-step: only a raw JSON string that is
		// exactly "null" (no escapes) resets the pointer after the element no-op. | 指针包装仍执行
		// 旧版收尾：只有原始 JSON 字符串恰好是 "null"（无转义）时，才在元素空操作之后把指针置零。
		if fv.Kind() == reflect.Pointer && string(d.data[rawStart:d.pos]) == `"null"` {
			fv.SetZero()
		}
		return nil
	default:
		// Numbers, booleans, arrays and objects are unquoted values; encoding/json consumes them
		// and records an error. | 数字、布尔、数组与对象都是未加引号的值；encoding/json 会消费它们
		// 并记录错误。
		return quotedUseErr(fv.Type())
	}
}

// storeNullField mirrors literalStore(nullLiteral, fv, false): a settable pointer is set to nil
// without consulting Unmarshaler, a non-pointer type with a custom Unmarshaler receives the
// literal "null", and interfaces/maps/slices are zeroed while other kinds keep their value.
// | storeNullField 对应 literalStore(nullLiteral, fv, false)：可设置的指针直接置 nil 且不调用
// Unmarshaler；带自定义 Unmarshaler 的非指针类型收到字面量 "null"；接口、map 与切片置零，其余
// kind 保留原值。
func (d *decoder) storeNullField(fv reflect.Value) error {
	if fv.Kind() == reflect.Pointer && fv.CanSet() {
		fv.SetZero()
		return nil
	}
	if fv.Kind() != reflect.Pointer && fv.Type().Name() != "" && fv.CanAddr() {
		if p := fv.Addr(); p.CanInterface() {
			if u, ok := p.Interface().(stdjson.Unmarshaler); ok {
				return u.UnmarshalJSON([]byte("null"))
			}
		}
	}
	zeroOnNull(fv)
	return nil
}

// zeroOnNull mirrors the plain null branch of literalStore. | zeroOnNull 对应 literalStore 的
// 普通 null 分支。
func zeroOnNull(fv reflect.Value) {
	for fv.Kind() == reflect.Pointer {
		if fv.CanSet() {
			fv.SetZero()
			return
		}
		if fv.IsNil() {
			return
		}
		fv = fv.Elem()
	}
	switch fv.Kind() {
	case reflect.Interface, reflect.Map, reflect.Slice:
		if fv.CanSet() {
			fv.SetZero()
		}
	}
}

// storeQuotedLiteral mirrors literalStore(item, fv, true): item is the text inside the outer JSON
// string, and pointers plus Unmarshaler / TextUnmarshaler are resolved exactly like the standard
// library's indirect. | storeQuotedLiteral 对应 literalStore(item, fv, true)：item 是外层 JSON 字符串
// 内部的文本，指针与 Unmarshaler / TextUnmarshaler 的解析与标准库的 indirect 完全一致。
func (d *decoder) storeQuotedLiteral(item []byte, fv reflect.Value) error {
	if len(item) == 0 {
		return quotedUseErr(fv.Type())
	}
	// json.Number has its own arshaler: with the ,string option the field must be a JSON string
	// whose content is a valid JSON number, with no quoted-null or double-quoted forms.
	// | json.Number 有自己的处理器：带 ,string 时字段必须是内容为合法 JSON 数字的 JSON 字符串，
	// 不接受引号包裹的 null 或二次引号形式。
	if ft := fv.Type(); ft == numberType || (ft.Kind() == reflect.Pointer && ft.Elem() == numberType) {
		target := fv
		if target.Kind() == reflect.Pointer {
			if target.IsNil() {
				if !target.CanSet() {
					return quotedUseErr(fv.Type())
				}
				target.Set(reflect.New(numberType))
			}
			target = target.Elem()
		}
		s := string(item)
		if !isValidNumber(s) {
			return typeErr("invalid number literal %q | 非法数字字面量 %q", s, s)
		}
		target.SetString(s)
		return nil
	}
	// A quoted null is a legacy no-op for the element value; pointer resetting is driven by the
	// raw token in decodeQuotedField. json.Number rejects it above. | 带引号的 null 对元素值是
	// 旧版空操作；指针置零由 decodeQuotedField 依据原始 token 处理。json.Number 已在上方拒绝。
	if string(item) == "null" {
		return nil
	}
	v0 := fv
	haveAddr := false
	if fv.Kind() != reflect.Pointer && fv.Type().Name() != "" && fv.CanAddr() {
		haveAddr = true
		fv = fv.Addr()
	}
	for {
		if fv.Kind() == reflect.Interface && !fv.IsNil() {
			e := fv.Elem()
			if e.Kind() == reflect.Pointer && !e.IsNil() {
				haveAddr = false
				fv = e
				continue
			}
		}
		if fv.Kind() != reflect.Pointer {
			break
		}
		if fv.Elem().Kind() == reflect.Interface && fv.Elem().Elem() == fv {
			fv = fv.Elem()
			break
		}
		if fv.IsNil() {
			if !fv.CanSet() {
				return quotedUseErr(v0.Type())
			}
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		if fv.Type().NumMethod() > 0 && fv.CanInterface() {
			if u, ok := fv.Interface().(stdjson.Unmarshaler); ok {
				return u.UnmarshalJSON(item)
			}
			if u, ok := fv.Interface().(encoding.TextUnmarshaler); ok {
				return callQuotedText(item, u, v0.Type())
			}
		}
		if haveAddr {
			fv = v0
			haveAddr = false
		} else {
			fv = fv.Elem()
		}
	}
	return d.storeQuotedValue(item, fv)
}

// callQuotedText requires a complete JSON string literal and forwards its unquoted bytes to the
// TextUnmarshaler, matching literalStore's text branch. | callQuotedText 要求完整 JSON 字符串字面量，
// 并把去引号后的字节转交给 TextUnmarshaler，与 literalStore 的文本分支一致。
func callQuotedText(item []byte, u encoding.TextUnmarshaler, t reflect.Type) error {
	if item[0] != '"' {
		return quotedUseErr(t)
	}
	s, ok := unquoteQuoted(item)
	if !ok {
		return quotedUseErr(t)
	}
	return u.UnmarshalText(s)
}

// unquoteQuoted decodes item as one complete JSON string literal. | unquoteQuoted 把 item 解码为
// 一个完整的 JSON 字符串字面量。
func unquoteQuoted(item []byte) ([]byte, bool) {
	sub := &decoder{data: item}
	s, err := sub.parseString()
	if err != nil || sub.pos != len(item) {
		return nil, false
	}
	return []byte(s), true
}

// storeQuotedValue is the kind switch of literalStore for the already-unwrapped value v.
// | storeQuotedValue 是 literalStore 针对已解包值 v 的 kind 分支。
func (d *decoder) storeQuotedValue(item []byte, v reflect.Value) error {
	switch c := item[0]; c {
	case 't', 'f':
		if s := string(item); s != "true" && s != "false" {
			return quotedUseErr(v.Type())
		}
		value := c == 't'
		switch v.Kind() {
		case reflect.Bool:
			v.SetBool(value)
			return nil
		case reflect.Interface:
			if v.NumMethod() == 0 {
				v.Set(reflect.ValueOf(value))
				return nil
			}
		}
		return quotedUseErr(v.Type())
	case '"':
		s, ok := unquoteQuoted(item)
		if !ok {
			return quotedUseErr(v.Type())
		}
		switch v.Kind() {
		case reflect.Slice:
			if v.Type().Elem().Kind() != reflect.Uint8 {
				return quotedUseErr(v.Type())
			}
			out, err := b64x.StdEncoding.DecodeString(string(s))
			if err != nil {
				return errx.Wrap(err, ErrTypeMismatch)
			}
			v.SetBytes(out)
			return nil
		case reflect.String:
			v.SetString(string(s))
			return nil
		case reflect.Interface:
			if v.NumMethod() == 0 {
				v.Set(reflect.ValueOf(string(s)))
				return nil
			}
		}
		return quotedUseErr(v.Type())
	default:
		// Legacy ,string numeric parsing has no leading-character gate: integers use
		// strconv.ParseInt/ParseUint (which accept a sign) and floats use strconv.ParseFloat
		// (which also accepts NaN/Inf). Anything unparsable is an error. | 旧版 ,string 数字解析
		// 没有首字符限制：整数用 ParseInt/ParseUint（接受符号），浮点用 ParseFloat（还接受
		// NaN/Inf）；无法解析即为错误。
		s := string(item)
		switch v.Kind() {
		case reflect.Interface:
			if v.NumMethod() != 0 {
				return quotedUseErr(v.Type())
			}
			if d.useNumber {
				v.Set(reflect.ValueOf(stdjson.Number(s)))
				return nil
			}
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return typeErr("number %s overflows float64 | 数字 %s 超出 float64", s, s)
			}
			v.Set(reflect.ValueOf(f))
			return nil
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, err := strconv.ParseInt(s, 10, v.Type().Bits())
			if err != nil {
				return typeErr("number %s overflows %s | 数字 %s 超出 %s", s, v.Type(), s, v.Type())
			}
			v.SetInt(n)
			return nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			n, err := strconv.ParseUint(s, 10, v.Type().Bits())
			if err != nil {
				return typeErr("number %s overflows %s | 数字 %s 超出 %s", s, v.Type(), s, v.Type())
			}
			v.SetUint(n)
			return nil
		case reflect.Float32, reflect.Float64:
			// Legacy ,string semantics parse the inner text with Go's float syntax and do not
			// apply an extra overflow check, so NaN and Inf are accepted. | 旧版 ,string 语义按
			// Go 浮点语法解析内层文本，不再做额外溢出检查，因此 NaN 与 Inf 会被接受。
			n, err := strconv.ParseFloat(s, v.Type().Bits())
			if err != nil {
				return typeErr("number %s overflows %s | 数字 %s 超出 %s", s, v.Type(), s, v.Type())
			}
			v.SetFloat(n)
			return nil
		}
		return quotedUseErr(v.Type())
	}
}
