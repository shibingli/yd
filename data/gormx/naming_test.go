package gormx_test

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shibingli/yd/data/gormx"
)

// appProbeRow 是「不属于 security/auth/gormstore 的使用方模型」：它刻意不实现 TableName()，
// 因此表名与索引名完全由框架命名策略决定 —— 这正是本组测试要证明的那条路径。
//
// appProbeRow is the probing model of an application: it deliberately implements no TableName(), so its table and
// its indexes come from the framework naming strategy alone — which is exactly the path this group proves.
type appProbeRow struct {
	// ID is the primary key. | ID 是主键。
	ID uint `gorm:"primaryKey"`
	// TenantID carries a plain derived index: the tag names no index, so GORM asks the strategy for one. | TenantID 带一个朴素的派生索引：标签没写索引名，GORM 因此向命名策略索取。
	TenantID string `gorm:"size:64;index"`
	// Slug carries a derived unique index, whose name the strategy derives as well. | Slug 带一个派生唯一索引，其名字同样由命名策略派生。
	Slug string `gorm:"size:64;uniqueIndex"`
}

// appProbeTable 是策略应当为 appProbeRow 产出的表名。 | appProbeTable is the table name the strategy must produce for appProbeRow.
const appProbeTable = "yd_app_probe_rows"

// TestOpenMigratesApplicationModelsUnderYDPrefix 是「使用方模型也带前缀」的实测证据：
// 用 gormx.Open 打开真实 SQLite，迁移一个不属于 security/auth/gormstore 的模型，然后直接从 sqlite_master
// 读回它落到的表与索引名 —— 断言的是库里的 DDL，而不是策略的自述。
//
// TestOpenMigratesApplicationModelsUnderYDPrefix is the measured evidence that application models carry the prefix
// too: it opens a real SQLite through gormx.Open, migrates a model that belongs to no framework package, and reads
// the table and index names back from sqlite_master — so the assertion is about the DDL in the database, not about
// what the strategy says about itself.
func TestOpenMigratesApplicationModelsUnderYDPrefix(t *testing.T) {
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "naming.db"))
	db := openProbe(t, gormx.Options{DSN: dsn}, newBufLogger(&bytes.Buffer{}))

	if err := gormx.AutoMigrate(db, &appProbeRow{}); err != nil {
		t.Fatalf("AutoMigrate() = %v, want nil | AutoMigrate() = %v，期望 nil", err, err)
	}
	if !db.Migrator().HasTable(appProbeTable) {
		t.Fatalf("table %q is missing after AutoMigrate | 迁移后缺少表 %q", appProbeTable, appProbeTable)
	}

	var tables []string
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type = 'table'").Scan(&tables).Error; err != nil {
		t.Fatalf("sqlite_master scan = %v, want nil | sqlite_master 扫描 = %v，期望 nil", err, err)
	}
	if !containsString(tables, appProbeTable) {
		t.Errorf("sqlite_master tables = %v, want one named %q | sqlite_master 的表 = %v，期望含有 %q", tables, appProbeTable, tables, appProbeTable)
	}

	// 索引名从 sqlite_master 逐条读回：派生索引必须带 yd_idx_ 前缀，且形态是「前缀 + 去掉表前缀的表名 + 列名」。
	// Every index name is read back from sqlite_master: a derived index must carry the yd_idx_ prefix and the shape
	// "prefix + table name without the table prefix + column".
	want := []string{
		"yd_idx_app_probe_rows_slug",
		"yd_idx_app_probe_rows_tenant_id",
	}
	var indexes []string
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name NOT LIKE 'sqlite_%'", appProbeTable).
		Scan(&indexes).Error; err != nil {
		t.Fatalf("sqlite_master index scan = %v, want nil | sqlite_master 索引扫描 = %v，期望 nil", err, err)
	}
	// 建索引的先后不构成契约，因此两边都排序后再比。
	// The order indexes were created in is no contract, so both sides are sorted before the comparison.
	slices.Sort(indexes)
	slices.Sort(want)
	if strings.Join(indexes, ",") != strings.Join(want, ",") {
		t.Errorf("derived indexes = %v, want %v | 派生索引 = %v，期望 %v", indexes, want, indexes, want)
	}
	for _, index := range indexes {
		if !strings.HasPrefix(index, gormx.IndexPrefix) {
			t.Errorf("index %q does not start with %q | 索引 %q 不以 %q 开头", index, gormx.IndexPrefix, index, gormx.IndexPrefix)
		}
	}
	// 证据原样写进日志：模型没有 TableName()，落到的却是 yd_ 表与 yd_idx_ 索引。
	// The evidence is logged verbatim: the model has no TableName(), yet it lands in a yd_ table with yd_idx_ indexes.
	t.Logf("application model table = %q, indexes = %v | 使用方模型表 = %q，索引 = %v", appProbeTable, indexes, appProbeTable, indexes)
}

// containsString 报告 list 里是否有且仅有 value。 | containsString reports whether list holds value.
func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// TestNamingStrategyPrefixesTablesAndIndexes 固定策略的两条明文规则：表名前缀与索引名形态。
//
// TestNamingStrategyPrefixesTablesAndIndexes pins the strategy's two stated rules: the table prefix and the index
// name shape. The index rule is deliberately checked in all three spellings the plan rejects or accepts, so a
// later "fix" that produces idx_yd_auth_users_... or yd_idx_yd_auth_users_... fails here instead of reaching a
// database.
func TestNamingStrategyPrefixesTablesAndIndexes(t *testing.T) {
	n := gormx.DefaultNamingStrategy()

	if got := n.TableName("User"); got != "yd_users" {
		t.Errorf("TableName(User) = %q, want %q | TableName(User) = %q，期望 %q", got, "yd_users", got, "yd_users")
	}

	cases := []struct {
		name   string
		table  string
		column string
		want   string
	}{
		{"framework table", "yd_auth_users", "tenant_id", "yd_idx_auth_users_tenant_id"},
		{"application table", "yd_users", "email", "yd_idx_users_email"},
		{"table pinned by TableName() without the prefix", "legacy_users", "email", "yd_idx_legacy_users_email"},
		{"composite name given as a phrase", "yd_auth_role_permissions", "tenant_role", "yd_idx_auth_role_permissions_tenant_role"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := n.IndexName(c.table, c.column)
			if got != c.want {
				t.Errorf("IndexName(%q, %q) = %q, want %q | IndexName(%q, %q) = %q，期望 %q", c.table, c.column, got, c.want, c.table, c.column, got, c.want)
			}
			if !strings.HasPrefix(got, gormx.IndexPrefix) {
				t.Errorf("IndexName(%q, %q) = %q does not start with %q | IndexName(%q, %q) = %q 不以 %q 开头", c.table, c.column, got, gormx.IndexPrefix, c.table, c.column, got, gormx.IndexPrefix)
			}
			if strings.Contains(got, gormx.IndexPrefix+gormx.TablePrefix) {
				t.Errorf("IndexName(%q, %q) = %q repeats the table prefix | IndexName(%q, %q) = %q 重复了表前缀", c.table, c.column, got, c.table, c.column, got)
			}
		})
	}
}

// TestIndexNameTruncatesLongNamesWithHash 固定超长名的截断规则：PostgreSQL 的 63 字节上限之下，
// 结果按字节截到上限并以名称散列收尾，而**不是**直接截断——同头不同尾的两个索引必须留在两个名字里，
// 否则它们会无声地变成一个索引。
//
// TestIndexNameTruncatesLongNamesWithHash pins the truncation rule for over-long names: under PostgreSQL's 63-byte
// cap the result is cut to the limit in bytes and ends with a hash of the name, and it is deliberately **not** a
// plain cut — two indexes sharing a head but differing in the tail must stay two names, or they would silently
// become one index.
func TestIndexNameTruncatesLongNamesWithHash(t *testing.T) {
	n := gormx.DefaultNamingStrategy()
	// 表名本身就长于上限，两个用例因此共享同一段前缀，只在尾部不同。
	// The table name alone exceeds the limit, so the two cases share a long head and differ only in the tail.
	longTable := gormx.TablePrefix + strings.Repeat("segment_", 8) + "sessions"

	first := n.IndexName(longTable, "tenant_id")
	second := n.IndexName(longTable, "tenant_code")

	if len(first) != gormx.IdentifierMaxLength {
		t.Errorf("len(IndexName(long, tenant_id)) = %d, want %d | 超长索引名的长度 = %d，期望 %d", len(first), gormx.IdentifierMaxLength, len(first), gormx.IdentifierMaxLength)
	}
	if len(second) != gormx.IdentifierMaxLength {
		t.Errorf("len(IndexName(long, tenant_code)) = %d, want %d | 超长索引名的长度 = %d，期望 %d", len(second), gormx.IdentifierMaxLength, len(second), gormx.IdentifierMaxLength)
	}
	if !strings.HasPrefix(first, gormx.IndexPrefix) {
		t.Errorf("truncated index %q does not start with %q | 截断后的索引 %q 不以 %q 开头", first, gormx.IndexPrefix, first, gormx.IndexPrefix)
	}
	if first == second {
		t.Errorf("two long names collided after truncation: %q | 两个长名字截断后撞成同一个：%q", first, first)
	}
	// 与「直接截断」对质：完整名的前 63 字节若就是结果，说明散列后缀根本没加上。
	// Confronted with a plain cut: if the first 63 bytes of the full name were the result, the hash suffix would be missing.
	plainCut := gormx.IndexPrefix + strings.TrimPrefix(longTable, gormx.TablePrefix) + "_tenant_id"
	if first == plainCut[:gormx.IdentifierMaxLength] {
		t.Errorf("index %q is a plain cut of the full name, the hash suffix is missing | 索引 %q 只是完整名的直接截断，缺少散列后缀", first, first)
	}
	// 同一次派生必须稳定：名字进日志、进迁移比较，抖动会让每次启动都重建索引。
	// The same derivation must be stable: the name reaches logs and migration comparisons, and a jittering name
	// would rebuild the index on every start.
	if again := n.IndexName(longTable, "tenant_id"); again != first {
		t.Errorf("IndexName is not deterministic: %q then %q | IndexName 不稳定：先 %q 后 %q", first, again, first, again)
	}
	// 上限之内一个字节都不动：短名字不截断、不改写。
	// Within the limit nothing is touched: a short name is neither cut nor rewritten.
	if got := n.IndexName("yd_users", "email"); got != "yd_idx_users_email" {
		t.Errorf("short IndexName = %q, want it untouched | 短索引名 = %q，期望原样返回", got, got)
	}
}
