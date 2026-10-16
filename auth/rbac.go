package auth

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/logx"
)

// Permission is a capability token of the form "resource:action". | Permission 是形如 "resource:action" 的能力标识。
type Permission string

// Role groups permissions under a name inside one tenant. | Role 在单个租户内按名称聚合一组权限。
//
// Users reference roles by name (User.Roles), which keeps assignment readable and
// survives a role id being regenerated. | 用户按名称引用角色（User.Roles），这样分配关系易读，且角色 ID 重建后依然有效。
type Role struct {
	// ID is the generated identifier, prefixed with "rl_". | ID 是生成的标识符，前缀为 "rl_"。
	ID string
	// TenantID is the owning tenant. | TenantID 是所属租户。
	TenantID string
	// Name is the unique role name inside the tenant. | Name 是租户内唯一的角色名。
	Name string
	// Description is the optional human-readable note. | Description 是可选的说明文字。
	Description string
	// Permissions lists the granted capabilities. | Permissions 列出被授予的能力。
	Permissions []string
}

// roleCache caches the permission list of every role of a tenant. | roleCache 缓存每个租户下全部角色的权限列表。
//
// HasPermission and PermissionsOf take no context, so they cannot query the store
// on demand; the cache is therefore loaded lazily through a background context,
// invalidated by every role mutation of the Service, and refreshed wholesale by
// ListRoles. | HasPermission 与 PermissionsOf 没有上下文参数，无法按需查询 Store；因此本缓存
// 通过后台上下文惰性加载，由 Service 的每次角色变更失效，并由 ListRoles 整体刷新。
type roleCache struct {
	// store is the read-through source of the cache. | store 是缓存的读透来源。
	store Store
	// loaded marks the tenants whose roles have been read from the store. | loaded 标记已经从 Store 读取过角色的租户。
	loaded map[string]struct{}
	// perms maps tenant id to role name to permissions. | perms 把租户 ID 映射到角色名再到权限列表。
	perms map[string]map[string][]Permission
	// mu guards the three fields below and is kept last to shrink the pointer
	// bytes the collector has to scan. | mu 保护下面三个字段，并置于最后以减少回收器需要扫描的指针字节数。
	mu sync.RWMutex
}

// newRoleCache returns an empty cache reading through store. | newRoleCache 返回一个以 store 为读透来源的空缓存。
func newRoleCache(store Store) *roleCache {
	return &roleCache{
		store:  store,
		loaded: make(map[string]struct{}),
		perms:  make(map[string]map[string][]Permission),
	}
}

// ensure loads the roles of a tenant from the store at most once per invalidation. | ensure 每个失效周期内最多从 Store 加载一次该租户的角色。
func (c *roleCache) ensure(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return nil
	}
	c.mu.RLock()
	_, ok := c.loaded[tenantID]
	c.mu.RUnlock()
	if ok {
		return nil
	}
	roles, err := c.store.ListRoles(ctx, tenantID)
	if err != nil {
		return err
	}
	c.refresh(tenantID, roles)
	return nil
}

// refresh replaces the cached roles of one tenant. | refresh 覆盖某个租户的缓存角色。
func (c *roleCache) refresh(tenantID string, roles []*Role) {
	if tenantID == "" {
		return
	}
	perms := make(map[string][]Permission, len(roles))
	for _, r := range roles {
		if r == nil || r.Name == "" {
			continue
		}
		perms[r.Name] = permissionsFrom(r.Permissions)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loaded[tenantID] = struct{}{}
	c.perms[tenantID] = perms
}

// set records one role without a store round trip. | set 记录单个角色，无需访问 Store。
func (c *roleCache) set(tenantID, name string, perms []Permission) {
	if tenantID == "" || name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	byName := c.perms[tenantID]
	if byName == nil {
		byName = make(map[string][]Permission)
		c.perms[tenantID] = byName
	}
	byName[name] = perms
}

// invalidate drops the cached roles of one tenant. | invalidate 丢弃某个租户的缓存角色。
func (c *roleCache) invalidate(tenantID string) {
	if tenantID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.loaded, tenantID)
	delete(c.perms, tenantID)
}

// lookup returns the cached permissions of one role and reports whether it is known. | lookup 返回某个角色的缓存权限，并报告该角色是否已知。
func (c *roleCache) lookup(tenantID, name string) ([]Permission, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	perms, ok := c.perms[tenantID][name]
	return perms, ok
}

// exists reports whether the tenant knows a role with that name. | exists 报告该租户是否存在同名角色。
//
// A Store failure is propagated, and the caller fails closed. | Store 失败会向上传播，调用方据此失败关闭。
func (c *roleCache) exists(ctx context.Context, tenantID, name string) (bool, error) {
	if err := c.ensure(ctx, tenantID); err != nil {
		return false, err
	}
	_, ok := c.lookup(tenantID, name)
	return ok, nil
}

// UpsertRole creates or updates a role of the tenant. | UpsertRole 创建或更新租户内的角色。
//
// A role id may be left empty, in which case one is generated — unless the tenant
// already holds a role of that name, whose id is then reused, so that the returned
// record stays addressable by GetRole and DeleteRole. A TenantID on r that names
// another tenant is reported as ErrNotFound, and so is an unknown tenant. | 角色 ID 可以留空，
// 此时会自动生成——除非租户内已存在同名角色，那样就复用它的 ID，使返回的记录仍可被
// GetRole 与 DeleteRole 按 ID 找到。r 上的 TenantID 指向其它租户时返回 ErrNotFound，租户未知时同样返回 ErrNotFound。
func (s *Service) UpsertRole(ctx context.Context, tenantID string, r *Role) (*Role, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	if r == nil || strings.TrimSpace(r.Name) == "" {
		return nil, errx.Detail(ErrInvalidInput, "role name must not be empty | 角色名不得为空")
	}
	if r.TenantID != "" && r.TenantID != tenantID {
		return nil, ErrNotFound
	}
	if _, err := s.store.GetTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	stored := &Role{
		ID:          r.ID,
		TenantID:    tenantID,
		Name:        strings.TrimSpace(r.Name),
		Description: strings.TrimSpace(r.Description),
		Permissions: permissionNames(permissionsFrom(r.Permissions)),
	}
	if stored.ID == "" {
		id, err := s.existingRoleID(ctx, tenantID, stored.Name)
		if err != nil {
			return nil, err
		}
		if id == "" {
			if id, err = newID(roleIDPrefix); err != nil {
				return nil, err
			}
		}
		stored.ID = id
	}
	if err := s.store.UpsertRole(ctx, stored); err != nil {
		return nil, err
	}
	// The cache is updated with the resolved permissions, never with a stale list. | 缓存写入的是归一化后的权限，绝不是过期列表。
	s.roles.set(tenantID, stored.Name, permissionsFrom(stored.Permissions))
	return cloneRole(stored), nil
}

// existingRoleID returns the id the tenant already uses for name, or "" when the
// name is free. | existingRoleID 返回该租户为 name 已使用的 ID；名称未被占用时返回 ""。
//
// The lookup goes to the store rather than to the permission cache, because the
// cache may be cold or stale and an id is a durable fact. | 该查询直接走 Store 而不是权限缓存，
// 因为缓存可能未加载或已过期，而 ID 是持久事实。
func (s *Service) existingRoleID(ctx context.Context, tenantID, name string) (string, error) {
	roles, err := s.store.ListRoles(ctx, tenantID)
	if err != nil {
		return "", err
	}
	for _, r := range roles {
		if r != nil && r.Name == name {
			return r.ID, nil
		}
	}
	return "", nil
}

// ListRoles returns every role of the tenant and refreshes the permission cache. | ListRoles 返回该租户的全部角色并刷新权限缓存。
func (s *Service) ListRoles(ctx context.Context, tenantID string) ([]*Role, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	roles, err := s.store.ListRoles(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	s.roles.refresh(tenantID, roles)
	return roles, nil
}

// DeleteRole removes a role and strips its name from every user of the tenant. | DeleteRole 删除角色，并从该租户的所有用户上摘掉该角色名。
//
// Leaving a dangling name on a user would be safe — it grants nothing — but it
// would also be invisible debt, so the cleanup happens here instead. | 把悬空角色名留在用户上虽然安全
// （不授予任何权限），却是一笔看不见的债，因此在这里直接清理。
func (s *Service) DeleteRole(ctx context.Context, tenantID, roleID string) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	if strings.TrimSpace(roleID) == "" {
		return errx.Detail(ErrNotFound, "empty role id | 角色 ID 为空")
	}
	role, err := s.store.GetRole(ctx, tenantID, roleID)
	if err != nil {
		return err
	}
	if err := s.store.DeleteRole(ctx, tenantID, roleID); err != nil {
		return err
	}
	s.roles.invalidate(tenantID)
	users, err := s.store.ListUsers(ctx, tenantID)
	if err != nil {
		return err
	}
	for _, u := range users {
		names, changed := withoutRole(u.Roles, role.Name)
		if !changed {
			continue
		}
		u.Roles = names
		u.UpdatedAt = s.now()
		if err := s.store.UpdateUser(ctx, tenantID, u); err != nil {
			return err
		}
	}
	return nil
}

// SetUserRoles replaces the role names of a user. | SetUserRoles 覆盖用户的角色名列表。
//
// Every name must exist inside the tenant, otherwise ErrNotFound is returned and
// nothing changes; the user must belong to the tenant for the same reason. | 每个名字都必须
// 存在于该租户内，否则返回 ErrNotFound 且不做任何改动；出于同样的理由，用户也必须属于该租户。
func (s *Service) SetUserRoles(ctx context.Context, tenantID, userID string, roles []string) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	names, err := s.resolveRoles(ctx, tenantID, roles)
	if err != nil {
		return err
	}
	u, err := s.store.GetUser(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	u.Roles = names
	u.UpdatedAt = s.now()
	return s.store.UpdateUser(ctx, tenantID, u)
}

// RolesOf returns the role names of a user as a sorted copy. | RolesOf 以排序后的副本返回用户的角色名。
func (s *Service) RolesOf(u *User) []string {
	if u == nil || len(u.Roles) == 0 {
		return nil
	}
	out := append([]string(nil), u.Roles...)
	sort.Strings(out)
	return out
}

// PermissionsOf returns the union of the permissions granted by the roles of u. | PermissionsOf 返回 u 的角色所授予权限的并集。
//
// A nil user, a user that is not active and a user without roles all yield an empty
// result, so a disabled account keeps no capability at all. | u 为空、u 非激活或 u 没有角色时都返回空结果，
// 因此被禁用的账号不保留任何能力。
func (s *Service) PermissionsOf(u *User) []Permission {
	if u == nil || u.Status != StatusActive || len(u.Roles) == 0 {
		return nil
	}
	s.loadRoles(u.TenantID)
	seen := make(map[Permission]struct{}, len(u.Roles))
	out := make([]Permission, 0, len(u.Roles))
	for _, name := range u.Roles {
		perms, _ := s.roles.lookup(u.TenantID, name)
		for _, p := range perms {
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// HasPermission reports whether u holds p through one of its roles. | HasPermission 报告 u 是否通过其角色持有 p。
//
// An empty permission, an unknown role and a disabled or unknown user all answer
// false, and the answer never depends on the existence of a resource. | 权限为空、角色未知、
// 用户被禁用或用户未知时一律返回 false，且该判定从不依赖资源是否存在。
func (s *Service) HasPermission(u *User, p Permission) bool {
	if p == "" {
		return false
	}
	for _, granted := range s.PermissionsOf(u) {
		if granted == p {
			return true
		}
	}
	return false
}

// loadRoles fills the role cache of a tenant once, through a background context
// because the RBAC decision helpers carry none. | loadRoles 通过后台上下文为某个租户填充一次角色缓存，
// 因为 RBAC 判定辅助函数不带上下文。
func (s *Service) loadRoles(tenantID string) {
	if tenantID == "" {
		return
	}
	if err := s.roles.ensure(context.Background(), tenantID); err != nil {
		// Fail closed: a role table that cannot be read grants nothing. | 失败关闭：读不到角色表就不授予任何权限。
		s.log.Warn("auth: role table unreadable, no permission is granted | auth：角色表不可读，不授予任何权限",
			logx.Err(err))
	}
}

// permissionsFrom normalises a permission list: trimmed, deduplicated and sorted. | permissionsFrom 归一化权限列表：去空白、去重并排序。
func permissionsFrom(names []string) []Permission {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]Permission, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, Permission(name))
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// permissionNames converts normalised permissions back to plain strings for storage. | permissionNames 把归一化后的权限还原为普通字符串以便落库。
func permissionNames(perms []Permission) []string {
	if len(perms) == 0 {
		return nil
	}
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		out = append(out, string(p))
	}
	return out
}

// withoutRole returns names without the given role and reports whether it changed. | withoutRole 返回去掉指定角色后的名字，并报告是否发生了改动。
func withoutRole(names []string, role string) ([]string, bool) {
	if role == "" || len(names) == 0 {
		return names, false
	}
	out := make([]string, 0, len(names))
	changed := false
	for _, name := range names {
		if name == role {
			changed = true
			continue
		}
		out = append(out, name)
	}
	if !changed {
		return names, false
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, true
}

// cloneRole returns a deep copy of r, or nil when r is nil. | cloneRole 返回 r 的深拷贝，r 为空时返回 nil。
func cloneRole(r *Role) *Role {
	if r == nil {
		return nil
	}
	dup := *r
	if r.Permissions != nil {
		dup.Permissions = append([]string(nil), r.Permissions...)
	}
	return &dup
}
