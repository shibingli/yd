// Package asynqx is the yd framework's asynchronous task layer on top of asynq. | Package asynqx 是 yd 框架基于 asynq 的异步任务层。
//
// Responsibilities: | 职责：
//
//   - Options/OptionsFromConfig describe one asynq deployment: connection, weighted queues, |   - Options/OptionsFromConfig 描述一套 asynq 部署：连接、加权队列、并发度、
//     concurrency, graceful-shutdown budget and the retry-delay policy; ParseQueues turns |     优雅关闭预算与重试延迟策略；ParseQueues 把 "critical:3,default:2,low:1"
//     the "critical:3,default:2,low:1" config string into a weight table; |     形式的配置串转为权重表；
//   - Registry is the declarative task table shared by clients and servers: one task type, |   - Registry 是客户端与服务端共享的声明式任务表：一种任务类型对应一个
//     one Handler over a JSON Payload; |     以 JSON Payload 为入参的 Handler；
//   - Client enqueues tasks - immediately, after a delay or at an instant - and exposes the |   - Client 负责入队（立即、延时或定时），并暴露 asynq 的 Inspector 供运维查询；
//     asynq Inspector for operations queries;
//   - Server consumes tasks through a built-in middleware chain: task context injection, |   - Server 消费任务，并内置中间件链：任务上下文注入、任务级日志与 panic 恢复；
//     per-task logging and panic recovery;
//   - TaskContext/TraceID travel with the task, so a handler can correlate logs across |   - TaskContext/TraceID 随任务传递，处理器据此跨进程、跨服务串联日志。
//     process and service boundaries.
//
// Logging goes through logx only: the framework's own records and asynq's internal ones are | 日志只走 logx：框架自身的记录与 asynq 内部记录写入同一输出端，
// written to the same sink, so one stream contains the whole task life cycle. | 一条流即可覆盖任务的完整生命周期。
package asynqx

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/core/logx"
)

// Declared sentinel errors, testable with errors.Is. | 已声明的哨兵错误，可用 errors.Is 判定。
var (
	// ErrEmptyTaskType indicates an empty task type was registered or enqueued. | ErrEmptyTaskType 表示注册或入队的任务类型为空。
	ErrEmptyTaskType = errx.New("asynqx: empty task type | asynqx：任务类型为空")
	// ErrNilHandler indicates a nil handler was registered for a task type. | ErrNilHandler 表示为某任务类型注册的处理器为 nil。
	ErrNilHandler = errx.New("asynqx: nil handler | asynqx：处理器为 nil")
	// ErrNilRegistry indicates a nil registry was handed to a helper. | ErrNilRegistry 表示传给辅助函数的注册表为 nil。
	ErrNilRegistry = errx.New("asynqx: nil registry | asynqx：注册表为 nil")
	// ErrEncodeFailed indicates the payload could not be serialized by jsonx. | ErrEncodeFailed 表示载荷无法由 jsonx 序列化。
	ErrEncodeFailed = errx.New("asynqx: encode payload failed | asynqx：载荷序列化失败")
	// ErrDecodeFailed indicates the task body is not a JSON object. | ErrDecodeFailed 表示任务载荷不是 JSON 对象。
	ErrDecodeFailed = errx.New("asynqx: decode payload failed | asynqx：载荷反序列化失败")
	// ErrEnqueueFailed indicates asynq rejected or failed to store the task. | ErrEnqueueFailed 表示 asynq 拒绝入队或写入失败。
	ErrEnqueueFailed = errx.New("asynqx: enqueue task failed | asynqx：任务入队失败")
	// ErrInvalidSchedule indicates an unusable delay or schedule instant. | ErrInvalidSchedule 表示不可用的延时或定时时刻。
	ErrInvalidSchedule = errx.New("asynqx: invalid schedule | asynqx：调度参数非法")
	// ErrMissingRedisAddr indicates no redis address was configured anywhere. | ErrMissingRedisAddr 表示任何位置都没有配置 Redis 地址。
	ErrMissingRedisAddr = errx.New("asynqx: missing redis address | asynqx：缺少 Redis 地址")
	// ErrInvalidRedisAddr indicates the address is not in host:port form. | ErrInvalidRedisAddr 表示地址不是 host:port 形式。
	ErrInvalidRedisAddr = errx.New("asynqx: invalid redis address | asynqx：Redis 地址非法")
	// ErrUnknownTaskType indicates no handler is registered for the task type being processed. | ErrUnknownTaskType 表示被处理的任务类型没有注册处理器。
	ErrUnknownTaskType = errx.New("asynqx: unknown task type | asynqx：未知的任务类型")
	// ErrHandlerPanic indicates a handler panicked; the recovered value is attached as detail. | ErrHandlerPanic 表示处理器发生 panic，恢复到的取值作为细节附加。
	ErrHandlerPanic = errx.New("asynqx: handler panicked | asynqx：处理器发生 panic")
	// ErrServerStartFailed indicates the asynq server refused to start. | ErrServerStartFailed 表示 asynq 服务端拒绝启动。
	ErrServerStartFailed = errx.New("asynqx: server start failed | asynqx：服务端启动失败")
	// ErrCloseFailed indicates the client failed to release its connections. | ErrCloseFailed 表示客户端释放连接失败。
	ErrCloseFailed = errx.New("asynqx: close client failed | asynqx：关闭客户端失败")
)

// defaultRetention is how long a finished task stays queryable through the Inspector. | defaultRetention 是已完成任务可通过 Inspector 查询的保留时长。
//
// asynq drops a finished task immediately unless its message carries a retention, which would | 若消息未携带保留时长，asynq 会立刻丢弃已完成的任务，
// make "what happened to task X" unanswerable. 24 hours keeps that operations question | 使「任务 X 到底怎么样」无从回答。保留 24 小时让这个问题在一天之内都有答案，
// answerable for a day, at the cost of a bounded number of extra keys in redis; pass | 代价是 Redis 中多出一批有上限的键；调用方显式传入 asynq.Retention 即可按任务覆盖。
// asynq.Retention to override it per task.
const defaultRetention = 24 * time.Hour

// Options describes one asynq deployment. | Options 描述一套 asynq 部署。
//
// Every field may be left at its zero value: Concurrency then follows the CPU count, Queues | 所有字段都可留零值：Concurrency 随之取 CPU 核数，Queues 为空则只消费 default 队列，
// falls back to consuming only the default queue, ShutdownTimeout falls back to asynq's | ShutdownTimeout 回落到 asynq 默认值，
// default, and a nil RetryDelayFunc keeps asynq's own retry policy. | RetryDelayFunc 为 nil 时沿用 asynq 自身的重试策略。
type Options struct {

	// err records a problem found while the options were built from config (OptionsFromConfig), | err 记录从配置构建选项时发现的问题（OptionsFromConfig）；
	// which cannot return an error without changing its signature; Err surfaces it and it | 该函数在不改变签名的前提下无法返回错误；Err 暴露它，
	// blocks NewClient and Server.Start. | 并阻断 NewClient 与 Server.Start。
	err error
	// Queues maps a queue name to its weight, for example {"critical":3,"default":2,"low":1}; | Queues 是队列名到权重的映射，例如 {"critical":3,"default":2,"low":1}；
	// an empty map means the server consumes only the default queue. | 空表表示只消费 default 队列。
	Queues map[string]int
	// RetryDelayFunc computes the delay before the retryCount-th retry; nil keeps asynq's | RetryDelayFunc 计算第 retryCount 次重试前的延迟；nil 表示沿用 asynq 自身策略，
	// own policy, and ExponentialBackoff builds one. | ExponentialBackoff 可构造一个。
	RetryDelayFunc func(retryCount int, err error, task *asynq.Task) time.Duration

	// RedisAddr is the address of the task broker, "host:port"; it is used when the | RedisAddr 是任务中间件的地址，"host:port"；
	// asynq.RedisClientOpt handed to NewClient / NewServer carries no address. | 当传给 NewClient / NewServer 的 asynq.RedisClientOpt 未带地址时使用它。
	RedisAddr string
	// RedisPassword is the authentication password; empty means no authentication. | RedisPassword 是认证口令；空值表示不认证。
	RedisPassword string
	// RedisDB is the database index; 0 leaves the choice to asynq's client option. | RedisDB 是库序号；为 0 时由 asynq 的客户端参数决定。
	RedisDB int
	// Concurrency is the number of tasks processed in parallel; <=0 follows the CPU count. | Concurrency 是并行处理的任务数；<=0 时取 CPU 核数。
	Concurrency int
	// ShutdownTimeout bounds how long Shutdown waits for in-flight handlers; <=0 uses the | ShutdownTimeout 限制 Shutdown 等待在途处理器的时长；<=0 时使用 asynq 默认值。
	// asynq default.
	ShutdownTimeout time.Duration
	// StrictPriority makes the server drain a higher-weight queue before touching a lower one; | StrictPriority 让服务端先清空高权重队列再处理低权重队列；
	// false makes the weight a sampling ratio that avoids starving low-weight queues. | false 时权重退化为采样比例，低权重队列不会被饿死。
	StrictPriority bool
}

// Err returns the latent error recorded while the options were built; nil when they are usable. | Err 返回构建选项时记录的潜在错误；选项可用时为 nil。
//
// It is the hook that lets OptionsFromConfig stay error-free while a malformed asynq.queues | 它让 OptionsFromConfig 保持无错误返回，同时把非法的 asynq.queues
// still stops the service at startup instead of being silently ignored. | 留到启动期阻断服务，而不是被静默忽略。
func (o Options) Err() error { return o.err }

// Client enqueues tasks into the queues described by Options. | Client 把任务入队到 Options 描述的队列中。
type Client struct {
	cli *asynq.Client
	rc  asynq.RedisClientOpt
}

// NewClient builds a client for the deployment described by r and o. | NewClient 为 r 与 o 描述的部署构建客户端。
//
// Connection parameters missing from r fall back to the Options fields (Addr from RedisAddr, | r 中缺失的连接参数回落到 Options 字段（地址取 RedisAddr，口令取 RedisPassword，
// Password from RedisPassword, DB from RedisDB). An empty address reports ErrMissingRedisAddr | 库序号取 RedisDB）。地址为空返回 ErrMissingRedisAddr，
// and an address that is not in host:port form reports ErrInvalidRedisAddr. | 不是 host:port 形式返回 ErrInvalidRedisAddr。
//
// NewClient performs no round trip, so a client can be built before the broker is reachable; | NewClient 不做网络往返，因此可以在服务端就绪之前构造；
// the caller owns the returned client and must Close it. | 返回的客户端由调用方负责 Close。
func NewClient(r asynq.RedisClientOpt, o Options) (*Client, error) {
	if err := o.Err(); err != nil {
		return nil, err
	}
	rc, err := resolveRedisOpt(r, o)
	if err != nil {
		return nil, err
	}
	return &Client{cli: asynq.NewClient(rc), rc: rc}, nil
}

// Enqueue puts a task of taskType on its queue and returns the asynq task info. | Enqueue 把 taskType 类型的任务放入其队列，并返回 asynq 任务信息。
//
// payload is serialized with jsonx: a payload jsonx cannot serialize reports | payload 由 jsonx 序列化：无法序列化时返回 ErrEncodeFailed，
// ErrEncodeFailed and an empty task type reports ErrEmptyTaskType. The trace id of ctx (see | 任务类型为空返回 ErrEmptyTaskType。ctx 中的链路 id（见 WithTraceID）
// WithTraceID) travels in the task headers, so the handler keeps the same trace even in | 随任务头传递，因此即使处理器位于其它进程也处于同一条链路。
// another process. asynq options (asynq.Queue, asynq.MaxRetry, asynq.ProcessIn, ...) are | asynq 选项（asynq.Queue、asynq.MaxRetry、asynq.ProcessIn 等）原样透传。
// forwarded verbatim.
//
// Every task carries defaultRetention unless the caller passes its own asynq.Retention, so a | 除调用方自行传入 asynq.Retention 外，每个任务都带 defaultRetention，
// finished task stays inspectable instead of vanishing the moment it succeeds. | 因此已完成任务仍可被查询，而不会在成功的瞬间消失。
func (c *Client) Enqueue(ctx context.Context, taskType string, payload any, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if strings.TrimSpace(taskType) == "" {
		return nil, ErrEmptyTaskType
	}
	raw, err := encodePayload(payload)
	if err != nil {
		return nil, err
	}
	info, err := c.cli.EnqueueContext(ctx, newTask(taskType, raw, traceHeaders(ctx)), withDefaults(opts)...)
	if err != nil {
		return nil, errx.Wrap(err, ErrEnqueueFailed)
	}
	return info, nil
}

// EnqueueIn puts a task on its queue to be processed after delay. | EnqueueIn 把任务放入队列，等待 delay 之后再处理。
//
// A non-positive delay reports ErrInvalidSchedule, because asynq would silently treat it as | 非正数的 delay 返回 ErrInvalidSchedule：asynq 会把它当作立即执行，
// an immediate run and hide a caller bug. | 从而掩盖调用方的错误。
func (c *Client) EnqueueIn(ctx context.Context, taskType string, payload any, delay time.Duration, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if delay <= 0 {
		return nil, errx.Detail(ErrInvalidSchedule, "delay %[1]v | 延迟 %[1]v", delay)
	}
	return c.Enqueue(ctx, taskType, payload, withOption(opts, asynq.ProcessIn(delay))...)
}

// EnqueueAt puts a task on its queue to be processed at the given instant. | EnqueueAt 把任务放入队列，在指定时刻处理。
//
// A zero instant reports ErrInvalidSchedule; an instant in the past runs through asynq's own | 零值时刻返回 ErrInvalidSchedule；过去的时刻按 asynq 自身的语义立即执行。
// semantics, which is immediate execution.
func (c *Client) EnqueueAt(ctx context.Context, taskType string, payload any, at time.Time, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if at.IsZero() {
		return nil, errx.Detail(ErrInvalidSchedule, "%s", "zero time | 零值时刻")
	}
	return c.Enqueue(ctx, taskType, payload, withOption(opts, asynq.ProcessAt(at))...)
}

// Inspector returns an asynq inspector bound to the same deployment. | Inspector 返回绑定同一部署的 asynq 检查器。
//
// It is meant for operations queries - queue statistics, task states, cancellation - and it | 它用于运维查询——队列统计、任务状态、取消任务——并持有自己的连接，
// holds its own connection, so the caller owns it and must Close it. | 因此由调用方负责 Close。
func (c *Client) Inspector() *asynq.Inspector { return asynq.NewInspector(c.rc) }

// Close releases the connections held by the client; a nil client is a no-op. | Close 释放客户端持有的连接；nil 客户端为空操作。
func (c *Client) Close() error {
	if c == nil || c.cli == nil {
		return nil
	}
	if err := c.cli.Close(); err != nil {
		return errx.Wrap(err, ErrCloseFailed)
	}
	return nil
}

// Server consumes tasks and dispatches them to a Registry. | Server 消费任务并分发给 Registry。
type Server struct {
	rc  asynq.RedisClientOpt
	reg *Registry
	log *logx.Logger

	srv *asynq.Server

	// baseCtx is the parent of every task handler context; cancelling it before Shutdown lets
	// in-flight handlers observe the shutdown immediately instead of after the grace period.
	// | baseCtx 是每个任务处理器上下文的父级；在 Shutdown 前取消它，使在途处理器立即感知退出，
	// 而不是等宽限期结束。
	baseCtx    context.Context
	baseCancel context.CancelFunc

	opt Options

	mu      sync.Mutex
	started bool
}

// NewServer builds a server for reg; l may be nil, in which case logx.Default() is used. | NewServer 为 reg 构建服务端；l 可为 nil，此时使用 logx.Default()。
//
// NewServer performs no I/O and reports no error: connection parameters, registrations and | NewServer 不做 I/O 也不返回错误：连接参数、注册表与选项的问题
// options are all validated by Start, which is where a service fails fast at startup. | 一律由 Start 校验，服务正是在启动期就此快速失败。
func NewServer(reg *Registry, r asynq.RedisClientOpt, o Options, l *logx.Logger) *Server {
	if l == nil {
		l = logx.Default()
	}
	return &Server{reg: reg, rc: r, opt: o, log: l}
}

// Start begins consuming tasks and returns once the workers are up. | Start 开始消费任务，工作协程就绪后返回。
//
// It fails fast on a nil registry (ErrNilRegistry), a registry whose registration was invalid | 它在以下情况快速失败：注册表为 nil（ErrNilRegistry）、注册表存在非法注册
// (the error recorded by Register), unusable options (Options.Err, for example a malformed | （Register 记录的错误）、选项不可用（Options.Err，例如由 OptionsFromConfig 传入的非法队列配置）
// asynq.queues coming from OptionsFromConfig) and unusable connection parameters | 以及连接参数不可用（ErrMissingRedisAddr / ErrInvalidRedisAddr）。
// (ErrMissingRedisAddr / ErrInvalidRedisAddr). A second call while running reports an error | 运行中再次调用会返回包装 ErrServerStartFailed 的错误；Start 只启动服务，阻塞由 Run 负责。
// wrapping ErrServerStartFailed. Start itself never blocks: Run waits for the shutdown signal.
func (s *Server) Start() error {
	if s.reg == nil {
		return ErrNilRegistry
	}
	if err := s.reg.Err(); err != nil {
		return err
	}
	if err := s.opt.Err(); err != nil {
		return err
	}
	rc, err := resolveRedisOpt(s.rc, s.opt)
	if err != nil {
		return err
	}

	baseCtx, baseCancel := context.WithCancel(context.Background())
	cfg := asynq.Config{
		Concurrency:     s.opt.Concurrency,
		Queues:          queueWeights(s.opt.Queues),
		StrictPriority:  s.opt.StrictPriority,
		ShutdownTimeout: s.opt.ShutdownTimeout,
		RetryDelayFunc:  s.opt.RetryDelayFunc,
		Logger:          asynqLogger{l: s.log.Named("asynq")},
		BaseContext:     func() context.Context { return baseCtx },
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		baseCancel()
		return errx.Detail(ErrServerStartFailed, "%s", "already started | 服务端已启动")
	}
	srv := asynq.NewServer(rc, cfg)
	if err := srv.Start(s); err != nil {
		baseCancel()
		return errx.Wrap(err, ErrServerStartFailed)
	}
	s.srv = srv
	s.baseCtx = baseCtx
	s.baseCancel = baseCancel
	s.started = true
	return nil
}

// Shutdown stops pulling tasks and waits for in-flight handlers, bounded by | Shutdown 停止拉取任务并等待在途处理器结束，上限由 Options.ShutdownTimeout 决定。
//
// It is safe to call more than once and a no-op before Start: the asynq server keeps its own | 重复调用是安全的，Start 之前调用为空操作：asynq 服务端自身维护关闭状态，
// closed state, so a second Shutdown returns immediately instead of blocking. Run also calls | 因此第二次 Shutdown 会立即返回而不会阻塞。Run 在上下文取消时也调用它收尾。
// it to wind down.
func (s *Server) Shutdown() {
	s.mu.Lock()
	srv := s.srv
	cancel := s.baseCancel
	s.mu.Unlock()
	if srv == nil {
		return
	}
	// Cancel handler contexts first: asynq's Shutdown waits for in-flight handlers, and a handler
	// that checks ctx should stop now rather than at the grace-period deadline. | 先取消处理器上下文：
	// asynq 的 Shutdown 会等待在途处理器，检查 ctx 的处理器应当现在停止，而不是等到宽限期结束。
	if cancel != nil {
		cancel()
	}
	srv.Shutdown()
}

// Run starts the server and blocks until ctx is cancelled, then shuts down gracefully. | Run 启动服务端并阻塞直到 ctx 被取消，随后优雅关闭。
//
// Cancellation is normal termination and returns nil; a failure to start is reported by Start | 取消属于正常终止并返回 nil；启动失败会原样返回 Start 的错误。
// unchanged. asynq's own Run waits for OS signals, which does not fit an application that owns | asynq 自带的 Run 等待操作系统信号，不适用于由框架掌握生命周期的应用，因此这里以 ctx 为准。
// its own lifecycle, so this variant is drive-by-context.
func (s *Server) Run(ctx context.Context) error {
	if err := s.Start(); err != nil {
		return err
	}
	<-ctx.Done()
	s.Shutdown()
	return nil
}

// ProcessTask dispatches one task; it is the asynq.Handler the server is started with. | ProcessTask 分发单个任务；它正是启动服务端时使用的 asynq.Handler。
//
// The middleware chain runs from the outside in as task context injection (TaskContext plus a | 中间件链由外到内依次为：任务上下文注入（TaskContext 与任务级子 logger）、任务级日志、
// task-scoped child logger), per-task logging, panic recovery, and finally payload decoding | panic 恢复，最后是载荷解码与已注册的 Handler。未注册的类型返回包装 ErrUnknownTaskType
// plus the registered Handler. An unregistered type reports an error wrapping | 的错误，因此任务会被重试，并最终带着明确原因进入失败归档。
// ErrUnknownTaskType, so the task is retried and eventually archived with a clear reason.
func (s *Server) ProcessTask(ctx context.Context, t *asynq.Task) error {
	name := ""
	if t != nil {
		name = t.Type()
	}
	h, ok := s.reg.lookup(name)
	if !ok {
		s.logger().Warn("unknown task type | 未知的任务类型", logx.Str("task_type", name))
		return errx.Detail(ErrUnknownTaskType, "%q", name)
	}

	base := func(ctx context.Context, t *asynq.Task) error {
		p, err := decodePayload(t.Payload())
		if err != nil {
			return err
		}
		return h(ctx, p)
	}
	return chain(base, s.injectTaskContext, s.logTask, s.recoverPanic)(ctx, t)
}

// handlerFunc is the chain-local task handler. | handlerFunc 是链内的任务处理器。
type handlerFunc func(ctx context.Context, t *asynq.Task) error

// middleware decorates the next handler of the chain. | middleware 装饰链上的下一个处理器。
type middleware func(next handlerFunc) handlerFunc

// chain applies mws to h so that mws[0] ends up outermost, mirroring the ServeMux convention. | chain 把 mws 套在 h 上，使 mws[0] 位于最外层，与 ServeMux 的约定一致。
func chain(h handlerFunc, mws ...middleware) handlerFunc {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// injectTaskContext builds the TaskContext and the task-scoped child logger, and puts both | injectTaskContext 构建 TaskContext 与任务级子 logger，并把两者放入 ctx。
// into ctx so every inner layer and the handler itself can read them. | 供内层与处理器本身读取。
func (s *Server) injectTaskContext(next handlerFunc) handlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		tc := taskContextOf(ctx, t)
		ctx = context.WithValue(ctx, ctxKeyTask, tc)
		ctx = context.WithValue(ctx, ctxKeyLogger, s.taskLogger(tc))
		if tc.TraceID != "" {
			ctx = context.WithValue(ctx, ctxKeyTraceID, tc.TraceID)
		}
		return next(ctx, t)
	}
}

// logTask writes one record per task: completion at info level, failure at error level. | logTask 为每个任务写一条记录：完成走 info 级，失败走 error 级。
//
// The level split keeps the completion stream quiet in production while a failure always | 分级使生产环境的完成流水保持安静，而失败总会
// reaches error sinks; both records carry the task fields and the elapsed duration. | 进入错误输出端；两条记录都带任务字段与耗时。
func (s *Server) logTask(next handlerFunc) handlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		started := time.Now()
		err := next(ctx, t)
		dur := logx.Dur("duration", time.Since(started))
		if err != nil {
			s.loggerFrom(ctx).Error("task failed | 任务处理失败", dur, logx.Err(err))
			return err
		}
		s.loggerFrom(ctx).Info("task completed | 任务处理完成", dur)
		return nil
	}
}

// recoverPanic turns a handler panic into an error, so the worker goroutine survives and asynq | recoverPanic 把处理器 panic 转为错误，使工作协程存活，并由 asynq 按重试策略重试该任务。
// retries the task according to its retry policy.
func (s *Server) recoverPanic(next handlerFunc) handlerFunc {
	return func(ctx context.Context, t *asynq.Task) (err error) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if l := s.loggerFrom(ctx); l.Enabled(logx.ErrorLevel) {
				l.Error("handler panicked | 处理器发生 panic",
					logx.Any("panic", r), logx.Str("stack", string(debug.Stack())))
			}
			err = errx.Wrap(&panicError{value: r}, ErrHandlerPanic)
		}()
		return next(ctx, t)
	}
}

// panicError carries a recovered panic value as an error. | panicError 把恢复到的 panic 取值承载为 error。
type panicError struct{ value any }

// Error renders the panic value the way fmt does. | Error 按 fmt 的方式渲染 panic 取值。
func (e *panicError) Error() string { return fmt.Sprint(e.value) }

// logger returns the server logger; it is never nil. | logger 返回服务端日志器；它永不为 nil。
func (s *Server) logger() *logx.Logger {
	if s.log == nil {
		return logx.Default()
	}
	return s.log
}

// loggerFrom returns the task-scoped logger injected into ctx, falling back to the server's. | loggerFrom 返回注入 ctx 的任务级日志器，缺失时回退到服务端日志器。
func (s *Server) loggerFrom(ctx context.Context) *logx.Logger {
	if l := LoggerFrom(ctx); l != nil {
		return l
	}
	return s.logger()
}

// taskContextOf reads the asynq task context and the task headers into a TaskContext. | taskContextOf 把 asynq 任务上下文与任务头读入 TaskContext。
//
// Every asynq accessor reports absence with ok, which happens when ProcessTask is driven | 每个 asynq 访问器都用 ok 报告缺失，直接驱动 ProcessTask 时（例如单元测试）即如此，
// directly (a unit test, for example), and the corresponding field is then left empty. | 此时对应字段留空。
func taskContextOf(ctx context.Context, t *asynq.Task) *TaskContext {
	tc := &TaskContext{}
	if t != nil {
		tc.Type = t.Type()
		if h := t.Headers(); h != nil {
			tc.TraceID = h[headerTraceID]
		}
	}
	if id, ok := asynq.GetTaskID(ctx); ok {
		tc.TaskID = id
	}
	if q, ok := asynq.GetQueueName(ctx); ok {
		tc.Queue = q
	}
	if n, ok := asynq.GetRetryCount(ctx); ok {
		tc.RetryCount = n
	}
	if n, ok := asynq.GetMaxRetry(ctx); ok {
		tc.MaxRetry = n
	}
	return tc
}

// taskLogger derives the child logger of one task. | taskLogger 派生单个任务的子 logger。
//
// The fields are the correlation keys of the whole life cycle: task_id and queue find the | 这些字段是整条生命周期的关联键：task_id 与 queue 用于定位任务，
// task, retry_count shows how many attempts were spent, and trace_id links the async hop | retry_count 显示已消耗的尝试次数，trace_id 则把异步这一跳接回调用方的链路。
// back to the caller's trace.
func (s *Server) taskLogger(tc *TaskContext) *logx.Logger {
	fields := []logx.Field{
		logx.Str("task_id", tc.TaskID),
		logx.Str("task_type", tc.Type),
		logx.Str("queue", tc.Queue),
		logx.Int("retry_count", tc.RetryCount),
		logx.Int("max_retry", tc.MaxRetry),
	}
	if tc.TraceID != "" {
		fields = append(fields, logx.Str("trace_id", tc.TraceID))
	}
	return s.logger().With(fields...)
}

// asynqLogger adapts logx to the asynq.Logger interface, so asynq's internal records land in | asynqLogger 把 logx 适配为 asynq.Logger 接口，使 asynq 的内部记录与框架记录
// the same sink as the framework's own records. | 写入同一输出端。
//
// asynq formats its arguments printf-style; they are joined into a single "args" field instead | asynq 按 printf 风格拼接参数；这里把它们合并成单个 "args" 字段，避免二次格式化。
// of being formatted again. Fatal is mapped to Error, because asynq reaches Fatal only from its | Fatal 映射为 Error：asynq 只在命令行解析路径用到 Fatal，服务端运行期不会调用它。
// command-line parsing path, never from the server runtime.
type asynqLogger struct{ l *logx.Logger }

// Debug logs at debug level. | Debug 以 debug 级别记录。
func (a asynqLogger) Debug(args ...interface{}) {
	a.log(logx.DebugLevel, "asynq debug | asynq 调试", args)
}

// Info logs at info level. | Info 以 info 级别记录。
func (a asynqLogger) Info(args ...interface{}) {
	a.log(logx.InfoLevel, "asynq info | asynq 信息", args)
}

// Warn logs at warn level. | Warn 以 warn 级别记录。
func (a asynqLogger) Warn(args ...interface{}) {
	a.log(logx.WarnLevel, "asynq warn | asynq 警告", args)
}

// Error logs at error level. | Error 以 error 级别记录。
func (a asynqLogger) Error(args ...interface{}) {
	a.log(logx.ErrorLevel, "asynq error | asynq 错误", args)
}

// Fatal logs at error level instead of terminating the process. | Fatal 以 error 级别记录，而不终止进程。
func (a asynqLogger) Fatal(args ...interface{}) {
	a.log(logx.ErrorLevel, "asynq fatal | asynq 致命错误", args)
}

// log writes one asynq record through logx, skipping the work at a disabled level. | log 通过 logx 写出一条 asynq 记录，级别被禁用时跳过全部工作。
func (a asynqLogger) log(lvl logx.Level, msg string, args []interface{}) {
	if a.l == nil || !a.l.Enabled(lvl) {
		return
	}
	f := logx.Str("args", fmt.Sprint(args...))
	switch lvl {
	case logx.DebugLevel:
		a.l.Debug(msg, f)
	case logx.WarnLevel:
		a.l.Warn(msg, f)
	case logx.ErrorLevel:
		a.l.Error(msg, f)
	default:
		a.l.Info(msg, f)
	}
}

// resolveRedisOpt fills the connection parameters r leaves unset from o and validates the result. | resolveRedisOpt 用 o 补齐 r 未设置的连接参数，并校验结果。
//
// The caller-provided option wins whenever it carries a value; the fallback exists so a service | 调用方提供的参数一旦有值即优先；兜底逻辑使服务
// can be configured from the asynq section alone, without building a RedisClientOpt by hand. | 仅凭 asynq 配置段即可运行，无需手工拼装 RedisClientOpt。
func resolveRedisOpt(r asynq.RedisClientOpt, o Options) (asynq.RedisClientOpt, error) {
	addr := strings.TrimSpace(r.Addr)
	if addr == "" {
		addr = strings.TrimSpace(o.RedisAddr)
	}
	if addr == "" {
		return r, ErrMissingRedisAddr
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return r, errx.WrapDetail(err, ErrInvalidRedisAddr, "%q", addr)
	}
	r.Addr = addr
	if r.Password == "" {
		r.Password = o.RedisPassword
	}
	if r.DB == 0 {
		r.DB = o.RedisDB
	}
	return r, nil
}

// newTask builds an asynq task, adding headers only when there is something to carry. | newTask 构建 asynq 任务，仅在有内容需要携带时才添加任务头。
func newTask(taskType string, raw []byte, headers map[string]string) *asynq.Task {
	if len(headers) == 0 {
		return asynq.NewTask(taskType, raw)
	}
	return asynq.NewTaskWithHeaders(taskType, raw, headers)
}

// traceHeaders returns the task headers derived from ctx. | traceHeaders 返回由 ctx 派生的任务头。
//
// A trace id is the only cross-process value the framework propagates today; returning nil | 链路 id 是框架目前唯一跨进程传递的值；无链路 id 时返回 nil，
// when there is none keeps the common case allocation-free. | 使常见路径不产生额外分配。
func traceHeaders(ctx context.Context) map[string]string {
	if id := TraceIDFrom(ctx); id != "" {
		return map[string]string{headerTraceID: id}
	}
	return nil
}

// withOption returns opts plus extra without aliasing the caller's slice. | withOption 返回 opts 与 extra 的组合，且不改动调用方切片。
func withOption(opts []asynq.Option, extra asynq.Option) []asynq.Option {
	out := make([]asynq.Option, 0, len(opts)+1)
	out = append(out, opts...)
	return append(out, extra)
}

// withDefaults returns opts preceded by the framework defaults. | withDefaults 返回前置框架默认选项后的 opts。
//
// The caller's options stay last on purpose: asynq applies options in order, so an explicit | 调用方选项刻意留在末尾：asynq 按顺序应用选项，
// asynq.Retention always wins over defaultRetention. | 因此显式的 asynq.Retention 总能覆盖 defaultRetention。
func withDefaults(opts []asynq.Option) []asynq.Option {
	out := make([]asynq.Option, 0, len(opts)+1)
	out = append(out, asynq.Retention(defaultRetention))
	return append(out, opts...)
}

// encodePayload serializes a task payload with jsonx. | encodePayload 用 jsonx 序列化任务载荷。
func encodePayload(v any) ([]byte, error) {
	raw, err := jsonx.Marshal(v)
	if err != nil {
		return nil, errx.Wrap(err, ErrEncodeFailed)
	}
	return raw, nil
}

// decodePayload parses a task body into a Payload; an empty body yields nil without error. | decodePayload 把任务载荷解析为 Payload；空载荷返回 nil 且不报错。
//
// A body that is not a JSON object reports ErrDecodeFailed: the task fails visibly instead of | 不是 JSON 对象的载荷返回 ErrDecodeFailed：任务显式失败，而不是让处理器拿到半个空表继续跑。
// handing the handler a half-empty map to run with.
func decodePayload(raw []byte) (Payload, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var p Payload
	if err := jsonx.Unmarshal(raw, &p); err != nil {
		return nil, errx.Wrap(err, ErrDecodeFailed)
	}
	return p, nil
}
