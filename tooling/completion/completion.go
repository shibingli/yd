// Package completion turns a command registry into shell completion scripts and into the environment
// helpers around them. | Package completion 把命令注册表变成 shell 补全脚本，以及围绕它的环境增强。
//
// Four shells are supported - bash, zsh, csh and powershell - and all four scripts are generated from
// one registry view instead of being written by hand. A new command or a new flag therefore shows up
// in every script on its own, which is the only way four copies cannot drift apart: four hand-written
// scripts silently diverge the first time somebody forgets one of them. | 支持四种 shell——bash、zsh、csh
// 与 powershell——四份脚本都由同一个注册表视图生成而不是手写。新增命令或新增标志会自动出现在每一份脚本里，
// 这也是四份副本不会漂移的唯一途径：手写的四份脚本，只要有人漏改其中一份，就会无声地彼此分叉。
//
// This package deliberately does NOT import the cli package: cli imports it, so the reverse direction
// would be an import cycle. The caller maps its own registry into the view types declared here
// (Registry, Command, Flag), which also keeps the generators usable by any other command line tool. |
// 本包刻意不 import cli 包：cli 会引用它，反向依赖即成环。调用方改为把自身的注册表映射成本包声明的视图类型
// （Registry、Command、Flag），这同时让生成器可被任何其它命令行工具复用。
//
// Installation never edits a user's shell startup file unless it is explicitly asked to: the script is
// written to a conventional location and the line that loads it is printed. Only AppendRC appends that
// line, and then it is idempotent - a marker line is looked up first - and the file being modified is
// backed up byte for byte beforehand. | 除非被明确要求，安装绝不改动用户的 shell 启动文件：脚本被写入
// 约定位置，加载它的那一行被打印出来。只有 AppendRC 才会追加该行，且追加是幂等的——先查标记行——并在改动
// 之前对目标文件做逐字节备份。
//
// Failure is always reported through a package-level sentinel declared here, so callers decide with
// errors.Is instead of matching message text. | 失败一律通过本文件声明的包级哨兵上报，调用方用 errors.Is
// 判定而不做文案匹配。
package completion

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// Declared sentinels of the package. | 本包声明的哨兵错误。
var (
	// ErrUnknownShell reports a shell name that is not one of the four supported ones. | ErrUnknownShell
	// 表示 shell 名不是受支持的四者之一。
	ErrUnknownShell = errx.New("completion: unsupported shell | completion：不支持的 shell")
	// ErrShellUndetected reports that the shell could not be deduced from the environment. | ErrShellUndetected
	// 表示无法从环境推断出 shell。
	ErrShellUndetected = errx.New("completion: cannot tell which shell is in use | completion：无法判断当前使用的是哪个 shell")
	// ErrInvalidRegistry reports a registry view that cannot produce a usable script. | ErrInvalidRegistry
	// 表示注册表视图无法产出可用的脚本。
	ErrInvalidRegistry = errx.New("completion: invalid command registry | completion：非法的命令注册表")
	// ErrNoHomeDir reports that no home directory could be resolved for the default install location. | ErrNoHomeDir
	// 表示无法解析主目录，因此拿不到默认安装位置。
	ErrNoHomeDir = errx.New("completion: home directory is unknown | completion：主目录未知")
	// ErrWriteScript reports a failure while writing the generated script. | ErrWriteScript 表示写入生成的脚本失败。
	ErrWriteScript = errx.New("completion: failed to write the completion script | completion：写入补全脚本失败")
	// ErrReadStartup reports a failure while reading the shell startup file. | ErrReadStartup 表示读取 shell 启动文件失败。
	ErrReadStartup = errx.New("completion: failed to read the startup file | completion：读取启动文件失败")
	// ErrWriteStartup reports a failure while appending to the shell startup file. | ErrWriteStartup
	// 表示追加 shell 启动文件失败。
	ErrWriteStartup = errx.New("completion: failed to update the startup file | completion：更新启动文件失败")
	// ErrWriteBackup reports a failure while backing the startup file up before changing it. | ErrWriteBackup
	// 表示在改动启动文件之前备份失败。
	ErrWriteBackup = errx.New("completion: failed to back up the startup file | completion：备份启动文件失败")
)

// shellEnvName is the environment variable that names the login shell. | shellEnvName 是标明登录 shell 的环境变量名。
const shellEnvName = "SHELL"

// ModuleEnvName is the environment variable PowerShell exports to every child process, which is what
// tells a non-PowerShell parent that the user works in PowerShell. | ModuleEnvName 是 PowerShell 导出给每个子进程的
// 环境变量，正是它让非 PowerShell 的父进程知道用户在用 PowerShell。
const ModuleEnvName = "PSModulePath"

// Shell identifies one supported interactive shell. | Shell 标识一种受支持的交互式 shell。
//
// The value is also the name typed on the command line (`yd completion zsh`) and, for three of the four
// shells, the suffix of the file the script is installed under; see FileName for PowerShell's exception. |
// 该取值同时也是命令行上输入的词（`yd completion zsh`），并且对四种 shell 中的三种而言还是安装脚本的文件名后缀；
// PowerShell 的例外见 FileName。
type Shell string

// The four supported shells. | 四种受支持的 shell。
const (
	// Bash is GNU bash, including the Git Bash shipped with Git for Windows. | Bash 是 GNU bash，也包括
	// Git for Windows 自带的 Git Bash。
	Bash Shell = "bash"
	// Zsh is zsh, whose completion system carries per-item descriptions. | Zsh 是 zsh，它的补全系统支持逐项描述。
	Zsh Shell = "zsh"
	// Csh covers both csh and its successor tcsh, which share the `complete` builtin. | Csh 同时覆盖 csh 与它的
	// 后继者 tcsh，二者的 `complete` 内建命令相同。
	Csh Shell = "csh"
	// PowerShell covers Windows PowerShell 5.1 and PowerShell 7+ (pwsh). | PowerShell 同时覆盖
	// Windows PowerShell 5.1 与 PowerShell 7+（pwsh）。
	PowerShell Shell = "powershell"
)

// Shells returns the four supported shells in a stable order. | Shells 按稳定顺序返回四种受支持的 shell。
//
// The order is the one used by help text, by the syntax-check matrix of the test suite and by any
// listing, so a new shell is added in exactly one place. | 该顺序被帮助文本、测试的语法检查矩阵以及任何列表使用，
// 因此新增一种 shell 只需要改这一处。
func Shells() []Shell { return []Shell{Bash, Zsh, Csh, PowerShell} }

// String returns the shell name as typed on the command line. | String 返回命令行上输入的 shell 名。
func (s Shell) String() string { return string(s) }

// FileName is the file name the script is installed under, inside the ".yd" directory. | FileName 是脚本安装到
// ".yd" 目录下所用的文件名。
//
// PowerShell is the one shell whose file name is not the shell name, because its dot-source operator loads a
// ".ps1" file only: `. '<dir>/.yd/completion.powershell'` returns quietly without defining anything, so the
// loading line printed into the user's $PROFILE would do nothing at all. The extension is a requirement of
// the shell rather than a preference of this package. | PowerShell 是唯一文件名不等于 shell 名的：它的点源
// 运算符只加载 ".ps1" 文件，`. '<目录>/.yd/completion.powershell'` 会静默返回且什么都不定义，于是打印进用户
// $PROFILE 的那一行加载语句等于什么都没做。这个扩展名是 shell 的要求而不是本包的偏好。
func (s Shell) FileName() string {
	if s == PowerShell {
		return "completion.ps1"
	}
	return "completion." + string(s)
}

// ParseShell normalizes a user supplied shell name into one of the four supported shells. | ParseShell 把用户
// 输入的 shell 名规范化为四种受支持的 shell 之一。
//
// The common spellings are accepted so that `--shell pwsh` and `--shell tcsh` do what the user means,
// while an unknown name is refused instead of being silently mapped onto bash: installing a bash script
// for an unrelated shell produces a file that loads but completes nothing. | 常见写法会被接受，因此
// `--shell pwsh` 与 `--shell tcsh` 会得到用户想要的结果；未知名字则被拒绝而不是悄悄当成 bash——为另一种
// shell 安装一份 bash 脚本，只会得到一个能加载但什么都不补全的文件。
func ParseShell(name string) (Shell, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case string(Bash):
		return Bash, nil
	case string(Zsh):
		return Zsh, nil
	case string(Csh), "tcsh":
		return Csh, nil
	case string(PowerShell), "pwsh":
		return PowerShell, nil
	default:
		return "", errx.Detail(ErrUnknownShell, "%q (want | 需要: %s)", name, shellNames())
	}
}

// shellNames renders the supported shell names as a comma separated list. | shellNames 把受支持的 shell 名渲染为
// 逗号分隔的列表。
func shellNames() string {
	names := make([]string, 0, len(Shells()))
	for _, sh := range Shells() {
		names = append(names, string(sh))
	}
	return strings.Join(names, "/")
}

// shellFromProbe maps a shell name that came from the environment onto a supported shell. | shellFromProbe 把来自
// 环境的 shell 名映射为受支持的 shell。
//
// Accepting all four shapes a login program may write into $SHELL is what makes detection work without a
// look-up of the executable in PATH: "bash", "bash -l", "/bin/bash" and, on Windows,
// "C:\Program Files\Git\bin\bash.exe". Comparison is case-insensitive, quotes are stripped and both
// platforms' separators are accepted, because $SHELL is whatever the login program decided to write into
// it. | 接受登录程序可能写进 $SHELL 的全部四种形态，才使探测无需再去 PATH 里查找可执行文件："bash"、
// "bash -l"、"/bin/bash"，以及 Windows 上的 "C:\Program Files\Git\bin\bash.exe"。比较不区分大小写、去掉引号、
// 两种平台的分隔符都接受——因为 $SHELL 的内容完全取决于登录程序写了什么。
func shellFromProbe(probe string) (Shell, bool) {
	normalized := normalizeProbe(probe)
	if normalized == "" {
		return "", false
	}
	for _, sh := range Shells() {
		for _, name := range probeNames(sh) {
			if matchesProbe(normalized, name) {
				return sh, true
			}
		}
	}
	return "", false
}

// probeNames lists the executable names a shell may be reported under. | probeNames 列出某个 shell 可能被上报的
// 可执行文件名。
func probeNames(sh Shell) []string {
	switch sh {
	case Csh:
		return []string{"csh", "tcsh"}
	case PowerShell:
		return []string{"powershell", "pwsh"}
	default:
		return []string{string(sh)}
	}
}

// normalizeProbe lowercases a probe, drops quotes and unifies the separators of both platforms. | normalizeProbe
// 把探测值转为小写、去掉引号，并统一两种平台的路径分隔符。
func normalizeProbe(probe string) string {
	s := strings.Trim(strings.TrimSpace(probe), `"'`)
	s = strings.TrimSpace(s)
	return strings.ToLower(strings.ReplaceAll(s, `\`, "/"))
}

// matchesProbe reports whether a normalized probe names one executable. | matchesProbe 报告规范化后的探测值
// 是否指向某个可执行文件。
//
// The probe is not simply split on whitespace on purpose: "C:/Program Files/Git/bin/bash.exe" would be read
// as "C:/Program" and bash would be lost, so a space is only treated as a boundary when what precedes it is
// already a complete executable name. | 这里刻意不简单按空白切分探测值：
// "C:/Program Files/Git/bin/bash.exe" 会被读成 "C:/Program" 从而丢掉 bash，因此只有当空格之前已经是一个完整的
// 可执行文件名时，空格才被当作分界。
func matchesProbe(normalized, name string) bool {
	for _, form := range []string{name, name + ".exe"} {
		switch {
		case normalized == form:
			return true
		case strings.HasPrefix(normalized, form+" "):
			return true
		case strings.HasSuffix(normalized, "/"+form):
			return true
		case strings.Contains(normalized, "/"+form+" "):
			return true
		}
	}
	return false
}

// Command is the completion-relevant view of one subcommand. | Command 是一个子命令在补全视角下的视图。
type Command struct {
	// Name is the command word typed after the program name. | Name 是紧跟在程序名之后输入的命令词。
	Name string
	// Short is the one-line summary; zsh and PowerShell show it next to the completion, which is what
	// makes completion double as documentation. | Short 是一行摘要；zsh 与 PowerShell 会把它显示在补全项旁边，
	// 这正是「补全即文档」的意思。
	Short string
	// Flags are the flags this command accepts, including the global ones it does not shadow. | Flags
	// 是该命令接受的标志，含它未遮蔽的全局标志。
	Flags []Flag
}

// Flag is the completion-relevant view of one flag. | Flag 是一个标志在补全视角下的视图。
type Flag struct {
	// Name is the long flag name without dashes. | Name 是不带横线的长标志名。
	Name string
	// Shorthand is the single-letter form, empty when the flag has none. | Shorthand 是单字母形式，无简写时为空。
	Shorthand string
	// Usage is the bilingual help text shown as the description. | Usage 是作为描述显示的双语帮助文案。
	Usage string
	// TakesValue reports whether the flag consumes the next word. | TakesValue 报告该标志是否消耗下一个词。
	TakesValue bool
}

// words renders every spelling of the flag that can be typed on the command line. | words 渲染该标志所有可以
// 在命令行上输入的写法。
func (f Flag) words() []string {
	out := make([]string, 0, 2)
	if f.Name != "" {
		out = append(out, "--"+f.Name)
	}
	if f.Shorthand != "" {
		out = append(out, "-"+f.Shorthand)
	}
	return out
}

// description is the text shown next to the flag. | description 是显示在标志旁边的文案。
func (f Flag) description() string {
	if f.Usage != "" {
		return f.Usage
	}
	return "--" + f.Name
}

// Registry is the completion-relevant view of a whole command line tool. | Registry 是整个命令行工具在补全视角下的视图。
type Registry struct {
	// Program is the command word the scripts complete for, e.g. "yd". | Program 是脚本为之补全的命令词，例如 "yd"。
	Program string
	// ToolsDir is the project-local tool directory named by the commented PATH snippet at the end of
	// every script. | ToolsDir 是每份脚本末尾那段被注释掉的 PATH 片段所指向的项目内工具目录。
	ToolsDir string
	// Commands are the commands to complete; they are sorted by name before rendering. | Commands 是要补全的命令；
	// 渲染前会按名字排序。
	Commands []Command
	// GlobalFlags are the flags accepted before the command word, typically --verbose/--quiet. | GlobalFlags
	// 是命令词之前就能接受的标志，通常包括 --verbose/--quiet。
	GlobalFlags []Flag
}

// Generate renders the completion script of one shell. | Generate 渲染某一种 shell 的补全脚本。
//
// The returned text is exactly what is written to disk and what `yd completion <shell>` prints, so the
// two can never disagree; the only failure left is an unsupported shell or a registry that would
// produce a script completing nothing. | 返回的文本与落盘内容、`yd completion <shell>` 打印的内容完全一致，
// 因此二者不可能不一致；剩下唯一的失败原因是 shell 不受支持，或注册表会产出一份什么都补全不了的脚本。
func Generate(sh Shell, reg Registry) (string, error) {
	v, err := reg.view()
	if err != nil {
		return "", err
	}
	switch sh {
	case Bash:
		return generateBash(v), nil
	case Zsh:
		return generateZsh(v), nil
	case Csh:
		return generateCsh(v), nil
	case PowerShell:
		return generatePowerShell(v), nil
	default:
		return "", errx.Detail(ErrUnknownShell, "%q (want | 需要: %s)", string(sh), shellNames())
	}
}

// view is the resolved form every generator renders from: commands sorted by name, each carrying its
// own flags followed by the global flags it does not already shadow, both sorted by name. | view 是每个生成器
// 渲染所用的统一形态：命令按名字排序，每个命令先带自身标志、再带它尚未遮蔽的全局标志，两者均按名字排序。
type view struct {
	// program is the command word the scripts complete for. | program 是脚本为之补全的命令词。
	program string
	// toolsDir is the project-local tool directory of the PATH snippet. | toolsDir 是 PATH 片段所指的项目内工具目录。
	toolsDir string
	// commands are the sorted commands with their merged flags. | commands 是合并标志后、按名字排序的命令。
	commands []Command
	// globals are the sorted global flags. | globals 是排序后的全局标志。
	globals []Flag
	// valueWords lists every spelling of every value-taking flag, across all commands. | valueWords
	// 列出所有命令中每个需要取值的标志的全部写法。
	valueWords []string
}

// view validates a registry and resolves it into the normal form. | view 校验注册表并把它解析为规范形态。
//
// An empty command list is refused rather than rendered: a script that completes nothing is worse than
// an error, because it looks installed and silently does the wrong thing. | 命令列表为空会被拒绝而不是照常渲染：
// 一份什么都不补全的脚本比一个错误更糟，因为它看起来装好了，却只在无声地做错事。
func (r Registry) view() (view, error) {
	if strings.TrimSpace(r.Program) == "" {
		return view{}, errx.Detail(ErrInvalidRegistry, "empty program name | 程序名为空")
	}
	if len(r.Commands) == 0 {
		return view{}, errx.Detail(ErrInvalidRegistry, "no commands | 没有命令")
	}
	v := view{
		program:  r.Program,
		toolsDir: r.ToolsDir,
		globals:  sortedFlags(r.GlobalFlags),
		commands: make([]Command, 0, len(r.Commands)),
	}
	if v.toolsDir == "" {
		v.toolsDir = ".yd/tools"
	}
	for _, c := range r.Commands {
		if strings.TrimSpace(c.Name) == "" {
			return view{}, errx.Detail(ErrInvalidRegistry, "empty command name | 命令名为空")
		}
		merged := mergeFlags(c.Flags, v.globals)
		v.commands = append(v.commands, Command{Name: c.Name, Short: c.Short, Flags: merged})
	}
	sort.Slice(v.commands, func(i, j int) bool { return v.commands[i].Name < v.commands[j].Name })
	v.valueWords = valueWords(v)
	return v, nil
}

// mergeFlags returns the command's own flags followed by the global flags it does not already carry. | mergeFlags
// 返回命令自身的标志，随后是它尚未携带的全局标志。
//
// A command may shadow a global flag (`yd assets add --version 1.0.0` is the live example), so the
// command's own definition wins and the global one of the same name is dropped. | 命令可以遮蔽全局标志
// （`yd assets add --version 1.0.0` 就是活生生的例子），因此命令自身的定义优先，同名的全局标志被丢弃。
func mergeFlags(own, globals []Flag) []Flag {
	seen := make(map[string]bool, len(own)+len(globals))
	short := make(map[string]bool, len(own)+len(globals))
	out := make([]Flag, 0, len(own)+len(globals))
	for _, group := range [][]Flag{own, globals} {
		for _, f := range group {
			if f.Name == "" || seen[f.Name] {
				continue
			}
			if f.Shorthand != "" && short[f.Shorthand] {
				f.Shorthand = ""
			}
			seen[f.Name] = true
			short[f.Shorthand] = true
			out = append(out, f)
		}
	}
	return sortedFlags(out)
}

// sortedFlags returns the flags sorted by name, dropping duplicates and empty names. | sortedFlags 按名字排序返回标志，
// 丢弃重复项与空名。
//
// Sorting is what makes the generated scripts byte-stable, so a test can compare two renders instead of
// hunting for a moving line. | 排序使生成的脚本逐字节稳定，测试才能直接比较两次渲染，而不必去追一行会动的输出。
func sortedFlags(flags []Flag) []Flag {
	uniq := make(map[string]Flag, len(flags))
	for _, f := range flags {
		if f.Name == "" {
			continue
		}
		if _, dup := uniq[f.Name]; dup {
			continue
		}
		uniq[f.Name] = f
	}
	out := make([]Flag, 0, len(uniq))
	for _, f := range uniq {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// valueWords lists every spelling of every value-taking flag in the view. | valueWords 列出视图中每个需要取值的
// 标志的全部写法。
//
// Bash uses it to leave the word after such a flag to file completion instead of offering a flag where a
// value is expected. | bash 用它把这类标志之后的那个词留给文件补全，而不是在应当取值的位置继续补全标志。
func valueWords(v view) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(v.globals))
	add := func(f Flag) {
		if !f.TakesValue {
			return
		}
		for _, w := range f.words() {
			if !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	for _, f := range v.globals {
		add(f)
	}
	for _, c := range v.commands {
		for _, f := range c.Flags {
			add(f)
		}
	}
	sort.Strings(out)
	return out
}

// flagWords renders every spelling of a flag list, in list order. | flagWords 按列表顺序渲染标志的全部写法。
func flagWords(flags []Flag) []string {
	out := make([]string, 0, len(flags)*2)
	for _, f := range flags {
		out = append(out, f.words()...)
	}
	return out
}

// commandNames renders the command words. | commandNames 渲染命令词。
func (v view) commandNames() []string {
	out := make([]string, 0, len(v.commands))
	for _, c := range v.commands {
		out = append(out, c.Name)
	}
	return out
}

// quoteSingle wraps s in single quotes for the POSIX family and terminates the quoting around any
// embedded single quote, which is the only character those two cannot carry literally. | quoteSingle 把 s
// 用单引号包裹以适配 POSIX 家族，并在内嵌单引号处结束引用再续上——那是它们唯一无法字面承载的字符。
func quoteSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// quotePowerShell wraps s in single quotes for PowerShell, where an embedded quote is doubled. | quotePowerShell
// 把 s 用单引号包裹以适配 PowerShell——在那里内嵌的引号要写两遍。
func quotePowerShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// describe renders the name/summary pair the way zsh and PowerShell expect it. | describe 按 zsh 与 PowerShell
// 期望的形态渲染「名字/摘要」对。
//
// Only the first colon separates the two fields, so a summary may itself contain colons, which pflag
// usages do (`service name, default: service.name`). | 只有第一个冒号起分隔作用，因此摘要本身可以含冒号，
// pflag 的帮助文案正是如此（`service name, default: service.name`）。
func describe(name, summary string) string {
	if summary == "" {
		return name
	}
	return name + ":" + summary
}

// writeLinef appends one formatted line to b. | writeLinef 向 b 追加一行格式化文本。
func writeLinef(b *strings.Builder, format string, args ...any) {
	fmt.Fprintf(b, format, args...)
	b.WriteByte('\n')
}
