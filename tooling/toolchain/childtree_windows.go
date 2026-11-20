//go:build windows

package toolchain

import (
	"context"
	"os/exec"
	"strconv"
	"time"
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
	// taskkill is an external command: a system under load can make it slow, and an unbounded call
	// would block Run indefinitely. The timeout only decides this attempt; the caller has its own
	// fallback. | taskkill 是外部命令：系统繁忙时可能很慢，无界调用会让 Run 永久阻塞。超时只决定
	// 本次尝试，调用方仍有自己的兜底。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run() == nil
}
