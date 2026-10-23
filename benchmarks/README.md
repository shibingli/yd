# benchmarks 总览

本目录包含若干**独立 Go 模块**，每个模块只负责一类基准，避免把该类的第三方对比依赖带进主模块的 `go.mod`。

x86_64 单核/多核的完整指标（含全部对比库、CPU 与峰值 RSS）见
[`x86_64-single-multi-core.md`](x86_64-single-multi-core.md)；与 ydx 组件基准的口径差异、
两轮优化的根因与剩余差距见 [`yd-vs-ydx-analysis.md`](yd-vs-ydx-analysis.md)。

| 模块 | 内容 | 主要对比对象 |
|---|---|---|
| [`ws/`](ws/README.md) | WebSocket 性能与兼容性 | coder、gorilla、gobwas、gws |
| [`router/`](router/README.md) | 静态/动态路由与握手开销 | 标准库 ServeMux、gin、chi、echo、httprouter、fiber |
| [`json/`](json/README.md) | JSON 编解码 | 标准库、go-json、sonic、jsoniter |
| [`base64/`](base64/README.md) | Base64 编解码 | 标准库、cloudwego/base64x、cristalhq/base64 |
| `ydx/benchmarks/regexpx`（ydx 仓） | 正则匹配、查找与批量扫描 | 标准库 regexp、grafana/regexp、regexp2、go-re2 |
| [`jwt/`](jwt/README.md) | JWT 签发、验签与高并发吞吐 | golang-jwt/jwt v5、cristalhq/jwt v5、kataras/jwt |
| [`crypto/`](crypto/README.md) | 国密 SM2/SM3/SM4 与常密基线 | emmansun/gmsm、tjfoc/gmsm、标准库（SHA-256/AES-GCM/ChaCha20-Poly1305/ECDSA/Ed25519） |
| [`compress/`](compress/README.md) | 压缩吞吐与压缩率 | gzip、zstd、brotli、snappy、lz4 |
| [`cache/`](cache/README.md) | 二级缓存 L1 吞吐、并行、TTL 与命中率 | Ristretto、FreeCache、Theine、BigCache、go-cache、ttlcache、golang-lru |
| [`logx/`](logx/README.md) | 结构化日志 | zap、zerolog、slog、logrus |
| [`template/`](template/README.md) | 模板引擎兼容性与渲染性能 | html/template、text/template、Jet、Pongo2、Liquid、Raymond、Mustache、quicktemplate、templ |

## 测试平台（主表统一口径）

所有主表在 **x86_64 基准平台**上采集：

- AMD Ryzen AI MAX+ 395，16 核 32 线程，1 NUMA 节点，L3 64 MiB，30 GiB 内存；
- Ubuntu 26.04.1 LTS，Linux 7.0.0-38-generic，Go 1.27.2 linux/amd64；
- CPU governor `performance`，AVX-512F/BW/CD/DQ/VL + VBMI/VBMI2/VNNI/BF16；
- 每个实现单独进程运行、轮转顺序、多轮中位数；
- 每个模块的 `YD_BENCH_ONLY` 环境变量用于选择单一实现，避免同进程 codec 顺序与 GC 偏差；
- 库本身不接收核心数/内存参数：`ydx/sysinfo` 在运行时读取 GOMAXPROCS 与 cgroup/主机内存，自动决定并行度与缓冲上限；单核/多核测量只通过 `GOMAXPROCS` 模拟环境。

CPU 利用率与峰值 RSS 使用 Linux `/usr/bin/time -v`，对每个实现单独进程测量 3 轮取中位数。CPU% 是进程平均占用，多线程/RunParallel 场景可远高于 100%；RSS 是进程峰值常驻内存，包含 Go 运行时、测试二进制与夹具。具体计时参数见各模块 README。

## 运行

每个模块独立运行：

```bash
cd benchmarks/<模块>
go test ./...                         # 正确性/兼容性/夹具校验
go test -run '^$' -bench . -benchmem  # 同进程快速检查

# 模板模块另有「输出物化」口径：所有引擎写入可复用 bytes.Buffer
cd benchmarks/template
go test -run '^$' -bench BenchmarkEnginesMaterialized -benchmem -benchtime=2s -count=5

# 公平采样（以 base64 为例；amd64 默认即自研内核）
for c in stdlib cloudwego/base64x cristalhq/base64 yd-b64x; do
  YD_BENCH_ONLY=$c GOEXPERIMENT=simd go test -run '^$' -bench . -benchmem \
    -benchtime=1s -count=5
done
```

## 关键结论（详见各模块 README，全部实现均在同一张表中）

- **ws**：顺序回显在 32 B/256 B/1 KiB/4 KiB/64 KiB 五档均为最低延迟（4,043 / 4,177 / 4,255 / 5,205 / 16,776 ns；64 KiB 3,906 MB/s），唯一全程 `0 B/op、0 allocs/op`；并发 1 KiB 下 yd 预读模式最快（**1,455 ns、703.76 MB/s、0 allocs/op**），比 gws（2,069 ns、494.81 MB/s、2 allocs）快约 29.7%，同步模式为 2,309 ns / 443.55 MB/s；新增 `WriteBatch`/`WriteMessageBuffered`/`Flush` 批量写 API，流水线回显（1 KiB × 8）由 277.70 提升到 **1,366.11 MB/s（4.92×，零分配）**；读写缓冲与单条消息上限由 `ydx/sysinfo` 按 GOMAXPROCS 与内存自动调优。兼容性 18/18。
- **router**：param/deep/wildcard/miss 四类最快（1.1–1.6 ns），五类路径零分配；静态路径 httprouter 最快（0.9 vs yd 1.1 ns）；WebSocket 握手经 router 增加约 15 ns（4.2%）。
- **json**（2026-10-23 重测，`ydx/json v0.1.9`）：amd64 默认已是自研后端。单核 Marshal 在 n=1/100/1000 全部第一（比 go-json 快约 8.3%/20.7%/20.6%）；Unmarshal 在 n=100/n=1000 快约 12.9%/9.5%，n=1 慢约 4.2%，B/op 降到 195,462。多核 Marshal 在 1–32 核全部领先（32 核 12,022 vs 10,462 MB/s，+14.9%）；Unmarshal 32 核 6,336 vs 4,409 MB/s（+43.7%），CPU 1,748% vs 2,421%、RSS 71.0 vs 119.0 MiB。嵌入字段支配规则、omitzero、解码 `,string`、深度上限 10000、Unicode/自定义接口兼容与差分/fuzz 全部通过。
- **base64**（2026-10-23 重测，`ydx/base64 v0.1.8`，`GOEXPERIMENT=simd`）：amd64 默认已是自研内核。单核 1 KiB/64 KiB/1 MiB 编码与解码全部第一（64 KiB 62.5/28.0 GB/s，1 MiB 54.9/26.8 GB/s）；**32 B 编码/解码为 8.80/12.86 ns**（SIMD 阈值修正后较上一轮 10.20/14.18 明显收窄），仍落后 cloudwego（7.47/11.51），编码已超过 cristalhq（9.57），解码与其基本持平。并发调用 1–32 核全部第一（16 核 623.9/341.1 GB/s，32 核 633.9/366.5 GB/s）。`EncodeParallel`/`DecodeParallel` 不接收核心数，按 GOMAXPROCS 与输入大小自动选择；8 MiB 编码在 16 核达 171.6 GB/s。三套内核零向量寄存器溢出；全长度+大长度差分、越界哨兵、race 通过。
- **regexpx**（组件基准在 ydx 仓；amd64 与 arm64 均为一等支持，各自使用 SIMD 预过滤）：自研引擎在 32 个场景全部最快。本机 AVX2 上 `MatchString/literal` 33.1 ns（stdlib 466 ns），AVX-512 主机 10.8 ns（stdlib 222 ns）；`FindAll/email` 311 µs（stdlib 12.4 ms、go-re2 1.66 ms）。详见 `ydx/benchmarks/regexpx/README.md`；主模块不再依赖 `regexp2`。
- **jwt**：HS256 验签 743.3 ns / 2 allocs，领先 cristal（2,134 / 19）2.87×、kataras（2,284 / 19）3.07×、golang-jwt（3,348 / 50）4.50×；并发复用 38.26 ns / 0 allocs（26.1 M ops/s），领先 cristal 13.8×；ES256 39.7 µs / 21 allocs 全部第一；`SignJSON` 531.5 ns、结构体 617.4 ns，快于 cristal 的结构体路径 1,030 ns。
- **crypto**：后端由 tjfoc/gmsm v1.4.1 切换到 emmansun/gmsm v0.45.0。SM3 4 KiB 12,506→7,360 ns（1.70×）；HMAC-SM3 1,765→729 ns（2.42×，0 allocs）；PBKDF2 939,609→283,344 ns（3.32×）；SM4-GCM 4 KiB seal 45,290→5,409 ns（8.37×，757.3 MB/s）；SM2 签名 131,572→14,364 ns（9.16×）、验签 687,942→34,392 ns（20.0×）、加密 715,805→35,969 ns（19.9×）、解密 554,528→29,024 ns（19.1×）。SM2 签名/验签快于 ECDSA P-256；SM3/SM4 无 SHA-NI/AES-NI 对应指令，仍慢于 SHA-256/AES-GCM。
- **compress**：zstd/fastest text 压缩 8.04 GB/s、压缩率 0.002，zstd 解压 13.67 GB/s；snappy text 压缩 13.57 GB/s 但压缩率 0.049；klauspost gzip 约标准库 gzip 的 3.7 倍吞吐，压缩率相同；brotli/q9 json 压缩率最好（0.021）但吞吐与内存成本最高。
- **cache**（Windows 本机口径，详见 [`cache/README.md`](cache/README.md)）：命中率达到该容量轨迹的上限——5k 容量/50k 键下 Get/GetAppend/View 为 **73.10% / 73.09% / 73.08%**，高于 Ristretto/Theine/ttlcache/LRU 的 72.60%–72.61%（BigCache 80.02% 因 2 MB 上限下有效容量更大，不可横比）；独立 1 KiB 轨迹为 72.56%–72.58%。读取路径改为分片读锁 + 锁外加权采样、槽表自适应并引入大值侧车后：1 KiB 零拷贝 View **89.8 ns**、Get **439.4 ns**、GetAppend **198.3 ns**，View 快于 FreeCache/Ristretto/ttlcache，Get 快于 FreeCache 的复制 `Get`；64 B View **103.2 ns**、Get **131.9 ns**、GetAppend **119.9 ns**；16 线程并行读 Get **45.6 ns**、GetAppend **37.4 ns**、View **35.4 ns**，均快于 FreeCache（51.2/42.5/42.5）；写入 **110.9/166.8 ns**（64 B/1 KiB）、变化值写 **112.0/182.9 ns**，均快于 FreeCache（113.8/220.5、128.9/222.4）。go-cache/golang-lru/ttlcache/Ristretto/Theine 返回缓存内引用或缺少容量/TTL 约束，部分指标不能与防御性拷贝路径直接横比。
- **logx**：dynamic Info 111.7 ns/op、零分配，略快于 zerolog（112.9 ns）；prebound Info 为 103.1 ns，**慢于 zerolog 的 94.9 ns 约 8.6%**；禁用路径 prebound 为 1.8 ns、零分配，与 logrus 并列最快，dynamic 为 7.9 ns（slog 5.1 ns 更快）。
- **template**：编译式 yd-render 在 100 行页面上为 41.7 µs/**0 allocs**（单核中位数），比 html/template（1,055.9 µs/7,142）快 **25.3×**，比 text/template 快 5.9×，比 Jet 快 4.0×，比 templ 快 3.4×；代码生成式 quicktemplate 在 `io.Discard` 口径下为 27.0 µs/0 allocs（快 1.54×），但把输出物化到真实缓冲后为 39.0 µs vs 40.7 µs（**1.04×，基本持平**）——差距主要来自流式写出与仅转义 5 个 HTML 字符，而非解释执行。上下文转义矩阵中只有 html/template、yd-render 与 templ 同时拦截 `javascript:` URL，其余引擎仅做 HTML 转义。

基准数值对机器负载、温度与电源策略敏感，跨进程独立运行的波动可达约 15%。跨机比较请以同一平台的相对关系为准。
