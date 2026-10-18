package ws

import "github.com/shibingli/yd/core/sysinfo"

// Tuned default bounds. | 自动调优默认值的边界。
const (
	// maxTunedBufferSize caps the sysinfo-derived per-connection buffer. | maxTunedBufferSize 是 sysinfo 推导出的每连接缓冲上限。
	maxTunedBufferSize = 64 << 10
	// minTunedMaxMessageSize / maxTunedMaxMessageSize bound the memory-derived per-message limit. | minTunedMaxMessageSize / maxTunedMaxMessageSize 是内存推导出的单条消息上限的上下界。
	minTunedMaxMessageSize = 1 << 20
	maxTunedMaxMessageSize = 32 << 20
)

// tunedReadBufferSize returns the per-connection read buffer default. | tunedReadBufferSize
// 返回每连接读缓冲的默认值。
func tunedReadBufferSize() int { return tunedBufferSize() }

// tunedWriteBufferSize returns the per-connection write buffer default. | tunedWriteBufferSize
// 返回每连接写缓冲的默认值。
func tunedWriteBufferSize() int { return tunedBufferSize() }

// tunedBufferSize derives a per-connection buffer from the actual CPU count and
// memory ceiling instead of assuming a desktop host. | tunedBufferSize 根据实际
// CPU 数与内存上限推导每连接缓冲，而不是假定桌面主机。
//
// More scheduler slots mean more concurrent connections and more aggregate
// pipeline traffic, so the buffer grows in tiers with GOMAXPROCS: 4 KiB up to
// 15 Ps, 8 KiB at 16–31, 16 KiB at 32–63 and 32 KiB from 64 Ps on. The result
// is capped by the process memory ceiling: one connection's buffer may use at
// most 1/256 of one P's memory share, so a small cgroup does not silently
// multiply its budget by the connection count. | 更多调度槽意味着更多并发连接与
// 更大的聚合流水线流量，因此缓冲随 GOMAXPROCS 分层增长：15 个 P 以内为 4 KiB，
// 16–31 为 8 KiB，32–63 为 16 KiB，64 个 P 起为 32 KiB。结果再受进程内存上限约束：
// 单连接的缓冲至多占一个 P 内存份额的 1/256，小 cgroup 因此不会把预算随连接数悄悄放大。
func tunedBufferSize() int {
	const base = defaultReadBufferSize

	n := sysinfo.Parallelism()
	size := base
	switch {
	case n >= 64:
		size = 32 << 10
	case n >= 32:
		size = 16 << 10
	case n >= 16:
		size = 8 << 10
	}

	if limit := sysinfo.MemoryLimit(); limit > 0 {
		if perP := limit / uint64(n) / 256; perP < uint64(size) {
			size = int(perP)
		}
	}
	if size < base {
		size = base
	}
	if size > maxTunedBufferSize {
		size = maxTunedBufferSize
	}
	return size
}

// tunedMaxMessageSize derives the per-message limit from the process memory
// ceiling. | tunedMaxMessageSize 根据进程内存上限推导单条消息上限。
//
// A process that may hold a maximum-size message on every connection needs a
// limit proportional to the memory it actually has; 64 concurrent maximum-size
// messages are budgeted, clamped to [1 MiB, 32 MiB]. Platforms that cannot
// report a limit keep the historical 32 MiB default. | 每条连接都可能持有最大消息的
// 进程，其上限应与实际可用内存成比例；这里按 64 条并发最大消息做预算，并夹在
// [1 MiB, 32 MiB] 内。平台无法报告内存上限时沿用历史默认值 32 MiB。
func tunedMaxMessageSize() int64 {
	limit := sysinfo.MemoryLimit()
	if limit == 0 {
		return maxTunedMaxMessageSize
	}
	size := int64(limit / 64)
	if size < minTunedMaxMessageSize {
		size = minTunedMaxMessageSize
	}
	if size > maxTunedMaxMessageSize {
		size = maxTunedMaxMessageSize
	}
	return size
}
