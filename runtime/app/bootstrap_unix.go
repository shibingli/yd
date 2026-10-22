//go:build unix

package app

import (
	"os"
	"syscall"

	"github.com/shibingli/ydx/errs"
)

// platformReexec replaces the current process image with the same binary and the
// supplied environment. syscall.Exec preserves the PID, so systemd, launchd and
// containers keep their signal and lifecycle semantics. | platformReexec 用同一二进制
// 与给定环境替换当前进程镜像。syscall.Exec 保持 PID，因此 systemd、launchd 与容器的信号和
// 生命周期语义不变。
func platformReexec(env []string) error {
	exe, err := os.Executable()
	if err != nil {
		return errs.WrapDetail(err, ErrReexecFailed, "resolve executable | 解析可执行文件")
	}
	if err := syscall.Exec(exe, os.Args, env); err != nil {
		return errs.WrapDetail(err, ErrReexecFailed, "exec %[1]s | 执行 %[1]s", exe)
	}
	return nil
}
