// Package tools is the single probe-and-install entry point for the optional external tools the
// framework shells out to. | Package tools 是框架所调用可选外部工具的统一探测与安装入口。
//
// Five tools are optional by design, and each resolver chain is owned by the package that uses it: | 按设计共有五个可选工具，
// 各自的解析链由使用它的包负责：
//
//   - betteralign, behind align.Resolve, reports struct field orders that waste memory (CLAUDE.md §8); |  - betteralign 由 align.Resolve 解析，报告浪费内存的结构体字段顺序（CLAUDE.md §8）；
//   - ko, behind ko.Resolve, builds images without a Dockerfile (CLAUDE.md §13); |  - ko 由 ko.Resolve 解析，无需 Dockerfile 构建镜像（CLAUDE.md §13）；
//   - tailwindcss, behind toolchain.ResolveTailwind, compiles the stylesheets; |  - tailwindcss 由 toolchain.ResolveTailwind 解析，编译样式表；
//   - minify, behind build.ResolveMinifyBin, minifies the JavaScript assets; |  - minify 由 build.ResolveMinifyBin 解析，压缩 JavaScript 资产；
//   - docker, looked up on PATH, builds and runs the container images. |  - docker 在 PATH 上查找，构建并运行容器镜像。
//
// What this package adds is the one place that can answer "what is still missing on this machine",
// and the one command that installs it: Registry lists the five entries, Check probes them through
// their own chains, Hints renders the bilingual recovery hints, and Install either runs `go install`
// with GOBIN, GOPATH, GOMODCACHE and GOCACHE pinned inside <dir>/.yd/tools, or downloads the
// tailwindcss standalone binary into the same directory. Both routes land on the project-local
// segment every chain already looks at. | 本包补充的是那个能回答「这台机器还缺什么」的唯一入口，以及那条统一的
// 安装命令：Registry 列出四个条目，Check 按各自链条探测，Hints 渲染双语恢复提示，Install 或以固定在
// <dir>/.yd/tools 内的 GOBIN、GOPATH、GOMODCACHE 与 GOCACHE 运行 `go install`，或把 tailwindcss
// standalone 二进制下载到同一目录。两条路径都落在每条解析链本来就查看的项目内一段。
//
// The probe is read-only: Check, Missing and Hints never install anything, never reach the network
// and never execute a tool, so a missing tool can only ever produce a hint. Versions are therefore
// left empty while probing and filled in by Install, once a tool has actually been installed. | 探测是只读的：
// Check、Missing 与 Hints 不安装任何东西、不触网、也不执行任何工具，因此工具缺失只会产出提示。版本号因此在探测时留空，
// 由 Install 在工具真正安装后填入。
//
// Dependency direction: tools depends on align, ko, toolchain and build - never the other way
// around. The resolvers must not import this package, otherwise the cycle would force a second copy
// of their InstallHint constants, and two copies of a hint are exactly how hints drift apart. | 依赖方向：
// tools 依赖 align、ko、toolchain、build，反向绝不成立。各解析包不得 import 本包，否则成环会迫使它们的
// InstallHint 常量出现第二份副本，而两份提示正是提示漂移的根源。
//
// Every failure path references a package-level sentinel declared here, so callers decide with
// errors.Is instead of string matching. | 所有失败路径都引用本文件声明的包级哨兵，调用方用 errors.Is 判定而不做字符串匹配。
package tools

import (
	"bytes"
	"context"
	"os/exec"

	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
)

// Declared sentinels of the package. | 本包声明的哨兵错误。
var (
	// ErrUnknownTool reports a name that is not in the registry. | ErrUnknownTool 表示名称不在注册表中。
	ErrUnknownTool = errs.New("tools: unknown tool | tools：未知工具")
	// ErrNoGoToolchain reports that no go command is on PATH, so nothing can be installed. | ErrNoGoToolchain 表示 PATH 上没有 go 命令，因此无法安装任何工具。
	ErrNoGoToolchain = errs.New("tools: go toolchain not found | tools：未找到 Go 工具链")
	// ErrNotInstallable reports an entry that cannot be installed with go install. | ErrNotInstallable 表示该条目无法用 go install 安装。
	ErrNotInstallable = errs.New("tools: tool cannot be installed automatically | tools：该工具无法自动安装")
	// ErrInstallFailed reports a failed installation: either go install itself failed, or the
	// verification that follows it could not resolve the tool. | ErrInstallFailed 表示安装失败：或 go install 本身失败，或随后的验证无法解析到该工具。
	ErrInstallFailed = errs.New("tools: installation failed | tools：安装失败")
)

// LocalToolDir is the project-local drop-in directory shared by every resolver, relative to the
// project root, i.e. the third segment of the resolution chain. | LocalToolDir 是各解析器共用的项目内放置目录，
// 相对项目根，也就是解析链的第三段。
const LocalToolDir = ".yd/tools"

// ExecCommandFunc runs one external command and returns its merged stdout and stderr. | ExecCommandFunc
// 执行一个外部命令并返回合并后的 stdout 与 stderr。
//
// It mirrors the injection point of the docker and ko packages, which is what lets every test here
// run without a real `go install` and without touching the network. | 它与 docker、ko 两个包的注入点同形，
// 因此本包的每个测试都不需要真正执行 `go install`，也不需要接触网络。
type ExecCommandFunc func(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, error)

// ExecCommand runs one external command and is the injection point of the installer; nil means
// runCommand. | ExecCommand 执行一个外部命令，是安装器的注入点；为 nil 表示 runCommand。
var ExecCommand ExecCommandFunc = runCommand

// PathLookup resolves a bare command name on PATH. | PathLookup 在 PATH 上解析裸命令名。
//
// It is a variable for the same reason align.PathLookup is: tests simulate a machine without the
// optional tools, or with a specific set of them, without touching the real PATH. | 它与 align.PathLookup
// 出于同一原因被声明为变量：测试可以在不触碰真实 PATH 的前提下模拟「机器上没有这些可选工具」或「只有某几个」。
var PathLookup = exec.LookPath

// Tool describes one optional external tool. | Tool 描述一个可选外部工具。
//
// A Tool is a value: Registry hands out copies, and nothing but the test suite ever replaces one. | Tool 是值类型：Registry 交出的是副本，
// 除测试之外没有任何代码会替换它。
type Tool struct {
	// Name is the stable identifier and the file name of the project-local copy under .yd/tools. | Name 是稳定标识，同时也是 .yd/tools 下项目内副本的文件名。
	Name string
	// Summary is the one-line bilingual purpose of the tool. | Summary 是双语的一句话用途。
	Summary string
	// InstallHint is the full installation command, or how to obtain the tool when it cannot be
	// installed automatically. | InstallHint 是完整的安装命令；无法自动安装时是获取途径。
	InstallHint string
	// GoInstall is the go install target including @latest; empty means the tool cannot be
	// installed through go install and is only reported. | GoInstall 是含 @latest 的 go install 目标；
	// 为空表示该工具无法经 go install 安装，只做报告。
	GoInstall string
	// DocURL points at the tool's documentation; it may be empty. | DocURL 指向工具的文档；可以为空。
	DocURL string
	// VersionArgs are the arguments of the version query; empty means no query at all. | VersionArgs 是版本查询的参数；为空表示完全不查询。
	VersionArgs []string
}

// Status is the probe result of one tool. | Status 是单个工具的探测结果。
type Status struct {
	// Err is an unexpected resolution failure: it is set only when the resolver failed for a reason
	// other than "not found", which is reported truthfully instead of being folded into a miss. | Err
	// 是意外的解析失败：仅当解析器以「未找到」之外的原因失败时设置，此时如实上报而不并入缺失。
	Err error
	// Path is the resolved executable; empty when the tool was not found. | Path 是解析出的可执行文件；未找到时为空。
	Path string
	// Version is the version reported after an installation; it stays empty when no query ran, and
	// when the query failed. | Version 是安装后报告的版本；未执行查询或查询失败时留空。
	Version string
	// Tool is the registry entry this status belongs to, so a caller can render the hint without a
	// second lookup. | Tool 是本状态所属的注册表条目，使调用方无需二次查找即可渲染提示。
	Tool Tool
	// Found reports whether the executable was resolved. | Found 报告是否解析到了可执行文件。
	Found bool
}

// Failure records one tool that could not be installed. | Failure 记录一个安装失败的工具。
type Failure struct {
	// Err is the failure; it matches ErrInstallFailed or ErrNotInstallable through errors.Is. | Err
	// 是失败原因；可用 errors.Is 匹配 ErrInstallFailed 或 ErrNotInstallable。
	Err error
	// Name is the registry name of the tool. | Name 是该工具的注册表名称。
	Name string
}

// Report summarizes one installation run. | Report 汇总一次安装运行。
//
// The three groups are disjoint: Installed holds the tools this run installed and verified,
// Skipped holds the ones that needed no action at all, and Failed holds the ones that went wrong. | 三组互不重叠：
// Installed 保存本次运行安装并验证通过的工具，Skipped 保存完全无需操作的工具，Failed 保存出错的那几个。
type Report struct {
	// Installed lists the tools this run installed, in registry order. | Installed 按注册表顺序列出本次运行安装的工具。
	Installed []Status
	// Skipped lists the tools that needed no action: the ones that were already available. | Skipped
	// 列出无需操作的工具：已经可用的那些。
	Skipped []Status
	// Failed lists the tools whose installation failed. | Failed 列出安装失败的工具。
	Failed []Failure
}

// runCommand is the default ExecCommandFunc. | runCommand 是默认的 ExecCommandFunc。
//
// The child's stdout and stderr are merged into one buffer, because a failed `go install` explains
// itself on stderr while a successful one may say nothing at all, and the caller of a failed run
// wants both. | 子进程的 stdout 与 stderr 被合并进同一个缓冲区，因为失败的 `go install` 在 stderr 上解释自己、
// 成功时可能什么都不说，而失败运行的调用方两者都要。
func runCommand(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// execCommand returns the command runner of this package. | execCommand 返回本包的命令执行器。
func execCommand() ExecCommandFunc {
	if ExecCommand != nil {
		return ExecCommand
	}
	return runCommand
}

// tailHead returns at most limit bytes of s, taken from the rear, so that a long output keeps the
// part that carries the failure. | tailHead 返回 s 末尾至多 limit 字节，使冗长的输出保留承载失败的那一段。
func tailHead(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[len(s)-limit:]
}

// loggerOr returns log, or the default logger when it is nil, so no code path has to nil-check. | loggerOr
// 返回 log，为 nil 时返回默认日志器，使任何代码路径都不必判空。
func loggerOr(log *ydlog.Logger) *ydlog.Logger {
	if log != nil {
		return log
	}
	return ydlog.Default()
}

// contextOr returns ctx, or a background context when it is nil. | contextOr 返回 ctx，为 nil 时返回后台上下文。
func contextOr(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}
