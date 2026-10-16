package asynqx_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"

	"github.com/yun-dev/yd/data/asynqx"
	"github.com/yun-dev/yd/logx"
)

// timeout bounds every wait for asynchronous work in this file. | timeout 限制本文件中所有异步等待的上限。
const timeout = 10 * time.Second

// syncBuf is a goroutine-safe io.Writer used as the logx sink. | syncBuf 是并发安全的 io.Writer，用作 logx 输出端。
//
// The server logs from worker goroutines while the test goroutine reads the sink, | 服务端在工作 goroutine 中写日志，测试 goroutine 同时读取输出端，
// so the buffer needs its own lock to stay -race clean. | 因此缓冲必须自带锁才能在 -race 下干净通过。
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p to the buffer under the lock. | Write 在锁保护下把 p 追加到缓冲。
func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns the buffered log text under the lock. | String 在锁保护下返回已缓冲的日志文本。
func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newTestLogger returns a logx logger writing into a fresh sink. | newTestLogger 返回写入新输出端的 logx 日志器。
func newTestLogger() (*logx.Logger, *syncBuf) {
	sink := &syncBuf{}
	return logx.New(logx.Options{Writer: sink, Service: "asynqx-test"}), sink
}

// redisOpt builds the asynq connection option for mr. | redisOpt 构建指向 mr 的 asynq 连接参数。
func redisOpt(mr *miniredis.Miniredis) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: mr.Addr()}
}

// testOptions returns options with one default queue, the framework fallback shape. | testOptions 返回单 default 队列的选项，即框架兜底形态。
func testOptions() asynqx.Options {
	return asynqx.Options{
		Queues:          map[string]int{"default": 1},
		Concurrency:     2,
		ShutdownTimeout: 5 * time.Second,
	}
}

// newTestClient builds a client for mr and closes it when the test ends. | newTestClient 构建指向 mr 的客户端，并在测试结束时关闭。
func newTestClient(t *testing.T, mr *miniredis.Miniredis, o asynqx.Options) *asynqx.Client {
	t.Helper()
	cli, err := asynqx.NewClient(redisOpt(mr), o)
	if err != nil {
		t.Fatalf("NewClient 失败: %v", err)
	}
	t.Cleanup(func() {
		if err := cli.Close(); err != nil {
			t.Errorf("关闭客户端失败: %v", err)
		}
	})
	return cli
}

// newInspector builds an inspector for mr and closes it when the test ends. | newInspector 构建指向 mr 的检查器，并在测试结束时关闭。
func newInspector(t *testing.T, cli *asynqx.Client) *asynq.Inspector {
	t.Helper()
	insp := cli.Inspector()
	t.Cleanup(func() {
		if err := insp.Close(); err != nil {
			t.Errorf("关闭检查器失败: %v", err)
		}
	})
	return insp
}

// TestRegisterAndHandleTaskEndToEnd asserts an enqueued payload reaches its handler and completes. | TestRegisterAndHandleTaskEndToEnd 断言入队载荷抵达处理器并最终完成。
func TestRegisterAndHandleTaskEndToEnd(t *testing.T) {
	mr := miniredis.RunT(t)
	o := testOptions()
	o.Queues = nil // 走 Options 的兜底队列，避免只测「显式配置」这一条路径 | exercise the fallback queue, not just the explicit config path

	log, sink := newTestLogger()
	reg := asynqx.NewRegistry()
	payloads := make(chan asynqx.Payload, 1)
	reg.Register("email:send", func(_ context.Context, p asynqx.Payload) error {
		payloads <- p
		return nil
	})

	srv := asynqx.NewServer(reg, redisOpt(mr), o, log)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(srv.Shutdown)

	cli := newTestClient(t, mr, o)
	ctx := context.Background()
	info, err := cli.Enqueue(ctx, "email:send", map[string]any{"to": "a@b.c", "count": 3})
	if err != nil {
		t.Fatalf("Enqueue 失败: %v", err)
	}
	if info.ID == "" || info.Queue != "default" || info.Type != "email:send" {
		t.Fatalf("TaskInfo = %+v，期望 default 队列的 email:send 任务", info)
	}

	select {
	case p := <-payloads:
		if p["to"] != "a@b.c" {
			t.Fatalf("处理器收到 payload = %+v，期望 to=a@b.c", p)
		}
		if p["count"] != float64(3) {
			t.Fatalf("处理器收到 payload[count] = %#v，期望 JSON 数字 3", p["count"])
		}
	case <-time.After(timeout):
		t.Fatal("等待处理器超时")
	}

	insp := newInspector(t, cli)
	waitForState(t, insp, "default", info.ID, asynq.TaskStateCompleted)
	if !strings.Contains(sink.String(), "task completed | 任务处理完成") {
		t.Fatalf("日志缺少完成记录: %s", sink.String())
	}
}

// TestUnknownTaskTypeReturnsError asserts an unregistered task type yields a declared sentinel. | TestUnknownTaskTypeReturnsError 断言未注册的任务类型返回已声明的哨兵错误。
func TestUnknownTaskTypeReturnsError(t *testing.T) {
	mr := miniredis.RunT(t)
	log, _ := newTestLogger()
	srv := asynqx.NewServer(asynqx.NewRegistry(), redisOpt(mr), testOptions(), log)

	err := srv.ProcessTask(context.Background(), asynq.NewTask("nobody:home", []byte(`{}`)))
	if !errors.Is(err, asynqx.ErrUnknownTaskType) {
		t.Fatalf("ProcessTask 错误 = %v，期望 errors.Is(err, ErrUnknownTaskType)", err)
	}
}

// TestTaskContextInjectedWithTraceID asserts the trace id travels with the task and lands in the handler context and logs. | TestTaskContextInjectedWithTraceID 断言 trace id 随任务传递，并同时进入处理器上下文与日志。
func TestTaskContextInjectedWithTraceID(t *testing.T) {
	mr := miniredis.RunT(t)
	o := testOptions()
	log, sink := newTestLogger()

	type seen struct {
		tc  *asynqx.TaskContext
		tid string
	}
	got := make(chan seen, 1)
	reg := asynqx.NewRegistry()
	reg.Register("trace:probe", func(ctx context.Context, _ asynqx.Payload) error {
		tc := asynqx.FromContext(ctx)
		l := asynqx.LoggerFrom(ctx)
		tid, _ := asynq.GetTaskID(ctx)
		if l == nil {
			l = logx.Default()
		}
		l.Info("handler observed task | 处理器已观察到任务", logx.Str("probe", "ok"))
		got <- seen{tc: tc, tid: tid}
		return nil
	})

	srv := asynqx.NewServer(reg, redisOpt(mr), o, log)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(srv.Shutdown)

	cli := newTestClient(t, mr, o)
	ctx := asynqx.WithTraceID(context.Background(), "trace-xyz")
	if got := asynqx.TraceIDFrom(ctx); got != "trace-xyz" {
		t.Fatalf("TraceIDFrom = %q，期望 trace-xyz", got)
	}
	info, err := cli.Enqueue(ctx, "trace:probe", map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("Enqueue 失败: %v", err)
	}

	select {
	case s := <-got:
		if s.tc == nil {
			t.Fatal("处理器上下文中没有 TaskContext")
		}
		if s.tc.TraceID != "trace-xyz" {
			t.Fatalf("TaskContext.TraceID = %q，期望 trace-xyz", s.tc.TraceID)
		}
		if s.tc.Type != "trace:probe" || s.tc.Queue != "default" {
			t.Fatalf("TaskContext = %+v，期望 type=trace:probe queue=default", s.tc)
		}
		if s.tc.TaskID != info.ID || s.tid != info.ID {
			t.Fatalf("TaskContext.TaskID = %q / asynq task id = %q，期望 %q", s.tc.TaskID, s.tid, info.ID)
		}
		if s.tc.RetryCount != 0 || s.tc.MaxRetry <= 0 {
			t.Fatalf("TaskContext 重试信息 = (%d, %d)，期望首次执行且 MaxRetry > 0", s.tc.RetryCount, s.tc.MaxRetry)
		}
	case <-time.After(timeout):
		t.Fatal("等待处理器超时")
	}

	// trace id 也写入 asynq 任务头，跨进程/跨服务可继续传递。 | The trace id is also written into the asynq task headers so it survives process hops.
	insp := newInspector(t, cli)
	taskInfo, err := insp.GetTaskInfo("default", info.ID)
	if err != nil {
		t.Fatalf("GetTaskInfo 失败: %v", err)
	}
	if taskInfo.Headers["trace_id"] != "trace-xyz" {
		t.Fatalf("任务头 trace_id = %q，期望 trace-xyz", taskInfo.Headers["trace_id"])
	}

	// 注入的子 logger 带任务字段，日志可按 task_id / trace_id 串联。 | The injected child logger carries task fields, so logs can be correlated by task_id / trace_id.
	out := sink.String()
	for _, want := range []string{"trace-xyz", "trace_id", "task_id", info.ID} {
		if !strings.Contains(out, want) {
			t.Fatalf("日志缺少 %q: %s", want, out)
		}
	}
}

// TestHandlerPanicRecoveredAndRetried asserts a panicking handler becomes an error and is retried by asynq. | TestHandlerPanicRecoveredAndRetried 断言 panic 的处理器被转为错误并由 asynq 重试。
func TestHandlerPanicRecoveredAndRetried(t *testing.T) {
	mr := miniredis.RunT(t)
	o := testOptions()
	o.Queues = map[string]int{"critical": 1}
	o.RetryDelayFunc = func(int, error, *asynq.Task) time.Duration { return time.Millisecond }

	t.Run("panic 转为错误", func(t *testing.T) {
		log, sink := newTestLogger()
		reg := asynqx.NewRegistry()
		reg.Register("panic:direct", func(context.Context, asynqx.Payload) error {
			panic("boom")
		})
		srv := asynqx.NewServer(reg, redisOpt(mr), o, log)

		err := srv.ProcessTask(context.Background(), asynq.NewTask("panic:direct", []byte(`{}`)))
		if !errors.Is(err, asynqx.ErrHandlerPanic) {
			t.Fatalf("panic 后 ProcessTask 错误 = %v，期望 errors.Is(err, ErrHandlerPanic)", err)
		}
		if !strings.Contains(sink.String(), "handler panicked | 处理器发生 panic") {
			t.Fatalf("日志缺少 panic 记录: %s", sink.String())
		}
	})

	t.Run("panic 后重试", func(t *testing.T) {
		log, _ := newTestLogger()
		var calls atomic.Int32
		done := make(chan struct{})
		reg := asynqx.NewRegistry()
		reg.Register("panic:retry", func(context.Context, asynqx.Payload) error {
			if calls.Add(1) < 2 {
				panic("retry me")
			}
			close(done)
			return nil
		})
		srv := asynqx.NewServer(reg, redisOpt(mr), o, log)
		if err := srv.Start(); err != nil {
			t.Fatalf("Start 失败: %v", err)
		}
		t.Cleanup(srv.Shutdown)

		cli := newTestClient(t, mr, o)
		info, err := cli.Enqueue(context.Background(), "panic:retry", nil,
			asynq.Queue("critical"), asynq.MaxRetry(3))
		if err != nil {
			t.Fatalf("Enqueue 失败: %v", err)
		}

		select {
		case <-done:
		case <-time.After(timeout):
			t.Fatal("等待重试后成功超时")
		}
		if n := calls.Load(); n < 2 {
			t.Fatalf("处理器调用 %d 次，期望 panic 后至少重试一次", n)
		}
		insp := newInspector(t, cli)
		waitForState(t, insp, "critical", info.ID, asynq.TaskStateCompleted)
	})
}

// TestShutdownStopsServer asserts Run returns on context cancellation, the workers stop and Shutdown stays idempotent. | TestShutdownStopsServer 断言上下文取消后 Run 返回、工作协程停止，且 Shutdown 幂等。
func TestShutdownStopsServer(t *testing.T) {
	mr := miniredis.RunT(t)
	o := testOptions()
	log, _ := newTestLogger()

	handled := make(chan struct{}, 4)
	reg := asynqx.NewRegistry()
	reg.Register("drain:probe", func(context.Context, asynqx.Payload) error {
		handled <- struct{}{}
		return nil
	})

	srv := asynqx.NewServer(reg, redisOpt(mr), o, log)
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	cli := newTestClient(t, mr, o)
	insp := newInspector(t, cli)
	first, err := cli.Enqueue(context.Background(), "drain:probe", nil)
	if err != nil {
		t.Fatalf("首次 Enqueue 失败: %v", err)
	}
	select {
	case <-handled:
	case <-time.After(timeout):
		cancel()
		t.Fatal("等待服务启动并处理任务超时")
	}
	waitForState(t, insp, "default", first.ID, asynq.TaskStateCompleted)

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run 返回错误: %v", err)
		}
	case <-time.After(timeout):
		t.Fatal("取消上下文后 Run 未返回")
	}

	// 关闭后入队的任务不再被消费，停留在 pending。 | Tasks enqueued after shutdown stay pending instead of being consumed.
	second, err := cli.Enqueue(context.Background(), "drain:probe", nil)
	if err != nil {
		t.Fatalf("关闭后 Enqueue 失败: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	info, err := insp.GetTaskInfo("default", second.ID)
	if err != nil {
		t.Fatalf("GetTaskInfo 失败: %v", err)
	}
	if info.State != asynq.TaskStatePending {
		t.Fatalf("关闭后任务状态 = %v，期望 pending", info.State)
	}

	// Shutdown 幂等：重复调用不 panic、不阻塞。 | Shutdown is idempotent: repeating it neither panics nor blocks.
	shutdownDone := make(chan struct{})
	go func() {
		srv.Shutdown()
		srv.Shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
	case <-time.After(timeout):
		t.Fatal("重复 Shutdown 阻塞")
	}
}

// TestEnqueueDelayOptions asserts EnqueueIn / EnqueueAt schedule instead of running immediately. | TestEnqueueDelayOptions 断言 EnqueueIn / EnqueueAt 走调度而非立即执行。
func TestEnqueueDelayOptions(t *testing.T) {
	mr := miniredis.RunT(t)
	o := testOptions()
	cli := newTestClient(t, mr, o)
	insp := newInspector(t, cli)
	ctx := context.Background()

	in, err := cli.EnqueueIn(ctx, "later:run", map[string]any{"n": 1}, time.Hour)
	if err != nil {
		t.Fatalf("EnqueueIn 失败: %v", err)
	}
	at, err := cli.EnqueueAt(ctx, "later:run", map[string]any{"n": 2}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("EnqueueAt 失败: %v", err)
	}
	for _, id := range []string{in.ID, at.ID} {
		info, err := insp.GetTaskInfo("default", id)
		if err != nil {
			t.Fatalf("GetTaskInfo(%s) 失败: %v", id, err)
		}
		if info.State != asynq.TaskStateScheduled {
			t.Fatalf("任务 %s 状态 = %v，期望 scheduled", id, info.State)
		}
	}
}

// TestEnqueueValidation asserts client-side validation reports declared sentinels. | TestEnqueueValidation 断言客户端校验返回已声明的哨兵错误。
func TestEnqueueValidation(t *testing.T) {
	mr := miniredis.RunT(t)
	o := testOptions()
	cli := newTestClient(t, mr, o)
	ctx := context.Background()

	if _, err := cli.Enqueue(ctx, "  ", nil); !errors.Is(err, asynqx.ErrEmptyTaskType) {
		t.Fatalf("空任务类型错误 = %v，期望 ErrEmptyTaskType", err)
	}
	if _, err := cli.Enqueue(ctx, "bad:payload", make(chan int)); !errors.Is(err, asynqx.ErrEncodeFailed) {
		t.Fatalf("不可序列化载荷错误 = %v，期望 ErrEncodeFailed", err)
	}

	// 连接参数直接取自 Options 的兜底字段。 | Connection parameters fall back to the Options fields.
	fallback, err := asynqx.NewClient(asynq.RedisClientOpt{}, asynqx.Options{RedisAddr: mr.Addr()})
	if err != nil {
		t.Fatalf("Options 兜底地址 NewClient 失败: %v", err)
	}
	defer func() { _ = fallback.Close() }()
	if _, err := fallback.Enqueue(ctx, "fallback:run", nil); err != nil {
		t.Fatalf("Options 兜底地址 Enqueue 失败: %v", err)
	}

	if _, err := asynqx.NewClient(asynq.RedisClientOpt{}, asynqx.Options{}); !errors.Is(err, asynqx.ErrMissingRedisAddr) {
		t.Fatalf("缺失地址错误 = %v，期望 ErrMissingRedisAddr", err)
	}
	if _, err := asynqx.NewClient(asynq.RedisClientOpt{Addr: "redis.example.com"}, o); !errors.Is(err, asynqx.ErrInvalidRedisAddr) {
		t.Fatalf("非法地址错误 = %v，期望 ErrInvalidRedisAddr", err)
	}
}

// waitForState polls the inspector until the task reaches want. | waitForState 轮询检查器直到任务达到 want 状态。
func waitForState(t *testing.T, insp *asynq.Inspector, queue, id string, want asynq.TaskState) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last asynq.TaskState
	var lastErr string
	for time.Now().Before(deadline) {
		info, err := insp.GetTaskInfo(queue, id)
		if err == nil {
			last = info.State
			lastErr = info.LastErr
			if info.State == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("任务 %s 状态 = %v（LastErr=%q），期望 %v", id, last, lastErr, want)
}
