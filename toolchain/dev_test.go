package toolchain

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yun-dev/yd/logx"
)

// The helper child identifies itself through these two environment variables and appends a start marker to a file. | 辅助子进程通过这两个环境变量识别自己，并把启动标记写进文件。
const (
	devHelperEnv       = "YD_DEV_HELPER"
	devHelperMarkerEnv = "YD_DEV_HELPER_MARKER"
)

// TestDevHelperChildProcess is started as a real child process by DevRunner; it skips itself in a normal run. | TestDevHelperChildProcess 被 DevRunner 以真实子进程方式启动；普通测试运行时直接跳过。
func TestDevHelperChildProcess(t *testing.T) {
	if os.Getenv(devHelperEnv) != "1" {
		t.Skip("非辅助进程调用 | not the helper invocation")
	}
	if marker := os.Getenv(devHelperMarkerEnv); marker != "" {
		f, err := os.OpenFile(marker, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString("start\n")
			_ = f.Close()
		}
	}
	// This line verifies that the child stdout is forwarded. | 这行输出用于验证子进程 stdout 被转发。
	_, _ = os.Stdout.WriteString("dev-helper-started\n")
	// Block until the parent terminates this process. | 阻塞直到父进程终止自己。
	for {
		time.Sleep(100 * time.Millisecond)
	}
}

// syncBuffer is a concurrency-safe byte buffer used to collect child output. | syncBuffer 是并发安全的字节缓冲，用于接收子进程输出。
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// testLogger returns a debug-level logger writing to io.Discard so that test output stays clean. | testLogger 返回写往 io.Discard 的调试级日志器，避免污染测试输出。
func testLogger() *logx.Logger {
	return logx.New(logx.Options{Writer: io.Discard, Level: logx.DebugLevel})
}

// devHarness collects the child process behaviour observed during one dev session. | devHarness 汇总一次开发会话中被观测到的子进程行为。
type devHarness struct {
	mu      sync.Mutex
	cmds    []*exec.Cmd
	exited  map[int]bool
	marker  string
	factory CommandFunc
}

// started returns the child processes in launch order. | started 返回已启动的子进程，按启动顺序排列。
func (h *devHarness) started() []*exec.Cmd {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*exec.Cmd, len(h.cmds))
	copy(out, h.cmds)
	return out
}

// hasExited reports whether a PID has already been reaped. | hasExited 判断某个 PID 是否已被回收。
func (h *devHarness) hasExited(pid int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exited[pid]
}

// exitCount returns how many children have been reaped. | exitCount 返回已回收的子进程数量。
func (h *devHarness) exitCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.exited)
}

// newDevHarness builds a child process factory that does not depend on the real Go toolchain. | newDevHarness 构造一个不依赖真实 Go 工具链的子进程工厂。
func newDevHarness(t *testing.T, dir string) *devHarness {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("取测试二进制路径失败 | os.Executable: %v", err)
	}
	h := &devHarness{exited: map[int]bool{}, marker: filepath.Join(dir, "child.log")}
	h.factory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.Command(exe, "-test.run=^TestDevHelperChildProcess$")
		cmd.Env = append(os.Environ(), devHelperEnv+"=1", devHelperMarkerEnv+"="+h.marker)
		h.mu.Lock()
		h.cmds = append(h.cmds, cmd)
		h.mu.Unlock()
		return cmd
	}
	return h
}

// onExit records reaped children. | onExit 记录被回收的子进程。
func (h *devHarness) onExit(pid int, _ error) {
	h.mu.Lock()
	h.exited[pid] = true
	h.mu.Unlock()
}

// devOptions assembles a dev configuration backed by a fake builder and a fake child. | devOptions 组装一份使用假构建器与假子进程的开发配置。
func (h *devHarness) devOptions(dir string, out io.Writer, mutate func(*DevOptions)) DevOptions {
	o := DevOptions{
		Dir:         dir,
		NoTailwind:  true,
		Debounce:    40 * time.Millisecond,
		Log:         testLogger(),
		Out:         out,
		Build:       func(context.Context, DevOptions) (string, error) { return "fake-app", nil },
		Command:     h.factory,
		OnChildExit: h.onExit,
	}
	if mutate != nil {
		mutate(&o)
	}
	return o
}

func TestDevRestartsChildOnGoChange(t *testing.T) {
	dir := t.TempDir()
	h := newDevHarness(t, dir)
	out := &syncBuffer{}
	r, err := NewDevRunner(h.devOptions(dir, out, nil))
	if err != nil {
		t.Fatalf("NewDevRunner 返回错误 | NewDevRunner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	waitFor(t, 10*time.Second, "首次启动子进程 | the first child start", func() bool { return r.Starts() == 1 })
	waitFor(t, 10*time.Second, "子进程启动标记 | the child start marker", func() bool { return markerHas(h.marker, "start") })

	first := h.started()[0]
	if first.Process == nil {
		t.Fatal("子进程未真正启动 | the child process was never started")
	}
	firstPID := first.Process.Pid

	// Touch a Go source file → a rebuild and a child restart are expected. | 修改 Go 源文件 → 期望重新编译并重启子进程。
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	waitFor(t, 15*time.Second, "重启子进程 | the restarted child", func() bool { return r.Starts() == 2 })
	// Restarts is bumped after the new child has started, so it must be observed by polling: reading it
	// immediately after Starts() would race the increment. | Restarts 在新子进程启动之后才自增，因此必须
	// 轮询观察：紧跟 Starts() 之后立即读取会与自增竞争。
	waitFor(t, 15*time.Second, "重启计数 | the restart counter", func() bool { return r.Restarts() == 1 })

	// The old child must be terminated and reaped: a second start lands in the marker and the first PID has exited. | 旧子进程必须已被终止并回收：标记文件中出现第二个 start，且首个 PID 已退出。
	waitFor(t, 15*time.Second, "第二次启动标记 | the second start marker", func() bool {
		return len(h.started()) == 2 && strings.Count(markerText(h.marker), "start") >= 2
	})
	waitFor(t, 15*time.Second, "旧子进程被回收 | the previous child being reaped", func() bool { return h.hasExited(firstPID) })

	if txt := out.String(); !strings.Contains(txt, "dev-helper-started") {
		t.Errorf("子进程输出未被转发，实际为 %q | child output was not forwarded: %q", txt, txt)
	}

	// The Ctrl+C equivalent: cancel the context → Run returns gracefully and terminates the child. | 等价于 Ctrl+C：取消上下文 → Run 优雅返回并终止子进程。
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run 返回错误 | Run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("取消后 Run 未返回 | Run did not return after cancel")
	}

	// No child may survive the shutdown. | 退出后必须没有存活的子进程。
	waitFor(t, 15*time.Second, "退出时终止全部子进程 | every child being terminated on shutdown", func() bool {
		return h.exitCount() == 2 && h.hasExited(startedPID(h.started(), 1))
	})
}

// startedPID returns the PID of the i-th launched child, or -1 when out of range. | startedPID 返回第 i 次启动的子进程 PID，越界返回 -1。
func startedPID(cmds []*exec.Cmd, i int) int {
	if i < 0 || i >= len(cmds) || cmds[i].Process == nil {
		return -1
	}
	return cmds[i].Process.Pid
}

// markerText reads the marker file and returns an empty string on error. | markerText 读取标记文件内容，失败返回空串。
func markerText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// markerHas reports whether the marker file already contains the wanted text. | markerHas 判断标记文件是否已经出现指定内容。
func markerHas(path, want string) bool {
	return strings.Contains(markerText(path), want)
}

func TestDevReloadEndpointOnlyInDevMode(t *testing.T) {
	dir := t.TempDir()
	h := newDevHarness(t, dir)

	t.Run("关闭热重载时端点不存在 | the endpoint is absent when reload is off", func(t *testing.T) {
		r, err := NewDevRunner(h.devOptions(dir, io.Discard, func(o *DevOptions) {
			o.NoReload = true
			o.NoWatch = true
		}))
		if err != nil {
			t.Fatalf("NewDevRunner 返回错误 | NewDevRunner: %v", err)
		}
		if r.ReloadEnabled() {
			t.Fatal("关闭热重载后 ReloadEnabled 仍为 true | ReloadEnabled is still true")
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- r.Run(ctx) }()
		waitFor(t, 15*time.Second, "启动子进程 | the child start", func() bool { return r.Starts() == 1 })

		srv := httptest.NewServer(r.Handler())
		defer srv.Close()
		resp, err := srv.Client().Get(srv.URL + DevReloadPath)
		if err != nil {
			t.Fatalf("请求热重载端点失败 | request: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("状态码 = %d，期望 404 | want 404", resp.StatusCode)
		}
		cancel()
		<-done
	})

	t.Run("开启热重载时推送 reload 事件 | reload events are pushed when enabled", func(t *testing.T) {
		r, err := NewDevRunner(h.devOptions(dir, io.Discard, nil))
		if err != nil {
			t.Fatalf("NewDevRunner 返回错误 | NewDevRunner: %v", err)
		}
		if !r.ReloadEnabled() {
			t.Fatal("开发模式下 ReloadEnabled 为 false | ReloadEnabled is false in dev mode")
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- r.Run(ctx) }()
		waitFor(t, 15*time.Second, "启动子进程 | the child start", func() bool { return r.Starts() == 1 })

		srv := httptest.NewServer(r.Handler())
		defer srv.Close()

		// Every other path still 404s: only the dev endpoint exists. | 其它路径依然是 404：只有开发端点存在。
		other, err := srv.Client().Get(srv.URL + "/__yd/dev/other")
		if err != nil {
			t.Fatalf("请求失败 | request: %v", err)
		}
		_ = other.Body.Close()
		if other.StatusCode != http.StatusNotFound {
			t.Fatalf("其它路径状态码 = %d，期望 404 | want 404", other.StatusCode)
		}

		reqCtx, stopReq := context.WithCancel(context.Background())
		defer stopReq()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL+DevReloadPath, nil)
		if err != nil {
			t.Fatalf("构造请求失败 | new request: %v", err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("连接热重载端点失败 | connect: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200 | want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
			t.Fatalf("Content-Type = %q，期望 text/event-stream | want text/event-stream", ct)
		}

		lines := make(chan string, 128)
		go func() {
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
				lines <- sc.Text()
			}
			close(lines)
		}()

		// A template/style change → Tailwind rebuild plus a browser reload event. | 模板/样式变更 → Tailwind 重建 + 浏览器刷新事件。
		writeFile(t, filepath.Join(dir, "web", "static", "css", "app.css"), "@import \"tailwindcss\";\n")

		deadline := time.After(20 * time.Second)
		sawReload := false
		for !sawReload {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatal("SSE 流意外结束 | the SSE stream ended unexpectedly")
				}
				if strings.Contains(line, "event: "+ReloadEventName) {
					sawReload = true
				}
			case <-deadline:
				t.Fatal("未收到 reload 事件 | no reload event was received")
			}
		}

		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Run 返回错误 | Run: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("取消后 Run 未返回 | Run did not return after cancel")
		}
	})
}

func TestDevRunnerValidation(t *testing.T) {
	t.Run("项目目录不存在返回哨兵 | a missing project dir returns the sentinel", func(t *testing.T) {
		dir := t.TempDir()
		_, err := NewDevRunner(DevOptions{Dir: filepath.Join(dir, "nope")})
		if err == nil {
			t.Fatal("期望错误，实际为 nil | want an error, got nil")
		}
		if !strings.Contains(err.Error(), ErrProjectDir.Error()) {
			t.Fatalf("err = %v，期望包装 ErrProjectDir | want ErrProjectDir", err)
		}
	})

	t.Run("重复运行返回哨兵 | running twice returns the sentinel", func(t *testing.T) {
		dir := t.TempDir()
		h := newDevHarness(t, dir)
		r, err := NewDevRunner(h.devOptions(dir, io.Discard, func(o *DevOptions) { o.NoWatch = true }))
		if err != nil {
			t.Fatalf("NewDevRunner 返回错误 | NewDevRunner: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = r.Run(ctx) }()
		waitFor(t, 15*time.Second, "启动子进程 | the child start", func() bool { return r.Starts() == 1 })

		if err := r.Run(ctx); err == nil {
			t.Fatal("重复 Run 未返回错误 | a second Run returned no error")
		}
		cancel()
	})
}

func TestDevOptionsDefaults(t *testing.T) {
	dir := t.TempDir()
	r, err := NewDevRunner(DevOptions{Dir: dir, Log: testLogger(), Out: io.Discard})
	if err != nil {
		t.Fatalf("NewDevRunner 返回错误 | NewDevRunner: %v", err)
	}
	if r.opts.Debounce != DefaultDebounce {
		t.Errorf("Debounce = %v，期望 %v | want %v", r.opts.Debounce, DefaultDebounce, DefaultDebounce)
	}
	if len(r.opts.Roots) != 1 || !samePath(r.opts.Roots[0], dir) {
		t.Errorf("Roots = %v，期望 [%s] | want [%s]", r.opts.Roots, dir, dir)
	}
	if r.opts.TailwindInput != DefaultCSSInput || r.opts.TailwindOutput != DefaultCSSOutput {
		t.Errorf("Tailwind 默认路径未应用：%q / %q | default CSS paths were not applied", r.opts.TailwindInput, r.opts.TailwindOutput)
	}
	if !r.ReloadEnabled() {
		t.Error("默认应启用热重载 | reload must be enabled by default")
	}
	if r.Handler() == nil {
		t.Error("Handler 不应为 nil | Handler must not be nil")
	}
}
