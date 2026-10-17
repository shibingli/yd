package router

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
	"github.com/spf13/afero"
)

// Default configuration values. | 默认配置值。
const (
	defaultMaxBodySize = 32 << 20 // default max body size: 32MB | 32MB
	// defaultMultipartMemory is the in-memory budget of ParseMultipartForm; the total body is
	// bounded separately by http.MaxBytesReader. | defaultMultipartMemory 是 ParseMultipartForm 的
	// 内存预算；请求体总量由 http.MaxBytesReader 单独限制。
	defaultMultipartMemory = 8 << 20
)

// routeTableBrokenBody is the response body ServeHTTP writes when the route table
// carries wiring errors. | routeTableBrokenBody 是路由表存在接线错误时 ServeHTTP 写出的响应正文。
const routeTableBrokenBody = "router: route table is invalid | router：路由表存在接线错误"

// Fallback response bodies are byte slices so that writing them does not convert a string on
// every miss. | 兜底响应正文预置为字节切片，避免每次未命中都做一次字符串到字节切片的转换。
var (
	methodNotAllowedBody = []byte("method not allowed | 方法不允许")
	notFoundBody         = []byte("not found | 未找到")
)

// Route wiring errors, declared once at package level. | 路由接线错误，在包级声明一次。
var (
	// ErrNilHandler indicates a nil handler or a nil middleware inside a call chain. | ErrNilHandler 表示调用链中存在空的处理器或中间件。
	ErrNilHandler = errx.New("router: nil handler | router：处理器为 nil")
	// ErrInvalidHandler indicates a handler stored in the tree that is not a call chain. | ErrInvalidHandler 表示树中存放的处理器不是调用链类型。
	ErrInvalidHandler = errx.New("router: handler is not a call chain | router：处理器不是调用链类型")
)

// RouteInfo describes a registered route, used for OpenAPI documents and diagnostic output. | RouteInfo 描述一条已注册的路由，供 OpenAPI 文档与诊断输出使用。
type RouteInfo struct {
	// Handler is the final handler (without the middleware attached to the route group). | Handler 为最终处理器（不含该路由组挂载的中间件）。
	Handler HandlerFunc
	// Method is the HTTP method. | Method 为 HTTP 方法。
	Method string
	// Pattern is the original pattern as registered (including :param and *wildcard). | Pattern 为注册时的原始模式（含 :param、*wildcard）。
	Pattern string
}

// Options holds the router construction parameters. | Options 是路由器构造参数。
type Options struct {
	// NotFound is a custom 404 handler. | NotFound 自定义 404 处理器。
	NotFound HandlerFunc
	// MethodNotAllowed is a custom 405 handler; when nil an empty response with an Allow header is written. | MethodNotAllowed 自定义 405 处理器；为 nil 时输出带 Allow 头的空响应。
	MethodNotAllowed HandlerFunc
	// PanicHandler is a custom panic handler; when nil the panic keeps propagating upward, | PanicHandler 自定义 panic 处理器；为 nil 时 panic 会继续向上抛出，
	// leaving it to outer middleware such as middleware.Recover. | 交由 middleware.Recover 等外层中间件处理。
	PanicHandler func(c *Ctx, recovered any)
	// MaxBodySize limits the request body size in bytes; <=0 means 32MB. | MaxBodySize 限制请求体大小（字节），<=0 时取 32MB。
	MaxBodySize int64
	// AutoOptions, when true, automatically answers OPTIONS requests for registered paths (returning an Allow header). | AutoOptions 为 true 时，对已注册路径自动响应 OPTIONS 请求（返回 Allow 头）。
	AutoOptions bool
}

// Router is an HTTP router based on the self-developed radix tree, implementing http.Handler. | Router 是基于自研基数树的 HTTP 路由，实现 http.Handler。
type Router struct {
	pool     sync.Pool
	renderer Renderer
	tree     *Tree

	// state is the immutable serving snapshot: global middleware plus the broken flag. It is
	// replaced only during wiring, so the request path performs a single pointer load instead
	// of taking the registration lock (and touching its cache line) on every request. | state
	// 是不可变的服务快照：全局中间件与损坏标记。它只在装配期替换，因此请求路径只需一次指针加载，
	// 不必每个请求都去抢注册锁（并触碰其缓存行）。
	state   atomic.Pointer[serveState]
	options Options

	routes   []RouteInfo
	groups   []*Group
	globalMW []Middleware
	errs     []error

	mu sync.RWMutex
	// brokenFlag is the registration-side source of truth behind state.broken. | brokenFlag 是
	// state.broken 在注册侧的权威来源。
	brokenFlag bool
}

// serveState is the immutable snapshot read by ServeHTTP. | serveState 是 ServeHTTP 读取的不可变快照。
type serveState struct {
	globalMW []Middleware
	broken   bool
}

// publishStateLocked replaces the serving snapshot; the caller must hold mu. | publishStateLocked
// 替换服务快照；调用方必须持有 mu。
func (r *Router) publishStateLocked() {
	r.state.Store(&serveState{
		globalMW: append([]Middleware(nil), r.globalMW...),
		broken:   r.brokenFlag,
	})
}

// New creates a router. | New 创建一个路由器。
func New(opts ...func(*Options)) *Router {
	o := Options{AutoOptions: true}
	for _, fn := range opts {
		fn(&o)
	}
	if o.MaxBodySize <= 0 {
		o.MaxBodySize = defaultMaxBodySize
	}
	r := &Router{tree: NewTree(), options: o}
	r.pool.New = func() any { return &Ctx{} }
	r.state.Store(&serveState{})
	return r
}

// WithMaxBodySize sets the request body limit. | WithMaxBodySize 设置请求体上限。
func WithMaxBodySize(n int64) func(*Options) { return func(o *Options) { o.MaxBodySize = n } }

// WithNotFound sets a custom 404 handler. | WithNotFound 设置自定义 404 处理器。
func WithNotFound(h HandlerFunc) func(*Options) { return func(o *Options) { o.NotFound = h } }

// WithMethodNotAllowed sets a custom 405 handler. | WithMethodNotAllowed 设置自定义 405 处理器。
func WithMethodNotAllowed(h HandlerFunc) func(*Options) {
	return func(o *Options) { o.MethodNotAllowed = h }
}

// WithPanicHandler sets the panic handler. | WithPanicHandler 设置 panic 处理器。
func WithPanicHandler(h func(*Ctx, any)) func(*Options) {
	return func(o *Options) { o.PanicHandler = h }
}

// WithAutoOptions controls whether OPTIONS is answered automatically. | WithAutoOptions 控制是否自动响应 OPTIONS。
func WithAutoOptions(on bool) func(*Options) { return func(o *Options) { o.AutoOptions = on } }

func (r *Router) maxBodySize() int64 { return r.options.MaxBodySize }

// SetRenderer injects the template renderer (usually called by app during the wiring stage). | SetRenderer 注入模板渲染器（通常由 app 在装配阶段调用）。
func (r *Router) SetRenderer(rd Renderer) { r.renderer = rd }

// Use registers global middleware; it must be called before the server starts. | Use 注册全局中间件；必须在开始服务前调用。
func (r *Router) Use(mw ...Middleware) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.globalMW = append(r.globalMW, mw...)
	r.publishStateLocked()
}

// Group creates a route group with a common prefix and middleware. | Group 创建一个带公共前缀与中间件的路由组。
func (r *Router) Group(prefix string, mw ...Middleware) *Group {
	g := &Group{router: r, prefix: strings.TrimSuffix(prefix, "/"), middleware: mw}
	r.mu.Lock()
	r.groups = append(r.groups, g)
	r.mu.Unlock()
	return g
}

// GET registers a GET route. | GET 注册 GET 路由。
func (r *Router) GET(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodGet, pattern, h, mw...)
}

// POST registers a POST route. | POST 注册 POST 路由。
func (r *Router) POST(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodPost, pattern, h, mw...)
}

// PUT registers a PUT route. | PUT 注册 PUT 路由。
func (r *Router) PUT(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodPut, pattern, h, mw...)
}

// PATCH registers a PATCH route. | PATCH 注册 PATCH 路由。
func (r *Router) PATCH(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodPatch, pattern, h, mw...)
}

// DELETE registers a DELETE route. | DELETE 注册 DELETE 路由。
func (r *Router) DELETE(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodDelete, pattern, h, mw...)
}

// HEAD registers a HEAD route. | HEAD 注册 HEAD 路由。
func (r *Router) HEAD(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodHead, pattern, h, mw...)
}

// OPTIONS registers an OPTIONS route. | OPTIONS 注册 OPTIONS 路由。
func (r *Router) OPTIONS(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodOptions, pattern, h, mw...)
}

// Handle registers a route with an arbitrary HTTP method. | Handle 以任意 HTTP 方法注册路由。
func (r *Router) Handle(method, pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(method, pattern, h, mw...)
}

func (r *Router) handle(method, pattern string, h HandlerFunc, mw ...Middleware) {
	if h == nil {
		r.recordWiringError(errx.Detail(ErrNilHandler, "%s %s", method, pattern))
		return
	}
	// The tree stores the full call chain of "route-level middleware + business handler", | 树中存放「路由级中间件 + 业务处理器」的完整调用链，
	// while global middleware is prepended at ServeHTTP time (because it can still be appended after registration). | 全局中间件在 ServeHTTP 时前置拼接（因为它可以在注册之后再追加）。
	chain := make([]HandlerFunc, 0, len(mw)+1)
	chain = append(chain, mw...)
	chain = append(chain, h)
	if err := r.tree.Add(method, pattern, chain); err != nil {
		// Registration must never take the process down: the error is recorded here and | 注册绝不能拖垮进程：错误在此登记，
		// reported once by Validate (or refused with a 500 by ServeHTTP). | 由 Validate 一次性报告（或由 ServeHTTP 直接以 500 拒绝）。
		r.recordWiringError(err)
		return
	}
	r.mu.Lock()
	r.routes = append(r.routes, RouteInfo{Method: method, Pattern: pattern, Handler: h})
	r.mu.Unlock()
}

// recordWiringError registers one wiring failure and marks the router as broken. | recordWiringError 登记一个接线失败并把路由器标记为损坏。
func (r *Router) recordWiringError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	r.errs = append(r.errs, err)
	r.brokenFlag = true
	r.publishStateLocked()
	r.mu.Unlock()
}

// Validate reports every wiring error collected while routes were registered, plus any
// structural defect found in the route tree. | Validate 一次性报告路由注册期间收集的全部接线错误，
// 以及路由树中发现的任何结构性缺陷。
//
// It is meant to run once at startup, after the last route is registered and before serving.
// A non-nil result means the route table must not serve; ServeHTTP enforces the same rule as
// soon as a registration error is recorded. | 该方法用于启动期在最后一条路由注册之后、开始服务之前
// 调用一次。返回非 nil 表示路由表不可用于服务；只要有注册错误被登记，ServeHTTP 就会执行同一规则。
func (r *Router) Validate() error {
	r.mu.RLock()
	errs := append([]error(nil), r.errs...)
	r.mu.RUnlock()
	if err := r.validateTree(); err != nil {
		errs = append(errs, err)
		r.mu.Lock()
		r.brokenFlag = true
		r.publishStateLocked()
		r.mu.Unlock()
	}
	return errors.Join(errs...)
}

// validateTree checks that every handler stored in the tree is a non-empty call chain
// whose elements are all non-nil. | validateTree 校验树中每个处理器都是非空调用链，且链上元素均非 nil。
func (r *Router) validateTree() error {
	var errs []error
	r.tree.Walk(func(method, pattern string, handler any) {
		chain, ok := handler.([]HandlerFunc)
		if !ok {
			errs = append(errs, errx.Detail(ErrInvalidHandler, "%s %s", method, pattern))
			return
		}
		if len(chain) == 0 {
			errs = append(errs, errx.Detail(ErrNilHandler, "%s %s", method, pattern))
			return
		}
		for _, h := range chain {
			if h == nil {
				errs = append(errs, errx.Detail(ErrNilHandler, "%s %s", method, pattern))
				return
			}
		}
	})
	return errors.Join(errs...)
}

// Static mounts a local directory at urlPrefix (its contents are served as static files). | Static 把本地目录挂载到 urlPrefix（目录内容以静态文件方式提供）。
//
// Internally based on afero.BasePathFs + http.FileServer, it automatically supports Range requests, | 内部基于 afero.BasePathFs + http.FileServer，自动支持 Range 请求、
// ETag / Last-Modified / If-Modified-Since and other standard semantics. | ETag / Last-Modified / If-Modified-Since 等标准语义。
func (r *Router) Static(urlPrefix, dir string) {
	r.StaticFS(urlPrefix, afero.NewBasePathFs(afero.NewOsFs(), dir))
}

// StaticFS mounts any afero file system at urlPrefix (a MemFS or go:embed assets can be mounted). | StaticFS 把任意 afero 文件系统挂载到 urlPrefix（可挂载 MemFS 或 go:embed 资源）。
func (r *Router) StaticFS(urlPrefix string, fs afero.Fs) {
	prefix := strings.TrimSuffix(urlPrefix, "/")
	if prefix == "" {
		prefix = "/static"
	}
	srv := http.FileServer(afs.NewHTTPFileSystem(fs))
	r.GET(prefix+"/*filepath", func(c *Ctx) {
		orig := c.Request.URL.Path
		c.Request.URL.Path = "/" + c.Param("filepath")
		defer func() { c.Request.URL.Path = orig }()
		srv.ServeHTTP(c.Writer, c.Request)
		c.SetStatus(http.StatusOK)
	})
}

// Routes returns a snapshot of the registered routes (sorted by method and pattern, stable and reproducible). | Routes 返回已注册路由的快照（按 method、pattern 排序，结果稳定可复现）。
func (r *Router) Routes() []RouteInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RouteInfo, len(r.routes))
	copy(out, r.routes)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pattern != out[j].Pattern {
			return out[i].Pattern < out[j].Pattern
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// ServeHTTP implements http.Handler. | ServeHTTP 实现 http.Handler。
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// A route table with recorded wiring errors must never serve traffic, not even the
	// routes that did register successfully. | 存在接线错误记录的路由表绝不能对外服务，
	// 即使其中某些路由注册成功了也不行。
	state := r.state.Load()
	if state == nil || state.broken {
		http.Error(w, routeTableBrokenBody, http.StatusInternalServerError)
		return
	}

	c := r.pool.Get().(*Ctx)
	c.reset(w, req, r)
	// Bound every body read once per request. ParseMultipartForm's argument is a memory hint, not a
	// body limit, so without this a multipart upload could exceed MaxBodySize and spill arbitrary
	// bytes to temp files. | 每个请求只包装一次请求体读取。ParseMultipartForm 的参数是内存提示而不是
	// 请求体上限，缺少这层包装时 multipart 上传可以超出 MaxBodySize 并把任意字节落到临时文件。
	c.limitBody()

	defer func() {
		if rec := recover(); rec != nil {
			if r.options.PanicHandler != nil {
				r.options.PanicHandler(c, rec)
			} else {
				c.Params = nil
				c.Request = nil
				c.Writer = nil
				c.keys = nil
				r.pool.Put(c)
				// Re-raise instead of swallowing: the request must not continue after a panic, and
				// the outer Recover middleware owns the final response. | 重新抛出而不是吞掉 panic：
				// 请求不得在 panic 之后继续执行，最终响应由更外层的 Recover 中间件负责。
				panic(rec)
			}
		}
		c.Params = nil
		c.Request = nil
		c.Writer = nil
		c.keys = nil
		r.pool.Put(c)
	}()

	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	c.path = path

	handler, tsr := r.tree.Find(req.Method, path, c.Params)
	// HEAD falls back to GET: the server discards the body of a HEAD response, so the handler can be
	// shared. | HEAD 回退到 GET：服务器会丢弃 HEAD 响应的正文，因此处理器可以复用。
	if handler == nil && req.Method == http.MethodHead {
		c.Params.Truncate(0)
		if h, t := r.tree.Find(http.MethodGet, path, c.Params); h != nil {
			handler, tsr = h, t
		}
	}

	// No match: check the trailing slash redirect first, then 405, and finally 404. | 未命中：优先判定尾斜杠重定向，再判定 405，最后 404。
	if handler == nil {
		methods := r.tree.Allowed(path)
		if len(methods) > 0 {
			if req.Method == http.MethodOptions && r.options.AutoOptions {
				c.SetHeader("Allow", strings.Join(withOptions(methods, r.options.AutoOptions), ", "))
				_ = c.NoContent()
				return
			}
			if r.options.MethodNotAllowed != nil {
				c.SetHeader("Allow", strings.Join(withOptions(methods, r.options.AutoOptions), ", "))
				r.setChain(c, state.globalMW, nil, r.options.MethodNotAllowed)
				c.Next()
				return
			}
			c.SetHeader("Allow", strings.Join(withOptions(methods, r.options.AutoOptions), ", "))
			writeFallback(c, http.StatusMethodNotAllowed, methodNotAllowedBody)
			return
		}
		if tsr {
			redirectTrailingSlash(c, path, req.URL.RawQuery)
			return
		}
		if r.options.NotFound != nil {
			r.setChain(c, state.globalMW, nil, r.options.NotFound)
			c.Next()
			return
		}
		writeFallback(c, http.StatusNotFound, notFoundBody)
		return
	}

	routeChain, ok := handler.([]HandlerFunc)
	if !ok {
		// An internal invariant, not a caller mistake: log it and answer 500 instead of
		// taking the whole process down. | 这是内部不变量而非调用方错误：记录日志并返回 500，
		// 而不是让整个进程崩溃。
		logx.Default().Error("router: handler in the tree is not a call chain | router：树中的处理器不是调用链类型",
			logx.Str("method", req.Method), logx.Str("path", path))
		http.Error(w, routeTableBrokenBody, http.StatusInternalServerError)
		return
	}
	r.setChain(c, state.globalMW, routeChain, nil)
	// The overwhelmingly common case is a single business handler with no global middleware;
	// calling it directly skips the Next loop without changing middleware semantics. | 最常见的
	// 情况是没有全局中间件的单个业务处理器；直接调用可以跳过 Next 循环，且不改变中间件语义。
	if len(c.handlers) == 1 {
		// Keep the same index semantics as Next: a handler that calls Next() must see the chain
		// exhausted, not re-enter itself. | 与 Next 保持相同的索引语义：处理器内部调用 Next 时必须
		// 看到调用链已结束，而不是再次进入自身。
		c.index = 0
		c.handlers[0](c)
		return
	}
	c.Next()
}

// setChain assembles "global middleware → route chain (or a single fallback handler)" and writes it into the reusable buffer of c. | setChain 组装「全局中间件 → 路由链（或单个兜底处理器）」并写入 c 的复用缓冲区。
//
// The global middleware slice comes from the immutable serveState snapshot, so no lock is taken;
// the function reuses the backing array of Ctx.handlers, so the hot path that hits an existing
// route performs no heap allocation. | 全局中间件切片来自不可变的 serveState 快照，因此无需加锁；
// 函数复用 Ctx.handlers 的底层数组，命中已有路由的热路径不产生堆分配。
func (r *Router) setChain(c *Ctx, globalMW []Middleware, route []HandlerFunc, tail HandlerFunc) {
	// With no global middleware the route chain is already exactly what Next must run, so borrow
	// it instead of copying; reset drops the borrowed slice before the context is reused. | 没有
	// 全局中间件时，路由链本身就是 Next 要执行的序列，直接借用而不是复制；上下文复用前由 reset
	// 丢弃借用的切片。
	if tail == nil && len(globalMW) == 0 {
		c.handlers = route
		c.borrowed = true
		return
	}
	if c.borrowed {
		c.handlers = nil
		c.borrowed = false
	}
	c.handlers = append(c.handlers[:0], globalMW...)
	if tail != nil {
		c.handlers = append(c.handlers, tail)
		return
	}
	c.handlers = append(c.handlers, route...)
}

// withOptions adds the implicit HEAD (when GET exists) and OPTIONS (when automatic responses are
// enabled) to the Allow list. | withOptions 在 Allow 列表中补充隐式 HEAD（存在 GET 时）与 OPTIONS
// （自动响应已开启时）。
func withOptions(methods []string, auto bool) []string {
	var hasGet, hasHead, hasOptions bool
	for _, m := range methods {
		switch m {
		case http.MethodGet:
			hasGet = true
		case http.MethodHead:
			hasHead = true
		case http.MethodOptions:
			hasOptions = true
		}
	}
	needHead := hasGet && !hasHead
	needOptions := auto && !hasOptions
	if !needHead && !needOptions {
		return methods
	}
	out := append(make([]string, 0, len(methods)+2), methods...)
	if needHead {
		out = append(out, http.MethodHead)
	}
	if needOptions {
		out = append(out, http.MethodOptions)
	}
	sort.Strings(out)
	return out
}

// writeFallback writes a plain text fallback response with a precomputed body. | writeFallback
// 使用预置正文写出纯文本兜底响应。
func writeFallback(c *Ctx, code int, body []byte) {
	c.SetHeader("Content-Type", "text/plain; charset=utf-8")
	c.WriteHeader(code)
	_, _ = c.Writer.Write(body)
}

// redirectTrailingSlash builds the trailing slash redirect: 301 for GET, 308 for the other methods to preserve method and body. | redirectTrailingSlash 生成尾斜杠重定向：GET 用 301，其余方法用 308 保留方法与请求体。
func redirectTrailingSlash(c *Ctx, path, rawQuery string) {
	target := path
	if strings.HasSuffix(path, "/") {
		target = strings.TrimSuffix(path, "/")
	} else {
		target += "/"
	}
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	code := http.StatusMovedPermanently
	if c.Request.Method != http.MethodGet {
		code = http.StatusPermanentRedirect
	}
	http.Redirect(c.Writer, c.Request, target, code)
	c.SetStatus(code)
}

// ErrNoRenderer is returned by c.HTML when no template renderer has been injected. | ErrNoRenderer 在未注入模板渲染器时由 c.HTML 返回。
var ErrNoRenderer = errx.New("router: no template renderer injected | router：未注入模板渲染器")
