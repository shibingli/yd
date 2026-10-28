package attachments

import (
	"context"
	"time"
)

// Store persists attachment metadata and its reference rows. | Store 持久化附件元数据与引用记录。
//
// Every method takes the tenant explicitly: the tenant is the hard boundary and no query may cross
// it. | 每个方法都显式接收租户：租户是硬边界，任何查询都不得跨越它。
type Store interface {
	// Create inserts one pending attachment. | Create 插入一条待引用附件。
	Create(ctx context.Context, attachment Attachment) error
	// Get reads one attachment inside the tenant. | Get 读取租户内的一条附件。
	Get(ctx context.Context, tenantID, id string) (Attachment, error)
	// List returns one page and its total, scoped to the tenant. When all is false only ownerID's
	// rows are eligible. | List 返回租户内的一页与总数；all 为 false 时只允许 ownerID 的记录。
	List(ctx context.Context, tenantID string, filter Filter, ownerID string, all bool) ([]Attachment, int, error)
	// Refs lists the references of one attachment. | Refs 列出某附件的引用。
	Refs(ctx context.Context, tenantID, attachmentID string) ([]Ref, error)
	// ListByRef returns the attachments referenced by one business object, including tombstones. |
	// ListByRef 返回某个业务对象引用的附件，包含墓碑。
	ListByRef(ctx context.Context, tenantID string, ref Ref) ([]Attachment, error)
	// Bind adds references and marks the attachments referenced, atomically. | Bind 原子地添加引用并标记附件为已引用。
	Bind(ctx context.Context, tenantID string, attachmentIDs []string, ref Ref, uploaderID string, at time.Time) error
	// Release removes every reference of one business object and returns the affected attachments
	// to pending when no reference remains, stamping released_at so the pending TTL restarts. |
	// Release 删除某个业务对象的全部引用，并在无剩余引用时把附件退回 pending，同时写入 released_at
	// 以重新计算待引用保留时长。
	Release(ctx context.Context, tenantID string, ref Ref, at time.Time) error
	// MarkDeleted turns one record into a tombstone. requireNoRefs refuses the update while any
	// reference exists. | MarkDeleted 把一条记录变成墓碑；requireNoRefs 在有引用时拒绝更新。
	MarkDeleted(ctx context.Context, tenantID, id string, at time.Time, by, reason string, requireNoRefs bool) error
	// DeleteUnreferenced tombstones one batch of attachments with no references and returns them. |
	// DeleteUnreferenced 把一批无引用附件变成墓碑并返回它们。
	DeleteUnreferenced(ctx context.Context, tenantID string, filter Filter, at time.Time, by, reason string, batch int) ([]Attachment, error)
	// DeleteExpiredPending tombstones pending uploads older than before and returns them. |
	// DeleteExpiredPending 把早于 before 的待引用上传变成墓碑并返回它们。
	DeleteExpiredPending(ctx context.Context, tenantID string, before time.Time, at time.Time, reason string, batch int) ([]Attachment, error)
	// DeleteExpiredPendingAll is the janitor variant across every tenant. It is used only by the
	// background cleaner; request paths must use the tenant-scoped method. |
	// DeleteExpiredPendingAll 是后台清理器使用的跨租户版本；请求路径必须使用带租户的方法。
	DeleteExpiredPendingAll(ctx context.Context, before time.Time, at time.Time, reason string, batch int) ([]Attachment, error)
}
