package gormstore

import (
	"context"

	"github.com/shibingli/yd/cms"
	"gorm.io/gorm/clause"
)

// GetSettings returns the tenant settings. | GetSettings 返回租户设置。
func (s *Store) GetSettings(ctx context.Context, tenantID string) (cms.Settings, error) {
	var row cms.Settings
	if err := s.dbc(ctx).Where("tenant_id = ?", tenantID).First(&row).Error; err != nil {
		return cms.Settings{}, queryError(err)
	}
	return row, nil
}

// SaveSettings upserts the tenant settings. | SaveSettings 插入或更新租户设置。
func (s *Store) SaveSettings(ctx context.Context, tenantID string, settings cms.Settings) error {
	settings.TenantID = tenantID
	// The insert carries every field (including false switches); the conflict branch updates the same set,
	// so a disabled switch can never be silently replaced by a column default. |
	// 插入携带全部字段（含 false 开关）；冲突分支更新同一组字段，因此关闭的开关不会被列默认值悄悄替换。
	return queryError(s.dbc(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"site_name", "site_description", "site_keywords", "footer_text",
			"posts_per_page", "comments_enabled", "comment_moderation", "rss_enabled", "updated_at",
		}),
	}).Create(&settings).Error)
}
