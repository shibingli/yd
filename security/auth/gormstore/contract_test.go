package gormstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/security/auth"
)

// TestContractMatchesTheMemoryStore runs one scenario against the map-backed
// reference implementation and against the GORM one, and compares the journals step
// by step. | TestContractMatchesTheMemoryStore 把同一个场景分别跑在 map 参考实现与 GORM 实现上，
// 并逐步比对两份日志。
//
// This is the strongest available statement of "the two implementations behave
// identically": the same inputs produce the same observable outcomes, including
// which sentinel each failure carries. | 这是「两个实现行为一致」所能给出的最强声明：
// 相同输入产生相同的可观察结果，包括每次失败携带的哨兵。
func TestContractMatchesTheMemoryStore(t *testing.T) {
	ctx := context.Background()
	memory := runContract(ctx, auth.NewMemoryStore())
	store, _ := openStore(t)
	gorm := runContract(ctx, store)
	assertJournalsEqual(t, memory, gorm)
}

// TestContractJournalPinsTheReferenceOutcomes keeps the shared suite honest: if
// both implementations were wrong in the same way, the journal comparison above
// would still pass, so a handful of outcomes are pinned by hand. | TestContractJournalPinsTheReferenceOutcomes
// 让共享用例保持诚实：如果两个实现以相同的方式一起出错，上面的日志比对依然会通过，
// 因此这里手工固定若干结果。
func TestContractJournalPinsTheReferenceOutcomes(t *testing.T) {
	ctx := context.Background()
	for _, impl := range []struct {
		name  string
		store func() auth.Store
	}{
		{"memory", func() auth.Store { return auth.NewMemoryStore() }},
		{"gorm", func() auth.Store { store, _ := openStore(t); return store }},
	} {
		ctx := ctx
		store := impl.store()
		journal := runContract(ctx, store)
		byLabel := make(map[string]string)
		for _, entry := range journal {
			label, observation, ok := strings.Cut(entry, " = ")
			if !ok {
				t.Fatalf("%s: malformed journal entry %q | %s：日志条目格式错误 %q", impl.name, entry, impl.name, entry)
			}
			byLabel[label] = observation
		}
		want := map[string]string{
			"create-tenant":                     "ok tn_a",
			"create-tenant-duplicate-id":        "err:conflict",
			"create-tenant-duplicate-slug":      "err:conflict",
			"create-user-duplicate-username":    "err:conflict",
			"create-user-unknown-tenant":        "err:not-found",
			"create-user-blank-tenant":          "err:tenant-required",
			"get-user-other-tenant":             "err:not-found",
			"get-user-blank-tenant":             "err:tenant-required",
			"get-user-by-username-folded":       "ok Alice",
			"get-user-by-username-other-tenant": "err:not-found",
			"list-users-unknown-tenant":         "err:not-found",
			"update-user-username-clash":        "err:conflict",
			"update-user-other-tenant":          "err:not-found",
			"upsert-role-unknown-tenant":        "err:not-found",
			"upsert-role-blank-tenant":          "err:tenant-required",
			"delete-role-missing":               "err:not-found",
			"list-roles-unknown-tenant":         "err:not-found",
		}
		for label, wantObservation := range want {
			if got := byLabel[label]; got != wantObservation {
				t.Errorf("%s: step %q = %q, want %q | %s：步骤 %q = %q，期望 %q", impl.name, label, got, wantObservation, impl.name, label, got, wantObservation)
			}
		}
		if len(byLabel) < 25 {
			t.Errorf("%s: journal has %d steps, want at least 25 | %s：日志只有 %d 个步骤，期望至少 25 个", impl.name, len(byLabel), impl.name, len(byLabel))
		}
		t.Logf("%s journal | %s 日志:\n%s", impl.name, impl.name, strings.Join(journal, "\n"))
	}
}

// TestNoCallerSliceIsRetainedOrShared checks the second half of the Store
// contract: the adapter may neither keep a caller's slice nor hand out a reference
// to its own state. | TestNoCallerSliceIsRetainedOrShared 检查 Store 契约的另一半：
// 适配器既不得保留调用方的切片，也不得交出指向自身状态的引用。
//
// It matters more here than for the map-backed store, because a GORM round trip
// copies values anyway: the danger is a caller's slice being written into a row and
// then mutated, or a decoded slice being reused across calls. | 这一点比 map 实现更值得测：
// GORM 往返本来就会复制值，真正的风险在于调用方的切片被写进了行、随后又被改动，
// 或者解码出的切片被跨调用复用。
func TestNoCallerSliceIsRetainedOrShared(t *testing.T) {
	ctx := context.Background()
	store, _ := openStore(t)
	if err := store.CreateTenant(ctx, &auth.Tenant{ID: "tn_a", Slug: "alpha", Name: "Alpha", Status: auth.StatusActive}); err != nil {
		t.Fatalf("CreateTenant() = %v, want nil | CreateTenant() = %v，期望 nil", err, err)
	}
	roles := []string{"reader"}
	key := []byte("sealed-key")
	if err := store.UpsertRole(ctx, &auth.Role{ID: "rl_reader", TenantID: "tn_a", Name: "reader"}); err != nil {
		t.Fatalf("UpsertRole() = %v, want nil | UpsertRole() = %v，期望 nil", err, err)
	}
	if err := store.CreateUser(ctx, &auth.User{
		ID: "us_a", TenantID: "tn_a", Username: "Alice",
		Status: auth.StatusActive, Roles: roles, EncryptedSecKey: key,
	}); err != nil {
		t.Fatalf("CreateUser() = %v, want nil | CreateUser() = %v，期望 nil", err, err)
	}
	// The caller mutating its own slice must not rewrite history. | 调用方改动自己的切片不得改写历史。
	roles[0] = "root"
	key[0] = 'X'
	got, err := store.GetUser(ctx, "tn_a", "us_a")
	if err != nil {
		t.Fatalf("GetUser() = %v, want nil | GetUser() = %v，期望 nil", err, err)
	}
	assertStringsEqual(t, got.Roles, []string{"reader"}, "stored Roles")
	if string(got.EncryptedSecKey) != "sealed-key" {
		t.Errorf("stored EncryptedSecKey = %q, want %q | 库中 EncryptedSecKey = %q，期望 %q", got.EncryptedSecKey, "sealed-key", got.EncryptedSecKey, "sealed-key")
	}
	// The caller mutating the returned value must not reach the stored one. | 调用方改动返回值不得触及库中数据。
	got.Roles[0] = "root"
	got.EncryptedSecKey[0] = 'X'
	again, err := store.GetUser(ctx, "tn_a", "us_a")
	if err != nil {
		t.Fatalf("GetUser() = %v, want nil | GetUser() = %v，期望 nil", err, err)
	}
	assertStringsEqual(t, again.Roles, []string{"reader"}, "stored Roles")
	if string(again.EncryptedSecKey) != "sealed-key" {
		t.Errorf("stored EncryptedSecKey = %q, want %q | 库中 EncryptedSecKey = %q，期望 %q", again.EncryptedSecKey, "sealed-key", again.EncryptedSecKey, "sealed-key")
	}
	// The same for role permissions in both directions. | 角色权限在两个方向上同样如此。
	perms := []string{"user:read"}
	if err := store.UpsertRole(ctx, &auth.Role{ID: "rl_a", TenantID: "tn_a", Name: "admin", Permissions: perms}); err != nil {
		t.Fatalf("UpsertRole() = %v, want nil | UpsertRole() = %v，期望 nil", err, err)
	}
	perms[0] = "user:delete"
	role, err := store.GetRole(ctx, "tn_a", "rl_a")
	if err != nil {
		t.Fatalf("GetRole() = %v, want nil | GetRole() = %v，期望 nil", err, err)
	}
	assertStringsEqual(t, role.Permissions, []string{"user:read"}, "stored Permissions")
	role.Permissions[0] = "user:delete"
	roleAgain, err := store.GetRole(ctx, "tn_a", "rl_a")
	if err != nil {
		t.Fatalf("GetRole() = %v, want nil | GetRole() = %v，期望 nil", err, err)
	}
	assertStringsEqual(t, roleAgain.Permissions, []string{"user:read"}, "stored Permissions")
}

// contractStep is one action of the shared scenario. | contractStep 是共享场景中的一个动作。
type contractStep struct {
	// label names the step in the journal. | label 在日志中为该步骤命名。
	label string
	// run performs the action and returns a short, comparable observation. | run 执行动作并返回简短、可比较的观察结果。
	run func(store auth.Store) string
}

// runContract executes the whole scenario and returns one journal entry per step. | runContract 执行整个场景，每个步骤返回一条日志条目。
func runContract(ctx context.Context, store auth.Store) []string {
	out := make([]string, 0, 40)
	for _, step := range contractScenario() {
		out = append(out, step.label+" = "+step.run(store))
	}
	return out
}

// contractScenario is the shared scenario. | contractScenario 是共享场景。
//
// Every entity is built by hand with fixed ids and a frozen clock, and every role
// permission list is handed over sorted, which is the shape the Service produces
// before it reaches a store. | 每个实体都由固定 ID 与冻结时钟手工构造，每个角色权限列表都按排序传入，
// 这正是 Service 交给 Store 之前的形状。
func contractScenario() []contractStep {
	const (
		tenantA = "tn_a"
		tenantB = "tn_b"
		userA   = "us_a"
		userB   = "us_b"
		roleA   = "rl_a"
		roleB   = "rl_b"
	)
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	later := at.Add(time.Hour)
	tenant := func(id, slug, name string, created time.Time) *auth.Tenant {
		return &auth.Tenant{ID: id, Slug: slug, Name: name, Status: auth.StatusActive, CreatedAt: created}
	}
	user := func(id, tenantID, username string, roles []string, created time.Time) *auth.User {
		return &auth.User{
			ID:              id,
			TenantID:        tenantID,
			Username:        username,
			Email:           id + "@example.com",
			DisplayName:     username,
			PasswordHash:    "hash:" + id,
			Status:          auth.StatusActive,
			EncryptedSecKey: []byte("sealed:" + id),
			Roles:           roles,
			CreatedAt:       created,
			UpdatedAt:       created,
		}
	}
	role := func(id, tenantID, name string, perms []string) *auth.Role {
		return &auth.Role{ID: id, TenantID: tenantID, Name: name, Description: "desc " + name, Permissions: perms}
	}
	observe := func(err error, value string) string {
		if err != nil {
			return describeError(err)
		}
		return "ok " + value
	}
	return []contractStep{
		{"create-tenant", func(s auth.Store) string {
			err := s.CreateTenant(context.Background(), tenant(tenantA, "Demo", "Demo Workspace", at))
			return observe(err, tenantA)
		}},
		{"create-tenant-duplicate-id", func(s auth.Store) string {
			return describeError(s.CreateTenant(context.Background(), tenant(tenantA, "other", "Other", later)))
		}},
		{"create-tenant-duplicate-slug", func(s auth.Store) string {
			// The slug key folds case and trims, so " DEMO " collides with "Demo". | Slug 键折叠大小写并去空白，因此 " DEMO " 与 "Demo" 冲突。
			return describeError(s.CreateTenant(context.Background(), tenant("tn_c", " DEMO ", "Third", later)))
		}},
		{"create-tenant-blank-slug", func(s auth.Store) string {
			return describeError(s.CreateTenant(context.Background(), tenant("tn_d", "  ", "Fourth", later)))
		}},
		{"create-tenant-no-id", func(s auth.Store) string {
			return describeError(s.CreateTenant(context.Background(), tenant("", "fifth", "Fifth", later)))
		}},
		{"create-tenant-second", func(s auth.Store) string {
			err := s.CreateTenant(context.Background(), tenant(tenantB, "Other", "Other Workspace", later))
			return observe(err, tenantB)
		}},
		{"get-tenant", func(s auth.Store) string {
			got, err := s.GetTenant(context.Background(), tenantA)
			return observe(err, tenantSnapshot(got))
		}},
		{"get-tenant-missing", func(s auth.Store) string {
			_, err := s.GetTenant(context.Background(), "tn_missing")
			return describeError(err)
		}},
		{"list-tenants", func(s auth.Store) string {
			got, err := s.ListTenants(context.Background())
			return observe(err, listSnapshot(got, tenantSnapshot))
		}},
		{"create-user-unknown-tenant", func(s auth.Store) string {
			return describeError(s.CreateUser(context.Background(), user(userA, "tn_missing", "Alice", nil, at)))
		}},
		{"create-user-blank-tenant", func(s auth.Store) string {
			return describeError(s.CreateUser(context.Background(), user(userA, "  ", "Alice", nil, at)))
		}},
		{"create-user-blank-username", func(s auth.Store) string {
			return describeError(s.CreateUser(context.Background(), user(userA, tenantA, "  ", nil, at)))
		}},
		{"create-user-no-id", func(s auth.Store) string {
			return describeError(s.CreateUser(context.Background(), user("", tenantA, "Alice", nil, at)))
		}},
		{"create-role-reader", func(s auth.Store) string {
			return describeError(s.UpsertRole(context.Background(), role("rl_reader", tenantA, "reader", nil)))
		}},
		{"create-user", func(s auth.Store) string {
			err := s.CreateUser(context.Background(), user(userA, tenantA, "Alice", []string{"reader"}, at))
			return observe(err, userA)
		}},
		{"create-user-duplicate-username", func(s auth.Store) string {
			return describeError(s.CreateUser(context.Background(), user(userB, tenantA, "ALICE", nil, later)))
		}},
		{"create-user-duplicate-id", func(s auth.Store) string {
			return describeError(s.CreateUser(context.Background(), user(userA, tenantA, "Bob", nil, later)))
		}},
		{"create-user-second", func(s auth.Store) string {
			err := s.CreateUser(context.Background(), user(userB, tenantA, "Bob", nil, later))
			return observe(err, userB)
		}},
		{"create-user-other-tenant", func(s auth.Store) string {
			// The very same username is free in another tenant. | 同一个用户名在另一个租户里是空闲的。
			err := s.CreateUser(context.Background(), user("us_c", tenantB, "Alice", nil, at))
			return observe(err, "us_c")
		}},
		{"get-user", func(s auth.Store) string {
			got, err := s.GetUser(context.Background(), tenantA, userA)
			return observe(err, userSnapshot(got))
		}},
		{"get-user-other-tenant", func(s auth.Store) string {
			_, err := s.GetUser(context.Background(), tenantB, userA)
			return describeError(err)
		}},
		{"get-user-blank-tenant", func(s auth.Store) string {
			_, err := s.GetUser(context.Background(), "  ", userA)
			return describeError(err)
		}},
		{"get-user-by-username-folded", func(s auth.Store) string {
			got, err := s.GetUserByUsername(context.Background(), tenantA, "alice")
			name := ""
			if got != nil {
				name = got.Username
			}
			return observe(err, name)
		}},
		{"get-user-by-username-other-tenant", func(s auth.Store) string {
			_, err := s.GetUserByUsername(context.Background(), tenantB, "bob")
			return describeError(err)
		}},
		{"list-users", func(s auth.Store) string {
			got, err := s.ListUsers(context.Background(), tenantA)
			return observe(err, listSnapshot(got, userSnapshot))
		}},
		{"list-users-unknown-tenant", func(s auth.Store) string {
			_, err := s.ListUsers(context.Background(), "tn_missing")
			return describeError(err)
		}},
		{"list-users-blank-tenant", func(s auth.Store) string {
			_, err := s.ListUsers(context.Background(), " ")
			return describeError(err)
		}},
		{"update-user-rename", func(s auth.Store) string {
			err := s.UpdateUser(context.Background(), tenantA, user(userB, tenantA, "Bobby", nil, later))
			if err != nil {
				return describeError(err)
			}
			got, err := s.GetUser(context.Background(), tenantA, userB)
			return observe(err, userSnapshot(got))
		}},
		{"update-user-username-clash", func(s auth.Store) string {
			return describeError(s.UpdateUser(context.Background(), tenantA, user(userA, tenantA, "bobby", nil, later)))
		}},
		{"update-user-other-tenant", func(s auth.Store) string {
			return describeError(s.UpdateUser(context.Background(), tenantA, user(userA, tenantB, "Alice", nil, later)))
		}},
		{"update-user-missing", func(s auth.Store) string {
			return describeError(s.UpdateUser(context.Background(), tenantA, user("us_missing", tenantA, "Nobody", nil, later)))
		}},
		{"update-user-blank-tenant", func(s auth.Store) string {
			return describeError(s.UpdateUser(context.Background(), "  ", user(userA, "", "Alice", nil, later)))
		}},
		{"upsert-role", func(s auth.Store) string {
			err := s.UpsertRole(context.Background(), role(roleA, tenantA, "admin", []string{"user:create", "user:read"}))
			return observe(err, roleA)
		}},
		{"upsert-role-by-name-keeps-id", func(s auth.Store) string {
			// A new id with an existing name must not fork the role: the stored id wins. | 新 ID 加已存在的名称不得把角色分裂：以库中 ID 为准。
			err := s.UpsertRole(context.Background(), role(roleB, tenantA, "admin", []string{"user:read"}))
			if err != nil {
				return describeError(err)
			}
			got, err := s.GetRole(context.Background(), tenantA, roleA)
			return observe(err, roleSnapshot(got))
		}},
		{"upsert-role-other-id-is-gone", func(s auth.Store) string {
			_, err := s.GetRole(context.Background(), tenantA, roleB)
			return describeError(err)
		}},
		{"upsert-role-unknown-tenant", func(s auth.Store) string {
			return describeError(s.UpsertRole(context.Background(), role(roleB, "tn_missing", "admin", nil)))
		}},
		{"upsert-role-blank-tenant", func(s auth.Store) string {
			return describeError(s.UpsertRole(context.Background(), role(roleB, " ", "admin", nil)))
		}},
		{"upsert-role-blank-name", func(s auth.Store) string {
			return describeError(s.UpsertRole(context.Background(), role(roleB, tenantA, "  ", nil)))
		}},
		{"upsert-role-second", func(s auth.Store) string {
			err := s.UpsertRole(context.Background(), role(roleB, tenantA, "auditor", []string{"user:list", "user:read"}))
			return observe(err, roleB)
		}},
		{"upsert-role-other-tenant", func(s auth.Store) string {
			err := s.UpsertRole(context.Background(), role("rl_c", tenantB, "admin", []string{"user:read"}))
			return observe(err, "rl_c")
		}},
		{"get-role", func(s auth.Store) string {
			got, err := s.GetRole(context.Background(), tenantA, roleB)
			return observe(err, roleSnapshot(got))
		}},
		{"get-role-other-tenant", func(s auth.Store) string {
			_, err := s.GetRole(context.Background(), tenantB, roleA)
			return describeError(err)
		}},
		{"get-role-blank-tenant", func(s auth.Store) string {
			_, err := s.GetRole(context.Background(), "  ", roleA)
			return describeError(err)
		}},
		{"list-roles", func(s auth.Store) string {
			got, err := s.ListRoles(context.Background(), tenantA)
			return observe(err, listSnapshot(got, roleSnapshot))
		}},
		{"list-roles-unknown-tenant", func(s auth.Store) string {
			_, err := s.ListRoles(context.Background(), "tn_missing")
			return describeError(err)
		}},
		{"delete-role", func(s auth.Store) string {
			return describeError(s.DeleteRole(context.Background(), tenantA, roleA))
		}},
		{"delete-role-missing", func(s auth.Store) string {
			return describeError(s.DeleteRole(context.Background(), tenantA, roleA))
		}},
		{"delete-role-other-tenant", func(s auth.Store) string {
			return describeError(s.DeleteRole(context.Background(), tenantB, roleB))
		}},
		{"delete-role-blank-tenant", func(s auth.Store) string {
			return describeError(s.DeleteRole(context.Background(), "  ", roleA))
		}},
		{"list-roles-after-delete", func(s auth.Store) string {
			got, err := s.ListRoles(context.Background(), tenantA)
			return observe(err, listSnapshot(got, roleSnapshot))
		}},
		{"list-users-after-everything", func(s auth.Store) string {
			got, err := s.ListUsers(context.Background(), tenantA)
			return observe(err, listSnapshot(got, userSnapshot))
		}},
	}
}

// describeError maps an error onto the sentinel the contract promises. | describeError 把错误映射到契约承诺的哨兵。
//
// Classifying by sentinel rather than by message is what makes the journal of the
// two implementations comparable at all. | 按哨兵而不是按报文分类，两份实现的日志才具有可比性。
func describeError(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, auth.ErrNotFound):
		return "err:not-found"
	case errors.Is(err, auth.ErrConflict):
		return "err:conflict"
	case errors.Is(err, auth.ErrInvalidInput):
		return "err:invalid-input"
	case errors.Is(err, auth.ErrTenantRequired):
		return "err:tenant-required"
	default:
		return "err:" + err.Error()
	}
}

// assertJournalsEqual compares two journals entry by entry. | assertJournalsEqual 逐条比较两份日志。
func assertJournalsEqual(t *testing.T, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("journal length = %d, want %d | 日志长度 = %d，期望 %d", len(got), len(want), len(got), len(want))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("step %d\n  memory | 内存: %s\n  gorm:       %s", i, want[i], got[i])
		}
	}
}

// tenantView is the comparable projection of a tenant. | tenantView 是租户的可比较投影。
type tenantView struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// tenantSnapshot renders a tenant for the journal. | tenantSnapshot 为日志渲染租户。
func tenantSnapshot(target *auth.Tenant) string {
	if target == nil {
		return "nil"
	}
	return marshalView(tenantView{
		ID:        target.ID,
		Slug:      target.Slug,
		Name:      target.Name,
		Status:    string(target.Status),
		CreatedAt: target.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
}

// userView is the comparable projection of a user. | userView 是用户的可比较投影。
type userView struct {
	ID           string   `json:"id"`
	TenantID     string   `json:"tenant_id"`
	Username     string   `json:"username"`
	Email        string   `json:"email"`
	DisplayName  string   `json:"display_name"`
	PasswordHash string   `json:"password_hash"`
	Status       string   `json:"status"`
	Roles        []string `json:"roles"`
	SecKeyLen    int      `json:"sec_key_len"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

// userSnapshot renders a user for the journal. | userSnapshot 为日志渲染用户。
func userSnapshot(target *auth.User) string {
	if target == nil {
		return "nil"
	}
	return marshalView(userView{
		ID:           target.ID,
		TenantID:     target.TenantID,
		Username:     target.Username,
		Email:        target.Email,
		DisplayName:  target.DisplayName,
		PasswordHash: target.PasswordHash,
		Status:       string(target.Status),
		Roles:        emptyToNil(target.Roles),
		SecKeyLen:    len(target.EncryptedSecKey),
		CreatedAt:    target.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:    target.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
}

// roleView is the comparable projection of a role. | roleView 是角色的可比较投影。
type roleView struct {
	ID          string   `json:"id"`
	TenantID    string   `json:"tenant_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// roleSnapshot renders a role for the journal. | roleSnapshot 为日志渲染角色。
func roleSnapshot(target *auth.Role) string {
	if target == nil {
		return "nil"
	}
	return marshalView(roleView{
		ID:          target.ID,
		TenantID:    target.TenantID,
		Name:        target.Name,
		Description: target.Description,
		Permissions: emptyToNil(target.Permissions),
	})
}

// listSnapshot renders a list of entities for the journal. | listSnapshot 为日志渲染实体列表。
func listSnapshot[T any](items []*T, render func(*T) string) string {
	if len(items) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, render(item))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// marshalView renders a projection through jsonx, which is what the rest of the
// repository serialises with. | marshalView 通过 jsonx 渲染投影，与仓库其余部分使用的序列化层一致。
func marshalView(v any) string {
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return "marshal-error"
	}
	return string(raw)
}

// emptyToNil normalises an empty slice to nil, so "no roles" and "an empty role
// list" cannot be reported as a difference between the implementations. | emptyToNil 把空切片归一为 nil，
// 使「没有角色」与「空角色列表」不会被报告为两个实现之间的差异。
func emptyToNil(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}
