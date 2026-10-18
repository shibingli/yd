//go:build jsonx_native

package jsonx

import (
	"reflect"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// fieldInfo is one exported struct field. | fieldInfo 是一个可导出的结构体字段。
type fieldInfo struct {
	isZero    func(reflect.Value) bool
	name      string
	typ       reflect.Type
	index     []int
	omitEmpty bool
	quoted    bool
	omitZero  bool
}

// hasUnmarshalMethod reports whether t resolves to a custom unmarshal method, either on the type
// itself or through an addressable pointer. encoding/json gives such methods precedence over the
// ,string option, so the field must not be destringed. | hasUnmarshalMethod 报告 t 是否解析到
// 自定义解码方法（类型自身或经可寻址指针）。encoding/json 让这类方法优先于 ,string 选项，
// 因此该字段不能走 destring 路径。
func hasUnmarshalMethod(t reflect.Type) bool {
	tr := cachedTraits(t)
	if tr.unmarshaler || tr.textUnmarshaler {
		return true
	}
	if t.Kind() != reflect.Pointer {
		p := reflect.PointerTo(t)
		return p.Implements(unmarshalerType) || p.Implements(textUnmarshalerType)
	}
	return false
}

// isZeroer mirrors the interface used by encoding/json's omitzero option. | isZeroer 对应
// encoding/json 的 omitzero 选项所使用的接口。
type isZeroer interface{ IsZero() bool }

var isZeroerType = reflect.TypeOf((*isZeroer)(nil)).Elem()

// makeIsZero returns the same zero predicate encoding/json builds for an omitzero field: a custom
// IsZero method wins over reflect.Value.IsZero, with the exact receiver/pointer/interface handling
// of the standard library. A nil result means reflective IsZero should be used. | makeIsZero 返回
// 与 encoding/json 为 omitzero 字段构建相同的零值判定：自定义 IsZero 方法优先于
// reflect.Value.IsZero，并严格沿用标准库对接收者、指针与接口的处理。返回 nil 表示应使用反射
// 的 IsZero。
func makeIsZero(t reflect.Type) func(reflect.Value) bool {
	switch {
	case t.Kind() == reflect.Interface && t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			// Avoid panics calling IsZero on a nil interface or a non-nil interface holding a
			// nil pointer. | 避免对 nil 接口或持有 nil 指针的非 nil 接口调用 IsZero 时 panic。
			return v.IsNil() ||
				(v.Elem().Kind() == reflect.Pointer && v.Elem().IsNil()) ||
				v.Interface().(isZeroer).IsZero()
		}
	case t.Kind() == reflect.Pointer && t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			return v.IsNil() || v.Interface().(isZeroer).IsZero()
		}
	case t.Implements(isZeroerType):
		return func(v reflect.Value) bool {
			return v.Interface().(isZeroer).IsZero()
		}
	case reflect.PointerTo(t).Implements(isZeroerType):
		return func(v reflect.Value) bool {
			if !v.CanAddr() {
				// Temporarily box v so the pointer receiver can be taken. | 临时装箱 v，以便取得
				// 指针接收者。
				v2 := reflect.New(v.Type()).Elem()
				v2.Set(v)
				v = v2
			}
			return v.Addr().Interface().(isZeroer).IsZero()
		}
	}
	return nil
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
	info := &structInfo{fields: collectFields(t)}
	fieldCache.Store(t, info)
	return info
}

// candidateField is a field found while flattening, before the dominance pass. | candidateField
// 是展平过程中发现的字段，尚未经过支配规则筛选。
type candidateField struct {
	typ       reflect.Type
	name      string
	index     []int
	omitEmpty bool
	omitZero  bool
	quoted    bool
	tagged    bool
}

// collectFields flattens the fields of t using the same breadth-first traversal and dominance
// rules as encoding/json: shallower fields win, a JSON tag wins over no tag at the same depth, and
// two equally ranked fields with the same name cancel each other. | collectFields 使用与
// encoding/json 相同的广度优先遍历与支配规则展平 t 的字段：更浅的字段优先；同一深度下带 JSON
// tag 的字段优先；同名的两个同等级字段互相抵消（该名字不参与编解码）。
func collectFields(t reflect.Type) []fieldInfo {
	type scan struct {
		typ   reflect.Type
		index []int
	}
	current := []scan{}
	next := []scan{{typ: t}}
	// count tracks how many times a type is queued at the current level; such fields are added
	// twice so the annihilation pass sees a duplicate. visited prevents a type reached at an
	// earlier level from contributing again and makes recursive embedding terminate. | count 记录
	// 当前层同一类型被排入的次数；这类字段会被加入两次，使抵消阶段能看到重复项。visited 阻止
	// 更早层级已展开的类型再次贡献字段，并让递归嵌入得以终止。
	var count, nextCount map[reflect.Type]int
	visited := make(map[reflect.Type]bool)
	var fields []candidateField

	for len(next) > 0 {
		current, next = next, current[:0]
		count, nextCount = nextCount, map[reflect.Type]int{}
		for _, f := range current {
			if visited[f.typ] {
				continue
			}
			visited[f.typ] = true
			for i := 0; i < f.typ.NumField(); i++ {
				sf := f.typ.Field(i)
				if sf.Anonymous {
					ft := sf.Type
					if ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if !sf.IsExported() && ft.Kind() != reflect.Struct {
						// Embedded unexported non-struct fields stay hidden, but embedded
						// unexported struct fields may still promote exported subfields. |
						// 未导出的非结构体嵌入字段保持隐藏；未导出的结构体嵌入字段仍可能提升导出子字段。
						continue
					}
				} else if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts := parseTag(tag)
				if !isValidTag(name) {
					name = ""
				}
				index := make([]int, len(f.index)+1)
				copy(index, f.index)
				index[len(f.index)] = i

				// Follow only unnamed pointer types; a named pointer type keeps its pointer
				// kind, exactly like encoding/json. | 只跟随未命名指针类型；具名指针类型保持
				// 指针 kind，与 encoding/json 完全一致。
				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				quoted := hasTagOption(opts, "string") && isQuotableKind(ft.Kind())
				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					tagged := name != ""
					if name == "" {
						name = sf.Name
					}
					field := candidateField{
						typ:       sf.Type,
						name:      name,
						index:     index,
						omitEmpty: hasTagOption(opts, "omitempty"),
						omitZero:  hasTagOption(opts, "omitzero"),
						quoted:    quoted,
						tagged:    tagged,
					}
					fields = append(fields, field)
					if count[f.typ] > 1 {
						// If the same embedded type appeared multiple times at this level, add
						// the field twice so the dominant-field pass drops the name entirely. |
						// 若同一嵌入类型在本层出现多次，则重复加入该字段，使支配规则阶段整体丢弃该名字。
						fields = append(fields, field)
					}
					continue
				}
				nextCount[ft]++
				if nextCount[ft] == 1 {
					next = append(next, scan{typ: ft, index: index})
				}
			}
		}
	}

	sort.Slice(fields, func(i, j int) bool {
		a, b := fields[i], fields[j]
		if a.name != b.name {
			return a.name < b.name
		}
		if len(a.index) != len(b.index) {
			return len(a.index) < len(b.index)
		}
		if a.tagged != b.tagged {
			return a.tagged
		}
		return lessIndex(a.index, b.index)
	})

	out := fields[:0]
	for advance, i := 0, 0; i < len(fields); i += advance {
		name := fields[i].name
		for advance = 1; i+advance < len(fields); advance++ {
			if fields[i+advance].name != name {
				break
			}
		}
		if advance == 1 {
			out = append(out, fields[i])
			continue
		}
		// The group is sorted by depth and then by tag presence, so the first entry dominates
		// unless the two shallowest candidates are equally ranked. | 组内按深度、再按是否带 tag
		// 排序，因此首项即支配者，除非最浅的两个候选等级相同。
		if len(fields[i].index) == len(fields[i+1].index) && fields[i].tagged == fields[i+1].tagged {
			continue
		}
		out = append(out, fields[i])
	}

	// Restore declaration order (depth-first index order), which both encoder and decoder rely on.
	// | 恢复声明顺序（索引序列的深度优先序），编码器与解码器都依赖该顺序。
	sort.Slice(out, func(i, j int) bool { return lessIndex(out[i].index, out[j].index) })
	resolved := make([]fieldInfo, len(out))
	for i, f := range out {
		resolved[i] = fieldInfo{name: f.name, typ: f.typ, index: f.index, omitEmpty: f.omitEmpty, quoted: f.quoted, omitZero: f.omitZero}
		if f.omitZero {
			resolved[i].isZero = makeIsZero(f.typ)
		}
	}
	return resolved
}

// lessIndex reports whether a sorts before b as index paths. | lessIndex 报告索引路径 a 是否排在 b 之前。
func lessIndex(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// hasTagOption reports whether opts contains name as an exact comma-separated option.
// | hasTagOption 报告 opts 是否恰好包含 name 这个逗号分隔的选项。
func hasTagOption(opts, name string) bool {
	for opts != "" {
		var s string
		s, opts, _ = strings.Cut(opts, ",")
		if s == name {
			return true
		}
	}
	return false
}

// isQuotableKind reports whether the ,string option applies to kind k, matching encoding/json.
// | isQuotableKind 报告 ,string 选项是否适用于 kind k，与 encoding/json 一致。
func isQuotableKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return true
	}
	return false
}

// isValidTag reports whether s is a usable JSON field name, matching encoding/json. | isValidTag
// 报告 s 是否是合法的 JSON 字段名，与 encoding/json 一致。
func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
			// Backslash and quote are reserved; other punctuation is allowed. | 反斜杠与引号
			// 保留，其余标点允许出现在 tag 名中。
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
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
