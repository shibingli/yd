package logx

import (
	"strconv"
	"strings"

	"github.com/yun-dev/yd/errx"
)

// ErrUnknownLevel indicates that ParseLevel encountered an unrecognized level name. | ErrUnknownLevel 表示 ParseLevel 遇到了无法识别的级别名。
var ErrUnknownLevel = errx.New("logx: unknown log level | logx：未知日志级别")

// Level is a log level. Larger values are more severe. | Level 是日志级别。取值越大越严重。
//
// The constant values deliberately make InfoLevel == 0, so the zero value of Options.Level / Logger is InfoLevel: | 常量取值刻意让 InfoLevel == 0，因此 Options.Level / Logger 的零值即 InfoLevel：
//
//	DebugLevel = -1, InfoLevel = 0, WarnLevel = 1, ErrorLevel = 2, FatalLevel = 3
type Level int8

// Log level constants. DebugLevel is the smallest and FatalLevel the largest, so comparison operators can be used for filtering directly. | 日志级别常量。DebugLevel 最小、FatalLevel 最大，比较运算符可直接用于筛选。
const (
	// DebugLevel is the debug level (-1), enabled only when diagnosing. | DebugLevel 是调试级别（-1），仅诊断时启用。
	DebugLevel Level = iota - 1
	// InfoLevel is the regular informational level (0, the default threshold). | InfoLevel 是常规信息级别（0，默认阈值）。
	InfoLevel
	// WarnLevel is the warning level (1), used for example by slow-query instrumentation. | WarnLevel 是告警级别（1），例如慢查询打点。
	WarnLevel
	// ErrorLevel is the error level (2). | ErrorLevel 是错误级别（2）。
	ErrorLevel
	// FatalLevel is the fatal level (3); the process exits after the record is written. | FatalLevel 是致命级别（3），记录后进程退出。
	FatalLevel
)

// String returns the lowercase name of the level (debug/info/warn/error/fatal); | String 返回级别的小写名称（debug/info/warn/error/fatal）；
// Unrecognized levels are rendered as "level(N)" to make configuration mistakes easy to diagnose. | 无法识别的级别返回 "level(N)" 形式，便于排查配置错误。
func (l Level) String() string {
	switch l {
	case DebugLevel:
		return "debug"
	case InfoLevel:
		return "info"
	case WarnLevel:
		return "warn"
	case ErrorLevel:
		return "error"
	case FatalLevel:
		return "fatal"
	}
	var b [16]byte
	buf := strconv.AppendInt(b[:0], int64(l), 10)
	return "level(" + string(buf) + ")"
}

// ParseLevel parses a level name; it is case-insensitive and ignores leading and trailing whitespace. | ParseLevel 解析级别名，大小写不敏感且忽略首尾空白。
//
// Accepted values: debug, info, warn, warning, error, fatal. | 接受：debug、info、warn、warning、error、fatal。
// When the name is unrecognized it returns an error wrapping ErrUnknownLevel, testable with errors.Is. | 无法识别时返回包装了 ErrUnknownLevel 的错误，可用 errors.Is 判定。
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return DebugLevel, nil
	case "info":
		return InfoLevel, nil
	case "warn", "warning":
		return WarnLevel, nil
	case "error":
		return ErrorLevel, nil
	case "fatal":
		return FatalLevel, nil
	}
	return InfoLevel, errx.Detail(ErrUnknownLevel, "level %q | 级别 %q", s, s)
}
