package gormstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/shibingli/yd/auth"
	"github.com/shibingli/yd/data/gormx"
)

const (
	// testMasterKey is the fixed ASCII master key of every fixture. | testMasterKey 是全部夹具使用的固定 ASCII 主密钥。
	testMasterKey = "yd-auth-gormstore-master-key-0123456789"
	// testIterations keeps PBKDF2 cheap, so the suite stays fast under -race. | testIterations 让 PBKDF2 保持廉价，使整套测试在 -race 下依然快速。
	testIterations = 1000
)

// openStore opens a file-backed SQLite database through the framework assembly,
// migrates it and returns the store plus the open *gorm.DB. | openStore 通过框架装配层打开一个
// 落盘 SQLite 数据库、完成迁移，并返回 store 与已打开的 *gorm.DB。
//
// A real file is used rather than an in-memory database on purpose: the whole point
// of this package is that data outlives the connection, which an in-memory database
// cannot demonstrate. | 这里刻意使用真实文件而不是内存库：本包存在的意义正是数据要活过连接，
// 而内存库无法证明这一点。
func openStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	db := openDB(t, filepath.Join(t.TempDir(), "auth.db"))
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() = %v, want nil | Migrate() = %v，期望 nil", err, err)
	}
	store, err := New(db, Options{QueryTimeout: time.Minute})
	if err != nil {
		t.Fatalf("New() = %v, want nil | New() = %v，期望 nil", err, err)
	}
	return store, db
}

// sqliteDSN builds the data source name every fixture uses. | sqliteDSN 构造全部夹具使用的数据源串。
//
// Three pragmas, each answering a real failure of the naive DSN:
//
//	_pragma=busy_timeout(10000)  a concurrent writer waits for the lock instead of failing with SQLITE_BUSY
//	_pragma=journal_mode(WAL)    a reader never queues behind a writer
//	_txlock=immediate            a write transaction takes the write lock when it begins
//
// The last one is the subtle one: a deferred transaction that reads first and
// writes later (which is exactly what the pre-checks in this package do) can lose
// its snapshot to a concurrent commit and then fail with SQLITE_BUSY_SNAPSHOT,
// which no busy timeout can resolve. A generated project receives these very
// pragmas in its default DSN. | 三个 pragma，各自解决朴素 DSN 的一个真实故障：
// busy_timeout 让并发写入者等待锁而不是以 SQLITE_BUSY 失败；WAL 让读不会排在写后面；
// _txlock=immediate 让写事务在开始时就取得写锁。最后一条最隐蔽：先读后写的延迟事务
// （本包的预检正是如此）可能被并发提交顶掉快照，随后以 SQLITE_BUSY_SNAPSHOT 失败，
// 而这是任何 busy timeout 都无法化解的。生成项目的默认 DSN 中带有同样这三个 pragma。
func sqliteDSN(path string) string {
	return "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate"
}

// openDB opens a SQLite database at path without migrating it. | openDB 打开 path 处的 SQLite 数据库，不做迁移。
func openDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gormx.Open(gormx.Options{
		Driver:   gormx.DriverSQLite,
		DSN:      sqliteDSN(path),
		LogLevel: "silent",
	}, nil)
	if err != nil {
		t.Fatalf("gormx.Open() = %v, want nil | gormx.Open() = %v，期望 nil", err, err)
	}
	t.Cleanup(func() { _ = gormx.Close(db) })
	return db
}

// newService builds a Service over store with a frozen clock. | newService 基于 store 构建一个时钟冻结的 Service。
func newService(t *testing.T, store auth.Store) *auth.Service {
	t.Helper()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	svc, err := auth.NewService(store, auth.Options{
		Now:        func() time.Time { return now },
		MasterKey:  []byte(testMasterKey),
		Iterations: testIterations,
	})
	if err != nil {
		t.Fatalf("auth.NewService() = %v, want nil | auth.NewService() = %v，期望 nil", err, err)
	}
	return svc
}

// captureLogger records every statement GORM executes. | captureLogger 记录 GORM 执行的每一条语句。
//
// It is the only honest way to prove "tenant_id is in the WHERE clause": reading
// the code shows the intent, the captured statement shows what the database was
// actually asked. | 这是证明「tenant_id 确实在 WHERE 里」的唯一诚实方式：读代码只能看到意图，
// 抓到的语句才能显示数据库究竟被要求做什么。
type captureLogger struct {
	// mu guards sqls, because a racing test writes from several goroutines. | mu 保护 sqls，因为并发测试会从多个 goroutine 写入。
	mu sync.Mutex
	// sqls holds the statements in execution order. | sqls 按执行顺序保存语句。
	sqls []string
}

// LogMode keeps the level as it is; capture happens at every level. | LogMode 保持级别不变；任何级别都会抓取。
func (c *captureLogger) LogMode(gormlogger.LogLevel) gormlogger.Interface { return c }

// Info discards informational messages. | Info 丢弃提示信息。
func (c *captureLogger) Info(context.Context, string, ...any) {}

// Warn discards warnings. | Warn 丢弃告警。
func (c *captureLogger) Warn(context.Context, string, ...any) {}

// Error discards errors. | Error 丢弃错误。
func (c *captureLogger) Error(context.Context, string, ...any) {}

// Trace records the statement the callback can render. | Trace 记录回调可以渲染出的语句。
func (c *captureLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	c.mu.Lock()
	c.sqls = append(c.sqls, sql)
	c.mu.Unlock()
}

// reset drops everything captured so far. | reset 丢弃此前抓取的全部语句。
func (c *captureLogger) reset() {
	c.mu.Lock()
	c.sqls = nil
	c.mu.Unlock()
}

// snapshot returns a copy of the captured statements. | snapshot 返回已抓取语句的副本。
func (c *captureLogger) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sqls...)
}

// joined returns every captured statement as one lowercase string. | joined 把已抓取的语句拼成一个全小写字符串。
func (c *captureLogger) joined() string {
	return strings.ToLower(strings.Join(c.snapshot(), "\n"))
}

// capturingDB opens a SQLite database whose logger records every statement. | capturingDB 打开一个日志器会记录每条语句的 SQLite 数据库。
func capturingDB(t *testing.T) (*gorm.DB, *captureLogger) {
	t.Helper()
	cap := &captureLogger{}
	db, err := gorm.Open(sqlite.Open(sqliteDSN(filepath.Join(t.TempDir(), "auth.db"))), &gorm.Config{
		Logger: cap,
	})
	if err != nil {
		t.Fatalf("gorm.Open() = %v, want nil | gorm.Open() = %v，期望 nil", err, err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db, cap
}

// mentionsValue reports whether captured SQL carries value as a literal. | mentionsValue 报告抓取的 SQL 是否以字面量携带 value。
//
// The dialect renders a string literal with double quotes on SQLite and with single
// quotes elsewhere, so both spellings are accepted. | 该方言在 SQLite 上把字符串字面量渲染为双引号，
// 在其它数据库上渲染为单引号，因此两种写法都接受。
func mentionsValue(sql, value string) bool {
	return strings.Contains(sql, `"`+value+`"`) || strings.Contains(sql, `'`+value+`'`)
}

// TestMigrateCreatesEveryTableAndIndex is the index contract of the plan: every
// index exists after Migrate, and every composite index has the designed column
// order. | TestMigrateCreatesEveryTableAndIndex 是计划的索引契约：迁移之后每个索引都存在，
// 且每个联合索引的列顺序与设计一致。
func TestMigrateCreatesEveryTableAndIndex(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "auth.db"))
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() = %v, want nil | Migrate() = %v，期望 nil", err, err)
	}
	// Migrate runs on every start, so it must be idempotent. | Migrate 每次启动都会执行，因此必须幂等。
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate() = %v, want nil | 第二次 Migrate() = %v，期望 nil", err, err)
	}

	for _, table := range []string{tableTenants, tableUsers, tableRoles, tableRolePermissions} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table %q is missing after Migrate | 迁移后缺少表 %q", table, table)
		}
	}

	want := []struct {
		index  string
		table  string
		unique bool
		cols   []string
	}{
		{indexTenantsSlug, tableTenants, true, []string{"slug_key"}},
		{indexTenantsCreatedAt, tableTenants, false, []string{"created_at"}},
		{indexUsersTenantID, tableUsers, true, []string{"tenant_id", "id"}},
		{indexUsersTenantUsername, tableUsers, true, []string{"tenant_id", "username_key"}},
		{indexUsersTenantCreatedAt, tableUsers, false, []string{"tenant_id", "created_at"}},
		{indexRolesTenantID, tableRoles, true, []string{"tenant_id", "id"}},
		{indexRolesTenantName, tableRoles, true, []string{"tenant_id", "name"}},
		{indexRolePermsUnique, tableRolePermissions, true, []string{"tenant_id", "role_id", "permission"}},
		{indexRolePermsTenantRole, tableRolePermissions, false, []string{"tenant_id", "role_id"}},
	}
	listed := listIndexes(t, db)
	for _, w := range want {
		got, ok := listed[w.index]
		if !ok {
			t.Errorf("index %q is missing after Migrate | 迁移后缺少索引 %q", w.index, w.index)
			continue
		}
		if got.table != w.table {
			t.Errorf("index %q is on table %q, want %q | 索引 %q 建在表 %q 上，期望 %q", w.index, got.table, w.table, w.index, got.table, w.table)
		}
		if got.unique != w.unique {
			t.Errorf("index %q unique = %v, want %v | 索引 %q 的唯一性 = %v，期望 %v", w.index, got.unique, w.unique, w.index, got.unique, w.unique)
		}
		cols := indexColumns(t, db, w.index)
		if strings.Join(cols, ",") != strings.Join(w.cols, ",") {
			t.Errorf("index %q columns = %v, want %v | 索引 %q 的列 = %v，期望 %v", w.index, cols, w.cols, w.index, cols, w.cols)
		}
	}
}

// TestMigrateRejectsNilDB pins the sentinel of the misassembled case. | TestMigrateRejectsNilDB 固定「装配错误」这一情形的哨兵。
func TestMigrateRejectsNilDB(t *testing.T) {
	if err := Migrate(context.Background(), nil); err == nil || !errors.Is(err, ErrNilDB) {
		t.Fatalf("Migrate(nil) = %v, want ErrNilDB | Migrate(nil) = %v，期望 ErrNilDB", err, err)
	}
	if _, err := New(nil, Options{}); err == nil || !errors.Is(err, ErrNilDB) {
		t.Fatalf("New(nil) = %v, want ErrNilDB | New(nil) = %v，期望 ErrNilDB", err, err)
	}
}

// TestOptionsFromConfig reads the documented configuration key. | TestOptionsFromConfig 读取文档化的配置键。
func TestOptionsFromConfig(t *testing.T) {
	if got := OptionsFromConfig(nil); got.QueryTimeout != 0 {
		t.Errorf("OptionsFromConfig(nil).QueryTimeout = %v, want 0 | OptionsFromConfig(nil).QueryTimeout = %v，期望 0", got.QueryTimeout, got.QueryTimeout)
	}
}

// indexMeta is one row of the SQLite index catalogue. | indexMeta 是 SQLite 索引目录的一行。
type indexMeta struct {
	// table is the table the index belongs to. | table 是索引所属的表。
	table string
	// unique reports whether the index enforces uniqueness. | unique 表示索引是否强制唯一。
	unique bool
}

// listIndexes reads sqlite_master plus the pragma tables into a map keyed by index
// name. | listIndexes 读取 sqlite_master 与 pragma 元数据，返回以索引名为键的映射。
//
// The catalogue is read from the database itself rather than from GORM's migrator,
// so the assertion is about the DDL that really landed on disk. | 目录信息取自数据库本身而不是 GORM 的
// migrator，因此断言针对的是真正落到磁盘上的 DDL。
func listIndexes(t *testing.T, db *gorm.DB) map[string]indexMeta {
	t.Helper()
	out := make(map[string]indexMeta)
	for _, table := range []string{tableTenants, tableUsers, tableRoles, tableRolePermissions} {
		var rows []struct {
			Name   string `gorm:"column:name"`
			Unique int    `gorm:"column:unique"`
		}
		if err := db.Raw("PRAGMA index_list('" + table + "')").Scan(&rows).Error; err != nil {
			t.Fatalf("PRAGMA index_list(%q) = %v, want nil | PRAGMA index_list(%q) = %v，期望 nil", table, err, table, err)
		}
		for _, row := range rows {
			out[row.Name] = indexMeta{table: table, unique: row.Unique == 1}
		}
	}
	// Indexes declared as table constraints carry no name in PRAGMA index_list, so
	// sqlite_master is checked as well to catch an index that GORM created inline. | 以表约束声明的索引在
	// PRAGMA index_list 里没有名字，因此同时检查 sqlite_master，以捕获 GORM 内联创建的索引。
	var named []struct {
		Name  string `gorm:"column:name"`
		Table string `gorm:"column:tbl_name"`
		SQL   string `gorm:"column:sql"`
	}
	if err := db.Raw("SELECT name, tbl_name, sql FROM sqlite_master WHERE type = 'index' AND name LIKE 'yd_idx_%'").Scan(&named).Error; err != nil {
		t.Fatalf("sqlite_master scan = %v, want nil | sqlite_master 扫描 = %v，期望 nil", err, err)
	}
	for _, row := range named {
		meta, ok := out[row.Name]
		if !ok {
			out[row.Name] = indexMeta{table: row.Table}
			continue
		}
		meta.table = row.Table
		meta.unique = meta.unique || strings.Contains(strings.ToUpper(row.SQL), "UNIQUE")
		out[row.Name] = meta
	}
	return out
}

// indexColumns returns the indexed columns of an index in key order. | indexColumns 按键序返回索引的列。
func indexColumns(t *testing.T, db *gorm.DB, index string) []string {
	t.Helper()
	var rows []struct {
		Seqno int    `gorm:"column:seqno"`
		Name  string `gorm:"column:name"`
	}
	if err := db.Raw("PRAGMA index_info('" + index + "')").Scan(&rows).Error; err != nil {
		t.Fatalf("PRAGMA index_info(%q) = %v, want nil | PRAGMA index_info(%q) = %v，期望 nil", index, err, index, err)
	}
	cols := make([]string, 0, len(rows))
	for _, row := range rows {
		cols = append(cols, row.Name)
	}
	return cols
}

// TestMigratePrefixesEveryObjectWithYD 是「表与索引统一加 yd_ 前缀」这条需求的防回归闸门：
// 迁移之后从 sqlite_master 读出**全部**对象，逐条断言每一张表与每一个由本包命名的索引都以 yd_ 开头。
//
// TestMigratePrefixesEveryObjectWithYD is the regression gate of the yd_ prefix requirement: after Migrate it reads
// **every** object back from sqlite_master and asserts that every table and every index this package names starts
// with yd_. A model or an index that forgets the prefix turns this red at once, instead of surfacing the day it
// collides with a table of the application's own.
//
// 索引断言的是更严的形态 yd_idx_ + 逻辑表名 + 列名，而不是仅仅「以 yd_ 开头」：本包的九个索引都是框架
// 命名策略的派生形态，写成别的样子就意味着显式名与派生名从此长得不一样。
//
// The index assertion is the stricter form, yd_idx_ + logical table name + column, rather than merely "starts with
// yd_": all nine indexes of this package carry the framework naming strategy's derived shape, and any other
// spelling would mean an explicit name and a derived one no longer look alike.
func TestMigratePrefixesEveryObjectWithYD(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "auth.db"))
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() = %v, want nil | Migrate() = %v，期望 nil", err, err)
	}

	var objects []struct {
		Type string `gorm:"column:type"`
		Name string `gorm:"column:name"`
	}
	if err := db.Raw("SELECT type, name FROM sqlite_master ORDER BY type, name").Scan(&objects).Error; err != nil {
		t.Fatalf("sqlite_master scan = %v, want nil | sqlite_master 扫描 = %v，期望 nil", err, err)
	}
	if len(objects) == 0 {
		t.Fatal("sqlite_master is empty after Migrate | 迁移后 sqlite_master 为空")
	}

	var tables, indexes []string
	for _, object := range objects {
		switch object.Type {
		case "table":
			tables = append(tables, object.Name)
		case "index":
			indexes = append(indexes, object.Name)
		}
	}
	// 清单原样写进日志：迁移契约一变，失败信息里就带着完整的前后对照，不必再去猜库是什么样的。
	// The listing is logged verbatim: the moment the migration contract changes, the failure carries the full
	// before-and-after, with no need to guess what the database looked like.
	t.Logf("tables = %v | 表 = %v", tables, tables)
	t.Logf("indexes = %v | 索引 = %v", indexes, indexes)

	for _, name := range tables {
		// sqlite_ 开头的是 SQLite 自己命名、自己维护的对象（sqlite_sequence 是 AUTOINCREMENT 的记账表）：
		// 本包既没有建它们，也没有权力给它们改名，因此按前缀豁免。
		// Names starting with sqlite_ belong to SQLite itself (sqlite_sequence is the AUTOINCREMENT bookkeeping
		// table): this package neither creates them nor may rename them, so the prefix is exempt.
		if strings.HasPrefix(name, "sqlite_") {
			continue
		}
		if !strings.HasPrefix(name, "yd_") {
			t.Errorf("table %q does not start with yd_ | 表 %q 不以 yd_ 开头", name, name)
		}
	}
	for _, name := range indexes {
		// sqlite_autoindex_* 是 SQLite 为 PRIMARY KEY / UNIQUE 约束自动生成的内部索引（本例是 tenants / users / roles
		// 三张表的字符串主键），名字由它自己取，我们从未给它们命名过，因此同样按前缀豁免——
		// 豁免的只是「不是我们起的名字」，不是「没有前缀」。
		// sqlite_autoindex_* is the internal index SQLite generates for a PRIMARY KEY / UNIQUE constraint (the
		// string primary keys of the tenants, users and roles tables here), named by SQLite itself and never named
		// by us, so the same prefix exemption applies — what is exempt is "not a name of ours", not "no prefix".
		if strings.HasPrefix(name, "sqlite_") {
			continue
		}
		if !strings.HasPrefix(name, "yd_idx_") {
			t.Errorf("index %q does not start with yd_idx_ | 索引 %q 不以 yd_idx_ 开头", name, name)
		}
	}
}
