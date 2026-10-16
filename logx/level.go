package logx

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrUnknownLevel 表示 ParseLevel 遇到了无法识别的级别名。
var ErrUnknownLevel = errors.New("logx: 未知日志级别")

// Level 是日志级别。取值越大越严重。
//
// 常量取值刻意让 InfoLevel == 0，因此 Options.Level / Logger 的零值即 InfoLevel：
//
//	DebugLevel = -1, InfoLevel = 0, WarnLevel = 1, ErrorLevel = 2, FatalLevel = 3
type Level int8

// 日志级别常量。DebugLevel 最小、FatalLevel 最大，比较运算符可直接用于筛选。
const (
	// DebugLevel 是调试级别（-1），仅诊断时启用。
	DebugLevel Level = iota - 1
	// InfoLevel 是常规信息级别（0，默认阈值）。
	InfoLevel
	// WarnLevel 是告警级别（1），例如慢查询打点。
	WarnLevel
	// ErrorLevel 是错误级别（2）。
	ErrorLevel
	// FatalLevel 是致命级别（3），记录后进程退出。
	FatalLevel
)

// String 返回级别的小写名称（debug/info/warn/error/fatal）；
// 无法识别的级别返回 "level(N)" 形式，便于排查配置错误。
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

// ParseLevel 解析级别名，大小写不敏感且忽略首尾空白。
//
// 接受：debug、info、warn、warning、error、fatal。
// 无法识别时返回包装了 ErrUnknownLevel 的错误，可用 errors.Is 判定。
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
	return InfoLevel, fmt.Errorf("logx: 无法解析日志级别 %q: %w", s, ErrUnknownLevel)
}
