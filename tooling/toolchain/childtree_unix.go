//go:build unix

package toolchain

import (
	"os/exec"
	"syscall"
)

// prepareChildTree puts the child in its own process group so descendants can be signalled as one
// tree. The development server does not read the terminal, so leaving the shell's foreground group
// has no effect on interaction; `yd dev` stops the group through stopChild. |
// prepareChildTree 把子进程放进独立进程组，使后代进程可以作为一个进程树统一收信号。
// 开发服务器不读取终端，因此离开 shell 的前台进程组不影响交互；`yd dev` 通过 stopChild 停止整组。
func prepareChildTree(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalChildTree asks the whole child tree to stop gracefully. | signalChildTree 请求整棵子进程树优雅停止。
func signalChildTree(pid int) bool {
	return pid > 0 && syscall.Kill(-pid, syscall.SIGTERM) == nil
}

// killChildTree force-terminates the whole child tree. | killChildTree 强制终止整棵子进程树。
func killChildTree(pid int) bool {
	return pid > 0 && syscall.Kill(-pid, syscall.SIGKILL) == nil
}
