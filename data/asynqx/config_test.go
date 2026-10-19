package asynqx_test

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/config"
	"github.com/shibingli/yd/data/asynqx"
)

// loadConfig builds a config instance from an in-memory YAML fixture. | loadConfig 从内存 YAML 夹具构建配置实例。
//
// EnvPrefix is test-specific so a developer's real YD_ASYNQ_* variables cannot | EnvPrefix 使用测试专用前缀，隔离开发机上真实的 YD_ASYNQ_* 环境变量；
// leak into the assertions, and absent.env keeps the repository .env out of the picture. | absent.env 同样避免仓库根的 .env 被读入。
func loadConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	mem := afs.NewMem()
	if yaml != "" {
		if err := afero.WriteFile(mem, "config.yaml", []byte(yaml), 0o644); err != nil {
			t.Fatalf("写入内存配置文件失败: %v", err)
		}
	}
	c, err := config.Load(config.Options{
		Name:       "asynqx",
		ConfigDir:  ".",
		ConfigName: "config",
		EnvPrefix:  "YDASYNQXTEST",
		EnvFile:    "absent.env",
		FS:         mem,
	})
	if err != nil {
		t.Fatalf("config.Load 失败: %v", err)
	}
	return c
}

// TestOptionsFromConfigReadsAsynqSection asserts OptionsFromConfig maps every asynq.* key. | TestOptionsFromConfigReadsAsynqSection 断言 OptionsFromConfig 映射全部 asynq.* 键。
func TestOptionsFromConfigReadsAsynqSection(t *testing.T) {
	const yaml = `
asynq:
  queues: "critical:5,default:2"
  concurrency: 4
  redis_addr: 127.0.0.1:6390
  redis_password: secret
  redis_db: 2
  strict_priority: true
  shutdown_timeout: 20s
  retry_base: 250ms
  retry_max: 5s
`
	o := asynqx.OptionsFromConfig(loadConfig(t, yaml))
	if err := o.Err(); err != nil {
		t.Fatalf("合法配置 Err = %v，期望 nil", err)
	}
	if want := map[string]int{"critical": 5, "default": 2}; !maps.Equal(o.Queues, want) {
		t.Fatalf("Queues = %v，期望 %v", o.Queues, want)
	}
	if o.Concurrency != 4 || o.RedisAddr != "127.0.0.1:6390" || o.RedisPassword != "secret" || o.RedisDB != 2 {
		t.Fatalf("Options = %+v，期望 concurrency=4 / redis_addr / redis_password / redis_db=2", o)
	}
	if !o.StrictPriority || o.ShutdownTimeout != 20*time.Second {
		t.Fatalf("Options = %+v，期望 strict_priority=true / shutdown_timeout=20s", o)
	}
	if o.RetryDelayFunc == nil {
		t.Fatal("RetryDelayFunc is still nil after retry_base/retry_max were configured | 配置了 retry_base/retry_max 后 RetryDelayFunc 仍为 nil")
	}
	task := asynq.NewTask("probe", nil)
	// asynq passes a zero-based retried count, so n=0 is the first retry. | asynq 传入从 0 开始的重试计数，n=0 才是第一次重试。
	if got := o.RetryDelayFunc(0, nil, task); got < 250*time.Millisecond || got > 275*time.Millisecond {
		t.Fatalf("首次重试延迟 = %v，期望 250ms 档（含抖动）", got)
	}
	if got := o.RetryDelayFunc(10, nil, task); got < 5*time.Second || got > 5500*time.Millisecond {
		t.Fatalf("高位重试延迟 = %v，期望封顶 5s（含抖动）", got)
	}

	// An empty config falls back to the asynq defaults registered by config.Defaults. | 空配置回落到 config.Defaults 注册的 asynq 默认值。
	def := asynqx.OptionsFromConfig(loadConfig(t, ""))
	if want := map[string]int{"critical": 3, "default": 2, "low": 1}; !maps.Equal(def.Queues, want) {
		t.Fatalf("默认 Queues = %v，期望 %v", def.Queues, want)
	}
	if def.Concurrency != 10 {
		t.Fatalf("默认 Concurrency = %d，期望 10", def.Concurrency)
	}
	if def.RetryDelayFunc != nil {
		t.Fatal("with no retry_base/retry_max configured the asynq retry policy must not be overridden | 未配置 retry_base/retry_max 时不应擅自覆盖 asynq 的重试策略")
	}
	if err := def.Err(); err != nil {
		t.Fatalf("默认配置 Err = %v，期望 nil", err)
	}

	// A nil config yields zero Options instead of panicking. | nil 配置返回零值 Options 而不是 panic。
	if z := asynqx.OptionsFromConfig(nil); z.Concurrency != 0 || z.RedisAddr != "" || z.Err() != nil {
		t.Fatalf("nil 配置 Options = %+v，期望零值且无错误", z)
	}
}

// TestOptionsFromConfigKeepsQueueParseError asserts a malformed asynq.queues surfaces as a sentinel instead of being swallowed. | TestOptionsFromConfigKeepsQueueParseError 断言非法的 asynq.queues 以哨兵错误暴露而非被吞掉。
func TestOptionsFromConfigKeepsQueueParseError(t *testing.T) {
	o := asynqx.OptionsFromConfig(loadConfig(t, "asynq:\n  queues: \"critical:oops\"\n"))
	if o.Queues != nil {
		t.Fatalf("非法队列配置 Queues = %v，期望 nil", o.Queues)
	}
	if !errors.Is(o.Err(), asynqx.ErrInvalidQueueWeight) {
		t.Fatalf("非法队列配置 Err = %v，期望 ErrInvalidQueueWeight", o.Err())
	}
}

// TestStartRejectsInvalidConfiguration asserts latent registration and option errors stop the server at startup. | TestStartRejectsInvalidConfiguration 断言注册与选项的潜在错误在启动期阻断服务。
func TestStartRejectsInvalidConfiguration(t *testing.T) {
	log, _ := newTestLogger()

	t.Run("非法注册", func(t *testing.T) {
		reg := asynqx.NewRegistry()
		reg.Register("", func(context.Context, asynqx.Payload) error { return nil })
		srv := asynqx.NewServer(reg, asynq.RedisClientOpt{Addr: "127.0.0.1:1"}, testOptions(), log)
		if err := srv.Start(); !errors.Is(err, asynqx.ErrEmptyTaskType) {
			t.Fatalf("Start 错误 = %v，期望 ErrEmptyTaskType", err)
		}
	})

	t.Run("非法队列配置", func(t *testing.T) {
		o := asynqx.OptionsFromConfig(loadConfig(t, "asynq:\n  queues: \"default:0\"\n"))
		srv := asynqx.NewServer(asynqx.NewRegistry(), asynq.RedisClientOpt{Addr: "127.0.0.1:1"}, o, log)
		if err := srv.Start(); !errors.Is(err, asynqx.ErrInvalidQueueWeight) {
			t.Fatalf("Start 错误 = %v，期望 ErrInvalidQueueWeight", err)
		}
	})

	t.Run("非法连接参数", func(t *testing.T) {
		srv := asynqx.NewServer(asynqx.NewRegistry(), asynq.RedisClientOpt{}, asynqx.Options{}, log)
		if err := srv.Start(); !errors.Is(err, asynqx.ErrMissingRedisAddr) {
			t.Fatalf("Start 错误 = %v，期望 ErrMissingRedisAddr", err)
		}
	})
}
