package cms_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shibingli/yd/cms"
	"github.com/shibingli/yd/cms/filestore"
	"github.com/shibingli/yd/cms/gormstore"
	"github.com/shibingli/yd/cms/markdown"
	"github.com/shibingli/yd/core/logx"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fixture struct {
	t     *testing.T
	ctx   context.Context
	db    *gorm.DB
	store *gormstore.Store
	svc   *cms.Service
	blobs *filestore.Store
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_", "#", "_").Replace(t.Name())
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
	if err := gormstore.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate twice: %v", err)
	}
	store, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	blobs, err := filestore.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}
	f := &fixture{t: t, ctx: context.Background(), db: db, store: store, blobs: blobs,
		now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	options := cms.DefaultOptions()
	options.Now = func() time.Time { return f.now }
	options.PageSize = 10
	options.MaxPageSize = 50
	svc, err := cms.New(store, markdown.New(), blobs, logx.Default(), options)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	f.svc = svc
	if err := svc.EnsureTenant(f.ctx, tenantA); err != nil {
		t.Fatalf("ensure tenant A: %v", err)
	}
	if err := svc.EnsureTenant(f.ctx, tenantB); err != nil {
		t.Fatalf("ensure tenant B: %v", err)
	}
	return f
}

const (
	tenantA = "tn_alpha"
	tenantB = "tn_beta"
)

func admin() cms.Actor {
	return cms.Actor{ID: "usr_admin", TenantID: tenantA, DisplayName: "Admin",
		Manage: true, Publish: true, Review: true, Moderate: true, MediaManage: true, SiteManage: true}
}

func member() cms.Actor {
	return cms.Actor{ID: "usr_member", TenantID: tenantA, DisplayName: "Member"}
}

func adminOf(tenant string) cms.Actor {
	actor := admin()
	actor.TenantID = tenant
	return actor
}

func defaultCategory(t *testing.T, f *fixture, tenantID string) cms.Category {
	t.Helper()
	category, err := f.svc.CategoryBySlug(f.ctx, tenantID, "uncategorized")
	if err != nil {
		t.Fatalf("default category: %v", err)
	}
	return category
}

func saveArticle(t *testing.T, f *fixture, actor cms.Actor, title, body string, tags ...string) cms.Article {
	t.Helper()
	category := defaultCategory(t, f, actor.TenantID)
	article, err := f.svc.SaveArticle(f.ctx, actor, cms.ArticleInput{
		CategoryID:     category.ID,
		Title:          title,
		BodyMarkdown:   body,
		Kind:           cms.KindPost,
		Visibility:     cms.VisibilityPublic,
		AllowComment:   true,
		TagNames:       tags,
		SEODescription: title,
	})
	if err != nil {
		t.Fatalf("save article %q: %v", title, err)
	}
	return article
}

func TestEnsureTenantIsIdempotent(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.EnsureTenant(f.ctx, tenantA); err != nil {
		t.Fatalf("ensure tenant again: %v", err)
	}
	categories, err := f.store.ListCategories(f.ctx, tenantA)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(categories) != 1 {
		t.Fatalf("default category count = %d, want 1", len(categories))
	}
	if _, err := f.svc.Settings(f.ctx, tenantA); err != nil {
		t.Fatalf("settings: %v", err)
	}
}

func TestCategoryTreeMoveAndDelete(t *testing.T) {
	f := newFixture(t)
	rootA, err := f.svc.CreateCategory(f.ctx, admin(), cms.CategoryInput{Name: "Root A", Slug: "root-a", Visible: true})
	if err != nil {
		t.Fatalf("create root A: %v", err)
	}
	child, err := f.svc.CreateCategory(f.ctx, admin(), cms.CategoryInput{Name: "Child", Slug: "child", ParentID: rootA.ID, Visible: true})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	grand, err := f.svc.CreateCategory(f.ctx, admin(), cms.CategoryInput{Name: "Grand", Slug: "grand", ParentID: child.ID, Visible: true})
	if err != nil {
		t.Fatalf("create grandchild: %v", err)
	}
	rootD, err := f.svc.CreateCategory(f.ctx, admin(), cms.CategoryInput{Name: "Root D", Slug: "root-d", Visible: true})
	if err != nil {
		t.Fatalf("create root D: %v", err)
	}
	tree, err := f.svc.CategoryTree(f.ctx, tenantA, true)
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if len(tree) < 3 {
		t.Fatalf("root count = %d, want at least 3 (default, A, D)", len(tree))
	}

	moved, err := f.svc.UpdateCategory(f.ctx, admin(), child.ID, cms.CategoryInput{
		Name: "Child", Slug: "child", ParentID: rootD.ID, Visible: true,
	})
	if err != nil {
		t.Fatalf("move child: %v", err)
	}
	if moved.Depth != 1 || moved.Path != rootD.Path+child.ID+"/" {
		t.Fatalf("moved child depth=%d path=%q, want depth 1 path %q", moved.Depth, moved.Path, rootD.Path+child.ID+"/")
	}
	grandAfter, err := f.svc.Category(f.ctx, tenantA, grand.ID)
	if err != nil {
		t.Fatalf("reload grandchild: %v", err)
	}
	if grandAfter.Depth != 2 || !strings.HasPrefix(grandAfter.Path, moved.Path) {
		t.Fatalf("grandchild depth=%d path=%q, want depth 2 under %q", grandAfter.Depth, grandAfter.Path, moved.Path)
	}

	if _, err := f.svc.UpdateCategory(f.ctx, admin(), rootD.ID, cms.CategoryInput{
		Name: "Root D", Slug: "root-d", ParentID: grand.ID, Visible: true,
	}); !errors.Is(err, cms.ErrCategoryCycle) {
		t.Fatalf("move root below descendant error = %v, want ErrCategoryCycle", err)
	}
	if err := f.svc.DeleteCategory(f.ctx, admin(), child.ID); !errors.Is(err, cms.ErrCategoryNotEmpty) {
		t.Fatalf("delete non-empty category error = %v, want ErrCategoryNotEmpty", err)
	}
	if err := f.svc.DeleteCategory(f.ctx, admin(), grand.ID); err != nil {
		t.Fatalf("delete grandchild: %v", err)
	}
	if err := f.svc.DeleteCategory(f.ctx, admin(), child.ID); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	options, err := f.svc.CategoryOptions(f.ctx, tenantA, rootD.ID)
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	for _, option := range options {
		if option.ID == rootD.ID {
			t.Fatalf("excluded category %q is still selectable", rootD.ID)
		}
	}
}

// TestCategoryDepthIsBounded prevents a materialised path from outgrowing its column. |
// TestCategoryDepthIsBounded 防止物化路径超出所在列的宽度。
func TestCategoryDepthIsBounded(t *testing.T) {
	f := newFixture(t)
	parentID := ""
	for depth := 0; depth <= cms.MaxCategoryDepth; depth++ {
		category, err := f.svc.CreateCategory(f.ctx, admin(), cms.CategoryInput{
			Name: "Depth", Slug: "depth-" + strconv.Itoa(depth), ParentID: parentID, Visible: true,
		})
		if err != nil {
			t.Fatalf("create depth %d: %v", depth, err)
		}
		parentID = category.ID
	}
	if _, err := f.svc.CreateCategory(f.ctx, admin(), cms.CategoryInput{
		Name: "Too Deep", Slug: "too-deep", ParentID: parentID, Visible: true,
	}); !errors.Is(err, cms.ErrCategoryParent) {
		t.Fatalf("depth %d error = %v, want ErrCategoryParent", cms.MaxCategoryDepth, err)
	}
}

func TestArticleLifecycleOwnershipAndSearch(t *testing.T) {
	f := newFixture(t)
	article := saveArticle(t, f, member(), "Hello World", "# Hi\n\nThis is **bold** and searchable.", "Go", "Web")
	if article.Status != cms.ArticleDraft {
		t.Fatalf("new status = %q, want draft", article.Status)
	}
	public, err := f.svc.Articles(f.ctx, tenantA, cms.ArticleFilter{PublicOnly: true})
	if err != nil {
		t.Fatalf("public list: %v", err)
	}
	if public.Total != 0 {
		t.Fatalf("draft leaked into public list: %d", public.Total)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, member(), article.ID, cms.ActionPublish, nil); !errors.Is(err, cms.ErrForbidden) {
		t.Fatalf("member publish error = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionPublish, nil); err != nil {
		t.Fatalf("admin publish: %v", err)
	}
	public, err = f.svc.Articles(f.ctx, tenantA, cms.ArticleFilter{PublicOnly: true})
	if err != nil {
		t.Fatalf("public list: %v", err)
	}
	if public.Total != 1 {
		t.Fatalf("published article count = %d, want 1", public.Total)
	}
	found, err := f.svc.Articles(f.ctx, tenantA, cms.ArticleFilter{Keyword: "bold"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if found.Total != 1 {
		t.Fatalf("body search count = %d, want 1", found.Total)
	}
	found, err = f.svc.Articles(f.ctx, tenantA, cms.ArticleFilter{Keyword: "web"})
	if err != nil {
		t.Fatalf("tag search: %v", err)
	}
	if found.Total != 1 {
		t.Fatalf("tag search count = %d, want 1", found.Total)
	}

	other := member()
	other.ID = "usr_other"
	if _, err := f.svc.SaveArticle(f.ctx, other, cms.ArticleInput{ID: article.ID, Title: "Hijack", BodyMarkdown: "x"}); !errors.Is(err, cms.ErrNotAuthor) {
		t.Fatalf("other author edit error = %v, want ErrNotAuthor", err)
	}
	updated, err := f.svc.SaveArticle(f.ctx, admin(), cms.ArticleInput{
		ID: article.ID, CategoryID: article.CategoryID, Title: "Hello World", BodyMarkdown: "# Hi\n\nChanged body.", AllowComment: true,
	})
	if err != nil {
		t.Fatalf("admin update: %v", err)
	}
	if updated.Version != article.Version+1 {
		t.Fatalf("version = %d, want %d", updated.Version, article.Version+1)
	}
	revisions, err := f.svc.Revisions(f.ctx, tenantA, article.ID)
	if err != nil {
		t.Fatalf("revisions: %v", err)
	}
	if len(revisions) != 1 || !strings.Contains(revisions[0].BodyMarkdown, "searchable") {
		t.Fatalf("revision snapshot = %#v, want previous body", revisions)
	}
	if _, err := f.svc.RestoreRevision(f.ctx, admin(), article.ID, revisions[0].ID); err != nil {
		t.Fatalf("restore revision: %v", err)
	}
	restored, err := f.svc.Article(f.ctx, tenantA, article.ID, false)
	if err != nil {
		t.Fatalf("reload article: %v", err)
	}
	if !strings.Contains(restored.BodyMarkdown, "searchable") {
		t.Fatalf("restored body = %q", restored.BodyMarkdown)
	}

	scheduled := saveArticle(t, f, member(), "Future Post", "later", "")
	future := f.now.Add(24 * time.Hour)
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), scheduled.ID, cms.ActionPublish, &future); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	public, err = f.svc.Articles(f.ctx, tenantA, cms.ArticleFilter{PublicOnly: true})
	if err != nil {
		t.Fatalf("public list: %v", err)
	}
	if public.Total != 1 {
		t.Fatalf("scheduled article leaked publicly: %d", public.Total)
	}
	f.now = future.Add(time.Minute)
	public, err = f.svc.Articles(f.ctx, tenantA, cms.ArticleFilter{PublicOnly: true})
	if err != nil {
		t.Fatalf("public list after schedule: %v", err)
	}
	if public.Total != 2 {
		t.Fatalf("published article count after schedule = %d, want 2", public.Total)
	}

	if _, err := f.svc.TransitionArticle(f.ctx, admin(), scheduled.ID, cms.ActionUnpublish, nil); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), scheduled.ID, cms.ActionArchive, nil); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), scheduled.ID, cms.ActionRestore, nil); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

func TestArticleContentWritesKeepStatusAndViews(t *testing.T) {
	f := newFixture(t)
	article := saveArticle(t, f, member(), "Concurrent", "first")
	stale := article
	if _, err := f.svc.SaveArticle(f.ctx, member(), cms.ArticleInput{
		ID: article.ID, Title: "Concurrent", BodyMarkdown: "second", AllowComment: true,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionPublish, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := f.svc.IncrementView(f.ctx, tenantA, article.ID); err != nil {
		t.Fatalf("increment view: %v", err)
	}
	// 陈旧的内容写入必须以冲突失败，而不是把刚发布的状态改回草稿。 |
	// A stale content write must fail with a conflict instead of reverting the just-published status.
	if err := f.store.UpdateArticleContent(f.ctx, tenantA, stale, stale.Version); !errors.Is(err, cms.ErrArticleConflict) {
		t.Fatalf("stale content write error = %v, want ErrArticleConflict", err)
	}
	// The conflict probe must reuse the transaction handle, not open a second pool connection. |
	// 冲突探测必须复用事务句柄，而不是另开一条连接池连接。
	deadlineCtx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	started := time.Now()
	if err := f.store.UpdateArticleContentWithTags(deadlineCtx, tenantA, stale, stale.Version, nil); !errors.Is(err, cms.ErrArticleConflict) {
		t.Fatalf("stale transactional write error = %v, want ErrArticleConflict", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stale transactional write took %v; it likely waited on its own pool connection", elapsed)
	}
	current, err := f.svc.Article(f.ctx, tenantA, article.ID, false)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if current.Status != cms.ArticlePublished {
		t.Fatalf("status = %q, want published", current.Status)
	}
	if current.ViewCount != 1 {
		t.Fatalf("view count = %d, want 1", current.ViewCount)
	}
}

func TestDeleteRewritesSlugAndHidesArticle(t *testing.T) {
	f := newFixture(t)
	article := saveArticle(t, f, admin(), "Delete Me", "body")
	if err := f.svc.DeleteArticle(f.ctx, admin(), article.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.svc.Article(f.ctx, tenantA, article.ID, false); !errors.Is(err, cms.ErrArticleNotFound) {
		t.Fatalf("deleted article error = %v, want ErrArticleNotFound", err)
	}
	replacement := saveArticle(t, f, admin(), "Delete Me", "replacement")
	if replacement.ID == article.ID {
		t.Fatalf("replacement reused the deleted id")
	}
}

func TestPublicCategoryOptionsHideDraftsAndHiddenSubtrees(t *testing.T) {
	f := newFixture(t)
	parent, err := f.svc.CreateCategory(f.ctx, admin(), cms.CategoryInput{Name: "Parent", Slug: "parent", Visible: true})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := f.svc.CreateCategory(f.ctx, admin(), cms.CategoryInput{Name: "Child", Slug: "child", ParentID: parent.ID, Visible: true})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	published, err := f.svc.SaveArticle(f.ctx, admin(), cms.ArticleInput{
		CategoryID: child.ID, Title: "Public", BodyMarkdown: "body", Kind: cms.KindPost, Visibility: cms.VisibilityPublic,
	})
	if err != nil {
		t.Fatalf("save article: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), published.ID, cms.ActionPublish, nil); err != nil {
		t.Fatalf("publish article: %v", err)
	}
	// 一篇草稿不能抬高公开计数。 | A draft must not raise the public count.
	if _, err := f.svc.SaveArticle(f.ctx, admin(), cms.ArticleInput{
		CategoryID: child.ID, Title: "Draft", BodyMarkdown: "draft", Kind: cms.KindPost, Visibility: cms.VisibilityPublic,
	}); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	options, err := f.svc.PublicCategoryOptions(f.ctx, tenantA)
	if err != nil {
		t.Fatalf("public options: %v", err)
	}
	found := map[string]int64{}
	for _, option := range options {
		found[option.ID] = option.ArticleCount
	}
	if found[child.ID] != 1 {
		t.Fatalf("public child count = %d, want 1 (options=%+v)", found[child.ID], options)
	}
	// 隐藏父分类必须连带隐藏整棵子树。 | Hiding the parent must hide the whole subtree.
	if _, err := f.svc.UpdateCategory(f.ctx, admin(), parent.ID, cms.CategoryInput{Name: "Parent", Slug: "parent", Visible: false}); err != nil {
		t.Fatalf("hide parent: %v", err)
	}
	options, err = f.svc.PublicCategoryOptions(f.ctx, tenantA)
	if err != nil {
		t.Fatalf("public options after hide: %v", err)
	}
	for _, option := range options {
		if option.ID == parent.ID || option.ID == child.ID {
			t.Fatalf("hidden subtree leaked into public options: %+v", options)
		}
	}
}

func TestCrossTenantIsolation(t *testing.T) {
	f := newFixture(t)
	article := saveArticle(t, f, member(), "Tenant A Only", "secret")
	if _, err := f.svc.Article(f.ctx, tenantB, article.ID, false); !errors.Is(err, cms.ErrArticleNotFound) {
		t.Fatalf("cross-tenant article error = %v, want ErrArticleNotFound", err)
	}
	if _, err := f.svc.Category(f.ctx, tenantB, article.CategoryID); !errors.Is(err, cms.ErrCategoryNotFound) {
		t.Fatalf("cross-tenant category error = %v, want ErrCategoryNotFound", err)
	}
	page, err := f.svc.Articles(f.ctx, tenantB, cms.ArticleFilter{})
	if err != nil {
		t.Fatalf("tenant B list: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("tenant B sees %d tenant A articles", page.Total)
	}
}

func TestMediaUploadServingAndIsolation(t *testing.T) {
	f := newFixture(t)
	data := tinyPNG(t)
	media, err := f.svc.UploadMedia(f.ctx, member(), cms.MediaInput{
		OriginalName: "../../evil.png", Data: data, Alt: "tiny",
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if media.ContentType != "image/png" || media.Width != 2 || media.Height != 3 {
		t.Fatalf("media type=%q size=%dx%d, want image/png 2x3", media.ContentType, media.Width, media.Height)
	}
	if strings.Contains(media.StoredKey, "evil") || strings.Contains(media.StoredKey, "..") {
		t.Fatalf("stored key %q leaks the original name", media.StoredKey)
	}
	stored, payload, err := f.svc.ReadMedia(f.ctx, tenantA, media.ID)
	if err != nil {
		t.Fatalf("read media: %v", err)
	}
	if stored.ID != media.ID || !bytes.Equal(payload, data) {
		t.Fatalf("read media returned a different payload")
	}
	if _, _, err := f.svc.ReadMedia(f.ctx, tenantB, media.ID); !errors.Is(err, cms.ErrMediaNotFound) {
		t.Fatalf("cross-tenant media error = %v, want ErrMediaNotFound", err)
	}
	if _, err := f.svc.UploadMedia(f.ctx, member(), cms.MediaInput{OriginalName: "x.txt", Data: []byte("hello")}); !errors.Is(err, cms.ErrMediaType) {
		t.Fatalf("text upload error = %v, want ErrMediaType", err)
	}
	if err := f.svc.DeleteMedia(f.ctx, member(), media.ID); err != nil {
		t.Fatalf("delete own media: %v", err)
	}
	if _, _, err := f.svc.ReadMedia(f.ctx, tenantA, media.ID); !errors.Is(err, cms.ErrMediaNotFound) {
		t.Fatalf("deleted media error = %v, want ErrMediaNotFound", err)
	}
}

func TestCommentModerationAndReplies(t *testing.T) {
	f := newFixture(t)
	article := saveArticle(t, f, member(), "Commented", "body")
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionPublish, nil); err != nil {
		t.Fatalf("publish commented article: %v", err)
	}
	root, err := f.svc.PostComment(f.ctx, member(), cms.CommentInput{ArticleID: article.ID, Body: "first **comment**"})
	if err != nil {
		t.Fatalf("post comment: %v", err)
	}
	if root.Status != cms.CommentPending {
		t.Fatalf("moderated comment status = %q, want pending", root.Status)
	}
	public, err := f.svc.Comments(f.ctx, tenantA, cms.CommentFilter{ArticleID: article.ID, PublicOnly: true})
	if err != nil {
		t.Fatalf("public comments: %v", err)
	}
	if public.Total != 0 {
		t.Fatalf("pending comment leaked publicly: %d", public.Total)
	}
	if _, err := f.svc.ModerateComment(f.ctx, member(), root.ID, cms.CommentApproved); !errors.Is(err, cms.ErrForbidden) {
		t.Fatalf("member moderate error = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.ModerateComment(f.ctx, admin(), root.ID, cms.CommentApproved); err != nil {
		t.Fatalf("approve comment: %v", err)
	}
	reply, err := f.svc.PostComment(f.ctx, member(), cms.CommentInput{ArticleID: article.ID, ParentID: root.ID, Body: "reply"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if reply.Depth != 1 {
		t.Fatalf("reply depth = %d, want 1", reply.Depth)
	}
	if _, err := f.svc.PostComment(f.ctx, member(), cms.CommentInput{ArticleID: article.ID, ParentID: reply.ID, Body: "too deep"}); !errors.Is(err, cms.ErrCommentParent) {
		t.Fatalf("deep reply error = %v, want ErrCommentParent", err)
	}
	if err := f.svc.DeleteComment(f.ctx, member(), root.ID); err != nil {
		t.Fatalf("delete own comment: %v", err)
	}
	deleted, err := f.store.GetComment(f.ctx, tenantA, root.ID)
	if err != nil {
		t.Fatalf("reload deleted comment: %v", err)
	}
	if deleted.Status != cms.CommentDeleted || deleted.BodyMarkdown != "" || deleted.BodyHTML != "" {
		t.Fatalf("tombstone = %#v, want deleted with empty body", deleted)
	}
	// A moderator holding the pre-delete snapshot must not resurrect the tombstone. |
	// 持有删除前快照的审核者绝不能复活墓碑。
	stale := root
	stale.Status = cms.CommentApproved
	if err := f.store.UpdateComment(f.ctx, tenantA, stale); !errors.Is(err, cms.ErrCommentNotFound) {
		t.Fatalf("stale moderation error = %v, want ErrCommentNotFound", err)
	}
	settings := cms.SettingsInput{SiteName: "Site", PostsPerPage: 10, CommentsEnabled: false, CommentModeration: true, RSSEnabled: true}
	if _, err := f.svc.SaveSettings(f.ctx, member(), settings); !errors.Is(err, cms.ErrForbidden) {
		t.Fatalf("member settings error = %v, want ErrForbidden", err)
	}
	if _, err := f.svc.SaveSettings(f.ctx, admin(), settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	if _, err := f.svc.PostComment(f.ctx, member(), cms.CommentInput{ArticleID: article.ID, Body: "closed"}); !errors.Is(err, cms.ErrCommentsClosed) {
		t.Fatalf("closed comment error = %v, want ErrCommentsClosed", err)
	}
}

// TestPageDoesNotAcceptComments proves standalone pages cannot accumulate comments that no public
// route renders. | TestPageDoesNotAcceptComments 证明单页不会积累公开路由无法渲染的评论。
func TestPageDoesNotAcceptComments(t *testing.T) {
	f := newFixture(t)
	category := defaultCategory(t, f, tenantA)
	page, err := f.svc.SaveArticle(f.ctx, admin(), cms.ArticleInput{
		CategoryID: category.ID, Title: "About", BodyMarkdown: "page body",
		Kind: cms.KindPage, Visibility: cms.VisibilityPublic, AllowComment: true,
	})
	if err != nil {
		t.Fatalf("save page: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), page.ID, cms.ActionPublish, nil); err != nil {
		t.Fatalf("publish page: %v", err)
	}
	if _, err := f.svc.PostComment(f.ctx, member(), cms.CommentInput{ArticleID: page.ID, Body: "hello"}); !errors.Is(err, cms.ErrCommentsClosed) {
		t.Fatalf("page comment error = %v, want ErrCommentsClosed", err)
	}
}

// TestCrossTenantRelationRowsAreInvisible proves tags, comments and revisions are addressed by the
// tenant in every relation query, not only in article lists. | TestCrossTenantRelationRowsAreInvisible
// 证明标签、评论与修订的每条关联查询都以租户为条件，而不只是文章列表。
func TestCrossTenantRelationRowsAreInvisible(t *testing.T) {
	f := newFixture(t)
	tag, err := f.svc.CreateTag(f.ctx, admin(), cms.TagInput{Name: "Secret", Slug: "secret"})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	article := saveArticle(t, f, member(), "Tenant A", "body", "Secret")
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionPublish, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := f.svc.PostComment(f.ctx, member(), cms.CommentInput{ArticleID: article.ID, Body: "private"}); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if _, err := f.svc.SaveArticle(f.ctx, admin(), cms.ArticleInput{
		ID: article.ID, CategoryID: article.CategoryID, Title: article.Title,
		BodyMarkdown: "changed body", Kind: cms.KindPost, Visibility: cms.VisibilityPublic,
		TagIDs: []string{tag.ID},
	}); err != nil {
		t.Fatalf("update article: %v", err)
	}
	revisions, err := f.svc.Revisions(f.ctx, tenantA, article.ID)
	if err != nil || len(revisions) == 0 {
		t.Fatalf("revisions = %v, %v", revisions, err)
	}
	if _, err := f.store.GetTag(f.ctx, tenantB, tag.ID); !errors.Is(err, cms.ErrNotFound) {
		t.Fatalf("cross-tenant tag error = %v, want not found", err)
	}
	if _, err := f.svc.Revision(f.ctx, tenantB, article.ID, revisions[0].ID); !errors.Is(err, cms.ErrRevisionNotFound) {
		t.Fatalf("cross-tenant revision error = %v, want ErrRevisionNotFound", err)
	}
	tags, err := f.svc.Tags(f.ctx, tenantB, "")
	if err != nil || len(tags) != 0 {
		t.Fatalf("tenant B tags = %v, %v; want empty", tags, err)
	}
	comments, err := f.svc.Comments(f.ctx, tenantB, cms.CommentFilter{ArticleID: article.ID})
	if err != nil || comments.Total != 0 {
		t.Fatalf("tenant B comments = %+v, %v; want empty", comments, err)
	}
	if links, err := f.store.ArticleTags(f.ctx, tenantB, article.ID); err != nil || len(links) != 0 {
		t.Fatalf("tenant B article tags = %v, %v; want empty", links, err)
	}
	if err := f.svc.DeleteTag(f.ctx, adminOf(tenantB), tag.ID); !errors.Is(err, cms.ErrTagNotFound) {
		t.Fatalf("cross-tenant tag delete error = %v, want ErrTagNotFound", err)
	}
}

func TestTagLifecycleAndInUseGuard(t *testing.T) {
	f := newFixture(t)
	tag, err := f.svc.CreateTag(f.ctx, admin(), cms.TagInput{Name: "Go Lang", Slug: "go-lang"})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	updated, err := f.svc.UpdateTag(f.ctx, admin(), tag.ID, cms.TagInput{Name: "Golang", Slug: "golang"})
	if err != nil {
		t.Fatalf("update tag: %v", err)
	}
	if updated.Name != "Golang" || updated.Slug != "golang" {
		t.Fatalf("updated tag = %#v", updated)
	}
	if _, err := f.svc.CreateTag(f.ctx, admin(), cms.TagInput{Name: "Duplicate", Slug: "golang"}); !errors.Is(err, cms.ErrTagSlugTaken) {
		t.Fatalf("duplicate tag error = %v, want ErrTagSlugTaken", err)
	}
	article, err := f.svc.SaveArticle(f.ctx, admin(), cms.ArticleInput{
		Title: "Tagged", BodyMarkdown: "body", AllowComment: true, TagIDs: []string{tag.ID},
	})
	if err != nil {
		t.Fatalf("create tagged article: %v", err)
	}
	if err := f.svc.DeleteTag(f.ctx, admin(), tag.ID); !errors.Is(err, cms.ErrTagInUse) {
		t.Fatalf("delete in-use tag error = %v, want ErrTagInUse", err)
	}
	if _, err := f.svc.SaveArticle(f.ctx, admin(), cms.ArticleInput{
		ID: article.ID, Title: "Tagged", BodyMarkdown: "body", AllowComment: true,
	}); err != nil {
		t.Fatalf("detach tags: %v", err)
	}
	if err := f.svc.DeleteTag(f.ctx, admin(), tag.ID); err != nil {
		t.Fatalf("delete detached tag: %v", err)
	}
}

func TestSlugUniqueness(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.CreateCategory(f.ctx, admin(), cms.CategoryInput{Name: "Unique", Slug: "unique", Visible: true}); err != nil {
		t.Fatalf("create category: %v", err)
	}
	if _, err := f.svc.CreateCategory(f.ctx, admin(), cms.CategoryInput{Name: "Unique Again", Slug: "unique", Visible: true}); !errors.Is(err, cms.ErrCategorySlugTaken) {
		t.Fatalf("duplicate category slug error = %v, want ErrCategorySlugTaken", err)
	}
	if _, err := f.svc.SaveArticle(f.ctx, admin(), cms.ArticleInput{Title: "Same Slug", BodyMarkdown: "a", AllowComment: true}); err != nil {
		t.Fatalf("create first article: %v", err)
	}
	if _, err := f.svc.SaveArticle(f.ctx, admin(), cms.ArticleInput{Title: "Same Slug", BodyMarkdown: "b", AllowComment: true}); !errors.Is(err, cms.ErrArticleSlugTaken) {
		t.Fatalf("duplicate article slug error = %v, want ErrArticleSlugTaken", err)
	}
}

func TestTransitionRules(t *testing.T) {
	f := newFixture(t)
	article := saveArticle(t, f, member(), "Transitions", "body")
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionApprove, nil); !errors.Is(err, cms.ErrArticleTransition) {
		t.Fatalf("approve draft error = %v, want ErrArticleTransition", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, member(), article.ID, cms.ActionSubmit, nil); err != nil {
		t.Fatalf("author submit: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionPublish, nil); !errors.Is(err, cms.ErrArticleTransition) {
		t.Fatalf("publish pending error = %v, want ErrArticleTransition", err)
	}
	pending, err := f.svc.Article(f.ctx, tenantA, article.ID, false)
	if err != nil {
		t.Fatalf("reload pending article: %v", err)
	}
	if pending.Status != cms.ArticlePending {
		t.Fatalf("status = %q, want pending", pending.Status)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionReject, nil); err != nil {
		t.Fatalf("reject pending: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionRestore, nil); !errors.Is(err, cms.ErrArticleTransition) {
		t.Fatalf("restore draft error = %v, want ErrArticleTransition", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionPublish, nil); err != nil {
		t.Fatalf("publish draft: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionArchive, nil); err != nil {
		t.Fatalf("archive published: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionPublish, nil); !errors.Is(err, cms.ErrArticleTransition) {
		t.Fatalf("publish archived error = %v, want ErrArticleTransition", err)
	}
}

func TestAuthorLifecycleActions(t *testing.T) {
	f := newFixture(t)
	article := saveArticle(t, f, member(), "Author Lifecycle", "body")
	if _, err := f.svc.TransitionArticle(f.ctx, member(), article.ID, cms.ActionSubmit, nil); err != nil {
		t.Fatalf("author submit: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, member(), article.ID, cms.ActionWithdraw, nil); err != nil {
		t.Fatalf("author withdraw: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, member(), article.ID, cms.ActionWithdraw, nil); !errors.Is(err, cms.ErrArticleTransition) {
		t.Fatalf("withdraw draft error = %v, want ErrArticleTransition", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, member(), article.ID, cms.ActionSubmit, nil); err != nil {
		t.Fatalf("author resubmit: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, admin(), article.ID, cms.ActionApprove, nil); err != nil {
		t.Fatalf("reviewer approve: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, member(), article.ID, cms.ActionArchive, nil); err != nil {
		t.Fatalf("author archive published: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, member(), article.ID, cms.ActionRestore, nil); err != nil {
		t.Fatalf("author restore: %v", err)
	}
	if _, err := f.svc.TransitionArticle(f.ctx, member(), article.ID, cms.ActionPublish, nil); !errors.Is(err, cms.ErrForbidden) {
		t.Fatalf("author publish error = %v, want ErrForbidden", err)
	}
}

func TestSearchTermCeiling(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Articles(f.ctx, tenantA, cms.ArticleFilter{Keyword: "a b c d e f g"})
	if !errors.Is(err, cms.ErrSearchTooBroad) {
		t.Fatalf("search term ceiling error = %v, want ErrSearchTooBroad", err)
	}
}

func TestZeroValueFlagsPersist(t *testing.T) {
	f := newFixture(t)
	hidden, err := f.svc.CreateCategory(f.ctx, admin(), cms.CategoryInput{Name: "Hidden", Slug: "hidden", Visible: false})
	if err != nil {
		t.Fatalf("create hidden category: %v", err)
	}
	reloaded, err := f.svc.Category(f.ctx, tenantA, hidden.ID)
	if err != nil {
		t.Fatalf("reload hidden category: %v", err)
	}
	if reloaded.Visible {
		t.Fatalf("hidden category became visible")
	}
	article, err := f.svc.SaveArticle(f.ctx, member(), cms.ArticleInput{
		Title: "Closed Comments", BodyMarkdown: "body", AllowComment: false,
	})
	if err != nil {
		t.Fatalf("create article: %v", err)
	}
	stored, err := f.store.GetArticle(f.ctx, tenantA, article.ID)
	if err != nil {
		t.Fatalf("reload article: %v", err)
	}
	if stored.AllowComment {
		t.Fatalf("allow_comment=false became true")
	}
	if _, err := f.svc.SaveSettings(f.ctx, admin(), cms.SettingsInput{
		SiteName: "Closed", PostsPerPage: 10, CommentsEnabled: false, CommentModeration: true, RSSEnabled: true,
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	settings, err := f.svc.Settings(f.ctx, tenantA)
	if err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	if settings.CommentsEnabled {
		t.Fatalf("comments_enabled=false became true")
	}
}

func TestRevisionRecordsTheActualEditor(t *testing.T) {
	f := newFixture(t)
	article := saveArticle(t, f, member(), "Edited By Admin", "first body")
	if _, err := f.svc.SaveArticle(f.ctx, admin(), cms.ArticleInput{
		ID: article.ID, Title: "Edited By Admin", BodyMarkdown: "second body", AllowComment: true,
	}); err != nil {
		t.Fatalf("admin update: %v", err)
	}
	revisions, err := f.svc.Revisions(f.ctx, tenantA, article.ID)
	if err != nil {
		t.Fatalf("revisions: %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("revision count = %d, want 1", len(revisions))
	}
	if revisions[0].EditorID != admin().ID {
		t.Fatalf("revision editor = %q, want %q", revisions[0].EditorID, admin().ID)
	}
}

func TestChineseTitleGetsIdentifierSlug(t *testing.T) {
	f := newFixture(t)
	article := saveArticle(t, f, member(), "中文标题", "正文")
	if !strings.HasPrefix(article.Slug, "article-") {
		t.Fatalf("slug = %q, want article- prefix", article.Slug)
	}
	if !strings.HasPrefix(article.BodyHTML, "<p>") {
		t.Fatalf("rendered HTML = %q, want a paragraph", article.BodyHTML)
	}
}

func TestMarkdownSanitisesRawHTML(t *testing.T) {
	renderer := markdown.New()
	html, text, err := renderer.Render("<script>alert(1)</script>\n\n[bad](javascript:alert(1))\n\n| a | b |\n|---|---|\n| 1 | 2 |")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "<script") || strings.Contains(html, "javascript:") {
		t.Fatalf("unsafe output survived: %q", html)
	}
	if !strings.Contains(html, "<table>") {
		t.Fatalf("GFM table missing: %q", html)
	}
	if strings.Contains(text, "alert") && strings.Contains(text, "script") {
		t.Fatalf("plain text kept raw HTML: %q", text)
	}
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// TestDeletingTargetsCleansSoftDeletedRelations proves that removing a category or tag never leaves a
// soft-deleted article pointing at a row that no longer exists. | TestDeletingTargetsCleansSoftDeletedRelations
// 证明删除分类或标签后，软删除文章不会继续指向已不存在的行。
func TestDeletingTargetsCleansSoftDeletedRelations(t *testing.T) {
	f := newFixture(t)
	actor := admin()
	category, err := f.svc.CreateCategory(f.ctx, actor, cms.CategoryInput{Name: "Cleaned", Slug: "cleaned", Visible: true})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	tag, err := f.svc.CreateTag(f.ctx, actor, cms.TagInput{Name: "Cleanup", Slug: "cleanup"})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	article, err := f.svc.SaveArticle(f.ctx, actor, cms.ArticleInput{
		CategoryID: category.ID, Title: "Soft deleted", BodyMarkdown: "body",
		Kind: cms.KindPost, Visibility: cms.VisibilityPublic, TagIDs: []string{tag.ID},
	})
	if err != nil {
		t.Fatalf("save article: %v", err)
	}
	if err := f.svc.DeleteArticle(f.ctx, actor, article.ID); err != nil {
		t.Fatalf("delete article: %v", err)
	}
	if err := f.svc.DeleteTag(f.ctx, actor, tag.ID); err != nil {
		t.Fatalf("delete tag: %v", err)
	}
	var links int64
	if err := f.db.Table("yd_cms_article_tags").
		Where("tenant_id = ? AND tag_id = ?", tenantA, tag.ID).Count(&links).Error; err != nil {
		t.Fatalf("count links: %v", err)
	}
	if links != 0 {
		t.Fatalf("article_tags links = %d, want 0", links)
	}
	if err := f.svc.DeleteCategory(f.ctx, actor, category.ID); err != nil {
		t.Fatalf("delete category: %v", err)
	}
	var categoryID string
	if err := f.db.Table("yd_cms_articles").Select("category_id").
		Where("tenant_id = ? AND id = ?", tenantA, article.ID).Row().Scan(&categoryID); err != nil {
		t.Fatalf("read article category: %v", err)
	}
	if categoryID != "" {
		t.Fatalf("soft-deleted article category_id = %q, want empty", categoryID)
	}
}
