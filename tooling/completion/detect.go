// This file owns shell detection: which shell is the user sitting in, and which startup file belongs to
// it. Nothing here writes anything, so detection can always be reported before it is acted upon. | 本文件
// 负责 shell 探测：用户正处在哪个 shell 里，以及它对应哪个启动文件。这里不写任何东西，因此探测结果总能先报告再动手。

package completion

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// Detection sources, i.e. the signal a detection rests on. | 探测依据，即某次探测所依赖的信号。
const (
	// SourceShellEnv marks a detection that read $SHELL. | SourceShellEnv 标记读取 $SHELL 得到的探测结果。
	SourceShellEnv = "$SHELL"
	// SourceModulePath marks the Windows fallback that recognised a PowerShell session. | SourceModulePath
	// 标记在 Windows 上识别出 PowerShell 会话的回退依据。
	SourceModulePath = ModuleEnvName
	// SourcePlatform marks the platform default, used when nothing else spoke. | SourcePlatform
	// 标记平台默认值，在其它信号都不发声时使用。
	SourcePlatform = "platform"
	// SourceOverride marks a shell the caller named instead of detected. | SourceOverride 标记由调用方指定
	// 而不是探测得到的 shell。
	SourceOverride = "--shell"
)

// windowsGOOS is the GOOS value that switches this file to its Windows rules. | windowsGOOS 是让本文件切到
// Windows 规则的 GOOS 取值。
//
// Windows has no $SHELL convention: cmd.exe does not set it and PowerShell sets PSModulePath instead.
// A user who typed `yd completion detect` from a plain cmd.exe window is therefore told powershell,
// which is the shell the completion can actually be installed for on that platform - and the printed
// report names the signal, so the guess is visible rather than hidden. | Windows 没有 $SHELL 约定：
// cmd.exe 不设置它，PowerShell 改为设置 PSModulePath。因此从普通 cmd.exe 窗口敲下
// `yd completion detect` 的用户会被告知 powershell——那是该平台上真正能装上补全的 shell，
// 且报告会点明依据，使这个推断可见而不是隐藏。
const windowsGOOS = "windows"

// Detection records which shell was detected and on what evidence. | Detection 记录探测到的 shell 及其依据。
type Detection struct {
	// Shell is the detected shell. | Shell 是探测到的 shell。
	Shell Shell
	// Source is the signal the detection rests on: one of SourceShellEnv, SourceModulePath, SourcePlatform
	// or SourceOverride. | Source 是探测所依赖的信号：SourceShellEnv、SourceModulePath、SourcePlatform
	// 或 SourceOverride 之一。
	Source string
	// Probe is the raw value of that signal, e.g. the exact contents of $SHELL; it is empty when the
	// signal is the platform default or a caller override, neither of which carries a value. | Probe
	// 是该信号的原始取值，例如 $SHELL 的确切内容；当信号是平台默认值或调用方指定时为空，二者都不带取值。
	Probe string
}

// Describe renders the evidence in one bilingual line. | Describe 用一行双语文本渲染探测依据。
func (d Detection) Describe() string {
	switch {
	case d.Source == SourceOverride:
		return "given on the command line | 由命令行指定"
	case d.Probe != "":
		return d.Source + "=" + d.Probe
	case d.Source == SourcePlatform:
		return "platform default | 平台默认值"
	default:
		return d.Source
	}
}

// Detect decides which shell is in use. | Detect 判断当前使用的是哪个 shell。
//
// goos is passed in rather than read from runtime, and getenv rather than os.Getenv, so the whole table
// of cases - Windows, POSIX, an empty $SHELL, a $SHELL with arguments - is testable without touching the
// process environment or the machine the test runs on. | goos 由调用方传入而不是读 runtime，getenv 由调用方
// 传入而不是直接用 os.Getenv，因此「Windows、POSIX、$SHELL 为空、$SHELL 带参数」这整张表都能在不触碰进程环境、
// 也不依赖测试所跑机器的前提下被验证。
//
// An unrecognised $SHELL is an error rather than a silent fallback: installing the wrong shell's script
// gives a file that loads without completing anything, which is far harder to diagnose than a message
// naming --shell. | 无法识别的 $SHELL 会报错而不是静默回退：装错 shell 的脚本只会得到一个能加载却什么都不补全的
// 文件，那比一句「请用 --shell」难诊断得多。
func Detect(goos string, getenv func(string) string) (Detection, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	probe := strings.TrimSpace(getenv(shellEnvName))
	if probe != "" {
		if sh, ok := shellFromProbe(probe); ok {
			return Detection{Shell: sh, Source: SourceShellEnv, Probe: probe}, nil
		}
		if !isWindows(goos) {
			return Detection{}, errx.Detail(ErrShellUndetected,
				"%s=%q (pass --shell %s | 请用 --shell %s)", shellEnvName, probe, shellNames(), shellNames())
		}
	}
	if isWindows(goos) {
		if strings.TrimSpace(getenv(ModuleEnvName)) != "" {
			return Detection{Shell: PowerShell, Source: SourceModulePath}, nil
		}
		return Detection{Shell: PowerShell, Source: SourcePlatform, Probe: probe}, nil
	}
	return Detection{}, errx.Detail(ErrShellUndetected,
		"%s is not set (pass --shell %s | %s 未设置，请用 --shell %s)",
		shellEnvName, shellNames(), shellEnvName, shellNames())
}

// isWindows reports whether goos names the Windows platform. | isWindows 报告 goos 是否指 Windows 平台。
func isWindows(goos string) bool { return strings.EqualFold(strings.TrimSpace(goos), windowsGOOS) }

// override turns a caller supplied shell name into a Detection. | override 把调用方给出的 shell 名变成一次探测结果。
func override(name string) (Detection, error) {
	sh, err := ParseShell(name)
	if err != nil {
		return Detection{}, err
	}
	return Detection{Shell: sh, Source: SourceOverride, Probe: string(sh)}, nil
}

// StartupFile returns the startup file of one shell: the file --append-rc would touch, and the file the
// printed instructions name. | StartupFile 返回某个 shell 的启动文件：--append-rc 会改动的文件，也是打印出来的
// 说明所点名的文件。
//
// The PowerShell path is the documented default of that shell's $PROFILE. Windows' shell folder
// redirection (Documents moved into OneDrive) can place it elsewhere, which is why the report always
// prints the exact path before it is written. | PowerShell 的路径是该 shell $PROFILE 的文档默认值。
// Windows 的 shell 文件夹重定向（文档目录被移到 OneDrive）可能把它放到别处，这正是报告在写入之前总会打印确切路径的原因。
func StartupFile(sh Shell, home, goos string, getenv func(string) string) string {
	if home == "" {
		return ""
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	switch sh {
	case Bash:
		return filepath.Join(home, ".bashrc")
	case Zsh:
		return filepath.Join(home, ".zshrc")
	case Csh:
		return filepath.Join(home, ".cshrc")
	case PowerShell:
		if isWindows(goos) {
			dir := "PowerShell"
			// Windows PowerShell 5.1 keeps its profile under WindowsPowerShell. | Windows PowerShell 5.1
			// 把 profile 放在 WindowsPowerShell 之下。
			if strings.Contains(strings.ToLower(getenv(ModuleEnvName)), "windowspowershell") {
				dir = "WindowsPowerShell"
			}
			return filepath.Join(home, "Documents", dir, "Microsoft.PowerShell_profile.ps1")
		}
		cfg := strings.TrimSpace(getenv("XDG_CONFIG_HOME"))
		if cfg == "" {
			cfg = filepath.Join(home, ".config")
		}
		return filepath.Join(cfg, "powershell", "profile.ps1")
	default:
		return ""
	}
}

// LoadLine renders the line that loads the installed script, in the syntax of sh. | LoadLine 用 sh 的语法渲染
// 加载已安装脚本的那一行。
//
// The path is quoted because a home directory may contain spaces, and it is absolute because that is
// what makes the line copy-pasteable into a startup file. | 路径被引用是因为主目录可能含空格；它是绝对路径，
// 因为这样这一行才能被直接复制粘贴进启动文件。
func LoadLine(sh Shell, scriptPath string) string {
	if sh == PowerShell {
		return ". " + quotePowerShell(scriptPath)
	}
	return "source " + quoteDouble(scriptPath)
}

// quoteDouble wraps s in double quotes for the POSIX family, escaping the four characters that are
// still special inside them. | quoteDouble 把 s 用双引号包裹以适配 POSIX 家族，并转义其中依然特殊的四个字符。
func quoteDouble(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
	return `"` + r.Replace(s) + `"`
}
