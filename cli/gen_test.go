package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/spf13/afero"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/shibingli/yd/gen"
)

// writeMemFile writes a file into fs, creating the parent directories on the way. | writeMemFile 写入文件，并沿途创建父目录。
func writeMemFile(t *testing.T, fs afero.Fs, path, content string) {
	t.Helper()
	if dir := filepath.Dir(path); dir != "." {
		if err := fs.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("创建 %s 失败: %v", dir, err)
		}
	}
	if err := afero.WriteFile(fs, path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

// TestGenModuleWritesArtifacts 验证 `yd gen module` 依据字段描述串产出全部生成物。
// TestGenModuleWritesArtifacts verifies that `yd gen module` produces every artifact from a field specification.
func TestGenModuleWritesArtifacts(t *testing.T) {
	te := newTestEnv(t, nil, "", false)

	err := Execute(context.Background(), []string{
		genCommandName, "module", "blog_post",
		"--fields", "title:string:required,max=120,views:int:min=0",
		"--module", "cms",
		"--package", "example.com/blog",
	}, te.env)
	if err != nil {
		t.Fatalf("yd gen module 失败: %v\n%s", err, te.errs.String())
	}

	for _, path := range []string{
		"internal/model/blog_post.go",
		"internal/repository/blog_post_repo.go",
		"internal/service/blog_post_service.go",
		"internal/controller/blog_post_controller.go",
		"internal/router/blog_post_routes.go",
		"internal/router/routes_gen.go",
		"web/templates/blog_post/list.html",
		"web/templates/blog_post/form.html",
		"web/templates/blog_post/row.html",
		"web/templates/blog_post/_fields.html",
	} {
		if !existsMem(t, te.env.FS, path) {
			t.Errorf("缺少生成物 %s", path)
		}
	}

	model := readMemFile(t, te.env.FS, "internal/model/blog_post.go")
	for _, want := range []string{
		gen.GeneratedMarker,
		"type BlogPost struct",
		`func (BlogPost) TableName() string { return "blog_posts" }`,
		`json:"title"`,
	} {
		if !strings.Contains(model, want) {
			t.Errorf("模型缺少 %q:\n%s", want, model)
		}
	}

	controller := readMemFile(t, te.env.FS, "internal/controller/blog_post_controller.go")
	for _, want := range []string{"example.com/blog/internal/service", "c.HTMLString(", "@Tags cms"} {
		if !strings.Contains(controller, want) {
			t.Errorf("控制器缺少 %q:\n%s", want, controller)
		}
	}
	if strings.Contains(controller, "c.JSON(") {
		t.Errorf("控制器不得以 c.JSON 作为主响应:\n%s", controller)
	}

	out := te.out.String()
	for _, want := range []string{"written | 已写入", "internal/controller/blog_post_controller.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("命令输出缺少 %q:\n%s", want, out)
		}
	}
}

// TestGenModuleTableOverride 验证 --table 覆盖从模块名推导出的表名。
// TestGenModuleTableOverride verifies that --table overrides the table name derived from the module name.
func TestGenModuleTableOverride(t *testing.T) {
	te := newTestEnv(t, nil, "", false)

	err := Execute(context.Background(), []string{
		genCommandName, "module", "Post",
		"--fields", "title:string",
		"--table", "cms_posts",
		"--package", "example.com/blog",
	}, te.env)
	if err != nil {
		t.Fatalf("yd gen module --table 失败: %v\n%s", err, te.errs.String())
	}

	model := readMemFile(t, te.env.FS, "internal/model/post.go")
	if !strings.Contains(model, `func (Post) TableName() string { return "cms_posts" }`) {
		t.Errorf("--table 未生效:\n%s", model)
	}
}

// TestGenModuleDryRunWritesNothing 验证 --dry-run 只报告待写入文件而不落盘。
// TestGenModuleDryRunWritesNothing verifies that --dry-run reports the pending files without writing any.
func TestGenModuleDryRunWritesNothing(t *testing.T) {
	te := newTestEnv(t, nil, "", false)

	err := Execute(context.Background(), []string{
		genCommandName, "module", "Post",
		"--fields", "title:string",
		"--dry-run",
	}, te.env)
	if err != nil {
		t.Fatalf("yd gen module --dry-run 失败: %v\n%s", err, te.errs.String())
	}

	if existsMem(t, te.env.FS, "internal/model/post.go") {
		t.Error("--dry-run must not write any file | --dry-run 不得写入任何文件")
	}
	out := te.out.String()
	for _, want := range []string{"dry run | 试运行", "internal/model/post.go", "internal/router/routes_gen.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("试运行输出缺少 %q:\n%s", want, out)
		}
	}
}

// TestGenCrudFromFields 验证 `yd gen crud --fields`（README 里的写法）与 module 等价。
// TestGenCrudFromFields verifies that `yd gen crud --fields`, the spelling used in the README, matches module.
func TestGenCrudFromFields(t *testing.T) {
	te := newTestEnv(t, nil, "", false)

	err := Execute(context.Background(), []string{
		genCommandName, "crud", "user",
		"--fields", "name:string,age:int",
		"--package", "example.com/blog",
	}, te.env)
	if err != nil {
		t.Fatalf("yd gen crud --fields 失败: %v\n%s", err, te.errs.String())
	}

	model := readMemFile(t, te.env.FS, "internal/model/user.go")
	for _, want := range []string{"type User struct", `func (User) TableName() string { return "users" }`} {
		if !strings.Contains(model, want) {
			t.Errorf("模型缺少 %q:\n%s", want, model)
		}
	}
}

// TestGenCrudFromTable 用真实 sqlite 表验证 `yd gen crud --table`。
// TestGenCrudFromTable verifies `yd gen crud --table` against a real sqlite table.
func TestGenCrudFromTable(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "gen_cli.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开 sqlite 失败: %v", err)
	}
	if err := db.Exec(`CREATE TABLE articles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title VARCHAR(80) NOT NULL,
		body TEXT
	)`).Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取 *sql.DB 失败: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("关闭 sqlite 失败: %v", err)
	}

	te := newTestEnv(t, nil, "", false)
	err = Execute(context.Background(), []string{
		genCommandName, "crud", "Article",
		"--table", "articles",
		"--dsn", dsn,
		"--module", "cms",
		"--package", "example.com/blog",
	}, te.env)
	if err != nil {
		t.Fatalf("yd gen crud --table 失败: %v\n%s", err, te.errs.String())
	}

	model := readMemFile(t, te.env.FS, "internal/model/article.go")
	for _, want := range []string{
		"type Article struct",
		`func (Article) TableName() string { return "articles" }`,
		`gorm:"column:title;size:80;not null"`,
	} {
		if !strings.Contains(model, want) {
			t.Errorf("模型缺少 %q:\n%s", want, model)
		}
	}
}

// TestGenCrudFromStruct 验证 `yd gen crud --struct`：源码里的模型直接生成，包名取自 go.mod。
// TestGenCrudFromStruct verifies `yd gen crud --struct`: a model is generated straight from source, with the package taken from go.mod.
func TestGenCrudFromStruct(t *testing.T) {
	fs := afero.NewMemMapFs()
	writeMemFile(t, fs, "go.mod", "module example.com/blog\n\ngo 1.27\n")
	writeMemFile(t, fs, "internal/model/post.go", `package model

import "time"

// Post is the article model. | Post 是文章模型。
type Post struct {
	ID        int64     `+"`"+`gorm:"column:id;primaryKey" json:"id"`+"`"+`
	Title     string    `+"`"+`gorm:"column:title;size:120;not null" json:"title"`+"`"+`
	Body      string    `+"`"+`gorm:"column:body" json:"body"`+"`"+`
	CreatedAt time.Time `+"`"+`gorm:"column:created_at" json:"created_at"`+"`"+`
}

// TableName returns the table name of the article. | TableName 返回文章表名。
func (Post) TableName() string { return "cms_posts" }
`)
	te := newTestEnv(t, fs, "", false)

	err := Execute(context.Background(), []string{
		genCommandName, "crud", "Post",
		"--struct", "internal/model.Post",
		"--module", "cms",
	}, te.env)
	if err != nil {
		t.Fatalf("yd gen crud --struct 失败: %v\n%s", err, te.errs.String())
	}

	model := readMemFile(t, te.env.FS, "internal/model/post.go")
	for _, want := range []string{
		`func (Post) TableName() string { return "cms_posts" }`,
		`gorm:"column:title;size:120;not null"`,
	} {
		if !strings.Contains(model, want) {
			t.Errorf("模型缺少 %q:\n%s", want, model)
		}
	}

	// The import prefix comes from the module path in go.mod. | 导入前缀取 go.mod 里的模块路径。
	svc := readMemFile(t, te.env.FS, "internal/service/post_service.go")
	if !strings.Contains(svc, `"example.com/blog/internal/repository"`) {
		t.Errorf("服务层未使用 go.mod 的模块路径:\n%s", svc)
	}
}

// TestGenModuleIdempotentAndForce 验证重复生成幂等，且 --force 才能改写手写文件。
// TestGenModuleIdempotentAndForce verifies that regeneration is idempotent and that only --force rewrites a hand-written file.
func TestGenModuleIdempotentAndForce(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	args := []string{genCommandName, "module", "Post", "--fields", "title:string", "--package", "example.com/blog"}

	if err := Execute(context.Background(), args, te.env); err != nil {
		t.Fatalf("首次生成失败: %v\n%s", err, te.errs.String())
	}
	first := readMemFile(t, te.env.FS, "internal/controller/post_controller.go")

	te.out.Reset()
	if err := Execute(context.Background(), args, te.env); err != nil {
		t.Fatalf("重复生成失败: %v\n%s", err, te.errs.String())
	}
	if got := readMemFile(t, te.env.FS, "internal/controller/post_controller.go"); got != first {
		t.Error("regeneration differs from the first run | 重复生成的内容与首次不一致")
	}
	if out := te.out.String(); !strings.Contains(out, "skipped | 已跳过") {
		t.Errorf("重复生成应报告跳过:\n%s", out)
	}

	// A hand-written file (no generator marker) is protected by default; only --force overwrites it. | 手写文件（无生成标记）默认被保护，--force 才会覆盖。
	writeMemFile(t, te.env.FS, "internal/model/post.go", "package model\n\n// 手写模型。| hand-written model.\ntype Post struct{}\n")
	te.out.Reset()
	if err := Execute(context.Background(), args, te.env); err != nil {
		t.Fatalf("第三次生成失败: %v\n%s", err, te.errs.String())
	}
	if !strings.Contains(readMemFile(t, te.env.FS, "internal/model/post.go"), "手写模型") {
		t.Error("a hand-written file without the marker was overwritten | 没有生成标记的手写文件被覆盖了")
	}

	te.out.Reset()
	forced := append(append([]string{}, args...), "--force")
	if err := Execute(context.Background(), forced, te.env); err != nil {
		t.Fatalf("yd gen module --force 失败: %v\n%s", err, te.errs.String())
	}
	if !strings.Contains(readMemFile(t, te.env.FS, "internal/model/post.go"), gen.GeneratedMarker) {
		t.Error("--force did not overwrite the hand-written file | --force 未覆盖手写文件")
	}
}

// TestGenArgumentErrors 覆盖子命令与标志的错误分支。
// TestGenArgumentErrors covers the error branches of the subcommands and flags.
func TestGenArgumentErrors(t *testing.T) {
	t.Run("缺少子命令", func(t *testing.T) {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{genCommandName}, te.env)
		if !errors.Is(err, ErrNoCommand) {
			t.Errorf("错误 = %v，期望 ErrNoCommand", err)
		}
		if !strings.Contains(te.out.String(), "yd gen") {
			t.Errorf("缺少用法输出:\n%s", te.out.String())
		}
	})

	t.Run("未知子命令", func(t *testing.T) {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{genCommandName, "nope"}, te.env)
		if !errors.Is(err, ErrUnknownCommand) {
			t.Errorf("错误 = %v，期望 ErrUnknownCommand", err)
		}
	})

	t.Run("缺少模块名", func(t *testing.T) {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{genCommandName, "module", "--fields", "title:string"}, te.env)
		if !errors.Is(err, ErrNoCommand) {
			t.Errorf("错误 = %v，期望 ErrNoCommand", err)
		}
	})

	t.Run("缺少字段描述", func(t *testing.T) {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{genCommandName, "module", "Post"}, te.env)
		if !errors.Is(err, ErrFlagParse) {
			t.Errorf("错误 = %v，期望 ErrFlagParse", err)
		}
	})

	t.Run("字段描述非法", func(t *testing.T) {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{genCommandName, "module", "Post", "--fields", "title:nope"}, te.env)
		if !errors.Is(err, gen.ErrUnknownType) {
			t.Errorf("错误 = %v，期望 gen.ErrUnknownType", err)
		}
	})

	t.Run("crud 缺少数据来源", func(t *testing.T) {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{genCommandName, "crud", "Post"}, te.env)
		if !errors.Is(err, ErrFlagParse) {
			t.Errorf("错误 = %v，期望 ErrFlagParse", err)
		}
	})

	t.Run("crud 同时给出 table 与 struct", func(t *testing.T) {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{
			genCommandName, "crud", "Post",
			"--table", "posts", "--dsn", "file::memory:?cache=shared",
			"--struct", "internal/model.Post",
		}, te.env)
		if !errors.Is(err, ErrFlagParse) {
			t.Errorf("错误 = %v，期望 ErrFlagParse", err)
		}
	})

	t.Run("crud 缺少 dsn", func(t *testing.T) {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{genCommandName, "crud", "Post", "--table", "posts"}, te.env)
		if !errors.Is(err, ErrFlagParse) {
			t.Errorf("错误 = %v，期望 ErrFlagParse", err)
		}
	})

	t.Run("struct 取值非法", func(t *testing.T) {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{genCommandName, "crud", "Post", "--struct", "Post"}, te.env)
		if !errors.Is(err, ErrFlagParse) {
			t.Errorf("错误 = %v，期望 ErrFlagParse", err)
		}
	})

	t.Run("struct 源码缺失", func(t *testing.T) {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{genCommandName, "crud", "Post", "--struct", "internal/model.Post"}, te.env)
		if !errors.Is(err, gen.ErrReadFile) {
			t.Errorf("错误 = %v，期望 gen.ErrReadFile", err)
		}
	})

	t.Run("表不存在", func(t *testing.T) {
		dsn := filepath.Join(t.TempDir(), "empty.db")
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			t.Fatalf("打开 sqlite 失败: %v", err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatalf("获取 *sql.DB 失败: %v", err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("关闭 sqlite 失败: %v", err)
		}

		te := newTestEnv(t, nil, "", false)
		err = Execute(context.Background(), []string{
			genCommandName, "crud", "Post", "--table", "missing", "--dsn", dsn,
		}, te.env)
		if !errors.Is(err, gen.ErrTableNotFound) {
			t.Errorf("错误 = %v，期望 gen.ErrTableNotFound", err)
		}
	})

	t.Run("不支持的驱动", func(t *testing.T) {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{
			genCommandName, "crud", "Post", "--table", "posts", "--dsn", "x", "--driver", "oracle",
		}, te.env)
		if err == nil {
			t.Error("an unknown driver must return an error | 未知驱动应返回错误")
		}
	})
}
