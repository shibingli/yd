package messaging

import (
	"context"
	"strings"

	"github.com/shibingli/ydx/errs"
)

// noticeKey returns the per-user notice conversation key. | noticeKey 返回每个用户的通知会话键。
func noticeKey(userID string) string { return "notice:" + strings.TrimSpace(userID) }

// NotifyUser writes one system message into the recipient's private notice channel. The recipient
// can read the channel but cannot post into it; the message carries the administrator as its sender
// so the recipient knows who acted. The caller must be a moderator or a system actor: a public
// method that can inject notices must not become a way around the message permissions. Notification
// creation is idempotent per conversation and is safe to call after a committed deletion. |
// NotifyUser 向收件人的私有通知频道写入一条系统消息。收件人可读不可写；消息以管理员为发送者，
// 收件人因此知道是谁执行了操作。调用方必须是管理员或系统主体：可注入通知的公开方法不能成为绕过
// 消息权限的通道。通知会话按用户幂等创建，可在删除提交后安全调用。
func (s *Service) NotifyUser(ctx context.Context, tenantID, recipientID string, actor Actor, title, markdown string) (MessageView, error) {
	tenantID = strings.TrimSpace(tenantID)
	recipientID = strings.TrimSpace(recipientID)
	if tenantID == "" || recipientID == "" || !actor.valid() {
		return MessageView{}, ErrInvalidInput
	}
	if !actor.System && !actor.Manage {
		return MessageView{}, ErrForbidden
	}
	title = truncate(strings.TrimSpace(title), s.options.MaxTitleLen)
	markdown = strings.TrimSpace(markdown)
	if title == "" && markdown == "" {
		return MessageView{}, ErrInvalidInput
	}
	if len(markdown) > s.options.MaxBodyLen {
		return MessageView{}, ErrInvalidInput
	}
	noticeActor := actor
	noticeActor.TenantID = tenantID
	noticeActor.System = true
	if err := s.EnsureTenant(ctx, tenantID); err != nil {
		return MessageView{}, err
	}
	conv, err := s.ensureNoticeConversation(ctx, tenantID, recipientID)
	if err != nil {
		return MessageView{}, err
	}
	members, err := s.store.ListMembers(ctx, tenantID, conv.ID)
	if err != nil {
		return MessageView{}, errs.Wrap(err, ErrUnavailable)
	}
	return s.createMessage(ctx, noticeActor, conv, members, SendInput{
		// The protected system type cannot be archived or deleted by a tenant administrator, so a
		// deletion notice always has a valid catalogue entry. | 受保护的系统类型不会被租户管理员归档或
		// 删除，删除通知因此总有一个有效的目录项。
		TypeKey:  TypeSystem,
		Title:    title,
		Markdown: markdown,
	}, OriginSystem)
}

// ensureNoticeConversation returns the recipient's private notice channel, creating it once. |
// ensureNoticeConversation 返回收件人的私有通知频道，首次调用时创建。
func (s *Service) ensureNoticeConversation(ctx context.Context, tenantID, recipientID string) (Conversation, error) {
	key := noticeKey(recipientID)
	conv, err := s.store.GetNoticeConversation(ctx, tenantID, key)
	if err != nil && !isNotFound(err) {
		return Conversation{}, errs.Wrap(err, ErrUnavailable)
	}
	if isNotFound(err) {
		now := s.options.Now()
		conv = Conversation{
			CreatedAt: now,
			UpdatedAt: now,
			NoticeKey: &key,
			ID:        mustID(s, "cv_"),
			TenantID:  tenantID,
			Kind:      KindNotice,
			CreatedBy: recipientID,
			Status:    ConversationActive,
		}
		member := Member{
			JoinedAt:       now,
			ID:             mustID(s, "mb_"),
			TenantID:       tenantID,
			ConversationID: conv.ID,
			UserID:         recipientID,
			Role:           MemberPlain,
		}
		if createErr := s.store.CreateConversation(ctx, conv, []Member{member}); createErr != nil {
			existing, getErr := s.store.GetNoticeConversation(ctx, tenantID, key)
			if getErr != nil {
				return Conversation{}, errs.Wrap(createErr, ErrUnavailable)
			}
			conv = existing
		}
	}
	if _, err := s.store.GetMember(ctx, tenantID, conv.ID, recipientID); err != nil {
		if !isNotFound(err) {
			return Conversation{}, errs.Wrap(err, ErrUnavailable)
		}
		member := Member{
			JoinedAt:       s.options.Now(),
			ID:             mustID(s, "mb_"),
			TenantID:       tenantID,
			ConversationID: conv.ID,
			UserID:         recipientID,
			Role:           MemberPlain,
		}
		if err := s.store.AddMember(ctx, member); err != nil {
			return Conversation{}, errs.Wrap(err, ErrUnavailable)
		}
	}
	return conv, nil
}
