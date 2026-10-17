package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shibingli/yd/http/router"
	"github.com/shibingli/yd/runtime/server"
)

// fakeGRPCServer is a GRPCServer whose graceful path can be blocked until Stop is called. | fakeGRPCServer 是一个 GRPCServer，其优雅路径可以被挡住直到 Stop 被调用。
type fakeGRPCServer struct {
	record  func(string)
	gate    chan struct{}
	release sync.Once
	block   bool

	stopped atomic.Bool
}

func (f *fakeGRPCServer) GracefulStop() {
	if f.record != nil {
		f.record("grpc:graceful")
	}
	if f.block {
		<-f.gate
	}
}

func (f *fakeGRPCServer) Stop() {
	if f.record != nil {
		f.record("grpc:stop")
	}
	f.stopped.Store(true)
	if f.block {
		f.release.Do(func() { close(f.gate) })
	}
}

// TestShutdownClosesLongConnectionsBeforeHooks 端到端验证退出顺序：
// 长连接先关，其次 HTTP 排空，然后 gRPC，最后应用关闭钩子。
// TestShutdownClosesLongConnectionsBeforeHooks is an end-to-end check of the shutdown order:
// long-lived connections first, then the HTTP drain, then gRPC, and finally the app hooks.
func TestShutdownClosesLongConnectionsBeforeHooks(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	record := func(name string) {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
	}

	grpc := &fakeGRPCServer{record: record}
	a := newTestApp(t, nil,
		WithServerOptions(func(o *server.Options) {
			o.Host = "127.0.0.1"
			o.Port = 0
			o.ShutdownTimeout = 5 * time.Second
		}),
		WithGRPC(grpc),
	)

	// The long-lived connection (SSE / WebSocket semantics) is closed by the lifecycle hook. | 长连接（SSE / WebSocket 语义）由 lifecycle 钩子关闭。
	streamClosed := make(chan struct{})
	a.Lifecycle().Add("stream", func(context.Context) error {
		record("lifecycle:stream")
		close(streamClosed)
		return nil
	})

	entered := make(chan struct{}, 1)
	a.GET("/stream", func(c *router.Ctx) {
		entered <- struct{}{}
		select {
		case <-streamClosed: // 退出流程关闭长连接后处理器才收尾 | the handler finishes only after the shutdown closes the stream
		case <-time.After(30 * time.Second): // 钩子未执行时会把排空拖过宽限期 | the drain would be dragged past the grace period when the hook never runs
		}
		record("drain:handler")
		_ = c.String(http.StatusOK, "bye")
	})

	a.OnShutdown(func(context.Context) error {
		record("hooks")
		return nil
	})

	if err := a.Server().Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	body := make(chan string, 1)
	go func() {
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Get("http://" + a.Server().Addr() + "/stream")
		if err != nil {
			body <- "error: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		body <- string(b)
	}()

	// Start the shutdown only once the request is inside the handler: a sync signal here, never a sleep-based guess. | 请求已进入处理器后才开始退出：这里用同步信号而非睡眠竞猜。
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not enter the handler within 5 seconds | 请求未在 5 秒内进入处理器")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	start := time.Now()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown 返回错误: %v", err)
	}
	t.Logf("完整退出流程实测耗时 = %v", time.Since(start))

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()

	want := []string{"lifecycle:stream", "drain:handler", "grpc:graceful", "hooks"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("退出顺序 = %v, 期望 %v", got, want)
	}
	if got := <-body; got != "bye" {
		t.Fatalf("长连接响应 = %q, 期望 bye", got)
	}

	// Idempotent: a repeated shutdown must not run any step again. | 幂等：重复关闭不得再执行任何一步。
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("第二次 Shutdown 返回错误: %v", err)
	}
	mu.Lock()
	after := append([]string(nil), order...)
	mu.Unlock()
	if strings.Join(after, ",") != strings.Join(want, ",") {
		t.Fatalf("第二次 Shutdown 后顺序 = %v, 期望不变 %v", after, want)
	}
}

// TestShutdownCollectsEveryStageError 验证各阶段错误都被聚合，且 ctx 耗尽不会阻止后续阶段。
// TestShutdownCollectsEveryStageError verifies every stage error is joined and an exhausted ctx never blocks later stages.
func TestShutdownCollectsEveryStageError(t *testing.T) {
	hookErr := errors.New("lifecycle hook failed")
	appErr := errors.New("app hook failed")

	// The grace period is too short for the gRPC graceful stop to finish. | 宽限期短到不足以让 gRPC 优雅停止完成。
	grpc := &fakeGRPCServer{gate: make(chan struct{}), block: true}
	a := newTestApp(t, nil, WithGRPC(grpc))

	a.Lifecycle().Add("conn", func(context.Context) error { return hookErr })

	hooksRan := false
	a.OnShutdown(func(context.Context) error {
		hooksRan = true
		return appErr
	})

	// The server never started: the drain stage returns immediately and yields no error. | 服务器未启动：排空阶段立即返回，不产生错误。
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := a.Shutdown(ctx)
	elapsed := time.Since(start)

	for _, want := range []error{hookErr, appErr, ErrShutdown, ErrGRPCShutdown, context.DeadlineExceeded} {
		if !errors.Is(err, want) {
			t.Errorf("Shutdown 错误 = %v, 期望包含 %v", err, want)
		}
	}
	if !hooksRan {
		t.Error("the app shutdown hook must still run after the gRPC stage exhausts the grace period | gRPC 阶段耗尽宽限期后，应用关闭钩子仍必须执行")
	}
	if !grpc.stopped.Load() {
		t.Error("gRPC must be force-stopped once the grace period is over | 宽限期过后必须强制停止 gRPC")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Shutdown 耗时 %v, 必须在宽限期附近返回", elapsed)
	}
}

// TestShutdownWithoutRunIsIdempotent 验证未启动时的退出：钩子与 io.Closer 服务各执行一次。
// TestShutdownWithoutRunIsIdempotent verifies shutting down without Run: hooks and io.Closer services run exactly once.
func TestShutdownWithoutRunIsIdempotent(t *testing.T) {
	a := newTestApp(t, nil)

	closer := &fakeCloser{}
	a.Set(ServiceRedis, closer)

	hookRuns := 0
	a.OnShutdown(func(context.Context) error { hookRuns++; return nil })
	connRuns := 0
	a.Lifecycle().Add("conn", func(context.Context) error { connRuns++; return nil })

	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown 返回错误: %v", err)
	}
	if hookRuns != 1 || connRuns != 1 || !closer.closed {
		t.Fatalf("首次退出: hookRuns=%d connRuns=%d closed=%v, 期望各执行一次", hookRuns, connRuns, closer.closed)
	}

	if err := a.Shutdown(nil); err != nil {
		t.Fatalf("Shutdown(nil) 返回错误: %v", err)
	}
	if hookRuns != 1 || connRuns != 1 {
		t.Fatalf("重复退出: hookRuns=%d connRuns=%d, 期望仍为 1", hookRuns, connRuns)
	}
}

// TestRunRunsLifecycleHooksOnExit 验证 Run 走的是完整退出流程（lifecycle 钩子同样被执行）。
// TestRunRunsLifecycleHooksOnExit verifies Run takes the full shutdown path (lifecycle hooks run as well).
func TestRunRunsLifecycleHooksOnExit(t *testing.T) {
	a := newTestApp(t, nil, WithServerOptions(func(o *server.Options) {
		o.Host = "127.0.0.1"
		o.Port = 0
	}))
	a.GET("/health", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })

	lifecycleRan := make(chan struct{})
	a.Lifecycle().Add("conn", func(context.Context) error {
		close(lifecycleRan)
		return nil
	})
	hookRan := make(chan struct{})
	a.OnShutdown(func(context.Context) error {
		close(hookRan)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- a.Run(ctx) }()

	waitReady(t, a)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run 返回错误: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after ctx was cancelled | ctx 取消后 Run 未返回")
	}

	for _, ch := range []struct {
		ch   chan struct{}
		name string
	}{{lifecycleRan, "lifecycle 钩子"}, {hookRan, "应用关闭钩子"}} {
		select {
		case <-ch.ch:
		default:
			t.Errorf("Run 返回前必须执行 %s", ch.name)
		}
	}
}
