// Package upx wraps the UPX executable packer behind a small, dependency-free API. | Package upx 用一个
// 无额外依赖的薄 API 包装 UPX 可执行文件压缩器。
//
// The package carries the pinned Linux amd64/arm64 and Windows amd64 UPX binaries, so image builds and the
// ko wrapper never need a download there; macOS and Windows/arm64 hosts use an external upx from PATH.
// An explicitly configured or locally installed binary always wins. | 本包携带固定版本的 Linux amd64/arm64
// 与 Windows amd64 UPX 二进制，因此这些平台上的镜像构建与 ko 包装器都不需要下载；macOS 与 Windows/arm64
// 宿主使用 PATH 上的外部 upx。显式配置或本地安装的二进制始终优先。
//
// UPX is an optional capability: a missing or too-old binary never fails a build, it only disables the
// compression step with a warning. | UPX 是可选能力：缺失或版本过旧绝不使构建失败，只会告警并跳过压缩步骤。
package upx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/shibingli/ydx/errs"
)

// The embedded UPX release and the policy constants. | 嵌入的 UPX 版本与策略常量。
const (
	// Version is the UPX release shipped inside yd. | Version 是随 yd 发布的 UPX 版本。
	Version = "5.2.1"
	// MinVersion is the oldest UPX release accepted from an external binary. | MinVersion 是外部二进制
	// 可接受的最低 UPX 版本。
	MinVersion = "5.2.1"
	// MaxSize is the largest file UPX is asked to compress (1 GiB). UPX itself refuses files beyond roughly
	// 768 MiB of in-memory working set, so the guard is deliberately conservative. | MaxSize 是交给 UPX 压缩的
	// 最大文件（1 GiB）。UPX 自身对超过约 768 MiB 常驻内存的文件会拒绝，因此该门槛刻意保守。
	MaxSize int64 = 1 << 30
	// EnvBin is the environment variable naming an explicit UPX binary. | EnvBin 是指定显式 UPX 二进制的环境变量。
	EnvBin = "YD_UPX_BIN"
	// KeyBinary is the configuration key naming an explicit UPX binary; EnvBin maps onto it through the
	// framework's YD_ prefix. | KeyBinary 是指定显式 UPX 二进制的配置键；EnvBin 经框架的 YD_ 前缀映射到它。
	KeyBinary = "upx.bin"
	// LocalToolDir is the project-local tool directory, shared with `yd tools`. | LocalToolDir 是与
	// `yd tools` 共用的项目内工具目录。
	LocalToolDir = ".yd/tools"
	// InstallHint tells the user where to obtain an external UPX release. | InstallHint 告诉用户从哪里获取外部 UPX 发行版。
	InstallHint = "download UPX " + MinVersion + "+ from https://github.com/upx/upx/releases | 从 https://github.com/upx/upx/releases 下载 UPX " + MinVersion + "+"
	// compressionFlags is the argv UPX receives before the target path. | compressionFlags 是 UPX 在目标路径之前收到的参数。
	compressionFlags = "--best --lzma"
)

// Package-level sentinel errors: every failure path refers to one of them and callers decide with errors.Is. |
// 包级哨兵错误：所有失败路径都引用其中之一，调用方用 errors.Is 判定。
var (
	// ErrBinaryNotFound indicates that no explicit, environment, local or embedded UPX binary exists. | ErrBinaryNotFound
	// 表示显式、环境变量、本地与嵌入来源都没有可用的 UPX 二进制。
	ErrBinaryNotFound = errs.New("upx: binary not found | upx：未找到可执行文件")
	// ErrVersionTooOld indicates an unparsable or older-than-MinVersion UPX. | ErrVersionTooOld 表示无法解析或低于
	// MinVersion 的 UPX。
	ErrVersionTooOld = errs.New("upx: version is too old | upx：版本过旧")
	// ErrCompressFailed indicates that the UPX process failed. | ErrCompressFailed 表示 UPX 进程执行失败。
	ErrCompressFailed = errs.New("upx: compression failed | upx：压缩失败")
	// ErrFileTooLarge indicates that the target exceeds MaxSize. | ErrFileTooLarge 表示目标文件超过 MaxSize。
	ErrFileTooLarge = errs.New("upx: file exceeds the size limit | upx：文件超过体积上限")
	// ErrUnsupported indicates a payload or target platform UPX cannot handle. | ErrUnsupported 表示 UPX 不支持的
	// payload 或目标平台。
	ErrUnsupported = errs.New("upx: unsupported platform | upx：不支持的平台")
)

// RunCommandFunc runs one external command and returns its merged output. It mirrors the injection point of
// tooling/ko, so tests never start a real process. | RunCommandFunc 执行一个外部命令并返回合并输出。它与
// tooling/ko 的注入点同形，使测试永不启动真实进程。
type RunCommandFunc func(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, error)

// Options configures one resolution. | Options 配置一次解析。
type Options struct {
	// PathLookup resolves bare names on PATH; nil means exec.LookPath. | PathLookup 在 PATH 上解析裸名称；
	// 为 nil 表示 exec.LookPath。
	PathLookup func(string) (string, error)
	// ExecCommand overrides the command runner; nil means runCommand. | ExecCommand 覆盖命令执行器；为 nil 表示 runCommand。
	ExecCommand RunCommandFunc
	// Binary is the explicit binary from a flag or config (upx.bin); it ranks first. | Binary 是来自标志或配置
	// （upx.bin）的显式二进制，优先级最高。
	Binary string
	// Dir is the project directory that holds .yd/tools; empty means the current directory. | Dir 是包含
	// .yd/tools 的项目目录；为空表示当前目录。
	Dir string
	// Materialize extracts the embedded host payload when no external binary exists. Probes that must stay
	// read-only (yd tools check) pass false and receive the embedded marker path instead. | Materialize 在
	// 没有外部二进制时解压嵌入的宿主 payload。必须保持只读的探测（yd tools check）传 false，改为得到嵌入标记路径。
	Materialize bool
	// SkipVersionCheck resolves without executing the binary; read-only probes set it because a check must
	// never run a tool. | SkipVersionCheck 在解析时不执行二进制；只读探测会设置它，因为检查绝不能运行工具。
	SkipVersionCheck bool
}

// Binary is the resolved UPX executable. | Binary 是解析出的 UPX 可执行文件。
type Binary struct {
	// Path is the absolute path, or the embedded marker when Materialize was false. | Path 是绝对路径；
	// Materialize 为 false 时是嵌入标记。
	Path string
	// Version is the UPX version, e.g. 5.2.1. | Version 是 UPX 版本，例如 5.2.1。
	Version string
	// Embedded reports that the binary came from the embedded payload. | Embedded 表示二进制来自嵌入 payload。
	Embedded bool
}

// EmbeddedMarker is the read-only placeholder path returned for an embedded payload. | EmbeddedMarker 是
// 嵌入 payload 在只读探测下返回的占位路径。
const EmbeddedMarker = "<embedded-upx>"

// execCommand returns the command runner of this resolution. | execCommand 返回本次解析使用的命令执行器。
func (o Options) execCommand() RunCommandFunc {
	if o.ExecCommand != nil {
		return o.ExecCommand
	}
	return runCommand
}

// lookup returns the PATH lookup of this resolution. | lookup 返回本次解析使用的 PATH 查找函数。
func (o Options) lookup() func(string) (string, error) {
	if o.PathLookup != nil {
		return o.PathLookup
	}
	return exec.LookPath
}

// runCommand is the default RunCommandFunc, merging stdout and stderr. | runCommand 是默认的 RunCommandFunc，
// 合并 stdout 与 stderr。
func runCommand(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	return out, err
}

// embeddedHost is EmbeddedHost behind a variable, so tests can simulate a host without a host payload.
// | embeddedHost 是经变量间接调用的 EmbeddedHost，便于测试模拟没有宿主 payload 的机器。
var embeddedHost = EmbeddedHost

// Resolve finds the UPX binary: explicit → YD_UPX_BIN → <project>/.yd/tools → embedded → PATH. | Resolve 查找
// UPX 二进制：显式 → YD_UPX_BIN → <项目>/.yd/tools → 嵌入 → PATH。
//
// An external binary must report at least MinVersion. A missing external binary falls through to the
// embedded payload, which is always Version; without Materialize the embedded result carries EmbeddedMarker
// so read-only probes can report availability without writing. Hosts without an embedded payload (macOS and
// Windows/arm64) finally use the upx found on PATH, so the documented Homebrew installation keeps working. |
// 外部二进制必须至少报告 MinVersion。外部缺失时回退到嵌入 payload（恒为 Version）；未开启 Materialize 时嵌入结果
// 携带 EmbeddedMarker，使只读探测无需写盘即可报告可用性。没有嵌入 payload 的宿主（macOS 与 Windows/arm64）最后
// 使用 PATH 上的 upx，使文档化的 Homebrew 安装方式仍然有效。
func Resolve(o Options) (Binary, error) {
	dir := strings.TrimSpace(o.Dir)
	if dir == "" {
		dir = "."
	}
	candidates := make([]string, 0, 3)
	if value := strings.TrimSpace(o.Binary); value != "" {
		candidates = append(candidates, value)
	}
	if value := strings.TrimSpace(os.Getenv(EnvBin)); value != "" {
		candidates = append(candidates, value)
	}
	candidates = append(candidates, filepath.Join(dir, LocalToolDir, upxBinaryName()))

	for _, candidate := range candidates {
		path, ok := resolveCandidate(candidate, o.lookup())
		if !ok {
			continue
		}
		if o.SkipVersionCheck {
			return Binary{Path: path}, nil
		}
		version, err := VersionOf(context.Background(), path, o.execCommand())
		if err != nil {
			return Binary{}, errs.WrapDetail(err, ErrVersionTooOld, "%s | %s", path, path)
		}
		return Binary{Path: path, Version: version}, nil
	}

	payload, name, ok := embeddedHost()
	if ok {
		if !o.Materialize {
			return Binary{Path: EmbeddedMarker, Version: Version, Embedded: true}, nil
		}
		path, err := Materialize(dir, name, payload)
		if err != nil {
			return Binary{}, err
		}
		return Binary{Path: path, Version: Version, Embedded: true}, nil
	}

	// Hosts without an embedded payload (macOS, Windows/arm64) fall back to PATH. | 没有嵌入 payload 的宿主
	// （macOS、Windows/arm64）回退到 PATH。
	if path, err := o.lookup()(upxBinaryName()); err == nil && strings.TrimSpace(path) != "" {
		if o.SkipVersionCheck {
			return Binary{Path: path}, nil
		}
		version, err := VersionOf(context.Background(), path, o.execCommand())
		if err != nil {
			return Binary{}, errs.WrapDetail(err, ErrVersionTooOld, "%s | %s", path, path)
		}
		return Binary{Path: path, Version: version}, nil
	}
	return Binary{}, errs.Detail(ErrBinaryNotFound,
		"no upx in %s, .yd/tools or PATH; %s | 在 %s、.yd/tools 与 PATH 中都没有 upx；%s",
		EnvBin, InstallHint, EnvBin, InstallHint)
}

// resolveCandidate maps one candidate onto an existing file. Bare names go through PATH; anything with a
// separator is resolved against the process working directory. | resolveCandidate 把一个候选映射为已存在的文件。
// 裸名称走 PATH；带分隔符的取值按进程工作目录解析。
func resolveCandidate(value string, lookup func(string) (string, error)) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if !strings.ContainsAny(value, `/\`) {
		path, err := lookup(value)
		if err != nil || path == "" {
			return "", false
		}
		return path, true
	}
	path, err := filepath.Abs(value)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	return path, true
}

// VersionOf runs upx --version and returns the version, rejecting anything older than MinVersion. |
// VersionOf 执行 upx --version 并返回版本，低于 MinVersion 的一律拒绝。
func VersionOf(ctx context.Context, bin string, run RunCommandFunc) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := run(ctx, bin, []string{"--version"}, "", nil)
	if err != nil {
		return "", errs.WrapDetail(err, ErrVersionTooOld, "run %s --version | 执行 %s --version: %v", bin, bin, err)
	}
	if err := CheckVersion(string(out)); err != nil {
		return "", err
	}
	version, _ := versionFromOutput(string(out))
	return version, nil
}

// CheckVersion parses upx --version output and enforces MinVersion. Unparsable output is reported as
// ErrVersionTooOld because an unknown UPX cannot be trusted to accept the shipped flags. | CheckVersion 解析
// upx --version 输出并强制 MinVersion。无法解析的输出按 ErrVersionTooOld 处理，因为无法信任未知 UPX 接受
// 随包发布的参数。
func CheckVersion(output string) error {
	version, ok := versionFromOutput(output)
	if !ok {
		return errs.Detail(ErrVersionTooOld, "unrecognized upx version output: %q | 无法识别的 upx 版本输出：%q", output, output)
	}
	if compareVersions(version, MinVersion) < 0 {
		return errs.Detail(ErrVersionTooOld, "upx %s is older than %s | upx %s 低于 %s", version, MinVersion, version, MinVersion)
	}
	return nil
}

// versionFromOutput extracts the first semantic version (two or three numeric segments) from UPX output. |
// versionFromOutput 从 UPX 输出中提取第一个语义版本（两到三段数字）。
func versionFromOutput(output string) (string, bool) {
	for _, field := range strings.Fields(output) {
		field = strings.TrimPrefix(field, "v")
		if cut, _, found := strings.Cut(field, "-"); found {
			field = cut
		}
		parts := strings.Split(field, ".")
		if len(parts) < 2 || len(parts) > 3 {
			continue
		}
		numbers := make([]int, 0, 3)
		valid := true
		for _, part := range parts {
			number, err := strconv.Atoi(part)
			if err != nil || number < 0 {
				valid = false
				break
			}
			numbers = append(numbers, number)
		}
		if !valid {
			continue
		}
		for len(numbers) < 3 {
			numbers = append(numbers, 0)
		}
		return fmt.Sprintf("%d.%d.%d", numbers[0], numbers[1], numbers[2]), true
	}
	return "", false
}

// compareVersions compares two dotted versions numerically, returning -1, 0 or 1. | compareVersions 按数字比较
// 两个点分版本，返回 -1、0 或 1。
func compareVersions(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for i := 0; i < 3; i++ {
		leftNumber := versionPart(leftParts, i)
		rightNumber := versionPart(rightParts, i)
		switch {
		case leftNumber < rightNumber:
			return -1
		case leftNumber > rightNumber:
			return 1
		}
	}
	return 0
}

// versionPart returns one numeric segment, treating a missing segment as zero. | versionPart 返回一个数字段，
// 缺失的段按 0 处理。
func versionPart(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	number, err := strconv.Atoi(parts[index])
	if err != nil {
		return 0
	}
	return number
}

// Compress runs upx --best --lzma on target. Files at or beyond maxBytes are rejected with ErrFileTooLarge
// before any process starts. | Compress 对 target 执行 upx --best --lzma。达到或超过 maxBytes 的文件会在启动
// 进程之前以 ErrFileTooLarge 拒绝。
func Compress(ctx context.Context, bin, target string, maxBytes int64, run RunCommandFunc) error {
	if ctx == nil {
		ctx = context.Background()
	}
	info, err := os.Stat(target)
	if err != nil {
		return errs.WrapDetail(err, ErrCompressFailed, "stat %s | 检查 %s", target, target)
	}
	if info.IsDir() {
		return errs.Detail(ErrCompressFailed, "%s is a directory | %s 是目录", target, target)
	}
	if maxBytes > 0 && info.Size() >= maxBytes {
		return errs.Detail(ErrFileTooLarge, "%s is %d bytes, limit %d | %s 为 %d 字节，上限 %d",
			target, info.Size(), maxBytes, target, info.Size(), maxBytes)
	}
	if run == nil {
		run = runCommand
	}
	flags := strings.Fields(compressionFlags)
	args := append(append([]string{}, flags...), target)
	out, err := run(ctx, bin, args, filepath.Dir(target), nil)
	if err != nil {
		return errs.WrapDetail(err, ErrCompressFailed, "upx %s failed | upx 压缩 %s 失败: %s", target, target, strings.TrimSpace(string(out)))
	}
	return nil
}

// upxBinaryName returns the host file name of the UPX executable. | upxBinaryName 返回宿主上 UPX 可执行文件的文件名。
func upxBinaryName() string {
	if runtime.GOOS == "windows" {
		return "upx.exe"
	}
	return "upx"
}

// IsEmbedded reports whether path is the read-only embedded marker. | IsEmbedded 报告 path 是否为只读嵌入标记。
func IsEmbedded(path string) bool { return path == EmbeddedMarker }

// ExitCode converts a command error into a process exit code. | ExitCode 把命令错误转换为进程退出码。
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}
