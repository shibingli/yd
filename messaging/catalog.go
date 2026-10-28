package messaging

import (
	"context"
	"strings"

	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/regexpx"
)

// catalogKeyRe constrains type, group and tag keys: a stable lower-case slug. | catalogKeyRe 约束类型、分组与标签键：
// 稳定的纯小写短标识。
var catalogKeyRe = regexpx.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// Built-in message type keys. | 内置消息类型键。
const (
	// TypeAnnouncement is the general announcement type. | TypeAnnouncement 是通用公告类型。
	TypeAnnouncement = "announcement"
	// TypeNormal is the default conversation type. | TypeNormal 是默认会话类型。
	TypeNormal = "normal"
	// TypeSystem is the protected administrator type. | TypeSystem 是受保护的管理员类型。
	TypeSystem = "system"
	// TypeFinance is the finance notice type. | TypeFinance 是财务通知类型。
	TypeFinance = "finance"
	// TypeMarketing is the marketing type. | TypeMarketing 是营销类型。
	TypeMarketing = "marketing"
)

// Catalog is the set of active types, groups and tags used by compose drawers. | Catalog 是撰写抽屉使用的生效类型、分组与标签集合。
type Catalog struct {
	// Types are active message types. | Types 是生效的消息类型。
	Types []MessageType `json:"types"`
	// Groups are active group markers. | Groups 是生效的分组标记。
	Groups []Tag `json:"groups"`
	// Tags are active free-form labels. | Tags 是生效的自由标签。
	Tags []Tag `json:"tags"`
}

// builtinTypes returns the five seeded types of one tenant. | builtinTypes 返回某个租户的五个内置类型。
func builtinTypes(tenantID string) []MessageType {
	return []MessageType{
		{TenantID: tenantID, Key: TypeAnnouncement, Name: "公告", NameEN: "Announcement", Tone: ToneInfo, Builtin: true},
		{TenantID: tenantID, Key: TypeNormal, Name: "普通", NameEN: "General", Tone: ToneNeutral, Builtin: true},
		{TenantID: tenantID, Key: TypeSystem, Name: "系统", NameEN: "System", Tone: ToneWarn, Builtin: true, Protected: true},
		{TenantID: tenantID, Key: TypeFinance, Name: "财务", NameEN: "Finance", Tone: ToneSuccess, Builtin: true},
		{TenantID: tenantID, Key: TypeMarketing, Name: "营销", NameEN: "Marketing", Tone: ToneDanger, Builtin: true},
	}
}

// Catalog returns the active catalog for compose forms. | Catalog 返回撰写表单使用的生效目录。
func (s *Service) Catalog(ctx context.Context, actor Actor) (Catalog, error) {
	if !actor.valid() {
		return Catalog{}, ErrInvalidInput
	}
	if err := s.EnsureTenant(ctx, actor.TenantID); err != nil {
		return Catalog{}, err
	}
	types, err := s.store.ListTypes(ctx, actor.TenantID, false)
	if err != nil {
		return Catalog{}, errs.Wrap(err, ErrUnavailable)
	}
	groups, err := s.store.ListTags(ctx, actor.TenantID, TagGroup, false)
	if err != nil {
		return Catalog{}, errs.Wrap(err, ErrUnavailable)
	}
	tags, err := s.store.ListTags(ctx, actor.TenantID, TagLabel, false)
	if err != nil {
		return Catalog{}, errs.Wrap(err, ErrUnavailable)
	}
	return Catalog{Types: types, Groups: groups, Tags: tags}, nil
}

// ListTypes returns the tenant's message types. Archived rows require TypeManage. | ListTypes 返回租户的消息类型。
// 已归档记录需要 TypeManage。
func (s *Service) ListTypes(ctx context.Context, actor Actor, includeArchived bool) ([]MessageType, error) {
	if !actor.valid() {
		return nil, ErrInvalidInput
	}
	if includeArchived && !actor.TypeManage {
		return nil, ErrForbidden
	}
	if err := s.EnsureTenant(ctx, actor.TenantID); err != nil {
		return nil, err
	}
	types, err := s.store.ListTypes(ctx, actor.TenantID, includeArchived)
	if err != nil {
		return nil, errs.Wrap(err, ErrUnavailable)
	}
	return types, nil
}

// TypeInput is the editable payload of a message type. | TypeInput 是消息类型的可编辑载荷。
type TypeInput struct {
	// Key is the immutable slug; ignored by UpdateType. | Key 是不可变标识；UpdateType 忽略它。
	Key string
	// Name is the Chinese label. | Name 是中文标签。
	Name string
	// NameEN is the English label. | NameEN 是英文标签。
	NameEN string
	// Tone is the semantic colour role. | Tone 是语义色角色。
	Tone string
}

// CreateType adds a custom message type. | CreateType 新增自定义消息类型。
func (s *Service) CreateType(ctx context.Context, actor Actor, in TypeInput) (MessageType, error) {
	if !actor.valid() || !actor.TypeManage {
		return MessageType{}, ErrForbidden
	}
	key := strings.ToLower(strings.TrimSpace(in.Key))
	if !catalogKeyRe.MatchString(key) {
		return MessageType{}, errs.Detail(ErrInvalidInput, "key=%[1]q | 键=%[1]q", key)
	}
	name, nameEN, tone, err := normalizeCatalogCopy(in)
	if err != nil {
		return MessageType{}, err
	}
	now := s.options.Now()
	id, err := s.options.NewID("mt_")
	if err != nil {
		return MessageType{}, err
	}
	t := MessageType{
		CreatedAt: now,
		UpdatedAt: now,
		ID:        id,
		TenantID:  actor.TenantID,
		Key:       key,
		Name:      name,
		NameEN:    nameEN,
		Tone:      tone,
	}
	if err := s.store.CreateType(ctx, t); err != nil {
		return MessageType{}, errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ActorID: actor.ID, Detail: map[string]string{"catalog": "type", "key": key}})
	return t, nil
}

// UpdateType edits a type's labels and tone. | UpdateType 编辑类型标签与语义色。
func (s *Service) UpdateType(ctx context.Context, actor Actor, key string, in TypeInput) (MessageType, error) {
	if !actor.valid() || !actor.TypeManage {
		return MessageType{}, ErrForbidden
	}
	current, err := s.store.GetType(ctx, actor.TenantID, strings.ToLower(strings.TrimSpace(key)))
	if err != nil {
		return MessageType{}, mapStoreError(err, ErrTypeUnknown)
	}
	name, nameEN, tone, err := normalizeCatalogCopy(in)
	if err != nil {
		return MessageType{}, err
	}
	current.Name = name
	current.NameEN = nameEN
	current.Tone = tone
	current.UpdatedAt = s.options.Now()
	if err := s.store.UpdateType(ctx, current); err != nil {
		return MessageType{}, errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ActorID: actor.ID, Detail: map[string]string{"catalog": "type", "key": current.Key}})
	return current, nil
}

// SetTypeArchived archives or restores a type. The protected system type cannot be archived. |
// SetTypeArchived 归档或恢复类型。受保护的系统类型不可归档。
func (s *Service) SetTypeArchived(ctx context.Context, actor Actor, key string, archived bool) error {
	if !actor.valid() || !actor.TypeManage {
		return ErrForbidden
	}
	current, err := s.store.GetType(ctx, actor.TenantID, strings.ToLower(strings.TrimSpace(key)))
	if err != nil {
		return mapStoreError(err, ErrTypeUnknown)
	}
	if current.Protected && archived {
		return ErrTypeProtected
	}
	now := s.options.Now()
	current.UpdatedAt = now
	if archived {
		current.ArchivedAt = &now
	} else {
		current.ArchivedAt = nil
	}
	if err := s.store.UpdateType(ctx, current); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ActorID: actor.ID, Detail: map[string]string{"catalog": "type", "key": current.Key}})
	return nil
}

// DeleteType removes an unused type. | DeleteType 删除未使用的类型。
func (s *Service) DeleteType(ctx context.Context, actor Actor, key string) error {
	if !actor.valid() || !actor.TypeManage {
		return ErrForbidden
	}
	current, err := s.store.GetType(ctx, actor.TenantID, strings.ToLower(strings.TrimSpace(key)))
	if err != nil {
		return mapStoreError(err, ErrTypeUnknown)
	}
	if current.Protected || current.Builtin {
		return ErrTypeProtected
	}
	used, err := s.store.CountMessagesOfType(ctx, actor.TenantID, current.Key)
	if err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	if used > 0 {
		return ErrTypeInUse
	}
	if err := s.store.DeleteType(ctx, actor.TenantID, current.Key); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ActorID: actor.ID, Detail: map[string]string{"catalog": "type", "key": current.Key}})
	return nil
}

// ListTags returns groups or labels. Archived rows require TypeManage. | ListTags 返回分组或标签。已归档记录需要 TypeManage。
func (s *Service) ListTags(ctx context.Context, actor Actor, kind TagKind, includeArchived bool) ([]Tag, error) {
	if !actor.valid() {
		return nil, ErrInvalidInput
	}
	if kind != TagGroup && kind != TagLabel {
		return nil, ErrInvalidInput
	}
	if includeArchived && !actor.TypeManage {
		return nil, ErrForbidden
	}
	tags, err := s.store.ListTags(ctx, actor.TenantID, kind, includeArchived)
	if err != nil {
		return nil, errs.Wrap(err, ErrUnavailable)
	}
	return tags, nil
}

// TagInput is the editable payload of a group or tag. | TagInput 是分组或标签的可编辑载荷。
type TagInput struct {
	// Key is the immutable slug; ignored by UpdateTag. | Key 是不可变标识；UpdateTag 忽略它。
	Key string
	// Name is the Chinese label. | Name 是中文标签。
	Name string
	// NameEN is the English label. | NameEN 是英文标签。
	NameEN string
	// Tone is the semantic colour role. | Tone 是语义色角色。
	Tone string
}

// CreateTag adds a group or label. | CreateTag 新增分组或标签。
func (s *Service) CreateTag(ctx context.Context, actor Actor, kind TagKind, in TagInput) (Tag, error) {
	if !actor.valid() || !actor.TypeManage {
		return Tag{}, ErrForbidden
	}
	if kind != TagGroup && kind != TagLabel {
		return Tag{}, ErrInvalidInput
	}
	key := strings.ToLower(strings.TrimSpace(in.Key))
	if !catalogKeyRe.MatchString(key) {
		return Tag{}, errs.Detail(ErrInvalidInput, "key=%[1]q | 键=%[1]q", key)
	}
	name, nameEN, tone, err := normalizeCatalogCopy(TypeInput{Name: in.Name, NameEN: in.NameEN, Tone: in.Tone})
	if err != nil {
		return Tag{}, err
	}
	now := s.options.Now()
	id, err := s.options.NewID("tg_")
	if err != nil {
		return Tag{}, err
	}
	t := Tag{
		CreatedAt: now,
		UpdatedAt: now,
		ID:        id,
		TenantID:  actor.TenantID,
		Kind:      kind,
		Key:       key,
		Name:      name,
		NameEN:    nameEN,
		Tone:      tone,
	}
	if err := s.store.CreateTag(ctx, t); err != nil {
		return Tag{}, errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ActorID: actor.ID, Detail: map[string]string{"catalog": string(kind), "key": key}})
	return t, nil
}

// UpdateTag edits a group or label. | UpdateTag 编辑分组或标签。
func (s *Service) UpdateTag(ctx context.Context, actor Actor, id string, in TagInput) (Tag, error) {
	if !actor.valid() || !actor.TypeManage {
		return Tag{}, ErrForbidden
	}
	tags, err := s.store.GetTags(ctx, actor.TenantID, []string{id})
	if err != nil {
		return Tag{}, errs.Wrap(err, ErrUnavailable)
	}
	if len(tags) == 0 {
		return Tag{}, ErrTagUnknown
	}
	current := tags[0]
	name, nameEN, tone, err := normalizeCatalogCopy(TypeInput{Name: in.Name, NameEN: in.NameEN, Tone: in.Tone})
	if err != nil {
		return Tag{}, err
	}
	current.Name = name
	current.NameEN = nameEN
	current.Tone = tone
	current.UpdatedAt = s.options.Now()
	if err := s.store.UpdateTag(ctx, current); err != nil {
		return Tag{}, errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ActorID: actor.ID, Detail: map[string]string{"catalog": string(current.Kind), "key": current.Key}})
	return current, nil
}

// SetTagArchived archives or restores a group or label. | SetTagArchived 归档或恢复分组/标签。
func (s *Service) SetTagArchived(ctx context.Context, actor Actor, id string, archived bool) error {
	if !actor.valid() || !actor.TypeManage {
		return ErrForbidden
	}
	tags, err := s.store.GetTags(ctx, actor.TenantID, []string{id})
	if err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	if len(tags) == 0 {
		return ErrTagUnknown
	}
	current := tags[0]
	now := s.options.Now()
	current.UpdatedAt = now
	if archived {
		current.ArchivedAt = &now
	} else {
		current.ArchivedAt = nil
	}
	if err := s.store.UpdateTag(ctx, current); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ActorID: actor.ID, Detail: map[string]string{"catalog": string(current.Kind), "key": current.Key}})
	return nil
}

// DeleteTag removes an unused group or label. | DeleteTag 删除未使用的分组或标签。
func (s *Service) DeleteTag(ctx context.Context, actor Actor, id string) error {
	if !actor.valid() || !actor.TypeManage {
		return ErrForbidden
	}
	tags, err := s.store.GetTags(ctx, actor.TenantID, []string{id})
	if err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	if len(tags) == 0 {
		return ErrTagUnknown
	}
	used, err := s.store.CountMessagesOfTag(ctx, actor.TenantID, id)
	if err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	if used > 0 {
		return ErrTypeInUse
	}
	if err := s.store.DeleteTag(ctx, actor.TenantID, id); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventConversationChanged, TenantID: actor.TenantID, ActorID: actor.ID, Detail: map[string]string{"catalog": string(tags[0].Kind), "key": tags[0].Key}})
	return nil
}

// normalizeCatalogCopy trims and validates the bilingual labels and the tone. | normalizeCatalogCopy 裁剪并校验双语标签与语义色。
func normalizeCatalogCopy(in TypeInput) (name, nameEN, tone string, err error) {
	name = strings.TrimSpace(in.Name)
	nameEN = strings.TrimSpace(in.NameEN)
	if name == "" || nameEN == "" {
		return "", "", "", ErrInvalidInput
	}
	if len(name) > 128 || len(nameEN) > 128 {
		return "", "", "", ErrInvalidInput
	}
	tone = strings.ToLower(strings.TrimSpace(in.Tone))
	switch tone {
	case "", ToneNeutral:
		tone = ToneNeutral
	case ToneInfo, ToneSuccess, ToneWarn, ToneDanger:
	default:
		return "", "", "", ErrInvalidInput
	}
	return name, nameEN, tone, nil
}

// mapStoreError turns a store not-found into the caller's sentinel and wraps anything else. |
// mapStoreError 把存储层的未找到转成调用方的哨兵，其余错误保持包装。
func mapStoreError(err error, sentinel error) error {
	if isNotFound(err) {
		return sentinel
	}
	return errs.Wrap(err, ErrUnavailable)
}
