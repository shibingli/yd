// This file tests the option mapping of the service package: the configuration keys behind
// OptionsFromConfig, their fallbacks and the kardianos configuration they produce. No test touches
// the operating system's service state. | 本文件测试 service 包的选项映射：OptionsFromConfig 背后的配置键、
// 它们的回退顺序以及由此产生的 kardianos 配置。任何测试都不触碰操作系统的服务状态。

package service

import (
	"errors"
	"testing"

	"github.com/kardianos/service"
	"github.com/shibingli/yd/config"
	"github.com/spf13/afero"
)

// serviceTestConfig 用内存文件系统加载一份分层配置，键值完全由调用方给出。
// serviceTestConfig loads a layered configuration from an in-memory file system whose keys are entirely supplied by the caller.
func serviceTestConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()

	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "config.yaml", []byte(yaml), 0o644); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	cfg, err := config.Load(config.Options{FS: fsys, ConfigDir: ".", Name: "service-test"})
	if err != nil {
		t.Fatalf("加载测试配置失败: %v", err)
	}
	return cfg
}

// TestOptionsFromConfigReadsServiceSection 断言 service.* 各键逐项落到 Options。
// TestOptionsFromConfigReadsServiceSection asserts every service.* key lands on its Options field.
func TestOptionsFromConfigReadsServiceSection(t *testing.T) {
	cfg := serviceTestConfig(t, `
app:
  name: shop-admin
service:
  name: shop-service
  display_name: Shop Admin Service
  description: 商城后台常驻服务 | shop admin daemon
  executable: /opt/shop/bin/shop-admin
  arguments:
    - -service
    - run
  working_directory: /opt/shop
  user_name: shop
  depends_on:
    - redis
    - mysql
  env:
    - YD_APP_ENV=production
    - TZ=Asia/Shanghai
  restart_on_exit: true
`)

	got := OptionsFromConfig(cfg)
	want := Options{
		Name:             "shop-service",
		DisplayName:      "Shop Admin Service",
		Description:      "商城后台常驻服务 | shop admin daemon",
		Executable:       "/opt/shop/bin/shop-admin",
		WorkingDirectory: "/opt/shop",
		UserName:         "shop",
		Arguments:        []string{"-service", "run"},
		DependsOn:        []string{"redis", "mysql"},
		EnvVars:          map[string]string{"YD_APP_ENV": "production", "TZ": "Asia/Shanghai"},
		RestartOnExit:    true,
	}

	if got.Name != want.Name {
		t.Errorf("service.name = %q, 期望 %q", got.Name, want.Name)
	}
	if got.DisplayName != want.DisplayName {
		t.Errorf("service.display_name = %q, 期望 %q", got.DisplayName, want.DisplayName)
	}
	if got.Description != want.Description {
		t.Errorf("service.description = %q, 期望 %q", got.Description, want.Description)
	}
	if got.Executable != want.Executable {
		t.Errorf("service.executable = %q, 期望 %q", got.Executable, want.Executable)
	}
	if got.WorkingDirectory != want.WorkingDirectory {
		t.Errorf("service.working_directory = %q, 期望 %q", got.WorkingDirectory, want.WorkingDirectory)
	}
	if got.UserName != want.UserName {
		t.Errorf("service.user_name = %q, 期望 %q", got.UserName, want.UserName)
	}
	if got.RestartOnExit != want.RestartOnExit {
		t.Errorf("service.restart_on_exit = %v, 期望 %v", got.RestartOnExit, want.RestartOnExit)
	}
	if len(got.Arguments) != len(want.Arguments) {
		t.Fatalf("service.arguments = %v, 期望 %v", got.Arguments, want.Arguments)
	}
	for i, arg := range want.Arguments {
		if got.Arguments[i] != arg {
			t.Errorf("service.arguments[%d] = %q, 期望 %q", i, got.Arguments[i], arg)
		}
	}
	if len(got.DependsOn) != len(want.DependsOn) {
		t.Fatalf("service.depends_on = %v, 期望 %v", got.DependsOn, want.DependsOn)
	}
	for i, dep := range want.DependsOn {
		if got.DependsOn[i] != dep {
			t.Errorf("service.depends_on[%d] = %q, 期望 %q", i, got.DependsOn[i], dep)
		}
	}
	if got.EnvVars["YD_APP_ENV"] != "production" || got.EnvVars["TZ"] != "Asia/Shanghai" {
		t.Errorf("service.env = %v, 期望含 YD_APP_ENV=production 与 TZ=Asia/Shanghai（列表形式保留大小写）", got.EnvVars)
	}
}

// TestOptionsFromConfigEnvMappingForm 断言映射形式的 service.env 仍被读取（键名由配置引擎转为小写）。
// TestOptionsFromConfigEnvMappingForm asserts the mapping form of service.env is still read (the configuration engine lower-cases its keys).
func TestOptionsFromConfigEnvMappingForm(t *testing.T) {
	cfg := serviceTestConfig(t, "service:\n  env:\n    YD_APP_ENV: production\n")

	got := OptionsFromConfig(cfg)
	if got.EnvVars["yd_app_env"] != "production" {
		t.Errorf("映射形式应读入并保留配置引擎的大小写结果, 实际 %v", got.EnvVars)
	}
}

// TestOptionsFromConfigFallsBackToAppName 断言缺省时服务名回落到 app.name，显示名与描述逐级回落。
// TestOptionsFromConfigFallsBackToAppName asserts the service name falls back to app.name and that display name and description fall back one step each.
func TestOptionsFromConfigFallsBackToAppName(t *testing.T) {
	cfg := serviceTestConfig(t, "app:\n  name: shop-admin\n")

	got := OptionsFromConfig(cfg)
	if got.Name != "shop-admin" {
		t.Errorf("服务名未回退到 app.name: %q", got.Name)
	}
	if got.DisplayName != "shop-admin" {
		t.Errorf("显示名未回退到服务名: %q", got.DisplayName)
	}
	if got.Description != "shop-admin" {
		t.Errorf("描述未回退到显示名: %q", got.Description)
	}
	if got.Executable != "" || got.RestartOnExit || len(got.Arguments) != 0 {
		t.Errorf("未配置的字段应为零值: %+v", got)
	}
}

// TestOptionsFromConfigNil 断言 nil 配置返回零值 Options 而不是 panic。
// TestOptionsFromConfigNil asserts a nil configuration yields the zero Options instead of panicking.
func TestOptionsFromConfigNil(t *testing.T) {
	got := OptionsFromConfig(nil)
	if got.Name != "" || got.DisplayName != "" || got.Description != "" || got.Executable != "" {
		t.Errorf("nil 配置应返回零值 Options, 实际 %+v", got)
	}
	if len(got.Arguments) != 0 || len(got.DependsOn) != 0 || len(got.EnvVars) != 0 || got.RestartOnExit {
		t.Errorf("nil 配置的集合字段应为空, 实际 %+v", got)
	}
}

// TestConfigForMapsOptions 断言 Options 被映射到 kardianos 配置，且空值被逐级补齐。
// TestConfigForMapsOptions asserts Options reaches the kardianos configuration and that empty values are filled step by step.
func TestConfigForMapsOptions(t *testing.T) {
	cfg := configFor(Options{
		Name:             "shop-service",
		Executable:       "/opt/shop/bin/shop-admin",
		Arguments:        []string{"-service", "run"},
		WorkingDirectory: "/opt/shop",
		UserName:         "shop",
		DependsOn:        []string{"redis"},
		EnvVars:          map[string]string{"YD_APP_ENV": "production"},
		RestartOnExit:    true,
	})

	if cfg.Name != "shop-service" {
		t.Errorf("Config.Name = %q", cfg.Name)
	}
	if cfg.DisplayName != "shop-service" {
		t.Errorf("显示名未回退到服务名: %q", cfg.DisplayName)
	}
	if cfg.Description != "shop-service" {
		t.Errorf("描述未回退到显示名: %q", cfg.Description)
	}
	if cfg.Executable != "/opt/shop/bin/shop-admin" {
		t.Errorf("Config.Executable = %q", cfg.Executable)
	}
	if cfg.WorkingDirectory != "/opt/shop" || cfg.UserName != "shop" {
		t.Errorf("工作目录或账号映射错误: %q / %q", cfg.WorkingDirectory, cfg.UserName)
	}
	if len(cfg.Arguments) != 2 || cfg.Arguments[0] != "-service" {
		t.Errorf("Config.Arguments = %v", cfg.Arguments)
	}
	if len(cfg.Dependencies) != 1 || cfg.Dependencies[0] != "redis" {
		t.Errorf("Config.Dependencies = %v", cfg.Dependencies)
	}
	if cfg.EnvVars["YD_APP_ENV"] != "production" {
		t.Errorf("Config.EnvVars = %v", cfg.EnvVars)
	}
	if got := cfg.Option[service.OnFailure]; got != service.OnFailureRestart {
		t.Errorf("RestartOnExit 应写入 %s=restart, 实际 %v", service.OnFailure, got)
	}
	if got := cfg.Option[restartOnExitKey]; got != restartAlwaysValue {
		t.Errorf("RestartOnExit 应写入 %s=always, 实际 %v", restartOnExitKey, got)
	}
}

// TestConfigForDefaultsNameFromExecutable 断言名字为空时用程序基础名补齐，程序也为空时回退为框架名。
// TestConfigForDefaultsNameFromExecutable asserts an empty name is filled from the executable base name, falling back to the framework name.
func TestConfigForDefaultsNameFromExecutable(t *testing.T) {
	windows := configFor(Options{Executable: `C:\Program Files\shop\shop-admin.exe`})
	if windows.Name != "shop-admin" {
		t.Errorf("未从程序路径推导服务名: %q", windows.Name)
	}

	empty := configFor(Options{})
	if empty.Name != fallbackServiceName {
		t.Errorf("空选项应回退到 %q, 实际 %q", fallbackServiceName, empty.Name)
	}
	if empty.Option != nil {
		t.Errorf("未开启自动重启时不应写入平台选项: %v", empty.Option)
	}
}

// TestPlatformErrorMapping 断言平台缺失与名字缺失分别映射到对应的哨兵错误。
// TestPlatformErrorMapping asserts a missing platform and a missing name map onto their sentinels.
func TestPlatformErrorMapping(t *testing.T) {
	if err := platformError(nil); err != nil {
		t.Errorf("nil 错误应返回 nil, 实际 %v", err)
	}
	if err := platformError(service.ErrNoServiceSystemDetected); err == nil {
		t.Error("缺少服务管理器的错误应被包装")
	}
}

// TestRunRejectsNilFunction 断言 Run 拒绝空的 run 函数，且判定先于服务句柄构建。
// TestRunRejectsNilFunction asserts Run rejects a nil run function and judges it before building a service handle.
func TestRunRejectsNilFunction(t *testing.T) {
	err := Run(Options{Name: "shop-service"}, nil)
	if !errors.Is(err, ErrNilRun) {
		t.Fatalf("期望 ErrNilRun, 实际 %v", err)
	}
}
