package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibingli/yd/config"
	"github.com/shibingli/yd/toolchain"
	"github.com/spf13/afero"
	"github.com/spf13/pflag"
)

// devOutBuffer is a concurrency-safe writer: the dev loop forwards child output into it from its own goroutine while the test reads it. | devOutBuffer 是并发安全的写入端：开发主循环在自己的 goroutine 里把子进程输出转发进来，测试则并行读取。
type devOutBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p and reports how many bytes were written. | Write 追加 p 并返回写入的字节数。
func (b *devOutBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything written so far. | String 返回截至目前的全部内容。
func (b *devOutBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// devParseFlags parses args with a fresh dev flag set. | devParseFlags 用全新的 dev 标志集解析 args。
func devParseFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet(devCommandName, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerDevFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("解析 dev 标志失败：%v | parsing dev flags: %v", err, err)
	}
	return fs
}

// devEnvWithConfig builds an Env whose layered configuration pins server.port. | devEnvWithConfig 构造一个在分层配置里固定 server.port 的 Env。
func devEnvWithConfig(t *testing.T, port int) *Env {
	t.Helper()
	cfg, err := config.Load(config.Options{Name: "yd", ConfigDir: t.TempDir(), FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("加载配置失败：%v | loading config: %v", err, err)
	}
	cfg.Set(keyDevServerPort, port)
	return &Env{Cfg: cfg, WorkDir: ".", Out: io.Discard}
}

// devWriteFile writes a file, creating its parent directory. | devWriteFile 写文件并创建其父目录。
func devWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败：%v | creating directory: %v", err, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写文件失败：%v | writing file: %v", err, err)
	}
}

// devWaitFor polls cond until it holds and fails the test on timeout. | devWaitFor 轮询 cond 直到成立，超时即终止测试。
func devWaitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待超时（%v）| timed out after %v", timeout, timeout)
}

// Every flag must map onto a toolchain option. | TestDevOptionsFromFlags 标志必须整份映射到 toolchain 选项。
func TestDevOptionsFromFlags(t *testing.T) {
	dir := t.TempDir()
	fs := devParseFlags(t,
		"--port=9090",
		"--no-tailwind",
		"--no-watch",
		"--no-reload",
		"--debounce=50ms",
		"--tailwind-bin=C:/tools/tailwindcss.exe",
		"--", "serve", "--debug",
	)
	out := &devOutBuffer{}
	env := &Env{WorkDir: dir, Out: out}

	o := devOptions(env, fs)

	if o.Dir != dir {
		t.Errorf("Dir = %q，期望 %q | want %q", o.Dir, dir, dir)
	}
	if o.Port != 9090 {
		t.Errorf("Port = %d，期望 9090 | want 9090", o.Port)
	}
	if !o.NoTailwind || !o.NoWatch || !o.NoReload {
		t.Errorf("NoTailwind/NoWatch/NoReload = %v/%v/%v，期望全为 true | want all true", o.NoTailwind, o.NoWatch, o.NoReload)
	}
	if o.Debounce != 50*time.Millisecond {
		t.Errorf("Debounce = %v，期望 50ms | want 50ms", o.Debounce)
	}
	if o.TailwindBin != "C:/tools/tailwindcss.exe" {
		t.Errorf("TailwindBin = %q，期望透传标志值 | want the flag value", o.TailwindBin)
	}
	if got := strings.Join(o.AppArgs, " "); got != "serve --debug" {
		t.Errorf("AppArgs = %q，期望 %q | want %q", got, "serve --debug", "serve --debug")
	}
	if o.Log == nil {
		t.Error("Log 为空，期望默认日志器 | want the default logger")
	}
	if o.Out != out {
		t.Error("Out 未指向 Env.Out | Out must point at Env.Out")
	}
}

// Without flags the toolchain defaults must apply. | TestDevDefaults 未传标志时必须落到 toolchain 默认值。
func TestDevDefaults(t *testing.T) {
	env := &Env{WorkDir: t.TempDir(), Out: io.Discard}
	o := devOptions(env, devParseFlags(t))

	if o.Debounce != toolchain.DefaultDebounce {
		t.Errorf("Debounce = %v，期望 %v | want %v", o.Debounce, toolchain.DefaultDebounce, toolchain.DefaultDebounce)
	}
	if o.Port != 0 {
		t.Errorf("Port = %d，期望 0（沿用应用默认端口）| want 0", o.Port)
	}
	if o.NoTailwind || o.NoWatch || o.NoReload {
		t.Errorf("默认不得关闭任何功能 | nothing may be disabled by default")
	}
	if len(o.AppArgs) != 0 {
		t.Errorf("AppArgs = %v，期望为空 | want no app args", o.AppArgs)
	}
}

// TestDevPortPrefersFlagOverConfig ranks the port sources: --port > server.port > the app default. | TestDevPortPrefersFlagOverConfig 端口优先级：--port > server.port > 应用默认值。
func TestDevPortPrefersFlagOverConfig(t *testing.T) {
	env := devEnvWithConfig(t, 7070)

	if got := devPort(env, devParseFlags(t)); got != 7070 {
		t.Errorf("devPort = %d，期望配置文件里的 7070 | want 7070 from the config file", got)
	}
	if got := devPort(env, devParseFlags(t, "--port=9090")); got != 9090 {
		t.Errorf("devPort = %d，期望标志值 9090 | want the flag value 9090", got)
	}
}

// tailwind.bin must feed the resolution chain. | TestDevTailwindBinFromConfig 配置里的 tailwind.bin 必须传给解析链。
func TestDevTailwindBinFromConfig(t *testing.T) {
	env := devEnvWithConfig(t, 8080)
	env.Cfg.Set(keyDevTailwindBin, "C:/tools/from-config/tailwindcss.exe")
	o := devOptions(env, devParseFlags(t))

	if o.TailwindConfigBin != "C:/tools/from-config/tailwindcss.exe" {
		t.Errorf("TailwindConfigBin = %q，期望配置值 | want the configured path", o.TailwindConfigBin)
	}
	if o.TailwindBin != "" {
		t.Errorf("TailwindBin = %q，未传标志时应为空 | want empty without the flag", o.TailwindBin)
	}
}

// dev must stay registered exactly once: Register panics on duplicates. | TestDevCommandRegisteredOnce 保证 dev 只注册一次：Register 对重名会 panic。
func TestDevCommandRegisteredOnce(t *testing.T) {
	count := 0
	for _, c := range Commands() {
		if c.Name != devCommandName {
			continue
		}
		count++
		if c.Run == nil {
			t.Error("dev 命令缺少 Run | the dev command has no Run")
		}
		if c.Flags == nil {
			t.Error("dev 命令缺少 Flags | the dev command has no Flags")
		}
		if c.Usage != devUsageLine {
			t.Errorf("Usage = %q，期望 %q | want %q", c.Usage, devUsageLine, devUsageLine)
		}
	}
	if count != 1 {
		t.Fatalf("dev 命令注册了 %d 次，期望 1 次 | want exactly 1 registration", count)
	}
}

// TestDevMissingProjectDirFailsFast returns the sentinel immediately for a missing project directory. | TestDevMissingProjectDirFailsFast 不存在的项目目录必须立刻返回哨兵错误，而不是启动任何进程。
func TestDevMissingProjectDirFailsFast(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = filepath.Join(t.TempDir(), "missing")

	err := Execute(context.Background(), []string{"dev", "--no-watch", "--no-tailwind"}, te.env)
	if !errors.Is(err, toolchain.ErrProjectDir) {
		t.Fatalf("err = %v，期望包装 toolchain.ErrProjectDir | want toolchain.ErrProjectDir", err)
	}
}

// All six flags must show up in the help text. | TestDevHelpListsFlags 六个标志都必须出现在帮助文本里。
func TestDevHelpListsFlags(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"dev", "--help"}, te.env); err != nil {
		t.Fatalf("dev --help 返回错误：%v | dev --help: %v", err, err)
	}
	got := te.out.String()
	for _, want := range []string{
		"--" + flagDevPort,
		"--" + flagDevNoTailwind,
		"--" + flagDevNoWatch,
		"--" + flagDevNoReload,
		"--" + flagDevDebounce,
		"--" + flagDevTailwindBin,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("帮助文本缺少 %s：\n%s", want, got)
		}
	}
}

// TestDevRunsChildAndStopsOnCancel runs a real `yd dev --no-watch` end to end: it builds and starts the child, forwards its output and shuts down gracefully when the context is cancelled (Ctrl+C). | TestDevRunsChildAndStopsOnCancel 端到端跑一次真实的 `yd dev --no-watch`：编译并启动子进程、转发其输出，取消上下文（等价于 Ctrl+C）后优雅退出。
func TestDevRunsChildAndStopsOnCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实编译 | skipped in short mode")
	}
	dir := t.TempDir()
	devWriteFile(t, filepath.Join(dir, "go.mod"), "module devapp\n\ngo 1.27.2\n")
	devWriteFile(t, filepath.Join(dir, "main.go"),
		"package main\n\nimport (\n\t\"fmt\"\n\t\"time\"\n)\n\n"+
			"func main() {\n\tfmt.Println(\"dev-app-ready\")\n\ttime.Sleep(time.Minute)\n}\n")

	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	out := &devOutBuffer{}
	te.env.Out = out

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- Execute(ctx, []string{"dev", "--no-watch", "--no-tailwind"}, te.env) }()

	devWaitFor(t, 120*time.Second, func() bool { return strings.Contains(out.String(), "dev-app-ready") })
	if got := out.String(); !strings.Contains(got, "yd dev - "+dir) {
		t.Errorf("会话摘要缺少项目目录：\n%s", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("yd dev 返回错误：%v | yd dev returned: %v", err, err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("取消上下文后 yd dev 未退出 | yd dev did not stop after cancellation")
	}
}
