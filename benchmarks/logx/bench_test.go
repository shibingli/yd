package logbench

import (
	"io"
	"log/slog"
	"testing"

	"github.com/rs/zerolog"
	"github.com/sirupsen/logrus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/shibingli/yd/core/logx"
)

// loggerCase is one logger under comparison. | loggerCase 是参与对比的一个日志器。
type loggerCase struct {
	info  func()
	debug func()
	name  string
}

// newLoggers builds every logger against io.Discard at Info level. | newLoggers 在 Info 级别上为每个日志器构造写入 io.Discard 的实例。
func newLoggers() []loggerCase {
	zl := zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(io.Discard),
		zap.InfoLevel,
	))
	zerologLogger := zerolog.New(io.Discard).With().Timestamp().Logger()
	slogLogger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(io.Discard)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logxLogger := logx.New(logx.Options{Writer: io.Discard, Level: logx.InfoLevel})

	return []loggerCase{
		{
			name: "yd-logx",
			info: func() {
				logxLogger.Info("request completed",
					logx.Str("method", "GET"), logx.Int("status", 200), logx.Bool("ok", true))
			},
			debug: func() {
				logxLogger.Debug("request completed",
					logx.Str("method", "GET"), logx.Int("status", 200), logx.Bool("ok", true))
			},
		},
		{
			name: "zap",
			info: func() {
				zl.Info("request completed",
					zap.String("method", "GET"), zap.Int("status", 200), zap.Bool("ok", true))
			},
			debug: func() {
				zl.Debug("request completed",
					zap.String("method", "GET"), zap.Int("status", 200), zap.Bool("ok", true))
			},
		},
		{
			name: "zerolog",
			info: func() {
				zerologLogger.Info().Str("method", "GET").Int("status", 200).Bool("ok", true).Msg("request completed")
			},
			debug: func() {
				zerologLogger.Debug().Str("method", "GET").Int("status", 200).Bool("ok", true).Msg("request completed")
			},
		},
		{
			name: "slog",
			info: func() {
				slogLogger.Info("request completed", "method", "GET", "status", 200, "ok", true)
			},
			debug: func() {
				slogLogger.Debug("request completed", "method", "GET", "status", 200, "ok", true)
			},
		},
		{
			name: "logrus",
			info: func() {
				logrusLogger.WithFields(logrus.Fields{"method": "GET", "status": 200, "ok": true}).Info("request completed")
			},
			debug: func() {
				logrusLogger.WithFields(logrus.Fields{"method": "GET", "status": 200, "ok": true}).Debug("request completed")
			},
		},
	}
}

// BenchmarkInfo measures an enabled Info record with three fields. | BenchmarkInfo 测量带三个字段的启用 Info 记录。
func BenchmarkInfo(b *testing.B) {
	for _, lc := range newLoggers() {
		lc := lc
		b.Run(lc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				lc.info()
			}
		})
	}
}

// BenchmarkDisabled measures a Debug call below the Info threshold. | BenchmarkDisabled 测量低于 Info 阈值的 Debug 调用。
func BenchmarkDisabled(b *testing.B) {
	for _, lc := range newLoggers() {
		lc := lc
		b.Run(lc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				lc.debug()
			}
		})
	}
}
