package auth

import (
	"errors"
	"testing"
)

// TestMembershipAcrossTenants covers the multi-tenant membership contract: one
// global identity, per-tenant roles, a configurable default tenant and a tenant
// switch that refuses a tenant the user does not belong to. | TestMembershipAcrossTenants
// 覆盖多租户成员契约：全局身份、按租户的角色、可配置的默认租户，以及拒绝非成员租户的切换。
func TestMembershipAcrossTenants(t *testing.T) {
	f := newFixture(t)

	tenantC := mustTenant(t, f.svc, "tenant-c", "Tenant C")
	mustRole(t, f.svc, tenantC.ID, "member", "billing:read")

	// The same identity may join another tenant, with roles of that tenant only.
	// | 同一个身份可以加入另一个租户，且只持有该租户的角色。
	requireNoError(t, f.svc.AddUserTenant(testCtx, f.member.ID, tenantC.ID, []string{"member"}), "AddUserTenant")
	switched, err := f.svc.SwitchTenant(testCtx, f.member.ID, tenantC.ID)
	requireNoError(t, err, "SwitchTenant(member, C)")
	if switched.TenantID != tenantC.ID || switched.HomeTenantID != f.tenantA.ID {
		t.Fatalf("switch landed in %q (home %q), want %q (home %q) | 切换落入 %q（身份 %q），期望 %q（身份 %q）",
			switched.TenantID, switched.HomeTenantID, tenantC.ID, f.tenantA.ID,
			switched.TenantID, switched.HomeTenantID, tenantC.ID, f.tenantA.ID)
	}
	if sortedNames(switched.Roles) != "member" {
		t.Fatalf("roles in C = %q, want member | 租户 C 的角色 = %q，期望 member", sortedNames(switched.Roles), sortedNames(switched.Roles))
	}
	if !f.svc.HasPermission(switched, "billing:read") || f.svc.HasPermission(switched, "user:read") {
		t.Fatalf("permissions did not follow the active tenant | 权限没有跟随当前租户")
	}

	// A tenant the user does not belong to is indistinguishable from a missing one.
	// | 用户并不隶属的租户与不存在的租户不可区分。
	if _, err := f.svc.SwitchTenant(testCtx, f.member.ID, f.tenantB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SwitchTenant(non-member) want ErrNotFound, got %v | 切换非成员租户期望 ErrNotFound，实际 %v", err, err)
	}

	// The default tenant is configurable, and a tenant-less sign-in lands there.
	// | 默认租户可配置，无租户登录落入其中。
	requireNoError(t, f.svc.SetDefaultTenant(testCtx, f.member.ID, tenantC.ID), "SetDefaultTenant")
	landed, err := f.svc.Authenticate(testCtx, "", f.member.Username, testPassword)
	requireNoError(t, err, "Authenticate(tenant-less)")
	if landed.TenantID != tenantC.ID {
		t.Fatalf("tenant-less sign-in landed in %q, want %q | 无租户登录落入 %q，期望 %q", landed.TenantID, tenantC.ID, landed.TenantID, tenantC.ID)
	}

	// The default membership cannot be removed; the home membership never can.
	// | 默认成员关系不可移除；身份租户的成员关系永远不可移除。
	if err := f.svc.RemoveUserTenant(testCtx, f.member.ID, tenantC.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("RemoveUserTenant(default) want ErrConflict, got %v | 移除默认成员关系期望 ErrConflict，实际 %v", err, err)
	}
	if err := f.svc.RemoveUserTenant(testCtx, f.member.ID, f.tenantA.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("RemoveUserTenant(home) want ErrConflict, got %v | 移除身份租户期望 ErrConflict，实际 %v", err, err)
	}
	requireNoError(t, f.svc.SetDefaultTenant(testCtx, f.member.ID, f.tenantA.ID), "SetDefaultTenant(home)")
	requireNoError(t, f.svc.RemoveUserTenant(testCtx, f.member.ID, tenantC.ID), "RemoveUserTenant(C)")
	if _, err := f.svc.SwitchTenant(testCtx, f.member.ID, tenantC.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after removal SwitchTenant(C) want ErrNotFound, got %v | 移除后切换 C 期望 ErrNotFound，实际 %v", err, err)
	}
}

// TestUsernameIsGloballyUnique locks the identity rule: a sign-in name can no
// longer be reused by another tenant. | TestUsernameIsGloballyUnique 固定身份规则：
// 登录名不能再被另一个租户复用。
func TestUsernameIsGloballyUnique(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.CreateUser(testCtx, f.tenantB.ID, CreateUserInput{
		Username: f.member.Username,
		Password: testPassword,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username across tenants want ErrConflict, got %v | 跨租户重复登录名期望 ErrConflict，实际 %v", err, err)
	}
}
