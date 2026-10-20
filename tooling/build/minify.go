// This file implements the asset minification stage: it resolves the optional minify CLI, walks
// web/static, writes one .min.js or .min.svg per source and can verify committed output without
// writing. CSS is deliberately excluded: Tailwind owns it. | 本文件实现资产压缩阶段：
// 解析可选的 minify CLI、遍历 web/static、为每个源码写出 .min.js 或 .min.svg，并能在不写盘的前提下
// 校验已提交产物。CSS 刻意排除在外：它由 Tailwind 负责。
package build

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/core/logx"
)

// The asset minification stage's constants. | 资产压缩阶段的常量。
const (
	// DefaultMinifyRoot is the directory scanned for .js and .svg sources, relative to the build directory. | DefaultMinifyRoot 是扫描 .js 与 .svg 源码的目录，相对构建目录。
	DefaultMinifyRoot = "web/static"
	// KeyMinifyBin is the configuration key holding the minify executable. | KeyMinifyBin 是保存 minify 可执行文件的配置键。
	KeyMinifyBin = "minify.bin"
	// MinifyInstallHint is the installation command of the optional minify CLI. | MinifyInstallHint 是可选 minify CLI 的安装命令。
	MinifyInstallHint = "go install github.com/tdewolff/minify/v2/cmd/minify@latest"
	// minifyEnvBin is the environment variable holding the minify path. | minifyEnvBin 是保存 minify 路径的环境变量。
	minifyEnvBin = "YD_MINIFY_BIN"
)

// Sentinel errors of the asset minification stage. | 资产压缩阶段的哨兵错误。
var (
	// ErrMinifyNotFound indicates that no minify executable could be located. | ErrMinifyNotFound 表示未能定位到 minify 可执行文件。
	ErrMinifyNotFound = errx.New("build: minify executable not found | build：未找到 minify 可执行文件")
	// ErrMinify indicates that the minify process failed or produced nothing. | ErrMinify 表示 minify 进程失败或未产出结果。
	ErrMinify = errx.New("build: asset minification failed | build：资产压缩失败")
)

// MinifyOptions configures the JavaScript minification stage. | MinifyOptions 配置 JavaScript 压缩阶段。
type MinifyOptions struct {
	// Bin overrides the minify executable; empty means auto-detection. | Bin 覆盖 minify 可执行文件；为空表示自动探测。
	Bin string
	// Root is the scanned directory, relative to the build directory; empty means DefaultMinifyRoot. | Root 是扫描目录，相对构建目录；为空表示 DefaultMinifyRoot。
	Root string
	// Enabled turns the stage on in Build. | Enabled 在 Build 中启用该阶段。
	Enabled bool
	// CheckOnly verifies committed .min.js/.min.svg files instead of writing them. | CheckOnly 校验已提交的 .min.js/.min.svg 而不是写入。
	CheckOnly bool
}

// MinifyResult reports what one minification run produced or found. | MinifyResult 报告一次压缩运行产出或发现的内容。
type MinifyResult struct {
	// Mismatched lists sources whose committed .min.js differs (CheckOnly). | Mismatched 列出已提交 .min.js 内容不同的源码（CheckOnly）。
	Mismatched []string
	// Missing lists sources whose committed .min.js is absent (CheckOnly). | Missing 列出缺少已提交 .min.js 的源码（CheckOnly）。
	Missing []string
	// Stale lists manifest-tracked outputs whose source disappeared (CheckOnly). | Stale 列出清单中源文件已消失的产物（CheckOnly）。
	Stale []string
	// Files is the number of source files processed. | Files 是处理的源码文件数。
	Files int
	// Bytes is the total size of the produced .min.js and .min.svg files. | Bytes 是产出的 .min.js 与 .min.svg 文件总字节数。
	Bytes int64
	// SVGFiles is the number of .svg sources among Files. | SVGFiles 是 Files 中 .svg 源码的数量。
	SVGFiles int
}

// MinifyFunc builds the JavaScript assets and is the injection point of Build;
// a nil hook means the built-in implementation. | MinifyFunc 构建 JavaScript 资产，
// 是 Build 的注入点；钩子为 nil 时使用内置实现。
type MinifyFunc func(ctx context.Context, dir string, o MinifyOptions) (MinifyResult, error)

// minifyBinName returns the platform specific file name of the minify binary. | minifyBinName 返回 minify 二进制的平台相关文件名。
func minifyBinName() string {
	if filepath.Separator == '\\' {
		return "minify.exe"
	}
	return "minify"
}

// ResolveMinifyBin locates the minify executable in the documented order: the
// explicit bin argument, YD_MINIFY_BIN, the project-local .yd/tools directory
// and finally PATH. An unresolvable location yields ErrMinifyNotFound. | ResolveMinifyBin
// 按文档化的顺序定位 minify 可执行文件：显式 bin 参数、YD_MINIFY_BIN、项目内
// .yd/tools 目录，最后是 PATH。全都找不到时返回 ErrMinifyNotFound。
func ResolveMinifyBin(dir, bin string) (string, error) {
	return ResolveMinifyBinWith(dir, bin, exec.LookPath)
}

// ResolveMinifyBinWith is ResolveMinifyBin with an injectable PATH lookup, so
// `yd tools check` probes through the same lookup it uses for every other tool. | ResolveMinifyBinWith
// 是带可注入 PATH 查找的 ResolveMinifyBin，使 `yd tools check` 用与其它工具相同的查找进行探测。
func ResolveMinifyBinWith(dir, bin string, lookup func(string) (string, error)) (string, error) {
	if lookup == nil {
		lookup = exec.LookPath
	}
	if bin != "" {
		return resolveMinifyCandidate(dir, bin, lookup)
	}
	if env := strings.TrimSpace(os.Getenv(minifyEnvBin)); env != "" {
		return resolveMinifyCandidate(dir, env, lookup)
	}
	local := filepath.Join(dir, filepath.FromSlash(localToolsDir), minifyBinName())
	if info, err := os.Stat(local); err == nil && !info.IsDir() {
		return absoluteBin(local), nil
	}
	if found, err := lookup("minify"); err == nil && found != "" {
		return found, nil
	}
	return "", errx.Detail(ErrMinifyNotFound,
		"tried --minify-bin, $%s, %s and PATH | 已尝试 --minify-bin、$%s、%s 与 PATH",
		minifyEnvBin, localToolsDir, minifyEnvBin, localToolsDir)
}

// resolveMinifyCandidate resolves one explicit executable reference: a value
// containing a path separator is joined with dir, anything else goes through
// PATH lookup. | resolveMinifyCandidate 解析单个显式可执行文件引用：含路径分隔符的取值
// 与 dir 拼接，其余走 PATH 查找。
func resolveMinifyCandidate(dir, bin string, lookup func(string) (string, error)) (string, error) {
	if !strings.ContainsRune(bin, filepath.Separator) && !strings.Contains(bin, "/") {
		found, err := lookup(bin)
		if err != nil || found == "" {
			return "", errx.WrapDetail(err, ErrMinifyNotFound, "%s | %s", bin, bin)
		}
		return found, nil
	}
	path := bin
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	path = absoluteBin(path)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", errx.Detail(ErrMinifyNotFound, "%s is not an executable file | %s 不是可执行文件", path, path)
	}
	return path, nil
}

// BuildMinify runs the minify CLI over every .js source under Root and returns
// the result. A missing root is a no-op; a missing executable yields
// ErrMinifyNotFound, which the CLI turns into a warning. | BuildMinify 对 Root 下的
// 每个 .js 源码运行 minify CLI 并返回结果。Root 缺失是无操作；可执行文件缺失返回
// ErrMinifyNotFound，由 CLI 转为告警。
func BuildMinify(ctx context.Context, dir string, o MinifyOptions, log *logx.Logger) (MinifyResult, error) {
	return buildMinifyWith(ctx, dir, o, log, exec.CommandContext)
}

// buildMinifyWith is BuildMinify with an injectable command constructor. | buildMinifyWith
// 是接受可注入命令构造器的 BuildMinify。
func buildMinifyWith(ctx context.Context, dir string, o MinifyOptions, log *logx.Logger, run ExecCommandFunc) (MinifyResult, error) {
	if log == nil {
		log = logx.Default()
	}
	// Normalize the project directory up front: the binary, inputs and outputs are all
	// absolute afterwards, so cmd.Dir cannot double-apply a relative path. | 先归一化项目目录：
	// 二进制、输入与产物随后都是绝对路径，cmd.Dir 不会把相对路径二次拼接。
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return MinifyResult{}, errx.WrapDetail(err, ErrMinify, "cannot resolve %q | 无法解析 %q", dir, dir)
	}
	dir = absDir
	root := resolvePath(dir, orDefault(o.Root, DefaultMinifyRoot))
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		// Check mode still validates the tool, so a missing tree cannot turn a machine
		// without minify into a false success. | 校验模式仍验证工具，缺少目录的机器不会
		// 因为没装 minify 而误报成功。
		if o.CheckOnly {
			if _, rerr := ResolveMinifyBin(dir, o.Bin); rerr != nil {
				return MinifyResult{}, rerr
			}
		}
		return MinifyResult{}, nil
	}

	previous := readMinifyManifest(dir)
	if !o.CheckOnly {
		previous = cleanGeneratedMinified(root, dir, previous, log)
	}

	files, err := minifySources(root)
	if err != nil {
		return MinifyResult{}, errx.WrapDetail(err, ErrMinify, "walk %[1]s | 遍历 %[1]s", root)
	}

	// In check mode the tool must be resolvable even when the tree has no sources:
	// otherwise a machine without minify would report a false success. | 校验模式下即使
	// 没有源码也必须能解析工具，否则缺少 minify 的机器会误报成功。
	var bin string
	if o.CheckOnly || len(files) > 0 {
		bin, err = ResolveMinifyBin(dir, o.Bin)
		if err != nil {
			return MinifyResult{}, err
		}
	}
	if o.CheckOnly {
		res := MinifyResult{Files: len(files), Stale: orphanedMinified(root, dir, previous)}
		for _, input := range files {
			if strings.HasSuffix(input, ".svg") {
				res.SVGFiles++
			}
			if err := checkMinified(ctx, run, dir, bin, input, minifiedOutput(dir, input), relTo(dir, input), &res); err != nil {
				return MinifyResult{}, err
			}
		}
		return res, nil
	}
	if len(files) == 0 {
		writeMinifyManifest(dir, nil, log)
		return MinifyResult{}, nil
	}

	res := MinifyResult{}
	generated := make([]string, 0, len(files))
	for _, input := range files {
		rel := relTo(dir, input)
		outRel := minifiedRel(rel)
		out := resolvePath(dir, outRel)
		size, err := writeMinified(ctx, run, dir, bin, input, out)
		if err != nil {
			return MinifyResult{}, err
		}
		generated = append(generated, outRel)
		res.Files++
		if strings.HasSuffix(input, ".svg") {
			res.SVGFiles++
		}
		res.Bytes += size
		log.Debug("asset minified | 资产已压缩",
			logx.Str("input", rel),
			logx.Str("output", outRel),
			logx.Int64("bytes", size),
		)
	}
	writeMinifyManifest(dir, generated, log)
	return res, nil
}

// relTo returns the slash-separated path of input relative to dir. | relTo
// 返回 input 相对 dir 的斜杠分隔路径。
func relTo(dir, input string) string {
	rel, err := filepath.Rel(dir, input)
	if err != nil {
		rel = input
	}
	return filepath.ToSlash(rel)
}

// minifiedOutput returns the minified output path of a source. | minifiedOutput 返回源码的压缩产物路径。
func minifiedOutput(dir, input string) string {
	return resolvePath(dir, minifiedRel(relTo(dir, input)))
}

// minifiedRel inserts .min before the .js or .svg extension. | minifiedRel 在 .js 或 .svg 扩展名前插入 .min。
func minifiedRel(rel string) string {
	switch {
	case strings.HasSuffix(rel, ".js"):
		return strings.TrimSuffix(rel, ".js") + ".min.js"
	case strings.HasSuffix(rel, ".svg"):
		return strings.TrimSuffix(rel, ".svg") + ".min.svg"
	default:
		return rel
	}
}

// minifiedSource returns the source path of a tracked minified output. | minifiedSource 返回受跟踪的压缩产物对应的源码路径。
func minifiedSource(abs string) (string, bool) {
	switch {
	case strings.HasSuffix(abs, ".min.js"):
		return strings.TrimSuffix(abs, ".min.js") + ".js", true
	case strings.HasSuffix(abs, ".min.svg"):
		return strings.TrimSuffix(abs, ".min.svg") + ".svg", true
	default:
		return "", false
	}
}

// minifySources returns every .js and .svg source under root, sorted, skipping vendor trees and
// already-minified files. CSS stays out: Tailwind owns its minification. | minifySources 返回 root 下
// 全部 .js 与 .svg 源码（已排序），跳过 vendor 目录与已压缩文件。CSS 不在其中：它由 Tailwind 压缩。
func minifySources(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".min.js") || strings.HasSuffix(name, ".min.svg") {
			return nil
		}
		if !strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".svg") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// writeMinified runs one minify invocation and verifies its output. | writeMinified
// 运行一次 minify 调用并校验产物。
func writeMinified(ctx context.Context, run ExecCommandFunc, dir, bin, input, out string) (int64, error) {
	cmd := run(ctx, bin, "-o", out, input)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		return 0, errx.WrapDetail(err, ErrMinify, "%s | %s: %s", input, input, strings.TrimSpace(buf.String()))
	}
	info, err := os.Stat(out)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return 0, errx.Detail(ErrMinify, "minify produced no output at %s | minify 未在 %s 产出结果", out, out)
	}
	return info.Size(), nil
}

// checkMinified compares a fresh minification (written to stdout) with the
// committed .min.js and records a mismatch or a missing file. | checkMinified
// 把写到 stdout 的新结果与已提交的 .min.js 比较，并记录不匹配或缺失。
func checkMinified(ctx context.Context, run ExecCommandFunc, dir, bin, input, out, rel string, res *MinifyResult) error {
	cmd := run(ctx, bin, input)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return errx.WrapDetail(err, ErrMinify, "%s | %s: %s", input, input, strings.TrimSpace(stderr.String()))
	}
	res.Bytes += int64(stdout.Len())
	want, err := os.ReadFile(out)
	if errors.Is(err, os.ErrNotExist) {
		res.Missing = append(res.Missing, rel)
		return nil
	}
	if err != nil {
		return errx.WrapDetail(err, ErrMinify, "%s | %s", out, out)
	}
	if !bytes.Equal(want, stdout.Bytes()) {
		res.Mismatched = append(res.Mismatched, rel)
	}
	return nil
}

// cleanStaleMinified removes .min.js files whose source disappeared, skipping
// vendor trees. | cleanStaleMinified 删除源文件已消失的 .min.js，跳过 vendor 目录。
// minifyManifestName is the project-local record of outputs this stage generated.
// It lives under .yd/, which every generated project already ignores, and it is
// what lets cleanup delete only files this stage owns. | minifyManifestName 是本阶段
// 生成产物的项目内记录。它位于每个生成项目本来就会忽略的 .yd/ 下，清理因此只删除本阶段拥有的文件。
const minifyManifestName = "minify.json"

// minifyManifest is the on-disk shape of the generated-output record. | minifyManifest
// 是生成产物记录的落盘结构。
type minifyManifest struct {
	// Outputs are slash-separated paths relative to the project root. | Outputs 是相对项目根的斜杠分隔路径。
	Outputs []string `json:"outputs"`
}

// minifyManifestPath returns the manifest path of a project. | minifyManifestPath 返回项目的清单路径。
func minifyManifestPath(dir string) string {
	return filepath.Join(dir, ".yd", minifyManifestName)
}

// readMinifyManifest returns the previously generated outputs; a missing or
// malformed manifest is an empty record, never a build failure. | readMinifyManifest
// 返回上次生成的产物；清单缺失或损坏时视为空记录，绝不导致构建失败。
func readMinifyManifest(dir string) []string {
	data, err := os.ReadFile(minifyManifestPath(dir))
	if err != nil {
		return nil
	}
	var m minifyManifest
	if err := jsonx.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m.Outputs
}

// writeMinifyManifest records the generated outputs; failures are warnings
// because the manifest only narrows future cleanup. | writeMinifyManifest 记录生成的
// 产物；失败只告警，因为清单只用于收窄后续清理的范围。
func writeMinifyManifest(dir string, outputs []string, log *logx.Logger) {
	path := minifyManifestPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Warn("cannot create the minify manifest directory | 无法创建压缩清单目录", logx.Err(err))
		return
	}
	data, err := jsonx.MarshalIndent(minifyManifest{Outputs: outputs}, "", "  ")
	if err != nil {
		log.Warn("cannot encode the minify manifest | 无法编码压缩清单", logx.Err(err))
		return
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Warn("cannot write the minify manifest | 无法写入压缩清单", logx.Err(err))
	}
}

// ownedOutput reports whether rel is a .min.js or .min.svg output this stage may own: inside root,
// not under vendor and ending in a tracked suffix. | ownedOutput 报告 rel 是否可能是本阶段拥有的
// .min.js 或 .min.svg 产物：位于 root 内、不在 vendor 下且以受跟踪后缀结尾。
func ownedOutput(root, dir, rel string) (string, bool) {
	cleanRel := filepath.ToSlash(rel)
	if _, ok := minifiedSource(cleanRel); !ok || strings.Contains("/"+cleanRel+"/", "/vendor/") {
		return "", false
	}
	abs := resolvePath(dir, cleanRel)
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", false
	}
	return abs, true
}

// cleanGeneratedMinified removes only manifest-tracked outputs whose source
// disappeared, so a hand-vendored chart.min.js is never deleted. | cleanGeneratedMinified
// 只删除清单中源文件已消失的产物，因此手工 vendor 的 chart.min.js 绝不会被删除。
func cleanGeneratedMinified(root, dir string, outputs []string, log *logx.Logger) []string {
	kept := make([]string, 0, len(outputs))
	for _, rel := range outputs {
		abs, ok := ownedOutput(root, dir, rel)
		if !ok {
			continue
		}
		cleanRel := filepath.ToSlash(rel)
		source, ok := minifiedSource(abs)
		if !ok {
			continue
		}
		if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
			kept = append(kept, cleanRel)
			continue
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warn("cannot remove stale minified asset | 无法删除陈旧的压缩资产",
				logx.Str("path", cleanRel), logx.Err(err))
			continue
		}
		log.Warn("removed stale minified asset | 已删除陈旧的压缩资产", logx.Str("path", cleanRel))
	}
	return kept
}

// orphanedMinified reports manifest-tracked outputs whose source disappeared;
// check mode uses it instead of deleting anything. | orphanedMinified 报告清单中源文件
// 已消失的产物；校验模式用它报告而不是删除。
func orphanedMinified(root, dir string, outputs []string) []string {
	var stale []string
	for _, rel := range outputs {
		abs, ok := ownedOutput(root, dir, rel)
		if !ok {
			continue
		}
		source, ok := minifiedSource(abs)
		if !ok {
			continue
		}
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			stale = append(stale, filepath.ToSlash(rel))
		}
	}
	return stale
}
