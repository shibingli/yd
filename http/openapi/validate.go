package openapi

import (
	"errors"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// ErrInvalidDocument reports a document that violates the OpenAPI 3.0 or 3.1 specification. | ErrInvalidDocument
// 表示文档违反 OpenAPI 3.0 或 3.1 规范。
var ErrInvalidDocument = errx.New("openapi: invalid document | openapi：非法的文档")

// Validate checks the document against the structural rules of OpenAPI 3.0 and 3.1 and returns
// every problem it finds, joined with errors.Join; errors.Is(err, ErrInvalidDocument) matches
// every problem. It is meant for start-up wiring, not for the request path. | Validate 按
// OpenAPI 3.0 与 3.1 的结构规则检查文档，并以 errors.Join 返回发现的全部问题；每个问题都能用
// errors.Is(err, ErrInvalidDocument) 匹配。它用于启动期装配，而不是请求热路径。
//
// Checked rules: info.title and info.version, absolute path templates, one response or more per
// operation, valid status codes and media types, path parameters declared and required, unique
// operationId values, declared security schemes with their type specific fields, resolvable
// component schema references, and supported parameter locations. | 检查规则：info.title 与
// info.version、以 "/" 开头的路径模板、每个操作至少一个响应、合法的状态码与媒体类型、路径参数
// 已声明且必填、operationId 唯一、安全方案已声明且类型专属字段齐全、组件 Schema 引用可解析，
// 以及参数位置受支持。
//
// A document assembled entirely by FromRouterWithAnnotations may legitimately carry dangling
// schema references when the handler annotations name types that were never registered; that is
// reported as an error here rather than being hidden. | 完全由 FromRouterWithAnnotations 装配的
// 文档在处理器注解引用了从未注册的类型时，确实可能带有悬空 Schema 引用；这里会如实报错，而不是
// 隐藏它。
func (d *Document) Validate() error {
	if d == nil {
		return errx.Detail(ErrInvalidDocument, "document is nil | 文档为 nil")
	}
	doc := d.normalized()
	var problems []error
	add := func(format string, args ...any) {
		problems = append(problems, errx.Detail(ErrInvalidDocument, format, args...))
	}

	if strings.TrimSpace(doc.Info.Title) == "" {
		add("info.title is required | info.title 为必填项")
	}
	if strings.TrimSpace(doc.Info.Version) == "" {
		add("info.version is required | info.version 为必填项")
	}
	if !supportedSpecVersion(doc.OpenAPI) {
		add("unsupported openapi version %q, want 3.0.x or 3.1.x | 不支持的 openapi 版本 %q，应为 3.0.x 或 3.1.x",
			doc.OpenAPI, doc.OpenAPI)
	}
	uses31, uses30 := documentSchemaDialect(doc)
	if isOpenAPI31(doc.OpenAPI) {
		if uses30 {
			add("document uses OpenAPI 3.0 schema keywords (nullable or boolean exclusive bounds) under %q; use Types and numeric exclusive bounds | 文档在 %q 下使用了 OpenAPI 3.0 Schema 关键字（nullable 或布尔开界）；请改用 Types 与数值开界",
				doc.OpenAPI, doc.OpenAPI)
		}
	} else if uses31 || documentUses31Metadata(doc) {
		add("document uses OpenAPI 3.1 features but declares %q; set openapi to 3.1.x | 文档使用了 OpenAPI 3.1 特性但声明版本为 %q；请把 openapi 设为 3.1.x",
			doc.OpenAPI, doc.OpenAPI)
	}
	validateServers(doc.Servers, "document", add)
	if doc.ExternalDocs != nil && strings.TrimSpace(doc.ExternalDocs.URL) == "" {
		add("externalDocs.url is required | externalDocs.url 为必填项")
	}
	for _, tag := range doc.Tags {
		if strings.TrimSpace(tag.Name) == "" {
			add("tag name is required | 标签名不能为空")
		}
	}

	for _, name := range sortedKeys(doc.Components.SecuritySchemes) {
		validateSecurityScheme(name, doc.Components.SecuritySchemes[name], add)
	}
	checkSecurity(doc.Security, "document", doc, add)

	refs := make([]string, 0, len(doc.Components.Schemas))
	for _, name := range sortedKeys(doc.Components.Schemas) {
		schema := doc.Components.Schemas[name]
		if schema == nil {
			add("component schema %q is nil | 组件 Schema %q 为 nil", name, name)
			continue
		}
		collectSchemaRefs(schema, &refs)
	}
	for _, name := range sortedKeys(doc.Components.Parameters) {
		p := doc.Components.Parameters[name]
		if p == nil {
			add("component parameter %q is nil | 组件参数 %q 为 nil", name, name)
			continue
		}
		validateParameter(*p, "component parameter "+name, add)
		collectParameterRefs(*p, &refs)
	}
	for _, name := range sortedKeys(doc.Components.Headers) {
		collectHeaderRefs(doc.Components.Headers[name], &refs)
	}
	for _, name := range sortedKeys(doc.Components.Responses) {
		res := doc.Components.Responses[name]
		if res == nil {
			add("component response %q is nil | 组件响应 %q 为 nil", name, name)
			continue
		}
		if res.Ref == "" && strings.TrimSpace(res.Description) == "" {
			add("component response %q has no description | 组件响应 %q 缺少说明", name, name)
		}
		collectContentRefs(res.Content, &refs)
		for _, header := range sortedKeys(res.Headers) {
			collectHeaderRefs(res.Headers[header], &refs)
		}
	}
	for _, name := range sortedKeys(doc.Components.RequestBodies) {
		body := doc.Components.RequestBodies[name]
		if body == nil {
			add("component request body %q is nil | 组件请求体 %q 为 nil", name, name)
			continue
		}
		collectContentRefs(body.Content, &refs)
	}
	for _, name := range sortedKeys(doc.Webhooks) {
		collectPathItemRefs(doc.Webhooks[name], &refs)
	}
	for _, name := range sortedKeys(doc.Components.PathItems) {
		collectPathItemRefs(doc.Components.PathItems[name], &refs)
	}
	for _, name := range sortedKeys(doc.Components.Callbacks) {
		callback := doc.Components.Callbacks[name]
		for _, expression := range sortedKeys(callback) {
			collectPathItemRefs(callback[expression], &refs)
		}
	}

	operationIDs := make(map[string]string, len(doc.Paths))

	for _, path := range sortedKeys(doc.Paths) {
		item := doc.Paths[path]
		if !strings.HasPrefix(path, "/") {
			add("path %q must start with '/' | 路径 %q 必须以 '/' 开头", path, path)
		}
		validateServers(item.Servers, "path item "+path, add)
		pathParams := pathParamNames(path)
		itemParams := make(map[string]bool, len(item.Parameters))
		for _, p := range item.Parameters {
			validateParameter(p, "path item "+path, add)
			if p.In == "path" {
				itemParams[p.Name] = true
			}
			collectParameterRefs(p, &refs)
		}
		for _, entry := range operationEntries(item) {
			op := entry.op
			if op == nil {
				continue
			}
			where := entry.method + " " + path
			if op.OperationID != "" {
				if previous, dup := operationIDs[op.OperationID]; dup {
					add("duplicate operationId %q on %s and %s | operationId %q 在 %s 与 %s 上重复",
						op.OperationID, previous, where, op.OperationID, previous, where)
				} else {
					operationIDs[op.OperationID] = where
				}
			}
			if len(op.Responses) == 0 {
				add("operation %s has no responses | 操作 %s 缺少响应", where, where)
			}
			validateServers(op.Servers, "operation "+where, add)
			declared := make(map[string]bool, len(itemParams)+len(op.Parameters))
			for name := range itemParams {
				declared[name] = true
			}
			for _, p := range op.Parameters {
				validateParameter(p, "operation "+where, add)
				if p.In == "path" {
					declared[p.Name] = true
				}
				collectParameterRefs(p, &refs)
			}
			for _, name := range pathParams {
				if !declared[name] {
					add("operation %s does not declare path parameter %q | 操作 %s 未声明路径参数 %q",
						where, name, where, name)
				}
			}
			for name := range declared {
				if !containsString(pathParams, name) {
					add("operation %s declares path parameter %q that is absent from the template | 操作 %s 声明了模板中不存在的路径参数 %q",
						where, name, where, name)
				}
			}
			for _, code := range sortedKeys(op.Responses) {
				res := op.Responses[code]
				if !validResponseCode(code) {
					add("operation %s uses invalid response code %q | 操作 %s 使用了非法的响应码 %q",
						where, code, where, code)
				}
				if res.Ref == "" && strings.TrimSpace(res.Description) == "" {
					add("response %s of %s has no description | %s 的响应 %s 缺少说明", code, where, where, code)
				}
				for _, media := range sortedKeys(res.Content) {
					mt := res.Content[media]
					if strings.TrimSpace(media) == "" {
						add("response %s of %s has an empty media type | %s 的响应 %s 含空媒体类型",
							code, where, where, code)
					}
					collectSchemaRefs(mt.Schema, &refs)
					for _, prop := range sortedKeys(mt.Encoding) {
						enc := mt.Encoding[prop]
						for _, header := range sortedKeys(enc.Headers) {
							collectHeaderRefs(enc.Headers[header], &refs)
						}
					}
				}
				for _, name := range sortedKeys(res.Headers) {
					if strings.TrimSpace(name) == "" {
						add("response %s of %s has an empty header name | %s 的响应 %s 含空响应头名",
							code, where, where, code)
					}
					collectHeaderRefs(res.Headers[name], &refs)
				}
			}
			if op.RequestBody != nil {
				if op.RequestBody.Ref == "" && len(op.RequestBody.Content) == 0 {
					add("request body of %s has no content | %s 的请求体缺少 content", where, where)
				}
				for media, mt := range op.RequestBody.Content {
					if strings.TrimSpace(media) == "" {
						add("request body of %s has an empty media type | %s 的请求体含空媒体类型", where, where)
					}
					collectSchemaRefs(mt.Schema, &refs)
					for _, prop := range sortedKeys(mt.Encoding) {
						enc := mt.Encoding[prop]
						for _, header := range sortedKeys(enc.Headers) {
							collectHeaderRefs(enc.Headers[header], &refs)
						}
					}
				}
			}
			checkSecurity(op.Security, where, doc, add)
		}
	}

	for _, ref := range refs {
		if name := strings.TrimPrefix(ref, refPrefix); name != ref {
			if _, ok := doc.Components.Schemas[name]; !ok {
				add("unresolved schema reference %q | 无法解析的 Schema 引用 %q", ref, ref)
			}
		}
	}

	return errors.Join(problems...)
}

// supportedSpecVersion reports whether v is an OpenAPI 3.0.x or 3.1.x version. | supportedSpecVersion
// 报告 v 是否为 OpenAPI 3.0.x 或 3.1.x 版本。
func supportedSpecVersion(v string) bool {
	return strings.HasPrefix(v, "3.0.") || strings.HasPrefix(v, "3.1.")
}

// validateServers checks that every server carries a URL. | validateServers 检查每个服务器都带 URL。
func validateServers(list []Server, where string, add func(string, ...any)) {
	for _, s := range list {
		if strings.TrimSpace(s.URL) == "" {
			add("%s contains a server without url | %s 含缺少 url 的服务器", where, where)
		}
	}
}

// validateSecurityScheme checks the type specific required fields of one scheme. | validateSecurityScheme
// 检查单个方案的类型专属必填字段。
func validateSecurityScheme(name string, s *SecurityScheme, add func(string, ...any)) {
	if s == nil {
		add("security scheme %q is nil | 安全方案 %q 为 nil", name, name)
		return
	}
	switch s.Type {
	case "apiKey":
		if strings.TrimSpace(s.Name) == "" {
			add("apiKey security scheme %q requires name | apiKey 安全方案 %q 缺少 name", name, name)
		}
		if s.In != "header" && s.In != "query" {
			add("apiKey security scheme %q requires in header or query | apiKey 安全方案 %q 的 in 必须为 header 或 query", name, name)
		}
	case "http":
		if strings.TrimSpace(s.Scheme) == "" {
			add("http security scheme %q requires scheme | http 安全方案 %q 缺少 scheme", name, name)
		}
	case "oauth2":
		if s.Flows == nil {
			add("oauth2 security scheme %q requires flows | oauth2 安全方案 %q 缺少 flows", name, name)
		}
	case "openIdConnect":
		if strings.TrimSpace(s.OpenIDConnectURL) == "" {
			add("openIdConnect security scheme %q requires openIdConnectUrl | openIdConnect 安全方案 %q 缺少 openIdConnectUrl", name, name)
		}
	case "mutualTLS":
		// mutualTLS has no type specific fields, an OpenAPI 3.1 addition. | mutualTLS 没有类型专属
		// 字段，是 OpenAPI 3.1 的新增内容。
	default:
		add("security scheme %q has unsupported type %q | 安全方案 %q 的类型 %q 不受支持", name, s.Type, name, s.Type)
	}
	for _, named := range oauthFlows(s.Flows) {
		flowName, flow := named.name, named.flow
		if flow == nil {
			continue
		}
		if flow.Scopes == nil {
			add("oauth2 flow %s of scheme %q requires scopes | 安全方案 %q 的 %s 流程缺少 scopes",
				flowName, name, name, flowName)
		}
		switch flowName {
		case "implicit":
			if strings.TrimSpace(flow.AuthorizationURL) == "" {
				add("implicit flow of scheme %q requires authorizationUrl | 安全方案 %q 的 implicit 流程缺少 authorizationUrl", name, name)
			}
		case "password", "clientCredentials":
			if strings.TrimSpace(flow.TokenURL) == "" {
				add("flow %s of scheme %q requires tokenUrl | 安全方案 %q 的 %s 流程缺少 tokenUrl",
					flowName, name, name, flowName)
			}
		case "authorizationCode":
			if strings.TrimSpace(flow.AuthorizationURL) == "" {
				add("authorizationCode flow of scheme %q requires authorizationUrl | 安全方案 %q 的 authorizationCode 流程缺少 authorizationUrl", name, name)
			}
			if strings.TrimSpace(flow.TokenURL) == "" {
				add("authorizationCode flow of scheme %q requires tokenUrl | 安全方案 %q 的 authorizationCode 流程缺少 tokenUrl", name, name)
			}
		}
	}
}

// namedOAuthFlow pairs an OAuth flow with its specification name. | namedOAuthFlow 把 OAuth
// 流程与其规范名配对。
type namedOAuthFlow struct {
	flow *OAuthFlow
	name string
}

// oauthFlows lists the flows of a scheme in a deterministic order. | oauthFlows 按确定顺序列出
// 方案的流程。
func oauthFlows(f *OAuthFlows) []namedOAuthFlow {
	if f == nil {
		return nil
	}
	return []namedOAuthFlow{
		{name: "implicit", flow: f.Implicit},
		{name: "password", flow: f.Password},
		{name: "clientCredentials", flow: f.ClientCredentials},
		{name: "authorizationCode", flow: f.AuthorizationCode},
	}
}

// checkSecurity verifies that every referenced security scheme is declared. | checkSecurity 校验每个
// 被引用的安全方案都已声明。
func checkSecurity(reqs []SecurityRequirement, where string, doc Document, add func(string, ...any)) {
	for _, req := range reqs {
		for _, scheme := range sortedKeys(req) {
			if strings.TrimSpace(scheme) == "" {
				add("%s contains an empty security scheme name | %s 含空的安全方案名", where, where)
				continue
			}
			if _, ok := doc.Components.SecuritySchemes[scheme]; !ok {
				add("%s references undeclared security scheme %q | %s 引用了未声明的安全方案 %q",
					where, scheme, where, scheme)
			}
		}
	}
}

// validateParameter checks one parameter. | validateParameter 检查单个参数。
func validateParameter(p Parameter, where string, add func(string, ...any)) {
	if p.Ref != "" {
		// A Reference Object carries no parameter fields of its own. | 引用对象不携带自身的参数字段。
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		add("%s contains a parameter without a name | %s 含缺少名字的参数", where, where)
	}
	switch p.In {
	case "path", "query", "header", "cookie":
	case "":
		add("parameter %q of %s has no location | %s 的参数 %q 缺少位置", p.Name, where, where, p.Name)
	default:
		add("parameter %q of %s has unsupported location %q | %s 的参数 %q 位置 %q 不受支持",
			p.Name, where, p.In, where, p.Name, p.In)
	}
	if p.In == "path" && !p.Required {
		add("path parameter %q of %s must be required | %s 的路径参数 %q 必须必填", p.Name, where, where, p.Name)
	}
	if p.Schema == nil {
		add("parameter %q of %s has no schema | %s 的参数 %q 缺少 Schema", p.Name, where, where, p.Name)
	}
}

// collectParameterRefs collects the schema references of one parameter. | collectParameterRefs
// 收集单个参数的 Schema 引用。
func collectParameterRefs(p Parameter, refs *[]string) {
	collectSchemaRefs(p.Schema, refs)
	collectContentRefs(p.Content, refs)
}

// collectContentRefs collects the schema references of a media type map, including the header
// schemas of its encodings. | collectContentRefs 收集媒体类型映射中的 Schema 引用，包含其编码
// 配置里的响应头 Schema。
func collectContentRefs(content map[string]MediaType, refs *[]string) {
	for _, media := range sortedKeys(content) {
		mt := content[media]
		collectSchemaRefs(mt.Schema, refs)
		for _, prop := range sortedKeys(mt.Encoding) {
			enc := mt.Encoding[prop]
			for _, header := range sortedKeys(enc.Headers) {
				collectHeaderRefs(enc.Headers[header], refs)
			}
		}
	}
}

// collectHeaderRefs collects the schema references of one header, covering both the schema and
// content forms. | collectHeaderRefs 收集单个响应头的 Schema 引用，同时覆盖 schema 与 content
// 两种形式。
func collectHeaderRefs(h *Header, refs *[]string) {
	if h == nil {
		return
	}
	collectSchemaRefs(h.Schema, refs)
	collectContentRefs(h.Content, refs)
}

// collectPathItemRefs collects the schema references reachable from one path item. | collectPathItemRefs
// 收集单个路径项可达的 Schema 引用。
func collectPathItemRefs(item PathItem, refs *[]string) {
	for _, p := range item.Parameters {
		collectParameterRefs(p, refs)
	}
	for _, entry := range operationEntries(item) {
		op := entry.op
		if op == nil {
			continue
		}
		for _, p := range op.Parameters {
			collectParameterRefs(p, refs)
		}
		if op.RequestBody != nil {
			collectContentRefs(op.RequestBody.Content, refs)
		}
		for _, code := range sortedKeys(op.Responses) {
			res := op.Responses[code]
			collectContentRefs(res.Content, refs)
			for _, header := range sortedKeys(res.Headers) {
				collectHeaderRefs(res.Headers[header], refs)
			}
		}
		for _, name := range sortedKeys(op.Callbacks) {
			callback := op.Callbacks[name]
			for _, expression := range sortedKeys(callback) {
				collectPathItemRefs(callback[expression], refs)
			}
		}
	}
}

// collectSchemaRefs appends every schema reference below s to refs. | collectSchemaRefs 把 s
// 之下的全部 Schema 引用追加到 refs。
func collectSchemaRefs(s *Schema, refs *[]string) {
	if s == nil {
		return
	}
	if s.Ref != "" {
		*refs = append(*refs, s.Ref)
		return
	}
	for _, name := range sortedKeys(s.Properties) {
		collectSchemaRefs(s.Properties[name], refs)
	}
	for _, name := range sortedKeys(s.Defs) {
		collectSchemaRefs(s.Defs[name], refs)
	}
	for _, name := range sortedKeys(s.PatternProperties) {
		collectSchemaRefs(s.PatternProperties[name], refs)
	}
	for _, name := range sortedKeys(s.DependentSchemas) {
		collectSchemaRefs(s.DependentSchemas[name], refs)
	}
	collectSchemaRefs(s.Items, refs)
	collectSchemaRefs(s.Not, refs)
	collectSchemaRefs(s.PropertyNames, refs)
	collectSchemaRefs(s.Contains, refs)
	collectSchemaRefs(s.ContentSchema, refs)
	collectSchemaRefs(s.If, refs)
	collectSchemaRefs(s.Then, refs)
	collectSchemaRefs(s.Else, refs)
	for _, child := range s.AllOf {
		collectSchemaRefs(child, refs)
	}
	for _, child := range s.OneOf {
		collectSchemaRefs(child, refs)
	}
	for _, child := range s.AnyOf {
		collectSchemaRefs(child, refs)
	}
	for _, child := range s.PrefixItems {
		collectSchemaRefs(child, refs)
	}
	if additional, ok := s.AdditionalProperties.(*Schema); ok {
		collectSchemaRefs(additional, refs)
	}
	if unevaluated, ok := s.UnevaluatedProperties.(*Schema); ok {
		collectSchemaRefs(unevaluated, refs)
	}
	if unevaluated, ok := s.UnevaluatedItems.(*Schema); ok {
		collectSchemaRefs(unevaluated, refs)
	}
	if s.Discriminator != nil {
		for _, ref := range s.Discriminator.Mapping {
			if strings.HasPrefix(ref, refPrefix) {
				*refs = append(*refs, ref)
			}
		}
	}
}

// operationEntry pairs an operation with its upper case HTTP method. | operationEntry 把操作与其
// 大写 HTTP 方法配对。
type operationEntry struct {
	op     *Operation
	method string
}

// operationEntries lists the non-nil operations of a path item in a deterministic order. | operationEntries
// 按确定顺序列出路径项中的非空操作。
func operationEntries(item PathItem) []operationEntry {
	return []operationEntry{
		{op: item.Get, method: "GET"},
		{op: item.Post, method: "POST"},
		{op: item.Put, method: "PUT"},
		{op: item.Patch, method: "PATCH"},
		{op: item.Delete, method: "DELETE"},
		{op: item.Head, method: "HEAD"},
		{op: item.Options, method: "OPTIONS"},
		{op: item.Trace, method: "TRACE"},
	}
}

// pathParamNames returns the names of the {name} placeholders of a path template. | pathParamNames
// 返回路径模板中 {name} 占位符的名字。
func pathParamNames(path string) []string {
	var names []string
	for i := 0; i < len(path); i++ {
		if path[i] != '{' {
			continue
		}
		end := strings.IndexByte(path[i+1:], '}')
		if end < 0 {
			break
		}
		name := path[i+1 : i+1+end]
		if name != "" && !containsString(names, name) {
			names = append(names, name)
		}
		i += end + 1
	}
	return names
}

// containsString reports whether list contains want. | containsString 报告 list 是否包含 want。
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// validResponseCode reports whether code is "default" or a status code accepted by OpenAPI 3.0,
// including the "1XX" to "5XX" ranges. | validResponseCode 报告 code 是否为 "default" 或
// OpenAPI 3.0 接受的状态码，包括 "1XX" 至 "5XX" 区间。
func validResponseCode(code string) bool {
	if code == "default" {
		return true
	}
	if len(code) != 3 || code[0] < '1' || code[0] > '5' {
		return false
	}
	if code[1:] == "XX" {
		return true
	}
	return code[1] >= '0' && code[1] <= '9' && code[2] >= '0' && code[2] <= '9'
}
