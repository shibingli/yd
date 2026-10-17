package middleware

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shibingli/yd/core/logx"
)

// benchSlowWriter simulates a blocking log destination such as a disk or a remote collector.
// | benchSlowWriter 模拟磁盘或远端采集器这类阻塞式日志目的地。
type benchSlowWriter struct{}

// Write sleeps for 100µs before accepting the record. | Write 在接受记录前休眠 100µs。
func (benchSlowWriter) Write(p []byte) (int, error) {
	time.Sleep(100 * time.Microsecond)
	return len(p), nil
}

// benchSlowLogger returns a synchronous Info-level logger whose writes cost 100µs.
// | benchSlowLogger 返回每次写入耗时 100µs 的同步 Info 级日志器。
func benchSlowLogger() *logx.Logger {
	return logx.New(logx.Options{Writer: benchSlowWriter{}, Level: logx.InfoLevel})
}

// BenchmarkAccessLogSync is the request-path reference for the synchronous AccessLog middleware:
// writer wrapping, status/byte counting and one full JSON record encoded into io.Discard.
// | BenchmarkAccessLogSync 是同步 AccessLog 中间件的请求路径参考：包装写入器、统计状态码与字节数，
// 并把一条完整 JSON 记录编码进 io.Discard。
func BenchmarkAccessLogSync(b *testing.B) {
	r := benchRouter(benchSmallBody, "text/plain; charset=utf-8", AccessLog(benchDiscardLogger()))
	req := benchRequest(nil)
	benchServe(b, r, req, benchExpectBody(benchSmallBody))
}

// BenchmarkAccessLogAsync measures the same capture work with the log record produced by a worker.
// The handler is a discard logger, so this isolates the hand-off cost rather than hiding I/O.
// | BenchmarkAccessLogAsync 用 worker 产生日志记录来测量同样的采集工作。处理器写入丢弃器，
// 因此这里隔离的是交接成本，而不是隐藏 I/O。
func BenchmarkAccessLogAsync(b *testing.B) {
	a := NewAsyncAfter(AsyncOptions{
		QueueSize: 4096,
		Overflow:  OverflowDropNew,
		Handler:   AccessLogHandler(benchDiscardLogger()),
	})
	b.Cleanup(func() { _ = a.Close(context.Background()) })

	r := benchRouter(benchSmallBody, "text/plain; charset=utf-8", AccessLogAsync(nil, a))
	req := benchRequest(nil)
	benchServe(b, r, req, benchExpectBody(benchSmallBody))
	b.ReportMetric(float64(a.Dropped()), "drops")
}

// BenchmarkAccessLogSyncSlowHandler puts a 100µs writer on the request path. This is the "before"
// picture for a log destination that is slow (disk, network collector).
// | BenchmarkAccessLogSyncSlowHandler 把 100µs 的写入器放在请求路径上，代表慢日志目的地（磁盘、
// 远端采集器）的「改造前」图景。
func BenchmarkAccessLogSyncSlowHandler(b *testing.B) {
	r := benchRouter(benchSmallBody, "text/plain; charset=utf-8", AccessLog(benchSlowLogger()))
	req := benchRequest(nil)
	benchServe(b, r, req, benchExpectBody(benchSmallBody))
}

// BenchmarkAccessLogAsyncSlowHandler uses the same 100µs writer inside the worker. Under
// OverflowDropNew an overloaded pipeline reports drops instead of blocking the request goroutine,
// which is exactly the trade-off the async axis offers. | BenchmarkAccessLogAsyncSlowHandler 在
// worker 内使用同样的 100µs 写入器。在 OverflowDropNew 下，过载管线上报丢弃而不是阻塞请求协程，
// 这正是异步轴提供的取舍。
func BenchmarkAccessLogAsyncSlowHandler(b *testing.B) {
	a := NewAsyncAfter(AsyncOptions{
		Workers:   8,
		QueueSize: 4096,
		Overflow:  OverflowDropNew,
		Handler:   AccessLogHandler(benchSlowLogger()),
	})
	b.Cleanup(func() { _ = a.Close(context.Background()) })

	r := benchRouter(benchSmallBody, "text/plain; charset=utf-8", AccessLogAsync(nil, a))
	req := benchRequest(nil)
	benchServe(b, r, req, benchExpectBody(benchSmallBody))
	b.ReportMetric(float64(a.Dropped()), "drops")
}

// BenchmarkAccessLogAsyncSubmit isolates the pipeline hand-off itself: string cloning, enqueue and
// overflow handling on the caller goroutine, without any router or response-writer work.
// | BenchmarkAccessLogAsyncSubmit 单独测量管线交接本身：调用方协程上的字符串克隆、入队与溢出处理，
// 不包含任何路由或响应写入器工作。
func BenchmarkAccessLogAsyncSubmit(b *testing.B) {
	var handled atomic.Int64
	a := NewAsyncAfter(AsyncOptions{
		QueueSize: 4096,
		Overflow:  OverflowDropNew,
		Handler:   func(Record) { handled.Add(1) },
	})
	b.Cleanup(func() { _ = a.Close(context.Background()) })

	rec := Record{
		ID:       "b7f1c2d3-0000-4000-8000-0123456789ab",
		Method:   "GET",
		Path:     "/bench",
		RemoteIP: "192.0.2.1",
		UA:       "yd-bench/1.0",
		Status:   200,
		Bytes:    int64(len(benchSmallBody)),
		Duration: 42 * time.Microsecond,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Submit(rec)
	}
	b.StopTimer()
	_ = a.Close(context.Background())
	b.ReportMetric(float64(a.Dropped()), "drops")
	b.ReportMetric(float64(handled.Load()), "handled")
}

// BenchmarkAccessLogAsyncThroughput compares three queue/worker shapes with a deliberately slow
// 10µs handler and OverflowDropNew. The ns/op column is request-path latency; drops shows how much
// work the configured capacity could not absorb. | BenchmarkAccessLogAsyncThroughput 用故意慢的
// 10µs 处理器与 OverflowDropNew 对比三种队列/worker 形态。ns/op 列是请求路径延迟，drops 表示配置
// 容量无法吸收的工作量。
func BenchmarkAccessLogAsyncThroughput(b *testing.B) {
	cases := []struct {
		name    string
		workers int
		queue   int
	}{
		{name: "workers1_queue16", workers: 1, queue: 16},
		{name: "workers4_queue256", workers: 4, queue: 256},
		{name: "workersN_queue4096", workers: runtime.GOMAXPROCS(0), queue: 4096},
	}
	rec := Record{
		ID:       "b7f1c2d3-0000-4000-8000-0123456789ab",
		Method:   "GET",
		Path:     "/bench",
		RemoteIP: "192.0.2.1",
		UA:       "yd-bench/1.0",
		Status:   200,
		Bytes:    int64(len(benchSmallBody)),
		Duration: 42 * time.Microsecond,
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			var handled atomic.Int64
			a := NewAsyncAfter(AsyncOptions{
				Workers:   tc.workers,
				QueueSize: tc.queue,
				Overflow:  OverflowDropNew,
				Handler: func(Record) {
					handled.Add(1)
					time.Sleep(10 * time.Microsecond)
				},
			})
			b.Cleanup(func() { _ = a.Close(context.Background()) })

			b.ReportAllocs()
			b.ResetTimer()
			start := time.Now()
			for i := 0; i < b.N; i++ {
				a.Submit(rec)
			}
			elapsed := time.Since(start)
			b.StopTimer()
			drops := a.Dropped()
			handledNow := handled.Load()
			_ = a.Close(context.Background())
			b.ReportMetric(float64(handledNow)/elapsed.Seconds(), "handled/s")
			total := float64(drops + uint64(handledNow))
			if total > 0 {
				b.ReportMetric(float64(drops)/total, "drop_ratio")
			}
			b.ReportMetric(float64(drops), "drops")
			b.ReportMetric(float64(handled.Load()), "handled")
		})
	}
}
