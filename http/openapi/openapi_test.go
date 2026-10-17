package openapi

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/http/router"
	"go.yaml.in/yaml/v3"
)

// noopHandler is a placeholder route handler used across the tests. | noopHandler 是各测试共用的占位路由处理器。
func noopHandler(c *router.Ctx) {}

// testAddress is a nested struct used to verify schema generation. | testAddress 是用于校验 Schema 生成的嵌套结构体。
type testAddress struct {
	City string `json:"city"`
	Zip  string `json:"zip,omitempty"`
}

// testUser exercises json tags, omitempty, slices, maps and pointers. | testUser 覆盖 json tag、omitempty、切片、map 与指针。
type testUser struct {
	ID       int64             `json:"id"`
	Name     string            `json:"name"`
	Nick     string            `json:"nick,omitempty"`
	Age      int               `json:"age"`
	Active   bool              `json:"active"`
	Score    float64           `json:"score,omitempty"`
	Tags     []string          `json:"tags"`
	Address  *testAddress      `json:"address"`
	Extra    map[string]string `json:"extra,omitempty"`
	Ignored  string            `json:"-"`
	Nickname string
}

// serve performs one request against the router and returns the recorder. | serve 对路由发起一次请求并返回记录器。
func serve(r *router.Router, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// findParam returns the parameter with the given name, or nil. | findParam 返回指定名称的参数，找不到时返回 nil。
func findParam(params []Parameter, name string) *Parameter {
	for i := range params {
		if params[i].Name == name {
			return &params[i]
		}
	}
	return nil
}

// operationsOf returns every non-nil operation of a path item. | operationsOf 返回路径项中所有非空操作。
func operationsOf(pi PathItem) []*Operation {
	return []*Operation{pi.Get, pi.Post, pi.Put, pi.Patch, pi.Delete, pi.Head, pi.Options}
}

// hasString reports whether the slice contains want. | hasString 报告切片是否包含 want。
func hasString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// normalizeNumbers rewrites YAML-decoded integers to float64 so that they can be
// compared with JSON-decoded values. | normalizeNumbers 把 YAML 解出的整数改写为 float64，
// 以便与 JSON 解出的值比较。
func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeNumbers(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeNumbers(val)
		}
		return out
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	default:
		return v
	}
}

func TestFromRouterConvertsPatternParams(t *testing.T) {
	r := router.New()
	r.GET("/report/:id-xx.png", noopHandler)

	doc := FromRouter(r, Info{Title: "Demo", Version: "1.0.0"}, Options{})

	if _, ok := doc.Paths["/report/:id-xx.png"]; ok {
		t.Fatalf("raw pattern leaked into paths | 原始模式不应出现在 paths 中: %v", doc.Paths)
	}
	item, ok := doc.Paths["/report/{id}-xx.png"]
	if !ok {
		t.Fatalf("missing converted path | 缺少转换后的路径: %v", doc.Paths)
	}
	if item.Get == nil {
		t.Fatalf("missing GET operation | 缺少 GET 操作")
	}
	p := findParam(item.Get.Parameters, "id")
	if p == nil {
		t.Fatalf("missing id parameter | 缺少 id 参数: %+v", item.Get.Parameters)
	}
	if p.In != "path" {
		t.Errorf("param in = %q, want path | 参数位置应为 path", p.In)
	}
	if !p.Required {
		t.Errorf("path param must be required | 路径参数必须必填")
	}
}

func TestFromRouterHandlesWildcardAndTrailingSlash(t *testing.T) {
	r := router.New()
	r.GET("/static/*filepath", noopHandler)
	r.GET("/users/", noopHandler)
	r.POST("/users/", noopHandler)

	doc := FromRouter(r, Info{Title: "Demo", Version: "1.0.0"}, Options{})

	item, ok := doc.Paths["/static/{filepath}"]
	if !ok {
		t.Fatalf("missing wildcard path | 缺少通配路径: %v", doc.Paths)
	}
	if item.Get == nil {
		t.Fatalf("missing GET operation for wildcard | 通配路径缺少 GET 操作")
	}
	p := findParam(item.Get.Parameters, "filepath")
	if p == nil || p.In != "path" || !p.Required {
		t.Fatalf("wildcard param = %+v, want in:path required | 通配参数应为 in:path 且必填", p)
	}

	trailing, ok := doc.Paths["/users/"]
	if !ok {
		t.Fatalf("trailing slash path lost | 尾斜杠路径丢失: %v", doc.Paths)
	}
	if trailing.Get == nil || trailing.Post == nil {
		t.Fatalf("trailing slash path must keep both methods | 尾斜杠路径应保留两个方法")
	}
	if len(trailing.Get.Parameters) != 0 {
		t.Fatalf("trailing slash produced parameters | 尾斜杠不应产生参数: %+v", trailing.Get.Parameters)
	}

	for path, pi := range doc.Paths {
		for _, op := range operationsOf(pi) {
			if op == nil {
				continue
			}
			for _, param := range op.Parameters {
				if param.Name == "" {
					t.Errorf("empty parameter name on %s | %s 上出现空名参数", path, path)
				}
			}
		}
	}
}

func TestJSONHasOpenAPIVersion(t *testing.T) {
	if SpecVersion != "3.0.3" {
		t.Fatalf("SpecVersion = %q, want 3.0.3 | 版本号必须为 3.0.3", SpecVersion)
	}

	doc := NewBuilder(Info{Title: "Demo", Version: "1.0.0"}).Build()
	raw, err := doc.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got map[string]any
	if err := jsonx.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["openapi"] != "3.0.3" {
		t.Errorf("openapi = %v, want 3.0.3 | openapi 字段应为 3.0.3", got["openapi"])
	}
	if got["paths"] == nil {
		t.Errorf("paths must always be present | paths 必须始终存在")
	}

	fresh := &Document{}
	raw, err = fresh.JSON()
	if err != nil {
		t.Fatalf("zero value JSON: %v", err)
	}
	var zero map[string]any
	if err := jsonx.Unmarshal(raw, &zero); err != nil {
		t.Fatalf("unmarshal zero: %v", err)
	}
	if zero["openapi"] != "3.0.3" {
		t.Errorf("zero value openapi = %v, want 3.0.3 | 零值文档也应补齐版本号", zero["openapi"])
	}
}

func TestSchemaFromStruct(t *testing.T) {
	b := NewBuilder(Info{Title: "Demo", Version: "1.0.0"})
	ref := b.RegisterSchema("User", &testUser{})
	if ref != "#/components/schemas/User" {
		t.Fatalf("ref = %q, want #/components/schemas/User | 引用格式不正确", ref)
	}
	doc := b.Build()
	s := doc.Components.Schemas["User"]
	if s == nil {
		t.Fatalf("schema not registered | Schema 未注册")
	}
	if s.Type != "object" {
		t.Fatalf("type = %q, want object | 顶层类型应为 object", s.Type)
	}

	id := s.Properties["id"]
	if id == nil || id.Type != "integer" || id.Format != "int64" {
		t.Errorf("id schema = %+v, want integer/int64 | id 应为 integer/int64", id)
	}
	tags := s.Properties["tags"]
	if tags == nil || tags.Type != "array" || tags.Items == nil || tags.Items.Type != "string" {
		t.Errorf("tags schema = %+v, want array of string | tags 应为 string 数组", tags)
	}
	addr := s.Properties["address"]
	if addr == nil || addr.Type != "object" || addr.Properties["city"] == nil {
		t.Errorf("address schema = %+v, want nested object | address 应为嵌套对象", addr)
	}
	if extra := s.Properties["extra"]; extra == nil || extra.Type != "object" {
		t.Errorf("extra schema = %+v, want object | map 字段应为 object", extra)
	}
	if _, ok := s.Properties["-"]; ok {
		t.Errorf(`json:"-" field leaked into schema | json:"-" 字段不应出现`)
	}
	if _, ok := s.Properties["Ignored"]; ok {
		t.Errorf("ignored field leaked into schema | 被忽略字段不应出现")
	}
	if _, ok := s.Properties["Nickname"]; !ok {
		t.Errorf("untagged field must keep its Go name | 无 tag 字段应保留 Go 字段名")
	}
	if s.Properties["city"] != nil {
		t.Errorf("nested properties must not be flattened | 嵌套字段不应被提升")
	}

	for _, want := range []string{"id", "name", "age", "active", "tags", "Nickname"} {
		if !hasString(s.Required, want) {
			t.Errorf("required missing %q | required 缺少 %q: %v", want, want, s.Required)
		}
	}
	for _, unwanted := range []string{"nick", "score", "address", "extra"} {
		if hasString(s.Required, unwanted) {
			t.Errorf("required must not contain %q | required 不应包含 %q: %v", unwanted, unwanted, s.Required)
		}
	}
}

func TestViewerHTMLHasNoExternalCDN(t *testing.T) {
	doc := NewBuilder(Info{Title: "Demo", Version: "1.0.0"}).Build()
	h := Handler(doc, Options{})
	r := router.New()
	r.GET("/docs", h)

	rec := serve(r, http.MethodGet, "/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 | 状态码应为 200", rec.Code)
	}
	body := strings.ToLower(rec.Body.String())
	for _, bad := range []string{
		`src="http`, `src='http`, `href="http`, `href='http`,
		`url(http`, `@import`, `<script src=`, `<link`,
	} {
		if strings.Contains(body, bad) {
			t.Errorf("viewer must not reference external assets | 查看器不得引用外部资源: %q", bad)
		}
	}
	if !strings.Contains(body, "<!doctype html") {
		t.Errorf("viewer must be a complete HTML page | 查看器必须是完整 HTML 页面")
	}
}

func TestYAMLValid(t *testing.T) {
	b := NewBuilder(Info{Title: "Demo", Version: "1.2.3", Description: "示例文档 | sample document"})
	b.AddServer(Server{URL: "https://api.example.com", Description: "prod"})
	ref := b.RegisterSchema("User", &testUser{})
	b.AddRoute("GET", "/users/:id", Operation{
		Summary: "Get user | 获取用户",
		Tags:    []string{"users"},
		Parameters: []Parameter{
			{Name: "verbose", In: "query", Schema: &Schema{Type: "boolean"}},
		},
		Responses: map[string]Response{
			"200": {
				Description: "OK",
				Content: map[string]MediaType{
					"application/json": {Schema: &Schema{Ref: ref, Example: 3}},
				},
			},
		},
	})
	doc := b.Build()

	yraw, err := doc.YAML()
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	var fromYAML any
	if err := yaml.Unmarshal(yraw, &fromYAML); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\n%s", err, yraw)
	}

	jraw, err := doc.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var fromJSON any
	if err := jsonx.Unmarshal(jraw, &fromJSON); err != nil {
		t.Fatalf("jsonx.Unmarshal: %v", err)
	}

	if !reflect.DeepEqual(normalizeNumbers(fromYAML), fromJSON) {
		t.Errorf("YAML and JSON documents differ\nYAML: %v\nJSON: %v", fromYAML, fromJSON)
	}
	if !strings.Contains(string(yraw), "$ref: '#/components/schemas/User'") &&
		!strings.Contains(string(yraw), "$ref: \"#/components/schemas/User\"") {
		t.Errorf("YAML must keep $ref keys | YAML 必须保留 $ref 键:\n%s", yraw)
	}
}

func TestHandlerServesDocAndViewer(t *testing.T) {
	doc := NewBuilder(Info{Title: "Demo", Version: "1.0.0"}).Build()

	t.Run("default paths | 默认路径", func(t *testing.T) {
		h := Handler(doc, Options{})
		r := router.New()
		r.GET(DefaultPath, h)
		r.GET(DefaultDocsPath, h)

		rec := serve(r, http.MethodGet, DefaultPath)
		if rec.Code != http.StatusOK {
			t.Fatalf("doc status = %d, want 200 | 文档状态码应为 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("doc content-type = %q | 文档 Content-Type 不正确", ct)
		}
		var got map[string]any
		if err := jsonx.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal served doc: %v", err)
		}
		if got["openapi"] != "3.0.3" {
			t.Errorf("served openapi = %v | 提供的文档版本号不正确", got["openapi"])
		}

		rec = serve(r, http.MethodGet, DefaultDocsPath)
		if rec.Code != http.StatusOK {
			t.Fatalf("viewer status = %d, want 200 | 查看器状态码应为 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Errorf("viewer content-type = %q | 查看器 Content-Type 不正确", ct)
		}
		if body := rec.Body.String(); !strings.Contains(body, "Demo") {
			t.Errorf("viewer must show the document title | 查看器应展示文档标题")
		}
	})

	t.Run("custom paths | 自定义路径", func(t *testing.T) {
		h := Handler(doc, Options{Path: "/api/spec.json", DocsPath: "/api/docs"})
		r := router.New()
		r.GET("/api/spec.json", h)
		r.GET("/api/docs", h)

		if rec := serve(r, http.MethodGet, "/api/spec.json"); rec.Code != http.StatusOK {
			t.Errorf("custom doc status = %d | 自定义文档路径状态码不正确", rec.Code)
		}
		if rec := serve(r, http.MethodGet, "/api/docs"); rec.Code != http.StatusOK {
			t.Errorf("custom viewer status = %d | 自定义查看器路径状态码不正确", rec.Code)
		}
	})
}

func TestBlockedReturnsNotFound(t *testing.T) {
	doc := NewBuilder(Info{Title: "Demo", Version: "1.0.0"}).Build()
	h := Handler(doc, Options{Blocked: true})
	r := router.New()
	r.GET(DefaultPath, h)
	r.GET(DefaultDocsPath, h)

	for _, target := range []string{DefaultPath, DefaultDocsPath} {
		if rec := serve(r, http.MethodGet, target); rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404 | %s 状态码应为 404", target, rec.Code, target)
		}
	}
}
