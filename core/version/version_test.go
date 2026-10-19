package version_test

import (
	"testing"

	"github.com/shibingli/yd/core/version"
)

// TestModuleVersionPrefersTheInjectedVersion pins the scaffold's source of the
// framework version: an ldflags-injected Version wins over build info, and the
// development placeholder is never written into a generated go.mod. | TestModuleVersionPrefersTheInjectedVersion
// 固定脚手架获取框架版本的来源：ldflags 注入的 Version 优先于 build info，开发占位符绝不写进生成的 go.mod。
func TestModuleVersionPrefersTheInjectedVersion(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old })

	version.Version = "v1.2.3"
	if got := version.ModuleVersion(); got != "v1.2.3" {
		t.Errorf("ModuleVersion() = %q，期望 v1.2.3", got)
	}

	version.Version = "dev"
	if got := version.ModuleVersion(); got == "dev" || got == "(devel)" {
		t.Errorf("ModuleVersion() = %q，开发占位符必须被过滤", got)
	}
}
