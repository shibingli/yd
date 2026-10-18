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
  一次 32 字节加载 + 一次 `VPERMB`，不进入尾部循环。URL 字母表在 ≥4096 字符时走高吞吐 512 位 LUT，其余走 AVX2 融合内核。
- 基础 AVX-512（无 VBMI）：编码用两次 32 字节 gather 加载（各带 4 字节前缀）凑出 512 位
  输入，再走 512 位乘加与 LUT。解码为 64 字符 LUT 内核：半字节 lo/hi 查表一次完成校验
  与取值映射，通道内 `VPSHUFB` 把每个 128 位通道的四个 24 位组压成 12 字节，再用跨通道
  `VPERMD` 拼接为 48 字节，最后分两次精确写入，不越过 `DecodedLen`。默认分发按 4096 字符
  阈值分流：小输入走 AVX2 融合内核，大输入走 512 位 LUT。
- AVX2：28–51 字节用 24 B 首块（256 位）加 128 位尾部；52 字节以上保留 12 B 首块，
  使块循环从尾部最小的对齐位置开始。主循环每轮 24 B ↔ 32 字符并在 `i-4` 处一次加载
  32 字节；32 B 另有精确直达内核——前 24 B 走 256 位块，末尾 8 B 用独立的 128 位
  常量集，44/43 个字符一次写完，不进入尾部循环。解码在**单遍**中完成校验与转换：输入
  只加载一次，校验复用取值映射所需的高半字节向量；32 字符块之后的最多 16 字符由融合
  尾部内核处理（raw/填充语义不变），尾部常量对所有块只加载一次。32 B 载荷（44 字符）
  不再回退标量。常量以表的形式存放，热内核一条加载指令取一个。
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
| 32 B | **5.65 / 8.67 ns** | 7.04 / 9.97 ns | 7.63 / 10.76 ns | 7.43 / 12.55 ns |
| 1 KiB | **35.1 / 27.7** | 21.2 / 19.6 | 27.8 / 18.4 | 23.3 / 16.2 |
| 64 KiB | **45.7 / 33.4** | 27.0 / 23.2 | 35.5 / 32.1 | 25.9 / 20.6 |
| 1 MiB | **43.4 / 32.4** | 26.7 / 22.8 | 34.8 / 31.4 | 25.6 / 20.5 |

默认 VBMI2 路径在 32 B–1 MiB 全面领先 cloudwego：32 B 编码/解码快约 24%/31%；
1 KiB 编码/解码快约 51%/71%；64 KiB 编码/解码快约 77%/62%；1 MiB 快约 70%/58%。
强制 AVX2 时 32 B 编码/解码快约 5%/21%，1 KiB 解码快约 21%，64 KiB/1 MiB 编码/
解码都快约 4–13%。基础 AVX-512 在 64 KiB/1 MiB 编码快约 37%/36%、解码快约 56%/53%；
由于 4096 字符阈值，1 KiB 解码走 AVX2。

#### 远程 2（4 vCPU 企业级 Xeon，无 VBMI）

| 载荷 | yd 默认（512 编 + 自适应解） | yd AVX2（强制） | yd 基础 AVX-512（强制） | cloudwego |
|---|---:|---:|---:|---:|
| 32 B | 23.6 / 32.8 ns | 22.4 / 31.4 ns | 23.8 / 32.9 ns | 20.8 / 37.4 ns |
| 1 KiB | 7.8 / 5.8 | 6.5 / 5.9 | 7.6 / 5.8 | 8.9 / 5.4 |
| 64 KiB | **10.4 / 7.6** | 8.2 / 6.4 | 10.4 / 7.6 | 10.4 / 7.0 |
| 1 MiB | **9.9 / 7.3** | 8.0 / 6.4 | 9.7 / 7.3 | 9.7 / 6.9 |

无 VBMI 时默认分发为「512 位 gather 编码 + 尺寸自适应解码」：32 B/1 KiB 走 AVX2 融合，
≥4096 字符走 512 位 LUT。解码在 32 B–1 MiB 全面快于 cloudwego（32 B 32.8 vs 37.4 ns；
64 KiB 7.6 vs 7.0 GB/s；1 MiB 7.3 vs 6.9 GB/s）；编码在 64 KiB/1 MiB 与 cloudwego
持平或略快，32 B/1 KiB 分别慢约 13%/13%。

#### 本地（16 逻辑核 AVX2）

| 载荷 | yd AVX2 | cloudwego |
|---|---:|---:|
| 32 B | 18.3 / 25.8 ns | 16.2 / 30.0 ns |
| 1 KiB | 8.4 / 6.9 | 11.2 / 6.5 |
| 64 KiB | 11.2 / 8.3 | 12.8 / 8.3 |
| 1 MiB | 10.2 / 7.8 | 11.7 / 8.3 |

较老的 AVX2 桌面平台上，32 B 解码快约 14%、编码慢约 13%；1 KiB 编码慢约 25%、解码
快约 6%；64 KiB 解码持平、编码慢约 13%；1 MiB 编码慢约 13%、解码慢约 7%。默认混合
后端会按尺寸选择 cloudwego/cristalhq，因此生产路径不受该实验后端影响。

### 安全与兼容性

- `b64x_differential_test.go` 对 0–1024 每个长度以及 2047/2048/4095/4096/4097/
  8191/8192/65536 的大长度 × std/URL × raw/padded 与 `encoding/base64` 逐字节对拍，
  覆盖中英混排、emoji、CR/LF、控制字符、高位字节、47 种非法输入与越界写哨兵；
  解码永不写出目标缓冲区。
- 融合解码主循环的 32 字节存储受 `o+32 <= len(dst)` 约束，128 位尾部只写 12 字节；
  基础 512 LUT 内核先用通道内 `VPSHUFB` 压缩、`VPERMD` 拼接 48 字节，再分两次精确
  写入（`o+48 <= len(dst)`），任何路径都不会写到 `DecodedLen` 之外。
- VBMI2 解码主循环用 `StorePart` 精确写 48 字节，避免 64 字节全宽存储在富余缓冲上
  越过 `DecodedLen`；raw 字母表在任意长度遇到 `=` 都拒绝，交由标量路径返回
  `ErrInvalidData`。
- 基础 512 LUT 内核的算法（LUT、roll、成对乘加、通道内 pack、`VPERMD` 索引）已由
  标量镜像对 std/URL 各 2000 个随机块验证，并在远程 1/2 的真实 AVX-512 上通过
  全模式差分、大长度差分与 race；默认分发按 4096 字符阈值启用。
- 全部内核在远程 1（VBMI2）与远程 2（无 VBMI）上通过正确性、大长度差分与 race；
  本地以 `-gcflags=all=-d=checkptr` 复跑通过。

关键优化：编码在 `i-4` 处一次加载 32 字节并用通道内重排汇集两个 12 字节组，主循环
2× 展开；精确 32 B 走「24 B 256 位块 + 8 B 128 位块」的直达内核，两段各自加载常量，
避免所有向量寄存器溢出。解码把校验与转换融合进同一遍：输入只读一次，`lo`/`hi` 半字节
向量与 roll 共用，128 位尾部内核把常量加载提到块循环之外，`=` 填充与 raw 语义保持
不变。基础 512 解码改用 64 字符 LUT 内核，跨通道 `VPERMD` 直接拼接 12 个有效双字，
替代原先的 5 组范围比较。AVX-512 编码复用同一 gather 技巧构造 512 位输入；精确 32 B
的 VBMI 路径跳过循环与掩码加载。所有掩码、乘法因子、字母表、重排与广播常量在启动时
构建一次。
