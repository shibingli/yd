<div align="center">

# YD

**YunDev 全栈 Web 框架及脚手架**

Go 1.27 · Htmx 4.0 · Tailwind v4 —— 极致性能、极低认知负载、高生产力的 Go 全栈单体方案

[![GitHub](https://img.shields.io/badge/GitHub-shibingli%2Fyd-181717.svg?logo=github&logoColor=white)](https://github.com/shibingli/yd)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8.svg?logo=go&logoColor=white)](https://go.dev/dl/)
[![Htmx](https://img.shields.io/badge/Htmx-4.0-3366CC.svg)](https://htmx.org)
[![Tailwind CSS](https://img.shields.io/badge/Tailwind_CSS-v4-38BDF8.svg?logo=tailwindcss&logoColor=white)](https://tailwindcss.com)

</div>

---

## 这是什么

YD 深度融合 **Go 1.27+ / Htmx 4.0 / Tailwind v4**，消除传统前后端分离（BFF + SPA）带来的高昂开发成本与复杂性，回归「全栈单体（Modern Monolith）」的高效开发模式：在保持媲美原生 API 框架的并发性能与吞吐量的同时，提供接近单页应用（SPA）的丝滑体验。

- **前端零 Node 依赖**：Tailwind v4 编译由 Go 脚手架静默驱动，前端资产经 `go:embed` 全部打进二进制，严禁运行时 CDN。
- **前后端同构安全**：后端纯 Go 国密套件（SM2/SM3/SM4）与前端国密沙箱（纯 JS）构成闭环，表单可声明式做传输前置加密。
- **离线可用**：所有前端依赖支持在线更新（`yd assets update`）并锁定 sha256；离线环境 `yd assets verify --offline` 即可独立运行。

## 快速开始

```bash
# 安装 CLI
go install github.com/shibingli/yd/cmd/yd@latest

# 初始化项目（交互式选择可选组件）
yd new myapp

cd myapp

yd dev        # 热重载开发：Go 自动编译重启 + Tailwind 监听 + 模板局部刷新
yd gen crud user --fields "name:string,age:int"
yd build      # 跨平台静态打包 → 单文件无依赖二进制

# 镜像打包：两条并列路径，产物一致（CGO_ENABLED=0 静态链接，约十几 MB）
yd docker     # 走 Dockerfile 多阶段构建（golang → scratch）
yd ko         # 无 Dockerfile：ko 从 Go 源码直出镜像（可选外部工具，不进 go.mod）
yd ko --repo registry.example.com/myapp --tags v1.0.0 \
      --platforms linux/amd64,linux/arm64 --push   # 多平台 + 推送

# 服务化：安装为 Windows 服务 / systemd / launchd，停止时走优雅退出
yd service install --name myapp

# 前端依赖治理：在线更新 + 离线独立使用
yd assets update          # 拉取并锁定 sha256（不匹配即拒绝写入）
yd assets verify --offline  # 离线校验，绝不触网

# 依赖升级与模板版本同步：脚手架版本从 go.mod / assets.lock.json 派生，不手抄
yd versions check         # 离线校验生成文件与真源一致（CI 可用）
yd versions upgrade       # Go 模块 + npm 资产一并升级，并重新生成脚手架常量

# 权限：注册表是角色与权限的唯一来源，check 抓拼写错误与裸字符串
yd authz list             # 打印注册表（24 权限 / 4 角色，四种引用形态）
yd authz check            # 静态校验，有错非零退出（可进 CI）

# Shell 补全：四种 shell 的脚本从命令注册表生成，命令/标志/位置参数（动作词与枚举值）一起覆盖
yd completion bash        # 打印脚本（source <(yd completion bash)）
yd completion install     # 探测 shell、更新脚本与已有加载行（默认不动 rc 文件）

# 可选外部工具：betteralign / ko / tailwindcss / minify / docker 的统一探测与安装
yd tools check            # 列出五个工具的状态与安装提示，只读、不触网
yd tools install          # 把缺失且可 go install 的项装进项目内 .yd/tools/（不改 PATH）
yd tools install tailwindcss  # 下载 Tailwind standalone 二进制到 .yd/tools/
```

> `go install` 的 `GOBIN` / `GOPATH` / `GOMODCACHE` / `GOCACHE` 全部隔离在 `.yd/tools` 之下，
> 默认使用国内可用的 `GOPROXY` 链（`GOSUMDB=off`）；显式导出的 `GOPROXY` / `GOSUMDB` 优先。
> `yd assets update` / `yd assets add`、`yd versions upgrade` 的下载与 Tailwind 二进制下载都支持
> `http.proxy`（HTTP 代理，或 `YD_HTTP_PROXY`）；Tailwind 还可用 `tailwind.url`（镜像地址，或 `YD_TAILWIND_URL`）。
> 未配置代理时遵循标准 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`。

> `yd new` 收尾会探测一次缺失的工具：交互模式询问是否就地安装（默认是），
> 非交互场景用 `--install-tools`；`--yes` 只建项目、不触网。
> 工具**一律可选**，缺失只影响对应能力，不会让 `yd build` / `yd dev` / `go test` 失败。

## 文档

- **[yd 使用手册](docs/yd-usage.md)** —— 命令参考、配置、鉴权与多租户、缓存、消息中心、部署与完整示例
- [二级缓存](docs/cache.md) · [消息中心](docs/message-center.md) · [中间件指标](docs/middleware-metrics.md) · [图标](docs/icon-usage.md) · [渲染引擎](docs/render-usage.md) · [验收记录](docs/verification.md)

## 架构一览

```
┌──────────────────────── 前端（go:embed，零 CDN） ────────────────────────┐
│ Htmx 4.0 事件总线 · Tailwind v4 @theme 语义主题 · Alpine · 国密 JS 沙箱 │
└──────────────────────────────────┬──────────────────────────────────────┘
                                   │ HTML 片段（非 JSON）
┌──────────────────────────────────▼──────────────────────────────────────┐
│ 路由：自研基数树（支持 /:id.jpg、/:id-xx.png 混合分段与后缀捕获）        │
│ 中间件：Recover · RequestID · AccessLog · Compress(br/zstd/gzip) · CORS │
│         · RateLimit · i18n · JWT · SSE · WebSocket(RFC 6455/7692/7936/8441/9220) · gRPC │
│ 服务器：HTTP/1.1 + H2C + HTTP/3(QUIC) + ACME 自动证书                   │
├─────────────────────────────────────────────────────────────────────────┤
│ 原语：viper 分层配置 · afero 抽象 FS · 零拷贝日志 · 自适应 JSON/Base64   │
├─────────────────────────────────────────────────────────────────────────┤
│ 数据：GORM(PrepareStmt) · go-redis(单机/哨兵/集群) · asynq(加权队列)     │
│ 安全：SM2 / SM3 / SM4（纯 Go）                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

## 配置优先级

命令行参数（pflag） > 环境变量（`YD_` 前缀，含 `.env`） > 配置文件（YAML/TOML/JSON） > 框架默认值

```bash
YD_DB_MAXCONN=200           # → db.maxconn
YD_SERVER_PORT=9090         # → server.port
yd dev --port 7070          # 命令行最高优先级
```

## 目录结构

### 框架库

```
# 可复用组件位于独立仓库 github.com/shibingli/ydx，按版本依赖：
#   errs sysinfo buffpool protocol base64 json afs config log regexpx router
#   gorm redis asynq cache sse websocket render i18n openapi jwt guomi middleware + middleware/* 插件
core/
  envx/        环境名归一化（development/dev、production/prod）
  version/     版本信息（-ldflags 注入）
http/          HTTP 与前端交付
  htmx/        Htmx 4 服务端辅助（HX-* 响应头 · hx-partial · OOB）
security/      安全与鉴权
  auth/        租户/用户/角色/能力领域模型（gormstore 子包负责落库）
  authz/       角色与权限注册表、静态校验
runtime/       进程与运行时
  server/      HTTP/1.1 + H2C + HTTP/3(QUIC) + ACME + 优雅退出
  grpcx/       gRPC 双通道（同端口按内容类型分流）
  lifecycle/   关闭信号与长连接钩子
  app/         应用门面（路由/渲染/服务容器/优雅退出）
  service/     系统服务安装与运行（SCM/systemd/launchd）
messaging/     站内消息中心（消息、好友、附件、实时广播）
tooling/       脚手架、生成器、构建、镜像、资产、诊断与 CLI
web/           内嵌前端资产（templates/ · static/ · assets.lock.json）
repotest/      仓库守卫
```

缓存的一致性模型、零拷贝读取与 GORM 插件用法见 [`docs/cache.md`](docs/cache.md)。

### WebSocket 标准与传输

| RFC | 能力 | 前提 |
|---|---|---|
| 6455 | 核心协议：握手、帧、掩码、分片、控制帧、关闭码 | 无 |
| 7692 | permessage-deflate（两端显式开启） | `ws.Options.EnableCompression` / `DialOptions.EnableWriteCompression` |
| 7936 | 子协议名大小写敏感 | 无 |
| 8441 | WebSocket over HTTP/2（含 h2c） | 服务端进程需 `GODEBUG=http2xconnect=1`；H2 客户端需 `-tags http2legacy`（Go 1.27 的 x/net/http2 包装了 http.Transport） |
| 9220 | WebSocket over HTTP/3 | 无需额外环境变量 |

```bash
# 强制 RFC 8441：yd dev / yd service install / yd docker 会自动写入 GODEBUG=http2xconnect=1
YD_SERVER_HTTP2_EXTENDED_CONNECT=true
```

服务端配置 `server.http2.extended_connect: true` 时，裸二进制在 POSIX 上通过 `syscall.Exec`、
Windows 控制台通过子进程重启完成配置；Windows 服务必须经 `yd service` 安装。
HTTP/2 与 HTTP/3 的处理器必须阻塞到连接结束——返回即结束流；HTTP/1.1 的 hijack 连接不受此限制。
路由无需额外注册 CONNECT：Extended CONNECT 会自动回退到同一 GET 路由。

### 脚手架能力

```
cmd/yd/        CLI 入口
tooling/       开发期工具与脚手架
  cli/         命令实现：new · dev · gen · build · docker · assets
  scaffold/    yd new 的项目模板（golang-standards 布局 + 治理文件）
  gen/         低代码生成器（DB Schema / Go Struct → 路由 · 控制器 · htmx 模板）
  toolchain/   Tailwind 二进制解析与 CSS 构建 · 文件监听（抖动合并）
  build/       跨平台静态打包（版本注入 · go:embed · 离线 vendor 模式）
  docker/      scratch 多阶段镜像构建
  assets/      前端依赖在线更新 + sha256 锁定 + 离线校验
  align/       betteralign 解析与迭代对齐
  ko/          无 Dockerfile 镜像打包（外部 CLI）
  tools/       可选外部工具的统一探测与安装入口
  completion/  Shell 补全脚本生成与安装
e2e/           端到端验收（-tags e2e）
repotest/      仓库治理守卫（治理文件与规范的可执行断言）
```

## 性能与兼容性指标

所有基准已统一到 ydx 仓的 [`benchmarks/`](https://github.com/shibingli/ydx/tree/main/benchmarks)
（14 个独立 Go 模块）；yd 仓不再保留 `benchmarks/` 目录。主表在 **x86_64 参考主机**
（AMD Ryzen AI MAX+ 395，16 核 32 线程，30 GiB，Ubuntu 26.04.1，Go 1.27.3，AVX-512
VBMI2）上以 `GOMAXPROCS=32`、`-count=5`、`-benchtime=500ms` 采集中位数；arm64 与旧指令集
x86_64 的冒烟结果、低效指标的优化前后对比与公平性说明见
[`optimization-report.md`](https://github.com/shibingli/ydx/tree/main/benchmarks/optimization-report.md)。

| 类别 | 基准模块 | 核心结论（同一轮同机中位数；详表见模块 README） |
|---|---|---|
| WebSocket | [`benchmarks/websocket`](https://github.com/shibingli/ydx/tree/main/benchmarks/websocket) | 32 B 回显 4.07 µs（gorilla 4.26 / gobwas 4.34 / coder 5.59 / gws 6.46）、64 KiB 回显 16.59 µs（gorilla 18.58 / gws 18.25）；64×1 KiB 批量写 14.06 µs（coder 73.7 / gorilla 124.3）；并发回显 readahead 变体 1.41 µs 领先全部实现；gobwas/gws 与兼容性测试已并入 |
| Router | [`benchmarks/router`](https://github.com/shibingli/ydx/tree/main/benchmarks/router) | 静态路由经 `GETRaw` 8.40 ns（httprouter 12.86）；参数 18.9 ns（httprouter 29.0）、通配 18.9 ns（gin 25.6 / httprouter 28.1）、未命中 25.8 ns（gin 28.1） |
| JSON | [`benchmarks/json`](https://github.com/shibingli/ydx/tree/main/benchmarks/json) | 5 字段载荷：Marshal 单对象 60.9 ns（goccy 89.8 / sonic 87.1），64 对象 2.59 µs（goccy 4.23 / sonic 2.99）；Unmarshal 单对象 119.1 ns（goccy 125.9），64 对象 6.51 µs（goccy 6.87）；1024 对象 101.5 µs（goccy 110.7）。7 字段/长小数/`time.Time` 载荷：n=1000 Marshal 89.7 µs（go-json 116.2）、Unmarshal 174.3 µs（195.2）；n=1 Unmarshal 214.2 ns，领先 go-json（245.6 ns）约 12.8% |
| Base64 | [`benchmarks/base64`](https://github.com/shibingli/ydx/tree/main/benchmarks/base64) | 32 B 编码 5.58 ns（cristalhq 9.57 / stdlib 14.38）、解码 10.47 ns（12.93 / 19.92）；1 KiB/64 KiB/1 MiB 编码与解码全部第一，1 MiB 编码 55.5 GB/s、解码 27.1 GB/s。cloudwego/base64x 的 JIT 在 Go 1.27.3 上崩溃，已从默认套件移除并记录 |
| 正则 | [`benchmarks/regexpx`](https://github.com/shibingli/ydx/tree/main/benchmarks/regexpx) | 匹配/查找/批量扫描全部最快：`FindAll/email` 94.98 µs（stdlib 5.679 ms、go-re2 732.3 µs），`FindAll/date` 110.8 µs（go-re2 705.5 µs）。简单模式编译仍慢于标准库（启动期一次性成本） |
| JWT | [`benchmarks/jwt`](https://github.com/shibingli/ydx/tree/main/benchmarks/jwt) | HS256 签发 1.257 µs（map 声明；类型化结构体 535 ns，cristalhq 同形态 1.061 µs），jwx 2.237 / golang-jwt 2.544；ES256 签发 19.81 µs（jwx 20.70 / golang-jwt 21.08）；HS256 验签 782.6 ns（golang-jwt 3.17 µs、jwx 4.73 µs）；并发验签 145.8 ns（1.117 µs）；cristalhq/kataras 对比已并入 |
| 国密/常密 | [`benchmarks/crypto`](https://github.com/shibingli/ydx/tree/main/benchmarks/crypto) | yd 使用的 `ydx/guomi` 委托的维护版 gmsm 在 SM2/SM3/SM4 同算法族内全面领先旧版 tjfoc；SM2 签名/验签快于 ECDSA P-256。SM3/SM4 无 SHA-NI/AES-NI 对应指令，绝对吞吐仍低于硬件加速的 SHA-256/AES-GCM |
| 压缩 | [`benchmarks/compress`](https://github.com/shibingli/ydx/tree/main/benchmarks/compress) | zstd/fastest、klauspost gzip、snappy、brotli、lz4 的吞吐/压缩率同表对比；yd 的 HTTP/WebSocket 选择 klauspost gzip/zstd；与 gzhttp 在 16 KiB 用例上持平（5.27 µs 对 5.21 µs） |
| 日志 | [`benchmarks/log`](https://github.com/shibingli/ydx/tree/main/benchmarks/log) | 5 字段 Info 134.1 ns（zerolog 168.6）、8 字段 206.1 ns（228.0）、并发写入 8.08 ns（10.53）、prebound 86.6 ns（zerolog 96.2）；禁用 dynamic 9.87 ns 仍略慢于 zerolog 7.33 ns（字段由调用方构造） |
| 中间件 | [`benchmarks/middleware`](https://github.com/shibingli/ydx/tree/main/benchmarks/middleware) | CORS 36.7 ns（rs/cors 53.2）、恢复 7.85 ns（chi 8.62）、请求 ID 170.2 ns（chi 275.0）；16 KiB gzip 5.268 µs 与 gzhttp 5.213 µs 持平；限流 136.5 ns，x/time/rate 59.92 ns（全局、不写头）、补头后 127.8 ns、按 IP 分桶后 139.0 ns，见下方口径说明 |
| 缓存 | [`benchmarks/cache`](https://github.com/shibingli/ydx/tree/main/benchmarks/cache) | L1 Set 49.07 ns，领先 freecache（75.5）/bigcache（95.8）；防御性副本 Get 45.0 ns（bigcache 43.1 ns，差异在噪声内），零拷贝 `GetAppend` 38.3 ns、`View` 36.5 ns；`sync.Map`/ristretto 返回引用，不能与副本路径直接横比。TTL、Mixed、命中率轨迹与 theine/ttlcache/golang-lru 对比已并入 |
| 模板引擎 | [`benchmarks/template`](https://github.com/shibingli/ydx/tree/main/benchmarks/template) | yd-render 100 行页面物化 18.78 µs/0 allocs（quicktemplate 21.46 µs），流式 `io.Discard` 18.33 µs（quicktemplate 15.15 µs，编译期生成代码直写）；绑定渲染微基准 253.7 ns（html/template 3.43 µs、text/template 2.12 µs）；html/template 473.2 µs；转义矩阵只有 html/template、yd-render、templ 同时拦截 `javascript:` URL |
| 缓冲池 | [`benchmarks/buffpool`](https://github.com/shibingli/ydx/tree/main/benchmarks/buffpool) | 1 KiB Get/Put 9.3 ns / 4 KiB 9.9 ns（bytebufferpool 9.4 / 9.5，差 3–4% 在噪声内）；并发 0.58 ns 对 bytebufferpool 30.36 ns；裸 `sync.Pool` 7.1–7.3 ns（无尺寸分级，口径不同） |

**口径说明**：限流基准中的 x/time/rate 默认变体使用单个全局 limiter 且不写 `X-RateLimit-*` 头，
ydx 默认按 IP 分桶并写 3 个响应头；基准另给出补头（127.8 ns）与按 IP 分桶（139.0 ns）两个
同口径变体，ydx 的 136.5 ns 在该口径下领先 1.8%（噪声内视为持平）。缓存 `Get` 返回调用方
拥有的副本，而 `sync.Map`/ristretto/go-cache/golang-lru 返回缓存内引用；带 TTL 的缓存读写
还要支付本机约 43 ns 的 `time.Now()`。缓冲池的 `sync.Pool` 基线为每个尺寸预建一个池，
不含尺寸分级；SSE 的手写基线不做 CR/LF 规整、不加锁、不 flush。以上差异已在
[`optimization-report.md`](https://github.com/shibingli/ydx/tree/main/benchmarks/optimization-report.md)
中逐项说明，报告中保留原始数字，不以删除对比项或放宽语义的方式制造“全胜”。

```bash
cd ydx/benchmarks/<模块>
go test ./...                         # 正确性/兼容性/夹具校验
go test -run '^$' -bench . -benchmem  # 同进程快速检查
```

## 许可

Apache License 2.0 —— 见 [LICENSE](LICENSE)。归属团队 YunDev（[yun-dev.cn](https://yun-dev.cn) · [yun-dev.net](https://yun-dev.net)）。
