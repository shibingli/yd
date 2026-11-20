package messaging_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shibingli/yd/messaging"
)

// TestNotifyUserRequiresTenantAlignmentAndActiveRecipient proves a notice cannot be injected into
// another tenant by an ordinary moderator, that the trusted system principal may cross tenants, and
// that the recipient must be an active member of the target tenant. |
// TestNotifyUserRequiresTenantAlignmentAndActiveRecipient 证明普通管理员不能把通知注入其它租户、
// 可信系统主体可以跨租户投递，且收件人必须是目标租户的活跃成员。
func TestNotifyUserRequiresTenantAlignmentAndActiveRecipient(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	foreign := messaging.Actor{ID: "us_admin", TenantID: "tn_other", DisplayName: "Admin", Manage: true}
	if _, err := f.svc.NotifyUser(ctx, tenantID, "us_alice", foreign, "title", "body"); !errors.Is(err, messaging.ErrForbidden) {
		t.Fatalf("cross-tenant moderator error = %v, want ErrForbidden", err)
	}

	system := messaging.Actor{ID: "workflow-system", TenantID: "tn_other", DisplayName: "Workflow", System: true}
	if _, err := f.svc.NotifyUser(ctx, tenantID, "us_alice", system, "title", "body"); err != nil {
		t.Fatalf("trusted system notice: %v", err)
	}

	f.dir.profiles["us_inactive"] = messaging.Profile{ID: "us_inactive", Username: "inactive", Active: false}
	if _, err := f.svc.NotifyUser(ctx, tenantID, "us_inactive", admin(), "title", "body"); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("inactive recipient error = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.NotifyUser(ctx, tenantID, "us_ghost", admin(), "title", "body"); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("missing recipient error = %v, want ErrNotFound", err)
	}
}
