// Package payload embeds pinned third-party tool binaries and materializes them into .yd/tools on demand. |
// Package payload 嵌入固定版本的第三方工具二进制，并在需要时物化到 .yd/tools。
//
// The host platform selects exactly one build-tagged payload set, so a released yd binary carries only the
// tools it can run. Payloads are gzip-compressed, checksum-locked, cached by content address and extracted
// atomically; a missing payload for the host is not an error, it simply leaves the tool to the normal
// download/PATH chain. | 宿主平台只选择一组带构建标签的 payload，因此发布出的 yd 只携带它能运行的工具。
// payload 以 gzip 压缩、校验和锁定、按内容地址缓存并原子解压；宿主没有 payload 不算错误，只是把工具交回正常的
// 下载/PATH 解析链。
package payload

import (
	"path/filepath"
	"strings"

	"github.com/shibingli/ydx/errs"
)

// The embedded tool names, versions and the project-local tool directory. | 嵌入的工具名、版本与项目内工具目录。
const (
	// ToolKo is the Dockerfile-less image builder. | ToolKo 是无 Dockerfile 镜像构建器。
	ToolKo = "ko"
	// ToolMinify is the JavaScript/SVG minifier. | ToolMinify 是 JavaScript/SVG 压缩器。
	ToolMinify = "minify"
	// ToolBetteralign is the struct-alignment analyzer. | ToolBetteralign 是结构体对齐分析器。
	ToolBetteralign = "betteralign"

	// VersionKo is the pinned ko release. | VersionKo 是固定的 ko 版本。
	VersionKo = "v0.19.1"
	// VersionMinify is the pinned minify release. | VersionMinify 是固定的 minify 版本。
	VersionMinify = "v2.24.24"
	// VersionBetteralign is the pinned betteralign release. | VersionBetteralign 是固定的 betteralign 版本。
	VersionBetteralign = "v0.15.4"

	// LocalToolDir is the shared project-local tool directory. | LocalToolDir 是共用的项目内工具目录。
	LocalToolDir = ".yd/tools"

	// markerPrefix and markerSuffix wrap the read-only embedded marker. | markerPrefix 与 markerSuffix 包裹只读嵌入标记。
	markerPrefix = "<embedded:"
	markerSuffix = ">"
)

// ErrExtract reports a failed payload extraction. | ErrExtract 表示 payload 解压失败。
var ErrExtract = errs.New("payload: extraction failed | payload：解压失败")

// Entry describes one embedded payload. | Entry 描述一个嵌入 payload。
type Entry struct {
	// Tool is the registry name, e.g. ko. | Tool 是注册表名称，例如 ko。
	Tool string
	// Version is the pinned tool version. | Version 是固定的工具版本。
	Version string
	// FileName is the file name under .yd/tools on this host. | FileName 是本宿主 .yd/tools 下的文件名。
	FileName string
	// SHA256 locks the compressed bytes. | SHA256 锁定压缩字节。
	SHA256 string
	// Data is the gzip-compressed binary. | Data 是 gzip 压缩的二进制。
	Data []byte
}

// Host returns the payloads embedded for the running platform. | Host 返回当前平台嵌入的 payload。
func Host() []Entry {
	entries := hostEntries()
	return append([]Entry(nil), entries...)
}

// Lookup returns the embedded payload of one tool. | Lookup 返回一个工具的嵌入 payload。
func Lookup(tool string) (Entry, bool) {
	for _, entry := range hostEntries() {
		if entry.Tool == tool {
			return entry, true
		}
	}
	return Entry{}, false
}

// Available reports whether the running platform embeds the tool. | Available 报告当前平台是否嵌入了该工具。
func Available(tool string) bool {
	_, ok := Lookup(tool)
	return ok
}

// Marker returns the read-only placeholder path for an embedded tool. | Marker 返回嵌入工具的只读占位路径。
func Marker(tool string) string { return markerPrefix + tool + markerSuffix }

// IsMarker reports whether path is an embedded marker. | IsMarker 报告 path 是否为嵌入标记。
func IsMarker(path string) bool {
	return strings.HasPrefix(path, markerPrefix) && strings.HasSuffix(path, markerSuffix)
}

// Ensure materializes the embedded payload of tool into <dir>/.yd/tools and returns its path. The second
// result is false when this platform has no payload; an existing non-empty file always wins, so a
// user-installed copy is never overwritten, while a zero-byte leftover is repaired. | Ensure 把 tool 的嵌入
// payload 物化到 <dir>/.yd/tools 并返回路径。第二个返回值在本平台没有 payload 时为 false；已存在的非空文件
// 始终优先，因此用户安装的副本绝不会被覆盖，零字节残留则会被修复。
func Ensure(dir, tool string) (string, bool, error) {
	entry, ok := Lookup(tool)
	if !ok {
		return "", false, nil
	}
	return ensureEntry(dir, entry)
}

// ensureEntry extracts one entry through the content-addressed cache; it is separate from Ensure so tests
// can drive a synthetic payload. | ensureEntry 经内容寻址缓存解压一个条目；与 Ensure 分开以便测试注入合成 payload。
func ensureEntry(dir string, entry Entry) (string, bool, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", false, errs.WrapDetail(err, ErrExtract, "resolve %s | 解析 %s", dir, dir)
	}
	target := filepath.Join(root, LocalToolDir, entry.FileName)
	if err := InstallAt(target, entry.Data, entry.SHA256); err != nil {
		return "", false, err
	}
	return target, true, nil
}
