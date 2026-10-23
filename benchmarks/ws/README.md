# WebSocket 性能与兼容性基准

本目录是一个**独立 Go 模块**，用于测量 `ydx/websocket` 的性能，并与主流 WebSocket 库做兼容性互操作验证。独立模块避免把 coder、gorilla、gobwas、gws 四个基准依赖带进主模块的 `go.mod`。

## 运行方式

```bash
cd benchmarks/ws

# 兼容性矩阵
go test -run TestCompat -v
CGO_ENABLED=1 go test -run TestCompat -race -v

# 性能基准（同进程快速检查）
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5

# 公平采样：每个实现独立进程，轮转顺序
for lib in our our-readahead coder gorilla gobwas gws; do
  YD_BENCH_ONLY=$lib go test -run '^$' -bench '^BenchmarkEcho$' -benchmem -benchtime=1s -count=5
done

# 并发吞吐与流水线回显（预读模式：our-readahead）
for lib in our our-readahead coder gorilla gobwas gws; do
  YD_BENCH_ONLY=$lib go test -run '^$' -bench '^BenchmarkEchoConcurrent$' -benchmem -benchtime=3s -count=5
done
YD_BENCH_ONLY=our go test -run '^$' -bench '^BenchmarkEchoPipelined$' -benchmem -benchtime=2s -count=5
```

`YD_BENCH_ONLY` 是本目录基准的进程内过滤器；设置后只有名称匹配的实现会运行。主表采用「每实现独立进程 + 轮转顺序 + 5 轮中位数」，避免同进程 codec 顺序与 GC 状态带来的偏差。

## 测试平台

主表在 **x86_64 基准平台**上采集：

- AMD Ryzen AI MAX+ 395，16 核 32 线程，1 NUMA 节点，L3 64 MiB，30 GiB 内存；
- Ubuntu 26.04.1 LTS，Linux 7.0.0-38-generic，Go 1.27.2 linux/amd64；
- CPU governor `performance`，AVX-512F/BW/CD/DQ/VL + VBMI/VBMI2/VNNI/BF16；
- TCP 回环、单连接顺序回显、二进制载荷；每档 5 轮中位数。
- 本轮（2026-10-18）在同一平台的 `mbox.yun-dev.cn` 上重新采集，Go 1.27.2 linux/amd64、GOMAXPROCS=32。

CPU 利用率与峰值 RSS 使用 Linux `/usr/bin/time -v`，对每个实现单独进程测量 3 轮取中位数（2 s 计时）。CPU% 是进程平均占用，Go 运行时/GC 与并行测试会让它略高于 100%；RSS 是进程峰值常驻内存，包含 Go 运行时、测试二进制与夹具。

## 兼容性指标

18 个用例全部通过（本地开发机与 x86_64 基准平台均通过）：

| 方向 | coder/websocket v1.8.15 | gorilla/websocket v1.5.3 | gobwas/ws v1.4.0 | lxzan/gws v1.10.2 |
|---|---|---|---|---|
| 各库客户端 → yd 服务端 | ✅ | ✅ | ✅ | ✅ |
| yd 客户端 → 各库服务端 | ✅ | ✅ | ✅ | ✅ |

覆盖内容：

- 文本消息（中英混合）、二进制 1 KiB、二进制 64 KiB（扩展长度、掩码、跨 TCP 读边界）
- Ping/Pong 双向应答
- 正常关闭握手（1000）
- 子协议 `yd.v1` 协商（coder / gorilla 双向）
- permessage-deflate 压缩（coder / gorilla / gws 双向，含上下文接管）
- 分片重组：gobwas 客户端发送 `OpText(fin=false)` + `OpContinuation(fin=true)`，yd 服务端重组后回显 `"fragment"`
- 4 KiB 可压缩 JSON 载荷在开启/关闭压缩时的双向回显

## 顺序回显性能

口径：TCP 回环、单连接、二进制载荷、每档 5 轮独立进程取中位数。每格为「延迟 ns/op / 吞吐 MB/s」，越低/越高越好。

| 载荷 | **yd** | gorilla | gobwas | coder | gws |
|---|---:|---:|---:|---:|---:|
| 32 B | **4,043 ns / 7.92 MB/s** | 4,262 / 7.51 | 4,354 / 7.35 | 5,561 / 5.75 | 6,399 / 5.00 |
| 256 B | **4,177 ns / 61.32 MB/s** | 4,383 / 58.41 | 4,580 / 55.90 | 5,682 / 45.06 | 6,588 / 38.86 |
| 1 KiB | **4,255 ns / 240.66 MB/s** | 4,470 / 229.08 | 4,708 / 217.49 | 5,723 / 178.94 | 6,918 / 148.03 |
| 4 KiB | **5,205 ns / 786.94 MB/s** | 5,526 / 741.21 | 8,253 / 496.31 | 8,765 / 467.32 | 8,307 / 493.08 |
| 64 KiB | **16,776 ns / 3,906.49 MB/s** | 18,061 / 3,628.61 | 22,324 / 2,935.73 | 37,849 / 1,731.52 | 18,326 / 3,576.15 |

### 分配（B/op / allocs/op）

| 库 | 32 B | 256 B | 1 KiB | 4 KiB | 64 KiB |
|---|---:|---:|---:|---:|---:|
| **yd** | **0 / 0** | **0 / 0** | **0 / 0** | **0 / 0** | **0 / 0** |
| gorilla | 112 / 4 | 112 / 4 | 112 / 4 | 114 / 6 | 114 / 6 |
| gobwas | 64 / 4 | 64 / 4 | 64 / 4 | 64 / 4 | 64 / 4 |
| coder | 0 / 0 | 0 / 0 | 0 / 0 | 2 / 2 | 2 / 2 |
| gws | 32 / 2 | 32 / 2 | 32 / 2 | 32 / 2 | 38 / 2 |

### 资源利用率（64 KiB 顺序回显）

| 库 | ns/op | 平均 CPU | 峰值 RSS |
|---|---:|---:|---:|
| **yd** | **17,032** | 128% | **14.3 MiB** |
| gorilla | 18,537 | 126% | 14.9 MiB |
| gobwas | 22,530 | 103% | 14.3 MiB |
| coder | 38,656 | 103% | 14.5 MiB |
| gws | 18,431 | 142% | 14.9 MiB |

## 并发吞吐（1 KiB，每协程一条连接）

GOMAXPROCS=32；每次迭代是一条连接的完整往返，`ns/op` 是聚合值。5 轮独立进程取中位数；CPU/RSS 为 3 轮 `/usr/bin/time -v` 中位数。

| 库 | 聚合 ns/op | MB/s | B/op | allocs/op | 平均 CPU | 峰值 RSS |
|---|---:|---:|---:|---:|---:|---:|
| **yd（预读）** | **1,455** | **703.76** | **0** | **0** | 1,646% | 18.2 MiB |
| **yd（同步）** | 2,309 | 443.55 | 1 | **0** | **1,209%** | **16.9 MiB** |
| gorilla | 2,360 | 433.95 | 113 | 4 | 1,231% | 21.9 MiB |
| gobwas | 2,348 | 436.15 | 64 | 4 | 1,233% | 18.8 MiB |
| coder | 2,408 | 425.55 | 1 | 0 | 1,332% | 17.9 MiB |
| gws | 2,069 | 494.81 | 33 | 2 | 1,357% | 20.1 MiB |

`DialOptions.ReadAhead` 装配内部读协程（首次 `ReadMessage` 时启动）：它预先备好下一条消息，并在调用方处理当前消息时挂起再下一次读取，从而消除「写完才发起读」的系统调用入口空档。两个复制槽按消息大小增长（单条超过 1 MiB 时不复制、直接借用读缓冲并放弃当轮预挂起），稳态 **0 B/op、0 allocs/op**；启用后 `NextReader` 返回 `ErrReadAheadActive`，控制帧处理器在内部读协程执行。读协程在终止性读错误或 `Close`/`CloseNow` 时退出。

高并发请求-响应客户端推荐启用预读；低并发或以内存最省为目标的客户端保持同步模式。同步模式仍是绝对 CPU 与 RSS 最低的实现；预读模式以更高的绝对 CPU（因为每秒完成约 40% 更多操作）换取吞吐，其 RSS 仍低于 gws。

## 流水线回显吞吐（批量写 API）

请求-响应式回显每条消息必须立刻上线，无法合并；**流水线**负载（客户端先发一批、再读回显）则可以把多条消息合并成一次传输层写入。`WriteBatch` / `WriteMessageBuffered` + `Flush` 就是为这类负载提供的 API，`BufferedRead` 让回显端在本地还有待处理输入时继续合并。

```bash
cd benchmarks/ws
go test -run '^$' -bench '^BenchmarkEchoPipelined$' -benchmem -benchtime=2s -count=5
```

x86_64 基准平台实测，1 KiB × 8 条一批，5 轮中位数：

| 模式 | ns/批 | MB/s | 相对基线 |
|---|---:|---:|---:|
| 逐条 `WriteMessage`（基线） | 29,500 | 277.70 | 1.00× |
| 仅客户端批量 | 18,138 | 451.65 | 1.63× |
| 仅服务端合并回显 | 17,559 | 466.55 | 1.68× |
| 两端都批量 | **5,997** | **1,366.11** | **4.92×** |

批量写不改变同步语义：帧在调用返回前已完整复制/发出，载荷切片不会被保留；传输层错误在 `Flush`（或 `WriteBatch` 末尾）返回。请求-响应负载继续使用 `WriteMessage`，不会被批量 API 影响。

## 结论（全部实现同表，不删弱项）

- **顺序回显**：yd 在 32 B、256 B、1 KiB、4 KiB、64 KiB 五档均为最低延迟；相对次优实现（五档均为 gorilla）分别快约 5.1%、4.7%、4.8%、5.8%、7.1%。
- **分配**：yd 是唯一在五档全部保持 `0 B/op、0 allocs/op` 的实现；coder 在 ≤1 KiB 也是零分配，但 4 KiB/64 KiB 为 2 B/2 allocs；gws 为 32 B/2 allocs（64 KiB 约 38 B/2）；gobwas 64 B/4；gorilla 112–114 B/4–6。
- **并发**：yd 预读模式最快（**1,455 ns/op、703.76 MB/s、0 allocs/op**），比 gws（2,069 ns/op、494.81 MB/s）快约 29.7%；同步模式（2,309 ns/op、443.55 MB/s）仍快于 gorilla/gobwas/coder，但落后 gws。同步模式的绝对 CPU（1,209%）与 RSS（16.9 MiB）最低；预读模式 RSS 18.2 MiB，低于 gws 的 20.1 MiB。
- **流水线**：批量写 API 在 1 KiB × 8 流水线回显上把吞吐从 277.70 MB/s 提升到 **1,366.11 MB/s（4.92×）**，且全程零分配。
- **自动调优**：读写缓冲与单条消息上限由 `ydx/sysinfo` 按 GOMAXPROCS 与内存上限推导（4–64 KiB、1–32 MiB），显式配置始终优先；客户端掩码键按 16 个一批从 CSPRNG 取用。
- 所有数值均为原始中位数；延迟对 CPU 频率、温度和后台负载敏感，跨进程波动可达约 15%，跨机比较请以同一平台的相对关系为准。

### 开发机（Windows 11 amd64）交叉复测

同一套基准在开发机（Intel i9-10980HK，16 线程，Go 1.27.2 windows/amd64）上复测，避免只在一个平台成立：

| 场景 | yd（同步） | yd（预读） | gorilla | gws |
|---|---:|---:|---:|---:|
| 并发 1 KiB，聚合 ns/op（两轮中位数） | **10,186 / 10,303** | 10,347 / 10,500 | — | 10,449 / 10,899 |
| 顺序 32 B，ns/op（两轮中位数） | **59,661 / 61,485** | — | 61,323 / 65,276 | 81,259 / 82,344 |

Windows 上两种 yd 模式均领先 gws，且保持 `0 allocs/op`（gws 为 2 allocs/op）；顺序 32 B 领先 gorilla 约 2.7%–5.8%。Windows 的 `writev` 经额外的 WSABuf 池，因此服务端帧默认走缓冲单写（`scatter_other.go`），Unix 才启用 `writev` 零拷贝分散写。
