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
9. **自适应槽表**：初始槽位按 `MaxEntrySize` 估算，负载达到 3/4 时翻倍并重哈希，上限为 `2 × entryLimit`。1 KiB 值的槽表由每分片约 128 KiB 降到约 8 KiB，显著降低随机槽位访问的缓存/TLB 压力。

效果（本轮轮转三遍中位数）：Get 并行 **47.5 ns**，GetAppend/View 并行 **37.9/36.5 ns**；64 B 零拷贝 View **111.3 ns**、GetAppend **114.5 ns**、Get **132.2 ns**；1 KiB View **192.2 ns**、GetAppend **202.4 ns**、Get **414.2 ns**；命中率 **73.10%**。1 KiB 三项较自适应槽表前分别由约 237/260/483 ns 降到 192/202/414 ns。宿主机采集时有 30%–50% 背景负载，绝对值高于空闲状态；请以同一张表内的相对关系为准。

> 采集口径：每个实现独立进程，13 个实现 × 3 遍**轮转**（每遍每个实现跑一次），`-benchtime=200ms`，取三遍中位数。GetAppend 并行基准为每个协程使用独立缓冲区，避免共享缓冲区造成的数据竞争。

### Get 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 132.2 | 484.0 | 64 | 1 |
| yd-local-getappend | 114.5 | 559.1 | 0 | 0 |
| yd-local-view | 111.3 | 574.9 | 0 | 0 |
| freecache-get | 165.1 | 387.6 | 64 | 1 |
| freecache-getfn | 149.5 | 428.0 | 0 | 0 |
| ristretto | 148.6 | 430.8 | 7 | 0 |
| theine | 232.0 | 275.8 | 16 | 1 |
| bigcache | 160.3 | 399.3 | 80 | 2 |
| go-cache | 44.8 | 1429.6 | 0 | 0 |
| ttlcache | 119.8 | 534.4 | 0 | 0 |
| golang-lru | 62.4 | 1025.0 | 0 | 0 |

### Get 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 414.2 | 2472.1 | 1024 | 1 |
| yd-local-getappend | 202.4 | 5059.9 | 0 | 0 |
| yd-local-view | 192.2 | 5327.5 | 0 | 0 |
| freecache-get | 448.0 | 2285.8 | 1024 | 1 |
| freecache-getfn | 194.2 | 5273.1 | 0 | 0 |
| ristretto | 144.8 | 7073.5 | 7 | 0 |
| theine | 225.7 | 4536.3 | 16 | 1 |
| bigcache | 449.3 | 2279.1 | 1040 | 2 |
| go-cache | 41.6 | 24606.3 | 0 | 0 |
| ttlcache | 121.5 | 8426.6 | 0 | 0 |
| golang-lru | 65.1 | 15733.1 | 0 | 0 |

### Get 并行（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 47.5 | 1348.8 | 64 | 1 |
| yd-local-getappend | 37.9 | 1689.5 | 0 | 0 |
| yd-local-view | 36.5 | 1751.5 | 0 | 0 |
| freecache-get | 51.2 | 1249.1 | 64 | 1 |
| freecache-getfn | 42.1 | 1520.4 | 0 | 0 |
| ristretto | 36.5 | 1751.1 | 0 | 0 |
| theine | 31.8 | 2010.6 | 0 | 0 |
| bigcache | 47.7 | 1340.6 | 80 | 2 |
| go-cache | 53.0 | 1207.6 | 0 | 0 |
| ttlcache | 215.2 | 297.5 | 0 | 0 |
| golang-lru | 165.6 | 386.6 | 0 | 0 |

### Mixed 90/10（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 128.9 | — | 57 | 0 |
| yd-local-getappend | 116.1 | — | 0 | 0 |
| yd-local-view | 114.6 | — | 0 | 0 |
| freecache-get | 186.2 | — | 57 | 0 |
| freecache-getfn | 123.9 | — | 0 | 0 |
| ristretto | 243.1 | — | 17 | 0 |
| theine | 326.3 | — | 14 | 0 |
| bigcache | 172.0 | — | 94 | 1 |
| go-cache | 51.1 | — | 2 | 0 |
| ttlcache | 119.5 | — | 0 | 0 |
| golang-lru | 63.3 | — | 0 | 0 |

### Get TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 142.4 | 449.4 | 64 | 1 |
| yd-local-getappend | 106.9 | 598.9 | 0 | 0 |
| yd-local-view | 123.3 | 519.0 | 0 | 0 |
| freecache-get | 154.7 | 413.8 | 64 | 1 |
| freecache-getfn | 148.4 | 431.4 | 0 | 0 |
| ristretto | 152.7 | 419.2 | 7 | 0 |
| theine | 234.7 | 272.7 | 16 | 1 |
| go-cache | 53.8 | 1188.9 | 0 | 0 |
| ttlcache | 325.8 | 196.4 | 0 | 0 |

### Set 64 B（同值重复写）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 113.1 | 565.7 | 0 | 0 |
| freecache | 119.2 | 537.0 | 0 | 0 |
| ristretto | 702.3 | 91.1 | 99 | 1 |
| theine | 354.7 | 180.4 | 0 | 0 |
| bigcache | 299.9 | 213.4 | 318 | 0 |
| go-cache | 88.5 | 723.1 | 24 | 1 |
| ttlcache | 171.6 | 373.0 | 0 | 0 |
| golang-lru | 67.9 | 942.8 | 0 | 0 |

### Set 1 KiB（同值重复写）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 174.6 | 5863.2 | 0 | 0 |
| freecache | 218.1 | 4694.4 | 0 | 0 |
| ristretto | 708.2 | 1446.0 | 99 | 1 |
| theine | 373.2 | 2744.1 | 0 | 0 |
| bigcache | 1055.0 | 970.7 | 2487 | 0 |
| go-cache | 94.0 | 10897.2 | 24 | 1 |
| ttlcache | 171.2 | 5982.2 | 0 | 0 |
| golang-lru | 69.7 | 14701.4 | 0 | 0 |

### Set 变化值 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 112.2 | 570.5 | 0 | 0 |
| freecache | 118.4 | 540.6 | 0 | 0 |
| ristretto | 731.9 | 87.5 | 99 | 1 |
| theine | 388.3 | 164.8 | 0 | 0 |
| bigcache | 283.6 | 225.6 | 318 | 0 |
| go-cache | 95.1 | 673.1 | 24 | 1 |
| ttlcache | 183.5 | 348.8 | 0 | 0 |
| golang-lru | 73.0 | 877.0 | 0 | 0 |

### Set 变化值 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 179.5 | 5706.3 | 0 | 0 |
| freecache | 212.9 | 4809.4 | 0 | 0 |
| ristretto | 750.8 | 1363.9 | 99 | 1 |
| theine | 365.4 | 2802.2 | 0 | 0 |
| bigcache | 705.1 | 1452.2 | 2396 | 0 |
| go-cache | 93.5 | 10949.9 | 24 | 1 |
| ttlcache | 173.2 | 5910.9 | 0 | 0 |
| golang-lru | 73.7 | 13888.3 | 0 | 0 |

### Set TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 131.4 | — | 0 | 0 |
| freecache | 114.6 | — | 0 | 0 |
| ristretto | 930.3 | — | 99 | 1 |
| theine | 428.9 | — | 0 | 0 |
| go-cache | 105.8 | — | 24 | 1 |
| ttlcache | 317.9 | — | 0 | 0 |

### 命中率（5,000 容量 / 50,000 键 / 200,000 次访问）

| 实现 | 命中率 |
|---|---:|
| yd-local-get | 73.10% |
| yd-local-getappend | 73.10% |
| yd-local-view | 73.08% |
| freecache-get | 71.32% |
| ristretto | 72.60% |
| theine | 72.61% |
| bigcache | 80.02% |
| ttlcache | 72.61% |
| golang-lru | 72.61% |

\* BigCache 使用 2 MB 内存上限，有效条目容量大于 5,000，其命中率不能与按条目数限制的实现直接横比；按轨迹计算的理论上限约 `70% + 30% × 4500/50000 ≈ 72.7%`。

## 4. 结论 | Takeaways

- **命中率**：`yd-local-get` / `yd-local-getappend` / `yd-local-view` 分别为 **73.10% / 73.10% / 73.08%**，高于 Ristretto/Theine/ttlcache/LRU 的 72.60%–72.61%；轨迹的理论上限约 72.7%，yd 已在该容量下达到上限。BigCache 的 80.02% 来自 2 MB 上限下更大的有效条目容量，不可直接横比。
- **64 B 零拷贝读**：`yd-local-view` **111.3 ns**，优于 FreeCache `GetFn`（149.5）、Ristretto（148.6）与 ttlcache（119.8）；go-cache（44.8）与 golang-lru（62.4）更快，但它们返回缓存内引用、没有容量淘汰或 TTL，语义与本模块不同。
- **64 B 复制/追加读**：`yd-local-get` **132.2 ns**、`yd-local-getappend` **114.5 ns**，均快于 FreeCache（165.1/149.5）与 Ristretto（148.6）；GetAppend 也快于 ttlcache（119.8），Get 稍慢于 ttlcache。
- **1 KiB 读**：View **192.2 ns**、GetAppend **202.4 ns**、Get **414.2 ns**。View 略快于 FreeCache `GetFn`（194.2）；Get 快于 FreeCache `Get`（448.0）与 BigCache（449.3）；GetAppend 与 FreeCache `GetFn` 基本持平（后者不拷贝）。Ristretto（144.8）与 ttlcache（121.5）返回缓存内引用，仍更快。
- **并行读**：Get **47.5 ns**、GetAppend **37.9 ns**、View **36.5 ns**，均快于 FreeCache（51.2/42.1/42.1）与 go-cache（53.0）；View 与 Ristretto（36.5）持平，Theine（31.8）更快。读锁改造让 Get/GetAppend 从约 57/60 ns 降到 47/38 ns。
- **Mixed 90/10**：Get **128.9 ns**、GetAppend **116.1 ns**、View **114.6 ns**，均快于 FreeCache（186.2/123.9/123.9）；View 也快于 ttlcache（119.5），Get/GetAppend 与之接近或略慢。
- **写入**：同值重复写 64 B/1 KiB 为 **113.1/174.6 ns**，均快于 FreeCache（119.2/218.1）；变化值写为 **112.2/179.5 ns**，也快于 FreeCache（118.4/212.9）。go-cache（88.5/94.0）、golang-lru（67.9/69.7）、ttlcache（171.6/171.2）在部分档位更快：它们保存切片引用而不做防御性拷贝，golang-lru 也没有 TTL/容量淘汰策略。`SetTTL` 131.4 ns 慢于 FreeCache（114.6）与 go-cache（105.8）。
- **TTL 读**：Get **142.4 ns**、GetAppend **106.9 ns**、View **123.3 ns**，均快于 FreeCache（154.7/148.4/148.4）与 Ristretto（152.7）。
- **GC**：环形缓冲区仍是预分配、指针自由；Set/`GetAppend`/`View` 均为 0 alloc，`Get` 仅返回副本分配。原地覆盖、幂等短路与自适应槽表减少了死版本、无效内存与缓存压力。
- **语义边界（必须明确）**：本表不支持「所有指标都大于所有对比库」的结论。go-cache/golang-lru 无容量上限或 TTL，ttlcache/Ristretto/Theine 返回缓存内引用，BigCache 在命中率轨迹中有效容量更大；要在这几项也全面胜出，只能放弃防御性拷贝、TTL 或淘汰策略，或提供返回引用/需要调用方保证不长期持有的 API，这超出了本模块「安全、可淘汰、可带 TTL」的设计前提。与同语义的 FreeCache 相比，yd 已在命中率、64 B 全部读路径、1 KiB Get/View、并行读、Mixed、写入与 TTL 读上领先或持平；剩余差距主要是返回引用型缓存的零拷贝语义。

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
