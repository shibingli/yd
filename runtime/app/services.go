package app

import (
	"context"
	"errors"
	"io"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
)

// Service keys reserved by the framework. | 框架预留的服务键。
//
// The container itself is open-ended (any string key works); these constants
// only keep the framework-provided handles discoverable. | 容器本身是开放的
// （任意字符串键都可用），这几个常量只是让框架提供的句柄可被检索。
const (
	// ServiceDB is the SQL handle registered by the data layer. | ServiceDB 是数据层注册的 SQL 句柄。
	ServiceDB = "db"
	// ServiceRedis is the Redis client registered by the data layer. | ServiceRedis 是数据层注册的 Redis 客户端。
	ServiceRedis = "redis"
	// ServiceAsynq is the task queue client registered by the data layer. | ServiceAsynq 是数据层注册的任务队列客户端。
	ServiceAsynq = "asynq"
	// ServiceI18n is the message catalog bundle registered by Default. | ServiceI18n 是 Default 注册的语言目录包。
	ServiceI18n = "i18n"
)

// serviceHookPrefix marks the shutdown hooks registered by Set on behalf of
// io.Closer services, so that re-registering a service replaces its hook
// instead of stacking another one. | serviceHookPrefix 标记 Set 为 io.Closer 服务
// 登记的关闭钩子，使同一服务重复注册时替换旧钩子而不是叠加。
const serviceHookPrefix = "service:"

// shutdownHook is one shutdown task: a name for diagnostics plus its function.
// | shutdownHook 是一项关闭任务：一个用于诊断的名称与执行函数。
type shutdownHook struct {
	fn   func(context.Context) error
	name string
}

// Set stores a service under name. | Set 以 name 存放一个服务。
//
// A value implementing io.Closer is closed automatically by Close (and hence by
// Run), so connection pools and clients registered here release their resources
// without extra bookkeeping. | 实现 io.Closer 的值会被 Close（以及 Run）自动关闭，
// 因此在此注册的连接池与客户端无需额外登记释放逻辑。
// Re-registering the same name replaces its close hook; storing a value that is
// not an io.Closer leaves any earlier hook untouched. | 同名重复注册会替换其关闭钩子；
// 存入非 io.Closer 的值不会影响此前的钩子。
func (a *App) Set(name string, v any) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.services[name] = v
	closer, ok := v.(io.Closer)
	if !ok || name == "" {
		return
	}
	a.registerHookLocked(serviceHookPrefix+name, func(context.Context) error { return closer.Close() })
}

// Get returns the service registered under name. | Get 返回以 name 注册的服务。
func (a *App) Get(name string) (any, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	v, ok := a.services[name]
	return v, ok
}

// MustGet returns the service registered under name, or ErrServiceMissing when it is absent;
// it is meant for startup wiring only. | MustGet 返回以 name 注册的服务，服务缺失时返回
// ErrServiceMissing；仅供启动期装配使用。
func (a *App) MustGet(name string) (any, error) {
	v, ok := a.Get(name)
	if !ok {
		return nil, errx.Detail(ErrServiceMissing, "%q", name)
	}
	return v, nil
}

// OnShutdown appends a hook that Close (and Run) runs in reverse registration
// order, i.e. the most recently registered hook runs first. | OnShutdown 追加一个
// 由 Close（以及 Run）执行的关闭钩子，执行顺序与注册顺序相反：最后注册的最先执行。
func (a *App) OnShutdown(fn func(context.Context) error) {
	if fn == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.registerHookLocked("", fn)
}

// registerHookLocked appends a hook, replacing an existing one of the same
// name; the caller must hold a.mu. | registerHookLocked 追加钩子，同名钩子会被替换；
// 调用方必须持有 a.mu。
func (a *App) registerHookLocked(name string, fn func(context.Context) error) {
	if name != "" {
		for i := range a.hooks {
			if a.hooks[i].name == name {
				a.hooks[i].fn = fn
				return
			}
		}
	}
	a.hooks = append(a.hooks, shutdownHook{name: name, fn: fn})
}

// Close runs every registered shutdown hook in reverse registration order and
// joins their errors. | Close 按注册逆序执行全部关闭钩子并聚合其错误。
//
// Close is idempotent: the hook list is snapped before it runs, so a second
// call is a no-op. | Close 幂等：钩子列表在执行前即被取出清空，因此第二次调用不再执行任何钩子。
// A failing hook is logged and never prevents the remaining hooks from running.
// | 失败的钩子会被记录，且不会阻止其余钩子执行。
func (a *App) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	a.mu.Lock()
	hooks := a.hooks
	a.hooks = nil
	a.mu.Unlock()

	var errs []error
	for i := len(hooks) - 1; i >= 0; i-- {
		hook := hooks[i]
		if hook.fn == nil {
			continue
		}
		if err := hook.fn(ctx); err != nil {
			a.log.Error("shutdown hook failed | 关闭钩子执行失败",
				logx.Str("hook", hook.name), logx.Err(err))
			errs = append(errs, errx.WrapDetail(err, ErrShutdown, "hook=%[1]q | 钩子=%[1]q", hook.name))
		}
	}
	return errors.Join(errs...)
}
