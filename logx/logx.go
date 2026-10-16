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
//   - Times are always output as UTC, with default layouts of millisecond-precision RFC3339 (JSON) and "15:04:05.000" (console); |   - 时间一律按 UTC 输出，默认布局为毫秒精度 RFC3339（JSON）与 "15:04:05.000"（console）；
//   - time.Duration is output as milliseconds (float64) in JSON and as a human-readable string in console; |   - time.Duration 在 JSON 中输出毫秒数（float64），在 console 中输出人类可读字符串；
//   - []byte fields are output as base64 (RFC 4648 standard encoding), matching the encoding/json convention for []byte; |   - []byte 字段按 base64（RFC 4648 标准编码）输出，与 encoding/json 对 []byte 的约定一致；
//   - Any is the slow path: known types are normalized in place to reflection-free representations, and unknown types fall back to encoding/json. |   - Any 为慢路径：已知类型就地归一为无反射表示，未知类型回退 encoding/json。
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
)

// Options configures a Logger. Every field may be left at its zero value, and the zero value means the default configuration. | Options 配置 Logger。所有字段都可留零值，零值即默认配置。
type Options struct {
	// Writer is the log output destination; nil means os.Stdout. | Writer 是日志输出目标；nil 表示 os.Stdout。
	Writer io.Writer
	// Level is the log level threshold; calls below it are discarded, and the zero value is InfoLevel. | Level 是日志级别阈值，低于该级别的调用被丢弃；零值即 InfoLevel。
	Level Level
	// Format is the output format: "json" (the default) or "console"; any other value is treated as "json". | Format 是输出格式："json"（默认）或 "console"；其它取值按 "json" 处理。
	Format string
	// TimeFormat is the Go reference time layout of the timestamp; empty means taking the default layout for the Format | TimeFormat 是时间戳的 Go 参考时间布局；空表示按 Format 取默认布局
	// (millisecond-precision RFC3339 for JSON, "15:04:05.000" for console), and times are always rendered as UTC. | （JSON 为毫秒精度 RFC3339，console 为 "15:04:05.000"），时间一律按 UTC 渲染。
	// A custom layout must not contain '"', '\\' or control characters, otherwise the output will not be valid JSON. | 自定义布局中不得出现 '"'、'\\' 或控制字符，否则输出将不是合法 JSON。
	TimeFormat string
	// When Caller is true every log record carries the call site (of the form file.go:123, with the file name only). | Caller 为 true 时每条日志附带调用位置（形如 file.go:123，只含文件名）。
	Caller bool
	// Service is the service name; when non-empty it is written to the service field of every log record. | Service 是服务名；非空时写入每条日志的 service 字段。
	Service string
	// ExitFunc is the function called after Fatal writes its log record, meant to terminate the process; nil means os.Exit. | ExitFunc 是 Fatal 记录日志之后调用的函数，用于结束进程；nil 表示 os.Exit。
	// Tests can replace it in order to observe the exit code. | 测试可替换它以观察退出码。
	ExitFunc func(int)
}

// core is the write state shared by one Logger family (the derivation chain of With/Named). | core 是同一 Logger 家族（With/Named 派生链）共享的写入状态。
type core struct {
	mu    sync.Mutex   // Serializes writes so that a single line is never torn apart under concurrency. | 串行化写入，保证并发时单行不被拆散
	w     io.Writer    // Output destination, never changed after creation. | 输出目标，创建后不再变更
	level atomic.Int32 // Current level threshold (lock-free read, hot path). | 当前级别阈值（无锁读取，热路径）
}

// Logger is a zero-copy structured logger that is safe to share across goroutines as a value (pass it by pointer). | Logger 是零拷贝结构化日志器，可作为值安全地跨 goroutine 共享（应以指针传递）。
//
// Child Loggers derived by With/Named share the writer, mutex, buffer pool and level threshold with the parent, | With/Named 派生出的子 Logger 与父级共享 writer、互斥锁、缓冲池与级别阈值，
// but their fields do not affect each other: appending fields to a child Logger never changes the parent Logger. | 但字段互不影响：向子 Logger 追加字段不会改变父 Logger。
type Logger struct {
	core       *core
	fields     []Field // Persistent fields accumulated by With. | With 累积的持久字段
	name       string
	service    string
	timeFormat string
	caller     bool
	console    bool // true means console format. | true 表示 console 格式
	exit       func(int)
}

// New creates a Logger from o. | New 依据 o 创建 Logger。
//
// Defaults: Writer is os.Stdout, Level is InfoLevel, Format is "json" and | 默认值：Writer 为 os.Stdout、Level 为 InfoLevel、Format 为 "json"、
// ExitFunc is os.Exit. | ExitFunc 为 os.Exit。
func New(o Options) *Logger {
	w := o.Writer
	if w == nil {
		w = os.Stdout
	}
	exit := o.ExitFunc
	if exit == nil {
		exit = os.Exit
	}
	c := &core{w: w}
	c.level.Store(int32(o.Level))

	l := &Logger{core: c, exit: exit, service: o.Service, caller: o.Caller, timeFormat: o.TimeFormat}
	if o.Format == formatConsole {
		l.console = true
		if l.timeFormat == "" {
			l.timeFormat = defaultConsoleTimeFormat
		}
	} else if l.timeFormat == "" {
		l.timeFormat = defaultJSONTimeFormat
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

// Fatal logs at FatalLevel and then calls Options.ExitFunc (os.Exit(1) by default). | Fatal 以 FatalLevel 记录日志，随后调用 Options.ExitFunc（默认 os.Exit(1)）。
//
// If a custom ExitFunc does not terminate the process, Fatal returns normally; the caller should then handle the rest of the flow itself. | 自定义 ExitFunc 若不结束进程，Fatal 会正常返回；此时调用方应自行处理后续流程。
func (l *Logger) Fatal(msg string, fields ...Field) {
	l.log(FatalLevel, msg, fields)
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
	return &child
}

// Sync flushes the underlying writer: when the writer implements Sync() error (such as *os.File) it is called and its error returned, | Sync 刷新底层写入器：写入器实现 Sync() error 时（如 *os.File）调用它并返回其错误，
// otherwise it returns nil. | 否则返回 nil。
func (l *Logger) Sync() error {
	l.core.mu.Lock()
	defer l.core.mu.Unlock()
	if s, ok := l.core.w.(interface{ Sync() error }); ok {
		return s.Sync()
	}
	return nil
}

// Writer returns the log output destination. | Writer 返回日志输出目标。
func (l *Logger) Writer() io.Writer { return l.core.w }

// log is the hot path for recording one log line: level check → take a pooled buffer → encode → a single Write → return the buffer. | log 是记录一条日志的热路径：级别判断 → 取池化缓冲 → 编码 → 单次 Write → 归还缓冲。
//
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
	b := getBuffer()
	now := time.Now()
	if l.console {
		b.buf = appendConsoleRecord(b.buf, l, lvl, now, msg, caller, fields)
	} else {
		b.buf = appendJSONRecord(b.buf, l, lvl, now, msg, caller, fields)
	}
	l.core.write(b.buf)
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
	const msg = "slow query"
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
	dst = appendPad(dst, y, 4)
	dst = append(dst, '-')
	dst = appendPad(dst, int(mo), 2)
	dst = append(dst, '-')
	dst = appendPad(dst, d, 2)
	dst = append(dst, 'T')
	dst = appendPad(dst, h, 2)
	dst = append(dst, ':')
	dst = appendPad(dst, mi, 2)
	dst = append(dst, ':')
	dst = appendPad(dst, s, 2)
	dst = append(dst, '.')
	dst = appendPad(dst, t.Nanosecond()/int(time.Millisecond), 3)
	return append(dst, 'Z')
}

// appendUTCTimeClock appends a timestamp of the form "15:04:05.000" (UTC). | appendUTCTimeClock 追加 "15:04:05.000" 形式的时间戳（UTC）。
func appendUTCTimeClock(dst []byte, t time.Time) []byte {
	t = t.UTC()
	h, mi, s := t.Clock()
	dst = appendPad(dst, h, 2)
	dst = append(dst, ':')
	dst = appendPad(dst, mi, 2)
	dst = append(dst, ':')
	dst = appendPad(dst, s, 2)
	dst = append(dst, '.')
	dst = appendPad(dst, t.Nanosecond()/int(time.Millisecond), 3)
	return dst
}

// appendPad appends the non-negative integer v padded with '0' to the given width. | appendPad 以 width 宽度（不足补 '0'）追加非负整数 v。
func appendPad(dst []byte, v, width int) []byte {
	var b [20]byte
	nb := strconv.AppendInt(b[:0], int64(v), 10)
	for i := width - len(nb); i > 0; i-- {
		dst = append(dst, '0')
	}
	return append(dst, nb...)
}
