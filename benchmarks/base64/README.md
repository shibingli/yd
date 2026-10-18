# Base64 编解码性能基准

独立 Go 模块，对比自适应 `core/b64x` 与标准库、cloudwego/base64x、cristalhq/base64。`b64x` 在 amd64 上按尺寸在 cloudwego（SIMD）与 cristalhq 之间分流，非 amd64 使用 cristalhq，loong64 或 `b64x_std` 标签回退标准库；`b64x_native` 标签启用自研 SIMD/标量内核。

## 运行

```bash
cd benchmarks/base64

# 同进程快速检查
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5

# 公平采样：每个实现独立进程，轮转顺序（自研后端）
for c in stdlib cloudwego/base64x cristalhq/base64 yd-b64x; do
  YD_BENCH_ONLY=$c GOEXPERIMENT=simd go test -run '^$' -bench . -benchmem \
    -benchtime=1s -count=5 -tags b64x_native
done
```

`YD_BENCH_ONLY` 是本目录基准的进程内过滤器；主表采用「每实现独立进程 + 轮转顺序 + 5 轮中位数」。所有编码/解码都写入**精确长度**的预分配目标缓冲（`EncodedLen`/`DecodedLen`，没有额外冗余），因此分配指标为 0，也不会用冗余空间掩盖越界问题。`TestCodecFixtures` 会先验证每个实现编码结果与标准库逐字节一致，并能解码任意对端的输出。

## 测试平台

主表在 **mbox.yun-dev.cn**（hostname `eric-minibox-01`）上采集：

- AMD Ryzen AI MAX+ 395，16 核 32 线程，1 NUMA 节点，L3 64 MiB，30 GiB 内存；
- Ubuntu 26.04.1 LTS，Linux 7.0.0-38-generic，Go 1.27.2 linux/amd64；
- CPU governor `performance`，AVX-512F/BW/CD/DQ/VL + VBMI/VBMI2/VNNI/BF16；
- 每档 5 轮独立进程中位数（强制内核对照表为 3 轮、500 ms）。

CPU 利用率与峰值 RSS 使用 Linux `/usr/bin/time -v`，对每个实现单独进程测量 3 轮取中位数（2 s 计时，64 KiB）。CPU% 是进程平均占用；RSS 包含 Go 运行时、测试二进制与夹具。

## 运行时选择与零寄存器溢出

运行时按 **AVX-512 + VBMI/VBMI2 → 基础 AVX-512 → AVX2 → 标量** 选择内核：

- AVX-512 + VBMI/VBMI2：编码用 `VPERMB` 汇集四个 12 字节组（48 B → 64 字符），解码用 `VPCOMPRESSB` 压缩 48 个有效字节（64 字符 → 48 B）；32 B 还有一条精确尺寸直达路径，一次 32 字节加载 + 一次 `VPERMB`，不进入尾部循环。URL 字母表在 ≥4096 字符时走高吞吐 512 位 LUT，其余走 AVX2 融合内核。
- 基础 AVX-512（无 VBMI）：编码用两次 32 字节 gather 加载（各带 4 字节前缀）凑出 512 位输入，再走 512 位乘加与 LUT。解码为 64 字符 LUT 内核：半字节 lo/hi 查表一次完成校验与取值映射，通道内 `VPSHUFB` 把每个 128 位通道的四个 24 位组压成 12 字节，再用跨通道 `VPERMD` 拼接为 48 字节，最后分两次精确写入，不越过 `DecodedLen`。默认分发按 4096 字符阈值分流：小输入走 AVX2 融合内核，大输入走 512 位 LUT。
- AVX2：28–51 字节用 24 B 首块（256 位）加 128 位尾部；52 字节以上保留 12 B 首块，使块循环从尾部最小的对齐位置开始。主循环每轮 24 B ↔ 32 字符并在 `i-4` 处一次加载 32 字节；32 B 另有精确直达内核——前 24 B 走 256 位块，末尾 8 B 用独立的 128 位常量集，44/43 个字符一次写完，不进入尾部循环。解码在**单遍**中完成校验与转换：输入只加载一次，校验复用取值映射所需的高半字节向量；32 字符块之后的最多 16 字符由融合尾部内核处理（raw/填充语义不变），尾部常量对所有块只加载一次。
- 标准字母表用半字节 lo/hi 查找表一次完成校验与值映射；URL 字母表使用独立的 lo/hi/roll LUT，热循环内没有标点分支。

对全部内核做同进程汇编检查：

```bash
GOEXPERIMENT=simd go test -c -tags b64x_native -o /tmp/b64x.test ./core/b64x
for fn in encodeSIMD encodeFirst24AVX2 encodeFirstAVX2 encode32AVX2 \
          encodeBlocksAVX2 encodeTailAVX2 \
          encodeSIMD512 encodeSIMD512Blocks encodeSIMD512VBMI \
          decodeSIMD decodeTailAVX2 decodeSIMD512 decodeSIMD512VBMI2; do
  go tool objdump -s "github.com/shibingli/yd/core/b64x.$fn" /tmp/b64x.test \
    | grep -cE 'VMOV[^ ]* +[XYZ][0-9]+, .*\(SP\)|VMOV[^ ]* +.*\(SP\), [XYZ][0-9]+'
done
```

所有计数均为 **0**：常量与临时变量全部驻留 XMM/YMM/ZMM 寄存器，没有向量栈溢出。`b64x_force_avx2` / `b64x_force_avx512` / `b64x_force_vbmi` 构建标签可在支持 AVX-512 的机器上强制指定内核，用于跨环境对照；生产构建始终走运行时特性分发。

## 三组内核实测（mbox，全部实现同表）

表中 32 B 为 **ns/op**，其余为 **GB/s**；每格为「编码 / 解码」，越低（32 B）或越高（其余）越好。所有实现均为 `0 B/op、0 allocs/op`。

### 默认混合后端（不带 `b64x_native`）

生产默认构建在 amd64 上按尺寸选择 cloudwego/cristalhq；本机所有档位都选中 cloudwego，因此 yd-b64x 与 cloudwego 基本一致，差异来自一次分发判断。

| 载荷 | stdlib | cloudwego/base64x | cristalhq/base64 | **yd-b64x** |
|---|---:|---:|---:|---:|
| 32 B | 14.40 / 20.13 ns | 7.53 / 11.62 ns | 9.58 / 12.40 ns | 8.18 / 12.88 ns |
| 1 KiB | 2.4 / 2.2 | 23.3 / 16.9 | 5.1 / 3.7 | 22.2 / 16.8 |
| 64 KiB | 2.4 / 2.3 | 25.8 / 20.6 | 5.1 / 3.8 | 25.8 / 20.6 |
| 1 MiB | 2.4 / 2.3 | 25.7 / 20.5 | 5.0 / 3.7 | 25.6 / 20.5 |

### 自研后端默认分发（运行时选择 VBMI2）

| 载荷 | stdlib | cloudwego/base64x | cristalhq/base64 | **yd-b64x** |
|---|---:|---:|---:|---:|
| 32 B | 14.40 / 20.13 ns | 7.53 / 11.54 ns | 9.58 / 12.40 ns | **5.35 / 8.43 ns** |
| 1 KiB | 2.4 / 2.2 | 23.3 / 16.8 | 5.1 / 3.7 | **35.4 / 28.5** |
| 64 KiB | 2.4 / 2.3 | 25.8 / 20.6 | 5.1 / 3.8 | **45.7 / 33.4** |
| 1 MiB | 2.4 / 2.3 | 25.5 / 20.5 | 5.1 / 3.7 | **43.7 / 32.2** |

默认 VBMI2 路径在 32 B–1 MiB 全面领先 cloudwego：32 B 编码/解码快约 29.0%/26.9%；1 KiB 快约 51.9%/69.6%；64 KiB 快约 77.1%/62.1%；1 MiB 快约 71.4%/57.1%。

### 强制 AVX2（`b64x_force_avx2`）

| 载荷 | stdlib | cloudwego/base64x | cristalhq/base64 | **yd-b64x AVX2** |
|---|---:|---:|---:|---:|
| 32 B | 14.34 / 20.10 ns | 7.49 / 11.59 ns | 9.58 / 12.44 ns | **7.05 / 9.97 ns** |
| 1 KiB | 2.4 / 2.2 | 23.3 / 16.8 | 5.2 / 3.7 | 21.3 / **19.5** |
| 64 KiB | 2.4 / 2.3 | 25.9 / 20.5 | 5.1 / 3.8 | **27.0 / 23.2** |
| 1 MiB | 2.4 / 2.3 | 25.6 / 20.5 | 5.0 / 3.7 | **26.8 / 22.8** |

强制 AVX2 时：32 B 编码/解码快约 6.2%/16.2%；1 KiB 解码快约 16.1%，但**编码慢约 8.6%**（21.3 vs 23.3 GB/s）；64 KiB 编码/解码快约 4.2%/13.2%；1 MiB 快约 4.7%/11.2%。

### 强制基础 AVX-512（`b64x_force_avx512`）

| 载荷 | stdlib | cloudwego/base64x | cristalhq/base64 | **yd-b64x AVX-512** |
|---|---:|---:|---:|---:|
| 32 B | 14.38 / 20.12 ns | 7.48 / 11.60 ns | 9.59 / 12.46 ns | 7.67 / **10.67 ns** |
| 1 KiB | 2.4 / 2.2 | 23.3 / 17.1 | 5.2 / 3.7 | **27.9 / 19.2** |
| 64 KiB | 2.4 / 2.3 | 25.8 / 20.6 | 5.1 / 3.8 | **35.5 / 31.6** |
| 1 MiB | 2.4 / 2.3 | 25.6 / 20.4 | 5.0 / 3.7 | **34.7 / 30.9** |

基础 AVX-512 时：32 B 解码快约 8.0%，但**编码慢约 2.5%**（7.67 vs 7.48 ns）；1 KiB 编码/解码快约 19.7%/12.3%；64 KiB 快约 37.6%/53.4%；1 MiB 快约 35.5%/51.5%。

### 资源利用率（64 KiB）

| 操作 | 实现 | ns/op | 平均 CPU | 峰值 RSS |
|---|---|---:|---:|---:|
| Encode | stdlib | 27,392 | 100% | 6.3 MiB |
| Encode | cloudwego/base64x | 2,534 | 100% | 6.0 MiB |
| Encode | cristalhq/base64 | 12,883 | 100% | 6.5 MiB |
| Encode | **yd-b64x** | **1,446** | 100% | **6.0 MiB** |
| Decode | stdlib | 28,421 | 100% | 6.8 MiB |
| Decode | cloudwego/base64x | 3,185 | 100% | 6.5 MiB |
| Decode | cristalhq/base64 | 17,477 | 100% | 6.5 MiB |
| Decode | **yd-b64x** | **1,962** | 100% | **6.3 MiB** |

所有 SIMD 内核均为单核路径，CPU 占用约 100%；峰值 RSS 与对比库同一量级，没有用额外内存换取吞吐。

默认混合后端与 cloudwego 在同一档位基本持平（本机所有尺寸都选择 cloudwego），32 B 解码因一次分发判断慢约 11%；只有启用 `b64x_native` 才获得 27%–77% 的编码/解码提升。三组内核都存在各自的落后项：AVX2 的 1 KiB 编码慢约 8.6%，基础 AVX-512 的 32 B 编码慢约 2.5%。

## 安全与兼容性

- `b64x_differential_test.go` 对 0–1024 每个长度以及 2047/2048/4095/4096/4097/8191/8192/65536 的大长度 × std/URL × raw/padded 与 `encoding/base64` 逐字节对拍，覆盖中英混排、emoji、CR/LF、控制字符、高位字节、47 种非法输入与越界写哨兵；解码永不写出目标缓冲区。
- 融合解码主循环的 32 字节存储受 `o+32 <= len(dst)` 约束，128 位尾部只写 12 字节；基础 512 LUT 内核先用通道内 `VPSHUFB` 压缩、`VPERMD` 拼接 48 字节，再分两次精确写入（`o+48 <= len(dst)`），任何路径都不会写到 `DecodedLen` 之外。
- VBMI2 解码主循环用 `StorePart` 精确写 48 字节，避免 64 字节全宽存储在富余缓冲上越过 `DecodedLen`；raw 字母表在任意长度遇到 `=` 都拒绝，交由标量路径返回 `ErrInvalidData`。
- 基础 512 LUT 内核的算法（LUT、roll、成对乘加、通道内 pack、`VPERMD` 索引）已由标量镜像对 std/URL 各 2000 个随机块验证，并在 mbox 的真实 AVX-512 上通过全模式差分、大长度差分与 race；默认分发按 4096 字符阈值启用。
- 全部内核在 mbox（VBMI2）上通过正确性、大长度差分与 race；本机 AVX2 环境以 `-gcflags=all=-d=checkptr` 复跑通过。基准工具本身也对每个实现做夹具差分，避免把错误实现带进性能表。

关键优化：编码在 `i-4` 处一次加载 32 字节并用通道内重排汇集两个 12 字节组，主循环 2× 展开；精确 32 B 走「24 B 256 位块 + 8 B 128 位块」的直达内核，两段各自加载常量，避免所有向量寄存器溢出。解码把校验与转换融合进同一遍：输入只读一次，`lo`/`hi` 半字节向量与 roll 共用，128 位尾部内核把常量加载提到块循环之外，`=` 填充与 raw 语义保持不变。基础 512 解码改用 64 字符 LUT 内核，跨通道 `VPERMD` 直接拼接 12 个有效双字，替代原先的 5 组范围比较。AVX-512 编码复用同一 gather 技巧构造 512 位输入；精确 32 B 的 VBMI 路径跳过循环与掩码加载。所有掩码、乘法因子、字母表、重排与广播常量在启动时构建一次。
