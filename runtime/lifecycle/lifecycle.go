// Package lifecycle carries the shutdown registry shared by every long-lived
// subsystem of the framework. | Package lifecycle 承载框架内各常驻子系统共用的关闭注册表。
//
// The registry answers one question for the whole process: "is the server going
// away, and who still has to be told?". Long-lived connections (WebSocket,
// SSE, watchers, workers) register a hook here, and the shutdown path closes
// the registry **before** it drains the HTTP server, so those connections start
// winding down while ordinary requests are still being served. | 注册表为整个进程回答
// 一个问题：「服务器是否正在退出，还有谁需要被通知？」长连接（WebSocket、SSE、
// 监听器、后台任务）在此登记钩子；退出流程在排空 HTTP 服务器**之前**关闭注册表，
// 于是长连接在普通请求仍在被处理时就已经开始收尾。
//
// Contract | 契约：
//
//   - hooks run in **reverse registration order**, so a dependency registered
//     later is torn down before the thing it depends on; | 钩子按**注册逆序**执行，
//     后注册的依赖先于它所依赖的对象被拆除；
//   - Shutdown is **idempotent**: concurrent or repeated calls run the hooks
//     once and observe the same aggregated error; | Shutdown **幂等**：并发或重复调用
//     只执行一次钩子，并得到同一个聚合错误；
//   - a hook that panics is recovered and recorded as an error, never
//     propagated; | 钩子 panic 会被恢复并记为错误，绝不向上传播；
//   - Done is closed the moment shutdown starts, which is how a connection
//     handler learns "finish now" without polling; | Done 在关闭开始时立即关闭，
//     连接处理器据此得知「立即收尾」，无需轮询；
//   - every hook is bounded by the context: when it expires the unfinished hook
//     is recorded as an error and the caller returns instead of blocking. | 每个钩子都受
//     上下文约束：ctx 到期即把未完成的钩子记为错误并返回，不阻塞调用方。
package lifecycle

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	yderrs "github.com/shibingli/ydx/errs"
)

// Sentinel errors of this package, all testable with errors.Is. | 本包的哨兵错误，均可通过 errors.Is 判定。
var (
	// ErrHookFailed reports a shutdown hook that returned an error. | ErrHookFailed 表示关闭钩子返回了错误。
	ErrHookFailed = yderrs.New("lifecycle: shutdown hook failed | lifecycle：关闭钩子执行失败")

	// ErrHookPanic reports a shutdown hook that panicked; the panic was recovered. | ErrHookPanic 表示关闭钩子发生 panic；该 panic 已被恢复。
	ErrHookPanic = yderrs.New("lifecycle: shutdown hook panicked | lifecycle：关闭钩子发生 panic")

	// ErrShutdownTimeout reports that the context expired before every hook finished. | ErrShutdownTimeout 表示 ctx 到期时仍有钩子未完成。
	ErrShutdownTimeout = yderrs.New("lifecycle: shutdown deadline exceeded | lifecycle：关闭超过截止时间")
)

// ShutdownFunc is one shutdown task. | ShutdownFunc 是一项关闭任务。
//
// It receives the shutdown context, which carries the grace period: a hook that
// can be interrupted should return as soon as that context is done. | 它接收携带宽限期的关闭上下文：
// 可被中断的钩子应在该上下文结束时尽快返回。
type ShutdownFunc func(ctx context.Context) error

// hook is one registered entry; the pointer identity is what remove uses. | hook 是一条已登记项；remove 依据指针身份定位。
type hook struct {
	fn   ShutdownFunc
	name string
}

// Registry holds the shutdown hooks of one process. | Registry 持有单个进程的关闭钩子。
//
// The zero value is not usable: build it with NewRegistry. A Registry is safe
// for concurrent use, and the shutdown state is tracked in two ways on purpose: | 零值不可用，
// 请用 NewRegistry 构建。Registry 可安全并发使用；关闭状态刻意用两种方式表达：
//
//   - Done / ShuttingDown report "shutdown has started", which is what a
//     connection handler watches; | Done / ShuttingDown 表示「关闭已开始」，供连接处理器观察；
//   - Err reports the error aggregated by the shutdown that finished. | Err 返回已完成的关闭所聚合的错误。
type Registry struct {
	err      error
	done     chan struct{}
	finished chan struct{}
	hooks    []*hook
	mu       sync.Mutex
	shutting atomic.Bool
	spent    bool
}

// NewRegistry creates an empty registry. | NewRegistry 创建一个空注册表。
func NewRegistry() *Registry {
	return &Registry{
		hooks:    make([]*hook, 0, 8),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
}

// Add registers a hook and returns an idempotent remove function. | Add 登记一个钩子并返回幂等的移除函数。
//
// Naming policy: a duplicate name **replaces the earlier hook in place**, so the
// later registration wins and only one hook per name is executed; the remove
// function of the replaced registration becomes a no-op (removal is by identity
// and that identity is gone). A nil fn registers nothing and returns a no-op
// remove. | 命名策略：同名钩子会**原位覆盖**先前的钩子，即后注册者生效、每个名字只执行一次；
// 被覆盖登记项的 remove 随之变成无操作（移除依据身份，而该身份已不存在）。
// fn 为 nil 时不登记任何内容，仅返回空 remove。
//
// A hook registered **after** Shutdown has already returned is not executed:
// the registry is spent, and Add returns a no-op remove. Callers that may race
// with shutdown should check ShuttingDown and finish their work themselves
// instead of relying on registration. | 在 Shutdown 返回**之后**登记的钩子不会被执行：
// 注册表已耗尽，Add 返回空 remove。可能与关闭并发的调用方应先查 ShuttingDown 并自行收尾，
// 而不要依赖登记。
func (r *Registry) Add(name string, fn ShutdownFunc) (remove func()) {
	if fn == nil {
		return func() {}
	}

	h := &hook{name: name, fn: fn}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.spent {
		// The registry is spent: registering would never run, so return an explicit no-op. | 注册表已耗尽：登记也无处执行，明确返回空 remove 而不是静默堆积。
		return func() {}
	}
	for i, existing := range r.hooks {
		if existing.name == name {
			// Same name replaces in place; the earlier remove becomes a no-op. | 同名原位覆盖：后注册者生效，先前的 remove 因身份消失而成为无操作。
			r.hooks[i] = h
			return func() { r.remove(h) }
		}
	}
	r.hooks = append(r.hooks, h)
	return func() { r.remove(h) }
}

// remove deletes one hook by identity; removing it twice is a no-op. | remove 按身份删除一个钩子；重复删除是无操作。
func (r *Registry) remove(h *hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.hooks {
		if existing == h {
			r.hooks = append(r.hooks[:i], r.hooks[i+1:]...)
			return
		}
	}
}

// Len returns the number of hooks still waiting to run. | Len 返回仍在等待执行的钩子数量。
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.hooks)
}

// Done returns a channel closed when shutdown starts. | Done 返回在关闭开始时被关闭的通道。
//
// It is closed before the first hook runs, so handlers can select on it to stop
// accepting new work immediately; it is never closed twice and never reopened. | 它在第一个钩子执行之前就被关闭，
// 处理器因此可以用 select 监听它并立即停止接受新工作；它不会被关闭两次，也不会被重新打开。
func (r *Registry) Done() <-chan struct{} { return r.done }

// ShuttingDown reports whether shutdown has started. | ShuttingDown 报告关闭是否已经开始。
//
// It is true from the moment Shutdown is called (including while the hooks are
// still running) and stays true afterwards. | 自 Shutdown 被调用的那一刻起为 true
// （钩子仍在执行期间同样为 true），此后保持 true。
func (r *Registry) ShuttingDown() bool { return r.shutting.Load() }

// Err returns the error aggregated by the shutdown that finished. | Err 返回已完成的关闭所聚合的错误。
//
// It is nil before the first Shutdown returns; a caller that just observed
// Done closing can wait for Shutdown (or for a concurrent Shutdown to return
// first) before reading it. | 首次 Shutdown 返回之前为 nil；观察到 Done 关闭的调用方
// 应先等待 Shutdown 返回（或等待并发的 Shutdown 先返回）再读取。
func (r *Registry) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Shutdown runs every hook in reverse registration order. | Shutdown 按注册逆序执行全部钩子。
//
// Semantics | 语义：
//
//   - idempotent: the first call runs the hooks, later calls (concurrent or
//     not) wait for that run and return the same aggregated error; | 幂等：首次调用执行钩子，
//     之后的调用（并发或非并发）等待该次执行并返回同一个聚合错误；
//   - Done is closed before the first hook starts; | 第一个钩子开始之前 Done 即被关闭；
//   - errors are joined with errors.Join, so errors.Is matches every failure; | 错误用 errors.Join 聚合，
//     errors.Is 可匹配其中任意一个；
//   - a panicking hook is recovered and recorded as ErrHookPanic; | panic 的钩子被恢复并记为 ErrHookPanic；
//   - when ctx expires the unfinished hook is recorded as ErrShutdownTimeout,
//     the remaining hooks are recorded as skipped, and Shutdown returns
//     immediately instead of blocking; the abandoned hook keeps running in the
//     background and its error is deliberately dropped. | ctx 到期时未完成的钩子记为
//     ErrShutdownTimeout，其余钩子记为跳过，Shutdown 立即返回而不阻塞；
//     被放弃的钩子仍在后台运行，其错误被有意丢弃。
//
// A nil ctx is treated as context.Background. | ctx 为 nil 时按 context.Background 处理。
func (r *Registry) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	if r.shutting.Load() {
		// 已有一次关闭在进行：等待它结束并复用同一个聚合错误。等待同样受本次 ctx 约束，
		// 否则先发起的那次关闭若拖得很久，本次调用会超出自身宽限期仍不返回。
		// A shutdown is already running: wait for it and reuse the same aggregated error.
		// That wait honors this call's ctx as well, otherwise a slow first shutdown would
		// keep this call past its own deadline.
		finished := r.finished
		r.mu.Unlock()
		select {
		case <-finished:
			return r.Err()
		case <-ctx.Done():
			return yderrs.WrapDetail(ctx.Err(), ErrShutdownTimeout,
				"waiting for the shutdown in progress | 等待进行中的关闭")
		}
	}
	r.shutting.Store(true)
	close(r.done)
	r.mu.Unlock()

	var errs []error
	for {
		r.mu.Lock()
		n := len(r.hooks)
		if n == 0 {
			// 与「注册表已耗尽」标记在同一个临界区内完成，
			// 否则并发 Add 可能把钩子塞进一个已经不会再被扫描的切片。
			// Marked spent in the same critical section as the emptiness check,
			// otherwise a concurrent Add could push a hook into a slice nobody scans again.
			joined := errors.Join(errs...)
			r.err = joined
			r.spent = true
			close(r.finished)
			r.mu.Unlock()
			return joined
		}
		h := r.hooks[n-1]
		r.hooks[n-1] = nil
		r.hooks = r.hooks[:n-1]
		r.mu.Unlock()

		if err := ctx.Err(); err != nil {
			// Grace period over: start no new hook, only record the skip, so Shutdown returns on time. | 宽限期已到：不再启动新钩子，只记录跳过，保证 Shutdown 及时返回。
			errs = append(errs, yderrs.WrapDetail(err, ErrShutdownTimeout,
				"hook %[1]q skipped | 钩子 %[1]q 被跳过", h.name))
			continue
		}
		if err := r.runHook(ctx, h); err != nil {
			errs = append(errs, err)
		}
	}
}

// runHook executes one hook under the shutdown context. | runHook 在关闭上下文下执行单个钩子。
//
// The hook runs on its own goroutine so that a hook ignoring the context cannot
// block the shutdown past the grace period; the result channel is buffered, so
// an abandoned hook never leaks a blocked send. | 钩子在自己的协程上执行，
// 因此忽略上下文的钩子无法把关闭拖过宽限期；结果通道带缓冲，被放弃的钩子不会因发送而泄漏。
func (r *Registry) runHook(ctx context.Context, h *hook) error {
	result := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				// A hook panic must be recovered inside the goroutine or it takes the process down. | 单条钩子的 panic 必须在协程内恢复，否则会终结整个进程。
				result <- yderrs.Detail(ErrHookPanic, "hook %[1]q: %[2]v | 钩子 %[1]q：%[2]v", h.name, p)
			}
		}()
		result <- h.fn(ctx)
	}()

	select {
	case err := <-result:
		if err != nil {
			return yderrs.WrapDetail(err, ErrHookFailed, "hook=%[1]q | 钩子=%[1]q", h.name)
		}
		return nil
	case <-ctx.Done():
		return yderrs.WrapDetail(ctx.Err(), ErrShutdownTimeout, "hook=%[1]q | 钩子=%[1]q", h.name)
	}
}
