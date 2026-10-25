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
//   - lifecycle: Run blocks on the server and then runs the full shutdown
//     sequence (see Shutdown): long-lived connections, HTTP drain, gRPC, and
//     finally every shutdown hook in reverse registration order. | 生命周期：
//     Run 阻塞在服务器上，随后执行完整的退出流程（见 Shutdown）：长连接、
//     HTTP 排空、gRPC，最后按注册逆序执行全部关闭钩子。
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

	"github.com/shibingli/yd/core/envx"
	"github.com/shibingli/yd/runtime/metricsx"
	"github.com/shibingli/yd/runtime/server"
	"github.com/shibingli/ydx/config"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/gc"
	"github.com/shibingli/ydx/i18n"
	ydlog "github.com/shibingli/ydx/log"
	"github.com/shibingli/ydx/middleware"
	"github.com/shibingli/ydx/nodeid"
	"github.com/shibingli/ydx/router"
	"github.com/shibingli/ydx/webtransport"
)

// Sentinel errors, declared once at package level. | 哨兵错误，在包级声明一次。
var (
	// ErrNilFS indicates a nil asset filesystem. | ErrNilFS 表示资产文件系统为 nil。
	ErrNilFS = errs.New("app: nil asset filesystem | app：资产文件系统为 nil")
	// ErrNilContext indicates Render received a nil request context. | ErrNilContext 表示 Render 收到 nil 请求上下文。
	ErrNilContext = errs.New("app: nil router context | app：路由上下文为 nil")
	// ErrTemplateName indicates an empty template name. | ErrTemplateName 表示模板名为空。
	ErrTemplateName = errs.New("app: empty template name | app：模板名为空")
	// ErrNoRenderer indicates that no template renderer has been injected. | ErrNoRenderer 表示未注入模板渲染器。
	ErrNoRenderer = errs.New("app: no template renderer | app：未注入模板渲染器")
	// ErrTemplateNotFound indicates that the named template does not exist. | ErrTemplateNotFound 表示指定名称的模板不存在。
	ErrTemplateNotFound = errs.New("app: template not found | app：模板不存在")
	// ErrTemplateParse indicates that a template could not be loaded or parsed. | ErrTemplateParse 表示模板加载或解析失败。
	ErrTemplateParse = errs.New("app: template parse failed | app：模板解析失败")
	// ErrTemplateExec indicates that a template failed while executing. | ErrTemplateExec 表示模板执行失败。
	ErrTemplateExec = errs.New("app: template execution failed | app：模板执行失败")
	// ErrAssetsDir indicates that an asset subtree could not be resolved. | ErrAssetsDir 表示资产子树无法解析。
	ErrAssetsDir = errs.New("app: cannot resolve asset directory | app：无法解析资产目录")
	// ErrConfigLoad indicates that the layered configuration could not be loaded. | ErrConfigLoad 表示分层配置加载失败。
	ErrConfigLoad = errs.New("app: config load failed | app：配置加载失败")
	// ErrNodeIdentity indicates that the configured node identity is invalid or
	// cannot be made durable when node.require_persistent is set. | ErrNodeIdentity 表示
	// 配置的节点身份非法，或 node.require_persistent 开启时无法持久化。
	ErrNodeIdentity = errs.New("app: node identity failed | app：节点身份解析失败")
	// ErrI18n indicates that the message catalogs could not be loaded. | ErrI18n 表示语言目录加载失败。
	ErrI18n = errs.New("app: i18n catalog load failed | app：语言目录加载失败")
	// ErrServiceMissing indicates that a service key is not registered. | ErrServiceMissing 表示服务键未注册。
	ErrServiceMissing = errs.New("app: service not registered | app：服务未注册")
	// ErrRuntimeGC indicates that the runtime GC scheduler rejected its configuration. | ErrRuntimeGC
	// 表示运行时 GC 调度器拒绝了配置。
	ErrRuntimeGC = errs.New("app: runtime gc scheduler failed | app：运行时 GC 调度器装配失败")
	// ErrShutdown indicates that a shutdown hook failed. | ErrShutdown 表示关闭钩子执行失败。
	ErrShutdown = errs.New("app: shutdown hook failed | app：关闭钩子执行失败")
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
	fs       afero.Fs
	staticFS afero.Fs
	renderer router.Renderer
	grpc     GRPCServer

	grpcErr error

	closeErr error

	cfg          *config.Config
	nodeIdentity nodeid.Identity
	log          *ydlog.Logger
	router       *router.Router
	server       *server.Server
	services     map[string]any
	// metrics observes middleware runtime behaviour and publishes it to dashboards. | metrics 观察中间件
	// 运行时行为，并发布给仪表盘。
	metrics *metricsx.Registry
	// runtimeGC schedules garbage collection in quiet periods; it is always non-nil after New. | runtimeGC
	// 在空闲时段调度垃圾回收；New 之后它始终非 nil。
	runtimeGC *gc.Scheduler

	closeDone chan struct{}

	configDir      string
	layout         string
	serverMutators []func(*server.Options)

	hooks []shutdownHook

	// grpcOnce guards the gRPC shutdown stage so it runs at most once; grpcErr keeps its result so
	// every caller sees the same outcome. | grpcOnce 保证 gRPC 退出阶段最多执行一次；grpcErr 保存
	// 结果，使每个调用方看到同一个结局。
	grpcOnce sync.Once

	// closeOnce/closeDone make concurrent Close calls wait for the same hook run and share its
	// error instead of the second caller returning before resources are released. | closeOnce/closeDone
	// 让并发的 Close 调用等待同一次钩子执行并共享错误，而不是让第二个调用方在资源释放前返回。
	closeOnce sync.Once

	// mu guards services and hooks. | mu 保护 services 与 hooks。
	mu sync.Mutex
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

// WithStaticFS sets the filesystem rooted at the /static URL prefix. It is what
// the renderer's asset helper reads, so it must be the same merged tree mounted
// through Router().StaticFS. | WithStaticFS 设置根为 /static URL 前缀的文件系统。
// 渲染器的 asset 助手读取它，因此它必须与 Router().StaticFS 挂载的是同一棵合并树。
func WithStaticFS(fsys afero.Fs) Option { return func(a *App) { a.staticFS = fsys } }

// WithLogger injects the logger shared by the app and the HTTP server;
// without it the ydx/log global default is used. | WithLogger 注入应用与 HTTP
// 服务器共用的日志器；未提供时使用 ydx/log 的全局默认值。
func WithLogger(l *ydlog.Logger) Option { return func(a *App) { a.log = l } }

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

// WithRenderer injects a template renderer, replacing the built-in render
// engine (see newRenderer). | WithRenderer 注入模板渲染器，替换内置渲染引擎
// （见 newRenderer）。
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
		log:       ydlog.Default(),
		layout:    DefaultLayout,
		configDir: defaultConfigDir,
		services:  make(map[string]any),
		metrics:   metricsx.NewRegistry(),
		closeDone: make(chan struct{}),
	}
	for _, fn := range opts {
		if fn != nil {
			fn(a)
		}
	}

	if a.fs == nil {
		return nil, ErrNilFS
	}
	if a.staticFS == nil {
		static, err := assetDir(a.fs, assetsStaticDir)
		if err != nil {
			return nil, err
		}
		a.staticFS = static
	}
	if a.log == nil {
		a.log = ydlog.Default()
	}

	if a.cfg == nil {
		cfg, err := config.Load(config.Options{Name: configName, ConfigDir: a.configDir})
		if err != nil {
			return nil, errs.WrapDetail(err, ErrConfigLoad, "dir=%[1]q | 目录=%[1]q", a.configDir)
		}
		a.cfg = cfg
	}

	// Resolve the GC policy before serving: an invalid strategy or threshold is a
	// configuration error and must fail startup instead of silently disabling collection.
	// | 在提供服务前解析 GC 策略：非法的策略或阈值属于配置错误，必须让启动失败，而不是静默关闭回收。
	gcOptions, err := gc.OptionsFromConfig(a.cfg)
	if err != nil {
		return nil, errs.Wrap(err, ErrRuntimeGC)
	}
	gcOptions.Logger = a.log
	// Optional request-activity gate: when gc.max_inflight is positive, any request passing
	// through the observed middleware chain defers collection until the chain is quiet again.
	// | 可选的请求活动门：gc.max_inflight 为正时，只要有请求经过已观察中间件链，回收就推迟到
	// 链再次安静为止。
	if maxInflight := a.cfg.GetInt("gc.max_inflight"); maxInflight < 0 {
		return nil, errs.Detail(ErrRuntimeGC, "gc.max_inflight=%d must not be negative | gc.max_inflight=%d 不得为负数", maxInflight, maxInflight)
	} else if maxInflight > 0 {
		limit := uint64(maxInflight)
		gcOptions.Busy = func() bool { return a.metrics.InFlight() >= limit }
	}
	runtimeGC, err := gc.New(gcOptions)
	if err != nil {
		return nil, errs.Wrap(err, ErrRuntimeGC)
	}
	a.runtimeGC = runtimeGC

	identity, identityErr := nodeid.Resolve(context.Background(), a.nodeOptions())
	if identityErr != nil {
		// An explicit node.id is a configuration error, and so is any failure when
		// durability is required; neither may silently fall back to a random id. |
		// 显式 node.id 出错属于配置错误；要求持久化时任何失败也同样；两者都不得静默退回随机 id。
		if nodeid.OptionsFromConfig(a.cfg).ConfigID != "" || a.cfg.GetBool("node.require_persistent") {
			return nil, errs.Wrap(identityErr, ErrNodeIdentity)
		}
		identity = nodeid.Ephemeral()
		a.log.Warn("node identity is ephemeral | 节点身份是临时身份", ydlog.Err(identityErr))
	}
	a.nodeIdentity = identity
	if identity.Warning != "" {
		a.log.Warn(identity.Warning, ydlog.Str("node", identity.ID))
	}
	a.log.Info("node identity resolved | 节点身份已解析",
		ydlog.Str("node", identity.ID),
		ydlog.Str("source", string(identity.Source)),
		ydlog.Str("environment", string(identity.Environment)),
		ydlog.Bool("persistent", identity.Persistent),
		ydlog.Bool("stable", identity.Stable),
	)

	a.router = router.New()

	if a.renderer == nil {
		// Templates are cached in every environment; `yd dev` restarts the child
		// when a template file changes, so hot reload does not require compiling
		// the whole tree on every request. | 所有环境都缓存模板；模板文件变化时由
		// `yd dev` 重启子进程，热重载因此不需要在每个请求上编译整棵模板树。
		engine, err := newCachedRenderer(a.fs, a.staticFS, !isDevelopment(a.cfg))
		if err != nil {
			return nil, err
		}
		a.renderer = engine
	}
	a.router.SetRenderer(a.renderer)

	so := server.OptionsFromConfig(a.cfg)
	for _, fn := range a.serverMutators {
		fn(&so)
	}
	so.Handler = a.router
	so.Logger = a.log
	a.server = server.New(so)

	// The registry is part of the application lifecycle: stopping records the final state while
	// keeping every counter readable after shutdown. | 注册表属于应用生命周期：停止时记录最终状态，
	// 同时让全部计数在关闭后仍可读取。
	a.OnShutdown(func(ctx context.Context) error { return a.metrics.Shutdown(ctx) })
	// The runtime GC loop is part of the same lifecycle; it stops before the metrics registry
	// because hooks run in reverse registration order. | 运行时 GC 循环属于同一生命周期；由于钩子
	// 按注册逆序执行，它会在指标注册表之前停止。
	a.OnShutdown(func(ctx context.Context) error { return a.runtimeGC.Stop(ctx) })

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

	a.UseObserved(metricsx.Descriptor{Name: "recover", Title: "Panic recovery | 异常恢复", Scope: metricsx.ScopeGlobal}, middleware.Recover(a.log))
	a.UseObserved(metricsx.Descriptor{Name: "request-id", Title: "Request ID | 请求 ID", Scope: metricsx.ScopeGlobal}, middleware.RequestID(""))
	a.UseObserved(metricsx.Descriptor{Name: "access-log", Title: "Access log | 访问日志", Scope: metricsx.ScopeGlobal}, middleware.AccessLog(a.log))
	a.UseObserved(metricsx.Descriptor{Name: "compress", Title: "Compression | 响应压缩", Scope: metricsx.ScopeGlobal}, middleware.Compress(middleware.CompressOptions{}))
	a.UseObserved(metricsx.Descriptor{Name: "cors", Title: "CORS | 跨域", Scope: metricsx.ScopeGlobal}, middleware.CORS(middleware.CORSOptions{}))

	bundle, err := a.i18nBundle()
	if err != nil {
		a.log.Warn("i18n catalogs unavailable, middleware skipped | 语言目录不可用，已跳过 i18n 中间件",
			ydlog.Err(err))
	} else {
		a.Set(ServiceI18n, bundle)
		a.UseObserved(metricsx.Descriptor{Name: "i18n", Title: "i18n | 多语言", Scope: metricsx.ScopeGlobal}, i18n.Middleware(bundle, i18n.Options{}))
	}

	// 静态缓存必须最后登记：i18n 会为自己的响应重写 Vary，先登记会让静态资源丢失
	// Accept-Encoding。作为最内层全局中间件，它在路由处理器之前落头，且不改变压缩行为。 |
	// The static cache registers last: i18n rewrites Vary for its own responses, so an earlier registration would
	// make static assets lose Accept-Encoding. As the innermost global middleware it writes the headers right before
	// the route handler and does not change compression behaviour.
	a.UseObserved(StaticCacheDescriptor, a.StaticCache())

	a.router.StaticFS(staticURLPrefix, a.staticFS)

	a.log.Info("app assembled | 应用已装配", ydlog.Str("env", a.env()))
	return a, nil
}

// ---------------------------------------------------------------- accessors | 访问器

// Config returns the layered configuration handle. | Config 返回分层配置句柄。
func (a *App) Config() *config.Config { return a.cfg }

// Logger returns the application logger. | Logger 返回应用日志器。
func (a *App) Logger() *ydlog.Logger { return a.log }

// RuntimeGC returns the runtime garbage-collection scheduler. It is always non-nil after
// New; use its Stats for observability and RunOnce for an operator-triggered collection.
// | RuntimeGC 返回运行时垃圾回收调度器。New 之后它始终非 nil；用 Stats 观测，用 RunOnce
// 执行运维触发的一次性回收。
func (a *App) RuntimeGC() *gc.Scheduler { return a.runtimeGC }

// FS returns the asset filesystem (templates/, static/, langs/). | FS 返回资产文件系统。
func (a *App) FS() afero.Fs { return a.fs }

// Router returns the underlying router. | Router 返回底层路由器。
func (a *App) Router() *router.Router { return a.router }

// MiddlewareMetrics returns the registry observing middleware runtime behaviour. | MiddlewareMetrics
// 返回观察中间件运行时行为的注册表。
//
// The registry lives as long as the application: built-in middleware is registered by Default and
// custom middleware can join through UseObserved or Registry.Wrap. | 注册表与应用同寿：内建中间件由
// Default 注册，自定义中间件可以通过 UseObserved 或 Registry.Wrap 加入。
func (a *App) MiddlewareMetrics() *metricsx.Registry { return a.metrics }

// UseObserved registers a middleware and its runtime descriptor in one call. | UseObserved 一次注册中间件
// 及其运行时描述符。
//
// The descriptor controls how the middleware appears on the dashboard; the observed middleware keeps
// exactly the original semantics. | 描述符决定中间件在仪表盘上的呈现方式；被观察的中间件保持原有语义不变。
func (a *App) UseObserved(d metricsx.Descriptor, mw router.Middleware) {
	a.Use(a.metrics.Wrap(d, mw))
}

// Server returns the HTTP server; it is built but not started until Run. | Server 返回 HTTP 服务器；它已构建，但直到 Run 才启动。
func (a *App) Server() *server.Server { return a.server }

// NodeID returns the stable node identity resolved at construction. It is safe
// to use as a message id, Redis key segment and metric label. | NodeID 返回构造时解析出的
// 稳定节点身份，可安全用于消息 id、Redis 键片段与指标标签。
func (a *App) NodeID() string { return a.nodeIdentity.ID }

// NodeIdentity returns the resolved identity together with its source,
// environment, durability and stability flags. | NodeIdentity 返回解析出的身份及其来源、环境、
// 持久性与稳定性标记。
func (a *App) NodeIdentity() nodeid.Identity { return a.nodeIdentity }

// nodeOptions maps the node section and decides whether a missing identity file
// may be created. Tests are read-only so a test run never writes into the
// developer's configuration directory. | nodeOptions 映射 node 段并决定是否允许创建缺失的
// 身份文件。测试保持只读，避免测试运行写入开发者的配置目录。
func (a *App) nodeOptions() nodeid.Options {
	options := nodeid.OptionsFromConfig(a.cfg)
	options.Create = !isTestProcess()
	return options
}

// WebTransport returns the HTTP/3 WebTransport server, or nil when
// server.http3.webtransport is off. | WebTransport 返回 HTTP/3 WebTransport 服务器；
// server.http3.webtransport 关闭时为 nil。
//
// The server exists only after Start, so a route handler must call this at
// request time rather than capture the value during wiring. | 该服务器只在 Start 之后
// 存在，因此路由处理器必须在请求时调用本方法，而不是在装配期捕获返回值。
func (a *App) WebTransport() *webtransport.Server {
	if a.server == nil {
		return nil
	}
	return a.server.WebTransport()
}

// Renderer returns the template renderer in use. | Renderer 返回当前使用的模板渲染器。
func (a *App) Renderer() router.Renderer { return a.renderer }

// env returns the runtime environment name taken from the configuration. | env 返回配置中的运行环境名。
func (a *App) env() string {
	if a.cfg == nil {
		return ""
	}
	return envx.Normalize(a.cfg.GetString("app.env"))
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
// arrives or a fatal serve error occurs; it then runs the full shutdown
// sequence (see Shutdown): long-lived connections first, then the HTTP drain,
// then gRPC, then every shutdown hook in reverse registration order. | Run 启动
// HTTP 服务器并阻塞，直到 ctx 被取消、收到信号或出现致命服务错误；
// 随后执行完整的退出流程（见 Shutdown）：先关闭长连接，再排空 HTTP，然后停止 gRPC，
// 最后按注册逆序执行全部关闭钩子。
//
// The serve error and the joined shutdown errors are returned together. | 服务错误与聚合后的退出错误一并返回。
func (a *App) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// Honor server.http2.extended_connect before serving: the standard library
	// reads GODEBUG in package init, so a re-exec may be required. | 在提供服务前执行
	// server.http2.extended_connect：标准库在包 init 时读取 GODEBUG，因此可能需要重启进程。
	if err := ensureHTTP2ExtendedConnect(a.server.Options(), reexecFunc); err != nil {
		return err
	}
	a.log.Info("application starting | 应用启动中",
		ydlog.Str("addr", a.server.Addr()),
		ydlog.Str("env", a.env()),
	)
	// Mark every registered middleware active before the first request arrives, so the dashboard
	// distinguishes "wired but never started" from "running but idle". | 在首个请求到达前把全部已注册
	// 中间件标为 active，使仪表盘能区分「已装配但从未启动」与「运行中但空闲」。
	a.metrics.Start()
	// Start the GC loop before serving; its first deadline is one interval away, so startup
	// itself is never delayed by a collection. | 在提供服务前启动 GC 循环；首个期限在一个间隔之后，
	// 因此启动本身绝不会被回收拖慢。
	a.runtimeGC.Start()

	runErr := a.server.Run(ctx)
	if runErr != nil {
		a.log.Error("http server stopped with an error | HTTP 服务器异常退出", ydlog.Err(runErr))
	}

	// Shutdown runs on a fresh context: the caller's ctx is already done. | 关闭使用全新的上下文：调用方的 ctx 此时已结束。
	closeCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout())
	defer cancel()
	closeErr := a.Shutdown(closeCtx)

	return errors.Join(runErr, closeErr)
}

// shutdownTimeout bounds the shutdown hooks, defaulting to the server timeout. | shutdownTimeout 限定关闭钩子的最长等待，默认取服务器的退出超时。
func (a *App) shutdownTimeout() time.Duration {
	if d := a.server.Options().ShutdownTimeout; d > 0 {
		return d
	}
	return defaultShutdownTimeout
}
