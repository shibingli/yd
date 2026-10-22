package gormcache

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/data/cachex"
	"github.com/shibingli/yd/data/cachex/local"
)

// cacheRow is the test model; it deliberately has no relationships. | cacheRow 是测试模型，刻意不含任何关联。
type cacheRow struct {
	Name string `gorm:"column:name"`
	ID   uint   `gorm:"primaryKey"`
}

// TableName pins the physical table. | TableName 固定物理表名。
func (cacheRow) TableName() string { return "cachex_gorm_rows" }

// quietLogger discards operational noise. | quietLogger 丢弃运行噪音。
func quietLogger() *logx.Logger {
	return logx.New(logx.Options{Writer: io.Discard, Level: logx.ErrorLevel})
}

// openTestDB opens a named shared in-memory database. | openTestDB 打开具名共享内存数据库。
func openTestDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:                                   gormlogger.Default.LogMode(gormlogger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v | 打开数据库失败", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v | 获取连接池失败", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&cacheRow{}); err != nil {
		t.Fatalf("AutoMigrate: %v | 迁移失败", err)
	}
	return db
}

// memoryDSN returns a unique shared in-memory DSN. | memoryDSN 返回唯一的共享内存 DSN。
func memoryDSN(t *testing.T) string {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_", "|", "_", ":", "_").Replace(t.Name())
	return "file:" + name + "?mode=memory&cache=shared&_pragma=busy_timeout(5000)"
}

// newLocalManager builds a local-only manager with a long lease. | newLocalManager 构造长租约的仅本地管理器。
func newLocalManager(t *testing.T) *cachex.Manager {
	t.Helper()
	m, err := cachex.NewManager(
		cachex.WithLocalOptions(local.Options{MaxCost: 1 << 20, Shards: 2, MaxEntrySize: 64 << 10, CleanupInterval: time.Hour}),
		cachex.WithLogger(quietLogger()),
		cachex.WithLease(time.Minute),
	)
	if err != nil {
		t.Fatalf("NewManager: %v | 构造管理器失败", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// install builds and installs a plugin. | install 构造并安装插件。
func install(t *testing.T, db *gorm.DB, m *cachex.Manager, mutate func(*Options)) *Plugin {
	t.Helper()
	o := Options{Manager: m}
	if mutate != nil {
		mutate(&o)
	}
	p, err := New(o)
	if err != nil {
		t.Fatalf("New plugin: %v | 构造插件失败", err)
	}
	if err := db.Use(p); err != nil {
		t.Fatalf("db.Use: %v | 安装插件失败", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// seed inserts rows through GORM. | seed 通过 GORM 插入行。
func seed(t *testing.T, db *gorm.DB, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := db.Create(&cacheRow{Name: name}).Error; err != nil {
			t.Fatalf("Create: %v | 创建失败", err)
		}
	}
}

// directExec bypasses GORM and the plugin. | directExec 绕过 GORM 与插件。
func directExec(t *testing.T, db *gorm.DB, sql string, args ...any) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v | 获取连接池失败", err)
	}
	if _, err := sqlDB.Exec(sql, args...); err != nil {
		t.Fatalf("direct exec: %v | 直接执行失败", err)
	}
}

func TestQueryCacheHit(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	seed(t, db, "a", "b", "c")
	p := install(t, db, newLocalManager(t), nil)
	var first []cacheRow
	if err := db.Order("id").Find(&first).Error; err != nil {
		t.Fatalf("Find: %v | 查询失败", err)
	}
	if len(first) != 3 {
		t.Fatalf("first Find = %d rows | 首次查询行数不符", len(first))
	}
	directExec(t, db, "UPDATE cachex_gorm_rows SET name = 'direct' WHERE id = 1")
	var second []cacheRow
	result := db.Order("id").Find(&second)
	if result.Error != nil {
		t.Fatalf("second Find: %v | 第二次查询失败", result.Error)
	}
	if second[0].Name != "a" {
		t.Fatalf("cached Find saw the direct write: %q | 缓存查询看到了直接写入", second[0].Name)
	}
	if result.RowsAffected != 3 {
		t.Fatalf("RowsAffected = %d, want 3 | 影响行数不符", result.RowsAffected)
	}
	if st := p.Stats(); st.Hits != 1 || st.Misses != 1 {
		t.Fatalf("stats = hits %d misses %d | 命中统计不符", st.Hits, st.Misses)
	}
}

func TestWriteInvalidatesQueries(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	seed(t, db, "a", "b")
	p := install(t, db, newLocalManager(t), nil)
	var rows []cacheRow
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("Find: %v | 查询失败", err)
	}
	if err := db.Model(&cacheRow{}).Where("id = ?", 1).Update("name", "updated").Error; err != nil {
		t.Fatalf("Update: %v | 更新失败", err)
	}
	var after []cacheRow
	if err := db.Order("id").Find(&after).Error; err != nil {
		t.Fatalf("Find: %v | 查询失败", err)
	}
	if after[0].Name != "updated" {
		t.Fatalf("query returned stale value %q | 查询返回了陈旧值", after[0].Name)
	}
	if p.Stats().Writes == 0 {
		t.Fatal("write counter not incremented | 写入计数未递增")
	}
}

func TestCreateAndDeleteInvalidate(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	p := install(t, db, newLocalManager(t), nil)
	var rows []cacheRow
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("Find: %v | 查询失败", err)
	}
	seed(t, db, "new")
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("Find: %v | 查询失败", err)
	}
	if len(rows) != 1 {
		t.Fatalf("after create = %d rows, want 1 | 创建后行数不符", len(rows))
	}
	if err := db.Delete(&cacheRow{}, 1).Error; err != nil {
		t.Fatalf("Delete: %v | 删除失败", err)
	}
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("Find: %v | 查询失败", err)
	}
	if len(rows) != 0 {
		t.Fatalf("after delete = %d rows, want 0 | 删除后行数不符", len(rows))
	}
	if p.Stats().Writes == 0 {
		t.Fatal("write counter not incremented | 写入计数未递增")
	}
}

func TestTTLExpiresCachedQuery(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	seed(t, db, "old")
	p := install(t, db, newLocalManager(t), func(o *Options) { o.TTL = 20 * time.Millisecond })
	var rows []cacheRow
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("Find: %v | 查询失败", err)
	}
	directExec(t, db, "UPDATE cachex_gorm_rows SET name = 'new' WHERE id = 1")
	time.Sleep(30 * time.Millisecond)
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("Find: %v | 查询失败", err)
	}
	if rows[0].Name != "new" {
		t.Fatalf("after TTL = %q, want new | TTL 到期后仍是陈旧值", rows[0].Name)
	}
	_ = p
}

func TestTransactionQueriesAreNotCached(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	seed(t, db, "outside")
	p := install(t, db, newLocalManager(t), nil)
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&cacheRow{Name: "inside"}).Error; err != nil {
			return err
		}
		var rows []cacheRow
		if err := tx.Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != 2 {
			t.Fatalf("inside transaction = %d rows, want 2 | 事务内查询行数不符", len(rows))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Transaction: %v | 事务失败", err)
	}
	if st := p.Stats(); st.Hits != 0 {
		t.Fatalf("transaction query hit the cache: %d | 事务内查询命中了缓存", st.Hits)
	}
	var rows []cacheRow
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("Find: %v | 查询失败", err)
	}
	if len(rows) != 2 {
		t.Fatalf("after commit = %d rows, want 2 | 提交后行数不符", len(rows))
	}
}

func TestRecordNotFoundIsNotCached(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	install(t, db, newLocalManager(t), nil)
	var one cacheRow
	if err := db.First(&one, 99).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("First missing = %v | 缺失查询错误不符", err)
	}
	if err := db.Create(&cacheRow{ID: 99, Name: "late"}).Error; err != nil {
		t.Fatalf("Create: %v | 创建失败", err)
	}
	if err := db.First(&one, 99).Error; err != nil {
		t.Fatalf("First after create = %v | 创建后仍未查到", err)
	}
}

func TestRawQueriesBypassCache(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	seed(t, db, "a")
	p := install(t, db, newLocalManager(t), nil)
	var rows []cacheRow
	for i := 0; i < 2; i++ {
		if err := db.Raw("SELECT * FROM cachex_gorm_rows").Scan(&rows).Error; err != nil {
			t.Fatalf("Raw Scan: %v | 原始查询失败", err)
		}
	}
	if st := p.Stats(); st.Hits != 0 {
		t.Fatalf("raw query hit the cache: %d | 原始查询命中了缓存", st.Hits)
	}
}

func TestTableFilters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Options)
	}{
		{name: "allowlist | 白名单", mutate: func(o *Options) { o.Tables = []string{"other_table"} }},
		{name: "denylist | 黑名单", mutate: func(o *Options) { o.IgnoreTables = []string{"cachex_gorm_rows"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t, memoryDSN(t))
			seed(t, db, "a")
			p := install(t, db, newLocalManager(t), tc.mutate)
			var rows []cacheRow
			for i := 0; i < 2; i++ {
				if err := db.Find(&rows).Error; err != nil {
					t.Fatalf("Find: %v | 查询失败", err)
				}
			}
			if st := p.Stats(); st.Hits != 0 || st.Writes != 0 {
				t.Fatalf("filtered table used the cache: hits %d writes %d | 被过滤的表使用了缓存", st.Hits, st.Writes)
			}
		})
	}
}

func TestManualInvalidate(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	seed(t, db, "old")
	p := install(t, db, newLocalManager(t), nil)
	var rows []cacheRow
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("Find: %v | 查询失败", err)
	}
	directExec(t, db, "UPDATE cachex_gorm_rows SET name = 'new' WHERE id = 1")
	if err := p.Invalidate(context.Background(), "cachex_gorm_rows"); err != nil {
		t.Fatalf("Invalidate: %v | 手动失效失败", err)
	}
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("Find: %v | 查询失败", err)
	}
	if rows[0].Name != "new" {
		t.Fatalf("after Invalidate = %q, want new | 手动失效后仍是陈旧值", rows[0].Name)
	}
}

func TestCrossInstanceInvalidation(t *testing.T) {
	mr := miniredis.RunT(t)
	cli1 := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cli2 := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = cli1.Close()
		_ = cli2.Close()
	})
	remote1, err := cachex.NewRedisRemote(cli1, cachex.RedisOptions{Prefix: "xt:", HealthInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("remote1: %v | 构造远端失败", err)
	}
	remote2, err := cachex.NewRedisRemote(cli2, cachex.RedisOptions{Prefix: "xt:", HealthInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("remote2: %v | 构造远端失败", err)
	}
	localOpts := cachex.WithLocalOptions(local.Options{MaxCost: 1 << 20, Shards: 2, MaxEntrySize: 64 << 10, CleanupInterval: time.Hour})
	mgr1, err := cachex.NewManager(cachex.WithRemote(remote1), localOpts, cachex.WithLogger(quietLogger()), cachex.WithLease(time.Minute))
	if err != nil {
		t.Fatalf("manager1: %v | 构造管理器失败", err)
	}
	t.Cleanup(func() { _ = mgr1.Close() })
	mgr2, err := cachex.NewManager(cachex.WithRemote(remote2), localOpts, cachex.WithLogger(quietLogger()), cachex.WithLease(time.Minute))
	if err != nil {
		t.Fatalf("manager2: %v | 构造管理器失败", err)
	}
	t.Cleanup(func() { _ = mgr2.Close() })

	path := filepath.Join(t.TempDir(), "shared.db")
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db1 := openTestDB(t, dsn)
	db2 := openTestDB(t, dsn)
	seed(t, db1, "old")
	install(t, db1, mgr1, nil)
	install(t, db2, mgr2, nil)
	// Give both Pub/Sub subscriptions time to attach. | 给两个 Pub/Sub 订阅一点挂载时间。
	time.Sleep(100 * time.Millisecond)

	var rows1 []cacheRow
	if err := db1.Order("id").Find(&rows1).Error; err != nil {
		t.Fatalf("db1 Find: %v | 节点 1 查询失败", err)
	}
	var rows2 []cacheRow
	if err := db2.Order("id").Find(&rows2).Error; err != nil {
		t.Fatalf("db2 Find: %v | 节点 2 查询失败", err)
	}
	if err := db1.Model(&cacheRow{}).Where("id = ?", 1).Update("name", "new").Error; err != nil {
		t.Fatalf("Update: %v | 更新失败", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := db2.Order("id").Find(&rows2).Error; err != nil {
			t.Fatalf("db2 Find: %v | 节点 2 查询失败", err)
		}
		if rows2[0].Name == "new" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("db2 stayed stale: %q | 节点 2 一直返回陈旧值", rows2[0].Name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestInitializeErrors(t *testing.T) {
	m := newLocalManager(t)
	if _, err := New(Options{}); !errors.Is(err, ErrNilManager) {
		t.Fatalf("nil manager = %v | nil 管理器错误不符", err)
	}
	p, err := New(Options{Manager: m})
	if err != nil {
		t.Fatalf("New: %v | 构造失败", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if err := p.Initialize(nil); !errors.Is(err, ErrNilDB) {
		t.Fatalf("nil db = %v | nil 数据库错误不符", err)
	}
	db := openTestDB(t, memoryDSN(t))
	if err := p.Initialize(db); err != nil {
		t.Fatalf("Initialize: %v | 初始化失败", err)
	}
	if err := p.Initialize(db); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("double Initialize = %v | 重复初始化错误不符", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v | 关闭失败", err)
	}
	if err := p.Invalidate(context.Background(), "x"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Invalidate after Close = %v | 关闭后失效错误不符", err)
	}
}

func TestResyncInvalidatesEveryGeneration(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	p := install(t, db, newLocalManager(t), nil)
	before := p.epoch.Load()
	p.onEvent(cachex.Event{Op: cachex.OpResync})
	if got := p.epoch.Load(); got != before+1 {
		t.Fatalf("resync epoch = %d, want %d | 重同步纪元不符", got, before+1)
	}
	p.onEvent(cachex.Event{Op: cachex.OpClear})
	if got := p.epoch.Load(); got != before+2 {
		t.Fatalf("clear epoch = %d, want %d | 全清纪元不符", got, before+2)
	}
}

func TestJoinQueriesBypassCache(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	seed(t, db, "a")
	p := install(t, db, newLocalManager(t), nil)
	var rows []cacheRow
	query := db.Table("cachex_gorm_rows AS a").
		Joins("JOIN cachex_gorm_rows AS b ON b.id = a.id").
		Select("a.*")
	for i := 0; i < 2; i++ {
		if err := query.Find(&rows).Error; err != nil {
			t.Fatalf("join Find: %v | JOIN 查询失败", err)
		}
	}
	if st := p.Stats(); st.Hits != 0 {
		t.Fatalf("join query hit the cache: %d | JOIN 查询命中了缓存", st.Hits)
	}
}
