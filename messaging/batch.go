// This file declares the optional batch capabilities of Store: the domain service prefers them to remove
// per-row queries and falls back to row-by-row reads when a store does not implement them.
// 本文件声明 Store 的可选批量能力：领域服务优先使用它们消除逐条查询，未实现时回退到逐条读取。

package messaging

import "context"

// conversationMemberBatch is the optional Store capability that resolves members for many conversations at once.
// conversationMemberBatch 是 Store 的可选能力：一次解析多个会话的成员。
type conversationMemberBatch interface {
	// ListMembersByConversation returns the members of every requested conversation, keyed by conversation id.
	// | ListMembersByConversation 返回每个请求会话的成员，以会话 ID 为键。
	ListMembersByConversation(ctx context.Context, tenantID string, conversationIDs []string) (map[string][]Member, error)
}

// conversationBatch is the optional Store capability that resolves many conversations by id at once.
// conversationBatch 是 Store 的可选能力：按 ID 一次解析多个会话。
type conversationBatch interface {
	// GetConversations returns the conversations found for the requested ids, keyed by id.
	// | GetConversations 返回请求 ID 中实际存在的会话，以 ID 为键。
	GetConversations(ctx context.Context, tenantID string, ids []string) (map[string]Conversation, error)
}

// conversationMessagePage is the optional Store capability that reads one page of a conversation without counting
// the whole conversation. | conversationMessagePage 是 Store 的可选能力：读取会话的一页消息，但不统计会话总行数。
type conversationMessagePage interface {
	// ListMessagesByConversationPage returns the newest page oldest-first. | ListMessagesByConversationPage 返回最新一页，按从旧到新排列。
	ListMessagesByConversationPage(ctx context.Context, tenantID, conversationID string, page, pageSize int, includeRecalled bool) ([]Message, error)
}

// conversationMessageAfter is the optional Store capability that reads messages newer than one anchor. |
// conversationMessageAfter 是 Store 的可选能力：读取锚点消息之后的新消息。
type conversationMessageAfter interface {
	// ListMessagesAfter returns messages newer than afterID, oldest first, inside one tenant and conversation. |
	// ListMessagesAfter 返回同一租户、同一会话内晚于 afterID 的消息，按从旧到新排列。
	ListMessagesAfter(ctx context.Context, tenantID, conversationID, afterID string, limit int) ([]Message, error)
}

// friendNoteBatch is the optional Store capability that resolves many friend notes at once.
// friendNoteBatch 是 Store 的可选能力：一次解析多条好友备注。
type friendNoteBatch interface {
	// FriendNotes returns the owner's remarks keyed by friend id; friends without a remark are absent.
	// | FriendNotes 返回以好友 ID 为键的备注；没有备注的好友不出现在结果中。
	FriendNotes(ctx context.Context, tenantID, ownerID string, friendIDs []string) (map[string]FriendNote, error)
}

// membersByConversation loads members through the batch capability when available. |
// membersByConversation 在批量能力可用时批量加载成员。
func (s *Service) membersByConversation(ctx context.Context, tenantID string, conversationIDs []string) (map[string][]Member, error) {
	if len(conversationIDs) == 0 {
		return map[string][]Member{}, nil
	}
	if batch, ok := s.store.(conversationMemberBatch); ok {
		out, err := batch.ListMembersByConversation(ctx, tenantID, conversationIDs)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	out := make(map[string][]Member, len(conversationIDs))
	for _, id := range conversationIDs {
		members, err := s.store.ListMembers(ctx, tenantID, id)
		if err != nil {
			return nil, err
		}
		out[id] = members
	}
	return out, nil
}

// conversationsByID loads conversations through the batch capability when available. |
// conversationsByID 在批量能力可用时批量加载会话。
func (s *Service) conversationsByID(ctx context.Context, tenantID string, ids []string) (map[string]Conversation, error) {
	if len(ids) == 0 {
		return map[string]Conversation{}, nil
	}
	if batch, ok := s.store.(conversationBatch); ok {
		return batch.GetConversations(ctx, tenantID, ids)
	}
	out := make(map[string]Conversation, len(ids))
	for _, id := range ids {
		conv, err := s.store.GetConversation(ctx, tenantID, id)
		if err != nil {
			continue
		}
		out[id] = conv
	}
	return out, nil
}

// conversationMessages loads one page of a conversation without paying for a total count the thread view never
// renders. | conversationMessages 读取会话的一页消息，不执行线程视图根本用不到的总数统计。
func (s *Service) conversationMessages(ctx context.Context, tenantID, conversationID string, page, pageSize int, includeRecalled bool) ([]Message, error) {
	if lister, ok := s.store.(conversationMessagePage); ok {
		return lister.ListMessagesByConversationPage(ctx, tenantID, conversationID, page, pageSize, includeRecalled)
	}
	messages, _, err := s.store.ListMessagesByConversation(ctx, tenantID, conversationID, page, pageSize, includeRecalled)
	return messages, err
}

// friendNotes loads friend remarks through the batch capability when available. |
// friendNotes 在批量能力可用时批量加载好友备注。
func (s *Service) friendNotes(ctx context.Context, tenantID, ownerID string, friendIDs []string) (map[string]FriendNote, error) {
	if len(friendIDs) == 0 {
		return map[string]FriendNote{}, nil
	}
	if batch, ok := s.store.(friendNoteBatch); ok {
		return batch.FriendNotes(ctx, tenantID, ownerID, friendIDs)
	}
	out := make(map[string]FriendNote, len(friendIDs))
	for _, friendID := range friendIDs {
		note, err := s.store.GetFriendNote(ctx, tenantID, ownerID, friendID)
		if err == nil {
			out[friendID] = note
			continue
		}
		if !isNotFound(err) {
			return nil, err
		}
	}
	return out, nil
}
