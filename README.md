<div align="center">

# yd

**YunDev 全栈 Web 框架及脚手架**

Go 1.27 · Htmx 4.0 · Tailwind v4 —— 极致性能、极低认知负载、高生产力的 Go 全栈单体方案

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

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
yd docker     # 基于 scratch 的生产镜像（约十几 MB）

# 前端依赖治理：在线更新 + 离线独立使用
yd assets update          # 拉取并锁定 sha256（不匹配即拒绝写入）
yd assets verify --offline  # 离线校验，绝不触网
```

## 架构一览

```
┌──────────────────────── 前端（go:embed，零 CDN） ────────────────────────┐
│ Htmx 4.0 事件总线 · Tailwind v4 @theme 语义主题 · Alpine · 国密 JS 沙箱 │
└──────────────────────────────────┬──────────────────────────────────────┘
                                   │ HTML 片段（非 JSON）
┌──────────────────────────────────▼──────────────────────────────────────┐
│ 路由：自研基数树（支持 /:id.jpg、/:id-xx.png 混合分段与后缀捕获）        │
│ 中间件：Recover · RequestID · AccessLog · Compress(br/zstd/gzip) · CORS │
│         · RateLimit · i18n · JWT · SSE · WebSocket(RFC6455 自研) · gRPC │
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
config/        分层配置引擎（viper + pflag + .env，前缀 YD_）
afs/           afero 抽象文件系统门面（OS / MemFS / go:embed）
jsonx/ b64x/   硬件自适应 JSON / Base64（sonic+base64x ↔ go-json+stdlib，构建标签分流）
logx/          零拷贝结构化日志（sync.Pool 字节缓冲直写 io.Writer）
errx/          声明式哨兵错误原语（禁止 fmt.Errorf 的统一替代）
router/        自研基数树路由（单段内混合参数与静态后缀捕获）
render/        模板引擎（afero 模板集 · Dev 免缓存 · asset/dict/json/hx）
htmx/          Htmx 4 服务端辅助（HX-* 响应头 · hx-partial · OOB）
sse/ ws/       W3C 事件流广播 · RFC6455 WebSocket（自研握手与帧编解码）
jwt/           免反射 JWT（claims 扫描零分配，拒绝 alg:none 与算法混淆）
guomi/         国密 SM2/SM3/SM4（纯 Go；SM2 KDF 与 C1C3C2 按 GB/T 32918.4 实现）
middleware/    Recover · RequestID · AccessLog · CORS · RateLimit · gzip/br/zstd 协商
i18n/          YAML/TOML/JSON 目录 + Accept-Language q 值协商
openapi/       OpenAPI 3.0 文档（源码注解解析 + 路由树合并 + 零 CDN 查看器）
server/        HTTP/1.1 + H2C + HTTP/3(QUIC) + ACME + 优雅退出
grpcx/         gRPC 双通道（同端口按内容类型分流）
data/          gormx（PrepareStmt + 慢查询打点）· redisx（泛型缓存装饰器）· asynqx（加权队列）
app/           应用门面（路由/渲染/服务容器/优雅退出）
web/           内嵌前端资产（templates/ · static/ · assets.lock.json）
```

### 脚手架能力

```
cmd/yd/        CLI 入口
cli/           命令实现：new · dev · gen · build · docker · assets
scaffold/      yd new 的项目模板（golang-standards 布局 + 治理文件）
gen/           低代码生成器（DB Schema / Go Struct → 路由 · 控制器 · htmx 模板）
toolchain/     Tailwind 二进制解析与 CSS 构建 · 文件监听（抖动合并）
build/         跨平台静态打包（版本注入 · go:embed · 离线 vendor 模式）
docker/        scratch 多阶段镜像构建
assets/        前端依赖在线更新 + sha256 锁定 + 离线校验
e2e/           端到端验收（-tags e2e）
```

## 许可

Apache License 2.0 —— 见 [LICENSE](LICENSE)。归属团队 YunDev（[yun-dev.cn](https://yun-dev.cn) · [yun-dev.net](https://yun-dev.net)）。
