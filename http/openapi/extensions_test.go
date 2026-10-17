package openapi

import (
	"errors"
	"strconv"
	"testing"

	"github.com/shibingli/yd/core/jsonx"
)

// floatPtr returns a pointer to v. | floatPtr 返回指向 v 的指针。
func floatPtr(v float64) *float64 { return &v }

// nodeAtPath walks decoded JSON, accepting object keys and array indices. | nodeAtPath 遍历解码后的
// JSON，同时接受对象键与数组下标。
func nodeAtPath(t *testing.T, root map[string]any, path []string) map[string]any {
	t.Helper()
	var current any = root
	for _, key := range path {
		switch node := current.(type) {
		case map[string]any:
			current = node[key]
		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(node) {
				t.Fatalf("bad array index %q in %v | 路径 %v 的数组下标 %q 非法", key, path, path, key)
			}
			current = node[index]
		default:
			t.Fatalf("cannot descend into %v at %q | 无法在 %q 处继续下探", current, key, key)
		}
	}
	out, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("path %v does not point at an object | 路径 %v 未指向对象", path, path)
	}
	return out
}

// TestExtensionsRoundTrip verifies that x-* members survive every struct based object. | TestExtensionsRoundTrip
// 校验 x-* 成员在全部以结构体表示的对象上都能往返。
func TestExtensionsRoundTrip(t *testing.T) {
	doc := NewBuilder(Info{Title: "Demo", Version: "1.0.0"}).Build()
	doc.Extensions = map[string]any{"x-root": true}
	doc.Info.Extensions = map[string]any{"x-info": "yes"}
	doc.Info.Contact = Contact{Name: "Team", Extensions: map[string]any{"x-contact": "c"}}
	doc.Info.License = License{Name: "Apache-2.0", Extensions: map[string]any{"x-license": "l"}}
	doc.Components.Extensions = map[string]any{"x-components": 1}
	doc.AddServer(Server{
		URL:        "https://api.example.com",
		Extensions: map[string]any{"x-server": "s"},
		Variables: map[string]ServerVariable{
			"env": {Default: "api", Extensions: map[string]any{"x-variable": "v"}},
		},
	})
	doc.AddTag(Tag{
		Name:         "users",
		Extensions:   map[string]any{"x-tag": "t"},
		ExternalDocs: &ExternalDocs{URL: "https://example.com", Extensions: map[string]any{"x-doc": "d"}},
	})
	doc.AddSecurityScheme("BearerAuth", SecurityScheme{
		Type:       "http",
		Scheme:     "bearer",
		Extensions: map[string]any{"x-scheme": "s"},
	})
	doc.AddSecurityScheme("OAuth", SecurityScheme{
		Type:       "oauth2",
		Extensions: map[string]any{"x-oauth": "o"},
		Flows: &OAuthFlows{
			Extensions: map[string]any{"x-flows": "fs"},
			Implicit: &OAuthFlow{
				AuthorizationURL: "https://example.com/auth",
				Scopes:           map[string]string{"read": "read"},
				Extensions:       map[string]any{"x-flow": "f"},
			},
		},
	})
	doc.Paths["/users"] = PathItem{
		Extensions: map[string]any{"x-path": "p"},
		Parameters: []Parameter{{
			Name:       "trace",
			In:         "header",
			Schema:     &Schema{Type: "boolean", Extensions: map[string]any{"x-param-schema": "ps"}},
			Extensions: map[string]any{"x-param": "pa"},
		}},
		Post: &Operation{
			OperationID:  "create-user",
			Extensions:   map[string]any{"x-operation": "op"},
			ExternalDocs: &ExternalDocs{URL: "https://example.com/op", Extensions: map[string]any{"x-op-doc": "od"}},
			RequestBody: &RequestBody{
				Extensions: map[string]any{"x-body": "b"},
				Content: map[string]MediaType{
					"application/json": {
						Schema:     &Schema{Type: "object"},
						Extensions: map[string]any{"x-media": "m"},
						Encoding: map[string]Encoding{
							"id": {ContentType: "text/plain", Extensions: map[string]any{"x-encoding": "e"}},
						},
					},
				},
			},
			Responses: map[string]Response{
				"200": {
					Description: "ok",
					Extensions:  map[string]any{"x-response": "r"},
					Headers: map[string]*Header{
						"X-Trace": {Schema: &Schema{Type: "string"}, Extensions: map[string]any{"x-header": "h"}},
					},
					Links: map[string]*Link{
						"self": {OperationID: "get-user", Extensions: map[string]any{"x-link": "l"}},
					},
					Content: map[string]MediaType{
						"application/json": {
							Schema:     &Schema{Type: "object", Extensions: map[string]any{"x-response-schema": "rs"}},
							Extensions: map[string]any{"x-response-media": "rm"},
							Examples: map[string]Example{
								"sample": {Value: "v", Extensions: map[string]any{"x-example": "ex"}},
							},
						},
					},
				},
			},
			Callbacks: map[string]Callback{
				"onCreate": {
					"{$request.body#/url}": PathItem{
						Extensions: map[string]any{"x-callback-path": "cp"},
						Post:       &Operation{Responses: map[string]Response{"200": {Description: "ok"}}},
					},
				},
			},
		},
	}
	doc.Components.Schemas["User"] = &Schema{
		Type:          "object",
		Extensions:    map[string]any{"x-user": "u"},
		Discriminator: &Discriminator{PropertyName: "kind", Extensions: map[string]any{"x-discriminator": "d"}},
		XML:           &XML{Name: "user", Extensions: map[string]any{"x-xml": "x"}},
		ExternalDocs:  &ExternalDocs{URL: "https://example.com/user", Extensions: map[string]any{"x-schema-doc": "sd"}},
	}

	raw, err := doc.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var tree map[string]any
	if err := jsonx.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}
	want := []struct {
		path []string
		key  string
	}{
		{nil, "x-root"},
		{[]string{"info"}, "x-info"},
		{[]string{"info", "contact"}, "x-contact"},
		{[]string{"info", "license"}, "x-license"},
		{[]string{"components"}, "x-components"},
		{[]string{"servers", "0"}, "x-server"},
		{[]string{"servers", "0", "variables", "env"}, "x-variable"},
		{[]string{"tags", "0"}, "x-tag"},
		{[]string{"tags", "0", "externalDocs"}, "x-doc"},
		{[]string{"components", "securitySchemes", "BearerAuth"}, "x-scheme"},
		{[]string{"components", "securitySchemes", "OAuth"}, "x-oauth"},
		{[]string{"components", "securitySchemes", "OAuth", "flows"}, "x-flows"},
		{[]string{"components", "securitySchemes", "OAuth", "flows", "implicit"}, "x-flow"},
		{[]string{"paths", "/users"}, "x-path"},
		{[]string{"paths", "/users", "post"}, "x-operation"},
		{[]string{"paths", "/users", "post", "externalDocs"}, "x-op-doc"},
		{[]string{"paths", "/users", "post", "requestBody"}, "x-body"},
		{[]string{"paths", "/users", "post", "requestBody", "content", "application/json"}, "x-media"},
		{[]string{"paths", "/users", "post", "requestBody", "content", "application/json", "encoding", "id"}, "x-encoding"},
		{[]string{"paths", "/users", "post", "responses", "200"}, "x-response"},
		{[]string{"paths", "/users", "post", "responses", "200", "headers", "X-Trace"}, "x-header"},
		{[]string{"paths", "/users", "post", "responses", "200", "links", "self"}, "x-link"},
		{[]string{"paths", "/users", "post", "responses", "200", "content", "application/json"}, "x-response-media"},
		{[]string{"paths", "/users", "post", "responses", "200", "content", "application/json", "schema"}, "x-response-schema"},
		{[]string{"paths", "/users", "post", "responses", "200", "content", "application/json", "examples", "sample"}, "x-example"},
		{[]string{"paths", "/users", "post", "callbacks", "onCreate", "{$request.body#/url}"}, "x-callback-path"},
		{[]string{"components", "schemas", "User"}, "x-user"},
		{[]string{"components", "schemas", "User", "discriminator"}, "x-discriminator"},
		{[]string{"components", "schemas", "User", "xml"}, "x-xml"},
		{[]string{"components", "schemas", "User", "externalDocs"}, "x-schema-doc"},
	}
	for _, w := range want {
		node := nodeAtPath(t, tree, w.path)
		if _, ok := node[w.key]; !ok {
			t.Errorf("missing extension %q at %v | %v 缺少扩展 %q", w.key, w.path, w.path, w.key)
		}
	}

	var back Document
	if err := jsonx.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal document: %v", err)
	}
	if back.Extensions["x-root"] != true || back.Info.Extensions["x-info"] != "yes" {
		t.Errorf("root or info extension lost | 根或 info 扩展丢失")
	}
	if back.Info.Contact.Extensions["x-contact"] != "c" || back.Info.License.Extensions["x-license"] != "l" {
		t.Errorf("contact or license extension lost | contact 或 license 扩展丢失")
	}
	path := back.Paths["/users"]
	if path.Extensions["x-path"] != "p" || path.Post.Extensions["x-operation"] != "op" {
		t.Errorf("path or operation extension lost | 路径或操作扩展丢失")
	}
	if path.Post.RequestBody.Extensions["x-body"] != "b" {
		t.Errorf("request body extension lost | 请求体扩展丢失")
	}
	response := path.Post.Responses["200"]
	if response.Extensions["x-response"] != "r" || response.Headers["X-Trace"].Extensions["x-header"] != "h" {
		t.Errorf("response or header extension lost | 响应或响应头扩展丢失")
	}
	if response.Links["self"].Extensions["x-link"] != "l" {
		t.Errorf("link extension lost | 链接扩展丢失")
	}
	if response.Content["application/json"].Examples["sample"].Extensions["x-example"] != "ex" {
		t.Errorf("example extension lost | 示例扩展丢失")
	}
	if path.Post.Callbacks["onCreate"]["{$request.body#/url}"].Extensions["x-callback-path"] != "cp" {
		t.Errorf("callback extension lost | 回调扩展丢失")
	}
	schema := back.Components.Schemas["User"]
	if schema.Extensions["x-user"] != "u" || schema.Discriminator.Extensions["x-discriminator"] != "d" {
		t.Errorf("schema or discriminator extension lost | Schema 或 discriminator 扩展丢失")
	}
	if schema.XML.Extensions["x-xml"] != "x" || schema.ExternalDocs.Extensions["x-schema-doc"] != "sd" {
		t.Errorf("xml or schema externalDocs extension lost | xml 或 Schema externalDocs 扩展丢失")
	}
	if back.Servers[0].Extensions["x-server"] != "s" || back.Servers[0].Variables["env"].Extensions["x-variable"] != "v" {
		t.Errorf("server extension lost | 服务器扩展丢失")
	}
	if back.Components.SecuritySchemes["OAuth"].Flows.Implicit.Extensions["x-flow"] != "f" {
		t.Errorf("oauth flow extension lost | OAuth 流程扩展丢失")
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("round-tripped document must validate | 往返后的文档应通过校验: %v", err)
	}
}

func TestExtensionsRejectInvalidNames(t *testing.T) {
	doc := NewBuilder(Info{Title: "Demo", Version: "1.0.0"}).Build()
	doc.Extensions = map[string]any{"bad": true}
	if _, err := doc.JSON(); !errors.Is(err, ErrInvalidExtension) {
		t.Fatalf("error = %v, want ErrInvalidExtension | 错误应包装 ErrInvalidExtension", err)
	}
}

func TestSchemaDialectRoundTrip(t *testing.T) {
	v31 := &Schema{
		Types:                 []string{"string", "null"},
		ExclusiveMinimumValue: floatPtr(1.5),
		ExclusiveMaximumValue: floatPtr(9.5),
	}
	raw, err := jsonx.Marshal(v31)
	if err != nil {
		t.Fatalf("marshal 3.1 schema: %v", err)
	}
	var tree map[string]any
	if err := jsonx.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("unmarshal 3.1 schema: %v", err)
	}
	types, ok := tree["type"].([]any)
	if !ok || len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Fatalf("type = %v | 3.1 类型数组丢失", tree["type"])
	}
	if tree["exclusiveMinimum"] != 1.5 || tree["exclusiveMaximum"] != 9.5 {
		t.Errorf("exclusive bounds = %v/%v | 3.1 数值开界丢失", tree["exclusiveMinimum"], tree["exclusiveMaximum"])
	}
	var back Schema
	if err := jsonx.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal 3.1 schema back: %v", err)
	}
	if len(back.Types) != 2 || back.Types[1] != "null" {
		t.Errorf("types = %v | 3.1 类型数组未回读", back.Types)
	}
	if back.ExclusiveMinimumValue == nil || *back.ExclusiveMinimumValue != 1.5 {
		t.Errorf("exclusiveMinimum = %v | 3.1 数值开界未回读", back.ExclusiveMinimumValue)
	}

	v30 := &Schema{Type: "number", ExclusiveMinimum: true, ExclusiveMaximum: true}
	raw, err = jsonx.Marshal(v30)
	if err != nil {
		t.Fatalf("marshal 3.0 schema: %v", err)
	}
	tree = map[string]any{}
	if err := jsonx.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("unmarshal 3.0 schema: %v", err)
	}
	if tree["exclusiveMinimum"] != true || tree["exclusiveMaximum"] != true {
		t.Errorf("3.0 exclusive bounds = %v/%v | 3.0 布尔开界丢失", tree["exclusiveMinimum"], tree["exclusiveMaximum"])
	}
	back = Schema{}
	if err := jsonx.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal 3.0 schema back: %v", err)
	}
	if !back.ExclusiveMinimum || back.ExclusiveMinimumValue != nil {
		t.Errorf("3.0 exclusive bound not read back | 3.0 布尔开界未回读: %+v", back)
	}
}

func TestValidateDialectCompatibility(t *testing.T) {
	base := func(version string) *Document {
		return &Document{
			OpenAPI: version,
			Info:    Info{Title: "Demo", Version: "1.0.0"},
			Paths: map[string]PathItem{
				"/users": {Get: &Operation{Responses: map[string]Response{"200": {Description: "ok"}}}},
			},
			Components: Components{Schemas: map[string]*Schema{}},
		}
	}

	tests := []struct {
		name    string
		version string
		mutate  func(*Document)
		wantOK  bool
	}{
		{name: "3.0 with 3.1 schema keyword | 3.0 使用 3.1 关键字", version: SpecVersion, mutate: func(d *Document) {
			d.Components.Schemas["X"] = &Schema{Types: []string{"string"}}
		}},
		{name: "3.0 with webhooks | 3.0 使用 webhooks", version: SpecVersion, mutate: func(d *Document) {
			d.Webhooks = map[string]PathItem{"created": {Post: &Operation{Responses: map[string]Response{"200": {Description: "ok"}}}}}
		}},
		{name: "3.0 with mutualTLS | 3.0 使用 mutualTLS", version: SpecVersion, mutate: func(d *Document) {
			d.Components.SecuritySchemes = map[string]*SecurityScheme{"mtls": {Type: "mutualTLS"}}
		}},
		{name: "3.1 with nullable | 3.1 使用 nullable", version: LatestSpecVersion, mutate: func(d *Document) {
			d.Components.Schemas["X"] = &Schema{Type: "string", Nullable: true}
		}},
		{name: "3.1 with boolean exclusive | 3.1 使用布尔开界", version: LatestSpecVersion, mutate: func(d *Document) {
			d.Components.Schemas["X"] = &Schema{Type: "number", ExclusiveMinimum: true}
		}},
		{name: "3.1 with numeric exclusive | 3.1 使用数值开界", version: LatestSpecVersion, wantOK: true, mutate: func(d *Document) {
			d.Components.Schemas["X"] = &Schema{Type: "number", ExclusiveMinimumValue: floatPtr(0)}
		}},
		{name: "3.1 with webhooks | 3.1 使用 webhooks", version: LatestSpecVersion, wantOK: true, mutate: func(d *Document) {
			d.Webhooks = map[string]PathItem{"created": {Post: &Operation{Responses: map[string]Response{"200": {Description: "ok"}}}}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := base(tt.version)
			tt.mutate(doc)
			err := doc.Validate()
			if tt.wantOK {
				if err != nil {
					t.Fatalf("Validate: %v | 合法文档不应报错", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("error = %v, want ErrInvalidDocument | 方言混用应被拒绝", err)
			}
		})
	}
}
