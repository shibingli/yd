package middleware

import (
	"context"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/http/router"
)

// defaultAsyncQueueSize is the queue capacity used when AsyncOptions.QueueSize is not positive.
// | defaultAsyncQueueSize 是 AsyncOptions.QueueSize 非正数时使用的队列容量。
const defaultAsyncQueueSize = 1024

// ErrCloseTimeout is returned by AsyncAfter.Close when its context expires before every worker exits.
// | ErrCloseTimeout 由 AsyncAfter.Close 在其上下文于全部 worker 退出前到期时返回。
//
// The queue has already been closed when this error is returned: workers keep draining the records
// that were accepted before Close and exit on their own once the queue is empty, so the error means
// "not drained within the deadline", never "goroutines leaked forever". | 返回该错误时队列已经关闭：
// worker 会继续排空 Close 之前已接收的记录，并在队列为空后自行退出；因此它表示「未在期限内排空」，
// 而不是「协程永久泄漏」。
var ErrCloseTimeout = errx.New("async-after close timed out | 响应后异步管线关闭超时")

// Record is a value snapshot of one finished request. | Record 是一次已完成请求的值快照。
//
// It deliberately contains no *router.Ctx, no http.ResponseWriter and no request-owned pointer:
// the record can be handed to a worker goroutine that never touches the request. All strings are
// cloned by AsyncAfter.Submit before the record leaves the request goroutine, so a Record may keep
// referring to request-owned memory at the call site. | 它刻意不含 *router.Ctx、http.ResponseWriter
// 或任何请求期指针：记录可以交给完全不接触请求的 worker 协程。AsyncAfter.Submit 会在记录离开
// 请求协程之前克隆全部字符串，因此调用点可以让 Record 暂时引用请求期内存。
type Record struct {
	// ID is the request id assigned by RequestID (empty when that middleware is absent). | ID 是 RequestID 分配的请求 ID（未启用该中间件时为空）。
	ID string
	// Method is the HTTP method. | Method 是 HTTP 方法。
	Method string
	// Path is the request path without the query string. | Path 是不含查询串的请求路径。
	Path string
	// RemoteIP is the client address as resolved by router.Ctx.RealIP. | RemoteIP 是 router.Ctx.RealIP 解析出的客户端地址。
	RemoteIP string
	// UA is the User-Agent header. | UA 是 User-Agent 请求头。
	UA string
	// Status is the final response status code. | Status 是最终响应状态码。
	Status int
	// Bytes is the number of response body bytes written. | Bytes 是已写出的响应正文字节数。
	Bytes int64
	// Duration is the time spent inside the middleware chain. | Duration 是在中间件链内花费的时间。
	Duration time.Duration
}

// OverflowPolicy selects what AsyncAfter.Submit does when the queue is full. | OverflowPolicy 选择队列满时 AsyncAfter.Submit 的行为。
type OverflowPolicy int

const (
	// OverflowBlock blocks the submitting goroutine until the queue has room. | OverflowBlock 阻塞提交方，直到队列有空位。
	//
	// This is the default. It never loses a record, but it pushes backpressure onto the request
	// goroutine, so a slow handler eventually slows the server down. | 这是默认值。它绝不丢记录，
	// 但会把背压传回请求协程，因此慢处理器最终会拖慢服务器。
	OverflowBlock OverflowPolicy = iota
	// OverflowDropNew drops the new record, increments Dropped and calls OnDrop. | OverflowDropNew 丢弃新记录，递增 Dropped 并调用 OnDrop。
	OverflowDropNew
	// OverflowDropOldest evicts the oldest queued record, increments Dropped, calls OnDrop with the
	// evicted record, and enqueues the new one. | OverflowDropOldest 挤掉最旧的排队记录，递增
	// Dropped、以被挤掉的记录调用 OnDrop，然后入队新记录。
	OverflowDropOldest
)

// AsyncOptions configures an AsyncAfter pipeline. | AsyncOptions 配置 AsyncAfter 管线。
//
// Every field may be left at its zero value. | 所有字段都可保留零值。
type AsyncOptions struct {
	// Handler processes one record. It runs on a worker goroutine and must never touch a
	// http.ResponseWriter, a *router.Ctx or the request body. It must be safe for concurrent use:
	// every worker may call it at the same time, and Submit calls it directly after Close. A nil
	// Handler discards records. | Handler 处理单条记录。它在 worker 协程上运行，绝不能接触
	// http.ResponseWriter、*router.Ctx 或请求体。它必须可安全并发调用：多个 worker 可能同时
	// 调用它，且 Submit 在 Close 之后会直接调用它。Handler 为 nil 时记录被丢弃。
	Handler func(Record)
	// OnDrop is called in the submitting goroutine with every record dropped by an overflow policy.
	// It may be nil. | OnDrop 在提交方协程上对每一条被溢出策略丢弃的记录调用；可为 nil。
	OnDrop func(Record)
	// Workers is the number of worker goroutines; zero or negative means GOMAXPROCS. | Workers 是
	// worker 协程数；零或负数表示 GOMAXPROCS。
	Workers int
	// QueueSize is the queue capacity; zero or negative means 1024. | QueueSize 是队列容量；零或负数表示 1024。
	QueueSize int
	// Overflow selects the full-queue policy; an unrecognized value falls back to OverflowBlock.
	// | Overflow 选择队列满时的策略；无法识别的值回退为 OverflowBlock。
	Overflow OverflowPolicy
}

// AsyncAfter decouples work that does not participate in response generation from the request path.
// | AsyncAfter 把不参与响应生成的工作从请求路径上摘下来。
//
// The contract is narrow and deliberate: [router.Middleware] chain order is a safety property
// (CORS before auth, Recover outermost, Compress closest to the handler), and every middleware
// shares one http.ResponseWriter, so the chain itself cannot run in parallel. The safe parallel
// axis is *after* the response: a finished request is copied into a Record value and handed to a
// worker pool. Workers never see the writer or the context. | 契约刻意保持狭窄：[router.Middleware]
// 链的顺序本身就是安全属性（CORS 在鉴权前、Recover 在最外层、Compress 最贴近处理器），而且
// 所有中间件共享同一个 http.ResponseWriter，因此链本身不能并行。安全的并行轴在响应之后：
// 已完成的请求被复制成 Record 值并交给 worker 池。worker 永远看不到写入器或上下文。
//
// A record submitted after Close runs Handler synchronously on the submitting goroutine instead of
// being dropped, so an audit trail stays complete during shutdown. | Close 之后提交的记录会在提交方
// 协程上同步执行 Handler，而不是被丢弃，从而保证关闭期间审计链仍然完整。
//
// With more than one worker, records are processed concurrently and their completion order is not
// the submission order; use Workers: 1 when strict ordering matters. | 多个 worker 并发处理记录，
// 完成顺序不等于提交顺序；需要严格顺序时使用 Workers: 1。
type AsyncAfter struct {
	handler func(Record)
	onDrop  func(Record)
	queue   chan Record
	stop    chan struct{}
	done    chan struct{}

	wg      sync.WaitGroup
	dropped atomic.Uint64

	workers int
	policy  OverflowPolicy

	sendMu    sync.RWMutex
	closeOnce sync.Once
	closing   bool // guarded by sendMu | 由 sendMu 保护
}

// NewAsyncAfter creates a pipeline and starts its workers. | NewAsyncAfter 创建管线并启动 worker。
//
// Workers exit only through Close; callers must always call Close. | worker 只能通过 Close 退出；
// 调用方必须始终调用 Close。
func NewAsyncAfter(o AsyncOptions) *AsyncAfter {
	workers := o.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	queueSize := o.QueueSize
	if queueSize <= 0 {
		queueSize = defaultAsyncQueueSize
	}
	policy := o.Overflow
	if policy < OverflowBlock || policy > OverflowDropOldest {
		policy = OverflowBlock
	}
	a := &AsyncAfter{
		handler: o.Handler,
		onDrop:  o.OnDrop,
		queue:   make(chan Record, queueSize),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		workers: workers,
		policy:  policy,
	}
	a.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go a.work()
	}
	// A single waiter turns WaitGroup completion into a channel close, so Close can honor a context
	// deadline without polling. It exits as soon as every worker has exited. | 用一个等待协程把
	// WaitGroup 完成转换为通道关闭，使 Close 能响应上下文期限而无需轮询；所有 worker 退出后它立即退出。
	go func() {
		a.wg.Wait()
		close(a.done)
	}()
	return a
}

// Submit copies rec into an immutable snapshot and enqueues it for Handler. | Submit 把 rec 复制为不可变快照并入队交给 Handler。
//
// Submit must be called from the request goroutine; every string field is cloned here, before the
// record can be observed by a worker, which is what makes the Record safe after the request's
// *router.Ctx has been returned to its pool. | Submit 必须由请求协程调用：所有字符串字段都在此处、
// 在 worker 可能观察到记录之前克隆，这正是 Record 在请求的 *router.Ctx 归还对象池后仍然安全的原因。
//
// The call honors the configured overflow policy and may block under OverflowBlock. It never
// touches the response writer and never reads the request body. | 该调用遵循配置的溢出策略，在
// OverflowBlock 下可能阻塞；它绝不触碰响应写入器，也绝不读取请求体。
//
// After Close, Submit no longer enqueues: it runs Handler synchronously on the caller goroutine, so
// the caller pays the handler cost but the record is never silently lost. | Close 之后 Submit 不再
// 入队：它在调用方协程上同步执行 Handler，因此调用方承担处理器成本，但记录绝不会被静默丢弃。
func (a *AsyncAfter) Submit(rec Record) {
	rec = cloneRecord(rec)

	a.sendMu.RLock()
	if a.closing {
		a.sendMu.RUnlock()
		a.run(rec)
		return
	}

	if a.policy == OverflowDropOldest {
		// enqueued is deliberately ignored: when the new record could not be enqueued it is
		// itself part of evicted, so counting the slice covers both cases. | 刻意忽略 enqueued：
		// 新记录未能入队时它本身就在 evicted 中，因此统计切片即可覆盖两种情况。
		evicted, _ := a.enqueueDropOldest(rec)
		a.sendMu.RUnlock()
		if len(evicted) > 0 {
			a.dropped.Add(uint64(len(evicted)))
			for _, dropped := range evicted {
				a.notifyDrop(dropped)
			}
		}
		return
	}

	if a.policy == OverflowDropNew {
		select {
		case a.queue <- rec:
			a.sendMu.RUnlock()
		default:
			a.sendMu.RUnlock()
			a.dropped.Add(1)
			a.notifyDrop(rec)
		}
		return
	}

	select {
	case a.queue <- rec:
		a.sendMu.RUnlock()
	case <-a.stop:
		// Close has started but has not closed the queue yet; running synchronously keeps the
		// record without touching a queue that is about to be closed. | Close 已开始但尚未关闭队列；
		// 同步执行可以保留记录，同时避免触碰即将关闭的队列。
		a.sendMu.RUnlock()
		a.run(rec)
	}
}

// Dropped returns the total number of records dropped by overflow policies since creation.
// | Dropped 返回创建以来被溢出策略丢弃的记录总数。
func (a *AsyncAfter) Dropped() uint64 { return a.dropped.Load() }

// Close stops accepting records, drains the queue, waits for every worker to exit and returns.
// | Close 停止接收记录、排空队列、等待所有 worker 退出后返回。
//
// It is idempotent. When ctx expires first, Close returns ErrCloseTimeout without waiting further;
// the queue is already closed at that point and the workers finish the accepted records and exit
// on their own, so no goroutine is left running forever. | 它是幂等的。若 ctx 先到期，Close 不再
// 等待并返回 ErrCloseTimeout；此时队列已经关闭，worker 处理完已接收的记录后自行退出，因此不会
// 留下永久运行的协程。
//
// Handler must terminate for workers to exit; a Handler that blocks forever makes the pipeline
// impossible to drain by construction. | Handler 必须能够结束，worker 才能退出；永久阻塞的
// Handler 在构造上就使管线无法排空。
func (a *AsyncAfter) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	a.closeOnce.Do(func() {
		// Release blocked submitters first; they either select the queue or fall back to a
		// synchronous run, so the write lock below is never held behind a full queue. | 先释放阻塞
		// 的提交方：它们要么把记录送入队列，要么退化为同步执行，因此下面的写锁不会卡在满队列之后。
		close(a.stop)
		a.sendMu.Lock()
		a.closing = true
		close(a.queue)
		a.sendMu.Unlock()
	})
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return errx.Wrap(ctx.Err(), ErrCloseTimeout)
	}
}

// work runs one worker loop. | work 运行一个 worker 循环。
func (a *AsyncAfter) work() {
	defer a.wg.Done()
	for {
		select {
		case rec, ok := <-a.queue:
			if !ok {
				// A closed queue yields zero-value receives forever; never hand a zero Record to
				// Handler. All buffered records arrive with ok == true before this branch. | 已关闭的
				// 队列会永远返回零值接收；绝不能把零值 Record 交给 Handler。所有缓冲记录都会在
				// ok == true 时先被取出。
				return
			}
			a.run(rec)
		case <-a.stop:
			// Once Close has started, drain whatever was accepted before the queue closes and
			// exit. | Close 开始后，排空队列关闭前已接收的记录，然后退出。
			for rec := range a.queue {
				a.run(rec)
			}
			return
		}
	}
}

// run executes Handler with a per-task recover, so one panicking task cannot kill a worker.
// | run 带单任务 recover 地执行 Handler，使一个任务 panic 不会杀死 worker。
func (a *AsyncAfter) run(rec Record) {
	if a.handler == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logx.Default().Error("async-after handler panic | 响应后异步处理器 panic",
				logx.Any("panic", r), logx.Str("stack", string(debug.Stack())))
		}
	}()
	a.handler(rec)
}

// notifyDrop invokes OnDrop with a per-task recover for the same reason as run. | notifyDrop 以与 run 相同的单任务 recover 调用 OnDrop。
func (a *AsyncAfter) notifyDrop(rec Record) {
	if a.onDrop == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logx.Default().Error("async-after OnDrop panic | 响应后异步丢弃回调 panic",
				logx.Any("panic", r), logx.Str("stack", string(debug.Stack())))
		}
	}()
	a.onDrop(rec)
}

// enqueueDropOldest implements OverflowDropOldest. It returns the records it evicted and whether
// rec was enqueued; when it returns false, rec itself is included in evicted. The caller must hold
// at least a read lock on sendMu. | enqueueDropOldest 实现 OverflowDropOldest。它返回被挤掉的记录
// 以及 rec 是否成功入队；返回 false 时 rec 本身也在 evicted 中。调用方必须至少持有 sendMu 的读锁。
func (a *AsyncAfter) enqueueDropOldest(rec Record) (evicted []Record, enqueued bool) {
	// A worker can consume a slot between the failed send and the eviction, so retry a bounded
	// number of times before giving up on the new record. | worker 可能在发送失败与挤旧之间消费掉
	// 一个空位，因此重试有限次数后才放弃新记录。
	for attempt := 0; attempt < 4; attempt++ {
		select {
		case a.queue <- rec:
			return evicted, true
		default:
		}
		select {
		case old := <-a.queue:
			evicted = append(evicted, old)
		default:
			// The worker consumed the slot; retry the send without dropping anything. | worker
			// 消费了空位，无需丢弃任何记录，直接重试发送。
		}
	}
	return append(evicted, rec), false
}

// cloneRecord returns a copy whose strings do not share memory with the input. | cloneRecord 返回
// 一个字符串不与输入共享内存的副本。
func cloneRecord(rec Record) Record {
	rec.ID = strings.Clone(rec.ID)
	rec.Method = strings.Clone(rec.Method)
	rec.Path = strings.Clone(rec.Path)
	rec.RemoteIP = strings.Clone(rec.RemoteIP)
	rec.UA = strings.Clone(rec.UA)
	return rec
}

// AccessLogHandler returns an AsyncOptions.Handler that writes one access-log record. | AccessLogHandler
// 返回把每条 Record 写成一条访问日志的 AsyncOptions.Handler。
//
// The logged fields are request_id, method, path, status, bytes, remote_ip, user_agent and duration.
// Unlike AccessLog, the Record snapshot carries no query string, so the async variant omits it; the
// synchronous AccessLog middleware is unchanged and still logs query. The timestamp of the line is
// when a worker writes it, while duration still measures the request itself. | 记录字段为 request_id、
// method、path、status、bytes、remote_ip、user_agent 与 duration。与 AccessLog 不同，Record 快照
// 不包含查询串，因此异步版本省略该字段；同步 AccessLog 中间件保持不变，仍记录 query。日志行的
// 时间戳是 worker 写出的时刻，而 duration 仍度量请求本身。
// A nil logger returns a nil handler. | 日志器为 nil 时返回 nil 处理器。
func AccessLogHandler(l *logx.Logger) func(Record) {
	if l == nil {
		return nil
	}
	return func(rec Record) {
		l.Info("access | 访问日志",
			logx.Str("request_id", rec.ID),
			logx.Str("method", rec.Method),
			logx.Str("path", rec.Path),
			logx.Str("remote_ip", rec.RemoteIP),
			logx.Str("user_agent", rec.UA),
			logx.Int("status", rec.Status),
			logx.Int64("bytes", rec.Bytes),
			logx.Dur("duration", rec.Duration),
		)
	}
}

// AccessLogAsync returns an access-log middleware that hands every finished request to the
// pipeline as a value Record instead of logging it on the request path. | AccessLogAsync 返回把每个
// 已完成请求以 Record 值交给管线的访问日志中间件，而不是在请求路径上写日志。
//
// The capture half is identical to AccessLog: a pooled countingWriter records status and bytes,
// upgrade requests are not wrapped, and a panic is still recorded as 500 and re-raised. | 采集部分
// 与 AccessLog 完全一致：池化的 countingWriter 记录状态码与字节数，升级请求不做包装，panic
// 仍按 500 记录后重新抛出。
// a owns the processing side; configure it with AccessLogHandler(l) to write the actual log line,
// for example:
//
//	after := middleware.NewAsyncAfter(middleware.AsyncOptions{
//	    Handler: middleware.AccessLogHandler(logger),
//	})
//	r.Use(middleware.RequestID(""), middleware.AccessLogAsync(logger, after))
//
// When a is nil the middleware degrades to the synchronous AccessLog(l), so the parameter is never
// silently unused. When a is non-nil, l is only that fallback logger and the pipeline handler does
// the real work. | a 拥有处理侧；用 AccessLogHandler(l) 配置它即可写出真正的日志行，例如上面的
// 示例。a 为 nil 时中间件退化为同步 AccessLog(l)，因此参数不会静默失效；a 非 nil 时 l 仅作为
// 该回退日志器，实际工作由管线处理器完成。
func AccessLogAsync(l *logx.Logger, a *AsyncAfter) router.Middleware {
	if a == nil {
		return AccessLog(l)
	}
	return func(c *router.Ctx) {
		if isUpgradeRequest(c.Request) {
			c.Next()
			return
		}
		start := time.Now()
		cw := countingWriterPool.Get().(*countingWriter)
		cw.ResponseWriter = c.Writer
		cw.status = 0
		cw.bytes = 0
		c.Writer = cw

		defer func() {
			rec := recover()
			c.Writer = cw.ResponseWriter
			status := cw.status
			if status == 0 {
				status = c.StatusCode()
			}
			if status == 0 {
				if rec != nil {
					// A panic that escaped us becomes a 500 upstream. | 逃逸到上层的 panic 最终会成为 500。
					status = http.StatusInternalServerError
				} else {
					status = http.StatusOK
				}
			}
			a.Submit(Record{
				ID:       requestID(c),
				Method:   c.Request.Method,
				Path:     c.Request.URL.Path,
				RemoteIP: c.RealIP(),
				UA:       c.Request.UserAgent(),
				Status:   status,
				Bytes:    cw.bytes,
				Duration: time.Since(start),
			})
			cw.ResponseWriter = nil
			cw.status = 0
			cw.bytes = 0
			countingWriterPool.Put(cw)
			if rec != nil {
				// Re-raise instead of swallowing: the outer Recover middleware owns the final response.
				// | 重新抛出而不是吞掉：最终响应由更外层的 Recover 中间件负责。
				panic(rec)
			}
		}()

		c.Next()
	}
}
