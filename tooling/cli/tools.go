// This file implements the tools command: it prints the status of the optional external tools and
// installs the ones that are missing. It owns flag handling, the two action words, the status table
// and the error hints, and nothing else - the probe and the installer live in the tools package. | 本文件
// 实现 tools 命令：打印可选外部工具的状态并安装缺失的那些。它只负责标志处理、两个动作词、状态表与错误提示，
// 别无其它——探测与安装由 tools 包负责。

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/tooling/ko"
	"github.com/shibingli/yd/tooling/tools"
	"github.com/spf13/pflag"
)

// toolsCommandName is the command word of the external tool command. | toolsCommandName 是外部工具命令的命令词。
const toolsCommandName = "tools"

// The tools command's action words. | tools 命令的动作词。
const (
	// toolsActionCheck lists the status of every registered tool; it is the default action. | toolsActionCheck
	// 列出每个已登记工具的状态；它是默认动作。
	toolsActionCheck = "check"
	// toolsActionInstall installs the named tools, or every installable one. | toolsActionInstall 安装具名工具，
	// 或全部可安装项。
	toolsActionInstall = "install"
)

// The three states of one row in the status table. | 状态表中一行的三种状态。
const (
	// toolsStateReady marks a tool that was resolved. | toolsStateReady 标记已解析到的工具。
	toolsStateReady = "ready | 就绪"
	// toolsStateMissing marks an absent tool, which is what the install hints are for. | toolsStateMissing
	// 标记缺失的工具，安装提示正是为它准备的。
	toolsStateMissing = "missing | 缺失"
	// toolsStateFailed marks a resolution failure that installing cannot repair. | toolsStateFailed 标记再装一次也修不好的解析失败。
	toolsStateFailed = "failed | 解析失败"
)

// toolsUsageLine is the usage line of the tools command. | toolsUsageLine 是 tools 命令的用法行。
const toolsUsageLine = "yd tools [check]\n" +
	"      yd tools install [name...]"

// Configuration keys read by the installer and the probe. | 安装器与探测读取的配置键。
const (
	// keyNetProxy is the HTTP proxy used by every yd-initiated download (assets and tailwindcss);
	// empty means the standard HTTP_PROXY / HTTPS_PROXY / NO_PROXY environment. | keyNetProxy 是
	// yd 发起的所有下载（资产与 tailwindcss）使用的 HTTP 代理；为空表示标准 HTTP_PROXY /
	// HTTPS_PROXY / NO_PROXY 环境变量。
	keyNetProxy = "http.proxy"
	// keyTailwindURL overrides the tailwindcss release asset URL, so a mirror can be used where
	// GitHub is unreachable. | keyTailwindURL 覆盖 tailwindcss 发布资产 URL，使 GitHub 不可达时可用镜像。
	keyTailwindURL = "tailwind.url"
)

// toolsFlags keeps the tools command's flag set, installed by registerToolsFlags. | toolsFlags 保存 tools
// 命令的标志集，由 registerToolsFlags 安装。
var toolsFlags *pflag.FlagSet

func init() {
	Register(Command{
		Name:  toolsCommandName,
		Short: "check and install the optional external tools | 检查并安装可选的外部工具",
		Long: "Report which of the optional external tools are available on this machine, and install the\n" +
			"missing ones into the project-local " + tools.LocalToolDir + " directory - the first place every\n" +
			"resolver looks, so nothing has to be added to PATH.\n" +
			"报告本机可用的可选外部工具，并把缺失的那些安装到项目内的 " + tools.LocalToolDir + " 目录——那是每个解析器\n" +
			"最先查看的位置，因此无需改动 PATH。\n" +
			"The four tools are betteralign, ko, tailwindcss and docker; betteralign, ko and tailwindcss can be\n" +
			"installed automatically (tailwindcss downloads a release binary and honours http.proxy and\n" +
			"tailwind.url), while docker is only reported. Only `yd tools install` ever touches the network.\n" +
			"四个工具是 betteralign、ko、tailwindcss 与 docker；betteralign、ko 与 tailwindcss 可以自动安装\n" +
			"（tailwindcss 下载发布二进制，并遵循 http.proxy 与 tailwind.url），docker 只做报告。只有\n" +
			"`yd tools install` 会接触网络。\n" +
			"check never fails a build: a missing optional tool is a hint, not an error.\n" +
			"check 绝不让构建失败：可选工具缺失是提示，不是错误。",
		Usage: toolsUsageLine,
		Flags: registerToolsFlags,
		Run:   runTools,
	})
}

// registerToolsFlags captures the command's flag set. | registerToolsFlags 捕获命令的标志集。
//
// The tools command declares no flag of its own - the action and the tool names are positional words -
// but the parsed flag set is still the only way to reach fs.Args(). | tools 命令不声明任何自己的标志——动作与工具名
// 都是位置词——但已解析的标志集仍是取得 fs.Args() 的唯一途径。
func registerToolsFlags(fs *pflag.FlagSet) {
	toolsFlags = fs
}

// runTools dispatches the two actions. | runTools 派发两个动作。
func runTools(ctx context.Context, env *Env) error {
	action, names, err := toolsRequest(toolsFlags)
	if err != nil {
		fmt.Fprintf(env.Err, "yd tools: %v\n", err)
		printToolsUsage(env.Err)
		return err
	}
	if action == toolsActionInstall {
		return runToolsInstall(ctx, env, names)
	}
	return runToolsCheck(env)
}

// toolsRequest splits the positional words into an action and the tool names. | toolsRequest 把位置词拆成动作与工具名。
//
// A missing word means check, so `yd tools` alone is the everyday entry point. | 缺少动作词时表示 check，因此
// 单独一条 `yd tools` 就是日常入口。
func toolsRequest(fs *pflag.FlagSet) (string, []string, error) {
	if fs == nil || len(fs.Args()) == 0 {
		return toolsActionCheck, nil, nil
	}
	args := fs.Args()
	action := strings.ToLower(strings.TrimSpace(args[0]))
	switch action {
	case toolsActionCheck:
		if len(args) > 1 {
			return "", nil, errx.Detail(ErrNoCommand,
				"%s (check takes no argument, got %q | check 不接受参数，收到 %q)", toolsUsageLine, args[1], args[1])
		}
		return toolsActionCheck, nil, nil
	case toolsActionInstall:
		return toolsActionInstall, args[1:], nil
	default:
		return "", nil, errx.Detail(ErrUnknownCommand,
			"%q (actions | 动作: %s, %s)", args[0], toolsActionCheck, toolsActionInstall)
	}
}

// runToolsCheck writes the status table and the hints of what is missing. | runToolsCheck 写出状态表与缺失项的提示。
//
// It returns nil even when tools are missing: they are optional by design, so a report command must
// never fail a build - that is what `yd tools install` is for. | 即使有工具缺失它也返回 nil：这些工具按设计是
// 可选的，报告命令绝不该让构建失败——安装是 `yd tools install` 的职责。
func runToolsCheck(env *Env) error {
	opts := toolsProbeOptions(env)
	printToolsStatus(env, tools.CheckWith(opts))
	if hints := tools.HintsWith(opts); len(hints) > 0 {
		fmt.Fprintf(env.Out, "hints | 提示:\n")
		for _, line := range hints {
			fmt.Fprintf(env.Out, "%s\n", line)
		}
	}
	return nil
}

// toolsProbeOptions maps the layered configuration onto the probe's executables. | toolsProbeOptions
// 把分层配置映射为探测所用的可执行文件。
//
// The three keys are the same ones the commands themselves read (align.bin, ko.binary,
// tailwind.bin), so `yd tools check` and `yd align`/`yd ko`/`yd dev` agree on what is available;
// leaving them empty keeps the env -> .yd/tools -> PATH tail of each chain unchanged. | 三个键与各命令
// 自身读取的键完全一致（align.bin、ko.binary、tailwind.bin），因此 `yd tools check` 与
// `yd align`/`yd ko`/`yd dev` 对「什么可用」的判断一致；留空则各链条尾部「环境变量 → .yd/tools → PATH」不变。
func toolsProbeOptions(env *Env) tools.Options {
	opts := tools.Options{
		Dir:         env.WorkDir,
		Proxy:       configString(env, keyNetProxy),
		TailwindURL: configString(env, keyTailwindURL),
	}
	if env.Cfg == nil {
		return opts
	}
	opts.Binaries = map[string]string{
		tools.NameBetteralign: configString(env, keyAlignBin),
		tools.NameKo:          configString(env, ko.KeyBinary),
		tools.NameTailwindcss: configString(env, keyDevTailwindBin),
	}
	return opts
}

// printToolsStatus writes one row per registered tool. | printToolsStatus 为每个已登记工具写出一行。
func printToolsStatus(env *Env, statuses []tools.Status) {
	fmt.Fprintf(env.Out, "tools | 工具: %d\n", len(statuses))
	for _, st := range statuses {
		fmt.Fprintf(env.Out, "  %-13s %s %s\n", st.Tool.Name, toolsState(st), toolsDetail(st))
	}
}

// toolsState renders the state column of one row. | toolsState 渲染一行的状态列。
func toolsState(st tools.Status) string {
	switch {
	case st.Err != nil:
		return toolsStateFailed
	case st.Found:
		return toolsStateReady
	default:
		return toolsStateMissing
	}
}

// toolsDetail renders the right-hand column of one row: the error of a broken resolution, the resolved
// path with its version, or a dash for a tool that is simply absent. | toolsDetail 渲染一行的右侧列：损坏解析的错误、
// 带版本的解析路径，或工具单纯缺失时的一个短横线。
func toolsDetail(st tools.Status) string {
	switch {
	case st.Err != nil:
		return st.Err.Error()
	case !st.Found:
		return "-"
	case st.Version != "":
		return st.Path + " (" + st.Version + ")"
	default:
		return st.Path
	}
}

// runToolsInstall installs the requested tools and reports what happened. | runToolsInstall 安装所请求的工具并报告结果。
//
// The report is printed even when something failed, because it carries the successful installations
// too; the returned error is what makes cmd/yd exit non-zero. | 即使有失败也会打印报告，因为它同时承载成功的安装；
// 返回的错误使 cmd/yd 以非零码退出。
func runToolsInstall(ctx context.Context, env *Env, names []string) error {
	report, err := installTools(ctx, env, names)
	if report != nil {
		printToolsReport(env, report)
	}
	if err != nil {
		printToolsInstallHint(env, err)
		return err
	}
	return nil
}

// installTools runs the installation through the tools package. | installTools 经 tools 包执行安装。
//
// No tool name means every installable entry, which is what keeps `yd tools install` a one-word
// command on a fresh machine. | 没有工具名表示全部可安装项，正是它让 `yd tools install` 在崭新的机器上只需一个词。
func installTools(ctx context.Context, env *Env, names []string) (*tools.Report, error) {
	return tools.InstallWith(ctx, toolsProbeOptions(env), names, commandLogger(env))
}

// printToolsReport writes the closing summary of an installation run. | printToolsReport 写出安装运行的收尾摘要。
func printToolsReport(env *Env, report *tools.Report) {
	fmt.Fprintf(env.Out, "installed | 已安装: %d\n", len(report.Installed))
	for _, st := range report.Installed {
		fmt.Fprintf(env.Out, "  %-13s %s\n", st.Tool.Name, toolsDetail(st))
	}
	fmt.Fprintf(env.Out, "skipped | 已跳过: %d\n", len(report.Skipped))
	for _, st := range report.Skipped {
		fmt.Fprintf(env.Out, "  %-13s %s\n", st.Tool.Name, st.Path)
	}
	fmt.Fprintf(env.Out, "failed | 失败: %d\n", len(report.Failed))
	for _, f := range report.Failed {
		fmt.Fprintf(env.Out, "  %-13s %v\n", f.Name, f.Err)
	}
}

// printToolsInstallHint renders the recovery hint of a failed installation run. | printToolsInstallHint 渲染失败安装运行的可恢复提示。
//
// The lead-in line names the verdict, which is what makes the error matchable with errors.Is, and
// the error itself carries the per-tool detail - including the hint of a tool that has to be
// installed by hand. A misspelled tool name additionally gets the usage block, because the error
// alone cannot show which words the command accepts. | 引导行点明结论——正是它让错误可用 errors.Is 判定——
// 错误本身则携带逐工具的细节，包括需要手动安装的提示。拼错的工具名还会得到用法块，因为单条错误无法
// 展示命令接受哪些词。
func printToolsInstallHint(env *Env, err error) {
	switch {
	case errors.Is(err, tools.ErrNoGoToolchain):
		fmt.Fprintf(env.Err, "yd tools: nothing can be installed without the Go toolchain | yd tools：没有 Go 工具链就装不了任何工具\n")
	case errors.Is(err, tools.ErrNotInstallable):
		fmt.Fprintf(env.Err, "yd tools: some tools must be installed by hand | yd tools：部分工具需手动安装\n")
	case errors.Is(err, tools.ErrUnknownTool):
		printToolsUsage(env.Err)
	}
	fmt.Fprintf(env.Err, "yd tools: %v\n", err)
}

// printToolsUsage writes the usage block of the tools command. | printToolsUsage 写出 tools 命令的用法块。
func printToolsUsage(w io.Writer) {
	fmt.Fprintf(w, "Usage | 用法:\n  %s\n", toolsUsageLine)
	fmt.Fprintf(w, "Actions | 动作: %s %s\n", toolsActionCheck, toolsActionInstall)
}
