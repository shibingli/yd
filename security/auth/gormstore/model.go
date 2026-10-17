package gormstore

import (
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/security/auth"
)

// ErrCorruptRow reports a stored row this package cannot decode. | ErrCorruptRow 表示本包无法解码的库中行。
//
// It means the database was written by something other than this adapter (or was
// truncated by hand), which is why it is a distinct sentinel and not ErrNotFound. | 它意味着库中的
// 行不是本适配器写入的（或被人手工截断），因此它是独立哨兵，而不是 ErrNotFound。
var ErrCorruptRow = errx.New("gormstore: stored row is malformed | gormstore：库中行数据已损坏")

// Column widths. | 列宽。
//
// Every string column is bounded on purpose: an unbounded varchar becomes a
// longtext/clob on MySQL and hides the index limit until the day an index is added
// over it. permission is 191 characters because that is the longest value MySQL
// still indexes with utf8mb4 (191 * 4 < 767 bytes). | 每个字符串列都刻意给出上界：
// 无界 varchar 在 MySQL 上会变成 longtext/clob，索引限制直到某天真的在它上面建索引时才暴露。
// permission 取 191 字符，因为这是 MySQL 在 utf8mb4 下仍然能建索引的最长值（191 * 4 < 767 字节）。
const (
	idWidth          = 64
	slugWidth        = 128
	usernameWidth    = 128
	emailWidth       = 191
	displayNameWidth = 191
	hashWidth        = 255
	statusWidth      = 32
	permissionWidth  = 191
	secKeyWidth      = 4096
)

// tenantRow maps auth.Tenant onto yd_auth_tenants. | tenantRow 把 auth.Tenant 映射到 yd_auth_tenants。
//
// slug holds the slug exactly as it was created while slug_key holds the
// case-folded copy the unique index sits on, because the contract treats "Demo"
// and "demo" as one slug. | slug 原样保存创建时的 Slug，slug_key 保存唯一索引所在的大小写折叠副本，
// 因为契约把 "Demo" 与 "demo" 视为同一个 Slug。
type tenantRow struct {
	// CreatedAt is the Service clock time; yd_idx_auth_tenants_created_at serves ListTenants. | CreatedAt 是 Service 时钟时间；yd_idx_auth_tenants_created_at 服务 ListTenants。
	CreatedAt time.Time `gorm:"column:created_at;index:yd_idx_auth_tenants_created_at"`
	// ID is the tenant id and the primary key. | ID 是租户 ID，也是主键。
	ID string `gorm:"column:id;type:varchar(64);primaryKey"`
	// Slug is the slug as created. | Slug 是创建时原样的 Slug。
	Slug string `gorm:"column:slug;type:varchar(128);not null"`
	// SlugKey is the folded slug; yd_idx_auth_tenants_slug is unique on it. | SlugKey 是折叠后的 Slug；yd_idx_auth_tenants_slug 在其上唯一。
	SlugKey string `gorm:"column:slug_key;type:varchar(128);not null;uniqueIndex:yd_idx_auth_tenants_slug"`
	// Name is the display name. | Name 是展示名称。
	Name string `gorm:"column:name;type:varchar(128);not null"`
	// Status is "active" or "disabled". | Status 取 "active" 或 "disabled"。
	Status string `gorm:"column:status;type:varchar(32);not null"`
}

// TableName pins the table name, so renaming the type can never move data. | TableName 固定表名，类型改名永远不会挪动数据。
func (tenantRow) TableName() string { return tableTenants }

// userRow maps auth.User onto yd_auth_users. | userRow 把 auth.User 映射到 yd_auth_users。
//
// There is no primary key column: inside a tenant the id is unique, but the same
// id may legitimately exist in another tenant, so uniqueness is expressed by
// yd_idx_auth_users_tenant_id on (tenant_id, id) — exactly the pair every lookup
// filters on. | 这里没有单列主键：租户内 ID 唯一，但同一个 ID 合法地可以存在于另一个租户，
// 因此唯一性由 (tenant_id, id) 上的 yd_idx_auth_users_tenant_id 表达 —— 这正是每次查询都用来过滤的那一对。
//
// Roles is JSON text produced by jsonx, and EncryptedSecKey is the SM4 ciphertext
// as raw bytes. | Roles 是 jsonx 生成的 JSON 文本，EncryptedSecKey 是 SM4 密文的原始字节。
type userRow struct {
	// CreatedAt is the Service clock time; ListUsers orders by it inside the tenant. | CreatedAt 是 Service 时钟时间；ListUsers 在租户内按它排序。
	CreatedAt time.Time `gorm:"column:created_at;index:yd_idx_auth_users_tenant_created_at,priority:2"`
	// UpdatedAt is the Service clock time of the last mutation. | UpdatedAt 是最后修改的 Service 时钟时间。
	UpdatedAt time.Time `gorm:"column:updated_at"`
	// ID is the user id. | ID 是用户 ID。
	ID string `gorm:"column:id;type:varchar(64);not null;uniqueIndex:yd_idx_auth_users_tenant_id,priority:2"`
	// TenantID is the owning tenant; it starts every index so a tenant can be scanned in isolation. | TenantID 是所属租户；它位于每个索引的首列，租户因此可以独立扫描。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:yd_idx_auth_users_tenant_id,priority:1;uniqueIndex:yd_idx_auth_users_tenant_username,priority:1;index:yd_idx_auth_users_tenant_created_at,priority:1"`
	// Username is the sign-in name as created. | Username 是创建时原样的登录名。
	Username string `gorm:"column:username;type:varchar(128);not null"`
	// UsernameKey is the folded sign-in name; yd_idx_auth_users_tenant_username is unique on it. | UsernameKey 是折叠后的登录名；yd_idx_auth_users_tenant_username 在其上唯一。
	UsernameKey string `gorm:"column:username_key;type:varchar(128);not null;uniqueIndex:yd_idx_auth_users_tenant_username,priority:2"`
	// Email is optional and never a sign-in key. | Email 可选，且从不作为登录键。
	Email string `gorm:"column:email;type:varchar(191)"`
	// DisplayName is optional. | DisplayName 可选。
	DisplayName string `gorm:"column:display_name;type:varchar(191)"`
	// PasswordHash is the guomi password hash; no plaintext is ever stored. | PasswordHash 是 guomi 口令散列；明文从不落库。
	PasswordHash string `gorm:"column:password_hash;type:varchar(255)"`
	// Status is "active" or "disabled". | Status 取 "active" 或 "disabled"。
	Status string `gorm:"column:status;type:varchar(32);not null"`
	// Roles is the JSON text of the role names, empty when there are none. | Roles 是角色名的 JSON 文本，没有角色时为空串。
	Roles string `gorm:"column:roles;type:text"`
	// EncryptedSecKey is the SM4-GCM sealed per-user JWT key; size keeps MySQL on varbinary. | EncryptedSecKey 是 SM4-GCM 封装的每用户 JWT 密钥；size 让 MySQL 落在 varbinary 上。
	EncryptedSecKey []byte `gorm:"column:encrypted_sec_key;size:4096"`
}

// TableName pins the table name. | TableName 固定表名。
func (userRow) TableName() string { return tableUsers }

// roleRow maps auth.Role onto yd_auth_roles. | roleRow 把 auth.Role 映射到 yd_auth_roles。
//
// The permission list is not a column: it lives in yd_auth_role_permissions as one
// row per capability, so a permission can be queried and indexed on its own. | 权限列表不是一列：
// 它存放在 yd_auth_role_permissions 中、每个能力一行，因此权限本身可以被单独查询与索引。
type roleRow struct {
	// ID is the role id. | ID 是角色 ID。
	ID string `gorm:"column:id;type:varchar(64);not null;uniqueIndex:yd_idx_auth_roles_tenant_id,priority:2"`
	// TenantID is the owning tenant and the first column of both indexes. | TenantID 是所属租户，也是两个索引的首列。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:yd_idx_auth_roles_tenant_id,priority:1;uniqueIndex:yd_idx_auth_roles_tenant_name,priority:1"`
	// Name is the unique role name inside the tenant; the name is what users reference. | Name 是租户内唯一的角色名；用户引用的正是它。
	Name string `gorm:"column:name;type:varchar(128);not null;uniqueIndex:yd_idx_auth_roles_tenant_name,priority:2"`
	// Description is the optional note. | Description 是可选说明。
	Description string `gorm:"column:description;type:varchar(255)"`
}

// TableName pins the table name. | TableName 固定表名。
func (roleRow) TableName() string { return tableRoles }

// rolePermissionRow is one granted capability of one role. | rolePermissionRow 是一个角色的一项被授予能力。
//
// The surrogate id exists for portability: a composite primary key would work on
// SQLite and PostgreSQL, but MySQL needs every primary key column to be NOT NULL
// with a fixed width, and a surrogate integer keeps the layout identical on all
// three. Uniqueness itself is enforced by the natural key. | 代理主键是为了可移植性：
// 复合主键在 SQLite 与 PostgreSQL 上可行，但 MySQL 要求主键每一列都非空且定长；
// 整数代理键让三种数据库的表结构完全一致。唯一性本身由自然键保证。
type rolePermissionRow struct {
	// TenantID is the owning tenant and the head of both indexes, so a role's permissions are never read across tenants. | TenantID 是所属租户，也是两个索引的首列，角色的权限绝不会跨租户读取。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:yd_idx_auth_role_permissions_tenant_role_perm,priority:1;index:yd_idx_auth_role_permissions_tenant_role,priority:1"`
	// RoleID is the role the permission is granted to. | RoleID 是被授予权限的角色。
	RoleID string `gorm:"column:role_id;type:varchar(64);not null;uniqueIndex:yd_idx_auth_role_permissions_tenant_role_perm,priority:2;index:yd_idx_auth_role_permissions_tenant_role,priority:2"`
	// Permission is the capability token, unique per role. | Permission 是能力标识，在同一角色内唯一。
	Permission string `gorm:"column:permission;type:varchar(191);not null;uniqueIndex:yd_idx_auth_role_permissions_tenant_role_perm,priority:3"`
	// ID is the surrogate primary key. | ID 是代理主键。
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
}

// TableName pins the table name. | TableName 固定表名。
func (rolePermissionRow) TableName() string { return tableRolePermissions }

// tenantFromRow converts a stored row into the domain type. | tenantFromRow 把库中行转换为领域类型。
func tenantFromRow(row *tenantRow) *auth.Tenant {
	if row == nil {
		return nil
	}
	return &auth.Tenant{
		ID:        row.ID,
		Slug:      row.Slug,
		Name:      row.Name,
		Status:    auth.Status(row.Status),
		CreatedAt: row.CreatedAt,
	}
}

// tenantToRow converts a domain tenant into its row. | tenantToRow 把领域租户转换为其行。
func tenantToRow(t *auth.Tenant) tenantRow {
	return tenantRow{
		ID:        t.ID,
		Slug:      t.Slug,
		SlugKey:   keyOf(t.Slug),
		Name:      t.Name,
		Status:    string(t.Status),
		CreatedAt: t.CreatedAt,
	}
}

// userFromRow converts a stored row into the domain type. | userFromRow 把库中行转换为领域类型。
//
// A malformed roles column is reported as ErrCorruptRow instead of being silently
// read as "no roles", because a role that vanishes without a trace is exactly the
// kind of silent privilege change this package must not produce. | roles 列损坏时报
// ErrCorruptRow，而不是静默读成「没有角色」：权限无声无息地消失，正是本包绝不能产生的那类静默变更。
func userFromRow(row *userRow) (*auth.User, error) {
	if row == nil {
		return nil, nil
	}
	roles, err := decodeRoles(row.Roles)
	if err != nil {
		return nil, err
	}
	return &auth.User{
		ID:              row.ID,
		TenantID:        row.TenantID,
		Username:        row.Username,
		Email:           row.Email,
		DisplayName:     row.DisplayName,
		PasswordHash:    row.PasswordHash,
		Status:          auth.Status(row.Status),
		EncryptedSecKey: append([]byte(nil), row.EncryptedSecKey...),
		Roles:           roles,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}, nil
}

// userToRow converts a domain user into its row. | userToRow 把领域用户转换为其行。
func userToRow(u *auth.User) (userRow, error) {
	roles, err := encodeRoles(u.Roles)
	if err != nil {
		return userRow{}, err
	}
	return userRow{
		ID:              u.ID,
		TenantID:        u.TenantID,
		Username:        u.Username,
		UsernameKey:     keyOf(u.Username),
		Email:           u.Email,
		DisplayName:     u.DisplayName,
		PasswordHash:    u.PasswordHash,
		Status:          string(u.Status),
		EncryptedSecKey: append([]byte(nil), u.EncryptedSecKey...),
		Roles:           roles,
		CreatedAt:       u.CreatedAt,
		UpdatedAt:       u.UpdatedAt,
	}, nil
}

// roleFromRow converts a stored row plus its permissions into the domain type. | roleFromRow 把库中行与其权限转换为领域类型。
func roleFromRow(row *roleRow, perms []string) *auth.Role {
	if row == nil {
		return nil
	}
	return &auth.Role{
		ID:          row.ID,
		TenantID:    row.TenantID,
		Name:        row.Name,
		Description: row.Description,
		Permissions: perms,
	}
}

// encodeRoles renders a role list as the JSON text of the roles column. | encodeRoles 把角色列表渲染成 roles 列的 JSON 文本。
//
// An empty list becomes an empty string so the column never holds "null", and a
// decoded empty list becomes nil — the same shape the memory implementation hands
// out, which keeps the two implementations comparable field by field. | 空列表写成空串，
// 使该列永不存放 "null"；解码出的空列表归一为 nil —— 与内存实现交出的形状一致，
// 两个实现因此可以逐字段比较。
func encodeRoles(roles []string) (string, error) {
	if len(roles) == 0 {
		return "", nil
	}
	raw, err := jsonx.Marshal(roles)
	if err != nil {
		return "", errx.Wrap(err, ErrCorruptRow)
	}
	return string(raw), nil
}

// decodeRoles parses the roles column back into a list. | decodeRoles 把 roles 列解析回列表。
func decodeRoles(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var roles []string
	if err := jsonx.Unmarshal([]byte(raw), &roles); err != nil {
		return nil, errx.WrapDetail(err, ErrCorruptRow, "roles column is not valid JSON | roles 列不是合法 JSON")
	}
	if len(roles) == 0 {
		return nil, nil
	}
	return roles, nil
}
