package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// dialRaw opens a raw TCP connection and sends a minimal HTTP/1.1 GET. | dialRaw 建立裸 TCP 连接并发送最小的 HTTP/1.1 GET。
func dialRaw(t *testing.T, addr, path string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("连接服务器失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	req := "GET " + path + " HTTP/1.1\r\nHost: " + addr + "\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("发送请求失败: %v", err)
	}
	return conn
}

// TestShutdownForceClosesHungHijackConnection 是「挂着的 hijack 连接」防回归测试：
// 处理器 hijack 连接后一直阻塞，Shutdown 仍必须在宽限期内返回，并且真的把该连接强制关闭。
// TestShutdownForceClosesHungHijackConnection is the regression guard for a hung hijacked connection:
// the handler hijacks and then blocks forever, yet Shutdown must still return within the grace period
// and must actually force-close that connection.
func TestShutdownForceClosesHungHijackConnection(t *testing.T) {
	const (
		grace = 10 * time.Second
		conns = 8 // 同时挂起的 hijack 连接数 | number of hung hijacked connections
	)

	hijacked := make(chan struct{}, conns)
	readReturned := make(chan struct{}, conns)

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("HTTP/1.1 处理器必须拿到可用的 http.Hijacker")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		// 收到本信号时连接已完成登记（登记发生在 Hijack 返回之前）。| Registration happened before Hijack returned, so this signal proves the connection is tracked.
		hijacked <- struct{}{}
		buf := make([]byte, 1)
		_, _ = conn.Read(buf) // 一直挂着，直到服务器强制关闭 | hangs until the server force-closes it
		readReturned <- struct{}{}
		_ = conn.Close()
	})

	srv := New(Options{Host: "127.0.0.1", Port: 0, Handler: h, ShutdownTimeout: grace})
	if err := srv.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	for i := 0; i < conns; i++ {
		_ = dialRaw(t, srv.Addr(), "/ws")
	}
	for i := 0; i < conns; i++ {
		select {
		case <-hijacked:
		case <-time.After(5 * time.Second):
			t.Fatalf("第 %d 条连接未在 5 秒内完成 hijack", i+1)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	start := time.Now()
	err := srv.Shutdown(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Shutdown 返回错误: %v", err)
	}
	t.Logf("挂着 %d 条 hijack 连接时 Shutdown 实测耗时 = %v（宽限期 %v）", conns, elapsed, grace)
	if elapsed > 3*time.Second {
		t.Fatalf("Shutdown 耗时 %v，存在挂起的 hijack 连接时仍必须在宽限期内返回", elapsed)
	}

	for i := 0; i < conns; i++ {
		select {
		case <-readReturned:
		case <-time.After(3 * time.Second):
			t.Fatalf("Shutdown 之后仍有 hijack 连接未被强制关闭（第 %d 条，对端会一直挂着）", i+1)
		}
	}
	if !srv.Lifecycle().ShuttingDown() {
		t.Fatal("Shutdown 之后 ShuttingDown 必须为 true")
	}
	// 已关闭的连接必须从登记表中移除（读取须持锁，处理器协程仍可能在取消登记）。| A closed connection must leave the registry (read under the lock: the handler goroutine may still be untracking).
	srv.mu.Lock()
	left := len(srv.hijacked)
	srv.mu.Unlock()
	if left != 0 {
		t.Fatalf("hijack 登记表残留 %d 条记录", left)
	}
}

// TestShutdownClosesLongStreamingRequestWithinGrace 验证长连接（SSE 语义）不会把排空拖到宽限期结束：
// lifecycle 钩子先关闭事件流，处理器随即返回，Shutdown 迅速完成。
// TestShutdownClosesLongStreamingRequestWithinGrace verifies a long-lived stream (SSE semantics) does not
// drag the drain out to the grace period: the lifecycle hook closes the stream, the handler returns, and
// Shutdown finishes promptly.
func TestShutdownClosesLongStreamingRequestWithinGrace(t *testing.T) {
	streamClosed := make(chan struct{})
	handlerReturned := make(chan struct{})

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush() // 经 hijackWriter 包装后 Flush 必须依然可用 | Flush must survive the hijackWriter wrapper
			_, _ = io.WriteString(w, ": ping\n\n")
			f.Flush()
		}
		defer close(handlerReturned)
		select {
		case <-streamClosed:
		case <-time.After(30 * time.Second): // 钩子未执行时把排空拖过宽限期 | drags the drain past the grace period when the hook never runs
		}
	})

	srv := New(Options{Host: "127.0.0.1", Port: 0, Handler: h, ShutdownTimeout: 10 * time.Second})
	srv.Lifecycle().Add("stream", func(context.Context) error {
		close(streamClosed)
		return nil
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	// 客户端保持事件流打开。| The client keeps the event stream open.
	resp, err := http.Get("http://" + srv.Addr() + "/events")
	if err != nil {
		t.Fatalf("建立事件流失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	br := bufio.NewReader(resp.Body)
	if _, err := br.ReadString('\n'); err != nil {
		t.Fatalf("读取事件流首帧失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	err = srv.Shutdown(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Shutdown 返回错误: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Shutdown 耗时 %v，长连接未被 lifecycle 钩子及时关闭", elapsed)
	}
	select {
	case <-handlerReturned:
	case <-time.After(3 * time.Second):
		t.Fatal("长连接处理器未在 Shutdown 期间返回")
	}
}

// TestLifecycleDoneClosesBeforeDrainFinishes 验证 ① 步（关闭 Done 信号）先于 ③ 步完成：
// 仍在处理中的请求能够观察到 Done 已关闭，从而及时收尾。
// TestLifecycleDoneClosesBeforeDrainFinishes verifies step ① (closing the Done signal) precedes the end of
// the drain: an in-flight request can observe that Done is closed and finish on time.
func TestLifecycleDoneClosesBeforeDrainFinishes(t *testing.T) {
	inFlight := make(chan struct{})
	observed := make(chan struct{})
	var done <-chan struct{}

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(inFlight)
		select {
		case <-done: // Shutdown 的 ① 步：Done 必须在排空完成之前就关闭 | step ① of Shutdown: Done must close before the drain finishes
			close(observed)
		case <-time.After(5 * time.Second):
		}
		_, _ = io.WriteString(w, "done")
	})

	srv := New(Options{Host: "127.0.0.1", Port: 0, Handler: h, ShutdownTimeout: 10 * time.Second})
	done = srv.Lifecycle().Done()
	if err := srv.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	// 客户端阻塞在请求中，直到 Shutdown 关闭 Done。| The client blocks inside the request until Shutdown closes Done.
	respBody := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + srv.Addr() + "/inflight")
		if err != nil {
			respBody <- ""
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		respBody <- string(b)
	}()

	select {
	case <-inFlight:
	case <-time.After(3 * time.Second):
		t.Fatal("请求未在 3 秒内进入处理器")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown 返回错误: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Shutdown 耗时 %v，Done 信号未能让进行中的请求及时收尾", elapsed)
	}
	select {
	case <-observed:
	case <-time.After(3 * time.Second):
		t.Fatal("进行中的请求未观察到 Done 已关闭")
	}
	if body := <-respBody; body != "done" {
		t.Fatalf("进行中的请求响应 = %q, 期望 done", body)
	}
}

// TestShutdownAggregatesLifecycleHookErrors 验证钩子错误被包装为 ErrShutdownHook 并聚合返回。
// TestShutdownAggregatesLifecycleHookErrors verifies hook errors are wrapped as ErrShutdownHook and aggregated.
func TestShutdownAggregatesLifecycleHookErrors(t *testing.T) {
	hookErr := errors.New("hook exploded")
	srv := New(Options{Host: "127.0.0.1", Port: 0, Handler: protoHandler(), ShutdownTimeout: 5 * time.Second})
	srv.Lifecycle().Add("failing", func(context.Context) error { return hookErr })
	if err := srv.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := srv.Shutdown(ctx)
	if !errors.Is(err, ErrShutdownHook) {
		t.Fatalf("错误 = %v, 期望匹配 ErrShutdownHook", err)
	}
	if !errors.Is(err, hookErr) {
		t.Fatalf("错误 = %v, 期望保留钩子错误", err)
	}
	// 重复调用不得重复执行钩子，也不得再次报错。| A repeated call must not re-run hooks nor report again.
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("重复 Shutdown 返回错误: %v", err)
	}
}

// TestShutdownWithoutStartIsSafe 验证未启动的服务器关闭时是无操作。
// TestShutdownWithoutStartIsSafe verifies shutting down a never-started server is a no-op.
func TestShutdownWithoutStartIsSafe(t *testing.T) {
	srv := New(Options{Host: "127.0.0.1", Port: 0, Handler: protoHandler()})
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown 返回错误: %v", err)
	}
	if err := srv.Shutdown(nil); err != nil {
		t.Fatalf("Shutdown(nil) 返回错误: %v", err)
	}
	if srv.Lifecycle().Done() == nil {
		t.Fatal("Lifecycle 注册表不得为 nil")
	}
}
