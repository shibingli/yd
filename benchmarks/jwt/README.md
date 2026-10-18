# JWT 签发与验签性能基准

本目录是一个**独立 Go 模块**，测量 `security/jwt` 的签发、验签与高并发吞吐，并与主流高性能 JWT 社区库在同一平台、同一份 claims 与同一份 token 字节上对比。独立模块避免把对比库带进主模块的 `go.mod`。

## 对比实现

| 标签 | 实现 | 说明 |
|---|---|---|
| `our` | `security/jwt` | 免反射：头部/载荷手写扫描，不构造 `map[string]any`；单次预分配数据缓冲；池化 HMAC；`Token.Release` 零分配复用路径 |
| `golang-jwt` | `github.com/golang-jwt/jwt/v5 v5.3.1` | 生态最广的 JWT 库，`MapClaims` + 函数式校验选项 |
| `cristal` | `github.com/cristalhq/jwt/v5 v5.4.0` | 以低分配为目标的高性能库，结构体 claims |
| `kataras` | `github.com/kataras/jwt v0.3.0` | 高吞吐 JWT 库，自带 claims 校验与自定义 claims |

所有实现使用同一组六项 claims（`sub`、`iss`、`aud`、`iat`、`exp`、`jti`），并启用算法白名单、`iss`、`aud` 与 `exp` 必填校验。`our` 额外测量 `Token.Release` 复用模式；`kataras` 会精确比对规范头部字节，因此它校验的是自己签发的 token（其头部不含 `kid`），其余实现校验同一份由 `our` 签发的 token。

## 运行方式

```bash
cd benchmarks/jwt

# 正确性/编译检查
go test ./...

# 同进程快速检查（仅用于冒烟，不作为主表）
go test -run '^$' -bench . -benchmem -benchtime=1s

# 公平采样：每个实现独立进程、5 轮取中位数
for lib in our golang-jwt cristal kataras; do
  for bench in 'BenchmarkSign$' 'BenchmarkParse$' 'BenchmarkParseParallel$' \
               'BenchmarkSignCristal$' 'BenchmarkParseCristal$' 'BenchmarkParseParallelCristal$' \
               'BenchmarkSignKataras$' 'BenchmarkParseKataras$' 'BenchmarkParseParallelKataras$'; do
    YD_BENCH_ONLY=$lib go test -run '^$' -bench "$bench" -benchmem -benchtime=1s -count=5
  done
done
```

`YD_BENCH_ONLY` 是进程内过滤器；主表采用「每实现独立进程 + 5 轮中位数」，避免同进程顺序、CPU 频率与 GC 状态带来的偏差。`BenchmarkParseParallel` 使用 `b.RunParallel`，并发度由 `GOMAXPROCS` 决定。

## 测试平台

主表在 **x86_64 基准平台**上采集：

- AMD Ryzen AI MAX+ 395，16 核 32 线程，1 NUMA 节点，L3 64 MiB，30 GiB 内存；
- Ubuntu 26.04.1 LTS，Linux 7.0.0-38-generic，Go 1.27.2 linux/amd64；
- CPU governor `performance`，AVX-512F/BW/CD/DQ/VL + VBMI/VBMI2/VNNI/BF16；
- `GOMAXPROCS=32`；顺序基准单协程，并发基准 `b.RunParallel`；`-benchmem`，5 轮中位数。

## 结果

### 签发 HS256（同一组 claims）

| 实现 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| **our-json（`SignJSON`，载荷已序列化）** | **531.5** | **264** | **2** |
| **our-struct（`Sign` 结构体 claims）** | **617.4** | **477** | **4** |
| our（`Sign` map claims） | 1,219 | 935 | 19 |
| cristal（结构体 claims） | 1,030 | 1,084 | 14 |
| kataras | 1,575 | 2,292 | 25 |
| golang-jwt（`MapClaims`） | 2,399 | 2,464 | 46 |

`our-json` 与 `our-struct` 是推荐的热路径；`our` 的 map 版本是便利 API，仍需序列化 map，因此慢于结构体路径，但仍快于 kataras 与 golang-jwt 的 map 路径。

### 验签 HS256（每请求）

| 实现 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| **our** | **743.3** | **289** | **2** |
| cristal | 2,134 | 1,077 | 19 |
| kataras | 2,284 | 1,259 | 19 |
| golang-jwt | 3,348 | 2,622 | 50 |

### 验签 ES256（每请求）

| 实现 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| **our** | **39,708** | **1,414** | **21** |
| kataras | 40,976 | 2,103 | 34 |
| cristal | 41,211 | 2,489 | 40 |
| golang-jwt | 42,212 | 3,468 | 65 |

ES256 的耗时由 ECDSA P-256 验签主导，四个实现的差距因此缩小；`our` 仍以最少分配取得最低延迟。

### 并发验签 HS256（`GOMAXPROCS=32`）

`ns/op` 是并发负载下的每操作墙钟时间；`ops/s` 为 `1e9 / ns/op` 的聚合吞吐。

| 实现 | ns/op | ops/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| **our-reuse（`Parse` + `Release`）** | **38.26** | **26.1 M** | **0** | **0** |
| **our（`Parse`，不归还）** | **148.1** | **6.75 M** | **291** | **2** |
| cristal | 527.9 | 1.89 M | 871 | 18 |
| kataras | 707.5 | 1.41 M | 1,302 | 19 |
| golang-jwt | 1,191 | 0.84 M | 2,673 | 50 |

`our` 的 2 次分配是 `Token` 与其数据缓冲；调用 `Release` 后由池复用，稳态为 0 分配。不调用 `Release` 时 Token 归调用方所有，可以安全地跨请求保留。

## 结论

- **HS256 验签**：`our` 743.3 ns / 2 allocs，比 cristal（2,134 ns / 19 allocs）快 **2.87×**、比 kataras（2,284 ns / 19）快 **3.07×**、比 golang-jwt（3,348 ns / 50）快 **4.50×**；分配次数为 2 vs 19/19/50。
- **ES256 验签**：`our` 39.7 µs / 21 allocs，比 kataras（40.98 µs / 34）快 **3.1%**、比 cristal（41.21 µs / 40）快 **3.8%**、比 golang-jwt（42.21 µs / 65）快 **5.9%**；ECDSA 运算占主导，但 `our` 的解析与声明扫描开销最小。
- **签发**：`our-json` 531.5 ns / 2 allocs，比 cristal 的结构体路径（1,030 ns / 14）快 **1.94×**、比 golang-jwt 的 MapClaims（2,399 ns / 46）快 **4.51×**；`our-struct` 617.4 ns / 4 allocs，比 cristal 快 **1.67×**。map 便利路径 1,219 ns 慢于 cristal 的结构体路径，但仍快于 kataras（1,575）与 golang-jwt（2,399）；热路径应使用 `SignJSON` 或结构体 claims。
- **并发吞吐**：`our-reuse` 38.26 ns/op ≈ **26.1 M ops/s、0 allocs/op**，比 cristal（527.9 ns / 18 allocs）快 **13.8×**、比 kataras（707.5 ns / 19）快 **18.5×**、比 golang-jwt（1,191 ns / 50）快 **31.1×**；不归还 Token 的 `our` 为 148.1 ns（6.75 M ops/s），仍比 cristal 快 **3.56×**。
- **为什么快**：解析路径完全不构造 `map[string]any`，头部与载荷由手写扫描器直接读取；载荷、签名、`kid` 与 `typ` 共用一次预分配的数据缓冲；HMAC 哈希状态从 `sync.Pool` 复用；ECDSA 的 `big.Int` 暂存跨调用复用；`Token.Release` 让池在稳态下零分配。
- **安全边界不变**：先校验头部结构与算法白名单，再解析载荷，之后才取密钥并验签，最后校验 `exp`/`nbf`/`iss`/`aud`；HMAC 使用常量时间比较；`alg:none` 永不接受；算法与密钥族不匹配一律 `ErrSignatureInvalid`；`Release` 后的 Token 不得再使用。race 与全量测试在 x86_64 平台通过。

## 安全性与口径

- `Parse` 的顺序是刻意的：先校验头部与算法白名单，再校验载荷结构，之后才取密钥并验签，最后校验登记声明；未经验签的声明绝不影响判定。
- HMAC 比较使用常量时间比较；`alg:none` 永不接受；密钥族与算法不匹配一律以 `ErrSignatureInvalid` 拒绝，封堵算法混淆。
- `Token.Release` 是可选的零分配快速路径；调用后不得再使用该 Token 及其派生字符串/切片。需要跨请求保留时必须不调用 `Release`。
- 基准数值对机器负载、温度与电源策略敏感，跨进程独立运行的波动可达约 15%。跨机比较请以同一平台的相对关系为准。
