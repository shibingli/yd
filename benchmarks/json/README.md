# JSON 编解码性能基准

独立 Go 模块，对比自适应 `core/jsonx` 与标准库及主流 Go JSON 库。`jsonx` 在 amd64 上使用 sonic `ConfigStd`，非 amd64 使用 go-json，`jsonx_std` 标签回退标准库。

## 运行

```bash
cd benchmarks/json
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5
```

## 数据集

同一份 `record` 切片，分别包含 1、100、1000 条记录，字段覆盖 int、string、bool、float64、[]string 与 time.Time。

## Marshal（ns/op，越低越好）

| 记录数 | stdlib | go-json | sonic-std | jsoniter-compat | **yd-jsonx** |
|---|---:|---:|---:|---:|---:|
| 1 | 838 | **283** | 631 | 524 | 704 |
| 100 | 74,658 | **37,509** | 51,262 | 57,889 | 46,898 |
| 1000 | 713,889 | **301,709** | 454,768 | 604,054 | 457,899 |

## Unmarshal（ns/op，越低越好）

| 记录数 | stdlib | go-json | sonic-std | jsoniter-compat | **yd-jsonx** |
|---|---:|---:|---:|---:|---:|
| 1 | 1,717 | 1,135 | 1,352 | **1,058** | 1,239 |
| 100 | 173,882 | **63,725** | 112,753 | 126,609 | 110,428 |
| 1000 | 1,620,344 | **504,882** | 978,174 | 1,078,973 | 948,201 |

## 分配（n=1000）

| 实现 | B/op | allocs/op |
|---|---:|---:|
| go-json | **224,180** | **1,005** |
| stdlib | 381,162 | 5,009 |
| jsoniter-compat | 458,801 | 11,344 |
| sonic-std | 687,631 | 7,013 |
| **yd-jsonx** | 687,004 | 7,013 |

结论：go-json 在该结构体切片上延迟与分配均最优；yd-jsonx 与其 sonic 后端（sonic-std）基本一致，差异在噪声内，并保持与 `encoding/json` 完全一致的输出语义。jsoniter 的兼容配置分配最高，标准库最慢。`jsonx` 的价值在于跨架构自动选后端与统一错误分类，而不是在 amd64 上超越其底层 sonic。

## 自研原生后端（实验性）

通过 `-tags jsonx_native` 启用 `core/jsonx` 的自研反射式编解码器；默认构建仍使用 sonic/go-json。

```bash
cd benchmarks/json
go test -run '^$' -bench . -benchmem -count=3 -tags jsonx_native
```

当前结果（n=1000，500ms × 3 中位数）：Marshal **2,110,502 ns** / 757 KB / 2,025 allocs；Unmarshal **3,147,717 ns** / 612 KB / 22,992 allocs。  
与 go-json（361 µs / 579 µs）相比仍有约 4–5 倍差距，主要来自逐值反射、结构体字段查找与字符串/集合构造；后续优化方向是类型缓存闭包编码器、专用解码路径与 SIMD 扫描。

默认后端（sonic 包装）与 `sonic-std` 在预热后处于同一水平（n=100 Marshal 54.5 vs 54.6 µs，n=1000 Unmarshal 877 vs 878 µs），此前的差异来自 sonic JIT 首次编译与子基准顺序，现已通过预热消除。
