package gormstore

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/shibingli/yd/security/auth"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/log"
)

// roleUpsertAttempts bounds the upsert retries. | roleUpsertAttempts 限定 upsert 的重试次数。
//
// Two attempts are enough: the only way to lose is a concurrent insert of the very
// row being written, and the retry then finds it with the SELECT and updates it.
// The retry must open a fresh transaction because PostgreSQL aborts the whole
// transaction after a failed statement. | 两次就够：唯一会输的情形是有人并发插入了正在写的这一行，
// 重试时的 SELECT 会找到它并改走 UPDATE。重试必须开新事务，因为 PostgreSQL 在语句失败后会中止整个事务。
const roleUpsertAttempts = 2

// UpsertRole stores a role, replacing the one with the same id or, inside the
// tenant, the one with the same name. | UpsertRole 存入角色，覆盖同 ID 或同租户内同名的既有角色。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_tenants WHERE id = ?
//	SELECT * FROM yd_auth_roles WHERE tenant_id = ? AND name = ? ORDER BY yd_auth_roles.id LIMIT 1
//	SELECT * FROM yd_auth_roles WHERE tenant_id = ? AND id = ? ORDER BY yd_auth_roles.id LIMIT 1
//	UPDATE yd_auth_roles SET name = ?, description = ? WHERE tenant_id = ? AND id = ?
//	INSERT INTO yd_auth_roles (...) VALUES (...)                                  -- only when absent
//	DELETE FROM yd_auth_role_permissions WHERE tenant_id = ? AND role_id = ?
//	INSERT INTO yd_auth_role_permissions (tenant_id, role_id, permission) VALUES ...
//
// The role row and its permission rows are written in one transaction, so a role
// can never be observed with half of its capabilities. | 角色行与它的权限行在同一事务内写入，
// 因此永远不会观察到只带一半能力的角色。
func (s *Store) UpsertRole(ctx context.Context, r *auth.Role) error {
	if r == nil || r.ID == "" || strings.TrimSpace(r.Name) == "" {
		return errs.Detail(auth.ErrInvalidInput, "role requires an id and a name | 角色必须带 ID 与名称")
	}
	if strings.TrimSpace(r.TenantID) == "" {
		return auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	name := strings.TrimSpace(r.Name)
	var err error
	for attempt := 0; ; attempt++ {
		var raced bool
		raced, err = s.upsertRole(ctx, r.TenantID, r.ID, name, r.Description, r.Permissions)
		if !raced {
			return err
		}
		if attempt+1 >= roleUpsertAttempts {
			// Two writers aimed at the same new role: report the conflict instead of
			// silently overwriting the winner. | 两个写入者瞄准同一新角色：报冲突，而不是静默覆盖胜出者。
			s.log.Warn("the unique index kept losing the role upsert | 唯一索引在角色 upsert 上持续竞争失败",
				log.Str("tenant_id", r.TenantID), log.Str("role_id", r.ID))
			return errs.WrapDetail(err, auth.ErrConflict, "the role was modified concurrently | 角色被并发修改")
		}
	}
}

// upsertRole performs one upsert attempt in its own transaction. | upsertRole 在自己的事务内执行一次 upsert 尝试。
//
// It reports raced=true when a concurrent writer inserted the row first, in which
// case nothing was written and the caller retries. | 并发写入者抢先插入该行时返回 raced=true，
// 此时没有写入任何内容，由调用方重试。
func (s *Store) upsertRole(ctx context.Context, tenantID, roleID, name, description string, perms []string) (bool, error) {
	raced := false
	err := s.transaction(ctx, []string{tableRoles, tableRolePermissions}, func(tx *gorm.DB) error {
		known, err := existsTenant(tx, tenantID)
		if err != nil {
			return err
		}
		if !known {
			return auth.ErrNotFound
		}
		id := roleID
		var holder roleRow
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND name = ?", tenantID, name).First(&holder).Error
		switch {
		case err == nil:
			if holder.ID != roleID {
				// Same name, different id: the stored id wins, so a rename cannot fork
				// a role — and the unique index on (tenant_id, name) makes that one role,
				// not two aliases for it. | 同名不同 ID：以库中 ID 为准，避免重命名把角色分裂成两个；
				// (tenant_id, name) 上的唯一索引保证这样的角色只有一个，而不是它的两个别名。
				id = holder.ID
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
		default:
			return err
		}
		var existing roleRow
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenantID, id).First(&existing).Error
		switch {
		case err == nil:
			// The name is the tenant's unique role key, so an update that clashes on it
			// means a concurrent writer created another role with this name: retry, so the
			// next attempt resolves the holder and updates that one instead. | 角色名是租户内的唯一键，
			// 因此这里的更新若在名称上撞车，说明有并发写入者用同一个名字建了另一个角色：重试，
			// 下一次尝试会解析出持有者并改去更新它。
			if err := tx.Model(&roleRow{}).Where("tenant_id = ? AND id = ?", tenantID, id).
				Updates(map[string]any{"name": name, "description": description}).Error; err != nil {
				raced = isDuplicate(err)
				return err
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			row := roleRow{ID: id, TenantID: tenantID, Name: name, Description: description}
			if err := tx.Create(&row).Error; err != nil {
				// Losing the insert race is not a failure: nothing was written, so the
				// caller retries with a fresh transaction and finds the winner's row. | 插入竞争失败不算错误：
				// 没有写入任何内容，调用方用新事务重试即可找到胜出者的行。
				raced = isDuplicate(err)
				return err
			}
		default:
			return err
		}
		return replacePermissions(tx, tenantID, id, perms)
	})
	return raced, err
}

// GetRole returns the role of that tenant. | GetRole 返回该租户下的角色。
//
// SQL | SQL:
//
//	SELECT * FROM yd_auth_roles WHERE tenant_id = ? AND id = ? ORDER BY yd_auth_roles.id LIMIT 1
//	SELECT permission FROM yd_auth_role_permissions WHERE tenant_id = ? AND role_id = ? ORDER BY permission ASC
//
// Permissions come back sorted and without duplicates: the association table is a
// set keyed by (tenant_id, role_id, permission), which is exactly what the unique
// index says. | 权限按排序返回且无重复：关联表是以 (tenant_id, role_id, permission) 为键的集合，
// 这正是唯一索引所声明的。
func (s *Store) GetRole(ctx context.Context, tenantID, roleID string) (*auth.Role, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	var row roleRow
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, roleID).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	perms, err := readPermissions(s.db.WithContext(ctx), tenantID, roleID)
	if err != nil {
		return nil, err
	}
	return roleFromRow(&row, perms), nil
}

// ListRoles returns every role of the tenant ordered by name. | ListRoles 按名称返回该租户的全部角色。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_tenants WHERE id = ?
//	SELECT * FROM yd_auth_roles WHERE tenant_id = ? ORDER BY name ASC, id ASC
//	SELECT role_id, permission FROM yd_auth_role_permissions WHERE tenant_id = ? ORDER BY role_id ASC, permission ASC
//
// The permissions of every role are read with one statement and grouped in
// memory, so listing N roles costs three round trips instead of N+1. | 全部角色的权限用一条语句读出、
// 在内存中分组，因此列出 N 个角色只花费三次往返，而不是 N+1 次。
func (s *Store) ListRoles(ctx context.Context, tenantID string) ([]*auth.Role, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	known, err := existsTenant(s.db.WithContext(ctx), tenantID)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, auth.ErrNotFound
	}
	var rows []roleRow
	if err := s.db.WithContext(ctx).Where("tenant_id = ?", tenantID).
		Order("name ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	var permRows []rolePermissionRow
	if err := s.db.WithContext(ctx).Where("tenant_id = ?", tenantID).
		Order("role_id ASC, permission ASC").Find(&permRows).Error; err != nil {
		return nil, err
	}
	byRole := make(map[string][]string, len(rows))
	for i := range permRows {
		byRole[permRows[i].RoleID] = append(byRole[permRows[i].RoleID], permRows[i].Permission)
	}
	out := make([]*auth.Role, 0, len(rows))
	for i := range rows {
		out = append(out, roleFromRow(&rows[i], byRole[rows[i].ID]))
	}
	return out, nil
}

// DeleteRole removes the role of that tenant together with its permissions. | DeleteRole 连同权限一起删除该租户下的角色。
//
// SQL | SQL:
//
//	SELECT * FROM yd_auth_roles WHERE tenant_id = ? AND id = ? ORDER BY yd_auth_roles.id LIMIT 1
//	DELETE FROM yd_auth_role_permissions WHERE tenant_id = ? AND role_id = ?
//	DELETE FROM yd_auth_roles WHERE tenant_id = ? AND id = ?
//
// Dropping the association rows first leaves no orphan behind, and the name is
// freed by the same statement that removes the role: a stale delete can therefore
// never drop a role that was re-created under the same name in the meantime. | 先删关联行，不留孤儿；
// 名字随删除角色的那条语句一起释放，因此迟到的删除永远不会清掉其间按同名重建的角色。
func (s *Store) DeleteRole(ctx context.Context, tenantID, roleID string) error {
	if strings.TrimSpace(tenantID) == "" {
		return auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	return s.transaction(ctx, []string{tableRolePermissions, tableUserTenants, tableRoles}, func(tx *gorm.DB) error {
		var row roleRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenantID, roleID).First(&row).Error; err != nil {
			return notFound(err)
		}
		if err := tx.Where("tenant_id = ? AND role_id = ?", tenantID, roleID).Delete(&rolePermissionRow{}).Error; err != nil {
			return err
		}
		// Memberships store role names as JSON rather than rows, so the name has to be detached here
		// in the same transaction; otherwise a failure after the delete would leave invisible debt. |
		// 成员关系用 JSON 而非行保存角色名，因此必须在这里的同一事务内摘掉该名字；否则删除后的失败会留下
		// 看不见的债务。
		if err := detachRoleName(tx, tenantID, row.Name); err != nil {
			return err
		}
		return tx.Where("tenant_id = ? AND id = ?", tenantID, roleID).Delete(&roleRow{}).Error
	})
}

// detachRoleName removes one role name from every membership of the tenant. |
// detachRoleName 从租户的每条成员关系中移除一个角色名。
func detachRoleName(tx *gorm.DB, tenantID, name string) error {
	var memberships []userTenantRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ?", tenantID).Order("user_id ASC").Find(&memberships).Error; err != nil {
		return err
	}
	for i := range memberships {
		roles, err := membershipFromRow(&memberships[i])
		if err != nil {
			return err
		}
		kept := make([]string, 0, len(roles))
		for _, role := range roles {
			if role != name {
				kept = append(kept, role)
			}
		}
		if len(kept) == len(roles) {
			continue
		}
		encoded, err := encodeRoles(kept)
		if err != nil {
			return err
		}
		if err := tx.Model(&userTenantRow{}).
			Where("tenant_id = ? AND user_id = ?", tenantID, memberships[i].UserID).
			Updates(map[string]any{"roles": encoded, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
	}
	return nil
}

// readPermissions returns the sorted permission set of one role. | readPermissions 返回某个角色排序后的权限集合。
func readPermissions(tx *gorm.DB, tenantID, roleID string) ([]string, error) {
	var rows []rolePermissionRow
	if err := tx.Where("tenant_id = ? AND role_id = ?", tenantID, roleID).
		Order("permission ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return permissionNames(rows), nil
}

// permissionNames projects rows onto their permission strings, nil when empty. | permissionNames 把行投影为权限字符串，空时返回 nil。
//
// Empty becomes nil on purpose: the memory implementation normalises an empty
// permission list the same way, which keeps the two implementations comparable. | 空集合归一为 nil
// 是刻意的：内存实现同样把空权限列表归一为 nil，两个实现因此可以逐字段比较。
func permissionNames(rows []rolePermissionRow) []string {
	if len(rows) == 0 {
		return nil
	}
	out := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].Permission)
	}
	return out
}

// replacePermissions rewrites the permission set of one role inside the caller's
// transaction. | replacePermissions 在调用方的事务内重写某个角色的权限集合。
//
// Delete-then-insert is the set semantic the unique index describes: granting the
// same capability twice stores it once, and a role can never keep a capability
// that the caller dropped. Duplicates inside the incoming slice are collapsed for
// the same reason — the association table is a set, not a list. | 先删后插正是唯一索引所描述的集合语义：
// 同一能力授权两次只会存一份，角色也永远不会留下调用方已撤下的能力。传入切片里的重复项同样被折叠 ——
// 关联表是集合，不是列表。
func replacePermissions(tx *gorm.DB, tenantID, roleID string, perms []string) error {
	if err := tx.Where("tenant_id = ? AND role_id = ?", tenantID, roleID).
		Delete(&rolePermissionRow{}).Error; err != nil {
		return err
	}
	if len(perms) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(perms))
	rows := make([]rolePermissionRow, 0, len(perms))
	for _, perm := range perms {
		if _, dup := seen[perm]; dup {
			continue
		}
		seen[perm] = struct{}{}
		rows = append(rows, rolePermissionRow{TenantID: tenantID, RoleID: roleID, Permission: perm})
	}
	if len(rows) == 0 {
		return nil
	}
	if err := tx.Create(&rows).Error; err != nil {
		if isDuplicate(err) {
			return errs.WrapDetail(err, auth.ErrConflict, "the role permissions changed concurrently | 角色权限被并发修改")
		}
		return err
	}
	return nil
}
