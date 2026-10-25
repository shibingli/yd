// This file tests cursor encoding, keyset SQL per dialect and the keyset page reader. | 本文件测试游标编码、
// 各方言的键集 SQL 与键集分页读取。

package paging

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// namedDialector lets a test exercise dialect detection without opening a database. | namedDialector 让测试
// 无需打开数据库即可验证方言识别。
type namedDialector struct {
	gorm.Dialector
	name string
}

// Name returns the dialect name. | Name 返回方言名。
func (d namedDialector) Name() string { return d.name }

// TestDialectOf pins the driver-name mapping used by every store. | TestDialectOf 固化所有存储共用的驱动名映射。
func TestDialectOf(t *testing.T) {
	tests := []struct {
		name string
		got  Dialect
	}{
		{"mysql", MySQL},
		{"postgres", PostgreSQL},
		{"postgresql", PostgreSQL},
		{"sqlite", SQLite},
		{"sqlite3", SQLite},
		{"dm", DM},
		{"dameng", DM},
		{"oracle", Unknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &gorm.DB{Config: &gorm.Config{Dialector: namedDialector{name: tc.name}}}
			if got := DialectOf(db); got != tc.got {
				t.Errorf("DialectOf(%q) = %q，期望 %q", tc.name, got, tc.got)
			}
		})
	}
	if DialectOf(nil) != Unknown {
		t.Error("nil 数据库应返回 Unknown | a nil database must be Unknown")
	}
}

// TestPredicatePerDialect pins the SQL and arguments of each supported dialect. | TestPredicatePerDialect
// 固化每种支持方言的 SQL 与参数。
func TestPredicatePerDialect(t *testing.T) {
	cursor, err := NewCursor(time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), int64(42))
	if err != nil {
		t.Fatalf("构造游标失败: %v", err)
	}
	keyset := Keyset{Sort: "m.created_at", ID: "m.id"}
	idCursor, err := NewCursor(int64(42), int64(42))
	if err != nil {
		t.Fatalf("构造同列游标失败: %v", err)
	}
	tests := []struct {
		name     string
		dialect  Dialect
		cursor   Cursor
		keyset   Keyset
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "mysql row value | MySQL 行值",
			dialect:  MySQL,
			cursor:   cursor,
			keyset:   keyset,
			wantSQL:  "(`m`.`created_at`, `m`.`id`) > (?, ?)",
			wantArgs: []any{time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), int64(42)},
		},
		{
			name:     "postgres row value | PostgreSQL 行值",
			dialect:  PostgreSQL,
			cursor:   cursor,
			keyset:   keyset,
			wantSQL:  `("m"."created_at", "m"."id") > (?, ?)`,
			wantArgs: []any{time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), int64(42)},
		},
		{
			name:     "sqlite row value | SQLite 行值",
			dialect:  SQLite,
			cursor:   cursor,
			keyset:   keyset,
			wantSQL:  `("m"."created_at", "m"."id") > (?, ?)`,
			wantArgs: []any{time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), int64(42)},
		},
		{
			name:     "dm OR expansion | 达梦 OR 展开",
			dialect:  DM,
			cursor:   cursor,
			keyset:   keyset,
			wantSQL:  `("m"."created_at" > ? OR ("m"."created_at" = ? AND "m"."id" > ?))`,
			wantArgs: []any{time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), int64(42)},
		},
		{
			name:     "same column desc | 同列降序",
			dialect:  PostgreSQL,
			cursor:   idCursor,
			keyset:   Keyset{Sort: "id", ID: "id", Desc: true},
			wantSQL:  `"id" < ?`,
			wantArgs: []any{int64(42)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := tc.keyset.Predicate(tc.dialect, tc.cursor)
			if err != nil {
				t.Fatalf("Predicate 失败: %v", err)
			}
			if sql != tc.wantSQL {
				t.Errorf("SQL = %q，期望 %q", sql, tc.wantSQL)
			}
			if len(args) != len(tc.wantArgs) {
				t.Fatalf("参数数 = %d，期望 %d：%v", len(args), len(tc.wantArgs), args)
			}
			for i := range args {
				if args[i] != tc.wantArgs[i] {
					t.Errorf("参数[%d] = %#v，期望 %#v", i, args[i], tc.wantArgs[i])
				}
			}
		})
	}
}

// TestKeysetRejectsUnsafeIdentifiers keeps a caller from smuggling SQL through a column name. | TestKeysetRejectsUnsafeIdentifiers
// 防止调用方通过列名夹带 SQL。
func TestKeysetRejectsUnsafeIdentifiers(t *testing.T) {
	cursor, err := NewCursor(int64(1), int64(1))
	if err != nil {
		t.Fatalf("构造游标失败: %v", err)
	}
	for _, keyset := range []Keyset{
		{Sort: "id; DROP TABLE users", ID: "id"},
		{Sort: "m.id OR 1=1", ID: "m.id"},
		{Sort: "m.id", ID: "id)"},
	} {
		if _, _, err := keyset.Predicate(PostgreSQL, cursor); !errors.Is(err, ErrIdentifier) {
			t.Errorf("Predicate(%+v) 错误 = %v，期望 ErrIdentifier", keyset, err)
		}
	}
}

// TestKeysetOrder pins the deterministic ORDER BY shared by every page. | TestKeysetOrder 固化每页共用的
// 确定性 ORDER BY。
func TestKeysetOrder(t *testing.T) {
	tests := []struct {
		dialect Dialect
		keyset  Keyset
		want    string
	}{
		{MySQL, Keyset{Sort: "created_at", ID: "id"}, "`created_at` ASC, `id` ASC"},
		{PostgreSQL, Keyset{Sort: "created_at", ID: "id", Desc: true}, `"created_at" DESC, "id" DESC`},
		{DM, Keyset{Sort: "id", ID: "id"}, `"id" ASC`},
	}
	for _, tc := range tests {
		if got, err := tc.keyset.Order(tc.dialect); err != nil || got != tc.want {
			t.Errorf("Order(%s) = %q/%v，期望 %q", tc.dialect, got, err, tc.want)
		}
	}
}

// TestCursorRoundTrip covers every supported value kind and rejects corrupted input. | TestCursorRoundTrip
// 覆盖全部支持的取值类型并拒绝损坏输入。
func TestCursorRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		value any
		id    any
	}{
		{"string | 字符串", "alpha", "user-1"},
		{"signed integer | 有符号整数", int64(42), int64(7)},
		{"unsigned integer | 无符号整数", uint64(42), uint64(7)},
		{"timestamp | 时间戳", time.Date(2026, 10, 26, 9, 0, 0, 123, time.UTC), "audit-1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cursor, err := NewCursor(tc.value, tc.id)
			if err != nil {
				t.Fatalf("NewCursor 失败: %v", err)
			}
			encoded, err := cursor.Encode()
			if err != nil {
				t.Fatalf("Encode 失败: %v", err)
			}
			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode 失败: %v", err)
			}
			gotValue, err := decoded.SortValue()
			if err != nil {
				t.Fatalf("SortValue 失败: %v", err)
			}
			gotID, err := decoded.TieBreaker()
			if err != nil {
				t.Fatalf("TieBreaker 失败: %v", err)
			}
			if gotValue != tc.value || gotID != tc.id {
				t.Errorf("往返 = %#v/%#v，期望 %#v/%#v", gotValue, gotID, tc.value, tc.id)
			}
		})
	}
	if _, err := NewCursor(struct{}{}, 1); !errors.Is(err, ErrCursor) {
		t.Errorf("不支持的类型错误 = %v，期望 ErrCursor", err)
	}
	for _, raw := range []string{"", "not-base64!", strings.Repeat("a", maxCursorBytes+1), "e30"} {
		if _, err := Decode(raw); !errors.Is(err, ErrCursor) {
			t.Errorf("Decode(%q) 错误 = %v，期望 ErrCursor", raw, err)
		}
	}
}

// TestKeysetPageSQLite walks forward and backward through a real sqlite table. | TestKeysetPageSQLite
// 在真实 SQLite 表上向前与向后行走。
func TestKeysetPageSQLite(t *testing.T) {
	db := openSQLite(t, 5)
	keyset := Keyset{Sort: "id", ID: "id"}
	cursorOf := func(row testRow) (Cursor, error) { return NewCursor(row.ID, row.ID) }

	first, err := KeysetPage[testRow](context.Background(), db.Model(&testRow{}), keyset, Request{Size: 2}, cursorOf)
	if err != nil {
		t.Fatalf("首页失败: %v", err)
	}
	if len(first.Items) != 2 || first.Items[0].ID != 1 || first.Items[1].ID != 2 {
		t.Fatalf("首页 = %+v，期望 id 1,2", first.Items)
	}
	if !first.HasNext || first.HasPrev || first.After == "" || first.Before != "" {
		t.Errorf("首页游标状态错误: %+v", first)
	}

	second, err := KeysetPage[testRow](context.Background(), db.Model(&testRow{}), keyset,
		Request{After: first.After, Size: 2}, cursorOf)
	if err != nil {
		t.Fatalf("第二页失败: %v", err)
	}
	if len(second.Items) != 2 || second.Items[0].ID != 3 || second.Items[1].ID != 4 {
		t.Fatalf("第二页 = %+v，期望 id 3,4", second.Items)
	}
	if !second.HasPrev || !second.HasNext || second.Before == "" || second.After == "" {
		t.Errorf("第二页游标状态错误: %+v", second)
	}

	back, err := KeysetPage[testRow](context.Background(), db.Model(&testRow{}), keyset,
		Request{Before: second.Before, Size: 2}, cursorOf)
	if err != nil {
		t.Fatalf("回退页失败: %v", err)
	}
	if len(back.Items) != 2 || back.Items[0].ID != 1 || back.Items[1].ID != 2 {
		t.Fatalf("回退页 = %+v，期望 id 1,2", back.Items)
	}
	if !back.HasNext || back.HasPrev {
		t.Errorf("回退页状态错误: %+v", back)
	}
}

// TestKeysetPageDescendingSQLite pins the reversed comparison and natural result order. | TestKeysetPageDescendingSQLite
// 固化反向比较与自然结果顺序。
func TestKeysetPageDescendingSQLite(t *testing.T) {
	db := openSQLite(t, 4)
	keyset := Keyset{Sort: "id", ID: "id", Desc: true}
	cursorOf := func(row testRow) (Cursor, error) { return NewCursor(row.ID, row.ID) }

	first, err := KeysetPage[testRow](context.Background(), db.Model(&testRow{}), keyset, Request{Size: 3}, cursorOf)
	if err != nil {
		t.Fatalf("首页失败: %v", err)
	}
	if len(first.Items) != 3 || first.Items[0].ID != 4 || first.Items[2].ID != 2 {
		t.Fatalf("降序首页 = %+v，期望 id 4,3,2", first.Items)
	}
	next, err := KeysetPage[testRow](context.Background(), db.Model(&testRow{}), keyset,
		Request{After: first.After, Size: 3}, cursorOf)
	if err != nil {
		t.Fatalf("下一页失败: %v", err)
	}
	if len(next.Items) != 1 || next.Items[0].ID != 1 || next.HasNext {
		t.Fatalf("降序末页 = %+v (hasNext=%v)，期望 id 1 且无下一页", next.Items, next.HasNext)
	}
}

// TestKeysetPageRejectsUnknownDialect pins the failure-closed strategy. | TestKeysetPageRejectsUnknownDialect
// 固化失败关闭的策略。
func TestKeysetPageRejectsUnknownDialect(t *testing.T) {
	db := &gorm.DB{Config: &gorm.Config{Dialector: namedDialector{name: "oracle"}}}
	_, err := KeysetPage[testRow](context.Background(), db, Keyset{Sort: "id", ID: "id"}, Request{}, func(row testRow) (Cursor, error) {
		return Cursor{}, nil
	})
	if !errors.Is(err, ErrDialect) {
		t.Fatalf("未知方言错误 = %v，期望 ErrDialect", err)
	}
}
