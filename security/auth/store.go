package auth

import (
	"context"
	"time"
)

// Store is the persistence boundary of the auth package. | Store 是 auth 包的持久化边界。
//
// Every method that reaches an entity by id takes the owning tenantID and must
// validate it; a foreign or unknown entity is reported as ErrNotFound, never as
// ErrForbidden, so that callers cannot probe for the existence of another
// tenant's data. A user is a global identity: the tenant-scoped user methods
// check membership, while GetUserByName resolves a globally unique sign-in name
// and lands the user in its default tenant. | 任何按 ID 触达实体的方法都带有所属 tenantID
// 并必须校验；外部或未知实体一律报 ErrNotFound 而非 ErrForbidden，调用方无法探测其它租户的数据
// 是否存在。用户是全局身份：按租户的用户方法校验成员关系，GetUserByName 则解析全局唯一的登录名
// 并让用户落入其默认租户。
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

	// CreateUser stores a new user and its membership of u.TenantID; a duplicate id or a globally duplicated username fails with an error wrapping ErrConflict. | CreateUser 存入新用户及其在 u.TenantID 的成员关系；ID 重复或登录名全局重复时返回包装了 ErrConflict 的错误。
	CreateUser(ctx context.Context, u *User) error
	// GetUser returns the user acting in that tenant, or ErrNotFound when the user is not a member of it. | GetUser 返回在该租户中活动的用户；用户不属于该租户时返回 ErrNotFound。
	GetUser(ctx context.Context, tenantID, userID string) (*User, error)
	// GetUserByUsername returns the user of that tenant by username, or ErrNotFound. | GetUserByUsername 按用户名返回该租户下的用户，不存在时返回 ErrNotFound。
	GetUserByUsername(ctx context.Context, tenantID, username string) (*User, error)
	// GetUserByName returns the globally unique user by sign-in name, acting in that user's default tenant. | GetUserByName 按全局唯一的登录名返回用户，并以该用户的默认租户作为活动租户。
	GetUserByName(ctx context.Context, username string) (*User, error)
	// ListUsers returns every member of the tenant ordered by creation time, or ErrNotFound when the tenant is unknown. | ListUsers 按创建时间返回该租户的全部成员；租户未知时返回 ErrNotFound。
	ListUsers(ctx context.Context, tenantID string) ([]*User, error)
	// UpdateUser replaces the stored user's profile and its roles inside tenantID, which must already hold a membership; otherwise ErrNotFound. Credential and lifecycle columns are owned by the dedicated setters below. | UpdateUser 覆盖已存在用户的资料及其在 tenantID 内的角色；该租户没有成员关系时返回 ErrNotFound。凭据与生命周期列由下面的专用方法维护。
	UpdateUser(ctx context.Context, tenantID string, u *User) error
	// SetUserStatus changes only the lifecycle status. | SetUserStatus 只修改生命周期状态。
	SetUserStatus(ctx context.Context, tenantID, userID string, status Status, updatedAt time.Time) error
	// SetUserCredentials changes only the password hash and the sealed sec_key atomically. |
	// SetUserCredentials 原子地只修改口令散列与封装的 sec_key。
	SetUserCredentials(ctx context.Context, tenantID, userID, passwordHash string, encryptedSecKey []byte, updatedAt time.Time) error
	// SetUserSecKey changes only the sealed sec_key, leaving the password untouched. |
	// SetUserSecKey 只修改封装的 sec_key，不改口令。
	SetUserSecKey(ctx context.Context, tenantID, userID string, encryptedSecKey []byte, updatedAt time.Time) error

	// GetUserByID returns the user by global id regardless of the tenant, or ErrNotFound. | GetUserByID 按全局 ID 返回用户（不区分租户），不存在时返回 ErrNotFound。
	GetUserByID(ctx context.Context, userID string) (*User, error)
	// ListUserTenants returns every tenant the user belongs to, ordered by creation time. | ListUserTenants 按创建时间返回该用户隶属的全部租户。
	ListUserTenants(ctx context.Context, userID string) ([]*Tenant, error)
	// AddUserTenant adds the user to a tenant, replacing the roles of an existing membership; an unknown user or tenant is ErrNotFound. | AddUserTenant 把用户加入租户，已存在成员关系时覆盖其角色；用户或租户未知时返回 ErrNotFound。
	AddUserTenant(ctx context.Context, userID, tenantID string, roles []string) error
	// RemoveUserTenant removes a non-default membership; removing the default or home tenant is refused with ErrConflict. | RemoveUserTenant 移除非默认的成员关系；移除默认租户或身份租户时以 ErrConflict 拒绝。
	RemoveUserTenant(ctx context.Context, userID, tenantID string) error
	// SetDefaultTenant makes an existing membership the tenant a bare sign-in lands in; otherwise ErrNotFound. | SetDefaultTenant 把已有成员关系设为未指明租户时登录落入的租户；成员关系不存在时返回 ErrNotFound。
	SetDefaultTenant(ctx context.Context, userID, tenantID string) error

	// UpsertRole stores a role, replacing the one with the same id, or the one with the same name inside the tenant. | UpsertRole 存入角色，覆盖同 ID 或同租户内同名的既有角色。
	UpsertRole(ctx context.Context, r *Role) error
	// GetRole returns the role of that tenant, or ErrNotFound. | GetRole 返回该租户下的角色，不存在时返回 ErrNotFound。
	GetRole(ctx context.Context, tenantID, roleID string) (*Role, error)
	// ListRoles returns every role of the tenant ordered by name, or ErrNotFound when the tenant is unknown. | ListRoles 按名称返回该租户的全部角色；租户未知时返回 ErrNotFound。
	ListRoles(ctx context.Context, tenantID string) ([]*Role, error)
	// DeleteRole removes the role of that tenant, or returns ErrNotFound. | DeleteRole 删除该租户下的角色，不存在时返回 ErrNotFound。
	DeleteRole(ctx context.Context, tenantID, roleID string) error
}

// TenantMembership is one desired user-tenant membership handed to TenantReplacer. | TenantMembership 是交给
// TenantReplacer 的一条期望用户-租户成员关系。
type TenantMembership struct {
	// TenantID is the tenant the user should belong to. | TenantID 是用户应当隶属的租户。
	TenantID string
	// Roles is the role set inside that tenant. A nil value keeps the stored roles of an existing membership, so a
	// caller that only rearranges tenants never has to reload every role list; a new membership with nil roles is
	// created without roles. | Roles 是该租户内的角色集合。取值为 nil 时保留已存在成员关系的库中角色，因此只调整
	// 租户的调用方无需逐个重新载入角色清单；新成员关系配 nil 角色即不授予任何角色。
	Roles []string
}

// TenantReplacer is the optional Store capability that rewrites a user's whole membership set in one atomic step.
// | TenantReplacer 是 Store 的可选能力：一步原子地重写某个用户的全部成员关系。
//
// The sequential AddUserTenant / RemoveUserTenant / SetDefaultTenant trio is not atomic: a failure halfway through
// leaves a partially adjusted membership set behind. A Store that implements this interface lets callers replace the
// whole set under one lock or one transaction, and the replacement must validate every target tenant, every role and
// the default tenant before writing anything. | 顺序版本 AddUserTenant / RemoveUserTenant / SetDefaultTenant 并不原子：
// 中途失败会留下改到一半的成员关系。实现本接口的 Store 让调用方在同一把锁或同一个事务里替换整个集合，替换必须
// 在写入之前完成全部目标租户、角色与默认租户的校验。
//
// Implementation contract | 实现契约：
//
//   - the user's home tenant (User.TenantID) must be present in memberships, otherwise ErrConflict;
//   - defaultTenantID must name one of memberships, otherwise ErrInvalidInput;
//   - every membership tenant and every non-nil role name must exist, otherwise ErrNotFound;
//   - nothing may be written when any validation fails.
//
// 用户的身份租户必须留在成员集合内，否则返回 ErrConflict；defaultTenantID 必须是成员之一，否则返回
// ErrInvalidInput；每个成员租户与每个非 nil 角色名都必须存在，否则返回 ErrNotFound；任一校验失败都不得写入任何数据。
type TenantReplacer interface {
	// ReplaceUserTenants atomically replaces the memberships of userID and its default tenant. | ReplaceUserTenants
	// 原子地替换 userID 的成员关系及其默认租户。
	ReplaceUserTenants(ctx context.Context, userID string, memberships []TenantMembership, defaultTenantID string) error
}
