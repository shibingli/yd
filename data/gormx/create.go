package gormx

import (
	"errors"
	"net/url"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
)

// The error codes that mean "the target database does not exist", plus the constants the CREATE
// DATABASE statement and the maintenance connection are built from. | 判定「目标数据库不存在」用的错误码，
// 以及建库语句与维护连接用到的常量。
//
// Only error codes are matched: message texts drift with language and server version, so string
// matching would blur the self-healing "the database is not there yet" case into the same reading as
// a real failure (see §2.9.1 of the plan). | 判定只认错误码：错误文案随语言与服务端版本漂移，
// 字符串匹配会把「库确实还没建」这种可自愈的情况读成与真正的故障同一件事（见计划 §2.9.1）。
const (
	// mysqlErrBadDB is MySQL's ER_BAD_DB_ERROR (1049): the server answered that the database the DSN
	// named does not exist. | mysqlErrBadDB 是 MySQL 的 ER_BAD_DB_ERROR（1049）：
	// 服务端答复连接串点名的数据库不存在。
	mysqlErrBadDB = 1049
	// pgErrInvalidCatalogName is PostgreSQL's SQLSTATE 3D000 (invalid_catalog_name). | pgErrInvalidCatalogName 是 PostgreSQL 的 SQLSTATE 3D000（invalid_catalog_name）。
	pgErrInvalidCatalogName = "3D000"
	// pgErrDuplicateDatabase is PostgreSQL's SQLSTATE 42P04 (duplicate_database). | pgErrDuplicateDatabase 是 PostgreSQL 的 SQLSTATE 42P04（duplicate_database）。
	//
	// CREATE DATABASE has no IF NOT EXISTS, so a lost race against another replica surfaces as this
	// code and must be read as success: the database exists, which is all this layer wanted. | CREATE DATABASE 没有
	// IF NOT EXISTS，因此与另一个副本抢建失败时得到的就是这个码，必须按成功处理：库已经在了，
	// 这正是本层想要的结果。
	pgErrDuplicateDatabase = "42P04"
	// postgresMaintenanceDatabase is the maintenance database PostgreSQL always ships. | postgresMaintenanceDatabase 是 PostgreSQL 必然自带的维护库。
	postgresMaintenanceDatabase = "postgres"
	// mysqlPresetKeyCharset and mysqlPresetKeyCollation name the keys the preset table carries the
	// character set and collation under. | mysqlPresetKeyCharset 与 mysqlPresetKeyCollation
	// 是预置表里承载字符集与排序规则的键名。
	mysqlPresetKeyCharset   = "charset"
	mysqlPresetKeyCollation = "collation"
	// logLevelSilent is the GORM log level of the throwaway maintenance connection. | logLevelSilent 是一次性维护连接的 GORM 日志级别。
	logLevelSilent = "silent"
	// postgresDBNameParam is the libpq parameter that names the database. | postgresDBNameParam 是 libpq 里点名数据库的参数。
	postgresDBNameParam = "dbname"
)

// openDatabaseFunc is the gorm.Open call Open performs, behind a package-level variable so that tests
// can drive the create-then-retry path without a MySQL or PostgreSQL server. | openDatabaseFunc 是
// Open 实际执行的 gorm.Open，放在包级变量后面，使测试无需 MySQL / PostgreSQL 服务端即可驱动
// 「建库后重试」这条路径。
var openDatabaseFunc = func(dialector gorm.Dialector, cfg *gorm.Config) (*gorm.DB, error) {
	return gorm.Open(dialector, cfg)
}

// maintenanceDDLFunc runs one CREATE DATABASE statement on a throwaway connection to the server's
// maintenance database. | maintenanceDDLFunc 在一条连向服务器维护库的一次性连接上执行一条
// CREATE DATABASE 语句。
//
// It is the seam tests observe the generated SQL through, and the only place a maintenance connection
// is opened. | 它是测试观察生成 SQL 的注入口，也是全包唯一打开维护连接的地方。
var maintenanceDDLFunc = runMaintenanceDDL

// creationPlan carries everything one automatic database creation needs. | creationPlan 承载一次自动建库所需的全部输入。
type creationPlan struct {
	// maintenanceDSN is the connection string of the maintenance connection: the same server,
	// without the target database. | maintenanceDSN 是维护连接的连接串：同一台服务器，但不连目标库。
	maintenanceDSN string
	// statement is the CREATE DATABASE statement, with the database name already escaped. | statement 是已把库名转义好的 CREATE DATABASE 语句。
	statement string
	// database is the name the statement creates. | database 是语句要创建的库名。
	database string
}

// isMissingDatabaseError reports whether err is the server's "the target database does not exist"
// answer for driver, judged by error code through errors.As. | isMissingDatabaseError 判断 err
// 是否为 driver 服务端「目标数据库不存在」的答复，用 errors.As 按错误码判定。
//
// Every other error is reported as false on purpose: a server that is unreachable, a password that is
// wrong or a database that is merely forbidden must not trigger a CREATE DATABASE. | 其它错误一律返回
// false，这是刻意的：服务不可达、口令错误、库只是无权访问，都不得触发 CREATE DATABASE。
func isMissingDatabaseError(driver string, err error) bool {
	switch driverKind(driver) {
	case DriverMySQL:
		var me *mysqldriver.MySQLError
		return errors.As(err, &me) && me.Number == mysqlErrBadDB
	case DriverPostgres:
		var pe *pgconn.PgError
		return errors.As(err, &pe) && pe.Code == pgErrInvalidCatalogName
	default:
		// SQLite creates its database file itself, so it has no "database does not exist" code, and
		// every other driver stays out of automatic creation. | SQLite 的库文件由驱动自行创建，
		// 没有「库不存在」这一种错误码；其余驱动也都不参与自动建库。
		return false
	}
}

// isDuplicateDatabaseError reports whether err is PostgreSQL's 42P04 answer, which the CREATE
// DATABASE path must read as success because that statement has no IF NOT EXISTS. | isDuplicateDatabaseError
// 判断 err 是否为 PostgreSQL 的 42P04 答复；CREATE DATABASE 没有 IF NOT EXISTS，该路径必须把它读成成功。
func isDuplicateDatabaseError(driver string, err error) bool {
	if driverKind(driver) != DriverPostgres {
		return false
	}
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == pgErrDuplicateDatabase
}

// planCreation derives everything one automatic database creation needs from the DSN, or reports
// ok=false when it cannot be derived. | planCreation 从连接串推导一次自动建库所需的全部输入；
// 无法推导时返回 ok=false。
//
// ok=false means "do not attempt creation at all": the driver does not take part (SQLite creates its
// file by itself), the DSN cannot be parsed the way the driver parses it, or the DSN names no
// database. Guessing a name in any of those cases would create the wrong database, which is worse
// than reporting the original failure. | ok=false 表示「完全不尝试建库」：驱动不参与（SQLite 的库文件
// 由驱动自行创建）、连接串无法按驱动的方式解析，或连接串根本没有点名任何库。这些情况下猜一个库名去建，
// 只会建出错误的库，比如实上报原始失败更糟。
func planCreation(driver, dsn string) (creationPlan, bool) {
	name, ok := targetDatabase(driver, dsn)
	if !ok {
		return creationPlan{}, false
	}
	statement, ok := createDatabaseStatement(driver, name)
	if !ok {
		return creationPlan{}, false
	}
	maintenance, ok := maintenanceDSN(driver, dsn)
	if !ok {
		return creationPlan{}, false
	}
	return creationPlan{maintenanceDSN: maintenance, statement: statement, database: name}, true
}

// targetDatabase returns the database name dsn points at, read the way the driver reads it. | targetDatabase
// 返回 dsn 指向的库名，读取方式与驱动本身一致。
//
// The name comes out of the driver's own parser rather than out of string surgery, so a name that is
// percent-escaped or written in PostgreSQL's keyword/value form arrives at the CREATE DATABASE
// statement exactly as the driver understood it. | 库名取自驱动自己的解析器，而不是字符串手术：
// 无论库名是百分号转义，还是写在 PostgreSQL 的关键字/值形态里，进入 CREATE DATABASE 的都是驱动的理解结果。
func targetDatabase(driver, dsn string) (string, bool) {
	switch driverKind(driver) {
	case DriverMySQL:
		cfg, err := mysqldriver.ParseDSN(dsn)
		if err != nil {
			return "", false
		}
		return cfg.DBName, cfg.DBName != ""
	case DriverPostgres:
		cfg, err := pgconn.ParseConfig(dsn)
		if err != nil {
			return "", false
		}
		return cfg.Database, cfg.Database != ""
	default:
		return "", false
	}
}

// maintenanceDSN returns a connection string for the same server as dsn, connected to the server's
// maintenance database instead of the target one: MySQL gets a connection that names no database at
// all, PostgreSQL gets the postgres database. | maintenanceDSN 返回与 dsn 同一台服务器、但连到维护库
// （而不是目标库）的连接串：MySQL 用不点名任何库的连接，PostgreSQL 用 postgres 库。
func maintenanceDSN(driver, dsn string) (string, bool) {
	switch driverKind(driver) {
	case DriverMySQL:
		cfg, err := mysqldriver.ParseDSN(dsn)
		if err != nil {
			return "", false
		}
		// An empty DBName is the driver's way of saying "the handshake carries no default
		// database", which is exactly what a maintenance connection is for. | 空 DBName 是驱动「握手不携带
		// 默认库」的表达方式，正是维护连接需要的形态。
		cfg.DBName = ""
		return cfg.FormatDSN(), true
	case DriverPostgres:
		return postgresMaintenanceDSN(dsn)
	default:
		return "", false
	}
}

// postgresMaintenanceDSN returns a DSN for the same server as dsn, with the target database replaced
// by the maintenance database. | postgresMaintenanceDSN 返回与 dsn 同一台服务器、但把目标库换成维护库的连接串。
//
// Two forms, two edits. | 两种形态，两种改法。
//
// The URL form has its path replaced, and a database parameter in its query string removed, because
// pgconn lets a query parameter override the path. | URL 形态替换路径，并删掉查询串里的 database 参数：
// pgconn 允许查询参数覆盖路径。
//
// The keyword/value form only gets "dbname=postgres" appended instead of being rewritten: pgconn's
// parser keeps the last occurrence of a key, so appending wins without touching the caller's text —
// and therefore without any chance of mangling it (a quoted password, for one). | 关键字/值形态只追加
// "dbname=postgres"，不重写原文：pgconn 的解析器取同名键的最后一次出现，追加即可胜出，
// 既不必触碰调用方原文，也就不会把原文改坏（比如带引号的口令）。
func postgresMaintenanceDSN(dsn string) (string, bool) {
	if !isPostgresURL(dsn) {
		// The separator only appears when there is something to separate from: an empty DSN is a
		// legal input here, because the database name may have come from PGDATABASE. | 只有当确实有内容
		// 需要分隔时才补分隔符：空连接串在这里是合法输入，因为库名可能来自 PGDATABASE。
		sep := " "
		if strings.TrimSpace(dsn) == "" {
			sep = ""
		}
		return dsn + sep + postgresDBNameParam + "=" + postgresMaintenanceDatabase, true
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", false
	}
	parsed.Path = "/" + postgresMaintenanceDatabase
	query := parsed.Query()
	query.Del(postgresDBNameParam)
	query.Del("database")
	parsed.RawQuery = query.Encode()
	return parsed.String(), true
}

// createDatabaseStatement returns the DDL that creates name on driver's server. | createDatabaseStatement
// 返回在 driver 的服务器上创建 name 的 DDL。
//
// name is external input: it comes from a DSN. It is escaped as an identifier and never pasted in, so
// a name carrying a backtick, a double quote or a whole statement cannot break out of the identifier
// position. | name 是外部输入——它来自连接串。这里按标识符转义后拼入，绝不直接粘贴，
// 因此带反引号、双引号乃至一整条语句的库名都无法逃出标识符位置。
//
// The MySQL statement carries the character set and collation of the preset layer, read from the very
// table PresetDSN uses, so a database created here cannot end up with defaults that contradict the
// connection string (plan §16). | MySQL 语句带上预置层的字符集与排序规则，取值直接取自 PresetDSN
// 使用的那张表，因此在这里建出的库不会拿到与连接串相互矛盾的默认值（计划 §16）。
func createDatabaseStatement(driver, name string) (string, bool) {
	if strings.TrimSpace(name) == "" {
		// An empty identifier would produce syntactically invalid DDL; targetDatabase already
		// rejects it, and this keeps the function total on its own. | 空标识符会拼出语法非法的 DDL；
		// targetDatabase 已经挡掉了它，这里保证本函数自身也是完备的。
		return "", false
	}
	switch driverKind(driver) {
	case DriverMySQL:
		return "CREATE DATABASE IF NOT EXISTS " + quoteMySQLIdentifier(name) +
			" CHARACTER SET " + presetParamValue(mysqlPresetParams, mysqlPresetKeyCharset) +
			" COLLATE " + presetParamValue(mysqlPresetParams, mysqlPresetKeyCollation), true
	case DriverPostgres:
		// PostgreSQL has no CREATE DATABASE IF NOT EXISTS; a lost race answers 42P04 and the caller
		// reads that as success (see isDuplicateDatabaseError). | PostgreSQL 没有 CREATE DATABASE
		// IF NOT EXISTS；抢建失败会答复 42P04，调用方按成功处理（见 isDuplicateDatabaseError）。
		return "CREATE DATABASE " + quotePostgresIdentifier(name), true
	default:
		return "", false
	}
}

// quoteMySQLIdentifier wraps name in backticks and doubles any backtick inside it. | quoteMySQLIdentifier
// 用反引号包住 name，并把内部的每个反引号写成两个。
//
// Doubling is MySQL's own escape rule for a quoted identifier, so the result is always one
// identifier no matter what the DSN carried. | 成对写出是 MySQL 对带引号标识符自身的转义规则，
// 因此无论连接串里带了什么，结果都是一个完整的标识符。
func quoteMySQLIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// quotePostgresIdentifier wraps name in double quotes and doubles any double quote inside it. | quotePostgresIdentifier
// 用双引号包住 name，并把内部的每个双引号写成两个。
//
// Doubling is SQL's own rule for a delimited identifier, and PostgreSQL applies it verbatim. | 成对写出是
// SQL 对定界标识符自身的规则，PostgreSQL 原样适用。
func quotePostgresIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// presetParamValue returns the value the preset table carries for key, or the empty string when the
// table has no such key. | presetParamValue 返回预置表里 key 对应的取值；表中没有该键时返回空串。
func presetParamValue(params []dsnParam, key string) string {
	for _, p := range params {
		if p.key == key {
			return p.value
		}
	}
	return ""
}

// runMaintenanceDDL opens a throwaway connection to the server's maintenance database, runs one DDL
// statement on it and closes it again. | runMaintenanceDDL 连到服务器的维护库，在其上执行一条 DDL，然后关闭连接。
//
// The temporary connection never escapes this function: it is closed before returning, so no
// connection pool outlives the call. Its close error is dropped the same way Open drops the close
// error of a half-built pool — a cleanup failure must not turn a successful CREATE DATABASE into a
// reported failure. | 临时连接不会离开本函数：返回前即关闭，因此没有任何连接池比这次调用活得更久。
// 它的关闭错误与 Open 处理半成品连接池时同样丢弃——清理失败不得把一次成功的 CREATE DATABASE
// 变成上报失败。
//
// The statement itself is silent in GORM's log: the caller logs one Warn line about the database it
// created, and that line carries the whole statement. | 语句本身在 GORM 日志里静默：调用方会就它建出的
// 库打一行 Warn，那行日志带着完整语句。
func runMaintenanceDDL(driver, dsn, statement string) error {
	dialector, err := dialectorFor(driver, dsn)
	if err != nil {
		return err
	}
	db, err := openDatabaseFunc(dialector, &gorm.Config{Logger: NewLogger(nil, 0, logLevelSilent)})
	if err != nil {
		return err
	}
	execErr := db.Exec(statement).Error
	_ = Close(db)
	return execErr
}

// createAndReconnect creates the database the DSN names and then connects once more. | createAndReconnect
// 建出连接串点名的数据库，然后重连一次。
//
// It is called only when the first connect failed with a "the database does not exist" code and
// Options.CreateDatabase is on, which bounds the work: one create and one reconnect per Open call,
// never a retry loop. The reconnect reuses the same dialector and gorm.Config, so the second
// connection carries exactly the logger, prepared-statement and preset settings the first one had.
// | 只有在首次连接以「数据库不存在」的错误码失败、且 Options.CreateDatabase 开启时才会调用，
// 因此工作量是有界的：每次 Open 一次建库加一次重连，绝不进入重试循环。重连复用同一个方言与
// gorm.Config，因此第二条连接携带的日志器、预编译语句与预置设置与第一条完全一致。
//
// fail is the first connect's error. It is kept verbatim when creation is skipped, and joined onto the
// returned error when creation itself fails, so neither reason is ever swallowed — one of them alone
// would read as a different problem than the pair does. | fail 是首次连接的错误。跳过建库时原样保留，
// 建库失败时与建库错误一起挂在返回错误的同一条链上，两个原因一个都不吞——只报其中一个，
// 读起来都像是另一个问题。
func createAndReconnect(o Options, dialector gorm.Dialector, cfg *gorm.Config, fail error, l *logx.Logger) (*gorm.DB, error) {
	plan, ok := planCreation(o.Driver, o.DSN)
	if !ok {
		// The database name, the maintenance DSN or the statement could not be derived: report the
		// original failure rather than invent a database name. | 库名、维护连接串或建库语句推导不出来时，
		// 如实上报原始失败，而不是凭空编一个库名。
		return nil, fail
	}

	if err := maintenanceDDLFunc(o.Driver, plan.maintenanceDSN, plan.statement); err != nil && !isDuplicateDatabaseError(o.Driver, err) {
		driver := strings.TrimSpace(o.Driver)
		return nil, errors.Join(
			errx.WrapDetail(fail, ErrDatabaseMissing, "driver %q database %q | 驱动 %q 数据库 %q",
				driver, plan.database, driver, plan.database),
			errx.WrapDetail(err, ErrCreateDatabase, "driver %q database %q | 驱动 %q 数据库 %q",
				driver, plan.database, driver, plan.database),
		)
	}

	// Logged before the reconnect: whether the reconnect works or not, a database now exists, and
	// whoever reads this log has to know which one. | 在重连之前打这行日志：无论重连成败，
	// 库里已经多出一个数据库，读日志的人必须知道是哪一个。
	l.Warn("database created, reconnecting | 数据库已创建，正在重连",
		logx.Str("driver", strings.TrimSpace(o.Driver)),
		logx.Str("database", plan.database),
		logx.Str("statement", plan.statement))

	return openDatabaseFunc(dialector, cfg)
}
