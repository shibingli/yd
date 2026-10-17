package auth

import (
	"errors"
	"strings"
	"testing"
)

// TestTenantLifecycle covers creating, reading and the list ordering of tenants. | TestTenantLifecycle 覆盖租户的创建、读取与列表排序。
func TestTenantLifecycle(t *testing.T) {
	f := newFixture(t)
	tn, err := f.svc.CreateTenant(testCtx, "acme", "Acme")
	requireNoError(t, err, "CreateTenant")
	if tn.Status != StatusActive {
		t.Fatalf("new tenant status want %q, got %q | 新租户状态期望 %q，实际 %q", StatusActive, tn.Status, StatusActive, tn.Status)
	}
	if !strings.HasPrefix(tn.ID, tenantIDPrefix) {
		t.Fatalf("tenant id %q does not carry the %q prefix | 租户 ID %q 未带 %q 前缀", tn.ID, tenantIDPrefix, tn.ID, tenantIDPrefix)
	}
	if tn.CreatedAt.IsZero() {
		t.Fatal("CreatedAt is zero | CreatedAt 为零值")
	}
	// The fixture clock is frozen, so the creation instant is exactly testStart. | 夹具时钟冻结，因此创建时刻恰为 testStart。
	if !tn.CreatedAt.Equal(testStart) {
		t.Fatalf("CreatedAt want %v, got %v | CreatedAt 期望 %v，实际 %v", testStart, tn.CreatedAt, testStart, tn.CreatedAt)
	}

	got, err := f.svc.GetTenant(testCtx, tn.ID)
	requireNoError(t, err, "GetTenant")
	if got.Slug != "acme" || got.Name != "Acme" {
		t.Fatalf("GetTenant returned %+v | GetTenant 返回 %+v", got, got)
	}

	all, err := f.svc.ListTenants(testCtx)
	requireNoError(t, err, "ListTenants")
	if len(all) != 3 {
		t.Fatalf("ListTenants want 3 tenants, got %d | ListTenants 期望 3 个租户，实际 %d", len(all), len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].CreatedAt.After(all[i].CreatedAt) {
			t.Fatalf("ListTenants is not ordered by creation time: %v then %v | ListTenants 未按创建时间排序：%v 之后为 %v",
				all[i-1], all[i], all[i-1], all[i])
		}
	}
}

// TestTenantInputValidation checks an empty slug, an empty name, a duplicate slug and an unknown tenant. | TestTenantInputValidation 校验空 slug、空名称、重复 slug 与未知租户。
func TestTenantInputValidation(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.CreateTenant(testCtx, "  ", "Name"); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("empty slug want ErrTenantRequired, got %v | 空 slug 期望 ErrTenantRequired，实际 %v", err, err)
	}
	if _, err := f.svc.CreateTenant(testCtx, "slug", " "); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("empty name want ErrTenantRequired, got %v | 空名称期望 ErrTenantRequired，实际 %v", err, err)
	}
	if _, err := f.svc.CreateTenant(testCtx, "tenant-a", "Again"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate slug want ErrConflict, got %v | 重复 slug 期望 ErrConflict，实际 %v", err, err)
	}
	if _, err := f.svc.CreateTenant(testCtx, "TENANT-A", "Case"); !errors.Is(err, ErrConflict) {
		t.Fatalf("case-variant slug want ErrConflict, got %v | 大小写不同的重复 slug 期望 ErrConflict，实际 %v", err, err)
	}
	if _, err := f.svc.GetTenant(testCtx, "tn_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown tenant want ErrNotFound, got %v | 未知租户期望 ErrNotFound，实际 %v", err, err)
	}
	if _, err := f.svc.GetTenant(testCtx, "  "); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("empty tenant id want ErrTenantRequired, got %v | 空租户 ID 期望 ErrTenantRequired，实际 %v", err, err)
	}
	if _, err := f.svc.ListUsers(testCtx, ""); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("ListUsers without tenant want ErrTenantRequired, got %v | 未指定租户的 ListUsers 期望 ErrTenantRequired，实际 %v", err, err)
	}
	// A tenant that was never created must not be reachable through any user call. | 从未创建的租户不得通过任何用户调用可达。
	if _, err := f.svc.ListUsers(testCtx, "tn_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListUsers of unknown tenant want ErrNotFound, got %v | 未知租户的 ListUsers 期望 ErrNotFound，实际 %v", err, err)
	}
}

// TestCallersCannotMutateStoredEntities checks that a copy read back cannot mutate the stored state. | TestCallersCannotMutateStoredEntities 校验读出的副本无法改动库中状态。
func TestCallersCannotMutateStoredEntities(t *testing.T) {
	f := newFixture(t)
	stored, err := f.store.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	stored.Username = "tampered"
	stored.Roles = append(stored.Roles, "admin")
	stored.EncryptedSecKey[0] ^= 0xff

	again, err := f.store.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser again")
	if again.Username != f.member.Username {
		t.Fatalf("stored username was mutated: %q | 库中用户名被改动：%q", again.Username, again.Username)
	}
	if len(again.Roles) != len(f.member.Roles) {
		t.Fatalf("stored roles were mutated: %v | 库中角色被改动：%v", again.Roles, again.Roles)
	}
	if again.EncryptedSecKey[0] != f.member.EncryptedSecKey[0] {
		t.Fatal("stored sec_key ciphertext was mutated | 库中 sec_key 密文被改动")
	}
}
