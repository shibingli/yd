// This file tests the ko command: registration, help output, the flag-to-options mapping, the
// bilingual session summary and every error hint. The ko runner is always replaced with a fake, so no
// test starts ko, reaches a registry or touches the network. | 本文件测试 ko 命令：注册、帮助输出、
// 标志到选项的映射、双语会话摘要与各类错误提示。ko 执行器始终被替换为假实现，因此任何测试都不会启动 ko、
// 接触镜像仓库或网络。

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/spf13/pflag"

	"github.com/shibingli/yd/tooling/ko"
	"github.com/shibingli/ydx/afs"
	"github.com/shibingli/ydx/config"
)

// cliKoDigest is the sha256 digest carried by the canned ko output. | cliKoDigest 是预置 ko 输出携带的 sha256 摘要。
var cliKoDigest = "sha256:" + strings.Repeat("b", 64)

// cliKoImage is the image reference the fake ko prints. | cliKoImage 是假 ko 打印的镜像引用。
var cliKoImage = "registry.example.com/team/app:v1.2.3@" + cliKoDigest

// cliKoOutput is the canned merged output of a successful `ko build`. | cliKoOutput 是一次成功 `ko build` 的预置合并输出。
var cliKoOutput = strings.Join([]string{
	"2026/10/17 10:00:00 Building github.com/shibingli/yd/cmd/app for linux/amd64",
	"2026/10/17 10:00:03 published " + cliKoImage,
	cliKoImage,
}, "\n")

// cliKoCall records one invocation of the fake ko runner. | cliKoCall 记录一次假 ko 执行器的调用。
type cliKoCall struct {
	name string
	args []string
	dir  string
	env  []string
}

// cliFakeKo mimics the ko CLI: it records the invocation and replays one canned result. | cliFakeKo
// 模拟 ko CLI：记录调用并回放固定结果。
type cliFakeKo struct {
	err    error
	output string
	calls  []cliKoCall
}

// exec implements ko.ExecCommandFunc. | exec 实现 ko.ExecCommandFunc。
func (f *cliFakeKo) exec(_ context.Context, name string, args []string, dir string, env []string) ([]byte, error) {
	f.calls = append(f.calls, cliKoCall{name: name, args: args, dir: dir, env: env})
	return []byte(f.output), f.err
}

// useKoExec injects a fake ko runner into the ko command and stubs the resolution chain, so the real
// PATH, YD_KO_BIN and .yd/tools can never leak into a test. | useKoExec 为 ko 命令注入假执行器并打桩解析链，
// 使真实 PATH、YD_KO_BIN 与 .yd/tools 绝不会影响测试。
func useKoExec(t *testing.T, output string, runErr error) *cliFakeKo {
	t.Helper()
	fake := &cliFakeKo{output: output, err: runErr}
	oldExec := koExecCommand
	koExecCommand = fake.exec
	t.Cleanup(func() { koExecCommand = oldExec })

	oldLookup := ko.PathLookup
	ko.PathLookup = func(name string) (string, error) {
		if name == ko.DefaultBinary {
			return "/resolved/ko", nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { ko.PathLookup = oldLookup })
	t.Setenv(ko.EnvBinary, "")
	return fake
}

// koParseFlags parses args with a fresh ko flag set. | koParseFlags 用全新的 ko 标志集解析 args。
func koParseFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet(koCommandName, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerKoFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("解析 ko 标志失败: %v", err)
	}
	return fs
}

// newKoTestEnv builds an env whose working directory is a real temporary directory, because the ko
// package resolves its local tool lookup and runs its child process relative to it. | newKoTestEnv
// 构造工作目录为真实临时目录的环境，因为 ko 包会在其中解析本地工具并运行子进程。
func newKoTestEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	dir := t.TempDir()
	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	return te, dir
}

// ---------------------------------------------------------------- Tests | 测试

// ko must stay registered exactly once with every flag: Register panics on duplicates. | TestKoCommandRegistered ko 只注册一次且标志齐全，Register 对重名会 panic。
func TestKoCommandRegistered(t *testing.T) {
	count := 0
	for _, c := range Commands() {
		if c.Name != koCommandName {
			continue
		}
		count++
		if c.Run == nil || c.Flags == nil {
			t.Error("the ko command is missing Run or Flags | ko 命令缺少 Run 或 Flags")
		}
		if c.Short == "" || !strings.Contains(c.Short, " | ") {
			t.Errorf("Short 必须中英同步: %q", c.Short)
		}
		if c.Usage != koUsageLine {
			t.Errorf("Usage = %q，期望 %q", c.Usage, koUsageLine)
		}
	}
	if count != 1 {
		t.Fatalf("ko 命令注册了 %d 次，期望 1 次", count)
	}

	fs := pflag.NewFlagSet(koCommandName, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerKoFlags(fs)
	for _, name := range []string{
		flagKoRepo, flagKoTags, flagKoPlatforms, flagKoBaseImage,
		flagKoPackage, flagKoLocal, flagKoPush, flagKoNoConfig, flagKoBinary, flagKoDir,
	} {
		if fs.Lookup(name) == nil {
			t.Errorf("ko 命令缺少标志 --%s", name)
		}
	}
}

// ko --help must list every flag without building anything. | TestKoCommandHelp ko --help 必须列出全部标志并且不执行构建。
func TestKoCommandHelp(t *testing.T) {
	fake := useKoExec(t, cliKoOutput, nil)

	te, _ := newKoTestEnv(t)
	if err := Execute(context.Background(), []string{"ko", "--help"}, te.env); err != nil {
		t.Fatalf("ko --help 不应返回错误: %v", err)
	}
	out := te.out.String()
	for _, want := range []string{
		"yd ko", "Usage",
		"--" + flagKoRepo, "--" + flagKoTags, "--" + flagKoPlatforms, "--" + flagKoBaseImage,
		"--" + flagKoPackage, "--" + flagKoLocal, "--" + flagKoPush, "--" + flagKoNoConfig,
		"--" + flagKoBinary, "--" + flagKoDir,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ko 帮助缺少 %q：\n%s", want, out)
		}
	}
	if len(fake.calls) != 0 {
		t.Errorf("--help 不得执行构建，实际调用 %v | --help must not build, got %v", fake.calls, fake.calls)
	}
}

// An unknown flag must be wrapped into ErrFlagParse. | TestKoUnknownFlagErrors 未知标志必须包装为 ErrFlagParse。
func TestKoUnknownFlagErrors(t *testing.T) {
	te, _ := newKoTestEnv(t)
	if err := Execute(context.Background(), []string{"ko", "--nope"}, te.env); !errors.Is(err, ErrFlagParse) {
		t.Fatalf("未知标志应返回 ErrFlagParse，实际 %v", err)
	}
}

// TestKoFlagsMapToOptions | TestKoFlagsMapToOptions 逐项校验标志到 ko.Options 的映射，含位置参数与显式关闭的布尔标志。
// checks the flag-to-options mapping flag by flag, positional package arguments and explicitly disabled booleans included.
func TestKoFlagsMapToOptions(t *testing.T) {
	t.Setenv(ko.EnvBinary, "")
	te, dir := newKoTestEnv(t)

	fs := koParseFlags(t,
		"--repo", " registry.example.com/team/app ",
		"--tags", "v1.2.3,latest",
		"--platforms", "linux/amd64,linux/arm64",
		"--base-image", "cgr.dev/chainguard/static:latest",
		"--package", "./cmd/api",
		"--local", "--push",
		"--binary", "/opt/ko",
		"--dir", "sub",
		"./cmd/worker",
	)
	o := koOptions(te.env, fs)

	if o.Repository != "registry.example.com/team/app" {
		t.Errorf("Repository = %q | Repository 得到 %q", o.Repository, o.Repository)
	}
	if !slices.Equal(o.Tags, []string{"v1.2.3", "latest"}) {
		t.Errorf("Tags = %q | Tags 得到 %q", o.Tags, o.Tags)
	}
	if !slices.Equal(o.Platforms, []string{"linux/amd64", "linux/arm64"}) {
		t.Errorf("Platforms = %q | Platforms 得到 %q", o.Platforms, o.Platforms)
	}
	if o.BaseImage != "cgr.dev/chainguard/static:latest" {
		t.Errorf("BaseImage = %q | BaseImage 得到 %q", o.BaseImage, o.BaseImage)
	}
	if !slices.Equal(o.Packages, []string{"./cmd/api", "./cmd/worker"}) {
		t.Errorf("Packages = %q | Packages 得到 %q，期望 --package 取值在前、位置参数在后", o.Packages, o.Packages)
	}
	if !o.Local || !o.Push {
		t.Errorf("Local/Push = %v/%v | Local/Push 得到 %v/%v，期望都为 true", o.Local, o.Push, o.Local, o.Push)
	}
	if o.Binary != "/opt/ko" {
		t.Errorf("Binary = %q | Binary 得到 %q", o.Binary, o.Binary)
	}
	if want := filepath.Join(dir, "sub"); o.WorkingDir != want {
		t.Errorf("WorkingDir = %q | WorkingDir 得到 %q，期望 %q", o.WorkingDir, o.WorkingDir, want)
	}
}

// TestKoFlagsNotPassedKeepConfig ko 只在标志确实传入时覆盖配置：--local=false 能关掉配置里的 ko.local=true，
// TestKoFlagsNotPassedKeepConfig keeps the config value unless the flag | 未传的 --repo 则保留配置文件取值。
// was actually passed: --local=false turns off a configured ko.local=true, while an absent --repo keeps the file.
func TestKoFlagsNotPassedKeepConfig(t *testing.T) {
	t.Setenv(ko.EnvBinary, "")
	te, _ := newKoTestEnv(t)
	te.env.Cfg = loadKoCLIConfig(t, `
ko:
  repository: registry.example.com/from-file
  local: true
  push: true
`)

	fromFile := koOptions(te.env, koParseFlags(t))
	if fromFile.Repository != "registry.example.com/from-file" || !fromFile.Local || !fromFile.Push {
		t.Errorf("未传标志时应保留配置：%+v | the config must survive untouched: %+v", fromFile, fromFile)
	}

	overridden := koOptions(te.env, koParseFlags(t, "--local=false", "--repo", "registry.example.com/from-flag"))
	if overridden.Local {
		t.Error("--local=false 必须关掉配置里的 ko.local=true | --local=false must disable a configured ko.local=true")
	}
	if !overridden.Push {
		t.Error("未传 --push 时配置里的 ko.push=true 必须保留 | the configured ko.push=true must survive an absent --push")
	}
	if overridden.Repository != "registry.example.com/from-flag" {
		t.Errorf("Repository = %q | Repository 得到 %q，期望标志取值", overridden.Repository, overridden.Repository)
	}
}

// TestKoNoConfigMapsToSkipConfig pins the default (package configs/config.yaml) and the opt-out. |
// TestKoNoConfigMapsToSkipConfig 固化默认（打包 configs/config.yaml）与显式关闭两种状态。
func TestKoNoConfigMapsToSkipConfig(t *testing.T) {
	te, _ := newKoTestEnv(t)

	if koOptions(te.env, koParseFlags(t)).SkipConfig {
		t.Error("默认必须打包配置文件 | configuration packaging must be on by default")
	}
	if !koOptions(te.env, koParseFlags(t, "--no-config")).SkipConfig {
		t.Error("--no-config 必须关闭配置打包 | --no-config must disable configuration packaging")
	}
}

// loadKoCLIConfig loads an in-memory configuration fixture with a test-only env prefix, so the
// developer's real YD_KO_* variables and the repository .env stay out of the assertion. | loadKoCLIConfig
// 加载带测试专用环境前缀的内存配置夹具，使开发机上真实的 YD_KO_* 变量与仓库 .env 不影响断言。
func loadKoCLIConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	mem := afs.NewMem()
	if err := afero.WriteFile(mem, "config.yaml", []byte(yaml), 0o644); err != nil {
		t.Fatalf("写入内存配置文件失败: %v", err)
	}
	c, err := config.Load(config.Options{
		Name:       "ko",
		ConfigDir:  ".",
		ConfigName: "config",
		EnvPrefix:  "YDKOCLITEST",
		EnvFile:    "absent.env",
		FS:         mem,
	})
	if err != nil {
		t.Fatalf("config.Load 失败: %v", err)
	}
	return c
}

// TestKoRunSummaryAndArgv | TestKoRunSummaryAndArgv 端到端校验：摘要先于构建写出、argv 形态正确、仓库经环境变量传递、镜像与摘要被解析出来。
// checks the whole command: the summary precedes the build, the argv has the documented shape, the
// repository travels in the environment, and the image and digest are parsed out of the output.
func TestKoRunSummaryAndArgv(t *testing.T) {
	fake := useKoExec(t, cliKoOutput, nil)

	te, dir := newKoTestEnv(t)
	err := Execute(context.Background(), []string{
		"ko",
		"--repo", "registry.example.com/team/app",
		"--tags", "v1.2.3,latest",
		"--platforms", "linux/amd64,linux/arm64",
		"--base-image", "cgr.dev/chainguard/static:latest",
		"--package", "./cmd/app",
		"--push",
	}, te.env)
	if err != nil {
		t.Fatalf("yd ko 失败: %v", err)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("ko 调用次数 = %d，期望 1 | ko ran %d times, want 1", len(fake.calls), len(fake.calls))
	}
	call := fake.calls[0]
	if call.name != "/resolved/ko" {
		t.Errorf("可执行文件 = %q，期望解析链给出的 /resolved/ko", call.name)
	}
	wantArgs := []string{
		"build", "--base-import-paths", "--push=true",
		"--tags", "v1.2.3", "--tags", "latest",
		"--platform", "linux/amd64", "--platform", "linux/arm64",
		"./cmd/app",
	}
	if !slices.Equal(call.args, wantArgs) {
		t.Errorf("ko argv = %q，期望 %q", call.args, wantArgs)
	}
	if call.dir != dir {
		t.Errorf("ko 工作目录 = %q，期望 %q", call.dir, dir)
	}
	if got := envValue(call.env, "KO_DOCKER_REPO"); got != "registry.example.com/team/app" {
		t.Errorf("KO_DOCKER_REPO = %q，期望仓库取值", got)
	}
	if got := envValue(call.env, "KO_DEFAULTBASEIMAGE"); got != "cgr.dev/chainguard/static:latest" {
		t.Errorf("KO_DEFAULTBASEIMAGE = %q，期望基础镜像取值", got)
	}

	out := te.out.String()
	for _, want := range []string{
		"package | 包: ./cmd/app",
		"repository | 仓库: registry.example.com/team/app",
		"tags | 标签: v1.2.3,latest",
		"platforms | 平台: linux/amd64,linux/arm64",
		"base image | 基础镜像: cgr.dev/chainguard/static:latest",
		"mode | 模式: push | 推送到仓库",
		"image | 镜像: " + cliKoImage,
		"digest | 摘要: " + cliKoDigest,
		"log lines | 日志行数: 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("摘要缺少 %q：\n%s", want, out)
		}
	}
}

// TestKoRunLocalWithoutRepository | TestKoRunLocalWithoutRepository --local 使仓库成为可选，并且 argv 里 --push 显式为 false。
// makes the repository optional under --local, and --push stays explicitly false in the argv.
func TestKoRunLocalWithoutRepository(t *testing.T) {
	fake := useKoExec(t, "ko.local/app:v1@"+cliKoDigest, nil)

	te, _ := newKoTestEnv(t)
	if err := Execute(context.Background(), []string{"ko", "--local"}, te.env); err != nil {
		t.Fatalf("yd ko --local 失败: %v", err)
	}

	wantArgs := []string{"build", "--base-import-paths", "--push=false", "--local", "./cmd/app"}
	if !slices.Equal(fake.calls[0].args, wantArgs) {
		t.Errorf("ko argv = %q，期望 %q", fake.calls[0].args, wantArgs)
	}
	out := te.out.String()
	if !strings.Contains(out, "mode | 模式: local daemon | 载入本地守护进程") {
		t.Errorf("摘要未报告本地模式：\n%s", out)
	}
	if !strings.Contains(out, "repository | 仓库: -") {
		t.Errorf("未配置仓库时应显示占位符：\n%s", out)
	}
}

// TestKoMissingRepositoryError | TestKoMissingRepositoryError 既未给仓库又未 --local 时返回 ErrNoRepository，并给出可执行的提示，且不执行 ko。
// returns ErrNoRepository with an actionable hint, without running ko at all.
func TestKoMissingRepositoryError(t *testing.T) {
	fake := useKoExec(t, cliKoOutput, nil)

	te, _ := newKoTestEnv(t)
	err := Execute(context.Background(), []string{"ko"}, te.env)
	if !errors.Is(err, ko.ErrNoRepository) {
		t.Fatalf("err = %v，期望 ErrNoRepository", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("校验失败时不得执行 ko，实际 %v | ko must not run after a validation failure: %v", fake.calls, fake.calls)
	}
	for _, want := range []string{"未配置镜像仓库", "--repo", "ko.repository", "KO_DOCKER_REPO", "--local"} {
		if !strings.Contains(te.errs.String(), want) {
			t.Errorf("错误提示缺少 %q：\n%s", want, te.errs.String())
		}
	}
}

// TestKoBinaryNotFoundHint | TestKoBinaryNotFoundHint ko 缺失时必须返回哨兵并打印安装提示，绝不 panic、绝不执行任何命令。
// requires the sentinel plus the installation hint when ko is missing, with no panic and no execution.
func TestKoBinaryNotFoundHint(t *testing.T) {
	fake := useKoExec(t, cliKoOutput, nil)
	ko.PathLookup = func(string) (string, error) { return "", os.ErrNotExist }

	te, _ := newKoTestEnv(t)
	err := Execute(context.Background(), []string{"ko", "--repo", "registry.example.com/team/app"}, te.env)
	if !errors.Is(err, ko.ErrBinaryNotFound) {
		t.Fatalf("err = %v，期望 ErrBinaryNotFound", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("ko 缺失时不得执行任何命令 | nothing may run when ko is missing: %v", fake.calls)
	}
	for _, want := range []string{"未找到 ko", "安装", ko.InstallHint, ko.LocalToolDir} {
		if !strings.Contains(te.errs.String(), want) {
			t.Errorf("安装提示缺少 %q：\n%s", want, te.errs.String())
		}
	}
}

// TestKoLocalDaemonHint | TestKoLocalDaemonHint --local 且本地守护进程不可达时，错误必须点明原因而不是埋在工具措辞里。
// spells out an unreachable local daemon instead of burying it in the tool's own wording.
func TestKoLocalDaemonHint(t *testing.T) {
	fake := useKoExec(t,
		"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
		errors.New("exit status 1"))

	te, _ := newKoTestEnv(t)
	err := Execute(context.Background(), []string{"ko", "--local"}, te.env)
	if !errors.Is(err, ko.ErrBuildFailed) {
		t.Fatalf("err = %v，期望 ErrBuildFailed", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("ko 调用次数 = %d，期望 1", len(fake.calls))
	}
	if !strings.Contains(te.errs.String(), "本地 docker 守护进程不可达") {
		t.Errorf("缺少守护进程不可达提示：\n%s", te.errs.String())
	}
}

// TestKoDirFlagResolvesAgainstWorkDir | TestKoDirFlagResolvesAgainstWorkDir --dir 的相对路径按 CLI 工作目录展开。
// resolves a relative --dir against the CLI working directory.
func TestKoDirFlagResolvesAgainstWorkDir(t *testing.T) {
	fake := useKoExec(t, cliKoOutput, nil)

	te, dir := newKoTestEnv(t)
	if err := Execute(context.Background(), []string{"ko", "--local", "--dir", "cmd"}, te.env); err != nil {
		t.Fatalf("yd ko --dir 失败: %v", err)
	}
	if want := filepath.Join(dir, "cmd"); fake.calls[0].dir != want {
		t.Errorf("ko 工作目录 = %q，期望 %q", fake.calls[0].dir, want)
	}
}

// TestKoEnvironmentWarning pins the production-environment hint: an image built with app.env unset runs in
// development unless the manifest injects YD_APP_ENV. | TestKoEnvironmentWarning 固定生产环境提示：
// app.env 未设置时，除非部署清单注入 YD_APP_ENV，镜像会以开发环境运行。
func TestKoEnvironmentWarning(t *testing.T) {
	cfg, err := config.Load(config.Options{Name: "yd", ConfigDir: t.TempDir(), FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	msg := koEnvironmentWarning(&Env{Cfg: cfg})
	if msg == "" || !strings.Contains(msg, "YD_APP_ENV=production") {
		t.Fatalf("warning = %q, want YD_APP_ENV hint | warning = %q，期望包含 YD_APP_ENV 提示", msg, msg)
	}

	cfg.Set("app.env", "production")
	if got := koEnvironmentWarning(&Env{Cfg: cfg}); got != "" {
		t.Fatalf("production warning = %q, want empty | production 下 warning = %q，期望为空", got, got)
	}
	cfg.Set("app.env", "prod")
	if got := koEnvironmentWarning(&Env{Cfg: cfg}); got != "" {
		t.Fatalf("prod alias warning = %q, want empty | prod 别名下 warning = %q，期望为空", got, got)
	}
	if got := koEnvironmentWarning(&Env{}); got != "" {
		t.Fatalf("warning without config = %q, want empty | 无配置时 warning = %q，期望为空", got, got)
	}
}

// TestKoHTTP2Warning pins the non-Linux deployment hint. | TestKoHTTP2Warning 固定非 Linux 部署提示。
func TestKoHTTP2Warning(t *testing.T) {
	cfg, err := config.Load(config.Options{Name: "yd", ConfigDir: t.TempDir(), FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	cfg.Set("server.http2.extended_connect", true)
	msg := koHTTP2Warning(&Env{Cfg: cfg})
	if msg == "" || !strings.Contains(msg, "GODEBUG=http2xconnect=1") {
		t.Fatalf("warning = %q, want GODEBUG hint | warning = %q，期望包含 GODEBUG 提示", msg, msg)
	}

	cfg2, err := config.Load(config.Options{Name: "yd", ConfigDir: t.TempDir(), FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if got := koHTTP2Warning(&Env{Cfg: cfg2}); got != "" {
		t.Fatalf("warning = %q, want empty | warning = %q，期望为空", got, got)
	}
	if got := koHTTP2Warning(&Env{}); got != "" {
		t.Fatalf("warning without config = %q, want empty | 无配置时 warning = %q，期望为空", got, got)
	}
}
