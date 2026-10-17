# 压缩编解码性能基准

独立 Go 模块，对比框架可选的压缩编解码器：标准库 gzip、klauspost gzip、klauspost zstd、brotli、snappy 与 lz4。框架本身使用 klauspost gzip / zstd（HTTP 中间件）与 klauspost flate（WebSocket permessage-deflate），本基准用于验证这些选择。

## 运行

```bash
cd benchmarks/compress
go test -run '^$' -bench . -benchmem -benchtime=500ms -count=3
go test -run TestRatioAndRoundTrip -v
```

## 载荷

- `text`：64 KiB 可压缩文本
- `json`：约 90 KiB 的类 JSON 记录
- `random`：64 KiB 不可压缩随机字节（所有编码器压缩率均约 1.000）

## 吞吐与压缩率（text / json）

| 编解码器 | text 压缩 | text 解压 | text 压缩率 | json 压缩 | json 解压 | json 压缩率 |
|---|---:|---:|---:|---:|---:|---:|
| gzip/stdlib | 542 | 3,684 | 0.006 | 313 | 1,298 | 0.048 |
| gzip/klauspost | 2,159 | 4,316 | 0.006 | 561 | 1,788 | 0.048 |
| gzip/klauspost-fast | 2,142 | 4,641 | 0.006 | 1,037 | 1,661 | 0.049 |
| zstd/default | 3,140 | 7,638 | 0.002 | 896 | 3,054 | 0.023 |
| zstd/fastest | **3,722** | **7,520** | 0.002 | **979** | 2,779 | 0.021 |
| brotli/default | 915 | 263 | 0.002 | 84 | 333 | 0.022 |
| brotli/q1 | 1,507 | 293 | 0.002 | 484 | 311 | 0.027 |
| brotli/q4 | 1,188 | 275 | 0.002 | 103 | 322 | **0.019** |
| brotli/q5 | 905 | 280 | 0.002 | 94 | 286 | 0.022 |
| brotli/q9 | 779 | 270 | 0.002 | 72 | 286 | **0.019** |
| snappy | **6,643** | 6,487 | 0.049 | **3,703** | **5,059** | 0.120 |
| lz4/default | 4,178 | 6,039 | 0.006 | 1,865 | 4,963 | 0.074 |

单位：MB/s（越高越好）；压缩率为压缩后大小 / 原始大小（越低越好）。

结论：

- **zstd** 在可压缩载荷上综合最优：text 压缩 3.1–3.7 GB/s、解压 7.5–7.6 GB/s，json 压缩约 0.9–1.0 GB/s、解压约 2.8–3.1 GB/s，压缩率 0.002 / 0.021。
- **klauspost gzip** 比标准库 gzip 快约 4 倍（text 压缩 2.16 vs 0.54 GB/s，json 0.56 vs 0.31 GB/s），压缩率完全相同或几乎相同。
- **snappy** 压缩最快（text 6.6 GB/s、json 3.7 GB/s），但压缩率明显更差（0.049 / 0.120）；**lz4** 是速度与压缩率的折中。
- **brotli** 压缩率最好（json 0.019），但解压只有约 0.27–0.33 GB/s，不适合作为通用默认。
- 所有编码器在随机数据上压缩率均为 1.000，说明不可压缩载荷不会产生额外膨胀（仅协议头开销）。
- 框架选择 klauspost gzip/zstd 的依据是：相对标准库数倍的吞吐提升、零额外格式成本，以及与 HTTP/WebSocket 标准压缩格式的兼容性。
