# benchmarks 总览

本目录包含若干**独立 Go 模块**，每个模块只负责一类基准，避免把该类的第三方对比依赖带进主模块的 `go.mod`。

x86_64 单核/多核的完整指标（含全部对比库、CPU 与峰值 RSS）见
[`x86_64-single-multi-core.md`](x86_64-single-multi-core.md)。

| 模块 | 内容 | 主要对比对象 |
|---|---|---|
| [`ws/`](ws/README.md) | WebSocket 性能与兼容性 | coder、gorilla、gobwas、gws |
| [`router/`](router/README.md) | 静态/动态路由与握手开销 | 标准库 ServeMux、gin、chi、echo、httprouter、fiber |
| [`json/`](json/README.md) | JSON 编解码 | 标准库、go-json、sonic、jsoniter |
| [`base64/`](base64/README.md) | Base64 编解码 | 标准库、cloudwego/base64x、cristalhq/base64 |
| [`compress/`](compress/README.md) | 压缩吞吐与压缩率 | gzip、zstd、brotli、snappy、lz4 |
| [`logx/`](logx/README.md) | 结构化日志 | zap、zerolog、slog、logrus |

## 测试平台（主表统一口径）

所有主表在 **x86_64 基准平台**上采集：

- AMD Ryzen AI MAX+ 395，16 核 32 线程，1 NUMA 节点，L3 64 MiB，30 GiB 内存；
- Ubuntu 26.04.1 LTS，Linux 7.0.0-38-generic，Go 1.27.2 linux/amd64；
- CPU governor `performance`，AVX-512F/BW/CD/DQ/VL + VBMI/VBMI2/VNNI/BF16；
- 每个实现单独进程运行、轮转顺序、多轮中位数；
- 每个模块的 `YD_BENCH_ONLY` 环境变量用于选择单一实现，避免同进程 codec 顺序与 GC 偏差；
- 库本身不接收核心数/内存参数：`core/sysinfo` 在运行时读取 GOMAXPROCS 与 cgroup/主机内存，自动决定并行度与缓冲上限；单核/多核测量只通过 `GOMAXPROCS` 模拟环境。

CPU 利用率与峰值 RSS 使用 Linux `/usr/bin/time -v`，对每个实现单独进程测量 3 轮取中位数。CPU% 是进程平均占用，多线程/RunParallel 场景可远高于 100%；RSS 是进程峰值常驻内存，包含 Go 运行时、测试二进制与夹具。具体计时参数见各模块 README。

## 运行

每个模块独立运行：

```bash
cd benchmarks/<模块>
go test ./...                         # 正确性/兼容性/夹具校验
go test -run '^$' -bench . -benchmem  # 同进程快速检查

# 公平采样（以 base64 为例；amd64 默认即自研内核）
for c in stdlib cloudwego/base64x cristalhq/base64 yd-b64x; do
  YD_BENCH_ONLY=$c GOEXPERIMENT=simd go test -run '^$' -bench . -benchmem \
    -benchtime=1s -count=5
done
```

## 关键结论（详见各模块 README，全部实现均在同一张表中）

- **ws**：顺序回显在 32 B/256 B/1 KiB/4 KiB/64 KiB 五档均为最低延迟（64 KiB 17,061 ns、3,841 MB/s），唯一全程 `0 B/op、0 allocs/op`；并发 1 KiB 下 gws 最快（2,022 ns、506.5 MB/s），yd 为 2,290 ns、447.2 MB/s 且 CPU/RSS 最低。兼容性 18/18。
- **router**：param/deep/wildcard/miss 四类最快（1.1–1.6 ns），五类路径零分配；静态路径 httprouter 最快（0.9 vs yd 1.1 ns）；WebSocket 握手经 router 增加约 15 ns（4.2%）。
- **json**：amd64 默认已是自研后端。单核 n=100 的 Marshal/Unmarshal 第一（比 go-json 快约 3.4%/4.5%）；n=1 与 n=1000 的 Marshal 慢约 1.1%/1.3%，n=1/n=1000 的 Unmarshal 慢约 10.4%/2.1%。多核 Marshal 在 2–16 核领先（16 核 9,907 MB/s），32 核被 go-json 反超约 20%；Unmarshal 在 8–32 核领先（32 核 5,858 vs 3,087 MB/s），且 RSS 64.3 vs 115.3 MiB。嵌入字段支配规则、omitzero、解码 `,string`、深度上限 10000、Unicode/自定义接口兼容与差分/fuzz 全部通过。
- **base64**：amd64 默认已是自研内核。单核 32 B–1 MiB 编码/解码全部第一（32 B 5.76/8.43 ns，64 KiB 45.6/33.5 GB/s）；并发调用 1–32 核全部第一（16 核 535.4/409.1 GB/s），16 核后接近内存带宽上限。`EncodeParallel`/`DecodeParallel` 不接收核心数，按 GOMAXPROCS 与输入大小自动选择；8 MiB 编码在 16 核达 160.1 GB/s。三套内核零向量寄存器溢出；全长度+大长度差分、越界哨兵、race 通过。
- **compress**：zstd/fastest text 压缩 8.04 GB/s、压缩率 0.002，zstd 解压 13.67 GB/s；snappy text 压缩 13.57 GB/s 但压缩率 0.049；klauspost gzip 约标准库 gzip 的 3.7 倍吞吐，压缩率相同；brotli/q9 json 压缩率最好（0.021）但吞吐与内存成本最高。
- **logx**：dynamic Info 111.7 ns/op、零分配，略快于 zerolog（112.9 ns）；prebound Info 为 103.1 ns，**慢于 zerolog 的 94.9 ns 约 8.6%**；禁用路径 prebound 为 1.8 ns、零分配，与 logrus 并列最快，dynamic 为 7.9 ns（slog 5.1 ns 更快）。

基准数值对机器负载、温度与电源策略敏感，跨进程独立运行的波动可达约 15%。跨机比较请以同一平台的相对关系为准。
