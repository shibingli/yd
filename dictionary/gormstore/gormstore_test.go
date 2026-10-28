package gormstore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/shibingli/yd/dictionary"
)

// newTestStore opens a migrated SQLite store in a temporary directory. | newTestStore 在临时目录打开一个已迁移的
// SQLite 存储。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "dictionary.db")) + "?_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}

// seedType inserts one type. | seedType 插入一个类型。
func seedType(t *testing.T, store *Store, tenantID, id, code string) dictionary.Type {
	t.Helper()
	now := time.Now().UTC()
	dictionaryType := dictionary.Type{
		CreatedAt: now,
		UpdatedAt: now,
		ID:        id,
		TenantID:  tenantID,
		Code:      code,
		Name:      code,
		Status:    dictionary.StatusEnabled,
	}
	if err := store.CreateType(context.Background(), dictionaryType); err != nil {
		t.Fatalf("CreateType(%s): %v", code, err)
	}
	return dictionaryType
}

// seedItem inserts one item. | seedItem 插入一个字典项。
func seedItem(t *testing.T, store *Store, tenantID, typeID, id, value string, sort int) dictionary.Item {
	t.Helper()
	now := time.Now().UTC()
	item := dictionary.Item{
		CreatedAt: now,
		UpdatedAt: now,
		ID:        id,
		TenantID:  tenantID,
		TypeID:    typeID,
		Value:     value,
		Label:     value,
		Status:    dictionary.StatusEnabled,
		Sort:      sort,
	}
	if err := store.CreateItem(context.Background(), item, dictionary.DefaultOptions().MaxItems); err != nil {
		t.Fatalf("CreateItem(%s): %v", value, err)
	}
	return item
}

// TestMigrateIsIdempotent proves the migration can run repeatedly. | TestMigrateIsIdempotent 证明迁移可重复执行。
func TestMigrateIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	if err := Migrate(context.Background(), store.DB()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if !store.DB().Migrator().HasTable(&dictionary.Type{}) || !store.DB().Migrator().HasTable(&dictionary.Item{}) {
		t.Fatal("dictionary tables were not created")
	}
}

// TestTenantIsolationAndLookups checks that every lookup is tenant-scoped. | TestTenantIsolationAndLookups
// 校验每次查询都带租户条件。
func TestTenantIsolationAndLookups(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedType(t, store, "tn_a", "dict_a", "gender")
	seedType(t, store, "tn_b", "dict_b", "gender")
	if _, err := store.GetType(ctx, "tn_a", "dict_b"); !errors.Is(err, dictionary.ErrNotFound) {
		t.Fatalf("cross-tenant GetType error = %v, want ErrNotFound", err)
	}
	if got, err := store.GetTypeByCode(ctx, "tn_b", "gender"); err != nil || got.ID != "dict_b" {
		t.Fatalf("GetTypeByCode = %+v, %v", got, err)
	}
	if _, err := store.GetTypeByCode(ctx, "tn_c", "gender"); !errors.Is(err, dictionary.ErrNotFound) {
		t.Fatalf("missing tenant error = %v, want ErrNotFound", err)
	}
}

// TestDuplicateCodeAndValue checks the unique indexes. | TestDuplicateCodeAndValue 校验唯一索引。
func TestDuplicateCodeAndValue(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	dictionaryType := seedType(t, store, "tn_a", "dict_a", "gender")
	if err := store.CreateType(ctx, dictionary.Type{ID: "dict_dup", TenantID: "tn_a", Code: "gender", Name: "dup", Status: dictionary.StatusEnabled}); !errors.Is(err, dictionary.ErrCodeTaken) {
		t.Fatalf("duplicate code error = %v, want ErrCodeTaken", err)
	}
	seedItem(t, store, "tn_a", dictionaryType.ID, "item_a", "male", 10)
	err := store.CreateItem(ctx, dictionary.Item{ID: "item_dup", TenantID: "tn_a", TypeID: dictionaryType.ID, Value: "male", Label: "dup", Status: dictionary.StatusEnabled}, dictionary.DefaultOptions().MaxItems)
	if !errors.Is(err, dictionary.ErrValueTaken) {
		t.Fatalf("duplicate value error = %v, want ErrValueTaken", err)
	}
}

// TestResolveFiltersAndOrders checks the single-query public resolution. | TestResolveFiltersAndOrders
// 校验单条 SQL 的公共解析。
func TestResolveFiltersAndOrders(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	gender := seedType(t, store, "tn_a", "dict_gender", "gender")
	hidden := seedType(t, store, "tn_a", "dict_hidden", "hidden")
	if err := store.UpdateType(ctx, dictionary.Type{
		CreatedAt: hidden.CreatedAt, UpdatedAt: time.Now().UTC(), ID: hidden.ID, TenantID: hidden.TenantID,
		Code: hidden.Code, Name: hidden.Name, Status: dictionary.StatusDisabled,
	}); err != nil {
		t.Fatalf("disable type: %v", err)
	}
	seedItem(t, store, "tn_a", gender.ID, "item_male", "male", 20)
	seedItem(t, store, "tn_a", gender.ID, "item_female", "female", 10)
	disabledItem := seedItem(t, store, "tn_a", gender.ID, "item_unknown", "unknown", 30)
	if err := store.SetItemStatus(ctx, "tn_a", gender.ID, disabledItem.ID, dictionary.StatusDisabled, time.Now().UTC()); err != nil {
		t.Fatalf("disable item: %v", err)
	}
	got, err := store.Resolve(ctx, "tn_a", []string{"gender", "hidden", "missing"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	snapshot, ok := got["gender"]
	if !ok {
		t.Fatal("gender is missing from the resolved map")
	}
	if len(snapshot.Items) != 2 {
		t.Fatalf("resolved %d items, want 2", len(snapshot.Items))
	}
	if snapshot.Items[0].Value != "female" || snapshot.Items[1].Value != "male" {
		t.Fatalf("items are not ordered by sort: %+v", snapshot.Items)
	}
	if _, ok := got["hidden"]; ok {
		t.Fatal("a disabled type leaked into the resolved map")
	}
	if _, err := store.Resolve(ctx, "tn_other", []string{"gender"}); err != nil {
		t.Fatalf("cross-tenant Resolve: %v", err)
	}
	if loaded, _ := store.Resolve(ctx, "tn_other", []string{"gender"}); len(loaded) != 0 {
		t.Fatal("cross-tenant Resolve returned rows")
	}
}

// TestDefaultExclusivityAndStatus checks the transactional default rule. | TestDefaultExclusivityAndStatus
// 校验事务内的默认项规则。
func TestDefaultExclusivityAndStatus(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	dictionaryType := seedType(t, store, "tn_a", "dict_a", "priority")
	now := time.Now().UTC()
	first := dictionary.Item{ID: "item_low", TenantID: "tn_a", TypeID: dictionaryType.ID, Value: "low", Label: "低", Status: dictionary.StatusEnabled, IsDefault: true, Sort: 10, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateItem(ctx, first, dictionary.DefaultOptions().MaxItems); err != nil {
		t.Fatalf("CreateItem first: %v", err)
	}
	second := dictionary.Item{ID: "item_high", TenantID: "tn_a", TypeID: dictionaryType.ID, Value: "high", Label: "高", Status: dictionary.StatusEnabled, IsDefault: true, Sort: 20, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateItem(ctx, second, dictionary.DefaultOptions().MaxItems); err != nil {
		t.Fatalf("CreateItem second: %v", err)
	}
	gotFirst, _ := store.GetItem(ctx, "tn_a", dictionaryType.ID, first.ID)
	if gotFirst.IsDefault {
		t.Fatal("the first default flag survived the second default")
	}
	gotSecond, _ := store.GetItem(ctx, "tn_a", dictionaryType.ID, second.ID)
	if !gotSecond.IsDefault {
		t.Fatal("the second item is not the default")
	}
	if err := store.SetItemStatus(ctx, "tn_a", dictionaryType.ID, second.ID, dictionary.StatusDisabled, time.Now().UTC()); err != nil {
		t.Fatalf("SetItemStatus: %v", err)
	}
	gotSecond, _ = store.GetItem(ctx, "tn_a", dictionaryType.ID, second.ID)
	if gotSecond.IsDefault {
		t.Fatal("a disabled item kept the default flag")
	}
}

// TestCreateItemEnforcesTheCeilingInsideTheTransaction checks the atomic item limit. |
// TestCreateItemEnforcesTheCeilingInsideTheTransaction 校验事务内的原子字典项上限。
func TestCreateItemEnforcesTheCeilingInsideTheTransaction(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	dictionaryType := seedType(t, store, "tn_a", "dict_a", "priority")
	now := time.Now().UTC()
	first := dictionary.Item{ID: "item_low", TenantID: "tn_a", TypeID: dictionaryType.ID, Value: "low", Label: "低", Status: dictionary.StatusEnabled, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateItem(ctx, first, 1); err != nil {
		t.Fatalf("CreateItem first: %v", err)
	}
	second := dictionary.Item{ID: "item_high", TenantID: "tn_a", TypeID: dictionaryType.ID, Value: "high", Label: "高", Status: dictionary.StatusEnabled, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateItem(ctx, second, 1); !errors.Is(err, dictionary.ErrItemLimit) {
		t.Fatalf("second CreateItem error = %v, want ErrItemLimit", err)
	}
	var total int64
	if err := store.DB().Model(&dictionary.Item{}).Where("tenant_id = ? AND type_id = ?", "tn_a", dictionaryType.ID).Count(&total).Error; err != nil {
		t.Fatalf("count items: %v", err)
	}
	if total != 1 {
		t.Fatalf("item total = %d, want 1", total)
	}
}

// TestDeleteTypeCascades checks the explicit cascade. | TestDeleteTypeCascades 校验显式级联删除。
func TestDeleteTypeCascades(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	dictionaryType := seedType(t, store, "tn_a", "dict_a", "gender")
	seedItem(t, store, "tn_a", dictionaryType.ID, "item_a", "male", 10)
	seedItem(t, store, "tn_a", dictionaryType.ID, "item_b", "female", 20)
	if err := store.DeleteType(ctx, "tn_a", dictionaryType.ID); err != nil {
		t.Fatalf("DeleteType: %v", err)
	}
	if _, err := store.GetType(ctx, "tn_a", dictionaryType.ID); !errors.Is(err, dictionary.ErrNotFound) {
		t.Fatalf("deleted type error = %v, want ErrNotFound", err)
	}
	var count int64
	if err := store.DB().Model(&dictionary.Item{}).Where("tenant_id = ? AND type_id = ?", "tn_a", dictionaryType.ID).Count(&count).Error; err != nil {
		t.Fatalf("count items: %v", err)
	}
	if count != 0 {
		t.Fatalf("%d items survived the type deletion", count)
	}
}

// TestListTypesSearchesAndCounts checks the admin list projection. | TestListTypesSearchesAndCounts
// 校验管理列表的检索与计数投影。
func TestListTypesSearchesAndCounts(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	gender := seedType(t, store, "tn_a", "dict_gender", "gender")
	seedType(t, store, "tn_a", "dict_priority", "priority")
	seedItem(t, store, "tn_a", gender.ID, "item_male", "male", 10)
	seedItem(t, store, "tn_a", gender.ID, "item_female", "female", 20)
	items, total, err := store.ListTypes(ctx, "tn_a", dictionary.Filter{Query: "gen", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListTypes: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Code != "gender" {
		t.Fatalf("search returned total=%d items=%+v", total, items)
	}
	if items[0].ItemCount != 2 {
		t.Fatalf("item count = %d, want 2", items[0].ItemCount)
	}
	itemPage, itemTotal, err := store.ListItems(ctx, "tn_a", gender.ID, dictionary.ItemFilter{Query: "fem", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if itemTotal != 1 || len(itemPage) != 1 || itemPage[0].Value != "female" {
		t.Fatalf("item search returned total=%d items=%+v", itemTotal, itemPage)
	}
	itemPage, itemTotal, err = store.ListItems(ctx, "tn_a", gender.ID, dictionary.ItemFilter{Status: dictionary.StatusDisabled, Page: 1, PageSize: 10})
	if err != nil || itemTotal != 0 || len(itemPage) != 0 {
		t.Fatalf("disabled item list = total=%d items=%+v err=%v", itemTotal, itemPage, err)
	}
}

// TestEnsureBuiltinsIdempotentAndPreservesEdits checks the startup seed. | TestEnsureBuiltinsIdempotentAndPreservesEdits
// 校验启动种子。
func TestEnsureBuiltinsIdempotentAndPreservesEdits(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	inserted, err := store.EnsureBuiltins(ctx, "tn_a")
	if err != nil {
		t.Fatalf("EnsureBuiltins: %v", err)
	}
	if inserted != len(dictionary.BuiltinCatalog()) {
		t.Fatalf("inserted %d types, want %d", inserted, len(dictionary.BuiltinCatalog()))
	}
	inserted, err = store.EnsureBuiltins(ctx, "tn_a")
	if err != nil || inserted != 0 {
		t.Fatalf("second EnsureBuiltins inserted=%d err=%v, want 0 and nil", inserted, err)
	}
	gender, err := store.GetTypeByCode(ctx, "tn_a", "gender")
	if err != nil {
		t.Fatalf("GetTypeByCode: %v", err)
	}
	gender.Name = "性别（本地化）"
	gender.UpdatedAt = time.Now().UTC()
	if err := store.UpdateType(ctx, gender); err != nil {
		t.Fatalf("UpdateType: %v", err)
	}
	items, _, err := store.ListItems(ctx, "tn_a", gender.ID, dictionary.ItemFilter{Page: 1, PageSize: 10})
	if err != nil || len(items) == 0 {
		t.Fatalf("ListItems = %+v, %v", items, err)
	}
	if err := store.DeleteItem(ctx, "tn_a", gender.ID, items[0].ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}
	if _, err := store.EnsureBuiltins(ctx, "tn_a"); err != nil {
		t.Fatalf("third EnsureBuiltins: %v", err)
	}
	gender, _ = store.GetTypeByCode(ctx, "tn_a", "gender")
	if gender.Name != "性别（本地化）" {
		t.Fatalf("EnsureBuiltins overwrote the localised name: %q", gender.Name)
	}
	items, _, _ = store.ListItems(ctx, "tn_a", gender.ID, dictionary.ItemFilter{Page: 1, PageSize: 10})
	if len(items) != 2 {
		t.Fatalf("EnsureBuiltins resurrected a deleted item: %d items", len(items))
	}
}
