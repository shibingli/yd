# 缓存性能对比 | Cache Benchmarks

本模块把 `data/cachex/local`（yd 一级缓存）与主流 Go 缓存库放在同一口径下对比：Ristretto、FreeCache、Theine、BigCache、go-cache、ttlcache 与 golang-lru。

This module compares the yd L1 cache (`data/cachex/local`) with mainstream Go caches under one methodology: Ristretto, FreeCache, Theine, BigCache, go-cache, ttlcache and golang-lru.

## 1. 对比实现 | Implementations

| 名称 | 版本 | 淘汰/容量模型 | 读语义 |
|---|---|---|---|
| `yd-local-get` | 本仓 | W-TinyLFU 准入 + CLOCK 访问位 + 分片环形缓冲区，按字节预算 | 复制返回 |
| `yd-local-getappend` | 本仓 | 同上 | 复制进调用方缓冲区 |
| `yd-local-view` | 本仓 | 同上 | 零拷贝回调（采样访问位与访问草图） |
| `freecache-get` | v1.2.7 | 分段环形缓冲区，按字节容量 | 复制返回 |
| `freecache-getfn` | v1.2.7 | 同上 | 零拷贝回调 |
| `ristretto` | v2.4.2 | W-TinyLFU（`IgnoreInternalCost: true`，按条目数计费） | 返回存储引用 |
| `theine` | v0.6.2 | W-TinyLFU + 分层时间轮 | 返回存储引用 |
| `bigcache` | v3.2.0 | 分片 ring + 全局 LifeWindow，按内存上限驱逐 | 复制返回（copy-on-read） |
| `go-cache` | v2.1.0 | 无容量上限，仅 TTL | 返回存储引用 |
| `ttlcache` | v3.4.1 | LRU + 容量上限 | 返回存储引用 |
| `golang-lru` | v2.0.7 | 经典 LRU，无 TTL | 返回存储引用 |

## 2. 基准口径 | Methodology

- 基础工作集 **20,000 个键**，容量 **40,000**（2× 工作集），因此 Get/Set 基准度量原始吞吐而非驱逐；值为 **64 B / 1 KiB** 两档。
- 键池 50,000 个，命中率轨迹使用 5,000 容量、50,000 键、70% 访问集中在前 500 个热键、30% 均匀冷键，共 200,000 次访问。
- **Set 为同步可见口径**：Ristretto/Theine 的 `Set` 是写缓冲，基准每 64 次 Set 调用一次 `Wait()`；yd/FreeCache/BigCache 的 `Set` 本身同步可见。
- **Set 的两种口径**：`BenchmarkSet` 每轮写入同一个值，yd 对「同尺寸且值相同、且不超过 256 B」的覆盖写做幂等短路（只刷新过期时间，不拷贝值），因此该表反映重复写入的上限；`BenchmarkSetChanging` 每次改变值，度量无法短路的真实覆盖写。两张表同时给出，避免只挑有利口径。
- **复制语义不同**：`yd-local-get`、`freecache-get` 在读取时复制；`yd-local-view`、`freecache-getfn` 提供零拷贝回调；Ristretto/Theine/go-cache/ttlcache/golang-lru 返回存储引用；BigCache 在公开 `Get` 中复制（`B/op` 可见）；FreeCache 的复制/零拷贝分别由 `get`/`getfn` 提供。表中 `B/op`/`allocs/op` 只统计调用路径，不统计缓存已持有的值。
- **TTL**：`Get TTL`/`Set TTL` 只包含支持逐条 TTL 的实现；BigCache 只有全局 `LifeWindow`，golang-lru 没有 TTL，因此不参与。
- **go-cache** 没有容量上限，不参与命中率轨迹；**BigCache** 在命中率轨迹中配置 2 MB 内存上限，其有效条目容量大于 5,000，因此其命中率不能与按条目数限制的实现直接横比。
- **命中率理论上限**：轨迹为 70% 访问集中在 500 个热键、30% 均匀落在 50,000 个冷键，容量 5,000。把 500 个热键全部缓存后只剩 4,500 个冷键位置，理论上限约为 `70% + 30% × 4500/50000 ≈ 72.7%`（实际轨迹抽样会在此上下浮动）；yd 的 View 采样策略约 73.0%，已在该容量下达到上限，而不是靠扩大容量刷高比例。
- Ristretto 的 `MaxCost` 默认会把内部存储开销计入成本；基准设置 `IgnoreInternalCost: true`，使 `MaxCost` 对应条目数，与其他实现的 5,000 容量可比。
- 每个实现由 `YD_BENCH_ONLY` 在**独立进程**中运行；采用**轮转三遍**（每遍每个实现跑一次）取中位数，避免某一实现恰好落在机器负载/降频窗口。

## 3. 结果与优化记录 | Results and Optimization Log

### 优化记录 | Optimization log

首轮基准暴露了 Set、1 KiB 零拷贝读与命中率三个短板。随后做了多轮结构性优化（均由 `data/cachex/local` 的单元测试与 `-race` 覆盖）：

1. **单次哈希 + 高位分片**：Get/Set/View/Delete 只计算一次 `maphash`，高 8 位选分片、低位索引槽表。旧实现两处都用低位，导致每个分片只能用到 1/N 的槽位，探测链被拉长。
2. **命中路径单次解码**：`find` 返回已解码的 `entry` 并预计算值偏移；头部 `keyLen+valLen`、`valLen+flags` 合并为 64 位读写；热路径去掉 `defer Unlock`。
3. **覆盖写与幂等短路**：同尺寸覆盖原地更新；值相同且不超过 256 B 时只刷新过期时间、不拷贝值；更大值直接覆盖，因为比较成本与拷贝相当。
4. **独立复制路径**：`Get` 不再走 `GetAppend(nil)`，而是在读锁内 `make + copy`，省掉 `growslice` 与一层函数调用。
5. **读锁 + 锁外采样**：Get/GetAppend/View 都在分片读锁内完成复制或零拷贝视图，释放读锁后才采样访问位与访问草图；读取路径不再获取写锁。
6. **加权采样**：读取按 1/8 采样访问位与访问草图，每个样本按 8 次命中加权，使频率估计接近逐次更新；命中率提升到 73.1%。
7. **访问位与命中计数**：每 64 字节一位的无锁原子位图参与 CLOCK 保护；每分片 8 个独占缓存行的命中计数器，避免并行读者争用一个原子字。
8. **草图降耗**：TinyLFU 计数从四次 `mix64` 改为一次混合 + 四个旋转窗口；主草图只记录 Set 插入，读取走采样草图，`TryLock` 争用时丢弃样本而不阻塞读者。

效果（本轮轮转三遍中位数）：Get 并行 **47.0 ns**（64 B）、GetAppend/View 并行 **37.0/37.1 ns**；64 B 零拷贝 View **101.3 ns**、GetAppend **119.3 ns**、Get **140.7 ns**；1 KiB View **236.5 ns**、GetAppend **260.3 ns**、Get **483.0 ns**；命中率 **73.10%**。宿主机采集时有 30%–50% 背景负载，绝对值高于空闲状态；请以同一张表内的相对关系为准。

> 采集口径：每个实现独立进程，13 个实现 × 3 遍**轮转**（每遍每个实现跑一次），`-benchtime=200ms`，取三遍中位数。GetAppend 并行基准为每个协程使用独立缓冲区，避免共享缓冲区造成的数据竞争。

### Get 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 140.7 | 454.7 | 64 | 1 |
| yd-local-getappend | 119.3 | 536.4 | 0 | 0 |
| yd-local-view | 101.3 | 631.6 | 0 | 0 |
| freecache-get | 185.2 | 345.6 | 64 | 1 |
| freecache-getfn | 142.4 | 449.4 | 0 | 0 |
| ristretto | 141.9 | 450.9 | 7 | 0 |
| theine | 227.2 | 281.6 | 16 | 1 |
| bigcache | 164.5 | 389.1 | 80 | 2 |
| go-cache | 45.9 | 1393.9 | 0 | 0 |
| ttlcache | 116.4 | 549.6 | 0 | 0 |
| golang-lru | 63.5 | 1007.5 | 0 | 0 |

### Get 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 483.0 | 2120.1 | 1024 | 1 |
| yd-local-getappend | 260.3 | 3934.3 | 0 | 0 |
| yd-local-view | 236.5 | 4329.8 | 0 | 0 |
| freecache-get | 438.3 | 2336.3 | 1024 | 1 |
| freecache-getfn | 187.1 | 5474.2 | 0 | 0 |
| ristretto | 138.2 | 7411.8 | 7 | 0 |
| theine | 231.4 | 4424.6 | 16 | 1 |
| bigcache | 446.5 | 2293.6 | 1040 | 2 |
| go-cache | 45.0 | 22765.8 | 0 | 0 |
| ttlcache | 119.7 | 8557.3 | 0 | 0 |
| golang-lru | 60.7 | 16876.2 | 0 | 0 |

### Get 并行（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 47.0 | 1362.7 | 64 | 1 |
| yd-local-getappend | 37.0 | 1730.0 | 0 | 0 |
| yd-local-view | 37.1 | 1724.2 | 0 | 0 |
| freecache-get | 49.6 | 1289.8 | 64 | 1 |
| freecache-getfn | 41.6 | 1538.4 | 0 | 0 |
| ristretto | 36.4 | 1760.7 | 0 | 0 |
| theine | 31.9 | 2005.6 | 0 | 0 |
| bigcache | 47.1 | 1358.5 | 80 | 2 |
| go-cache | 53.4 | 1199.7 | 0 | 0 |
| ttlcache | 236.0 | 271.2 | 0 | 0 |
| golang-lru | 160.5 | 398.7 | 0 | 0 |

### Mixed 90/10（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 150.4 | — | 57 | 0 |
| yd-local-getappend | 122.7 | — | 0 | 0 |
| yd-local-view | 121.8 | — | 0 | 0 |
| freecache-get | 163.2 | — | 57 | 0 |
| freecache-getfn | 125.0 | — | 0 | 0 |
| ristretto | 226.4 | — | 17 | 0 |
| theine | 307.7 | — | 14 | 0 |
| bigcache | 186.9 | — | 95 | 1 |
| go-cache | 48.5 | — | 2 | 0 |
| ttlcache | 124.2 | — | 0 | 0 |
| golang-lru | 67.2 | — | 0 | 0 |

### Get TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 165.5 | 386.7 | 64 | 1 |
| yd-local-getappend | 142.2 | 449.9 | 0 | 0 |
| yd-local-view | 120.0 | 533.3 | 0 | 0 |
| freecache-get | 172.5 | 371.0 | 64 | 1 |
| freecache-getfn | 135.0 | 474.1 | 0 | 0 |
| ristretto | 158.7 | 403.4 | 7 | 0 |
| theine | 220.7 | 290.0 | 16 | 1 |
| go-cache | 52.6 | 1217.6 | 0 | 0 |
| ttlcache | 349.4 | 183.2 | 0 | 0 |

### Set 64 B（同值重复写）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 123.9 | 516.4 | 0 | 0 |
| freecache | 136.5 | 468.9 | 0 | 0 |
| ristretto | 682.8 | 93.7 | 99 | 1 |
| theine | 366.1 | 174.8 | 0 | 0 |
| bigcache | 294.4 | 217.3 | 318 | 0 |
| go-cache | 94.2 | 679.8 | 24 | 1 |
| ttlcache | 171.5 | 373.1 | 0 | 0 |
| golang-lru | 71.9 | 889.6 | 0 | 0 |

### Set 1 KiB（同值重复写）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 231.3 | 4428.0 | 0 | 0 |
| freecache | 211.8 | 4835.0 | 0 | 0 |
| ristretto | 698.7 | 1465.5 | 99 | 1 |
| theine | 351.3 | 2915.2 | 0 | 0 |
| bigcache | 1059.0 | 967.2 | 2484 | 0 |
| go-cache | 89.4 | 11459.7 | 24 | 1 |
| ttlcache | 173.7 | 5894.0 | 0 | 0 |
| golang-lru | 67.2 | 15235.9 | 0 | 0 |

### Set 变化值 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 133.5 | 479.6 | 0 | 0 |
| freecache | 115.6 | 553.7 | 0 | 0 |
| ristretto | 681.7 | 93.9 | 99 | 1 |
| theine | 352.4 | 181.6 | 0 | 0 |
| bigcache | 284.2 | 225.2 | 318 | 0 |
| go-cache | 93.1 | 687.7 | 24 | 1 |
| ttlcache | 175.6 | 364.4 | 0 | 0 |
| golang-lru | 67.0 | 955.2 | 0 | 0 |

### Set 变化值 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 262.2 | 3904.7 | 0 | 0 |
| freecache | 207.6 | 4931.9 | 0 | 0 |
| ristretto | 775.0 | 1321.3 | 99 | 1 |
| theine | 385.8 | 2654.0 | 0 | 0 |
| bigcache | 723.0 | 1416.3 | 2508 | 0 |
| go-cache | 94.3 | 10859.4 | 24 | 1 |
| ttlcache | 179.9 | 5691.6 | 0 | 0 |
| golang-lru | 70.1 | 14603.5 | 0 | 0 |

### Set TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 133.1 | — | 0 | 0 |
| freecache | 129.7 | — | 0 | 0 |
| ristretto | 902.5 | — | 99 | 1 |
| theine | 405.4 | — | 0 | 0 |
| go-cache | 115.0 | — | 24 | 1 |
| ttlcache | 316.7 | — | 0 | 0 |

### 命中率（5,000 容量 / 50,000 键 / 200,000 次访问）

| 实现 | 命中率 |
|---|---:|
| yd-local-get | 73.10% |
| yd-local-getappend | 73.10% |
| yd-local-view | 73.09% |
| freecache-get | 71.32% |
| ristretto | 72.60% |
| theine | 72.61% |
| bigcache | 80.02% |
| ttlcache | 72.61% |
| golang-lru | 72.61% |

\* BigCache 使用 2 MB 内存上限，有效条目容量大于 5,000，其命中率不能与按条目数限制的实现直接横比；按轨迹计算的理论上限约 `70% + 30% × 4500/50000 ≈ 72.7%`。

## 4. 结论 | Takeaways

- **命中率**：`yd-local-get` / `yd-local-getappend` / `yd-local-view` 均为 **73.09%–73.10%**，高于 Ristretto/Theine/ttlcache/LRU 的 72.60%–72.61%；轨迹的理论上限约 72.7%，yd 已在该容量下达到上限。BigCache 的 80.02% 来自 2 MB 上限下更大的有效条目容量，不可直接横比。
- **64 B 零拷贝读**：`yd-local-view` **101.3 ns**，优于 FreeCache `GetFn`（142.4）、Ristretto（141.9）与 ttlcache（116.4）；go-cache（45.9）与 golang-lru（63.5）更快，但它们返回缓存内引用、没有容量淘汰或 TTL，语义与本模块的「锁内回调 + 容量/TTL + 淘汰策略」不同。
- **1 KiB 零拷贝读**：`yd-local-view` **236.5 ns**，慢于 FreeCache `GetFn`（187.1）、Ristretto（138.2）与 ttlcache（119.7）。这一档由 40 MB 随机访问的内存带宽主导，额外访问位/采样草图与回调开销被放大。
- **复制/追加读**：64 B `yd-local-get` **140.7 ns**、`yd-local-getappend` **119.3 ns**，均快于 FreeCache 的 185.2/142.4；1 KiB 分别为 **483.0/260.3 ns**，慢于 FreeCache 的 438.3/187.1。`Get` 的分配是「返回调用方自有副本」语义的固有成本；需要零分配时用 `GetAppend`/`View`。
- **并行读**：`yd-local-get` **47.0 ns**、`yd-local-getappend` **37.0 ns**、`yd-local-view` **37.1 ns**，均快于 FreeCache（49.6/41.6/41.6）与 go-cache（53.4）；View 与 Ristretto（36.4）基本持平，Theine（31.9）更快。读锁改造让 Get/GetAppend 从约 57/60 ns 降到 47/37 ns。
- **Mixed 90/10**：Get **150.4 ns**、GetAppend **122.7 ns**、View **121.8 ns**，均快于 FreeCache（163.2/125.0/125.0）；ttlcache 为 124.2 ns，与 View 接近。
- **写入**：同值重复写 64 B 为 **123.9 ns**（FreeCache 136.5），1 KiB 为 **231.3 ns**（FreeCache 211.8）；变化值写 64 B/1 KiB 为 **133.5/262.2 ns** vs FreeCache **115.6/207.6 ns**。go-cache（94.2/89.4）、golang-lru（71.9/67.2）、ttlcache（171.5/173.7）在部分写入档更快：它们保存切片引用而不做防御性拷贝，golang-lru 也没有 TTL/容量淘汰策略。
- **TTL**：`yd-local-get` **165.5 ns** 与 `yd-local-view` **120.0 ns** 快于 FreeCache（172.5/135.0）；`yd-local-getappend` 142.2 ns 慢于 FreeCache 135.0；`SetTTL` 133.1 ns 与 FreeCache 129.7 基本持平。
- **GC**：环形缓冲区仍是预分配、指针自由；Set/`GetAppend`/`View` 均为 0 alloc，`Get` 仅返回副本分配。原地覆盖与幂等短路减少了死版本、ring 回绕与 GC 压力。
- **语义边界（必须明确）**：本表不支持「所有指标都大于所有对比库」的结论。go-cache/golang-lru 无容量上限或 TTL，ttlcache/Ristretto/Theine 返回缓存内引用，BigCache 在命中率轨迹中有效容量更大；要在这几项也全面胜出，只能放弃防御性拷贝、TTL 或淘汰策略，或提供返回引用/需要调用方保证不长期持有的 API，这超出了本模块「安全、可淘汰、可带 TTL」的设计前提。与同语义的 FreeCache 相比，yd 在命中率、64 B 零拷贝读、64 B 复制/追加读、64 B 写、并行读、Mixed 与部分 TTL 读上胜出，在 1 KiB 复制/零拷贝读与部分写路径上仍有差距。

## 5. 复现 | Reproduce

```bash
cd benchmarks/cache

# 命中率轨迹（确定性冷热分布）
go test ./... -run TestHitRatio -v -count=1

# 每个实现独立进程；轮转三遍后取中位数（Ristretto/Theine 的 Wait 由适配器处理）
for round in 1 2 3; do
  for impl in yd-local-get yd-local-getappend yd-local-view freecache-get freecache-getfn ristretto theine bigcache go-cache ttlcache golang-lru; do
    YD_BENCH_ONLY=$impl go test -run '^$' -bench 'Benchmark(Get|GetParallel|Mixed|GetTTL)' -benchmem -benchtime=200ms -count=1
  done
  for impl in yd-local freecache ristretto theine bigcache go-cache ttlcache golang-lru; do
    YD_BENCH_ONLY=$impl go test -run '^$' -bench 'Benchmark(Set|SetTTL|SetChanging)' -benchmem -benchtime=200ms -count=1
  done
done
```

## 6. 测试平台 | Platform

- Windows 11 Pro for Workstations，Intel Core i9-10980HK（8 核 16 线程，`GOMAXPROCS=16`），Go 1.27.2 windows/amd64。
- 数值对机器负载、温度与电源策略敏感；本轮采集时宿主机有 30%–50% 背景负载（CPU 约 2.4 GHz），绝对值高于空闲状态。跨机比较请以同一张表内的相对关系为准，建议在目标 Linux 服务器空闲时用 `-count=5` 重新采集。
- 本模块是独立 Go module，第三方缓存库不会进入框架根 `go.mod`。
