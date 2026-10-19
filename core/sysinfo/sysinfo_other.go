//go:build !linux && !windows

package sysinfo

// readTotalMemory reports that this platform does not expose total memory. | readTotalMemory
// 表示当前平台不提供总内存。
func readTotalMemory() uint64 { return 0 }

// readMemoryLimit reports that this platform does not expose a memory limit. | readMemoryLimit
// 表示当前平台不提供内存上限。
func readMemoryLimit() uint64 { return 0 }

// readAvailableMemory reports that this platform does not expose available memory. | readAvailableMemory
// 表示当前平台不提供可用内存。
func readAvailableMemory() uint64 { return 0 }
