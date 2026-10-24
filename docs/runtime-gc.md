# 运行时 GC 调度（ydx/gc）

`github.com/shibingli/ydx/gc` 只在**策略允许**的空闲窗口里触发一次 GC，并在需要时把空闲
内存归还操作系统。它不会修改 `GOGC`、内存上限或任何运行时调参，也不接管 Go 自带的并发
回收；Go 的自动 GC 始终照常工作，本组件只决定「额外一次回收与归还」发生在何时。

yd 在 `runtime/app` 中默认装配它：`app.New` 解析 `gc.*` 配置，`app.Run` 启动调度，
`app.Shutdown` 通过生命周期钩子停止。生成项目无需任何接线代码。

---

## 1. 为什么需要它

Go 运行时的后台清扫器会逐步归还内存，但节奏由运行时决定。长驻服务在流量低谷执行一次
`debug.FreeOSMemory` 可以更快把 RSS 降下来；而在繁忙时段做同样的事只会放大尾延迟。
本组件把「何时做」变成可配置、可观测、可无人值守的策略：

- **忙时跳过**：进程 CPU 利用率连续低于阈值达到空闲窗口才开始；
- **没有可回收的东西就不做**：堆低于下限直接跳过；
- **不会因为一直忙而永不回收**：达到内存上限比例时强制执行；
- **不会反复做无用功**：回收量很小时自动把下次间隔翻倍；
- **不会拖垮进程**：回调与调度循环中的 panic 会被恢复并计数。

## 2. 安全模型

| 风险 | 措施 |
|---|---|
| 在业务高峰触发全量回收 | 进程 CPU 利用率门 + 连续空闲窗口；可选在途请求门 |
| 小堆上无意义地回收 | `min_heap` 堆下限，低于它直接跳过本轮 |
| 持续高负载导致内存无限增长 | `pressure_ratio` 达到生效内存上限比例时强制回收（繁忙门被压过） |
| 多副本同时回收造成全局抖动 | 每次间隔施加 `jitter` |
| 空闲堆反复回收浪费 CPU | `adaptive`：回收比例低于 5% 时把间隔翻倍，上限 `max_interval` |
| 回调 panic 杀死进程 | `Load` / `Busy` 回调 panic 被恢复、计入 `Stats.Errors`，并按**失败关闭**处理（未知负载视为繁忙、繁忙门视为繁忙） |
| 探针故障时在高峰误回收 | 连续 3 次没有读数或回调 panic 时按繁忙推迟；只有构造期就静态不可用的探针才关闭负载门 |
| 回收路径 panic 杀死进程 | 定时循环用 recover 收敛，退避到 `max_interval` 后继续；`RunOnce` 返回 `ErrPanic` |
| 并发回收互相重叠 | 定时与手动路径共用一把运行锁，逐次串行 |
| 关闭时协程泄漏 | 只拥有一个协程；`Stop` 取消并等待退出，超时返回 `ErrStopTimeout` |
| 采样本身造成停顿 | 只用 `runtime/metrics`，**不**调用会停止世界的 `runtime.ReadMemStats` |

调度器不修改 `GOGC` 与 `debug.SetMemoryLimit`：内存上限只被读取，用于计算压力阈值。

## 3. 策略与默认值

`gc.strategy` 选择一个预设，`gc.*` 单项键覆盖预设：

| 键 | gentle | balanced（默认） | aggressive |
|---|---:|---:|---:|
| `mode` | `soft` | `release` | `release` |
| `interval` | 30m | 10m | 2m |
| `max_interval` | 2h | 1h | 30m |
| `sample_interval` | 10s | 5s | 2s |
| `quiet_window` | 60s | 30s | 10s |
| `load_threshold` | 20 | 30 | 60 |
| `min_heap` | 64 MiB | 32 MiB | 8 MiB |
| `pressure_ratio` | 0.90 | 0.85 | 0.80 |
| `jitter` | 0.15 | 0.10 | 0.10 |
| `adaptive` | true | true | true |

另有 `off` 关闭后台调度；`App.RuntimeGC().RunOnce(ctx)` 仍可手动执行。单项键只在**显式
配置**时覆盖预设（配置文件、`YD_GC_*` 环境变量或绑定参数），因此预设与逐项调优可以组合。

### 配置键

| 键 | 默认 | 说明 |
|---|---|---|
| `gc.enabled` | `true` | 后台调度开关；关闭后仍可手动执行 |
| `gc.strategy` | `balanced` | `gentle` / `balanced` / `aggressive` / `off` |
| `gc.mode` | `release` | `soft` = `runtime.GC`；`release` = `debug.FreeOSMemory` |
| `gc.interval` | 10m | 两次调度回收的基础间隔 |
| `gc.max_interval` | 1h | 自适应退避上限，必须 ≥ `interval` |
| `gc.sample_interval` | 5s | 到期待回收时的负载采样间隔 |
| `gc.quiet_window` | 30s | 负载必须连续低于阈值的时长 |
| `gc.max_defer` | 0 | 繁忙超过此时长后强制执行；0 表示不强制（内存压力仍可强制） |
| `gc.min_heap` | 32 MiB | 堆对象字节下限；低于它跳过本轮；0 关闭 |
| `gc.load_threshold` | 30 | 进程 CPU 利用率百分比 [0,100]；0 关闭负载门 |
| `gc.pressure_ratio` | 0.85 | 达到生效内存上限该比例时强制回收；0 关闭 |
| `gc.jitter` | 0.1 | 间隔相对抖动，取值 [0,1) |
| `gc.adaptive` | true | 回收量过小时增大下次间隔 |
| `gc.max_inflight` | 0 | 在途中间件调用达到该值视为繁忙；0 关闭（yd 侧接线） |

环境变量按既有规则映射：`YD_GC_STRATEGY` → `gc.strategy`、`YD_GC_INTERVAL` → `gc.interval`。

## 4. 判定顺序

每次到期后循环采样，按以下优先级判定：

1. **内存压力**：`mapped >= min(Go 软上限, 宿主/cgroup 上限) × pressure_ratio` 时立即回收，
   无视繁忙门（`Forced = true`）；随后的下次复查缩短到 1 分钟（或基础间隔，取较小者）；
2. **堆下限**：`heap_objects < min_heap` 时跳过本轮；
3. **繁忙门**：`Busy()` 为真或采样负载 ≥ `load_threshold` 时推迟，每个
   `sample_interval` 重新采样；连续空闲 `quiet_window` 后才进入第 4 步；
4. **回收**：按 `mode` 执行 `runtime.GC` 或 `debug.FreeOSMemory`；
5. **自适应**：回收比例 ≥ 5% 时重置为基础间隔，否则把间隔翻倍直到 `max_interval`。

负载默认取进程自身的 CPU 利用率（`runtime/metrics` 的 CPU 类别指标，相对 `GOMAXPROCS`），
不是宿主 CPU 利用率；容器内因此不会被邻居的负载误判为繁忙。探针连续 3 次没有读数时按
**失败关闭**处理（视为繁忙并推迟），记录 warn，避免坏探针在高负载期触发回收。只有构造期
就静态不可用的探针（运行时未发布相应指标）才关闭负载门，以免永久不再回收；此时内存压力
覆盖仍然有效。

## 5. yd 中的接线

- `app.New`：`gc.OptionsFromConfig` → `gc.New`，配置非法直接让启动失败
  （哨兵 `app.ErrRuntimeGC`）。
- `app.Run`：在 HTTP 服务开始前 `Start`；首个期限在一个 `interval` 之后，启动不会被回收拖延。
- `app.Shutdown`：通过 `OnShutdown` 钩子 `Stop`，按逆序先于指标注册表停止。
- `gc.max_inflight > 0` 时，`Busy` 绑定到 `runtime/metricsx.Registry.InFlight()`：
  只要有请求经过已观察中间件链，就推迟回收。该值是多条中间件条目之和，一个请求可能被
  多次计入，因此用于「是否有活动」而不是精确请求数。
- `App.RuntimeGC()` 暴露调度器，可读取 `Stats()` 或执行 `RunOnce(ctx)`。

```go
a, err := app.Default()
if err != nil {
    return err
}

// 运维触发的一次性回收（绕过负载/空闲/堆下限门，但仍与定时回收串行）。
res, err := a.RuntimeGC().RunOnce(ctx)
if err != nil {
    return err
}
log.Info("manual gc | 手动 GC",
    log.Uint64("reclaimed", res.Reclaimed),
    log.Dur("duration", res.Duration),
)

// 观测：最近一次结果、累计次数、推迟/跳过/强制次数、恢复的 panic 次数。
stats := a.RuntimeGC().Stats()
```

## 6. 观测

每次回收写一条 INFO 日志（`mode`、`heap_before`、`heap_after`、`reclaimed`、`released`、
`gc_cycles`、`duration`、`forced`、`manual`）。繁忙推迟只在进入繁忙时写一条 DEBUG；
恢复的 panic 最多每分钟写一条 ERROR，其余降为 DEBUG。

`Stats` 字段：

| 字段 | 含义 |
|---|---|
| `Started` / `LastRun` / `NextRun` | 启动、最近完成、下次到期时刻 |
| `Last` | 最近一次 `Result` |
| `Interval` | 当前（可能已退避的）间隔 |
| `Runs` | 已完成回收数（含手动） |
| `Deferred` | 观察到繁忙并推迟的到期周期数 |
| `SkippedHeap` | 因堆下限跳过的周期数 |
| `Forced` | 因内存压力或 `max_defer` 强制执行的次数 |
| `Errors` | 恢复的 panic 次数 |

`Result.Reclaimed` 是回收前后的堆对象字节差，`Released` 是归还操作系统内存的增量。
两者为 0 不表示回收失败：可能堆上全是存活对象。

## 7. 性能边界

- 空闲时每 `interval` 只有一个定时器，不采样；
- 到期待回收时才按 `sample_interval` 读取指标；每次读取是 `runtime/metrics` 的固定长度
  采样，不分配、不停止世界；
- 定时器、日志与 `Stats()` 都在低频路径；请求热路径完全不经过本组件；
- `mode: release` 会执行一次完整 GC 加清扫，代价高于 `soft`；这正是它必须被空闲门与堆下限
  约束的原因。

## 8. 调优建议

- **延迟敏感、流量稳定**：`strategy: gentle`，或 `balanced` + `load_threshold: 15`；
- **内存受限容器**：`strategy: aggressive`，必要时把 `pressure_ratio` 降到 0.75；
- **想彻底让出控制权**：`gc.enabled: false`，仅保留 `RunOnce` 的手动入口；
- **有明确的活动信号**（在途请求、任务数）：设置 `gc.max_inflight`，或直接构造
  `gc.Options{Busy: ...}` 注入自定义门。

## 9. 测试与守卫

`ydx/gc` 覆盖：预设与配置覆盖、CPU/堆探针、判定优先级、繁忙推迟与恢复、压力强制、
自适应退避、并发 `RunOnce`、停止超时、回调与回收路径 panic 恢复。yd 侧覆盖默认接线、
非法配置拒绝启动、运行期的真实回收与关闭钩子停止。
