//go:build !windows

package service

// failureOption reports that Windows-only failure keys do not exist on this
// platform. | failureOption 表示当前平台没有 Windows 专属的失败恢复键。
func failureOption() (key, value string) { return "", "" }
