// This file tests the control half of the service package with an injected fake handle: the action
// vocabulary, the error mapping and the readable results are asserted without ever registering,
// starting or stopping a real service. | 本文件用注入的假句柄测试 service 包的控制部分：动作词表、错误映射与可读结果
// 都在不真实注册、启动或停止服务的前提下被断言。

package service

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/kardianos/service"
	"github.com/shibingli/yd/errx"
)

// fakeHandle records the service manager calls made on it. | fakeHandle 记录在它身上发生的服务管理器调用。
type fakeHandle struct {
	// calls lists the method names in call order. | calls 按调用顺序列出方法名。
	calls []string
	// errs maps an action onto the error its method returns. | errs 把动作映射到其方法返回的错误。
	errs map[Action]error
	// platform is returned by Platform. | platform 由 Platform 返回。
	platform string
	// name is returned by String. | name 由 String 返回。
	name string
	// status is returned by Status. | status 由 Status 返回。
	status service.Status
	// statusErr is returned by Status alongside status. | statusErr 与 status 一同由 Status 返回。
	statusErr error
}

// record appends a call and returns the error configured for that action. | record 记录一次调用并返回为该动作配置的错误。
func (f *fakeHandle) record(a Action) error {
	f.calls = append(f.calls, a.method())
	return f.errs[a]
}

// Install records the call. | Install 记录调用。
func (f *fakeHandle) Install() error { return f.record(ActionInstall) }

// Uninstall records the call. | Uninstall 记录调用。
func (f *fakeHandle) Uninstall() error { return f.record(ActionUninstall) }

// Start records the call. | Start 记录调用。
func (f *fakeHandle) Start() error { return f.record(ActionStart) }

// Stop records the call. | Stop 记录调用。
func (f *fakeHandle) Stop() error { return f.record(ActionStop) }

// Restart records the call. | Restart 记录调用。
func (f *fakeHandle) Restart() error { return f.record(ActionRestart) }

// Status returns the canned status. | Status 返回预置状态。
func (f *fakeHandle) Status() (service.Status, error) {
	f.calls = append(f.calls, ActionStatus.method())
	return f.status, f.statusErr
}

// String returns the canned display name. | String 返回预置显示名。
func (f *fakeHandle) String() string { return f.name }

// Platform returns the canned platform. | Platform 返回预置平台。
func (f *fakeHandle) Platform() string { return f.platform }

// method maps an action onto the kardianos method it drives. | method 把动作映射到它驱动的 kardianos 方法。
func (a Action) method() string {
	switch a {
	case ActionInstall:
		return "Install"
	case ActionUninstall:
		return "Uninstall"
	case ActionStart:
		return "Start"
	case ActionStop:
		return "Stop"
	case ActionRestart:
		return "Restart"
	case ActionStatus:
		return "Status"
	default:
		return string(a)
	}
}

// useHandle injects a fake handle for the duration of one test and returns the options it received.
// | useHandle 在单个测试期间注入假句柄，并返回它收到的选项。
func useHandle(t *testing.T, fake *fakeHandle, buildErr error) *Options {
	t.Helper()

	original := newControlHandle
	t.Cleanup(func() { newControlHandle = original })

	seen := &Options{}
	newControlHandle = func(o Options) (controlHandle, error) {
		*seen = o
		if buildErr != nil {
			return nil, buildErr
		}
		return fake, nil
	}
	return seen
}

// TestControlHandsTheOptionsToTheService 断言 Control 把收到的选项原样交给服务构建点。
// TestControlHandsTheOptionsToTheService asserts Control hands its options to the service factory unchanged.
func TestControlHandsTheOptionsToTheService(t *testing.T) {
	fake := &fakeHandle{name: "shop-service", platform: "windows-service"}
	seen := useHandle(t, fake, nil)

	opts := Options{Name: "shop-service", DisplayName: "Shop", Executable: "/opt/shop/bin/shop-admin"}
	if _, err := Control(opts, ActionInstall); err != nil {
		t.Fatalf("install 期望成功, 实际 %v", err)
	}
	if seen.Name != opts.Name || seen.DisplayName != opts.DisplayName || seen.Executable != opts.Executable {
		t.Errorf("服务构建点收到的选项 = %+v, 期望 %+v", *seen, opts)
	}
}

// TestControlDrivesTheMatchingServiceCall 断言每个动作只驱动对应的一个 kardianos 调用。
// TestControlDrivesTheMatchingServiceCall asserts every action drives exactly its one kardianos call.
func TestControlDrivesTheMatchingServiceCall(t *testing.T) {
	cases := []struct {
		action Action
		method string
	}{
		{ActionInstall, "Install"},
		{ActionUninstall, "Uninstall"},
		{ActionStart, "Start"},
		{ActionStop, "Stop"},
		{ActionRestart, "Restart"},
	}

	for _, tc := range cases {
		fake := &fakeHandle{name: "shop-service", platform: "linux-systemd"}
		useHandle(t, fake, nil)

		result, err := Control(Options{Name: "shop-service"}, tc.action)
		if err != nil {
			t.Fatalf("%s: 期望成功, 实际 %v", tc.action, err)
		}
		if len(fake.calls) != 1 || fake.calls[0] != tc.method {
			t.Errorf("%s 应只调用 %s, 实际 %v", tc.action, tc.method, fake.calls)
		}
		// The summary must be bilingual and carry the service and platform names. | 摘要必须中英同步，并带上服务名与平台名。
		if !strings.Contains(result, " | ") || !strings.Contains(result, "shop-service") || !strings.Contains(result, "linux-systemd") {
			t.Errorf("%s 的结果应为中英同步的摘要: %q", tc.action, result)
		}
	}
}

// TestControlStatusReturnsStatusText 断言 status 动作返回状态文本，而不是动作摘要。
// TestControlStatusReturnsStatusText asserts the status action returns the status text rather than an action summary.
func TestControlStatusReturnsStatusText(t *testing.T) {
	cases := []struct {
		status service.Status
		want   string
	}{
		{service.StatusRunning, "running | 运行中"},
		{service.StatusStopped, "stopped | 已停止"},
		{service.StatusUnknown, "unknown | 未知"},
	}

	for _, tc := range cases {
		fake := &fakeHandle{name: "shop-service", platform: "linux-systemd", status: tc.status}
		useHandle(t, fake, nil)

		got, err := Control(Options{Name: "shop-service"}, ActionStatus)
		if err != nil {
			t.Fatalf("status: 期望成功, 实际 %v", err)
		}
		if got != tc.want {
			t.Errorf("status 文本 = %q, 期望 %q", got, tc.want)
		}
	}
}

// TestControlRejectsUnknownActions 断言未知动作与 run 都在构建句柄之前被拒绝。
// TestControlRejectsUnknownActions asserts unknown actions and run are rejected before any handle is built.
func TestControlRejectsUnknownActions(t *testing.T) {
	for _, action := range []Action{ActionRun, "", "reload", "Install"} {
		fake := &fakeHandle{}
		useHandle(t, fake, nil)

		result, err := Control(Options{Name: "shop-service"}, action)
		if !errors.Is(err, ErrUnknownAction) {
			t.Errorf("%q: 期望 ErrUnknownAction, 实际 %v", action, err)
		}
		if result != "" {
			t.Errorf("%q: 失败时不应返回结果 %q", action, result)
		}
		if len(fake.calls) != 0 {
			t.Errorf("%q: 不应驱动任何服务管理器调用, 实际 %v", action, fake.calls)
		}
	}
}

// TestControlMapsServiceErrors 断言 kardianos 的错误被映射到本包哨兵，未知错误归入动作失败。
// TestControlMapsServiceErrors asserts kardianos errors map onto the package sentinels and that unknown errors become action failures.
func TestControlMapsServiceErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"未安装 | not installed", service.ErrNotInstalled, ErrNotInstalled},
		{"无服务管理器 | no service manager", service.ErrNoServiceSystemDetected, ErrUnsupportedPlatform},
		{"权限不足 | permission denied", fs.ErrPermission, ErrPermissionDenied},
		{"其它失败 | other failure", errors.New("boom"), ErrActionFailed},
	}

	for _, tc := range cases {
		fake := &fakeHandle{errs: map[Action]error{ActionStart: tc.err}}
		useHandle(t, fake, nil)

		_, err := Control(Options{Name: "shop-service"}, ActionStart)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: 期望 %v, 实际 %v", tc.name, tc.want, err)
		}
		if !errors.Is(err, tc.err) {
			t.Errorf("%s: 原始错误应留在错误链上, 实际 %v", tc.name, err)
		}
	}
}

// TestControlReportsBuildFailure 断言构建句柄失败时 Control 原样返回该错误（含平台不支持）。
// TestControlReportsBuildFailure asserts Control returns a failed handle build as-is (including an unsupported platform).
func TestControlReportsBuildFailure(t *testing.T) {
	useHandle(t, nil, errx.Wrap(service.ErrNoServiceSystemDetected, ErrUnsupportedPlatform))

	_, err := Control(Options{Name: "shop-service"}, ActionInstall)
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("期望 ErrUnsupportedPlatform, 实际 %v", err)
	}
}

// TestNeedsElevation 断言只有改动机器级状态的动作才提示需要提权。
// TestNeedsElevation asserts only actions changing machine-wide state carry the elevation hint.
func TestNeedsElevation(t *testing.T) {
	elevated := map[Action]bool{ActionInstall: true, ActionUninstall: true, ActionStart: true, ActionStop: true, ActionRestart: true}
	for action, want := range elevated {
		if got := needsElevation(action); got != want {
			t.Errorf("%s: needsElevation = %v, 期望 %v", action, got, want)
		}
	}
	if needsElevation(ActionStatus) {
		t.Error("status 不改动服务状态，不应提示提权")
	}
}

// TestPlatformErrorKeepsTheUnderlyingError 断言平台错误的原始错误保留在错误链上。
// TestPlatformErrorKeepsTheUnderlyingError asserts the underlying error of a platform failure stays on the chain.
func TestPlatformErrorKeepsTheUnderlyingError(t *testing.T) {
	err := platformError(service.ErrNoServiceSystemDetected)
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("期望 ErrUnsupportedPlatform, 实际 %v", err)
	}
	if !errors.Is(err, service.ErrNoServiceSystemDetected) {
		t.Errorf("原始错误应保留在错误链上: %v", err)
	}
}
