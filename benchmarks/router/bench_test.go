package routerbench

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/gofiber/fiber/v3"
	"github.com/julienschmidt/httprouter"
	"github.com/labstack/echo/v4"
	"github.com/valyala/fasthttp"

	ydrouter "github.com/shibingli/ydx/router"
)

// benchSelected reports whether name passes the YD_BENCH_ONLY filter. Set it to
// one comma-separated list of router names to run each implementation in its own
// process and remove same-process ordering and GC bias. | benchSelected 判断
// name 是否通过 YD_BENCH_ONLY 过滤。设为逗号分隔的路由器名可让每个实现独立
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

// routeNopWriter is a reusable response writer with no body or header work. | routeNopWriter
// 是可复用的响应写入器，不做正文与响应头工作。
type routeNopWriter struct {
	header http.Header
	status int
}

// Header returns the header map. | Header 返回头部映射。
func (w *routeNopWriter) Header() http.Header { return w.header }

// Write discards the body. | Write 丢弃正文。
func (w *routeNopWriter) Write(p []byte) (int, error) { return len(p), nil }

// WriteHeader records the status code. | WriteHeader 记录状态码。
func (w *routeNopWriter) WriteHeader(code int) { w.status = code }

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

// registerYd registers the shared route set on the yd router and validates the
// wiring. | registerYd 在 yd 路由器上注册共用路由并校验接线。
func registerYd(tb testing.TB, r *ydrouter.Router) {
	tb.Helper()
	h := func(c *ydrouter.Ctx) { c.WriteHeader(http.StatusNoContent) }
	r.GET("/static/ping", h)
	r.GET("/users/:id", h)
	r.GET("/users/:id/posts", h)
	r.GET("/files/*filepath", h)
	if err := r.Validate(); err != nil {
		tb.Fatalf("yd router validation: %v | yd 路由校验失败", err)
	}
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

// wantStatus returns the status the shared handler must produce for sc. | wantStatus
// 返回共用处理器对 sc 必须产生的状态码。
func wantStatus(sc routeScenario) int {
	if sc.name == "miss" {
		return http.StatusNotFound
	}
	return http.StatusNoContent
}

// verifyHTTP proves one http.Handler really reaches the expected route. | verifyHTTP
// 证明一个 http.Handler 确实命中了预期路由。
func verifyHTTP(tb testing.TB, h http.Handler, sc routeScenario) {
	tb.Helper()
	w := &routeNopWriter{header: make(http.Header)}
	h.ServeHTTP(w, newRouteRequest(sc.path))
	if want := wantStatus(sc); w.status != want {
		tb.Fatalf("%s: status = %d, want %d | 路由命中状态错误", sc.name, w.status, want)
	}
}

// verifyFiber proves the fiber application really reaches the expected route. | verifyFiber
// 证明 fiber 应用确实命中了预期路由。
func verifyFiber(tb testing.TB, app *fiber.App, sc routeScenario) {
	tb.Helper()
	var ctx fasthttp.RequestCtx
	var req fasthttp.Request
	req.Header.SetMethod(http.MethodGet)
	req.SetRequestURI(sc.path)
	ctx.Init(&req, nil, nil)
	app.Handler()(&ctx)
	if want := wantStatus(sc); ctx.Response.StatusCode() != want {
		tb.Fatalf("%s: fiber status = %d, want %d | fiber 路由命中状态错误",
			sc.name, ctx.Response.StatusCode(), want)
	}
}

// TestRoutesReachHandlers proves every router registration reaches the shared
// handler instead of silently benchmarking a 404 path. | TestRoutesReachHandlers
// 证明每个路由器的注册都命中共用处理器，而不是悄悄基准一个 404 路径。
func TestRoutesReachHandlers(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	for _, sc := range routeScenarios {
		t.Run(sc.name, func(t *testing.T) {
			r := ydrouter.New()
			registerYd(t, r)
			verifyHTTP(t, r, sc)

			m := http.NewServeMux()
			registerStdlib(m)
			verifyHTTP(t, m, sc)

			g := gin.New()
			registerGin(g)
			verifyHTTP(t, g, sc)

			c := chi.NewRouter()
			registerChi(c)
			verifyHTTP(t, c, sc)

			e := echo.New()
			registerEcho(e)
			verifyHTTP(t, e, sc)

			hr := httprouter.New()
			registerHTTPRouter(hr)
			verifyHTTP(t, hr, sc)

			app := fiber.New()
			registerFiber(app)
			verifyFiber(t, app, sc)
		})
	}
}

// BenchmarkRouters compares in-process static and dynamic routing throughput and
// allocations across mainstream Go routers. It does not exercise the network.
// | BenchmarkRouters 对比主流 Go 路由器在进程内的静态与动态路由吞吐及分配，不经过网络。
func BenchmarkRouters(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)

	for _, sc := range routeScenarios {
		sc := sc
		run := func(name string, fn func(*testing.B)) {
			if benchSelected(name) {
				b.Run(sc.name+"/"+name, fn)
			}
		}

		run("yd", func(b *testing.B) {
			r := ydrouter.New()
			registerYd(b, r)
			verifyHTTP(b, r, sc)
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

		run("stdlib", func(b *testing.B) {
			m := http.NewServeMux()
			registerStdlib(m)
			verifyHTTP(b, m, sc)
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

		run("gin", func(b *testing.B) {
			r := gin.New()
			registerGin(r)
			verifyHTTP(b, r, sc)
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

		run("chi", func(b *testing.B) {
			r := chi.NewRouter()
			registerChi(r)
			verifyHTTP(b, r, sc)
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

		run("echo", func(b *testing.B) {
			e := echo.New()
			registerEcho(e)
			verifyHTTP(b, e, sc)
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

		run("httprouter", func(b *testing.B) {
			r := httprouter.New()
			registerHTTPRouter(r)
			verifyHTTP(b, r, sc)
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

		run("fiber", func(b *testing.B) {
			app := fiber.New()
			registerFiber(app)
			verifyFiber(b, app, sc)
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
