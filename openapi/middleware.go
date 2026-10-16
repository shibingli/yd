package openapi

import (
	"net/http"

	"github.com/shibingli/yd/router"
)

// Handler returns the router handler serving the JSON document and the built-in
// viewer. | Handler 返回提供 JSON 文档与内置查看器的路由处理器。
//
// Mount the same handler on both configured endpoints: | 请把同一个处理器挂载到两个配置端点上：
//
//	h := openapi.Handler(doc, opts)
//	r.GET(opts.Path, h)
//	r.GET(opts.DocsPath, h)
//
// Requests for any other path receive 404, so the handler may also be mounted on a
// catch-all route. | 其他路径的请求会返回 404，因此该处理器也可以挂在通配路由上。
//
// Options.Blocked answers both endpoints with 404, which is how production switches
// the documentation off. | Options.Blocked 会让两个端点都以 404 应答，生产环境据此关闭文档。
//
// The whole package stays silent: no logging is emitted, and nothing is written to
// stdout. | 本包保持静默：不输出任何日志，也不向 stdout 写入内容。
func Handler(d *Document, o Options) router.HandlerFunc {
	opts := o.withDefaults()
	doc := d
	if doc == nil {
		doc = &Document{}
	}
	return func(c *router.Ctx) {
		if opts.Blocked {
			writeNotFound(c)
			return
		}
		switch c.Path() {
		case opts.Path:
			raw, err := doc.JSON()
			if err != nil {
				writeInternalError(c)
				return
			}
			c.SetHeader("Cache-Control", "no-cache")
			_ = c.Blob(http.StatusOK, "application/json; charset=utf-8", raw)
		case opts.DocsPath:
			page, err := renderViewer(doc, opts)
			if err != nil {
				writeInternalError(c)
				return
			}
			c.SetHeader("Cache-Control", "no-cache")
			_ = c.HTMLString(http.StatusOK, string(page))
		default:
			writeNotFound(c)
		}
	}
}

// writeNotFound answers with a plain text 404. | writeNotFound 以纯文本 404 应答。
func writeNotFound(c *router.Ctx) {
	_ = c.String(http.StatusNotFound, http.StatusText(http.StatusNotFound))
}

// writeInternalError answers with a plain text 500. | writeInternalError 以纯文本 500 应答。
func writeInternalError(c *router.Ctx) {
	_ = c.String(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
}
