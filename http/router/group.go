package router

import (
	"net/http"
	"strings"

	"github.com/spf13/afero"
)

// Group is a set of routes sharing a prefix and middleware. | Group 是一组共享前缀与中间件的路由。
//
// Middleware runs in the order "outer group → inner group → the route itself", matching registration order. | 中间件执行顺序为「外层组 → 内层组 → 路由自身」，与注册顺序一致。
type Group struct {
	router     *Router
	prefix     string
	middleware []Middleware
}

// Prefix returns the path prefix of the group. | Prefix 返回该组的路径前缀。
func (g *Group) Prefix() string { return g.prefix }

// Use appends middleware to the group; call it before registering routes. | Use 为该组追加中间件；应在注册路由之前调用。
func (g *Group) Use(mw ...Middleware) *Group {
	g.middleware = append(g.middleware, mw...)
	return g
}

// Group creates a nested child group. | Group 创建嵌套子组。
func (g *Group) Group(prefix string, mw ...Middleware) *Group {
	child := &Group{
		router:     g.router,
		prefix:     g.prefix + strings.TrimSuffix(prefix, "/"),
		middleware: append(append([]Middleware{}, g.middleware...), mw...),
	}
	g.router.mu.Lock()
	g.router.groups = append(g.router.groups, child)
	g.router.mu.Unlock()
	return child
}

// GET registers a GET route in the group. | GET 在组内注册 GET 路由。
func (g *Group) GET(pattern string, h HandlerFunc, mw ...Middleware) {
	g.handle(http.MethodGet, pattern, h, mw...)
}

// POST registers a POST route in the group. | POST 在组内注册 POST 路由。
func (g *Group) POST(pattern string, h HandlerFunc, mw ...Middleware) {
	g.handle(http.MethodPost, pattern, h, mw...)
}

// PUT registers a PUT route in the group. | PUT 在组内注册 PUT 路由。
func (g *Group) PUT(pattern string, h HandlerFunc, mw ...Middleware) {
	g.handle(http.MethodPut, pattern, h, mw...)
}

// PATCH registers a PATCH route in the group. | PATCH 在组内注册 PATCH 路由。
func (g *Group) PATCH(pattern string, h HandlerFunc, mw ...Middleware) {
	g.handle(http.MethodPatch, pattern, h, mw...)
}

// DELETE registers a DELETE route in the group. | DELETE 在组内注册 DELETE 路由。
func (g *Group) DELETE(pattern string, h HandlerFunc, mw ...Middleware) {
	g.handle(http.MethodDelete, pattern, h, mw...)
}

// HEAD registers a HEAD route in the group. | HEAD 在组内注册 HEAD 路由。
func (g *Group) HEAD(pattern string, h HandlerFunc, mw ...Middleware) {
	g.handle(http.MethodHead, pattern, h, mw...)
}

// OPTIONS registers an OPTIONS route in the group. | OPTIONS 在组内注册 OPTIONS 路由。
func (g *Group) OPTIONS(pattern string, h HandlerFunc, mw ...Middleware) {
	g.handle(http.MethodOptions, pattern, h, mw...)
}

// Handle registers a route in the group with an arbitrary method. | Handle 在组内以任意方法注册路由。
func (g *Group) Handle(method, pattern string, h HandlerFunc, mw ...Middleware) {
	g.handle(method, pattern, h, mw...)
}

// StaticFS mounts a static file system under the group prefix. | StaticFS 在组前缀下挂载静态文件系统。
func (g *Group) StaticFS(urlPrefix string, fs afero.Fs) {
	g.router.StaticFS(g.prefix+urlPrefix, fs)
}

func (g *Group) handle(method, pattern string, h HandlerFunc, mw ...Middleware) {
	// assemble middleware: group middleware first, route-level middleware after | 组装中间件：组中间件在前，路由级中间件在后
	total := append(append([]Middleware{}, g.middleware...), mw...)
	g.router.handle(method, joinPath(g.prefix, pattern), h, total...)
}

// joinPath joins the group prefix and the route pattern, guaranteeing exactly one "/". | joinPath 拼接组前缀与路由模式，保证恰好一个 "/"。
func joinPath(prefix, pattern string) string {
	if prefix == "" {
		if pattern == "" {
			return "/"
		}
		return pattern
	}
	if pattern == "" || pattern == "/" {
		return prefix + "/"
	}
	if !strings.HasPrefix(pattern, "/") {
		return prefix + "/" + pattern
	}
	return prefix + pattern
}
