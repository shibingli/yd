package messaging

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/core/logx"
)

// SendInput is the payload of a new message. | SendInput 是一条新消息的载荷。
type SendInput struct {
	// ConversationID is the target conversation. | ConversationID 是目标会话。
	ConversationID string
	// TypeKey is the message type; empty means "normal". | TypeKey 是消息类型；为空表示 normal。
	TypeKey string
	// Title is the optional subject. | Title 是可选标题。
	Title string
	// Markdown is the body source. | Markdown 是正文源。
	Markdown string
	// TagIDs selects free-form labels. | TagIDs 选择自由标签。
	TagIDs []string
	// GroupIDs selects group markers. | GroupIDs 选择分组标记。
	GroupIDs []string
	// AttachmentIDs binds pending uploads. | AttachmentIDs 绑定待挂载上传。
	AttachmentIDs []string
}

// EditInput is the editable payload of an existing message. | EditInput 是既有消息的可编辑载荷。
type EditInput struct {
	// TypeKey is the new type. | TypeKey 是新类型。
	TypeKey string
	// Title is the new subject. | Title 是新标题。
	Title string
	// Markdown is the new body source. | Markdown 是新正文源。
	Markdown string
	// TagIDs replaces the label set. | TagIDs 覆盖标签集合。
	TagIDs []string
	// GroupIDs replaces the group set. | GroupIDs 覆盖分组集合。
	GroupIDs []string
}

// Send stores a user message in a conversation the actor belongs to. | Send 在主体所属会话中保存一条用户消息。
func (s *Service) Send(ctx context.Context, actor Actor, in SendInput) (MessageView, error) {
	if !actor.valid() {
		return MessageView{}, ErrInvalidInput
	}
	if err := s.EnsureTenant(ctx, actor.TenantID); err != nil {
		return MessageView{}, err
	}
	conv, err := s.store.GetConversation(ctx, actor.TenantID, strings.TrimSpace(in.ConversationID))
	if err != nil {
		return MessageView{}, mapStoreError(err, ErrNotFound)
	}
	if conv.Status == ConversationDissolved {
		return MessageView{}, ErrConversationClosed
	}
	members, err := s.store.ListMembers(ctx, actor.TenantID, conv.ID)
	if err != nil {
		return MessageView{}, errx.Wrap(err, ErrUnavailable)
	}
	if !containsMember(members, actor.ID) {
		return MessageView{}, ErrNotFound
	}
	if conv.Kind == KindSystem {
		return MessageView{}, ErrSystemOnly
	}
	if conv.Kind == KindDirect {
		peer := directPeer(members, actor.ID)
		if peer == "" {
			return MessageView{}, ErrNotFound
		}
		if err := s.requireFriend(ctx, actor.TenantID, actor.ID, peer); err != nil {
			return MessageView{}, err
		}
	}
	return s.createMessage(ctx, actor, conv, members, in, OriginUser)
}

// SendSystem stores an administrator announcement in the tenant channel. | SendSystem 在租户公告频道保存管理员公告。
func (s *Service) SendSystem(ctx context.Context, actor Actor, in SendInput) (MessageView, error) {
	if !actor.valid() {
		return MessageView{}, ErrInvalidInput
	}
	if !actor.System {
		return MessageView{}, ErrSystemOnly
	}
	if err := s.EnsureTenant(ctx, actor.TenantID); err != nil {
		return MessageView{}, err
	}
	conv, err := s.ensureSystemConversation(ctx, actor)
	if err != nil {
		return MessageView{}, err
	}
	members, err := s.store.ListMembers(ctx, actor.TenantID, conv.ID)
	if err != nil {
		return MessageView{}, errx.Wrap(err, ErrUnavailable)
	}
	return s.createMessage(ctx, actor, conv, members, in, OriginSystem)
}

// createMessage validates and stores one message with its tags and attachments. | createMessage 校验并保存一条消息及其标签与附件。
func (s *Service) createMessage(ctx context.Context, actor Actor, conv Conversation, members []Member, in SendInput, origin MessageOrigin) (MessageView, error) {
	msgType, err := s.resolveType(ctx, actor, in.TypeKey, origin)
	if err != nil {
		return MessageView{}, err
	}
	title := truncate(strings.TrimSpace(in.Title), s.options.MaxTitleLen)
	markdown := strings.TrimSpace(in.Markdown)
	if len(markdown) > s.options.MaxBodyLen {
		return MessageView{}, ErrInvalidInput
	}
	tags, err := s.resolveTags(ctx, actor, in.GroupIDs, in.TagIDs)
	if err != nil {
		return MessageView{}, err
	}
	attachments, err := s.pendingAttachments(ctx, actor, in.AttachmentIDs)
	if err != nil {
		return MessageView{}, err
	}
	if title == "" && markdown == "" && len(attachments) == 0 {
		return MessageView{}, ErrInvalidInput
	}
	html, text, err := s.renderer.Render(markdown)
	if err != nil {
		return MessageView{}, errx.Wrap(err, ErrInvalidInput)
	}

	now := s.options.Now()
	msg := Message{
		CreatedAt:      now,
		UpdatedAt:      now,
		ID:             mustID(s, "msg_"),
		TenantID:       actor.TenantID,
		ConversationID: conv.ID,
		SenderID:       actor.ID,
		SenderName:     actor.DisplayName,
		Origin:         origin,
		TypeKey:        msgType.Key,
		TypeName:       msgType.Name,
		TypeNameEN:     msgType.NameEN,
		Tone:           msgType.Tone,
		Title:          title,
		BodyMarkdown:   markdown,
		BodyHTML:       html,
		BodyText:       text,
		Status:         StatusSent,
	}
	msg.SearchText = searchText(msg, tags, conv)
	attachmentIDs := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		attachmentIDs = append(attachmentIDs, attachment.ID)
	}
	tagIDs := make([]string, 0, len(tags))
	for _, tag := range tags {
		tagIDs = append(tagIDs, tag.ID)
	}
	if err := s.store.CreateMessage(ctx, msg, tagIDs, attachmentIDs); err != nil {
		return MessageView{}, messageWriteError(err)
	}
	labels, groups := splitTags(tags)
	audience := memberIDsOf(members)
	s.emit(ctx, Event{
		Type:           EventMessageCreated,
		TenantID:       actor.TenantID,
		ConversationID: conv.ID,
		MessageID:      msg.ID,
		ActorID:        actor.ID,
		Audience:       audience,
		Detail:         map[string]string{"origin": string(origin), "type": msg.TypeKey},
	})
	return MessageView{
		Message:           msg,
		Attachments:       attachments,
		Tags:              labels,
		Groups:            groups,
		Mine:              true,
		ConversationKind:  conv.Kind,
		ConversationTitle: conv.Title,
	}, nil
}

// Edit updates a message inside the edit window or as a moderator. | Edit 在编辑窗口内或作为管理员更新消息。
func (s *Service) Edit(ctx context.Context, actor Actor, messageID string, in EditInput) (MessageView, error) {
	if !actor.valid() {
		return MessageView{}, ErrInvalidInput
	}
	msg, conv, members, err := s.messageForChange(ctx, actor, messageID)
	if err != nil {
		return MessageView{}, err
	}
	now := s.options.Now()
	// System content belongs to message:system even for an actor with message:manage. |
	// 系统内容属于 message:system，即使主体持有 message:manage 也不例外。
	if msg.Origin == OriginSystem && !actor.System {
		return MessageView{}, ErrSystemOnly
	}
	if msg.Recalled() {
		return MessageView{}, ErrRecalled
	}
	if !actor.Manage {
		if msg.SenderID != actor.ID {
			return MessageView{}, ErrNotSender
		}
		if now.Sub(msg.CreatedAt) > s.options.EditWindow {
			return MessageView{}, ErrEditExpired
		}
	}
	msgType, err := s.resolveType(ctx, actor, in.TypeKey, msg.Origin)
	if err != nil {
		return MessageView{}, err
	}
	title := truncate(strings.TrimSpace(in.Title), s.options.MaxTitleLen)
	markdown := strings.TrimSpace(in.Markdown)
	if len(markdown) > s.options.MaxBodyLen {
		return MessageView{}, ErrInvalidInput
	}
	existing, err := s.store.MessageAttachments(ctx, actor.TenantID, []string{msg.ID})
	if err != nil {
		return MessageView{}, errx.Wrap(err, ErrUnavailable)
	}
	if title == "" && markdown == "" && len(existing[msg.ID]) == 0 {
		return MessageView{}, ErrInvalidInput
	}
	tags, err := s.resolveTags(ctx, actor, in.GroupIDs, in.TagIDs)
	if err != nil {
		return MessageView{}, err
	}
	html, text, err := s.renderer.Render(markdown)
	if err != nil {
		return MessageView{}, errx.Wrap(err, ErrInvalidInput)
	}
	editedAt := now
	msg.EditedAt = &editedAt
	msg.UpdatedAt = now
	msg.TypeKey = msgType.Key
	msg.TypeName = msgType.Name
	msg.TypeNameEN = msgType.NameEN
	msg.Tone = msgType.Tone
	msg.Title = title
	msg.BodyMarkdown = markdown
	msg.BodyHTML = html
	msg.BodyText = text
	msg.SearchText = searchText(msg, tags, conv)
	tagIDs := make([]string, 0, len(tags))
	for _, tag := range tags {
		tagIDs = append(tagIDs, tag.ID)
	}
	if err := s.store.UpdateMessage(ctx, msg, tagIDs); err != nil {
		return MessageView{}, messageWriteError(err)
	}
	if actor.Manage {
		s.recordAudit(ctx, AuditEntry{
			CreatedAt:      now,
			TenantID:       actor.TenantID,
			ActorID:        actor.ID,
			Action:         "message.edit",
			MessageID:      msg.ID,
			ConversationID: conv.ID,
			Detail:         auditDetail(map[string]string{"sender": msg.SenderID}),
		})
	}
	s.emit(ctx, Event{Type: EventMessageUpdated, TenantID: actor.TenantID, ConversationID: conv.ID, MessageID: msg.ID, ActorID: actor.ID, Audience: memberIDsOf(members)})
	views, err := s.messageViews(ctx, actor, []Message{msg}, members, true)
	if err != nil || len(views) == 0 {
		if err != nil {
			return MessageView{}, err
		}
		return MessageView{}, ErrNotFound
	}
	return views[0], nil
}

// Recall turns a message into a tombstone. Regular authors are bounded by the recall window;
// moderators are not, and an administrator announcement additionally requires message:system. |
// Recall 把消息变成墓碑。普通作者受撤回窗口限制；管理员不受限制，系统公告还必须具备 message:system。
func (s *Service) Recall(ctx context.Context, actor Actor, messageID, reason string) error {
	if !actor.valid() {
		return ErrInvalidInput
	}
	msg, conv, members, err := s.messageForChange(ctx, actor, messageID)
	if err != nil {
		return err
	}
	if msg.Recalled() {
		return nil
	}
	now := s.options.Now()
	if msg.Origin == OriginSystem && !actor.System {
		return ErrSystemOnly
	}
	if !actor.Manage {
		if msg.SenderID != actor.ID {
			return ErrNotSender
		}
		if now.Sub(msg.CreatedAt) > s.options.RecallWindow {
			return ErrRecallExpired
		}
	}
	recalledAt := now
	msg.Status = StatusRecalled
	msg.RecalledAt = &recalledAt
	msg.RecalledBy = actor.ID
	msg.RecallReason = truncate(strings.TrimSpace(reason), 160)
	msg.UpdatedAt = now
	audit := AuditEntry{
		ID:             mustID(s, "au_"),
		CreatedAt:      now,
		TenantID:       actor.TenantID,
		ActorID:        actor.ID,
		Action:         "message.recall",
		MessageID:      msg.ID,
		ConversationID: conv.ID,
		Detail: auditDetail(map[string]string{
			"origin":        string(msg.Origin),
			"sender":        msg.SenderID,
			"title":         msg.Title,
			"body_markdown": msg.BodyMarkdown,
			"reason":        msg.RecallReason,
		}),
	}
	if err := s.store.RecallMessage(ctx, msg, audit); err != nil {
		if errors.Is(err, ErrRecalled) {
			return nil
		}
		return errx.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventMessageRecalled, TenantID: actor.TenantID, ConversationID: conv.ID, MessageID: msg.ID, ActorID: actor.ID, Audience: memberIDsOf(members)})
	return nil
}

// Pin sets or clears the pin window of a system message. | Pin 设置或清除系统消息的置顶窗口。
func (s *Service) Pin(ctx context.Context, actor Actor, messageID string, start, end *time.Time) error {
	if !actor.valid() || !actor.Manage {
		return ErrForbidden
	}
	msg, conv, members, err := s.messageForChange(ctx, actor, messageID)
	if err != nil {
		return err
	}
	if msg.Origin != OriginSystem {
		return ErrPinNotAllowed
	}
	if msg.Recalled() {
		return ErrInvalidInput
	}
	now := s.options.Now()
	if start != nil && end != nil && !end.After(*start) {
		return ErrPinWindowInvalid
	}
	if end != nil && !end.After(now) {
		return ErrPinWindowInvalid
	}
	// A future start without an end is allowed: it becomes active at that instant and stays
	// pinned until an explicit unpin. | 未来开始且无结束是允许的：到点生效，直到显式取消。
	pinnedAt := now
	msg.Pinned = true
	msg.PinStartAt = start
	msg.PinEndAt = end
	msg.PinnedAt = &pinnedAt
	msg.UpdatedAt = now
	audit := AuditEntry{
		ID:             mustID(s, "au_"),
		CreatedAt:      now,
		TenantID:       actor.TenantID,
		ActorID:        actor.ID,
		Action:         "message.pin",
		MessageID:      msg.ID,
		ConversationID: conv.ID,
		Detail:         auditDetail(map[string]string{"start": formatTimePtr(start), "end": formatTimePtr(end)}),
	}
	if err := s.store.SetPin(ctx, msg, audit); err != nil {
		return errx.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventMessagePinned, TenantID: actor.TenantID, ConversationID: conv.ID, MessageID: msg.ID, ActorID: actor.ID, Audience: memberIDsOf(members)})
	return nil
}

// Unpin clears the pin of a system message. | Unpin 取消系统消息的置顶。
func (s *Service) Unpin(ctx context.Context, actor Actor, messageID string) error {
	if !actor.valid() || !actor.Manage {
		return ErrForbidden
	}
	msg, conv, members, err := s.messageForChange(ctx, actor, messageID)
	if err != nil {
		return err
	}
	if msg.Origin != OriginSystem {
		return ErrPinNotAllowed
	}
	now := s.options.Now()
	msg.Pinned = false
	msg.PinStartAt = nil
	msg.PinEndAt = nil
	msg.PinnedAt = nil
	msg.UpdatedAt = now
	audit := AuditEntry{
		ID:             mustID(s, "au_"),
		CreatedAt:      now,
		TenantID:       actor.TenantID,
		ActorID:        actor.ID,
		Action:         "message.unpin",
		MessageID:      msg.ID,
		ConversationID: conv.ID,
	}
	if err := s.store.SetPin(ctx, msg, audit); err != nil {
		return errx.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventMessagePinned, TenantID: actor.TenantID, ConversationID: conv.ID, MessageID: msg.ID, ActorID: actor.ID, Audience: memberIDsOf(members)})
	return nil
}

// Messages returns one page of messages matching the filter. | Messages 返回匹配过滤条件的一页消息。
func (s *Service) Messages(ctx context.Context, actor Actor, filter MessageFilter) (PageResult, error) {
	if !actor.valid() {
		return PageResult{}, ErrInvalidInput
	}
	filter = s.normalizeFilter(filter)
	if err := s.EnsureTenant(ctx, actor.TenantID); err != nil {
		return PageResult{}, err
	}
	list, total, err := s.store.ListMessages(ctx, actor.TenantID, actor.ID, filter, actor.Manage)
	if err != nil {
		return PageResult{}, errx.Wrap(err, ErrUnavailable)
	}
	views, err := s.messageViews(ctx, actor, list, nil, false)
	if err != nil {
		return PageResult{}, err
	}
	return PageResult{Messages: views, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

// Message returns one message the actor may see. | Message 返回主体可见的一条消息。
func (s *Service) Message(ctx context.Context, actor Actor, messageID string) (MessageView, error) {
	if !actor.valid() {
		return MessageView{}, ErrInvalidInput
	}
	msg, _, members, err := s.messageForChange(ctx, actor, messageID)
	if err != nil {
		return MessageView{}, err
	}
	views, err := s.messageViews(ctx, actor, []Message{msg}, members, true)
	if err != nil {
		return MessageView{}, err
	}
	if len(views) == 0 {
		return MessageView{}, ErrNotFound
	}
	return views[0], nil
}

// messageForChange resolves a message, its conversation and members, enforcing membership unless
// the actor is a moderator. | messageForChange 解析消息、会话与成员；非管理员必须满足成员关系。
func (s *Service) messageForChange(ctx context.Context, actor Actor, messageID string) (Message, Conversation, []Member, error) {
	msg, err := s.store.GetMessage(ctx, actor.TenantID, strings.TrimSpace(messageID))
	if err != nil {
		return Message{}, Conversation{}, nil, mapStoreError(err, ErrNotFound)
	}
	conv, err := s.store.GetConversation(ctx, actor.TenantID, msg.ConversationID)
	if err != nil {
		return Message{}, Conversation{}, nil, mapStoreError(err, ErrNotFound)
	}
	members, err := s.store.ListMembers(ctx, actor.TenantID, conv.ID)
	if err != nil {
		return Message{}, Conversation{}, nil, errx.Wrap(err, ErrUnavailable)
	}
	if !containsMember(members, actor.ID) && !actor.Manage {
		return Message{}, Conversation{}, nil, ErrNotFound
	}
	return msg, conv, members, nil
}

// messageViews enriches stored messages with attachments, tags, author names and read counts. |
// messageViews 用附件、标签、作者名与已读计数补全存储消息。
func (s *Service) messageViews(ctx context.Context, actor Actor, messages []Message, members []Member, withReadCounts bool) ([]MessageView, error) {
	if len(messages) == 0 {
		return []MessageView{}, nil
	}
	ids := make([]string, 0, len(messages))
	conversationIDs := make([]string, 0, len(messages))
	for _, msg := range messages {
		ids = append(ids, msg.ID)
		conversationIDs = append(conversationIDs, msg.ConversationID)
	}
	attachments, err := s.store.MessageAttachments(ctx, actor.TenantID, ids)
	if err != nil {
		return nil, errx.Wrap(err, ErrUnavailable)
	}
	tags, err := s.store.MessageTags(ctx, actor.TenantID, ids)
	if err != nil {
		return nil, errx.Wrap(err, ErrUnavailable)
	}
	conversations := make(map[string]Conversation, len(conversationIDs))
	for _, id := range conversationIDs {
		if _, seen := conversations[id]; seen {
			continue
		}
		conv, err := s.store.GetConversation(ctx, actor.TenantID, id)
		if err == nil {
			conversations[id] = conv
		}
	}
	out := make([]MessageView, 0, len(messages))
	for _, msg := range messages {
		view := MessageView{
			Message:           msg,
			Attachments:       attachments[msg.ID],
			Mine:              msg.SenderID == actor.ID,
			ConversationKind:  conversations[msg.ConversationID].Kind,
			ConversationTitle: conversations[msg.ConversationID].Title,
		}
		view.Tags, view.Groups = splitTags(tags[msg.ID])
		if withReadCounts && len(members) > 0 {
			for _, member := range members {
				if member.UserID == msg.SenderID {
					continue
				}
				view.RecipientCount++
				if member.LastReadAt != nil && !member.LastReadAt.Before(msg.CreatedAt) {
					view.ReadCount++
				}
			}
		}
		if msg.Recalled() && !actor.Manage {
			view.BodyMarkdown = ""
			view.BodyHTML = ""
			view.BodyText = ""
			view.Title = ""
			view.Attachments = nil
		}
		out = append(out, view)
	}
	return out, nil
}

// normalizeFilter validates the folder, search expression and page bounds. | normalizeFilter 校验文件夹、检索表达式与页码边界。
func (s *Service) normalizeFilter(filter MessageFilter) MessageFilter {
	switch filter.Folder {
	case FolderSent, FolderSystem, FolderUnread, FolderPinned:
	default:
		filter.Folder = FolderAll
	}
	filter.Query = truncate(strings.TrimSpace(filter.Query), 256)
	if terms := strings.Fields(filter.Query); len(terms) > s.options.SearchTerms {
		filter.Query = strings.Join(terms[:s.options.SearchTerms], " ")
	}
	filter.Title = truncate(strings.TrimSpace(filter.Title), 160)
	filter.Content = truncate(strings.TrimSpace(filter.Content), 160)
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = s.options.PageSize
	}
	if filter.PageSize > s.options.MaxPageSize {
		filter.PageSize = s.options.MaxPageSize
	}
	return filter
}

// resolveType validates the selected type and defaults to "normal". | resolveType 校验所选类型，缺省为 normal。
func (s *Service) resolveType(ctx context.Context, actor Actor, key string, origin MessageOrigin) (MessageType, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		if origin == OriginSystem {
			key = TypeAnnouncement
		} else {
			key = TypeNormal
		}
	}
	if key == TypeSystem && !actor.System && !actor.Manage {
		return MessageType{}, ErrSystemOnly
	}
	t, err := s.store.GetType(ctx, actor.TenantID, key)
	if err != nil {
		return MessageType{}, mapStoreError(err, ErrTypeUnknown)
	}
	if t.Archived() {
		return MessageType{}, ErrTypeUnknown
	}
	return t, nil
}

// resolveTags validates and de-duplicates the selected groups and labels. | resolveTags 校验并去重所选分组与标签。
func (s *Service) resolveTags(ctx context.Context, actor Actor, groupIDs, tagIDs []string) ([]Tag, error) {
	ids := append(uniqueIDs(groupIDs, ""), uniqueIDs(tagIDs, "")...)
	if len(ids) > s.options.MaxTags {
		return nil, ErrInvalidInput
	}
	if len(ids) == 0 {
		return []Tag{}, nil
	}
	tags, err := s.store.GetTags(ctx, actor.TenantID, ids)
	if err != nil {
		return nil, errx.Wrap(err, ErrUnavailable)
	}
	byID := make(map[string]Tag, len(tags))
	for _, tag := range tags {
		byID[tag.ID] = tag
	}
	out := make([]Tag, 0, len(ids))
	for _, id := range ids {
		tag, ok := byID[id]
		if !ok {
			return nil, ErrTagUnknown
		}
		if tag.Archived() {
			return nil, ErrTagUnknown
		}
		out = append(out, tag)
	}
	return out, nil
}

// messageWriteError preserves the business sentinels of a concurrent tag/type deletion; wrapping
// them only as ErrUnavailable would turn a 422 into a misleading 503. |
// messageWriteError 保留并发删除标签/类型时的业务哨兵；只包装为 ErrUnavailable 会把 422 误报成 503。
func messageWriteError(err error) error {
	switch {
	case errors.Is(err, ErrTagUnknown):
		return ErrTagUnknown
	case errors.Is(err, ErrTypeUnknown):
		return ErrTypeUnknown
	case errors.Is(err, ErrRecalled):
		return ErrRecalled
	default:
		return errx.Wrap(err, ErrUnavailable)
	}
}

// pendingAttachments validates the upload ids and returns the pending rows. | pendingAttachments 校验上传 ID 并返回待挂载记录。
func (s *Service) pendingAttachments(ctx context.Context, actor Actor, ids []string) ([]Attachment, error) {
	ids = uniqueIDs(ids, "")
	if len(ids) == 0 {
		return []Attachment{}, nil
	}
	if len(ids) > s.options.MaxAttachments {
		return nil, ErrTooManyAttachments
	}
	out := make([]Attachment, 0, len(ids))
	for _, id := range ids {
		attachment, err := s.store.GetAttachment(ctx, actor.TenantID, id)
		if err != nil {
			return nil, mapStoreError(err, ErrAttachmentNotFound)
		}
		if attachment.UploaderID != actor.ID || attachment.Status != AttachmentPending {
			return nil, ErrAttachmentNotFound
		}
		out = append(out, attachment)
	}
	return out, nil
}

// splitTags partitions a tag list into labels and groups. | splitTags 把标签列表拆成自由标签与分组。
func splitTags(tags []Tag) (labels, groups []Tag) {
	labels = make([]Tag, 0, len(tags))
	groups = make([]Tag, 0, len(tags))
	for _, tag := range tags {
		if tag.Kind == TagGroup {
			groups = append(groups, tag)
		} else {
			labels = append(labels, tag)
		}
	}
	return labels, groups
}

// directPeer returns the other member of a direct conversation. | directPeer 返回单聊中的另一方。
func directPeer(members []Member, actorID string) string {
	for _, member := range members {
		if member.UserID != actorID {
			return member.UserID
		}
	}
	return ""
}

// searchText builds the lower-case searchable projection of a message. | searchText 构建消息的小写可检索投影。
func searchText(msg Message, tags []Tag, conv Conversation) string {
	parts := make([]string, 0, 16)
	parts = append(parts, msg.Title, msg.BodyText, msg.SenderName, msg.TypeName, msg.TypeNameEN, conv.Title, string(conv.Kind))
	for _, tag := range tags {
		parts = append(parts, tag.Name, tag.NameEN)
	}
	joined := strings.Join(parts, " ")
	return strings.ToLower(strings.Join(strings.Fields(joined), " "))
}

// formatTimePtr renders an optional time for audit details. | formatTimePtr 为审计细节渲染可选时间。
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// auditDetail serialises an audit detail map through the framework JSON layer. | auditDetail 经框架 JSON 层序列化审计细节。
func auditDetail(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	data, err := jsonx.Marshal(values)
	if err != nil {
		return ""
	}
	return string(data)
}

// recordAudit appends an audit entry and logs a failure without failing the business action. |
// recordAudit 追加审计记录；失败只记录日志，不使业务动作失败。
func (s *Service) recordAudit(ctx context.Context, entry AuditEntry) {
	if entry.ID == "" {
		entry.ID = mustID(s, "au_")
	}
	if err := s.store.AddAudit(ctx, entry); err != nil {
		s.log.Warn("message audit write failed | 消息审计写入失败", logx.Err(err), logx.Str("action", entry.Action))
	}
}

// Audit returns one page of administrator audit entries. | Audit 返回一页管理员审计记录。
func (s *Service) Audit(ctx context.Context, actor Actor, filter AuditFilter) ([]AuditEntry, int, error) {
	if !actor.valid() || !actor.Manage {
		return nil, 0, ErrForbidden
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = s.options.PageSize
	}
	if filter.PageSize > s.options.MaxPageSize {
		filter.PageSize = s.options.MaxPageSize
	}
	list, total, err := s.store.ListAudit(ctx, actor.TenantID, filter)
	if err != nil {
		return nil, 0, errx.Wrap(err, ErrUnavailable)
	}
	return list, total, nil
}

// sortMessages orders messages by creation time ascending. | sortMessages 按创建时间升序排列消息。
func sortMessages(list []Message) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
}
