package attachments_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/shibingli/yd/attachments"
)

// TestReadRechecksTombstone proves a stale attachment value cannot keep serving bytes after the row
// was tombstoned: Read re-reads the state by tenant and id first. |
// TestReadRechecksTombstone 证明记录被墓碑化之后，陈旧的附件值不再能提供字节：Read 会先按租户与 ID
// 重读状态。
func TestReadRechecksTombstone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := actor("us_alice")
	attachment, err := f.svc.Upload(ctx, alice, "a.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if data, err := f.svc.Read(ctx, attachment); err != nil || len(data) != 12 {
		t.Fatalf("read before delete len=%d err=%v", len(data), err)
	}
	if err := f.svc.DeleteOwn(ctx, alice, attachment.ID); err != nil {
		t.Fatalf("delete own: %v", err)
	}
	if _, err := f.svc.Read(ctx, attachment); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("read tombstone error = %v, want ErrNotFound", err)
	}
}

// TestReadRejectsUnknownTenant keeps the re-read scoped to the caller's tenant. |
// TestReadRejectsUnknownTenant 让重读限定在调用方租户内。
func TestReadRejectsUnknownTenant(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	attachment, err := f.svc.Upload(ctx, actor("us_alice"), "a.png", 12, bytes.NewReader(pngBytes(12)))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	stale := attachment
	stale.TenantID = "tn_other"
	if _, err := f.svc.Read(ctx, stale); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("cross-tenant read error = %v, want ErrNotFound", err)
	}
}
