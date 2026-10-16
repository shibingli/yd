package openapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shibingli/yd/errx"
	"github.com/spf13/afero"
)

// Source scanning errors, declared once at package level. | 源码扫描错误，在包级声明一次。
var (
	// ErrScan means the annotation directories could not be traversed. | ErrScan 表示注解目录无法遍历。
	ErrScan = errx.New("openapi: failed to scan annotation sources | openapi：注解源码扫描失败")
	// ErrRead means an annotation source file could not be read. | ErrRead 表示注解源文件无法读取。
	ErrRead = errx.New("openapi: failed to read annotation source | openapi：注解源文件读取失败")
)

// Annotation is the parsed form of one handler annotation block. | Annotation 是单个处理器注解块的解析结果。
//
// The understood dialect is a swaggo style comment block placed directly above a
// handler: | 可识别的方言是写在处理器上方的 swaggo 风格注释块：
//
//	// @Summary Get user detail | 获取用户详情
//	// @Description Returns the user detail fragment. | 返回用户详情片段。
//	// @Tags users
//	// @Accept json
//	// @Produce html,json
//	// @Param id path string true "user id | 用户 ID"
//	// @Param body body CreateUserRequest true "create payload | 创建参数"
//	// @Success 200 {object} model.User "user detail | 用户详情"
//	// @Failure 404 {string} string "not found | 未找到"
//	// @Security BearerAuth
//	// @Router /users/{id} [get]
//
// Directive arguments are kept verbatim, including the bilingual "|" separator, so
// both "English | 中文" and "中文 | English" orderings survive untouched. | 指令参数原样保留，
// 含中英分隔符 "|"，因此 "English | 中文" 与 "中文 | English" 两种顺序都不会被截断。
type Annotation struct {
	// Summary is the single line summary taken from the first @Summary. | Summary 取自首个 @Summary 的一行摘要。
	Summary string
	// Description joins multiple @Description lines with a newline. | Description 以换行连接多行 @Description。
	Description string
	// Tags lists the @Tags values. | Tags 列出 @Tags 取值。
	Tags []string
	// Accept lists the @Accept media tokens, for example json. | Accept 列出 @Accept 媒体标记，例如 json。
	Accept []string
	// Produce lists the @Produce media tokens. | Produce 列出 @Produce 媒体标记。
	Produce []string
	// Params lists the @Param declarations in source order. | Params 按源码顺序列出 @Param 声明。
	Params []AnnotationParam
	// Success lists the @Success responses. | Success 列出 @Success 响应。
	Success []AnnotationResponse
	// Failure lists the @Failure responses. | Failure 列出 @Failure 响应。
	Failure []AnnotationResponse
	// Security lists the @Security scheme names. | Security 列出 @Security 方案名。
	Security []string
	// RouterPath is the template path of @Router, for example /users/{id}. | RouterPath 为 @Router 的模板路径，例如 /users/{id}。
	RouterPath string
	// RouterMethod is the upper cased HTTP method of @Router. | RouterMethod 为 @Router 的大写 HTTP 方法。
	RouterMethod string
	// Pos is the "file:line" of the block, used for diagnostics. | Pos 为注解块的 "文件:行号"，用于诊断。
	Pos string
}

// AnnotationParam is one @Param declaration. | AnnotationParam 是一条 @Param 声明。
type AnnotationParam struct {
	// Name is the parameter name. | Name 为参数名。
	Name string
	// In is path, query, header, cookie or body. | In 取 path、query、header、cookie 或 body。
	In string
	// Type is the declared type token, for example string or model.User. | Type 为声明的类型标记，例如 string 或 model.User。
	Type string
	// Required reports the fourth @Param token. | Required 对应 @Param 的第四个标记。
	Required bool
	// Description is the quoted trailing text, kept verbatim. | Description 为引号包裹的尾部文本，原样保留。
	Description string
	// Example is the example bound by a following @Example line, kept verbatim. | Example 为紧随
	// 其后的 @Example 行绑定的示例，原样保留。
	Example string
	// Schema is the derived schema; body parameters produce a component reference. | Schema 为推导出的
	// Schema；body 参数会生成组件引用。
	Schema *Schema
}

// AnnotationResponse is one @Success or @Failure declaration. | AnnotationResponse 是一条 @Success 或 @Failure 声明。
type AnnotationResponse struct {
	// Code is the status code, 0 for the default response. | Code 为状态码，0 表示默认响应。
	Code int
	// Type is the declared type token. | Type 为声明的类型标记。
	Type string
	// Ref is the component reference derived from "{object} X" style annotations, empty
	// for primitive types. | Ref 为 "{object} X" 风格注解推导出的组件引用，基础类型时为空。
	Ref string
	// Description is the quoted trailing text, kept verbatim. | Description 为引号包裹的尾部文本，原样保留。
	Description string
	// Example is the example bound by a following @Example line, kept verbatim. | Example 为紧随
	// 其后的 @Example 行绑定的示例，原样保留。
	Example string

	// schema is the fully resolved schema of the response, including array shapes that
	// Type and Ref cannot express. | schema 是响应完全解析后的 Schema，涵盖 Type 与 Ref
	// 无法表达的数组成员类型。
	schema *Schema
}

// Report summarizes annotation coverage. | Report 汇总注解覆盖情况。
//
// Keys are "<METHOD> <template path>", for example "GET /users/{id}". | 键为 "<METHOD> <模板路径>"，
// 例如 "GET /users/{id}"。
//
// CollectFromSource fills Matched (every key found in source) and Incomplete. Missing
// and Orphan require router knowledge and are filled by FromRouterWithAnnotations,
// which is the authoritative, router aware view. | CollectFromSource 填写 Matched（源码中发现的
// 全部键）与 Incomplete；Missing 与 Orphan 需要路由信息，由 FromRouterWithAnnotations 填写，
// 后者是权威的、结合路由的视图。
type Report struct {
	// Matched lists keys that have both a route and an annotation. | Matched 列出既有路由又有注解的键。
	Matched []string
	// Missing lists route keys without annotation. | Missing 列出有路由但缺注解的键。
	Missing []string
	// Orphan lists annotation keys without a matching route. | Orphan 列出有注解但无对应路由的键。
	Orphan []string
	// Incomplete lists keys whose annotation lacks @Summary, @Tags or @Success; blocks
	// without @Router cannot be keyed and are listed by Pos instead. | Incomplete 列出缺少
	// @Summary、@Tags 或 @Success 的注解键；没有 @Router 的注解块无法建键，改以 Pos 列出。
	Incomplete []string
	// AnnotatedTypes lists the names of the annotated types: struct annotations and const
	// block enums, sorted. | AnnotatedTypes 列出被注解的类型名：结构体注解与常量块枚举，已排序。
	AnnotatedTypes []string
	// UnannotatedTypes lists the names registered by reflection through RegisterSchema that
	// carry no annotation, sorted. | UnannotatedTypes 列出经 RegisterSchema 反射注册但没有任何
	// 注解的类型名，已排序。
	UnannotatedTypes []string
	// EnumCount is the number of enum annotations: found in source by CollectFromSource, or
	// merged into the document by FromRouterWithAnnotations. | EnumCount 是枚举报注解数量：
	// CollectFromSource 统计源码中发现的数量，FromRouterWithAnnotations 统计合并进文档的数量。
	EnumCount int
}

// ParseAnnotations walks dirs inside fsys, parses annotation blocks from non-test Go
// files and returns them keyed by "<METHOD> <template path>". | ParseAnnotations 遍历 fsys
// 中 dirs 目录下的非测试 Go 文件，解析注解块并以 "<METHOD> <模板路径>" 为键返回。
//
// Skipped: files ending in _test.go, and directories named vendor, node_modules or
// testdata, or whose name starts with "." or "_". | 跳过：以 _test.go 结尾的文件；名为
// vendor、node_modules、testdata 或以 "."、"_" 开头的目录。
//
// Blocks without @Router cannot be keyed and are omitted; when two blocks declare the
// same key the first one wins. | 没有 @Router 的注解块无法建键，会被忽略；两个块声明同一
// 键时以先出现者为准。
func ParseAnnotations(fsys afero.Fs, dirs []string) (map[string]Annotation, error) {
	blocks, err := parseAnnotationFiles(fsys, dirs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Annotation, len(blocks))
	for _, a := range blocks {
		key := a.key()
		if key == "" {
			continue
		}
		if _, dup := out[key]; dup {
			continue
		}
		out[key] = a
	}
	return out, nil
}

// The same directories are scanned for type annotations, so AnnotatedTypes and EnumCount
// describe the structs, const blocks and variables of the source as well; UnannotatedTypes
// requires reflection knowledge and is filled by Builder.TypeCoverage instead. | 同一批目录也会
// 被扫描以收集类型注解，因此 AnnotatedTypes 与 EnumCount 同时描述源码中的结构体、常量块与变量；
// UnannotatedTypes 需要反射信息，改由 Builder.TypeCoverage 填写。
func CollectFromSource(fsys afero.Fs, dirs []string) (*Report, error) {
	blocks, err := parseAnnotationFiles(fsys, dirs)
	if err != nil {
		return nil, err
	}
	report := &Report{}
	seen := make(map[string]bool, len(blocks))
	for _, a := range blocks {
		key := a.key()
		if key == "" {
			report.Incomplete = append(report.Incomplete, a.Pos)
			continue
		}
		if !seen[key] {
			seen[key] = true
			report.Matched = append(report.Matched, key)
		}
		if !a.complete() {
			report.Incomplete = append(report.Incomplete, key)
		}
	}
	sort.Strings(report.Matched)
	sort.Strings(report.Incomplete)

	types, err := ParseTypeAnnotations(fsys, dirs)
	if err != nil {
		return nil, err
	}
	report.AnnotatedTypes = types.TypeNames()
	report.EnumCount = len(types.Enums)
	return report, nil
}

// key returns the "<METHOD> <template path>" lookup key, or "" without @Router. | key
// 返回 "<METHOD> <模板路径>" 查找键，缺 @Router 时返回 ""。
func (a Annotation) key() string {
	if a.RouterPath == "" || a.RouterMethod == "" {
		return ""
	}
	return a.RouterMethod + " " + a.RouterPath
}

// complete reports whether every required directive is present. | complete 报告必填指令是否齐全。
func (a Annotation) complete() bool {
	return a.Summary != "" && len(a.Tags) > 0 && len(a.Success) > 0 &&
		a.RouterPath != "" && a.RouterMethod != ""
}

// bodyParam returns the first body parameter, if any. | bodyParam 返回首个 body 参数（若有）。
func (a Annotation) bodyParam() (AnnotationParam, bool) {
	for _, p := range a.Params {
		if strings.EqualFold(p.In, "body") {
			return p, true
		}
	}
	return AnnotationParam{}, false
}

// operation converts the annotation into an OpenAPI operation; params are the path
// parameters derived from the route pattern. | operation 把注解转换为 OpenAPI 操作；params
// 是从路由模式推导出的路径参数。
func (a Annotation) operation(path string, params []Parameter) Operation {
	op := Operation{
		Summary:     a.Summary,
		Description: a.Description,
		Tags:        a.Tags,
	}
	declared := make(map[string]bool, len(a.Params))
	for _, p := range a.Params {
		if strings.EqualFold(p.In, "body") {
			continue
		}
		location := strings.ToLower(strings.TrimSpace(p.In))
		if location == "" {
			location = "query"
		}
		op.Parameters = append(op.Parameters, Parameter{
			Name:        p.Name,
			In:          location,
			Required:    p.Required || location == "path",
			Description: p.Description,
			Example:     exampleValue(p.Example),
			Schema:      p.Schema,
		})
		declared[p.Name] = true
	}
	if body, ok := a.bodyParam(); ok {
		op.RequestBody = &RequestBody{
			Required: body.Required,
			Content:  contentFor(defaultMedia(a.Accept), body.Schema, exampleValue(body.Example)),
		}
	}
	op = mergePathParams(op, params)
	op.Responses = a.responses()
	return op
}

// responses converts the annotated responses into the OpenAPI response map. | responses
// 把注解响应转换为 OpenAPI 响应映射。
func (a Annotation) responses() map[string]Response {
	if len(a.Success) == 0 && len(a.Failure) == 0 {
		return map[string]Response{"200": {Description: http.StatusText(http.StatusOK)}}
	}
	out := make(map[string]Response, len(a.Success)+len(a.Failure))
	all := make([]AnnotationResponse, 0, len(a.Success)+len(a.Failure))
	all = append(all, a.Success...)
	all = append(all, a.Failure...)
	for _, r := range all {
		code := strconv.Itoa(r.Code)
		if r.Code == 0 {
			code = "default"
		}
		out[code] = Response{
			Description: responseDescription(r),
			Content:     contentFor(a.Produce, r.schema, exampleValue(r.Example)),
		}
	}
	return out
}

// responseDescription falls back to the standard status text. | responseDescription 回退到标准状态文本。
func responseDescription(r AnnotationResponse) string {
	if r.Description != "" {
		return r.Description
	}
	if r.Code != 0 {
		if text := http.StatusText(r.Code); text != "" {
			return text
		}
	}
	return "response"
}

// contentFor expands media tokens into an OpenAPI content map; no tokens means no
// content section, and the example is attached to every media type. | contentFor 把媒体标记
// 展开为 OpenAPI content 映射；无标记时不生成 content，示例会附加到每个媒体类型上。
func contentFor(media []string, schema *Schema, example any) map[string]MediaType {
	if len(media) == 0 {
		return nil
	}
	out := make(map[string]MediaType, len(media))
	for _, token := range media {
		name := mediaTypeName(token)
		if name == "" {
			continue
		}
		out[name] = MediaType{Schema: schema, Example: example}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// defaultMedia falls back to JSON when no @Accept token is present. | defaultMedia 在缺 @Accept 时回退到 JSON。
func defaultMedia(media []string) []string {
	if len(media) == 0 {
		return []string{"json"}
	}
	return media
}

// mediaTypeName maps a short media token to a full media type. | mediaTypeName 把简写媒体标记映射为完整媒体类型。
func mediaTypeName(token string) string {
	name := strings.ToLower(strings.TrimSpace(token))
	switch name {
	case "":
		return ""
	case "json":
		return "application/json"
	case "xml":
		return "application/xml"
	case "html":
		return "text/html"
	case "text", "plain":
		return "text/plain"
	case "form":
		return "application/x-www-form-urlencoded"
	case "multipart":
		return "multipart/form-data"
	case "octet-stream", "binary":
		return "application/octet-stream"
	}
	return name
}

// refForType builds the component reference of a model type token; a leading "*" is
// stripped, any package qualifier is kept as part of the component name. | refForType
// 构造模型类型标记的组件引用；会去掉前导 "*"，包限定名保留为组件名的一部分。
func refForType(token string) string {
	name := strings.TrimSpace(token)
	name = strings.TrimPrefix(name, "*")
	if name == "" {
		return ""
	}
	return refPrefix + name
}

// schemaForTypeName maps an annotation type token to a schema. | schemaForTypeName 把注解类型标记映射为 Schema。
func schemaForTypeName(token string) *Schema {
	name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(token), "*"))
	switch name {
	case "", "string":
		return &Schema{Type: "string"}
	case "int", "int32":
		return &Schema{Type: "integer", Format: "int32"}
	case "int64", "uint", "uint32", "uint64":
		return &Schema{Type: "integer", Format: "int64"}
	case "bool", "boolean":
		return &Schema{Type: "boolean"}
	case "float", "float32":
		return &Schema{Type: "number", Format: "float"}
	case "double", "float64":
		return &Schema{Type: "number", Format: "double"}
	case "array":
		return &Schema{Type: "array"}
	case "object":
		return &Schema{Type: "object"}
	}
	return &Schema{Ref: refForType(token)}
}

// schemaForParam derives the schema of a parameter; a body parameter always refers to
// a component. | schemaForParam 推导参数的 Schema；body 参数始终引用组件。
func schemaForParam(in, token string) *Schema {
	if strings.EqualFold(strings.TrimSpace(in), "body") {
		return &Schema{Ref: refForType(token)}
	}
	return schemaForTypeName(token)
}

// schemaForResponse derives the schema of an annotated response. | schemaForResponse 推导注解响应的 Schema。
func schemaForResponse(kind, token string) *Schema {
	switch kind {
	case "object":
		if strings.TrimSpace(token) == "" {
			return &Schema{Type: "object"}
		}
		return &Schema{Ref: refForType(token)}
	case "array":
		return &Schema{Type: "array", Items: schemaForTypeName(token)}
	default:
		return schemaForTypeName(token)
	}
}

// incompleteKeys lists the annotation keys missing a required directive. | incompleteKeys
// 列出缺少必填指令的注解键。
func incompleteKeys(annots map[string]Annotation) []string {
	out := make([]string, 0, len(annots))
	for key, a := range annots {
		if !a.complete() {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// mergeUniqueSorted appends the missing items to dst and returns a sorted copy. | mergeUniqueSorted
// 把缺失项追加到 dst 并返回排序后的副本。
func mergeUniqueSorted(dst, items []string) []string {
	seen := make(map[string]bool, len(dst)+len(items))
	out := make([]string, 0, len(dst)+len(items))
	for _, list := range [][]string{dst, items} {
		for _, s := range list {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// annotationFiles lists every candidate Go file below dirs in a deterministic order. | annotationFiles
// 按确定性顺序列出 dirs 下的全部候选 Go 文件。
//
// Both the handler dialect and the type dialect scan the same file set, so the two parsers
// always agree on what is in scope. | 处理器方言与类型方言扫描同一批文件，因此两个解析器的可见
// 范围始终一致。
func annotationFiles(fsys afero.Fs, dirs []string) ([]string, error) {
	var files []string
	for _, dir := range dirs {
		err := afero.Walk(fsys, dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if skipAnnotationDir(info.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			name := info.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			return nil, errx.WrapDetail(err, ErrScan, "walk %q", dir)
		}
	}
	sort.Strings(files)
	return files, nil
}

// parseAnnotationFiles parses the handler annotation blocks of every candidate Go file
// below dirs. | parseAnnotationFiles 解析 dirs 下全部候选 Go 文件的处理器注解块。
func parseAnnotationFiles(fsys afero.Fs, dirs []string) ([]Annotation, error) {
	files, err := annotationFiles(fsys, dirs)
	if err != nil {
		return nil, err
	}
	var out []Annotation
	for _, path := range files {
		src, err := afero.ReadFile(fsys, path)
		if err != nil {
			return nil, errx.WrapDetail(err, ErrRead, "%q", path)
		}
		out = append(out, parseFileAnnotations(path, string(src))...)
	}
	return out, nil
}

// skipAnnotationDir reports whether a directory is excluded from annotation scanning. | skipAnnotationDir
// 报告目录是否被排除在注解扫描之外。
func skipAnnotationDir(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	switch name {
	case "vendor", "node_modules", "testdata":
		return true
	}
	return false
}

// parseFileAnnotations extracts the annotation blocks of one source file. | parseFileAnnotations
// 提取单个源文件中的注解块。
func parseFileAnnotations(path, src string) []Annotation {
	lines := strings.Split(src, "\n")
	var out []Annotation
	for i := 0; i < len(lines); {
		if !isCommentLine(lines[i]) {
			i++
			continue
		}
		start := i
		for i < len(lines) && isCommentLine(lines[i]) {
			i++
		}
		if a, ok := parseAnnotationBlock(lines[start:i], path, start+1); ok {
			out = append(out, a)
		}
	}
	return out
}

// isCommentLine reports whether the line is a "//" comment line. | isCommentLine 报告该行是否为 "//" 注释行。
func isCommentLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(strings.TrimRight(line, "\r")), "//")
}

// parseAnnotationBlock parses one comment block; ok is false when it holds no known
// directive, or when the block belongs to the type dialect. | parseAnnotationBlock 解析单个
// 注释块；不含已知指令或属于类型方言时 ok 为 false。
func parseAnnotationBlock(block []string, path string, line int) (Annotation, bool) {
	if blockHasTypeDirective(block) {
		return Annotation{}, false
	}
	a := Annotation{Pos: fmt.Sprintf("%s:%d", path, line)}
	found := false
	var (
		bindKind string
		bindSpan [2]int
	)
	for _, raw := range block {
		body := commentBody(raw)
		if !strings.HasPrefix(body, "@") {
			continue
		}
		name, args := splitDirective(body[1:])
		switch name {
		case "summary":
			if a.Summary == "" {
				a.Summary = args
			}
			found = true
		case "description":
			if a.Description == "" {
				a.Description = args
			} else if args != "" {
				a.Description += "\n" + args
			}
			found = true
		case "tags":
			a.Tags = append(a.Tags, splitTokens(args)...)
			found = true
		case "accept":
			a.Accept = append(a.Accept, splitTokens(args)...)
			found = true
		case "produce":
			a.Produce = append(a.Produce, splitTokens(args)...)
			found = true
		case "param":
			before := len(a.Params)
			if p, ok := parseParam(args); ok {
				a.Params = append(a.Params, p)
			}
			if len(a.Params) > before {
				bindKind, bindSpan = "param", [2]int{before, len(a.Params)}
			}
			found = true
		case "success":
			before := len(a.Success)
			a.Success = append(a.Success, parseResponses(args)...)
			if len(a.Success) > before {
				bindKind, bindSpan = "success", [2]int{before, len(a.Success)}
			}
			found = true
		case "failure":
			before := len(a.Failure)
			a.Failure = append(a.Failure, parseResponses(args)...)
			if len(a.Failure) > before {
				bindKind, bindSpan = "failure", [2]int{before, len(a.Failure)}
			}
			found = true
		case "example":
			// 绑定到最近一条声明；未绑定到任何声明的 @Example 不算注解，避免误吞类型注解块。
			// Binds to the latest declaration; an unbound @Example is not an annotation, so type annotation blocks never leak in.
			if bindExample(&a, bindKind, bindSpan, args) {
				found = true
			}
		case "security":
			a.Security = append(a.Security, splitTokens(args)...)
			found = true
		case "router":
			a.RouterPath, a.RouterMethod = parseRouter(args)
			found = true
		}
	}
	return a, found
}

// commentBody strips the comment marker and the surrounding space of one comment line. | commentBody
// 去掉单行注释的注释符与首尾空白。
func commentBody(raw string) string {
	body := strings.TrimSpace(strings.TrimRight(raw, "\r"))
	return strings.TrimSpace(strings.TrimPrefix(body, "//"))
}

// blockHasTypeDirective reports whether a comment block carries a directive that only the
// type dialect understands, such as @Name or @Enum; such a block annotates a struct, a const
// block or a variable and never a handler. | blockHasTypeDirective 报告注释块是否含 @Name、@Enum
// 之类仅类型方言可识别的指令；这类注释块注解的是结构体、常量块或变量，而不是处理器。
func blockHasTypeDirective(block []string) bool {
	for _, raw := range block {
		body := commentBody(raw)
		if !strings.HasPrefix(body, "@") {
			continue
		}
		name, _ := splitDirective(body[1:])
		if typeOnlyDirectives[name] {
			return true
		}
	}
	return false
}

// bindExample attaches an @Example line to the most recent @Param, @Success or @Failure
// declaration and reports whether the example was bound; every entry produced by a comma
// separated status code list receives the same example. | bindExample 把 @Example 行绑定到最近
// 一条 @Param、@Success 或 @Failure 声明并报告是否绑定成功；逗号分隔的状态码展开出的每条响应
// 都会得到同一个示例。
func bindExample(a *Annotation, kind string, span [2]int, example string) bool {
	if a == nil || kind == "" || span[1] <= span[0] {
		return false
	}
	switch kind {
	case "param":
		for i := span[0]; i < span[1] && i < len(a.Params); i++ {
			a.Params[i].Example = example
		}
		return true
	case "success":
		for i := span[0]; i < span[1] && i < len(a.Success); i++ {
			a.Success[i].Example = example
		}
		return true
	case "failure":
		for i := span[0]; i < span[1] && i < len(a.Failure); i++ {
			a.Failure[i].Example = example
		}
		return true
	}
	return false
}

// splitDirective splits "@Summary text" into its lower cased name and arguments. | splitDirective
// 把 "@Summary text" 拆成小写指令名与参数。
func splitDirective(s string) (string, string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return strings.ToLower(s[:i]), strings.TrimSpace(s[i+1:])
	}
	return strings.ToLower(s), ""
}

// splitTokens splits on commas and whitespace, dropping empty tokens. | splitTokens 按逗号与空白切分并丢弃空标记。
func splitTokens(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// splitFields splits on whitespace while keeping a double quoted segment as one field
// with the quotes removed. | splitFields 按空白切分，同时把双引号包裹的部分保留为单个字段并去掉引号。
func splitFields(s string) []string {
	var (
		out    []string
		cur    strings.Builder
		quoted bool
		open   bool
	)
	flush := func() {
		if open || cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
			open = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quoted {
			if c == '"' {
				quoted = false
				continue
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '"':
			quoted = true
			open = true
		case ' ', '\t':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// parseParam parses "@Param name in type required "description"". | parseParam 解析
// "@Param name in type required "description""。
func parseParam(args string) (AnnotationParam, bool) {
	fields := splitFields(args)
	if len(fields) < 4 {
		return AnnotationParam{}, false
	}
	p := AnnotationParam{
		Name:     fields[0],
		In:       strings.ToLower(fields[1]),
		Type:     fields[2],
		Required: strings.EqualFold(fields[3], "true"),
	}
	if len(fields) > 4 {
		p.Description = strings.Join(fields[4:], " ")
	}
	p.Schema = schemaForParam(p.In, p.Type)
	return p, true
}

// parseResponses parses "@Success 200 {object} model.User "description"" style
// directives; comma separated codes expand into one entry each. | parseResponses 解析
// "@Success 200 {object} model.User "description"" 风格指令；逗号分隔的多个状态码会展开为多条。
func parseResponses(args string) []AnnotationResponse {
	fields := splitFields(args)
	if len(fields) == 0 {
		return nil
	}
	index := 1
	kind := ""
	if index < len(fields) && strings.HasPrefix(fields[index], "{") && strings.HasSuffix(fields[index], "}") {
		kind = strings.ToLower(strings.Trim(fields[index], "{}"))
		index++
	}
	token := ""
	if index < len(fields) && !strings.HasPrefix(fields[index], "{") {
		token = fields[index]
		index++
	}
	description := strings.Join(fields[index:], " ")
	var schema *Schema
	if kind != "" || token != "" {
		schema = schemaForResponse(kind, token)
	}

	var out []AnnotationResponse
	for _, code := range strings.Split(fields[0], ",") {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		res := AnnotationResponse{Type: token, Description: description, schema: schema}
		if n, err := strconv.Atoi(code); err == nil {
			res.Code = n
		}
		if schema != nil {
			res.Ref = schema.Ref
		}
		out = append(out, res)
	}
	return out
}

// parseRouter parses "/users/{id} [get]" into the template path and the upper cased
// method; ":id" and "*rest" are normalised to "{id}" and "{rest}". | parseRouter 把
// "/users/{id} [get]" 解析为模板路径与大写方法；":id" 与 "*rest" 会规范化为 "{id}" 与 "{rest}"。
func parseRouter(args string) (string, string) {
	fields := splitFields(args)
	if len(fields) == 0 {
		return "", ""
	}
	path := normalizeRouterPath(fields[0])
	method := ""
	if len(fields) > 1 {
		method = strings.ToUpper(strings.Trim(fields[1], "[]"))
	}
	return path, method
}

// normalizeRouterPath converts ":name" and "*name" segments into "{name}" while
// leaving existing "{name}" templates untouched. | normalizeRouterPath 把 ":name" 与
// "*name" 段转换为 "{name}"，同时保留已有的 "{name}" 模板。
func normalizeRouterPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	var (
		b    strings.Builder
		open bool
	)
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch {
		case open:
			b.WriteByte(c)
			if c == '}' {
				open = false
			}
		case c == '{':
			open = true
			b.WriteByte(c)
		case c == ':' || c == '*':
			j := i + 1
			for j < len(path) && isPathNameChar(path[j]) {
				j++
			}
			if j == i+1 {
				b.WriteByte(c)
				continue
			}
			b.WriteString("{" + path[i+1:j] + "}")
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// isPathNameChar reports whether c may appear in a parameter name. | isPathNameChar 报告 c 是否可用于参数名。
func isPathNameChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
