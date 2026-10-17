package lifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestShutdownRunsHooksInReverseOrder(t *testing.T) {
	r := NewRegistry()
	var order []string
	var mu sync.Mutex

	record := func(name string) ShutdownFunc {
		return func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, name)
			return nil
		}
	}

	r.Add("database", record("database"))
	r.Add("redis", record("redis"))
	r.Add("websocket", record("websocket"))

	if got := r.Len(); got != 3 {
		t.Fatalf("Len = %d, 期望 3", got)
	}
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown 返回错误: %v", err)
	}

	want := []string{"websocket", "redis", "database"}
	if len(order) != len(want) {
		t.Fatalf("执行顺序 = %v, 期望 %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("执行顺序 = %v, 期望 %v", order, want)
		}
	}
	if got := r.Len(); got != 0 {
		t.Fatalf("关闭后 Len = %d, 期望 0", got)
	}
}

func TestShutdownIsIdempotent(t *testing.T) {
	r := NewRegistry()
	var calls int
	r.Add("once", func(context.Context) error {
		calls++
		return nil
	})

	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatalf("首次 Shutdown 返回错误: %v", err)
	}
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatalf("重复 Shutdown 返回错误: %v", err)
	}
	if calls != 1 {
		t.Fatalf("钩子执行次数 = %d, 期望 1（幂等）", calls)
	}
}

func TestConcurrentShutdownRunsHooksOnceAndSharesError(t *testing.T) {
	r := NewRegistry()
	wantErr := errors.New("boom")
	var calls int
	var mu sync.Mutex
	started := make(chan struct{})

	r.Add("slow", func(context.Context) error {
		close(started)
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		calls++
		mu.Unlock()
		return wantErr
	})

	const goroutines = 8
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx > 0 {
				<-started // 让并发调用与首次关闭重叠 | overlap the concurrent calls with the first shutdown
			}
			errs[idx] = r.Shutdown(context.Background())
		}(i)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("钩子执行次数 = %d, 期望 1", calls)
	}
	for i, err := range errs {
		if !errors.Is(err, ErrHookFailed) || !errors.Is(err, wantErr) {
			t.Fatalf("第 %d 个调用的错误 = %v, 期望同时匹配 ErrHookFailed 与底层错误", i, err)
		}
	}
	if !errors.Is(r.Err(), wantErr) {
		t.Fatalf("Err() = %v, 期望匹配底层错误", r.Err())
	}
}

func TestShutdownHonorsContextDeadline(t *testing.T) {
	r := NewRegistry()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	// Reverse order: the later wedged runs first and eats the grace period, so never-runs is skipped. | 逆序执行：后注册的 wedged 先跑并吃满宽限期，先注册的 never-runs 因而被跳过。
	r.Add("never-runs", func(context.Context) error {
		t.Error("the grace period is over, no later hook may run | 宽限期已过，后续钩子不应被执行")
		return nil
	})
	r.Add("wedged", func(context.Context) error {
		<-release // 故意忽略 ctx，模拟无法中断的钩子 | deliberately ignores ctx, simulating an uninterruptible hook
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := r.Shutdown(ctx)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Fatalf("Shutdown 耗时 %v，宽限期到点必须立即返回", elapsed)
	}
	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("错误 = %v, 期望 ErrShutdownTimeout", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("错误 = %v, 期望保留 context.DeadlineExceeded", err)
	}
}

func TestShutdownRecoversHookPanic(t *testing.T) {
	r := NewRegistry()
	ran := false
	r.Add("panics", func(context.Context) error { panic("hook exploded") })
	r.Add("survives", func(context.Context) error {
		ran = true
		return nil
	})

	err := r.Shutdown(context.Background())
	if !errors.Is(err, ErrHookPanic) {
		t.Fatalf("错误 = %v, 期望 ErrHookPanic（panic 必须被恢复并记为错误）", err)
	}
	if !ran {
		t.Fatal("a panicking hook must not stop the remaining hooks | panic 的钩子不得阻止其余钩子执行")
	}
}

func TestShutdownJoinsEveryHookError(t *testing.T) {
	r := NewRegistry()
	errA := errors.New("A")
	errB := errors.New("B")
	r.Add("a", func(context.Context) error { return errA })
	r.Add("b", func(context.Context) error { return errB })

	err := r.Shutdown(context.Background())
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Fatalf("聚合错误 = %v, 期望同时包含 A 与 B", err)
	}
	if !errors.Is(err, ErrHookFailed) {
		t.Fatalf("聚合错误 = %v, 期望匹配 ErrHookFailed", err)
	}
}

func TestDoneClosesBeforeFirstHook(t *testing.T) {
	r := NewRegistry()
	if r.ShuttingDown() {
		t.Fatal("ShuttingDown must be false before shutdown starts | 尚未关闭时 ShuttingDown 应为 false")
	}
	select {
	case <-r.Done():
		t.Fatal("Done must not be closed before shutdown starts | 尚未关闭时 Done 不应关闭")
	default:
	}

	running := make(chan struct{})
	release := make(chan struct{})
	r.Add("blocking", func(context.Context) error {
		close(running)
		<-release
		return nil
	})

	done := make(chan error, 1)
	go func() { done <- r.Shutdown(context.Background()) }()

	<-running
	if !r.ShuttingDown() {
		t.Fatal("ShuttingDown must be true while shutdown is in progress | 关闭进行中 ShuttingDown 应为 true")
	}
	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("Done must close as soon as shutdown begins | 关闭开始后 Done 必须立即关闭")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Shutdown 返回错误: %v", err)
	}
}

func TestRemoveIsIdempotentAndNameCollisionReplaces(t *testing.T) {
	r := NewRegistry()
	first := 0
	second := 0
	removeFirst := r.Add("ws", func(context.Context) error { first++; return nil })

	// Same name replaces: only the later registration runs. | 同名覆盖：只有后注册者生效。
	removeSecond := r.Add("ws", func(context.Context) error { second++; return nil })
	if got := r.Len(); got != 1 {
		t.Fatalf("同名覆盖后 Len = %d, 期望 1", got)
	}

	removeFirst() // 被覆盖的身份已消失，此时是无操作 | the replaced identity is gone, so this is a no-op
	removeFirst()
	if got := r.Len(); got != 1 {
		t.Fatalf("被覆盖的 remove 不应移除当前钩子，Len = %d", got)
	}

	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown 返回错误: %v", err)
	}
	if first != 0 || second != 1 {
		t.Fatalf("执行次数 first=%d second=%d, 期望 0/1", first, second)
	}

	removeSecond()
	if got := r.Len(); got != 0 {
		t.Fatalf("Len = %d, 期望 0", got)
	}
}

func TestAddAfterShutdownIsNoop(t *testing.T) {
	r := NewRegistry()
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatalf("空注册表 Shutdown 返回错误: %v", err)
	}

	ran := false
	remove := r.Add("late", func(context.Context) error { ran = true; return nil })
	remove()
	if ran {
		t.Fatal("a hook registered after shutdown finished must not run | 关闭完成后登记的钩子不应被执行")
	}
	if got := r.Len(); got != 0 {
		t.Fatalf("关闭完成后 Len = %d, 期望 0", got)
	}
}

func TestNilHookAndNilContext(t *testing.T) {
	r := NewRegistry()
	remove := r.Add("nil", nil)
	remove()
	if got := r.Len(); got != 0 {
		t.Fatalf("nil 钩子不应登记，Len = %d", got)
	}
	// nil ctx 按 context.Background 处理：关闭路径宁可容忍也不能 panic。
	// A nil ctx is treated as context.Background: the shutdown path must tolerate it rather than panic.
	// 用显式 nil 变量传参是刻意的——要测的就是这个值本身。
	// Passing an explicit nil variable is deliberate: that exact value is what is under test.
	var nilCtx context.Context
	if err := r.Shutdown(nilCtx); err != nil {
		t.Fatalf("Shutdown(nil) 返回错误: %v", err)
	}
}

func TestConcurrentAddDuringShutdown(t *testing.T) {
	r := NewRegistry()
	const workers = 16
	var wg sync.WaitGroup

	stop := make(chan struct{})
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; ; j++ {
				select {
				case <-stop:
					return
				default:
				}
				remove := r.Add("hook", func(context.Context) error { return nil })
				remove()
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 20 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = r.Shutdown(ctx)
			cancel()
		}
		close(stop)
	}()

	wg.Wait()
	if got := r.Len(); got != 0 {
		t.Fatalf("并发压测后 Len = %d, 期望 0（登记的钩子必须被执行或移除）", got)
	}
}

func TestWaitForRunningShutdownHonorsContext(t *testing.T) {
	r := NewRegistry()
	wedged := make(chan struct{})
	release := make(chan struct{})

	// This hook ignores ctx on purpose: only the first shutdown waits out its grace period. | 该钩子故意忽略 ctx：只有先发起的那次关闭会等到宽限期结束。
	r.Add("wedged", func(context.Context) error {
		close(wedged)
		<-release
		return nil
	})

	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFirst()
	go func() { _ = r.Shutdown(firstCtx) }()

	select {
	case <-wedged:
	case <-time.After(3 * time.Second):
		t.Fatal("the first shutdown did not start its hook within 3 seconds | 首次关闭未在 3 秒内启动钩子")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := r.Shutdown(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("错误 = %v, 期望匹配 ErrShutdownTimeout", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("错误 = %v, 期望保留 context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("等待进行中的关闭耗时 %v, 必须受本次宽限期约束", elapsed)
	}

	close(release)
}
