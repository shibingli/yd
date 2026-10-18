//go:build jsonx_native

package jsonx

import (
	"bytes"
	"encoding"
	stdjson "encoding/json"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/core/errx"
)

// timeCacheEntry caches one formatted RFC 3339 timestamp. | timeCacheEntry 缓存一个已格式化的
// RFC 3339 时间戳。
type timeCacheEntry struct {
	k       time.Time
	b       [40]byte
	full    [64]byte
	key     [16]byte
	n       uint8
	keyLen  uint8
	fullLen uint8
}

type encoder struct {
	buf        []byte
	timeCache  [4]timeCacheEntry
	escapeHTML bool
	depth      int
}

// enter marks entry into one value level and enforces the encoder nesting limit. | enter 标记进入一层值并
// 执行编码嵌套上限校验。
func (e *encoder) enter() error {
	e.depth++
	if e.depth > maxNestingDepth {
		e.depth--
		return errx.Detail(errUnsupported, "exceeded max depth of %d | 嵌套深度超过上限 %d", maxNestingDepth, maxNestingDepth)
	}
	return nil
}

// leave marks exit from one value level. | leave 标记退出一层值。
func (e *encoder) leave() { e.depth-- }

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
	if err := e.enter(); err != nil {
		return err
	}
	err := encoderFor(v.Type())(e, v)
	e.leave()
	return err
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
	var fastSlot atomic.Pointer[fastStructInfo]
	fast := func() *fastStructInfo {
		if p := fastSlot.Load(); p != nil {
			return p
		}
		info := fastStructFor(t.Elem())
		if info == nil {
			info = &fastStructInfo{}
		}
		fastSlot.Store(info)
		return info
	}
	return func(e *encoder, v reflect.Value) error {
		if v.IsNil() {
			encodeNull(e)
			return nil
		}
		if err := e.enter(); err != nil {
			return err
		}
		e.buf = append(e.buf, '[')
		if sf := fast(); sf.enc != nil {
			n := v.Len()
			if n > 0 {
				base := unsafe.Pointer(v.Pointer())
				size := sf.size
				for i := 0; i < n; i++ {
					if i > 0 {
						e.buf = append(e.buf, ',')
					}
					if err := sf.enc(e, unsafe.Add(base, uintptr(i)*size)); err != nil {
						e.leave()
						return err
					}
				}
			}
			e.buf = append(e.buf, ']')
			e.leave()
			return nil
		}
		enc := elem()
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				e.buf = append(e.buf, ',')
			}
			if err := enc(e, v.Index(i)); err != nil {
				e.leave()
				return err
			}
		}
		e.buf = append(e.buf, ']')
		e.leave()
		return nil
	}
}

func buildArrayEncoder(t reflect.Type) encodeFunc {
	elem := lazyEncoder(t.Elem())
	return func(e *encoder, v reflect.Value) error {
		if err := e.enter(); err != nil {
			return err
		}
		enc := elem()
		e.buf = append(e.buf, '[')
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				e.buf = append(e.buf, ',')
			}
			if err := enc(e, v.Index(i)); err != nil {
				e.leave()
				return err
			}
		}
		e.buf = append(e.buf, ']')
		e.leave()
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
		if err := e.enter(); err != nil {
			return err
		}
		defer e.leave()
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

// fastStructInfo describes a struct whose every field has a direct-memory encoder.
// fastStructInfo 描述所有字段都可直接按内存编码的结构体。
type fastStructInfo struct {
	enc  func(*encoder, unsafe.Pointer) error
	size uintptr
}

var fastStructCache sync.Map

// fastStructFor returns the fast struct encoder for t, if one was built. | fastStructFor 返回 t 的快速
// 结构体编码器（若已构建）。
func fastStructFor(t reflect.Type) *fastStructInfo {
	if v, ok := fastStructCache.Load(t); ok {
		return v.(*fastStructInfo)
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	encoderFor(t)
	if v, ok := fastStructCache.Load(t); ok {
		return v.(*fastStructInfo)
	}
	return nil
}

func buildStructEncoder(t reflect.Type) encodeFunc {
	fields := cachedStruct(t).fields
	type encField struct {
		key       []byte
		fastKey   []byte
		typ       reflect.Type
		index     []int
		offset    uintptr
		direct    int
		fast      func(*encoder, unsafe.Pointer) error
		fastEmpty func(unsafe.Pointer) bool
		enc       encodeFunc
		omitEmpty bool
		quoted    bool
		fastOK    bool
		full      func(*encoder, unsafe.Pointer) error
	}
	list := make([]encField, len(fields))
	allFast := true
	for i, f := range fields {
		direct := -1
		if len(f.index) == 1 {
			direct = f.index[0]
		}
		var ke encoder
		ke.str(f.name)
		ke.buf = append(ke.buf, ':')
		offset, fastOK := fieldOffset(t, f.index)
		fast := buildFastFieldEncoder(f.typ)
		fastKey := ke.buf
		if i > 0 {
			fastKey = append([]byte{','}, ke.buf...)
		}
		list[i] = encField{
			key:       ke.buf,
			fastKey:   fastKey,
			typ:       f.typ,
			index:     f.index,
			offset:    offset,
			direct:    direct,
			fast:      fast,
			fastEmpty: buildFastEmpty(f.typ),
			enc:       encoderFor(f.typ),
			omitEmpty: f.omitEmpty,
			quoted:    f.quoted,
			fastOK:    fastOK && !f.quoted,
		}
		if !fastOK || fast == nil || f.omitEmpty || f.quoted {
			allFast = false
		}
	}
	for i := range list {
		f := &list[i]
		if f.typ != timeType || !f.fastOK || f.omitEmpty || f.quoted || len(f.fastKey) > 16 {
			continue
		}
		key := append([]byte(nil), f.fastKey...)
		f.full = func(e *encoder, p unsafe.Pointer) error {
			tt := (*time.Time)(p)
			entry := &e.timeCache[uint64(tt.UnixNano())&3]
			if entry.fullLen != 0 && entry.k == *tt && int(entry.keyLen) == len(key) && bytes.Equal(entry.key[:len(key)], key) {
				e.buf = append(e.buf, entry.full[:entry.fullLen]...)
				return nil
			}
			if y := tt.Year(); y < 0 || y >= 10000 {
				return errx.Detail(errUnsupported, "time year %d out of range | 时间年份 %d 超出范围", y, y)
			}
			var full [64]byte
			n := copy(full[:], key)
			full[n] = '"'
			n++
			formatted := tt.AppendFormat(full[n:n], time.RFC3339Nano)
			if len(formatted) > cap(full)-n {
				return errx.Detail(errUnsupported, "time formatting overflow | 时间格式化溢出")
			}
			copy(full[n:], formatted)
			n += len(formatted)
			full[n] = '"'
			n++
			e.buf = append(e.buf, full[:n]...)
			copy(entry.full[:], full[:n])
			entry.fullLen = uint8(n)
			entry.keyLen = uint8(len(key))
			copy(entry.key[:], key)
			entry.k = *tt
			return nil
		}
	}
	fastEnc := func(e *encoder, base unsafe.Pointer) error {
		e.buf = append(e.buf, '{')
		for i := range list {
			f := &list[i]
			p := unsafe.Add(base, f.offset)
			if f.full != nil {
				if err := f.full(e, p); err != nil {
					return err
				}
				continue
			}
			e.buf = append(e.buf, f.fastKey...)
			if err := f.fast(e, p); err != nil {
				return err
			}
		}
		e.buf = append(e.buf, '}')
		return nil
	}
	if allFast {
		fastStructCache.Store(t, &fastStructInfo{enc: fastEnc, size: t.Size()})
	}
	return func(e *encoder, v reflect.Value) error {
		if allFast && v.CanAddr() {
			return fastEnc(e, unsafe.Pointer(v.UnsafeAddr()))
		}
		var base unsafe.Pointer
		if v.CanAddr() {
			base = unsafe.Pointer(v.UnsafeAddr())
		}
		e.buf = append(e.buf, '{')
		first := true
		for i := range list {
			f := &list[i]
			var fv reflect.Value
			var fieldPtr unsafe.Pointer
			if base != nil && f.fastOK {
				fieldPtr = unsafe.Add(base, f.offset)
			} else if f.direct >= 0 {
				fv = v.Field(f.direct)
			} else {
				var ok bool
				fv, ok = fieldByIndex(v, f.index)
				if !ok {
					continue
				}
			}
			if f.omitEmpty {
				if fieldPtr != nil && f.fastEmpty != nil {
					if f.fastEmpty(fieldPtr) {
						continue
					}
				} else {
					if !fv.IsValid() {
						fv = reflect.NewAt(f.typ, unsafe.Add(base, f.offset)).Elem()
					}
					if isEmptyValue(fv) {
						continue
					}
				}
			}
			if !first {
				e.buf = append(e.buf, ',')
			}
			first = false
			e.buf = append(e.buf, f.key...)
			if f.quoted {
				if !fv.IsValid() {
					fv = reflect.NewAt(f.typ, fieldPtr).Elem()
				}
				if err := e.quotedValue(fv); err != nil {
					return err
				}
			} else if fieldPtr != nil && f.fast != nil {
				if err := f.fast(e, fieldPtr); err != nil {
					return err
				}
			} else {
				if !fv.IsValid() {
					fv = reflect.NewAt(f.typ, unsafe.Add(base, f.offset)).Elem()
				}
				if err := f.enc(e, fv); err != nil {
					return err
				}
			}
		}
		e.buf = append(e.buf, '}')
		return nil
	}
}

// fieldOffset returns the fixed byte offset of an index path, if every intermediate is a struct value.
// fieldOffset 返回索引路径的固定字节偏移；若中间经过指针则返回 false。
