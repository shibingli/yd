package logx

import (
	"io"
	"testing"
	"time"
)

// TestDisabledPathIsAllocationFree is a regression guard: a call at a disabled level must not allocate any heap memory. | TestDisabledPathIsAllocationFree 是回归护栏：禁用级别下调用不得产生任何堆分配。
func TestDisabledPathIsAllocationFree(t *testing.T) {
	l := New(Options{Writer: io.Discard, Level: ErrorLevel})
	allocs := testing.AllocsPerRun(1000, func() {
		l.Info("request handled", Str("method", "GET"), Int("status", 200))
	})
	if allocs != 0 {
		t.Fatalf("禁用级别路径分配数 = %v，期望 0", allocs)
	}
}

// BenchmarkInfoFields measures the end-to-end cost of "one 5-field JSON log record" at an enabled level. | BenchmarkInfoFields 测量启用级别下「一条 5 字段 JSON 日志」的端到端开销。
func BenchmarkInfoFields(b *testing.B) {
	l := New(Options{Writer: io.Discard, Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Info("request handled",
			Str("method", "GET"),
			Str("path", "/api/v1/users"),
			Int("status", 200),
			Str("request_id", "7f3c1a9e"),
			Dur("took", 1500*time.Microsecond),
		)
	}
}

// BenchmarkConsoleFields measures the encoding cost of the console format. | BenchmarkConsoleFields 测量 console 格式的编码开销。
func BenchmarkConsoleFields(b *testing.B) {
	l := New(Options{Writer: io.Discard, Format: "console", Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Info("request handled",
			Str("method", "GET"),
			Str("path", "/api/v1/users"),
			Int("status", 200),
			Dur("took", 1500*time.Microsecond),
		)
	}
}

// BenchmarkSlowQuery measures the cost of the slow-query instrumentation path (WarnLevel). | BenchmarkSlowQuery 测量慢查询打点路径（WarnLevel）的开销。
func BenchmarkSlowQuery(b *testing.B) {
	l := New(Options{Writer: io.Discard, Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SlowQuery(l, "SELECT id, name FROM users WHERE id = ?", 1, 12*time.Millisecond, nil)
	}
}

// BenchmarkDisabledLevel measures the cost of a call below the threshold and asserts zero allocations (regression guard). | BenchmarkDisabledLevel 测量低于阈值的调用开销，并断言零分配（回归护栏）。
func BenchmarkDisabledLevel(b *testing.B) {
	l := New(Options{Writer: io.Discard, Level: ErrorLevel})
	allocs := testing.AllocsPerRun(1000, func() {
		l.Info("request handled", Str("method", "GET"), Int("status", 200))
	})
	if allocs != 0 {
		b.Fatalf("禁用级别路径分配数 = %v，期望 0", allocs)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Info("request handled", Str("method", "GET"), Int("status", 200))
	}
}
