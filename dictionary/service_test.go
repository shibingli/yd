package dictionary

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// memoryStore is an in-memory Store used by the service tests. | memoryStore 是服务测试使用的内存存储。
type memoryStore struct {
	types        map[string]Type
	items        map[string]Item
	resolveCalls int
	mu           sync.Mutex
}

// newMemoryStore returns an empty store. | newMemoryStore 返回一个空存储。
func newMemoryStore() *memoryStore {
	return &memoryStore{types: map[string]Type{}, items: map[string]Item{}}
}

// CreateType inserts a type. | CreateType 插入一个类型。
func (m *memoryStore) CreateType(_ context.Context, t Type) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, current := range m.types {
		if current.TenantID == t.TenantID && current.Code == t.Code {
			return ErrCodeTaken
		}
	}
	m.types[t.ID] = t
	return nil
}

// UpdateType replaces a type. | UpdateType 覆盖一个类型。
func (m *memoryStore) UpdateType(_ context.Context, t Type) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.types[t.ID]
	if !ok || current.TenantID != t.TenantID {
		return ErrNotFound
	}
	m.types[t.ID] = t
	return nil
}

// GetType reads a type. | GetType 读取一个类型。
func (m *memoryStore) GetType(_ context.Context, tenantID, id string) (Type, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.types[id]
	if !ok || current.TenantID != tenantID {
		return Type{}, ErrNotFound
	}
	return current, nil
}

// GetTypeByCode reads a type by code. | GetTypeByCode 按编码读取一个类型。
func (m *memoryStore) GetTypeByCode(_ context.Context, tenantID, code string) (Type, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, current := range m.types {
		if current.TenantID == tenantID && current.Code == code {
			return current, nil
		}
	}
	return Type{}, ErrNotFound
}

// ListTypes returns one page. | ListTypes 返回一页。
func (m *memoryStore) ListTypes(_ context.Context, tenantID string, filter Filter) ([]Type, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Type
	for _, current := range m.types {
		if current.TenantID != tenantID {
			continue
		}
		if filter.Status != "" && current.Status != filter.Status {
			continue
		}
		if filter.Query != "" && !strings.Contains(strings.ToLower(current.Code+" "+current.Name+" "+current.NameEN), strings.ToLower(filter.Query)) {
			continue
		}
		out = append(out, current)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sort < out[j].Sort })
	return out, len(out), nil
}

// DeleteType deletes a type and its items. | DeleteType 删除类型及其字典项。
func (m *memoryStore) DeleteType(_ context.Context, tenantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.types[id]
	if !ok || current.TenantID != tenantID {
		return ErrNotFound
	}
	delete(m.types, id)
	for itemID, item := range m.items {
		if item.TenantID == tenantID && item.TypeID == id {
			delete(m.items, itemID)
		}
	}
	return nil
}

// CreateItem inserts an item, enforces the ceiling and replaces the default. | CreateItem 插入字典项、
// 执行数量上限并替换默认项。
func (m *memoryStore) CreateItem(_ context.Context, item Item, maxItems int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if maxItems > 0 {
		total := 0
		for _, current := range m.items {
			if current.TenantID == item.TenantID && current.TypeID == item.TypeID {
				total++
			}
		}
		if total >= maxItems {
			return ErrItemLimit
		}
	}
	for _, current := range m.items {
		if current.TenantID == item.TenantID && current.TypeID == item.TypeID && current.Value == item.Value {
			return ErrValueTaken
		}
	}
	if item.IsDefault {
		for id, current := range m.items {
			if current.TenantID == item.TenantID && current.TypeID == item.TypeID {
				current.IsDefault = false
				m.items[id] = current
			}
		}
	}
	m.items[item.ID] = item
	return nil
}

// UpdateItem replaces an item. | UpdateItem 覆盖字典项。
func (m *memoryStore) UpdateItem(_ context.Context, item Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.items[item.ID]
	if !ok || current.TenantID != item.TenantID || current.TypeID != item.TypeID {
		return ErrNotFound
	}
	if item.IsDefault {
		for id, other := range m.items {
			if other.TenantID == item.TenantID && other.TypeID == item.TypeID {
				other.IsDefault = false
				m.items[id] = other
			}
		}
	}
	m.items[item.ID] = item
	return nil
}

// GetItem reads an item. | GetItem 读取字典项。
func (m *memoryStore) GetItem(_ context.Context, tenantID, typeID, id string) (Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.items[id]
	if !ok || current.TenantID != tenantID || current.TypeID != typeID {
		return Item{}, ErrNotFound
	}
	return current, nil
}

// GetItemByValue reads an item by value. | GetItemByValue 按值读取字典项。
func (m *memoryStore) GetItemByValue(_ context.Context, tenantID, typeID, value string) (Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, current := range m.items {
		if current.TenantID == tenantID && current.TypeID == typeID && current.Value == value {
			return current, nil
		}
	}
	return Item{}, ErrNotFound
}

// ListItems returns one page. | ListItems 返回一页。
func (m *memoryStore) ListItems(_ context.Context, tenantID, typeID string, filter ItemFilter) ([]Item, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Item
	for _, current := range m.items {
		if current.TenantID != tenantID || current.TypeID != typeID {
			continue
		}
		if filter.Status != "" && current.Status != filter.Status {
			continue
		}
		out = append(out, current)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sort < out[j].Sort })
	return out, len(out), nil
}

// DeleteItem deletes an item. | DeleteItem 删除字典项。
func (m *memoryStore) DeleteItem(_ context.Context, tenantID, typeID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.items[id]
	if !ok || current.TenantID != tenantID || current.TypeID != typeID {
		return ErrNotFound
	}
	delete(m.items, id)
	return nil
}

// SetItemStatus changes a status. | SetItemStatus 修改状态。
func (m *memoryStore) SetItemStatus(_ context.Context, tenantID, typeID, itemID string, status Status, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.items[itemID]
	if !ok || current.TenantID != tenantID || current.TypeID != typeID {
		return ErrNotFound
	}
	current.Status = status
	current.UpdatedAt = at
	if status == StatusDisabled {
		current.IsDefault = false
	}
	m.items[itemID] = current
	return nil
}

// Resolve returns enabled snapshots. | Resolve 返回启用快照。
func (m *memoryStore) Resolve(_ context.Context, tenantID string, codes []string) (map[string]Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolveCalls++
	wanted := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		wanted[code] = struct{}{}
	}
	out := make(map[string]Snapshot, len(codes))
	for _, current := range m.types {
		if current.TenantID != tenantID || current.Status != StatusEnabled {
			continue
		}
		if _, ok := wanted[current.Code]; !ok {
			continue
		}
		snapshot := Snapshot{Scope: ScopeOf(tenantID), Code: current.Code, Name: current.Name, NameEN: current.NameEN, Description: current.Description, Items: []SnapshotItem{}}
		var items []Item
		for _, item := range m.items {
			if item.TenantID == tenantID && item.TypeID == current.ID && item.Status == StatusEnabled {
				items = append(items, item)
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Sort < items[j].Sort })
		for _, item := range items {
			snapshot.Items = append(snapshot.Items, SnapshotItem{
				Value: item.Value, Label: item.Label, LabelEN: item.LabelEN,
				Extra: item.Extra, IsDefault: item.IsDefault, Sort: item.Sort,
			})
		}
		out[current.Code] = snapshot
	}
	return out, nil
}

// TestCreateTypeRejectsInvalidAndDuplicateInput checks the write rules. | TestCreateTypeRejectsInvalidAndDuplicateInput
// 校验写入规则。
func TestCreateTypeRejectsInvalidAndDuplicateInput(t *testing.T) {
	store := newMemoryStore()
	service, err := New(store, nil, nil, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	created, err := service.CreateType(context.Background(), "tn_a", TypeInput{Code: "Gender", Name: "性别"})
	if err != nil {
		t.Fatalf("CreateType: %v", err)
	}
	if created.Code != "gender" {
		t.Fatalf("code = %q, want gender", created.Code)
	}
	if _, err := service.CreateType(context.Background(), "tn_a", TypeInput{Code: "gender", Name: "重复"}); !errors.Is(err, ErrCodeTaken) {
		t.Fatalf("duplicate code error = %v, want ErrCodeTaken", err)
	}
	for _, input := range []TypeInput{
		{Code: "Bad-Code", Name: "x"},
		{Code: "ok", Name: ""},
		{Code: "ok", Name: "x", Status: "unknown"},
		{Code: "ok", Name: "x", Sort: -1},
	} {
		if _, err := service.CreateType(context.Background(), "tn_a", input); !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrInvalidExtra) {
			t.Fatalf("input %+v error = %v, want ErrInvalidInput", input, err)
		}
	}
}

// TestItemDefaultAndStatusRules checks default exclusivity and the disabled-item rules. |
// TestItemDefaultAndStatusRules 校验默认项互斥与停用规则。
func TestItemDefaultAndStatusRules(t *testing.T) {
	store := newMemoryStore()
	service, err := New(store, nil, nil, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	dictionaryType, err := service.CreateType(ctx, "tn_a", TypeInput{Code: "priority", Name: "优先级"})
	if err != nil {
		t.Fatalf("CreateType: %v", err)
	}
	first, err := service.CreateItem(ctx, "tn_a", dictionaryType.ID, ItemInput{Value: "low", Label: "低", Default: true, Sort: 10})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	second, err := service.CreateItem(ctx, "tn_a", dictionaryType.ID, ItemInput{Value: "high", Label: "高", Default: true, Sort: 20})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	gotFirst, _ := service.GetItem(ctx, "tn_a", dictionaryType.ID, first.ID)
	if gotFirst.IsDefault {
		t.Fatal("first item kept the default flag after a second default was created")
	}
	gotSecond, _ := service.GetItem(ctx, "tn_a", dictionaryType.ID, second.ID)
	if !gotSecond.IsDefault {
		t.Fatal("second item lost the default flag")
	}
	if _, err := service.CreateItem(ctx, "tn_a", dictionaryType.ID, ItemInput{Value: "high", Label: "重复"}); !errors.Is(err, ErrValueTaken) {
		t.Fatalf("duplicate value error = %v, want ErrValueTaken", err)
	}
	if _, err := service.CreateItem(ctx, "tn_a", dictionaryType.ID, ItemInput{Value: "off", Label: "停用", Default: true, Status: StatusDisabled}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("disabled default error = %v, want ErrInvalidInput", err)
	}
	updated, err := service.SetItemStatus(ctx, "tn_a", dictionaryType.ID, second.ID, StatusDisabled)
	if err != nil {
		t.Fatalf("SetItemStatus: %v", err)
	}
	if updated.IsDefault {
		t.Fatal("disabling the default item kept the default flag")
	}
	if _, err := service.CreateItem(ctx, "tn_a", dictionaryType.ID, ItemInput{Value: "bad", Label: "坏", Extra: "[1]"}); !errors.Is(err, ErrInvalidExtra) {
		t.Fatalf("bad extra error = %v, want ErrInvalidExtra", err)
	}
}

// TestResolveManyUncachedMergesSystemFallback checks the cache-disabled batch merge. |
// TestResolveManyUncachedMergesSystemFallback 校验关闭缓存时的批量合并。
func TestResolveManyUncachedMergesSystemFallback(t *testing.T) {
	store := newMemoryStore()
	service, err := New(store, nil, nil, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	systemShared, err := service.CreateType(ctx, SystemTenantID, TypeInput{Code: "shared", Name: "系统共享"})
	if err != nil {
		t.Fatalf("CreateType(system shared): %v", err)
	}
	if _, err := service.CreateItem(ctx, SystemTenantID, systemShared.ID, ItemInput{Value: "one", Label: "系统项"}); err != nil {
		t.Fatalf("CreateItem(system shared): %v", err)
	}
	systemOnly, err := service.CreateType(ctx, SystemTenantID, TypeInput{Code: "system_only", Name: "仅系统"})
	if err != nil {
		t.Fatalf("CreateType(system only): %v", err)
	}
	if _, err := service.CreateItem(ctx, SystemTenantID, systemOnly.ID, ItemInput{Value: "two", Label: "系统项二"}); err != nil {
		t.Fatalf("CreateItem(system only): %v", err)
	}
	tenantShared, err := service.CreateType(ctx, "tn_a", TypeInput{Code: "shared", Name: "租户覆盖"})
	if err != nil {
		t.Fatalf("CreateType(tenant shared): %v", err)
	}
	if _, err := service.CreateItem(ctx, "tn_a", tenantShared.ID, ItemInput{Value: "one", Label: "租户项"}); err != nil {
		t.Fatalf("CreateItem(tenant shared): %v", err)
	}

	batch, err := service.ResolveMany(ctx, "tn_a", []string{"shared", "system_only"})
	if err != nil {
		t.Fatalf("ResolveMany tenant: %v", err)
	}
	if batch["shared"].Scope != ScopeTenant || batch["shared"].Name != "租户覆盖" {
		t.Fatalf("tenant shared = %+v", batch["shared"])
	}
	if batch["system_only"].Scope != ScopeSystem || batch["system_only"].Name != "仅系统" {
		t.Fatalf("system only = %+v", batch["system_only"])
	}
	batch, err = service.ResolveMany(ctx, "tn_b", []string{"shared"})
	if err != nil {
		t.Fatalf("ResolveMany fallback: %v", err)
	}
	if batch["shared"].Scope != ScopeSystem || batch["shared"].Name != "系统共享" {
		t.Fatalf("system fallback = %+v", batch["shared"])
	}
}

// TestItemLimitAndTypeStatus checks the item ceiling and the public visibility of a disabled type. |
// TestItemLimitAndTypeStatus 校验字典项上限与停用类型的公共可见性。
func TestItemLimitAndTypeStatus(t *testing.T) {
	store := newMemoryStore()
	service, err := New(store, nil, nil, Options{MaxItems: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	dictionaryType, err := service.CreateType(ctx, "tn_a", TypeInput{Code: "priority", Name: "优先级"})
	if err != nil {
		t.Fatalf("CreateType: %v", err)
	}
	if _, err := service.CreateItem(ctx, "tn_a", dictionaryType.ID, ItemInput{Value: "low", Label: "低"}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	loaded, err := service.GetType(ctx, "tn_a", dictionaryType.ID)
	if err != nil || loaded.ItemCount != 1 {
		t.Fatalf("GetType item count = %d, %v; want 1", loaded.ItemCount, err)
	}
	if _, err := service.CreateItem(ctx, "tn_a", dictionaryType.ID, ItemInput{Value: "high", Label: "高"}); !errors.Is(err, ErrItemLimit) {
		t.Fatalf("item limit error = %v, want ErrItemLimit", err)
	}
	updated, err := service.SetTypeStatus(ctx, "tn_a", dictionaryType.ID, StatusDisabled)
	if err != nil {
		t.Fatalf("SetTypeStatus: %v", err)
	}
	if updated.ItemCount != 1 {
		t.Fatalf("updated item count = %d, want 1", updated.ItemCount)
	}
	if _, err := service.Resolve(ctx, "tn_a", "priority"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled type Resolve error = %v, want ErrNotFound", err)
	}
}

// TestListRejectsOversizedSearch checks the search keyword ceiling. | TestListRejectsOversizedSearch 校验搜索关键字上限。
func TestListRejectsOversizedSearch(t *testing.T) {
	store := newMemoryStore()
	service, err := New(store, nil, nil, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	dictionaryType, err := service.CreateType(ctx, "tn_a", TypeInput{Code: "gender", Name: "性别"})
	if err != nil {
		t.Fatalf("CreateType: %v", err)
	}
	long := strings.Repeat("x", DefaultOptions().MaxQueryLen+1)
	if _, err := service.ListTypes(ctx, "tn_a", Filter{Query: long}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized type search error = %v, want ErrInvalidInput", err)
	}
	if _, err := service.ListItems(ctx, "tn_a", dictionaryType.ID, ItemFilter{Query: long}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized item search error = %v, want ErrInvalidInput", err)
	}
}

// TestUpdatePreservesOmittedStatus checks that a partial update never silently enables a row. |
// TestUpdatePreservesOmittedStatus 校验局部更新不会静默启用一行。
func TestUpdatePreservesOmittedStatus(t *testing.T) {
	store := newMemoryStore()
	service, err := New(store, nil, nil, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	dictionaryType, err := service.CreateType(ctx, "tn_a", TypeInput{Code: "gender", Name: "性别", Status: StatusDisabled})
	if err != nil {
		t.Fatalf("CreateType: %v", err)
	}
	updated, err := service.UpdateType(ctx, "tn_a", dictionaryType.ID, TypeInput{Name: "性别（新）"})
	if err != nil {
		t.Fatalf("UpdateType: %v", err)
	}
	if updated.Status != StatusDisabled {
		t.Fatalf("omitted status changed to %q, want disabled", updated.Status)
	}
	item, err := service.CreateItem(ctx, "tn_a", dictionaryType.ID, ItemInput{Value: "male", Label: "男"})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if _, err := service.SetItemStatus(ctx, "tn_a", dictionaryType.ID, item.ID, StatusDisabled); err != nil {
		t.Fatalf("SetItemStatus: %v", err)
	}
	updatedItem, err := service.UpdateItem(ctx, "tn_a", dictionaryType.ID, item.ID, ItemInput{Label: "男（新）"})
	if err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	if updatedItem.Status != StatusDisabled {
		t.Fatalf("omitted item status changed to %q, want disabled", updatedItem.Status)
	}
}

// TestSnapshotLabel checks locale-aware label fallback. | TestSnapshotLabel 校验按语言回退的标签。
func TestSnapshotLabel(t *testing.T) {
	snapshot := Snapshot{Items: []SnapshotItem{{Value: "male", Label: "男", LabelEN: "Male"}, {Value: "unknown", Label: "未知"}}}
	if got := snapshot.Label("male", "en-US"); got != "Male" {
		t.Fatalf("English label = %q, want Male", got)
	}
	if got := snapshot.Label("male", "zh-CN"); got != "男" {
		t.Fatalf("Chinese label = %q, want 男", got)
	}
	if got := snapshot.Label("unknown", "en"); got != "未知" {
		t.Fatalf("fallback label = %q, want 未知", got)
	}
	if got := snapshot.Label("absent", "en"); got != "absent" {
		t.Fatalf("unknown value = %q, want absent", got)
	}
}
