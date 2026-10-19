//go:build windows

package sysinfo

import (
	"syscall"
	"unsafe"
)

// memoryStatusEx mirrors the Windows MEMORYSTATUSEX structure. | memoryStatusEx 对应 Windows MEMORYSTATUSEX 结构。
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// procGlobalMemoryStatusEx is resolved lazily once per process. | procGlobalMemoryStatusEx 每进程惰性解析一次。
var procGlobalMemoryStatusEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// memoryStatus calls GlobalMemoryStatusEx and reports whether it succeeded. | memoryStatus 调用 GlobalMemoryStatusEx 并报告是否成功。
func memoryStatus() (memoryStatusEx, bool) {
	status := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	ret, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	return status, ret != 0
}

// readTotalMemory returns the physical memory installed on the host. | readTotalMemory 返回宿主机安装的物理内存。
func readTotalMemory() uint64 {
	status, ok := memoryStatus()
	if !ok {
		return 0
	}
	return status.TotalPhys
}

// readMemoryLimit returns the host physical memory as the effective ceiling. | readMemoryLimit 以主机物理内存作为生效上限。
func readMemoryLimit() uint64 { return readTotalMemory() }

// readAvailableMemory returns the physical memory currently available. | readAvailableMemory 返回当前可用的物理内存。
func readAvailableMemory() uint64 {
	status, ok := memoryStatus()
	if !ok {
		return 0
	}
	return status.AvailPhys
}
