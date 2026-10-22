# Router 静动态路由性能基准

本目录是一个**独立 Go 模块**，用于对比 `ydx/router` 与主流 Go 路由器的静态/动态路由性能，并测量 WebSocket 握手路径上的 router 开销。

## 运行方式

```bash
cd benchmarks/router

# 静动态路由对比（同进程快速检查）
go test -run '^$' -bench '^BenchmarkRouters$' -benchmem -benchtime=1s -count=5

# WebSocket 握手路径上的 router 开销
go test -run '^$' -bench '^BenchmarkUpgradeRouter$' -benchmem -benchtime=1s -count=5

# 公平采样：每个实现独立进程，轮转顺序
for r in yd stdlib gin chi echo httprouter fiber; do
  YD_BENCH_ONLY=$r go test -run '^$' -bench '^BenchmarkRouters$' -benchmem -benchtime=1s -count=5
done
```

`YD_BENCH_ONLY` 是本目录基准的进程内过滤器；主表采用「每实现独立进程 + 轮转顺序 + 5 轮中位数」。

## 测试平台

主表在 **x86_64 基准平台**上采集：

- AMD Ryzen AI MAX+ 395，16 核 32 线程，1 NUMA 节点，L3 64 MiB，30 GiB 内存；
- Ubuntu 26.04.1 LTS，Linux 7.0.0-38-generic，Go 1.27.2 linux/amd64；
- CPU governor `performance`；
- 进程内匹配，不经过网络；`RunParallel`，GOMAXPROCS=32。

CPU 利用率与峰值 RSS 使用 Linux `/usr/bin/time -v`，对每个实现单独进程测量 3 轮取中位数（2 s 计时，param 场景）。CPU% 是进程平均占用；`RunParallel` 会占满多个核心，因此可远高于 100%。RSS 包含 Go 运行时、测试二进制与夹具。

## 参测实现

标准库 `http.ServeMux`（Go 1.22 模式）、gin v1.12.0、chi v5.5.0、echo v4.16.0、httprouter v1.3.0、fiber v3.6.1，以及本项目的 `yd router`。所有实现注册同一组路由，并在计时前用真实请求验证命中状态（命中 204、未命中 404），避免把 404 路径误当成路由性能。

## 静动态路由对比

### 延迟（ns/op，越低越好，5 轮独立进程中位数）

`RunParallel` 的聚合值；32 线程并行时每操作可低于 1 ns。

| 路由器 | static | param | deep | wildcard | miss |
|---|---:|---:|---:|---:|---:|
| **yd router** | 1.1 | **1.2** | **1.1** | **1.2** | **1.6** |
| httprouter | **0.9** | 8.0 | 8.1 | 8.2 | 23.3 |
| gin | 1.6 | 2.0 | 2.1 | 2.0 | 2.2 |
| echo | 1.8 | 1.8 | 2.2 | 1.7 | 133.6 |
| fiber | 3.8 | 4.2 | 5.2 | 4.5 | 5.3 |
| stdlib `ServeMux` | 25.1 | 37.8 | 36.9 | 62.0 | 123.5 |
| chi | 84.5 | 158.5 | 155.4 | 154.5 | 109.3 |

### 分配（B/op / allocs/op）

| 路由器 | static | param | deep | wildcard | miss |
|---|---:|---:|---:|---:|---:|
| **yd router** | **0 / 0** | **0 / 0** | **0 / 0** | **0 / 0** | **0 / 0** |
| httprouter | 0 / 0 | 32 / 1 | 32 / 1 | 32 / 1 | 48 / 3 |
| gin | 0 / 0 | 0 / 0 | 0 / 0 | 0 / 0 | 0 / 0 |
| echo | 0 / 0 | 0 / 0 | 0 / 0 | 0 / 0 | 457 / 8 |
| fiber | 0 / 0 | 0 / 0 | 0 / 0 | 0 / 0 | 0 / 0 |
| stdlib `ServeMux` | 0 / 0 | 16 / 1 | 16 / 1 | 88 / 5 | 192 / 12 |
| chi | 368 / 2 | 704 / 4 | 704 / 4 | 704 / 4 | 417 / 5 |

### 资源利用率（param 场景）

| 路由器 | ns/op | 平均 CPU | 峰值 RSS |
|---|---:|---:|---:|
| **yd router** | 1.1 | 3,157% | 19.8 MiB |
| httprouter | 7.8 | 945% | 54.6 MiB |
| gin | 1.9 | 3,169% | 19.8 MiB |
| echo | 1.8 | 3,171% | 19.7 MiB |
| fiber | 4.1 | 3,184% | 19.8 MiB |
| stdlib `ServeMux` | 37.6 | 2,613% | 31.0 MiB |
| chi | 155.2 | 682% | 36.6 MiB |

## WebSocket 握手路径上的 router 开销

`BenchmarkUpgradeRouter` 在进程内直接调用 `ServeHTTP`，连接使用丢弃写入的假 `net.Conn`，只测握手本身。5 轮独立进程中位数。

| 接入方式 | ns/op | B/op | allocs/op | 平均 CPU | 峰值 RSS |
|---|---:|---:|---:|---:|---:|
| 原始 `http.Handler` | 362 | 864 | 2 | 181% | 24.4 MiB |
| `router.GET("/ws")` | 377 | 865 | 2 | 177% | 24.7 MiB |

router 每次连接约增加 **15 ns（约 4.2%）**，分配次数不变。握手成本只发生在连接建立期，不进入每消息路径。

## 结论（全部实现同表，不删弱项）

- **动态路径**：yd 在 param、deep、wildcard、miss 四类均为最低延迟；param/deep/wildcard 约 1.1–1.2 ns，未命中 1.6 ns。
- **静态路径**：httprouter 最快（0.9 ns），yd 为 1.1 ns，慢约 0.2 ns（约 22%）；这是当前唯一的静态路径劣势。
- **未命中**：yd 1.6 ns 最快；gin 2.2 ns、fiber 5.3 ns 也保持低延迟；echo 133.6 ns、stdlib 123.5 ns、chi 109.3 ns，且 echo 有 8 次分配。
- **分配**：yd、gin、fiber 在五类路径全部零分配；httprouter 动态路径 32 B/1 alloc；echo 未命中 457 B/8；stdlib 与 chi 在多类路径有分配。
- **资源**：httprouter 在 param 场景 CPU 占用较低（945%）但 RSS 最高（54.6 MiB，包含其路由树与测试夹具）；yd/gin/echo/fiber 都接近占满 32 线程；chi 因每次请求分配较多，CPU 占用反而较低（682%）。CPU% 只描述测量进程，不代表单位请求成本。
- 所有数值均为原始中位数；`RunParallel` 的 ns/op 是聚合值，不能直接与单线程库的绝对延迟横向换算。
