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
混合后端。该实现不依赖反射，按固定类型路径生成 SIMD 内核，并提供与标准库一致的
std/URL/raw/填充语义。纯标量版本在 1 KiB/64 KiB 上约 **1.0 GB/s** 编码、**0.9–0.96 GB/s** 解码。

```bash
GOEXPERIMENT=simd go test -run '^$' -bench . -benchmem -count=3 -tags b64x_native
```

### 运行时选择与零寄存器溢出

运行时按 **AVX-512 + VBMI/VBMI2 → 基础 AVX-512 → AVX2 → 标量** 选择内核：

- AVX-512 + VBMI/VBMI2：编码用 `VPERMB` 汇集四个 12 字节组（48 B → 64 字符），解码用
  `VPCOMPRESSB` 压缩 48 个有效字节（64 字符 → 48 B）；32 B 还有一条精确尺寸直达路径，
  一次 32 字节加载 + 一次 `VPERMB`，不进入尾部循环。
- 基础 AVX-512（无 VBMI）：编码用两次 32 字节 gather 加载（各带 4 字节前缀）凑出 512 位
  输入，再走 512 位乘加与 LUT；解码默认交给 AVX2 内核——实测无 VBMI2 的 Xeon 上基础
  512 位比较解码只有 AVX2 的约 2/3 吞吐。
- AVX2：每轮 24 B ↔ 32 字符，尾部回退 128 位与标量。
- 标准字母表用半字节 lo/hi 查找表一次完成校验与值映射；URL 字母表使用独立的 lo/hi/roll
  LUT，热循环内没有标点分支。

对全部内核做同进程汇编检查：

```bash
GOEXPERIMENT=simd go test -c -tags b64x_native -o /tmp/b64x.test ./core/b64x
for fn in encodeSIMD encodeFirstAVX2 encodeBlocksAVX2 encodeTailAVX2 \
          encodeSIMD512 encodeSIMD512Blocks encodeSIMD512VBMI \
          decodeSIMD decodeSIMD512 decodeSIMD512VBMI2; do
  go tool objdump -s "github.com/shibingli/yd/core/b64x.$fn" /tmp/b64x.test \
    | grep -cE 'VMOV[^ ]* +[XYZ][0-9]+, .*\(SP\)|VMOV[^ ]* +.*\(SP\), [XYZ][0-9]+'
done
```

所有计数均为 **0**：常量与临时变量全部驻留 XMM/YMM/ZMM 寄存器，没有向量栈溢出。
`b64x_force_avx2` / `b64x_force_avx512` / `b64x_force_vbmi` 构建标签可在支持 AVX-512 的
机器上强制指定内核，用于跨环境对照；生产构建始终走运行时特性分发。

### 三环境实测

测试环境：

- **远程 1**：32 核 Xeon Platinum，AVX-512F/BW/CD/DQ/VL + VBMI/VBMI2/VNNI/BF16；
- **远程 2**：4 vCPU 企业级 Xeon Platinum，AVX-512F/BW/CD/DQ/VL，**无 VBMI/VBMI2**；
- **本地**：16 逻辑核 AVX2（不支持 AVX-512）。

表中 32 B 为 **ns**，其余为 **GB/s**；每格为「编码 / 解码」。全部实现均为
`0 B/op、0 allocs/op`（预分配目标缓冲）。

#### 远程 1（32 核，AVX-512 VBMI2）

| 载荷 | yd VBMI | yd AVX2（强制） | yd 基础 AVX-512（强制） | cloudwego |
|---|---:|---:|---:|---:|
| 32 B | **5.62 / 8.58 ns** | 14.7 / 18.5 ns | 15.1 / 18.9 ns | 7.44 / 12.55 ns |
| 1 KiB | **31.1 / 26.2** | 22.9 / 14.2 | 27.7 / 10.6 | 23.3 / 16.2 |
| 64 KiB | **39.8 / 34.3** | 31.0 / 18.8 | 35.4 / 13.2 | 25.9 / 20.6 |
| 1 MiB | **38.2 / 33.3** | 30.4 / 18.3 | 34.7 / 13.1 | 25.6 / 20.5 |

默认 VBMI2 路径在 32 B–1 MiB 全面领先 cloudwego：32 B 编码快约 24%、解码快约 32%；
1 KiB 编码/解码快约 34%/62%；64 KiB 与 1 MiB 编码快约 54%/49%，解码快约 67%/62%。
强制 AVX2 时，64 KiB/1 MiB 编码反超 cloudwego 约 20%（31.0 vs 25.9、30.4 vs 25.6 GB/s），
解码仍落后约 9–12%；32 B 小载荷落后约 2 倍。

#### 远程 2（4 vCPU 企业级 Xeon，无 VBMI）

| 载荷 | yd 默认（512 编 + AVX2 解） | yd AVX2（强制） | yd 基础 AVX-512（强制） | cloudwego |
|---|---:|---:|---:|---:|
| 32 B | 54.7 / 52.8 ns | 46.1 / 52.3 ns | 53.8 / 53.9 ns | 20.7 / 37.4 ns |
| 1 KiB | 7.8 / 4.3 | 6.7 / 4.3 | 7.8 / 2.8 | 8.9 / 5.4 |
| 64 KiB | **10.4** / 4.8 | 8.2 / 4.9 | 10.4 / 3.3 | 10.3 / 7.0 |
| 1 MiB | **9.7** / 4.6 | 8.0 / 4.6 | 9.5 / 3.3 | 9.6 / 6.8 |

无 VBMI 时默认分发选择「512 位 gather 编码 + AVX2 解码」：64 KiB/1 MiB 编码与 cloudwego
持平或略快，1 KiB 与 32 B 落后；解码仍落后约 30–40%。基础 512 位比较解码在该 CPU 上只有
约 3.3 GB/s，明显低于 AVX2 的 4.9 GB/s，因此默认不使用它。32 B 默认值含 512 分发入口的
额外调用开销，略高于强制 AVX2；两者都明显落后 cloudwego。

#### 本地（16 逻辑核 AVX2）

| 载荷 | yd AVX2 | cloudwego |
|---|---:|---:|
| 32 B | 38.4 / 45.0 ns | 17.4 / 28.5 ns |
| 1 KiB | 8.4 / 5.2 | 10.7 / 7.1 |
| 64 KiB | 11.1 / 6.7 | 12.6 / 8.9 |
| 1 MiB | 10.8 / 6.2 | 12.2 / 8.7 |

较老的 AVX2 桌面平台上自研内核编码落后 cloudwego 约 12–21%、解码约 25–29%（32 B
小载荷差距更大）；默认混合后端会按尺寸选择 cloudwego/cristalhq，因此生产路径不受该
实验后端影响。

关键优化：编码在 `i-4` 处一次加载 32 字节并用通道内重排汇集两个 12 字节组，主循环
2× 展开；解码先整段校验再转换，校验与转换各自的常量集都落在 16 个寄存器内，2× 顺序链
由乱序引擎重叠；尾部走掩码加载/存储，编码按预计算表写 `=` 填充；所有掩码、乘法因子、
字母表与重排表在启动时构建一次。AVX-512 编码复用同一 gather 技巧构造 512 位输入，
避免四次 128 位加载；精确 32 B 的 VBMI 路径跳过循环与掩码加载。
