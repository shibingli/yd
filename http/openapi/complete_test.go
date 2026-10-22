package openapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/shibingli/ydx/json"
	"github.com/shibingli/ydx/router"
)

// decodeDoc marshals the document and decodes it back into a generic map. | decodeDoc 序列化文档
// 并解码回通用映射。
func decodeDoc(t *testing.T, doc *Document) map[string]any {
	t.Helper()
	raw, err := doc.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// objectAt walks a decoded JSON map along the given keys. | objectAt 沿给定键遍历解码后的 JSON 映射。
func objectAt(t *testing.T, root map[string]any, keys ...string) map[string]any {
	t.Helper()
	cur := root
	for _, key := range keys {
		next, ok := cur[key].(map[string]any)
		if !ok {
			t.Fatalf("missing object %q in %v | 缺少对象 %q", key, cur, key)
		}
		cur = next
	}
	return cur
}

// arrayAt returns the array stored under key. | arrayAt 返回 key 下的数组。
func arrayAt(t *testing.T, root map[string]any, key string) []any {
	t.Helper()
	list, ok := root[key].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("missing array %q in %v | 缺少数组 %q", key, root, key)
	}
	return list
}

func TestInfoOmitsEmptyContactAndLicense(t *testing.T) {
	doc := &Document{Info: Info{Title: "Demo", Version: "1.0.0"}}
	got := decodeDoc(t, doc)
	info := objectAt(t, got, "info")
	if _, ok := info["contact"]; ok {
		t.Errorf("empty contact must be omitted | 空 contact 必须省略: %v", info)
	}
	if _, ok := info["license"]; ok {
		t.Errorf("empty license must be omitted | 空 license 必须省略: %v", info)
	}

	doc.Info.Contact = Contact{Name: "Team", Email: "team@example.com"}
	doc.Info.License = License{Name: "Apache-2.0"}
	got = decodeDoc(t, doc)
	info = objectAt(t, got, "info")
	if objectAt(t, info, "contact")["name"] != "Team" {
		t.Errorf("contact name lost | contact.name 丢失")
	}
	if objectAt(t, info, "license")["name"] != "Apache-2.0" {
		t.Errorf("license name lost | license.name 丢失")
	}
}

func TestDocumentCoversOpenAPIObjects(t *testing.T) {
	doc := NewBuilder(Info{
		Title:       "Demo",
		Summary:     "demo api | 演示接口",
		Description: "sample | 示例",
		Version:     "1.0.0",
		License:     License{Name: "Apache-2.0", Identifier: "Apache-2.0"},
	}).Build()
	doc.OpenAPI = LatestSpecVersion
	doc.JSONSchemaDialect = "https://json-schema.org/draft/2020-12/schema"
	doc.ExternalDocs = &ExternalDocs{URL: "https://example.com/docs", Description: "docs | 文档"}
	doc.AddTag(Tag{Name: "users", Description: "user operations | 用户操作"})
	doc.AddServer(Server{
		URL:         "https://{env}.example.com",
		Description: "multi environment | 多环境",
		Variables: map[string]ServerVariable{
			"env": {Default: "api", Description: "environment | 环境", Enum: []string{"api", "staging"}},
		},
	})
	doc.AddSecurityScheme("BearerAuth", SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "JWT",
		Description:  "per-user JWT | 每用户 JWT",
	})
	doc.Components.Schemas["User"] = &Schema{
		Type: "object",
		Properties: map[string]*Schema{
			"id": {Type: "integer", Format: "int64"},
		},
		Required: []string{"id"},
	}
	doc.Components.Schemas["Pet"] = &Schema{
		OneOf:         []*Schema{{Ref: "#/components/schemas/User"}},
		Discriminator: &Discriminator{PropertyName: "kind", Mapping: map[string]string{"user": "#/components/schemas/User"}},
		XML:           &XML{Name: "pet", Namespace: "urn:demo", Attribute: false, Wrapped: true},
	}
	doc.Components.Links["UserById"] = &Link{OperationID: "get-user", Description: "user link | 用户链接"}
	doc.Components.Callbacks["onEvent"] = Callback{
		"{$request.body#/url}": PathItem{Post: &Operation{Responses: map[string]Response{"200": {Description: "ok | 成功"}}}},
	}
	doc.Webhooks["user.created"] = PathItem{
		Post: &Operation{OperationID: "user-created", Responses: map[string]Response{"200": {Description: "ok | 成功"}}},
	}
	doc.Paths["/users"] = PathItem{
		Summary:     "users | 用户",
		Description: "user collection | 用户集合",
		Parameters: []Parameter{{
			Name: "trace", In: "header", Style: "simple", Required: false,
			Schema: &Schema{Type: "boolean"},
		}},
		Post: &Operation{
			OperationID: "create-user",
			Summary:     "create | 创建",
			Deprecated:  true,
			Tags:        []string{"users"},
			Security:    []SecurityRequirement{{"BearerAuth": []string{}}},
			Parameters: []Parameter{{
				Name: "verbose", In: "query", Style: "form", AllowEmptyValue: true, AllowReserved: true,
				Examples: map[string]Example{"short": {Summary: "short | 简短", Value: true}},
				Schema:   &Schema{Type: "boolean"},
			}},
			RequestBody: &RequestBody{
				Description: "create payload | 创建载荷",
				Required:    true,
				Content: map[string]MediaType{
					"application/json": {
						Schema: &Schema{Ref: "#/components/schemas/User"},
						Encoding: map[string]Encoding{
							"id": {ContentType: "text/plain", Style: "form"},
						},
					},
				},
			},
			Responses: map[string]Response{
				"201": {
					Description: "created | 已创建",
					Headers: map[string]*Header{
						"Location": {Description: "resource URL | 资源地址", Schema: &Schema{Type: "string"}},
					},
					Links: map[string]*Link{
						"self": {OperationID: "get-user", Parameters: map[string]any{"id": "$response.body#/id"}},
					},
					Content: map[string]MediaType{
						"application/json": {Schema: &Schema{Ref: "#/components/schemas/User"}},
					},
				},
			},
			Callbacks: map[string]Callback{
				"onCreate": {"{$request.body#/callbackUrl}": {Post: &Operation{Responses: map[string]Response{"200": {Description: "ok | 成功"}}}}},
			},
		},
	}
	doc.Paths["/users/{id}"] = PathItem{Get: &Operation{
		OperationID: "get-user",
		Parameters:  []Parameter{{Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"}}},
		Responses:   map[string]Response{"200": {Description: "ok | 成功"}},
	}}

	got := decodeDoc(t, doc)
	if got["openapi"] != LatestSpecVersion {
		t.Errorf("openapi = %v | 版本应为 %s", got["openapi"], LatestSpecVersion)
	}
	if got["jsonSchemaDialect"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("jsonSchemaDialect lost | jsonSchemaDialect 丢失")
	}
	if objectAt(t, got, "info")["summary"] != "demo api | 演示接口" {
		t.Errorf("info.summary lost | info.summary 丢失")
	}
	if objectAt(t, got, "info", "license")["identifier"] != "Apache-2.0" {
		t.Errorf("license.identifier lost | license.identifier 丢失")
	}
	if objectAt(t, got, "externalDocs")["url"] == "" {
		t.Errorf("externalDocs lost | externalDocs 丢失")
	}
	servers := arrayAt(t, got, "servers")
	server, ok := servers[0].(map[string]any)
	if !ok {
		t.Fatalf("server entry = %v | 服务器条目格式不正确", servers[0])
	}
	if objectAt(t, got, "components", "securitySchemes", "BearerAuth")["scheme"] != "bearer" {
		t.Errorf("security scheme lost | 安全方案丢失")
	}
	if objectAt(t, got, "components", "links", "UserById")["operationId"] != "get-user" {
		t.Errorf("component link lost | 组件链接丢失")
	}
	if objectAt(t, got, "components", "callbacks", "onEvent") == nil {
		t.Errorf("component callback lost | 组件回调丢失")
	}
	if objectAt(t, got, "webhooks", "user.created") == nil {
		t.Errorf("webhooks lost | webhooks 丢失")
	}
	post := objectAt(t, got, "paths", "/users", "post")
	if post["deprecated"] != true {
		t.Errorf("operation.deprecated lost | operation.deprecated 丢失")
	}
	security, ok := arrayAt(t, post, "security")[0].(map[string]any)
	if !ok || security["BearerAuth"] == nil {
		t.Errorf("operation.security lost | operation.security 丢失")
	}
	if objectAt(t, post, "callbacks", "onCreate") == nil {
		t.Errorf("operation.callbacks lost | operation.callbacks 丢失")
	}
	if objectAt(t, post, "responses", "201", "links", "self")["operationId"] != "get-user" {
		t.Errorf("response links lost | 响应链接丢失")
	}
	if objectAt(t, post, "responses", "201", "headers", "Location")["description"] == "" {
		t.Errorf("response headers lost | 响应头丢失")
	}
	if objectAt(t, post, "requestBody", "content", "application/json", "encoding", "id")["contentType"] != "text/plain" {
		t.Errorf("media type encoding lost | 媒体类型编码丢失")
	}
	param, ok := arrayAt(t, post, "parameters")[0].(map[string]any)
	if !ok || param["style"] != "form" || param["allowEmptyValue"] != true || param["allowReserved"] != true {
		t.Errorf("parameter fields lost | 参数字段丢失")
	}
	pet := objectAt(t, got, "components", "schemas", "Pet")
	if objectAt(t, pet, "discriminator")["propertyName"] != "kind" {
		t.Errorf("discriminator lost | discriminator 丢失")
	}
	if objectAt(t, pet, "xml")["name"] != "pet" {
		t.Errorf("xml lost | xml 丢失")
	}
	if objectAt(t, server, "variables", "env")["default"] != "api" {
		t.Errorf("server variable lost | 服务器变量丢失")
	}

	if err := doc.Validate(); err != nil {
		t.Fatalf("extended document must validate | 扩展文档应通过校验: %v", err)
	}
}

func TestSchemaEmitsTypeArray(t *testing.T) {
	raw, err := json.Marshal(&Schema{Types: []string{"string", "null"}, Description: "nullable | 可空"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	types, ok := got["type"].([]any)
	if !ok || len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Fatalf("type = %v, want [string null] | type 应为 [string null]", got["type"])
	}

	raw, err = json.Marshal(&Schema{Type: "string"})
	if err != nil {
		t.Fatalf("marshal scalar: %v", err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal scalar: %v", err)
	}
	if got["type"] != "string" {
		t.Fatalf("type = %v, want string | type 应为 string", got["type"])
	}

	// JSON Schema 2020-12 keywords are carried verbatim. | JSON Schema 2020-12 关键字原样承载。
	rich := &Schema{
		ID:                    "urn:example:item",
		Defs:                  map[string]*Schema{"item": {Type: "string"}},
		PrefixItems:           []*Schema{{Type: "string"}, {Type: "integer"}},
		If:                    &Schema{Type: "object"},
		Then:                  &Schema{Required: []string{"a"}},
		Else:                  &Schema{Type: "object"},
		UnevaluatedProperties: false,
		ContentEncoding:       "base64",
	}
	raw, err = json.Marshal(rich)
	if err != nil {
		t.Fatalf("marshal rich: %v", err)
	}
	got = map[string]any{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal rich: %v", err)
	}
	for _, key := range []string{"$id", "$defs", "prefixItems", "if", "then", "else", "contentEncoding"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing JSON Schema keyword %q | 缺少 JSON Schema 关键字 %q: %s", key, key, raw)
		}
	}
	if got["unevaluatedProperties"] != false {
		t.Errorf("unevaluatedProperties = %v, want false | unevaluatedProperties 应为 false", got["unevaluatedProperties"])
	}
}

func TestAnnotationExtendedDirectives(t *testing.T) {
	src := `package handlers

// Create creates a user. | Create 创建用户。
//
// @Summary Create user | 创建用户
// @Tags users
// @ID create-user
// @Deprecated
// @Produce html
// @Param body body string true "payload | 载荷"
// @Success 201 {object} string "created | 已创建"
// @Failure 400 {object} string "bad request | 请求错误"
// @Header 201 {string} Location "resource URL | 资源地址"
// @Header all {string} X-Request-Id "request id | 请求 ID"
// @Security OAuth2 read:users write:users
// @Security ApiKey && Basic
// @Router /users [post]
func Create() {}
`
	blocks := parseFileAnnotations("handlers.go", src)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1 | 注解块数量应为 1", len(blocks))
	}
	a := blocks[0]
	if a.OperationID != "create-user" {
		t.Errorf("operationId = %q | @ID 解析不正确", a.OperationID)
	}
	if !a.Deprecated {
		t.Errorf("@Deprecated not parsed | @Deprecated 未解析")
	}
	if len(a.Security) != 2 {
		t.Fatalf("security = %v | @Security 应产生两个要求", a.Security)
	}
	if scopes := a.Security[0]["OAuth2"]; len(scopes) != 2 || scopes[0] != "read:users" || scopes[1] != "write:users" {
		t.Errorf("oauth scopes = %v | OAuth2 作用域解析不正确", scopes)
	}
	if _, ok := a.Security[1]["ApiKey"]; !ok {
		t.Errorf("AND requirement lost ApiKey | AND 要求丢失 ApiKey")
	}
	if _, ok := a.Security[1]["Basic"]; !ok {
		t.Errorf("AND requirement lost Basic | AND 要求丢失 Basic")
	}

	op := a.operation("/users", nil)
	if op.OperationID != "create-user" || !op.Deprecated {
		t.Errorf("operation identity lost | 操作标识丢失: %+v", op)
	}
	if len(op.Security) != 2 {
		t.Errorf("operation security = %v | 操作安全要求不正确", op.Security)
	}
	created := op.Responses["201"]
	if created.Headers["Location"] == nil || created.Headers["X-Request-Id"] == nil {
		t.Errorf("201 headers = %v | 201 响应头不完整", created.Headers)
	}
	if got := created.Headers["Location"].Schema; got == nil || got.Type != "string" {
		t.Errorf("header schema = %+v | 响应头 Schema 不正确", got)
	}
	if got := created.Content["text/html"].Schema; got == nil || got.Type != "string" {
		t.Errorf("{object} string must become a string schema, got %+v | {object} string 应生成 string Schema", got)
	}
	bad := op.Responses["400"]
	if bad.Headers["X-Request-Id"] == nil {
		t.Errorf("@Header all must reach every response | @Header all 应覆盖全部响应")
	}
	if bad.Headers["Location"] != nil {
		t.Errorf("@Header 201 must not leak into 400 | @Header 201 不应出现在 400 上")
	}
}

func TestParamAttributes(t *testing.T) {
	p, ok := parseParam(`age query int false "age | 年龄" Minimum(1) Maximum(120) default(18) Enums(18,30,60) Format(int32)`)
	if !ok {
		t.Fatal("parseParam rejected a valid declaration | parseParam 拒绝了合法声明")
	}
	if p.Description != "age | 年龄" {
		t.Errorf("description = %q | 说明解析不正确", p.Description)
	}
	s := p.Schema
	if s == nil || s.Type != "integer" {
		t.Fatalf("schema = %+v | Schema 解析不正确", s)
	}
	if s.Minimum == nil || *s.Minimum != 1 || s.Maximum == nil || *s.Maximum != 120 {
		t.Errorf("bounds = %v..%v | 数值边界解析不正确", s.Minimum, s.Maximum)
	}
	if s.Format != "int32" {
		t.Errorf("format = %q | Format 解析不正确", s.Format)
	}
	if len(s.Enum) != 3 {
		t.Fatalf("enum = %v | 枚举解析不正确", s.Enum)
	}
	if v, ok := s.Enum[0].(int64); !ok || v != 18 {
		t.Errorf("enum[0] = %v (%T) | 整数枚举应保持整型", s.Enum[0], s.Enum[0])
	}
	if v, ok := s.Default.(float64); !ok || v != 18 {
		t.Errorf("default = %v (%T) | 默认值解析不正确", s.Default, s.Default)
	}

	// Attributes do not disturb a plain quoted description. | 属性不会破坏普通的引号说明。
	plain, ok := parseParam(`name query string false "display name (full) | 完整名称"`)
	if !ok || plain.Description != "display name (full) | 完整名称" {
		t.Errorf("plain description = %q | 普通说明被误拆", plain.Description)
	}
}

func TestFormDataBecomesRequestBody(t *testing.T) {
	src := `package handlers

// Upload uploads a file. | Upload 上传文件。
//
// @Summary Upload | 上传
// @Tags files
// @Accept multipart
// @Produce json
// @Param name formData string true "display name | 显示名"
// @Param avatar formData file false "avatar | 头像"
// @Success 201 {object} string "created | 已创建"
// @Router /upload [post]
func Upload() {}
`
	blocks := parseFileAnnotations("handlers.go", src)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d | 注解块数量不正确", len(blocks))
	}
	op := blocks[0].operation("/upload", nil)
	if op.RequestBody == nil || !op.RequestBody.Required {
		t.Fatalf("form request body = %+v | 表单请求体缺失或必填标记错误", op.RequestBody)
	}
	mt, ok := op.RequestBody.Content["multipart/form-data"]
	if !ok || mt.Schema == nil {
		t.Fatalf("content = %v | multipart 媒体类型缺失", op.RequestBody.Content)
	}
	if mt.Schema.Type != "object" || len(mt.Schema.Properties) != 2 {
		t.Errorf("form schema = %+v | 表单 Schema 不正确", mt.Schema)
	}
	if len(mt.Schema.Required) != 1 || mt.Schema.Required[0] != "name" {
		t.Errorf("required = %v | 表单必填项不正确", mt.Schema.Required)
	}
	if avatar := mt.Schema.Properties["avatar"]; avatar == nil || avatar.Type != "string" || avatar.Format != "binary" {
		t.Errorf("avatar = %+v | 文件字段应生成 string/binary", avatar)
	}
	for _, p := range op.Parameters {
		if p.In == "formdata" || p.In == "form" {
			t.Errorf("formData must not stay a parameter | formData 不应保留为参数: %+v", p)
		}
	}
}

func TestAnnotationFileTokenBecomesBinary(t *testing.T) {
	a := parseResponses(`200 {file} file "download | 下载"`)
	if len(a) != 1 || a[0].schema == nil {
		t.Fatalf("responses = %+v | 响应解析失败", a)
	}
	if a[0].schema.Type != "string" || a[0].schema.Format != "binary" {
		t.Fatalf("file schema = %+v | file 应生成 string/binary", a[0].schema)
	}
	if a[0].Ref != "" {
		t.Fatalf("file must not become a component ref | file 不应变成组件引用: %q", a[0].Ref)
	}
}

func TestWrapperEnvelopeSyntax(t *testing.T) {
	s := schemaForResponse("object", "model.Result{data=model.User,code=int}")
	if s == nil || len(s.AllOf) != 2 {
		t.Fatalf("wrapper schema = %+v | 包装语法应生成 allOf", s)
	}
	if s.AllOf[0].Ref != "#/components/schemas/model.Result" {
		t.Errorf("wrapper ref = %q | 包装引用不正确", s.AllOf[0].Ref)
	}
	props := s.AllOf[1].Properties
	if props["data"] == nil || props["data"].Ref != "#/components/schemas/model.User" {
		t.Errorf("data = %+v | data 覆盖属性不正确", props["data"])
	}
	if props["code"] == nil || props["code"].Type != "integer" {
		t.Errorf("code = %+v | code 覆盖属性不正确", props["code"])
	}
}

func TestCompositeTypeTokens(t *testing.T) {
	arr := schemaForTypeName("[]model.User")
	if arr == nil || arr.Type != "array" || arr.Items == nil || arr.Items.Ref != "#/components/schemas/model.User" {
		t.Fatalf("[]model.User = %+v | 切片类型解析不正确", arr)
	}
	m := schemaForTypeName("map[string]int")
	if m == nil || m.Type != "object" {
		t.Fatalf("map[string]int = %+v | map 类型解析不正确", m)
	}
	additional, ok := m.AdditionalProperties.(*Schema)
	if !ok || additional.Type != "integer" {
		t.Fatalf("additionalProperties = %+v | map 值类型解析不正确", m.AdditionalProperties)
	}
}

func TestValidateGeneratedDocument(t *testing.T) {
	src := `package handlers

// Get returns one user. | Get 返回单个用户。
//
// @Summary Get user | 获取用户
// @Tags users
// @Param id path string true "user id | 用户 ID"
// @Security BearerAuth
// @Success 200 {object} string "ok | 成功"
// @Router /users/{id} [get]
func Get() {}
`
	annots := map[string]Annotation{}
	for _, a := range parseFileAnnotations("handlers.go", src) {
		annots[a.key()] = a
	}
	r := router.New()
	r.GET("/users/:id", noopHandler)
	doc := FromRouterWithAnnotations(r, Info{Title: "Demo", Version: "1.0.0"}, Options{}, annots, nil)
	doc.AddSecurityScheme("BearerAuth", SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "JWT"})
	if err := doc.Validate(); err != nil {
		t.Fatalf("generated document must validate | 生成的文档应通过校验: %v", err)
	}

	auto := FromRouter(r, Info{Title: "Auto", Version: "0.1.0"}, Options{})
	if err := auto.Validate(); err != nil {
		t.Fatalf("auto derived document must validate | 自动推导的文档应通过校验: %v", err)
	}
}

func TestValidateRejectsInvalidDocuments(t *testing.T) {
	okResponse := map[string]Response{"200": {Description: "ok | 成功"}}
	newDoc := func() *Document {
		return &Document{
			OpenAPI: SpecVersion,
			Info:    Info{Title: "Demo", Version: "1.0.0"},
			Paths: map[string]PathItem{
				"/users": {Get: &Operation{Responses: copyResponses(okResponse)}},
			},
		}
	}

	tests := []struct {
		name   string
		mutate func(*Document)
	}{
		{name: "missing title | 缺少标题", mutate: func(d *Document) { d.Info.Title = "" }},
		{name: "missing version | 缺少版本", mutate: func(d *Document) { d.Info.Version = "" }},
		{name: "path without slash | 路径缺少斜杠", mutate: func(d *Document) { d.Paths["users"] = d.Paths["/users"]; delete(d.Paths, "/users") }},
		{name: "no responses | 缺少响应", mutate: func(d *Document) { d.Paths["/users"].Get.Responses = nil }},
		{name: "bad response code | 非法响应码", mutate: func(d *Document) {
			d.Paths["/users"].Get.Responses = map[string]Response{"abc": {Description: "x"}}
		}},
		{name: "empty description | 空说明", mutate: func(d *Document) {
			d.Paths["/users"].Get.Responses = map[string]Response{"200": {Description: ""}}
		}},
		{name: "path param not declared | 路径参数未声明", mutate: func(d *Document) {
			d.Paths = map[string]PathItem{"/users/{id}": {Get: &Operation{Responses: copyResponses(okResponse)}}}
		}},
		{name: "unknown path param | 多余的路径参数", mutate: func(d *Document) {
			d.Paths["/users"].Get.Parameters = []Parameter{{
				Name: "id", In: "path", Required: true, Schema: &Schema{Type: "string"},
			}}
		}},
		{name: "path param not required | 路径参数非必填", mutate: func(d *Document) {
			d.Paths = map[string]PathItem{"/users/{id}": {Get: &Operation{
				Responses:  copyResponses(okResponse),
				Parameters: []Parameter{{Name: "id", In: "path", Schema: &Schema{Type: "string"}}},
			}}}
		}},
		{name: "duplicate operation id | operationId 重复", mutate: func(d *Document) {
			d.Paths["/users"].Get.OperationID = "same"
			d.Paths["/other"] = PathItem{Get: &Operation{OperationID: "same", Responses: copyResponses(okResponse)}}
		}},
		{name: "undeclared security | 未声明的安全方案", mutate: func(d *Document) {
			d.Paths["/users"].Get.Security = []SecurityRequirement{{"Missing": []string{}}}
		}},
		{name: "bad security scheme | 非法安全方案", mutate: func(d *Document) {
			d.Components.SecuritySchemes = map[string]*SecurityScheme{"Bad": {Type: "magic"}}
		}},
		{name: "dangling ref | 悬空引用", mutate: func(d *Document) {
			d.Components.Schemas = map[string]*Schema{"User": {Ref: "#/components/schemas/Missing"}}
		}},
		{name: "bad parameter location | 非法参数位置", mutate: func(d *Document) {
			d.Paths["/users"].Get.Parameters = []Parameter{{Name: "x", In: "body", Schema: &Schema{Type: "string"}}}
		}},
		{name: "unsupported version | 不支持的版本", mutate: func(d *Document) { d.OpenAPI = "2.0" }},
		{name: "server without url | 服务器缺少 url", mutate: func(d *Document) {
			d.Servers = []Server{{Description: "no url | 无地址"}}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := newDoc()
			tt.mutate(doc)
			err := doc.Validate()
			if err == nil {
				t.Fatalf("Validate accepted an invalid document | Validate 接受了非法文档")
			}
			if !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("error must wrap ErrInvalidDocument | 错误应包装 ErrInvalidDocument: %v", err)
			}
		})
	}

	var nilDoc *Document
	if err := nilDoc.Validate(); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("nil document must be rejected | nil 文档应被拒绝: %v", err)
	}
}

// copyResponses returns a shallow copy so that subtests do not share maps. | copyResponses 返回浅拷贝，
// 避免子测试共享映射。
func copyResponses(in map[string]Response) map[string]Response {
	out := make(map[string]Response, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func TestHandlerServesETagAndNotModified(t *testing.T) {
	doc := NewBuilder(Info{Title: "Demo", Version: "1.0.0"}).Build()
	doc.Paths["/ping"] = PathItem{Get: &Operation{Responses: map[string]Response{"200": {Description: "ok"}}}}
	r := router.New()
	h := Handler(doc, Options{})
	r.GET(DefaultPath, h)
	r.GET(DefaultDocsPath, h)

	for _, target := range []string{DefaultPath, DefaultDocsPath} {
		rec := serve(r, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200 | %s 状态码应为 200", target, rec.Code, target)
		}
		etag := rec.Header().Get("ETag")
		if etag == "" {
			t.Fatalf("%s must carry an ETag | %s 必须带 ETag", target, target)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s Cache-Control = %q | 缓存策略不正确", target, cc)
		}

		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("If-None-Match", etag)
		notModifiedRec := httptest.NewRecorder()
		r.ServeHTTP(notModifiedRec, req)
		if notModifiedRec.Code != http.StatusNotModified {
			t.Fatalf("%s conditional status = %d, want 304 | %s 条件请求状态码应为 304",
				target, notModifiedRec.Code, target)
		}
		if notModifiedRec.Body.Len() != 0 {
			t.Errorf("304 response must have no body | 304 响应不应有正文")
		}

		wildcard := httptest.NewRequest(http.MethodGet, target, nil)
		wildcard.Header.Set("If-None-Match", "*")
		wildcardRec := httptest.NewRecorder()
		r.ServeHTTP(wildcardRec, wildcard)
		if wildcardRec.Code != http.StatusNotModified {
			t.Errorf("%s wildcard status = %d, want 304 | 通配 If-None-Match 应返回 304",
				target, wildcardRec.Code)
		}
	}
}

func TestHandlerConcurrentRequests(t *testing.T) {
	doc := NewBuilder(Info{Title: "Demo", Version: "1.0.0"}).Build()
	doc.Paths["/ping"] = PathItem{Get: &Operation{Responses: map[string]Response{"200": {Description: "ok"}}}}
	r := router.New()
	h := Handler(doc, Options{})
	r.GET(DefaultPath, h)
	r.GET(DefaultDocsPath, h)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				for _, target := range []string{DefaultPath, DefaultDocsPath} {
					rec := serve(r, http.MethodGet, target)
					if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
						t.Errorf("%s status = %d, body = %d | 并发请求结果不正确",
							target, rec.Code, rec.Body.Len())
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

func TestViewerShowsExtendedOperationFacts(t *testing.T) {
	doc := NewBuilder(Info{Title: "Demo", Version: "1.0.0"}).Build()
	doc.Paths["/users"] = PathItem{Post: &Operation{
		OperationID: "create-user",
		Deprecated:  true,
		Security:    []SecurityRequirement{{"BearerAuth": []string{}}},
		Responses: map[string]Response{
			"201": {
				Description: "created | 已创建",
				Headers:     map[string]*Header{"Location": {Description: "url | 地址", Schema: &Schema{Type: "string"}}},
			},
		},
	}}
	page, err := renderViewer(doc, Options{})
	if err != nil {
		t.Fatalf("renderViewer: %v", err)
	}
	html := string(page)
	for _, want := range []string{"deprecated", "BearerAuth", "Location", "create-user"} {
		if !strings.Contains(html, want) {
			t.Errorf("viewer must mention %q | 查看器应展示 %q", want, want)
		}
	}
}
