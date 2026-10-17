package logx

import (
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// BenchmarkAsyncInfoFieldsSerial measures the opt-in async producer path with a single goroutine
// and the cheapest possible consumer, io.Discard. Compare it with BenchmarkWriterDiscard to see
// the pure enqueue/encode cost without a slow destination. | BenchmarkAsyncInfoFieldsSerial 用单
// goroutine 与最廉价的消费者 io.Discard 测量可选异步生产者路径。与 BenchmarkWriterDiscard 对比
// 可看出没有慢目的地时纯粹的编码/入队成本。
func BenchmarkAsyncInfoFieldsSerial(b *testing.B) {
	l := New(Options{Writer: io.Discard, Level: InfoLevel, Async: true})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchLog5(l)
	}
	b.StopTimer()
	_ = l.Close()
	b.StartTimer()
}

// BenchmarkAsyncInfoFieldsParallel measures async producer scaling with b.RunParallel at the
// -cpu values passed on the command line. It is the direct counterpart of
// BenchmarkInfoFieldsParallel, which measures the default synchronous path. | BenchmarkAsyncInfoFieldsParallel
// 用 b.RunParallel 在命令行给定的 -cpu 值上测量异步生产者的扩展性。它是默认同步路径
// BenchmarkInfoFieldsParallel 的直接对照。
func BenchmarkAsyncInfoFieldsParallel(b *testing.B) {
	l := New(Options{Writer: io.Discard, Level: InfoLevel, Async: true})
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			benchLog5(l)
		}
	})
	b.StopTimer()
	_ = l.Close()
	b.StartTimer()
}

// benchmarkSlowSync is the baseline slow-writer scenario: one Write per record, each Write
// sleeping slowWriterDelay, so the producer is exposed to the full destination latency. | benchmarkSlowSync
// 是慢写入器基线场景：每条记录一次 Write，每次 Write 休眠 slowWriterDelay，生产者直接承受目的地延迟。
func benchmarkSlowSync(b *testing.B) {
	w := &slowWriter{delay: slowWriterDelay}
	l := New(Options{Writer: w, Level: InfoLevel})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchLog5(l)
	}
	b.StopTimer()
	b.ReportMetric(float64(atomic.LoadInt64(&w.writes))/float64(b.N), "writes/op")
}

// benchmarkSlowAsync is the async counterpart of benchmarkSlowSync: batchBytes controls how many
// whole records are coalesced per Write, so it directly shows the PIPE_BUF trade-off between
// syscall count and per-batch size. | benchmarkSlowAsync 是 benchmarkSlowSync 的异步对照：
// batchBytes 控制每次 Write 合并多少完整记录，因此直接体现「系统调用次数 vs 单批大小」的 PIPE_BUF 取舍。
func benchmarkSlowAsync(b *testing.B, batchBytes int) {
	w := &slowWriter{delay: slowWriterDelay}
	l := New(Options{Writer: w, Level: InfoLevel, Async: true, BatchBytes: batchBytes})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchLog5(l)
	}
	b.StopTimer()
	_ = l.Close()
	b.ReportMetric(float64(atomic.LoadInt64(&w.writes))/float64(b.N), "writes/op")
}

// BenchmarkAsyncWriterFile is the async counterpart of BenchmarkWriterFile: the same record, the
// same unbuffered *os.File, but the writer goroutine batches records so the producer pays one
// Write syscall per batch instead of one per record. | BenchmarkAsyncWriterFile 是
// BenchmarkWriterFile 的异步对照：同样的记录、同样的无缓冲 *os.File，但写协程把记录合并成批，
// 生产者支付的是每批一次 Write 系统调用，而不是每条记录一次。
func BenchmarkAsyncWriterFile(b *testing.B) {
	f, err := os.CreateTemp(b.TempDir(), "logx-async-bench-*.log")
	if err != nil {
		b.Fatalf("create temp log file | 创建临时日志文件失败: %v", err)
	}
	l := New(Options{Writer: f, Level: InfoLevel, Async: true})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchLog5(l)
	}
	b.StopTimer()
	_ = l.Close()
	if err := f.Sync(); err != nil {
		b.Fatalf("file Sync | 文件 Sync 失败: %v", err)
	}
	if err := f.Close(); err != nil {
		b.Fatalf("file Close | 文件 Close 失败: %v", err)
	}
	b.StartTimer()
}

// slowWriterDelay is the per-Write latency used by the slow-writer benchmarks. | slowWriterDelay
// 是慢写入器基准使用的单次 Write 延迟。
const slowWriterDelay = 500 * time.Microsecond

// BenchmarkSlowWriterSync measures the default synchronous path against a 500µs writer. | BenchmarkSlowWriterSync
// 测量默认同步路径面对 500µs 写入器时的表现。
func BenchmarkSlowWriterSync(b *testing.B) { benchmarkSlowSync(b) }

// BenchmarkAsyncSlowWriterBatch4K measures async batching with BatchBytes = PipeBufSize, the
// setting recommended when the destination is a pipe. | BenchmarkAsyncSlowWriterBatch4K 测量
// BatchBytes = PipeBufSize 的异步批量写入，这是目的地为管道时的推荐设置。
func BenchmarkAsyncSlowWriterBatch4K(b *testing.B) { benchmarkSlowAsync(b, PipeBufSize) }

// BenchmarkAsyncSlowWriterBatch64K measures async batching with the 64 KiB default batch size. | BenchmarkAsyncSlowWriterBatch64K
// 测量默认 64 KiB 批量大小的异步批量写入。
func BenchmarkAsyncSlowWriterBatch64K(b *testing.B) { benchmarkSlowAsync(b, 64<<10) }
