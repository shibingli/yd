// Package gormx 是 yd 框架的 GORM 装配层：把配置、日志与连接池收敛为一次 Open。
//
// 职责边界：
//   - 依据 Options（通常来自 OptionsFromConfig）选择驱动并打开 *gorm.DB，
//     驱动支持 mysql / postgres / sqlite（sqlite 为纯 Go 实现，无 cgo）；
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

	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/logx"
)

// 支持的驱动名，取值为 Options.Driver 接受的字符串。
const (
	// DriverMySQL 是 MySQL 驱动名。
	DriverMySQL = "mysql"
	// DriverPostgres 是 PostgreSQL 驱动名。
	DriverPostgres = "postgres"
	// DriverSQLite 是 SQLite 驱动名（纯 Go，无 cgo）。
	DriverSQLite = "sqlite"
)

// 框架默认值，与 config.Defaults 注册的 db.* 默认键保持一致。
const (
	// defaultMaxOpenConns 是连接池最大打开连接数的默认值（对应 db.maxconn）。
	defaultMaxOpenConns = 100
	// defaultMaxIdleConns 是连接池最大空闲连接数的默认值（对应 db.maxidle）。
	defaultMaxIdleConns = 10
	// defaultSlowThreshold 是慢查询阈值的默认值（对应 db.slow_threshold）。
	defaultSlowThreshold = 200 * time.Millisecond
	// redactedPlaceholder 是敏感实参脱敏后的替代文本。
	redactedPlaceholder = "?"
)

// 包级哨兵错误：所有失败路径都引用下列声明，调用方一律用 errors.Is 判定。
var (
	// ErrUnsupportedDriver indicates that Options.Driver is not one of the supported drivers. | ErrUnsupportedDriver 表示 Options.Driver 不在受支持的驱动之列。
	ErrUnsupportedDriver = errx.New("gormx: unsupported driver | gormx：不支持的驱动")
	// ErrNilDB indicates that a lifecycle helper received a nil *gorm.DB. | ErrNilDB 表示生命周期函数收到了 nil 的 *gorm.DB。
	ErrNilDB = errx.New("gormx: nil *gorm.DB | gormx：*gorm.DB 为 nil")
	// ErrOpenFailed indicates that gorm.Open failed. | ErrOpenFailed 表示 gorm.Open 失败。
	ErrOpenFailed = errx.New("gormx: failed to open database | gormx：打开数据库失败")
	// ErrConnPoolUnavailable indicates that the underlying *sql.DB could not be retrieved from *gorm.DB. | ErrConnPoolUnavailable 表示无法从 *gorm.DB 取回底层 *sql.DB。
	ErrConnPoolUnavailable = errx.New("gormx: underlying *sql.DB unavailable | gormx：无法获取底层 *sql.DB")
	// ErrMigrateFailed indicates that AutoMigrate failed. | ErrMigrateFailed 表示自动迁移失败。
	ErrMigrateFailed = errx.New("gormx: auto migrate failed | gormx：自动迁移失败")
	// ErrHealthCheckFailed indicates that the database ping failed. | ErrHealthCheckFailed 表示数据库探活失败。
	ErrHealthCheckFailed = errx.New("gormx: database health check failed | gormx：数据库探活失败")
	// ErrCloseFailed indicates that closing the connection pool failed. | ErrCloseFailed 表示关闭连接池失败。
	ErrCloseFailed = errx.New("gormx: failed to close database | gormx：关闭数据库失败")
)

// Options 描述一次 Open 的输入参数，零值即框架默认配置。
type Options struct {
	// Driver 是驱动名，支持 mysql / postgres / sqlite；空值按框架默认的 sqlite 处理。
	Driver string
	// DSN 是驱动对应的数据源串；SQLite 可用 "file::memory:?cache=shared" 打开内存库。
	DSN string
	// LogLevel 是 GORM 日志级别，接受 silent / error / warn / info，
	// 空值与无法识别的取值回落到 warn（慢查询与错误）。
	LogLevel string
	// MaxOpenConns 是连接池最大打开连接数，<=0 时取默认值 100。
	MaxOpenConns int
	// MaxIdleConns 是连接池最大空闲连接数，<=0 时取默认值 10。
	MaxIdleConns int
	// ConnMaxLifetime 是连接最长存活时间，0 表示不限（对应 sql.DB 的默认行为）。
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime 是连接最长空闲时间，0 表示不限（对应 sql.DB 的默认行为）。
	ConnMaxIdleTime time.Duration
	// SlowThreshold 是慢查询阈值，<=0 时取默认值 200ms。
	SlowThreshold time.Duration
	// PrepareStmt 是配置层 db.prepare_stmt 的映射字段。
	//
	// 预编译语句缓存属于框架硬规则，Open 恒以 true 装配（零值即框架默认），
	// 以免下游误关导致的吞吐退化与注入面扩大；该字段保留用于与配置层对齐。
	PrepareStmt bool
	// SkipDefaultTransaction 为 true 时，单条写操作不再包在事务里执行。
	SkipDefaultTransaction bool
}

// Config 把 Options 与 logx 日志器绑在一起，便于在装配层之间整体传递。
type Config struct {
	// Logger 是 GORM 日志的承载者；为 nil 时使用 logx.Default()。
	Logger *logx.Logger
	Options
}

// Open 打开数据库并完成连接池、日志与预编译语句的装配。
//
// Driver 为空时按框架默认的 sqlite 处理，其它取值返回包装了 ErrUnsupportedDriver 的错误。
// l 为 nil 时回落到 logx.Default()；GORM 的日志级别由 Options.LogLevel 决定。
// 打开成功后连接池参数立即生效，调用方负责最终调用 Close 释放资源。
func Open(o Options, l *logx.Logger) (*gorm.DB, error) {
	dialector, err := dialectorFor(o.Driver, o.DSN)
	if err != nil {
		return nil, err
	}
	if l == nil {
		l = logx.Default()
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger:                 NewLogger(l, o.SlowThreshold, o.LogLevel),
		PrepareStmt:            true, // 框架硬规则：预编译语句缓存默认开启
		SkipDefaultTransaction: o.SkipDefaultTransaction,
	})
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

// Open 用自身携带的 Options 与 Logger 打开数据库，等价于 Open(c.Options, c.Logger)。
func (c Config) Open() (*gorm.DB, error) { return Open(c.Options, c.Logger) }

// dialectorFor 把驱动名映射为 GORM 方言；空驱动名按框架默认的 SQLite 处理。
func dialectorFor(driver, dsn string) (gorm.Dialector, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", DriverSQLite:
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

// AutoMigrate 迁移 models 对应的表结构；db 为 nil 时返回 ErrNilDB，失败返回 ErrMigrateFailed。
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

// Close 关闭底层连接池并释放全部连接；db 为 nil 时返回 ErrNilDB，失败返回 ErrCloseFailed。
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
