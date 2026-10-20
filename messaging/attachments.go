package messaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
)

// UploadAttachment stores one pending file and returns its record. | UploadAttachment 保存一个待挂载文件并返回记录。
//
// The bytes are read with a hard ceiling and the content type is sniffed from the bytes, never
// taken from the client: a caller can name any type in the multipart part, and only the bytes decide
// what the browser will later see. | 读取字节时施加硬上限，内容类型由字节嗅探得出，绝不采信客户端：
// 调用方可以在 multipart 分段里写任意类型，而只有字节才能决定浏览器最终看到什么。
func (s *Service) UploadAttachment(ctx context.Context, actor Actor, name string, declaredSize int64, r io.Reader) (Attachment, error) {
	if !actor.valid() {
		return Attachment{}, ErrInvalidInput
	}
	if s.blobs == nil {
		return Attachment{}, errx.Detail(ErrUnavailable, "attachment storage is not configured | 未配置附件存储")
	}
	if declaredSize > s.options.MaxAttachmentSize {
		return Attachment{}, ErrAttachmentTooLarge
	}
	limited := io.LimitReader(r, s.options.MaxAttachmentSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Attachment{}, errx.Wrap(err, ErrInvalidInput)
	}
	if int64(len(data)) > s.options.MaxAttachmentSize {
		return Attachment{}, ErrAttachmentTooLarge
	}
	if len(data) == 0 {
		return Attachment{}, ErrInvalidInput
	}
	contentType := normalizeContentType(http.DetectContentType(data))
	if !s.allowedContentType(contentType) {
		return Attachment{}, errx.Detail(ErrAttachmentType, "type=%[1]q | 类型=%[1]q", contentType)
	}
	name = safeFileName(name)
	digest := sha256.Sum256(data)
	id := mustID(s, "att_")
	key := attachmentKey(actor.TenantID, id)
	if err := s.blobs.Put(ctx, key, data); err != nil {
		return Attachment{}, errx.Wrap(err, ErrUnavailable)
	}
	attachment := Attachment{
		CreatedAt:    s.options.Now(),
		ID:           id,
		TenantID:     actor.TenantID,
		UploaderID:   actor.ID,
		OriginalName: name,
		StoredKey:    key,
		ContentType:  contentType,
		SHA256:       hex.EncodeToString(digest[:]),
		Size:         int64(len(data)),
		Status:       AttachmentPending,
	}
	if err := s.store.CreateAttachment(ctx, attachment); err != nil {
		_ = s.blobs.Delete(ctx, key)
		return Attachment{}, errx.Wrap(err, ErrUnavailable)
	}
	return attachment, nil
}

// DownloadAttachment returns the attachment record and its bytes when the actor may read it. |
// DownloadAttachment 在主体有权读取时返回附件记录与字节。
func (s *Service) DownloadAttachment(ctx context.Context, actor Actor, id string) (Attachment, []byte, error) {
	if !actor.valid() {
		return Attachment{}, nil, ErrInvalidInput
	}
	if s.blobs == nil {
		return Attachment{}, nil, ErrUnavailable
	}
	attachment, err := s.store.GetAttachment(ctx, actor.TenantID, strings.TrimSpace(id))
	if err != nil {
		return Attachment{}, nil, mapStoreError(err, ErrAttachmentNotFound)
	}
	if err := s.authorizeAttachment(ctx, actor, attachment); err != nil {
		return Attachment{}, nil, err
	}
	data, err := s.blobs.Get(ctx, attachment.StoredKey)
	if err != nil {
		return Attachment{}, nil, errx.Wrap(err, ErrAttachmentNotFound)
	}
	return attachment, data, nil
}

// authorizeAttachment applies the visibility rule of an attachment. | authorizeAttachment 应用附件的可见性规则。
func (s *Service) authorizeAttachment(ctx context.Context, actor Actor, attachment Attachment) error {
	if attachment.Status == AttachmentPending || attachment.MessageID == nil {
		if attachment.UploaderID != actor.ID && !actor.Manage {
			return ErrAttachmentNotFound
		}
		return nil
	}
	msg, err := s.store.GetMessage(ctx, actor.TenantID, *attachment.MessageID)
	if err != nil {
		return mapStoreError(err, ErrAttachmentNotFound)
	}
	members, err := s.store.ListMembers(ctx, actor.TenantID, msg.ConversationID)
	if err != nil {
		return errx.Wrap(err, ErrUnavailable)
	}
	if !containsMember(members, actor.ID) && !actor.Manage {
		return ErrAttachmentNotFound
	}
	if msg.Recalled() && !actor.Manage {
		return ErrAttachmentNotFound
	}
	return nil
}

// CleanupPending removes unattached uploads older than before and deletes their bytes. | CleanupPending 删除早于 before 的未挂载上传及其字节。
func (s *Service) CleanupPending(ctx context.Context, before time.Time) (int, error) {
	if s.blobs == nil {
		return 0, nil
	}
	stale, err := s.store.CleanupPending(ctx, before)
	if err != nil {
		return 0, errx.Wrap(err, ErrUnavailable)
	}
	for _, attachment := range stale {
		s.logCleanupFailure(s.blobs.Delete(ctx, attachment.StoredKey), attachment.StoredKey)
	}
	return len(stale), nil
}

// allowedContentType reports whether the sniffed type is on the allow list. | allowedContentType 报告嗅探类型是否在白名单内。
func (s *Service) allowedContentType(contentType string) bool {
	for _, allowed := range s.options.AllowedTypes {
		if strings.EqualFold(normalizeContentType(allowed), contentType) {
			return true
		}
	}
	return false
}

// normalizeContentType strips parameters and lower-cases the media type. | normalizeContentType 去掉参数并小写媒体类型。
func normalizeContentType(value string) string {
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = value[:index]
	}
	return strings.ToLower(strings.TrimSpace(value))
}

// safeFileName keeps only the base name and caps its length without splitting UTF-8. | safeFileName 只保留基础文件名，
// 并在不切断 UTF-8 的前提下限制长度。
func safeFileName(name string) string {
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

// attachmentKey derives an opaque storage key that cannot contain the original name or escape the
// root: the tenant id is hashed and the attachment id is already url-safe. | attachmentKey 生成不透明存储键：
// 租户 ID 经哈希，附件 ID 本身已是 URL 安全字符，因此键既不含原始文件名，也无法逃出根目录。
func attachmentKey(tenantID, attachmentID string) string {
	digest := sha256.Sum256([]byte(tenantID))
	return hex.EncodeToString(digest[:8]) + "/" + attachmentID
}

// logCleanupFailure records a blob deletion failure during janitor runs. | logCleanupFailure 记录清理任务中的字节删除失败。
func (s *Service) logCleanupFailure(err error, key string) {
	if err != nil {
		s.log.Warn("attachment blob cleanup failed | 附件字节清理失败", logx.Err(err), logx.Str("key", key))
	}
}
