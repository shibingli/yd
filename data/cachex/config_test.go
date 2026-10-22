package cachex_test

import (
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/config"
	"github.com/shibingli/yd/data/cachex"
)

// loadConfig builds a config from an in-memory fixture with a test-only env prefix. | loadConfig 使用测试专用环境前缀从内存夹具构建配置。
func loadConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	mem := afs.NewMem()
	if yaml != "" {
		if err := afero.WriteFile(mem, "config.yaml", []byte(yaml), 0o644); err != nil {
			t.Fatalf("write fixture: %v | 写入夹具失败", err)
		}
	}
	c, err := config.Load(config.Options{
		Name:       "cachex",
		ConfigDir:  ".",
		ConfigName: "config",
		EnvPrefix:  "YCACHETEST",
		EnvFile:    "absent.env",
		FS:         mem,
	})
	if err != nil {
		t.Fatalf("config.Load: %v | 加载配置失败", err)
	}
	return c
}

func TestOptionsFromConfig(t *testing.T) {
	c := loadConfig(t, `
cache:
  prefix: "app:"
  lease: 5s
  load_timeout: 2s
  event_buffer: 32
  fail_open: false
  tombstone_ttl: 2h
  max_cost: 1048576
  shards: 4
  max_entry_size: 65536
  cleanup_interval: 250ms
  sample_size: 64
`)
	o := cachex.OptionsFromConfig(c)
	if o.Lease != 5*time.Second || o.LoadTimeout != 2*time.Second {
		t.Fatalf("basic options = %+v | 基础选项不符", o)
	}
	if o.EventBuffer != 32 || o.FailOpen || o.TombstoneTTL != 2*time.Hour {
		t.Fatalf("event options = %+v | 事件选项不符", o)
	}
	if o.Local.MaxCost != 1048576 || o.Local.Shards != 4 || o.Local.MaxEntrySize != 65536 ||
		o.Local.CleanupInterval != 250*time.Millisecond || o.Local.SampleSize != 64 {
		t.Fatalf("local options = %+v | 本地选项不符", o.Local)
	}
}

func TestOptionsFromConfigDefaults(t *testing.T) {
	c := loadConfig(t, "")
	o := cachex.OptionsFromConfig(c)
	if o.Lease != cachex.DefaultLease || o.LoadTimeout != cachex.DefaultLoadTimeout {
		t.Fatalf("defaults = %+v | 默认值不符", o)
	}
	if !o.FailOpen || o.EventBuffer != cachex.DefaultEventBuffer || o.TombstoneTTL != cachex.DefaultTombstoneTTL {
		t.Fatalf("defaults = %+v | 默认值不符", o)
	}
	if o.Local.MaxCost != 0 || o.Local.Shards != 0 || o.Local.MaxEntrySize != 0 {
		t.Fatalf("local defaults = %+v | 本地默认值应为零（自动推导）", o.Local)
	}
	if o := cachex.OptionsFromConfig(nil); !o.FailOpen {
		t.Fatalf("nil config = %+v | nil 配置应保持失败开放", o)
	}
}

func TestRedisOptionsFromConfig(t *testing.T) {
	c := loadConfig(t, `
redis:
  key_prefix: "rk:"
cache:
  prefix: "app:"
  tombstone_ttl: 2h
  redis:
    channel: "events"
    subscribe_buffer: 8
    health_interval: 3s
`)
	o := cachex.RedisOptionsFromConfig(c)
	if o.Prefix != "rk:app:" {
		t.Fatalf("prefix = %q, want rk:app: | Redis 前缀不符", o.Prefix)
	}
	if o.Channel != "events" || o.SubscribeBuffer != 8 || o.HealthInterval != 3*time.Second {
		t.Fatalf("redis options = %+v | Redis 选项不符", o)
	}
	if o.TombstoneTTL != 2*time.Hour {
		t.Fatalf("tombstone TTL = %v, want 2h | 墓碑 TTL 回退不符", o.TombstoneTTL)
	}
}

func TestRedisOptionsFromConfigExplicitPrefix(t *testing.T) {
	c := loadConfig(t, `
redis:
  key_prefix: "ignored:"
cache:
  prefix: "app:"
  redis:
    prefix: "explicit:"
`)
	o := cachex.RedisOptionsFromConfig(c)
	if o.Prefix != "explicit:" {
		t.Fatalf("prefix = %q, want explicit: | 显式 Redis 前缀未生效", o.Prefix)
	}
	if o.Channel != "explicit:events" {
		t.Fatalf("channel = %q | 默认频道不符", o.Channel)
	}
	if o := cachex.RedisOptionsFromConfig(nil); o.Prefix != "" {
		t.Fatalf("nil config = %+v | nil 配置应返回零值", o)
	}
}
