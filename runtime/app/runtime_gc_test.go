package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/runtime/server"
	"github.com/shibingli/ydx/config"
	"github.com/shibingli/ydx/gc"
	"github.com/shibingli/ydx/log"
	"github.com/shibingli/ydx/router"
)

// loadAppConfig builds an App configuration from an in-memory YAML fixture. | loadAppConfig
// 从内存 YAML 夹具构建 App 配置。
func loadAppConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()

	mem := afero.NewMemMapFs()
	if yaml != "" {
		if err := afero.WriteFile(mem, "config.yaml", []byte(yaml), 0o644); err != nil {
			t.Fatalf("write fixture: %v | 写入夹具失败", err)
		}
	}
	cfg, err := config.Load(config.Options{
		Name:       "app",
		ConfigDir:  ".",
		ConfigName: "config",
		EnvPrefix:  "YDAPPTEST",
		EnvFile:    "absent.env",
		FS:         mem,
	})
	if err != nil {
		t.Fatalf("config.Load: %v | 加载配置失败", err)
	}
	return cfg
}

// discardAppLogger keeps scheduler records out of the test output. | discardAppLogger 让
// 调度器记录不进入测试输出。
func discardAppLogger() *log.Logger {
	return log.New(log.Options{Writer: io.Discard})
}

func TestRuntimeGCWiredByDefault(t *testing.T) {
	a := newTestApp(t, nil)
	if a.RuntimeGC() == nil {
		t.Fatal("New must always wire the runtime GC scheduler | New 必须始终装配运行时 GC 调度器")
	}
	if !a.RuntimeGC().Enabled() {
		t.Fatal("the balanced default must enable scheduling | balanced 默认必须开启调度")
	}
	if a.RuntimeGC().Mode() != gc.ModeRelease {
		t.Fatalf("mode = %q, want release | 默认模式应为 release", a.RuntimeGC().Mode())
	}
}

func TestRuntimeGCRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"unknown strategy", "gc:\n  strategy: turbo\n"},
		{"negative inflight gate", "gc:\n  max_inflight: -1\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(WithConfig(loadAppConfig(t, tc.yaml)), WithLogger(discardAppLogger()))
			if !errors.Is(err, ErrRuntimeGC) {
				t.Fatalf("New error = %v, want ErrRuntimeGC | 非法 GC 配置必须让启动失败", err)
			}
		})
	}
}

func TestRuntimeGCCollectsWhileRunning(t *testing.T) {
	cfg := loadAppConfig(t, `
gc:
  enabled: true
  interval: 100ms
  sample_interval: 10ms
  quiet_window: 0s
  load_threshold: 0
  min_heap: 0
  pressure_ratio: 0
  jitter: 0
  adaptive: false
`)
	a, err := New(
		WithConfig(cfg),
		WithLogger(discardAppLogger()),
		WithServerOptions(func(o *server.Options) {
			o.Host = "127.0.0.1"
			o.Port = 0
		}),
	)
	if err != nil {
		t.Fatalf("New: %v | 构造应用失败", err)
	}
	a.GET("/health", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- a.Run(ctx) }()

	waitReady(t, a)
	waitForRuntimeGC(t, a, 5*time.Second, func(s gc.Stats) bool { return s.Runs >= 1 })

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v | 运行返回错误", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation | 取消后 Run 未返回")
	}

	// The shutdown hook must have stopped the scheduler; a second Stop is a no-op. | 关闭钩子
	// 必须已停止调度器；再次 Stop 是无操作。
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := a.RuntimeGC().Stop(stopCtx); err != nil {
		t.Fatalf("Stop after Run: %v | 运行结束后停止应成功", err)
	}
}

// waitForRuntimeGC polls the scheduler statistics. | waitForRuntimeGC 轮询调度器统计。
func waitForRuntimeGC(t *testing.T, a *App, timeout time.Duration, cond func(gc.Stats) bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond(a.RuntimeGC().Stats()) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runtime GC condition was not met within %v: %+v | 运行时 GC 条件未在期限内成立", timeout, a.RuntimeGC().Stats())
}
