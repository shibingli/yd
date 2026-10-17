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

通过 `-tags jsonx_native` 启用 `core/jsonx` 的自研编解码器；默认构建仍使用 sonic/go-json。
该实现不依赖 sonic/go-json，按 `reflect.Type` 预编译编解码闭包，并保持与 `encoding/json`
一致的复制语义（解码出的字符串不别名调用方输入）。

```bash
cd benchmarks/json
go test -run '^$' -bench . -benchmem -count=5 -tags jsonx_native
```

### n=1000（交替独立进程 5 轮中位数）

| 实现 | Marshal | Unmarshal | Marshal B/op / allocs | Unmarshal B/op / allocs |
|---|---:|---:|---:|---:|
| go-json | **260,832 ns** | 527,733 ns | 166 KB / 2 | **224 KB / 1,005** |
| **yd-jsonx（native）** | 271,232 ns | **487,097 ns** | **164 KB / 3** | 293 KB / 1,006 |
| sonic-std | 475,995 ns | 912,402 ns | 228 KB / 1,004 | 686 KB / 7,013 |
| jsoniter-compat | 596,248 ns | 1,019,462 ns | 214 KB / 1,002 | 459 KB / 11,344 |
| stdlib | 707,678 ns | 1,572,033 ns | 165 KB / 3 | 381 KB / 5,009 |

结论：Unmarshal 比 go-json 快约 8%，Marshal 慢约 4%；相对 sonic、jsoniter 与标准库，
两个方向都明显领先。n=1 时 Marshal 约 324 ns（go-json 约 278 ns），n=100 时约
27 µs（go-json 约 25 µs）；小载荷仍以 go-json 略优。基准对 CPU 频率与子基准顺序敏感，
跨进程独立运行的波动可达约 15%。

本后端相对早期反射实现的优化：

- 按类型缓存编解码闭包，消除逐值的类型特征查询与 kind 分派；
- 结构体字段使用固定偏移直接读写内存（基础类型、`time.Time`、`[]string`），字段名与
  `"key":` 前缀预编码；
- 字符串与数字扫描使用 8/4 字节 SWAR，字段键按首字节分桶并用 8 字节掩码比较；
- 解码字符串进入单块 arena，保持复制语义的同时把每字符串一次分配降为整体一次分配；
- 顶层切片按输入规模预分配，使用 `Grow`+`SetLen` 代替逐元素 `reflect.Append`；
- 解码器缓存上一条 `time.Time` 的原始文本，重复时间戳直接复用；命中前直接扫描字符串
  token，不经过通用 `skipValue`；
- 编码端按缓存的类型体积估算直接分配输出缓冲，避免尾部整块复制；对完全相同的
  `time.Time` 复用已格式化的 RFC 3339 结果。

解码 B/op 高于 go-json，主要因为后者采用更激进的字符串零拷贝；本后端选择与标准库一致的
复制语义。该后端仍在实验阶段，默认构建不会启用。
