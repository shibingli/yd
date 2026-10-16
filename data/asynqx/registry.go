package asynqx

import (
	"context"
	"strings"
	"sync"

	"github.com/yun-dev/yd/errx"
	"github.com/yun-dev/yd/logx"
)

// Payload is the decoded JSON body of a task. | Payload 是任务已解码的 JSON 载荷。
//
// It is nil for a task enqueued without a body. Following the JSON data model, numbers | 未携带载荷的任务其值为 nil。按 JSON 数据模型，数字解码为 float64，
// decode to float64, so a handler that needs a concrete numeric type converts it itself | 需要具体数值类型的处理器需自行转换（例如 int(p["count"].(float64))）。
// (for example int(p["count"].(float64))).
type Payload map[string]any

// Handler handles one task type. | Handler 处理一种任务类型。
//
// Returning a non-nil error makes asynq retry the task until MaxRetry, after which the task | 返回非 nil 错误会让 asynq 重试任务直到 MaxRetry，之后任务进入失败归档；
// is archived; returning nil marks it completed. A panic is recovered by the server, logged | 返回 nil 则标记完成。panic 由服务端恢复、记日志，
// and returned as an error wrapping ErrHandlerPanic, so a panic behaves like a failure and is | 并以包装了 ErrHandlerPanic 的错误返回，因此 panic 等价于失败并会被重试。
// retried as well.
type Handler func(context.Context, Payload) error

// Registry is the declarative task-type table shared by clients and servers. | Registry 是客户端与服务端共享的声明式任务类型表。
//
// Build it once at startup (typically in a provider) and hand it to both NewClient and | 启动期构建一次（通常放在 Provider 中），再同时交给 NewClient 与 NewServer。
// NewServer. It is safe for concurrent use: reads take the read lock, Register takes the | 它可安全并发使用：读取取读锁，Register 取写锁。
// write lock.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	err      error
}

// NewRegistry returns an empty registry. | NewRegistry 返回空注册表。
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

// Register declares the handler of taskType and returns the registry for chaining. | Register 声明 taskType 的处理器，并返回注册表以支持链式调用。
//
// An empty task type or a nil handler is not stored; the first such problem is kept and | 空任务类型或 nil 处理器不会被登记：首个此类问题会被保留下来，
// reported by Err (surfaced by Server.Start / NewClient at startup). Register never panics, | 由 Err 上报（由 Server.Start / NewClient 在启动期暴露）。Register 不 panic，
// so registration can stay part of a declarative wiring block. Registering the same type | 因此注册可以留在声明式装配代码里。重复注册同一类型会覆盖先前的处理器。
// again replaces the previous handler.
func (r *Registry) Register(taskType string, h Handler) *Registry {
	if r == nil {
		return r
	}
	name := strings.TrimSpace(taskType)
	if name == "" || h == nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.err == nil {
			if name == "" {
				r.err = ErrEmptyTaskType
			} else {
				r.err = errx.Detail(ErrNilHandler, "%q", name)
			}
		}
		return r
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handlers == nil {
		r.handlers = make(map[string]Handler)
	}
	r.handlers[name] = h
	return r
}

// Handlers returns a snapshot copy of the registered handlers. | Handlers 返回已注册处理器的快照副本。
//
// The copy is defensive: a caller may walk or mutate it without affecting the registry. | 该副本是防御性的：调用方可以遍历或修改它而不影响注册表。
func (r *Registry) Handlers() map[string]Handler {
	out := make(map[string]Handler)
	if r == nil {
		return out
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, h := range r.handlers {
		out[name] = h
	}
	return out
}

// Err returns the first problem recorded by Register; nil when every registration was valid. | Err 返回 Register 记录的首个问题；全部注册合法时为 nil。
func (r *Registry) Err() error {
	if r == nil {
		return ErrNilRegistry
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.err
}

// lookup returns the handler of taskType; ok is false when the type is not registered. | lookup 返回 taskType 的处理器；未注册时 ok 为 false。
func (r *Registry) lookup(taskType string) (Handler, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[strings.TrimSpace(taskType)]
	return h, ok
}

// TaskContext is the read-only description of the task a handler is processing. | TaskContext 是处理器正在处理任务的只读描述。
//
// It is injected by the server; a handler reads it with FromContext. | 它由服务端注入，处理器通过 FromContext 读取。
type TaskContext struct {
	// TaskID is the asynq task id, unique and stable across retries. | TaskID 是 asynq 任务 id，唯一且重试期间保持不变。
	TaskID string
	// Type is the registered task type. | Type 是已注册的任务类型。
	Type string
	// Queue is the queue the task was pulled from. | Queue 是任务被取出的队列。
	Queue string
	// RetryCount is the number of retries so far; 0 on the first run. | RetryCount 是已重试次数；首次执行时为 0。
	RetryCount int
	// MaxRetry is the maximum number of retries configured for the task. | MaxRetry 是任务配置的最大重试次数。
	MaxRetry int
	// TraceID is the trace id carried in the task headers; empty when the producer sent none. | TraceID 是任务头中携带的链路 id；生产端未提供时为空。
	TraceID string
}

// headerTraceID is the task header key that carries the trace id across process hops. | headerTraceID 是跨进程传递链路 id 的任务头键。
//
// The key is part of the wire format: a producer in another language reaches the same | 该键属于线上格式：其它语言的客户端只要写入同样的头，处理器就能读到同样的链路 id。
// handler simply by writing the same header.
const headerTraceID = "trace_id"

// ctxKey is the private context key type of this package. | ctxKey 是本包私有的上下文键类型。
type ctxKey uint8

const (
	// ctxKeyTask stores the *TaskContext. | ctxKeyTask 存放 *TaskContext。
	ctxKeyTask ctxKey = iota
	// ctxKeyLogger stores the task-scoped *logx.Logger. | ctxKeyLogger 存放任务级 *logx.Logger。
	ctxKeyLogger
	// ctxKeyTraceID stores the trace id string. | ctxKeyTraceID 存放链路 id 字符串。
	ctxKeyTraceID
)

// FromContext returns the TaskContext injected by the server; nil outside a task. | FromContext 返回服务端注入的 TaskContext；不在任务中时为 nil。
func FromContext(ctx context.Context) *TaskContext {
	tc, _ := ctx.Value(ctxKeyTask).(*TaskContext)
	return tc
}

// LoggerFrom returns the task-scoped child logger injected by the server; nil outside a task. | LoggerFrom 返回服务端注入的任务级子 logger；不在任务中时为 nil。
//
// The logger already carries task_id / task_type / queue / retry_count / trace_id, so handler | 该日志器已带 task_id / task_type / queue / retry_count / trace_id 字段，
// logs can be correlated with the server's own records by those fields. | 处理器的日志可据此与服务端自身记录互相关联。
func LoggerFrom(ctx context.Context) *logx.Logger {
	l, _ := ctx.Value(ctxKeyLogger).(*logx.Logger)
	return l
}

// WithTraceID stores id as the trace id of ctx. | WithTraceID 把 id 存为 ctx 的链路 id。
//
// Pass the returned context to Client.Enqueue and the id travels in the task headers, so a | 把返回的上下文交给 Client.Enqueue，id 会随任务头传递，
// handler in another process (or another service) keeps the same trace. | 其它进程（或其它服务）的处理器仍处于同一条链路。
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyTraceID, id)
}

// TraceIDFrom returns the trace id of ctx; empty when none was set. | TraceIDFrom 返回 ctx 的链路 id；未设置时为空串。
//
// It reads what WithTraceID wrote on the producer side, and what the server injected from the | 生产端读取 WithTraceID 写入的值；服务端处理器读取的是从任务头注入的值，
// task headers on the consumer side. | 两者语义一致。
func TraceIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyTraceID).(string)
	return id
}
