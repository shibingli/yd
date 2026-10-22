package gormcache_test

import (
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/config"
	"github.com/shibingli/yd/data/cachex/gormcache"
)

// loadConfig builds a config fixture with a test-only env prefix. | loadConfig 使用测试专用环境前缀构建配置夹具。
func loadConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	mem := afs.NewMem()
	if yaml != "" {
		if err := afero.WriteFile(mem, "config.yaml", []byte(yaml), 0o644); err != nil {
			t.Fatalf("write fixture: %v | 写入夹具失败", err)
		}
	}
	c, err := config.Load(config.Options{
		Name:       "gormcache",
		ConfigDir:  ".",
		ConfigName: "config",
		EnvPrefix:  "YGORMCACHETEST",
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
  gorm:
    enabled: true
    namespace: "db"
    ttl: 45s
    tables: ["users", "roles"]
    ignore_tables: ["audit_logs"]
`)
	if !gormcache.EnabledFromConfig(c) {
		t.Fatal("enabled = false, want true | 启用开关不符")
	}
	o := gormcache.OptionsFromConfig(c, nil)
	if o.Namespace != "db" || o.TTL != 45*time.Second {
		t.Fatalf("options = %+v | 插件选项不符", o)
	}
	if len(o.Tables) != 2 || o.Tables[0] != "users" || len(o.IgnoreTables) != 1 || o.IgnoreTables[0] != "audit_logs" {
		t.Fatalf("table filters = %+v | 表过滤不符", o)
	}
}

func TestOptionsFromConfigDefaults(t *testing.T) {
	c := loadConfig(t, "")
	if gormcache.EnabledFromConfig(c) {
		t.Fatal("GORM cache enabled by default | GORM 缓存默认被启用")
	}
	o := gormcache.OptionsFromConfig(c, nil)
	if o.Namespace != "gorm" || o.TTL != 30*time.Second {
		t.Fatalf("defaults = %+v | 默认值不符", o)
	}
	if gormcache.EnabledFromConfig(nil) {
		t.Fatal("nil config enabled the plugin | nil 配置启用了插件")
	}
}
