package messaging

import (
	"strings"
	"time"
	"unicode/utf8"
)

// ConversationKind tells a direct chat, an ad-hoc group and the tenant announcement channel apart. |
// ConversationKind 区分单聊、临时群聊与租户公告频道。
type ConversationKind string

// Conversation kinds. | 会话类型。
const (
	// KindDirect is a one-to-one conversation between two friends. | KindDirect 是两个好友之间的一对一会话。
	KindDirect ConversationKind = "direct"
	// KindGroup is a group conversation created from a set of friends. | KindGroup 是由一组好友创建的群会话。
	KindGroup ConversationKind = "group"
	// KindSystem is the tenant announcement channel; only message:system may post into it. | KindSystem 是租户公告频道，只有 message:system 可以写入。
	KindSystem ConversationKind = "system"
)

// ConversationStatus is the lifecycle state of a conversation. | ConversationStatus 是会话的生命周期状态。
type ConversationStatus string

// Conversation statuses. | 会话状态。
const (
	// ConversationActive accepts new messages. | ConversationActive 接受新消息。
	ConversationActive ConversationStatus = "active"
	// ConversationDissolved is read-only and refuses new messages. | ConversationDissolved 只读并拒绝新消息。
	ConversationDissolved ConversationStatus = "dissolved"
)

// MemberRole is the role of one member inside a group. | MemberRole 是成员在群内的角色。
type MemberRole string

// Member roles. | 成员角色。
const (
	// MemberOwner may rename, add, remove and dissolve a group. | MemberOwner 可以改名、增删成员并解散群。
	MemberOwner MemberRole = "owner"
	// MemberPlain is an ordinary group member. | MemberPlain 是普通群成员。
	MemberPlain MemberRole = "member"
)

// MessageOrigin tells a user message from an administrator announcement. | MessageOrigin 区分用户消息与管理员公告。
type MessageOrigin string

// Message origins. | 消息来源。
const (
	// OriginUser is a message written by a regular conversation member. | OriginUser 是会话成员写的消息。
	OriginUser MessageOrigin = "user"
	// OriginSystem is an administrator announcement. | OriginSystem 是管理员公告。
	OriginSystem MessageOrigin = "system"
)

// MessageStatus is the lifecycle state of one message. | MessageStatus 是一条消息的生命周期状态。
type MessageStatus string

// Message statuses. | 消息状态。
const (
	// StatusSent is visible to its audience. | StatusSent 对受众可见。
	StatusSent MessageStatus = "sent"
	// StatusRecalled is a tombstone: the body is retained for audit and hidden from regular readers. | StatusRecalled 是墓碑：正文保留供审计，但普通读者看不到。
	StatusRecalled MessageStatus = "recalled"
)

// FriendRequestStatus is the state machine of a friend request. | FriendRequestStatus 是好友申请的状态机。
type FriendRequestStatus string

// Friend request statuses. | 好友申请状态。
const (
	// FriendPending waits for the addressee. | FriendPending 等待被申请人处理。
	FriendPending FriendRequestStatus = "pending"
	// FriendAccepted produced a friendship. | FriendAccepted 已生成好友关系。
	FriendAccepted FriendRequestStatus = "accepted"
	// FriendRejected was declined by the addressee. | FriendRejected 已被被申请人拒绝。
	FriendRejected FriendRequestStatus = "rejected"
	// FriendCancelled was withdrawn by the requester. | FriendCancelled 已被申请人取消。
	FriendCancelled FriendRequestStatus = "cancelled"
)

// TagKind tells a group marker from a free-form tag. | TagKind 区分分组标记与自由标签。
type TagKind string

// Tag kinds. | 标签类型。
const (
	// TagGroup groups messages into an administrator-defined family. | TagGroup 把消息归入管理员定义的分组。
	TagGroup TagKind = "group"
	// TagLabel is a free-form multi-select tag. | TagLabel 是可多选自由标签。
	TagLabel TagKind = "tag"
)

// AttachmentStatus is the lifecycle of an uploaded file. | AttachmentStatus 是已上传文件的生命周期。
type AttachmentStatus string

// Attachment statuses. | 附件状态。
const (
	// AttachmentPending was uploaded but not yet bound to a message. | AttachmentPending 已上传但尚未绑定消息。
	AttachmentPending AttachmentStatus = "pending"
	// AttachmentAttached belongs to a sent message. | AttachmentAttached 已属于一条已发送消息。
	AttachmentAttached AttachmentStatus = "attached"
)

// Tone names a semantic colour role instead of a literal colour. | Tone 命名语义色角色，而不是颜色字面量。
const (
	// ToneNeutral is the default neutral tone. | ToneNeutral 是默认中性色。
	ToneNeutral = "neutral"
	// ToneInfo marks informational content. | ToneInfo 表示信息类内容。
	ToneInfo = "info"
	// ToneSuccess marks completed or positive content. | ToneSuccess 表示完成或正向内容。
	ToneSuccess = "success"
	// ToneWarn marks attention-worthy content. | ToneWarn 表示需要注意的内容。
	ToneWarn = "warn"
	// ToneDanger marks destructive or critical content. | ToneDanger 表示破坏性或关键内容。
	ToneDanger = "danger"
)

// Conversation is one direct, group or system conversation. | Conversation 是一个单聊、群聊或系统会话。
type Conversation struct {
	// CreatedAt is the UTC creation time. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime;index:idx_msg_conv_tenant_created,priority:2"`
	// UpdatedAt is the UTC last mutation time. | UpdatedAt 是 UTC 最后修改时间。
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
	// LastMessageAt orders the conversation list; nil means no message yet. | LastMessageAt 用于会话列表排序；nil 表示尚无消息。
	LastMessageAt *time.Time `json:"last_message_at" gorm:"column:last_message_at"`
	// DirectKey is the canonical pair key for direct conversations and NULL otherwise. | DirectKey 是单聊的规范对键，其它会话为 NULL。
	DirectKey *string `json:"-" gorm:"column:direct_key;type:varchar(160);uniqueIndex:uidx_msg_conv_direct,priority:2"`
	// SystemKey is the fixed announcement key for the tenant system channel and NULL otherwise. | SystemKey 是租户公告频道的固定键，其它会话为 NULL。
	SystemKey *string `json:"-" gorm:"column:system_key;type:varchar(64);uniqueIndex:uidx_msg_conv_system,priority:2"`
	// ID is the primary key, prefixed "cv_". | ID 是主键，前缀为 cv_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the hard tenant boundary. | TenantID 是硬租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;index:idx_msg_conv_tenant_created,priority:1;uniqueIndex:uidx_msg_conv_direct,priority:1;uniqueIndex:uidx_msg_conv_system,priority:1"`
	// Kind is direct, group or system. | Kind 是 direct、group 或 system。
	Kind ConversationKind `json:"kind" gorm:"column:kind;type:varchar(16);not null;index:idx_msg_conv_kind"`
	// Title is the group name; direct and system conversations derive their display name. | Title 是群名；单聊与系统会话由展示层推导。
	Title string `json:"title" gorm:"column:title;type:varchar(160)"`
	// CreatedBy is the user id that opened the conversation. | CreatedBy 是创建会话的用户 ID。
	CreatedBy string `json:"created_by" gorm:"column:created_by;type:varchar(64);not null"`
	// Status is active or dissolved. | Status 取 active 或 dissolved。
	Status ConversationStatus `json:"status" gorm:"column:status;type:varchar(16);not null;index:idx_msg_conv_status"`
}

// TableName pins the table name. | TableName 固定表名。
func (Conversation) TableName() string { return "yd_msg_conversations" }

// Member binds one user to one conversation and carries the read watermark. | Member 把一个用户绑定到一个会话，并保存已读水位。
type Member struct {
	// JoinedAt is when the user entered the conversation; messages before it are unread only by history. | JoinedAt 是加入时间；此前的消息只作为历史，不计未读。
	JoinedAt time.Time `json:"joined_at" gorm:"column:joined_at;autoCreateTime"`
	// LastReadAt advances monotonically when the user opens the thread. | LastReadAt 在用户打开会话时单调推进。
	LastReadAt *time.Time `json:"last_read_at" gorm:"column:last_read_at"`
	// ID is the primary key, prefixed "mb_". | ID 是主键，前缀为 mb_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_msg_member,priority:1;index:idx_msg_member_user,priority:1"`
	// ConversationID is the conversation this membership belongs to. | ConversationID 是成员关系所属的会话。
	ConversationID string `json:"conversation_id" gorm:"column:conversation_id;type:varchar(64);not null;uniqueIndex:uidx_msg_member,priority:2;index:idx_msg_member_user,priority:2"`
	// UserID is the member. | UserID 是成员用户。
	UserID string `json:"user_id" gorm:"column:user_id;type:varchar(64);not null;uniqueIndex:uidx_msg_member,priority:3;index:idx_msg_member_user,priority:3"`
	// Role is owner or member. | Role 取 owner 或 member。
	Role MemberRole `json:"role" gorm:"column:role;type:varchar(16);not null"`
}

// TableName pins the table name. | TableName 固定表名。
func (Member) TableName() string { return "yd_msg_members" }

// Message is one message with its rendered and searchable snapshots. | Message 是一条消息，包含渲染与检索快照。
type Message struct {
	// CreatedAt is the UTC send time. | CreatedAt 是 UTC 发送时间。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime;index:idx_msg_message_thread,priority:3;index:idx_msg_message_tenant_created,priority:2"`
	// UpdatedAt is the UTC last mutation time. | UpdatedAt 是 UTC 最后修改时间。
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
	// EditedAt is non-nil after an edit. | EditedAt 在编辑后非空。
	EditedAt *time.Time `json:"edited_at" gorm:"column:edited_at"`
	// RecalledAt is non-nil after a recall. | RecalledAt 在撤回后非空。
	RecalledAt *time.Time `json:"recalled_at" gorm:"column:recalled_at"`
	// PinStartAt is the optional start of the pin window. | PinStartAt 是置顶窗口的可选起点。
	PinStartAt *time.Time `json:"pin_start_at" gorm:"column:pin_start_at"`
	// PinEndAt is the optional end of the pin window. | PinEndAt 是置顶窗口的可选终点。
	PinEndAt *time.Time `json:"pin_end_at" gorm:"column:pin_end_at"`
	// PinnedAt records when the pin was applied. | PinnedAt 记录置顶时间。
	PinnedAt *time.Time `json:"pinned_at" gorm:"column:pinned_at"`
	// ID is the primary key, prefixed "msg_". | ID 是主键，前缀为 msg_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;index:idx_msg_message_thread,priority:1;index:idx_msg_message_tenant_created,priority:1;index:idx_msg_message_sender,priority:1;index:idx_msg_message_type,priority:1"`
	// ConversationID is the owning conversation. | ConversationID 是所属会话。
	ConversationID string `json:"conversation_id" gorm:"column:conversation_id;type:varchar(64);not null;index:idx_msg_message_thread,priority:2"`
	// SenderID is the author. | SenderID 是作者。
	SenderID string `json:"sender_id" gorm:"column:sender_id;type:varchar(64);not null;index:idx_msg_message_sender,priority:2"`
	// SenderName is the display name snapshot used by lists and search. | SenderName 是列表与检索使用的展示名快照。
	SenderName string `json:"sender_name" gorm:"column:sender_name;type:varchar(128)"`
	// Origin is user or system. | Origin 取 user 或 system。
	Origin MessageOrigin `json:"origin" gorm:"column:origin;type:varchar(16);not null;index:idx_msg_message_origin,priority:1"`
	// TypeKey is the message type at send time. | TypeKey 是发送时的消息类型。
	TypeKey string `json:"type_key" gorm:"column:type_key;type:varchar(64);not null;index:idx_msg_message_type,priority:2"`
	// TypeName is the Chinese type label snapshot. | TypeName 是中文类型标签快照。
	TypeName string `json:"type_name" gorm:"column:type_name;type:varchar(128)"`
	// TypeNameEN is the English type label snapshot. | TypeNameEN 是英文类型标签快照。
	TypeNameEN string `json:"type_name_en" gorm:"column:type_name_en;type:varchar(128)"`
	// Tone is the semantic colour role snapshot. | Tone 是语义色角色快照。
	Tone string `json:"tone" gorm:"column:tone;type:varchar(16)"`
	// Title is the optional subject. | Title 是可选标题。
	Title string `json:"title" gorm:"column:title;type:varchar(160)"`
	// BodyMarkdown is the editable source. | BodyMarkdown 是可编辑源。
	BodyMarkdown string `json:"body_markdown" gorm:"column:body_markdown;type:text"`
	// BodyHTML is the server-rendered, sanitised HTML snapshot. | BodyHTML 是服务端渲染并净化的 HTML 快照。
	BodyHTML string `json:"body_html" gorm:"column:body_html;type:text"`
	// BodyText is the plain-text projection used by previews and search. | BodyText 是用于预览与检索的纯文本投影。
	BodyText string `json:"body_text" gorm:"column:body_text;type:text"`
	// SearchText is the lower-cased concatenation searched by ListMessages. | SearchText 是 ListMessages 检索的小写拼接文本。
	SearchText string `json:"-" gorm:"column:search_text;type:text"`
	// Status is sent or recalled. | Status 取 sent 或 recalled。
	Status MessageStatus `json:"status" gorm:"column:status;type:varchar(16);not null;index:idx_msg_message_status"`
	// RecalledBy is the actor that recalled the message. | RecalledBy 是执行撤回的主体。
	RecalledBy string `json:"recalled_by" gorm:"column:recalled_by;type:varchar(64)"`
	// RecallReason is the optional explanation. | RecallReason 是可选说明。
	RecallReason string `json:"recall_reason" gorm:"column:recall_reason;type:varchar(160)"`
	// Pinned marks a pinned system message; the window decides whether it is currently active. | Pinned 标记已置顶的系统消息；生效与否由窗口决定。
	Pinned bool `json:"pinned" gorm:"column:pinned;not null;default:false;index:idx_msg_message_pinned,priority:2"`
}

// TableName pins the table name. | TableName 固定表名。
func (Message) TableName() string { return "yd_msg_messages" }

// Label returns the type label for a language tag, falling back to the other language. | Label 按语言标签返回类型名，缺失时回退到另一种语言。
func (m Message) Label(lang string) string {
	if isChinese(lang) {
		if strings.TrimSpace(m.TypeName) != "" {
			return m.TypeName
		}
		return m.TypeNameEN
	}
	if strings.TrimSpace(m.TypeNameEN) != "" {
		return m.TypeNameEN
	}
	return m.TypeName
}

// Recalled reports whether the message is a tombstone. | Recalled 报告消息是否为墓碑。
func (m Message) Recalled() bool { return m.Status == StatusRecalled }

// PinnedNow reports whether the pin is active at now. | PinnedNow 报告置顶在 now 时刻是否生效。
func (m Message) PinnedNow(now time.Time) bool {
	if !m.Pinned || m.Status == StatusRecalled {
		return false
	}
	if m.PinStartAt != nil && now.Before(*m.PinStartAt) {
		return false
	}
	if m.PinEndAt != nil && !now.Before(*m.PinEndAt) {
		return false
	}
	return true
}

// MessageType is an administrator-managed message category. | MessageType 是管理员维护的消息分类。
type MessageType struct {
	// CreatedAt is the UTC creation time. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	// UpdatedAt is the UTC last mutation time. | UpdatedAt 是 UTC 最后修改时间。
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
	// ArchivedAt is non-nil after archival. | ArchivedAt 在归档后非空。
	ArchivedAt *time.Time `json:"archived_at" gorm:"column:archived_at"`
	// ID is the primary key, prefixed "mt_". | ID 是主键，前缀为 mt_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_msg_type_key,priority:1;index:idx_msg_type_archived,priority:1"`
	// Key is the stable slug used by messages. | Key 是消息引用的稳定标识。
	Key string `json:"key" gorm:"column:type_key;type:varchar(64);not null;uniqueIndex:uidx_msg_type_key,priority:2"`
	// Name is the Chinese label. | Name 是中文标签。
	Name string `json:"name" gorm:"column:name;type:varchar(128);not null"`
	// NameEN is the English label. | NameEN 是英文标签。
	NameEN string `json:"name_en" gorm:"column:name_en;type:varchar(128);not null"`
	// Tone is the semantic colour role. | Tone 是语义色角色。
	Tone string `json:"tone" gorm:"column:tone;type:varchar(16);not null;default:neutral"`
	// Builtin marks the five seeded types. | Builtin 标记五个内置类型。
	Builtin bool `json:"builtin" gorm:"column:builtin;not null;default:false"`
	// Protected marks the system type that cannot be archived or removed. | Protected 标记不可归档或删除的系统类型。
	Protected bool `json:"protected" gorm:"column:protected;not null;default:false"`
}

// TableName pins the table name. | TableName 固定表名。
func (MessageType) TableName() string { return "yd_msg_types" }

// Label returns the label for a language tag. | Label 按语言标签返回标签名。
func (t MessageType) Label(lang string) string {
	if isChinese(lang) {
		if strings.TrimSpace(t.Name) != "" {
			return t.Name
		}
		return t.NameEN
	}
	if strings.TrimSpace(t.NameEN) != "" {
		return t.NameEN
	}
	return t.Name
}

// Archived reports whether the type is retired for new messages. | Archived 报告类型是否已停止用于新消息。
func (t MessageType) Archived() bool { return t.ArchivedAt != nil }

// Tag is an administrator-managed group marker or free-form label. | Tag 是管理员维护的分组标记或自由标签。
type Tag struct {
	// CreatedAt is the UTC creation time. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	// UpdatedAt is the UTC last mutation time. | UpdatedAt 是 UTC 最后修改时间。
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
	// ArchivedAt is non-nil after archival. | ArchivedAt 在归档后非空。
	ArchivedAt *time.Time `json:"archived_at" gorm:"column:archived_at"`
	// ID is the primary key, prefixed "tg_". | ID 是主键，前缀为 tg_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_msg_tag_key,priority:1"`
	// Kind is group or tag. | Kind 取 group 或 tag。
	Kind TagKind `json:"kind" gorm:"column:kind;type:varchar(16);not null;uniqueIndex:uidx_msg_tag_key,priority:2;index:idx_msg_tag_kind,priority:2"`
	// Key is the stable slug. | Key 是稳定标识。
	Key string `json:"key" gorm:"column:tag_key;type:varchar(64);not null;uniqueIndex:uidx_msg_tag_key,priority:3"`
	// Name is the Chinese label. | Name 是中文标签。
	Name string `json:"name" gorm:"column:name;type:varchar(128);not null"`
	// NameEN is the English label. | NameEN 是英文标签。
	NameEN string `json:"name_en" gorm:"column:name_en;type:varchar(128);not null"`
	// Tone is the semantic colour role. | Tone 是语义色角色。
	Tone string `json:"tone" gorm:"column:tone;type:varchar(16);not null;default:neutral"`
}

// TableName pins the table name. | TableName 固定表名。
func (Tag) TableName() string { return "yd_msg_tags" }

// Label returns the label for a language tag. | Label 按语言标签返回标签名。
func (t Tag) Label(lang string) string {
	if isChinese(lang) {
		if strings.TrimSpace(t.Name) != "" {
			return t.Name
		}
		return t.NameEN
	}
	if strings.TrimSpace(t.NameEN) != "" {
		return t.NameEN
	}
	return t.Name
}

// Archived reports whether the tag is retired for new messages. | Archived 报告标签是否已停止用于新消息。
func (t Tag) Archived() bool { return t.ArchivedAt != nil }

// MessageTag binds one message to one group or tag. | MessageTag 把一条消息绑定到一个分组或标签。
type MessageTag struct {
	// ID is the primary key, prefixed "mtg_". | ID 是主键，前缀为 mtg_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_msg_message_tag,priority:1;index:idx_msg_message_tag_tag,priority:1"`
	// MessageID is the message. | MessageID 是消息。
	MessageID string `json:"message_id" gorm:"column:message_id;type:varchar(64);not null;uniqueIndex:uidx_msg_message_tag,priority:2;index:idx_msg_message_tag_tag,priority:2"`
	// TagID is the group or tag. | TagID 是分组或标签。
	TagID string `json:"tag_id" gorm:"column:tag_id;type:varchar(64);not null;uniqueIndex:uidx_msg_message_tag,priority:3;index:idx_msg_message_tag_tag,priority:3"`
}

// TableName pins the table name. | TableName 固定表名。
func (MessageTag) TableName() string { return "yd_msg_message_tags" }

// Attachment is one uploaded file, pending or bound to a message. | Attachment 是一个已上传文件，待挂载或已绑定消息。
type Attachment struct {
	// CreatedAt is the UTC upload time. | CreatedAt 是 UTC 上传时间。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime;index:idx_msg_attachment_pending,priority:3"`
	// MessageID is nil while pending. | MessageID 在待挂载时为空。
	MessageID *string `json:"message_id" gorm:"column:message_id;type:varchar(64);index:idx_msg_attachment_message"`
	// ID is the primary key, prefixed "att_". | ID 是主键，前缀为 att_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;index:idx_msg_attachment_pending,priority:1"`
	// UploaderID is the user that uploaded the file. | UploaderID 是上传者。
	UploaderID string `json:"uploader_id" gorm:"column:uploader_id;type:varchar(64);not null;index:idx_msg_attachment_pending,priority:2"`
	// OriginalName is the file name as submitted, escaped on display. | OriginalName 是提交时的文件名，展示时转义。
	OriginalName string `json:"name" gorm:"column:original_name;type:varchar(255);not null"`
	// StoredKey is the opaque storage key; it never contains the original name. | StoredKey 是不透明存储键，绝不包含原始文件名。
	StoredKey string `json:"-" gorm:"column:stored_key;type:varchar(255);not null"`
	// ContentType is the sniffed MIME type. | ContentType 是嗅探出的 MIME 类型。
	ContentType string `json:"content_type" gorm:"column:content_type;type:varchar(128);not null"`
	// SHA256 is the lowercase hex digest of the content. | SHA256 是内容的小写十六进制摘要。
	SHA256 string `json:"sha256" gorm:"column:sha256;type:varchar(64);not null"`
	// Status is pending or attached. | Status 取 pending 或 attached。
	Status AttachmentStatus `json:"status" gorm:"column:status;type:varchar(16);not null;index:idx_msg_attachment_pending,priority:4"`
	// Size is the byte length. | Size 是字节长度。
	Size int64 `json:"size" gorm:"column:size;not null"`
}

// TableName pins the table name. | TableName 固定表名。
func (Attachment) TableName() string { return "yd_msg_attachments" }

// FriendRequest is one directed request in the friend lifecycle. | FriendRequest 是好友生命周期中的一条有向申请。
type FriendRequest struct {
	// CreatedAt is the UTC request time. | CreatedAt 是 UTC 申请时间。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime;index:idx_msg_friend_request_addressee,priority:2"`
	// RespondedAt is set when the request leaves pending. | RespondedAt 在申请离开 pending 时写入。
	RespondedAt *time.Time `json:"responded_at" gorm:"column:responded_at"`
	// ID is the primary key, prefixed "fr_". | ID 是主键，前缀为 fr_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_msg_friend_request,priority:1;index:idx_msg_friend_request_addressee,priority:1"`
	// RequesterID is the sender of the request. | RequesterID 是申请人。
	RequesterID string `json:"requester_id" gorm:"column:requester_id;type:varchar(64);not null;uniqueIndex:uidx_msg_friend_request,priority:2"`
	// AddresseeID is the target. | AddresseeID 是被申请人。
	AddresseeID string `json:"addressee_id" gorm:"column:addressee_id;type:varchar(64);not null;uniqueIndex:uidx_msg_friend_request,priority:3"`
	// Message is an optional greeting. | Message 是可选招呼语。
	Message string `json:"message" gorm:"column:message;type:varchar(255)"`
	// Status follows the friend state machine. | Status 遵循好友状态机。
	Status FriendRequestStatus `json:"status" gorm:"column:status;type:varchar(16);not null"`
}

// TableName pins the table name. | TableName 固定表名。
func (FriendRequest) TableName() string { return "yd_msg_friend_requests" }

// Friendship is one accepted undirected pair stored in canonical id order. | Friendship 是一条已接受的无向关系，按规范化 ID 顺序存储。
type Friendship struct {
	// CreatedAt is the UTC acceptance time. | CreatedAt 是 UTC 接受时间。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	// ID is the primary key, prefixed "fs_". | ID 是主键，前缀为 fs_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_msg_friendship_pair,priority:1;index:idx_msg_friendship_low,priority:1;index:idx_msg_friendship_high,priority:1"`
	// UserLowID is the lexicographically smaller user id. | UserLowID 是字典序较小的用户 ID。
	UserLowID string `json:"user_low_id" gorm:"column:user_low_id;type:varchar(64);not null;uniqueIndex:uidx_msg_friendship_pair,priority:2;index:idx_msg_friendship_low,priority:2"`
	// UserHighID is the lexicographically larger user id. | UserHighID 是字典序较大的用户 ID。
	UserHighID string `json:"user_high_id" gorm:"column:user_high_id;type:varchar(64);not null;uniqueIndex:uidx_msg_friendship_pair,priority:3;index:idx_msg_friendship_high,priority:2"`
}

// TableName pins the table name. | TableName 固定表名。
func (Friendship) TableName() string { return "yd_msg_friendships" }

// FriendNote is a per-owner remark for one friend. | FriendNote 是一个所有者对一位好友的备注。
type FriendNote struct {
	// ID is the primary key, prefixed "fn_". | ID 是主键，前缀为 fn_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// UpdatedAt is the UTC last edit time. | UpdatedAt 是 UTC 最后编辑时间。
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_msg_friend_note,priority:1"`
	// OwnerID is the user that set the remark. | OwnerID 是设置备注的用户。
	OwnerID string `json:"owner_id" gorm:"column:owner_id;type:varchar(64);not null;uniqueIndex:uidx_msg_friend_note,priority:2"`
	// FriendID is the friend the remark describes. | FriendID 是备注指向的好友。
	FriendID string `json:"friend_id" gorm:"column:friend_id;type:varchar(64);not null;uniqueIndex:uidx_msg_friend_note,priority:3"`
	// Remark is the alias shown instead of the profile name. | Remark 是替代资料名展示的备注。
	Remark string `json:"remark" gorm:"column:remark;type:varchar(128)"`
}

// TableName pins the table name. | TableName 固定表名。
func (FriendNote) TableName() string { return "yd_msg_friend_notes" }

// Block is one direction of a block relationship. | Block 是拉黑关系的一个方向。
type Block struct {
	// ID is the primary key, prefixed "bk_". | ID 是主键，前缀为 bk_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// CreatedAt is the UTC block time. | CreatedAt 是 UTC 拉黑时间。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_msg_block,priority:1;index:idx_msg_block_blocked,priority:1"`
	// BlockerID is the user that blocked. | BlockerID 是拉黑者。
	BlockerID string `json:"blocker_id" gorm:"column:blocker_id;type:varchar(64);not null;uniqueIndex:uidx_msg_block,priority:2"`
	// BlockedID is the user that was blocked. | BlockedID 是被拉黑者。
	BlockedID string `json:"blocked_id" gorm:"column:blocked_id;type:varchar(64);not null;uniqueIndex:uidx_msg_block,priority:3;index:idx_msg_block_blocked,priority:2"`
}

// TableName pins the table name. | TableName 固定表名。
func (Block) TableName() string { return "yd_msg_blocks" }

// AuditEntry is one administrator or lifecycle action kept for later review. | AuditEntry 是为后续审查保留的一条管理或生命周期动作。
type AuditEntry struct {
	// CreatedAt is the UTC action time. | CreatedAt 是 UTC 动作时间。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime;index:idx_msg_audit_tenant_created,priority:2"`
	// ID is the primary key, prefixed "au_". | ID 是主键，前缀为 au_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;index:idx_msg_audit_tenant_created,priority:1;index:idx_msg_audit_message,priority:1"`
	// ActorID is the user that performed the action. | ActorID 是执行动作的用户。
	ActorID string `json:"actor_id" gorm:"column:actor_id;type:varchar(64);not null"`
	// Action is a stable identifier such as "message.recall". | Action 是稳定标识，例如 message.recall。
	Action string `json:"action" gorm:"column:action;type:varchar(64);not null"`
	// MessageID is set for message actions. | MessageID 在消息动作上写入。
	MessageID string `json:"message_id" gorm:"column:message_id;type:varchar(64);index:idx_msg_audit_message,priority:2"`
	// ConversationID is set for conversation actions. | ConversationID 在会话动作上写入。
	ConversationID string `json:"conversation_id" gorm:"column:conversation_id;type:varchar(64)"`
	// Detail is a JSON document with the action-specific snapshot. | Detail 是携带动作快照的 JSON 文档。
	Detail string `json:"detail" gorm:"column:detail;type:text"`
}

// TableName pins the table name. | TableName 固定表名。
func (AuditEntry) TableName() string { return "yd_msg_audit" }

// Profile is the subset of a tenant user the message centre needs. | Profile 是消息中心所需的租户用户子集。
type Profile struct {
	// ID is the global user id. | ID 是全局用户 ID。
	ID string `json:"id"`
	// Username is the globally unique sign-in name. | Username 是全局唯一登录名。
	Username string `json:"username"`
	// DisplayName is the optional human-readable name. | DisplayName 是可选的展示名。
	DisplayName string `json:"display_name"`
	// Email is the optional contact address. | Email 是可选联络邮箱。
	Email string `json:"email"`
	// Active reports whether the account may sign in. | Active 报告账号是否可登录。
	Active bool `json:"active"`
}

// Display returns the preferred human-readable name. | Display 返回优先展示的人类可读名称。
func (p Profile) Display() string {
	if name := strings.TrimSpace(p.DisplayName); name != "" {
		return name
	}
	return p.Username
}

// Initial returns the first rune of the display name, upper-cased. | Initial 返回展示名的第一个符文并转大写。
func (p Profile) Initial() string {
	value := strings.TrimSpace(p.Display())
	if value == "" {
		return "?"
	}
	first, _ := utf8.DecodeRuneInString(value)
	if first == utf8.RuneError {
		return "?"
	}
	return strings.ToUpper(string(first))
}

// MessageView is one message with its attachment, tag and read-count projections. | MessageView 是一条消息及其附件、标签与已读计数投影。
type MessageView struct {
	// ConversationKind is the kind of the owning conversation. | ConversationKind 是所属会话的类型。
	ConversationKind ConversationKind `json:"conversation_kind"`
	// ConversationTitle is the stored group title; direct and system titles are derived by the UI. |
	// ConversationTitle 是存储的群名；单聊与系统会话标题由前端推导。
	ConversationTitle string `json:"conversation_title"`
	// Message is the stored message. | Message 是存储的消息。
	Message
	// Attachments lists the files bound to the message. | Attachments 列出绑定到消息的文件。
	Attachments []Attachment `json:"attachments"`
	// Tags lists the free-form labels. | Tags 列出自由标签。
	Tags []Tag `json:"tags"`
	// Groups lists the group markers. | Groups 列出分组标记。
	Groups []Tag `json:"groups"`
	// ReadCount is how many other members have read past this message. | ReadCount 是已读越过该消息的其他成员数。
	ReadCount int `json:"read_count"`
	// RecipientCount is the number of members that should read it. | RecipientCount 是应当阅读它的成员数。
	RecipientCount int `json:"recipient_count"`
	// Mine reports whether the actor wrote it. | Mine 报告当前主体是否作者。
	Mine bool `json:"mine"`
}

// ConversationView is one conversation with its peer, last message and unread count. | ConversationView 是一个会话及其对端、最后一条消息与未读数。
type ConversationView struct {
	// Peer is the other participant of a direct conversation. | Peer 是单聊的另一方。
	Peer *Profile `json:"peer,omitempty"`
	// LastMessage is the newest visible message. | LastMessage 是可见的最新消息。
	LastMessage *Message `json:"last_message,omitempty"`
	// Conversation is the stored conversation. | Conversation 是存储的会话。
	Conversation
	// Members lists the member profiles (display names resolved). | Members 列出成员资料（已解析展示名）。
	Members []Profile `json:"members"`
	// Unread is the number of unread messages for the actor. | Unread 是当前主体的未读数。
	Unread int `json:"unread"`
}

// FriendView is one accepted friend with an optional remark. | FriendView 是一位已接受好友及其可选备注。
type FriendView struct {
	// Since is the UTC acceptance time. | Since 是 UTC 接受时间。
	Since time.Time `json:"since"`
	// Remark is the owner's alias. | Remark 是所有者设置的备注。
	Remark string `json:"remark"`
	// Profile is the friend's profile. | Profile 是好友资料。
	Profile Profile `json:"profile"`
}

// Display returns the remark when set, otherwise the profile display name. | Display 优先返回备注，否则返回资料展示名。
func (f FriendView) Display() string {
	if remark := strings.TrimSpace(f.Remark); remark != "" {
		return remark
	}
	return f.Profile.Display()
}

// FriendRequestView is one request with both profiles resolved. | FriendRequestView 是一条申请及双方资料。
type FriendRequestView struct {
	// Request is the stored request. | Request 是存储的申请。
	FriendRequest
	// Requester is the sender profile. | Requester 是申请人资料。
	Requester Profile `json:"requester"`
	// Addressee is the target profile. | Addressee 是被申请人资料。
	Addressee Profile `json:"addressee"`
	// Incoming reports whether the actor is the addressee. | Incoming 报告当前主体是否被申请人。
	Incoming bool `json:"incoming"`
}

// FriendSummary is the friend page payload. | FriendSummary 是好友页数据。
type FriendSummary struct {
	// Friends lists accepted friends. | Friends 列出已接受好友。
	Friends []FriendView `json:"friends"`
	// Incoming lists pending requests addressed to the actor. | Incoming 列出指向当前主体的待处理申请。
	Incoming []FriendRequestView `json:"incoming"`
	// Outgoing lists pending requests sent by the actor. | Outgoing 列出当前主体发出的待处理申请。
	Outgoing []FriendRequestView `json:"outgoing"`
	// Blocked lists users the actor blocked. | Blocked 列出当前主体拉黑的用户。
	Blocked []Profile `json:"blocked"`
}

// FriendCandidate is one search result with the current relation state. | FriendCandidate 是一条检索结果及其当前关系状态。
type FriendCandidate struct {
	// Relation is none, friend, incoming, outgoing or blocked. | Relation 取 none、friend、incoming、outgoing 或 blocked。
	Relation string `json:"relation"`
	// Profile is the candidate. | Profile 是候选用户。
	Profile Profile `json:"profile"`
}

// Friend relation labels used by FriendCandidate. | FriendCandidate 使用的关系标签。
const (
	// RelationNone means no relation exists. | RelationNone 表示尚无关系。
	RelationNone = "none"
	// RelationFriend means an accepted friendship exists. | RelationFriend 表示已存在好友关系。
	RelationFriend = "friend"
	// RelationIncoming means the candidate sent a pending request. | RelationIncoming 表示候选用户发来了待处理申请。
	RelationIncoming = "incoming"
	// RelationOutgoing means the actor sent a pending request. | RelationOutgoing 表示当前主体已发出待处理申请。
	RelationOutgoing = "outgoing"
	// RelationBlocked means either side blocked the other. | RelationBlocked 表示任一方拉黑了对方。
	RelationBlocked = "blocked"
	// RelationSelf means the target is the actor itself. Search excludes the actor, so only the request drawer sees it. |
	// RelationSelf 表示目标就是主体本人。检索会排除主体，因此只有好友申请抽屉会遇到它。
	RelationSelf = "self"
)

// UnreadSummary is the badge payload rendered in the shell. | UnreadSummary 是外壳徽标使用的未读汇总。
type UnreadSummary struct {
	// Total is the number of unread messages across conversations. | Total 是全部会话的未读消息数。
	Total int `json:"total"`
	// System is the unread count in the announcement channel. | System 是公告频道的未读数。
	System int `json:"system"`
	// FriendRequests is the number of pending incoming requests. | FriendRequests 是指向当前主体的待处理好友申请数。
	FriendRequests int `json:"friend_requests"`
}

// Message folders accepted by MessageFilter.Folder. | MessageFilter.Folder 接受的消息文件夹。
const (
	// FolderAll lists every message visible to the actor. | FolderAll 列出主体可见的全部消息。
	FolderAll = "all"
	// FolderSent lists messages written by the actor. | FolderSent 列出主体发送的消息。
	FolderSent = "sent"
	// FolderSystem lists administrator announcements. | FolderSystem 列出管理员公告。
	FolderSystem = "system"
	// FolderUnread lists unread messages. | FolderUnread 列出未读消息。
	FolderUnread = "unread"
	// FolderPinned lists active pinned system messages. | FolderPinned 列出生效的置顶系统消息。
	FolderPinned = "pinned"
)

// MessageFilter selects a page of messages. | MessageFilter 选择一页消息。
type MessageFilter struct {
	// ConversationID restricts to one conversation. | ConversationID 限定单个会话。
	ConversationID string
	// Folder is all, sent, system, unread or pinned. | Folder 取 all、sent、system、unread 或 pinned。
	Folder string
	// Query is the mixed full-text expression. | Query 是混合全文检索表达式。
	Query string
	// TypeKey filters by message type. | TypeKey 按消息类型过滤。
	TypeKey string
	// SenderID filters by author. | SenderID 按作者过滤。
	SenderID string
	// Title filters the title only. | Title 只过滤标题。
	Title string
	// Content filters the body text only. | Content 只过滤正文文本。
	Content string
	// TagID filters by one group or tag. | TagID 按一个分组或标签过滤。
	TagID string
	// Page is the 1-based page number. | Page 是从 1 开始的页码。
	Page int
	// PageSize is the page length. | PageSize 是每页长度。
	PageSize int
	// IncludeRecalled lets administrators see tombstones' metadata. | IncludeRecalled 允许管理员查看墓碑元数据。
	IncludeRecalled bool
}

// AuditFilter selects a page of audit entries. | AuditFilter 选择一页审计记录。
type AuditFilter struct {
	// Action filters by action identifier. | Action 按动作标识过滤。
	Action string
	// MessageID filters by message. | MessageID 按消息过滤。
	MessageID string
	// Page is the 1-based page number. | Page 是从 1 开始的页码。
	Page int
	// PageSize is the page length. | PageSize 是每页长度。
	PageSize int
}

// PageResult is one page of messages plus the total count. | PageResult 是一页消息及总数。
type PageResult struct {
	// Messages is the page. | Messages 是本页消息。
	Messages []MessageView `json:"messages"`
	// Total is the unpaged count. | Total 是不分页的总数。
	Total int `json:"total"`
	// Page echoes the requested page. | Page 回显请求页码。
	Page int `json:"page"`
	// PageSize echoes the requested size. | PageSize 回显请求页长。
	PageSize int `json:"page_size"`
}

// isChinese reports whether the language tag is Chinese. | isChinese 报告语言标签是否为中文。
func isChinese(lang string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "zh")
}
