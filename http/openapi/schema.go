package openapi

import (
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/shibingli/yd/core/jsonx"
)

// refPrefix is the prefix of a component schema reference. | refPrefix 是组件 Schema 引用前缀。
const refPrefix = "#/components/schemas/"

// Schema is the subset of JSON Schema used by this package, covering the OpenAPI 3.0 Schema
// Object. | Schema 是本包使用的 JSON Schema 子集，覆盖 OpenAPI 3.0 的 Schema 对象。
//
// Composition keywords (allOf, oneOf, anyOf, not) and the remaining validation constraints are
// carried for documents assembled by hand or enriched by type annotations; the reflection
// builder only emits type, format, properties, items and required. | 组合关键字（allOf、oneOf、
// anyOf、not）与其余校验约束用于手工装配或由类型注解补全的文档；反射构建器只产出 type、format、
// properties、items 与 required。
type Schema struct {
	// Example is a sample value, filled by @Example annotations. | Example 为示例值，由 @Example 注解填充。
	Example any `json:"example,omitempty"`
	// Default is the value used when the client omits the field. | Default 为客户端省略字段时使用的取值。
	Default any `json:"default,omitempty"`
	// AdditionalProperties is either a bool or a *Schema describing members not listed in
	// Properties. | AdditionalProperties 取 bool 或 *Schema，描述未列在 Properties 中的成员。
	AdditionalProperties any `json:"additionalProperties,omitempty"`
	// Properties describes object members. | Properties 描述对象成员。
	Properties map[string]*Schema `json:"properties,omitempty"`
	// Items describes array elements. | Items 描述数组元素。
	Items *Schema `json:"items,omitempty"`
	// Not requires the value to fail the given schema. | Not 要求取值不满足给定 Schema。
	Not *Schema `json:"not,omitempty"`
	// Discriminator selects the concrete schema of a polymorphic value. | Discriminator 为多态
	// 取值选择具体 Schema。
	Discriminator *Discriminator `json:"discriminator,omitempty"`
	// XML carries the XML representation hints of the schema. | XML 承载 Schema 的 XML 表示提示。
	XML *XML `json:"xml,omitempty"`
	// ExternalDocs points at the external documentation of the schema. | ExternalDocs 指向该 Schema
	// 的外部文档。
	ExternalDocs *ExternalDocs `json:"externalDocs,omitempty"`
	// Const is the only allowed value, an OpenAPI 3.1 / JSON Schema addition. | Const 为唯一允许
	// 的取值，是 OpenAPI 3.1 / JSON Schema 的新增内容。
	Const any `json:"const,omitempty"`
	// Examples lists sample values, an OpenAPI 3.1 addition; Example remains the 3.0 form. | Examples
	// 列出示例值，是 OpenAPI 3.1 的新增内容；Example 仍为 3.0 形式。
	Examples []any `json:"examples,omitempty"`
	// Types is emitted as the JSON Schema 2020-12 type array when non-empty, an OpenAPI 3.1
	// addition that replaces Type; it is not serialized under its own name. | Types 非空时以
	// JSON Schema 2020-12 的类型数组形式序列化为 type，是 OpenAPI 3.1 中取代 Type 的新增内容；
	// 它不会以自身名字序列化。
	Types []string `json:"-"`
	// Defs maps a name to a reusable subschema ($defs), a JSON Schema 2020-12 keyword. | Defs 把
	// 名字映射到可复用子 Schema（$defs），是 JSON Schema 2020-12 关键字。
	Defs map[string]*Schema `json:"$defs,omitempty"`
	// PatternProperties applies a schema to members whose name matches the pattern. | PatternProperties
	// 把 Schema 应用于名字匹配模式的成员。
	PatternProperties map[string]*Schema `json:"patternProperties,omitempty"`
	// DependentRequired lists members required when a given member is present. | DependentRequired
	// 列出某个成员存在时要求的其它成员。
	DependentRequired map[string][]string `json:"dependentRequired,omitempty"`
	// DependentSchemas applies schemas conditionally on member presence. | DependentSchemas 依据
	// 成员是否存在条件性地应用 Schema。
	DependentSchemas map[string]*Schema `json:"dependentSchemas,omitempty"`
	// PropertyNames constrains every member name. | PropertyNames 约束每个成员名。
	PropertyNames *Schema `json:"propertyNames,omitempty"`
	// Contains requires at least one array element to match. | Contains 要求数组至少一个元素匹配。
	Contains *Schema `json:"contains,omitempty"`
	// ContentSchema describes decoded string content. | ContentSchema 描述解码后的字符串内容。
	ContentSchema *Schema `json:"contentSchema,omitempty"`
	// PrefixItems describes the leading array elements positionally. | PrefixItems 按位置描述数组
	// 开头元素。
	PrefixItems []*Schema `json:"prefixItems,omitempty"`
	// If, Then and Else express conditional validation. | If、Then 与 Else 表达条件校验。
	If   *Schema `json:"if,omitempty"`
	Then *Schema `json:"then,omitempty"`
	Else *Schema `json:"else,omitempty"`
	// UnevaluatedProperties is either a bool or a *Schema applied to members not evaluated by
	// other subschemas. | UnevaluatedProperties 取 bool 或 *Schema，应用于其它子 Schema 未评估的
	// 成员。
	UnevaluatedProperties any `json:"unevaluatedProperties,omitempty"`
	// UnevaluatedItems is either a bool or a *Schema applied to array items not evaluated by
	// other subschemas. | UnevaluatedItems 取 bool 或 *Schema，应用于其它子 Schema 未评估的数组元素。
	UnevaluatedItems any `json:"unevaluatedItems,omitempty"`
	// ID is the schema identifier ($id). | ID 为 Schema 标识（$id）。
	ID string `json:"$id,omitempty"`
	// SchemaDialect selects the JSON Schema dialect of this subschema ($schema). | SchemaDialect
	// 选择该子 Schema 的 JSON Schema 方言（$schema）。
	SchemaDialect string `json:"$schema,omitempty"`
	// Anchor declares a plain-name fragment identifier ($anchor). | Anchor 声明纯名字片段标识（$anchor）。
	Anchor string `json:"$anchor,omitempty"`
	// DynamicRef references a dynamically scoped schema ($dynamicRef). | DynamicRef 引用动态作用域
	// Schema（$dynamicRef）。
	DynamicRef string `json:"$dynamicRef,omitempty"`
	// DynamicAnchor declares a dynamic fragment identifier ($dynamicAnchor). | DynamicAnchor 声明
	// 动态片段标识（$dynamicAnchor）。
	DynamicAnchor string `json:"$dynamicAnchor,omitempty"`
	// Comment is an implementation note ($comment). | Comment 为实现注记（$comment）。
	Comment string `json:"$comment,omitempty"`
	// ContentEncoding declares the string content encoding, for example base64. | ContentEncoding
	// 声明字符串内容编码，例如 base64。
	ContentEncoding string `json:"contentEncoding,omitempty"`
	// ContentMediaType declares the string content media type. | ContentMediaType 声明字符串内容
	// 媒体类型。
	ContentMediaType string `json:"contentMediaType,omitempty"`
	// MinContains is the minimum number of matching array elements. | MinContains 为匹配数组元素的
	// 最小数量。
	MinContains *int `json:"minContains,omitempty"`
	// MaxContains is the maximum number of matching array elements. | MaxContains 为匹配数组元素的
	// 最大数量。
	MaxContains *int `json:"maxContains,omitempty"`
	// AllOf requires the value to match every schema. | AllOf 要求取值满足每一个 Schema。
	AllOf []*Schema `json:"allOf,omitempty"`
	// OneOf requires the value to match exactly one schema. | OneOf 要求取值恰好满足一个 Schema。
	OneOf []*Schema `json:"oneOf,omitempty"`
	// AnyOf requires the value to match at least one schema. | AnyOf 要求取值至少满足一个 Schema。
	AnyOf []*Schema `json:"anyOf,omitempty"`
	// Minimum is the inclusive lower bound of a numeric value, filled by @Minimum. | Minimum
	// 为数字取值的闭下界，由 @Minimum 填充。
	Minimum *float64 `json:"minimum,omitempty"`
	// Maximum is the inclusive upper bound of a numeric value, filled by @Maximum. | Maximum
	// 为数字取值的闭上界，由 @Maximum 填充。
	Maximum *float64 `json:"maximum,omitempty"`
	// MultipleOf requires the numeric value to be a multiple of it. | MultipleOf 要求数字取值是其倍数。
	MultipleOf *float64 `json:"multipleOf,omitempty"`
	// MinLength is the minimum string length. | MinLength 为字符串最小长度。
	MinLength *int `json:"minLength,omitempty"`
	// MaxLength is the maximum string length. | MaxLength 为字符串最大长度。
	MaxLength *int `json:"maxLength,omitempty"`
	// MinItems is the minimum array length. | MinItems 为数组最小长度。
	MinItems *int `json:"minItems,omitempty"`
	// MaxItems is the maximum array length. | MaxItems 为数组最大长度。
	MaxItems *int `json:"maxItems,omitempty"`
	// MinProperties is the minimum object member count. | MinProperties 为对象成员最小数量。
	MinProperties *int `json:"minProperties,omitempty"`
	// MaxProperties is the maximum object member count. | MaxProperties 为对象成员最大数量。
	MaxProperties *int `json:"maxProperties,omitempty"`
	// Type is the JSON type. | Type 为 JSON 类型。
	Type string `json:"type,omitempty"`
	// Format refines the type, for example int64 or date-time. | Format 细化类型，例如 int64 或 date-time。
	Format string `json:"format,omitempty"`
	// Ref is serialized as $ref; when it is set the other fields stay empty. | Ref 序列化为 $ref；当其非空时其余字段保持为空。
	Ref string `json:"$ref,omitempty"`
	// Title is a short human readable name. | Title 为简短的人类可读名称。
	Title string `json:"title,omitempty"`
	// Description explains the schema, filled by @Description annotations or doc prose. | Description
	// 说明 Schema 含义，由 @Description 注解或 doc 正文填充。
	Description string `json:"description,omitempty"`
	// Pattern is the regular expression a string value must match, filled by @Pattern. | Pattern
	// 为字符串取值必须匹配的正则表达式，由 @Pattern 填充。
	Pattern string `json:"pattern,omitempty"`
	// Required lists mandatory object members, sorted. | Required 列出必填对象成员，已排序。
	Required []string `json:"required,omitempty"`
	// Enum lists the allowed values. | Enum 列出允许取值。
	Enum []any `json:"enum,omitempty"`
	// Nullable allows an explicit null, filled by @Nullable. | Nullable 允许显式 null，由 @Nullable 填充。
	Nullable bool `json:"nullable,omitempty"`
	// Deprecated marks the value as deprecated, filled by @Deprecated. | Deprecated 标记取值已废弃，由 @Deprecated 填充。
	Deprecated bool `json:"deprecated,omitempty"`
	// ReadOnly marks a value the server owns, filled by @ReadOnly. | ReadOnly 标记由服务端拥有的取值，由 @ReadOnly 填充。
	ReadOnly bool `json:"readOnly,omitempty"`
	// WriteOnly marks a value the server accepts but never returns. | WriteOnly 标记服务端接受但
	// 从不返回的取值。
	WriteOnly bool `json:"writeOnly,omitempty"`
	// ExclusiveMinimum makes Minimum exclusive. | ExclusiveMinimum 使 Minimum 成为开下界。
	ExclusiveMinimum bool `json:"exclusiveMinimum,omitempty"`
	// ExclusiveMaximum makes Maximum exclusive. | ExclusiveMaximum 使 Maximum 成为闭上界之外的
	// 开上界。
	ExclusiveMaximum bool `json:"exclusiveMaximum,omitempty"`
	// UniqueItems requires array elements to be unique. | UniqueItems 要求数组元素互不相同。
	UniqueItems bool `json:"uniqueItems,omitempty"`
}

// Discriminator selects the concrete schema of a polymorphic value. | Discriminator 为多态取值选择
// 具体 Schema。
type Discriminator struct {
	// Mapping maps a discriminator value to a schema name or reference. | Mapping 把鉴别值映射到
	// Schema 名或引用。
	Mapping map[string]string `json:"mapping,omitempty"`
	// PropertyName is the property carrying the discriminator value. | PropertyName 为承载鉴别值的属性名。
	PropertyName string `json:"propertyName"`
}

// XML carries the XML representation hints of a schema. | XML 承载 Schema 的 XML 表示提示。
type XML struct {
	// Name is the element name. | Name 为元素名。
	Name string `json:"name,omitempty"`
	// Namespace is the element namespace. | Namespace 为元素命名空间。
	Namespace string `json:"namespace,omitempty"`
	// Prefix is the element namespace prefix. | Prefix 为元素命名空间前缀。
	Prefix string `json:"prefix,omitempty"`
	// Attribute marks the property as an attribute instead of an element. | Attribute 标记该属性
	// 为特性而不是元素。
	Attribute bool `json:"attribute,omitempty"`
	// Wrapped marks an array as a wrapped element. | Wrapped 标记数组为包裹元素。
	Wrapped bool `json:"wrapped,omitempty"`
}

// MarshalJSON implements json.Marshaler so that Types can be emitted as the JSON Schema 2020-12
// type array without changing the 3.0 Type field. | MarshalJSON 实现 json.Marshaler，使 Types 能
// 以 JSON Schema 2020-12 的类型数组序列化，同时不改变 3.0 的 Type 字段。
func (s Schema) MarshalJSON() ([]byte, error) {
	type wire struct {
		Example               any                 `json:"example,omitempty"`
		Default               any                 `json:"default,omitempty"`
		AdditionalProperties  any                 `json:"additionalProperties,omitempty"`
		Const                 any                 `json:"const,omitempty"`
		Properties            map[string]*Schema  `json:"properties,omitempty"`
		Items                 *Schema             `json:"items,omitempty"`
		Not                   *Schema             `json:"not,omitempty"`
		Discriminator         *Discriminator      `json:"discriminator,omitempty"`
		XML                   *XML                `json:"xml,omitempty"`
		ExternalDocs          *ExternalDocs       `json:"externalDocs,omitempty"`
		AllOf                 []*Schema           `json:"allOf,omitempty"`
		OneOf                 []*Schema           `json:"oneOf,omitempty"`
		AnyOf                 []*Schema           `json:"anyOf,omitempty"`
		Examples              []any               `json:"examples,omitempty"`
		Defs                  map[string]*Schema  `json:"$defs,omitempty"`
		PatternProperties     map[string]*Schema  `json:"patternProperties,omitempty"`
		DependentRequired     map[string][]string `json:"dependentRequired,omitempty"`
		DependentSchemas      map[string]*Schema  `json:"dependentSchemas,omitempty"`
		PropertyNames         *Schema             `json:"propertyNames,omitempty"`
		Contains              *Schema             `json:"contains,omitempty"`
		ContentSchema         *Schema             `json:"contentSchema,omitempty"`
		PrefixItems           []*Schema           `json:"prefixItems,omitempty"`
		If                    *Schema             `json:"if,omitempty"`
		Then                  *Schema             `json:"then,omitempty"`
		Else                  *Schema             `json:"else,omitempty"`
		UnevaluatedProperties any                 `json:"unevaluatedProperties,omitempty"`
		UnevaluatedItems      any                 `json:"unevaluatedItems,omitempty"`
		Type                  any                 `json:"type,omitempty"`
		Minimum               *float64            `json:"minimum,omitempty"`
		Maximum               *float64            `json:"maximum,omitempty"`
		MultipleOf            *float64            `json:"multipleOf,omitempty"`
		MinLength             *int                `json:"minLength,omitempty"`
		MaxLength             *int                `json:"maxLength,omitempty"`
		MinItems              *int                `json:"minItems,omitempty"`
		MaxItems              *int                `json:"maxItems,omitempty"`
		MinProperties         *int                `json:"minProperties,omitempty"`
		MaxProperties         *int                `json:"maxProperties,omitempty"`
		MinContains           *int                `json:"minContains,omitempty"`
		MaxContains           *int                `json:"maxContains,omitempty"`
		Format                string              `json:"format,omitempty"`
		Ref                   string              `json:"$ref,omitempty"`
		ID                    string              `json:"$id,omitempty"`
		SchemaDialect         string              `json:"$schema,omitempty"`
		Anchor                string              `json:"$anchor,omitempty"`
		DynamicRef            string              `json:"$dynamicRef,omitempty"`
		DynamicAnchor         string              `json:"$dynamicAnchor,omitempty"`
		Comment               string              `json:"$comment,omitempty"`
		ContentEncoding       string              `json:"contentEncoding,omitempty"`
		ContentMediaType      string              `json:"contentMediaType,omitempty"`
		Title                 string              `json:"title,omitempty"`
		Description           string              `json:"description,omitempty"`
		Pattern               string              `json:"pattern,omitempty"`
		Required              []string            `json:"required,omitempty"`
		Enum                  []any               `json:"enum,omitempty"`
		Nullable              bool                `json:"nullable,omitempty"`
		Deprecated            bool                `json:"deprecated,omitempty"`
		ReadOnly              bool                `json:"readOnly,omitempty"`
		WriteOnly             bool                `json:"writeOnly,omitempty"`
		ExclusiveMinimum      bool                `json:"exclusiveMinimum,omitempty"`
		ExclusiveMaximum      bool                `json:"exclusiveMaximum,omitempty"`
		UniqueItems           bool                `json:"uniqueItems,omitempty"`
	}
	w := wire{
		Example:               s.Example,
		Default:               s.Default,
		AdditionalProperties:  s.AdditionalProperties,
		Const:                 s.Const,
		Properties:            s.Properties,
		Items:                 s.Items,
		Not:                   s.Not,
		Discriminator:         s.Discriminator,
		XML:                   s.XML,
		ExternalDocs:          s.ExternalDocs,
		AllOf:                 s.AllOf,
		OneOf:                 s.OneOf,
		AnyOf:                 s.AnyOf,
		Examples:              s.Examples,
		Defs:                  s.Defs,
		PatternProperties:     s.PatternProperties,
		DependentRequired:     s.DependentRequired,
		DependentSchemas:      s.DependentSchemas,
		PropertyNames:         s.PropertyNames,
		Contains:              s.Contains,
		ContentSchema:         s.ContentSchema,
		PrefixItems:           s.PrefixItems,
		If:                    s.If,
		Then:                  s.Then,
		Else:                  s.Else,
		UnevaluatedProperties: s.UnevaluatedProperties,
		UnevaluatedItems:      s.UnevaluatedItems,
		Minimum:               s.Minimum,
		Maximum:               s.Maximum,
		MultipleOf:            s.MultipleOf,
		MinLength:             s.MinLength,
		MaxLength:             s.MaxLength,
		MinItems:              s.MinItems,
		MaxItems:              s.MaxItems,
		MinProperties:         s.MinProperties,
		MaxProperties:         s.MaxProperties,
		MinContains:           s.MinContains,
		MaxContains:           s.MaxContains,
		Format:                s.Format,
		Ref:                   s.Ref,
		ID:                    s.ID,
		SchemaDialect:         s.SchemaDialect,
		Anchor:                s.Anchor,
		DynamicRef:            s.DynamicRef,
		DynamicAnchor:         s.DynamicAnchor,
		Comment:               s.Comment,
		ContentEncoding:       s.ContentEncoding,
		ContentMediaType:      s.ContentMediaType,
		Title:                 s.Title,
		Description:           s.Description,
		Pattern:               s.Pattern,
		Required:              s.Required,
		Enum:                  s.Enum,
		Nullable:              s.Nullable,
		Deprecated:            s.Deprecated,
		ReadOnly:              s.ReadOnly,
		WriteOnly:             s.WriteOnly,
		ExclusiveMinimum:      s.ExclusiveMinimum,
		ExclusiveMaximum:      s.ExclusiveMaximum,
		UniqueItems:           s.UniqueItems,
	}
	if len(s.Types) > 0 {
		w.Type = s.Types
	} else if s.Type != "" {
		w.Type = s.Type
	}
	return jsonx.Marshal(w)
}

// Builder assembles a Document incrementally. | Builder 以增量方式装配 Document。
//
// A Builder is not safe for concurrent use; assemble documents during start-up. | Builder
// 不支持并发使用，请在启动期装配文档。
type Builder struct {
	doc  *Document
	keys map[string]routeRef
	// annotated records the names that received type annotations. | annotated 记录收到类型注解的名字。
	annotated map[string]bool
	// enums records the names of the enum schemas merged into the document. | enums 记录合并进文档的枚举 Schema 名。
	enums map[string]bool
	// registered lists the names passed to RegisterSchema, in call order. | registered 按调用顺序
	// 记录传给 RegisterSchema 的名字。
	registered []string
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
			OpenAPI:  SpecVersion,
			Info:     info,
			Paths:    map[string]PathItem{},
			Webhooks: map[string]PathItem{},
			Components: Components{
				// Every component map is initialized so callers can assign into them directly;
				// the empty maps stay out of the serialized document through omitempty. | 所有组件
				// 映射都会初始化，调用方可以直接赋值；空映射经 omitempty 不会出现在序列化结果中。
				Schemas:         map[string]*Schema{},
				SecuritySchemes: map[string]*SecurityScheme{},
				Parameters:      map[string]*Parameter{},
				Responses:       map[string]*Response{},
				RequestBodies:   map[string]*RequestBody{},
				Headers:         map[string]*Header{},
				Examples:        map[string]Example{},
				Links:           map[string]*Link{},
				Callbacks:       map[string]Callback{},
				PathItems:       map[string]PathItem{},
			},
		},
		keys:      map[string]routeRef{},
		annotated: map[string]bool{},
		enums:     map[string]bool{},
	}
}

// AddServer appends a server entry. | AddServer 追加一个服务器条目。
func (b *Builder) AddServer(s Server) {
	if b == nil {
		return
	}
	b.doc.AddServer(s)
}

// AddTag appends a tag description. | AddTag 追加一个标签说明。
func (b *Builder) AddTag(t Tag) {
	if b == nil {
		return
	}
	b.doc.AddTag(t)
}

// AddSecurityScheme stores a security scheme under name; empty names are ignored. | AddSecurityScheme
// 把安全方案以 name 存入组件；空名字会被忽略。
func (b *Builder) AddSecurityScheme(name string, s SecurityScheme) {
	if b == nil {
		return
	}
	b.doc.AddSecurityScheme(name, s)
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
//   - pointers are unwrapped, slices and arrays become arrays, maps become objects with a
//     typed additionalProperties, interfaces become plain objects, time.Time becomes a
//     date-time string and []byte a base64 string. | 指针被解引用，切片与数组变为 array，map
//     变为带类型 additionalProperties 的 object，接口变为普通 object，time.Time 变为
//     date-time 字符串，[]byte 变为 base64 字符串。
//
// The registered name is remembered so that TypeCoverage can report reflected types that
// carry no annotation; RegisterSchemaWithAnnotations and AddTypeAnnotations then enrich the
// schema with the type level annotations. | 注册名会被记录，以便 TypeCoverage 报告没有注解的反射
// 类型；随后可由 RegisterSchemaWithAnnotations 与 AddTypeAnnotations 用类型级注解补全该 Schema。
func (b *Builder) RegisterSchema(name string, v any) string {
	if b.doc.Components.Schemas == nil {
		b.doc.Components.Schemas = map[string]*Schema{}
	}
	b.doc.Components.Schemas[name] = schemaOfValue(v)
	b.registered = append(b.registered, name)
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
	case http.MethodTrace:
		item.Trace = op
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
		return &Schema{Type: "object", AdditionalProperties: schemaOfType(t.Elem(), seen)}
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
