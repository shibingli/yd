package build

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/logx"
)

// Sentinel errors of the asset stage. | 资产阶段的哨兵错误。
var (
	// ErrTailwindNotFound indicates that no tailwindcss executable could be located. | ErrTailwindNotFound 表示未能定位到 tailwindcss 可执行文件。
	ErrTailwindNotFound = errx.New("build: tailwindcss executable not found | build：未找到 tailwindcss 可执行文件")
	// ErrTailwindInput indicates that the Tailwind input stylesheet does not exist. | ErrTailwindInput 表示 Tailwind 输入样式表不存在。
	ErrTailwindInput = errx.New("build: tailwind input stylesheet missing | build：缺少 Tailwind 输入样式表")
	// ErrTailwind indicates that the tailwindcss process failed or produced nothing. | ErrTailwind 表示 tailwindcss 进程失败或未产出结果。
	ErrTailwind = errx.New("build: tailwind compilation failed | build：Tailwind 编译失败")
)

const (
	// DefaultTailwindInput is the stylesheet scanned by Tailwind, relative to the build directory. | DefaultTailwindInput 是 Tailwind 扫描的样式表，相对构建目录。
	DefaultTailwindInput = "web/static/css/app.css"
	// DefaultTailwindOutput is the minified stylesheet embedded by go:embed, relative to the build directory. | DefaultTailwindOutput 是由 go:embed 内嵌的压缩样式表，相对构建目录。
	DefaultTailwindOutput = "web/static/css/yd.css"
	// tailwindEnvBin is the environment variable holding the tailwindcss path. | tailwindEnvBin 是保存 tailwindcss 路径的环境变量。
	tailwindEnvBin = "YD_TAILWIND_BIN"
	// localToolsDir is the project-local directory managed by `yd assets`. | localToolsDir is `.yd/tools`，由 `yd assets` 管理的项目内目录。
	localToolsDir = ".yd/tools"
)

// TailwindOptions configures the optional Tailwind CSS stage. | TailwindOptions 配置可选的 Tailwind CSS 阶段。
type TailwindOptions struct {
	// Bin overrides the tailwindcss executable; empty means auto-detection. | Bin 覆盖 tailwindcss 可执行文件；为空表示自动探测。
	Bin string
	// Input is the scanned stylesheet; empty means DefaultTailwindInput. | Input 是被扫描的样式表；为空表示 DefaultTailwindInput。
	Input string
	// Output is the minified stylesheet; empty means DefaultTailwindOutput. | Output 是压缩后的样式表；为空表示 DefaultTailwindOutput。
	Output string
	// Enabled turns the stage on. | Enabled 启用该阶段。
	Enabled bool
	// Minify asks tailwindcss for the minified output. | Minify 要求 tailwindcss 输出压缩结果。
	Minify bool
}

// TailwindFunc builds the Tailwind stylesheet and returns the number of bytes
// written. It is the injection point for toolchain.BuildCSS once the toolchain
// package exists; a nil hook means using BuildTailwind. | TailwindFunc 构建
// Tailwind 样式表并返回写入的字节数。它是 toolchain.BuildCSS 的注入点（待 toolchain 包就绪后接入）；
// 钩子为 nil 时使用 BuildTailwind。
type TailwindFunc func(ctx context.Context, dir string, o TailwindOptions) (int64, error)

// tailwindBinName returns the platform specific file name of the standalone
// tailwindcss binary. | tailwindBinName 返回 tailwindcss standalone 二进制的平台相关文件名。
func tailwindBinName() string {
	if runtime.GOOS == "windows" {
		return "tailwindcss.exe"
	}
	return "tailwindcss"
}

// inputPath resolves the stylesheet scanned by Tailwind. | inputPath 解析 Tailwind 扫描的样式表路径。
func inputPath(dir string, o TailwindOptions) string {
	return resolvePath(dir, orDefault(o.Input, DefaultTailwindInput))
}

// outputPath resolves the stylesheet produced by Tailwind. | outputPath 解析 Tailwind 产出的样式表路径。
func outputPath(dir string, o TailwindOptions) string {
	return resolvePath(dir, orDefault(o.Output, DefaultTailwindOutput))
}

// orDefault returns value when it is non-empty and fallback otherwise. | orDefault 在 value 非空时返回它，否则返回 fallback。
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// resolvePath resolves p against dir unless it is already absolute. | resolvePath 在 p 非绝对路径时按 dir 展开。
func resolvePath(dir, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, filepath.FromSlash(p))
}

// ResolveTailwindBin locates the tailwindcss executable in the documented order:
// the explicit bin argument, the YD_TAILWIND_BIN environment variable, the
// project-local .yd/tools directory (managed by `yd assets`) and finally PATH.
// An unresolvable location yields ErrTailwindNotFound. | ResolveTailwindBin 按文档化的
// 顺序定位 tailwindcss 可执行文件：显式 bin 参数、YD_TAILWIND_BIN 环境变量、项目内
// .yd/tools 目录（由 `yd assets` 维护）、最后是 PATH。全都找不到时返回 ErrTailwindNotFound。
func ResolveTailwindBin(dir, bin string) (string, error) {
	if bin != "" {
		return resolveBin(dir, bin)
	}
	if env := strings.TrimSpace(os.Getenv(tailwindEnvBin)); env != "" {
		return resolveBin(dir, env)
	}
	local := filepath.Join(dir, filepath.FromSlash(localToolsDir), tailwindBinName())
	if info, err := os.Stat(local); err == nil && !info.IsDir() {
		return local, nil
	}
	if found, err := exec.LookPath(tailwindBinName()); err == nil {
		return found, nil
	}
	return "", errx.Detail(ErrTailwindNotFound,
		"tried --tailwind-bin, $%s, %s and PATH | 已尝试 --tailwind-bin、$%s、%s 与 PATH", tailwindEnvBin, localToolsDir, tailwindEnvBin, localToolsDir)
}

// resolveBin resolves one explicit executable reference: a value containing a path
// separator is joined with dir, anything else goes through PATH lookup. | resolveBin 解析
// 单个显式可执行文件引用：含路径分隔符的取值与 dir 拼接，其余走 PATH 查找。
func resolveBin(dir, bin string) (string, error) {
	if !strings.ContainsRune(bin, filepath.Separator) && !strings.Contains(bin, "/") {
		found, err := exec.LookPath(bin)
		if err != nil {
			return "", errx.WrapDetail(err, ErrTailwindNotFound, "%s | %s", bin, bin)
		}
		return found, nil
	}
	path := bin
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", errx.Detail(ErrTailwindNotFound, "%s is not an executable file | %s 不是可执行文件", path, path)
	}
	return path, nil
}

// BuildTailwind runs the standalone tailwindcss binary and returns the size of the
// produced stylesheet. Defaults are applied for the input, output and binary
// location; a missing input stylesheet yields ErrTailwindInput, a missing binary
// yields ErrTailwindNotFound and a failing process yields ErrTailwind. | BuildTailwind
// 运行 tailwindcss standalone 二进制并返回产出样式表的字节数。输入、输出与二进制位置都会套用默认值；
// 输入样式表缺失返回 ErrTailwindInput，二进制缺失返回 ErrTailwindNotFound，进程失败返回 ErrTailwind。
func BuildTailwind(ctx context.Context, dir string, o TailwindOptions, log *logx.Logger) (int64, error) {
	return buildTailwindWith(ctx, dir, o, log, exec.CommandContext)
}

// buildTailwindWith is BuildTailwind with an injectable command constructor, used
// by Build so that Options.ExecCommand also drives the asset stage and tests can
// fake tailwindcss everywhere. | buildTailwindWith 是接受可注入命令构造器的 BuildTailwind，
// Build 借此让 Options.ExecCommand 同样驱动资产阶段，测试可在任意平台伪造 tailwindcss。
func buildTailwindWith(ctx context.Context, dir string, o TailwindOptions, log *logx.Logger, run ExecCommandFunc) (int64, error) {
	input := inputPath(dir, o)
	if info, err := os.Stat(input); err != nil || info.IsDir() {
		return 0, errx.Detail(ErrTailwindInput, "%s is missing | %s 不存在", input, input)
	}
	bin, err := ResolveTailwindBin(dir, o.Bin)
	if err != nil {
		return 0, err
	}
	output := outputPath(dir, o)

	args := []string{"-i", input, "-o", output}
	if o.Minify {
		args = append(args, "--minify")
	}
	cmd := run(ctx, bin, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		return 0, errx.WrapDetail(err, ErrTailwind, "%s | %s: %s", bin, bin, strings.TrimSpace(buf.String()))
	}
	info, err := os.Stat(output)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return 0, errx.Detail(ErrTailwind, "tailwindcss produced no stylesheet at %s | tailwindcss 未在 %s 产出样式表", output, output)
	}
	log.Info("tailwind stylesheet built | Tailwind 样式表已构建",
		logx.Str("bin", bin),
		logx.Str("input", input),
		logx.Str("output", output),
		logx.Bool("minify", o.Minify),
		logx.Int64("bytes", info.Size()),
	)
	return info.Size(), nil
}

// splitLines splits s into non-empty lines with surrounding blanks removed. | splitLines 把 s 切分为去除了首尾空白的非空行。
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
