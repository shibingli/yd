// Package ko wraps the ko builder (https://ko.build) as an optional external CLI behind `yd ko`. | Package ko 把 ko 构建器（https://ko.build）作为可选外部 CLI 封装在 `yd ko` 背后。
//
// ko builds images straight from Go source: it compiles the packages, assembles a layer from the
// binaries and publishes the result, so a project needs neither a Dockerfile nor a container build
// daemon. The CLI is deliberately not vendored as a library - pulling github.com/google/ko in would
// drag go-containerregistry and a long dependency tail into this module - so this package follows
// the same shape as docker, betteralign and Tailwind: call the external tool, keep an injection
// point for tests, and degrade gracefully when the tool is missing. | ko 直接从 Go 源码构建镜像：编译包、
// 用二进制组装镜像层并发布结果，因此项目既不需要 Dockerfile 也不需要容器构建守护进程。该 CLI 刻意不作为库引入
// ——把 github.com/google/ko 拉进来会连带 go-containerregistry 及一长串依赖——因此本包与 docker、betteralign、
// Tailwind 保持同一形态：调用外部工具、保留注入点供测试使用、工具缺失时优雅降级。
//
// The flow is: | 流程如下：
//
//  1. validate the options: packages default to ./cmd/app and a repository is mandatory unless the
//     image only goes to the local daemon; |  1. 校验选项：包缺省为 ./cmd/app，除「只载入本地守护进程」外必须给出仓库；
//  2. resolve the ko executable through Options.Binary, YD_KO_BIN, <workdir>/.yd/tools and PATH; |  2. 依次经 Options.Binary、YD_KO_BIN、<workdir>/.yd/tools 与 PATH 解析 ko 可执行文件；
//  3. run `ko build` with the repository and the base image passed through KO_DOCKER_REPO and
//     KO_DEFAULTBASEIMAGE, the documented interface, because ko has no flag for either; |  3. 以 KO_DOCKER_REPO 与
//     KO_DEFAULTBASEIMAGE 传递仓库与基础镜像并执行 `ko build`——这是官方文档给出的接口，ko 对两者都没有标志；
//  4. parse the image references ko printed and report them together with the full log. |  4. 解析 ko 打印的镜像引用，连同完整日志一起报告。
//
// Every external command goes through Options.Exec, so no test ever needs a real ko binary, a
// registry or a network. | 所有外部命令都经由 Options.Exec 执行，因此测试永远不需要真实 ko 二进制、镜像仓库或网络。
package ko

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
)

// Build sentinel errors. | 构建相关哨兵错误。
var (
	// ErrBinaryNotFound indicates that no source yielded a ko executable. | ErrBinaryNotFound 表示所有来源都未能给出 ko 可执行文件。
	ErrBinaryNotFound = errx.New("ko: ko binary not found | ko：未找到 ko 可执行文件")
	// ErrNoRepository indicates that neither the options nor --local supplied an image repository. | ErrNoRepository 表示选项与 --local 都没有给出镜像仓库。
	ErrNoRepository = errx.New("ko: no image repository configured | ko：未配置镜像仓库")
	// ErrBuildFailed indicates that the ko build exited with an error. | ErrBuildFailed 表示 ko 构建以错误退出。
	ErrBuildFailed = errx.New("ko: image build failed | ko：镜像构建失败")
	// ErrInvalidOptions indicates an option combination ko itself would refuse. | ErrInvalidOptions 表示 ko 自身会拒绝的选项组合。
	ErrInvalidOptions = errx.New("ko: invalid options | ko：非法的选项")
)

// The environment variables ko reads the two non-flag settings from. | ko 从中读取两项非标志设置的环境变量。
const (
	// envRepository is ko's documented interface for the repository, since `ko build` has no
	// --repository flag. | envRepository 是 ko 文档化的仓库接口，因为 `ko build` 没有 --repository 标志。
	envRepository = "KO_DOCKER_REPO"
	// envBaseImage overrides the base image from .ko.yaml, since `ko build` has no --base flag. | envBaseImage
	// 覆盖 .ko.yaml 里的基础镜像，因为 `ko build` 没有 --base 标志。
	envBaseImage = "KO_DEFAULTBASEIMAGE"
)

// daemonMarkers are the output fragments that mean "no local daemon is listening". They match the
// ones the docker package treats as a missing daemon, so both commands give the same reading. | daemonMarkers
// 是表示「本地守护进程没有在监听」的输出片段，与 docker 包视为守护进程缺失的片段一致，使两个命令的判读相同。
var daemonMarkers = []string{
	"cannot connect to the docker daemon",
	"is the docker daemon running",
	"error during connect",
	"daemon is not running",
	"failed to connect to the docker api",
}

// ExecCommandFunc runs one external command and returns its merged stdout and stderr. | ExecCommandFunc
// 执行一个外部命令并返回合并后的 stdout 与 stderr。
//
// It is the injection point of the whole package: replacing it is what lets the tests cover success,
// failure and a missing binary without a real ko, registry or network. | 它是整个包的注入点：替换它即可在
// 没有真实 ko、镜像仓库与网络的前提下覆盖成功、失败与二进制缺失三条路径。
type ExecCommandFunc func(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, error)

// Options configures one image build. | Options 配置一次镜像构建。
type Options struct {
	// Exec overrides the command runner; nil means runCommand, which merges the output of the
	// child's stdout and stderr. | Exec 覆盖命令执行器；为 nil 表示 runCommand，它会合并子进程的
	// stdout 与 stderr。
	Exec ExecCommandFunc
	// Env holds extra environment entries, applied last so they may override the derived
	// KO_DOCKER_REPO / KO_DEFAULTBASEIMAGE values. | Env 保存附加环境变量，最后叠加，因此可以覆盖推导出的
	// KO_DOCKER_REPO / KO_DEFAULTBASEIMAGE。
	Env map[string]string
	// Binary is the explicit ko executable; empty falls back to the resolution chain. | Binary 是显式的
	// ko 可执行文件；为空时回退到解析链。
	Binary string
	// Repository is the target repository, e.g. registry.example.com/team/app, passed through
	// KO_DOCKER_REPO. | Repository 是目标仓库，例如 registry.example.com/team/app，经 KO_DOCKER_REPO 传递。
	Repository string
	// BaseImage overrides the base image, passed through KO_DEFAULTBASEIMAGE. | BaseImage 覆盖基础镜像，
	// 经 KO_DEFAULTBASEIMAGE 传递。
	BaseImage string
	// WorkingDir is both the directory ko runs in and the root of the local tool lookup; empty
	// means the current directory. | WorkingDir 既是 ko 运行所在目录，也是本地工具查找的根；为空表示当前目录。
	WorkingDir string
	// Packages are the import paths to build; empty means DefaultPackage. | Packages 是待构建的导入路径；
	// 为空表示 DefaultPackage。
	Packages []string
	// Platforms are the targets passed as one --platform flag each, e.g. linux/amd64; ko emits a
	// manifest list when several of them match a multi-arch base image. | Platforms 是逐个以 --platform
	// 传入的目标平台，例如 linux/amd64；当多个平台命中多架构基础镜像时 ko 会产出清单列表。
	Platforms []string
	// Tags are the tags passed as one --tags flag each; empty keeps ko's own default (latest). | Tags 是
	// 逐个以 --tags 传入的标签；为空时沿用 ko 自身的默认值（latest）。
	Tags []string
	// Local loads the result into the local docker daemon instead of a registry (--local). | Local 把
	// 结果载入本地 docker 守护进程而不推送到仓库（--local）。
	Local bool
	// Push pushes the result to the repository (--push); it is always passed explicitly, so the
	// intent is visible in the argv rather than inherited from ko's default. | Push 把结果推送到仓库
	// （--push）；它始终显式传入，使意图体现在 argv 中而不依赖 ko 的默认值。
	Push bool
}

// Result reports one finished image build. | Result 报告一次已完成的镜像构建。
type Result struct {
	// Reference is the first image reference ko printed. | Reference 是 ko 打印的第一个镜像引用。
	Reference string
	// Digest is the sha256 digest of the first reference that carries one; empty when none does. | Digest
	// 是首个带摘要的引用的 sha256 摘要；都没有时为空。
	Digest string
	// Images holds every distinct reference ko printed, in order. | Images 按顺序保存 ko 打印的全部不重复引用。
	Images []string
	// Logs holds every output line of the build, in order. | Logs 按顺序保存构建的全部输出行。
	Logs []string
}

// exec returns the command runner of this run. | exec 返回本次运行使用的命令执行器。
func (o Options) exec() ExecCommandFunc {
	if o.Exec != nil {
		return o.Exec
	}
	return runCommand
}

// runCommand is the default ExecCommandFunc. | runCommand 是默认的 ExecCommandFunc。
//
// ko writes the image references to stdout and its progress log to stderr; both streams are merged
// into one buffer because the references are found by shape rather than by position, and a failure
// is much easier to explain with the progress messages attached. | ko 把镜像引用写到 stdout，把进度日志
// 写到 stderr；两路被合并进同一个缓冲区，因为引用是按形态而不是按位置识别的，而且附上进度信息的失败原因要好解释得多。
func runCommand(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// withDefaults fills every zero-valued option and normalizes the working directory. | withDefaults
// 填充全部零值选项并归一化工作目录。
func (o Options) withDefaults() Options {
	// ko's documented environment variables are accepted as inputs too, not only passed through
	// to the child: validate() used to reject a run that set only KO_DOCKER_REPO even though the
	// error message named it as valid. | ko 文档化的环境变量也可作为输入，而不只是透传给子进程：
	// validate() 过去会拒绝只设置 KO_DOCKER_REPO 的运行，尽管错误文案把它列为有效来源。
	if strings.TrimSpace(o.Repository) == "" {
		o.Repository = strings.TrimSpace(os.Getenv(envRepository))
	}
	if strings.TrimSpace(o.BaseImage) == "" {
		o.BaseImage = strings.TrimSpace(os.Getenv(envBaseImage))
	}
	if strings.TrimSpace(o.WorkingDir) == "" {
		o.WorkingDir = "."
	}
	if abs, err := filepath.Abs(o.WorkingDir); err == nil {
		o.WorkingDir = abs
	}
	return o
}

// BuildImage builds and publishes one image, reporting the references ko printed together with the
// full log. | BuildImage 构建并发布一个镜像，报告 ko 打印的引用与完整日志。
//
// The returned Result is never nil once ko was actually started, so the caller can always show the
// log; an invalid option combination yields ErrInvalidOptions, an unresolvable executable yields
// ErrBinaryNotFound and every other failure is wrapped in ErrBuildFailed, except that an
// unreachable local daemon under Local is spelled out in the detail because it is by far the most
// common cause of a --local failure. | 一旦真正启动过 ko，返回的 Result 就不会是 nil，调用方始终可以
// 展示日志；非法选项组合返回 ErrInvalidOptions，无法解析的可执行文件返回 ErrBinaryNotFound，其余失败包装进
// ErrBuildFailed——例外是 Local 下本地守护进程不可达的情形，它会在细节里被明确点出，因为这是 --local 失败最常见的原因。
func BuildImage(ctx context.Context, o Options, log *logx.Logger) (*Result, error) {
	if log == nil {
		log = logx.Default()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	o = o.withDefaults()
	if err := o.validate(); err != nil {
		return nil, err
	}
	bin, err := Resolve(o)
	if err != nil {
		return nil, err
	}

	args := o.Args()
	log.Debug("ko: running | ko：正在执行",
		logx.Str("bin", bin), logx.Strs("args", args), logx.Str("dir", o.WorkingDir))

	out, runErr := o.exec()(ctx, bin, args, o.WorkingDir, o.environ())
	logs := splitLines(string(out))
	for _, line := range logs {
		log.Debug("ko: output | ko：输出", logx.Str("line", line))
	}

	result := newResult(logs)
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, errx.WrapDetail(ctxErr, ErrBuildFailed, "ko build cancelled | ko 构建被取消：%v", runErr)
		}
		return result, buildFailure(o, runErr, logs)
	}

	log.Info("ko: image built | ko：镜像已构建",
		logx.Str("reference", result.Reference),
		logx.Str("digest", result.Digest),
		logx.Int("images", len(result.Images)),
		logx.Int("log_lines", len(logs)))
	return result, nil
}

// buildFailure turns a failed run into a sentinel error that names the most likely cause. | buildFailure
// 把失败的运行转换为指出最可能原因哨兵错误。
func buildFailure(o Options, runErr error, logs []string) error {
	if o.Local && isDaemonUnavailable(logs) {
		return errx.WrapDetail(runErr, ErrBuildFailed,
			"local docker daemon unreachable; start docker or drop --local | 本地 docker 守护进程不可达；请启动 docker 或去掉 --local：%s",
			lastLine(logs))
	}
	return errx.WrapDetail(runErr, ErrBuildFailed, "%s: %s | %s：%s",
		DefaultBinary, lastLine(logs), DefaultBinary, lastLine(logs))
}

// newResult summarizes the build output. | newResult 汇总构建输出。
func newResult(logs []string) *Result {
	images := parseImages(logs)
	result := &Result{Images: images, Logs: logs}
	if len(images) > 0 {
		result.Reference = images[0]
		for _, image := range images {
			if digest := digestOf(image); digest != "" {
				result.Digest = digest
				break
			}
		}
	}
	return result
}

// environ returns the child environment of the ko process. | environ 返回 ko 子进程的环境。
//
// The repository and the base image travel as KO_DOCKER_REPO and KO_DEFAULTBASEIMAGE, which are the
// documented interfaces for both: ko deliberately has no --repository and no --base flag. The
// inherited process environment is kept, the two derived values are laid on top of it and
// Options.Env is applied last, so an explicit entry always wins. Keys are sorted, which keeps the
// child environment deterministic. | 仓库与基础镜像以 KO_DOCKER_REPO 与 KO_DEFAULTBASEIMAGE 传递，二者都是
// 官方文档给出的接口：ko 刻意不提供 --repository 与 --base 标志。继承的进程环境被保留，两个推导值叠加其上，
// Options.Env 最后应用，因此显式条目始终优先。键经过排序，使子进程环境保持确定性。
func (o Options) environ() []string {
	// Windows environment names are case-insensitive; os/exec deduplicates them case-insensitively
	// and keeps the last occurrence. Without normalizing here, an inherited Ko_Docker_Repo would
	// survive as a separate map entry, sort after the derived KO_DOCKER_REPO and silently win.
	// | Windows 的环境变量名不区分大小写；os/exec 按不区分大小写去重并保留最后一项。若这里不统一
	// 大小写，继承来的 Ko_Docker_Repo 会作为独立条目保留，排序后落在推导出的 KO_DOCKER_REPO 之后并
	// 悄悄获胜。
	keyOf := func(key string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(key)
		}
		return key
	}
	merged := make(map[string]string, len(o.Env)+2)
	for _, kv := range os.Environ() {
		if key, value, ok := strings.Cut(kv, "="); ok {
			merged[keyOf(key)] = value
		}
	}
	if o.Repository != "" {
		merged[keyOf(envRepository)] = o.Repository
	}
	if o.BaseImage != "" {
		merged[keyOf(envBaseImage)] = o.BaseImage
	}
	for key, value := range o.Env {
		merged[keyOf(key)] = value
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+merged[key])
	}
	return env
}

// isDaemonUnavailable reports whether the build output blames a missing local daemon. | isDaemonUnavailable
// 判断构建输出是否归因于本地守护进程缺失。
func isDaemonUnavailable(logs []string) bool {
	joined := strings.ToLower(strings.Join(logs, "\n"))
	for _, marker := range daemonMarkers {
		if strings.Contains(joined, marker) {
			return true
		}
	}
	return false
}

// lastLine returns the last log line, used as the error detail. | lastLine 返回最后一条日志行，用作错误细节。
func lastLine(logs []string) string {
	if len(logs) == 0 {
		return ""
	}
	return logs[len(logs)-1]
}

// splitLines splits s into non-empty lines with surrounding blanks removed. | splitLines 把 s 切分为去除了首尾空白的非空行。
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
