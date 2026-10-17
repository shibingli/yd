package gormstore

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/shibingli/yd/security/auth"
)

// TestUpsertRoleRewritesThePermissionSet walks the whole lifetime of a role's
// permissions: grant, shrink, drop everything. | TestUpsertRoleRewritesThePermissionSet 走遍角色权限的完整生命周期：
// 授予、收缩、清空。
//
// The role row and its permission rows live in two tables, so this is the most
// error-prone corner of the adapter: a stale row here would silently grant a
// capability somebody already revoked. | 角色行与权限行分居两张表，因此这是适配器最容易出错的角落：
// 一行残留就会静默授予某个已被撤下的能力。
func TestUpsertRoleRewritesThePermissionSet(t *testing.T) {
	ctx := context.Background()
	store, db, _ := capturedStore(t)
	if err := store.CreateTenant(ctx, &auth.Tenant{ID: "tn_a", Slug: "alpha", Name: "Alpha", Status: auth.StatusActive}); err != nil {
		t.Fatalf("CreateTenant() = %v, want nil | CreateTenant() = %v，期望 nil", err, err)
	}
	grants := [][]string{
		{"user:create", "user:list", "user:read"},
		{"user:list", "user:read"},
		{"user:read"},
		nil,
	}
	for i, perms := range grants {
		if err := store.UpsertRole(ctx, &auth.Role{ID: "rl_a", TenantID: "tn_a", Name: "admin", Permissions: perms}); err != nil {
			t.Fatalf("UpsertRole(%d) = %v, want nil | UpsertRole(%d) = %v，期望 nil", i, err, i, err)
		}
		if got := countPermissions(t, db, "tn_a", "rl_a"); got != int64(len(perms)) {
			t.Errorf("after upsert %d the tenant has %d permission rows, want %d | 第 %d 次 upsert 后权限行数为 %d，期望 %d", i, got, len(perms), i, got, len(perms))
		}
		got, err := store.GetRole(ctx, "tn_a", "rl_a")
		if err != nil {
			t.Fatalf("GetRole() = %v, want nil | GetRole() = %v，期望 nil", err, err)
		}
		assertStringsEqual(t, got.Permissions, perms, "role Permissions")
	}
}

// TestRepeatedGrantIsIdempotent proves the unique index turns a repeated grant into
// a no-op instead of a duplicate row. | TestRepeatedGrantIsIdempotent 证明唯一索引把重复授权变成无操作，
// 而不是多出一行。
func TestRepeatedGrantIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, db, _ := capturedStore(t)
	seedTwoTenants(t, ctx, store)
	perms := []string{"user:list", "user:read", "role:write"}
	for i := 0; i < 3; i++ {
		if err := store.UpsertRole(ctx, &auth.Role{ID: "rl_a", TenantID: "tn_a", Name: "admin", Permissions: perms}); err != nil {
			t.Fatalf("UpsertRole(%d) = %v, want nil | UpsertRole(%d) = %v，期望 nil", i, err, i, err)
		}
	}
	if got := countPermissions(t, db, "tn_a", "rl_a"); got != int64(len(perms)) {
		t.Errorf("permission rows = %d, want %d | 权限行数 = %d，期望 %d", got, len(perms), got, len(perms))
	}
	// A duplicate inside one call is collapsed for the same reason. | 同一次调用里的重复项出于同样的原因被折叠。
	if err := store.UpsertRole(ctx, &auth.Role{ID: "rl_a", TenantID: "tn_a", Name: "admin", Permissions: []string{"user:read", "user:read"}}); err != nil {
		t.Fatalf("UpsertRole(duplicate) = %v, want nil | UpsertRole(重复项) = %v，期望 nil", err, err)
	}
	if got := countPermissions(t, db, "tn_a", "rl_a"); got != 1 {
		t.Errorf("permission rows after a duplicated grant = %d, want 1 | 重复授权后的权限行数 = %d，期望 1", got, got)
	}
	// The unique index refuses a hand-written duplicate as well. | 唯一索引同样拒绝手写的重复行。
	dup := rolePermissionRow{TenantID: "tn_a", RoleID: "rl_a", Permission: "user:read"}
	if err := db.Create(&dup).Error; !isDuplicate(err) {
		t.Fatalf("duplicate (tenant_id, role_id, permission) = %[1]v, want a unique violation | 重复的 (tenant_id, role_id, permission) = %[1]v，期望唯一约束冲突", err)
	}
}

// TestPermissionReadsAreSortedAndDeduplicated documents the set semantics of the
// association table, which is a deliberate difference from the ordered list the
// memory store keeps. | TestPermissionReadsAreSortedAndDeduplicated 记录关联表的集合语义，
// 这是它与内存实现保序列表的一处刻意差异。
func TestPermissionReadsAreSortedAndDeduplicated(t *testing.T) {
	ctx := context.Background()
	store, _, _ := capturedStore(t)
	seedTwoTenants(t, ctx, store)
	if err := store.UpsertRole(ctx, &auth.Role{
		ID: "rl_a", TenantID: "tn_a", Name: "admin",
		Permissions: []string{"user:read", "role:write", "user:read", "audit:list"},
	}); err != nil {
		t.Fatalf("UpsertRole() = %v, want nil | UpsertRole() = %v，期望 nil", err, err)
	}
	got, err := store.GetRole(ctx, "tn_a", "rl_a")
	if err != nil {
		t.Fatalf("GetRole() = %v, want nil | GetRole() = %v，期望 nil", err, err)
	}
	assertStringsEqual(t, got.Permissions, []string{"audit:list", "role:write", "user:read"}, "role Permissions")
}

// TestDeleteRoleLeavesNoOrphanPermissions deletes a role and checks both tables. | TestDeleteRoleLeavesNoOrphanPermissions 删除角色并检查两张表。
func TestDeleteRoleLeavesNoOrphanPermissions(t *testing.T) {
	ctx := context.Background()
	store, db, _ := capturedStore(t)
	seedTwoTenants(t, ctx, store)
	if got := countPermissions(t, db, "tn_a", "rl_a"); got == 0 {
		t.Fatalf("the fixture granted no permission, nothing to clean | 夹具没有授予任何权限，无从清理")
	}
	if err := store.DeleteRole(ctx, "tn_a", "rl_a"); err != nil {
		t.Fatalf("DeleteRole() = %v, want nil | DeleteRole() = %v，期望 nil", err, err)
	}
	if got := countPermissions(t, db, "tn_a", "rl_a"); got != 0 {
		t.Errorf("orphan permission rows after delete = %d, want 0 | 删除后的孤儿权限行数 = %d，期望 0", got, got)
	}
	var roles int64
	if err := db.Model(&roleRow{}).Where("tenant_id = ?", "tn_a").Count(&roles).Error; err != nil {
		t.Fatalf("count roles = %v, want nil | 统计角色 = %v，期望 nil", err, err)
	}
	if roles != 0 {
		t.Errorf("role rows after delete = %d, want 0 | 删除后的角色行数 = %d，期望 0", roles, roles)
	}
	// Re-creating the same name is possible right away, which proves the name was
	// released together with the row. | 立刻可以重建同名角色，证明名字是随行一起释放的。
	if err := store.UpsertRole(ctx, &auth.Role{ID: "rl_new", TenantID: "tn_a", Name: "admin", Permissions: []string{"user:read"}}); err != nil {
		t.Fatalf("UpsertRole() after delete = %v, want nil | 删除后的 UpsertRole() = %v，期望 nil", err, err)
	}
}

// TestNameCollisionRewritesThePermissionsOfTheStoredID is the transactional
// consistency case: the caller asked for a new id, the name resolved to the stored
// id, and the permission set belongs to the stored id only — never to both. | TestNameCollisionRewritesThePermissionsOfTheStoredID
// 是事务一致性用例：调用方给的是新 ID，名称解析到了库中 ID，而权限集合只属于库中 ID —— 绝不属于两者。
func TestNameCollisionRewritesThePermissionsOfTheStoredID(t *testing.T) {
	ctx := context.Background()
	store, db, _ := capturedStore(t)
	seedTwoTenants(t, ctx, store)
	if err := store.UpsertRole(ctx, &auth.Role{
		ID: "rl_b", TenantID: "tn_a", Name: "admin",
		Permissions: []string{"audit:list", "user:list"},
	}); err != nil {
		t.Fatalf("UpsertRole() = %v, want nil | UpsertRole() = %v，期望 nil", err, err)
	}
	var roles int64
	if err := db.Model(&roleRow{}).Where("tenant_id = ?", "tn_a").Count(&roles).Error; err != nil {
		t.Fatalf("count roles = %v, want nil | 统计角色 = %v，期望 nil", err, err)
	}
	if roles != 1 {
		t.Errorf("role rows = %d, want 1 | 角色行数 = %d，期望 1", roles, roles)
	}
	if got := countPermissions(t, db, "tn_a", "rl_b"); got != 0 {
		t.Errorf("permission rows under the discarded id = %d, want 0 | 被丢弃 ID 下的权限行数 = %d，期望 0", got, got)
	}
	if got := countPermissions(t, db, "tn_a", "rl_a"); got != 2 {
		t.Errorf("permission rows under the stored id = %d, want 2 | 库中 ID 下的权限行数 = %d，期望 2", got, got)
	}
	got, err := store.GetRole(ctx, "tn_a", "rl_a")
	if err != nil {
		t.Fatalf("GetRole() = %v, want nil | GetRole() = %v，期望 nil", err, err)
	}
	assertStringsEqual(t, got.Permissions, []string{"audit:list", "user:list"}, "role Permissions")
}

// TestUpsertRoleIntoAnUnknownTenantWritesNothing checks that the transaction is all
// or nothing: a refused role must not leave a permission row behind. | TestUpsertRoleIntoAnUnknownTenantWritesNothing
// 检查事务的全有全无：被拒绝的角色不得留下任何权限行。
func TestUpsertRoleIntoAnUnknownTenantWritesNothing(t *testing.T) {
	ctx := context.Background()
	store, db, _ := capturedStore(t)
	if err := store.UpsertRole(ctx, &auth.Role{ID: "rl_a", TenantID: "tn_missing", Name: "admin", Permissions: []string{"user:read"}}); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("UpsertRole(unknown tenant) = %v, want ErrNotFound | UpsertRole(未知租户) = %v，期望 ErrNotFound", err, err)
	}
	var rows int64
	if err := db.Model(&rolePermissionRow{}).Count(&rows).Error; err != nil {
		t.Fatalf("count permissions = %v, want nil | 统计权限 = %v，期望 nil", err, err)
	}
	if rows != 0 {
		t.Errorf("permission rows after a refused upsert = %d, want 0 | 被拒绝的 upsert 之后的权限行数 = %d，期望 0", rows, rows)
	}
	var roles int64
	if err := db.Model(&roleRow{}).Count(&roles).Error; err != nil {
		t.Fatalf("count roles = %v, want nil | 统计角色 = %v，期望 nil", err, err)
	}
	if roles != 0 {
		t.Errorf("role rows after a refused upsert = %d, want 0 | 被拒绝的 upsert 之后的角色行数 = %d，期望 0", roles, roles)
	}
}

// countPermissions returns how many association rows exist for one role. | countPermissions 返回某个角色的关联行数。
func countPermissions(t *testing.T, db *gorm.DB, tenantID, roleID string) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&rolePermissionRow{}).
		Where("tenant_id = ? AND role_id = ?", tenantID, roleID).
		Count(&count).Error; err != nil {
		t.Fatalf("count permissions = %v, want nil | 统计权限 = %v，期望 nil", err, err)
	}
	return count
}
