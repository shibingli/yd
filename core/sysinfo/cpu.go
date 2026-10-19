package sysinfo

// CPUSample is one cumulative CPU-time snapshot of the host. | CPUSample 是宿主机的一次累计 CPU 时间快照。
//
// Total and Idle are expressed in the platform's own tick unit; only their
// differences are meaningful, and Usage divides them, so no unit conversion is
// needed. | Total 与 Idle 使用平台自身的计时单位；只有它们的差值有意义，且 Usage 只做除法，
// 因此不需要单位换算。
type CPUSample struct {
	// Total is the cumulative total CPU time since boot. | Total 是自启动以来的累计总 CPU 时间。
	Total uint64
	// Idle is the cumulative idle time included in Total. | Idle 是包含在 Total 中的累计空闲时间。
	Idle uint64
}

// Usage returns the busy percentage between prev and s, in [0, 100]. | Usage 返回 prev 与 s 之间的忙碌百分比，取值 [0, 100]。
//
// A snapshot that did not advance (or went backwards) reports 0 instead of a
// negative or +Inf value, so a counter reset can never poison a gauge. | 快照没有前进（或倒退）时返回 0，
// 而不是负数或 +Inf，因此计数器重置不会污染仪表盘。
func (s CPUSample) Usage(prev CPUSample) float64 {
	if s.Total <= prev.Total {
		return 0
	}
	total := s.Total - prev.Total
	idle := uint64(0)
	if s.Idle > prev.Idle {
		idle = s.Idle - prev.Idle
	}
	if idle > total {
		idle = total
	}
	return float64(total-idle) / float64(total) * 100
}
