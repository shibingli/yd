// Package gormstore implements auth.Store on top of GORM, so tenants, users, roles
// and their permissions survive a process restart and are shared by every replica. | Package gormstore
// 用 GORM 实现 auth.Store，使租户、用户、角色及其权限在进程重启后依然存在，并可被多个副本共享。
//
// Boundary | 边界：
//
//   - the auth package itself stays free of any ORM (a repository guard enforces it);
//     this subpackage is the one place where GORM is allowed to appear. | auth 包自身保持零 ORM 依赖
//     （仓库守卫会强制这一点），GORM 只允许出现在本子包。
//   - New accepts an already opened *gorm.DB (see data/gormx) and never opens a
//     connection; Migrate builds the tables and every index. | New 接受已经打开的 *gorm.DB
//     （见 data/gormx），自身绝不打开连接；Migrate 负责建表与建全部索引。
//   - the contract is the one of auth.Store, aligned line by line with the memory
//     implementation: every tenant-scoped method writes its tenantID into the SQL
//     WHERE clause, while the global lookup and membership methods use the globally
//     unique user id or username_key; a foreign or unknown entity reports
//     auth.ErrNotFound (never auth.ErrForbidden), and a unique-constraint violation
//     wraps auth.ErrConflict. | 契约即 auth.Store 的契约，并与内存实现逐条对齐：
//     所有按租户的方法都把 tenantID 写进 SQL 的 WHERE，全局查找与成员关系方法则使用全局唯一的
//     用户 ID 或 username_key；外部或未知实体报 auth.ErrNotFound（绝不报 auth.ErrForbidden）；
//     唯一约束冲突包装 auth.ErrConflict。
//
// Tables | 表：
//
//	yd_auth_tenants            租户
//	yd_auth_users              全局用户（含口令散列与 sec_key 密文）
//	yd_auth_user_tenants       用户与租户的成员关系（含各租户角色与默认标记）
//	yd_auth_roles              角色
//	yd_auth_role_permissions   角色与权限的关联表
//
// Table names and index names are written out in full, prefix included: GORM takes an explicit TableName() and an
// explicit index tag verbatim and never runs them through the naming strategy, so the yd_ prefix has to be typed
// here as it is in data/gormx. | 表名与索引名一律写全，含前缀：GORM 对显式的 TableName() 与显式的索引标签
// 直接采用其返回值、绝不经过命名策略，因此 yd_ 前缀在这里必须像在 data/gormx 里那样手写出来。
//
// The tables carry the yd_auth_ prefix because the framework owns their DDL: the yd_ prefix marks the framework's
// namespace, so a generated project's own users or roles table can never collide with yd_auth_users or
// yd_auth_roles, while the auth_ segment keeps saying which part of the framework owns them. | 这些表带 yd_auth_
// 前缀，因为它们的 DDL 由框架拥有：yd_ 前缀划出框架的命名空间，生成项目自己的 users 或 roles 表永远不会与
// yd_auth_users 或 yd_auth_roles 撞名；中间的 auth_ 段则继续说明它们属于框架的哪个领域。
//
// The indexes carry the derived form the framework's naming strategy gives every other index —
// yd_idx_ + the table name without the yd_ prefix + the column (yd_idx_auth_users_tenant_id) — so an explicit name
// and a derived one are indistinguishable in the database. | 索引采用框架命名策略给予其它所有索引的派生形态——
// yd_idx_ + 去掉 yd_ 前缀的表名 + 列名（yd_idx_auth_users_tenant_id）——因此显式名与派生名在库里长得一模一样。
//
// Two columns deviate from a naive mapping, both because the memory implementation
// folds case before comparing: slugs and usernames are stored as entered in
// slug/username plus a case-folded copy in slug_key/username_key, and the unique
// indexes sit on the folded copy. A unique index on the raw column would accept
// "Demo" next to "demo" — two tenants, or two accounts, that the contract says are
// one. | 有两列偏离了直白映射，原因都在内存实现：比较之前它会做大小写折叠。
// slug/username 按输入原样保存，折叠后的副本存进 slug_key/username_key，唯一索引建在折叠列上。
// 若把唯一索引建在原始列上，"Demo" 与 "demo" 会被当成两个租户或两个账号，而契约认定它们是一个。
package gormstore

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/shibingli/yd/security/auth"
	"github.com/shibingli/ydx/config"
	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
)

// Package-level sentinels; callers decide with errors.Is. | 包级哨兵错误，调用方一律用 errors.Is 判定。
var (
	// ErrNilDB reports a nil *gorm.DB handed to New or Migrate. | ErrNilDB 表示传给 New 或 Migrate 的 *gorm.DB 为空。
	ErrNilDB = errs.New("gormstore: nil *gorm.DB | gormstore：*gorm.DB 为空")
	// ErrMigrateFailed reports a failed table or index migration. | ErrMigrateFailed 表示建表或建索引失败。
	ErrMigrateFailed = errs.New("gormstore: migrate failed | gormstore：迁移失败")
)

// Table names of the four tables this package owns. | 本包拥有的四张表的表名。
const (
	// tableTenants holds one row per tenant. | tableTenants 每个租户一行。
	tableTenants = "yd_auth_tenants"
	// tableUsers holds one row per global user. | tableUsers 每个全局用户一行。
	tableUsers = "yd_auth_users"
	// tableUserTenants holds one row per user-tenant membership. | tableUserTenants 每条用户-租户成员关系一行。
	tableUserTenants = "yd_auth_user_tenants"
	// tableRoles holds one row per role. | tableRoles 每个角色一行。
	tableRoles = "yd_auth_roles"
	// tableRolePermissions holds one row per granted capability. | tableRolePermissions 每项被授予的能力一行。
	tableRolePermissions = "yd_auth_role_permissions"
)

// Index names, declared once so Migrate and the tests cannot drift apart. | 索引名，集中声明一次，使 Migrate 与测试不会漂移。
const (
	indexTenantsSlug          = "yd_idx_auth_tenants_slug"
	indexTenantsCreatedAt     = "yd_idx_auth_tenants_created_at"
	indexUsersID              = "yd_idx_auth_users_id"
	indexUsersUsername        = "yd_idx_auth_users_username"
	indexUsersCreatedAt       = "yd_idx_auth_users_created_at"
	indexUsersHomeTenant      = "yd_idx_auth_users_home_tenant"
	indexUsersDefaultTenant   = "yd_idx_auth_users_default_tenant"
	indexUserTenantsUnique    = "yd_idx_auth_user_tenants_user_tenant"
	indexUserTenantsTenant    = "yd_idx_auth_user_tenants_tenant"
	indexUserTenantsTenantAt  = "yd_idx_auth_user_tenants_tenant_created_at"
	indexUserTenantsUserDeflt = "yd_idx_auth_user_tenants_user_default"
	indexRolesTenantID        = "yd_idx_auth_roles_tenant_id"
	indexRolesTenantName      = "yd_idx_auth_roles_tenant_name"
	indexRolePermsUnique      = "yd_idx_auth_role_permissions_tenant_role_perm"
	indexRolePermsTenantRole  = "yd_idx_auth_role_permissions_tenant_role"
)

// configQueryTimeout is the configuration key bounding one statement. | configQueryTimeout 是限制单条语句耗时的配置键。
const configQueryTimeout = "auth.gorm.query_timeout"

// Options configures a Store. | Options 配置 Store。
type Options struct {
	// Log receives adapter diagnostics; a nil value selects ydlog.Default(). | Log 接收适配器诊断；为空时使用 ydlog.Default()。
	Log *ydlog.Logger
	// QueryTimeout bounds a single statement; <= 0 leaves the caller's context as it is. | QueryTimeout 限制单条语句的耗时；<= 0 时保持调用方上下文原样。
	//
	// A store that talks to a real database must not let a stuck connection hang a
	// request forever, and a value <= 0 keeps the choice with the caller. | 连真实数据库的存储不能让卡住的连接把请求永久挂住；
	// 取值 <= 0 时把这份决定权留给调用方。
	QueryTimeout time.Duration
}

// OptionsFromConfig maps the auth.gorm section onto Options. | OptionsFromConfig 把配置的 auth.gorm 段映射为 Options。
//
// Keys | 键：
//
//	auth.gorm.query_timeout  单条语句的上限，缺省为 0（不额外设限）
//
// A nil Config yields the zero Options. | Config 为空时返回零值 Options。
func OptionsFromConfig(c *config.Config) Options {
	if c == nil {
		return Options{}
	}
	return Options{
		QueryTimeout: c.Duration(configQueryTimeout),
		// The logger does not come from configuration: the entry point assembles it once and injects it, exactly as in data/gormx.
		// 日志器不来自配置：它由入口装配一次并注入（与 data/gormx 同一口径）。
	}
}

// Store is the GORM-backed auth.Store. | Store 是基于 GORM 的 auth.Store。
//
// It is safe for concurrent use: *gorm.DB is, and the adapter keeps no mutable
// state of its own. | 它并发安全：*gorm.DB 本身如此，而适配器不持有任何可变状态。
type Store struct {
	// db is the connection pool opened by the caller. | db 是调用方打开的连接池。
	db *gorm.DB
	// log receives adapter diagnostics. | log 接收适配器诊断。
	log *ydlog.Logger
	// queryTimeout bounds a single statement, 0 meaning "no extra bound". | queryTimeout 限制单条语句耗时，0 表示不额外设限。
	queryTimeout time.Duration
}

// The compiler pins the contract: every method of auth.Store is implemented below. | 编译期固定契约：auth.Store 的每个方法都在下面实现。
var _ auth.Store = (*Store)(nil)

// New returns a Store over an already opened connection. | New 基于已打开的连接返回 Store。
//
// It never opens or closes a connection: the pool belongs to the caller, who stays
// free to hand the same *gorm.DB to other repositories. A nil db is refused with
// ErrNilDB, so a misassembled application fails at startup instead of on the first
// request. | 它不打开也不关闭连接：连接池属于调用方，可以继续把同一个 *gorm.DB 交给别的仓储。
// db 为空时以 ErrNilDB 拒绝，装配错误在启动期就暴露，而不是等到第一次请求。
func New(db *gorm.DB, o Options) (*Store, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	log := o.Log
	if log == nil {
		log = ydlog.Default()
	}
	return &Store{db: db, log: log, queryTimeout: o.QueryTimeout}, nil
}

// Migrate creates the four auth_* tables together with every index. | Migrate 建立四张 auth_* 表及其全部索引。
//
// It is idempotent: AutoMigrate only adds what is missing, so calling it on every
// start is the intended usage. A nil db is refused with ErrNilDB and any failure is
// wrapped into ErrMigrateFailed. | 它是幂等的：AutoMigrate 只补齐缺失的部分，因此每次启动都调用正是预期用法。
// db 为空时以 ErrNilDB 拒绝，任何失败都包装进 ErrMigrateFailed。
func Migrate(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return ErrNilDB
	}
	if err := db.WithContext(ctx).AutoMigrate(
		&tenantRow{},
		&userRow{},
		&userTenantRow{},
		&roleRow{},
		&rolePermissionRow{},
	); err != nil {
		return errs.Wrap(err, ErrMigrateFailed)
	}
	return errs.Wrap(backfillMemberships(db.WithContext(ctx)), ErrMigrateFailed)
}

// backfillMemberships upgrades a database written before memberships existed. | backfillMemberships 升级成员关系出现之前写入的数据库。
//
// The old user row was tenant-scoped and carried its roles in a roles column; the new model
// keeps one global user plus yd_auth_user_tenants. This copies each legacy row into a home
// membership and fills default_tenant_id, so an existing project keeps signing in after the
// upgrade instead of losing every account to a missing membership. | 旧的用户行是租户内记录，
// 角色放在 roles 列里；新模型是一条全局用户加 yd_auth_user_tenants。这里把每条旧记录复制成
// 身份租户的成员关系并补齐 default_tenant_id，使既有项目升级后仍能登录，而不是因为缺少成员关系
// 丢掉全部账号。
//
// It is idempotent: the NOT EXISTS clause and the WHERE on an empty default make a second run a
// no-op. | 它是幂等的：NOT EXISTS 子句与「默认租户为空」的条件让第二次运行什么都不做。
func backfillMemberships(db *gorm.DB) error {
	if err := db.Exec(
		"UPDATE " + tableUsers + " SET default_tenant_id = tenant_id WHERE default_tenant_id IS NULL OR default_tenant_id = ''",
	).Error; err != nil {
		return err
	}
	// The roles column only exists on databases created before memberships; a fresh schema has
	// no such column and there is nothing to copy. | roles 列只存在于成员关系出现之前的库；
	// 全新 schema 没有该列，因此没有可复制的数据。
	if !db.Migrator().HasColumn(tableUsers, "roles") {
		return nil
	}
	return db.Exec(
		"INSERT INTO " + tableUserTenants + " (user_id, tenant_id, roles, is_default, created_at, updated_at) " +
			"SELECT u.id, u.tenant_id, COALESCE(u.roles, ''), true, u.created_at, u.updated_at " +
			"FROM " + tableUsers + " u " +
			"WHERE NOT EXISTS (SELECT 1 FROM " + tableUserTenants + " m WHERE m.user_id = u.id AND m.tenant_id = u.tenant_id)",
	).Error
}

// scoped derives a context bounded by Options.QueryTimeout. | scoped 派生一个受 Options.QueryTimeout 限制的上下文。
//
// An earlier deadline on the caller's context always wins, because WithTimeout
// keeps the nearest one. | 调用方上下文上更早的截止时间总是生效，因为 WithTimeout 保留最近的那一个。
func (s *Store) scoped(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.queryTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.queryTimeout)
}

// countRows returns how many rows of model match the condition. | countRows 返回 model 下满足条件的行数。
//
// It backs the "does it already exist" pre-checks, which exist to reproduce the
// diagnostics of the memory implementation; the unique indexes remain the real
// guarantee against a concurrent duplicate. | 它支撑「是否已存在」的预检，预检的目的在于复现内存实现的报文；
// 真正对抗并发重复写入的仍然是唯一索引。
func countRows(tx *gorm.DB, model any, cond string, args ...any) (int64, error) {
	var count int64
	if err := tx.Model(model).Where(cond, args...).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// existsTenant reports whether the tenant id is known to the store. | existsTenant 报告租户 ID 是否已知。
func existsTenant(tx *gorm.DB, tenantID string) (bool, error) {
	count, err := countRows(tx, &tenantRow{}, "id = ?", tenantID)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// notFound translates GORM's sentinel into the one of the auth contract. | notFound 把 GORM 的哨兵翻译成 auth 契约的哨兵。
//
// auth.ErrNotFound is what a caller can rely on for "unknown or foreign entity":
// the two are deliberately indistinguishable, so a probe learns nothing about
// another tenant's data. | auth.ErrNotFound 是调用方对「未知或外部实体」可以依赖的哨兵：
// 两者刻意不可区分，探测者无法借此了解其它租户的数据。
func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return auth.ErrNotFound
	}
	return err
}

// duplicateMarkers are the driver messages that mean "unique constraint violation". | duplicateMarkers 是表示「唯一约束冲突」的驱动报文。
//
// gormx opens connections without TranslateError, so GORM's own ErrDuplicatedKey is
// only reachable when the caller enabled the translation; the textual markers cover
// the three supported dialects either way. Detecting a duplicate is the adapter's
// job — the caller of this package still decides with errors.Is(auth.ErrConflict). | gormx 打开连接时未开启
// TranslateError，因此只有调用方自行开启翻译时 GORM 自身的 ErrDuplicatedKey 才可达；下面这些文本标记在三种方言上都能兜底。
// 「识别重复」是本适配器的职责，本包的调用方仍然一律用 errors.Is(auth.ErrConflict) 判定。
var duplicateMarkers = []string{
	"unique constraint failed", // SQLite
	"duplicate entry",          // MySQL
	"duplicate key value",      // PostgreSQL
	"unique violation",         // PostgreSQL (the wording of SQLSTATE 23505) | PostgreSQL（SQLSTATE 23505 的文案）
}

// isDuplicate reports whether err is a unique-constraint violation. | isDuplicate 报告 err 是否为唯一约束冲突。
func isDuplicate(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range duplicateMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// keyOf folds a slug or a username into its uniqueness key. | keyOf 把 Slug 或用户名折叠为唯一性键。
//
// The memory implementation does exactly the same before comparing, which is why
// "Demo" and "demo" are one tenant and "Alice" and "alice" are one account. | 内存实现在比较之前做的
// 是同一件事，因此 "Demo" 与 "demo" 是一个租户，"Alice" 与 "alice" 是一个账号。
func keyOf(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
