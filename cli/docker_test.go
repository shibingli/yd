// This file tests the docker command: registration, help output, flag validation, the default
// image tag and the asset stage switch. Every test injects a fake docker CLI, so no test ever
// contacts a docker daemon. | 本文件测试 docker 命令：注册、帮助输出、标志校验、缺省镜像标签与资产阶段开关。
// 每个测试都注入假 docker CLI，因此任何测试都不会接触真实 docker 守护进程。

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/spf13/pflag"
	"github.com/yun-dev/yd/docker"
	"github.com/yun-dev/yd/logx"
	"github.com/yun-dev/yd/version"
)

// cliDockerImageID is the image id carried by the canned build output: the sha256 prefix plus 64
// lowercase hex digits, which is exactly the shape docker.ParseImageID accepts. | cliDockerImageID
// 是预置构建输出携带的镜像 ID：sha256 前缀加 64 位小写十六进制数字，正是 docker.ParseImageID 接受的形态。
var cliDockerImageID = "sha256:" + strings.Repeat("1", 64)

// cliDockerBuildOutput is the canned stdout of a successful `docker build`, shaped like the real
// builder output, including the image id and the reported size. | cliDockerBuildOutput 是一次成功
// `docker build` 的预置标准输出，形态与真实构建器输出一致，含镜像 ID 与报告的体积。
var cliDockerBuildOutput = strings.Join([]string{
	"#1 [internal] load build definition from Dockerfile",
	"#9 writing image " + cliDockerImageID + " done",
	"#9 naming to docker.io/library/yd:dev done",
	"Successfully built 111111111111",
	"Successfully tagged yd:dev",
	"image size: 12.3MB",
}, "\n") + "\n"

// cliDockerLogLines is the number of log lines cliDockerBuildOutput yields. | cliDockerLogLines 是 cliDockerBuildOutput 产出的日志行数。
const cliDockerLogLines = 6

// cliFakeDocker mimics the docker CLI: `version --format` answers the daemon probe and every other
// invocation is answered with a successful build log. | cliFakeDocker 模拟 docker CLI：`version --format`
// 应答守护进程探活，其余调用一律以成功的构建日志应答。
func cliFakeDocker() *cliFakeExec {
	return &cliFakeExec{
		handler: func(_ string, args []string) (string, int) {
			if len(args) > 0 && args[0] == "version" {
				return "27.0.0\n", 0
			}
			return cliDockerBuildOutput, 0
		},
	}
}

// useDockerExec injects a fake docker CLI into the docker command for the duration of one test. | useDockerExec 在单个测试期间为 docker 命令注入假 docker CLI。
func useDockerExec(t *testing.T, f *cliFakeExec) {
	t.Helper()
	old := dockerExecCommand
	dockerExecCommand = f.exec
	t.Cleanup(func() { dockerExecCommand = old })
}

// useDockerAssets replaces the asset stage with a recorder and returns the call counter, which is
// how --no-assets is observed without running tailwindcss. | useDockerAssets 用记录器替换资产阶段并返回
// 调用计数器，从而在不运行 tailwindcss 的前提下观察 --no-assets。
func useDockerAssets(t *testing.T) *int {
	t.Helper()
	calls := new(int)
	old := dockerPrepareAssets
	dockerPrepareAssets = func(context.Context, string, *logx.Logger) error {
		*calls = *calls + 1
		return nil
	}
	t.Cleanup(func() { dockerPrepareAssets = old })
	return calls
}

// dockerParseFlags parses args with a fresh docker flag set. | dockerParseFlags 用全新的 docker 标志集解析 args。
func dockerParseFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet(dockerCommandName, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerDockerFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("解析 docker 标志失败: %v", err)
	}
	return fs
}

// dockerBuildArgv returns the single `docker build` invocation, failing when there is not exactly
// one. | dockerBuildArgv 返回唯一一次 `docker build` 调用，次数不为 1 时终止测试。
func dockerBuildArgv(t *testing.T, f *cliFakeExec) []string {
	t.Helper()
	builds := f.invocations("build")
	if len(builds) != 1 {
		t.Fatalf("docker build 调用次数 = %d，期望 1；全部调用: %v", len(builds), f.calls)
	}
	return builds[0]
}

// dockerWriteFile writes a file on disk, creating its parent directory. | dockerWriteFile 在磁盘上写文件并创建其父目录。
func dockerWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
}

// newDockerProject returns a real working directory plus an in-memory file system carrying
// optional project files, which is what the docker command reads the Dockerfile and .yd.json from. |
// newDockerProject 返回真实的工作目录，以及携带可选项目文件的内存文件系统，docker 命令正是从其中读取
// Dockerfile 与 .yd.json。
func newDockerProject(t *testing.T, files map[string]string) (string, afero.Fs) {
	t.Helper()
	dir := t.TempDir()
	fs := afero.NewMemMapFs()
	for name, body := range files {
		if err := afero.WriteFile(fs, filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("写入 %s 失败: %v", name, err)
		}
	}
	return dir, fs
}

// ---------------------------------------------------------------- Tests | 测试

// TestDockerCommandRegistered docker 只注册一次且标志齐全，Register 对重名会 panic。 | docker must stay registered exactly once with every flag: Register panics on duplicates.
func TestDockerCommandRegistered(t *testing.T) {
	count := 0
	for _, c := range Commands() {
		if c.Name != dockerCommandName {
			continue
		}
		count++
		if c.Run == nil || c.Flags == nil {
			t.Error("docker 命令缺少 Run 或 Flags")
		}
		if c.Usage != dockerUsageLine {
			t.Errorf("Usage = %q，期望 %q", c.Usage, dockerUsageLine)
		}
	}
	if count != 1 {
		t.Fatalf("docker 命令注册了 %d 次，期望 1 次", count)
	}

	fs := pflag.NewFlagSet(dockerCommandName, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerDockerFlags(fs)
	for _, name := range []string{
		flagDockerTag, flagDockerPlatform, flagDockerNoCache, flagDockerFile,
		flagDockerTarget, flagDockerPush, flagDockerBin, flagDockerNoAssets,
	} {
		if fs.Lookup(name) == nil {
			t.Errorf("docker 命令缺少标志 --%s", name)
		}
	}
}

// TestDockerCommandHelp docker --help 必须列出全部标志并且不执行构建。 | docker --help must list every flag without building anything.
func TestDockerCommandHelp(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"docker", "--help"}, te.env); err != nil {
		t.Fatalf("docker --help 不应返回错误: %v", err)
	}
	out := te.out.String()
	for _, want := range []string{
		"yd docker", "Usage",
		"--" + flagDockerTag, "--" + flagDockerPlatform, "--" + flagDockerNoCache,
		"--" + flagDockerFile, "--" + flagDockerTarget, "--" + flagDockerPush,
		"--" + flagDockerBin, "--" + flagDockerNoAssets,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("docker 帮助缺少 %q：\n%s", want, out)
		}
	}
}

// TestDockerUnknownFlagErrors 未知标志必须包装为 ErrFlagParse。 | An unknown flag must be wrapped into ErrFlagParse.
func TestDockerUnknownFlagErrors(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"docker", "--nope"}, te.env); !errors.Is(err, ErrFlagParse) {
		t.Fatalf("未知标志应返回 ErrFlagParse，实际 %v", err)
	}
}

// TestDockerDefaultTagFromMeta .yd.json 记录项目名时，缺省标签为 <项目名>:<版本>，且探活在构建之前。 | With a project name in .yd.json the default tag is <project>:<version>, and the daemon probe precedes the build.
func TestDockerDefaultTagFromMeta(t *testing.T) {
	fake := cliFakeDocker()
	useDockerExec(t, fake)
	useDockerAssets(t)

	dir, fs := newDockerProject(t, map[string]string{
		dockerProjectMetaName: `{"name": "shop-admin", "module": "example.com/shop"}`,
	})
	te := newTestEnv(t, fs, "", false)
	te.env.WorkDir = dir
	if err := Execute(context.Background(), []string{"docker"}, te.env); err != nil {
		t.Fatalf("yd docker 失败: %v", err)
	}

	argv := dockerBuildArgv(t, fake)
	want := "shop-admin:" + version.Version
	if got, _ := argValue(argv, "-t"); got != want {
		t.Errorf("缺省标签 = %q，期望 %q", got, want)
	}
	if len(fake.calls) != 2 || fake.calls[0][1] != "version" || fake.calls[1][1] != "build" {
		t.Errorf("调用顺序 = %v，期望先探活再构建", fake.calls)
	}
}

// TestDockerDefaultTagFromDirName .yd.json 缺失、损坏或未记录名字时，缺省标签回退为 <目录名>:<版本>。 | A missing, broken or nameless .yd.json falls back to the <directory>:<version> tag.
func TestDockerDefaultTagFromDirName(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"no metadata | 无元信息", nil},
		{"broken metadata | 元信息损坏", map[string]string{dockerProjectMetaName: "{not json"}},
		{"nameless metadata | 未记录名字", map[string]string{dockerProjectMetaName: `{"module": "example.com/shop"}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := cliFakeDocker()
			useDockerExec(t, fake)
			useDockerAssets(t)

			dir, fs := newDockerProject(t, tc.files)
			te := newTestEnv(t, fs, "", false)
			te.env.WorkDir = dir
			if err := Execute(context.Background(), []string{"docker"}, te.env); err != nil {
				t.Fatalf("yd docker 失败: %v", err)
			}

			want := filepath.Base(dir) + ":" + version.Version
			if got, _ := argValue(dockerBuildArgv(t, fake), "-t"); got != want {
				t.Errorf("缺省标签 = %q，期望 %q", got, want)
			}
		})
	}
}

// TestDockerTagFlagWins --tag 原样覆盖缺省标签。 | --tag overrides the default tag verbatim.
func TestDockerTagFlagWins(t *testing.T) {
	fake := cliFakeDocker()
	useDockerExec(t, fake)
	useDockerAssets(t)

	dir, fs := newDockerProject(t, map[string]string{
		dockerProjectMetaName: `{"name": "shop-admin"}`,
	})
	te := newTestEnv(t, fs, "", false)
	te.env.WorkDir = dir
	const tag = "registry.example.com/team/shop-admin:v2.1.0"
	if err := Execute(context.Background(), []string{"docker", "--tag", tag}, te.env); err != nil {
		t.Fatalf("yd docker --tag 失败: %v", err)
	}
	if got, _ := argValue(dockerBuildArgv(t, fake), "-t"); got != tag {
		t.Errorf("--tag 取值 = %q，期望 %q", got, tag)
	}
}

// TestDockerNoAssetsMapsToWithAssets --no-assets 必须映射为 WithAssets=false。 | --no-assets must map onto WithAssets=false.
func TestDockerNoAssetsMapsToWithAssets(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"default refreshes assets | 默认刷新资产", nil, true},
		{"no-assets skips them | 关闭资产刷新", []string{"--no-assets"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := dockerOptions(&Env{WorkDir: "."}, dockerParseFlags(t, tc.args...))
			if o.WithAssets != tc.want {
				t.Errorf("WithAssets = %v，期望 %v", o.WithAssets, tc.want)
			}
		})
	}
}

// TestDockerAssetsStageRunsUnlessDisabled 资产阶段默认执行，--no-assets 时不执行。 | The asset stage runs by default and never under --no-assets.
func TestDockerAssetsStageRunsUnlessDisabled(t *testing.T) {
	fake := cliFakeDocker()
	useDockerExec(t, fake)
	calls := useDockerAssets(t)

	dir, fs := newDockerProject(t, nil)
	te := newTestEnv(t, fs, "", false)
	te.env.WorkDir = dir
	if err := Execute(context.Background(), []string{"docker"}, te.env); err != nil {
		t.Fatalf("yd docker 失败: %v", err)
	}
	if *calls != 1 {
		t.Errorf("资产阶段调用次数 = %d，期望 1", *calls)
	}

	dir, fs = newDockerProject(t, nil)
	te = newTestEnv(t, fs, "", false)
	te.env.WorkDir = dir
	if err := Execute(context.Background(), []string{"docker", "--no-assets"}, te.env); err != nil {
		t.Fatalf("yd docker --no-assets 失败: %v", err)
	}
	if *calls != 1 {
		t.Errorf("--no-assets 之后资产阶段又执行了 %d 次，期望 0 次", *calls-1)
	}
}

// TestDockerForwardsBuildFlags 平台、缓存、Dockerfile、阶段、推送与 docker CLI 必须原样出现在 argv 中。 | Platform, cache, Dockerfile, stage, push and the docker CLI must all reach the argv verbatim.
func TestDockerForwardsBuildFlags(t *testing.T) {
	fake := cliFakeDocker()
	useDockerExec(t, fake)

	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "deploy", "Dockerfile")
	dockerWriteFile(t, dockerfile, "FROM scratch\n")

	te := newTestEnv(t, afero.NewOsFs(), "", false)
	te.env.WorkDir = dir
	err := Execute(context.Background(), []string{
		"docker", "--no-assets", "--platform", "linux/arm64", "--no-cache",
		"--file", filepath.Join("deploy", "Dockerfile"), "--target", "runtime",
		"--push", "--docker-bin", "podman",
	}, te.env)
	if err != nil {
		t.Fatalf("yd docker 失败: %v", err)
	}

	if len(fake.calls) != 2 || fake.calls[0][0] != "podman" || fake.calls[1][0] != "podman" {
		t.Fatalf("docker CLI = %v，期望全部使用 podman", fake.calls)
	}
	argv := dockerBuildArgv(t, fake)
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"--platform linux/arm64",
		"--no-cache",
		"--target runtime",
		"--push",
		"-f " + dockerfile,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("docker build 参数缺少 %q：\n%s", want, joined)
		}
	}
	if argv[len(argv)-1] != dir {
		t.Errorf("argv 末尾（构建上下文）= %q，期望 %q", argv[len(argv)-1], dir)
	}
}

// TestDockerUsesBuiltinDockerfileWithoutProjectFile 项目没有 Dockerfile 时使用内置模板而不是失败。 | A project without a Dockerfile falls back to the built-in template instead of failing.
func TestDockerUsesBuiltinDockerfileWithoutProjectFile(t *testing.T) {
	fake := cliFakeDocker()
	// The built-in template is materialized to a temporary file that is removed as soon as the
	// build returns, so its existence is observed while `docker build` is being constructed. | 内置模板
	// 会被物化到临时文件，并在构建返回后立即删除，因此存在性在构造 `docker build` 时观察。
	var (
		seenPath   string
		seenExists bool
	)
	fake.onCall = func(_ string, args []string) {
		if path, ok := argValue(args, "-f"); ok {
			seenPath = path
			_, err := os.Stat(path)
			seenExists = err == nil
		}
	}
	useDockerExec(t, fake)
	useDockerAssets(t)

	dir, fs := newDockerProject(t, nil)
	te := newTestEnv(t, fs, "", false)
	te.env.WorkDir = dir
	if err := Execute(context.Background(), []string{"docker"}, te.env); err != nil {
		t.Fatalf("缺少 Dockerfile 时 yd docker 不应失败: %v", err)
	}

	if seenPath == "" {
		t.Fatal("docker build 缺少 -f：内置模板未被物化")
	}
	if !seenExists {
		t.Errorf("docker build 执行时内置模板 %s 不存在", seenPath)
	}
}

// TestDockerReportsResult 构建结果必须依次报告镜像引用、镜像 ID 与日志行数。 | The report must show the image reference, the parsed image id and the log line count.
func TestDockerReportsResult(t *testing.T) {
	fake := cliFakeDocker()
	useDockerExec(t, fake)
	useDockerAssets(t)

	dir, fs := newDockerProject(t, map[string]string{
		dockerProjectMetaName: `{"name": "shop-admin"}`,
	})
	te := newTestEnv(t, fs, "", false)
	te.env.WorkDir = dir
	if err := Execute(context.Background(), []string{"docker"}, te.env); err != nil {
		t.Fatalf("yd docker 失败: %v", err)
	}

	out := te.out.String()
	for _, want := range []string{
		"image | 镜像: shop-admin:" + version.Version,
		"image id | 镜像 ID: " + cliDockerImageID,
		"log lines | 日志行数: " + strconv.Itoa(cliDockerLogLines),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("docker 报告缺少 %q：\n%s", want, out)
		}
	}
}

// TestDockerDaemonUnavailablePropagates 守护进程探活失败时返回 docker.ErrDaemonUnavailable 且不打印结果。 | A failing daemon probe returns docker.ErrDaemonUnavailable and prints nothing.
func TestDockerDaemonUnavailablePropagates(t *testing.T) {
	fake := cliFakeDocker()
	fake.handler = func(_ string, args []string) (string, int) {
		if len(args) > 0 && args[0] == "version" {
			return "Cannot connect to the Docker daemon at unix:///var/run/docker.sock\n", 1
		}
		return cliDockerBuildOutput, 0
	}
	useDockerExec(t, fake)
	useDockerAssets(t)

	dir, fs := newDockerProject(t, nil)
	te := newTestEnv(t, fs, "", false)
	te.env.WorkDir = dir
	err := Execute(context.Background(), []string{"docker"}, te.env)
	if !errors.Is(err, docker.ErrDaemonUnavailable) {
		t.Fatalf("err = %v，期望 docker.ErrDaemonUnavailable", err)
	}
	if out := te.out.String(); out != "" {
		t.Errorf("守护进程不可用时不应报告结果：\n%s", out)
	}
	if got := len(fake.invocations("build")); got != 0 {
		t.Errorf("守护进程不可用时不应构建，实际构建 %d 次", got)
	}
}
