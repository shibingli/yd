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
go install github.com/yun-dev/yd/cmd/yd@latest

# 初始化项目（交互式选择可选组件）
yd new myapp

cd myapp

yd dev        # 热重载开发：Go 自动编译重启 + Tailwind 监听 + 模板局部刷新
yd gen crud user --fields "name:string,age:int"
yd build      # 跨平台静态打包 → 单文件无依赖二进制
yd docker     # 基于 scratch 的生产镜像（约十几 MB）
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

```
cmd/yd/        CLI 入口
config/        分层配置引擎（viper + pflag + .env）
afs/           afero 抽象文件系统门面
jsonx/ b64x/   硬件自适应 JSON / Base64（构建标签分流）
logx/          零拷贝日志
router/        基数树路由（部分路径与后缀混合匹配）
render/ htmx/  模板引擎与 Htmx 4 服务端辅助
sse/ ws/       SSE 流与 RFC6455 WebSocket
jwt/ guomi/    免反射 JWT 与国密套件
middleware/    压缩 · CORS · 限流 · i18n · 访问日志
openapi/       OpenAPI 3.0 文档与内置查看器
server/ grpcx/ HTTP/1.1+H2C+HTTP/3+ACME 与 gRPC 双通道
data/          GORM · Redis · asynq 插件
app/           应用门面
web/           内嵌前端资产（templates/ static/ assets.lock.json）
scaffold/ gen/ build/ docker/ assets/ toolchain/  脚手架能力
```

## 许可

Apache License 2.0 —— 见 [LICENSE](LICENSE)。归属团队 YunDev（[yun-dev.cn](https://yun-dev.cn) · [yun-dev.net](https://yun-dev.net)）。
