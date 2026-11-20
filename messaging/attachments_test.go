package messaging_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/shibingli/yd/messaging"
)

// TestRecalledMessageAttachmentDeniedToMembers proves an attachment referenced only by a recalled
// message is not downloadable by another member; the uploader and a moderator keep access. |
// TestRecalledMessageAttachmentDeniedToMembers 证明仅被已撤回消息引用的附件对其它成员不可下载；
// 上传者与管理员仍可访问。
func TestRecalledMessageAttachmentDeniedToMembers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob, carol := actor("us_alice", "Alice"), actor("us_bob", "Bob"), actor("us_carol", "Carol")
	conv := befriend(t, f, alice, bob)
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	attachment, err := f.svc.UploadAttachment(ctx, alice, "secret.png", int64(len(png)), bytes.NewReader(png))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	sent, err := f.svc.Send(ctx, alice, messaging.SendInput{
		ConversationID: conv.ID,
		Markdown:       "see file",
		AttachmentIDs:  []string{attachment.ID},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, _, err := f.svc.DownloadAttachment(ctx, bob, attachment.ID); err != nil {
		t.Fatalf("member download before recall: %v", err)
	}
	if _, _, err := f.svc.DownloadAttachment(ctx, carol, attachment.ID); !errors.Is(err, messaging.ErrAttachmentNotFound) {
		t.Fatalf("non-member download error = %v, want ErrAttachmentNotFound", err)
	}
	if err := f.svc.Recall(ctx, alice, sent.ID, ""); err != nil {
		t.Fatalf("recall: %v", err)
	}
	if _, _, err := f.svc.DownloadAttachment(ctx, bob, attachment.ID); !errors.Is(err, messaging.ErrAttachmentNotFound) {
		t.Fatalf("member download after recall error = %v, want ErrAttachmentNotFound", err)
	}
	if _, _, err := f.svc.DownloadAttachment(ctx, alice, attachment.ID); err != nil {
		t.Fatalf("uploader download after recall: %v", err)
	}
	if _, _, err := f.svc.DownloadAttachment(ctx, admin(), attachment.ID); err != nil {
		t.Fatalf("moderator download after recall: %v", err)
	}
}
