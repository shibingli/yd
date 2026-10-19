//go:build windows

package sysinfo

import (
	"syscall"
	"unsafe"
)

// kernel32 and GetSystemTimes are resolved lazily once per process. | kernel32 与 GetSystemTimes 每进程惰性解析一次。
var (
	modkernel32        = syscall.NewLazyDLL("kernel32.dll")
	procGetSystemTimes = modkernel32.NewProc("GetSystemTimes")
)

// fileTime mirrors the Windows FILETIME layout (100-nanosecond intervals since 1601). | fileTime 对应 Windows
// FILETIME 布局（自 1601 年起的 100 纳秒间隔）。
type fileTime struct {
	Low  uint32
	High uint32
}

// CPUSnapshot reads the host-wide idle, kernel and user times. | CPUSnapshot 读取全主机空闲、内核与用户时间。
//
// GetSystemTimes reports kernel time inclusive of idle time, so total = kernel +
// user and idle is already part of it. | GetSystemTimes 返回的内核时间包含空闲时间，
// 因此 total = kernel + user，而空闲时间已经包含在其中。
func CPUSnapshot() (CPUSample, bool) {
	var idle, kernel, user fileTime
	ret, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ret == 0 {
		return CPUSample{}, false
	}
	toTicks := func(ft fileTime) uint64 {
		return uint64(ft.High)<<32 | uint64(ft.Low)
	}
	k := toTicks(kernel)
	u := toTicks(user)
	return CPUSample{Total: k + u, Idle: toTicks(idle)}, true
}
