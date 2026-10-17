// Package openapi builds OpenAPI 3.0 documents from yd router definitions, merges
// source level annotations and serves the result together with a self-contained
// viewer page. The default specification version is 3.0.3; setting Document.OpenAPI to
// LatestSpecVersion switches on the 3.1 object additions. | Package openapi 依据 yd 路由定义
// 构建 OpenAPI 3.0 文档、合并源码注解，并连同自包含的查看器页面一起对外提供。默认规范版本为
// 3.0.3；把 Document.OpenAPI 设为 LatestSpecVersion 可启用 3.1 的对象补充。
//
// Three layers are provided: | 本包提供三层能力：
//
//   - the document model (Document, PathItem, Operation, ...) which is only ever
//     serialized through jsonx; | 文档模型（Document、PathItem、Operation 等），只经 jsonx 序列化；
//   - Builder, which assembles documents by hand and derives JSON Schemas from Go
//     structs by reflection (build time only, never on the request path); | Builder，
//     用于手工装配文档，并通过反射从 Go 结构体推导 JSON Schema（仅构建期，不进请求热路径）；
//   - FromRouter / FromRouterWithAnnotations, which turn router.Routes() into paths and
//     merge @Summary style annotations parsed from handler source. | FromRouter /
//     FromRouterWithAnnotations，把 router.Routes() 转换为路径，并合并从处理器源码解析出的
//     @Summary 风格注解。
//
// Handler serves both the JSON document and the built-in viewer without referencing
// any external CDN. | Handler 同时提供 JSON 文档与内置查看器，且不引用任何外部 CDN。
//
// Typical wiring: | 典型装配方式：
//
//	info := openapi.Info{Title: "Demo", Version: "1.0.0"}
//	opts := openapi.Options{}
//	doc := openapi.FromRouter(r, info, opts)
//	h := openapi.Handler(doc, opts)
//	r.GET(opts.Path, h)
//	r.GET(opts.DocsPath, h)
package openapi

import (
	"bytes"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
	"go.yaml.in/yaml/v3"
)

// Serialization errors, declared once at package level. | 序列化错误，在包级声明一次。
var (
	// ErrMarshal means the document could not be serialized. | ErrMarshal 表示文档无法序列化。
	ErrMarshal = errx.New("openapi: failed to marshal document | openapi：文档序列化失败")
	// ErrDecode means the serialized document could not be decoded back. | ErrDecode 表示序列化后的文档无法解码回结构。
	ErrDecode = errx.New("openapi: failed to decode document | openapi：文档反序列化失败")
)

// Specification version and default endpoints. | 规范版本与默认端点。
const (
	// SpecVersion is the default OpenAPI specification version emitted by this package. | SpecVersion
	// 是本包默认输出的 OpenAPI 规范版本。
	SpecVersion = "3.0.3"

	// DefaultPath is the default endpoint serving the JSON document. | DefaultPath 是提供 JSON 文档的默认端点。
	DefaultPath = "/openapi.json"

	// DefaultDocsPath is the default endpoint serving the built-in viewer. | DefaultDocsPath 是提供内置查看器的默认端点。
	DefaultDocsPath = "/docs"

	// LatestSpecVersion is the newest OpenAPI specification supported by the model. Setting
	// Document.OpenAPI to this value enables the 3.1 object additions (webhooks,
	// jsonSchemaDialect, info.summary, license.identifier, components.pathItems and schema type
	// arrays); the default stays SpecVersion so existing documents are unchanged. | LatestSpecVersion
	// 是模型支持的最新 OpenAPI 规范版本。把 Document.OpenAPI 设为此值即可启用 3.1 的对象补充
	// （webhooks、jsonSchemaDialect、info.summary、license.identifier、components.pathItems 与
	// Schema 类型数组）；默认仍为 SpecVersion，既有文档因此保持不变。
	LatestSpecVersion = "3.1.1"
)

// Document is the root object of an OpenAPI 3.0 or 3.1 document. | Document 是 OpenAPI 3.0 或
// 3.1 文档的根对象。
type Document struct {
	// Paths maps an OpenAPI path template to its operations. | Paths 把 OpenAPI 路径模板映射到操作。
	Paths map[string]PathItem `json:"paths"`
	// Webhooks maps a webhook name to its path item; this is an OpenAPI 3.1 addition. | Webhooks
	// 把 Webhook 名映射到路径项；这是 OpenAPI 3.1 的新增内容。
	Webhooks map[string]PathItem `json:"webhooks,omitempty"`
	// Components holds reusable schemas and other reusable objects. | Components 存放可复用 Schema
	// 及其它可复用对象。
	Components Components `json:"components,omitempty"`
	// Info carries the metadata section. | Info 承载元数据段。
	Info Info `json:"info"`
	// ExternalDocs points at the external documentation. | ExternalDocs 指向外部文档。
	ExternalDocs *ExternalDocs `json:"externalDocs,omitempty"`
	// JSONSchemaDialect is the default dialect of the schema objects, an OpenAPI 3.1 addition. | JSONSchemaDialect
	// 为 Schema 对象的默认方言，是 OpenAPI 3.1 的新增内容。
	JSONSchemaDialect string `json:"jsonSchemaDialect,omitempty"`
	// OpenAPI is the specification version; empty serializes as SpecVersion, and
	// LatestSpecVersion enables the 3.1 additions. | OpenAPI 为规范版本；留空时序列化为
	// SpecVersion，设为 LatestSpecVersion 可启用 3.1 的新增内容。
	OpenAPI string `json:"openapi"`
	// Servers lists the target servers. | Servers 列出目标服务器。
	Servers []Server `json:"servers,omitempty"`
	// Security applies the given requirements to every operation unless the operation
	// declares its own. | Security 为未自行声明的操作提供全局安全要求。
	Security []SecurityRequirement `json:"security,omitempty"`
	// Tags documents the tags used by operations. | Tags 说明操作使用的标签。
	Tags []Tag `json:"tags,omitempty"`
}

// Info is the metadata section of a document. | Info 是文档的元数据段。
type Info struct {
	// Title is the API title. | Title 为 API 标题。
	Title string `json:"title"`
	// Description is the API description. | Description 为 API 描述。
	Description string `json:"description,omitempty"`
	// Version is the API version. | Version 为 API 版本。
	Version string `json:"version"`
	// Summary is a short summary, an OpenAPI 3.1 addition. | Summary 为简短摘要，是 OpenAPI 3.1
	// 的新增内容。
	Summary string `json:"summary,omitempty"`
	// TermsOfService points at the terms of service. | TermsOfService 指向服务条款。
	TermsOfService string `json:"termsOfService,omitempty"`
	// Contact is the contact section. | Contact 为联系人段。
	Contact Contact `json:"contact"`
	// License is the license section. | License 为许可协议段。
	License License `json:"license"`
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
//
// Contact and License are optional in OpenAPI 3.0, and License requires a name when it is
// present, so a zero value must be omitted rather than emitted as an empty object. | 在
// OpenAPI 3.0 中 Contact 与 License 都是可选项，且 License 出现时必须带 name，因此零值必须省略，
// 不能输出成空对象。
func (i Info) MarshalJSON() ([]byte, error) {
	type wireInfo struct {
		Contact        *Contact `json:"contact,omitempty"`
		License        *License `json:"license,omitempty"`
		Title          string   `json:"title"`
		Description    string   `json:"description,omitempty"`
		Version        string   `json:"version"`
		Summary        string   `json:"summary,omitempty"`
		TermsOfService string   `json:"termsOfService,omitempty"`
	}
	w := wireInfo{
		Title:          i.Title,
		Description:    i.Description,
		Version:        i.Version,
		Summary:        i.Summary,
		TermsOfService: i.TermsOfService,
	}
	if i.Contact != (Contact{}) {
		w.Contact = &i.Contact
	}
	if i.License != (License{}) {
		w.License = &i.License
	}
	return jsonx.Marshal(w)
}

// Contact is the contact section of Info. | Contact 是 Info 的联系人段。
type Contact struct {
	// Name is the contact name. | Name 为联系人名称。
	Name string `json:"name,omitempty"`
	// URL is the contact page. | URL 为联系人页面。
	URL string `json:"url,omitempty"`
	// Email is the contact mailbox. | Email 为联系人邮箱。
	Email string `json:"email,omitempty"`
}

// License is the license section of Info. | License 是 Info 的许可协议段。
type License struct {
	// Name is the license name. | Name 为许可协议名称。
	Name string `json:"name,omitempty"`
	// URL is the license text. | URL 为许可协议正文。
	URL string `json:"url,omitempty"`
	// Identifier is an SPDX license expression, an OpenAPI 3.1 addition that is mutually
	// exclusive with URL. | Identifier 为 SPDX 许可表达式，是 OpenAPI 3.1 的新增内容，与 URL
	// 互斥。
	Identifier string `json:"identifier,omitempty"`
}

// Server describes one target server. | Server 描述一台目标服务器。
type Server struct {
	// Variables holds the values of the {variable} placeholders in URL. | Variables 保存 URL 中
	// {variable} 占位符的取值。
	Variables map[string]ServerVariable `json:"variables,omitempty"`
	// URL is the server base URL. | URL 为服务器基地址。
	URL string `json:"url"`
	// Description explains the server role. | Description 说明服务器用途。
	Description string `json:"description,omitempty"`
}

// ServerVariable is one server URL variable. | ServerVariable 是服务器 URL 的一个变量。
type ServerVariable struct {
	// Default is the value used when the client does not supply one. | Default 是客户端未提供时
	// 使用的取值。
	Default string `json:"default"`
	// Description explains the variable. | Description 说明该变量。
	Description string `json:"description,omitempty"`
	// Enum lists the allowed values. | Enum 列出允许取值。
	Enum []string `json:"enum,omitempty"`
}

// Tag describes one operation tag. | Tag 描述一个操作用标签。
type Tag struct {
	// ExternalDocs points at the external documentation of the tag. | ExternalDocs 指向该标签的
	// 外部文档。
	ExternalDocs *ExternalDocs `json:"externalDocs,omitempty"`
	// Name is the tag name. | Name 为标签名。
	Name string `json:"name"`
	// Description explains the tag. | Description 说明标签含义。
	Description string `json:"description,omitempty"`
}

// ExternalDocs points at documentation outside the document. | ExternalDocs 指向文档之外的外部文档。
type ExternalDocs struct {
	// URL is the documentation address. | URL 为文档地址。
	URL string `json:"url"`
	// Description explains the target. | Description 说明目标内容。
	Description string `json:"description,omitempty"`
}

// SecurityRequirement names the security schemes applied to an operation together with the
// scopes required from each. | SecurityRequirement 列出一个操作应用的安全方案以及每个方案要求的
// 作用域。
//
// Several entries in one requirement are combined with AND, while several requirements in a
// list are combined with OR, exactly as OpenAPI 3.0 defines. | 同一个要求中的多个条目按 AND 组合，
// 列表中的多个要求按 OR 组合，与 OpenAPI 3.0 的定义一致。
type SecurityRequirement map[string][]string

// SecurityScheme declares an authentication scheme referenced by SecurityRequirement. | SecurityScheme
// 声明由 SecurityRequirement 引用的认证方案。
type SecurityScheme struct {
	// Flows holds the OAuth 2 flows, required when Type is "oauth2". | Flows 保存 OAuth 2 流程，
	// Type 为 "oauth2" 时必填。
	Flows *OAuthFlows `json:"flows,omitempty"`
	// Type is one of apiKey, http, oauth2, openIdConnect or mutualTLS (3.1). | Type 取 apiKey、
	// http、oauth2、openIdConnect 或 mutualTLS（3.1）。
	Type string `json:"type"`
	// Description explains the scheme. | Description 说明该方案。
	Description string `json:"description,omitempty"`
	// Name is the header or query parameter name, required for apiKey. | Name 为请求头或查询参数名，
	// apiKey 方案必填。
	Name string `json:"name,omitempty"`
	// In is the apiKey location, either header or query. | In 为 apiKey 的位置，取 header 或 query。
	In string `json:"in,omitempty"`
	// Scheme is the HTTP authorization scheme, for example bearer, required for http. | Scheme
	// 为 HTTP 认证方案，例如 bearer，http 方案必填。
	Scheme string `json:"scheme,omitempty"`
	// BearerFormat hints at the bearer token format, for example JWT. | BearerFormat 提示 bearer
	// 令牌格式，例如 JWT。
	BearerFormat string `json:"bearerFormat,omitempty"`
	// OpenIDConnectURL is the discovery document address, required for openIdConnect. | OpenIDConnectURL
	// 为发现文档地址，openIdConnect 方案必填。
	OpenIDConnectURL string `json:"openIdConnectUrl,omitempty"`
}

// OAuthFlows groups the OAuth 2 flows supported by a scheme. | OAuthFlows 汇总一个方案支持的 OAuth 2 流程。
type OAuthFlows struct {
	// Implicit is the implicit grant flow. | Implicit 为隐式授权流程。
	Implicit *OAuthFlow `json:"implicit,omitempty"`
	// Password is the resource owner password flow. | Password 为资源所有者口令流程。
	Password *OAuthFlow `json:"password,omitempty"`
	// ClientCredentials is the client credentials flow. | ClientCredentials 为客户端凭据流程。
	ClientCredentials *OAuthFlow `json:"clientCredentials,omitempty"`
	// AuthorizationCode is the authorization code flow. | AuthorizationCode 为授权码流程。
	AuthorizationCode *OAuthFlow `json:"authorizationCode,omitempty"`
}

// OAuthFlow describes one OAuth 2 flow. | OAuthFlow 描述一个 OAuth 2 流程。
type OAuthFlow struct {
	// Scopes maps a scope name to its description, required for every flow. | Scopes 把作用域名
	// 映射到说明，每个流程都必填。
	Scopes map[string]string `json:"scopes"`
	// AuthorizationURL is the authorization endpoint. | AuthorizationURL 为授权端点。
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	// TokenURL is the token endpoint. | TokenURL 为令牌端点。
	TokenURL string `json:"tokenUrl,omitempty"`
	// RefreshURL is the refresh endpoint. | RefreshURL 为刷新端点。
	RefreshURL string `json:"refreshUrl,omitempty"`
}

// PathItem groups the operations available on one path, keyed by lower case HTTP
// method names. | PathItem 汇总一个路径上的可用操作，以 HTTP 方法小写名作为键。
type PathItem struct {
	// Get is the GET operation. | Get 为 GET 操作。
	Get *Operation `json:"get,omitempty"`
	// Post is the POST operation. | Post 为 POST 操作。
	Post *Operation `json:"post,omitempty"`
	// Put is the PUT operation. | Put 为 PUT 操作。
	Put *Operation `json:"put,omitempty"`
	// Patch is the PATCH operation. | Patch 为 PATCH 操作。
	Patch *Operation `json:"patch,omitempty"`
	// Delete is the DELETE operation. | Delete 为 DELETE 操作。
	Delete *Operation `json:"delete,omitempty"`
	// Head is the HEAD operation. | Head 为 HEAD 操作。
	Head *Operation `json:"head,omitempty"`
	// Options is the OPTIONS operation. | Options 为 OPTIONS 操作。
	Options *Operation `json:"options,omitempty"`
	// Trace is the TRACE operation. | Trace 为 TRACE 操作。
	Trace *Operation `json:"trace,omitempty"`
	// Summary is a one line summary shared by the operations of the path. | Summary 为该路径
	// 各操作共享的一行摘要。
	Summary string `json:"summary,omitempty"`
	// Description is the shared long form description. | Description 为共享的详细描述。
	Description string `json:"description,omitempty"`
	// Ref is serialized as $ref when the path item is a reference. | Ref 在路径项为引用时序列化为 $ref。
	Ref string `json:"$ref,omitempty"`
	// Servers overrides the document servers for this path. | Servers 为该路径覆盖文档级服务器。
	Servers []Server `json:"servers,omitempty"`
	// Parameters lists parameters shared by every operation of the path. | Parameters 列出该路径
	// 各操作共享的参数。
	Parameters []Parameter `json:"parameters,omitempty"`
}

// Operation describes a single endpoint. | Operation 描述单个端点。
type Operation struct {
	// RequestBody describes the request payload. | RequestBody 描述请求载荷。
	RequestBody *RequestBody `json:"requestBody,omitempty"`
	// ExternalDocs points at the external documentation of the operation. | ExternalDocs 指向该操作
	// 的外部文档。
	ExternalDocs *ExternalDocs `json:"externalDocs,omitempty"`
	// Responses maps a status code to its response. | Responses 把状态码映射到响应。
	Responses map[string]Response `json:"responses,omitempty"`
	// Callbacks maps a callback name to the callback object describing the out-of-band
	// requests the operation may trigger. | Callbacks 把回调名映射到描述该操作可能触发的带外请求的
	// 回调对象。
	Callbacks map[string]Callback `json:"callbacks,omitempty"`
	// Summary is a one line summary. | Summary 为一行摘要。
	Summary string `json:"summary,omitempty"`
	// Description is the long form description. | Description 为详细描述。
	Description string `json:"description,omitempty"`
	// OperationID is the unique operation identifier, filled by @ID. | OperationID 为唯一操作标识，
	// 由 @ID 填充。
	OperationID string `json:"operationId,omitempty"`
	// Tags group operations in the viewer. | Tags 用于在查看器中分组。
	Tags []string `json:"tags,omitempty"`
	// Parameters lists path, query, header and cookie parameters. | Parameters 列出路径、查询、请求头与 Cookie 参数。
	Parameters []Parameter `json:"parameters,omitempty"`
	// Security lists the security requirements, filled by @Security. | Security 列出安全要求，
	// 由 @Security 填充。
	Security []SecurityRequirement `json:"security,omitempty"`
	// Servers overrides the document servers for this operation. | Servers 为该操作覆盖文档级服务器。
	Servers []Server `json:"servers,omitempty"`
	// Deprecated marks the operation as deprecated, filled by @Deprecated. | Deprecated 标记该操作
	// 已废弃，由 @Deprecated 填充。
	Deprecated bool `json:"deprecated,omitempty"`
}

// Example is a reusable example object. | Example 是可复用的示例对象。
type Example struct {
	// Value is the literal example value. | Value 为字面示例值。
	Value any `json:"value,omitempty"`
	// Summary is a one line summary. | Summary 为一行摘要。
	Summary string `json:"summary,omitempty"`
	// Description explains the example. | Description 说明示例含义。
	Description string `json:"description,omitempty"`
	// ExternalValue points at an example stored elsewhere. | ExternalValue 指向存放在别处的示例。
	ExternalValue string `json:"externalValue,omitempty"`
}

// Parameter describes one operation parameter. | Parameter 描述一个操作参数。
type Parameter struct {
	// Example is a sample value, filled by an @Example line below the @Param. | Example 为示例值，
	// 由 @Param 下方的 @Example 行填充。
	Example any `json:"example,omitempty"`
	// Schema is the value schema. | Schema 为取值 Schema。
	Schema *Schema `json:"schema,omitempty"`
	// Examples maps a name to a reusable example. | Examples 把名字映射到可复用示例。
	Examples map[string]Example `json:"examples,omitempty"`
	// Content describes the parameter with a media type map instead of Schema; the two are
	// mutually exclusive. | Content 用媒体类型映射而不是 Schema 描述参数；两者互斥。
	Content map[string]MediaType `json:"content,omitempty"`
	// Explode controls array and object serialization; nil means the style default. | Explode
	// 控制数组与对象的序列化方式；nil 表示使用样式的默认值。
	Explode *bool `json:"explode,omitempty"`
	// Name is the parameter name. | Name 为参数名。
	Name string `json:"name"`
	// In is one of path, query, header or cookie. | In 取 path、query、header 或 cookie。
	In string `json:"in"`
	// Description explains the parameter. | Description 说明参数含义。
	Description string `json:"description,omitempty"`
	// Style is the serialization style, for example form or simple. | Style 为序列化样式，例如
	// form 或 simple。
	Style string `json:"style,omitempty"`
	// Ref is serialized as $ref when the parameter is a reference. | Ref 在参数为引用时序列化为 $ref。
	Ref string `json:"$ref,omitempty"`
	// Required marks mandatory parameters; path parameters are always required. | Required 标记必填参数；路径参数恒为必填。
	Required bool `json:"required,omitempty"`
	// Deprecated marks the parameter as deprecated. | Deprecated 标记该参数已废弃。
	Deprecated bool `json:"deprecated,omitempty"`
	// AllowEmptyValue allows an empty value for query parameters. | AllowEmptyValue 允许查询参数
	// 取空值。
	AllowEmptyValue bool `json:"allowEmptyValue,omitempty"`
	// AllowReserved keeps reserved characters unescaped in query parameters. | AllowReserved 在
	// 查询参数中保留保留字符而不转义。
	AllowReserved bool `json:"allowReserved,omitempty"`
}

// Header describes one response header. | Header 描述一个响应头。
type Header struct {
	// Schema is the header value schema. | Schema 为响应头取值 Schema。
	Schema *Schema `json:"schema,omitempty"`
	// Example is a sample value. | Example 为示例值。
	Example any `json:"example,omitempty"`
	// Examples maps a name to a reusable example. | Examples 把名字映射到可复用示例。
	Examples map[string]Example `json:"examples,omitempty"`
	// Content describes the header with a media type map instead of Schema. | Content 用媒体
	// 类型映射而不是 Schema 描述响应头。
	Content map[string]MediaType `json:"content,omitempty"`
	// Explode controls array and object serialization; nil means the style default. | Explode
	// 控制数组与对象的序列化方式；nil 表示使用样式的默认值。
	Explode *bool `json:"explode,omitempty"`
	// Description explains the header. | Description 说明响应头含义。
	Description string `json:"description,omitempty"`
	// Style is the serialization style. | Style 为序列化样式。
	Style string `json:"style,omitempty"`
	// Ref is serialized as $ref when the header is a reference. | Ref 在响应头为引用时序列化为 $ref。
	Ref string `json:"$ref,omitempty"`
	// Required marks the header as mandatory. | Required 标记该响应头是否必填。
	Required bool `json:"required,omitempty"`
	// Deprecated marks the header as deprecated. | Deprecated 标记该响应头已废弃。
	Deprecated bool `json:"deprecated,omitempty"`
	// AllowReserved keeps reserved characters unescaped. | AllowReserved 保留保留字符而不转义。
	AllowReserved bool `json:"allowReserved,omitempty"`
	// AllowEmptyValue allows an empty value. | AllowEmptyValue 允许空值。
	AllowEmptyValue bool `json:"allowEmptyValue,omitempty"`
}

// Response describes one response. | Response 描述一个响应。
type Response struct {
	// Content maps a media type to its payload schema. | Content 把媒体类型映射到载荷 Schema。
	Content map[string]MediaType `json:"content,omitempty"`
	// Headers maps a header name to its description. | Headers 把响应头名映射到说明。
	Headers map[string]*Header `json:"headers,omitempty"`
	// Links maps a link name to the operation it relates to. | Links 把链接名映射到相关操作。
	Links map[string]*Link `json:"links,omitempty"`
	// Description explains the response. | Description 说明响应含义。
	Description string `json:"description"`
	// Ref is serialized as $ref when the response is a reference. | Ref 在响应为引用时序列化为 $ref。
	Ref string `json:"$ref,omitempty"`
}

// MediaType binds a schema to a media type. | MediaType 把 Schema 绑定到媒体类型。
type MediaType struct {
	// Schema is the payload schema. | Schema 为载荷 Schema。
	Schema *Schema `json:"schema,omitempty"`
	// Example is a sample payload, filled by an @Example line below the @Success, @Failure or
	// body @Param it belongs to. | Example 为示例载荷，由所属的 @Success、@Failure 或 body
	// @Param 下方的 @Example 行填充。
	Example any `json:"example,omitempty"`
	// Examples maps a name to a reusable example. | Examples 把名字映射到可复用示例。
	Examples map[string]Example `json:"examples,omitempty"`
	// Encoding maps a request body property name to its serialization rules. | Encoding 把请求体
	// 属性名映射到其序列化规则。
	Encoding map[string]Encoding `json:"encoding,omitempty"`
}

// Encoding describes how one request body property is serialized. | Encoding 描述单个请求体属性的
// 序列化方式。
type Encoding struct {
	// Headers maps a header name to its description. | Headers 把响应头名映射到说明。
	Headers map[string]*Header `json:"headers,omitempty"`
	// Explode controls array and object serialization. | Explode 控制数组与对象的序列化方式。
	Explode *bool `json:"explode,omitempty"`
	// ContentType overrides the media type of the property. | ContentType 覆盖该属性的媒体类型。
	ContentType string `json:"contentType,omitempty"`
	// Style is the serialization style. | Style 为序列化样式。
	Style string `json:"style,omitempty"`
	// AllowReserved keeps reserved characters unescaped. | AllowReserved 保留保留字符而不转义。
	AllowReserved bool `json:"allowReserved,omitempty"`
}

// Callback maps a runtime expression to the path item describing the out-of-band request. | Callback
// 把运行时表达式映射到描述带外请求的路径项。
type Callback map[string]PathItem

// Link describes a relationship between a response and another operation. | Link 描述响应与另一个
// 操作之间的关系。
type Link struct {
	// Parameters maps a target parameter name to its value or runtime expression. | Parameters
	// 把目标参数名映射到取值或运行时表达式。
	Parameters map[string]any `json:"parameters,omitempty"`
	// RequestBody is the request body of the target operation. | RequestBody 为目标操作的请求体。
	RequestBody any `json:"requestBody,omitempty"`
	// Server overrides the server of the target operation. | Server 覆盖目标操作的服务器。
	Server *Server `json:"server,omitempty"`
	// OperationRef is a relative or absolute reference to the target operation. | OperationRef
	// 指向目标操作的相对或绝对引用。
	OperationRef string `json:"operationRef,omitempty"`
	// OperationID is the operationId of the target operation. | OperationID 为目标操作的 operationId。
	OperationID string `json:"operationId,omitempty"`
	// Description explains the link. | Description 说明链接含义。
	Description string `json:"description,omitempty"`
}

// RequestBody describes a request payload. | RequestBody 描述请求载荷。
type RequestBody struct {
	// Content maps a media type to its payload schema. | Content 把媒体类型映射到载荷 Schema。
	Content map[string]MediaType `json:"content,omitempty"`
	// Description explains the payload. | Description 说明载荷含义。
	Description string `json:"description,omitempty"`
	// Ref is serialized as $ref when the request body is a reference. | Ref 在请求体为引用时
	// 序列化为 $ref。
	Ref string `json:"$ref,omitempty"`
	// Required marks a mandatory payload. | Required 标记载荷是否必填。
	Required bool `json:"required,omitempty"`
}

// Components holds reusable document fragments. | Components 存放可复用的文档片段。
type Components struct {
	// Schemas maps a component name to its schema. | Schemas 把组件名映射到 Schema。
	Schemas map[string]*Schema `json:"schemas,omitempty"`
	// SecuritySchemes maps a scheme name to its declaration. | SecuritySchemes 把方案名映射到声明。
	SecuritySchemes map[string]*SecurityScheme `json:"securitySchemes,omitempty"`
	// Parameters maps a component name to a reusable parameter. | Parameters 把组件名映射到可复用参数。
	Parameters map[string]*Parameter `json:"parameters,omitempty"`
	// Responses maps a component name to a reusable response. | Responses 把组件名映射到可复用响应。
	Responses map[string]*Response `json:"responses,omitempty"`
	// RequestBodies maps a component name to a reusable request body. | RequestBodies 把组件名映射
	// 到可复用请求体。
	RequestBodies map[string]*RequestBody `json:"requestBodies,omitempty"`
	// Headers maps a component name to a reusable header. | Headers 把组件名映射到可复用响应头。
	Headers map[string]*Header `json:"headers,omitempty"`
	// Examples maps a component name to a reusable example. | Examples 把组件名映射到可复用示例。
	Examples map[string]Example `json:"examples,omitempty"`
	// Links maps a component name to a reusable link. | Links 把组件名映射到可复用链接。
	Links map[string]*Link `json:"links,omitempty"`
	// Callbacks maps a component name to a reusable callback. | Callbacks 把组件名映射到可复用回调。
	Callbacks map[string]Callback `json:"callbacks,omitempty"`
	// PathItems maps a component name to a reusable path item, an OpenAPI 3.1 addition. | PathItems
	// 把组件名映射到可复用路径项，是 OpenAPI 3.1 的新增内容。
	PathItems map[string]PathItem `json:"pathItems,omitempty"`
}

// Options configures how the document endpoints are served. | Options 配置文档端点的提供方式。
type Options struct {
	// TypeAnnotations carries explicitly parsed type annotations, which take precedence over
	// SourceDirs. | TypeAnnotations 携带显式解析的类型注解，优先于 SourceDirs。
	TypeAnnotations *TypeAnnotations
	// Path is the endpoint returning the JSON document; empty means DefaultPath. | Path 是返回 JSON 文档的端点，留空表示 DefaultPath。
	Path string
	// DocsPath is the endpoint returning the built-in viewer; empty means DefaultDocsPath. | DocsPath 是返回内置查看器的端点，留空表示 DefaultDocsPath。
	DocsPath string
	// Title is the viewer page title; empty falls back to Info.Title. | Title 为查看器页面标题，留空时回退到 Info.Title。
	Title string
	// SourceDirs lists the source directories holding type annotations; when TypeAnnotations
	// is nil, FromRouterWithAnnotations parses them from these directories. | SourceDirs 列出
	// 存放类型注解的源码目录；TypeAnnotations 为 nil 时由 FromRouterWithAnnotations 从这些目录解析。
	SourceDirs []string
	// Blocked disables both endpoints, which is how production switches docs off. | Blocked 关闭两个端点，生产环境据此下线文档。
	Blocked bool
}

// withDefaults returns a copy with empty paths replaced by the defaults and a
// leading slash guaranteed. | withDefaults 返回副本，空路径替换为默认值并保证以 "/" 开头。
func (o Options) withDefaults() Options {
	if strings.TrimSpace(o.Path) == "" {
		o.Path = DefaultPath
	}
	if strings.TrimSpace(o.DocsPath) == "" {
		o.DocsPath = DefaultDocsPath
	}
	if !strings.HasPrefix(o.Path, "/") {
		o.Path = "/" + o.Path
	}
	if !strings.HasPrefix(o.DocsPath, "/") {
		o.DocsPath = "/" + o.DocsPath
	}
	return o
}

// title resolves the viewer page title. | title 解析查看器页面标题。
func (o Options) title(info Info) string {
	if t := strings.TrimSpace(o.Title); t != "" {
		return t
	}
	if t := strings.TrimSpace(info.Title); t != "" {
		return t
	}
	return "API Docs | API 文档"
}

// AddServer appends a server entry. | AddServer 追加一个服务器条目。
func (d *Document) AddServer(s Server) {
	if d == nil {
		return
	}
	d.Servers = append(d.Servers, s)
}

// AddTag appends a tag description. | AddTag 追加一个标签说明。
func (d *Document) AddTag(t Tag) {
	if d == nil {
		return
	}
	d.Tags = append(d.Tags, t)
}

// AddSecurityScheme stores a security scheme under name, initializing the component map on
// first use; an empty name is ignored. | AddSecurityScheme 把安全方案以 name 存入组件，首次使用时
// 初始化映射；空名字会被忽略。
func (d *Document) AddSecurityScheme(name string, s SecurityScheme) {
	if d == nil || strings.TrimSpace(name) == "" {
		return
	}
	if d.Components.SecuritySchemes == nil {
		d.Components.SecuritySchemes = map[string]*SecurityScheme{}
	}
	scheme := s
	scheme.Flows = normalizeOAuthFlows(s.Flows)
	d.Components.SecuritySchemes[name] = &scheme
}

// normalizeOAuthFlows returns a copy whose flow scopes are non-nil, because OpenAPI requires the
// scopes object even when it is empty. | normalizeOAuthFlows 返回作用域非 nil 的副本，因为即使
// 作用域为空，OpenAPI 也要求 scopes 对象存在。
func normalizeOAuthFlows(flows *OAuthFlows) *OAuthFlows {
	if flows == nil {
		return nil
	}
	out := *flows
	out.Implicit = normalizeOAuthFlow(out.Implicit)
	out.Password = normalizeOAuthFlow(out.Password)
	out.ClientCredentials = normalizeOAuthFlow(out.ClientCredentials)
	out.AuthorizationCode = normalizeOAuthFlow(out.AuthorizationCode)
	return &out
}

// normalizeOAuthFlow returns a copy with a non-nil scopes map. | normalizeOAuthFlow 返回作用域映射
// 非 nil 的副本。
func normalizeOAuthFlow(flow *OAuthFlow) *OAuthFlow {
	if flow == nil {
		return nil
	}
	out := *flow
	if out.Scopes == nil {
		out.Scopes = map[string]string{}
	}
	return &out
}

// normalized returns a copy with spec mandated fields filled in. | normalized 返回补齐规范必填字段的副本。
func (d *Document) normalized() Document {
	doc := *d
	if strings.TrimSpace(doc.OpenAPI) == "" {
		doc.OpenAPI = SpecVersion
	}
	if doc.Paths == nil {
		doc.Paths = map[string]PathItem{}
	}
	return doc
}

// JSON serializes the document through the framework JSON layer. | JSON 通过框架 JSON 层序列化文档。
//
// The openapi field is always present; a zero valued Document still serializes as
// version SpecVersion. | openapi 字段始终存在；零值 Document 也会以 SpecVersion 版本序列化。
func (d *Document) JSON() ([]byte, error) {
	doc := d.normalized()
	raw, err := jsonx.MarshalIndent(&doc, "", "  ")
	if err != nil {
		return nil, errx.Wrap(err, ErrMarshal)
	}
	return raw, nil
}

// YAML serializes the document as YAML. | YAML 把文档序列化为 YAML。
//
// The document is encoded with the framework JSON layer first and then converted to
// YAML nodes, so the YAML shape is always identical to JSON(). | 文档先经框架 JSON 层编码
// 再转换为 YAML 节点，因此 YAML 形状与 JSON() 始终一致。
func (d *Document) YAML() ([]byte, error) {
	doc := d.normalized()
	raw, err := jsonx.Marshal(&doc)
	if err != nil {
		return nil, errx.Wrap(err, ErrMarshal)
	}
	var tree any
	if err := jsonx.Unmarshal(raw, &tree); err != nil {
		return nil, errx.Wrap(err, ErrDecode)
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(tree); err != nil {
		return nil, errx.Wrap(err, ErrMarshal)
	}
	if err := encoder.Close(); err != nil {
		return nil, errx.Wrap(err, ErrMarshal)
	}
	return buf.Bytes(), nil
}
