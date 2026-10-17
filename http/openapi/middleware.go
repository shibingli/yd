package openapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"

	"github.com/shibingli/yd/http/router"
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
// The handler is safe for concurrent use: the document is serialized once on first use and
// both payloads are then served from that snapshot with a strong ETag, so repeated requests
// neither re-encode the document nor observe a half-mutated one. Mutate the document only
// before the first request. | 该处理器可安全并发使用：文档在首次使用时序列化一次，此后两个
// 载荷都从该快照提供并带强 ETag，因此重复请求既不重新编码文档，也不会观察到改到一半的状态。
// 请只在首次请求之前修改文档。
//
// The whole package stays silent: no logging is emitted, and nothing is written to
// stdout. | 本包保持静默：不输出任何日志，也不向 stdout 写入内容。
func Handler(d *Document, o Options) router.HandlerFunc {
	state := &handlerState{opts: o.withDefaults(), doc: d}
	if state.doc == nil {
		state.doc = &Document{}
	}
	return state.serve
}

// handlerState memoizes the serialized document and viewer page. | handlerState 缓存序列化后的
// 文档与查看器页面。
type handlerState struct {
	err error

	doc      *Document
	jsonETag string
	pageETag string

	raw  []byte
	page []byte
	opts Options

	once sync.Once
}

// prepare serializes both payloads exactly once. | prepare 只序列化两个载荷一次。
func (s *handlerState) prepare() {
	s.once.Do(func() {
		doc := s.doc.normalized()
		s.raw, s.err = doc.JSON()
		if s.err != nil {
			return
		}
		s.jsonETag = etagOf(s.raw)
		s.page, s.err = renderViewerWithJSON(doc, s.opts, s.raw)
		if s.err != nil {
			return
		}
		s.pageETag = etagOf(s.page)
	})
}

// serve answers one documentation request. | serve 应答一次文档请求。
func (s *handlerState) serve(c *router.Ctx) {
	if s.opts.Blocked {
		writeNotFound(c)
		return
	}
	switch c.Path() {
	case s.opts.Path:
		s.serveJSON(c)
	case s.opts.DocsPath:
		s.serveViewer(c)
	default:
		writeNotFound(c)
	}
}

// serveJSON writes the JSON document, honouring If-None-Match. | serveJSON 写出 JSON 文档，
// 并遵循 If-None-Match。
func (s *handlerState) serveJSON(c *router.Ctx) {
	s.prepare()
	if s.err != nil {
		writeInternalError(c)
		return
	}
	c.SetHeader("Cache-Control", "no-cache")
	if notModified(c, s.jsonETag) {
		return
	}
	c.SetHeader("ETag", s.jsonETag)
	_ = c.Blob(http.StatusOK, "application/json; charset=utf-8", s.raw)
}

// serveViewer writes the built-in viewer, honouring If-None-Match. | serveViewer 写出内置查看器，
// 并遵循 If-None-Match。
func (s *handlerState) serveViewer(c *router.Ctx) {
	s.prepare()
	if s.err != nil {
		writeInternalError(c)
		return
	}
	c.SetHeader("Cache-Control", "no-cache")
	if notModified(c, s.pageETag) {
		return
	}
	c.SetHeader("ETag", s.pageETag)
	_ = c.HTMLString(http.StatusOK, string(s.page))
}

// notModified writes 304 and reports true when the request's If-None-Match matches etag. | notModified
// 在请求的 If-None-Match 与 etag 匹配时写出 304 并返回 true。
func notModified(c *router.Ctx, etag string) bool {
	if etag == "" || !etagMatches(c.Header("If-None-Match"), etag) {
		return false
	}
	c.SetHeader("ETag", etag)
	c.WriteHeader(http.StatusNotModified)
	return true
}

// etagMatches reports whether an If-None-Match header contains etag, accepting the "*" form
// and weak validators. | etagMatches 报告 If-None-Match 是否包含 etag，接受 "*" 形式与弱校验器。
func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || part == etag || strings.TrimPrefix(part, "W/") == etag {
			return true
		}
	}
	return false
}

// etagOf builds a strong ETag from the SHA-256 digest of the payload, which is computed once
// per handler when the document is snapshotted. | etagOf 依据载荷的 SHA-256 摘要构造强 ETag，
// 每个处理器只在文档快照时计算一次。
func etagOf(payload []byte) string {
	sum := sha256.Sum256(payload)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// writeNotFound answers with a plain text 404. | writeNotFound 以纯文本 404 应答。
func writeNotFound(c *router.Ctx) {
	_ = c.String(http.StatusNotFound, http.StatusText(http.StatusNotFound))
}

// writeInternalError answers with a plain text 500. | writeInternalError 以纯文本 500 应答。
func writeInternalError(c *router.Ctx) {
	_ = c.String(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
}
