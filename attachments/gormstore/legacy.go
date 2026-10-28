package gormstore

import (
	"context"
	"time"

	"github.com/shibingli/yd/attachments"
	"github.com/shibingli/ydx/errs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// legacyAttachment is the pre-2026-10 attachment table. MigrateLegacy reads it and DropLegacy
// removes it after the copy. | legacyAttachment 是 2026-10 之前的附件表；MigrateLegacy 读取它，
// DropLegacy 在复制完成后删除它。
type legacyAttachment struct {
	CreatedAt    time.Time `gorm:"column:created_at"`
	MessageID    *string   `gorm:"column:message_id"`
	ID           string    `gorm:"column:id"`
	TenantID     string    `gorm:"column:tenant_id"`
	UploaderID   string    `gorm:"column:uploader_id"`
	OriginalName string    `gorm:"column:original_name"`
	StoredKey    string    `gorm:"column:stored_key"`
	ContentType  string    `gorm:"column:content_type"`
	SHA256       string    `gorm:"column:sha256"`
	Status       string    `gorm:"column:status"`
	Size         int64     `gorm:"column:size"`
}

// TableName pins the legacy table name. | TableName 固定旧表名。
func (legacyAttachment) TableName() string { return "yd_msg_attachments" }

// MigrateLegacy copies rows from the message-centre attachment table into yd_attachments and
// yd_attachment_refs. It is idempotent and only reads the old table; DropLegacy removes it after
// every row has been copied. | MigrateLegacy 把消息中心附件表复制到 yd_attachments 与
// yd_attachment_refs。迁移幂等，只读取旧表；全部行复制完成后由 DropLegacy 删除旧表。
func MigrateLegacy(ctx context.Context, db *gorm.DB) (int, error) {
	if db == nil {
		return 0, attachments.ErrUnavailable
	}
	if !db.WithContext(ctx).Migrator().HasTable(&legacyAttachment{}) {
		return 0, nil
	}
	inserted := 0
	offset := 0
	const batch = 500
	for {
		var rows []legacyAttachment
		if err := db.WithContext(ctx).
			Order("created_at ASC, id ASC").
			Offset(offset).
			Limit(batch).
			Find(&rows).Error; err != nil {
			// Another replica may have dropped the table after copying it. That is the desired
			// end state, not a migration failure. | 另一个副本可能在复制完成后删除了旧表；这是期望的
			// 终态，不是迁移失败。
			if !db.WithContext(ctx).Migrator().HasTable(&legacyAttachment{}) {
				return inserted, nil
			}
			return inserted, errs.Wrap(err, attachments.ErrUnavailable)
		}
		if len(rows) == 0 {
			return inserted, nil
		}
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for _, row := range rows {
				attachment := attachments.Attachment{
					CreatedAt:    row.CreatedAt.UTC(),
					ID:           row.ID,
					TenantID:     row.TenantID,
					UploaderID:   row.UploaderID,
					OriginalName: row.OriginalName,
					StoredKey:    row.StoredKey,
					ContentType:  row.ContentType,
					SHA256:       row.SHA256,
					Status:       attachments.StatusPending,
					Size:         row.Size,
				}
				if row.Status == "attached" && row.MessageID != nil && *row.MessageID != "" {
					attachment.Status = attachments.StatusReferenced
				}
				result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attachment)
				if result.Error != nil {
					return errs.Wrap(result.Error, attachments.ErrUnavailable)
				}
				if result.RowsAffected > 0 {
					inserted++
				}
				if row.MessageID != nil && *row.MessageID != "" {
					binding := attachments.RefBinding{
						CreatedAt:    row.CreatedAt.UTC(),
						TenantID:     row.TenantID,
						AttachmentID: row.ID,
						RefType:      attachments.RefTypeMessage,
						RefID:        *row.MessageID,
					}
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
						return errs.Wrap(err, attachments.ErrUnavailable)
					}
				}
			}
			return nil
		})
		if err != nil {
			return inserted, err
		}
		offset += len(rows)
		if len(rows) < batch {
			return inserted, nil
		}
	}
}

// DropLegacy removes the pre-2026-10 attachment table after MigrateLegacy has copied it. It reports
// whether the table existed. A missing table is a success: a previous replica or an earlier start
// already removed it. | DropLegacy 在 MigrateLegacy 复制完成后删除 2026-10 之前的附件表，并报告表
// 是否存在。表不存在也算成功：此前的副本或启动已经删除过它。
func DropLegacy(ctx context.Context, db *gorm.DB) (bool, error) {
	if db == nil {
		return false, attachments.ErrUnavailable
	}
	if !db.WithContext(ctx).Migrator().HasTable(&legacyAttachment{}) {
		return false, nil
	}
	if err := db.WithContext(ctx).Migrator().DropTable(&legacyAttachment{}); err != nil {
		return false, errs.WrapDetail(err, attachments.ErrUnavailable, "drop yd_msg_attachments | 删除 yd_msg_attachments")
	}
	return true, nil
}
