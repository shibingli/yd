package redisx_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/shibingli/yd/data/redisx"
	"github.com/shibingli/yd/jsonx"
)

// cachedUser is the payload type exercised by the cache tests. | cachedUser 是缓存用例使用的载荷类型。
type cachedUser struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// errLoadProbe is the loader failure used by the "not cached on failure" case. | errLoadProbe 是「失败不入缓存」用例使用的 loader 失败。
var errLoadProbe = errors.New("loader probe failure")

// newCache opens a single-node client plus a cache decorator with a fixed prefix. | newCache 打开单机客户端与固定前缀的缓存装饰器。
func newCache(t *testing.T, mr *miniredis.Miniredis, o redisx.CacheOptions) (redis.UniversalClient, *redisx.Cache[cachedUser]) {
	t.Helper()
	cli := openSingle(t, mr)
	if o.Prefix == "" {
		o.Prefix = "cache:"
	}
	if o.TTL == 0 {
		o.TTL = time.Minute
	}
	return cli, redisx.NewCache[cachedUser](cli, o)
}

// TestCacheGetOrLoadCachesAndReuses asserts the loader runs once and the second read is served from Redis. | TestCacheGetOrLoadCachesAndReuses 断言 loader 只运行一次，第二次读取由 Redis 提供。
func TestCacheGetOrLoadCachesAndReuses(t *testing.T) {
	mr := newMiniRedis(t)
	cli, cache := newCache(t, mr, redisx.CacheOptions{})

	var calls atomic.Int32
	want := cachedUser{ID: 7, Name: "alice"}
	loader := func(context.Context) (cachedUser, error) {
		calls.Add(1)
		return want, nil
	}

	ctx := context.Background()
	got, err := cache.GetOrLoad(ctx, "user:7", loader)
	if err != nil {
		t.Fatalf("首次 GetOrLoad 失败: %v", err)
	}
	if got != want {
		t.Fatalf("首次 GetOrLoad = %+v，期望 %+v", got, want)
	}

	again, err := cache.GetOrLoad(ctx, "user:7", loader)
	if err != nil {
		t.Fatalf("二次 GetOrLoad 失败: %v", err)
	}
	if again != want {
		t.Fatalf("二次 GetOrLoad = %+v，期望 %+v", again, want)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("loader 调用 %d 次，期望 1 次", n)
	}

	// The entry carries a TTL, so no permanent key is written. | 缓存条目带 TTL，避免写入永久键。
	ttl, err := cli.TTL(ctx, "cache:user:7").Result()
	if err != nil {
		t.Fatalf("TTL 查询失败: %v", err)
	}
	if ttl <= 0 || ttl > time.Minute {
		t.Fatalf("TTL = %v，期望落在 (0, 1m] 内", ttl)
	}
}

// TestCacheSingleflightUnderConcurrency asserts 100 concurrent misses collapse into a single loader call. | TestCacheSingleflightUnderConcurrency 断言 100 个并发未命中收敛为一次 loader 调用。
//
// Run this case with -race: it is the repository's guard that the decorator shares no mutable state. | 该用例需以 -race 运行：它是本仓对「装饰器不共享可变状态」的护栏。
func TestCacheSingleflightUnderConcurrency(t *testing.T) {
	mr := newMiniRedis(t)
	_, cache := newCache(t, mr, redisx.CacheOptions{})

	const workers = 100
	var calls atomic.Int32
	want := cachedUser{ID: 42, Name: "concurrent"}
	loader := func(context.Context) (cachedUser, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return want, nil
	}

	ctx := context.Background()
	results := make([]cachedUser, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = cache.GetOrLoad(ctx, "user:42", loader)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 个并发 GetOrLoad 失败: %v", i, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("并发下 loader 调用 %d 次，期望 1 次（单飞失效）", n)
	}
	for i, got := range results {
		if got != want {
			t.Fatalf("第 %d 个结果 = %+v，期望 %+v", i, got, want)
		}
	}
}

// TestCacheLoadErrorNotCached asserts a failed load never pollutes Redis. | TestCacheLoadErrorNotCached 断言 load 失败绝不污染 Redis。
func TestCacheLoadErrorNotCached(t *testing.T) {
	mr := newMiniRedis(t)
	cli, cache := newCache(t, mr, redisx.CacheOptions{})

	var calls atomic.Int32
	loader := func(context.Context) (cachedUser, error) {
		calls.Add(1)
		return cachedUser{}, errLoadProbe
	}

	ctx := context.Background()
	for i := range 2 {
		got, err := cache.GetOrLoad(ctx, "user:9", loader)
		if !errors.Is(err, errLoadProbe) {
			t.Fatalf("第 %d 次 GetOrLoad 错误 = %v，期望 errors.Is(err, errLoadProbe)", i+1, err)
		}
		if got != (cachedUser{}) {
			t.Fatalf("第 %d 次失败返回值 = %+v，期望零值", i+1, got)
		}
	}

	if n := calls.Load(); n != 2 {
		t.Fatalf("loader 调用 %d 次，期望 2 次（失败结果不得被记住）", n)
	}
	exists, err := cli.Exists(ctx, "cache:user:9").Result()
	if err != nil {
		t.Fatalf("EXISTS 查询失败: %v", err)
	}
	if exists != 0 {
		t.Fatalf("失败后缓存键存在 %d 个，期望 0 个", exists)
	}
}

// TestCacheNegativeTTLCachesNotFound asserts the ErrNotFound contract writes a short-lived negative entry. | TestCacheNegativeTTLCachesNotFound 断言 ErrNotFound 约定写入短时效的负缓存条目。
func TestCacheNegativeTTLCachesNotFound(t *testing.T) {
	mr := newMiniRedis(t)
	cli, cache := newCache(t, mr, redisx.CacheOptions{NegativeTTL: 30 * time.Second})

	var calls atomic.Int32
	loader := func(context.Context) (cachedUser, error) {
		calls.Add(1)
		return cachedUser{}, redisx.ErrNotFound
	}

	ctx := context.Background()
	for i := range 2 {
		got, err := cache.GetOrLoad(ctx, "user:404", loader)
		if !errors.Is(err, redisx.ErrNotFound) {
			t.Fatalf("第 %d 次 GetOrLoad 错误 = %v，期望 ErrNotFound", i+1, err)
		}
		if got != (cachedUser{}) {
			t.Fatalf("第 %d 次返回值 = %+v，期望零值", i+1, got)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("loader 调用 %d 次，期望 1 次（负缓存应挡住穿透）", n)
	}

	// Get reports a negative entry as a miss, so a zero value is never mistaken for real data. | Get 把负缓存条目当作未命中上报，调用方不会把零值误当真实值。
	if _, ok, err := cache.Get(ctx, "user:404"); ok || err != nil {
		t.Fatalf("负缓存 Get = (_, %v, %v)，期望 (_, false, nil)", ok, err)
	}

	ttl, err := cli.TTL(ctx, "cache:user:404").Result()
	if err != nil {
		t.Fatalf("TTL 查询失败: %v", err)
	}
	if ttl <= 0 || ttl > 30*time.Second {
		t.Fatalf("负缓存 TTL = %v，期望落在 (0, 30s] 内", ttl)
	}

	// Once the entity exists, Invalidate makes the next call hit the loader again. | 实体出现后，Invalidate 让下一次调用重新走 loader。
	if err := cache.Invalidate(ctx, "user:404"); err != nil {
		t.Fatalf("Invalidate 失败: %v", err)
	}
	if _, err := cache.GetOrLoad(ctx, "user:404", loader); !errors.Is(err, redisx.ErrNotFound) {
		t.Fatalf("Invalidate 后 GetOrLoad 错误 = %v，期望 ErrNotFound", err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("Invalidate 后 loader 调用 %d 次，期望 2 次", n)
	}
}

// TestCacheKeyPrefixApplied asserts every key is namespaced by the prefix and Delete removes it. | TestCacheKeyPrefixApplied 断言所有键都带前缀命名空间，且 Delete 能删除它。
func TestCacheKeyPrefixApplied(t *testing.T) {
	mr := newMiniRedis(t)
	cli, cache := newCache(t, mr, redisx.CacheOptions{Prefix: "app:"})

	ctx := context.Background()
	if err := cache.Set(ctx, "user:1", cachedUser{ID: 1, Name: "bob"}); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
	if n, err := cli.Exists(ctx, "app:user:1").Result(); err != nil || n != 1 {
		t.Fatalf("前缀键 EXISTS = (%d, %v)，期望 (1, nil)", n, err)
	}
	if n, err := cli.Exists(ctx, "user:1").Result(); err != nil || n != 0 {
		t.Fatalf("裸键 EXISTS = (%d, %v)，期望 (0, nil)", n, err)
	}

	got, ok, err := cache.Get(ctx, "user:1")
	if err != nil || !ok || got.Name != "bob" {
		t.Fatalf("Get = (%+v, %v, %v)，期望命中 bob", got, ok, err)
	}

	if err := cache.Delete(ctx, "user:1"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if _, ok, err := cache.Get(ctx, "user:1"); ok || err != nil {
		t.Fatalf("Delete 后 Get = (_, %v, %v)，期望未命中", ok, err)
	}
}

// TestCacheUsesAdaptiveJSON asserts the payload is plain JSON produced and consumed by jsonx. | TestCacheUsesAdaptiveJSON 断言载荷是 jsonx 产出的纯 JSON，且能被 jsonx 反解。
func TestCacheUsesAdaptiveJSON(t *testing.T) {
	mr := newMiniRedis(t)
	cli, cache := newCache(t, mr, redisx.CacheOptions{})

	ctx := context.Background()
	want := cachedUser{ID: 5, Name: "json"}
	if err := cache.Set(ctx, "user:5", want); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}

	raw, err := cli.Get(ctx, "cache:user:5").Bytes()
	if err != nil {
		t.Fatalf("读取原始字节失败: %v", err)
	}
	var decoded cachedUser
	if err := jsonx.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("jsonx.Unmarshal 原始载荷失败: %v", err)
	}
	if decoded != want {
		t.Fatalf("原始载荷解码 = %+v，期望 %+v", decoded, want)
	}
	if !strings.Contains(string(raw), `"name":"json"`) {
		t.Fatalf("原始载荷 = %s，期望 JSON 对象文本", raw)
	}

	// Interop in reverse: a payload written externally with jsonx is readable by Get. | 反向互通：外部用 jsonx 写入的载荷必须能被 Get 读出。
	external, err := jsonx.Marshal(cachedUser{ID: 6, Name: "external"})
	if err != nil {
		t.Fatalf("jsonx.Marshal 失败: %v", err)
	}
	if err := cli.Set(ctx, "cache:user:6", external, time.Minute).Err(); err != nil {
		t.Fatalf("外部写入失败: %v", err)
	}
	got, ok, err := cache.Get(ctx, "user:6")
	if err != nil || !ok || got.Name != "external" {
		t.Fatalf("外部载荷 Get = (%+v, %v, %v)，期望命中 external", got, ok, err)
	}
}

// TestLoadManyPipelinesMissing asserts only the missing keys reach the loader and the result is written back. | TestLoadManyPipelinesMissing 断言只有缺失键进入 loader，且结果被回填。
func TestLoadManyPipelinesMissing(t *testing.T) {
	mr := newMiniRedis(t)
	cli, cache := newCache(t, mr, redisx.CacheOptions{})

	ctx := context.Background()
	warm := map[string]cachedUser{"user:1": {ID: 1, Name: "a"}, "user:2": {ID: 2, Name: "b"}}
	for k, u := range warm {
		if err := cache.Set(ctx, k, u); err != nil {
			t.Fatalf("预热 Set %s 失败: %v", k, err)
		}
	}

	var (
		mu      sync.Mutex
		batches [][]string
		calls   int
	)
	loader := func(_ context.Context, keys []string) (map[string]cachedUser, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		batches = append(batches, append([]string(nil), keys...))
		out := make(map[string]cachedUser, len(keys))
		for _, k := range keys {
			out[k] = cachedUser{ID: 3, Name: "loaded:" + k}
		}
		return out, nil
	}

	keys := []string{"user:1", "user:2", "user:3"}
	got, err := cache.LoadMany(ctx, keys, loader)
	if err != nil {
		t.Fatalf("LoadMany 失败: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("LoadMany 返回 %d 个键，期望 3 个: %+v", len(got), got)
	}
	if got["user:1"].Name != "a" || got["user:2"].Name != "b" {
		t.Fatalf("命中键被 loader 覆盖: %+v", got)
	}
	if got["user:3"].Name != "loaded:user:3" {
		t.Fatalf("缺失键未回填: %+v", got)
	}

	mu.Lock()
	if calls != 1 {
		mu.Unlock()
		t.Fatalf("loader 调用 %d 次，期望 1 次", calls)
	}
	if len(batches[0]) != 1 || batches[0][0] != "user:3" {
		mu.Unlock()
		t.Fatalf("loader 收到的键 = %v，期望仅 [user:3]", batches[0])
	}
	mu.Unlock()

	// After the backfill the second call no longer wakes the loader. | 回填后再次调用不再唤醒 loader。
	if _, err := cache.LoadMany(ctx, keys, loader); err != nil {
		t.Fatalf("二次 LoadMany 失败: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("回填后 loader 调用 %d 次，期望仍为 1 次", calls)
	}

	// The loaded value really landed in Redis, with a TTL attached. | 回填值确实写进了 Redis，并带上 TTL。
	ttl, err := cli.TTL(ctx, "cache:user:3").Result()
	if err != nil {
		t.Fatalf("TTL 查询失败: %v", err)
	}
	if ttl <= 0 || ttl > time.Minute {
		t.Fatalf("回填条目 TTL = %v，期望落在 (0, 1m] 内", ttl)
	}
}

// TestLoadManyLoaderErrorLeavesCacheClean asserts a batch failure writes nothing back. | TestLoadManyLoaderErrorLeavesCacheClean 断言批量失败时不回填任何数据。
func TestLoadManyLoaderErrorLeavesCacheClean(t *testing.T) {
	mr := newMiniRedis(t)
	cli, cache := newCache(t, mr, redisx.CacheOptions{})

	ctx := context.Background()
	loader := func(context.Context, []string) (map[string]cachedUser, error) {
		return nil, errLoadProbe
	}
	if _, err := cache.LoadMany(ctx, []string{"user:8"}, loader); !errors.Is(err, errLoadProbe) {
		t.Fatalf("LoadMany 错误 = %v，期望 errors.Is(err, errLoadProbe)", err)
	}
	if n, err := cli.Exists(ctx, "cache:user:8").Result(); err != nil || n != 0 {
		t.Fatalf("失败后 EXISTS = (%d, %v)，期望 (0, nil)", n, err)
	}

	if _, err := cache.LoadMany(ctx, []string{"user:8"}, nil); !errors.Is(err, redisx.ErrNilLoader) {
		t.Fatalf("nil loader 错误 = %v，期望 ErrNilLoader", err)
	}
}
