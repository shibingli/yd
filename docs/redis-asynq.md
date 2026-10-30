# Redis 与 asynq 使用分区指南

本文回答一个问题：**同一个功能里，什么时候直接用 Redis 命令，什么时候走 Redis Pub/Sub，
什么时候交给 asynq，什么时候交给 cron。** 它们不是互相替代的关系——选错不会报错，只会在故障时
表现为「消息偶尔丢了」「任务重复执行」「延迟莫名升高」，因此必须按投递语义而不是按熟悉程度选择。

适用范围：yd v1.5.0+、ydx v0.1.x。

## 1. 一句话结论

| 机制 | 投递语义 | 用在哪 | 不能用在 |
|---|---|---|---|
| Redis 直接命令（含 Lua） | 单键原子；多键需脚本或事务 | 共享状态、协调、计数、去重、游标、在线状态、限流、锁、缓存 | 当消息通道（轮询 list / 轮询键） |
| Redis Pub/Sub | **至多一次**、不持久、无确认、无消费组 | 实时扇出、缓存失效广播、可丢的控制信号 | 任何「不能丢」的工作；离线补发 |
| asynq | **至少一次**、持久、重试/退避/归档、可延迟 | 失败补发、离线通知、重活、延迟/一次性任务、外部副作用、outbox 中继 | 实时广播（一个任务只被一个 worker 消费）；周期调度 |
| cron（`ydx/cron`） | 按计划触发；多机 `Singleton` 由 Redis 锁保证每次至多一个节点执行 | 周期作业、扫描、清理、报表、对账 | 需要持久重试的一次性任务（用 asynq）；需要恰好一次的业务（必须自身幂等） |

三条硬规则：

1. **实时路径用 Pub/Sub，持久路径用 asynq，周期路径用 cron，共享状态用直接命令。**
2. **asynq 不是广播总线**：任务队列的语义是「一个任务交给一个消费者」，让每个节点都收到
   必须为每个节点各建一个任务，扇出成本随节点数线性增长，且实时延迟受队列积压影响。
3. **Pub/Sub 不是可靠队列**：没有确认、没有重放；订阅者断线期间的消息就是丢了。要可靠，
   先把事实写进数据库（或 outbox），再通知；或者用 asynq。

## 2. 三问决策法

```text
这条数据/事件丢了会怎样？
├─ 不可接受
│    ├─ 需要所有节点都看到同一份？
│    │    ├─ 是 → 业务表/outbox 持久化 + asynq 逐节点投递（或由各节点自行补拉）
│    │    └─ 否 → asynq（一个任务一个消费者，重试到成功或归档）
│    └─ 只要最终一致 → 数据库为准 + 通知用 Pub/Sub + 定期对账/补拉
└─ 可接受（有补偿机制）
     ├─ 所有节点都要看到 → Redis Pub/Sub
     └─ 只是共享状态 → Redis 直接命令（键 + TTL；必要时 Lua 保证原子）
```

补充判断：

- **延迟预算 < 10ms**：不要经过 asynq。asynq 正常也在毫秒级，但队列积压、调度轮询和重试
  会让尾延迟不可预测。
- **单条载荷 > 100KiB**：不要内联进 Pub/Sub。发布一个键（直接 Redis 写入）再加一条
  「键已更新」的通知，让订阅者按需读取，避免一次发布放大成 N 份大包。
- **需要严格顺序**：asynq 不保证顺序（重试会打乱），Pub/Sub 只保证单连接内发布顺序。
  顺序应由业务序号 + 消费端重排解决，或使用单消费者队列并把并发设为 1。
- **按固定周期反复执行**：用 cron，不要用 asynq 反复入队。多机语义由 `Singleton` + 同一个
  `cron.namespace` 下的 Redis 锁保证（见 `docs/cron.md`）。
- **需要「恰好一次」**：它们都做不到。做到「至少一次 + 幂等消费」才是工程上可交付的语义。

## 3. 能力对照

| 维度 | 直接命令 | Pub/Sub | asynq | cron |
|---|---|---|---|---|
| 持久化 | 取决于 AOF/RDB，可能被逐出 | 无 | 有（Redis 持久化 + 租约恢复） | 定义可选 Store；共享调度器只存内存 |
| 确认/重试 | 无（调用方自己重试） | 无 | 有（MaxRetry、退避、归档） | 有任务级 Retries/退避；无跨故障恰好一次 |
| 扇出 | 无（读方各自读） | 天然扇出 | 每个消费者一个任务 | 非 Singleton 时每个节点都执行 |
| 顺序 | 单连接命令顺序 | 单发布者内有序 | 不保证 | 按触发时刻；同刻任务由工作池并发 |
| 定时/延时 | 需自己实现（ZSET） | 无 | 原生 `EnqueueIn` / `EnqueueAt` | 原生 cron/Quartz 表达式与 `@every` |
| 背压 | 无（读方控制） | Redis 输出缓冲满会断开慢订阅者 | 队列积压，可监控 | 有界队列，满时丢弃并计数 |
| 典型延迟 | 亚毫秒~毫秒 | 亚毫秒~毫秒 | 毫秒~秒（取决于队列） | 毫秒级（取决于调度循环） |
| 典型故障 | 键被逐出/过期、集群 CROSSSLOT | 订阅者断线丢消息、慢订阅者被断开 | 任务重复、队列积压、Redis 内存不足 | 命名空间冲突、锁失败跳过、节点时钟漂移 |

## 4. 当前功能落位

| 功能 | 机制 | 实现位置 | 说明 |
|---|---|---|---|
| 消息中心实时事件 | 本地扇出 + Pub/Sub + asynq 补发 | `messaging/realtime`、脚手架 `internal/service/message_bus.go` | 先本地投递，再广播；广播失败入 `yd:message:event` |
| 通用实时主题 | 本地扇出 + Pub/Sub + 可选 asynq 补发 | `ydx/realtime`（`RetryQueue` / `PublishRemote`） | v0.1.3 起提供持久补发扩展点 |
| 二级缓存失效 | 直接命令 + Pub/Sub + 租约兜底 | `ydx/cache` | L2 写后发事件；丢事件由 L1 租约兜底 |
| GORM 查询缓存 | 同上 | `ydx/cache/gormcache` | 表代际推进 + 广播 |
| 限流 | 直接命令（单键 Lua） | `ydx/middleware/ratelimit.RedisStore` | 令牌桶在 Lua 内原子完成 |
| 未读数/已读水位 | 数据库 | `messaging`（`last_read_at`） | 事件只负责通知，不作为计数事实 |
| SSE 断线补拉 | 业务数据库 | `adapter.SSE` 写 `id:` + 业务端点 | 不依赖 Redis 保存历史 |
| 系统与中间件指标 | 进程内原子计数 | `runtime/metricsx` | 不跨节点聚合 |
| 后台任务 / 延迟任务 | asynq | `ydx/asynq` | 通用任务层，按需接入 |
| 周期调度（扫描、清理、报表） | cron + Redis 锁 | `ydx/cron`、脚手架 `internal/service/cron.go` | 周期定义只存内存时不要用 Store；命名空间见 `docs/cron.md` |

> 注意：`ydx/realtime` 与 `yd/messaging/realtime` 是两个层次。前者是传输无关的通用中枢，
> 后者是消息中心的领域 Hub（按租户/受众过滤）。二者都遵循同一分区规则，业务侧不要另写
> 一套「本地 map + Redis 轮询」的广播。

## 5. 逐场景规则

### 5.1 直接操作 Redis

**适用**：值可以被重建、丢失有兜底、需要跨请求/跨节点原子读写的状态。

| 场景 | 键形态 | 要点 |
|---|---|---|
| 幂等/去重 | `yd:idem:<scope>:<id>` + `SET NX EX` | 以业务事件 ID 为键；TTL 覆盖最大重试窗口 |
| 在线状态 | `yd:presence:<tenant>:<user>` 哈希（连接 ID → 心跳时间）+ `EXPIRE` | 心跳续期；读取时过滤过期条目，不用 `KEYS` |
| 分布式锁 | `SET key token NX PX ttl` + Lua 比对删除 | 锁只是效率优化；临界区仍需数据库约束/版本号兜底 |
| 计数/配额 | `INCR` + `EXPIRE`，或 Lua 原子「读-判断-写」 | 计数可从数据库重建；不要把唯一事实只放在 Redis |
| 游标/水位 | 单键或哈希 | 与业务表同事务更新时，以数据库为准，Redis 只做加速 |
| 限流 | 单键 Lua | 已由 `middleware.RedisStore` 封装，直接复用 |

硬约束：

- **每个键都要有 TTL**（锁、去重、在线、计数一视同仁）；无 TTL 的键在故障恢复后会变成
  永久脏状态。
- **多键原子性用 Lua，不要用「先读后写」**；Redis Cluster 下多键脚本必须使用相同 hash tag，
  否则 `CROSSSLOT`。`ydx/cache` 的远端存储就是单键 Lua 的范例。
- **禁止在请求热路径使用 `KEYS` / `SCAN` / `FLUSHDB`**；遍历用游标并限流。
- **禁止把 Redis 当唯一事实源**：内存压力下的逐出、AOF 回放窗口、主从切换都可能让键消失。
- **不要在请求路径使用阻塞命令**（`BLPOP` 等）；队列语义交给 asynq。

### 5.2 Redis Pub/Sub

**适用**：现在就要送达、丢了可以由别的机制补上的通知。

| 场景 | 频道 | 补偿机制 |
|---|---|---|
| 消息中心事件 | `yd:message:events` | 页面重连后重取历史/会话片段；失败广播入 asynq |
| 通用主题广播 | `realtime.channel_prefix + topic`（默认 `yd:rt:`） | 订阅者重连后自行补拉；可选 `RetryQueue` |
| 缓存失效 | `cache.redis.channel` | L1 租约到期强制回源；重连清空 L1 |
| 控制信号（踢下线、刷新配置） | 自定义前缀 | 定期对账；下次请求重新校验 |

硬约束：

- **频道名必须带部署级前缀**，同一 Redis 上的多个环境不得共用前缀。
- **订阅端必须有有界队列与慢消费者策略**；Redis 的 `client-output-buffer-limit pubsub`
  超出后会直接断开慢订阅者，应用侧的有界队列只能保护自己，保护不了 Redis。
- **发布端不要等待订阅端**；Pub/Sub 没有确认，等待没有意义。
- **不要用 keyspace notifications 做失效**：它是节点本地事件、默认关闭，且集群下不保证
  全节点可见。
- **消息体保持小**；大对象写键后发通知。
- **重连必须视为「可能漏消息」**：清空本地缓存、重取快照、重置游标；不要假设断线期间
  的消息会在重连后到达。

### 5.3 asynq

**适用**：必须最终完成、可以晚一点、失败要重试的工作。

| 场景 | 任务形态 | 要点 |
|---|---|---|
| 广播补发 | 任务载荷 = 原事件（含 ID/来源节点） | worker 调 `PublishRemote`，**不得再本地投递** |
| 离线通知/推送 | 载荷只放用户 ID 与消息 ID | worker 重新读库，避免携带过期快照 |
| 附件处理/缩略图/扫描 | 载荷放附件 ID | 幂等：重复处理覆盖同一产物 |
| 外部副作用（邮件、Webhook） | 载荷放业务 ID + 目标 | 外部接口按幂等键去重 |
| 延迟/一次性任务 | `EnqueueIn` / `EnqueueAt` | 时区统一 UTC；错过触发窗口要有补偿扫描；周期调度用 cron 而不是反复入队 |
| outbox 中继 | 扫描未发布行 → 发布 → 标记 | 与业务写入同库，保证不丢 |

硬约束：

- **处理器必须幂等**：asynq 是至少一次，崩溃恢复与重试都会重复执行。用业务唯一键、
  状态机「已处理则跳过」或数据库唯一索引兜底。
- **任务载荷只放标识，不放快照**；worker 执行时重新读取最新状态。
- **重试要有上限与归档**：`MaxRetry` + 指数退避；进入归档的任务必须可查询、可告警，
  不能静默堆积。
- **按延迟等级分队列**（如 `critical` / `default` / `low`），不要让一个慢任务堵住实时补发。
- **asynq 所在 Redis 实例必须 `maxmemory-policy noeviction`**；逐出策略会直接丢掉任务。
  如果缓存也在这个实例上，缓存通常需要 `allkeys-lru`——两者冲突，应使用不同实例
  （逻辑库不能隔离逐出策略）。这是部署时最容易被忽略的一条。
- **不要把 asynq 当实时通道**：任务从入队到被执行没有延迟上限承诺。

### 5.4 组合：outbox（严格不丢）

当「数据库已提交但通知没发出」不可接受时，使用 outbox：

```text
事务内：写业务行 + 写 outbox 行（事件 ID、主题、载荷、状态）
提交后：asynq 入队 outbox ID（失败也不影响提交）
worker：读取 outbox 行 → Pub/Sub 发布 → 标记已发布
兜底：定时扫描 createdAt 超时且未发布的行，重新入队
消费端：按事件 ID 幂等
```

当前 yd 的消息中心是「提交后直接发布 + 失败入 asynq」，**没有 outbox**：进程在提交与发布
之间崩溃时，这次通知会丢（数据仍在库里，页面刷新可见）。需要严格语义的业务应自行加
outbox 表；这是业务选择，不是框架默认。

## 6. 反模式

| 反模式 | 后果 | 正确做法 |
|---|---|---|
| 用 asynq 广播给所有节点 | 任务只被一个节点消费；为每节点建任务则扇出成本线性增长 | Pub/Sub；可靠性不足时 outbox + 每节点补拉 |
| 用 Pub/Sub 承载不能丢的工作 | 断线/慢订阅者/重启即丢，且无告警 | 落库 + asynq |
| 轮询 Redis list 当队列 | 无租约恢复、无重试、无归档；崩溃丢任务 | asynq |
| 用 `KEYS` 找在线用户/房间 | 阻塞 Redis，规模上去直接拖垮实例 | 维护索引集合或哈希，按前缀分片 |
| 把未读数只存 Redis | 逐出/重启后计数错乱 | 数据库为准，Redis 仅加速 |
| 在数据库事务提交前发布事件 | 事务回滚后通知已发出（幽灵事件） | 提交后发布，或 outbox |
| 用 Pub/Sub 传大文件 | 扇出放大带宽，慢订阅者被断开 | 写对象存储/Redis 键，只发引用 |
| 分布式锁保护唯一性 | 锁过期、主从切换导致双写 | 数据库唯一索引/版本号兜底 |
| 每个请求创建一个 asynq 任务做轻量副作用 | 队列膨胀、延迟上升 | 合并/批量，或同步执行 |

## 7. 当前实现的可靠性边界

必须明确告诉使用方，避免把「尽力而为」当成「保证送达」：

- **消息中心**：数据库写入成功后调用 `Hub.Publish`；本地投递同步完成，跨节点广播失败时
  入 asynq。若进程在「提交」与「发布」之间崩溃，或 asynq 入队也失败，这次实时通知会丢；
  数据本身不丢，重连/刷新后可见。广播重试可能造成重复通知，页面按事件 ID/片段幂等处理。
- **通用 Hub**：未配置 `RetryQueue` 时，广播失败只返回错误/计数，不自动补发；配置后
  语义与消息中心一致（至少一次、可能重复）。
- **asynq 与 sentinel / cluster**：生成项目的消息中心共享 Pub/Sub 总线使用唯一的
  `goredis.UniversalClient`，支持单机、哨兵与集群；但 asynq 的持久重试当前通过
  `asynqio.RedisClientOpt` 连接单机地址，`redis.mode` 为 `sentinel` 或 `cluster` 时会打印告警并停用补发。
  需要多机补发时，应先在 `ydx/asynq` 增加 `NewClientFromRedisClient` / `NewServerFromRedisClient`
  支持共享客户端，再打开该路径。
- **缓存**：失效事件丢失由 L1 租约兜底，因此是「最终一致」，不是「立即一致」。
- **直接 Redis 状态**：全部按可重建设计；任何键都不应成为唯一事实源。
- **SSE**：`Last-Event-ID` 只携带 ID，补拉由业务端点完成；框架不保存历史。

## 8. 配置与运维

| 配置 | 作用 | 建议 |
|---|---|---|
| `realtime.node_id` | 多副本唯一标识 | 留空回退 `node.id` 与持久身份文件；滚动发布也不要复用 |
| `realtime.channel_prefix` | Pub/Sub 命名空间 | 每个环境唯一 |
| `realtime.queue_size` / `slow_policy` | 订阅端背压 | 实时页面用 `drop`；状态同步类用 `close` 触发重连补拉 |
| `realtime.max_message_bytes` | 单条上限 | 默认 1MiB；大对象改发引用 |
| `asynq.queues` / `asynq.concurrency` | 队列权重与并发 | 按延迟等级拆分，实时补发放高优先级 |
| `asynq.retry_base` / `asynq.retry_max` | 指数退避 | 外部服务用分钟级上限；内部补发可更短 |
| `asynq.strict_priority` | 严格优先级 | 有实时补发队列时开启 |
| `asynq.shutdown_timeout` | 关停预算 | 小于整体优雅退出预算 |
| `cache.lease` | L1 租约 | 权衡一致性与回源压力 |
| `message.node_id` | 消息中心兼容别名 | 留空使用 `node.id` 与持久身份文件 |

运维要点：

- **Redis 实例分工**：asynq 用 `noeviction` 实例；缓存用可逐出实例；Pub/Sub 可与任一共享，
  但要关注 `client-output-buffer-limit pubsub` 与带宽。
- **持久化**：asynq 所在实例开启 AOF（`appendfsync everysec`）；Pub/Sub 不需要持久化，
  但同实例的队列需要。
- **监控**：asynq 队列深度/延迟/归档数（`Inspector`）、Pub/Sub 断连次数、订阅端丢弃计数
  （`realtime.Stats().Dropped` / `ClosedSlow` / `Retried`）、Redis 内存与逐出数。
- **容量**：Pub/Sub 的扇出成本按「订阅节点数 × 消息大小」计算；频道数量本身不是问题，
  但每个节点对每个主题一条订阅连接/回调，主题极多时应合并主题或改用分片。

## 9. 代码示例

### 9.1 通用 Hub + asynq 补发

示例中的 `asynq` 指 `github.com/shibingli/ydx/asynq`，`json` 指 `github.com/shibingli/ydx/json`。

```go
// 任务类型与 worker：消息只携带标识与原节点，重发时不再本地投递。
const broadcastTask = "yd:realtime:broadcast"

type retryQueue struct{ cli *asynq.Client }

func (q *retryQueue) Enqueue(ctx context.Context, msg realtime.Message) error {
	// Hub 传入的 ctx 已与请求取消解绑；实现仍应施加自己的短超时。
	enqueueCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := q.cli.Enqueue(enqueueCtx, broadcastTask, msg)
	return err
}

registry.Register(broadcastTask, func(ctx context.Context, payload asynq.Payload) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var msg realtime.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return err
	}
	return hub.PublishRemote(ctx, msg) // 只发后端，不重复本地扇出
})

hub := realtime.NewHub(realtime.Options{
	NodeID: cfg.GetString("node.id"),
	Retry:  &retryQueue{cli: client},
})
hub.Use(realtime.NewRedisBackend(rdb, realtime.RedisOptionsFromConfig(cfg)))
```

### 9.2 幂等消费

```go
func handle(ctx context.Context, rdb *redis.Client, eventID string) error {
	ok, err := rdb.SetNX(ctx, "yd:idem:notify:"+eventID, "1", 24*time.Hour).Result()
	if err != nil {
		return err
	}
	if !ok {
		return nil // 已处理；至少一次语义下这是正常路径
	}
	return deliver(ctx, eventID)
}
```

### 9.3 在线状态

```go
// 心跳续期；连接关闭时删除自己的条目。读取方按需清理过期连接，不要用 KEYS。
key := "yd:presence:" + tenantID + ":" + userID
rdb.HSet(ctx, key, connID, time.Now().Unix())
rdb.Expire(ctx, key, 90*time.Second)
```

## 10. 检查清单

- [ ] 这个通道丢了消息，业务能否发现并补上？不能 → 换 asynq 或 outbox。
- [ ] asynq 任务处理器是否幂等？重复执行会不会重复扣款/重复发信？
- [ ] 是否有人把 asynq 当广播用？每个节点都要收到时是否改成了 Pub/Sub？
- [ ] Pub/Sub 订阅端是否有有界队列、慢消费者策略、重连后的补拉？
- [ ] 每个 Redis 键是否有 TTL？是否有重建路径？
- [ ] 多键 Lua 是否在同一个 hash tag 下？
- [ ] asynq 实例是否为 `noeviction`？缓存实例是否与它分开？
- [ ] 是否监控队列积压、归档任务、丢弃计数与订阅断连？
- [ ] 事件是否在数据库事务提交后才发布？
