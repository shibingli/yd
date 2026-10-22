package gen_test

import (
	"context"
	"errors"
	"fmt"
	"go/format"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/tooling/gen"
	"github.com/shibingli/ydx/afs"
)

// sampleModel 是生成器测试夹具：含主键、字符串、文本、数值与时间字段，并开启时间戳。
// sampleModel is the generator fixture: it carries a primary key plus string, text, numeric and time fields, with timestamps enabled.
func sampleModel() *gen.Model {
	return &gen.Model{
		Name:   "Post",
		GoName: "Post",
		Table:  "posts",
		Module: "cms",
		Fields: []gen.Field{
			{Name: "id", GoName: "ID", GoType: "int64", Column: "id", JSONName: "id", Primary: true, Label: "ID", Comment: "Primary key | 主键"},
			{Name: "title", GoName: "Title", GoType: "string", Column: "title", JSONName: "title", Size: 120, Widget: "text", Label: "标题", Validate: "required,max=120", Comment: "Post title | 文章标题"},
			{Name: "body", GoName: "Body", GoType: "string", Column: "body", JSONName: "body", Nullable: true, Widget: "textarea", Label: "正文"},
			{Name: "views", GoName: "Views", GoType: "int64", Column: "views", JSONName: "views", Widget: "number", Label: "浏览量", Validate: "min=0"},
			{Name: "published_at", GoName: "PublishedAt", GoType: "time.Time", Column: "published_at", JSONName: "published_at", Nullable: true, Widget: "date", Label: "发布时间"},
		},
		Timestamps: true,
	}
}

// sampleOptions 返回写入 MemFS、模块路径为示例工程的生成选项。
// sampleOptions returns the generation options targeting a MemFS with an example project module path.
func sampleOptions() gen.Options {
	return gen.Options{Package: "example.com/blog"}
}

// walkFiles 列出 fs 上的全部文件（仓库相对路径、斜杠分隔）。
// walkFiles lists every file on fs (repository-relative, slash separated).
func walkFiles(t *testing.T, fsys afero.Fs) []string {
	t.Helper()
	var out []string
	err := afs.Walk(fsys, ".", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		out = append(out, filepath.ToSlash(p))
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 MemFS 失败: %v", err)
	}
	sort.Strings(out)
	return out
}

// readFile 读取 MemFS 上的文本文件。
// readFile reads a text file from the MemFS.
func readFile(t *testing.T, fsys afero.Fs, path string) string {
	t.Helper()
	b, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return string(b)
}

// wantArtifacts 是生成器必须产出的全部文件。
// wantArtifacts is the complete set of files the generator must produce.
var wantArtifacts = []string{
	"internal/controller/post_controller.go",
	"internal/model/post.go",
	"internal/repository/post_repo.go",
	"internal/router/post_routes.go",
	"internal/router/routes_gen.go",
	"internal/service/post_service.go",
	"web/templates/post/_fields.html",
	"web/templates/post/form.html",
	"web/templates/post/list.html",
	"web/templates/post/row.html",
}

// TestGenerateWritesAllArtifacts 验证生成物清单与头部生成标记。
// TestGenerateWritesAllArtifacts verifies the artifact inventory and the generated-file header.
func TestGenerateWritesAllArtifacts(t *testing.T) {
	fsys := afs.NewMem()
	res, err := gen.Generate(context.Background(), sampleModel(), sampleOptions(), fsys)
	if err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}

	got := walkFiles(t, fsys)
	if strings.Join(got, "\n") != strings.Join(wantArtifacts, "\n") {
		t.Errorf("生成文件清单不符\n got: %v\nwant: %v", got, wantArtifacts)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("首次生成不应有跳过项: %v", res.Skipped)
	}
	if len(res.Written) != len(wantArtifacts) {
		t.Errorf("Written 条数 = %d，期望 %d: %v", len(res.Written), len(wantArtifacts), res.Written)
	}
	for _, p := range got {
		content := readFile(t, fsys, p)
		if strings.HasSuffix(p, ".go") {
			if !strings.HasPrefix(content, gen.GeneratedMarker) {
				t.Errorf("%s 缺少生成标记 %q", p, gen.GeneratedMarker)
			}
			continue
		}
		if !strings.HasPrefix(content, "<!-- "+gen.GeneratedMarker+" -->") {
			t.Errorf("%s 缺少 HTML 生成标记", p)
		}
	}
}

// TestGenerateProducesFormattedGoCode 验证全部 .go 生成物都能被 format.Source 解析，且 HTML 模板可被 html/template 解析。
// TestGenerateProducesFormattedGoCode verifies every generated .go file parses through format.Source and every HTML template parses through html/template.
func TestGenerateProducesFormattedGoCode(t *testing.T) {
	fsys := afs.NewMem()
	if _, err := gen.Generate(context.Background(), sampleModel(), sampleOptions(), fsys); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}

	for _, p := range walkFiles(t, fsys) {
		content := readFile(t, fsys, p)
		switch {
		case strings.HasSuffix(p, ".go"):
			if _, err := format.Source([]byte(content)); err != nil {
				t.Errorf("%s 不能被 go/format 解析: %v\n---\n%s", p, err, content)
			}
		case strings.HasSuffix(p, ".html"):
			parsed, err := template.New(filepath.ToSlash(p)).
				Funcs(template.FuncMap{"pathEscape": func(value any) string {
					return fmt.Sprint(value)
				}}).Parse(content)
			if err != nil {
				t.Errorf("%s 不能被 html/template 解析: %v\n---\n%s", p, err, content)
			}
			_ = parsed
		}
	}

	t.Run("导入前缀", func(t *testing.T) {
		svc := readFile(t, fsys, "internal/service/post_service.go")
		if !strings.Contains(svc, `"example.com/blog/internal/repository"`) {
			t.Errorf("生成代码未使用 Options.Package 作为内部导入前缀:\n%s", svc)
		}
		if !strings.Contains(svc, `"github.com/shibingli/ydx/errs"`) || !strings.Contains(svc, `"github.com/shibingli/ydx/log"`) {
			t.Error("generated code does not import the framework errs/log packages | 生成代码未引用框架 errs/log 包")
		}
		if strings.Contains(svc, "fmt.Errorf") {
			t.Error("generated code must not use fmt.Errorf | 生成代码不得使用 fmt.Errorf")
		}
	})

	t.Run("模型标签", func(t *testing.T) {
		model := readFile(t, fsys, "internal/model/post.go")
		for _, want := range []string{
			"gorm:\"column:id;primaryKey;autoIncrement\"",
			"gorm:\"column:title;size:120;not null\"",
			"func (Post) TableName() string { return \"posts\" }",
			"CreatedAt time.Time",
		} {
			if !strings.Contains(model, want) {
				t.Errorf("模型文件缺少片段 %q:\n%s", want, model)
			}
		}
	})

	t.Run("搜索转义", func(t *testing.T) {
		repo := readFile(t, fsys, "internal/repository/post_repo.go")
		for _, want := range []string{
			`LIKE ? ESCAPE '!'`,
			`strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")`,
		} {
			if !strings.Contains(repo, want) {
				t.Errorf("生成仓库缺少 LIKE 转义片段 %q:\n%s", want, repo)
			}
		}
	})
}

// TestGeneratedControllerReturnsHTMLFragments 验证控制器返回 HTML 片段、校验失败返回 422 片段，且注解块完整。
// TestGeneratedControllerReturnsHTMLFragments verifies the controller returns HTML fragments, answers validation failures with a 422 fragment, and carries complete annotation blocks.
func TestGeneratedControllerReturnsHTMLFragments(t *testing.T) {
	fsys := afs.NewMem()
	if _, err := gen.Generate(context.Background(), sampleModel(), sampleOptions(), fsys); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}
	ctrl := readFile(t, fsys, "internal/controller/post_controller.go")

	for _, want := range []string{
		`c.HTML(http.StatusOK, "post/list.html"`,
		`c.HTML(http.StatusOK, "post/form.html"`,
		`c.HTML(http.StatusOK, "post/row.html"`,
		"htmx.ErrorFragment(",
		"htmx.TriggerFromCtx(",
		"http.StatusUnprocessableEntity",
	} {
		if !strings.Contains(ctrl, want) {
			t.Errorf("控制器缺少片段 %q:\n%s", want, ctrl)
		}
	}
	if strings.Contains(ctrl, "c.JSON") {
		t.Error("the controller must not use c.JSON as its primary response | 控制器不得以 c.JSON 作为主响应")
	}

	// The OpenAPI block of every handler must be complete. | 每个处理器的 OpenAPI 注解块必须完整。
	required := []string{"@Summary", "@Description", "@Tags", "@Accept", "@Produce", "@Param", "@Success", "@Failure", "@Router"}
	for _, key := range required {
		if got := strings.Count(ctrl, key); got < 7 {
			t.Errorf("注解 %s 出现 %d 次，至少应有 7 个处理器各一次", key, got)
		}
	}
	if got := strings.Count(ctrl, "@Router"); got != 7 {
		t.Errorf("@Router 出现 %d 次，期望 7", got)
	}
	for _, r := range []string{"@Router /post [get]", "@Router /post [post]", "@Router /post/{id} [get]", "@Router /post/{id} [put]", "@Router /post/{id} [delete]", "@Router /post/{id}/edit [get]", "@Router /post/new [get]"} {
		if !strings.Contains(ctrl, r) {
			t.Errorf("控制器缺少路由注解 %q", r)
		}
	}
	if !strings.Contains(ctrl, "@Tags cms") {
		t.Error("the annotation block must use the module name as @Tags | 注解块应使用模块名作为 @Tags")
	}
}

// TestGenerateIdempotent 验证相同输入重复生成时内容不变且全部跳过。
// TestGenerateIdempotent verifies repeated generation with the same input changes nothing and skips everything.
func TestGenerateIdempotent(t *testing.T) {
	fsys := afs.NewMem()
	first, err := gen.Generate(context.Background(), sampleModel(), sampleOptions(), fsys)
	if err != nil {
		t.Fatalf("首次 Generate 返回错误: %v", err)
	}
	before := map[string]string{}
	for _, p := range walkFiles(t, fsys) {
		before[p] = readFile(t, fsys, p)
	}

	second, err := gen.Generate(context.Background(), sampleModel(), sampleOptions(), fsys)
	if err != nil {
		t.Fatalf("二次 Generate 返回错误: %v", err)
	}
	if len(second.Written) != 0 {
		t.Errorf("二次生成不应写入任何文件: %v", second.Written)
	}
	if len(second.Skipped) != len(first.Written) {
		t.Errorf("二次生成跳过 %d 个文件，期望 %d: %v", len(second.Skipped), len(first.Written), second.Skipped)
	}
	for p, want := range before {
		if got := readFile(t, fsys, p); got != want {
			t.Errorf("%s 在二次生成后内容变化", p)
		}
	}
}

// TestGenerateRespectsExistingCustomFiles 验证没有生成标记的手写文件不被覆盖，Force 时才覆盖。
// TestGenerateRespectsExistingCustomFiles verifies hand-written files without the marker are preserved, and overwritten only under Force.
func TestGenerateRespectsExistingCustomFiles(t *testing.T) {
	const custom = "package controller\n\n// HandWritten is a user file. | HandWritten 是用户手写文件。\nfunc HandWritten() string { return \"mine\" }\n"

	fsys := afs.NewMem()
	if err := afero.WriteFile(fsys, "internal/controller/post_controller.go", []byte(custom), 0o644); err != nil {
		t.Fatalf("预置手写文件失败: %v", err)
	}

	res, err := gen.Generate(context.Background(), sampleModel(), sampleOptions(), fsys)
	if err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}
	if got := readFile(t, fsys, "internal/controller/post_controller.go"); got != custom {
		t.Errorf("手写文件被覆盖:\n%s", got)
	}
	if !contains(res.Skipped, "internal/controller/post_controller.go") {
		t.Errorf("手写文件未出现在 Skipped 中: %v", res.Skipped)
	}
	if !contains(res.Written, "internal/model/post.go") {
		t.Errorf("其余文件应正常生成: %v", res.Written)
	}

	forced := sampleOptions()
	forced.Force = true
	res, err = gen.Generate(context.Background(), sampleModel(), forced, fsys)
	if err != nil {
		t.Fatalf("Force Generate 返回错误: %v", err)
	}
	got := readFile(t, fsys, "internal/controller/post_controller.go")
	if got == custom {
		t.Error("Force did not overwrite the hand-written file | Force 未覆盖手写文件")
	}
	if !strings.HasPrefix(got, gen.GeneratedMarker) {
		t.Errorf("Force 覆盖后缺少生成标记:\n%s", got)
	}
	if !contains(res.Written, "internal/controller/post_controller.go") {
		t.Errorf("Force 覆盖的文件未出现在 Written 中: %v", res.Written)
	}
}

// TestRoutesGenMarkerBlockReplacedNotDuplicated 验证 routes_gen.go 的标记区块只替换、不重复。
// TestRoutesGenMarkerBlockReplacedNotDuplicated verifies the marker block of routes_gen.go is replaced, never duplicated.
func TestRoutesGenMarkerBlockReplacedNotDuplicated(t *testing.T) {
	const path = "internal/router/routes_gen.go"
	fsys := afs.NewMem()

	if _, err := gen.Generate(context.Background(), sampleModel(), sampleOptions(), fsys); err != nil {
		t.Fatalf("首次 Generate 返回错误: %v", err)
	}
	first := readFile(t, fsys, path)
	if n := strings.Count(first, "//yd:gen:begin"); n != 1 {
		t.Fatalf("起始标记出现 %d 次，期望 1:\n%s", n, first)
	}
	if n := strings.Count(first, "RegisterPostRoutes(g, deps.Post)"); n != 1 {
		t.Fatalf("注册行出现 %d 次，期望 1:\n%s", n, first)
	}

	// A second module must join the block without losing the first one. | 第二个模块加入区块时不得丢掉第一个。
	tag := sampleModel()
	tag.Name, tag.GoName, tag.Table, tag.Module = "Tag", "Tag", "tags", "cms"
	if _, err := gen.Generate(context.Background(), tag, sampleOptions(), fsys); err != nil {
		t.Fatalf("生成第二个模块返回错误: %v", err)
	}
	second := readFile(t, fsys, path)
	if n := strings.Count(second, "//yd:gen:begin"); n != 1 {
		t.Errorf("第二次生成后起始标记出现 %d 次，期望 1:\n%s", n, second)
	}
	if !strings.Contains(second, "RegisterPostRoutes(g, deps.Post)") || !strings.Contains(second, "RegisterTagRoutes(g, deps.Tag)") {
		t.Errorf("两个模块应同时注册:\n%s", second)
	}
	if !strings.Contains(second, "Post *controller.PostController") || !strings.Contains(second, "Tag *controller.TagController") {
		t.Errorf("依赖结构体应包含两个控制器:\n%s", second)
	}

	// Regenerating the first module must replace its line instead of appending. | 重新生成第一个模块应替换其注册行，而不是追加。
	if _, err := gen.Generate(context.Background(), sampleModel(), sampleOptions(), fsys); err != nil {
		t.Fatalf("重复生成第一个模块返回错误: %v", err)
	}
	third := readFile(t, fsys, path)
	if n := strings.Count(third, "RegisterPostRoutes(g, deps.Post)"); n != 1 {
		t.Errorf("重复生成后注册行出现 %d 次，期望 1:\n%s", n, third)
	}
	if n := strings.Count(third, "//yd:gen:end"); n != 1 {
		t.Errorf("结束标记出现 %d 次，期望 1:\n%s", n, third)
	}

	t.Run("手写内容保留", func(t *testing.T) {
		const custom = "package router\n\n// Custom registers a hand-written route. | Custom 注册手写路由。\nfunc Custom(g *router.Group) { _ = g }\n"
		customFS := afs.NewMem()
		if err := afero.WriteFile(customFS, path, []byte(custom), 0o644); err != nil {
			t.Fatalf("预置手写 routes_gen.go 失败: %v", err)
		}
		if _, err := gen.Generate(context.Background(), sampleModel(), sampleOptions(), customFS); err != nil {
			t.Fatalf("Generate 返回错误: %v", err)
		}
		merged := readFile(t, customFS, path)
		if !strings.Contains(merged, "func Custom(g *router.Group)") {
			t.Errorf("手写内容丢失:\n%s", merged)
		}
		if !strings.Contains(merged, "RegisterPostRoutes(g, deps.Post)") {
			t.Errorf("标记区块未补写:\n%s", merged)
		}
	})
}

// TestDryRunDoesNotWrite 验证 --dry-run 只报告、不落盘。
// TestDryRunDoesNotWrite verifies --dry-run only reports and never touches the filesystem.
func TestDryRunDoesNotWrite(t *testing.T) {
	fsys := afs.NewMem()
	opts := sampleOptions()
	opts.DryRun = true

	res, err := gen.Generate(context.Background(), sampleModel(), opts, fsys)
	if err != nil {
		t.Fatalf("DryRun Generate 返回错误: %v", err)
	}
	if got := walkFiles(t, fsys); len(got) != 0 {
		t.Errorf("试运行不应写入任何文件: %v", got)
	}
	if len(res.Written) != len(wantArtifacts) {
		t.Errorf("试运行应报告 %d 个待写入文件，实际 %d: %v", len(wantArtifacts), len(res.Written), res.Written)
	}
	if !contains(res.Written, "web/templates/post/list.html") {
		t.Errorf("试运行报告缺少模板文件: %v", res.Written)
	}

	// A real run afterwards must produce exactly the reported files. | 随后真实写入的文件应与试运行报告一致。
	real, err := gen.Generate(context.Background(), sampleModel(), sampleOptions(), fsys)
	if err != nil {
		t.Fatalf("真实 Generate 返回错误: %v", err)
	}
	sort.Strings(res.Written)
	sort.Strings(real.Written)
	if strings.Join(res.Written, ",") != strings.Join(real.Written, ",") {
		t.Errorf("试运行与真实写入清单不一致\n dry: %v\nreal: %v", res.Written, real.Written)
	}
}

// TestGenerateStopsOnCancelledContext 验证已取消的上下文会中止生成。
// TestGenerateStopsOnCancelledContext verifies a cancelled context aborts generation.
func TestGenerateStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fsys := afs.NewMem()
	_, err := gen.Generate(ctx, sampleModel(), sampleOptions(), fsys)
	if !errors.Is(err, gen.ErrCancelled) {
		t.Errorf("错误 = %v，期望 ErrCancelled", err)
	}
	if got := walkFiles(t, fsys); len(got) != 0 {
		t.Errorf("取消后不应写入任何文件: %v", got)
	}
}

// TestGenerateRejectsUnsupportedPrimaryKey verifies floats and []byte cannot be route keys. |
// TestGenerateRejectsUnsupportedPrimaryKey 验证浮点与 []byte 不能作为路由主键。
func TestGenerateRejectsUnsupportedPrimaryKey(t *testing.T) {
	for _, goType := range []string{"float64", "[]byte"} {
		m := sampleModel()
		m.Fields[0].GoType = goType
		if _, err := gen.Generate(context.Background(), m, sampleOptions(), afs.NewMem()); !errors.Is(err, gen.ErrBadModel) {
			t.Errorf("primary key %s error = %v, want ErrBadModel", goType, err)
		}
	}
}

// TestGenerateRejectsUnquotedIdentifiers verifies uppercase and reserved column names are refused
// because the generated SQL does not quote identifiers. | TestGenerateRejectsUnquotedIdentifiers
// 验证大写与保留字列名被拒绝，因为生成 SQL 不对标识符加引号。
func TestGenerateRejectsUnquotedIdentifiers(t *testing.T) {
	for _, column := range []string{"Title", "select", "order"} {
		m := sampleModel()
		m.Fields[1].Column = column
		if _, err := gen.Generate(context.Background(), m, sampleOptions(), afs.NewMem()); !errors.Is(err, gen.ErrBadModel) {
			t.Errorf("column %q error = %v, want ErrBadModel", column, err)
		}
	}
}

// TestGenerateRejectsEmptyModel 验证空模型被拒绝。
// TestGenerateRejectsEmptyModel verifies an empty model is rejected.
func TestGenerateRejectsEmptyModel(t *testing.T) {
	_, err := gen.Generate(context.Background(), &gen.Model{Name: "Empty"}, gen.Options{}, afs.NewMem())
	if !errors.Is(err, gen.ErrNoFields) {
		t.Errorf("错误 = %v，期望 ErrNoFields", err)
	}
	if _, err := gen.Generate(context.Background(), nil, gen.Options{}, afs.NewMem()); !errors.Is(err, gen.ErrBadModel) {
		t.Errorf("nil model error = %v, want ErrBadModel", err)
	}
}

// TestGenerateRejectsWindowsDeviceModelName proves a model whose snake name is a Windows device
// cannot create a file or directory that resolves to a device. | TestGenerateRejectsWindowsDeviceModelName
// 证明蛇形名是 Windows 设备名的模型不会创建解析为设备的文件或目录。
func TestGenerateRejectsWindowsDeviceModelName(t *testing.T) {
	for _, name := range []string{"Con", "Aux", "Com1", "Lpt9", "Nul"} {
		m := sampleModel()
		m.Name, m.GoName = name, name
		if _, err := gen.Generate(context.Background(), m, sampleOptions(), afs.NewMem()); !errors.Is(err, gen.ErrBadModel) {
			t.Errorf("Generate(%q) 错误 = %v，期望 ErrBadModel | want ErrBadModel", name, err)
		}
	}
	m := sampleModel()
	m.Name, m.GoName = "Console", "Console"
	if _, err := gen.Generate(context.Background(), m, sampleOptions(), afs.NewMem()); err != nil {
		t.Fatalf("非设备名模型应成功 | a non-device name must succeed: %v", err)
	}
}

// TestGenerateUnderDir 验证 Options.Dir 之下相对落盘。
// TestGenerateUnderDir verifies artifacts land relative to Options.Dir.
func TestGenerateUnderDir(t *testing.T) {
	fsys := afs.NewMem()
	opts := sampleOptions()
	opts.Dir = "app"
	if _, err := gen.Generate(context.Background(), sampleModel(), opts, fsys); err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}
	if !afs.Exists(fsys, filepath.Join("app", "internal", "model", "post.go")) {
		t.Errorf("Dir 未被应用: %v", walkFiles(t, fsys))
	}
}

// contains 报告 ss 中是否包含 want。
// contains reports whether ss contains want.
func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestModelElapsedGuard 保证夹具中的时间字段确实参与了生成（避免 time 导入被优化掉后用例失去意义）。
// TestModelElapsedGuard keeps the fixture's time field meaningful for generation.
func TestModelElapsedGuard(t *testing.T) {
	var _ time.Time
	if got := sampleModel().Field("published_at"); got == nil || got.GoType != "time.Time" {
		t.Fatalf("夹具缺少 time.Time 字段: %+v", got)
	}
}
