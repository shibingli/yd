# yd 实施计划：Recover 的场景覆盖与安全稳定

> 需求来源（用户原话）：
> 「Recover插件也要适配更多的应用场景，并且可以安全、高效、稳定的实现Recover的应用功能」

## 0. 需求差异说明

只读需求基线未涵盖本节，本文件即差异说明。

## 1. 现状与其结构性盲区

现有 `middleware.Recover` 在处理器链上 `defer recover()`：记日志（panic 值 + `debug.Stack()`）、
`c.Abort()`、未写出响应时回 500。非 panic 路径实测约 0–14 ns，基本免费。

**盲区（按影响排序）**：

| # | 场景 | 现状 | 后果 |
|---|---|---|---|
| **1** | **处理器内 `go func()` 里的 panic** | **完全抓不到** | **整个进程崩溃**——这是最严重的一条 |
| 2 | `panic(nil)` | Go 1.21+ 会得到 `*runtime.PanicNilError`，现有 `rec == nil` 判断可用，但无测试 | 行为未固化，升级 Go 版本时可能静默变化 |
| 3 | 响应已开始写出后 panic | 跳过 500（正确），但没有可观测信号 | 客户端拿到**截断的响应**且无从知晓 |
| 4 | 需要定制错误响应 | 固定回裸 500 纯文本 | htmx 请求拿到整页纯文本、API 请求拿到纯文本而非 JSON |
| 5 | panic 栈可能极大 / 含敏感内容 | 无条件 `debug.Stack()` 全量入日志 | 日志爆量；栈里的路径/值可能含敏感信息 |
| 6 | 开发期需要让 panic 暴露出来 | 一律吞掉 | 调试时看不到真实崩溃点 |
| 7 | 恢复路径自身 panic | 无保护 | 二次 panic 会让进程崩溃，违背"稳定" |
| 8 | 没有可观测钩子 | 无计数/无回调 | 无法做告警与指标 |

## 2. 设计

### 2.1 安全协程启动器（补上最大的盲区）

```go
func Go(l *logx.Logger, fn func())                 // 等价于 go fn()，但 panic 不会崩溃进程
func GoContext(ctx context.Context, l *logx.Logger, fn func())
```

- 内部 `defer recover()`，panic 时记日志并**不再向上传播**；
- **必须在文档里写明这是唯一受支持的「后台起协程」方式**，并给出反例（裸 `go func()` 的 panic 会终止进程）；
- 与优雅退出打通：`GoContext` 的协程在 ctx 取消后应能退出（不强求，但文档说明）。

### 2.2 `Recover` 的可配置化

```go
type RecoverOptions struct {
    Logger      *logx.Logger
    OnPanic     func(c *router.Ctx, rec any, stack []byte)   // 自定义响应；nil 用默认 500
    Metrics     func(PanicInfo)                              // 计数/告警钩子
    MaxStack    int                                          // 栈上限字节（默认 64 KiB，0 关闭）
    Repanic     bool                                         // 开发期复抛
    SkipPaths   []string                                     // 健康检查等路径不记录
}
```

- **恢复路径自身必须不可 panic**：整个 recover 分支再套一层 `defer recover()`，
  二次 panic 只记一条"recovery failed"日志，绝不让进程崩溃；
- **响应已写出**时：不发 500，但通过 `Metrics` 与日志明确标记"响应已截断"；
- **`panic(nil)`**：显式归一为空值标记，并有测试钉住 Go 当前语义；
- **栈截断**：按 `MaxStack` 截断并标注"truncated"，避免日志爆量；
- **默认行为保持向后兼容**：`Recover(l)` 签名不变，内部委托给新实现。

### 2.3 场景清单（每个都要有测试）

1. 处理器 panic → 500、链中止、日志含 panic 值与栈；
2. 响应已写出后 panic → 不改状态码、有截断标记；
3. `panic(nil)` → 仍被捕获；
4. **协程内 panic（`Go`）** → 进程存活、日志完整；
5. 定制 `OnPanic` → 走自定义响应（htmx 片段 / JSON）；
6. `Repanic=true` → panic 继续向上传播；
7. `MaxStack` 截断 → 栈长不超过上限且带 truncated 标记；
8. 恢复路径内二次 panic → 进程不崩、只记日志；
9. 并发大量 panic → `-race` 干净、无协程泄漏；
10. `Logger == nil` / `OnPanic == nil` / 空栈 → 不 panic。

## 3. 效率

- **非 panic 路径零成本**：现有 `defer recover()` 约 0–14 ns，新实现不得显著劣化
  （基准门限：相对裸处理器增量 < 50 ns）。
- panic 路径的开销（`debug.Stack()`、日志）本就罕见，允许分配。
- `Metrics` 回调不得在热路径上被调用（只有 panic 时才调）。

## 4. 验收

1. 上述 10 个场景全部有测试且通过，其中**协程 panic 不崩溃进程**要有一个真实的子进程级验证
   （测试二进制再执行自己作为子进程触发协程 panic，父进程断言子进程正常退出而非崩溃）；
2. `-race` 全绿；`gofmt`/`vet` 零输出；
3. 非 panic 路径的基准增量 < 50 ns；
4. 现有 `Recover(l)` 的调用点**不需要修改**（向后兼容）。

## 5. 已知取舍

- **不拦 `runtime.Goexit`**：它不是 panic，`recover()` 抓不到，也不该抓。
- **不拦不可恢复的 fatal error**（栈溢出、内存耗尽）：Go 运行时层面无法恢复，文档说明即可。
- **`Go()` 不是万能兜底**：它只保护经它启动的协程；裸 `go func()` 仍会崩进程——因此文档与
  治理约束都要写明"后台协程必须经 `middleware.Go` 启动"。
