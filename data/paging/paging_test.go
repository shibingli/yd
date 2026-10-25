// This file tests the shared request, offset page and pager window. | 本文件测试共用的请求、偏移分页与分页窗口。

package paging

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// testRow is the probe table of the offset and keyset integration tests. | testRow 是偏移与键集集成测试的探针表。
type testRow struct {
	// ID is the primary key and the keyset sort column. | ID 是主键与键集排序列。
	ID int64 `gorm:"primaryKey"`
	// Name is an ordinary payload column. | Name 是普通载荷列。
	Name string
}

// TableName pins the probe table name. | TableName 固定探针表名。
func (testRow) TableName() string { return "paging_test_rows" }

// openSQLite opens an in-memory database with the probe table populated with ids 1..count. | openSQLite
// 打开一个内存数据库，并写入 id 从 1 到 count 的探针记录。
func openSQLite(t *testing.T, count int) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开 sqlite 失败: %v", err)
	}
	if err := db.Migrator().DropTable(&testRow{}); err != nil {
		t.Fatalf("清理探针表失败: %v", err)
	}
	if err := db.AutoMigrate(&testRow{}); err != nil {
		t.Fatalf("迁移探针表失败: %v", err)
	}
	for i := 1; i <= count; i++ {
		if err := db.Create(&testRow{ID: int64(i), Name: "row"}).Error; err != nil {
			t.Fatalf("写入探针记录失败: %v", err)
		}
	}
	return db
}

// TestRequestNormalize pins the shared range and cursor exclusivity. | TestRequestNormalize 固化共用区间与
// 游标互斥规则。
func TestRequestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   Request
		want Request
	}{
		{"defaults | 缺省值", Request{}, Request{Page: 1, Size: DefaultSize}},
		{"clamps both ends | 两端收敛", Request{Page: -2, Size: MaxSize + 1}, Request{Page: 1, Size: MaxSize}},
		{"keeps explicit values | 保留显式值", Request{Page: 3, Size: 12}, Request{Page: 3, Size: 12}},
		{"after wins over before | after 优先", Request{After: " a ", Before: "b"}, Request{After: "a", Page: 1, Size: DefaultSize}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Normalize(); got != tc.want {
				t.Errorf("Normalize() = %+v | Normalize() 得到 %+v，期望 %+v", got, got, tc.want)
			}
		})
	}
	if !(Request{After: "x"}).CursorMode() || (Request{Page: 1}).CursorMode() {
		t.Error("CursorMode 判定错误 | CursorMode is wrong")
	}
}

// TestOffsetPageSQLite reads the second page, then confirms the last page has no next. | TestOffsetPageSQLite
// 读取第二页，再确认末页没有下一页。
func TestOffsetPageSQLite(t *testing.T) {
	db := openSQLite(t, 5)
	query := db.Model(&testRow{}).Order("id ASC")

	page, err := OffsetPage[testRow](context.Background(), query, Request{Page: 2, Size: 2, WithTotal: true})
	if err != nil {
		t.Fatalf("OffsetPage 失败: %v", err)
	}
	if page.Total != 5 || !page.HasTotal {
		t.Errorf("Total = %d (has=%v)，期望 5/true", page.Total, page.HasTotal)
	}
	if len(page.Items) != 2 || page.Items[0].ID != 3 || page.Items[1].ID != 4 {
		t.Errorf("第二页 = %+v，期望 id 3,4", page.Items)
	}
	if !page.HasPrev || !page.HasNext {
		t.Errorf("HasPrev/HasNext = %v/%v，期望 true/true", page.HasPrev, page.HasNext)
	}

	last, err := OffsetPage[testRow](context.Background(), query, Request{Page: 3, Size: 2, WithTotal: true})
	if err != nil {
		t.Fatalf("OffsetPage 末页失败: %v", err)
	}
	if len(last.Items) != 1 || last.Items[0].ID != 5 || last.HasNext {
		t.Errorf("末页 = %+v (hasNext=%v)，期望 id 5 且无下一页", last.Items, last.HasNext)
	}
}

// TestOffsetPageWithoutTotalStillDetectsNext pins the extra-row probe: no COUNT is needed to know whether
// another page exists. | TestOffsetPageWithoutTotalStillDetectsNext 固化多取一行探测：不执行 COUNT 也能
// 判断是否还有下一页。
func TestOffsetPageWithoutTotalStillDetectsNext(t *testing.T) {
	db := openSQLite(t, 3)
	page, err := OffsetPage[testRow](context.Background(), db.Model(&testRow{}).Order("id ASC"), Request{Page: 1, Size: 2})
	if err != nil {
		t.Fatalf("OffsetPage 失败: %v", err)
	}
	if page.HasTotal || page.Total != -1 {
		t.Errorf("未请求计数时 Total = %d (has=%v)，期望 -1/false", page.Total, page.HasTotal)
	}
	if len(page.Items) != 2 || !page.HasNext {
		t.Errorf("首页 = %+v (hasNext=%v)，期望 2 行且有下一页", page.Items, page.HasNext)
	}
}

// TestOffsetPageRequiresQuery pins the nil-handle guard. | TestOffsetPageRequiresQuery 固化空句柄保护。
func TestOffsetPageRequiresQuery(t *testing.T) {
	if _, err := OffsetPage[testRow](context.Background(), nil, Request{}); !errors.Is(err, ErrQuery) {
		t.Fatalf("空 query 错误 = %v，期望 ErrQuery", err)
	}
}

// TestPageCountAndBounds pins the page arithmetic shared by every renderer. | TestPageCountAndBounds 固化
// 所有渲染方共用的页码算术。
func TestPageCountAndBounds(t *testing.T) {
	if got := PageCount(0, 20); got != 1 {
		t.Errorf("PageCount(0) = %d，期望 1", got)
	}
	if got := PageCount(41, 20); got != 3 {
		t.Errorf("PageCount(41,20) = %d，期望 3", got)
	}
	current, start, end := Bounds(41, 9, 20)
	if current != 3 || start != 40 || end != 41 {
		t.Errorf("Bounds = %d/%d/%d，期望 3/40/41", current, start, end)
	}
}

// TestWindow pins the compact page window with ellipses. | TestWindow 固化带省略号的紧凑分页窗口。
func TestWindow(t *testing.T) {
	if got := Window(1, 1, 1); got != nil {
		t.Errorf("单页窗口 = %+v，期望 nil", got)
	}
	got := Window(5, 10, 1)
	want := []Link{
		{Number: 1}, {Ellipsis: true},
		{Number: 4}, {Number: 5, Current: true}, {Number: 6},
		{Ellipsis: true}, {Number: 10},
	}
	if len(got) != len(want) {
		t.Fatalf("窗口长度 = %d，期望 %d：%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("窗口[%d] = %+v，期望 %+v", i, got[i], want[i])
		}
	}
}
