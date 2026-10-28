package attachments_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shibingli/yd/attachments"
	"github.com/shibingli/yd/attachments/filestore"
	attachmentsgorm "github.com/shibingli/yd/attachments/gormstore"
	"github.com/shibingli/ydx/log"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const tenant = "tn_attach"

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type fixture struct {
	t      *testing.T
	svc    *attachments.Service
	store  *attachmentsgorm.Store
	db     *gorm.DB
	clock  *clock
	nextID int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(strings.ToLower(t.Name()))
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
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
	if err := attachmentsgorm.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store, err := attachmentsgorm.New(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	blobs, err := filestore.New(t.TempDir())
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}
	f := &fixture{t: t, db: db, clock: &clock{now: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}, nextID: 0}
	svc, err := attachments.New(store, blobs, log.Default(), attachments.Options{
		Now:     f.clock.Now,
		MaxSize: 1 << 20,
		NewID: func(prefix string) (string, error) {
			f.nextID++
			return prefix + strconvI(f.nextID), nil
		},
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	f.svc = svc
	f.store = store
	return f
}

func strconvI(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	return string(buf[i:])
}

func actor(id string) attachments.Actor {
	return attachments.Actor{ID: id, TenantID: tenant, DisplayName: id}
}

func manager(id string) attachments.Actor {
	return attachments.Actor{ID: id, TenantID: tenant, DisplayName: id, Manage: true}
}

func pngBytes(n int) []byte {
	out := make([]byte, n)
	copy(out, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	return out
}

func TestUploadAndKeyLayout(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	attachment, err := f.svc.Upload(ctx, actor("us_alice"), "../../evil.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if attachment.Status != attachments.StatusPending || attachment.OriginalName != "evil.png" || attachment.Size != 12 {
		t.Fatalf("attachment = %+v", attachment)
	}
	parts := strings.Split(attachment.StoredKey, "/")
	if len(parts) != 6 || parts[2] != "2026" || parts[3] != "10" || parts[4] != "27" || parts[5] != attachment.ID {
		t.Fatalf("stored key = %q", attachment.StoredKey)
	}
	if strings.Contains(attachment.StoredKey, "evil") || strings.Contains(attachment.StoredKey, "us_alice") {
		t.Fatalf("key leaks name or identity: %q", attachment.StoredKey)
	}
	if _, err := f.svc.Upload(ctx, actor("us_alice"), "big.png", 2<<20, bytes.NewReader(pngBytes(10))); !errors.Is(err, attachments.ErrTooLarge) {
		t.Fatalf("too large err = %v", err)
	}
	if _, err := f.svc.Upload(ctx, actor("us_alice"), "page.html", 10, bytes.NewReader([]byte("<html></html>"))); !errors.Is(err, attachments.ErrTypeNotAllowed) {
		t.Fatalf("html err = %v", err)
	}
}

func TestOwnershipAndLists(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob := actor("us_alice"), actor("us_bob")
	first, err := f.svc.Upload(ctx, alice, "a.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := f.svc.Upload(ctx, bob, "b.png", 12, bytes.NewReader(pngBytes(12))); err != nil {
		t.Fatalf("bob upload: %v", err)
	}
	page, err := f.svc.ListOwn(ctx, alice, attachments.Filter{})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("alice list = %+v err=%v", page, err)
	}
	all, err := f.svc.ListAll(ctx, manager("us_admin"), attachments.Filter{})
	if err != nil || all.Total != 2 {
		t.Fatalf("admin list = %+v err=%v", all, err)
	}
	if _, err := f.svc.Get(ctx, bob, first.ID); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("bob get err = %v", err)
	}
	if _, err := f.svc.ListAll(ctx, alice, attachments.Filter{}); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("non-manager all err = %v", err)
	}
	if _, _, err := f.svc.Download(ctx, bob, first.ID); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("bob download err = %v", err)
	}
	_, data, err := f.svc.Download(ctx, alice, first.ID)
	if err != nil || len(data) != 12 {
		t.Fatalf("alice download len=%d err=%v", len(data), err)
	}
}

func TestBindReleaseAndDeleteRules(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := actor("us_alice")
	attachment, err := f.svc.Upload(ctx, alice, "a.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	ref := attachments.Ref{Type: "message", ID: "msg_1"}
	if err := f.svc.Bind(ctx, alice, []string{attachment.ID}, ref); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := f.svc.Bind(ctx, alice, []string{attachment.ID}, ref); err != nil {
		t.Fatalf("idempotent bind: %v", err)
	}
	got, err := f.svc.Lookup(ctx, tenant, attachment.ID)
	if err != nil || got.Status != attachments.StatusReferenced || got.RefCount != 1 {
		t.Fatalf("bound = %+v err=%v", got, err)
	}
	if err := f.svc.DeleteOwn(ctx, alice, attachment.ID); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("delete referenced own err = %v", err)
	}
	byRef, err := f.svc.ListByRef(ctx, tenant, ref)
	if err != nil || len(byRef) != 1 {
		t.Fatalf("list by ref = %+v err=%v", byRef, err)
	}
	if err := f.svc.Release(ctx, alice, ref); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, err = f.svc.Lookup(ctx, tenant, attachment.ID)
	if err != nil || got.Status != attachments.StatusPending || got.RefCount != 0 {
		t.Fatalf("released = %+v err=%v", got, err)
	}
	if err := f.svc.DeleteOwn(ctx, alice, attachment.ID); err != nil {
		t.Fatalf("delete unreferenced own: %v", err)
	}
	if _, err := f.svc.Lookup(ctx, tenant, attachment.ID); err != nil {
		t.Fatalf("tombstone lookup: %v", err)
	}
	if _, _, err := f.svc.Download(ctx, alice, attachment.ID); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("download tombstone err = %v", err)
	}
}

func TestManagedDeleteKeepsTombstoneAndRequiresReason(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, admin := actor("us_alice"), manager("us_admin")
	attachment, err := f.svc.Upload(ctx, alice, "a.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	ref := attachments.Ref{Type: "message", ID: "msg_1"}
	if err := f.svc.Bind(ctx, alice, []string{attachment.ID}, ref); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := f.svc.DeleteManaged(ctx, admin, attachment.ID, "  "); !errors.Is(err, attachments.ErrReasonRequired) {
		t.Fatalf("missing reason err = %v", err)
	}
	outcome, err := f.svc.DeleteManaged(ctx, admin, attachment.ID, "违规内容")
	if err != nil {
		t.Fatalf("managed delete: %v", err)
	}
	if outcome.UploaderID != alice.ID || outcome.Reason != "违规内容" || outcome.Attachment.Status != attachments.StatusDeleted {
		t.Fatalf("outcome = %+v", outcome)
	}
	got, err := f.svc.Lookup(ctx, tenant, attachment.ID)
	if err != nil || got.Status != attachments.StatusDeleted || got.DeleteReason != "违规内容" {
		t.Fatalf("tombstone = %+v err=%v", got, err)
	}
	byRef, err := f.svc.ListByRef(ctx, tenant, ref)
	if err != nil || len(byRef) != 1 || byRef[0].Status != attachments.StatusDeleted {
		t.Fatalf("tombstone refs = %+v err=%v", byRef, err)
	}
	if _, err := f.svc.DeleteManaged(ctx, admin, attachment.ID, "again"); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("second delete err = %v", err)
	}
}

func TestDeleteUnreferencedAndCleanup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := actor("us_alice")
	kept, err := f.svc.Upload(ctx, alice, "kept.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := f.svc.Bind(ctx, alice, []string{kept.ID}, attachments.Ref{Type: "message", ID: "msg_keep"}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	stale, err := f.svc.Upload(ctx, alice, "stale.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := f.svc.DeleteUnreferenced(ctx, manager("us_admin"), "  ", attachments.Filter{}); !errors.Is(err, attachments.ErrReasonRequired) {
		t.Fatalf("missing reason err = %v", err)
	}
	report, err := f.svc.DeleteUnreferenced(ctx, manager("us_admin"), "集中清理", attachments.Filter{})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if report.Count != 1 || report.ByUploader[alice.ID].Count != 1 || len(report.ByUploader[alice.ID].Names) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := f.svc.Lookup(ctx, tenant, stale.ID); err != nil {
		t.Fatalf("stale tombstone: %v", err)
	}
	if _, err := f.svc.Lookup(ctx, tenant, kept.ID); err != nil {
		t.Fatalf("kept lookup: %v", err)
	}
	f.clock.Advance(25 * time.Hour)
	fresh, err := f.svc.Upload(ctx, alice, "fresh.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("fresh upload: %v", err)
	}
	removed, err := f.svc.CleanupPending(ctx, tenant)
	if err != nil || removed != 0 {
		t.Fatalf("cleanup should not remove the fresh row: %d err=%v", removed, err)
	}
	f.clock.Advance(25 * time.Hour)
	removed, err = f.svc.CleanupPending(ctx, tenant)
	if err != nil || removed != 1 {
		t.Fatalf("cleanup removed=%d err=%v", removed, err)
	}
	got, err := f.svc.Lookup(ctx, tenant, fresh.ID)
	if err != nil || got.Status != attachments.StatusDeleted {
		t.Fatalf("fresh tombstone = %+v err=%v", got, err)
	}
}

func TestReleaseRestartsPendingTTL(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := actor("us_alice")
	attachment, err := f.svc.Upload(ctx, alice, "old.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	ref := attachments.Ref{Type: "message", ID: "msg_old"}
	if err := f.svc.Bind(ctx, alice, []string{attachment.ID}, ref); err != nil {
		t.Fatalf("bind: %v", err)
	}
	f.clock.Advance(48 * time.Hour)
	if err := f.svc.Release(ctx, alice, ref); err != nil {
		t.Fatalf("release: %v", err)
	}
	removed, err := f.svc.CleanupPending(ctx, tenant)
	if err != nil || removed != 0 {
		t.Fatalf("released attachment must get a fresh TTL: removed=%d err=%v", removed, err)
	}
	f.clock.Advance(25 * time.Hour)
	removed, err = f.svc.CleanupPending(ctx, tenant)
	if err != nil || removed != 1 {
		t.Fatalf("released attachment should expire after the new TTL: removed=%d err=%v", removed, err)
	}
}

func TestCrossTenantAndLegacyMigration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other := attachments.Actor{ID: "us_alice", TenantID: "tn_other", DisplayName: "Alice"}
	attachment, err := f.svc.Upload(ctx, actor("us_alice"), "a.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := f.svc.Lookup(ctx, other.TenantID, attachment.ID); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("cross tenant lookup err = %v", err)
	}
	if err := f.svc.Bind(ctx, other, []string{attachment.ID}, attachments.Ref{Type: "message", ID: "m"}); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("cross tenant bind err = %v", err)
	}

	// Create a legacy table and verify the copy is idempotent. | 创建旧表并验证复制幂等。
	if err := f.db.Exec(`CREATE TABLE yd_msg_attachments (
		created_at datetime, message_id varchar(64), id varchar(64) primary key,
		tenant_id varchar(64), uploader_id varchar(64), original_name varchar(255),
		stored_key varchar(255), content_type varchar(128), sha256 varchar(64),
		status varchar(16), size bigint)`).Error; err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	msgID := "msg_legacy"
	if err := f.db.Exec(`INSERT INTO yd_msg_attachments
		(created_at, message_id, id, tenant_id, uploader_id, original_name, stored_key, content_type, sha256, status, size)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), msgID, "att_legacy", tenant, "us_alice",
		"legacy.png", "aaaaaaaaaaaaaaaa/bbbbbbbbbbbbbbbb/2026/10/26/att_legacy", "image/png", "abc", "attached", 12).Error; err != nil {
		t.Fatalf("insert legacy: %v", err)
	}
	inserted, err := attachmentsgorm.MigrateLegacy(ctx, f.db)
	if err != nil || inserted != 1 {
		t.Fatalf("legacy migrate inserted=%d err=%v", inserted, err)
	}
	if inserted, err := attachmentsgorm.MigrateLegacy(ctx, f.db); err != nil || inserted != 0 {
		t.Fatalf("second legacy migrate inserted=%d err=%v", inserted, err)
	}
	dropped, err := attachmentsgorm.DropLegacy(ctx, f.db)
	if err != nil || !dropped {
		t.Fatalf("drop legacy dropped=%v err=%v", dropped, err)
	}
	var legacyTables int64
	if err := f.db.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='yd_msg_attachments'`).Scan(&legacyTables).Error; err != nil {
		t.Fatalf("count legacy table: %v", err)
	}
	if legacyTables != 0 {
		t.Fatal("yd_msg_attachments was not dropped after migration")
	}
	if dropped, err := attachmentsgorm.DropLegacy(ctx, f.db); err != nil || dropped {
		t.Fatalf("second drop legacy dropped=%v err=%v", dropped, err)
	}
	legacyAttachment, err := f.svc.Lookup(ctx, tenant, "att_legacy")
	if err != nil || legacyAttachment.Status != attachments.StatusReferenced {
		t.Fatalf("legacy attachment = %+v err=%v", legacyAttachment, err)
	}
	byRef, err := f.svc.ListByRef(ctx, tenant, attachments.Ref{Type: attachments.RefTypeMessage, ID: msgID})
	if err != nil || len(byRef) != 1 || byRef[0].ID != "att_legacy" {
		t.Fatalf("legacy refs = %+v err=%v", byRef, err)
	}
}

// TestAttachmentIndexes proves the tenant-first indexes the queries rely on were actually created. |
// TestAttachmentIndexes 证明查询依赖的租户优先索引确实被创建。
func TestAttachmentIndexes(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{
		"uidx_attachment_tenant_id",
		"idx_attachment_uploader",
		"idx_attachment_tenant_created",
		"idx_attachment_status_created",
		"uidx_attachment_ref",
		"idx_attachment_ref_attachment",
		"idx_attachment_ref_lookup",
	} {
		if !f.db.Migrator().HasIndex(&attachments.Attachment{}, name) &&
			!f.db.Migrator().HasIndex(&attachments.RefBinding{}, name) {
			t.Errorf("missing attachment index %q", name)
		}
	}
}

// TestSearchTreatsPercentAndUnderscoreLiterally proves the LIKE escape is portable: a file named
// "100%_done.txt" is found by its literal characters and not by a wildcard expansion. |
// TestSearchTreatsPercentAndUnderscoreLiterally 证明 LIKE 转义可移植：名为 "100%_done.txt" 的文件按字面
// 字符命中，不会被当作通配符。
func TestSearchTreatsPercentAndUnderscoreLiterally(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := actor("us_search")
	if _, err := f.svc.Upload(ctx, owner, "100%_done.txt", 4, bytes.NewReader([]byte("data"))); err != nil {
		t.Fatalf("upload: %v", err)
	}
	page, err := f.svc.ListOwn(ctx, owner, attachments.Filter{Query: "100%_"})
	if err != nil {
		t.Fatalf("literal search: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("literal search total = %d, want 1", page.Total)
	}
	page, err = f.svc.ListOwn(ctx, owner, attachments.Filter{Query: "100%x"})
	if err != nil {
		t.Fatalf("wildcard search: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("wildcard search total = %d, want 0", page.Total)
	}
}

// TestSVGIsNeverInline proves the inline preview guard does not trust an operator's allow-list
// addition: SVG is executable XML and is always served as a download. |
// TestSVGIsNeverInline 证明内联预览保护不采信运维加入白名单：SVG 是可执行 XML，永远以下载方式提供。
func TestSVGIsNeverInline(t *testing.T) {
	svg := attachments.Attachment{ContentType: "image/svg+xml", Status: attachments.StatusPending, OriginalName: "x.svg"}
	if attachments.IsImage(svg) {
		t.Fatal("SVG was classified as an inline image")
	}
	if got := attachments.ContentDisposition(svg, true); !strings.HasPrefix(got, "attachment") {
		t.Fatalf("SVG disposition = %q, want attachment", got)
	}
}

// TestListFiltersCoverTheManagementSurface proves the uploader, reference-state, status and date
// filters the back office exposes all reach the store. | TestListFiltersCoverTheManagementSurface 证明
// 后台暴露的上传者、引用状态、状态与日期筛选都真正落到存储层。
func TestListFiltersCoverTheManagementSurface(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice, bob, admin := actor("us_alice"), actor("us_bob"), manager("us_admin")
	first, err := f.svc.Upload(ctx, alice, "a.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("alice upload: %v", err)
	}
	if _, err := f.svc.Upload(ctx, bob, "b.png", 12, bytes.NewReader(pngBytes(12))); err != nil {
		t.Fatalf("bob upload: %v", err)
	}
	if err := f.svc.Bind(ctx, alice, []string{first.ID}, attachments.Ref{Type: "message", ID: "msg_1"}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	byUploader, err := f.svc.ListAll(ctx, admin, attachments.Filter{UploaderID: alice.ID})
	if err != nil || byUploader.Total != 1 || byUploader.Items[0].ID != first.ID {
		t.Fatalf("uploader filter = %+v err=%v", byUploader, err)
	}
	referenced, err := f.svc.ListAll(ctx, admin, attachments.Filter{Referenced: testBool(true)})
	if err != nil || referenced.Total != 1 || referenced.Items[0].ID != first.ID {
		t.Fatalf("referenced filter = %+v err=%v", referenced, err)
	}
	unreferenced, err := f.svc.ListAll(ctx, admin, attachments.Filter{Referenced: testBool(false)})
	if err != nil || unreferenced.Total != 1 || unreferenced.Items[0].UploaderID != bob.ID {
		t.Fatalf("unreferenced filter = %+v err=%v", unreferenced, err)
	}
	pending, err := f.svc.ListAll(ctx, admin, attachments.Filter{Status: attachments.StatusPending})
	if err != nil || pending.Total != 1 || pending.Items[0].UploaderID != bob.ID {
		t.Fatalf("status filter = %+v err=%v", pending, err)
	}
	tomorrow := f.clock.Now().Add(24 * time.Hour)
	fromTomorrow, err := f.svc.ListAll(ctx, admin, attachments.Filter{From: &tomorrow})
	if err != nil || fromTomorrow.Total != 0 {
		t.Fatalf("date filter = %+v err=%v", fromTomorrow, err)
	}
}

// testBool returns a pointer to value. | testBool 返回 value 的指针。
func testBool(value bool) *bool { return &value }
