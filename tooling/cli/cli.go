// Package cli is the command line front end of the yd framework: it owns the command registry, the
// global flag set, the config layering hook and the minimal interactive helpers. | Package cli 是 yd 框架
// 的命令行前端：负责命令注册表、全局标志集、配置分层接入点与极简交互辅助。
//
// Responsibility boundary | 职责边界：
//
//   - it does parse the command line, load the layered configuration and dispatch to one Command; | 它负责解析命令行、
//     加载分层配置并把控制权交给某个 Command；
//   - it does NOT implement any business behavior: each subcommand lives in its own file (new/dev/gen/
//     build/docker/assets) and only receives an Env; | 它不实现任何业务行为：每个子命令各占一个文件
//     （new/dev/gen/build/docker/assets），只接收一个 Env；
//   - it does NOT print diagnostics itself: user-visible output goes to Env.Out, diagnostics go through
//     ydx/log, and a returned error is rendered by the caller (cmd/yd) with a non-zero exit code. | 它自身
//     不打印诊断：用户可见输出走 Env.Out，诊断日志走 ydx/log，返回的错误由调用方（cmd/yd）渲染并以非零码退出。
//
// Dispatch is POSIX style: `yd <command> [flags] [args]`. Global flags may stand before or after the
// command name, `--help/-h` is resolved per command, and an unknown command lists the available ones
// and returns a non-nil error. | 派发遵循 POSIX 风格：`yd <命令> [标志] [参数]`。全局标志可位于命令名
// 之前或之后，`--help/-h` 按命令各自解析，未知命令会列出可用命令并返回非 nil 错误。
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shibingli/yd/core/version"
	"github.com/shibingli/ydx/config"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/log"
	"github.com/spf13/afero"
	"github.com/spf13/pflag"
)

// Package-level sentinel errors, declared once so every failure path can be matched with errors.Is. | 包级哨兵错误，
// 只声明一次，使所有失败路径都能用 errors.Is 判定。
var (
	// ErrUnknownCommand reports a command name that is not in the registry. | ErrUnknownCommand 表示命令名不在注册表中。
	ErrUnknownCommand = errs.New("cli: unknown command | cli：未知命令")
	// ErrNoCommand reports a missing command or a missing required positional argument. | ErrNoCommand 表示缺少命令或缺少必需的定位参数。
	ErrNoCommand = errs.New("cli: no command given | cli：未提供命令")
	// ErrNotImplemented reports a subcommand whose implementation lands in a later task. | ErrNotImplemented 表示该子命令的实现将在后续任务交付。
	ErrNotImplemented = errs.New("cli: command not implemented yet | cli：命令尚未实现")
	// ErrNilEnv reports a nil *Env handed to Execute or to an interactive helper. | ErrNilEnv 表示传给 Execute 或交互辅助的 *Env 为空。
	ErrNilEnv = errs.New("cli: nil environment | cli：环境为空")
	// ErrFlagParse reports a command line that pflag could not parse. | ErrFlagParse 表示 pflag 无法解析的命令行。
	ErrFlagParse = errs.New("cli: flag parsing failed | cli：标志解析失败")
	// ErrConfigLoad reports a failure while loading the layered configuration. | ErrConfigLoad 表示加载分层配置失败。
	ErrConfigLoad = errs.New("cli: failed to load configuration | cli：加载配置失败")
	// ErrInputFailed reports a failure while reading interactive input. | ErrInputFailed 表示读取交互输入失败。
	ErrInputFailed = errs.New("cli: failed to read input | cli：读取输入失败")
	// ErrEmptyOptions reports an empty option list handed to Choose. | ErrEmptyOptions 表示传给 Choose 的选项列表为空。
	ErrEmptyOptions = errs.New("cli: no options to choose from | cli：无可选项")
	// ErrOutputFailed reports a failure while writing command output. | ErrOutputFailed 表示写出命令输出失败。
	ErrOutputFailed = errs.New("cli: failed to write output | cli：写出输出失败")
	// ErrAssetDrift reports committed minified assets that no longer match their sources. | ErrAssetDrift 表示已提交的压缩资产与源码不再一致。
	ErrAssetDrift = errs.New("cli: committed minified asset is out of date | cli：已提交的压缩资产已过期")
	// ErrEmptyCommandName reports a command registered without a name. | ErrEmptyCommandName 表示注册的命令名为空。
	ErrEmptyCommandName = errs.New("cli: empty command name | cli：命令名为空")
	// ErrDuplicateCommand reports a command name registered twice. | ErrDuplicateCommand 表示命令名重复注册。
	ErrDuplicateCommand = errs.New("cli: duplicated command | cli：命令重复注册")
	// ErrNilRun reports a command registered without a Run function. | ErrNilRun 表示注册的命令缺少 Run 函数。
	ErrNilRun = errs.New("cli: nil Run for command | cli：命令的 Run 为空")
)

// Execute parses args, loads the layered configuration and runs the matching command. | Execute 解析 args、
// 加载分层配置并运行匹配的命令。
//
// Layering: `--config/-c`, `--env-file`, `--verbose/-v`, `--quiet/-q` and `--no-color` are applied
// before the command runs, so Env.Cfg already carries them. Only global flags that were explicitly
// passed are written into the configuration, so the default value of an unpassed flag can never
// shadow an environment variable or a config file (Review Focus #2). | 分层：`--config/-c`、`--env-file`、
// `--verbose/-v`、`--quiet/-q`、`--no-color` 在命令运行前生效，因此 Env.Cfg 已带上它们。只有命令行显式
// 传入的全局标志才会写入配置，未传参标志的默认值绝不会遮蔽环境变量与配置文件（Review Focus #2）。
//
// env.Cfg is loaded from env.WorkDir when it is nil; a caller-provided Cfg is kept as-is and only has
// the global flags applied to it. | env.Cfg 为 nil 时从 env.WorkDir 加载；调用方自备的 Cfg 原样保留，
// 只在其上应用全局标志。
func Execute(ctx context.Context, args []string, env *Env) error {
	if env == nil {
		return ErrNilEnv
	}
	// A broken registry is a defect of the binary itself, so every invocation fails with the
	// recorded sentinel instead of surfacing as a confusing "unknown command". | 注册表损坏是二进制
	// 自身的缺陷，因此每次调用都以登记过的哨兵失败，而不是表现为令人困惑的「未知命令」。
	if err := registryError(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	applyEnvDefaults(env)
	// A leading "--" terminates flag parsing and asks that the next word be taken literally as the
	// command. Drop it here: phase 2 re-parses the whole line, where a surviving "--" would stop
	// pflag and turn every command flag into a positional argument. | 前导 "--" 结束标志解析并要求
	// 下一个词原样作为命令。这里把它去掉：第二阶段会重新解析整行，残留的 "--" 会让 pflag 停止
	// 解析，把所有命令标志都变成位置参数。
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}

	// Phase 1: global flags standing before the command name locate the subcommand. | 第一阶段：
	// 位于命令名之前的全局标志用于定位子命令。
	root := pflag.NewFlagSet("yd", pflag.ContinueOnError)
	root.SetOutput(env.Out)
	root.SetInterspersed(false)
	registerGlobalFlags(root)
	root.Usage = func() { printRootHelp(env.Out) }
	if err := root.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil // The help text is already on its way out. | 帮助文本已经写出
		}
		return errs.Wrap(err, ErrFlagParse)
	}
	if flagChanged(root, flagVersion) {
		printVersion(env)
		return nil
	}
	rest := root.Args()
	if len(rest) == 0 {
		printRootHelp(env.Out)
		return ErrNoCommand
	}

	cmd, ok := findCommand(rest[0])
	if !ok {
		printUnknownCommand(env, rest[0])
		return errs.Detail(ErrUnknownCommand, "%q", rest[0])
	}

	// Phase 2: re-parse the whole line with the command's own flag set, then drop the command token
	// itself, so command flags and positional arguments may be interspersed freely. | 第二阶段：用命令
	// 自己的标志集重新解析整行，随后剔除命令名本身，从而允许命令标志与位置参数任意交错。
	fs := newCommandFlagSet(env, cmd)
	if err := fs.Parse(dropArg(args, len(args)-len(rest))); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil
		}
		return errs.Wrap(err, ErrFlagParse)
	}
	// Global flags are registered on the command's flag set too, so "--version" placed after the
	// command name parses successfully; without this check the command would run instead. | 全局标志
	// 也注册在命令标志集上，因此写在命令名之后的 "--version" 能解析成功；缺少这道检查时命令会照常执行。
	// Some commands deliberately shadow the global "--version" with their own string flag (for
	// example "assets add --version 0.8.0"); only the global boolean form means "print version".
	// | 部分命令刻意用同名的字符串标志遮蔽全局 "--version"（例如 "assets add --version 0.8.0"）；
	// 只有全局的布尔形态才表示「打印版本」。
	if fl := fs.Lookup(flagVersion); fl != nil && fl.Changed && fl.Value.Type() == "bool" {
		printVersion(env)
		return nil
	}

	cfg, err := loadConfig(env, fs)
	if err != nil {
		return err
	}
	env.Cfg = cfg
	applyGlobalFlags(cfg, fs)
	configureLogger(env, cfg)

	return cmd.Run(ctx, env)
}

// applyEnvDefaults fills the zero fields of env with safe defaults. | applyEnvDefaults 用安全默认值填充 env 的零值字段。
func applyEnvDefaults(env *Env) {
	if env.FS == nil {
		env.FS = afero.NewOsFs()
	}
	if env.Out == nil {
		env.Out = io.Discard
	}
	if env.Err == nil {
		env.Err = io.Discard
	}
	if env.In == nil {
		env.In = strings.NewReader("")
	}
	if env.WorkDir == "" {
		env.WorkDir = "."
	}
	if env.Version == "" {
		env.Version = version.String()
	}
}

// dropArg returns args without the element at index i. | dropArg 返回去掉下标 i 元素后的 args。
func dropArg(args []string, i int) []string {
	if i < 0 || i >= len(args) {
		return args
	}
	out := make([]string, 0, len(args)-1)
	out = append(out, args[:i]...)
	out = append(out, args[i+1:]...)
	return out
}

// loadConfig builds the layered configuration for this invocation. | loadConfig 为本次调用构建分层配置。
func loadConfig(env *Env, fs *pflag.FlagSet) (*config.Config, error) {
	if env.Cfg != nil {
		return env.Cfg, nil
	}
	dir, name := projectConfigDir(env), ""
	if v := flagString(fs, flagConfig); v != "" {
		dir, name = splitConfigPath(v, env.WorkDir)
	}
	envFile := filepath.Join(env.WorkDir, config.DefaultEnvFileName)
	if v := flagString(fs, flagEnvFile); v != "" {
		if filepath.IsAbs(v) {
			envFile = v
		} else {
			envFile = filepath.Join(env.WorkDir, v)
		}
	}

	cfg, err := config.Load(config.Options{
		Name:       "yd",
		ConfigDir:  dir,
		ConfigName: name,
		EnvFile:    envFile,
		FS:         env.FS,
	})
	if err != nil {
		return nil, errs.Wrap(err, ErrConfigLoad)
	}
	return cfg, nil
}

// projectConfigDir resolves the default configuration directory of the current project. | projectConfigDir
// 解析当前项目的默认配置目录。
//
// Generated applications read configs/config.yaml (or YD_CONFIG_DIR / $KO_DATA_PATH/configs), while the
// CLI used to search only the project root. That mismatch made `yd dev` read framework defaults even
// when the project set server.http2.extended_connect or server.port, so the Windows child re-executed
// and left a grandchild holding the dev port. The precedence here mirrors meta.ConfigDirPath: explicit
// YD_CONFIG_DIR > $KO_DATA_PATH/configs when present > <workdir>/configs when present > the workdir
// fallback used by tests and minimal projects. |
// 生成应用读取 configs/config.yaml（或 YD_CONFIG_DIR / $KO_DATA_PATH/configs），而 CLI 过去只搜索项目
// 根目录。这一差异使 `yd dev` 在项目设置了 server.http2.extended_connect 或 server.port 时仍读取框架
// 默认值，导致 Windows 子进程重新执行并留下占住开发端口的孙进程。这里的优先级与 meta.ConfigDirPath
// 一致：显式 YD_CONFIG_DIR > 存在时的 $KO_DATA_PATH/configs > 存在时的 <workdir>/configs > 测试与
// 最小项目所用的 workdir 回退。
func projectConfigDir(env *Env) string {
	if env == nil {
		return "."
	}
	workDir := env.WorkDir
	if strings.TrimSpace(workDir) == "" {
		workDir = "."
	}
	if dir := strings.TrimSpace(os.Getenv(configDirEnvKey)); dir != "" {
		return resolveUserPath(workDir, dir)
	}
	if root := strings.TrimSpace(os.Getenv(koDataEnvKey)); root != "" {
		if candidate := filepath.Join(root, projectConfigsDir); isConfigDir(env, candidate) {
			return candidate
		}
	}
	if candidate := filepath.Join(workDir, projectConfigsDir); isConfigDir(env, candidate) {
		return candidate
	}
	return workDir
}

// isConfigDir reports whether path is an existing directory in the CLI file system. | isConfigDir
// 报告 path 在 CLI 文件系统中是否为已存在的目录。
func isConfigDir(env *Env, path string) bool {
	if env.FS == nil {
		info, err := os.Stat(path)
		return err == nil && info.IsDir()
	}
	info, err := env.FS.Stat(path)
	return err == nil && info.IsDir()
}

// Configuration directory names and environment keys shared with generated applications. | 与生成
// 应用共享的配置目录名与环境变量键。
const (
	projectConfigsDir = "configs"
	configDirEnvKey   = "YD_CONFIG_DIR"
	koDataEnvKey      = "KO_DATA_PATH"
)

// splitConfigPath splits a --config value into a search directory and a config name (extension optional). | splitConfigPath 把 --config 取值拆分为搜索目录与配置名（扩展名可选）。
func splitConfigPath(value, workDir string) (dir, name string) {
	d, f := filepath.Split(value)
	d = filepath.Clean(d)
	switch {
	case f == "":
		// A bare directory was given: keep the default config base name. | 只给了目录：沿用默认配置基础名。
		return joinWorkDir(workDir, d), ""
	case filepath.IsAbs(value):
		return d, f
	default:
		if d == "." && filepath.Clean(value) != "." {
			return workDir, f
		}
		return joinWorkDir(workDir, d), f
	}
}

// joinWorkDir joins dir onto workDir unless dir is the current directory. | joinWorkDir 把 dir 接到 workDir 上，除非 dir 就是当前目录。
func joinWorkDir(workDir, dir string) string {
	if dir == "." {
		return workDir
	}
	return resolveUserPath(workDir, dir)
}

// applyGlobalFlags writes the explicitly passed global flags into the configuration layer. | applyGlobalFlags 把命令行显式传入的全局标志写入配置层。
//
// viper.Set is the override layer, i.e. the highest precedence, which is exactly what an explicitly
// passed command line argument deserves. Unpassed flags are skipped on purpose: writing their default
// values would shadow environment variables and config files. | viper.Set 属于覆盖层，优先级最高，
// 正是命令行显式传参应得的层级。未传参的标志刻意跳过：写入它们的默认值会遮蔽环境变量与配置文件。
func applyGlobalFlags(cfg *config.Config, fs *pflag.FlagSet) {
	if flagChanged(fs, flagVerbose) {
		cfg.Set(keyLogLevel, levelDebug)
	}
	if flagChanged(fs, flagQuiet) {
		cfg.Set(keyLogLevel, levelError)
	}
	if flagChanged(fs, flagNoColor) {
		cfg.Set(keyLogColor, false)
	}
}

// configureLogger points the global ydx/log logger at the CLI's diagnostic stream. | configureLogger 把全局 ydx/log 日志器指向 CLI 的诊断流。
func configureLogger(env *Env, cfg *config.Config) {
	lvl, err := log.ParseLevel(cfg.GetString(keyLogLevel))
	if err != nil {
		log.Default().Warn("cli: unrecognized log level, falling back to info | cli：无法识别的日志级别，回退到 info",
			log.Str("level", cfg.GetString(keyLogLevel)))
		lvl = log.InfoLevel
	}
	log.SetDefault(log.New(log.Options{
		Writer:  env.Err,
		Level:   lvl,
		Format:  cfg.GetString("log.format"),
		Service: "yd",
	}))
}

// ColorEnabled reports whether ANSI colors may be used for CLI output. | ColorEnabled 报告 CLI 输出是否可以使用 ANSI 颜色。
//
// It is false when log.color is explicitly set to false, e.g. through --no-color or YD_LOG_COLOR=false. | 当 log.color 被显式置为 false（例如 --no-color 或 YD_LOG_COLOR=false）时返回 false。
func ColorEnabled(env *Env) bool {
	if env == nil || env.Cfg == nil || !env.Cfg.IsSet(keyLogColor) {
		return true
	}
	return env.Cfg.GetBool(keyLogColor)
}

// ANSI color codes used by the CLI's own output. | CLI 自身输出使用的 ANSI 颜色码。
const (
	ansiRed   = "31"
	ansiGreen = "32"
)

// colorize wraps s in an ANSI color when the environment allows colors. | colorize 在环境允许时用 ANSI 颜色包裹 s。
func colorize(env *Env, code, s string) string {
	if !ColorEnabled(env) {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// printVersion writes the build version. | printVersion 写出构建版本。
func printVersion(env *Env) {
	v := env.Version
	if v == "" {
		v = version.String()
	}
	fmt.Fprintf(env.Out, "yd %s\n", v)
}

// printUnknownCommand writes the unknown-command hint together with the available commands. | printUnknownCommand 写出未知命令提示与可用命令列表。
func printUnknownCommand(env *Env, name string) {
	fmt.Fprintf(env.Err, "yd: unknown command %q | yd：未知命令 %q\n", name, name)
	fmt.Fprintf(env.Err, "available commands | 可用命令: %s\n", strings.Join(commandNames(), " "))
	fmt.Fprintf(env.Err, "run \"yd --help\" for usage | 运行 \"yd --help\" 查看用法\n")
}

// printRootHelp writes the top-level usage text. | printRootHelp 写出顶层用法文本。
func printRootHelp(w io.Writer) {
	fmt.Fprintf(w, "yd - YunDev full-stack web framework CLI | yd - YunDev 全栈 Web 框架脚手架\n\n")
	fmt.Fprintf(w, "Usage | 用法:\n  yd <command> [flags] [args]\n\n")
	fmt.Fprintf(w, "Commands | 命令:\n")

	cmds := Commands()
	width := 0
	for _, c := range cmds {
		if n := len(c.Name); n > width {
			width = n
		}
	}
	for _, c := range cmds {
		desc := c.Short
		if desc == "" {
			desc = c.Name
		}
		fmt.Fprintf(w, "  %-*s  %s\n", width, c.Name, desc)
	}

	fmt.Fprintf(w, "\nGlobal flags | 全局标志:\n%s", globalFlagUsages())
	fmt.Fprintf(w, "  -h, --help                 print help | 打印帮助\n")
	fmt.Fprintf(w, "\nRun \"yd <command> --help\" for details of a command. | 运行 \"yd <command> --help\" 查看命令详情。\n")
}

// printCommandHelp writes the usage text of one command. | printCommandHelp 写出单个命令的用法文本。
func printCommandHelp(w io.Writer, c Command, fs *pflag.FlagSet) {
	fmt.Fprintf(w, "yd %s - %s\n", c.Name, c.Short)
	if c.Long != "" {
		fmt.Fprintf(w, "\n%s\n", c.Long)
	}
	usage := c.Usage
	if usage == "" {
		usage = fmt.Sprintf("yd %s [flags] [args]", c.Name)
	}
	fmt.Fprintf(w, "\nUsage | 用法:\n  %s\n", usage)
	fmt.Fprintf(w, "\nFlags | 标志:\n%s", fs.FlagUsages())
	fmt.Fprintf(w, "  -h, --help                 print help | 打印帮助\n")
}

// commandNames returns the sorted names of all registered commands. | commandNames 返回全部已注册命令的排序后名字。
func commandNames() []string {
	cmds := Commands()
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	return names
}

// registryMu guards the command registry. | registryMu 保护命令注册表。
var registryMu sync.RWMutex

// registry holds the registered commands, keyed by name. | registry 按名字保存已注册的命令。
var registry = map[string]Command{}

// registryErrs collects the wiring errors recorded by Register; registryMu guards it too. | registryErrs
// 收集 Register 登记的接线错误；同样由 registryMu 保护。
var registryErrs []error

// Register adds c to the global command registry. | Register 把 c 加入全局命令注册表。
//
// It is meant to be called from package init. An empty name, a duplicate name or a nil Run is
// recorded as a wiring error instead of panicking, and Execute returns those errors on the next
// invocation, so a miswired binary fails once with a clear sentinel instead of crashing at init.
// | 该方法供包初始化阶段调用。名字为空、重复或 Run 为空时登记为接线错误而不是 panic；
// 下一次调用 Execute 会返回这些错误，使接错线的二进制在启动时以明确的哨兵失败，而不是在 init 中崩溃。
func Register(c Command) {
	if c.Name == "" {
		recordRegistryError(ErrEmptyCommandName)
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[c.Name]; dup {
		recordRegistryErrorLocked(errs.Detail(ErrDuplicateCommand, "%q", c.Name))
		return
	}
	if c.Run == nil {
		recordRegistryErrorLocked(errs.Detail(ErrNilRun, "%q", c.Name))
		return
	}
	registry[c.Name] = c
}

// recordRegistryError appends one wiring error under the registry lock. | recordRegistryError 在注册表锁内追加一个接线错误。
func recordRegistryError(err error) {
	if err == nil {
		return
	}
	registryMu.Lock()
	registryErrs = append(registryErrs, err)
	registryMu.Unlock()
}

// recordRegistryErrorLocked appends one wiring error; the caller must hold registryMu. | recordRegistryErrorLocked
// 追加一个接线错误；调用方必须持有 registryMu。
func recordRegistryErrorLocked(err error) {
	if err == nil {
		return
	}
	registryErrs = append(registryErrs, err)
}

// registryError joins every wiring error recorded by Register. | registryError 聚合 Register 登记的全部接线错误。
func registryError() error {
	registryMu.RLock()
	defer registryMu.RUnlock()
	if len(registryErrs) == 0 {
		return nil
	}
	return errors.Join(registryErrs...)
}

// Commands returns every registered command, sorted by name for stable help output. | Commands 返回全部已注册命令，按名字排序以保证帮助输出稳定。
func Commands() []Command {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Command, 0, len(registry))
	for _, c := range registry {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// findCommand looks a command up by name. | findCommand 按名字查找命令。
func findCommand(name string) (Command, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	c, ok := registry[name]
	return c, ok
}

// placeholderNames records the commands still backed by notImplemented, in registration order. | placeholderNames
// 按注册顺序记录仍由 notImplemented 支撑的命令。
//
// The placeholder test derives its list from here, so a task that lands a real Run simply stops calling
// notImplemented and the test follows automatically. | 占位测试从这里取清单，因此真正交付命令的任务只需
// 不再调用 notImplemented，测试会自动跟随。
var placeholderNames []string

// notImplemented builds the Run function of a command that a later task will implement. | notImplemented 构造后续任务才会实现的命令的 Run 函数。
func notImplemented(name string) func(context.Context, *Env) error {
	placeholderNames = append(placeholderNames, name)
	return func(context.Context, *Env) error {
		return errs.Detail(ErrNotImplemented, "yd %s", name)
	}
}
