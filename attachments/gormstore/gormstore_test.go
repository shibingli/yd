package gormstore

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shibingli/yd/attachments"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// testTenant is the tenant used by the store-level attachment tests. | testTenant 是存储层附件测试使用的租户。
const testTenant = "tn_attach_store"

// newTestStore opens an isolated SQLite store with both attachment tables. SQLite serialises the
// connection pool, so the concurrency test exercises the guarded statements deterministically; on a
// server engine the same statements rely on the row locks taken by clause.Locking. |
// newTestStore 打开一个带两张附件表的隔离 SQLite 存储。SQLite 会串行化连接池，因此并发测试以确定性
// 方式验证带守卫的语句；在服务器引擎上，同样的语句依赖 clause.Locking 取得的行锁。
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

// seedAttachment inserts one row with a deterministic identity. | seedAttachment 写入一条确定性记录。
func seedAttachment(t *testing.T, db *gorm.DB, id, tenantID string, status attachments.Status, at time.Time) attachments.Attachment {
	t.Helper()
	attachment := attachments.Attachment{
		CreatedAt:    at,
		ID:           id,
		TenantID:     tenantID,
		UploaderID:   "us_uploader",
		OriginalName: id + ".png",
		StoredKey:    "opaque/" + id,
		ContentType:  "image/png",
		SHA256:       "digest",
		Size:         1,
		Status:       status,
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatalf("seed attachment %s: %v", id, err)
	}
	return attachment
}

// TestReleaseKeepsSharedAttachmentsReferenced proves releasing one business object only returns
// attachments with no other references to pending, and that the guarded update re-checks NOT EXISTS. |
// TestReleaseKeepsSharedAttachmentsReferenced 证明释放一个业务对象只会把没有其它引用的附件退回
// pending，且带条件的更新会复查 NOT EXISTS。
func TestReleaseKeepsSharedAttachmentsReferenced(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	shared := seedAttachment(t, db, "att_shared", testTenant, attachments.StatusPending, at)
	single := seedAttachment(t, db, "att_single", testTenant, attachments.StatusPending, at)

	firstRef := attachments.Ref{Type: attachments.RefTypeMessage, ID: "msg_1"}
	secondRef := attachments.Ref{Type: attachments.RefTypeMessage, ID: "msg_2"}
	if err := store.Bind(ctx, testTenant, []string{shared.ID, single.ID}, firstRef, "us_uploader", at); err != nil {
		t.Fatalf("bind first: %v", err)
	}
	if err := store.Bind(ctx, testTenant, []string{shared.ID}, secondRef, "us_uploader", at); err != nil {
		t.Fatalf("bind second: %v", err)
	}
	if err := store.Release(ctx, testTenant, firstRef, at.Add(time.Minute)); err != nil {
		t.Fatalf("release first: %v", err)
	}
	gotShared, err := store.Get(ctx, testTenant, shared.ID)
	if err != nil {
		t.Fatalf("get shared: %v", err)
	}
	if gotShared.Status != attachments.StatusReferenced || gotShared.ReleasedAt != nil {
		t.Fatalf("shared attachment = status %q released_at %v, want referenced and nil", gotShared.Status, gotShared.ReleasedAt)
	}
	gotSingle, err := store.Get(ctx, testTenant, single.ID)
	if err != nil {
		t.Fatalf("get single: %v", err)
	}
	if gotSingle.Status != attachments.StatusPending || gotSingle.ReleasedAt == nil {
		t.Fatalf("single attachment = status %q released_at %v, want pending with released_at", gotSingle.Status, gotSingle.ReleasedAt)
	}

	if err := store.Release(ctx, testTenant, secondRef, at.Add(2*time.Minute)); err != nil {
		t.Fatalf("release second: %v", err)
	}
	gotShared, err = store.Get(ctx, testTenant, shared.ID)
	if err != nil {
		t.Fatalf("get shared after second release: %v", err)
	}
	if gotShared.Status != attachments.StatusPending {
		t.Fatalf("shared attachment after last release = %q, want pending", gotShared.Status)
	}
}

// TestDeleteExpiredPendingSkipsReferencedRows proves the sweep only tombstones genuinely unreferenced
// pending uploads; both the tenant and the cross-tenant variant must respect NOT EXISTS even when a
// reference row appears while the candidate is pending. |
// TestDeleteExpiredPendingSkipsReferencedRows 证明清理只把真正无引用的 pending 上传变成墓碑；
// 租户与跨租户两个版本都必须在候选仍为 pending 时尊重 NOT EXISTS。
func TestDeleteExpiredPendingSkipsReferencedRows(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	before := now.Add(-24 * time.Hour)

	orphan := seedAttachment(t, db, "att_orphan", testTenant, attachments.StatusPending, old)
	raced := seedAttachment(t, db, "att_raced", testTenant, attachments.StatusPending, old)
	live := seedAttachment(t, db, "att_live", testTenant, attachments.StatusReferenced, old)
	// 引用行先于状态写入出现，模拟 Bind 在候选查询与更新之间提交的竞态窗口。 |
	// The reference row exists before the status write, simulating a Bind that commits between the
	// candidate query and the update.
	for _, binding := range []attachments.RefBinding{
		{CreatedAt: old, TenantID: testTenant, AttachmentID: raced.ID, RefType: attachments.RefTypeMessage, RefID: "msg_raced"},
		{CreatedAt: old, TenantID: testTenant, AttachmentID: live.ID, RefType: attachments.RefTypeMessage, RefID: "msg_live"},
	} {
		if err := db.Create(&binding).Error; err != nil {
			t.Fatalf("seed reference: %v", err)
		}
	}

	deleted, err := store.DeleteExpiredPending(ctx, testTenant, before, now, "expired", 200)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if len(deleted) != 1 || deleted[0].ID != orphan.ID {
		t.Fatalf("deleted = %+v, want only %s", deleted, orphan.ID)
	}
	for id, want := range map[string]attachments.Status{
		orphan.ID: attachments.StatusDeleted,
		raced.ID:  attachments.StatusPending,
		live.ID:   attachments.StatusReferenced,
	} {
		got, err := store.Get(ctx, testTenant, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if got.Status != want {
			t.Fatalf("%s status = %q, want %q", id, got.Status, want)
		}
	}

	// 跨租户版本同样只清理无引用行。 | The cross-tenant variant also removes only unreferenced rows.
	otherTenant := "tn_attach_other"
	otherOrphan := seedAttachment(t, db, "att_other_orphan", otherTenant, attachments.StatusPending, old)
	otherReferenced := seedAttachment(t, db, "att_other_referenced", otherTenant, attachments.StatusPending, old)
	if err := db.Create(&attachments.RefBinding{
		CreatedAt: old, TenantID: otherTenant, AttachmentID: otherReferenced.ID, RefType: attachments.RefTypeMessage, RefID: "msg_other",
	}).Error; err != nil {
		t.Fatalf("seed other reference: %v", err)
	}
	deleted, err = store.DeleteExpiredPendingAll(ctx, before, now, "expired", 200)
	if err != nil {
		t.Fatalf("delete expired all: %v", err)
	}
	if len(deleted) != 1 || deleted[0].ID != otherOrphan.ID {
		t.Fatalf("cross-tenant deleted = %+v, want only %s", deleted, otherOrphan.ID)
	}
	gotReferenced, err := store.Get(ctx, otherTenant, otherReferenced.ID)
	if err != nil {
		t.Fatalf("get other referenced: %v", err)
	}
	if gotReferenced.Status != attachments.StatusPending {
		t.Fatalf("referenced pending row was tombstoned: %q", gotReferenced.Status)
	}
}

// TestConcurrentBindReleaseKeepsStatusConsistent proves the lock order and the guarded update keep
// the status in sync with the reference count under interleaved binds and releases. |
// TestConcurrentBindReleaseKeepsStatusConsistent 证明交错绑定与释放时，加锁顺序与带守卫的更新让状态
// 始终与引用数一致。
func TestConcurrentBindReleaseKeepsStatusConsistent(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	attachment := seedAttachment(t, db, "att_race", testTenant, attachments.StatusPending, at)

	const rounds = 16
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		ref := attachments.Ref{Type: attachments.RefTypeMessage, ID: "msg_" + strconv.Itoa(i)}
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = store.Bind(ctx, testTenant, []string{attachment.ID}, ref, "us_uploader", at)
		}()
		go func() {
			defer wg.Done()
			_ = store.Release(ctx, testTenant, ref, at)
		}()
	}
	wg.Wait()

	var refCount int64
	if err := db.Model(&attachments.RefBinding{}).
		Where("tenant_id = ? AND attachment_id = ?", testTenant, attachment.ID).
		Count(&refCount).Error; err != nil {
		t.Fatalf("count references: %v", err)
	}
	got, err := store.Get(ctx, testTenant, attachment.ID)
	if err != nil {
		t.Fatalf("get attachment: %v", err)
	}
	if (got.Status == attachments.StatusReferenced) != (refCount > 0) {
		t.Fatalf("status = %q with %d references", got.Status, refCount)
	}
	if got.Status == attachments.StatusPending && got.ReleasedAt == nil {
		t.Fatal("pending attachment lost its released_at marker")
	}
}

// TestReleaseMissingReferenceIsNoop proves releasing an unknown business object does not fail. |
// TestReleaseMissingReferenceIsNoop 证明释放未知业务对象不会失败。
func TestReleaseMissingReferenceIsNoop(t *testing.T) {
	store, _ := newTestStore(t)
	if err := store.Release(context.Background(), testTenant, attachments.Ref{Type: attachments.RefTypeMessage, ID: "msg_missing"}, time.Now().UTC()); err != nil {
		t.Fatalf("release missing: %v", err)
	}
}

// TestCrossTenantReleaseLeavesOtherReferences proves a release scoped to one tenant never touches
// another tenant's rows. | TestCrossTenantReleaseLeavesOtherReferences 证明限定租户的释放绝不会触碰
// 其它租户的行。
func TestCrossTenantReleaseLeavesOtherReferences(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	mine := seedAttachment(t, db, "att_mine", testTenant, attachments.StatusPending, at)
	theirs := seedAttachment(t, db, "att_theirs", "tn_attach_other", attachments.StatusPending, at)
	ref := attachments.Ref{Type: attachments.RefTypeMessage, ID: "msg_shared"}
	if err := store.Bind(ctx, testTenant, []string{mine.ID}, ref, "us_uploader", at); err != nil {
		t.Fatalf("bind mine: %v", err)
	}
	if err := store.Bind(ctx, "tn_attach_other", []string{theirs.ID}, ref, "us_uploader", at); err != nil {
		t.Fatalf("bind theirs: %v", err)
	}
	if err := store.Release(ctx, testTenant, ref, at.Add(time.Minute)); err != nil {
		t.Fatalf("release mine: %v", err)
	}
	got, err := store.Get(ctx, "tn_attach_other", theirs.ID)
	if err != nil {
		t.Fatalf("get theirs: %v", err)
	}
	if got.Status != attachments.StatusReferenced {
		t.Fatalf("other tenant attachment changed to %q", got.Status)
	}
}
