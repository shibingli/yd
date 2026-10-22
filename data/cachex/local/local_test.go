package local

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// newTestCache builds a small deterministic cache. | newTestCache 构造一个小而确定的缓存。
func newTestCache(t *testing.T, o Options) *Cache {
	t.Helper()
	if o.MaxCost == 0 {
		o.MaxCost = 64 << 10
	}
	if o.Shards == 0 {
		o.Shards = 1
	}
	if o.MaxEntrySize == 0 {
		o.MaxEntrySize = 8 << 10
	}
	if o.CleanupInterval == 0 {
		o.CleanupInterval = time.Hour
	}
	c, err := New(o)
	if err != nil {
		t.Fatalf("New: %v | 构造失败", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestSetGetDelete(t *testing.T) {
	c := newTestCache(t, Options{})
	if err := c.Set("a", []byte("one"), 0); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	got, ok := c.Get("a")
	if !ok || string(got) != "one" {
		t.Fatalf("Get = (%q,%v), want (one,true) | 读取结果不符", got, ok)
	}
	got[0] = 'X'
	again, _ := c.Get("a")
	if string(again) != "one" {
		t.Fatal("Get did not return a copy | Get 未返回副本")
	}
	if !c.Delete("a") {
		t.Fatal("Delete reported false | Delete 返回 false")
	}
	if c.Delete("a") {
		t.Fatal("second Delete reported true | 第二次 Delete 返回 true")
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("deleted key still present | 删除后的键仍存在")
	}
}

func TestSetCopiesInput(t *testing.T) {
	c := newTestCache(t, Options{})
	val := []byte("abc")
	if err := c.Set("k", val, 0); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	val[0] = 'z'
	got, _ := c.Get("k")
	if string(got) != "abc" {
		t.Fatalf("stored value aliases the caller: %q | 存储值仍与调用方共享底层数组", got)
	}
}

func TestTTLExpires(t *testing.T) {
	c := newTestCache(t, Options{CleanupInterval: time.Hour})
	if err := c.Set("short", []byte("v"), 10*time.Millisecond); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if _, ok := c.Get("short"); !ok {
		t.Fatal("fresh entry missing | 新写入条目缺失")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.Get("short"); ok {
		t.Fatal("expired entry returned | 返回了过期条目")
	}
	if c.Stats().Expired == 0 {
		t.Fatal("expired counter not incremented | 过期计数未递增")
	}
	if err := c.Set("forever", []byte("v"), 0); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, ok := c.Get("forever"); !ok {
		t.Fatal("ttl=0 entry expired | ttl=0 条目过期了")
	}
}

func TestSweeperReclaimsExpired(t *testing.T) {
	c := newTestCache(t, Options{CleanupInterval: 5 * time.Millisecond, SampleSize: 128})
	for i := 0; i < 50; i++ {
		if err := c.Set(fmt.Sprintf("k%02d", i), []byte("v"), 10*time.Millisecond); err != nil {
			t.Fatalf("Set: %v | 写入失败", err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.Len() == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("sweeper left %d entries | 清扫器仍留下 %d 个条目", c.Len(), c.Len())
}

func TestGetAppendAndView(t *testing.T) {
	c := newTestCache(t, Options{})
	if err := c.Set("k", []byte("value"), 0); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	buf := []byte("prefix:")
	buf, ok := c.GetAppend(buf, "k")
	if !ok || string(buf) != "prefix:value" {
		t.Fatalf("GetAppend = %q,%v | 追加结果不符", buf, ok)
	}
	var seen string
	found, err := c.View("k", func(v []byte) error {
		seen = string(v)
		return nil
	})
	if err != nil || !found || seen != "value" {
		t.Fatalf("View = %q,%v,%v | 视图结果不符", seen, found, err)
	}
	sentinel := errors.New("stop")
	found, err = c.View("k", func([]byte) error { return sentinel })
	if !found || !errors.Is(err, sentinel) {
		t.Fatalf("View did not surface callback error: %v | View 未透传回调错误", err)
	}
	if found, err := c.View("missing", func([]byte) error { return nil }); found || err != nil {
		t.Fatalf("View(missing) = %v,%v | 缺失键视图结果不符", found, err)
	}
}

func TestDeletePrefix(t *testing.T) {
	c := newTestCache(t, Options{})
	for _, k := range []string{"user:1", "user:2", "role:1"} {
		if err := c.Set(k, []byte(k), 0); err != nil {
			t.Fatalf("Set: %v | 写入失败", err)
		}
	}
	if n := c.DeletePrefix("user:"); n != 2 {
		t.Fatalf("DeletePrefix removed %d, want 2 | 删除数量不符", n)
	}
	if _, ok := c.Get("role:1"); !ok {
		t.Fatal("unrelated key removed | 无关键被删除")
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1 | 条目数不符", c.Len())
	}
}

func TestClear(t *testing.T) {
	c := newTestCache(t, Options{})
	for i := 0; i < 20; i++ {
		if err := c.Set(fmt.Sprintf("k%d", i), []byte("v"), 0); err != nil {
			t.Fatalf("Set: %v | 写入失败", err)
		}
	}
	c.Clear()
	if c.Len() != 0 || c.Cost() != 0 {
		t.Fatalf("Clear left Len=%d Cost=%d | Clear 后仍有残留", c.Len(), c.Cost())
	}
	if _, ok := c.Get("k0"); ok {
		t.Fatal("entry survived Clear | 条目在 Clear 后仍存在")
	}
}

func TestInputValidation(t *testing.T) {
	c := newTestCache(t, Options{MaxEntrySize: 128})
	if err := c.Set("", []byte("v"), 0); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty key error = %v | 空键错误不符", err)
	}
	longKey := make([]byte, MaxKeyLength+1)
	for i := range longKey {
		longKey[i] = 'k'
	}
	if err := c.Set(string(longKey), []byte("v"), 0); !errors.Is(err, ErrKeyTooLong) {
		t.Fatalf("long key error = %v | 长键错误不符", err)
	}
	if err := c.Set("k", make([]byte, 1024), 0); !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("large value error = %v | 大值错误不符", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	c := newTestCache(t, Options{})
	if err := c.Set("k", []byte("v"), 0); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v | 关闭失败", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v | 第二次关闭失败", err)
	}
	if err := c.Set("k", []byte("v"), 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("Set after Close = %v | 关闭后写入错误不符", err)
	}
	if _, ok := c.Get("k"); ok {
		t.Fatal("Get after Close returned a value | 关闭后仍读到值")
	}
	if _, err := c.View("k", func([]byte) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("View after Close = %v | 关闭后视图错误不符", err)
	}
	c.Delete("k")
	c.DeletePrefix("k")
	c.Clear()
	c.Sweep()
}

func TestEvictionKeepsWithinBudget(t *testing.T) {
	c := newTestCache(t, Options{MaxCost: 64 << 10, MaxEntrySize: 1024})
	value := make([]byte, 256)
	for i := 0; i < 4000; i++ {
		if err := c.Set(fmt.Sprintf("key:%05d", i), value, 0); err != nil {
			t.Fatalf("Set: %v | 写入失败", err)
		}
	}
	st := c.Stats()
	if st.Cost > c.Capacity() {
		t.Fatalf("cost %d exceeds capacity %d | 开销超过容量", st.Cost, c.Capacity())
	}
	if st.Evictions == 0 {
		t.Fatal("no evictions under pressure | 受压时没有发生驱逐")
	}
	if c.Len() == 0 {
		t.Fatal("cache is empty after writes | 写入后缓存为空")
	}
}

func TestHotEntrySurvivesColdFlood(t *testing.T) {
	c := newTestCache(t, Options{MaxCost: 16 << 10, MaxEntrySize: 512, Shards: 1})
	if err := c.Set("hot", []byte("hot-value"), 0); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	for i := 0; i < 50; i++ {
		if _, ok := c.Get("hot"); !ok {
			t.Fatal("hot key disappeared | 热键消失")
		}
	}
	value := make([]byte, 128)
	for i := 0; i < 2000; i++ {
		if err := c.Set(fmt.Sprintf("cold:%05d", i), value, 0); err != nil {
			t.Fatalf("Set: %v | 写入失败", err)
		}
	}
	if _, ok := c.Get("hot"); !ok {
		t.Fatal("TinyLFU admitted cold entries over a hot one | TinyLFU 让冷条目挤掉了热条目")
	}
}

func TestRingWrap(t *testing.T) {
	c := newTestCache(t, Options{MaxCost: 8 << 10, MaxEntrySize: 512, Shards: 1})
	// Phase one: uneven values force the write head to wrap many times. Entries are
	// deliberately allowed to be evicted; this phase only exercises the ring arithmetic.
	// | 第一阶段：不齐整的值迫使写头多次回绕。这里允许条目被驱逐，只检验环形算术。
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("wrap:%03d", i)
		value := []byte(fmt.Sprintf("%03d:%s", i, string(make([]byte, i%37))))
		if err := c.Set(key, value, 0); err != nil {
			t.Fatalf("Set: %v | 写入失败", err)
		}
	}
	// Phase two: a fresh batch that comfortably fits must be readable and intact after
	// all the wrapping. | 第二阶段：一整批放得下的新键在多次回绕后必须完整可读。
	for i := 0; i < 40; i++ {
		key := fmt.Sprintf("final:%02d", i)
		value := []byte(fmt.Sprintf("value-%02d-%s", i, string(make([]byte, i%23))))
		if err := c.Set(key, value, 0); err != nil {
			t.Fatalf("Set: %v | 写入失败", err)
		}
		got, ok := c.Get(key)
		if !ok || string(got) != string(value) {
			t.Fatalf("key %q = %q,%v | 回绕后取值不符", key, got, ok)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := newTestCache(t, Options{MaxCost: 128 << 10, MaxEntrySize: 1024, Shards: 4})
	const workers = 8
	const ops = 2000
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				key := fmt.Sprintf("k:%d:%d", w, i%128)
				switch i % 4 {
				case 0, 1:
					_ = c.Set(key, []byte(key), time.Duration(i%7)*time.Millisecond)
				case 2:
					_, _ = c.Get(key)
				case 3:
					c.Delete(key)
				}
			}
		}(w)
	}
	wg.Wait()
	if st := c.Stats(); st.Hits+st.Misses == 0 {
		t.Fatal("no lookups recorded | 没有记录任何查询")
	}
}

func TestZeroAllocationsOnHotPath(t *testing.T) {
	c := newTestCache(t, Options{MaxCost: 64 << 10, MaxEntrySize: 1024, Shards: 1})
	if err := c.Set("hot", []byte("0123456789abcdef"), 0); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	buf := make([]byte, 0, 64)
	if allocs := testing.AllocsPerRun(1000, func() {
		buf, _ = c.GetAppend(buf[:0], "hot")
	}); allocs != 0 {
		t.Fatalf("GetAppend allocated %v times | GetAppend 分配了 %v 次", allocs, allocs)
	}
	if allocs := testing.AllocsPerRun(1000, func() {
		_, _ = c.View("hot", func([]byte) error { return nil })
	}); allocs != 0 {
		t.Fatalf("View allocated %v times | View 分配了 %v 次", allocs, allocs)
	}
	if allocs := testing.AllocsPerRun(1000, func() {
		_ = c.Set("hot", []byte("0123456789abcdef"), 0)
	}); allocs != 0 {
		t.Fatalf("Set allocated %v times | Set 分配了 %v 次", allocs, allocs)
	}
}

func TestInvalidOptions(t *testing.T) {
	if _, err := New(Options{MaxCost: 128}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("tiny max cost error = %v | 极小预算错误不符", err)
	}
	if _, err := New(Options{MaxCost: 64 << 10, MaxEntrySize: 1 << 20}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("oversized entry limit error = %v | 过大条目上限错误不符", err)
	}
}
