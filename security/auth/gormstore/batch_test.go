package gormstore

import (
	"context"
	"testing"
	"time"

	"github.com/shibingli/yd/security/auth"
)

// seedBatchUsers creates two tenants and a handful of users for the batch lookups. |
// seedBatchUsers 为批量查询创建两个租户与若干用户。
func seedBatchUsers(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	for _, tenant := range []*auth.Tenant{
		{ID: "tn_a", Slug: "alpha", Name: "Alpha", Status: auth.StatusActive},
		{ID: "tn_b", Slug: "beta", Name: "Beta", Status: auth.StatusActive},
	} {
		if err := store.CreateTenant(ctx, tenant); err != nil {
			t.Fatalf("CreateTenant(%s) = %v | CreateTenant(%s) = %v", tenant.ID, err, tenant.ID, err)
		}
	}
	if err := store.UpsertRole(ctx, &auth.Role{ID: "rl_reader", TenantID: "tn_a", Name: "reader"}); err != nil {
		t.Fatalf("UpsertRole() = %v | UpsertRole() = %v", err, err)
	}
	now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	users := []*auth.User{
		{ID: "us_alice", TenantID: "tn_a", Username: "alice", DisplayName: "Alice", Email: "alice@example.com", Status: auth.StatusActive, Roles: []string{"reader"}, CreatedAt: now},
		{ID: "us_alicia", TenantID: "tn_a", Username: "alicia", DisplayName: "Alicia", Status: auth.StatusActive, CreatedAt: now.Add(time.Minute)},
		{ID: "us_bob", TenantID: "tn_a", Username: "bob", DisplayName: "Bob", Status: auth.StatusActive, CreatedAt: now.Add(2 * time.Minute)},
		{ID: "us_carol", TenantID: "tn_a", Username: "carol", DisplayName: "Alice Cooper", Status: auth.StatusDisabled, CreatedAt: now.Add(3 * time.Minute)},
		{ID: "us_dave", TenantID: "tn_b", Username: "dave", DisplayName: "Alice Dave", Status: auth.StatusActive, CreatedAt: now.Add(4 * time.Minute)},
	}
	for _, user := range users {
		if err := store.CreateUser(ctx, user); err != nil {
			t.Fatalf("CreateUser(%s) = %v | CreateUser(%s) = %v", user.ID, err, user.ID, err)
		}
	}
}

// TestGetUsersBatchesAndKeepsTenantBoundary proves GetUsers returns exactly the requested members with their
// tenant roles and never leaks another tenant's user. |
// TestGetUsersBatchesAndKeepsTenantBoundary 证明 GetUsers 精确返回请求的成员及其租户角色，且绝不泄露其它租户的用户。
func TestGetUsersBatchesAndKeepsTenantBoundary(t *testing.T) {
	store, _ := openStore(t)
	seedBatchUsers(t, store)
	ctx := context.Background()

	users, err := store.GetUsers(ctx, "tn_a", []string{"us_alice", "us_bob", "us_dave", "us_missing", "us_alice"})
	if err != nil {
		t.Fatalf("GetUsers() = %v | GetUsers() = %v", err, err)
	}
	if len(users) != 2 {
		t.Fatalf("GetUsers() returned %d users, want 2 | GetUsers() 返回 %d 个用户，期望 2", len(users), len(users))
	}
	byID := make(map[string]*auth.User, len(users))
	for _, user := range users {
		byID[user.ID] = user
		if user.TenantID != "tn_a" {
			t.Errorf("%s acts in tenant %q, want tn_a | %s 的活动租户 = %q，期望 tn_a", user.ID, user.TenantID, user.ID, user.TenantID)
		}
	}
	if _, leaked := byID["us_dave"]; leaked {
		t.Errorf("GetUsers leaked another tenant's user | GetUsers 泄露了其它租户的用户")
	}
	if got := byID["us_alice"]; got == nil {
		t.Fatalf("GetUsers did not return us_alice | GetUsers 未返回 us_alice")
	} else if len(got.Roles) != 1 || got.Roles[0] != "reader" {
		t.Errorf("us_alice roles = %v, want [reader] | us_alice 角色 = %v，期望 [reader]", got.Roles, got.Roles)
	}
	if _, err := store.GetUsers(ctx, "tn_missing", []string{"us_alice"}); err == nil {
		t.Errorf("GetUsers(unknown tenant) = nil, want an error | GetUsers(未知租户) = nil，期望错误")
	}
}

// TestListUsersPageFiltersAndPaginates proves the admin list is paged in SQL, newest first, and keeps the
// tenant boundary. | TestListUsersPageFiltersAndPaginates 证明管理列表在 SQL 内分页、从新到旧，并保持租户边界。
func TestListUsersPageFiltersAndPaginates(t *testing.T) {
	store, _ := openStore(t)
	seedBatchUsers(t, store)
	ctx := context.Background()

	first, total, err := store.ListUsersPage(ctx, "tn_a", "", 2, 0)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if total != 4 || len(first) != 2 {
		t.Fatalf("first page total=%d len=%d, want 4/2 | 第一页 total=%d len=%d，期望 4/2", total, len(first), total, len(first))
	}
	if first[0].ID != "us_carol" || first[1].ID != "us_bob" {
		t.Errorf("first page order = %s, %s, want us_carol, us_bob | 第一页顺序 = %s, %s，期望 us_carol, us_bob",
			first[0].ID, first[1].ID, first[0].ID, first[1].ID)
	}

	second, total, err := store.ListUsersPage(ctx, "tn_a", "", 2, 2)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if total != 4 || len(second) != 2 || second[0].ID != "us_alicia" || second[1].ID != "us_alice" {
		t.Fatalf("second page = %+v (total %d), want us_alicia, us_alice | 第二页 = %+v（总数 %d），期望 us_alicia, us_alice", second, total, second, total)
	}

	filtered, filteredTotal, err := store.ListUsersPage(ctx, "tn_a", "ali", 10, 0)
	if err != nil {
		t.Fatalf("filtered page: %v", err)
	}
	// The admin list includes disabled accounts, so "ali" also matches Carol's display name "Alice Cooper".
	// 管理列表包含停用账号，因此 "ali" 也会匹配 Carol 的展示名 "Alice Cooper"。
	if filteredTotal != 3 || len(filtered) != 3 {
		t.Fatalf("filtered total=%d len=%d, want 3/3 | 过滤 total=%d len=%d，期望 3/3", filteredTotal, len(filtered), filteredTotal, len(filtered))
	}
	if filtered[0].ID != "us_carol" || filtered[1].ID != "us_alicia" || filtered[2].ID != "us_alice" {
		t.Errorf("filtered order = %s, %s, %s | 过滤顺序 = %s, %s, %s",
			filtered[0].ID, filtered[1].ID, filtered[2].ID, filtered[0].ID, filtered[1].ID, filtered[2].ID)
	}

	otherTenant, otherTotal, err := store.ListUsersPage(ctx, "tn_b", "ali", 10, 0)
	if err != nil {
		t.Fatalf("other tenant page: %v", err)
	}
	if otherTotal != 1 || len(otherTenant) != 1 || otherTenant[0].ID != "us_dave" {
		t.Errorf("tenant boundary leaked: total=%d rows=%+v | 租户边界泄漏：total=%d rows=%+v", otherTotal, otherTenant, otherTotal, otherTenant)
	}
}

// TestCountAndRecentUsersStayBounded proves the dashboard reads a count and a bounded strip instead of the roster. |
// TestCountAndRecentUsersStayBounded 证明仪表盘读取的是计数与有界条带，而不是整份名册。
func TestCountAndRecentUsersStayBounded(t *testing.T) {
	store, _ := openStore(t)
	seedBatchUsers(t, store)
	ctx := context.Background()

	count, err := store.CountUsers(ctx, "tn_a")
	if err != nil {
		t.Fatalf("CountUsers() = %v | CountUsers() = %v", err, err)
	}
	if count != 4 {
		t.Errorf("CountUsers(tn_a) = %d, want 4 | CountUsers(tn_a) = %d，期望 4", count, count)
	}
	if _, err := store.CountUsers(ctx, "tn_missing"); err == nil {
		t.Errorf("CountUsers(unknown tenant) = nil, want an error | CountUsers(未知租户) = nil，期望错误")
	}

	recent, err := store.RecentUsers(ctx, "tn_a", 2)
	if err != nil {
		t.Fatalf("RecentUsers() = %v | RecentUsers() = %v", err, err)
	}
	if len(recent) != 2 {
		t.Fatalf("RecentUsers(limit=2) returned %d users, want 2 | RecentUsers(limit=2) 返回 %d 个用户，期望 2", len(recent), len(recent))
	}
	if recent[0].ID != "us_carol" || recent[1].ID != "us_bob" {
		t.Errorf("RecentUsers order = %s, %s, want us_carol, us_bob | 最近用户排序 = %s, %s，期望 us_carol, us_bob",
			recent[0].ID, recent[1].ID, recent[0].ID, recent[1].ID)
	}
	for _, user := range recent {
		if user.TenantID != "tn_a" {
			t.Errorf("%s acts in tenant %q, want tn_a | %s 的活动租户 = %q，期望 tn_a", user.ID, user.TenantID, user.ID, user.TenantID)
		}
	}
}

// TestSearchUsersPushesFiltersIntoSQL proves inactive users and other tenants are excluded and the limit applies
// after ordering. | TestSearchUsersPushesFiltersIntoSQL 证明停用用户与其它租户被排除，且上限在排序后生效。
func TestSearchUsersPushesFiltersIntoSQL(t *testing.T) {
	store, _ := openStore(t)
	seedBatchUsers(t, store)
	ctx := context.Background()

	users, err := store.SearchUsers(ctx, "tn_a", "ali", "", 10)
	if err != nil {
		t.Fatalf("SearchUsers() = %v | SearchUsers() = %v", err, err)
	}
	if len(users) != 2 {
		t.Fatalf("SearchUsers(ali) returned %d users, want 2 | SearchUsers(ali) 返回 %d 个用户，期望 2", len(users), len(users))
	}
	if users[0].ID != "us_alice" || users[1].ID != "us_alicia" {
		t.Errorf("SearchUsers(ali) order = %s, %s, want us_alice, us_alicia | 排序 = %s, %s，期望 us_alice, us_alicia",
			users[0].ID, users[1].ID, users[0].ID, users[1].ID)
	}
	for _, user := range users {
		if user.Status != auth.StatusActive {
			t.Errorf("SearchUsers returned inactive user %s | SearchUsers 返回了停用用户 %s", user.ID, user.ID)
		}
		if user.ID == "us_dave" {
			t.Errorf("SearchUsers leaked another tenant's user | SearchUsers 泄露了其它租户的用户")
		}
	}

	limited, err := store.SearchUsers(ctx, "tn_a", "", "", 1)
	if err != nil {
		t.Fatalf("SearchUsers(limit) = %v | SearchUsers(limit) = %v", err, err)
	}
	if len(limited) != 1 {
		t.Fatalf("SearchUsers(limit=1) returned %d users, want 1 | SearchUsers(limit=1) 返回 %d 个用户，期望 1", len(limited), len(limited))
	}
	withoutAlice, err := store.SearchUsers(ctx, "tn_a", "ali", "us_alice", 10)
	if err != nil {
		t.Fatalf("SearchUsers(exclude) = %v | SearchUsers(exclude) = %v", err, err)
	}
	for _, user := range withoutAlice {
		if user.ID == "us_alice" {
			t.Errorf("SearchUsers returned the excluded user | SearchUsers 返回了被排除的用户")
		}
	}
}
