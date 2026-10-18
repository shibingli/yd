# 国密与常密性能基准

本目录是一个**独立 Go 模块**，测量 `security/guomi` 的 SM2/SM3/SM4 实现，并与维护版 `emmansun/gmsm`、旧版 `tjfoc/gmsm` 后端以及 Go 标准库的常密算法做同平台对比。独立模块避免把对比依赖带进主模块的 `go.mod`。

## 实现与版本

| 标签 | 实现 | 说明 |
|---|---|---|
| `our` | `security/guomi`（`emmansun/gmsm v0.45.0`） | 框架封装：固定原始 C1C3C2 布局、空明文处理、可复用 SM4-GCM AEAD、SM4 CBC/ECB 加速接口探测 |
| `emmansun` | `github.com/emmansun/gmsm v0.45.0` | 维护版上游，纯 Go、无 cgo |
| `tjfoc` | `github.com/tjfoc/gmsm v1.4.1` | 切换前的旧后端，保留为「改动前」基线 |
| `stdlib` | Go 1.27.2 `crypto/*` | SHA-256、HMAC-SHA256、PBKDF2-SHA256、AES-GCM、ChaCha20-Poly1305、ECDSA P-256、Ed25519 |

`emmansun/gmsm` 使用 MIT 协议，与项目的 Apache-2.0 兼容；本次是**替换** `tjfoc/gmsm` 而非新增依赖，`go.sum` 净减少约 70 行，主模块仍未引入任何 cgo 组件。

SM2 私钥、公钥与签名的字节语义遵循 GB/T 32918；SM3 遵循 GB/T 32905-2016；SM4 遵循 GB/T 32907-2016。SM2 签名使用默认用户标识 `1234567812345678`，C1C3C2 原始布局为 `X1(32) || Y1(32) || C3(32) || C2`（无 `0x04` 前缀）。

## 运行方式

```bash
cd benchmarks/crypto

# 正确性 / 夹具校验（无基准依赖）
go test ./...

# 同进程快速检查（仅用于冒烟，不作为主表）
go test -run '^$' -bench . -benchmem -benchtime=1s

# 公平采样：每个实现独立进程、5 轮取中位数
# 基准按实现分组，YD_BENCH_ONLY 只让匹配的实现运行。
for lib in our emmansun; do
  for bench in 'BenchmarkSM3$' 'BenchmarkHMAC$' 'BenchmarkPBKDF2$' \
               'BenchmarkSM4GCM$' 'BenchmarkSM4CBC$' 'BenchmarkSM2$' 'BenchmarkSM2Encrypt$'; do
    YD_BENCH_ONLY=$lib go test -run '^$' -bench "$bench" -benchmem -benchtime=1s -count=5
  done
done

# 常密基线
for bench in 'BenchmarkSM3$' 'BenchmarkHMAC$' 'BenchmarkPBKDF2$' 'BenchmarkSM4GCM$' 'BenchmarkSM2$'; do
  YD_BENCH_ONLY=stdlib go test -run '^$' -bench "$bench" -benchmem -benchtime=1s -count=5
done

# 旧后端基线
for bench in 'BenchmarkSM3Legacy$' 'BenchmarkHMACLegacy$' 'BenchmarkPBKDF2Legacy$' \
             'BenchmarkSM4GCMLegacy$' 'BenchmarkSM2Legacy$'; do
  YD_BENCH_ONLY=tjfoc go test -run '^$' -bench "$bench" -benchmem -benchtime=1s -count=5
done
```

`YD_BENCH_ONLY` 是本目录基准的进程内过滤器；设置后只有名称匹配的实现会运行。主表采用「每实现独立进程 + 5 轮中位数」，避免同进程顺序、CPU 频率与 GC 状态带来的偏差。`our` 与 `emmansun` 的 CBC 基准都优先使用底层密码自有的加速 CBC 模式，保证比较的是同一条最优路径。

## 测试平台

主表在 **x86_64 基准平台**上采集：

- AMD Ryzen AI MAX+ 395，16 核 32 线程，1 NUMA 节点，L3 64 MiB，30 GiB 内存；
- Ubuntu 26.04.1 LTS，Linux 7.0.0-38-generic，Go 1.27.2 linux/amd64；
- CPU governor `performance`，AVX-512F/BW/CD/DQ/VL + VBMI/VBMI2/VNNI/BF16；
- `GOMAXPROCS=32`，单协程顺序调用；每个实现独立进程、5 轮中位数、`-benchmem`；
- SM3 吞吐按输入字节数计；SM4-GCM/CBC 吞吐按明文字节数计；SM2 计时包含完整的曲线运算与 KDF，不含随机数预生成。

## 结果

### SM3

每格为「ns/op / MB/s」。所有实现均对同一份确定性载荷做一次性摘要；`MB/s` 按输入字节数计算。

| 载荷 | **our** | emmansun | tjfoc（旧后端） | SHA-256 |
|---|---:|---:|---:|---:|
| 64 B | **244.0 ns / 262.3 MB/s** | 244.1 / 262.2 | — | 80.43 / 795.7 |
| 4 KiB | **7,360 ns / 556.5 MB/s** | 7,362 / 556.4 | 12,506 / 327.5 | 1,677 / 2,442.5 |
| 64 KiB | **115,821 ns / 565.8 MB/s** | 115,895 / 565.5 | 194,111 / 337.6 | 26,004 / 2,520.2 |

分配：`our`、`emmansun` 与 SHA-256 全程 `0 B/op、0 allocs/op`；tjfoc 在 4 KiB 为 `4,248 B/op、6 allocs/op`，64 KiB 为 `65,688 B/op、6 allocs/op`。SM3 没有 SHA-NI 一类硬件指令，因此 4 KiB 吞吐约为 SHA-256 的 23%。

### HMAC-SM3（256 B 消息）

| 实现 | ns/op | MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| **our（逐调用 `guomi.HMAC`）** | **1,000.0** | **256.0** | **0** | **0** |
| our-reuse（预计算密钥编排） | 730.9 | 350.3 | 0 | 0 |
| emmansun（预计算密钥编排） | 729.4 | 350.9 | 0 | 0 |
| tjfoc（预计算密钥编排） | 1,765.0 | 145.1 | 600 | 9 |
| HMAC-SHA256（预计算） | 212.2 | 1,206.4 | 0 | 0 |

`guomi.HMAC(key, data)` 是逐调用 API，每次都要重新施加密钥编排；`our-reuse` 用 `hmac.New(guomi.New, key)` 预计算一次，与上游基准的口径一致，二者相差 0.2%。HMAC-SHA256 受益于 SHA-NI，约快 4.7 倍。

### PBKDF2-HMAC-SM3（1,000 次迭代，32 字节输出）

| 实现 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| **our** | **283,344** | **740** | **10** |
| emmansun | 281,963 | 756 | 11 |
| tjfoc（旧后端） | 939,609 | 320,517 | 6,011 |
| PBKDF2-SHA256 | 114,043 | 788 | 11 |

PBKDF2 的每次迭代都是一次 HMAC；`crypto/hmac` 会在第一次 `Reset` 时把 ipad/opad 状态预先序列化，因此双方都在迭代热路径上零分配。SM3 缺少 SHA-NI，PBKDF2-SHA256 约快 2.5 倍。

### SM4-GCM（复用 AEAD）

每格为「ns/op / MB/s」，`MB/s` 按明文字节数计算。

| 载荷 / 操作 | **our** | emmansun | tjfoc（旧后端） | AES-GCM | ChaCha20-Poly1305 |
|---|---:|---:|---:|---:|---:|
| 256 B seal | **470.7 ns / 543.9 MB/s** | 472.0 / 542.4 | 3,357 / 76.25 | 100.4 / 2,549.8 | 221.8 / 1,154.2 |
| 256 B open | **443.2 ns / 577.6 MB/s** | 443.5 / 577.2 | 3,339 / 76.66 | 90.92 / 2,815.7 | 220.8 / 1,159.4 |
| 4 KiB seal | **5,409 ns / 757.3 MB/s** | 5,422 / 755.4 | 45,290 / 90.44 | 777.8 / 5,266.1 | 1,500 / 2,730.7 |
| 4 KiB open | **5,118 ns / 800.3 MB/s** | 5,152 / 795.0 | 45,336 / 90.35 | 792.6 / 5,167.8 | 1,611 / 2,542.5 |

分配：`our`、`emmansun`、AES-GCM 与 ChaCha20 均为 `1 alloc/op`（返回的输出缓冲）；tjfoc 为 `6 allocs/op`。需要零分配时可复用调用方缓冲：`aead.Seal(dst[:0], nonce, plain, aad)`。

SM4-GCM 的 `Seal`/`Open` 走底层密码通过 `crypto/cipher.gcmAble` 注册的优化实现；`our` 与 `emmansun` 的差异小于 1%。AES-GCM 受益于 AES-NI/PCLMUL，4 KiB 约快 7 倍；SM4 无对应硬件指令，但仍比旧后端快 8.4 倍。

### SM4-CBC（4 KiB，PKCS#7）

| 操作 | **our** | emmansun |
|---|---:|---:|
| encrypt | **25,454 ns / 160.9 MB/s** | 24,267 / 168.8 |
| decrypt | **3,226 ns / 1,269.7 MB/s** | 2,841 / 1,441.7 |

`our` 的加密路径包含 PKCS#7 填充并产生 `4 allocs/op`；解密路径包含输出缓冲与填充校验。两者都优先使用底层密码通过 `NewCBCEncrypter`/`NewCBCDecrypter` 暴露的多分组加速实现；CBC 加密是串行依赖链，解密可并行处理多个分组，因此解密快约 7.9 倍。

### SM2

每格为「ns/op / allocs/op」。SM2 计时包含完整的曲线运算；签名/验签包含 `SM3(ZA||M)`（默认用户标识 `1234567812345678`），加密/解密包含 KDF 与 C3 校验。

| 操作 | **our** | emmansun | tjfoc（旧后端） | ECDSA P-256 | Ed25519 |
|---|---:|---:|---:|---:|---:|
| keygen | **7,227 / 18** | 7,257 / 17 | — | 7,403 / 16 | 10,057 / 2 |
| sign | **14,364 / 61** | 14,403 / 61 | 131,572 / 108 | 17,889 / 58 | 12,104 / 1 |
| verify | **34,392 / 10** | 34,121 / 8 | 687,942 / 1,772 | 38,589 / 9 | 28,878 / 0 |
| encrypt | **35,969 / 16** | 35,854 / 14 | 715,805 / 1,790 | — | — |
| decrypt | **29,024 / 14** | 28,864 / 6 | 554,528 / 1,739 | — | — |

`our` 相对直接调用 `emmansun` 的差异（正值为 `our` 稍慢）：keygen −0.4%、sign −0.3%、verify +0.8%、encrypt +0.3%、decrypt +0.6%；分配次数分别多 1/0/2/2/8 次，来自框架层的密钥/公钥校验与原始密文的 `0x04` 前缀拼接。tjfoc 的 SM2 路径每次调用产生四位数的分配，是切换后最大的收益来源。

## 结论

- **后端切换是本轮最大的收益**：`tjfoc/gmsm v1.4.1` → `emmansun/gmsm v0.45.0` 后，SM3 4 KiB 由 12,506 ns 降到 7,360 ns（**1.70×**），64 KiB 由 194,111 ns 降到 115,821 ns（**1.68×**）；HMAC-SM3 预计算路径由 1,765 ns 降到 729 ns（**2.42×**），分配由 9 次降到 0；PBKDF2（1,000 次）由 939,609 ns 降到 283,344 ns（**3.32×**），分配由 6,011 次降到 10 次。
- **SM4-GCM**：4 KiB seal 由 45,290 ns 降到 5,409 ns（**8.37×**，90.44 → 757.3 MB/s），open 由 45,336 ns 降到 5,118 ns（**8.86×**，90.35 → 800.3 MB/s）；分配由 6 次降到 1 次。
- **SM2**：签名由 131,572 ns 降到 14,364 ns（**9.16×**），验签由 687,942 ns 降到 34,392 ns（**20.0×**），加密由 715,805 ns 降到 35,969 ns（**19.9×**），解密由 554,528 ns 降到 29,024 ns（**19.1×**）；验签分配由 1,772 次降到 10 次，加解密分配由约 1,790 次降到 16/14 次。
- **相对常密基线**：SM2 签名/验签快于 ECDSA P-256（14,364 vs 17,889 ns；34,392 vs 38,589 ns），Ed25519 的签名/验签更快（12,104 / 28,878 ns）但没有加密能力。SM3 与 SM4 没有 SHA-NI/AES-NI 对应的硬件指令，因此 SM3 4 KiB 约为 SHA-256 的 23%，SM4-GCM 4 KiB 约为 AES-GCM 的 14%、ChaCha20-Poly1305 的 28%；这是算法与指令集差异，不是封装开销。
- **封装开销**：`our` 相对直接调用 `emmansun`，SM3、SM4-GCM、SM2 签名/验签/加解密均在 **0.8% 以内**；SM4-CBC 因 PKCS#7 填充与输出缓冲多 4.9%（加密）/13.6%（解密）的耗时；`guomi.HMAC` 的逐调用 API 因重新施加密钥编排为 1,000 ns，预计算形式与上游相差 0.2%。
- **安全口径不变**：C1 先做曲线校验再解密，C3 用常量时间比较；空明文由本包显式处理；所有失败返回哨兵错误，调用方用 `errors.Is` 判定；纯 Go、无 cgo，随机数全部来自 `crypto/rand`。

## 安全性与口径

- 所有随机数来自 `crypto/rand`；测试中的固定随机数只用于可复现向量，不进入生产路径。
- SM2 解密先校验 C1 在曲线上，再用常量时间比较 C3；失败统一返回哨兵错误，调用方用 `errors.Is` 判定。
- SM4-GCM 的 nonce 绝不能在同一密钥下重复；高频路径应复用 `NewGCM` 返回的 AEAD，一次性 `SealGCM`/`OpenGCM` 适合低频调用。
- 基准数值对机器负载、温度与电源策略敏感，跨进程独立运行的波动可达约 15%。跨机比较请以同一平台的相对关系为准。
