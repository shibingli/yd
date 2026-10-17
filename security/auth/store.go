package auth

import "context"

// Store is the persistence boundary of the auth package. | Store 是 auth 包的持久化边界。
//
// Every method that reaches an entity by id takes the owning tenantID and must
// validate it; a foreign or unknown entity is reported as ErrNotFound, never as
// ErrForbidden, so that callers cannot probe for the existence of another
// tenant's data. | 任何按 ID 触达实体的方法都带有所属 tenantID 并必须校验；外部或未知实体一律报
// ErrNotFound 而非 ErrForbidden，调用方无法探测其它租户的数据是否存在。
//
// Implementations must be safe for concurrent use, must never retain caller-owned
// slices, and must return copies so that a caller cannot mutate stored state. | 实现必须
// 并发安全、不得保留调用方持有的切片，并返回副本，使调用方无法改动库中状态。
//
// The context is passed for tracing and cancellation; the default memory store
// performs no I/O and therefore ignores it. | 上下文用于链路追踪与取消；默认的内存实现不做 I/O，因此忽略它。
type Store interface {
	// CreateTenant stores a new tenant; a duplicate id or slug fails with an error wrapping ErrConflict. | CreateTenant 存入新租户；ID 或 Slug 重复时返回包装了 ErrConflict 的错误。
	CreateTenant(ctx context.Context, t *Tenant) error
	// GetTenant returns the tenant with the given id, or ErrNotFound. | GetTenant 返回指定 ID 的租户，不存在时返回 ErrNotFound。
	GetTenant(ctx context.Context, tenantID string) (*Tenant, error)
	// ListTenants returns every tenant ordered by creation time. | ListTenants 按创建时间返回全部租户。
	ListTenants(ctx context.Context) ([]*Tenant, error)

	// CreateUser stores a new user inside its TenantID; a duplicate id or username fails with an error wrapping ErrConflict. | CreateUser 在用户的 TenantID 下存入新用户；ID 或用户名重复时返回包装了 ErrConflict 的错误。
	CreateUser(ctx context.Context, u *User) error
	// GetUser returns the user of that tenant, or ErrNotFound when the tenant is unknown or the user belongs to another tenant. | GetUser 返回该租户下的用户；租户未知或用户属于其它租户时返回 ErrNotFound。
	GetUser(ctx context.Context, tenantID, userID string) (*User, error)
	// GetUserByUsername returns the user of that tenant by username, or ErrNotFound. | GetUserByUsername 按用户名返回该租户下的用户，不存在时返回 ErrNotFound。
	GetUserByUsername(ctx context.Context, tenantID, username string) (*User, error)
	// ListUsers returns every user of the tenant ordered by creation time, or ErrNotFound when the tenant is unknown. | ListUsers 按创建时间返回该租户的全部用户；租户未知时返回 ErrNotFound。
	ListUsers(ctx context.Context, tenantID string) ([]*User, error)
	// UpdateUser replaces the stored user, which must already exist inside tenantID; otherwise ErrNotFound. | UpdateUser 覆盖已存在的用户，且该用户必须位于 tenantID 内，否则返回 ErrNotFound。
	UpdateUser(ctx context.Context, tenantID string, u *User) error

	// UpsertRole stores a role, replacing the one with the same id, or the one with the same name inside the tenant. | UpsertRole 存入角色，覆盖同 ID 或同租户内同名的既有角色。
	UpsertRole(ctx context.Context, r *Role) error
	// GetRole returns the role of that tenant, or ErrNotFound. | GetRole 返回该租户下的角色，不存在时返回 ErrNotFound。
	GetRole(ctx context.Context, tenantID, roleID string) (*Role, error)
	// ListRoles returns every role of the tenant ordered by name, or ErrNotFound when the tenant is unknown. | ListRoles 按名称返回该租户的全部角色；租户未知时返回 ErrNotFound。
	ListRoles(ctx context.Context, tenantID string) ([]*Role, error)
	// DeleteRole removes the role of that tenant, or returns ErrNotFound. | DeleteRole 删除该租户下的角色，不存在时返回 ErrNotFound。
	DeleteRole(ctx context.Context, tenantID, roleID string) error
}
