package cachex

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/data/cachex/local"
)

// testLogger discards operational noise. | testLogger 丢弃运行噪音。
func testLogger() *logx.Logger {
	return logx.New(logx.Options{Writer: io.Discard, Level: logx.ErrorLevel})
}

// testLocal keeps tests small and deterministic. | testLocal 让测试小而确定。
func testLocal() local.Options {
	return local.Options{MaxCost: 1 << 20, Shards: 2, MaxEntrySize: 64 << 10, CleanupInterval: time.Hour}
}

// newTestManager builds a manager wired to r (nil means local-only). | newTestManager 构造连接到 r 的管理器（nil 表示仅本地）。
func newTestManager(t *testing.T, r Remote, opts ...Option) *Manager {
	t.Helper()
	base := []Option{
		WithRemote(r),
		WithLocalOptions(testLocal()),
		WithLogger(testLogger()),
		WithLease(50 * time.Millisecond),
	}
	base = append(base, opts...)
	m, err := NewManager(base...)
	if err != nil {
		t.Fatalf("NewManager: %v | 构造管理器失败", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// waitSubscribed blocks until f has at least n subscribers. | waitSubscribed 阻塞到 f 至少有 n 个订阅者。
func waitSubscribed(t *testing.T, f *fakeRemote, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := len(f.subs)
		f.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("remote has %d subscribers, want %d | 远端订阅者数量不符", len(f.subs), n)
}

func TestRawLocalOnlyCRUD(t *testing.T) {
	m := newTestManager(t, nil)
	r, err := m.Raw("users", WithTTL(time.Minute))
	if err != nil {
		t.Fatalf("Raw: %v | 打开命名空间失败", err)
	}
	ctx := context.Background()
	if err := r.Set(ctx, "a", []byte("one")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	got, ok, err := r.Get(ctx, "a")
	if err != nil || !ok || string(got) != "one" {
		t.Fatalf("Get = %q,%v,%v | 读取结果不符", got, ok, err)
	}
	buf := []byte("x:")
	buf, ok, err = r.GetAppend(ctx, buf, "a")
	if err != nil || !ok || string(buf) != "x:one" {
		t.Fatalf("GetAppend = %q,%v,%v | 追加结果不符", buf, ok, err)
	}
	var seen string
	found, err := r.View(ctx, "a", func(data []byte) error {
		seen = string(data)
		return nil
	})
	if err != nil || !found || seen != "one" {
		t.Fatalf("View = %q,%v,%v | 视图结果不符", seen, found, err)
	}
	if err := r.Delete(ctx, "a"); err != nil {
		t.Fatalf("Delete: %v | 删除失败", err)
	}
	if _, ok, _ := r.Get(ctx, "a"); ok {
		t.Fatal("deleted key still present | 删除后的键仍存在")
	}
}

func TestRawTTL(t *testing.T) {
	m := newTestManager(t, nil)
	r, _ := m.Raw("ttl", WithTTL(15*time.Millisecond))
	ctx := context.Background()
	if err := r.Set(ctx, "k", []byte("v")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if _, ok, _ := r.Get(ctx, "k"); !ok {
		t.Fatal("fresh key missing | 新键缺失")
	}
	time.Sleep(25 * time.Millisecond)
	if _, ok, _ := r.Get(ctx, "k"); ok {
		t.Fatal("expired key returned | 返回了过期键")
	}
}

func TestTwoLevelL1ThenL2(t *testing.T) {
	f := newFakeRemote()
	m := newTestManager(t, f)
	waitSubscribed(t, f, 1)
	r, _ := m.Raw("users")
	ctx := context.Background()
	if err := r.Set(ctx, "a", []byte("v1")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if _, ok, err := r.Get(ctx, "a"); err != nil || !ok {
		t.Fatalf("L1 Get = %v,%v | L1 读取失败", ok, err)
	}
	loads, _ := f.counts()
	if loads != 0 {
		t.Fatalf("L1 hit touched L2 %d times | L1 命中却访问了 L2 %d 次", loads, loads)
	}
	m.local.Delete("users:a")
	got, ok, err := r.Get(ctx, "a")
	if err != nil || !ok || string(got) != "v1" {
		t.Fatalf("L2 Get = %q,%v,%v | L2 读取结果不符", got, ok, err)
	}
	if loads, _ := f.counts(); loads != 1 {
		t.Fatalf("L2 loads = %d, want 1 | L2 读取次数不符", loads)
	}
	if _, ok, _ := r.Get(ctx, "a"); !ok {
		t.Fatal("value not refilled into L1 | 值未回填到 L1")
	}
	if st := r.Stats(); st.RemoteHits != 1 {
		t.Fatalf("RemoteHits = %d, want 1 | 远端命中计数不符", st.RemoteHits)
	}
}

func TestCrossManagerInvalidation(t *testing.T) {
	f := newFakeRemote()
	m1 := newTestManager(t, f)
	m2 := newTestManager(t, f)
	waitSubscribed(t, f, 2)
	r1, _ := m1.Raw("shared")
	r2, _ := m2.Raw("shared")
	ctx := context.Background()
	if err := r1.Set(ctx, "k", []byte("v1")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if got, ok, _ := r2.Get(ctx, "k"); !ok || string(got) != "v1" {
		t.Fatalf("m2 first Get = %q,%v | m2 首次读取不符", got, ok)
	}
	if err := r1.Set(ctx, "k", []byte("v2")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if got, ok, _ := r2.Get(ctx, "k"); !ok || string(got) != "v2" {
		t.Fatalf("m2 after invalidation = %q,%v | m2 失效后读取不符", got, ok)
	}
	if m2.Stats().Invalidations == 0 {
		t.Fatal("m2 did not record an invalidation | m2 未记录失效")
	}
}

func TestCASRejectsStaleWriteBack(t *testing.T) {
	f := newFakeRemote()
	m := newTestManager(t, f)
	waitSubscribed(t, f, 1)
	r, _ := m.Raw("cas")
	ctx := context.Background()
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	type result struct {
		value []byte
		err   error
	}
	done := make(chan result, 1)
	go func() {
		v, err := r.GetOrLoad(ctx, "k", func(context.Context) ([]byte, error) {
			once.Do(func() { close(started) })
			<-release
			return []byte("old"), nil
		})
		done <- result{value: v, err: err}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("loader did not start | loader 未启动")
	}
	if err := r.Set(ctx, "k", []byte("new")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	close(release)
	res := <-done
	if res.err != nil {
		t.Fatalf("GetOrLoad: %v | 读取失败", res.err)
	}
	if string(res.value) != "new" {
		t.Fatalf("GetOrLoad = %q, want new | CAS 未阻止陈旧回填", res.value)
	}
	if st := r.Stats(); st.CASLost != 1 {
		t.Fatalf("CASLost = %d, want 1 | CAS 失败计数不符", st.CASLost)
	}
	if data, _, ok := f.value("cas:k"); !ok || string(data) != "new" {
		t.Fatalf("L2 value = %q,%v, want new | L2 最终值不符", data, ok)
	}
}

func TestNegativeCaching(t *testing.T) {
	f := newFakeRemote()
	m := newTestManager(t, f)
	r, _ := m.Raw("neg", WithNegativeTTL(time.Minute))
	ctx := context.Background()
	var loads atomic.Int32
	missing := func(context.Context) ([]byte, error) {
		loads.Add(1)
		return nil, ErrNotFound
	}
	if _, err := r.GetOrLoad(ctx, "gone", missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first GetOrLoad = %v | 首次读取错误不符", err)
	}
	if _, err := r.GetOrLoad(ctx, "gone", func(context.Context) ([]byte, error) {
		t.Fatal("loader called for a negative entry | 负缓存条目仍唤醒了 loader")
		return nil, nil
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second GetOrLoad = %v | 第二次读取错误不符", err)
	}
	if loads.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1 | loader 调用次数不符", loads.Load())
	}
	if _, ok, _ := r.Get(ctx, "gone"); ok {
		t.Fatal("negative entry reported as a hit | 负缓存被报告为命中")
	}
	if st := r.Stats(); st.Negatives == 0 {
		t.Fatal("negative counter not incremented | 负缓存计数未递增")
	}
}

func TestEventSubscription(t *testing.T) {
	m := newTestManager(t, nil)
	r, _ := m.Raw("evt")
	events := make(chan Event, 8)
	unsub, err := r.Subscribe(func(ev Event) { events <- ev })
	if err != nil {
		t.Fatalf("Subscribe: %v | 订阅失败", err)
	}
	defer unsub()
	ctx := context.Background()
	if err := r.Set(ctx, "k", []byte("v")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if err := r.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete: %v | 删除失败", err)
	}
	if err := r.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v | 清理失败", err)
	}
	want := []Op{OpSet, OpDelete, OpClear}
	for _, op := range want {
		select {
		case ev := <-events:
			if ev.Op != op || ev.Namespace != "evt" {
				t.Fatalf("event = (%d,%q), want (%d,evt) | 事件不符", ev.Op, ev.Namespace, op)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for op %d | 等待操作 %d 超时", op, op)
		}
	}
}

func TestNamespaceConflict(t *testing.T) {
	m := newTestManager(t, nil)
	r, err := m.Raw("dup")
	if err != nil {
		t.Fatalf("Raw: %v | 打开命名空间失败", err)
	}
	if _, err := m.Raw("dup"); !errors.Is(err, ErrNamespaceInUse) {
		t.Fatalf("duplicate namespace = %v | 重复命名空间错误不符", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v | 关闭失败", err)
	}
	if _, err := m.Raw("dup"); err != nil {
		t.Fatalf("reopen after Close: %v | 关闭后重开失败", err)
	}
	for _, bad := range []string{"", "a:b", string([]byte{'x', 0})} {
		if _, err := m.Raw(bad); !errors.Is(err, ErrInvalidNamespace) {
			t.Fatalf("namespace %q error = %v | 非法命名空间错误不符", bad, err)
		}
	}
}

func TestLeaseBoundsStalenessAfterLostEvent(t *testing.T) {
	f := newFakeRemote()
	m1 := newTestManager(t, f, WithLease(20*time.Millisecond))
	m2 := newTestManager(t, f, WithLease(20*time.Millisecond))
	waitSubscribed(t, f, 2)
	r1, _ := m1.Raw("lease")
	r2, _ := m2.Raw("lease")
	ctx := context.Background()
	if err := r1.Set(ctx, "k", []byte("v1")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if got, ok, _ := r2.Get(ctx, "k"); !ok || string(got) != "v1" {
		t.Fatalf("initial Get = %q,%v | 初始读取不符", got, ok)
	}
	f.setDrop(true)
	if err := r1.Set(ctx, "k", []byte("v2")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if got, _, _ := r2.Get(ctx, "k"); string(got) != "v1" {
		t.Fatalf("L1 should still hold v1, got %q | L1 本应仍是 v1", got)
	}
	time.Sleep(40 * time.Millisecond)
	if got, ok, _ := r2.Get(ctx, "k"); !ok || string(got) != "v2" {
		t.Fatalf("after lease Get = %q,%v, want v2 | 租约到期后读取不符", got, ok)
	}
	f.setDrop(false)
}

func TestFailOpenPolicy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failOpen bool
	}{
		{name: "fail open | 失败开放", failOpen: true},
		{name: "fail closed | 失败关闭", failOpen: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeRemote()
			m := newTestManager(t, f, WithFailOpen(tc.failOpen))
			r, _ := m.Raw("fo")
			ctx := context.Background()
			f.setFailures(true, true)
			_, _, err := r.Get(ctx, "k")
			if tc.failOpen {
				if err != nil {
					t.Fatalf("fail-open Get = %v | 失败开放读取报错", err)
				}
				got, err := r.GetOrLoad(ctx, "k", func(context.Context) ([]byte, error) {
					return []byte("from-loader"), nil
				})
				if err != nil || string(got) != "from-loader" {
					t.Fatalf("fail-open GetOrLoad = %q,%v | 失败开放加载不符", got, err)
				}
			} else {
				if !errors.Is(err, ErrRemote) {
					t.Fatalf("fail-closed Get = %v | 失败关闭读取错误不符", err)
				}
				if _, err := r.GetOrLoad(ctx, "k", func(context.Context) ([]byte, error) {
					return []byte("x"), nil
				}); !errors.Is(err, ErrRemote) {
					t.Fatalf("fail-closed GetOrLoad = %v | 失败关闭加载错误不符", err)
				}
			}
		})
	}
}

func TestSingleflightCollapsesMisses(t *testing.T) {
	f := newFakeRemote()
	m := newTestManager(t, f)
	waitSubscribed(t, f, 1)
	r, _ := m.Raw("sf")
	ctx := context.Background()
	var loads atomic.Int32
	loader := func(context.Context) ([]byte, error) {
		loads.Add(1)
		time.Sleep(10 * time.Millisecond)
		return []byte("value"), nil
	}
	const workers = 64
	var wg sync.WaitGroup
	wg.Add(workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			got, err := r.GetOrLoad(ctx, "k", loader)
			if err != nil {
				errs <- err
				return
			}
			if string(got) != "value" {
				errs <- errors.New("unexpected value | 意外的值")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("worker: %v", err)
	}
	if loads.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1 | loader 调用次数不符", loads.Load())
	}
}

func TestViewErrorsAndMisses(t *testing.T) {
	m := newTestManager(t, nil)
	r, _ := m.Raw("view")
	ctx := context.Background()
	if err := r.Set(ctx, "k", []byte("v")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	sentinel := errors.New("stop")
	found, err := r.View(ctx, "k", func([]byte) error { return sentinel })
	if !found || !errors.Is(err, sentinel) {
		t.Fatalf("View error = %v,%v | 视图未透传回调错误", found, err)
	}
	if found, err := r.View(ctx, "missing", func([]byte) error { return nil }); found || err != nil {
		t.Fatalf("View(missing) = %v,%v | 缺失键视图结果不符", found, err)
	}
	if _, err := r.View(ctx, "k", nil); !errors.Is(err, ErrNilView) {
		t.Fatalf("nil callback = %v | nil 回调错误不符", err)
	}
}

func TestManagerCloseIsIdempotent(t *testing.T) {
	m := newTestManager(t, nil)
	r, _ := m.Raw("close")
	ctx := context.Background()
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v | 关闭失败", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v | 第二次关闭失败", err)
	}
	if err := r.Set(ctx, "k", []byte("v")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Set after Close = %v | 关闭后写入错误不符", err)
	}
	if _, _, err := r.Get(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close = %v | 关闭后读取错误不符", err)
	}
	if _, err := m.Raw("other"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Raw after Close = %v | 关闭后打开命名空间错误不符", err)
	}
}

func TestOversizedValuePolicy(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, nil)
	r, _ := m.Raw("big")
	if err := r.Set(ctx, "k", make([]byte, 128<<10)); !errors.Is(err, local.ErrEntryTooLarge) {
		t.Fatalf("local-only oversized Set = %v | 仅本地超大值错误不符", err)
	}
	f := newFakeRemote()
	m2 := newTestManager(t, f)
	r2, _ := m2.Raw("big")
	value := make([]byte, 128<<10)
	value[0] = 'x'
	if err := r2.Set(ctx, "k", value); err != nil {
		t.Fatalf("two-level oversized Set = %v | 两级超大值写入失败", err)
	}
	if data, _, ok := f.value("big:k"); !ok || len(data) != len(value) || data[0] != 'x' {
		t.Fatalf("L2 did not retain the oversized value | L2 未保留超大值")
	}
}

func TestFreshReadsBypassL1(t *testing.T) {
	f := newFakeRemote()
	m := newTestManager(t, f)
	r, _ := m.Raw("fresh")
	ctx := context.Background()
	if err := r.Set(ctx, "k", []byte("v1")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if _, err := f.Store(ctx, "fresh:k", []byte("v2"), FlagNone, 0); err != nil {
		t.Fatalf("L2 write: %v | 远端写入失败", err)
	}
	if got, ok, _ := r.Get(ctx, "k"); !ok || string(got) != "v1" {
		t.Fatalf("ordinary Get = %q,%v, want the L1 value | 普通读取本应返回 L1 值", got, ok)
	}
	got, ok, err := r.GetFresh(ctx, "k")
	if err != nil || !ok || string(got) != "v2" {
		t.Fatalf("GetFresh = %q,%v,%v, want v2 | 严格读结果不符", got, ok, err)
	}
	if got, _, _ := r.Get(ctx, "k"); string(got) != "v2" {
		t.Fatalf("fresh read did not refresh L1: %q | 严格读未刷新 L1", got)
	}
	if _, err := f.Remove(ctx, "fresh:k", FlagTombstone, time.Minute); err != nil {
		t.Fatalf("L2 remove: %v | 远端删除失败", err)
	}
	if got, ok, _ := r.Get(ctx, "k"); !ok || string(got) != "v2" {
		t.Fatalf("ordinary Get after remote delete = %q,%v | 远端删除后普通读取不符", got, ok)
	}
	if _, ok, err := r.GetFresh(ctx, "k"); err != nil || ok {
		t.Fatalf("GetFresh after remote delete = %v,%v | 远端删除后严格读不符", ok, err)
	}
	if _, ok, _ := r.Get(ctx, "k"); ok {
		t.Fatal("L1 was not invalidated by the fresh read | 严格读未使 L1 失效")
	}
}

func TestTypedFreshRead(t *testing.T) {
	f := newFakeRemote()
	m := newTestManager(t, f)
	c, _ := New[testUser](m, "fresh-typed")
	ctx := context.Background()
	if err := c.Set(ctx, "u", testUser{ID: 1, Name: "old"}); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	body, err := jsonx.Marshal(testUser{ID: 2, Name: "new"})
	if err != nil {
		t.Fatalf("marshal: %v | 序列化失败", err)
	}
	if _, err := f.Store(ctx, "fresh-typed:u", body, FlagNone, 0); err != nil {
		t.Fatalf("L2 write: %v | 远端写入失败", err)
	}
	got, ok, err := c.GetFresh(ctx, "u")
	if err != nil || !ok || got.ID != 2 || got.Name != "new" {
		t.Fatalf("GetFresh = %+v,%v,%v | 严格读结果不符", got, ok, err)
	}
}

func TestThreeNodesConverge(t *testing.T) {
	f := newFakeRemote()
	m1 := newTestManager(t, f)
	m2 := newTestManager(t, f)
	m3 := newTestManager(t, f)
	waitSubscribed(t, f, 3)
	r1, _ := m1.Raw("multi")
	r2, _ := m2.Raw("multi")
	r3, _ := m3.Raw("multi")
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		value := []byte{byte(i)}
		if err := r1.Set(ctx, "k", value); err != nil {
			t.Fatalf("Set: %v | 写入失败", err)
		}
		for _, r := range []*Raw{r1, r2, r3} {
			got, ok, err := r.Get(ctx, "k")
			if err != nil || !ok || len(got) != 1 || got[0] != byte(i) {
				t.Fatalf("round %d Get = %v,%v,%v | 多节点收敛不符", i, got, ok, err)
			}
		}
	}
}

func TestClearPropagatesAcrossNodes(t *testing.T) {
	f := newFakeRemote()
	m1 := newTestManager(t, f)
	m2 := newTestManager(t, f)
	waitSubscribed(t, f, 2)
	r1, _ := m1.Raw("clear")
	r2, _ := m2.Raw("clear")
	ctx := context.Background()
	if err := r1.Set(ctx, "a", []byte("v1")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if got, ok, _ := r2.Get(ctx, "a"); !ok || string(got) != "v1" {
		t.Fatalf("initial Get = %q,%v | 初始读取不符", got, ok)
	}
	loadsBefore, _ := f.counts()
	if err := r1.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v | 清理失败", err)
	}
	if m2.Stats().Invalidations == 0 {
		t.Fatal("m2 did not observe the clear | m2 未观察到清理事件")
	}
	if got, ok, _ := r2.Get(ctx, "a"); !ok || string(got) != "v1" {
		t.Fatalf("Get after clear = %q,%v | 清理后读取不符", got, ok)
	}
	if loadsAfter, _ := f.counts(); loadsAfter <= loadsBefore {
		t.Fatalf("clear did not drop m2's L1 (loads %d -> %d) | 清理未使 m2 的 L1 失效", loadsBefore, loadsAfter)
	}
}

func TestResyncClearsL1AfterGap(t *testing.T) {
	f := newFakeRemote()
	m1 := newTestManager(t, f)
	m2 := newTestManager(t, f)
	waitSubscribed(t, f, 2)
	r1, _ := m1.Raw("gap")
	r2, _ := m2.Raw("gap")
	ctx := context.Background()
	if err := r1.Set(ctx, "k", []byte("v1")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if got, ok, _ := r2.Get(ctx, "k"); !ok || string(got) != "v1" {
		t.Fatalf("initial Get = %q,%v | 初始读取不符", got, ok)
	}
	f.setDrop(true)
	if err := r1.Set(ctx, "k", []byte("v2")); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if got, _, _ := r2.Get(ctx, "k"); string(got) != "v1" {
		t.Fatalf("m2 should still hold v1 before resync, got %q | 重同步前 m2 本应仍是 v1", got)
	}
	m2.resync()
	if got, ok, _ := r2.Get(ctx, "k"); !ok || string(got) != "v2" {
		t.Fatalf("Get after resync = %q,%v | 重同步后读取不符", got, ok)
	}
	f.setDrop(false)
}
