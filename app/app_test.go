package app

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/afs"
	"github.com/shibingli/yd/config"
	"github.com/shibingli/yd/logx"
	"github.com/shibingli/yd/render"
	"github.com/shibingli/yd/router"
	"github.com/shibingli/yd/server"
)

// TestMain silences the global logger so that the middleware chain wired by
// Default never pollutes the test output. | TestMain 静默全局日志器，避免 Default
// 装配的中间件链污染测试输出。
func TestMain(m *testing.M) {
	previous := logx.Default()
	logx.SetDefault(logx.New(logx.Options{Writer: io.Discard}))
	code := m.Run()
	logx.SetDefault(previous)
	os.Exit(code)
}

// Fixture templates shared by the rendering tests. | 渲染测试共用的模板夹具。
const (
	// testLayout is a layout using the documented Content key. | testLayout 是使用约定 Content 键的布局。
	testLayout = `<!DOCTYPE html><html><head><title>{{ with .Title }}{{ . }}{{ end }}</title></head><body>{{ .Content }}</body></html>`
	// testPage is a page fragment referring to the same data. | testPage 是引用同一份数据的页面片段。
	testPage = `<h1>{{ .Title }}</h1>`
	// wantFragment is testPage rendered with Title = "yd". | wantFragment 是 testPage 在 Title = "yd" 下的渲染结果。
	wantFragment = `<h1>yd</h1>`
	// wantFull is the layout-wrapped whole page of the same fixture pair. | wantFull 是同一组夹具的布局包裹整页。
	wantFull = `<!DOCTYPE html><html><head><title>yd</title></head><body><h1>yd</h1></body></html>`
)

// newTestApp builds an App over an in-memory asset filesystem. | newTestApp 在内存资产文件系统上构造 App。
func newTestApp(t *testing.T, files map[string]string, opts ...Option) *App {
	t.Helper()

	fsys := afs.NewMem()
	for path, content := range files {
		if err := afero.WriteFile(fsys, path, []byte(content), 0o644); err != nil {
			t.Fatalf("写入 %s 失败: %v", path, err)
		}
	}

	cfg, err := config.Load(config.Options{FS: afs.NewMem(), ConfigDir: "."})
	if err != nil {
		t.Fatalf("加载测试配置失败: %v", err)
	}

	base := []Option{
		WithFS(fsys),
		WithConfig(cfg),
		WithLogger(logx.New(logx.Options{Writer: io.Discard})),
	}
	a, err := New(append(base, opts...)...)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	return a
}

// doRequest serves one request through the app router and returns the recorder. | doRequest 通过应用路由处理一次请求并返回记录器。
func doRequest(t *testing.T, a *App, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	return rec
}

// renderHandler returns a handler rendering name with data through a.Render. | renderHandler 返回以 a.Render 渲染 name 的处理器。
func renderHandler(t *testing.T, a *App, status int, name string, data any) router.HandlerFunc {
	t.Helper()
	return func(c *router.Ctx) {
		if err := a.Render(c, status, name, data); err != nil {
			t.Errorf("Render(%q) 失败: %v", name, err)
		}
	}
}

// testData is the shared page data of the rendering tests. | testData 是渲染测试共用的页面数据。
func testData() map[string]any { return map[string]any{"Title": "yd"} }

// ---------------------------------------------------------------- wiring | 装配

func TestDefaultWiresEmbeddedAssets(t *testing.T) {
	a, err := Default()
	if err != nil {
		t.Fatalf("Default 失败: %v", err)
	}

	if a.Router() == nil || a.Server() == nil || a.Config() == nil ||
		a.Logger() == nil || a.FS() == nil || a.Renderer() == nil {
		t.Fatal("Default must wire router / server / config / logger / fs / renderer | Default 必须装配 router / server / config / logger / fs / renderer")
	}

	// Embedded assets: templates, static files and language catalogs. | 内嵌资产：模板、静态资源与语言目录。
	for _, path := range []string{"templates/layouts/base.html", "static/app.css", "langs/zh-CN.yaml"} {
		if !afs.Exists(a.FS(), path) {
			t.Errorf("内嵌资产缺失: %s", path)
		}
	}

	// The static route is mounted and serves the embedded stylesheet. | 静态路由已挂载并能提供内嵌样式表。
	var mounted bool
	for _, r := range a.Router().Routes() {
		if r.Method == http.MethodGet && r.Pattern == "/static/*filepath" {
			mounted = true
		}
	}
	if !mounted {
		t.Fatal("Default must register GET /static/*filepath | Default 必须注册 GET /static/*filepath")
	}

	rec := doRequest(t, a, http.MethodGet, "/static/app.css", map[string]string{"X-Request-Id": "trace-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/app.css 状态码 = %d, 期望 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "--yd-brand") {
		t.Errorf("静态资源正文异常: %q", body)
	}
	// RequestID runs in the default chain and echoes the incoming header. | 默认链上的 RequestID 会回显请求头。
	if got := rec.Header().Get("X-Request-Id"); got != "trace-1" {
		t.Errorf("X-Request-Id 回显 = %q, 期望 %q", got, "trace-1")
	}

	// The embedded layout renders through the default renderer and injects Content raw. | 内嵌布局可经默认渲染器渲染，并把 Content 原样注入。
	var buf bytes.Buffer
	data := map[string]any{ContentKey: template.HTML("<p>ok</p>"), "Title": "yd"}
	if err := a.Renderer().Render(&buf, DefaultLayout, data); err != nil {
		t.Fatalf("渲染内嵌布局失败: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "<p>ok</p>") || !strings.Contains(out, "<title>yd</title>") {
		t.Errorf("内嵌布局输出异常: %q", out)
	}

	// The embedded language catalogs are loaded into the built-in service container. | 内嵌语言目录已装入内建服务容器。
	if _, ok := a.Get(ServiceI18n); !ok {
		t.Error("Default must register the i18n Bundle as a built-in service | Default 必须把 i18n Bundle 注册为内建服务")
	}
}

// TestNewWiresDefaultsFromEmbed covers the zero-option contract: New alone must
// hand back an app whose every subsystem is usable, without Default's
// conventions (middleware chain, /static mount, i18n service). | TestNewWiresDefaultsFromEmbed
// 覆盖零选项契约：仅靠 New 就必须返回各子系统均可用的应用，且不含 Default 的约定
// （中间件链、/static 挂载、i18n 服务）。
func TestNewWiresDefaultsFromEmbed(t *testing.T) {
	a, err := New()
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	defer func() {
		if err := a.Close(context.Background()); err != nil {
			t.Errorf("Close 失败: %v", err)
		}
	}()

	if a.Config() == nil || a.Logger() == nil || a.FS() == nil || a.Router() == nil || a.Renderer() == nil {
		t.Fatal("New must wire config/logger/fs/router/renderer | New 必须装配 config/logger/fs/router/renderer")
	}
	if a.Server() == nil || a.Server().Addr() == "" {
		t.Fatal("New must build a server carrying a listen address | New 必须构建带监听地址的服务器")
	}

	// The built-in tree is embedded, so the default template root resolves
	// without any option. | 内建资产树来自 embed，因此无需任何选项即可解析默认模板根。
	tpl, err := assetDir(a.FS(), assetsTemplatesDir)
	if err != nil {
		t.Fatalf("解析内建模板根失败: %v", err)
	}
	if !afs.Exists(tpl, DefaultLayout) {
		t.Errorf("内建模板根必须包含 %s", DefaultLayout)
	}

	// Default's conventions are not applied by New. | New 不施加 Default 的约定。
	if _, ok := a.Get(ServiceI18n); ok {
		t.Error("New must not register the i18n service | New 不应注册 i18n 服务")
	}
}

func TestNewAppliesOptions(t *testing.T) {
	fsys := afs.NewMem()
	if err := afero.WriteFile(fsys, "templates/layouts/base.html", []byte(testLayout), 0o644); err != nil {
		t.Fatalf("写入模板失败: %v", err)
	}
	cfg, err := config.Load(config.Options{FS: afs.NewMem()})
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	logger := logx.New(logx.Options{Writer: io.Discard})

	a, err := New(
		WithFS(fsys),
		WithConfig(cfg),
		WithLogger(logger),
		WithServerOptions(func(o *server.Options) {
			o.Host = "127.0.0.1"
			o.Port = 0
			o.H2C = true
		}),
	)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}

	if a.FS() != fsys {
		t.Error("WithFS was not applied | WithFS 未被应用")
	}
	if a.Config() != cfg {
		t.Error("WithConfig was not applied | WithConfig 未被应用")
	}
	if a.Logger() != logger {
		t.Error("WithLogger was not applied | WithLogger 未被应用")
	}
	opts := a.Server().Options()
	if opts.Host != "127.0.0.1" || opts.Port != 0 || !opts.H2C {
		t.Errorf("WithServerOptions 未被应用: %+v", opts)
	}
	if opts.Handler != http.Handler(a.Router()) {
		t.Error("the server Handler must be this app's router | server 的 Handler 必须是本应用的 router")
	}
	if a.Server().Options().Logger != logger {
		t.Error("the server Logger must be this app's logger | server 的 Logger 必须是本应用的 logger")
	}
}

func TestWithConfigDirReadsTheConfigFile(t *testing.T) {
	dir := t.TempDir()
	body := "app:\n  name: from-config-dir\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}

	a, err := New(WithConfigDir(dir), WithFS(afs.NewMem()), WithLogger(logx.New(logx.Options{Writer: io.Discard})))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if got := a.Config().GetString("app.name"); got != "from-config-dir" {
		t.Errorf("app.name = %q, 期望 %q", got, "from-config-dir")
	}
}

func TestRouterProxiesAndGroups(t *testing.T) {
	a := newTestApp(t, nil)

	a.Use(func(c *router.Ctx) {
		c.SetHeader("X-Global", "1")
		c.Next()
	})

	a.GET("/get", func(c *router.Ctx) { _ = c.String(http.StatusOK, "get") })
	a.POST("/post", func(c *router.Ctx) { _ = c.String(http.StatusOK, "post") })
	a.PUT("/put", func(c *router.Ctx) { _ = c.String(http.StatusOK, "put") })
	a.PATCH("/patch", func(c *router.Ctx) { _ = c.String(http.StatusOK, "patch") })
	a.DELETE("/delete", func(c *router.Ctx) { _ = c.String(http.StatusOK, "delete") })
	a.HEAD("/head", func(c *router.Ctx) { c.SetStatus(http.StatusOK) })
	a.OPTIONS("/options", func(c *router.Ctx) { _ = c.NoContent() })

	api := a.Group("/api", func(c *router.Ctx) {
		c.SetHeader("X-API", "1")
		c.Next()
	})
	if api == nil {
		t.Fatal("Group must return a non-nil group | Group 必须返回非 nil 的分组")
	}
	api.GET("/ping", func(c *router.Ctx) { _ = c.String(http.StatusOK, "pong") })

	want := map[string]bool{
		http.MethodGet + " /get":         false,
		http.MethodPost + " /post":       false,
		http.MethodPut + " /put":         false,
		http.MethodPatch + " /patch":     false,
		http.MethodDelete + " /delete":   false,
		http.MethodHead + " /head":       false,
		http.MethodOptions + " /options": false,
		http.MethodGet + " /api/ping":    false,
	}
	for _, r := range a.Router().Routes() {
		key := r.Method + " " + r.Pattern
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, seen := range want {
		if !seen {
			t.Errorf("路由 %s 未注册", key)
		}
	}

	rec := doRequest(t, a, http.MethodGet, "/api/ping", nil)
	if rec.Body.String() != "pong" {
		t.Errorf("GET /api/ping 正文 = %q", rec.Body.String())
	}
	if rec.Header().Get("X-Global") != "1" || rec.Header().Get("X-API") != "1" {
		t.Errorf("中间件头缺失: global=%q api=%q", rec.Header().Get("X-Global"), rec.Header().Get("X-API"))
	}
}

// ---------------------------------------------------------------- rendering | 渲染

func TestHtmxRequestRendersPartial(t *testing.T) {
	a := newTestApp(t, map[string]string{
		"templates/layouts/base.html": testLayout,
		"templates/pages/home.html":   testPage,
	})
	a.GET("/", renderHandler(t, a, http.StatusOK, "pages/home.html", testData()))

	t.Run("htmx request renders the fragment only", func(t *testing.T) {
		rec := doRequest(t, a, http.MethodGet, "/", map[string]string{"HX-Request": "true"})
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d, 期望 200", rec.Code)
		}
		if got := rec.Body.String(); got != wantFragment {
			t.Errorf("片段正文 = %q, 期望 %q", got, wantFragment)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, 期望 text/html", ct)
		}
	})

	t.Run("plain request renders the layout-wrapped page", func(t *testing.T) {
		rec := doRequest(t, a, http.MethodGet, "/", nil)
		if got := rec.Body.String(); got != wantFull {
			t.Errorf("整页正文 = %q, 期望 %q", got, wantFull)
		}
	})

	t.Run("htmx full-page request renders the layout-wrapped page", func(t *testing.T) {
		rec := doRequest(t, a, http.MethodGet, "/", map[string]string{
			"HX-Request":      "true",
			"HX-Request-Type": "full",
		})
		if got := rec.Body.String(); got != wantFull {
			t.Errorf("整页正文 = %q, 期望 %q", got, wantFull)
		}
	})

	t.Run("missing layout falls back to the bare template", func(t *testing.T) {
		b := newTestApp(t, map[string]string{"templates/pages/home.html": testPage})
		b.GET("/", renderHandler(t, b, http.StatusOK, "pages/home.html", testData()))
		rec := doRequest(t, b, http.MethodGet, "/", nil)
		if got := rec.Body.String(); got != wantFragment {
			t.Errorf("回退正文 = %q, 期望 %q", got, wantFragment)
		}
	})
}

func TestRenderAppliesStatusAndReportsMissingTemplates(t *testing.T) {
	a := newTestApp(t, map[string]string{"templates/layouts/base.html": testLayout})
	a.GET("/teapot", renderHandler(t, a, http.StatusTeapot, "layouts/base.html", testData()))

	rec := doRequest(t, a, http.MethodGet, "/teapot", map[string]string{"HX-Request": "true"})
	if rec.Code != http.StatusTeapot {
		t.Errorf("状态码 = %d, 期望 %d", rec.Code, http.StatusTeapot)
	}

	var renderErr error
	b := newTestApp(t, map[string]string{"templates/layouts/base.html": testLayout})
	b.GET("/missing", func(c *router.Ctx) { renderErr = b.Render(c, http.StatusOK, "pages/nope.html", nil) })
	doRequest(t, b, http.MethodGet, "/missing", nil)
	// The built-in render engine reports the render package's sentinel. | 内置渲染引擎报告 render 包的哨兵。
	if !errors.Is(renderErr, render.ErrTemplateNotFound) {
		t.Errorf("Render 错误 = %v, 期望 render.ErrTemplateNotFound", renderErr)
	}
}

// TestRenderFailureWritesNothing covers the buffering contract: a template that
// fails halfway through must never leave a half-written body in the response.
// | TestRenderFailureWritesNothing 覆盖缓冲契约：渲染中途失败的模板绝不能在响应里
// 留下半截正文。
func TestRenderFailureWritesNothing(t *testing.T) {
	// The page emits text and then fails on the nil field chain. | 页面先输出文本，随后在 nil 字段链上失败。
	a := newTestApp(t, map[string]string{
		"templates/layouts/base.html": testLayout,
		"templates/pages/boom.html":   `<p>partial</p>{{ .A.B }}`,
	})

	var renderErr error
	a.GET("/boom", func(c *router.Ctx) {
		renderErr = a.Render(c, http.StatusOK, "pages/boom.html", map[string]any{"A": nil})
	})

	for _, headers := range []map[string]string{nil, {"HX-Request": "true"}} {
		rec := doRequest(t, a, http.MethodGet, "/boom", headers)
		if renderErr == nil {
			t.Fatal("the render must fail | 渲染必须失败")
		}
		if rec.Body.Len() != 0 {
			t.Errorf("渲染失败后(headers=%v)正文 = %q, 期望为空", headers, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------- services | 服务容器

// fakeCloser records whether Close ran. | fakeCloser 记录 Close 是否被执行。
type fakeCloser struct{ closed bool }

func (f *fakeCloser) Close() error {
	f.closed = true
	return nil
}

func TestServiceContainer(t *testing.T) {
	a := newTestApp(t, nil)

	if _, ok := a.Get(ServiceDB); ok {
		t.Fatal("an unregistered service must not exist | 未注册的服务不应存在")
	}

	a.Set(ServiceDB, "sqlite")
	if v, ok := a.Get(ServiceDB); !ok || v != "sqlite" {
		t.Fatalf("Get(ServiceDB) = (%v, %v)", v, ok)
	}
	if v, err := a.MustGet(ServiceDB); err != nil || v != "sqlite" {
		t.Fatalf("MustGet(ServiceDB) = (%v, %v)", v, err)
	}

	a.Set(ServiceRedis, 42)
	if v, err := a.MustGet(ServiceRedis); err != nil || v != 42 {
		t.Fatalf("MustGet(ServiceRedis) = (%v, %v)", v, err)
	}

	if v, err := a.MustGet("nope"); v != nil || !errors.Is(err, ErrServiceMissing) {
		t.Fatalf("MustGet(nope) = (%v, %v)，期望 (nil, ErrServiceMissing)", v, err)
	}

	for got, want := range map[string]string{
		ServiceDB:    "db",
		ServiceRedis: "redis",
		ServiceAsynq: "asynq",
		ServiceI18n:  "i18n",
	} {
		if got != want {
			t.Errorf("内建服务键 = %q, 期望 %q", got, want)
		}
	}
}

// ---------------------------------------------------------------- lifecycle | 生命周期

func TestOnShutdownCalledInReverseOrder(t *testing.T) {
	a := newTestApp(t, nil)

	var order []string
	record := func(name string) func(context.Context) error {
		return func(context.Context) error {
			order = append(order, name)
			return nil
		}
	}

	a.OnShutdown(record("first"))
	a.OnShutdown(record("second"))
	closer := &fakeCloser{}
	a.Set(ServiceRedis, closer)
	a.OnShutdown(record("third"))

	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	want := []string{"third", "second", "first"}
	if !slices.Equal(order, want) {
		t.Errorf("钩子执行顺序 = %v, 期望 %v", order, want)
	}
	if !closer.closed {
		t.Error("an io.Closer registered through Set must be closed by Close | Set 注册的 io.Closer 必须在 Close 时关闭")
	}

	// Close is idempotent: a second call must not rerun anything. | Close 幂等：再次调用不得重复执行。
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("第二次 Close 失败: %v", err)
	}
	if !slices.Equal(order, want) {
		t.Errorf("第二次 Close 后顺序 = %v, 期望 %v", order, want)
	}
}

func TestCloseCollectsHookErrors(t *testing.T) {
	a := newTestApp(t, nil)

	boom := errors.New("boom | 爆炸")
	ran := false
	a.OnShutdown(func(context.Context) error { return boom })
	a.OnShutdown(func(context.Context) error { ran = true; return nil })

	err := a.Close(context.Background())
	if !errors.Is(err, boom) {
		t.Errorf("Close 错误 = %v, 期望包含底层错误", err)
	}
	if !errors.Is(err, ErrShutdown) {
		t.Errorf("Close 错误 = %v, 期望包含 ErrShutdown", err)
	}
	if !ran {
		t.Error("one failing hook must not stop the remaining hooks from running | 单个钩子失败不得阻止其余钩子执行")
	}
}

// waitReady polls the bound address until it answers /health. | waitReady 轮询已绑定地址直到 /health 可用。
func waitReady(t *testing.T, a *App) {
	t.Helper()

	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		addr := a.Server().Addr()
		if !strings.HasSuffix(addr, ":0") {
			resp, err := client.Get("http://" + addr + "/health")
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the server was not ready within the timeout | 服务器未在超时内就绪")
}

func TestRunStopsOnContextCancel(t *testing.T) {
	a := newTestApp(t, nil, WithServerOptions(func(o *server.Options) {
		o.Host = "127.0.0.1"
		o.Port = 0
	}))
	a.GET("/health", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })

	closed := make(chan struct{})
	a.OnShutdown(func(context.Context) error {
		close(closed)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- a.Run(ctx) }()

	waitReady(t, a)

	// The listener is bound and serving. | 监听已绑定并在服务。
	if addr := a.Server().Addr(); !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("Addr() = %q, 期望绑定 127.0.0.1", addr)
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run 返回错误: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after ctx was cancelled | ctx 取消后 Run 未返回")
	}

	select {
	case <-closed:
	default:
		t.Error("the shutdown hook must run before Run returns | Run 返回前必须执行关闭钩子")
	}
	// The port is released after Run returns. | Run 返回后端口已释放。
	dialer := net.Dialer{Timeout: 200 * time.Millisecond}
	conn, err := dialer.DialContext(context.Background(), "tcp", a.Server().Addr())
	if err == nil {
		_ = conn.Close()
		t.Error("the listener should be closed once Run returns | Run 返回后监听应当已关闭")
	}
}
