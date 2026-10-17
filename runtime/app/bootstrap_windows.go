//go:build windows

package app

import (
	"errors"
	"os"
	"os/exec"

	"github.com/kardianos/service"
	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/runtime/server"
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
		return errx.WrapDetail(err, ErrReexecFailed, "resolve executable | 解析可执行文件")
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return errx.WrapDetail(err, ErrReexecFailed, "run child process | 运行子进程")
	}
	os.Exit(0)
	return nil
}
