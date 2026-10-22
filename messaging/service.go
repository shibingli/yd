package messaging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
)

// Actor is the authenticated principal a service call acts as. The capability booleans are filled
// by the HTTP layer from the authz registry, so the domain never imports authz and a caller cannot
// widen its own rights by spelling a permission name. | Actor 是服务调用所代表的主体。
// 能力布尔值由 HTTP 层从 authz 注册表填充，因此领域层不依赖 authz，调用方也无法通过拼写权限名扩大自己的权利。
type Actor struct {
	// ID is the global user id. | ID 是全局用户 ID。
	ID string
	// TenantID is the active tenant. | TenantID 是当前活动租户。
	TenantID string
	// DisplayName is the snapshot written into sent messages. | DisplayName 是写入已发消息的展示名快照。
	DisplayName string
	// Manage allows moderation of every message and conversation in the tenant. | Manage 允许管理租户内的全部消息与会话。
	Manage bool
	// System allows posting and revoking administrator announcements. | System 允许发布与撤销管理员公告。
	System bool
	// TypeManage allows the message type, group and tag lifecycle. | TypeManage 允许管理消息类型、分组与标签的生命周期。
	TypeManage bool
}

// Actor is valid when it names a user inside a tenant. | Actor 在同时给出用户与租户时有效。
func (a Actor) valid() bool {
	return strings.TrimSpace(a.ID) != "" && strings.TrimSpace(a.TenantID) != ""
}

// Options configures the service limits and windows. | Options 配置服务限制与窗口。
type Options struct {
	// Now is the clock; nil means time.Now().UTC(), matching every timestamp this package stores. |
	// Now 是时钟；nil 表示 time.Now().UTC()，与本包存储的每个时间戳一致。
	Now func() time.Time
	// NewID generates an identifier; nil installs the crypto/rand generator. | NewID 生成标识符；nil 安装 crypto/rand 生成器。
	NewID func(prefix string) (string, error)
	// SystemConversationKey is the stable key of the tenant announcement channel. | SystemConversationKey 是租户公告频道的稳定键。
	SystemConversationKey string
	// AllowedTypes lists accepted MIME types; nil installs the default allow list. | AllowedTypes 列出接受的 MIME 类型；nil 安装默认白名单。
	AllowedTypes []string
	// RecallWindow bounds a regular author's recall. | RecallWindow 限制普通作者的撤回窗口。
	RecallWindow time.Duration
	// EditWindow bounds a regular author's edit. | EditWindow 限制普通作者的编辑窗口。
	EditWindow time.Duration
	// MaxAttachments is the number of files one message may carry. | MaxAttachments 是单条消息可携带的文件数。
	MaxAttachments int
	// MaxAttachmentSize is the per-file byte ceiling. | MaxAttachmentSize 是单文件字节上限。
	MaxAttachmentSize int64
	// MaxMembers is the group member ceiling. | MaxMembers 是群成员上限。
	MaxMembers int
	// MaxTitleLen is the title byte ceiling. | MaxTitleLen 是标题字节上限。
	MaxTitleLen int
	// MaxBodyLen is the Markdown body byte ceiling. | MaxBodyLen 是 Markdown 正文字节上限。
	MaxBodyLen int
	// MaxTags is the number of groups plus tags one message may carry. | MaxTags 是单条消息可携带的分组与标签总数。
	MaxTags int
	// PageSize is the default page length. | PageSize 是默认页长。
	PageSize int
	// MaxPageSize is the largest accepted page length. | MaxPageSize 是允许的最大页长。
	MaxPageSize int
	// SearchTerms is the maximum number of whitespace-separated search terms. | SearchTerms 是空白分隔检索词的最大数量。
	SearchTerms int
	// PendingTTL is how long an unattached upload survives. | PendingTTL 是未挂载上传的保留时长。
	PendingTTL time.Duration
}

// DefaultOptions returns production-safe defaults. | DefaultOptions 返回生产可用的默认值。
func DefaultOptions() Options {
	return Options{
		RecallWindow:          5 * time.Minute,
		EditWindow:            5 * time.Minute,
		MaxAttachments:        10,
		MaxAttachmentSize:     20 << 20,
		MaxMembers:            50,
		MaxTitleLen:           160,
		MaxBodyLen:            128 << 10,
		MaxTags:               20,
		PageSize:              20,
		MaxPageSize:           100,
		SearchTerms:           6,
		PendingTTL:            24 * time.Hour,
		SystemConversationKey: "announcements",
	}
}

// OptionsFromConfig maps a configuration section onto service options, keeping the default for
// every zero value. The section is deliberately read here rather than in the domain so the
// package stays free of the config type. | OptionsFromConfig 把配置段映射到服务选项，零值保持默认。
// 读取配置放在此处而不是领域规则里，使本包不依赖配置类型。
func (o Options) normalized() Options {
	d := DefaultOptions()
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.NewID == nil {
		o.NewID = newID
	}
	if o.RecallWindow <= 0 {
		o.RecallWindow = d.RecallWindow
	}
	if o.EditWindow <= 0 {
		o.EditWindow = d.EditWindow
	}
	if o.MaxAttachments <= 0 {
		o.MaxAttachments = d.MaxAttachments
	}
	if o.MaxAttachmentSize <= 0 {
		o.MaxAttachmentSize = d.MaxAttachmentSize
	}
	if o.MaxMembers <= 0 {
		o.MaxMembers = d.MaxMembers
	}
	if o.MaxTitleLen <= 0 {
		o.MaxTitleLen = d.MaxTitleLen
	}
	if o.MaxBodyLen <= 0 {
		o.MaxBodyLen = d.MaxBodyLen
	}
	if o.MaxTags <= 0 {
		o.MaxTags = d.MaxTags
	}
	if o.PageSize <= 0 {
		o.PageSize = d.PageSize
	}
	if o.MaxPageSize <= 0 || o.MaxPageSize < o.PageSize {
		o.MaxPageSize = d.MaxPageSize
	}
	if o.SearchTerms <= 0 {
		o.SearchTerms = d.SearchTerms
	}
	if o.PendingTTL <= 0 {
		o.PendingTTL = d.PendingTTL
	}
	if o.SystemConversationKey == "" {
		o.SystemConversationKey = d.SystemConversationKey
	}
	if len(o.AllowedTypes) == 0 {
		o.AllowedTypes = defaultAllowedTypes()
	}
	return o
}

// Service owns every message-centre rule. | Service 拥有消息中心的全部规则。
type Service struct {
	// store is the persistence boundary. | store 是持久化边界。
	store Store
	// directory resolves tenant users. | directory 解析租户用户。
	directory Directory
	// renderer turns Markdown into sanitised HTML. | renderer 把 Markdown 转成净化 HTML。
	renderer Renderer
	// blobs stores attachment bytes; nil disables uploads. | blobs 保存附件字节；nil 表示禁用上传。
	blobs BlobStore
	// notifier delivers realtime events; nil drops them. | notifier 投递实时事件；nil 表示丢弃。
	notifier Notifier
	// log records delivery and cleanup problems. | log 记录投递与清理问题。
	log *ydlog.Logger
	// options holds the normalized limits. | options 保存规范化后的限制。
	options Options
}

// New constructs a service. Store, Directory and Renderer are mandatory; BlobStore and Notifier
// are optional, and a missing one degrades exactly the feature that needs it. | New 构造服务。
// Store、Directory 与 Renderer 必填；BlobStore 与 Notifier 可选，缺失时只降级依赖它的能力。
func New(store Store, directory Directory, renderer Renderer, blobs BlobStore, notifier Notifier, log *ydlog.Logger, options Options) (*Service, error) {
	if store == nil || directory == nil || renderer == nil {
		return nil, errs.Detail(ErrUnavailable, "store, directory and renderer are required | store、directory 与 renderer 为必填")
	}
	if log == nil {
		log = ydlog.Default()
	}
	return &Service{
		store:     store,
		directory: directory,
		renderer:  renderer,
		blobs:     blobs,
		notifier:  notifier,
		log:       log,
		options:   options.normalized(),
	}, nil
}

// Options returns the normalized options. | Options 返回规范化后的选项。
func (s *Service) Options() Options { return s.options }

// EnsureTenant seeds the built-in message types for a tenant. It is idempotent and safe to call on
// every message-centre entry point. | EnsureTenant 为租户写入内置消息类型。它幂等，可在每个消息中心入口调用。
func (s *Service) EnsureTenant(ctx context.Context, tenantID string) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrInvalidInput
	}
	types := builtinTypes(tenantID)
	now := s.options.Now()
	for i := range types {
		if types[i].ID == "" {
			types[i].ID = mustID(s, "mt_")
		}
		types[i].CreatedAt = now
		types[i].UpdatedAt = now
	}
	if err := s.store.EnsureTypes(ctx, tenantID, types); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	return nil
}

// Unread returns the shell badge payload and lazily joins the actor to the announcement channel. |
// Unread 返回外壳徽标载荷，并按需把主体加入公告频道。
func (s *Service) Unread(ctx context.Context, actor Actor) (UnreadSummary, error) {
	if !actor.valid() {
		return UnreadSummary{}, ErrInvalidInput
	}
	if err := s.EnsureTenant(ctx, actor.TenantID); err != nil {
		return UnreadSummary{}, err
	}
	system, err := s.ensureSystemConversation(ctx, actor)
	if err != nil {
		return UnreadSummary{}, err
	}
	counts, err := s.store.ConversationUnread(ctx, actor.TenantID, actor.ID)
	if err != nil {
		return UnreadSummary{}, errs.Wrap(err, ErrUnavailable)
	}
	summary := UnreadSummary{System: counts[system.ID]}
	for _, count := range counts {
		summary.Total += count
	}
	pending, err := s.store.CountPendingFriendRequests(ctx, actor.TenantID, actor.ID)
	if err != nil {
		return UnreadSummary{}, errs.Wrap(err, ErrUnavailable)
	}
	summary.FriendRequests = pending
	return summary, nil
}

// Profiles resolves a batch of user profiles for the HTTP layer. | Profiles 为 HTTP 层批量解析用户资料。
func (s *Service) Profiles(ctx context.Context, tenantID string, ids []string) (map[string]Profile, error) {
	return s.directory.Profiles(ctx, tenantID, ids)
}

// SearchUsers returns friend candidates with their current relation. | SearchUsers 返回带当前关系状态的好友候选。
func (s *Service) SearchUsers(ctx context.Context, actor Actor, keyword string) ([]FriendCandidate, error) {
	if !actor.valid() {
		return nil, ErrInvalidInput
	}
	profiles, err := s.directory.Search(ctx, actor.TenantID, keyword, actor.ID, 20)
	if err != nil {
		return nil, errs.Wrap(err, ErrUnavailable)
	}
	out := make([]FriendCandidate, 0, len(profiles))
	for _, profile := range profiles {
		// 目录实现可能忽略 excludeID；这里再排除一次，保证任何实现下都不会把自己列为好友候选。
		// A directory implementation may ignore excludeID; skip the actor again here so no implementation can list the
		// current user as a friend candidate.
		if profile.ID == actor.ID {
			continue
		}
		relation, err := s.relationWith(ctx, actor, profile.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, FriendCandidate{Profile: profile, Relation: relation})
	}
	return out, nil
}

// FriendRelation returns the current relation between the actor and another user, including the actor itself. |
// FriendRelation 返回当前主体与另一用户的关系，并覆盖目标就是主体本人的情况。
//
// Search excludes the actor, so RelationSelf can only surface through a direct request-drawer link; callers use this
// method to refuse an add form for self, friends, pending requests or a blocked pair before rendering it. |
// 检索会排除主体，因此 RelationSelf 只可能通过直接打开好友申请抽屉出现；调用方据此在渲染申请表单之前拒绝
// 自己、已是好友、已有待处理申请或拉黑关系。
func (s *Service) FriendRelation(ctx context.Context, actor Actor, otherID string) (string, error) {
	if !actor.valid() {
		return "", ErrInvalidInput
	}
	otherID = strings.TrimSpace(otherID)
	if otherID == "" {
		return "", ErrInvalidInput
	}
	if otherID == actor.ID {
		return RelationSelf, nil
	}
	return s.relationWith(ctx, actor, otherID)
}

// relationWith classifies the current relation between the actor and another user. | relationWith 分类当前主体与另一用户的关系。
func (s *Service) relationWith(ctx context.Context, actor Actor, otherID string) (string, error) {
	if blocked, err := s.store.EitherBlocked(ctx, actor.TenantID, actor.ID, otherID); err != nil {
		return "", errs.Wrap(err, ErrUnavailable)
	} else if blocked {
		return RelationBlocked, nil
	}
	if _, err := s.store.GetFriendship(ctx, actor.TenantID, actor.ID, otherID); err == nil {
		return RelationFriend, nil
	} else if !isNotFound(err) {
		return "", errs.Wrap(err, ErrUnavailable)
	}
	incoming, err := s.store.FriendRequestBetween(ctx, actor.TenantID, otherID, actor.ID)
	if err == nil && incoming.Status == FriendPending {
		return RelationIncoming, nil
	}
	if err != nil && !isNotFound(err) {
		return "", errs.Wrap(err, ErrUnavailable)
	}
	outgoing, err := s.store.FriendRequestBetween(ctx, actor.TenantID, actor.ID, otherID)
	if err == nil && outgoing.Status == FriendPending {
		return RelationOutgoing, nil
	}
	if err != nil && !isNotFound(err) {
		return "", errs.Wrap(err, ErrUnavailable)
	}
	return RelationNone, nil
}

// emit publishes an event without ever failing the caller's write. | emit 投递事件，绝不使调用方的写入失败。
func (s *Service) emit(ctx context.Context, event Event) {
	if s.notifier == nil {
		return
	}
	if event.At.IsZero() {
		event.At = s.options.Now()
	}
	if err := s.notifier.Publish(ctx, event); err != nil {
		s.log.Warn("message event delivery failed | 消息事件投递失败",
			ydlog.Err(err),
			ydlog.Str("event", event.Type),
			ydlog.Str("tenant", event.TenantID),
			ydlog.Str("conversation", event.ConversationID),
		)
	}
}

// newID generates a random prefixed identifier. | newID 生成带前缀的随机标识符。
func newID(prefix string) (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errs.Wrap(err, ErrUnavailable)
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

// isNotFound reports the domain not-found sentinel. | isNotFound 报告领域层的未找到哨兵。
func isNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
