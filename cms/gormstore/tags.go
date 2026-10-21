package gormstore

import (
	"context"
	"errors"
	"strings"

	"github.com/shibingli/yd/cms"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// tagListLimit bounds the selector query. A tag catalogue is a navigation aid, not a data export;
// without a ceiling a tenant with tens of thousands of tags would load all of them into every editor. |
// tagListLimit 限制选择器查询。标签目录是导航辅助而非数据导出；没有上限时，拥有数万标签的租户每次
// 打开编辑器都会把它们全部读进内存。
const tagListLimit = 2000

// ListTags returns tags, optionally filtered by keyword. | ListTags 返回标签，可按关键词过滤。
func (s *Store) ListTags(ctx context.Context, tenantID, keyword string) ([]cms.Tag, error) {
	query := s.dbc(ctx).Where("tenant_id = ?", tenantID)
	if keyword = keywordString(keyword); keyword != "" {
		pattern := likePattern(keyword)
		query = query.Where("(LOWER(name) LIKE ? ESCAPE '!' OR slug LIKE ? ESCAPE '!')", pattern, pattern)
	}
	var rows []cms.Tag
	if err := query.Order("name ASC, id ASC").Limit(tagListLimit).Find(&rows).Error; err != nil {
		return nil, queryError(err)
	}
	return rows, nil
}

// GetTag returns one tag. | GetTag 返回一个标签。
func (s *Store) GetTag(ctx context.Context, tenantID, id string) (cms.Tag, error) {
	var row cms.Tag
	if err := s.dbc(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&row).Error; err != nil {
		return cms.Tag{}, queryError(err)
	}
	return row, nil
}

// GetTagBySlug returns one tag by slug. | GetTagBySlug 按别名返回一个标签。
func (s *Store) GetTagBySlug(ctx context.Context, tenantID, slug string) (cms.Tag, error) {
	var row cms.Tag
	if err := s.dbc(ctx).Where("tenant_id = ? AND slug = ?", tenantID, slug).First(&row).Error; err != nil {
		return cms.Tag{}, queryError(err)
	}
	return row, nil
}

// CreateTag inserts a tag. | CreateTag 插入一个标签。
func (s *Store) CreateTag(ctx context.Context, tenantID string, tag cms.Tag) error {
	tag.TenantID = tenantID
	if err := s.dbc(ctx).Create(&tag).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return cms.ErrTagSlugTaken
		}
		return queryError(err)
	}
	return nil
}

// UpdateTag replaces the editable fields of one tag. | UpdateTag 替换一个标签的可编辑字段。
func (s *Store) UpdateTag(ctx context.Context, tenantID string, tag cms.Tag) error {
	result := s.dbc(ctx).Model(&cms.Tag{}).
		Where("tenant_id = ? AND id = ?", tenantID, tag.ID).
		Select("name", "slug", "updated_at").Updates(&tag)
	if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
		return cms.ErrTagSlugTaken
	}
	return s.updateResult(ctx, result, tenantID, &cms.Tag{}, tag.ID)
}

// DeleteTagIfUnused re-checks usage under a row lock before deleting. |
// DeleteTagIfUnused 在行锁下重新检查使用情况后再删除。
func (s *Store) DeleteTagIfUnused(ctx context.Context, tenantID, id string) error {
	return queryError(s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		var tag cms.Tag
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenantID, id).First(&tag).Error; err != nil {
			return err
		}
		var used int64
		if err := tx.Table("yd_cms_article_tags AS at").
			Joins("JOIN yd_cms_articles AS a ON a.tenant_id = at.tenant_id AND a.id = at.article_id").
			Where("at.tenant_id = ? AND at.tag_id = ? AND a.deleted_at IS NULL", tenantID, id).
			Count(&used).Error; err != nil {
			return err
		}
		if used > 0 {
			return cms.ErrTagInUse
		}
		// Links left by soft-deleted articles would otherwise point at a row that no longer exists. |
		// 软删除文章留下的连接否则会指向已不存在的行。
		if err := tx.Where("tenant_id = ? AND tag_id = ?", tenantID, id).
			Delete(&cms.ArticleTag{}).Error; err != nil {
			return err
		}
		return rowsError(tx.Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&cms.Tag{}))
	}))
}

// CountArticlesOfTag counts non-deleted articles carrying the tag. | CountArticlesOfTag 统计携带该标签的未删除文章数。
func (s *Store) CountArticlesOfTag(ctx context.Context, tenantID, id string) (int64, error) {
	var total int64
	err := s.dbc(ctx).Table("yd_cms_article_tags AS at").
		Joins("JOIN yd_cms_articles AS a ON a.tenant_id = at.tenant_id AND a.id = at.article_id").
		Where("at.tenant_id = ? AND at.tag_id = ? AND a.deleted_at IS NULL", tenantID, id).
		Count(&total).Error
	if err != nil {
		return 0, queryError(err)
	}
	return total, nil
}

// keywordString trims a keyword. | keywordString 裁剪关键词。
func keywordString(value string) string { return strings.TrimSpace(value) }
