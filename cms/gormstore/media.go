package gormstore

import (
	"context"
	"strings"

	"github.com/shibingli/yd/cms"
	"gorm.io/gorm"
)

// CreateMedia inserts a media record. | CreateMedia 插入一条媒体记录。
func (s *Store) CreateMedia(ctx context.Context, tenantID string, media cms.Media) error {
	media.TenantID = tenantID
	return queryError(s.dbc(ctx).Create(&media).Error)
}

// GetMedia returns one media record. | GetMedia 返回一条媒体记录。
func (s *Store) GetMedia(ctx context.Context, tenantID, id string) (cms.Media, error) {
	var row cms.Media
	if err := s.dbc(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&row).Error; err != nil {
		return cms.Media{}, queryError(err)
	}
	return row, nil
}

// ListMedia returns a page of media. | ListMedia 返回一页媒体。
func (s *Store) ListMedia(ctx context.Context, tenantID string, filter cms.MediaFilter) (cms.MediaPage, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	query := s.dbc(ctx).Model(&cms.Media{}).Where("tenant_id = ?", tenantID)
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := likePattern(keyword)
		query = query.Where("(LOWER(original_name) LIKE ? ESCAPE '!' OR LOWER(alt) LIKE ? ESCAPE '!')", pattern, pattern)
	}
	if contentType := strings.TrimSpace(filter.ContentType); contentType != "" {
		query = query.Where("content_type LIKE ? ESCAPE '!'", likePrefix(strings.ToLower(contentType)))
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return cms.MediaPage{}, queryError(err)
	}
	var rows []cms.Media
	if err := query.Session(&gorm.Session{}).Order("created_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return cms.MediaPage{}, queryError(err)
	}
	return cms.MediaPage{Items: rows, Total: total, Page: page, PageSize: pageSize}, nil
}

// DeleteMedia removes a media record. | DeleteMedia 删除一条媒体记录。
func (s *Store) DeleteMedia(ctx context.Context, tenantID, id string) error {
	return rowsError(s.dbc(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&cms.Media{}))
}
