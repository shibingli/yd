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
//     logx, and a returned error is rendered by the caller (cmd/yd) with a non-zero exit code. | 它自身
//     不打印诊断：用户可见输出走 Env.Out，诊断日志走 logx，返回的错误由调用方（cmd/yd）渲染并以非零码退出。
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
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/afero"
	"github.com/spf13/pflag"
	"github.com/yun-dev/yd/config"
	"github.com/yun-dev/yd/errx"
	"github.com/yun-dev/yd/logx"
	"github.com/yun-dev/yd/version"
)

// Package-level sentinel errors, declared once so every failure path can be matched with errors.Is. | 包级哨兵错误，
// 只声明一次，使所有失败路径都能用 errors.Is 判定。
var (
	// ErrUnknownCommand reports a command name that is not in the registry. | ErrUnknownCommand 表示命令名不在注册表中。
	ErrUnknownCommand = errx.New("cli: unknown command | cli：未知命令")
	// ErrNoCommand reports a missing command or a missing required positional argument. | ErrNoCommand 表示缺少命令或缺少必需的定位参数。
	ErrNoCommand = errx.New("cli: no command given | cli：未提供命令")
	// ErrNotImplemented reports a subcommand whose implementation lands in a later task. | ErrNotImplemented 表示该子命令的实现将在后续任务交付。
	ErrNotImplemented = errx.New("cli: command not implemented yet | cli：命令尚未实现")
	// ErrNilEnv reports a nil *Env handed to Execute or to an interactive helper. | ErrNilEnv 表示传给 Execute 或交互辅助的 *Env 为空。
	ErrNilEnv = errx.New("cli: nil environment | cli：环境为空")
	// ErrFlagParse reports a command line that pflag could not parse. | ErrFlagParse 表示 pflag 无法解析的命令行。
	ErrFlagParse = errx.New("cli: flag parsing failed | cli：标志解析失败")
	// ErrConfigLoad reports a failure while loading the layered configuration. | ErrConfigLoad 表示加载分层配置失败。
	ErrConfigLoad = errx.New("cli: failed to load configuration | cli：加载配置失败")
	// ErrInputFailed reports a failure while reading interactive input. | ErrInputFailed 表示读取交互输入失败。
	ErrInputFailed = errx.New("cli: failed to read input | cli：读取输入失败")
	// ErrEmptyOptions reports an empty option list handed to Choose. | ErrEmptyOptions 表示传给 Choose 的选项列表为空。
	ErrEmptyOptions = errx.New("cli: no options to choose from | cli：无可选项")
	// ErrOutputFailed reports a failure while writing command output. | ErrOutputFailed 表示写出命令输出失败。
	ErrOutputFailed = errx.New("cli: failed to write output | cli：写出输出失败")
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
	if ctx == nil {
		ctx = context.Background()
	}
	applyEnvDefaults(env)

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
		return errx.Wrap(err, ErrFlagParse)
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
		return errx.Detail(ErrUnknownCommand, "%q", rest[0])
	}

	// Phase 2: re-parse the whole line with the command's own flag set, then drop the command token
	// itself, so command flags and positional arguments may be interspersed freely. | 第二阶段：用命令
	// 自己的标志集重新解析整行，随后剔除命令名本身，从而允许命令标志与位置参数任意交错。
	fs := newCommandFlagSet(env, cmd)
	if err := fs.Parse(dropArg(args, len(args)-len(rest))); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil
		}
		return errx.Wrap(err, ErrFlagParse)
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
	dir, name := env.WorkDir, ""
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
		return nil, errx.Wrap(err, ErrConfigLoad)
	}
	return cfg, nil
}

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
	if dir == "." || dir == string(filepath.Separator) && workDir == "" {
		return workDir
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(workDir, dir)
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

// configureLogger points the global logx logger at the CLI's diagnostic stream. | configureLogger 把全局 logx 日志器指向 CLI 的诊断流。
func configureLogger(env *Env, cfg *config.Config) {
	lvl, err := logx.ParseLevel(cfg.GetString(keyLogLevel))
	if err != nil {
		logx.Default().Warn("cli: unrecognized log level, falling back to info | cli：无法识别的日志级别，回退到 info",
			logx.Str("level", cfg.GetString(keyLogLevel)))
		lvl = logx.InfoLevel
	}
	logx.SetDefault(logx.New(logx.Options{
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

// Register adds c to the global command registry. | Register 把 c 加入全局命令注册表。
//
// It is meant to be called from package init; a duplicate or empty name panics immediately, because
// such a registry is a programming error that would otherwise surface as a confusing runtime failure. | 该方法
// 供包初始化阶段调用；名字重复或为空会立即 panic，因为这种注册表属于编码错误，否则只会在运行期
// 以难以理解的方式暴露。
func Register(c Command) {
	if c.Name == "" {
		panic("cli: Register: empty command name | cli: Register：命令名为空")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[c.Name]; dup {
		panic("cli: Register: duplicated command " + c.Name + " | cli: Register：命令重复注册 " + c.Name)
	}
	if c.Run == nil {
		panic("cli: Register: nil Run for command " + c.Name + " | cli: Register：命令 " + c.Name + " 的 Run 为空")
	}
	registry[c.Name] = c
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

// notImplemented builds the Run function of a command that a later task will implement. | notImplemented 构造后续任务才会实现的命令的 Run 函数。
func notImplemented(name string) func(context.Context, *Env) error {
	return func(context.Context, *Env) error {
		return errx.Detail(ErrNotImplemented, "yd %s", name)
	}
}
