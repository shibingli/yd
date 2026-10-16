// This file tests the whole build flow of the ko package with an injected runner: no test ever
// starts a real ko, contacts a registry or touches the network. | 本文件用注入的执行器测试 ko 包的完整构建流程：
// 任何测试都不会启动真实 ko、接触镜像仓库或网络。

package ko

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// koRun records one invocation of the injected runner. | koRun 记录一次注入执行器的调用。
type koRun struct {
	name string
	args []string
	dir  string
	env  []string
}

// stubExec returns a runner that records its invocation and replays one canned result. | stubExec
// 返回一个记录调用并回放固定结果的执行器。
func stubExec(out string, err error, runs *[]koRun) ExecCommandFunc {
	return func(_ context.Context, name string, args []string, dir string, env []string) ([]byte, error) {
		if runs != nil {
			*runs = append(*runs, koRun{name: name, args: args, dir: dir, env: env})
		}
		return []byte(out), err
	}
}

// stubResolvedKo installs a PATH lookup that resolves the bare ko name and clears YD_KO_BIN, so the
// resolution chain cannot pick up anything from the developer's machine. | stubResolvedKo 安装一个能解析
// 裸 ko 名字的 PATH 查找函数并清空 YD_KO_BIN，使解析链不会从开发机上取到任何东西。
func stubResolvedKo(t *testing.T) {
	t.Helper()
	t.Setenv(EnvBinary, "")
	usePathLookup(t, stubLookup(map[string]string{DefaultBinary: "/resolved/ko"}))
}

// envEntries counts how often key appears in env and returns its value. | envEntries 统计 key 在 env 中出现的次数并返回其取值。
func envEntries(env []string, key string) (string, int) {
	value, count := "", 0
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			value, count = v, count+1
		}
	}
	return value, count
}

// TestBuildImageSuccess covers the happy path: the argv, the working directory, the repository and
// base image travelling through the environment, and the references parsed out of the output. | TestBuildImageSuccess
// 覆盖成功路径：argv、工作目录、经环境传递的仓库与基础镜像，以及从输出中解析出的引用。
func TestBuildImageSuccess(t *testing.T) {
	stubResolvedKo(t)

	dir := t.TempDir()
	var runs []koRun
	output := strings.Join([]string{
		"2026/10/17 10:00:00 Building github.com/shibingli/yd/cmd/app for linux/amd64",
		"registry.example.com/team/app:v1.2.3@" + koTestDigest,
	}, "\n")
	o := Options{
		Binary:     DefaultBinary,
		Repository: "registry.example.com/team/app",
		BaseImage:  "cgr.dev/chainguard/static:latest",
		WorkingDir: dir,
		Tags:       []string{"v1.2.3"},
		Platforms:  []string{"linux/amd64,linux/arm64"},
		Packages:   []string{"./cmd/app"},
		Push:       true,
		Exec:       stubExec(output, nil, &runs),
	}

	result, err := BuildImage(context.Background(), o, nil)
	if err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}

	if len(runs) != 1 {
		t.Fatalf("执行器被调用 %d 次 | runner called %d times, want 1", len(runs), len(runs))
	}
	run := runs[0]
	if run.name != "/resolved/ko" {
		t.Errorf("executable = %q | 可执行文件为 %q，期望 /resolved/ko", run.name, run.name)
	}
	if !slices.Equal(run.args, o.Args()) {
		t.Errorf("args = %q | args 得到 %q，期望 %q（即 Options.Args()）", run.args, run.args, o.Args())
	}
	if want, _ := filepath.Abs(dir); run.dir != want {
		t.Errorf("dir = %q | dir 得到 %q，期望 %q", run.dir, run.dir, want)
	}
	if value, count := envEntries(run.env, envRepository); value != o.Repository || count != 1 {
		t.Errorf("KO_DOCKER_REPO = %q (%d 次) | KO_DOCKER_REPO = %q (%d entries), want %q once",
			value, count, value, count, o.Repository)
	}
	if value, count := envEntries(run.env, envBaseImage); value != o.BaseImage || count != 1 {
		t.Errorf("KO_DEFAULTBASEIMAGE = %q (%d 次) | KO_DEFAULTBASEIMAGE = %q (%d entries), want %q once",
			value, count, value, count, o.BaseImage)
	}

	wantImage := "registry.example.com/team/app:v1.2.3@" + koTestDigest
	if result.Reference != wantImage {
		t.Errorf("Reference = %q | Reference 得到 %q，期望 %q", result.Reference, result.Reference, wantImage)
	}
	if result.Digest != koTestDigest {
		t.Errorf("Digest = %q | Digest 得到 %q，期望 %q", result.Digest, result.Digest, koTestDigest)
	}
	if !slices.Equal(result.Images, []string{wantImage}) {
		t.Errorf("Images = %q | Images 得到 %q，期望 %q", result.Images, result.Images, []string{wantImage})
	}
	if len(result.Logs) != 2 {
		t.Errorf("Logs 行数 = %d | Logs has %d lines, want 2", len(result.Logs), len(result.Logs))
	}
}

// TestBuildImageEnvOverridesDerivedValues checks that an explicit Options.Env entry wins over the
// derived repository, so a caller can point one run at another registry. | TestBuildImageEnvOverridesDerivedValues
// 校验显式的 Options.Env 条目覆盖推导出的仓库，使调用方可以把某一次运行指向另一个仓库。
func TestBuildImageEnvOverridesDerivedValues(t *testing.T) {
	stubResolvedKo(t)

	var runs []koRun
	o := Options{
		Binary:     DefaultBinary,
		Repository: "registry.example.com/team/app",
		WorkingDir: t.TempDir(),
		Local:      true,
		Env:        map[string]string{envRepository: "override.example.com/app", "GOFLAGS": "-trimpath"},
		Exec:       stubExec("", nil, &runs),
	}
	if _, err := BuildImage(context.Background(), o, nil); err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	if value, count := envEntries(runs[0].env, envRepository); value != "override.example.com/app" || count != 1 {
		t.Errorf("KO_DOCKER_REPO = %q (%d 次) | KO_DOCKER_REPO = %q (%d entries), want the override once",
			value, count, value, count)
	}
	if value, _ := envEntries(runs[0].env, "GOFLAGS"); value != "-trimpath" {
		t.Errorf("GOFLAGS = %q | GOFLAGS = %q, want -trimpath", value, value)
	}
	// An unset BaseImage must not inject KO_DEFAULTBASEIMAGE. | 未设置 BaseImage 时不应注入 KO_DEFAULTBASEIMAGE。
	if _, count := envEntries(runs[0].env, envBaseImage); count != 0 {
		t.Errorf("KO_DEFAULTBASEIMAGE 出现 %d 次 | KO_DEFAULTBASEIMAGE appears %d times, want 0", count, count)
	}
}

// TestBuildImageFailure checks that a non-zero exit yields ErrBuildFailed together with the parsed
// log, and that the result is still returned so the caller can show it. | TestBuildImageFailure 校验非零退出
// 返回 ErrBuildFailed 与已解析的日志，且仍然返回结果供调用方展示。
func TestBuildImageFailure(t *testing.T) {
	stubResolvedKo(t)

	output := strings.Join([]string{
		"Building github.com/shibingli/yd/cmd/app",
		"error: failed to build: exit status 1",
	}, "\n")
	o := Options{
		Binary:     DefaultBinary,
		Repository: "registry.example.com/team/app",
		WorkingDir: t.TempDir(),
		Exec:       stubExec(output, errors.New("exit status 1"), nil),
	}

	result, err := BuildImage(context.Background(), o, nil)
	if !errors.Is(err, ErrBuildFailed) {
		t.Fatalf("err = %v | err 得到 %v，期望 ErrBuildFailed", err, err)
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("错误细节应包含 ko 的最后一行输出：%v | the detail must carry ko's last output line: %v", err, err)
	}
	if result == nil || len(result.Logs) != 2 {
		t.Fatalf("失败时也必须返回日志 | the log must be returned on failure too: %+v", result)
	}
	if result.Reference != "" {
		t.Errorf("失败时不应解析出镜像引用：%q | no reference may be parsed from a failure: %q", result.Reference, result.Reference)
	}
}

// TestBuildImageLocalDaemonHint checks that an unreachable local daemon under --local, by far the
// most common --local failure, is spelled out instead of being buried in go-containerregistry's
// wording. | TestBuildImageLocalDaemonHint 校验 --local 下本地守护进程不可达——最常见的一类 --local
// 失败——会被明确点出，而不是埋在 go-containerregistry 的措辞里。
func TestBuildImageLocalDaemonHint(t *testing.T) {
	stubResolvedKo(t)

	output := "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"
	o := Options{
		Binary:     DefaultBinary,
		WorkingDir: t.TempDir(),
		Local:      true,
		Exec:       stubExec(output, errors.New("exit status 1"), nil),
	}

	_, err := BuildImage(context.Background(), o, nil)
	if !errors.Is(err, ErrBuildFailed) {
		t.Fatalf("err = %v | err 得到 %v，期望 ErrBuildFailed", err, err)
	}
	if !strings.Contains(err.Error(), "本地 docker 守护进程不可达") {
		t.Errorf("应给出守护进程不可达的双语提示：%v | the bilingual daemon hint is missing: %v", err, err)
	}
}

// TestBuildImageBinaryNotFound checks that a missing ko is reported through the sentinel with the
// installation hint, and that nothing is executed. | TestBuildImageBinaryNotFound 校验 ko 缺失时经哨兵报告并
// 附带安装提示，且不会执行任何命令。
func TestBuildImageBinaryNotFound(t *testing.T) {
	t.Setenv(EnvBinary, "")
	usePathLookup(t, stubLookup(map[string]string{}))

	executed := false
	o := Options{
		Repository: "registry.example.com/team/app",
		WorkingDir: t.TempDir(),
		Exec: func(context.Context, string, []string, string, []string) ([]byte, error) {
			executed = true
			return nil, nil
		},
	}

	result, err := BuildImage(context.Background(), o, nil)
	if !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("err = %v | err 得到 %v，期望 ErrBinaryNotFound", err, err)
	}
	if result != nil {
		t.Errorf("未启动构建时不应返回结果 | no result may be returned when nothing ran: %+v", result)
	}
	if executed {
		t.Error("ko 未解析成功却执行了命令 | the runner must not be called when ko was not resolved")
	}
	if !strings.Contains(err.Error(), InstallHint) {
		t.Errorf("安装提示缺失：%v | the install hint is missing: %v", err, err)
	}
}

// TestBuildImageNoRepository checks that a full build without a repository is refused before
// anything is executed, because ko itself would only fail deep inside its publisher. | TestBuildImageNoRepository
// 校验既没有仓库、又未开启本地模式时会在执行任何命令之前被拒绝，因为 ko 自身只会在发布器深处才失败。
func TestBuildImageNoRepository(t *testing.T) {
	stubResolvedKo(t)

	executed := false
	o := Options{
		Binary:     DefaultBinary,
		WorkingDir: t.TempDir(),
		Exec: func(context.Context, string, []string, string, []string) ([]byte, error) {
			executed = true
			return nil, nil
		},
	}

	result, err := BuildImage(context.Background(), o, nil)
	if !errors.Is(err, ErrNoRepository) {
		t.Fatalf("err = %v | err 得到 %v，期望 ErrNoRepository", err, err)
	}
	if result != nil || executed {
		t.Errorf("校验失败时不得执行任何命令 | nothing may run after a validation failure: result=%+v executed=%v",
			result, executed)
	}
}

// TestBuildImageCanceled checks that a canceled context stays recognizable on the error chain. | TestBuildImageCanceled
// 校验被取消的上下文在错误链上依然可识别。
func TestBuildImageCanceled(t *testing.T) {
	stubResolvedKo(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := Options{
		Binary:     DefaultBinary,
		Repository: "registry.example.com/team/app",
		WorkingDir: t.TempDir(),
		Exec: func(ctx context.Context, _ string, _ []string, _ string, _ []string) ([]byte, error) {
			return []byte("interrupted"), ctx.Err()
		},
	}

	_, err := BuildImage(ctx, o, nil)
	if !errors.Is(err, ErrBuildFailed) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v | err 得到 %v，期望同时匹配 ErrBuildFailed 与 context.Canceled", err, err)
	}
}
