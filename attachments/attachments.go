// Package attachments owns uploaded file bytes and their references. | Package attachments 负责已上传的
// 文件字节及其引用关系。
//
// It is deliberately independent of the message centre: any feature can upload a file, bind it to its
// own object through a (ref_type, ref_id) pair, and later release or list it. The message centre is
// only the first consumer. | 它刻意不依赖消息中心：任何功能都能上传文件，通过
// (ref_type, ref_id) 绑定到自己的业务对象，并在之后解除引用或列出附件。消息中心只是第一个消费方。
package attachments

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/shibingli/ydx/errs"
)

// Sentinel errors of the attachment domain. | 附件域的哨兵错误。
var (
	// ErrInvalidInput reports malformed upload, filter or reference input. | ErrInvalidInput 表示上传、筛选或引用参数非法。
	ErrInvalidInput = errs.New("invalid attachment input | 非法的附件输入")
	// ErrNotFound reports an attachment outside the tenant, deleted, or not visible to the actor. |
	// ErrNotFound 表示附件不在租户内、已删除或对主体不可见。
	ErrNotFound = errs.New("attachment not found | 附件不存在")
	// ErrTooLarge reports a file above the configured ceiling. | ErrTooLarge 表示文件超过配置上限。
	ErrTooLarge = errs.New("attachment is too large | 附件超过大小上限")
	// ErrTypeNotAllowed reports a sniffed MIME type outside the allow list. | ErrTypeNotAllowed 表示嗅探出的 MIME 类型不在白名单内。
	ErrTypeNotAllowed = errs.New("attachment type is not allowed | 附件类型不允许")
	// ErrReferenced reports a deletion of an attachment that still has references. | ErrReferenced 表示附件仍被引用，不能删除。
	ErrReferenced = errs.New("attachment is still referenced | 附件仍被引用")
	// ErrReasonRequired reports a management deletion without a reason. | ErrReasonRequired 表示管理员删除缺少原因。
	ErrReasonRequired = errs.New("a deletion reason is required | 必须填写删除原因")
	// ErrReasonTooLong reports a reason above the configured ceiling. | ErrReasonTooLong 表示删除原因超过上限。
	ErrReasonTooLong = errs.New("the deletion reason is too long | 删除原因过长")
	// ErrRefInvalid reports a malformed reference type or id. | ErrRefInvalid 表示引用类型或 ID 非法。
	ErrRefInvalid = errs.New("attachment reference is invalid | 附件引用非法")
	// ErrUnavailable reports an unavailable store or blob backend. | ErrUnavailable 表示存储或字节后端不可用。
	ErrUnavailable = errs.New("attachment storage is unavailable | 附件存储不可用")
)

// RefTypeMessage is the reference type used by the message centre. | RefTypeMessage 是消息中心使用的引用类型。
const RefTypeMessage = "message"

// Status is the lifecycle of one attachment. | Status 是一个附件的生命周期状态。
type Status string

// Attachment statuses. | 附件状态。
const (
	// StatusPending was uploaded but no business object references it yet. | StatusPending 已上传但尚无业务引用。
	StatusPending Status = "pending"
	// StatusReferenced is bound to at least one business object. | StatusReferenced 已被至少一个业务对象引用。
	StatusReferenced Status = "referenced"
	// StatusDeleted is a soft-deleted tombstone: bytes are gone and the record stays for audit and
	// for rendering a tombstone where the attachment used to be. | StatusDeleted 是软删除墓碑：字节已删除，
	// 记录保留用于审计与在原位置渲染墓碑。
	StatusDeleted Status = "deleted"
)

// Valid reports whether s is a known status. | Valid 报告 s 是否为已知状态。
func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusReferenced, StatusDeleted:
		return true
	default:
		return false
	}
}

// Ref identifies the business object that owns one or more attachments. | Ref 标识持有附件的一个或多个业务对象。
type Ref struct {
	// Type is the business kind, for example "message". | Type 是业务类型，例如 "message"。
	Type string `json:"type"`
	// ID is the business primary key. | ID 是业务主键。
	ID string `json:"id"`
}

// Valid reports whether the reference is well-formed. | Valid 报告引用是否合法。
func (r Ref) Valid() bool {
	return validRefType(r.Type) && validRefID(r.ID)
}

// Actor is the authenticated subject operating on attachments. | Actor 是操作附件的已认证主体。
type Actor struct {
	// ID is the user id. | ID 是用户 ID。
	ID string
	// TenantID is the active tenant and the hard boundary of every operation. | TenantID 是当前活动租户，也是所有操作的硬边界。
	TenantID string
	// DisplayName is the name shown in deletion notices. | DisplayName 是删除通知中显示的姓名。
	DisplayName string
	// Manage allows managing every attachment of the tenant. | Manage 允许管理租户内全部附件。
	Manage bool
	// ReadAll allows reading every attachment of the tenant without changing it (the auditor). |
	// ReadAll 允许只读访问租户内全部附件（审计员），不允许修改。
	ReadAll bool
	// System marks an administrator announcement author. | System 标记管理员公告作者。
	System bool
}

// Valid reports whether the actor has the two mandatory identifiers. | Valid 报告主体是否具备两个必需标识。
func (a Actor) Valid() bool {
	return strings.TrimSpace(a.ID) != "" && strings.TrimSpace(a.TenantID) != ""
}

// Attachment is one stored file record. | Attachment 是一条文件记录。
type Attachment struct {
	// CreatedAt is the UTC upload time. | CreatedAt 是 UTC 上传时间。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime;index:idx_attachment_uploader,priority:3;index:idx_attachment_tenant_created,priority:2;index:idx_attachment_status_created,priority:3"`
	// DeletedAt is non-nil once the record is a tombstone. | DeletedAt 在记录成为墓碑后非空。
	DeletedAt *time.Time `json:"deleted_at,omitempty" gorm:"column:deleted_at"`
	// ReleasedAt is set when the last reference is released; it restarts the pending TTL so an
	// attachment uploaded long ago is not cleaned the moment it becomes unreferenced. | ReleasedAt
	// 在最后一个引用释放时写入；它重置待引用保留时长，避免很久以前上传的附件在刚变成无引用时被立刻清理。
	ReleasedAt *time.Time `json:"released_at,omitempty" gorm:"column:released_at"`
	// ID is the primary key, prefixed "att_". | ID 是主键，前缀为 att_。
	ID string `json:"id" gorm:"column:id;type:varchar(64);primaryKey;uniqueIndex:uidx_attachment_tenant_id,priority:2"`
	// TenantID is the hard tenant boundary. | TenantID 是硬租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_attachment_tenant_id,priority:1;index:idx_attachment_uploader,priority:1;index:idx_attachment_tenant_created,priority:1;index:idx_attachment_status_created,priority:1"`
	// UploaderID is the user that uploaded the file. | UploaderID 是上传者。
	UploaderID string `json:"uploader_id" gorm:"column:uploader_id;type:varchar(64);not null;index:idx_attachment_uploader,priority:2"`
	// OriginalName is the file name as submitted, escaped on display. | OriginalName 是提交时的文件名，展示时转义。
	OriginalName string `json:"name" gorm:"column:original_name;type:varchar(255);not null"`
	// StoredKey is the opaque storage key; it never contains the original name and never reaches an
	// API payload, which is why the whole table must stay out of the JSON query cache. |
	// StoredKey 是不透明存储键，绝不含原始文件名，也绝不进入 API 载荷；因此整张表必须排除在 JSON 查询缓存之外。
	StoredKey string `json:"-" gorm:"column:stored_key;type:varchar(255);not null"`
	// ContentType is the sniffed MIME type. | ContentType 是嗅探出的 MIME 类型。
	ContentType string `json:"content_type" gorm:"column:content_type;type:varchar(128);not null"`
	// SHA256 is the lowercase hex digest of the bytes. | SHA256 是字节的小写十六进制摘要。
	SHA256 string `json:"sha256" gorm:"column:sha256;type:varchar(64);not null"`
	// Status is pending, referenced or deleted. | Status 取 pending、referenced 或 deleted。
	Status Status `json:"status" gorm:"column:status;type:varchar(16);not null;index:idx_attachment_status_created,priority:2"`
	// DeletedBy is the user that deleted the record. | DeletedBy 是删除记录的用户。
	DeletedBy string `json:"deleted_by,omitempty" gorm:"column:deleted_by;type:varchar(64)"`
	// DeleteReason is the administrator's reason; empty for a user's own cleanup. | DeleteReason 是管理员填写的原因；用户自删时为空。
	DeleteReason string `json:"delete_reason,omitempty" gorm:"column:delete_reason;type:varchar(500)"`
	// Refs lists the references when a detail query asks for them. | Refs 在详情查询需要时列出引用。
	Refs []Ref `json:"refs,omitempty" gorm:"-"`
	// Size is the byte length. | Size 是字节长度。
	Size int64 `json:"size" gorm:"column:size;not null"`
	// RefCount is a query projection, not a column. | RefCount 是查询投影，不是表列。
	RefCount int `json:"ref_count" gorm:"-"`
}

// TableName pins the table name. | TableName 固定表名。
func (Attachment) TableName() string { return "yd_attachments" }

// RefBinding is one persisted reference row. | RefBinding 是一条持久化引用记录。
type RefBinding struct {
	// CreatedAt is the UTC binding time. | CreatedAt 是 UTC 绑定时间。
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime;index:idx_attachment_ref_lookup,priority:4"`
	// TenantID is the tenant boundary. | TenantID 是租户边界。
	TenantID string `json:"tenant_id" gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_attachment_ref,priority:1;index:idx_attachment_ref_attachment,priority:1;index:idx_attachment_ref_lookup,priority:1"`
	// AttachmentID is the referenced attachment. | AttachmentID 是被引用的附件。
	AttachmentID string `json:"attachment_id" gorm:"column:attachment_id;type:varchar(64);not null;uniqueIndex:uidx_attachment_ref,priority:2;index:idx_attachment_ref_attachment,priority:2"`
	// RefType is the business kind. | RefType 是业务类型。
	RefType string `json:"ref_type" gorm:"column:ref_type;type:varchar(48);not null;uniqueIndex:uidx_attachment_ref,priority:3;index:idx_attachment_ref_lookup,priority:2"`
	// RefID is the business primary key. | RefID 是业务主键。
	RefID string `json:"ref_id" gorm:"column:ref_id;type:varchar(64);not null;uniqueIndex:uidx_attachment_ref,priority:4;index:idx_attachment_ref_lookup,priority:3"`
	// ID is an auto-increment row id. | ID 是自增行 ID。
	ID uint64 `json:"id" gorm:"column:id;primaryKey;autoIncrement"`
}

// TableName pins the table name. | TableName 固定表名。
func (RefBinding) TableName() string { return "yd_attachment_refs" }

// Filter selects a page of attachments. | Filter 选择一页附件。
type Filter struct {
	// Referenced narrows by reference existence; nil means either. | Referenced 按是否存在引用筛选；nil 表示不限。
	Referenced *bool
	// From and To bound created_at. | From 与 To 限制创建时间。
	From *time.Time
	To   *time.Time
	// Query matches the original file name case-insensitively. | Query 不区分大小写匹配原始文件名。
	Query string
	// UploaderID narrows to one uploader. | UploaderID 限定一个上传者。
	UploaderID string
	// Status narrows to one lifecycle state; empty means every non-deleted state. | Status 限定一个状态；空表示全部未删除状态。
	Status Status
	// RefType and RefID narrow to objects referenced by one business object. | RefType 与 RefID 限定某个业务对象引用的附件。
	RefType string
	RefID   string
	// Page and PageSize are 1-based; zero uses the service defaults. | Page 与 PageSize 从 1 开始；零值使用服务默认值。
	Page     int
	PageSize int
	// IncludeDeleted keeps tombstones in an unfiltered page. | IncludeDeleted 让无状态筛选的列表包含墓碑。
	IncludeDeleted bool
}

// Page is one attachment page. | Page 是一页附件。
type Page struct {
	// Items are the rows in this page. | Items 是本页记录。
	Items []Attachment `json:"items"`
	// Total is the number of matching rows. | Total 是匹配总数。
	Total int `json:"total"`
	// Page and PageSize echo the effective paging. | Page 与 PageSize 回显生效的分页。
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// DeletionOutcome describes one managed deletion for the application layer to notify. |
// DeletionOutcome 描述一次管理员删除，供应用层发送通知。
type DeletionOutcome struct {
	UploaderID string
	ActorID    string
	ActorName  string
	Reason     string
	Attachment Attachment
}

// DeleteGroup is one uploader's aggregate inside a cleanup report. | DeleteGroup 是清理报告中一个上传者的汇总。
type DeleteGroup struct {
	// Names holds at most the first ten original names for the notice. | Names 至多保留前十个原始文件名，供通知使用。
	Names []string
	// Count is the number of attachments removed for this uploader. | Count 是该上传者被删除的附件数。
	Count int
}

// DeleteReport describes a bulk unreferenced cleanup. | DeleteReport 描述一次未引用附件批量清理。
type DeleteReport struct {
	// ByUploader groups counts and a bounded name sample per uploader for one notice per person. |
	// ByUploader 按上传者汇总数量与有界文件名样本，便于每人一条通知。
	ByUploader map[string]DeleteGroup
	// ActorID and ActorName are the administrator performing the cleanup. | ActorID 与 ActorName 是执行清理的管理员。
	ActorID   string
	ActorName string
	// Reason is the administrator's reason. | Reason 是管理员填写的原因。
	Reason string
	// Count is the total number of deleted attachments. | Count 是删除附件总数。
	Count int
}

// Options configure the attachment service. Zero values keep the framework defaults. |
// Options 配置附件服务，零值保持框架默认。
type Options struct {
	// Now and NewID are injectable for tests. | Now 与 NewID 供测试注入。
	Now   func() time.Time
	NewID func(prefix string) (string, error)
	// AllowedTypes is the sniffed-MIME allow list. | AllowedTypes 是嗅探后的 MIME 白名单。
	AllowedTypes []string
	// MaxSize is the per-file byte ceiling. | MaxSize 是单文件字节上限。
	MaxSize int64
	// PendingTTL is how long an unreferenced upload survives. | PendingTTL 是未被引用上传的保留时长。
	PendingTTL time.Duration
	// PageSize and MaxPageSize bound list pages. | PageSize 与 MaxPageSize 限制列表分页。
	PageSize    int
	MaxPageSize int
	// MaxReasonLen bounds an administrator's deletion reason. | MaxReasonLen 限制管理员删除原因的长度。
	MaxReasonLen int
	// MaxBind bounds one Bind call. | MaxBind 限制一次 Bind 的附件数量。
	MaxBind int
}

// DefaultOptions returns the framework defaults. | DefaultOptions 返回框架默认值。
func DefaultOptions() Options {
	return Options{
		MaxSize:      20 << 20,
		AllowedTypes: defaultAllowedTypes(),
		PendingTTL:   24 * time.Hour,
		PageSize:     20,
		MaxPageSize:  100,
		MaxReasonLen: 500,
		MaxBind:      100,
	}
}

// BlobStore stores attachment bytes under opaque keys. | BlobStore 以不透明键保存附件字节。
type BlobStore interface {
	// Put writes data under key. | Put 把 data 写入 key。
	Put(ctx context.Context, key string, data []byte) error
	// Get reads the bytes stored under key. | Get 读取 key 下的字节。
	Get(ctx context.Context, key string) ([]byte, error)
	// Delete removes key; a missing key is not an error. | Delete 删除 key；键不存在不算错误。
	Delete(ctx context.Context, key string) error
}

// defaultAllowedTypes mirrors the historical message-centre allow list. | defaultAllowedTypes 与历史消息中心白名单一致。
func defaultAllowedTypes() []string {
	return []string{
		"image/png",
		"image/jpeg",
		"image/gif",
		"image/webp",
		"application/pdf",
		"application/zip",
		"text/plain",
		"application/octet-stream",
	}
}

// normalizeOptions fills every zero value. | normalizeOptions 补齐全部零值。
func normalizeOptions(o Options) Options {
	d := DefaultOptions()
	if o.MaxSize <= 0 {
		o.MaxSize = d.MaxSize
	}
	if len(o.AllowedTypes) == 0 {
		o.AllowedTypes = d.AllowedTypes
	}
	if o.PendingTTL <= 0 {
		o.PendingTTL = d.PendingTTL
	}
	if o.PageSize <= 0 {
		o.PageSize = d.PageSize
	}
	if o.MaxPageSize <= 0 {
		o.MaxPageSize = d.MaxPageSize
	}
	if o.MaxReasonLen <= 0 {
		o.MaxReasonLen = d.MaxReasonLen
	}
	if o.MaxBind <= 0 {
		o.MaxBind = d.MaxBind
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

// safeFileName keeps only the base name and caps its length without splitting UTF-8. | safeFileName
// 只保留基础文件名，并在不切断 UTF-8 的前提下限制长度。
func safeFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = path.Base(name)
	if name == "." || name == "/" || name == "" {
		return "file"
	}
	name = strings.Trim(name, " \t\r\n")
	if name == "" {
		return "file"
	}
	return truncate(name, 255)
}

// truncate caps a string at max bytes without splitting a UTF-8 rune. | truncate 在 max 字节内截断且不切断 UTF-8 符文。
func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	cut := max
	for cut > 0 && !utf8RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// utf8RuneStart reports whether b starts a UTF-8 rune. | utf8RuneStart 报告 b 是否是一个 UTF-8 符文的首字节。
func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// normalizeContentType strips parameters and lower-cases the media type. | normalizeContentType 去掉参数并小写媒体类型。
func normalizeContentType(value string) string {
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = value[:index]
	}
	return strings.ToLower(strings.TrimSpace(value))
}

// sniffContentType detects the MIME type from bytes. | sniffContentType 从字节嗅探 MIME 类型。
func sniffContentType(data []byte) string {
	return normalizeContentType(http.DetectContentType(data))
}

// attachmentKey derives an opaque storage key partitioned by tenant, uploader and UTC day:
// <tenant hash>/<uploader hash>/<YYYY>/<MM>/<DD>/<attachment id>. | attachmentKey 生成按租户、上传者与
// UTC 日期分区的不透明存储键：<租户哈希>/<上传者哈希>/<YYYY>/<MM>/<DD>/<附件 ID>。
func attachmentKey(tenantID, uploaderID string, createdAt time.Time, attachmentID string) string {
	tenantDigest := sha256.Sum256([]byte(tenantID))
	uploaderDigest := sha256.Sum256([]byte(uploaderID))
	return hex.EncodeToString(tenantDigest[:8]) + "/" +
		hex.EncodeToString(uploaderDigest[:8]) + "/" +
		createdAt.UTC().Format("2006/01/02") + "/" +
		attachmentID
}

// validRefType accepts lower-case business kinds. | validRefType 接受小写业务类型。
func validRefType(value string) bool {
	if value == "" || len(value) > 48 {
		return false
	}
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		case (r == '_' || r == '-') && i > 0:
		default:
			return false
		}
	}
	return true
}

// validRefID accepts URL-safe business ids. | validRefID 接受 URL 安全的业务 ID。
func validRefID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-' || r == ':' {
			continue
		}
		return false
	}
	return true
}

// safeDownloadName returns a Content-Disposition-safe file name. | safeDownloadName 返回可安全放入
// Content-Disposition 的文件名。
func safeDownloadName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" {
		return "file"
	}
	if mime.FormatMediaType("attachment", map[string]string{"filename": name}) == "" {
		return "file"
	}
	return name
}
