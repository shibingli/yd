package gormstore

import (
	"context"
	"strings"

	"github.com/shibingli/yd/cms"
	"gorm.io/gorm"
)

// CreateComment inserts a comment. | CreateComment 插入一条评论。
func (s *Store) CreateComment(ctx context.Context, tenantID string, comment cms.Comment) error {
	comment.TenantID = tenantID
	return queryError(s.dbc(ctx).Create(&comment).Error)
}

// GetComment returns one comment. | GetComment 返回一条评论。
func (s *Store) GetComment(ctx context.Context, tenantID, id string) (cms.Comment, error) {
	var row cms.Comment
	if err := s.dbc(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&row).Error; err != nil {
		return cms.Comment{}, queryError(err)
	}
	return row, nil
}

// ListComments returns a decorated page of comments. | ListComments 返回装饰后的一页评论。
func (s *Store) ListComments(ctx context.Context, tenantID string, filter cms.CommentFilter) (cms.CommentPage, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	query := s.dbc(ctx).Model(&cms.Comment{}).Where("tenant_id = ?", tenantID)
	if filter.PublicOnly {
		query = query.Where("status = ?", cms.CommentApproved).
			Where("(parent_id = ? OR parent_id IN (?))", "",
				s.dbc(ctx).Model(&cms.Comment{}).Select("id").
					Where("tenant_id = ? AND status = ?", tenantID, cms.CommentApproved))
	} else if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	} else {
		query = query.Where("status <> ?", cms.CommentDeleted)
	}
	if filter.ArticleID != "" {
		query = query.Where("article_id = ?", filter.ArticleID)
	}
	if filter.AuthorID != "" {
		query = query.Where("author_id = ?", filter.AuthorID)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := likePattern(keyword)
		query = query.Where("(LOWER(author_name) LIKE ? ESCAPE '!' OR LOWER(body_text) LIKE ? ESCAPE '!')", pattern, pattern)
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return cms.CommentPage{}, queryError(err)
	}
	order := "created_at DESC, id DESC"
	if filter.PublicOnly {
		order = "created_at ASC, id ASC"
	}
	var rows []cms.Comment
	if err := query.Session(&gorm.Session{}).Order(order).
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return cms.CommentPage{}, queryError(err)
	}
	items, err := s.fillCommentViews(ctx, tenantID, rows)
	if err != nil {
		return cms.CommentPage{}, err
	}
	return cms.CommentPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

// fillCommentViews decorates comments with their article title and slug. | fillCommentViews 用文章标题与别名装饰评论。
func (s *Store) fillCommentViews(ctx context.Context, tenantID string, rows []cms.Comment) ([]cms.CommentView, error) {
	if len(rows) == 0 {
		return []cms.CommentView{}, nil
	}
	ids := make([]string, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if !seen[row.ArticleID] {
			seen[row.ArticleID] = true
			ids = append(ids, row.ArticleID)
		}
	}
	type articleRow struct {
		ID    string
		Title string
		Slug  string
	}
	articles := make(map[string]articleRow, len(ids))
	for _, batch := range chunks(ids) {
		var found []articleRow
		if err := s.dbc(ctx).Model(&cms.Article{}).Select("id, title, slug").
			Where("tenant_id = ? AND id IN ?", tenantID, batch).Scan(&found).Error; err != nil {
			return nil, queryError(err)
		}
		for _, item := range found {
			articles[item.ID] = item
		}
	}
	out := make([]cms.CommentView, 0, len(rows))
	for _, row := range rows {
		view := cms.CommentView{Comment: row}
		if article, ok := articles[row.ArticleID]; ok {
			view.ArticleTitle = article.Title
			view.ArticleSlug = article.Slug
		}
		out = append(out, view)
	}
	return out, nil
}

// UpdateComment replaces the moderation state and body of one comment. A tombstone is never
// resurrected: the predicate excludes deleted rows, and a moderation write that loses to a
// concurrent delete reports the comment as missing while a repeated delete stays idempotent. |
// UpdateComment 替换一条评论的审核状态与正文。墓碑绝不会复活：条件排除已删除行；输给并发删除的审核
// 写入报告评论不存在，而重复删除保持幂等。
func (s *Store) UpdateComment(ctx context.Context, tenantID string, comment cms.Comment) error {
	result := s.dbc(ctx).Model(&cms.Comment{}).
		Where("tenant_id = ? AND id = ? AND status <> ?", tenantID, comment.ID, cms.CommentDeleted).
		Select("status", "body_markdown", "body_html", "body_text", "updated_at").Updates(&comment)
	if result.Error != nil {
		return queryError(result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	var stored cms.Comment
	if err := s.dbc(ctx).Where("tenant_id = ? AND id = ?", tenantID, comment.ID).First(&stored).Error; err != nil {
		return queryError(err)
	}
	if stored.Status == cms.CommentDeleted && comment.Status != cms.CommentDeleted {
		return cms.ErrCommentNotFound
	}
	// A no-op update (same state and body) is success; MySQL reports zero affected rows for it. |
	// 原样更新（状态与正文相同）算成功；MySQL 对它会报告零影响行。
	return nil
}

// CountComments counts comments by article and status. | CountComments 按文章与状态统计评论。
func (s *Store) CountComments(ctx context.Context, tenantID, articleID string, status cms.CommentStatus) (int64, error) {
	query := s.dbc(ctx).Model(&cms.Comment{}).Where("tenant_id = ?", tenantID)
	if articleID != "" {
		query = query.Where("article_id = ?", articleID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	} else {
		query = query.Where("status <> ?", cms.CommentDeleted)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return 0, queryError(err)
	}
	return total, nil
}
