// Package version carries the version metadata injected at build time via ldflags. | Package version 承载由构建期 ldflags 注入的版本元信息。
package version

import (
	"runtime/debug"
	"strings"
)

// The variables below are injected via -ldflags "-X github.com/shibingli/yd/core/version.Version=...". | 以下变量通过 -ldflags "-X github.com/shibingli/yd/core/version.Version=..." 注入。
var (
	// Version is the semantic version number, e.g. v1.0.0. | Version 语义化版本号，例如 v1.0.0。
	Version = "dev"
	// Commit is the git commit the build was made from. | Commit 构建时所在的 git 提交。
	Commit = "none"
	// Date is the build time (RFC3339). | Date 构建时间（RFC3339）。
	Date = "unknown"
)

// ModuleVersion returns the module version of the running yd binary. It prefers
// an ldflags-injected Version and falls back to the main module version recorded
// by `go install module/cmd@version`; development placeholders are filtered.
// An empty result means the version is unknown and must not be written to a
// generated go.mod. | ModuleVersion 返回运行中的 yd 二进制的模块版本：优先使用 ldflags
// 注入的 Version，其次使用 `go install module/cmd@version` 记录的 main module 版本；
// 开发占位符会被过滤。返回空串表示版本未知，不得写进生成的 go.mod。
func ModuleVersion() string {
	if Version != "" && Version != "dev" {
		return cleanModuleVersion(Version)
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		// `go install module/cmd@vX.Y.Z` records the module version in Main.Version even
		// without ldflags; ignoring it made installed binaries report "dev". | 即使没有
		// ldflags，`go install module/cmd@vX.Y.Z` 也会把模块版本写进 Main.Version；忽略它
		// 会让安装出来的二进制报告 "dev"。
		if mv := cleanModuleVersion(bi.Main.Version); mv != "" && mv != "(devel)" {
			return mv
		}
	}
	return ""
}

// cleanModuleVersion removes the `+dirty` suffix a build from a modified worktree carries.
// That suffix is valid build metadata but not a fetchable module version, so writing it into a
// generated go.mod makes `go mod tidy` fail. `+incompatible` and other metadata are preserved.
// | cleanModuleVersion 去掉脏工作区构建携带的 `+dirty` 后缀。它是合法的构建元数据，但不是
// 可获取的模块版本；写进生成的 go.mod 会让 `go mod tidy` 失败。`+incompatible` 等其它元数据
// 保持不变。
func cleanModuleVersion(value string) string {
	return strings.TrimSuffix(value, "+dirty")
}

// String returns a readable "version (commit, build time)" string. | String 返回 "版本 (提交, 构建时间)" 形式的可读字符串。
func String() string {
	v, c, d := Version, Commit, Date
	if v == "dev" {
		if mv := ModuleVersion(); mv != "" {
			v = mv
		}
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" && c == "none" {
					c = s.Value
				}
				if s.Key == "vcs.time" && d == "unknown" {
					d = s.Value
				}
			}
		}
	}
	return v + " (" + c + ", " + d + ")"
}
