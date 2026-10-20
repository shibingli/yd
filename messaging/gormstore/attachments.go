package gormstore

import (
	"context"
	"time"

	"github.com/shibingli/yd/messaging"
	"gorm.io/gorm"
)

// CreateAttachment stores one pending upload. | CreateAttachment 保存一个待挂载上传。
func (s *Store) CreateAttachment(ctx context.Context, a messaging.Attachment) error {
	if err := s.dbc(ctx).Create(&a).Error; err != nil {
		return createError(err)
	}
	return nil
}

// GetAttachment reads one attachment. | GetAttachment 读取一个附件。
func (s *Store) GetAttachment(ctx context.Context, tenantID, id string) (messaging.Attachment, error) {
	var a messaging.Attachment
	if err := s.dbc(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&a).Error; err != nil {
		return messaging.Attachment{}, queryError(err)
	}
	return a, nil
}

// CleanupPending removes pending attachments older than the cutoff. | CleanupPending 删除早于截止时间的待挂载附件。
func (s *Store) CleanupPending(ctx context.Context, before time.Time) ([]messaging.Attachment, error) {
	var stale []messaging.Attachment
	err := s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Where("status = ? AND created_at < ?", messaging.AttachmentPending, before).
			Order("created_at ASC").
			Limit(500).
			Find(&stale).Error; err != nil {
			return queryError(err)
		}
		if len(stale) == 0 {
			return nil
		}
		ids := make([]string, 0, len(stale))
		for _, item := range stale {
			ids = append(ids, item.ID)
		}
		if err := tx.
			Where("status = ? AND id IN ?", messaging.AttachmentPending, ids).
			Delete(&messaging.Attachment{}).Error; err != nil {
			return queryError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stale, nil
}
