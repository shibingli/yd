// This test only runs when YD_TEST_MYSQL_DSN points at a MySQL account that may create and drop a
// disposable database. It validates the dialect-specific parts of the CMS schema and the behaviours
// SQLite cannot prove: MEDIUMTEXT body columns, the removed oversized category-path index, and MySQL's
// zero-affected-rows semantics for unchanged updates. | 仅当 YD_TEST_MYSQL_DSN 指向一个允许创建/删除
// 临时库的 MySQL 账号时运行。它验证 SQLite 无法证明的方言行为：MEDIUMTEXT 正文列、已移除的超长分类
// 路径索引，以及 MySQL「更新未改变」时零影响行的语义。
package gormstore_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/shibingli/yd/cms"
	"github.com/shibingli/yd/cms/filestore"
	"github.com/shibingli/yd/cms/gormstore"
	"github.com/shibingli/yd/cms/markdown"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/data/gormx"
)

// TestMySQLSchemaAndArticleBehaviours exercises the MySQL-only parts of the CMS store. |
// TestMySQLSchemaAndArticleBehaviours 验证 CMS 存储的 MySQL 专属行为。
func TestMySQLSchemaAndArticleBehaviours(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("YD_TEST_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("YD_TEST_MYSQL_DSN is not set | 未设置 YD_TEST_MYSQL_DSN")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse YD_TEST_MYSQL_DSN: %v", err)
	}
	// A disposable database keeps the schema test away from the caller's data. |
	// 一次性的库让模式测试不触碰调用方的数据。
	name := fmt.Sprintf("yd_cms_it_%d", time.Now().UnixNano())
	cfg.DBName = name
	testDSN := cfg.FormatDSN()

	db, err := gormx.Open(gormx.Options{
		Driver:         "mysql",
		DSN:            testDSN,
		CreateDatabase: true,
		LogLevel:       "silent",
	}, logx.Default())
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
		adminCfg := *cfg
		adminCfg.DBName = ""
		admin, adminErr := sql.Open("mysql", adminCfg.FormatDSN())
		if adminErr != nil {
			return
		}
		defer func() { _ = admin.Close() }()
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+name+"`")
	})

	ctx := context.Background()
	if err := gormstore.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := gormstore.Migrate(ctx, db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	assertTable := func(table, want string) {
		t.Helper()
		var gotTable, create string
		if err := db.Raw("SHOW CREATE TABLE `"+table+"`").Row().Scan(&gotTable, &create); err != nil {
			t.Fatalf("show create %s: %v", table, err)
		}
		if !strings.Contains(strings.ToLower(create), strings.ToLower(want)) {
			t.Fatalf("%s schema is missing %q:\n%s", table, want, create)
		}
	}
	assertTable("yd_cms_articles", "mediumtext")
	assertTable("yd_cms_revisions", "mediumtext")
	var categoryTable string
	var categoryCreate string
	if err := db.Raw("SHOW CREATE TABLE `yd_cms_categories`").Row().Scan(&categoryTable, &categoryCreate); err != nil {
		t.Fatalf("show categories: %v", err)
	}
	if strings.Contains(categoryCreate, "idx_cms_category_path") {
		t.Fatalf("oversized category path index is still present:\n%s", categoryCreate)
	}
	// Relations are maintained by application code; the schema must contain no foreign keys. |
	// 关系由应用代码维护，模式中不得出现任何外键。
	var foreignKeys int64
	if err := db.Raw(`SELECT COUNT(*) FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE() AND REFERENCED_TABLE_NAME IS NOT NULL`).Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("count foreign keys: %v", err)
	}
	if foreignKeys != 0 {
		t.Fatalf("schema declares %d foreign key column(s); relations must be maintained in application code", foreignKeys)
	}

	blobs, err := filestore.New(t.TempDir())
	if err != nil {
		t.Fatalf("filestore: %v", err)
	}
	store, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	svc, err := cms.New(store, markdown.New(), blobs, logx.Default(), cms.DefaultOptions())
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	if err := svc.EnsureTenant(ctx, "tn_mysql"); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	actor := cms.Actor{ID: "usr_mysql", TenantID: "tn_mysql", DisplayName: "MySQL", Manage: true, Publish: true, Review: true}

	category, err := svc.CreateCategory(ctx, actor, cms.CategoryInput{Name: "News", Slug: "news", Visible: true})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	if _, err := svc.CreateCategory(ctx, actor, cms.CategoryInput{Name: "News Again", Slug: "news", Visible: true}); !errors.Is(err, cms.ErrCategorySlugTaken) {
		t.Fatalf("duplicate category error = %v, want ErrCategorySlugTaken", err)
	}
	// Re-saving the same values must succeed even though MySQL reports zero affected rows. |
	// 原样保存必须成功，尽管 MySQL 报告零影响行。
	if _, err := svc.UpdateCategory(ctx, actor, category.ID, cms.CategoryInput{Name: "News", Slug: "news", Visible: true}); err != nil {
		t.Fatalf("no-op category update: %v", err)
	}
	child, err := svc.CreateCategory(ctx, actor, cms.CategoryInput{
		Name: "Sub", Slug: "sub", ParentID: category.ID, Visible: true,
	})
	if err != nil {
		t.Fatalf("create child category: %v", err)
	}
	grand, err := svc.CreateCategory(ctx, actor, cms.CategoryInput{
		Name: "Grand", Slug: "grand", ParentID: child.ID, Visible: true,
	})
	if err != nil {
		t.Fatalf("create grandchild category: %v", err)
	}
	moved, err := svc.UpdateCategory(ctx, actor, child.ID, cms.CategoryInput{Name: "Sub", Slug: "sub", Visible: true})
	if err != nil {
		t.Fatalf("move category to root: %v", err)
	}
	if moved.Depth != 0 || moved.Path != "/"+child.ID+"/" {
		t.Fatalf("moved category = depth %d path %q", moved.Depth, moved.Path)
	}
	movedGrand, err := svc.Category(ctx, "tn_mysql", grand.ID)
	if err != nil {
		t.Fatalf("reload moved grandchild: %v", err)
	}
	if movedGrand.Depth != 1 || movedGrand.Path != moved.Path+grand.ID+"/" {
		t.Fatalf("moved grandchild = depth %d path %q, want under %q", movedGrand.Depth, movedGrand.Path, moved.Path)
	}

	tag, err := svc.CreateTag(ctx, actor, cms.TagInput{Name: "Go", Slug: "go"})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	if _, err := svc.CreateTag(ctx, actor, cms.TagInput{Name: "Go Again", Slug: "go"}); !errors.Is(err, cms.ErrTagSlugTaken) {
		t.Fatalf("duplicate tag error = %v, want ErrTagSlugTaken", err)
	}
	tags, err := svc.Tags(ctx, "tn_mysql", "go")
	if err != nil || len(tags) != 1 || tags[0].ID != tag.ID {
		t.Fatalf("case-insensitive tag search = %+v, %v", tags, err)
	}

	// 100 KiB exceeds MySQL TEXT but fits the documented 512 KiB CMS ceiling. |
	// 100 KiB 超过 MySQL TEXT，但在文档规定的 512 KiB CMS 上限之内。
	body := strings.Repeat("MySQL body ", 10000)
	article, err := svc.SaveArticle(ctx, actor, cms.ArticleInput{
		CategoryID: category.ID, Title: "Large MySQL Body", BodyMarkdown: body, Kind: cms.KindPost,
		Visibility: cms.VisibilityPublic, TagIDs: []string{tag.ID},
	})
	if err != nil {
		t.Fatalf("save large body: %v", err)
	}
	loaded, err := svc.Article(ctx, "tn_mysql", article.ID, false)
	if err != nil {
		t.Fatalf("reload large body: %v", err)
	}
	if len(loaded.BodyMarkdown) != len(body) {
		t.Fatalf("body length = %d, want %d", len(loaded.BodyMarkdown), len(body))
	}
	// A restart reruns AutoMigrate before ensureMySQLLongText; the declared column type must already be
	// mediumtext, otherwise the shrink fails or truncates the stored 100 KiB body. |
	// 重启会在 ensureMySQLLongText 之前重跑 AutoMigrate；声明类型必须已经是 mediumtext，否则收缩会失败
	// 或截断已存的 100 KiB 正文。
	if err := gormstore.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate after large body: %v", err)
	}
	assertTable("yd_cms_articles", "mediumtext")
	var storedBody string
	if err := db.Table("yd_cms_articles").Select("body_markdown").
		Where("tenant_id = ? AND id = ?", "tn_mysql", article.ID).Row().Scan(&storedBody); err != nil {
		t.Fatalf("read stored body: %v", err)
	}
	if len(storedBody) != len(body) {
		t.Fatalf("stored body after restart = %d bytes, want %d", len(storedBody), len(body))
	}
	if _, err := svc.SaveArticle(ctx, actor, cms.ArticleInput{
		CategoryID: category.ID, Title: "Large MySQL Body", BodyMarkdown: "other", Kind: cms.KindPost, Visibility: cms.VisibilityPublic,
	}); !errors.Is(err, cms.ErrArticleSlugTaken) {
		t.Fatalf("duplicate article error = %v, want ErrArticleSlugTaken", err)
	}
	if err := svc.DeleteTag(ctx, actor, tag.ID); !errors.Is(err, cms.ErrTagInUse) {
		t.Fatalf("delete used tag error = %v, want ErrTagInUse", err)
	}
	if err := svc.DeleteCategory(ctx, actor, category.ID); !errors.Is(err, cms.ErrCategoryNotEmpty) {
		t.Fatalf("delete non-empty category error = %v, want ErrCategoryNotEmpty", err)
	}
}
