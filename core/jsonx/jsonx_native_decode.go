//go:build jsonx_native

package jsonx

import (
	"bytes"
	"encoding"
	"encoding/binary"
	stdjson "encoding/json"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/shibingli/yd/core/errx"
)

// maxNestingDepth aligns with encoding/json's scanner limit. | maxNestingDepth 与 encoding/json 的扫描器上限一致。
const maxNestingDepth = 10000

type decoder struct {
	data      []byte
	arena     []byte
	timeVal   time.Time
	timeRaw   [40]byte
	pos       int
	depth     int
	timeLen   uint8
	useNumber bool
	skipLen   bool
}

// enter marks entry into one container and enforces the nesting limit. | enter 标记进入一层容器并执行嵌套上限校验。
func (d *decoder) enter() error {
	d.depth++
	if d.depth > maxNestingDepth {
		d.depth--
		return errx.Detail(ErrSyntax, "exceeded max depth of %d | 嵌套深度超过上限 %d", maxNestingDepth, maxNestingDepth)
	}
	return nil
}

// leave marks exit from one container. | leave 标记退出一层容器。
func (d *decoder) leave() { d.depth-- }

// copyString copies b into the decoder's string arena and returns a string over it. Strings keep
// the arena alive through interior pointers, so the result stays valid after Unmarshal returns and
// never aliases the caller's input. | copyString 把 b 复制进解码器的字符串 arena 并返回其字符串；
// 字符串经内部指针保持 arena 存活，因此结果在 Unmarshal 返回后仍有效，也不会别名调用方输入。
func (d *decoder) copyString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if d.arena == nil {
		// Decoded strings are a fraction of the input (keys are not copied and structural bytes
		// take space), so a third of the input is a good first guess; too small a hint forces one
		// growth that keeps the old array alive through the returned strings. | 解码出的字符串
		// 只占输入的一部分（键不复制、结构字节也占空间），取输入的三分之一作为初始估算；估算过小
		// 会触发一次扩容，而旧数组会被返回的字符串继续引用。
		hint := len(d.data) / 3
		if hint < 64 {
			hint = 64
		}
		if hint > 1<<20 {
			hint = 1 << 20
		}
		d.arena = make([]byte, 0, hint)
	}
	n := len(b)
	// Short strings are copied with fixed-size stores. b keeps the input's capacity, so an
	// eight-byte load may read bytes past its length but never past its capacity; the extra bytes
	// land beyond the new arena length and are overwritten or ignored. | 短字符串用定长存储复制。
	// b 保留输入的容量，因此 8 字节加载可能越过长度但绝不会越过容量；多余字节落在新 arena 长度
	// 之外，会被后续覆盖或忽略。
	if n <= 16 && cap(b) >= 8 {
		start := len(d.arena)
		need := n
		if n < 8 {
			need = 8
		}
		if cap(d.arena)-start >= need {
			dst := d.arena[start : start+need]
			if n >= 8 {
				binary.LittleEndian.PutUint64(dst, binary.LittleEndian.Uint64(b[:8]))
				binary.LittleEndian.PutUint64(dst[n-8:], binary.LittleEndian.Uint64(b[n-8:n]))
			} else {
				binary.LittleEndian.PutUint64(dst, binary.LittleEndian.Uint64(b[:8]))
			}
			d.arena = d.arena[:start+n]
			return unsafe.String(unsafe.SliceData(d.arena[start:]), n)
		}
	}
	start := len(d.arena)
	d.arena = append(d.arena, b...)
	return unsafe.String(unsafe.SliceData(d.arena[start:]), n)
}

// decoderPool reuses decoder state across Unmarshal calls. The string arena is deliberately
// dropped on release: returned strings point into it, so it must never be reused. | decoderPool
// 在多次 Unmarshal 调用间复用解码器状态；字符串 arena 在归还时刻意丢弃，因为返回的字符串
// 指向它，绝不能复用。
var decoderPool = sync.Pool{New: func() any { return new(decoder) }}

// Unmarshal parses data and stores the result in the value pointed to by v. | Unmarshal 解析 data 并写入 v 指向的值。
func Unmarshal(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errx.Detail(ErrTypeMismatch, "jsonx: Unmarshal target must be a non-nil pointer | jsonx：Unmarshal 目标必须是非 nil 指针")
	}
	d := decoderPool.Get().(*decoder)
	*d = decoder{data: data}
	err := d.unmarshal(rv)
	*d = decoder{}
	decoderPool.Put(d)
	return err
}

// unmarshal decodes one top-level value into rv. | unmarshal 把一个顶层值解码进 rv。
func (d *decoder) unmarshal(rv reflect.Value) error {
	d.skipSpace()
	if d.pos >= len(d.data) {
		return syntaxErr("unexpected end of JSON input | JSON 输入意外结束")
	}
	target := rv.Elem()
	if target.Kind() == reflect.Slice && target.IsNil() && d.data[d.pos] == '[' {
		guess := len(d.data) / 128
		if guess > 1<<20 {
			guess = 1 << 20
		}
		if guess > 0 {
			target.Set(reflect.MakeSlice(target.Type(), guess, guess))
			d.skipLen = true
		}
	}
	if err := d.value(target); err != nil {
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

// decodeFast mirrors encodeFast for decoders: the hot path resolves the same few types on every
// call, so a handful of atomic slots avoids hashing a reflect.Type via sync.Map each time.
// | decodeFast 是解码器侧与 encodeFast 对应的前置缓存：热路径每次只解析固定几种类型，几个原子槽
// 即可避免每次都经 sync.Map 哈希 reflect.Type。
const decodeFastSlots = 4

type decodeFastEntry struct {
	typ reflect.Type
	fn  decodeFunc
}

var (
	decodeFast  [decodeFastSlots]atomic.Pointer[decodeFastEntry]
	decodeFastN atomic.Uint32
	decodeFastL atomic.Uint32 // index of the most recent slot, a lookup hint | 最近使用槽位的索引，仅作查找提示
)

// decoderFor returns the cached decoder for t, building it on first use. | decoderFor 返回 t 的缓存解码器，
// 首次调用时构建。
func decoderFor(t reflect.Type) decodeFunc {
	// See encoderFor: the hint turns the steady single-type case into one atomic load and one
	// comparison, and a stale hint stays correct because the slot is compared by type. | 参见
	// encoderFor：提示位把单一类型稳态降为一次原子加载与一次比较，提示过期也仍正确，因为槽位按类型比较。
	last := decodeFastL.Load()
	if last < decodeFastSlots {
		if e := decodeFast[last].Load(); e != nil && e.typ == t {
			return e.fn
		}
	}
	for i := range decodeFast {
		if uint32(i) == last {
			continue
		}
		if e := decodeFast[i].Load(); e != nil && e.typ == t {
			return e.fn
		}
	}
	var f decodeFunc
	if v, ok := decodeCache.Load(t); ok {
		f = v.(decodeFunc)
	} else {
		f = buildDecoder(t)
		decodeCache.Store(t, f)
	}
	slot := decodeFastN.Add(1) % decodeFastSlots
	decodeFast[slot].Store(&decodeFastEntry{typ: t, fn: f})
	decodeFastL.Store(slot)
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
	raw, err := d.captureValueView()
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

func buildStructDecoder(t reflect.Type) decodeFunc {
	info := cachedStruct(t)
	type decField struct {
		index  []int
		offset uintptr
		fast   fastFieldDec
		dec    func() decodeFunc
		fastOK bool
	}
	fields := make([]*decField, len(info.fields))
	names := make([]string, len(info.fields))
	anyFast := false
	for i, f := range info.fields {
		offset, fastOK := fieldOffset(t, f.index)
		fast := buildFastFieldDecoder(f.typ)
		fields[i] = &decField{index: f.index, offset: offset, fast: fast, dec: lazyDecoder(f.typ), fastOK: fastOK && fast != nil}
		names[i] = f.name
		if fast != nil && fastOK {
			anyFast = true
		}
	}
	var lookup func(string) (*decField, bool)
	var matchKey func(*decoder) (*decField, bool)
	matchKey = func(*decoder) (*decField, bool) { return nil, false }
	if len(names) <= 8 {
		var byLen [33][]int
		var byFirst [256][]int
		var single [256]int16 // index+1 for first-byte buckets with one candidate | 单候选首字节桶的索引+1
		keyBytes := make([][]byte, len(names))
		keyWords := make([]uint64, len(names))
		keyTails := make([]uint64, len(names))
		keyMasks := make([]uint64, len(names))
		keyLens := make([]int, len(names))
		for i, name := range names {
			kb := make([]byte, 0, len(name)+2)
			kb = append(kb, name...)
			kb = append(kb, '"', ':')
			keyBytes[i] = kb
			l := len(kb)
			keyLens[i] = l
			// Keys up to eight bytes are compared with one masked word. Longer keys use the first
			// and last eight bytes, which avoids a memequal call for the common 9-16 byte names. |
			// 不超过八字节的键用一次掩码比较；更长的键比较首尾两个八字，避免为常见的 9-16 字节
			// 字段名走 memequal。
			if l >= 8 {
				keyWords[i] = binary.LittleEndian.Uint64(kb)
				keyTails[i] = binary.LittleEndian.Uint64(kb[l-8:])
				keyMasks[i] = ^uint64(0)
			} else {
				var w uint64
				for j := 0; j < l; j++ {
					w |= uint64(kb[j]) << (8 * j)
				}
				keyWords[i] = w
				keyMasks[i] = uint64(1)<<(8*l) - 1
			}
			if l := len(name); l < len(byLen) {
				byLen[l] = append(byLen[l], i)
			}
			if len(name) > 0 {
				byFirst[name[0]] = append(byFirst[name[0]], i)
			}
		}
		for b := range byFirst {
			if len(byFirst[b]) == 1 {
				single[b] = int16(byFirst[b][0]) + 1
			}
		}
		matchKey = func(d *decoder) (*decField, bool) {
			if d.pos+1 >= len(d.data) {
				return nil, false
			}
			rem := d.data[d.pos+1:]
			if si := single[d.data[d.pos+1]]; si != 0 {
				// Most first-byte buckets hold exactly one field. Match it directly and fail
				// without scanning when it does not match, since no other candidate exists. |
				// 多数首字节桶只含一个字段：直接匹配；不匹配时无需扫描即可失败，因为没有其他候选。
				i := int(si - 1)
				l := keyLens[i]
				if l <= 8 {
					if len(rem) >= 8 {
						x := binary.LittleEndian.Uint64(unsafe.Slice(unsafe.SliceData(rem), 8)) ^ keyWords[i]
						if x&keyMasks[i] == 0 {
							d.pos += 1 + l
							return fields[i], true
						}
						return nil, false
					}
				} else if len(rem) >= l {
					if binary.LittleEndian.Uint64(unsafe.Slice(unsafe.SliceData(rem), 8)) == keyWords[i] &&
						binary.LittleEndian.Uint64(unsafe.Slice(unsafe.SliceData(rem[l-8:]), 8)) == keyTails[i] {
						d.pos += 1 + l
						return fields[i], true
					}
					return nil, false
				}
				if len(rem) >= l && bytes.HasPrefix(rem, keyBytes[i]) {
					d.pos += 1 + l
					return fields[i], true
				}
				return nil, false
			}
			for _, i := range byFirst[d.data[d.pos+1]] {
				l := keyLens[i]
				if l <= 8 {
					if len(rem) >= 8 {
						x := binary.LittleEndian.Uint64(unsafe.Slice(unsafe.SliceData(rem), 8)) ^ keyWords[i]
						if x&keyMasks[i] == 0 {
							d.pos += 1 + l
							return fields[i], true
						}
						continue
					}
				} else if len(rem) >= l {
					if binary.LittleEndian.Uint64(unsafe.Slice(unsafe.SliceData(rem), 8)) == keyWords[i] &&
						binary.LittleEndian.Uint64(unsafe.Slice(unsafe.SliceData(rem[l-8:]), 8)) == keyTails[i] {
						d.pos += 1 + l
						return fields[i], true
					}
					continue
				}
				kb := keyBytes[i]
				if len(rem) >= l && bytes.HasPrefix(rem, kb) {
					d.pos += 1 + l
					return fields[i], true
				}
			}
			return nil, false
		}
		lookup = func(key string) (*decField, bool) {
			if l := len(key); l < len(byLen) {
				for _, i := range byLen[l] {
					if key == names[i] {
						return fields[i], true
					}
				}
			} else {
				for i := range names {
					if key == names[i] {
						return fields[i], true
					}
				}
			}
			for i := range names {
				if strings.EqualFold(key, names[i]) {
					return fields[i], true
				}
			}
			return nil, false
		}
	} else {
		byName := make(map[string]int, len(names))
		for i, name := range names {
			byName[name] = i
		}
		// Exact matches use the map; case-insensitive matches must use strings.EqualFold rather
		// than a lower-cased map, because Unicode simple folding (e.g. 'ſ' and 's') is not the
		// same relation as ToLower equality. | 精确匹配走映射；大小写不敏感匹配必须使用
		// strings.EqualFold，而不能用转小写映射——Unicode 简单折叠（如 'ſ' 与 's'）与
		// ToLower 相等的语义并不相同。
		lookup = func(key string) (*decField, bool) {
			if i, ok := byName[key]; ok {
				return fields[i], true
			}
			for i := range names {
				if strings.EqualFold(key, names[i]) {
					return fields[i], true
				}
			}
			return nil, false
		}
	}
	return func(d *decoder, v reflect.Value) error {
		if d.data[d.pos] == 'n' {
			return d.literal("null")
		}
		if err := d.enter(); err != nil {
			return err
		}
		defer d.leave()
		if d.data[d.pos] != '{' {
			return typeErr("cannot unmarshal non-object into %s | 无法把非对象解析进 %s", t, t)
		}
		var base unsafe.Pointer
		if anyFast && v.CanAddr() {
			base = unsafe.Pointer(v.UnsafeAddr())
		}
		d.pos++
		d.skipSpace()
		if d.pos < len(d.data) && d.data[d.pos] == '}' {
			d.pos++
			return nil
		}
		for {
			if d.pos >= len(d.data) || d.data[d.pos] != '"' {
				return syntaxErr("expected object key | 期望对象键")
			}
			f, ok := matchKey(d)
			if !ok {
				key, err := d.parseKey()
				if err != nil {
					return err
				}
				d.skipSpace()
				if d.pos >= len(d.data) || d.data[d.pos] != ':' {
					return syntaxErr("expected ':' after object key | 对象键之后期望 ':'")
				}
				d.pos++
				f, ok = lookup(key)
			}
			d.skipSpace()
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of object | 对象意外结束")
			}
			if !ok {
				if err := d.skipValue(); err != nil {
					return err
				}
			} else if base != nil && f.fastOK {
				if err := f.fast(d, unsafe.Add(base, f.offset)); err != nil {
					return err
				}
			} else {
				fv, ok, err := fieldByIndexAlloc(v, f.index)
				if err != nil {
					return err
				}
				if !ok {
					if err := d.skipValue(); err != nil {
						return err
					}
				} else {
					if err := f.dec()(d, fv); err != nil {
						return err
					}
				}
			}
			if d.pos < len(d.data) {
				switch d.data[d.pos] {
				case ',':
					d.pos++
					d.skipSpace()
					continue
				case '}':
					d.pos++
					return nil
				}
			}
			d.skipSpace()
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of object | 对象意外结束")
			}
			switch d.data[d.pos] {
			case ',':
				d.pos++
				d.skipSpace()
			case '}':
				d.pos++
				return nil
			default:
				return syntaxErr("expected ',' or '}' in object | 对象中期望 ',' 或 '}'")
			}
		}
	}
}

// fieldByIndexAlloc walks an index path and allocates nil embedded pointers on the way. It reports
// an error for an embedded pointer to an unexported struct type, which cannot be allocated through
// reflection; this matches encoding/json's behavior. | fieldByIndexAlloc 遍历索引路径，并在途中
// 分配为 nil 的嵌入指针；若嵌入指针指向未导出结构体类型（反射无法分配），则返回错误，与
// encoding/json 的行为一致。
func fieldByIndexAlloc(v reflect.Value, index []int) (reflect.Value, bool, error) {
	for _, i := range index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				if !v.CanSet() {
					elem := v.Type().Elem()
					return reflect.Value{}, false, typeErr("cannot set embedded pointer to unexported struct %s | 无法设置指向未导出结构体的嵌入指针 %s", elem, elem)
				}
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct || i >= v.NumField() {
			return reflect.Value{}, false, nil
		}
		v = v.Field(i)
	}
	return v, v.CanSet(), nil
}
