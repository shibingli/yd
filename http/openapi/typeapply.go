package openapi

import (
	"sort"
	"strconv"
	"strings"

	"github.com/shibingli/yd/core/jsonx"
)

// TypeCoverage summarizes how type annotations covered the schemas of a builder. | TypeCoverage
// 汇总类型注解对构建器内 Schema 的覆盖情况。
type TypeCoverage struct {
	// Annotated lists the schema names that carry annotations, sorted. | Annotated 列出带注解的
	// Schema 名，已排序。
	Annotated []string
	// Unannotated lists the names registered by reflection that carry no annotation,
	// sorted. | Unannotated 列出经反射注册但没有任何注解的类型名，已排序。
	Unannotated []string
	// Enums is the number of enum schemas merged into the document. | Enums 是合并进文档的
	// 枚举 Schema 数量。
	Enums int
}

// AddTypeAnnotations merges parsed type annotations into the component schemas. | AddTypeAnnotations
// 把已解析的类型注解合并进组件 Schema。
//
// The merge is annotation first: an annotation overwrites the reflected description and
// example, and field annotations are matched by json tag name, so a reflected field keeps its
// type while gaining description, example, enum, format, minimum, maximum, pattern, nullable,
// deprecated and readOnly. A type that was never registered through RegisterSchema is inserted
// as a schema built from its annotations, which is how enum and variable annotations reach the
// document. A variable @Example fills the referenced schema only when the struct declares no
// example of its own. | 合并以注解优先：注解会覆盖反射生成的 description 与 example；字段注解按 json
// 标签名匹配，因此反射字段在保留类型的同时获得 description、example、enum、format、minimum、
// maximum、pattern、nullable、deprecated 与 readOnly。未经 RegisterSchema 注册的类型会以注解
// 内容生成的 Schema 插入，枚举报与变量注解正是借此进入文档；变量 @Example 仅在对应结构体未声明
// 示例时补位。
func (b *Builder) AddTypeAnnotations(ta *TypeAnnotations) {
	if b == nil || ta == nil {
		return
	}
	if b.doc.Components.Schemas == nil {
		b.doc.Components.Schemas = map[string]*Schema{}
	}
	for _, name := range sortedKeys(ta.Schemas) {
		applySchemaAnnotation(b.ensureSchema(name), ta.Schemas[name])
		b.markAnnotated(name)
	}
	for _, name := range sortedKeys(ta.Enums) {
		applyEnumAnnotation(b.ensureSchema(name), ta.Enums[name])
		b.markAnnotated(name)
		b.markEnum(name)
	}
	for _, name := range sortedKeys(ta.Vars) {
		va := ta.Vars[name]
		target := schemaRefName(va.Schema)
		if target == "" {
			continue
		}
		schema := b.ensureSchema(target)
		// 结构体自带的 @Example 更具体，变量示例只在其缺失时补位；同名变量按名排序，先到者生效。
		// The struct's own @Example is more specific: a variable example only fills the gap; variables are visited in name order, so the first wins.
		if schema.Example != nil {
			continue
		}
		if example := exampleValue(va.Example); example != nil {
			schema.Example = example
		}
	}
}

// RegisterSchemaWithAnnotations registers v under name by reflection and then merges the
// schema and enum annotations that ta carries for that name. | RegisterSchemaWithAnnotations
// 先按反射把 v 注册为 name，再合并 ta 中该名字对应的结构体与枚举报注解。
//
// It is the one call equivalent of RegisterSchema followed by AddTypeAnnotations for a
// single name, and it returns the same "#/components/schemas/<name>" reference. | 它等价于针对
// 单个名字依次调用 RegisterSchema 与 AddTypeAnnotations，并返回同样的
// "#/components/schemas/<name>" 引用。
func RegisterSchemaWithAnnotations(b *Builder, name string, v any, ta *TypeAnnotations) string {
	if b == nil {
		return ""
	}
	ref := b.RegisterSchema(name, v)
	if ta == nil {
		return ref
	}
	if sa, ok := ta.Schemas[name]; ok {
		applySchemaAnnotation(b.ensureSchema(name), sa)
		b.markAnnotated(name)
	}
	if ea, ok := ta.Enums[name]; ok {
		applyEnumAnnotation(b.ensureSchema(name), ea)
		b.markAnnotated(name)
		b.markEnum(name)
	}
	return ref
}

// TypeCoverage reports how type annotations covered the schemas of the builder. | TypeCoverage
// 报告类型注解对构建器内 Schema 的覆盖情况。
//
// Unannotated is derived from the names passed to RegisterSchema, so a builder that only
// merges annotations reports no unannotated type. | Unannotated 由传给 RegisterSchema 的名字推导，
// 因此只合并注解的构建器不会报告未注解类型。
func (b *Builder) TypeCoverage() TypeCoverage {
	cov := TypeCoverage{}
	if b == nil {
		return cov
	}
	annotated := make([]string, 0, len(b.annotated))
	for name := range b.annotated {
		annotated = append(annotated, name)
	}
	cov.Annotated = mergeUniqueSorted(nil, annotated)
	var unannotated []string
	for _, name := range b.registered {
		if !b.annotated[name] {
			unannotated = append(unannotated, name)
		}
	}
	cov.Unannotated = mergeUniqueSorted(nil, unannotated)
	cov.Enums = len(b.enums)
	return cov
}

// ensureSchema returns the component schema stored under name, inserting an empty schema
// when the name was never registered. | ensureSchema 返回 name 下的组件 Schema；该名字从未注册时
// 插入一个空 Schema。
func (b *Builder) ensureSchema(name string) *Schema {
	if schema, ok := b.doc.Components.Schemas[name]; ok && schema != nil {
		return schema
	}
	schema := &Schema{}
	b.doc.Components.Schemas[name] = schema
	return schema
}

// markAnnotated records that a schema received type annotations. | markAnnotated 记录某个 Schema
// 收到过类型注解。
func (b *Builder) markAnnotated(name string) {
	if b.annotated == nil {
		b.annotated = map[string]bool{}
	}
	b.annotated[name] = true
}

// markEnum records that an enum schema was merged into the document. | markEnum 记录某个枚举
// Schema 已合并进文档。
func (b *Builder) markEnum(name string) {
	if b.enums == nil {
		b.enums = map[string]bool{}
	}
	b.enums[name] = true
}

// applySchemaAnnotation merges a struct annotation into its schema. | applySchemaAnnotation
// 把结构体注解合并进对应 Schema。
func applySchemaAnnotation(schema *Schema, sa SchemaAnnotation) {
	if schema == nil {
		return
	}
	if sa.Description != "" {
		schema.Description = sa.Description
	}
	if example := exampleValue(sa.Example); example != nil {
		schema.Example = example
	}
	if len(sa.Fields) == 0 {
		return
	}
	if schema.Type == "" {
		schema.Type = "object"
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*Schema{}
	}
	for _, name := range sortedKeys(sa.Fields) {
		field := schema.Properties[name]
		if field == nil {
			field = &Schema{}
			schema.Properties[name] = field
		}
		applyFieldAnnotation(field, sa.Fields[name])
	}
}

// applyFieldAnnotation merges a field annotation into its property schema. | applyFieldAnnotation
// 把字段注解合并进对应的属性 Schema。
func applyFieldAnnotation(schema *Schema, fa FieldAnnotation) {
	if schema == nil {
		return
	}
	if fa.Description != "" {
		schema.Description = fa.Description
	}
	if example := exampleValue(fa.Example); example != nil {
		schema.Example = example
	}
	if fa.Format != "" {
		schema.Format = fa.Format
	}
	if len(fa.Enum) > 0 {
		schema.Enum = make([]any, 0, len(fa.Enum))
		for _, value := range fa.Enum {
			schema.Enum = append(schema.Enum, enumValue(value, schema.Type))
		}
	}
	if fa.Minimum != nil {
		minimum := *fa.Minimum
		schema.Minimum = &minimum
	}
	if fa.Maximum != nil {
		maximum := *fa.Maximum
		schema.Maximum = &maximum
	}
	if fa.Pattern != "" {
		schema.Pattern = fa.Pattern
	}
	if fa.Nullable {
		schema.Nullable = true
	}
	if fa.Deprecated {
		schema.Deprecated = true
	}
	if fa.ReadOnly {
		schema.ReadOnly = true
	}
}

// applyEnumAnnotation merges a const block annotation into its schema. | applyEnumAnnotation
// 把常量块注解合并进对应 Schema。
func applyEnumAnnotation(schema *Schema, ea EnumAnnotation) {
	if schema == nil {
		return
	}
	if ea.Description != "" {
		schema.Description = ea.Description
	}
	if jsonType := jsonTypeName(ea.Type); jsonType != "" {
		schema.Type = jsonType
	}
	if len(ea.Values) == 0 {
		return
	}
	schema.Enum = make([]any, 0, len(ea.Values))
	for _, value := range ea.Values {
		schema.Enum = append(schema.Enum, enumValue(value.Value, schema.Type))
	}
}

// exampleValue converts a verbatim @Example argument into a JSON value: text that looks like
// JSON keeps its JSON type, anything else stays the raw string. | exampleValue 把原样的
// @Example 参数转换为 JSON 取值：形似 JSON 的文本保留其 JSON 类型，其余保持原字符串。
func exampleValue(raw string) any {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	if !looksLikeJSON(text) {
		return text
	}
	var value any
	if err := jsonx.Unmarshal([]byte(text), &value); err != nil {
		return text
	}
	return value
}

// looksLikeJSON reports whether the first byte may start a JSON value. | looksLikeJSON
// 报告首字节是否可能是 JSON 取值的开头。
func looksLikeJSON(text string) bool {
	switch c := text[0]; {
	case c == '{' || c == '[' || c == '"' || c == '-' || c == '+' || c == '.':
		return true
	case c >= '0' && c <= '9':
		return true
	default:
		return text == "true" || text == "false" || text == "null"
	}
}

// enumValue renders an enum literal with the JSON type of its schema. | enumValue 按 Schema 的
// JSON 类型渲染枚举字面量。
func enumValue(value, jsonType string) any {
	switch jsonType {
	case "integer":
		if n, err := strconv.ParseInt(strings.TrimSpace(value), 0, 64); err == nil {
			return n
		}
	case "number":
		if f, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
			return f
		}
	case "boolean":
		if b, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			return b
		}
	}
	return value
}

// jsonTypeName normalises an annotated @Type token into a JSON Schema type, or "" when the
// token names no JSON type. | jsonTypeName 把注解的 @Type 标记规范化为 JSON Schema 类型，
// 标记不对应任何 JSON 类型时返回 ""。
func jsonTypeName(token string) string {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "string", "text":
		return "string"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "integer":
		return "integer"
	case "float", "float32", "float64", "double", "number":
		return "number"
	case "bool", "boolean":
		return "boolean"
	}
	return ""
}

// schemaRefName normalises an @Schema argument into a component name, accepting both a bare
// name and a full "#/components/schemas/<name>" reference. | schemaRefName 把 @Schema 参数
// 规范化为组件名，裸名与完整的 "#/components/schemas/<name>" 引用均可。
func schemaRefName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, refPrefix)
	return strings.TrimPrefix(name, "*")
}

// sortedKeys returns the sorted keys of a string keyed map. | sortedKeys 返回字符串键 map 的排序键。
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
