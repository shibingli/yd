// Package logx 是 yd 框架的零拷贝、零反射结构化日志系统。
//
// 设计要点：
//
//   - 编码直接在 sync.Pool 复用的字节缓冲（初始 1KB，超过 64KB 不回池）上 append，
//     每条日志只调用一次 io.Writer.Write，因此多 goroutine 并发写入不会产生交错行；
//   - 字段以联合表示（Field）承载，热路径不装箱、不反射、不产生中间 string，
//     数值一律经 strconv.AppendXxx 直写缓冲，字符串按 JSON 规则就地转义；
//   - 调用方可用 Enabled 把级别判断前置，避免在禁用级别上构造字段；
//     禁用路径零分配（由 BenchmarkDisabledLevel 的分配护栏保证）；
//   - 时间一律按 UTC 输出，默认布局为毫秒精度 RFC3339（JSON）与 "15:04:05.000"（console）；
//   - time.Duration 在 JSON 中输出毫秒数（float64），在 console 中输出人类可读字符串；
//   - []byte 字段按 base64（RFC 4648 标准编码）输出，与 encoding/json 对 []byte 的约定一致；
//   - Any 为慢路径：已知类型就地归一为无反射表示，未知类型回退 encoding/json。
//
// 基本用法：
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
	// formatConsole 是 Options.Format 中 console 格式的取值；
	// "json" 及任何无法识别的取值都走 JSON 编码。
	formatConsole = "console"

	// defaultJSONTimeFormat 是 JSON 格式的默认时间布局（UTC 毫秒精度 RFC3339）。
	defaultJSONTimeFormat = "2006-01-02T15:04:05.000Z07:00"
	// defaultConsoleTimeFormat 是 console 格式的默认时间布局（UTC 毫秒精度）。
	defaultConsoleTimeFormat = "15:04:05.000"

	// defaultExitCode 是 Fatal 调用 ExitFunc 时使用的默认退出码。
	defaultExitCode = 1
)

// Options 配置 Logger。所有字段都可留零值，零值即默认配置。
type Options struct {
	// Writer 是日志输出目标；nil 表示 os.Stdout。
	Writer io.Writer
	// Level 是日志级别阈值，低于该级别的调用被丢弃；零值即 InfoLevel。
	Level Level
	// Format 是输出格式："json"（默认）或 "console"；其它取值按 "json" 处理。
	Format string
	// TimeFormat 是时间戳的 Go 参考时间布局；空表示按 Format 取默认布局
	// （JSON 为毫秒精度 RFC3339，console 为 "15:04:05.000"），时间一律按 UTC 渲染。
	// 自定义布局中不得出现 '"'、'\\' 或控制字符，否则输出将不是合法 JSON。
	TimeFormat string
	// Caller 为 true 时每条日志附带调用位置（形如 file.go:123，只含文件名）。
	Caller bool
	// Service 是服务名；非空时写入每条日志的 service 字段。
	Service string
	// ExitFunc 是 Fatal 记录日志之后调用的函数，用于结束进程；nil 表示 os.Exit。
	// 测试可替换它以观察退出码。
	ExitFunc func(int)
}

// core 是同一 Logger 家族（With/Named 派生链）共享的写入状态。
type core struct {
	mu    sync.Mutex   // 串行化写入，保证并发时单行不被拆散
	w     io.Writer    // 输出目标，创建后不再变更
	level atomic.Int32 // 当前级别阈值（无锁读取，热路径）
}

// Logger 是零拷贝结构化日志器，可作为值安全地跨 goroutine 共享（应以指针传递）。
//
// With/Named 派生出的子 Logger 与父级共享 writer、互斥锁、缓冲池与级别阈值，
// 但字段互不影响：向子 Logger 追加字段不会改变父 Logger。
type Logger struct {
	core       *core
	fields     []Field // With 累积的持久字段
	name       string
	service    string
	timeFormat string
	caller     bool
	console    bool // true 表示 console 格式
	exit       func(int)
}

// New 依据 o 创建 Logger。
//
// 默认值：Writer 为 os.Stdout、Level 为 InfoLevel、Format 为 "json"、
// ExitFunc 为 os.Exit。
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

// defaultLogger 保存全局默认 Logger。
var defaultLogger atomic.Pointer[Logger]

func init() {
	defaultLogger.Store(New(Options{}))
}

// Default 返回全局默认 Logger：写入 os.Stdout、JSON 格式、InfoLevel。
func Default() *Logger { return defaultLogger.Load() }

// SetDefault 替换全局默认 Logger；l 为 nil 时忽略该调用。
func SetDefault(l *Logger) {
	if l != nil {
		defaultLogger.Store(l)
	}
}

// Enabled 报告 lvl 是否达到当前阈值。
//
// 在构造字段之前调用它可让禁用级别的调用完全零分配：
//
//	if log.Enabled(logx.DebugLevel) {
//		log.Debug("dump", logx.Str("body", expensiveCall()))
//	}
func (l *Logger) Enabled(lvl Level) bool {
	return lvl >= Level(l.core.level.Load())
}

// SetLevel 设置级别阈值。同一 Logger 家族（With/Named 派生出的子 Logger）共享该阈值。
func (l *Logger) SetLevel(lvl Level) {
	l.core.level.Store(int32(lvl))
}

// Debug 以 DebugLevel 记录日志；低于阈值时立即返回。
func (l *Logger) Debug(msg string, fields ...Field) { l.log(DebugLevel, msg, fields) }

// Info 以 InfoLevel 记录日志；低于阈值时立即返回。
func (l *Logger) Info(msg string, fields ...Field) { l.log(InfoLevel, msg, fields) }

// Warn 以 WarnLevel 记录日志；低于阈值时立即返回。
func (l *Logger) Warn(msg string, fields ...Field) { l.log(WarnLevel, msg, fields) }

// Error 以 ErrorLevel 记录日志；低于阈值时立即返回。
func (l *Logger) Error(msg string, fields ...Field) { l.log(ErrorLevel, msg, fields) }

// Fatal 以 FatalLevel 记录日志，随后调用 Options.ExitFunc（默认 os.Exit(1)）。
//
// 自定义 ExitFunc 若不结束进程，Fatal 会正常返回；此时调用方应自行处理后续流程。
func (l *Logger) Fatal(msg string, fields ...Field) {
	l.log(FatalLevel, msg, fields)
	l.exit(defaultExitCode)
}

// With 返回派生的子 Logger：fields 追加在该子 Logger 的持久字段末尾，
// 对之后每条日志生效，且不会影响父 Logger（不可变语义）。
//
// 子 Logger 与父级共享 writer、互斥锁、缓冲池和级别阈值。
func (l *Logger) With(fields ...Field) *Logger {
	child := *l // 只复制配置与共享指针，core（含锁）仍为同一实例
	if len(fields) == 0 {
		return &child
	}
	merged := make([]Field, 0, len(l.fields)+len(fields))
	merged = append(merged, l.fields...)
	merged = append(merged, fields...)
	child.fields = merged
	return &child
}

// Named 返回带上名称的子 Logger，名称以 "logger" 字段写入每条日志。
// 父级已有名称时以 "." 连接（Named("http").Named("server") → "http.server"）；
// name 为空时返回等价的子 Logger。
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

// Sync 刷新底层写入器：写入器实现 Sync() error 时（如 *os.File）调用它并返回其错误，
// 否则返回 nil。
func (l *Logger) Sync() error {
	l.core.mu.Lock()
	defer l.core.mu.Unlock()
	if s, ok := l.core.w.(interface{ Sync() error }); ok {
		return s.Sync()
	}
	return nil
}

// Writer 返回日志输出目标。
func (l *Logger) Writer() io.Writer { return l.core.w }

// log 是记录一条日志的热路径：级别判断 → 取池化缓冲 → 编码 → 单次 Write → 归还缓冲。
//
// fields 只被读取、不会被保存，因此调用方的可变参数切片可留在栈上——
// 这是禁用级别路径零分配的前提（见 BenchmarkDisabledLevel）。
// 写入错误被忽略（零拷贝路径不做重试），需要感知刷盘错误时使用 Sync。
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

// write 在锁内完成一次 Write，保证并发写入不会交错。
func (c *core) write(p []byte) {
	c.mu.Lock()
	_, _ = c.w.Write(p)
	c.mu.Unlock()
}

// SlowQuery 记录一条慢查询日志（WarnLevel）。
//
// 输出字段：
//
//	slow_query = true
//	sql        = 原始 SQL 文本
//	rows       = 受影响/返回的行数
//	duration   = 本次耗时；JSON 中为毫秒数（float64，可含小数，例如 1.5 表示 1.5ms），
//	             console 中为人类可读字符串（例如 "1.5ms"）
//	error      = err 非 nil 时附带（经 Err 展开错误链）
//
// l 为 nil 或阈值高于 WarnLevel 时不做任何事，供 gormx 等调用方安全打点。
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

// callerFileLine 返回调用点的 "file.go:行号"（只保留文件名）。
// 调用链为 callerFileLine ← log ← Debug/Info/... ← 使用方，故 skip = 3。
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

// ---------- 共享时间格式化原语 ----------

// appendTime 将 t 按 layout 追加到 dst（一律 UTC）。
// 命中默认布局时走无分配快速路径，否则退回 time.Time.AppendFormat。
func appendTime(dst []byte, t time.Time, layout string) []byte {
	switch layout {
	case defaultJSONTimeFormat:
		return appendUTCMillis(dst, t)
	case defaultConsoleTimeFormat:
		return appendUTCTimeClock(dst, t)
	}
	return t.UTC().AppendFormat(dst, layout)
}

// appendUTCMillis 追加 "2006-01-02T15:04:05.000Z" 形式的时间戳（UTC）。
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

// appendUTCTimeClock 追加 "15:04:05.000" 形式的时间戳（UTC）。
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

// appendPad 以 width 宽度（不足补 '0'）追加非负整数 v。
func appendPad(dst []byte, v, width int) []byte {
	var b [20]byte
	nb := strconv.AppendInt(b[:0], int64(v), 10)
	for i := width - len(nb); i > 0; i-- {
		dst = append(dst, '0')
	}
	return append(dst, nb...)
}
