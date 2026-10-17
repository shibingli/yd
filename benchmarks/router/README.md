# Router 静动态路由性能基准

本目录是一个**独立 Go 模块**，用于对比 `http/router` 与主流 Go 路由器的静态/动态路由性能，并测量 WebSocket 握手路径上的 router 开销。

## 运行方式

```bash
cd benchmarks/router

# 静动态路由对比
go test -run '^$' -bench '^BenchmarkRouters$' -benchmem -benchtime=1s -count=5

# WebSocket 握手路径上的 router 开销
go test -run '^$' -bench '^BenchmarkUpgradeRouter$' -benchmem -benchtime=1s -count=5
```

## 静动态路由对比

`BenchmarkRouters` 在**进程内**对比路由匹配本身（不经过网络），使用 `RunParallel` 与无操作的 `ResponseWriter`，覆盖静态、参数、深层参数、通配与未命中五类路径。参与对比的库：标准库 `http.ServeMux`（Go 1.22 模式）、gin v1.12.0、chi v5.5.0、echo v4.16.0、httprouter v1.3.0、fiber v3.6.1。

### 延迟（ns/op，越低越好，5 次中位数）

| 路由器 | static | param | deep | wildcard | miss |
|---|---:|---:|---:|---:|---:|
| **yd router** | **4.8** | **5.1** | **5.1** | **5.1** | **8.8** |
| httprouter | 4.3 | 20.7 | 21.6 | 21.0 | 97.8 |
| gin | 9.1 | 10.8 | 11.6 | 11.8 | 12.5 |
| echo | 8.7 | 10.8 | 12.2 | 9.2 | 340.0 |
| fiber | 25.5 | 28.2 | 36.4 | 29.3 | 36.6 |
| stdlib `ServeMux` | 54.9 | 65.8 | 69.1 | 131.4 | 251.5 |
| chi | 164.5 | 297.2 | 310.0 | 293.6 | 222.2 |

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

- **yd router 在参数、深层、通配与未命中路径上均为最快**，静态路径仅比 httprouter 慢约 0.5 ns（4.8 vs 4.3 ns）。
- yd 是唯一在五类路径（含未命中）保持**零分配**的实现；gin 与 fiber 在命中路径零分配，但 fiber 静态延迟约为 yd 的 5 倍。
- httprouter 静态路径最快，但参数/深层/通配路径慢约 4 倍，未命中慢约 11 倍，并且动态路径有分配。
- echo 命中路径接近 gin，但未命中退化到 340 ns / 8 次分配；标准库 `ServeMux` 为 55–252 ns 并带分配；chi 为 165–310 ns、2–5 次分配。

## WebSocket 握手路径上的 router 开销

`BenchmarkUpgradeRouter` 在进程内直接调用 `ServeHTTP`，连接使用丢弃写入的假 `net.Conn`，只测握手本身：

| 接入方式 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| 原始 `http.Handler` | 1,107 | 864 | 2 |
| `router.GET("/ws")` | 1,169 | 864 | 2 |

router 每次连接约增加 **62 ns（约 5.6%）**，分配次数不变。握手成本只发生在连接建立期，不进入每消息路径。

## 测试环境

- Go 1.27.2，Windows 11 Pro for Workstations，amd64
- CPU：Intel Core i9-10980HK（8 核 16 线程）
- 进程内路由匹配，`RunParallel`，无网络
- 延迟对机器负载、温度与电源策略敏感；跨进程独立运行的波动可达约 15%
