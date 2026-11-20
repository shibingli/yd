package messaging_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/yd/messaging"
	"github.com/shibingli/yd/messaging/gormstore"
	"github.com/shibingli/yd/messaging/markdown"
	yjson "github.com/shibingli/ydx/json"
	"github.com/shibingli/ydx/log"
)

// failingThreadStore returns an error from the thread page so a failed history load can be exercised. |
// failingThreadStore 从线程分页返回错误，用于演练历史加载失败。
type failingThreadStore struct {
	*gormstore.Store
}

// ListMessagesByConversationPage always fails. | ListMessagesByConversationPage 始终失败。
func (failingThreadStore) ListMessagesByConversationPage(context.Context, string, string, int, int, bool) ([]messaging.Message, error) {
	return nil, errors.New("simulated thread load failure | 模拟线程加载失败")
}

// TestConversationWatermarkUsesNewestMessageTime proves opening a thread moves the watermark to the
// newest committed message instead of the caller's clock, so a concurrently committed message with an
// earlier timestamp is not silently marked read. |
// TestConversationWatermarkUsesNewestMessageTime 证明打开会话把水位推进到最新已提交消息而非调用方时钟，
// 因此并发提交、时间戳更早的消息不会被静默标成已读。
func TestConversationWatermarkUsesNewestMessageTime(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice", "Alice"), actor("us_bob", "Bob")
	conv := befriend(t, f, alice, bob)
	sentAt := f.clock.Now()

	if _, err := f.svc.Send(ctx, alice, messaging.SendInput{ConversationID: conv.ID, Markdown: "first"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	// 读者时钟前进了整整一小时，但期间没有新消息：水位只能停在消息时间。 |
	// The reader's clock advances a full hour while no message arrives: the watermark may only stop at the message time.
	f.clock.Advance(time.Hour)
	if _, _, err := f.svc.Conversation(ctx, bob, conv.ID); err != nil {
		t.Fatalf("open conversation: %v", err)
	}
	member, err := f.store.GetMember(ctx, tenantID, conv.ID, bob.ID)
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	if member.LastReadAt == nil || !member.LastReadAt.Equal(sentAt) {
		t.Fatalf("watermark = %v, want message time %v", member.LastReadAt, sentAt)
	}

	// 一条在读取之后才提交、创建时间落在水位与读者时钟之间的消息必须保持未读。 |
	// A message committed after the read, created between the watermark and the reader's clock, must stay unread.
	lateAt := sentAt.Add(5 * time.Minute)
	late := messaging.Message{
		CreatedAt:      lateAt,
		UpdatedAt:      lateAt,
		ID:             "msg_late_commit",
		TenantID:       tenantID,
		ConversationID: conv.ID,
		SenderID:       alice.ID,
		SenderName:     "Alice",
		Origin:         messaging.OriginUser,
		Status:         messaging.StatusSent,
		BodyText:       "late",
	}
	if err := f.db.Create(&late).Error; err != nil {
		t.Fatalf("seed late message: %v", err)
	}
	unread, err := f.store.ConversationUnread(ctx, tenantID, bob.ID)
	if err != nil {
		t.Fatalf("unread: %v", err)
	}
	if unread[conv.ID] != 1 {
		t.Fatalf("concurrently committed message unread = %d, want 1", unread[conv.ID])
	}

	if err := f.svc.MarkRead(ctx, bob, conv.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	member, err = f.store.GetMember(ctx, tenantID, conv.ID, bob.ID)
	if err != nil {
		t.Fatalf("member after mark read: %v", err)
	}
	if member.LastReadAt == nil || !member.LastReadAt.Equal(lateAt) {
		t.Fatalf("watermark after mark read = %v, want %v", member.LastReadAt, lateAt)
	}
	unread, err = f.store.ConversationUnread(ctx, tenantID, bob.ID)
	if err != nil {
		t.Fatalf("unread after mark read: %v", err)
	}
	if unread[conv.ID] != 0 {
		t.Fatalf("unread after mark read = %d, want 0", unread[conv.ID])
	}
}

// TestConversationWatermarkStaysOnEmptyThread proves an empty conversation never writes a watermark. |
// TestConversationWatermarkStaysOnEmptyThread 证明空会话绝不写入水位。
func TestConversationWatermarkStaysOnEmptyThread(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice", "Alice"), actor("us_bob", "Bob")
	conv := befriend(t, f, alice, bob)

	if _, _, err := f.svc.Conversation(ctx, bob, conv.ID); err != nil {
		t.Fatalf("open empty conversation: %v", err)
	}
	member, err := f.store.GetMember(ctx, tenantID, conv.ID, bob.ID)
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	if member.LastReadAt != nil {
		t.Fatalf("empty conversation wrote watermark %v", member.LastReadAt)
	}
	if err := f.svc.MarkRead(ctx, bob, conv.ID); err != nil {
		t.Fatalf("mark read on empty conversation: %v", err)
	}
	member, err = f.store.GetMember(ctx, tenantID, conv.ID, bob.ID)
	if err != nil {
		t.Fatalf("member after mark read: %v", err)
	}
	if member.LastReadAt != nil {
		t.Fatalf("empty conversation wrote watermark after mark read: %v", member.LastReadAt)
	}
}

// TestFailedThreadLoadDoesNotAdvanceWatermark proves a failed history load leaves the watermark and
// the read event untouched. | TestFailedThreadLoadDoesNotAdvanceWatermark 证明历史加载失败不会推进水位，
// 也不会发出已读事件。
func TestFailedThreadLoadDoesNotAdvanceWatermark(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice", "Alice"), actor("us_bob", "Bob")
	conv := befriend(t, f, alice, bob)
	if _, err := f.svc.Send(ctx, alice, messaging.SendInput{ConversationID: conv.ID, Markdown: "unseen"}); err != nil {
		t.Fatalf("send: %v", err)
	}

	broken, err := messaging.New(failingThreadStore{Store: f.store}, f.dir, markdown.New(), f.attach, f.notifier, log.Default(), messaging.Options{Now: f.clock.Now})
	if err != nil {
		t.Fatalf("broken service: %v", err)
	}
	if _, _, err := broken.Conversation(ctx, bob, conv.ID); err == nil {
		t.Fatal("expected the simulated thread load failure")
	}
	member, err := f.store.GetMember(ctx, tenantID, conv.ID, bob.ID)
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	if member.LastReadAt != nil {
		t.Fatalf("failed thread load advanced the watermark: %v", member.LastReadAt)
	}
	for _, event := range f.notifier.snapshot() {
		if event.Type == messaging.EventMessageRead {
			t.Fatalf("failed thread load emitted a read event: %+v", event)
		}
	}
}

// TestConversationsStripRecalledTitleAndBody proves the conversation-list preview clears the title
// together with the body and that the JSON payload carries neither. |
// TestConversationsStripRecalledTitleAndBody 证明会话列表预览连同标题一起清空正文，且 JSON 载荷两者都不携带。
func TestConversationsStripRecalledTitleAndBody(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice", "Alice"), actor("us_bob", "Bob")
	conv := befriend(t, f, alice, bob)
	sent, err := f.svc.Send(ctx, alice, messaging.SendInput{
		ConversationID: conv.ID,
		Title:          "top secret title",
		Markdown:       "top secret body",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := f.svc.Recall(ctx, alice, sent.ID, ""); err != nil {
		t.Fatalf("recall: %v", err)
	}
	list, err := f.svc.Conversations(ctx, bob)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	found := false
	for _, view := range list {
		if view.ID != conv.ID || view.LastMessage == nil {
			continue
		}
		found = true
		if view.LastMessage.Title != "" || view.LastMessage.BodyMarkdown != "" ||
			view.LastMessage.BodyHTML != "" || view.LastMessage.BodyText != "" {
			t.Fatalf("recalled preview leaked content: %+v", view.LastMessage)
		}
	}
	if !found {
		t.Fatal("conversation preview missing")
	}
	payload, err := yjson.Marshal(list)
	if err != nil {
		t.Fatalf("marshal conversations: %v", err)
	}
	if strings.Contains(string(payload), "top secret") {
		t.Fatalf("recalled content leaked into JSON: %s", payload)
	}
}

// TestAddGroupMemberEnforcesMemberCeiling proves the service path uses the atomic bounded insert, so
// a full group refuses a new member without a check-then-insert window. |
// TestAddGroupMemberEnforcesMemberCeiling 证明服务路径使用原子有界插入，群满时拒绝新成员，不存在
// 先查后插的窗口。
func TestAddGroupMemberEnforcesMemberCeiling(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob, carol := actor("us_alice", "Alice"), actor("us_bob", "Bob"), actor("us_carol", "Carol")
	befriend(t, f, alice, bob)
	befriend(t, f, alice, carol)
	group, err := f.svc.CreateGroup(ctx, alice, "team", []string{bob.ID})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	// 先验证重复添加是幂等成功。 | First verify a duplicate add is idempotent success.
	if err := f.svc.AddGroupMember(ctx, alice, group.ID, bob.ID); err != nil {
		t.Fatalf("duplicate add: %v", err)
	}

	// 用合成成员把群填到默认上限 50（现有 alice 与 bob 两人）。 |
	// Fill the group to the default ceiling of 50 with synthetic members (alice and bob already present).
	for i := 0; i < 48; i++ {
		index := strconv.Itoa(i)
		member := messaging.Member{
			JoinedAt:       f.clock.Now(),
			ID:             "mb_fill_" + index,
			TenantID:       tenantID,
			ConversationID: group.ID,
			UserID:         "us_fill_" + index,
			Role:           messaging.MemberPlain,
		}
		if err := f.store.AddMember(ctx, member); err != nil {
			t.Fatalf("fill member %s: %v", index, err)
		}
	}
	if err := f.svc.AddGroupMember(ctx, alice, group.ID, carol.ID); !errors.Is(err, messaging.ErrTooManyMembers) {
		t.Fatalf("full group add error = %v, want ErrTooManyMembers", err)
	}
	members, err := f.store.ListMembers(ctx, tenantID, group.ID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 50 {
		t.Fatalf("member count = %d, want 50", len(members))
	}
}

// TestConversationMessagesLimitReportsEarlier proves the history window is bounded, reports whether
// older messages exist, and keeps the newest page in oldest-first order. |
// TestConversationMessagesLimitReportsEarlier 证明历史窗口有界、能报告是否存在更早消息，并保持最新一页
// 按从旧到新排列。
func TestConversationMessagesLimitReportsEarlier(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice", "Alice"), actor("us_bob", "Bob")
	conv := befriend(t, f, alice, bob)
	for i := 0; i < 60; i++ {
		f.clock.Advance(time.Second)
		if _, err := f.svc.Send(ctx, alice, messaging.SendInput{
			ConversationID: conv.ID,
			Markdown:       "m" + strconv.Itoa(i),
		}); err != nil {
			t.Fatalf("send %d: %v | 发送第 %d 条失败：%v", i, err, i, err)
		}
	}

	views, hasEarlier, err := f.svc.ConversationMessagesLimit(ctx, alice, conv.ID, 50)
	if err != nil {
		t.Fatalf("ConversationMessagesLimit(50) = %v | 返回 %v", err, err)
	}
	if !hasEarlier || len(views) != 50 {
		t.Fatalf("limit 50: hasEarlier=%v len=%d, want true/50 | limit 50：hasEarlier=%v len=%d，期望 true/50", hasEarlier, len(views), hasEarlier, len(views))
	}
	if got := views[0].BodyMarkdown; got != "m10" {
		t.Errorf("oldest visible = %q, want m10 | 最旧可见消息 = %q，期望 m10", got, got)
	}
	if got := views[len(views)-1].BodyMarkdown; got != "m59" {
		t.Errorf("newest visible = %q, want m59 | 最新可见消息 = %q，期望 m59", got, got)
	}

	all, hasEarlier, err := f.svc.ConversationMessagesLimit(ctx, alice, conv.ID, 100)
	if err != nil || hasEarlier || len(all) != 60 {
		t.Fatalf("limit 100: hasEarlier=%v len=%d err=%v, want false/60 | limit 100：hasEarlier=%v len=%d err=%v，期望 false/60", hasEarlier, len(all), err, hasEarlier, len(all), err)
	}

	opened, _, err := f.svc.Conversation(ctx, alice, conv.ID)
	if err != nil || !opened.HasEarlier {
		t.Fatalf("Conversation HasEarlier = %v err=%v, want true | Conversation 的 HasEarlier = %v err=%v，期望 true", opened.HasEarlier, err, opened.HasEarlier, err)
	}
}
