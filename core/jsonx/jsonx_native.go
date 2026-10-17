//go:build jsonx_native

package jsonx

import (
	"bytes"
	"encoding"
	stdjson "encoding/json"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/core/errx"
)

// Backend returns the active backend name for this build. | Backend 返回当前构建使用的后端名称。
func Backend() string { return "yd-native" }

var (
	numberType          = reflect.TypeOf(stdjson.Number(""))
	rawMessageType      = reflect.TypeOf(stdjson.RawMessage(nil))
	marshalerType       = reflect.TypeOf((*stdjson.Marshaler)(nil)).Elem()
	unmarshalerType     = reflect.TypeOf((*stdjson.Unmarshaler)(nil)).Elem()
	textMarshalerType   = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// Marshal returns the JSON encoding of v. | Marshal 返回 v 的 JSON 编码。
func Marshal(v any) ([]byte, error) {
	e := &encoder{escapeHTML: true}
	if err := e.value(reflect.ValueOf(v)); err != nil {
		return nil, err
	}
	return e.buf, nil
}

// MarshalIndent is like Marshal but indents the output. | MarshalIndent 与 Marshal 类似，但缩进输出。
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	b, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := indentJSON(&out, b, prefix, indent); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Valid reports whether data is a single valid JSON value. | Valid 报告 data 是否是单个合法 JSON 值。
func Valid(data []byte) bool {
	d := &decoder{data: data}
	d.skipSpace()
	if err := d.skipValue(); err != nil {
		return false
	}
	d.skipSpace()
	return d.pos == len(data)
}

// Marshaler and Unmarshaler interfaces are the standard library's; this backend uses them directly. | Marshaler 与
// Unmarshaler 接口直接采用标准库定义，保证与 encoding/json 的扩展点一致。

type encoder struct {
	buf        []byte
	escapeHTML bool
}

func syntaxErr(format string, args ...any) error {
	return errx.Detail(ErrSyntax, format, args...)
}

func typeErr(format string, args ...any) error {
	return errx.Detail(ErrTypeMismatch, format, args...)
}

var errUnsupported = errx.New("jsonx: unsupported value | jsonx：不支持的值")

func (e *encoder) value(v reflect.Value) error {
	if !v.IsValid() {
		e.buf = append(e.buf, "null"...)
		return nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			e.buf = append(e.buf, "null"...)
			return nil
		}
		return e.value(v.Elem())
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			e.buf = append(e.buf, "null"...)
			return nil
		}
		if cachedTraits(v.Type()).marshaler {
			return e.marshaler(v.Interface().(stdjson.Marshaler))
		}
		return e.value(v.Elem())
	}
	if v.Type() == numberType {
		s := v.String()
		if !isValidNumber(s) {
			return errx.Detail(errUnsupported, "invalid number %q | 非法数字 %q", s, s)
		}
		e.buf = append(e.buf, s...)
		return nil
	}
	tr := cachedTraits(v.Type())
	if tr.marshaler {
		return e.marshaler(v.Interface().(stdjson.Marshaler))
	}
	if v.CanAddr() && cachedTraits(v.Addr().Type()).marshaler {
		return e.marshaler(v.Addr().Interface().(stdjson.Marshaler))
	}
	if tr.textMarshaler {
		text, err := v.Interface().(encoding.TextMarshaler).MarshalText()
		if err != nil {
			return err
		}
		e.str(string(text))
		return nil
	}
	switch v.Kind() {
	case reflect.Bool:
		e.buf = strconv.AppendBool(e.buf, v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.buf = strconv.AppendInt(e.buf, v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		e.buf = strconv.AppendUint(e.buf, v.Uint(), 10)
	case reflect.Float32:
		return e.float(v.Float(), 32)
	case reflect.Float64:
		return e.float(v.Float(), 64)
	case reflect.String:
		e.str(v.String())
	case reflect.Slice:
		if v.IsNil() {
			e.buf = append(e.buf, "null"...)
			return nil
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			e.str(b64x.StdEncoding.EncodeToString(v.Bytes()))
			return nil
		}
		return e.array(v)
	case reflect.Array:
		return e.array(v)
	case reflect.Map:
		return e.object(v)
	case reflect.Struct:
		return e.structValue(v)
	default:
		return errx.Detail(errUnsupported, "unsupported type %s | 不支持的类型 %s", v.Type(), v.Type())
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

func (e *encoder) str(s string) {
	for i := 0; i < len(s); i++ {
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

func (e *encoder) array(v reflect.Value) error {
	e.buf = append(e.buf, '[')
	for i := 0; i < v.Len(); i++ {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		if err := e.value(v.Index(i)); err != nil {
			return err
		}
	}
	e.buf = append(e.buf, ']')
	return nil
}

func (e *encoder) object(v reflect.Value) error {
	type kv struct {
		key string
		val reflect.Value
	}
	entries := make([]kv, 0, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		k := iter.Key()
		var name string
		if k.Kind() == reflect.String {
			name = k.String()
		} else if k.CanInterface() {
			if m, ok := k.Interface().(encoding.TextMarshaler); ok {
				b, err := m.MarshalText()
				if err != nil {
					return err
				}
				name = string(b)
			} else {
				return errx.Detail(errUnsupported, "unsupported map key type %s | 不支持的 map 键类型 %s", k.Type(), k.Type())
			}
		} else {
			return errx.Detail(errUnsupported, "unsupported map key | 不支持的 map 键")
		}
		entries = append(entries, kv{key: name, val: iter.Value()})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	e.buf = append(e.buf, '{')
	for i, entry := range entries {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		e.str(entry.key)
		e.buf = append(e.buf, ':')
		if err := e.value(entry.val); err != nil {
			return err
		}
	}
	e.buf = append(e.buf, '}')
	return nil
}

// fieldInfo is one exported struct field. | fieldInfo 是一个可导出的结构体字段。
type fieldInfo struct {
	name      string
	index     []int
	omitEmpty bool
	quoted    bool
}

type typeTraits struct {
	marshaler       bool
	textMarshaler   bool
	unmarshaler     bool
	textUnmarshaler bool
}

var traitCache sync.Map

func cachedTraits(t reflect.Type) typeTraits {
	if v, ok := traitCache.Load(t); ok {
		return v.(typeTraits)
	}
	tr := typeTraits{
		marshaler:       t.Implements(marshalerType),
		textMarshaler:   t.Implements(textMarshalerType),
		unmarshaler:     t.Implements(unmarshalerType),
		textUnmarshaler: t.Implements(textUnmarshalerType),
	}
	traitCache.Store(t, tr)
	return tr
}

type structInfo struct {
	fields []fieldInfo
	byName map[string]fieldInfo
	byFold map[string]fieldInfo
}

var fieldCache sync.Map

func cachedStruct(t reflect.Type) *structInfo {
	if v, ok := fieldCache.Load(t); ok {
		return v.(*structInfo)
	}
	fields := collectFields(t, nil)
	info := &structInfo{fields: fields, byName: make(map[string]fieldInfo, len(fields)), byFold: make(map[string]fieldInfo, len(fields))}
	for _, f := range fields {
		info.byName[f.name] = f
		info.byFold[strings.ToLower(f.name)] = f
	}
	fieldCache.Store(t, info)
	return info
}

func collectFields(t reflect.Type, index []int) []fieldInfo {
	var out []fieldInfo
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts := parseTag(tag)
		idx := append(append([]int(nil), index...), i)
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				out = append(out, collectFields(ft, idx)...)
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, fieldInfo{name: name, index: idx, omitEmpty: strings.Contains(opts, "omitempty"), quoted: strings.Contains(opts, "string")})
	}
	return out
}

func parseTag(tag string) (string, string) {
	if tag == "" {
		return "", ""
	}
	if i := strings.IndexByte(tag, ','); i >= 0 {
		return tag[:i], tag[i+1:]
	}
	return tag, ""
}

func (e *encoder) structValue(v reflect.Value) error {
	fields := cachedStruct(v.Type()).fields
	e.buf = append(e.buf, '{')
	first := true
	for _, f := range fields {
		fv, ok := fieldByIndex(v, f.index)
		if !ok {
			continue
		}
		if f.omitEmpty && isEmptyValue(fv) {
			continue
		}
		if !first {
			e.buf = append(e.buf, ',')
		}
		first = false
		e.str(f.name)
		e.buf = append(e.buf, ':')
		if f.quoted {
			if err := e.quotedValue(fv); err != nil {
				return err
			}
		} else if err := e.value(fv); err != nil {
			return err
		}
	}
	e.buf = append(e.buf, '}')
	return nil
}

func fieldByIndex(v reflect.Value, index []int) (reflect.Value, bool) {
	for _, i := range index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}, false
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct || i >= v.NumField() {
			return reflect.Value{}, false
		}
		v = v.Field(i)
	}
	return v, true
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
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
type decoder struct {
	data      []byte
	pos       int
	useNumber bool
}

// Unmarshal parses data and stores the result in the value pointed to by v. | Unmarshal 解析 data 并写入 v 指向的值。
func Unmarshal(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errx.Detail(ErrTypeMismatch, "jsonx: Unmarshal target must be a non-nil pointer | jsonx：Unmarshal 目标必须是非 nil 指针")
	}
	d := &decoder{data: data}
	d.skipSpace()
	if d.pos >= len(d.data) {
		return syntaxErr("unexpected end of JSON input | JSON 输入意外结束")
	}
	if err := d.value(rv.Elem()); err != nil {
		return err
	}
	d.skipSpace()
	if d.pos != len(d.data) {
		return syntaxErr("invalid character %q after top-level value | 顶层值之后的非法字符 %q", d.data[d.pos], d.data[d.pos])
	}
	return nil
}

func (d *decoder) skipSpace() {
	for d.pos < len(d.data) {
		switch d.data[d.pos] {
		case ' ', '\t', '\n', '\r':
			d.pos++
		default:
			return
		}
	}
}

func (d *decoder) value(v reflect.Value) error {
	d.skipSpace()
	if d.pos >= len(d.data) {
		return syntaxErr("unexpected end of JSON input | JSON 输入意外结束")
	}
	if v.Kind() == reflect.Pointer {
		if d.data[d.pos] == 'n' {
			if err := d.literal("null"); err != nil {
				return err
			}
			v.SetZero()
			return nil
		}
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		return d.value(v.Elem())
	}
	if v.Type() == rawMessageType {
		raw, err := d.captureValue()
		if err != nil {
			return err
		}
		v.SetBytes(raw)
		return nil
	}
	if v.CanAddr() && cachedTraits(v.Addr().Type()).unmarshaler {
		raw, err := d.captureValue()
		if err != nil {
			return err
		}
		return v.Addr().Interface().(stdjson.Unmarshaler).UnmarshalJSON(raw)
	}
	if v.Kind() == reflect.Interface && v.NumMethod() == 0 {
		x, err := d.valueAny()
		if err != nil {
			return err
		}
		if x == nil {
			v.SetZero()
		} else {
			v.Set(reflect.ValueOf(x))
		}
		return nil
	}
	if v.Type() == numberType {
		if d.data[d.pos] == '"' {
			s, err := d.parseString()
			if err != nil {
				return err
			}
			v.SetString(s)
			return nil
		}
		s, err := d.parseNumber()
		if err != nil {
			return err
		}
		v.SetString(s)
		return nil
	}
	if v.CanAddr() && cachedTraits(v.Addr().Type()).textUnmarshaler && d.data[d.pos] == '"' {
		s, err := d.parseString()
		if err != nil {
			return err
		}
		return v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s))
	}
	switch d.data[d.pos] {
	case 'n':
		if err := d.literal("null"); err != nil {
			return err
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			v.SetZero()
		}
		return nil
	case 't', 'f':
		x, err := d.parseBool()
		if err != nil {
			return err
		}
		if v.Kind() != reflect.Bool {
			return typeErr("cannot unmarshal bool into %s | 无法把布尔值解析进 %s", v.Type(), v.Type())
		}
		v.SetBool(x)
		return nil
	case '"':
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
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
		s, err := d.parseString()
		if err != nil {
			return err
		}
		if v.Kind() != reflect.String {
			return typeErr("cannot unmarshal string into %s | 无法把字符串解析进 %s", v.Type(), v.Type())
		}
		v.SetString(s)
		return nil
	case '{':
		return d.object(v)
	case '[':
		return d.array(v)
	default:
		return d.numberValue(v)
	}
}

func (d *decoder) object(v reflect.Value) error {
	switch v.Kind() {
	case reflect.Map:
		return d.mapValue(v)
	case reflect.Struct:
		return d.structValue(v)
	default:
		return typeErr("cannot unmarshal object into %s | 无法把对象解析进 %s", v.Type(), v.Type())
	}
}

func (d *decoder) structValue(v reflect.Value) error {
	if d.data[d.pos] != '{' {
		return typeErr("cannot unmarshal non-object into %s | 无法把非对象解析进 %s", v.Type(), v.Type())
	}
	d.pos++
	info := cachedStruct(v.Type())
	byName, byFold := info.byName, info.byFold
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
		f, ok := byName[key]
		if !ok {
			f, ok = byFold[strings.ToLower(key)]
		}
		if !ok {
			if err := d.skipValue(); err != nil {
				return err
			}
		} else {
			fv, ok := fieldByIndexAlloc(v, f.index)
			if !ok {
				if err := d.skipValue(); err != nil {
					return err
				}
			} else if err := d.value(fv); err != nil {
				return err
			}
		}
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

func fieldByIndexAlloc(v reflect.Value, index []int) (reflect.Value, bool) {
	for _, i := range index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct || i >= v.NumField() {
			return reflect.Value{}, false
		}
		v = v.Field(i)
	}
	return v, v.CanSet()
}

func (d *decoder) mapValue(v reflect.Value) error {
	if v.Kind() != reflect.Map {
		return typeErr("cannot unmarshal object into %s | 无法把对象解析进 %s", v.Type(), v.Type())
	}
	if v.Type().Key().Kind() != reflect.String {
		return typeErr("unsupported map key type %s | 不支持的 map 键类型 %s", v.Type().Key(), v.Type().Key())
	}
	if v.IsNil() {
		v.Set(reflect.MakeMap(v.Type()))
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
		elem := reflect.New(v.Type().Elem()).Elem()
		if err := d.value(elem); err != nil {
			return err
		}
		v.SetMapIndex(reflect.ValueOf(key).Convert(v.Type().Key()), elem)
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

func (d *decoder) array(v reflect.Value) error {
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return typeErr("cannot unmarshal array into %s | 无法把数组解析进 %s", v.Type(), v.Type())
	}
	d.pos++
	d.skipSpace()
	if d.pos < len(d.data) && d.data[d.pos] == ']' {
		d.pos++
		return nil
	}
	if v.Kind() == reflect.Slice && v.IsNil() {
		v.Set(reflect.MakeSlice(v.Type(), 0, 4))
	}
	i := 0
	for {
		if v.Kind() == reflect.Slice {
			if i >= v.Len() {
				v.Set(reflect.Append(v, reflect.Zero(v.Type().Elem())))
			}
			if err := d.value(v.Index(i)); err != nil {
				return err
			}
		} else {
			if i < v.Len() {
				if err := d.value(v.Index(i)); err != nil {
					return err
				}
			} else if err := d.skipValue(); err != nil {
				return err
			}
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

func (d *decoder) numberValue(v reflect.Value) error {
	s, err := d.parseNumber()
	if err != nil {
		return err
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if strings.ContainsAny(s, ".eE") {
			return typeErr("cannot unmarshal number %s into %s | 无法把数字 %s 解析进 %s", s, v.Type(), s, v.Type())
		}
		n, err := strconv.ParseInt(s, 10, v.Type().Bits())
		if err != nil {
			return typeErr("number %s overflows %s | 数字 %s 超出 %s", s, v.Type(), s, v.Type())
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if strings.ContainsAny(s, ".eE-") {
			return typeErr("cannot unmarshal number %s into %s | 无法把数字 %s 解析进 %s", s, v.Type(), s, v.Type())
		}
		n, err := strconv.ParseUint(s, 10, v.Type().Bits())
		if err != nil {
			return typeErr("number %s overflows %s | 数字 %s 超出 %s", s, v.Type(), s, v.Type())
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(s, v.Type().Bits())
		if err != nil {
			return typeErr("number %s overflows %s | 数字 %s 超出 %s", s, v.Type(), s, v.Type())
		}
		v.SetFloat(n)
	default:
		return typeErr("cannot unmarshal number into %s | 无法把数字解析进 %s", v.Type(), v.Type())
	}
	return nil
}

func (d *decoder) valueAny() (any, error) {
	d.skipSpace()
	if d.pos >= len(d.data) {
		return nil, syntaxErr("unexpected end of JSON input | JSON 输入意外结束")
	}
	switch d.data[d.pos] {
	case 'n':
		return nil, d.literal("null")
	case 't', 'f':
		return d.parseBool()
	case '"':
		return d.parseString()
	case '{':
		return d.anyObject()
	case '[':
		return d.anyArray()
	default:
		s, err := d.parseNumber()
		if err != nil {
			return nil, err
		}
		if d.useNumber {
			return stdjson.Number(s), nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, syntaxErr("invalid number %q | 非法数字 %q", s, s)
		}
		return f, nil
	}
}

func (d *decoder) anyObject() (any, error) {
	d.pos++
	out := make(map[string]any)
	d.skipSpace()
	if d.pos < len(d.data) && d.data[d.pos] == '}' {
		d.pos++
		return out, nil
	}
	for {
		d.skipSpace()
		if d.pos >= len(d.data) || d.data[d.pos] != '"' {
			return nil, syntaxErr("expected object key | 期望对象键")
		}
		key, err := d.parseString()
		if err != nil {
			return nil, err
		}
		d.skipSpace()
		if d.pos >= len(d.data) || d.data[d.pos] != ':' {
			return nil, syntaxErr("expected ':' after object key | 对象键之后期望 ':'")
		}
		d.pos++
		val, err := d.valueAny()
		if err != nil {
			return nil, err
		}
		out[key] = val
		d.skipSpace()
		if d.pos >= len(d.data) {
			return nil, syntaxErr("unexpected end of object | 对象意外结束")
		}
		switch d.data[d.pos] {
		case ',':
			d.pos++
		case '}':
			d.pos++
			return out, nil
		default:
			return nil, syntaxErr("expected ',' or '}' in object | 对象中期望 ',' 或 '}'")
		}
	}
}

func (d *decoder) anyArray() (any, error) {
	d.pos++
	out := make([]any, 0)
	d.skipSpace()
	if d.pos < len(d.data) && d.data[d.pos] == ']' {
		d.pos++
		return out, nil
	}
	for {
		val, err := d.valueAny()
		if err != nil {
			return nil, err
		}
		out = append(out, val)
		d.skipSpace()
		if d.pos >= len(d.data) {
			return nil, syntaxErr("unexpected end of array | 数组意外结束")
		}
		switch d.data[d.pos] {
		case ',':
			d.pos++
		case ']':
			d.pos++
			return out, nil
		default:
			return nil, syntaxErr("expected ',' or ']' in array | 数组中期望 ',' 或 ']'")
		}
	}
}

func (d *decoder) literal(s string) error {
	if len(d.data)-d.pos < len(s) || string(d.data[d.pos:d.pos+len(s)]) != s {
		return syntaxErr("invalid literal | 非法字面量")
	}
	d.pos += len(s)
	return nil
}

func (d *decoder) parseBool() (bool, error) {
	if d.pos < len(d.data) && d.data[d.pos] == 't' {
		if err := d.literal("true"); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := d.literal("false"); err != nil {
		return false, err
	}
	return false, nil
}

func (d *decoder) parseString() (string, error) {
	if d.pos >= len(d.data) || d.data[d.pos] != '"' {
		return "", syntaxErr("expected string | 期望字符串")
	}
	start := d.pos + 1
	for i := start; i < len(d.data); i++ {
		c := d.data[i]
		if c == '"' {
			d.pos = i + 1
			return string(d.data[start:i]), nil
		}
		if c == '\\' || c < 0x20 {
			break
		}
	}
	d.pos = start - 1
	d.pos++
	var b strings.Builder
	for d.pos < len(d.data) {
		c := d.data[d.pos]
		switch c {
		case '"':
			d.pos++
			return b.String(), nil
		case '\\':
			d.pos++
			if d.pos >= len(d.data) {
				return "", syntaxErr("unfinished escape sequence | 未完成的转义序列")
			}
			esc := d.data[d.pos]
			d.pos++
			switch esc {
			case '"', '\\', '/':
				b.WriteByte(esc)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				r, err := d.parseUnicodeEscape()
				if err != nil {
					return "", err
				}
				b.WriteRune(r)
			default:
				return "", syntaxErr("invalid escape character %q | 非法转义字符 %q", esc, esc)
			}
		default:
			if c < 0x20 {
				return "", syntaxErr("invalid control character in string | 字符串中包含非法控制字符")
			}
			r, size := utf8.DecodeRune(d.data[d.pos:])
			if r == utf8.RuneError && size == 1 {
				b.WriteRune('�')
				d.pos++
				continue
			}
			b.Write(d.data[d.pos : d.pos+size])
			d.pos += size
		}
	}
	return "", syntaxErr("unexpected end of string | 字符串意外结束")
}

func (d *decoder) parseUnicodeEscape() (rune, error) {
	if d.pos+4 > len(d.data) {
		return 0, syntaxErr("unfinished unicode escape | 未完成的 Unicode 转义")
	}
	v, err := strconv.ParseUint(string(d.data[d.pos:d.pos+4]), 16, 32)
	if err != nil {
		return 0, syntaxErr("invalid unicode escape | 非法 Unicode 转义")
	}
	d.pos += 4
	r := rune(v)
	if r >= 0xD800 && r < 0xDC00 {
		if d.pos+6 > len(d.data) || d.data[d.pos] != '\\' || d.data[d.pos+1] != 'u' {
			return '�', nil
		}
		d.pos += 2
		v2, err := strconv.ParseUint(string(d.data[d.pos:d.pos+4]), 16, 32)
		if err != nil {
			return 0, syntaxErr("invalid unicode escape | 非法 Unicode 转义")
		}
		d.pos += 4
		r2 := rune(v2)
		if r2 < 0xDC00 || r2 > 0xDFFF {
			return '�', nil
		}
		return (r-0xD800)<<10 + (r2 - 0xDC00) + 0x10000, nil
	}
	if r >= 0xDC00 && r <= 0xDFFF {
		return '�', nil
	}
	return r, nil
}

func (d *decoder) parseNumber() (string, error) {
	start := d.pos
	if d.pos < len(d.data) && d.data[d.pos] == '-' {
		d.pos++
	}
	digits := d.pos
	for d.pos < len(d.data) && d.data[d.pos] >= '0' && d.data[d.pos] <= '9' {
		d.pos++
	}
	if d.pos == digits {
		return "", syntaxErr("invalid number | 非法数字")
	}
	if d.data[digits] == '0' && d.pos-digits > 1 {
		return "", syntaxErr("invalid leading zero | 非法前导零")
	}
	if d.pos < len(d.data) && d.data[d.pos] == '.' {
		d.pos++
		frac := d.pos
		for d.pos < len(d.data) && d.data[d.pos] >= '0' && d.data[d.pos] <= '9' {
			d.pos++
		}
		if d.pos == frac {
			return "", syntaxErr("invalid number fraction | 非法数字小数部分")
		}
	}
	if d.pos < len(d.data) && (d.data[d.pos] == 'e' || d.data[d.pos] == 'E') {
		d.pos++
		if d.pos < len(d.data) && (d.data[d.pos] == '+' || d.data[d.pos] == '-') {
			d.pos++
		}
		exp := d.pos
		for d.pos < len(d.data) && d.data[d.pos] >= '0' && d.data[d.pos] <= '9' {
			d.pos++
		}
		if d.pos == exp {
			return "", syntaxErr("invalid number exponent | 非法数字指数")
		}
	}
	return string(d.data[start:d.pos]), nil
}

func (d *decoder) captureValue() ([]byte, error) {
	start := d.pos
	if err := d.skipValue(); err != nil {
		return nil, err
	}
	raw := make([]byte, d.pos-start)
	copy(raw, d.data[start:d.pos])
	return raw, nil
}

func (d *decoder) skipValue() error {
	d.skipSpace()
	if d.pos >= len(d.data) {
		return syntaxErr("unexpected end of JSON input | JSON 输入意外结束")
	}
	switch d.data[d.pos] {
	case 'n':
		return d.literal("null")
	case 't':
		return d.literal("true")
	case 'f':
		return d.literal("false")
	case '"':
		_, err := d.parseString()
		return err
	case '{':
		d.pos++
		d.skipSpace()
		if d.pos < len(d.data) && d.data[d.pos] == '}' {
			d.pos++
			return nil
		}
		for {
			d.skipSpace()
			if _, err := d.parseString(); err != nil {
				return err
			}
			d.skipSpace()
			if d.pos >= len(d.data) || d.data[d.pos] != ':' {
				return syntaxErr("expected ':' | 期望 ':'")
			}
			d.pos++
			if err := d.skipValue(); err != nil {
				return err
			}
			d.skipSpace()
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of object | 对象意外结束")
			}
			if d.data[d.pos] == ',' {
				d.pos++
				continue
			}
			if d.data[d.pos] == '}' {
				d.pos++
				return nil
			}
			return syntaxErr("expected ',' or '}' | 期望 ',' 或 '}'")
		}
	case '[':
		d.pos++
		d.skipSpace()
		if d.pos < len(d.data) && d.data[d.pos] == ']' {
			d.pos++
			return nil
		}
		for {
			if err := d.skipValue(); err != nil {
				return err
			}
			d.skipSpace()
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of array | 数组意外结束")
			}
			if d.data[d.pos] == ',' {
				d.pos++
				continue
			}
			if d.data[d.pos] == ']' {
				d.pos++
				return nil
			}
			return syntaxErr("expected ',' or ']' | 期望 ',' 或 ']'")
		}
	default:
		_, err := d.parseNumber()
		return err
	}
}

// Encoder writes JSON values to an io.Writer. | Encoder 把 JSON 值写入 io.Writer。
type Encoder struct {
	w          io.Writer
	escapeHTML bool
}

// NewEncoder returns an Encoder writing to w. | NewEncoder 返回写入 w 的 Encoder。
func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w, escapeHTML: true} }

// Encode writes v followed by a newline. | Encode 写入 v 并追加换行。
func (e *Encoder) Encode(v any) error {
	enc := &encoder{escapeHTML: e.escapeHTML}
	if err := enc.value(reflect.ValueOf(v)); err != nil {
		return err
	}
	enc.buf = append(enc.buf, '\n')
	_, err := e.w.Write(enc.buf)
	return err
}

// SetEscapeHTML controls HTML escaping. | SetEscapeHTML 控制 HTML 转义。
func (e *Encoder) SetEscapeHTML(on bool) { e.escapeHTML = on }

// Decoder reads JSON values from an io.Reader. | Decoder 从 io.Reader 读取 JSON 值。
type Decoder struct {
	r         io.Reader
	data      []byte
	pos       int
	loaded    bool
	useNumber bool
}

// NewDecoder returns a Decoder reading from r. | NewDecoder 返回从 r 读取的 Decoder。
func NewDecoder(r io.Reader) *Decoder { return &Decoder{r: r} }

// UseNumber makes Decode store numbers as json.Number. | UseNumber 使 Decode 把数字存为 json.Number。
func (d *Decoder) UseNumber() { d.useNumber = true }

// Decode reads the next JSON value into v. | Decode 读取下一个 JSON 值并写入 v。
func (d *Decoder) Decode(v any) error {
	if !d.loaded {
		data, err := io.ReadAll(d.r)
		if err != nil {
			return err
		}
		d.data, d.loaded = data, true
	}
	dec := &decoder{data: d.data, pos: d.pos, useNumber: d.useNumber}
	dec.skipSpace()
	if dec.pos >= len(dec.data) {
		d.pos = dec.pos
		return io.EOF
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errx.Detail(ErrTypeMismatch, "jsonx: Decode target must be a non-nil pointer | jsonx：Decode 目标必须是非 nil 指针")
	}
	if err := dec.value(rv.Elem()); err != nil {
		return err
	}
	d.pos = dec.pos
	return nil
}

func isValidNumber(s string) bool {
	if s == "" {
		return false
	}
	d := &decoder{data: []byte(s)}
	if _, err := d.parseNumber(); err != nil {
		return false
	}
	return d.pos == len(s)
}

func indentJSON(out *bytes.Buffer, src []byte, prefix, indent string) error {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			out.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '"':
			inString = true
			out.WriteByte(c)
		case '{', '[':
			out.WriteByte(c)
			depth++
			if i+1 < len(src) && (src[i+1] == '}' || src[i+1] == ']') {
				continue
			}
			out.WriteByte('\n')
			out.WriteString(prefix)
			for j := 0; j < depth; j++ {
				out.WriteString(indent)
			}
		case '}', ']':
			depth--
			out.WriteByte('\n')
			out.WriteString(prefix)
			for j := 0; j < depth; j++ {
				out.WriteString(indent)
			}
			out.WriteByte(c)
		case ',':
			out.WriteByte(',')
			out.WriteByte('\n')
			out.WriteString(prefix)
			for j := 0; j < depth; j++ {
				out.WriteString(indent)
			}
		case ':':
			out.WriteString(": ")
		default:
			out.WriteByte(c)
		}
	}
	return nil
}
