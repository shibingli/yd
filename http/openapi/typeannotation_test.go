package openapi

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/ydx/json"
	"github.com/shibingli/ydx/router"
)

// typeAnnotatedSource is the struct, const block and variable annotation fixture. It also
// carries declarations that must stay invisible: an unannotated struct, an unannotated const
// block, an unannotated variable, an unexported field, a json:"-" field and a field without
// any comment. | typeAnnotatedSource 是结构体、常量块与变量注解夹具。它同时包含必须保持不可见的
// 声明：无注解结构体、无注解常量块、无注解变量、未导出字段、json:"-" 字段与无任何注释的字段。
const typeAnnotatedSource = `package model

import "time"

// Status is the account status type. | Status 是账号状态类型。
type Status string

// Level is the account level type. | Level 是账号等级类型。
type Level int

// Plain is an unannotated struct. | Plain 是无注解结构体。
type Plain struct {
	Name string ` + "`" + `json:"name"` + "`" + `
}

// User is the account entity. | User 是账号实体。
//
// @Name User
// @Description 系统用户账号 | A system user account
// @Example {"id":"u_1","name":"admin","status":"active"}
type User struct {
	// ID is the primary key. | ID 是主键。
	// @Example u_1
	// @Format uuid
	ID string ` + "`" + `json:"id"` + "`" + `

	// @Enum active disabled
	// @Description 账号状态 | Account status
	Status Status ` + "`" + `json:"status"` + "`" + `

	// @Minimum 0
	// @Maximum 150
	Age int ` + "`" + `json:"age"` + "`" + `

	// @Pattern ^1[0-9]{10}$
	// @Nullable
	Phone string ` + "`" + `json:"phone,omitempty"` + "`" + ` // @Deprecated

	// Nickname is chosen by the operator. | Nickname 由运营设置。
	// @ReadOnly
	Nickname string ` + "`" + `json:"nickname"` + "`" + `

	// Joined is documented but not annotated. | Joined 有说明但无注解。
	Joined time.Time ` + "`" + `json:"joined"` + "`" + `

	Score float64 ` + "`" + `json:"score,omitempty"` + "`" + `

	// @Example hidden
	hidden string ` + "`" + `json:"hidden"` + "`" + `

	// @Example ignored
	Dropped string ` + "`" + `json:"-"` + "`" + `
}

// Status values. | 状态取值。
//
// @Name UserStatus
// @Description 账号状态 | Account status
// @Type string
// @Enum active:启用 | enabled, disabled:禁用 | disabled
const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

// Level values are inferred from iota. | Level 取值由 iota 推断。
//
// @Name UserLevel
const (
	LevelGuest Level = iota
	LevelMember
	LevelAdmin
)

// Code values are inferred from string literals. | Code 取值由字符串字面量推断。
//
// @Name UserCode
const (
	CodeOK       = "ok"
	CodeNotFound = "not_found"
)

// unannotatedConst must never be collected. | unannotatedConst 绝不应被收集。
const unannotatedConst = "plain"

// DefaultUser is the seed account. | DefaultUser 是种子账号。
//
// @Name DefaultUser
// @Schema User
// @Example {"id":"u_1","name":"admin"}
var DefaultUser = User{ID: "u_1"}

// unannotatedVar must never be collected. | unannotatedVar 绝不应被收集。
var unannotatedVar = 1
`

// typeAnnotatedTestSource lives in a _test.go file and must be skipped. | typeAnnotatedTestSource
// 位于 _test.go 文件中，必须被跳过。
const typeAnnotatedTestSource = `package model

// @Name TestOnlyType
type TestOnlyType struct {
	Name string ` + "`" + `json:"name"` + "`" + `
}
`

// typeAnnotatedSkipDirSource lives under testdata and must be skipped. | typeAnnotatedSkipDirSource
// 位于 testdata 目录下，必须被跳过。
const typeAnnotatedSkipDirSource = `package model

// @Name TestdataType
type TestdataType struct {
	Name string ` + "`" + `json:"name"` + "`" + `
}
`

// exampleAnnotatedSource binds @Example lines to @Param, @Success and @Failure. | exampleAnnotatedSource
// 把 @Example 行绑定到 @Param、@Success 与 @Failure。
const exampleAnnotatedSource = `package api

import "github.com/shibingli/ydx/router"

// GetUser returns one user. | GetUser 返回单个用户。
//
// @Summary Get user detail | 获取用户详情
// @Tags users
// @Accept json
// @Produce json
// @Param id path string true "user id | 用户 ID"
// @Example u_1
// @Success 200 {object} User "user detail | 用户详情"
// @Example {"id":"u_1","name":"admin","status":"active"}
// @Failure 404 {string} string "not found | 未找到"
// @Example missing
// @Router /users/{id} [get]
func GetUser(c *router.Ctx) {}

// CreateUser stores one user. | CreateUser 新建单个用户。
//
// @Summary Create user | 创建用户
// @Tags users
// @Accept json
// @Produce json
// @Param body body User true "create payload | 创建参数"
// @Example {"id":"u_9","name":"admin"}
// @Success 201 {object} User "created | 已创建"
// @Example {"id":"u_1","name":"admin"}
// @Router /users [post]
func CreateUser(c *router.Ctx) {}
`

// reflectedUser mirrors the User fixture struct, so annotations parsed from source can be
// merged into a schema derived by reflection. | reflectedUser 与夹具中的 User 结构体保持同名同
// 标签，从而能把源码注解合并进反射生成的 Schema。
type reflectedUser struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Age      int     `json:"age"`
	Phone    string  `json:"phone,omitempty"`
	Nickname string  `json:"nickname"`
	Score    float64 `json:"score,omitempty"`
}

// reflectedPlain is an unannotated type used to verify UnannotatedTypes. | reflectedPlain
// 是无注解类型，用于验证 UnannotatedTypes。
type reflectedPlain struct {
	Name string `json:"name"`
}

// memFSWithTypes writes the type annotation fixtures into a memory file system. | memFSWithTypes
// 把类型注解夹具写入内存文件系统。
func memFSWithTypes(t *testing.T) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	for path, src := range map[string]string{
		"/model/models.go":           typeAnnotatedSource,
		"/model/models_test.go":      typeAnnotatedTestSource,
		"/model/testdata/legacy.go":  typeAnnotatedSkipDirSource,
		"/model/vendor/third.go":     typeAnnotatedSkipDirSource,
		"/model/.hidden/private.go":  typeAnnotatedSkipDirSource,
		"/model/_build/generated.go": typeAnnotatedSkipDirSource,
	} {
		if err := afero.WriteFile(fs, path, []byte(src), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v | 写入夹具失败", path, err)
		}
	}
	return fs
}

// parseTypeFixtures parses the type annotation fixtures once. | parseTypeFixtures 解析一次类型注解夹具。
func parseTypeFixtures(t *testing.T) *TypeAnnotations {
	t.Helper()
	types, err := ParseTypeAnnotations(memFSWithTypes(t), []string{"/model"})
	if err != nil {
		t.Fatalf("ParseTypeAnnotations: %v | 解析类型注解失败", err)
	}
	return types
}

// jsonAt walks a decoded JSON tree along the given keys. | jsonAt 沿给定键遍历解码后的 JSON 树。
func jsonAt(t *testing.T, root any, keys ...string) any {
	t.Helper()
	current := root
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("key %q: parent is %T, want object | 键 %q 的父节点应为对象", key, current, key)
		}
		current, ok = object[key]
		if !ok {
			t.Fatalf("key %q missing | 缺少键 %q: %v", key, key, object)
		}
	}
	return current
}

// TestParseSchemaAnnotationFromStruct 验证结构体级注解与全部字段级注解。
// TestParseSchemaAnnotationFromStruct verifies the struct level annotation and every field level directive.
func TestParseSchemaAnnotationFromStruct(t *testing.T) {
	types := parseTypeFixtures(t)

	sa, ok := types.Schemas["User"]
	if !ok {
		t.Fatalf("missing schema annotation | 缺少结构体注解: %v", sortedKeys(types.Schemas))
	}
	if sa.Name != "User" {
		t.Errorf("name = %q, want User | @Name 应为 User", sa.Name)
	}
	if sa.Description != "系统用户账号 | A system user account" {
		t.Errorf("description = %q | @Description 应保留完整中英文本", sa.Description)
	}
	if sa.Example != `{"id":"u_1","name":"admin","status":"active"}` {
		t.Errorf("example = %q | 结构体示例应原样保留", sa.Example)
	}
	if !strings.Contains(sa.Pos, "models.go:") {
		t.Errorf("pos = %q | Pos 应包含 文件:行号", sa.Pos)
	}
	if _, ok := types.Schemas["Plain"]; ok {
		t.Errorf("unannotated struct must not be collected | 无注解结构体不应被收集")
	}

	id, ok := sa.Fields["id"]
	if !ok {
		t.Fatalf("missing id field | 缺少 id 字段: %v", sortedKeys(sa.Fields))
	}
	if id.Name != "id" {
		t.Errorf("id name = %q | 字段名应取 json 标签名", id.Name)
	}
	if id.Description != "ID is the primary key. | ID 是主键。" {
		t.Errorf("id description = %q | 字段说明应回退到 doc 正文", id.Description)
	}
	if id.Example != "u_1" || id.Format != "uuid" {
		t.Errorf("id example/format = %q/%q | @Example 与 @Format 解析不正确", id.Example, id.Format)
	}

	status := sa.Fields["status"]
	if status.Description != "账号状态 | Account status" {
		t.Errorf("status description = %q | @Description 应覆盖 doc 正文", status.Description)
	}
	if !reflect.DeepEqual(status.Enum, []string{"active", "disabled"}) {
		t.Errorf("status enum = %v | @Enum 应按空格切分", status.Enum)
	}

	age := sa.Fields["age"]
	if age.Minimum == nil || *age.Minimum != 0 || age.Maximum == nil || *age.Maximum != 150 {
		t.Errorf("age minimum/maximum = %v/%v | @Minimum 与 @Maximum 解析不正确", age.Minimum, age.Maximum)
	}

	phone := sa.Fields["phone"]
	if phone.Pattern != `^1[0-9]{10}$` {
		t.Errorf("phone pattern = %q | @Pattern 解析不正确", phone.Pattern)
	}
	if !phone.Nullable || !phone.Deprecated {
		t.Errorf("phone nullable/deprecated = %v/%v | @Nullable 与行尾 @Deprecated 解析不正确", phone.Nullable, phone.Deprecated)
	}

	nickname := sa.Fields["nickname"]
	if !nickname.ReadOnly || nickname.Description != "Nickname is chosen by the operator. | Nickname 由运营设置。" {
		t.Errorf("nickname = %+v | @ReadOnly 与 doc 正文解析不正确", nickname)
	}

	joined := sa.Fields["joined"]
	if joined.Description == "" || joined.Example != "" || joined.Enum != nil {
		t.Errorf("joined = %+v | 仅有 doc 正文的字段应只带 description", joined)
	}
	for _, skipped := range []string{"hidden", "-", "score"} {
		if _, ok := sa.Fields[skipped]; ok {
			t.Errorf("field %q must be skipped | 字段 %q 应被跳过", skipped, skipped)
		}
	}
}

// TestParseEnumFromConstBlock 验证显式 @Enum 与从字面量推断（含 iota）两种枚举解析。
// TestParseEnumFromConstBlock verifies explicit @Enum and literal inference, iota included.
func TestParseEnumFromConstBlock(t *testing.T) {
	types := parseTypeFixtures(t)

	if len(types.Enums) != 3 {
		t.Fatalf("enum count = %d, want 3 | 枚举报数量应为 3: %v", len(types.Enums), sortedKeys(types.Enums))
	}

	explicit, ok := types.Enums["UserStatus"]
	if !ok {
		t.Fatalf("missing UserStatus | 缺少 UserStatus: %v", sortedKeys(types.Enums))
	}
	if explicit.Name != "UserStatus" || explicit.Type != "string" {
		t.Errorf("UserStatus name/type = %q/%q | @Name 与 @Type 解析不正确", explicit.Name, explicit.Type)
	}
	if explicit.Description != "账号状态 | Account status" {
		t.Errorf("UserStatus description = %q | @Description 解析不正确", explicit.Description)
	}
	if !reflect.DeepEqual(explicit.Values, []EnumValue{
		{Value: "active", Description: "启用 | enabled"},
		{Value: "disabled", Description: "禁用 | disabled"},
	}) {
		t.Errorf("UserStatus values = %+v | @Enum 应按逗号切分并保留说明", explicit.Values)
	}
	if !strings.Contains(explicit.Pos, "models.go:") {
		t.Errorf("UserStatus pos = %q | Pos 应包含 文件:行号", explicit.Pos)
	}

	iotaEnum, ok := types.Enums["UserLevel"]
	if !ok {
		t.Fatalf("missing UserLevel | 缺少 UserLevel: %v", sortedKeys(types.Enums))
	}
	if iotaEnum.Type != "integer" {
		t.Errorf("UserLevel type = %q, want integer | iota 应推断为 integer", iotaEnum.Type)
	}
	if !reflect.DeepEqual(iotaEnum.Values, []EnumValue{
		{Value: "0"}, {Value: "1"}, {Value: "2"},
	}) {
		t.Errorf("UserLevel values = %+v | iota 应按序号推断", iotaEnum.Values)
	}

	literalEnum, ok := types.Enums["UserCode"]
	if !ok {
		t.Fatalf("missing UserCode | 缺少 UserCode: %v", sortedKeys(types.Enums))
	}
	if literalEnum.Type != "string" {
		t.Errorf("UserCode type = %q, want string | 字符串字面量应推断为 string", literalEnum.Type)
	}
	if !reflect.DeepEqual(literalEnum.Values, []EnumValue{
		{Value: "ok"}, {Value: "not_found"},
	}) {
		t.Errorf("UserCode values = %+v | 字符串字面量应被推断为取值", literalEnum.Values)
	}

	if _, ok := types.Enums["unannotatedConst"]; ok {
		t.Errorf("unannotated const block must not be collected | 无注解常量块不应被收集")
	}
}

// TestParseVarAnnotationProvidesExample 验证变量级注解提供示例值。
// TestParseVarAnnotationProvidesExample verifies that a variable level annotation provides an example.
func TestParseVarAnnotationProvidesExample(t *testing.T) {
	types := parseTypeFixtures(t)

	if len(types.Vars) != 1 {
		t.Fatalf("var count = %d, want 1 | 变量注解数量应为 1: %v", len(types.Vars), sortedKeys(types.Vars))
	}
	va, ok := types.Vars["DefaultUser"]
	if !ok {
		t.Fatalf("missing DefaultUser | 缺少 DefaultUser: %v", sortedKeys(types.Vars))
	}
	if va.Schema != "User" {
		t.Errorf("schema = %q, want User | @Schema 解析不正确", va.Schema)
	}
	if va.Example != `{"id":"u_1","name":"admin"}` {
		t.Errorf("example = %q | @Example 应原样保留", va.Example)
	}
	if va.Description != "DefaultUser is the seed account. | DefaultUser 是种子账号。" {
		t.Errorf("description = %q | 变量说明应回退到 doc 正文", va.Description)
	}
	if !strings.Contains(va.Pos, "models.go:") {
		t.Errorf("pos = %q | Pos 应包含 文件:行号", va.Pos)
	}
	if _, ok := types.Vars["unannotatedVar"]; ok {
		t.Errorf("unannotated var must not be collected | 无注解变量不应被收集")
	}
}

// TestTypeAnnotationsSkipRules 验证 _test.go 与被排除目录中的注解不会被解析。
// TestTypeAnnotationsSkipRules verifies that annotations in _test.go files and excluded directories are skipped.
func TestTypeAnnotationsSkipRules(t *testing.T) {
	types := parseTypeFixtures(t)
	for _, skipped := range []string{"TestOnlyType", "TestdataType"} {
		if _, ok := types.Schemas[skipped]; ok {
			t.Errorf("type %q must be skipped | 类型 %q 应被跳过", skipped, skipped)
		}
	}
}

// TestBuilderMergesTypeAnnotationsIntoReflectedSchema 验证先反射注册再合并类型注解的结果。
// TestBuilderMergesTypeAnnotationsIntoReflectedSchema verifies merging annotations into a reflected schema.
func TestBuilderMergesTypeAnnotationsIntoReflectedSchema(t *testing.T) {
	types := parseTypeFixtures(t)

	b := NewBuilder(Info{Title: "Demo", Version: "1.0.0"})
	if ref := b.RegisterSchema("User", &reflectedUser{}); ref != "#/components/schemas/User" {
		t.Fatalf("ref = %q | 引用格式不正确", ref)
	}
	b.AddTypeAnnotations(types)

	schema := b.Build().Components.Schemas["User"]
	if schema == nil {
		t.Fatalf("schema not registered | Schema 未注册")
	}
	if schema.Description != "系统用户账号 | A system user account" {
		t.Errorf("description = %q | 结构体说明应被注解覆盖", schema.Description)
	}
	example, ok := schema.Example.(map[string]any)
	if !ok || example["id"] != "u_1" || example["status"] != "active" {
		t.Errorf("example = %#v | 结构体示例应为解析后的 JSON 对象", schema.Example)
	}

	id := schema.Properties["id"]
	if id == nil || id.Type != "string" {
		t.Fatalf("id = %+v | 反射类型必须保留", id)
	}
	if id.Example != "u_1" || id.Format != "uuid" || id.Description != "ID is the primary key. | ID 是主键。" {
		t.Errorf("id = %+v | 字段注解未合并", id)
	}

	status := schema.Properties["status"]
	if !reflect.DeepEqual(status.Enum, []any{"active", "disabled"}) || status.Description != "账号状态 | Account status" {
		t.Errorf("status = %+v | 字段枚举未合并", status)
	}

	age := schema.Properties["age"]
	if age.Type != "integer" || age.Minimum == nil || *age.Minimum != 0 || age.Maximum == nil || *age.Maximum != 150 {
		t.Errorf("age = %+v | 反射类型与注解边界应共存", age)
	}

	phone := schema.Properties["phone"]
	if phone.Pattern != `^1[0-9]{10}$` || !phone.Nullable || !phone.Deprecated {
		t.Errorf("phone = %+v | pattern/nullable/deprecated 未合并", phone)
	}
	if nickname := schema.Properties["nickname"]; nickname == nil || !nickname.ReadOnly {
		t.Errorf("nickname = %+v | readOnly 未合并", nickname)
	}

	raw, err := b.Build().JSON()
	if err != nil {
		t.Fatalf("JSON: %v | 序列化失败", err)
	}
	for _, want := range []string{`"u_1"`, `"uuid"`, `"active"`, `"disabled"`, `"minimum": 0`, `"maximum": 150`, `"readOnly": true`, `"deprecated": true`, `"nullable": true`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON must contain %s | JSON 文本缺少 %s:\n%s", want, want, raw)
		}
	}
}

// TestAnnotationExamplesAppearInDocument 验证三处示例都会被序列化进文档。
// TestAnnotationExamplesAppearInDocument verifies that all three example carriers reach the serialized document.
func TestAnnotationExamplesAppearInDocument(t *testing.T) {
	fs := memFSWithTypes(t)
	if err := afero.WriteFile(fs, "/api/handlers.go", []byte(exampleAnnotatedSource), 0o644); err != nil {
		t.Fatalf("write fixture: %v | 写入夹具失败", err)
	}
	annots, err := ParseAnnotations(fs, []string{"/api"})
	if err != nil {
		t.Fatalf("ParseAnnotations: %v | 解析处理器注解失败", err)
	}
	if len(annots) != 2 {
		t.Fatalf("annotation count = %d, want 2 | 注解数量应为 2: %v", len(annots), sortedKeys(annots))
	}
	a, ok := annots["GET /users/{id}"]
	if !ok {
		t.Fatalf("missing GET /users/{id} | 缺少 GET /users/{id}: %v", sortedKeys(annots))
	}
	if len(a.Params) != 1 || a.Params[0].Example != "u_1" {
		t.Fatalf("param examples = %+v | @Example 应绑定到最近一条 @Param", a.Params)
	}
	if len(a.Success) != 1 || a.Success[0].Example != `{"id":"u_1","name":"admin","status":"active"}` {
		t.Fatalf("success examples = %+v | @Example 应绑定到最近一条 @Success", a.Success)
	}
	if len(a.Failure) != 1 || a.Failure[0].Example != "missing" {
		t.Fatalf("failure examples = %+v | @Example 应绑定到最近一条 @Failure", a.Failure)
	}
	if a.Summary != "Get user detail | 获取用户详情" || a.Produce[0] != "json" {
		t.Errorf("existing directives must be untouched | 既有指令不应被改动: %+v", a)
	}
	create := annots["POST /users"]
	if len(create.Params) != 1 || create.Params[0].In != "body" || create.Params[0].Example != `{"id":"u_9","name":"admin"}` {
		t.Fatalf("body param = %+v | body @Example 应绑定到 @Param body", create.Params)
	}

	b := NewBuilder(Info{Title: "Demo", Version: "1.0.0"})
	b.AddRoute("GET", "/users/:id", Operation{})
	b.AddRoute("POST", "/users", Operation{})
	b.AddAnnotations(annots)
	b.AddTypeAnnotations(parseTypeFixtures(t))
	doc := b.Build()

	raw, err := doc.JSON()
	if err != nil {
		t.Fatalf("JSON: %v | 序列化失败", err)
	}
	text := string(raw)
	for _, want := range []string{`"u_1"`, `"u_9"`, `"missing"`, `"active"`, `"admin"`} {
		if !strings.Contains(text, want) {
			t.Errorf("JSON text must contain %s | JSON 文本缺少 %s:\n%s", want, want, text)
		}
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v | 反序列化失败", err)
	}
	params, _ := jsonAt(t, decoded, "paths", "/users/{id}", "get", "parameters").([]any)
	if len(params) != 1 {
		t.Fatalf("parameter count = %d, want 1 | 路径参数应为 1: %v", len(params), params)
	}
	idParam, _ := params[0].(map[string]any)
	if idParam["name"] != "id" || idParam["example"] != "u_1" {
		t.Errorf("id parameter = %v | Parameter.Example 未填充", idParam)
	}
	success, _ := jsonAt(t, decoded, "paths", "/users/{id}", "get", "responses", "200", "content", "application/json", "example").(map[string]any)
	if success["id"] != "u_1" || success["status"] != "active" {
		t.Errorf("success example = %v | 响应 MediaType.Example 未填充", success)
	}
	if failure := jsonAt(t, decoded, "paths", "/users/{id}", "get", "responses", "404", "content", "application/json", "example"); failure != "missing" {
		t.Errorf("failure example = %v | 失败响应示例未填充", failure)
	}
	bodyExample, _ := jsonAt(t, decoded, "paths", "/users", "post", "requestBody", "content", "application/json", "example").(map[string]any)
	if bodyExample["id"] != "u_9" {
		t.Errorf("body example = %v | MediaType.Example 未填充", bodyExample)
	}
	schemaExample, _ := jsonAt(t, decoded, "components", "schemas", "User", "example").(map[string]any)
	if schemaExample["name"] != "admin" {
		t.Errorf("schema example = %v | Schema.Example 未填充", schemaExample)
	}
	statusEnum, _ := jsonAt(t, decoded, "components", "schemas", "User", "properties", "status", "enum").([]any)
	if !reflect.DeepEqual(statusEnum, []any{"active", "disabled"}) {
		t.Errorf("status enum = %v | 字段枚举未写入文档", statusEnum)
	}
}

// TestUnannotatedTypesAreReported 验证反射注册但无注解的类型会被报告。
// TestUnannotatedTypesAreReported verifies that reflected types without annotations are reported.
func TestUnannotatedTypesAreReported(t *testing.T) {
	b := NewBuilder(Info{Title: "Demo", Version: "1.0.0"})
	b.RegisterSchema("User", &reflectedUser{})
	b.RegisterSchema("Plain", &reflectedPlain{})
	b.AddTypeAnnotations(parseTypeFixtures(t))

	coverage := b.TypeCoverage()
	wantAnnotated := []string{"User", "UserCode", "UserLevel", "UserStatus"}
	if !reflect.DeepEqual(coverage.Annotated, wantAnnotated) {
		t.Errorf("annotated = %v, want %v | 已注解类型统计不正确", coverage.Annotated, wantAnnotated)
	}
	if !reflect.DeepEqual(coverage.Unannotated, []string{"Plain"}) {
		t.Errorf("unannotated = %v, want [Plain] | 无注解类型统计不正确", coverage.Unannotated)
	}
	if coverage.Enums != 3 {
		t.Errorf("enums = %d, want 3 | 枚举报数量应为 3", coverage.Enums)
	}

	report := &Report{
		AnnotatedTypes:   coverage.Annotated,
		UnannotatedTypes: coverage.Unannotated,
		EnumCount:        coverage.Enums,
	}
	if !hasString(report.AnnotatedTypes, "User") || !hasString(report.UnannotatedTypes, "Plain") {
		t.Errorf("report = %+v | Report 类型级字段未填充", report)
	}
}

// TestFromRouterWithAnnotationsMergesTypeAnnotations 验证类型注解的自动合并与报告填充。
// TestFromRouterWithAnnotationsMergesTypeAnnotations verifies automatic type annotation merging and reporting.
func TestFromRouterWithAnnotationsMergesTypeAnnotations(t *testing.T) {
	r := router.New()
	r.GET("/users/:id", noopHandler)

	t.Run("explicit annotations | 显式提供注解", func(t *testing.T) {
		report := &Report{}
		doc := FromRouterWithAnnotations(r, Info{Title: "Demo", Version: "1.0.0"},
			Options{TypeAnnotations: parseTypeFixtures(t)}, nil, report)

		schema := doc.Components.Schemas["User"]
		if schema == nil || schema.Description != "系统用户账号 | A system user account" {
			t.Fatalf("schema = %+v | 类型注解未合并进文档", schema)
		}
		if !reflect.DeepEqual(schema.Properties["status"].Enum, []any{"active", "disabled"}) {
			t.Errorf("status enum = %v | 枚举未合并", schema.Properties["status"].Enum)
		}
		if !hasString(report.AnnotatedTypes, "User") || !hasString(report.AnnotatedTypes, "UserStatus") {
			t.Errorf("annotated types = %v | Report.AnnotatedTypes 未填充", report.AnnotatedTypes)
		}
		if len(report.UnannotatedTypes) != 0 {
			t.Errorf("unannotated types = %v | 未注册类型不应出现在 UnannotatedTypes", report.UnannotatedTypes)
		}
		if report.EnumCount != 3 {
			t.Errorf("enum count = %d, want 3 | Report.EnumCount 未填充", report.EnumCount)
		}
		if doc.Components.Schemas["DefaultUser"] != nil {
			t.Errorf("variable annotation must not create a schema | 变量注解不应创建 Schema")
		}
	})

	t.Run("source dirs | 从源码目录解析", func(t *testing.T) {
		dir := t.TempDir()
		fs := afero.NewOsFs()
		path := filepath.Join(dir, "models.go")
		if err := afero.WriteFile(fs, path, []byte(typeAnnotatedSource), 0o644); err != nil {
			t.Fatalf("write fixture: %v | 写入夹具失败", err)
		}

		report := &Report{}
		doc := FromRouterWithAnnotations(r, Info{Title: "Demo", Version: "1.0.0"},
			Options{SourceDirs: []string{dir}}, nil, report)

		schema := doc.Components.Schemas["User"]
		if schema == nil || schema.Example == nil {
			t.Fatalf("schema = %+v | SourceDirs 中的类型注解未合并", schema)
		}
		if example, ok := schema.Example.(map[string]any); !ok || example["id"] != "u_1" {
			t.Errorf("example = %#v | 结构体示例不正确", schema.Example)
		}
		if !reflect.DeepEqual(schema.Properties["status"].Enum, []any{"active", "disabled"}) {
			t.Errorf("status enum = %v | 字段枚举不正确", schema.Properties["status"].Enum)
		}
		if report.EnumCount != 3 || !hasString(report.AnnotatedTypes, "UserLevel") {
			t.Errorf("report = %+v | SourceDirs 解析结果未写入 Report", report)
		}
		if !hasString(report.Missing, "GET /users/{id}") {
			t.Errorf("missing = %v | 无注解路由仍应计入 Missing", report.Missing)
		}
	})
}

// TestBackwardCompatibilityWithExistingDialect 验证既有处理器方言与 CollectFromSource 语义不变。
// TestBackwardCompatibilityWithExistingDialect verifies the handler dialect and CollectFromSource keep their semantics.
func TestBackwardCompatibilityWithExistingDialect(t *testing.T) {
	annots, err := ParseAnnotations(memFSWithAnnotations(t), []string{"/api"})
	if err != nil {
		t.Fatalf("ParseAnnotations: %v | 解析处理器注解失败", err)
	}
	if len(annots) != 3 {
		t.Fatalf("annotation count = %d, want 3 | 注解数量应为 3: %v", len(annots), sortedKeys(annots))
	}
	a := annots["GET /users/{id}"]
	if a.Summary != "Get user detail | 获取用户详情" || len(a.Success) != 2 || len(a.Failure) != 1 {
		t.Errorf("existing dialect changed | 既有方言解析结果被改动: %+v", a)
	}
	for _, p := range a.Params {
		if p.Example != "" {
			t.Errorf("param %q example = %q | 无 @Example 时示例应为空", p.Name, p.Example)
		}
	}
	for _, r := range append(append([]AnnotationResponse{}, a.Success...), a.Failure...) {
		if r.Example != "" {
			t.Errorf("response %d example = %q | 无 @Example 时示例应为空", r.Code, r.Example)
		}
	}

	report, err := CollectFromSource(memFSWithAnnotations(t), []string{"/api"})
	if err != nil {
		t.Fatalf("CollectFromSource: %v | 收集覆盖率失败", err)
	}
	for _, key := range []string{"GET /users/{id}", "POST /users", "GET /legacy"} {
		if !hasString(report.Matched, key) {
			t.Errorf("matched missing %q | Matched 缺少 %q: %v", key, key, report.Matched)
		}
	}
	if !hasString(report.Incomplete, "GET /legacy") || hasString(report.Incomplete, "POST /users") {
		t.Errorf("incomplete = %v | Incomplete 统计被改动", report.Incomplete)
	}
	if len(report.Missing) != 0 || len(report.Orphan) != 0 {
		t.Errorf("report = %+v | 仅源码解析时 Missing/Orphan 应为空", report)
	}
	if len(report.AnnotatedTypes) != 0 || report.EnumCount != 0 {
		t.Errorf("type fields = %v/%d | 无类型注解的目录不应报告类型", report.AnnotatedTypes, report.EnumCount)
	}

	// 类型注解块不得被误判为处理器注解，同时类型级报告照常填充。
	// Type annotation blocks must never be read as handler annotations, while type coverage keeps working.
	mixed := memFSWithAnnotations(t)
	if err := afero.WriteFile(mixed, "/api/model/models.go", []byte(typeAnnotatedSource), 0o644); err != nil {
		t.Fatalf("write fixture: %v | 写入夹具失败", err)
	}
	mixedAnnots, err := ParseAnnotations(mixed, []string{"/api"})
	if err != nil {
		t.Fatalf("ParseAnnotations: %v | 解析处理器注解失败", err)
	}
	if len(mixedAnnots) != len(annots) {
		t.Errorf("annotation count = %d, want %d | 类型注解块不应产生处理器注解: %v",
			len(mixedAnnots), len(annots), sortedKeys(mixedAnnots))
	}
	for key := range mixedAnnots {
		if _, ok := annots[key]; !ok {
			t.Errorf("unexpected key %q | 出现意外的注解键", key)
		}
	}

	mixedReport, err := CollectFromSource(mixed, []string{"/api"})
	if err != nil {
		t.Fatalf("CollectFromSource: %v | 收集覆盖率失败", err)
	}
	if !reflect.DeepEqual(mixedReport.Matched, report.Matched) || !reflect.DeepEqual(mixedReport.Incomplete, report.Incomplete) {
		t.Errorf("matched/incomplete changed | 处理器覆盖率被改动: %v/%v", mixedReport.Matched, mixedReport.Incomplete)
	}
	if !hasString(mixedReport.AnnotatedTypes, "UserStatus") || mixedReport.EnumCount != 3 {
		t.Errorf("type coverage = %v/%d | 类型级覆盖率未填充", mixedReport.AnnotatedTypes, mixedReport.EnumCount)
	}
}
