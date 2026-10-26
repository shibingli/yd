//go:build windows

package toolchain

import (
	"os/exec"
	"strconv"
)

// prepareChildTree is a no-op on Windows: taskkill /T walks the process tree by parent PID, so no
// extra process-group setup is required. | prepareChildTree 在 Windows 上为空操作：taskkill /T 按父
// 进程 PID 遍历进程树，不需要额外的进程组设置。
func prepareChildTree(*exec.Cmd) {}

// signalChildTree reports that Windows has no graceful tree signal; the caller force-terminates the
// tree instead. | signalChildTree 表示 Windows 没有进程树级的优雅信号；调用方改为强制终止整棵树。
func signalChildTree(int) bool { return false }

// killChildTree terminates pid and every descendant through taskkill. | killChildTree 通过 taskkill
// 终止 pid 及其全部后代进程。
//
// The direct Process.Kill only terminates the wrapper; a child that re-executed itself to set
// GODEBUG keeps the real listener in its grandchild, which would otherwise keep the development port
// bound. | 直接 Process.Kill 只会终止包装进程；为设置 GODEBUG 而重新执行自身的子进程会把真正的监听器
// 留在孙进程里，否则孙进程会继续占住开发端口。
func killChildTree(pid int) bool {
	if pid <= 0 {
		return false
	}
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run() == nil
}
