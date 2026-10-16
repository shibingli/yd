package redisx_test

import (
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/afs"
	"github.com/shibingli/yd/config"
)

// loadConfig builds a config instance from an in-memory YAML fixture. | loadConfig 从内存 YAML 夹具构建配置实例。
//
// EnvPrefix is deliberately test-specific so a developer's real YD_REDIS_* variables | EnvPrefix 刻意使用测试专用前缀，隔离开发机上真实的 YD_REDIS_* 环境变量；
// cannot leak into the assertions; absent.env keeps the repository .env out of the picture too. | absent.env 同样避免仓库根的 .env 被读入。
func loadConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	mem := afs.NewMem()
	if yaml != "" {
		if err := afero.WriteFile(mem, "config.yaml", []byte(yaml), 0o644); err != nil {
			t.Fatalf("写入内存配置文件失败: %v", err)
		}
	}
	c, err := config.Load(config.Options{
		Name:       "redisx",
		ConfigDir:  ".",
		ConfigName: "config",
		EnvPrefix:  "YDREDISXTEST",
		EnvFile:    "absent.env",
		FS:         mem,
	})
	if err != nil {
		t.Fatalf("config.Load 失败: %v", err)
	}
	return c
}
