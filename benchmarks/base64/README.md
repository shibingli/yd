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
