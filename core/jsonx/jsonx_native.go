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
	"sync/atomic"
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

// encodeFunc encodes one value of a fixed Go type. | encodeFunc 编码一个固定 Go 类型的值。
type encodeFunc func(*encoder, reflect.Value) error

// encodeCache maps each reflected type to its compiled encoder. | encodeCache 把反射类型映射到已编译的编码器。
var encodeCache sync.Map

// encoderFor returns the cached encoder for t, building it on first use. | encoderFor 返回 t 的缓存编码器，
// 首次调用时构建。
func encoderFor(t reflect.Type) encodeFunc {
	if f, ok := encodeCache.Load(t); ok {
		return f.(encodeFunc)
	}
	f := buildEncoder(t)
	encodeCache.Store(t, f)
	return f
}

func (e *encoder) value(v reflect.Value) error {
	if !v.IsValid() {
		e.buf = append(e.buf, "null"...)
		return nil
	}
	return encoderFor(v.Type())(e, v)
}

func encodeNull(e *encoder) {
	e.buf = append(e.buf, "null"...)
}

func buildEncoder(t reflect.Type) encodeFunc {
	if t == numberType {
		return func(e *encoder, v reflect.Value) error {
			s := v.String()
			if !isValidNumber(s) {
				return errx.Detail(errUnsupported, "invalid number %q | 非法数字 %q", s, s)
			}
			e.buf = append(e.buf, s...)
			return nil
		}
	}
	if t.Kind() == reflect.Interface {
		return func(e *encoder, v reflect.Value) error {
			if v.IsNil() {
				encodeNull(e)
				return nil
			}
			return e.value(v.Elem())
		}
	}
	tr := cachedTraits(t)
	if t.Kind() == reflect.Pointer {
		switch {
		case tr.marshaler:
			return func(e *encoder, v reflect.Value) error {
				if v.IsNil() {
					encodeNull(e)
					return nil
				}
				return e.marshaler(v.Interface().(stdjson.Marshaler))
			}
		case tr.textMarshaler:
			return func(e *encoder, v reflect.Value) error {
				if v.IsNil() {
					encodeNull(e)
					return nil
				}
				return encodeText(e, v)
			}
		default:
			return func(e *encoder, v reflect.Value) error {
				if v.IsNil() {
					encodeNull(e)
					return nil
				}
				return e.value(v.Elem())
			}
		}
	}
	if tr.marshaler {
		return func(e *encoder, v reflect.Value) error {
			return e.marshaler(v.Interface().(stdjson.Marshaler))
		}
	}
	if tr.textMarshaler {
		return func(e *encoder, v reflect.Value) error {
			return encodeText(e, v)
		}
	}
	norm := buildKindEncoder(t)
	if reflect.PointerTo(t).Implements(marshalerType) {
		return func(e *encoder, v reflect.Value) error {
			if v.CanAddr() {
				return e.marshaler(v.Addr().Interface().(stdjson.Marshaler))
			}
			return norm(e, v)
		}
	}
	return norm
}

func encodeText(e *encoder, v reflect.Value) error {
	text, err := v.Interface().(encoding.TextMarshaler).MarshalText()
	if err != nil {
		return err
	}
	e.str(string(text))
	return nil
}

func buildKindEncoder(t reflect.Type) encodeFunc {
	switch t.Kind() {
	case reflect.Bool:
		return func(e *encoder, v reflect.Value) error {
			e.buf = strconv.AppendBool(e.buf, v.Bool())
			return nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(e *encoder, v reflect.Value) error {
			e.buf = strconv.AppendInt(e.buf, v.Int(), 10)
			return nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return func(e *encoder, v reflect.Value) error {
			e.buf = strconv.AppendUint(e.buf, v.Uint(), 10)
			return nil
		}
	case reflect.Float32:
		return func(e *encoder, v reflect.Value) error {
			return e.float(v.Float(), 32)
		}
	case reflect.Float64:
		return func(e *encoder, v reflect.Value) error {
			return e.float(v.Float(), 64)
		}
	case reflect.String:
		return func(e *encoder, v reflect.Value) error {
			e.str(v.String())
			return nil
		}
	case reflect.Slice:
		return buildSliceEncoder(t)
	case reflect.Array:
		return buildArrayEncoder(t)
	case reflect.Map:
		return buildMapEncoder(t)
	case reflect.Struct:
		return buildStructEncoder(t)
	default:
		return func(e *encoder, v reflect.Value) error {
			return errx.Detail(errUnsupported, "unsupported type %s | 不支持的类型 %s", t, t)
		}
	}
}

// lazyEncoder defers resolving an element encoder until the first value is encoded.
// 延迟到首次编码时才解析元素编码器，使递归类型不会在构建期无限递归。
func lazyEncoder(t reflect.Type) func() encodeFunc {
	var slot atomic.Pointer[encodeFunc]
	return func() encodeFunc {
		if f := slot.Load(); f != nil {
			return *f
		}
		f := encoderFor(t)
		slot.Store(&f)
		return f
	}
}

func buildSliceEncoder(t reflect.Type) encodeFunc {
	if t.Elem().Kind() == reflect.Uint8 {
		return func(e *encoder, v reflect.Value) error {
			if v.IsNil() {
				encodeNull(e)
				return nil
			}
			e.str(b64x.StdEncoding.EncodeToString(v.Bytes()))
			return nil
		}
	}
	elem := lazyEncoder(t.Elem())
	return func(e *encoder, v reflect.Value) error {
		if v.IsNil() {
			encodeNull(e)
			return nil
		}
		enc := elem()
		e.buf = append(e.buf, '[')
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				e.buf = append(e.buf, ',')
			}
			if err := enc(e, v.Index(i)); err != nil {
				return err
			}
		}
		e.buf = append(e.buf, ']')
		return nil
	}
}

func buildArrayEncoder(t reflect.Type) encodeFunc {
	elem := lazyEncoder(t.Elem())
	return func(e *encoder, v reflect.Value) error {
		enc := elem()
		e.buf = append(e.buf, '[')
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				e.buf = append(e.buf, ',')
			}
			if err := enc(e, v.Index(i)); err != nil {
				return err
			}
		}
		e.buf = append(e.buf, ']')
		return nil
	}
}

func buildMapKeyEncoder(t reflect.Type) func(reflect.Value) (string, error) {
	switch t.Kind() {
	case reflect.String:
		return func(k reflect.Value) (string, error) { return k.String(), nil }
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(k reflect.Value) (string, error) { return strconv.FormatInt(k.Int(), 10), nil }
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return func(k reflect.Value) (string, error) { return strconv.FormatUint(k.Uint(), 10), nil }
	}
	if t.Implements(textMarshalerType) {
		return func(k reflect.Value) (string, error) {
			text, err := k.Interface().(encoding.TextMarshaler).MarshalText()
			if err != nil {
				return "", err
			}
			return string(text), nil
		}
	}
	return func(k reflect.Value) (string, error) {
		return "", errx.Detail(errUnsupported, "unsupported map key type %s | 不支持的 map 键类型 %s", t, t)
	}
}

func buildMapEncoder(t reflect.Type) encodeFunc {
	key := buildMapKeyEncoder(t.Key())
	elem := lazyEncoder(t.Elem())
	type kv struct {
		key string
		val reflect.Value
	}
	return func(e *encoder, v reflect.Value) error {
		if v.IsNil() {
			encodeNull(e)
			return nil
		}
		enc := elem()
		entries := make([]kv, 0, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			name, err := key(iter.Key())
			if err != nil {
				return err
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
			if err := enc(e, entry.val); err != nil {
				return err
			}
		}
		e.buf = append(e.buf, '}')
		return nil
	}
}

func buildStructEncoder(t reflect.Type) encodeFunc {
	fields := cachedStruct(t).fields
	type encField struct {
		name      string
		index     []int
		direct    int
		enc       encodeFunc
		omitEmpty bool
		quoted    bool
	}
	list := make([]encField, len(fields))
	for i, f := range fields {
		direct := -1
		if len(f.index) == 1 {
			direct = f.index[0]
		}
		list[i] = encField{
			name:      f.name,
			index:     f.index,
			direct:    direct,
			enc:       encoderFor(f.typ),
			omitEmpty: f.omitEmpty,
			quoted:    f.quoted,
		}
	}
	return func(e *encoder, v reflect.Value) error {
		e.buf = append(e.buf, '{')
		first := true
		for i := range list {
			f := &list[i]
			var fv reflect.Value
			if f.direct >= 0 {
				fv = v.Field(f.direct)
			} else {
				var ok bool
				fv, ok = fieldByIndex(v, f.index)
				if !ok {
					continue
				}
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
			} else if err := f.enc(e, fv); err != nil {
				return err
			}
		}
		e.buf = append(e.buf, '}')
		return nil
	}
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

// fieldInfo is one exported struct field. | fieldInfo 是一个可导出的结构体字段。
type fieldInfo struct {
	name      string
	typ       reflect.Type
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
		out = append(out, fieldInfo{name: name, typ: f.Type, index: idx, omitEmpty: strings.Contains(opts, "omitempty"), quoted: strings.Contains(opts, "string")})
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

// decodeFunc decodes one JSON value into a fixed Go type. | decodeFunc 把一个 JSON 值解析进固定 Go 类型。
type decodeFunc func(*decoder, reflect.Value) error

// decodeCache maps each reflected type to its compiled decoder. | decodeCache 把反射类型映射到已编译的解码器。
var decodeCache sync.Map

// decoderFor returns the cached decoder for t, building it on first use. | decoderFor 返回 t 的缓存解码器，
// 首次调用时构建。
func decoderFor(t reflect.Type) decodeFunc {
	if f, ok := decodeCache.Load(t); ok {
		return f.(decodeFunc)
	}
	f := buildDecoder(t)
	decodeCache.Store(t, f)
	return f
}

func (d *decoder) value(v reflect.Value) error {
	d.skipSpace()
	if d.pos >= len(d.data) {
		return syntaxErr("unexpected end of JSON input | JSON 输入意外结束")
	}
	return decoderFor(v.Type())(d, v)
}

// lazyDecoder defers resolving an element decoder until the first value is decoded.
// 延迟到首次解码时才解析元素解码器，使递归类型不会在构建期无限递归。
func lazyDecoder(t reflect.Type) func() decodeFunc {
	var slot atomic.Pointer[decodeFunc]
	return func() decodeFunc {
		if f := slot.Load(); f != nil {
			return *f
		}
		f := decoderFor(t)
		slot.Store(&f)
		return f
	}
}

func buildDecoder(t reflect.Type) decodeFunc {
	if t == rawMessageType {
		return func(d *decoder, v reflect.Value) error {
			raw, err := d.captureValue()
			if err != nil {
				return err
			}
			v.SetBytes(raw)
			return nil
		}
	}
	if t == numberType {
		return func(d *decoder, v reflect.Value) error {
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
	}
	if t.Kind() == reflect.Interface {
		if t.NumMethod() == 0 {
			return func(d *decoder, v reflect.Value) error {
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
		}
		return func(d *decoder, v reflect.Value) error {
			if err := d.literal("null"); err == nil {
				v.SetZero()
				return nil
			}
			return typeErr("cannot unmarshal into non-empty interface %s | 无法解析进非空接口 %s", t, t)
		}
	}
	tr := cachedTraits(t)
	if t.Kind() == reflect.Pointer {
		elem := lazyDecoder(t.Elem())
		switch {
		case tr.unmarshaler:
			return func(d *decoder, v reflect.Value) error {
				if d.data[d.pos] == 'n' {
					if err := d.literal("null"); err != nil {
						return err
					}
					v.SetZero()
					return nil
				}
				if v.IsNil() {
					v.Set(reflect.New(t.Elem()))
				}
				return decodeUnmarshaler(d, v)
			}
		case tr.textUnmarshaler:
			return func(d *decoder, v reflect.Value) error {
				if d.data[d.pos] == 'n' {
					if err := d.literal("null"); err != nil {
						return err
					}
					v.SetZero()
					return nil
				}
				if d.data[d.pos] == '"' {
					if v.IsNil() {
						v.Set(reflect.New(t.Elem()))
					}
					return decodeTextUnmarshaler(d, v)
				}
				if v.IsNil() {
					v.Set(reflect.New(t.Elem()))
				}
				return elem()(d, v.Elem())
			}
		default:
			return func(d *decoder, v reflect.Value) error {
				if d.data[d.pos] == 'n' {
					if err := d.literal("null"); err != nil {
						return err
					}
					v.SetZero()
					return nil
				}
				if v.IsNil() {
					v.Set(reflect.New(t.Elem()))
				}
				return elem()(d, v.Elem())
			}
		}
	}
	if tr.unmarshaler {
		return func(d *decoder, v reflect.Value) error {
			return decodeUnmarshaler(d, v)
		}
	}
	norm := buildKindDecoder(t)
	if reflect.PointerTo(t).Implements(unmarshalerType) {
		return func(d *decoder, v reflect.Value) error {
			if v.CanAddr() {
				return decodeUnmarshaler(d, v.Addr())
			}
			return norm(d, v)
		}
	}
	if tr.textUnmarshaler {
		return func(d *decoder, v reflect.Value) error {
			if d.data[d.pos] == '"' {
				return decodeTextUnmarshaler(d, v)
			}
			return norm(d, v)
		}
	}
	return norm
}

func decodeUnmarshaler(d *decoder, v reflect.Value) error {
	raw, err := d.captureValue()
	if err != nil {
		return err
	}
	return v.Interface().(stdjson.Unmarshaler).UnmarshalJSON(raw)
}

func decodeTextUnmarshaler(d *decoder, v reflect.Value) error {
	s, err := d.parseString()
	if err != nil {
		return err
	}
	return v.Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s))
}

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
			s, err := d.parseNumber()
			if err != nil {
				return err
			}
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
			s, err := d.parseNumber()
			if err != nil {
				return err
			}
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
			s, err := d.parseNumber()
			if err != nil {
				return err
			}
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
		if d.data[d.pos] != '[' {
			return typeErr("cannot unmarshal non-array into %s | 无法把非数组解析进 %s", t, t)
		}
		enc := elem()
		d.pos++
		d.skipSpace()
		if d.pos < len(d.data) && d.data[d.pos] == ']' {
			d.pos++
			if !v.IsNil() {
				v.SetLen(0)
			}
			return nil
		}
		if v.IsNil() {
			v.Set(reflect.MakeSlice(t, 0, 4))
		}
		i := 0
		for {
			if i >= v.Len() {
				v.Set(reflect.Append(v, reflect.Zero(t.Elem())))
			}
			d.skipSpace()
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of array | 数组意外结束")
			}
			if err := enc(d, v.Index(i)); err != nil {
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

func buildMapDecoder(t reflect.Type) decodeFunc {
	if t.Key().Kind() != reflect.String {
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
			mapKey.SetString(key)
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

func buildStructDecoder(t reflect.Type) decodeFunc {
	info := cachedStruct(t)
	type decField struct {
		index []int
		dec   func() decodeFunc
	}
	fields := make([]*decField, len(info.fields))
	byName := make(map[string]int, len(info.fields))
	byFold := make(map[string]int, len(info.fields))
	for i, f := range info.fields {
		fields[i] = &decField{index: f.index, dec: lazyDecoder(f.typ)}
		byName[f.name] = i
		byFold[strings.ToLower(f.name)] = i
	}
	lookup := func(key string) (*decField, bool) {
		i, ok := byName[key]
		if !ok {
			i, ok = byFold[strings.ToLower(key)]
		}
		if !ok {
			return nil, false
		}
		return fields[i], true
	}
	return func(d *decoder, v reflect.Value) error {
		if d.data[d.pos] == 'n' {
			return d.literal("null")
		}
		if d.data[d.pos] != '{' {
			return typeErr("cannot unmarshal non-object into %s | 无法把非对象解析进 %s", t, t)
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
			f, ok := lookup(key)
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
				} else {
					d.skipSpace()
					if d.pos >= len(d.data) {
						return syntaxErr("unexpected end of object | 对象意外结束")
					}
					if err := f.dec()(d, fv); err != nil {
						return err
					}
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
