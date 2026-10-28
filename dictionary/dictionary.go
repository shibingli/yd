// Package dictionary owns tenant-scoped and system-wide data dictionaries: named enumerations
// made of ordered items that carry a stable machine value and bilingual labels. | Package dictionary
// 负责租户级与系统级数据字典：由有序字典项组成、带稳定机器值与中英标签的命名枚举。
//
// A dictionary type is identified by an immutable lower-case code, and each of its items is
// identified by an immutable value inside that type. Business rows keep the value, never the label,
// so renaming a label never rewrites data and a disabled item simply stops being offered. |
// 字典类型由不可变的小写编码标识，其字典项由类型内不可变的机器值标识。业务数据只保存机器值、
// 从不保存标签，因此改标签不会重写数据，停用字典项也只是不再提供选择。
//
// Every tenant query is scoped by its tenant; system dictionaries live under the reserved
// SystemTenantID and are the only rows a tenant read may deliberately fall back to. |
// 每个租户查询都以自己的租户为条件；系统级字典存放在保留的 SystemTenantID 下，
// 是租户读取唯一允许有意回退到的行。
package dictionary

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"
	"time"

	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/json"
	"github.com/shibingli/ydx/regexpx"
)

// Sentinel errors of the dictionary domain. | 数据字典域的哨兵错误。
var (
	// ErrInvalidInput reports a malformed type, item, filter or paging argument. | ErrInvalidInput 表示类型、字典项、筛选或分页参数非法。
	ErrInvalidInput = errs.New("invalid dictionary input | 非法的数据字典输入")
	// ErrNotFound reports a dictionary outside the tenant or not present at all. | ErrNotFound 表示数据字典不在租户内或根本不存在。
	ErrNotFound = errs.New("dictionary not found | 数据字典不存在")
	// ErrCodeTaken reports a duplicate dictionary code inside one tenant. | ErrCodeTaken 表示同一租户内字典编码重复。
	ErrCodeTaken = errs.New("dictionary code already exists | 数据字典编码已存在")
	// ErrValueTaken reports a duplicate item value inside one dictionary. | ErrValueTaken 表示同一字典内字典项值重复。
	ErrValueTaken = errs.New("dictionary item value already exists | 数据字典项值已存在")
	// ErrBuiltinProtected reports a deletion attempt on a built-in dictionary. | ErrBuiltinProtected 表示试图删除内置数据字典。
	ErrBuiltinProtected = errs.New("built-in dictionary cannot be deleted | 内置数据字典不可删除")
	// ErrItemLimit reports a dictionary that already holds the maximum number of items. | ErrItemLimit 表示字典项数量已达上限。
	ErrItemLimit = errs.New("the dictionary has reached its item limit | 数据字典项已达上限")
	// ErrTooManyCodes reports a batch resolution above the accepted code count. | ErrTooManyCodes 表示一次批量解析的编码数量超过上限。
	ErrTooManyCodes = errs.New("too many dictionary codes in one request | 单次请求的字典编码过多")
	// ErrInvalidExtra reports item metadata that is not a JSON object. | ErrInvalidExtra 表示字典项元数据不是 JSON 对象。
	ErrInvalidExtra = errs.New("dictionary item extra is not a JSON object | 数据字典项的扩展字段不是 JSON 对象")
	// ErrUnavailable reports an unavailable dictionary store or cache. | ErrUnavailable 表示数据字典存储或缓存不可用。
	ErrUnavailable = errs.New("dictionary storage is unavailable | 数据字典存储不可用")
)

// SystemTenantID is the reserved owner of system-wide dictionaries. It is not a tenant identifier
// and must never be used for authentication or membership. | SystemTenantID 是系统级字典的保留归属，
// 不是租户标识，绝不能用于鉴权或成员关系。
const SystemTenantID = "__system__"

// Scope is the visibility layer of a dictionary. | Scope 是数据字典的可见范围。
type Scope string

// Dictionary scopes. | 数据字典范围。
const (
	// ScopeTenant is a dictionary owned by one tenant. | ScopeTenant 是由单个租户拥有的字典。
	ScopeTenant Scope = "tenant"
	// ScopeSystem is a dictionary shared by every tenant. | ScopeSystem 是由全部租户共享的字典。
	ScopeSystem Scope = "system"
)

// ScopeOf reports the scope of the owner. | ScopeOf 报告归属对应的范围。
func ScopeOf(tenantID string) Scope {
	if tenantID == SystemTenantID {
		return ScopeSystem
	}
	return ScopeTenant
}

// Status is the lifecycle state of a dictionary type or item. | Status 是数据字典类型或字典项的生命周期状态。
type Status string

// Dictionary statuses. | 数据字典状态。
const (
	// StatusEnabled makes a type or item visible to public resolution. | StatusEnabled 使类型或字典项对公共解析可见。
	StatusEnabled Status = "enabled"
	// StatusDisabled hides a type or item from public resolution while keeping it for administration. |
	// StatusDisabled 让类型或字典项从公共解析中隐藏，但仍在管理中保留。
	StatusDisabled Status = "disabled"
)

// Valid reports whether s is a known status. | Valid 报告 s 是否为已知状态。
func (s Status) Valid() bool {
	return s == StatusEnabled || s == StatusDisabled
}

// codeRe constrains a dictionary code: a stable lower-case slug of at most 64 bytes. | codeRe 约束字典编码：
// 最多 64 字节的稳定小写短标识。
var codeRe = regexpx.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// valueRe constrains an item value: a stable machine value that survives URLs and JSON. | valueRe 约束字典项值：
// 可安全用于 URL 与 JSON 的稳定机器值。
var valueRe = regexpx.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

// Type is one dictionary type. | Type 是一个数据字典类型。
type Type struct {
	// CreatedAt is the UTC creation instant. | CreatedAt 是 UTC 创建时刻。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime;index:idx_dict_type_tenant_created,priority:2"`
	// UpdatedAt is the UTC last-write instant. | UpdatedAt 是 UTC 最后写入时刻。
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
	// ID is the primary key, prefixed "dict_". | ID 是主键，前缀为 dict_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the owner: a tenant identifier, or SystemTenantID for system rows. |
	// TenantID 是归属方：租户标识，系统行则为 SystemTenantID。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_dict_type_code,priority:1;index:idx_dict_type_tenant_sort,priority:1;index:idx_dict_type_tenant_created,priority:1;index:idx_dict_type_tenant_status,priority:1"`
	// Code is the immutable public identifier, unique inside the tenant. | Code 是不可变的公共标识，租户内唯一。
	Code string `json:"code" gorm:"column:code;type:varchar(64);not null;uniqueIndex:uidx_dict_type_code,priority:2"`
	// Name is the Chinese display name. | Name 是中文展示名。
	Name string `json:"name" gorm:"column:name;type:varchar(80);not null"`
	// NameEN is the English display name. | NameEN 是英文展示名。
	NameEN string `json:"name_en,omitempty" gorm:"column:name_en;type:varchar(80)"`
	// Description explains the dictionary's purpose. | Description 说明字典用途。
	Description string `json:"description,omitempty" gorm:"column:description;type:varchar(500)"`
	// Status is enabled or disabled. | Status 取 enabled 或 disabled。
	Status Status `json:"status" gorm:"column:status;type:varchar(16);not null;index:idx_dict_type_tenant_status,priority:2"`
	// Builtin marks a dictionary seeded by the framework; it cannot be deleted. | Builtin 标记框架预置字典，不可删除。
	Builtin bool `json:"builtin,omitempty" gorm:"column:builtin;not null;default:false"`
	// Sort orders types in administration lists. | Sort 决定类型在管理列表中的顺序。
	Sort int `json:"sort" gorm:"column:sort_order;not null;default:0;index:idx_dict_type_tenant_sort,priority:3;index:idx_dict_type_tenant_status,priority:3"`
	// ItemCount is a query projection, not a column. | ItemCount 是查询投影，不是表列。
	ItemCount int `json:"item_count" gorm:"-"`
}

// TableName pins the table name. | TableName 固定表名。
func (Type) TableName() string { return "yd_dict_types" }

// Item is one dictionary item. | Item 是一条字典项。
type Item struct {
	// CreatedAt is the UTC creation instant. | CreatedAt 是 UTC 创建时刻。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	// UpdatedAt is the UTC last-write instant. | UpdatedAt 是 UTC 最后写入时刻。
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
	// ID is the primary key, prefixed "dit_". | ID 是主键，前缀为 dit_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the owner: a tenant identifier, or SystemTenantID for system rows. |
	// TenantID 是归属方：租户标识，系统行则为 SystemTenantID。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_dict_item_value,priority:1;index:idx_dict_item_tenant_sort,priority:1;index:idx_dict_item_tenant_status,priority:1"`
	// TypeID is the owning dictionary type. | TypeID 是所属字典类型。
	TypeID string `json:"type_id" gorm:"column:type_id;type:varchar(64);not null;uniqueIndex:uidx_dict_item_value,priority:2;index:idx_dict_item_tenant_sort,priority:2;index:idx_dict_item_tenant_status,priority:2"`
	// Value is the immutable machine value, unique inside the type. | Value 是不可变机器值，类型内唯一。
	Value string `json:"value" gorm:"column:item_value;type:varchar(64);not null;uniqueIndex:uidx_dict_item_value,priority:3"`
	// Label is the Chinese display label. | Label 是中文展示标签。
	Label string `json:"label" gorm:"column:label;type:varchar(120);not null"`
	// LabelEN is the English display label. | LabelEN 是英文展示标签。
	LabelEN string `json:"label_en,omitempty" gorm:"column:label_en;type:varchar(120)"`
	// Extra is a JSON object carrying presentation metadata such as a colour or icon. | Extra 是携带颜色、图标等展示元数据的 JSON 对象。
	Extra string `json:"extra,omitempty" gorm:"column:extra;type:text"`
	// Remark is a free-form administrative note. | Remark 是管理备注。
	Remark string `json:"remark,omitempty" gorm:"column:remark;type:varchar(500)"`
	// Status is enabled or disabled. | Status 取 enabled 或 disabled。
	Status Status `json:"status" gorm:"column:status;type:varchar(16);not null;index:idx_dict_item_tenant_status,priority:3"`
	// IsDefault marks the item a form preselects. | IsDefault 标记表单预选项。
	IsDefault bool `json:"default" gorm:"column:is_default;not null;default:false"`
	// Sort orders items inside their type. | Sort 决定字典项在类型内的顺序。
	Sort int `json:"sort" gorm:"column:sort_order;not null;default:0;index:idx_dict_item_tenant_sort,priority:3"`
}

// TableName pins the table name. | TableName 固定表名。
func (Item) TableName() string { return "yd_dict_items" }

// SnapshotItem is the public, cacheable projection of one enabled item. | SnapshotItem 是一个启用字典项的公共可缓存投影。
type SnapshotItem struct {
	// Value is the machine value. | Value 是机器值。
	Value string `json:"value"`
	// Label is the Chinese label. | Label 是中文标签。
	Label string `json:"label"`
	// LabelEN is the English label. | LabelEN 是英文标签。
	LabelEN string `json:"label_en,omitempty"`
	// Extra is the raw JSON object text, empty when the item carries no metadata. | Extra 是原始 JSON 对象文本，未携带元数据时为空。
	Extra string `json:"extra,omitempty"`
	// IsDefault reports whether the item is preselected. | IsDefault 报告该字典项是否为预选项。
	IsDefault bool `json:"default,omitempty"`
	// Sort is the display order inside the type. | Sort 是类型内的展示顺序。
	Sort int `json:"sort,omitempty"`
}

// Snapshot is the cacheable public view of one enabled dictionary type. | Snapshot 是一个启用字典类型的可缓存公共视图。
type Snapshot struct {
	// Scope is the layer that supplied the dictionary: tenant or system. | Scope 是提供该字典的层级：租户或系统。
	Scope Scope `json:"scope"`
	// Code is the immutable dictionary code. | Code 是不可变字典编码。
	Code string `json:"code"`
	// Name is the Chinese display name. | Name 是中文展示名。
	Name string `json:"name"`
	// NameEN is the English display name. | NameEN 是英文展示名。
	NameEN string `json:"name_en,omitempty"`
	// Description explains the dictionary. | Description 说明字典用途。
	Description string `json:"description,omitempty"`
	// Items are the enabled items in display order. | Items 是按展示顺序排列的启用字典项。
	Items []SnapshotItem `json:"items"`
}

// Lookup returns the item carrying value. | Lookup 返回携带 value 的字典项。
//
// A dictionary is small by design (the framework caps a type at 1000 items), so a linear scan is
// faster than building a map on every cached read. | 字典按设计很小（框架把单个类型限制在 1000 项），
// 因此线性扫描比每次缓存读取都建一张映射更快。
func (s Snapshot) Lookup(value string) (SnapshotItem, bool) {
	for i := range s.Items {
		if s.Items[i].Value == value {
			return s.Items[i], true
		}
	}
	return SnapshotItem{}, false
}

// Label returns the label of value in lang, or value itself when the item is unknown. | Label 返回 value 在
// lang 下的标签；字典项未知时原样返回 value。
func (s Snapshot) Label(value, lang string) string {
	item, ok := s.Lookup(value)
	if !ok {
		return value
	}
	return item.DisplayLabel(lang)
}

// DisplayLabel returns the English label for an English locale and the Chinese one otherwise. | DisplayLabel
// 在英文语言环境下返回英文标签，其余情况返回中文标签。
func (i SnapshotItem) DisplayLabel(lang string) string {
	if isEnglish(lang) && strings.TrimSpace(i.LabelEN) != "" {
		return i.LabelEN
	}
	return i.Label
}

// TypeInput is the editable payload of a dictionary type. | TypeInput 是字典类型的可编辑载荷。
type TypeInput struct {
	// Code is the immutable code; updates ignore it. | Code 是不可变编码；更新时忽略它。
	Code string
	// Name is the Chinese display name. | Name 是中文展示名。
	Name string
	// NameEN is the English display name. | NameEN 是英文展示名。
	NameEN string
	// Description explains the dictionary. | Description 说明字典用途。
	Description string
	// Status is enabled or disabled; empty means enabled. | Status 取 enabled 或 disabled；空值表示启用。
	Status Status
	// Sort orders the type in administration lists. | Sort 决定类型在管理列表中的顺序。
	Sort int
}

// ItemInput is the editable payload of a dictionary item. | ItemInput 是字典项的可编辑载荷。
type ItemInput struct {
	// Value is the immutable machine value; updates ignore it. | Value 是不可变机器值；更新时忽略它。
	Value string
	// Label is the Chinese display label. | Label 是中文展示标签。
	Label string
	// LabelEN is the English display label. | LabelEN 是英文展示标签。
	LabelEN string
	// Extra is a JSON object with presentation metadata; empty means none. | Extra 是携带展示元数据的 JSON 对象；空值表示没有。
	Extra string
	// Remark is a free-form administrative note. | Remark 是管理备注。
	Remark string
	// Status is enabled or disabled; empty means enabled. | Status 取 enabled 或 disabled；空值表示启用。
	Status Status
	// Default marks the item a form preselects. | Default 标记表单预选项。
	Default bool
	// Sort orders items inside their type. | Sort 决定字典项在类型内的顺序。
	Sort int
}

// Filter selects a page of dictionary types. | Filter 选择一页字典类型。
type Filter struct {
	// Query matches code, Chinese name and English name case-insensitively. | Query 不区分大小写匹配编码、中文名与英文名。
	Query string
	// Status narrows to one lifecycle state; empty means every state. | Status 限定一个生命周期状态；空值表示全部。
	Status Status
	// Page and PageSize are 1-based; zero uses the service defaults. | Page 与 PageSize 从 1 开始；零值使用服务默认值。
	Page     int
	PageSize int
}

// ItemFilter selects a page of items inside one type. | ItemFilter 选择一个类型内的一页字典项。
type ItemFilter struct {
	// Query matches value, Chinese label and English label case-insensitively. | Query 不区分大小写匹配值、中文标签与英文标签。
	Query string
	// Status narrows to one lifecycle state; empty means every state. | Status 限定一个生命周期状态；空值表示全部。
	Status Status
	// Page and PageSize are 1-based; zero uses the service defaults. | Page 与 PageSize 从 1 开始；零值使用服务默认值。
	Page     int
	PageSize int
}

// TypePage is one page of dictionary types. | TypePage 是一页数据字典类型。
type TypePage struct {
	// Items are the rows in this page. | Items 是本页记录。
	Items []Type `json:"items"`
	// Total is the number of matching types. | Total 是匹配总数。
	Total int `json:"total"`
	// Page and PageSize echo the effective paging. | Page 与 PageSize 回显生效的分页。
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// ItemPage is one page of dictionary items. | ItemPage 是一页字典项。
type ItemPage struct {
	// Items are the rows in this page. | Items 是本页记录。
	Items []Item `json:"items"`
	// Total is the number of matching items. | Total 是匹配总数。
	Total int `json:"total"`
	// Page and PageSize echo the effective paging. | Page 与 PageSize 回显生效的分页。
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// Options configure the dictionary service. Zero values keep the framework defaults. | Options 配置数据字典服务，零值保持框架默认。
type Options struct {
	// Now and NewID are injectable for tests. | Now 与 NewID 供测试注入。
	Now   func() time.Time
	NewID func(prefix string) (string, error)
	// MaxNameLen bounds the Chinese and English display names. | MaxNameLen 限制中英文展示名长度。
	MaxNameLen int
	// MaxDescriptionLen bounds a type description. | MaxDescriptionLen 限制类型说明长度。
	MaxDescriptionLen int
	// MaxLabelLen bounds the Chinese and English item labels. | MaxLabelLen 限制中英文标签长度。
	MaxLabelLen int
	// MaxRemarkLen bounds an item remark. | MaxRemarkLen 限制字典项备注长度。
	MaxRemarkLen int
	// MaxExtraLen bounds the raw JSON object of an item. | MaxExtraLen 限制字典项原始 JSON 对象长度。
	MaxExtraLen int
	// MaxItems bounds the number of items one type may hold. | MaxItems 限制单个类型可容纳的字典项数量。
	MaxItems int
	// MaxSort bounds the sort key. | MaxSort 限制排序值。
	MaxSort int
	// MaxBatchCodes bounds one ResolveMany call. | MaxBatchCodes 限制一次 ResolveMany 调用的编码数量。
	MaxBatchCodes int
	// MaxQueryLen bounds an administration search keyword. | MaxQueryLen 限制管理列表的搜索关键字长度。
	MaxQueryLen int
	// PageSize and MaxPageSize bound administration lists. | PageSize 与 MaxPageSize 限制管理列表分页。
	PageSize    int
	MaxPageSize int
}

// DefaultOptions returns the framework defaults. | DefaultOptions 返回框架默认值。
func DefaultOptions() Options {
	return Options{
		MaxNameLen:        80,
		MaxDescriptionLen: 500,
		MaxLabelLen:       120,
		MaxRemarkLen:      500,
		MaxExtraLen:       4096,
		MaxItems:          1000,
		MaxSort:           1_000_000,
		MaxBatchCodes:     50,
		MaxQueryLen:       128,
		PageSize:          20,
		MaxPageSize:       100,
	}
}

// normalizeOptions fills every zero value. | normalizeOptions 补齐全部零值。
func normalizeOptions(o Options) Options {
	d := DefaultOptions()
	if o.MaxNameLen <= 0 {
		o.MaxNameLen = d.MaxNameLen
	}
	if o.MaxDescriptionLen <= 0 {
		o.MaxDescriptionLen = d.MaxDescriptionLen
	}
	if o.MaxLabelLen <= 0 {
		o.MaxLabelLen = d.MaxLabelLen
	}
	if o.MaxRemarkLen <= 0 {
		o.MaxRemarkLen = d.MaxRemarkLen
	}
	if o.MaxExtraLen <= 0 {
		o.MaxExtraLen = d.MaxExtraLen
	}
	if o.MaxItems <= 0 {
		o.MaxItems = d.MaxItems
	}
	if o.MaxSort <= 0 {
		o.MaxSort = d.MaxSort
	}
	if o.MaxBatchCodes <= 0 {
		o.MaxBatchCodes = d.MaxBatchCodes
	}
	if o.MaxQueryLen <= 0 {
		o.MaxQueryLen = d.MaxQueryLen
	}
	if o.PageSize <= 0 {
		o.PageSize = d.PageSize
	}
	if o.MaxPageSize <= 0 {
		o.MaxPageSize = d.MaxPageSize
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.NewID == nil {
		o.NewID = newID
	}
	return o
}

// newID returns a prefixed random identifier. | newID 返回带前缀的随机标识。
func newID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", errs.Wrap(err, ErrUnavailable)
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

// normalizeTypeInput validates and canonicalizes a type payload. | normalizeTypeInput 校验并规范化类型载荷。
func (s *Service) normalizeTypeInput(in TypeInput, requireCode bool) (TypeInput, error) {
	in.Code = strings.ToLower(strings.TrimSpace(in.Code))
	if requireCode && !codeRe.MatchString(in.Code) {
		return TypeInput{}, errs.Detail(ErrInvalidInput, "code=%[1]q | 编码=%[1]q", in.Code)
	}
	in.Name = strings.TrimSpace(in.Name)
	in.NameEN = strings.TrimSpace(in.NameEN)
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" || len([]rune(in.Name)) > s.options.MaxNameLen {
		return TypeInput{}, ErrInvalidInput
	}
	if len([]rune(in.NameEN)) > s.options.MaxNameLen {
		return TypeInput{}, ErrInvalidInput
	}
	if len([]rune(in.Description)) > s.options.MaxDescriptionLen {
		return TypeInput{}, ErrInvalidInput
	}
	if in.Status == "" {
		in.Status = StatusEnabled
	}
	if !in.Status.Valid() {
		return TypeInput{}, ErrInvalidInput
	}
	if in.Sort < 0 || in.Sort > s.options.MaxSort {
		return TypeInput{}, ErrInvalidInput
	}
	return in, nil
}

// normalizeItemInput validates and canonicalizes an item payload. | normalizeItemInput 校验并规范化字典项载荷。
func (s *Service) normalizeItemInput(in ItemInput, requireValue bool) (ItemInput, error) {
	in.Value = strings.TrimSpace(in.Value)
	if requireValue && !valueRe.MatchString(in.Value) {
		return ItemInput{}, errs.Detail(ErrInvalidInput, "value=%[1]q | 值=%[1]q", in.Value)
	}
	in.Label = strings.TrimSpace(in.Label)
	in.LabelEN = strings.TrimSpace(in.LabelEN)
	in.Extra = normalizeExtra(in.Extra)
	in.Remark = strings.TrimSpace(in.Remark)
	if in.Label == "" || len([]rune(in.Label)) > s.options.MaxLabelLen {
		return ItemInput{}, ErrInvalidInput
	}
	if len([]rune(in.LabelEN)) > s.options.MaxLabelLen {
		return ItemInput{}, ErrInvalidInput
	}
	if len([]rune(in.Remark)) > s.options.MaxRemarkLen {
		return ItemInput{}, ErrInvalidInput
	}
	if len(in.Extra) > s.options.MaxExtraLen || !validExtra(in.Extra) {
		return ItemInput{}, ErrInvalidExtra
	}
	if in.Status == "" {
		in.Status = StatusEnabled
	}
	if !in.Status.Valid() {
		return ItemInput{}, ErrInvalidInput
	}
	if in.Default && in.Status == StatusDisabled {
		return ItemInput{}, errs.Detail(ErrInvalidInput, "a disabled item cannot be the default | 停用的字典项不能作为默认项")
	}
	if in.Sort < 0 || in.Sort > s.options.MaxSort {
		return ItemInput{}, ErrInvalidInput
	}
	return in, nil
}

// normalizeExtra trims surrounding space and treats a JSON null as "no metadata". | normalizeExtra 去掉首尾空白，
// 并把 JSON null 视为「没有元数据」。
func normalizeExtra(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "null" {
		return ""
	}
	return raw
}

// validExtra reports whether raw is empty or a JSON object. | validExtra 报告 raw 是否为空或 JSON 对象。
func validExtra(raw string) bool {
	if raw == "" {
		return true
	}
	if !strings.HasPrefix(raw, "{") || !strings.HasSuffix(raw, "}") {
		return false
	}
	return json.Valid([]byte(raw))
}

// isEnglish reports whether lang names an English locale. | isEnglish 报告 lang 是否为英文语言环境。
func isEnglish(lang string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "en")
}
