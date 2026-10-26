//go:build windows

package app

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/kardianos/service"
	"github.com/shibingli/yd/runtime/server"
	"github.com/shibingli/ydx/errs"
)

// platformReexec restarts the binary as a child with the merged environment.
//
// Windows has no process-image replacement, and re-executing under the SCM would
// detach the service from its hosted process, so a service fails closed instead.
// An interactive console process spawns a child, waits for it and propagates its
// exit code. | Windows 没有进程镜像替换；在 SCM 下重启会让服务脱离被托管进程，因此
// 服务模式失败关闭。交互式控制台进程启动子进程、等待并透传退出码。
func platformReexec(env []string) error {
	if !service.Interactive() {
		return server.ErrHTTP2ExtendedConnectRequired
	}
	exe, err := os.Executable()
	if err != nil {
		return errs.WrapDetail(err, ErrReexecFailed, "resolve executable | 解析可执行文件")
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// The console delivers Ctrl+C, Ctrl+Break and close/logoff/shutdown to the wrapper and the
	// re-executed child at the same time. If the wrapper keeps the default handlers it exits first,
	// so `yd dev` sees the child as stopped and returns its prompt while the real server is still
	// draining. Ignore the catchable console signals here: the child owns the graceful shutdown,
	// and the wrapper waits for its exit code. TerminateProcess (Task Manager End Task) cannot be
	// caught, so no handler can cover that case. |
	// 控制台会把 Ctrl+C、Ctrl+Break 以及关闭/注销/关机同时送给包装进程与重新执行的子进程。包装进程
	// 若保留默认处理，会先于子进程退出，使 `yd dev` 误以为子进程已停止并提前返回提示符，而真正的
	// 服务器仍在排空。这里忽略可捕获的控制台信号：优雅退出由子进程负责，包装进程只等待它的退出码。
	// TerminateProcess（任务管理器“结束任务”）无法被捕获，因此没有任何处理器能覆盖该情况。
	signal.Ignore(os.Interrupt, syscall.SIGTERM)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return errs.WrapDetail(err, ErrReexecFailed, "run child process | 运行子进程")
	}
	os.Exit(0)
	return nil
}
