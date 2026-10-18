# JSON 编解码性能基准

独立 Go 模块，对比自适应 `core/jsonx` 与标准库及主流 Go JSON 库。

## 后端选择

| 环境 | 后端 |
|---|---|
| amd64，默认 | 自研原生编解码器（`yd-native`，按类型预编译闭包） |
| 非 amd64，默认 | `github.com/goccy/go-json` |
| 任意架构，`-tags jsonx_std` | 标准库 `encoding/json` |

默认构建不再依赖 sonic 或 jsoniter。完整的单核/多核指标、CPU 与峰值 RSS 见
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
- 单核表 `GOMAXPROCS=1`、5 轮中位数；多核表分别设置 `GOMAXPROCS=1/2/4/8/16/32`；
- CPU/RSS 使用 Linux `/usr/bin/time -v`，每实现独立进程测量。

## 单核性能（`GOMAXPROCS=1`）

### Marshal（ns/op；n=1000 为 µs/op）

| 记录数 | stdlib | go-json | sonic-std | jsoniter-compat | **yd-jsonx** |
|---|---:|---:|---:|---:|---:|
| 1 | 473 | **132** | 315 | 215 | 133 |
| 100 | 36,135 | 12,118 | 19,295 | 20,099 | **11,710** |
| 1000 | 356.8 | **116.3** | 186.4 | 196.5 | 117.8 |

### Unmarshal（ns/op；n=1000 为 µs/op）

| 记录数 | stdlib | go-json | sonic-std | jsoniter-compat | **yd-jsonx** |
|---|---:|---:|---:|---:|---:|
| 1 | 826 | **288** | 450 | 445 | 317 |
| 100 | 73,568 | 22,184 | 36,468 | 48,615 | **21,190** |
| 1000 | 733.9 | **209.6** | 358.5 | 472.8 | 214.0 |

### 分配（n=1000，B/op / allocs/op）

| 实现 | Marshal | Unmarshal |
|---|---:|---:|
| stdlib | 163,897 / 3 | 380,790 / 5,009 |
| go-json | **163,873 / 2** | **218,123** / 1,005 |
| sonic-std | 211,937 / 1,003 | 683,786 / 7,013 |
| jsoniter-compat | 211,876 / 1,002 | 458,355 / 11,344 |
| **yd-jsonx** | 172,065 / **2** | 252,478 / 1,004 |

单核结论：n=100 时 yd 的 Marshal/Unmarshal 均第一；n=1 与 n=1000 的 Marshal 慢于 go-json 约 1.1%/1.3%，n=1/n=1000 的 Unmarshal 慢约 10.4%/2.1%。分配上 Marshal 与 go-json 同为 2 次；Unmarshal 字符串保持与标准库一致的复制语义，B/op 高于 go-json。

## 多核性能（n=1000，MB/s）

| GOMAXPROCS | 操作 | go-json | **yd-jsonx** | 说明 |
|---|---:|---:|---:|---|
| 8 | Marshal | 7,959 | **8,575** | yd 领先 |
| 16 | Marshal | 9,303 | **9,907** | yd 峰值，7.35× 单核 |
| 32 | Marshal | **11,208** | 8,961 | yd 回落，go-json 领先约 20% |
| 8 | Unmarshal | 3,252 | **3,671** | yd 领先 |
| 16 | Unmarshal | 2,712 | **4,758** | yd 领先 |
| 32 | Unmarshal | 3,087 | **5,858** | yd 领先约 89.8%，7.87× 单核 |

32 核资源：Marshal 的 yd CPU 1,336%、RSS 181.1 MiB（go-json 1,644%、182.4 MiB）；Unmarshal 的 yd CPU 1,918%、RSS 64.3 MiB（go-json 2,449%、115.3 MiB）。完整的 1/2/4/8/16/32 核表与其它实现见[报告](../x86_64-single-multi-core.md)。

## 优化要点

- 字段解析在类型缓存构建期完成标准库的嵌入字段支配规则（更浅者优先、同深度 tag 优先、同级同名双方抵消、未导出匿名结构体按标准库提升），编码与解码共用同一份解析结果；
- 按类型缓存编解码闭包，类型解析前再挂 4 槽原子缓存，避免每次调用都对 `reflect.Type` 做 `sync.Map` 哈希；
- 结构体字段使用固定偏移直接读写（基础类型、`time.Time`、`[]string`），字段名与 `"key":` 前缀预编码；不超过 16 字节的键用一次 16 字节存储；
- 字符串与数字扫描使用 8/4 字节 SWAR，字段键按首字节分桶；字符串转义检查尾部改用查表；
- 解码字符串进入单块 arena，保持复制语义；顶层切片使用 `Grow`+`SetLen`；
- 编码端估算体积 ≥512 B 时直接分配输出缓冲，上限由 `core/sysinfo` 按进程内存上限自动缩放（不再写死 4 MiB）；编码器池上限同样按内存上限与 GOMAXPROCS 缩放；
- 小整数查表输出、整值浮点整数路径、`time.Time` 原文缓存与快速解析、`Marshaler` 输出校验/压缩/HTML 转义。

## 安全与兼容性

- 解码嵌套深度上限 10000；超深、截断输入返回 `ErrSyntax`，不 panic、不无限递归；编码侧同样有深度上限，循环指针返回错误。
- 非法 UTF-8 解码替换为 U+FFFD、编码输出 U+FFFD 字节，与当前 Go 标准库一致；代理项逐个替换。
- `core/jsonx/jsonx_native_compat_test.go` 提供与 `encoding/json` 的差分表驱动测试、每个截断前缀对比、深层嵌套/循环指针安全测试，以及 `FuzzNativeUnmarshal` / `FuzzNativeMarshalRoundTrip`。
- 字段名大小写匹配使用精确映射 + `strings.EqualFold`；嵌入字段与同名冲突完全按标准库规则解析；解码端 `jsonx_native_fields_test.go` 提供逐字节差分。
- `omitzero` 与 `omitempty` 可同时使用，自定义 `IsZero` 优先于 `reflect.Value.IsZero`；测试见 `jsonx_native_omitzero_test.go`。
- 编解码两端支持 `,string`：bool/整数/无符号/浮点/字符串/json.Number 与单层指针；`null`、空串、非法转义、类型不匹配均有差分覆盖。Go 1.27 的 `encoding/json` 由 v2 兼容层实现，自定义 `Unmarshaler`/`TextUnmarshaler` 优先于 `,string`，浮点接受 `NaN`/`Inf`/十六进制，`json.Number` 只接受合法 JSON 数字文本——本后端逐项对齐。
- 已知边角差异：转义形式（如 `"null"`）依赖原始 token 比较，与标准库同样不做特殊处理；未导出匿名结构体类型若实现方法，标准库构建期报错，本后端不做该检查（该类字段无法被外部 tag 选中）。
