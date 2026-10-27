// This file implements the ko command: it owns flag parsing, the configuration lookup, the session
// summary and the error hints, and nothing else. The image build itself lives in the ko package. | 本文件
// 实现 ko 命令：只负责标志解析、配置查询、会话摘要与错误提示，别无其它；镜像构建本身由 ko 包负责。

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/shibingli/yd/core/envx"
	"github.com/shibingli/yd/tooling/build"
	"github.com/shibingli/yd/tooling/ko"
	"github.com/shibingli/yd/tooling/upx"
	ydlog "github.com/shibingli/ydx/log"
	"github.com/spf13/pflag"
)

// koCommandName is the command word of the Dockerfile-less image command. | koCommandName 是无 Dockerfile 镜像命令的命令词。
const koCommandName = "ko"

// The ko command's flag names. | ko 命令的标志名。
const (
	// flagKoRepo is the target repository, passed to ko through KO_DOCKER_REPO. | flagKoRepo 是目标仓库，
	// 经 KO_DOCKER_REPO 传给 ko。
	flagKoRepo = "repo"
	// flagKoTags lists the tags to publish; one value may hold a comma-separated list. | flagKoTags 列出要发布的标签；
	// 单个取值可自带逗号分隔的列表。
	flagKoTags = "tags"
	// flagKoPlatforms lists the platforms to build. | flagKoPlatforms 列出要构建的平台。
	flagKoPlatforms = "platforms"
	// flagKoBaseImage overrides the base image, passed to ko through KO_DEFAULTBASEIMAGE. | flagKoBaseImage
	// 覆盖基础镜像，经 KO_DEFAULTBASEIMAGE 传给 ko。
	flagKoBaseImage = "base-image"
	// flagKoPackage lists the import paths to build, repeatable. | flagKoPackage 列出要构建的导入路径，可重复。
	flagKoPackage = "package"
	// flagKoLocal loads the result into the local docker daemon instead of pushing it. | flagKoLocal 把结果载入
	// 本地 docker 守护进程而不推送。
	flagKoLocal = "local"
	// flagKoPush pushes the result to the repository. | flagKoPush 把结果推送到仓库。
	flagKoPush = "push"
	// flagKoBinary pins the ko executable and skips the resolution chain. | flagKoBinary 直接指定 ko 可执行文件
	// 并跳过解析链。
	flagKoBinary = "binary"
	// flagKoDir overrides the directory ko runs in, i.e. the build root. | flagKoDir 覆盖 ko 运行所在目录，
	// 即构建根目录。
	flagKoDir = "dir"
	// flagKoNoConfig suppresses packaging configs/config.yaml into the image. | flagKoNoConfig 关闭把
	// configs/config.yaml 打包进镜像。
	flagKoNoConfig = "no-config"
	// flagKoLDFlags overrides the linker flags passed to ko; the default keeps "-s -w". | flagKoLDFlags
	// 覆盖传给 ko 的链接参数；默认保留 "-s -w"。
	flagKoLDFlags = "ldflags"
	// flagKoNoUPX disables the UPX compression of the built binary. | flagKoNoUPX 关闭构建产物的 UPX 压缩。
	flagKoNoUPX = "no-upx"
)

// koUsageLine is the usage line of the ko command. | koUsageLine 是 ko 命令的用法行。
const koUsageLine = "yd ko [--repo <registry>/<team>/<app>] [--tags v1.2.3,latest] [--platforms linux/amd64,linux/arm64] [--base-image IMG]\n" +
	"      [--package ./cmd/app] [--local] [--push] [--no-config] [--ldflags \"-s -w\"] [--no-upx] [--binary PATH] [--dir DIR] [packages...]"

// koFlags keeps the ko command's flag set, installed by registerKoFlags. | koFlags 保存 ko 命令的标志集，
// 由 registerKoFlags 安装。
var koFlags *pflag.FlagSet

// koExecCommand overrides the ko runner; nil means the ko package's own runner, which executes the
// real CLI. It is a variable so tests can inject a fake and never start ko, reach a registry or touch
// the network. | koExecCommand 覆盖 ko 的执行器；nil 表示使用 ko 包自带的执行器（它会执行真实的 CLI）。
// 它被声明为变量，便于测试注入假实现而永不启动 ko、接触镜像仓库或网络。
var koExecCommand ko.ExecCommandFunc

// koUPXEnv builds the KO_GO_PATH wrapper environment for one run; it is a variable so tests can pin the
// child environment without resolving a real Go toolchain or extracting the embedded payload. | koUPXEnv
// 为一次运行构造 KO_GO_PATH 包装器环境；它是变量，便于测试固定子进程环境而无需解析真实 Go 工具链或解压嵌入 payload。
var koUPXEnv = resolveKoUPXEnv

func init() {
	Register(Command{
		Name:  koCommandName,
		Short: "build images straight from Go source, without a Dockerfile | 直接从 Go 源码构建镜像，无需 Dockerfile",
		Long: "Compile the packages with ko and publish the resulting images, so a project needs neither a\n" +
			"Dockerfile nor a container build daemon.\n" +
			"用 ko 编译包并发布产出的镜像，因此项目既不需要 Dockerfile 也不需要容器构建守护进程。\n" +
			"The repository and the base image are handed to ko through KO_DOCKER_REPO and KO_DEFAULTBASEIMAGE,\n" +
			"the interface ko itself documents; the image name is pinned to <repository>/<package base>.\n" +
			"仓库与基础镜像经 KO_DOCKER_REPO 与 KO_DEFAULTBASEIMAGE 交给 ko——那是 ko 自身文档给出的接口；\n" +
			"镜像名固定为 <仓库>/<包基础名>。\n" +
			"Values come from, highest priority first: flags, the environment (YD_KO_*), the config file\n" +
			"(ko.repository, ko.tags, ...) and the defaults, and packages may also be given as positional\n" +
			"arguments.\n" +
			"取值优先级由高到低：标志、环境变量（YD_KO_*）、配置文件（ko.repository、ko.tags 等）与默认值；\n" +
			"包也可以用位置参数给出。\n" +
			"Binaries are stripped by default with -ldflags \"-s -w\", matching yd docker; pass an empty\n" +
			"--ldflags to keep the symbol table and debug information.\n" +
			"二进制默认以 -ldflags \"-s -w\" 去符号，与 yd docker 一致；传空的 --ldflags 可保留符号表与调试信息。\n" +
			"Built binaries are UPX-compressed with the embedded UPX 5.2.1 unless --no-upx is passed.\n" +
			"构建产物默认经嵌入的 UPX 5.2.1 压缩，除非传入 --no-upx。\n" +
			"ko is an optional external tool: when it is missing the command prints an installation hint and\n" +
			"exits non-zero, without ever panicking.\n" +
			"ko 是可选的外部工具：缺失时命令打印安装提示并以非零码退出，绝不 panic。",
		Usage: koUsageLine,
		Flags: registerKoFlags,
		Run:   runKo,
	})
}

// registerKoFlags declares the ko command's flags. | registerKoFlags 声明 ko 命令的标志。
func registerKoFlags(fs *pflag.FlagSet) {
	fs.String(flagKoRepo, "", "target repository, e.g. registry.example.com/team/app | 目标仓库")
	fs.StringSlice(flagKoTags, nil, "tag to publish, repeatable and comma-separated | 要发布的标签，可重复且支持逗号分隔")
	fs.StringSlice(flagKoPlatforms, nil, "platform to build, e.g. linux/amd64, repeatable | 要构建的平台，例如 linux/amd64，可重复")
	fs.String(flagKoBaseImage, "", "base image override | 基础镜像覆盖")
	fs.StringSlice(flagKoPackage, nil, "import path to build, repeatable, default ./cmd/app | 要构建的导入路径，可重复，缺省 ./cmd/app")
	fs.Bool(flagKoLocal, false, "load the image into the local docker daemon, no push | 把镜像载入本地 docker 守护进程，不推送")
	fs.Bool(flagKoPush, false, "push the image to the repository | 把镜像推送到仓库")
	fs.String(flagKoBinary, "", "ko executable, skips the resolution chain | ko 可执行文件，跳过解析链")
	fs.String(flagKoDir, "", "directory ko runs in, default the working directory | ko 运行所在目录，缺省为工作目录")
	fs.Bool(flagKoNoConfig, false, "do not package configs/config.yaml into the image | 不把 configs/config.yaml 打包进镜像")
	fs.StringSlice(flagKoLDFlags, nil, "linker flags passed to ko, default \"-s -w\"; an empty value disables them | 传给 ko 的链接参数，默认 \"-s -w\"；传空值关闭")
	fs.Bool(flagKoNoUPX, false, "keep the built binary uncompressed (skip UPX) | 不压缩构建产物（跳过 UPX）")
	koFlags = fs
}

// runKo builds one image and prints what came out of it. The session summary is written before ko
// starts, so a long build shows what is being built, and the parsed references are reported even
// when the build failed, because their log explains the failure. | runKo 构建一个镜像并报告结果。会话摘要
// 在 ko 启动前写出，使耗时构建过程中可见正在构建的内容；即使构建失败也会报告解析出的引用，因为其中的日志解释了失败原因。
func runKo(ctx context.Context, env *Env) error {
	o := koOptions(env, koFlags)
	printKoSession(env, o)
	if msg := koHTTP2Warning(env); msg != "" {
		commandLogger(env).Warn(msg)
	}
	if msg := koEnvironmentWarning(env); msg != "" {
		commandLogger(env).Warn(msg)
	}
	result, err := ko.BuildImage(ctx, o, commandLogger(env))
	if result != nil {
		printKoResult(env, result)
	}
	if err != nil {
		printKoHint(env, err)
		return err
	}
	return nil
}

// koHTTP2Warning returns the deployment hint for the force switch, or "" when
// the switch is off. | koHTTP2Warning 在强制开关开启时返回部署提示，否则返回空串。
//
// ko has no flag for injecting environment variables; the Linux binary fixes that
// by re-executing itself, but other platforms must be configured by the manifest. | ko
// 没有注入环境变量的标志；Linux 二进制会通过重启自行修复，其他平台必须由部署清单配置。
func koHTTP2Warning(env *Env) string {
	if env == nil || env.Cfg == nil || !env.Cfg.GetBool("server.http2.extended_connect") {
		return ""
	}
	return "ko cannot inject environment variables; the Linux container binary re-executes itself with GODEBUG=http2xconnect=1, but non-Linux deployment manifests must set GODEBUG=http2xconnect=1 explicitly | ko 无法注入环境变量；Linux 容器内的二进制会自动以 GODEBUG=http2xconnect=1 重启，非 Linux 部署清单必须显式设置该变量"
}

// koEnvironmentWarning returns the production-environment hint, or "" when app.env is already production. |
// koEnvironmentWarning 返回生产环境提示；app.env 已是 production 时返回空串。
//
// ko 没有注入环境变量的标志，而 development 会让镜像使用源码资产、跳过不可变缓存，并在未配置
// seed.admin_password 时回退到文档化的开发口令，因此部署清单必须显式设置 YD_APP_ENV=production。 |
// ko has no flag for injecting environment variables, while development makes the image use source assets, skip
// the immutable cache, and fall back to the documented development seed password when seed.admin_password is
// unset, so the deployment manifest must set YD_APP_ENV=production explicitly.
func koEnvironmentWarning(env *Env) string {
	if env == nil || env.Cfg == nil || envx.IsProduction(env.Cfg.GetString("app.env")) {
		return ""
	}
	return "ko cannot inject environment variables; set YD_APP_ENV=production in the deployment manifest, otherwise the image runs in development with source assets and the documented seed password | ko 无法注入环境变量；请在部署清单中设置 YD_APP_ENV=production，否则镜像会以开发环境运行（源码资产与文档化种子口令）"
}

// koOptions assembles the ko package options from the parsed flags and the layered configuration. | koOptions
// 依据已解析的标志与分层配置组装 ko 包选项。
//
// Precedence follows the repository rule 命令行参数 > 环境变量（含 .env）> 配置文件 > 框架默认值: the
// environment layer is already applied inside OptionsFromConfig (YD_KO_REPOSITORY and friends map onto
// the ko.* keys), and a flag overwrites a value only when it was actually passed, so `--local=false`
// can still turn off a ko.local=true from the config file. | 优先级遵循仓库规则「命令行参数 > 环境变量（含
// .env）> 配置文件 > 框架默认值」：环境层已由 OptionsFromConfig 施加（YD_KO_REPOSITORY 等映射到 ko.* 键），
// 且标志只在确实被传入时才覆盖取值，因此 `--local=false` 依然可以关掉配置文件里的 ko.local=true。
func koOptions(env *Env, fs *pflag.FlagSet) ko.Options {
	o := ko.OptionsFromConfig(env.Cfg, koWorkDir(env, fs))
	if flagChanged(fs, flagKoRepo) {
		o.Repository = strings.TrimSpace(flagString(fs, flagKoRepo))
	}
	if flagChanged(fs, flagKoTags) {
		o.Tags = flagStringSlice(fs, flagKoTags)
	}
	if flagChanged(fs, flagKoPlatforms) {
		o.Platforms = flagStringSlice(fs, flagKoPlatforms)
	}
	if flagChanged(fs, flagKoBaseImage) {
		o.BaseImage = strings.TrimSpace(flagString(fs, flagKoBaseImage))
	}
	if flagChanged(fs, flagKoLDFlags) {
		// An explicitly empty slice disables the default strip flags, so it must stay non-nil. |
		// 显式空切片会关闭默认去符号参数，因此必须保持非 nil。
		o.LDFlags = flagStringSlice(fs, flagKoLDFlags)
		if o.LDFlags == nil {
			o.LDFlags = []string{}
		}
	}
	if flagChanged(fs, flagKoBinary) {
		o.Binary = strings.TrimSpace(flagString(fs, flagKoBinary))
	}
	if flagChanged(fs, flagKoLocal) {
		o.Local = flagBool(fs, flagKoLocal)
	}
	if flagChanged(fs, flagKoPush) {
		o.Push = flagBool(fs, flagKoPush)
	}
	if packages := koPackages(fs); len(packages) > 0 {
		o.Packages = packages
	}
	o.SkipConfig = flagBool(fs, flagKoNoConfig)
	o.Exec = koExecCommand
	o.Embedded = true
	if koUPXEnabled(env, fs) {
		o.Env = koUPXEnv(env, o.WorkingDir)
	}
	// 与 yd build / yd docker 同一套资产阶段：ko 直接编译源码树，若不先产出 yd.css 与 .min 同族文件，
	// 干净检出会回退到框架样式表并发送未压缩脚本。 |
	// The same asset stages as yd build / yd docker: ko compiles the source tree directly, so without producing
	// yd.css and the .min siblings first a clean checkout falls back to the framework stylesheet and ships
	// unminified scripts.
	o.PrepareAssets = build.PrepareAssets
	return o
}

// koPackages returns the import paths of the run: every --package value followed by the positional
// arguments, in the order given. | koPackages 返回本次运行的导入路径：先是全部 --package 取值，随后是位置参数，
// 顺序保持输入顺序。
func koPackages(fs *pflag.FlagSet) []string {
	packages := flagStringSlice(fs, flagKoPackage)
	if fs == nil {
		return packages
	}
	return append(packages, fs.Args()...)
}

// koUPXEnabled reports whether the UPX step runs: --no-upx wins, then upx.enabled, default true. | koUPXEnabled
// 报告是否执行 UPX 步骤：--no-upx 优先，其次 upx.enabled，默认开启。
func koUPXEnabled(env *Env, fs *pflag.FlagSet) bool {
	if flagBool(fs, flagKoNoUPX) {
		return false
	}
	if env != nil && env.Cfg != nil && env.Cfg.IsSet("upx.enabled") {
		return env.Cfg.GetBool("upx.enabled")
	}
	return true
}

// resolveKoUPXEnv resolves the UPX binary, the real Go toolchain and this executable, and returns the
// KO_GO_PATH wrapper environment. An explicit KO_GO_PATH is respected as the real Go target. | resolveKoUPXEnv
// 解析 UPX、真实 Go 工具链与本可执行文件，返回 KO_GO_PATH 包装器环境。显式 KO_GO_PATH 会被尊重为真实 Go 目标。
func resolveKoUPXEnv(env *Env, workDir string) map[string]string {
	if env == nil {
		return nil
	}
	explicit := ""
	if env.Cfg != nil {
		explicit = strings.TrimSpace(env.Cfg.GetString("upx.bin"))
	}
	bin, err := upx.Resolve(upx.Options{Binary: explicit, Dir: workDir, Materialize: true})
	if err != nil {
		commandLogger(env).Warn("ko: UPX unavailable; the binary stays uncompressed | ko：UPX 不可用，二进制保持未压缩",
			ydlog.Err(err))
		return nil
	}
	goBin := strings.TrimSpace(os.Getenv("KO_GO_PATH"))
	if goBin == "" {
		goBin, err = exec.LookPath("go")
		if err != nil {
			commandLogger(env).Warn("ko: the go toolchain was not found; skipping UPX | ko：未找到 go 工具链，跳过 UPX",
				ydlog.Err(err))
			return nil
		}
	}
	executable, err := os.Executable()
	if err != nil {
		commandLogger(env).Warn("ko: cannot locate the yd executable; skipping UPX | ko：无法定位 yd 可执行文件，跳过 UPX",
			ydlog.Err(err))
		return nil
	}
	return upx.WrapperEnvironment(executable, goBin, bin, upx.MaxSize)
}

// koWorkDir resolves the directory ko runs in: --dir when it was given (relative values resolve
// against the CLI working directory), otherwise the CLI working directory itself. | koWorkDir 解析
// ko 运行所在目录：传了 --dir 就用它（相对路径按 CLI 工作目录展开），否则就是 CLI 工作目录本身。
func koWorkDir(env *Env, fs *pflag.FlagSet) string {
	dir := strings.TrimSpace(flagString(fs, flagKoDir))
	if dir == "" {
		return env.WorkDir
	}
	return resolveUserPath(env.WorkDir, dir)
}

// printKoSession writes the bilingual session summary before ko starts. | printKoSession 在 ko 启动前写出双语会话摘要。
//
// It echoes the effective options - the ones that come from flags, environment and config file alike -
// so a user can see the repository and the mode that are about to be used. | 它回显生效的选项——无论它们来自
// 标志、环境变量还是配置文件——使用户能看到即将使用的仓库与模式。
func printKoSession(env *Env, o ko.Options) {
	fmt.Fprintf(env.Out, "package | 包: %s\n", strings.Join(koSessionPackages(o), " "))
	fmt.Fprintf(env.Out, "repository | 仓库: %s\n", koDash(o.Repository))
	fmt.Fprintf(env.Out, "tags | 标签: %s\n", koDash(strings.Join(o.Tags, ",")))
	fmt.Fprintf(env.Out, "platforms | 平台: %s\n", koDash(strings.Join(o.Platforms, ",")))
	fmt.Fprintf(env.Out, "base image | 基础镜像: %s\n", koDash(o.BaseImage))
	fmt.Fprintf(env.Out, "ldflags | 链接参数: %s\n", koLDFlagsLabel(o))
	fmt.Fprintf(env.Out, "config | 配置: %s\n", koConfigLabel(o))
	fmt.Fprintf(env.Out, "mode | 模式: %s\n", koMode(o))
}

// koConfigLabel renders the configuration packaged into the image, or a dash when staging is off. | koConfigLabel
// 渲染打包进镜像的配置；关闭暂存时显示短横线。
func koConfigLabel(o ko.Options) string {
	if o.SkipConfig {
		return "-"
	}
	return ko.DefaultConfigDir + "/config.yaml"
}

// koLDFlagsLabel renders the effective linker flags, or a dash when they were disabled. | koLDFlagsLabel
// 渲染实际生效的链接参数；关闭时显示短横线。
func koLDFlagsLabel(o ko.Options) string {
	return koDash(strings.Join(o.EffectiveLDFlags(), " "))
}

// koSessionPackages renders the package list of the session summary, defaulting to the layout
// `yd new` scaffolds. | koSessionPackages 渲染会话摘要中的包列表，缺省为 `yd new` 脚手架生成的布局。
func koSessionPackages(o ko.Options) []string {
	if len(o.Packages) == 0 {
		return []string{ko.DefaultPackage}
	}
	return o.Packages
}

// koMode renders the publish mode of the run: ko treats --local and --push as independent switches,
// so both can be on at once. | koMode 渲染本次运行的发布模式：ko 把 --local 与 --push 视为互相独立的开关，
// 因此两者可以同时开启。
func koMode(o ko.Options) string {
	switch {
	case o.Local && o.Push:
		return "local daemon + push | 载入本地守护进程并推送"
	case o.Local:
		return "local daemon | 载入本地守护进程"
	case o.Push:
		return "push | 推送到仓库"
	default:
		return "build only | 仅构建"
	}
}

// koDash renders an unset value as a dash, so the summary always has a value to show. | koDash 把未设置的取值渲染为短横线，使摘要始终有值可显示。
func koDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

// printKoResult writes the image reference, the digest and the log size. | printKoResult 写出镜像引用、摘要与日志行数。
func printKoResult(env *Env, result *ko.Result) {
	if result.Reference != "" {
		fmt.Fprintf(env.Out, "image | 镜像: %s\n", result.Reference)
	}
	if result.Digest != "" {
		fmt.Fprintf(env.Out, "digest | 摘要: %s\n", result.Digest)
	}
	if len(result.Images) > 1 {
		fmt.Fprintf(env.Out, "images | 镜像数: %d\n", len(result.Images))
	}
	fmt.Fprintf(env.Out, "log lines | 日志行数: %d\n", len(result.Logs))
}

// printKoHint renders the recovery hint of a failed run. | printKoHint 渲染失败运行的可恢复提示。
//
// A missing ko never panics and never blocks anything: it is rendered as an actionable installation
// hint and returned so that cmd/yd exits non-zero. Every other failure prints the sentinel error,
// whose detail already names the cause - for --local that includes an unreachable docker daemon. | ko
// 缺失绝不 panic 也不阻塞任何东西：它被渲染为可执行的安装提示并返回，由 cmd/yd 以非零码退出。其余失败打印
// 哨兵错误，其细节已经点出原因——对 --local 而言包括 docker 守护进程不可达。
func printKoHint(env *Env, err error) {
	switch {
	case errors.Is(err, ko.ErrBinaryNotFound):
		fmt.Fprintf(env.Err, "yd ko: ko not found | yd ko：未找到 ko\n")
		fmt.Fprintf(env.Err, "  install | 安装: %s\n", ko.InstallHint)
		fmt.Fprintf(env.Err, "  or drop the binary into %s | 或把二进制放入 %s\n", ko.LocalToolDir, ko.LocalToolDir)
	case errors.Is(err, ko.ErrNoRepository):
		fmt.Fprintf(env.Err, "yd ko: no repository configured | yd ko：未配置镜像仓库\n")
		fmt.Fprintf(env.Err, "  pass --repo registry.example.com/team/app or set ko.repository / KO_DOCKER_REPO | 请用 --repo registry.example.com/team/app 指定仓库，或配置 ko.repository / KO_DOCKER_REPO\n")
		fmt.Fprintf(env.Err, "  or pass --local to load the image into the local docker daemon | 或用 --local 把镜像载入本地 docker 守护进程\n")
	default:
		fmt.Fprintf(env.Err, "yd ko: %v\n", err)
	}
}
