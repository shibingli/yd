package gormstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shibingli/yd/security/auth"
	"gorm.io/gorm/schema"
)

// TestAuthModelsHaveNoDatabaseRelationships asserts that GORM infers no association from any auth
// row type. An association would make AutoMigrate create a foreign key, which the project forbids:
// tenant, user and role relations are validated and maintained by the store itself. |
// TestAuthModelsHaveNoDatabaseRelationships 断言 GORM 没有从任何鉴权行类型推断出关联。
// 关联会让 AutoMigrate 建外键，而项目禁止外键：租户、用户与角色关系由存储层自行校验与维护。
func TestAuthModelsHaveNoDatabaseRelationships(t *testing.T) {
	models := []any{&tenantRow{}, &userRow{}, &userTenantRow{}, &roleRow{}, &rolePermissionRow{}}
	cache := &sync.Map{}
	for _, model := range models {
		parsed, err := schema.Parse(model, cache, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("%T: schema parse: %v", model, err)
		}
		if n := len(parsed.Relationships.Relations); n != 0 {
			t.Errorf("%T declares %d GORM relationship(s): relations must be maintained in application code, not by the database | %T 声明了 %d 个 GORM 关系：关系必须由应用代码维护，不能交给数据库",
				model, n, model, n)
		}
	}
}

// TestDeleteRoleDetachesMembershipName asserts that deleting a role strips its name from the JSON
// memberships in the same transaction. | TestDeleteRoleDetachesMembershipName 断言删除角色时在同一事务内
// 从 JSON 成员关系中摘掉该角色名。
func TestDeleteRoleDetachesMembershipName(t *testing.T) {
	store, _ := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	tenant := &auth.Tenant{ID: "tn_cleanup", Slug: "cleanup", Name: "Cleanup", Status: auth.StatusActive, CreatedAt: now}
	if err := store.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	role := &auth.Role{ID: "rl_cleanup", TenantID: tenant.ID, Name: "owner"}
	if err := store.UpsertRole(ctx, role); err != nil {
		t.Fatalf("upsert role: %v", err)
	}
	user := &auth.User{
		ID: "usr_cleanup", TenantID: tenant.ID, HomeTenantID: tenant.ID, DefaultTenantID: tenant.ID,
		Username: "cleanup", Status: auth.StatusActive, Roles: []string{"owner"}, CreatedAt: now,
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.DeleteRole(ctx, tenant.ID, role.ID); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	loaded, err := store.GetUser(ctx, tenant.ID, user.ID)
	if err != nil {
		t.Fatalf("load user: %v", err)
	}
	if len(loaded.Roles) != 0 {
		t.Fatalf("membership roles = %v, want empty", loaded.Roles)
	}
}

// TestUpdateUserRejectsADeletedRole proves a stale assignment cannot resurrect a role after its
// deletion: the store revalidates role names while holding the same locks as DeleteRole. |
// TestUpdateUserRejectsADeletedRole 证明过期分配无法在角色删除后把它复活：存储层持有与 DeleteRole
// 相同的锁重新校验角色名。
func TestUpdateUserRejectsADeletedRole(t *testing.T) {
	store, _ := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	tenant := &auth.Tenant{ID: "tn_stale", Slug: "stale", Name: "Stale", Status: auth.StatusActive, CreatedAt: now}
	if err := store.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	role := &auth.Role{ID: "rl_stale", TenantID: tenant.ID, Name: "owner"}
	if err := store.UpsertRole(ctx, role); err != nil {
		t.Fatalf("upsert role: %v", err)
	}
	user := &auth.User{
		ID: "usr_stale", TenantID: tenant.ID, HomeTenantID: tenant.ID, DefaultTenantID: tenant.ID,
		Username: "stale", Status: auth.StatusActive, Roles: []string{role.Name}, CreatedAt: now,
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.DeleteRole(ctx, tenant.ID, role.ID); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	user.Roles = []string{role.Name}
	if err := store.UpdateUser(ctx, tenant.ID, user); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("UpdateUser after role deletion = %v, want auth.ErrNotFound", err)
	}
}
