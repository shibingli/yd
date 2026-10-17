// This file holds the read-only probe shared by Check, Missing and Hints. | 本文件保存 Check、Missing 与 Hints 共用的只读探测。

package tools

import (
	"errors"
	"strings"

	"github.com/shibingli/yd/align"
	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/ko"
	"github.com/shibingli/yd/toolchain"
)

// errDockerNotFound reports that no PATH directory provides the docker executable. | errDockerNotFound 表示没有任何 PATH 目录提供 docker 可执行文件。
//
// docker has no resolver package of its own, so the sentinel lives here and plays the role the three
// resolver sentinels play: it is what turns a lookup failure into "missing" rather than
// "unexpected". | docker 没有自己的解析包，因此该哨兵放在这里，承担与三个解析器哨兵相同的角色：
// 它把查找失败判成「缺失」而不是「意外」。
var errDockerNotFound = errx.New("tools: docker is not on PATH | tools：docker 不在 PATH 上")

// errEmptyPath reports a resolver that claimed success without naming an executable. | errEmptyPath 表示解析器声称成功却没有给出可执行文件。
//
// It is an unexpected failure, not a miss: the two are kept apart so a broken resolution chain is
// never silently reported as "just install it". | 它属于意外失败而非缺失：两者必须分开，否则损坏的解析链会被静默报成「装上就好」。
var errEmptyPath = errx.New("tools: resolver returned an empty path | tools：解析器返回了空路径")

// installEntryHint is the uniform entry point appended to every hint block. | installEntryHint 是追加到每个提示块末尾的统一入口。
//
// It is stated once per block instead of once per tool, so the user reads the automatic route a
// single time. | 它每块只说一次而不是每个工具说一次，使用户只读一遍自动安装这条路。
const installEntryHint = "run `yd tools install` to install the installable tools into " + LocalToolDir +
	" | 运行 `yd tools install` 把可安装的工具安装到 " + LocalToolDir

// hintLabel labels the install hint lines. | hintLabel 标注安装提示行。
const hintLabel = "install | 安装"

// resolveTool resolves one tool without executing it. | resolveTool 在不执行工具的前提下解析单个工具。
//
// Each tool goes through its own resolver chain, so the probe agrees with the command that will
// later need the binary; swapping the function out is what lets the tests cover an unexpected
// resolver failure and a project-local copy that wins over PATH. | 每个工具都走自己的解析链，
// 因此探测结果与之后真正需要该二进制的命令一致；替换该函数即可让测试覆盖「解析器意外失败」与
// 「项目内副本优先于 PATH」两种情况。
var resolveTool = func(t Tool, dir string) (string, error) {
	switch t.Name {
	case toolBetteralign:
		return align.Resolve(align.ToolOptions{PathLookup: PathLookup, LocalDir: dir})
	case toolKo:
		return ko.Resolve(ko.Options{WorkingDir: dir})
	case toolTailwindcss:
		return toolchain.ResolveTailwind(toolchain.ToolOptions{PathLookup: PathLookup, LocalDir: dir})
	case toolDocker:
		path, err := PathLookup(t.Name)
		if err != nil {
			// Keep the lookup error on the chain so the injected sentinel decides the verdict. | 把查找错误留在错误链上，由注入的哨兵决定结论。
			return "", errx.Wrap(err, errDockerNotFound)
		}
		if strings.TrimSpace(path) == "" {
			return "", errx.Detail(errDockerNotFound, "lookup returned no path | 查找未返回路径")
		}
		return path, nil
	}
	return "", errx.Detail(ErrUnknownTool, "%q has no resolver | %q 没有解析器", t.Name, t.Name)
}

// notFoundSentinel returns the sentinel the resolver of t reports when the tool is absent. | notFoundSentinel 返回 t 的解析器在工具缺失时报告的哨兵。
//
// It returns nil for a tool without one, and every caller treats that as "nothing is missing". | 该工具没有哨兵时返回 nil，所有调用方都据此认为「无缺失」。
func notFoundSentinel(t Tool) error {
	switch t.Name {
	case toolBetteralign:
		return align.ErrToolNotFound
	case toolKo:
		return ko.ErrBinaryNotFound
	case toolTailwindcss:
		return toolchain.ErrToolNotFound
	case toolDocker:
		return errDockerNotFound
	}
	return nil
}

// isNotFound reports whether err is the "tool is absent" verdict of t. | isNotFound 报告 err 是否是 t 的「工具缺失」结论。
//
// Anything else - a permission problem, a broken configuration value, an unresolvable command name -
// is deliberately not folded in here. | 其它任何情况——权限问题、损坏的配置取值、无法解析的命令名——都被刻意排除在外。
func isNotFound(t Tool, err error) bool {
	sentinel := notFoundSentinel(t)
	return sentinel != nil && errors.Is(err, sentinel)
}

// toolDir normalizes the probe root: an empty dir means the current directory, so the project-local
// .yd/tools segment is part of the chain either way. | toolDir 归一化探测根：dir 为空表示当前目录，
// 使项目内的 .yd/tools 一段在两种情况下都在解析链中。
func toolDir(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return "."
	}
	return dir
}

// probe resolves one tool and classifies the outcome. | probe 解析单个工具并归类结果。
//
// No tool is ever executed here, and nothing reaches the network: a miss produces an empty Path with
// a nil Err, while a resolver failure produces the error itself. | 这里绝不执行任何工具，也不触网：
// 缺失得到空的 Path 与 nil 的 Err，解析器失败则得到错误本身。
func probe(t Tool, dir string) Status {
	st := Status{Tool: t}
	path, err := resolveTool(t, dir)
	switch {
	case err == nil && strings.TrimSpace(path) != "":
		st.Path, st.Found = path, true
	case err == nil:
		st.Err = errx.Detail(errEmptyPath, "%[1]s resolved without a path | %[1]s 解析结果没有路径", t.Name)
	case isNotFound(t, err):
		// The expected outcome for an optional tool: it stays empty, Err stays nil. | 可选工具的预期结果：保持为空，Err 也为 nil。
	default:
		st.Err = err
	}
	return st
}

// Check probes every registered tool under dir. | Check 探测 dir 下每个已登记的工具。
//
// It is read-only by contract: no installation, no network, no execution, so it is safe to call from
// `yd new`, from CI, and from any command that merely wants to warn. | 它按契约是只读的：不安装、不触网、不执行，
// 因此可以从 `yd new`、从 CI、以及任何只想给出提醒的命令里安全调用。
func Check(dir string) []Status {
	root := toolDir(dir)
	list := Registry()
	out := make([]Status, 0, len(list))
	for _, t := range list {
		out = append(out, probe(t, root))
	}
	return out
}

// Missing returns the tools that are absent, in registry order. | Missing 按注册表顺序返回缺失的工具。
//
// A tool whose resolver failed unexpectedly is not missing - it is reported through Status.Err by
// Check, because installing it again would not fix a broken chain. | 解析器意外失败的工具不算缺失：它由 Check 经
// Status.Err 如实上报，因为再装一次修不好一条损坏的链。
func Missing(dir string) []Status {
	var out []Status
	for _, st := range Check(dir) {
		if !st.Found && st.Err == nil {
			out = append(out, st)
		}
	}
	return out
}

// Hints renders the bilingual recovery hints for the missing tools under dir. | Hints 渲染 dir 下缺失工具的双语恢复提示。
//
// Two lines per tool - what it is for, and how to obtain it - followed by the uniform
// `yd tools install` entry. It returns nil when nothing is missing, so a caller can decide whether
// to print a hint block by checking for nil. | 每个工具两行——用途与获取方式——其后是统一的
// `yd tools install` 入口。无缺失时返回 nil，调用方据此决定要不要打印提示块。
func Hints(dir string) []string {
	missing := Missing(dir)
	if len(missing) == 0 {
		return nil
	}
	lines := make([]string, 0, len(missing)*2+1)
	for _, st := range missing {
		lines = append(lines, hintLines(st)...)
	}
	return append(lines, "  "+installEntryHint)
}

// hintLines renders the two hint lines of one missing tool. | hintLines 渲染单个缺失工具的两行提示。
func hintLines(st Status) []string {
	return []string{
		"  " + st.Tool.Name + ": " + st.Tool.Summary,
		"    " + hintLabel + ": " + st.Tool.InstallHint,
	}
}
