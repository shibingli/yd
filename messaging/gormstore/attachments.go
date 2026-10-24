package gormstore

import (
	"context"
	"time"

	"github.com/shibingli/yd/messaging"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// CleanupPending removes pending attachments older than the cutoff. It returns only rows this call
// actually deleted: a concurrent message may bind a row between the select and the delete, and
// deleting that row's bytes would leave a live message with a missing file. |
// CleanupPending 删除早于截止时间的待挂载附件，只返回本次调用确实删除的行：并发发送消息可能在查询与删除
// 之间挂载某一行，删除该行的字节会让一条仍然可见的消息失去文件。
func (s *Store) CleanupPending(ctx context.Context, before time.Time) ([]messaging.Attachment, error) {
	var stale []messaging.Attachment
	err := s.transaction(ctx, []string{"yd_msg_attachments"}, func(tx *gorm.DB) error {
		// Lock the candidates so a message cannot bind them until this transaction commits. |
		// 锁定候选行，使消息在事务提交前无法挂载它们。
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
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
		// A row that survived the guarded delete was bound (or removed) concurrently; its bytes are
		// still owned by that newer state, so keep it out of the deletion list. |
		// 在带状态条件的删除中存活的记录已被并发挂载（或移除），其字节属于更新的状态，必须从删除列表中排除。
		var surviving []string
		if err := tx.Model(&messaging.Attachment{}).Where("id IN ?", ids).Pluck("id", &surviving).Error; err != nil {
			return queryError(err)
		}
		alive := make(map[string]struct{}, len(surviving))
		for _, id := range surviving {
			alive[id] = struct{}{}
		}
		deleted := stale[:0]
		for _, item := range stale {
			if _, ok := alive[item.ID]; ok {
				continue
			}
			deleted = append(deleted, item)
		}
		stale = deleted
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stale, nil
}
