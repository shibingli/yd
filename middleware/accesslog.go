package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/http/router"
)

// countingWriterPool recycles the access-log wrapper, removing one allocation per request.
// | countingWriterPool 复用访问日志包装器，消除每请求一次分配。
//
// The wrapper is returned only after its fields have been read for the log record and the
// context has been detached from it; no reference survives the request, so reuse cannot leak
// status or byte counts across requests. | 只有日志记录读完其字段、且上下文已与其解绑之后，
// 包装器才会被归还；请求结束后不再存在引用，因此复用不会让状态码或字节数跨请求泄漏。
var countingWriterPool = sync.Pool{New: func() any { return new(countingWriter) }}

// AccessLog returns a middleware that logs one record per request. | AccessLog 返回每个请求记录一条日志的中间件。
// Recorded fields are method, path, query, status, bytes, remote_ip, duration — milliseconds in JSON
// output — and request_id, which is filled when RequestID runs earlier in the chain. | 记录字段包括 method、path、query、status、bytes、remote_ip、duration（JSON 中为毫秒）以及 request_id（RequestID 位于链上更早位置时才有值）。
// Upgrade requests are not wrapped so the WebSocket handshake keeps a usable http.Hijacker. | 升级请求不做包装，以保证 WebSocket 握手仍有可用的 http.Hijacker。
// A nil logger turns the middleware into a pass-through. | 日志器为 nil 时该中间件退化为直通。
func AccessLog(l *logx.Logger) router.Middleware {
	return func(c *router.Ctx) {
		if l == nil || isUpgradeRequest(c.Request) {
			c.Next()
			return
		}
		start := time.Now()
		cw := countingWriterPool.Get().(*countingWriter)
		cw.ResponseWriter = c.Writer
		cw.status = 0
		cw.bytes = 0
		c.Writer = cw

		defer func() {
			rec := recover()
			c.Writer = cw.ResponseWriter
			status := cw.status
			if status == 0 {
				status = c.StatusCode()
			}
			if status == 0 {
				if rec != nil {
					// A panic that escaped us becomes a 500 upstream. | 逃逸到上层的 panic 最终会成为 500。
					status = http.StatusInternalServerError
				} else {
					status = http.StatusOK
				}
			}
			l.Info("access | 访问日志",
				logx.Str("request_id", requestID(c)),
				logx.Str("method", c.Request.Method),
				logx.Str("path", c.Request.URL.Path),
				logx.Str("query", c.Request.URL.RawQuery),
				logx.Int("status", status),
				logx.Int64("bytes", cw.bytes),
				logx.Str("remote_ip", c.RealIP()),
				logx.Dur("duration", time.Since(start)),
			)
			// Every field has been consumed and the context no longer points at the wrapper,
			// so it can safely serve another request. | 所有字段都已消费，上下文也不再指向
			// 该包装器，因此可以安全地服务下一个请求。
			cw.ResponseWriter = nil
			cw.status = 0
			cw.bytes = 0
			countingWriterPool.Put(cw)
			if rec != nil {
				// Re-raise instead of swallowing: the request must not continue after a panic, and
				// the outer Recover middleware owns the final response. | 重新抛出而不是吞掉 panic：
				// 请求不得在 panic 之后继续执行，最终响应由更外层的 Recover 中间件负责。
				panic(rec)
			}
		}()

		c.Next()
	}
}

// countingWriter counts the response bytes and remembers the status code.
// | countingWriter 统计响应字节数并记录状态码。
type countingWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

// WriteHeader records the status code before delegating. | WriteHeader 记录状态码后转发。
func (w *countingWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write counts the written bytes. | Write 累加已写出的字节数。
func (w *countingWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

// Flush implements http.Flusher so streaming responses keep working. | Flush 实现 http.Flusher，保证流式响应不受影响。
func (w *countingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer to http.ResponseController. | Unwrap 向 http.ResponseController 暴露被包装的写入器。
func (w *countingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
