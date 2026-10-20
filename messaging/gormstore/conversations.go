package gormstore

import (
	"context"
	"time"

	"github.com/shibingli/yd/messaging"
	"gorm.io/gorm"
)

// CreateConversation inserts a conversation and its members atomically. | CreateConversation 原子地插入会话与成员。
func (s *Store) CreateConversation(ctx context.Context, conv messaging.Conversation, members []messaging.Member) error {
	return s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
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

// UpdateConversation replaces the mutable conversation fields. | UpdateConversation 覆盖会话的可变字段。
func (s *Store) UpdateConversation(ctx context.Context, conv messaging.Conversation) error {
	result := s.dbc(ctx).Model(&messaging.Conversation{}).
		Where("tenant_id = ? AND id = ?", conv.TenantID, conv.ID).
		Updates(map[string]any{
			"title":      conv.Title,
			"status":     conv.Status,
			"updated_at": conv.UpdatedAt,
		})
	return queryError(result.Error)
}

// AddMember inserts a membership; an existing membership is left untouched. | AddMember 插入成员关系；已存在时保持原样。
func (s *Store) AddMember(ctx context.Context, m messaging.Member) error {
	if err := s.dbc(ctx).Clauses(onConflictDoNothing()).Create(&m).Error; err != nil {
		return createError(err)
	}
	return nil
}

// RemoveMember deletes one membership. | RemoveMember 删除一条成员关系。
func (s *Store) RemoveMember(ctx context.Context, tenantID, conversationID, userID string) error {
	result := s.dbc(ctx).
		Where("tenant_id = ? AND conversation_id = ? AND user_id = ?", tenantID, conversationID, userID).
		Delete(&messaging.Member{})
	return rowsError(result)
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

// AdvanceRead moves a member's read watermark forward, never backward. | AdvanceRead 单调推进成员的已读水位，绝不回退。
func (s *Store) AdvanceRead(ctx context.Context, tenantID, conversationID, userID string, at time.Time) error {
	return s.dbc(ctx).Model(&messaging.Member{}).
		Where("tenant_id = ? AND conversation_id = ? AND user_id = ? AND (last_read_at IS NULL OR last_read_at < ?)",
			tenantID, conversationID, userID, at).
		Update("last_read_at", at).Error
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
