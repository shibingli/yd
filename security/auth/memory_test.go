package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibingli/yd/http/router"
)

// TestCrossTenantAccessIsDenied 要求跨租户的每一次读写都以 ErrNotFound 拒绝，且绝不返回数据。
//
// The test walks the whole surface that reaches an entity by id: user reads, the
// username index, updates, disable, role assignment, role reads and role deletion,
// and finally the same probe through an authenticated HTTP request. | 本测试走遍所有「按 ID 触达实体」
// 的接口：用户读取、用户名索引、更新、禁用、角色分配、角色读取与角色删除，最后再通过一次已认证的 HTTP 请求复现同样的探测。
func TestCrossTenantAccessIsDenied(t *testing.T) {
	f := newFixture(t)
	ctx := testCtx

	// Service layer: tenant B asking for an entity of tenant A. | 服务层：租户 B 索要租户 A 的实体。
	u, err := f.svc.GetUser(ctx, f.tenantB.ID, f.member.ID)
	requireIs(t, err, ErrNotFound, "GetUser(B, memberA)")
	requireNoUser(t, u, "GetUser(B, memberA)")

	u, err = f.svc.GetUser(ctx, f.tenantA.ID, f.stranger.ID)
	requireIs(t, err, ErrNotFound, "GetUser(A, strangerB)")
	requireNoUser(t, u, "GetUser(A, strangerB)")

	// Store layer: same answer, because the tenant is the first key of every map. | Store 层：答案相同，因为租户是每个 map 的第一层键。
	u, err = f.store.GetUser(ctx, f.tenantB.ID, f.member.ID)
	requireIs(t, err, ErrNotFound, "store.GetUser(B, memberA)")
	requireNoUser(t, u, "store.GetUser(B, memberA)")

	u, err = f.store.GetUserByUsername(ctx, f.tenantB.ID, f.member.Username)
	requireIs(t, err, ErrNotFound, "store.GetUserByUsername(B, memberA)")
	requireNoUser(t, u, "store.GetUserByUsername(B, memberA)")

	// Credentials of tenant A must not sign anybody into tenant B. | 租户 A 的凭据不得让其登录租户 B。
	u, err = f.svc.Authenticate(ctx, f.tenantB.ID, f.member.Username, testPassword)
	requireIs(t, err, ErrInvalidCredentials, "Authenticate(B, memberA)")
	requireNoUser(t, u, "Authenticate(B, memberA)")

	// Writes must not cross the boundary either. | 写操作同样不得越界。
	err = f.svc.UpdateUser(ctx, f.tenantB.ID, &User{ID: f.member.ID, Username: "hijacked"})
	requireIs(t, err, ErrNotFound, "UpdateUser(B, memberA)")

	err = f.svc.DisableUser(ctx, f.tenantB.ID, f.member.ID)
	requireIs(t, err, ErrNotFound, "DisableUser(B, memberA)")

	err = f.svc.SetUserRoles(ctx, f.tenantB.ID, f.member.ID, []string{"member"})
	requireIs(t, err, ErrNotFound, "SetUserRoles(B, memberA)")

	err = f.svc.DeleteRole(ctx, f.tenantB.ID, f.adminRole.ID)
	requireIs(t, err, ErrNotFound, "DeleteRole(B, adminRoleA)")

	err = f.svc.RotateSecKey(ctx, f.tenantB.ID, f.member.ID)
	requireIs(t, err, ErrNotFound, "RotateSecKey(B, memberA)")

	// Nothing changed: tenant A still owns its user, its roles and its token. | 一切未变：租户 A 仍拥有其用户、角色与令牌。
	kept, err := f.svc.GetUser(ctx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser(A, memberA)")
	if kept.Username != "member" || kept.Status != StatusActive || len(kept.Roles) != 1 {
		t.Fatalf("tenant A user was touched by cross-tenant calls: %+v | 租户 A 的用户被跨租户调用改动：%+v", kept, kept)
	}
	if _, err := f.store.GetRole(ctx, f.tenantA.ID, f.adminRole.ID); err != nil {
		t.Fatalf("tenant A role was deleted by a cross-tenant call: %v | 租户 A 的角色被跨租户调用删除：%v", err, err)
	}
	if _, err := f.issuer.Verify(ctx, mustIssue(t, f, f.member)); err != nil {
		t.Fatalf("token of tenant A was invalidated by cross-tenant calls: %v | 租户 A 的令牌被跨租户调用作废：%v", err, err)
	}

	// Listing is tenant-scoped as well. | 列表同样受租户约束。
	users, err := f.svc.ListUsers(ctx, f.tenantB.ID)
	requireNoError(t, err, "ListUsers(B)")
	if len(users) != 1 || users[0].ID != f.stranger.ID {
		t.Fatalf("ListUsers(B) leaked users of another tenant: %+v | ListUsers(B) 泄露了其它租户的用户：%+v", users, users)
	}

	// HTTP layer: a token of tenant A cannot read a user of tenant B. | HTTP 层：租户 A 的令牌读不到租户 B 的用户。
	r := router.New()
	r.Use(Middleware(MiddlewareOptions{Issuer: f.issuer}))
	r.GET("/users/:id", func(c *router.Ctx) {
		current, ok := CurrentUser(c)
		if !ok {
			if err := c.String(http.StatusUnauthorized, "no user"); err != nil {
				t.Errorf("write failed | 写响应失败: %v", err)
			}
			return
		}
		// The handler only ever trusts the tenant of the authenticated user. | 处理器只信任已认证用户所属的租户。
		target, getErr := f.svc.GetUser(c.Request.Context(), current.TenantID, c.Param("id"))
		if getErr != nil {
			status := http.StatusInternalServerError
			if errors.Is(getErr, ErrNotFound) {
				status = http.StatusNotFound
			}
			if err := c.String(status, getErr.Error()); err != nil {
				t.Errorf("write failed | 写响应失败: %v", err)
			}
			return
		}
		if err := c.String(http.StatusOK, target.Username); err != nil {
			t.Errorf("write failed | 写响应失败: %v", err)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/users/"+f.stranger.ID, nil)
	req.Header.Set("Authorization", "Bearer "+mustIssue(t, f, f.member))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant HTTP read want 404, got %d | 跨租户 HTTP 读取期望 404，实际 %d", rec.Code, rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, f.stranger.Username) || strings.Contains(body, f.stranger.ID) {
		t.Fatalf("404 body leaked the foreign user: %q | 404 正文泄露了外部用户：%q", body, body)
	}
	if !strings.Contains(rec.Body.String(), ErrNotFound.Error()) {
		t.Fatalf("404 body want the ErrNotFound text, got %q | 404 正文期望 ErrNotFound 文案，实际 %q", rec.Body.String(), rec.Body.String())
	}
}

// TestCrossTenantRoleNamesDoNotCollide checks that a same-named role in two tenants stays independent. | TestCrossTenantRoleNamesDoNotCollide 校验同名角色在两个租户内互不影响。
func TestCrossTenantRoleNamesDoNotCollide(t *testing.T) {
	f := newFixture(t)
	if !f.svc.HasPermission(f.member, "user:read") {
		t.Fatal("tenant A member lost its own permission | 租户 A 的 member 丢失了自己的权限")
	}
	if f.svc.HasPermission(f.member, "billing:read") {
		t.Fatal("tenant A member gained the permission of tenant B's same-named role | 租户 A 的 member 获得了租户 B 同名角色的权限")
	}
	if !f.svc.HasPermission(f.stranger, "billing:read") {
		t.Fatal("tenant B member lost its own permission | 租户 B 的 member 丢失了自己的权限")
	}
	if f.svc.HasPermission(f.stranger, "user:read") {
		t.Fatal("tenant B member gained the permission of tenant A's same-named role | 租户 B 的 member 获得了租户 A 同名角色的权限")
	}
}

// TestMemoryStoreIsConcurrencySafe 在 -race 下并发读写 Store、Service 与签发器。
//
// The test is deliberately run under the race detector, so it fails on any shared
// mutable state that is not guarded. | 本测试刻意在竞态检测器下运行，因此任何未被保护的共享可变状态都会让它失败。
func TestMemoryStoreIsConcurrencySafe(t *testing.T) {
	clock := newTestClock()
	svc, store := newTestService(t, Options{Now: clock.Now})
	issuer, err := NewTokenIssuer(svc, TokenOptions{Issuer: testIssuer, TTL: time.Hour, Now: clock.Now})
	requireNoError(t, err, "NewTokenIssuer")

	tenant := mustTenant(t, svc, "concurrent", "Concurrent")
	mustRole(t, svc, tenant.ID, "member", "user:read")
	mustRole(t, svc, tenant.ID, "other", "user:write")

	const (
		workers = 8
		rounds  = 25
	)
	accounts := make([]*User, workers)
	for i := range accounts {
		accounts[i] = mustUser(t, svc, tenant.ID, "worker-"+strconv.Itoa(i), "member")
	}

	var wg sync.WaitGroup
	// A dedicated writer flips a role's permissions while the readers evaluate them. | 专职写入者反复改写某个角色的权限，与读者并发。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if _, err := svc.UpsertRole(testCtx, tenant.ID, &Role{
				Name:        "member",
				Permissions: []string{"user:read", "user:write"},
			}); err != nil {
				t.Errorf("UpsertRole | UpsertRole 失败: %v", err)
				return
			}
			if _, err := svc.ListRoles(testCtx, tenant.ID); err != nil {
				t.Errorf("ListRoles | ListRoles 失败: %v", err)
				return
			}
		}
	}()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			me := accounts[w]
			for round := 0; round < rounds; round++ {
				// Every round starts from a fresh copy, because the running copy of
				// this test rotates its own sec_key. | 每一轮都从新副本开始，因为本测试的运行副本会轮换自己的 sec_key。
				current, err := svc.GetUser(testCtx, tenant.ID, me.ID)
				if err != nil {
					t.Errorf("GetUser | GetUser 失败: %v", err)
					return
				}
				if _, err := store.ListUsers(testCtx, tenant.ID); err != nil {
					t.Errorf("ListUsers | ListUsers 失败: %v", err)
					return
				}
				_ = svc.PermissionsOf(current)
				_ = svc.HasPermission(current, "user:read")
				_ = svc.RolesOf(current)
				if err := svc.SetUserRoles(testCtx, tenant.ID, me.ID, []string{"member"}); err != nil {
					t.Errorf("SetUserRoles | SetUserRoles 失败: %v", err)
					return
				}
				if err := svc.UpdateUser(testCtx, tenant.ID, &User{ID: me.ID, DisplayName: "round-" + strconv.Itoa(round)}); err != nil {
					t.Errorf("UpdateUser | UpdateUser 失败: %v", err)
					return
				}
				token, _, err := issuer.Issue(testCtx, current)
				if err != nil {
					t.Errorf("Issue | Issue 失败: %v", err)
					return
				}
				if _, err := issuer.Verify(testCtx, token); err != nil {
					t.Errorf("Verify | Verify 失败: %v", err)
					return
				}
				if round%7 == 0 {
					if err := svc.RotateSecKey(testCtx, tenant.ID, me.ID); err != nil {
						t.Errorf("RotateSecKey | RotateSecKey 失败: %v", err)
						return
					}
				}
				// Reads of a *shared* role table and of other users' records. | 读取共享角色表与其它用户的记录。
				if _, err := store.GetUser(testCtx, tenant.ID, accounts[(w+1)%workers].ID); err != nil {
					t.Errorf("GetUser(other) | GetUser(other) 失败: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// Exactly one creator wins a username race; the store must report a conflict
	// for every loser instead of overwriting the winner. | 用户名竞争中只有一个创建者胜出；Store 必须为所有失败者报告冲突，
	// 而不是覆盖胜出者。
	var (
		mu       sync.Mutex
		wins     int
		conflict int
	)
	var racers sync.WaitGroup
	for i := 0; i < workers; i++ {
		racers.Add(1)
		go func() {
			defer racers.Done()
			_, err := svc.CreateUser(testCtx, tenant.ID, CreateUserInput{Username: "race", Password: testPassword})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrConflict):
				conflict++
			default:
				t.Errorf("CreateUser race returned %v | CreateUser 竞争返回 %v", err, err)
			}
		}()
	}
	racers.Wait()
	if wins != 1 || conflict != workers-1 {
		t.Fatalf("username race want 1 win and %d conflicts, got %d and %d | 用户名竞争期望 1 胜 %d 冲突，实际 %d 胜 %d 冲突",
			workers-1, wins, conflict, workers-1, wins, conflict)
	}
}

// TestMemoryStoreDuplicateRefusals checks how duplicate entities and cross-tenant writes are refused. | TestMemoryStoreDuplicateRefusals 校验重复实体与越权写入的拒绝行为。
func TestMemoryStoreDuplicateRefusals(t *testing.T) {
	f := newFixture(t)
	err := f.store.CreateTenant(testCtx, &Tenant{ID: f.tenantA.ID, Slug: "again"})
	requireIs(t, err, ErrConflict, "CreateTenant(duplicate id)")

	err = f.store.CreateUser(testCtx, &User{ID: "us_dup", TenantID: "tn_missing", Username: "ghost"})
	requireIs(t, err, ErrNotFound, "CreateUser(unknown tenant)")

	err = f.store.CreateUser(testCtx, &User{ID: f.member.ID, TenantID: f.tenantA.ID, Username: "other"})
	requireIs(t, err, ErrConflict, "CreateUser(duplicate id)")

	err = f.store.CreateUser(testCtx, &User{ID: "us_new", TenantID: f.tenantA.ID, Username: f.member.Username})
	requireIs(t, err, ErrConflict, "CreateUser(duplicate username)")

	err = f.store.CreateUser(testCtx, &User{ID: "us_new", TenantID: f.tenantA.ID, Username: "MEMBER"})
	requireIs(t, err, ErrConflict, "CreateUser(case-variant username)")

	err = f.store.UpdateUser(testCtx, f.tenantA.ID, &User{ID: "us_missing", Username: "x"})
	requireIs(t, err, ErrNotFound, "UpdateUser(unknown user)")

	err = f.store.UpdateUser(testCtx, f.tenantB.ID, &User{ID: f.member.ID, TenantID: f.tenantA.ID, Username: "x"})
	requireIs(t, err, ErrNotFound, "UpdateUser(tenant mismatch)")

	_, err = f.store.GetRole(testCtx, f.tenantB.ID, f.adminRole.ID)
	requireIs(t, err, ErrNotFound, "GetRole(cross tenant)")

	_, err = f.store.GetRole(testCtx, f.tenantA.ID, "rl_missing")
	requireIs(t, err, ErrNotFound, "GetRole(unknown id)")

	err = f.store.DeleteRole(testCtx, f.tenantA.ID, "rl_missing")
	requireIs(t, err, ErrNotFound, "DeleteRole(unknown id)")

	_, err = f.store.ListRoles(testCtx, "tn_missing")
	requireIs(t, err, ErrNotFound, "ListRoles(unknown tenant)")
}
