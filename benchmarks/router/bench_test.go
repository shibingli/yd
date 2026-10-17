package routerbench

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/gofiber/fiber/v3"
	"github.com/julienschmidt/httprouter"
	"github.com/labstack/echo/v4"
	"github.com/valyala/fasthttp"

	ydrouter "github.com/shibingli/yd/http/router"
)

// routeNopWriter is a reusable response writer with no body or header work. | routeNopWriter
// 是可复用的响应写入器，不做正文与响应头工作。
type routeNopWriter struct{ header http.Header }

// Header returns the header map. | Header 返回头部映射。
func (w *routeNopWriter) Header() http.Header { return w.header }

// Write discards the body. | Write 丢弃正文。
func (w *routeNopWriter) Write(p []byte) (int, error) { return len(p), nil }

// WriteHeader is a no-op. | WriteHeader 为空操作。
func (w *routeNopWriter) WriteHeader(int) {}

// routeScenario is one request path used by every router. | routeScenario 是各路由器共用的请求路径。
type routeScenario struct {
	name string
	path string
}

// routeScenarios covers static, parameter, deep, wildcard and miss paths. | routeScenarios
// 覆盖静态、参数、深层、通配与未命中路径。
var routeScenarios = []routeScenario{
	{name: "static", path: "/static/ping"},
	{name: "param", path: "/users/42"},
	{name: "deep", path: "/users/42/posts"},
	{name: "wildcard", path: "/files/a/b/c.txt"},
	{name: "miss", path: "/nope"},
}

// newRouteRequest builds one reusable request. | newRouteRequest 构造一个可复用的请求。
func newRouteRequest(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, path, nil)
}

// registerYd registers the shared route set on the yd router. | registerYd 在 yd 路由器上注册共用路由。
func registerYd(r *ydrouter.Router) {
	h := func(c *ydrouter.Ctx) { c.SetStatus(http.StatusNoContent) }
	r.GET("/static/ping", h)
	r.GET("/users/:id", h)
	r.GET("/users/:id/posts", h)
	r.GET("/files/*filepath", h)
}

// registerStdlib registers the shared route set on the standard library mux. | registerStdlib
// 在标准库 mux 上注册共用路由。
func registerStdlib(m *http.ServeMux) {
	h := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	m.HandleFunc("GET /static/ping", h)
	m.HandleFunc("GET /users/{id}", h)
	m.HandleFunc("GET /users/{id}/posts", h)
	m.HandleFunc("GET /files/{path...}", h)
}

// registerGin registers the shared route set on gin. | registerGin 在 gin 上注册共用路由。
func registerGin(r *gin.Engine) {
	h := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	r.GET("/static/ping", h)
	r.GET("/users/:id", h)
	r.GET("/users/:id/posts", h)
	r.GET("/files/*filepath", h)
}

// registerChi registers the shared route set on chi. | registerChi 在 chi 上注册共用路由。
func registerChi(r chi.Router) {
	h := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	r.Get("/static/ping", h)
	r.Get("/users/{id}", h)
	r.Get("/users/{id}/posts", h)
	r.Get("/files/*", h)
}

// registerEcho registers the shared route set on echo. | registerEcho 在 echo 上注册共用路由。
func registerEcho(e *echo.Echo) {
	h := func(c echo.Context) error { return c.NoContent(http.StatusNoContent) }
	e.GET("/static/ping", h)
	e.GET("/users/:id", h)
	e.GET("/users/:id/posts", h)
	e.GET("/files/*", h)
}

// registerHTTPRouter registers the shared route set on httprouter. | registerHTTPRouter
// 在 httprouter 上注册共用路由。
func registerHTTPRouter(r *httprouter.Router) {
	h := func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
		w.WriteHeader(http.StatusNoContent)
	}
	r.GET("/static/ping", h)
	r.GET("/users/:id", h)
	r.GET("/users/:id/posts", h)
	r.GET("/files/*filepath", h)
}

// registerFiber registers the shared route set on fiber. | registerFiber 在 fiber 上注册共用路由。
func registerFiber(app *fiber.App) {
	h := func(c fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) }
	app.Get("/static/ping", h)
	app.Get("/users/:id", h)
	app.Get("/users/:id/posts", h)
	app.Get("/files/*", h)
}

// BenchmarkRouters compares in-process static and dynamic routing throughput and
// allocations across mainstream Go routers. It does not exercise the network.
// | BenchmarkRouters 对比主流 Go 路由器在进程内的静态与动态路由吞吐及分配，不经过网络。
func BenchmarkRouters(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)

	for _, sc := range routeScenarios {
		sc := sc
		b.Run(sc.name+"/yd", func(b *testing.B) {
			r := ydrouter.New()
			registerYd(r)
			req := newRouteRequest(sc.path)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				w := &routeNopWriter{header: make(http.Header)}
				for pb.Next() {
					r.ServeHTTP(w, req)
				}
			})
		})

		b.Run(sc.name+"/stdlib", func(b *testing.B) {
			m := http.NewServeMux()
			registerStdlib(m)
			req := newRouteRequest(sc.path)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				w := &routeNopWriter{header: make(http.Header)}
				for pb.Next() {
					m.ServeHTTP(w, req)
				}
			})
		})

		b.Run(sc.name+"/gin", func(b *testing.B) {
			r := gin.New()
			registerGin(r)
			req := newRouteRequest(sc.path)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				w := &routeNopWriter{header: make(http.Header)}
				for pb.Next() {
					r.ServeHTTP(w, req)
				}
			})
		})

		b.Run(sc.name+"/chi", func(b *testing.B) {
			r := chi.NewRouter()
			registerChi(r)
			req := newRouteRequest(sc.path)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				w := &routeNopWriter{header: make(http.Header)}
				for pb.Next() {
					r.ServeHTTP(w, req)
				}
			})
		})

		b.Run(sc.name+"/echo", func(b *testing.B) {
			e := echo.New()
			registerEcho(e)
			req := newRouteRequest(sc.path)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				w := &routeNopWriter{header: make(http.Header)}
				for pb.Next() {
					e.ServeHTTP(w, req)
				}
			})
		})

		b.Run(sc.name+"/httprouter", func(b *testing.B) {
			r := httprouter.New()
			registerHTTPRouter(r)
			req := newRouteRequest(sc.path)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				w := &routeNopWriter{header: make(http.Header)}
				for pb.Next() {
					r.ServeHTTP(w, req)
				}
			})
		})

		b.Run(sc.name+"/fiber", func(b *testing.B) {
			app := fiber.New()
			registerFiber(app)
			handler := app.Handler()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				var ctx fasthttp.RequestCtx
				var req fasthttp.Request
				req.Header.SetMethod(http.MethodGet)
				req.SetRequestURI(sc.path)
				ctx.Init(&req, nil, nil)
				for pb.Next() {
					handler(&ctx)
				}
			})
		})
	}
}
