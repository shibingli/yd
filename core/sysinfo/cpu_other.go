//go:build !linux && !windows

package sysinfo

// CPUSnapshot reports that this platform does not expose aggregate CPU times. | CPUSnapshot 表示当前平台不提供汇总 CPU 时间。
func CPUSnapshot() (CPUSample, bool) { return CPUSample{}, false }
