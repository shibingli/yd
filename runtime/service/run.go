// This file implements the hosting half of the package: the interactive probe, the kardianos
// "-service <action>" argument convention and Run, which hands a foreground function to the service
// manager and stops it through context cancellation only. | 本文件实现本包的托管部分：交互探测、
// kardianos 的「-service <action>」参数约定，以及把前台函数交给服务管理器、且只经 context 取消来停止它的 Run。

package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kardianos/service"
	"github.com/shibingli/yd/core/errx"
)

// serviceFlag is the argument name carrying a service action, following the kardianos convention
// `program -service install`. A single leading dash is the canonical spelling; the double dash form
// is accepted as well. | serviceFlag 是承载服务动作的参数名，遵循 kardianos 的
// `program -service install` 约定。规范写法是单横线，同时也接受双横线写法。
const serviceFlag = "service"

// defaultStopGrace bounds how long the service-mode Stop waits for the run function to return after
// its context was cancelled. It must stay above the framework's server.shutdown_timeout (10s by
// default), so that a well-behaved application is never cut short. | defaultStopGrace 限定服务模式的 Stop 在取消 context 后
// 等待 run 函数返回的时长。它必须大于框架的 server.shutdown_timeout（默认 10 秒），使行为正常的应用不会被打断。
const defaultStopGrace = 30 * time.Second

// stopGrace is the effective bound used by Run. It is a variable so tests can shorten the wait.
// | stopGrace 是 Run 实际使用的等待上限；声明为变量以便测试缩短等待。
var stopGrace = defaultStopGrace

// Interactive reports whether the process runs in a terminal rather than under the service manager.
// | Interactive 报告进程是否运行在终端中，而不是运行在服务管理器下。
//
// A generated main uses it to decide whether to host the application for the service manager or to
// run in the foreground. | 生成项目的入口用它决定是为服务管理器托管应用，还是以前台方式运行。
func Interactive() bool { return service.Interactive() }

// IsServiceArg scans args for the kardianos convention `-service <action>` (also spelled
// `--service <action>` or `-service=<action>`) and returns the action it carries.
// | IsServiceArg 扫描 args 中的 kardianos 约定 `-service <action>`（也写作
// `--service <action>` 或 `-service=<action>`），并返回它承载的动作。
//
// The boolean reports whether the flag is present at all, so a caller can tell "no service flag"
// from "service flag with an unusable value". The action word is returned verbatim (lower-cased and
// trimmed) rather than validated: Control and Run report an unknown word as ErrUnknownAction, which
// keeps the failure in one place. | 布尔值报告该标志是否存在，使调用方能够区分「没有服务标志」与
// 「服务标志取值不可用」。动作词原样返回（仅做小写与去空白），不做校验：未知动作由 Control 与 Run
// 报告为 ErrUnknownAction，使失败处理集中在一处。
func IsServiceArg(args []string) (Action, bool) {
	single, doubled := "-"+serviceFlag, "--"+serviceFlag
	for i, arg := range args {
		switch {
		case arg == single || arg == doubled:
			if i+1 == len(args) {
				return "", true
			}
			return normalizeAction(args[i+1]), true
		case strings.HasPrefix(arg, single+"="), strings.HasPrefix(arg, doubled+"="):
			return normalizeAction(arg[strings.Index(arg, "=")+1:]), true
		}
	}
	return "", false
}

// normalizeAction lower-cases and trims an action word. | normalizeAction 把动作词小写并去掉首尾空白。
func normalizeAction(word string) Action {
	return Action(strings.ToLower(strings.TrimSpace(word)))
}

// Run hosts run for the service manager and blocks until the service stops. | Run 为服务管理器托管 run，并阻塞至服务停止。
//
// Start launches run in its own goroutine; Stop cancels the context handed to run and waits for it to
// return, bounded by the stop grace period. os.Exit is never called from here, so the application
// keeps its own graceful shutdown path (drain the connections, run the shutdown hooks, close the
// resources) and the process exits normally right after Run returns. | Start 在独立 goroutine 中启动 run；
// Stop 取消交给 run 的 context 并等待其返回，等待时长受停止宽限期约束。这里绝不调用 os.Exit，
// 因此优雅退出仍归应用自己（排空连接、执行关闭钩子、释放资源），进程在 Run 返回后正常退出。
//
// A run function that returns an error makes Stop return that error, so a crashing service is
// reported to the service manager. | run 函数返回错误时 Stop 会返回同一个错误，从而把崩溃的服务报告给服务管理器。
func Run(o Options, run func(ctx context.Context) error) error {
	if run == nil {
		return ErrNilRun
	}

	svc, err := service.New(&runProgram{run: run, done: make(chan struct{}), grace: stopGrace}, configFor(o))
	if err != nil {
		return platformError(err)
	}
	return svc.Run()
}

// controlProgram is the stub program behind New. | controlProgram 是 New 背后的桩程序。
//
// Control only drives service manager actions, which never call Start or Stop, so both methods do
// nothing; a caller that really wants to host a program must use Run, which installs a program that
// owns the run function. | Control 只驱动服务管理器动作，这些动作从不调用 Start 或 Stop，因此两个方法都什么都不做；
// 真正要托管程序的调用方必须使用 Run，它会安装一个持有 run 函数的程序。
type controlProgram struct{}

// Start does nothing: the control path never hosts a program. | Start 什么都不做：控制路径从不托管程序。
func (controlProgram) Start(service.Service) error { return nil }

// Stop does nothing: the control path never hosts a program. | Stop 什么都不做：控制路径从不托管程序。
func (controlProgram) Stop(service.Service) error { return nil }

// runProgram adapts a foreground function to the kardianos program interface. | runProgram 把前台函数适配为 kardianos 的程序接口。
type runProgram struct {
	// cancel cancels the context handed to run; it is nil until Start ran. | cancel 取消交给 run 的 context；Start 之前为 nil。
	cancel context.CancelFunc
	// run is the hosted function; it returns when its context is cancelled or on failure. | run 是被托管的函数；它在 context 被取消时或失败时返回。
	run func(ctx context.Context) error
	// err is the error run returned, read by Stop after done is closed. | err 是 run 返回的错误，由 Stop 在 done 关闭后读取。
	err error
	// done is closed once run returned. | done 在 run 返回后关闭。
	done chan struct{}
	// grace bounds how long Stop waits for run to return. | grace 限定 Stop 等待 run 返回的时长。
	grace time.Duration
	// mu guards cancel and err. | mu 保护 cancel 与 err。
	mu sync.Mutex
}

// Start launches the hosted function and returns immediately, as the kardianos contract requires.
// | Start 启动被托管的函数并立即返回，这正是 kardianos 的接口约定。
func (p *runProgram) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()

	go p.serve(ctx)
	return nil
}

// Stop cancels the context of the hosted function and waits for it to return. | Stop 取消被托管函数的 context 并等待其返回。
//
// It never calls os.Exit: the wait is bounded by the grace period, and an expired grace period is
// reported as ErrStopTimeout instead of killing the process. | 它绝不调用 os.Exit：等待受宽限期约束，
// 宽限期到期时报告 ErrStopTimeout，而不是杀掉进程。
func (p *runProgram) Stop(service.Service) error {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	grace := p.grace
	if grace <= 0 {
		grace = defaultStopGrace
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()

	select {
	case <-p.done:
		return p.runError()
	case <-timer.C:
		return errx.Detail(ErrStopTimeout, "grace=%[1]s | 宽限期=%[1]s", grace)
	}
}

// Shutdown handles a machine shutdown exactly like a stop, so that a system reboot drains the
// application instead of killing it. | Shutdown 把机器关闭当作停止处理，使系统重启时排空应用而不是直接杀掉它。
func (p *runProgram) Shutdown(s service.Service) error { return p.Stop(s) }

// serve runs the hosted function and records its error. | serve 运行被托管的函数并记录其错误。
func (p *runProgram) serve(ctx context.Context) {
	defer close(p.done)

	err := p.run(ctx)
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
}

// runError returns the error the hosted function produced, if any. | runError 返回被托管函数产生的错误（若有）。
func (p *runProgram) runError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}
