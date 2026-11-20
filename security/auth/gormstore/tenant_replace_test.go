package gormstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/yd/security/auth"
)

// replaceFixture seeds three tenants, a member role each and one user who belongs to the first two tenants. |
// replaceFixture 播种三个租户、每个租户一个 member 角色，以及一个隶属前两个租户的用户。
func replaceFixture(t *testing.T, store *Store) string {
	t.Helper()

	ctx := context.Background()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"tn_a", "tn_b", "tn_c"} {
		if err := store.CreateTenant(ctx, &auth.Tenant{ID: id, Slug: id, Name: id, Status: auth.StatusActive, CreatedAt: now}); err != nil {
			t.Fatalf("CreateTenant(%s) = %v", id, err)
		}
		if err := store.UpsertRole(ctx, &auth.Role{ID: "rl_" + id, TenantID: id, Name: "member"}); err != nil {
			t.Fatalf("UpsertRole(%s) = %v", id, err)
		}
	}
	user := &auth.User{
		ID:              "usr_replace",
		TenantID:        "tn_a",
		HomeTenantID:    "tn_a",
		DefaultTenantID: "tn_a",
		TenantIDs:       []string{"tn_b"},
		Username:        "replace",
		Status:          auth.StatusActive,
		Roles:           []string{"member"},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser() = %v", err)
	}
	return user.ID
}

// TestTenantGormReplaceIsAtomic asserts the single-transaction replacement, including the preserved role set
// of a kept membership and the moved default tenant. | TestTenantGormReplaceIsAtomic 断言单事务替换：
// 保留已有成员关系的角色集合，并移动默认租户。
func TestTenantGormReplaceIsAtomic(t *testing.T) {
	store, _ := openStore(t)
	ctx := context.Background()
	userID := replaceFixture(t, store)

	if err := store.ReplaceUserTenants(ctx, userID, []auth.TenantMembership{
		{TenantID: "tn_a"},
		{TenantID: "tn_c", Roles: []string{"member"}},
	}, "tn_c"); err != nil {
		t.Fatalf("ReplaceUserTenants() = %v, want nil", err)
	}

	user, err := store.GetUser(ctx, "tn_c", userID)
	if err != nil {
		t.Fatalf("GetUser(tn_c) = %v", err)
	}
	if user.DefaultTenantID != "tn_c" {
		t.Errorf("DefaultTenantID = %q, want tn_c", user.DefaultTenantID)
	}
	if !containsTenant(user.TenantIDs, "tn_a") || !containsTenant(user.TenantIDs, "tn_c") {
		t.Errorf("TenantIDs = %v, want tn_a and tn_c", user.TenantIDs)
	}
	if containsTenant(user.TenantIDs, "tn_b") {
		t.Errorf("TenantIDs = %v, tn_b must have been removed", user.TenantIDs)
	}
	if len(user.Roles) != 1 || user.Roles[0] != "member" {
		t.Errorf("tn_c roles = %v, want [member]", user.Roles)
	}
	home, err := store.GetUser(ctx, "tn_a", userID)
	if err != nil {
		t.Fatalf("GetUser(tn_a) = %v", err)
	}
	if len(home.Roles) != 1 || home.Roles[0] != "member" {
		t.Errorf("tn_a roles = %v, want the stored [member]", home.Roles)
	}
}

// TestTenantGormReplaceValidatesBeforeWriting asserts every rejected replacement leaves the membership set
// untouched. | TestTenantGormReplaceValidatesBeforeWriting 断言每一次被拒绝的替换都不改动成员关系集合。
func TestTenantGormReplaceValidatesBeforeWriting(t *testing.T) {
	store, _ := openStore(t)
	ctx := context.Background()
	userID := replaceFixture(t, store)

	cases := []struct {
		name        string
		memberships []auth.TenantMembership
		defaultID   string
		want        error
	}{
		{
			name:        "removing the home tenant",
			memberships: []auth.TenantMembership{{TenantID: "tn_b"}, {TenantID: "tn_c"}},
			defaultID:   "tn_b",
			want:        auth.ErrConflict,
		},
		{
			name:        "default outside the set",
			memberships: []auth.TenantMembership{{TenantID: "tn_a"}, {TenantID: "tn_c"}},
			defaultID:   "tn_b",
			want:        auth.ErrInvalidInput,
		},
		{
			name:        "unknown tenant after valid ones",
			memberships: []auth.TenantMembership{{TenantID: "tn_a"}, {TenantID: "tn_c", Roles: []string{"member"}}, {TenantID: "tn_missing"}},
			defaultID:   "tn_c",
			want:        auth.ErrNotFound,
		},
		{
			name:        "unknown role",
			memberships: []auth.TenantMembership{{TenantID: "tn_a"}, {TenantID: "tn_c", Roles: []string{"no-such-role"}}},
			defaultID:   "tn_c",
			want:        auth.ErrNotFound,
		},
		{
			name:        "duplicate tenant",
			memberships: []auth.TenantMembership{{TenantID: "tn_a"}, {TenantID: "tn_c"}, {TenantID: "tn_c"}},
			defaultID:   "tn_c",
			want:        auth.ErrInvalidInput,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := store.ReplaceUserTenants(ctx, userID, tc.memberships, tc.defaultID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ReplaceUserTenants() = %v, want %v", err, tc.want)
			}
			assertGormFixtureUnchanged(t, store, userID)
		})
	}
}

// assertGormFixtureUnchanged asserts the fixture still holds exactly tn_a and tn_b with tn_a as the default. |
// assertGormFixtureUnchanged 断言夹具仍然只包含 tn_a 与 tn_b，且默认租户仍是 tn_a。
func assertGormFixtureUnchanged(t *testing.T, store *Store, userID string) {
	t.Helper()

	ctx := context.Background()
	user, err := store.GetUser(ctx, "tn_a", userID)
	if err != nil {
		t.Fatalf("GetUser(tn_a) = %v", err)
	}
	if user.DefaultTenantID != "tn_a" {
		t.Errorf("DefaultTenantID = %q, want tn_a", user.DefaultTenantID)
	}
	if len(user.TenantIDs) != 2 || !containsTenant(user.TenantIDs, "tn_a") || !containsTenant(user.TenantIDs, "tn_b") {
		t.Errorf("TenantIDs = %v, want exactly tn_a and tn_b", user.TenantIDs)
	}
	if _, err := store.GetUser(ctx, "tn_c", userID); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("GetUser(tn_c) = %v, want auth.ErrNotFound", err)
	}
}

// containsTenant reports whether ids contains want. | containsTenant 报告 ids 是否包含 want。
func containsTenant(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// legacyUsersTable creates the pre-global-identity yd_auth_users shape: no unique indexes and no username_key
// column. | legacyUsersTable 创建全局身份模型之前的 yd_auth_users 结构：没有唯一索引，也没有 username_key 列。
const legacyUsersTable = `CREATE TABLE yd_auth_users (
	created_at datetime,
	updated_at datetime,
	id varchar(64) NOT NULL,
	tenant_id varchar(64) NOT NULL,
	default_tenant_id varchar(64),
	username varchar(128) NOT NULL,
	email varchar(191),
	display_name varchar(191),
	password_hash varchar(255),
	status varchar(32) NOT NULL,
	encrypted_sec_key blob
)`

// TestMigrateRejectsCrossTenantDuplicateUsernames proves a legacy database with the same sign-in name in two
// tenants is refused with an actionable sentinel before any unique index is created. | TestMigrateRejectsCrossTenantDuplicateUsernames
// 证明旧库在两个租户里出现同一登录名时，会在创建任何唯一索引之前以可操作的哨兵拒绝。
func TestMigrateRejectsCrossTenantDuplicateUsernames(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "legacy.db"))
	if err := db.Exec(legacyUsersTable).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	for _, row := range []struct{ id, tenant, username string }{
		{"usr_1", "tn_a", "Alice"},
		{"usr_2", "tn_b", "alice"},
	} {
		if err := db.Exec(
			"INSERT INTO yd_auth_users (id, tenant_id, default_tenant_id, username, status) VALUES (?, ?, ?, ?, 'active')",
			row.id, row.tenant, row.tenant, row.username,
		).Error; err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}

	err := Migrate(context.Background(), db)
	if !errors.Is(err, ErrMigrationIdentityConflict) {
		t.Fatalf("Migrate() = %v, want ErrMigrationIdentityConflict", err)
	}
}

// TestMigrateRejectsDuplicateUserIDs proves duplicate global ids in a legacy database are refused as well. |
// TestMigrateRejectsDuplicateUserIDs 证明旧库中的重复全局 ID 同样被拒绝。
func TestMigrateRejectsDuplicateUserIDs(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "legacy-ids.db"))
	if err := db.Exec(legacyUsersTable).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	for _, row := range []struct{ tenant, username string }{
		{"tn_a", "first"},
		{"tn_b", "second"},
	} {
		if err := db.Exec(
			"INSERT INTO yd_auth_users (id, tenant_id, default_tenant_id, username, status) VALUES ('usr_same', ?, ?, ?, 'active')",
			row.tenant, row.tenant, row.username,
		).Error; err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}

	err := Migrate(context.Background(), db)
	if !errors.Is(err, ErrMigrationIdentityConflict) {
		t.Fatalf("Migrate() = %v, want ErrMigrationIdentityConflict", err)
	}
}

// TestMigrateRejectsDuplicateFoldedKeys proves a partially migrated table whose username_key column already
// exists is checked on the effective key, so two rows sharing a folded name are still refused. |
// TestMigrateRejectsDuplicateFoldedKeys 证明已经带有 username_key 列的半迁移表仍按有效键检查，两行共享折叠登录名
// 时同样被拒绝。
func TestMigrateRejectsDuplicateFoldedKeys(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "legacy-keys.db"))
	if err := db.Exec(`CREATE TABLE yd_auth_users (
	created_at datetime,
	updated_at datetime,
	id varchar(64) NOT NULL,
	tenant_id varchar(64) NOT NULL,
	default_tenant_id varchar(64),
	username varchar(128) NOT NULL,
	username_key varchar(128) NOT NULL,
	status varchar(32) NOT NULL
)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	for _, row := range []struct{ id, tenant, username string }{
		{"usr_1", "tn_a", "Alice"},
		{"usr_2", "tn_b", "alice"},
	} {
		if err := db.Exec(
			"INSERT INTO yd_auth_users (id, tenant_id, default_tenant_id, username, username_key, status) VALUES (?, ?, ?, ?, ?, 'active')",
			row.id, row.tenant, row.tenant, row.username, strings.ToLower(row.username),
		).Error; err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}
	if err := Migrate(context.Background(), db); !errors.Is(err, ErrMigrationIdentityConflict) {
		t.Fatalf("Migrate() = %v, want ErrMigrationIdentityConflict", err)
	}
}

// TestMigrateAcceptsUniqueLegacyRows proves the pre-check is not a blanket refusal: a legacy table whose ids and
// folded names are unique migrates normally. | TestMigrateAcceptsUniqueLegacyRows 证明预检不是一刀切拒绝：
// ID 与折叠登录名唯一的旧表会正常迁移。
func TestMigrateAcceptsUniqueLegacyRows(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "legacy-clean.db"))
	if err := db.Exec(legacyUsersTable).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO yd_auth_users (id, tenant_id, default_tenant_id, username, status) VALUES ('usr_1', 'tn_a', 'tn_a', 'Alice', 'active')",
	).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() = %v, want nil", err)
	}
}
