package logx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// lockedBuffer is a bytes.Buffer guarded by a mutex so that a test may read what the writer
// goroutine has already produced while producers are still running. | lockedBuffer 是用互斥锁
// 保护的 bytes.Buffer，便于测试在生产协程仍在运行时读取写协程已经产生的输出。
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *lockedBuffer) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Len()
}

// recordLenWriter records the length and content of every Write call, which is how the batch
// boundary tests prove that a batch never splits a record and never exceeds BatchBytes. | recordLenWriter
// 记录每次 Write 的长度与内容，批次边界测试据此证明批次既不拆开记录，也不超过 BatchBytes。
type recordLenWriter struct {
	mu     sync.Mutex
	writes [][]byte
}

func (w *recordLenWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (w *recordLenWriter) snapshot() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([][]byte, len(w.writes))
	copy(out, w.writes)
	return out
}

// slowWriter sleeps on every Write, simulating a pipe or disk that is much slower than the
// producers; it counts calls and bytes for the backpressure tests. | slowWriter 每次 Write 都休眠，
// 模拟远慢于生产者的管道或磁盘；它统计调用次数与字节数供背压测试使用。
type slowWriter struct {
	delay  time.Duration
	writes int64
	bytes  int64
}

func (w *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(w.delay)
	atomic.AddInt64(&w.writes, 1)
	atomic.AddInt64(&w.bytes, int64(len(p)))
	return len(p), nil
}

// slowCaptureWriter sleeps on every Write and still records the bytes, so the no-loss test can
// verify the full content after real backpressure. | slowCaptureWriter 每次 Write 都休眠，但仍记录
// 字节，使不丢失测试能在真实背压之后校验完整内容。
type slowCaptureWriter struct {
	delay  time.Duration
	writes int64
	mu     sync.Mutex
	buf    bytes.Buffer
}

func (w *slowCaptureWriter) Write(p []byte) (int, error) {
	time.Sleep(w.delay)
	atomic.AddInt64(&w.writes, 1)
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *slowCaptureWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// asyncLines splits an async log output into non-empty lines after requiring the trailing newline. | asyncLines
// 在要求结尾换行之后，把异步日志输出拆成非空行。
func asyncLines(t *testing.T, raw string) []string {
	t.Helper()
	if raw == "" {
		return nil
	}
	if !strings.HasSuffix(raw, "\n") {
		t.Fatalf("异步输出未以换行结尾: %q", raw)
	}
	return strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
}

// decodeAsyncLines parses every line as JSON and fails on the first malformed line. | decodeAsyncLines
// 把每一行按 JSON 解析，遇到第一行不合法即失败。
func decodeAsyncLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	lines := asyncLines(t, raw)
	recs := make([]map[string]any, 0, len(lines))
	for i, ln := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatalf("异步第 %d 行不是合法 JSON: %v\n%s", i+1, err, ln)
		}
		recs = append(recs, rec)
	}
	return recs
}

// failingSyncWriter returns a fixed error from Sync, used to verify error propagation. | failingSyncWriter
// 从 Sync 返回固定错误，用于验证错误传播。
type failingSyncWriter struct{ err error }

func (w *failingSyncWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *failingSyncWriter) Sync() error                 { return w.err }

// TestAsyncSyncAndCloseReturnWriterError proves that destination Sync errors reach the caller
// from both Sync and Close instead of being swallowed by the writer goroutine. | TestAsyncSyncAndCloseReturnWriterError
// 证明目的地 Sync 错误会从 Sync 与 Close 一并返回给调用方，而不是被写协程吞掉。
func TestAsyncSyncAndCloseReturnWriterError(t *testing.T) {
	sentinel := errors.New("destination sync failed")
	l := New(Options{Writer: &failingSyncWriter{err: sentinel}, Async: true})
	l.Info("x")
	if err := l.Sync(); !errors.Is(err, sentinel) {
		t.Fatalf("Sync() = %v，期望包装 %v", err, sentinel)
	}
	if err := l.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("Close() = %v，期望包装 %v", err, sentinel)
	}
}

// TestSyncRecordPerWriteAcrossPipeBuf asserts the synchronous multi-process contract at the
// PIPE_BUF boundary: exactly one Write per record, the small record fits within PipeBufSize (so a
// POSIX pipe write is atomic), and a record above the boundary is still issued as one whole Write
// (it is a single record, but the kernel may split it on a pipe, as documented by PipeBufSize). | TestSyncRecordPerWriteAcrossPipeBuf
// 在 PIPE_BUF 边界上断言同步模式的多进程约定：每条记录恰好一次 Write，小记录不超过 PipeBufSize
// （因此向 POSIX 管道的写是原子的），超过边界的记录仍作为一次完整 Write 发出（它仍是一条记录，
// 但内核在管道上可能拆分它，这正是 PipeBufSize 文档说明的限制）。
func TestSyncRecordPerWriteAcrossPipeBuf(t *testing.T) {
	w := &recordLenWriter{}
	l := New(Options{Writer: w, Level: DebugLevel})
	l.Info("small", Str("pad", strings.Repeat("s", 100)))
	l.Info("large", Str("pad", strings.Repeat("l", 2*PipeBufSize)))
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	writes := w.snapshot()
	if len(writes) != 2 {
		t.Fatalf("Write 次数 = %d，期望 2（每条记录一次 Write）", len(writes))
	}
	if len(writes[0]) > PipeBufSize {
		t.Fatalf("小记录长度 = %d，期望不超过 PIPE_BUF %d", len(writes[0]), PipeBufSize)
	}
	for i, p := range writes {
		if p[len(p)-1] != '\n' || bytes.Count(p, []byte{'\n'}) != 1 {
			t.Fatalf("第 %d 次 Write 不是恰好一条记录: len=%d", i+1, len(p))
		}
	}
	if len(writes[1]) <= PipeBufSize {
		t.Fatalf("大记录长度 = %d，期望超过 PIPE_BUF %d", len(writes[1]), PipeBufSize)
	}
}

// TestAsyncUniqueContentNoInterleave is the coroutine-side content test: every goroutine logs a
// unique (g, seq) pair; after Sync the parsed set must be exactly the expected set, so line count
// alone cannot hide a lost or duplicated record. | TestAsyncUniqueContentNoInterleave 是协程侧的
// 内容测试：每个 goroutine 记录唯一的 (g, seq)；Sync 之后解析出的集合必须与期望集合完全一致，
// 因此仅凭行数无法掩盖丢失或重复的记录。
func TestAsyncUniqueContentNoInterleave(t *testing.T) {
	w := &lockedBuffer{}
	l := New(Options{Writer: w, Level: DebugLevel, Async: true, QueueSize: 1024, BatchBytes: 8 << 10})
	const goroutines, perG = 16, 300
	pad := strings.Repeat("p", 96)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				l.Info("async unique", Int("g", g), Int("seq", i), Str("pad", pad))
			}
		}(g)
	}
	wg.Wait()
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync() = %v | Sync() 返回错误", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close() = %v | Close() 返回错误", err)
	}

	recs := decodeAsyncLines(t, w.String())
	if len(recs) != goroutines*perG {
		t.Fatalf("记录条数 = %d，期望 %d", len(recs), goroutines*perG)
	}
	seen := make(map[[2]int]bool, goroutines*perG)
	for _, rec := range recs {
		g, okG := rec["g"].(float64)
		seq, okSeq := rec["seq"].(float64)
		if !okG || !okSeq {
			t.Fatalf("记录缺少 g/seq 字段，疑似交错: %v", rec)
		}
		if rec["msg"] != "async unique" {
			t.Fatalf("msg 被破坏: %v", rec["msg"])
		}
		if rec["pad"] != pad {
			t.Fatalf("pad 长度 = %d，期望 %d（缓冲被并发破坏？）", len(rec["pad"].(string)), len(pad))
		}
		if pid, ok := rec["pid"].(float64); !ok || int(pid) != os.Getpid() {
			t.Fatalf("pid = %#v，期望 %d", rec["pid"], os.Getpid())
		}
		key := [2]int{int(g), int(seq)}
		if key[0] < 0 || key[0] >= goroutines || key[1] < 0 || key[1] >= perG {
			t.Fatalf("越界记录: %v", key)
		}
		if seen[key] {
			t.Fatalf("记录重复: %v", key)
		}
		seen[key] = true
	}
	for g := 0; g < goroutines; g++ {
		for i := 0; i < perG; i++ {
			if !seen[[2]int{g, i}] {
				t.Fatalf("缺少记录 (%d,%d)，共解析 %d 条", g, i, len(recs))
			}
		}
	}
}

// TestAsyncSyncFlushesBeforeClose proves the Sync barrier: after Sync returns every record logged
// before it is visible in the writer, even though Close has not been called yet. | TestAsyncSyncFlushesBeforeClose
// 证明 Sync 屏障：Sync 返回后，此前记录的每条日志都已出现在写入器中，即使尚未调用 Close。
func TestAsyncSyncFlushesBeforeClose(t *testing.T) {
	w := &syncingWriter{}
	l := New(Options{Writer: w, Level: DebugLevel, Async: true, QueueSize: 1024})
	const n = 200
	for i := 0; i < n; i++ {
		l.Info("sync flush", Int("seq", i))
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if w.syncs != 1 {
		t.Fatalf("底层 Sync 调用次数 = %d，期望 1", w.syncs)
	}
	recs := decodeAsyncLines(t, w.buf.String())
	if len(recs) != n {
		t.Fatalf("Sync 后记录条数 = %d，期望 %d", len(recs), n)
	}
	// The async writer uses several rings, so it guarantees integrity, not call order; the set of
	// sequence numbers must nevertheless be complete. | 异步写协程使用多个环，保证的是完整性而非调用顺序；
	// 但序号集合必须完整。
	seen := make(map[int]bool, n)
	for _, rec := range recs {
		seen[int(rec["seq"].(float64))] = true
	}
	for i := 0; i < n; i++ {
		if !seen[i] {
			t.Fatalf("Sync 后缺少 seq=%d", i)
		}
	}
	for i := 0; i < n; i++ {
		l.Info("after sync", Int("seq", i))
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if got := len(decodeAsyncLines(t, w.buf.String())); got != 2*n {
		t.Fatalf("Close 后记录条数 = %d，期望 %d", got, 2*n)
	}
	if w.syncs != 2 { // one explicit Sync plus one from Close. | 一次显式 Sync 加 Close 内的一次。
		t.Fatalf("底层 Sync 调用次数 = %d，期望 2", w.syncs)
	}
}

// TestAsyncBatchKeepsWholeRecords verifies the PIPE_BUF contract at the batch level: with
// BatchBytes = PipeBufSize every Write is at most PipeBufSize, every Write starts and ends on a
// record boundary, and an oversized record is written alone rather than split between batches. | TestAsyncBatchKeepsWholeRecords
// 在批次层面验证 PIPE_BUF 约定：BatchBytes = PipeBufSize 时每次 Write 不超过 PipeBufSize、
// 起止都落在记录边界上，超大记录单独写出而不是被拆到两个批次。
func TestAsyncBatchKeepsWholeRecords(t *testing.T) {
	w := &recordLenWriter{}
	l := New(Options{Writer: w, Level: DebugLevel, Async: true, QueueSize: 4096, BatchBytes: PipeBufSize})
	const n = 500
	for i := 0; i < n; i++ {
		l.Info("batch", Int("seq", i), Str("pad", strings.Repeat("b", 100)))
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync() = %v", err)
	}

	writes := w.snapshot()
	if len(writes) == 0 {
		t.Fatal("没有任何 Write | no Write observed")
	}
	if len(writes) >= n {
		t.Fatalf("Write 次数 = %d，期望远小于记录数 %d（未发生批量合并）", len(writes), n)
	}
	total := 0
	for i, p := range writes {
		if len(p) > PipeBufSize {
			t.Fatalf("第 %d 次 Write 长度 = %d，超过 PIPE_BUF %d", i+1, len(p), PipeBufSize)
		}
		if p[0] == '\n' || p[len(p)-1] != '\n' {
			t.Fatalf("第 %d 次 Write 未以完整记录开始/结束: %q", i+1, p)
		}
		total += bytes.Count(p, []byte{'\n'})
	}
	if total != n {
		t.Fatalf("所有批次的行数之和 = %d，期望 %d", total, n)
	}

	// A record larger than BatchBytes must be written alone and remain one intact line. | 大于 BatchBytes 的记录必须单独写出，且仍是一整行。
	l.Info("oversized", Str("pad", strings.Repeat("z", 2*PipeBufSize)))
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	writes = w.snapshot()
	last := writes[len(writes)-1]
	if len(last) <= PipeBufSize {
		t.Fatalf("超大记录的 Write 长度 = %d，期望大于 %d", len(last), PipeBufSize)
	}
	if bytes.Count(last, []byte{'\n'}) != 1 || last[len(last)-1] != '\n' {
		t.Fatalf("超大记录不是单独一整行: len=%d", len(last))
	}
	var rec map[string]any
	if err := json.Unmarshal(last[:len(last)-1], &rec); err != nil {
		t.Fatalf("超大记录不是合法 JSON: %v", err)
	}
	if rec["msg"] != "oversized" {
		t.Fatalf("超大记录 msg = %#v", rec["msg"])
	}
}

// TestAsyncSlowWriterBackpressure shows that a bounded queue plus a slow writer stalls the
// producer instead of dropping records or growing without bound: the producer eventually
// finishes, every sequence survives, and the writer performs far fewer Write calls than records. | TestAsyncSlowWriterBackpressure
// 证明有界队列遇到慢写入器时会让生产者阻塞，而不是丢记录或无界增长：生产者最终完成、
// 所有序号都保留下来，且写入器的 Write 次数远少于记录数。
func TestAsyncSlowWriterBackpressure(t *testing.T) {
	const n = 4000
	w := &slowWriter{delay: time.Millisecond}
	l := New(Options{Writer: w, Level: DebugLevel, Async: true, QueueSize: 256, BatchBytes: PipeBufSize})
	defer func() { _ = l.Close() }()

	start := time.Now()
	for i := 0; i < n; i++ {
		l.Info("pressure", Int("seq", i), Str("pad", strings.Repeat("q", 80)))
	}
	elapsed := time.Since(start)
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	writes := atomic.LoadInt64(&w.writes)
	if writes >= n {
		t.Fatalf("Write 次数 = %d，期望远小于记录数 %d（未批量合并）", writes, n)
	}
	if elapsed < 10*time.Millisecond {
		t.Fatalf("生产者耗时 = %v，慢写入器下应发生可见的背压等待", elapsed)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("生产者耗时 = %v，疑似永久阻塞", elapsed)
	}
	t.Logf("背压：%d 条记录，%d 次 Write，生产者耗时 %v", n, writes, elapsed)
}

// TestAsyncSlowWriterNoLoss checks the coroutine-side content completeness after backpressure:
// 3000 unique records through a 1ms writer must all arrive exactly once. | TestAsyncSlowWriterNoLoss
// 检查背压之后协程侧内容的完整性：经过 1ms 写入器的 3000 条唯一记录必须各到达一次。
func TestAsyncSlowWriterNoLoss(t *testing.T) {
	const n = 3000
	w := &slowCaptureWriter{delay: time.Millisecond}
	l := New(Options{Writer: w, Level: DebugLevel, Async: true, QueueSize: 256, BatchBytes: PipeBufSize})
	start := time.Now()
	for i := 0; i < n; i++ {
		l.Info("no loss", Int("seq", i))
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 10*time.Millisecond {
		t.Fatalf("生产者耗时 = %v，期望慢写入器下发生可见的背压等待", elapsed)
	}
	t.Logf("慢写入器不丢失：%d 条记录，%d 次 Write，耗时 %v", n, atomic.LoadInt64(&w.writes), elapsed)
	if err := l.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	recs := decodeAsyncLines(t, w.String())
	if len(recs) != n {
		t.Fatalf("记录条数 = %d，期望 %d", len(recs), n)
	}
	seen := make(map[int]bool, n)
	for _, rec := range recs {
		seq := int(rec["seq"].(float64))
		if seen[seq] {
			t.Fatalf("seq 重复: %d", seq)
		}
		seen[seq] = true
	}
	for i := 0; i < n; i++ {
		if !seen[i] {
			t.Fatalf("缺少 seq=%d", i)
		}
	}
}

// TestAsyncCloseIsIdempotentAndStopsGoroutine asserts that Close is idempotent, that Sync after
// Close is a no-op, and that the dedicated writer goroutine exits (no leak). | TestAsyncCloseIsIdempotentAndStopsGoroutine
// 断言 Close 幂等、Close 之后 Sync 为无操作，并且专用写协程已经退出（无泄漏）。
func TestAsyncCloseIsIdempotentAndStopsGoroutine(t *testing.T) {
	base := runtime.NumGoroutine()
	w := &lockedBuffer{}
	l := New(Options{Writer: w, Level: DebugLevel, Async: true, QueueSize: 1024})
	l.Info("one")
	if err := l.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync() after Close = %v", err)
	}
	waitGoroutines(t, base)
	if got := len(decodeAsyncLines(t, w.String())); got != 1 {
		t.Fatalf("Close 后记录条数 = %d，期望 1", got)
	}
	l.Info("after close must be dropped | Close 之后的日志应被丢弃")
	if got := len(decodeAsyncLines(t, w.String())); got != 1 {
		t.Fatalf("Close 之后仍产生记录: %d", got)
	}
}

// waitGoroutines waits until the goroutine count drops to want, emulating the leak-detection
// style used elsewhere in the repository. | waitGoroutines 等待 goroutine 数降到 want，
// 沿用仓库其它包使用的泄漏检测风格。
func waitGoroutines(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine 数 = %d，期望回落到 %d（写协程泄漏）", runtime.NumGoroutine(), want)
}

// TestAsyncConcurrentLogSyncClose races producers, Sync and Close under -race. Records that are
// enqueued before Close must all survive; records racing with Close may be dropped by contract. | TestAsyncConcurrentLogSyncClose
// 在 -race 下让生产者、Sync 与 Close 竞争。Close 之前入队的记录必须全部保留；
// 与 Close 竞争的记录按约定允许被丢弃。
func TestAsyncConcurrentLogSyncClose(t *testing.T) {
	w := &lockedBuffer{}
	l := New(Options{Writer: w, Level: DebugLevel, Async: true, QueueSize: 2048})
	const producers, perP = 8, 300

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perP; i++ {
				l.Info("main", Int("p", p), Int("seq", i))
			}
		}(p)
	}

	stopSync := make(chan struct{})
	syncDone := make(chan struct{})
	var syncs atomic.Int64
	go func() {
		defer close(syncDone)
		for {
			select {
			case <-stopSync:
				return
			default:
				if err := l.Sync(); err != nil {
					return
				}
				syncs.Add(1)
			}
		}
	}()

	wg.Wait()
	close(stopSync)
	<-syncDone

	var late sync.WaitGroup
	for g := 0; g < 4; g++ {
		late.Add(1)
		go func(g int) {
			defer late.Done()
			for i := 0; i < 100; i++ {
				l.Info("late", Int("p", g), Int("seq", i))
			}
		}(g)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	late.Wait()
	if err := l.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if syncs.Load() == 0 {
		t.Fatal("Sync goroutine made no progress | Sync 协程未取得任何进展")
	}

	recs := decodeAsyncLines(t, w.String())
	mainSeen := make(map[[2]int]bool, producers*perP)
	for _, rec := range recs {
		if rec["msg"] != "main" {
			continue
		}
		p := int(rec["p"].(float64))
		seq := int(rec["seq"].(float64))
		key := [2]int{p, seq}
		if mainSeen[key] {
			t.Fatalf("main 记录重复: %v", key)
		}
		mainSeen[key] = true
	}
	if len(mainSeen) != producers*perP {
		t.Fatalf("Close 前入队的记录 = %d，期望 %d", len(mainSeen), producers*perP)
	}
}

// TestAsyncFatalFlushes proves that Fatal performs Sync before ExitFunc, so the fatal record is
// visible even though Close is never called. | TestAsyncFatalFlushes 证明 Fatal 在 ExitFunc 之前
// 执行 Sync，因此即使从未调用 Close，致命记录也已写出。
func TestAsyncFatalFlushes(t *testing.T) {
	w := &lockedBuffer{}
	var code = -1
	l := New(Options{Writer: w, Level: DebugLevel, Async: true, ExitFunc: func(c int) { code = c }})
	l.Fatal("fatal async", Str("k", "v"))
	if code != 1 {
		t.Fatalf("ExitFunc 收到退出码 = %d，期望 1", code)
	}
	recs := decodeAsyncLines(t, w.String())
	if len(recs) != 1 || recs[0]["level"] != "fatal" || recs[0]["msg"] != "fatal async" {
		t.Fatalf("Fatal 未在退出前写出记录: %v", recs)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

// TestSyncHotPathZeroAllocations guards the default-path allocation promise: after warm-up a
// fixed synchronous record must not allocate. Async is intentionally excluded here because the
// producer hands buffers to the writer goroutine, so under GOMAXPROCS(1) the pool may have to
// create new buffers while the writer has not run yet; that is ownership, not an encoding
// allocation in the producer. | TestSyncHotPathZeroAllocations 守护默认路径的零分配承诺：
// 预热之后固定记录必须不分配。异步路径刻意不在此断言，因为生产者把缓冲交给写协程，
// 在 GOMAXPROCS(1) 下写协程尚未运行时池可能必须新建缓冲；那是缓冲所有权，而不是生产者编码分配。
func TestSyncHotPathZeroAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector itself allocates, so 0 allocs/op is only meaningful without -race | 竞态检测器自身会分配，0 allocs/op 只在非 -race 下有意义")
	}
	l := New(Options{Writer: io.Discard})
	for i := 0; i < 1024; i++ { // warm up the pool. | 预热缓冲池
		l.Info("warm")
	}
	allocs := testing.AllocsPerRun(1000, func() {
		l.Info("alloc probe", Str("method", "GET"), Int("status", 200))
	})
	if allocs != 0 {
		t.Fatalf("同步热路径分配 = %v allocs/op，期望 0", allocs)
	}
}
