package openapi

import "strings"

// isOpenAPI31 reports whether v selects the OpenAPI 3.1 dialect. | isOpenAPI31 报告 v 是否选择
// OpenAPI 3.1 方言。
func isOpenAPI31(v string) bool {
	return strings.HasPrefix(v, "3.1.")
}

// documentUses31Metadata reports whether the document uses a non-schema construct that only
// OpenAPI 3.1 defines: webhooks, jsonSchemaDialect, info.summary, license.identifier,
// components.pathItems or the mutualTLS security scheme. | documentUses31Metadata 报告文档是否
// 使用了仅 OpenAPI 3.1 定义的非 Schema 构造：webhooks、jsonSchemaDialect、info.summary、
// license.identifier、components.pathItems 或 mutualTLS 安全方案。
func documentUses31Metadata(doc Document) bool {
	if len(doc.Webhooks) > 0 || doc.JSONSchemaDialect != "" ||
		doc.Info.Summary != "" || doc.Info.License.Identifier != "" ||
		len(doc.Components.PathItems) > 0 {
		return true
	}
	for _, scheme := range doc.Components.SecuritySchemes {
		if scheme != nil && scheme.Type == "mutualTLS" {
			return true
		}
	}
	return false
}

// documentSchemaDialect reports whether the schemas of the document use 3.1-only constructs or
// 3.0-only constructs such as nullable and boolean exclusive bounds. | documentSchemaDialect
// 报告文档中的 Schema 是否使用了 3.1 专属构造，或 nullable、布尔开界等 3.0 专属构造。
func documentSchemaDialect(doc Document) (uses31, uses30 bool) {
	visit := func(s *Schema) {
		if s == nil {
			return
		}
		if s.has31Features() {
			uses31 = true
		}
		if s.has30Features() {
			uses30 = true
		}
	}
	for _, schema := range doc.Components.Schemas {
		walkSchema(schema, visit)
	}
	for _, p := range doc.Components.Parameters {
		if p != nil {
			walkParameterSchemas(*p, visit)
		}
	}
	for _, h := range doc.Components.Headers {
		walkHeaderSchemas(h, visit)
	}
	for _, res := range doc.Components.Responses {
		if res == nil {
			continue
		}
		walkContentSchemas(res.Content, visit)
		for _, h := range res.Headers {
			walkHeaderSchemas(h, visit)
		}
	}
	for _, body := range doc.Components.RequestBodies {
		if body != nil {
			walkContentSchemas(body.Content, visit)
		}
	}
	for _, item := range doc.Components.PathItems {
		walkPathItemSchemas(item, visit)
	}
	for _, callback := range doc.Components.Callbacks {
		for _, item := range callback {
			walkPathItemSchemas(item, visit)
		}
	}
	for _, item := range doc.Paths {
		walkPathItemSchemas(item, visit)
	}
	for _, item := range doc.Webhooks {
		walkPathItemSchemas(item, visit)
	}
	return uses31, uses30
}

// has31Features reports whether the schema sets a field that only OpenAPI 3.1 defines. | has31Features
// 报告 Schema 是否设置了仅 OpenAPI 3.1 定义的字段。
func (s *Schema) has31Features() bool {
	if s == nil {
		return false
	}
	return len(s.Types) > 0 || s.Const != nil || len(s.Examples) > 0 ||
		len(s.Defs) > 0 || len(s.PatternProperties) > 0 ||
		len(s.DependentRequired) > 0 || len(s.DependentSchemas) > 0 ||
		s.PropertyNames != nil || s.Contains != nil || s.ContentSchema != nil ||
		len(s.PrefixItems) > 0 || s.If != nil || s.Then != nil || s.Else != nil ||
		s.UnevaluatedProperties != nil || s.UnevaluatedItems != nil ||
		s.ID != "" || s.SchemaDialect != "" || s.Anchor != "" ||
		s.DynamicRef != "" || s.DynamicAnchor != "" || s.Comment != "" ||
		s.ContentEncoding != "" || s.ContentMediaType != "" ||
		s.MinContains != nil || s.MaxContains != nil ||
		s.ExclusiveMinimumValue != nil || s.ExclusiveMaximumValue != nil
}

// has30Features reports whether the schema sets nullable or a boolean exclusive bound, both of
// which OpenAPI 3.1 replaced. | has30Features 报告 Schema 是否设置了 nullable 或布尔开界，二者
// 都已被 OpenAPI 3.1 取代。
func (s *Schema) has30Features() bool {
	if s == nil {
		return false
	}
	return s.Nullable ||
		(s.ExclusiveMinimum && s.ExclusiveMinimumValue == nil) ||
		(s.ExclusiveMaximum && s.ExclusiveMaximumValue == nil)
}

// walkSchema visits s and every schema nested below it. | walkSchema 访问 s 及其下嵌套的每个 Schema。
func walkSchema(s *Schema, fn func(*Schema)) {
	if s == nil {
		return
	}
	fn(s)
	for _, child := range s.Properties {
		walkSchema(child, fn)
	}
	for _, child := range s.Defs {
		walkSchema(child, fn)
	}
	for _, child := range s.PatternProperties {
		walkSchema(child, fn)
	}
	for _, child := range s.DependentSchemas {
		walkSchema(child, fn)
	}
	walkSchema(s.Items, fn)
	walkSchema(s.Not, fn)
	walkSchema(s.PropertyNames, fn)
	walkSchema(s.Contains, fn)
	walkSchema(s.ContentSchema, fn)
	walkSchema(s.If, fn)
	walkSchema(s.Then, fn)
	walkSchema(s.Else, fn)
	for _, child := range s.AllOf {
		walkSchema(child, fn)
	}
	for _, child := range s.OneOf {
		walkSchema(child, fn)
	}
	for _, child := range s.AnyOf {
		walkSchema(child, fn)
	}
	for _, child := range s.PrefixItems {
		walkSchema(child, fn)
	}
	if child, ok := s.AdditionalProperties.(*Schema); ok {
		walkSchema(child, fn)
	}
	if child, ok := s.UnevaluatedProperties.(*Schema); ok {
		walkSchema(child, fn)
	}
	if child, ok := s.UnevaluatedItems.(*Schema); ok {
		walkSchema(child, fn)
	}
}

// walkContentSchemas visits every schema of a media type map and its encodings. | walkContentSchemas
// 访问媒体类型映射及其编码配置中的每个 Schema。
func walkContentSchemas(content map[string]MediaType, fn func(*Schema)) {
	for _, media := range content {
		walkSchema(media.Schema, fn)
		for _, encoding := range media.Encoding {
			for _, header := range encoding.Headers {
				walkHeaderSchemas(header, fn)
			}
		}
	}
}

// walkHeaderSchemas visits the schema and content schemas of a header. | walkHeaderSchemas 访问
// 响应头的 Schema 与 content 中的 Schema。
func walkHeaderSchemas(h *Header, fn func(*Schema)) {
	if h == nil {
		return
	}
	walkSchema(h.Schema, fn)
	walkContentSchemas(h.Content, fn)
}

// walkParameterSchemas visits the schema and content schemas of a parameter. | walkParameterSchemas
// 访问参数的 Schema 与 content 中的 Schema。
func walkParameterSchemas(p Parameter, fn func(*Schema)) {
	walkSchema(p.Schema, fn)
	walkContentSchemas(p.Content, fn)
}

// walkOperationSchemas visits every schema reachable from an operation, including callbacks. | walkOperationSchemas
// 访问操作可达的每个 Schema，包括回调。
func walkOperationSchemas(op *Operation, fn func(*Schema)) {
	if op == nil {
		return
	}
	for _, p := range op.Parameters {
		walkParameterSchemas(p, fn)
	}
	if op.RequestBody != nil {
		walkContentSchemas(op.RequestBody.Content, fn)
	}
	for _, res := range op.Responses {
		walkContentSchemas(res.Content, fn)
		for _, header := range res.Headers {
			walkHeaderSchemas(header, fn)
		}
	}
	for _, callback := range op.Callbacks {
		for _, item := range callback {
			walkPathItemSchemas(item, fn)
		}
	}
}

// walkPathItemSchemas visits every schema reachable from a path item. | walkPathItemSchemas 访问
// 路径项可达的每个 Schema。
func walkPathItemSchemas(item PathItem, fn func(*Schema)) {
	for _, p := range item.Parameters {
		walkParameterSchemas(p, fn)
	}
	for _, entry := range operationEntries(item) {
		walkOperationSchemas(entry.op, fn)
	}
}
