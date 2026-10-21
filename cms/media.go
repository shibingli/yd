package cms

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
)

// UploadMedia sniffs, stores and records one upload. The sniffed type decides acceptance; the
// client-declared type is never trusted and the original name never becomes part of the key. |
// UploadMedia 嗅探、保存并记录一次上传。探测出的类型决定是否接受；客户端声明类型永不被信任，
// 原始文件名也永不进入键。
func (s *Service) UploadMedia(ctx context.Context, actor Actor, input MediaInput) (Media, error) {
	if !actor.valid() {
		return Media{}, ErrForbidden
	}
	if s.blobs == nil {
		return Media{}, errx.Detail(ErrUnavailable, "media storage is not configured | 未配置媒体存储")
	}
	if len(input.Data) == 0 {
		return Media{}, ErrInvalidInput
	}
	if int64(len(input.Data)) > s.options.MaxMediaSize {
		return Media{}, ErrMediaTooLarge
	}
	contentType := http.DetectContentType(input.Data)
	if !containsFold(s.options.AllowedMediaTypes, contentType) {
		return Media{}, errx.Detail(ErrMediaType, "content type %[1]q is not allowed | 不允许的内容类型 %[1]q", contentType)
	}
	alt := strings.TrimSpace(input.Alt)
	if err := checkOptionalText("media alt text | 媒体替代文本", alt, 500); err != nil {
		return Media{}, err
	}
	id, err := s.mustID("med_")
	if err != nil {
		return Media{}, err
	}
	key := actor.TenantID + "/" + id
	if err := s.blobs.Put(ctx, key, input.Data); err != nil {
		return Media{}, errx.Wrap(err, ErrUnavailable)
	}
	sum := sha256.Sum256(input.Data)
	width, height := imageSize(input.Data)
	now := s.options.Now()
	media := Media{
		ID:           id,
		TenantID:     actor.TenantID,
		UploaderID:   actor.ID,
		UploaderName: actor.DisplayName,
		OriginalName: displayName(input.OriginalName),
		StoredKey:    key,
		ContentType:  contentType,
		SHA256:       hex.EncodeToString(sum[:]),
		Alt:          alt,
		Size:         int64(len(input.Data)),
		Width:        width,
		Height:       height,
		CreatedAt:    now,
	}
	if err := s.store.CreateMedia(ctx, actor.TenantID, media); err != nil {
		if removeErr := s.blobs.Delete(ctx, key); removeErr != nil {
			s.log.Warn("orphan media blob could not be removed | 孤立媒体文件无法删除", logx.Err(removeErr))
		}
		return Media{}, errx.Wrap(err, ErrUnavailable)
	}
	s.audit(ctx, actor, "media.upload", "media", media.ID, media.OriginalName)
	return media, nil
}

// Media returns one media record. | Media 返回一条媒体记录。
func (s *Service) Media(ctx context.Context, tenantID, id string) (Media, error) {
	media, err := s.store.GetMedia(ctx, tenantID, id)
	if err != nil {
		return Media{}, mapStoreError(err, ErrMediaNotFound)
	}
	return media, nil
}

// Medias returns a normalised page of media records. | Medias 返回规范化后的一页媒体记录。
func (s *Service) Medias(ctx context.Context, tenantID string, filter MediaFilter) (MediaPage, error) {
	filter.Page, filter.PageSize = s.normalizePage(filter.Page, filter.PageSize)
	page, err := s.store.ListMedia(ctx, tenantID, filter)
	if err != nil {
		return MediaPage{}, errx.Wrap(err, ErrUnavailable)
	}
	page.Page = filter.Page
	page.PageSize = filter.PageSize
	page.TotalPages = totalPages(page.Total, filter.PageSize)
	return page, nil
}

// ReadMedia returns the media record and its bytes. A missing blob is reported as a missing media
// record, so the storage layer cannot reveal another tenant's key. | ReadMedia 返回媒体记录与字节。
// 文件缺失按媒体记录不存在上报，因此存储层无法泄露其它租户的键。
func (s *Service) ReadMedia(ctx context.Context, tenantID, id string) (Media, []byte, error) {
	media, err := s.Media(ctx, tenantID, id)
	if err != nil {
		return Media{}, nil, err
	}
	if s.blobs == nil {
		return Media{}, nil, errx.Detail(ErrUnavailable, "media storage is not configured | 未配置媒体存储")
	}
	data, err := s.blobs.Get(ctx, media.StoredKey)
	if err != nil {
		// The key comes from a tenant-scoped row, so a missing object is a real 404 while any other
		// read failure (permissions, disk, I/O) is a storage outage and must surface as 503. |
		// 键来自租户作用域的行，因此对象缺失是真正的 404；其它读取失败（权限、磁盘、I/O）属于存储故障，
		// 必须上报为 503。
		if errors.Is(err, os.ErrNotExist) {
			return Media{}, nil, ErrMediaNotFound
		}
		return Media{}, nil, errx.Wrap(err, ErrUnavailable)
	}
	return media, data, nil
}

// DeleteMedia removes one media file; the uploader or an actor with media management may do it. |
// DeleteMedia 删除一份媒体文件；上传者或具备媒体管理能力的主体可以执行。
func (s *Service) DeleteMedia(ctx context.Context, actor Actor, id string) error {
	if !actor.valid() {
		return ErrForbidden
	}
	media, err := s.store.GetMedia(ctx, actor.TenantID, id)
	if err != nil {
		return mapStoreError(err, ErrMediaNotFound)
	}
	if media.UploaderID != actor.ID && !actor.MediaManage {
		return ErrForbidden
	}
	// 先删记录再删文件：反过来的顺序一旦记录删除失败，库里的行会永远指向已经不存在的文件。 |
	// Delete the row before the blob: the other order leaves a row pointing at a file already gone if the row
	// deletion fails.
	if err := s.store.DeleteMedia(ctx, actor.TenantID, media.ID); err != nil {
		return mapStoreError(err, ErrMediaNotFound)
	}
	if s.blobs != nil {
		// 文件删除失败只会留下一个不可达的孤儿文件，不影响记录语义。 |
		// A failed blob deletion only leaves an unreachable orphan file; the record semantics stay correct.
		_ = s.blobs.Delete(ctx, media.StoredKey)
	}
	s.audit(ctx, actor, "media.delete", "media", media.ID, media.OriginalName)
	return nil
}

// displayName strips path separators and control characters from a user file name. | displayName 去掉用户文件名中的路径分隔符与控制字符。
func displayName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	runes := []rune(out)
	if len(runes) > 300 {
		out = string(runes[:300])
	}
	return out
}

// imageSize decodes the pixel dimensions of a standard-library image, or returns zeroes. |
// imageSize 解码标准库支持的图片像素尺寸，否则返回零。
func imageSize(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// containsFold reports whether values contains target, ignoring ASCII case. | containsFold 报告 values 是否忽略 ASCII 大小写包含 target。
func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
