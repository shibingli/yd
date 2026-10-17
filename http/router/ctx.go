package router

import (
	"encoding/xml"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
)

// HandlerFunc is the unified signature for route handlers and middleware. | HandlerFunc 是路由处理函数与中间件的统一签名。
//
// Middleware calls the next handler in the chain through c.Next(); not calling Next ends the chain. | 中间件通过 c.Next() 调用链上的下一个处理器；不调用 Next 即终止链。
type HandlerFunc func(*Ctx)

// Middleware is an alias for a handler, semantically emphasizing a "pluggable chain handler". | Middleware 是中间件的别名，语义上强调"可插拔的链式处理器"。
type Middleware = HandlerFunc

// Renderer abstracts template rendering and is implemented by the render package, so that router does not depend on a template engine in reverse. | Renderer 是模板渲染能力的抽象，由 render 包实现，避免 router 反向依赖模板引擎。
type Renderer interface {
	// Render renders the template named name to w. | Render 把名为 name 的模板渲染到 w。
	Render(w io.Writer, name string, data any) error
}

// Ctx carries the context of a single request. | Ctx 承载单次请求的上下文。
//
// Ctx is reused from an object pool and is only valid during this request; never keep a reference after the request ends. | Ctx 由对象池复用，仅在本次请求处理期间有效；禁止在请求结束后继续持有。
type Ctx struct {
	// Writer is the underlying response writer. | Writer 是底层响应写入器。
	Writer http.ResponseWriter
	// Request is the original request (URL.Path is left untouched during route matching). | Request 是原始请求（路由匹配期间 URL.Path 保持原样）。
	Request *http.Request
	// Params holds the path parameters captured for this request. | Params 保存本次请求捕获到的路径参数。
	Params *Params

	// paramStore backs Params so that no second object pool is needed per request. | paramStore
	// 为 Params 提供存储，从而不必为每个请求再走一次对象池。
	paramStore Params

	router *Router

	query       url.Values
	keys        map[string]any
	path        string
	handlers    []HandlerFunc
	index       int
	status      int
	written     bool
	aborted     bool
	bodyLimited bool // request body already wrapped by http.MaxBytesReader | 请求体已由 http.MaxBytesReader 包装
	// borrowed marks handlers as the immutable route chain owned by the tree; it must never be
	// appended to or written through. | borrowed 标记 handlers 为路由树持有的不可变调用链，
	// 不得向其追加或经它写入。
	borrowed bool
}

// reset prepares the value for reuse. | reset 为复用做准备。
func (c *Ctx) reset(w http.ResponseWriter, r *http.Request, rt *Router) {
	c.Writer = w
	c.Request = r
	c.router = rt
	// Drop an abnormally large parameter slice instead of retaining it for the life of the
	// pooled context; ordinary requests keep the backing array. | 异常大的参数切片不随池化上下文长期
	// 保留；普通请求保留底层数组。
	if cap(c.paramStore.ps) > 64 {
		c.paramStore.ps = nil
	} else {
		c.paramStore.ps = c.paramStore.ps[:0]
	}
	c.Params = &c.paramStore
	// Keep the backing array of handlers for reuse, except when it was borrowed from the tree:
	// appending through that slice would overwrite the immutable route chain. | 保留 handlers 的
	// 底层数组以便复用，但从路由树借用时必须丢弃：经该切片追加会覆写不可变的调用链。
	if c.borrowed {
		c.handlers = nil
		c.borrowed = false
	} else {
		c.handlers = c.handlers[:0]
	}
	c.index = -1
	c.status = 0
	c.written = false
	c.aborted = false
	c.bodyLimited = false
	c.query = nil
	if c.keys != nil {
		clear(c.keys)
	}
}

// Next runs the next handler in the chain. | Next 执行链上的下一个处理器。
//
// It should be called from middleware; the last handler (the business handler) normally does not need to call it. | 应在中间件中调用；最后一个处理器（业务处理函数）通常不需要调用。
func (c *Ctx) Next() {
	c.index++
	for c.index < len(c.handlers) {
		if c.aborted {
			return
		}
		// The index is incremented **after** the call: this way calling Next() again inside a handler resumes from | 索引在调用**之后**自增：这样处理器内部再次调用 Next() 会从
		// the next handler instead of skipping it (only then is the order correct with nested calls). | 下一个处理器继续，而不是跳过它（嵌套调用下顺序才正确）。
		c.handlers[c.index](c)
		c.index++
	}
}

// Abort stops the remaining handlers, but an already written response stays valid. | Abort 终止后续处理器，但已写出的响应保持有效。
func (c *Ctx) Abort() { c.aborted = true }

// Aborted reports whether the current chain has been aborted. | Aborted 报告当前链是否已被终止。
func (c *Ctx) Aborted() bool { return c.aborted }

// Router returns the owning router. | Router 返回所属路由器。
func (c *Ctx) Router() *Router { return c.router }

// ---------------------------------------------------------------- Request information | 请求信息

// Method returns the HTTP method. | Method 返回 HTTP 方法。
func (c *Ctx) Method() string { return c.Request.Method }

// Path returns the request path (in its original encoded form). | Path 返回请求路径（原始编码形式）。
//
// The value is computed once by ServeHTTP and cached on the context, so middleware and handlers
// can call Path repeatedly without rescanning the URL. | 该值由 ServeHTTP 计算一次并缓存在上下文上，
// 中间件与处理器可以反复调用 Path 而无需重复扫描 URL。
func (c *Ctx) Path() string {
	if c.path != "" {
		return c.path
	}
	return fastEscapedPath(c.Request.URL)
}

// URI returns the path and the query string. | URI 返回路径与查询串。
func (c *Ctx) URI() string { return c.Request.URL.RequestURI() }

// Host returns the request Host. | Host 返回请求 Host。
func (c *Ctx) Host() string { return c.Request.Host }

// Scheme returns the request scheme (http / https). | Scheme 返回请求协议（http / https）。
func (c *Ctx) Scheme() string {
	if c.Request.TLS != nil {
		return "https"
	}
	if p := c.Request.Header.Get("X-Forwarded-Proto"); p != "" {
		return p
	}
	return "http"
}

// IsTLS reports whether the request went through TLS. | IsTLS 报告请求是否经由 TLS。
func (c *Ctx) IsTLS() bool { return c.Request.TLS != nil }

// RealIP resolves the client IP from X-Forwarded-For, X-Real-Ip and RemoteAddr, in that order. | RealIP 依次从 X-Forwarded-For、X-Real-Ip、RemoteAddr 解析客户端 IP。
//
// It trusts proxy headers unconditionally: the leftmost X-Forwarded-For value is controlled by the
// client, so a security decision derived from RealIP can be spoofed. Rate limiting, access logs and
// audit records must use ClientIP with an explicit trusted-proxy list instead. | 它无条件信任代理头：
// X-Forwarded-For 最左值由客户端控制，因此据 RealIP 做出的安全判定可被伪造。限流、访问日志与审计
// 应改用带显式可信代理列表的 ClientIP。
func (c *Ctx) RealIP() string {
	if xff := c.Request.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := c.Request.Header.Get("X-Real-Ip"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return c.Request.RemoteAddr
	}
	return host
}

// Param returns the path parameter with the given name (the original encoding is kept, no percent-decoding). | Param 返回指定名称的路径参数（保持原始编码，不做百分号解码）。
func (c *Ctx) Param(name string) string {
	if c.Params == nil {
		return ""
	}
	return c.Params.Get(name)
}

// ParamUnescaped returns the percent-decoded path parameter; on decoding failure it returns the raw value. | ParamUnescaped 返回百分号解码后的路径参数；解码失败时返回原始值。
func (c *Ctx) ParamUnescaped(name string) string {
	raw := c.Param(name)
	if raw == "" {
		return ""
	}
	dec, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return dec
}

// QueryValues returns the parsed query parameters (parsed lazily and cached). | QueryValues 返回解析后的查询参数（惰性解析并缓存）。
func (c *Ctx) QueryValues() url.Values {
	if c.query == nil {
		c.query = c.Request.URL.Query()
	}
	return c.query
}

// Query returns the first value of a query parameter. | Query 返回查询参数的首个值。
func (c *Ctx) Query(key string) string { return c.QueryValues().Get(key) }

// DefaultQuery returns a query parameter, or def when it is absent. | DefaultQuery 返回查询参数，缺省时返回 def。
func (c *Ctx) DefaultQuery(key, def string) string {
	if v, ok := c.QueryValues()[key]; ok && len(v) > 0 && v[0] != "" {
		return v[0]
	}
	return def
}

// QueryValuesAll returns all values of a query parameter with the same name. | QueryValuesAll 返回同名查询参数的全部取值。
func (c *Ctx) QueryValuesAll(key string) []string { return c.QueryValues()[key] }

// limitBody bounds the request body with http.MaxBytesReader exactly once. | limitBody 用 http.MaxBytesReader 只包装一次请求体。
//
// The wrapper is what actually enforces MaxBodySize for streaming reads; ParseMultipartForm's
// argument is only an in-memory budget. | 这层包装才是流式读取时真正执行 MaxBodySize 的地方；
// ParseMultipartForm 的参数只是内存预算。
func (c *Ctx) limitBody() {
	if c.bodyLimited || c.Request == nil || c.Request.Body == nil {
		return
	}
	// net/http gives a body-less GET or HEAD http.NoBody and ContentLength 0; skipping them here
	// avoids an interface comparison on the most common requests. Chunked bodies have
	// ContentLength -1 and still take the wrapper. | net/http 对无正文的 GET/HEAD 给出
	// http.NoBody 且 ContentLength 为 0；在这里跳过它们可以避免最常见请求上的接口比较。分块正文的
	// ContentLength 为 -1，仍会走包装。
	if c.Request.ContentLength == 0 &&
		(c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) {
		return
	}
	if c.Request.Body == http.NoBody {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, c.router.maxBodySize())
	c.bodyLimited = true
}

// Body returns the request body, bounded by the router's MaxBodySize, for streaming uploads. | Body 返回受路由器 MaxBodySize 限制的请求体，供流式上传使用。
func (c *Ctx) Body() io.ReadCloser {
	c.limitBody()
	return c.Request.Body
}

// bodyError maps an over-limit body read to ErrBodyTooLarge. | bodyError 把超限的请求体读取映射为 ErrBodyTooLarge。
func bodyError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return errx.Wrap(err, ErrBodyTooLarge)
	}
	return err
}

// FormValue returns a form value (parsing the request body when necessary, bounded by MaxBodySize). | FormValue 返回表单值（必要时解析请求体，受 MaxBodySize 限制）。
func (c *Ctx) FormValue(key string) string {
	c.limitBody()
	if err := c.Request.ParseMultipartForm(defaultMultipartMemory); err != nil {
		_ = c.Request.ParseForm()
	}
	if v := c.Request.Form.Get(key); v != "" {
		return v
	}
	return c.Request.PostForm.Get(key)
}

// FormFile returns an uploaded file. | FormFile 返回上传文件。
func (c *Ctx) FormFile(key string) (multipart.File, *multipart.FileHeader, error) {
	c.limitBody()
	if err := c.Request.ParseMultipartForm(defaultMultipartMemory); err != nil {
		return nil, nil, bodyError(err)
	}
	return c.Request.FormFile(key)
}

// Header returns a request header. | Header 返回请求头。
func (c *Ctx) Header(key string) string { return c.Request.Header.Get(key) }

// Cookie returns the named cookie, or an empty string when it is absent. | Cookie 返回指定 cookie，不存在返回空串。
func (c *Ctx) Cookie(name string) string {
	cookie, err := c.Request.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// ---------------------------------------------------------------- Response information and writing | 响应信息与写入

// SetStatus records the status code; the actual write happens on the first body write or an explicit WriteHeader call. | SetStatus 记录状态码；实际写出发生在首次写入正文或显式调用 WriteHeader 时。
func (c *Ctx) SetStatus(code int) { c.status = code }

// StatusCode returns the recorded status code (0 when it has not been set). | StatusCode 返回已记录的状态码（未设置时为 0）。
func (c *Ctx) StatusCode() int { return c.status }

// Written reports whether the response header or body has been written. | Written 报告响应头或正文是否已写出。
func (c *Ctx) Written() bool { return c.written }

// SetHeader sets a response header (effective before the response header is written). | SetHeader 设置响应头（在写出响应头之前有效）。
func (c *Ctx) SetHeader(key, value string) { c.Writer.Header().Set(key, value) }

// AddHeader appends a response header. | AddHeader 追加响应头。
func (c *Ctx) AddHeader(key, value string) { c.Writer.Header().Add(key, value) }

// SetCookie writes a response cookie. | SetCookie 写入一个响应 cookie。
func (c *Ctx) SetCookie(cookie *http.Cookie) { http.SetCookie(c.Writer, cookie) }

// WriteHeader explicitly writes the response header. | WriteHeader 显式写出响应头。
func (c *Ctx) WriteHeader(code int) {
	if c.written {
		return
	}
	c.status = code
	c.written = true
	c.Writer.WriteHeader(code)
}

// Write implements io.Writer; the status code defaults to 200 when it is not set explicitly. | Write 实现 io.Writer；未显式设置状态码时默认 200。
func (c *Ctx) Write(b []byte) (int, error) {
	if !c.written {
		c.WriteHeader(http.StatusOK)
	}
	return c.Writer.Write(b)
}

// Flush pushes buffered content to the client (the response writer must implement http.Flusher). | Flush 把缓冲内容推送到客户端（响应写入器需实现 http.Flusher）。
func (c *Ctx) Flush() {
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}
}

// String writes plain text. | String 输出纯文本。
func (c *Ctx) String(code int, s string) error {
	c.SetHeader("Content-Type", "text/plain; charset=utf-8")
	c.WriteHeader(code)
	_, err := io.WriteString(c.Writer, s)
	return err
}

// HTMLString writes already rendered HTML text. | HTMLString 输出已渲染好的 HTML 文本。
func (c *Ctx) HTMLString(code int, s string) error {
	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	c.WriteHeader(code)
	_, err := io.WriteString(c.Writer, s)
	return err
}

// HTML renders with the injected template engine and writes it as text/html. | HTML 使用已注入的模板引擎渲染并以 text/html 输出。
func (c *Ctx) HTML(code int, name string, data any) error {
	if c.router.renderer == nil {
		return errx.Detail(ErrNoRenderer, "use SetRenderer or c.HTMLString | 请使用 SetRenderer 或 c.HTMLString")
	}
	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	c.WriteHeader(code)
	return c.router.renderer.Render(c.Writer, name, data)
}

// JSON serializes and writes the value using the framework's adaptive JSON layer. | JSON 使用框架自适应 JSON 层序列化输出。
func (c *Ctx) JSON(code int, v any) error {
	buf, err := jsonx.Marshal(v)
	if err != nil {
		c.SetHeader("Content-Type", "application/json; charset=utf-8")
		c.WriteHeader(http.StatusInternalServerError)
		return err
	}
	c.SetHeader("Content-Type", "application/json; charset=utf-8")
	c.WriteHeader(code)
	_, err = c.Writer.Write(buf)
	return err
}

// JSONStream writes v as JSON incrementally through the adaptive encoder. | JSONStream 通过自适应编码器增量输出 JSON。
//
// Unlike JSON, the value is encoded straight into the response instead of into a full-size buffer,
// so a very large slice or document does not need a second copy in memory. The trade-off is error
// atomicity: an encoding error can surface after the header and part of the body are already
// written, so values that can fail to encode should still use JSON. The encoder appends a trailing
// newline. | 与 JSON 不同，它把值直接编码进响应而不是先写入一份完整大小的缓冲，因此巨大的切片或
// 文档不需要在内存中再复制一份。代价是错误原子性：编码错误可能在响应头与部分正文写出之后才出现，
// 因此可能编码失败的值仍应使用 JSON。编码器会在末尾追加一个换行。
func (c *Ctx) JSONStream(code int, v any) error {
	c.SetHeader("Content-Type", "application/json; charset=utf-8")
	c.WriteHeader(code)
	return jsonx.NewEncoder(c.Writer).Encode(v)
}

// XML writes XML (standard library encoding/xml). | XML 输出 XML（标准库 encoding/xml）。
func (c *Ctx) XML(code int, v any) error {
	buf, err := xml.Marshal(v)
	if err != nil {
		return err
	}
	c.SetHeader("Content-Type", "application/xml; charset=utf-8")
	c.WriteHeader(code)
	_, err = c.Writer.Write(buf)
	return err
}

// Blob writes a byte block with the given Content-Type. | Blob 输出指定 Content-Type 的字节块。
func (c *Ctx) Blob(code int, contentType string, b []byte) error {
	if contentType != "" {
		c.SetHeader("Content-Type", contentType)
	}
	c.WriteHeader(code)
	_, err := c.Writer.Write(b)
	return err
}

// Stream writes in streaming mode (combined with c.Flush() it supports SSE and other long-lived connections). | Stream 以流式方式输出（配合 c.Flush() 可实现 SSE 等长连接）。
func (c *Ctx) Stream(contentType string, fn func(w io.Writer) error) error {
	if contentType != "" {
		c.SetHeader("Content-Type", contentType)
	}
	c.WriteHeader(http.StatusOK)
	return fn(c.Writer)
}

// NoContent writes 204 (or the given status code) with no body. | NoContent 输出 204（或指定状态码）且不带正文。
func (c *Ctx) NoContent(code ...int) error {
	st := http.StatusNoContent
	if len(code) > 0 {
		st = code[0]
	}
	c.WriteHeader(st)
	return nil
}

// ErrRedirectStatus is returned by c.Redirect when the status code is outside the 3xx range. | ErrRedirectStatus 在重定向状态码不在 3xx 区间时由 c.Redirect 返回。
var ErrRedirectStatus = errx.New("router: redirect status must be in the 3xx range | router：重定向状态码必须在 3xx 区间")

// Redirect writes a redirect response. | Redirect 输出重定向响应。
func (c *Ctx) Redirect(code int, location string) error {
	if code < 300 || code > 399 {
		return ErrRedirectStatus
	}
	http.Redirect(c.Writer, c.Request, location, code)
	c.written = true
	c.status = code
	return nil
}

// ---------------------------------------------------------------- Request-level key/value | 请求级键值

// Set stores a value in this request's context (shared between middleware and handlers). | Set 在本次请求的上下文中保存一个值（供中间件与处理器共享）。
func (c *Ctx) Set(key string, value any) {
	if c.keys == nil {
		c.keys = make(map[string]any, 4)
	}
	c.keys[key] = value
}

// Get reads a request-level value. | Get 读取请求级值。
func (c *Ctx) Get(key string) (any, bool) {
	v, ok := c.keys[key]
	return v, ok
}

// MustGet reads a request-level value and returns nil when it is absent. | MustGet 读取请求级值，不存在时返回 nil。
func (c *Ctx) MustGet(key string) any { return c.keys[key] }

// Deadline implements the context.Context delegation, making it easy to pass Ctx to APIs that need a context. | Deadline 实现 context.Context 委托，便于把 Ctx 传入需要 context 的 API。
func (c *Ctx) Deadline() (time.Time, bool) { return c.Request.Context().Deadline() }

// Done implements the context.Context delegation. | Done 实现 context.Context 委托。
func (c *Ctx) Done() <-chan struct{} { return c.Request.Context().Done() }

// Err implements the context.Context delegation. | Err 实现 context.Context 委托。
func (c *Ctx) Err() error { return c.Request.Context().Err() }

// Value implements the context.Context delegation. | Value 实现 context.Context 委托。
func (c *Ctx) Value(key any) any { return c.Request.Context().Value(key) }
