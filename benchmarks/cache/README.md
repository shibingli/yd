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
11. **锁缓存行隔离**：热只读字段 `mask` 与 `sync.RWMutex` 之间保留 64 B 填充，避免锁原子状态更新反复使读者频繁读取的缓存行失效；并行 View/GetAppend 聚焦轮次约 36 ns。

效果（最终复核，轮转三遍中位数）：1 KiB 零拷贝 View **89.8 ns**、Get **439.4 ns**、GetAppend **198.3 ns**；64 B View **103.2 ns**、Get **131.9 ns**、GetAppend **119.9 ns**；并行 Get/GetAppend/View **45.6/37.4/35.4 ns**；64 B 命中率 **73.10%**，独立 1 KiB 轨迹 **72.56%–72.58%**。宿主机采集时有 30%–50% 背景负载，绝对值高于空闲状态；请以同一张表内的相对关系为准。

> 采集口径：每个实现独立进程，11 个读实现 + 8 个写实现 × 3 遍**轮转**（每遍每个实现跑一次），`-benchtime=200ms`，取三遍中位数。GetAppend 并行基准为每个协程使用独立缓冲区，避免共享缓冲区造成的数据竞争。

### Get 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 131.9 | 485.4 | 64 | 1 |
| yd-local-getappend | 119.9 | 533.6 | 0 | 0 |
| yd-local-view | 103.2 | 620.2 | 0 | 0 |
| freecache-get | 159.5 | 401.2 | 64 | 1 |
| freecache-getfn | 161.2 | 396.9 | 0 | 0 |
| ristretto | 138.1 | 463.3 | 7 | 0 |
| theine | 233.1 | 274.6 | 16 | 1 |
| bigcache | 156.4 | 409.2 | 80 | 2 |
| go-cache | 43.1 | 1483.3 | 0 | 0 |
| ttlcache | 111.8 | 572.7 | 0 | 0 |
| golang-lru | 60.1 | 1064.6 | 0 | 0 |

### Get 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 439.4 | 2330.6 | 1024 | 1 |
| yd-local-getappend | 198.3 | 5164.4 | 0 | 0 |
| yd-local-view | 89.8 | 11408.2 | 0 | 0 |
| freecache-get | 460.8 | 2222.2 | 1024 | 1 |
| freecache-getfn | 184.4 | 5552.3 | 0 | 0 |
| ristretto | 137.1 | 7469.2 | 7 | 0 |
| theine | 232.7 | 4401.2 | 16 | 1 |
| bigcache | 452.3 | 2264.2 | 1040 | 2 |
| go-cache | 44.2 | 23179.5 | 0 | 0 |
| ttlcache | 112.4 | 9111.4 | 0 | 0 |
| golang-lru | 61.1 | 16768.5 | 0 | 0 |

### Get 并行（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 45.6 | 1404.3 | 64 | 1 |
| yd-local-getappend | 37.4 | 1710.3 | 0 | 0 |
| yd-local-view | 35.4 | 1808.9 | 0 | 0 |
| freecache-get | 51.2 | 1249.9 | 64 | 1 |
| freecache-getfn | 42.5 | 1505.0 | 0 | 0 |
| ristretto | 35.7 | 1790.8 | 1 | 0 |
| theine | 30.8 | 2077.8 | 0 | 0 |
| bigcache | 47.5 | 1348.7 | 80 | 2 |
| go-cache | 49.7 | 1287.0 | 0 | 0 |
| ttlcache | 225.3 | 284.0 | 0 | 0 |
| golang-lru | 164.3 | 389.5 | 0 | 0 |

### Mixed 90/10（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 127.6 | — | 57 | 0 |
| yd-local-getappend | 117.1 | — | 0 | 0 |
| yd-local-view | 108.8 | — | 0 | 0 |
| freecache-get | 161.3 | — | 57 | 0 |
| freecache-getfn | 122.5 | — | 0 | 0 |
| ristretto | 230.1 | — | 17 | 0 |
| theine | 312.0 | — | 14 | 0 |
| bigcache | 177.4 | — | 94 | 1 |
| go-cache | 49.5 | — | 2 | 0 |
| ttlcache | 126.0 | — | 0 | 0 |
| golang-lru | 78.8 | — | 0 | 0 |

### Get TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 139.8 | 457.8 | 64 | 1 |
| yd-local-getappend | 117.1 | 546.5 | 0 | 0 |
| yd-local-view | 110.0 | 581.8 | 0 | 0 |
| freecache-get | 172.4 | 371.3 | 64 | 1 |
| freecache-getfn | 120.5 | 531.2 | 0 | 0 |
| ristretto | 147.7 | 433.4 | 7 | 0 |
| theine | 227.7 | 281.1 | 16 | 1 |
| go-cache | 56.4 | 1135.8 | 0 | 0 |
| ttlcache | 294.3 | 217.5 | 0 | 0 |

### Set 64 B（同值重复写）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 110.9 | 577.3 | 0 | 0 |
| freecache | 113.8 | 562.5 | 0 | 0 |
| ristretto | 664.9 | 96.3 | 99 | 1 |
| theine | 363.8 | 175.9 | 0 | 0 |
| bigcache | 298.6 | 214.3 | 318 | 0 |
| go-cache | 88.6 | 722.6 | 24 | 1 |
| ttlcache | 169.3 | 378.0 | 0 | 0 |
| golang-lru | 66.3 | 964.8 | 0 | 0 |

### Set 1 KiB（同值重复写）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 166.8 | 6138.0 | 0 | 0 |
| freecache | 220.5 | 4643.1 | 0 | 0 |
| ristretto | 719.5 | 1423.2 | 99 | 1 |
| theine | 355.4 | 2881.2 | 0 | 0 |
| bigcache | 1052.0 | 973.4 | 2801 | 0 |
| go-cache | 89.3 | 11462.0 | 24 | 1 |
| ttlcache | 180.7 | 5666.0 | 0 | 0 |
| golang-lru | 67.1 | 15270.7 | 0 | 0 |

### Set 变化值 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 112.0 | 571.4 | 0 | 0 |
| freecache | 128.9 | 496.4 | 0 | 0 |
| ristretto | 725.1 | 88.3 | 99 | 1 |
| theine | 359.2 | 178.2 | 0 | 0 |
| bigcache | 274.6 | 233.1 | 318 | 0 |
| go-cache | 89.8 | 712.8 | 24 | 1 |
| ttlcache | 176.1 | 363.3 | 0 | 0 |
| golang-lru | 77.5 | 826.0 | 0 | 0 |

### Set 变化值 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 182.9 | 5597.4 | 0 | 0 |
| freecache | 222.4 | 4604.6 | 0 | 0 |
| ristretto | 740.1 | 1383.5 | 99 | 1 |
| theine | 375.8 | 2725.0 | 0 | 0 |
| bigcache | 787.0 | 1301.1 | 3208 | 0 |
| go-cache | 89.7 | 11418.2 | 24 | 1 |
| ttlcache | 178.4 | 5739.3 | 0 | 0 |
| golang-lru | 73.8 | 13876.9 | 0 | 0 |

### Set TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 135.1 | — | 0 | 0 |
| freecache | 128.0 | — | 0 | 0 |
| ristretto | 873.1 | — | 99 | 1 |
| theine | 413.1 | — | 0 | 0 |
| go-cache | 102.8 | — | 24 | 1 |
| ttlcache | 304.4 | — | 0 | 0 |

### 命中率（5,000 容量 / 50,000 键 / 200,000 次访问）

| 实现 | 命中率 |
|---|---:|
| yd-local-get | 73.10% |
| yd-local-getappend | 73.09% |
| yd-local-view | 73.08% |
| freecache-get | 71.32% |
| ristretto | 72.60% |
| theine | 72.61% |
| bigcache | 80.02% |
| ttlcache | 72.61% |
| golang-lru | 72.61% |

### 1 KiB 命中率（独立轨迹）

| 模式 | 命中率 |
|---|---:|
| yd-local-get | 72.57% |
| yd-local-getappend | 72.56% |
| yd-local-view | 72.58% |

\* BigCache 使用 2 MB 内存上限，有效条目容量大于 5,000，其命中率不能与按条目数限制的实现直接横比；按轨迹计算的理论上限约 `70% + 30% × 4500/50000 ≈ 72.7%`。

## 4. 结论 | Takeaways

- **命中率**：64 B 轨迹下 `yd-local-get` / `yd-local-getappend` / `yd-local-view` 为 **73.10% / 73.09% / 73.08%**，高于 Ristretto/Theine/ttlcache/LRU 的 72.60%–72.61%；独立 1 KiB 轨迹（`TestHitRatioLargeValue`）为 **72.56%–72.58%**，同样处于该容量轨迹的理论上限附近。BigCache 的 80.02% 来自 2 MB 上限下更大的有效条目容量，不可直接横比。
- **1 KiB 零拷贝读**：`yd-local-view` **89.8 ns**，快于 FreeCache `GetFn`（184.4）、Ristretto（137.1）、ttlcache（112.4）与 Theine（232.7）。大值侧车让 ≤16 B 的键在 48 B 侧车记录内完成精确比较，不再读取 1 KiB 间隔的环形头部。
- **1 KiB 复制/追加读**：Get **439.4 ns** 快于 FreeCache `Get`（460.8）与 BigCache（452.3）；GetAppend **198.3 ns** 慢于 FreeCache `GetFn`（184.4，不拷贝），但快于 FreeCache 的复制 `Get`。Ristretto（137.1）与 ttlcache（112.4）返回缓存内引用，不参与防御性拷贝比较。
- **64 B 读**：View **103.2 ns**、Get **131.9 ns**、GetAppend **119.9 ns**。View 快于 FreeCache `GetFn`（161.2）、Ristretto（138.1）与 ttlcache（111.8）；Get 快于 FreeCache（159.5）与 Ristretto，稍慢于 ttlcache（111.8）；GetAppend 快于 FreeCache/Ristretto，与 ttlcache 接近。
- **并行读**：Get **45.6 ns**、GetAppend **37.4 ns**、View **35.4 ns**，均快于 FreeCache（51.2/42.5/42.5）与 go-cache（49.7）；View 略快于 Ristretto（35.7），Theine（30.8）更快。
- **Mixed 90/10**：Get **127.6 ns**、GetAppend **117.1 ns**、View **108.8 ns**，均快于 FreeCache（161.3/122.5/122.5）与 ttlcache（126.0）。
- **写入**：同值重复写 64 B/1 KiB 为 **110.9/166.8 ns**，变化值写为 **112.0/182.9 ns**，均快于 FreeCache（113.8/220.5 与 128.9/222.4）。go-cache（88.6/89.3）与 golang-lru（66.3/67.1）仍更快：它们保存切片引用而不做防御性拷贝，golang-lru 也没有 TTL/容量淘汰策略。`SetTTL` 135.1 ns 慢于 FreeCache（128.0）与 go-cache（102.8）。
- **TTL 读**：Get **139.8 ns**、GetAppend **117.1 ns**、View **110.0 ns**，均快于 FreeCache（172.4/120.5/120.5）与 Ristretto（147.7）。
- **GC**：环形缓冲区仍是预分配、指针自由；大值侧车为定长指针自由结构，不引入每条目 Go 对象；Set/`GetAppend`/`View` 均为 0 alloc，`Get` 仅返回副本分配。
- **语义边界（必须明确）**：本表不支持「所有指标都大于所有对比库」的结论。go-cache/golang-lru 无容量上限或 TTL，ttlcache/Ristretto/Theine 返回缓存内引用，BigCache 在命中率轨迹中有效容量更大；要在这几项也全面胜出，只能放弃防御性拷贝、TTL 或淘汰策略，或提供返回引用/需要调用方保证不长期持有的 API，这超出了本模块「安全、可淘汰、可带 TTL」的设计前提。与同语义的 FreeCache 相比，yd 已在命中率、64 B 全部读路径、1 KiB Get/View、并行读、Mixed、写入与 TTL 读上领先或持平。

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
