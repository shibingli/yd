package logbench

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/sirupsen/logrus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/shibingli/yd/core/logx"
)

// benchSelected reports whether name passes the YD_BENCH_ONLY filter. Set it to
// one comma-separated list of logger names to run each implementation in its own
// process and remove same-process ordering and GC bias. | benchSelected 判断
// name 是否通过 YD_BENCH_ONLY 过滤。设为逗号分隔的日志器名可让每个实现独立
// 进程运行，消除同进程顺序与 GC 偏差。
func benchSelected(name string) bool {
	want := os.Getenv("YD_BENCH_ONLY")
	if want == "" {
		return true
	}
	for _, part := range strings.Split(want, ",") {
		if strings.TrimSpace(part) == name {
			return true
		}
	}
	return false
}

// loggerCase is one logger under comparison. The bound variants pre-attach the
// three static fields once, which is the recommended API for fixed context;
// the dynamic variants rebuild them on every call. | loggerCase 是参与对比的
// 一个日志器。bound 变体只预绑定一次三个静态字段，是固定上下文的推荐写法；
// dynamic 变体每次调用都重建字段。
type loggerCase struct {
	info       func()
	debug      func()
	infoBound  func()
	debugBound func()
	name       string
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

	zapBound := zl.With(zap.String("method", "GET"), zap.Int("status", 200), zap.Bool("ok", true))
	zerologBound := zerologLogger.With().Str("method", "GET").Int("status", 200).Bool("ok", true).Logger()
	slogBound := slogLogger.With("method", "GET", "status", 200, "ok", true)
	logrusBound := logrusLogger.WithFields(logrus.Fields{"method": "GET", "status": 200, "ok": true})
	logxBound := logxLogger.With(logx.Str("method", "GET"), logx.Int("status", 200), logx.Bool("ok", true))

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
			infoBound:  func() { logxBound.Info("request completed") },
			debugBound: func() { logxBound.Debug("request completed") },
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
			infoBound:  func() { zapBound.Info("request completed") },
			debugBound: func() { zapBound.Debug("request completed") },
		},
		{
			name: "zerolog",
			info: func() {
				zerologLogger.Info().Str("method", "GET").Int("status", 200).Bool("ok", true).Msg("request completed")
			},
			debug: func() {
				zerologLogger.Debug().Str("method", "GET").Int("status", 200).Bool("ok", true).Msg("request completed")
			},
			infoBound:  func() { zerologBound.Info().Msg("request completed") },
			debugBound: func() { zerologBound.Debug().Msg("request completed") },
		},
		{
			name: "slog",
			info: func() {
				slogLogger.Info("request completed", "method", "GET", "status", 200, "ok", true)
			},
			debug: func() {
				slogLogger.Debug("request completed", "method", "GET", "status", 200, "ok", true)
			},
			infoBound:  func() { slogBound.Info("request completed") },
			debugBound: func() { slogBound.Debug("request completed") },
		},
		{
			name: "logrus",
			info: func() {
				logrusLogger.WithFields(logrus.Fields{"method": "GET", "status": 200, "ok": true}).Info("request completed")
			},
			debug: func() {
				logrusLogger.WithFields(logrus.Fields{"method": "GET", "status": 200, "ok": true}).Debug("request completed")
			},
			infoBound:  func() { logrusBound.Info("request completed") },
			debugBound: func() { logrusBound.Debug("request completed") },
		},
	}
}

// BenchmarkInfo measures an enabled Info record with three per-call fields. | BenchmarkInfo
// 测量带三个逐调用字段的启用 Info 记录。
func BenchmarkInfo(b *testing.B) {
	for _, lc := range newLoggers() {
		lc := lc
		if !benchSelected(lc.name) {
			continue
		}
		b.Run(lc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				lc.info()
			}
		})
	}
}

// BenchmarkInfoPrebound measures an enabled Info record with the three static
// fields bound once, which removes field construction from the hot path. | BenchmarkInfoPrebound
// 测量三个静态字段只绑定一次的启用 Info 记录，把字段构造移出热路径。
func BenchmarkInfoPrebound(b *testing.B) {
	for _, lc := range newLoggers() {
		lc := lc
		if !benchSelected(lc.name) {
			continue
		}
		b.Run(lc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				lc.infoBound()
			}
		})
	}
}

// BenchmarkDisabled measures a Debug call below the Info threshold with
// per-call fields. | BenchmarkDisabled 测量低于 Info 阈值、逐调用构造字段的 Debug 调用。
func BenchmarkDisabled(b *testing.B) {
	for _, lc := range newLoggers() {
		lc := lc
		if !benchSelected(lc.name) {
			continue
		}
		b.Run(lc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				lc.debug()
			}
		})
	}
}

// BenchmarkDisabledPrebound measures a Debug call below the Info threshold with
// the static fields bound once. | BenchmarkDisabledPrebound 测量低于 Info 阈值、
// 静态字段只绑定一次的 Debug 调用。
func BenchmarkDisabledPrebound(b *testing.B) {
	for _, lc := range newLoggers() {
		lc := lc
		if !benchSelected(lc.name) {
			continue
		}
		b.Run(lc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				lc.debugBound()
			}
		})
	}
}
