// This file implements the KO_GO_PATH wrapper: it forwards every Go invocation and UPX-compresses the
// binary a successful `go build -o` produced. | 本文件实现 KO_GO_PATH 包装器：转发所有 Go 调用，并对成功的
// `go build -o` 产物执行 UPX 压缩。
package upx

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// The wrapper's private environment contract and the backup suffix. | 包装器的私有环境契约与备份后缀。
const (
	// WrapperMarkerEnv marks a process that must behave as the Go wrapper instead of the normal CLI. | WrapperMarkerEnv
	// 标记一个必须作为 Go 包装器而不是普通 CLI 行为的进程。
	WrapperMarkerEnv = "YD_KO_GO_WRAPPER"
	// RealGoEnv names the real Go executable the wrapper must forward to. | RealGoEnv 指定包装器需要转发的
	// 真实 Go 可执行文件。
	RealGoEnv = "YD_KO_GO_REAL"
	// MaxBytesEnv optionally overrides MaxSize for one build. | MaxBytesEnv 可选地为一次构建覆盖 MaxSize。
	MaxBytesEnv = "YD_UPX_MAX_BYTES"
	// backupSuffix marks the pre-UPX copy ko reads build information from. | backupSuffix 标记 ko 读取构建信息
	// 所用的压缩前副本。
	backupSuffix = ".upx-orig"
)

// WrapperRunFunc runs one process with inherited stdio, mirroring exec.Cmd.Run. | WrapperRunFunc 以继承的
// 标准流运行一个进程，签名与 exec.Cmd.Run 对应。
type WrapperRunFunc func(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error

// WrapperOptions configures one wrapper pass. | WrapperOptions 配置一次包装器执行。
type WrapperOptions struct {
	// Run overrides the process runner; nil means defaultWrapperExec. | Run 覆盖进程执行器；为 nil 表示
	// defaultWrapperExec。
	Run WrapperRunFunc
	// Pack compresses one built binary; nil disables compression. | Pack 压缩一个构建产物；为 nil 表示关闭压缩。
	Pack func(ctx context.Context, target string) error
	// GoBin is the real Go executable; empty fails the wrapper. | GoBin 是真实的 Go 可执行文件；为空时包装器失败。
	GoBin string
	// MaxBytes is the compression size limit; zero means MaxSize. | MaxBytes 是压缩体积上限；零表示 MaxSize。
	MaxBytes int64
}

// exec returns the process runner of this pass. | exec 返回本次执行的进程运行器。
func (o WrapperOptions) run() WrapperRunFunc {
	if o.Run != nil {
		return o.Run
	}
	return defaultWrapperExec
}

// maxBytes returns the effective size limit. | maxBytes 返回实际生效的体积上限。
func (o WrapperOptions) maxBytes() int64 {
	if o.MaxBytes > 0 {
		return o.MaxBytes
	}
	return MaxSize
}

// defaultWrapperExec runs the child with inherited stdio and environment. | defaultWrapperExec 以继承的标准流
// 与环境运行子进程。
func defaultWrapperExec(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

// WrapperMainFromEnv builds the wrapper options from the environment and runs one Go invocation. It is only
// called when WrapperMarkerEnv is set, so a normal yd process is never hijacked. | WrapperMainFromEnv 从环境
// 变量构造包装器选项并执行一次 Go 调用。仅在 WrapperMarkerEnv 已设置时调用，因此普通 yd 进程不会被劫持。
func WrapperMainFromEnv(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	maxBytes := MaxSize
	if value := strings.TrimSpace(os.Getenv(MaxBytesEnv)); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
			maxBytes = parsed
		}
	}
	opts := WrapperOptions{
		GoBin:    strings.TrimSpace(os.Getenv(RealGoEnv)),
		MaxBytes: maxBytes,
	}
	if bin := strings.TrimSpace(os.Getenv(EnvBin)); bin != "" {
		opts.Pack = func(ctx context.Context, target string) error {
			return Compress(ctx, bin, target, maxBytes, nil)
		}
	}
	return RunWrapper(context.Background(), argv, opts, stdin, stdout, stderr)
}

// RunWrapper forwards one Go invocation, compresses the output of a successful `go build -o <file>`, and
// serves `go version -m <file>` from the pre-compression backup. | RunWrapper 转发一次 Go 调用，压缩成功的
// `go build -o <file>` 产物，并让 `go version -m <file>` 读取压缩前的备份。
//
// The compression is best effort: a missing packer, a failed UPX run or an oversized file only produces a
// bilingual warning and leaves the uncompressed binary. The backup exists because ko reads the Go build
// information with `go version -m`, which cannot parse a packed binary; it is removed as soon as ko asks. |
// 压缩是尽力而为：缺少压缩器、UPX 失败或文件超限都只产生双语告警并保留未压缩产物。备份的存在是因为 ko 会用
// `go version -m` 读取构建信息，而该命令无法解析已压缩的二进制；ko 一旦读取就删除备份。
func RunWrapper(ctx context.Context, argv []string, opts WrapperOptions, stdin io.Reader, stdout, stderr io.Writer) int {
	if strings.TrimSpace(opts.GoBin) == "" {
		fmt.Fprintf(stderr, "yd: the KO_GO_PATH wrapper needs %s | yd：KO_GO_PATH 包装器需要 %s\n", RealGoEnv, RealGoEnv)
		return 1
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runArgs := append([]string(nil), argv...)
	versionTarget := ""
	if len(runArgs) >= 3 && runArgs[0] == "version" && runArgs[1] == "-m" {
		versionTarget = runArgs[2]
		if _, err := os.Stat(versionTarget + backupSuffix); err == nil {
			runArgs[2] = versionTarget + backupSuffix
		}
	}
	code := ExitCode(opts.run()(ctx, opts.GoBin, runArgs, stdin, stdout, stderr))
	if code != 0 {
		return code
	}
	switch {
	case len(argv) > 0 && argv[0] == "build":
		if target := outputPath(argv); target != "" {
			packBuild(ctx, opts, target, stderr)
		}
	case versionTarget != "":
		// ko has read the build information; the backup can go. | ko 已读取构建信息，备份可以删除。
		_ = os.Remove(versionTarget + backupSuffix)
	}
	return 0
}

// packBuild keeps a pre-compression backup and runs the configured packer. | packBuild 保留压缩前备份并运行
// 配置的压缩器。
func packBuild(ctx context.Context, opts WrapperOptions, target string, stderr io.Writer) {
	if opts.Pack == nil {
		return
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		return
	}
	if limit := opts.maxBytes(); limit > 0 && info.Size() >= limit {
		fmt.Fprintf(stderr, "yd: %s exceeds the UPX size limit; keeping it uncompressed | yd：%s 超过 UPX 体积上限，保留未压缩文件\n", target, target)
		return
	}
	backup := target + backupSuffix
	_ = os.Remove(backup)
	if err := linkOrCopy(target, backup); err != nil {
		fmt.Fprintf(stderr, "yd: cannot keep a pre-UPX backup of %s: %v | yd：无法为 %s 保留压缩前备份：%v\n", target, err, target, err)
		return
	}
	if err := opts.Pack(ctx, target); err != nil {
		if restoreErr := restoreBackup(target, backup); restoreErr != nil {
			// Keep the backup for manual recovery when the original cannot be put back. | 原文件无法放回时
			// 保留备份以便手动恢复。
			fmt.Fprintf(stderr, "yd: UPX failed for %s; the original stays at %s: %v | yd：%s 的 UPX 压缩失败，原文件保留在 %s：%v\n",
				target, backup, restoreErr, target, backup, restoreErr)
			return
		}
		_ = os.Remove(backup)
		fmt.Fprintf(stderr, "yd: UPX failed for %s; keeping the original binary: %v | yd：%s 的 UPX 压缩失败，保留原文件：%v\n", target, err, target, err)
	}
}

// restoreBackup puts the pre-pack copy back after a failed packer. A target that still has the backup's
// size is treated as untouched, so its inode is left alone; anything else is removed and restored from
// the backup. | restoreBackup 在压缩器失败后放回压缩前副本。与备份大小相同的 target 视为未被改动，保持其
// inode 不变；其余情况删除后从备份恢复。
func restoreBackup(target, backup string) error {
	backupInfo, err := os.Stat(backup)
	if err != nil {
		return err
	}
	if targetInfo, err := os.Stat(target); err == nil && !targetInfo.IsDir() && targetInfo.Size() == backupInfo.Size() {
		return nil
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return linkOrCopy(backup, target)
}

// outputPath extracts the -o value from a go build invocation. | outputPath 从 go build 调用中提取 -o 取值。
func outputPath(args []string) string {
	out := ""
	for i, arg := range args {
		if arg == "-o" && i+1 < len(args) {
			out = args[i+1]
		}
		if strings.HasPrefix(arg, "-o=") {
			out = strings.TrimPrefix(arg, "-o=")
		}
	}
	return out
}

// linkOrCopy hard-links src to dst, falling back to a copy when the file system refuses links. | linkOrCopy 把
// src 硬链接到 dst，文件系统拒绝链接时回退为复制。
func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

// WrapperEnvironment returns the environment entries that turn executable into ko's Go wrapper. An explicit
// KO_GO_PATH already present in the environment is respected as the real Go target. | WrapperEnvironment 返回
// 把 executable 变成 ko 的 Go 包装器所需的环境项。环境中已有的显式 KO_GO_PATH 会被尊重为真实 Go 目标。
func WrapperEnvironment(executable, goBin string, bin Binary, maxBytes int64) map[string]string {
	env := map[string]string{
		WrapperMarkerEnv: "1",
		RealGoEnv:        goBin,
		"KO_GO_PATH":     executable,
		MaxBytesEnv:      strconv.FormatInt(maxBytes, 10),
	}
	if bin.Path != "" && !IsEmbedded(bin.Path) {
		env[EnvBin] = bin.Path
	}
	return env
}
