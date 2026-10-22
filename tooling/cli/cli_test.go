package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shibingli/ydx/log"
	"github.com/spf13/afero"
	"github.com/spf13/pflag"
)

// probeSeq numbers the probe commands registered by the tests. | probeSeq 为测试注册的探针命令编号。
var probeSeq atomic.Int64

// registerProbe registers a probe command under a name unique within the process and returns that name. | registerProbe 以进程内唯一的名字注册探针命令并返回该名字。
//
// Register records a duplicate name as a wiring error that Execute would then return, so
// `go test -count=N` would otherwise fail on its second pass; numbering the probes keeps the
// whole suite repeatable, which is what makes stress runs and the race detector useful. | Register 会把重名
// 登记为接线错误并让 Execute 返回它，否则 `go test -count=N` 在第二轮就会失败；给探针编号
// 使整套测试可重复，这正是压力运行与竞态检测有价值的前提。
func registerProbe(c Command) string {
	c.Name = fmt.Sprintf("%s-%d", c.Name, probeSeq.Add(1))
	Register(c)
	return c.Name
}

// resetRegistryErrors clears the wiring errors recorded by Register for the duration of one test. | resetRegistryErrors
// 在单个测试期间清空 Register 登记的接线错误。
//
// A test that deliberately registers a broken command would otherwise poison every later test in
// the same process. | 有意注册损坏命令的测试否则会污染同进程内后续的每个测试。
func resetRegistryErrors(t *testing.T) {
	t.Helper()
	clear := func() {
		registryMu.Lock()
		registryErrs = nil
		registryMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// testEnv bundles an Env with the in-memory buffers it writes to. | testEnv 把 Env 与它写入的内存缓冲区打包在一起。
type testEnv struct {
	env  *Env
	out  *bytes.Buffer
	errs *bytes.Buffer
}

// newTestEnv builds an Env backed by memory, so no test ever touches the real filesystem. | newTestEnv 构造内存支撑的 Env，任何测试都不触碰真实文件系统。
func newTestEnv(t *testing.T, fs afero.Fs, in string, tty bool) *testEnv {
	t.Helper()
	if fs == nil {
		fs = afero.NewMemMapFs()
	}
	te := &testEnv{out: &bytes.Buffer{}, errs: &bytes.Buffer{}}
	te.env = &Env{
		FS:      fs,
		Out:     te.out,
		Err:     te.errs,
		In:      strings.NewReader(in),
		WorkDir: ".",
		Version: "v0.0.0-test",
		IsTTY:   tty,
	}
	return te
}

// failingReader fails the test when anyone reads from it. | failingReader 在被读取时让测试失败。
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Errorf("非 TTY 路径不得读取输入 | non-TTY path must not read input")
	return 0, io.EOF
}

// TestExecuteUnknownCommandErrors 未知命令必须返回非零错误并列出可用命令。
// TestExecuteUnknownCommandErrors requires an unknown command to fail and list the available commands.
func TestExecuteUnknownCommandErrors(t *testing.T) {
	te := newTestEnv(t, nil, "", false)

	err := Execute(context.Background(), []string{"nope"}, te.env)
	if !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("期望 ErrUnknownCommand，实际 %v", err)
	}
	got := te.errs.String()
	for _, want := range []string{"assets", "build", "dev", "docker", "gen", "new"} {
		if !strings.Contains(got, want) {
			t.Errorf("可用命令列表缺少 %q：\n%s", want, got)
		}
	}
}

// TestExecuteNoArgsPrintsHelp 无参数时打印帮助并返回非零错误。
// TestExecuteNoArgsPrintsHelp prints help and returns a non-zero error when no command is given.
func TestExecuteNoArgsPrintsHelp(t *testing.T) {
	te := newTestEnv(t, nil, "", false)

	err := Execute(context.Background(), nil, te.env)
	if !errors.Is(err, ErrNoCommand) {
		t.Fatalf("期望 ErrNoCommand，实际 %v", err)
	}
	if out := te.out.String(); !strings.Contains(out, "Usage") || !strings.Contains(out, "assets") {
		t.Errorf("帮助输出不完整：\n%s", out)
	}
}

// TestExecuteNilEnvErrors nil 环境必须被拒绝而不是 panic。
// TestExecuteNilEnvErrors rejects a nil environment instead of panicking.
func TestExecuteNilEnvErrors(t *testing.T) {
	if err := Execute(context.Background(), nil, nil); !errors.Is(err, ErrNilEnv) {
		t.Fatalf("期望 ErrNilEnv，实际 %v", err)
	}
}

// TestRegisterWiringErrorsSurfaceFromExecute 要求注册错误被登记并由 Execute 以哨兵返回，而不是 panic。
// TestRegisterWiringErrorsSurfaceFromExecute records registration errors and returns them from Execute instead of panicking.
func TestRegisterWiringErrorsSurfaceFromExecute(t *testing.T) {
	noop := func(context.Context, *Env) error { return nil }
	cases := []struct {
		name     string
		register func()
		want     error
	}{
		{
			name:     "empty name | 空名",
			register: func() { Register(Command{Name: "", Run: noop}) },
			want:     ErrEmptyCommandName,
		},
		{
			name: "duplicate name | 重名",
			register: func() {
				c := Command{
					Name:  "t-dup",
					Short: "duplicate probe | 重复探针",
					Usage: "yd t-dup",
					Run:   noop,
				}
				name := registerProbe(c)
				c.Name = name
				Register(c)
			},
			want: ErrDuplicateCommand,
		},
		{
			name:     "nil Run | 空 Run",
			register: func() { Register(Command{Name: fmt.Sprintf("t-nil-run-%d", probeSeq.Add(1))}) },
			want:     ErrNilRun,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetRegistryErrors(t)
			tc.register()
			err := Execute(context.Background(), []string{"new"}, newTestEnv(t, nil, "", false).env)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Execute 返回 %v，期望携带 %v", err, tc.want)
			}
		})
	}
}

// TestGlobalFlagsBindToConfig 校验 --verbose/--quiet/--no-color 对配置层的写入。
// TestGlobalFlagsBindToConfig checks what --verbose/--quiet/--no-color write into the config layer.
func TestGlobalFlagsBindToConfig(t *testing.T) {
	var (
		level   string
		color   bool
		colored bool
	)
	probe := registerProbe(Command{
		Name:  "t-bind",
		Short: "config binding probe | 配置绑定探针",
		Usage: "yd t-bind [flags]",
		Flags: func(fs *pflag.FlagSet) {
			fs.Bool("local", false, "local flag | 本地标志")
		},
		Run: func(_ context.Context, env *Env) error {
			level = env.Cfg.GetString("log.level")
			color = env.Cfg.GetBool("log.color")
			colored = env.Cfg.IsSet("log.color")
			return nil
		},
	})

	cases := []struct {
		name  string
		args  []string
		level string
	}{
		{"verbose before command | 命令前 verbose", []string{"--verbose", probe}, "debug"},
		{"verbose after command | 命令后 verbose", []string{probe, "--verbose"}, "debug"},
		{"shorthand verbose | 简写 verbose", []string{"-v", probe}, "debug"},
		{"quiet | 静默", []string{probe, "--quiet"}, "error"},
		{"unchanged keeps default | 未传参保持默认", []string{probe}, "info"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			te := newTestEnv(t, nil, "", false)
			if err := Execute(context.Background(), tc.args, te.env); err != nil {
				t.Fatalf("Execute(%v) 失败: %v", tc.args, err)
			}
			if level != tc.level {
				t.Errorf("log.level = %q，期望 %q", level, tc.level)
			}
		})
	}

	// --verbose must also raise the global logger's own threshold. | --verbose 还必须抬高全局日志器自身的阈值。
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"--verbose", probe}, te.env); err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if !log.Default().Enabled(log.DebugLevel) {
		t.Error("DebugLevel should be enabled after --verbose | --verbose 之后 DebugLevel 应当被启用")
	}

	// --no-color is explicit and must be recorded in the config layer. | --no-color 是显式传参，必须写入配置层。
	te = newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"--no-color", probe}, te.env); err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if colored && color {
		t.Error("log.color should be false after --no-color | --no-color 之后 log.color 应为 false")
	}
	if !colored {
		t.Error("log.color should be set explicitly after --no-color | --no-color 之后 log.color 应被显式设置")
	}
	if ColorEnabled(te.env) {
		t.Error("ColorEnabled should be false after --no-color | --no-color 之后 ColorEnabled 应为 false")
	}
}

// TestCommandHelpPrinted 每个命令的 --help 独立打印且不执行命令本体。
// TestCommandHelpPrinted prints per-command help without running the command body.
func TestCommandHelpPrinted(t *testing.T) {
	ran := false
	probe := registerProbe(Command{
		Name:  "t-help",
		Short: "help probe | 帮助探针",
		Long:  "a long description | 一段较长的描述",
		Usage: "yd t-help [flags]",
		Flags: func(fs *pflag.FlagSet) {
			fs.Bool("dry-run", false, "dry run | 空跑")
		},
		Run: func(_ context.Context, _ *Env) error {
			ran = true
			return nil
		},
	})

	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{probe, "--help"}, te.env); err != nil {
		t.Fatalf("--help 不应返回错误: %v", err)
	}
	out := te.out.String()
	for _, want := range []string{"t-help", "help probe", "yd t-help [flags]", "--dry-run"} {
		if !strings.Contains(out, want) {
			t.Errorf("命令帮助缺少 %q：\n%s", want, out)
		}
	}
	if ran {
		t.Error("--help must not run the command | --help 不应执行命令")
	}

	te = newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"--help"}, te.env); err != nil {
		t.Fatalf("根 --help 不应返回错误: %v", err)
	}
	if out := te.out.String(); !strings.Contains(out, "Commands") || !strings.Contains(out, "assets") {
		t.Errorf("根帮助输出不完整：\n%s", out)
	}
}

// TestVersionFlag --version 打印版本且不落到子命令。
// TestVersionFlag prints the version and never reaches a subcommand.
func TestVersionFlag(t *testing.T) {
	te := newTestEnv(t, nil, "", false)

	if err := Execute(context.Background(), []string{"--version"}, te.env); err != nil {
		t.Fatalf("--version 不应返回错误: %v", err)
	}
	out := te.out.String()
	if !strings.Contains(out, "v0.0.0-test") {
		t.Errorf("版本输出缺少版本号：\n%s", out)
	}
	if strings.Contains(out, "Usage") {
		t.Errorf("--version 不应打印帮助：\n%s", out)
	}
}

// TestNonTTYHelpersUseDefaults 非 TTY 时交互辅助必须直接返回默认值且不读取输入。
// TestNonTTYHelpersUseDefaults requires interaction helpers to return defaults without reading input when not a TTY.
func TestNonTTYHelpersUseDefaults(t *testing.T) {
	te := newTestEnv(t, nil, "3\n", false)
	te.env.In = failingReader{t: t}

	got, err := Prompt(te.env, "name? | 名称？", "fallback")
	if err != nil || got != "fallback" {
		t.Errorf("Prompt = (%q, %v)，期望 (\"fallback\", nil)", got, err)
	}

	ok, err := Confirm(te.env, "sure? | 确定？", true)
	if err != nil || !ok {
		t.Errorf("Confirm = (%v, %v)，期望 (true, nil)", ok, err)
	}

	idx, err := Choose(te.env, "pick? | 选择？", []string{"a", "b", "c"}, 2)
	if err != nil || idx != 2 {
		t.Errorf("Choose = (%d, %v)，期望 (2, nil)", idx, err)
	}
}

// TestNonTTYChooseUsesDefault 非 TTY 下 Choose 直接返回默认下标，不读取任何输入。
// TestNonTTYChooseUsesDefault makes Choose return the default index without reading anything when not a TTY.
func TestNonTTYChooseUsesDefault(t *testing.T) {
	te := newTestEnv(t, nil, "1\n", false)
	te.env.In = failingReader{t: t}

	idx, err := Choose(te.env, "pick? | 选择？", []string{"a", "b", "c"}, 2)
	if err != nil || idx != 2 {
		t.Fatalf("非 TTY Choose = (%d, %v)，期望 (2, nil)", idx, err)
	}
	if out := te.out.String(); strings.Contains(out, "1)") {
		t.Errorf("非 TTY 不应渲染选项列表：\n%s", out)
	}
}

// TestTTYHelpersReadInput TTY 下交互辅助消费输入流。
// TestTTYHelpersReadInput consumes the input stream through the interaction helpers on a TTY.
func TestTTYHelpersReadInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		def  string
		want string
	}{
		{"typed value | 输入取值", "hello\n", "def", "hello"},
		{"empty line keeps default | 空行取默认", "\n", "def", "def"},
		{"eof keeps default | EOF 取默认", "", "def", "def"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			te := newTestEnv(t, nil, tc.in, true)
			got, err := Prompt(te.env, "value? | 取值？", tc.def)
			if err != nil || got != tc.want {
				t.Fatalf("Prompt = (%q, %v)，期望 (%q, nil)", got, err, tc.want)
			}
			if !strings.Contains(te.out.String(), "value?") {
				t.Errorf("提示语未写入输出：\n%s", te.out.String())
			}
		})
	}

	confirm := []struct {
		in   string
		def  bool
		want bool
	}{
		{"y\n", false, true},
		{"yes\n", false, true},
		{"n\n", true, false},
		{"no\n", true, false},
		{"\n", true, true},
		{"\n", false, false},
		{"whatever\n", true, true},
	}
	for _, tc := range confirm {
		te := newTestEnv(t, nil, tc.in, true)
		got, err := Confirm(te.env, "ok? | 可以吗？", tc.def)
		if err != nil || got != tc.want {
			t.Errorf("Confirm(%q, def=%v) = (%v, %v)，期望 %v", tc.in, tc.def, got, err, tc.want)
		}
	}

	choose := []struct {
		in   string
		def  int
		want int
	}{
		{"2\n", 0, 1},
		{"1\n", 2, 0},
		{"9\n", 1, 1},
		{"abc\n", 0, 0},
		{"\n", 2, 2},
	}
	for _, tc := range choose {
		te := newTestEnv(t, nil, tc.in, true)
		got, err := Choose(te.env, "pick? | 选择？", []string{"a", "b", "c"}, tc.def)
		if err != nil || got != tc.want {
			t.Errorf("Choose(%q, def=%d) = (%d, %v)，期望 %d", tc.in, tc.def, got, err, tc.want)
		}
	}

	te := newTestEnv(t, nil, "1\n", true)
	if _, err := Choose(te.env, "pick? | 选择？", nil, 0); !errors.Is(err, ErrEmptyOptions) {
		t.Errorf("空选项应返回 ErrEmptyOptions，实际 %v", err)
	}
}

// TestExecuteLoadsConfigFromFlags -c/--env-file 必须参与配置分层。
// TestExecuteLoadsConfigFromFlags makes -c/--env-file part of the config layering.
func TestExecuteLoadsConfigFromFlags(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "conf/site.yaml", []byte("server:\n  port: 9876\n"), 0o644); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	if err := afero.WriteFile(fs, "conf/site.env", []byte("YD_APP_ENV=from-env-file\n"), 0o644); err != nil {
		t.Fatalf("写入测试 .env 失败: %v", err)
	}

	var (
		port   int
		appEnv string
	)
	probe := registerProbe(Command{
		Name:  "t-cfg",
		Short: "config probe | 配置探针",
		Usage: "yd t-cfg",
		Run: func(_ context.Context, env *Env) error {
			port = env.Cfg.GetInt("server.port")
			appEnv = env.Cfg.GetString("app.env")
			return nil
		},
	})

	te := newTestEnv(t, fs, "", false)
	if err := Execute(context.Background(), []string{"-c", "conf/site.yaml", probe}, te.env); err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if port != 9876 {
		t.Errorf("server.port = %d，期望 9876", port)
	}

	te = newTestEnv(t, fs, "", false)
	if err := Execute(context.Background(), []string{"--env-file", "conf/site.env", probe}, te.env); err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if appEnv != "from-env-file" {
		t.Errorf("app.env = %q，期望 from-env-file", appEnv)
	}
}

// TestExecuteParsesCommandFlagsAndArgs 命令标志与位置参数交错解析。
// TestExecuteParsesCommandFlagsAndArgs parses command flags and positional args when interspersed.
func TestExecuteParsesCommandFlagsAndArgs(t *testing.T) {
	var (
		captured *pflag.FlagSet
		local    bool
		args     []string
	)
	probe := registerProbe(Command{
		Name:  "t-args",
		Short: "args probe | 参数探针",
		Usage: "yd t-args [flags] [args]",
		Flags: func(fs *pflag.FlagSet) {
			captured = fs
			fs.Bool("local", false, "local flag | 本地标志")
		},
		Run: func(_ context.Context, _ *Env) error {
			local, _ = captured.GetBool("local")
			args = captured.Args()
			return nil
		},
	})

	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{probe, "pos1", "--local", "pos2"}, te.env); err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if !local {
		t.Error("--local was not parsed | --local 未被解析")
	}
	if strings.Join(args, ",") != "pos1,pos2" {
		t.Errorf("位置参数 = %v，期望 [pos1 pos2]", args)
	}
}

// TestExecuteUnknownFlagErrors 未知标志返回包装了 ErrFlagParse 的错误。
// TestExecuteUnknownFlagErrors wraps unknown-flag failures with ErrFlagParse.
func TestExecuteUnknownFlagErrors(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	err := Execute(context.Background(), []string{"--nope"}, te.env)
	if !errors.Is(err, ErrFlagParse) {
		t.Fatalf("期望 ErrFlagParse，实际 %v", err)
	}
}

// TestCommandsAreSortedAndUnique 注册表按名字排序且无重复。
// TestCommandsAreSortedAndUnique keeps the registry sorted by name and free of duplicates.
func TestCommandsAreSortedAndUnique(t *testing.T) {
	cmds := Commands()
	if len(cmds) < 6 {
		t.Fatalf("命令数量 = %d，至少应有 6 个", len(cmds))
	}
	seen := map[string]bool{}
	for i, c := range cmds {
		if seen[c.Name] {
			t.Errorf("命令 %q 重复", c.Name)
		}
		seen[c.Name] = true
		if i > 0 && cmds[i-1].Name > c.Name {
			t.Errorf("命令未按名字排序：%q 在 %q 之后", c.Name, cmds[i-1].Name)
		}
		if c.Short == "" || c.Usage == "" || c.Run == nil {
			t.Errorf("命令 %q 缺少 Short/Usage/Run", c.Name)
		}
	}
	for _, want := range []string{"assets", "build", "dev", "docker", "gen", "new"} {
		if !seen[want] {
			t.Errorf("缺少命令 %q", want)
		}
	}
}

// TestPlaceholderCommandsNotImplemented 尚未交付的占位命令返回尚未实现的哨兵错误。
// TestPlaceholderCommandsNotImplemented has every still-unimplemented placeholder command return the not-implemented sentinel.
//
// The list comes from | 清单取自 notImplemented 自身的登记，因此交付某个命令的任务无需再修改本测试。
// notImplemented's own bookkeeping, so landing a command never requires editing this test.
func TestPlaceholderCommandsNotImplemented(t *testing.T) {
	for _, name := range placeholderNames {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{name}, te.env)
		if !errors.Is(err, ErrNotImplemented) {
			t.Errorf("命令 %q 应返回 ErrNotImplemented，实际 %v", name, err)
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("命令 %q 的错误信息应包含命令名：%v", name, err)
		}
	}
}
