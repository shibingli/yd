// Package gormx 是 yd 框架的 GORM 装配层：把配置、日志与连接池收敛为一次 Open。
//
// 职责边界：
//   - 依据 Options（通常来自 OptionsFromConfig）选择驱动并打开 *gorm.DB，
//     驱动支持 mysql / postgres / sqlite（sqlite 为纯 Go 实现，无 cgo）；
//   - 施加框架命名策略：表统一带 yd_ 前缀，派生索引名统一取 yd_idx_ 形态（见 naming.go）；
//   - 把连接池参数落到 sql.DB，并按框架硬规则默认开启预编译语句缓存；
//   - 用 logx 承载 GORM 的日志：慢查询只经 logx.SlowQuery 打点（零反射、零字符串拼接），
//     并由 ParamsFilter 屏蔽口令、Token 等敏感实参；
//   - 提供 AutoMigrate / Health / Close 等生命周期辅助函数，失败路径统一返回包级哨兵错误。
//
// 不做什么：不定义模型与业务查询、不做连接以外的资源管理（缓存、迁移版本表），
// 也不替调用方决定 DSN 之外的重试与降级策略。
package gormx

import (
	"context"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
)

// Supported driver names: the strings Options.Driver accepts. | 支持的驱动名，取值为 Options.Driver 接受的字符串。
const (
	// DriverMySQL is the MySQL driver name. | DriverMySQL 是 MySQL 驱动名。
	DriverMySQL = "mysql"
	// DriverPostgres is the PostgreSQL driver name. | DriverPostgres 是 PostgreSQL 驱动名。
	DriverPostgres = "postgres"
	// DriverSQLite is the SQLite driver name (pure Go, no cgo). | DriverSQLite 是 SQLite 驱动名（纯 Go，无 cgo）。
	DriverSQLite = "sqlite"
)

// Framework defaults, kept in step with the db.* default keys registered by config.Defaults. | 框架默认值，与 config.Defaults 注册的 db.* 默认键保持一致。
const (
	// defaultMaxOpenConns is the default maximum number of open connections in the pool (db.maxconn). | defaultMaxOpenConns 是连接池最大打开连接数的默认值（对应 db.maxconn）。
	defaultMaxOpenConns = 100
	// defaultMaxIdleConns is the default maximum number of idle connections in the pool (db.maxidle). | defaultMaxIdleConns 是连接池最大空闲连接数的默认值（对应 db.maxidle）。
	defaultMaxIdleConns = 10
	// defaultSlowThreshold is the default slow-query threshold (db.slow_threshold). | defaultSlowThreshold 是慢查询阈值的默认值（对应 db.slow_threshold）。
	defaultSlowThreshold = 200 * time.Millisecond
	// redactedPlaceholder is the replacement text a sensitive argument is redacted to. | redactedPlaceholder 是敏感实参脱敏后的替代文本。
	redactedPlaceholder = "?"
)

// Package-level sentinel errors: every failure path refers to the declarations below and callers always decide with errors.Is. | 包级哨兵错误：所有失败路径都引用下列声明，调用方一律用 errors.Is 判定。
var (
	// ErrUnsupportedDriver indicates that Options.Driver is not one of the supported drivers. | ErrUnsupportedDriver 表示 Options.Driver 不在受支持的驱动之列。
	ErrUnsupportedDriver = errx.New("gormx: unsupported driver | gormx：不支持的驱动")
	// ErrNilDB indicates that a lifecycle helper received a nil *gorm.DB. | ErrNilDB 表示生命周期函数收到了 nil 的 *gorm.DB。
	ErrNilDB = errx.New("gormx: nil *gorm.DB | gormx：*gorm.DB 为 nil")
	// ErrOpenFailed indicates that gorm.Open failed. | ErrOpenFailed 表示 gorm.Open 失败。
	ErrOpenFailed = errx.New("gormx: failed to open database | gormx：打开数据库失败")
	// ErrDatabaseMissing indicates that the server answered that the database the DSN named does not
	// exist, which is what lets automatic creation step in. | ErrDatabaseMissing 表示服务端答复连接串点名
	// 的数据库不存在，正是它让自动建库得以介入。
	ErrDatabaseMissing = errx.New("gormx: target database does not exist | gormx：目标数据库不存在")
	// ErrCreateDatabase indicates that automatic database creation failed. The original
	// "database does not exist" error and this one share one error chain, so both reasons stay
	// readable with errors.Is. | ErrCreateDatabase 表示自动建库失败。原始的「数据库不存在」错误与本
	// 错误挂在同一条错误链上，两个原因都能被 errors.Is 读到。
	ErrCreateDatabase = errx.New("gormx: failed to create database | gormx：创建数据库失败")
	// ErrConnPoolUnavailable indicates that the underlying *sql.DB could not be retrieved from *gorm.DB. | ErrConnPoolUnavailable 表示无法从 *gorm.DB 取回底层 *sql.DB。
	ErrConnPoolUnavailable = errx.New("gormx: underlying *sql.DB unavailable | gormx：无法获取底层 *sql.DB")
	// ErrMigrateFailed indicates that AutoMigrate failed. | ErrMigrateFailed 表示自动迁移失败。
	ErrMigrateFailed = errx.New("gormx: auto migrate failed | gormx：自动迁移失败")
	// ErrHealthCheckFailed indicates that the database ping failed. | ErrHealthCheckFailed 表示数据库探活失败。
	ErrHealthCheckFailed = errx.New("gormx: database health check failed | gormx：数据库探活失败")
	// ErrCloseFailed indicates that closing the connection pool failed. | ErrCloseFailed 表示关闭连接池失败。
	ErrCloseFailed = errx.New("gormx: failed to close database | gormx：关闭数据库失败")
)

// Options describes the input of one Open call; the zero value is the framework default configuration. | Options 描述一次 Open 的输入参数，零值即框架默认配置。
type Options struct {
	// Driver is the driver name, one of mysql / postgres / sqlite; an empty value reads as the framework default sqlite. | Driver 是驱动名，支持 mysql / postgres / sqlite；空值按框架默认的 sqlite 处理。
	Driver string
	// DSN is the data source string of the driver; SQLite can open an in-memory database with "file::memory:?cache=shared". | DSN 是驱动对应的数据源串；SQLite 可用 "file::memory:?cache=shared" 打开内存库。
	DSN string
	// LogLevel 是 GORM 日志级别，接受 silent / error / warn / info，
	// 空值与无法识别的取值回落到 warn（慢查询与错误）。
	LogLevel string
	// MaxOpenConns is the maximum number of open connections in the pool; a value <=0 takes the default 100. | MaxOpenConns 是连接池最大打开连接数，<=0 时取默认值 100。
	MaxOpenConns int
	// MaxIdleConns is the maximum number of idle connections in the pool; a value <=0 takes the default 10. | MaxIdleConns 是连接池最大空闲连接数，<=0 时取默认值 10。
	MaxIdleConns int
	// ConnMaxLifetime is the longest lifetime of a connection, 0 meaning unlimited (sql.DB's own default). | ConnMaxLifetime 是连接最长存活时间，0 表示不限（对应 sql.DB 的默认行为）。
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime is the longest idle time of a connection, 0 meaning unlimited (sql.DB's own default). | ConnMaxIdleTime 是连接最长空闲时间，0 表示不限（对应 sql.DB 的默认行为）。
	ConnMaxIdleTime time.Duration
	// SlowThreshold is the slow-query threshold; a value <=0 takes the default 200ms. | SlowThreshold 是慢查询阈值，<=0 时取默认值 200ms。
	SlowThreshold time.Duration
	// PrepareStmt 是配置层 db.prepare_stmt 的映射字段。
	//
	// 预编译语句缓存属于框架硬规则，Open 恒以 true 装配（零值即框架默认），
	// 以免下游误关导致的吞吐退化与注入面扩大；该字段保留用于与配置层对齐。
	PrepareStmt bool
	// SkipDefaultTransaction, when true, stops wrapping a single write operation in a transaction. | SkipDefaultTransaction 为 true 时，单条写操作不再包在事务里执行。
	SkipDefaultTransaction bool
	// CreateDatabase controls automatic database creation: when the first connect fails with the
	// server's "the target database does not exist" answer (MySQL 1049 / PostgreSQL 3D000), Open
	// creates the database the DSN names once, and then connects once more. Somebody has to create
	// the database, and an account that is allowed to create it should not have to be told to.
	// | CreateDatabase 控制自动建库：首次连接以服务端「目标数据库不存在」的答复失败时
	// （MySQL 1049 / PostgreSQL 3D000），Open 建出连接串点名的那个库，然后重连一次。
	// 库总得有人建，而权限足够的账号不该还需要别人替它建。
	//
	// The risk, stated plainly: automatic creation makes a mistyped database name indistinguishable
	// from a database that is not there yet. A DSN that says prod_usr where prod_users was meant
	// creates an empty prod_usr and the process starts normally, so the mistake surfaces much later
	// as missing data instead of at startup as a failed connect. That is the inherent cost of the
	// feature, not a defect that will be fixed later — the only mitigations are the Warn log line
	// that carries the database name and the switch documented here. | 风险直说：自动建库把「库名写错」
	// 与「库确实还没建」变得无法区分。本该写 prod_users 却写成 prod_usr 的连接串会建出一个空的
	// prod_usr，进程照常启动，于是错误要等到数据缺失时才暴露，而不是在启动时以连接失败暴露。
	// 这是本特性固有的代价，不是日后会修掉的缺陷——唯一的缓解手段就是那行带着库名的 Warn 日志，
	// 以及这里写明的开关。
	//
	// Turn it off with db.create_database: false, YD_DB_CREATE_DATABASE=false, or this field set to
	// false on a hand-written Options; the connection then fails exactly as it did before and names
	// the missing database. | 关闭方式：db.create_database: false、YD_DB_CREATE_DATABASE=false，
	// 或在手写的 Options 里把本字段置为 false；关闭后连接照旧失败，并说出缺失的库名。
	//
	// The framework default is on: OptionsFromConfig answers true when db.create_database appears in
	// no layer at all (config file, environment, defaults), so a generated project gets automatic
	// creation without writing the key. A plain bool cannot tell "explicitly false" from "unset",
	// so a hand-written Options has to ask for it by setting this field to true. | 框架默认开启：
	// db.create_database 在任何一层（配置文件、环境变量、默认值）都没有出现时，OptionsFromConfig 给出
	// true，因此生成的项目无需写这个键就得到自动建库。bool 区分不了「显式 false」与「未设置」，
	// 所以手写的 Options 必须把本字段置为 true 才启用。
	CreateDatabase bool
}

// Config binds Options together with a logx logger so the pair travels between assembly layers as one. | Config 把 Options 与 logx 日志器绑在一起，便于在装配层之间整体传递。
type Config struct {
	// Logger carries GORM's logging; a nil value falls back to logx.Default(). | Logger 是 GORM 日志的承载者；为 nil 时使用 logx.Default()。
	Logger *logx.Logger
	Options
}

// Open 打开数据库并完成连接池、日志与预编译语句的装配。
//
// Driver 为空时按框架默认的 sqlite 处理，其它取值返回包装了 ErrUnsupportedDriver 的错误。
// l 为 nil 时回落到 logx.Default()；GORM 的日志级别由 Options.LogLevel 决定。
// 打开成功后连接池参数立即生效，调用方负责最终调用 Close 释放资源。
//
// When the driver answers that the target database does not exist and Options.CreateDatabase is on,
// Open creates that database once and connects once more, both bounded to one attempt per Open call.
// | 当驱动答复目标数据库不存在且 Options.CreateDatabase 开启时，Open 建一次库并重连一次，
// 每次 Open 调用都只各尝试一次。
func Open(o Options, l *logx.Logger) (*gorm.DB, error) {
	dialector, err := dialectorFor(o.Driver, o.DSN)
	if err != nil {
		return nil, err
	}
	if l == nil {
		l = logx.Default()
	}

	cfg := &gorm.Config{
		Logger:                 NewLogger(l, o.SlowThreshold, o.LogLevel),
		NamingStrategy:         DefaultNamingStrategy(), // Framework hard rule: every table and every derived index carries the yd_ prefix (see naming.go) | 框架硬规则：每张表与每个派生索引都带 yd_ 前缀（见 naming.go）
		PrepareStmt:            true,                    // Framework hard rule: the prepared statement cache is on by default | 框架硬规则：预编译语句缓存默认开启
		SkipDefaultTransaction: o.SkipDefaultTransaction,
		// Relations are maintained by application code (see CLAUDE.md §26); AutoMigrate must never emit a
		// foreign key even if a model accidentally declares an association. |
		// 关联由应用代码维护（见 CLAUDE.md §26）；即使模型误声明了关联，AutoMigrate 也绝不生成外键。
		DisableForeignKeyConstraintWhenMigrating: true,
		// TranslateError turns driver-specific unique/foreign-key failures into gorm.ErrDuplicatedKey and
		// gorm.ErrForeignKeyViolated, so callers classify them with errors.Is instead of matching message text. |
		// TranslateError 把驱动特有的唯一键/外键失败翻译为 gorm.ErrDuplicatedKey 与 gorm.ErrForeignKeyViolated，
		// 调用方因此用 errors.Is 判定，而不是匹配错误文案。
		TranslateError: true,
		// GORM's default clock is local time; the framework stores UTC so every dialect and every
		// consumer sees one timeline. | GORM 默认使用本地时钟；框架统一存 UTC，使每种方言与每个消费者
		// 看到同一条时间线。
		NowFunc: func() time.Time { return time.Now().UTC() },
	}
	db, err := openDatabaseFunc(dialector, cfg)
	// 只有错误码说明「目标库不存在」且开关开启时才建库；其余失败一律走原路径。
	// Only a "database does not exist" code with the switch on creates anything; every other failure
	// takes the original path.
	if err != nil && o.CreateDatabase && isMissingDatabaseError(o.Driver, err) {
		db, err = createAndReconnect(o, dialector, cfg, err, l)
	}
	if err != nil {
		driver := strings.TrimSpace(o.Driver)
		return nil, errx.WrapDetail(err, ErrOpenFailed, "driver %q | 驱动 %q", driver, driver)
	}
	if err := applyPool(db, o); err != nil {
		_ = Close(db)
		return nil, err
	}
	return db, nil
}

// Open opens the database with the Options and Logger this Config carries; it is Open(c.Options, c.Logger). | Open 用自身携带的 Options 与 Logger 打开数据库，等价于 Open(c.Options, c.Logger)。
func (c Config) Open() (*gorm.DB, error) { return Open(c.Options, c.Logger) }

// dialectorFor 把驱动名映射为 GORM 方言；空驱动名按框架默认的 SQLite 处理。
//
// Every dialector is handed the preset DSN (see PresetDSN), so a hand-written application gets the
// framework defaults for time zone, time handling, nullability strictness and character set without
// calling PresetDSN itself. | 每个方言拿到的都是预置后的连接串（见 PresetDSN），
// 因此手写应用无需自行调用 PresetDSN 即可获得时区、时间配置、空值严格性与字符集的框架默认值。
func dialectorFor(driver, dsn string) (gorm.Dialector, error) {
	dsn = PresetDSN(driver, dsn)
	switch driverKind(driver) {
	case DriverSQLite:
		return sqlite.Open(dsn), nil
	case DriverMySQL:
		return mysql.Open(dsn), nil
	case DriverPostgres:
		return postgres.Open(dsn), nil
	default:
		return nil, errx.Detail(ErrUnsupportedDriver,
			"driver %q is not one of mysql/postgres/sqlite | 驱动 %q 不在 mysql/postgres/sqlite 之列",
			driver, driver)
	}
}

// driverKind returns the canonical name of the driver a configured name selects: mysql, postgres or
// sqlite, with the empty name and both spellings of SQLite reading as sqlite, exactly as
// dialectorFor reads them. It returns "" for a name this package does not support, which is the one
// outcome callers must reject rather than act on. | driverKind 返回配置的驱动名所选择的规范驱动名：
// mysql、postgres 或 sqlite，其中空名与 sqlite 的各种写法都读作 sqlite，与 dialectorFor 的口径一致。
// 本包不支持的驱动名返回 ""，这是调用方必须拒绝、而不是据以行事的那一种结果。
//
// Every decision about a driver — building a dialect, classifying an error code, deciding whether
// creation takes part at all — goes through this one function, so "which name means which driver"
// is stated once. | 一切与驱动有关的判断——构造方言、判定错误码、决定是否参与建库——都经过这一处，
// 因此「哪个名字是哪个驱动」只写了一遍。
func driverKind(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", DriverSQLite:
		return DriverSQLite
	case DriverMySQL:
		return DriverMySQL
	case DriverPostgres:
		return DriverPostgres
	default:
		return ""
	}
}

// applyPool 把 Options 中的连接池参数落到 sql.DB，未显式给出的参数按框架默认补齐。
//
// 预编译语句缓存会把 *sql.DB 包装为 gorm.PreparedStmtDB，但 db.DB() 仍能取回同一
// *sql.DB，因此连接池设置作用于真正的连接池。
func applyPool(db *gorm.DB, o Options) error {
	sqlDB, err := db.DB()
	if err != nil {
		return errx.Wrap(err, ErrConnPoolUnavailable)
	}

	maxOpen := o.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = defaultMaxOpenConns
	}
	maxIdle := o.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = defaultMaxIdleConns
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(o.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(o.ConnMaxIdleTime)
	return nil
}

// AutoMigrate migrates the tables behind models; a nil db returns ErrNilDB and a failure returns ErrMigrateFailed. | AutoMigrate 迁移 models 对应的表结构；db 为 nil 时返回 ErrNilDB，失败返回 ErrMigrateFailed。
func AutoMigrate(db *gorm.DB, models ...any) error {
	if db == nil {
		return ErrNilDB
	}
	return errx.Wrap(db.AutoMigrate(models...), ErrMigrateFailed)
}

// Health 以 ctx 为限探活数据库（底层 sql.DB.PingContext）；
// db 为 nil 时返回 ErrNilDB，探活失败返回 ErrHealthCheckFailed。
func Health(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return ErrNilDB
	}
	sqlDB, err := db.DB()
	if err != nil {
		return errx.Wrap(err, ErrConnPoolUnavailable)
	}
	return errx.Wrap(sqlDB.PingContext(ctx), ErrHealthCheckFailed)
}

// Close closes the underlying connection pool and releases every connection; a nil db returns ErrNilDB and a failure returns ErrCloseFailed. | Close 关闭底层连接池并释放全部连接；db 为 nil 时返回 ErrNilDB，失败返回 ErrCloseFailed。
func Close(db *gorm.DB) error {
	if db == nil {
		return ErrNilDB
	}
	sqlDB, err := db.DB()
	if err != nil {
		return errx.Wrap(err, ErrConnPoolUnavailable)
	}
	return errx.Wrap(sqlDB.Close(), ErrCloseFailed)
}

// ParamsFilter 返回脱敏后的 SQL 与实参：SQL 原样返回，每个 "?" 位置上的实参
// 都被替换为字面量 "?"，从而避免 GORM 在格式化日志时把口令、Token 等敏感值写进日志。
//
// 返回的实参个数与 SQL 中 "?" 的个数一致，交由 gorm.Dialector.Explain 完成占位符替换；
// 采用 $1 编号占位符（如 PostgreSQL）的方言不会命中 "?"，其占位符本就不含实参明文。
//
// 本函数实现 gorm.ParamsFilter 的语义，logger 类型上的同名方法把它接入 GORM。
func ParamsFilter(ctx context.Context, sql string) (string, []any) {
	n := strings.Count(sql, redactedPlaceholder)
	if n == 0 {
		return sql, nil
	}
	params := make([]any, n)
	for i := range params {
		params[i] = redactedPlaceholder
	}
	return sql, params
}
