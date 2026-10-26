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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibingli/ydx/log"
)

// The helper child identifies itself through these two environment variables and appends a start marker to a file. | 辅助子进程通过这两个环境变量识别自己，并把启动标记写进文件。
const (
	devHelperEnv       = "YD_DEV_HELPER"
	devHelperMarkerEnv = "YD_DEV_HELPER_MARKER"
)

// devHelperMaxLifetime bounds how long the helper blocks before exiting on its own. | devHelperMaxLifetime 限定辅助进程在自行退出之前阻塞的最长时间。
//
// The bound is a safety net: a bug in the reaping path or in a guard can then only orphan the helper for a while instead of forever. | 该上限是一张安全网：回收路径或守卫自身出现缺陷时，辅助进程最多只会短暂成为孤儿，而不是永远。
const devHelperMaxLifetime = 2 * time.Minute

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
	// Block until the parent terminates this process, or until the safety-net lifetime expires. | 阻塞直到父进程终止自己，或直到安全网上限到期。
	deadline := time.Now().Add(devHelperMaxLifetime)
	for time.Now().Before(deadline) {
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
func testLogger() *log.Logger {
	return log.New(log.Options{Writer: io.Discard, Level: log.DebugLevel})
}

// devHarness collects the child process behaviour observed during one dev session. | devHarness 汇总一次开发会话中被观测到的子进程行为。
type devHarness struct {
	mu      sync.Mutex
	cmds    []*exec.Cmd
	exited  map[int]bool
	dir     string
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

// exitedPIDs returns a snapshot of the PIDs whose exit the callback reported. | exitedPIDs 返回回调已上报过退出的 PID 快照。
func (h *devHarness) exitedPIDs() []int {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]int, 0, len(h.exited))
	for pid := range h.exited {
		out = append(out, pid)
	}
	return out
}

// newDevHarness builds a child process factory that does not depend on the real Go toolchain. | newDevHarness 构造一个不依赖真实 Go 工具链的子进程工厂。
func newDevHarness(t *testing.T, dir string) *devHarness {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("取测试二进制路径失败 | os.Executable: %v", err)
	}
	h := &devHarness{exited: map[int]bool{}, dir: dir, marker: filepath.Join(dir, "child.log")}
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

// newRunner builds a runner for one scenario and registers the cleanup that reaps every child which outlived Run. | newRunner 为一个场景构建运行器，并登记清理：回收所有比 Run 活得更久的子进程。
//
// The cleanup is registered before any session cleanup, so cleanups run in the right order: the session stops Run first, the harness reaps afterwards. | 该清理比任何会话清理登记得更早，因此清理顺序正确：会话先停掉 Run，随后由测试夹具回收。
func (h *devHarness) newRunner(t *testing.T, out io.Writer, mutate func(*DevOptions)) *DevRunner {
	t.Helper()
	r, err := NewDevRunner(h.devOptions(h.dir, out, mutate))
	if err != nil {
		t.Fatalf("NewDevRunner 返回错误 | NewDevRunner: %v", err)
	}
	t.Cleanup(func() { h.reapLeftovers(t, r) })
	return r
}

// reapLeftovers kills any child that outlived Run and reports it as a test failure. | reapLeftovers 终止一切比 Run 活得更久的子进程，并将其上报为测试失败。
//
// It runs even when the test body failed, which is what keeps a failing assertion or a t.Fatal from turning into an orphan process. | 即使测试主体失败它也会运行，从而避免断言失败或 t.Fatal 变成孤儿进程。
func (h *devHarness) reapLeftovers(t *testing.T, r *DevRunner) {
	t.Helper()
	if pid := r.ChildPID(); pid > 0 {
		t.Errorf("Run 返回后仍追踪着子进程 %d | Run still tracks child %d after returning", pid, pid)
		killAndWait(pid)
	}
	// A PID the callback reported as reaped must really be gone from the system process table. | 回调报告过退出的 PID 必须真的从系统进程表中消失。
	for _, pid := range h.exitedPIDs() {
		if !ProcessAlive(pid) {
			continue
		}
		t.Errorf("子进程 %d 已被回调上报退出但仍在运行 | child %d was reported reaped but is still running", pid, pid)
		killAndWait(pid)
	}
}

// killAndWait terminates one leftover child process and waits for the system to stop seeing it. | killAndWait 终止一个残留子进程，并等待系统不再看到它。
//
// os.FindProcess is only used here as a handle to a process this test started, never as a liveness check. | 这里只把 os.FindProcess 当作本测试所启动进程的句柄，绝不把它当作存活判断。
func killAndWait(pid int) {
	p, err := os.FindProcess(pid)
	if err == nil {
		_ = p.Kill()
	}
	_ = waitGone(pid, 10*time.Second)
}

// devOptions assembles a dev configuration backed by a fake builder and a fake child. | devOptions 组装一份使用假构建器与假子进程的开发配置。
func (h *devHarness) devOptions(dir string, out io.Writer, mutate func(*DevOptions)) DevOptions {
	o := DevOptions{
		Dir:        dir,
		NoTailwind: true,
		Debounce:   40 * time.Millisecond,
		// Keep the suite fast: the helper ignores Ctrl+C, so the graceful wait must expire quickly
		// before the tree is force-terminated. | 保持套件快速：辅助进程忽略 Ctrl+C，优雅等待必须尽快
		// 到期，然后强制终止进程树。
		ShutdownGrace: 100 * time.Millisecond,
		Log:           testLogger(),
		Out:           out,
		Build:         func(context.Context, DevOptions) (string, error) { return "fake-app", nil },
		Command:       h.factory,
		OnChildExit:   h.onExit,
	}
	if mutate != nil {
		mutate(&o)
	}
	return o
}

// devRun owns one Run call: its cleanup cancels the context and waits for Run to return, so the test binary can never exit while a child is still being reaped. | devRun 掌管一次 Run 调用：清理时取消上下文并等待 Run 返回，因此测试二进制绝不会在子进程仍待回收时退出。
type devRun struct {
	runner   *DevRunner
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
	panicV   any
	panicked bool
	allow    bool
}

// startDevRun launches Run in the background and registers the cleanup that stops it. | startDevRun 在后台启动 Run，并登记停止它的清理。
//
// The panic recovery exists so that a panicking Run fails only its own test instead of killing the whole test binary. | panic 恢复的存在，使 panic 的 Run 只让本测试失败，而不会杀死整个测试二进制。
func startDevRun(t *testing.T, r *DevRunner) *devRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := &devRun{runner: r, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		defer func() {
			if v := recover(); v != nil {
				s.panicV, s.panicked = v, true
			}
		}()
		s.err = r.Run(ctx)
	}()
	t.Cleanup(func() { s.cleanup(t) })
	return s
}

// expectPanic marks the panic of this session as expected, so cleanup does not report it. | expectPanic 把本会话的 panic 标记为预期，清理时不再上报。
func (s *devRun) expectPanic() { s.allow = true }

// stop cancels the context, waits for Run to return and reports its error. | stop 取消上下文、等待 Run 返回并报告其错误。
func (s *devRun) stop(t *testing.T) error {
	t.Helper()
	s.cancel()
	s.wait(t, 30*time.Second)
	return s.err
}

// wait blocks until Run returned. | wait 阻塞直到 Run 返回。
func (s *devRun) wait(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(timeout):
		t.Fatalf("等待超时：Run 未返回 | timed out waiting for Run to return")
	}
}

// panicValue waits for Run to finish unwinding and returns the recovered panic value. | panicValue 等待 Run 完成栈展开并返回恢复到的 panic 值。
func (s *devRun) panicValue(t *testing.T) any {
	t.Helper()
	s.wait(t, 30*time.Second)
	return s.panicV
}

// cleanup is the t.Cleanup body: it always stops Run and reports an unexpected panic without aborting the remaining cleanups. | cleanup 是 t.Cleanup 的主体：它总是停掉 Run，并在出现未预期 panic 时上报而不中断后续清理。
func (s *devRun) cleanup(t *testing.T) {
	t.Helper()
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(30 * time.Second):
		t.Errorf("清理超时：Run 未返回 | cleanup timed out waiting for Run to return")
		return
	}
	if s.panicked && !s.allow {
		t.Errorf("Run 发生了未预期的 panic：%v | Run panicked unexpectedly: %v", s.panicV, s.panicV)
	}
}

// waitChildPID waits until the runner tracks a running child and returns its PID. | waitChildPID 等待运行器追踪到运行中的子进程并返回其 PID。
func waitChildPID(t *testing.T, r *DevRunner) int {
	t.Helper()
	waitFor(t, 15*time.Second, "子进程 PID 可观测 | an observable child PID", func() bool { return r.ChildPID() > 0 })
	return r.ChildPID()
}

func TestDevRestartsChildOnGoChange(t *testing.T) {
	dir := t.TempDir()
	h := newDevHarness(t, dir)
	out := &syncBuffer{}
	r := h.newRunner(t, out, nil)
	s := startDevRun(t, r)

	waitFor(t, 10*time.Second, "首次启动子进程 | the first child start", func() bool { return r.Starts() == 1 })
	waitFor(t, 10*time.Second, "子进程启动标记 | the child start marker", func() bool { return markerHas(h.marker, "start") })

	firstPID := waitChildPID(t, r)

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
	if err := s.stop(t); err != nil {
		t.Fatalf("Run 返回错误 | Run: %v", err)
	}

	// No child may survive the shutdown. | 退出后必须没有存活的子进程。
	waitFor(t, 15*time.Second, "退出时终止全部子进程 | every child being terminated on shutdown", func() bool {
		return h.exitCount() == 2 && h.hasExited(startedPID(h.started(), 1))
	})
	// Run must not return while it still holds a child. | Run 不得在仍持有子进程时返回。
	if pid := r.ChildPID(); pid != 0 {
		t.Errorf("Run 返回后仍追踪着子进程 %d | Run still tracks child %d after returning", pid, pid)
	}
}

func TestDevRestartsChildOnTemplateChange(t *testing.T) {
	dir := t.TempDir()
	// Create the template directory before the watcher starts: on Linux and macOS a directory
	// created and populated inside one notification burst can lose the file event before the new
	// watch is registered. This test owns the restart contract, not that watcher race. |
	// 在监听器启动前创建模板目录：在 Linux 与 macOS 上，于同一通知爆发中创建目录并写入文件，
	// 可能在新监听注册前丢失文件事件。本测试验证重启契约，而不是该监听竞态。
	writeFile(t, filepath.Join(dir, "web", "templates", "home.html"), "<p>old</p>\n")
	h := newDevHarness(t, dir)
	out := &syncBuffer{}
	r := h.newRunner(t, out, nil)
	s := startDevRun(t, r)

	waitFor(t, 10*time.Second, "首次启动子进程 | the first child start", func() bool { return r.Starts() == 1 })
	waitFor(t, 10*time.Second, "子进程启动标记 | the child start marker", func() bool { return markerHas(h.marker, "start") })
	firstPID := waitChildPID(t, r)

	// A template edit restarts the child so the cached renderer compiles the new tree once,
	// instead of paying that compile on every request. | 模板修改会重启子进程，让缓存渲染器重新
	// 编译一次新模板树，而不是在每个请求上重复编译。
	writeFile(t, filepath.Join(dir, "web", "templates", "home.html"), "<p>home</p>")
	waitFor(t, 15*time.Second, "模板变更后重启子进程 | the restarted child after a template edit", func() bool { return r.Starts() == 2 })
	waitFor(t, 15*time.Second, "重启计数 | the restart counter", func() bool { return r.Restarts() == 1 })
	waitFor(t, 15*time.Second, "旧子进程被回收 | the previous child being reaped", func() bool { return h.hasExited(firstPID) })

	if err := s.stop(t); err != nil {
		t.Fatalf("Run 返回错误 | Run: %v", err)
	}
	waitFor(t, 15*time.Second, "退出时终止全部子进程 | every child being terminated on shutdown", func() bool { return h.exitCount() == 2 })
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
		r := h.newRunner(t, io.Discard, func(o *DevOptions) {
			o.NoReload = true
			o.NoWatch = true
		})
		if r.ReloadEnabled() {
			t.Fatal("关闭热重载后 ReloadEnabled 仍为 true | ReloadEnabled is still true")
		}
		s := startDevRun(t, r)
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
		if err := s.stop(t); err != nil {
			t.Fatalf("Run 返回错误 | Run: %v", err)
		}
	})

	t.Run("开启热重载时推送 reload 事件 | reload events are pushed when enabled", func(t *testing.T) {
		// Create the stylesheet directory before the watcher starts: fsnotify only
		// starts watching a new directory after it observes its creation, so a
		// directory plus file created in one burst can lose the file event. The
		// contract under test is reload delivery, not the new-directory race. | 在监听器
		// 启动前创建样式目录：fsnotify 只有在观察到目录创建后才开始监听，目录与文件
		// 在同一瞬间创建时可能丢失文件事件。本测试验证的是刷新事件投递，而不是新建目录竞态。
		cssPath := filepath.Join(dir, "web", "static", "css", "app.css")
		writeFile(t, cssPath, "/* initial | 初始 */\n")
		r := h.newRunner(t, io.Discard, nil)
		if !r.ReloadEnabled() {
			t.Fatal("开发模式下 ReloadEnabled 为 false | ReloadEnabled is false in dev mode")
		}
		s := startDevRun(t, r)
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

		// A template/style change → Tailwind rebuild plus a browser reload event. Retry the
		// write periodically: an OS watcher may drop a notification that races with the
		// watcher's startup, and the contract here is reload delivery, not one specific
		// inotify event. | 模板/样式变更 → Tailwind 重建 + 浏览器刷新事件。周期性重写文件：
		// 操作系统监听器可能丢弃与启动竞争的第一次通知；本测试的契约是刷新事件最终送达，
		// 而不是某一个具体的 inotify 事件。
		writeFile(t, cssPath, "@import \"tailwindcss\";\n")
		retry := time.NewTicker(300 * time.Millisecond)
		defer retry.Stop()

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
			case <-retry.C:
				writeFile(t, cssPath, "@import \"tailwindcss\";\n")
			case <-deadline:
				t.Fatal("未收到 reload 事件 | no reload event was received")
			}
		}

		if err := s.stop(t); err != nil {
			t.Fatalf("Run 返回错误 | Run: %v", err)
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
		r := h.newRunner(t, io.Discard, func(o *DevOptions) { o.NoWatch = true })
		s := startDevRun(t, r)
		waitFor(t, 15*time.Second, "启动子进程 | the child start", func() bool { return r.Starts() == 1 })

		if err := r.Run(context.Background()); err == nil {
			t.Fatal("重复 Run 未返回错误 | a second Run returned no error")
		}
		if err := s.stop(t); err != nil {
			t.Fatalf("Run 返回错误 | Run: %v", err)
		}
	})
}

// TestDevBuildArgsResolvesFreshProject proves the development build can run before go.sum exists,
// while an existing go.sum keeps the default readonly module mode. | TestDevBuildArgsResolvesFreshProject
// 证明开发构建能在 go.sum 尚不存在时运行，而已有 go.sum 时保持默认的只读模块模式。
func TestDevBuildArgsResolvesFreshProject(t *testing.T) {
	dir := t.TempDir()
	args := devBuildArgs(dir, "out", "./cmd/app")
	if !slices.Contains(args, "-mod=mod") {
		t.Fatalf("缺 go.sum 时 argv = %v，期望包含 -mod=mod | argv = %v, want -mod=mod", args, args)
	}
	if got := args[len(args)-1]; got != "./cmd/app" {
		t.Fatalf("最后一个参数 = %q，期望构建入口 | last arg = %q, want the package", got, got)
	}

	if err := os.WriteFile(filepath.Join(dir, "go.sum"), nil, 0o644); err != nil {
		t.Fatalf("写入 go.sum 失败 | writing go.sum: %v", err)
	}
	if args := devBuildArgs(dir, "out", "./cmd/app"); slices.Contains(args, "-mod=mod") {
		t.Fatalf("已有 go.sum 时 argv = %v，不应包含 -mod=mod | argv = %v, must not contain -mod=mod", args, args)
	}
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
