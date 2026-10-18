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
- AVX2：28–51 字节用 24 B 首块（256 位）加 128 位尾部；52 字节以上保留 12 B 首块，
  使块循环从尾部最小的对齐位置开始。主循环每轮 24 B ↔ 32 字符并在 `i-4` 处一次加载
  32 字节；32 B 另有精确直达内核——前 24 B 走 256 位块，末尾 8 B 用独立的 128 位
  常量集，44/43 个字符一次写完，不进入尾部循环。解码先整段校验再转换，32 字符块之后
  最多 16 字符由 128 位尾部内核融合校验与转换（含 `=` 填充与 raw 语义），32 B 载荷
  （44 字符）不再回退标量。常量以表的形式存放，热内核一条加载指令取一个。
- 标准字母表用半字节 lo/hi 查找表一次完成校验与值映射；URL 字母表使用独立的 lo/hi/roll
  LUT，热循环内没有标点分支。

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
| 32 B | **5.56 / 8.60 ns** | 7.04 / 11.9 ns | 7.63 / 12.5 ns | 7.44 / 12.55 ns |
| 1 KiB | **35.2 / 27.3** | 23.0 / 16.1 | 27.8 / 10.6 | 23.3 / 16.2 |
| 64 KiB | **45.9 / 33.4** | 27.0 / 19.2 | 35.6 / 13.3 | 25.8 / 20.6 |
| 1 MiB | **43.4 / 32.0** | 26.9 / 18.7 | 34.6 / 13.2 | 25.6 / 20.5 |

默认 VBMI2 路径在 32 B–1 MiB 全面领先 cloudwego：32 B 编码/解码快约 25%/32%；
1 KiB 编码/解码快约 51%/69%；64 KiB 编码/解码快约 78%/62%；1 MiB 快约 70%/56%。
强制 AVX2 时 32 B 编码/解码也快约 5%，1 KiB 与 cloudwego 持平，64 KiB/1 MiB 编码
快约 5%、解码慢约 7–9%。基础 AVX-512 编码在 1 KiB–1 MiB 快约 19–38%，但基础 512
比较解码慢约 35%，因此无 VBMI 主机默认用 AVX2 解码。

#### 远程 2（4 vCPU 企业级 Xeon，无 VBMI）

| 载荷 | yd 默认（512 编 + AVX2 解） | yd AVX2（强制） | yd 基础 AVX-512（强制） | cloudwego |
|---|---:|---:|---:|---:|
| 32 B | 23.6 / 37.2 ns | 22.4 / 37.2 ns | 23.9 / 38.5 ns | 20.8 / 37.5 ns |
| 1 KiB | 7.8 / 4.7 | 6.7 / 4.7 | 7.8 / 2.8 | 8.5 / 5.4 |
| 64 KiB | **10.4** / 4.8 | 8.2 / 4.9 | 10.4 / 3.3 | 10.4 / 7.0 |
| 1 MiB | **9.7** / 4.7 | 8.0 / 4.7 | 9.7 / 3.3 | 9.7 / 6.9 |

无 VBMI 时默认分发选择「512 位 gather 编码 + AVX2 解码」：32 B 解码与 cloudwego 持平
（37.2 vs 37.5 ns），编码慢约 13%；1 KiB 编码/解码分别慢约 8%/13%；64 KiB/1 MiB 编码
持平或略快，解码慢约 30%。基础 512 位比较解码只有约 3.3 GB/s，明显低于 AVX2 的 4.7 GB/s，
因此默认不使用它。

#### 本地（16 逻辑核 AVX2）

| 载荷 | yd AVX2 | cloudwego |
|---|---:|---:|
| 32 B | 17.5 / 29.5 ns | 17.1 / 28.9 ns |
| 1 KiB | 8.1 / 5.8 | 10.2 / 7.0 |
| 64 KiB | 10.8 / 6.8 | 13.1 / 9.0 |
| 1 MiB | 10.2 / 6.2 | 11.8 / 8.6 |

较老的 AVX2 桌面平台上，32 B 已与 cloudwego 基本持平（编码/解码约慢 2–3%）；1 KiB
编码慢约 20%、解码慢约 17%；64 KiB/1 MiB 编码慢约 13–17%、解码慢约 24–28%。默认混合
后端会按尺寸选择 cloudwego/cristalhq，因此生产路径不受该实验后端影响。

### 安全与兼容性

- `b64x_differential_test.go` 对 0–1024 每个长度 × std/URL × raw/padded 与
  `encoding/base64` 逐字节对拍，覆盖中英混排、emoji、CR/LF、控制字符、高位字节、
  47 种非法输入与越界写哨兵；解码永不写出目标缓冲区。
- VBMI2 解码主循环用 `StorePart` 精确写 48 字节，避免 64 字节全宽存储在富余缓冲上
  越过 `DecodedLen`；raw 字母表在任意长度遇到 `=` 都拒绝，交由标量路径返回
  `ErrInvalidData`。
- 全部内核在远程 VBMI2 与本地 AVX2 上通过正确性与 race；编解码均为 0 分配。

关键优化：编码在 `i-4` 处一次加载 32 字节并用通道内重排汇集两个 12 字节组，主循环
2× 展开；精确 32 B 走「24 B 256 位块 + 8 B 128 位块」的直达内核，两段各自加载常量，
避免所有向量寄存器溢出。解码先整段校验再转换，校验与转换各自的常量集都落在 16 个寄存器
内，2× 顺序链由乱序引擎重叠；32 字符块之后的最多 16 字符由 128 位尾部内核处理——它的
转换窗口截止到该块最后一个完整四元组，所以加载始终在 `src` 之内且天然按 128 位组对齐，
`=` 填充与 raw 语义直接在 SIMD 内完成。所有掩码、乘法因子、字母表、重排与广播常量在
启动时构建一次。AVX-512 编码复用同一 gather 技巧构造 512 位输入，避免四次 128 位加载；
精确 32 B 的 VBMI 路径跳过循环与掩码加载。
