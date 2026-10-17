package gormstore

import (
	"bytes"
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/shibingli/yd/data/gormx"
	"github.com/shibingli/yd/security/auth"
)

// TestRoundTripSurvivesReopen is the headline proof of this package: the very
// same file is written through one connection pool, that pool is closed, and a
// brand new pool reads every field back. | TestRoundTripSurvivesReopen 是本包的核心证明：
// 通过一个连接池写入同一个文件，关闭该连接池，再由一个全新的连接池把每个字段读回来。
//
// Nothing is cached between the two halves: the second store shares no memory with
// the first, so a value that comes back is a value that was really stored. | 前后两半之间没有任何缓存：
// 第二个 store 与第一个不共享内存，因此能读回来的值就是真正存进去的值。
func TestRoundTripSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	first, err := assemble(t, path)
	if err != nil {
		t.Fatalf("assemble() = %v, want nil | assemble() = %v，期望 nil", err, err)
	}
	svc := newService(t, first.store)
	tenant, err := svc.CreateTenant(ctx, "demo", "Demo Workspace")
	if err != nil {
		t.Fatalf("CreateTenant() = %v, want nil | CreateTenant() = %v，期望 nil", err, err)
	}
	role, err := svc.UpsertRole(ctx, tenant.ID, &auth.Role{
		Name:        "admin",
		Description: "administrator | 管理员",
		Permissions: []string{"user:create", "user:read"},
	})
	if err != nil {
		t.Fatalf("UpsertRole() = %v, want nil | UpsertRole() = %v，期望 nil", err, err)
	}
	user, err := svc.CreateUser(ctx, tenant.ID, auth.CreateUserInput{
		Username:    "Alice",
		Email:       "alice@example.com",
		DisplayName: "Alice",
		Password:    "P@ssw0rd-Str0ng",
		Roles:       []string{role.Name},
	})
	if err != nil {
		t.Fatalf("CreateUser() = %v, want nil | CreateUser() = %v，期望 nil", err, err)
	}
	secKey, err := svc.SecKey(ctx, user)
	if err != nil {
		t.Fatalf("SecKey() = %v, want nil | SecKey() = %v，期望 nil", err, err)
	}

	// The process "exits" here: the pool is closed and nothing of it is reused. | 进程在此「退出」：连接池关闭，其后不再复用任何东西。
	if err := gormx.Close(first.db); err != nil {
		t.Fatalf("Close() = %v, want nil | Close() = %v，期望 nil", err, err)
	}

	second, err := assemble(t, path)
	if err != nil {
		t.Fatalf("second assemble() = %v, want nil | 第二次 assemble() = %v，期望 nil", err, err)
	}
	svc2 := newService(t, second.store)

	gotTenant, err := svc2.GetTenant(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("GetTenant() after reopen = %v, want nil | 重开后的 GetTenant() = %v，期望 nil", err, err)
	}
	assertTenantEqual(t, gotTenant, tenant)

	gotUser, err := svc2.GetUser(ctx, tenant.ID, user.ID)
	if err != nil {
		t.Fatalf("GetUser() after reopen = %v, want nil | 重开后的 GetUser() = %v，期望 nil", err, err)
	}
	assertUserEqual(t, gotUser, user)

	gotRole, err := second.store.GetRole(ctx, tenant.ID, role.ID)
	if err != nil {
		t.Fatalf("GetRole() after reopen = %v, want nil | 重开后的 GetRole() = %v，期望 nil", err, err)
	}
	assertRoleEqual(t, gotRole, role)

	// The sec_key must survive byte for byte, otherwise every token issued before
	// the restart would be unverifiable. | sec_key 必须逐字节存活，否则重启前签发的每个令牌都无法验签。
	secKey2, err := svc2.SecKey(ctx, gotUser)
	if err != nil {
		t.Fatalf("SecKey() after reopen = %v, want nil | 重开后的 SecKey() = %v，期望 nil", err, err)
	}
	if !bytes.Equal(secKey, secKey2) {
		t.Errorf("sec_key changed across the reopen | sec_key 在重开后发生了变化")
	}

	// Authenticate reruns the whole sign-in path over the reloaded row: the password
	// hash, the status and the role list all have to be intact. | Authenticate 在重新载入的行上重跑整条登录链路：
	// 口令散列、状态与角色列表都必须完好。
	signedIn, err := svc2.Authenticate(ctx, tenant.ID, "alice", "P@ssw0rd-Str0ng")
	if err != nil {
		t.Fatalf("Authenticate() after reopen = %v, want nil | 重开后的 Authenticate() = %v，期望 nil", err, err)
	}
	if signedIn.ID != user.ID {
		t.Errorf("Authenticate() returned user %q, want %q | Authenticate() 返回用户 %q，期望 %q", signedIn.ID, user.ID, signedIn.ID, user.ID)
	}
	if list, err := svc2.ListUsers(ctx, tenant.ID); err != nil || len(list) != 1 {
		t.Errorf("ListUsers() after reopen = %[1]d users, %[2]v; want 1, nil | 重开后的 ListUsers() = %[1]d 个用户，%[2]v；期望 1，nil", len(list), err)
	}
	if list, err := svc2.ListRoles(ctx, tenant.ID); err != nil || len(list) != 1 {
		t.Errorf("ListRoles() after reopen = %[1]d roles, %[2]v; want 1, nil | 重开后的 ListRoles() = %[1]d 个角色，%[2]v；期望 1，nil", len(list), err)
	}
	if list, err := svc2.ListTenants(ctx); err != nil || len(list) != 1 {
		t.Errorf("ListTenants() after reopen = %[1]d tenants, %[2]v; want 1, nil | 重开后的 ListTenants() = %[1]d 个租户，%[2]v；期望 1，nil", len(list), err)
	}
}

// TestSecondReplicaSeesTheSameRows shows what a file-backed store buys over the
// map-backed one: two independent pools over one file agree, so a second replica
// no longer holds a private copy of the world. | TestSecondReplicaSeesTheSameRows 展示落盘存储
// 相对 map 存储的价值：同一个文件上的两个独立连接池看到一致的数据，第二个副本不再各自持有一份世界。
func TestSecondReplicaSeesTheSameRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.db")
	first, err := assemble(t, path)
	if err != nil {
		t.Fatalf("assemble() = %v, want nil | assemble() = %v，期望 nil", err, err)
	}
	second, err := assemble(t, path)
	if err != nil {
		t.Fatalf("second assemble() = %v, want nil | 第二次 assemble() = %v，期望 nil", err, err)
	}
	tenant, err := newService(t, first.store).CreateTenant(ctx, "demo", "Demo Workspace")
	if err != nil {
		t.Fatalf("CreateTenant() = %v, want nil | CreateTenant() = %v，期望 nil", err, err)
	}
	got, err := second.store.GetTenant(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("second replica GetTenant() = %v, want nil | 第二副本 GetTenant() = %v，期望 nil", err, err)
	}
	assertTenantEqual(t, got, tenant)
}

// TestConcurrentAccessIsRaceFree drives the store from several goroutines at once;
// the suite runs with -race, so a shared mutable state inside the adapter would be
// reported here. | TestConcurrentAccessIsRaceFree 由多个 goroutine 同时驱动 store；
// 整套测试以 -race 运行，因此适配器内部若有共享可变状态，会在这里被报出。
func TestConcurrentAccessIsRaceFree(t *testing.T) {
	ctx := context.Background()
	store, _ := openStore(t)
	svc := newService(t, store)
	tenant, err := svc.CreateTenant(ctx, "demo", "Demo Workspace")
	if err != nil {
		t.Fatalf("CreateTenant() = %v, want nil | CreateTenant() = %v，期望 nil", err, err)
	}
	role, err := svc.UpsertRole(ctx, tenant.ID, &auth.Role{Name: "reader", Permissions: []string{"user:read"}})
	if err != nil {
		t.Fatalf("UpsertRole() = %v, want nil | UpsertRole() = %v，期望 nil", err, err)
	}
	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			username := "user" + string(rune('a'+i))
			if _, err := svc.CreateUser(ctx, tenant.ID, auth.CreateUserInput{
				Username: username,
				Password: "P@ssw0rd-Str0ng",
				Roles:    []string{role.Name},
			}); err != nil {
				errs[i] = err
			}
		}(i)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.ListUsers(ctx, tenant.ID); err != nil {
				errs[0] = err
			}
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d = %v, want nil | 协程 %d = %v，期望 nil", i, err, i, err)
		}
	}
	users, err := svc.ListUsers(ctx, tenant.ID)
	if err != nil {
		t.Fatalf("ListUsers() = %v, want nil | ListUsers() = %v，期望 nil", err, err)
	}
	if len(users) != workers {
		t.Errorf("ListUsers() = %d users, want %d | ListUsers() = %d 个用户，期望 %d", len(users), workers, len(users), workers)
	}
}

// assembled is an open database plus the store built over it. | assembled 是已打开的数据库与基于它的 store。
type assembled struct {
	// db is the connection pool. | db 是连接池。
	db *gorm.DB
	// store is the adapter. | store 是适配器。
	store *Store
}

// assemble opens path, migrates it and returns the store. | assemble 打开 path、完成迁移并返回 store。
func assemble(t *testing.T, path string) (*assembled, error) {
	t.Helper()
	db, err := gormx.Open(gormx.Options{
		Driver:   gormx.DriverSQLite,
		DSN:      sqliteDSN(path),
		LogLevel: "silent",
	}, nil)
	if err != nil {
		return nil, err
	}
	if err := Migrate(context.Background(), db); err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = gormx.Close(db) })
	store, err := New(db, Options{QueryTimeout: time.Minute})
	if err != nil {
		return nil, err
	}
	return &assembled{db: db, store: store}, nil
}

// assertTenantEqual compares every field of two tenants. | assertTenantEqual 比较两个租户的每一个字段。
func assertTenantEqual(t *testing.T, got, want *auth.Tenant) {
	t.Helper()
	if got.ID != want.ID || got.Slug != want.Slug || got.Name != want.Name || got.Status != want.Status {
		t.Errorf("tenant = %+v, want %+v | 租户 = %+v，期望 %+v", got, want, got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("tenant CreatedAt = %v, want %v | 租户 CreatedAt = %v，期望 %v", got.CreatedAt, want.CreatedAt, got.CreatedAt, want.CreatedAt)
	}
}

// assertUserEqual compares every field of two users. | assertUserEqual 比较两个用户的每一个字段。
func assertUserEqual(t *testing.T, got, want *auth.User) {
	t.Helper()
	if got.ID != want.ID || got.TenantID != want.TenantID || got.Username != want.Username {
		t.Errorf("user identity = %+v, want %+v | 用户身份 = %+v，期望 %+v", got, want, got, want)
	}
	if got.Email != want.Email || got.DisplayName != want.DisplayName || got.PasswordHash != want.PasswordHash {
		t.Errorf("user fields = %+v, want %+v | 用户字段 = %+v，期望 %+v", got, want, got, want)
	}
	if got.Status != want.Status {
		t.Errorf("user Status = %q, want %q | 用户 Status = %q，期望 %q", got.Status, want.Status, got.Status, want.Status)
	}
	if !bytes.Equal(got.EncryptedSecKey, want.EncryptedSecKey) {
		t.Errorf("user EncryptedSecKey differs | 用户的 EncryptedSecKey 不一致")
	}
	assertStringsEqual(t, got.Roles, want.Roles, "user Roles")
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("user times = %[1]v/%[2]v, want %[3]v/%[4]v | 用户时间 = %[1]v/%[2]v，期望 %[3]v/%[4]v",
			got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt)
	}
}

// assertRoleEqual compares every field of two roles. | assertRoleEqual 比较两个角色的每一个字段。
func assertRoleEqual(t *testing.T, got, want *auth.Role) {
	t.Helper()
	if got.ID != want.ID || got.TenantID != want.TenantID || got.Name != want.Name || got.Description != want.Description {
		t.Errorf("role = %+v, want %+v | 角色 = %+v，期望 %+v", got, want, got, want)
	}
	assertStringsEqual(t, got.Permissions, want.Permissions, "role Permissions")
}

// assertStringsEqual compares two string slices, treating nil and empty as equal. | assertStringsEqual 比较两个字符串切片，nil 与空视为相等。
func assertStringsEqual(t *testing.T, got, want []string, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%[1]s = %[2]v, want %[3]v | %[1]s = %[2]v，期望 %[3]v", label, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%[1]s = %[2]v, want %[3]v | %[1]s = %[2]v，期望 %[3]v", label, got, want)
			return
		}
	}
}
