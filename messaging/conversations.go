package messaging

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/shibingli/ydx/errs"
)

// Conversations returns the conversation list of the actor with unread counts and last messages. |
// Conversations 返回主体的会话列表，包含未读数与最后一条消息。
func (s *Service) Conversations(ctx context.Context, actor Actor) ([]ConversationView, error) {
	if !actor.valid() {
		return nil, ErrInvalidInput
	}
	if err := s.EnsureTenant(ctx, actor.TenantID); err != nil {
		return nil, err
	}
	if _, err := s.ensureSystemConversation(ctx, actor); err != nil {
		return nil, err
	}
	list, err := s.store.ListConversations(ctx, actor.TenantID, actor.ID)
	if err != nil {
		return nil, errs.Wrap(err, ErrUnavailable)
	}
	unread, err := s.store.ConversationUnread(ctx, actor.TenantID, actor.ID)
	if err != nil {
		return nil, errs.Wrap(err, ErrUnavailable)
	}
	ids := make([]string, 0, len(list))
	for _, conv := range list {
		ids = append(ids, conv.ID)
	}
	last, err := s.store.LastMessages(ctx, actor.TenantID, ids)
	if err != nil {
		return nil, errs.Wrap(err, ErrUnavailable)
	}

	out := make([]ConversationView, 0, len(list))
	for _, conv := range list {
		members, err := s.store.ListMembers(ctx, actor.TenantID, conv.ID)
		if err != nil {
			return nil, errs.Wrap(err, ErrUnavailable)
		}
		memberIDs := make([]string, 0, len(members))
		for _, member := range members {
			memberIDs = append(memberIDs, member.UserID)
		}
		profiles, err := s.directory.Profiles(ctx, actor.TenantID, memberIDs)
		if err != nil {
			return nil, errs.Wrap(err, ErrUnavailable)
		}
		view := ConversationView{Conversation: conv, Unread: unread[conv.ID]}
		for _, id := range memberIDs {
			if profile, ok := profiles[id]; ok {
				view.Members = append(view.Members, profile)
				if conv.Kind == KindDirect && id != actor.ID {
					peer := profile
					view.Peer = &peer
				}
			}
		}
		if msg, ok := last[conv.ID]; ok {
			if msg.Recalled() && !actor.Manage {
				msg.BodyMarkdown, msg.BodyHTML, msg.BodyText = "", "", ""
			}
			view.LastMessage = &msg
		}
		out = append(out, view)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, right := conversationTime(out[i]), conversationTime(out[j])
		return left.After(right)
	})
	return out, nil
}

// conversationTime returns the ordering timestamp of a conversation. | conversationTime 返回会话的排序时间戳。
func conversationTime(v ConversationView) time.Time {
	if v.LastMessageAt != nil {
		return *v.LastMessageAt
	}
	return v.CreatedAt
}

// Conversation returns one conversation, its newest page of messages and marks it read. | Conversation 返回一个会话及其最新一页消息，并标记已读。
func (s *Service) Conversation(ctx context.Context, actor Actor, conversationID string) (ConversationView, []MessageView, error) {
	if !actor.valid() {
		return ConversationView{}, nil, ErrInvalidInput
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ConversationView{}, nil, ErrNotFound
	}
	conv, err := s.store.GetConversation(ctx, actor.TenantID, conversationID)
	if err != nil {
		return ConversationView{}, nil, mapStoreError(err, ErrNotFound)
	}
	members, err := s.store.ListMembers(ctx, actor.TenantID, conv.ID)
	if err != nil {
		return ConversationView{}, nil, errs.Wrap(err, ErrUnavailable)
	}
	if !containsMember(members, actor.ID) && !actor.Manage {
		return ConversationView{}, nil, ErrNotFound
	}

	// 打开会话即推进已读水位；管理员旁路查看不推进，避免替成员“读掉”消息。
	// 只有确实存在未读时才推进并广播：否则一次线程刷新会触发一条已读事件，对方的刷新又触发下一条，
	// 两个客户端就会互相来回推送。
	// Opening a conversation advances the watermark; an administrator's moderation view does not, so it never marks
	// messages read on a member's behalf. The watermark only moves and only emits when there really are unread
	// messages: otherwise one refresh emits a read event, the peer's refresh emits the next one, and two clients
	// ping-pong forever.
	if containsMember(members, actor.ID) {
		unreadBefore, err := s.store.ConversationUnread(ctx, actor.TenantID, actor.ID)
		if err != nil {
			return ConversationView{}, nil, errs.Wrap(err, ErrUnavailable)
		}
		if unreadBefore[conv.ID] > 0 {
			now := s.options.Now()
			if err := s.store.AdvanceRead(ctx, actor.TenantID, conv.ID, actor.ID, now); err != nil {
				return ConversationView{}, nil, errs.Wrap(err, ErrUnavailable)
			}
			for _, member := range members {
				if member.UserID == actor.ID {
					member.LastReadAt = &now
				}
			}
			// 受众包含读者本人：读者自己的 WebSocket 连接也要据此刷新未读角标，否则角标会一直停留在旧值。
			// The audience includes the reader: their own WebSocket connection needs the event to refresh the unread
			// badge, otherwise the badge keeps the previous count until a page reload.
			s.emit(ctx, Event{Type: EventMessageRead, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{"at": now.UTC().Format(time.RFC3339)}})
		}
	}

	page := s.options.PageSize
	if page < 50 {
		page = 50
	}
	// A recalled message stays in the thread as a tombstone; the view layer strips its body for
	// readers without moderation rights. | 撤回的消息以墓碑形式留在会话里；视图层会为无管理权的读者
	// 剥掉正文。
	messages, _, err := s.store.ListMessagesByConversation(ctx, actor.TenantID, conv.ID, 1, page, true)
	if err != nil {
		return ConversationView{}, nil, errs.Wrap(err, ErrUnavailable)
	}
	views, err := s.messageViews(ctx, actor, messages, members, true)
	if err != nil {
		return ConversationView{}, nil, err
	}

	memberIDs := make([]string, 0, len(members))
	for _, member := range members {
		memberIDs = append(memberIDs, member.UserID)
	}
	profiles, err := s.directory.Profiles(ctx, actor.TenantID, memberIDs)
	if err != nil {
		return ConversationView{}, nil, errs.Wrap(err, ErrUnavailable)
	}
	view := ConversationView{Conversation: conv}
	for _, id := range memberIDs {
		if profile, ok := profiles[id]; ok {
			view.Members = append(view.Members, profile)
			if conv.Kind == KindDirect && id != actor.ID {
				peer := profile
				view.Peer = &peer
			}
		}
	}
	unread, err := s.store.ConversationUnread(ctx, actor.TenantID, actor.ID)
	if err != nil {
		return ConversationView{}, nil, errs.Wrap(err, ErrUnavailable)
	}
	view.Unread = unread[conv.ID]
	if len(views) > 0 {
		last := views[len(views)-1].Message
		view.LastMessage = &last
	}
	return view, views, nil
}

// DirectConversation returns the canonical direct conversation with a friend, creating it once. |
// DirectConversation 返回与好友的规范单聊，首次调用时创建。
func (s *Service) DirectConversation(ctx context.Context, actor Actor, friendID string) (Conversation, error) {
	if !actor.valid() {
		return Conversation{}, ErrInvalidInput
	}
	friendID = strings.TrimSpace(friendID)
	if err := s.requireFriend(ctx, actor.TenantID, actor.ID, friendID); err != nil {
		return Conversation{}, err
	}
	if conv, err := s.store.GetDirectConversation(ctx, actor.TenantID, actor.ID, friendID); err == nil {
		if conv.Status == ConversationDissolved {
			return Conversation{}, ErrConversationClosed
		}
		return conv, nil
	} else if !isNotFound(err) {
		return Conversation{}, errs.Wrap(err, ErrUnavailable)
	}
	now := s.options.Now()
	id, err := s.options.NewID("cv_")
	if err != nil {
		return Conversation{}, err
	}
	low, high := canonicalPair(actor.ID, friendID)
	key := low + ":" + high
	conv := Conversation{
		CreatedAt: now,
		UpdatedAt: now,
		DirectKey: &key,
		ID:        id,
		TenantID:  actor.TenantID,
		Kind:      KindDirect,
		CreatedBy: actor.ID,
		Status:    ConversationActive,
	}
	members := []Member{
		{JoinedAt: now, ID: mustID(s, "mb_"), TenantID: actor.TenantID, ConversationID: id, UserID: actor.ID, Role: MemberPlain},
		{JoinedAt: now, ID: mustID(s, "mb_"), TenantID: actor.TenantID, ConversationID: id, UserID: friendID, Role: MemberPlain},
	}
	if err := s.store.CreateConversation(ctx, conv, members); err != nil {
		// 并发创建时唯一索引冲突意味着另一个请求刚刚建好同一个会话：读回它，而不是把冲突抛给用户。
		// A unique conflict on concurrent creation means the other request just created the same conversation: read it
		// back instead of surfacing the conflict.
		if existing, getErr := s.store.GetDirectConversation(ctx, actor.TenantID, actor.ID, friendID); getErr == nil {
			return existing, nil
		}
		return Conversation{}, errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: id, ActorID: actor.ID, Audience: []string{friendID}})
	return conv, nil
}

// CreateGroup creates a group from a set of the actor's friends. | CreateGroup 从主体的一组好友创建群。
func (s *Service) CreateGroup(ctx context.Context, actor Actor, title string, memberIDs []string) (Conversation, error) {
	if !actor.valid() {
		return Conversation{}, ErrInvalidInput
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return Conversation{}, ErrInvalidInput
	}
	title = truncate(title, s.options.MaxTitleLen)
	memberIDs = uniqueIDs(memberIDs, actor.ID)
	if len(memberIDs) == 0 {
		return Conversation{}, ErrInvalidInput
	}
	if len(memberIDs)+1 > s.options.MaxMembers {
		return Conversation{}, ErrTooManyMembers
	}
	for _, id := range memberIDs {
		if _, err := s.requireActiveUser(ctx, actor.TenantID, id); err != nil {
			return Conversation{}, err
		}
		if err := s.requireFriend(ctx, actor.TenantID, actor.ID, id); err != nil {
			return Conversation{}, err
		}
	}
	now := s.options.Now()
	id, err := s.options.NewID("cv_")
	if err != nil {
		return Conversation{}, err
	}
	conv := Conversation{
		CreatedAt: now,
		UpdatedAt: now,
		ID:        id,
		TenantID:  actor.TenantID,
		Kind:      KindGroup,
		Title:     title,
		CreatedBy: actor.ID,
		Status:    ConversationActive,
	}
	members := make([]Member, 0, len(memberIDs)+1)
	members = append(members, Member{JoinedAt: now, ID: mustID(s, "mb_"), TenantID: actor.TenantID, ConversationID: id, UserID: actor.ID, Role: MemberOwner})
	for _, memberID := range memberIDs {
		members = append(members, Member{JoinedAt: now, ID: mustID(s, "mb_"), TenantID: actor.TenantID, ConversationID: id, UserID: memberID, Role: MemberPlain})
	}
	if err := s.store.CreateConversation(ctx, conv, members); err != nil {
		return Conversation{}, errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: id, ActorID: actor.ID, Audience: memberIDs, Detail: map[string]string{"action": "created"}})
	return conv, nil
}

// AddGroupMember adds one friend to a group the actor owns. | AddGroupMember 把一位好友加入主体拥有的群。
func (s *Service) AddGroupMember(ctx context.Context, actor Actor, conversationID, memberID string) error {
	conv, actorMember, err := s.groupForChange(ctx, actor, conversationID)
	if err != nil {
		return err
	}
	if actorMember.Role != MemberOwner && !actor.Manage {
		return ErrForbidden
	}
	memberID = strings.TrimSpace(memberID)
	if err := s.requireFriend(ctx, actor.TenantID, actor.ID, memberID); err != nil {
		return err
	}
	members, err := s.store.ListMembers(ctx, actor.TenantID, conv.ID)
	if err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	if containsMember(members, memberID) {
		return nil
	}
	if len(members)+1 > s.options.MaxMembers {
		return ErrTooManyMembers
	}
	member := Member{
		JoinedAt:       s.options.Now(),
		ID:             mustID(s, "mb_"),
		TenantID:       actor.TenantID,
		ConversationID: conv.ID,
		UserID:         memberID,
		Role:           MemberPlain,
	}
	if err := s.store.AddMember(ctx, member); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{"action": "member_added"}})
	return nil
}

// RemoveGroupMember removes one member from a group the actor owns. | RemoveGroupMember 从主体拥有的群移除一位成员。
func (s *Service) RemoveGroupMember(ctx context.Context, actor Actor, conversationID, memberID string) error {
	conv, actorMember, err := s.groupForChange(ctx, actor, conversationID)
	if err != nil {
		return err
	}
	if actorMember.Role != MemberOwner && !actor.Manage {
		return ErrForbidden
	}
	if memberID == actor.ID {
		return ErrInvalidInput
	}
	if err := s.store.RemoveMember(ctx, actor.TenantID, conv.ID, memberID); err != nil {
		return mapStoreError(err, ErrNotFound)
	}
	members, _ := s.store.ListMembers(ctx, actor.TenantID, conv.ID)
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: append(memberIDsOf(members), memberID), Detail: map[string]string{"action": "member_removed"}})
	return nil
}

// LeaveGroup removes the actor from a group. The owner has to dissolve the group instead. |
// LeaveGroup 让主体退出群。群主必须改为解散群。
func (s *Service) LeaveGroup(ctx context.Context, actor Actor, conversationID string) error {
	conv, member, err := s.groupForChange(ctx, actor, conversationID)
	if err != nil {
		return err
	}
	if member.Role == MemberOwner {
		return errs.Detail(ErrForbidden, "the owner must dissolve the group | 群主必须解散群")
	}
	if err := s.store.RemoveMember(ctx, actor.TenantID, conv.ID, actor.ID); err != nil {
		return mapStoreError(err, ErrNotFound)
	}
	members, _ := s.store.ListMembers(ctx, actor.TenantID, conv.ID)
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{"action": "member_left"}})
	return nil
}

// RenameGroup changes a group title. | RenameGroup 修改群名。
func (s *Service) RenameGroup(ctx context.Context, actor Actor, conversationID, title string) error {
	conv, member, err := s.groupForChange(ctx, actor, conversationID)
	if err != nil {
		return err
	}
	if member.Role != MemberOwner && !actor.Manage {
		return ErrForbidden
	}
	title = truncate(strings.TrimSpace(title), s.options.MaxTitleLen)
	if title == "" {
		return ErrInvalidInput
	}
	now := s.options.Now()
	conv.Title = title
	conv.UpdatedAt = now
	if err := s.store.UpdateConversation(ctx, conv); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	members, _ := s.store.ListMembers(ctx, actor.TenantID, conv.ID)
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{"action": "renamed"}})
	return nil
}

// DissolveGroup closes a group; only its owner or a moderator may do it. | DissolveGroup 关闭群；仅群主或管理员可执行。
func (s *Service) DissolveGroup(ctx context.Context, actor Actor, conversationID string) error {
	conv, member, err := s.groupForChange(ctx, actor, conversationID)
	if err != nil {
		return err
	}
	if member.Role != MemberOwner && !actor.Manage {
		return ErrForbidden
	}
	members, _ := s.store.ListMembers(ctx, actor.TenantID, conv.ID)
	conv.Status = ConversationDissolved
	conv.UpdatedAt = s.options.Now()
	if err := s.store.UpdateConversation(ctx, conv); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{"action": "dissolved"}})
	return nil
}

// groupForChange resolves a group and the actor's membership. | groupForChange 解析群与主体成员关系。
func (s *Service) groupForChange(ctx context.Context, actor Actor, conversationID string) (Conversation, Member, error) {
	if !actor.valid() {
		return Conversation{}, Member{}, ErrInvalidInput
	}
	conv, err := s.store.GetConversation(ctx, actor.TenantID, strings.TrimSpace(conversationID))
	if err != nil {
		return Conversation{}, Member{}, mapStoreError(err, ErrNotFound)
	}
	if conv.Kind != KindGroup {
		return Conversation{}, Member{}, ErrInvalidInput
	}
	if conv.Status == ConversationDissolved {
		return Conversation{}, Member{}, ErrConversationClosed
	}
	member, err := s.store.GetMember(ctx, actor.TenantID, conv.ID, actor.ID)
	if err != nil {
		if actor.Manage {
			member = Member{UserID: actor.ID, Role: MemberPlain}
		} else {
			return Conversation{}, Member{}, ErrNotMember
		}
	}
	return conv, member, nil
}

// MarkRead advances the actor's read watermark in a conversation. | MarkRead 推进主体在会话中的已读水位。
func (s *Service) MarkRead(ctx context.Context, actor Actor, conversationID string) error {
	if !actor.valid() {
		return ErrInvalidInput
	}
	members, err := s.store.ListMembers(ctx, actor.TenantID, strings.TrimSpace(conversationID))
	if err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	if !containsMember(members, actor.ID) {
		return ErrNotFound
	}
	unread, err := s.store.ConversationUnread(ctx, actor.TenantID, actor.ID)
	if err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	if unread[conversationID] == 0 {
		return nil
	}
	now := s.options.Now()
	if err := s.store.AdvanceRead(ctx, actor.TenantID, conversationID, actor.ID, now); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	// 与打开会话一致：读者本人也在受众里，自己的 WebSocket 才能立即刷新未读角标。
	// As with opening a conversation, the reader is part of the audience so their own WebSocket refreshes the badge
	// immediately.
	s.emit(ctx, Event{Type: EventMessageRead, TenantID: actor.TenantID, ConversationID: conversationID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{"at": now.UTC().Format(time.RFC3339)}})
	return nil
}

// PinnedMessages returns the active pinned system messages visible to the actor. | PinnedMessages 返回主体可见的生效置顶系统消息。
func (s *Service) PinnedMessages(ctx context.Context, actor Actor) ([]MessageView, error) {
	if !actor.valid() {
		return nil, ErrInvalidInput
	}
	if _, err := s.ensureSystemConversation(ctx, actor); err != nil {
		return nil, err
	}
	messages, err := s.store.ListPinned(ctx, actor.TenantID, actor.ID, s.options.Now())
	if err != nil {
		return nil, errs.Wrap(err, ErrUnavailable)
	}
	return s.messageViews(ctx, actor, messages, nil, false)
}

// ensureSystemConversation returns the announcement channel and joins the actor once. | ensureSystemConversation 返回公告频道，并把主体加入一次。
func (s *Service) ensureSystemConversation(ctx context.Context, actor Actor) (Conversation, error) {
	conv, err := s.store.GetSystemConversation(ctx, actor.TenantID)
	if err != nil && !isNotFound(err) {
		return Conversation{}, errs.Wrap(err, ErrUnavailable)
	}
	if isNotFound(err) {
		now := s.options.Now()
		key := s.options.SystemConversationKey
		conv = Conversation{
			CreatedAt: now,
			UpdatedAt: now,
			SystemKey: &key,
			ID:        mustID(s, "cv_"),
			TenantID:  actor.TenantID,
			Kind:      KindSystem,
			CreatedBy: actor.ID,
			Status:    ConversationActive,
		}
		member := Member{JoinedAt: now, ID: mustID(s, "mb_"), TenantID: actor.TenantID, ConversationID: conv.ID, UserID: actor.ID, Role: MemberPlain}
		if createErr := s.store.CreateConversation(ctx, conv, []Member{member}); createErr != nil {
			existing, getErr := s.store.GetSystemConversation(ctx, actor.TenantID)
			if getErr != nil {
				return Conversation{}, errs.Wrap(createErr, ErrUnavailable)
			}
			conv = existing
		}
	}
	if _, err := s.store.GetMember(ctx, actor.TenantID, conv.ID, actor.ID); err != nil {
		if !isNotFound(err) {
			return Conversation{}, errs.Wrap(err, ErrUnavailable)
		}
		member := Member{
			JoinedAt:       s.options.Now(),
			ID:             mustID(s, "mb_"),
			TenantID:       actor.TenantID,
			ConversationID: conv.ID,
			UserID:         actor.ID,
			Role:           MemberPlain,
		}
		if err := s.store.AddMember(ctx, member); err != nil {
			return Conversation{}, errs.Wrap(err, ErrUnavailable)
		}
	}
	return conv, nil
}

// memberIDsOf returns the user ids of a membership list. | memberIDsOf 返回成员列表的用户 ID。
func memberIDsOf(members []Member) []string {
	out := make([]string, 0, len(members))
	for _, member := range members {
		out = append(out, member.UserID)
	}
	return out
}

// containsMember reports whether the list contains the user. | containsMember 报告列表是否包含该用户。
func containsMember(members []Member, userID string) bool {
	for _, member := range members {
		if member.UserID == userID {
			return true
		}
	}
	return false
}

// uniqueIDs trims, de-duplicates and removes the actor from a member selection. | uniqueIDs 裁剪、去重并从成员选择中移除主体本人。
func uniqueIDs(ids []string, exclude string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || id == exclude {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// mustID generates an identifier and drops the error, which can only mean the process is out of
// entropy. The caller still gets a stable empty id and every write rejects it. | mustID 生成标识并丢弃错误，
// 该错误只可能意味着进程熵耗尽；调用方拿到的空 ID 会被每一次写入拒绝。
func mustID(s *Service, prefix string) string {
	id, err := s.options.NewID(prefix)
	if err != nil {
		return ""
	}
	return id
}
