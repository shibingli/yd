//go:build windows || unix

package toolchain

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDevTreeGrandchild is the grandchild used by TestStopChildTerminatesGrandchild: it
// holds a TCP listener open so the parent can prove the child tree was really terminated. |
// TestDevTreeGrandchild 是 TestStopChildTerminatesGrandchild 使用的孙进程：它占住一个
// TCP 监听端口，父进程据此证明整棵子进程树确实已被终止。
func TestDevTreeGrandchild(t *testing.T) {
	if os.Getenv("YD_DEV_TREE_GRANDCHILD") != "1" {
		t.Skip("非孙进程调用 | not the grandchild invocation")
	}
	port, err := strconv.Atoi(os.Getenv("YD_DEV_TREE_PORT"))
	if err != nil || port <= 0 {
		t.Fatalf("孙进程端口无效：%v | invalid grandchild port: %v", err, err)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("孙进程监听失败：%v | grandchild listen: %v", err, err)
	}
	defer func() { _ = ln.Close() }()
	if path := os.Getenv("YD_DEV_TREE_READY"); path != "" {
		if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			t.Fatalf("写入孙进程标记失败：%v | writing grandchild marker: %v", err, err)
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}

// TestDevTreeParent is the direct child used by TestStopChildTerminatesGrandchild: it
// starts the grandchild and waits, so killing only this process leaves the listener alive. |
// TestDevTreeParent 是 TestStopChildTerminatesGrandchild 使用的直接子进程：它启动孙进程并
// 等待，因此只杀死本进程时监听端口仍会存活。
func TestDevTreeParent(t *testing.T) {
	if os.Getenv("YD_DEV_TREE_PARENT") != "1" {
		t.Skip("非父进程调用 | not the parent invocation")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("取测试二进制路径失败：%v | os.Executable: %v", err, err)
	}
	cmd := exec.Command(exe, "-test.run=^TestDevTreeGrandchild$")
	cmd.Env = append(os.Environ(),
		"YD_DEV_TREE_GRANDCHILD=1",
		"YD_DEV_TREE_PORT="+os.Getenv("YD_DEV_TREE_PORT"),
		"YD_DEV_TREE_READY="+os.Getenv("YD_DEV_TREE_READY"),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动孙进程失败：%v | starting grandchild: %v", err, err)
	}
	_ = cmd.Wait()
}

// TestStopChildTerminatesGrandchild locks the process-tree contract on Unix and Windows: a dev child
// that re-executes itself starts a grandchild, and stopping the runner must terminate the whole
// tree. Killing only the direct child leaves the grandchild holding the development port, which is
// exactly how a config hot reload ends in "bind: Only one usage of each socket address". |
// TestStopChildTerminatesGrandchild 固定 Unix 与 Windows 的进程树契约：重新执行自身的开发子进程会
// 启动孙进程，停止运行器必须终止整棵树。只杀死直接子进程会让孙进程继续占住开发端口，这正是配置热重载
// 后出现「bind: Only one usage of each socket address」的原因。
func TestStopChildTerminatesGrandchild(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("取测试二进制路径失败：%v | os.Executable: %v", err, err)
	}
	port := freeTCPPort(t)
	dir := t.TempDir()
	ready := filepath.Join(dir, "grandchild.pid")
	var grandchildPID int
	t.Cleanup(func() {
		if grandchildPID > 0 {
			if p, err := os.FindProcess(grandchildPID); err == nil {
				_ = p.Kill()
			}
		}
	})

	r, err := NewDevRunner(DevOptions{
		Dir:        dir,
		NoTailwind: true,
		NoWatch:    true,
		Log:        testLogger(),
		Out:        io.Discard,
		Command: func(context.Context, string, ...string) *exec.Cmd {
			cmd := exec.Command(exe, "-test.run=^TestDevTreeParent$")
			cmd.Env = append(os.Environ(),
				"YD_DEV_TREE_PARENT=1",
				"YD_DEV_TREE_PORT="+strconv.Itoa(port),
				"YD_DEV_TREE_READY="+ready,
			)
			return cmd
		},
	})
	if err != nil {
		t.Fatalf("创建 DevRunner 失败：%v | NewDevRunner: %v", err, err)
	}
	if err := r.startChild(context.Background(), "unused"); err != nil {
		t.Fatalf("启动子进程失败：%v | startChild: %v", err, err)
	}
	waitFor(t, 15*time.Second, "孙进程监听端口 | the grandchild listening", func() bool {
		data, err := os.ReadFile(ready)
		if err != nil {
			return false
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			return false
		}
		grandchildPID = pid
		return true
	})

	r.stopChild()

	waitFor(t, 10*time.Second, "开发端口被释放 | the development port being released", func() bool {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return false
		}
		_ = ln.Close()
		return true
	})
	if ProcessAlive(grandchildPID) {
		t.Errorf("孙进程 %d 仍在运行 | grandchild %d is still alive", grandchildPID, grandchildPID)
	}
}

// TestDevGracefulHelper is the child of TestStopChildGracefullyWaitsForExit. It writes a ready
// marker, ignores the shutdown signal long enough to exceed the short force-kill grace, then writes
// a completion marker and exits on its own. | TestDevGracefulHelper 是
// TestStopChildGracefullyWaitsForExit 的子进程：它先写就绪标记，忽略退出信号直到超过短强制宽限期，
// 再写完成标记并自行退出。
func TestDevGracefulHelper(t *testing.T) {
	if os.Getenv("YD_DEV_GRACEFUL_HELPER") != "1" {
		t.Skip("非优雅退出辅助进程调用 | not the graceful helper invocation")
	}
	ready := os.Getenv("YD_DEV_GRACEFUL_READY")
	done := os.Getenv("YD_DEV_GRACEFUL_DONE")
	if err := os.WriteFile(ready, []byte("ready"), 0o644); err != nil {
		t.Fatalf("写入就绪标记失败：%v | writing ready marker: %v", err, err)
	}
	// Catch the Unix SIGTERM; Windows has no signal to catch here because the test uses a
	// programmatic cancellation. Either way the helper finishes after the forced-kill window. |
	// 捕获 Unix 的 SIGTERM；Windows 上没有需要捕获的信号，因为该测试使用程序化取消。两种情况下
	// 辅助进程都在强制终止宽限期之后才自行结束。
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	time.Sleep(4 * time.Second)
	if err := os.WriteFile(done, []byte("done"), 0o644); err != nil {
		t.Fatalf("写入完成标记失败：%v | writing done marker: %v", err, err)
	}
}

// TestStopChildGracefullyWaitsForExit locks the Ctrl+C contract: a cancelled session gives the child
// tree its full shutdown window and does not force-kill it after the short restart grace. The helper
// takes longer than childGracePeriod but less than ShutdownGrace. |
// TestStopChildGracefullyWaitsForExit 固定 Ctrl+C 契约：取消会话会给子进程树完整的退出窗口，
// 不会在短的重启宽限期后就强制终止。辅助进程的退出时间超过 childGracePeriod，但小于 ShutdownGrace。
func TestStopChildGracefullyWaitsForExit(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("取测试二进制路径失败：%v | os.Executable: %v", err, err)
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	done := filepath.Join(dir, "done")
	var pid int
	t.Cleanup(func() {
		if pid > 0 {
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill()
			}
		}
	})

	r, err := NewDevRunner(DevOptions{
		Dir:           dir,
		NoTailwind:    true,
		NoWatch:       true,
		ShutdownGrace: 10 * time.Second,
		Log:           testLogger(),
		Out:           io.Discard,
		Command: func(context.Context, string, ...string) *exec.Cmd {
			cmd := exec.Command(exe, "-test.run=^TestDevGracefulHelper$")
			cmd.Env = append(os.Environ(),
				"YD_DEV_GRACEFUL_HELPER=1",
				"YD_DEV_GRACEFUL_READY="+ready,
				"YD_DEV_GRACEFUL_DONE="+done,
			)
			return cmd
		},
	})
	if err != nil {
		t.Fatalf("创建 DevRunner 失败：%v | NewDevRunner: %v", err, err)
	}
	if err := r.startChild(context.Background(), "unused"); err != nil {
		t.Fatalf("启动子进程失败：%v | startChild: %v", err, err)
	}
	pid = r.ChildPID()
	waitFor(t, 10*time.Second, "优雅退出辅助进程就绪 | the graceful helper being ready", func() bool {
		_, err := os.Stat(ready)
		return err == nil
	})

	start := time.Now()
	r.stopChildGracefully()
	if elapsed := time.Since(start); elapsed < 3*time.Second {
		t.Errorf("优雅退出只等待了 %v，期望超过强制宽限期 | graceful stop returned after %v, want more than the force grace", elapsed, elapsed)
	}
	if _, err := os.Stat(done); err != nil {
		t.Errorf("子进程未完成自身退出就被终止：%v | child was terminated before finishing: %v", err, err)
	}
	if got := r.Reaped(); got != 1 {
		t.Errorf("Reaped = %d，期望 1 | want 1", got)
	}
}

// freeTCPPort returns a currently free loopback TCP port. | freeTCPPort 返回一个当前空闲的回环 TCP 端口。
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("取空闲端口失败：%v | finding a free port: %v", err, err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}
