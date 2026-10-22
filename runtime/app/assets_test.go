package app

import (
	"testing"

	"github.com/shibingli/ydx/config"
)

// TestIsDevelopmentRecognisesShortProductionAlias pins the runtime-mode
// normalization: prod behaves like production, dev like development, and an
// unknown value keeps the framework's development default. | TestIsDevelopmentRecognisesShortProductionAlias
// 固定运行模式归一化：prod 等同 production、dev 等同 development，未知取值保持框架的开发默认。
func TestIsDevelopmentRecognisesShortProductionAlias(t *testing.T) {
	cases := []struct {
		env  string
		want bool
	}{
		{"prod", false},
		{"production", false},
		{"PROD", false},
		{" Production ", false},
		{"dev", true},
		{"development", true},
		{"DEV", true},
		{"staging", true},
		{"", true},
	}
	for _, tc := range cases {
		cfg, err := config.Load(config.Options{})
		if err != nil {
			t.Fatalf("加载配置失败: %v", err)
		}
		cfg.Set("app.env", tc.env)
		if got := isDevelopment(cfg); got != tc.want {
			t.Errorf("isDevelopment(app.env=%q) = %v，期望 %v", tc.env, got, tc.want)
		}
	}
}
