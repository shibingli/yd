package openapi

import (
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"
)

// refPrefix is the prefix of a component schema reference. | refPrefix 是组件 Schema 引用前缀。
const refPrefix = "#/components/schemas/"

// Schema is the subset of JSON Schema used by this package. | Schema 是本包使用的 JSON Schema 子集。
type Schema struct {
	// Type is the JSON type. | Type 为 JSON 类型。
	Type string `json:"type,omitempty"`
	// Format refines the type, for example int64 or date-time. | Format 细化类型，例如 int64 或 date-time。
	Format string `json:"format,omitempty"`
	// Ref is serialized as $ref; when it is set the other fields stay empty. | Ref 序列化为 $ref；当其非空时其余字段保持为空。
	Ref string `json:"$ref,omitempty"`
	// Properties describes object members. | Properties 描述对象成员。
	Properties map[string]*Schema `json:"properties,omitempty"`
	// Items describes array elements. | Items 描述数组元素。
	Items *Schema `json:"items,omitempty"`
	// Required lists mandatory object members, sorted. | Required 列出必填对象成员，已排序。
	Required []string `json:"required,omitempty"`
	// Enum lists the allowed values. | Enum 列出允许取值。
	Enum []any `json:"enum,omitempty"`
	// Example is a sample value. | Example 为示例值。
	Example any `json:"example,omitempty"`
	// Description explains the schema. | Description 说明 Schema 含义。
	Description string `json:"description,omitempty"`
}

// Builder assembles a Document incrementally. | Builder 以增量方式装配 Document。
//
// A Builder is not safe for concurrent use; assemble documents during start-up. | Builder
// 不支持并发使用，请在启动期装配文档。
type Builder struct {
	doc  *Document
	keys map[string]routeRef
}

// routeRef locates a registered operation inside the document. | routeRef 定位文档中已注册的操作。
type routeRef struct {
	path   string
	method string
	params []Parameter
}

// NewBuilder creates a builder for the given info section. | NewBuilder 依据给定的 info 段创建构建器。
func NewBuilder(info Info) *Builder {
	return &Builder{
		doc: &Document{
			OpenAPI:    SpecVersion,
			Info:       info,
			Paths:      map[string]PathItem{},
			Components: Components{Schemas: map[string]*Schema{}},
		},
		keys: map[string]routeRef{},
	}
}

// AddServer appends a server entry. | AddServer 追加一个服务器条目。
func (b *Builder) AddServer(s Server) {
	b.doc.Servers = append(b.doc.Servers, s)
}

// AddRoute registers an operation for a router pattern. | AddRoute 为路由模式注册一个操作。
//
// Path parameters found in the pattern are appended to o.Parameters unless the caller
// already declared a parameter with the same name. | 模式中的路径参数会追加到 o.Parameters，
// 除非调用方已声明同名参数。
func (b *Builder) AddRoute(method, pattern string, o Operation) {
	path, params := pathTemplate(pattern)
	op := mergePathParams(o, params)
	b.add(routeKey(method, path), path, method, op, params)
}

// AddAnnotations merges parsed annotations into previously added routes; annotations
// without a matching route are ignored. | AddAnnotations 把已解析的注解合并进先前添加的路由；
// 没有匹配路由的注解会被忽略。
//
// An annotation replaces the whole operation of its route. | 注解会整体替换对应路由的操作。
func (b *Builder) AddAnnotations(annots map[string]Annotation) {
	for key, a := range annots {
		ref, ok := b.keys[key]
		if !ok {
			continue
		}
		b.add(key, ref.path, ref.method, a.operation(ref.path, ref.params), ref.params)
	}
}

// RegisterSchema derives a JSON Schema from a Go value by reflection, stores it under
// name and returns the "#/components/schemas/<name>" reference. | RegisterSchema 通过反射
// 从 Go 值推导 JSON Schema，以 name 存入组件，并返回 "#/components/schemas/<name>" 引用。
//
// Reflection runs at build time only. | 反射仅在构建期执行。
//
// Derivation rules: | 推导规则：
//
//   - a struct becomes an object, its members named by the json tag (or the Go field
//     name when untagged), `json:"-"` and unexported fields are skipped; | 结构体变为对象，
//     成员名取自 json tag（无 tag 时取 Go 字段名），`json:"-"` 与非导出字段被跳过；
//   - embedded structs are flattened; | 内嵌结构体会被展平；
//   - a field is required when its json tag has no omitempty and its type is not a
//     pointer; | 字段在 json tag 不含 omitempty 且类型不是指针时计为必填；
//   - pointers are unwrapped, slices and arrays become arrays, maps and interfaces
//     become plain objects, time.Time becomes a date-time string and []byte a base64
//     string. | 指针被解引用，切片与数组变为 array，map 与接口变为普通 object，
//     time.Time 变为 date-time 字符串，[]byte 变为 base64 字符串。
func (b *Builder) RegisterSchema(name string, v any) string {
	if b.doc.Components.Schemas == nil {
		b.doc.Components.Schemas = map[string]*Schema{}
	}
	b.doc.Components.Schemas[name] = schemaOfValue(v)
	return refPrefix + name
}

// Build returns the assembled document. | Build 返回装配好的文档。
//
// The document shares its maps with the builder, so it must not be mutated after the
// build completes. | 该文档与构建器共享底层 map，构建完成后不应再修改。
func (b *Builder) Build() *Document {
	return b.doc
}

// add stores an operation and remembers how to reach it again. | add 存入操作并记录再次定位它的方式。
func (b *Builder) add(key, path, method string, op Operation, params []Parameter) {
	item := b.doc.Paths[path]
	if !setOperation(&item, method, &op) {
		return
	}
	b.doc.Paths[path] = item
	b.keys[key] = routeRef{path: path, method: method, params: params}
}

// setOperation stores op under the given HTTP method; unknown methods are rejected. | setOperation
// 把 op 存入指定的 HTTP 方法；未知方法会被拒绝。
func setOperation(item *PathItem, method string, op *Operation) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet:
		item.Get = op
	case http.MethodPost:
		item.Post = op
	case http.MethodPut:
		item.Put = op
	case http.MethodPatch:
		item.Patch = op
	case http.MethodDelete:
		item.Delete = op
	case http.MethodHead:
		item.Head = op
	case http.MethodOptions:
		item.Options = op
	default:
		return false
	}
	return true
}

// routeKey builds the "<METHOD> <template path>" lookup key. | routeKey 构造 "<METHOD> <模板路径>" 形式的查找键。
func routeKey(method, path string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + path
}

// byteSliceType and timeType are the Go types recognised by the schema generator. | byteSliceType
// 与 timeType 是 Schema 生成器识别的 Go 类型。
var (
	byteSliceType = reflect.TypeOf([]byte(nil))
	timeType      = reflect.TypeOf(time.Time{})
)

// schemaOfValue derives a JSON Schema from a Go value. | schemaOfValue 从 Go 值推导 JSON Schema。
func schemaOfValue(v any) *Schema {
	if v == nil {
		return &Schema{}
	}
	return schemaOfType(reflect.TypeOf(v), map[reflect.Type]bool{})
}

// schemaOfType derives a JSON Schema from a Go type; seen breaks reference cycles. | schemaOfType
// 从 Go 类型推导 JSON Schema；seen 用于打断引用环。
func schemaOfType(t reflect.Type, seen map[reflect.Type]bool) *Schema {
	if t == nil {
		return &Schema{}
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
		if t == nil {
			return &Schema{}
		}
	}
	switch t {
	case byteSliceType:
		return &Schema{Type: "string", Format: "byte"}
	case timeType:
		return &Schema{Type: "string", Format: "date-time"}
	}
	switch t.Kind() {
	case reflect.Struct:
		if seen[t] {
			return &Schema{Type: "object"}
		}
		seen[t] = true
		defer delete(seen, t)
		return objectSchema(t, seen)
	case reflect.Map:
		return &Schema{Type: "object"}
	case reflect.Slice, reflect.Array:
		return &Schema{Type: "array", Items: schemaOfType(t.Elem(), seen)}
	case reflect.String:
		return &Schema{Type: "string"}
	case reflect.Bool:
		return &Schema{Type: "boolean"}
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint64:
		return &Schema{Type: "integer", Format: "int64"}
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uintptr:
		return &Schema{Type: "integer", Format: "int32"}
	case reflect.Float32:
		return &Schema{Type: "number", Format: "float"}
	case reflect.Float64:
		return &Schema{Type: "number", Format: "double"}
	default:
		return &Schema{}
	}
}

// objectSchema builds the object schema of a struct type. | objectSchema 构建结构体类型的 object Schema。
func objectSchema(t reflect.Type, seen map[reflect.Type]bool) *Schema {
	s := &Schema{Type: "object", Properties: map[string]*Schema{}}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" && !field.Anonymous {
			continue
		}
		name, opts := jsonFieldName(field)
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" && embeddedStruct(field.Type) {
			flatten(s, schemaOfType(field.Type, seen))
			continue
		}
		if name == "" {
			name = field.Name
		}
		if _, dup := s.Properties[name]; dup {
			continue
		}
		s.Properties[name] = schemaOfType(field.Type, seen)
		if !strings.Contains(opts, "omitempty") && field.Type.Kind() != reflect.Pointer {
			s.Required = append(s.Required, name)
		}
	}
	if len(s.Properties) == 0 {
		s.Properties = nil
	}
	if len(s.Required) == 0 {
		s.Required = nil
	} else {
		sort.Strings(s.Required)
	}
	return s
}

// flatten promotes the members of an embedded struct into the outer object. | flatten
// 把内嵌结构体的成员提升进外层对象。
func flatten(dst, embed *Schema) {
	if embed == nil {
		return
	}
	if dst.Properties == nil {
		dst.Properties = map[string]*Schema{}
	}
	for name, prop := range embed.Properties {
		if _, dup := dst.Properties[name]; dup {
			continue
		}
		dst.Properties[name] = prop
	}
	dst.Required = append(dst.Required, embed.Required...)
}

// embeddedStruct reports whether a field type is a struct or a pointer to a struct
// that should be flattened. | embeddedStruct 报告字段类型是否为应被展平的结构体或结构体指针。
func embeddedStruct(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}

// jsonFieldName returns the encoded name and the options of a struct field. | jsonFieldName
// 返回结构体字段的编码名与选项。
func jsonFieldName(field reflect.StructField) (string, string) {
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return "", ""
	}
	name, opts, _ := strings.Cut(tag, ",")
	return name, opts
}

// mergePathParams appends path parameters that the caller has not declared yet. | mergePathParams
// 追加调用方尚未声明的路径参数。
func mergePathParams(o Operation, params []Parameter) Operation {
	if len(params) == 0 {
		return o
	}
	declared := make(map[string]bool, len(o.Parameters)+len(params))
	out := make([]Parameter, 0, len(o.Parameters)+len(params))
	for _, p := range o.Parameters {
		declared[p.Name] = true
		out = append(out, p)
	}
	for _, p := range params {
		if declared[p.Name] {
			continue
		}
		declared[p.Name] = true
		out = append(out, p)
	}
	o.Parameters = out
	return o
}

// pathParameter builds the required string path parameter of a captured segment. | pathParameter
// 构造捕获段的必填字符串路径参数。
func pathParameter(name string) Parameter {
	return Parameter{Name: name, In: "path", Required: true, Schema: &Schema{Type: "string"}}
}
