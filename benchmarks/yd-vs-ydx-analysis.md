# yd 与 ydx 基准口径差异分析

本文解释为什么 `benchmarks/json`、`benchmarks/base64` 与 ydx 仓内的组件基准
（`ydx/benchmarks/json`、`ydx/benchmarks/base64`）给出的排名不一致，并记录
2026-10-23 的两轮优化（`ydx/base64 v0.1.8`、`ydx/json v0.1.9`）。

## 结论摘要

1. **两套基准的载荷与对比集合不同**，绝对 ns 不可直接比较：
   - yd 的 JSON 使用 7 字段 `record`（含 `time.Time`、`Email`，`Score` 为
     `float64(i)/3` 的长小数，n=1 时是单元素数组）；
   - ydx 的 JSON 使用 5 字段 `benchUser`（`Score` 为 `98.5` 短小数，`one` 是单个对象）；
   - yd 的 Base64 覆盖 32 B 并加入 cloudwego/base64x；ydx 的 Base64 从 16 B 起，
     只对比标准库与 cristalhq。
2. **不是工具退化**：在同一载荷上，ydx 的短小数、`time.Time` 与 `[]string` 路径领先；
   长小数路径落后 go-json，原因是超出精确短范围后回退到 `strconv.ParseFloat`。
3. **Base64 32 B 是真实弱项**：v0.1.7 的 SIMD 阈值（28/32）让 32 B 过早进入
   SIMD，固定初始化成本反而更慢；v0.1.8 提到 64 后回到标量路径，32 B 明显改善，
   但仍落后 cloudwego 的小块 SIMD 内核。
4. **测量框架差异**：yd 的对比基准通过函数指针调用各实现（对全部实现公平），
   ydx 的组件基准直接调用 API；在 32 B 上函数指针额外约 0.7–1.4 ns，绝对数会高于
   ydx README，但相对排名仍以同一框架为准。

## JSON

### 载荷差异

| 项 | yd `benchmarks/json` | ydx `benchmarks/json` |
|---|---|---|
| 字段数 | 7（`created`、`tags`、`id`、`score`、`name`、`email`、`active`） | 5（`name`、`tags`、`id`、`score`、`active`） |
| 时间字段 | `time.Time`（固定值 `2026-10-18T12:00:00Z`） | 无 |
| Score | `float64(i)/3`，最长 17 位小数 | `98.5`，短小数 |
| n=1 形态 | 单元素数组 | 单个对象 |

因此 ydx README 的 `Unmarshal/one`（118.6 ns）与 yd 的
`Unmarshal/n=1`（优化前 316 ns）不是同一件事。

### 长小数根因

对同一 7 字段载荷做单对象直接调用（参考主机，`GOMAXPROCS=1`）：

| 载荷 | ydx/json v0.1.8 | goccy | 说明 |
|---|---:|---:|---|
| `Score=98.5`（短小数） | 200 ns | 205 ns | ydx 领先 |
| `Score=7.0/3`（长小数） | 255 ns | 221 ns | ydx 落后 |

长小数 `2.3333333333333335` 的尾数超过 2^53，v0.1.8 的
`parseShortFloatField` 放弃后，依次重复 `parseNumberBytes`、`parseShortDecimalFloat`
扫描，最后调用 `strconv.ParseFloat`。v0.1.9 改为单遍 `scanFloatField`，一次扫描
同时完成语法校验与短范围转换，长小数不再多付两遍扫描。

### 优化前后（参考主机）

| 场景 | v0.1.8 | v0.1.9 |
|---|---:|---:|
| ydx/json 单对象长小数 | 255 ns | 约 249 ns |
| ydx/json 单对象短小数 | 199 ns | 约 194 ns |
| yd `Unmarshal/n=1`（数组） | 316 ns | 约 300 ns |
| yd `Unmarshal/n=100` | 19,930 ns | 约 19,150 ns |
| yd `Unmarshal/n=1000` | 195.4 µs | 约 189.5 µs |

剩余差距（n=1 约 4%，单对象长小数约 12%）来自十进制到二进制的转换：
`strconv.ParseFloat` 约 23 ns，而 go-json 使用自带的快速浮点解析。要完全追平，
需要在 ydx/json 内实现等价的 Eisel-Lemire 快速路径；这属于后续独立优化项，
本轮以消除重复扫描、保证语义与 `encoding/json` 完全一致为先。

## Base64

### 载荷与对比集合差异

| 项 | yd `benchmarks/base64` | ydx `benchmarks/base64` |
|---|---|---|
| 最小尺寸 | 32 B | 16 B |
| 对比实现 | 标准库、cloudwego/base64x、cristalhq | 标准库、cristalhq |
| 调用方式 | 函数指针（所有实现一致） | 直接调用 API |

### 32 B 阈值问题

v0.1.7 的编码 SIMD 阈值是 28 B、解码是 32 字符，32 B 输入因此进入 SIMD。
参考主机实测（直接调用）：

| 操作 | SIMD 路径（v0.1.7） | 标量路径 | cloudwego |
|---|---:|---:|---:|
| Encode 32 B | 9.60 ns | **7.91 ns** | 6.51 ns |
| Decode 44 字符 | 12.88 ns | **11.47 ns** | 10.44 ns |

v0.1.8 把阈值统一提高到 64（编码 64 B、解码 64 字符），32 B 回到标量路径。
yd 的函数指针框架下：

| 指标 | v0.1.7 | v0.1.8 | cloudwego |
|---|---:|---:|---:|
| Encode 32 B | 10.20 ns | **8.80 ns** | 7.47 ns |
| Decode 32 B | 13.93 ns | **12.86 ns** | 11.51 ns |

编码已超过 cristalhq（9.57 ns），解码与 cristalhq（12.74 ns）基本持平；
cloudwego 仍领先约 18%/12%，差距来自其针对小块输入的 SIMD 内核。
要彻底追平需要新增低启动成本的 32 B SIMD 内核，而不是继续调整标量循环。

## 方法学建议

- 对比 ranking 时保持**同一框架、同一载荷、同一进程模型**；不要跨 README 比较绝对 ns。
- 新增实现或修改阈值后，至少覆盖 **16 B、32 B、64 B、1 KiB** 与**短/长小数**两类 JSON 载荷。
- 函数指针带来的固定开销在小尺寸上占比很高；报告绝对数时应注明调用方式，
  或同时给出直接调用参考值。
