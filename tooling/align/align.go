// Package align wraps betteralign, the optional analyzer that reports struct field orders which waste memory, and exposes it both as a library and as the `yd align` command. | Package align 封装可选分析器 betteralign——它报告浪费内存的结构体字段顺序——并以库与 `yd align` 命令两种形态对外提供。
//
// The tool is optional by design: when it cannot be resolved, Resolve returns ErrToolNotFound, whose message carries an actionable installation hint, so a missing analyzer never fails a build. | 该工具按设计就是可选的：无法解析时 Resolve 返回 ErrToolNotFound，其文案带有可执行的安装提示，因此分析器缺失绝不会让构建失败。
//
// Resolution follows the repository precedence chain 命令行参数 > 环境变量（含 .env）> 配置文件 > 框架默认值: Bin, ConfigBin, EnvBin, the project-local .yd/tools copy and finally PATH. | 解析遵循仓库优先级链「命令行参数 > 环境变量（含 .env）> 配置文件 > 框架默认值」：Bin、ConfigBin、EnvBin、项目内 .yd/tools 副本，最后是 PATH。
//
// Every failure path references a package-level sentinel declared here, so callers decide with errors.Is instead of string matching. | 所有失败路径都引用本文件声明的包级哨兵，调用方用 errors.Is 判定而不做字符串匹配。
package align

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
)

// EnvToolBin is the environment variable holding the betteralign path. | EnvToolBin 是保存 betteralign 路径的环境变量。
const EnvToolBin = "YD_BETTERALIGN_BIN"

// InstallHint is the command that installs the optional betteralign tool. | InstallHint 是安装可选工具 betteralign 的命令。
//
// ErrToolNotFound and the `yd align` command quote it verbatim, so the hint stays actionable. | ErrToolNotFound 与 `yd align` 命令会逐字引用它，保证提示始终可执行。
const InstallHint = "go install github.com/dkorunic/betteralign/cmd/betteralign@latest"

// LocalToolDir is the project-local drop-in directory of the analyzer, relative to the project root. | LocalToolDir 是相对项目根的分析器本地放置目录。
const LocalToolDir = ".yd/tools"

// DefaultTarget is the package pattern analyzed when Options.Targets is empty. | DefaultTarget 是 Options.Targets 为空时分析的包模式。
const DefaultTarget = "./..."

// DefaultMaxPasses caps the number of -apply passes when Options.MaxPasses is not set. | DefaultMaxPasses 是 Options.MaxPasses 未设置时 -apply 的遍数上限。
//
// One pass is not always enough: the analyzer's rewrite of a struct is not idempotent, so a second run over the already rewritten sources usually finds further pointer-byte savings, and the loop keeps applying until the diagnostics reach zero or stop shrinking. | 单遍并不总是足够：分析器对结构体的重写并非幂等，对已重写的源码再跑一遍通常还能发现进一步的指针字节收益，因此循环会持续应用，直到诊断归零或不再减少。
const DefaultMaxPasses = 5

// toolName is the bare command name of the analyzer. | toolName 是分析器的裸命令名。
const toolName = "betteralign"

// exitDiagnostics is the exit code the analyzer's singlechecker driver uses to report that diagnostics were found; it is a successful analysis, not a failure. | exitDiagnostics 是分析器 singlechecker 驱动报告「发现诊断」时使用的退出码；它属于成功的分析而非失败。
const exitDiagnostics = 3

// Output caps. | 输出上限。
const (
	// maxOutputKeep caps the bytes retained in Report.Output. | maxOutputKeep 限定 Report.Output 保留的字节数。
	maxOutputKeep = 64 << 10
	// maxLineKeep caps a single unterminated line while diagnostics are counted. | maxLineKeep 限定统计诊断时单行未终止内容的长度。
	maxLineKeep = 1 << 20
)

// Declared sentinels of the package. | 本包声明的哨兵错误。
var (
	// ErrToolNotFound reports that no source yielded a betteralign executable. | ErrToolNotFound 表示所有来源都未能给出 betteralign 可执行文件。
	ErrToolNotFound = errx.New("align: betteralign binary not found | align：未找到 betteralign 可执行文件")
	// ErrRunFailed reports that the analyzer could not be started, or failed for a reason other than reporting diagnostics. | ErrRunFailed 表示分析器无法启动，或因「报告诊断」之外的原因失败。
	ErrRunFailed = errx.New("align: betteralign run failed | align：betteralign 执行失败")
	// ErrToolTooOld reports an analyzer built with a Go version older than the analyzed module's, which cannot type-check it. | ErrToolTooOld 表示分析器的 Go 版本低于被分析模块，无法对其做类型检查。
	ErrToolTooOld = errx.New("align: betteralign is older than the module's Go version | align：betteralign 版本低于模块的 Go 版本")
)

// PathLookup resolves a bare command name on PATH. | PathLookup 在 PATH 上解析裸命令名。
//
// It is a variable so tests can simulate a missing tool without touching the real PATH. | 它被声明为变量，便于测试在不触碰真实 PATH 的前提下模拟工具缺失。
var PathLookup = exec.LookPath

// ToolOptions collects every source that may point at the betteralign binary. | ToolOptions 汇总所有可能指向 betteralign 可执行文件的来源。
//
// Resolution order, highest priority first: Bin, ConfigBin, EnvBin, the project-local .yd/tools copy under LocalDir, and finally PATH. | 解析顺序由高到低：Bin、ConfigBin、EnvBin、LocalDir 下项目内的 .yd/tools 副本，最后是 PATH。
type ToolOptions struct {
	// PathLookup resolves a bare command name; nil means the package-level PathLookup. | PathLookup 解析裸命令名；nil 表示包级 PathLookup。
	PathLookup func(string) (string, error)
	// Bin is the explicit --bin flag value. | Bin 是 --bin 标志的显式取值。
	Bin string
	// ConfigBin is the align.bin configuration value. | ConfigBin 是配置项 align.bin 的取值。
	ConfigBin string
	// EnvBin is the YD_BETTERALIGN_BIN value; when empty the environment is read as well. | EnvBin 是 YD_BETTERALIGN_BIN 的取值；为空时还会去读环境变量。
	EnvBin string
	// LocalDir is the project root that hosts .yd/tools/betteralign[.exe]. | LocalDir 是承载 .yd/tools/betteralign[.exe] 的项目根目录。
	LocalDir string
}

// Resolve resolves the betteralign executable following the documented precedence chain. | Resolve 按文档化的优先级链解析 betteralign 可执行文件。
//
// A value without a path separator is treated as a command name and looked up through the injected PathLookup, so YD_BETTERALIGN_BIN=betteralign keeps working. | 不含路径分隔符的取值被视为命令名并通过注入的 PathLookup 查找，因此 YD_BETTERALIGN_BIN=betteralign 依然可用。
// ErrToolNotFound carries the installation hint when no source yields an existing binary. | 所有来源都得不到存在的可执行文件时，ErrToolNotFound 会带上安装提示。
func Resolve(o ToolOptions) (string, error) {
	envBin := o.EnvBin
	if envBin == "" {
		envBin = os.Getenv(EnvToolBin)
	}
	lookup := o.PathLookup
	if lookup == nil {
		lookup = PathLookup
	}
	local := ""
	if o.LocalDir != "" {
		local = filepath.Join(o.LocalDir, ".yd", "tools", toolName+exeSuffix())
	}

	candidates := []struct {
		source string
		value  string
	}{
		{"Bin | 标志", o.Bin},
		{"align.bin | 配置项", o.ConfigBin},
		{EnvToolBin + " | 环境变量", envBin},
		{"local bin | 本地安装", local},
	}
	for _, c := range candidates {
		if strings.TrimSpace(c.value) == "" {
			continue
		}
		if p, ok := resolveCandidate(c.value, lookup); ok {
			return p, nil
		}
	}
	// Finally fall back to the betteralign command on PATH. | 最后回退 PATH 上的 betteralign 命令。
	if p, err := lookup(toolName); err == nil && p != "" {
		return p, nil
	}
	return "", errx.Detail(ErrToolNotFound,
		"checked Bin, ConfigBin, EnvBin, %[2]s and PATH; install it with %[1]q or drop the binary into %[2]s | 已检查 Bin、ConfigBin、EnvBin、%[2]s 与 PATH；可用 %[1]q 安装，或把二进制放入 %[2]s",
		InstallHint, LocalToolDir)
}

// resolveCandidate turns one configured value into an existing executable. | resolveCandidate 把一个配置取值变成真实存在的可执行文件。
func resolveCandidate(value string, lookup func(string) (string, error)) (string, bool) {
	// A bare command name (no separator) goes through PATH lookup. | 裸命令名（无路径分隔符）交给 PATH 查找。
	if !strings.ContainsAny(value, `/\`) {
		p, err := lookup(value)
		if err != nil || p == "" {
			return "", false
		}
		return p, true
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		abs = filepath.Clean(value)
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", false
	}
	return abs, true
}

// exeSuffix returns the platform executable suffix. | exeSuffix 返回当前平台的可执行文件后缀。
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// Options describes one analysis run. | Options 描述一次分析运行。
type Options struct {
	// ExecCommand is the process factory; nil means exec.CommandContext. | ExecCommand 是进程工厂；nil 表示 exec.CommandContext。
	ExecCommand func(ctx context.Context, name string, args ...string) *exec.Cmd
	// Bin is the resolved analyzer binary; Run resolves it through Resolve when empty. | Bin 是已解析的分析器二进制；为空时 Run 会通过 Resolve 解析。
	Bin string
	// Dir is the directory the analyzer runs in; empty means the current directory. | Dir 是分析器运行所在目录；空表示当前目录。
	Dir string
	// Targets are the package patterns; empty means DefaultTarget. | Targets 是包模式；空表示 DefaultTarget。
	Targets []string
	// ExcludeDirs lists directory patterns to skip; one entry may itself hold a comma-separated list. | ExcludeDirs 列出要跳过的目录模式；单项内部也可用逗号分隔。
	ExcludeDirs []string
	// ExcludeFiles lists file patterns to skip; one entry may itself hold a comma-separated list. | ExcludeFiles 列出要跳过的文件模式；单项内部也可用逗号分隔。
	ExcludeFiles []string
	// MaxPasses caps the -apply passes; 0 means DefaultMaxPasses and a run without Apply always executes exactly one pass. | MaxPasses 限定 -apply 的遍数上限；0 表示 DefaultMaxPasses，未开启 Apply 的运行始终只执行一遍。
	MaxPasses int
	// Apply writes the suggested field order back to the sources. | Apply 把建议的字段顺序写回源码。
	Apply bool
	// IncludeTests brings test files into the analysis. | IncludeTests 把测试文件纳入分析。
	IncludeTests bool
}

// Args returns the argv handed to the analyzer. | Args 返回交给分析器的 argv。
//
// The assembly is deterministic and fully explicit: -apply is passed when Apply is set, -test is always passed explicitly (as -test when tests are included and as -test=false when they are not, so that IncludeTests has exactly one meaning instead of silently inheriting the tool's default), every non-empty exclude list becomes one comma-separated -exclude_dirs / -exclude_files value, and the package patterns close the line. | 拼装规则确定且完全显式：Apply 为真时传入 -apply；-test 一律显式传入（含测试时为 -test，不含时为 -test=false，使 IncludeTests 只有一种含义，而不会悄悄继承工具的默认值）；每个非空的排除列表成为一条逗号分隔的 -exclude_dirs / -exclude_files 取值；包模式位于末尾。
func (o Options) Args() []string {
	args := make([]string, 0, 6)
	if o.Apply {
		args = append(args, "-apply")
	}
	if o.IncludeTests {
		args = append(args, "-test")
	} else {
		args = append(args, "-test=false")
	}
	if dirs := nonEmpty(o.ExcludeDirs); len(dirs) > 0 {
		args = append(args, "-exclude_dirs", strings.Join(dirs, ","))
	}
	if files := nonEmpty(o.ExcludeFiles); len(files) > 0 {
		args = append(args, "-exclude_files", strings.Join(files, ","))
	}
	return append(args, o.patterns()...)
}

// patterns returns the package patterns of the run, defaulting to DefaultTarget. | patterns 返回本次运行的包模式，缺省为 DefaultTarget。
func (o Options) patterns() []string {
	if p := nonEmpty(o.Targets); len(p) > 0 {
		return p
	}
	return []string{DefaultTarget}
}

// nonEmpty trims a repeatable flag's entries and drops the blank ones. | nonEmpty 去除可重复标志各项的首尾空白并丢弃空项。
func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Report summarizes one analysis run. | Report 汇总一次分析运行。
type Report struct {
	// Output is the analyzer's combined stdout and stderr, capped at maxOutputKeep bytes. | Output 是分析器合并后的 stdout 与 stderr，上限为 maxOutputKeep 字节。
	Output string
	// Diagnostics counts the structs the analyzer asked to reorder; with Apply it is the count left after the last pass, and 0 therefore means the tree is aligned. | Diagnostics 统计分析器建议重排的结构体数量；开启 Apply 时它是最后一遍结束后剩余的数量，因此 0 表示整棵树已对齐。
	Diagnostics int
	// Passes is how many passes were executed; a run without Apply always reports one. | Passes 是实际执行的遍数；未开启 Apply 的运行恒为一遍。
	Passes int
	// Duration is how long the analyzer ran, summed over all passes. | Duration 是分析器运行耗时，多遍时为其总和。
	Duration time.Duration
	// Applied reports whether this run was asked to write the suggestions back. | Applied 报告本次运行是否被要求把建议写回源码。
	Applied bool
	// Converged reports whether the loop stopped because the diagnostics reached zero or stopped shrinking; it is false when the pass limit was hit while they were still shrinking. | Converged 报告循环是否因诊断归零或不再减少而结束；当诊断仍在减少却已触及遍数上限时它为 false。
	Converged bool
}

// Run executes the analyzer and turns its output into a Report. | Run 执行分析器并把输出汇总为 Report。
//
// A missing binary yields ErrToolNotFound (carrying the installation hint), a binary older than the module's Go version yields ErrToolTooOld, and any other failure is wrapped in ErrRunFailed together with the tool's output. | 二进制缺失返回 ErrToolNotFound（带安装提示），二进制低于模块的 Go 版本返回 ErrToolTooOld，其它失败连同工具输出包装进 ErrRunFailed。
// The exit code singlechecker uses for "diagnostics reported" counts as a successful run. | singlechecker 表示「已报告诊断」的退出码按成功处理。
// With Apply the analyzer is run repeatedly until it converges, because one pass does not always reach a fixed point. | 开启 Apply 时会反复执行分析器直到收敛，因为单遍并不总能到达不动点。
func Run(ctx context.Context, o Options, log *logx.Logger) (*Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	bin := strings.TrimSpace(o.Bin)
	if bin == "" {
		resolved, err := Resolve(ToolOptions{LocalDir: o.Dir})
		if err != nil {
			return nil, err
		}
		bin = resolved
	}
	if !o.Apply {
		return runOnce(ctx, bin, o, log)
	}
	return runApplyLoop(ctx, bin, o, log)
}

// runApplyLoop applies the analyzer until it converges, hits the pass limit, or fails. | runApplyLoop 反复应用分析器，直到收敛、触及遍数上限或失败。
//
// The stop rules are, in order: a pass with zero diagnostics (aligned), a pass whose diagnostics did not shrink (no further progress), and the pass limit. Only the pass limit leaves Converged false, and that case is logged as a warning because raising MaxPasses would keep improving the layout. | 结束规则依次为：某遍诊断为零（已对齐）、某遍诊断未再减少（已无进展）、以及触及遍数上限。只有触及上限会让 Converged 为 false，该情形会记录告警，因为提高 MaxPasses 仍能继续改善布局。
func runApplyLoop(ctx context.Context, bin string, o Options, log *logx.Logger) (*Report, error) {
	limit := o.MaxPasses
	if limit <= 0 {
		limit = DefaultMaxPasses
	}
	total := &Report{Applied: true}
	var buf strings.Builder
	previous := -1
	for pass := 1; pass <= limit; pass++ {
		rep, err := runOnce(ctx, bin, o, log)
		total.Passes = pass
		total.Diagnostics = rep.Diagnostics
		total.Duration += rep.Duration
		appendPassOutput(&buf, pass, rep.Output)
		if err != nil {
			total.Output = buf.String()
			return total, err
		}
		if rep.Diagnostics == 0 || (previous >= 0 && rep.Diagnostics >= previous) {
			total.Converged = true
			break
		}
		previous = rep.Diagnostics
	}
	total.Output = buf.String()
	if !total.Converged && log != nil {
		log.Warn("align: diagnostics are still shrinking at the pass limit | align：达到遍数上限时诊断数仍在下降",
			logx.Int("passes", total.Passes), logx.Int("diagnostics", total.Diagnostics), logx.Int("max_passes", limit))
	}
	return total, nil
}

// appendPassOutput appends one pass's output, labelled from the second pass on, keeping the aggregate within maxOutputKeep. | appendPassOutput 追加一遍的输出（自第二遍起加标签），并把总量控制在上限内。
func appendPassOutput(buf *strings.Builder, pass int, text string) {
	body := strings.TrimSpace(text)
	if body != "" {
		if pass > 1 {
			buf.WriteString("--- pass ")
			buf.WriteString(strconv.Itoa(pass))
			buf.WriteString(" | 第 ")
			buf.WriteString(strconv.Itoa(pass))
			buf.WriteString(" 遍 ---\n")
		}
		buf.WriteString(body)
		buf.WriteByte('\n')
	}
	if buf.Len() > maxOutputKeep {
		trimmed := buf.String()[:maxOutputKeep]
		buf.Reset()
		buf.WriteString(trimmed)
		buf.WriteString("... | 输出已截断\n")
	}
}

// runOnce executes exactly one analyzer pass. | runOnce 执行恰好一遍分析。
func runOnce(ctx context.Context, bin string, o Options, log *logx.Logger) (*Report, error) {
	args := o.Args()
	if log != nil {
		log.Debug("align: running betteralign | align：正在运行 betteralign",
			logx.Str("bin", bin), logx.Strs("args", args))
	}
	factory := o.ExecCommand
	if factory == nil {
		factory = exec.CommandContext
	}
	cmd := factory(ctx, bin, args...)
	if dir := strings.TrimSpace(o.Dir); dir != "" {
		cmd.Dir = dir
	}
	out := &output{}
	cmd.Stdout, cmd.Stderr = out, out

	start := time.Now()
	err := cmd.Run()
	// A single pass is never cut short by the pass limit, so it always counts as converged. | 单遍绝不会被遍数上限截断，因此它总是算作已收敛。
	rep := &Report{
		Diagnostics: out.Diagnostics(),
		Passes:      1,
		Applied:     o.Apply,
		Output:      out.String(),
		Duration:    time.Since(start),
		Converged:   true,
	}
	if err == nil {
		return rep, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return rep, errx.WrapDetail(ctxErr, ErrRunFailed, "betteralign cancelled | betteralign 被取消：%v", err)
	}
	// Exit code 3 means "diagnostics found": the analysis itself succeeded. | 退出码 3 表示「发现了诊断」：分析本身是成功的。
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == exitDiagnostics {
		if log != nil {
			log.Debug("align: diagnostics reported | align：已报告诊断", logx.Int("diagnostics", rep.Diagnostics))
		}
		return rep, nil
	}
	if tooOldRe.MatchString(rep.Output) {
		return rep, errx.WrapDetail(err, ErrToolTooOld, "reinstall it with %[1]q | 请用 %[1]q 重新安装", InstallHint)
	}
	return rep, errx.WrapDetail(err, ErrRunFailed, "betteralign %s | betteralign 执行失败：%s",
		err.Error(), rep.Output)
}

// ResolveAndRun resolves the analyzer through tool and then runs one analysis. | ResolveAndRun 先用 tool 解析分析器，再执行一次分析。
func ResolveAndRun(ctx context.Context, tool ToolOptions, o Options, log *logx.Logger) (*Report, error) {
	bin, err := Resolve(tool)
	if err != nil {
		return nil, err
	}
	o.Bin = bin
	return Run(ctx, o, log)
}

// diagnosticRe matches one betteralign diagnostic, e.g. "http/router/pattern.go:77:14: 8 bytes saved: struct of size 96 could be 88". | diagnosticRe 匹配一条 betteralign 诊断，例如 "http/router/pattern.go:77:14: 8 bytes saved: struct of size 96 could be 88"。
//
// Both diagnostic families are covered - the struct-size one and the pointer-bytes one - and the pattern is anchored on the analyzer's own wording, so that a compiler error mentioning a struct literal is never miscounted. | 两类诊断都被覆盖——体积类与指针字节类——且模式锚定在分析器自身的措辞上，因此提及 struct literal 的编译错误绝不会被误计。
var diagnosticRe = regexp.MustCompile(`\.go:\d+:\d+: (?:\d+ bytes saved: )?struct (?:of size \d+ could be \d+|with \d+ pointer bytes could be \d+)`)

// tooOldRe matches the signatures of an analyzer built with a Go older than the analyzed module, e.g. "package requires newer Go version go1.27 (application built with go1.26)". | tooOldRe 匹配「分析器比被分析模块的 Go 版本旧」的特征，例如 "package requires newer Go version go1.27 (application built with go1.26)"。
var tooOldRe = regexp.MustCompile(`requires newer Go version|export data version \d+ is greater than maximum supported version`)

// isDiagnostic reports whether a line is one of the analyzer's diagnostics. | isDiagnostic 报告一行是否为分析器的诊断。
func isDiagnostic(line []byte) bool { return diagnosticRe.Match(line) }

// output keeps the head of the analyzer's output while counting diagnostics on the fly, so that a chatty tool cannot exhaust memory and the count stays exact even after the retained text is truncated. | output 在保留分析器输出开头的同时即时统计诊断，因此啰嗦的工具不会耗尽内存，且即使保留文本被截断计数依然精确。
type output struct {
	retained  []byte // 输出开头，上限 maxOutputKeep。 | Head of the output, capped at maxOutputKeep.
	partial   []byte // 尚未以换行结束的当前行。 | Bytes of the line not yet terminated by a newline.
	count     int    // 目前已统计的诊断数。 | Diagnostics counted so far.
	mu        sync.Mutex
	truncated bool // 是否已丢弃后续输出。 | Whether further output was dropped.
}

// Write implements io.Writer: it counts diagnostics line by line and retains the head of the output. | Write 实现 io.Writer：逐行统计诊断并保留输出开头。
func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	raw := p
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			o.partial = append(o.partial, p...)
			if len(o.partial) > maxLineKeep {
				o.partial = o.partial[:0] // 病态长行：放弃跟踪。 | A pathological line: stop tracking it.
			}
			break
		}
		o.partial = append(o.partial, p[:i]...)
		if isDiagnostic(o.partial) {
			o.count++
		}
		o.partial = o.partial[:0]
		p = p[i+1:]
	}

	room := maxOutputKeep - len(o.retained)
	switch {
	case room >= len(raw):
		o.retained = append(o.retained, raw...)
	case room > 0:
		o.retained = append(o.retained, raw[:room]...)
		o.truncated = true
	default:
		o.truncated = len(raw) > 0
	}
	return len(raw), nil
}

// Diagnostics returns the number of diagnostics seen so far. | Diagnostics 返回目前已看到的诊断数量。
func (o *output) Diagnostics() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.count
}

// String returns the retained output, with a truncation marker once text was dropped. | String 返回保留的输出，文本被丢弃后附带截断标记。
func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.truncated {
		return string(o.retained)
	}
	return string(o.retained) + "... | 输出已截断\n"
}
