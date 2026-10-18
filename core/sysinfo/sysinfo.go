// Package sysinfo exposes the runtime capacities algorithms need to size
// parallelism and buffers: logical CPUs, GOMAXPROCS, and (where the platform
// exposes it) the memory limit and currently available memory. | Package sysinfo
// 暴露算法用来确定并行度与缓冲大小的运行时容量：逻辑 CPU 数、GOMAXPROCS，以及
// 平台可提供的内存上限与当前可用内存。
//
// Every value is read from the running process's environment; nothing is
// hard-coded. Memory queries are container-aware on Linux (cgroup v1/v2 plus
// /proc/meminfo) and return 0 when the platform cannot report a value, which
// lets callers fall back to a conservative default. | 所有取值都来自进程运行环境，
// 不写死任何数值。Linux 的内存查询感知 cgroup v1/v2 与 /proc/meminfo；平台无法
// 提供时返回 0，调用方据此回退到保守默认值。
package sysinfo

import (
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// NumCPU returns the number of logical CPUs usable by the current process. | NumCPU
// 返回当前进程可用的逻辑 CPU 数。
func NumCPU() int { return runtime.NumCPU() }

// GOMAXPROCS returns the current Go scheduler parallelism, which is the value
// algorithms should use for worker counts. Go keeps it container-aware on
// Linux, so a cgroup CPU quota is reflected here. | GOMAXPROCS 返回当前 Go 调度器
// 并行度，算法应以它作为工作协程数；Go 在 Linux 上使其感知容器配额。
func GOMAXPROCS() int { return runtime.GOMAXPROCS(0) }

// Parallelism returns the worker count to use for CPU-bound work: GOMAXPROCS,
// clamped to at least one. | Parallelism 返回 CPU 密集工作应使用的工作协程数：
// GOMAXPROCS，至少为一。
func Parallelism() int {
	if n := GOMAXPROCS(); n > 0 {
		return n
	}
	return 1
}

var (
	totalOnce sync.Once
	total     uint64
	limitOnce sync.Once
	limit     uint64
)

// IsMultiCore reports whether the current environment offers more than one
// scheduler slot. Algorithms use it to choose their parallel or serial path
// without asking the caller for a core count. | IsMultiCore 报告当前环境是否
// 提供多于一个调度槽；算法据此选择并行或串行路径，无需调用方指定核心数。
func IsMultiCore() bool { return Parallelism() > 1 }

// MemoryTotal returns the total memory visible to the process in bytes, or 0
// when the platform does not expose it. | MemoryTotal 返回进程可见的总内存字节数；
// 平台不提供时返回 0。
func MemoryTotal() uint64 {
	totalOnce.Do(func() { total = readTotalMemory() })
	return total
}

// MemoryLimit returns the effective memory ceiling in bytes: the smaller of the
// host total and the cgroup limit, or 0 when unknown. | MemoryLimit 返回生效的内存
// 上限字节数：主机总量与 cgroup 限额中的较小者；未知时返回 0。
func MemoryLimit() uint64 {
	limitOnce.Do(func() { limit = readMemoryLimit() })
	return limit
}

// MemoryAvailable returns the memory currently available to the process in
// bytes, or 0 when unknown. Unlike the other queries it is not cached, because
// it changes while the process runs. | MemoryAvailable 返回进程当前可用内存字节数；
// 未知时返回 0。与其它查询不同，它随进程运行而变化，因此不缓存。
func MemoryAvailable() uint64 { return readAvailableMemory() }

// parseMeminfo extracts MemTotal, MemFree and MemAvailable from /proc/meminfo
// content, converting kB values to bytes. | parseMeminfo 从 /proc/meminfo 内容中
// 提取 MemTotal、MemFree、MemAvailable，并把 kB 换算为字节。
func parseMeminfo(data string) map[string]uint64 {
	out := make(map[string]uint64, 3)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		switch key {
		case "MemTotal", "MemFree", "MemAvailable":
		default:
			continue
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		out[key] = n * 1024
	}
	return out
}

// parseLimit parses a cgroup memory limit. "max" and sentinel values that mean
// "unlimited" return 0. | parseLimit 解析 cgroup 内存限额；"max" 与表示「无限」的
// 哨兵值返回 0。
func parseLimit(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "max" {
		return 0
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	// cgroup v1 uses a page-rounded "unlimited" value near 2^63; anything that
	// large is not a real ceiling. | cgroup v1 用接近 2^63 的页对齐值表示「无限」；
	// 这么大的数值不是真实上限。
	if n >= 1<<60 {
		return 0
	}
	return n
}

// clampLimit picks the smaller of a cgroup limit and the host total. | clampLimit
// 在 cgroup 限额与主机总量之间取较小者。
func clampLimit(cgroup, total uint64) uint64 {
	switch {
	case cgroup == 0:
		return total
	case total == 0:
		return cgroup
	case cgroup < total:
		return cgroup
	default:
		return total
	}
}
