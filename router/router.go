package router

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/afero"
	"github.com/yun-dev/yd/afs"
)

// 默认配置值。
const (
	defaultMaxBodySize = 32 << 20 // 32MB
)

// RouteInfo 描述一条已注册的路由，供 OpenAPI 文档与诊断输出使用。
type RouteInfo struct {
	// Method 为 HTTP 方法。
	Method string
	// Pattern 为注册时的原始模式（含 :param、*wildcard）。
	Pattern string
	// Handler 为最终处理器（不含该路由组挂载的中间件）。
	Handler HandlerFunc
}

// Options 是路由器构造参数。
type Options struct {
	// MaxBodySize 限制请求体大小（字节），<=0 时取 32MB。
	MaxBodySize int64
	// NotFound 自定义 404 处理器。
	NotFound HandlerFunc
	// MethodNotAllowed 自定义 405 处理器；为 nil 时输出带 Allow 头的空响应。
	MethodNotAllowed HandlerFunc
	// PanicHandler 自定义 panic 处理器；为 nil 时 panic 会继续向上抛出，
	// 交由 middleware.Recover 等外层中间件处理。
	PanicHandler func(c *Ctx, recovered any)
	// AutoOptions 为 true 时，对已注册路径自动响应 OPTIONS 请求（返回 Allow 头）。
	AutoOptions bool
}

// Router 是基于自研基数树的 HTTP 路由，实现 http.Handler。
type Router struct {
	tree     *Tree
	renderer Renderer
	options  Options

	mu       sync.RWMutex
	routes   []RouteInfo
	groups   []*Group
	pool     sync.Pool
	globalMW []Middleware
}

// New 创建一个路由器。
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
	return r
}

// WithMaxBodySize 设置请求体上限。
func WithMaxBodySize(n int64) func(*Options) { return func(o *Options) { o.MaxBodySize = n } }

// WithNotFound 设置自定义 404 处理器。
func WithNotFound(h HandlerFunc) func(*Options) { return func(o *Options) { o.NotFound = h } }

// WithMethodNotAllowed 设置自定义 405 处理器。
func WithMethodNotAllowed(h HandlerFunc) func(*Options) {
	return func(o *Options) { o.MethodNotAllowed = h }
}

// WithPanicHandler 设置 panic 处理器。
func WithPanicHandler(h func(*Ctx, any)) func(*Options) {
	return func(o *Options) { o.PanicHandler = h }
}

// WithAutoOptions 控制是否自动响应 OPTIONS。
func WithAutoOptions(on bool) func(*Options) { return func(o *Options) { o.AutoOptions = on } }

func (r *Router) maxBodySize() int64 { return r.options.MaxBodySize }

// SetRenderer 注入模板渲染器（通常由 app 在装配阶段调用）。
func (r *Router) SetRenderer(rd Renderer) { r.renderer = rd }

// Use 注册全局中间件；必须在开始服务前调用。
func (r *Router) Use(mw ...Middleware) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.globalMW = append(r.globalMW, mw...)
}

// Group 创建一个带公共前缀与中间件的路由组。
func (r *Router) Group(prefix string, mw ...Middleware) *Group {
	g := &Group{router: r, prefix: strings.TrimSuffix(prefix, "/"), middleware: mw}
	r.mu.Lock()
	r.groups = append(r.groups, g)
	r.mu.Unlock()
	return g
}

// GET 注册 GET 路由。
func (r *Router) GET(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodGet, pattern, h, mw...)
}

// POST 注册 POST 路由。
func (r *Router) POST(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodPost, pattern, h, mw...)
}

// PUT 注册 PUT 路由。
func (r *Router) PUT(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodPut, pattern, h, mw...)
}

// PATCH 注册 PATCH 路由。
func (r *Router) PATCH(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodPatch, pattern, h, mw...)
}

// DELETE 注册 DELETE 路由。
func (r *Router) DELETE(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodDelete, pattern, h, mw...)
}

// HEAD 注册 HEAD 路由。
func (r *Router) HEAD(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodHead, pattern, h, mw...)
}

// OPTIONS 注册 OPTIONS 路由。
func (r *Router) OPTIONS(pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(http.MethodOptions, pattern, h, mw...)
}

// Handle 以任意 HTTP 方法注册路由。
func (r *Router) Handle(method, pattern string, h HandlerFunc, mw ...Middleware) {
	r.handle(method, pattern, h, mw...)
}

func (r *Router) handle(method, pattern string, h HandlerFunc, mw ...Middleware) {
	if h == nil {
		panic("router: 处理器不能为 nil: " + method + " " + pattern)
	}
	// 树中存放「路由级中间件 + 业务处理器」的完整调用链，
	// 全局中间件在 ServeHTTP 时前置拼接（因为它可以在注册之后再追加）。
	chain := make([]HandlerFunc, 0, len(mw)+1)
	chain = append(chain, mw...)
	chain = append(chain, h)
	if err := r.tree.Add(method, pattern, chain); err != nil {
		panic(err)
	}
	r.mu.Lock()
	r.routes = append(r.routes, RouteInfo{Method: method, Pattern: pattern, Handler: h})
	r.mu.Unlock()
}

// Static 把本地目录挂载到 urlPrefix（目录内容以静态文件方式提供）。
//
// 内部基于 afero.BasePathFs + http.FileServer，自动支持 Range 请求、
// ETag / Last-Modified / If-Modified-Since 等标准语义。
func (r *Router) Static(urlPrefix, dir string) {
	r.StaticFS(urlPrefix, afero.NewBasePathFs(afero.NewOsFs(), dir))
}

// StaticFS 把任意 afero 文件系统挂载到 urlPrefix（可挂载 MemFS 或 go:embed 资源）。
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

// Routes 返回已注册路由的快照（按 method、pattern 排序，结果稳定可复现）。
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

// ServeHTTP 实现 http.Handler。
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	c := r.pool.Get().(*Ctx)
	c.reset(w, req, r)
	params := acquireParams()
	c.Params = params

	defer func() {
		if rec := recover(); rec != nil {
			if r.options.PanicHandler != nil {
				r.options.PanicHandler(c, rec)
			} else {
				releaseParams(params)
				c.Params = nil
				c.Request = nil
				c.Writer = nil
				c.keys = nil
				r.pool.Put(c)
				panic(rec)
			}
		}
		releaseParams(params)
		c.Params = nil
		c.Request = nil
		c.Writer = nil
		c.keys = nil
		c.handlers = c.handlers[:0]
		r.pool.Put(c)
	}()

	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}

	handler, tsr := r.tree.Find(req.Method, path, c.Params)

	// 未命中：优先判定尾斜杠重定向，再判定 405，最后 404。
	if handler == nil {
		methods := r.tree.Allowed(path)
		if len(methods) > 0 {
			if req.Method == http.MethodOptions && r.options.AutoOptions {
				c.SetHeader("Allow", strings.Join(methods, ", "))
				_ = c.NoContent()
				return
			}
			if r.options.MethodNotAllowed != nil {
				c.SetHeader("Allow", strings.Join(withOptions(methods, r.options.AutoOptions), ", "))
				r.setChain(c, nil, r.options.MethodNotAllowed)
				c.Next()
				return
			}
			c.SetHeader("Allow", strings.Join(withOptions(methods, r.options.AutoOptions), ", "))
			c.SetStatus(http.StatusMethodNotAllowed)
			_ = c.String(http.StatusMethodNotAllowed, http.StatusText(http.StatusMethodNotAllowed))
			return
		}
		if tsr {
			redirectTrailingSlash(c, path, req.URL.RawQuery)
			return
		}
		if r.options.NotFound != nil {
			r.setChain(c, nil, r.options.NotFound)
			c.Next()
			return
		}
		http.NotFound(w, req)
		c.SetStatus(http.StatusNotFound)
		return
	}

	routeChain, ok := handler.([]HandlerFunc)
	if !ok {
		panic("router: 树中出现了非调用链类型的处理器")
	}
	r.setChain(c, routeChain, nil)
	c.Next()
}

// setChain 组装「全局中间件 → 路由链（或单个兜底处理器）」并写入 c 的复用缓冲区。
//
// 复用 Ctx 的 handlers 底层数组，命中已有路由的热路径不产生堆分配。
func (r *Router) setChain(c *Ctx, route []HandlerFunc, tail HandlerFunc) {
	r.mu.RLock()
	c.handlers = append(c.handlers[:0], r.globalMW...)
	r.mu.RUnlock()
	if tail != nil {
		c.handlers = append(c.handlers, tail)
		return
	}
	c.handlers = append(c.handlers, route...)
}

// withOptions 在 Allow 列表中补充 OPTIONS（自动响应已开启时）。
func withOptions(methods []string, auto bool) []string {
	if !auto {
		return methods
	}
	for _, m := range methods {
		if m == http.MethodOptions {
			return methods
		}
	}
	out := append(make([]string, 0, len(methods)+1), methods...)
	out = append(out, http.MethodOptions)
	sort.Strings(out)
	return out
}

// redirectTrailingSlash 生成尾斜杠重定向：GET 用 301，其余方法用 308 保留方法与请求体。
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

// ErrNoRenderer 在未注入模板渲染器时由 c.HTML 返回。
var ErrNoRenderer = errors.New("router: 未注入模板渲染器")
