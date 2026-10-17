// Package toolchain manages the external command line tools that yd needs at development time (currently the Tailwind CSS standalone binary). | Package toolchain 管理 yd 开发期依赖的外部命令行工具（目前是 Tailwind CSS standalone）。
//
// It resolves the tool through the documented precedence chain, runs a CSS build, watches the project tree with debounced events, and drives the live-reload loop behind `yd dev`. | 它按文档化的优先级链解析工具、执行 CSS 构建、以抖动合并事件监听项目树，并驱动 `yd dev` 的热重载主循环。
//
// Every failure path references a package-level sentinel declared here, so callers decide with errors.Is instead of string matching. | 所有失败路径都引用本文件声明的包级哨兵，调用方用 errors.Is 判定而不做字符串匹配。
package toolchain

import (
	"context"
	"os/exec"
	"runtime"
	"sync"

	"github.com/shibingli/yd/core/errx"
)

// Declared sentinels of the package. | 本包声明的哨兵错误。
var (
	// ErrToolNotFound reports that the tailwindcss executable could not be resolved. | ErrToolNotFound 表示未能解析到 tailwindcss 可执行文件。
	ErrToolNotFound = errx.New("toolchain: tailwindcss binary not found | toolchain: 未找到 tailwindcss 可执行文件")
	// ErrBuildFailed reports that an external build command failed or produced no output. | ErrBuildFailed 表示外部构建命令执行失败或未产出文件。
	ErrBuildFailed = errx.New("toolchain: build command failed | toolchain: 构建命令执行失败")
	// ErrNoWatchRoot reports that no directory of the watch roots could be watched. | ErrNoWatchRoot 表示监听根目录中没有任何一个可以被监听。
	ErrNoWatchRoot = errx.New("toolchain: no watchable root directory | toolchain: 没有可监听的根目录")
	// ErrWatchFailed reports a failure while registering or tearing down a watch. | ErrWatchFailed 表示注册或拆除监听时失败。
	ErrWatchFailed = errx.New("toolchain: file watch failed | toolchain: 文件监听失败")
	// ErrProjectDir reports that the project directory of a dev session does not exist. | ErrProjectDir 表示开发会话的项目目录不存在。
	ErrProjectDir = errx.New("toolchain: project directory does not exist | toolchain: 项目目录不存在")
	// ErrChildStart reports that the dev child process could not be started. | ErrChildStart 表示开发子进程启动失败。
	ErrChildStart = errx.New("toolchain: cannot start child process | toolchain: 无法启动子进程")
	// ErrDevRunning reports that a DevRunner is already running. | ErrDevRunning 表示 DevRunner 已在运行中。
	ErrDevRunning = errx.New("toolchain: dev runner already running | toolchain: 开发运行器已在运行中")
)

// ExecCommand is the process factory used by the whole package; tests replace it to observe argv without spawning anything. | ExecCommand 是全包使用的进程工厂；测试替换它以观察 argv 而无需真正启动进程。
var ExecCommand = exec.CommandContext

// maxTailBytes caps how much command output is kept for error reporting. | maxTailBytes 限定为报错保留的命令输出上限。
const maxTailBytes = 8 << 10

// tailBuffer is an io.Writer that keeps only the trailing bytes, so a chatty tool cannot exhaust memory. | tailBuffer 是只保留末尾若干字节的 io.Writer，避免啰嗦的工具耗尽内存。
type tailBuffer struct {
	buf []byte
	mu  sync.Mutex
}

// Write appends p and trims the buffer to the last maxTailBytes bytes. | Write 追加 p 并把缓冲裁剪到末尾 maxTailBytes 字节。
func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > maxTailBytes {
		// Forward copy with the source after the destination: overlap-safe. | 前向复制，源在目标之后，重叠安全。
		b.buf = append(b.buf[:0], b.buf[len(b.buf)-maxTailBytes:]...)
	}
	return len(p), nil
}

// String returns the retained output. | String 返回保留的输出。
func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// truncateOutput trims s to at most limit bytes for log and error messages. | truncateOutput 把 s 裁剪到最多 limit 字节，用于日志与错误信息。
func truncateOutput(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "... | 已截断"
}

// exeSuffix returns the platform executable suffix. | exeSuffix 返回当前平台的可执行文件后缀。
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// newCommand creates a child process through the package factory. | newCommand 通过包级工厂创建子进程。
func newCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	factory := ExecCommand
	if factory == nil {
		factory = exec.CommandContext
	}
	return factory(ctx, name, args...)
}
