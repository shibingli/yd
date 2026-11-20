package messaging

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
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
	// Members of every conversation load in one batch, and their profiles resolve in one directory call.
	// The previous per-conversation loop turned one page into 2N+3 round trips. |
	// 所有会话的成员一次批量加载，资料也一次解析；此前逐会话循环会把一次页面加载变成 2N+3 次往返。
	membersByConversation, err := s.membersByConversation(ctx, actor.TenantID, ids)
	if err != nil {
		return nil, errs.Wrap(err, ErrUnavailable)
	}
	allMemberIDs := make([]string, 0, len(list))
	seenMember := make(map[string]struct{}, len(list))
	for _, members := range membersByConversation {
		for _, member := range members {
			if _, seen := seenMember[member.UserID]; seen {
				continue
			}
			seenMember[member.UserID] = struct{}{}
			allMemberIDs = append(allMemberIDs, member.UserID)
		}
	}
	profiles, err := s.directory.Profiles(ctx, actor.TenantID, allMemberIDs)
	if err != nil {
		return nil, errs.Wrap(err, ErrUnavailable)
	}

	out := make([]ConversationView, 0, len(list))
	for _, conv := range list {
		view := ConversationView{Conversation: conv, Unread: unread[conv.ID]}
		for _, member := range membersByConversation[conv.ID] {
			profile, ok := profiles[member.UserID]
			if !ok {
				continue
			}
			view.Members = append(view.Members, profile)
			if conv.Kind == KindDirect && member.UserID != actor.ID {
				peer := profile
				view.Peer = &peer
			}
		}
		if msg, ok := last[conv.ID]; ok {
			if msg.Recalled() && !actor.Manage {
				// A recalled message is a tombstone: its title is content just like the body and must never
				// leak through a preview without moderation rights. | 撤回的消息是墓碑：标题和正文一样都是内容，
				// 在没有管理权时绝不能经预览泄漏。
				stripRecalled(&msg)
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

// openConversation 解析会话与成员并校验成员关系，但不推进已读水位：调用方必须先成功加载线程，
// 之后才允许标记已读，因此加载失败绝不会把没看到的消息标成已读。
// openConversation resolves the conversation and its members and checks membership. It deliberately does not
// advance the read watermark: the caller must load the thread first and only then mark it read, so a failed
// history load can never mark unseen messages as read.
func (s *Service) openConversation(ctx context.Context, actor Actor, conversationID string) (Conversation, []Member, int, error) {
	if !actor.valid() {
		return Conversation{}, nil, 0, ErrInvalidInput
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return Conversation{}, nil, 0, ErrNotFound
	}
	conv, err := s.store.GetConversation(ctx, actor.TenantID, conversationID)
	if err != nil {
		return Conversation{}, nil, 0, mapStoreError(err, ErrNotFound)
	}
	members, err := s.store.ListMembers(ctx, actor.TenantID, conv.ID)
	if err != nil {
		return Conversation{}, nil, 0, errs.Wrap(err, ErrUnavailable)
	}
	if !containsMember(members, actor.ID) {
		// A notice channel is private to its recipient; management bypass never opens one. |
		// 通知频道只属于收件人，管理旁路也不能打开。
		if conv.Kind == KindNotice || !actor.Manage {
			return Conversation{}, nil, 0, ErrNotFound
		}
	}
	// 未读只读一次：管理员旁路查不到成员关系时该查询同样返回零值。
	// Read the unread map once; a moderator without membership simply gets no entry and therefore zero.
	unread, err := s.store.ConversationUnread(ctx, actor.TenantID, actor.ID)
	if err != nil {
		return Conversation{}, nil, 0, errs.Wrap(err, ErrUnavailable)
	}
	return conv, members, unread[conv.ID], nil
}

// markConversationRead advances the actor's watermark after the thread has loaded and emits the read event
// only when the member really had unread messages. The watermark moves to the newest committed message instead
// of the caller's clock, so a message committed later with an earlier timestamp can never be marked read. |
// markConversationRead 在线程加载完成后推进主体水位，并仅在成员确实有未读时发出已读事件。水位推进到当前
// 已提交的最新消息而非调用方的时钟，因此稍后提交、时间戳更早的消息绝不会被误标为已读。
func (s *Service) markConversationRead(ctx context.Context, actor Actor, conv Conversation, members []Member, unreadBefore int) (int, error) {
	if unreadBefore <= 0 || !containsMember(members, actor.ID) {
		return unreadBefore, nil
	}
	at, moved, err := s.advanceReadToLatest(ctx, actor.TenantID, conv.ID, actor.ID)
	if err != nil {
		return 0, errs.Wrap(err, ErrUnavailable)
	}
	if !moved {
		// 没有可推进的目标；保持原水位与未读数，绝不广播一条并未发生的已读。
		// There is nothing to advance to; keep the previous watermark and count, and never emit a read that did not happen.
		return unreadBefore, nil
	}
	for i := range members {
		if members[i].UserID == actor.ID {
			members[i].LastReadAt = &at
		}
	}
	// 受众包含读者本人：读者自己的 WebSocket 连接也要据此刷新未读角标，否则角标会一直停留在旧值。
	// The audience includes the reader: their own WebSocket connection needs the event to refresh the unread
	// badge, otherwise the badge keeps the previous count until a page reload.
	s.emit(ctx, Event{Type: EventMessageRead, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{DetailAt: at.UTC().Format(time.RFC3339)}})
	return 0, nil
}

// readWatermarkAdvancer is the optional Store capability that resolves a read watermark from committed
// messages instead of the caller's clock. | readWatermarkAdvancer 是 Store 的可选能力：从已提交消息解析
// 已读水位，而不是使用调用方的时钟。
type readWatermarkAdvancer interface {
	// AdvanceReadToLatest moves the watermark to the newest committed sent message and reports the effective
	// watermark plus whether it moved. | AdvanceReadToLatest 把水位推进到最新已提交的 sent 消息，并报告
	// 生效水位与是否发生推进。
	AdvanceReadToLatest(ctx context.Context, tenantID, conversationID, userID string) (time.Time, bool, error)
}

// advanceReadToLatest resolves the newest committed message and moves the watermark to it. Stores without the
// atomic capability fall back to reading the newest page; an empty conversation never advances. |
// advanceReadToLatest 解析最新已提交消息并把水位推进到它。没有原子能力的存储回退到读取最新一页；
// 空会话绝不推进。
func (s *Service) advanceReadToLatest(ctx context.Context, tenantID, conversationID, userID string) (time.Time, bool, error) {
	if advancer, ok := s.store.(readWatermarkAdvancer); ok {
		return advancer.AdvanceReadToLatest(ctx, tenantID, conversationID, userID)
	}
	messages, _, err := s.store.ListMessagesByConversation(ctx, tenantID, conversationID, 1, 1, false)
	if err != nil {
		return time.Time{}, false, err
	}
	if len(messages) == 0 {
		return time.Time{}, false, nil
	}
	at := messages[0].CreatedAt
	if err := s.store.AdvanceRead(ctx, tenantID, conversationID, userID, at); err != nil {
		return time.Time{}, false, err
	}
	return at, true, nil
}

// Conversation returns one conversation, its newest page of messages and marks it read. | Conversation 返回一个会话及其最新一页消息，并标记已读。
func (s *Service) Conversation(ctx context.Context, actor Actor, conversationID string) (ConversationView, []MessageView, error) {
	conv, members, unreadCount, err := s.openConversation(ctx, actor, conversationID)
	if err != nil {
		return ConversationView{}, nil, err
	}

	page := s.options.PageSize
	if page < 50 {
		page = 50
	}
	// A recalled message stays in the thread as a tombstone; the view layer strips its body for
	// readers without moderation rights. | 撤回的消息以墓碑形式留在会话里；视图层会为无管理权的读者
	// 剥掉正文。
	// Fetch one extra message to decide whether the "load earlier" control is needed without a
	// second count query. The extra oldest row is dropped before rendering. |
	// 多取一条消息即可判断是否需要「加载更早」，无需第二次计数查询；渲染前丢弃最旧的那条。
	messages, err := s.conversationMessages(ctx, actor.TenantID, conv.ID, 1, page+1, true)
	if err != nil {
		return ConversationView{}, nil, errs.Wrap(err, ErrUnavailable)
	}
	hasEarlier := len(messages) > page
	if hasEarlier {
		messages = messages[1:]
	}
	// 线程已经成功加载，此时才允许推进水位；加载失败的路径在上面直接返回，水位保持不动。
	// The thread has loaded successfully, so and only so may the watermark move; a failed load returned above and
	// left the watermark untouched.
	unreadCount, err = s.markConversationRead(ctx, actor, conv, members, unreadCount)
	if err != nil {
		return ConversationView{}, nil, err
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
	view := ConversationView{Conversation: conv, HasEarlier: hasEarlier}
	for _, id := range memberIDs {
		if profile, ok := profiles[id]; ok {
			view.Members = append(view.Members, profile)
			if conv.Kind == KindDirect && id != actor.ID {
				peer := profile
				view.Peer = &peer
			}
		}
	}
	view.Unread = unreadCount
	if len(views) > 0 {
		last := views[len(views)-1].Message
		view.LastMessage = &last
	}
	return view, views, nil
}

// ConversationMessages returns the newest page of one conversation and advances the read watermark, but does not
// resolve member profiles or build the conversation header. The history-refresh fragment renders only the messages,
// so the peer/avatar lookups and the summary view would be discarded work. |
// ConversationMessages 返回一个会话的最新一页消息并推进已读水位，但不解析成员资料、也不构造会话标题。
// 历史刷新片段只渲染消息，成员头像与汇总视图在那里都是会被丢弃的工作。
func (s *Service) ConversationMessages(ctx context.Context, actor Actor, conversationID string) ([]MessageView, error) {
	views, _, err := s.ConversationMessagesLimit(ctx, actor, conversationID, 0)
	return views, err
}

// ConversationMessagesLimit returns the newest limit messages and whether older messages exist. A
// non-positive limit uses the configured page size; the value is capped to keep one response bounded.
// It advances the read watermark exactly like opening the conversation. |
// ConversationMessagesLimit 返回最新的 limit 条消息以及是否存在更早的消息。limit 非正时使用配置页长；
// 取值有上限，使单次响应有界。它像打开会话一样推进已读水位。
func (s *Service) ConversationMessagesLimit(ctx context.Context, actor Actor, conversationID string, limit int) ([]MessageView, bool, error) {
	conv, members, unreadCount, err := s.openConversation(ctx, actor, conversationID)
	if err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		limit = s.options.PageSize
	}
	if limit < 50 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	messages, err := s.conversationMessages(ctx, actor.TenantID, conv.ID, 1, limit+1, true)
	if err != nil {
		return nil, false, errs.Wrap(err, ErrUnavailable)
	}
	hasEarlier := len(messages) > limit
	if hasEarlier {
		messages = messages[1:]
	}
	if _, err := s.markConversationRead(ctx, actor, conv, members, unreadCount); err != nil {
		return nil, false, err
	}
	views, err := s.messageViews(ctx, actor, messages, members, true)
	if err != nil {
		return nil, false, err
	}
	return views, hasEarlier, nil
}

// ConversationMessagesAfter returns the messages newer than one anchor, oldest first. It advances the read
// watermark exactly like opening the conversation does, so a realtime append also clears the unread state. |
// ConversationMessagesAfter 返回晚于锚点消息的新消息，按从旧到新排列。它像打开会话一样推进已读水位，
// 因此实时追加也会同时清除未读状态。
func (s *Service) ConversationMessagesAfter(ctx context.Context, actor Actor, conversationID, afterID string, limit int) ([]MessageView, error) {
	if strings.TrimSpace(afterID) == "" {
		return nil, ErrInvalidInput
	}
	conv, members, unreadCount, err := s.openConversation(ctx, actor, conversationID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	var messages []Message
	if lister, ok := s.store.(conversationMessageAfter); ok {
		messages, err = lister.ListMessagesAfter(ctx, actor.TenantID, conv.ID, afterID, limit)
		if err != nil {
			return nil, err
		}
	} else {
		// Without the incremental capability, read the newest page and look for the anchor; a missing anchor means
		// the client is too far behind, and the caller falls back to a full render. | 存储层没有增量能力时退回
		// 最新一页，并在其中寻找锚点；找不到锚点说明客户端落后太多，由调用方转为整段重取。
		page := limit
		if page < 50 {
			page = 50
		}
		recent, err := s.conversationMessages(ctx, actor.TenantID, conv.ID, 1, page, true)
		if err != nil {
			return nil, errs.Wrap(err, ErrUnavailable)
		}
		index := -1
		for i := range recent {
			if recent[i].ID == afterID {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, ErrNotFound
		}
		messages = recent[index+1:]
		if len(messages) > limit {
			messages = messages[:limit]
		}
	}
	if _, err := s.markConversationRead(ctx, actor, conv, members, unreadCount); err != nil {
		return nil, err
	}
	return s.messageViews(ctx, actor, messages, members, true)
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
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: id, ActorID: actor.ID, Audience: memberIDs, Detail: map[string]string{DetailAction: ActionCreated}})
	return conv, nil
}

// boundedMemberAdder is the optional Store capability that inserts a membership under the conversation row
// lock and enforces the member ceiling. | boundedMemberAdder 是 Store 的可选能力：在会话行锁内插入成员关系
// 并执行成员上限。
type boundedMemberAdder interface {
	// AddMemberBounded inserts one membership and reports whether it was inserted. An existing membership is
	// idempotent success, a full group yields ErrTooManyMembers and a dissolved conversation yields
	// ErrConversationClosed. | AddMemberBounded 插入一条成员关系并报告是否插入。已存在按幂等成功处理，
	// 群满返回 ErrTooManyMembers，已解散返回 ErrConversationClosed。
	AddMemberBounded(ctx context.Context, member Member, maxMembers int) (bool, error)
}

// conversationWriteError preserves the business sentinels of a locked conversation write. |
// conversationWriteError 保留会话行锁写入的业务哨兵。
func conversationWriteError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ErrConversationClosed):
		return ErrConversationClosed
	case errors.Is(err, ErrTooManyMembers):
		return ErrTooManyMembers
	default:
		return errs.Wrap(err, ErrUnavailable)
	}
}

// AddGroupMember adds one friend to a group the actor owns. The membership is inserted under the conversation
// row lock so a concurrent add cannot exceed MaxMembers: the ceiling check and the insert are one atomic step
// on stores that provide the bounded capability. | AddGroupMember 把一位好友加入主体拥有的群。成员关系在
// 会话行锁内插入，并发添加无法突破 MaxMembers：提供有界能力的存储把上限检查与插入放在同一个原子步骤里。
func (s *Service) AddGroupMember(ctx context.Context, actor Actor, conversationID, memberID string) error {
	conv, actorMember, err := s.groupForChange(ctx, actor, conversationID)
	if err != nil {
		return err
	}
	if actorMember.Role != MemberOwner && !actor.Manage {
		return ErrForbidden
	}
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return ErrInvalidInput
	}
	if err := s.requireFriend(ctx, actor.TenantID, actor.ID, memberID); err != nil {
		return err
	}
	member := Member{
		JoinedAt:       s.options.Now(),
		ID:             mustID(s, "mb_"),
		TenantID:       actor.TenantID,
		ConversationID: conv.ID,
		UserID:         memberID,
		Role:           MemberPlain,
	}
	if adder, ok := s.store.(boundedMemberAdder); ok {
		added, err := adder.AddMemberBounded(ctx, member, s.options.MaxMembers)
		if err != nil {
			return conversationWriteError(err)
		}
		if !added {
			return nil
		}
		s.emitMemberChange(ctx, actor, conv.ID, ActionMemberAdded)
		return nil
	}
	// 没有有界能力的存储只能先查后插；gormstore 始终走上面的原子路径。
	// A store without the bounded capability can only check and then insert; gormstore always takes the atomic path above.
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
	if err := s.store.AddMember(ctx, member); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: append(memberIDsOf(members), memberID), Detail: map[string]string{DetailAction: ActionMemberAdded}})
	return nil
}

// emitMemberChange broadcasts a membership change to the current member set. |
// emitMemberChange 向当前成员集合广播一次成员变化。
func (s *Service) emitMemberChange(ctx context.Context, actor Actor, conversationID, action string) {
	members, err := s.store.ListMembers(ctx, actor.TenantID, conversationID)
	if err != nil {
		s.log.Warn("conversation member refresh failed | 会话成员刷新失败",
			ydlog.Err(err), ydlog.Str("conversation", conversationID))
		return
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: conversationID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{DetailAction: action}})
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
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: append(memberIDsOf(members), memberID), Detail: map[string]string{DetailAction: ActionMemberRemoved}})
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
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{DetailAction: ActionMemberLeft}})
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
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{DetailAction: ActionRenamed}})
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
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ConversationID: conv.ID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{DetailAction: ActionDissolved}})
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

// MarkRead advances the actor's read watermark in a conversation. The watermark targets the newest committed
// message rather than the caller's clock, so a concurrently committed message with an earlier timestamp stays
// unread. | MarkRead 推进主体在会话中的已读水位。水位指向当前已提交的最新消息而非调用方时钟，
// 因此并发提交、时间戳更早的消息仍保持未读。
func (s *Service) MarkRead(ctx context.Context, actor Actor, conversationID string) error {
	if !actor.valid() {
		return ErrInvalidInput
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ErrNotFound
	}
	members, err := s.store.ListMembers(ctx, actor.TenantID, conversationID)
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
	at, moved, err := s.advanceReadToLatest(ctx, actor.TenantID, conversationID, actor.ID)
	if err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	if !moved {
		return nil
	}
	// 与打开会话一致：读者本人也在受众里，自己的 WebSocket 才能立即刷新未读角标。
	// As with opening a conversation, the reader is part of the audience so their own WebSocket refreshes the badge
	// immediately.
	s.emit(ctx, Event{Type: EventMessageRead, TenantID: actor.TenantID, ConversationID: conversationID, ActorID: actor.ID, Audience: memberIDsOf(members), Detail: map[string]string{DetailAt: at.UTC().Format(time.RFC3339)}})
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
