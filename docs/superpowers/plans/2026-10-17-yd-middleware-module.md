# yd 实施计划：middleware 主模块的场景适配与整体性能

> 需求来源（用户原话）：
> 「middleware插件主模块，也要进行各类场景的适配、优化、高性能处理、高效率进行。」

## 0. 需求差异说明

只读需求基线未涵盖本节，本文件即差异说明。

## 1. 现状与一个已经埋好的陷阱

`middleware` 包现有 6 个中间件（AccessLog / Compress / CORS / RateLimit / Recover / RequestID）
与一个组合函数 `Chain`。`Chain` 的文档写着「子中间件只应完成自身逻辑，内部调用 `c.Next()`
会提前进入路由器链」——**而本包自带的中间件全都调用 `c.Next()`**。

后果（已按 `Ctx.Next()` 的实现推导确认）：

- `Chain(Recover(l))` 恰好能用（第二次 `Next()` 在链尾幂等）；
- 但 `Chain(Recover(), AccessLog())` 会**静默错序**：`Recover` 内的 `c.Next()` 先跑完业务处理器，
  之后 `AccessLog` 才执行——日志记录的耗时与状态码全是错的；
- 生产代码中 `Chain` **零使用**（仅本包两个测试），所以这是个"还没人踩到"的陷阱，不是线上缺陷。

## 2. 设计

### 2.1 从类型上杜绝误用

- **移除 `Chain`**，改为返回切片：

```go
// Web returns the recommended middleware order for server-rendered HTML applications.
func Web(l *logx.Logger, o Options) []router.Middleware
// API returns the recommended order for JSON APIs.
func API(l *logx.Logger, o Options) []router.Middleware
// Minimal returns request id + recover only.
func Minimal(l *logx.Logger) []router.Middleware
```

  调用方一律 `r.Use(middleware.Web(log, opts)...)`——`router.Use` 天然是嵌套正确的组合方式，
  **不存在"子中间件误用 `Next()`"的可能**，因为类型上根本不再提供那个组合入口。

- `Options` 承载各中间件的可选配置（CORS/限流/压缩/访问日志），避免调用方手工拼 6 个构造函数。

### 2.2 顺序是契约，不是建议

固定顺序并在测试中钉死：

```
RequestID → Recover → AccessLog → CORS → RateLimit → Compress
```

理由逐条写进文档：

- **RequestID 在最前**：后续所有中间件与日志都要能带上它做关联；
- **Recover 在第二**：必须罩住其余全部（包含 AccessLog 的收尾逻辑）；
- **CORS 在 RateLimit 与认证之前**：**预检请求不能被鉴权或限流拦截**，
  否则浏览器侧报的是"CORS 错误"而不是"未授权"——这是排查成本最高的误配；
- **Compress 最后（最贴近处理器）**：它必须看到处理器最终写出的正文；
  放在前面会压缩到中间件自己写的响应。

**测试**：断言 `Web()/API()/Minimal()` 返回的顺序与上述清单逐项相等（golden list）。
顺序被改动时测试立刻红，而不是等到线上出现"CORS 错误其实是 401"。

### 2.3 场景适配

| 场景 | 预设 | 说明 |
|---|---|---|
| 服务端渲染 HTML | `Web()` | 含 CORS、压缩（br/zstd/gzip）、访问日志 |
| JSON API | `API()` | 含 CORS 与限流，压缩策略偏 zstd，访问日志字段含耗时与状态 |
| 内部服务 | `Minimal()` | 只要 RequestID + Recover，零多余开销 |

**跳过机制**：`Options.Skip`（`func(*router.Ctx) bool`）与 `Options.SkipPaths []string`，
让健康检查、静态资源、指标端点不付限流/日志/压缩的成本——这些路径往往 QPS 最高而最不需要这些中间件。

### 2.4 整体性能

- 补**整链基准**：裸处理器 vs 三种预设，报告 ns/op、B/op、allocs/op；
- 定**分配预算**：整链相对裸处理器的**额外分配 ≤ 3 次/请求**（现有限流 8 次、压缩跳过路径 5 次，
  这两项由并行任务压下来后合并计算）；
- 目标延迟：`Minimal()` 增量 < 50 ns，`Web()` 增量 < 1 µs（不含真实压缩）。

### 2.5 文档

包文档重写，包含：
- 固定顺序与**每条顺序的理由**（尤其 CORS 必须在鉴权之前）；
- 三种预设的适用场景与取舍；
- 「中间件只在 `router.Use` 注册，不要自己写组合器」——并说明为什么（`Next()` 语义）。

## 3. 验收

1. `Chain` 已移除，全仓无引用（含测试）；
2. 三种预设的顺序有 golden 测试；
3. 预设经真实 `router.Use` 跑通：请求经过完整链，响应正确、日志字段完整、压缩生效；
4. 整链基准有前后数据，分配预算达标；
5. 既有单中间件的测试全部保留并通过；
6. 全仓门禁全绿。

## 4. 已知取舍

- **移除 `Chain` 是破坏性变更**：但它生产零使用，且语义本身就是错的——保留比移除更危险。
- **预设不是"最佳配置"**：它只是"大多数项目该从哪开始"；每项都可被 `Options` 覆盖。
- **不做动态链**：运行期增删中间件会让顺序契约失效，代价远大于收益。
