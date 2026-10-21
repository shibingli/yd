package gormstore_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shibingli/yd/cms"
	"github.com/shibingli/yd/cms/gormstore"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newCategoryStore opens an isolated SQLite store for category lock tests. |
// newCategoryStore 为分类锁测试打开一个隔离的 SQLite 存储。
func newCategoryStore(t *testing.T) *gormstore.Store {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dsn := "file:" + strings.ToLower(name) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := gormstore.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return store
}

func categoryRow(id, parentID, path string, depth int) cms.Category {
	return cms.Category{
		ID: id, TenantID: "tn_lock", ParentID: parentID, Name: id, Slug: id,
		Path: path, Depth: depth, Visible: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
}

// TestMoveCategoryReReadsDestination proves the store rejects a destination inside the moved subtree
// after a concurrent move, even when the service snapshot predates it. |
// TestMoveCategoryReReadsDestination 证明存储层在并发移动后仍拒绝把分类移入自身子树，即使服务快照已经过期。
func TestMoveCategoryReReadsDestination(t *testing.T) {
	store := newCategoryStore(t)
	ctx := context.Background()
	a := categoryRow("cat_a", "", "/cat_a/", 0)
	b := categoryRow("cat_b", "", "/cat_b/", 0)
	for _, category := range []cms.Category{a, b} {
		if err := store.CreateCategory(ctx, "tn_lock", category); err != nil {
			t.Fatalf("create %s: %v", category.ID, err)
		}
	}
	b.ParentID = a.ID
	b.Path = a.Path + b.ID + "/"
	b.Depth = 1
	if err := store.MoveCategory(ctx, "tn_lock", b, "/cat_b/"); err != nil {
		t.Fatalf("move B under A: %v", err)
	}
	// The service computed A's move against the pre-move tree; the store must see B is now a child. |
	// 服务层基于移动前的树计算 A 的移动；存储层必须看到 B 现在已是 A 的子节点。
	a.ParentID = b.ID
	a.Path = b.Path + a.ID + "/"
	a.Depth = 2
	if err := store.MoveCategory(ctx, "tn_lock", a, "/cat_a/"); !errors.Is(err, cms.ErrCategoryCycle) {
		t.Fatalf("reciprocal move error = %v, want ErrCategoryCycle", err)
	}
	if err := store.MoveCategory(ctx, "tn_lock", b, "/cat_b/"); !errors.Is(err, cms.ErrCategoryConflict) {
		t.Fatalf("stale root move error = %v, want ErrCategoryConflict", err)
	}
}

// TestUpdateCategoryDoesNotOverwriteMove proves a stale edit cannot revert parent/path/depth written
// by a concurrent move. | TestUpdateCategoryDoesNotOverwriteMove 证明过期编辑无法回退并发移动写入的
// 父级、路径与深度。
func TestUpdateCategoryDoesNotOverwriteMove(t *testing.T) {
	store := newCategoryStore(t)
	ctx := context.Background()
	a := categoryRow("cat_a", "", "/cat_a/", 0)
	b := categoryRow("cat_b", "", "/cat_b/", 0)
	for _, category := range []cms.Category{a, b} {
		if err := store.CreateCategory(ctx, "tn_lock", category); err != nil {
			t.Fatalf("create %s: %v", category.ID, err)
		}
	}
	stale := a
	a.ParentID = b.ID
	a.Path = b.Path + a.ID + "/"
	a.Depth = 1
	if err := store.MoveCategory(ctx, "tn_lock", a, "/cat_a/"); err != nil {
		t.Fatalf("move A under B: %v", err)
	}
	stale.Name = "renamed"
	if err := store.UpdateCategory(ctx, "tn_lock", stale); err != nil {
		t.Fatalf("rename after move: %v", err)
	}
	loaded, err := store.GetCategory(ctx, "tn_lock", a.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.Name != "renamed" || loaded.Path != a.Path || loaded.ParentID != b.ID || loaded.Depth != 1 {
		t.Fatalf("stale edit overwrote the move: %+v", loaded)
	}
}

// TestMoveCategoryRejectsSubtreeDepthOverflow proves the depth guard covers descendants, not only
// the moved node. | TestMoveCategoryRejectsSubtreeDepthOverflow 证明深度守卫覆盖后代，而不仅是移动节点本身。
func TestMoveCategoryRejectsSubtreeDepthOverflow(t *testing.T) {
	store := newCategoryStore(t)
	ctx := context.Background()
	build := func(prefix string, count int) (cms.Category, cms.Category) {
		var root, leaf cms.Category
		parent := ""
		for i := 0; i < count; i++ {
			category := categoryRow(prefix+"_"+strconv.Itoa(i), parent, "", 0)
			if err := store.CreateCategory(ctx, "tn_lock", category); err != nil {
				t.Fatalf("create %s: %v", category.ID, err)
			}
			created, err := store.GetCategory(ctx, "tn_lock", category.ID)
			if err != nil {
				t.Fatalf("reload %s: %v", category.ID, err)
			}
			if i == 0 {
				root = created
			}
			leaf = created
			parent = created.ID
		}
		return root, leaf
	}
	_, deepLeaf := build("cat_deep", cms.MaxCategoryDepth)
	shallowRoot, _ := build("cat_shallow", 41)
	move := shallowRoot
	move.ParentID = deepLeaf.ID
	if err := store.MoveCategory(ctx, "tn_lock", move, shallowRoot.Path); !errors.Is(err, cms.ErrCategoryParent) {
		t.Fatalf("subtree depth overflow error = %v, want ErrCategoryParent", err)
	}
	unchanged, err := store.GetCategory(ctx, "tn_lock", shallowRoot.ID)
	if err != nil {
		t.Fatalf("reload shallow root: %v", err)
	}
	if unchanged.Path != shallowRoot.Path || unchanged.ParentID != "" {
		t.Fatalf("failed move left partial state: %+v", unchanged)
	}
}

// TestPruneRevisionsUsesVersionCutoff proves pruning keeps the newest versions and never deletes a
// newer revision that appeared after the selection. | TestPruneRevisionsUsesVersionCutoff 证明裁剪保留最新
// 版本，并且绝不删除选择之后出现的更新修订。
func TestPruneRevisionsUsesVersionCutoff(t *testing.T) {
	store := newCategoryStore(t)
	ctx := context.Background()
	for version := 1; version <= 5; version++ {
		if err := store.CreateRevision(ctx, "tn_lock", cms.Revision{
			ID: "rev_" + strconv.Itoa(version), TenantID: "tn_lock", ArticleID: "art_1",
			Title: "v", BodyMarkdown: "body", Version: version, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create revision %d: %v", version, err)
		}
	}
	if err := store.PruneRevisions(ctx, "tn_lock", "art_1", 2); err != nil {
		t.Fatalf("prune: %v", err)
	}
	revisions, err := store.ListRevisions(ctx, "tn_lock", "art_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(revisions) != 2 || revisions[0].Version != 5 || revisions[1].Version != 4 {
		t.Fatalf("remaining revisions = %+v, want versions 5 and 4", revisions)
	}
}

// TestCreateCategoryRejectsMissingParent proves the store re-checks the parent inside the insert
// transaction. | TestCreateCategoryRejectsMissingParent 证明存储层在插入事务内重新校验父分类。
func TestCreateCategoryRejectsMissingParent(t *testing.T) {
	store := newCategoryStore(t)
	if err := store.CreateCategory(context.Background(), "tn_lock",
		categoryRow("cat_child", "cat_missing", "/cat_missing/cat_child/", 1)); !errors.Is(err, cms.ErrCategoryParent) {
		t.Fatalf("missing parent error = %v, want ErrCategoryParent", err)
	}
}
