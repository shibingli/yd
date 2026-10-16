package gormx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gormlogger "gorm.io/gorm/logger"

	"github.com/yun-dev/yd/logx"
)

// GORM 适配器的日志文案，一律中英同步（English | 中文）。
//
// GORM 自身的提示文本（可能带 %v 动词）由 logx 的 message 字段承载，
// 使稳定的事件名与上游文案分离：事件名可被告警规则直接匹配，
// 上游文案仍原样保留、可检索。
const (
	msgGormInfo   = "gorm info | GORM 提示"
	msgGormWarn   = "gorm warning | GORM 告警"
	msgGormError  = "gorm error | GORM 错误"
	msgGormQuery  = "gorm query executed | GORM 查询执行完成"
	msgQueryFail  = "gorm query failed | GORM 查询失败"
	fieldGormText = "message"
)

// gormLogger 把 GORM 的日志回调桥接到 logx：所有输出只经 logx，
// 不使用标准库 log，也不引入任何第三方日志库。
type gormLogger struct {
	log   *logx.Logger
	slow  time.Duration
	level gormlogger.LogLevel
}

// NewLogger 构造满足 gorm.io/gorm/logger.Interface 的日志器。
//
// slow 是慢查询阈值，<=0 时取默认值 200ms；level 接受 silent / error / warn / info，
// 空值与无法识别的取值回落到 warn。l 为 nil 时使用 logx.Default()。
func NewLogger(l *logx.Logger, slow time.Duration, level string) gormlogger.Interface {
	if l == nil {
		l = logx.Default()
	}
	if slow <= 0 {
		slow = defaultSlowThreshold
	}
	return &gormLogger{log: l, slow: slow, level: parseLogLevel(level)}
}

// parseLogLevel 把配置中的级别名解析为 GORM 日志级别，大小写不敏感。
func parseLogLevel(level string) gormlogger.LogLevel {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "silent":
		return gormlogger.Silent
	case "error":
		return gormlogger.Error
	case "info", "debug":
		return gormlogger.Info
	default:
		// "" 与 "warn"/"warning" 及一切无法识别的取值都回落到 warn。
		return gormlogger.Warn
	}
}

// LogMode 实现 gorm.io/gorm/logger.Interface：返回指定级别的新日志器，原对象保持不变。
func (l *gormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	clone := *l
	clone.level = level
	return &clone
}

// Info 实现 gorm.io/gorm/logger.Interface：GORM 的提示性消息以 InfoLevel 转交 logx。
func (l *gormLogger) Info(ctx context.Context, msg string, data ...any) {
	if l.level < gormlogger.Info {
		return
	}
	l.log.Info(msgGormInfo, logx.Str(fieldGormText, fmt.Sprintf(msg, data...)))
}

// Warn 实现 gorm.io/gorm/logger.Interface：GORM 的告警消息以 WarnLevel 转交 logx。
func (l *gormLogger) Warn(ctx context.Context, msg string, data ...any) {
	if l.level < gormlogger.Warn {
		return
	}
	l.log.Warn(msgGormWarn, logx.Str(fieldGormText, fmt.Sprintf(msg, data...)))
}

// Error 实现 gorm.io/gorm/logger.Interface：GORM 的错误消息以 ErrorLevel 转交 logx。
func (l *gormLogger) Error(ctx context.Context, msg string, data ...any) {
	if l.level < gormlogger.Error {
		return
	}
	l.log.Error(msgGormError, logx.Str(fieldGormText, fmt.Sprintf(msg, data...)))
}

// Trace 实现 gorm.io/gorm/logger.Interface：GORM 每次执行 SQL 后回调本方法。
//
// 分级策略：执行失败 → ErrorLevel；超过慢查询阈值 → logx.SlowQuery；
// 其余情况仅在 info 级别记录一行摘要。ErrRecordNotFound 属于业务常态，不作为错误上报。
//
// 慢查询路径严格遵守「零反射、零字符串拼接」：SQL 与行数都由 GORM 通过 fc 提供
// （SQL 已由方言格式化完毕），本方法只把它交给 logx.SlowQuery，不做任何拼装。
func (l *gormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level <= gormlogger.Silent {
		return
	}
	elapsed := time.Since(begin)

	switch {
	case err != nil && l.level >= gormlogger.Error && !errors.Is(err, gormlogger.ErrRecordNotFound):
		sql, rows := fc()
		l.log.Error(msgQueryFail,
			logx.Str("sql", sql),
			logx.Int64("rows", rows),
			logx.Dur("duration", elapsed),
			logx.Err(err),
		)
	case l.slow > 0 && elapsed >= l.slow && l.level >= gormlogger.Warn:
		sql, rows := fc()
		logx.SlowQuery(l.log, sql, rows, elapsed, nil)
	case l.level >= gormlogger.Info:
		sql, rows := fc()
		l.log.Info(msgGormQuery,
			logx.Str("sql", sql),
			logx.Int64("rows", rows),
			logx.Dur("duration", elapsed),
		)
	}
}

// ParamsFilter 实现 gorm.ParamsFilter 接口：把实参替换为字面量 "?"，
// 避免 gorm.Dialector.Explain 之后的口令、Token 等明文进入日志。
func (l *gormLogger) ParamsFilter(ctx context.Context, sql string, params ...any) (string, []any) {
	return ParamsFilter(ctx, sql)
}
