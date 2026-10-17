# Base64 编解码性能基准

独立 Go 模块，对比自适应 `core/b64x` 与标准库、cloudwego/base64x、cristalhq/base64。`b64x` 在 amd64 上按尺寸在 cloudwego（SIMD）与 cristalhq 之间分流，非 amd64 使用 cristalhq，loong64 或 `b64x_std` 标签回退标准库。

## 运行

```bash
cd benchmarks/base64
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5
```

所有编码/解码都写入预分配目标缓冲，因此分配指标为 0，差异只反映算法本身。

## Encode（MB/s，越高越好）

| 载荷 | stdlib | cloudwego/base64x | cristalhq/base64 | **yd-b64x** |
|---|---:|---:|---:|---:|
| 32 B | 1,017 | **1,833** | 1,415 | 1,700 |
| 1 KiB | 1,154 | **10,546** | 2,389 | 10,324 |
| 64 KiB | 1,143 | **12,130** | 2,257 | 12,018 |
| 1 MiB | 1,161 | **11,639** | 2,410 | 11,672 |

## Decode（MB/s，越高越好）

| 载荷 | stdlib | cloudwego/base64x | cristalhq/base64 | **yd-b64x** |
|---|---:|---:|---:|---:|
| 32 B | 713 | 1,123 | **1,238** | 1,123 |
| 1 KiB | 1,073 | 6,975 | 1,889 | **7,209** |
| 64 KiB | 1,114 | 9,003 | 1,879 | **9,066** |
| 1 MiB | 1,111 | 8,668 | 1,918 | **8,776** |

## 分配

全部实现均为 `0 B/op、0 allocs/op`（预分配目标缓冲）。

结论：≥1 KiB 时 cloudwego 与 yd-b64x 明显领先，编码约 10–12 GB/s、解码约 7–9 GB/s；标准库约 1.1 GB/s，cristalhq 约 1.9–2.4 GB/s。32 B 小载荷上 yd-b64x 走小尺寸路径，性能接近 cloudwego 并高于 cristalhq 与标准库。`b64x` 的混合分流使小尺寸不必承担 SIMD 路径的固定开销，同时在大尺寸保留 cloudwego 的吞吐。

## 自研原生后端（实验性）

通过 `-tags b64x_native` 启用 `core/b64x` 的自研实现；默认构建仍使用 cloudwego/cristalhq
混合后端。纯标量版本在 1 KiB/64 KiB 上约 **1.0 GB/s** 编码、**0.9–0.96 GB/s** 解码。

### SIMD（GOEXPERIMENT=simd）

```bash
GOEXPERIMENT=simd go test -run '^$' -bench . -benchmem -count=3 -tags b64x_native
```

运行时按 **AVX-512 + VBMI/VBMI2 → AVX-512 → AVX2 → 标量** 选择内核：

- AVX-512 + VBMI/VBMI2：编码用 `VPERMB` 汇集四个 12 字节组（48 B → 64 字符），解码用
  `VPCOMPRESSB` 压缩 48 个有效字节（64 字符 → 48 B）；
- AVX-512：每轮 48 B ↔ 64 字符，四通道分别收尾；
- AVX2：每轮 24 B ↔ 32 字符，尾部回退 128 位与标量；
- 标准字母表用半字节 lo/hi 查找表一次完成校验与值映射，URL 字母表走比较路径。

在 AVX-512 主机（32 核 Ubuntu，Go 1.27.2）上与 cloudwego/base64x 同机对比：

| 载荷 | cloudwego 编码/解码 | **yd-b64x 编码/解码** |
|---|---:|---:|
| 32 B | 7.4 / 12.5 ns（4.3 / 2.6 GB/s） | **7.5 / 9.3 ns（4.3 / 3.4 GB/s）** |
| 1 KiB | 43.9 / 63.2 ns（23.3 / 16.2 GB/s） | **32.5 / 37.0 ns（31.5 / 27.7 GB/s）** |
| 64 KiB | 2,530 / 3,186 ns（25.9 / 20.6 GB/s） | **1,668 / 1,908 ns（39.3 / 34.4 GB/s）** |
| 1 MiB | 40.9 / 51.4 µs（25.6 / 20.4 GB/s） | **28.2 / 34.7 µs（37.0 / 30.1 GB/s）** |

结论：AVX-512 主机上 32 B–1 MiB 四档整体超过 cloudwego/base64x：32 B 编码 7.5 vs 7.4 ns
（基本持平）、解码 9.3 vs 12.5 ns；1 KiB 吞吐高约 35%/71%；64 KiB 以上高约 52%/67%。
默认构建的混合后端仍按尺寸选择 cloudwego/cristalhq，因此生产路径不受影响。AVX2 主机上
自研内核约 4.7–5.3 GB/s 编码、2.3–2.5 GB/s 解码，仍低于 cloudwego 的 AVX2 内核；该后端
保持实验性质，需显式构建标签启用。

小载荷的关键优化：完整块之外的尾部同样走掩码加载（`LoadUint8x64Part`/`StorePart`），
32 B 编码不再回退标量；编码尾部按预计算表写入 `=` 填充，解码尾部把 `=` 临时映射为 `A`
后向量解码并按填充数截断；所有掩码、乘法因子、字母表与重排表在启动时构建一次。
