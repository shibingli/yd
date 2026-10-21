package gormstore

import (
	"context"

	"github.com/shibingli/yd/cms"
	"gorm.io/gorm"
)

// AddAudit appends one audit entry. | AddAudit 追加一条审计记录。
func (s *Store) AddAudit(ctx context.Context, tenantID string, entry cms.AuditEntry) error {
	entry.TenantID = tenantID
	return queryError(s.dbc(ctx).Create(&entry).Error)
}

// ListAudit returns a page of audit entries. | ListAudit 返回一页审计记录。
func (s *Store) ListAudit(ctx context.Context, tenantID string, filter cms.AuditFilter) (cms.AuditPage, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	query := s.dbc(ctx).Model(&cms.AuditEntry{}).Where("tenant_id = ?", tenantID)
	if filter.TargetID != "" {
		query = query.Where("target_id = ?", filter.TargetID)
	}
	if filter.ActorID != "" {
		query = query.Where("actor_id = ?", filter.ActorID)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return cms.AuditPage{}, queryError(err)
	}
	var rows []cms.AuditEntry
	if err := query.Session(&gorm.Session{}).Order("created_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return cms.AuditPage{}, queryError(err)
	}
	return cms.AuditPage{Items: rows, Total: total, Page: page, PageSize: pageSize}, nil
}
