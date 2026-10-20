package gormstore

import (
	"context"

	"github.com/shibingli/yd/messaging"
)

// AddAudit appends one audit entry. | AddAudit 追加一条审计记录。
func (s *Store) AddAudit(ctx context.Context, entry messaging.AuditEntry) error {
	if err := s.dbc(ctx).Create(&entry).Error; err != nil {
		return createError(err)
	}
	return nil
}

// ListAudit returns one page of audit entries. | ListAudit 返回一页审计记录。
func (s *Store) ListAudit(ctx context.Context, tenantID string, filter messaging.AuditFilter) ([]messaging.AuditEntry, int, error) {
	query := s.dbc(ctx).Model(&messaging.AuditEntry{}).Where("tenant_id = ?", tenantID)
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.MessageID != "" {
		query = query.Where("message_id = ?", filter.MessageID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, queryError(err)
	}
	offset := (filter.Page - 1) * filter.PageSize
	if offset < 0 {
		offset = 0
	}
	var out []messaging.AuditEntry
	if err := query.Order("created_at DESC").Offset(offset).Limit(filter.PageSize).Find(&out).Error; err != nil {
		return nil, 0, queryError(err)
	}
	return out, int(total), nil
}
