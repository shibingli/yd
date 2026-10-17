package build

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/yd/core/logx"
)

// ---------------------------------------------------------------- Test helpers | 测试辅助

// Magic numbers of the executable formats the verifier understands. | 校验器能识别的可执行格式的魔数。
var (
	elfMagic = []byte{0x7f, 'E', 'L', 'F'}
	peMagic  = []byte{'M', 'Z'}
)

const (
	// helperModeEnv marks the carrier process of the fake command. | helperModeEnv 标记假命令的载体进程。
	helperModeEnv = "YD_TEST_HELPER_MODE"
	// helperStdoutEnv carries the canned stdout of the fake command. | helperStdoutEnv 承载假命令的预置标准输出。
	helperStdoutEnv = "YD_TEST_HELPER_STDOUT"
	// helperExitEnv carries the exit code of the fake command. | helperExitEnv 承载假命令的退出码。
	helperExitEnv = "YD_TEST_HELPER_EXIT"
)

// TestFakeCommandProcess is the carrier process of the injectable ExecCommand:
// the fake re-executes this test binary with -test.run=^TestFakeCommandProcess$,
// which then writes the canned stdout and exits with the canned code, so no real
// toolchain, docker daemon or network access is ever needed. | TestFakeCommandProcess
// 是可注入 ExecCommand 的载体进程：假命令以 -test.run=^TestFakeCommandProcess$ 重新执行本
// 测试二进制，这里写出预置标准输出并以预置退出码退出，全程不依赖真实工具链、docker 守护进程或网络。
func TestFakeCommandProcess(t *testing.T) {
	if os.Getenv(helperModeEnv) != "1" {
		return
	}
	_, _ = os.Stdout.WriteString(os.Getenv(helperStdoutEnv))
	code, err := strconv.Atoi(os.Getenv(helperExitEnv))
	if err != nil {
		code = 0
	}
	os.Exit(code)
}

// fakeExec records the argv of every injected command and dispatches to a carrier process. | fakeExec 记录每条注入命令的 argv，并转发给载体进程执行。
type fakeExec struct {
	// calls holds one argv snapshot per invocation. | calls 保存每次调用的 argv 快照。
	calls [][]string
	// lastCmd is the most recently constructed *exec.Cmd, kept so tests can inspect the
	// environment and working directory that Build installed. | lastCmd 是最近构造的 *exec.Cmd，
	// 供测试检查 Build 安装的环境变量与工作目录。
	lastCmd *exec.Cmd
	// handler maps an invocation to its canned stdout and exit code. | handler 把一次调用映射为预置标准输出与退出码。
	handler func(name string, args []string) (stdout string, exit int)
	// onCall runs before the carrier process, used to fake artifacts on disk. | onCall 在载体进程之前执行，用于就地伪造磁盘上的产物。
	onCall func(name string, args []string)
}

// exec implements the injectable ExecCommand signature. | exec 实现可注入的 ExecCommand 签名。
func (f *fakeExec) exec(ctx context.Context, name string, args ...string) *exec.Cmd {
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
		helperModeEnv+"=1",
		helperStdoutEnv+"="+stdout,
		helperExitEnv+"="+strconv.Itoa(exit),
	)
	f.lastCmd = cmd
	return cmd
}

// argsOf returns the recorded argv of the first call whose leading argument equals verb. | argsOf 返回首个首参数等于 verb 的调用 argv。
func (f *fakeExec) argsOf(t *testing.T, verb string) []string {
	t.Helper()
	for _, argv := range f.calls {
		if len(argv) > 1 && argv[1] == verb {
			return argv
		}
	}
	t.Fatalf("未记录到以 %q 开头的调用，实际调用: %v", verb, f.calls)
	return nil
}

// flagValue returns the value of a "-flag value" / "-flag=value" pair. | flagValue 返回 "-flag value" 或 "-flag=value" 形式的取值。
func flagValue(args []string, flag string) (string, bool) {
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

// fakeGoBuild mimics `go build`: it records the argv and writes a fake artifact
// (magic + padding) to the -o path so the post-build verification can run. | fakeGoBuild
// 模拟 `go build`：记录 argv，并按 -o 路径写出伪造产物（魔数 + 填充），使构建后校验得以执行。
func fakeGoBuild(magic []byte) *fakeExec {
	return fakeGoBuildWith(func(string) []byte { return magic })
}

// fakeGoBuildAuto picks the magic that matches each artifact name, which is what a
// multi-target fake needs. | fakeGoBuildAuto 按产物名挑选匹配的魔数，多目标伪造需要它。
func fakeGoBuildAuto() *fakeExec { return fakeGoBuildWith(magicForPath) }

// magicForPath returns the magic of the executable format implied by the file name. | magicForPath 返回文件名所暗示的可执行格式的魔数。
func magicForPath(path string) []byte {
	if strings.HasSuffix(strings.ToLower(path), ".exe") {
		return peMagic
	}
	return elfMagic
}

// fakeGoBuildWith builds the fake around a per-artifact magic picker. | fakeGoBuildWith 以按产物挑选魔数的函数构造伪造器。
func fakeGoBuildWith(pick func(path string) []byte) *fakeExec {
	return &fakeExec{
		handler: func(string, []string) (string, int) { return "", 0 },
		onCall: func(_ string, args []string) {
			path, ok := flagValue(args, "-o")
			if !ok {
				return
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return
			}
			body := append(append([]byte{}, pick(path)...), make([]byte, 128)...)
			_ = os.WriteFile(path, body, 0o755)
		},
	}
}

// testLogger returns a Logger that discards output, keeping test logs clean. | testLogger 返回丢弃输出的 Logger，保持测试输出干净。
func testLogger() *logx.Logger {
	return logx.New(logx.Options{Writer: io.Discard, Level: logx.DebugLevel})
}

// writeFile writes a fixture file, creating parent directories as needed. | writeFile 写入夹具文件，必要时创建父目录。
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录 %s 失败: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

// newTestModule writes a minimal Go module into a temp dir. The module path matches
// the real repository on purpose: the -X flag targets github.com/shibingli/yd/core/version,
// so the injected package has to live at exactly that import path. | newTestModule 在临时
// 目录写入最小 Go 模块。模块路径刻意与真实仓库一致：-X 目标为 github.com/shibingli/yd/core/version，
// 因此被注入的包必须位于该导入路径之下。
func newTestModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module github.com/shibingli/yd\n\ngo 1.27\n")
	writeFile(t, filepath.Join(dir, "core", "version", "version.go"), `package version

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func String() string { return Version + "|" + Commit + "|" + Date }
`)
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import (
	"os"

	"github.com/shibingli/yd/core/version"
)

func main() { _, _ = os.Stdout.WriteString(version.String()) }
`)
	return dir
}

// readHead reads the first n bytes of a file. | readHead 读取文件开头的 n 个字节。
func readHead(t *testing.T, path string, n int) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开 %s 失败: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil {
		t.Fatalf("读取 %s 头部失败: %v", path, err)
	}
	return buf
}

// exeName returns the native executable file name for the current platform. | exeName 返回当前平台的原生可执行文件名。
func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// ---------------------------------------------------------------- Real compilation | 真实编译

// TestBuildProducesStaticBinary really compiles a tiny main package and verifies the
// produced artifact: it must exist, be non-empty, carry the ELF magic and expose
// readable build info through `go version -m`. | TestBuildProducesStaticBinary 真实编译一个
// 极小 main 包并校验产物：必须存在、非空、带 ELF 魔数，且 `go version -m` 能读出构建信息。
func TestBuildProducesStaticBinary(t *testing.T) {
	dir := newTestModule(t)
	out := filepath.Join(t.TempDir(), "app-linux-amd64")

	start := time.Now()
	report, err := Build(context.Background(), Options{
		Dir:      dir,
		Output:   out,
		Targets:  []Target{{GOOS: "linux", GOARCH: "amd64"}},
		TrimPath: true,
	}, testLogger())
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	elapsed := time.Since(start)

	if len(report.Artifacts) != 1 {
		t.Fatalf("产物数量 = %d, 期望 1", len(report.Artifacts))
	}
	art := report.Artifacts[0]
	if art.Path != out {
		t.Errorf("产物路径 = %q, 期望 %q", art.Path, out)
	}
	if art.GOOS != "linux" || art.GOARCH != "amd64" {
		t.Errorf("产物目标 = %s/%s, 期望 linux/amd64", art.GOOS, art.GOARCH)
	}
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatalf("产物不存在: %v", err)
	}
	if art.Bytes != fi.Size() || art.Bytes <= 0 {
		t.Errorf("产物大小 = %d, 实际文件 = %d, 需大于 0", art.Bytes, fi.Size())
	}
	if art.Duration <= 0 || art.Duration > elapsed {
		t.Errorf("产物耗时 = %v, 应落在 (0, %v] 区间", art.Duration, elapsed)
	}
	if got := readHead(t, out, 4); !bytes.Equal(got, elfMagic) {
		t.Fatalf("产物魔数 = %x, 期望 ELF %x", got, elfMagic)
	}
	t.Logf("真实产物: %s, %d 字节, 魔数 % x, 耗时 %v", art.Path, art.Bytes, readHead(t, out, 4), art.Duration)

	meta, err := exec.Command("go", "version", "-m", out).CombinedOutput()
	if err != nil {
		t.Fatalf("go version -m 失败: %v\n%s", err, meta)
	}
	for _, want := range []string{"github.com/shibingli/yd", "-trimpath", "CGO_ENABLED=0"} {
		if !strings.Contains(string(meta), want) {
			t.Errorf("go version -m 输出缺少 %q:\n%s", want, meta)
		}
	}

	// A static ELF carries no interpreter, so the dynamic loader must not be referenced. | 静态 ELF 不带解释器，因此不应引用动态加载器。
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("读取产物失败: %v", err)
	}
	for _, unwanted := range []string{"ld-linux", "libc.so"} {
		if bytes.Contains(body, []byte(unwanted)) {
			t.Errorf("产物引用了 %q, 说明不是静态链接", unwanted)
		}
	}
}

// TestBuildMultiTargetProducesRealArtifacts cross-compiles the tiny module for Linux
// and Windows at once and pins the dist/ naming plus both executable magics. | TestBuildMultiTargetProducesRealArtifacts
// 一次性为目标 Linux 与 Windows 交叉编译极小模块，固定 dist/ 命名规则与两种可执行魔数。
func TestBuildMultiTargetProducesRealArtifacts(t *testing.T) {
	dir := newTestModule(t)
	targets := []Target{{GOOS: "linux", GOARCH: "amd64"}, {GOOS: "windows", GOARCH: "amd64"}}

	report, err := Build(context.Background(), Options{
		Dir:      dir,
		Name:     "app",
		Targets:  targets,
		TrimPath: true,
	}, testLogger())
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	if len(report.Artifacts) != 2 {
		t.Fatalf("产物数量 = %d, 期望 2", len(report.Artifacts))
	}

	for i, want := range []struct {
		name  string
		magic []byte
	}{
		{name: "app_linux_amd64", magic: elfMagic},
		{name: "app_windows_amd64.exe", magic: peMagic},
	} {
		art := report.Artifacts[i]
		if filepath.Base(art.Path) != want.name {
			t.Errorf("产物文件名 = %q, 期望 %q", filepath.Base(art.Path), want.name)
		}
		if filepath.Base(filepath.Dir(art.Path)) != "dist" {
			t.Errorf("产物目录 = %q, 期望 dist", filepath.Dir(art.Path))
		}
		if got := readHead(t, art.Path, len(want.magic)); !bytes.Equal(got, want.magic) {
			t.Errorf("%s 魔数 = %x, 期望 %x", want.name, got, want.magic)
		}
		if art.Bytes <= 0 {
			t.Errorf("%s 大小 = %d, 需大于 0", want.name, art.Bytes)
		}
		t.Logf("真实产物: %s, %d 字节, 魔数 % x, 耗时 %v", art.Path, art.Bytes, readHead(t, art.Path, len(want.magic)), art.Duration)
	}
}

// TestBuildInjectsVersionLdflags builds the module for the host platform, runs the
// artifact and asserts the version triple injected through -X. | TestBuildInjectsVersionLdflags
// 为主机平台编译该模块，运行产物并断言通过 -X 注入的版本三元组。
func TestBuildInjectsVersionLdflags(t *testing.T) {
	dir := newTestModule(t)
	out := filepath.Join(t.TempDir(), exeName("app"))

	_, err := Build(context.Background(), Options{
		Dir:      dir,
		Output:   out,
		Targets:  []Target{{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}},
		TrimPath: true,
		Version:  "v9.9.9",
		Commit:   "abcdef0",
		Date:     "2026-10-16T00:00:00Z",
	}, testLogger())
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}

	got, err := exec.Command(out).Output()
	if err != nil {
		t.Fatalf("运行产物失败: %v", err)
	}
	const want = "v9.9.9|abcdef0|2026-10-16T00:00:00Z"
	if string(got) != want {
		t.Fatalf("产物输出 = %q, 期望 %q", got, want)
	}
}

// TestGenerateLDFlags pins the argv fragment appended to `go build`. | TestGenerateLDFlags 固定追加到 `go build` 的参数片段。
func TestGenerateLDFlags(t *testing.T) {
	got := GenerateLDFlags(Options{
		LDFlags: []string{"-X", "main.build=ci"},
		Version: "v1.2.3",
		Commit:  "deadbee",
		Date:    "2026-10-16T08:00:00Z",
	})
	if len(got) != 2 || got[0] != "-ldflags" {
		t.Fatalf("GenerateLDFlags = %v, 期望首个元素为 -ldflags", got)
	}
	for _, want := range []string{
		"-s", "-w",
		"-X main.build=ci",
		"-X github.com/shibingli/yd/core/version.Version=v1.2.3",
		"-X github.com/shibingli/yd/core/version.Commit=deadbee",
		"-X github.com/shibingli/yd/core/version.Date=2026-10-16T08:00:00Z",
	} {
		if !strings.Contains(got[1], want) {
			t.Errorf("GenerateLDFlags 值 %q 缺少 %q", got[1], want)
		}
	}

	// Omitting the version triple leaves only the size-reducing flags. | 省略版本三元组时只保留瘦身标志。
	bare := GenerateLDFlags(Options{})
	if bare[1] != "-s -w" {
		t.Errorf("空版本时 GenerateLDFlags 值 = %q, 期望 %q", bare[1], "-s -w")
	}
}

// ---------------------------------------------------------------- Argument construction | 参数构造

// TestBuildOfflineUsesVendorMode asserts that Offline forces -mod=vendor. | TestBuildOfflineUsesVendorMode 断言 Offline 强制使用 -mod=vendor。
func TestBuildOfflineUsesVendorMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		offline bool
		want    bool
	}{
		{name: "offline", offline: true, want: true},
		{name: "online", offline: false, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := fakeGoBuild(elfMagic)
			_, err := Build(context.Background(), Options{
				Dir:         t.TempDir(),
				Name:        "app",
				Output:      filepath.Join(t.TempDir(), "app"),
				Targets:     []Target{{GOOS: "linux", GOARCH: "amd64"}},
				Offline:     tc.offline,
				TrimPath:    true,
				ExecCommand: fake.exec,
			}, testLogger())
			if err != nil {
				t.Fatalf("Build 失败: %v", err)
			}
			args := fake.argsOf(t, "build")
			if got := slices.Contains(args, "-mod=vendor"); got != tc.want {
				t.Fatalf("argv = %v, -mod=vendor 出现 = %v, 期望 %v", args, got, tc.want)
			}
		})
	}
}

// TestBuildPassesTagsAndTrimPath asserts the optional go build flags reach the argv. | TestBuildPassesTagsAndTrimPath 断言可选的 go build 标志出现在 argv 中。
func TestBuildPassesTagsAndTrimPath(t *testing.T) {
	fake := fakeGoBuild(elfMagic)
	_, err := Build(context.Background(), Options{
		Dir:         t.TempDir(),
		Name:        "app",
		Output:      filepath.Join(t.TempDir(), "app"),
		Targets:     []Target{{GOOS: "linux", GOARCH: "amd64"}},
		TrimPath:    true,
		Tags:        []string{"jsonx_std", "b64x_std"},
		Package:     "./cmd/app",
		LDFlags:     []string{"-X", "main.build=ci"},
		ExecCommand: fake.exec,
	}, testLogger())
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	args := fake.argsOf(t, "build")
	for _, want := range []string{"-trimpath", "-ldflags", "-o"} {
		if !slices.Contains(args, want) {
			t.Errorf("argv = %v, 缺少 %q", args, want)
		}
	}
	if got, _ := flagValue(args, "-tags"); got != "jsonx_std,b64x_std" {
		t.Errorf("-tags = %q, 期望 %q", got, "jsonx_std,b64x_std")
	}
	if got, _ := flagValue(args, "-ldflags"); !strings.Contains(got, "-X main.build=ci") {
		t.Errorf("-ldflags = %q, 缺少调用方传入的 LDFlags", got)
	}
	if args[len(args)-1] != "./cmd/app" {
		t.Errorf("argv 末尾 = %q, 期望构建入口 ./cmd/app", args[len(args)-1])
	}
	if out, _ := flagValue(args, "-o"); out == "" {
		t.Errorf("argv = %v, 缺少 -o 取值", args)
	}
}

// TestBuildSetsCrossCompileEnv asserts CGO_ENABLED=0 and the GOOS/GOARCH overrides,
// including caller supplied Env entries which must win over os.Environ(). | TestBuildSetsCrossCompileEnv
// 断言 CGO_ENABLED=0 与 GOOS/GOARCH 覆盖，且调用方传入的 Env 条目优先于 os.Environ()。
func TestBuildSetsCrossCompileEnv(t *testing.T) {
	fake := fakeGoBuild(peMagic)
	_, err := Build(context.Background(), Options{
		Dir:         t.TempDir(),
		Name:        "app",
		Output:      filepath.Join(t.TempDir(), "app.exe"),
		Targets:     []Target{{GOOS: "windows", GOARCH: "amd64"}},
		TrimPath:    true,
		Env:         []string{"CGO_ENABLED=1", "YD_BUILD_FROM_TEST=1"},
		ExecCommand: fake.exec,
	}, testLogger())
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	if fake.lastCmd == nil {
		t.Fatal("no *exec.Cmd was recorded | 未记录到 *exec.Cmd")
	}
	env := envMap(fake.lastCmd.Env)
	for key, want := range map[string]string{
		"GOOS":               "windows",
		"GOARCH":             "amd64",
		"CGO_ENABLED":        "1",
		"YD_BUILD_FROM_TEST": "1",
	} {
		if env[key] != want {
			t.Errorf("环境变量 %s = %q, 期望 %q", key, env[key], want)
		}
	}
	if fake.lastCmd.Dir == "" {
		t.Error("the build command has no working directory | 构建命令缺少工作目录")
	}
}

// envMap folds an environment slice into a map; later duplicates win. | envMap 把环境变量切片折叠为映射，后出现的重复项生效。
func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// TestBuildMultiTargetOutputsDistNames pins the dist/<name>_<os>_<arch> naming. | TestBuildMultiTargetOutputsDistNames 固定 dist/<name>_<os>_<arch> 命名规则。
func TestBuildMultiTargetOutputsDistNames(t *testing.T) {
	targets, err := ParseTargets("linux/amd64,linux/arm64,windows/amd64")
	if err != nil {
		t.Fatalf("ParseTargets 失败: %v", err)
	}
	dir := t.TempDir()
	fake := fakeGoBuildAuto()
	report, err := Build(context.Background(), Options{
		Dir:         dir,
		Name:        "app",
		Output:      filepath.Join(dir, "ignored-for-multi-target"),
		Targets:     targets,
		TrimPath:    true,
		ExecCommand: fake.exec,
	}, testLogger())
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	if len(report.Artifacts) != len(targets) {
		t.Fatalf("产物数量 = %d, 期望 %d", len(report.Artifacts), len(targets))
	}
	for i, want := range []string{
		"app_linux_amd64",
		"app_linux_arm64",
		"app_windows_amd64.exe",
	} {
		art := report.Artifacts[i]
		if got := filepath.Base(art.Path); got != want {
			t.Errorf("产物文件名 = %q, 期望 %q", got, want)
		}
		if got := filepath.Base(filepath.Dir(art.Path)); got != "dist" {
			t.Errorf("产物目录 = %q, 期望 dist", got)
		}
		if art.GOOS != targets[i].GOOS || art.GOARCH != targets[i].GOARCH {
			t.Errorf("产物目标 = %s/%s, 期望 %s", art.GOOS, art.GOARCH, targets[i])
		}
	}
	if len(fake.calls) != len(targets) {
		t.Errorf("调用次数 = %d, 期望 %d", len(fake.calls), len(targets))
	}
}

// TestBuildSingleTargetDefaultsOutput asserts the bin/<name> default for one target. | TestBuildSingleTargetDefaultsOutput 断言单目标时 bin/<name> 的默认输出。
func TestBuildSingleTargetDefaultsOutput(t *testing.T) {
	dir := t.TempDir()
	fake := fakeGoBuild(elfMagic)
	report, err := Build(context.Background(), Options{
		Dir:         dir,
		Name:        "app",
		Targets:     []Target{{GOOS: "linux", GOARCH: "amd64"}},
		ExecCommand: fake.exec,
	}, testLogger())
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	want := filepath.Join(dir, "bin", "app")
	if report.Artifacts[0].Path != want {
		t.Fatalf("产物路径 = %q, 期望 %q", report.Artifacts[0].Path, want)
	}
}

// ---------------------------------------------------------------- Tailwind hook | Tailwind 钩子

// TestBuildRunsInjectedTailwindHook asserts the injectable Tailwind stage runs once
// with the resolved options and that Embed also turns it on. | TestBuildRunsInjectedTailwindHook
// 断言可注入的 Tailwind 阶段按解析后的选项执行一次，且 Embed 同样会启用它。
func TestBuildRunsInjectedTailwindHook(t *testing.T) {
	for _, tc := range []struct {
		name         string
		tailwind     TailwindOptions
		embed        bool
		wantMinified bool
	}{
		{name: "explicit", tailwind: TailwindOptions{Enabled: true, Minify: true}, wantMinified: true},
		{name: "embed forces assets", embed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fake := fakeGoBuild(elfMagic)
			var (
				calls    int
				gotDir   string
				gotTail  TailwindOptions
				execSeen bool
			)
			tail := func(_ context.Context, d string, o TailwindOptions) (int64, error) {
				calls++
				gotDir, gotTail = d, o
				execSeen = len(fake.calls) == 0
				return 2048, nil
			}
			_, err := Build(context.Background(), Options{
				Dir:          dir,
				Name:         "app",
				Targets:      []Target{{GOOS: "linux", GOARCH: "amd64"}},
				Tailwind:     tc.tailwind,
				Embed:        tc.embed,
				TailwindFunc: tail,
				ExecCommand:  fake.exec,
			}, testLogger())
			if err != nil {
				t.Fatalf("Build 失败: %v", err)
			}
			if calls != 1 {
				t.Fatalf("Tailwind 钩子调用次数 = %d, 期望 1", calls)
			}
			if !execSeen {
				t.Error("the Tailwind stage must run before go build | Tailwind 阶段必须在 go build 之前执行")
			}
			if gotDir != dir {
				t.Errorf("Tailwind 目录 = %q, 期望 %q", gotDir, dir)
			}
			if !gotTail.Enabled {
				t.Errorf("Tailwind 选项 = %+v, 期望 Enabled 为 true", gotTail)
			}
			if gotTail.Minify != tc.wantMinified {
				t.Errorf("Tailwind Minify = %v, 期望 %v", gotTail.Minify, tc.wantMinified)
			}
			if gotTail.Input != DefaultTailwindInput || gotTail.Output != DefaultTailwindOutput {
				t.Errorf("Tailwind 输入/输出 = %q/%q, 期望默认值 %q/%q", gotTail.Input, gotTail.Output, DefaultTailwindInput, DefaultTailwindOutput)
			}
		})
	}
}

// TestBuildTailwindHookFailureAborts asserts a failing Tailwind stage stops the build. | TestBuildTailwindHookFailureAborts 断言 Tailwind 阶段失败会中止构建。
func TestBuildTailwindHookFailureAborts(t *testing.T) {
	fake := fakeGoBuild(elfMagic)
	_, err := Build(context.Background(), Options{
		Dir:         t.TempDir(),
		Name:        "app",
		Targets:     []Target{{GOOS: "linux", GOARCH: "amd64"}},
		Tailwind:    TailwindOptions{Enabled: true},
		ExecCommand: fake.exec,
		TailwindFunc: func(context.Context, string, TailwindOptions) (int64, error) {
			return 0, errFakeTailwind
		},
	}, testLogger())
	if !errors.Is(err, errFakeTailwind) {
		t.Fatalf("错误 = %v, 期望 Tailwind 钩子的错误原样返回", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("Tailwind 失败后仍执行了 %d 次 go build", len(fake.calls))
	}
}

// ---------------------------------------------------------------- Failure paths | 失败路径

// TestBuildReportsCompileFailure asserts the compile error carries the compiler output. | TestBuildReportsCompileFailure 断言编译错误携带编译器输出。
func TestBuildReportsCompileFailure(t *testing.T) {
	fake := &fakeExec{handler: func(string, []string) (string, int) {
		return "main.go:3:1: undefined: nope\n", 1
	}}
	_, err := Build(context.Background(), Options{
		Dir:         t.TempDir(),
		Name:        "app",
		Targets:     []Target{{GOOS: "linux", GOARCH: "amd64"}},
		ExecCommand: fake.exec,
	}, testLogger())
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("错误 = %v, 期望 errors.Is(err, ErrCompile)", err)
	}
	if !strings.Contains(err.Error(), "undefined: nope") {
		t.Errorf("错误 = %v, 期望包含编译器输出", err)
	}
}

// TestBuildRejectsMissingArtifact asserts a vanished artifact is reported. | TestBuildRejectsMissingArtifact 断言产物缺失会被报告。
func TestBuildRejectsMissingArtifact(t *testing.T) {
	fake := &fakeExec{handler: func(string, []string) (string, int) { return "", 0 }}
	_, err := Build(context.Background(), Options{
		Dir:         t.TempDir(),
		Name:        "app",
		Targets:     []Target{{GOOS: "linux", GOARCH: "amd64"}},
		ExecCommand: fake.exec,
	}, testLogger())
	if !errors.Is(err, ErrArtifactMissing) {
		t.Fatalf("错误 = %v, 期望 errors.Is(err, ErrArtifactMissing)", err)
	}
}

// TestBuildRejectsForeignMagic asserts the executable magic is validated per GOOS. | TestBuildRejectsForeignMagic 断言按 GOOS 校验可执行魔数。
func TestBuildRejectsForeignMagic(t *testing.T) {
	fake := fakeGoBuild([]byte{0xCA, 0xFE, 0x00, 0x00})
	_, err := Build(context.Background(), Options{
		Dir:         t.TempDir(),
		Name:        "app",
		Targets:     []Target{{GOOS: "linux", GOARCH: "amd64"}},
		ExecCommand: fake.exec,
	}, testLogger())
	if !errors.Is(err, ErrArtifactFormat) {
		t.Fatalf("错误 = %v, 期望 errors.Is(err, ErrArtifactFormat)", err)
	}
}

// TestBuildDefaultsTargetsToHost asserts an empty Targets list falls back to the host platform. | TestBuildDefaultsTargetsToHost 断言 Targets 为空时回退到主机平台。
func TestBuildDefaultsTargetsToHost(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.27\n")
	fake := fakeGoBuildAuto()
	report, err := Build(context.Background(), Options{
		Dir:         dir,
		Targets:     nil,
		ExecCommand: fake.exec,
	}, testLogger())
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	art := report.Artifacts[0]
	if art.GOOS != runtime.GOOS || art.GOARCH != runtime.GOARCH {
		t.Fatalf("默认目标 = %s/%s, 期望 %s/%s", art.GOOS, art.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	if want := filepath.Join(dir, "bin", exeName("demo")); art.Path != want {
		t.Fatalf("产物路径 = %q, 期望 %q", art.Path, want)
	}
}
