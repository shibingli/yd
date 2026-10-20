// Package messaging is the tenant-scoped in-app message centre: friendships, direct and group
// conversations, administrator announcements, attachments, tags and read state. | Package messaging 是租户级站内消息中心：
// 好友、单聊与群聊、管理员公告、附件、标签与已读状态。
//
// The package owns the rules and depends on no ORM and no HTTP layer: persistence arrives through
// Store, user profiles through Directory, markdown rendering through Renderer, attachment bytes
// through BlobStore and realtime delivery through Notifier. The GORM implementation lives in
// messaging/gormstore, exactly like security/auth and security/auth/gormstore. | 本包只拥有规则，
// 不依赖 ORM 与 HTTP 层：持久化经 Store、用户资料经 Directory、Markdown 渲染经 Renderer、附件字节经
// BlobStore、实时投递经 Notifier 注入。GORM 实现位于 messaging/gormstore，与 security/auth 及其
// gormstore 的分层完全一致。
package messaging

import "github.com/shibingli/yd/core/errx"

// Sentinel errors of the message centre. Callers match them with errors.Is and never parse copy. |
// 消息中心的哨兵错误。调用方一律用 errors.Is 判定，绝不解析文案。
var (
	// ErrUnavailable reports a message centre without a usable store. | ErrUnavailable 表示消息中心没有可用的存储。
	ErrUnavailable = errx.New("message centre requires a database | 消息中心需要数据库")
	// ErrNotFound reports a missing resource or a resource outside the actor's tenant. | ErrNotFound 表示资源不存在或不在当前租户内。
	ErrNotFound = errx.New("message resource not found | 消息资源不存在")
	// ErrForbidden reports an operation the actor may not perform. | ErrForbidden 表示当前主体无权执行该操作。
	ErrForbidden = errx.New("message operation forbidden | 消息操作被拒绝")
	// ErrInvalidInput reports empty or malformed input. | ErrInvalidInput 表示输入为空或格式非法。
	ErrInvalidInput = errx.New("invalid message input | 非法的消息输入")
	// ErrNotFriend reports a direct message between non-friends. | ErrNotFriend 表示非好友之间的单聊。
	ErrNotFriend = errx.New("direct messages require an accepted friendship | 单聊需要已接受的好友关系")
	// ErrBlocked reports a request refused because either side blocked the other. | ErrBlocked 表示请求因任一方拉黑而被拒绝。
	ErrBlocked = errx.New("the user is blocked | 对方已被拉黑")
	// ErrSelfOperation reports a friendship or block aimed at the actor itself. | ErrSelfOperation 表示好友或拉黑操作指向主体本人。
	ErrSelfOperation = errx.New("cannot target yourself | 不能以自己为目标")
	// ErrDuplicateRequest reports a second pending friend request. | ErrDuplicateRequest 表示重复的好友申请。
	ErrDuplicateRequest = errx.New("a friend request is already pending | 已存在待处理的好友申请")
	// ErrRecallExpired reports a recall outside the configured window. | ErrRecallExpired 表示撤回超出配置窗口。
	ErrRecallExpired = errx.New("the recall window has expired | 撤回窗口已过期")
	// ErrEditExpired reports an edit outside the configured window. | ErrEditExpired 表示编辑超出配置窗口。
	ErrEditExpired = errx.New("the edit window has expired | 编辑窗口已过期")
	// ErrNotSender reports a mutation of a message sent by somebody else. | ErrNotSender 表示变更他人发送的消息。
	ErrNotSender = errx.New("only the sender may change this message | 只有发送者可以变更该消息")
	// ErrNotMember reports an operation on a conversation the actor does not belong to. | ErrNotMember 表示操作不属于自己的会话。
	ErrNotMember = errx.New("not a member of this conversation | 不是该会话的成员")
	// ErrConversationClosed reports an operation on a dissolved conversation. | ErrConversationClosed 表示操作已解散的会话。
	ErrConversationClosed = errx.New("the conversation is closed | 会话已关闭")
	// ErrSystemOnly reports a system-message action without message:system. | ErrSystemOnly 表示缺少 message:system 却执行系统消息操作。
	ErrSystemOnly = errx.New("system messages require message:system | 系统消息需要 message:system")
	// ErrPinNotAllowed reports an attempt to pin a non-system message. | ErrPinNotAllowed 表示尝试置顶非系统消息。
	ErrPinNotAllowed = errx.New("only system messages can be pinned | 只有系统消息可以置顶")
	// ErrPinWindowInvalid reports a pin window whose end precedes its start. | ErrPinWindowInvalid 表示置顶窗口的结束早于开始。
	ErrPinWindowInvalid = errx.New("invalid pin window | 非法的置顶窗口")
	// ErrTypeUnknown reports an unknown or archived message type. | ErrTypeUnknown 表示未知或已归档的消息类型。
	ErrTypeUnknown = errx.New("unknown or archived message type | 未知或已归档的消息类型")
	// ErrTypeProtected reports removal of a protected built-in type. | ErrTypeProtected 表示删除受保护的内置类型。
	ErrTypeProtected = errx.New("built-in message types cannot be removed | 内置消息类型不可删除")
	// ErrTypeInUse reports removal of a type that still owns messages. | ErrTypeInUse 表示删除仍被消息使用的类型。
	ErrTypeInUse = errx.New("the message type is still in use | 消息类型仍在使用中")
	// ErrTagUnknown reports an unknown or archived group/tag. | ErrTagUnknown 表示未知或已归档的分组/标签。
	ErrTagUnknown = errx.New("unknown or archived message tag | 未知或已归档的消息标签")
	// ErrAttachmentNotFound reports a missing attachment or one outside the actor's reach. | ErrAttachmentNotFound 表示附件不存在或不可达。
	ErrAttachmentNotFound = errx.New("attachment not found | 附件不存在")
	// ErrAttachmentTooLarge reports an upload over the configured ceiling. | ErrAttachmentTooLarge 表示上传超过配置上限。
	ErrAttachmentTooLarge = errx.New("attachment is too large | 附件超过大小上限")
	// ErrAttachmentType reports a blocked content type. | ErrAttachmentType 表示被禁止的内容类型。
	ErrAttachmentType = errx.New("attachment type is not allowed | 不允许的附件类型")
	// ErrTooManyAttachments reports more files than one message may carry. | ErrTooManyAttachments 表示附件数超过单条消息上限。
	ErrTooManyAttachments = errx.New("too many attachments | 附件数量超过上限")
	// ErrTooManyMembers reports a group larger than the configured ceiling. | ErrTooManyMembers 表示群成员超过配置上限。
	ErrTooManyMembers = errx.New("too many group members | 群成员超过上限")
	// ErrSearchTooBroad reports an empty or over-long search expression. | ErrSearchTooBroad 表示检索表达式为空或过长。
	ErrSearchTooBroad = errx.New("search expression is invalid | 检索表达式非法")
)
