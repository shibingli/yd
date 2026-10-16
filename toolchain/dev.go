package toolchain

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/jsonx"
	"github.com/shibingli/yd/logx"
	"github.com/shibingli/yd/sse"
)

// DevReloadPath is the dev-only SSE endpoint that pushes browser reload events. | DevReloadPath 是仅开发模式注册的 SSE 端点，用于推送浏览器刷新事件。
const DevReloadPath = "/__yd/dev"

// ReloadEventName is the SSE event name consumed by the front-end reload shim. | ReloadEventName 是前端刷新垫片消费的 SSE 事件名。
const ReloadEventName = "reload"

// devTopic is the broker topic carrying reload events. | devTopic 是承载刷新事件的广播主题。
const devTopic = "yd.dev.reload"

// devHeartbeat is the SSE keep-alive interval of the reload endpoint. | devHeartbeat 是热重载端点的 SSE 保活间隔。
const devHeartbeat = 15 * time.Second

// EnvDev and EnvDevPort are injected into the child process. | EnvDev 与 EnvDevPort 会被注入子进程。
const (
	// EnvDev marks a process as started by `yd dev`. | EnvDev 标记进程由 `yd dev` 启动。
	EnvDev = "YD_DEV"
	// EnvDevPort carries the dev server port, which the framework maps to server.port. | EnvDevPort 携带开发服务器端口，框架会将其映射为 server.port。
	EnvDevPort = "YD_SERVER_PORT"
)

// BuildFunc compiles the application and returns the produced binary path. | BuildFunc 编译应用并返回产物二进制路径。
type BuildFunc func(ctx context.Context, o DevOptions) (string, error)

// CommandFunc creates the child process; tests inject a fake. | CommandFunc 创建子进程；测试可注入假实现。
type CommandFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

// ReloadEvent is the JSON payload pushed to the browser by the reload endpoint. | ReloadEvent 是热重载端点推送给浏览器的 JSON 负载。
type ReloadEvent struct {
	// At is when the change was observed. | At 是观测到变更的时间。
	At time.Time `json:"at"`
	// Path is the changed path relative to the project root. | Path 是相对项目根目录的变更路径。
	Path string `json:"path"`
	// Op is the merged change operation. | Op 是合并后的变更操作。
	Op string `json:"op"`
	// Kind is the change classification: go, config or web. | Kind 是变更分类：go、config 或 web。
	Kind string `json:"kind"`
}

// DevOptions configures a DevRunner; the zero value means the full dev experience with every default applied. | DevOptions 配置 DevRunner；零值即应用全部默认值的完整开发体验。
type DevOptions struct {
	// Out receives the child stdout and stderr; nil means os.Stdout. | Out 接收子进程 stdout 与 stderr；nil 表示 os.Stdout。
	Out io.Writer
	// Log is the logger; nil means logx.Default(). | Log 是日志器；nil 表示 logx.Default()。
	Log *logx.Logger
	// Build compiles the application; nil means the built-in go build. | Build 编译应用；nil 表示内置的 go build。
	Build BuildFunc
	// Command creates the child process; nil means ExecCommand. | Command 创建子进程；nil 表示 ExecCommand。
	Command CommandFunc
	// OnChildExit runs after a child has been reaped; nil means no callback. | OnChildExit 在子进程被回收后调用；nil 表示无回调。
	OnChildExit func(pid int, err error)
	// Dir is the project root; empty means the current directory. | Dir 是项目根目录；空表示当前目录。
	Dir string
	// Package is the go build target; empty means ".". | Package 是 go build 目标；空表示 "."。
	Package string
	// TailwindBin is the tailwindcss executable; empty means resolving through ResolveTailwind. | TailwindBin 是 tailwindcss 可执行文件；空表示走 ResolveTailwind 解析。
	TailwindBin string
	// TailwindConfigBin is the tailwind.bin configuration value passed to ResolveTailwind. | TailwindConfigBin 是传给 ResolveTailwind 的配置项 tailwind.bin 取值。
	TailwindConfigBin string
	// TailwindInput is the Tailwind entry stylesheet; empty means DefaultCSSInput. | TailwindInput 是 Tailwind 入口样式表；空表示 DefaultCSSInput。
	TailwindInput string
	// TailwindOutput is the compiled stylesheet; empty means DefaultCSSOutput. | TailwindOutput 是编译产物样式表；空表示 DefaultCSSOutput。
	TailwindOutput string
	// Roots are the watched roots; empty means []string{Dir}. | Roots 是监听根目录；空表示 []string{Dir}。
	Roots []string
	// Ignore adds directory names or paths on top of DefaultIgnoreDirs. | Ignore 在 DefaultIgnoreDirs 之上追加目录名或路径。
	Ignore []string
	// AppArgs are extra arguments passed to the child process. | AppArgs 是传给子进程的额外参数。
	AppArgs []string
	// Port is injected into the child as YD_SERVER_PORT; <=0 means no injection. | Port 以 YD_SERVER_PORT 注入子进程；<=0 表示不注入。
	Port int
	// Debounce is the file event coalescing window; <=0 means DefaultDebounce. | Debounce 是文件事件抖动合并窗口；<=0 表示 DefaultDebounce。
	Debounce time.Duration
	// NoTailwind disables the CSS build entirely (the --no-tailwind flag). | NoTailwind 完全关闭 CSS 构建（--no-tailwind 标志）。
	NoTailwind bool
	// NoWatch disables file watching: build and start the app once (the --no-watch flag). | NoWatch 关闭文件监听：只构建并启动一次应用（--no-watch 标志）。
	NoWatch bool
	// NoReload disables the dev-only reload endpoint (the --no-reload flag). | NoReload 关闭仅开发模式的热重载端点（--no-reload 标志）。
	NoReload bool
}

// changeKind classifies a file change for the dev loop. | changeKind 为开发主循环分类文件变更。
type changeKind string

// Change classifications. | 变更分类。
const (
	// kindNone marks a change irrelevant to the build. | kindNone 表示与构建无关的变更。
	kindNone changeKind = ""
	// kindGo marks Go sources and module files: rebuild and restart. | kindGo 表示 Go 源码与模块文件：重新编译并重启。
	kindGo changeKind = "go"
	// kindConfig marks configuration files: restart only. | kindConfig 表示配置文件：仅重启。
	kindConfig changeKind = "config"
	// kindWeb marks templates, styles and static assets: rebuild CSS and push a reload. | kindWeb 表示模板、样式与静态资源：重建 CSS 并推送刷新。
	kindWeb changeKind = "web"
)

// DevRunner drives the `yd dev` loop: initial build, child process, watcher and reload endpoint. | DevRunner 驱动 `yd dev` 主循环：首次构建、子进程、监听器与热重载端点。
//
// Run owns every goroutine it starts and terminates them before returning; the caller stops the loop by cancelling the context it passed in, which is the Ctrl+C equivalent. | Run 负责它启动的所有 goroutine，并在返回前终止它们；调用方通过取消传入的上下文来停止循环，这正是 Ctrl+C 的等价物。
// The child process is reaped on every exit path of Run, including panics, so a failed or interrupted dev session never leaves an orphan behind. | 无论 Run 以哪条路径退出（包括 panic），子进程都会被回收，因此失败或中断的开发会话不会留下孤儿进程。
type DevRunner struct {
	out io.Writer
	log *logx.Logger
	hub *sse.Broker

	done     chan struct{}
	watcher  *Watcher
	child    *childProc
	bin      string
	opts     DevOptions
	starts   int
	restarts int
	reaped   int
	closeOne sync.Once

	mu      sync.Mutex
	running bool
}

// NewDevRunner normalizes options and creates the runner. | NewDevRunner 归一化配置并创建运行器。
//
// It fails with ErrProjectDir when Dir does not exist. | 当 Dir 不存在时返回 ErrProjectDir。
func NewDevRunner(o DevOptions) (*DevRunner, error) {
	if strings.TrimSpace(o.Dir) == "" {
		o.Dir = "."
	}
	abs, err := filepath.Abs(o.Dir)
	if err != nil {
		abs = filepath.Clean(o.Dir)
	}
	info, statErr := os.Stat(abs)
	if statErr != nil {
		return nil, errx.WrapDetail(statErr, ErrProjectDir, "dir %s | 目录 %s", abs, abs)
	}
	if !info.IsDir() {
		return nil, errx.Detail(ErrProjectDir, "%s is not a directory | %s 不是目录", abs, abs)
	}
	o.Dir = abs
	if o.Debounce <= 0 {
		o.Debounce = DefaultDebounce
	}
	if o.TailwindInput == "" {
		o.TailwindInput = DefaultCSSInput
	}
	if o.TailwindOutput == "" {
		o.TailwindOutput = DefaultCSSOutput
	}
	if len(o.Roots) == 0 {
		o.Roots = []string{abs}
	}
	if o.Log == nil {
		o.Log = logx.Default()
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Build == nil {
		o.Build = defaultBuild
	}
	if o.Command == nil {
		o.Command = newCommand
	}
	return &DevRunner{
		opts: o,
		log:  o.Log,
		out:  o.Out,
		hub:  sse.NewBroker(sse.BrokerOptions{QueueSize: 16}),
		done: make(chan struct{}),
	}, nil
}

// Run builds, starts and watches until ctx is cancelled. | Run 构建、启动并监听，直到 ctx 被取消。
//
// It returns nil on a graceful shutdown. | 优雅退出时返回 nil。
// Every exit path reaps the child: a normal return, an error return and a panic all pass through the deferred stopChild, which signals the child, kills it after the grace period and waits for its reaper. | 每条退出路径都会回收子进程：普通返回、错误返回与 panic 都会经过延迟的 stopChild——它先发信号，超宽限期后强杀，并等待回收 goroutine 收尸。
func (r *DevRunner) Run(ctx context.Context) error {
	if !r.begin() {
		return errx.Detail(ErrDevRunning, "Run called while already running | Run 在运行中被重复调用")
	}
	// 延迟调用后进先出：先停子进程，再释放运行标记。 | Defers run last-in-first-out: the child is stopped first, the running flag is released after.
	defer r.finish()
	defer r.stopChild()

	if !r.opts.NoTailwind {
		r.buildCSS(ctx)
	}
	bin, err := r.build(ctx)
	if err != nil {
		return err
	}
	if err := r.startChild(ctx, bin); err != nil {
		return err
	}

	if r.opts.NoWatch {
		r.log.Info("dev: file watching disabled | dev: 已关闭文件监听", logx.Str("dir", r.opts.Dir))
		<-ctx.Done()
		return nil
	}

	w, err := NewWatcher(WatchOptions{Roots: r.opts.Roots, Ignore: r.opts.Ignore, Debounce: r.opts.Debounce})
	if err != nil {
		return err
	}
	r.setWatcher(w)
	defer func() {
		w.Close()
		r.setWatcher(nil)
	}()
	r.log.Info("dev: watching for changes | dev: 已开始监听文件变更",
		logx.Int("dirs", w.WatchCount()), logx.Strs("roots", r.opts.Roots))

	for {
		select {
		case <-ctx.Done():
			r.log.Info("dev: shutting down | dev: 正在退出")
			return nil
		case ev, ok := <-w.Events():
			if !ok {
				return nil
			}
			r.handle(ctx, ev)
		}
	}
}

// Handler returns the HTTP handler of the dev-only reload endpoint. | Handler 返回仅开发模式热重载端点的 HTTP 处理器。
//
// When reload is disabled the handler answers 404 for every path, so production wiring that mounts it by mistake still exposes nothing. | 关闭热重载时该处理器对所有路径一律响应 404，因此生产环境即使误挂载也不会暴露任何内容。
func (r *DevRunner) Handler() http.Handler {
	if r.opts.NoReload {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(r.serveReload)
}

// ReloadEnabled reports whether the reload endpoint is served. | ReloadEnabled 报告是否提供热重载端点。
func (r *DevRunner) ReloadEnabled() bool { return !r.opts.NoReload }

// Starts returns how many child processes have been started. | Starts 返回已启动的子进程数量。
func (r *DevRunner) Starts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts
}

// Restarts returns how many rebuild-and-restart cycles completed. | Restarts 返回已完成的“重新编译并重启”次数。
func (r *DevRunner) Restarts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.restarts
}

// ChildPID returns the PID of the child the runner currently tracks, or 0 when there is none. | ChildPID 返回运行器当前追踪的子进程 PID，没有子进程时返回 0。
//
// It is the observable handle behind the leak guards: a PID that is still reported after Run returned is a child that was never reaped. | 它是泄漏守卫所依赖的可观测句柄：Run 返回后仍被报告的 PID 就是一个从未被回收的子进程。
func (r *DevRunner) ChildPID() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.child == nil {
		return 0
	}
	return r.child.pid
}

// Reaped returns how many child processes have been reaped by Wait so far. | Reaped 返回迄今为止已被 Wait 收尸的子进程数量。
//
// Together with Starts it states the invariant the leak guards assert: after Run returned, Starts and Reaped are equal. | 它与 Starts 一起构成泄漏守卫断言的不变式：Run 返回后 Starts 与 Reaped 相等。
func (r *DevRunner) Reaped() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reaped
}

// SubscribeReload subscribes to the reload payloads for process-local hosts. | SubscribeReload 供进程内的宿主订阅刷新负载。
//
// It is the bridge a self-hosted dev server (or a later app-side endpoint) uses to forward reloads to the browser. | 它是自托管开发服务器（或后续 app 侧端点）把刷新转发到浏览器的桥梁。
func (r *DevRunner) SubscribeReload() (<-chan sse.Event, func()) { return r.hub.Subscribe(devTopic) }

// begin marks the runner as running, reporting whether it just started. | begin 把运行器标记为运行中，并返回本次是否为首次启动。
func (r *DevRunner) begin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running = true
	return true
}

// finish releases the running flag and ends every reload subscriber. | finish 释放运行标记并结束所有刷新订阅者。
func (r *DevRunner) finish() {
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()
	r.hub.Close()
	r.closeOne.Do(func() { close(r.done) })
}

// setWatcher stores the active watcher. | setWatcher 记录当前生效的监听器。
func (r *DevRunner) setWatcher(w *Watcher) {
	r.mu.Lock()
	r.watcher = w
	r.mu.Unlock()
}

// binPath returns the compiled binary currently in use. | binPath 返回当前使用的编译产物。
func (r *DevRunner) binPath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bin
}

// build compiles the application and records the binary path. | build 编译应用并记录产物路径。
func (r *DevRunner) build(ctx context.Context) (string, error) {
	start := time.Now()
	bin, err := r.opts.Build(ctx, r.opts)
	if err != nil {
		r.log.Error("dev: build failed, keeping the previous binary | dev: 构建失败，继续使用上一次的二进制", logx.Err(err))
		return "", err
	}
	r.mu.Lock()
	r.bin = bin
	r.mu.Unlock()
	r.log.Info("dev: build finished | dev: 构建完成", logx.Str("bin", bin), logx.Dur("elapsed", time.Since(start)))
	return bin, nil
}

// buildCSS rebuilds the Tailwind stylesheet, degrading to a warning when the tool is absent so the dev loop still serves the app. | buildCSS 重建 Tailwind 样式表；工具缺失时降级为告警，让开发循环依然能提供服务。
func (r *DevRunner) buildCSS(ctx context.Context) {
	bin := strings.TrimSpace(r.opts.TailwindBin)
	if bin == "" {
		resolved, err := ResolveTailwind(ToolOptions{
			ConfigBin: r.opts.TailwindConfigBin,
			LocalDir:  r.opts.Dir,
		})
		if err != nil {
			r.log.Warn("dev: tailwindcss not found, skipping the CSS build | dev: 未找到 tailwindcss，跳过 CSS 构建", logx.Err(err))
			return
		}
		bin = resolved
	}
	res, err := BuildCSS(ctx, bin, CSSOptions{
		Input:  r.absPath(r.opts.TailwindInput),
		Output: r.absPath(r.opts.TailwindOutput),
	})
	if err != nil {
		r.log.Error("dev: CSS build failed | dev: CSS 构建失败", logx.Err(err))
		return
	}
	r.log.Info("dev: CSS rebuilt | dev: CSS 已重建",
		logx.Str("path", res.CSSPath), logx.Int("bytes", res.Bytes), logx.Dur("elapsed", res.Duration))
}

// absPath resolves a project-relative path against the project root. | absPath 把项目相对路径解析到项目根目录。
func (r *DevRunner) absPath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(r.opts.Dir, p)
}

// restart optionally rebuilds and then replaces the child process. | restart 可选地重新编译，然后替换子进程。
//
// A failed rebuild keeps the previous binary running. | 编译失败时保留上一次的二进制继续运行。
func (r *DevRunner) restart(ctx context.Context, rebuild bool) error {
	bin := r.binPath()
	if rebuild {
		fresh, err := r.build(ctx)
		if err != nil {
			return err
		}
		bin = fresh
	}
	r.stopChild()
	if err := r.startChild(ctx, bin); err != nil {
		return err
	}
	r.mu.Lock()
	r.restarts++
	r.mu.Unlock()
	return nil
}

// handle reacts to one coalesced change. | handle 对一次合并后的变更作出反应。
func (r *DevRunner) handle(ctx context.Context, ev Event) {
	rel := r.rel(ev.Path)
	switch classify(rel) {
	case kindGo:
		r.log.Info("dev: go change detected, rebuilding | dev: 检测到 Go 变更，重新编译",
			logx.Str("path", rel), logx.Str("op", ev.Op))
		if err := r.restart(ctx, true); err != nil {
			return
		}
		r.broadcast(kindGo, rel, ev.Op)
	case kindConfig:
		r.log.Info("dev: config change detected, restarting | dev: 检测到配置变更，重启子进程",
			logx.Str("path", rel), logx.Str("op", ev.Op))
		if err := r.restart(ctx, false); err != nil {
			return
		}
		r.broadcast(kindConfig, rel, ev.Op)
	case kindWeb:
		r.log.Info("dev: web asset change detected | dev: 检测到 Web 资源变更",
			logx.Str("path", rel), logx.Str("op", ev.Op))
		if !r.opts.NoTailwind {
			r.buildCSS(ctx)
		}
		r.broadcast(kindWeb, rel, ev.Op)
	default:
		r.log.Debug("dev: ignoring change | dev: 忽略该变更", logx.Str("path", rel))
	}
}

// broadcast publishes a reload event to the browser subscribers. | broadcast 向浏览器订阅者发布刷新事件。
func (r *DevRunner) broadcast(kind changeKind, p, op string) {
	if r.opts.NoReload {
		return
	}
	payload, err := jsonx.Marshal(ReloadEvent{Path: p, Op: op, Kind: string(kind), At: time.Now()})
	if err != nil {
		r.log.Warn("dev: cannot encode the reload event | dev: 无法编码刷新事件", logx.Err(err))
		return
	}
	r.hub.Publish(devTopic, sse.Event{Name: ReloadEventName, Data: payload})
}

// rel makes a path relative to the project root. | rel 把路径转换为相对项目根目录的形式。
func (r *DevRunner) rel(p string) string {
	rel, err := filepath.Rel(r.opts.Dir, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

// serveReload streams reload events over SSE. | serveReload 通过 SSE 推送刷新事件。
func (r *DevRunner) serveReload(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != DevReloadPath {
		http.NotFound(w, req)
		return
	}
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed | 方法不允许", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sw, err := sse.NewWriter(w, sse.Options{Heartbeat: devHeartbeat})
	if err != nil {
		http.Error(w, "streaming unsupported | 不支持流式响应", http.StatusInternalServerError)
		return
	}
	defer func() { _ = sw.Close() }()
	// 立即发一帧注释并刷出响应头，客户端不必等到第一次变更才拿到响应。 | Flush an initial comment frame so the client gets its response before the first change.
	if err := sw.Comment("connected | 已连接"); err != nil {
		return
	}
	sw.Heartbeat(devHeartbeat)

	events, cancel := r.hub.Subscribe(devTopic)
	defer cancel()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-r.done:
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if err := sw.WriteEvent(ev); err != nil {
				r.log.Debug("dev: reload stream closed | dev: 刷新流已关闭", logx.Err(err))
				return
			}
		}
	}
}

// defaultBuild compiles the application into bin/.dev-app[.exe]. | defaultBuild 把应用编译到 bin/.dev-app[.exe]。
func defaultBuild(ctx context.Context, o DevOptions) (string, error) {
	pkg := o.Package
	if pkg == "" {
		pkg = "."
	}
	out := filepath.Join(o.Dir, "bin", ".dev-app"+exeSuffix())
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", errx.WrapDetail(err, ErrBuildFailed, "create %s | 创建 %s", filepath.Dir(out), filepath.Dir(out))
	}
	var buf tailBuffer
	cmd := newCommand(ctx, "go", "build", "-o", out, pkg)
	cmd.Dir = o.Dir
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", errx.WrapDetail(err, ErrBuildFailed, "go build %s | go build %s：%s",
			pkg, pkg, truncateOutput(buf.String(), 2048))
	}
	return out, nil
}

// classify maps a project-relative path to its change kind. | classify 把相对项目根目录的路径映射为变更分类。
func classify(rel string) changeKind {
	slash := filepath.ToSlash(strings.TrimPrefix(rel, "./"))
	base := path.Base(slash)
	ext := path.Ext(slash)
	switch {
	case ext == ".go", base == "go.mod", base == "go.sum":
		return kindGo
	case strings.HasPrefix(slash, "configs/"), base == ".env", strings.HasPrefix(base, ".env."):
		return kindConfig
	case strings.HasPrefix(slash, "web/templates/"), strings.HasPrefix(slash, "web/static/"):
		return kindWeb
	}
	switch ext {
	case ".toml", ".yaml", ".yml", ".json":
		return kindConfig
	case ".css", ".html", ".htm", ".tmpl", ".tpl", ".js", ".mjs", ".svg":
		return kindWeb
	}
	return kindNone
}
