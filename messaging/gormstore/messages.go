package gormstore

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/messaging"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateMessage inserts a message with its tags and attachments atomically. | CreateMessage 原子地插入消息及其标签与附件。
func (s *Store) CreateMessage(ctx context.Context, msg messaging.Message, tagIDs, attachmentIDs []string) error {
	return s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMessageTypeTx(tx, msg.TenantID, msg.TypeKey); err != nil {
			return err
		}
		ids, err := lockMessageTagsTx(tx, msg.TenantID, tagIDs)
		if err != nil {
			return err
		}
		if err := tx.Create(&msg).Error; err != nil {
			return createError(err)
		}
		for _, tagID := range ids {
			link := messaging.MessageTag{ID: newRowID("mtg_"), TenantID: msg.TenantID, MessageID: msg.ID, TagID: tagID}
			if err := tx.Clauses(onConflictDoNothing()).Create(&link).Error; err != nil {
				return createError(err)
			}
		}
		if len(attachmentIDs) > 0 {
			result := tx.Model(&messaging.Attachment{}).
				Where("tenant_id = ? AND id IN ? AND uploader_id = ? AND status = ?",
					msg.TenantID, attachmentIDs, msg.SenderID, messaging.AttachmentPending).
				Updates(map[string]any{"message_id": msg.ID, "status": messaging.AttachmentAttached})
			if result.Error != nil {
				return queryError(result.Error)
			}
			if int(result.RowsAffected) != len(attachmentIDs) {
				return errx.Detail(messaging.ErrAttachmentNotFound,
					"expected=%[1]d actual=%[2]d | 期望=%[1]d 实际=%[2]d", len(attachmentIDs), result.RowsAffected)
			}
		}
		if err := tx.Model(&messaging.Conversation{}).
			Where("tenant_id = ? AND id = ?", msg.TenantID, msg.ConversationID).
			Updates(map[string]any{"last_message_at": msg.CreatedAt, "updated_at": msg.CreatedAt}).Error; err != nil {
			return queryError(err)
		}
		// The author has by definition read their own message; advancing the watermark here also keeps
		// the sender's unread counter from counting it. | 作者按定义已读自己的消息；在这里推进水位也避免发送者
		// 的未读计数把它算进去。
		return tx.Model(&messaging.Member{}).
			Where("tenant_id = ? AND conversation_id = ? AND user_id = ? AND (last_read_at IS NULL OR last_read_at < ?)",
				msg.TenantID, msg.ConversationID, msg.SenderID, msg.CreatedAt).
			Update("last_read_at", msg.CreatedAt).Error
	})
}

// GetMessage reads one message. | GetMessage 读取一条消息。
func (s *Store) GetMessage(ctx context.Context, tenantID, id string) (messaging.Message, error) {
	var msg messaging.Message
	if err := s.dbc(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&msg).Error; err != nil {
		return messaging.Message{}, queryError(err)
	}
	return msg, nil
}

// UpdateMessage replaces the editable fields and the tag set. | UpdateMessage 覆盖可编辑字段与标签集合。
func (s *Store) UpdateMessage(ctx context.Context, msg messaging.Message, tagIDs []string) error {
	return s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMessageTypeTx(tx, msg.TenantID, msg.TypeKey); err != nil {
			return err
		}
		ids, err := lockMessageTagsTx(tx, msg.TenantID, tagIDs)
		if err != nil {
			return err
		}
		result := tx.Model(&messaging.Message{}).
			Where("tenant_id = ? AND id = ? AND status <> ?", msg.TenantID, msg.ID, messaging.StatusRecalled).
			Updates(map[string]any{
				"type_key":      msg.TypeKey,
				"type_name":     msg.TypeName,
				"type_name_en":  msg.TypeNameEN,
				"tone":          msg.Tone,
				"title":         msg.Title,
				"body_markdown": msg.BodyMarkdown,
				"body_html":     msg.BodyHTML,
				"body_text":     msg.BodyText,
				"search_text":   msg.SearchText,
				"edited_at":     msg.EditedAt,
				"updated_at":    msg.UpdatedAt,
			})
		if result.Error != nil {
			return queryError(result.Error)
		}
		if result.RowsAffected == 0 {
			return messaging.ErrRecalled
		}
		if err := tx.Where("tenant_id = ? AND message_id = ?", msg.TenantID, msg.ID).
			Delete(&messaging.MessageTag{}).Error; err != nil {
			return queryError(err)
		}
		for _, tagID := range ids {
			link := messaging.MessageTag{ID: newRowID("mtg_"), TenantID: msg.TenantID, MessageID: msg.ID, TagID: tagID}
			if err := tx.Clauses(onConflictDoNothing()).Create(&link).Error; err != nil {
				return createError(err)
			}
		}
		return nil
	})
}

// ListMessages returns one page of messages and the total count. | ListMessages 返回一页消息与总数。
func (s *Store) ListMessages(ctx context.Context, tenantID, userID string, filter messaging.MessageFilter, admin bool) ([]messaging.Message, int, error) {
	query := s.dbc(ctx).Model(&messaging.Message{}).Where("yd_msg_messages.tenant_id = ?", tenantID)

	needMember := !admin || filter.Folder == messaging.FolderUnread
	if needMember {
		query = query.Joins(
			"JOIN yd_msg_members mem ON mem.tenant_id = yd_msg_messages.tenant_id AND mem.conversation_id = yd_msg_messages.conversation_id AND mem.user_id = ?",
			userID,
		)
	}
	if !admin {
		query = query.Where("yd_msg_messages.status = ?", messaging.StatusSent)
	}

	if filter.ConversationID != "" {
		query = query.Where("yd_msg_messages.conversation_id = ?", filter.ConversationID)
	}
	if filter.TypeKey != "" {
		query = query.Where("yd_msg_messages.type_key = ?", filter.TypeKey)
	}
	if filter.SenderID != "" {
		query = query.Where("yd_msg_messages.sender_id = ?", filter.SenderID)
	}
	if filter.Title != "" {
		query = query.Where("LOWER(yd_msg_messages.title) LIKE ? ESCAPE '!'", likePattern(filter.Title))
	}
	if filter.Content != "" {
		query = query.Where("LOWER(yd_msg_messages.body_text) LIKE ? ESCAPE '!'", likePattern(filter.Content))
	}
	if filter.TagID != "" {
		query = query.Where(`EXISTS (
SELECT 1 FROM yd_msg_message_tags mt
WHERE mt.tenant_id = yd_msg_messages.tenant_id
  AND mt.message_id = yd_msg_messages.id
  AND mt.tag_id = ?)`, filter.TagID)
	}
	for _, term := range strings.Fields(filter.Query) {
		query = query.Where("yd_msg_messages.search_text LIKE ? ESCAPE '!'", likePattern(term))
	}

	now := time.Now().UTC()
	switch filter.Folder {
	case messaging.FolderSent:
		query = query.Where("yd_msg_messages.sender_id = ?", userID)
	case messaging.FolderSystem:
		query = query.Where("yd_msg_messages.origin = ?", messaging.OriginSystem)
	case messaging.FolderUnread:
		query = query.Where("yd_msg_messages.sender_id <> ?", userID).
			Where("yd_msg_messages.status = ?", messaging.StatusSent).
			Where("yd_msg_messages.created_at > COALESCE(mem.last_read_at, mem.joined_at)")
	case messaging.FolderPinned:
		query = query.Where("yd_msg_messages.origin = ? AND yd_msg_messages.pinned = ? AND yd_msg_messages.status = ?",
			messaging.OriginSystem, true, messaging.StatusSent).
			Where("(yd_msg_messages.pin_start_at IS NULL OR yd_msg_messages.pin_start_at <= ?)", now).
			Where("(yd_msg_messages.pin_end_at IS NULL OR yd_msg_messages.pin_end_at > ?)", now)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, queryError(err)
	}
	offset := (filter.Page - 1) * filter.PageSize
	if offset < 0 {
		offset = 0
	}
	var out []messaging.Message
	if err := query.Select("yd_msg_messages.*").
		Order("yd_msg_messages.created_at DESC").
		Offset(offset).Limit(filter.PageSize).
		Find(&out).Error; err != nil {
		return nil, 0, queryError(err)
	}
	return out, int(total), nil
}

// ListMessagesByConversation returns one page of a conversation's messages. | ListMessagesByConversation 返回一个会话的一页消息。
func (s *Store) ListMessagesByConversation(ctx context.Context, tenantID, conversationID string, page, pageSize int, includeRecalled bool) ([]messaging.Message, int, error) {
	query := s.dbc(ctx).Model(&messaging.Message{}).
		Where("tenant_id = ? AND conversation_id = ?", tenantID, conversationID)
	if !includeRecalled {
		query = query.Where("status = ?", messaging.StatusSent)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, queryError(err)
	}
	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	var out []messaging.Message
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&out).Error; err != nil {
		return nil, 0, queryError(err)
	}
	// The caller renders a thread oldest-first; the page itself is the newest page. | 调用方按从旧到新渲染线程；
	// 本页取的是最新一页。
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out, int(total), nil
}

// LastMessages resolves the newest visible message per conversation. | LastMessages 解析每个会话最新的可见消息。
func (s *Store) LastMessages(ctx context.Context, tenantID string, conversationIDs []string) (map[string]messaging.Message, error) {
	out := make(map[string]messaging.Message, len(conversationIDs))
	for _, id := range conversationIDs {
		var msg messaging.Message
		err := s.dbc(ctx).
			Where("tenant_id = ? AND conversation_id = ?", tenantID, id).
			Order("created_at DESC").
			First(&msg).Error
		if err == nil {
			out[id] = msg
			continue
		}
		if mapped := notFound(err); mapped != nil {
			continue
		}
		return nil, queryError(err)
	}
	return out, nil
}

// ListPinned lists active pinned system messages visible to one user. | ListPinned 列出某个用户可见的生效置顶系统消息。
func (s *Store) ListPinned(ctx context.Context, tenantID, userID string, now time.Time) ([]messaging.Message, error) {
	var out []messaging.Message
	err := s.dbc(ctx).Model(&messaging.Message{}).
		Joins("JOIN yd_msg_members mem ON mem.tenant_id = yd_msg_messages.tenant_id AND mem.conversation_id = yd_msg_messages.conversation_id AND mem.user_id = ?", userID).
		Where("yd_msg_messages.tenant_id = ? AND yd_msg_messages.origin = ? AND yd_msg_messages.pinned = ? AND yd_msg_messages.status = ?",
			tenantID, messaging.OriginSystem, true, messaging.StatusSent).
		Where("(yd_msg_messages.pin_start_at IS NULL OR yd_msg_messages.pin_start_at <= ?)", now).
		Where("(yd_msg_messages.pin_end_at IS NULL OR yd_msg_messages.pin_end_at > ?)", now).
		Select("yd_msg_messages.*").
		Order("yd_msg_messages.pinned_at DESC, yd_msg_messages.created_at DESC").
		Limit(50).
		Find(&out).Error
	if err != nil {
		return nil, queryError(err)
	}
	return out, nil
}

// RecallMessage advances a message to the tombstone state with its audit entry. | RecallMessage 把消息推进到墓碑状态并写入审计。
func (s *Store) RecallMessage(ctx context.Context, msg messaging.Message, audit messaging.AuditEntry) error {
	return s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&messaging.Message{}).
			Where("tenant_id = ? AND id = ? AND status <> ?", msg.TenantID, msg.ID, messaging.StatusRecalled).
			Updates(map[string]any{
				"status":        msg.Status,
				"recalled_at":   msg.RecalledAt,
				"recalled_by":   msg.RecalledBy,
				"recall_reason": msg.RecallReason,
				"updated_at":    msg.UpdatedAt,
			})
		if result.Error != nil {
			return queryError(result.Error)
		}
		if result.RowsAffected == 0 {
			// A concurrent recall already wrote the tombstone; the caller treats this as idempotent
			// and must not emit a second event or audit row. |
			// 并发撤回已经写入墓碑；调用方按幂等处理，不得再发第二次事件或审计行。
			return messaging.ErrRecalled
		}
		if err := tx.Create(&audit).Error; err != nil {
			return createError(err)
		}
		return nil
	})
}

// SetPin replaces the pin fields with its audit entry. | SetPin 覆盖置顶字段并写入审计。
func (s *Store) SetPin(ctx context.Context, msg messaging.Message, audit messaging.AuditEntry) error {
	return s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&messaging.Message{}).
			Where("tenant_id = ? AND id = ? AND status <> ?", msg.TenantID, msg.ID, messaging.StatusRecalled).
			Updates(map[string]any{
				"pinned":       msg.Pinned,
				"pin_start_at": msg.PinStartAt,
				"pin_end_at":   msg.PinEndAt,
				"pinned_at":    msg.PinnedAt,
				"updated_at":   msg.UpdatedAt,
			})
		if result.Error != nil {
			return queryError(result.Error)
		}
		if result.RowsAffected == 0 {
			return messaging.ErrRecalled
		}
		if err := tx.Create(&audit).Error; err != nil {
			return createError(err)
		}
		return nil
	})
}

// MessageAttachments resolves attachments for a batch of messages. | MessageAttachments 批量解析消息的附件。
func (s *Store) MessageAttachments(ctx context.Context, tenantID string, messageIDs []string) (map[string][]messaging.Attachment, error) {
	out := make(map[string][]messaging.Attachment, len(messageIDs))
	for _, batch := range chunks(messageIDs) {
		var rows []messaging.Attachment
		if err := s.dbc(ctx).
			Where("tenant_id = ? AND message_id IN ? AND status = ?", tenantID, batch, messaging.AttachmentAttached).
			Order("created_at ASC").
			Find(&rows).Error; err != nil {
			return nil, queryError(err)
		}
		for _, row := range rows {
			if row.MessageID == nil {
				continue
			}
			out[*row.MessageID] = append(out[*row.MessageID], row)
		}
	}
	return out, nil
}

// MessageTags resolves groups and tags for a batch of messages. | MessageTags 批量解析消息的分组与标签。
func (s *Store) MessageTags(ctx context.Context, tenantID string, messageIDs []string) (map[string][]messaging.Tag, error) {
	out := make(map[string][]messaging.Tag, len(messageIDs))
	for _, batch := range chunks(messageIDs) {
		var rows []tagRow
		if err := s.dbc(ctx).Raw(`
SELECT mt.message_id AS message_id, t.id AS id, t.tenant_id AS tenant_id, t.kind AS kind,
       t.tag_key AS tag_key, t.name AS name, t.name_en AS name_en, t.tone AS tone,
       t.archived_at AS archived_at, t.created_at AS created_at, t.updated_at AS updated_at
FROM yd_msg_message_tags mt
JOIN yd_msg_tags t ON t.tenant_id = mt.tenant_id AND t.id = mt.tag_id
WHERE mt.tenant_id = ? AND mt.message_id IN ?
ORDER BY t.kind ASC, t.name ASC`, tenantID, batch).Scan(&rows).Error; err != nil {
			return nil, queryError(err)
		}
		for _, row := range rows {
			out[row.MessageID] = append(out[row.MessageID], row.tag())
		}
	}
	return out, nil
}

// tagRow is the flat join row used by MessageTags. | tagRow 是 MessageTags 使用的扁平连接行。
type tagRow struct {
	ArchivedAt *time.Time `gorm:"column:archived_at"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at"`
	MessageID  string     `gorm:"column:message_id"`
	ID         string     `gorm:"column:id"`
	TenantID   string     `gorm:"column:tenant_id"`
	Kind       string     `gorm:"column:kind"`
	Key        string     `gorm:"column:tag_key"`
	Name       string     `gorm:"column:name"`
	NameEN     string     `gorm:"column:name_en"`
	Tone       string     `gorm:"column:tone"`
}

// tag converts the join row into the domain value. | tag 把连接行转成领域值。
func (r tagRow) tag() messaging.Tag {
	return messaging.Tag{
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
		ArchivedAt: r.ArchivedAt,
		ID:         r.ID,
		TenantID:   r.TenantID,
		Kind:       messaging.TagKind(r.Kind),
		Key:        r.Key,
		Name:       r.Name,
		NameEN:     r.NameEN,
		Tone:       r.Tone,
	}
}

// lockMessageTypeTx locks the referenced type row and reports an unknown type. The shared lock
// serialises message writes against a concurrent type deletion. | lockMessageTypeTx 锁定被引用的类型行
// 并报告未知类型。共享锁让消息写入与并发的类型删除互斥。
func lockMessageTypeTx(tx *gorm.DB, tenantID, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	var row messaging.MessageType
	err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id").
		Where("tenant_id = ? AND type_key = ?", tenantID, key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return messaging.ErrTypeUnknown
	}
	if err != nil {
		return queryError(err)
	}
	return nil
}

// lockMessageTagsTx locks the referenced tag rows and reports an unknown tag. Ids are ordered so
// concurrent writers acquire the same locks in the same sequence. | lockMessageTagsTx 锁定被引用的标签行
// 并报告未知标签。ID 会排序，使并发写入者以相同顺序获取锁。
func lockMessageTagsTx(tx *gorm.DB, tenantID string, tagIDs []string) ([]string, error) {
	ids := uniqueSortedIDs(tagIDs)
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []messaging.Tag
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id").
		Where("tenant_id = ? AND id IN ?", tenantID, ids).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, queryError(err)
	}
	if len(rows) != len(ids) {
		return nil, messaging.ErrTagUnknown
	}
	return ids, nil
}

// uniqueSortedIDs trims, deduplicates and orders ids. | uniqueSortedIDs 裁剪、去重并排序 ID。
func uniqueSortedIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
