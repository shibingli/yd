package sysinfo

import "testing"

// TestCPUSampleUsage covers the percentage arithmetic, including the reset and
// idle-over-total edge cases that would otherwise produce a negative or infinite
// gauge value. | TestCPUSampleUsage 覆盖百分比算术，包括重置与空闲超过总量的边界；
// 这些边界原本会产生负数或无穷大的仪表盘取值。
func TestCPUSampleUsage(t *testing.T) {
	tests := []struct {
		name string
		prev CPUSample
		next CPUSample
		want float64
	}{
		{"half busy", CPUSample{Total: 100, Idle: 40}, CPUSample{Total: 200, Idle: 90}, 50},
		{"all idle", CPUSample{Total: 100, Idle: 40}, CPUSample{Total: 200, Idle: 140}, 0},
		{"all busy", CPUSample{Total: 100, Idle: 40}, CPUSample{Total: 200, Idle: 40}, 100},
		{"counter did not advance", CPUSample{Total: 200, Idle: 90}, CPUSample{Total: 200, Idle: 90}, 0},
		{"counter went backwards", CPUSample{Total: 200, Idle: 90}, CPUSample{Total: 100, Idle: 40}, 0},
		{"idle jumped past total", CPUSample{Total: 100, Idle: 40}, CPUSample{Total: 200, Idle: 300}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.next.Usage(tc.prev); got != tc.want {
				t.Errorf("Usage = %v, want %v | Usage = %v，期望 %v", got, tc.want, got, tc.want)
			}
		})
	}
}
