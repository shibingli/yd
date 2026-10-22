package toolchain

// This file holds the leak guards: every scenario of the dev loop that spawns a child must prove that the child is reaped on the code side and gone on the system side. | 本文件承载泄漏守卫：开发循环中每一种会 spawn 子进程的场景，都必须在代码侧证明其被收尸，并在系统侧证明其已消失。

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shibingli/ydx/errs"
)

// guardSettle bounds how long the system side may lag behind the code side, and how long a reported exit may take to arrive. | guardSettle 限定系统侧允许落后代码侧的时间，以及上报退出允许的延迟。
//
// A leaked child lives far longer than this, so the bound can never hide one: it only keeps the guard from failing on a slow machine. | 泄漏的子进程存活时间远长于该上限，因此它绝不会掩盖泄漏，只是让守卫在慢机器上不误报。
const guardSettle = 3 * time.Second

// waitGone polls the system-side probe until the PID disappears or the timeout expires. | waitGone 轮询系统侧探针，直到 PID 消失或超时。
func waitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !ProcessAlive(pid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitExitObserved polls until the OnChildExit callback reported the PID. | waitExitObserved 轮询直到 OnChildExit 回调上报了该 PID。
func waitExitObserved(h *devHarness, pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if h.hasExited(pid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// leakScenario drives one way of spawning and shutting a child down, and reports the PIDs it spawned. | leakScenario 驱动一种 spawn 子进程并关闭它的方式，并报告它启动过的 PID。
type leakScenario struct {
	name string
	// spawns is how many children the scenario must have started; 0 means it must start none at all. | spawns 是该场景必须启动的子进程数量；0 表示它必须一个都不启动。
	spawns int
	// run drives the scenario with a fresh runner and returns that runner plus the PIDs it spawned. | run 用一个全新的运行器驱动场景，返回该运行器以及它启动过的 PID。
	run func(t *testing.T, dir string, h *devHarness) (*DevRunner, []int)
}

// leakScenarios covers every spawn path of the dev loop: a graceful cancel with and without watching, a cancel after a failed rebuild, a child that exits on its own, a child that cannot start and a panic out of Run. | leakScenarios 覆盖开发循环的每条 spawn 路径：带监听与不带监听的优雅取消、编译失败后取消、自行退出的子进程、无法启动的子进程，以及 Run 抛出的 panic。
var leakScenarios = []leakScenario{
	{"正常取消 | cancel while running", 1, scenarioCancel},
	{"无监听模式取消 | cancel in no-watch mode", 1, scenarioCancelNoWatch},
	{"编译失败后取消 | cancel after a failed rebuild", 1, scenarioCancelAfterFailedRebuild},
	{"子进程自行退出 | the child exits on its own", 1, scenarioChildExitsImmediately},
	{"启动失败 | the child cannot start", 0, scenarioChildStartFailure},
	{"构建器 panic | a panic out of the builder", 1, scenarioBuilderPanic},
}

// TestNoChildProcessLeaks runs every spawn scenario and asserts that not a single child survives it. | TestNoChildProcessLeaks 跑遍所有 spawn 场景，并断言没有任何一个子进程存活。
//
// Each child is judged twice: the code side must have observed the exit (the reaper's Wait, the Reaped counter and the OnChildExit callback) and the system side must no longer see the PID (tasklist on Windows, kill -0 elsewhere). | 每个子进程都判定两次：代码侧必须观测到退出（回收 goroutine 的 Wait、Reaped 计数与 OnChildExit 回调），系统侧必须不再看到该 PID（Windows 用 tasklist，其它平台用 kill -0）。
func TestNoChildProcessLeaks(t *testing.T) {
	for _, sc := range leakScenarios {
		t.Run(sc.name, func(t *testing.T) {
			dir := t.TempDir()
			h := newDevHarness(t, dir)
			r, pids := sc.run(t, dir, h)

			if len(pids) != sc.spawns {
				t.Fatalf("启动了 %d 个子进程，期望 %d | spawned %d children, want %d", len(pids), sc.spawns, len(pids), sc.spawns)
			}
			if sc.spawns == 0 && r.ChildPID() != 0 {
				t.Errorf("未启动任何子进程，却仍追踪着 %d | no child was started, yet %d is still tracked", r.ChildPID(), r.ChildPID())
			}
			for _, pid := range pids {
				if pid <= 0 {
					t.Fatalf("场景未产生可观测的 PID | the scenario produced no observable PID")
				}
				// (a) Code side: the exit must have been observed. | (a) 代码侧：退出必须被观测到。
				if !waitExitObserved(h, pid, guardSettle) {
					t.Errorf("未观察到子进程 %d 的退出 | the exit of child %d was never observed", pid, pid)
				}
				// (b) System side: the PID must be gone from the system process table. | (b) 系统侧：PID 必须已从系统进程表中消失。
				if !waitGone(pid, guardSettle) {
					t.Errorf("子进程 %d 在 Run 返回后仍被系统看到 | child %d is still visible to the system after Run returned", pid, pid)
				}
			}
			if pid := r.ChildPID(); pid != 0 {
				t.Errorf("Run 返回后仍追踪着子进程 %d | Run still tracks child %d after returning", pid, pid)
			}
			if starts, reaped := r.Starts(), r.Reaped(); starts != reaped {
				t.Errorf("Starts=%d 与 Reaped=%d 不等，说明有子进程未被收尸 | Starts=%d and Reaped=%d differ, so some child was never reaped", starts, reaped, starts, reaped)
			}
		})
	}
}

// scenarioCancel starts a child, cancels the context and expects a graceful shutdown. | scenarioCancel 启动子进程、取消上下文，并期望优雅关闭。
func scenarioCancel(t *testing.T, _ string, h *devHarness) (*DevRunner, []int) {
	t.Helper()
	r := h.newRunner(t, io.Discard, nil)
	s := startDevRun(t, r)
	pid := waitChildPID(t, r)
	if err := s.stop(t); err != nil {
		t.Fatalf("Run 返回错误 | Run: %v", err)
	}
	return r, []int{pid}
}

// scenarioCancelNoWatch covers the --no-watch path, which waits for the context without a watcher. | scenarioCancelNoWatch 覆盖 --no-watch 路径：没有监听器，只等待上下文。
func scenarioCancelNoWatch(t *testing.T, _ string, h *devHarness) (*DevRunner, []int) {
	t.Helper()
	r := h.newRunner(t, io.Discard, func(o *DevOptions) { o.NoWatch = true })
	s := startDevRun(t, r)
	pid := waitChildPID(t, r)
	if err := s.stop(t); err != nil {
		t.Fatalf("Run 返回错误 | Run: %v", err)
	}
	return r, []int{pid}
}

// scenarioCancelAfterFailedRebuild triggers a rebuild that fails; the old child must survive the failure and still be reaped on shutdown. | scenarioCancelAfterFailedRebuild 触发一次失败的重新编译；旧子进程必须扛过这次失败，并在关闭时仍被回收。
func scenarioCancelAfterFailedRebuild(t *testing.T, dir string, h *devHarness) (*DevRunner, []int) {
	t.Helper()
	var calls atomic.Int32
	build := func(context.Context, DevOptions) (string, error) {
		if calls.Add(1) > 1 {
			return "", errs.Detail(ErrBuildFailed, "stub rebuild failure | 打桩的重新编译失败")
		}
		return "fake-app", nil
	}
	r := h.newRunner(t, io.Discard, func(o *DevOptions) { o.Build = build })
	s := startDevRun(t, r)
	pid := waitChildPID(t, r)

	// Touch a Go source file to trigger a rebuild that is bound to fail. | 改动 Go 源文件触发重新编译，而这次编译必然失败。
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	waitFor(t, 15*time.Second, "第二次构建调用 | the second build call", func() bool { return calls.Load() >= 2 })
	// A failed build must not touch the running child. | 编译失败不得动到正在运行的子进程。
	if got := r.ChildPID(); got != pid {
		t.Fatalf("编译失败后子进程被替换 %d -> %d | the child was replaced after a failed build: %d -> %d", pid, got, pid, got)
	}
	if err := s.stop(t); err != nil {
		t.Fatalf("Run 返回错误 | Run: %v", err)
	}
	return r, []int{pid}
}

// scenarioChildExitsImmediately runs a child that exits on its own, which must still be reaped and reported. | scenarioChildExitsImmediately 运行一个自行退出的子进程：它同样必须被收尸并上报。
func scenarioChildExitsImmediately(t *testing.T, _ string, h *devHarness) (*DevRunner, []int) {
	t.Helper()
	r := h.newRunner(t, io.Discard, func(o *DevOptions) {
		o.NoWatch = true
		o.Command = exitingCommand(t)
	})
	s := startDevRun(t, r)
	waitFor(t, 15*time.Second, "自行退出的子进程被观测 | the self-exiting child being observed", func() bool { return h.exitCount() == 1 })
	if err := s.stop(t); err != nil {
		t.Fatalf("Run 返回错误 | Run: %v", err)
	}
	return r, h.exitedPIDs()
}

// scenarioChildStartFailure makes the child impossible to start; the run must fail with ErrChildStart and leave no registration behind. | scenarioChildStartFailure 让子进程无法启动；本次运行必须以 ErrChildStart 失败，且不留下任何登记。
func scenarioChildStartFailure(t *testing.T, _ string, h *devHarness) (*DevRunner, []int) {
	t.Helper()
	missing := filepath.Join(h.dir, "yd-no-such-binary")
	r := h.newRunner(t, io.Discard, func(o *DevOptions) {
		o.Command = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, missing)
		}
	})
	if err := r.Run(context.Background()); !errors.Is(err, ErrChildStart) {
		t.Fatalf("Run 返回 %v，期望 ErrChildStart | want ErrChildStart", err)
	}
	return r, nil
}

// scenarioBuilderPanic panics inside the injected builder while a child is running; the panic must unwind through Run without losing the child. | scenarioBuilderPanic 在子进程运行期间让注入的构建器 panic；panic 必须穿过 Run 展开，且不能丢掉该子进程。
func scenarioBuilderPanic(t *testing.T, dir string, h *devHarness) (*DevRunner, []int) {
	t.Helper()
	var calls atomic.Int32
	build := func(context.Context, DevOptions) (string, error) {
		if calls.Add(1) > 1 {
			panic("打桩的构建器 panic | stub builder panic")
		}
		return "fake-app", nil
	}
	r := h.newRunner(t, io.Discard, func(o *DevOptions) { o.Build = build })
	s := startDevRun(t, r)
	s.expectPanic()
	pid := waitChildPID(t, r)

	// Touch a Go source file to trigger the second build, which is bound to panic. | 改动 Go 源文件触发第二次构建，而它必定 panic。
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	if v := s.panicValue(t); v == nil {
		t.Fatal("期望 panic 穿出 Run | want a panic out of Run")
	}
	return r, []int{pid}
}

// devExitingHelperEnv marks the helper invocation that exits at once. | devExitingHelperEnv 标记立即退出的辅助进程调用。
const devExitingHelperEnv = "YD_DEV_EXIT_HELPER"

// TestDevExitingHelper exits immediately; the leak guards launch it as a real child process and a normal run skips it. | TestDevExitingHelper 立即退出；泄漏守卫把它作为真实子进程启动，普通测试运行则跳过它。
func TestDevExitingHelper(t *testing.T) {
	if os.Getenv(devExitingHelperEnv) != "1" {
		t.Skip("非辅助进程调用 | not the helper invocation")
	}
	_, _ = os.Stdout.WriteString("dev-exit-helper\n")
	os.Exit(0)
}

// exitingCommand returns a command factory whose child exits on its own right after starting. | exitingCommand 返回一个子进程启动后立即自行退出的命令工厂。
func exitingCommand(t *testing.T) CommandFunc {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("取测试二进制路径失败 | os.Executable: %v", err)
	}
	return func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestDevExitingHelper$")
		cmd.Env = append(os.Environ(), devExitingHelperEnv+"=1")
		return cmd
	}
}

// TestProcessAliveProbe checks the system-side probe against real processes, without os.FindProcess. | TestProcessAliveProbe 用真实进程校验系统侧探针，且不依赖 os.FindProcess。
func TestProcessAliveProbe(t *testing.T) {
	t.Run("自身进程存活 | the own process is alive", func(t *testing.T) {
		if !ProcessAlive(os.Getpid()) {
			t.Fatal("自身 PID 被判为不存在 | the own PID was reported as gone")
		}
	})

	t.Run("零与负数一律判为不存在 | zero and negatives are never alive", func(t *testing.T) {
		if ProcessAlive(0) || ProcessAlive(-1) {
			t.Fatal("0 或负数被判为存活 | zero or a negative PID was reported alive")
		}
	})

	t.Run("运行中的 PID 存活、收尸后消失 | a running PID is alive and a reaped one is gone", func(t *testing.T) {
		exe, err := os.Executable()
		if err != nil {
			t.Fatalf("取测试二进制路径失败 | os.Executable: %v", err)
		}
		cmd := exec.Command(exe, "-test.run=^TestDevHelperChildProcess$")
		cmd.Env = append(os.Environ(), devHelperEnv+"=1")
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			t.Fatalf("启动辅助进程失败 | start the helper: %v", err)
		}
		pid := cmd.Process.Pid
		// However the assertions end, the helper must be terminated and reaped. | 无论断言成败，辅助进程都必须被终止并收尸。
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
		if !ProcessAlive(pid) {
			t.Fatalf("运行中的子进程 %d 被判为不存在 | the running child %d was reported gone", pid, pid)
		}
		// Once killed and reaped, the probe must report it gone. | 终止并收尸之后，探针必须判定它已消失。
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if !waitGone(pid, 10*time.Second) {
			t.Fatalf("已收尸的 PID %d 仍被判为存活 | the reaped PID %d is still reported alive", pid, pid)
		}
	})
}

// procProbeStubEnv selects the behaviour of the stub process the probe tests launch instead of tasklist or kill. | procProbeStubEnv 选择探针测试用来替代 tasklist 或 kill 的桩进程行为。
const procProbeStubEnv = "YD_PROC_PROBE_STUB"

// TestProcProbeStubChild is the stub command the probe tests inject; a normal run skips it. | TestProcProbeStubChild 是探针测试注入的桩命令；普通运行会跳过它。
func TestProcProbeStubChild(t *testing.T) {
	mode := os.Getenv(procProbeStubEnv)
	if mode == "" {
		t.Skip("非桩进程调用 | not the stub invocation")
	}
	switch mode {
	case procProbeStubEcho:
		// Echo the arguments so the Windows branch sees the PID. | 回显参数：Windows 分支据此在看到 PID。
		_, _ = os.Stdout.WriteString(strings.Join(os.Args[1:], " ") + "\n")
		os.Exit(0)
	case procProbeStubQuiet:
		// Empty output with exit code 0: the Windows branch reports the process gone. | 空输出 + 退出码 0：Windows 分支据此判定进程不存在。
		os.Exit(0)
	default:
		// A non-zero exit: the Unix branch reports the process gone. | 非零退出：Unix 分支据此判定进程不存在。
		os.Exit(3)
	}
}

// Stub modes of TestProcProbeStubChild. | TestProcProbeStubChild 的桩模式。
const (
	// procProbeStubEcho echoes its arguments and exits 0. | procProbeStubEcho 回显参数并以 0 退出。
	procProbeStubEcho = "echo"
	// procProbeStubQuiet prints nothing and exits 0. | procProbeStubQuiet 不输出任何内容并以 0 退出。
	procProbeStubQuiet = "quiet"
	// procProbeStubFail exits with a non-zero code. | procProbeStubFail 以非零码退出。
	procProbeStubFail = "fail"
)

// TestProcessAliveUsesCommandFactory proves that the probe goes through the injectable command factory and that both verdicts are reachable. | TestProcessAliveUsesCommandFactory 证明探针经由可注入的命令工厂，且两种判定都可达。
//
// The stub replaces tasklist and kill altogether, so the test pins the probe contract instead of the operating system. | 桩完全取代 tasklist 与 kill，因此本测试锁定的是探针契约而非操作系统行为。
func TestProcessAliveUsesCommandFactory(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("取测试二进制路径失败 | os.Executable: %v", err)
	}
	// The "gone" stub: Windows judges by an empty output, Unix by a non-zero exit code. | 「已消失」的桩：Windows 依据空输出判定，Unix 依据非零退出码判定。
	mode := procProbeStubEcho
	goneMode := procProbeStubQuiet
	if runtime.GOOS != "windows" {
		goneMode = procProbeStubFail
	}
	original := ExecCommand
	ExecCommand = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		// Hand the probe's own arguments to the stub, which echoes them back. | 把探针自身的参数原样交给桩，桩再回显它们。
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestProcProbeStubChild$", "--", strings.Join(args, " "))
		cmd.Env = append(os.Environ(), procProbeStubEnv+"="+mode)
		return cmd
	}
	// Restore the seam: the cleanup still needs the real probe to judge leftover children. | 恢复注入点：清理阶段仍要用真实探针判断残留子进程。
	defer func() { ExecCommand = original }()

	const stubPID = 4242
	if !ProcessAlive(stubPID) {
		t.Fatalf("桩回显了 PID %d，探针却判为不存在 | the stub echoed PID %d but the probe reported it gone", stubPID, stubPID)
	}
	mode = goneMode
	if ProcessAlive(stubPID) {
		t.Fatal("桩报告进程不存在，探针却判为存活 | the stub reported the process gone but the probe reported alive")
	}
}
