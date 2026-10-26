//go:build !unix && !windows

package toolchain

import "os/exec"

// prepareChildTree is a no-op on platforms without a supported process-tree mechanism. | prepareChildTree
// 在不支持进程树机制的平台上为空操作。
func prepareChildTree(*exec.Cmd) {}

// signalChildTree reports that the platform has no graceful tree signal. | signalChildTree 表示该平台
// 没有进程树级的优雅信号。
func signalChildTree(int) bool { return false }

// killChildTree reports that the platform has no supported tree kill; the caller falls back to the
// direct child. | killChildTree 表示该平台没有受支持的进程树终止方式；调用方回退到直接子进程。
func killChildTree(int) bool { return false }
