# benchmarks 总览

本目录包含若干**独立 Go 模块**，每个模块只负责一类基准，避免把该类的第三方对比依赖带进主模块的 `go.mod`。

| 模块 | 内容 | 主要对比对象 |
|---|---|---|
| [`ws/`](ws/README.md) | WebSocket 性能与兼容性 | coder、gorilla、gobwas、gws |
| [`router/`](router/README.md) | 静态/动态路由与握手开销 | 标准库 ServeMux、gin、chi、echo、httprouter、fiber |
| [`json/`](json/README.md) | JSON 编解码 | 标准库、go-json、sonic、jsoniter |
| [`base64/`](base64/README.md) | Base64 编解码 | 标准库、cloudwego/base64x、cristalhq/base64 |
| [`compress/`](compress/README.md) | 压缩吞吐与压缩率 | gzip、zstd、brotli、snappy、lz4 |
| [`logx/`](logx/README.md) | 结构化日志 | zap、zerolog、slog、logrus |

## 运行

每个模块独立运行：

```bash
cd benchmarks/<模块>
go test ./...                         # 正确性/兼容性
go test -run '^$' -bench . -benchmem  # 性能
```

## 关键结论（详见各模块 README）

- **ws**：五档载荷中 32 B/256 B 延迟最低，64 KiB 吞吐最高（572.9 MB/s）；唯一全程零分配。
- **router**：参数、深层、通配与未命中路径最快；五类路径全部零分配。静态路径仅比 httprouter 慢约 0.5 ns。
- **json**：go-json 在该结构体切片上最优；jsonx 与其 sonic 后端一致，并提供跨架构后端切换与统一错误分类。
- **base64**：≥1 KiB 时 cloudwego 与 b64x 约 10–12 GB/s 编码、7–9 GB/s 解码，远高于标准库的约 1.1 GB/s。
- **compress**：zstd 综合最优；klauspost gzip 约为标准库 gzip 的 4 倍吞吐；snappy/lz4 更快但压缩率更差。
- **logx**：启用 Info 182 ns/op、零分配，快于 zerolog；禁用路径 20 ns/op、零分配。

具体数值与测试环境见各模块 README；延迟对机器负载与温度敏感，跨进程独立运行的波动可达约 15%。
