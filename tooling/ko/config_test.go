// This file tests the mapping from the framework configuration onto the ko options. | 本文件测试框架配置到 ko 选项的映射。

package ko

import (
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/ydx/afs"
	"github.com/shibingli/ydx/config"
)

// loadKoConfig builds a config instance from an in-memory YAML fixture. | loadKoConfig 从内存 YAML 夹具构建配置实例。
//
// The env prefix and .env file are test-specific, so neither the developer's real YD_KO_* variables
// nor the repository's own .env can leak into the assertions. | 环境变量前缀与 .env 文件均为测试专用，
// 因此开发机上真实的 YD_KO_* 变量与仓库自身的 .env 都不会影响断言。
func loadKoConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	mem := afs.NewMem()
	if yaml != "" {
		if err := afero.WriteFile(mem, "config.yaml", []byte(yaml), 0o644); err != nil {
			t.Fatalf("写入内存配置文件失败: %v", err)
		}
	}
	c, err := config.Load(config.Options{
		Name:       "ko",
		ConfigDir:  ".",
		ConfigName: "config",
		EnvPrefix:  "YDKOTEST",
		EnvFile:    "absent.env",
		FS:         mem,
	})
	if err != nil {
		t.Fatalf("config.Load 失败: %v", err)
	}
	return c
}

// TestOptionsFromConfig maps every ko.* key of a config file onto the options. | TestOptionsFromConfig
// 把配置文件里的每个 ko.* 键映射到选项上。
func TestOptionsFromConfig(t *testing.T) {
	c := loadKoConfig(t, `
ko:
  repository: registry.example.com/team/app
  base_image: cgr.dev/chainguard/static:latest
  binary: /usr/local/bin/ko
  packages: ["./cmd/api", "./cmd/worker"]
  tags: ["v1.2.3", "latest"]
  platforms: ["linux/amd64", "linux/arm64"]
  local: true
  push: false
  ldflags: ["-s -w", "-X main.version=v1"]
`)

	o := OptionsFromConfig(c, "work")
	want := Options{
		Binary:     "/usr/local/bin/ko",
		Repository: "registry.example.com/team/app",
		BaseImage:  "cgr.dev/chainguard/static:latest",
		WorkingDir: "work",
		Packages:   []string{"./cmd/api", "./cmd/worker"},
		Tags:       []string{"v1.2.3", "latest"},
		Platforms:  []string{"linux/amd64", "linux/arm64"},
		LDFlags:    []string{"-s -w", "-X main.version=v1"},
		Local:      true,
		Push:       false,
	}
	assertKoOptionsEqual(t, o, want)
}

// TestOptionsFromConfigEmptyLDFlagsDisables pins the escape hatch: an explicitly empty ko.ldflags list
// disables the default strip flags instead of silently restoring them. | TestOptionsFromConfigEmptyLDFlagsDisables
// 固化逃生口：显式空的 ko.ldflags 列表关闭默认的去符号参数，而不是悄悄回退到默认值。
func TestOptionsFromConfigEmptyLDFlagsDisables(t *testing.T) {
	c := loadKoConfig(t, `
ko:
  ldflags: []
`)
	o := OptionsFromConfig(c, "work")
	if o.LDFlags == nil {
		t.Fatal("显式空的 ko.ldflags 必须产生非 nil 空切片 | an explicit empty ko.ldflags must yield a non-nil empty slice")
	}
	if got := o.EffectiveLDFlags(); len(got) != 0 {
		t.Errorf("EffectiveLDFlags() = %q | EffectiveLDFlags() 得到 %q，期望为空", got, got)
	}
}

// TestOptionsFromConfigUnset checks the zero-value fallbacks: an empty config file leaves everything
// at its zero value, which BuildImage then fills with its own defaults. | TestOptionsFromConfigUnset
// 校验零值回退：空的配置文件让所有项保持零值，随后由 BuildImage 用自身默认值补齐。
func TestOptionsFromConfigUnset(t *testing.T) {
	o := OptionsFromConfig(loadKoConfig(t, ""), "work")
	if !slices.Equal(o.Packages, []string(nil)) {
		t.Errorf("Packages = %q | Packages 得到 %q，期望 nil", o.Packages, o.Packages)
	}
	if o.Local || o.Push {
		t.Errorf("Local/Push 应为 false，得到 %v/%v | Local/Push must stay false, got %v/%v", o.Local, o.Push, o.Local, o.Push)
	}
	if o.Repository != "" || o.BaseImage != "" || o.Binary != "" {
		t.Errorf("字符串项应为空：%+v | the string fields must stay empty: %+v", o, o)
	}
	// Without explicit packages the argv falls back to the `yd new` layout. | 未显式给出包时，argv 回退到 `yd new` 的布局。
	if got, want := o.Args(), []string{"build", "--base-import-paths", "--push=false", "--ldflags", DefaultLDFlags, DefaultPackage}; !slices.Equal(got, want) {
		t.Errorf("Args() = %q | Args() 得到 %q，期望 %q", got, got, want)
	}
	if o.LDFlags != nil {
		t.Errorf("未配置 ko.ldflags 时应保持 nil | LDFlags must stay nil when ko.ldflags is unset: %q", o.LDFlags)
	}

	if o := OptionsFromConfig(nil, "work"); o.WorkingDir != "work" {
		t.Errorf("nil 配置只保留 WorkingDir，得到 %+v | a nil config keeps WorkingDir only, got %+v", o, o)
	}
}

// assertKoOptionsEqual compares two option sets field by field, so a failure names the field. | assertKoOptionsEqual
// 逐字段比较两组选项，使失败信息能指出具体字段。
func assertKoOptionsEqual(t *testing.T, got, want Options) {
	t.Helper()
	if got.Binary != want.Binary {
		t.Errorf("Binary = %q | Binary 得到 %q，期望 %q", got.Binary, got.Binary, want.Binary)
	}
	if got.Repository != want.Repository {
		t.Errorf("Repository = %q | Repository 得到 %q，期望 %q", got.Repository, got.Repository, want.Repository)
	}
	if got.BaseImage != want.BaseImage {
		t.Errorf("BaseImage = %q | BaseImage 得到 %q，期望 %q", got.BaseImage, got.BaseImage, want.BaseImage)
	}
	if got.WorkingDir != want.WorkingDir {
		t.Errorf("WorkingDir = %q | WorkingDir 得到 %q，期望 %q", got.WorkingDir, got.WorkingDir, want.WorkingDir)
	}
	if !slices.Equal(got.Packages, want.Packages) {
		t.Errorf("Packages = %q | Packages 得到 %q，期望 %q", got.Packages, got.Packages, want.Packages)
	}
	if !slices.Equal(got.Tags, want.Tags) {
		t.Errorf("Tags = %q | Tags 得到 %q，期望 %q", got.Tags, got.Tags, want.Tags)
	}
	if !slices.Equal(got.Platforms, want.Platforms) {
		t.Errorf("Platforms = %q | Platforms 得到 %q，期望 %q", got.Platforms, got.Platforms, want.Platforms)
	}
	if !slices.Equal(got.LDFlags, want.LDFlags) {
		t.Errorf("LDFlags = %q | LDFlags 得到 %q，期望 %q", got.LDFlags, got.LDFlags, want.LDFlags)
	}
	if got.Local != want.Local {
		t.Errorf("Local = %v | Local 得到 %v，期望 %v", got.Local, got.Local, want.Local)
	}
	if got.Push != want.Push {
		t.Errorf("Push = %v | Push 得到 %v，期望 %v", got.Push, got.Push, want.Push)
	}
}
