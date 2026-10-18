# WebSocket 性能与兼容性基准

本目录是一个**独立 Go 模块**，用于测量 `http/ws` 的性能，并与主流 WebSocket 库做兼容性互操作验证。独立模块避免把 coder、gorilla、gobwas、gws 四个基准依赖带进主模块的 `go.mod`。

## 运行方式

```bash
cd benchmarks/ws

# 兼容性矩阵
go test -run TestCompat -v
CGO_ENABLED=1 go test -run TestCompat -race -v

# 性能基准（同进程快速检查）
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5

# 公平采样：每个实现独立进程，轮转顺序
for lib in our coder gorilla gobwas gws; do
  YD_BENCH_ONLY=$lib go test -run '^$' -bench '^BenchmarkEcho$' -benchmem -benchtime=1s -count=5
done
```

`YD_BENCH_ONLY` 是本目录基准的进程内过滤器；设置后只有名称匹配的实现会运行。主表采用「每实现独立进程 + 轮转顺序 + 5 轮中位数」，避免同进程 codec 顺序与 GC 状态带来的偏差。

## 测试平台

主表在 **mbox.yun-dev.cn**（hostname `eric-minibox-01`）上采集：

- AMD Ryzen AI MAX+ 395，16 核 32 线程，1 NUMA 节点，L3 64 MiB，30 GiB 内存；
- Ubuntu 26.04.1 LTS，Linux 7.0.0-38-generic，Go 1.27.2 linux/amd64；
- CPU governor `performance`，AVX-512F/BW/CD/DQ/VL + VBMI/VBMI2/VNNI/BF16；
- TCP 回环、单连接顺序回显、二进制载荷；每档 5 轮中位数。

CPU 利用率与峰值 RSS 使用 Linux `/usr/bin/time -v`，对每个实现单独进程测量 3 轮取中位数（2 s 计时）。CPU% 是进程平均占用，Go 运行时/GC 与并行测试会让它略高于 100%；RSS 是进程峰值常驻内存，包含 Go 运行时、测试二进制与夹具。

## 兼容性指标

18 个用例全部通过（本机与 mbox 均通过）：

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
| 32 B | **4,113 ns / 7.78 MB/s** | 4,260 / 7.51 | 4,321 / 7.41 | 5,559 / 5.76 | 6,625 / 4.83 |
| 256 B | **4,158 ns / 61.56 MB/s** | 4,341 / 58.97 | 4,579 / 55.90 | 5,644 / 45.36 | 6,636 / 38.58 |
| 1 KiB | **4,243 ns / 241.36 MB/s** | 4,448 / 230.22 | 4,694 / 218.13 | 5,735 / 178.56 | 6,905 / 148.30 |
| 4 KiB | **5,347 ns / 766.06 MB/s** | 5,515 / 742.72 | 8,482 / 482.88 | 8,899 / 460.30 | 8,536 / 479.86 |
| 64 KiB | **17,061 ns / 3,841.34 MB/s** | 18,396 / 3,562.46 | 22,484 / 2,914.72 | 38,450 / 1,704.46 | 18,229 / 3,595.14 |

### 分配（B/op / allocs/op）

| 库 | 32 B | 256 B | 1 KiB | 4 KiB | 64 KiB |
|---|---:|---:|---:|---:|---:|
| **yd** | **0 / 0** | **0 / 0** | **0 / 0** | **0 / 0** | **0 / 0** |
| gorilla | 112 / 4 | 112 / 4 | 112 / 4 | 114 / 6 | 114 / 6 |
| gobwas | 64 / 4 | 64 / 4 | 64 / 4 | 64 / 4 | 64 / 4 |
| coder | 0 / 0 | 0 / 0 | 0 / 0 | 2 / 2 | 2 / 2 |
| gws | 32 / 2 | 32 / 2 | 32 / 2 | 32 / 2 | 32 / 2 |

### 资源利用率（64 KiB 顺序回显）

| 库 | ns/op | 平均 CPU | 峰值 RSS |
|---|---:|---:|---:|
| **yd** | 17,142 | 129% | 18.7 MiB |
| gorilla | 18,276 | 122% | 19.3 MiB |
| gobwas | 22,374 | 104% | 18.6 MiB |
| coder | 39,179 | 104% | 17.1 MiB |
| gws | 18,451 | 141% | 18.1 MiB |

## 并发吞吐（1 KiB，每协程一条连接）

GOMAXPROCS=32；每次迭代是一条连接的完整往返，`ns/op` 是聚合值。5 轮独立进程取中位数。

| 库 | 聚合 ns/op | MB/s | B/op | allocs/op | 平均 CPU | 峰值 RSS |
|---|---:|---:|---:|---:|---:|---:|
| **yd** | 2,290 | 447.23 | **2** | **0** | **1,220%** | **21.1 MiB** |
| gorilla | 2,331 | 439.29 | 115 | 4 | 1,229% | 27.4 MiB |
| gobwas | 2,341 | 437.51 | 66 | 4 | 1,239% | 23.6 MiB |
| coder | 2,389 | 428.68 | 3 | 0 | 1,328% | 24.2 MiB |
| gws | **2,022** | **506.50** | 34 | 2 | 1,375% | 25.5 MiB |

## 结论（全部实现同表，不删弱项）

- **顺序回显**：yd 在 32 B、256 B、1 KiB、4 KiB、64 KiB 五档均为最低延迟；相对次优实现分别快约 3.5%、4.2%、4.6%、3.0%、6.4%（64 KiB 次优为 gws），相对 gorilla 在 64 KiB 快约 7.3%。
- **分配**：yd 是唯一在五档全部保持 `0 B/op、0 allocs/op` 的实现；coder 在 ≤1 KiB 也是零分配，但 4 KiB/64 KiB 为 2 B/2 allocs；gws 为 32 B/2 allocs；gobwas 64 B/4；gorilla 112–114 B/4–6。
- **并发**：gws 的聚合吞吐最高（2,022 ns/op、506.5 MB/s）；yd 为 2,290 ns/op、447.2 MB/s，略快于 gorilla/gobwas/coder，但**不是并发第一**。yd 的 CPU 占用（1,220%）最低，峰值 RSS（21.1 MiB）也最低；gws 的 CPU 占用（1,375%）与 RSS（25.5 MiB）最高。
- 所有数值均为原始中位数；延迟对 CPU 频率、温度和后台负载敏感，跨进程波动可达约 15%，跨机比较请以同一平台的相对关系为准。
