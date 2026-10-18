# 压缩编解码性能基准

独立 Go 模块，对比框架可选的压缩编解码器：标准库 gzip、klauspost gzip、klauspost zstd、brotli、snappy 与 lz4。框架本身使用 klauspost gzip / zstd（HTTP 中间件）与 klauspost flate（WebSocket permessage-deflate），本基准用于验证这些选择。

## 运行

```bash
cd benchmarks/compress
go test -run '^$' -bench . -benchmem -benchtime=500ms -count=3
go test -run TestRatioAndRoundTrip -v

# 公平采样：每个实现独立进程，轮转顺序
for c in gzip/stdlib gzip/klauspost gzip/klauspost-fast zstd/default zstd/fastest \
         brotli/default brotli/q1 brotli/q4 brotli/q5 brotli/q9 snappy lz4/default; do
  YD_BENCH_ONLY=$c go test -run '^$' -bench . -benchmem -benchtime=500ms -count=3
done
```

`YD_BENCH_ONLY` 是本目录基准的进程内过滤器；主表采用「每实现独立进程 + 轮转顺序 + 3 轮中位数」。`TestRatioAndRoundTrip` 会验证每个编解码器对空载荷、单字节、文本、JSON、随机数据逐字节往返一致，并打印真实压缩率；性能表只使用通过往返校验的编解码器。

## 测试平台

主表在 **mbox.yun-dev.cn**（hostname `eric-minibox-01`）上采集：

- AMD Ryzen AI MAX+ 395，16 核 32 线程，1 NUMA 节点，L3 64 MiB，30 GiB 内存；
- Ubuntu 26.04.1 LTS，Linux 7.0.0-38-generic，Go 1.27.2 linux/amd64；
- CPU governor `performance`；
- 3 轮独立进程、500 ms 计时取中位数；流式编解码器复用，输出到 `io.Discard`。

CPU 利用率与峰值 RSS 使用 Linux `/usr/bin/time -v`，对每个实现单独进程测量 3 轮取中位数（2 s 计时，text 载荷）。CPU% 是进程平均占用；RSS 包含 Go 运行时、测试二进制与夹具。

## 载荷

- `text`：65,536 字节可压缩文本，保证合法 UTF-8（截断落在符文内部时最多少 3 字节）；
- `json`：65,555 字节的合法 JSON 记录数组；
- `random`：65,536 字节确定性不可压缩随机字节。

## 吞吐、压缩率与分配

单位：MB/s（越高越好）；压缩率为压缩后大小 / 原始大小（越低越好）。所有数值为 3 轮独立进程中位数。

### text（65,536 B）

| 编解码器 | 压缩 MB/s | 解压 MB/s | 压缩率 | B/op 压缩 | B/op 解压 |
|---|---:|---:|---:|---:|---:|
| gzip/stdlib | 1,159.6 | 8,616.1 | 0.006 | 107 | 0 |
| gzip/klauspost | 4,272.1 | 9,305.7 | 0.006 | 27 | 72 |
| gzip/klauspost-fast | 4,319.6 | 9,290.1 | 0.006 | 20 | 72 |
| zstd/default | 6,291.2 | 13,674.6 | 0.002 | 1 | 2 |
| zstd/fastest | **8,037.6** | **13,665.0** | 0.002 | 136 | 37 |
| brotli/default | 2,278.7 | 1,543.4 | 0.002 | 813 | 138,669 |
| brotli/q1 | 3,028.9 | 1,540.3 | 0.002 | 35 | 135,951 |
| brotli/q4 | 3,357.7 | 1,521.2 | 0.002 | 326 | 135,955 |
| brotli/q5 | 2,274.3 | 1,440.9 | 0.002 | 762 | 138,681 |
| brotli/q9 | 1,892.0 | 1,517.4 | 0.002 | 2,268 | 141,538 |
| snappy | **13,571.2** | 12,949.2 | 0.049 | **0** | **0** |
| lz4/default | 7,559.8 | 11,698.3 | 0.006 | **0** | **0** |

### json（65,555 B）

| 编解码器 | 压缩 MB/s | 解压 MB/s | 压缩率 | B/op 压缩 | B/op 解压 |
|---|---:|---:|---:|---:|---:|
| gzip/stdlib | 755.8 | 2,954.7 | 0.050 | 157 | 865 |
| gzip/klauspost | 1,329.5 | 3,967.8 | 0.050 | 88 | 72 |
| gzip/klauspost-fast | 2,531.3 | 3,960.9 | 0.050 | 35 | 72 |
| zstd/default | 2,175.5 | 5,680.6 | 0.024 | 5 | 6 |
| zstd/fastest | 2,317.3 | 5,676.5 | 0.024 | 418 | 89 |
| brotli/default | 215.5 | 1,874.8 | 0.023 | 16,135 | 204,351 |
| brotli/q1 | 920.2 | 1,295.1 | 0.028 | 183 | 201,625 |
| brotli/q4 | 273.8 | 1,712.2 | 0.021 | 4,141 | 204,418 |
| brotli/q5 | 236.1 | 1,869.7 | 0.023 | 15,095 | 204,363 |
| brotli/q9 | 181.6 | 1,808.2 | **0.021** | 35,562 | 215,551 |
| snappy | **6,421.6** | **9,403.5** | 0.119 | **0** | **0** |
| lz4/default | 4,143.2 | 8,923.6 | 0.075 | 116 | **0** |

### random（65,536 B）

| 编解码器 | 压缩 MB/s | 解压 MB/s | 压缩率 |
|---|---:|---:|---:|
| gzip/stdlib | 12,953.2 | 23,996.5 | 1.000 |
| gzip/klauspost | 18,555.8 | **29,686.3** | 1.000 |
| gzip/klauspost-fast | **20,824.2** | 29,645.0 | 1.000 |
| zstd/default | 8,126.0 | 16,536.4 | 1.000 |
| zstd/fastest | 13,191.8 | 16,475.3 | 1.000 |
| brotli/default | 534.6 | 5,152.9 | 1.000 |
| brotli/q1 | 2,694.0 | 5,303.2 | 1.000 |
| brotli/q4 | 652.7 | 5,154.4 | 1.000 |
| brotli/q5 | 550.5 | 4,696.0 | 1.000 |
| brotli/q9 | 323.2 | 5,103.2 | 1.000 |
| snappy | 19,329.9 | 23,949.7 | 1.000 |
| lz4/default | 8,463.2 | 10,574.2 | 1.000 |

所有压缩率在 3 位小数下均为 1.000；原始大小和压缩后大小的差异仅为协议头/帧开销。

### 资源利用率（text）

| 编解码器 | 压缩 ns/op | 压缩 CPU | 压缩 RSS | 解压 CPU | 解压 RSS |
|---|---:|---:|---:|---:|---:|
| gzip/stdlib | 56,465 | 100% | 10.7 MiB | 100% | 11.1 MiB |
| gzip/klauspost | 15,312 | 100% | 10.9 MiB | 100% | 13.6 MiB |
| gzip/klauspost-fast | 15,129 | 100% | 10.6 MiB | 100% | 13.1 MiB |
| zstd/default | 10,397 | 100% | 11.7 MiB | 100% | 11.9 MiB |
| zstd/fastest | **8,169** | 100% | 23.9 MiB | 100% | 23.1 MiB |
| brotli/default | 28,780 | 100% | 32.8 MiB | 146% | 25.1 MiB |
| brotli/q1 | 21,592 | 100% | **10.4 MiB** | 143% | 16.2 MiB |
| brotli/q4 | 19,456 | 100% | 20.1 MiB | 141% | 25.2 MiB |
| brotli/q5 | 28,806 | 100% | 30.6 MiB | 141% | 25.3 MiB |
| brotli/q9 | 34,477 | 100% | 60.6 MiB | 144% | **68.5 MiB** |
| snappy | 4,829 | 100% | **9.4 MiB** | 100% | **9.6 MiB** |
| lz4/default | 8,660 | 100% | 17.4 MiB | 100% | 17.8 MiB |

## 结论（全部实现同表，不删弱项）

- **text 压缩**：snappy 最快（13.57 GB/s），但压缩率 0.049；zstd/fastest 8.04 GB/s、压缩率 0.002，是速度与压缩率的综合最优；lz4 7.56 GB/s、0.006；klauspost gzip 4.27–4.32 GB/s、0.006；标准库 gzip 1.16 GB/s、0.006。brotli 压缩率同为 0.002，但吞吐 1.9–3.4 GB/s。
- **text 解压**：zstd 13.67 GB/s 最快，snappy 12.95 GB/s，lz4 11.70 GB/s，klauspost gzip 9.29–9.31 GB/s，标准库 gzip 8.62 GB/s；brotli 仅 1.44–1.54 GB/s，且解压 CPU 占用 141–146%。
- **json**：snappy 压缩 6.42 GB/s、解压 9.40 GB/s 最快，但压缩率 0.119；zstd/default 2.18/5.68 GB/s、0.024；brotli/q9 压缩率最好（0.021）但只有 0.18/1.81 GB/s；klauspost gzip-fast 2.53/3.96 GB/s、0.050；标准库 gzip 0.76/2.95 GB/s、0.050。
- **random**：klauspost gzip-fast/snappy 压缩约 19–21 GB/s，klauspost gzip 解压约 29.7 GB/s 最高；压缩率均为 1.000，说明不可压缩载荷只增加协议开销。
- **资源**：gzip/zstd/snappy/lz4 压缩与解压基本为单核（100%）；brotli 解压会使用多个核心（141–146%），q9 压缩峰值 RSS 60.6 MiB、解压 68.5 MiB，明显高于其他实现。zstd/fastest 与 lz4 用更高 RSS 换吞吐，snappy 的 RSS 最低。
- 框架选择 klauspost gzip/zstd 的依据是：相对标准库数倍的吞吐提升、零额外格式成本，以及与 HTTP/WebSocket 标准压缩格式的兼容性；若场景更看重压缩率而非吞吐，brotli/q9 是更低压缩率的选择，但其解压成本与内存占用都更高。
