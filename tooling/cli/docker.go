// This file implements the docker command: it owns flag parsing, the default image tag and the
// human-readable result report, and nothing else. The image build itself lives in the docker
// package. | 本文件实现 docker 命令：只负责标志解析、缺省镜像标签与人类可读的结果报告，别无其它；
// 镜像构建本身由 docker 包负责。

package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shibingli/yd/core/version"
	"github.com/shibingli/yd/tooling/docker"
	"github.com/shibingli/ydx/json"
	ydlog "github.com/shibingli/ydx/log"
	"github.com/spf13/afero"
	"github.com/spf13/pflag"
)

// dockerCommandName is the command word of the container command. | dockerCommandName 是容器命令的命令词。
const dockerCommandName = "docker"

// The docker command's flag names. | docker 命令的标志名。
const (
	// flagDockerTag overrides the image reference. | flagDockerTag 覆盖镜像引用。
	flagDockerTag = "tag"
	// flagDockerPlatform sets the target platform of the image. | flagDockerPlatform 设置镜像的目标平台。
	flagDockerPlatform = "platform"
	// flagDockerNoCache disables the docker build cache. | flagDockerNoCache 关闭 docker 构建缓存。
	flagDockerNoCache = "no-cache"
	// flagDockerFile overrides the Dockerfile path. | flagDockerFile 覆盖 Dockerfile 路径。
	flagDockerFile = "file"
	// flagDockerTarget stops the build at one stage. | flagDockerTarget 让构建停在某个阶段。
	flagDockerTarget = "target"
	// flagDockerPush pushes the result after a successful build. | flagDockerPush 在构建成功后推送结果。
	flagDockerPush = "push"
	// flagDockerBin overrides the docker CLI. | flagDockerBin 覆盖 docker CLI。
	flagDockerBin = "docker-bin"
	// flagDockerNoAssets skips the embedded asset refresh. | flagDockerNoAssets 跳过内嵌资产刷新。
	flagDockerNoAssets = "no-assets"
)

// dockerUsageLine is the usage line of the docker command. | dockerUsageLine 是 docker 命令的用法行。
const dockerUsageLine = "yd docker [--tag <project>:<version>] [--platform linux/amd64] [flags]"

// dockerProjectMetaName is the metadata file `yd new` writes and the docker and service commands
// read for the project name. | dockerProjectMetaName 是 `yd new` 写入、docker 与 service 命令用于读取
// 项目名的元信息文件。
const dockerProjectMetaName = ".yd.json"

// dockerFallbackProject is the project name used when neither .yd.json nor the working directory
// yields one, i.e. when the working directory is a file system root. | dockerFallbackProject 是
// .yd.json 与工作目录都无法给出项目名时（即工作目录为文件系统根）使用的项目名。
const dockerFallbackProject = "app"

// dockerFlags keeps the docker command's flag set, installed by registerDockerFlags. | dockerFlags 保存
// docker 命令的标志集，由 registerDockerFlags 安装。
var dockerFlags *pflag.FlagSet

// dockerExecCommand overrides the docker CLI constructor; nil means exec.CommandContext. It is a
// variable so tests can inject a fake and never contact a real daemon. | dockerExecCommand 覆盖
// docker CLI 构造器；nil 表示 exec.CommandContext。它被声明为变量，便于测试注入假实现而永不接触真实守护进程。
var dockerExecCommand docker.ExecCommandFunc

// dockerPrepareAssets overrides the asset stage run before `docker build`; nil means the docker
// package's built-in Tailwind refresh, which --no-assets turns off. | dockerPrepareAssets 覆盖
// `docker build` 之前运行的资产阶段；nil 表示 docker 包内置的 Tailwind 刷新，--no-assets 会关闭它。
var dockerPrepareAssets func(ctx context.Context, dir string, log *ydlog.Logger) error

// dockerProjectMeta is the subset of .yd.json the docker command reads. | dockerProjectMeta 是 docker
// 命令读取的 .yd.json 子集。
type dockerProjectMeta struct {
	// Name is the project name, which is also the directory name. | Name 是项目名，同时也是目录名。
	Name string `json:"name"`
}

func init() {
	Register(Command{
		Name:  dockerCommandName,
		Short: "build the container image | 构建容器镜像",
		Long: "Resolve the multi-stage scratch Dockerfile, refresh the embedded assets and build the image\n" +
			"through the docker CLI, reporting the parsed image id.\n" +
			"解析多阶段 scratch Dockerfile、刷新内嵌资产，并经 docker CLI 构建镜像，随后报告解析出的镜像 ID。",
		Usage: dockerUsageLine,
		Flags: registerDockerFlags,
		Run:   runDocker,
	})
}

// registerDockerFlags declares the docker command's flags. | registerDockerFlags 声明 docker 命令的标志。
func registerDockerFlags(fs *pflag.FlagSet) {
	fs.String(flagDockerTag, "", "image reference, default <project>:<version> | 镜像引用，缺省为 <项目名>:<版本>")
	fs.String(flagDockerPlatform, "", "target platform, e.g. linux/amd64 | 目标平台，例如 linux/amd64")
	fs.Bool(flagDockerNoCache, false, "disable the docker build cache | 关闭 docker 构建缓存")
	fs.String(flagDockerFile, "", "Dockerfile path, default Dockerfile | Dockerfile 路径，缺省为 Dockerfile")
	fs.String(flagDockerTarget, "", "build stage to stop at | 停在该构建阶段")
	fs.Bool(flagDockerPush, false, "push the image after a successful build | 构建成功后推送镜像")
	fs.String(flagDockerBin, "", "docker CLI to invoke | 调用的 docker CLI")
	fs.Bool(flagDockerNoAssets, false, "skip the embedded asset refresh | 跳过内嵌资产刷新")
	dockerFlags = fs
}

// runDocker builds one image and prints what came out of it. The result is reported even when the
// build failed, because its log explains the failure. | runDocker 构建一个镜像并报告结果。即使构建失败也会
// 报告结果，因为其中的日志解释了失败原因。
func runDocker(ctx context.Context, env *Env) error {
	result, err := docker.BuildImage(ctx, dockerOptions(env, dockerFlags), env.FS, commandLogger(env))
	if result != nil {
		printDockerResult(env, result)
	}
	return err
}

// dockerOptions assembles the docker package options from the parsed flags. | dockerOptions 依据已解析的标志组装 docker 包选项。
func dockerOptions(env *Env, fs *pflag.FlagSet) docker.Options {
	o := docker.Options{
		Dir:           env.WorkDir,
		Dockerfile:    flagString(fs, flagDockerFile),
		Tag:           dockerTag(env, fs),
		Platform:      flagString(fs, flagDockerPlatform),
		NoCache:       flagBool(fs, flagDockerNoCache),
		Target:        flagString(fs, flagDockerTarget),
		Push:          flagBool(fs, flagDockerPush),
		DockerBin:     flagString(fs, flagDockerBin),
		WithAssets:    !flagBool(fs, flagDockerNoAssets),
		PrepareAssets: dockerPrepareAssets,
		ExecCommand:   dockerExecCommand,
	}
	// The Dockerfile turns YD_GODEBUG into the runtime ENV; only pass it when the
	// operator asked to force RFC 8441. | Dockerfile 把 YD_GODEBUG 写成运行时 ENV；
	// 仅在使用方要求强制 RFC 8441 时传入。
	if env.Cfg != nil && env.Cfg.GetBool("server.http2.extended_connect") {
		if o.BuildArgs == nil {
			o.BuildArgs = make(map[string]string, 1)
		}
		o.BuildArgs["YD_GODEBUG"] = "http2xconnect=1"
	}
	return o
}

// dockerTag resolves the image reference: --tag verbatim when it was given, otherwise
// <project>:<version> built from the project name and the stamped version. | dockerTag 解析镜像引用：
// 传了 --tag 就原样使用，否则用项目名与被注入的版本拼出 <项目名>:<版本>。
func dockerTag(env *Env, fs *pflag.FlagSet) string {
	if tag := strings.TrimSpace(flagString(fs, flagDockerTag)); tag != "" {
		return tag
	}
	return projectName(env, env.WorkDir) + ":" + dockerProjectVersion()
}

// dockerProjectVersion returns the version stamped into the default tag: the ldflags-injected
// version.Version, falling back to "dev" when nothing was injected. | dockerProjectVersion 返回写入缺省标签的
// 版本：由 ldflags 注入的 version.Version，未被注入时回退为 "dev"。
func dockerProjectVersion() string {
	if v := strings.TrimSpace(version.Version); v != "" {
		return v
	}
	return "dev"
}

// projectName derives the project name from the name recorded in .yd.json below dir, falling back
// to the directory name. It is shared by the docker command (default image tag) and the service
// command (service name and artifact name). | projectName 从 dir 之下 .yd.json 记录的名字推导项目名，
// 回退为目录名。它由 docker 命令（缺省镜像标签）与 service 命令（服务名与产物名）共用。
func projectName(env *Env, dir string) string {
	if name := projectMetaName(env, dir); name != "" {
		return name
	}
	return dirBaseName(dir)
}

// projectMetaName reads the project name recorded in .yd.json. An unreadable or nameless metadata
// file is not an error, it simply yields "", so the caller falls back to the directory name. | projectMetaName
// 读取 .yd.json 记录的项目名。元信息文件无法读取或未记录名字都不算错误，只返回 ""，由调用方回退到目录名。
func projectMetaName(env *Env, dir string) string {
	if env == nil || env.FS == nil {
		return ""
	}
	data, err := afero.ReadFile(env.FS, filepath.Join(dir, dockerProjectMetaName))
	if err != nil {
		return ""
	}
	var meta dockerProjectMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	name := strings.TrimSpace(meta.Name)
	// A hand-edited metadata file must not turn the project name into a path: it is used as one
	// element of bin/<name> and as an image/service name. Reject separators, a drive colon and the
	// dot names so the value always stays a single, local segment. | 手工编辑的元信息不得把项目名变成
	// 路径：它被用作 bin/<name> 的一个元素以及镜像/服务名。拒绝分隔符、盘符冒号与点号名，使取值始终是
	// 单个本地路径段。
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00") {
		return ""
	}
	return name
}

// dirBaseName returns the directory name of dir, resolving a relative dir against the process
// working directory so that the default tag stays stable. | dirBaseName 返回 dir 的目录名，相对路径会按进程
// 工作目录展开，使缺省标签保持稳定。
func dirBaseName(dir string) string {
	if dir == "" {
		dir = "."
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if base := filepath.Base(filepath.Clean(dir)); base != "" && base != "." && base != string(filepath.Separator) {
		return base
	}
	return dockerFallbackProject
}

// printDockerResult writes the image reference, the parsed image id and the log size. | printDockerResult
// 写出镜像引用、解析出的镜像 ID 与日志行数。
func printDockerResult(env *Env, result *docker.Result) {
	if result.Tag != "" {
		fmt.Fprintf(env.Out, "image | 镜像: %s\n", result.Tag)
	}
	if result.ImageID != "" {
		fmt.Fprintf(env.Out, "image id | 镜像 ID: %s\n", result.ImageID)
	}
	fmt.Fprintf(env.Out, "log lines | 日志行数: %d\n", len(result.Logs))
}
