package gormx_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/yun-dev/yd/afs"
	"github.com/yun-dev/yd/config"
	"github.com/yun-dev/yd/data/gormx"
	"github.com/yun-dev/yd/logx"
)

// NewLogger 的返回值必须满足 GORM 的日志接口；其动态类型还应额外实现
// gorm.ParamsFilter，把口令等敏感实参从日志中抹除（运行时断言见下方测试）。
var _ gormlogger.Interface = gormx.NewLogger(nil, 0, "")

// probeRow 是 AutoMigrate 测试用的最小模型。
type probeRow struct {
	ID   uint   `gorm:"primaryKey"`
	Name string `gorm:"size:64"`
}

// TableName 固定表名，避免测试依赖 GORM 的复数化规则。
func (probeRow) TableName() string { return "gormx_probe_rows" }

// memoryDSN 返回进程内内存库的 DSN。
//
// 每个测试使用独立命名（mode=memory&cache=shared）而非共享的 "file::memory:"，
// 从而避免同一测试进程内多个用例通过共享缓存互相看到对方的表。
func memoryDSN(t *testing.T) string {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_", "#", "_").Replace(t.Name())
	return "file:" + name + "?mode=memory&cache=shared"
}

// newBufLogger 返回写入 buf 的 logx 日志器（默认 JSON、Info 及以上级别）。
func newBufLogger(buf *bytes.Buffer) *logx.Logger {
	return logx.New(logx.Options{Writer: buf})
}

// openProbe 用给定选项打开一个 SQLite 内存库，并在测试结束时关闭。
func openProbe(t *testing.T, o gormx.Options, l *logx.Logger) *gorm.DB {
	t.Helper()
	if o.Driver == "" {
		o.Driver = gormx.DriverSQLite
	}
	if o.DSN == "" {
		o.DSN = memoryDSN(t)
	}
	db, err := gormx.Open(o, l)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	t.Cleanup(func() { _ = gormx.Close(db) })
	return db
}

// TestOpenSQLiteWithPrepareStmtEnabled 断言预编译语句缓存默认开启，
// 且同一 SQL 连续执行两次只产生一条缓存语句（即两次都走预编译路径）。
func TestOpenSQLiteWithPrepareStmtEnabled(t *testing.T) {
	db := openProbe(t, gormx.Options{}, newBufLogger(&bytes.Buffer{}))

	if !db.Config.PrepareStmt {
		t.Fatalf("db.Config.PrepareStmt = false，期望框架默认开启")
	}

	ps, ok := db.ConnPool.(*gorm.PreparedStmtDB)
	if !ok {
		t.Fatalf("db.ConnPool 类型 = %T，期望 *gorm.PreparedStmtDB", db.ConnPool)
	}

	if err := db.Exec("CREATE TABLE gormx_prepare_probe (id INTEGER PRIMARY KEY, name TEXT)").Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	before := len(ps.Stmts.Keys())

	const query = "SELECT count(*) FROM gormx_prepare_probe WHERE name = ?"
	for i := 0; i < 2; i++ {
		var got int64
		if err := db.Raw(query, "probe").Scan(&got).Error; err != nil {
			t.Fatalf("第 %d 次执行查询失败: %v", i+1, err)
		}
	}

	if added := len(ps.Stmts.Keys()) - before; added != 1 {
		t.Fatalf("同一 SQL 连续执行两次新增了 %d 条预编译语句，期望 1 条（第二次应复用首次的预编译语句）", added)
	}
}

// TestSlowQueryLoggedThroughLogx 断言超过阈值的查询经 logx.SlowQuery 打点，
// 日志中出现 slow_query 与 duration 字段。
func TestSlowQueryLoggedThroughLogx(t *testing.T) {
	var buf bytes.Buffer
	db := openProbe(t, gormx.Options{SlowThreshold: time.Nanosecond, LogLevel: "warn"}, newBufLogger(&buf))

	if err := db.Exec("CREATE TABLE gormx_slow_probe (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	buf.Reset()

	if err := db.Exec("INSERT INTO gormx_slow_probe (id) VALUES (?)", 1).Error; err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("慢查询未产生任何日志")
	}
	for _, want := range []string{`"slow_query":true`, `"duration"`, `"sql"`, `"rows"`, "gormx_slow_probe", `"level":"warn"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("慢查询日志缺少 %s: %s", want, out)
		}
	}
}

// TestNewLoggerRelaysToLogx 断言 GORM 日志适配器只经 logx 输出：
// 中英同步的事件名 + 上游文案字段，并在 silent 级别下完全静默。
func TestNewLoggerRelaysToLogx(t *testing.T) {
	var buf bytes.Buffer
	l := gormx.NewLogger(newBufLogger(&buf), time.Millisecond, "info")
	if _, ok := l.(gorm.ParamsFilter); !ok {
		t.Fatalf("NewLogger 返回值 %T 未实现 gorm.ParamsFilter", l)
	}

	l.Info(context.Background(), "creating table %v", "gormx_probe_rows")
	l.Warn(context.Background(), "record not found")

	out := buf.String()
	if !strings.Contains(out, "gorm info | GORM 提示") || !strings.Contains(out, "gorm warning | GORM 告警") {
		t.Fatalf("日志事件名未中英同步: %s", out)
	}
	if !strings.Contains(out, `"message":"creating table gormx_probe_rows"`) {
		t.Fatalf("GORM 上游文案未进入 message 字段: %s", out)
	}

	buf.Reset()
	silent := l.LogMode(gormlogger.Silent)
	silent.Info(context.Background(), "creating table %v", "gormx_probe_rows")
	silent.Trace(context.Background(), time.Now(), func() (string, int64) { return "SELECT 1", 1 }, nil)
	if out := buf.String(); out != "" {
		t.Fatalf("silent 级别仍产生了日志: %s", out)
	}
}

// TestConnPoolSettingsApplied 断言连接池参数落到 sql.DB 上，
// 且未显式给出的参数按框架默认补齐。
func TestConnPoolSettingsApplied(t *testing.T) {
	db := openProbe(t, gormx.Options{MaxOpenConns: 7, MaxIdleConns: 3}, nil)

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取底层 *sql.DB 失败: %v", err)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections = %d，期望 7", got)
	}

	def := openProbe(t, gormx.Options{}, nil)
	defSQL, err := def.DB()
	if err != nil {
		t.Fatalf("获取底层 *sql.DB 失败: %v", err)
	}
	if got := defSQL.Stats().MaxOpenConnections; got != 100 {
		t.Fatalf("默认 MaxOpenConnections = %d，期望 100", got)
	}
}

// TestAutoMigrateAndHealth 断言迁移、写入、探活与关闭的完整生命周期。
func TestAutoMigrateAndHealth(t *testing.T) {
	ctx := context.Background()
	db := openProbe(t, gormx.Options{}, newBufLogger(&bytes.Buffer{}))

	if err := gormx.AutoMigrate(db, &probeRow{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	if err := db.Create(&probeRow{Name: "alice"}).Error; err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	var got probeRow
	if err := db.First(&got, "name = ?", "alice").Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if got.ID == 0 {
		t.Fatalf("查询结果 = %+v，期望 ID 非零", got)
	}

	if err := gormx.Health(ctx, db); err != nil {
		t.Fatalf("Health 失败: %v", err)
	}
	if err := gormx.Close(db); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	if err := gormx.Health(ctx, db); err == nil {
		t.Fatal("Close 之后 Health 应返回错误")
	}
}

// TestParamsFilterRedactsSensitive 断言 ? 位置上的实参被替换为 "?"，
// 且端到端执行时口令明文不会出现在日志里。
func TestParamsFilterRedactsSensitive(t *testing.T) {
	ctx := context.Background()
	const (
		secret = "s3cr3t-p@ssw0rd"
		sql    = "INSERT INTO users (name, password) VALUES (?, ?)"
	)

	gotSQL, gotParams := gormx.ParamsFilter(ctx, sql)
	if gotSQL != sql {
		t.Fatalf("ParamsFilter 返回的 SQL = %q，期望原样返回 %q", gotSQL, sql)
	}
	if len(gotParams) != 2 {
		t.Fatalf("ParamsFilter 返回 %d 个参数，期望 2 个", len(gotParams))
	}
	for i, p := range gotParams {
		if p != "?" {
			t.Fatalf("第 %d 个参数 = %v，期望被替换为 \"?\"", i+1, p)
		}
	}

	var buf bytes.Buffer
	db := openProbe(t, gormx.Options{SlowThreshold: time.Nanosecond, LogLevel: "info"}, newBufLogger(&buf))
	if err := db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, password TEXT)").Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	buf.Reset()

	if err := db.Exec(sql, "alice", secret).Error; err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("日志泄漏了口令明文: %s", out)
	}
	if !strings.Contains(out, "users") {
		t.Fatalf("日志应保留 SQL 骨架: %s", out)
	}
}

// TestUnsupportedDriverReturnsTypedError 断言未知驱动返回可判定的类型化错误。
func TestUnsupportedDriverReturnsTypedError(t *testing.T) {
	for _, driver := range []string{"oracle", "mongodb", "SQL Server"} {
		db, err := gormx.Open(gormx.Options{Driver: driver, DSN: "dsn"}, nil)
		if db != nil {
			_ = gormx.Close(db)
			t.Fatalf("驱动 %q 期望返回 nil *gorm.DB", driver)
		}
		if !errors.Is(err, gormx.ErrUnsupportedDriver) {
			t.Fatalf("驱动 %q 的错误 = %v，期望包装 ErrUnsupportedDriver", driver, err)
		}
		if !strings.Contains(err.Error(), driver) {
			t.Fatalf("错误信息 %q 应包含驱动名 %q", err.Error(), driver)
		}
	}
}

// TestOptionsFromConfigReadsDbSection 断言 OptionsFromConfig 读取 db 段，
// 且缺省配置下回落到框架默认值（sqlite / 100 / 10 / 200ms / 预编译开启）。
func TestOptionsFromConfigReadsDbSection(t *testing.T) {
	mem := afs.NewMem()
	const yaml = `
db:
  driver: postgres
  dsn: "postgres://user:pw@127.0.0.1:5432/yd?sslmode=disable"
  maxconn: 42
  maxidle: 7
  slow_threshold: 350ms
  prepare_stmt: true
`
	if err := afero.WriteFile(mem, "config.yaml", []byte(yaml), 0o644); err != nil {
		t.Fatalf("写入内存配置文件失败: %v", err)
	}

	// EnvPrefix 刻意使用测试专用前缀，隔离开发机上真实的 YD_DB_* 环境变量。
	load := func(t *testing.T, fs afero.Fs) *config.Config {
		t.Helper()
		c, err := config.Load(config.Options{
			Name:       "gormx",
			ConfigDir:  ".",
			ConfigName: "config",
			EnvPrefix:  "YDGORMXTEST",
			EnvFile:    "absent.env",
			FS:         fs,
		})
		if err != nil {
			t.Fatalf("config.Load 失败: %v", err)
		}
		return c
	}

	got := gormx.OptionsFromConfig(load(t, mem))
	want := gormx.Options{
		Driver:        "postgres",
		DSN:           "postgres://user:pw@127.0.0.1:5432/yd?sslmode=disable",
		MaxOpenConns:  42,
		MaxIdleConns:  7,
		SlowThreshold: 350 * time.Millisecond,
		PrepareStmt:   true,
	}
	if got != want {
		t.Fatalf("OptionsFromConfig = %+v，期望 %+v", got, want)
	}

	def := gormx.OptionsFromConfig(load(t, afs.NewMem()))
	if def.Driver != "sqlite" || def.MaxOpenConns != 100 || def.MaxIdleConns != 10 {
		t.Fatalf("默认 Options = %+v，期望 sqlite/100/10", def)
	}
	if def.SlowThreshold != 200*time.Millisecond || !def.PrepareStmt {
		t.Fatalf("默认 Options = %+v，期望 200ms 且预编译开启", def)
	}
}
