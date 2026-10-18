# JSON 编解码性能基准

独立 Go 模块，对比自适应 `core/jsonx` 与标准库及主流 Go JSON 库。`jsonx` 在 amd64 上默认使用 sonic `ConfigStd`，非 amd64 使用 go-json，`jsonx_std` 标签回退标准库；`jsonx_native` 标签启用自研后端。

## 运行

```bash
cd benchmarks/json

# 同进程快速检查
go test -run '^$' -bench . -benchmem -benchtime=1s -count=5

# 公平采样：每个实现独立进程，轮转顺序（默认后端）
for c in stdlib go-json sonic-std jsoniter-compat yd-jsonx; do
  YD_BENCH_ONLY=$c go test -run '^$' -bench . -benchmem -benchtime=1s -count=5
done

# 自研原生后端
for c in stdlib go-json sonic-std jsoniter-compat yd-jsonx; do
  YD_BENCH_ONLY=$c go test -run '^$' -bench . -benchmem -benchtime=1s -count=5 -tags jsonx_native
done
```

`YD_BENCH_ONLY` 是本目录基准的进程内过滤器；主表采用「每实现独立进程 + 轮转顺序 + 5 轮中位数」，避免同进程 codec 顺序与 GC 状态影响。`TestCodecFixtures` 会先验证每个实现产生的 JSON 语义与标准库一致、并能解回同一组记录，再进入性能比较。

## 测试平台

主表在 **mbox.yun-dev.cn**（hostname `eric-minibox-01`）上采集：

- AMD Ryzen AI MAX+ 395，16 核 32 线程，1 NUMA 节点，L3 64 MiB，30 GiB 内存；
- Ubuntu 26.04.1 LTS，Linux 7.0.0-38-generic，Go 1.27.2 linux/amd64；
- CPU governor `performance`，AVX-512F/BW/CD/DQ/VL + VBMI/VBMI2/VNNI/BF16；
- `GOMAXPROCS=32`；每档 5 轮中位数。

CPU 利用率与峰值 RSS 使用 Linux `/usr/bin/time -v`，对每个实现单独进程测量 3 轮取中位数（2 s 计时，n=1000）。CPU% 是进程平均占用；RSS 包含 Go 运行时、测试二进制与夹具。分配数据来自 `-benchmem`。

## 数据集

同一份 `record` 切片，分别包含 1、100、1000 条记录，字段覆盖 int、string、bool、float64、[]string 与 time.Time。所有实现读取同一份由标准库生成的 JSON 夹具，写入各自的目标变量。

## 默认后端（sonic）

### Marshal（ns/op；n=1000 为 µs/op，越低越好）

| 记录数 | stdlib | go-json | sonic-std | jsoniter-compat | **yd-jsonx** |
|---|---:|---:|---:|---:|---:|
| 1 | 398 | **122** | 268 | 200 | 274 |
| 100 | 31,589 | **11,413** | 15,811 | 18,942 | 16,034 |
| 1000 | 310.6 | **110.5** | 155.7 | 183.7 | 156.3 |

### Unmarshal（ns/op；n=1000 为 µs/op，越低越好）

| 记录数 | stdlib | go-json | sonic-std | jsoniter-compat | **yd-jsonx** |
|---|---:|---:|---:|---:|---:|
| 1 | 835 | **257** | 372 | 381 | 375 |
| 100 | 73,533 | **18,943** | 30,256 | 41,967 | 30,567 |
| 1000 | 733.4 | **181.3** | 295.1 | 415.9 | 297.6 |

### 分配（n=1000，B/op / allocs/op）

| 实现 | Marshal | Unmarshal |
|---|---:|---:|
| stdlib | 168,105 / 3 | 381,588 / 5,009 |
| go-json | 172,092 / **2** | **226,222** / 1,005 |
| sonic-std | 241,991 / 1,004 | 688,873 / 7,013 |
| jsoniter-compat | 220,202 / 1,002 | 459,398 / 11,344 |
| **yd-jsonx** | 242,037 / 1,004 | 688,886 / 7,013 |

默认后端的结论：`jsonx` 与其底层 sonic 基本一致（n=1000 Marshal 156.3 vs 155.7 µs，Unmarshal 297.6 vs 295.1 µs，差异在噪声内）；go-json 在该结构体切片上仍是最快；标准库最慢；jsoniter 兼容配置分配最高。`jsonx` 的默认价值是跨架构自动选后端与统一错误分类，不是在 amd64 上超越 sonic。

## 自研原生后端（`-tags jsonx_native`）

该后端不依赖 sonic/go-json，按 `reflect.Type` 预编译编解码闭包，并保持与 `encoding/json` 一致的复制语义（解码出的字符串不别名调用方输入）。默认构建不会启用。

### Marshal（ns/op；n=1000 为 µs/op，越低越好）

| 记录数 | stdlib | go-json | sonic-std | jsoniter-compat | **yd-jsonx（native）** |
|---|---:|---:|---:|---:|---:|
| 1 | 397 | 121 | 279 | 201 | **107** |
| 100 | 31,357 | 11,529 | 16,192 | 18,874 | **9,255** |
| 1000 | 310.5 | 110.7 | 155.8 | 183.5 | **88.7** |

### Unmarshal（ns/op；n=1000 为 µs/op，越低越好）

| 记录数 | stdlib | go-json | sonic-std | jsoniter-compat | **yd-jsonx（native）** |
|---|---:|---:|---:|---:|---:|
| 1 | 816 | **240** | 369 | 396 | 266 |
| 100 | 73,789 | 19,299 | 30,239 | 43,265 | **18,067** |
| 1000 | 728.7 | 185.8 | 286.7 | 420.1 | **177.8** |

### 分配（n=1000，B/op / allocs/op）

| 实现 | Marshal | Unmarshal |
|---|---:|---:|
| stdlib | 165,991 / 3 | 381,587 / 5,009 |
| go-json | 169,938 / **2** | **225,429** / 1,005 |
| sonic-std | 238,054 / 1,004 | 686,544 / 7,013 |
| jsoniter-compat | 219,494 / 1,002 | 459,380 / 11,344 |
| **yd-jsonx（native）** | 180,635 / **2** | 252,983 / 1,004 |

### 资源利用率（n=1000）

| 操作 | 实现 | ns/op | 平均 CPU | 峰值 RSS |
|---|---|---:|---:|---:|
| Marshal | stdlib | 309,465 | 117% | 17.5 MiB |
| Marshal | go-json | 111,483 | 138% | 20.4 MiB |
| Marshal | sonic-std | 156,676 | 132% | 20.2 MiB |
| Marshal | jsoniter-compat | 182,991 | 132% | 19.3 MiB |
| Marshal | **yd-jsonx（native）** | 89,444 | 147% | 16.9 MiB |
| Unmarshal | stdlib | 729,599 | 119% | 17.7 MiB |
| Unmarshal | go-json | 186,502 | 132% | 19.2 MiB |
| Unmarshal | sonic-std | 300,965 | 159% | 19.0 MiB |
| Unmarshal | jsoniter-compat | 431,567 | 134% | 17.7 MiB |
| Unmarshal | **yd-jsonx（native）** | 179,734 | 134% | 19.3 MiB |

### 原生后端结论（全部实现同表，不删弱项）

- **Marshal**：yd 在 n=1/100/1000 三档均为最快；相对 go-json 快约 11.7%/19.7%/19.9%，相对 sonic 快约 61.5%/42.9%/43.1%。
- **Unmarshal**：n=1 时 go-json 最快（240 ns），yd 为 266 ns，**慢约 11.0%**；n=100 与 n=1000 时 yd 最快（18,067 ns / 177.8 µs），相对 go-json 快约 6.4%/4.3%。n=1 的差距来自小对象上反射类型缓存与字段偏移之外的固定开销，n≥100 已摊销。
- **分配**：Marshal 在 n≥100 使用按估算体积一次性分配的输出缓冲，n=1000 为 180.6 KB/2 allocs（go-json 169.9 KB/2）；Unmarshal 的字符串 arena 使 n=1000 为 253.0 KB/1,004 allocs，B/op 仍高于 go-json 的 225.4 KB，因为本后端坚持与标准库一致的复制语义，而 go-json 采用更激进的字符串零拷贝。
- **资源**：yd Marshal 的 CPU 占用（147%）最高、峰值 RSS（16.9 MiB）最低；Unmarshal 的 CPU/RSS 与 go-json 接近。CPU% 含 Go 运行时后台线程，不代表单核占用。

## 优化要点

- 字段解析在类型缓存构建期完成标准库的嵌入字段支配规则（更浅者优先、同深度 tag 优先、同级同名双方抵消、未导出匿名结构体按标准库提升），编码与解码共用同一份解析结果；
- 按类型缓存编解码闭包，消除逐值的类型特征查询与 kind 分派；类型解析前再挂一个 4 槽原子缓存，避免每次 `Marshal`/`Unmarshal` 都对 `reflect.Type` 做一次 `sync.Map` 哈希；
- 结构体字段使用固定偏移直接读写内存（基础类型、`time.Time`、`[]string`），字段名与 `"key":` 前缀预编码；基础类型在快速路径内联编码，不经逐字段函数值调用；
- 不超过 16 字节的字段键预填充为定长块，用一次 16 字节存储代替变长 `memmove` 追加；
- 字符串与数字扫描使用 8/4 字节 SWAR，字段键按首字节分桶；8 字节键用掩码比较，9–16 字节键比较首尾两个 8 字节，避免 `memequal`；单候选首字节桶直接匹配，字符串转义检查的尾部（≤3 字节）改用查表；
- 解码字符串进入单块 arena，保持复制语义的同时把每字符串一次分配降为整体一次分配；不超过 16 字节的字符串用定长存储直接写入 arena；快速路径检测到高位字节时才做 UTF-8 校验，非法序列按标准库口径替换为 U+FFFD；
- 顶层切片按输入规模预分配，使用 `Grow`+`SetLen` 代替逐元素 `reflect.Append`；
- 解码器缓存上一条 `time.Time` 的原始文本，重复时间戳直接复用；严格的 `YYYY-MM-DDTHH:MM:SSZ` 形式走快速解析，其余形式回退 `time.Time.UnmarshalJSON`；0–9999 的整值浮点与纯整数字面量走整数快速路径；
- 编码端在估算体积 ≥512 B 时直接分配输出缓冲；对完全相同的 `time.Time` 把 key 与格式化结果作为整块缓存；0–9999 的小整数走查表十进制输出，整值浮点（含 `-0`）同样走整数路径；
- 编码器的 `Marshaler` 输出会先校验、去除无意义空白，并按标准库口径重新做 HTML 与 U+2028/U+2029 转义。

## 安全与兼容性

- 解码嵌套深度上限 10000，与 `encoding/json` 的扫描器上限一致；超深、截断输入返回 `ErrSyntax` 而不是 panic 或无限递归；编码侧同样有深度上限，循环指针返回错误而非崩溃。
- 非法 UTF-8 在解码时替换为 U+FFFD、在编码时输出 U+FFFD 字节，与当前 Go 标准库一致；代理项（孤立高/低代理）逐个替换，`\b`/`\f` 使用短转义。
- `core/jsonx/jsonx_native_compat_test.go` 提供与 `encoding/json` 的差分表驱动测试、每个截断前缀的对比测试、深层嵌套/循环指针安全测试，以及 `FuzzNativeUnmarshal` / `FuzzNativeMarshalRoundTrip` 两个模糊测试。
- 字段名大小写匹配使用精确映射 + `strings.EqualFold`，覆盖 `ſ`/`s` 等 Unicode 简单折叠；嵌入字段（值/指针、导出/未导出匿名结构体）完全按标准库规则提升，同名冲突按「深度 → tag → 歧义抵消」解析，指向未导出结构体的嵌入指针返回与标准库同款错误。解码端 `core/jsonx/jsonx_native_fields_test.go` 提供上述冲突规则的逐字节差分测试。
- `omitzero` 与 `omitempty` 可同时使用：自定义 `IsZero`（值/指针接收者、接口、指针与基础类型）优先于 `reflect.Value.IsZero`，语义与标准库一致；差分测试见 `core/jsonx/jsonx_native_omitzero_test.go`。
- 编解码两端均支持 `,string`：bool/整数/无符号/浮点/字符串/json.Number 与单层指针按标准库口径从 JSON 字符串中解出；`null`、空串、非法转义、类型不匹配、与 `omitempty`/`omitzero` 的组合均有差分覆盖（`core/jsonx/jsonx_native_destring_test.go`）。
  注意当前工具链的 `encoding/json` 由 v2 兼容层实现（Go 1.27 默认启用 `jsonv2`）：自定义 `Unmarshaler`/`TextUnmarshaler` 方法优先于 `,string`，引号内的 `null` 按旧版语义空操作，浮点按 Go 语法解析（接受 `NaN`/`Inf`/十六进制浮点），`json.Number` 只接受合法 JSON 数字文本——本后端逐项对齐了这些行为。
- 已知边角差异：带自定义方法的指针字段在 `,string` 下的「原始 token 恰为 `"null"` 时置空指针」收尾已实现；但转义形式（如 `"null"`）依赖原始 token 比较，与标准库同样不做特殊处理。未导出匿名结构体类型若实现方法，标准库会在构建期报错，本后端不做该检查（该类字段无法被外部 tag 选中，影响仅限嵌入类型自身的方法）。
- 基准夹具测试会先对每个实现做语义差分；性能表只比较通过同一夹具校验的实现。
