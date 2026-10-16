// Package openapi builds OpenAPI 3.0 documents from yd router definitions, merges
// source level annotations and serves the result together with a self-contained
// viewer page. | Package openapi 依据 yd 路由定义构建 OpenAPI 3.0 文档、合并源码注解，
// 并连同自包含的查看器页面一起对外提供。
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

	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/jsonx"
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
	// SpecVersion is the OpenAPI specification version emitted by this package. | SpecVersion 是本包输出的 OpenAPI 规范版本。
	SpecVersion = "3.0.3"

	// DefaultPath is the default endpoint serving the JSON document. | DefaultPath 是提供 JSON 文档的默认端点。
	DefaultPath = "/openapi.json"

	// DefaultDocsPath is the default endpoint serving the built-in viewer. | DefaultDocsPath 是提供内置查看器的默认端点。
	DefaultDocsPath = "/docs"
)

// Document is the root object of an OpenAPI 3.0 document. | Document 是 OpenAPI 3.0 文档的根对象。
type Document struct {
	// Paths maps an OpenAPI path template to its operations. | Paths 把 OpenAPI 路径模板映射到操作。
	Paths map[string]PathItem `json:"paths"`
	// Components holds reusable schemas. | Components 存放可复用 Schema。
	Components Components `json:"components,omitempty"`
	// Info carries the metadata section. | Info 承载元数据段。
	Info Info `json:"info"`
	// OpenAPI is the specification version, always SpecVersion when serialized. | OpenAPI 为规范版本，序列化时始终为 SpecVersion。
	OpenAPI string `json:"openapi"`
	// Servers lists the target servers. | Servers 列出目标服务器。
	Servers []Server `json:"servers,omitempty"`
}

// Info is the metadata section of a document. | Info 是文档的元数据段。
type Info struct {
	// Title is the API title. | Title 为 API 标题。
	Title string `json:"title"`
	// Description is the API description. | Description 为 API 描述。
	Description string `json:"description,omitempty"`
	// Version is the API version. | Version 为 API 版本。
	Version string `json:"version"`
	// TermsOfService points at the terms of service. | TermsOfService 指向服务条款。
	TermsOfService string `json:"termsOfService,omitempty"`
	// Contact is the contact section. | Contact 为联系人段。
	Contact Contact `json:"contact"`
	// License is the license section. | License 为许可协议段。
	License License `json:"license"`
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
}

// Server describes one target server. | Server 描述一台目标服务器。
type Server struct {
	// URL is the server base URL. | URL 为服务器基地址。
	URL string `json:"url"`
	// Description explains the server role. | Description 说明服务器用途。
	Description string `json:"description,omitempty"`
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
}

// Operation describes a single endpoint. | Operation 描述单个端点。
type Operation struct {
	// RequestBody describes the request payload. | RequestBody 描述请求载荷。
	RequestBody *RequestBody `json:"requestBody,omitempty"`
	// Responses maps a status code to its response. | Responses 把状态码映射到响应。
	Responses map[string]Response `json:"responses,omitempty"`
	// Summary is a one line summary. | Summary 为一行摘要。
	Summary string `json:"summary,omitempty"`
	// Description is the long form description. | Description 为详细描述。
	Description string `json:"description,omitempty"`
	// OperationID is the unique operation identifier. | OperationID 为唯一操作标识。
	OperationID string `json:"operationId,omitempty"`
	// Tags group operations in the viewer. | Tags 用于在查看器中分组。
	Tags []string `json:"tags,omitempty"`
	// Parameters lists path, query, header and cookie parameters. | Parameters 列出路径、查询、请求头与 Cookie 参数。
	Parameters []Parameter `json:"parameters,omitempty"`
}

// Parameter describes one operation parameter. | Parameter 描述一个操作参数。
type Parameter struct {
	// Example is a sample value, filled by an @Example line below the @Param. | Example 为示例值，
	// 由 @Param 下方的 @Example 行填充。
	Example any `json:"example,omitempty"`
	// Schema is the value schema. | Schema 为取值 Schema。
	Schema *Schema `json:"schema,omitempty"`
	// Name is the parameter name. | Name 为参数名。
	Name string `json:"name"`
	// In is one of path, query, header or cookie. | In 取 path、query、header 或 cookie。
	In string `json:"in"`
	// Description explains the parameter. | Description 说明参数含义。
	Description string `json:"description,omitempty"`
	// Required marks mandatory parameters; path parameters are always required. | Required 标记必填参数；路径参数恒为必填。
	Required bool `json:"required,omitempty"`
}

// Response describes one response. | Response 描述一个响应。
type Response struct {
	// Content maps a media type to its payload schema. | Content 把媒体类型映射到载荷 Schema。
	Content map[string]MediaType `json:"content,omitempty"`
	// Description explains the response. | Description 说明响应含义。
	Description string `json:"description"`
}

// MediaType binds a schema to a media type. | MediaType 把 Schema 绑定到媒体类型。
type MediaType struct {
	// Schema is the payload schema. | Schema 为载荷 Schema。
	Schema *Schema `json:"schema,omitempty"`
	// Example is a sample payload, filled by an @Example line below the @Success, @Failure or
	// body @Param it belongs to. | Example 为示例载荷，由所属的 @Success、@Failure 或 body
	// @Param 下方的 @Example 行填充。
	Example any `json:"example,omitempty"`
}

// RequestBody describes a request payload. | RequestBody 描述请求载荷。
type RequestBody struct {
	// Content maps a media type to its payload schema. | Content 把媒体类型映射到载荷 Schema。
	Content map[string]MediaType `json:"content,omitempty"`
	// Required marks a mandatory payload. | Required 标记载荷是否必填。
	Required bool `json:"required,omitempty"`
}

// Components holds reusable document fragments. | Components 存放可复用的文档片段。
type Components struct {
	// Schemas maps a component name to its schema. | Schemas 把组件名映射到 Schema。
	Schemas map[string]*Schema `json:"schemas,omitempty"`
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
