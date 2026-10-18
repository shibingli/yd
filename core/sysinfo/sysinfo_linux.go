//go:build linux

package sysinfo

import (
	"os"
	"path/filepath"
	"strings"
)

// meminfo reads /proc/meminfo once per call. | meminfo 每次调用读取一次 /proc/meminfo。
func meminfo() map[string]uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return nil
	}
	return parseMeminfo(string(data))
}

func readTotalMemory() uint64 {
	return meminfo()["MemTotal"]
}

// cgroupMemoryFiles returns the cgroup v2 and v1 memory limit/usage files for
// the current process, with the root fallbacks. | cgroupMemoryFiles 返回当前进程的
// cgroup v2/v1 内存限额与用量文件，并附带根目录回退。
func cgroupMemoryFiles() (limits, usages []string) {
	limits = []string{
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
	}
	usages = []string{
		"/sys/fs/cgroup/memory.current",
		"/sys/fs/cgroup/memory/memory.usage_in_bytes",
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return limits, usages
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			rel := strings.TrimPrefix(parts[2], "/")
			base := filepath.Join("/sys/fs/cgroup", rel)
			limits = append([]string{filepath.Join(base, "memory.max")}, limits...)
			usages = append([]string{filepath.Join(base, "memory.current")}, usages...)
			continue
		}
		if parts[1] == "memory" {
			rel := strings.TrimPrefix(parts[2], "/")
			base := filepath.Join("/sys/fs/cgroup/memory", rel)
			limits = append([]string{filepath.Join(base, "memory.limit_in_bytes")}, limits...)
			usages = append([]string{filepath.Join(base, "memory.usage_in_bytes")}, usages...)
		}
	}
	return limits, usages
}

func readFileUint(path string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return parseLimit(string(data))
}

func readMemoryLimit() uint64 {
	limits, _ := cgroupMemoryFiles()
	var cgroup uint64
	for _, p := range limits {
		if n := readFileUint(p); n != 0 {
			cgroup = n
			break
		}
	}
	return clampLimit(cgroup, readTotalMemory())
}

func readAvailableMemory() uint64 {
	mi := meminfo()
	avail := mi["MemAvailable"]
	if avail == 0 {
		avail = mi["MemFree"]
	}
	limit := MemoryLimit()
	if limit == 0 {
		return avail
	}
	_, usages := cgroupMemoryFiles()
	var used uint64
	for _, p := range usages {
		if n := readFileUint(p); n != 0 {
			used = n
			break
		}
	}
	if used < limit {
		room := limit - used
		if avail == 0 || room < avail {
			return room
		}
	}
	return avail
}
