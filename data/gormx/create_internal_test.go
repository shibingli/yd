package gormx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/shibingli/yd/core/logx"
)

// createDenied 是本文件注入建库注入口的失败原因。
//
// 它刻意不沿用任何驱动错误的类型：这样 errors.As 才能在同一条错误链上把「建库失败」与「库不存在」
// 分开断言，而不是双双命中遍历中先遇到的第一个 *mysqldriver.MySQLError。
type createDenied struct {
	number uint16
}

// Error renders the injected failure reason. | Error 渲染注入的失败原因。
func (e *createDenied) Error() string {
	return fmt.Sprintf("access denied while creating the database (error %d)", e.number)
}

// openResult is one preloaded return value of the connection injection point. | openResult 是连接注入口的一次预置返回值。
type openResult struct {
	db  *gorm.DB
	err error
}

// ddlCall records one call of the maintenance-DDL injection point. | ddlCall 记录维护 DDL 注入口的一次调用。
type ddlCall struct {
	driver    string
	dsn       string
	statement string
}

// stubOpen 把连接注入口换成一段预置序列，并返回调用计数指针。
//
// 替换它的用例必须串行执行：这个注入口是包级状态。
func stubOpen(t *testing.T, results ...openResult) *int {
	t.Helper()
	calls := new(int)
	previous := openDatabaseFunc
	openDatabaseFunc = func(gorm.Dialector, *gorm.Config) (*gorm.DB, error) {
		*calls++
		if *calls > len(results) {
			t.Errorf("连接注入口被调用了 %d 次，用例只准备了 %d 个返回值", *calls, len(results))
			return nil, errors.New("unexpected extra open call")
		}
		return results[*calls-1].db, results[*calls-1].err
	}
	t.Cleanup(func() { openDatabaseFunc = previous })
	return calls
}

// stubMaintenanceDDL swaps the maintenance-DDL injection point for a recording stub and returns the call log. | stubMaintenanceDDL 把维护 DDL 注入口换成记录调用的桩，并返回调用记录。
func stubMaintenanceDDL(t *testing.T, err error) *[]ddlCall {
	t.Helper()
	calls := new([]ddlCall)
	previous := maintenanceDDLFunc
	maintenanceDDLFunc = func(driver, dsn, statement string) error {
		*calls = append(*calls, ddlCall{driver: driver, dsn: dsn, statement: statement})
		return err
	}
	t.Cleanup(func() { maintenanceDDLFunc = previous })
	return calls
}

// newSQLiteProbe 打开一个真实的 SQLite 内存库，供注入口当作「重连成功」的返回值交回。
// 它直接调用 gorm.Open 而不是包级注入口，因此不受用例所装的桩影响。
func newSQLiteProbe(t *testing.T, suffix string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(memoryProbeDSN(t, suffix)), &gorm.Config{Logger: NewLogger(nil, 0, logLevelSilent)})
	if err != nil {
		t.Fatalf("打开 SQLite 探针库失败: %v", err)
	}
	t.Cleanup(func() { _ = Close(db) })
	return db
}

// memoryProbeDSN returns the DSN of an in-process in-memory database, keyed by the test name so two cases never share one cache. | memoryProbeDSN 返回进程内内存库的连接串，按用例名区分，避免两个用例共用一份缓存。
func memoryProbeDSN(t *testing.T, suffix string) string {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_", "#", "_").Replace(t.Name())
	return "file:" + name + "-" + suffix + "?mode=memory&cache=shared"
}

// TestMissingDatabaseErrorIsDecidedByCode 断言「库不存在」只按错误码判定：只有 MySQL 1049 与
// PostgreSQL 3D000 触发建库，其余错误码与普通错误一律不触发。
func TestMissingDatabaseErrorIsDecidedByCode(t *testing.T) {
	cases := []struct {
		name   string
		driver string
		err    error
		want   bool
	}{
		{
			name:   "mysql-1049-unknown-database",
			driver: DriverMySQL,
			err:    &mysqldriver.MySQLError{Number: 1049, Message: "Unknown database 'prod_users'"},
			want:   true,
		},
		{
			// Both database/sql and GORM may wrap a driver error several layers deep; errors.As must see through them. | database/sql 与 GORM 都可能把驱动错误包几层；errors.As 必须能穿透。
			name:   "mysql-1049-wrapped",
			driver: DriverMySQL,
			err:    fmt.Errorf("failed to initialize database, got error %w", &mysqldriver.MySQLError{Number: 1049}),
			want:   true,
		},
		{
			// Permission denied: the account reaches the server yet has no rights, and that must never be read as "the database does not exist". | 权限拒绝：账号连得上服务器但没有权限，绝不能被读成「库不存在」。
			name:   "mysql-1045-access-denied",
			driver: DriverMySQL,
			err:    &mysqldriver.MySQLError{Number: 1045, Message: "Access denied for user 'yd'@'%'"},
			want:   false,
		},
		{
			name:   "mysql-1044-access-denied-for-database",
			driver: DriverMySQL,
			err:    &mysqldriver.MySQLError{Number: 1044, Message: "Access denied for user 'yd'@'%' to database 'prod_users'"},
			want:   false,
		},
		{
			name:   "mysql-connection-refused",
			driver: DriverMySQL,
			err:    errors.New("dial tcp 127.0.0.1:3306: connectex: No connection could be made"),
			want:   false,
		},
		{
			// Whitespace and case in the driver name take no part in the decision, exactly as in dialectorFor. | 驱动名的空白与大小写不参与判定：与 dialectorFor 的口径一致。
			name:   "mysql-driver-name-spelling",
			driver: " MySQL ",
			err:    &mysqldriver.MySQLError{Number: 1049},
			want:   true,
		},
		{
			name:   "postgres-3D000-invalid-catalog-name",
			driver: DriverPostgres,
			err:    &pgconn.PgError{Code: "3D000", Message: `database "prod_users" does not exist`},
			want:   true,
		},
		{
			name:   "postgres-3D000-wrapped",
			driver: DriverPostgres,
			err:    fmt.Errorf("failed to connect to `host=127.0.0.1`: %w", &pgconn.PgError{Code: "3D000"}),
			want:   true,
		},
		{
			name:   "postgres-28P01-invalid-password",
			driver: DriverPostgres,
			err:    &pgconn.PgError{Code: "28P01", Message: "password authentication failed"},
			want:   false,
		},
		{
			name:   "postgres-42501-insufficient-privilege",
			driver: DriverPostgres,
			err:    &pgconn.PgError{Code: "42501", Message: "permission denied for database"},
			want:   false,
		},
		{
			// Already exists is not the same as not existing: 42P04 is handled separately by isDuplicateDatabaseError. | 已存在不是「不存在」：42P04 由 isDuplicateDatabaseError 单独处理。
			name:   "postgres-42P04-duplicate-database",
			driver: DriverPostgres,
			err:    &pgconn.PgError{Code: "42P04", Message: `database "prod_users" already exists`},
			want:   false,
		},
		{
			name:   "postgres-connection-refused",
			driver: DriverPostgres,
			err:    errors.New("failed to connect to `host=127.0.0.1`: dial error"),
			want:   false,
		},
		{
			// The error type must match the driver: asking MySQL's decision with a PostgreSQL error can only answer no. | 类型与驱动必须对上：拿着 PostgreSQL 的错误去问 MySQL 的判定，答案只能是否。
			name:   "mysql-driver-with-postgres-error",
			driver: DriverMySQL,
			err:    &pgconn.PgError{Code: "3D000"},
			want:   false,
		},
		{
			name:   "postgres-driver-with-mysql-error",
			driver: DriverPostgres,
			err:    &mysqldriver.MySQLError{Number: 1049},
			want:   false,
		},
		{
			// The SQLite driver creates the database file itself, so there is no "database does not exist" error code. | SQLite 的库文件由驱动自行创建，没有「库不存在」这一种错误码。
			name:   "sqlite-never",
			driver: DriverSQLite,
			err:    &mysqldriver.MySQLError{Number: 1049},
			want:   false,
		},
		{
			name:   "empty-driver-means-sqlite",
			driver: "",
			err:    &mysqldriver.MySQLError{Number: 1049},
			want:   false,
		},
		{
			name:   "unsupported-driver",
			driver: "oracle",
			err:    &mysqldriver.MySQLError{Number: 1049},
			want:   false,
		},
		{name: "nil-error", driver: DriverMySQL, err: nil, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMissingDatabaseError(tc.driver, tc.err); got != tc.want {
				t.Fatalf("isMissingDatabaseError(%q, %v) = %v，期望 %v", tc.driver, tc.err, got, tc.want)
			}
		})
	}
}

// TestDuplicateDatabaseErrorIsDecidedByCode 断言 PostgreSQL 的 42P04 被单独识别：
// CREATE DATABASE 没有 IF NOT EXISTS，抢建失败必须按成功读。
func TestDuplicateDatabaseErrorIsDecidedByCode(t *testing.T) {
	cases := []struct {
		name   string
		driver string
		err    error
		want   bool
	}{
		{name: "postgres-42P04", driver: DriverPostgres, err: &pgconn.PgError{Code: "42P04"}, want: true},
		{
			name:   "postgres-42P04-wrapped",
			driver: DriverPostgres,
			err:    fmt.Errorf("create database failed: %w", &pgconn.PgError{Code: "42P04"}),
			want:   true,
		},
		{name: "postgres-3D000", driver: DriverPostgres, err: &pgconn.PgError{Code: "3D000"}, want: false},
		{name: "postgres-42501", driver: DriverPostgres, err: &pgconn.PgError{Code: "42501"}, want: false},
		{name: "mysql-driver", driver: DriverMySQL, err: &pgconn.PgError{Code: "42P04"}, want: false},
		{name: "sqlite-driver", driver: DriverSQLite, err: &pgconn.PgError{Code: "42P04"}, want: false},
		{name: "plain-error", driver: DriverPostgres, err: errors.New("boom"), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDuplicateDatabaseError(tc.driver, tc.err); got != tc.want {
				t.Fatalf("isDuplicateDatabaseError(%q, %v) = %v，期望 %v", tc.driver, tc.err, got, tc.want)
			}
		})
	}
}

// TestCreateDatabaseStatementEscapesIdentifier 断言建库语句的原文：库名按标识符转义，
// 含反引号/双引号乃至整条语句的恶意库名都逃不出标识符位置。
func TestCreateDatabaseStatementEscapesIdentifier(t *testing.T) {
	const mysqlTail = " CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"

	cases := []struct {
		name   string
		driver string
		db     string
		want   string
		ok     bool
	}{
		{
			name:   "mysql",
			driver: DriverMySQL,
			db:     "prod_users",
			want:   "CREATE DATABASE IF NOT EXISTS `prod_users`" + mysqlTail,
			ok:     true,
		},
		{
			name:   "mysql-driver-name-spelling",
			driver: " MySQL ",
			db:     "prod_users",
			want:   "CREATE DATABASE IF NOT EXISTS `prod_users`" + mysqlTail,
			ok:     true,
		},
		{
			// A backtick inside the name must be written in pairs, otherwise the identifier closes early. | 库名里的反引号必须成对写出，否则标识符会提前闭合。
			name:   "mysql-backtick-in-name",
			driver: DriverMySQL,
			db:     "prod`users",
			want:   "CREATE DATABASE IF NOT EXISTS `prod``users`" + mysqlTail,
			ok:     true,
		},
		{
			// Malicious database name: the injected fragment stays wholly inside the identifier and the statement structure is not rewritten. | 恶意库名：注入片段整体落在标识符内部，语句结构不被改写。
			name:   "mysql-injection-attempt",
			driver: DriverMySQL,
			db:     "prod`; DROP DATABASE important; --",
			want:   "CREATE DATABASE IF NOT EXISTS `prod``; DROP DATABASE important; --`" + mysqlTail,
			ok:     true,
		},
		{
			name:   "postgres",
			driver: DriverPostgres,
			db:     "prod_users",
			want:   `CREATE DATABASE "prod_users"`,
			ok:     true,
		},
		{
			name:   "postgres-double-quote-in-name",
			driver: DriverPostgres,
			db:     `prod"users`,
			want:   `CREATE DATABASE "prod""users"`,
			ok:     true,
		},
		{
			// Malicious database name: the double quote is written in pairs, same as above. | 恶意库名：双引号成对写出后，同上。
			name:   "postgres-injection-attempt",
			driver: DriverPostgres,
			db:     `prod"; DROP DATABASE important; --`,
			want:   `CREATE DATABASE "prod""; DROP DATABASE important; --"`,
			ok:     true,
		},
		{name: "mysql-empty-name", driver: DriverMySQL, db: "", ok: false},
		{name: "mysql-blank-name", driver: DriverMySQL, db: "   ", ok: false},
		{name: "postgres-empty-name", driver: DriverPostgres, db: "", ok: false},
		{name: "sqlite-does-not-participate", driver: DriverSQLite, db: "yd", ok: false},
		{name: "unsupported-driver", driver: "oracle", db: "yd", ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := createDatabaseStatement(tc.driver, tc.db)
			if ok != tc.ok {
				t.Fatalf("createDatabaseStatement(%q, %q) 的 ok = %v，期望 %v（语句 %q）", tc.driver, tc.db, ok, tc.ok, got)
			}
			if got != tc.want {
				t.Fatalf("createDatabaseStatement(%q, %q)\n  得到 %q\n  期望 %q", tc.driver, tc.db, got, tc.want)
			}
		})
	}
}

// TestCreateDatabaseStatementFollowsPresetTable 断言建库语句的字符集与排序规则直接取自预置表：
// 两处各写一份字面量的话，改预置就架空建库，改建库又架空预置。
func TestCreateDatabaseStatementFollowsPresetTable(t *testing.T) {
	statement, ok := createDatabaseStatement(DriverMySQL, "prod_users")
	if !ok {
		t.Fatal("createDatabaseStatement(MySQL, prod_users) returned ok=false | createDatabaseStatement(MySQL, prod_users) 返回 ok=false")
	}
	for _, key := range []string{mysqlPresetKeyCharset, mysqlPresetKeyCollation} {
		value := presetParamValue(mysqlPresetParams, key)
		if value == "" {
			t.Fatalf("预置表里没有 %q，本用例失去意义", key)
		}
		if !strings.Contains(statement, value) {
			t.Fatalf("建库语句 %q 未包含预置表的 %s=%q", statement, key, value)
		}
	}
}

// TestPlanCreationReadsDSNLikeTheDriver 断言建库计划完全按驱动自己的解析结果推导：
// 库名、维护连接串与语句三者都来自驱动对连接串的读法，而不是字符串手术。
func TestPlanCreationReadsDSNLikeTheDriver(t *testing.T) {
	// The database name may come from PGDATABASE: clearing it keeps the case independent of the host machine. | 库名可能来自 PGDATABASE：清空它，用例才与所在机器的环境无关。
	t.Setenv("PGDATABASE", "")

	const mysqlTail = " CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"

	cases := []struct {
		name     string
		driver   string
		dsn      string
		wantDB   string
		wantDSN  string
		wantStmt string
		wantOK   bool
	}{
		{
			name:     "mysql",
			driver:   DriverMySQL,
			dsn:      "root:root@tcp(127.0.0.1:3306)/prod_users",
			wantDB:   "prod_users",
			wantDSN:  "root:root@tcp(127.0.0.1:3306)/",
			wantStmt: "CREATE DATABASE IF NOT EXISTS `prod_users`" + mysqlTail,
			wantOK:   true,
		},
		{
			// The maintenance DSN keeps the caller's parameters and drops only the default database (loc=UTC matches the driver default, so it is never formatted back). | 维护连接串保留调用方的参数，只把默认库去掉（loc=UTC 与驱动默认一致，本身不会被格式化回来）。
			name:     "mysql-keeps-params",
			driver:   DriverMySQL,
			dsn:      "root:root@tcp(127.0.0.1:3306)/prod_users?charset=utf8mb4&parseTime=true",
			wantDB:   "prod_users",
			wantDSN:  "root:root@tcp(127.0.0.1:3306)/?parseTime=true&charset=utf8mb4",
			wantStmt: "CREATE DATABASE IF NOT EXISTS `prod_users`" + mysqlTail,
			wantOK:   true,
		},
		{
			// The password carries @ and /: the database name is still the segment after the last '/', matching the driver. | 口令里带 @ 与 /：库名仍取最后一个 '/' 之后的那一段，与驱动一致。
			name:     "mysql-password-with-at-and-slash",
			driver:   DriverMySQL,
			dsn:      "root:p@ss/word@tcp(127.0.0.1:3306)/prod_users",
			wantDB:   "prod_users",
			wantDSN:  "root:p@ss/word@tcp(127.0.0.1:3306)/",
			wantStmt: "CREATE DATABASE IF NOT EXISTS `prod_users`" + mysqlTail,
			wantOK:   true,
		},
		{
			// A percent-escaped database name is decoded the way the driver does, then escaped as an identifier. | 百分号转义的库名按驱动的方式解码，再按标识符转义。
			name:     "mysql-percent-escaped-name",
			driver:   DriverMySQL,
			dsn:      "root:root@tcp(127.0.0.1:3306)/prod%60users",
			wantDB:   "prod`users",
			wantDSN:  "root:root@tcp(127.0.0.1:3306)/",
			wantStmt: "CREATE DATABASE IF NOT EXISTS `prod``users`" + mysqlTail,
			wantOK:   true,
		},
		{
			name:     "postgres-url",
			driver:   DriverPostgres,
			dsn:      "postgres://user:pw@127.0.0.1:5432/prod_users?sslmode=disable",
			wantDB:   "prod_users",
			wantDSN:  "postgres://user:pw@127.0.0.1:5432/postgres?sslmode=disable",
			wantStmt: `CREATE DATABASE "prod_users"`,
			wantOK:   true,
		},
		{
			// In the URL form a query parameter overrides the path, so the database parameter both decides the target database and has to be dropped from the maintenance DSN. | URL 形态里查询参数会覆盖路径，因此 database 参数既决定目标库、也必须从维护连接串里删掉。
			name:     "postgres-url-query-database-overrides-path",
			driver:   DriverPostgres,
			dsn:      "postgres://user:pw@127.0.0.1:5432/prod_users?sslmode=disable&database=other",
			wantDB:   "other",
			wantDSN:  "postgres://user:pw@127.0.0.1:5432/postgres?sslmode=disable",
			wantStmt: `CREATE DATABASE "other"`,
			wantOK:   true,
		},
		{
			name:     "postgres-keyword",
			driver:   DriverPostgres,
			dsn:      "host=127.0.0.1 user=yd password=pw dbname=prod_users",
			wantDB:   "prod_users",
			wantDSN:  "host=127.0.0.1 user=yd password=pw dbname=prod_users dbname=postgres",
			wantStmt: `CREATE DATABASE "prod_users"`,
			wantOK:   true,
		},
		{
			// database= and dbname= are two names of one parameter, and the appended dbname wins as the last occurrence. | database= 与 dbname= 是同一个参数的两个名字，追加的 dbname 取最后一次出现而胜出。
			name:     "postgres-keyword-database-key",
			driver:   DriverPostgres,
			dsn:      "host=127.0.0.1 user=yd database=prod_users",
			wantDB:   "prod_users",
			wantDSN:  "host=127.0.0.1 user=yd database=prod_users dbname=postgres",
			wantStmt: `CREATE DATABASE "prod_users"`,
			wantOK:   true,
		},
		{
			// The DSN names no database and the environment has none either: no name is guessed and nothing is created. | 连接串没点名库、环境里也没有：不猜名字，直接不建库。
			name:   "postgres-keyword-without-database",
			driver: DriverPostgres,
			dsn:    "host=127.0.0.1 user=yd",
			wantOK: false,
		},
		{
			name:   "postgres-url-without-database",
			driver: DriverPostgres,
			dsn:    "postgres://user:pw@127.0.0.1:5432",
			wantOK: false,
		},
		{
			name:   "mysql-without-database",
			driver: DriverMySQL,
			dsn:    "root:root@tcp(127.0.0.1:3306)/",
			wantOK: false,
		},
		{
			name:   "mysql-unparsable",
			driver: DriverMySQL,
			dsn:    "this is not a mysql dsn",
			wantOK: false,
		},
		{
			name:   "sqlite-does-not-participate",
			driver: DriverSQLite,
			dsn:    "data/yd.db",
			wantOK: false,
		},
		{
			name:   "unsupported-driver",
			driver: "oracle",
			dsn:    "user:pw@tcp(127.0.0.1:3306)/prod_users",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, ok := planCreation(tc.driver, tc.dsn)
			if ok != tc.wantOK {
				t.Fatalf("planCreation(%q, %q) 的 ok = %v，期望 %v（计划 %+v）", tc.driver, tc.dsn, ok, tc.wantOK, plan)
			}
			if !ok {
				return
			}
			if plan.database != tc.wantDB {
				t.Errorf("库名 = %q，期望 %q", plan.database, tc.wantDB)
			}
			if plan.maintenanceDSN != tc.wantDSN {
				t.Errorf("维护连接串\n  得到 %q\n  期望 %q", plan.maintenanceDSN, tc.wantDSN)
			}
			if plan.statement != tc.wantStmt {
				t.Errorf("建库语句\n  得到 %q\n  期望 %q", plan.statement, tc.wantStmt)
			}

			// The maintenance DSN must really connect to the maintenance database: the driver's own parser re-checks it rather than a mere string comparison. | 维护连接串必须真的连向维护库：交给驱动自己的解析器复核，而不是只看字符串。
			switch driverKind(tc.driver) {
			case DriverMySQL:
				cfg, err := mysqldriver.ParseDSN(plan.maintenanceDSN)
				if err != nil {
					t.Fatalf("驱动无法解析维护连接串 %q: %v", plan.maintenanceDSN, err)
				}
				if cfg.DBName != "" {
					t.Fatalf("维护连接串的默认库 = %q，期望不点名任何库", cfg.DBName)
				}
				if cfg.Net != "tcp" || cfg.Addr != "127.0.0.1:3306" {
					t.Fatalf("维护连接串指向 %s(%s)，期望仍是原服务器的 tcp(127.0.0.1:3306)", cfg.Net, cfg.Addr)
				}
			case DriverPostgres:
				cfg, err := pgconn.ParseConfig(plan.maintenanceDSN)
				if err != nil {
					t.Fatalf("驱动无法解析维护连接串 %q: %v", plan.maintenanceDSN, err)
				}
				if cfg.Database != postgresMaintenanceDatabase {
					t.Fatalf("维护连接串的库 = %q，期望 %q", cfg.Database, postgresMaintenanceDatabase)
				}
			}
		})
	}
}

// TestOpenCreatesMissingDatabaseAndRetries 断言完整的一次成功路径：
// 首次连接因「库不存在」失败 → 建一次库（语句与维护连接串正确）→ 重连一次成功 → 打一条含库名的 Warn。
func TestOpenCreatesMissingDatabaseAndRetries(t *testing.T) {
	probe := newSQLiteProbe(t, "retry")
	var buf bytes.Buffer
	l := logx.New(logx.Options{Writer: &buf})

	calls := stubOpen(t,
		openResult{err: &mysqldriver.MySQLError{Number: 1049, Message: "Unknown database 'prod_users'"}},
		openResult{db: probe},
	)
	created := stubMaintenanceDDL(t, nil)

	db, err := Open(Options{
		Driver:         DriverMySQL,
		DSN:            "root:root@tcp(127.0.0.1:3306)/prod_users",
		MaxOpenConns:   5,
		CreateDatabase: true,
	}, l)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	t.Cleanup(func() { _ = Close(db) })

	if *calls != 2 {
		t.Fatalf("连接注入口被调用 %d 次，期望 2 次（首次 + 重连一次）", *calls)
	}
	if len(*created) != 1 {
		t.Fatalf("建库语句执行了 %d 次，期望 1 次：%+v", len(*created), *created)
	}
	got := (*created)[0]
	if got.driver != DriverMySQL {
		t.Errorf("建库用的驱动 = %q，期望 %q", got.driver, DriverMySQL)
	}
	if got.dsn != "root:root@tcp(127.0.0.1:3306)/" {
		t.Errorf("维护连接串 = %q，期望不点名目标库的连接", got.dsn)
	}
	if want := "CREATE DATABASE IF NOT EXISTS `prod_users` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"; got.statement != want {
		t.Errorf("建库语句\n  得到 %q\n  期望 %q", got.statement, want)
	}

	// The handle returned must be the one obtained on the retry, and it still went through the connection-pool assembly. | 返回的必须是重连那一次拿到的句柄，而且它照样走完了连接池装配。
	if db != probe {
		t.Fatalf("Open 返回的 *gorm.DB 不是重连拿到的句柄")
	}
	if err := Health(context.Background(), db); err != nil {
		t.Fatalf("重连后的句柄探活失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取回底层 *sql.DB 失败: %v", err)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != 5 {
		t.Fatalf("重连句柄的 MaxOpenConnections = %d，期望 5（连接池装配必须照样生效）", got)
	}

	out := buf.String()
	for _, want := range []string{
		`"level":"warn"`,
		"database created, reconnecting | 数据库已创建，正在重连",
		`"database":"prod_users"`,
		`"driver":"mysql"`,
		"CREATE DATABASE IF NOT EXISTS `prod_users`",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Warn 日志缺少 %s: %s", want, out)
		}
	}
	t.Logf("建库时打出的 Warn 日志原文: %s", strings.TrimRight(out, "\n"))
}

// TestOpenCreateDatabaseDisabledCreatesNothing 断言开关关闭时完全不尝试建库：
// 没有建库语句、没有重连，错误与关闭前逐字一致。
func TestOpenCreateDatabaseDisabledCreatesNothing(t *testing.T) {
	var buf bytes.Buffer
	newFailingOpen := func() *int {
		return stubOpen(t, openResult{err: &mysqldriver.MySQLError{Number: 1049, Message: "Unknown database 'prod_users'"}})
	}

	for _, tc := range []struct {
		name   string
		opts   Options
		driver string
	}{
		{
			name:   "explicit-false",
			opts:   Options{Driver: DriverMySQL, DSN: "root:root@tcp(127.0.0.1:3306)/prod_users", CreateDatabase: false},
			driver: DriverMySQL,
		},
		{
			name:   "zero-value",
			opts:   Options{Driver: DriverMySQL, DSN: "root:root@tcp(127.0.0.1:3306)/prod_users"},
			driver: DriverMySQL,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := newFailingOpen()
			created := stubMaintenanceDDL(t, nil)
			buf.Reset()

			db, err := Open(tc.opts, logx.New(logx.Options{Writer: &buf}))
			if db != nil {
				_ = Close(db)
				t.Fatalf("Open 返回了非 nil 的 *gorm.DB，期望 nil")
			}
			if !errors.Is(err, ErrOpenFailed) {
				t.Fatalf("Open 的错误 = %v，期望包装 ErrOpenFailed", err)
			}
			if errors.Is(err, ErrDatabaseMissing) || errors.Is(err, ErrCreateDatabase) {
				t.Fatalf("关闭建库后仍出现建库相关哨兵: %v", err)
			}
			var me *mysqldriver.MySQLError
			if !errors.As(err, &me) || me.Number != mysqlErrBadDB {
				t.Fatalf("关闭建库后原始驱动错误未保留: %v", err)
			}
			if len(*created) != 0 {
				t.Fatalf("开关关闭时发出了 %d 条建库语句: %+v", len(*created), *created)
			}
			if *calls != 1 {
				t.Fatalf("开关关闭时连接注入口被调用 %d 次，期望 1 次（不重连）", *calls)
			}
			if out := buf.String(); strings.Contains(out, "database created") {
				t.Fatalf("开关关闭时仍打出建库日志: %s", out)
			}
		})
	}
}

// TestOpenAggregatesMissingDatabaseAndCreateFailure 断言建库失败时两个原因都在错误链上：
// 「库不存在」与「建库失败」缺一不可，且旧调用方判定的 ErrOpenFailed 依然成立。
func TestOpenAggregatesMissingDatabaseAndCreateFailure(t *testing.T) {
	var buf bytes.Buffer
	calls := stubOpen(t, openResult{err: &mysqldriver.MySQLError{Number: 1049, Message: "Unknown database 'prod_users'"}})
	created := stubMaintenanceDDL(t, &createDenied{number: 1045})

	db, err := Open(Options{
		Driver:         DriverMySQL,
		DSN:            "root:root@tcp(127.0.0.1:3306)/prod_users",
		CreateDatabase: true,
	}, logx.New(logx.Options{Writer: &buf}))
	if db != nil {
		_ = Close(db)
		t.Fatalf("Open 返回了非 nil 的 *gorm.DB，期望 nil")
	}
	if err == nil {
		t.Fatal("Open returned no error | Open 未返回错误")
	}

	for name, sentinel := range map[string]error{
		"ErrOpenFailed":      ErrOpenFailed,
		"ErrDatabaseMissing": ErrDatabaseMissing,
		"ErrCreateDatabase":  ErrCreateDatabase,
	} {
		if !errors.Is(err, sentinel) {
			t.Errorf("错误链上缺少 %s: %v", name, err)
		}
	}

	// Either cause is recoverable with errors.As: the original 1049 and the injected create-database failure. | 两个原因各自可被 errors.As 取回：原始的 1049 与注入的建库失败。
	var me *mysqldriver.MySQLError
	if !errors.As(err, &me) || me.Number != mysqlErrBadDB {
		t.Errorf("错误链上没有原始的「库不存在」原因: %v", err)
	}
	var denied *createDenied
	if !errors.As(err, &denied) || denied.number != 1045 {
		t.Errorf("错误链上没有建库失败原因: %v", err)
	}

	// The error text must state both facts at once: the target database is missing, and which driver and database the failed creation was about. | 错误文案必须同时说出两件事：目标库不存在、以及建库失败时驱动与库名是谁。
	text := err.Error()
	t.Logf("聚合错误原文: %s", text)
	for _, want := range []string{
		"gormx: target database does not exist | gormx：目标数据库不存在",
		"gormx: failed to create database | gormx：创建数据库失败",
		`driver "mysql" database "prod_users"`,
		"Unknown database 'prod_users'",
		"access denied while creating the database",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("聚合错误缺少 %q: %s", want, text)
		}
	}

	if len(*created) != 1 {
		t.Fatalf("建库语句执行了 %d 次，期望 1 次", len(*created))
	}
	if *calls != 1 {
		t.Fatalf("建库失败后连接注入口被调用 %d 次，期望 1 次（不得重连）", *calls)
	}
	if out := buf.String(); strings.Contains(out, "database created") {
		t.Fatalf("建库失败时不得打出成功日志: %s", out)
	}
}

// TestOpenTreatsDuplicateDatabaseAsSuccess 断言 PostgreSQL 的 42P04 按成功读：
// 另一副本抢先建出了同一个库，重连必须照常发生。
func TestOpenTreatsDuplicateDatabaseAsSuccess(t *testing.T) {
	probe := newSQLiteProbe(t, "duplicate")
	var buf bytes.Buffer
	calls := stubOpen(t,
		openResult{err: &pgconn.PgError{Code: pgErrInvalidCatalogName, Message: `database "prod_users" does not exist`}},
		openResult{db: probe},
	)
	created := stubMaintenanceDDL(t, &pgconn.PgError{Code: pgErrDuplicateDatabase, Message: `database "prod_users" already exists`})

	db, err := Open(Options{
		Driver:         DriverPostgres,
		DSN:            "postgres://user:pw@127.0.0.1:5432/prod_users?sslmode=disable",
		CreateDatabase: true,
	}, logx.New(logx.Options{Writer: &buf}))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	t.Cleanup(func() { _ = Close(db) })

	if db != probe {
		t.Fatal("Open did not return the handle obtained on the retry | Open 返回的不是重连拿到的句柄")
	}
	if *calls != 2 {
		t.Fatalf("连接注入口被调用 %d 次，期望 2 次", *calls)
	}
	if len(*created) != 1 {
		t.Fatalf("建库语句执行了 %d 次，期望 1 次", len(*created))
	}
	if got := (*created)[0].statement; got != `CREATE DATABASE "prod_users"` {
		t.Fatalf("建库语句 = %q，期望不带 IF NOT EXISTS（PostgreSQL 不支持）", got)
	}
	if out := buf.String(); !strings.Contains(out, "database created") {
		t.Fatalf("抢建成功也应打出建库日志: %s", out)
	}
}

// TestOpenRetryFailureIsReportedAsOpenFailure 断言「建库成功但重连失败」的语义：
// 库已经建出来了（有 Warn），错误如实来自重连那一次，不再挂建库相关哨兵。
func TestOpenRetryFailureIsReportedAsOpenFailure(t *testing.T) {
	var buf bytes.Buffer
	refused := errors.New("dial tcp 127.0.0.1:3306: connectex: No connection could be made")
	calls := stubOpen(t,
		openResult{err: &mysqldriver.MySQLError{Number: 1049, Message: "Unknown database 'prod_users'"}},
		openResult{err: refused},
	)
	created := stubMaintenanceDDL(t, nil)

	db, err := Open(Options{
		Driver:         DriverMySQL,
		DSN:            "root:root@tcp(127.0.0.1:3306)/prod_users",
		CreateDatabase: true,
	}, logx.New(logx.Options{Writer: &buf}))
	if db != nil {
		_ = Close(db)
		t.Fatalf("Open 返回了非 nil 的 *gorm.DB，期望 nil")
	}
	if !errors.Is(err, ErrOpenFailed) {
		t.Fatalf("Open 的错误 = %v，期望包装 ErrOpenFailed", err)
	}
	if !errors.Is(err, refused) {
		t.Fatalf("Open 的错误未保留重连失败的原因: %v", err)
	}
	if errors.Is(err, ErrDatabaseMissing) || errors.Is(err, ErrCreateDatabase) {
		t.Fatalf("库已建出后不应再挂建库相关哨兵: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("连接注入口被调用 %d 次，期望 2 次", *calls)
	}
	if len(*created) != 1 {
		t.Fatalf("建库语句执行了 %d 次，期望 1 次", len(*created))
	}
	if out := buf.String(); !strings.Contains(out, `"database":"prod_users"`) {
		t.Fatalf("库确实建出来了，Warn 必须写明库名: %s", out)
	}
}

// TestOpenDoesNotGuessDatabaseName 断言连接串没有点名库时不建库：
// 猜一个库名只会建出错误的库，不如如实上报原始失败。
func TestOpenDoesNotGuessDatabaseName(t *testing.T) {
	var buf bytes.Buffer
	calls := stubOpen(t, openResult{err: &mysqldriver.MySQLError{Number: 1049, Message: "Unknown database ''"}})
	created := stubMaintenanceDDL(t, nil)

	db, err := Open(Options{
		Driver:         DriverMySQL,
		DSN:            "root:root@tcp(127.0.0.1:3306)/",
		CreateDatabase: true,
	}, logx.New(logx.Options{Writer: &buf}))
	if db != nil {
		_ = Close(db)
		t.Fatalf("Open 返回了非 nil 的 *gorm.DB，期望 nil")
	}
	if !errors.Is(err, ErrOpenFailed) {
		t.Fatalf("Open 的错误 = %v，期望包装 ErrOpenFailed", err)
	}
	if errors.Is(err, ErrDatabaseMissing) || errors.Is(err, ErrCreateDatabase) {
		t.Fatalf("无法推导库名时不应出现建库相关哨兵: %v", err)
	}
	if len(*created) != 0 {
		t.Fatalf("无法推导库名时仍发出了建库语句: %+v", *created)
	}
	if *calls != 1 {
		t.Fatalf("连接注入口被调用 %d 次，期望 1 次", *calls)
	}
	if out := buf.String(); strings.Contains(out, "database created") {
		t.Fatalf("未建库却打出了建库日志: %s", out)
	}
}

// TestOpenLeavesSQLiteAndUnknownDriversAlone 断言真实 SQLite 连接一字未变，
// 且未知驱动仍然在建库判定之前就被拒绝。
func TestOpenLeavesSQLiteAndUnknownDriversAlone(t *testing.T) {
	created := stubMaintenanceDDL(t, nil)

	db, err := Open(Options{
		Driver:         DriverSQLite,
		DSN:            memoryProbeDSN(t, "untouched"),
		CreateDatabase: true,
	}, logx.New(logx.Options{Writer: &bytes.Buffer{}}))
	if err != nil {
		t.Fatalf("SQLite 打开失败: %v", err)
	}
	t.Cleanup(func() { _ = Close(db) })

	// One real round trip proves this feature never touched SQLite's existing path. | 真实往返一次，证明本特性没有碰过 SQLite 的既有路径。
	if err := db.Exec("CREATE TABLE create_probe (id INTEGER PRIMARY KEY, name TEXT)").Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := db.Exec("INSERT INTO create_probe (name) VALUES (?)", "alice").Error; err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	var count int64
	if err := db.Raw("SELECT count(*) FROM create_probe").Scan(&count).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("写入后行数 = %d，期望 1", count)
	}

	if _, err := Open(Options{Driver: "oracle", DSN: "dsn", CreateDatabase: true}, nil); !errors.Is(err, ErrUnsupportedDriver) {
		t.Fatalf("未知驱动的错误 = %v，期望 ErrUnsupportedDriver", err)
	}
	if len(*created) != 0 {
		t.Fatalf("SQLite 与未知驱动都不得发出建库语句: %+v", *created)
	}
}

// TestRunMaintenanceDDLReportsUnreachableServer 走一遍真实的维护连接路径：
// 本机 127.0.0.1:1 必然无人监听，因此这里验证的是「能建立维护连接串、不 panic、连不上时如实报错」，
// 而不是真的连上了 MySQL —— 本机没有 MySQL 服务端。
func TestRunMaintenanceDDLReportsUnreachableServer(t *testing.T) {
	err := runMaintenanceDDL(DriverMySQL,
		"root:root@tcp(127.0.0.1:1)/?timeout=1s",
		"CREATE DATABASE IF NOT EXISTS `prod_users` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	if err == nil {
		t.Fatal("runMaintenanceDDL must return an error while the server is unreachable | 连不上服务器时 runMaintenanceDDL 应返回错误")
	}
	if strings.TrimSpace(err.Error()) == "" {
		t.Fatal("runMaintenanceDDL returned an empty error text | runMaintenanceDDL 返回了空错误文案")
	}
	t.Logf("真实尝试的失败原因（未连上，如实上报）: %v", err)
}

// TestRunMaintenanceDDLClosesItsConnection 断言维护连接用完即关：
// 语句在真实驱动上真的执行了（SQL 合法、驱动认它），且返回之后该连接池已不可再用。
func TestRunMaintenanceDDLClosesItsConnection(t *testing.T) {
	probe := newSQLiteProbe(t, "maintenance-close")
	calls := stubOpen(t, openResult{db: probe})

	err := runMaintenanceDDL(DriverSQLite, memoryProbeDSN(t, "maintenance-dsn"),
		"CREATE TABLE maintenance_probe (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatalf("维护连接上的 DDL 执行失败: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("维护连接只应打开一次，实际 %d 次", *calls)
	}
	// The probe database is the maintenance connection's underlying pool: it must already be closed, otherwise the one-shot connection stays alive inside the pool. | 探针库就是维护连接的底层连接池：它必须已经被关闭，否则一次性连接会留在池里活着。
	if execErr := probe.Exec("SELECT 1").Error; execErr == nil {
		t.Fatal("the maintenance connection must have closed its pool before returning | 维护连接在返回前必须已关闭连接池")
	} else {
		t.Logf("维护连接关闭后的再用错误: %v", execErr)
	}
}
