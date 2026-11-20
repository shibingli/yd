package gormstore

import (
	"context"
	"time"

	"github.com/shibingli/yd/messaging"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateConversation inserts a conversation and its members atomically. | CreateConversation 原子地插入会话与成员。
func (s *Store) CreateConversation(ctx context.Context, conv messaging.Conversation, members []messaging.Member) error {
	return s.transaction(ctx, []string{"yd_msg_conversations", "yd_msg_members"}, func(tx *gorm.DB) error {
		if err := tx.Create(&conv).Error; err != nil {
			return createError(err)
		}
		for _, member := range members {
			if member.ConversationID == "" {
				member.ConversationID = conv.ID
			}
			if member.TenantID == "" {
				member.TenantID = conv.TenantID
			}
			if err := tx.Create(&member).Error; err != nil {
				return createError(err)
			}
		}
		return nil
	})
}

// GetConversation reads one conversation. | GetConversation 读取一个会话。
func (s *Store) GetConversation(ctx context.Context, tenantID, id string) (messaging.Conversation, error) {
	var conv messaging.Conversation
	if err := s.dbc(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&conv).Error; err != nil {
		return messaging.Conversation{}, queryError(err)
	}
	return conv, nil
}

// GetDirectConversation resolves the canonical direct conversation of two users. | GetDirectConversation 解析两个用户的规范单聊。
func (s *Store) GetDirectConversation(ctx context.Context, tenantID, userA, userB string) (messaging.Conversation, error) {
	low, high := orderedPair(userA, userB)
	key := low + ":" + high
	var conv messaging.Conversation
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND direct_key = ?", tenantID, key).
		First(&conv).Error; err != nil {
		return messaging.Conversation{}, queryError(err)
	}
	return conv, nil
}

// GetSystemConversation resolves the tenant announcement channel. | GetSystemConversation 解析租户公告频道。
func (s *Store) GetSystemConversation(ctx context.Context, tenantID string) (messaging.Conversation, error) {
	var conv messaging.Conversation
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND kind = ?", tenantID, messaging.KindSystem).
		First(&conv).Error; err != nil {
		return messaging.Conversation{}, queryError(err)
	}
	return conv, nil
}

// GetNoticeConversation resolves one user's private notice channel. | GetNoticeConversation 解析某个用户的
// 私有通知频道。
func (s *Store) GetNoticeConversation(ctx context.Context, tenantID, noticeKey string) (messaging.Conversation, error) {
	var conv messaging.Conversation
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND notice_key = ?", tenantID, noticeKey).
		First(&conv).Error; err != nil {
		return messaging.Conversation{}, queryError(err)
	}
	return conv, nil
}

// ListConversations lists the conversations of one user. | ListConversations 列出某个用户的会话。
func (s *Store) ListConversations(ctx context.Context, tenantID, userID string) ([]messaging.Conversation, error) {
	var out []messaging.Conversation
	if err := s.dbc(ctx).Model(&messaging.Conversation{}).
		Joins("JOIN yd_msg_members ON yd_msg_members.tenant_id = yd_msg_conversations.tenant_id AND yd_msg_members.conversation_id = yd_msg_conversations.id").
		Where("yd_msg_conversations.tenant_id = ? AND yd_msg_members.user_id = ?", tenantID, userID).
		Order("COALESCE(yd_msg_conversations.last_message_at, yd_msg_conversations.created_at) DESC").
		Find(&out).Error; err != nil {
		return nil, queryError(err)
	}
	return out, nil
}

// GetConversations resolves many conversations by id in one query per batch. |
// GetConversations 每个批次用一条查询按 ID 解析多个会话。
func (s *Store) GetConversations(ctx context.Context, tenantID string, ids []string) (map[string]messaging.Conversation, error) {
	out := make(map[string]messaging.Conversation, len(ids))
	for _, batch := range chunks(ids) {
		var rows []messaging.Conversation
		if err := s.dbc(ctx).
			Where("tenant_id = ? AND id IN ?", tenantID, batch).
			Find(&rows).Error; err != nil {
			return nil, queryError(err)
		}
		for _, row := range rows {
			out[row.ID] = row
		}
	}
	return out, nil
}

// ListAllConversations lists every conversation of a tenant. | ListAllConversations 列出租户的全部会话。
func (s *Store) ListAllConversations(ctx context.Context, tenantID string) ([]messaging.Conversation, error) {
	var out []messaging.Conversation
	if err := s.dbc(ctx).Where("tenant_id = ?", tenantID).
		Order("COALESCE(last_message_at, created_at) DESC").
		Find(&out).Error; err != nil {
		return nil, queryError(err)
	}
	return out, nil
}

// UpdateConversation replaces the mutable conversation fields. The row is locked for the whole update so
// a dissolve or rename cannot interleave with a message insert that is checking the conversation state. |
// UpdateConversation 覆盖会话的可变字段。整个更新期间锁定该行，解散或改名因此无法与正在检查会话状态的
// 消息插入交错。
func (s *Store) UpdateConversation(ctx context.Context, conv messaging.Conversation) error {
	return s.transaction(ctx, []string{"yd_msg_conversations"}, func(tx *gorm.DB) error {
		var current messaging.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").
			Where("tenant_id = ? AND id = ?", conv.TenantID, conv.ID).Take(&current).Error; err != nil {
			return queryError(err)
		}
		return queryError(tx.Model(&messaging.Conversation{}).
			Where("tenant_id = ? AND id = ?", conv.TenantID, conv.ID).
			Updates(map[string]any{
				"title":      conv.Title,
				"status":     conv.Status,
				"updated_at": conv.UpdatedAt,
			}).Error)
	})
}

// AddMember inserts a membership; an existing membership is left untouched. | AddMember 插入成员关系；已存在时保持原样。
func (s *Store) AddMember(ctx context.Context, m messaging.Member) error {
	if err := s.dbc(ctx).Clauses(onConflictDoNothing()).Create(&m).Error; err != nil {
		return createError(err)
	}
	return nil
}

// AddMemberBounded inserts a membership under the conversation row lock and enforces the member
// ceiling inside the same transaction, so concurrent adds cannot overshoot MaxMembers. It reports
// whether a row was inserted; an existing membership is idempotent success. |
// AddMemberBounded 在会话行锁内插入成员关系，并在同一事务里执行成员上限，因此并发添加无法突破
// MaxMembers。它报告是否真正插入了一行；已存在的成员关系按幂等成功处理。
func (s *Store) AddMemberBounded(ctx context.Context, m messaging.Member, maxMembers int) (bool, error) {
	inserted := false
	err := s.transaction(ctx, []string{"yd_msg_conversations", "yd_msg_members"}, func(tx *gorm.DB) error {
		var conv messaging.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "status").
			Where("tenant_id = ? AND id = ?", m.TenantID, m.ConversationID).Take(&conv).Error; err != nil {
			return queryError(err)
		}
		if conv.Status != messaging.ConversationActive {
			return messaging.ErrConversationClosed
		}
		var existing int64
		if err := tx.Model(&messaging.Member{}).
			Where("tenant_id = ? AND conversation_id = ? AND user_id = ?", m.TenantID, m.ConversationID, m.UserID).
			Count(&existing).Error; err != nil {
			return queryError(err)
		}
		if existing > 0 {
			return nil
		}
		var total int64
		if err := tx.Model(&messaging.Member{}).
			Where("tenant_id = ? AND conversation_id = ?", m.TenantID, m.ConversationID).
			Count(&total).Error; err != nil {
			return queryError(err)
		}
		if maxMembers > 0 && total+1 > int64(maxMembers) {
			return messaging.ErrTooManyMembers
		}
		if err := tx.Clauses(onConflictDoNothing()).Create(&m).Error; err != nil {
			return createError(err)
		}
		inserted = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return inserted, nil
}

// RemoveMember deletes one membership under the conversation row lock, so the removal cannot interleave
// with a message insert that has already re-checked the sender's membership. |
// RemoveMember 在会话行锁内删除一条成员关系，因此移除操作无法与已经复查过发送者成员关系的消息插入交错。
func (s *Store) RemoveMember(ctx context.Context, tenantID, conversationID, userID string) error {
	return s.transaction(ctx, []string{"yd_msg_conversations", "yd_msg_members"}, func(tx *gorm.DB) error {
		var conv messaging.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").
			Where("tenant_id = ? AND id = ?", tenantID, conversationID).Take(&conv).Error; err != nil {
			return queryError(err)
		}
		return rowsError(tx.
			Where("tenant_id = ? AND conversation_id = ? AND user_id = ?", tenantID, conversationID, userID).
			Delete(&messaging.Member{}))
	})
}

// GetMember reads one membership. | GetMember 读取一条成员关系。
func (s *Store) GetMember(ctx context.Context, tenantID, conversationID, userID string) (messaging.Member, error) {
	var m messaging.Member
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND conversation_id = ? AND user_id = ?", tenantID, conversationID, userID).
		First(&m).Error; err != nil {
		return messaging.Member{}, queryError(err)
	}
	return m, nil
}

// ListMembers lists every membership of a conversation. | ListMembers 列出会话的全部成员关系。
func (s *Store) ListMembers(ctx context.Context, tenantID, conversationID string) ([]messaging.Member, error) {
	var out []messaging.Member
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND conversation_id = ?", tenantID, conversationID).
		Order("joined_at ASC").
		Find(&out).Error; err != nil {
		return nil, queryError(err)
	}
	return out, nil
}

// ListMembersByConversation resolves the members of many conversations in one query per batch. |
// ListMembersByConversation 每个批次用一条查询解析多个会话的成员。
func (s *Store) ListMembersByConversation(ctx context.Context, tenantID string, conversationIDs []string) (map[string][]messaging.Member, error) {
	out := make(map[string][]messaging.Member, len(conversationIDs))
	for _, batch := range chunks(conversationIDs) {
		var rows []messaging.Member
		if err := s.dbc(ctx).
			Where("tenant_id = ? AND conversation_id IN ?", tenantID, batch).
			Order("joined_at ASC").
			Find(&rows).Error; err != nil {
			return nil, queryError(err)
		}
		for _, row := range rows {
			out[row.ConversationID] = append(out[row.ConversationID], row)
		}
	}
	return out, nil
}

// AdvanceRead moves a member's read watermark forward, never backward. | AdvanceRead 单调推进成员的已读水位，绝不回退。
func (s *Store) AdvanceRead(ctx context.Context, tenantID, conversationID, userID string, at time.Time) error {
	return s.dbc(ctx).Model(&messaging.Member{}).
		Where("tenant_id = ? AND conversation_id = ? AND user_id = ? AND (last_read_at IS NULL OR last_read_at < ?)",
			tenantID, conversationID, userID, at).
		Update("last_read_at", at).Error
}

// AdvanceReadToLatest moves a member's read watermark to the newest committed sent message. The
// conversation row is locked first, which serialises the read with message inserts: a message either
// commits before the maximum is taken and is included, or waits and stays newer than the new watermark.
// A conversation without messages leaves the watermark untouched. The single UPDATE is monotonic and
// never moves the watermark backwards. It returns the effective watermark and whether it moved. |
// AdvanceReadToLatest 把成员的已读水位推进到最新已提交的 sent 消息。它先锁定会话行，使本次读取与消息
// 插入串行：消息要么在取最大值之前提交并被包含，要么等待且仍新于新水位。没有消息的会话保持原水位。
// 单条 UPDATE 单调推进，水位绝不回退。它返回生效水位与是否发生推进。
func (s *Store) AdvanceReadToLatest(ctx context.Context, tenantID, conversationID, userID string) (time.Time, bool, error) {
	var watermark time.Time
	moved := false
	err := s.transaction(ctx, []string{"yd_msg_members"}, func(tx *gorm.DB) error {
		var conv messaging.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").
			Where("tenant_id = ? AND id = ?", tenantID, conversationID).Take(&conv).Error; err != nil {
			return queryError(err)
		}
		// 单条 UPDATE 同时完成「取最新创建时间」与「单调推进」；GORM 的 Update 回调因此仍然触发，
		// 未绑定 Invalidator 时查询缓存同样会被写回调失效。
		// The single UPDATE both takes the newest creation time and advances monotonically; using the
		// GORM Update callback keeps the query-cache write hook working when no Invalidator is attached.
		maxWatermark := "(SELECT MAX(m.created_at) FROM yd_msg_messages m WHERE m.tenant_id = ? AND m.conversation_id = ? AND m.status = ?)"
		status := string(messaging.StatusSent)
		result := tx.Model(&messaging.Member{}).
			Where("tenant_id = ? AND conversation_id = ? AND user_id = ?", tenantID, conversationID, userID).
			Where("(last_read_at IS NULL OR last_read_at < "+maxWatermark+")", tenantID, conversationID, status).
			Where("EXISTS (SELECT 1 FROM yd_msg_messages m WHERE m.tenant_id = ? AND m.conversation_id = ? AND m.status = ?)",
				tenantID, conversationID, status).
			Update("last_read_at", gorm.Expr(maxWatermark, tenantID, conversationID, status))
		if result.Error != nil {
			return queryError(result.Error)
		}
		moved = result.RowsAffected > 0
		var member messaging.Member
		if err := tx.Select("last_read_at").
			Where("tenant_id = ? AND conversation_id = ? AND user_id = ?", tenantID, conversationID, userID).
			Take(&member).Error; err != nil {
			return queryError(err)
		}
		if member.LastReadAt != nil {
			watermark = *member.LastReadAt
		}
		return nil
	})
	if err != nil {
		return time.Time{}, false, err
	}
	return watermark, moved, nil
}

// ConversationUnread returns the unread count per conversation for one user. | ConversationUnread 返回某个用户每个会话的未读数。
func (s *Store) ConversationUnread(ctx context.Context, tenantID, userID string) (map[string]int, error) {
	type row struct {
		ConversationID string
		Count          int
	}
	var rows []row
	err := s.dbc(ctx).Raw(`
SELECT m.conversation_id AS conversation_id, COUNT(*) AS count
FROM yd_msg_messages m
JOIN yd_msg_members mem
  ON mem.tenant_id = m.tenant_id
 AND mem.conversation_id = m.conversation_id
 AND mem.user_id = ?
WHERE m.tenant_id = ?
  AND m.status = ?
  AND m.sender_id <> ?
  AND ((mem.last_read_at IS NOT NULL AND m.created_at > mem.last_read_at)
       OR (mem.last_read_at IS NULL AND m.created_at >= mem.joined_at))
GROUP BY m.conversation_id`,
		// 已读语义与 ListMessages 的 FolderUnread 完全一致：等于水位视为已读，尚无水位时以加入时间为基线。
		// The read semantics match FolderUnread in ListMessages exactly: equal to the watermark is read, and
		// without a watermark the join time is the baseline.
		userID, tenantID, string(messaging.StatusSent), userID).Scan(&rows).Error
	if err != nil {
		return nil, queryError(err)
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.ConversationID] = r.Count
	}
	return out, nil
}
