// This file implements the build command: it owns flag parsing, target resolution, the version
// stamp injected through ldflags and the human-readable artifact report, and nothing else. The
// compilation itself lives in the build package. | 本文件实现 build 命令：只负责标志解析、目标解析、
// 经 ldflags 注入的版本戳与人类可读的产物报告，别无其它；编译本身由 build 包负责。

package cli

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/core/version"
	"github.com/shibingli/yd/tooling/build"
	"github.com/spf13/pflag"
)

// buildCommandName is the command word of the production build command. | buildCommandName 是生产构建命令的命令词。
const buildCommandName = "build"

// The build command's flag names. | build 命令的标志名。
const (
	// flagBuildOS selects the target operating system. | flagBuildOS 选择目标操作系统。
	flagBuildOS = "os"
	// flagBuildArch selects the target architecture. | flagBuildArch 选择目标架构。
	flagBuildArch = "arch"
	// flagBuildTargets lists comma separated GOOS/GOARCH pairs. | flagBuildTargets 列出以逗号分隔的 GOOS/GOARCH 组合。
	flagBuildTargets = "targets"
	// flagBuildAll expands to every platform a yd release covers. | flagBuildAll 展开为 yd 发布覆盖的全部平台。
	flagBuildAll = "all"
	// flagBuildOutput overrides the artifact path of a single-target build. | flagBuildOutput 覆盖单目标构建的产物路径。
	flagBuildOutput = "output"
	// flagBuildName overrides the artifact base name. | flagBuildName 覆盖产物基础名。
	flagBuildName = "name"
	// flagBuildTailwind runs the Tailwind CSS stage. | flagBuildTailwind 执行 Tailwind CSS 阶段。
	flagBuildTailwind = "tailwind"
	// flagBuildNoTailwind skips the Tailwind CSS stage, overriding --tailwind. | flagBuildNoTailwind 跳过 Tailwind CSS 阶段，优先于 --tailwind。
	flagBuildNoTailwind = "no-tailwind"
	// flagBuildMinify asks tailwindcss for the minified stylesheet. | flagBuildMinify 要求 tailwindcss 输出压缩后的样式表。
	flagBuildMinify = "minify"
	// flagBuildOffline forces -mod=vendor, so no module is fetched from the network. | flagBuildOffline 强制 -mod=vendor，不从网络拉取任何模块。
	flagBuildOffline = "offline"
	// flagBuildTags adds build tags. | flagBuildTags 追加构建标签。
	flagBuildTags = "tags"
	// flagBuildLDFlags appends extra linker flags after -s -w. | flagBuildLDFlags 在 -s -w 之后追加额外的链接参数。
	flagBuildLDFlags = "ldflags"
)

// buildUsageLine is the usage line of the build command. | buildUsageLine 是 build 命令的用法行。
const buildUsageLine = "yd build [--os GOOS] [--arch GOARCH] [--targets linux/amd64,linux/arm64] [--all] [flags]"

// buildAllTargets is the platform list --all expands to: the server platforms yd ships to plus
// the two desktop architectures. It is parsed by build.ParseTargets like any other list. | buildAllTargets
// 是 --all 展开的平台列表：yd 发布的服务端平台加两种桌面架构。它与其它列表一样由 build.ParseTargets 解析。
const buildAllTargets = "linux/amd64,linux/arm64,darwin/arm64,windows/amd64"

// keyLogFormat is the log format key consumed by commandLogger; it is the same key configureLogger
// reads. | keyLogFormat 是 commandLogger 消费的日志格式键，与 configureLogger 读取的键相同。
const keyLogFormat = "log.format"

// buildFlags keeps the build command's flag set, installed by registerBuildFlags. | buildFlags 保存 build
// 命令的标志集，由 registerBuildFlags 安装。
var buildFlags *pflag.FlagSet

// buildExecCommand overrides the toolchain constructor of the build command; nil means
// exec.CommandContext. It is a variable so tests can inject a fake and never compile anything for
// real. | buildExecCommand 覆盖 build 命令的工具链构造器；nil 表示 exec.CommandContext。它被声明为变量，
// 便于测试注入假实现而完全不执行真实编译。
var buildExecCommand build.ExecCommandFunc

// buildTailwindFunc overrides the stylesheet stage the build command injects; nil means the
// command's own skip-tolerant wrapper around build.BuildTailwind. | buildTailwindFunc 覆盖 build 命令注入的
// 样式表阶段；nil 表示使用命令自身的、容忍缺失的 build.BuildTailwind 包装。
var buildTailwindFunc build.TailwindFunc

func init() {
	Register(Command{
		Name:  buildCommandName,
		Short: "build the production binary | 构建生产二进制",
		Long: "Compile the production binary for one or more GOOS/GOARCH targets, inject the version\n" +
			"recorded by github.com/shibingli/yd/core/version through -ldflags and verify every artifact.\n" +
			"为一个或多个 GOOS/GOARCH 目标编译生产二进制，经 -ldflags 注入 github.com/shibingli/yd/core/version\n" +
			"记录的版本信息，并校验每个产物。",
		Usage: buildUsageLine,
		Flags: registerBuildFlags,
		Run:   runBuild,
	})
}

// registerBuildFlags declares the build command's flags. | registerBuildFlags 声明 build 命令的标志。
func registerBuildFlags(fs *pflag.FlagSet) {
	fs.String(flagBuildOS, "", "target operating system | 目标操作系统")
	fs.String(flagBuildArch, "", "target architecture | 目标架构")
	fs.String(flagBuildTargets, "", "comma separated GOOS/GOARCH list | 逗号分隔的 GOOS/GOARCH 列表")
	fs.Bool(flagBuildAll, false, "build "+buildAllTargets+" | 构建 "+buildAllTargets)
	fs.String(flagBuildOutput, "", "artifact path of a single-target build | 单目标构建的产物路径")
	fs.String(flagBuildName, "", "artifact base name | 产物基础名")
	fs.Bool(flagBuildTailwind, true, "run the Tailwind CSS stage | 执行 Tailwind CSS 阶段")
	fs.Bool(flagBuildNoTailwind, false, "skip the Tailwind CSS stage, overriding --tailwind | 跳过 Tailwind CSS 阶段，优先于 --tailwind")
	fs.Bool(flagBuildMinify, false, "produce the minified stylesheet (--tailwind) | 产出压缩后的样式表（--tailwind）")
	fs.Bool(flagBuildOffline, false, "force -mod=vendor and forbid network access | 强制 -mod=vendor 并禁止网络访问")
	fs.StringArray(flagBuildTags, nil, "build tags, repeatable | 构建标签，可重复")
	fs.StringArray(flagBuildLDFlags, nil, "extra linker flags, repeatable | 额外的链接参数，可重复")
	buildFlags = fs
}

// runBuild compiles every requested target and prints the artifact report. The error is returned
// untouched: cmd/yd renders it and picks the process exit code. | runBuild 编译全部请求的目标并打印产物报告。
// 错误原样返回：由 cmd/yd 渲染并决定进程退出码。
func runBuild(ctx context.Context, env *Env) error {
	log := commandLogger(env)
	opts, err := buildOptions(env, buildFlags, log)
	if err != nil {
		return err
	}
	report, err := build.Build(ctx, opts, log)
	if report != nil {
		printBuildReport(env, report)
	}
	return err
}

// buildOptions assembles the build package options from the parsed flags. The version triple comes
// straight from github.com/shibingli/yd/core/version, so a binary built by yd records which yd built it. |
// buildOptions 依据已解析的标志组装 build 包选项。版本三元组直接取自 github.com/shibingli/yd/core/version，
// 因此由 yd 构建出的二进制会记录构建它的 yd 版本。
func buildOptions(env *Env, fs *pflag.FlagSet, log *logx.Logger) (build.Options, error) {
	targets, err := buildTargets(fs)
	if err != nil {
		return build.Options{}, err
	}
	return build.Options{
		Dir:     env.WorkDir,
		Package: projectEntry(env, env.WorkDir),
		Output:  flagString(fs, flagBuildOutput),
		Name:    flagString(fs, flagBuildName),
		Targets: targets,
		LDFlags: flagStrings(fs, flagBuildLDFlags),
		Tags:    flagStrings(fs, flagBuildTags),
		Tailwind: build.TailwindOptions{
			Enabled: buildTailwindEnabled(fs),
			Minify:  flagBool(fs, flagBuildMinify),
		},
		Offline:      flagBool(fs, flagBuildOffline),
		TrimPath:     true,
		Version:      version.Version,
		Commit:       version.Commit,
		Date:         version.Date,
		TailwindFunc: buildTailwindStage(log),
		ExecCommand:  buildExecCommand,
	}, nil
}

// buildTargets resolves the target list from --targets, --all and --os/--arch, in that order of
// precedence. An omitted GOOS or GOARCH falls back to the host value, and a run without any target
// flag returns nil, which lets the build package default to the host platform. | buildTargets 按
// --targets、--all、--os/--arch 的优先级解析目标列表。缺失的 GOOS 或 GOARCH 回退为主机取值；未给任何目标标志时
// 返回 nil，由 build 包默认取主机平台。
func buildTargets(fs *pflag.FlagSet) ([]build.Target, error) {
	spec := strings.TrimSpace(flagString(fs, flagBuildTargets))
	switch {
	case flagBool(fs, flagBuildAll) && spec != "":
		return nil, errx.Detail(ErrFlagParse, "--all and --targets are mutually exclusive | --all 与 --targets 互斥")
	case flagBool(fs, flagBuildAll):
		spec = buildAllTargets
	case spec != "":
		// --os/--arch only fill in a missing half of the pair. | --os/--arch 只补全该组合中缺失的一半。
	default:
		goos, goarch := flagString(fs, flagBuildOS), flagString(fs, flagBuildArch)
		if goos == "" && goarch == "" {
			return nil, nil
		}
		if goos == "" {
			goos = runtime.GOOS
		}
		if goarch == "" {
			goarch = runtime.GOARCH
		}
		spec = goos + "/" + goarch
	}
	return build.ParseTargets(spec)
}

// buildTailwindEnabled reports whether the stylesheet stage runs. The stage is on by default;
// --tailwind=false or --no-tailwind turn it off, and --no-tailwind always wins when both are
// given. | buildTailwindEnabled 报告样式表阶段是否运行。该阶段默认开启；--tailwind=false 或
// --no-tailwind 会关闭它，两者同时出现时 --no-tailwind 优先。
func buildTailwindEnabled(fs *pflag.FlagSet) bool {
	enabled := true
	if flagChanged(fs, flagBuildTailwind) {
		enabled = flagBool(fs, flagBuildTailwind)
	}
	if flagBool(fs, flagBuildNoTailwind) {
		enabled = false
	}
	return enabled
}

// buildTailwindStage returns the stylesheet hook the build command injects. A project without a
// Tailwind stylesheet, or a machine without a tailwindcss binary, is not a build failure: the
// stage is skipped with a warning, because the compiled binary is still perfectly valid. | buildTailwindStage
// 返回 build 命令注入的样式表钩子。项目没有 Tailwind 样式表、或机器上没有 tailwindcss 二进制都不算构建失败：
// 该阶段会以告警跳过，因为编译出的二进制依然完全有效。
func buildTailwindStage(log *logx.Logger) build.TailwindFunc {
	if buildTailwindFunc != nil {
		return buildTailwindFunc
	}
	return func(ctx context.Context, dir string, o build.TailwindOptions) (int64, error) {
		size, err := build.BuildTailwind(ctx, dir, o, log)
		switch {
		case err == nil:
			return size, nil
		case errors.Is(err, build.ErrTailwindInput), errors.Is(err, build.ErrTailwindNotFound):
			log.Warn("tailwind stage skipped | 跳过 Tailwind 阶段", logx.Err(err))
			return 0, nil
		default:
			return 0, err
		}
	}
}

// printBuildReport writes one block per artifact plus the total count. | printBuildReport 为每个产物写一个文本块，并写出总数。
func printBuildReport(env *Env, report *build.Report) {
	for _, a := range report.Artifacts {
		fmt.Fprintf(env.Out, "artifact | 产物: %s\n", a.Path)
		fmt.Fprintf(env.Out, "  target | 目标: %s/%s\n", a.GOOS, a.GOARCH)
		fmt.Fprintf(env.Out, "  size | 体积: %s\n", formatSize(a.Bytes))
		fmt.Fprintf(env.Out, "  duration | 耗时: %s\n", a.Duration.Round(time.Millisecond))
	}
	fmt.Fprintf(env.Out, "built | 已构建: %d\n", len(report.Artifacts))
}

// commandLogger returns the logger a command records diagnostics through. The level and format are
// read from env.Cfg (log.level / log.format), i.e. the same keys Execute already maps onto the
// default logger; an env without configuration falls back to logx.Default(). | commandLogger 返回命令记录
// 诊断所用的日志器。级别与格式取自 env.Cfg（log.level / log.format），即 Execute 已经映射到默认日志器上的同
// 一批键；环境未携带配置时回退为 logx.Default()。
func commandLogger(env *Env) *logx.Logger {
	if env == nil || env.Cfg == nil {
		return logx.Default()
	}
	lvl, err := logx.ParseLevel(env.Cfg.GetString(keyLogLevel))
	if err != nil {
		return logx.Default()
	}
	return logx.New(logx.Options{
		Writer:  env.Err,
		Level:   lvl,
		Format:  env.Cfg.GetString(keyLogFormat),
		Service: "yd",
	})
}
