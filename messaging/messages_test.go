package messaging_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shibingli/yd/messaging"
	"github.com/shibingli/yd/messaging/gormstore"
	"github.com/shibingli/yd/messaging/markdown"
	"github.com/shibingli/ydx/log"
)

// attachmentFailureStore returns ErrAttachmentNotFound from CreateMessage so the service's sentinel
// mapping can be exercised without a real storage race. |
// attachmentFailureStore 从 CreateMessage 返回 ErrAttachmentNotFound，以便在没有真实存储竞态的情况下
// 验证服务层的哨兵映射。
type attachmentFailureStore struct {
	*gormstore.Store
}

// CreateMessage always reports a missing attachment. | CreateMessage 始终报告附件不存在。
func (attachmentFailureStore) CreateMessage(context.Context, messaging.Message, []string, []string) error {
	return messaging.ErrAttachmentNotFound
}

// TestSendPreservesAttachmentNotFound proves messageWriteError keeps the attachment sentinel instead
// of flattening it into ErrUnavailable. | TestSendPreservesAttachmentNotFound 证明 messageWriteError
// 保留附件哨兵，而不是把它压平成 ErrUnavailable。
func TestSendPreservesAttachmentNotFound(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice", "Alice"), actor("us_bob", "Bob")
	conv := befriend(t, f, alice, bob)
	svc, err := messaging.New(attachmentFailureStore{Store: f.store}, f.dir, markdown.New(), f.attach, f.notifier, log.Default(), messaging.Options{Now: f.clock.Now})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	if _, err := svc.Send(ctx, alice, messaging.SendInput{ConversationID: conv.ID, Markdown: "x"}); !errors.Is(err, messaging.ErrAttachmentNotFound) {
		t.Fatalf("send error = %v, want ErrAttachmentNotFound", err)
	}
}

// TestNoticeChannelRejectsEveryMutation proves a private notice channel is read-only for its
// recipient and even for a moderator who was added as a member. |
// TestNoticeChannelRejectsEveryMutation 证明私有通知频道对收件人只读，对被加为成员的管理员同样只读。
func TestNoticeChannelRejectsEveryMutation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := actor("us_alice", "Alice")
	sent, err := f.svc.NotifyUser(ctx, tenantID, alice.ID, admin(), "附件删除通知 | Attachment deleted", "reason")
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if _, err := f.svc.Edit(ctx, alice, sent.ID, messaging.EditInput{Markdown: "edited"}); !errors.Is(err, messaging.ErrForbidden) {
		t.Fatalf("edit notice error = %v, want ErrForbidden", err)
	}
	if err := f.svc.Recall(ctx, alice, sent.ID, ""); !errors.Is(err, messaging.ErrForbidden) {
		t.Fatalf("recall notice error = %v, want ErrForbidden", err)
	}

	// 把管理员加入通知频道，使 Pin/Unpin 的类别闸门本身被覆盖，而不是被成员检查提前拦下。 |
	// Add the moderator to the notice channel so the pin gate itself is exercised instead of being
	// short-circuited by the membership check.
	if err := f.store.AddMember(ctx, messaging.Member{
		JoinedAt:       f.clock.Now(),
		ID:             "mb_admin_notice",
		TenantID:       tenantID,
		ConversationID: sent.ConversationID,
		UserID:         "us_admin",
		Role:           messaging.MemberPlain,
	}); err != nil {
		t.Fatalf("add moderator member: %v", err)
	}
	if err := f.svc.Pin(ctx, admin(), sent.ID, nil, nil); !errors.Is(err, messaging.ErrForbidden) {
		t.Fatalf("pin notice error = %v, want ErrForbidden", err)
	}
	if err := f.svc.Unpin(ctx, admin(), sent.ID); !errors.Is(err, messaging.ErrForbidden) {
		t.Fatalf("unpin notice error = %v, want ErrForbidden", err)
	}
}

// TestAuditActionConstantsStayStable pins the persisted machine values. |
// TestAuditActionConstantsStayStable 固定持久化的机器可读取值。
func TestAuditActionConstantsStayStable(t *testing.T) {
	cases := map[string]string{
		messaging.AuditActionEdit:   "message.edit",
		messaging.AuditActionRecall: "message.recall",
		messaging.AuditActionPin:    "message.pin",
		messaging.AuditActionUnpin:  "message.unpin",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("audit action = %q, want %q", got, want)
		}
	}
}
