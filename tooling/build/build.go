// Package build implements the cross-platform static packaging pipeline behind `yd build`. | Package build 实现 `yd build` 背后的跨平台静态打包流水线。
//
// The pipeline is driven by Options and has five stages: | 流水线由 Options 驱动，共五个阶段：
//
//  1. an optional Tailwind CSS pass that writes the uncompressed stylesheet and, when minification
//     is on, a second `--minify` pass for the production stylesheet; |  1. 可选的 Tailwind CSS 阶段，写出未压缩样式表，并在启用压缩时以第二遍 `--minify` 产出生产样式表；
//  2. an optional JavaScript pass that writes one `.min.js` per source; |  2. 可选的 JavaScript 阶段，为每个源码写出 `.min.js`；
//  3. version injection through `-ldflags -X github.com/shibingli/yd/core/version.*`; |  3. 通过 `-ldflags -X github.com/shibingli/yd/core/version.*` 注入版本信息；
//  4. CGO-free compilation (`CGO_ENABLED=0`) for every requested target; |  4. 为每个目标平台执行无 CGO 编译（`CGO_ENABLED=0`）；
//  5. verification of every artifact: existence, size and executable magic. |  5. 校验每个产物：存在性、大小与可执行魔数。
//
// The result is a single statically linked file per target, which is exactly what a
// scratch container needs. Every external command goes through Options.ExecCommand,
// so tests can fake the toolchain completely. | 每个目标产出一个静态链接的单文件，正是 scratch
// 容器所需。所有外部命令都经由 Options.ExecCommand 执行，因此测试可以完整伪造工具链。
package build

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
)

// Sentinel errors of the packaging pipeline. | 打包流水线的哨兵错误。
var (
	// ErrInvalidOptions indicates that the options could not be normalized, e.g. a build directory that cannot be resolved. | ErrInvalidOptions 表示选项无法归一化，例如构建目录无法解析。
	ErrInvalidOptions = errs.New("build: invalid build options | build：非法的构建选项")
	// ErrCompile indicates that `go build` failed for one target. | ErrCompile 表示某个目标平台的 `go build` 失败。
	ErrCompile = errs.New("build: go build failed | build：go build 执行失败")
)

// ExecCommandFunc constructs an external command; it mirrors exec.CommandContext so
// that the default can be replaced verbatim. | ExecCommandFunc 构造外部命令；签名与
// exec.CommandContext 一致，便于原样替换默认实现。
type ExecCommandFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

// Options configures one packaging run. | Options 配置一次打包运行。
type Options struct {
	// TailwindFunc overrides the built-in Tailwind implementation; nil means
	// BuildTailwind, and the CLI injects toolchain.BuildCSS here once it exists. | TailwindFunc 覆盖内置的
	// Tailwind 实现；为 nil 表示使用 BuildTailwind，toolchain 包就绪后由命令行注入 toolchain.BuildCSS。
	TailwindFunc TailwindFunc
	// ExecCommand overrides the external command constructor; nil means
	// exec.CommandContext. | ExecCommand 覆盖外部命令构造器；为 nil 表示 exec.CommandContext。
	ExecCommand ExecCommandFunc
	// Tailwind configures the optional stylesheet stage. | Tailwind 配置可选的样式表阶段。
	Tailwind TailwindOptions
	// Minify configures the optional JavaScript minification stage. | Minify 配置可选的 JavaScript 压缩阶段。
	Minify MinifyOptions
	// MinifyFunc overrides the built-in JavaScript minifier; nil means BuildMinify. | MinifyFunc 覆盖内置 JavaScript 压缩实现；nil 表示 BuildMinify。
	MinifyFunc MinifyFunc
	// Dir is the project directory, i.e. the module root; empty means the current directory. | Dir 是项目目录（模块根）；为空表示当前目录。
	Dir string
	// Output is the artifact path of a single-target build; empty means bin/<name>.
	// It is ignored when more than one target is requested, because multi-target
	// builds always follow the dist/<name>_<os>_<arch> convention. An existing
	// directory (or a path ending with a separator) receives the default file name. | Output 是单目标构建的产物路径；为空表示 bin/<name>。
	// 当目标多于一个时该字段被忽略，因为多目标构建始终遵循 dist/<name>_<os>_<arch> 约定。
	// 指向已存在目录（或以路径分隔符结尾）时，默认文件名会被追加在该目录之下。
	Output string
	// Name is the artifact base name; empty means the last segment of the go.mod module path, falling back to the directory name. | Name 是产物基础名；为空表示 go.mod module 路径的末段，回退为目录名。
	Name string
	// Package is the build entry passed to the toolchain; empty means "." (the module root). | Package 是传给工具链的构建入口；为空表示 "."（模块根）。
	Package string
	// Version, Commit and Date are injected into github.com/shibingli/yd/core/version. | Version、Commit 与 Date 会被注入 github.com/shibingli/yd/core/version。
	Version string
	// Commit is the git revision recorded at build time. | Commit 是构建时记录的 git 修订。
	Commit string
	// Date is the RFC3339 build timestamp. | Date 是 RFC3339 格式的构建时间。
	Date string
	// Targets are the GOOS/GOARCH pairs to build; empty means the host platform. | Targets 是待构建的 GOOS/GOARCH 组合；为空表示主机平台。
	Targets []Target
	// LDFlags are extra linker flags appended after -s -w. | LDFlags 是追加在 -s -w 之后的额外链接参数。
	LDFlags []string
	// Tags are the build tags joined with commas and passed through -tags. | Tags 是以逗号连接并经 -tags 传入的构建标签。
	Tags []string
	// Env holds extra environment variables for the toolchain, overriding both the
	// process environment and the defaults installed by Build. | Env 保存传给工具链的额外环境变量，
	// 优先于进程环境与 Build 安装的默认值。
	Env []string
	// Embed marks the binary as carrying go:embed assets, which turns the Tailwind
	// stage on even when Tailwind.Enabled is false, so that the embedded stylesheet
	// is always regenerated before compilation. | Embed 标记二进制内嵌 go:embed 资产；即使
	// Tailwind.Enabled 为 false 也会启用 Tailwind 阶段，保证内嵌样式表在编译前始终是最新的。
	Embed bool
	// Offline forces -mod=vendor so that no module is fetched from the network. | Offline 强制 -mod=vendor，不从网络拉取任何模块。
	Offline bool
	// TrimPath adds -trimpath, dropping local file system paths from the binary and
	// making builds reproducible. The yd CLI enables it by default. | TrimPath 追加
	// -trimpath，从二进制中剔除本地文件系统路径并使构建可复现；yd 命令行默认开启。
	TrimPath bool
}

// Artifact describes one verified build output. | Artifact 描述一个已校验的构建产物。
type Artifact struct {
	// Path is the absolute path of the produced executable. | Path 是产物的绝对路径。
	Path string
	// GOOS is the target operating system. | GOOS 是目标操作系统。
	GOOS string
	// GOARCH is the target architecture. | GOARCH 是目标架构。
	GOARCH string
	// Bytes is the artifact size in bytes. | Bytes 是产物的字节数。
	Bytes int64
	// Duration is the wall-clock time spent compiling and verifying this artifact. | Duration 是该产物的编译与校验耗时。
	Duration time.Duration
}

// Report summarizes one packaging run. | Report 汇总一次打包运行。
type Report struct {
	// Artifacts holds every artifact in target order. | Artifacts 按目标顺序保存全部产物。
	Artifacts []Artifact
}

// execCommand returns the command constructor of this run. | execCommand 返回本次运行使用的命令构造器。
func (o Options) execCommand() ExecCommandFunc {
	if o.ExecCommand != nil {
		return o.ExecCommand
	}
	return exec.CommandContext
}

// withDefaults fills every zero-valued option and normalizes the build directory to
// an absolute path. | withDefaults 填充全部零值选项，并把构建目录归一化为绝对路径。
func (o Options) withDefaults() (Options, error) {
	if o.Dir == "" {
		o.Dir = "."
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return o, errs.WrapDetail(err, ErrInvalidOptions, "cannot resolve build dir %q | 无法解析构建目录 %q", o.Dir, o.Dir)
	}
	o.Dir = dir
	if o.Name == "" {
		o.Name = projectName(dir)
	}
	if o.Package == "" {
		o.Package = "."
	}
	if len(o.Targets) == 0 {
		o.Targets = []Target{{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}}
	}
	if o.Tailwind.Input == "" {
		o.Tailwind.Input = DefaultTailwindInput
	}
	if o.Tailwind.Output == "" {
		o.Tailwind.Output = DefaultTailwindOutput
	}
	if o.Tailwind.MinOutput == "" {
		o.Tailwind.MinOutput = DefaultTailwindMinOutput
	}
	if o.Minify.Root == "" {
		o.Minify.Root = DefaultMinifyRoot
	}
	return o, nil
}

// outputFor returns the absolute artifact path of one target: dist/<name>_<os>_<arch>
// for multi-target builds, Output (or bin/<name>) for single-target ones, always with
// a .exe suffix on Windows. | outputFor 返回单个目标的产物绝对路径：多目标构建为
// dist/<name>_<os>_<arch>，单目标为 Output（或 bin/<name>），Windows 一律带 .exe 后缀。
func (o Options) outputFor(target Target) string {
	name := o.Name + exeSuffix(target.GOOS)
	if len(o.Targets) > 1 {
		return filepath.Join(o.Dir, "dist", o.Name+"_"+target.GOOS+"_"+target.GOARCH+exeSuffix(target.GOOS))
	}
	if o.Output == "" {
		return filepath.Join(o.Dir, "bin", name)
	}
	out := resolvePath(o.Dir, o.Output)
	if isDir(out) || strings.HasSuffix(o.Output, "/") || strings.HasSuffix(o.Output, `\`) {
		return filepath.Join(out, name)
	}
	return out
}

// isDir reports whether path exists and is a directory. | isDir 判断 path 是否存在且为目录。
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// exeSuffix returns the executable suffix of a GOOS. | exeSuffix 返回某个 GOOS 的可执行文件后缀。
func exeSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// projectName derives the project name from the module path in go.mod, falling back
// to the base name of dir. | projectName 从 go.mod 的 module 路径推导项目名，回退为 dir 的目录名。
func projectName(dir string) string {
	if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
		for _, line := range splitLines(string(data)) {
			rest, ok := strings.CutPrefix(line, "module ")
			if !ok {
				continue
			}
			if name := path.Base(strings.Trim(strings.TrimSpace(rest), `"`)); name != "" && name != "." && name != "/" {
				return name
			}
		}
	}
	return filepath.Base(dir)
}

// goSumPresent reports whether the build directory already carries a go.sum. | goSumPresent 报告构建目录
// 是否已有 go.sum。
func goSumPresent(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "go.sum"))
	return err == nil && !info.IsDir()
}

// buildArgs assembles the argv of one `go build` invocation. | buildArgs 组装一次 `go build` 调用的 argv。
func (o Options) buildArgs(target Target, out string) []string {
	args := []string{"build"}
	if o.TrimPath {
		args = append(args, "-trimpath")
	}
	if len(o.Tags) > 0 {
		args = append(args, "-tags", strings.Join(o.Tags, ","))
	}
	if o.Offline {
		args = append(args, "-mod=vendor")
	} else if !goSumPresent(o.Dir) {
		// A freshly scaffolded project has no go.sum yet. Without -mod=mod every third-party
		// import fails with "missing go.sum entry" instead of resolving once; the command then
		// writes go.sum as any first `go build` does. Offline mode stays vendor-only. | 新生成的
		// 脚手架项目还没有 go.sum。不加 -mod=mod 时，每个第三方导入都会以 missing go.sum entry 失败，
		// 而不是解析一次；随后 go.sum 会像任何首次 `go build` 一样被写出。离线模式仍只用 vendor。
		args = append(args, "-mod=mod")
	}
	args = append(args, GenerateLDFlags(o)...)
	args = append(args, "-o", out, o.Package)
	return args
}

// buildEnv merges the toolchain environment for one target: CGO is always disabled
// so the artifact stays static, GOOS/GOARCH are pinned, and caller supplied Env
// entries win over both. | buildEnv 合并单个目标的工具链环境：始终关闭 CGO 以保证产物静态链接，
// 固定 GOOS/GOARCH，调用方传入的 Env 条目则优先于两者。
func (o Options) buildEnv(target Target) []string {
	return []string{
		"CGO_ENABLED=0",
		"GOOS=" + target.GOOS,
		"GOARCH=" + target.GOARCH,
	}
}

// mergeEnv overlays override groups onto base with last-wins semantics and a stable
// order, so that a caller can replace a variable inherited from the process
// environment instead of relying on duplicate entries. | mergeEnv 以"后者优先"语义把若干
// override 组覆盖到 base 上并保持顺序稳定，使调用方可以替换从进程环境继承的变量，而不必依赖重复条目。
func mergeEnv(base []string, overrides ...[]string) []string {
	index := make(map[string]int, len(base)+8)
	merged := make([]string, 0, len(base)+8)
	put := func(kv string) {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			return
		}
		if i, seen := index[key]; seen {
			merged[i] = kv
			return
		}
		index[key] = len(merged)
		merged = append(merged, kv)
	}
	for _, kv := range base {
		put(kv)
	}
	for _, group := range overrides {
		for _, kv := range group {
			put(kv)
		}
	}
	return merged
}

// PrepareAssets runs the packaging asset stages shared by `yd build`, `yd docker` and `yd ko`:
// Tailwind first (it produces yd.css) and minify second (it produces the .min.js/.min.svg siblings).
// A missing optional tool skips only its own stage with a warning; a real failure is returned. |
// PrepareAssets 运行 `yd build`、`yd docker` 与 `yd ko` 共用的打包资产阶段：先 Tailwind（产出 yd.css），
// 再 minify（产出 .min.js/.min.svg 同族文件）。缺少可选工具时只跳过对应阶段并告警，真实失败才返回。
func PrepareAssets(ctx context.Context, dir string, log *ydlog.Logger) error {
	if log == nil {
		log = ydlog.Default()
	}
	size, err := BuildTailwind(ctx, dir, TailwindOptions{Enabled: true, Minify: true}, log)
	switch {
	case err == nil:
		log.Info("tailwind stage finished | Tailwind 阶段已完成", ydlog.Int64("bytes", size))
	case errors.Is(err, ErrTailwindNotFound), errors.Is(err, ErrTailwindInput):
		log.Warn("tailwind stage skipped | 跳过 Tailwind 阶段", ydlog.Err(err))
	default:
		return err
	}
	if _, err := BuildMinify(ctx, dir, MinifyOptions{Enabled: true}, log); err != nil {
		if errors.Is(err, ErrMinifyNotFound) {
			log.Warn("minify stage skipped | 跳过压缩阶段", ydlog.Err(err))
			return nil
		}
		return err
	}
	return nil
}

// Build compiles every requested target into a static single-file executable and
// returns the verified artifacts. | Build 把每个目标编译为静态单文件可执行程序，并返回已校验的产物。
//
// Steps: (1) the optional Tailwind stage, (2) version injection through ldflags,
// (3) `go build` with CGO_ENABLED=0 (plus -mod=vendor when Offline) and (4) magic,
// size and existence verification. A failure in any step aborts the run and the
// compiler output is carried by the returned error. | 步骤：(1) 可选的 Tailwind 阶段，
// (2) 可选的 JavaScript 压缩阶段，(3) 通过 ldflags 注入版本信息，(4) 以 CGO_ENABLED=0 执行
// `go build`（Offline 时附带 -mod=vendor），(5) 校验魔数、大小与存在性。任一步失败都会中止运行，
// 编译器输出随返回的错误一并带出。
func Build(ctx context.Context, o Options, log *ydlog.Logger) (*Report, error) {
	if log == nil {
		log = ydlog.Default()
	}
	o, err := o.withDefaults()
	if err != nil {
		return nil, err
	}

	if o.Tailwind.Enabled || o.Embed {
		// Embed implies the asset stage, so the flag is normalized for the hook. | Embed 隐含资产阶段，因此为钩子归一化该标志。
		o.Tailwind.Enabled = true
		size, err := o.buildTailwind(ctx, log)
		if err != nil {
			return nil, err
		}
		log.Info("tailwind stage finished | Tailwind 阶段已完成",
			ydlog.Int64("bytes", size),
			ydlog.Str("output", outputPath(o.Dir, o.Tailwind)),
		)
	}

	if o.Minify.Enabled {
		res, err := o.runMinify(ctx, log)
		if err != nil {
			return nil, err
		}
		log.Info("minify stage finished | 资产压缩阶段已完成",
			ydlog.Int("files", res.Files),
			ydlog.Int64("bytes", res.Bytes),
		)
	}

	report := &Report{Artifacts: make([]Artifact, 0, len(o.Targets))}
	for _, target := range o.Targets {
		out := o.outputFor(target)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, errs.WrapDetail(err, ErrInvalidOptions, "cannot create %s | 无法创建 %s", filepath.Dir(out), filepath.Dir(out))
		}

		start := time.Now()
		args := o.buildArgs(target, out)
		cmd := o.execCommand()(ctx, "go", args...)
		cmd.Dir = o.Dir
		base := cmd.Env
		if base == nil {
			base = os.Environ()
		}
		cmd.Env = mergeEnv(base, o.buildEnv(target), o.Env)
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		if err := cmd.Run(); err != nil {
			return nil, errs.WrapDetail(err, ErrCompile, "go build %[1]s failed | 编译 %[1]s 失败: %[2]s", target, strings.TrimSpace(buf.String()))
		}

		artifact, err := verifyArtifact(out, target, time.Since(start))
		if err != nil {
			return nil, err
		}
		report.Artifacts = append(report.Artifacts, *artifact)
		log.Info("artifact built | 产物已生成",
			ydlog.Str("target", target.String()),
			ydlog.Str("path", artifact.Path),
			ydlog.Int64("bytes", artifact.Bytes),
			ydlog.Dur("duration", artifact.Duration),
		)
	}
	return report, nil
}

// buildTailwind runs the asset stage through the injected hook when present and
// through the built-in implementation otherwise. | buildTailwind 优先通过注入的钩子执行资产阶段，
// 无钩子时使用内置实现。
func (o Options) buildTailwind(ctx context.Context, log *ydlog.Logger) (int64, error) {
	if o.TailwindFunc != nil {
		return o.TailwindFunc(ctx, o.Dir, o.Tailwind)
	}
	return buildTailwindWith(ctx, o.Dir, o.Tailwind, log, o.execCommand())
}

// runMinify runs the JavaScript minification stage through the injected hook
// when present and through the built-in implementation otherwise. | runMinify
// 优先通过注入的钩子执行 JavaScript 压缩阶段，无钩子时使用内置实现。
func (o Options) runMinify(ctx context.Context, log *ydlog.Logger) (MinifyResult, error) {
	if o.MinifyFunc != nil {
		return o.MinifyFunc(ctx, o.Dir, o.Minify)
	}
	return buildMinifyWith(ctx, o.Dir, o.Minify, log, o.execCommand())
}
