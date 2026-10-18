//go:build windows

package service

import "github.com/kardianos/service"

// failureOption returns the Windows-only kardianos failure keys. | failureOption
// 返回 kardianos 仅在 Windows 提供的失败恢复键。
func failureOption() (key, value string) {
	return service.OnFailure, service.OnFailureRestart
}
