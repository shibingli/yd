package attachments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"strings"
	"time"

	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
)

// Service implements the attachment lifecycle on top of a Store and a BlobStore. | Service 在 Store 与
// BlobStore 之上实现附件生命周期。
type Service struct {
	// store persists metadata and references. | store 持久化元数据与引用。
	store Store
	// blobs stores the bytes. | blobs 保存字节。
	blobs BlobStore
	// log records cleanup and notification-adjacent failures. | log 记录清理与通知相关失败。
	log *ydlog.Logger
	// options are the normalized effective options. | options 是归一化后的生效选项。
	options Options
}

// New constructs a service. Store and BlobStore are mandatory. | New 构造服务；Store 与 BlobStore 必填。
func New(store Store, blobs BlobStore, log *ydlog.Logger, options Options) (*Service, error) {
	if store == nil || blobs == nil {
		return nil, ErrUnavailable
	}
	if log == nil {
		log = ydlog.Default()
	}
	return &Service{store: store, blobs: blobs, log: log, options: normalizeOptions(options)}, nil
}

// Options returns the effective options. | Options 返回生效选项。
func (s *Service) Options() Options { return s.options }

// Upload stores one file and returns its pending record. | Upload 保存一个文件并返回其待引用记录。
//
// The bytes are read with a hard ceiling and the content type is sniffed from the bytes, never taken
// from the client. | 读取字节时施加硬上限，内容类型由字节嗅探得出，绝不采信客户端。
func (s *Service) Upload(ctx context.Context, actor Actor, name string, declaredSize int64, r io.Reader) (Attachment, error) {
	if !actor.Valid() || s.blobs == nil {
		return Attachment{}, ErrInvalidInput
	}
	if declaredSize > s.options.MaxSize {
		return Attachment{}, ErrTooLarge
	}
	limited := io.LimitReader(r, s.options.MaxSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Attachment{}, errs.Wrap(err, ErrInvalidInput)
	}
	if int64(len(data)) > s.options.MaxSize {
		return Attachment{}, ErrTooLarge
	}
	if len(data) == 0 {
		return Attachment{}, ErrInvalidInput
	}
	contentType := sniffContentType(data)
	if !s.allowedContentType(contentType) {
		return Attachment{}, errs.Detail(ErrTypeNotAllowed, "type=%[1]q | 类型=%[1]q", contentType)
	}
	name = safeFileName(name)
	digest := sha256.Sum256(data)
	id, err := s.options.NewID("att_")
	if err != nil {
		return Attachment{}, errs.Wrap(err, ErrUnavailable)
	}
	createdAt := s.options.Now().UTC()
	key := attachmentKey(actor.TenantID, actor.ID, createdAt, id)
	if err := s.blobs.Put(ctx, key, data); err != nil {
		return Attachment{}, errs.Wrap(err, ErrUnavailable)
	}
	attachment := Attachment{
		CreatedAt:    createdAt,
		ID:           id,
		TenantID:     actor.TenantID,
		UploaderID:   actor.ID,
		OriginalName: name,
		StoredKey:    key,
		ContentType:  contentType,
		SHA256:       hex.EncodeToString(digest[:]),
		Size:         int64(len(data)),
		Status:       StatusPending,
	}
	if err := s.store.Create(ctx, attachment); err != nil {
		// The metadata write failed; the bytes are unreachable, so remove them too. |
		// 元数据写入失败，字节已不可达，一并删除。
		s.deleteBlob(ctx, key)
		return Attachment{}, errs.Wrap(err, ErrUnavailable)
	}
	return attachment, nil
}

// Lookup reads one attachment without an ownership check. It is for trusted adapters that enforce
// their own visibility rule; client-facing code must use Get or Download. | Lookup 读取附件但不校验
// 所有权，供自行执行可见性规则的受信适配器使用；面向客户端的代码必须使用 Get 或 Download。
func (s *Service) Lookup(ctx context.Context, tenantID, id string) (Attachment, error) {
	attachment, err := s.store.Get(ctx, tenantID, strings.TrimSpace(id))
	if err != nil {
		return Attachment{}, err
	}
	refs, refErr := s.store.Refs(ctx, tenantID, attachment.ID)
	if refErr != nil {
		return Attachment{}, errs.Wrap(refErr, ErrUnavailable)
	}
	attachment.Refs = refs
	attachment.RefCount = len(refs)
	return attachment, nil
}

// Get reads one attachment visible to the actor: its uploader or a manager. | Get 读取主体可见的附件：
// 上传者或管理员。
func (s *Service) Get(ctx context.Context, actor Actor, id string) (Attachment, error) {
	attachment, err := s.Lookup(ctx, actor.TenantID, id)
	if err != nil {
		return Attachment{}, err
	}
	if attachment.UploaderID != actor.ID && !actor.Manage && !actor.ReadAll {
		return Attachment{}, ErrNotFound
	}
	if attachment.Status == StatusDeleted {
		return Attachment{}, ErrNotFound
	}
	return attachment, nil
}

// Read returns the bytes of one attachment. The caller has already authorized visibility. | Read 返回
// 附件字节；调用方必须已经完成可见性鉴权。
func (s *Service) Read(ctx context.Context, attachment Attachment) ([]byte, error) {
	if attachment.Status == StatusDeleted || strings.TrimSpace(attachment.StoredKey) == "" {
		return nil, ErrNotFound
	}
	data, err := s.blobs.Get(ctx, attachment.StoredKey)
	if err != nil {
		return nil, errs.Wrap(err, ErrNotFound)
	}
	return data, nil
}

// Download returns the record and bytes for its uploader or a manager. | Download 为上传者或管理员返回
// 记录与字节。
func (s *Service) Download(ctx context.Context, actor Actor, id string) (Attachment, []byte, error) {
	attachment, err := s.Get(ctx, actor, id)
	if err != nil {
		return Attachment{}, nil, err
	}
	data, err := s.Read(ctx, attachment)
	if err != nil {
		return Attachment{}, nil, err
	}
	return attachment, data, nil
}

// ListOwn lists the actor's own attachments. | ListOwn 列出主体自己的附件。
func (s *Service) ListOwn(ctx context.Context, actor Actor, filter Filter) (Page, error) {
	if !actor.Valid() {
		return Page{}, ErrInvalidInput
	}
	return s.list(ctx, actor.TenantID, filter, actor.ID, false)
}

// ListAll lists every attachment of the tenant; it requires Manager. | ListAll 列出租户内全部附件；需要
// Manage 权限。
func (s *Service) ListAll(ctx context.Context, actor Actor, filter Filter) (Page, error) {
	if !actor.Valid() || (!actor.Manage && !actor.ReadAll) {
		return Page{}, ErrNotFound
	}
	return s.list(ctx, actor.TenantID, filter, "", true)
}

// Pending validates that ids are pending uploads owned by actor and returns them. It is used by
// features that bind attachments in their own transaction. | Pending 校验 ids 是主体拥有的待引用上传
// 并返回它们，供在自己的事务里绑定附件的功能使用。
func (s *Service) Pending(ctx context.Context, tenantID, uploaderID string, ids []string) ([]Attachment, error) {
	ids = uniqueIDs(ids)
	if len(ids) == 0 {
		return []Attachment{}, nil
	}
	if len(ids) > s.options.MaxBind {
		return nil, ErrInvalidInput
	}
	out := make([]Attachment, 0, len(ids))
	for _, id := range ids {
		attachment, err := s.store.Get(ctx, tenantID, id)
		if err != nil {
			return nil, err
		}
		if attachment.UploaderID != uploaderID || attachment.Status != StatusPending {
			return nil, ErrNotFound
		}
		out = append(out, attachment)
	}
	return out, nil
}

// Bind references attachments from one business object. | Bind 让一个业务对象引用附件。
func (s *Service) Bind(ctx context.Context, actor Actor, ids []string, ref Ref) error {
	if !actor.Valid() || !ref.Valid() {
		return ErrRefInvalid
	}
	ids = uniqueIDs(ids)
	if len(ids) == 0 {
		return ErrInvalidInput
	}
	if len(ids) > s.options.MaxBind {
		return ErrInvalidInput
	}
	for _, id := range ids {
		attachment, err := s.store.Get(ctx, actor.TenantID, id)
		if err != nil {
			return err
		}
		if attachment.UploaderID != actor.ID && !actor.Manage {
			return ErrNotFound
		}
		if attachment.Status == StatusDeleted {
			return ErrNotFound
		}
	}
	if err := s.store.Bind(ctx, actor.TenantID, ids, ref, bindOwner(actor), s.options.Now().UTC()); err != nil {
		return err
	}
	return nil
}

// bindOwner returns the uploader filter for Bind; a manager may bind anyone's attachment. | bindOwner
// 返回 Bind 的上传者过滤条件；管理员可以绑定任何人的附件。
func bindOwner(actor Actor) string {
	if actor.Manage {
		return ""
	}
	return actor.ID
}

// Release removes every reference of one business object. | Release 删除一个业务对象的全部引用。
func (s *Service) Release(ctx context.Context, actor Actor, ref Ref) error {
	if !actor.Valid() || !ref.Valid() {
		return ErrRefInvalid
	}
	attachments, err := s.store.ListByRef(ctx, actor.TenantID, ref)
	if err != nil {
		return err
	}
	for _, attachment := range attachments {
		if attachment.UploaderID != actor.ID && !actor.Manage {
			return ErrNotFound
		}
	}
	return s.store.Release(ctx, actor.TenantID, ref, s.options.Now().UTC())
}

// ListByRef returns the attachments of one business object, including tombstones. | ListByRef 返回一个
// 业务对象的附件，包含墓碑。
func (s *Service) ListByRef(ctx context.Context, tenantID string, ref Ref) ([]Attachment, error) {
	if !ref.Valid() {
		return nil, ErrRefInvalid
	}
	return s.store.ListByRef(ctx, tenantID, ref)
}

// Refs returns the references of one attachment. | Refs 返回一个附件的引用。
func (s *Service) Refs(ctx context.Context, tenantID, attachmentID string) ([]Ref, error) {
	return s.store.Refs(ctx, tenantID, attachmentID)
}

// DeleteOwn removes one pending attachment owned by actor. | DeleteOwn 删除主体拥有的一个待引用附件。
func (s *Service) DeleteOwn(ctx context.Context, actor Actor, id string) error {
	attachment, err := s.store.Get(ctx, actor.TenantID, id)
	if err != nil {
		return err
	}
	if attachment.UploaderID != actor.ID || attachment.Status != StatusPending {
		return ErrNotFound
	}
	refs, err := s.store.Refs(ctx, actor.TenantID, id)
	if err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	if len(refs) > 0 {
		return ErrReferenced
	}
	// Mark first: the store locks the row and re-checks references in the same transaction, so a
	// concurrent Bind either wins before the mark (this call fails) or waits and then sees deleted.
	// Bytes are removed after the tombstone commits; a blob failure is logged and can be retried by
	// an operator without allowing access again. | 先标记：存储在同一个事务里锁行并复查引用，并发
	// Bind 要么先成功（本次删除失败），要么等待后看到已删除。字节在墓碑提交后删除；字节失败只记日志，
	// 可由运维重试，且不会重新放行访问。
	if err := s.store.MarkDeleted(ctx, actor.TenantID, id, s.options.Now().UTC(), actor.ID, "", true); err != nil {
		return err
	}
	s.deleteBlob(ctx, attachment.StoredKey)
	return nil
}

// DeleteManaged removes any attachment of the tenant and returns the outcome for the application
// layer to notify the uploader. A referenced attachment keeps its reference rows so the owning
// feature can render a tombstone. | DeleteManaged 删除租户内任意附件，并返回结果供应用层通知上传者。
// 已引用附件保留引用行，使原业务仍能渲染墓碑。
func (s *Service) DeleteManaged(ctx context.Context, actor Actor, id, reason string) (DeletionOutcome, error) {
	if !actor.Valid() || !actor.Manage {
		return DeletionOutcome{}, ErrNotFound
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return DeletionOutcome{}, ErrReasonRequired
	}
	if len([]rune(reason)) > s.options.MaxReasonLen {
		return DeletionOutcome{}, ErrReasonTooLong
	}
	attachment, err := s.store.Get(ctx, actor.TenantID, id)
	if err != nil {
		return DeletionOutcome{}, err
	}
	if attachment.Status == StatusDeleted {
		return DeletionOutcome{}, ErrNotFound
	}
	at := s.options.Now().UTC()
	if err := s.store.MarkDeleted(ctx, actor.TenantID, id, at, actor.ID, reason, false); err != nil {
		return DeletionOutcome{}, err
	}
	s.deleteBlob(ctx, attachment.StoredKey)
	attachment.Status = StatusDeleted
	attachment.DeleteReason = reason
	attachment.DeletedBy = actor.ID
	attachment.DeletedAt = &at
	return DeletionOutcome{
		Attachment: attachment,
		UploaderID: attachment.UploaderID,
		ActorID:    actor.ID,
		ActorName:  actor.DisplayName,
		Reason:     reason,
	}, nil
}

// DeleteUnreferenced removes every attachment matching filter that has no references yet. | DeleteUnreferenced
// 删除 filter 匹配且尚无任何引用的全部附件。
func (s *Service) DeleteUnreferenced(ctx context.Context, actor Actor, reason string, filter Filter) (DeleteReport, error) {
	if !actor.Valid() || !actor.Manage {
		return DeleteReport{}, ErrNotFound
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return DeleteReport{}, ErrReasonRequired
	}
	if len([]rune(reason)) > s.options.MaxReasonLen {
		return DeleteReport{}, ErrReasonTooLong
	}
	filter.Referenced = boolPtr(false)
	filter.Status = ""
	filter.IncludeDeleted = false
	report := DeleteReport{ActorID: actor.ID, ActorName: actor.DisplayName, Reason: reason, ByUploader: map[string]DeleteGroup{}}
	for {
		deleted, err := s.store.DeleteUnreferenced(ctx, actor.TenantID, filter, s.options.Now().UTC(), actor.ID, reason, 200)
		if err != nil {
			return report, err
		}
		if len(deleted) == 0 {
			break
		}
		for _, attachment := range deleted {
			s.deleteBlob(ctx, attachment.StoredKey)
			report.Count++
			group := report.ByUploader[attachment.UploaderID]
			group.Count++
			if len(group.Names) < 10 {
				group.Names = append(group.Names, attachment.OriginalName)
			}
			report.ByUploader[attachment.UploaderID] = group
		}
	}
	return report, nil
}

// CleanupPending tombstones pending uploads older than PendingTTL for one tenant. | CleanupPending 把
// 某个租户超过 PendingTTL 的待引用上传变成墓碑。
func (s *Service) CleanupPending(ctx context.Context, tenantID string) (int, error) {
	before := s.options.Now().UTC().Add(-s.options.PendingTTL)
	total := 0
	for {
		deleted, err := s.store.DeleteExpiredPending(ctx, tenantID, before, s.options.Now().UTC(), "pending TTL expired | 待引用超时", 200)
		if err != nil {
			return total, err
		}
		if len(deleted) == 0 {
			return total, nil
		}
		for _, attachment := range deleted {
			s.deleteBlob(ctx, attachment.StoredKey)
		}
		total += len(deleted)
	}
}

// CleanupPendingAll is the background janitor entry point across every tenant. | CleanupPendingAll 是
// 跨租户的后台清理入口。
func (s *Service) CleanupPendingAll(ctx context.Context) (int, error) {
	before := s.options.Now().UTC().Add(-s.options.PendingTTL)
	total := 0
	for {
		deleted, err := s.store.DeleteExpiredPendingAll(ctx, before, s.options.Now().UTC(), "pending TTL expired | 待引用超时", 200)
		if err != nil {
			return total, err
		}
		if len(deleted) == 0 {
			return total, nil
		}
		for _, attachment := range deleted {
			s.deleteBlob(ctx, attachment.StoredKey)
		}
		total += len(deleted)
	}
}

// list normalizes paging and delegates to the store. | list 归一化分页并委托给存储。
func (s *Service) list(ctx context.Context, tenantID string, filter Filter, ownerID string, all bool) (Page, error) {
	if filter.Status != "" && !filter.Status.Valid() {
		return Page{}, ErrInvalidInput
	}
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = s.options.PageSize
	}
	if filter.PageSize > s.options.MaxPageSize {
		filter.PageSize = s.options.MaxPageSize
	}
	items, total, err := s.store.List(ctx, tenantID, filter, ownerID, all)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

// allowedContentType reports whether contentType is on the allow list. | allowedContentType 报告类型是否在白名单内。
func (s *Service) allowedContentType(contentType string) bool {
	for _, allowed := range s.options.AllowedTypes {
		if strings.EqualFold(normalizeContentType(allowed), contentType) {
			return true
		}
	}
	return false
}

// removeBlob deletes bytes and surfaces a failure to the caller. | removeBlob 删除字节并把失败上报给调用方。
func (s *Service) removeBlob(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	if err := s.blobs.Delete(ctx, key); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	return nil
}

// deleteBlob is the best-effort variant used after a tombstone was committed. | deleteBlob 是墓碑提交后
// 使用的最佳努力删除。
func (s *Service) deleteBlob(ctx context.Context, key string) {
	if strings.TrimSpace(key) == "" {
		return
	}
	if err := s.blobs.Delete(ctx, key); err != nil {
		s.log.Warn("attachment blob cleanup failed | 附件字节清理失败", ydlog.Err(err), ydlog.Str("key", key))
	}
}

// ContentDisposition builds the download header; images may be inline for preview. | ContentDisposition
// 构造下载响应头；图片可以内联用于预览。
func ContentDisposition(attachment Attachment, inline bool) string {
	disposition := "attachment"
	if inline && IsImage(attachment) {
		disposition = "inline"
	}
	value := mime.FormatMediaType(disposition, map[string]string{"filename": safeDownloadName(attachment.OriginalName)})
	if value == "" {
		return disposition
	}
	return value
}

// IsImage reports whether the attachment can be previewed inline. | IsImage 报告附件是否可以内联预览。
func IsImage(attachment Attachment) bool {
	if attachment.Status == StatusDeleted {
		return false
	}
	contentType := normalizeContentType(attachment.ContentType)
	if contentType == "image/svg+xml" || contentType == "image/svg" {
		// SVG is executable XML. Even when an operator adds it to the allow list, it must only
		// ever be downloaded, never rendered inline on the application origin. |
		// SVG 是可执行的 XML；即使运维把它加入白名单，也只能下载，绝不能在应用源上内联渲染。
		return false
	}
	return strings.HasPrefix(contentType, "image/")
}

// uniqueIDs trims, drops empties and de-duplicates ids while preserving order. | uniqueIDs 去空白、丢弃空值并
// 按原顺序去重。
func uniqueIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// boolPtr returns a pointer to v. | boolPtr 返回 v 的指针。
func boolPtr(v bool) *bool { return &v }

// ptrTime returns a pointer to t. | ptrTime 返回 t 的指针。
func ptrTime(t time.Time) *time.Time { return &t }
