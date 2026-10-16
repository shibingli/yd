// This file tests the build command: registration, help output, flag validation, target
// resolution and the wiring into build.Build. Every test but the last one injects a fake
// toolchain, so only TestBuildRealCompileOfCmdYD ever runs a real compiler. | 本文件测试 build 命令：
// 注册、帮助输出、标志校验、目标解析与对 build.Build 的接线。除最后一个用例之外，每个测试都注入假工具链，
// 因此只有 TestBuildRealCompileOfCmdYD 会真正调用编译器。

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/shibingli/yd/build"
	"github.com/shibingli/yd/version"
)

// ---------------------------------------------------------------- Test helpers | 测试辅助

const (
	// cliHelperModeEnv marks the carrier process of the fake toolchain. | cliHelperModeEnv 标记假工具链的载体进程。
	cliHelperModeEnv = "YD_CLI_TEST_HELPER_MODE"
	// cliHelperStdoutEnv carries the canned stdout of the fake command. | cliHelperStdoutEnv 承载假命令的预置标准输出。
	cliHelperStdoutEnv = "YD_CLI_TEST_HELPER_STDOUT"
	// cliHelperExitEnv carries the exit code of the fake command. | cliHelperExitEnv 承载假命令的退出码。
	cliHelperExitEnv = "YD_CLI_TEST_HELPER_EXIT"
)

// The executable magics accepted by the build package's verifier. | build 包校验器接受的可执行魔数。
var (
	cliELFMagic   = []byte{0x7f, 'E', 'L', 'F'}
	cliPEMagic    = []byte{'M', 'Z'}
	cliMachOMagic = []byte{0xcf, 0xfa, 0xed, 0xfe}
	cliWASMMagic  = []byte{0x00, 0x61, 0x73, 0x6d}
)

// TestFakeCommandProcess is the carrier process shared by the fake ExecCommand of the build and
// docker command tests: the fake re-executes this test binary with
// -test.run=^TestFakeCommandProcess$, which writes the canned stdout and exits with the canned
// code, so no CLI test ever needs a real toolchain, docker daemon or network access. | TestFakeCommandProcess
// 是 build 与 docker 命令测试所共用的假 ExecCommand 载体进程：假命令以 -test.run=^TestFakeCommandProcess$
// 重新执行本测试二进制，这里写出预置标准输出并以预置退出码退出，因此命令行测试永不依赖真实工具链、docker
// 守护进程或网络。
func TestFakeCommandProcess(t *testing.T) {
	if os.Getenv(cliHelperModeEnv) != "1" {
		return
	}
	_, _ = os.Stdout.WriteString(os.Getenv(cliHelperStdoutEnv))
	code, err := strconv.Atoi(os.Getenv(cliHelperExitEnv))
	if err != nil {
		code = 0
	}
	os.Exit(code)
}

// cliFakeExec records every injected command and dispatches it to the carrier process. | cliFakeExec 记录每条注入的命令并转发给载体进程。
type cliFakeExec struct {
	// calls holds one argv snapshot per invocation. | calls 保存每次调用的 argv 快照。
	calls [][]string
	// cmds keeps every constructed command, so tests can read the environment and the working
	// directory that the caller installed after construction. | cmds 保存每个已构造的命令，使测试能读取
	// 调用方在构造之后安装的环境变量与工作目录。
	cmds []*exec.Cmd
	// handler maps an invocation to its canned stdout and exit code; nil means success with
	// empty output. | handler 把一次调用映射为预置标准输出与退出码；nil 表示成功且无输出。
	handler func(name string, args []string) (stdout string, exit int)
	// onCall runs before the carrier process, used to fake artifacts on disk. | onCall 在载体进程之前执行，用于就地伪造磁盘上的产物。
	onCall func(name string, args []string)
}

// exec implements the injectable ExecCommand signature of build and docker. | exec 实现 build 与 docker 的可注入 ExecCommand 签名。
func (f *cliFakeExec) exec(ctx context.Context, name string, args ...string) *exec.Cmd {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.onCall != nil {
		f.onCall(name, args)
	}
	stdout, exit := "", 0
	if f.handler != nil {
		stdout, exit = f.handler(name, args)
	}
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFakeCommandProcess$")
	cmd.Env = append(os.Environ(),
		cliHelperModeEnv+"=1",
		cliHelperStdoutEnv+"="+stdout,
		cliHelperExitEnv+"="+strconv.Itoa(exit),
	)
	f.cmds = append(f.cmds, cmd)
	return cmd
}

// invocations returns the argv of every call whose verb equals verb. | invocations 返回动词等于 verb 的全部调用 argv。
func (f *cliFakeExec) invocations(verb string) [][]string {
	var out [][]string
	for _, argv := range f.calls {
		if len(argv) > 1 && argv[1] == verb {
			out = append(out, argv)
		}
	}
	return out
}

// argValue returns the value of a "-flag value" or "-flag=value" pair. | argValue 返回 "-flag value" 或 "-flag=value" 形式的取值。
func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v, true
		}
	}
	return "", false
}

// envValue returns the value of one KEY=VALUE entry of an environment block. | envValue 返回环境块中某个 KEY=VALUE 条目的取值。
func envValue(environ []string, key string) string {
	prefix := key + "="
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			return v
		}
	}
	return ""
}

// cliExeSuffix returns the native executable suffix of the host platform. | cliExeSuffix 返回宿主平台的原生可执行后缀。
func cliExeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// cliMagicForPath returns the magic of the format Go emits for the target an artifact path implies:
// the multi-target layout carries <name>_<goos>_<goarch>, a .exe suffix always means Windows, and a
// single-target build lands on the host platform. | cliMagicForPath 返回产物路径所暗示目标的格式魔数：
// 多目标布局携带 <名字>_<GOOS>_<GOARCH>，.exe 后缀始终意味着 Windows，单目标构建落在宿主平台。
func cliMagicForPath(path string) []byte {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".exe"), strings.Contains(lower, "_windows_"):
		return cliPEMagic
	case strings.Contains(lower, "_darwin_"), strings.Contains(lower, "_ios_"):
		return cliMachOMagic
	case strings.Contains(lower, "_js_"), strings.Contains(lower, "_wasip1_"):
		return cliWASMMagic
	case strings.Contains(lower, "_linux_"), strings.Contains(lower, "_freebsd_"), strings.Contains(lower, "_netbsd_"):
		return cliELFMagic
	default:
		return cliHostMagic()
	}
}

// cliHostMagic returns the magic of the host platform's executable format. | cliHostMagic 返回宿主平台可执行格式的魔数。
func cliHostMagic() []byte {
	switch runtime.GOOS {
	case "windows":
		return cliPEMagic
	case "darwin", "ios":
		return cliMachOMagic
	case "js", "wasip1":
		return cliWASMMagic
	default:
		return cliELFMagic
	}
}

// cliFakeGoBuild mimics `go build`: it writes a fake artifact (magic + padding) to the -o path, so
// the build package's post-build verification has something to accept. The magic is derived from
// the artifact path. | cliFakeGoBuild 模拟 `go build`：按 -o 路径写出伪造产物（魔数 + 填充），使 build 包的
// 构建后校验有可接受的对象；魔数由产物路径推导。
func cliFakeGoBuild() *cliFakeExec { return cliFakeGoBuildWith(nil) }

// cliFakeGoBuildWith is cliFakeGoBuild with an explicit magic, needed whenever the artifact name
// says nothing about the target platform. | cliFakeGoBuildWith 是带显式魔数的 cliFakeGoBuild，用于产物名
// 无法表明目标平台的场合。
func cliFakeGoBuildWith(magic []byte) *cliFakeExec {
	return &cliFakeExec{
		onCall: func(_ string, args []string) {
			path, ok := argValue(args, "-o")
			if !ok {
				return
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return
			}
			m := magic
			if m == nil {
				m = cliMagicForPath(path)
			}
			body := append(append([]byte{}, m...), make([]byte, 128)...)
			_ = os.WriteFile(path, body, 0o755)
		},
	}
}

// newBuildDir writes a minimal Go module into a temp directory and returns it, so the build
// command has a real directory to work in even though nothing is ever compiled. | newBuildDir 在临时目录
// 写入最小 Go 模块并返回该目录，使 build 命令有真实目录可用，尽管全程不做编译。
func newBuildDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/shibingli/yd\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatalf("写入 go.mod 失败: %v", err)
	}
	return dir
}

// useBuildExec injects a fake toolchain into the build command for the duration of one test. | useBuildExec 在单个测试期间为 build 命令注入假工具链。
func useBuildExec(t *testing.T, f *cliFakeExec) {
	t.Helper()
	old := buildExecCommand
	buildExecCommand = f.exec
	t.Cleanup(func() { buildExecCommand = old })
}

// useBuildTailwind injects a recorder for the stylesheet stage, which is how the --tailwind flags
// are observed without running tailwindcss. | useBuildTailwind 注入样式表阶段的记录器，从而在不运行 tailwindcss
// 的前提下观察 --tailwind 标志。
func useBuildTailwind(t *testing.T, stage build.TailwindFunc) {
	t.Helper()
	old := buildTailwindFunc
	buildTailwindFunc = stage
	t.Cleanup(func() { buildTailwindFunc = old })
}

// buildModule is the module name of the temp modules the build tests create. | buildModule 是 build 测试所建临时模块的模块名。
const buildModule = "yd"

// ---------------------------------------------------------------- Tests | 测试

// TestBuildCommandRegistered build 只注册一次且标志齐全，Register 对重名会 panic。 | build must stay registered exactly once with every flag: Register panics on duplicates.
func TestBuildCommandRegistered(t *testing.T) {
	count := 0
	for _, c := range Commands() {
		if c.Name != buildCommandName {
			continue
		}
		count++
		if c.Run == nil || c.Flags == nil {
			t.Error("build 命令缺少 Run 或 Flags")
		}
		if c.Usage != buildUsageLine {
			t.Errorf("Usage = %q，期望 %q", c.Usage, buildUsageLine)
		}
	}
	if count != 1 {
		t.Fatalf("build 命令注册了 %d 次，期望 1 次", count)
	}

	fs := pflag.NewFlagSet(buildCommandName, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerBuildFlags(fs)
	for _, name := range []string{
		flagBuildOS, flagBuildArch, flagBuildTargets, flagBuildAll, flagBuildOutput, flagBuildName,
		flagBuildTailwind, flagBuildNoTailwind, flagBuildMinify, flagBuildOffline, flagBuildTags, flagBuildLDFlags,
	} {
		if fs.Lookup(name) == nil {
			t.Errorf("build 命令缺少标志 --%s", name)
		}
	}
	if f := fs.Lookup(flagBuildTailwind); f != nil && f.DefValue != "true" {
		t.Errorf("--tailwind 缺省值 = %q，期望 true", f.DefValue)
	}
}

// TestBuildCommandHelp build 命令注册成功、--help 可用且列出全部标志。
// TestBuildCommandHelp requires the build command to be registered with working --help output.
func TestBuildCommandHelp(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"build", "--help"}, te.env); err != nil {
		t.Fatalf("build --help 不应返回错误: %v", err)
	}
	out := te.out.String()
	for _, want := range []string{"yd build", "Usage", "--targets", "--os", "--arch", "--all", "--output", "--name", "--tailwind", "--no-tailwind", "--minify", "--offline", "--tags", "--ldflags"} {
		if !strings.Contains(out, want) {
			t.Errorf("build 帮助缺少 %q：\n%s", want, out)
		}
	}
}

// TestBuildUnknownFlagErrors 未知标志必须包装为 ErrFlagParse。
// TestBuildUnknownFlagErrors wraps an unknown flag into ErrFlagParse.
func TestBuildUnknownFlagErrors(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"build", "--nope"}, te.env); !errors.Is(err, ErrFlagParse) {
		t.Fatalf("未知标志应返回 ErrFlagParse，实际 %v", err)
	}
}

// TestBuildPassesTargetsToBuild --targets 被解析并逐个传给 build.Build，多目标产物落在 dist/ 下。
// TestBuildPassesTargetsToBuild parses --targets, hands every pair to build.Build and expects the multi-target dist/ naming.
func TestBuildPassesTargetsToBuild(t *testing.T) {
	dir := newBuildDir(t)
	fake := cliFakeGoBuild()
	useBuildExec(t, fake)

	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	if err := Execute(context.Background(), []string{"build", "--targets", "linux/amd64,linux/arm64", "--no-tailwind"}, te.env); err != nil {
		t.Fatalf("build --targets 失败: %v", err)
	}

	builds := fake.invocations("build")
	if len(builds) != 2 {
		t.Fatalf("go build 调用次数 = %d，期望 2（每个目标一次）", len(builds))
	}
	wantGOARCH := []string{"amd64", "arm64"}
	wantOut := []string{
		filepath.Join(dir, "dist", buildModule+"_linux_amd64"),
		filepath.Join(dir, "dist", buildModule+"_linux_arm64"),
	}
	for i, argv := range builds {
		out, ok := argValue(argv, "-o")
		if !ok || out != wantOut[i] {
			t.Errorf("第 %d 次 go build 的 -o = %q (found=%v)，期望 %q", i+1, out, ok, wantOut[i])
		}
	}
	for i, cmd := range fake.cmds {
		if got := envValue(cmd.Env, "GOOS"); got != "linux" {
			t.Errorf("第 %d 次 go build 的 GOOS = %q，期望 linux", i+1, got)
		}
		if got := envValue(cmd.Env, "GOARCH"); got != wantGOARCH[i] {
			t.Errorf("第 %d 次 go build 的 GOARCH = %q，期望 %q", i+1, got, wantGOARCH[i])
		}
		if got := envValue(cmd.Env, "CGO_ENABLED"); got != "0" {
			t.Errorf("第 %d 次 go build 的 CGO_ENABLED = %q，期望 0", i+1, got)
		}
		if cmd.Dir != dir {
			t.Errorf("第 %d 次 go build 的工作目录 = %q，期望 %q", i+1, cmd.Dir, dir)
		}
	}

	out := te.out.String()
	for _, want := range []string{wantOut[0], wantOut[1], "linux/amd64", "linux/arm64", "built | 已构建: 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("构建报告缺少 %q：\n%s", want, out)
		}
	}
}

// TestBuildDefaultsToHostPlatform 未给目标标志时按当前平台构建单目标，产物落在 bin/ 下。
// TestBuildDefaultsToHostPlatform falls back to the host platform and the bin/ layout when no target flag is given.
func TestBuildDefaultsToHostPlatform(t *testing.T) {
	dir := newBuildDir(t)
	fake := cliFakeGoBuild()
	useBuildExec(t, fake)

	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	if err := Execute(context.Background(), []string{"build", "--no-tailwind"}, te.env); err != nil {
		t.Fatalf("build 失败: %v", err)
	}

	builds := fake.invocations("build")
	if len(builds) != 1 {
		t.Fatalf("go build 调用次数 = %d，期望 1", len(builds))
	}
	if got := envValue(fake.cmds[0].Env, "GOOS"); got != runtime.GOOS {
		t.Errorf("GOOS = %q，期望宿主平台 %q", got, runtime.GOOS)
	}
	if got := envValue(fake.cmds[0].Env, "GOARCH"); got != runtime.GOARCH {
		t.Errorf("GOARCH = %q，期望宿主平台 %q", got, runtime.GOARCH)
	}
	want := filepath.Join(dir, "bin", buildModule+cliExeSuffix())
	if out, _ := argValue(builds[0], "-o"); out != want {
		t.Errorf("单目标产物路径 = %q，期望 %q", out, want)
	}
}

// TestBuildOSArchAndOutputFlags --os/--arch 合成单个目标，--output 覆盖产物路径，--name 覆盖基础名。
// TestBuildOSArchAndOutputFlags composes a single target from --os/--arch and honours --output and --name.
func TestBuildOSArchAndOutputFlags(t *testing.T) {
	dir := newBuildDir(t)
	// The custom --output name carries no GOOS, so the fake pins the ELF magic of linux/arm64. | 自定义的
	// --output 名字不含 GOOS，因此假工具链直接钉住 linux/arm64 的 ELF 魔数。
	fake := cliFakeGoBuildWith(cliELFMagic)
	useBuildExec(t, fake)

	custom := filepath.Join(dir, "out", "release-yard")
	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	err := Execute(context.Background(), []string{
		"build", "--os", "linux", "--arch", "arm64", "--output", custom, "--name", "yard", "--no-tailwind",
	}, te.env)
	if err != nil {
		t.Fatalf("build --os/--arch/--output/--name 失败: %v", err)
	}

	builds := fake.invocations("build")
	if len(builds) != 1 {
		t.Fatalf("go build 调用次数 = %d，期望 1", len(builds))
	}
	if got := envValue(fake.cmds[0].Env, "GOARCH"); got != "arm64" {
		t.Errorf("GOARCH = %q，期望 arm64", got)
	}
	if out, _ := argValue(builds[0], "-o"); out != custom {
		t.Errorf("--output 产物路径 = %q，期望 %q", out, custom)
	}
	// --name only changes the default file name, so the artifact of an explicit --output keeps
	// its caller-given path; the report must show it verbatim. | --name 只改变缺省文件名，显式 --output 的
	// 产物保持调用方给定的路径；报告必须原样展示它。
	if out := te.out.String(); !strings.Contains(out, custom) {
		t.Errorf("构建报告缺少产物路径 %q：\n%s", custom, out)
	}
}

// TestBuildAllExpandsToFourTargets --all 展开为四个平台，Windows 目标带 .exe 后缀。
// TestBuildAllExpandsToFourTargets expands --all into four platforms and keeps the Windows .exe suffix.
func TestBuildAllExpandsToFourTargets(t *testing.T) {
	dir := newBuildDir(t)
	fake := cliFakeGoBuild()
	useBuildExec(t, fake)

	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	if err := Execute(context.Background(), []string{"build", "--all", "--no-tailwind"}, te.env); err != nil {
		t.Fatalf("build --all 失败: %v", err)
	}

	builds := fake.invocations("build")
	if len(builds) != 4 {
		t.Fatalf("go build 调用次数 = %d，期望 4", len(builds))
	}
	got := map[string]bool{}
	for _, argv := range builds {
		out, ok := argValue(argv, "-o")
		if !ok {
			t.Fatalf("go build 缺少 -o：%v", argv)
		}
		got[filepath.Base(out)] = true
	}
	for _, want := range []string{
		buildModule + "_linux_amd64", buildModule + "_linux_arm64",
		buildModule + "_darwin_arm64", buildModule + "_windows_amd64.exe",
	} {
		if !got[want] {
			t.Errorf("--all 未产出 %q，实际产物: %v", want, got)
		}
	}
	if out := te.out.String(); !strings.Contains(out, "built | 已构建: 4") {
		t.Errorf("构建报告应报告 4 个产物：\n%s", out)
	}
}

// TestBuildRejectsInvalidTargetFlags --all 与 --targets 互斥，非法目标返回 build.ErrInvalidTarget，且都不触发构建。
// TestBuildRejectsInvalidTargetFlags fails on conflicting or invalid target flags without ever invoking the toolchain.
func TestBuildRejectsInvalidTargetFlags(t *testing.T) {
	dir := newBuildDir(t)
	fake := cliFakeGoBuild()
	useBuildExec(t, fake)

	cases := []struct {
		name  string
		args  []string
		check func(error) bool
	}{
		{"all plus targets | all 与 targets 互斥", []string{"build", "--all", "--targets", "linux/amd64"}, func(err error) bool { return errors.Is(err, ErrFlagParse) }},
		{"unknown goos | 未知 GOOS", []string{"build", "--targets", "beos/amd64"}, func(err error) bool { return errors.Is(err, build.ErrInvalidTarget) }},
		{"malformed pair | 残缺组合", []string{"build", "--targets", "linux"}, func(err error) bool { return errors.Is(err, build.ErrInvalidTarget) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			te := newTestEnv(t, nil, "", false)
			te.env.WorkDir = dir
			err := Execute(context.Background(), tc.args, te.env)
			if !tc.check(err) {
				t.Fatalf("Execute(%v) = %v，期望目标校验错误", tc.args, err)
			}
		})
	}
	if len(fake.calls) != 0 {
		t.Errorf("目标校验失败时不得调用工具链，实际调用: %v", fake.calls)
	}
}

// TestBuildPassesLDFlagsTagsAndOffline --ldflags/--tags/--offline/--trimpath 被原样交给工具链。
// TestBuildPassesLDFlagsTagsAndOffline hands --ldflags, --tags and --offline to the toolchain verbatim.
func TestBuildPassesLDFlagsTagsAndOffline(t *testing.T) {
	dir := newBuildDir(t)
	fake := cliFakeGoBuild()
	useBuildExec(t, fake)

	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	err := Execute(context.Background(), []string{
		"build", "--no-tailwind", "--offline",
		"--tags", "sqlite,redis", "--tags", "extra", "--ldflags", "-X example.com/mode=release",
	}, te.env)
	if err != nil {
		t.Fatalf("build 失败: %v", err)
	}

	builds := fake.invocations("build")
	if len(builds) != 1 {
		t.Fatalf("go build 调用次数 = %d，期望 1", len(builds))
	}
	joined := strings.Join(builds[0], " ")
	for _, want := range []string{
		"-trimpath",
		"-mod=vendor",
		"-tags sqlite,redis,extra",
		"-s -w",
		"-X example.com/mode=release",
		"github.com/shibingli/yd/version.Version=" + version.Version,
		"github.com/shibingli/yd/version.Commit=" + version.Commit,
		"github.com/shibingli/yd/version.Date=" + version.Date,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("go build 参数缺少 %q：\n%s", want, joined)
		}
	}
}

// TestBuildTailwindFlagsControlStage --tailwind/--no-tailwind/--minify 控制样式表阶段及其压缩开关。
// TestBuildTailwindFlagsControlStage drives the stylesheet stage and its minify switch through the flags.
func TestBuildTailwindFlagsControlStage(t *testing.T) {
	dir := newBuildDir(t)
	fake := cliFakeGoBuild()
	useBuildExec(t, fake)

	var seen []build.TailwindOptions
	useBuildTailwind(t, func(_ context.Context, _ string, o build.TailwindOptions) (int64, error) {
		seen = append(seen, o)
		return 42, nil
	})

	cases := []struct {
		name    string
		args    []string
		wantRun bool
		minify  bool
	}{
		{"default runs and trims | 默认执行且不压缩", []string{"build"}, true, false},
		{"minify | 压缩", []string{"build", "--minify"}, true, true},
		{"explicit on | 显式开启", []string{"build", "--tailwind"}, true, false},
		{"tailwind=false | 显式关闭", []string{"build", "--tailwind=false"}, false, false},
		{"no-tailwind wins | no-tailwind 优先", []string{"build", "--tailwind", "--no-tailwind"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen = nil
			te := newTestEnv(t, nil, "", false)
			te.env.WorkDir = dir
			if err := Execute(context.Background(), tc.args, te.env); err != nil {
				t.Fatalf("Execute(%v) 失败: %v", tc.args, err)
			}
			if !tc.wantRun {
				if len(seen) != 0 {
					t.Fatalf("样式表阶段不应运行，实际调用 %d 次", len(seen))
				}
				return
			}
			if len(seen) != 1 {
				t.Fatalf("样式表阶段调用次数 = %d，期望 1", len(seen))
			}
			if !seen[0].Enabled || seen[0].Minify != tc.minify {
				t.Errorf("样式表选项 = %+v，期望 Enabled=true Minify=%v", seen[0], tc.minify)
			}
		})
	}
}

// TestBuildSkipsTailwindWithoutStylesheet 项目没有样式表时阶段被跳过而不是失败。
// TestBuildSkipsTailwindWithoutStylesheet skips the stage instead of failing when the project has no stylesheet.
func TestBuildSkipsTailwindWithoutStylesheet(t *testing.T) {
	dir := newBuildDir(t)
	fake := cliFakeGoBuild()
	useBuildExec(t, fake)

	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	if err := Execute(context.Background(), []string{"build"}, te.env); err != nil {
		t.Fatalf("缺少样式表时 build 不应失败: %v", err)
	}
	if got := len(fake.invocations("build")); got != 1 {
		t.Fatalf("go build 调用次数 = %d，期望 1", got)
	}
}

// TestBuildRealCompileOfCmdYD 真实调用 build.Build 编译本仓库的 ./cmd/yd，并断言产物存在。
// TestBuildRealCompileOfCmdYD really compiles this repository's ./cmd/yd through build.Build and requires the artifact to exist.
//
// 这是唯一不注入假工具链的用例：它在 cmd/yd 目录里以单目标执行一次真实编译，产物写入临时目录，因此既验证了
// 命令行到 build.Build 的整条链路，也不会在仓库里留下任何文件。
// This is the only case without a fake toolchain: it runs one real single-target compilation inside cmd/yd and
// writes the artifact to a temp directory, so the whole CLI-to-build.Build path is exercised without leaving a
// single file behind in the repository.
func TestBuildRealCompileOfCmdYD(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "cmd", "yd"))
	if err != nil {
		t.Fatalf("解析 cmd/yd 路径失败: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("跳过：%s 不可用: %v", dir, err)
	}
	out := filepath.Join(t.TempDir(), "yd-cli-build"+cliExeSuffix())

	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	if err := Execute(context.Background(), []string{"build", "--no-tailwind", "--output", out}, te.env); err != nil {
		t.Fatalf("真实编译 ./cmd/yd 失败: %v\nstderr:\n%s", err, te.errs.String())
	}

	fi, err := os.Stat(out)
	if err != nil {
		t.Fatalf("产物不存在: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("产物为空: %s", out)
	}
	if got := te.out.String(); !strings.Contains(got, out) {
		t.Errorf("构建报告缺少产物路径 %q：\n%s", out, got)
	}
	t.Logf("真实产物: %s (%d 字节)", out, fi.Size())
}
