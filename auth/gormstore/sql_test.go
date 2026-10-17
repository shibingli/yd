package gormstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/shibingli/yd/auth"
)

// TestTenantScopedSQLCarriesTenantID captures the statements of every
// tenant-scoped method and asserts that the tenant really reached the database. | TestTenantScopedSQLCarriesTenantID
// 抓取每个带租户方法的语句，断言租户确实抵达了数据库。
//
// Checking the Go source would only show the intent; the captured statement shows
// what the database was asked to do, which is the only thing that protects one
// tenant's rows from another tenant's request. | 检查 Go 源码只能看到意图；抓到的语句才能显示数据库被要求做什么，
// 而只有后者能真正保护一个租户的数据不被另一个租户的请求碰到。
func TestTenantScopedSQLCarriesTenantID(t *testing.T) {
	ctx := context.Background()
	store, _, cap := capturedStore(t)
	seedTwoTenants(t, ctx, store)
	cap.reset()

	calls := []struct {
		name string
		call func() error
	}{
		{"GetUser", func() error { _, err := store.GetUser(ctx, "tn_a", "us_a"); return err }},
		{"GetUserByUsername", func() error { _, err := store.GetUserByUsername(ctx, "tn_a", "alice"); return err }},
		{"ListUsers", func() error { _, err := store.ListUsers(ctx, "tn_a"); return err }},
		{"UpdateUser", func() error {
			return store.UpdateUser(ctx, "tn_a", &auth.User{ID: "us_a", TenantID: "tn_a", Username: "alice", Status: auth.StatusActive})
		}},
		{"UpsertRole", func() error {
			return store.UpsertRole(ctx, &auth.Role{ID: "rl_a", TenantID: "tn_a", Name: "admin", Permissions: []string{"user:read"}})
		}},
		{"GetRole", func() error { _, err := store.GetRole(ctx, "tn_a", "rl_a"); return err }},
		{"ListRoles", func() error { _, err := store.ListRoles(ctx, "tn_a"); return err }},
		{"DeleteRole", func() error { return store.DeleteRole(ctx, "tn_a", "rl_a") }},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			cap.reset()
			if err := c.call(); err != nil {
				t.Fatalf("%s() = %v, want nil | %s() = %v，期望 nil", c.name, err, c.name, err)
			}
			sql := cap.joined()
			if sql == "" {
				t.Fatalf("%s() executed no statement | %s() 没有执行任何语句", c.name, c.name)
			}
			if !strings.Contains(sql, "tenant_id") {
				t.Errorf("%s() never filtered by tenant_id:\n%s | %s() 从未按 tenant_id 过滤：\n%s", c.name, sql, c.name, sql)
			}
			if !mentionsValue(sql, "tn_a") {
				t.Errorf("%s() never passed the tenant value:\n%s | %s() 从未传入租户值：\n%s", c.name, sql, c.name, sql)
			}
			if !strings.Contains(sql, "auth_") {
				t.Errorf("%s() did not touch an auth_ table:\n%s | %s() 未触达 auth_ 表：\n%s", c.name, sql, c.name, sql)
			}
		})
	}
}

// TestCreateStatementsCarryTenantID covers the two inserts, whose tenant column is
// the first index column and therefore the reason cross-replica writes cannot leak
// into another tenant. | TestCreateStatementsCarryTenantID 覆盖两条插入语句：租户列是它们索引的首列，
// 也是跨副本写入不会漏进别的租户的原因。
func TestCreateStatementsCarryTenantID(t *testing.T) {
	ctx := context.Background()
	store, _, cap := capturedStore(t)
	cap.reset()
	if _, err := store.GetTenant(ctx, "tn_a"); err == nil {
		t.Fatalf("GetTenant() = nil, want ErrNotFound | GetTenant() = nil，期望 ErrNotFound")
	}
	if err := store.CreateTenant(ctx, &auth.Tenant{ID: "tn_a", Slug: "Demo", Name: "Demo", Status: auth.StatusActive, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("CreateTenant() = %v, want nil | CreateTenant() = %v，期望 nil", err, err)
	}
	if sql := cap.joined(); !strings.Contains(sql, "insert into") || !strings.Contains(sql, tableTenants) {
		t.Errorf("CreateTenant() did not insert into yd_auth_tenants:\n%s | CreateTenant() 未插入 yd_auth_tenants：\n%s", sql, sql)
	}
	cap.reset()
	if err := store.CreateUser(ctx, &auth.User{ID: "us_a", TenantID: "tn_a", Username: "Alice", Status: auth.StatusActive}); err != nil {
		t.Fatalf("CreateUser() = %v, want nil | CreateUser() = %v，期望 nil", err, err)
	}
	sql := cap.joined()
	if !strings.Contains(sql, "insert into") || !strings.Contains(sql, tableUsers) || !mentionsValue(sql, "tn_a") {
		t.Errorf("CreateUser() did not insert the tenant id:\n%s | CreateUser() 未插入租户 ID：\n%s", sql, sql)
	}
}

// TestCrossTenantAccessIsNotFoundNotForbidden pins the security-relevant half of
// the contract: a foreign entity must be indistinguishable from a missing one. | TestCrossTenantAccessIsNotFoundNotForbidden
// 固定契约中与安全相关的那一半：外部实体必须与不存在的实体完全无法区分。
func TestCrossTenantAccessIsNotFoundNotForbidden(t *testing.T) {
	ctx := context.Background()
	store, _, cap := capturedStore(t)
	seedTwoTenants(t, ctx, store)

	calls := []struct {
		name string
		call func() error
	}{
		{"GetUser", func() error { _, err := store.GetUser(ctx, "tn_b", "us_a"); return err }},
		{"GetUserByUsername", func() error { _, err := store.GetUserByUsername(ctx, "tn_b", "alice"); return err }},
		{"UpdateUser", func() error {
			return store.UpdateUser(ctx, "tn_b", &auth.User{ID: "us_a", TenantID: "tn_b", Username: "alice"})
		}},
		{"GetRole", func() error { _, err := store.GetRole(ctx, "tn_b", "rl_a"); return err }},
		{"DeleteRole", func() error { return store.DeleteRole(ctx, "tn_b", "rl_a") }},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			cap.reset()
			err := c.call()
			if !errors.Is(err, auth.ErrNotFound) {
				t.Fatalf("%s() = %v, want ErrNotFound | %s() = %v，期望 ErrNotFound", c.name, err, c.name, err)
			}
			if errors.Is(err, auth.ErrForbidden) {
				t.Errorf("%s() leaked ErrForbidden | %s() 泄露了 ErrForbidden", c.name, c.name)
			}
			sql := cap.joined()
			if !mentionsValue(sql, "tn_b") {
				t.Errorf("%s() did not scope the query to the requesting tenant:\n%s | %s() 未把查询限定在请求方租户：\n%s", c.name, sql, c.name, sql)
			}
			if mentionsValue(sql, "tn_a") {
				t.Errorf("%s() mentioned the other tenant:\n%s | %s() 提到了另一个租户：\n%s", c.name, sql, c.name, sql)
			}
		})
	}
	// The other tenant's rows are untouched by the whole exchange. | 整轮交互完全没有碰到另一个租户的行。
	if list, err := store.ListUsers(ctx, "tn_a"); err != nil || len(list) != 1 {
		t.Errorf("ListUsers(tn_a) = %d users, %v; want 1, nil | ListUsers(tn_a) = %d 个用户，%v；期望 1，nil", len(list), err, len(list), err)
	}
	if list, err := store.ListRoles(ctx, "tn_a"); err != nil || len(list) != 1 {
		t.Errorf("ListRoles(tn_a) = %d roles, %v; want 1, nil | ListRoles(tn_a) = %d 个角色，%v；期望 1，nil", len(list), err, len(list), err)
	}
}

// TestUniqueIndexesScopeUniquenessToTheTenant proves the composite unique indexes
// do the work, not only the pre-checks: the same id and the same username coexist
// in two tenants and collide inside one. | TestUniqueIndexesScopeUniquenessToTheTenant 证明
// 起作用的是联合唯一索引而不只是预检：同一个 ID 与同一个用户名可以并存于两个租户，而在同一租户内相撞。
func TestUniqueIndexesScopeUniquenessToTheTenant(t *testing.T) {
	ctx := context.Background()
	store, db, _ := capturedStore(t)
	seedTwoTenants(t, ctx, store)

	row := userRow{ID: "us_dup", TenantID: "tn_b", Username: "Alice", UsernameKey: keyOf("Alice"), Status: string(auth.StatusActive)}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("cross-tenant insert of the same id failed: %[1]v | 跨租户插入同一个 ID 失败：%[1]v", err)
	}
	sameID := userRow{ID: "us_dup", TenantID: "tn_a", Username: "Bob", UsernameKey: keyOf("Bob"), Status: string(auth.StatusActive)}
	if err := db.Create(&sameID).Error; !isDuplicate(err) {
		t.Fatalf("duplicate (tenant_id, id) = %[1]v, want a unique violation | 重复的 (tenant_id, id) = %[1]v，期望唯一约束冲突", err)
	}
	sameName := userRow{ID: "us_other", TenantID: "tn_a", Username: "alice", UsernameKey: keyOf("alice"), Status: string(auth.StatusActive)}
	if err := db.Create(&sameName).Error; !isDuplicate(err) {
		t.Fatalf("duplicate (tenant_id, username_key) = %[1]v, want a unique violation | 重复的 (tenant_id, username_key) = %[1]v，期望唯一约束冲突", err)
	}

	role := roleRow{ID: "rl_dup", TenantID: "tn_b", Name: "admin"}
	if err := db.Create(&role).Error; err != nil {
		t.Fatalf("cross-tenant insert of the same role name failed: %[1]v | 跨租户插入同名角色失败：%[1]v", err)
	}
	if err := db.Create(&roleRow{ID: "rl_other", TenantID: "tn_a", Name: "admin"}).Error; !isDuplicate(err) {
		t.Fatalf("duplicate (tenant_id, name) = %[1]v, want a unique violation | 重复的 (tenant_id, name) = %[1]v，期望唯一约束冲突", err)
	}
}

// capturedStore opens a capturing database, migrates it and returns both the store
// and the logger. | capturedStore 打开一个带抓取的数据库、完成迁移，并返回 store 与日志器。
func capturedStore(t *testing.T) (*Store, *gorm.DB, *captureLogger) {
	t.Helper()
	db, cap := capturingDB(t)
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() = %v, want nil | Migrate() = %v，期望 nil", err, err)
	}
	store, err := New(db, Options{QueryTimeout: time.Minute})
	if err != nil {
		t.Fatalf("New() = %v, want nil | New() = %v，期望 nil", err, err)
	}
	return store, db, cap
}

// seedTwoTenants creates two tenants, one user and one role in the first of them. | seedTwoTenants 建立两个租户，并在第一个租户内建一个用户与一个角色。
func seedTwoTenants(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, tenant := range []*auth.Tenant{
		{ID: "tn_a", Slug: "alpha", Name: "Alpha", Status: auth.StatusActive, CreatedAt: now},
		{ID: "tn_b", Slug: "beta", Name: "Beta", Status: auth.StatusActive, CreatedAt: now},
	} {
		if err := store.CreateTenant(ctx, tenant); err != nil {
			t.Fatalf("CreateTenant(%q) = %v, want nil | CreateTenant(%q) = %v，期望 nil", tenant.ID, err, tenant.ID, err)
		}
	}
	if err := store.CreateUser(ctx, &auth.User{
		ID: "us_a", TenantID: "tn_a", Username: "Alice",
		Status: auth.StatusActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateUser() = %v, want nil | CreateUser() = %v，期望 nil", err, err)
	}
	if err := store.UpsertRole(ctx, &auth.Role{ID: "rl_a", TenantID: "tn_a", Name: "admin", Permissions: []string{"user:read"}}); err != nil {
		t.Fatalf("UpsertRole() = %v, want nil | UpsertRole() = %v，期望 nil", err, err)
	}
}
