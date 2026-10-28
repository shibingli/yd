package gormstore

import (
	"context"

	"github.com/shibingli/yd/attachments"
	"github.com/shibingli/yd/messaging"
)

// MessageAttachments resolves attachments for a batch of messages through the shared reference
// table, including tombstones so the card can render a deletion tombstone. | MessageAttachments 经共享
// 引用表批量解析消息的附件，并包含墓碑，使卡片能渲染删除占位。
func (s *Store) MessageAttachments(ctx context.Context, tenantID string, messageIDs []string) (map[string][]messaging.Attachment, error) {
	out := make(map[string][]messaging.Attachment, len(messageIDs))
	type row struct {
		RefID string `gorm:"column:ref_id"`
		messaging.Attachment
	}
	for _, batch := range chunks(messageIDs) {
		var rows []row
		if err := s.dbc(ctx).
			Model(&messaging.Attachment{}).
			Select("yd_attachments.*, ref.ref_id AS ref_id").
			Joins("JOIN yd_attachment_refs ref ON ref.tenant_id = yd_attachments.tenant_id AND ref.attachment_id = yd_attachments.id").
			Where("yd_attachments.tenant_id = ? AND ref.ref_type = ? AND ref.ref_id IN ?", tenantID, attachments.RefTypeMessage, batch).
			Order("yd_attachments.created_at ASC, yd_attachments.id ASC").
			Scan(&rows).Error; err != nil {
			return nil, queryError(err)
		}
		for _, item := range rows {
			out[item.RefID] = append(out[item.RefID], item.Attachment)
		}
	}
	return out, nil
}
