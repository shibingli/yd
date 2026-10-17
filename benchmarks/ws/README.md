# WebSocket 性能与兼容性基准

本目录是一个**独立 Go 模块**，用于测量 `http/ws` 的性能，并与主流 WebSocket 库做兼容性互操作验证。独立模块避免把 coder、gorilla、gobwas、gws 四个基准依赖带进主模块的 `go.mod`。

## 运行方式

```bash
cd benchmarks/ws

# 兼容性矩阵
go test -run TestCompat -v
CGO_ENABLED=1 go test -run TestCompat -race -v

# 性能基准
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5
```

服务端使用普通 `http.Handler` + `ws.Upgrade`，只测 WebSocket 本身。

## 兼容性指标

18 个用例全部通过：

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

## 稳态回显性能

口径：TCP 回环、单连接顺序回显、二进制载荷、每档 `-benchtime=1s -count=5` 取中位数。

### 延迟（ns/op，越低越好）

| 载荷 | **yd** | gorilla | gobwas | coder | gws |
|---|---:|---:|---:|---:|---:|
| 32 B | **49,779** | 51,490 | 53,112 | 59,725 | 65,683 |
| 256 B | **49,760** | 51,420 | 54,433 | 60,670 | 66,256 |
| 1 KiB | 55,957 | **53,510** | 56,380 | 61,296 | 70,170 |
| 4 KiB | 63,385 | **62,127** | 96,712 | 113,026 | 77,340 |
| 64 KiB | **114,396** | 117,953 | 173,256 | 488,898 | 171,363 |

### 吞吐（MB/s，越高越好）

| 载荷 | **yd** | gorilla | gobwas | coder | gws |
|---|---:|---:|---:|---:|---:|
| 32 B | **0.64** | 0.62 | 0.60 | 0.54 | 0.49 |
| 256 B | **5.14** | 4.98 | 4.70 | 4.22 | 3.86 |
| 1 KiB | 18.30 | **19.14** | 18.16 | 16.71 | 14.59 |
| 4 KiB | 64.62 | **65.93** | 42.35 | 36.24 | 52.96 |
| 64 KiB | **572.89** | 555.61 | 378.26 | 134.05 | 382.44 |

### 分配（B/op 与 allocs/op）

| 库 | 32 B–1 KiB | 4 KiB | 64 KiB |
|---|---|---|---|
| **yd** | **0 B / 0** | **0 B / 0** | **0 B / 0** |
| gorilla | 112 B / 4 | 114 B / 6 | 114 B / 6 |
| gobwas | 64 B / 4 | 64 B / 4 | 64 B / 4 |
| coder | 760–761 B / 20 | 763 B / 22 | 762 B / 22 |
| gws | 208 B–2.1 KiB / 4 | 8.4 KiB / 4 | 152.9 KiB / 5 |

结论：32 B 与 256 B 档 yd 延迟最低；1 KiB 与 4 KiB 档 gorilla 略快约 2%–4%；64 KiB 档 yd 吞吐最高（572.9 MB/s）。yd 是唯一全程零分配的实现，相对 coder/gws 优势明显。

## 并发吞吐（16 协程 × 16 连接，1 KiB）

| 库 | 聚合 ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| **yd** | **10,060** | **101.8** | **4** | **0** |
| gorilla | 10,198 | 100.4 | 119 | 4 |

两者扩展性处于同一水平，yd 约快 1.4%，并保持零分配。

## 测试环境

- Go 1.27.2，Windows 11 Pro for Workstations，amd64
- CPU：Intel Core i9-10980HK（8 核 16 线程）
- 回环 TCP，顺序回显，单连接
- 各库使用当前最新版本与推荐低分配 API
- 延迟对机器负载、温度与电源策略敏感；跨进程独立运行的波动可达约 15%
