package logx

import (
	"strconv"
	"time"
)

// appendConsoleRecord appends one complete record (including the trailing newline) to dst in the human-readable console format. | appendConsoleRecord 将一条完整记录（含结尾换行）按人类可读的 console 格式追加到 dst。
//
// Record shape: | 记录形状：
//
//	15:04:05.000 LEVEL pid=1234 host=box service logger (file.go:12) msg key=value key2=3
//
// Here pid always appears, host appears when the platform reports one, service/logger/caller appear | 其中 pid 总是出现，host 在平台能提供时出现，service/logger/caller 仅在配置后出现，
func appendConsoleRecord(dst []byte, l *Logger, lvl Level, t time.Time, msg, caller string, extra []Field) []byte {
	dst = appendTime(dst, t, l.timeFormat)
	dst = append(dst, ' ')
	dst = append(dst, levelLabel(lvl)...)
	dst = append(dst, ' ')
	dst = append(dst, "pid="...)
	dst = strconv.AppendInt(dst, int64(l.pid), 10)
	dst = append(dst, ' ')
	if l.host != "" {
		dst = append(dst, "host="...)
		dst = appendConsoleString(dst, l.host)
		dst = append(dst, ' ')
	}
	if l.service != "" {
		dst = append(dst, l.service...)
		dst = append(dst, ' ')
	}
	if l.name != "" {
		dst = append(dst, l.name...)
		dst = append(dst, ' ')
	}
	if caller != "" {
		dst = append(dst, '(')
		dst = append(dst, caller...)
		dst = append(dst, ')', ' ')
	}
	dst = append(dst, msg...)
	dst = appendConsoleFields(dst, l.fields, l.timeFormat)
	dst = appendConsoleFields(dst, extra, l.timeFormat)
	return append(dst, '\n')
}

// appendConsoleFields encodes a group of fields in order. | appendConsoleFields 依次编码一组字段。
func appendConsoleFields(dst []byte, fields []Field, timeFormat string) []byte {
	for i := range fields {
		dst = appendConsoleField(dst, &fields[i], timeFormat)
	}
	return dst
}

// appendConsoleField encodes a single field as " key=value" and appends it to dst. | appendConsoleField 将单个字段编码为 " key=value" 追加到 dst。
// UnknownType fields are skipped; strings and durations use human-readable forms, while all other types reuse the JSON value encoder. | UnknownType 字段跳过；字符串与时长使用人类可读形式，其余类型复用 JSON 值编码。
func appendConsoleField(dst []byte, f *Field, timeFormat string) []byte {
	if f.Type == UnknownType {
		return dst
	}
	if f.Key == "" {
		if f.Type != RawType {
			return dst
		}
		dst = append(dst, ' ')
		return append(dst, f.bs...)
	}
	dst = append(dst, ' ')
	dst = append(dst, f.Key...)
	dst = append(dst, '=')
	switch f.Type {
	case StringType, ErrorType:
		return appendConsoleString(dst, f.str)
	case DurationType:
		return appendHumanDuration(dst, int64(f.num))
	}
	return appendJSONValue(dst, f, timeFormat)
}

// appendConsoleString outputs a string in console format: verbatim when unambiguous, otherwise with Go escaping | appendConsoleString 在 console 格式下输出字符串：无歧义时原样输出，否则用 Go 转义
// (strconv.AppendQuote, so a newline is written as \n and a control character as \x01). | （strconv.AppendQuote，例如换行输出为 \n、控制字符输出为 \x01）。
func appendConsoleString(dst []byte, s string) []byte {
	if consoleSafe(s) {
		return append(dst, s...)
	}
	return strconv.AppendQuote(dst, s)
}

// consoleSafe reports whether s can appear unquoted in a console line. | consoleSafe 报告 s 能否不加引号地出现在 console 行中。
func consoleSafe(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c <= ' ' || c == '"' || c == '\\' || c == '=' || c == 0x7f {
			return false
		}
	}
	return true
}

// appendHumanDuration appends a duration in human-readable form: ns / µs / ms / s. | appendHumanDuration 以人类可读形式追加时长：ns / µs / ms / s。
func appendHumanDuration(dst []byte, ns int64) []byte {
	if ns == 0 {
		return append(dst, "0s"...)
	}
	if ns < 0 {
		ns = -ns
		dst = append(dst, '-')
	}
	switch {
	case ns < int64(time.Microsecond):
		dst = strconv.AppendInt(dst, ns, 10)
		return append(dst, "ns"...)
	case ns < int64(time.Millisecond):
		dst = strconv.AppendFloat(dst, float64(ns)/float64(time.Microsecond), 'f', -1, 64)
		return append(dst, "µs"...)
	case ns < int64(time.Second):
		dst = strconv.AppendFloat(dst, float64(ns)/float64(time.Millisecond), 'f', -1, 64)
		return append(dst, "ms"...)
	default:
		dst = strconv.AppendFloat(dst, float64(ns)/float64(time.Second), 'f', -1, 64)
		return append(dst, 's')
	}
}

// levelLabel returns a fixed-width 5-character level label, making vertical scanning by eye easy. | levelLabel 返回 5 字符定宽的级别标签，便于肉眼纵向扫描。
func levelLabel(lvl Level) string {
	switch lvl {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO "
	case WarnLevel:
		return "WARN "
	case ErrorLevel:
		return "ERROR"
	case FatalLevel:
		return "FATAL"
	}
	return "?????"
}
