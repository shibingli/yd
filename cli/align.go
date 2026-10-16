// This file implements the align command: it is a thin shell around the align package and owns nothing but flag parsing, the configuration lookups and the bilingual summary. | 本文件实现 align 命令：它只是 align 包的一层薄壳，只负责标志解析、配置查询与双语摘要。

package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shibingli/yd/align"
	"github.com/shibingli/yd/logx"
	"github.com/spf13/pflag"
)

// alignCommandName is the command word of the memory alignment command. | alignCommandName 是内存对齐命令的命令词。
const alignCommandName = "align"

// The align command's flag names. | align 命令的标志名。
const (
	// flagAlignApply writes the suggested field order back to the sources. | flagAlignApply 把建议的字段顺序写回源码。
	flagAlignApply = "apply"
	// flagAlignTest brings test files into the analysis. | flagAlignTest 把测试文件纳入分析。
	flagAlignTest = "test"
	// flagAlignExcludeDir adds a repeatable directory pattern to skip. | flagAlignExcludeDir 追加可重复的待跳过目录模式。
	flagAlignExcludeDir = "exclude-dir"
	// flagAlignBin pins the betteralign executable and skips the resolution chain. | flagAlignBin 直接指定 betteralign 可执行文件并跳过解析链。
	flagAlignBin = "bin"
)

// keyAlignBin is the configured betteralign path, ranked below --bin. | keyAlignBin 是配置的 betteralign 路径，优先级低于 --bin。
const keyAlignBin = "align.bin"

// alignUsageLine is the usage line of the align command. | alignUsageLine 是 align 命令的用法行。
const alignUsageLine = "yd align [--apply] [--test] [--exclude-dir DIR]... [--bin PATH] [targets...]"

// alignFlags keeps the align command's flag set, installed by registerAlignFlags. | alignFlags 保存 align 命令的标志集，由 registerAlignFlags 安装。
var alignFlags *pflag.FlagSet

func init() {
	Register(Command{
		Name:  alignCommandName,
		Short: "check and apply struct memory alignment | 检查并应用结构体内存对齐",
		Long: "Run the optional betteralign analyzer over the package patterns and report how many structs would use less memory with a different field order. | 对给定的包模式运行可选的 betteralign 分析器，报告有多少结构体在字段顺序变化后会占用更少内存。\n" +
			"Patterns default to ./... and --apply writes the suggestions back to the sources. | 包模式默认 ./...，--apply 会把建议写回源码。\n" +
			"The tool is optional: when it is missing the command prints an installation hint and exits non-zero. | 该工具是可选的：缺失时命令打印安装提示并以非零码退出。",
		Usage: alignUsageLine,
		Flags: registerAlignFlags,
		Run:   runAlign,
	})
}

// registerAlignFlags declares the flags of the align command. | registerAlignFlags 声明 align 命令的标志。
func registerAlignFlags(fs *pflag.FlagSet) {
	fs.Bool(flagAlignApply, false, "write the suggested field order back to the sources | 把建议的字段顺序写回源码")
	fs.Bool(flagAlignTest, false, "include test files in the analysis | 分析包含测试文件")
	fs.StringArray(flagAlignExcludeDir, nil, "directory pattern to skip, repeatable and comma-separated | 要跳过的目录模式，可重复且支持逗号分隔")
	fs.String(flagAlignBin, "", "betteralign executable, skips the resolution chain | betteralign 可执行文件，跳过解析链")
	alignFlags = fs
}

// runAlign resolves the analyzer, runs it and prints the bilingual summary. | runAlign 解析分析器、执行它并打印双语摘要。
//
// A missing or outdated tool never panics: the sentinel is rendered as an actionable hint and returned so that cmd/yd exits non-zero. | 工具缺失或版本过旧都不会 panic：哨兵被渲染为可执行的提示并返回，由 cmd/yd 以非零码退出。
func runAlign(ctx context.Context, env *Env) error {
	o, tool := alignOptions(env, alignFlags)
	rep, err := align.ResolveAndRun(ctx, tool, o, logx.Default())
	if rep != nil {
		printAlignReport(env, o, rep)
	}
	if err != nil {
		printAlignHint(env, err)
		return err
	}
	return nil
}

// alignOptions assembles the align package options from the parsed flags. | alignOptions 依据已解析的标志组装 align 包选项。
//
// Precedence follows the repository rule 命令行参数 > 环境变量（含 .env）> 配置文件 > 框架默认值: --bin wins, then align.bin, then YD_BETTERALIGN_BIN, then .yd/tools, then PATH (the chain itself lives in the align package). | 优先级遵循仓库规则「命令行参数 > 环境变量（含 .env）> 配置文件 > 框架默认值」：--bin 优先，其次 align.bin，再次 YD_BETTERALIGN_BIN、.yd/tools，最后 PATH（该链本身位于 align 包内）。
func alignOptions(env *Env, fs *pflag.FlagSet) (align.Options, align.ToolOptions) {
	o := align.Options{
		Dir:          env.WorkDir,
		Targets:      fs.Args(),
		Apply:        flagBool(fs, flagAlignApply),
		IncludeTests: flagBool(fs, flagAlignTest),
		ExcludeDirs:  flagStrings(fs, flagAlignExcludeDir),
	}
	tool := align.ToolOptions{
		Bin:       flagString(fs, flagAlignBin),
		ConfigBin: configString(env, keyAlignBin),
		LocalDir:  env.WorkDir,
	}
	return o, tool
}

// printAlignReport writes the bilingual summary of one analysis run. | printAlignReport 写出一次分析运行的双语摘要。
func printAlignReport(env *Env, o align.Options, rep *align.Report) {
	fmt.Fprintf(env.Out, "align: %d diagnostics | %d 条告警\n", rep.Diagnostics, rep.Diagnostics)
	if rep.Applied {
		fmt.Fprintf(env.Out, "applied | 已应用: %s\n", alignTargets(o))
	}
	fmt.Fprintf(env.Out, "duration | 耗时: %s\n", rep.Duration.Round(time.Millisecond))
}

// alignTargets renders the analyzed package patterns for the summary. | alignTargets 渲染摘要中的被分析包模式。
func alignTargets(o align.Options) string {
	targets := o.Targets
	if len(targets) == 0 {
		targets = []string{align.DefaultTarget}
	}
	out := targets[0]
	for _, t := range targets[1:] {
		out += " " + t
	}
	return out
}

// printAlignHint renders the recovery hint of a failed run, which is what keeps the optional tool from ever blocking a build. | printAlignHint 渲染失败运行的可恢复提示，正是它让可选工具永远不会阻塞构建。
func printAlignHint(env *Env, err error) {
	switch {
	case errors.Is(err, align.ErrToolNotFound):
		fmt.Fprintf(env.Err, "yd align: betteralign not found | yd align：未找到 betteralign\n")
		fmt.Fprintf(env.Err, "  install | 安装: %s\n", align.InstallHint)
		fmt.Fprintf(env.Err, "  or drop the binary into %s | 或把二进制放入 %s\n", align.LocalToolDir, align.LocalToolDir)
	case errors.Is(err, align.ErrToolTooOld):
		fmt.Fprintf(env.Err, "yd align: betteralign is older than this module's Go version | yd align：betteralign 的 Go 版本低于本模块\n")
		fmt.Fprintf(env.Err, "  rebuild | 重新构建: %s\n", align.InstallHint)
	default:
		fmt.Fprintf(env.Err, "yd align: %v\n", err)
	}
}
