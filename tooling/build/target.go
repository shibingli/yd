package build

import (
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// ErrInvalidTarget indicates that a target string is not a known GOOS/GOARCH pair. | ErrInvalidTarget 表示目标字符串不是已知的 GOOS/GOARCH 组合。
var ErrInvalidTarget = errx.New("build: invalid build target | build：非法的构建目标")

// Target is one GOOS/GOARCH pair to build for. | Target 是一个待构建的 GOOS/GOARCH 组合。
type Target struct {
	// GOOS is the target operating system, e.g. "linux". | GOOS 是目标操作系统，例如 "linux"。
	GOOS string
	// GOARCH is the target architecture, e.g. "arm64". | GOARCH 是目标架构，例如 "arm64"。
	GOARCH string
}

// String renders the target as "GOOS/GOARCH", the spelling used on the command line. | String 按命令行写法渲染为 "GOOS/GOARCH"。
func (t Target) String() string { return t.GOOS + "/" + t.GOARCH }

// knownGOOS lists the operating systems accepted by go tool dist list. | knownGOOS 列出 go tool dist list 接受的操作系统。
var knownGOOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "illumos": true, "ios": true, "js": true,
	"linux": true, "netbsd": true, "openbsd": true, "plan9": true,
	"solaris": true, "wasip1": true, "windows": true,
}

// knownGOARCH lists the architectures accepted by go tool dist list. | knownGOARCH 列出 go tool dist list 接受的架构。
var knownGOARCH = map[string]bool{
	"386": true, "amd64": true, "arm": true, "arm64": true,
	"loong64": true, "mips": true, "mips64": true, "mips64le": true,
	"mipsle": true, "ppc64": true, "ppc64le": true, "riscv64": true,
	"s390x": true, "wasm": true,
}

// ParseTargets parses a comma separated list such as
// "linux/amd64,linux/arm64,windows/amd64" into individual targets. | ParseTargets 把
// "linux/amd64,linux/arm64,windows/amd64" 这样以逗号分隔的列表解析为独立的目标。
//
// Every entry is trimmed and lower-cased; empty entries (a trailing comma, for
// instance) are skipped and duplicates are collapsed while the original order is
// preserved. An empty list, a missing "/", an empty GOOS/GOARCH or a name outside
// the go tool dist list set all yield ErrInvalidTarget. | 每个条目都会去除首尾空白并
// 转小写；空条目（例如结尾的逗号）被跳过，重复项在保持原顺序的前提下去重。空列表、缺少 "/"、
// GOOS/GOARCH 为空，或名称不在 go tool dist list 范围内，都会返回 ErrInvalidTarget。
func ParseTargets(s string) ([]Target, error) {
	var (
		targets []Target
		seen    = make(map[string]bool)
	)
	for _, raw := range strings.Split(s, ",") {
		entry := strings.ToLower(strings.TrimSpace(raw))
		if entry == "" {
			continue
		}
		goos, goarch, ok := strings.Cut(entry, "/")
		if !ok || goos == "" || goarch == "" || strings.Contains(goarch, "/") {
			return nil, errx.Detail(ErrInvalidTarget, "%q is not a GOOS/GOARCH pair | %q 不是 GOOS/GOARCH 组合", strings.TrimSpace(raw), strings.TrimSpace(raw))
		}
		if !knownGOOS[goos] {
			return nil, errx.Detail(ErrInvalidTarget, "unknown GOOS %q | 未知的 GOOS %q", goos, goos)
		}
		if !knownGOARCH[goarch] {
			return nil, errx.Detail(ErrInvalidTarget, "unknown GOARCH %q | 未知的 GOARCH %q", goarch, goarch)
		}
		if seen[entry] {
			continue
		}
		seen[entry] = true
		targets = append(targets, Target{GOOS: goos, GOARCH: goarch})
	}
	if len(targets) == 0 {
		return nil, errx.Detail(ErrInvalidTarget, "no target given | 未提供任何目标平台")
	}
	return targets, nil
}
