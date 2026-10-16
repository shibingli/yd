package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/yun-dev/yd/logx"
	"github.com/yun-dev/yd/router"
)

// Recover returns a middleware that catches panics from downstream handlers.
//
// | Recover 返回捕获下游处理器 panic 的中间件。
//
// The panic value and the goroutine stack are logged through l (which may be nil), the
// remaining chain is aborted, and a bare 500 response is written. When the response header
// has already been written the 500 is skipped so the started response stays consistent.
//
// | panic 值与 goroutine 调用栈经 l 记录（l 可为 nil），随后终止链上剩余处理器并写出一个
// | 不含内部细节的 500 响应；若响应头已写出则跳过 500，以免破坏已经开始输出的响应。
func Recover(l *logx.Logger) router.Middleware {
	return func(c *router.Ctx) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if l != nil {
				l.Error("panic recovered | 捕获到 panic",
					logx.Str("request_id", requestID(c)),
					logx.Str("method", c.Request.Method),
					logx.Str("path", c.Request.URL.Path),
					logx.Str("panic", fmt.Sprint(rec)),
					logx.Str("stack", string(debug.Stack())),
				)
			}
			// Stop the remaining handlers so a recovered panic does not resume the chain. | 终止剩余处理器，避免恢复后继续执行链上内容。
			c.Abort()
			if c.Written() {
				return
			}
			_ = c.String(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
		}()
		c.Next()
	}
}
