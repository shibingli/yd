// Package app is the yd application facade: it wires the router, the template
// renderer, the layered config, the logger and the HTTP server into one object
// with a single lifecycle. | Package app 是 yd 的应用门面：把路由、模板渲染、
// 分层配置、日志器与 HTTP 服务器装配为一个整体，并统一暴露生命周期。
//
// Responsibilities | 职责：
//
//   - wiring: New builds every subsystem from Options; Default adds the
//     framework conventions (embedded templates/static/langs assets, the
//     default middleware chain and the /static mount); | 装配：New 依据 Options
//     构建全部子系统；Default 追加框架约定（内嵌 templates/static/langs 资产、
//     默认中间件链与 /static 挂载）；
//   - routing: App proxies the router verb helpers, groups and middleware
//     registration, while Router exposes the raw router for advanced wiring;
//     | 路由：App 代理路由器的动词方法、分组与中间件注册，Router 暴露原始路由器供高级装配；
//   - rendering: Render merges the Htmx fragment/whole-page semantics (see
//     render.go); | 渲染：Render 合并 Htmx 片段/整页语义（见 render.go）；
//   - services: a string-keyed container for the DB/Redis/Asynq/I18n handles,
//     where io.Closer values are released automatically on shutdown (see
//     services.go); | 服务容器：以字符串为键存放 DB/Redis/Asynq/I18n 句柄，
//     实现 io.Closer 的值在关闭时自动释放（见 services.go）；
//   - lifecycle: Run blocks on the server and then runs every shutdown hook in
//     reverse registration order. | 生命周期：Run 阻塞在服务器上，随后按注册逆序执行关闭钩子。
//
// Non-goals | 不做的事：
//
//   - it never opens databases, Redis clients or task queues (see data/*); | 不负责
//     打开数据库、Redis 客户端或任务队列（见 data/*）；
//   - it does not parse command lines (see cli) nor generate code (see gen);
//     | 不负责命令行解析（见 cli），也不做代码生成（见 gen）；
//   - it does not interpret the business meaning of the configuration or of the
//     registered services. | 不感知配置与已注册服务的业务语义。
package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/spf13/afero"

	"github.com/yun-dev/yd/config"
	"github.com/yun-dev/yd/errx"
	"github.com/yun-dev/yd/i18n"
	"github.com/yun-dev/yd/logx"
	"github.com/yun-dev/yd/middleware"
	"github.com/yun-dev/yd/router"
	"github.com/yun-dev/yd/server"
)

// Sentinel errors, declared once at package level. | 哨兵错误，在包级声明一次。
var (
	// ErrNilFS indicates a nil asset filesystem. | ErrNilFS 表示资产文件系统为 nil。
	ErrNilFS = errx.New("app: nil asset filesystem | app：资产文件系统为 nil")
	// ErrNilContext indicates Render received a nil request context. | ErrNilContext 表示 Render 收到 nil 请求上下文。
	ErrNilContext = errx.New("app: nil router context | app：路由上下文为 nil")
	// ErrTemplateName indicates an empty template name. | ErrTemplateName 表示模板名为空。
	ErrTemplateName = errx.New("app: empty template name | app：模板名为空")
	// ErrNoRenderer indicates that no template renderer has been injected. | ErrNoRenderer 表示未注入模板渲染器。
	ErrNoRenderer = errx.New("app: no template renderer | app：未注入模板渲染器")
	// ErrTemplateNotFound indicates that the named template does not exist. | ErrTemplateNotFound 表示指定名称的模板不存在。
	ErrTemplateNotFound = errx.New("app: template not found | app：模板不存在")
	// ErrTemplateParse indicates that a template could not be loaded or parsed. | ErrTemplateParse 表示模板加载或解析失败。
	ErrTemplateParse = errx.New("app: template parse failed | app：模板解析失败")
	// ErrTemplateExec indicates that a template failed while executing. | ErrTemplateExec 表示模板执行失败。
	ErrTemplateExec = errx.New("app: template execution failed | app：模板执行失败")
	// ErrAssetsDir indicates that an asset subtree could not be resolved. | ErrAssetsDir 表示资产子树无法解析。
	ErrAssetsDir = errx.New("app: cannot resolve asset directory | app：无法解析资产目录")
	// ErrConfigLoad indicates that the layered configuration could not be loaded. | ErrConfigLoad 表示分层配置加载失败。
	ErrConfigLoad = errx.New("app: config load failed | app：配置加载失败")
	// ErrI18n indicates that the message catalogs could not be loaded. | ErrI18n 表示语言目录加载失败。
	ErrI18n = errx.New("app: i18n catalog load failed | app：语言目录加载失败")
	// ErrServiceMissing indicates that a service key is not registered. | ErrServiceMissing 表示服务键未注册。
	ErrServiceMissing = errx.New("app: service not registered | app：服务未注册")
	// ErrShutdown indicates that a shutdown hook failed. | ErrShutdown 表示关闭钩子执行失败。
	ErrShutdown = errx.New("app: shutdown hook failed | app：关闭钩子执行失败")
)

// Conventions and defaults of the facade. | 门面的约定与默认值。
const (
	// DefaultLayout is the layout template wrapping whole pages. | DefaultLayout 是包裹整页的布局模板名。
	DefaultLayout = "layouts/base.html"
	// ContentKey is the data key carrying the rendered fragment inside the layout. | ContentKey 是布局中承载已渲染片段的数据键。
	ContentKey = "Content"
	// contentKey is the lower-case alias layouts may use as well. | contentKey 是布局同样可用的全小写别名。
	contentKey = "content"
	// DataKey carries the original page data when it is not a map. | DataKey 在页面数据不是映射时承载原始数据。
	DataKey = "Data"
	// staticURLPrefix is the URL prefix of the embedded static assets. | staticURLPrefix 是内嵌静态资源的 URL 前缀。
	staticURLPrefix = "/static"
	// defaultShutdownTimeout bounds the shutdown hooks when the server sets none. | defaultShutdownTimeout 在服务器未配置时限定关闭钩子的最长等待。
	defaultShutdownTimeout = 10 * time.Second
	// configName is the diagnostics name of the configuration loaded by New. | configName 是 New 加载配置时使用的诊断名称。
	configName = "app"
	// defaultConfigDir is the configuration directory used when none is given. | defaultConfigDir 是未显式指定时使用的配置目录。
	defaultConfigDir = "."
)

// App is the application facade. | App 是应用门面。
//
// It is built once at startup by New. Afterwards the routing helpers, the
// service container and the shutdown hooks are safe for concurrent use; only
// Run and Close must not be called concurrently with each other. | 它在启动期由
// New 构建一次；此后路由辅助方法、服务容器与关闭钩子均可安全并发使用，
// 只有 Run 与 Close 之间不可并发调用。
type App struct {
	cfg      *config.Config
	log      *logx.Logger
	fs       afero.Fs
	router   *router.Router
	server   *server.Server
	renderer router.Renderer

	configDir      string
	layout         string
	serverMutators []func(*server.Options)

	// mu guards services and hooks. | mu 保护 services 与 hooks。
	mu       sync.Mutex
	services map[string]any
	hooks    []shutdownHook
}

// Option customizes an App while it is being wired. | Option 在装配过程中定制 App。
type Option func(*App)

// WithConfig injects an already loaded configuration handle; New loads one
// itself when this option is absent. | WithConfig 注入已加载的配置句柄；
// 未提供时 New 会自行加载。
func WithConfig(c *config.Config) Option { return func(a *App) { a.cfg = c } }

// WithFS sets the asset filesystem holding templates/, static/ and langs/;
// without it the framework's embedded assets are used. | WithFS 设置承载
// templates/、static/ 与 langs/ 的资产文件系统；未提供时使用框架内嵌资产。
func WithFS(fsys afero.Fs) Option { return func(a *App) { a.fs = fsys } }

// WithLogger injects the logger shared by the app and the HTTP server;
// without it the logx global default is used. | WithLogger 注入应用与 HTTP
// 服务器共用的日志器；未提供时使用 logx 的全局默认值。
func WithLogger(l *logx.Logger) Option { return func(a *App) { a.log = l } }

// WithConfigDir sets the directory New reads the configuration from
// (default "."). | WithConfigDir 设置 New 读取配置的目录（默认 "."）。
//
// Configuration always comes from the operating system filesystem: unlike
// assets it is a runtime concern, and embedded assets are read-only. | 配置一律来自
// 操作系统文件系统：它是运行时关注点，而内嵌资产是只读的。
func WithConfigDir(dir string) Option { return func(a *App) { a.configDir = dir } }

// WithServerOptions registers a mutator applied to the server options derived
// from the configuration; mutators run in registration order right before the
// server is built. | WithServerOptions 注册一个作用于配置所推导服务器参数的变更函数；
// 多个变更在构建服务器之前按注册顺序执行。
func WithServerOptions(fn func(*server.Options)) Option {
	return func(a *App) {
		if fn != nil {
			a.serverMutators = append(a.serverMutators, fn)
		}
	}
}

// WithRenderer injects a template renderer (for example the dedicated render
// engine), replacing the built-in html/template renderer. | WithRenderer 注入模板
// 渲染器（例如专用渲染引擎），替换内置的 html/template 渲染器。
func WithRenderer(rd router.Renderer) Option { return func(a *App) { a.renderer = rd } }

// New wires a fully usable application. | New 装配一个可直接使用的应用。
//
// Wiring order: options → assets → config → renderer → server. Only failures
// that make the app unusable are reported; a missing asset subtree or language
// catalog degrades gracefully instead of failing. | 装配顺序：选项 → 资产 →
// 配置 → 渲染器 → 服务器。只有会让应用无法工作的失败才返回错误；
// 缺失的资产子树或语言目录会优雅降级而非报错。
func New(opts ...Option) (*App, error) {
	builtinFS, err := defaultAssetsFS()
	if err != nil {
		return nil, err
	}

	a := &App{
		fs:        builtinFS,
		log:       logx.Default(),
		layout:    DefaultLayout,
		configDir: defaultConfigDir,
		services:  make(map[string]any),
	}
	for _, fn := range opts {
		if fn != nil {
			fn(a)
		}
	}

	if a.fs == nil {
		return nil, ErrNilFS
	}
	if a.log == nil {
		a.log = logx.Default()
	}

	if a.cfg == nil {
		cfg, err := config.Load(config.Options{Name: configName, ConfigDir: a.configDir})
		if err != nil {
			return nil, errx.WrapDetail(err, ErrConfigLoad, "dir=%q", a.configDir)
		}
		a.cfg = cfg
	}

	a.router = router.New()

	if a.renderer == nil {
		tplFS, err := assetDir(a.fs, assetsTemplatesDir)
		if err != nil {
			return nil, err
		}
		rd, err := newTemplateRenderer(tplFS, rendererOptions{Dev: isDevelopment(a.cfg)})
		if err != nil {
			return nil, err
		}
		a.renderer = rd
	}
	a.router.SetRenderer(a.renderer)

	so := server.OptionsFromConfig(a.cfg)
	for _, fn := range a.serverMutators {
		fn(&so)
	}
	so.Handler = a.router
	so.Logger = a.log
	a.server = server.New(so)

	return a, nil
}

// Default assembles the conventional application: the embedded assets, the
// default middleware chain (Recover → RequestID → AccessLog → Compress → CORS →
// I18n) and the /static mount. | Default 按约定装配应用：内嵌资产、默认中间件链
// （Recover → RequestID → AccessLog → Compress → CORS → I18n）以及 /static 挂载。
//
// The CORS defaults deny every cross-origin caller; downstream projects opt in
// through their own middleware registration. | CORS 采用默认拒绝全部跨源调用的配置，
// 下游项目通过自行注册中间件放开。
// When no language catalog can be loaded the I18n middleware is skipped with a
// warning, so a project without catalogs still boots. | 无法加载语言目录时会跳过
// I18n 中间件并记录告警，因此没有语言目录的项目仍可启动。
func Default() (*App, error) {
	a, err := New()
	if err != nil {
		return nil, err
	}

	a.Use(
		middleware.Recover(a.log),
		middleware.RequestID(""),
		middleware.AccessLog(a.log),
		middleware.Compress(middleware.CompressOptions{}),
		middleware.CORS(middleware.CORSOptions{}),
	)

	bundle, err := a.i18nBundle()
	if err != nil {
		a.log.Warn("i18n catalogs unavailable, middleware skipped | 语言目录不可用，已跳过 i18n 中间件",
			logx.Err(err))
	} else {
		a.Set(ServiceI18n, bundle)
		a.Use(i18n.Middleware(bundle, i18n.Options{}))
	}

	static, err := assetDir(a.fs, assetsStaticDir)
	if err != nil {
		return nil, err
	}
	a.router.StaticFS(staticURLPrefix, static)

	a.log.Info("app assembled | 应用已装配", logx.Str("env", a.env()))
	return a, nil
}

// ---------------------------------------------------------------- accessors | 访问器

// Config returns the layered configuration handle. | Config 返回分层配置句柄。
func (a *App) Config() *config.Config { return a.cfg }

// Logger returns the application logger. | Logger 返回应用日志器。
func (a *App) Logger() *logx.Logger { return a.log }

// FS returns the asset filesystem (templates/, static/, langs/). | FS 返回资产文件系统。
func (a *App) FS() afero.Fs { return a.fs }

// Router returns the underlying router. | Router 返回底层路由器。
func (a *App) Router() *router.Router { return a.router }

// Server returns the HTTP server; it is built but not started until Run. | Server 返回 HTTP 服务器；它已构建，但直到 Run 才启动。
func (a *App) Server() *server.Server { return a.server }

// Renderer returns the template renderer in use. | Renderer 返回当前使用的模板渲染器。
func (a *App) Renderer() router.Renderer { return a.renderer }

// env returns the runtime environment name taken from the configuration. | env 返回配置中的运行环境名。
func (a *App) env() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.GetString("app.env")
}

// ---------------------------------------------------------------- routing | 路由

// Use registers global middleware; it must be called before the server starts. | Use 注册全局中间件；必须在服务器启动前调用。
func (a *App) Use(mw ...router.Middleware) { a.router.Use(mw...) }

// Group creates a route group sharing prefix and middleware. | Group 创建共享前缀与中间件的路由组。
func (a *App) Group(prefix string, mw ...router.Middleware) *router.Group {
	return a.router.Group(prefix, mw...)
}

// GET registers a GET route. | GET 注册 GET 路由。
func (a *App) GET(pattern string, h router.HandlerFunc) { a.router.GET(pattern, h) }

// POST registers a POST route. | POST 注册 POST 路由。
func (a *App) POST(pattern string, h router.HandlerFunc) { a.router.POST(pattern, h) }

// PUT registers a PUT route. | PUT 注册 PUT 路由。
func (a *App) PUT(pattern string, h router.HandlerFunc) { a.router.PUT(pattern, h) }

// PATCH registers a PATCH route. | PATCH 注册 PATCH 路由。
func (a *App) PATCH(pattern string, h router.HandlerFunc) { a.router.PATCH(pattern, h) }

// DELETE registers a DELETE route. | DELETE 注册 DELETE 路由。
func (a *App) DELETE(pattern string, h router.HandlerFunc) { a.router.DELETE(pattern, h) }

// HEAD registers a HEAD route. | HEAD 注册 HEAD 路由。
func (a *App) HEAD(pattern string, h router.HandlerFunc) { a.router.HEAD(pattern, h) }

// OPTIONS registers an OPTIONS route. | OPTIONS 注册 OPTIONS 路由。
func (a *App) OPTIONS(pattern string, h router.HandlerFunc) { a.router.OPTIONS(pattern, h) }

// ---------------------------------------------------------------- lifecycle | 生命周期

// Run starts the HTTP server and blocks until ctx is cancelled, a signal
// arrives or a fatal serve error occurs; it then stops the server gracefully
// and runs every shutdown hook in reverse registration order. | Run 启动 HTTP
// 服务器并阻塞，直到 ctx 被取消、收到信号或出现致命服务错误；
// 随后优雅停止服务器，并按注册逆序执行全部关闭钩子。
//
// The serve error and the joined hook errors are returned together. | 服务错误与聚合后的钩子错误一并返回。
func (a *App) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	a.log.Info("application starting | 应用启动中",
		logx.Str("addr", a.server.Addr()),
		logx.Str("env", a.env()),
	)

	runErr := a.server.Run(ctx)
	if runErr != nil {
		a.log.Error("http server stopped with an error | HTTP 服务器异常退出", logx.Err(runErr))
	}

	// Shutdown runs on a fresh context: the caller's ctx is already done. | 关闭使用全新的上下文：调用方的 ctx 此时已结束。
	closeCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout())
	defer cancel()
	closeErr := a.Close(closeCtx)

	return errors.Join(runErr, closeErr)
}

// shutdownTimeout bounds the shutdown hooks, defaulting to the server timeout. | shutdownTimeout 限定关闭钩子的最长等待，默认取服务器的退出超时。
func (a *App) shutdownTimeout() time.Duration {
	if d := a.server.Options().ShutdownTimeout; d > 0 {
		return d
	}
	return defaultShutdownTimeout
}
