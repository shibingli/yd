// Package docker implements the scratch-based multi-stage image build behind `yd docker`. | Package docker 实现 `yd docker` 背后基于 scratch 的多阶段镜像构建。
//
// The flow is deliberately thin: | 流程刻意保持轻薄：
//
//  1. resolve the Dockerfile through afero, preferring the project file over the embedded template; |  1. 通过 afero 解析 Dockerfile，项目文件优先于内置模板；
//  2. optionally refresh the embedded assets (the Tailwind stage of `yd build`); |  2. 可选地刷新内嵌资产（`yd build` 的 Tailwind 阶段）；
//  3. probe the daemon with `docker version --format {{.Server.Version}}`, reporting ErrDaemonUnavailable when nothing answers; |  3. 以 `docker version --format {{.Server.Version}}` 探活守护进程，无应答时报告 ErrDaemonUnavailable；
//  4. run `docker build` with --platform/--build-arg/-t/--no-cache/--target/--push and report the parsed image id. |  4. 以 --platform/--build-arg/-t/--no-cache/--target/--push 执行 `docker build`，并报告解析出的镜像 ID。
//
// Every external command goes through Options.ExecCommand, so no test ever needs a
// real docker daemon or network access. | 所有外部命令都经由 Options.ExecCommand 执行，因此测试永远不需要真实
// docker 守护进程与网络。
package docker

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/tooling/build"
	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
)

// Build sentinel errors. | 构建相关哨兵错误。
var (
	// ErrDaemonUnavailable indicates that the docker daemon is not reachable. | ErrDaemonUnavailable 表示 docker 守护进程不可达。
	ErrDaemonUnavailable = errs.New("docker: daemon unavailable | docker：守护进程不可用")
	// ErrBuildFailed indicates that `docker build` exited with an error. | ErrBuildFailed 表示 `docker build` 以错误退出。
	ErrBuildFailed = errs.New("docker: image build failed | docker：镜像构建失败")
)

// DefaultDockerBin is the docker CLI looked up on PATH when Options.DockerBin is empty. | DefaultDockerBin 是 Options.DockerBin 为空时在 PATH 上查找的 docker CLI。
const DefaultDockerBin = "docker"

// daemonMarkers are the output fragments that mean "no daemon is listening". | daemonMarkers 是表示"没有守护进程在监听"的输出片段。
var daemonMarkers = []string{
	"cannot connect to the docker daemon",
	"is the docker daemon running",
	"error during connect",
	"daemon is not running",
	"cannot connect to the docker daemon at",
}

// ExecCommandFunc constructs an external command; it mirrors exec.CommandContext so
// that the default can be replaced verbatim. | ExecCommandFunc 构造外部命令；签名与
// exec.CommandContext 一致，便于原样替换默认实现。
type ExecCommandFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

// Options configures one image build. | Options 配置一次镜像构建。
type Options struct {
	// BuildArgs are passed as --build-arg in sorted key order, so the argv stays deterministic. | BuildArgs 按键排序后以 --build-arg 传入，保证 argv 稳定可复现。
	BuildArgs map[string]string
	// PrepareAssets overrides the asset stage; nil means build.BuildTailwind, which
	// is silently skipped when the project has no Tailwind stylesheet. | PrepareAssets 覆盖资产阶段；
	// 为 nil 表示使用 build.BuildTailwind，项目没有 Tailwind 样式表时会静默跳过。
	PrepareAssets func(ctx context.Context, dir string, log *ydlog.Logger) error
	// ExecCommand overrides the external command constructor; nil means exec.CommandContext. | ExecCommand 覆盖外部命令构造器；为 nil 表示 exec.CommandContext。
	ExecCommand ExecCommandFunc
	// Dir is the project directory and the docker build context; empty means the current directory. | Dir 是项目目录与 docker 构建上下文；为空表示当前目录。
	Dir string
	// Dockerfile is the recipe path relative to Dir (or absolute); empty means Dockerfile. | Dockerfile 是相对 Dir（或绝对）的配方路径；为空表示 Dockerfile。
	Dockerfile string
	// Tag is the image reference passed to -t; empty omits the flag. | Tag 是传给 -t 的镜像引用；为空则不传该标志。
	Tag string
	// Platform is the target platform passed to --platform, e.g. linux/amd64. | Platform 是传给 --platform 的目标平台，例如 linux/amd64。
	Platform string
	// Target selects a build stage through --target. | Target 通过 --target 选择构建阶段。
	Target string
	// DockerBin overrides the docker CLI; empty means docker. | DockerBin 覆盖 docker CLI；为空表示 docker。
	DockerBin string
	// NoCache disables the docker build cache. | NoCache 关闭 docker 构建缓存。
	NoCache bool
	// Push asks the builder to push the result (--push). | Push 要求构建器推送结果（--push）。
	Push bool
	// WithAssets refreshes the embedded stylesheet before building, so that the
	// go:embed assets baked into the image are never stale. | WithAssets 在构建前刷新内嵌样式表，
	// 保证烘进镜像的 go:embed 资产不过期。
	WithAssets bool
	// UPX compresses the built binary with the embedded Linux UPX; the resolved Dockerfile must carry the
	// YD_INCLUDE_UPX step, otherwise the step is skipped with a warning. | UPX 用嵌入的 Linux UPX 压缩构建
	// 产物；解析出的 Dockerfile 必须带 YD_INCLUDE_UPX 步骤，否则告警并跳过。
	UPX bool
}

// Result reports one finished image build. | Result 报告一次已完成的镜像构建。
type Result struct {
	// ImageID is the digest or short id parsed from the build output; it stays empty when the output format is unknown. | ImageID 是从构建输出解析出的摘要或短 ID；输出格式无法识别时为空。
	ImageID string
	// Tag is the image reference requested through -t. | Tag 是通过 -t 请求的镜像引用。
	Tag string
	// Logs holds every output line of the build, in order. | Logs 按顺序保存构建的全部输出行。
	Logs []string
}

// execCommand returns the command constructor of this run. | execCommand 返回本次运行使用的命令构造器。
func (o Options) execCommand() ExecCommandFunc {
	if o.ExecCommand != nil {
		return o.ExecCommand
	}
	return exec.CommandContext
}

// withDefaults fills every zero-valued option and normalizes the context directory. | withDefaults 填充全部零值选项并归一化构建上下文目录。
func (o Options) withDefaults() Options {
	if o.Dir == "" {
		o.Dir = "."
	}
	if abs, err := filepath.Abs(o.Dir); err == nil {
		o.Dir = abs
	}
	if o.Dockerfile == "" {
		o.Dockerfile = DefaultDockerfile
	}
	if o.DockerBin == "" {
		o.DockerBin = DefaultDockerBin
	}
	return o
}

// buildArgs assembles the argv of `docker build`; the context directory is always last. | buildArgs 组装 `docker build` 的 argv，构建上下文目录始终位于末尾。
func (o Options) buildArgs(dockerfilePath string) []string {
	args := []string{"build"}
	if o.Platform != "" {
		args = append(args, "--platform", o.Platform)
	}
	if o.NoCache {
		args = append(args, "--no-cache")
	}
	if o.Target != "" {
		args = append(args, "--target", o.Target)
	}
	if o.Tag != "" {
		args = append(args, "-t", o.Tag)
	}
	for _, kv := range sortedBuildArgs(o.BuildArgs) {
		args = append(args, "--build-arg", kv)
	}
	if o.Push {
		args = append(args, "--push")
	}
	return append(args, "-f", dockerfilePath, o.Dir)
}

// sortedBuildArgs renders --build-arg values in sorted key order. Values keep their
// first "=" so that a value may itself contain "=". | sortedBuildArgs 按键排序渲染
// --build-arg 取值；值保留首个 "="，因此取值本身可以包含 "="。
func sortedBuildArgs(buildArgs map[string]string) []string {
	out := make([]string, 0, len(buildArgs))
	for key, value := range buildArgs {
		out = append(out, key+"="+value)
	}
	sort.Strings(out)
	return out
}

// BuildImage builds one image and reports the parsed image id together with the
// full build log. | BuildImage 构建一个镜像，并报告解析出的镜像 ID 与完整构建日志。
//
// The recipe comes from fs, so tests can drive the whole flow from an in-memory file
// system; a project Dockerfile always wins over the embedded template. The daemon is
// probed first and a failing probe, like a daemon outage surfacing during the build,
// returns ErrDaemonUnavailable, while any other rejection returns ErrBuildFailed. The
// returned Result is never nil once the build was attempted, so the caller can always
// show the log. | 配方来自 fs，因此测试可以用内存文件系统驱动整个流程；项目 Dockerfile 始终
// 优先于内置模板。守护进程会被预先探活：探活失败，或构建过程中暴露守护进程故障，都返回
// ErrDaemonUnavailable；其余失败返回 ErrBuildFailed。一旦真正发起过构建，返回的 Result 就不会是 nil，
// 调用方始终可以展示日志。
func BuildImage(ctx context.Context, o Options, fs afero.Fs, log *ydlog.Logger) (*Result, error) {
	if log == nil {
		log = ydlog.Default()
	}
	o = o.withDefaults()
	run := o.execCommand()

	dockerfile, err := ResolveDockerfile(fs, o.Dir, o.Dockerfile)
	if err != nil {
		return nil, err
	}
	defer dockerfile.Cleanup()
	log.Debug("dockerfile resolved | 已解析 Dockerfile",
		ydlog.Str("path", dockerfile.Path),
		ydlog.Bool("from_project", dockerfile.FromProject),
	)

	if o.WithAssets {
		if err := o.prepareAssets(ctx, log); err != nil {
			return nil, err
		}
	}

	server, err := o.serverVersion(ctx, run)
	if err != nil {
		return nil, err
	}
	log.Debug("docker daemon reachable | docker 守护进程可达", ydlog.Str("server_version", server))

	// UPX is a best-effort step: the embedded Linux payload is staged into the build context, and a
	// Dockerfile without the YD_INCLUDE_UPX step simply skips it with a warning. | UPX 是尽力而为的步骤：
	// 嵌入的 Linux payload 会被暂存进构建上下文，没有 YD_INCLUDE_UPX 步骤的 Dockerfile 只会告警并跳过。
	if o.UPX {
		if !strings.Contains(string(dockerfile.Content), "YD_INCLUDE_UPX") {
			log.Warn("docker: the resolved Dockerfile has no UPX step; skipping compression (regenerate the project or add ARG YD_INCLUDE_UPX) | docker：解析出的 Dockerfile 没有 UPX 步骤，跳过压缩（重新生成项目或补上 ARG YD_INCLUDE_UPX）")
		} else {
			defer stageUPX(o, log).cleanup(log)
		}
	}

	cmd := run(ctx, o.DockerBin, o.buildArgs(dockerfile.Path)...)
	cmd.Dir = o.Dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	runErr := cmd.Run()

	logs := splitLines(buf.String())
	for _, line := range logs {
		log.Debug("docker output | docker 输出", ydlog.Str("line", line))
	}
	result := &Result{ImageID: ParseImageID(logs), Tag: o.Tag, Logs: logs}
	if runErr != nil {
		if isDaemonUnavailable(logs) {
			return result, errs.WrapDetail(runErr, ErrDaemonUnavailable, "%s | %s", o.DockerBin, o.DockerBin)
		}
		return result, errs.WrapDetail(runErr, ErrBuildFailed, "%s | %s: %s", o.DockerBin, o.DockerBin, lastLine(logs))
	}

	fields := []ydlog.Field{ydlog.Str("image_id", result.ImageID), ydlog.Str("tag", result.Tag), ydlog.Int("log_lines", len(logs))}
	if size := parseImageSize(logs); size != "" {
		fields = append(fields, ydlog.Str("size", size))
	}
	log.Info("image built | 镜像已构建", fields...)
	return result, nil
}

// prepareAssets refreshes the embedded assets before the image build. | prepareAssets 在镜像构建前刷新内嵌资产。
//
// A project without Tailwind is not an error: the stage is skipped with a warning,
// because the image build itself is still perfectly valid. | 项目没有 Tailwind 不算错误：该阶段会以告警
// 跳过，因为镜像构建本身依然完全有效。
func (o Options) prepareAssets(ctx context.Context, log *ydlog.Logger) error {
	if o.PrepareAssets != nil {
		return o.PrepareAssets(ctx, o.Dir, log)
	}
	// 与 yd build 使用同一套资产阶段：只跑 Tailwind 会让镜像里的 JavaScript/SVG 仍是未压缩源码。 |
	// The same asset stages as yd build: Tailwind alone would leave unminified JavaScript and SVG in the image.
	if err := build.PrepareAssets(ctx, o.Dir, log); err != nil {
		return err
	}
	log.Info("embedded assets refreshed | 内嵌资产已刷新")
	return nil
}

// serverVersion probes the daemon and returns its version string. | serverVersion 探活守护进程并返回其版本字符串。
func (o Options) serverVersion(ctx context.Context, run ExecCommandFunc) (string, error) {
	cmd := run(ctx, o.DockerBin, "version", "--format", "{{.Server.Version}}")
	cmd.Dir = o.Dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		return "", errs.WrapDetail(err, ErrDaemonUnavailable,
			"%s version failed | %s version 执行失败: %s", o.DockerBin, o.DockerBin, strings.TrimSpace(buf.String()))
	}
	return strings.TrimSpace(buf.String()), nil
}

// isDaemonUnavailable reports whether the build output blames a missing daemon. | isDaemonUnavailable 判断构建输出是否归因于守护进程缺失。
func isDaemonUnavailable(logs []string) bool {
	joined := strings.ToLower(strings.Join(logs, "\n"))
	for _, marker := range daemonMarkers {
		if strings.Contains(joined, marker) {
			return true
		}
	}
	return false
}

// ParseImageID extracts the image id from the build output: the first full sha256
// digest wins, otherwise the classic "Successfully built <id>" line is used. An
// unrecognized format yields an empty string. | ParseImageID 从构建输出中提取镜像 ID：优先取
// 第一个完整的 sha256 摘要，其次使用经典构建器的 "Successfully built <id>" 行；无法识别时返回空串。
func ParseImageID(logs []string) string {
	for _, line := range logs {
		if idx := strings.Index(line, "sha256:"); idx >= 0 {
			if id, ok := cutDigest(line[idx:]); ok {
				return "sha256:" + id
			}
		}
	}
	for _, line := range logs {
		if id, ok := strings.CutPrefix(strings.TrimSpace(line), "Successfully built "); ok {
			if id = strings.TrimSpace(id); id != "" {
				return id
			}
		}
	}
	return ""
}

// cutDigest returns the 64 lowercase hex digits following a "sha256:" prefix. | cutDigest 返回 "sha256:" 前缀之后的 64 位小写十六进制摘要。
func cutDigest(s string) (string, bool) {
	const prefix = "sha256:"
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok || len(rest) < 64 {
		return "", false
	}
	digest := rest[:64]
	for i := 0; i < len(digest); i++ {
		c := digest[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return digest, true
}

// parseImageSize extracts the human readable image size when the builder reports
// one; the size also appears in the build log, so an empty result is harmless. | parseImageSize
// 在构建器报告体积时提取可读大小；体积同时存在于构建日志中，因此返回空串也无害。
func parseImageSize(logs []string) string {
	for i := len(logs) - 1; i >= 0; i-- {
		line := logs[i]
		if idx := strings.Index(strings.ToLower(line), "size:"); idx >= 0 {
			if size := strings.TrimSpace(line[idx+len("size:"):]); size != "" {
				return size
			}
		}
	}
	return ""
}

// lastLine returns the last non-empty log line, used as the error detail. | lastLine 返回最后一条非空日志行，用作错误细节。
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
