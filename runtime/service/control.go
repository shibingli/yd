// This file implements the control half of the package: the action vocabulary, the single-action
// Control entry point and the mapping of third-party service manager errors onto the package's
// sentinels. | 本文件实现本包的控制部分：动作词表、单动作入口 Control，以及第三方服务管理器错误到本包哨兵错误的映射。

package service

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/kardianos/service"
	"github.com/shibingli/yd/core/errx"
)

// Action is one service manager action. | Action 是一个服务管理器动作。
type Action string

// The action vocabulary understood by Control and by IsServiceArg. | Control 与 IsServiceArg 理解的动作词表。
const (
	// ActionInstall registers the service with the service manager. | ActionInstall 把服务注册到服务管理器。
	ActionInstall Action = "install"
	// ActionUninstall removes the service from the service manager. | ActionUninstall 从服务管理器移除服务。
	ActionUninstall Action = "uninstall"
	// ActionStart asks the service manager to start the service. | ActionStart 请求服务管理器启动服务。
	ActionStart Action = "start"
	// ActionStop asks the service manager to stop the service. | ActionStop 请求服务管理器停止服务。
	ActionStop Action = "stop"
	// ActionRestart asks the service manager to stop and start the service. | ActionRestart 请求服务管理器重启服务。
	ActionRestart Action = "restart"
	// ActionStatus reads the current service status. | ActionStatus 读取服务当前状态。
	ActionStatus Action = "status"
	// ActionRun is not a service manager action: it tells a generated main to host the program under
	// the service manager, i.e. to call Run instead of Control. | ActionRun 不是服务管理器动作：
	// 它告诉生成项目的入口在服务管理器下托管程序，即调用 Run 而不是 Control。
	ActionRun Action = "run"
)

// controlHandle is the subset of service.Service that Control drives. It is an interface so that
// tests can inject a fake service and never touch the operating system's service state.
// | controlHandle 是 Control 驱动的 service.Service 子集。它被声明为接口，便于测试注入假服务而绝不改动操作系统的服务状态。
type controlHandle interface {
	// Install registers the service with the service manager. | Install 把服务注册到服务管理器。
	Install() error
	// Uninstall removes the service from the service manager. | Uninstall 从服务管理器移除服务。
	Uninstall() error
	// Start asks the service manager to start the service. | Start 请求服务管理器启动服务。
	Start() error
	// Stop asks the service manager to stop the service. | Stop 请求服务管理器停止服务。
	Stop() error
	// Restart asks the service manager to restart the service. | Restart 请求服务管理器重启服务。
	Restart() error
	// Status reads the current service status. | Status 读取服务当前状态。
	Status() (service.Status, error)
	// String returns the display name of the service. | String 返回服务的显示名。
	String() string
	// Platform returns the detected service manager. | Platform 返回探测到的服务管理器。
	Platform() string
}

// newControlHandle builds the handle Control drives; it is a variable so that tests substitute a
// fake and no test ever registers, starts or stops a real service. | newControlHandle 构建 Control 驱动的句柄；
// 它被声明为变量，使测试可以替换为假实现，任何测试都不会真实注册、启动或停止服务。
var newControlHandle = func(o Options) (controlHandle, error) {
	svc, err := New(o)
	if err != nil {
		return nil, err
	}
	return svc, nil
}

// Control performs exactly one service manager action and returns a readable bilingual result.
// | Control 只执行一个服务管理器动作，并返回可读的双语结果。
//
// The returned string is the summary of the action, e.g. "installed: myapp (windows-service)";
// ActionStatus instead returns the status text, see StatusText. | 返回的字符串是动作摘要，
// 例如 "installed: myapp (windows-service)"；ActionStatus 返回的则是状态文本，见 StatusText。
//
// Every failure satisfies errors.Is against one of ErrUnsupportedPlatform, ErrNotInstalled,
// ErrPermissionDenied, ErrUnknownAction or ErrActionFailed. | 所有失败都能用 errors.Is 判定到
// ErrUnsupportedPlatform、ErrNotInstalled、ErrPermissionDenied、ErrUnknownAction 或 ErrActionFailed 之一。
func Control(o Options, a Action) (string, error) {
	if !isControlAction(a) {
		return "", errx.Detail(ErrUnknownAction, "%q", string(a))
	}

	handle, err := newControlHandle(o)
	if err != nil {
		return "", err
	}

	switch a {
	case ActionInstall:
		if err := handle.Install(); err != nil {
			return "", actionError(err, a)
		}
		return summary(handle, "installed | 已安装"), nil
	case ActionUninstall:
		if err := handle.Uninstall(); err != nil {
			return "", actionError(err, a)
		}
		return summary(handle, "uninstalled | 已卸载"), nil
	case ActionStart:
		if err := handle.Start(); err != nil {
			return "", actionError(err, a)
		}
		return summary(handle, "started | 已启动"), nil
	case ActionStop:
		if err := handle.Stop(); err != nil {
			return "", actionError(err, a)
		}
		return summary(handle, "stopped | 已停止"), nil
	case ActionRestart:
		if err := handle.Restart(); err != nil {
			return "", actionError(err, a)
		}
		return summary(handle, "restarted | 已重启"), nil
	case ActionStatus:
		status, err := handle.Status()
		if err != nil {
			return "", actionError(err, a)
		}
		return StatusText(status), nil
	default:
		// Unreachable: isControlAction has already rejected every other action. | 不可达：其它动作已被 isControlAction 拒绝。
		return "", errx.Detail(ErrUnknownAction, "%q", string(a))
	}
}

// isControlAction reports whether a maps onto one service manager call. ActionRun deliberately does
// not: it selects the hosting path and is handled by Run. | isControlAction 报告 a 是否对应一次服务管理器调用。
// ActionRun 刻意不在其中：它选择托管路径，由 Run 处理。
func isControlAction(a Action) bool {
	switch a {
	case ActionInstall, ActionUninstall, ActionStart, ActionStop, ActionRestart, ActionStatus:
		return true
	default:
		return false
	}
}

// StatusText renders a service status as bilingual text. | StatusText 把服务状态渲染为双语文本。
func StatusText(status service.Status) string {
	switch status {
	case service.StatusRunning:
		return "running | 运行中"
	case service.StatusStopped:
		return "stopped | 已停止"
	default:
		return "unknown | 未知"
	}
}

// summary renders the result of one mutating action. | summary 渲染一次变更类动作的结果。
func summary(handle controlHandle, verb string) string {
	return fmt.Sprintf("%s: %s (%s)", verb, handle.String(), handle.Platform())
}

// platformError maps the errors returned while building a service handle. | platformError 映射构建服务句柄时返回的错误。
func platformError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, service.ErrNoServiceSystemDetected):
		return errx.Wrap(err, ErrUnsupportedPlatform)
	case errors.Is(err, service.ErrNameFieldRequired):
		// configFor always fills a name, so reaching this branch means the kardianos package changed
		// its contract: report it as a plain action failure instead of guessing. | configFor 始终会补齐名字，
		// 走到这一分支说明 kardianos 改变了约定：按普通动作失败上报，不做猜测。
		return errx.Wrap(err, ErrActionFailed)
	default:
		return errx.Wrap(err, ErrActionFailed)
	}
}

// actionError maps the error of one service manager action onto the package sentinels. | actionError 把一次服务管理器动作的错误映射到本包哨兵。
//
// Errors of the kardianos package are matched with errors.Is only; no error message is ever parsed
// to classify a failure. | 对 kardianos 的错误只用 errors.Is 匹配，绝不解析错误文案来判断失败类型。
func actionError(err error, a Action) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, service.ErrNotInstalled):
		return errx.Wrap(err, ErrNotInstalled)
	case errors.Is(err, service.ErrNoServiceSystemDetected):
		return errx.Wrap(err, ErrUnsupportedPlatform)
	case errors.Is(err, fs.ErrPermission):
		// Windows maps ERROR_ACCESS_DENIED onto fs.ErrPermission, and a POSIX helper that lost its
		// privileges reports the same sentinel. | Windows 把 ERROR_ACCESS_DENIED 映射为 fs.ErrPermission，
		// 失去特权的 POSIX 助手程序也会报告同一个哨兵。
		return errx.Wrap(err, ErrPermissionDenied)
	case needsElevation(a):
		return errx.WrapDetail(err, ErrActionFailed,
			"action=%s (administrator or root rights may be required) | 动作=%s（可能需要管理员或 root 权限）", a, a)
	default:
		return errx.WrapDetail(err, ErrActionFailed, "action=%s", a)
	}
}

// needsElevation reports whether an action changes machine-wide service state, i.e. whether a
// permission failure is plausible. | needsElevation 报告某动作是否改动机器级服务状态，即权限失败是否可能。
func needsElevation(a Action) bool {
	switch a {
	case ActionInstall, ActionUninstall, ActionStart, ActionStop, ActionRestart:
		return true
	default:
		return false
	}
}
