// This file stages the embedded Linux UPX payload into the docker build context and removes it again. |
// 本文件把嵌入的 Linux UPX payload 暂存进 docker 构建上下文，并在构建结束后移除。
package docker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shibingli/yd/tooling/payload"
	"github.com/shibingli/yd/tooling/upx"
	ydlog "github.com/shibingli/ydx/log"
)

// upxStagedPath is the build-context location the Dockerfile copies UPX from. | upxStagedPath 是 Dockerfile
// 读取 UPX 的构建上下文位置。
const upxStagedPath = ".yd/upx/upx"

// stagedUPX records the files and directories a staging created, so cleanup removes exactly those. | stagedUPX
// 记录一次暂存创建的文件与目录，使清理只删除这些条目。
type stagedUPX struct {
	// file is the staged binary; empty means nothing was created. | file 是暂存的二进制；为空表示没有创建。
	file string
	// dirs lists the directories created by the staging, outermost first. | dirs 按从外到内列出暂存创建的目录。
	dirs []string
}

// stageUPX writes the Linux payload matching the target platform into the build context. It never fails a
// build: an unsupported platform, an unwritable directory or an existing staged file only produces a
// warning or a debug line. | stageUPX 把与目标平台匹配的 Linux payload 写入构建上下文。它绝不使构建失败：
// 不支持的平台、不可写目录或已存在的暂存文件只会产生告警或调试日志。
func stageUPX(o Options, log *ydlog.Logger) *stagedUPX {
	arch, ok := targetArch(o.Platform)
	if !ok {
		log.Warn("docker: UPX does not support the target platform; skipping compression | docker：UPX 不支持目标平台，跳过压缩",
			ydlog.Str("platform", o.Platform))
		return nil
	}
	payloadData, ok := upx.EmbeddedLinux(arch)
	if !ok {
		log.Warn("docker: no embedded UPX payload for the target architecture; skipping compression | docker：目标架构没有嵌入的 UPX payload，跳过压缩",
			ydlog.Str("arch", arch))
		return nil
	}
	target := filepath.Join(o.Dir, filepath.FromSlash(upxStagedPath))
	if _, err := os.Stat(target); err == nil {
		log.Debug("docker: existing staged UPX kept | docker：保留已有的暂存 UPX", ydlog.Str("path", target))
		return nil
	} else if !os.IsNotExist(err) {
		log.Warn("docker: cannot inspect the staged UPX path; skipping compression | docker：无法检查暂存 UPX 路径，跳过压缩",
			ydlog.Err(err), ydlog.Str("path", target))
		return nil
	}

	staged := &stagedUPX{file: target}
	base := filepath.Join(o.Dir, ".yd")
	upxDir := filepath.Dir(target)
	for _, dir := range []string{base, upxDir} {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			staged.dirs = append(staged.dirs, dir)
		}
	}
	if err := payload.InstallAt(target, payloadData, payload.Key(payloadData)); err != nil {
		log.Warn("docker: cannot stage the UPX binary; skipping compression | docker：无法暂存 UPX 二进制，跳过压缩",
			ydlog.Err(err), ydlog.Str("path", target))
		staged.cleanup(log)
		return nil
	}
	log.Debug("docker: UPX staged | docker：UPX 已暂存", ydlog.Str("path", target), ydlog.Str("arch", arch))
	return staged
}

// targetArch extracts the architecture of the docker build platform. An empty platform means the host
// architecture; a non-Linux or non-amd64/arm64 platform is unsupported. | targetArch 提取 docker 构建平台的
// 架构。空平台表示宿主架构；非 Linux 或非 amd64/arm64 的平台不支持。
func targetArch(platform string) (string, bool) {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return runtime.GOARCH, true
	}
	parts := strings.Split(platform, "/")
	if len(parts) < 2 || parts[0] != "linux" {
		return "", false
	}
	switch parts[1] {
	case "amd64", "arm64":
		return parts[1], true
	default:
		return "", false
	}
}

// cleanup removes the staged binary and the directories this staging created, ignoring entries a user or a
// concurrent build already owns. | cleanup 删除暂存的二进制与本文件创建的目录，忽略用户或并发构建已拥有的条目。
func (s *stagedUPX) cleanup(log *ydlog.Logger) {
	if s == nil {
		return
	}
	if s.file != "" {
		if err := os.Remove(s.file); err != nil && !os.IsNotExist(err) {
			log.Warn("docker: staged UPX could not be removed | docker：暂存的 UPX 无法移除",
				ydlog.Err(err), ydlog.Str("path", s.file))
		}
	}
	for i := len(s.dirs) - 1; i >= 0; i-- {
		if err := os.Remove(s.dirs[i]); err != nil && !os.IsNotExist(err) {
			log.Debug("docker: staged UPX directory kept | docker：保留暂存 UPX 目录",
				ydlog.Err(err), ydlog.Str("path", s.dirs[i]))
		}
	}
	s.file, s.dirs = "", nil
}
