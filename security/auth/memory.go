package auth

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shibingli/yd/core/errx"
)

// membership is one user's participation in one tenant: the roles granted there
// and whether that tenant is the sign-in default. | membership 是一个用户在某个租户中的成员关系：
// 该租户授予的角色，以及它是否为登录默认租户。
type membership struct {
	// createdAt orders memberships deterministically when timestamps tie. | createdAt 在时间戳相同时提供确定顺序。
	createdAt time.Time
	// roles holds the role names of that tenant. | roles 保存该租户内的角色名。
	roles []string
	// defaultTenant marks the tenant a bare sign-in lands in. | defaultTenant 标记未指明租户时登录落入的租户。
	defaultTenant bool
}

// clone returns a deep copy of m. | clone 返回 m 的深拷贝。
func (m *membership) clone() *membership {
	if m == nil {
		return nil
	}
	dup := *m
	if m.roles != nil {
		dup.roles = append([]string(nil), m.roles...)
	}
	return &dup
}

// memoryStore is the default in-process Store. | memoryStore 是默认的进程内 Store。
//
// Users are global identities: one row holds the password, the sec_key and the
// immutable home tenant, while memberships carry the per-tenant roles. A lookup
// for a tenant the user does not belong to simply misses and reports ErrNotFound,
// so there is no code path that could return a foreign entity even by accident.
// | 用户是全局身份：一条记录保存口令、sec_key 与不可变的身份租户，成员关系则携带各租户的角色。
// 对用户并不隶属的租户做查询必然未命中并报 ErrNotFound，因此不存在任何可能意外返回外部实体的代码路径。
//
// Reads hand out deep copies, so a caller can never mutate stored state and two
// goroutines never share a mutable struct: the whole type is -race clean. | 读取一律交出深拷贝，
// 因此调用方无法改动库中状态，两个 goroutine 也永远不会共享可变结构体：整个类型在 -race 下干净。
type memoryStore struct {
	// tenants maps tenant id to tenant. | tenants 把租户 ID 映射到租户。
	tenants map[string]*Tenant
	// slugs maps tenant slug to tenant id. | slugs 把租户 Slug 映射到租户 ID。
	slugs map[string]string
	// users maps user id to the global user record (its TenantID is the home tenant). | users 把用户 ID 映射到全局用户记录（其 TenantID 是身份租户）。
	users map[string]*User
	// usernames maps the lowercased globally unique username to user id. | usernames 把全局唯一的折叠登录名映射到用户 ID。
	usernames map[string]string
	// members maps tenant id to user id to membership. | members 把租户 ID 映射到用户 ID 再到成员关系。
	members map[string]map[string]*membership
	// userMembers maps user id to tenant id to membership. | userMembers 把用户 ID 映射到租户 ID 再到成员关系。
	userMembers map[string]map[string]*membership
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
		tenants:     make(map[string]*Tenant),
		slugs:       make(map[string]string),
		users:       make(map[string]*User),
		usernames:   make(map[string]string),
		members:     make(map[string]map[string]*membership),
		userMembers: make(map[string]map[string]*membership),
		roles:       make(map[string]map[string]*Role),
		roleNames:   make(map[string]map[string]string),
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

// CreateUser stores a new global user and its memberships. | CreateUser 存入新的全局用户及其成员关系。
//
// u.TenantID is the immutable home tenant and the first membership. Additional
// ids in u.TenantIDs become memberships without roles, which the caller fills in
// through AddUserTenant. The username is globally unique, so two tenants can no
// longer hold the same sign-in name. | u.TenantID 是不可变的身份租户与第一个成员关系；
// u.TenantIDs 中额外的 ID 会成为没有角色的成员关系，由调用方通过 AddUserTenant 补齐。
// 登录名全局唯一，因此两个租户不再能持有同一个登录名。
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
	stored := cloneUser(u)
	stored.TenantID = u.TenantID
	stored.HomeTenantID = u.TenantID
	if stored.DefaultTenantID == "" {
		stored.DefaultTenantID = u.TenantID
	}
	stored.Roles = nil
	stored.TenantIDs = nil

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tenants[u.TenantID]; !ok {
		// A user outside a known tenant is unreachable, so the write is refused. | 不属于已知租户的用户不可达，因此拒绝写入。
		return ErrNotFound
	}
	if _, dup := m.users[u.ID]; dup {
		return errx.Detail(ErrConflict, "user id %q already exists | 用户 ID %q 已存在", u.ID, u.ID)
	}
	if _, dup := m.usernames[key]; dup {
		return errx.Detail(ErrConflict, "username %q already exists | 用户名 %q 已存在", u.Username, u.Username)
	}
	if _, ok := m.tenants[stored.DefaultTenantID]; !ok {
		return ErrNotFound
	}
	defaultIsMember := stored.DefaultTenantID == u.TenantID
	for _, tenantID := range u.TenantIDs {
		if tenantID == "" || tenantID == u.TenantID {
			continue
		}
		if _, ok := m.tenants[tenantID]; !ok {
			return ErrNotFound
		}
		if tenantID == stored.DefaultTenantID {
			defaultIsMember = true
		}
	}
	if !defaultIsMember {
		return ErrNotFound
	}
	if err := m.requireRolesLocked(u.TenantID, u.Roles); err != nil {
		return err
	}

	m.users[u.ID] = stored
	m.usernames[key] = u.ID
	m.addMembershipLocked(u.ID, u.TenantID, u.Roles, stored.DefaultTenantID == u.TenantID, stored.CreatedAt)
	for _, tenantID := range u.TenantIDs {
		if tenantID == "" || tenantID == u.TenantID {
			continue
		}
		if _, exists := m.members[tenantID][u.ID]; exists {
			continue
		}
		m.addMembershipLocked(u.ID, tenantID, nil, stored.DefaultTenantID == tenantID, stored.CreatedAt)
	}
	return nil
}

// addMembershipLocked files one membership under both indexes; the caller holds the write lock.
// | addMembershipLocked 把一条成员关系同时写入两个索引；调用方必须持有写锁。
func (m *memoryStore) addMembershipLocked(userID, tenantID string, roles []string, isDefault bool, createdAt time.Time) {
	entry := &membership{roles: append([]string(nil), roles...), createdAt: createdAt, defaultTenant: isDefault}
	byTenant, ok := m.members[tenantID]
	if !ok {
		byTenant = make(map[string]*membership)
		m.members[tenantID] = byTenant
	}
	byTenant[userID] = entry
	byUser, ok := m.userMembers[userID]
	if !ok {
		byUser = make(map[string]*membership)
		m.userMembers[userID] = byUser
	}
	byUser[tenantID] = entry
}

// GetUser returns the user acting in that tenant. | GetUser 返回在该租户中活动的用户。
func (m *memoryStore) GetUser(_ context.Context, tenantID, userID string) (*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.buildUserLocked(tenantID, userID)
}

// buildUserLocked materialises the request-scoped view of a stored user. | buildUserLocked 构造库中用户在请求作用域下的视图。
//
// The caller holds at least a read lock. | 调用方至少持有读锁。
func (m *memoryStore) buildUserLocked(tenantID, userID string) (*User, error) {
	stored, ok := m.users[userID]
	if !ok {
		return nil, ErrNotFound
	}
	entry, ok := m.members[tenantID][userID]
	if !ok {
		return nil, ErrNotFound
	}
	out := cloneUser(stored)
	out.TenantID = tenantID
	out.HomeTenantID = stored.TenantID
	out.DefaultTenantID = stored.DefaultTenantID
	if out.DefaultTenantID == "" {
		out.DefaultTenantID = stored.TenantID
	}
	out.Roles = append([]string(nil), entry.roles...)
	out.TenantIDs = m.tenantIDsLocked(userID)
	return out, nil
}

// tenantIDsLocked returns the sorted membership ids of one user. | tenantIDsLocked 返回某用户已排序的成员租户 ID。
func (m *memoryStore) tenantIDsLocked(userID string) []string {
	byUser := m.userMembers[userID]
	out := make([]string, 0, len(byUser))
	for tenantID := range byUser {
		out = append(out, tenantID)
	}
	sort.Strings(out)
	return out
}

// GetUserByUsername returns the user of that tenant by username. | GetUserByUsername 按用户名返回该租户下的用户。
//
// Usernames are matched case-insensitively; the name itself is globally unique. | 用户名按大小写不敏感匹配；登录名本身全局唯一。
func (m *memoryStore) GetUserByUsername(_ context.Context, tenantID, username string) (*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.usernames[usernameKey(username)]
	if !ok {
		return nil, ErrNotFound
	}
	return m.buildUserLocked(tenantID, id)
}

// GetUserByName returns the globally unique user by sign-in name, acting in the
// user's default tenant. | GetUserByName 按全局唯一的登录名返回用户，并以默认租户作为活动租户。
func (m *memoryStore) GetUserByName(_ context.Context, username string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.usernames[usernameKey(username)]
	if !ok {
		return nil, ErrNotFound
	}
	return m.buildDefaultUserLocked(id)
}

// GetUserByID returns the user by global id, acting in the user's default tenant. | GetUserByID 按全局 ID 返回用户，并以默认租户作为活动租户。
func (m *memoryStore) GetUserByID(_ context.Context, userID string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.buildDefaultUserLocked(userID)
}

// buildDefaultUserLocked materialises a user in the default tenant (home when the
// default membership has gone missing). | buildDefaultUserLocked 以默认租户构造用户
// （默认成员关系丢失时回退到身份租户）。
func (m *memoryStore) buildDefaultUserLocked(userID string) (*User, error) {
	stored, ok := m.users[userID]
	if !ok {
		return nil, ErrNotFound
	}
	target := stored.DefaultTenantID
	if target == "" {
		target = stored.TenantID
	}
	if _, ok := m.members[target][userID]; !ok {
		target = stored.TenantID
	}
	return m.buildUserLocked(target, userID)
}

// ListUsers returns every member of the tenant ordered by creation time and then by id. | ListUsers 按创建时间、再按 ID 返回该租户的全部成员。
func (m *memoryStore) ListUsers(_ context.Context, tenantID string) ([]*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.tenants[tenantID]; !ok {
		return nil, ErrNotFound
	}
	out := make([]*User, 0, len(m.members[tenantID]))
	for userID := range m.members[tenantID] {
		u, err := m.buildUserLocked(tenantID, userID)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// UpdateUser replaces the stored profile and the roles inside tenantID. | UpdateUser 覆盖库中资料及在 tenantID 内的角色。
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
	stored, ok := m.users[u.ID]
	if !ok {
		return ErrNotFound
	}
	if _, ok := m.members[tenantID][u.ID]; !ok {
		return ErrNotFound
	}
	if err := m.requireRolesLocked(tenantID, u.Roles); err != nil {
		return err
	}
	oldKey := usernameKey(stored.Username)
	if oldKey != key {
		if holder, dup := m.usernames[key]; dup && holder != u.ID {
			return errx.Detail(ErrConflict, "username %q already exists | 用户名 %q 已存在", u.Username, u.Username)
		}
		delete(m.usernames, oldKey)
		m.usernames[key] = u.ID
	}
	updated := cloneUser(u)
	updated.ID = stored.ID
	updated.TenantID = stored.TenantID
	updated.HomeTenantID = stored.TenantID
	updated.DefaultTenantID = stored.DefaultTenantID
	updated.Status = stored.Status
	updated.PasswordHash = stored.PasswordHash
	updated.EncryptedSecKey = append([]byte(nil), stored.EncryptedSecKey...)
	updated.TenantIDs = nil
	updated.Roles = nil
	m.users[u.ID] = updated
	entry := m.members[tenantID][u.ID]
	entry.roles = append([]string(nil), u.Roles...)
	return nil
}

// SetUserStatus changes only the user's lifecycle status. | SetUserStatus 只修改用户的生命周期状态。
func (m *memoryStore) SetUserStatus(_ context.Context, tenantID, userID string, status Status, updatedAt time.Time) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.users[userID]
	if !ok {
		return ErrNotFound
	}
	if _, ok := m.members[tenantID][userID]; !ok {
		return ErrNotFound
	}
	stored.Status = status
	stored.UpdatedAt = updatedAt
	return nil
}

// SetUserCredentials changes the password hash and the sealed sec_key atomically. |
// SetUserCredentials 原子地修改口令散列与封装的 sec_key。
func (m *memoryStore) SetUserCredentials(_ context.Context, tenantID, userID, passwordHash string, encryptedSecKey []byte, updatedAt time.Time) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.users[userID]
	if !ok {
		return ErrNotFound
	}
	if _, ok := m.members[tenantID][userID]; !ok {
		return ErrNotFound
	}
	stored.PasswordHash = passwordHash
	stored.EncryptedSecKey = append([]byte(nil), encryptedSecKey...)
	stored.UpdatedAt = updatedAt
	return nil
}

// SetUserSecKey changes only the sealed sec_key. | SetUserSecKey 只修改封装的 sec_key。
func (m *memoryStore) SetUserSecKey(_ context.Context, tenantID, userID string, encryptedSecKey []byte, updatedAt time.Time) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.users[userID]
	if !ok {
		return ErrNotFound
	}
	if _, ok := m.members[tenantID][userID]; !ok {
		return ErrNotFound
	}
	stored.EncryptedSecKey = append([]byte(nil), encryptedSecKey...)
	stored.UpdatedAt = updatedAt
	return nil
}

// requireRolesLocked reports whether every named role exists in the tenant. |
// requireRolesLocked 报告每个命名角色是否都存在于该租户。
func (m *memoryStore) requireRolesLocked(tenantID string, names []string) error {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := m.roleNames[tenantID][name]; !ok {
			return ErrNotFound
		}
	}
	return nil
}

// ListUserTenants returns every tenant the user belongs to, ordered by creation time. | ListUserTenants 按创建时间返回该用户隶属的全部租户。
func (m *memoryStore) ListUserTenants(_ context.Context, userID string) ([]*Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.users[userID]; !ok {
		return nil, ErrNotFound
	}
	out := make([]*Tenant, 0, len(m.userMembers[userID]))
	for tenantID := range m.userMembers[userID] {
		if t, ok := m.tenants[tenantID]; ok {
			out = append(out, cloneTenant(t))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// AddUserTenant adds a membership, replacing the roles of an existing one. | AddUserTenant 添加成员关系，已存在时覆盖其角色。
func (m *memoryStore) AddUserTenant(_ context.Context, userID, tenantID string, roles []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return ErrNotFound
	}
	if _, ok := m.tenants[tenantID]; !ok {
		return ErrNotFound
	}
	if err := m.requireRolesLocked(tenantID, roles); err != nil {
		return err
	}
	if entry, ok := m.members[tenantID][userID]; ok {
		entry.roles = append([]string(nil), roles...)
		return nil
	}
	isDefault := m.users[userID].DefaultTenantID == tenantID
	m.addMembershipLocked(userID, tenantID, roles, isDefault, m.users[userID].CreatedAt)
	return nil
}

// RemoveUserTenant removes a membership that is neither the home nor the default tenant. | RemoveUserTenant 移除既非身份租户也非默认租户的成员关系。
func (m *memoryStore) RemoveUserTenant(_ context.Context, userID, tenantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.users[userID]
	if !ok {
		return ErrNotFound
	}
	entry, ok := m.members[tenantID][userID]
	if !ok {
		return ErrNotFound
	}
	if tenantID == stored.TenantID || entry.defaultTenant || stored.DefaultTenantID == tenantID {
		return errx.Detail(ErrConflict, "the home and default tenants cannot be removed | 身份租户与默认租户不可移除")
	}
	delete(m.members[tenantID], userID)
	delete(m.userMembers[userID], tenantID)
	return nil
}

// SetDefaultTenant makes an existing membership the sign-in default. | SetDefaultTenant 把已有成员关系设为登录默认租户。
func (m *memoryStore) SetDefaultTenant(_ context.Context, userID, tenantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.users[userID]
	if !ok {
		return ErrNotFound
	}
	if _, ok := m.members[tenantID][userID]; !ok {
		return ErrNotFound
	}
	if stored.DefaultTenantID == tenantID {
		return nil
	}
	if entry, ok := m.members[stored.DefaultTenantID][userID]; ok {
		entry.defaultTenant = false
	}
	stored.DefaultTenantID = tenantID
	m.members[tenantID][userID].defaultTenant = true
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
	// Same name, different id: the stored id wins, so a rename cannot fork a role. The losing
	// role keeps its old name mapping untouched. | 同名不同 ID：以库中 ID 为准，避免重命名把角色分裂成两个；
	// 落败的角色保留原有名称映射，不会被变成无名角色。
	if holder, dup := byName[name]; dup && holder != r.ID {
		replaced := byID[holder]
		updated := cloneRole(r)
		updated.ID = holder
		if replaced != nil {
			updated.TenantID = replaced.TenantID
		}
		byID[holder] = updated
		byName[name] = holder
		return nil
	}
	// A rename must release the old name; otherwise the old name keeps resolving to this id and
	// users carrying it keep the role's permissions (fail-open) after the rename. | 改名必须释放旧名字；
	// 否则旧名字仍解析到该 ID，携带旧名字的用户在改名后继续拥有该角色的权限（失败开放）。
	if existing := byID[r.ID]; existing != nil && existing.Name != name {
		if holder, ok := byName[existing.Name]; ok && holder == r.ID {
			delete(byName, existing.Name)
		}
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
	// Role names live in the membership records, so detaching them here keeps the delete atomic with
	// the role row. | 角色名保存在成员关系里，在这里一并摘除才能让删除与角色行保持原子。
	for _, entry := range m.members[tenantID] {
		kept := entry.roles[:0]
		for _, name := range entry.roles {
			if name != r.Name {
				kept = append(kept, name)
			}
		}
		entry.roles = kept
	}
	return nil
}

// usernameKey normalises a username for the uniqueness index. | usernameKey 归一化用户名，用于唯一性索引。
func usernameKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
