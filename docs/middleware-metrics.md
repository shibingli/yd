# 系统资源与中间件运行时指标

`runtime/metricsx` 是系统资源与中间件运行时的**唯一抽象层**。管理概览的“系统资源”卡片与“中间件运行时”
卡片都只依赖这一层，因此新增指标来源、替换采样器或加入自定义中间件，都不需要改模板或 HTTP 处理器。

## 1. 抽象接口

```go
type SystemSource interface {
    SystemSnapshot() SystemSnapshot
}

type MiddlewareSource interface {
    MiddlewareSnapshot() []MiddlewareSnapshot
}

type Provider interface {
    Snapshot() Snapshot
}
```

- `SystemSnapshot`：CPU 核心数/利用率、宿主机内存、进程 CPU/内存，以及 `HostCPU` / `HostMemory`
  两个平台可用性标记。平台不提供时对应仪表盘显示“不可用”，不编造数字。
- `MiddlewareSnapshot`：名称、标签、作用域、生命周期状态、请求计数、进行中、中止、错误、panic、
  平均/P95/最大延迟、最近请求时刻，以及调用方自定义的计数器与瞬时值。
- `Snapshot`：`Collected` 时间、`System` 与按注册顺序排列的 `Middleware`。

`Collector` 把一个系统来源与任意数量的中间件来源组合为 `Provider`：

```go
provider := metricsx.NewCollector(metricsService, app.MiddlewareMetrics())
snapshot := provider.Snapshot()
```

nil 来源会被忽略，因此没有系统采样器的项目仍然能展示中间件指标，反之亦然。

## 2. 内建中间件的开箱接入

框架默认链在 `app.Default` 中经 `UseObserved` 注册：

| 名称 | 作用域 | 说明 |
|---|---|---|
| `recover` | global | panic 恢复 |
| `request-id` | global | 请求 ID |
| `access-log` | global | 访问日志 |
| `compress` | global | 响应压缩 |
| `cors` | global | 跨域 |
| `i18n` | global | 多语言 |
| `static-cache` | global | 静态资源缓存策略（最后登记，负责写最终 `Vary` 与 `Cache-Control`） |

`yd new` 生成的项目在此基础上增加 `security`（安全响应头）以及受保护分组的
`promote` / `auth` / `authz` / `tenant`。全部名称、i18n 键与作用域集中在
`internal/middleware/observability.go`，`cmd/app/main.go` 与 `internal/router/router.go`
都通过 `middleware.MetricsDescriptor("name")` 取值。

```go
application.UseObserved(middleware.MetricsDescriptor("security"), middleware.Security())
```

`MetricsDescriptor` 对未知名称返回一个 `custom` 作用域、以名称为显示标签的描述符，因此自定义中间件
可以直接加入：

```go
application.UseObserved(
    middleware.MetricsDescriptor("cache"),
    cacheMiddleware(),
)
```

## 3. 生命周期

每个条目按以下顺序迁移，仪表盘与 API 都能看到：

1. `registered`：已装配，尚未开始服务，也没有请求；
2. `active`：`app.Run` 启动时统一置位，或首个请求到达时置位；
3. `stopped`：`app.Shutdown` 执行注册表的关闭钩子时置位；计数与延迟**保留**，供事后查看。

约束：

- `Start` 在 `Stop` 之后被忽略；关闭后的迟到请求不会把条目翻回 `active`；
- `Stop` 幂等，可重复调用；
- 关闭钩子由 `app.New` 自动登记，项目无需手工接线；
- 注册表挂载的是**同一个中间件实例**时按 `Name` 合并，因此同时挂在公开组与受保护组的
  `tenant` 只占一行，计数合并。

## 4. 请求指标

每个被观察的中间件在请求路径上只做原子更新：

| 字段 | 含义 |
|---|---|
| `Requests` | 调用次数 |
| `InFlight` | 仍在执行的调用数 |
| `Aborted` | 调用了 `Ctx.Abort()` 的请求数 |
| `Errors` | 以 5xx 状态结束的请求数 |
| `Panics` | 发生 panic 并重新抛出的请求数 |
| `AverageLatency` | 算术平均耗时 |
| `P95Latency` | 有限桶直方图的 95 分位（夹到精确最大值） |
| `MaxLatency` | 最长耗时 |
| `LastSeen` | 最近一次请求时刻（UTC） |

panic 只会被**最内层**观察者计数，随后原样重新抛出。这样一次处理器故障不会把链上每一层的
panic 计数都放大一遍，外层 `middleware.Recover` 仍然负责写出 500。

`Aborted` 同样只由最靠近终止点的观察者计数（`Ctx.Abort()` 设置的标志会留给每个外层中间件，
但“谁终止了链”只能有一个答案）。`Errors` 则是“经过该中间件的请求最终以 5xx 结束”，
因此链上每层都会各自记录一次，这是该字段的预期语义。

热路径不取锁、不做反射。延迟直方图的桶数量固定，不随请求增长而分配内存。

## 5. 自定义指标

自动的请求指标之外，中间件可以发布自己的计数与瞬时值：

```go
entry := application.MiddlewareMetrics().Entry(metricsx.Descriptor{
    Name:     "cache",
    TitleKey: "metrics.middleware.name.cache", // 可选；缺省时显示 Title
    Scope:    metricsx.ScopeCustom,
})

entry.Incr("hits")
entry.Add("bytes", uint64(n))
entry.SetGauge("entries", int64(size))
```

- `Incr` / `Add` / `SetGauge` 并发安全；可以按事件调用，但**不要**在每个请求上无意义地重复调用；
- 值会出现在 `/mgr/metrics/middleware` 的同一行里，计数器按名称排序显示；
- 若要接入完全独立的来源（例如数据库连接池），实现 `MiddlewareSource` 并传给
  `metricsx.NewCollector(system, source)` 即可，控制器只认 `Provider`。

## 6. HTTP 与前端

- `GET /mgr/metrics`：系统资源片段。控制器只读取 `Provider.Snapshot().System`，不再直连
  `MetricsService`；片段结构与仪表盘显示保持不变。
- `GET /mgr/metrics/middleware`：中间件运行时片段；
- 两个端点都在受保护分组内，需要有效会话；它们保留为可直接取用的服务端片段（直连、调试与无脚本回退），
  首页不再请求它们；
- 管理概览只建立**一条** WebSocket `/mgr/ws/metrics`：服务端每 5 秒把两块内容合并渲染成一个
  `<hx-partial hx-target="#yd-live-metrics" hx-swap="innerMorph">` 信封推给每条连接，首帧在握手后立即写出。
  连接失败由 hx-ws 扩展按指数退避重连，重连成功后服务端立即补一帧，**不得**另写轮询或 HTTP 重同步；
- 推送端点只写不回显：入站消息一律忽略，回显会替换整块指标面板；
- 表头、状态、作用域与内建中间件名称全部来自
  `web/i18n/zh-CN.yaml` / `web/i18n/en.yaml` 的 `metrics.middleware.*` 键；
  `TestGeneratedI18nCatalogueCoversEveryTrKey` 会拦截缺失或陈旧的键；
- 生命周期徽标：`active` 为成功色，`stopped` 为警示色，`registered` 为中性色。

## 7. 新增中间件的检查清单

1. 通过 `application.UseObserved(descriptor, mw)` 装配，不要只调用 `application.Use`；
2. 在 `internal/middleware/observability.go` 登记稳定名称、`TitleKey` 与作用域
   （或复用 `MetricsDescriptor` 的 custom 回退）；
3. 在两份语言目录加入同名键；
4. 若要发布业务计数，使用 `Registry.Entry(...).Incr/Add/SetGauge`；
5. 运行：

```bash
go build ./... && go vet ./... && go test ./... -race
go test ./tooling/scaffold/ -run 'TestHomeShowsMiddlewareRuntime'
go test ./tooling/scaffold/ -run 'TestGeneratedAppServesFrameworkFeatures'
```

## 8. 禁止事项

- 禁止在处理器或模板里重新计算指标；仪表盘只呈现 `runtime/metricsx` 的快照；
- 禁止为每个请求创建新的 `Entry` 或新的直方图；条目在装配期创建；
- 禁止在请求路径上取注册表写锁、做反射或分配直方图；
- 禁止用 `application.Use` 装配内建中间件后另行“补统计”，否则两套计数会分叉；
- 禁止在关闭后重新置为 `active`，历史计数必须保持可读。
