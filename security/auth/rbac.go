package auth

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
)

// defaultRoleCacheTTL bounds how long a replica may serve a stale role table. Mutations in this
// process invalidate immediately; the TTL is what covers writes made by another replica, which
// this process cannot observe. | defaultRoleCacheTTL 限定单副本可以继续使用旧角色表的最长时间。
// 本进程内的变更会立即失效；TTL 覆盖的是本进程无法观察到的其它副本的写入。
const defaultRoleCacheTTL = 30 * time.Second

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
	// now is the clock used for TTL comparisons. | now 是 TTL 比较使用的时钟。
	now func() time.Time
	// loaded marks the tenants whose roles have been read from the store. | loaded 标记已经从 Store 读取过角色的租户。
	loaded map[string]struct{}
	// loadedAt records when each tenant was last refreshed, so a replica eventually observes
	// changes made by another process. | loadedAt 记录每个租户上次刷新的时间，使本副本最终能观察到
	// 其它进程所做的变更。
	loadedAt map[string]time.Time
	// perms maps tenant id to role name to permissions. | perms 把租户 ID 映射到角色名再到权限列表。
	perms map[string]map[string][]Permission
	// version counts invalidations per tenant. A load publishes only if its tenant's version did
	// not move while it was reading the store, and a reader retries when the version moved under
	// it; without that, an invalidation between "the cache is fresh" and "look up the name" makes
	// a role that exists in the store look unknown (fail-closed, but a spurious error for
	// SetUserRoles). Per tenant, not global: a role write in one tenant must not make a stale read
	// in another tenant look acceptable. | version 按租户记录失效次数。加载只有在读取 Store 期间该租户
	// 的版本未变时才发布；读者发现版本变化就重试。否则在「缓存是新的」与「查这个名字」之间发生失效时，
	// 库里存在的角色会被看成不存在（虽然失败关闭，但会让 SetUserRoles 报出假错误）。按租户而非全局计数：
	// 一个租户写角色，不能让另一个租户的陈旧读取看起来仍然可接受。
	version map[string]uint64
	// ttl bounds the staleness of one tenant. | ttl 限定单个租户缓存的最大陈旧时长。
	ttl time.Duration
	// mu guards the fields above and is kept last to shrink the pointer
	// bytes the collector has to scan. | mu 保护上述字段，并置于最后以减少回收器需要扫描的指针字节数。
	mu sync.RWMutex
}

// newRoleCache returns an empty cache reading through store. | newRoleCache 返回一个以 store 为读透来源的空缓存。
func newRoleCache(store Store, now func() time.Time) *roleCache {
	if now == nil {
		now = time.Now
	}
	return &roleCache{
		store:    store,
		now:      now,
		loaded:   make(map[string]struct{}),
		loadedAt: make(map[string]time.Time),
		perms:    make(map[string]map[string][]Permission),
		version:  make(map[string]uint64),
		ttl:      defaultRoleCacheTTL,
	}
}

// ensure loads the roles of a tenant from the store at most once per invalidation. | ensure 每个失效周期内最多从 Store 加载一次该租户的角色。
func (c *roleCache) ensure(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return nil
	}
	c.mu.RLock()
	gen := c.version[tenantID]
	_, ok := c.loaded[tenantID]
	loadedAt := c.loadedAt[tenantID]
	c.mu.RUnlock()
	if ok && c.ttl > 0 && c.now().Sub(loadedAt) < c.ttl {
		return nil
	}
	roles, err := c.store.ListRoles(ctx, tenantID)
	if err != nil {
		return err
	}
	c.refresh(tenantID, roles, gen)
	return nil
}

// refresh replaces the cached roles of one tenant, unless an invalidation happened after the
// caller started reading the store. Publishing a snapshot taken before that write would resurrect
// the stale role table the invalidation was meant to drop. | refresh 覆盖某个租户的缓存角色；若调用方
// 开始读取 Store 之后发生过失效，则放弃发布。否则会把失效本应丢弃的旧角色表重新放回缓存。
func (c *roleCache) refresh(tenantID string, roles []*Role, gen uint64) {
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
	if c.version[tenantID] != gen {
		return
	}
	c.loaded[tenantID] = struct{}{}
	c.loadedAt[tenantID] = c.now()
	c.perms[tenantID] = perms
}

// invalidate drops the cached roles of one tenant. | invalidate 丢弃某个租户的缓存角色。
func (c *roleCache) invalidate(tenantID string) {
	if tenantID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.version[tenantID]++
	delete(c.loaded, tenantID)
	delete(c.loadedAt, tenantID)
	delete(c.perms, tenantID)
}

// versionOf returns the invalidation version of one tenant. | versionOf 返回单个租户的失效版本。
func (c *roleCache) versionOf(tenantID string) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version[tenantID]
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
	// Retry when an invalidation lands between the freshness check and the lookup: the role may
	// be present in the store even though the just-dropped cache no longer maps its name. | 若失效
	// 发生在「确认缓存新鲜」与「查名字」之间则重试：角色可能仍在库里，只是刚被丢弃的缓存不再映射该名字。
	for attempt := 0; attempt < 4; attempt++ {
		gen := c.versionOf(tenantID)
		if err := c.ensure(ctx, tenantID); err != nil {
			return false, err
		}
		_, ok := c.lookup(tenantID, name)
		if c.versionOf(tenantID) == gen {
			return ok, nil
		}
	}
	// A writer that invalidates faster than the retries is answered from the store directly;
	// that keeps a known role from looking unknown while never granting an unknown one. | 写入者失效得
	// 比重试还快时直接以 Store 为准；这样已知角色不会看起来不存在，未知角色也绝不会被放行。
	roles, err := c.store.ListRoles(ctx, tenantID)
	if err != nil {
		return false, err
	}
	for _, r := range roles {
		if r != nil && r.Name == name {
			return true, nil
		}
	}
	return false, nil
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
	// Invalidate rather than patch: the role may have been renamed, and leaving the old name in
	// the cache would keep granting its old permissions (fail-open) until a refresh. | 选择失效而不是
	// 局部更新：角色可能被改名，把旧名字留在缓存里会让旧权限继续生效（失败开放）直到下一次刷新。
	s.roles.invalidate(tenantID)
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
	gen := s.roles.versionOf(tenantID)
	roles, err := s.store.ListRoles(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	s.roles.refresh(tenantID, roles, gen)
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
