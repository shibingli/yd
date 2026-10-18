//go:build jsonx_native

package jsonx

import (
	"reflect"
	"strings"
	"sync"
)

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

// structInfo holds the flattened field list of one struct type. Name lookup is compiled per
// decoder (exact map plus EqualFold scan), so no additional maps are kept here. | structInfo 保存
// 一个结构体类型展平后的字段列表；字段名匹配由各解码器自行编译（精确映射加 EqualFold 扫描），
// 因此这里不额外保存映射。
type structInfo struct {
	fields []fieldInfo
}

var fieldCache sync.Map

func cachedStruct(t reflect.Type) *structInfo {
	if v, ok := fieldCache.Load(t); ok {
		return v.(*structInfo)
	}
	info := &structInfo{fields: collectFields(t, nil)}
	fieldCache.Store(t, info)
	return info
}

func collectFields(t reflect.Type, index []int) []fieldInfo {
	var out []fieldInfo
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts := parseTag(tag)
		idx := append(append([]int(nil), index...), i)
		if f.PkgPath != "" {
			// Unexported fields are ignored except for anonymous struct fields, whose exported
			// subfields are still promoted, matching encoding/json. | 未导出字段一律忽略，但匿名
			// 结构体字段例外：其导出子字段仍会被提升，与 encoding/json 一致。
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if !f.Anonymous || ft.Kind() != reflect.Struct {
				continue
			}
		}
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
