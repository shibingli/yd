package toolchain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/shibingli/yd/errx"
)

// EnvTailwindBin is the environment variable holding the tailwindcss path. | EnvTailwindBin 是保存 tailwindcss 路径的环境变量。
const EnvTailwindBin = "YD_TAILWIND_BIN"

// DefaultCSSInput is the default Tailwind entry stylesheet. | DefaultCSSInput 是默认的 Tailwind 入口样式表。
const DefaultCSSInput = "web/static/css/app.css"

// DefaultCSSOutput is the default compiled stylesheet. | DefaultCSSOutput 是默认的编译产物样式表。
const DefaultCSSOutput = "web/static/css/yd.css"

// ToolOptions collects every source that may point at the tailwindcss binary. | ToolOptions 汇总所有可能指向 tailwindcss 可执行文件的来源。
//
// Resolution order, highest priority first: Bin, ConfigBin, EnvBin, the locally installed binary under LocalDir/.yd/tools, and finally PATH. | 解析顺序由高到低：Bin、ConfigBin、EnvBin、LocalDir/.yd/tools 下的本地安装、最后是 PATH。
type ToolOptions struct {
	// Bin is the explicit --tailwind-bin flag value. | Bin 是 --tailwind-bin 标志的显式取值。
	Bin string
	// ConfigBin is the tailwind.bin configuration value. | ConfigBin 是配置项 tailwind.bin 的取值。
	ConfigBin string
	// EnvBin is the YD_TAILWIND_BIN value; when empty the environment is read as well. | EnvBin 是 YD_TAILWIND_BIN 的取值；为空时还会去读环境变量。
	EnvBin string
	// LocalDir is the project root that hosts .yd/tools/tailwindcss[.exe]. | LocalDir 是承载 .yd/tools/tailwindcss[.exe] 的项目根目录。
	LocalDir string
	// PathLookup resolves a bare command name; nil means exec.LookPath. | PathLookup 解析裸命令名；nil 表示 exec.LookPath。
	PathLookup func(string) (string, error)
}

// ResolveTailwind resolves the tailwindcss executable following the documented precedence chain. | ResolveTailwind 按文档化的优先级链解析 tailwindcss 可执行文件。
//
// A value without a path separator is treated as a command name and looked up on PATH, so YD_TAILWIND_BIN=tailwindcss keeps working. | 不含路径分隔符的取值被视为命令名并在 PATH 上查找，因此 YD_TAILWIND_BIN=tailwindcss 依然可用。
// ErrToolNotFound is returned when no source yields an existing binary. | 当所有来源都得不到存在的可执行文件时返回 ErrToolNotFound。
func ResolveTailwind(o ToolOptions) (string, error) {
	envBin := o.EnvBin
	if envBin == "" {
		envBin = os.Getenv(EnvTailwindBin)
	}
	lookup := o.PathLookup
	if lookup == nil {
		lookup = exec.LookPath
	}
	local := ""
	if o.LocalDir != "" {
		local = filepath.Join(o.LocalDir, ".yd", "tools", "tailwindcss"+exeSuffix())
	}

	candidates := []struct {
		source string
		value  string
	}{
		{"Bin | 标志", o.Bin},
		{"tailwind.bin | 配置项", o.ConfigBin},
		{EnvTailwindBin + " | 环境变量", envBin},
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
	// 最后回退 PATH 上的 tailwindcss 命令。 | Finally fall back to the tailwindcss command on PATH.
	if p, err := lookup("tailwindcss"); err == nil && p != "" {
		return p, nil
	}
	return "", errx.Detail(ErrToolNotFound, "checked %s, %s, %s, %s and PATH", "Bin", "ConfigBin", "EnvBin", "local install")
}

// resolveCandidate turns one configured value into an existing executable. | resolveCandidate 把一个配置取值变成真实存在的可执行文件。
func resolveCandidate(value string, lookup func(string) (string, error)) (string, bool) {
	// 裸命令名（无路径分隔符）交给 PATH 查找。 | A bare command name (no separator) goes through PATH lookup.
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

// CSSOptions describes one Tailwind build. | CSSOptions 描述一次 Tailwind 构建。
type CSSOptions struct {
	// Input is the entry stylesheet; empty means DefaultCSSInput. | Input 是入口样式表；空表示 DefaultCSSInput。
	Input string
	// Output is the compiled stylesheet; empty means DefaultCSSOutput. | Output 是编译产物样式表；空表示 DefaultCSSOutput。
	Output string
	// Minify turns on CSS minification. | Minify 开启 CSS 压缩。
	Minify bool
	// Watch keeps the process alive in Tailwind watch mode. | Watch 让进程以 Tailwind watch 模式常驻。
	Watch bool
	// Content lists extra scan paths; Tailwind v4 projects using @source may leave it empty. | Content 是额外扫描路径；使用 @source 指令的 Tailwind v4 项目可留空。
	Content []string
}

// Args returns the argv passed to the tailwindcss binary. | Args 返回传给 tailwindcss 二进制的 argv。
func (o CSSOptions) Args() []string {
	in := o.Input
	if in == "" {
		in = DefaultCSSInput
	}
	out := o.Output
	if out == "" {
		out = DefaultCSSOutput
	}
	args := make([]string, 0, 6+2*len(o.Content))
	args = append(args, "-i", in, "-o", out)
	if o.Minify {
		args = append(args, "--minify")
	}
	if o.Watch {
		args = append(args, "--watch")
	}
	for _, c := range o.Content {
		if strings.TrimSpace(c) == "" {
			continue
		}
		args = append(args, "--content", c)
	}
	return args
}

// BuildResult reports what a build produced. | BuildResult 报告一次构建的产物。
type BuildResult struct {
	// CSSPath is the compiled stylesheet path. | CSSPath 是编译产物的样式表路径。
	CSSPath string
	// Bytes is the size of the compiled stylesheet. | Bytes 是编译产物样式表的字节数。
	Bytes int
	// Duration is how long the build took. | Duration 是构建耗时。
	Duration time.Duration
}

// BuildCSS runs the tailwindcss binary and returns the produced stylesheet. | BuildCSS 运行 tailwindcss 二进制并返回产出的样式表。
//
// The output directory is created when missing, and a non-zero exit status or a missing output file wraps ErrBuildFailed together with the tool's tail output. | 输出目录缺失时会被创建；退出码非零或产物缺失时，会把工具的末尾输出一并包装进 ErrBuildFailed。
func BuildCSS(ctx context.Context, bin string, o CSSOptions) (*BuildResult, error) {
	if strings.TrimSpace(bin) == "" {
		return nil, errx.Detail(ErrToolNotFound, "empty tailwindcss binary path | tailwindcss 二进制定位为空")
	}
	in := o.Input
	if in == "" {
		in = DefaultCSSInput
	}
	out := o.Output
	if out == "" {
		out = DefaultCSSOutput
	}
	if dir := filepath.Dir(out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, errx.WrapDetail(err, ErrBuildFailed, "create output dir %s | 创建输出目录 %s", dir, dir)
		}
	}

	opts := o
	opts.Input, opts.Output = in, out
	var outBuf tailBuffer
	start := time.Now()
	cmd := newCommand(ctx, bin, opts.Args()...)
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	err := cmd.Run()
	elapsed := time.Since(start)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, errx.WrapDetail(ctxErr, ErrBuildFailed, "tailwindcss cancelled | tailwindcss 被取消：%v", err)
		}
		return nil, errx.WrapDetail(err, ErrBuildFailed, "tailwindcss %s | tailwindcss 执行失败：%s",
			err.Error(), truncateOutput(outBuf.String(), 2048))
	}

	info, statErr := os.Stat(out)
	if statErr != nil {
		return nil, errx.WrapDetail(statErr, ErrBuildFailed, "missing output %s | 缺少产物 %s：%s",
			out, out, truncateOutput(outBuf.String(), 2048))
	}
	return &BuildResult{CSSPath: filepath.Clean(out), Bytes: int(info.Size()), Duration: elapsed}, nil
}
