// Package gormstore persists attachments and their references with GORM. | Package gormstore 使用 GORM
// 持久化附件及其引用。
package gormstore

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/shibingli/yd/attachments"
	"github.com/shibingli/ydx/errs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store is the GORM attachment store. | Store 是 GORM 附件存储。
type Store struct {
	// db is the shared connection pool. | db 是共享连接池。
	db *gorm.DB
}

// New returns a store over db. | New 返回基于 db 的存储。
func New(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, attachments.ErrUnavailable
	}
	return &Store{db: db}, nil
}

// DB returns the underlying handle for callers that need a shared transaction. | DB 返回底层句柄，供需要
// 共享事务的调用方使用。
func (s *Store) DB() *gorm.DB { return s.db }

// Migrate creates the attachment tables and indexes. | Migrate 创建附件表与索引。
func Migrate(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return attachments.ErrUnavailable
	}
	if err := db.WithContext(ctx).AutoMigrate(&attachments.Attachment{}, &attachments.RefBinding{}); err != nil {
		return errs.Wrap(err, attachments.ErrUnavailable)
	}
	return nil
}

// Create inserts one attachment. | Create 插入一条附件。
func (s *Store) Create(ctx context.Context, attachment attachments.Attachment) error {
	if err := s.db.WithContext(ctx).Create(&attachment).Error; err != nil {
		return createError(err)
	}
	return nil
}

// Get reads one attachment inside the tenant. | Get 读取租户内的一条附件。
func (s *Store) Get(ctx context.Context, tenantID, id string) (attachments.Attachment, error) {
	var attachment attachments.Attachment
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, strings.TrimSpace(id)).
		First(&attachment).Error; err != nil {
		return attachments.Attachment{}, queryError(err)
	}
	return attachment, nil
}

// Refs lists the references of one attachment. | Refs 列出一个附件的引用。
func (s *Store) Refs(ctx context.Context, tenantID, attachmentID string) ([]attachments.Ref, error) {
	var rows []attachments.RefBinding
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND attachment_id = ?", tenantID, attachmentID).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, queryError(err)
	}
	refs := make([]attachments.Ref, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, attachments.Ref{Type: row.RefType, ID: row.RefID})
	}
	return refs, nil
}

// ListByRef returns the attachments of one business object, including tombstones. | ListByRef 返回一个业务
// 对象的附件，包含墓碑。
func (s *Store) ListByRef(ctx context.Context, tenantID string, ref attachments.Ref) ([]attachments.Attachment, error) {
	type row struct {
		RefID string `gorm:"column:ref_id"`
		attachments.Attachment
	}
	var rows []row
	if err := s.db.WithContext(ctx).
		Model(&attachments.Attachment{}).
		Select("yd_attachments.*, ref.ref_id AS ref_id").
		Joins("JOIN yd_attachment_refs ref ON ref.tenant_id = yd_attachments.tenant_id AND ref.attachment_id = yd_attachments.id").
		Where("yd_attachments.tenant_id = ? AND ref.ref_type = ? AND ref.ref_id = ?", tenantID, ref.Type, ref.ID).
		Order("yd_attachments.created_at ASC, yd_attachments.id ASC").
		Scan(&rows).Error; err != nil {
		return nil, queryError(err)
	}
	out := make([]attachments.Attachment, 0, len(rows))
	for _, item := range rows {
		item.RefCount = 1
		out = append(out, item.Attachment)
	}
	return out, nil
}

// Bind adds references and marks the attachments referenced, atomically. | Bind 原子地添加引用并把附件
// 标记为已引用。
func (s *Store) Bind(ctx context.Context, tenantID string, attachmentIDs []string, ref attachments.Ref, uploaderID string, at time.Time) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked []attachments.Attachment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id IN ?", tenantID, attachmentIDs).
			Find(&locked).Error; err != nil {
			return queryError(err)
		}
		if len(locked) != len(attachmentIDs) {
			return attachments.ErrNotFound
		}
		for _, attachment := range locked {
			if attachment.Status == attachments.StatusDeleted ||
				(uploaderID != "" && attachment.UploaderID != uploaderID) {
				return attachments.ErrNotFound
			}
		}
		for _, id := range attachmentIDs {
			binding := attachments.RefBinding{
				CreatedAt:    at,
				TenantID:     tenantID,
				AttachmentID: id,
				RefType:      ref.Type,
				RefID:        ref.ID,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
				return createError(err)
			}
		}
		if err := tx.Model(&attachments.Attachment{}).
			Where("tenant_id = ? AND id IN ? AND status <> ?", tenantID, attachmentIDs, attachments.StatusDeleted).
			Updates(map[string]any{"status": attachments.StatusReferenced, "released_at": nil}).Error; err != nil {
			return queryError(err)
		}
		return nil
	})
}

// Release removes every reference of one business object and returns attachments with no remaining
// references to pending. | Release 删除一个业务对象的全部引用，并把不再有引用的附件退回 pending。
func (s *Store) Release(ctx context.Context, tenantID string, ref attachments.Ref, at time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Model(&attachments.RefBinding{}).
			Where("tenant_id = ? AND ref_type = ? AND ref_id = ?", tenantID, ref.Type, ref.ID).
			Pluck("attachment_id", &ids).Error; err != nil {
			return queryError(err)
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Where("tenant_id = ? AND ref_type = ? AND ref_id = ?", tenantID, ref.Type, ref.ID).
			Delete(&attachments.RefBinding{}).Error; err != nil {
			return queryError(err)
		}
		return tx.Model(&attachments.Attachment{}).
			Where("tenant_id = ? AND id IN ? AND status = ? AND NOT EXISTS (SELECT 1 FROM yd_attachment_refs r WHERE r.tenant_id = yd_attachments.tenant_id AND r.attachment_id = yd_attachments.id)",
				tenantID, ids, attachments.StatusReferenced).
			Updates(map[string]any{"status": attachments.StatusPending, "released_at": at}).Error
	})
}

// MarkDeleted turns one record into a tombstone. | MarkDeleted 把一条记录变成墓碑。
func (s *Store) MarkDeleted(ctx context.Context, tenantID, id string, at time.Time, by, reason string, requireNoRefs bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var attachment attachments.Attachment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenantID, id).
			First(&attachment).Error; err != nil {
			return queryError(err)
		}
		if attachment.Status == attachments.StatusDeleted {
			return attachments.ErrNotFound
		}
		if requireNoRefs {
			var count int64
			if err := tx.Model(&attachments.RefBinding{}).
				Where("tenant_id = ? AND attachment_id = ?", tenantID, id).
				Count(&count).Error; err != nil {
				return queryError(err)
			}
			if count > 0 {
				return attachments.ErrReferenced
			}
		}
		if err := tx.Model(&attachments.Attachment{}).
			Where("tenant_id = ? AND id = ? AND status <> ?", tenantID, id, attachments.StatusDeleted).
			Updates(map[string]any{
				"status":        attachments.StatusDeleted,
				"deleted_at":    at,
				"deleted_by":    by,
				"delete_reason": reason,
			}).Error; err != nil {
			return queryError(err)
		}
		return nil
	})
}

// DeleteUnreferenced tombstones one batch of attachments with no references. | DeleteUnreferenced 把一批
// 无引用附件变成墓碑。
func (s *Store) DeleteUnreferenced(ctx context.Context, tenantID string, filter attachments.Filter, at time.Time, by, reason string, batch int) ([]attachments.Attachment, error) {
	if batch <= 0 {
		batch = 200
	}
	var deleted []attachments.Attachment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&attachments.Attachment{}).
			Where("yd_attachments.tenant_id = ? AND yd_attachments.status <> ?", tenantID, attachments.StatusDeleted).
			Where("NOT EXISTS (SELECT 1 FROM yd_attachment_refs r WHERE r.tenant_id = yd_attachments.tenant_id AND r.attachment_id = yd_attachments.id)")
		query = applyFilter(query, filter)
		if err := query.Clauses(clause.Locking{Strength: "UPDATE"}).
			Order("yd_attachments.created_at ASC, yd_attachments.id ASC").
			Limit(batch).
			Find(&deleted).Error; err != nil {
			return queryError(err)
		}
		if len(deleted) == 0 {
			return nil
		}
		ids := make([]string, 0, len(deleted))
		for _, item := range deleted {
			ids = append(ids, item.ID)
		}
		if err := tx.Model(&attachments.Attachment{}).
			Where("tenant_id = ? AND id IN ? AND status <> ? AND NOT EXISTS (SELECT 1 FROM yd_attachment_refs r WHERE r.tenant_id = yd_attachments.tenant_id AND r.attachment_id = yd_attachments.id)",
				tenantID, ids, attachments.StatusDeleted).
			Updates(map[string]any{
				"status":        attachments.StatusDeleted,
				"deleted_at":    at,
				"deleted_by":    by,
				"delete_reason": reason,
			}).Error; err != nil {
			return queryError(err)
		}
		// Only rows that actually reached the tombstone state may lose their bytes; a candidate
		// that gained a reference before the guarded update stays live. | 只有真正进入墓碑状态的行
		// 才能删除字节；在带条件更新前获得引用的候选行仍然存活。
		deleted = deleted[:0]
		if err := tx.Where("tenant_id = ? AND id IN ? AND status = ?", tenantID, ids, attachments.StatusDeleted).
			Order("created_at ASC").Find(&deleted).Error; err != nil {
			return queryError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}

// DeleteExpiredPending tombstones pending uploads older than before. | DeleteExpiredPending 把早于 before
// 的待引用上传变成墓碑。
func (s *Store) DeleteExpiredPending(ctx context.Context, tenantID string, before, at time.Time, reason string, batch int) ([]attachments.Attachment, error) {
	if batch <= 0 {
		batch = 200
	}
	var deleted []attachments.Attachment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&attachments.Attachment{}).
			Where("tenant_id = ? AND status = ? AND COALESCE(released_at, created_at) < ?", tenantID, attachments.StatusPending, before).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Order("COALESCE(released_at, created_at) ASC").
			Limit(batch).
			Find(&deleted).Error; err != nil {
			return queryError(err)
		}
		if len(deleted) == 0 {
			return nil
		}
		ids := make([]string, 0, len(deleted))
		for _, item := range deleted {
			ids = append(ids, item.ID)
		}
		if err := tx.Model(&attachments.Attachment{}).
			Where("tenant_id = ? AND id IN ? AND status = ?", tenantID, ids, attachments.StatusPending).
			Updates(map[string]any{
				"status":        attachments.StatusDeleted,
				"deleted_at":    at,
				"deleted_by":    "system",
				"delete_reason": reason,
			}).Error; err != nil {
			return queryError(err)
		}
		deleted = deleted[:0]
		if err := tx.Where("tenant_id = ? AND id IN ? AND status = ?", tenantID, ids, attachments.StatusDeleted).
			Order("created_at ASC").Find(&deleted).Error; err != nil {
			return queryError(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}

// DeleteExpiredPendingAll tombstones expired pending uploads across every tenant. | DeleteExpiredPendingAll
// 把跨租户的过期待引用上传变成墓碑。
func (s *Store) DeleteExpiredPendingAll(ctx context.Context, before, at time.Time, reason string, batch int) ([]attachments.Attachment, error) {
	if batch <= 0 {
		batch = 200
	}
	var deleted []attachments.Attachment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&attachments.Attachment{}).
			Where("status = ? AND COALESCE(released_at, created_at) < ?", attachments.StatusPending, before).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Order("COALESCE(released_at, created_at) ASC").
			Limit(batch).
			Find(&deleted).Error; err != nil {
			return queryError(err)
		}
		if len(deleted) == 0 {
			return nil
		}
		ids := make([]string, 0, len(deleted))
		for _, item := range deleted {
			ids = append(ids, item.ID)
		}
		if err := tx.Model(&attachments.Attachment{}).
			Where("id IN ? AND status = ?", ids, attachments.StatusPending).
			Updates(map[string]any{
				"status":        attachments.StatusDeleted,
				"deleted_at":    at,
				"deleted_by":    "system",
				"delete_reason": reason,
			}).Error; err != nil {
			return queryError(err)
		}
		deleted = deleted[:0]
		return tx.Where("id IN ? AND status = ?", ids, attachments.StatusDeleted).
			Order("created_at ASC").Find(&deleted).Error
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}

// List returns one page and its total. | List 返回一页与总数。
func (s *Store) List(ctx context.Context, tenantID string, filter attachments.Filter, ownerID string, all bool) ([]attachments.Attachment, int, error) {
	page, pageSize := filter.Page, filter.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	query := s.db.WithContext(ctx).Model(&attachments.Attachment{}).Where("yd_attachments.tenant_id = ?", tenantID)
	if !all {
		query = query.Where("yd_attachments.uploader_id = ?", ownerID)
	}
	query = applyFilter(query, filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, queryError(err)
	}
	var items []attachments.Attachment
	if err := query.Order("yd_attachments.created_at DESC, yd_attachments.id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&items).Error; err != nil {
		return nil, 0, queryError(err)
	}
	if err := s.fillRefCounts(ctx, tenantID, items); err != nil {
		return nil, 0, err
	}
	return items, int(total), nil
}

// fillRefCounts adds the reference count to a page. | fillRefCounts 为整页补充引用数。
func (s *Store) fillRefCounts(ctx context.Context, tenantID string, items []attachments.Attachment) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	type countRow struct {
		AttachmentID string `gorm:"column:attachment_id"`
		Total        int    `gorm:"column:total"`
	}
	var rows []countRow
	if err := s.db.WithContext(ctx).Model(&attachments.RefBinding{}).
		Select("attachment_id, COUNT(*) AS total").
		Where("tenant_id = ? AND attachment_id IN ?", tenantID, ids).
		Group("attachment_id").
		Scan(&rows).Error; err != nil {
		return queryError(err)
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.AttachmentID] = row.Total
	}
	for i := range items {
		items[i].RefCount = counts[items[i].ID]
	}
	return nil
}

// applyFilter adds the shared filter predicates. | applyFilter 添加共享筛选条件。
func applyFilter(query *gorm.DB, filter attachments.Filter) *gorm.DB {
	if q := strings.TrimSpace(filter.Query); q != "" {
		pattern := "%" + strings.ToLower(escapeLike(q)) + "%"
		query = query.Where("LOWER(yd_attachments.original_name) LIKE ? ESCAPE '!'", pattern)
	}
	if uploader := strings.TrimSpace(filter.UploaderID); uploader != "" {
		query = query.Where("yd_attachments.uploader_id = ?", uploader)
	}
	switch {
	case filter.Status == attachments.StatusDeleted:
		query = query.Where("yd_attachments.status = ?", attachments.StatusDeleted)
	case filter.Status != "":
		query = query.Where("yd_attachments.status = ?", filter.Status)
	case !filter.IncludeDeleted:
		query = query.Where("yd_attachments.status <> ?", attachments.StatusDeleted)
	}
	if filter.Referenced != nil {
		sub := "SELECT 1 FROM yd_attachment_refs r WHERE r.tenant_id = yd_attachments.tenant_id AND r.attachment_id = yd_attachments.id"
		if *filter.Referenced {
			query = query.Where("EXISTS (" + sub + ")")
		} else {
			query = query.Where("NOT EXISTS (" + sub + ")")
		}
	}
	if refType := strings.TrimSpace(filter.RefType); refType != "" {
		sub := "SELECT 1 FROM yd_attachment_refs r WHERE r.tenant_id = yd_attachments.tenant_id AND r.attachment_id = yd_attachments.id AND r.ref_type = ?"
		args := []any{refType}
		if refID := strings.TrimSpace(filter.RefID); refID != "" {
			sub += " AND r.ref_id = ?"
			args = append(args, refID)
		}
		query = query.Where("EXISTS ("+sub+")", args...)
	}
	if filter.From != nil {
		query = query.Where("yd_attachments.created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("yd_attachments.created_at <= ?", *filter.To)
	}
	return query
}

// escapeLike escapes the LIKE metacharacters with "!", the escape character every dialect of this
// repository accepts. A backslash is a string-literal escape in MySQL, where ESCAPE '\' silently
// breaks the clause; the message store already uses "!" for the same reason. |
// escapeLike 用 "!" 转义 LIKE 元字符；这是本仓所有方言都接受的转义字符。反斜杠在 MySQL 里是字符串
// 字面量转义符，ESCAPE '\' 会让子句静默失效；消息存储出于同样理由使用 "!"。
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	return strings.ReplaceAll(value, "_", "!_")
}

// queryError maps GORM errors onto attachment sentinels. | queryError 把 GORM 错误映射为附件哨兵。
func queryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return attachments.ErrNotFound
	}
	return errs.Wrap(err, attachments.ErrUnavailable)
}

// createError maps a write failure. | createError 映射写入失败。
func createError(err error) error {
	if err == nil {
		return nil
	}
	return errs.Wrap(err, attachments.ErrUnavailable)
}
