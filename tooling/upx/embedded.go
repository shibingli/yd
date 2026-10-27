// This file embeds the pinned UPX payloads and extracts them atomically. | 本文件嵌入固定版本的 UPX payload
// 并以原子方式解压。
package upx

import (
	_ "embed"
	"path/filepath"
	"runtime"

	"github.com/shibingli/yd/tooling/payload"
	"github.com/shibingli/ydx/errs"
)

// The payload files are committed without an executable suffix so .gitignore's *.exe rule cannot hide them. |
// payload 文件入库时不带可执行后缀，避免被 .gitignore 的 *.exe 规则忽略。
//
// Each checksum locks the exact bytes shipped inside yd; the lock test fails when a payload drifts. |
// 每个校验和锁定 yd 内嵌的确切字节；payload 漂移时锁定测试会失败。
const (
	// LinuxAMD64SHA256 is the SHA-256 of the linux/amd64 payload. | LinuxAMD64SHA256 是 linux/amd64 payload 的 SHA-256。
	LinuxAMD64SHA256 = "287b3dffe9dcafd8e366e162ac4ab41e5cf45a3c6768970256af0869288d84a1"
	// LinuxARM64SHA256 is the SHA-256 of the linux/arm64 payload. | LinuxARM64SHA256 是 linux/arm64 payload 的 SHA-256。
	LinuxARM64SHA256 = "871d65c543cfda923d8502b92a58d4934c434aead8746d7b4a7f6dad66eb3dad"
	// WindowsAMD64SHA256 is the SHA-256 of the windows/amd64 payload. | WindowsAMD64SHA256 是 windows/amd64 payload 的 SHA-256。
	WindowsAMD64SHA256 = "d20ebe0b7b22b6be968c8c34be61f94ddea12cb11462e2cec27f548ef9574df8"
)

var (
	//go:embed data/upx-linux-amd64
	payloadLinuxAMD64 []byte
	//go:embed data/upx-linux-arm64
	payloadLinuxARM64 []byte
	//go:embed data/upx-windows-amd64
	payloadWindowsAMD64 []byte
)

// EmbeddedLinux returns the Linux UPX payload for a target architecture. | EmbeddedLinux 返回目标架构对应的
// Linux UPX payload。
func EmbeddedLinux(arch string) ([]byte, bool) {
	switch arch {
	case "amd64":
		return payloadLinuxAMD64, true
	case "arm64":
		return payloadLinuxARM64, true
	default:
		return nil, false
	}
}

// EmbeddedHost returns the UPX payload for the host platform and its file name. Linux reuses the matching
// target payload; Windows/amd64 uses the win64 binary; other hosts have no embedded payload and fall back to
// an external binary. | EmbeddedHost 返回宿主平台对应的 UPX payload 与文件名。Linux 复用对应的目标
// payload；Windows/amd64 使用 win64 二进制；其它宿主没有嵌入 payload，回退到外部二进制。
func EmbeddedHost() ([]byte, string, bool) {
	switch runtime.GOOS {
	case "linux":
		payload, ok := EmbeddedLinux(runtime.GOARCH)
		return payload, upxBinaryName(), ok
	case "windows":
		if runtime.GOARCH == "amd64" {
			return payloadWindowsAMD64, upxBinaryName(), true
		}
	}
	return nil, "", false
}

// EmbeddedHostAvailable reports whether this host has an embedded UPX payload. | EmbeddedHostAvailable
// 报告当前宿主是否有嵌入的 UPX payload。
func EmbeddedHostAvailable() bool {
	_, _, ok := EmbeddedHost()
	return ok
}

// HostBinaryName returns the host file name of the UPX executable. | HostBinaryName 返回宿主上 UPX 可执行文件的文件名。
func HostBinaryName() string { return upxBinaryName() }

// Materialize writes payload to <dir>/.yd/tools/<name> through the shared content-addressed cache and returns
// the absolute path. An existing non-empty file wins, so a newer copy installed by `yd tools install` is
// never overwritten (a zero-byte leftover is repaired); the same payload is decompressed only once per
// machine. | Materialize 把 payload 经共享的内容寻址缓存写入 <dir>/.yd/tools/<name> 并返回绝对路径。已存在的
// 非空文件优先，因此 `yd tools install` 安装的新版本绝不会被覆盖（零字节残留会被修复）；同一 payload 每台机器
// 只解压一次。
func Materialize(dir, name string, data []byte) (string, error) {
	if name == "" {
		return "", errs.Detail(ErrBinaryNotFound, "empty payload name | payload 名称为空")
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", errs.WrapDetail(err, ErrBinaryNotFound, "resolve %s | 解析 %s", dir, dir)
	}
	target := filepath.Join(root, LocalToolDir, name)
	if err := payload.InstallAt(target, data, payload.Key(data)); err != nil {
		return "", errs.WrapDetail(err, ErrBinaryNotFound, "materialize %s | 物化 %s", target, target)
	}
	return target, nil
}
