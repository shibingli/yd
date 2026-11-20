package gormstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shibingli/yd/messaging"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// testTenant is the tenant used by the store-level tests. | testTenant 是存储层测试使用的租户。
const testTenant = "tn_msg_store"

// newTestStore opens an isolated SQLite store with every message table. SQLite serialises the
// connection pool, so the concurrency tests below exercise the guarded SQL deterministically; on a
// server engine the same statements rely on the row lock taken by clause.Locking. |
// newTestStore 打开一个带全部消息表的隔离 SQLite 存储。SQLite 会串行化连接池，因此下面的并发测试以
// 确定性方式验证带守卫的 SQL；在服务器引擎上，同样的语句依赖 clause.Locking 取得的行锁。
func newTestStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(strings.ToLower(t.Name()))
	dsn := "file:" + name + "?mode=memory&cache=shared&_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return store, db
}

// testTime returns a deterministic timestamp offset by minutes. | testTime 返回按分钟偏移的确定性时间戳。
func testTime(minute int) time.Time {
	return time.Date(2026, 11, 1, 9, minute, 0, 0, time.UTC)
}

// seedMembers creates a conversation with the supplied member ids, all joining at joined. |
// seedMembers 创建会话并写入给定成员，全部在 joined 时刻加入。
func seedMembers(t *testing.T, store *Store, convID string, kind messaging.ConversationKind, status messaging.ConversationStatus, joined time.Time, userIDs ...string) messaging.Conversation {
	t.Helper()
	conv := messaging.Conversation{
		CreatedAt: joined,
		UpdatedAt: joined,
		ID:        convID,
		TenantID:  testTenant,
		Kind:      kind,
		CreatedBy: userIDs[0],
		Status:    status,
	}
	members := make([]messaging.Member, 0, len(userIDs))
	for i, userID := range userIDs {
		role := messaging.MemberPlain
		if i == 0 {
			role = messaging.MemberOwner
		}
		members = append(members, messaging.Member{
			JoinedAt:       joined,
			ID:             "mb_" + convID + "_" + userID,
			TenantID:       testTenant,
			ConversationID: convID,
			UserID:         userID,
			Role:           role,
		})
	}
	if err := store.CreateConversation(context.Background(), conv, members); err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	return conv
}

// testMessage builds one sent message. | testMessage 构造一条已发送消息。
func testMessage(id, convID, senderID string, at time.Time) messaging.Message {
	return messaging.Message{
		CreatedAt:      at,
		UpdatedAt:      at,
		ID:             id,
		TenantID:       testTenant,
		ConversationID: convID,
		SenderID:       senderID,
		SenderName:     senderID,
		Origin:         messaging.OriginUser,
		Status:         messaging.StatusSent,
		BodyText:       "body",
	}
}

// TestAdvanceReadToLatestUsesCommittedMessages proves the watermark targets the newest committed
// message, stays put on an empty conversation and never covers a message committed with an earlier
// timestamp. | TestAdvanceReadToLatestUsesCommittedMessages 证明水位指向最新已提交消息、空会话保持
// 不动，并且绝不会覆盖以更早时间戳提交的消息。
func TestAdvanceReadToLatestUsesCommittedMessages(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	conv := seedMembers(t, store, "cv_read", messaging.KindGroup, messaging.ConversationActive, testTime(0), "us_alice", "us_bob")

	watermark, moved, err := store.AdvanceReadToLatest(ctx, testTenant, conv.ID, "us_bob")
	if err != nil {
		t.Fatalf("empty advance: %v", err)
	}
	if moved || !watermark.IsZero() {
		t.Fatalf("empty conversation advanced: watermark=%v moved=%v", watermark, moved)
	}
	member, err := store.GetMember(ctx, testTenant, conv.ID, "us_bob")
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member.LastReadAt != nil {
		t.Fatalf("empty conversation wrote a watermark: %v", member.LastReadAt)
	}

	if err := store.CreateMessage(ctx, testMessage("msg_first", conv.ID, "us_alice", testTime(0)), nil, nil); err != nil {
		t.Fatalf("create first message: %v", err)
	}
	watermark, moved, err = store.AdvanceReadToLatest(ctx, testTenant, conv.ID, "us_bob")
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if !moved || !watermark.Equal(testTime(0)) {
		t.Fatalf("watermark = %v moved = %v, want %v", watermark, moved, testTime(0))
	}
	// 单调：重复推进不再移动。 | Monotonic: a repeated advance never moves.
	if _, moved, err = store.AdvanceReadToLatest(ctx, testTenant, conv.ID, "us_bob"); err != nil || moved {
		t.Fatalf("repeat advance moved=%v err=%v", moved, err)
	}

	// 直接在数据库里写入一条更晚的消息，模拟「读取时钟之后才提交、但创建时间早于时钟」的并发提交。
	// Seed a later message directly to simulate a concurrent commit that lands after the reader's
	// clock but was created before it; the message-driven watermark must keep it unread.
	late := testMessage("msg_late", conv.ID, "us_alice", testTime(5))
	if err := db.Create(&late).Error; err != nil {
		t.Fatalf("seed late message: %v", err)
	}
	unread, err := store.ConversationUnread(ctx, testTenant, "us_bob")
	if err != nil {
		t.Fatalf("unread: %v", err)
	}
	if unread[conv.ID] != 1 {
		t.Fatalf("late message unread = %d, want 1", unread[conv.ID])
	}
	member, err = store.GetMember(ctx, testTenant, conv.ID, "us_bob")
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member.LastReadAt == nil || !member.LastReadAt.Equal(testTime(0)) {
		t.Fatalf("watermark = %v, want %v", member.LastReadAt, testTime(0))
	}

	watermark, moved, err = store.AdvanceReadToLatest(ctx, testTenant, conv.ID, "us_bob")
	if err != nil || !moved || !watermark.Equal(testTime(5)) {
		t.Fatalf("second advance = %v moved=%v err=%v", watermark, moved, err)
	}
	unread, err = store.ConversationUnread(ctx, testTenant, "us_bob")
	if err != nil {
		t.Fatalf("unread after advance: %v", err)
	}
	if unread[conv.ID] != 0 {
		t.Fatalf("unread after advance = %d, want 0", unread[conv.ID])
	}
}

// TestConversationUnreadTreatsWatermarkEqualityAsRead keeps the count and the FolderUnread filter on
// the same comparison: a message created exactly at the watermark is read. |
// TestConversationUnreadTreatsWatermarkEqualityAsRead 让未读计数与 FolderUnread 过滤使用同一比较：
// 创建时间正好等于水位的消息视为已读。
func TestConversationUnreadTreatsWatermarkEqualityAsRead(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	conv := seedMembers(t, store, "cv_equal", messaging.KindGroup, messaging.ConversationActive, testTime(0), "us_alice", "us_bob")
	readAt := testTime(0)
	if err := store.dbc(ctx).Model(&messaging.Member{}).
		Where("tenant_id = ? AND conversation_id = ? AND user_id = ?", testTenant, conv.ID, "us_bob").
		Update("last_read_at", readAt).Error; err != nil {
		t.Fatalf("seed watermark: %v", err)
	}
	if err := store.CreateMessage(ctx, testMessage("msg_equal", conv.ID, "us_alice", readAt), nil, nil); err != nil {
		t.Fatalf("create message at the watermark: %v", err)
	}
	unread, err := store.ConversationUnread(ctx, testTenant, "us_bob")
	if err != nil {
		t.Fatalf("unread: %v", err)
	}
	if unread[conv.ID] != 0 {
		t.Fatalf("message at the watermark counted as unread: %d", unread[conv.ID])
	}
}

// TestAddMemberBoundedEnforcesMembershipCeiling proves the ceiling check and the insert live in one
// transaction, so concurrent adds cannot overshoot MaxMembers. |
// TestAddMemberBoundedEnforcesMembershipCeiling 证明上限检查与插入处于同一事务，并发添加无法突破
// MaxMembers。
func TestAddMemberBoundedEnforcesMembershipCeiling(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	conv := seedMembers(t, store, "cv_bounded", messaging.KindGroup, messaging.ConversationActive, testTime(0), "us_owner", "us_existing")
	const maxMembers = 5

	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		userID := "us_" + string(rune('a'+i))
		member := messaging.Member{
			JoinedAt:       testTime(1),
			ID:             "mb_bounded_" + userID,
			TenantID:       testTenant,
			ConversationID: conv.ID,
			UserID:         userID,
			Role:           messaging.MemberPlain,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.AddMemberBounded(ctx, member, maxMembers)
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	added, overflow := 0, 0
	for err := range results {
		switch {
		case err == nil:
			added++
		case errors.Is(err, messaging.ErrTooManyMembers):
			overflow++
		default:
			t.Fatalf("unexpected add error: %v", err)
		}
	}
	if added != maxMembers-2 || overflow != 8-(maxMembers-2) {
		t.Fatalf("added=%d overflow=%d, want %d/%d", added, overflow, maxMembers-2, 8-(maxMembers-2))
	}
	members, err := store.ListMembers(ctx, testTenant, conv.ID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != maxMembers {
		t.Fatalf("member count = %d, want %d", len(members), maxMembers)
	}

	// An existing membership is idempotent success even when the group is full. |
	// 群已满时，已存在的成员关系仍按幂等成功处理。
	inserted, err := store.AddMemberBounded(ctx, messaging.Member{
		JoinedAt:       testTime(1),
		ID:             "mb_duplicate",
		TenantID:       testTenant,
		ConversationID: conv.ID,
		UserID:         "us_existing",
		Role:           messaging.MemberPlain,
	}, maxMembers)
	if err != nil || inserted {
		t.Fatalf("duplicate add inserted=%v err=%v", inserted, err)
	}
}

// TestAddMemberBoundedRejectsDissolvedConversation proves the in-transaction re-check refuses a
// conversation that was dissolved after the service's pre-check. |
// TestAddMemberBoundedRejectsDissolvedConversation 证明事务内复查会拒绝在服务层预检之后被解散的会话。
func TestAddMemberBoundedRejectsDissolvedConversation(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	conv := seedMembers(t, store, "cv_dissolved", messaging.KindGroup, messaging.ConversationDissolved, testTime(0), "us_owner")
	_, err := store.AddMemberBounded(ctx, messaging.Member{
		JoinedAt:       testTime(1),
		ID:             "mb_late",
		TenantID:       testTenant,
		ConversationID: conv.ID,
		UserID:         "us_late",
		Role:           messaging.MemberPlain,
	}, 50)
	if !errors.Is(err, messaging.ErrConversationClosed) {
		t.Fatalf("dissolved add error = %v, want ErrConversationClosed", err)
	}
}

// TestConversationWritesRejectMissingRows proves the locked writes still report not-found. |
// TestConversationWritesRejectMissingRows 证明加锁写入仍然报告未找到。
func TestConversationWritesRejectMissingRows(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	if err := store.RemoveMember(ctx, testTenant, "cv_missing", "us_alice"); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("remove member error = %v, want ErrNotFound", err)
	}
	err := store.UpdateConversation(ctx, messaging.Conversation{
		TenantID: testTenant,
		ID:       "cv_missing",
		Title:    "x",
		Status:   messaging.ConversationActive,
	})
	if !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("update conversation error = %v, want ErrNotFound", err)
	}
}
