# 二级缓存 cache | Two-Level Cache cache

`ydx/cache` 是 yd 的二级缓存组件：一级缓存是进程内环形字节缓存，二级缓存是 Redis（可选）。它面向 Controller 与 Service 的读多写少场景，提供泛型 API、TTL、CRUD 事件订阅、零拷贝读取与 GORM 插件。

`ydx/cache` is the yd two-level cache: a process-local ring cache in front of an optional Redis tier. It targets read-heavy Controller/Service workloads and provides typed APIs, TTL, CRUD event subscriptions, zero-copy reads and a GORM plugin.

## 1. 能力一览 | Capabilities

| 能力 | 说明 |
|---|---|
| 二级缓存 | L1 本地 + L2 Redis；未配置 Redis 时自动退化为仅本地 |
| 一致性 | 写入先 L2 后 L1；版本号 + CAS 回填；Pub/Sub 失效广播；L1 租约上界 |
| 本地淘汰 | 分片环形缓冲区、W-TinyLFU 准入、CLOCK 访问位保护、惰性 TTL + 采样清扫 |
| GC 友好 | 条目存放在预分配 `[]byte` 区域，索引/草图/TTL 结构均为指针自由数组 |
| 零拷贝 | `Raw.View` / `Cache[T].View` 在读锁内把环形缓冲区中的字节直接交给回调 |
| 数据类型 | `JSON[T]` 覆盖结构体/切片/map/指针/接口；另有 String/Bytes/Int/Int64/Uint64/Float64/Bool/Time |
| 可扩展 | `Remote` 接口可替换 L2；`Codec[T]` 可自定义序列化；`Manager` 可被多个命名空间共享 |
| 事件 | `Subscribe` 订阅 Set/Delete/Clear/Invalidate/Resync，单调度协程、不阻塞热路径 |
| GORM | `gormcache.Plugin` 缓存 SELECT，按表代际失效，跨实例广播 |

## 2. 包与分层 | Packages

```
ydx/cache              Manager、Raw、Cache[T]、Codec、事件、Redis Remote、配置映射
ydx/cache/local        L1 环形缓存（可独立使用）
ydx/cache/gormcache    GORM 插件
```

- `Manager` 持有一个共享 L1、一个可选 `Remote` 与事件总线。一个进程通常只建一个。
- `Raw` 是一个字节命名空间；`Cache[T]` 是在 `Raw` 之上的泛型视图。
- 命名空间在同一个 `Manager` 内唯一；关闭后可重新打开。

## 3. 快速开始 | Quick Start

### 3.1 仅本地缓存 | Local-only

```go
mgr, err := cache.NewManager(
    cache.WithLocalOptions(local.Options{MaxCost: 128 << 20}),
    cache.WithLogger(log),
)
if err != nil { return err }
defer mgr.Close()

users, err := cache.New[User](mgr, "users", cache.WithTTL(5*time.Minute))
if err != nil { return err }

_ = users.Set(ctx, "u:1", User{ID: 1, Name: "ada"})
u, ok, err := users.Get(ctx, "u:1")
```

### 3.2 接入 Redis | With Redis

```go
cli, err := redis.Open(redis.OptionsFromConfig(cfg))
if err != nil { return err }
defer redis.Close(cli)

remote, err := cache.NewRedisRemote(cli, cache.RedisOptionsFromConfig(cfg))
if err != nil { return err }

mgr, err := cache.NewManager(
    cache.WithOptions(cache.OptionsFromConfig(cfg)), // 先应用整组配置（会重置 Remote）
    cache.WithRemote(remote),                         // 再挂载 L2
    cache.WithLogger(log),
)
if err != nil { return err }
defer mgr.Close()

a.Set(app.ServiceCache, mgr) // io.Closer 会被 app 自动关闭
```

`*cache.Manager` 实现 `Close() error`，因此 `a.Set(app.ServiceCache, mgr)` 会自动登记优雅退出钩子；`Remote.Close` 只停止订阅，不关闭调用方拥有的 Redis 客户端。

### 3.3 读透与负缓存 | Read-through and negative caching

```go
users, _ := cache.New[User](mgr, "users",
    cache.WithTTL(5*time.Minute),
    cache.WithNegativeTTL(30*time.Second),
    cache.WithJitter(15*time.Second),
)

u, err := users.GetOrLoad(ctx, "u:1", func(ctx context.Context) (User, error) {
    row, err := repo.FindUser(ctx, 1)
    if errors.Is(err, sql.ErrNoRows) {
        return User{}, cache.ErrNotFound // 写入负缓存，后续请求不再穿透
    }
    return row, err
})
```

同一进程内同一键的并发未命中由 singleflight 收敛；共享加载使用脱离首个调用方取消的受限 context。

### 3.4 零拷贝读取 | Zero-copy reads

```go
found, err := users.View(ctx, "u:1", func(u User) error {
    // u 已在读锁内从环形缓冲区直接解码；不要保留它引用的缓存字节。
    return render(u)
})
```

```go
found, err := raw.View(ctx, "key", func(data []byte) error {
    // data 只在回调期间有效：不得保留、修改或回调同一缓存。
    return json.Unmarshal(data, &v)
})
```

`View` 持有分片读锁；多个读者可并发，同分片写者会等待。`View` 不更新访问位与频率草图，因此纯 View 负载不参与淘汰决策；需要影响淘汰时用 `Get`/`GetAppend`（一次拷贝）。若回调耗时很长，改用 `Get`/`GetAppend` 释放锁。

### 3.5 严格读 | Strict reads

```go
u, ok, err := users.GetFresh(ctx, "u:1")  // 绕过 L1，直接读 L2
found, err := users.ViewFresh(ctx, "u:1", func(u User) error { return use(u) })
```

`GetFresh`/`ViewFresh` 跳过 L1，直接读取 L2，并在命中后刷新 L1；没有 L2 时回退到普通 L1 读取。它用于无法容忍租约窗口的调用方，但仍不是数据库直读：如果数据库写入绕过了缓存，L2 仍可能在 TTL 内陈旧。

### 3.6 事件订阅 | Events

```go
unsub, err := users.Subscribe(func(ev cache.Event) {
    switch ev.Op {
    case cache.OpSet, cache.OpDelete:
        log.Info("cache changed | 缓存变化", log.Strs("keys", ev.Keys))
    case cache.OpClear:
        log.Info("cache cleared | 缓存清理", log.Str("ns", ev.Namespace))
    }
})
defer unsub()
```

事件在单独的调度协程上按入队顺序投递；处理器 panic 会被恢复并记录。队列满时丢弃并计数（`Stats.EventsDropped`），绝不阻塞缓存读写。读透回填不发布事件（避免读风暴），只有显式 `Set`/`Delete`/`Clear` 与 GORM 失效会发布。

## 4. 一致性模型 | Consistency Model

### 4.1 写路径 | Write path

1. 写 L2（`Store`）并取得新版本号；
2. 把同一信封写入 L1，TTL 取 `min(valueTTL, lease)`；
3. 发布 `OpSet`/`OpDelete` 事件；
4. 其他节点的订阅者删除对应 L1 键；发布者自己的回声按 `Source` 丢弃。

写失败时 L2 是权威：`Set` 返回错误且不会只更新 L1；读失败按 `FailOpen` 选择降级或上报。

### 4.2 CAS 回填 | CAS write-back

- L2 信封头包含单调递增版本号；Loader 未命中时记录观察到的版本 `v0`。
- 回填使用 Lua 单键 CAS：当前版本仍为 `v0` 才写入，否则返回 `-1` 并重新读取 L2。
- 因此慢 loader 永远无法覆盖并发写入；`Stats.CASLost` 计数这类竞争。

### 4.3 租约与重连 | Lease and reconnect

- L1 条目最长存活 `lease`（默认 30s），即使值本身 TTL 更长。
- 丢失 Pub/Sub 消息时，陈旧窗口被租约限制；订阅中断后会重连，重连前清空 L1 并派发 `OpResync`。
- 订阅连接每 `health_interval` 做一次 `PING`；失败则重建订阅。

这给出的是**有界陈旧**而不是强一致：跨节点写入在下一次事件到达（通常毫秒级）或租约到期后可见。需要强一致的读请直接读数据库，或使用很短的租约并将 `FailOpen=false`。

### 4.4 失败策略 | Failure policy

| 场景 | `FailOpen=true`（默认） | `FailOpen=false` |
|---|---|---|
| L2 读失败 | 视为未命中，继续 loader | 返回 `ErrRemote` |
| L2 写失败（`Set`） | 返回错误 | 返回错误 |
| 回填失败（`GetOrLoad`） | 返回已加载值并告警 | 返回错误 |
| 事件发布失败 | 告警，依赖租约 | 返回错误 |

### 4.5 多机同步 | Multi-node synchronization

```text
Node A L1 ─┐
Node B L1 ─┼── 共享 L2（Redis）── 应用层 Pub/Sub 频道 <prefix>events
Node C L1 ─┘
```

多机同步走“共享 L2 + 应用层失效广播”，不做 L1 之间的点对点复制：

- **写**：写入 L2 并取得新版本 → 更新本机 L1 → 发布 `OpSet`/`OpDelete`（含完整逻辑键）。
  其他节点收到后删除对应 L1 条目；下一次读取从 L2 回填。
- **读透**：回填用 CAS（观察到的版本）；读回填不发布事件，避免读风暴。其他节点可能保留旧 L1，
  但最长只到 `lease`（默认 30s）。
- **Clear**：发布 `OpClear`；各节点按命名空间删除 L1。L2 条目不被扫描删除，靠 TTL/覆盖自然淘汰。
- **GORM**：写回调发布 `OpInvalidate{table, version}`；各节点至少推进一格且不低于事件版本，保证即使本地代际领先，旧查询键也会失效。订阅缺口后的
  `OpResync` 与全缓存 `OpClear` 会推进整体纪元，旧查询键立即不可达。
- **重连**：订阅因频道关闭或健康探针失败返回后，`Manager` 清空 L1、派发 `OpResync`，再以退避重订阅。

| 故障 | 行为 | 陈旧上界 |
|---|---|---|
| Pub/Sub 丢失一条事件 | L1 不失效，继续服务旧值 | `lease` |
| 订阅短暂断开 | 健康探针发现后重连；重连前清空 L1 | `max(lease, health_interval)` |
| Redis 读不可用 | `FailOpen=true` 时 L1 服务至 lease 到期，之后走 loader；`false` 时返回错误 | `lease` |
| Redis 写不可用 | `Set`/`Delete` 返回错误；`Set` 不更新 L1，`Delete` 仍删除本地 L1 | 事件发布失败时依赖 lease |
| Redis 故障切换/数据丢失 | 重连后清空 L1，从数据库重新加载；缓存不是事实来源 | 重连后归零 |
| 节点重启 | L1 为空；GORM 表代际从 0 开始，旧 L2 查询键由 TTL 兜底 | 插件 TTL（默认 30s） |

多机部署要求：

- **NodeID 必须唯一**：默认每个进程随机生成；若显式配置，必须保证同一部署内不重复，否则节点会把
  其他节点的失效事件误判为自己的回声而忽略。
- **Prefix/Channel 必须唯一**：多个部署共享同一个 Redis 时，`cache.prefix`（进而默认频道
  `<prefix>events`）或显式 `cache.redis.channel` 必须按部署隔离；Redis 的 Pub/Sub 频道不按 DB 隔离。
- **租约与探活一起调**：`lease` 决定丢事件时的陈旧上界，`health_interval` 决定断开后多久被发现；
  建议 `health_interval <= lease`。缩短 lease 会让 L1 更及时地回到 L2，但增加 Redis 读。
- **Redis Cluster**：应用层 `PUBLISH` 是集群级广播，一条订阅即可；不要改用 keyspace notifications，
  后者是节点本地事件，必须逐节点订阅。
- **时钟**：一致性不依赖各节点时钟；版本来自 Redis，事件时间只用于诊断。
- **监控**：关注 `Stats.Invalidations`、`Stats.EventsDropped`、`Stats.RemoteErrors`、`Stats.CASLost`。

已知限制与可选增强：

- **跨节点单飞**：singleflight 是进程内的，N 个节点可能各加载一次数据库；CAS 保证不会互相覆盖。
  若数据库压力不可接受，可增加 Redis 分布式填充锁（需额外一次 `SET NX`）。
- **Clear 是逻辑失效**：只清 L1；命名空间级 Clear 会扫描本机 L1，超大缓存上应避免频繁调用。
- **GORM 代际分叉/重启窗口**：表代际是每进程计数器；漏事件或新进程从 0 开始时，旧 L2 查询键
  最多存活一个插件 TTL。需要严格的多机代际时，应在 Redis 中维护全局表代际（`INCR`）并在启动时读取，
  或在启动后主动 `Invalidate`。
- **事件持久性**：Pub/Sub 是 fire-and-forget；若需要断线后重放失效事件，应改用 Redis Streams
  并维护消费位点，当前设计选择用租约 + 重连清空换取简单性。
- **读回填竞态**：节点从 L2 读到旧值后、写入 L1 前，另一节点可能已写入新值并广播失效；
  失效先到、回填后到，本机 L1 仍可能保留旧值。租约把该窗口限制在 `lease` 内；
  严格读请用 `GetFresh`/`ViewFresh`。要彻底消除需要带版本的本地失效水位/墓碑，代价是热路径多一次检查。

多机上线检查清单：

1. 每个节点 `NodeID` 唯一，`cache.prefix` / 频道按部署隔离。
2. `lease` 与 `health_interval` 已按“可接受陈旧度 vs Redis 读量”调过。
3. 集群模式下确认 `PUBLISH`/`SUBSCRIBE` 跨节点可达。
4. 演练一次 Redis 重启/故障切换：写失败、读降级、重连后 L1 清空并恢复。
5. 所有节点启用同一 `cache.gorm.namespace`，并确认写后其他节点的查询已失效。
6. 监控 `Invalidations`/`EventsDropped`/`RemoteErrors`/`CASLost`，异常时优先检查订阅与租约。

## 5. L1 设计 | L1 Design

### 5.1 环形缓冲区 | Ring buffer

每个分片是一段固定大小的 `[]byte`，条目按 `[头 32B][key][value]` 连续存放：

```
[expireAt int64][hash uint64][keyLen uint32][valLen uint32][flags uint32][metaID uint32]
```

- 写入头单调递增，物理偏移取模；空间不足时从尾部驱逐，必要时在环末写入“填充区间”。
- 同尺寸覆盖写原地更新；值也相同且不超过 256 B 时只刷新过期时间、不拷贝值（更大值直接覆盖，比较成本与拷贝相当）；尺寸变化时才追加新条目并把旧条目标记死亡，旧字节在环回绕时自然回收。
- 每次操作只计算一次 `maphash`：高 8 位选分片，低 32 位索引槽表。若两处都用低位，每个分片只能用到 1/N 的槽位，探测链会急剧变长。
- 索引是开放寻址槽表（`hash32 + token+2`，0 空、1 墓碑），键比较使用完整哈希与 SIMD 字节比较，杜绝 32 位哈希碰撞导致的错误命中。槽表初始大小按 `MaxEntrySize` 估算（至少 64 槽），负载达到 3/4 时翻倍并重哈希，上限为 `2 × entryLimit`；大值缓存因此不会为每分片分配一张几乎全空的百 KB 槽表。
- 大值（≥512 B）额外持有一条 48 B 的指针自由侧车记录（hash、expireAt、off、valStart、valLen、keyLen、16 B 键前缀），槽位令牌最高位标识侧车索引。查找 ≤16 B 的键时全部在侧车内完成，不再触碰按 1 KiB 间隔散布的环形头部；更长的键回退到环形中的完整字节比较。侧车仅服务大值，小值仍走原环形头部；TTL 同步写入侧车与头部，驱逐/删除时归还侧车索引。
- 频率估计由两张 4 行 4-bit Count-Min Sketch 组成：主草图在 Set 新增键时更新；读取（Get/GetAppend/View）按 1/8 采样访问草图，每个样本按 8 次命中加权，使估计值接近逐次更新（`TryLock`，争用时丢弃样本而不阻塞读者）；准入取两者较大值。达到 `10 × 宽度` 后整体减半（TinyLFU 老化）。
- 读取路径只持分片读锁：Get 复制、GetAppend 写入调用方缓冲区、View 直接暴露字节；释放读锁后才采样访问位与访问草图，因此读者不获取写锁。过期条目在读取时按未命中返回，由后台清扫回收。
- 访问位是分片内的无锁原子位图（每 64 字节一位），读取按 1/8 采样设置；新条目覆盖旧块时按块掩码清除（保留首块，以免清掉相邻存活记录的位）。共享同一块的多个小条目共享一个访问位，这是有意的近似。
- 新条目在受压时与最旧存活条目比较频率；最旧条目被访问过且候选严格更冷时才拒绝（同频允许进入，兼顾冷键轮换）。
- TTL 惰性判定：任何读取都不会返回过期条目；后台清扫器每 tick 每分片采样固定条目数，标记过期并释放会计。

### 5.2 GC 影响 | GC behavior

- 所有热数据都在预分配的大 `[]byte` 里；槽表、双草图、访问位图、大值侧车、分片计数都是 `[]uint32`/`[]uint64`/`atomic.Uint64`/定长结构体，**不含指针**。
- 一个持有数百 MB 的缓存只会让 GC 看到少量切片头与分片对象，而不是每个键值一个 Go 对象。
- `Get` 返回一份副本（一次分配）；`GetAppend` 写入调用方缓冲区；`View` 在读锁内直接暴露环形字节，零拷贝。三者都只在读锁内完成数据访问，策略采样在锁外进行，因此并发读者不会互相串行。
- 值以序列化字节保存，因此也避免了缓存持有大型对象图（例如带大量子对象的模型）。

### 5.3 容量推导 | Capacity defaults

`local.Options{}` 全零即可用：

- `MaxCost`：进程可见内存的 1/16，夹在 32 MiB..1 GiB；
- `Shards`：`GOMAXPROCS × 4` 向上取 2 的幂，夹在 1..256；预算太小时自动收缩；
- `MaxEntrySize`：分片大小的 1/8；超过上限的值不会进入 L1（仍可进入 L2）；
- `CleanupInterval`：1s；`SampleSize`：每分片 256 条。

### 5.4 关键优化 | Key optimizations

- **单次哈希 + 高位分片**：Get/Set/View/Delete 只算一次 `maphash`，高 8 位选分片、低位索引槽表；
  修掉了原先「分片与槽表都取低位」导致每片只能用 1/N 槽位的缺陷。
- **自适应槽表**：按 `MaxEntrySize` 估算初始槽位，负载 3/4 时翻倍重哈希，最大 `2 × entryLimit`。
  1 KiB 值的槽表由每分片约 128 KiB 降到约 8 KiB，减少随机槽位访问的缓存/TLB 压力。
- **大值侧车**：≥512 B 的条目在分片内保存 48 B 指针自由侧车记录；≤16 B 的键直接在侧车中精确比较，
  查找不再读取 1 KiB 间隔的环形头部。侧车只服务大值，64 B 等小值路径不增加内存；
  TTL 双写、驱逐/删除归还索引，命中判定与全键比较规则不变。1 KiB 的 View/Get/GetAppend
  在本轮轮转中位数为约 90/439/198 ns，其中 View 较侧车前的约 192 ns 下降约 53%。
- **命中路径单次解码**：`find` 直接返回已解码 `entry`，并预计算值的起始偏移；Get/View/Delete 不再二次读取头部。
- **惰性时钟**：只有条目带 TTL 时才调用 `time.Now`；无 TTL 的读路径省掉一次时钟读取。
- **同尺寸原地更新 + 幂等短路**：值相同且不超过 256 B 时只刷新过期时间，值不同原地覆盖；只有尺寸变化才删除+追加。
  不产生死版本、不改动槽位、不触发 `makeRoom`，也不让环形缓冲区被更新垃圾填满。
- **合并写入**：`keyLen+valLen` 与 `valLen+flags` 各用一次 64 位写完成，头部读写各减少一次内存操作。
- **读锁 + 锁外采样**：Get/GetAppend/View 统一使用分片读锁完成复制或视图，释放后再采样访问位与访问草图；
  读取路径不再获取写锁，16 线程并行读为约 45.6 ns（Get）、37.4 ns（GetAppend）与 35.4 ns（View）。
- **草图降耗**：TinyLFU 计数从四次 `mix64` 改为一次混合 + 四个旋转窗口；主草图在 Set 新增键时更新，
  读取按 1/8 采样访问草图并以 8 倍权重累计，`TryLock` 避免并行读者串行化。
- **无锁访问位**：每 64 字节一位的原子位图由读取按 1/8 采样设置，使读者无需写锁也能参与 CLOCK 保护。
- **分条命中计数**：每分片 8 个独占缓存行的计数器，按哈希低位分布，避免同一热键的并行读者争用一个原子字。
- **锁缓存行隔离**：热只读字段 `mask` 与 `sync.RWMutex` 之间保留 64 B 填充，避免锁的原子状态更新
  使 `mask` 所在缓存行在并行读者间反复失效。
- **准入平局**：最旧条目被访问过且候选频率**严格更低**时才拒绝；同频允许冷键进入。
  5k 容量/50k 键的冷热轨迹命中率因此从 70.5% 提升到约 73.1%，达到该容量下的上限。

## 6. Redis 远端 | Redis Remote

- 键 = `Prefix + namespace + ":" + key`；所有 L2 载荷都带 `YD` 信封头。
- `Store`/`StoreIf`/`Remove` 通过单键 Lua 完成“读旧版本 → 版本加一 → 写头+数据+PX”；单键脚本在 Redis Cluster 中不会触发 CROSSSLOT。
- 删除与负缓存写 `FlagTombstone`/`FlagNegative` 标记并保留版本，防止慢 loader 在删除后复活旧值。
- `Load`/`LoadMany` 用流水线同时取 `GET` 与 `PTTL`；坏载荷由上层删除并按未命中处理。
- 事件为 JSON，键按 64 个一块分片发布；订阅端会校验操作类型、键数量与长度。
- Redis 客户端仍由调用方拥有；`Remote.Close` 只关闭 Pub/Sub 订阅。

## 7. GORM 插件 | GORM Plugin

```go
mgr, _ := cache.NewManager(cache.WithRemote(remote), cache.WithOptions(cache.OptionsFromConfig(cfg)))
plugin, err := gormcache.New(gormcache.Options{
    Manager: mgr,
    Namespace: "gorm",
    TTL: 30 * time.Second,
    IgnoreTables: []string{"yd_msg_messages"},
})
if err != nil { return err }
if err := db.Use(plugin); err != nil { return err }
defer plugin.Close()
```

### 7.1 机制 | How it works

- 包装 `gorm:query`；在 `callbacks.BuildQuerySQL` 之后按
  `table + epoch + generation + destType + SQL + sha256(vars)` 生成跨节点稳定的键。
- 命中时直接把 JSON 解到 `Statement.Dest`，并设置 `RowsAffected`；`AfterQuery` 与模型 `AfterFind` 仍会执行。
- 未命中时执行原查询；成功且 `RowsAffected > 0` 时写入 `8 字节行数 + JSON`。
- 写回调注册在 `gorm:commit_or_rollback_transaction` 之后：Create 总是推进表代际，Update/Delete 在确实影响行时推进。
- 代际随事件广播；其他实例至少推进一格且不低于事件版本，旧查询键自然不可达（本地领先时也不会继续命中旧键）。
- 事务内查询不缓存（`Statement.ConnPool` 实现 `gorm.TxCommitter`），避免把未提交数据放进共享缓存。
- Raw SQL（`db.Raw`）、手工预构建 SQL、JOIN、`FOR UPDATE`、Preload、`*sql.Rows` 目标均跳过（JOIN 读取的表不在键的代际里，跳过可避免关联表写入后的陈旧结果）。
- 表过滤：`Tables` 白名单，`IgnoreTables` 黑名单；`gorm:query` 之外的回调不受影响。

### 7.2 显式事务 | Explicit transactions

GORM 没有“外事务提交后”钩子。写回调在显式事务中会在语句结束时推进代际（早于 `tx.Commit()`），这比完全不失效安全，但存在“提交前读者回填旧值”的窗口，由租约/TTL 兜底。需要严格语义时：

```go
err := db.Transaction(func(tx *gorm.DB) error { ... })
if err != nil { return err }
return plugin.Invalidate(ctx, "users") // 提交后手动失效
```

绕过 GORM 的写入同样使用 `Invalidate`。

### 7.3 配置 | Configuration

```yaml
cache:
  gorm:
    enabled: true
    namespace: gorm
    ttl: 30s
    tables: []
    ignore_tables: ["audit_logs"]
```

`gormcache.EnabledFromConfig(cfg)` 与 `gormcache.OptionsFromConfig(cfg, mgr)` 负责映射。

## 8. 配置 | Configuration

```yaml
cache:
  prefix: "yd:cache:"  # Redis key prefix | Redis 键前缀
  lease: 30s
  load_timeout: 30s
  event_buffer: 256
  fail_open: true
  tombstone_ttl: 1h
  max_cost: 0          # 0 = 自动推导
  shards: 0            # 0 = 自动推导
  max_entry_size: 0    # 0 = 自动推导
  cleanup_interval: 1s
  sample_size: 256
  redis:
    prefix: ""         # 空 = redis.key_prefix + cache.prefix
    channel: ""        # 空 = prefix + events
    tombstone_ttl: 0   # 0 = 回退 cache.tombstone_ttl
    subscribe_buffer: 64
    health_interval: 30s
```

环境变量沿用 `YD_` 前缀：`YD_CACHE_LEASE`、`YD_CACHE_REDIS_CHANNEL` 等。`cache.OptionsFromConfig` 与 `cache.RedisOptionsFromConfig` 读取这些键。

## 9. 性能与基准 | Performance

本地包内置基准：

```bash
(cd ../ydx/cache/local && go test -run '^$' -bench . -benchmem)
(cd ../ydx/cache && go test -run '^$' -bench . -benchmem)
```

参考结果（Windows 11 / i9-10980HK，Go 1.27.2，单进程；采集时宿主有背景负载，绝对值仅作量级参考）：

| 基准 | ns/op | 分配 |
|---|---|---|
| `BenchmarkGetAppend` | ~120 | 0 |
| `BenchmarkView` | ~103 | 0 |
| `BenchmarkGet`（返回副本） | ~132 | 1（返回值） |
| `BenchmarkSet`（同值重复写） | ~111 | 0（缓存本身；基准键格式化另计） |

`View`/`GetAppend` 是零分配读路径；`Get` 的分配仅来自返回值副本。`cache.Raw` 在 `namespace:key` 拼接与事件发布上还有少量短期分配，这是跨层 API 的代价，L1 核心保持零分配。两级命中（L1）不会触达 Redis；L2 访问是一次 `GET + PTTL` 流水线往返。与主流库的横向对比、命中率轨迹与优化记录见 [`benchmarks/cache/README.md`](../benchmarks/cache/README.md)。

## 10. 限制与检查清单 | Limits and Checklist

- **不是强一致**：跨节点可见性由 Pub/Sub + 租约决定；需要线性一致请直读数据库。
- **`Clear` 只清 L1**：L2 条目靠 TTL 或覆盖自然淘汰；需要清空 L2 请自行按前缀 `SCAN`/`UNLINK`。
- **`View` 回调**：不得保留、修改字节，不得回调同一缓存；长回调会阻塞同分片写者。
- **L1 只存能放进分片的值**：超大值会被 L1 拒绝但可进入 L2；`Stats.Local.Rejected` 可观测。
- **GORM 不跟踪绕过 GORM 的写入**：请调用 `Invalidate` 或依赖 TTL/租约。
- **非确定性 SQL**：`NOW()`、`RAND()` 等函数的结果会被冻结到 TTL；这类查询请用 `IgnoreTables` 排除或缩短 TTL。
- **显式事务**：写失效早于提交（见 7.2）。
- **新增命名空间**：在 `Manager` 内唯一，不得包含 `:` 或控制字符。
- **发布事件**：事件只含键名与命名空间，不含值；不要把敏感数据放进键名。
- **关闭顺序**：先 `plugin.Close()`/`Cache.Close()`，再 `Manager.Close()`；`Manager` 可重复关闭。
- **Redis 客户端归属**：`Remote.Close` 不关闭客户端；客户端由应用统一关闭。
- **守卫**：新代码同样受 `repotest` 的双语注释、哨兵错误、禁止 panic、禁止直接 JSON/Base64、无效外键等约束。
