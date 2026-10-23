# Base64 编解码性能基准

独立 Go 模块，对比自适应 `ydx/base64` 与标准库、cloudwego/base64x、cristalhq/base64。
本轮数据由 `github.com/shibingli/ydx/base64 v0.1.8` 在参考 x86_64 主机上重新采集（2026-10-23）。

## 后端选择

| 环境 | 后端 |
|---|---|
| amd64 / arm64，默认 | 自研编解码器（amd64 在 `GOEXPERIMENT=simd` 下按 VBMI2/VBMI → 基础 AVX-512 → AVX2 选择，否则标量；arm64 NEON） |
| 其它架构，默认 | 同一套自研内核的纯 Go 标量路径 |
| 任意架构，`-tags b64x_std` | 标准库 `encoding/base64` |

默认构建不再依赖 cloudwego/base64x 或 cristalhq/base64。完整的单核/多核指标、CPU 与峰值 RSS 见
[`benchmarks/x86_64-single-multi-core.md`](../x86_64-single-multi-core.md)。

## 运行

```bash
cd benchmarks/base64

# 同进程快速检查
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5

# 公平采样：每个实现独立进程，轮转顺序
for c in stdlib cloudwego/base64x cristalhq/base64 yd-base64; do
  YD_BENCH_ONLY=$c GOEXPERIMENT=simd go test -run '^$' -bench . -benchmem \
    -benchtime=1s -count=5
done
```

`YD_BENCH_ONLY` 是本目录基准的进程内过滤器。所有编码/解码都写入**精确长度**的预分配目标缓冲（`EncodedLen`/`DecodedLen`），因此分配为 0，也不会用冗余空间掩盖越界。`TestCodecFixtures` 会先验证每个实现编码结果与标准库逐字节一致，并能解码任意对端输出。

## 测试平台

- x86_64 基准平台：AMD Ryzen AI MAX+ 395，16 核 32 线程，30 GiB，Ubuntu 26.04，Go 1.27.2，AVX-512 VBMI2；
- 单核表 `GOMAXPROCS=1`、本轮 3 轮中位数；多核表分别设置 `GOMAXPROCS=1/2/4/8/16/32`、3 轮中位数；
- CPU/RSS 使用 Linux `/usr/bin/time -v`，每实现独立进程测量。

## 单核性能（`GOMAXPROCS=1`）

32 B 为 ns/op（越低越好），其余为 GB/s（越高越好）。

### Encode

| 载荷 | stdlib | cloudwego/base64x | cristalhq/base64 | **yd-base64** |
|---|---:|---:|---:|---:|
| 32 B | 15.00 ns | **7.47 ns** | 9.57 ns | 8.80 ns |
| 1 KiB | 2.17 | 23.2 | 5.15 | **42.6** |
| 64 KiB | 2.19 | 25.8 | 5.09 | **62.5** |
| 1 MiB | 2.19 | 25.6 | 5.04 | **54.9** |

### Decode

| 载荷 | stdlib | cloudwego/base64x | cristalhq/base64 | **yd-base64** |
|---|---:|---:|---:|---:|
| 32 B | 19.36 ns | **11.51 ns** | 12.74 ns | 12.86 ns |
| 1 KiB | 2.25 | 17.0 | 3.73 | **23.7** |
| 64 KiB | 2.31 | 20.6 | 3.76 | **28.0** |
| 1 MiB | 2.30 | 20.5 | 3.73 | **26.8** |

单核结论：yd 在 1 KiB、64 KiB、1 MiB 的编码与解码两个方向均为第一；相对次优 cloudwego，
1 KiB 编码/解码快约 83.6%/39.4%，64 KiB 快约 142.2%/35.9%，1 MiB 快约 114.5%/30.7%。
**32 B 仍是 yd 的弱项，但本轮已明显收窄**：把 SIMD 阈值从 28/32 提到 64 后，
32 B 改走标量路径，编码由 10.20 降到 **8.80 ns**、解码由 14.18 降到 **12.86 ns**。
cloudwego 仍领先（7.47 / 11.51 ns，编码快约 18%、解码快约 12%）；编码已超过
cristalhq（9.57 ns），解码与 cristalhq（12.74 ns）基本持平。上一轮记录的
5.76 / 8.43 ns 仍未复现，小载荷 SIMD 内核仍是后续优化项。所有实现均为
`0 B/op、0 allocs/op`。

## 多核性能

### 并发调用（1 MiB，GB/s）

| GOMAXPROCS | 编码 yd | 编码 cloudwego | 解码 yd | 解码 cloudwego |
|---|---:|---:|---:|---:|
| 1 | 54.4 | 25.5 | 26.8 | 20.3 |
| 2 | 107.8 | 50.7 | 53.4 | 40.4 |
| 4 | 209.0 | 99.2 | 104.3 | 78.8 |
| 8 | 329.9 | 179.1 | 185.9 | 143.2 |
| 16 | **623.9** | 345.2 | **341.1** | 274.3 |
| 32 | **633.9** | 327.5 | **366.5** | 259.2 |

完整四实现对照（stdlib / cristalhq）见
[`x86_64-single-multi-core.md`](../x86_64-single-multi-core.md)。32 核资源：编码 yd
CPU 3,157%、RSS 50.7 MiB（cloudwego 3,154%、49.4 MiB）；解码 yd 3,157%、41.7 MiB
（cloudwego 3,154%、42.7 MiB）。16 核后接近内存带宽上限，继续加核收益有限。

### 单次调用自动并行（`EncodeParallel` / `DecodeParallel`）

API 不接收核心数：运行时读取当前 GOMAXPROCS，并按输入大小与 256 KiB 最小分块自动选择，单次调用最多 8 个协程；并发调用方仍可使用全部核心。

| GOMAXPROCS | Encode 1 MiB | Encode 8 MiB | Decode 1 MiB | Decode 8 MiB |
|---|---:|---:|---:|---:|
| 1 | 55.1 | 54.3 | 26.6 | 26.2 |
| 2 | 72.1 | 87.2 | 27.3 | 29.9 |
| 4 | 95.2 | 149.9 | 37.4 | 52.8 |
| 8 | 94.3 | 159.1 | 39.8 | 75.1 |
| 16 | 94.3 | **171.6** | 43.7 | 73.3 |
| 32 | **96.6** | 160.7 | **45.1** | **76.4** |

单次调用的自动并行最多使用 8 个协程，因此 1 MiB 输入在 16/32 核上收益有限；8 MiB
输入的分块更充分。并发调用方仍可使用全部核心（上一张表）。

## 运行时选择与零寄存器溢出

运行时按 **AVX-512 + VBMI/VBMI2 → 基础 AVX-512 → AVX2 → 标量** 选择内核：

- AVX-512 + VBMI/VBMI2：编码用 `VPERMB` 汇集四个 12 字节组（48 B → 64 字符），解码用 `VPCOMPRESSB` 压缩 48 个有效字节；32 B 有精确尺寸直达路径。
- 基础 AVX-512：编码用两次 32 字节 gather 凑出 512 位输入；解码为 64 字符 LUT 内核，通道内 `VPSHUFB` 压缩、`VPERMD` 拼接 48 字节，精确写入。
- AVX2：28–51 字节用 24 B 首块 + 128 位尾部；解码单遍完成校验与转换；32 B 另有精确直达内核。
- 标准/URL 字母表使用独立 LUT；URL 热循环无标点分支。

对全部内核做同进程汇编检查：

```bash
GOEXPERIMENT=simd go test -c -o /tmp/base64.test ./ydx/base64
for fn in encodeSIMD encodeFirst24AVX2 encodeFirstAVX2 encode32AVX2 \
          encodeBlocksAVX2 encodeTailAVX2 \
          encodeSIMD512 encodeSIMD512Blocks encodeSIMD512VBMI \
          decodeSIMD decodeTailAVX2 decodeSIMD512 decodeSIMD512VBMI2; do
  go tool objdump -s "github.com/shibingli/ydx/base64.$fn" /tmp/base64.test \
    | grep -cE 'VMOV[^ ]* +[XYZ][0-9]+, .*\(SP\)|VMOV[^ ]* +.*\(SP\), [XYZ][0-9]+'
done
```

所有计数均为 **0**。`b64x_force_avx2` / `b64x_force_avx512` / `b64x_force_vbmi` 可在支持 AVX-512 的机器上强制指定内核，用于跨环境对照；生产构建始终走运行时特性分发。

## 安全与兼容性

- `b64x_differential_test.go` 对 0–1024 每个长度以及 2047/2048/4095/4096/4097/8191/8192/65536 的大长度 × std/URL × raw/padded 与 `encoding/base64` 逐字节对拍，覆盖中英混排、emoji、CR/LF、控制字符、高位字节、47 种非法输入与越界写哨兵。
- 融合解码主循环的 32 字节存储受 `o+32 <= len(dst)` 约束，128 位尾部只写 12 字节；基础 512 LUT 内核分两次精确写入 48 字节，不越过 `DecodedLen`。
- VBMI2 解码主循环用 `StorePart` 精确写 48 字节；raw 字母表遇到 `=` 一律拒绝。
- 并行解码按 4 字符组切分；含 CR/LF 的分块在协程内检测并回退串行解码，非法输入与短缓冲保持串行错误语义。
- 全部内核在该平台（VBMI2）通过正确性、大长度差分与 race；本机 AVX2 以 `-gcflags=all=-d=checkptr` 复跑通过。
