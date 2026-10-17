package logx

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

// The benchmarks in this file complement bench_test.go with the scenarios that matter for the
// zero-copy promise: the no-field hot path, wide records, an apples-to-apples JSON/console pair,
// sustained concurrent writers and the split between encoding cost and I/O cost.
// | 本文件的基准补齐 bench_test.go 未覆盖、但对零拷贝承诺最关键的场景：无字段热路径、宽记录、
// 同字段数的 JSON/console 对照、并发持续写入，以及「编码开销」与「I/O 开销」的拆分。

// benchFieldValues are the fixed values used by the wide-record benchmarks so that every
// encoder comparison sees exactly the same input.
// | benchFieldValues 是宽记录基准使用的固定值，保证每个编码器对照看到的输入完全一致。
var (
	benchStrValue  = "GET"
	benchPathValue = "/api/v1/users/42"
	benchTimeValue = time.Date(2026, time.January, 2, 3, 4, 5, 678000000, time.UTC)
)

// BenchmarkInfoNoFields measures the absolute hot path: one message, zero fields, JSON format.
// This is the cheapest enabled log line and therefore the floor every other scenario is compared against.
// | BenchmarkInfoNoFields 测量绝对热路径：一条消息、零字段、JSON 格式。
// 这是启用状态下最便宜的一条日志，也是其它场景对照的下限。
func BenchmarkInfoNoFields(b *testing.B) {
	l := New(Options{Writer: io.Discard, Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Info("request handled")
	}
}

// BenchmarkInfo8Fields measures a wide record (8 mixed-type fields) in JSON format.
// Wide records put the per-field append path, numeric formatting and string escaping under load,
// and they still fit in the 1 KiB pooled buffer, so steady state should stay allocation-free.
// | BenchmarkInfo8Fields 测量 JSON 格式下的宽记录（8 个混合类型字段）。
// 宽记录会放大逐字段 append、数值格式化与字符串转义的开销，且仍落在 1 KiB 池化缓冲内，
// 因此稳态应保持零分配。
func BenchmarkInfo8Fields(b *testing.B) {
	l := New(Options{Writer: io.Discard, Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Info("request handled",
			Str("method", benchStrValue),
			Str("path", benchPathValue),
			Int("status", 200),
			Int64("bytes", 4096),
			Bool("cached", true),
			Float64("latency_ms", 12.5),
			Time("at", benchTimeValue),
			Dur("took", 1500*time.Microsecond),
		)
	}
}

// BenchmarkConsole8Fields is the console-format counterpart of BenchmarkInfo8Fields: same message,
// same 8 fields, same writer, so the two numbers isolate the encoder cost difference.
// | BenchmarkConsole8Fields 是 BenchmarkInfo8Fields 的 console 格式对照：同样的消息、同样的 8 个字段、
// 同样的写入器，两个数字之差即两种编码器的成本差。
func BenchmarkConsole8Fields(b *testing.B) {
	l := New(Options{Writer: io.Discard, Format: "console", Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Info("request handled",
			Str("method", benchStrValue),
			Str("path", benchPathValue),
			Int("status", 200),
			Int64("bytes", 4096),
			Bool("cached", true),
			Float64("latency_ms", 12.5),
			Time("at", benchTimeValue),
			Dur("took", 1500*time.Microsecond),
		)
	}
}

// BenchmarkInfoFieldsParallel measures sustained concurrent writers with b.RunParallel.
// Zero-copy logging must not degrade under concurrency: the mutex serializes only the single
// Write call, while encoding runs in parallel into per-goroutine pooled buffers. ns/op here is
// wall-clock time per operation summed over all goroutines, so a large jump over the serial
// benchmark would expose lock contention or pool contention.
// | BenchmarkInfoFieldsParallel 用 b.RunParallel 测量持续并发写入。
// 零拷贝日志不得在并发下退化：互斥锁只串行化单次 Write，编码在各自 goroutine 的池化缓冲中并行完成。
// 这里的 ns/op 是跨所有 goroutine 的墙钟时间均摊，若明显高于串行基准，说明存在锁竞争或缓冲池竞争。
func BenchmarkInfoFieldsParallel(b *testing.B) {
	l := New(Options{Writer: io.Discard, Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Info("request handled",
				Str("method", "GET"),
				Str("path", "/api/v1/users"),
				Int("status", 200),
				Str("request_id", "7f3c1a9e"),
				Dur("took", 1500*time.Microsecond),
			)
		}
	})
}

// BenchmarkConsoleFieldsParallel is the console-format counterpart of BenchmarkInfoFieldsParallel,
// checking that the human-readable encoder scales the same way under concurrency.
// | BenchmarkConsoleFieldsParallel 是 BenchmarkInfoFieldsParallel 的 console 格式对照，
// 用于确认人类可读编码器在并发下同样线性扩展。
func BenchmarkConsoleFieldsParallel(b *testing.B) {
	l := New(Options{Writer: io.Discard, Format: "console", Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Info("request handled",
				Str("method", "GET"),
				Str("path", "/api/v1/users"),
				Int("status", 200),
				Str("request_id", "7f3c1a9e"),
				Dur("took", 1500*time.Microsecond),
			)
		}
	})
}

// BenchmarkWriterDiscard measures encoding plus the minimal io.Discard Write and is the reference
// point for the writer comparison below.
// | BenchmarkWriterDiscard 测量编码加上最廉价的 io.Discard 写入，是下面写入器对照的参照点。
func BenchmarkWriterDiscard(b *testing.B) {
	l := New(Options{Writer: io.Discard, Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchLog5(l)
	}
}

// BenchmarkWriterBuffer measures the same record written into a reused bytes.Buffer.
// The buffer is reset only after 64 KiB of output, so the reset branch is amortized to nothing;
// the number shows what a minimal in-memory consumer costs on top of encoding.
// | BenchmarkWriterBuffer 测量同一条记录写入复用的 bytes.Buffer。
// 缓冲仅在累计 64 KiB 后重置，重置分支成本被摊薄到可忽略；该数字表示「编码 + 最小内存消费者」的成本。
func BenchmarkWriterBuffer(b *testing.B) {
	buf := &bytes.Buffer{}
	buf.Grow(64 << 10)
	l := New(Options{Writer: buf, Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if buf.Len() >= 64<<10 {
			buf.Reset()
		}
		benchLog5(l)
	}
}

// BenchmarkWriterFile measures the same record written to an unbuffered *os.File.
// Every record is therefore one Write syscall, which is the "real I/O" side of the comparison
// and isolates syscall cost from encoding cost.
// | BenchmarkWriterFile 测量同一条记录写入无缓冲的 *os.File。
// 每条记录因此对应一次 Write 系统调用，这就是对照中的「真实 I/O」一侧，用于把系统调用成本从编码成本中分离出来。
func BenchmarkWriterFile(b *testing.B) {
	f, err := os.CreateTemp(b.TempDir(), "logx-bench-*.log")
	if err != nil {
		b.Fatalf("create temp log file | 创建临时日志文件失败: %v", err)
	}
	b.Cleanup(func() { _ = f.Close() })
	l := New(Options{Writer: f, Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchLog5(l)
	}
}

// BenchmarkWriterBufferedFile measures the same record through a bufio.Writer over a real file.
// Compared with BenchmarkWriterFile it shows how much of the file cost is per-record syscall
// overhead rather than encoding; compared with BenchmarkWriterDiscard it approaches the encoding floor.
// | BenchmarkWriterBufferedFile 测量同一条记录经 bufio.Writer 写入真实文件。
// 与 BenchmarkWriterFile 对比可看出文件成本中有多少是逐记录系统调用；与 BenchmarkWriterDiscard 对比则逼近编码下限。
func BenchmarkWriterBufferedFile(b *testing.B) {
	f, err := os.CreateTemp(b.TempDir(), "logx-bench-*.log")
	if err != nil {
		b.Fatalf("create temp log file | 创建临时日志文件失败: %v", err)
	}
	bw := bufio.NewWriterSize(f, 64<<10)
	b.Cleanup(func() {
		_ = bw.Flush()
		_ = f.Close()
	})
	l := New(Options{Writer: bw, Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchLog5(l)
	}
}

// benchLog5 emits the fixed five-field record shared by the writer benchmarks.
// Inlining the field constructors keeps the same stack-allocated variadic slice shape as
// the hand-written benchmarks, so the writer comparison is not polluted by a helper's slice.
// | benchLog5 输出写入器基准共用的固定五字段记录。
// 字段构造写在函数内，保持与手写基准相同的「可变参数切片留在栈上」形态，避免辅助函数自身的切片污染写入器对照。
func benchLog5(l *Logger) {
	l.Info("request handled",
		Str("method", "GET"),
		Str("path", "/api/v1/users"),
		Int("status", 200),
		Str("request_id", "7f3c1a9e"),
		Dur("took", 1500*time.Microsecond),
	)
}

// BenchmarkInfoFieldsParallelConcurrent measures the opt-in lock-free write mode with a writer
// that serializes concurrent Write itself (io.Discard here; *os.File in production). | BenchmarkInfoFieldsParallelConcurrent
// 测量可选的免锁写入模式，写入器为自身串行化并发写的那种（此处 io.Discard；生产环境为 *os.File）。
func BenchmarkInfoFieldsParallelConcurrent(b *testing.B) {
	l := New(Options{Writer: io.Discard, Level: InfoLevel, ConcurrentWrites: true})
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			benchLog5(l)
		}
	})
}

// BenchmarkInfoFieldsConcurrent is the single-goroutine counterpart of the lock-free mode. | BenchmarkInfoFieldsConcurrent
// 是免锁模式的单协程对照。
func BenchmarkInfoFieldsConcurrent(b *testing.B) {
	l := New(Options{Writer: io.Discard, Level: InfoLevel, ConcurrentWrites: true})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchLog5(l)
	}
}
