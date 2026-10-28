package messaging

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/shibingli/yd/attachments"
	"github.com/shibingli/ydx/errs"
)

// attachmentActor converts the message-centre actor to the attachments actor. | attachmentActor 把消息中心
// 主体转换为附件主体。
func attachmentActor(actor Actor) attachments.Actor {
	return attachments.Actor{
		ID:          actor.ID,
		TenantID:    actor.TenantID,
		DisplayName: actor.DisplayName,
		Manage:      actor.Manage,
		System:      actor.System,
	}
}

// UploadAttachment stores one pending file through the attachments domain. | UploadAttachment 经附件域保存
// 一个待引用文件。
func (s *Service) UploadAttachment(ctx context.Context, actor Actor, name string, declaredSize int64, r io.Reader) (Attachment, error) {
	if !actor.valid() {
		return Attachment{}, ErrInvalidInput
	}
	if s.attachments == nil {
		return Attachment{}, errs.Detail(ErrUnavailable, "attachment storage is not configured | 未配置附件存储")
	}
	attachment, err := s.attachments.Upload(ctx, attachmentActor(actor), name, declaredSize, r)
	if err != nil {
		return Attachment{}, mapAttachmentError(err)
	}
	return attachment, nil
}

// DownloadAttachment returns the attachment record and bytes when the actor may read it. | DownloadAttachment
// 在主体有权读取时返回附件记录与字节。
func (s *Service) DownloadAttachment(ctx context.Context, actor Actor, id string) (Attachment, []byte, error) {
	if !actor.valid() || s.attachments == nil {
		return Attachment{}, nil, ErrInvalidInput
	}
	attachment, err := s.attachments.Lookup(ctx, actor.TenantID, strings.TrimSpace(id))
	if err != nil {
		return Attachment{}, nil, mapAttachmentError(err)
	}
	if attachment.Status == attachments.StatusDeleted {
		return Attachment{}, nil, ErrAttachmentNotFound
	}
	if err := s.authorizeAttachment(ctx, actor, attachment); err != nil {
		return Attachment{}, nil, err
	}
	data, err := s.attachments.Read(ctx, attachment)
	if err != nil {
		return Attachment{}, nil, mapAttachmentError(err)
	}
	return attachment, data, nil
}

// authorizeAttachment applies the visibility rule of a message attachment. | authorizeAttachment 应用
// 消息附件的可见性规则。
func (s *Service) authorizeAttachment(ctx context.Context, actor Actor, attachment Attachment) error {
	if attachment.UploaderID == actor.ID || actor.Manage {
		return nil
	}
	refs, err := s.attachments.Refs(ctx, actor.TenantID, attachment.ID)
	if err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	for _, ref := range refs {
		if ref.Type != attachments.RefTypeMessage {
			continue
		}
		msg, err := s.store.GetMessage(ctx, actor.TenantID, ref.ID)
		if err != nil {
			continue
		}
		members, err := s.store.ListMembers(ctx, actor.TenantID, msg.ConversationID)
		if err != nil {
			return errs.Wrap(err, ErrUnavailable)
		}
		if containsMember(members, actor.ID) {
			return nil
		}
	}
	return ErrAttachmentNotFound
}

// CleanupPending removes unattached uploads older than the attachment domain's pending TTL. |
// CleanupPending 删除早于附件域待引用保留时长的未挂载上传。
func (s *Service) CleanupPending(ctx context.Context, tenantID string) (int, error) {
	if s.attachments == nil {
		return 0, nil
	}
	return s.attachments.CleanupPending(ctx, tenantID)
}

// mapAttachmentError maps attachment sentinels onto message-centre sentinels. | mapAttachmentError 把
// 附件哨兵映射为消息中心哨兵。
func mapAttachmentError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, attachments.ErrNotFound):
		return ErrAttachmentNotFound
	case errors.Is(err, attachments.ErrTooLarge):
		return ErrAttachmentTooLarge
	case errors.Is(err, attachments.ErrTypeNotAllowed):
		return ErrAttachmentType
	case errors.Is(err, attachments.ErrInvalidInput), errors.Is(err, attachments.ErrReasonRequired),
		errors.Is(err, attachments.ErrReasonTooLong), errors.Is(err, attachments.ErrRefInvalid):
		return ErrInvalidInput
	case errors.Is(err, attachments.ErrReferenced):
		return ErrAttachmentNotFound
	default:
		return errs.Wrap(err, ErrUnavailable)
	}
}
