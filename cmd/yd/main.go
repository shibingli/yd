// Command yd is the framework's command line entry point: it assembles the process environment, hands it
// to the cli package and turns the returned error into a process exit code. | yd 命令是框架的命令行入口：
// 它组装进程环境，交给 cli 包执行，并把返回的错误转换为进程退出码。
//
// Nothing else lives here on purpose: every behavior belongs to a command under cli/, so the entry point
// stays small enough to be read in one sitting. | 这里刻意不放其它内容：所有行为都属于 cli/ 下的某个命令，
// 使入口文件小到可以一次读完。
package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/core/version"
	"github.com/shibingli/yd/tooling/cli"
)

// Process exit codes. | 进程退出码。
const (
	// exitOK reports success. | exitOK 表示成功。
	exitOK = 0
	// exitFailure reports a command that ran and failed. | exitFailure 表示命令已运行但失败。
	exitFailure = 1
	// exitUsage reports a usage error: unknown command, missing argument or an unparsable flag. | exitUsage
	// 表示用法错误：未知命令、缺少参数或标志无法解析。
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Stdin))
}

// run executes one invocation and returns the process exit code. | run 执行一次调用并返回进程退出码。
//
// It takes its streams as arguments rather than reaching for the process globals, so the entry point can
// be exercised without touching the real stdio. | 它把标准流作为参数接收而不是直接取用进程全局变量，
// 因此可以在不触碰真实标准输入输出的前提下验证入口逻辑。
func run(args []string, stdout, stderr io.Writer, stdin *os.File) int {
	env := &cli.Env{
		FS:      afs.NewOS(""),
		Out:     stdout,
		Err:     stderr,
		In:      stdin,
		WorkDir: workDir(),
		Version: version.String(),
		IsTTY:   isTerminal(stdin),
	}

	// Ctrl+C and SIGTERM cancel the context, so a long-running command (dev, docker) can shut down
	// cleanly instead of being killed mid-write. | Ctrl+C 与 SIGTERM 会取消上下文，使长时间运行的命令
	// （dev、docker）能够干净退出，而不是在写入过程中被强杀。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := cli.Execute(ctx, args, env)
	if err == nil {
		return exitOK
	}

	// Diagnostics go through logx, which Execute has already pointed at the CLI's stderr stream. | 诊断
	// 信息经 logx 输出，Execute 已把日志器指向 CLI 的标准错误流。
	logx.Default().Error("yd: command failed | yd：命令执行失败", logx.Err(err))
	if isUsageError(err) {
		return exitUsage
	}
	return exitFailure
}

// isUsageError reports whether err describes a misuse of the command line rather than a failed command. | isUsageError 报告 err 是否属于命令行用法错误而非命令执行失败。
func isUsageError(err error) bool {
	return errors.Is(err, cli.ErrUnknownCommand) ||
		errors.Is(err, cli.ErrNoCommand) ||
		errors.Is(err, cli.ErrFlagParse) ||
		errors.Is(err, cli.ErrEmptyOptions)
}

// isTerminal reports whether f is attached to an interactive terminal. | isTerminal 报告 f 是否连接到交互终端。
//
// It uses the standard library instead of a dedicated dependency: a character device is exactly what a
// console is, and a pipe or a redirected file is not. | 它使用标准库而不引入额外依赖：控制台正是字符设备，
// 而管道或重定向文件不是。
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// workDir returns the directory the CLI resolves configuration and asset paths against. | workDir 返回 CLI 解析配置与资产路径所依据的目录。
func workDir() string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		logx.Default().Warn("yd: working directory unavailable, falling back to \".\" | yd：无法获取工作目录，回退到 \".\"",
			logx.Err(err))
		return "."
	}
	return wd
}
