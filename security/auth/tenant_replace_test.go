package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// replaceFixture builds a memory store with three tenants, a member role in each and one user who
// belongs to the first two tenants. | replaceFixture 构造一个内存存储：三个租户、每个租户一个 member 角色，
// 以及一个隶属前两个租户的用户。
func replaceFixture(t *testing.T) (*memoryStore, string) {
	t.Helper()

	store := newMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"tn_a", "tn_b", "tn_c"} {
		tenant := &Tenant{ID: id, Slug: id, Name: id, Status: StatusActive, CreatedAt: now}
		if err := store.CreateTenant(ctx, tenant); err != nil {
			t.Fatalf("CreateTenant(%s) = %v", id, err)
		}
		role := &Role{ID: "rl_" + id, TenantID: id, Name: "member"}
		if err := store.UpsertRole(ctx, role); err != nil {
			t.Fatalf("UpsertRole(%s) = %v", id, err)
		}
	}
	user := &User{
		ID:              "usr_replace",
		TenantID:        "tn_a",
		HomeTenantID:    "tn_a",
		DefaultTenantID: "tn_a",
		TenantIDs:       []string{"tn_b"},
		Username:        "replace",
		Status:          StatusActive,
		Roles:           []string{"member"},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser() = %v", err)
	}
	return store, user.ID
}

// TestTenantReplaceIsAtomic asserts that a successful replacement applies additions, removals, kept roles and
// the default tenant together. | TestTenantReplaceIsAtomic 断言原子替换成功时新增、移除、保留角色与默认租户一起生效。
func TestTenantReplaceIsAtomic(t *testing.T) {
	store, userID := replaceFixture(t)
	ctx := context.Background()

	err := store.ReplaceUserTenants(ctx, userID, []TenantMembership{
		{TenantID: "tn_a"}, // keep the stored member role | 保留库中 member 角色
		{TenantID: "tn_c", Roles: []string{"member"}}, // new membership | 新成员关系
	}, "tn_c")
	if err != nil {
		t.Fatalf("ReplaceUserTenants() = %v, want nil", err)
	}

	user, err := store.GetUser(ctx, "tn_c", userID)
	if err != nil {
		t.Fatalf("GetUser(tn_c) = %v", err)
	}
	if user.DefaultTenantID != "tn_c" {
		t.Errorf("DefaultTenantID = %q, want tn_c", user.DefaultTenantID)
	}
	if !hasTenantID(user.TenantIDs, "tn_a") || !hasTenantID(user.TenantIDs, "tn_c") {
		t.Errorf("TenantIDs = %v, want tn_a and tn_c", user.TenantIDs)
	}
	if hasTenantID(user.TenantIDs, "tn_b") {
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
	for tenantID, entry := range store.userMembers[userID] {
		want := tenantID == "tn_c"
		if entry.defaultTenant != want {
			t.Errorf("default flag of %s = %v, want %v", tenantID, entry.defaultTenant, want)
		}
	}
}

// TestTenantReplaceValidatesBeforeWriting asserts that any validation failure leaves no partial membership
// adjustment behind (the P1-8 atomicity requirement). | TestTenantReplaceValidatesBeforeWriting 断言任一校验失败
// 都不留下部分成员关系（P1-8 的原子性要求）。
func TestTenantReplaceValidatesBeforeWriting(t *testing.T) {
	store, userID := replaceFixture(t)
	ctx := context.Background()

	cases := []struct {
		name        string
		memberships []TenantMembership
		defaultID   string
		want        error
	}{
		{
			name:        "removing the home tenant",
			memberships: []TenantMembership{{TenantID: "tn_b"}, {TenantID: "tn_c"}},
			defaultID:   "tn_b",
			want:        ErrConflict,
		},
		{
			name:        "default outside the set",
			memberships: []TenantMembership{{TenantID: "tn_a"}, {TenantID: "tn_c"}},
			defaultID:   "tn_b",
			want:        ErrInvalidInput,
		},
		{
			name:        "unknown tenant after a valid one",
			memberships: []TenantMembership{{TenantID: "tn_a"}, {TenantID: "tn_c", Roles: []string{"member"}}, {TenantID: "tn_missing"}},
			defaultID:   "tn_c",
			want:        ErrNotFound,
		},
		{
			name:        "unknown role",
			memberships: []TenantMembership{{TenantID: "tn_a"}, {TenantID: "tn_c", Roles: []string{"no-such-role"}}},
			defaultID:   "tn_c",
			want:        ErrNotFound,
		},
		{
			name:        "duplicate tenant",
			memberships: []TenantMembership{{TenantID: "tn_a"}, {TenantID: "tn_c"}, {TenantID: "tn_c"}},
			defaultID:   "tn_c",
			want:        ErrInvalidInput,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := store.ReplaceUserTenants(ctx, userID, tc.memberships, tc.defaultID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ReplaceUserTenants() = %v, want %v", err, tc.want)
			}
			assertReplaceFixtureUnchanged(t, store, userID)
		})
	}
}

// assertReplaceFixtureUnchanged asserts the fixture still holds exactly tn_a and tn_b with tn_a as the default. |
// assertReplaceFixtureUnchanged 断言夹具仍然只包含 tn_a 与 tn_b，且默认租户仍是 tn_a。
func assertReplaceFixtureUnchanged(t *testing.T, store *memoryStore, userID string) {
	t.Helper()

	stored, ok := store.users[userID]
	if !ok {
		t.Fatal("the user disappeared")
	}
	if stored.DefaultTenantID != "tn_a" {
		t.Errorf("DefaultTenantID = %q, want tn_a", stored.DefaultTenantID)
	}
	members := store.userMembers[userID]
	if len(members) != 2 {
		t.Fatalf("memberships = %v, want tn_a and tn_b", members)
	}
	for _, tenantID := range []string{"tn_a", "tn_b"} {
		if _, ok := members[tenantID]; !ok {
			t.Errorf("membership %s is missing", tenantID)
		}
	}
	if _, ok := members["tn_c"]; ok {
		t.Error("a failed replacement still added tn_c")
	}
}

// hasTenantID reports whether ids contains want. | hasTenantID 报告 ids 是否包含 want。
func hasTenantID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
