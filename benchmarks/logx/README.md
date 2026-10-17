# 日志性能基准

独立 Go 模块，对比零拷贝、零反射的 `core/logx` 与 zap、zerolog、slog、logrus。所有日志器都写入 `io.Discard`，输出 JSON，级别为 Info，记录包含 `method`、`status`、`ok` 三个字段。

## 运行

```bash
cd benchmarks/logx
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5
```

## 启用 Info（ns/op，越低越好）

| 日志器 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| **yd-logx** | **182** | **0** | **0** |
| zerolog | 203 | 0 | 0 |
| zap | 489 | 192 | 1 |
| slog | 782 | 0 | 0 |
| logrus | 3,798 | 1,623 | 32 |

## 禁用 Debug（低于 Info 阈值）

| 日志器 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| slog | **11** | 0 | 0 |
| **yd-logx** | **20** | **0** | **0** |
| zap | 80 | 192 | 1 |
| zerolog | 192 | 0 | 0 |
| logrus | 408 | 496 | 4 |

结论：

- **yd-logx 是最快的零分配启用路径**：182 ns/op，比 zerolog 快约 10%，比 slog 快约 4.3 倍，比 logrus 快约 21 倍；zap 每次记录仍有 1 次分配。
- 禁用路径 yd-logx 为 20 ns/op、零分配，仅慢于 slog 的 11 ns；zerolog 的链式构造使其禁用路径反而比启用路径更慢。
- 这组数据验证了 logx 的核心设计：字段联合表示避免装箱与反射、数值经 `strconv.AppendXxx` 直写、每条记录只调用一次 `Write`、禁用级别不构造字段。

## 测试环境

- Go 1.27.2，Windows 11 Pro for Workstations，amd64
- CPU：Intel Core i9-10980HK（8 核 16 线程）
- 输出目标：`io.Discard`；zap/logrus 使用 JSON 编码器，slog 使用 JSON handler，zerolog 默认 JSON
