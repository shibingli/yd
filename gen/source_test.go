package gen_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/gen"
)

// sourcePost 是一段贴近真实项目的模型源码：内嵌 gorm.Model、gorm/json/yd 三种标签、
// 需要被跳过的关系字段与未导出字段，以及一个自带表名的方法。
// sourcePost is model source close to a real project: an embedded gorm.Model, all three
// tag families, relation and unexported fields that must be skipped, and a TableName method.
const sourcePost = `package model

import (
	"time"

	"gorm.io/gorm"
)

// Post is the article model. | Post 是文章模型。
type Post struct {
	gorm.Model
	Title string    ` + "`" + `gorm:"column:title;size:120;not null" json:"title" yd:"label=标题;validate=required,max=120"` + "`" + `
	Body  string    ` + "`" + `gorm:"column:body" json:"body"` + "`" + `
	Views int64     ` + "`" + `gorm:"column:views;not null" json:"views"` + "`" + `
	At    time.Time ` + "`" + `gorm:"column:published_at" json:"published_at"` + "`" + `
	Cover *string   ` + "`" + `gorm:"column:cover" json:"cover"` + "`" + `
	Raw   []byte    ` + "`" + `gorm:"column:raw" json:"raw"` + "`" + `
	Extra string    ` + "`" + `json:"extra"` + "`" + `
	Tags  []string  ` + "`" + `gorm:"-"` + "`" + `
	Meta  map[string]string
	Owner Author

	hidden string ` + "`" + `gorm:"column:hidden"` + "`" + `
}

// Author is the related model referenced by the relation field. | Author 是内嵌示例里被引用的关联模型。
type Author struct {
	ID int64
}

// TableName returns the table name of the article. | TableName 返回文章表名。
func (Post) TableName() string { return "cms_posts" }
`

// writeSource writes source into the in-memory file system. | writeSource 把源码写进内存文件系统。
func writeSource(t *testing.T, path, content string) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
	return fs
}

// TestModelFromSourceReadsStructTags 验证源码解析路径与反射路径得到同样的字段集合。
// TestModelFromSourceReadsStructTags verifies that the source path yields the same field set as the reflection path.
func TestModelFromSourceReadsStructTags(t *testing.T) {
	fs := writeSource(t, "internal/model/post.go", sourcePost)

	m, err := gen.ModelFromSource(fs, "internal/model", "Post", gen.StructOptions{Module: "cms"})
	if err != nil {
		t.Fatalf("ModelFromSource 返回错误: %v", err)
	}
	if m.Name != "Post" || m.GoName != "Post" {
		t.Errorf("Name/GoName = %q/%q，期望 Post/Post", m.Name, m.GoName)
	}
	if m.Table != "cms_posts" {
		t.Errorf("Table = %q，期望 TableName 方法给出的 %q", m.Table, "cms_posts")
	}
	if m.Module != "cms" {
		t.Errorf("Module = %q，期望 %q", m.Module, "cms")
	}
	if !m.Timestamps {
		t.Error("Timestamps must be true after embedding gorm.Model | 内嵌 gorm.Model 后 Timestamps 应为 true")
	}

	id := m.Field("id")
	if id == nil || !id.Primary || id.GoType != "int64" {
		t.Errorf("gorm.Model 的主键未展开: %+v", id)
	}
	if m.Field("created_at") == nil || m.Field("updated_at") == nil || m.Field("deleted_at") == nil {
		t.Errorf("gorm.Model 的时间戳列未展开: %+v", m.Fields)
	}

	title := m.Field("title")
	if title == nil {
		t.Fatalf("title 字段缺失: %+v", m.Fields)
	}
	if title.GoType != "string" || title.Size != 120 || title.Nullable {
		t.Errorf("title 类型/长度/可空 = %q/%d/%v，期望 string/120/false", title.GoType, title.Size, title.Nullable)
	}
	if title.Label != "标题" || title.Validate != "required,max=120" {
		t.Errorf("yd 标签未生效: label=%q validate=%q", title.Label, title.Validate)
	}
	if title.JSONName != "title" {
		t.Errorf("JSONName = %q，期望 %q", title.JSONName, "title")
	}

	if at := m.Field("published_at"); at == nil || at.GoType != "time.Time" {
		t.Errorf("time.Time 字段映射错误: %+v", at)
	}
	if cover := m.Field("cover"); cover == nil || cover.GoType != "string" {
		t.Errorf("指针字段应取元素类型: %+v", cover)
	}
	if raw := m.Field("raw"); raw == nil || raw.GoType != "[]byte" {
		t.Errorf("[]byte 字段映射错误: %+v", raw)
	}
	if extra := m.Field("extra"); extra == nil || extra.Column != "extra" {
		t.Errorf("无 gorm 标签的字段应退回蛇形列名: %+v", extra)
	}

	for _, name := range []string{"Tags", "Meta", "Owner", "hidden"} {
		if f := m.Field(name); f != nil {
			t.Errorf("字段 %s 应被跳过，实际 %+v", name, f)
		}
	}

	t.Run("单文件路径", func(t *testing.T) {
		got, err := gen.ModelFromSource(fs, "internal/model/post.go", "Post", gen.StructOptions{})
		if err != nil {
			t.Fatalf("ModelFromSource 返回错误: %v", err)
		}
		if got.Table != "cms_posts" || len(got.Fields) != len(m.Fields) {
			t.Errorf("文件路径与目录路径结果不一致: table=%q fields=%d/%d", got.Table, len(got.Fields), len(m.Fields))
		}
	})

	t.Run("选项覆盖", func(t *testing.T) {
		no := false
		got, err := gen.ModelFromSource(fs, "internal/model", "Post", gen.StructOptions{
			Name:       "Article",
			Table:      "articles",
			Module:     "cms",
			Timestamps: &no,
		})
		if err != nil {
			t.Fatalf("ModelFromSource 返回错误: %v", err)
		}
		if got.Name != "Article" || got.GoName != "Article" || got.Table != "articles" {
			t.Errorf("选项未被应用: Name=%q GoName=%q Table=%q", got.Name, got.GoName, got.Table)
		}
		if got.Timestamps {
			t.Error("Timestamps must be false once explicitly disabled | Timestamps 被显式关闭后应为 false")
		}
	})
}

// TestModelFromSourceEmbeddedAndAliases 覆盖同包内嵌、类型别名与递归内嵌的收敛。
// TestModelFromSourceEmbeddedAndAliases covers same-package embedding, type aliases and the convergence of recursive embedding.
func TestModelFromSourceEmbeddedAndAliases(t *testing.T) {
	const src = `package model

type Status string

type Base struct {
	Slug   *string ` + "`" + `gorm:"column:slug;uniqueIndex"` + "`" + `
	Status Status  ` + "`" + `gorm:"column:status"` + "`" + `
}

type Node struct {
	Base
	*Leaf
	Label string ` + "`" + `gorm:"column:label"` + "`" + `
}

type Leaf struct {
	Node *Node
	Kind string ` + "`" + `gorm:"column:kind"` + "`" + `
}
`
	fs := writeSource(t, "internal/model/node.go", src)

	m, err := gen.ModelFromSource(fs, "internal/model", "Node", gen.StructOptions{})
	if err != nil {
		t.Fatalf("ModelFromSource 返回错误: %v", err)
	}
	for _, want := range []string{"slug", "status", "kind", "label"} {
		if m.Field(want) == nil {
			t.Errorf("字段 %s 缺失: %+v", want, m.Fields)
		}
	}
	if slug := m.Field("slug"); slug == nil || !slug.Unique || slug.GoType != "string" {
		t.Errorf("uniqueIndex 未解析: %+v", slug)
	}
	if node := m.Field("Node"); node != nil {
		t.Errorf("指针自引用结构体应被跳过: %+v", node)
	}
	if m.Table != "nodes" {
		t.Errorf("Table = %q，期望 %q", m.Table, "nodes")
	}
}

// TestModelFromSourceErrors 覆盖源码解析路径的错误分支。
// TestModelFromSourceErrors covers the error branches of the source path.
func TestModelFromSourceErrors(t *testing.T) {
	fs := writeSource(t, "internal/model/post.go", sourcePost)

	t.Run("类型不存在", func(t *testing.T) {
		_, err := gen.ModelFromSource(fs, "internal/model", "Missing", gen.StructOptions{})
		if !errors.Is(err, gen.ErrTypeNotFound) {
			t.Errorf("错误 = %v，期望 ErrTypeNotFound", err)
		}
	})

	t.Run("不是结构体", func(t *testing.T) {
		_, err := gen.ModelFromSource(fs, "internal/model", "time", gen.StructOptions{})
		if !errors.Is(err, gen.ErrTypeNotFound) {
			t.Errorf("错误 = %v，期望 ErrTypeNotFound", err)
		}
	})

	t.Run("目录不存在", func(t *testing.T) {
		_, err := gen.ModelFromSource(fs, "internal/nope", "Post", gen.StructOptions{})
		if !errors.Is(err, gen.ErrReadFile) {
			t.Errorf("错误 = %v，期望 ErrReadFile", err)
		}
	})

	t.Run("目录下没有源码", func(t *testing.T) {
		empty := afero.NewMemMapFs()
		if err := empty.MkdirAll("internal/empty", 0o755); err != nil {
			t.Fatalf("建目录失败: %v", err)
		}
		_, err := gen.ModelFromSource(empty, "internal/empty", "Post", gen.StructOptions{})
		if !errors.Is(err, gen.ErrTypeNotFound) {
			t.Errorf("错误 = %v，期望 ErrTypeNotFound", err)
		}
	})

	t.Run("源码不合法", func(t *testing.T) {
		broken := writeSource(t, "internal/model/broken.go", "package model\n\ntype Post struct {\n")
		_, err := gen.ModelFromSource(broken, "internal/model", "Post", gen.StructOptions{})
		if !errors.Is(err, gen.ErrBadModel) {
			t.Errorf("错误 = %v，期望 ErrBadModel", err)
		}
	})

	t.Run("非法入参", func(t *testing.T) {
		if _, err := gen.ModelFromSource(nil, "internal/model", "Post", gen.StructOptions{}); !errors.Is(err, gen.ErrNilFS) {
			t.Errorf("空文件系统错误 = %v，期望 ErrNilFS", err)
		}
		if _, err := gen.ModelFromSource(fs, "internal/model", "  ", gen.StructOptions{}); !errors.Is(err, gen.ErrBadModel) {
			t.Errorf("空类型名错误 = %v，期望 ErrBadModel", err)
		}
		if _, err := gen.ModelFromSource(fs, "  ", "Post", gen.StructOptions{}); !errors.Is(err, gen.ErrBadModel) {
			t.Errorf("空路径错误 = %v，期望 ErrBadModel", err)
		}
	})

	t.Run("没有列字段", func(t *testing.T) {
		const src = "package model\n\ntype Empty struct {\n\tID int64 `gorm:\"-\"`\n}\n"
		only := writeSource(t, "internal/model/only.go", src)
		_, err := gen.ModelFromSource(only, "internal/model", "Empty", gen.StructOptions{})
		if !errors.Is(err, gen.ErrNoFields) {
			t.Errorf("错误 = %v，期望 ErrNoFields", err)
		}
	})
}

// TestModelFromSourceGeneratesCompilableModel 端到端：源码 → 模型 → 生成物，确认两条路径能串起来。
// TestModelFromSourceGeneratesCompilableModel goes end to end: source → model → artifacts, confirming the two paths chain together.
func TestModelFromSourceGeneratesCompilableModel(t *testing.T) {
	fs := writeSource(t, "internal/model/post.go", sourcePost)

	m, err := gen.ModelFromSource(fs, "internal/model", "Post", gen.StructOptions{Module: "cms"})
	if err != nil {
		t.Fatalf("ModelFromSource 返回错误: %v", err)
	}
	res, err := gen.Generate(context.Background(), m, gen.Options{Package: "example.com/blog", Dir: "out"}, fs)
	if err != nil {
		t.Fatalf("Generate 返回错误: %v", err)
	}

	model := readFile(t, fs, "out/internal/model/post.go")
	for _, want := range []string{
		gen.GeneratedMarker,
		"func (Post) TableName() string { return \"cms_posts\" }",
		"gorm:\"column:title;size:120;not null\"",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("模型缺少 %q:\n%s", want, model)
		}
	}
	if len(res.Written) != len(wantArtifacts) || len(res.Skipped) != 0 {
		t.Errorf("生成清单 = %d 写入 / %d 跳过，期望 %d / 0", len(res.Written), len(res.Skipped), len(wantArtifacts))
	}
}
