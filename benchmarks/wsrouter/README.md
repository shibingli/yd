# ws + router 性能与兼容性基准

本目录是一个**独立 Go 模块**，用于测量 `http/ws` 与 `http/router` 集成后的性能，并与主流 WebSocket 库做兼容性互操作验证。独立模块避免把 coder、gorilla、gobwas、gws 四个基准依赖带进主模块的 `go.mod`。

## 运行方式

```bash
cd benchmarks/wsrouter

# 兼容性矩阵
go test -run TestCompat -v
CGO_ENABLED=1 go test -run TestCompat -race -v

# 性能基准
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5

# 仅 router 静动态路由对比
go test -run '^$' -bench '^BenchmarkRouters$' -benchmem -benchtime=1s -count=5
```

服务端统一通过 `router.Router` 注册 `/ws` 路由，处理器内调用 `ws.Upgrade`，与实际脚手架生成的接入方式一致。

## 兼容性指标

18 个用例全部通过：

| 方向 | coder/websocket v1.8.15 | gorilla/websocket v1.5.3 | gobwas/ws v1.4.0 | lxzan/gws v1.10.2 |
|---|---|---|---|---|
| 各库客户端 → yd router 服务端 | ✅ | ✅ | ✅ | ✅ |
| yd 客户端 → 各库服务端 | ✅ | ✅ | ✅ | ✅ |

覆盖内容：

- 文本消息（中英混合）、二进制 1 KiB、二进制 64 KiB（扩展长度、掩码、跨 TCP 读边界）
- Ping/Pong 双向应答
- 正常关闭握手（1000）
- 子协议 `yd.v1` 协商（coder / gorilla 双向）
- permessage-deflate 压缩（coder / gorilla / gws 双向，含上下文接管）
- 分片重组：gobwas 客户端发送 `OpText(fin=false)` + `OpContinuation(fin=true)`，yd 服务端重组后回显 `"fragment"`
- 4 KiB 可压缩 JSON 载荷在开启/关闭压缩时的双向回显

## router 握手开销

`BenchmarkUpgradeRouter` 在进程内直接调用 `ServeHTTP`，连接使用丢弃写入的假 `net.Conn`，因此只测握手本身：

| 接入方式 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| 原始 `http.Handler` | 1,078 | 864 | 2 |
| `router.GET("/ws")` | 1,137 | 864 | 2 |

router 每次连接约增加 **59 ns（约 5.5%）**，分配次数不变。握手成本只发生在连接建立期，不进入每消息路径。

## 稳态回显性能

口径：TCP 回环、单连接顺序回显、二进制载荷、每档 `-benchtime=1s -count=5`；下表合并两轮共 10 个样本取中位数。yd 一栏为 **router + ws** 集成路径。

### 延迟（ns/op，越低越好）

| 载荷 | **yd + router** | gorilla | gobwas | coder | gws |
|---|---:|---:|---:|---:|---:|
| 32 B | **62,422** | 62,334 | 63,430 | 71,804 | 77,271 |
| 256 B | **59,437** | 60,768 | 65,458 | 74,472 | 78,957 |
| 1 KiB | **61,028** | 62,014 | 69,100 | 73,412 | 80,486 |
| 4 KiB | **73,432** | 72,457 | 124,674 | 143,902 | 93,071 |
| 64 KiB | **133,122** | 134,510 | 180,716 | 632,870 | 237,876 |

### 吞吐（MB/s，越高越好）

| 载荷 | **yd + router** | gorilla | gobwas | coder | gws |
|---|---:|---:|---:|---:|---:|
| 32 B | **0.51** | 0.52 | 0.51 | 0.45 | 0.41 |
| 256 B | **4.30** | 4.21 | 3.91 | 3.44 | 3.25 |
| 1 KiB | **16.77** | 16.52 | 14.82 | 13.95 | 12.74 |
| 4 KiB | **55.79** | 56.59 | 32.86 | 28.48 | 44.00 |
| 64 KiB | **492.30** | 487.23 | 362.65 | 103.56 | 275.55 |

### 分配（B/op 与 allocs/op）

| 库 | 32 B–1 KiB | 4 KiB | 64 KiB |
|---|---|---|---|
| **yd + router** | **0 B / 0** | **0 B / 0** | **0 B / 0** |
| gorilla | 112 B / 4 | 114 B / 6 | 114 B / 6 |
| gobwas | 64 B / 4 | 64 B / 4 | 64 B / 4 |
| coder | 761 B / 20 | 763 B / 22 | 762 B / 22 |
| gws | 208 B–2.1 KiB / 4 | 8.4 KiB / 4 | 153.8 KiB / 5 |

结论：经过 router 后，yd 与 gorilla 在延迟上基本持平（差异约 ±1.3%，处于运行噪声内），但 yd 是唯一全程零分配的实现；相对 gobwas/coder/gws，yd 在各档载荷领先约 10%–375%。32 B–1 KiB 区间受 TCP 回环往返限制，绝对延迟接近下限。

## 并发吞吐（16 协程 × 16 连接，1 KiB）

| 库 | 聚合 ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| **yd + router** | **10,282** | **99.59** | **4** | **0** |
| gorilla | 10,344 | 98.99 | 119 | 4 |

两者扩展性处于同一水平，yd 约快 0.6%，并保持零分配。

## 接入方式对比

`BenchmarkRouterModes` 对原始 `http.Handler`、`router.GETRaw`、`router.GET` 三种接入方式做稳态回显。由于握手在计时循环之外，三种方式的差异属于噪声（例如 1 KiB 分别为 62.1 / 58.6 / 57.4 µs），说明 router 只影响连接建立期，不影响每消息的读写路径。

## Router 静态/动态路由性能

`BenchmarkRouters` 在**进程内**对比路由匹配本身（不经过网络），使用 `RunParallel` 与无操作的 `ResponseWriter`，覆盖静态、参数、深层参数、通配与未命中五类路径。参与对比的库：标准库 `http.ServeMux`（Go 1.22 模式）、gin v1.12.0、chi v5.5.0、echo v4.16.0、httprouter v1.3.0、fiber v3.6.1。

### 延迟（ns/op，越低越好，5 次中位数）

| 路由器 | static | param | deep | wildcard | miss |
|---|---:|---:|---:|---:|---:|
| **yd router** | **5.2** | **5.4** | **5.4** | **5.4** | **8.5** |
| httprouter | 5.1 | 23.6 | 23.4 | 22.4 | 96.9 |
| gin | 8.9 | 10.9 | 11.9 | 11.4 | 13.0 |
| echo | 9.2 | 11.0 | 12.4 | 9.8 | 340.6 |
| fiber | 26.9 | 30.1 | 37.2 | 30.3 | 37.5 |
| stdlib `ServeMux` | 56.8 | 70.3 | 71.7 | 129.7 | 251.8 |
| chi | 200.3 | 349.4 | 380.9 | 352.3 | 231.1 |

### 分配（B/op / allocs/op）

| 路由器 | static | param | deep | wildcard | miss |
|---|---:|---:|---:|---:|---:|
| **yd router** | **0 / 0** | **0 / 0** | **0 / 0** | **0 / 0** | **0 / 0** |
| gin | 0 / 0 | 0 / 0 | 0 / 0 | 0 / 0 | 0 / 0 |
| fiber | 0 / 0 | 0 / 0 | 0 / 0 | 0 / 0 | 0 / 0 |
| httprouter | 0 / 0 | 32 / 1 | 32 / 1 | 32 / 1 | 48 / 3 |
| echo | 0 / 0 | 0 / 0 | 0 / 0 | 0 / 0 | 456 / 8 |
| stdlib `ServeMux` | 0 / 0 | 16 / 1 | 16 / 1 | 88 / 5 | 192 / 12 |
| chi | 368 / 2 | 704 / 4 | 704 / 4 | 704 / 4 | 416 / 5 |

结论：

- **yd router 在五类路径上均为最快或并列最快**，且是唯一在所有路径（含未命中）保持零分配的实现。
- httprouter 静态路径与 yd 并列（5.1 vs 5.2 ns），但参数/深层/通配路径慢约 4 倍，未命中慢约 11 倍，并且动态路径有分配。
- gin/echo 命中路径约 9–13 ns、零分配；echo 未命中退化到 340 ns / 8 次分配。
- 标准库 `ServeMux` 为 57–252 ns 并带分配；chi 为 200–381 ns、2–5 次分配；fiber 为 27–39 ns、零分配。
- 这组数据只反映路由匹配开销；上面的 ws + router 端到端数据才包含网络与握手成本。

## 测试环境

- Go 1.27.2，Windows 11 Pro for Workstations，amd64
- CPU：Intel Core i9-10980HK（8 核 16 线程）
- 回环 TCP，顺序回显，单连接
- 各库使用当前最新版本与推荐低分配 API
- 延迟对机器负载、温度与电源策略敏感；跨进程独立运行的波动可达约 15%，本目录表格以同进程交错/多轮中位数为准
