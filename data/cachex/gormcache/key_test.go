package gormcache

import (
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
)

// barePlugin builds a plugin with only the generation map; queryKey needs nothing else. | barePlugin 只构造代际映射，queryKey 不需要其它字段。
func barePlugin() *Plugin {
	return &Plugin{gens: make(map[string]*atomic.Uint64)}
}

// buildQuery returns a dry-run statement with its SQL built. | buildQuery 返回已构建 SQL 的 dry-run 语句。
func buildQuery(t *testing.T, db *gorm.DB, dest any, where string, args ...any) *gorm.DB {
	t.Helper()
	stmt := db.Session(&gorm.Session{DryRun: true}).Where(where, args...).Order("id").Find(dest)
	callbacks.BuildQuerySQL(stmt)
	if stmt.Error != nil {
		t.Fatalf("build query: %v | 构建查询失败", stmt.Error)
	}
	if stmt.Statement.SQL.Len() == 0 {
		t.Fatal("query SQL was not built | 查询 SQL 未构建")
	}
	return stmt
}

func TestQueryKeyIsStable(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	p := barePlugin()
	var rows []cacheRow
	k1, ok := p.queryKey(buildQuery(t, db, &rows, "name = ?", "a"))
	if !ok {
		t.Fatal("queryKey rejected a normal query | queryKey 拒绝了普通查询")
	}
	k2, ok := p.queryKey(buildQuery(t, db, &rows, "name = ?", "a"))
	if !ok || k1 != k2 {
		t.Fatalf("same query produced %q and %q | 同一查询的键不稳定", k1, k2)
	}
	other := barePlugin()
	k3, ok := other.queryKey(buildQuery(t, db, &rows, "name = ?", "a"))
	if !ok || k3 != k1 {
		t.Fatalf("two processes disagree on the key: %q vs %q | 两个进程的键不一致", k1, k3)
	}
	k4, _ := p.queryKey(buildQuery(t, db, &rows, "name = ?", "b"))
	if k4 == k1 {
		t.Fatal("different bind variables produced the same key | 不同绑定变量生成了相同键")
	}
}

func TestQueryKeyChangesWithGenerations(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	p := barePlugin()
	var rows []cacheRow
	base, ok := p.queryKey(buildQuery(t, db, &rows, "name = ?", "a"))
	if !ok {
		t.Fatal("queryKey failed | queryKey 失败")
	}
	p.genFor("cachex_gorm_rows").Add(1)
	afterGen, _ := p.queryKey(buildQuery(t, db, &rows, "name = ?", "a"))
	if afterGen == base {
		t.Fatal("table generation did not change the key | 表代际未改变键")
	}
	p.epoch.Add(1)
	afterEpoch, _ := p.queryKey(buildQuery(t, db, &rows, "name = ?", "a"))
	if afterEpoch == afterGen {
		t.Fatal("global epoch did not change the key | 全局纪元未改变键")
	}
}

func TestQueryKeyRejectsBadInput(t *testing.T) {
	db := openTestDB(t, memoryDSN(t))
	p := barePlugin()
	var rows []cacheRow
	if _, ok := p.queryKey(buildQuery(t, db, &rows, "name = ?", make(chan int))); ok {
		t.Fatal("accepted an unserializable variable | 接受了无法序列化的变量")
	}
	stmt := db.Session(&gorm.Session{DryRun: true})
	stmt.Statement.Dest = &rows
	if _, ok := p.queryKey(stmt); ok {
		t.Fatal("accepted a statement without SQL | 接受了没有 SQL 的语句")
	}
}

func TestGenerationAdoptionAdvancesMonotonically(t *testing.T) {
	p := barePlugin()
	p.adopt("t", 5)
	if got := p.genFor("t").Load(); got != 5 {
		t.Fatalf("adopt(5) = %d | 代际采纳不符", got)
	}
	p.adopt("t", 3)
	if got := p.genFor("t").Load(); got != 6 {
		t.Fatalf("adopt(3) did not advance past the local generation: %d | 本地领先时未推进代际", got)
	}
	p.adopt("t", 9)
	if got := p.genFor("t").Load(); got != 9 {
		t.Fatalf("adopt(9) = %d | 代际采纳不符", got)
	}
	p.adopt("t", 9)
	if got := p.genFor("t").Load(); got != 10 {
		t.Fatalf("duplicate adopt(9) did not advance: %d | 重复事件未推进代际", got)
	}
}
