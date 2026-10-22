// This file implements the completion command: it maps the command registry into the view types of the
// completion package, prints one of the four scripts, reports what was detected, and installs the script
// together with the prompt that shell integration needs. It owns flag handling and the printed report,
// and nothing else - generation, detection and installation live in the completion package. | 本文件实现
// completion 命令：把命令注册表映射为 completion 包的视图类型、打印四份脚本之一、报告探测结果，并在需要提示的情况下
// 安装脚本。它只负责标志处理与打印报告，别无其它——生成、探测与安装都在 completion 包内。

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/shibingli/yd/tooling/completion"
	"github.com/shibingli/yd/tooling/tools"
	"github.com/shibingli/ydx/errs"
	"github.com/spf13/pflag"
)

// completionCommandName is the command word of the shell integration command. | completionCommandName
// 是 shell 集成命令的命令词。
const completionCommandName = "completion"

// completionProgram is the command word the generated scripts complete for. | completionProgram 是生成的脚本
// 为之补全的命令词。
const completionProgram = "yd"

// The completion command's action words. | completion 命令的动作词。
const (
	// completionActionDetect reports the detected shell and the paths an installation would use. | completionActionDetect
	// 报告探测到的 shell 与一次安装将使用的路径。
	completionActionDetect = "detect"
	// completionActionInstall writes the script and prints the line that loads it. | completionActionInstall
	// 写入脚本并打印加载它的那一行。
	completionActionInstall = "install"
)

// The completion command's flag names. | completion 命令的标志名。
const (
	// flagCompletionShell overrides the detected shell. | flagCompletionShell 覆盖探测到的 shell。
	flagCompletionShell = "shell"
	// flagCompletionDir overrides the base directory that holds the ".yd" segment. | flagCompletionDir
	// 覆盖承载 ".yd" 段的基准目录。
	flagCompletionDir = "dir"
	// flagCompletionPrint prints the script instead of installing it. | flagCompletionPrint 打印脚本而不安装。
	flagCompletionPrint = "print"
	// flagCompletionAppendRC appends the loading line to the shell startup file. | flagCompletionAppendRC
	// 把加载行追加到 shell 启动文件。
	flagCompletionAppendRC = "append-rc"
	// flagCompletionYes answers every prompt with its default without asking. | flagCompletionYes 不提问，
	// 所有提问都取默认值。
	flagCompletionYes = "yes"
)

// completionUsageLine is the usage line of the completion command. | completionUsageLine 是 completion 命令的用法行。
const completionUsageLine = "yd completion bash|zsh|csh|powershell\n" +
	"      yd completion detect [--shell S] [--dir D]\n" +
	"      yd completion install [--shell S] [--dir D] [--print] [--append-rc] [--yes]"

// completionFlags keeps the completion command's flag set, installed by registerCompletionFlags. | completionFlags
// 保存 completion 命令的标志集，由 registerCompletionFlags 安装。
var completionFlags *pflag.FlagSet

// Completion command injection points, used the way align.PathLookup and tools.PathLookup are. | completion
// 命令的注入点，用法与 align.PathLookup、tools.PathLookup 一致。
//
// The environment and the target platform are read through these variables, so a test can put the whole
// table - every `$SHELL` value, a Windows machine without `$SHELL`, an unknown home directory - in front
// of the command without touching the process environment of the machine the test runs on. | 环境与目标平台都经
// 这两个变量读取，因此测试可以把整张表——`$SHELL` 的各个取值、没有 `$SHELL` 的 Windows 机器、未知的主目录——
// 摆在命令面前，而不触碰测试所跑机器的进程环境。
var (
	// shellLookup reads environment variables for detection and for the home directory. | shellLookup
	// 为探测与主目录读取环境变量。
	shellLookup = os.Getenv
	// completionGOOS is the platform the Windows-only rules key off; it is a variable rather than a direct
	// runtime.GOOS read so that both branches are testable on either platform. | completionGOOS 是 Windows
	// 专有规则所依据的平台；它是变量而不是直接读 runtime.GOOS，使两个分支在任一平台上都可被测。
	completionGOOS = runtime.GOOS
)

func init() {
	Register(Command{
		Name:  completionCommandName,
		Short: "generate and install shell completion | 生成并安装 shell 补全",
		Long: "Print or install a completion script for bash, zsh, csh or powershell.\n" +
			"打印或安装 bash、zsh、csh、powershell 的补全脚本。\n" +
			"The scripts are generated from the command registry, so a command, a flag or a positional\n" +
			"vocabulary added later is completed without anybody editing a script by hand. Positional words\n" +
			"are action words and enumerated values such as `yd assets <list|update|...>` and\n" +
			"`yd tools install <name...>`; paths keep the shell's own file completion.\n" +
			"脚本由命令注册表生成，因此日后新增的命令、标志或位置参数词表无需任何人手工修改脚本即可被补全。\n" +
			"位置参数包括动作词与枚举值，例如 `yd assets <list|update|...>` 与 `yd tools install <name...>`；\n" +
			"路径仍交给 shell 自带的文件补全。\n" +
			"install writes the script to <dir>/.yd/completion.<shell> and prints the line that loads it.\n" +
			"Every run rewrites the script; with --append-rc an existing marker block is updated in place,\n" +
			"older or duplicate blocks are collapsed, and a changed script path updates the loading line.\n" +
			"powershell is the one exception to that file name: its script lands in completion.ps1, because\n" +
			"the PowerShell dot-source operator loads a \".ps1\" file only.\n" +
			"It never touches ~/.bashrc, ~/.zshrc, ~/.cshrc or $PROFILE unless --append-rc is given, and\n" +
			"even then it only writes an idempotent marker block and backs the file up first.\n" +
			"install 把脚本写入 <dir>/.yd/completion.<shell> 并打印加载它的那一行。\n" +
			"每次运行都会重写脚本；带 --append-rc 时已有标记块会被就地更新，旧块与重复块会合并，\n" +
			"脚本路径变化时加载行也会随之更新。\n" +
			"PowerShell 是这一文件名的唯一例外：它的脚本落在 completion.ps1，因为 PowerShell 的点源运算符只加载 \".ps1\" 文件。\n" +
			"除非显式给出 --append-rc，它绝不触碰 ~/.bashrc、~/.zshrc、~/.cshrc 或 $PROFILE；\n" +
			"即便给出了，也只写入带标记的幂等块，并先做备份。\n" +
			"Every script ends with a commented snippet that would put the project-local " + tools.LocalToolDir + "\n" +
			"directory on PATH; enabling it stays the user's decision.\n" +
			"每份脚本末尾都有一段被注释掉的片段，可以把项目内的 " + tools.LocalToolDir + " 目录加入 PATH；\n" +
			"是否启用始终由用户自己决定。",
		Usage: completionUsageLine,
		Flags: registerCompletionFlags,
		Run:   runCompletion,
		Positionals: []Positional{{
			Name:        "action",
			Description: "shell or action: bash/zsh/csh/powershell/detect/install | shell 或动作",
			Values:      completionActionWords(),
		}},
	})
}

// completionActionWords lists the four shells and the two action words in usage order. | completionActionWords
// 按用法顺序列出四种 shell 与两个动作词。
//
// The shells are taken from completion.Shells rather than retyped, so a fifth shell reaches the completion
// of the completion command itself without a second list to update. | shell 取自 completion.Shells 而不是重抄，
// 因此新增第五种 shell 时，`yd completion` 自身的补全也无需第二份列表。
func completionActionWords() []string {
	shells := completion.Shells()
	out := make([]string, 0, len(shells)+2)
	for _, sh := range shells {
		out = append(out, string(sh))
	}
	return append(out, completionActionDetect, completionActionInstall)
}

// registerCompletionFlags declares the completion command's flags. | registerCompletionFlags 声明 completion 命令的标志。
func registerCompletionFlags(fs *pflag.FlagSet) {
	fs.String(flagCompletionShell, "", "target shell: bash/zsh/csh/powershell, default detected | 目标 shell：bash/zsh/csh/powershell，缺省为探测所得")
	fs.String(flagCompletionDir, "", "directory holding .yd, default the home directory | 承载 .yd 的目录，缺省为主目录")
	fs.Bool(flagCompletionPrint, false, "print the script instead of installing it | 打印脚本而不安装")
	fs.Bool(flagCompletionAppendRC, false, "append the loading line to the shell startup file, backing it up first | 把加载行追加到 shell 启动文件，并先做备份")
	fs.Bool(flagCompletionYes, false, "never prompt, keep every default | 不提问，一律取默认值")
	completionFlags = fs
}

// completionOptions is the parsed command line of one run. | completionOptions 是一次运行解析出的命令行。
//
// The flags are copied out of the flag set instead of being read from it as needed, because generating
// the script rebuilds every command's flag set - including this command's own - and would therefore
// replace the very variable the flags are stored in. Snapshotting first keeps the whole run reading one
// consistent command line. | 标志从标志集中复制出来，而不是按需再去读它：生成脚本时会重建每个命令的标志集
// （包括本命令自己的），从而替换掉存放标志的那个变量。先做快照，整轮运行读到的是同一条一致的命令行。
type completionOptions struct {
	// shell is the --shell override, empty when the shell is to be detected. | shell 是 --shell 的覆盖值，
	// 为空表示走探测。
	shell string
	// dir is the --dir override, empty when the home directory is the base. | dir 是 --dir 的覆盖值，
	// 为空表示以主目录为基准。
	dir string
	// args are the positional arguments after the flags. | args 是标志之后的位置参数。
	args []string
	// printOnly reports --print. | printOnly 报告 --print。
	printOnly bool
	// appendRC reports --append-rc. | appendRC 报告 --append-rc。
	appendRC bool
	// yes reports --yes. | yes 报告 --yes。
	yes bool
}

// completionOptionsFrom snapshots the flag set the command's closure installed. | completionOptionsFrom
// 快照命令闭包安装的标志集。
func completionOptionsFrom() completionOptions {
	fs := completionFlags
	if fs == nil {
		return completionOptions{}
	}
	return completionOptions{
		shell:     flagString(fs, flagCompletionShell),
		dir:       flagString(fs, flagCompletionDir),
		printOnly: flagBool(fs, flagCompletionPrint),
		appendRC:  flagBool(fs, flagCompletionAppendRC),
		yes:       flagBool(fs, flagCompletionYes),
		args:      append([]string(nil), fs.Args()...),
	}
}

// runCompletion dispatches the six action words. | runCompletion 派发六个动作词。
func runCompletion(_ context.Context, env *Env) error {
	opts := completionOptionsFrom()
	if len(opts.args) == 0 {
		return completionUsageError(env, "no action | 缺少动作")
	}
	word := strings.ToLower(strings.TrimSpace(opts.args[0]))
	switch word {
	case completionActionDetect:
		return runCompletionDetect(env, opts)
	case completionActionInstall:
		return runCompletionInstall(env, opts)
	}
	sh, err := completion.ParseShell(word)
	if err != nil {
		return completionUsageError(env, err.Error())
	}
	if len(opts.args) > 1 {
		return completionUsageError(env, fmt.Sprintf("%s takes no argument, got %q | %s 不接受参数，收到 %q", word, opts.args[1], word, opts.args[1]))
	}
	return printCompletionScript(env, sh)
}

// printCompletionScript writes one generated script to Out. | printCompletionScript 把生成的一份脚本写到 Out。
//
// Out carries the script and nothing else, because `source <(yd completion bash)` reads exactly that
// stream: a single word of commentary on Out would be fed to the shell as a command. | Out 上只有脚本本身，
// 因为 `source <(yd completion bash)` 读的正是这条流：一旦 Out 上多出一句说明，它就会被当成命令交给 shell。
func printCompletionScript(env *Env, sh completion.Shell) error {
	script, err := completion.Generate(sh, completionRegistry(env))
	if err != nil {
		return completionHint(env, err)
	}
	if _, err := io.WriteString(env.Out, script); err != nil {
		return errs.Wrap(err, ErrOutputFailed)
	}
	return nil
}

// runCompletionDetect reports the detected shell and the paths an installation would use. | runCompletionDetect
// 报告探测到的 shell 与一次安装将使用的路径。
//
// It answers the question a user actually has before installing anything - which shell was recognised,
// and which files would be written - so the report is printed even when a later install is only being
// considered. | 它回答的是用户在安装之前真正关心的问题——识别出了哪个 shell、会写哪些文件——因此即便只是
// 打算安装，这份报告也值得先看一眼。
func runCompletionDetect(env *Env, opts completionOptions) error {
	if len(opts.args) > 1 {
		return completionUsageError(env, fmt.Sprintf("detect takes no argument, got %q | detect 不接受参数，收到 %q", opts.args[1], opts.args[1]))
	}
	if opts.printOnly || opts.appendRC || opts.yes {
		return completionUsageError(env, "--print/--append-rc/--yes belong to install | --print/--append-rc/--yes 属于 install")
	}
	plan, err := completion.Resolve(completionRequest(env, opts))
	if err != nil {
		return completionHint(env, err)
	}
	printCompletionPlan(env.Out, plan)
	return nil
}

// runCompletionInstall writes the script and, when asked, appends the loading line. | runCompletionInstall
// 写入脚本，并在被要求时追加加载行。
//
// The startup file is only ever touched on an explicit --append-rc, or on a yes answer to the prompt that
// an interactive run with a TTY shows. A non-interactive run - --yes, or no TTY at all - writes the file
// and explains, and never edits a dotfile: an automated pipeline has nobody who could undo a change it
// did not expect. | 启动文件只在显式给出 --append-rc 时被触碰，或在 TTY 交互运行中出现提问并得到 yes 时被触碰。
// 非交互运行（--yes 或根本没有 TTY）只写文件并给出说明，绝不改动 dotfile：自动化流水线上没有人能撤销它
// 不期望的改动。
func runCompletionInstall(env *Env, opts completionOptions) error {
	if len(opts.args) > 1 {
		return completionUsageError(env, fmt.Sprintf("install takes no argument, got %q | install 不接受参数，收到 %q", opts.args[1], opts.args[1]))
	}
	if opts.printOnly && opts.appendRC {
		return completionUsageError(env, "--print writes nothing, so it cannot be combined with --append-rc | --print 不写任何东西，因此不能与 --append-rc 同时使用")
	}

	req := completionRequest(env, opts)
	plan, err := completion.Resolve(req)
	if err != nil {
		return completionHint(env, err)
	}

	if opts.printOnly {
		// The report goes to Err and Out carries nothing but the script, so that
		// `yd completion install --print --shell bash > ~/.yd/completion.bash` writes a script and not a
		// script with a report glued to it. | 报告写到 Err，Out 上只有脚本本身，因此
		// `yd completion install --print --shell bash > ~/.yd/completion.bash` 写出的是一份脚本，
		// 而不是脚本后面粘了一段报告。
		printCompletionPlan(env.Err, plan)
		return printCompletionScript(env, plan.Detection.Shell)
	}

	appendRC := opts.appendRC
	if !appendRC && env.IsTTY && !opts.yes {
		question := fmt.Sprintf("yd completion: append the loading line to %s? | yd completion：把加载行追加到 %s 吗？", plan.RCFile, plan.RCFile)
		answer, err := Confirm(env, question, false)
		if err != nil {
			return err
		}
		appendRC = answer
	}

	req.AppendRC = appendRC
	res, err := completion.Install(req)
	printCompletionPlan(env.Out, res.Plan)
	printCompletionResult(env.Out, res)
	if err != nil {
		return completionHint(env, err)
	}
	return nil
}

// completionRequest assembles the completion package request from the snapshotted options. | completionRequest
// 依据快照到的选项组装 completion 包的请求。
//
// Home and platform are left to the completion package, which owns their documented resolution order;
// only the injected lookups travel from here. | 主目录与平台交给 completion 包处理，由它负责自己那套写明顺序的
// 解析；从这里过去的只有被注入的读取函数。
func completionRequest(env *Env, opts completionOptions) completion.Request {
	return completion.Request{
		Registry: completionRegistry(env),
		Getenv:   shellLookup,
		FS:       env.FS,
		GOOS:     completionGOOS,
		Dir:      opts.dir,
		Shell:    completion.Shell(opts.shell),
		AppendRC: opts.appendRC,
	}
}

// completionRegistry maps the cli command registry into the completion view. | completionRegistry 把 cli 命令注册表
// 映射为补全视图。
//
// Each command is asked for its flags through the very same function Execute uses, so what is completed
// is exactly what can be typed - including the global flags, which every command accepts after its own
// name - and its positional vocabulary is carried over as declared. A command added tomorrow needs no
// change here at all; it only has to describe its positionals where it is registered. | 每个命令的标志都经由
// Execute 所用的同一个函数取得，因此能被补全的正是能被输入的东西——包括每个命令在自身名字之后都接受的全局标志
// ——而它的位置参数词表按声明原样传递。明天新增的命令完全不需要改这里；它只需在注册处描述自己的位置参数。
func completionRegistry(env *Env) completion.Registry {
	quiet := *env
	quiet.Out = io.Discard
	cmds := Commands()
	reg := completion.Registry{
		Program:     completionProgram,
		ToolsDir:    tools.LocalToolDir,
		Commands:    make([]completion.Command, 0, len(cmds)),
		GlobalFlags: completionGlobalFlags(),
	}
	for _, c := range cmds {
		fs := newCommandFlagSet(&quiet, c)
		reg.Commands = append(reg.Commands, completion.Command{
			Name:        c.Name,
			Short:       c.Short,
			Flags:       completionFlagsOf(fs),
			Positionals: completionPositionals(c.Positionals),
		})
	}
	return reg
}

// completionGlobalFlags renders the global flags as the completion view sees them. | completionGlobalFlags
// 把全局标志渲染为补全视图所见的形态。
func completionGlobalFlags() []completion.Flag {
	fs := pflag.NewFlagSet(completionProgram, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerGlobalFlags(fs)
	return completionFlagsOf(fs)
}

// completionPositionals converts the command's positional slots into the completion view. | completionPositionals
// 把命令的位置槽位转换为补全视图。
//
// The value slices are copied so the generated script can never alias a slice another command mutates; the
// registry is assembled once per invocation, and a command's slice is shared with its registration value.
// | 候选词切片会被复制，使生成的脚本绝不会别名到另一个命令可能修改的切片；注册表每次调用只装配一次，而命令的
// 切片与它的注册值共享。
func completionPositionals(in []Positional) []completion.Positional {
	if len(in) == 0 {
		return nil
	}
	out := make([]completion.Positional, len(in))
	for i, p := range in {
		out[i] = completion.Positional{
			Name:        p.Name,
			Description: p.Description,
			Values:      append([]string(nil), p.Values...),
			Variadic:    p.Variadic,
		}
	}
	return out
}

// completionFlagsOf converts a flag set into the completion view. | completionFlagsOf 把标志集转换为补全视图。
//
// pflag exposes "takes a value" as "has no optional default": a boolean flag is given one ("true"), while
// a flag whose value must follow it is left without any. That is exactly the distinction a completion
// script has to make, so no type switch over value types is needed. | pflag 以「没有可选默认值」表达「需要取值」：
// 布尔标志被赋予一个（"true"），而必须跟一个取值的标志则没有。这正是补全脚本要区分的差别，因此无需按取值类型做类型分支。
func completionFlagsOf(fs *pflag.FlagSet) []completion.Flag {
	flags := make([]completion.Flag, 0, fs.NFlag())
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		flags = append(flags, completion.Flag{
			Name:       f.Name,
			Shorthand:  f.Shorthand,
			Usage:      f.Usage,
			TakesValue: f.NoOptDefVal == "",
		})
	})
	return flags
}

// printCompletionPlan writes the resolved shell and the paths an installation uses. | printCompletionPlan
// 写出解析出的 shell 与一次安装所用的路径。
func printCompletionPlan(w io.Writer, plan completion.Plan) {
	fmt.Fprintf(w, "yd completion | yd 补全\n")
	fmt.Fprintf(w, "shell | Shell 环境: %s (%s)\n", plan.Detection.Shell, plan.Detection.Describe())
	fmt.Fprintf(w, "script | 脚本: %s\n", plan.ScriptPath)
	fmt.Fprintf(w, "load now | 立即加载: %s\n", plan.LoadLine)
	fmt.Fprintf(w, "startup file | 启动文件: %s (only written with --append-rc | 仅在 --append-rc 时写入)\n", plan.RCFile)
}

// printCompletionResult reports what an installation did. | printCompletionResult 报告一次安装做了什么。
func printCompletionResult(w io.Writer, res completion.Result) {
	fmt.Fprintf(w, "written | 已写入: %s (%d bytes | 字节)\n", res.ScriptPath, res.Bytes)
	switch {
	case res.RCUpdated:
		fmt.Fprintf(w, "updated | 已更新: %s\n", res.RCFile)
		if res.BackupPath != "" {
			fmt.Fprintf(w, "backup | 备份: %s\n", res.BackupPath)
		}
	case res.RCAppended:
		fmt.Fprintf(w, "appended | 已追加: %s\n", res.RCFile)
		if res.BackupPath != "" {
			fmt.Fprintf(w, "backup | 备份: %s\n", res.BackupPath)
		}
	case res.RCSkipped:
		fmt.Fprintf(w, "appended | 已追加: %s (marker already present, left untouched | 已含标记，保持原样)\n", res.RCFile)
	default:
		fmt.Fprintf(w, "startup file | 启动文件: %s (unchanged, pass --append-rc to append the line above | 未改动，传 --append-rc 才会追加上面那一行)\n", res.RCFile)
	}
	fmt.Fprintf(w, "environment | 环境增强: the PATH snippet at the end of the script stays commented out | 脚本末尾的 PATH 片段保持注释状态\n")
}

// completionUsageError writes the usage block and reports the failure. | completionUsageError 写出用法块并报告失败。
func completionUsageError(env *Env, detail string) error {
	fmt.Fprintf(env.Err, "yd completion: %s\n", detail)
	printCompletionUsage(env.Err)
	return errs.Detail(ErrNoCommand, "%s", completionUsageLine)
}

// printCompletionUsage writes the usage block of the completion command. | printCompletionUsage 写出 completion
// 命令的用法块。
func printCompletionUsage(w io.Writer) {
	fmt.Fprintf(w, "Usage | 用法:\n  %s\n", completionUsageLine)
	fmt.Fprintf(w, "Actions | 动作: bash zsh csh powershell %s %s\n", completionActionDetect, completionActionInstall)
}

// completionHint renders the recovery hint of a failed detection or installation. | completionHint 渲染探测或安装
// 失败的恢复提示。
func completionHint(env *Env, err error) error {
	switch {
	case errors.Is(err, completion.ErrShellUndetected), errors.Is(err, completion.ErrUnknownShell):
		fmt.Fprintf(env.Err, "yd completion: pass --shell bash|zsh|csh|powershell | yd completion：请传入 --shell bash|zsh|csh|powershell\n")
	case errors.Is(err, completion.ErrNoHomeDir):
		fmt.Fprintf(env.Err, "yd completion: set HOME, or USERPROFILE on Windows | yd completion：请设置 HOME，Windows 上为 USERPROFILE\n")
	}
	fmt.Fprintf(env.Err, "yd completion: %v\n", err)
	return err
}
