// This file implements the service command: it owns flag parsing, the derivation of the service
// name and of the target binary, the single service manager action and the human-readable report,
// and nothing else. The platform specific service management itself lives in the service
// package. | 本文件实现 service 命令：只负责标志解析、服务名与目标程序的推导、单次服务管理器动作与
// 人类可读的报告，别无其它；平台相关的服务管理本身由 service 包负责。
//
// Two derivations are worth spelling out: | 有两处推导值得说明：
//
//   - the service name is --name, then service.name of the configuration, then the name recorded
//     in .yd.json, then the project directory name; app.name is deliberately NOT consulted, because
//     the framework defaults always fill it with "yd" and a project would otherwise register a
//     service called "yd"; | 服务名的顺序是 --name、配置中的 service.name、.yd.json 记录的名字、
//     项目目录名；刻意不读取 app.name，因为框架默认值总会把它填成 "yd"，否则项目会注册出名为
//     "yd" 的服务；
//   - the target binary is --binary, else the build artifact of the project
//     (bin/<project>[.exe]), else a one-shot build through the build package. | 目标程序顺序是
//     --binary、项目的构建产物（bin/<项目名>[.exe]）、经 build 包一次性编译。

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shibingli/yd/core/version"
	"github.com/shibingli/yd/runtime/service"
	"github.com/shibingli/yd/tooling/build"
	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
	"github.com/spf13/pflag"
)

// serviceCommandName is the command word of the system service command. | serviceCommandName 是系统服务命令的命令词。
const serviceCommandName = "service"

// The service command's flag names. | service 命令的标志名。
const (
	// flagServiceName overrides the service name. | flagServiceName 覆盖服务名。
	flagServiceName = "name"
	// flagServiceDisplayName overrides the display name. | flagServiceDisplayName 覆盖显示名。
	flagServiceDisplayName = "display-name"
	// flagServiceDescription overrides the service description. | flagServiceDescription 覆盖服务描述。
	flagServiceDescription = "description"
	// flagServiceBinary overrides the executable the service runs. | flagServiceBinary 覆盖服务运行的程序。
	flagServiceBinary = "binary"
	// flagServiceDir overrides the project directory the artifact is resolved in. | flagServiceDir 覆盖解析产物的项目目录。
	flagServiceDir = "dir"
	// flagServiceUser overrides the account the service runs as. | flagServiceUser 覆盖服务运行所用的账号。
	flagServiceUser = "user"
	// flagServiceArgs overrides the arguments passed to the executable, as one shell-like string. | flagServiceArgs 以单个类 shell 字符串覆盖传给程序的参数。
	flagServiceArgs = "args"
)

// serviceUsageLine is the usage line of the service command. | serviceUsageLine 是 service 命令的用法行。
const serviceUsageLine = "yd service <install|uninstall|start|stop|restart|status> [--name N] [--binary PATH] [flags]"

// serviceBinDir is the directory holding the single-target build artifact. | serviceBinDir 是存放单目标构建产物的目录。
const serviceBinDir = "bin"

// serviceEntryDir and serviceEntryFile locate the conventional main package of a scaffolded
// project, i.e. the entry a one-shot build compiles. | serviceEntryDir 与 serviceEntryFile 定位脚手架项目的约定主包，
// 即一次性编译所构建的入口。
const (
	serviceEntryDir  = "cmd/app"
	serviceEntryFile = "main.go"
)

// serviceDefaultEntry is the build entry used when the project has no cmd/app main package. | serviceDefaultEntry 是项目没有 cmd/app 主包时使用的构建入口。
const serviceDefaultEntry = "."

// serviceExeSuffix is the artifact suffix of a Windows build, i.e. the second artifact candidate. | serviceExeSuffix 是 Windows 构建产物的后缀，也是第二个候选产物路径。
const serviceExeSuffix = ".exe"

// serviceFlags keeps the service command's flag set, installed by registerServiceFlags. | serviceFlags 保存
// service 命令的标志集，由 registerServiceFlags 安装。
var serviceFlags *pflag.FlagSet

// serviceControlFunc performs one service manager action; nil means service.Control. It is a
// variable so tests inject a recorder and never register, start or stop a real system service. | serviceControlFunc
// 执行一次服务管理器动作；nil 表示 service.Control。它被声明为变量，使测试注入记录器而永不真实注册、
// 启动或停止系统服务。
var serviceControlFunc func(o service.Options, a service.Action) (string, error)

// serviceBuildFunc compiles the artifact when it is missing; nil means the build package's own
// pipeline. It is a variable so tests never compile anything for real. | serviceBuildFunc 在产物缺失时编译产物；
// nil 表示使用 build 包自身的流水线。它被声明为变量，使测试永不执行真实编译。
var serviceBuildFunc func(ctx context.Context, o build.Options, log *ydlog.Logger) (string, error)

// ErrServiceBinary reports a service executable that could not be resolved or produced. | ErrServiceBinary 表示无法解析或产出服务程序。
var ErrServiceBinary = errs.New("cli: cannot resolve the service executable | cli：无法解析服务程序")

func init() {
	Register(Command{
		Name:  serviceCommandName,
		Short: "manage the system service | 管理系统服务",
		Long: "Install, uninstall, start, stop, restart and query the system service of this project.\n" +
			"Windows uses the Service Control Manager, Linux uses systemd and macOS uses launchd;\n" +
			"installing or changing a service requires Administrator (Windows) or root (Linux/macOS) rights.\n" +
			"安装、卸载、启动、停止、重启与查询本项目的系统服务。Windows 使用服务控制管理器，Linux 使用\n" +
			"systemd，macOS 使用 launchd；安装或变更服务需要管理员（Windows）或 root（Linux/macOS）权限。",
		Usage: serviceUsageLine,
		Flags: registerServiceFlags,
		Run:   runService,
		Positionals: []Positional{{
			Name:        "action",
			Description: "service action: install/uninstall/start/stop/restart/status | 服务动作",
			Values: []string{
				string(service.ActionInstall),
				string(service.ActionUninstall),
				string(service.ActionStart),
				string(service.ActionStop),
				string(service.ActionRestart),
				string(service.ActionStatus),
			},
		}},
	})
}

// registerServiceFlags declares the service command's flags. | registerServiceFlags 声明 service 命令的标志。
func registerServiceFlags(fs *pflag.FlagSet) {
	fs.String(flagServiceName, "", "service name, default: service.name, .yd.json, directory name | 服务名，缺省取 service.name、.yd.json、目录名")
	fs.String(flagServiceDisplayName, "", "display name shown by the service manager | 服务管理器显示的显示名")
	fs.String(flagServiceDescription, "", "service description | 服务描述")
	fs.String(flagServiceBinary, "", "executable the service runs | 服务运行的程序")
	fs.String(flagServiceDir, "", "project directory, default: the working directory | 项目目录，缺省为工作目录")
	fs.String(flagServiceUser, "", "account the service runs as | 服务运行所用的账号")
	fs.String(flagServiceArgs, "", "arguments of the executable, e.g. \"-service run --config /opt/app/config.yaml\" | 程序参数，例如 \"-service run --config /opt/app/config.yaml\"")
	serviceFlags = fs
}

// runService performs exactly one service manager action and prints the bilingual report. | runService
// 只执行一次服务管理器动作，并打印双语报告。
func runService(ctx context.Context, env *Env) error {
	fs := serviceFlags
	args := fs.Args()
	if len(args) == 0 {
		printServiceUsage(env.Out)
		return ErrNoCommand
	}

	action, ok := serviceAction(strings.ToLower(strings.TrimSpace(args[0])))
	if !ok {
		printServiceUnknownAction(env, args[0])
		return errs.Detail(service.ErrUnknownAction, "%q", args[0])
	}

	opts, err := serviceOptions(ctx, env, fs, action)
	if err != nil {
		return err
	}

	result, err := controlService(opts, action)
	if err != nil {
		printServiceHint(env, err)
		return err
	}

	printServiceResult(env, opts, action, result)
	return nil
}

// serviceAction maps a command line action word onto a service manager action. The run action is
// deliberately absent: it selects the hosting path of the application binary and is never driven by
// this command. | serviceAction 把命令行动作词映射为服务管理器动作。run 动作刻意不在其中：它选择应用二进制的
// 托管路径，绝不由本命令驱动。
func serviceAction(word string) (service.Action, bool) {
	switch service.Action(word) {
	case service.ActionInstall, service.ActionUninstall, service.ActionStart,
		service.ActionStop, service.ActionRestart, service.ActionStatus:
		return service.Action(word), true
	default:
		return "", false
	}
}

// printServiceUnknownAction writes the unknown-action hint, the usage line and, for the run action,
// the explanation that the application binary owns it. | printServiceUnknownAction 写出未知动作提示、用法行，
// 并在动作为 run 时说明它由应用二进制负责。
func printServiceUnknownAction(env *Env, word string) {
	fmt.Fprintf(env.Err, "yd service: unknown action %q | yd service：未知动作 %q\n", word, word)
	if service.Action(strings.ToLower(strings.TrimSpace(word))) == service.ActionRun {
		fmt.Fprintf(env.Err, "hint | 提示: the run action belongs to the application binary: run it with \"-service run\" | "+
			"run 动作属于应用二进制：请以 \"-service run\" 运行它\n")
	}
	printServiceUsage(env.Err)
}

// serviceOptions assembles the service package options for one action. The structural fields
// (working directory, dependencies, environment variables, restart policy) come straight from the
// service section of the configuration; the identity fields (name, display name, description,
// account, arguments, executable) are resolved here, see the file header for their precedence. |
// serviceOptions 为一次动作组装 service 包选项。结构化字段（工作目录、依赖、环境变量、重启策略）直接取自
// 配置的 service 段；身份字段（服务名、显示名、描述、账号、参数、程序）在此解析，优先级见文件头说明。
func serviceOptions(ctx context.Context, env *Env, fs *pflag.FlagSet, action service.Action) (service.Options, error) {
	opts := service.OptionsFromConfig(env.Cfg)

	name := serviceName(env, fs)
	opts.Name = name
	opts.DisplayName = serviceFirstNonEmpty(
		flagString(fs, flagServiceDisplayName),
		serviceConfigString(env, service.KeyDisplayName),
		name,
	)
	opts.Description = serviceFirstNonEmpty(
		flagString(fs, flagServiceDescription),
		serviceConfigString(env, service.KeyDescription),
		opts.DisplayName,
	)
	opts.UserName = serviceFirstNonEmpty(
		flagString(fs, flagServiceUser),
		serviceConfigString(env, service.KeyUserName),
	)
	if args, ok := serviceArguments(fs); ok {
		opts.Arguments = args
	}

	binary, err := serviceBinary(ctx, env, fs, action)
	if err != nil {
		return service.Options{}, err
	}
	opts.Executable = binary
	return opts, nil
}

// serviceName resolves the service name: --name, then service.name, then the project name. | serviceName
// 解析服务名：--name、service.name、项目名。
func serviceName(env *Env, fs *pflag.FlagSet) string {
	return serviceFirstNonEmpty(
		flagString(fs, flagServiceName),
		serviceConfigString(env, service.KeyName),
		projectName(env, serviceDir(env, fs)),
	)
}

// serviceConfigString reads one trimmed configuration string, tolerating a missing configuration. | serviceConfigString
// 读取一个去空白的配置字符串，容忍配置缺失。
func serviceConfigString(env *Env, key string) string {
	if env == nil || env.Cfg == nil {
		return ""
	}
	return strings.TrimSpace(env.Cfg.GetString(key))
}

// serviceFirstNonEmpty returns the first trimmed non-empty value. | serviceFirstNonEmpty 返回第一个去空白后非空的取值。
func serviceFirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// serviceArguments reads the --args override; ok is false when the flag was not passed, in which
// case the configured arguments are kept. | serviceArguments 读取 --args 覆盖值；未传入该标志时 ok 为 false，
// 此时保留配置中的参数。
func serviceArguments(fs *pflag.FlagSet) ([]string, bool) {
	if !flagChanged(fs, flagServiceArgs) {
		return nil, false
	}
	return splitServiceArgs(flagString(fs, flagServiceArgs)), true
}

// splitServiceArgs splits a shell-like argument string on unquoted whitespace. Single and double
// quotes group a value, an empty quoted pair yields an empty argument, and no escape sequence is
// interpreted, because the value reaches the service manager verbatim. | splitServiceArgs 按未加引号的空白切分类 shell
// 参数串。单引号与双引号用于成组，一对空引号产出空参数，且不解释任何转义序列，因为该取值会原样交给服务管理器。
func splitServiceArgs(value string) []string {
	var (
		args    []string
		current strings.Builder
		quote   rune
		inField bool
	)

	for _, r := range value {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			inField = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inField {
				args = append(args, current.String())
				current.Reset()
				inField = false
			}
		default:
			current.WriteRune(r)
			inField = true
		}
	}
	if inField {
		args = append(args, current.String())
	}
	return args
}

// serviceBinary resolves the executable the service runs: the --binary value, else the build
// artifact of the project, else a one-shot build. Only an install registers the path with the
// service manager, so the other actions skip the derivation unless --binary was given explicitly. |
// serviceBinary 解析服务运行的程序：--binary 取值、项目的构建产物、一次性编译。只有安装会把该路径登记到
// 服务管理器，因此其它动作在未显式传入 --binary 时跳过推导。
func serviceBinary(ctx context.Context, env *Env, fs *pflag.FlagSet, action service.Action) (string, error) {
	if given := strings.TrimSpace(flagString(fs, flagServiceBinary)); given != "" {
		path := serviceResolvePath(env, given)
		if !serviceIsFile(env, path) {
			return "", errs.Detail(ErrServiceBinary, "%[1]q does not exist | %[1]q 不存在", path)
		}
		return path, nil
	}

	if action != service.ActionInstall {
		return "", nil
	}

	dir := serviceDir(env, fs)
	project := projectName(env, dir)
	for _, candidate := range serviceArtifacts(dir, project) {
		if serviceIsFile(env, candidate) {
			return candidate, nil
		}
	}
	return serviceBuildArtifact(ctx, env, dir, project)
}

// serviceDir returns the project directory the artifact is resolved in: --dir, else the working
// directory. | serviceDir 返回解析产物所用的项目目录：--dir，否则工作目录。
func serviceDir(env *Env, fs *pflag.FlagSet) string {
	if dir := strings.TrimSpace(flagString(fs, flagServiceDir)); dir != "" {
		if env != nil {
			return resolveUserPath(env.WorkDir, dir)
		}
		return resolveUserPath("", dir)
	}
	if env != nil && env.WorkDir != "" {
		return env.WorkDir
	}
	return "."
}

// serviceArtifacts lists the single-target artifacts of the project, in the order they are
// probed: bin/<project> then bin/<project>.exe. | serviceArtifacts 按探测顺序列出项目的单目标产物：
// bin/<项目名>，然后是 bin/<项目名>.exe。
func serviceArtifacts(dir, project string) []string {
	base := filepath.Join(dir, serviceBinDir, project)
	return []string{base, base + serviceExeSuffix}
}

// serviceResolvePath resolves a relative path against the working directory. | serviceResolvePath 把相对路径按工作目录展开。
func serviceResolvePath(env *Env, path string) string {
	if path == "" {
		return path
	}
	if env != nil {
		return resolveUserPath(env.WorkDir, path)
	}
	return resolveUserPath("", path)
}

// serviceIsFile reports whether path exists and is a regular file. | serviceIsFile 报告 path 是否存在且为普通文件。
func serviceIsFile(env *Env, path string) bool {
	if env == nil || env.FS == nil {
		return false
	}
	info, err := env.FS.Stat(path)
	return err == nil && !info.IsDir()
}

// serviceBuildArtifact compiles the project once because no artifact was found, and returns the
// path of the produced file. | serviceBuildArtifact 在未找到产物时把项目编译一次，并返回产出文件的路径。
func serviceBuildArtifact(ctx context.Context, env *Env, dir, project string) (string, error) {
	log := commandLogger(env)
	opts := serviceBuildOptions(env, dir, project, log)

	buildFn := serviceBuildFunc
	if buildFn == nil {
		buildFn = serviceDefaultBuild
	}
	artifact, err := buildFn(ctx, opts, log)
	if err != nil {
		return "", errs.WrapDetail(err, ErrServiceBinary,
			"cannot build %[1]s; run `yd build` first or pass --binary PATH | 无法构建 %[1]s；请先运行 `yd build` 或传入 --binary PATH", project)
	}
	if artifact == "" {
		return "", errs.Detail(ErrServiceBinary,
			"the build of %[1]s produced no artifact | %[1]s 的构建未产出任何产物", project)
	}
	return artifact, nil
}

// serviceBuildOptions assembles the one-shot build of the service artifact. The entry is the
// conventional cmd/app main package of a scaffolded project, falling back to the module root. | serviceBuildOptions
// 组装服务程序的一次性构建。构建入口是脚手架项目约定的 cmd/app 主包，否则回退为模块根。
func serviceBuildOptions(env *Env, dir, project string, log *ydlog.Logger) build.Options {
	return build.Options{
		Dir:          dir,
		Name:         project,
		Package:      projectEntry(env, dir),
		Tailwind:     build.TailwindOptions{Enabled: true},
		TrimPath:     true,
		Version:      version.Version,
		Commit:       version.Commit,
		Date:         version.Date,
		TailwindFunc: buildTailwindStage(log),
		ExecCommand:  buildExecCommand,
	}
}

// projectEntry returns the Go build entry of the project rooted at dir: ./cmd/app when that
// scaffolded main package exists, else the module root, which is what a framework library or a
// hand-written project uses. yd dev, yd build and the service command all compile through it, so
// every command agrees on where the program starts. | projectEntry 返回 dir 下项目的 Go 构建入口：
// 存在脚手架主包时为 ./cmd/app，否则为模块根，即框架库或手写项目的形态。yd dev、yd build 与 service
// 命令都经它选择入口，因此各命令对「程序从哪里开始」的判断保持一致。
func projectEntry(env *Env, dir string) string {
	if serviceIsFile(env, filepath.Join(dir, serviceEntryDir, serviceEntryFile)) {
		return "./" + serviceEntryDir
	}
	return serviceDefaultEntry
}

// serviceDefaultBuild runs the build package and returns the first artifact. | serviceDefaultBuild 运行 build 包并返回第一个产物。
func serviceDefaultBuild(ctx context.Context, o build.Options, log *ydlog.Logger) (string, error) {
	report, err := build.Build(ctx, o, log)
	if err != nil {
		return "", err
	}
	if report == nil || len(report.Artifacts) == 0 {
		return "", errs.Detail(ErrServiceBinary, "the build produced no artifact | 构建未产出任何产物")
	}
	return report.Artifacts[0].Path, nil
}

// controlService performs the action through the injected control point, defaulting to
// service.Control. | controlService 经注入的控制点执行动作，缺省为 service.Control。
func controlService(o service.Options, a service.Action) (string, error) {
	if serviceControlFunc != nil {
		return serviceControlFunc(o, a)
	}
	return service.Control(o, a)
}

// printServiceResult writes the resolved service, the action and the result of the action. | printServiceResult
// 写出解析后的服务、动作与该动作的结果。
func printServiceResult(env *Env, opts service.Options, action service.Action, result string) {
	fmt.Fprintf(env.Out, "service | 服务: %s\n", opts.Name)
	if opts.DisplayName != "" {
		fmt.Fprintf(env.Out, "  display name | 显示名: %s\n", opts.DisplayName)
	}
	if opts.Description != "" {
		fmt.Fprintf(env.Out, "  description | 描述: %s\n", opts.Description)
	}
	fmt.Fprintf(env.Out, "action | 动作: %s\n", action)
	fmt.Fprintf(env.Out, "platform | 平台: %s\n", runtime.GOOS)
	if opts.Executable != "" {
		fmt.Fprintf(env.Out, "  binary | 程序: %s\n", opts.Executable)
	}
	if len(opts.Arguments) > 0 {
		fmt.Fprintf(env.Out, "  args | 参数: %s\n", strings.Join(opts.Arguments, " "))
	}
	fmt.Fprintf(env.Out, "result | 结果: %s\n", result)
}

// printServiceHint writes the actionable hint of a failed action: elevation for a denied action, the
// missing service manager for an unsupported platform and the install hint for a missing service. |
// printServiceHint 写出失败动作的可操作提示：权限被拒时提示提权，平台不支持时提示缺少服务管理器，
// 服务缺失时提示先安装。
func printServiceHint(env *Env, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, service.ErrPermissionDenied):
		fmt.Fprintf(env.Err, "hint | 提示: run as Administrator (Windows) or with sudo (Linux/macOS) | "+
			"请以管理员身份运行（Windows）或使用 sudo（Linux/macOS）\n")
	case errors.Is(err, service.ErrUnsupportedPlatform):
		fmt.Fprintf(env.Err, "hint | 提示: no supported service manager was detected (Windows SCM, systemd or launchd) | "+
			"未检测到受支持的服务管理器（Windows SCM、systemd 或 launchd）\n")
	case errors.Is(err, service.ErrNotInstalled):
		fmt.Fprintf(env.Err, "hint | 提示: install the service first: yd service install | "+
			"请先安装服务：yd service install\n")
	}
}

// printServiceUsage writes the usage line of the service command. | printServiceUsage 写出 service 命令的用法行。
func printServiceUsage(w io.Writer) {
	fmt.Fprintf(w, "Usage | 用法:\n  %s\n", serviceUsageLine)
	fmt.Fprintf(w, "Actions | 动作: install uninstall start stop restart status\n")
}
