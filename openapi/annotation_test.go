package openapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/shibingli/yd/router"
)

// annotatedSource is a fake handler file written into a memory file system. | annotatedSource 是写入内存文件系统的假处理器源码。
const annotatedSource = `package api

import "github.com/shibingli/yd/router"

// GetUser returns one user as an HTML fragment. | GetUser 以 HTML 片段返回单个用户。
//
// @Summary Get user detail | 获取用户详情
// @Description Returns the user detail fragment. | 返回用户详情片段。
// @Tags users
// @Accept json
// @Produce html,json
// @Param id path string true "user id | 用户 ID"
// @Param body body CreateUserRequest true "create payload | 创建参数"
// @Success 200 {object} model.User "user detail | 用户详情"
// @Success 422 {object} htmx.ErrorFragment "validation failed | 校验失败"
// @Failure 404 {string} string "not found | 未找到"
// @Security BearerAuth
// @Router /users/{id} [get]
func GetUser(c *router.Ctx) {}

// CreateUser creates one user. | CreateUser 创建单个用户。
//
// @Summary Create user | 创建用户
// @Tags users
// @Success 201 {object} model.User "created | 已创建"
// @Router /users [post]
func CreateUser(c *router.Ctx) {}

// LegacyHandler misses tags and success responses. | LegacyHandler 缺少 Tags 与 Success。
//
// @Summary Legacy endpoint | 遗留端点
// @Router /legacy [get]
func LegacyHandler(c *router.Ctx) {}

// NoRouterBlock carries annotations without a @Router directive. | NoRouterBlock 带注解但没有 @Router。
//
// @Summary Dangling endpoint | 游离端点
func NoRouterBlock(c *router.Ctx) {}
`

// annotatedTestSource must be skipped by the parser. | annotatedTestSource 必须被解析器跳过。
const annotatedTestSource = `package api

// @Summary Skipped | 被跳过
// @Router /skipped [get]
func TestSomething(t *testing.T) {}
`

// memFSWithAnnotations builds a memory file system holding the fixtures. | memFSWithAnnotations 构造存放测试夹具的内存文件系统。
func memFSWithAnnotations(t *testing.T) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/api/handlers.go", []byte(annotatedSource), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := afero.WriteFile(fs, "/api/handlers_test.go", []byte(annotatedTestSource), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return fs
}

func TestParseAnnotationsFromSource(t *testing.T) {
	annots, err := ParseAnnotations(memFSWithAnnotations(t), []string{"/api"})
	if err != nil {
		t.Fatalf("ParseAnnotations: %v", err)
	}
	if len(annots) != 3 {
		t.Fatalf("annotation count = %d, want 3 | 注解数量应为 3: %v", len(annots), annots)
	}

	a, ok := annots["GET /users/{id}"]
	if !ok {
		t.Fatalf("missing key GET /users/{id} | 缺少键 GET /users/{id}: %v", annots)
	}
	if a.Summary != "Get user detail | 获取用户详情" {
		t.Errorf("summary = %q | Summary 应保留完整中英文本", a.Summary)
	}
	if !strings.Contains(a.Description, "|") {
		t.Errorf("description must keep the bilingual separator | Description 应保留中英分隔符: %q", a.Description)
	}
	if !hasString(a.Tags, "users") {
		t.Errorf("tags = %v | Tags 解析不正确", a.Tags)
	}
	if !hasString(a.Accept, "json") {
		t.Errorf("accept = %v | Accept 解析不正确", a.Accept)
	}
	if !hasString(a.Produce, "html") || !hasString(a.Produce, "json") {
		t.Errorf("produce = %v | Produce 解析不正确", a.Produce)
	}
	if a.RouterMethod != "GET" || a.RouterPath != "/users/{id}" {
		t.Errorf("router = %q %q | @Router 解析不正确", a.RouterMethod, a.RouterPath)
	}
	if a.Pos == "" || !strings.Contains(a.Pos, "handlers.go:") {
		t.Errorf("pos = %q | Pos 应包含 文件:行号", a.Pos)
	}
	if !hasString(a.Security, "BearerAuth") {
		t.Errorf("security = %v | Security 解析不正确", a.Security)
	}

	var idParam, bodyParam *AnnotationParam
	for i := range a.Params {
		switch a.Params[i].Name {
		case "id":
			idParam = &a.Params[i]
		case "body":
			bodyParam = &a.Params[i]
		}
	}
	if idParam == nil || idParam.In != "path" || idParam.Type != "string" || !idParam.Required {
		t.Fatalf("id param = %+v | id 参数解析不正确", idParam)
	}
	if idParam.Schema == nil || idParam.Schema.Type != "string" {
		t.Errorf("id schema = %+v | 标量类型应生成基础 Schema", idParam.Schema)
	}
	if bodyParam == nil || bodyParam.Schema == nil || bodyParam.Schema.Ref != "#/components/schemas/CreateUserRequest" {
		t.Fatalf("body param = %+v | body 参数应生成模型引用", bodyParam)
	}

	if len(a.Success) != 2 {
		t.Fatalf("success count = %d, want 2 | Success 数量应为 2: %+v", len(a.Success), a.Success)
	}
	if a.Success[0].Code != 200 || a.Success[0].Ref != "#/components/schemas/model.User" {
		t.Errorf("success[0] = %+v | 200 响应应引用 model.User", a.Success[0])
	}
	if a.Success[1].Code != 422 || a.Success[1].Ref != "#/components/schemas/htmx.ErrorFragment" {
		t.Errorf("success[1] = %+v | 422 响应应引用 htmx.ErrorFragment", a.Success[1])
	}
	if len(a.Failure) != 1 || a.Failure[0].Code != 404 || a.Failure[0].Type != "string" {
		t.Fatalf("failure = %+v | Failure 解析不正确", a.Failure)
	}
	if a.Failure[0].Description != "not found | 未找到" {
		t.Errorf("failure description = %q | 响应描述应保留完整文本", a.Failure[0].Description)
	}

	if _, ok := annots["GET /skipped"]; ok {
		t.Errorf("_test.go files must be skipped | 必须跳过 _test.go 文件")
	}
	if _, ok := annots["GET /legacy"]; !ok {
		t.Errorf("incomplete annotation must still be collected | 不完整注解也应被收集")
	}
}

func TestCollectFromSourceReport(t *testing.T) {
	report, err := CollectFromSource(memFSWithAnnotations(t), []string{"/api"})
	if err != nil {
		t.Fatalf("CollectFromSource: %v", err)
	}
	for _, key := range []string{"GET /users/{id}", "POST /users", "GET /legacy"} {
		if !hasString(report.Matched, key) {
			t.Errorf("matched missing %q | Matched 缺少 %q: %v", key, key, report.Matched)
		}
	}
	if !hasString(report.Incomplete, "GET /legacy") {
		t.Errorf("incomplete = %v | 缺少必填指令的注解应计入 Incomplete", report.Incomplete)
	}
	if hasString(report.Incomplete, "POST /users") {
		t.Errorf("complete annotation marked incomplete | 完整注解不应计入 Incomplete: %v", report.Incomplete)
	}
	if len(report.Missing) != 0 || len(report.Orphan) != 0 {
		t.Errorf("source-only report must leave Missing/Orphan empty | 仅源码解析时 Missing/Orphan 应为空: %+v", report)
	}

	if _, err := CollectFromSource(memFSWithAnnotations(t), []string{"/nope"}); !errors.Is(err, ErrScan) {
		t.Errorf("missing directory must return ErrScan, got %v | 目录不存在时应返回 ErrScan，实际为 %v", err, err)
	}
}

func TestFromRouterWithAnnotationsMergesAndReports(t *testing.T) {
	annots, err := ParseAnnotations(memFSWithAnnotations(t), []string{"/api"})
	if err != nil {
		t.Fatalf("ParseAnnotations: %v", err)
	}

	r := router.New()
	r.GET("/users/:id", noopHandler)
	r.POST("/users", noopHandler)
	r.GET("/health", noopHandler)

	report := &Report{}
	doc := FromRouterWithAnnotations(r, Info{Title: "Demo", Version: "1.0.0"}, Options{}, annots, report)

	item, ok := doc.Paths["/users/{id}"]
	if !ok {
		t.Fatalf("missing annotated path | 缺少注解路径: %v", doc.Paths)
	}
	op := item.Get
	if op == nil {
		t.Fatalf("missing GET operation | 缺少 GET 操作")
	}
	if op.Summary != "Get user detail | 获取用户详情" {
		t.Errorf("summary = %q | 注解应优先于自动推导", op.Summary)
	}
	if !hasString(op.Tags, "users") {
		t.Errorf("tags = %v | 注解 Tags 未合并", op.Tags)
	}
	if p := findParam(op.Parameters, "id"); p == nil || p.In != "path" || !p.Required {
		t.Errorf("path param = %+v | 路径参数应由模式自动补全", p)
	}
	if op.RequestBody == nil {
		t.Fatalf("body param must produce a requestBody | body 参数应生成 requestBody")
	}
	if _, ok := op.RequestBody.Content["application/json"]; !ok {
		t.Errorf("requestBody content = %v | @Accept 应转换为媒体类型", op.RequestBody.Content)
	}
	ok200, has200 := op.Responses["200"]
	if !has200 {
		t.Fatalf("responses = %v | 缺少 200 响应", op.Responses)
	}
	if ok200.Content["text/html"].Schema.Ref != "#/components/schemas/model.User" {
		t.Errorf("200 content = %+v | @Produce 应转换为响应内容", ok200.Content)
	}
	if notFound, has404 := op.Responses["404"]; !has404 || notFound.Content["text/html"].Schema.Type != "string" {
		t.Errorf("404 response = %+v | Failure 响应映射不正确", op.Responses["404"])
	}

	health := doc.Paths["/health"]
	if health.Get == nil || len(health.Get.Responses) == 0 {
		t.Errorf("unannotated route must keep a default operation | 无注解路由应保留默认操作: %+v", health.Get)
	}

	if !hasString(report.Matched, "GET /users/{id}") || !hasString(report.Matched, "POST /users") {
		t.Errorf("matched = %v | Matched 统计不正确", report.Matched)
	}
	if !hasString(report.Missing, "GET /health") {
		t.Errorf("missing = %v | 缺注解端点必须计入 Missing", report.Missing)
	}
	if !hasString(report.Orphan, "GET /legacy") {
		t.Errorf("orphan = %v | 无路由的注解必须计入 Orphan", report.Orphan)
	}
	if !hasString(report.Incomplete, "GET /legacy") {
		t.Errorf("incomplete = %v | 缺必填指令的注解必须计入 Incomplete", report.Incomplete)
	}
	if hasString(report.Missing, "GET /users/{id}") {
		t.Errorf("annotated route marked missing | 已有注解的路由不应计入 Missing: %v", report.Missing)
	}
}

func TestBuilderAddAnnotations(t *testing.T) {
	annots, err := ParseAnnotations(memFSWithAnnotations(t), []string{"/api"})
	if err != nil {
		t.Fatalf("ParseAnnotations: %v", err)
	}

	b := NewBuilder(Info{Title: "Demo", Version: "1.0.0"})
	b.AddRoute("GET", "/users/:id", Operation{})
	b.AddRoute("GET", "/health", Operation{})
	b.AddAnnotations(annots)
	doc := b.Build()

	op := doc.Paths["/users/{id}"].Get
	if op == nil || op.Summary != "Get user detail | 获取用户详情" {
		t.Fatalf("annotation not applied to builder route | 注解未合并进构建器路由: %+v", op)
	}
	if op.RequestBody == nil {
		t.Errorf("builder route must merge the request body | 构建器路由应合并请求体")
	}
	health := doc.Paths["/health"].Get
	if health == nil || health.Summary != "" {
		t.Errorf("unannotated builder route must be untouched | 无注解路由不应被改动: %+v", health)
	}
	if _, ok := doc.Paths["/legacy"]; ok {
		t.Errorf("orphan annotation must not create a path | 游离注解不应创建路径")
	}
}
