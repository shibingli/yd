// This file maps the ko section of the framework configuration onto Options. | 本文件把框架配置的 ko 段映射为 Options。

package ko

import (
	"strings"

	"github.com/shibingli/yd/config"
)

// Configuration keys of the ko section. | ko 段的配置键。
const (
	// KeyBinary is the ko executable, ranked below --binary but above YD_KO_BIN. | KeyBinary 是 ko 可执行文件，
	// 优先级低于 --binary 但高于 YD_KO_BIN。
	KeyBinary = "ko.binary"
	// KeyRepository is the target repository, passed through KO_DOCKER_REPO. | KeyRepository 是目标仓库，
	// 经 KO_DOCKER_REPO 传递。
	KeyRepository = "ko.repository"
	// KeyTags lists the tags to publish. | KeyTags 列出要发布的标签。
	KeyTags = "ko.tags"
	// KeyPlatforms lists the platforms to build. | KeyPlatforms 列出要构建的平台。
	KeyPlatforms = "ko.platforms"
	// KeyBaseImage overrides the base image, passed through KO_DEFAULTBASEIMAGE. | KeyBaseImage 覆盖基础镜像，
	// 经 KO_DEFAULTBASEIMAGE 传递。
	KeyBaseImage = "ko.base_image"
	// KeyPackages lists the import paths to build. | KeyPackages 列出要构建的导入路径。
	KeyPackages = "ko.packages"
	// KeyLocal publishes into the local docker daemon instead of a registry. | KeyLocal 把结果发布到本地
	// docker 守护进程而不推送到仓库。
	KeyLocal = "ko.local"
	// KeyPush pushes the result to the repository. | KeyPush 把结果推送到仓库。
	KeyPush = "ko.push"
)

// OptionsFromConfig maps the ko section of the framework config onto Options. | OptionsFromConfig
// 把框架配置的 ko 段映射为 Options。
//
// Every key is read through the accessors rather than Unmarshal, so the mapping is unaffected by
// mapstructure tags or key-name case; the command-line flags are layered on top by the `yd ko`
// command, which only overwrites the values whose flag was actually passed. | 所有键都经访问器读取而非
// Unmarshal，因此不受 mapstructure 标签与键名大小写影响；命令行标志由 `yd ko` 命令叠加在其上，且只覆盖确实
// 传入了标志的取值。
//
// Keys, with the value the package falls back to when the key is unset: | 配置键，以及键未设置时本包回退到的取值：
//
//	ko.packages   import paths to build; empty means DefaultPackage (./cmd/app) | 待构建的导入路径；为空表示 DefaultPackage（./cmd/app）
//	ko.binary     ko executable, skips the resolution chain; empty means the chain | ko 可执行文件，跳过解析链；为空表示走解析链
//	ko.repository target repository, e.g. registry.example.com/team/app; required unless ko.local | 目标仓库；除非 ko.local 否则必填
//	ko.tags       tags to publish; empty keeps ko's own default (latest) | 要发布的标签；为空沿用 ko 的默认值（latest）
//	ko.platforms  platforms to build, e.g. ["linux/amd64", "linux/arm64"] | 要构建的平台
//	ko.base_image base image override; empty keeps ko's default (or .ko.yaml) | 基础镜像覆盖；为空沿用 ko 默认值（或 .ko.yaml）
//	ko.local      publish to the local docker daemon instead of a registry | 发布到本地 docker 守护进程而不推送到仓库
//	ko.push       push the result to the repository | 把结果推送到仓库
//
// The two booleans default to false, which is why the `yd ko` command passes their flags to ko
// explicitly instead of relying on ko's own --push default of true. | 两个布尔项默认 false，正因如此
// `yd ko` 命令会把它们的标志显式传给 ko，而不依赖 ko 自身 --push 的 true 默认值。
//
// The environment layer is already applied by the time this function runs, so YD_KO_REPOSITORY (and
// the rest of YD_KO_*) lands on the same keys and wins over the config file, as the framework's
// precedence rule requires. A nil c yields zero Options, and workDir becomes WorkingDir when it is
// not empty. | 本函数运行时环境变量层已经生效，因此 YD_KO_REPOSITORY（以及其余 YD_KO_*）会落在同一批键上
// 并覆盖配置文件，符合框架的优先级规则。c 为 nil 时返回零值 Options；workDir 非空时成为 WorkingDir。
func OptionsFromConfig(c *config.Config, workDir string) Options {
	o := Options{WorkingDir: strings.TrimSpace(workDir)}
	if c == nil {
		return o
	}
	o.Binary = strings.TrimSpace(c.GetString(KeyBinary))
	o.Repository = strings.TrimSpace(c.GetString(KeyRepository))
	o.BaseImage = strings.TrimSpace(c.GetString(KeyBaseImage))
	o.Tags = c.GetStringSlice(KeyTags)
	o.Platforms = c.GetStringSlice(KeyPlatforms)
	if packages := c.GetStringSlice(KeyPackages); len(packages) > 0 {
		o.Packages = packages
	}
	o.Local = c.GetBool(KeyLocal)
	o.Push = c.GetBool(KeyPush)
	return o
}
