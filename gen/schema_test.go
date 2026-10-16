package gen_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/shibingli/yd/gen"
)

// openSQLite 打开一个测试专用的内存库；DSN 以测试名唯一化，避免用例之间互相污染。
// openSQLite opens a test-only in-memory database; the DSN is unique per test so cases cannot pollute each other.
func openSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:gen_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开 sqlite 内存库失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取 *sql.DB 失败: %v", err)
	}
	// A single connection keeps the shared-cache memory database alive. | 单连接可保证共享缓存内存库不被回收。
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// TestModelFromSQLiteTable 用真实建表结果验证表 → 模型反射（字段/主键/可空/长度）。
// TestModelFromSQLiteTable verifies table → model reflection (columns, primary key, nullability, length) against a real table.
func TestModelFromSQLiteTable(t *testing.T) {
	db := openSQLite(t)
	const ddl = `CREATE TABLE posts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title VARCHAR(120) NOT NULL,
		body TEXT,
		views INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME,
		updated_at DATETIME
	)`
	if err := db.Exec(ddl).Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	m, err := gen.ModelFromTable(context.Background(), db, "posts", gen.TableOptions{Module: "cms"})
	if err != nil {
		t.Fatalf("ModelFromTable 返回错误: %v", err)
	}
	if m.GoName != "Post" {
		t.Errorf("GoName = %q，期望 %q", m.GoName, "Post")
	}
	if m.Table != "posts" {
		t.Errorf("Table = %q，期望 %q", m.Table, "posts")
	}
	if m.Module != "cms" {
		t.Errorf("Module = %q，期望 %q", m.Module, "cms")
	}
	if !m.Timestamps {
		t.Error("存在 created_at/updated_at，Timestamps 应为 true")
	}

	id := m.Field("id")
	if id == nil {
		t.Fatalf("id 字段缺失: %+v", m.Fields)
	}
	if !id.Primary {
		t.Errorf("id 应为 primary key: %+v", id)
	}
	if id.Nullable {
		t.Errorf("主键列不应为 nullable: %+v", id)
	}
	if id.GoType != "int64" {
		t.Errorf("id.GoType = %q，期望 %q", id.GoType, "int64")
	}

	title := m.Field("title")
	if title == nil {
		t.Fatalf("title 字段缺失: %+v", m.Fields)
	}
	if title.GoType != "string" || title.Size != 120 {
		t.Errorf("title 类型/长度 = %q/%d，期望 string/120", title.GoType, title.Size)
	}
	if title.Nullable {
		t.Errorf("title 带 NOT NULL，Nullable 应为 false: %+v", title)
	}

	body := m.Field("body")
	if body == nil {
		t.Fatalf("body 字段缺失: %+v", m.Fields)
	}
	if !body.Nullable {
		t.Errorf("body 无 NOT NULL 约束，Nullable 应为 true: %+v", body)
	}

	views := m.Field("views")
	if views == nil {
		t.Fatalf("views 字段缺失: %+v", m.Fields)
	}
	if views.GoType != "int64" || views.Nullable {
		t.Errorf("views 类型/可空 = %q/%v，期望 int64/false", views.GoType, views.Nullable)
	}

	t.Run("表不存在", func(t *testing.T) {
		_, err := gen.ModelFromTable(context.Background(), db, "no_such_table", gen.TableOptions{})
		if !errors.Is(err, gen.ErrTableNotFound) {
			t.Errorf("错误 = %v，期望 ErrTableNotFound", err)
		}
	})

	t.Run("缺少数据库句柄", func(t *testing.T) {
		_, err := gen.ModelFromTable(context.Background(), nil, "posts", gen.TableOptions{})
		if !errors.Is(err, gen.ErrNilDB) {
			t.Errorf("错误 = %v，期望 ErrNilDB", err)
		}
	})
}

// TestModelFromFields 验证 `yd gen module --fields` 走的构造路径：名字推导、选项覆盖与非法输入。
// TestModelFromFields verifies the construction path taken by `yd gen module --fields`: name derivation, option overrides and invalid input.
func TestModelFromFields(t *testing.T) {
	fields, err := gen.ParseFields("title:string:required,max=120,views:int:min=0,cover:file")
	if err != nil {
		t.Fatalf("ParseFields 返回错误: %v", err)
	}

	m, err := gen.ModelFromFields("blog_post", fields, gen.ModelOptions{Module: "cms"})
	if err != nil {
		t.Fatalf("ModelFromFields 返回错误: %v", err)
	}
	if m.Name != "BlogPost" || m.GoName != "BlogPost" {
		t.Errorf("Name/GoName = %q/%q，期望 BlogPost/BlogPost", m.Name, m.GoName)
	}
	if m.Table != "blog_posts" {
		t.Errorf("Table = %q，期望 %q", m.Table, "blog_posts")
	}
	if m.Module != "cms" {
		t.Errorf("Module = %q，期望 %q", m.Module, "cms")
	}
	if !m.Timestamps {
		t.Error("未显式关闭时 Timestamps 应为 true")
	}
	if len(m.Fields) != 4 {
		t.Fatalf("字段数 = %d，期望自动补主键后的 4 个: %+v", len(m.Fields), m.Fields)
	}
	if id := m.Field("id"); id == nil || !id.Primary {
		t.Errorf("缺少自动补齐的主键: %+v", m.Fields)
	}
	if cover := m.Field("cover"); cover == nil || cover.Widget != "file" {
		t.Errorf("cover 字段解析错误: %+v", cover)
	}

	t.Run("选项覆盖", func(t *testing.T) {
		no := false
		got, err := gen.ModelFromFields("blog_post", fields, gen.ModelOptions{
			Name:       "Article",
			Table:      "cms_articles",
			Module:     "cms",
			Timestamps: &no,
		})
		if err != nil {
			t.Fatalf("ModelFromFields 返回错误: %v", err)
		}
		if got.Name != "Article" || got.GoName != "Article" || got.Table != "cms_articles" {
			t.Errorf("选项未被应用: Name=%q GoName=%q Table=%q", got.Name, got.GoName, got.Table)
		}
		if got.Timestamps {
			t.Error("Timestamps 被显式关闭后应为 false")
		}
	})

	t.Run("非法输入", func(t *testing.T) {
		if _, err := gen.ModelFromFields("  ", fields, gen.ModelOptions{}); !errors.Is(err, gen.ErrBadModel) {
			t.Errorf("空名字错误 = %v，期望 ErrBadModel", err)
		}
		if _, err := gen.ModelFromFields("post", nil, gen.ModelOptions{}); !errors.Is(err, gen.ErrNoFields) {
			t.Errorf("空字段错误 = %v，期望 ErrNoFields", err)
		}
	})
}
