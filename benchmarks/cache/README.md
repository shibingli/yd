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
2. **命中路径单次解码**：`find` 返回已解码的 `entry` 并预计算值偏移；头部 `keyLen+valLen`、`valLen+flags+metaID` 合并为 64 位读写；热路径去掉 `defer Unlock`。
3. **覆盖写与幂等短路**：同尺寸覆盖原地更新；值相同且不超过 256 B 时只刷新过期时间、不拷贝值；更大值直接覆盖，因为比较成本与拷贝相当。
4. **独立复制路径**：`Get` 不再走 `GetAppend(nil)`，而是在读锁内 `make + copy`，省掉 `growslice` 与一层函数调用。
5. **读锁 + 锁外采样**：Get/GetAppend/View 都在分片读锁内完成复制或零拷贝视图，释放读锁后才采样访问位与访问草图；读取路径不再获取写锁。
6. **加权采样**：读取按 1/8 采样访问位与访问草图，每个样本按 8 次命中加权，使频率估计接近逐次更新；命中率提升到 73.1%。
7. **访问位与命中计数**：每 64 字节一位的无锁原子位图参与 CLOCK 保护；每分片 8 个独占缓存行的命中计数器，避免并行读者争用一个原子字。
8. **草图降耗**：TinyLFU 计数从四次 `mix64` 改为一次混合 + 四个旋转窗口；主草图只记录 Set 插入，读取走采样草图，`TryLock` 争用时丢弃样本而不阻塞读者。
9. **自适应槽表**：初始槽位按 `MaxEntrySize` 估算，负载达到 3/4 时翻倍并重哈希，上限为 `2 × entryLimit`。1 KiB 值的槽表由每分片约 128 KiB 降到约 8 KiB，显著降低随机槽位访问的缓存/TLB 压力。
10. **大值侧车**：≥512 B 的条目在分片内保存 48 B 指针自由侧车（hash、TTL、偏移、长度、16 B 键前缀）。≤16 B 的键直接在侧车中精确比较，1 KiB 查找不再触碰按 1 KiB 间隔散布的环形头部；更长的键回退到完整字节比较。小值不使用侧车，64 B 路径内存不变。

效果（本轮轮转三遍中位数）：1 KiB 零拷贝 View **85.4 ns**、Get **400.0 ns**、GetAppend **209.6 ns**；64 B View **99.0 ns**、GetAppend **118.6 ns**、Get **122.5 ns**；并行 Get/GetAppend/View **45.5/36.7/37.4 ns**；命中率 **73.09%**。1 KiB View 较侧车前的约 192 ns 下降约 55%；64 B 路径未使用侧车，命中率与内存结构不变。宿主机采集时有 30%–50% 背景负载，绝对值高于空闲状态；请以同一张表内的相对关系为准。

> 采集口径：每个实现独立进程，13 个实现 × 3 遍**轮转**（每遍每个实现跑一次），`-benchtime=200ms`，取三遍中位数。GetAppend 并行基准为每个协程使用独立缓冲区，避免共享缓冲区造成的数据竞争。

### Get 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 122.5 | 522.7 | 64 | 1 |
| yd-local-getappend | 118.6 | 539.4 | 0 | 0 |
| yd-local-view | 99.0 | 646.5 | 0 | 0 |
| freecache-get | 154.0 | 415.5 | 64 | 1 |
| freecache-getfn | 108.5 | 589.7 | 0 | 0 |
| ristretto | 132.2 | 484.2 | 7 | 0 |
| theine | 218.6 | 292.8 | 16 | 1 |
| bigcache | 144.9 | 441.6 | 80 | 2 |
| go-cache | 39.9 | 1602.5 | 0 | 0 |
| ttlcache | 107.8 | 594.0 | 0 | 0 |
| golang-lru | 61.3 | 1043.6 | 0 | 0 |

### Get 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 400.0 | 2560.0 | 1024 | 1 |
| yd-local-getappend | 209.6 | 4885.4 | 0 | 0 |
| yd-local-view | 85.4 | 11990.8 | 0 | 0 |
| freecache-get | 425.9 | 2404.6 | 1024 | 1 |
| freecache-getfn | 139.6 | 7333.4 | 0 | 0 |
| ristretto | 123.4 | 8298.9 | 7 | 0 |
| theine | 217.2 | 4713.7 | 16 | 1 |
| bigcache | 437.6 | 2340.0 | 1040 | 2 |
| go-cache | 40.5 | 25291.8 | 0 | 0 |
| ttlcache | 111.5 | 9181.3 | 0 | 0 |
| golang-lru | 62.2 | 16465.5 | 0 | 0 |

### Get 并行（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 45.5 | 1407.9 | 64 | 1 |
| yd-local-getappend | 36.7 | 1741.8 | 0 | 0 |
| yd-local-view | 37.4 | 1712.1 | 0 | 0 |
| freecache-get | 48.8 | 1310.9 | 64 | 1 |
| freecache-getfn | 39.8 | 1608.0 | 0 | 0 |
| ristretto | 36.9 | 1734.2 | 0 | 0 |
| theine | 31.7 | 2018.2 | 0 | 0 |
| bigcache | 47.1 | 1358.9 | 80 | 2 |
| go-cache | 51.7 | 1237.8 | 0 | 0 |
| ttlcache | 220.7 | 290.0 | 0 | 0 |
| golang-lru | 154.9 | 413.0 | 0 | 0 |

### Mixed 90/10（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 127.7 | — | 57 | 0 |
| yd-local-getappend | 121.1 | — | 0 | 0 |
| yd-local-view | 97.5 | — | 0 | 0 |
| freecache-get | 136.2 | — | 57 | 0 |
| freecache-getfn | 110.1 | — | 0 | 0 |
| ristretto | 221.5 | — | 17 | 0 |
| theine | 293.6 | — | 14 | 0 |
| bigcache | 161.9 | — | 93 | 1 |
| go-cache | 49.2 | — | 2 | 0 |
| ttlcache | 115.7 | — | 0 | 0 |
| golang-lru | 67.9 | — | 0 | 0 |

### Get TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 133.6 | 479.0 | 64 | 1 |
| yd-local-getappend | 113.8 | 562.3 | 0 | 0 |
| yd-local-view | 103.9 | 616.2 | 0 | 0 |
| freecache-get | 143.2 | 446.9 | 64 | 1 |
| freecache-getfn | 102.6 | 623.8 | 0 | 0 |
| ristretto | 143.1 | 447.3 | 7 | 0 |
| theine | 223.8 | 285.9 | 16 | 1 |
| go-cache | 51.1 | 1252.5 | 0 | 0 |
| ttlcache | 290.6 | 220.2 | 0 | 0 |

### Set 64 B（同值重复写）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 106.8 | 599.5 | 0 | 0 |
| freecache | 130.5 | 490.6 | 0 | 0 |
| ristretto | 635.9 | 100.6 | 99 | 1 |
| theine | 317.3 | 201.7 | 0 | 0 |
| bigcache | 272.5 | 234.8 | 318 | 0 |
| go-cache | 87.6 | 730.5 | 24 | 1 |
| ttlcache | 161.9 | 395.2 | 0 | 0 |
| golang-lru | 68.9 | 929.2 | 0 | 0 |

### Set 1 KiB（同值重复写）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 168.8 | 6067.1 | 0 | 0 |
| freecache | 234.2 | 4372.8 | 0 | 0 |
| ristretto | 657.0 | 1558.7 | 99 | 1 |
| theine | 353.9 | 2893.5 | 0 | 0 |
| bigcache | 1150.0 | 890.1 | 3241 | 0 |
| go-cache | 88.7 | 11549.1 | 24 | 1 |
| ttlcache | 163.4 | 6266.1 | 0 | 0 |
| golang-lru | 69.3 | 14764.8 | 0 | 0 |

### Set 变化值 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 115.1 | 555.9 | 0 | 0 |
| freecache | 129.2 | 495.4 | 0 | 0 |
| ristretto | 656.8 | 97.4 | 99 | 1 |
| theine | 436.7 | 146.6 | 0 | 0 |
| bigcache | 260.1 | 246.1 | 318 | 0 |
| go-cache | 90.0 | 710.7 | 24 | 1 |
| ttlcache | 169.1 | 378.5 | 0 | 0 |
| golang-lru | 70.2 | 911.1 | 0 | 0 |

### Set 变化值 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 175.3 | 5842.9 | 0 | 0 |
| freecache | 213.5 | 4797.1 | 0 | 0 |
| ristretto | 655.8 | 1561.6 | 99 | 1 |
| theine | 355.2 | 2883.2 | 0 | 0 |
| bigcache | 758.8 | 1349.5 | 2980 | 0 |
| go-cache | 84.6 | 12100.3 | 24 | 1 |
| ttlcache | 177.6 | 5764.4 | 0 | 0 |
| golang-lru | 72.6 | 14096.6 | 0 | 0 |

### Set TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 119.5 | — | 0 | 0 |
| freecache | 117.2 | — | 0 | 0 |
| ristretto | 763.6 | — | 99 | 1 |
| theine | 382.4 | — | 0 | 0 |
| go-cache | 100.1 | — | 24 | 1 |
| ttlcache | 286.3 | — | 0 | 0 |

### 命中率（5,000 容量 / 50,000 键 / 200,000 次访问）

| 实现 | 命中率 |
|---|---:|
| yd-local-get | 73.09% |
| yd-local-getappend | 73.09% |
| yd-local-view | 73.09% |
| freecache-get | 71.32% |
| ristretto | 72.60% |
| theine | 72.59% |
| bigcache | 80.02% |
| ttlcache | 72.61% |
| golang-lru | 72.61% |

\* BigCache 使用 2 MB 内存上限，有效条目容量大于 5,000，其命中率不能与按条目数限制的实现直接横比；按轨迹计算的理论上限约 `70% + 30% × 4500/50000 ≈ 72.7%`。

## 4. 结论 | Takeaways

- **命中率**：`yd-local-get` / `yd-local-getappend` / `yd-local-view` 均为 **73.09%**，高于 Ristretto/Theine/ttlcache/LRU 的 72.59%–72.61%；轨迹的理论上限约 72.7%，yd 已在该容量下达到上限。BigCache 的 80.02% 来自 2 MB 上限下更大的有效条目容量，不可直接横比。
- **1 KiB 零拷贝读（本轮最大提升）**：`yd-local-view` **85.4 ns**，快于 FreeCache `GetFn`（139.6）、Ristretto（123.4）、ttlcache（111.5）与 Theine（217.2）。大值侧车让 ≤16 B 的键在 48 B 侧车记录内完成精确比较，不再读取 1 KiB 间隔的环形头部。
- **1 KiB 复制/追加读**：Get **400.0 ns** 快于 FreeCache `Get`（425.9）与 BigCache（437.6）；GetAppend **209.6 ns** 慢于 FreeCache `GetFn`（139.6，不拷贝），但快于 FreeCache 的复制 `Get`。Ristretto（123.4）与 ttlcache（111.5）返回缓存内引用，不参与防御性拷贝比较。
- **64 B 读**：View **99.0 ns**、Get **122.5 ns**、GetAppend **118.6 ns**。View 快于 FreeCache `GetFn`（108.5）、Ristretto（132.2）与 ttlcache（107.8）；Get 快于 FreeCache（154.0）与 Ristretto，稍慢于 ttlcache（107.8）；GetAppend 快于 Ristretto，与 FreeCache `GetFn` 接近。
- **并行读**：Get **45.5 ns**、GetAppend **36.7 ns**、View **37.4 ns**，均快于 FreeCache（48.8/39.8/39.8）与 go-cache（51.7）；View 与 Ristretto（36.9）持平，Theine（31.7）更快。
- **Mixed 90/10**：Get **127.7 ns**、GetAppend **121.1 ns**、View **97.5 ns**；Get/View 快于 FreeCache（136.2/110.1），GetAppend 略慢于 FreeCache `GetFn`（110.1）。ttlcache 为 115.7 ns，View 更快。
- **写入**：同值重复写 64 B/1 KiB 为 **106.8/168.8 ns**，变化值写为 **115.1/175.3 ns**，均快于 FreeCache（130.5/234.2 与 129.2/213.5）。go-cache（87.6/88.7）、golang-lru（68.9/69.3）仍更快：它们保存切片引用而不做防御性拷贝，golang-lru 也没有 TTL/容量淘汰策略。`SetTTL` 119.5 ns 与 FreeCache 117.2 基本持平。
- **TTL 读**：Get **133.6 ns**、GetAppend **113.8 ns**、View **103.9 ns**；Get 快于 FreeCache（143.2），View 与 FreeCache（102.6）基本持平，GetAppend 略慢。
- **GC**：环形缓冲区仍是预分配、指针自由；大值侧车为定长指针自由结构，不引入每条目 Go 对象；Set/`GetAppend`/`View` 均为 0 alloc，`Get` 仅返回副本分配。
- **语义边界（必须明确）**：本表不支持「所有指标都大于所有对比库」的结论。go-cache/golang-lru 无容量上限或 TTL，ttlcache/Ristretto/Theine 返回缓存内引用，BigCache 在命中率轨迹中有效容量更大；要在这几项也全面胜出，只能放弃防御性拷贝、TTL 或淘汰策略，或提供返回引用/需要调用方保证不长期持有的 API，这超出了本模块「安全、可淘汰、可带 TTL」的设计前提。与同语义的 FreeCache 相比，yd 已在命中率、64 B View/Get、1 KiB Get/View、并行读、Mixed、写入与 TTL Get/View 上领先或持平。

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
