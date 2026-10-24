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

// TestOptionsFromConfigReadsHTTP3Forms pins the object form and the legacy
// boolean scalar of server.http3. | TestOptionsFromConfigReadsHTTP3Forms 固定
// server.http3 的对象形式与旧布尔标量。
func TestOptionsFromConfigReadsHTTP3Forms(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		enabled bool
		wt      bool
	}{
		{"object | 对象形式", "server:\n  http3:\n    enabled: true\n    webtransport: true\n", true, true},
		{"legacy true | 旧布尔真", "server:\n  http3: true\n", true, false},
		{"legacy false | 旧布尔假", "server:\n  http3: false\n", false, false},
		{"absent | 未配置", "server:\n  port: 8080\n", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := afs.NewMem()
			if err := afero.WriteFile(mem, "config.yaml", []byte(tc.body), 0o644); err != nil {
				t.Fatalf("写入配置失败: %v", err)
			}
			cfg, err := config.Load(config.Options{FS: mem, ConfigDir: ".", ConfigName: "config"})
			if err != nil {
				t.Fatalf("加载配置失败: %v", err)
			}
			got := OptionsFromConfig(cfg).HTTP3
			if got.Enabled != tc.enabled || got.WebTransport != tc.wt {
				t.Fatalf("HTTP3 = %+v, want enabled=%v webtransport=%v", got, tc.enabled, tc.wt)
			}
		})
	}
}
