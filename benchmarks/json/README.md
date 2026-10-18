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

### 交替独立进程基准（本机 16 线程，中位数）

n=1/n=100 取 5 轮交替独立进程，n=1000 取 7 轮；每轮只跑一种实现，避免同进程
codec 顺序与 GC 状态影响。表中数值为 `ns/op`。

| 记录数 | 实现 | Marshal | Unmarshal | Marshal B/op / allocs | Unmarshal B/op / allocs |
|---|---|---:|---:|---:|---:|
| n=1 | go-json | 304 ns | 726 ns | 168 B / 2 | 542 B / 3 |
| n=1 | **yd-jsonx（native）** | 320 ns | **723 ns** | 168 B / 2 | **288 B / 5** |
| n=100 | go-json | 26,269 ns | 53,155 ns | 16.4 KB / 2 | 28.2 KB / 103 |
| n=100 | **yd-jsonx（native）** | **24,203 ns** | **51,478 ns** | 18.5 KB / **2** | 25.4 KB / 104 |
| n=1000 | go-json | 269,989 ns | 553,156 ns | 165 KB / 2 | 225 KB / 1,005 |
| n=1000 | **yd-jsonx（native）** | **246,021 ns** | **508,613 ns** | 180 KB / **2** | 253 KB / 1,004 |

结论：n=100/1000 的 Marshal 快约 8%–10%，Unmarshal 快约 3%–8%；n=1 的 Unmarshal
持平（723 vs 726 ns），Marshal 慢约 5%。相对 sonic、jsoniter 与标准库，两个方向都
明显领先。绝对 ns 对 CPU 频率与后台负载敏感，跨机器比较请以同一轮的相对关系为准。

三环境 n=1000（远程为同进程 3 轮中位数，本地为交替独立进程）：

| 环境 | Marshal（yd vs go-json） | Unmarshal（yd vs go-json） |
|---|---:|---:|
| 本地 16 线程 AVX2 | **246 vs 270 µs（快 8.9%）** | **509 vs 553 µs（快 8.0%）** |
| 远程 1 32 核 | **102 vs 118 µs（快 14.1%）** | 189 vs 185 µs（慢 2.4%） |
| 远程 2 4 vCPU 企业级 Xeon | **286 vs 309 µs（快 7.4%）** | 608 vs 607 µs（持平） |

分配方面：Marshal 在 n≥100 时改用按估算体积一次性分配的输出缓冲，alloc 从 3 降到 2，
B/op 上升约 9%–10%；Unmarshal 通过按输入三分之一初始化字符串 arena、复用解码器状态，
n=1000 的 B/op 从 293 KB 降到 253 KB（go-json 为 225 KB），n=1 从 608 B 降到 288 B。
解码 B/op 仍高于 go-json，因为后者采用更激进的字符串零拷贝；本后端坚持与标准库一致的
复制语义。

本后端相对早期反射实现的优化：

- 按类型缓存编解码闭包，消除逐值的类型特征查询与 kind 分派；
- 结构体字段使用固定偏移直接读写内存（基础类型、`time.Time`、`[]string`），字段名与
  `"key":` 前缀预编码；
- 字符串与数字扫描使用 8/4 字节 SWAR，字段键按首字节分桶；8 字节键用掩码比较，
  9–16 字节键比较首尾两个 8 字节，避免 `memequal`；
- 解码字符串进入单块 arena，保持复制语义的同时把每字符串一次分配降为整体一次分配；
  快速路径检测到高位字节时才做 UTF-8 校验，非法序列按标准库口径替换为 U+FFFD；
- 顶层切片按输入规模预分配，使用 `Grow`+`SetLen` 代替逐元素 `reflect.Append`；
- 解码器缓存上一条 `time.Time` 的原始文本，重复时间戳直接复用；严格的
  `YYYY-MM-DDTHH:MM:SSZ` 形式走快速解析，其余形式回退 `time.Time.UnmarshalJSON`；
  0–9999 的整值浮点与纯整数字面量走整数快速路径，避免 `strconv` 的通用十进制处理；
- 编码端在估算体积 ≥512 B 时直接分配输出缓冲，避免尾部整块复制与多次扩容；对完全相同的
  `time.Time` 把 key 与格式化结果作为整块缓存，命中时一次 append 写入；
  0–9999 的小整数走查表十进制输出，整值浮点（含 `-0`）同样走整数路径；
- 编码器的 `Marshaler` 输出会先校验、去除无意义空白，并按标准库口径重新做 HTML 与
  U+2028/U+2029 转义。

### 安全与兼容性

- 解码嵌套深度上限 10000，与 `encoding/json` 的扫描器上限一致；超深、截断输入返回
  `ErrSyntax` 而不是 panic 或无限递归；编码侧同样有深度上限，循环指针返回错误而非崩溃。
- 非法 UTF-8 在解码时替换为 U+FFFD、在编码时输出 U+FFFD 字节，与当前 Go 标准库一致；
  代理项（孤立高/低代理）逐个替换，`\b`/`\f` 使用短转义。
- `core/jsonx/jsonx_native_compat_test.go` 提供与 `encoding/json` 的差分表驱动测试、
  每个截断前缀的对比测试、深层嵌套/循环指针安全测试，以及
  `FuzzNativeUnmarshal` / `FuzzNativeMarshalRoundTrip` 两个模糊测试。
- 字段名大小写匹配使用精确映射 + `strings.EqualFold`，覆盖 `ſ`/`s` 等 Unicode 简单折叠；
  嵌入字段按标准库提升，指向未导出结构体的嵌入指针返回与标准库同款错误。已知残留：
  同名嵌入字段的冲突优先级尚未完全对齐标准库，会按声明顺序输出重复键；常规嵌入与
  重复 JSON 键已对齐。

该后端仍在实验阶段，默认构建不会启用。
