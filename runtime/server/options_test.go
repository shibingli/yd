package server

import (
	"testing"

	"github.com/shibingli/ydx/afs"
	"github.com/shibingli/ydx/config"
	"github.com/spf13/afero"
)

// TestOptionsFromConfigReadsHTTP2ExtendedConnect pins the config key mapping. | TestOptionsFromConfigReadsHTTP2ExtendedConnect 固定配置键映射。
func TestOptionsFromConfigReadsHTTP2ExtendedConnect(t *testing.T) {
	mem := afs.NewMem()
	if err := afero.WriteFile(mem, "config.yaml", []byte("server:\n  http2:\n    extended_connect: true\n"), 0o644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	cfg, err := config.Load(config.Options{FS: mem, ConfigDir: ".", ConfigName: "config"})
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if !OptionsFromConfig(cfg).ForceHTTP2ExtendedConnect {
		t.Fatal("ForceHTTP2ExtendedConnect = false, want true | ForceHTTP2ExtendedConnect = false，期望 true")
	}

	mem2 := afs.NewMem()
	if err := afero.WriteFile(mem2, "config.yaml", []byte("server:\n  h2c: true\n"), 0o644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	cfg2, err := config.Load(config.Options{FS: mem2, ConfigDir: ".", ConfigName: "config"})
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if OptionsFromConfig(cfg2).ForceHTTP2ExtendedConnect {
		t.Fatal("ForceHTTP2ExtendedConnect = true without the key, want false | 未配置该项时不应为 true")
	}
}
