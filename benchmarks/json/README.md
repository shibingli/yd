# JSON 编解码性能基准

独立 Go 模块，对比自适应 `ydx/json` 与标准库及主流 Go JSON 库。本轮数据由
`github.com/shibingli/ydx/json v0.1.10` 在参考 x86_64 主机上重新采集（2026-10-23）。

## 后端选择

| 环境 | 后端 |
|---|---|
| amd64 / arm64，默认 | 自研原生编解码器（`yd-native`；amd64 SSE2/AVX2/AVX-512BW，arm64 NEON） |
| 其它架构，默认 | 同一套自研编解码器的 SWAR/标量路径 |
| 任意架构，`-tags jsonx_std` | 标准库 `encoding/json` |

默认构建不再依赖 sonic、jsoniter 或 goccy/go-json。完整的单核/多核指标、CPU 与峰值 RSS 见
[`benchmarks/x86_64-single-multi-core.md`](../x86_64-single-multi-core.md)。

## 运行

```bash
cd benchmarks/json

# 同进程快速检查
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5

# 公平采样：每个实现独立进程，轮转顺序
for c in stdlib go-json sonic-std jsoniter-compat yd-jsonx; do
  YD_BENCH_ONLY=$c go test -run '^$' -bench . -benchmem -benchtime=1s -count=5
done

# 单核
for c in stdlib go-json sonic-std jsoniter-compat yd-jsonx; do
  GOMAXPROCS=1 YD_BENCH_ONLY=$c go test -run '^$' -bench . -benchmem -benchtime=1s -count=5
done
```

`YD_BENCH_ONLY` 是本目录基准的进程内过滤器。主表采用「每实现独立进程 + 轮转顺序 + 多轮中位数」；`TestCodecFixtures` 会先验证每个实现产生的 JSON 语义与标准库一致、并能解回同一组记录。

## 测试平台

- x86_64 基准平台：AMD Ryzen AI MAX+ 395，16 核 32 线程，30 GiB，Ubuntu 26.04，Go 1.27.2，AVX-512 VBMI2；
- 单核表 `GOMAXPROCS=1`、本轮 3 轮中位数；多核表分别设置 `GOMAXPROCS=1/2/4/8/16/32`、3 轮中位数；
- CPU/RSS 使用 Linux `/usr/bin/time -v`，每实现独立进程测量。

## 单核性能（`GOMAXPROCS=1`）

### Marshal（ns/op；n=1000 为 µs/op）

| 记录数 | stdlib | go-json | sonic-std | jsoniter-compat | **yd-jsonx** |
|---|---:|---:|---:|---:|---:|
| 1 | 470 | 132 | 319 | 215 | **121** |
| 100 | 36,201 | 12,023 | 20,261 | 20,038 | **9,533** |
| 1000 | 360.9 | 116.1 | 195.0 | 195.5 | **92.2** |

### Unmarshal（ns/op；n=1000 为 µs/op）

| 记录数 | stdlib | go-json | sonic-std | jsoniter-compat | **yd-jsonx** |
|---|---:|---:|---:|---:|---:|
| 1 | 831 | **286** | 442 | 449 | 317 |
| 100 | 74,650 | 22,638 | 36,922 | 49,184 | **19,154** |
| 1000 | 735.3 | 211.3 | 361.4 | 476.8 | **190.4** |

### 分配（n=1000，B/op / allocs/op）

| 实现 | Marshal | Unmarshal |
|---|---:|---:|
| stdlib | 163,897 / 3 | 380,790 / 5,009 |
| go-json | **163,873 / 2** | **218,123** / 1,005 |
| sonic-std | 211,937 / 1,003 | 683,786 / 7,013 |
| jsoniter-compat | 211,876 / 1,002 | 458,355 / 11,344 |
| **yd-jsonx** | 172,065 / **2** | 195,462 / 1,004 |

单核结论：Marshal 在 n=1/100/1000 全部第一，相对 go-json 快约 8.3%/20.7%/20.6%；
Unmarshal 在 n=100/n=1000 第一（快约 15.4%/9.9%），n=1 慢约 11%。分配上 Marshal 与
go-json 同为 2 次；Unmarshal 的 B/op 为 **195,462**（解码 arena 复用），字符串保持与
标准库一致的复制语义。

v0.1.10 为长尾数与指数形式加入非舍入缩放快速路径：单个长小数记录（`1.3333333333333333`）
从约 220.7 ns 降到 209.5 ns（-5.1%），n=100/n=1000 相对 v0.1.9 约快 1.1%–1.2%。n=1 的
单记录结果对链接布局敏感，本轮比 v0.1.9 的约 300 ns 慢约 4%，因此表中 n=1 落后 go-json；
多记录吞吐仍是本轮的主要收益。

## 多核性能（n=1000，MB/s）

| GOMAXPROCS | 操作 | go-json | **yd-jsonx** | 说明 |
|---|---:|---:|---:|---|
| 8 | Marshal | 7,892 | **9,012** | yd 领先 |
| 16 | Marshal | 9,535 | **10,367** | yd 领先，5.9× 单核 |
| 32 | Marshal | 10,462 | **12,022** | yd 领先约 14.9% |
| 8 | Unmarshal | 3,688 | **4,703** | yd 领先 |
| 16 | Unmarshal | 3,573 | **5,249** | yd 领先 |
| 32 | Unmarshal | 4,409 | **6,336** | yd 领先约 43.7%，7.4× 单核 |

32 核资源（预编译测试二进制，3 轮中位数）：Marshal 的 yd CPU 1,447%、RSS 83.6 MiB
（go-json 1,604%、169.2 MiB）；Unmarshal 的 yd CPU 1,748%、RSS 71.0 MiB
（go-json 2,421%、119.0 MiB）。完整的 1/2/4/8/16/32 核表与其它实现见[报告](../x86_64-single-multi-core.md)。

## 优化要点

- 字段解析在类型缓存构建期完成标准库的嵌入字段支配规则（更浅者优先、同深度 tag 优先、同级同名双方抵消、未导出匿名结构体按标准库提升），编码与解码共用同一份解析结果；
- 按类型缓存编解码闭包，类型解析前再挂 4 槽原子缓存，避免每次调用都对 `reflect.Type` 做 `sync.Map` 哈希；
- 结构体字段使用固定偏移直接读写（基础类型、`time.Time`、`[]string`），字段名与 `"key":` 前缀预编码；不超过 16 字节的键用一次 16 字节存储；
- 字符串与数字扫描使用 8/4 字节 SWAR，字段键按首字节分桶；字符串转义检查尾部改用查表；
- 解码字符串进入单块 arena，保持复制语义；顶层切片使用 `Grow`+`SetLen`；
- 编码端估算体积 ≥512 B 时直接分配输出缓冲，上限由 `ydx/sysinfo` 按进程内存上限自动缩放（不再写死 4 MiB）；编码器池上限同样按内存上限与 GOMAXPROCS 缩放；
- 小整数查表输出、整值浮点整数路径、`time.Time` 原文缓存与快速解析、`Marshaler` 输出校验/压缩/HTML 转义。

## 安全与兼容性

- 解码嵌套深度上限 10000；超深、截断输入返回 `ErrSyntax`，不 panic、不无限递归；编码侧同样有深度上限，循环指针返回错误。
- 非法 UTF-8 解码替换为 U+FFFD、编码输出 U+FFFD 字节，与当前 Go 标准库一致；代理项逐个替换。
- `ydx/json/jsonx_native_compat_test.go` 提供与 `encoding/json` 的差分表驱动测试、每个截断前缀对比、深层嵌套/循环指针安全测试，以及 `FuzzNativeUnmarshal` / `FuzzNativeMarshalRoundTrip`。
- 字段名大小写匹配使用精确映射 + `strings.EqualFold`；嵌入字段与同名冲突完全按标准库规则解析；解码端 `jsonx_native_fields_test.go` 提供逐字节差分。
- `omitzero` 与 `omitempty` 可同时使用，自定义 `IsZero` 优先于 `reflect.Value.IsZero`；测试见 `jsonx_native_omitzero_test.go`。
- 编解码两端支持 `,string`：bool/整数/无符号/浮点/字符串/json.Number 与单层指针；`null`、空串、非法转义、类型不匹配均有差分覆盖。Go 1.27 的 `encoding/json` 由 v2 兼容层实现，自定义 `Unmarshaler`/`TextUnmarshaler` 优先于 `,string`，浮点接受 `NaN`/`Inf`/十六进制，`json.Number` 只接受合法 JSON 数字文本——本后端逐项对齐。
- 已知边角差异：转义形式（如 `"null"`）依赖原始 token 比较，与标准库同样不做特殊处理；未导出匿名结构体类型若实现方法，标准库构建期报错，本后端不做该检查（该类字段无法被外部 tag 选中）。
