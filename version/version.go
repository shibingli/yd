// Package version 承载由构建期 ldflags 注入的版本元信息。
package version

import "runtime/debug"

// 以下变量通过 -ldflags "-X github.com/shibingli/yd/version.Version=..." 注入。
var (
	// Version 语义化版本号，例如 v1.0.0。
	Version = "dev"
	// Commit 构建时所在的 git 提交。
	Commit = "none"
	// Date 构建时间（RFC3339）。
	Date = "unknown"
)

// String 返回 "版本 (提交, 构建时间)" 形式的可读字符串。
func String() string {
	v, c, d := Version, Commit, Date
	if v == "dev" {
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
