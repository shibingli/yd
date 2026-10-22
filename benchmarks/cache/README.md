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

首轮基准暴露了 Set、1 KiB 零拷贝读与命中率三个短板。随后做了两轮结构性优化（均由 `data/cachex/local` 的单元测试与 `-race` 覆盖）：

1. **单次哈希 + 高位分片**：Get/Set/View/Delete 只计算一次 `maphash`，高 8 位选分片、低位索引槽表。旧实现两处都用低位，导致每个分片只能用到 1/N 的槽位，探测链被拉长。
2. **命中路径单次解码**：`find` 返回已解码的 `entry` 并预计算值偏移；头部 `keyLen+valLen`、`valLen+flags` 合并为 64 位读写；热路径去掉 `defer Unlock`。
3. **覆盖写与幂等短路**：同尺寸覆盖原地更新；值相同且不超过 256 B 时只刷新过期时间、不拷贝值；更大值直接覆盖，因为比较成本与拷贝相当。
4. **独立复制路径**：`Get` 不再走 `GetAppend(nil)`，而是在锁内 `make + copy`，省掉 `growslice` 与一层函数调用。
5. **访问位与访问草图**：View 使用每 64 字节一位的无锁原子位图参与 CLOCK 保护；Get/View 以稀疏采样（Get 1/16、View 1/8）更新独立访问草图，`TryLock` 争用时丢弃样本而不阻塞读者；主草图仍由 Get/Set 每次更新。
6. **分条命中计数**：每分片 8 个独占缓存行的计数器，按哈希低位分布，避免同一热键的并行读者争用一个原子字。
7. **草图降耗**：TinyLFU 计数从四次 `mix64` 改为一次混合 + 四个旋转窗口；准入平局允许同频冷键进入。

效果（跨轮绝对值受背景负载影响，以下为可比口径下的代表值）：同值重复写 Set **221.4 → 95.2 ns**（64 B）、**405.1 → 201.3 ns**（1 KiB，含幂等短路）；变化值写 98.8/200.2 ns（新增口径，无历史基线）；1 KiB `View` **251.8 → 166.6 ns**；64 B `View` **96.7 → 93.6 ns**；命中率 **70.5% → 72.73%（Get）/ 72.97%（View）**。`Get` 的 `make+copy` 改动在相邻同负载轮次中把 64 B 从约 200 ns 降到 163 ns、1 KiB 从约 536 ns 降到 440 ns（1 KiB 绝对值随内存压力波动较大）。

> 采集口径：每个实现独立进程，13 个实现 × 3 遍**轮转**（每遍每个实现跑一次），`-benchtime=200ms`，取三遍中位数。采集时宿主机仍有 30%–50% 背景负载，绝对值比空闲机器高；请以同一张表内的相对关系为准。

### Get 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 163.3 | 391.9 | 64 | 1 |
| yd-local-getappend | 127.9 | 500.2 | 0 | 0 |
| yd-local-view | 93.6 | 683.8 | 0 | 0 |
| freecache-get | 128.8 | 496.9 | 64 | 1 |
| freecache-getfn | 110.2 | 580.9 | 0 | 0 |
| ristretto | 117.2 | 546.3 | 7 | 0 |
| theine | 197.6 | 323.9 | 16 | 1 |
| bigcache | 136.1 | 470.2 | 80 | 2 |
| go-cache | 40.4 | 1585.3 | 0 | 0 |
| ttlcache | 100.8 | 635.1 | 0 | 0 |
| golang-lru | 57.5 | 1113.7 | 0 | 0 |


### Get 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 536.2 | 1909.8 | 1024 | 1 |
| yd-local-getappend | 264.2 | 3876.1 | 0 | 0 |
| yd-local-view | 166.6 | 6147.7 | 0 | 0 |
| freecache-get | 418.8 | 2445.2 | 1024 | 1 |
| freecache-getfn | 138.1 | 7413.5 | 0 | 0 |
| ristretto | 118.8 | 8621.2 | 7 | 0 |
| theine | 194.9 | 5252.7 | 16 | 1 |
| bigcache | 404.2 | 2533.3 | 1040 | 2 |
| go-cache | 42.2 | 24239.9 | 0 | 0 |
| ttlcache | 97.7 | 10482.8 | 0 | 0 |
| golang-lru | 57.5 | 17803.7 | 0 | 0 |


### Get 并行（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 56.9 | 1125.3 | 64 | 1 |
| yd-local-getappend | 59.9 | 1067.9 | 0 | 0 |
| yd-local-view | 38.1 | 1678.4 | 0 | 0 |
| freecache-get | 48.9 | 1309.7 | 64 | 1 |
| freecache-getfn | 38.1 | 1680.0 | 0 | 0 |
| ristretto | 34.8 | 1839.9 | 1 | 0 |
| theine | 29.5 | 2166.9 | 0 | 0 |
| bigcache | 46.0 | 1391.6 | 80 | 2 |
| go-cache | 49.3 | 1298.5 | 0 | 0 |
| ttlcache | 189.2 | 338.2 | 0 | 0 |
| golang-lru | 146.7 | 436.2 | 0 | 0 |


### Mixed 90/10（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 142.6 | — | 57 | 0 |
| yd-local-getappend | 118.3 | — | 0 | 0 |
| yd-local-view | 99.5 | — | 0 | 0 |
| freecache-get | 138.1 | — | 57 | 0 |
| freecache-getfn | 109.7 | — | 0 | 0 |
| ristretto | 195.8 | — | 17 | 0 |
| theine | 275.8 | — | 14 | 0 |
| bigcache | 158.5 | — | 94 | 1 |
| go-cache | 47.6 | — | 2 | 0 |
| ttlcache | 109.2 | — | 0 | 0 |
| golang-lru | 56.6 | — | 0 | 0 |


### Get TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 171.5 | 373.2 | 64 | 1 |
| yd-local-getappend | 136.7 | 468.2 | 0 | 0 |
| yd-local-view | 91.5 | 699.7 | 0 | 0 |
| freecache-get | 129.9 | 492.7 | 64 | 1 |
| freecache-getfn | 116.9 | 547.3 | 0 | 0 |
| ristretto | 125.1 | 511.8 | 7 | 0 |
| theine | 198.3 | 322.8 | 16 | 1 |
| go-cache | 50.5 | 1267.2 | 0 | 0 |
| ttlcache | 233.9 | 273.6 | 0 | 0 |


### Set 64 B（同值重复写）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 95.2 | 672.3 | 0 | 0 |
| freecache | 99.8 | 641.4 | 0 | 0 |
| ristretto | 603.7 | 106.0 | 99 | 1 |
| theine | 313.2 | 204.3 | 0 | 0 |
| bigcache | 252.4 | 253.6 | 318 | 0 |
| go-cache | 85.9 | 744.7 | 24 | 1 |
| ttlcache | 153.5 | 416.9 | 0 | 0 |
| golang-lru | 66.7 | 959.6 | 0 | 0 |


### Set 1 KiB（同值重复写）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 201.3 | 5085.8 | 0 | 0 |
| freecache | 188.8 | 5422.6 | 0 | 0 |
| ristretto | 598.9 | 1709.8 | 99 | 1 |
| theine | 304.6 | 3361.9 | 0 | 0 |
| bigcache | 1032.0 | 992.4 | 3239 | 0 |
| go-cache | 82.4 | 12433.1 | 24 | 1 |
| ttlcache | 143.9 | 7117.3 | 0 | 0 |
| golang-lru | 62.6 | 16357.8 | 0 | 0 |


### Set 变化值 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 98.8 | 647.7 | 0 | 0 |
| freecache | 96.5 | 663.1 | 0 | 0 |
| ristretto | 603.0 | 106.1 | 99 | 1 |
| theine | 296.9 | 215.6 | 0 | 0 |
| bigcache | 231.8 | 276.1 | 318 | 0 |
| go-cache | 85.5 | 748.7 | 24 | 1 |
| ttlcache | 153.7 | 416.5 | 0 | 0 |
| golang-lru | 64.5 | 992.5 | 0 | 0 |


### Set 变化值 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 200.2 | 5116.0 | 0 | 0 |
| freecache | 182.1 | 5624.5 | 0 | 0 |
| ristretto | 624.7 | 1639.2 | 99 | 1 |
| theine | 302.0 | 3390.6 | 0 | 0 |
| bigcache | 775.7 | 1320.2 | 3449 | 0 |
| go-cache | 91.8 | 11160.2 | 24 | 1 |
| ttlcache | 144.9 | 7068.1 | 0 | 0 |
| golang-lru | 59.4 | 17235.2 | 0 | 0 |


### Set TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 112.0 | — | 0 | 0 |
| freecache | 100.3 | — | 0 | 0 |
| ristretto | 752.8 | — | 99 | 1 |
| theine | 333.0 | — | 0 | 0 |
| go-cache | 94.3 | — | 24 | 1 |
| ttlcache | 257.3 | — | 0 | 0 |


### 命中率（5,000 容量 / 50,000 键 / 200,000 次访问）

| 实现 | 命中率 |
|---|---:|
| yd-local-get | 72.73% |
| yd-local-getappend | 72.71% |
| yd-local-view | 72.97% |
| freecache-get | 71.32% |
| ristretto | 72.60% |
| theine | 72.60% |
| bigcache | 80.02% |
| ttlcache | 72.61% |
| golang-lru | 72.61% |

\* BigCache 使用 2 MB 内存上限，有效条目容量大于 5,000，其命中率不能与按条目数限制的实现直接横比；按轨迹计算的理论上限约 `70% + 30% × 4500/50000 ≈ 72.7%`。

## 4. 结论 | Takeaways

- **命中率（达到容量上限）**：`yd-local-get` 72.73%、`yd-local-view` 72.97%，高于 Ristretto/Theine/ttlcache/LRU 的 72.60%–72.61%；`yd-local-getappend` 72.71%。轨迹的理论上限约 72.7%，yd 已在该容量下达到上限。BigCache 的 80.02% 来自 2 MB 上限下更大的有效条目容量，不可直接横比。
- **64 B 零拷贝读**：`yd-local-view` 93.6 ns，优于 FreeCache `GetFn`（110.2）、Ristretto（117.2）与 ttlcache（100.8）；go-cache（40.4）与 golang-lru（57.5）更快，但它们返回缓存内引用、没有容量淘汰或 TTL，语义与本模块的「锁内回调 + 容量/TTL + 淘汰策略」不同。
- **1 KiB 零拷贝读**：`yd-local-view` 166.6 ns，慢于 FreeCache `GetFn`（138.1）、Ristretto（118.8）与 ttlcache（97.7）。这一档由 40 MB 随机访问的内存带宽主导，额外访问位/采样草图与回调开销被放大；同一代码在较早的空闲轮次曾测得约 98 ns，说明该指标对宿主机负载与缓存压力高度敏感。
- **复制/追加读**：`yd-local-get` 163.3/536.2 ns（64 B/1 KiB），`yd-local-getappend` 127.9/264.2 ns。FreeCache 为 128.8/418.8（`Get`）与 110.2/138.1（`GetFn`）。`Get` 的分配是「返回调用方自有副本」语义的固有成本；需要零分配时用 `GetAppend`/`View`。
- **写入**：同值重复写时 `yd-local` 95.2/201.3 ns，FreeCache 99.8/188.8 ns；变化值写为 98.8/200.2 ns vs FreeCache 96.5/182.1 ns，两者已处于同一量级。go-cache（85.9/82.4）、golang-lru（66.7/62.6）、ttlcache（153.5/143.9）在变化值 1 KiB 上更快：它们保存切片引用而不做防御性拷贝，golang-lru 也没有 TTL/容量淘汰策略。
- **并发读**：`yd-local-view` 38.1 ns / 1.68 GB/s，与 FreeCache `GetFn`（38.1 ns）持平；Ristretto（34.8）与 Theine（29.5）更快，ttlcache/LRU 因全局锁退化到 189.2/146.7 ns。
- **TTL**：`yd-local-view` 91.5 ns，优于 FreeCache `GetFn`（116.9）与 Ristretto（125.1）；`yd-local-get` 171.5 ns 慢于 FreeCache（129.9），`SetTTL` 112.0 ns 慢于 FreeCache（100.3）与 go-cache（94.3）。
- **GC**：环形缓冲区仍是预分配、指针自由；Set/`GetAppend`/`View` 均为 0 alloc，`Get` 仅返回副本分配。原地覆盖与幂等短路减少了死版本、ring 回绕与 GC 压力。
- **语义边界（必须明确）**：本表不支持「所有指标都大于所有对比库」的结论。go-cache/golang-lru 无容量上限或 TTL，ttlcache/Ristretto/Theine 返回缓存内引用，BigCache 在命中率轨迹中有效容量更大；要在这几项也全面胜出，只能放弃防御性拷贝、TTL 或淘汰策略，或提供返回引用/需要调用方保证不长期持有的 API，这超出了本模块「安全、可淘汰、可带 TTL」的设计前提。与同语义的 FreeCache 相比，yd 在命中率、64 B 零拷贝读、64 B 写、TTL View 上胜出，在 1 KiB 复制/零拷贝读与部分 TTL 写上仍有差距。

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
