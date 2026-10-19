package auth

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/shibingli/yd/core/errx"
)

// memoryStore is the default in-process Store. | memoryStore 是默认的进程内 Store。
//
// Every map is keyed by tenant id first, so a lookup for another tenant simply
// misses and reports ErrNotFound; there is no code path that could return a
// foreign entity even by accident. | 每个 map 都以租户 ID 为第一层键，因此对其它租户的查询必然
// 未命中并报 ErrNotFound；不存在任何可能意外返回外部实体的代码路径。
//
// Reads hand out deep copies, so a caller can never mutate stored state and two
// goroutines never share a mutable struct: the whole type is -race clean. | 读取一律交出深拷贝，
// 因此调用方无法改动库中状态，两个 goroutine 也永远不会共享可变结构体：整个类型在 -race 下干净。
type memoryStore struct {
	// tenants maps tenant id to tenant. | tenants 把租户 ID 映射到租户。
	tenants map[string]*Tenant
	// slugs maps tenant slug to tenant id. | slugs 把租户 Slug 映射到租户 ID。
	slugs map[string]string
	// users maps tenant id to user id to user. | users 把租户 ID 映射到用户 ID 再到用户。
	users map[string]map[string]*User
	// usernames maps tenant id to the lowercased username to user id. | usernames 把租户 ID 映射到小写用户名再到用户 ID。
	usernames map[string]map[string]string
	// roles maps tenant id to role id to role. | roles 把租户 ID 映射到角色 ID 再到角色。
	roles map[string]map[string]*Role
	// roleNames maps tenant id to role name to role id. | roleNames 把租户 ID 映射到角色名再到角色 ID。
	roleNames map[string]map[string]string
	// mu guards every map below and is kept last to shrink the pointer bytes the
	// collector has to scan. | mu 保护下面全部 map，并置于最后以减少回收器需要扫描的指针字节数。
	mu sync.RWMutex
}

// NewMemoryStore returns an empty, concurrency-safe Store backed by maps. | NewMemoryStore 返回一个由 map 支撑、并发安全的空 Store。
//
// It is meant for tests, single-process deployments and as a reference
// implementation of the Store contract; a database-backed Store can be dropped in
// without touching the Service. | 它面向测试、单进程部署，同时是 Store 契约的参考实现；
// 换成数据库实现无需改动 Service。
func NewMemoryStore() Store { return newMemoryStore() }

// newMemoryStore returns the concrete store, which tests use to inspect raw state. | newMemoryStore 返回具体类型，测试用它检查库中原始状态。
func newMemoryStore() *memoryStore {
	return &memoryStore{
		tenants:   make(map[string]*Tenant),
		slugs:     make(map[string]string),
		users:     make(map[string]map[string]*User),
		usernames: make(map[string]map[string]string),
		roles:     make(map[string]map[string]*Role),
		roleNames: make(map[string]map[string]string),
	}
}

// CreateTenant stores a new tenant. | CreateTenant 存入新租户。
func (m *memoryStore) CreateTenant(_ context.Context, t *Tenant) error {
	if t == nil || t.ID == "" {
		return errx.Detail(ErrInvalidInput, "tenant requires an id | 租户必须带 ID")
	}
	slug := strings.ToLower(strings.TrimSpace(t.Slug))
	if slug == "" {
		return errx.Detail(ErrInvalidInput, "tenant requires a slug | 租户必须带 Slug")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.tenants[t.ID]; dup {
		return errx.Detail(ErrConflict, "tenant id %q already exists | 租户 ID %q 已存在", t.ID, t.ID)
	}
	if _, dup := m.slugs[slug]; dup {
		return errx.Detail(ErrConflict, "tenant slug %q already exists | 租户 Slug %q 已存在", slug, slug)
	}
	m.tenants[t.ID] = cloneTenant(t)
	m.slugs[slug] = t.ID
	return nil
}

// GetTenant returns one tenant by id. | GetTenant 按 ID 返回单个租户。
func (m *memoryStore) GetTenant(_ context.Context, tenantID string) (*Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tenants[tenantID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneTenant(t), nil
}

// ListTenants returns every tenant ordered by creation time and then by id. | ListTenants 按创建时间、再按 ID 返回全部租户。
func (m *memoryStore) ListTenants(_ context.Context) ([]*Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Tenant, 0, len(m.tenants))
	for _, t := range m.tenants {
		out = append(out, cloneTenant(t))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// CreateUser stores a new user inside its tenant. | CreateUser 在用户的租户内存入新用户。
func (m *memoryStore) CreateUser(_ context.Context, u *User) error {
	if u == nil || u.ID == "" {
		return errx.Detail(ErrInvalidInput, "user requires an id | 用户必须带 ID")
	}
	if strings.TrimSpace(u.TenantID) == "" {
		return ErrTenantRequired
	}
	if strings.TrimSpace(u.Username) == "" {
		return errx.Detail(ErrInvalidInput, "user requires a username | 用户必须带用户名")
	}
	key := usernameKey(u.Username)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tenants[u.TenantID]; !ok {
		// A user outside a known tenant is unreachable, so the write is refused. | 不属于已知租户的用户不可达，因此拒绝写入。
		return ErrNotFound
	}
	byID, ok := m.users[u.TenantID]
	if !ok {
		byID = make(map[string]*User)
		m.users[u.TenantID] = byID
	}
	if _, dup := byID[u.ID]; dup {
		return errx.Detail(ErrConflict, "user id %q already exists | 用户 ID %q 已存在", u.ID, u.ID)
	}
	byName, ok := m.usernames[u.TenantID]
	if !ok {
		byName = make(map[string]string)
		m.usernames[u.TenantID] = byName
	}
	if _, dup := byName[key]; dup {
		return errx.Detail(ErrConflict, "username %q already exists | 用户名 %q 已存在", u.Username, u.Username)
	}
	byID[u.ID] = cloneUser(u)
	byName[key] = u.ID
	return nil
}

// GetUser returns the user of that tenant. | GetUser 返回该租户下的用户。
func (m *memoryStore) GetUser(_ context.Context, tenantID, userID string) (*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[tenantID][userID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneUser(u), nil
}

// GetUserByUsername returns the user of that tenant by username. | GetUserByUsername 按用户名返回该租户下的用户。
//
// Usernames are matched case-insensitively, so "Alice" and "alice" are one account
// inside a tenant. | 用户名按大小写不敏感匹配，因此同一租户内 "Alice" 与 "alice" 是同一个账号。
func (m *memoryStore) GetUserByUsername(_ context.Context, tenantID, username string) (*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.usernames[tenantID][usernameKey(username)]
	if !ok {
		return nil, ErrNotFound
	}
	u, ok := m.users[tenantID][id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneUser(u), nil
}

// ListUsers returns every user of the tenant ordered by creation time and then by id. | ListUsers 按创建时间、再按 ID 返回该租户的全部用户。
func (m *memoryStore) ListUsers(_ context.Context, tenantID string) ([]*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.tenants[tenantID]; !ok {
		return nil, ErrNotFound
	}
	out := make([]*User, 0, len(m.users[tenantID]))
	for _, u := range m.users[tenantID] {
		out = append(out, cloneUser(u))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// UpdateUser replaces an existing user of the tenant. | UpdateUser 覆盖该租户内已存在的用户。
func (m *memoryStore) UpdateUser(_ context.Context, tenantID string, u *User) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	if u == nil || u.ID == "" {
		return errx.Detail(ErrInvalidInput, "user requires an id | 用户必须带 ID")
	}
	if u.TenantID != "" && u.TenantID != tenantID {
		return ErrNotFound
	}
	key := usernameKey(u.Username)
	m.mu.Lock()
	defer m.mu.Unlock()
	byID := m.users[tenantID]
	old, ok := byID[u.ID]
	if !ok {
		return ErrNotFound
	}
	byName := m.usernames[tenantID]
	oldKey := usernameKey(old.Username)
	if oldKey != key {
		if holder, dup := byName[key]; dup && holder != u.ID {
			return errx.Detail(ErrConflict, "username %q already exists | 用户名 %q 已存在", u.Username, u.Username)
		}
		delete(byName, oldKey)
		byName[key] = u.ID
	}
	// The tenant is the key this record is filed under, never whatever the payload carried;
	// storing an empty value would make the row unusable for token issuance and role lookup.
	// | 租户就是本条记录所在的键，而不是载荷携带的值；存入空值会让该行无法签令牌、也无法查角色。
	updated := cloneUser(u)
	updated.TenantID = tenantID
	byID[u.ID] = updated
	return nil
}

// UpsertRole stores a role, replacing the one with the same id or, inside the
// tenant, the one with the same name. | UpsertRole 存入角色，覆盖同 ID 或同租户内同名的既有角色。
//
// The stored role always carries a generated id and the tenant id of the key it is
// filed under. | 落库的角色始终带有生成的 ID 与所在租户的 ID。
func (m *memoryStore) UpsertRole(_ context.Context, r *Role) error {
	if r == nil || r.ID == "" || strings.TrimSpace(r.Name) == "" {
		return errx.Detail(ErrInvalidInput, "role requires an id and a name | 角色必须带 ID 与名称")
	}
	if strings.TrimSpace(r.TenantID) == "" {
		return ErrTenantRequired
	}
	name := strings.TrimSpace(r.Name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tenants[r.TenantID]; !ok {
		return ErrNotFound
	}
	byID, ok := m.roles[r.TenantID]
	if !ok {
		byID = make(map[string]*Role)
		m.roles[r.TenantID] = byID
	}
	byName, ok := m.roleNames[r.TenantID]
	if !ok {
		byName = make(map[string]string)
		m.roleNames[r.TenantID] = byName
	}
	// A rename must release the old name; otherwise the old name keeps resolving to this id and
	// users carrying it keep the role's permissions (fail-open) after the rename. | 改名必须释放旧名字；
	// 否则旧名字仍解析到该 ID，携带旧名字的用户在改名后继续拥有该角色的权限（失败开放）。
	if existing := byID[r.ID]; existing != nil && existing.Name != name {
		if holder, ok := byName[existing.Name]; ok && holder == r.ID {
			delete(byName, existing.Name)
		}
	}
	if holder, dup := byName[name]; dup && holder != r.ID {
		// Same name, different id: the stored id wins, so a rename cannot fork a role. | 同名不同 ID：以库中 ID 为准，避免重命名把角色分裂成两个。
		replaced := byID[holder]
		updated := cloneRole(r)
		updated.ID = holder
		if replaced != nil {
			updated.TenantID = replaced.TenantID
		}
		byID[holder] = updated
		return nil
	}
	byID[r.ID] = cloneRole(r)
	byName[name] = r.ID
	return nil
}

// GetRole returns the role of that tenant. | GetRole 返回该租户下的角色。
func (m *memoryStore) GetRole(_ context.Context, tenantID, roleID string) (*Role, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.roles[tenantID][roleID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneRole(r), nil
}

// ListRoles returns every role of the tenant ordered by name. | ListRoles 按名称返回该租户的全部角色。
func (m *memoryStore) ListRoles(_ context.Context, tenantID string) ([]*Role, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.tenants[tenantID]; !ok {
		return nil, ErrNotFound
	}
	out := make([]*Role, 0, len(m.roles[tenantID]))
	for _, r := range m.roles[tenantID] {
		out = append(out, cloneRole(r))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// DeleteRole removes the role of that tenant. | DeleteRole 删除该租户下的角色。
//
// The name is only freed when it still points at the deleted id, so a concurrent
// re-creation under the same name cannot be dropped by a stale delete. | 仅当名字仍指向被删除的 ID 时才释放它，
// 因此同名角色的并发重建不会被迟到的删除操作清掉。
func (m *memoryStore) DeleteRole(_ context.Context, tenantID, roleID string) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	byID := m.roles[tenantID]
	r, ok := byID[roleID]
	if !ok {
		return ErrNotFound
	}
	delete(byID, roleID)
	if holder, ok := m.roleNames[tenantID][r.Name]; ok && holder == roleID {
		delete(m.roleNames[tenantID], r.Name)
	}
	return nil
}

// usernameKey normalises a username for the uniqueness index. | usernameKey 归一化用户名，用于唯一性索引。
func usernameKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
