package messaging

import (
	"context"
	"time"
)

// Store is the persistence boundary of the message centre. Every method takes the tenant id first
// because the tenant is the hard boundary: an id from another tenant must behave exactly like a
// missing id. | Store 是消息中心的持久化边界。每个方法都以租户 ID 为第一参数，因为租户是硬边界：
// 其它租户的 ID 必须表现得与不存在的 ID 完全一致。
//
// Implementations live in messaging/gormstore. The domain package never imports an ORM. |
// 实现位于 messaging/gormstore；领域包从不引入 ORM。
type Store interface {
	// EnsureTypes inserts the missing built-in types without touching existing rows. | EnsureTypes 插入缺失的内置类型，不触碰已有行。
	EnsureTypes(ctx context.Context, tenantID string, types []MessageType) error
	// ListTypes returns the tenant's types in creation order. | ListTypes 按创建顺序返回租户的消息类型。
	ListTypes(ctx context.Context, tenantID string, includeArchived bool) ([]MessageType, error)
	// GetType resolves one type by its stable key. | GetType 按稳定键解析一个类型。
	GetType(ctx context.Context, tenantID, key string) (MessageType, error)
	// CreateType stores one custom type. | CreateType 保存一个自定义类型。
	CreateType(ctx context.Context, t MessageType) error
	// UpdateType replaces the editable fields of a type. | UpdateType 覆盖类型的可编辑字段。
	UpdateType(ctx context.Context, t MessageType) error
	// DeleteType removes an unused type. | DeleteType 删除未被使用的类型。
	DeleteType(ctx context.Context, tenantID, key string) error
	// CountMessagesOfType returns how many messages use a type. | CountMessagesOfType 返回使用某类型的消息数。
	CountMessagesOfType(ctx context.Context, tenantID, key string) (int64, error)

	// ListTags returns the tenant's groups or labels. | ListTags 返回租户的分组或标签。
	ListTags(ctx context.Context, tenantID string, kind TagKind, includeArchived bool) ([]Tag, error)
	// GetTags resolves a batch of tag ids inside one tenant. | GetTags 批量解析同一租户内的标签 ID。
	GetTags(ctx context.Context, tenantID string, ids []string) ([]Tag, error)
	// CreateTag stores one group or label. | CreateTag 保存一个分组或标签。
	CreateTag(ctx context.Context, t Tag) error
	// UpdateTag replaces the editable fields of a tag. | UpdateTag 覆盖标签的可编辑字段。
	UpdateTag(ctx context.Context, t Tag) error
	// DeleteTag removes an unused group or label. | DeleteTag 删除未被使用的分组或标签。
	DeleteTag(ctx context.Context, tenantID, id string) error
	// CountMessagesOfTag returns how many messages carry a tag. | CountMessagesOfTag 返回携带某标签的消息数。
	CountMessagesOfTag(ctx context.Context, tenantID, id string) (int64, error)

	// CreateFriendRequest inserts or reactivates one directed request. | CreateFriendRequest 插入或重新激活一条有向申请。
	CreateFriendRequest(ctx context.Context, req FriendRequest) error
	// GetFriendRequest reads one request by id. | GetFriendRequest 按 ID 读取一条申请。
	GetFriendRequest(ctx context.Context, tenantID, id string) (FriendRequest, error)
	// FriendRequestBetween reads the directed request in one direction. | FriendRequestBetween 读取某一方向上的申请。
	FriendRequestBetween(ctx context.Context, tenantID, requesterID, addresseeID string) (FriendRequest, error)
	// UpdateFriendRequest advances the state machine. | UpdateFriendRequest 推进状态机。
	UpdateFriendRequest(ctx context.Context, req FriendRequest) error
	// AcceptFriendRequest accepts a request and creates its friendship atomically. | AcceptFriendRequest 原子地接受申请并创建好友关系。
	AcceptFriendRequest(ctx context.Context, req FriendRequest, friendship Friendship) error
	// ListFriendRequests lists one direction of requests for a user. | ListFriendRequests 列出某个用户某一方向的申请。
	ListFriendRequests(ctx context.Context, tenantID, userID string, incoming bool, status FriendRequestStatus) ([]FriendRequest, error)
	// CountPendingFriendRequests counts incoming pending requests. | CountPendingFriendRequests 统计收到的待处理申请。
	CountPendingFriendRequests(ctx context.Context, tenantID, userID string) (int, error)

	// CreateFriendship stores one canonical pair. | CreateFriendship 保存一条规范化好友对。
	CreateFriendship(ctx context.Context, f Friendship) error
	// DeleteFriendship removes the canonical pair. | DeleteFriendship 删除规范化好友对。
	DeleteFriendship(ctx context.Context, tenantID, userA, userB string) error
	// GetFriendship reads the canonical pair. | GetFriendship 读取规范化好友对。
	GetFriendship(ctx context.Context, tenantID, userA, userB string) (Friendship, error)
	// ListFriendships lists every accepted friendship of one user. | ListFriendships 列出某个用户的全部已接受好友关系。
	ListFriendships(ctx context.Context, tenantID, userID string) ([]Friendship, error)

	// GetFriendNote reads the owner's remark for a friend. | GetFriendNote 读取所有者对好友的备注。
	GetFriendNote(ctx context.Context, tenantID, ownerID, friendID string) (FriendNote, error)
	// SetFriendNote upserts one remark. | SetFriendNote upsert 一条备注。
	SetFriendNote(ctx context.Context, note FriendNote) error

	// CreateBlock stores one direction of a block. | CreateBlock 保存拉黑关系的一个方向。
	CreateBlock(ctx context.Context, b Block) error
	// BlockUser stores the block, removes the friendship and closes pending requests atomically. |
	// BlockUser 原子地保存拉黑、删除好友关系并关闭待处理申请。
	BlockUser(ctx context.Context, b Block) error
	// DeleteBlock removes one direction of a block. | DeleteBlock 删除拉黑关系的一个方向。
	DeleteBlock(ctx context.Context, tenantID, blockerID, blockedID string) error
	// GetBlock reads one direction of a block. | GetBlock 读取拉黑关系的一个方向。
	GetBlock(ctx context.Context, tenantID, blockerID, blockedID string) (Block, error)
	// EitherBlocked reports whether either direction exists. | EitherBlocked 报告任一方向是否存在拉黑。
	EitherBlocked(ctx context.Context, tenantID, userA, userB string) (bool, error)
	// ListBlocks lists users blocked by one actor. | ListBlocks 列出某个主体拉黑的用户。
	ListBlocks(ctx context.Context, tenantID, blockerID string) ([]Block, error)

	// CreateConversation inserts a conversation and its initial members atomically. | CreateConversation 原子地插入会话与初始成员。
	CreateConversation(ctx context.Context, conv Conversation, members []Member) error
	// GetConversation reads one conversation. | GetConversation 读取一个会话。
	GetConversation(ctx context.Context, tenantID, id string) (Conversation, error)
	// GetDirectConversation resolves the canonical direct conversation of two users. | GetDirectConversation 解析两个用户的规范单聊。
	GetDirectConversation(ctx context.Context, tenantID, userA, userB string) (Conversation, error)
	// GetSystemConversation resolves the tenant announcement channel. | GetSystemConversation 解析租户公告频道。
	GetSystemConversation(ctx context.Context, tenantID string) (Conversation, error)
	// ListConversations lists the conversations of one user. | ListConversations 列出某个用户的会话。
	ListConversations(ctx context.Context, tenantID, userID string) ([]Conversation, error)
	// ListAllConversations lists every conversation of a tenant, for administrators. | ListAllConversations 列出租户的全部会话，供管理员使用。
	ListAllConversations(ctx context.Context, tenantID string) ([]Conversation, error)
	// UpdateConversation replaces the mutable conversation fields. | UpdateConversation 覆盖会话的可变字段。
	UpdateConversation(ctx context.Context, conv Conversation) error

	// AddMember inserts a membership; an existing membership is left untouched. | AddMember 插入成员关系；已存在时保持原样。
	AddMember(ctx context.Context, m Member) error
	// RemoveMember deletes one membership. | RemoveMember 删除一条成员关系。
	RemoveMember(ctx context.Context, tenantID, conversationID, userID string) error
	// GetMember reads one membership. | GetMember 读取一条成员关系。
	GetMember(ctx context.Context, tenantID, conversationID, userID string) (Member, error)
	// ListMembers lists every membership of a conversation. | ListMembers 列出会话的全部成员关系。
	ListMembers(ctx context.Context, tenantID, conversationID string) ([]Member, error)
	// AdvanceRead moves a member's read watermark forward, never backward. | AdvanceRead 单调推进成员的已读水位，绝不回退。
	AdvanceRead(ctx context.Context, tenantID, conversationID, userID string, at time.Time) error
	// ConversationUnread returns the unread count per conversation for one user. | ConversationUnread 返回某个用户每个会话的未读数。
	ConversationUnread(ctx context.Context, tenantID, userID string) (map[string]int, error)

	// CreateMessage inserts a message together with its tags, attachments and conversation timestamp. | CreateMessage 插入消息及其标签、附件与会话时间戳。
	CreateMessage(ctx context.Context, msg Message, tagIDs, attachmentIDs []string) error
	// GetMessage reads one message. | GetMessage 读取一条消息。
	GetMessage(ctx context.Context, tenantID, id string) (Message, error)
	// UpdateMessage replaces the editable fields and the tag set. | UpdateMessage 覆盖可编辑字段与标签集合。
	UpdateMessage(ctx context.Context, msg Message, tagIDs []string) error
	// ListMessages returns one page of messages and the total count. | ListMessages 返回一页消息与总数。
	ListMessages(ctx context.Context, tenantID, userID string, filter MessageFilter, admin bool) ([]Message, int, error)
	// ListMessagesByConversation returns one page of a conversation's messages. | ListMessagesByConversation 返回一个会话的一页消息。
	ListMessagesByConversation(ctx context.Context, tenantID, conversationID string, page, pageSize int, includeRecalled bool) ([]Message, int, error)
	// LastMessages resolves the newest visible message per conversation. | LastMessages 解析每个会话最新的可见消息。
	LastMessages(ctx context.Context, tenantID string, conversationIDs []string) (map[string]Message, error)
	// ListPinned lists active pinned system messages visible to one user. | ListPinned 列出某个用户可见的生效置顶系统消息。
	ListPinned(ctx context.Context, tenantID, userID string, now time.Time) ([]Message, error)
	// RecallMessage advances a message to the tombstone state, atomically with its audit entry. | RecallMessage 原子地把消息推进到墓碑状态并写入审计。
	RecallMessage(ctx context.Context, msg Message, audit AuditEntry) error
	// SetPin replaces the pin fields, atomically with its audit entry. | SetPin 原子地覆盖置顶字段并写入审计。
	SetPin(ctx context.Context, msg Message, audit AuditEntry) error

	// MessageAttachments resolves attachments for a batch of messages. | MessageAttachments 批量解析消息的附件。
	MessageAttachments(ctx context.Context, tenantID string, messageIDs []string) (map[string][]Attachment, error)
	// MessageTags resolves groups and tags for a batch of messages. | MessageTags 批量解析消息的分组与标签。
	MessageTags(ctx context.Context, tenantID string, messageIDs []string) (map[string][]Tag, error)

	// CreateAttachment stores one pending upload. | CreateAttachment 保存一个待挂载上传。
	CreateAttachment(ctx context.Context, a Attachment) error
	// GetAttachment reads one attachment. | GetAttachment 读取一个附件。
	GetAttachment(ctx context.Context, tenantID, id string) (Attachment, error)
	// CleanupPending removes pending attachments older than the cutoff and returns them so their
	// blobs can be deleted. | CleanupPending 删除早于截止时间的待挂载附件并返回它们，以便删除对应字节。
	CleanupPending(ctx context.Context, before time.Time) ([]Attachment, error)

	// AddAudit appends one audit entry. | AddAudit 追加一条审计记录。
	AddAudit(ctx context.Context, entry AuditEntry) error
	// ListAudit returns one page of audit entries. | ListAudit 返回一页审计记录。
	ListAudit(ctx context.Context, tenantID string, filter AuditFilter) ([]AuditEntry, int, error)
}
