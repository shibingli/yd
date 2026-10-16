// This file tests the hosting half of the service package: the "-service <action>" argument
// convention, the interactive probe and the program adapter that stops the hosted function through
// context cancellation only. No test starts a real service. | 本文件测试 service 包的托管部分：
// 「-service <action>」参数约定、交互探测，以及只经 context 取消来停止被托管函数的程序适配器。任何测试都不会启动真实服务。

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kardianos/service"
)

// TestIsServiceArgParsesTheKardianosConvention 断言 -service 的各种写法都被识别，且动作词被规范化。
// TestIsServiceArgParsesTheKardianosConvention asserts every spelling of -service is recognized and that the action word is normalized.
func TestIsServiceArgParsesTheKardianosConvention(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		action Action
		found  bool
	}{
		{"单横线 | single dash", []string{"-service", "install"}, ActionInstall, true},
		{"双横线 | double dash", []string{"--service", "stop"}, ActionStop, true},
		{"等号形式 | equals form", []string{"-service=uninstall"}, ActionUninstall, true},
		{"双横线等号 | double dash equals", []string{"--service=restart"}, ActionRestart, true},
		{"运行动作 | run action", []string{"-service", "run"}, ActionRun, true},
		{"大小写与空白 | case and blanks", []string{"-service", " Install "}, ActionInstall, true},
		{"紧跟其它参数 | after other arguments", []string{"--config", "configs/config.yaml", "-service", "status"}, ActionStatus, true},
		{"缺少取值 | missing value", []string{"-service"}, "", true},
		{"未知动作 | unknown action", []string{"-service", "reload"}, Action("reload"), true},
		{"没有该标志 | flag absent", []string{"--config", "configs/config.yaml"}, "", false},
		{"空参数 | empty arguments", nil, "", false},
	}

	for _, tc := range cases {
		action, found := IsServiceArg(tc.args)
		if found != tc.found {
			t.Errorf("%s: found = %v, 期望 %v", tc.name, found, tc.found)
			continue
		}
		if action != tc.action {
			t.Errorf("%s: action = %q, 期望 %q", tc.name, action, tc.action)
		}
	}
}

// TestInteractiveMirrorsTheServicePackage 断言交互探测直接沿用 kardianos 的判定结果。
// TestInteractiveMirrorsTheServicePackage asserts the interactive probe mirrors the kardianos verdict.
func TestInteractiveMirrorsTheServicePackage(t *testing.T) {
	if got, want := Interactive(), service.Interactive(); got != want {
		t.Errorf("Interactive() = %v, 期望 %v", got, want)
	}
}

// TestRunProgramCancelsThenWaits 断言 Stop 先取消 context，再等待 run 返回。
// TestRunProgramCancelsThenWaits asserts Stop cancels the context and then waits for run to return.
func TestRunProgramCancelsThenWaits(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})

	program := &runProgram{
		run: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil
		},
		done:  make(chan struct{}),
		grace: time.Second,
	}

	if err := program.Start(nil); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	<-started

	if err := program.Stop(nil); err != nil {
		t.Fatalf("Stop 应等到 run 正常返回, 实际 %v", err)
	}
	select {
	case <-stopped:
	default:
		t.Error("Stop 返回时 run 的 context 尚未被取消")
	}
}

// TestRunProgramReportsTheRunError 断言 run 的错误经 Stop 上报给服务管理器。
// TestRunProgramReportsTheRunError asserts the error of run reaches the service manager through Stop.
func TestRunProgramReportsTheRunError(t *testing.T) {
	failure := errors.New("listen tcp: address already in use")
	program := &runProgram{
		run:   func(context.Context) error { return failure },
		done:  make(chan struct{}),
		grace: time.Second,
	}

	if err := program.Start(nil); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	err := program.Stop(nil)
	if !errors.Is(err, failure) {
		t.Fatalf("Stop 应返回 run 的错误, 实际 %v", err)
	}
}

// TestRunProgramStopIsBounded 断言宽限期到期后 Stop 返回而非无限等待，也不杀进程。
// TestRunProgramStopIsBounded asserts Stop returns once the grace period expires instead of waiting forever, and never kills the process.
func TestRunProgramStopIsBounded(t *testing.T) {
	release := make(chan struct{})
	program := &runProgram{
		run: func(context.Context) error {
			<-release
			return nil
		},
		done:  make(chan struct{}),
		grace: 20 * time.Millisecond,
	}

	if err := program.Start(nil); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	started := time.Now()
	err := program.Stop(nil)
	elapsed := time.Since(started)
	if !errors.Is(err, ErrStopTimeout) {
		t.Fatalf("期望 ErrStopTimeout, 实际 %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Stop 未在宽限期内返回, 耗时 %s", elapsed)
	}

	close(release)
	<-program.done
}

// TestRunProgramStopWithoutStart 断言 Start 之前调用 Stop 也会在宽限期内返回，不阻塞在未关闭的 done 上。
// TestRunProgramStopWithoutStart asserts Stop before Start also returns within the grace period instead of blocking on a done channel nobody closes.
func TestRunProgramStopWithoutStart(t *testing.T) {
	program := &runProgram{run: func(context.Context) error { return nil }, done: make(chan struct{}), grace: 20 * time.Millisecond}

	if err := program.Stop(nil); !errors.Is(err, ErrStopTimeout) {
		t.Fatalf("Start 之前 Stop 应在宽限期后返回 ErrStopTimeout, 实际 %v", err)
	}
}

// TestRunProgramShutdownStopsLikeStop 断言机器关闭与停止走同一条路径。
// TestRunProgramShutdownStopsLikeStop asserts a machine shutdown follows the very same path as a stop.
func TestRunProgramShutdownStopsLikeStop(t *testing.T) {
	program := &runProgram{
		run: func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		},
		done:  make(chan struct{}),
		grace: time.Second,
	}

	if err := program.Start(nil); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if err := program.Shutdown(nil); err != nil {
		t.Fatalf("Shutdown 应等到 run 返回, 实际 %v", err)
	}
}
