package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// joinPermissions renders a stored permission list as a comma-separated string. | joinPermissions 把库中权限列表渲染为逗号分隔字符串。
func joinPermissions(perms []string) string { return strings.Join(perms, ",") }

// failingStore wraps a Store and can make ListRoles fail on demand, which is the
// only way to prove that an unreadable role table grants nothing. | failingStore 包装 Store，
// 并可按需让 ListRoles 失败，这是证明「读不到角色表就不授予任何权限」的唯一手段。
type failingStore struct {
	Store
	unreadable bool
}

// ListRoles fails while unreadable is set. | unreadable 置位期间 ListRoles 失败。
func (s *failingStore) ListRoles(ctx context.Context, tenantID string) ([]*Role, error) {
	if s.unreadable {
		return nil, ErrNotFound
	}
	return s.Store.ListRoles(ctx, tenantID)
}

// TestRBACPermissionChecks 覆盖角色到权限的映射、并集、判定与失败关闭。
func TestRBACPermissionChecks(t *testing.T) {
	f := newFixture(t)

	if got := f.svc.RolesOf(f.admin); len(got) != 1 || got[0] != "admin" {
		t.Fatalf("RolesOf(admin) want [admin], got %v | RolesOf(admin) 期望 [admin]，实际 %v", got, got)
	}
	if got := f.svc.RolesOf(nil); got != nil {
		t.Fatalf("RolesOf(nil) want nil, got %v | RolesOf(nil) 期望 nil，实际 %v", got, got)
	}

	adminPerms := f.svc.PermissionsOf(f.admin)
	if len(adminPerms) != 3 {
		t.Fatalf("PermissionsOf(admin) want 3 permissions, got %v | PermissionsOf(admin) 期望 3 个权限，实际 %v", adminPerms, adminPerms)
	}
	want := []Permission{"role:write", "user:read", "user:write"}
	for i, p := range want {
		if adminPerms[i] != p {
			t.Fatalf("PermissionsOf(admin) want %v, got %v | PermissionsOf(admin) 期望 %v，实际 %v", want, adminPerms, want, adminPerms)
		}
	}

	memberPerms := f.svc.PermissionsOf(f.member)
	if len(memberPerms) != 1 || memberPerms[0] != "user:read" {
		t.Fatalf("PermissionsOf(member) want [user:read], got %v | PermissionsOf(member) 期望 [user:read]，实际 %v", memberPerms, memberPerms)
	}

	cases := []struct {
		name    string
		user    *User
		perm    Permission
		granted bool
	}{
		{"admin reads", f.admin, "user:read", true},
		{"admin writes", f.admin, "user:write", true},
		{"admin misses billing", f.admin, "billing:read", false},
		{"member reads", f.member, "user:read", true},
		{"member cannot write", f.member, "user:write", false},
		{"member cannot write roles", f.member, "role:write", false},
		{"empty permission", f.admin, "", false},
		{"nil user", nil, "user:read", false},
		{"unknown role name", &User{ID: "us_x", TenantID: f.tenantA.ID, Status: StatusActive, Roles: []string{"ghost"}}, "user:read", false},
		{"disabled user", &User{ID: f.admin.ID, TenantID: f.tenantA.ID, Status: StatusDisabled, Roles: []string{"admin"}}, "user:read", false},
		{"user without roles", &User{ID: "us_y", TenantID: f.tenantA.ID, Status: StatusActive}, "user:read", false},
	}
	for _, tc := range cases {
		if got := f.svc.HasPermission(tc.user, tc.perm); got != tc.granted {
			t.Errorf("%s: HasPermission = %v, want %v | %s：HasPermission = %v，期望 %v", tc.name, got, tc.granted, tc.name, got, tc.granted)
		}
	}
	if got := f.svc.PermissionsOf(nil); got != nil {
		t.Fatalf("PermissionsOf(nil) want nil, got %v | PermissionsOf(nil) 期望 nil，实际 %v", got, got)
	}

	// The union is computed over the roles of one user and is deduplicated. | 并集按单个用户的角色计算，并已去重。
	both, err := f.svc.UpsertRole(testCtx, f.tenantA.ID, &Role{Name: "auditor", Permissions: []string{"user:read", "audit:read"}})
	requireNoError(t, err, "UpsertRole(auditor)")
	requireNoError(t, f.svc.SetUserRoles(testCtx, f.tenantA.ID, f.member.ID, []string{"member", "auditor"}), "SetUserRoles")
	refreshed, err := f.svc.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	if got := f.svc.PermissionsOf(refreshed); len(got) != 2 || got[0] != "audit:read" || got[1] != "user:read" {
		t.Fatalf("union want [audit:read user:read], got %v | 并集期望 [audit:read user:read]，实际 %v", got, got)
	}
	if got := f.svc.RolesOf(refreshed); len(got) != 2 || got[0] != "auditor" || got[1] != "member" {
		t.Fatalf("RolesOf want [auditor member], got %v | RolesOf 期望 [auditor member]，实际 %v", got, got)
	}
	if !f.svc.HasPermission(refreshed, "audit:read") {
		t.Fatal("a permission of the second role is missing | 第二个角色的权限缺失")
	}
	if both.ID == "" || both.TenantID != f.tenantA.ID {
		t.Fatalf("UpsertRole returned an unfiled role: %+v | UpsertRole 返回了未归属的角色：%+v", both, both)
	}

	// Clearing the roles removes every permission of that user. | 清空角色即移除该用户的全部权限。
	requireNoError(t, f.svc.SetUserRoles(testCtx, f.tenantA.ID, f.member.ID, nil), "SetUserRoles(nil)")
	cleared, err := f.svc.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	if perms := f.svc.PermissionsOf(cleared); len(perms) != 0 {
		t.Fatalf("a user without roles kept permissions: %v | 没有角色的用户仍保留权限：%v", perms, perms)
	}
}

// TestRoleLifecycle 覆盖角色的创建、更新、列表与删除。
func TestRoleLifecycle(t *testing.T) {
	f := newFixture(t)
	created, err := f.svc.UpsertRole(testCtx, f.tenantA.ID, &Role{Name: "editor", Description: "Editor", Permissions: []string{"doc:write"}})
	requireNoError(t, err, "UpsertRole(create)")
	if created.TenantID != f.tenantA.ID {
		t.Fatalf("TenantID want %q, got %q | TenantID 期望 %q，实际 %q", f.tenantA.ID, created.TenantID, f.tenantA.ID, created.TenantID)
	}
	if len(created.ID) == 0 || created.ID[:3] != roleIDPrefix {
		t.Fatalf("role id %q does not carry the %q prefix | 角色 ID %q 未带 %q 前缀", created.ID, roleIDPrefix, created.ID, roleIDPrefix)
	}

	// Upserting the same name updates in place and keeps the id. | 同名 Upsert 就地更新并保留 ID。
	updated, err := f.svc.UpsertRole(testCtx, f.tenantA.ID, &Role{Name: "editor", Permissions: []string{"doc:read", "doc:write"}})
	requireNoError(t, err, "UpsertRole(update)")
	if updated.ID != created.ID {
		t.Fatalf("role id changed on update: %q -> %q | 更新改变了角色 ID：%q -> %q", created.ID, updated.ID, created.ID, updated.ID)
	}
	roles, err := f.svc.ListRoles(testCtx, f.tenantA.ID)
	requireNoError(t, err, "ListRoles")
	if len(roles) != 3 {
		t.Fatalf("ListRoles(A) want 3 roles, got %d | ListRoles(A) 期望 3 个角色，实际 %d", len(roles), len(roles))
	}
	for i := 1; i < len(roles); i++ {
		if roles[i-1].Name > roles[i].Name {
			t.Fatalf("ListRoles is not ordered by name | ListRoles 未按名称排序")
		}
	}

	// The permission cache follows the mutation without a ListRoles round trip. | 权限缓存随变更更新，无需再走一次 ListRoles。
	assigned := mustUser(t, f.svc, f.tenantA.ID, "editor-user", "editor")
	if !f.svc.HasPermission(assigned, "doc:read") {
		t.Fatal("the fresh role is not visible to the permission check | 新角色对权限判定不可见")
	}

	// Deleting the role strips it from every user of the tenant. | 删除角色会从该租户的所有用户上摘掉它。
	requireNoError(t, f.svc.DeleteRole(testCtx, f.tenantA.ID, updated.ID), "DeleteRole")
	if _, err := f.store.GetRole(testCtx, f.tenantA.ID, updated.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the deleted role is still stored, got %v | 已删除的角色仍在库中，实际 %v", err, err)
	}
	after, err := f.svc.GetUser(testCtx, f.tenantA.ID, assigned.ID)
	requireNoError(t, err, "GetUser")
	if len(after.Roles) != 0 {
		t.Fatalf("the deleted role name is still on the user: %v | 已删除的角色名仍留在用户上：%v", after.Roles, after.Roles)
	}
	if f.svc.HasPermission(after, "doc:read") {
		t.Fatal("a deleted role still grants a permission | 已删除的角色仍在授予权限")
	}
	if err := f.svc.DeleteRole(testCtx, f.tenantA.ID, updated.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting twice want ErrNotFound, got %v | 重复删除期望 ErrNotFound，实际 %v", err, err)
	}
	if err := f.svc.DeleteRole(testCtx, f.tenantA.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty role id want ErrNotFound, got %v | 空角色 ID 期望 ErrNotFound，实际 %v", err, err)
	}

	// Deleting one tenant's role never touches the other tenant's users. | 删除某个租户的角色绝不影响另一个租户的用户。
	requireNoError(t, f.svc.DeleteRole(testCtx, f.tenantB.ID, f.bMemberRole.ID), "DeleteRole(B)")
	stranger, err := f.svc.GetUser(testCtx, f.tenantB.ID, f.stranger.ID)
	requireNoError(t, err, "GetUser(stranger)")
	if len(stranger.Roles) != 0 {
		t.Fatalf("tenant B's user kept a deleted role: %v | 租户 B 的用户仍保留已删除的角色：%v", stranger.Roles, stranger.Roles)
	}
	if len(f.svc.RolesOf(f.member)) != 1 {
		t.Fatal("deleting a role of tenant B touched a user of tenant A | 删除租户 B 的角色却改动了租户 A 的用户")
	}
}

// TestRoleUpsertAndAssignmentValidation 覆盖角色与角色分配的非法输入。
func TestRoleUpsertAndAssignmentValidation(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.UpsertRole(testCtx, f.tenantA.ID, &Role{Name: "  "}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty role name want ErrInvalidInput, got %v | 空角色名期望 ErrInvalidInput，实际 %v", err, err)
	}
	if _, err := f.svc.UpsertRole(testCtx, f.tenantA.ID, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("nil role want ErrInvalidInput, got %v | 空角色期望 ErrInvalidInput，实际 %v", err, err)
	}
	if _, err := f.svc.UpsertRole(testCtx, "", &Role{Name: "x"}); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("empty tenant want ErrTenantRequired, got %v | 空租户期望 ErrTenantRequired，实际 %v", err, err)
	}
	if _, err := f.svc.UpsertRole(testCtx, "tn_missing", &Role{Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown tenant want ErrNotFound, got %v | 未知租户期望 ErrNotFound，实际 %v", err, err)
	}
	if _, err := f.svc.UpsertRole(testCtx, f.tenantA.ID, &Role{Name: "x", TenantID: f.tenantB.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign tenant want ErrNotFound, got %v | 外部租户期望 ErrNotFound，实际 %v", err, err)
	}
	if _, err := f.svc.ListRoles(testCtx, ""); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("ListRoles without tenant want ErrTenantRequired, got %v | 未指定租户的 ListRoles 期望 ErrTenantRequired，实际 %v", err, err)
	}
	if _, err := f.svc.ListRoles(testCtx, "tn_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListRoles of unknown tenant want ErrNotFound, got %v | 未知租户的 ListRoles 期望 ErrNotFound，实际 %v", err, err)
	}

	// A refusal must leave the stored role list untouched. | 拒绝之后库中角色列表必须原样不动。
	before, err := f.svc.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	if err := f.svc.SetUserRoles(testCtx, f.tenantA.ID, f.member.ID, []string{"member", "ghost"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown role want ErrNotFound, got %v | 未知角色期望 ErrNotFound，实际 %v", err, err)
	}
	mustRole(t, f.svc, f.tenantB.ID, "b-only", "b:read")
	if err := f.svc.SetUserRoles(testCtx, f.tenantA.ID, f.member.ID, []string{"b-only"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("role of another tenant want ErrNotFound, got %v | 其它租户的角色期望 ErrNotFound，实际 %v", err, err)
	}
	if err := f.svc.SetUserRoles(testCtx, f.tenantA.ID, "us_missing", []string{"member"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user want ErrNotFound, got %v | 未知用户期望 ErrNotFound，实际 %v", err, err)
	}
	if err := f.svc.SetUserRoles(testCtx, "", f.member.ID, nil); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("empty tenant want ErrTenantRequired, got %v | 空租户期望 ErrTenantRequired，实际 %v", err, err)
	}
	after, err := f.svc.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	if len(after.Roles) != len(before.Roles) || after.Roles[0] != before.Roles[0] {
		t.Fatalf("a refused SetUserRoles changed the roles: %v -> %v | 被拒绝的 SetUserRoles 改动了角色：%v -> %v",
			before.Roles, after.Roles, before.Roles, after.Roles)
	}
}

// TestRolePermissionsAreNormalised 校验权限列表的去空白、去重与排序。
func TestRolePermissionsAreNormalised(t *testing.T) {
	f := newFixture(t)
	role, err := f.svc.UpsertRole(testCtx, f.tenantA.ID, &Role{
		Name:        "  spaced  ",
		Description: "  note  ",
		Permissions: []string{" user:read ", "user:read", "", "   ", "b:read", "a:write"},
	})
	requireNoError(t, err, "UpsertRole")
	if role.Name != "spaced" || role.Description != "note" {
		t.Fatalf("role text was not trimmed: %+v | 角色文本未去除首尾空白：%+v", role, role)
	}
	want := "a:write,b:read,user:read"
	if got := joinPermissions(role.Permissions); got != want {
		t.Fatalf("permissions want %q, got %q | 权限期望 %q，实际 %q", want, got, want, got)
	}
	stored, err := f.store.GetRole(testCtx, f.tenantA.ID, role.ID)
	requireNoError(t, err, "GetRole")
	if got := joinPermissions(stored.Permissions); got != want {
		t.Fatalf("stored permissions want %q, got %q | 库中权限期望 %q，实际 %q", want, got, want, got)
	}
	// The cache stores the normalised form too. | 缓存中保存的同样是归一化后的形式。
	assigned := mustUser(t, f.svc, f.tenantA.ID, "spaced-user", "spaced")
	if !f.svc.HasPermission(assigned, "a:write") {
		t.Fatal("a trimmed permission is not granted | 去空白后的权限未被授予")
	}
	if perms := f.svc.PermissionsOf(assigned); len(perms) != 3 {
		t.Fatalf("PermissionsOf want 3, got %v | PermissionsOf 期望 3 个，实际 %v", perms, perms)
	}
	if _, err := f.svc.UpsertRole(testCtx, f.tenantA.ID, &Role{Name: "empty-perms", Permissions: []string{"", "  "}}); err != nil {
		t.Fatalf("UpsertRole with empty permissions: %v | 权限全为空的 UpsertRole 失败：%v", err, err)
	}
	nobody := mustUser(t, f.svc, f.tenantA.ID, "nobody-perms", "empty-perms")
	if perms := f.svc.PermissionsOf(nobody); len(perms) != 0 {
		t.Fatalf("a role without permissions granted %v | 没有权限的角色授予了 %v", perms, perms)
	}
}

// TestRoleCacheFailsClosed 校验角色表不可读时一个权限都不授予，且恢复后立即重试。
func TestRoleCacheFailsClosed(t *testing.T) {
	f := newFixture(t)
	flaky := &failingStore{Store: f.store, unreadable: true}
	svc, err := NewService(flaky, Options{MasterKey: []byte(testMasterKey), Iterations: testIterations})
	requireNoError(t, err, "NewService(flaky)")

	if svc.HasPermission(f.member, "user:read") {
		t.Fatal("an unreadable role table must grant nothing | 读不到角色表时不得授予任何权限")
	}
	if perms := svc.PermissionsOf(f.member); len(perms) != 0 {
		t.Fatalf("an unreadable role table yielded %v | 读不到角色表却得到 %v", perms, perms)
	}
	if _, err := svc.ListRoles(testCtx, f.tenantA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListRoles must surface the store error, got %v | ListRoles 必须上报 Store 错误，实际 %v", err, err)
	}

	// A failure is never cached: the next evaluation retries and succeeds. | 失败绝不被缓存：下一次判定会重试并成功。
	flaky.unreadable = false
	if !svc.HasPermission(f.member, "user:read") {
		t.Fatal("the permission check did not recover after the store recovered | Store 恢复后权限判定未恢复")
	}
}

// TestSameServiceSeesStoreSideRoleChanges 校验 ListRoles 会整体刷新缓存。
func TestSameServiceSeesStoreSideRoleChanges(t *testing.T) {
	f := newFixture(t)
	if !f.svc.HasPermission(f.member, "user:read") {
		t.Fatal("the fixture permission is missing | 夹具权限缺失")
	}
	// A role is written behind the Service's back, exactly as another process would. | 绕开 Service 直接写角色，模拟另一个进程的行为。
	requireNoError(t, f.store.UpsertRole(testCtx, &Role{
		ID: "rl_side", TenantID: f.tenantA.ID, Name: "member", Permissions: []string{"side:read"},
	}), "store.UpsertRole")
	// The cached entry still answers with the old permissions until a refresh, which
	// is exactly why every Service-side role mutation invalidates it. | 在刷新之前，缓存仍以旧权限作答，
	// 这正是 Service 侧每次角色变更都要失效缓存的原因。
	if !f.svc.HasPermission(f.member, "user:read") {
		t.Fatal("the cache did not answer with the old permission | 缓存未以旧权限作答")
	}
	if f.svc.HasPermission(f.member, "side:read") {
		t.Fatal("the store-side role leaked into the cache before a refresh | 刷新之前，Store 侧的新角色泄漏进了缓存")
	}
	_, err := f.svc.ListRoles(testCtx, f.tenantA.ID)
	requireNoError(t, err, "ListRoles")
	if !f.svc.HasPermission(f.member, "side:read") {
		t.Fatal("ListRoles did not refresh the permission cache | ListRoles 未刷新权限缓存")
	}
	if f.svc.HasPermission(f.member, "user:read") {
		t.Fatal("the stale permission survived the refresh | 刷新后仍残留过期权限")
	}
}
