//go:build linux

package sysinfo

import (
	"os"
	"strconv"
	"strings"
)

// CPUSnapshot reads the aggregate "cpu" line of /proc/stat. | CPUSnapshot 读取 /proc/stat 的汇总 "cpu" 行。
//
// Fields are user nice system idle iowait irq softirq steal (guest times are
// already included in user/nice, so adding them would double count). Total is
// their sum and Idle is idle + iowait, the same convention top uses. | 字段依次为
// user nice system idle iowait irq softirq steal（guest 时间已包含在 user/nice 中，再加会重复计数）。
// Total 是它们的和，Idle 是 idle + iowait，与 top 的口径一致。
func CPUSnapshot() (CPUSample, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return CPUSample{}, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		if len(fields) < 4 {
			return CPUSample{}, false
		}
		var total, idle uint64
		for i, field := range fields {
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				return CPUSample{}, false
			}
			if i >= 8 {
				break
			}
			total += value
			if i == 3 || i == 4 {
				idle += value
			}
		}
		return CPUSample{Total: total, Idle: idle}, true
	}
	return CPUSample{}, false
}
