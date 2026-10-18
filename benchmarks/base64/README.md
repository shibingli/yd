# Base64 编解码性能基准

独立 Go 模块，对比自适应 `core/b64x` 与标准库、cloudwego/base64x、cristalhq/base64。

## 后端选择

| 环境 | 后端 |
|---|---|
| amd64，默认 | 自研编解码器；`GOEXPERIMENT=simd` 时按 AVX-512 VBMI2/VBMI → 基础 AVX-512 → AVX2 运行时选择，否则纯 Go 标量 |
| 非 amd64，默认 | `github.com/cristalhq/base64` |
| `loong64` 或 `-tags b64x_std` | 标准库 `encoding/base64` |

默认构建不再依赖 cloudwego/base64x。完整的单核/多核指标、CPU 与峰值 RSS 见
[`benchmarks/x86_64-single-multi-core.md`](../x86_64-single-multi-core.md)。

## 运行

```bash
cd benchmarks/base64

# 同进程快速检查
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5

# 公平采样：每个实现独立进程，轮转顺序
for c in stdlib cloudwego/base64x cristalhq/base64 yd-b64x; do
  YD_BENCH_ONLY=$c GOEXPERIMENT=simd go test -run '^$' -bench . -benchmem \
    -benchtime=1s -count=5
done
```

`YD_BENCH_ONLY` 是本目录基准的进程内过滤器。所有编码/解码都写入**精确长度**的预分配目标缓冲（`EncodedLen`/`DecodedLen`），因此分配为 0，也不会用冗余空间掩盖越界。`TestCodecFixtures` 会先验证每个实现编码结果与标准库逐字节一致，并能解码任意对端输出。

## 测试平台

- x86_64 基准平台：AMD Ryzen AI MAX+ 395，16 核 32 线程，30 GiB，Ubuntu 26.04，Go 1.27.2，AVX-512 VBMI2；
- 单核表 `GOMAXPROCS=1`、5 轮中位数；多核表分别设置 `GOMAXPROCS=1/2/4/8/16/32`；
- CPU/RSS 使用 Linux `/usr/bin/time -v`，每实现独立进程测量。

## 单核性能（`GOMAXPROCS=1`）

32 B 为 ns/op（越低越好），其余为 GB/s（越高越好）。

### Encode

| 载荷 | stdlib | cloudwego/base64x | cristalhq/base64 | **yd-b64x** |
|---|---:|---:|---:|---:|
| 32 B | 14.48 ns | 7.47 ns | 9.58 ns | **5.76 ns** |
| 1 KiB | 2.4 | 23.3 | 5.2 | **34.8** |
| 64 KiB | 2.4 | 25.9 | 5.1 | **45.6** |
| 1 MiB | 2.4 | 25.6 | 5.1 | **43.6** |

### Decode

| 载荷 | stdlib | cloudwego/base64x | cristalhq/base64 | **yd-b64x** |
|---|---:|---:|---:|---:|
| 32 B | 20.17 ns | 11.60 ns | 12.43 ns | **8.43 ns** |
| 1 KiB | 2.3 | 17.1 | 3.7 | **28.4** |
| 64 KiB | 2.3 | 20.6 | 3.8 | **33.5** |
| 1 MiB | 2.3 | 20.5 | 3.7 | **31.7** |

单核结论：yd 在全部载荷、编码与解码两个方向均为第一；相对次优 cloudwego，32 B 编码/解码快约 22.9%/27.3%，1 KiB 快约 49.4%/66.1%，64 KiB 快约 76.1%/62.6%，1 MiB 快约 70.3%/54.6%。所有实现均为 `0 B/op、0 allocs/op`。

## 多核性能

### 并发调用（1 MiB，GB/s）

| GOMAXPROCS | 编码 yd | 编码 cloudwego | 解码 yd | 解码 cloudwego |
|---|---:|---:|---:|---:|
| 1 | 43.1 | 25.5 | 31.5 | 20.3 |
| 4 | 166.7 | 99.1 | 123.0 | 78.9 |
| 8 | 273.0 | 179.9 | 206.9 | 143.1 |
| 16 | **535.4** | 346.1 | **409.1** | 275.9 |
| 32 | **538.5** | 340.8 | **413.7** | 272.7 |

32 核资源：编码 yd CPU 3,175%、RSS 49.9 MiB（cloudwego 3,170%、50.4 MiB）；解码 yd 3,177%、41.1 MiB（cloudwego 3,178%、41.8 MiB）。16 核后接近内存带宽上限，继续加核不再提升。

### 单次调用自动并行（`EncodeParallel` / `DecodeParallel`）

API 不接收核心数：运行时读取当前 GOMAXPROCS，并按输入大小与 256 KiB 最小分块自动选择，单次调用最多 8 个协程；并发调用方仍可使用全部核心。

| GOMAXPROCS | Encode 1 MiB | Encode 8 MiB | Decode 1 MiB | Decode 8 MiB |
|---|---:|---:|---:|---:|
| 1 | 43.3 | 43.6 | 31.9 | 29.8 |
| 4 | **87.3** | 134.0 | 42.8 | 56.9 |
| 8 | 86.1 | 155.9 | 48.2 | **84.3** |
| 16 | 78.9 | **160.1** | 49.1 | 72.2 |
| 32 | 83.2 | 157.3 | **50.8** | 74.4 |

## 运行时选择与零寄存器溢出

运行时按 **AVX-512 + VBMI/VBMI2 → 基础 AVX-512 → AVX2 → 标量** 选择内核：

- AVX-512 + VBMI/VBMI2：编码用 `VPERMB` 汇集四个 12 字节组（48 B → 64 字符），解码用 `VPCOMPRESSB` 压缩 48 个有效字节；32 B 有精确尺寸直达路径。
- 基础 AVX-512：编码用两次 32 字节 gather 凑出 512 位输入；解码为 64 字符 LUT 内核，通道内 `VPSHUFB` 压缩、`VPERMD` 拼接 48 字节，精确写入。
- AVX2：28–51 字节用 24 B 首块 + 128 位尾部；解码单遍完成校验与转换；32 B 另有精确直达内核。
- 标准/URL 字母表使用独立 LUT；URL 热循环无标点分支。

对全部内核做同进程汇编检查：

```bash
GOEXPERIMENT=simd go test -c -o /tmp/b64x.test ./core/b64x
for fn in encodeSIMD encodeFirst24AVX2 encodeFirstAVX2 encode32AVX2 \
          encodeBlocksAVX2 encodeTailAVX2 \
          encodeSIMD512 encodeSIMD512Blocks encodeSIMD512VBMI \
          decodeSIMD decodeTailAVX2 decodeSIMD512 decodeSIMD512VBMI2; do
  go tool objdump -s "github.com/shibingli/yd/core/b64x.$fn" /tmp/b64x.test \
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
