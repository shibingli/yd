// This file resolves the ko executable following the repository's precedence chain. | 本文件按仓库优先级链解析 ko 可执行文件。

package ko

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shibingli/yd/tooling/payload"
	"github.com/shibingli/ydx/errs"
)

// DefaultBinary is the command name looked up on PATH when nothing else yields a ko executable. | DefaultBinary
// 是其它来源都未给出 ko 可执行文件时在 PATH 上查找的命令名。
const DefaultBinary = "ko"

// EnvBinary is the environment variable holding the ko path. | EnvBinary 是保存 ko 路径的环境变量。
const EnvBinary = "YD_KO_BIN"

// InstallHint is the command that installs the optional ko CLI. | InstallHint 是安装可选 CLI ko 的命令。
//
// ErrBinaryNotFound and the `yd ko` command quote it verbatim, so the hint stays actionable. | ErrBinaryNotFound
// 与 `yd ko` 命令会逐字引用它，保证提示始终可执行。
const InstallHint = "go install github.com/google/ko@latest"

// LocalToolDir is the project-local drop-in directory of the CLI, relative to the project root. | LocalToolDir
// 是相对项目根的 CLI 本地放置目录。
const LocalToolDir = ".yd/tools"

// PathLookup resolves a bare command name on PATH. | PathLookup 在 PATH 上解析裸命令名。
//
// It is a variable so tests can simulate a missing CLI without touching the real PATH. | 它被声明为变量，
// 便于测试在不触碰真实 PATH 的前提下模拟 CLI 缺失。
var PathLookup = exec.LookPath

// EmbeddedEnsure materializes the embedded ko payload; it is a variable so tests can simulate a host
// without a payload. | EmbeddedEnsure 物化嵌入的 ko payload；它是变量，便于测试模拟没有 payload 的宿主。
var EmbeddedEnsure = payload.Ensure

// Resolve resolves the ko executable following the documented precedence chain. | Resolve 按文档化的优先级链解析 ko 可执行文件。
//
// Resolution order, highest priority first: Options.Binary, YD_KO_BIN, the project-local
// <workdir>/.yd/tools/ko[.exe] copy, and finally PATH - the same chain the align package uses for
// its optional analyzer, and the same order the framework applies to configuration. | 解析顺序由高到低：
// Options.Binary、YD_KO_BIN、项目内 <workdir>/.yd/tools/ko[.exe] 副本，最后是 PATH——与 align 包解析其可选
// 分析器所用的链条一致，也与框架对配置施加的顺序一致。
//
// A value without a path separator is treated as a command name and looked up through the injected
// PathLookup, so YD_KO_BIN=ko keeps working. ErrBinaryNotFound carries the installation hint when no
// source yields an existing binary. | 不含路径分隔符的取值被视为命令名并通过注入的 PathLookup 查找，因此
// YD_KO_BIN=ko 依然可用。所有来源都得不到存在的可执行文件时，ErrBinaryNotFound 会带上安装提示。
func Resolve(o Options) (string, error) {
	lookup := PathLookup
	envBin := strings.TrimSpace(os.Getenv(EnvBinary))
	workingDir := strings.TrimSpace(o.WorkingDir)
	if workingDir == "" {
		workingDir = "."
	}
	local := filepath.Join(workingDir, filepath.FromSlash(LocalToolDir), DefaultBinary+exeSuffix())

	candidates := []struct {
		source string
		value  string
	}{
		{"Binary | 标志", o.Binary},
		{EnvBinary + " | 环境变量", envBin},
		{"local bin | 本地安装", local},
	}
	for _, c := range candidates {
		if strings.TrimSpace(c.value) == "" {
			continue
		}
		if p, ok := resolveCandidate(c.value, lookup); ok {
			return p, nil
		}
	}
	// Finally fall back to the ko command on PATH. | 最后回退 PATH 上的 ko 命令。
	if p, err := lookup(DefaultBinary); err == nil && p != "" {
		return p, nil
	}
	// Execution commands may materialize the pinned ko payload shipped inside yd. | 执行命令可以物化 yd
	// 内嵌的固定版本 ko payload。
	if o.Embedded {
		if path, ok, err := EmbeddedEnsure(workingDir, payload.ToolKo); err == nil && ok {
			return path, nil
		}
	}
	return "", errs.Detail(ErrBinaryNotFound,
		"checked Binary, %[2]s, %[3]s and PATH; install it with %[1]q or drop the binary into %[3]s | 已检查 Binary、%[2]s、%[3]s 与 PATH；可用 %[1]q 安装，或把二进制放入 %[3]s",
		InstallHint, EnvBinary, LocalToolDir)
}

// resolveCandidate turns one configured value into an existing executable. | resolveCandidate 把一个配置取值变成真实存在的可执行文件。
func resolveCandidate(value string, lookup func(string) (string, error)) (string, bool) {
	// A bare command name (no separator) goes through PATH lookup. | 裸命令名（无路径分隔符）交给 PATH 查找。
	if !strings.ContainsAny(value, `/\`) {
		p, err := lookup(value)
		if err != nil || p == "" {
			return "", false
		}
		return p, true
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		abs = filepath.Clean(value)
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", false
	}
	return abs, true
}

// exeSuffix returns the platform executable suffix. | exeSuffix 返回当前平台的可执行文件后缀。
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
