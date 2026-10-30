# 定时任务调度（ydx/cron）

`github.com/shibingli/ydx/cron` 是 ydx 的定时任务调度模块：表达式解析、最小堆调度、工作池
执行、超时/重试/并发策略、生命周期管理与 HTTP 管理 API 都在模块内实现，不依赖第三方调度库。
生成项目只创建**一个进程级调度器**，流程引擎的扫描任务（`workflow-scan`）挂载在它上面，业务
也可以在同一实例上注册自己的任务。

> 本文是 yd 生成项目中 cron 接线的唯一使用说明；组件本身的完整 API、语法与性能数据见
> ydx 仓库的 `cron/README.md`。

---

## 1. 装配结构

| 位置 | 职责 |
|---|---|
| `internal/service/cron.go` | 读取 `cron.*` 配置、解析命名空间、按需装配 Redis Locker、启动与停止共享调度器 |
| `internal/controller/controller.go` | 构造 `CronService`，把 `Scheduler()` 交给流程引擎 |
| `internal/service/workflow.go` | 通过 `workflow.WithCronScheduler(shared)` 把扫描任务挂到共享调度器 |
| `internal/router/router.go` | 启动顺序：共享调度器 → 流程扫描；关停顺序相反 |

生成项目**不会**创建第二个定时循环：流程引擎不再使用私有调度器，所有任务共用一个堆、一个
工作池和一份关闭预算。

## 2. 配置

| 键 | 默认 | 说明 |
|---|---|---|
| `cron.namespace` | `yd:<项目名>:cron` | 分布式锁键前缀；留空时按同一规则回退 |
| `cron.node_id` | `node.id` / 身份文件 | 手动触发锁键中的节点标识；留空使用应用节点身份 |
| `cron.concurrency` | `GOMAXPROCS` | 工作协程数 |
| `cron.queue_size` | `1024` | 待运行队列容量 |
| `cron.lock_ttl` | `30s` | 锁有效期，也是计划触发的去重窗口 |
| `cron.lock_timeout` | `5s` | 单次加锁预算 |
| `cron.shutdown_timeout` | `30s` | 在途任务的优雅关闭预算 |
| `cron.history_limit` | `8` | 每个任务的运行记录环大小；负数关闭 |

环境变量沿用既有映射：`YD_CRON_NAMESPACE`、`YD_CRON_NODE_ID`、`YD_CRON_LOCK_TTL` 等。

这些键由 `cron.OptionsFromConfig` 读取；`Store`、`Locker`、`Logger` 属于进程内协作者，由
`internal/service/cron.go` 装配，不从配置读取。

## 3. 多实例命名空间（重要）

Redis Locker 的锁键格式是：

```
<namespace>:lock:<任务 ID>:<计划触发时刻的 UTC 纳秒>
```

手动触发额外带上 `:manual:<node_id>`，因为手动运行没有共享的计划时刻。由此得到两条硬规则：

1. **同一部署的所有副本必须使用相同的 namespace**。否则各副本的锁键互不相同，`Singleton`
   任务会在每个副本上各跑一次。
2. **不同应用或不同环境共用同一套 Redis 时必须使用不同的 namespace**。否则两个部署会抢
   同一把锁：每个周期只有一个部署的 `workflow-scan` 真正执行，另一个部署的催办、定时器与
   通知重投递被静默跳过。

生成配置默认写入 `yd:<项目名>:cron`，已经按应用名隔离；生产与预发共用 Redis 时必须再按
环境改值（例如 `yd:<项目名>:production:cron`）。namespace 不是安全边界，多租户或不可信
负载请使用独立 Redis DB、ACL 或独立实例。

多副本部署要求节点时钟由 NTP 同步：锁键包含计划触发时刻，时钟偏差过大会让同一名义触发算出
不同的键，`Singleton` 的去重随之失效（数据库条件领取仍能避免重复处理，但会产生多余扫描）。

Redis 不可用时的行为：

- **启动期不可用**：`service.NewRedis` 返回 nil，调度器不装配 Locker；所有副本都会扫描，
  正确性仍由数据库的条件领取保证（`ClaimReminder` / `ClaimNotification` / `ListDueTimerTokens`）。
- **运行期故障**：Locker 调用失败会让当次触发行按失败跳过（cron 的失败关闭语义），
  Redis 恢复后下一个周期自动继续；扫描是幂等的，暂停不会丢数据。

## 4. 生命周期与关停

`Controller.StartWorkflowRuntime` 按以下顺序启动：

1. `CronService.Start` 启动共享调度器（上下文与调用方取消解耦，由 `Stop` 掌握关闭预算）；
2. `workflow.Scheduler.Start` 只把 `workflow-scan` 添加进共享调度器，不启动它。

停止钩子按注册顺序**逆序**执行：流程扫描先卸载，共享调度器随后停止。`CronService.Stop` 传入
10 秒的截止时间；cron 取它与 `cron.shutdown_timeout` 的较早者作为等待窗口，超时后取消工作协程
并再等待一次宽限，仍不退出则返回 `ErrShutdownTimeout`。重复调用 `Stop` 是安全的。

## 5. 注册业务任务

共享调度器通过 `Controller.CronScheduler()` 暴露；处理器要在添加任务之前注册：

```go
scheduler := ctrl.CronScheduler()
if scheduler != nil {
    _ = scheduler.Register("billing-sweep", func(ctx context.Context, job *cron.Job) error {
        return billing.Sweep(ctx)
    })
    _ = scheduler.Add(ctx, &cron.Job{
        ID:        "billing-sweep",
        Name:      "billing sweep",
        Task:      "billing-sweep",
        Schedule:  cron.MustParse("@every 10m"),
        Timeout:   5 * time.Minute,
        Retries:   2,
        Overlap:   cron.SkipIfRunning,
        Singleton: true, // 多副本时每个周期只有一个节点执行
    })
}
```

- **定义只在内存**：共享调度器不配置 `Store`，重启后需要重新注册。需要持久化定义的应用
  应另建一个带 `Store` 的调度器，并且**不要**把 `workflow-scan` 挂到它上面：带 Store 的
  `Reload` 会按持久化定义重建任务表，内存中的扫描任务会被丢弃。
- **任务 ID 唯一**：同一共享调度器内不得重复；多个流程服务共用一个调度器时用
  `workflow.WithScanJobID` 区分。
- **共享工作池**：业务任务与 `workflow-scan` 共用 `cron.concurrency` 个工作协程；长任务较多时
  调大 `cron.concurrency`，或把重活交给 asynq 队列，避免流程扫描被挤占。
- **幂等**：`Singleton` 语义是「每次触发至多一个节点执行」，不是跨故障的恰好一次；需要崩溃
  补偿的任务必须自身幂等，并由业务重试。
- **删除**：`scheduler.Remove(ctx, id)`；运行期添加与删除都不写 Store。

## 6. 管理 API（可选）

cron 自带管理路由，但生成项目**默认不挂载**。需要时在受保护的路由组里挂载，并自行套上管理
权限守卫：

```go
if scheduler := ctrl.CronScheduler(); scheduler != nil {
    _ = scheduler.Mount(router, "/mgr/cron")
}
```

`Mount` 的前缀在同一路由器上必须唯一；API 不内建认证与限流，**不得**直接暴露在公开路由组。
可用路由见 ydx 的 `cron/README.md`「管理 API」一节。

## 7. 排查

| 现象 | 检查 |
|---|---|
| 只有一台副本在扫描 | 这是 `Singleton` + Redis Locker 的预期行为；确认 `cron.namespace` 在副本间一致 |
| 所有副本都扫描 | Redis 不可用或启动期探活失败，Locker 未装配；查看启动日志的 `distributed` 字段 |
| 某套部署的扫描完全不执行 | 与另一套部署共用 Redis 却使用了相同的 `cron.namespace`；两套部署必须不同 |
| 扫描在 Redis 抖动时暂停 | Locker 失败关闭是 cron 的既定语义；Redis 恢复后自动继续 |
| 任务添加成功但从不触发 | 共享调度器未启动（查看 `cron scheduler failed to start` 告警）或表达式无未来触发时刻 |
| 关停变慢 | 在途任务不响应 ctx；cron 到 `cron.shutdown_timeout` 后强制取消工作协程 |

相关文档：[`workflow.md`](workflow.md)（流程调度语义）、[`redis-asynq.md`](redis-asynq.md)
（Redis 与 asynq 的分工）、[`node-identity.md`](node-identity.md)（`node.id` 与多副本身份）。
