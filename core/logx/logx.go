// Package logx is the yd framework's zero-copy, zero-reflection structured logging system. | Package logx 是 yd 框架的零拷贝、零反射结构化日志系统。
//
// Design highlights: | 设计要点：
//
//   - Encoding appends directly onto byte buffers reused from sync.Pool (1KB initially; buffers above 64KB are not returned to the pool), |   - 编码直接在 sync.Pool 复用的字节缓冲（初始 1KB，超过 64KB 不回池）上 append，
//     and every log record calls io.Writer.Write exactly once, so concurrent writes from many goroutines never interleave lines; |     每条日志只调用一次 io.Writer.Write，因此多 goroutine 并发写入不会产生交错行；
//   - Fields are carried in a union representation (Field); the hot path does no boxing, no reflection and creates no intermediate strings, |   - 字段以联合表示（Field）承载，热路径不装箱、不反射、不产生中间 string，
//     numbers are written straight into the buffer through strconv.AppendXxx, and strings are escaped in place according to JSON rules; |     数值一律经 strconv.AppendXxx 直写缓冲，字符串按 JSON 规则就地转义；
//   - Callers can hoist the level check with Enabled, avoiding field construction at disabled levels; |   - 调用方可用 Enabled 把级别判断前置，避免在禁用级别上构造字段；
//     the disabled path allocates nothing (guaranteed by the allocation guard in BenchmarkDisabledLevel); |     禁用路径零分配（由 BenchmarkDisabledLevel 的分配护栏保证）；
//   - Every record carries the process id (pid) and, when the platform reports one, the host name, both captured once in New; |   - 每条记录都带进程号（pid），平台能提供时还带主机名（host），两者都在 New 时取一次并缓存；
//   - Times are always output as UTC, with default layouts of millisecond-precision RFC3339 (JSON) and "15:04:05.000" (console); |   - 时间一律按 UTC 输出，默认布局为毫秒精度 RFC3339（JSON）与 "15:04:05.000"（console）；
//   - time.Duration is output as milliseconds (float64) in JSON and as a human-readable string in console; |   - time.Duration 在 JSON 中输出毫秒数（float64），在 console 中输出人类可读字符串；
//   - []byte fields are output as base64 (RFC 4648 standard encoding), matching the encoding/json convention for []byte; |   - []byte 字段按 base64（RFC 4648 标准编码）输出，与 encoding/json 对 []byte 的约定一致；
//   - Any is the slow path: known types are normalized in place to reflection-free representations, and unknown types fall back to encoding/json; |   - Any 为慢路径：已知类型就地归一为无反射表示，未知类型回退 encoding/json；
//   - Async is opt-in: producers encode locally and a dedicated writer goroutine coalesces whole records into batched writes, |   - Async 为显式开启：生产者在本地完成编码，专用写协程把完整记录合并为批量写，
//     with a bounded queue and blocking backpressure; the default remains one synchronous Write per record. |     队列有界且采用阻塞背压；默认仍为每条记录一次同步 Write。
//
// Basic usage: | 基本用法：
//
//	log := logx.New(logx.Options{Service: "api", Level: logx.InfoLevel})
//	log.Info("server started", logx.Str("addr", ":8080"), logx.Int("pid", os.Getpid()))
//	reqLog := log.With(logx.Str("request_id", id))
package logx

import (
	"io"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// formatConsole is the console-format value of Options.Format; | formatConsole 是 Options.Format 中 console 格式的取值；
	// "json" and any unrecognized value use JSON encoding. | "json" 及任何无法识别的取值都走 JSON 编码。
	formatConsole = "console"

	// defaultJSONTimeFormat is the default time layout for the JSON format (UTC, millisecond-precision RFC3339). | defaultJSONTimeFormat 是 JSON 格式的默认时间布局（UTC 毫秒精度 RFC3339）。
	defaultJSONTimeFormat = "2006-01-02T15:04:05.000Z07:00"
	// defaultConsoleTimeFormat is the default time layout for the console format (UTC, millisecond precision). | defaultConsoleTimeFormat 是 console 格式的默认时间布局（UTC 毫秒精度）。
	defaultConsoleTimeFormat = "15:04:05.000"

	// defaultExitCode is the default exit code Fatal passes to ExitFunc. | defaultExitCode 是 Fatal 调用 ExitFunc 时使用的默认退出码。
	defaultExitCode = 1

	// PipeBufSize is the POSIX PIPE_BUF atomic-write boundary (4096 bytes on Linux). | PipeBufSize 是 POSIX PIPE_BUF 的原子写边界（Linux 为 4096 字节）。
	//
	// Writes of at most this many bytes to a pipe are atomic on POSIX systems; longer writes may | 在 POSIX 系统上，写入管道的单次写不超过该字节数时是原子的；更长的写可能
	// be split by the kernel and interleave with writes from other processes. This requires the record | 被内核拆分并与其他进程的写交错。这要求整条记录（同步模式）或整个批次（异步模式）
	// (sync mode) or batch (async mode) to stay within the boundary when the destination is a pipe. | 在目的地为管道时保持在该边界内。
	PipeBufSize = 4096
)

// Options configures a Logger. Every field may be left at its zero value, and the zero value means the default configuration. | Options 配置 Logger。所有字段都可留零值，零值即默认配置。
type Options struct {
	// Writer is the log output destination; nil means os.Stdout. | Writer 是日志输出目标；nil 表示 os.Stdout。
	//
	// Concurrent use is serialized per Logger family (core), but not across separately created | 同一个 Logger 家族（core）内的并发使用会被串行化，但分别创建的
	// Loggers: two Loggers writing to the same Writer have independent locks. When several processes | Logger 之间不会：两个写同一 Writer 的 Logger 各有独立锁。多个进程写同一个
	// append to one file, open it with OpenFile (O_APPEND) so every Write lands at the current end; | 文件时，用 OpenFile（O_APPEND）打开，使每次 Write 都落在当前末尾；
	// when the destination is a pipe, keep each record (and, in async mode, each batch) within | 目的地为管道时，让每条记录（异步模式下是每个批次）保持在
	// PipeBufSize, because POSIX only makes writes up to PIPE_BUF atomic across processes. | PipeBufSize 以内，因为 POSIX 只保证不超过 PIPE_BUF 的写在进程间是原子的。
	Writer io.Writer
	// ExitFunc is the function called after Fatal writes its log record, meant to terminate the process; nil means os.Exit. | ExitFunc 是 Fatal 记录日志之后调用的函数，用于结束进程；nil 表示 os.Exit。
	// Tests can replace it in order to observe the exit code. | 测试可替换它以观察退出码。
	ExitFunc func(int)
	// Format is the output format: "json" (the default) or "console"; any other value is treated as "json". | Format 是输出格式："json"（默认）或 "console"；其它取值按 "json" 处理。
	Format string
	// TimeFormat is the Go reference time layout of the timestamp; empty means taking the default layout for the Format | TimeFormat 是时间戳的 Go 参考时间布局；空表示按 Format 取默认布局
	// (millisecond-precision RFC3339 for JSON, "15:04:05.000" for console), and times are always rendered as UTC. | （JSON 为毫秒精度 RFC3339，console 为 "15:04:05.000"），时间一律按 UTC 渲染。
	// A custom layout must not contain '"', '\\' or control characters, otherwise the output will not be valid JSON. | 自定义布局中不得出现 '"'、'\\' 或控制字符，否则输出将不是合法 JSON。
	TimeFormat string
	// Service is the service name; when non-empty it is written to the service field of every log record. | Service 是服务名；非空时写入每条日志的 service 字段。
	Service string
	// Level is the log level threshold; calls below it are discarded, and the zero value is InfoLevel. | Level 是日志级别阈值，低于该级别的调用被丢弃；零值即 InfoLevel。
	Level Level
	// When Caller is true every log record carries the call site (of the form file.go:123, with the file name only). | Caller 为 true 时每条日志附带调用位置（形如 file.go:123，只含文件名）。
	Caller bool
	// ConcurrentWrites drops the per-record write lock and calls Writer.Write directly. | ConcurrentWrites
	// 去掉逐记录的写锁，直接调用 Writer.Write。
	//
	// Only enable this when the Writer itself serializes concurrent Write calls: *os.File does
	// (Go's poll.FD takes a write lock, and with O_APPEND the kernel appends each write
	// contiguously), while bytes.Buffer, bufio.Writer and most wrappers do not. The Logger's
	// guarantee that a record never interleaves with another then depends on the Writer's own
	// atomicity: on Linux, an O_APPEND *os.File keeps every write contiguous; a plain offset-based
	// file does not. The Writer must also tolerate Sync being called while such writes are in
	// flight. This mode is ignored in async mode, whose writer goroutine is already the only caller. | 仅当
	// Writer 自身串行化并发 Write 时才应启用：*os.File 满足（Go 的 poll.FD 内部有写锁，且带 O_APPEND
	// 时内核会把每次写的字节连续追加），而 bytes.Buffer、bufio.Writer 与大多数包装器不满足。此后
	// 「记录不会相互交错」的保证取决于 Writer 自身的原子性：在 Linux 上带 O_APPEND 的 *os.File 保证
	// 每次写的字节连续，普通按偏移写入的文件则不保证。Writer 还必须能承受在写入进行时被调用 Sync。
	// 异步模式下该选项被忽略，因为写协程本来就是唯一调用者。
	ConcurrentWrites bool
	// Async enables the optional asynchronous, batched writer. | Async 启用可选的异步批量写入器。
	//
	// When Async is false (the default) every log call encodes into a pooled buffer and performs one | Async 为 false（默认）时，每次日志调用在池化缓冲中编码后，在锁内执行一次
	// synchronous Write under the lock before returning; this is the historical semantics and is kept | 同步 Write 才返回；这是既有语义，完整保留。
	// intact. When Async is true, log calls encode into a pooled buffer and enqueue it, returning before | Async 为 true 时，日志调用在池化缓冲中编码后入队，在写协程完成实际写出之前就返回；
	// the record has been written; a dedicated writer goroutine drains a bounded queue and coalesces | 专用写协程排空有界队列，把完整记录合并成批次，每批执行一次 Write。
	// whole records into one Write per batch. Each record is still whole and never interleaved with | 每条记录仍完整，不与其他记录交错；
	// another; the durability semantics change: returning from a log call no longer means the record | 但持久性语义改变：日志调用返回不再代表记录已写入 Writer。
	// reached the Writer. Call Sync when that must be guaranteed, and call Close before exiting (Fatal | 需要确保已写出时调用 Sync；进程退出前必须调用 Close（Fatal 会自动先 Sync）。
	// flushes automatically). When the queue is full producers block (backpressure); records are never | 队列满时生产者阻塞等待（背压），记录不会被丢弃。
	// dropped. Close is mandatory in async mode, otherwise the writer goroutine stays alive. | 异步模式下必须调用 Close，否则写协程会一直存活。
	// Record integrity and completeness are guaranteed, but call order is not: the writer drains | 记录的完整性与不丢失有保证，但调用顺序不保证：写协程排空多个独立的环，
	// several independent rings, so records from one goroutine may be written in a different | 因此同一 goroutine 的记录可能以不同于调用顺序的次序写出。需要严格顺序时
	// order than they were logged. Keep Async false when strict ordering is required. | 保持 Async 为 false。
	Async bool
	// QueueSize is the total number of records the async queue can hold before producers are blocked | QueueSize 是异步队列在生产者被阻塞（背压）之前能容纳的记录总数；
	// (backpressure); zero means 8192. The value is rounded up to a power of two per ring and clamped | 零表示 8192。该值按每个环向上取整为 2 的幂，并夹紧到总计
	// to [1024, 262144] records in total. It is ignored when Async is false. | [1024, 262144] 条。Async 为 false 时忽略。
	QueueSize int
	// BatchBytes is the maximum number of bytes coalesced into one Write in async mode; zero means | BatchBytes 是异步模式下合并进一次 Write 的最大字节数；零表示
	// 64 KiB. It is ignored when Async is false. | 64 KiB。Async 为 false 时忽略。
	// When the destination is a pipe, set BatchBytes to at most PipeBufSize so each batch stays within | 目的地为管道时，把 BatchBytes 设为不超过 PipeBufSize，使每个批次保持在
	// the POSIX atomic-write boundary; a single record larger than BatchBytes is written on its own and | POSIX 原子写边界内；单条记录大于 BatchBytes 时单独写出，
	// is therefore only atomic up to the destination's own limits. | 因此只在目的地自身的限制内保持原子。
	BatchBytes int
}

// core is the write state shared by one Logger family (the derivation chain of With/Named). | core 是同一 Logger 家族（With/Named 派生链）共享的写入状态。
//
// The level threshold and the write mutex are separated by cache-line padding on purpose: the
// level is read on every call from every core, while the mutex line is written per record by
// whichever goroutine holds it. Sharing a line would make every level load miss in the caches
// of the other cores — the dominant scalability limit of the synchronous path. | 级别阈值与写互斥锁
// 刻意用缓存行填充隔开：级别在每个核心的每次调用中都会被读取，而互斥锁所在行会由持锁协程逐记录写入。
// 二者共享一行会让其它核心的每次级别读取都缓存未命中——这正是同步路径最主要的扩展性瓶颈。
type core struct { //betteralign:ignore
	w     io.Writer // Output destination, never changed after creation. | 输出目标，创建后不再变更
	async *asyncState
	// concurrentWrites is read-only after creation; it lives beside w so the hot path reads one line. | concurrentWrites 创建后只读；与 w 相邻，使热路径只读一行。
	concurrentWrites bool
	_                cacheLinePad
	mu               sync.Mutex // Serializes writes so that a single line is never torn apart under concurrency. | 串行化写入，保证并发时单行不被拆散
	_                cacheLinePad
	// level is the lock-free threshold read on the hot path. | level 是热路径无锁读取的级别阈值。
	level atomic.Int32
	_     cacheLinePad
}

// Logger is a zero-copy structured logger that is safe to share across goroutines as a value (pass it by pointer). | Logger 是零拷贝结构化日志器，可作为值安全地跨 goroutine 共享（应以指针传递）。
//
// Child Loggers derived by With/Named share the writer, mutex, buffer pool and level threshold with the parent, | With/Named 派生出的子 Logger 与父级共享 writer、互斥锁、缓冲池与级别阈值，
// but their fields do not affect each other: appending fields to a child Logger never changes the parent Logger. | 但字段互不影响：向子 Logger 追加字段不会改变父 Logger。
type Logger struct {
	core       *core
	exit       func(int)
	name       string
	service    string
	host       string
	timeFormat string
	// The members below are pre-encoded at construction, so a record never re-escapes them. | 以下成员在构造期预编码，记录写出时不再重复转义。
	serviceJSON string
	nameJSON    string
	hostJSON    string
	pidStr      string
	fields      []Field // Persistent fields accumulated by With. | With 累积的持久字段
	caller      bool
	console     bool // true means console format. | true 表示 console 格式
}

// New creates a Logger from o. | New 依据 o 创建 Logger。
//
// Defaults: Writer is os.Stdout, Level is InfoLevel, Format is "json" and | 默认值：Writer 为 os.Stdout、Level 为 InfoLevel、Format 为 "json"、
// ExitFunc is os.Exit. | ExitFunc 为 os.Exit。
// The process id and host name are captured here once, never on the logging hot path; when the | 进程号与主机名在这里取一次，日志热路径不再获取；平台无法提供主机名时
// platform cannot report a host name the host field is omitted from every record. | 主机名为空，所有记录都省略 host 字段。
// When o.Async is true the dedicated writer goroutine starts here; the caller must eventually call Close. | o.Async 为 true 时专用写协程在此启动；调用方最终必须调用 Close。
func New(o Options) *Logger {
	w := o.Writer
	if w == nil {
		w = os.Stdout
	}
	exit := o.ExitFunc
	if exit == nil {
		exit = os.Exit
	}
	c := &core{w: w, concurrentWrites: o.ConcurrentWrites}
	c.level.Store(int32(o.Level))

	host, _ := os.Hostname()
	l := &Logger{core: c, exit: exit, service: o.Service, caller: o.Caller, timeFormat: o.TimeFormat, pidStr: strconv.Itoa(os.Getpid()), host: host}
	l.serviceJSON = jsonStringMember("service", l.service)
	l.nameJSON = jsonStringMember("logger", l.name)
	l.hostJSON = jsonStringMember("host", l.host)
	if o.Format == formatConsole {
		l.console = true
		if l.timeFormat == "" {
			l.timeFormat = defaultConsoleTimeFormat
		}
	} else if l.timeFormat == "" {
		l.timeFormat = defaultJSONTimeFormat
	}
	if o.Async {
		c.startAsync(o.QueueSize, o.BatchBytes)
	}
	return l
}

// defaultLogger holds the global default Logger. | defaultLogger 保存全局默认 Logger。
var defaultLogger atomic.Pointer[Logger]

func init() {
	defaultLogger.Store(New(Options{}))
}

// Default returns the global default Logger: it writes to os.Stdout, uses JSON format and InfoLevel. | Default 返回全局默认 Logger：写入 os.Stdout、JSON 格式、InfoLevel。
func Default() *Logger { return defaultLogger.Load() }

// SetDefault replaces the global default Logger; the call is ignored when l is nil. | SetDefault 替换全局默认 Logger；l 为 nil 时忽略该调用。
func SetDefault(l *Logger) {
	if l != nil {
		defaultLogger.Store(l)
	}
}

// Enabled reports whether lvl reaches the current threshold. | Enabled 报告 lvl 是否达到当前阈值。
//
// Calling it before constructing fields makes calls at a disabled level completely allocation-free: | 在构造字段之前调用它可让禁用级别的调用完全零分配：
//
//	if log.Enabled(logx.DebugLevel) {
//		log.Debug("dump", logx.Str("body", expensiveCall()))
//	}
func (l *Logger) Enabled(lvl Level) bool {
	return lvl >= Level(l.core.level.Load())
}

// SetLevel sets the level threshold. One Logger family (the child Loggers derived by With/Named) shares that threshold. | SetLevel 设置级别阈值。同一 Logger 家族（With/Named 派生出的子 Logger）共享该阈值。
func (l *Logger) SetLevel(lvl Level) {
	l.core.level.Store(int32(lvl))
}

// Debug logs at DebugLevel; it returns immediately when below the threshold. | Debug 以 DebugLevel 记录日志；低于阈值时立即返回。
func (l *Logger) Debug(msg string, fields ...Field) { l.log(DebugLevel, msg, fields) }

// Info logs at InfoLevel; it returns immediately when below the threshold. | Info 以 InfoLevel 记录日志；低于阈值时立即返回。
func (l *Logger) Info(msg string, fields ...Field) { l.log(InfoLevel, msg, fields) }

// Warn logs at WarnLevel; it returns immediately when below the threshold. | Warn 以 WarnLevel 记录日志；低于阈值时立即返回。
func (l *Logger) Warn(msg string, fields ...Field) { l.log(WarnLevel, msg, fields) }

// Error logs at ErrorLevel; it returns immediately when below the threshold. | Error 以 ErrorLevel 记录日志；低于阈值时立即返回。
func (l *Logger) Error(msg string, fields ...Field) { l.log(ErrorLevel, msg, fields) }

// Fatal logs at FatalLevel, flushes pending records and then calls Options.ExitFunc (os.Exit(1) by default). | Fatal 以 FatalLevel 记录日志，先冲刷待写记录，随后调用 Options.ExitFunc（默认 os.Exit(1)）。
//
// If a custom ExitFunc does not terminate the process, Fatal returns normally; the caller should then handle the rest of the flow itself. | 自定义 ExitFunc 若不结束进程，Fatal 会正常返回；此时调用方应自行处理后续流程。
func (l *Logger) Fatal(msg string, fields ...Field) {
	l.log(FatalLevel, msg, fields)
	_ = l.Sync()
	l.exit(defaultExitCode)
}

// With returns a derived child Logger: fields are appended to the end of that child Logger's persistent fields | With 返回派生的子 Logger：fields 追加在该子 Logger 的持久字段末尾，
// and take effect for every subsequent log record, without affecting the parent Logger (immutable semantics). | 对之后每条日志生效，且不会影响父 Logger（不可变语义）。
//
// The child Logger shares the writer, mutex, buffer pool and level threshold with the parent. | 子 Logger 与父级共享 writer、互斥锁、缓冲池和级别阈值。
func (l *Logger) With(fields ...Field) *Logger {
	child := *l // Copies only the configuration and the shared pointers; core (including its lock) stays the same instance | 只复制配置与共享指针，core（含锁）仍为同一实例
	if len(fields) == 0 {
		return &child
	}
	merged := make([]Field, 0, len(l.fields)+len(fields))
	merged = append(merged, l.fields...)
	merged = append(merged, fields...)
	child.fields = merged
	return &child
}

// Named returns a child Logger carrying a name, which is written to the "logger" field of every log record. | Named 返回带上名称的子 Logger，名称以 "logger" 字段写入每条日志。
// When the parent already has a name they are joined with "." (Named("http").Named("server") → "http.server"); | 父级已有名称时以 "." 连接（Named("http").Named("server") → "http.server"）；
// an empty name returns an equivalent child Logger. | name 为空时返回等价的子 Logger。
func (l *Logger) Named(name string) *Logger {
	child := *l
	switch {
	case name == "":
	case child.name == "":
		child.name = name
	default:
		child.name = child.name + "." + name
	}
	child.nameJSON = jsonStringMember("logger", child.name)
	return &child
}

// Sync flushes the underlying writer: in the default synchronous mode every record has already been written, so this only calls | Sync 刷新底层写入器：默认同步模式下所有记录都已写出，因此这里只调用
// the writer's Sync() error method when it has one (such as *os.File) and returns its error; otherwise it returns nil. | 写入器实现的 Sync() error 方法（如 *os.File）并返回其错误；没有实现时返回 nil。
//
// In async mode Sync first waits until every record enqueued before this call has been written and the | 异步模式下，Sync 先等待本次调用之前入队的所有记录写出、
// current batch has been flushed, and then calls the writer's Sync method; records enqueued concurrently | 当前批次冲刷完毕，再调用写入器的 Sync 方法；与此并发的入队记录
// by other goroutines may or may not be included. After Close, Sync returns nil. | 可能包含也可能不包含。Close 之后 Sync 返回 nil。
func (l *Logger) Sync() error {
	if a := l.core.async; a != nil {
		return l.syncAsync(a)
	}
	return l.core.syncWriter()
}

// Close stops the optional async writer goroutine after draining and flushing every record | Close 在排空并冲刷所有已入队记录之后停止可选的异步写协程，
// enqueued before the call, and then calls the writer's Sync method when it has one. It is idempotent | 随后在写入器实现时调用其 Sync 方法。它是幂等的，
// and safe to call concurrently with logging and Sync. Log calls that race with Close are discarded. | 且可与日志和 Sync 并发调用；与 Close 竞争的日志调用会被丢弃。
//
// In the default synchronous mode Close is a no-op: the Logger does not own the Writer and never closes it. | 默认同步模式下 Close 是无操作：Logger 不拥有 Writer，也从不关闭它。
// Async mode requires Close (or the writer goroutine leaks), and Fatal flushes automatically before exit. | 异步模式必须调用 Close（否则写协程泄漏）；Fatal 在退出前会自动冲刷。
func (l *Logger) Close() error {
	if a := l.core.async; a != nil {
		return l.closeAsync(a)
	}
	return nil
}

// Writer returns the log output destination. | Writer 返回日志输出目标。
func (l *Logger) Writer() io.Writer { return l.core.w }

// log is the hot path for recording one log line: level check → take a pooled buffer → encode → publish. | log 是记录一条日志的热路径：级别判断 → 取池化缓冲 → 编码 → 发布。
//
// In the default synchronous mode publishing is a single Write under the lock; in async mode it is a | 默认同步模式下发布是在锁内执行一次 Write；异步模式下发布是把缓冲指针
// lock-free enqueue of the buffer pointer, and the writer goroutine owns the buffer from then on. | 无锁入队，此后缓冲由写协程接管。
// fields is only read and never retained, so the caller's variadic slice can stay on the stack— | fields 只被读取、不会被保存，因此调用方的可变参数切片可留在栈上——
// this is what makes the disabled-level path allocation-free (see BenchmarkDisabledLevel). | 这是禁用级别路径零分配的前提（见 BenchmarkDisabledLevel）。
// Write errors are ignored (the zero-copy path does not retry); use Sync when flush errors must be observed. | 写入错误被忽略（零拷贝路径不做重试），需要感知刷盘错误时使用 Sync。
func (l *Logger) log(lvl Level, msg string, fields []Field) {
	if lvl < Level(l.core.level.Load()) {
		return
	}
	var caller string
	if l.caller {
		caller = callerFileLine()
	}
	// In async mode the buffer comes from the ring that will carry it, so the writer can return
	// it to the same pool; the synchronous path keeps using the global pool. | 异步模式下缓冲取自
	// 将要承载它的那个环的池，写协程因此能把缓冲还回同一个池；同步路径仍使用全局池。
	a := l.core.async
	var ring int
	var b *buffer
	if a != nil {
		ring, b = a.getBuffer()
	} else {
		b = getBuffer()
	}
	now := time.Now()
	if l.console {
		b.buf = appendConsoleRecord(b.buf, l, lvl, now, msg, caller, fields)
	} else {
		b.buf = appendJSONRecord(b.buf, l, lvl, now, msg, caller, fields)
	}
	if a != nil {
		a.pushBuffer(ring, b)
		return
	}
	if l.core.concurrentWrites {
		// The Writer serializes concurrent writes itself; skip the second lock. | Writer 自身串行化
		// 并发写入，因此跳过第二层锁。
		_, _ = l.core.w.Write(b.buf)
	} else {
		l.core.write(b.buf)
	}
	putBuffer(b)
}

// write performs a single Write while holding the lock, so concurrent writes never interleave. | write 在锁内完成一次 Write，保证并发写入不会交错。
func (c *core) write(p []byte) {
	c.mu.Lock()
	_, _ = c.w.Write(p)
	c.mu.Unlock()
}

// SlowQuery records a slow-query log record (WarnLevel). | SlowQuery 记录一条慢查询日志（WarnLevel）。
//
// Output fields: | 输出字段：
//
//	slow_query = true
//	sql        = 原始 SQL 文本
//	rows       = 受影响/返回的行数
//	duration   = 本次耗时；JSON 中为毫秒数（float64，可含小数，例如 1.5 表示 1.5ms），
//	             console 中为人类可读字符串（例如 "1.5ms"）
//	error      = err 非 nil 时附带（经 Err 展开错误链）
//
// When l is nil or the threshold is above WarnLevel it does nothing, so callers such as gormx can instrument safely. | l 为 nil 或阈值高于 WarnLevel 时不做任何事，供 gormx 等调用方安全打点。
func SlowQuery(l *Logger, sql string, rows int64, elapsed time.Duration, err error) {
	if l == nil || !l.Enabled(WarnLevel) {
		return
	}
	const msg = "slow query | 慢查询"
	if err != nil {
		l.Warn(msg,
			Bool("slow_query", true),
			Str("sql", sql),
			Int64("rows", rows),
			Dur("duration", elapsed),
			Err(err),
		)
		return
	}
	l.Warn(msg,
		Bool("slow_query", true),
		Str("sql", sql),
		Int64("rows", rows),
		Dur("duration", elapsed),
	)
}

// callerFileLine returns the call site as "file.go:line" (keeping the file name only). | callerFileLine 返回调用点的 "file.go:行号"（只保留文件名）。
// The call chain is callerFileLine ← log ← Debug/Info/... ← caller, hence skip = 3. | 调用链为 callerFileLine ← log ← Debug/Info/... ← 使用方，故 skip = 3。
func callerFileLine() string {
	_, file, line, ok := runtime.Caller(3)
	if !ok {
		return ""
	}
	for i := len(file) - 1; i > 0; i-- {
		if file[i] == '/' || file[i] == '\\' {
			file = file[i+1:]
			break
		}
	}
	var b [24]byte
	nb := strconv.AppendInt(b[:0], int64(line), 10)
	return file + ":" + string(nb)
}

// ---------- Shared time formatting primitives ---------- | ---------- 共享时间格式化原语 ----------

// appendTime appends t to dst using layout (always UTC). | appendTime 将 t 按 layout 追加到 dst（一律 UTC）。
// It takes an allocation-free fast path when a default layout is used, and otherwise falls back to time.Time.AppendFormat. | 命中默认布局时走无分配快速路径，否则退回 time.Time.AppendFormat。
func appendTime(dst []byte, t time.Time, layout string) []byte {
	switch layout {
	case defaultJSONTimeFormat:
		return appendUTCMillis(dst, t)
	case defaultConsoleTimeFormat:
		return appendUTCTimeClock(dst, t)
	}
	return t.UTC().AppendFormat(dst, layout)
}

// appendUTCMillis appends a timestamp of the form "2006-01-02T15:04:05.000Z" (UTC). | appendUTCMillis 追加 "2006-01-02T15:04:05.000Z" 形式的时间戳（UTC）。
func appendUTCMillis(dst []byte, t time.Time) []byte {
	t = t.UTC()
	y, mo, d := t.Date()
	h, mi, s := t.Clock()
	dst = appendYear(dst, y)
	dst = append(dst, '-')
	dst = appendTwoDigits(dst, int(mo))
	dst = append(dst, '-')
	dst = appendTwoDigits(dst, d)
	dst = append(dst, 'T')
	dst = appendTwoDigits(dst, h)
	dst = append(dst, ':')
	dst = appendTwoDigits(dst, mi)
	dst = append(dst, ':')
	dst = appendTwoDigits(dst, s)
	dst = append(dst, '.')
	ms := t.Nanosecond() / int(time.Millisecond)
	dst = append(dst, byte('0'+ms/100), byte('0'+ms/10%10), byte('0'+ms%10))
	return append(dst, 'Z')
}

// appendUTCTimeClock appends a timestamp of the form "15:04:05.000" (UTC). | appendUTCTimeClock 追加 "15:04:05.000" 形式的时间戳（UTC）。
func appendUTCTimeClock(dst []byte, t time.Time) []byte {
	t = t.UTC()
	h, mi, s := t.Clock()
	dst = appendTwoDigits(dst, h)
	dst = append(dst, ':')
	dst = appendTwoDigits(dst, mi)
	dst = append(dst, ':')
	dst = appendTwoDigits(dst, s)
	dst = append(dst, '.')
	ms := t.Nanosecond() / int(time.Millisecond)
	dst = append(dst, byte('0'+ms/100), byte('0'+ms/10%10), byte('0'+ms%10))
	return dst
}

// twoDigits is the lookup table "00".."99". | twoDigits 是 "00".."99" 的查表字符串。
const twoDigits = "00010203040506070809" +
	"10111213141516171819" +
	"20212223242526272829" +
	"30313233343536373839" +
	"40414243444546474849" +
	"50515253545556575859" +
	"60616263646566676869" +
	"70717273747576777879" +
	"80818283848586878889" +
	"90919293949596979899"

// appendTwoDigits appends v as exactly two digits (00..99). | appendTwoDigits 把 v 追加为恰好两位数字（00..99）。
func appendTwoDigits(dst []byte, v int) []byte {
	if v >= 0 && v < 100 {
		return append(dst, twoDigits[v*2], twoDigits[v*2+1])
	}
	return appendPad(dst, v, 2)
}

// appendYear appends year as exactly four digits when it fits, with a generic fallback otherwise.
// | appendYear 在年份可容纳时追加恰好四位数字，否则退回通用实现。
func appendYear(dst []byte, y int) []byte {
	if y >= 0 && y < 10000 {
		return append(dst, byte('0'+y/1000), byte('0'+y/100%10), byte('0'+y/10%10), byte('0'+y%10))
	}
	return appendPad(dst, y, 4)
}

// appendPad appends the non-negative integer v padded with '0' to the given width. | appendPad 以 width 宽度（不足补 '0'）追加非负整数 v。
func appendPad(dst []byte, v, width int) []byte {
	// Date and time components use widths 1-4; writing the digits straight into dst avoids
	// strconv.AppendInt's generic path on every log record. | 日期与时间分量使用 1-4 的宽度；
	// 把数字直接写进 dst，避免每条日志都走 strconv.AppendInt 的通用路径。
	if width >= 1 && width <= 4 && v >= 0 && v < smallPow10[width] {
		start := len(dst)
		for i := 0; i < width; i++ {
			dst = append(dst, '0')
		}
		for i := start + width - 1; v > 0; i-- {
			dst[i] = byte('0' + v%10)
			v /= 10
		}
		return dst
	}
	var b [20]byte
	nb := strconv.AppendInt(b[:0], int64(v), 10)
	for i := width - len(nb); i > 0; i-- {
		dst = append(dst, '0')
	}
	return append(dst, nb...)
}

// smallPow10 holds 10^0..10^4 for the fixed-width fast path. | smallPow10 保存 10^0..10^4，供定宽快路径使用。
var smallPow10 = [5]int{1, 10, 100, 1000, 10000}
