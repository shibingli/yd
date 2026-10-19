// This file holds the one resolver for user-supplied filesystem paths in the CLI. | 本文件保存 CLI 中
// 用户输入文件系统路径的唯一解析器。

package cli

import (
	"path/filepath"
	"runtime"
	"strings"
)

// resolveUserPath resolves a user-supplied path against workDir when it is relative. | resolveUserPath
// 在用户输入的路径为相对路径时按 workDir 展开。
//
// Windows has shapes that filepath.IsAbs does not recognize but that must never be joined as a child
// of workDir: a drive-relative path ("C:proj") and a rooted path ("/x" or `\x`), which is relative
// to the current drive. Passing them to filepath.Abs lets Windows apply its own rules instead of
// producing an invalid component such as `workDir\C:proj`. On POSIX a backslash is an ordinary file
// name character, so the rooted checks are Windows-only. | Windows 有些形态不被 filepath.IsAbs 识别，
// 但绝不能当作 workDir 的子路径拼接：盘符相对路径（"C:proj"），以及相对于当前驱动器的根路径（"/x" 或
// `\x`）。把它们交给 filepath.Abs，由 Windows 按自身规则解析，而不是产生 `workDir\C:proj` 这类非法分量。
// POSIX 上的反斜杠是普通文件名字符，因此根路径检查只在 Windows 上生效。
func resolveUserPath(workDir, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	rooted := runtime.GOOS == "windows" && (strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`))
	if filepath.IsAbs(p) || filepath.VolumeName(p) != "" || rooted {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	if workDir == "" {
		return p
	}
	return filepath.Join(workDir, p)
}
