<div align="center">

# yd

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

yd 深度融合 **Go 1.27+ / Htmx 4.0 / Tailwind v4**，消除传统前后端分离（BFF + SPA）带来的高昂开发成本与复杂性，回归「全栈单体（Modern Monolith）」的高效开发模式：在保持媲美原生 API 框架的并发性能与吞吐量的同时，提供接近单页应用（SPA）的丝滑体验。

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
yd authz list             # 打印注册表（16 权限 / 4 角色，四种引用形态）
yd authz check            # 静态校验，有错非零退出（可进 CI）

# Shell 补全：四种 shell 的脚本从命令注册表生成
yd completion bash        # 打印脚本（source <(yd completion bash)）
yd completion install     # 探测 shell、写脚本并打印加载行（默认不动 rc 文件）

# 可选外部工具：betteralign / ko / tailwindcss / docker 的统一探测与安装
yd tools check            # 列出四个工具的状态与安装提示，只读、不触网
yd tools install          # 把缺失且可 go install 的项装进项目内 .yd/tools/（不改 PATH）
```

> `yd new` 收尾会探测一次缺失的工具：交互模式询问是否就地安装（默认是），
> 非交互场景用 `--install-tools`；`--yes` 只建项目、不触网。
> 工具**一律可选**，缺失只影响对应能力，不会让 `yd build` / `yd dev` / `go test` 失败。

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
core/          与领域无关的基础原语
  config/      分层配置引擎（viper + pflag + .env，前缀 YD_）
  afs/         afero 抽象文件系统门面（OS / MemFS / go:embed）
  jsonx/ b64x/ 硬件自适应 JSON / Base64（sonic ↔ go-json；cloudwego ↔ cristalhq ↔ stdlib，按尺寸分流；另有 `jsonx_native` / `b64x_native` 自研实验后端）
  logx/        零拷贝结构化日志（sync.Pool 字节缓冲直写 io.Writer）
  errx/        声明式哨兵错误原语（禁止 fmt.Errorf 的统一替代）
  version/     版本信息（-ldflags 注入）
http/          HTTP 与前端交付
  router/      自研基数树路由（混合参数后缀捕获 · HEAD/OPTIONS 语义 · 流式上传 · Range/ETag 下载）
  render/      模板引擎（afero 模板集 · Dev 免缓存 · asset/dict/json/hx）
  htmx/        Htmx 4 服务端辅助（HX-* 响应头 · hx-partial · OOB）
  sse/ ws/     W3C 事件流广播 · WebSocket RFC 6455/7692/7936/8441/9220（自研握手与帧编解码）
  i18n/        YAML/TOML/JSON 目录 + Accept-Language q 值协商
  openapi/     OpenAPI 3.0 文档（源码注解解析 + 路由树合并 + 零 CDN 查看器）
middleware/    Recover · RequestID · AccessLog · CORS · RateLimit · gzip/br/zstd 协商
security/      安全与鉴权
  jwt/         免反射 JWT（claims 扫描零分配，拒绝 alg:none 与算法混淆）
  guomi/       国密 SM2/SM3/SM4（纯 Go；SM2 KDF 与 C1C3C2 按 GB/T 32918.4 实现）
  auth/        租户/用户/角色/能力领域模型（gormstore 子包负责落库）
  authz/       角色与权限注册表、静态校验
runtime/       进程与运行时
  server/      HTTP/1.1 + H2C + HTTP/3(QUIC) + ACME + 优雅退出
  grpcx/       gRPC 双通道（同端口按内容类型分流）
  lifecycle/   关闭信号与长连接钩子
  app/         应用门面（路由/渲染/服务容器/优雅退出）
  service/     系统服务安装与运行（SCM/systemd/launchd）
data/          gormx（PrepareStmt + 慢查询打点）· redisx（泛型缓存装饰器）· asynqx（加权队列）
web/           内嵌前端资产（templates/ · static/ · assets.lock.json）
```

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

完整基准位于 [`benchmarks/`](benchmarks/README.md)，每个类别是一个独立 Go 模块，自带 `go.mod`/`go.sum` 与 README，不会把对比库带进主模块依赖。

| 类别 | 基准模块 | 核心结论 |
|---|---|---|
| WebSocket | [`benchmarks/ws`](benchmarks/ws/README.md) | 与 coder/gorilla/gobwas/gws 兼容性 18/18；32 B/256 B 延迟最低，64 KiB 吞吐 572.9 MB/s；唯一全程 `0 B/op、0 allocs/op` |
| Router | [`benchmarks/router`](benchmarks/router/README.md) | 参数/深层/通配/未命中路径最快（4.8–5.1 ns，未命中 8.8 ns），五类路径零分配；WebSocket 握手经 router 仅增加 62 ns（5.6%） |
| JSON | [`benchmarks/json`](benchmarks/json/README.md) | 默认与 sonic 一致；`jsonx_native` 自研后端（`-tags jsonx_native`）接近 go-json（276 vs 260 µs Marshal、593 vs 546 µs Unmarshal），领先 sonic/jsoniter/标准库 |
| Base64 | [`benchmarks/base64`](benchmarks/base64/README.md) | ≥1 KiB 时 cloudwego 与 `b64x` 编码约 10–12 GB/s、解码约 7–9 GB/s；标准库约 1.1 GB/s |
| 压缩 | [`benchmarks/compress`](benchmarks/compress/README.md) | zstd 综合最优（text 压缩 3.7 GB/s / 解压 7.5 GB/s）；klauspost gzip 约为标准库 gzip 的 4 倍吞吐，压缩率相同 |
| 日志 | [`benchmarks/logx`](benchmarks/logx/README.md) | `logx` 启用 Info 182 ns/op、零分配，快于 zerolog；禁用路径 20 ns/op、零分配 |

汇总口径、测试环境与完整表格见 [`benchmarks/README.md`](benchmarks/README.md)。基准数值对机器负载、温度与电源策略敏感，跨进程独立运行的波动可达约 15%。

```bash
cd benchmarks/<模块>
go test ./...                         # 正确性/兼容性
go test -run '^$' -bench . -benchmem  # 性能
```

## 许可

Apache License 2.0 —— 见 [LICENSE](LICENSE)。归属团队 YunDev（[yun-dev.cn](https://yun-dev.cn) · [yun-dev.net](https://yun-dev.net)）。
