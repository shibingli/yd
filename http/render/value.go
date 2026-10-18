package render

// Value access and formatting. | 值访问与格式化。
//
// Map lookups take a direct path for the map[string]any shape used by the
// framework facade; struct fields, non-string maps and methods fall back to
// reflection. Truthiness and printing follow text/template so the compiled
// engine stays a drop-in replacement. | 映射查找对框架门面使用的 map[string]any
// 走直接路径；结构体字段、非字符串键映射与方法退回反射。真值判断与打印遵循
// text/template，使编译引擎可直接替换标准库实现。

import (
	"fmt"
	"html/template"
	"reflect"
	"strconv"
	"sync"
)

// structFieldKey identifies one struct field lookup. | structFieldKey 标识一次结构体字段查找。
type structFieldKey struct {
	typ  reflect.Type
	name string
}

// structFieldInfo caches a field index path. | structFieldInfo 缓存字段索引路径。
type structFieldInfo struct {
	index []int
	ok    bool
}

// structFieldCache memoizes FieldByName, which is one of the hottest reflection
// calls when rendering struct slices. | structFieldCache 缓存 FieldByName —— 渲染结构体切片时
// 最热的反射调用之一。
var structFieldCache sync.Map

// structField returns the cached index path of an exported field. | structField 返回导出字段的缓存索引路径。
func structField(t reflect.Type, name string) ([]int, bool) {
	key := structFieldKey{typ: t, name: name}
	if v, ok := structFieldCache.Load(key); ok {
		info := v.(structFieldInfo)
		return info.index, info.ok
	}
	f, ok := t.FieldByName(name)
	info := structFieldInfo{}
	if ok && f.PkgPath == "" {
		info.index = f.Index
		info.ok = true
	}
	structFieldCache.Store(key, info)
	return info.index, info.ok
}

// missingValue marks a map key that is absent; nilValue marks one that is
// present with a nil value. text/template distinguishes the two: a missing key
// silently propagates through a field chain, while an explicit nil fails when a
// field is requested from it. | missingValue 标记不存在的映射键；nilValue 标记存在但值为
// nil 的映射键。text/template 区分二者：缺失键在字段链中静默传播，显式 nil 在继续求字段时失败。
type missingValue struct{}

type nilValue struct{}

// normalizeValue converts both markers to ordinary nil at a pipeline boundary.
// | normalizeValue 在流水线边界把两种标记转换为普通 nil。
func normalizeValue(v any) any {
	switch v.(type) {
	case missingValue, nilValue:
		return nil
	}
	return v
}

// isNilMarker reports whether v is one of the nil markers. | isNilMarker 报告 v 是否为 nil 标记之一。
func isNilMarker(v any) bool {
	switch v.(type) {
	case missingValue, nilValue:
		return true
	}
	return false
}

// fieldOrMethod resolves one name on receiver, preferring a method exactly as
// text/template does. | fieldOrMethod 在 receiver 上解析一个名字；与 text/template
// 一致，优先当作方法。
func fieldOrMethod(s *execState, receiver any, name string, args []any, forceCall bool) (any, error) {
	if m, ok := lookupMethod(receiver, name); ok {
		return callReflect(m, args)
	}
	if forceCall {
		return nil, execErr("%[1]s is not a method but has arguments | %[1]s 不是方法却带参数", name)
	}
	return fieldValue(receiver, name)
}

// lookupMethod returns the named method of receiver, if any. | lookupMethod 返回 receiver 的指定方法（若存在）。
func lookupMethod(receiver any, name string) (reflect.Value, bool) {
	if receiver == nil {
		return reflect.Value{}, false
	}
	rv := reflect.ValueOf(receiver)
	if !rv.IsValid() {
		return reflect.Value{}, false
	}
	if m := rv.MethodByName(name); m.IsValid() {
		return m, true
	}
	if rv.Kind() != reflect.Pointer && rv.CanAddr() {
		if m := rv.Addr().MethodByName(name); m.IsValid() {
			return m, true
		}
	}
	return reflect.Value{}, false
}

// fieldValue reads one field or map entry. | fieldValue 读取一个字段或映射项。
func fieldValue(receiver any, name string) (any, error) {
	if receiver == nil {
		return missingValue{}, nil
	}
	switch m := receiver.(type) {
	case map[string]any:
		v, ok := m[name]
		if !ok {
			return missingValue{}, nil
		}
		if v == nil {
			return nilValue{}, nil
		}
		return v, nil
	case map[string]string:
		v, ok := m[name]
		if !ok {
			return missingValue{}, nil
		}
		return v, nil
	}
	rv := reflect.ValueOf(receiver)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nilValue{}, nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Struct:
		idx, ok := structField(rv.Type(), name)
		if !ok {
			return nil, execErr("can't evaluate field %[1]s in type %[2]s | 无法在类型 %[2]s 上求值字段 %[1]s", name, rv.Type())
		}
		f, err := rv.FieldByIndexErr(idx)
		if err != nil {
			return nil, execErr("can't evaluate field %[1]s in type %[2]s | 无法在类型 %[2]s 上求值字段 %[1]s", name, rv.Type())
		}
		return f.Interface(), nil
	case reflect.Map:
		key := reflect.ValueOf(name)
		if !key.Type().AssignableTo(rv.Type().Key()) {
			return nil, execErr("can't use string as key of %[1]s | 不能把字符串用作 %[1]s 的键", rv.Type())
		}
		v := rv.MapIndex(key)
		if !v.IsValid() {
			return missingValue{}, nil
		}
		e := elementValue(v)
		if e == nil {
			return nilValue{}, nil
		}
		return e, nil
	default:
		return nil, execErr("can't evaluate field %[1]s in type %[2]s | 无法在类型 %[2]s 上求值字段 %[1]s", name, rv.Type())
	}
}

// resolveFieldRef walks a field chain while keeping the value as reflect.Value,
// so strings and numbers are not boxed into interfaces on the way out. It
// reports false when the chain needs the generic evaluator (a method, an
// unsupported receiver, or an unexported field). | resolveFieldRef 以 reflect.Value
// 遍历字段链，使字符串与数字在输出路径上不被装箱进接口。遇到方法、不支持的接收者或
// 未导出字段时返回 false，交由通用求值器处理。
func resolveFieldRef(root any, chain []string) (reflect.Value, bool) {
	v := reflect.ValueOf(root)
	for _, name := range chain {
		for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
			if v.IsNil() {
				// A nil receiver with a field still to evaluate is an explicit nil:
				// fall back so the generic evaluator reports the same error as
				// text/template. | 仍有字段要求值时遇到 nil 接收者即为显式 nil：回退通用
				// 求值器，以报告与 text/template 相同的错误。
				return reflect.Value{}, false
			}
			v = v.Elem()
		}
		if !v.IsValid() {
			return reflect.Value{}, true
		}
		switch v.Kind() {
		case reflect.Struct:
			idx, ok := structField(v.Type(), name)
			if !ok {
				return reflect.Value{}, false
			}
			f, err := v.FieldByIndexErr(idx)
			if err != nil {
				return reflect.Value{}, false
			}
			v = f
		case reflect.Map:
			key := reflect.ValueOf(name)
			if !key.Type().AssignableTo(v.Type().Key()) {
				return reflect.Value{}, false
			}
			v = v.MapIndex(key)
		default:
			return reflect.Value{}, false
		}
	}
	return v, true
}

// appendEscapedReflect escapes a reflect value without boxing primitive kinds.
// | appendEscapedReflect 转义反射值，且不对基础类型装箱。
func appendEscapedReflect(dst []byte, rv reflect.Value, kind escKind) []byte {
	for rv.IsValid() && rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return appendEscapedString(dst, "", kind)
		}
		rv = rv.Elem()
	}
	// A pointer implementing error or fmt.Stringer must be formatted through
	// fmt while it is still a pointer: dereferencing first would drop a
	// pointer-receiver method and print the bare struct instead. | 实现 error 或
	// fmt.Stringer 的指针必须在仍为指针时交给 fmt：先解引用会丢掉指针接收者方法，
	// 输出裸结构体。
	if rv.IsValid() && rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			// A typed nil pointer prints as "<nil>" through fmt, exactly as
			// html/template does. | 带类型的 nil 指针经 fmt 输出 "<nil>"，与
			// html/template 一致。
			return appendEscaped(dst, rv.Interface(), kind)
		}
		t := rv.Type()
		if t.Implements(reflect.TypeFor[error]()) || t.Implements(reflect.TypeFor[fmt.Stringer]()) {
			return appendEscaped(dst, rv.Interface(), kind)
		}
		rv = rv.Elem()
	}
	for rv.IsValid() && rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return appendEscaped(dst, rv.Interface(), kind)
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return appendEscapedString(dst, "", kind)
	}
	if ct, ok := safeContentType(rv.Type()); ok {
		return appendEscapedTyped(dst, rv.String(), ct, kind)
	}
	// Named types may implement fmt.Stringer or error, so only predeclared
	// kinds take the primitive fast path; the rest go through stringify. | 具名类型
	// 可能实现 fmt.Stringer 或 error，因此只有预声明类型走基础快路径，其余交给 stringify。
	if rv.Type().PkgPath() != "" {
		return appendEscaped(dst, rv.Interface(), kind)
	}
	switch rv.Kind() {
	case reflect.String:
		return appendEscapedString(dst, rv.String(), kind)
	case reflect.Bool:
		var buf [8]byte
		return appendEscapedBytes(dst, strconv.AppendBool(buf[:0], rv.Bool()), kind)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var buf [24]byte
		return appendEscapedBytes(dst, strconv.AppendInt(buf[:0], rv.Int(), 10), kind)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var buf [24]byte
		return appendEscapedBytes(dst, strconv.AppendUint(buf[:0], rv.Uint(), 10), kind)
	case reflect.Float32:
		var buf [32]byte
		return appendEscapedBytes(dst, strconv.AppendFloat(buf[:0], rv.Float(), 'g', -1, 32), kind)
	case reflect.Float64:
		var buf [32]byte
		return appendEscapedBytes(dst, strconv.AppendFloat(buf[:0], rv.Float(), 'g', -1, 64), kind)
	default:
		return appendEscaped(dst, rv.Interface(), kind)
	}
}

// safeContentType maps html/template's trusted string types to content kinds.
// | safeContentType 把 html/template 的受信字符串类型映射为内容种类。
func safeContentType(t reflect.Type) (contentType, bool) {
	switch t {
	case reflect.TypeFor[template.HTML]():
		return contentTypeHTML, true
	case reflect.TypeFor[template.CSS]():
		return contentTypeCSS, true
	case reflect.TypeFor[template.HTMLAttr]():
		return contentTypeHTMLAttr, true
	case reflect.TypeFor[template.JS]():
		return contentTypeJS, true
	case reflect.TypeFor[template.JSStr]():
		return contentTypeJSStr, true
	case reflect.TypeFor[template.URL]():
		return contentTypeURL, true
	case reflect.TypeFor[template.Srcset]():
		return contentTypeSrcset, true
	}
	return contentTypePlain, false
}

// appendEscapedTyped applies one escaper to a typed trusted string. | appendEscapedTyped 对带类型信任字符串应用一个转义器。
func appendEscapedTyped(dst []byte, s string, ct contentType, kind escKind) []byte {
	switch kind {
	case escHTML:
		return appendHTMLEscaped(dst, s, ct)
	case escAttr:
		return appendAttrEscaped(dst, s, ct)
	case escRCDATA:
		return appendRCDATAEscaped(dst, s, ct)
	case escNoSpace:
		return appendNoSpaceEscaped(dst, s, ct)
	default:
		return appendEscaped(dst, typedString(ct, s), kind)
	}
}

// typedString reconstructs a trusted string type for the rare escaper that
// needs one. | typedString 为少数需要类型的转义器重建受信字符串类型。
func typedString(ct contentType, s string) any {
	switch ct {
	case contentTypeCSS:
		return template.CSS(s)
	case contentTypeHTML:
		return template.HTML(s)
	case contentTypeHTMLAttr:
		return template.HTMLAttr(s)
	case contentTypeJS:
		return template.JS(s)
	case contentTypeJSStr:
		return template.JSStr(s)
	case contentTypeURL:
		return template.URL(s)
	case contentTypeSrcset:
		return template.Srcset(s)
	}
	return s
}

// truthy reports the text/template truth of v. | truthy 返回 v 在 text/template 中的真值。
func truthy(v any) bool {
	switch x := v.(type) {
	case nil, missingValue, nilValue:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int8:
		return x != 0
	case int16:
		return x != 0
	case int32:
		return x != 0
	case int64:
		return x != 0
	case uint:
		return x != 0
	case uint8:
		return x != 0
	case uint16:
		return x != 0
	case uint32:
		return x != 0
	case uint64:
		return x != 0
	case uintptr:
		return x != 0
	case float32:
		return x != 0
	case float64:
		return x != 0
	case complex64:
		return x != 0
	case complex128:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len() > 0
	case reflect.Bool:
		return rv.Bool()
	case reflect.Complex64, reflect.Complex128:
		return rv.Complex() != 0
	case reflect.Chan, reflect.Func, reflect.Pointer, reflect.UnsafePointer, reflect.Interface:
		return !rv.IsNil()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() != 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint() != 0
	case reflect.Struct:
		return true
	}
	return false
}

// printValue writes the textual representation of a value that did not carry
// an contextual escaper. | printValue 输出未带上下文转义器的值的文本表示。
func printValue(s *execState, v any) error {
	if v == nil || isNilMarker(v) {
		s.out.b = append(s.out.b, "<no value>"...)
		return nil
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Chan, reflect.Func:
		return execErr("can't print value of type %[1]s | 无法打印类型 %[1]s 的值", reflect.TypeOf(v))
	}
	s.out.b = fmt.Append(s.out.b, v)
	return nil
}

// appendWriter adapts a byte slice to io.Writer for fmt.Fprint style helpers.
// | appendWriter 把字节切片适配为 io.Writer，供 fmt.Fprint 风格辅助函数使用。
type appendWriter struct{ b *[]byte }

// Write appends p to the underlying slice. | Write 把 p 追加到底层切片。
func (w appendWriter) Write(p []byte) (int, error) {
	*w.b = append(*w.b, p...)
	return len(p), nil
}
