# 日志性能基准

独立 Go 模块，对比零拷贝、零反射的 `core/logx` 与 zap、zerolog、slog、logrus。所有日志器都写入 `io.Discard`，输出 JSON，级别为 Info，记录包含 `method`、`status`、`ok` 三个字段。

## 运行

```bash
cd benchmarks/logx
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5

# 公平采样：每个实现独立进程，轮转顺序
for l in yd-logx zap zerolog slog logrus; do
  YD_BENCH_ONLY=$l go test -run '^$' -bench . -benchmem -benchtime=1s -count=5
done
```

`YD_BENCH_ONLY` 是本目录基准的进程内过滤器；主表采用「每实现独立进程 + 轮转顺序 + 5 轮中位数」。每个日志器同时提供两种用法：

- **逐调用字段（dynamic）**：每次调用重新构造 `method/status/ok`，代表字段随请求变化的常见写法；
- **预绑定字段（prebound）**：三个静态字段只绑定一次，代表固定上下文（如服务名、实例、请求常量）的推荐写法。

两种用法都测，避免只挑对某一实现有利的口径。

## 测试平台

主表在 **mbox.yun-dev.cn**（hostname `eric-minibox-01`）上采集：

- AMD Ryzen AI MAX+ 395，16 核 32 线程，1 NUMA 节点，L3 64 MiB，30 GiB 内存；
- Ubuntu 26.04.1 LTS，Linux 7.0.0-38-generic，Go 1.27.2 linux/amd64；
- CPU governor `performance`；
- 每档 5 轮独立进程中位数。

CPU 利用率与峰值 RSS 使用 Linux `/usr/bin/time -v`，对每个实现单独进程测量 3 轮取中位数（2 s 计时，prebound Info）。CPU% 是进程平均占用；RSS 包含 Go 运行时、测试二进制与夹具。所有日志器都包含时间戳与 JSON 编码器。

## 启用 Info（ns/op，越低越好）

| 日志器 | dynamic ns/op | dynamic B/op / allocs | prebound ns/op | prebound B/op / allocs |
|---|---:|---:|---:|---:|
| **yd-logx** | **111.7** | **0 / 0** | 103.1 | **0 / 0** |
| zerolog | 112.9 | **0 / 0** | **94.9** | **0 / 0** |
| zap | 266.9 | 193 / 1 | 160.2 | 0 / 0 |
| slog | 404.9 | **0 / 0** | 261.3 | **0 / 0** |
| logrus | 1,977.0 | 1,633 / 32 | 1,697.0 | 1,129 / 28 |

## 禁用 Debug（低于 Info 阈值，ns/op，越低越好）

| 日志器 | dynamic ns/op | dynamic B/op / allocs | prebound ns/op | prebound B/op / allocs |
|---|---:|---:|---:|---:|
| **yd-logx** | 7.9 | **0 / 0** | **1.8** | **0 / 0** |
| slog | **5.1** | **0 / 0** | 4.5 | **0 / 0** |
| logrus | 199.0 | 496 / 4 | **1.8** | **0 / 0** |
| zap | 35.9 | 192 / 1 | 3.5 | **0 / 0** |
| zerolog | 113.5 | **0 / 0** | 94.7 | **0 / 0** |

## 资源利用率（prebound Info）

| 日志器 | ns/op | 平均 CPU | 峰值 RSS |
|---|---:|---:|---:|
| **yd-logx** | 102.8 | 100% | 14.3 MiB |
| zerolog | **92.7** | 100% | 14.2 MiB |
| zap | 159.2 | 100% | 14.4 MiB |
| slog | 264.1 | 100% | 14.3 MiB |
| logrus | 1,637.0 | 120% | 18.3 MiB |

## 结论（全部实现同表，不删弱项）

- **dynamic Info**：yd-logx 111.7 ns/op，zerolog 112.9 ns/op，yd 快约 1.1%，属于接近噪声的微弱优势；两者都是零分配。zap 266.9 ns/op 且有 1 次分配；slog 404.9 ns/op；logrus 1,977 ns/op、32 次分配。
- **prebound Info**：zerolog 最快（94.9 ns/op），yd-logx 103.1 ns/op，**慢约 8.6%**；zap 160.2 ns/op、slog 261.3 ns/op、logrus 1,697 ns/op。预绑定后 zerolog/yd/zap/slog 都为零分配，logrus 仍有 28 次分配。
- **禁用路径**：prebound 时 yd-logx 与 logrus 并列最快（1.8 ns/op，零分配），zap 3.5 ns、slog 4.5 ns、zerolog 94.7 ns；dynamic 时 slog 最快（5.1 ns），yd 7.9 ns，zap 35.9 ns，zerolog 113.5 ns，logrus 199 ns。zerolog 的链式构造在禁用路径上仍需构建事件，因此明显慢于直接级别判断。
- **资源**：prebound Info 下 yd/zerolog/zap/slog 峰值 RSS 约 14.2–14.4 MiB，logrus 18.3 MiB；CPU 占用都约为单核（logrus 120%）。RSS 包含约 14 MiB 的 Go 运行时基线，库间差异主要体现在分配行为。
- 这组数据验证了 logx 的核心设计：字段联合表示避免装箱与反射、数值经 `strconv.AppendXxx` 直写、每条记录只调用一次 `Write`、禁用级别不构造字段。它并非在所有口径下都第一：固定字段预绑定时 zerolog 更快，禁用路径上 slog/logrus 的预绑定写法更便宜。
