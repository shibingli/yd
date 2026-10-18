package sysinfo

import "testing"

// TestParseMeminfo converts kB values and ignores unrelated lines. | TestParseMeminfo
// 换算 kB 取值并忽略无关行。
func TestParseMeminfo(t *testing.T) {
	mi := parseMeminfo("MemTotal:       2048 kB\nMemFree: 512 kB\nMemAvailable:  1024 kB\nBuffers: 9 kB\n")
	if mi["MemTotal"] != 2048*1024 || mi["MemFree"] != 512*1024 || mi["MemAvailable"] != 1024*1024 {
		t.Fatalf("parseMeminfo = %v | 解析结果不符", mi)
	}
	if _, ok := mi["Buffers"]; ok {
		t.Fatalf("parseMeminfo kept an unrelated key | 保留了无关键")
	}
}

// TestParseLimit handles max, unlimited sentinels and real limits. | TestParseLimit
// 处理 max、无限哨兵与真实限额。
func TestParseLimit(t *testing.T) {
	cases := map[string]uint64{
		"max":                 0,
		"":                    0,
		"not-a-number":        0,
		"9223372036854771712": 0,
		"1073741824":          1 << 30,
		" 268435456\n":        1 << 28,
	}
	for in, want := range cases {
		if got := parseLimit(in); got != want {
			t.Fatalf("parseLimit(%q) = %d, want %d | 解析限额不符", in, got, want)
		}
	}
}

// TestClampLimit prefers the smaller ceiling. | TestClampLimit 取较小的上限。
func TestClampLimit(t *testing.T) {
	if got := clampLimit(0, 100); got != 100 {
		t.Fatalf("clampLimit(0,100) = %d | 结果不符", got)
	}
	if got := clampLimit(50, 0); got != 50 {
		t.Fatalf("clampLimit(50,0) = %d | 结果不符", got)
	}
	if got := clampLimit(50, 100); got != 50 {
		t.Fatalf("clampLimit(50,100) = %d | 结果不符", got)
	}
	if got := clampLimit(100, 50); got != 50 {
		t.Fatalf("clampLimit(100,50) = %d | 结果不符", got)
	}
}

// TestParallelismIsPositive guards the worker-count contract. | TestParallelismIsPositive
// 守卫工作协程数至少为一的约定。
func TestParallelismIsPositive(t *testing.T) {
	if Parallelism() < 1 {
		t.Fatalf("Parallelism() = %d | 并行度必须至少为一", Parallelism())
	}
}
