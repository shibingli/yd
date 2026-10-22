# 缓存性能对比 | Cache Benchmarks

本模块把 `data/cachex/local`（yd 一级缓存）与主流 Go 缓存库放在同一口径下对比：Ristretto、FreeCache、Theine、BigCache、go-cache、ttlcache 与 golang-lru。

This module compares the yd L1 cache (`data/cachex/local`) with mainstream Go caches under one methodology: Ristretto, FreeCache, Theine, BigCache, go-cache, ttlcache and golang-lru.

## 1. 对比实现 | Implementations

| 名称 | 版本 | 淘汰/容量模型 | 读语义 |
|---|---|---|---|
| `yd-local-get` | 本仓 | W-TinyLFU 准入 + CLOCK 访问位 + 分片环形缓冲区，按字节预算 | 复制返回 |
| `yd-local-getappend` | 本仓 | 同上 | 复制进调用方缓冲区 |
| `yd-local-view` | 本仓 | 同上 | 零拷贝回调（不更新访问位/频率） |
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
- **复制语义不同**：`yd-local-get`、`freecache-get` 在读取时复制；`yd-local-view`、`freecache-getfn` 提供零拷贝回调；Ristretto/Theine/go-cache/ttlcache/golang-lru 返回存储引用；BigCache 在公开 `Get` 中复制（`B/op` 可见）；FreeCache 的复制/零拷贝分别由 `get`/`getfn` 提供。表中 `B/op`/`allocs/op` 只统计调用路径，不统计缓存已持有的值。
- **TTL**：`Get TTL`/`Set TTL` 只包含支持逐条 TTL 的实现；BigCache 只有全局 `LifeWindow`，golang-lru 没有 TTL，因此不参与。
- **go-cache** 没有容量上限，不参与命中率轨迹；**BigCache** 在命中率轨迹中配置 2 MB 内存上限，其有效条目容量大于 5,000，因此 80% 的命中率不能与按条目数限制的实现直接横比。
- Ristretto 的 `MaxCost` 默认会把内部存储开销计入成本；基准设置 `IgnoreInternalCost: true`，使 `MaxCost` 对应条目数，与其他实现的 5,000 容量可比。
- 每个实现由 `YD_BENCH_ONLY` 在**独立进程**中运行，`-count=3` 取中位数，减少同进程顺序与 GC 偏差。

## 3. 结果 | Results

### Get 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 182.3 | 351.1 | 64 | 1 |
| yd-local-getappend | 147.3 | 434.4 | 0 | 0 |
| yd-local-view | 101.0 | 633.9 | 0 | 0 |
| freecache-get | 137.6 | 465.1 | 64 | 1 |
| freecache-getfn | 106.2 | 602.7 | 0 | 0 |
| ristretto | 123.5 | 518.4 | 7 | 0 |
| theine | 203.1 | 315.2 | 16 | 1 |
| bigcache | 147.4 | 434.1 | 80 | 2 |
| go-cache | 42.5 | 1505.3 | 0 | 0 |
| ttlcache | 107.2 | 597.1 | 0 | 0 |
| golang-lru | 64.8 | 988.1 | 0 | 0 |

### Get 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 511.6 | 2001.6 | 1024 | 1 |
| yd-local-getappend | 276.6 | 3701.4 | 0 | 0 |
| yd-local-view | 251.8 | 4066.1 | 0 | 0 |
| freecache-get | 388.1 | 2638.5 | 1024 | 1 |
| freecache-getfn | 133.8 | 7655.9 | 0 | 0 |
| ristretto | 122.0 | 8392.2 | 7 | 0 |
| theine | 201.1 | 5091.6 | 16 | 1 |
| bigcache | 400.2 | 2558.5 | 1040 | 2 |
| go-cache | 41.1 | 24885.2 | 0 | 0 |
| ttlcache | 108.9 | 9404.8 | 0 | 0 |
| golang-lru | 60.0 | 17072.5 | 0 | 0 |

### Get 并行（64 B，GOMAXPROCS=16）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 70.0 | 914.3 | 64 | 1 |
| yd-local-getappend | 59.6 | 1074.2 | 0 | 0 |
| yd-local-view | 36.8 | 1739.0 | 0 | 0 |
| freecache-get | 48.8 | 1310.7 | 64 | 1 |
| freecache-getfn | 39.5 | 1619.5 | 0 | 0 |
| ristretto | 36.8 | 1737.3 | 1 | 0 |
| theine | 28.8 | 2221.0 | 0 | 0 |
| bigcache | 46.3 | 1383.2 | 80 | 2 |
| go-cache | 50.2 | 1276.0 | 0 | 0 |
| ttlcache | 211.7 | 302.3 | 0 | 0 |
| golang-lru | 159.4 | 401.6 | 0 | 0 |

### Set 64 B

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 221.4 | 289.1 | 0 | 0 |
| freecache | 101.9 | 628.4 | 0 | 0 |
| ristretto | 663.4 | 96.5 | 99 | 1 |
| theine | 344.6 | 185.7 | 0 | 0 |
| bigcache | 248.0 | 258.1 | 229 | 0 |
| go-cache | 83.3 | 768.2 | 24 | 1 |
| ttlcache | 152.8 | 418.9 | 0 | 0 |
| golang-lru | 63.4 | 1009.2 | 0 | 0 |

### Set 1 KiB

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 405.1 | 2527.6 | 0 | 0 |
| freecache | 200.4 | 5109.5 | 0 | 0 |
| ristretto | 683.8 | 1497.4 | 99 | 1 |
| theine | 330.1 | 3102.3 | 0 | 0 |
| bigcache | 733.7 | 1395.6 | 2906 | 0 |
| go-cache | 82.0 | 12491.3 | 24 | 1 |
| ttlcache | 155.7 | 6578.5 | 0 | 0 |
| golang-lru | 62.2 | 16453.6 | 0 | 0 |

### Mixed 90/10（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 107.2 | — | 7 | 0 |
| yd-local-getappend | 100.7 | — | 0 | 0 |
| yd-local-view | 88.0 | — | 0 | 0 |
| freecache-get | 133.1 | — | 57 | 0 |
| freecache-getfn | 102.1 | — | 0 | 0 |
| ristretto | 202.9 | — | 17 | 0 |
| theine | 279.2 | — | 14 | 0 |
| bigcache | 171.6 | — | 99 | 1 |
| go-cache | 54.4 | — | 2 | 0 |
| ttlcache | 120.2 | — | 0 | 0 |
| golang-lru | 63.3 | — | 0 | 0 |

### Get TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local-get | 182.9 | 350.0 | 64 | 1 |
| yd-local-getappend | 144.3 | 443.6 | 0 | 0 |
| yd-local-view | 104.0 | 615.6 | 0 | 0 |
| freecache-get | 137.8 | 464.6 | 64 | 1 |
| freecache-getfn | 100.2 | 638.5 | 0 | 0 |
| ristretto | 134.2 | 477.1 | 7 | 0 |
| theine | 204.7 | 312.7 | 16 | 1 |
| go-cache | 53.8 | 1190.5 | 0 | 0 |
| ttlcache | 282.5 | 226.5 | 0 | 0 |

### Set TTL 1h（64 B）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| yd-local | 236.4 | — | 0 | 0 |
| freecache | 109.6 | — | 0 | 0 |
| ristretto | 840.1 | — | 99 | 1 |
| theine | 374.3 | — | 0 | 0 |
| go-cache | 92.3 | — | 24 | 1 |
| ttlcache | 263.3 | — | 0 | 0 |

### 命中率（5,000 容量 / 50,000 键 / 200,000 次访问）

| 实现 | 命中率 |
|---|---:|
| yd-local-get | 70.22% |
| yd-local-getappend | 70.52% |
| yd-local-view | 70.99% |
| freecache-get / getfn | 71.32% |
| ristretto | 72.59% |
| theine | 72.62% |
| ttlcache | 72.61% |
| golang-lru | 72.61% |
| bigcache | 80.02%* |

\* BigCache 使用 2 MB 内存上限，有效条目容量大于 5,000，不能与按条目数限制的实现直接横比。

`yd-local-view` 不更新访问位与频率草图，命中率仅用于展示零拷贝路径；淘汰策略对比以 `yd-local-get` 为准。

## 4. 结论 | Takeaways

- **零拷贝读路径有竞争力**：64 B 下 `yd-local-view` 为 101.0 ns、0 alloc，略快于 FreeCache `GetFn`（106.2 ns）并与 Ristretto（123.5 ns）同一量级；1 KiB 下 FreeCache `GetFn`（133.8 ns）和 Ristretto（122.0 ns）更快，`yd-local-view` 为 251.8 ns，主要成本是分片读锁内的哈希/槽表查找与统计。
- **复制读路径**：`yd-local-getappend` 在 1 KiB 下为 276.6 ns / 0 alloc，快于 FreeCache `Get`（388.1 ns / 1 alloc）；`yd-local-get`（复制返回）为 511.6 ns / 1 alloc，慢于 FreeCache 的复制读，但提供了安全的返回值语义。
- **写路径**：`yd-local` 的 Set 为 221.4 ns（64 B）/ 405.1 ns（1 KiB）、0 alloc；FreeCache 更快（101.9 / 200.4 ns）。yd 的 Set 成本包含环形缓冲区复制、W-TinyLFU 采样与统计；Ristretto/Theine 的数值包含每 64 次 Set 一次同步 `Wait()` 的成本。
- **并发读**：`yd-local-view` 36.8 ns / 1.74 GB/s，与 Ristretto 持平；Theine 最快（28.8 ns / 2.22 GB/s）；ttlcache/LRU 因全局锁在 16 线程下明显退化（211.7 / 159.4 ns）。
- **命中率**：该轨迹的热键集合（500）远小于容量（5,000），LRU 类实现已经能全部保留热键，因此 yd 的 W-TinyLFU 与 LRU 基本持平（70.2–71.0% vs 72.6%）；W-TinyLFU 的优势场景是热集合大、一次性冷键多，而不是“小热集合 + 大容量”。
- **GC 语义**：yd 的 L1 条目存放在预分配环形缓冲区，`View`/`GetAppend` 为 0 alloc；Set 把值复制进环形缓冲区，因此不持有调用方的切片。Ristretto/Theine/BigCache/go-cache/ttlcache/LRU 的 `B/op` 不包含其保留的值；比较吞吐时应同时看复制语义和 `allocs/op`。

## 5. 复现 | Reproduce

```bash
cd benchmarks/cache

# 命中率轨迹（确定性冷热分布）
go test ./... -run TestHitRatio -v -count=1

# 单个实现独立进程，3 轮取中位数
for impl in yd-local-get yd-local-getappend yd-local-view freecache-get freecache-getfn \
            ristretto theine bigcache go-cache ttlcache golang-lru; do
  YD_BENCH_ONLY=$impl go test -run '^$' \
    -bench 'Benchmark(Get|GetParallel|Mixed|GetTTL)' -benchmem -benchtime=300ms -count=3
done

for impl in yd-local freecache ristretto theine bigcache go-cache ttlcache golang-lru; do
  YD_BENCH_ONLY=$impl go test -run '^$' \
    -bench 'Benchmark(Set|SetTTL)' -benchmem -benchtime=300ms -count=3
done
```

## 6. 测试平台 | Platform

- Windows 11 Pro for Workstations，Intel Core i9-10980HK（8 核 16 线程，`GOMAXPROCS=16`），Go 1.27.2 windows/amd64。
- 数值对机器负载、温度与电源策略敏感；跨机比较请以同一平台的相对关系为准，建议在目标 Linux 服务器上用 `-count=5` 重新采集。
- 本模块是独立 Go module，第三方缓存库不会进入框架根 `go.mod`。
