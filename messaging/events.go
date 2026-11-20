package messaging

import (
	"context"
	"time"

	"github.com/shibingli/yd/attachments"
)

// Event types emitted by the service after a successful write. | 服务在写入成功后发出的事件类型。
const (
	// EventMessageCreated announces a new message. | EventMessageCreated 宣告一条新消息。
	EventMessageCreated = "message.created"
	// EventMessageUpdated announces an edited message. | EventMessageUpdated 宣告一条被编辑的消息。
	EventMessageUpdated = "message.updated"
	// EventMessageRecalled announces a tombstone. | EventMessageRecalled 宣告一条撤回。
	EventMessageRecalled = "message.recalled"
	// EventMessageRead announces that a member advanced the read watermark. | EventMessageRead 宣告成员推进了已读水位。
	EventMessageRead = "message.read"
	// EventMessagePinned announces a pin change. | EventMessagePinned 宣告置顶变化。
	EventMessagePinned = "message.pinned"
	// EventFriendRequested announces an incoming friend request. | EventFriendRequested 宣告收到好友申请。
	EventFriendRequested = "friend.requested"
	// EventFriendAccepted announces an accepted request. | EventFriendAccepted 宣告申请已接受。
	EventFriendAccepted = "friend.accepted"
	// EventFriendRejected announces a rejected or cancelled request. | EventFriendRejected 宣告申请被拒绝或取消。
	EventFriendRejected = "friend.rejected"
	// EventFriendRemoved announces a removed friendship. | EventFriendRemoved 宣告好友关系已删除。
	EventFriendRemoved = "friend.removed"
	// EventFriendBlocked announces a new block. | EventFriendBlocked 宣告新增拉黑。
	EventFriendBlocked = "friend.blocked"
	// EventConversationChanged announces group membership or title changes. | EventConversationChanged 宣告群成员或群名变化。
	EventConversationChanged = "conversation.changed"
)

// Event detail keys used by Event.Detail. | Event.Detail 使用的事件细节键。
const (
	// DetailAction names the conversation change carried by an event. | DetailAction 命名事件携带的会话变更动作。
	DetailAction = "action"
	// DetailCatalog names the catalog family carried by an event. | DetailCatalog 命名事件携带的目录族。
	DetailCatalog = "catalog"
	// DetailKey carries the affected catalog key. | DetailKey 携带受影响的目录键。
	DetailKey = "key"
	// DetailAt carries a UTC timestamp in RFC 3339 form. | DetailAt 以 RFC 3339 形式携带 UTC 时间戳。
	DetailAt = "at"
)

// Conversation change actions carried in Event.Detail[DetailAction]. | Event.Detail[DetailAction] 携带的会话变更动作。
const (
	// ActionCreated announces a new conversation. | ActionCreated 宣告新建会话。
	ActionCreated = "created"
	// ActionMemberAdded announces an added group member. | ActionMemberAdded 宣告新增群成员。
	ActionMemberAdded = "member_added"
	// ActionMemberRemoved announces a removed group member. | ActionMemberRemoved 宣告移除群成员。
	ActionMemberRemoved = "member_removed"
	// ActionMemberLeft announces a member leaving a group. | ActionMemberLeft 宣告成员退出群聊。
	ActionMemberLeft = "member_left"
	// ActionRenamed announces a conversation rename. | ActionRenamed 宣告会话改名。
	ActionRenamed = "renamed"
	// ActionDissolved announces a dissolved group. | ActionDissolved 宣告群聊解散。
	ActionDissolved = "dissolved"
)

// Audit action identifiers persisted in yd_msg_audit.action. They are machine-readable values and must
// never drift, so every write path uses these constants instead of spelling the string again. |
// 审计动作标识，持久化在 yd_msg_audit.action。它们是机器可读的稳定值，绝不能漂移，因此每个写入路径都
// 使用这些常量，而不是再拼一遍字符串。
const (
	// AuditActionEdit records a moderator edit. | AuditActionEdit 记录管理员编辑。
	AuditActionEdit = "message.edit"
	// AuditActionRecall records a recall. | AuditActionRecall 记录撤回。
	AuditActionRecall = "message.recall"
	// AuditActionPin records a pin. | AuditActionPin 记录置顶。
	AuditActionPin = "message.pin"
	// AuditActionUnpin records an unpin. | AuditActionUnpin 记录取消置顶。
	AuditActionUnpin = "message.unpin"
)

// Catalog families carried in Event.Detail[DetailCatalog]. | Event.Detail[DetailCatalog] 携带的目录族。
const (
	// CatalogFamilyType names the message-type catalog. | CatalogFamilyType 命名消息类型目录。
	CatalogFamilyType = "type"
)

// Event is one realtime notification. It carries no message body: receivers re-read what they are
// allowed to see through the service, so a stale or leaked event can never become a data leak. |
// Event 是一条实时通知。它不携带消息正文：接收方通过服务重新读取自己有权看到的内容，
// 因此过期或泄漏的事件都不可能变成数据泄漏。
type Event struct {
	// At is the UTC occurrence time. | At 是 UTC 发生时间。
	At time.Time `json:"at"`
	// Detail carries small scalar metadata such as a type key or a read count. | Detail 携带类型键或读数等小型标量元数据。
	Detail map[string]string `json:"detail,omitempty"`
	// ID is the unique event id used for diagnostics and idempotent refresh. | ID 是用于诊断与幂等刷新的唯一事件 ID。
	ID string `json:"id,omitempty"`
	// Origin is the node that first published the event; a node skips its own echo from the bus. |
	// Origin 是首次发布事件的节点；节点会跳过自己经总线回传的事件。
	Origin string `json:"origin,omitempty"`
	// Type is one of the Event* constants. | Type 取 Event* 常量之一。
	Type string `json:"type"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id"`
	// ConversationID is set for message and conversation events. | ConversationID 在消息与会话事件上写入。
	ConversationID string `json:"conversation_id,omitempty"`
	// MessageID is set for message events. | MessageID 在消息事件上写入。
	MessageID string `json:"message_id,omitempty"`
	// ActorID is the user that caused the event. | ActorID 是触发事件的主体。
	ActorID string `json:"actor_id,omitempty"`
	// Audience lists user ids that should be notified. An empty audience means every member of the
	// conversation. | Audience 列出应收到通知的用户 ID；为空表示会话的全部成员。
	Audience []string `json:"audience,omitempty"`
}

// Notifier delivers events to connected clients. Publish must not block the write path: an
// implementation may queue or drop, but it must never make a saved message fail. | Notifier 把事件投递给在线客户端。
// Publish 不得阻塞写入路径：实现可以排队或丢弃，但绝不能让已保存的消息失败。
type Notifier interface {
	// Publish delivers e, returning a delivery error that callers log but never propagate to the
	// user write. | Publish 投递 e，返回的投递错误只记录日志，绝不上抛为用户写入失败。
	Publish(ctx context.Context, e Event) error
}

// Renderer turns Markdown into sanitised HTML and a plain-text projection. | Renderer 把 Markdown 转成净化后的 HTML 与纯文本投影。
type Renderer interface {
	// Render returns the HTML fragment and the plain text of markdown. Raw HTML must never survive. |
	// Render 返回 markdown 的 HTML 片段与纯文本；原始 HTML 绝不允许保留。
	Render(markdown string) (html, text string, err error)
}

// BlobStore stores attachment bytes under opaque keys. | BlobStore 以不透明键保存附件字节。
//
// Deprecated: use attachments.BlobStore. The message centre now delegates all attachment storage to
// the attachments domain. | 已弃用：请使用 attachments.BlobStore；消息中心现在把附件存储全部委托给附件域。
type BlobStore = attachments.BlobStore
