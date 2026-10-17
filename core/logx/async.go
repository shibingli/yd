package logx

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// The async engine is an opt-in (Options.Async) batched writer built on a set of independent
// bounded MPSC rings. Producers encode into their own pooled buffer and publish the buffer
// pointer with a lock-free reservation; one dedicated writer goroutine drains the rings,
// coalesces whole records into one byte slice and performs one Write per batch. The default
// synchronous path (one Write per record under a mutex) is untouched. | 异步引擎是可选
// （Options.Async）的批量写入器，建立在若干独立的有界 MPSC 环上。生产者在自己取自池的缓冲里
// 完成编码，用无锁预约发布缓冲指针；唯一的写协程排空各个环，把完整记录合并进一个字节切片，
// 每批只调用一次 Write。默认的同步路径（互斥锁内每条记录一次 Write）保持不变。

const (
	// asyncShardMin and asyncShardMax bound the number of independent rings. The actual count
	// follows runtime.GOMAXPROCS, which modern Go makes container-aware on Linux: a many-core host
	// gets one ring per core (up to the cap), while a two-core container does not pay for unused
	// rings. Producers choose a ring with one atomic increment, so no shared counter becomes a
	// bottleneck. | asyncShardMin 与 asyncShardMax 限定独立环的数量。实际数量跟随
	// runtime.GOMAXPROCS——现代 Go 在 Linux 上已使其感知容器配额：多核主机每核一个环（至上限），
	// 双核容器则不会为闲置的环付出代价。生产者用一次原子自增选择环，因此不会出现共享计数器瓶颈。
	asyncShardMin = 4
	asyncShardMax = 64
	// asyncShardMinCap / asyncShardMaxCap bound the per-ring capacity after rounding. | asyncShardMinCap / asyncShardMaxCap 是取整后每个环容量的上下界。
	asyncShardMinCap = 32
	asyncShardMaxCap = 8 << 10
	// asyncQueueSizeDefault is the total record capacity used when Options.QueueSize is zero. | asyncQueueSizeDefault 是 Options.QueueSize 为零时使用的总记录容量。
	asyncQueueSizeDefault = 8 << 10
	// asyncBatchBytesDefault is the batch size used when Options.BatchBytes is zero (64 KiB). | asyncBatchBytesDefault 是 Options.BatchBytes 为零时使用的批量大小（64 KiB）。
	asyncBatchBytesDefault = 64 << 10
	// asyncWaitSleep is the backoff used when a producer finds its ring slot still occupied.
	// It only happens when the writer is slower than the producers (backpressure). | asyncWaitSleep
	// 是生产者发现其环槽位仍被占用时的退避间隔。只有写协程慢于生产者（背压）时才会发生。
	asyncWaitSleep = 50 * time.Microsecond
)

// asyncShardCount returns the ring count for this process: one per P, clamped so that tiny and
// very large machines both stay reasonable. | asyncShardCount 返回本进程的环数量：每个 P 一个，
// 并做上下限夹紧，使极小与超大机器都保持合理。
func asyncShardCount() int {
	n := runtime.GOMAXPROCS(0)
	if n < asyncShardMin {
		return asyncShardMin
	}
	if n > asyncShardMax {
		return asyncShardMax
	}
	return n
}

// cacheLinePad separates hot atomics that would otherwise share a cache line. | cacheLinePad 用于隔开原本会共享缓存行的热点原子量。
type cacheLinePad [64]byte

// asyncEntry is one item in a ring: a data buffer, a Sync marker or a Close marker. | asyncEntry 是环中的一个条目：数据缓冲、Sync 标记或 Close 标记。
type asyncEntry struct {
	buf   *buffer
	sync  *syncRequest
	close *closeRequest
}

// asyncSlot is one ring slot. seq follows the Vyukov MPSC convention: a producer waits until
// seq == position, publishes the entry and stores seq = position+1; the consumer waits for
// position+1 and stores seq = position+capacity. | asyncSlot 是一个环槽位。seq 遵循 Vyukov MPSC
// 约定：生产者等到 seq == 位置，写入条目后存 seq = 位置+1；消费者等到 位置+1，消费后存
// seq = 位置+容量。
type asyncSlot struct {
	entry asyncEntry
	seq   atomic.Uint64
}

// asyncRing is one bounded MPSC ring. head is written only by producers (one fetch-add per
// record) and read by the consumer; tail is touched only by the consumer; padding keeps the
// two atomics off each other's cache line. The ring also owns a buffer pool, so producers and
// the writer exchange buffers through the ring itself instead of contending on one global
// sync.Pool root. | asyncRing 是一个有界 MPSC 环。head 只由生产者写入（每条记录一次 fetch-add）
// 并由消费者读取；tail 只由消费者访问；填充让两个原子量不共享缓存行。环还持有一个缓冲池，
// 生产者与写协程因此经环本身交换缓冲，而不是争抢同一个全局 sync.Pool 根。
type asyncRing struct { //betteralign:ignore
	slots []asyncSlot
	mask  uint64
	pool  sync.Pool
	_     cacheLinePad
	head  atomic.Uint64
	_     cacheLinePad
	tail  atomic.Uint64
	_     cacheLinePad
	// inflight counts producers that passed the closed check but have not published yet; it is
	// sharded per ring for the same reason as the pools. | inflight 统计已通过关闭检查但尚未发布
	// 的生产者；与缓冲池同理，它按环分片。
	inflight atomic.Int64
	_        cacheLinePad
}

// syncRequest is a barrier submitted by Logger.Sync: one marker per ring, and the writer
// acknowledges only after every marker has been seen, the batch has been flushed and the
// underlying Sync (if any) has returned. | syncRequest 是 Logger.Sync 提交的屏障：每个环一个
// 标记，写协程在看到全部标记、冲刷当前批次并调用底层 Sync（若实现）之后才应答。
type syncRequest struct {
	done      chan error
	remaining atomic.Int32
}

// closeRequest is the writer-exit barrier submitted by Logger.Close, with the same
// one-marker-per-ring scheme as syncRequest. | closeRequest 是 Logger.Close 提交的写协程退出屏障，
// 与 syncRequest 一样采用每环一个标记的方案。
type closeRequest struct {
	remaining atomic.Int32
}

// asyncState is the per-Logger-family async writer state shared through core. | asyncState 是同一
// Logger 家族经 core 共享的异步写入状态。
type asyncState struct { //betteralign:ignore
	closeErr error // Written by the writer goroutine before done is closed, read after. | 写协程在关闭 done 之前写入，读取发生在其后
	notify   chan struct{}
	done     chan struct{}
	rings    []asyncRing
	batch    []byte
	batchCap int
	lifeMu   sync.RWMutex
	_        cacheLinePad
	// nextRing spreads producers across rings; it is written on every record, so it owns its line.
	// | nextRing 把生产者分散到各环；它每条记录都会写入，因此独占一行。
	nextRing atomic.Uint64
	_        cacheLinePad
	// notifyPending coalesces wakeups: the channel is signalled once per idle-to-busy transition,
	// not once per record. | notifyPending 合并唤醒：通道只在「空闲转忙碌」时通知一次，而不是每条记录一次。
	notifyPending atomic.Bool
	_             cacheLinePad
	closed        atomic.Bool
	_             cacheLinePad
}

// newAsyncState builds the rings and the batch buffer. Invalid or zero sizes fall back to the
// documented defaults; QueueSize is rounded up to a power of two per ring and clamped. | newAsyncState
// 构建环与批次缓冲。非法或零尺寸回落到文档默认值；QueueSize 按每个环向上取整为 2 的幂并夹紧。
func newAsyncState(queueSize, batchBytes int) *asyncState {
	if queueSize <= 0 {
		queueSize = asyncQueueSizeDefault
	}
	if batchBytes <= 0 {
		batchBytes = asyncBatchBytesDefault
	}
	if batchBytes < 256 {
		batchBytes = 256
	}
	shards := asyncShardCount()
	perRing := (queueSize + shards - 1) / shards
	perRing = nextPowerOfTwo(perRing)
	if perRing < asyncShardMinCap {
		perRing = asyncShardMinCap
	}
	if perRing > asyncShardMaxCap {
		perRing = asyncShardMaxCap
	}
	a := &asyncState{
		rings:    make([]asyncRing, shards),
		notify:   make(chan struct{}, 1),
		done:     make(chan struct{}),
		batchCap: batchBytes,
	}
	initial := batchBytes
	if initial > 64<<10 {
		initial = 64 << 10
	}
	a.batch = make([]byte, 0, initial)
	for i := range a.rings {
		q := &a.rings[i]
		q.pool.New = newPooledBuffer
		q.mask = uint64(perRing - 1)
		q.slots = make([]asyncSlot, perRing)
		for j := range q.slots {
			q.slots[j].seq.Store(uint64(j))
		}
	}
	return a
}

// nextPowerOfTwo returns the smallest power of two greater than or equal to n (n >= 1). | nextPowerOfTwo 返回不小于 n 的最小 2 的幂（n >= 1）。
func nextPowerOfTwo(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// getBuffer takes a buffer from the ring selected for this record. Both the ring choice and the
// buffer pool are spread across GOMAXPROCS rings, so neither the selection counter nor the pool
// root becomes a single contended cache line. The ring index is returned so the writer can put
// the buffer back into the same pool. | getBuffer 从本条记录选中的环取缓冲。环选择与缓冲池都按
// GOMAXPROCS 个环分散，因此选择计数器与池根都不会成为单条争用缓存行。返回环下标，写协程据此
// 把缓冲还回同一个池。
func (a *asyncState) getBuffer() (int, *buffer) {
	i := int(a.nextRing.Add(1)) % len(a.rings)
	b := a.rings[i].pool.Get().(*buffer)
	b.buf = b.buf[:0]
	return i, b
}

// putBuffer returns b to the pool of ring i, dropping buffers beyond the retention cap. | putBuffer
// 把 b 还回环 i 的池；超过保留上限的缓冲直接丢弃。
func (a *asyncState) putBuffer(i int, b *buffer) {
	if cap(b.buf) > maxPooledBufSize {
		return
	}
	b.buf = b.buf[:0]
	a.rings[i].pool.Put(b)
}

// push publishes e at the next position of ring q. If the slot is still occupied by the
// previous lap it backs off instead of spinning hot; the writer frees slots in order, so the
// wait is bounded as long as the writer keeps making progress. | push 在环 q 的下一个位置发布 e。
// 若槽位仍被上一圈占用则退避而不是空转；消费者按顺序释放槽位，因此只要写协程持续推进，
// 等待就是有界的。
func (q *asyncRing) push(e asyncEntry) {
	h := q.head.Add(1) - 1
	s := &q.slots[h&q.mask]
	yields := 0
	for s.seq.Load() != h {
		if yields < 8 {
			yields++
			runtime.Gosched()
			continue
		}
		time.Sleep(asyncWaitSleep)
	}
	s.entry = e
	s.seq.Store(h + 1)
}

// pop removes the oldest entry; it returns false when the ring is empty or the next slot has
// been reserved by a producer that has not published it yet. Only the single writer goroutine
// calls pop. | pop 取出最旧的条目；环为空、或下一个槽位已被生产者预约但尚未发布时返回 false。
// 只有唯一的写协程会调用 pop。
func (q *asyncRing) pop() (asyncEntry, bool) {
	t := q.tail.Load()
	if t == q.head.Load() {
		return asyncEntry{}, false
	}
	s := &q.slots[t&q.mask]
	if s.seq.Load() != t+1 {
		return asyncEntry{}, false
	}
	e := s.entry
	s.entry = asyncEntry{}
	s.seq.Store(t + q.mask + 1)
	q.tail.Store(t + 1)
	return e, true
}

// pushBuffer enqueues one encoded record into ring i. It counts the producer as in-flight so
// Close can wait for every in-progress push before placing its exit markers; the counter is
// per ring, so the accounting never contends on a single cache line. A push that observes the
// closed flag is discarded (logging after Close is documented as a no-op). | pushBuffer 把一条
// 已编码记录入队到环 i。它把生产者计入在途计数，使 Close 能在放置退出标记前等待所有进行中的
// push；计数按环分片，因此记账不会争用同一条缓存行。观察到已关闭标志的 push 被丢弃
// （Close 之后的日志按文档属于无操作）。
func (a *asyncState) pushBuffer(ring int, b *buffer) {
	q := &a.rings[ring]
	q.inflight.Add(1)
	if a.closed.Load() {
		q.inflight.Add(-1)
		a.putBuffer(ring, b)
		return
	}
	q.push(asyncEntry{buf: b})
	q.inflight.Add(-1)
	a.wake()
}

// pushControl enqueues a Sync or Close marker into ring i and wakes the writer. | pushControl 把 Sync 或 Close 标记入队到环 i 并唤醒写协程。
func (a *asyncState) pushControl(i int, e asyncEntry) {
	a.rings[i].push(e)
	a.wake()
}

// wake notifies the writer at most once while a wakeup is still pending. The flag is cleared by
// the writer before it drains, so a producer that publishes before the clear is guaranteed to be
// seen by that drain, and one that publishes after it wins the CAS and signals. | wake 在已有待处理
// 唤醒时至多通知一次。写协程在排空前清除该标志，因此在其之前发布的生产者必定被这次排空看到，
// 而在其后发布的生产者会赢得 CAS 并发出通知。
func (a *asyncState) wake() {
	if a.notifyPending.CompareAndSwap(false, true) {
		a.signal()
	}
}

// signal wakes the writer without ever blocking a producer. When the single-slot channel is
// already full a wakeup is pending, so dropping this one is safe. | signal 唤醒写协程且绝不阻塞
// 生产者。单槽通道已满说明已有待处理唤醒，因此丢弃本次唤醒是安全的。
func (a *asyncState) signal() {
	select {
	case a.notify <- struct{}{}:
	default:
	}
}

// flushBatch writes the accumulated batch with one Write and resets the slice length. The
// mutex is kept for symmetry with the synchronous path; in async mode the writer goroutine is
// the only caller, so it is uncontended. | flushBatch 用一次 Write 写出累积批次并重置切片长度。
// 保留互斥锁是为了与同步路径一致；异步模式下写协程是唯一调用者，因此不存在竞争。
func (c *core) flushBatch(batch []byte) []byte {
	if len(batch) == 0 {
		return batch
	}
	c.mu.Lock()
	_, _ = c.w.Write(batch)
	c.mu.Unlock()
	return batch[:0]
}

// syncWriter flushes the underlying writer when it implements Sync() error. | syncWriter 在底层写入器实现 Sync() error 时刷新它。
func (c *core) syncWriter() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.w.(interface{ Sync() error }); ok {
		return s.Sync()
	}
	return nil
}

// asyncWriteLoop is the dedicated writer goroutine. It drains all rings, coalesces whole
// records into the batch buffer, flushes when the batch reaches BatchBytes or when the rings
// momentarily run dry, and exits after all Close markers have been seen. | asyncWriteLoop 是专用写协程。
// 它排空所有环，把完整记录合并进批次缓冲，在批次达到 BatchBytes 或环暂时为空时冲刷，
// 并在看到全部 Close 标记后退出。
func (c *core) asyncWriteLoop(a *asyncState) {
	defer close(a.done)
	batch := a.batch
	closing := false
	var closeErr error
	for {
		// Clear the wakeup latch before draining: a producer that publishes before this store is
		// seen by the drain below; one that publishes after it wins the CAS and signals. | 在排空前
		// 清除唤醒闩锁：此前发布的生产者会被下面的排空看到，此后发布的生产者会赢得 CAS 并通知。
		a.notifyPending.Store(false)
		progress := false
		for i := range a.rings {
			q := &a.rings[i]
			for {
				e, ok := q.pop()
				if !ok {
					break
				}
				progress = true
				switch {
				case e.close != nil:
					if e.close.remaining.Add(-1) == 0 {
						batch = c.flushBatch(batch)
						closeErr = c.syncWriter()
						closing = true
					}
				case e.sync != nil:
					if e.sync.remaining.Add(-1) == 0 {
						batch = c.flushBatch(batch)
						e.sync.done <- c.syncWriter()
					}
				default:
					b := e.buf
					if len(batch) > 0 && len(batch)+len(b.buf) > a.batchCap {
						batch = c.flushBatch(batch)
					}
					if len(b.buf) > a.batchCap {
						// A single oversized record is written alone so it is never split by the batch cap. | 单条超大记录单独写出，绝不因批次上限而被拆开。
						batch = c.flushBatch(batch)
						c.write(b.buf)
					} else {
						batch = append(batch, b.buf...)
					}
					a.putBuffer(i, b)
				}
			}
		}
		if len(batch) > 0 {
			batch = c.flushBatch(batch)
		}
		if closing {
			a.batch = batch
			a.closeErr = closeErr
			return
		}
		if !progress {
			<-a.notify
		}
	}
}

// startAsync prepares the async state and launches the writer goroutine. | startAsync 准备异步状态并启动写协程。
func (c *core) startAsync(queueSize, batchBytes int) {
	c.async = newAsyncState(queueSize, batchBytes)
	go c.asyncWriteLoop(c.async)
}

// closeAsync drains every queued record, flushes the batch, syncs the writer and stops the
// writer goroutine. It is idempotent and safe to call concurrently with logging and Sync. | closeAsync
// 排空所有已入队记录、冲刷批次、同步写入器并停止写协程。它是幂等的，且可与日志和 Sync 并发调用。
func (l *Logger) closeAsync(a *asyncState) error {
	a.lifeMu.Lock()
	defer a.lifeMu.Unlock()
	if a.closed.Load() {
		return nil
	}
	a.closed.Store(true)
	for {
		var inflight int64
		for i := range a.rings {
			inflight += a.rings[i].inflight.Load()
		}
		if inflight == 0 {
			break
		}
		runtime.Gosched()
	}
	req := &closeRequest{}
	req.remaining.Store(int32(len(a.rings)))
	for i := range a.rings {
		a.pushControl(i, asyncEntry{close: req})
	}
	<-a.done
	return a.closeErr
}

// syncAsync implements the async form of Sync: it places one barrier marker per ring after the
// caller's already-recorded lines, then waits until the writer has flushed them and, when the
// destination implements Sync() error, flushed the destination too. | syncAsync 实现异步形式的 Sync：
// 在调用方已记录的行之后向每个环放置一个屏障标记，等待写协程冲刷这些记录，并在目的地实现
// Sync() error 时一并刷新目的地。
func (l *Logger) syncAsync(a *asyncState) error {
	a.lifeMu.RLock()
	defer a.lifeMu.RUnlock()
	if a.closed.Load() {
		return nil
	}
	req := &syncRequest{done: make(chan error, 1)}
	req.remaining.Store(int32(len(a.rings)))
	for i := range a.rings {
		a.pushControl(i, asyncEntry{sync: req})
	}
	return <-req.done
}
