package dictionary

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/ydx/cache"
	"github.com/shibingli/ydx/cache/local"
)

// TestResolveCachesAndInvalidates checks the L1 path and write invalidation. |
// TestResolveCachesAndInvalidates 校验 L1 命中与写入失效。
func TestResolveCachesAndInvalidates(t *testing.T) {
	manager, err := cache.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	snapshots, err := cache.New[Snapshot](manager, "dictionary")
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	store := newMemoryStore()
	service, err := New(store, snapshots, nil, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	dictionaryType, err := service.CreateType(ctx, "tn_a", TypeInput{Code: "gender", Name: "性别", NameEN: "Gender"})
	if err != nil {
		t.Fatalf("CreateType: %v", err)
	}
	if _, err := service.CreateItem(ctx, "tn_a", dictionaryType.ID, ItemInput{Value: "male", Label: "男", Default: true}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if _, err := service.Resolve(ctx, "tn_a", "gender"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := service.Resolve(ctx, "tn_a", "gender"); err != nil {
		t.Fatalf("Resolve second: %v", err)
	}
	if store.resolveCalls != 1 {
		t.Fatalf("resolve calls = %d, want 1", store.resolveCalls)
	}
	if _, err := service.UpdateType(ctx, "tn_a", dictionaryType.ID, TypeInput{Name: "性别（新）", Status: StatusEnabled}); err != nil {
		t.Fatalf("UpdateType: %v", err)
	}
	snapshot, err := service.Resolve(ctx, "tn_a", "gender")
	if err != nil {
		t.Fatalf("Resolve after update: %v", err)
	}
	if snapshot.Name != "性别（新）" {
		t.Fatalf("cached name = %q, want the updated one", snapshot.Name)
	}
	if store.resolveCalls != 2 {
		t.Fatalf("resolve calls after update = %d, want 2", store.resolveCalls)
	}
	if _, err := service.Resolve(ctx, "tn_other", "gender"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant Resolve error = %v, want ErrNotFound", err)
	}
}

// TestResolveManyUsesPerCodeCache checks that warm codes are cache hits and a cold code is loaded
// through its own GetOrLoad. | TestResolveManyUsesPerCodeCache 校验命中编码走缓存、冷编码经各自的 GetOrLoad 加载。
func TestResolveManyUsesPerCodeCache(t *testing.T) {
	manager, err := cache.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	snapshots, err := cache.New[Snapshot](manager, "dictionary")
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	store := newMemoryStore()
	service, err := New(store, snapshots, nil, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	for _, code := range []string{"gender", "priority", "yes_no"} {
		if _, err := service.CreateType(ctx, "tn_a", TypeInput{Code: code, Name: code}); err != nil {
			t.Fatalf("CreateType(%s): %v", code, err)
		}
	}
	got, err := service.ResolveMany(ctx, "tn_a", []string{"gender", "priority", "yes_no", "missing", "gender"})
	if err != nil {
		t.Fatalf("ResolveMany: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("resolved %d dictionaries, want 3", len(got))
	}
	// Three existing codes plus the missing one each own one GetOrLoad on a cold batch. |
	// 冷批量中三个存在的编码与一个缺失编码各触发一次 GetOrLoad。
	if store.resolveCalls != 4 {
		t.Fatalf("resolve calls = %d, want 4", store.resolveCalls)
	}
	if _, err := service.ResolveMany(ctx, "tn_a", []string{"gender", "priority"}); err != nil {
		t.Fatalf("ResolveMany warm: %v", err)
	}
	if store.resolveCalls != 4 {
		t.Fatalf("resolve calls after warm batch = %d, want 4", store.resolveCalls)
	}
	if _, err := service.ResolveMany(ctx, "tn_a", []string{"Bad-Code"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid code error = %v, want ErrInvalidInput", err)
	}
	tooMany := make([]string, 0, DefaultOptions().MaxBatchCodes+1)
	for i := 0; i <= DefaultOptions().MaxBatchCodes; i++ {
		tooMany = append(tooMany, "code_"+strconv.Itoa(i))
	}
	if _, err := service.ResolveMany(ctx, "tn_a", tooMany); !errors.Is(err, ErrTooManyCodes) {
		t.Fatalf("too many codes error = %v, want ErrTooManyCodes", err)
	}
}

// TestResolveFallsBackWhenTheCacheCannotStoreTheValue checks that an entry larger than the L1 budget
// still resolves through the store. | TestResolveFallsBackWhenTheCacheCannotStoreTheValue 校验超过 L1 预算的
// 条目仍然能通过存储解析。
func TestResolveFallsBackWhenTheCacheCannotStoreTheValue(t *testing.T) {
	manager, err := cache.NewManager(cache.WithLocalOptions(local.Options{
		MaxCost:      4096,
		MaxEntrySize: 1024,
		Shards:       1,
	}))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	snapshots, err := cache.New[Snapshot](manager, "dictionary")
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	store := newMemoryStore()
	service, err := New(store, snapshots, nil, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	dictionaryType, err := service.CreateType(ctx, "tn_a", TypeInput{Code: "long", Name: "长字典"})
	if err != nil {
		t.Fatalf("CreateType: %v", err)
	}
	extra := `{"blob":"` + strings.Repeat("x", 2000) + `"}`
	if _, err := service.CreateItem(ctx, "tn_a", dictionaryType.ID, ItemInput{Value: "value", Label: "长标签", Extra: extra}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	snapshot, err := service.Resolve(ctx, "tn_a", "long")
	if err != nil {
		t.Fatalf("Resolve with an oversized cache entry: %v", err)
	}
	if snapshot.Code != "long" || len(snapshot.Items) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

// TestResolveNegativeCacheInvalidatedOnCreate checks that a missing code cached negatively becomes
// visible immediately after creation. | TestResolveNegativeCacheInvalidatedOnCreate 校验负缓存的缺失编码在
// 创建后立即可见。
func TestResolveNegativeCacheInvalidatedOnCreate(t *testing.T) {
	manager, err := cache.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	snapshots, err := cache.New[Snapshot](manager, "dictionary", cache.WithNegativeTTL(time.Minute))
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	store := newMemoryStore()
	service, err := New(store, snapshots, nil, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if _, err := service.Resolve(ctx, "tn_a", "gender"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Resolve error = %v, want ErrNotFound", err)
	}
	if _, err := service.CreateType(ctx, "tn_a", TypeInput{Code: "gender", Name: "性别"}); err != nil {
		t.Fatalf("CreateType: %v", err)
	}
	snapshot, err := service.Resolve(ctx, "tn_a", "gender")
	if err != nil {
		t.Fatalf("Resolve after create: %v", err)
	}
	if snapshot.Code != "gender" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
