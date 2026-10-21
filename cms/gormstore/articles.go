package gormstore

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/shibingli/yd/cms"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateArticle inserts an article inside a transaction so the category lock lives until the write
// commits. | CreateArticle 在事务中插入文章，使分类锁持续到写入提交。
func (s *Store) CreateArticle(ctx context.Context, tenantID string, article cms.Article) error {
	return queryError(s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		return s.createArticleTx(ctx, tx, tenantID, article)
	}))
}

// CreateArticleWithTags inserts an article and its tag links atomically. |
// CreateArticleWithTags 原子地插入文章及其标签连接。
func (s *Store) CreateArticleWithTags(ctx context.Context, tenantID string, article cms.Article, tags []cms.Tag) error {
	err := s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.createArticleTx(ctx, tx, tenantID, article); err != nil {
			return err
		}
		return s.replaceArticleTagsTx(ctx, tx, tenantID, article.ID, tags)
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return cms.ErrArticleSlugTaken
	}
	return queryError(err)
}

// createArticleTx writes one article on the supplied handle. | createArticleTx 在给定句柄上写入一篇文章。
func (s *Store) createArticleTx(_ context.Context, tx *gorm.DB, tenantID string, article cms.Article) error {
	article.TenantID = tenantID
	if err := lockCategoryTx(tx, tenantID, article.CategoryID); err != nil {
		return err
	}
	// Select("*") includes zero values, so allow_comment=false survives the column default. |
	// Select("*") 包含零值，因此 allow_comment=false 不会被列默认值覆盖。
	return queryError(tx.Select("*").Create(&article).Error)
}

// UpdateArticleContent replaces the content fields of one article. The version in the WHERE clause is
// the optimistic lock: a save that read version N cannot overwrite a row already at N+1, so a slow
// editor never reverts a newer body, and status, publish_at and view_count are never touched here. |
// UpdateArticleContent 替换一篇文章的内容字段。WHERE 中的版本就是乐观锁：读到版本 N 的保存无法覆盖
// 已经是 N+1 的行，因此慢编辑器不会把更新的正文改回去；状态、publish_at 与浏览量也不在这里写。
func (s *Store) UpdateArticleContent(ctx context.Context, tenantID string, article cms.Article, expectedVersion int) error {
	return queryError(s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		return s.updateArticleContentTx(ctx, tx, tenantID, article, expectedVersion)
	}))
}

// updateArticleContentTx writes the content fields on the supplied handle. | updateArticleContentTx 在给定句柄上写入内容字段。
func (s *Store) updateArticleContentTx(ctx context.Context, tx *gorm.DB, tenantID string, article cms.Article, expectedVersion int) error {
	if err := lockCategoryTx(tx, tenantID, article.CategoryID); err != nil {
		return err
	}
	result := tx.Model(&cms.Article{}).
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL AND version = ?", tenantID, article.ID, expectedVersion).
		Select("category_id", "title", "slug", "summary", "cover_url",
			"body_markdown", "body_html", "body_text", "search_text",
			"kind", "visibility", "featured", "pinned", "allow_comment",
			"seo_title", "seo_description", "seo_keywords", "canonical_url", "source",
			"version", "updated_at").
		Updates(&article)
	if result.Error != nil {
		return queryError(result.Error)
	}
	if result.RowsAffected == 0 {
		return s.articleMissingOrConflict(tx, tenantID, article.ID)
	}
	return nil
}

// UpdateArticleContentWithTags updates the content and tag links atomically. |
// UpdateArticleContentWithTags 原子地更新内容与标签连接。
func (s *Store) UpdateArticleContentWithTags(ctx context.Context, tenantID string, article cms.Article, expectedVersion int, tags []cms.Tag) error {
	err := s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.updateArticleContentTx(ctx, tx, tenantID, article, expectedVersion); err != nil {
			return err
		}
		return s.replaceArticleTagsTx(ctx, tx, tenantID, article.ID, tags)
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return cms.ErrArticleSlugTaken
	}
	return queryError(err)
}

// UpdateArticleStatus applies one status change guarded by the expected current status. |
// UpdateArticleStatus 应用一次状态变更，并以期望的当前状态作为前置条件。
func (s *Store) UpdateArticleStatus(ctx context.Context, tenantID, id string, expectedStatus, status cms.ArticleStatus, publishAt *time.Time, updatedAt time.Time) error {
	result := s.dbc(ctx).Model(&cms.Article{}).
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL AND status = ?", tenantID, id, expectedStatus).
		Select("status", "publish_at", "updated_at").
		Updates(&cms.Article{Status: status, PublishAt: publishAt, UpdatedAt: updatedAt})
	if result.Error != nil {
		return queryError(result.Error)
	}
	if result.RowsAffected == 0 {
		return s.articleMissingOrConflict(s.dbc(ctx), tenantID, id)
	}
	return nil
}

// articleMissingOrConflict distinguishes a vanished row from a concurrent modification. It must use
// the caller's handle: opening a second connection from inside a transaction self-deadlocks the pool
// at MaxOpenConns=1 and stalls every connection at higher limits. |
// articleMissingOrConflict 区分行已消失与并发修改。它必须使用调用方的句柄：在事务内另开连接会在
// MaxOpenConns=1 时自锁连接池，并在更高上限下拖住所有连接。
func (s *Store) articleMissingOrConflict(tx *gorm.DB, tenantID, id string) error {
	var count int64
	if err := tx.Model(&cms.Article{}).
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", tenantID, id).
		Count(&count).Error; err != nil {
		return queryError(err)
	}
	if count == 0 {
		return cms.ErrArticleNotFound
	}
	return cms.ErrArticleConflict
}

// GetArticle returns one article. | GetArticle 返回一篇文章。
func (s *Store) GetArticle(ctx context.Context, tenantID, id string) (cms.Article, error) {
	var row cms.Article
	if err := s.dbc(ctx).Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", tenantID, id).
		First(&row).Error; err != nil {
		return cms.Article{}, queryError(err)
	}
	return row, nil
}

// GetArticleBySlug returns one article by slug. | GetArticleBySlug 按别名返回一篇文章。
func (s *Store) GetArticleBySlug(ctx context.Context, tenantID, slug string) (cms.Article, error) {
	var row cms.Article
	if err := s.dbc(ctx).Where("tenant_id = ? AND slug = ? AND deleted_at IS NULL", tenantID, slug).
		First(&row).Error; err != nil {
		return cms.Article{}, queryError(err)
	}
	return row, nil
}

// ListArticles returns a decorated page. | ListArticles 返回装饰后的一页文章。
func (s *Store) ListArticles(ctx context.Context, tenantID string, filter cms.ArticleFilter) (cms.ArticlePage, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	query := s.articleFilter(ctx, tenantID, filter)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return cms.ArticlePage{}, queryError(err)
	}
	var rows []cms.Article
	if err := query.Session(&gorm.Session{}).Order(articleOrder(filter)).
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return cms.ArticlePage{}, queryError(err)
	}
	now := filter.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	items, err := s.fillArticleViews(ctx, tenantID, rows, now)
	if err != nil {
		return cms.ArticlePage{}, err
	}
	return cms.ArticlePage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

// articleFilter builds the tenant-first article query. | articleFilter 构建 tenant-first 文章查询。
func (s *Store) articleFilter(ctx context.Context, tenantID string, filter cms.ArticleFilter) *gorm.DB {
	query := s.dbc(ctx).Model(&cms.Article{}).Where("tenant_id = ? AND deleted_at IS NULL", tenantID)
	if filter.PublicOnly {
		query = query.Where("status = ? AND visibility = ?", cms.ArticlePublished, cms.VisibilityPublic)
		query = query.Where("(publish_at IS NULL OR publish_at <= ?)", filter.Now.UTC())
	} else {
		if filter.ScheduledOnly {
			query = query.Where("status = ? AND publish_at > ?", cms.ArticlePublished, filter.Now.UTC())
		} else if filter.Status != "" {
			query = query.Where("status = ?", filter.Status)
		}
		if filter.Visibility != "" {
			query = query.Where("visibility = ?", filter.Visibility)
		}
	}
	if filter.Kind != "" {
		query = query.Where("kind = ?", filter.Kind)
	}
	if filter.AuthorID != "" {
		query = query.Where("author_id = ?", filter.AuthorID)
	}
	if len(filter.CategoryIDs) > 0 {
		query = query.Where("category_id IN ?", filter.CategoryIDs)
	} else if filter.CategoryID != "" {
		query = query.Where("category_id = ?", filter.CategoryID)
	}
	if filter.TagID != "" {
		sub := s.dbc(ctx).Model(&cms.ArticleTag{}).Select("article_id").
			Where("tenant_id = ? AND tag_id = ?", tenantID, filter.TagID)
		query = query.Where("id IN (?)", sub)
	}
	if filter.Featured != nil {
		query = query.Where("featured = ?", *filter.Featured)
	}
	if filter.Pinned != nil {
		query = query.Where("pinned = ?", *filter.Pinned)
	}
	for _, term := range strings.Fields(filter.Keyword) {
		query = query.Where("search_text LIKE ? ESCAPE '!'", likePattern(term))
	}
	return query
}

// articleOrder returns a whitelisted ORDER BY clause. | articleOrder 返回白名单内的 ORDER BY 子句。
func articleOrder(filter cms.ArticleFilter) string {
	if filter.PublicOnly {
		return "pinned DESC, COALESCE(publish_at, created_at) DESC, id DESC"
	}
	switch filter.Order {
	case "created":
		return "created_at DESC, id DESC"
	case "oldest":
		return "created_at ASC, id ASC"
	case "title":
		return "title ASC, id ASC"
	default:
		return "updated_at DESC, id DESC"
	}
}

// fillArticleViews decorates article rows with category and tag projections. | fillArticleViews 用分类与标签投影装饰文章行。
func (s *Store) fillArticleViews(ctx context.Context, tenantID string, rows []cms.Article, now time.Time) ([]cms.ArticleView, error) {
	if len(rows) == 0 {
		return []cms.ArticleView{}, nil
	}
	articleIDs := make([]string, 0, len(rows))
	categoryIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		articleIDs = append(articleIDs, row.ID)
		if row.CategoryID != "" {
			categoryIDs = append(categoryIDs, row.CategoryID)
		}
	}
	categories, err := s.categoryMap(ctx, tenantID, categoryIDs)
	if err != nil {
		return nil, err
	}
	tags, err := s.ArticlesTags(ctx, tenantID, articleIDs)
	if err != nil {
		return nil, err
	}
	out := make([]cms.ArticleView, 0, len(rows))
	for _, row := range rows {
		view := cms.ArticleView{Article: row, Tags: tags[row.ID]}
		if category, ok := categories[row.CategoryID]; ok {
			view.CategoryName = category.Name
			view.CategorySlug = category.Slug
			view.CategoryPath = category.Path
		}
		view.Scheduled = row.Status == cms.ArticlePublished && row.PublishAt != nil && row.PublishAt.After(now)
		out = append(out, view)
	}
	return out, nil
}

// replaceArticleTagsTx replaces the tag links on the supplied handle. | replaceArticleTagsTx 在给定句柄上替换标签连接。
func (s *Store) replaceArticleTagsTx(_ context.Context, tx *gorm.DB, tenantID, articleID string, tags []cms.Tag) error {
	ids := make([]string, 0, len(tags))
	for _, tag := range tags {
		ids = append(ids, tag.ID)
	}
	ids, err := lockArticleTagsTx(tx, tenantID, ids)
	if err != nil {
		return err
	}
	if err := tx.Where("tenant_id = ? AND article_id = ?", tenantID, articleID).
		Delete(&cms.ArticleTag{}).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	links := make([]cms.ArticleTag, 0, len(ids))
	for _, id := range ids {
		links = append(links, cms.ArticleTag{
			ID:        newRowID("atg_"),
			TenantID:  tenantID,
			ArticleID: articleID,
			TagID:     id,
			CreatedAt: time.Now().UTC(),
		})
	}
	return tx.Clauses(onConflictDoNothing()).Create(&links).Error
}

// lockArticleTagsTx locks the referenced tag rows and reports a missing tag. The shared lock keeps a
// concurrent tag delete from removing a row between validation and link insertion; without a foreign
// key this is the only place that can prevent a dangling link. | lockArticleTagsTx 锁定被引用的标签行并
// 报告缺失标签。共享锁阻止并发的标签删除在校验与插入连接之间移除行；没有外键时，这是唯一能阻止悬空连接的位置。
func lockArticleTagsTx(tx *gorm.DB, tenantID string, tagIDs []string) ([]string, error) {
	ids := uniqueSortedIDs(tagIDs)
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []cms.Tag
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
		Select("id").Where("tenant_id = ? AND id IN ?", tenantID, ids).
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, queryError(err)
	}
	if len(rows) != len(ids) {
		return nil, cms.ErrTagNotFound
	}
	return ids, nil
}

// lockCategoryTx locks the referenced category row and reports a missing category. |
// lockCategoryTx 锁定被引用的分类行并报告缺失分类。
func lockCategoryTx(tx *gorm.DB, tenantID, categoryID string) error {
	if strings.TrimSpace(categoryID) == "" {
		return nil
	}
	var category cms.Category
	err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id").
		Where("tenant_id = ? AND id = ?", tenantID, categoryID).Take(&category).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cms.ErrCategoryNotFound
	}
	if err != nil {
		return queryError(err)
	}
	return nil
}

// uniqueSortedIDs trims, deduplicates and orders ids so concurrent transactions always lock rows in
// the same order. | uniqueSortedIDs 裁剪、去重并排序 ID，使并发事务始终以相同顺序锁定行。
func uniqueSortedIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ArticleTags returns the tags of one article, ordered by name. | ArticleTags 按名称返回一篇文章的标签。
func (s *Store) ArticleTags(ctx context.Context, tenantID, articleID string) ([]cms.Tag, error) {
	var tags []cms.Tag
	err := s.dbc(ctx).Table("yd_cms_tags AS t").
		Select("t.*").
		Joins("JOIN yd_cms_article_tags AS at ON at.tenant_id = t.tenant_id AND at.tag_id = t.id").
		Where("at.tenant_id = ? AND at.article_id = ?", tenantID, articleID).
		Order("t.name ASC, t.id ASC").Scan(&tags).Error
	if err != nil {
		return nil, queryError(err)
	}
	return tags, nil
}

// ArticlesTags batches tags for a set of articles. | ArticlesTags 批量返回一组文章的标签。
func (s *Store) ArticlesTags(ctx context.Context, tenantID string, articleIDs []string) (map[string][]cms.Tag, error) {
	out := make(map[string][]cms.Tag, len(articleIDs))
	if len(articleIDs) == 0 {
		return out, nil
	}
	type row struct {
		ArticleID string
		ID        string
		Name      string
		Slug      string
	}
	for _, batch := range chunks(articleIDs) {
		var rows []row
		err := s.dbc(ctx).Table("yd_cms_article_tags AS at").
			Select("at.article_id AS article_id, t.id AS id, t.name AS name, t.slug AS slug").
			Joins("JOIN yd_cms_tags AS t ON t.tenant_id = at.tenant_id AND t.id = at.tag_id").
			Where("at.tenant_id = ? AND at.article_id IN ?", tenantID, batch).
			Order("t.name ASC, t.id ASC").Scan(&rows).Error
		if err != nil {
			return nil, queryError(err)
		}
		for _, item := range rows {
			out[item.ArticleID] = append(out[item.ArticleID], cms.Tag{ID: item.ID, TenantID: tenantID, Name: item.Name, Slug: item.Slug})
		}
	}
	return out, nil
}

// IncrementArticleViews adds one to the view counter. | IncrementArticleViews 把浏览量加一。
func (s *Store) IncrementArticleViews(ctx context.Context, tenantID, id string) error {
	result := s.dbc(ctx).Model(&cms.Article{}).
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", tenantID, id).
		UpdateColumn("view_count", gorm.Expr("view_count + ?", 1))
	if result.Error != nil {
		return queryError(result.Error)
	}
	return nil
}

// SoftDeleteArticle marks an article deleted and rewrites its slug. | SoftDeleteArticle 标记文章已删除并改写别名。
func (s *Store) SoftDeleteArticle(ctx context.Context, tenantID, id string, deletedAt time.Time, slug string) error {
	return rowsError(s.dbc(ctx).Model(&cms.Article{}).
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", tenantID, id).
		Updates(map[string]any{"deleted_at": deletedAt, "updated_at": deletedAt, "slug": slug}))
}

// RelatedArticles returns public articles in the same categories. | RelatedArticles 返回同分类的公开文章。
func (s *Store) RelatedArticles(ctx context.Context, tenantID, articleID string, categoryIDs []string, now time.Time, limit int) ([]cms.ArticleView, error) {
	query := s.dbc(ctx).Model(&cms.Article{}).
		Where("tenant_id = ? AND id <> ? AND deleted_at IS NULL", tenantID, articleID).
		Where("status = ? AND visibility = ?", cms.ArticlePublished, cms.VisibilityPublic).
		Where("(publish_at IS NULL OR publish_at <= ?)", now.UTC())
	if len(categoryIDs) > 0 {
		query = query.Where("category_id IN ?", categoryIDs)
	}
	var rows []cms.Article
	if err := query.Order("COALESCE(publish_at, created_at) DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, queryError(err)
	}
	return s.fillArticleViews(ctx, tenantID, rows, now)
}

// Counts returns the dashboard aggregate. | Counts 返回概览聚合。
func (s *Store) Counts(ctx context.Context, tenantID string) (cms.Counts, error) {
	var out cms.Counts
	base := func() *gorm.DB {
		return s.dbc(ctx).Model(&cms.Article{}).Where("tenant_id = ? AND deleted_at IS NULL", tenantID)
	}
	if err := base().Count(&out.Articles).Error; err != nil {
		return out, queryError(err)
	}
	if err := base().Where("status = ?", cms.ArticlePublished).Count(&out.Published).Error; err != nil {
		return out, queryError(err)
	}
	if err := base().Where("status = ?", cms.ArticleDraft).Count(&out.Drafts).Error; err != nil {
		return out, queryError(err)
	}
	if err := base().Where("status = ?", cms.ArticlePending).Count(&out.Pending).Error; err != nil {
		return out, queryError(err)
	}
	if err := base().Where("status = ?", cms.ArticleArchived).Count(&out.Archived).Error; err != nil {
		return out, queryError(err)
	}
	if err := base().Where("kind = ?", cms.KindPage).Count(&out.Pages).Error; err != nil {
		return out, queryError(err)
	}
	if err := s.dbc(ctx).Model(&cms.Category{}).Where("tenant_id = ?", tenantID).Count(&out.Categories).Error; err != nil {
		return out, queryError(err)
	}
	if err := s.dbc(ctx).Model(&cms.Tag{}).Where("tenant_id = ?", tenantID).Count(&out.Tags).Error; err != nil {
		return out, queryError(err)
	}
	if err := s.dbc(ctx).Model(&cms.Media{}).Where("tenant_id = ?", tenantID).Count(&out.Media).Error; err != nil {
		return out, queryError(err)
	}
	if err := s.dbc(ctx).Model(&cms.Comment{}).
		Where("tenant_id = ? AND status <> ?", tenantID, cms.CommentDeleted).Count(&out.Comments).Error; err != nil {
		return out, queryError(err)
	}
	if err := s.dbc(ctx).Model(&cms.Comment{}).
		Where("tenant_id = ? AND status = ?", tenantID, cms.CommentPending).Count(&out.PendingComments).Error; err != nil {
		return out, queryError(err)
	}
	var views struct {
		Total int64
	}
	if err := base().Select("COALESCE(SUM(view_count), 0) AS total").Scan(&views).Error; err != nil {
		return out, queryError(err)
	}
	out.Views = views.Total
	return out, nil
}
