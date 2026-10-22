// This file owns the child process lifecycle of the dev loop: spawning, reaping, stopping and the system-side liveness probe the leak guards rely on. | 本文件负责开发循环的子进程生命周期：启动、收尸、停止，以及泄漏守卫所依赖的系统侧存活探针。

package toolchain

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/log"
	"github.com/shibingli/ydx/protocol"
)

// childGracePeriod is how long a child may take to exit before it is killed, and how long a forced kill may take to be reaped. | childGracePeriod 是子进程被强杀前允许的退出时间，也是强杀后被收尸的等待上限。
const childGracePeriod = 3 * time.Second

// ProcessProbe reports whether the operating system still sees a process. | ProcessProbe 报告操作系统是否仍能看到某个进程。
type ProcessProbe func(pid int) bool

// ProcessAlive is the system-side liveness probe shared by the dev loop and the leak guards. | ProcessAlive 是开发循环与泄漏守卫共用的系统侧存活探针。
//
// It exists so that "the child is gone" can be asserted without os.FindProcess, which succeeds for every PID on Windows. | 它的存在使「子进程已消失」可以被断言，而不依赖 os.FindProcess——后者在 Windows 上对任意 PID 都会成功。
// Tests replace it to stub the system side. | 测试可替换它以对系统侧打桩。
var ProcessAlive ProcessProbe = defaultProcessAlive

// defaultProcessAlive asks the operating system whether a PID is still there. | defaultProcessAlive 询问操作系统某个 PID 是否仍然存在。
//
// Windows runs tasklist with a PID filter, other platforms run kill -0; both go through the package command factory (ExecCommand), the seam tests stub. | Windows 执行带 PID 过滤的 tasklist，其它平台执行 kill -0；两者都经过包级命令工厂（ExecCommand），即测试用来打桩的注入点。
// A probe that cannot run at all reports alive, because a false leak alarm is better than a silent one. | 探针完全无法运行时按存活处理：误报泄漏好过静默漏报。
func defaultProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	name, args := probeCommand(pid)
	out, err := newCommand(context.Background(), name, args...).Output()
	if runtime.GOOS == "windows" {
		if err != nil {
			return true
		}
		// The filter guarantees that only the line of that PID can appear. | 过滤器保证输出里只可能出现该 PID 的进程行。
		return strings.Contains(string(out), strconv.Itoa(pid))
	}
	if err == nil {
		return true
	}
	// A non-zero exit of kill -0 means the process is gone; any other error (a missing kill, for instance) counts as alive. | kill -0 以非零退出即进程不存在；其余错误（例如 kill 缺失）按存活处理。
	var exitErr *exec.ExitError
	return !errors.As(err, &exitErr)
}

// probeCommand returns the platform command that reports whether a PID is alive. | probeCommand 返回用于判断 PID 是否存活的平台命令。
func probeCommand(pid int) (string, []string) {
	if runtime.GOOS == "windows" {
		return "tasklist", []string{"/FI", "PID eq " + strconv.Itoa(pid), "/NH"}
	}
	return "kill", []string{"-0", strconv.Itoa(pid)}
}

// childProc tracks one running child process. | childProc 追踪一个运行中的子进程。
type childProc struct {
	err   error // Start error, valid once ready is closed. | 启动错误，ready 关闭后有效。
	cmd   *exec.Cmd
	done  chan struct{} // Closed by the reaper once Wait returned, so receiving from it proves the child was reaped. | 回收 goroutine 从 Wait 返回后关闭，因此读到它即证明子进程已被收尸。
	ready chan struct{} // Closed when startChild ends, success or failure, publishing pid and err. | startChild 结束时关闭（无论成败），同时发布 pid 与 err。
	pid   int           // Valid once ready is closed; 0 when the child never started. | ready 关闭后有效；子进程从未启动时为 0。
}

// startChild launches the compiled binary and forwards its output. | startChild 启动编译产物并转发其输出。
//
// The child is registered before cmd.Start is called, so no path can leave a spawned process unregistered: a concurrent stopChild, or a panic unwinding through Run, always sees it. | 子进程在 cmd.Start 之前就被登记，因此不存在「已 spawn 却未登记」的窗口：并发的 stopChild 或穿过 Run 的 panic 都必然能看到它。
// ready is closed by a deferred call, so even a panic inside startChild cannot leave stopChild waiting forever. | ready 由延迟调用关闭，因此即使 startChild 内部 panic，stopChild 也不会永远等待。
func (r *DevRunner) startChild(ctx context.Context, bin string) error {
	cmd := r.opts.Command(ctx, bin, r.opts.AppArgs...)
	cmd.Dir = r.opts.Dir
	cmd.Stdout = r.out
	cmd.Stderr = r.out
	cmd.Env = childEnv(cmd, r.opts.Port, r.opts.HTTP2ExtendedConnect)

	proc := &childProc{cmd: cmd, done: make(chan struct{}), ready: make(chan struct{})}
	defer close(proc.ready)
	r.mu.Lock()
	r.child = proc
	r.mu.Unlock()

	if err := cmd.Start(); err != nil {
		proc.err = err
		r.forgetChild(proc)
		return errs.WrapDetail(err, ErrChildStart, "exec %s | 启动 %s", bin, bin)
	}
	// Take the PID before starting the reaper: cmd must not be touched afterwards, or it would race with Wait. | PID 必须在启动回收 goroutine 之前取出：此后不再触碰 cmd，避免与 Wait 竞争。
	pid := cmd.Process.Pid
	r.mu.Lock()
	proc.pid = pid
	r.starts++
	r.mu.Unlock()
	// Start the reaper first: even if a later step panics, the child is still reaped by Wait. | 先起回收 goroutine：即使其后任何一步 panic，子进程也一定被 Wait 收尸。
	go r.waitChild(proc, pid)
	r.log.Info("dev: child started | dev: 子进程已启动",
		log.Str("bin", bin), log.Int("pid", pid), log.Int("port", r.opts.Port))
	return nil
}

// childEnv adds the dev markers to the environment the child inherits. | childEnv 在子进程继承的环境上追加开发标记。
//
// When http2Ext is true the inherited GODEBUG (if any) is merged with
// http2xconnect=1; other settings keep their order and value. | http2Ext 为 true 时，
// 继承的 GODEBUG（如有）会合并 http2xconnect=1；其余设置保持原有顺序与取值。
func childEnv(cmd *exec.Cmd, port int, http2Ext bool) []string {
	env := cmd.Environ()
	env = append(env, EnvDev+"=1")
	if port > 0 {
		env = append(env, EnvDevPort+"="+strconv.Itoa(port))
	}
	if http2Ext {
		env = mergeEnvGODEBUG(env)
	}
	return env
}

// mergeEnvGODEBUG replaces every GODEBUG entry with one merged value. | mergeEnvGODEBUG 用一条合并值替换所有 GODEBUG 条目。
func mergeEnvGODEBUG(env []string) []string {
	value := ""
	seen := false
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(k, "GODEBUG") {
			if !seen {
				value = v
				seen = true
			}
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GODEBUG="+protocol.MergeGODEBUG(value, "http2xconnect=1"))
}

// forgetChild drops the registration of one specific child, leaving a newer registration untouched. | forgetChild 撤销指定子进程的登记，不影响更新的登记。
func (r *DevRunner) forgetChild(p *childProc) {
	r.mu.Lock()
	if r.child == p {
		r.child = nil
	}
	r.mu.Unlock()
}

// takeChild detaches the registered child so that exactly one caller owns stopping it. | takeChild 摘取已登记的子进程，保证只有一个调用方负责停止它。
func (r *DevRunner) takeChild() *childProc {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.child
	r.child = nil
	return p
}

// waitChild reaps a child, publishes its exit and reports it. | waitChild 回收子进程、发布其退出并上报。
//
// done is closed before the callback runs, so a slow or panicking OnChildExit can never block a shutdown; pid is passed in because the reaper must not touch cmd. | done 在回调之前关闭，因此缓慢或 panic 的 OnChildExit 绝不会阻塞关闭流程；pid 以参数传入，因为回收 goroutine 不得再触碰 cmd。
func (r *DevRunner) waitChild(p *childProc, pid int) {
	err := p.cmd.Wait()
	r.forgetChild(p)
	// reaped must be incremented before done is closed, so the count is settled once stopChild stops waiting. | reaped 必须在 done 关闭之前自增，stopChild 的等待返回后计数才是确定的。
	r.mu.Lock()
	r.reaped++
	r.mu.Unlock()
	close(p.done)
	// The callback fires on every path: once a child started, the reaper goroutine is always running. | 回调在所有路径上都会触发：只要子进程启动成功，回收 goroutine 就一定被启动。
	if r.opts.OnChildExit != nil {
		r.opts.OnChildExit(pid, err)
	}
	if err != nil {
		r.log.Debug("dev: child exited | dev: 子进程已退出", log.Int("pid", pid), log.Err(err))
		return
	}
	r.log.Info("dev: child exited cleanly | dev: 子进程正常退出", log.Int("pid", pid))
}

// stopChild terminates the running child, politely first and forcibly after the grace period. | stopChild 终止运行中的子进程：先请求优雅退出，超过宽限期后强制结束。
//
// It never returns while the child is still reaped-able: the reaper's Wait is awaited, so no zombie and no orphan is left behind. | 只要子进程还有待收尸的可能，它就不会返回：它会等待回收 goroutine 的 Wait，因此不会留下僵尸或孤儿。
// With no child registered it is a no-op, which is what makes it safe to defer on every exit path of Run. | 没有登记子进程时它是空操作，因此可以安全地在 Run 的每条退出路径上 defer。
func (r *DevRunner) stopChild() {
	p := r.takeChild()
	if p == nil {
		return
	}
	// pid is not trustworthy until the spawn attempt has finished. | 启动尝试结束之前 pid 不可信。
	<-p.ready
	if p.err != nil || p.pid == 0 {
		return
	}
	// Windows has no SIGTERM; a Signal error means killing right away. | Windows 不支持 SIGTERM，Signal 返回错误即直接强杀。
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		_ = p.cmd.Process.Kill()
	}
	if !childReaped(p.done, childGracePeriod) {
		_ = p.cmd.Process.Kill()
		if !childReaped(p.done, childGracePeriod) {
			// Still not reaped within the grace period: record the fact in the log, never return silently. | 宽限期内仍未被收尸：把事实写进日志，绝不静默返回。
			r.log.Error("dev: child survived SIGTERM and Kill, it may leak | dev: 子进程在 SIGTERM 与 Kill 之后仍然存活，可能泄漏",
				log.Int("pid", p.pid), log.Bool("alive", ProcessAlive(p.pid)))
			return
		}
	}
	r.log.Info("dev: child stopped | dev: 子进程已停止", log.Int("pid", p.pid))
}

// childReaped reports whether done was closed within d, that is whether the child was reaped in time. | childReaped 判断 done 是否在 d 内被关闭，即子进程是否及时被收尸。
func childReaped(done <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
