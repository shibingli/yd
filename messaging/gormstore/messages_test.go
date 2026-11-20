package gormstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shibingli/yd/messaging"
)

// TestCreateMessageRechecksConversationAndMembership proves the insert refuses a dissolved
// conversation and a sender that is no longer a member, re-checking under the transaction lock. |
// TestCreateMessageRechecksConversationAndMembership 证明插入会拒绝已解散的会话与不再是成员的发送者，
// 判定发生在事务行锁内。
func TestCreateMessageRechecksConversationAndMembership(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	conv := seedMembers(t, store, "cv_recheck", messaging.KindGroup, messaging.ConversationActive, testTime(0), "us_alice", "us_bob")

	if err := store.CreateMessage(ctx, testMessage("msg_ok", conv.ID, "us_alice", testTime(1)), nil, nil); err != nil {
		t.Fatalf("active send: %v", err)
	}
	if err := store.CreateMessage(ctx, testMessage("msg_outsider", conv.ID, "us_outsider", testTime(1)), nil, nil); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("outsider error = %v, want ErrNotFound", err)
	}

	// A membership that joined after the message creation time is stale for this send. |
	// 加入时间晚于消息创建时间的成员关系对本次发送是陈旧的。
	if err := store.AddMember(ctx, messaging.Member{
		JoinedAt:       testTime(5),
		ID:             "mb_late",
		TenantID:       testTenant,
		ConversationID: conv.ID,
		UserID:         "us_late",
		Role:           messaging.MemberPlain,
	}); err != nil {
		t.Fatalf("seed late member: %v", err)
	}
	if err := store.CreateMessage(ctx, testMessage("msg_late", conv.ID, "us_late", testTime(1)), nil, nil); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("late join error = %v, want ErrNotFound", err)
	}

	if err := db.Model(&messaging.Conversation{}).
		Where("tenant_id = ? AND id = ?", testTenant, conv.ID).
		Update("status", messaging.ConversationDissolved).Error; err != nil {
		t.Fatalf("dissolve: %v", err)
	}
	if err := store.CreateMessage(ctx, testMessage("msg_closed", conv.ID, "us_alice", testTime(2)), nil, nil); !errors.Is(err, messaging.ErrConversationClosed) {
		t.Fatalf("dissolved send error = %v, want ErrConversationClosed", err)
	}
}

// TestCreateMessageRechecksDirectFriendship proves a direct send re-checks the friendship and both
// block directions inside the same transaction. | TestCreateMessageRechecksDirectFriendship 证明单聊
// 发送会在同一事务内复查好友关系与两个方向的拉黑。
func TestCreateMessageRechecksDirectFriendship(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	conv := seedMembers(t, store, "cv_direct", messaging.KindDirect, messaging.ConversationActive, testTime(0), "us_alice", "us_bob")

	if err := store.CreateMessage(ctx, testMessage("msg_stranger", conv.ID, "us_alice", testTime(1)), nil, nil); !errors.Is(err, messaging.ErrNotFriend) {
		t.Fatalf("non-friend error = %v, want ErrNotFriend", err)
	}
	if err := store.CreateFriendship(ctx, messaging.Friendship{
		CreatedAt:  testTime(1),
		ID:         "fs_pair",
		TenantID:   testTenant,
		UserLowID:  "us_alice",
		UserHighID: "us_bob",
	}); err != nil {
		t.Fatalf("create friendship: %v", err)
	}
	if err := store.CreateMessage(ctx, testMessage("msg_friend", conv.ID, "us_alice", testTime(2)), nil, nil); err != nil {
		t.Fatalf("friend send: %v", err)
	}
	if err := store.CreateBlock(ctx, messaging.Block{
		CreatedAt: testTime(3),
		ID:        "bk_block",
		TenantID:  testTenant,
		BlockerID: "us_bob",
		BlockedID: "us_alice",
	}); err != nil {
		t.Fatalf("create block: %v", err)
	}
	if err := store.CreateMessage(ctx, testMessage("msg_blocked", conv.ID, "us_alice", testTime(4)), nil, nil); !errors.Is(err, messaging.ErrBlocked) {
		t.Fatalf("blocked send error = %v, want ErrBlocked", err)
	}
}

// TestCreateMessageAllowsSystemAuthorOutsideNoticeMembership proves the system notifier may write into
// a private notice channel without being a member of it. |
// TestCreateMessageAllowsSystemAuthorOutsideNoticeMembership 证明系统通知器即使不是私有通知频道的成员
// 也能写入。
func TestCreateMessageAllowsSystemAuthorOutsideNoticeMembership(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	conv := seedMembers(t, store, "cv_notice", messaging.KindNotice, messaging.ConversationActive, testTime(0), "us_recipient")
	msg := testMessage("msg_notice", conv.ID, "workflow-system", testTime(1))
	msg.Origin = messaging.OriginSystem
	if err := store.CreateMessage(ctx, msg, nil, nil); err != nil {
		t.Fatalf("system notice: %v", err)
	}
}

// TestCreateMessageRejectsArchivedCatalogueRows proves the shared row locks also refuse archived
// types and tags, closing the window between the service's check and the insert. |
// TestCreateMessageRejectsArchivedCatalogueRows 证明共享行锁同时拒绝已归档类型与标签，消除了服务层
// 检查与插入之间的窗口。
func TestCreateMessageRejectsArchivedCatalogueRows(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	conv := seedMembers(t, store, "cv_archive", messaging.KindGroup, messaging.ConversationActive, testTime(0), "us_alice")
	archivedAt := testTime(1)
	if err := db.Create(&messaging.MessageType{
		CreatedAt: testTime(0), UpdatedAt: testTime(0), ArchivedAt: &archivedAt,
		ID: "mt_archived", TenantID: testTenant, Key: "archived", Name: "归档", NameEN: "Archived", Tone: messaging.ToneNeutral,
	}).Error; err != nil {
		t.Fatalf("seed type: %v", err)
	}
	msg := testMessage("msg_type", conv.ID, "us_alice", testTime(2))
	msg.TypeKey = "archived"
	if err := store.CreateMessage(ctx, msg, nil, nil); !errors.Is(err, messaging.ErrTypeUnknown) {
		t.Fatalf("archived type error = %v, want ErrTypeUnknown", err)
	}

	if err := db.Create(&messaging.Tag{
		CreatedAt: testTime(0), UpdatedAt: testTime(0), ArchivedAt: &archivedAt,
		ID: "tg_archived", TenantID: testTenant, Kind: messaging.TagLabel, Key: "archived", Name: "归档", NameEN: "Archived", Tone: messaging.ToneNeutral,
	}).Error; err != nil {
		t.Fatalf("seed tag: %v", err)
	}
	if err := store.CreateMessage(ctx, testMessage("msg_tag", conv.ID, "us_alice", testTime(2)), []string{"tg_archived"}, nil); !errors.Is(err, messaging.ErrTagUnknown) {
		t.Fatalf("archived tag error = %v, want ErrTagUnknown", err)
	}
}

// TestUpdateMessageDistinguishesMissingFromRecalled keeps a zero-row update honest on MySQL, where
// unchanged values also report zero affected rows. |
// TestUpdateMessageDistinguishesMissingFromRecalled 让零影响行更新保持诚实：在 MySQL 上，值未变化的
// 更新同样报告零影响行。
func TestUpdateMessageDistinguishesMissingFromRecalled(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	conv := seedMembers(t, store, "cv_update", messaging.KindGroup, messaging.ConversationActive, testTime(0), "us_alice")
	if err := store.CreateMessage(ctx, testMessage("msg_update", conv.ID, "us_alice", testTime(1)), nil, nil); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if err := store.UpdateMessage(ctx, messaging.Message{ID: "msg_missing", TenantID: testTenant}, nil); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("missing update error = %v, want ErrNotFound", err)
	}
	if err := db.Model(&messaging.Message{}).
		Where("tenant_id = ? AND id = ?", testTenant, "msg_update").
		Update("status", messaging.StatusRecalled).Error; err != nil {
		t.Fatalf("recall seed: %v", err)
	}
	if err := store.UpdateMessage(ctx, messaging.Message{ID: "msg_update", TenantID: testTenant}, nil); !errors.Is(err, messaging.ErrRecalled) {
		t.Fatalf("recalled update error = %v, want ErrRecalled", err)
	}
}

// TestCreateCatalogueRowsReportConflicts proves the explicit pre-check surfaces a duplicate type or
// tag as ErrConflict instead of a generic statement failure. |
// TestCreateCatalogueRowsReportConflicts 证明显式预查会把重复类型或标签报告为 ErrConflict，而不是泛化的
// 语句失败。
func TestCreateCatalogueRowsReportConflicts(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	messageType := messaging.MessageType{
		CreatedAt: testTime(0), UpdatedAt: testTime(0),
		ID: "mt_custom", TenantID: testTenant, Key: "custom", Name: "自定义", NameEN: "Custom", Tone: messaging.ToneNeutral,
	}
	if err := store.CreateType(ctx, messageType); err != nil {
		t.Fatalf("create type: %v", err)
	}
	duplicateType := messageType
	duplicateType.ID = "mt_custom_2"
	if err := store.CreateType(ctx, duplicateType); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate type error = %v, want ErrConflict", err)
	}

	tag := messaging.Tag{
		CreatedAt: testTime(0), UpdatedAt: testTime(0),
		ID: "tg_custom", TenantID: testTenant, Kind: messaging.TagLabel, Key: "custom", Name: "自定义", NameEN: "Custom", Tone: messaging.ToneNeutral,
	}
	if err := store.CreateTag(ctx, tag); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	duplicateTag := tag
	duplicateTag.ID = "tg_custom_2"
	if err := store.CreateTag(ctx, duplicateTag); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate tag error = %v, want ErrConflict", err)
	}
}

// TestListMessagesUsesStableTieBreak proves the page order is deterministic for messages written at
// the same clock tick. | TestListMessagesUsesStableTieBreak 证明同一时钟刻度写入的消息在分页时顺序稳定。
func TestListMessagesUsesStableTieBreak(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	conv := seedMembers(t, store, "cv_order", messaging.KindGroup, messaging.ConversationActive, testTime(0), "us_alice", "us_bob")
	for _, id := range []string{"msg_a", "msg_b", "msg_c"} {
		if err := store.CreateMessage(ctx, testMessage(id, conv.ID, "us_alice", testTime(1)), nil, nil); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	list, total, err := store.ListMessages(ctx, testTenant, "us_bob", messaging.MessageFilter{Page: 1, PageSize: 10, ConversationID: conv.ID}, false)
	if err != nil || total != 3 {
		t.Fatalf("list total=%d err=%v", total, err)
	}
	if len(list) != 3 || list[0].ID != "msg_c" || list[1].ID != "msg_b" || list[2].ID != "msg_a" {
		t.Fatalf("unstable order: %v", list)
	}
}

// TestLastMessagesResolvesNewestPerConversation keeps the derived-table rewrite working. |
// TestLastMessagesResolvesNewestPerConversation 保证派生表改写仍然可用。
func TestLastMessagesResolvesNewestPerConversation(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	conv := seedMembers(t, store, "cv_last", messaging.KindGroup, messaging.ConversationActive, testTime(0), "us_alice")
	first := testTime(1)
	second := testTime(2)
	if err := store.CreateMessage(ctx, testMessage("msg_first", conv.ID, "us_alice", first), nil, nil); err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := store.CreateMessage(ctx, testMessage("msg_second", conv.ID, "us_alice", second), nil, nil); err != nil {
		t.Fatalf("create second: %v", err)
	}
	last, err := store.LastMessages(ctx, testTenant, []string{conv.ID})
	if err != nil {
		t.Fatalf("last messages: %v", err)
	}
	if got := last[conv.ID]; got.ID != "msg_second" {
		t.Fatalf("last message = %q, want msg_second", got.ID)
	}
}

// TestListAuditUsesStableTieBreak proves audit pages are deterministic for rows written at the same
// clock tick. | TestListAuditUsesStableTieBreak 证明同一时钟刻度写入的审计记录分页顺序稳定。
func TestListAuditUsesStableTieBreak(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for _, id := range []string{"au_a", "au_b", "au_c"} {
		if err := store.AddAudit(ctx, messaging.AuditEntry{
			CreatedAt: at, ID: id, TenantID: testTenant, ActorID: "us_admin", Action: messaging.AuditActionRecall,
		}); err != nil {
			t.Fatalf("add audit: %v", err)
		}
	}
	list, total, err := store.ListAudit(ctx, testTenant, messaging.AuditFilter{Page: 1, PageSize: 10})
	if err != nil || total != 3 {
		t.Fatalf("audit total=%d err=%v", total, err)
	}
	if len(list) != 3 || list[0].ID != "au_c" || list[1].ID != "au_b" || list[2].ID != "au_a" {
		t.Fatalf("unstable audit order: %v", list)
	}
}
