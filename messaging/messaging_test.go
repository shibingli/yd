package messaging_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shibingli/yd/messaging"
	"github.com/shibingli/yd/messaging/filestore"
	"github.com/shibingli/yd/messaging/gormstore"
	"github.com/shibingli/yd/messaging/markdown"
	"github.com/shibingli/ydx/log"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const tenantID = "tn_test"

// testClock is a mutable clock shared by one test. | testClock 是单个测试共享的可变时钟。
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the current test time. | Now 返回当前测试时间。
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward. | Advance 向前推进时钟。
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// directory is a tiny in-memory messaging.Directory. | directory 是极简的内存 messaging.Directory。
type directory struct {
	profiles map[string]messaging.Profile
}

// Search returns active profiles containing the keyword. | Search 返回包含关键字的活跃资料。
func (d *directory) Search(_ context.Context, tenantID, keyword, excludeID string, limit int) ([]messaging.Profile, error) {
	out := make([]messaging.Profile, 0, len(d.profiles))
	for id, profile := range d.profiles {
		if id == excludeID || !profile.Active {
			continue
		}
		if keyword != "" && !containsFold(profile.Username, keyword) && !containsFold(profile.DisplayName, keyword) {
			continue
		}
		out = append(out, profile)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Profiles resolves the requested ids. | Profiles 解析请求的 ID。
func (d *directory) Profiles(_ context.Context, _ string, ids []string) (map[string]messaging.Profile, error) {
	out := make(map[string]messaging.Profile, len(ids))
	for _, id := range ids {
		if profile, ok := d.profiles[id]; ok {
			out[id] = profile
		}
	}
	return out, nil
}

// containsFold is a case-insensitive substring check. | containsFold 是大小写不敏感的子串判断。
func containsFold(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexFold(haystack, needle) >= 0)
}

// indexFold is strings.Index folded to lower case. | indexFold 是转小写后的 strings.Index。
func indexFold(haystack, needle string) int {
	haystack = lower(haystack)
	needle = lower(needle)
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// lower is a tiny ASCII lower-caser for the test directory. | lower 是测试目录使用的极小 ASCII 转小写函数。
func lower(value string) string {
	out := []byte(value)
	for i := range out {
		if out[i] >= 'A' && out[i] <= 'Z' {
			out[i] += 'a' - 'A'
		}
	}
	return string(out)
}

// notifier records published events. | notifier 记录已发布事件。
type notifier struct {
	mu     sync.Mutex
	events []messaging.Event
}

// Publish records one event. | Publish 记录一条事件。
func (n *notifier) Publish(_ context.Context, event messaging.Event) error {
	n.mu.Lock()
	n.events = append(n.events, event)
	n.mu.Unlock()
	return nil
}

// count returns the number of recorded events. | count 返回已记录事件数。
func (n *notifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.events)
}

// snapshot returns a copy of the recorded events. | snapshot 返回已记录事件的副本。
func (n *notifier) snapshot() []messaging.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]messaging.Event, len(n.events))
	copy(out, n.events)
	return out
}

// fixture is one wired service with its dependencies. | fixture 是一套装配好的服务及其依赖。
type fixture struct {
	svc      *messaging.Service
	store    *gormstore.Store
	db       *gorm.DB
	clock    *testClock
	notifier *notifier
	dir      *directory
}

// newFixture builds an isolated SQLite-backed service. | newFixture 构建隔离的 SQLite 服务。
func newFixture(t *testing.T) *fixture {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(lower(t.Name()))
	dsn := "file:" + name + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	ctx := context.Background()
	if err := gormstore.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store, err := gormstore.New(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	blobs, err := filestore.New(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	clock := &testClock{now: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}
	note := &notifier{}
	dir := &directory{profiles: map[string]messaging.Profile{
		"us_alice": {ID: "us_alice", Username: "alice", DisplayName: "Alice", Active: true},
		"us_bob":   {ID: "us_bob", Username: "bob", DisplayName: "Bob", Active: true},
		"us_carol": {ID: "us_carol", Username: "carol", DisplayName: "Carol", Active: true},
		"us_admin": {ID: "us_admin", Username: "admin", DisplayName: "Admin", Active: true},
	}}
	svc, err := messaging.New(store, dir, markdown.New(), blobs, note, log.Default(), messaging.Options{
		Now:               clock.Now,
		RecallWindow:      5 * time.Minute,
		EditWindow:        5 * time.Minute,
		MaxAttachmentSize: 1 << 20,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return &fixture{svc: svc, store: store, db: db, clock: clock, notifier: note, dir: dir}
}

// actor builds an actor of the fixture tenant. | actor 构造 fixture 租户中的主体。
func actor(id, name string) messaging.Actor {
	return messaging.Actor{ID: id, TenantID: tenantID, DisplayName: name}
}

// admin builds a moderator actor. | admin 构造管理员主体。
func admin() messaging.Actor {
	a := actor("us_admin", "Admin")
	a.Manage = true
	a.System = true
	a.TypeManage = true
	return a
}

// befriend establishes an accepted friendship and returns the direct conversation. | befriend 建立已接受好友关系并返回单聊。
func befriend(t *testing.T, f *fixture, left, right messaging.Actor) messaging.Conversation {
	t.Helper()
	req, err := f.svc.SendFriendRequest(context.Background(), left, right.ID, "hi")
	if err != nil {
		t.Fatalf("send friend request: %v", err)
	}
	if err := f.svc.RespondFriendRequest(context.Background(), right, req.ID, true); err != nil {
		t.Fatalf("accept friend request: %v", err)
	}
	conv, err := f.svc.DirectConversation(context.Background(), left, right.ID)
	if err != nil {
		t.Fatalf("direct conversation: %v", err)
	}
	return conv
}

// TestMigrateIsIdempotent proves the schema can be applied twice. | TestMigrateIsIdempotent 证明模式可重复应用。
func TestMigrateIsIdempotent(t *testing.T) {
	f := newFixture(t)
	if err := gormstore.Migrate(context.Background(), f.db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

// TestFriendLifecycle walks request, accept, remark, remove and block. | TestFriendLifecycle 走查申请、接受、备注、删除与拉黑。
func TestFriendLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice", "Alice"), actor("us_bob", "Bob")

	if _, err := f.svc.SendFriendRequest(ctx, alice, alice.ID, ""); !errors.Is(err, messaging.ErrSelfOperation) {
		t.Fatalf("self request err = %v", err)
	}
	req, err := f.svc.SendFriendRequest(ctx, alice, bob.ID, "hello")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := f.svc.SendFriendRequest(ctx, alice, bob.ID, "again"); !errors.Is(err, messaging.ErrDuplicateRequest) {
		t.Fatalf("duplicate err = %v", err)
	}
	if err := f.svc.RespondFriendRequest(ctx, bob, req.ID, true); err != nil {
		t.Fatalf("accept: %v", err)
	}
	staleRequest := req
	staleRequest.Status = messaging.FriendCancelled
	if err := f.store.UpdateFriendRequest(ctx, staleRequest, messaging.FriendPending); !errors.Is(err, messaging.ErrRequestClosed) {
		t.Fatalf("stale request transition error = %v, want ErrRequestClosed", err)
	}
	if ok, err := f.svc.IsFriend(ctx, tenantID, alice.ID, bob.ID); err != nil || !ok {
		t.Fatalf("friend = %v err = %v", ok, err)
	}
	// 已是好友后不能再次申请；关系查询同时覆盖自己与好友两种抽屉场景。
	// An accepted friendship refuses a second request; the relation query covers both the self and friend drawer cases.
	if _, err := f.svc.SendFriendRequest(ctx, alice, bob.ID, ""); !errors.Is(err, messaging.ErrDuplicateRequest) {
		t.Fatalf("request after friendship err = %v", err)
	}
	if relation, err := f.svc.FriendRelation(ctx, alice, bob.ID); err != nil || relation != messaging.RelationFriend {
		t.Fatalf("friend relation = %q err = %v", relation, err)
	}
	if relation, err := f.svc.FriendRelation(ctx, alice, alice.ID); err != nil || relation != messaging.RelationSelf {
		t.Fatalf("self relation = %q err = %v", relation, err)
	}
	if err := f.svc.SetFriendRemark(ctx, alice, bob.ID, "Bobby"); err != nil {
		t.Fatalf("remark: %v", err)
	}
	summary, err := f.svc.FriendSummary(ctx, alice)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary.Friends) != 1 || summary.Friends[0].Remark != "Bobby" {
		t.Fatalf("summary = %+v", summary)
	}
	if err := f.svc.RemoveFriend(ctx, alice, bob.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if ok, _ := f.svc.IsFriend(ctx, tenantID, alice.ID, bob.ID); ok {
		t.Fatal("friendship survived removal")
	}
	if err := f.svc.SetFriendRemark(ctx, alice, bob.ID, "stale"); !errors.Is(err, messaging.ErrNotFriend) {
		t.Fatalf("remark after unfriend error = %v, want ErrNotFriend", err)
	}

	req, err = f.svc.SendFriendRequest(ctx, alice, bob.ID, "again")
	if err != nil {
		t.Fatalf("re-request: %v", err)
	}
	if err := f.svc.RespondFriendRequest(ctx, bob, req.ID, true); err != nil {
		t.Fatalf("re-accept: %v", err)
	}
	summary, err = f.svc.FriendSummary(ctx, alice)
	if err != nil {
		t.Fatalf("summary after re-friend: %v", err)
	}
	if len(summary.Friends) != 1 || summary.Friends[0].Remark != "" {
		t.Fatalf("a removed friendship kept its remark: %+v", summary)
	}
	if err := f.svc.SetFriendRemark(ctx, alice, bob.ID, "Bobby again"); err != nil {
		t.Fatalf("remark after re-friend: %v", err)
	}
	if err := f.svc.BlockUser(ctx, bob, alice.ID); err != nil {
		t.Fatalf("block: %v", err)
	}
	if ok, _ := f.svc.IsFriend(ctx, tenantID, alice.ID, bob.ID); ok {
		t.Fatal("friendship survived a block")
	}
	var notes int64
	if err := f.db.Model(&messaging.FriendNote{}).
		Where("tenant_id = ? AND ((owner_id = ? AND friend_id = ?) OR (owner_id = ? AND friend_id = ?))",
			tenantID, alice.ID, bob.ID, bob.ID, alice.ID).Count(&notes).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if notes != 0 {
		t.Fatalf("friend notes after block = %d, want 0", notes)
	}
	if _, err := f.svc.SendFriendRequest(ctx, alice, bob.ID, ""); !errors.Is(err, messaging.ErrBlocked) {
		t.Fatalf("blocked request err = %v", err)
	}
	if err := f.svc.UnblockUser(ctx, bob, alice.ID); err != nil {
		t.Fatalf("unblock: %v", err)
	}
}

// TestReadEventReachesTheReader proves the read-watermark event also reaches the reader. Otherwise the reader's own
// WebSocket never refreshes the unread badge, and only a page reload clears it. |
// TestReadEventReachesTheReader 证明推进已读水位的事件也会发给读者本人；否则读者自己的 WebSocket 不会刷新未读角标，
// 只能通过刷新页面才能清除。
func TestReadEventReachesTheReader(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice", "Alice"), actor("us_bob", "Bob")
	conv := befriend(t, f, alice, bob)

	if _, err := f.svc.Send(ctx, alice, messaging.SendInput{ConversationID: conv.ID, Markdown: "first"}); err != nil {
		t.Fatalf("send failed: %[1]v | 发送失败: %[1]v", err)
	}
	if err := f.svc.MarkRead(ctx, bob, conv.ID); err != nil {
		t.Fatalf("mark read failed: %[1]v | 标记已读失败: %[1]v", err)
	}
	assertReaderInLastReadEvent(t, f, bob.ID)

	if _, err := f.svc.Send(ctx, alice, messaging.SendInput{ConversationID: conv.ID, Markdown: "second"}); err != nil {
		t.Fatalf("send second failed: %[1]v | 发送第二条失败: %[1]v", err)
	}
	if _, _, err := f.svc.Conversation(ctx, bob, conv.ID); err != nil {
		t.Fatalf("open conversation failed: %[1]v | 打开会话失败: %[1]v", err)
	}
	assertReaderInLastReadEvent(t, f, bob.ID)
}

// assertReaderInLastReadEvent asserts the latest read event lists the reader in its audience. |
// assertReaderInLastReadEvent 断言最新一条已读事件的受众包含读者本人。
func assertReaderInLastReadEvent(t *testing.T, f *fixture, readerID string) {
	t.Helper()

	events := f.notifier.snapshot()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != messaging.EventMessageRead {
			continue
		}
		for _, id := range events[i].Audience {
			if id == readerID {
				return
			}
		}
		t.Fatalf("read event audience = %[1]v, missing reader %[2]s | 已读事件受众 = %[1]v, 缺少读者 %[2]s",
			events[i].Audience, readerID)
	}
	t.Fatal("no read event was emitted | 没有发出已读事件")
}

// TestDirectMessageReadAndRecall walks send, unread, read receipt and recall windows. | TestDirectMessageReadAndRecall 走查发送、未读、已读回执与撤回窗口。
func TestDirectMessageReadAndRecall(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice", "Alice"), actor("us_bob", "Bob")
	conv := befriend(t, f, alice, bob)

	if _, err := f.svc.Send(ctx, alice, messaging.SendInput{ConversationID: conv.ID, Markdown: "before friendship"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	unread, err := f.svc.Unread(ctx, bob)
	if err != nil {
		t.Fatalf("unread: %v", err)
	}
	if unread.Total != 1 {
		t.Fatalf("bob unread = %d", unread.Total)
	}
	view, messages, err := f.svc.Conversation(ctx, bob, conv.ID)
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	if view.Unread != 0 || len(messages) != 1 || messages[0].BodyText == "" {
		t.Fatalf("view = %+v messages = %+v", view, messages)
	}

	if _, err := f.svc.Send(ctx, bob, messaging.SendInput{ConversationID: conv.ID, Markdown: "reply"}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	_, messages, err = f.svc.Conversation(ctx, alice, conv.ID)
	if err != nil {
		t.Fatalf("alice conversation: %v", err)
	}
	if len(messages) != 2 || messages[1].ReadCount != 1 || messages[1].RecipientCount != 1 {
		t.Fatalf("read stats = %+v", messages)
	}

	first, reply := messages[0], messages[1]
	if !first.Mine || reply.Mine {
		t.Fatalf("ownership flags = first:%v reply:%v", first.Mine, reply.Mine)
	}
	if _, err := f.svc.Edit(ctx, alice, reply.ID, messaging.EditInput{Markdown: "edited"}); !errors.Is(err, messaging.ErrNotSender) {
		t.Fatalf("edit other err = %v", err)
	}
	if _, err := f.svc.Edit(ctx, bob, first.ID, messaging.EditInput{Markdown: "edited"}); !errors.Is(err, messaging.ErrNotSender) {
		t.Fatalf("edit other err = %v", err)
	}
	if err := f.svc.Recall(ctx, bob, first.ID, ""); !errors.Is(err, messaging.ErrNotSender) {
		t.Fatalf("recall other err = %v", err)
	}
	f.clock.Advance(6 * time.Minute)
	if err := f.svc.Recall(ctx, alice, first.ID, ""); !errors.Is(err, messaging.ErrRecallExpired) {
		t.Fatalf("expired recall err = %v", err)
	}
	if err := f.svc.Recall(ctx, admin(), first.ID, "moderated"); err != nil {
		t.Fatalf("admin recall: %v", err)
	}
	_, messages, err = f.svc.Conversation(ctx, bob, conv.ID)
	if err != nil {
		t.Fatalf("after recall: %v", err)
	}
	if messages[0].BodyText != "" || messages[0].Status != messaging.StatusRecalled {
		t.Fatalf("recalled message = %+v", messages[0])
	}
}

// TestSystemMessagesPinAndTypes covers announcements, permissions, pinning and catalog lifecycle. |
// TestSystemMessagesPinAndTypes 覆盖公告、权限、置顶与目录生命周期。
func TestSystemMessagesPinAndTypes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := actor("us_alice", "Alice")
	root := admin()

	if _, err := f.svc.SendSystem(ctx, alice, messaging.SendInput{Title: "nope", Markdown: "x"}); !errors.Is(err, messaging.ErrSystemOnly) {
		t.Fatalf("non-admin system err = %v", err)
	}
	view, err := f.svc.SendSystem(ctx, root, messaging.SendInput{Title: "Maintenance", Markdown: "tonight"})
	if err != nil {
		t.Fatalf("system send: %v", err)
	}
	if view.Origin != messaging.OriginSystem {
		t.Fatalf("origin = %q", view.Origin)
	}
	manageOnly := actor("us_manage", "Manage")
	manageOnly.Manage = true
	extra, err := f.svc.SendSystem(ctx, root, messaging.SendInput{Title: "Recall Me", Markdown: "tombstone"})
	if err != nil {
		t.Fatalf("second system send: %v", err)
	}
	if err := f.svc.Recall(ctx, manageOnly, extra.ID, ""); !errors.Is(err, messaging.ErrSystemOnly) {
		t.Fatalf("manage-only system recall error = %v, want ErrSystemOnly", err)
	}
	if _, err := f.svc.Edit(ctx, manageOnly, extra.ID, messaging.EditInput{Title: "hijack"}); !errors.Is(err, messaging.ErrSystemOnly) {
		t.Fatalf("manage-only system edit error = %v, want ErrSystemOnly", err)
	}
	if err := f.svc.Recall(ctx, root, extra.ID, "maintenance done"); err != nil {
		t.Fatalf("system recall: %v", err)
	}
	if _, err := f.svc.Edit(ctx, root, extra.ID, messaging.EditInput{Title: "resurrect"}); !errors.Is(err, messaging.ErrRecalled) {
		t.Fatalf("recalled edit error = %v, want ErrRecalled", err)
	}
	// Alice joins the announcement channel through the unread summary. | Alice 通过未读汇总加入公告频道。
	if _, err := f.svc.Unread(ctx, alice); err != nil {
		t.Fatalf("unread: %v", err)
	}
	pinned, err := f.svc.PinnedMessages(ctx, alice)
	if err != nil {
		t.Fatalf("pinned before: %v", err)
	}
	if len(pinned) != 0 {
		t.Fatalf("pinned before = %+v", pinned)
	}
	if err := f.svc.Pin(ctx, alice, view.ID, nil, nil); !errors.Is(err, messaging.ErrForbidden) {
		t.Fatalf("non-admin pin err = %v", err)
	}
	start := f.clock.Now().Add(-time.Minute)
	end := f.clock.Now().Add(time.Hour)
	if err := f.svc.Pin(ctx, root, view.ID, &start, &end); err != nil {
		t.Fatalf("pin: %v", err)
	}
	pinned, err = f.svc.PinnedMessages(ctx, alice)
	if err != nil {
		t.Fatalf("pinned after: %v", err)
	}
	if len(pinned) != 1 || pinned[0].ID != view.ID {
		t.Fatalf("pinned after = %+v", pinned)
	}
	if err := f.svc.Unpin(ctx, root, view.ID); err != nil {
		t.Fatalf("unpin: %v", err)
	}

	created, err := f.svc.CreateType(ctx, root, messaging.TypeInput{Key: "alert", Name: "警报", NameEN: "Alert", Tone: messaging.ToneDanger})
	if err != nil {
		t.Fatalf("create type: %v", err)
	}
	if created.Key != "alert" {
		t.Fatalf("type = %+v", created)
	}
	if _, err := f.svc.SendSystem(ctx, root, messaging.SendInput{TypeKey: "alert", Title: "Alert", Markdown: "x"}); err != nil {
		t.Fatalf("send custom type: %v", err)
	}
	if err := f.svc.DeleteType(ctx, root, "alert"); !errors.Is(err, messaging.ErrTypeInUse) {
		t.Fatalf("delete used type err = %v", err)
	}
	if err := f.svc.SetTypeArchived(ctx, root, "alert", true); err != nil {
		t.Fatalf("archive type: %v", err)
	}
	if _, err := f.svc.SendSystem(ctx, root, messaging.SendInput{TypeKey: "alert", Title: "x", Markdown: "x"}); !errors.Is(err, messaging.ErrTypeUnknown) {
		t.Fatalf("archived type err = %v", err)
	}
	if err := f.svc.SetTypeArchived(ctx, root, messaging.TypeSystem, true); !errors.Is(err, messaging.ErrTypeProtected) {
		t.Fatalf("protected archive err = %v", err)
	}
}

// TestSearchAndTags covers the mixed full-text filter and multi-tag binding. | TestSearchAndTags 覆盖混合全文过滤与多标签绑定。
func TestSearchAndTags(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice", "Alice"), actor("us_bob", "Bob")
	conv := befriend(t, f, alice, bob)
	group, err := f.svc.CreateTag(ctx, admin(), messaging.TagGroup, messaging.TagInput{Key: "ops", Name: "运维", NameEN: "Ops", Tone: messaging.ToneInfo})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	label, err := f.svc.CreateTag(ctx, admin(), messaging.TagLabel, messaging.TagInput{Key: "urgent", Name: "紧急", NameEN: "Urgent", Tone: messaging.ToneWarn})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	sent, err := f.svc.Send(ctx, alice, messaging.SendInput{
		ConversationID: conv.ID,
		TypeKey:        messaging.TypeFinance,
		Title:          "Budget review",
		Markdown:       "The quarterly budget needs a decision",
		GroupIDs:       []string{group.ID},
		TagIDs:         []string{label.ID},
	})
	if err != nil {
		t.Fatalf("send tagged: %v", err)
	}
	result, err := f.svc.Messages(ctx, bob, messaging.MessageFilter{Query: "budget decision"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if result.Total != 1 || result.Messages[0].ID != sent.ID {
		t.Fatalf("search result = %+v", result)
	}
	if len(result.Messages[0].Tags) != 1 || len(result.Messages[0].Groups) != 1 {
		t.Fatalf("tags = %+v groups = %+v", result.Messages[0].Tags, result.Messages[0].Groups)
	}
	result, err = f.svc.Messages(ctx, bob, messaging.MessageFilter{TagID: group.ID})
	if err != nil || result.Total != 1 {
		t.Fatalf("tag filter total = %d err = %v", result.Total, err)
	}
	result, err = f.svc.Messages(ctx, bob, messaging.MessageFilter{TypeKey: messaging.TypeFinance, SenderID: alice.ID})
	if err != nil || result.Total != 1 {
		t.Fatalf("type/sender filter total = %d err = %v", result.Total, err)
	}
	if _, err := f.svc.Messages(ctx, actor("us_carol", "Carol"), messaging.MessageFilter{Query: "budget"}); err != nil {
		t.Fatalf("non-member search: %v", err)
	}
}

// TestAttachments enforces ownership and lets a participant download. | TestAttachments 强制所有权并允许参与者下载。
func TestAttachments(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob, carol := actor("us_alice", "Alice"), actor("us_bob", "Bob"), actor("us_carol", "Carol")
	conv := befriend(t, f, alice, bob)

	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	attachment, err := f.svc.UploadAttachment(ctx, alice, "../../evil.png", int64(len(png)), bytes.NewReader(png))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if attachment.OriginalName != "evil.png" || attachment.ContentType != "image/png" {
		t.Fatalf("attachment = %+v", attachment)
	}
	if _, err := f.svc.Send(ctx, alice, messaging.SendInput{ConversationID: conv.ID, Markdown: "see attachment", AttachmentIDs: []string{attachment.ID}}); err != nil {
		t.Fatalf("send with attachment: %v", err)
	}
	downloaded, data, err := f.svc.DownloadAttachment(ctx, bob, attachment.ID)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if downloaded.ID != attachment.ID || len(data) != len(png) {
		t.Fatalf("download = %+v len=%d", downloaded, len(data))
	}
	if _, _, err := f.svc.DownloadAttachment(ctx, carol, attachment.ID); !errors.Is(err, messaging.ErrAttachmentNotFound) {
		t.Fatalf("foreign download err = %v", err)
	}
	if _, err := f.svc.UploadAttachment(ctx, alice, "page.html", 10, bytes.NewReader([]byte("<html></html>"))); !errors.Is(err, messaging.ErrAttachmentType) {
		t.Fatalf("html upload err = %v", err)
	}
}

// TestStoreRejectsMissingOrInUseRelations proves that link tables never outlive the rows they point
// at: an unknown tag is rejected before any write, and deleting an in-use tag or type is refused
// inside the same transaction as the delete. | TestStoreRejectsMissingOrInUseRelations 证明连接表不会
// 比其指向的行活得更久：未知标签在写入前被拒绝，删除使用中的标签或类型会在与删除相同的事务内被拒绝。
func TestStoreRejectsMissingOrInUseRelations(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice", "Alice"), actor("us_bob", "Bob")
	conv := befriend(t, f, alice, bob)
	sent, err := f.svc.Send(ctx, alice, messaging.SendInput{ConversationID: conv.ID, Markdown: "hello"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := f.store.UpdateMessage(ctx, sent.Message, []string{"tg_missing"}); !errors.Is(err, messaging.ErrTagUnknown) {
		t.Fatalf("update with missing tag error = %v, want ErrTagUnknown", err)
	}
	var links int64
	if err := f.db.Table("yd_msg_message_tags").Where("tenant_id = ? AND message_id = ?", tenantID, sent.ID).
		Count(&links).Error; err != nil {
		t.Fatalf("count links: %v", err)
	}
	if links != 0 {
		t.Fatalf("message tag links = %d, want 0", links)
	}

	tag, err := f.svc.CreateTag(ctx, admin(), messaging.TagLabel, messaging.TagInput{
		Key: "audit", Name: "审计", NameEN: "Audit", Tone: messaging.ToneInfo,
	})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	if err := f.db.Create(&messaging.MessageTag{
		ID: "mtg_stale", TenantID: tenantID, MessageID: sent.ID, TagID: tag.ID,
	}).Error; err != nil {
		t.Fatalf("seed link: %v", err)
	}
	if err := f.store.DeleteTag(ctx, tenantID, tag.ID); !errors.Is(err, messaging.ErrTypeInUse) {
		t.Fatalf("delete in-use tag error = %v, want ErrTypeInUse", err)
	}
	if err := f.db.Where("tenant_id = ? AND tag_id = ?", tenantID, tag.ID).
		Delete(&messaging.MessageTag{}).Error; err != nil {
		t.Fatalf("clear link: %v", err)
	}
	if err := f.store.DeleteTag(ctx, tenantID, tag.ID); err != nil {
		t.Fatalf("delete unused tag: %v", err)
	}

	custom, err := f.svc.CreateType(ctx, admin(), messaging.TypeInput{
		Key: "notice", Name: "通知", NameEN: "Notice", Tone: messaging.ToneInfo,
	})
	if err != nil {
		t.Fatalf("create type: %v", err)
	}
	if _, err := f.svc.Send(ctx, alice, messaging.SendInput{ConversationID: conv.ID, Markdown: "typed", TypeKey: custom.Key}); err != nil {
		t.Fatalf("send typed: %v", err)
	}
	if err := f.store.DeleteType(ctx, tenantID, custom.Key); !errors.Is(err, messaging.ErrTypeInUse) {
		t.Fatalf("delete in-use type error = %v, want ErrTypeInUse", err)
	}
}
