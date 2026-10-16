package docker

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/afs"
	"github.com/shibingli/yd/logx"
)

// ---------------------------------------------------------------- Test helpers | 测试辅助

const (
	// helperModeEnv marks the carrier process of the fake docker CLI. | helperModeEnv 标记假 docker CLI 的载体进程。
	helperModeEnv = "YD_TEST_HELPER_MODE"
	// helperStdoutEnv carries the canned stdout of the fake docker CLI. | helperStdoutEnv 承载假 docker CLI 的预置标准输出。
	helperStdoutEnv = "YD_TEST_HELPER_STDOUT"
	// helperExitEnv carries the exit code of the fake docker CLI. | helperExitEnv 承载假 docker CLI 的退出码。
	helperExitEnv = "YD_TEST_HELPER_EXIT"
)

// probeOutput is the canned answer of `docker version --format {{.Server.Version}}`. | probeOutput 是 `docker version --format {{.Server.Version}}` 的预置应答。
const probeOutput = "27.0.0\n"

// buildOutput mimics a BuildKit build that reports an image id, a tag and a size. | buildOutput 模拟一次报告镜像 ID、标签与体积的 BuildKit 构建输出。
const buildOutput = `#1 [internal] load build definition from Dockerfile
#9 exporting layers done
#9 writing image sha256:1111111111111111111111111111111111111111111111111111111111111111 done
#9 naming to docker.io/library/app:v1 done
#9 DONE 12.3s
Successfully built 111111111111
Successfully tagged app:v1
image size: 12.3MB
`

// daemonDownOutput is the message a docker CLI prints when no daemon answers. | daemonDownOutput 是 docker CLI 在守护进程无应答时输出的文案。
const daemonDownOutput = "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?\n"

// TestFakeCommandProcess is the carrier process of the injectable ExecCommand: the
// fake re-executes this test binary with -test.run=^TestFakeCommandProcess$, which
// then writes the canned stdout and exits with the canned code, so the tests never
// touch a real docker daemon or network. | TestFakeCommandProcess 是可注入 ExecCommand 的载体
// 进程：假命令以 -test.run=^TestFakeCommandProcess$ 重新执行本测试二进制，这里写出预置标准输出
// 并以预置退出码退出，因此测试全程不接触真实 docker 守护进程与网络。
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
	// probeStdout / probeExit configure the daemon probe answer. | probeStdout / probeExit 配置守护进程探活的应答。
	probeStdout string
	probeExit   int
	// buildStdout / buildExit configure the `docker build` answer. | buildStdout / buildExit 配置 `docker build` 的应答。
	buildStdout string
	buildExit   int
	// onBuild runs before the carrier process, used to inspect the materialized Dockerfile. | onBuild 在载体进程之前执行，用于检查落盘的 Dockerfile。
	onBuild func(args []string)
	// dockerfileSnapshots collects the content of the -f file of every build call. | dockerfileSnapshots 收集每次构建调用中 -f 文件的内容。
	dockerfileSnapshots []string
}

// exec implements the injectable ExecCommand signature. | exec 实现可注入的 ExecCommand 签名。
func (f *fakeExec) exec(ctx context.Context, name string, args ...string) *exec.Cmd {
	f.calls = append(f.calls, append([]string{name}, args...))
	stdout, exit := f.probeStdout, f.probeExit
	if len(args) > 0 && args[0] == "build" {
		stdout, exit = f.buildStdout, f.buildExit
		if path, ok := flagValue(args, "-f"); ok {
			if data, err := os.ReadFile(path); err == nil {
				f.dockerfileSnapshots = append(f.dockerfileSnapshots, string(data))
			}
		}
		if f.onBuild != nil {
			f.onBuild(args)
		}
	}
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFakeCommandProcess$")
	cmd.Env = append(os.Environ(),
		helperModeEnv+"=1",
		helperStdoutEnv+"="+stdout,
		helperExitEnv+"="+strconv.Itoa(exit),
	)
	return cmd
}

// verbs returns the leading argument of every recorded call, e.g. ["version" "build"]. | verbs 返回每次调用的首参数，例如 ["version" "build"]。
func (f *fakeExec) verbs() []string {
	out := make([]string, 0, len(f.calls))
	for _, argv := range f.calls {
		if len(argv) > 1 {
			out = append(out, argv[1])
		}
	}
	return out
}

// buildArgs returns the argv of the `docker build` call. | buildArgs 返回 `docker build` 调用的 argv。
func (f *fakeExec) buildArgs(t *testing.T) []string {
	t.Helper()
	for _, argv := range f.calls {
		if len(argv) > 1 && argv[1] == "build" {
			return argv
		}
	}
	t.Fatalf("未记录到 docker build 调用，实际调用: %v", f.calls)
	return nil
}

// flagValue returns the values of every "-flag value" / "-flag=value" occurrence. | flagValue 返回所有 "-flag value" / "-flag=value" 出现的取值。
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

// flagValues returns every value assigned to flag, in argv order. | flagValues 按 argv 顺序返回 flag 的全部取值。
func flagValues(args []string, flag string) []string {
	var out []string
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			out = append(out, args[i+1])
		} else if v, ok := strings.CutPrefix(a, flag+"="); ok {
			out = append(out, v)
		}
	}
	return out
}

// newFakeDocker returns a fake docker CLI that answers the daemon probe and a successful build. | newFakeDocker 返回一个回答守护进程探活与成功构建的假 docker CLI。
func newFakeDocker() *fakeExec {
	return &fakeExec{probeStdout: probeOutput, buildStdout: buildOutput}
}

// testLogger returns a Logger that discards output, keeping test logs clean. | testLogger 返回丢弃输出的 Logger，保持测试输出干净。
func testLogger() *logx.Logger {
	return logx.New(logx.Options{Writer: io.Discard, Level: logx.DebugLevel})
}

// ---------------------------------------------------------------- Builtin template | 内置模板

// TestBuiltinDockerfileUsesScratchAndMultiStage pins the embedded template: a
// golang:1.27-alpine builder stage producing a CGO-free static binary that is copied
// into a scratch runtime stage together with CA certificates and the zone database. | TestBuiltinDockerfileUsesScratchAndMultiStage
// 固定内置模板：golang:1.27-alpine 构建阶段产出无 CGO 的静态二进制，与根证书、时区库一并复制进 scratch 运行阶段。
func TestBuiltinDockerfileUsesScratchAndMultiStage(t *testing.T) {
	data, err := BuiltinDockerfile(BuiltinDockerfileFS())
	if err != nil {
		t.Fatalf("读取内置 Dockerfile 失败: %v", err)
	}
	body := string(data)

	for _, want := range []string{
		"FROM golang:1.27-alpine AS builder",
		"CGO_ENABLED=0",
		"FROM scratch",
		"/etc/ssl/certs/ca-certificates.crt",
		"/usr/share/zoneinfo",
		`ENTRYPOINT ["/app"]`,
		"COPY --from=builder /out/app /app",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("内置 Dockerfile 缺少 %q:\n%s", want, body)
		}
	}

	builder := strings.Index(body, "AS builder")
	scratch := strings.Index(body, "FROM scratch")
	if builder < 0 || scratch < 0 || builder > scratch {
		t.Fatalf("内置 Dockerfile 必须为多阶段且 builder 在前、scratch 在后:\n%s", body)
	}
	if n := strings.Count(body, "FROM "); n != 2 {
		t.Errorf("FROM 指令数量 = %d, 期望 2（多阶段）:\n%s", n, body)
	}
	if strings.Contains(body, "FROM alpine") || strings.Contains(body, "FROM golang:1.27-alpine\n") {
		t.Errorf("运行时阶段必须基于 scratch:\n%s", body)
	}
}

// TestBuiltinDockerfileNilFSFallsBackToEmbed asserts a nil filesystem reads the embedded template. | TestBuiltinDockerfileNilFSFallsBackToEmbed 断言文件系统为 nil 时读取内置模板。
func TestBuiltinDockerfileNilFSFallsBackToEmbed(t *testing.T) {
	data, err := BuiltinDockerfile(nil)
	if err != nil {
		t.Fatalf("读取内置 Dockerfile 失败: %v", err)
	}
	if !strings.Contains(string(data), "FROM scratch") {
		t.Fatal("nil 文件系统未回退到内置模板")
	}
}

// ---------------------------------------------------------------- Dockerfile resolution | Dockerfile 解析

// TestProjectDockerfilePreferredOverBuiltin asserts a project Dockerfile shadows the
// builtin one, and that its content is what docker actually receives through -f. | TestProjectDockerfilePreferredOverBuiltin
// 断言项目 Dockerfile 覆盖内置模板，且 docker 通过 -f 实际收到的是项目文件内容。
func TestProjectDockerfilePreferredOverBuiltin(t *testing.T) {
	const projectDockerfile = "FROM project-base\nENTRYPOINT [\"/custom\"]\n"

	dir := t.TempDir()
	mem := afs.NewMem()
	if err := afero.WriteFile(mem, filepath.Join(dir, "Dockerfile"), []byte(projectDockerfile), 0o644); err != nil {
		t.Fatalf("写入内存 Dockerfile 失败: %v", err)
	}

	resolved, err := ResolveDockerfile(mem, dir, "")
	if err != nil {
		t.Fatalf("ResolveDockerfile 失败: %v", err)
	}
	defer resolved.Cleanup()

	if !resolved.FromProject {
		t.Error("ResolveDockerfile 未标记来自项目文件")
	}
	if string(resolved.Content) != projectDockerfile {
		t.Errorf("解析内容 = %q, 期望项目文件内容", resolved.Content)
	}
	onDisk, err := os.ReadFile(resolved.Path)
	if err != nil {
		t.Fatalf("读取落盘 Dockerfile 失败: %v", err)
	}
	if string(onDisk) != projectDockerfile {
		t.Errorf("落盘内容 = %q, 期望项目文件内容", onDisk)
	}

	fake := newFakeDocker()
	if _, err := BuildImage(context.Background(), Options{Dir: dir, Tag: "app:v1", ExecCommand: fake.exec}, mem, testLogger()); err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	if len(fake.dockerfileSnapshots) != 1 {
		t.Fatalf("docker -f 内容快照数量 = %d, 期望 1", len(fake.dockerfileSnapshots))
	}
	if fake.dockerfileSnapshots[0] != projectDockerfile {
		t.Errorf("docker 收到的 Dockerfile = %q, 期望项目文件内容", fake.dockerfileSnapshots[0])
	}
}

// TestMissingProjectDockerfileFallsBackToBuiltin asserts the embedded template is used otherwise. | TestMissingProjectDockerfileFallsBackToBuiltin 断言项目无 Dockerfile 时回退到内置模板。
func TestMissingProjectDockerfileFallsBackToBuiltin(t *testing.T) {
	resolved, err := ResolveDockerfile(afs.NewMem(), ".", "")
	if err != nil {
		t.Fatalf("ResolveDockerfile 失败: %v", err)
	}
	defer resolved.Cleanup()

	if resolved.FromProject {
		t.Error("空文件系统不应报告来自项目文件")
	}
	if !strings.Contains(string(resolved.Content), "FROM scratch") {
		t.Error("回退内容不是内置 scratch 模板")
	}
	if !resolved.Temporary {
		t.Error("内置模板必须落盘到临时文件供 docker -f 读取")
	}
}

// TestExplicitDockerfilePathHonored asserts an explicit --file wins over the default name. | TestExplicitDockerfilePathHonored 断言显式 --file 优先于默认文件名。
func TestExplicitDockerfilePathHonored(t *testing.T) {
	mem := afs.NewMem()
	if err := afero.WriteFile(mem, "deploy/Containerfile", []byte("FROM custom\n"), 0o644); err != nil {
		t.Fatalf("写入内存 Containerfile 失败: %v", err)
	}
	resolved, err := ResolveDockerfile(mem, ".", filepath.Join("deploy", "Containerfile"))
	if err != nil {
		t.Fatalf("ResolveDockerfile 失败: %v", err)
	}
	defer resolved.Cleanup()
	if !resolved.FromProject || string(resolved.Content) != "FROM custom\n" {
		t.Fatalf("解析结果 = %+v, 期望使用 deploy/Containerfile", resolved)
	}
}

// ---------------------------------------------------------------- Argument construction | 参数构造

// TestBuildImageArgsConstructed asserts the exact argv handed to the docker CLI. | TestBuildImageArgsConstructed 断言交给 docker CLI 的精确 argv。
func TestBuildImageArgsConstructed(t *testing.T) {
	dir := t.TempDir()
	fake := newFakeDocker()

	result, err := BuildImage(context.Background(), Options{
		Dir:         dir,
		Tag:         "app:v1",
		Platform:    "linux/amd64",
		NoCache:     true,
		BuildArgs:   map[string]string{"VERSION": "1.0.0", "GIT_COMMIT": "abcdef0"},
		Target:      "runtime",
		DockerBin:   "docker",
		ExecCommand: fake.exec,
	}, afs.NewMem(), testLogger())
	if err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}

	if got := fake.verbs(); !slices.Equal(got, []string{"version", "build"}) {
		t.Fatalf("调用序列 = %v, 期望 [version build]（先探活再构建）", got)
	}
	probe := fake.calls[0]
	if probe[0] != "docker" {
		t.Errorf("探活命令 = %q, 期望 docker", probe[0])
	}
	if !strings.Contains(strings.Join(probe, " "), "--format") {
		t.Errorf("探活 argv = %v, 期望带 --format", probe)
	}

	args := fake.buildArgs(t)
	for _, want := range []string{"-t", "--platform", "--no-cache", "--build-arg", "--target", "-f"} {
		if !slices.Contains(args, want) {
			t.Errorf("argv = %v, 缺少 %q", args, want)
		}
	}
	if got, _ := flagValue(args, "-t"); got != "app:v1" {
		t.Errorf("-t = %q, 期望 app:v1", got)
	}
	if got, _ := flagValue(args, "--platform"); got != "linux/amd64" {
		t.Errorf("--platform = %q, 期望 linux/amd64", got)
	}
	if got, _ := flagValue(args, "--target"); got != "runtime" {
		t.Errorf("--target = %q, 期望 runtime", got)
	}
	buildArgs := flagValues(args, "--build-arg")
	wantBuildArgs := []string{"GIT_COMMIT=abcdef0", "VERSION=1.0.0"}
	if !slices.Equal(buildArgs, wantBuildArgs) {
		t.Errorf("--build-arg = %v, 期望按键排序的 %v", buildArgs, wantBuildArgs)
	}
	if got := args[len(args)-1]; got != dir {
		t.Errorf("构建上下文 = %q, 期望 %q", got, dir)
	}
	if result.ImageID != "sha256:1111111111111111111111111111111111111111111111111111111111111111" {
		t.Errorf("ImageID = %q, 期望解析出的 sha256 摘要", result.ImageID)
	}
	if result.Tag != "app:v1" {
		t.Errorf("Tag = %q, 期望 app:v1", result.Tag)
	}
	if len(result.Logs) == 0 {
		t.Error("Logs 为空, 期望保留 docker 输出行")
	}
}

// TestBuildImageOmitsOptionalFlags asserts empty options produce a minimal argv. | TestBuildImageOmitsOptionalFlags 断言空选项产生最小 argv。
func TestBuildImageOmitsOptionalFlags(t *testing.T) {
	dir := t.TempDir()
	fake := newFakeDocker()
	if _, err := BuildImage(context.Background(), Options{Dir: dir, ExecCommand: fake.exec}, afs.NewMem(), testLogger()); err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	args := fake.buildArgs(t)
	for _, unwanted := range []string{"-t", "--platform", "--no-cache", "--build-arg", "--target", "--push"} {
		if slices.Contains(args, unwanted) {
			t.Errorf("argv = %v, 不应包含 %q", args, unwanted)
		}
	}
	if _, ok := flagValue(args, "-f"); !ok {
		t.Errorf("argv = %v, 缺少 -f", args)
	}
}

// TestBuildImagePushFlag asserts --push is forwarded. | TestBuildImagePushFlag 断言 --push 被透传。
func TestBuildImagePushFlag(t *testing.T) {
	fake := newFakeDocker()
	if _, err := BuildImage(context.Background(), Options{
		Dir: t.TempDir(), Tag: "registry.example.com/app:v1", Push: true, ExecCommand: fake.exec,
	}, afs.NewMem(), testLogger()); err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	if args := fake.buildArgs(t); !slices.Contains(args, "--push") {
		t.Errorf("argv = %v, 缺少 --push", args)
	}
}

// TestBuildImageDockerBinOverride asserts the configured binary name is used. | TestBuildImageDockerBinOverride 断言使用配置的二进制名。
func TestBuildImageDockerBinOverride(t *testing.T) {
	fake := newFakeDocker()
	if _, err := BuildImage(context.Background(), Options{
		Dir: t.TempDir(), DockerBin: "podman", ExecCommand: fake.exec,
	}, afs.NewMem(), testLogger()); err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	for _, argv := range fake.calls {
		if argv[0] != "podman" {
			t.Fatalf("命令 = %q, 期望 podman", argv[0])
		}
	}
}

// ---------------------------------------------------------------- Failure paths | 失败路径

// TestDaemonUnavailableReturnsTypedError asserts a failing probe short-circuits the build. | TestDaemonUnavailableReturnsTypedError 断言探活失败会短路构建。
func TestDaemonUnavailableReturnsTypedError(t *testing.T) {
	fake := &fakeExec{probeStdout: daemonDownOutput, probeExit: 1}
	_, err := BuildImage(context.Background(), Options{
		Dir: t.TempDir(), Tag: "app:v1", ExecCommand: fake.exec,
	}, afs.NewMem(), testLogger())
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("错误 = %v, 期望 errors.Is(err, ErrDaemonUnavailable)", err)
	}
	if got := fake.verbs(); !slices.Equal(got, []string{"version"}) {
		t.Fatalf("调用序列 = %v, 守护进程不可用时不应执行 docker build", got)
	}
}

// TestBuildOutputDaemonDownMapsToSentinel asserts a daemon outage surfacing during the
// build itself is still typed as ErrDaemonUnavailable. | TestBuildOutputDaemonDownMapsToSentinel
// 断言构建过程中暴露的守护进程故障同样归类为 ErrDaemonUnavailable。
func TestBuildOutputDaemonDownMapsToSentinel(t *testing.T) {
	fake := newFakeDocker()
	fake.buildStdout, fake.buildExit = daemonDownOutput, 1
	_, err := BuildImage(context.Background(), Options{
		Dir: t.TempDir(), Tag: "app:v1", ExecCommand: fake.exec,
	}, afs.NewMem(), testLogger())
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("错误 = %v, 期望 errors.Is(err, ErrDaemonUnavailable)", err)
	}
}

// TestBuildFailureReturnsErrBuildFailed asserts a plain failure keeps the docker log. | TestBuildFailureReturnsErrBuildFailed 断言普通失败会保留 docker 日志。
func TestBuildFailureReturnsErrBuildFailed(t *testing.T) {
	fake := newFakeDocker()
	fake.buildStdout, fake.buildExit = "#5 ERROR: failed to solve: process \"/bin/sh -c go build\" did not complete\n", 1

	result, err := BuildImage(context.Background(), Options{
		Dir: t.TempDir(), Tag: "app:v1", ExecCommand: fake.exec,
	}, afs.NewMem(), testLogger())
	if !errors.Is(err, ErrBuildFailed) {
		t.Fatalf("错误 = %v, 期望 errors.Is(err, ErrBuildFailed)", err)
	}
	if result == nil || len(result.Logs) == 0 {
		t.Fatal("失败时仍应返回带日志的 Result")
	}
	if !strings.Contains(strings.Join(result.Logs, "\n"), "failed to solve") {
		t.Errorf("Logs = %v, 期望包含 docker 输出", result.Logs)
	}
}

// TestBuildImageStartErrorIsWrapped asserts an unlaunchable binary is reported as a build failure. | TestBuildImageStartErrorIsWrapped 断言无法启动的二进制被归类为构建失败。
func TestBuildImageStartErrorIsWrapped(t *testing.T) {
	fake := newFakeDocker()
	fake.probeStdout, fake.probeExit = "ok\n", 0
	_, err := BuildImage(context.Background(), Options{
		Dir:       t.TempDir(),
		DockerBin: filepath.Join(t.TempDir(), "definitely-missing-docker"),
		ExecCommand: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, name, args...)
		},
	}, afs.NewMem(), testLogger())
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("错误 = %v, 期望 errors.Is(err, ErrDaemonUnavailable)", err)
	}
}

// ---------------------------------------------------------------- Assets hook | 资产钩子

// TestBuildImagePrepareAssetsHook asserts the asset hook runs before docker build and
// that its failure aborts the image build. | TestBuildImagePrepareAssetsHook 断言资产钩子在
// docker build 之前执行，且其失败会中止镜像构建。
func TestBuildImagePrepareAssetsHook(t *testing.T) {
	var (
		hookCalls int
		hookDir   string
		order     []string
	)
	fake := newFakeDocker()
	fake.onBuild = func([]string) { order = append(order, "build") }

	_, err := BuildImage(context.Background(), Options{
		Dir:        t.TempDir(),
		Tag:        "app:v1",
		WithAssets: true,
		PrepareAssets: func(_ context.Context, dir string, _ *logx.Logger) error {
			hookCalls++
			hookDir = dir
			order = append(order, "assets")
			return nil
		},
		ExecCommand: fake.exec,
	}, afs.NewMem(), testLogger())
	if err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	if hookCalls != 1 {
		t.Fatalf("资产钩子调用次数 = %d, 期望 1", hookCalls)
	}
	if hookDir == "" {
		t.Error("资产钩子未收到构建目录")
	}
	if !slices.Equal(order, []string{"assets", "build"}) {
		t.Errorf("执行顺序 = %v, 期望先资产后构建", order)
	}

	hookErr := errors.New("asset pipeline failed")
	_, err = BuildImage(context.Background(), Options{
		Dir:           t.TempDir(),
		WithAssets:    true,
		PrepareAssets: func(context.Context, string, *logx.Logger) error { return hookErr },
		ExecCommand:   fake.exec,
	}, afs.NewMem(), testLogger())
	if !errors.Is(err, hookErr) {
		t.Fatalf("错误 = %v, 期望资产钩子错误原样返回", err)
	}
}

// TestBuildImageWithoutAssetsSkipsPreparation asserts the default skips the asset stage. | TestBuildImageWithoutAssetsSkipsPreparation 断言默认跳过资产准备阶段。
func TestBuildImageWithoutAssetsSkipsPreparation(t *testing.T) {
	called := false
	_, err := BuildImage(context.Background(), Options{
		Dir: t.TempDir(),
		PrepareAssets: func(context.Context, string, *logx.Logger) error {
			called = true
			return nil
		},
		ExecCommand: newFakeDocker().exec,
	}, afs.NewMem(), testLogger())
	if err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	if called {
		t.Error("WithAssets 关闭时不应调用资产钩子")
	}
}

// TestBuildImageDefaultAssetsToleratesMissingTailwind asserts the built-in asset stage
// degrades gracefully when the project has no Tailwind stylesheet. | TestBuildImageDefaultAssetsToleratesMissingTailwind
// 断言项目没有 Tailwind 样式表时内置资产阶段优雅降级。
func TestBuildImageDefaultAssetsToleratesMissingTailwind(t *testing.T) {
	fake := newFakeDocker()
	if _, err := BuildImage(context.Background(), Options{
		Dir: t.TempDir(), Tag: "app:v1", WithAssets: true, ExecCommand: fake.exec,
	}, afs.NewMem(), testLogger()); err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	if got := fake.verbs(); !slices.Equal(got, []string{"version", "build"}) {
		t.Fatalf("调用序列 = %v, 期望镜像构建照常进行", got)
	}
}

// ---------------------------------------------------------------- Output parsing | 输出解析

// TestParseImageID covers the output shapes of both the classic and the BuildKit builder. | TestParseImageID 覆盖经典构建器与 BuildKit 两种输出形态。
func TestParseImageID(t *testing.T) {
	const digest = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	cases := []struct {
		name string
		logs []string
		want string
	}{
		{
			name: "buildkit writing image",
			logs: []string{"#9 exporting layers done", "#9 writing image " + digest + " done"},
			want: digest,
		},
		{
			name: "classic successfully built",
			logs: []string{"Successfully built 111111111111", "Successfully tagged app:v1"},
			want: "111111111111",
		},
		{
			name: "unknown output",
			logs: []string{"#1 [internal] load build definition", "DONE 0.1s"},
			want: "",
		},
		{
			name: "short sha is rejected",
			logs: []string{"writing image sha256:deadbeef done"},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseImageID(tc.logs); got != tc.want {
				t.Fatalf("ParseImageID = %q, 期望 %q", got, tc.want)
			}
		})
	}
}

// TestBuildImageResolvesDockerfileOnRealFilesystem asserts a real project Dockerfile
// is passed to docker by path, without copying it to a temp file. | TestBuildImageResolvesDockerfileOnRealFilesystem
// 断言真实存在的项目 Dockerfile 直接以路径交给 docker，不复制到临时文件。
func TestBuildImageResolvesDockerfileOnRealFilesystem(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(path, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("写入 Dockerfile 失败: %v", err)
	}

	fake := newFakeDocker()
	if _, err := BuildImage(context.Background(), Options{
		Dir: dir, Tag: "app:v1", ExecCommand: fake.exec,
	}, afs.NewOS(""), testLogger()); err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	args := fake.buildArgs(t)
	got, ok := flagValue(args, "-f")
	if !ok {
		t.Fatalf("argv = %v, 缺少 -f", args)
	}
	if got != path {
		t.Errorf("-f = %q, 期望项目内真实路径 %q", got, path)
	}
}
