# yd 使用手册

**yd** 是 YunDev 全栈 Web 框架与脚手架：Go 1.27 + Htmx 4 + Tailwind CSS v4，
默认单二进制、`CGO_ENABLED=0` 静态链接、前端资产 `go:embed`、零运行时 CDN。

本手册面向使用 `yd` 创建、开发、测试与部署项目的开发者。框架内部的约束与实现细节见
[`CLAUDE.md`](../CLAUDE.md) 与 [`docs/verification.md`](verification.md)。

> 适用版本：yd v1.7.0+（Go 1.27.3+）。命令、配置与示例均以仓库当前实现为准；
> 组件实现位于 [`github.com/shibingli/ydx`](https://github.com/shibingli/ydx)。

## 文档地图

| 文档 | 内容 |
|---|---|
| [`README.md`](../README.md) | 项目概览、快速开始、性能指标 |
| [`docs/cache.md`](cache.md) | 二级缓存 `ydx/cache` 的一致性模型与 API |
| [`docs/gorm-dialects.md`](gorm-dialects.md) | GORM 四方言（MySQL / PostgreSQL / SQLite / 达梦 DM8）的配置、能力矩阵与跨方言表达式 |
| [`docs/message-center.md`](message-center.md) | 站内消息中心的配置、API、WebSocket 协议与多节点部署 |
| [`docs/realtime-transports.md`](realtime-transports.md) | WebSocket / WebTransport / SSE 的选型、单机与多机、安全、背压与兼容性矩阵 |
| [`docs/node-identity.md`](node-identity.md) | 节点身份的来源、稳定性边界、安全、性能与唯一性，以及物理机/虚机/容器/Kubernetes 部署方式 |
| [`docs/runtime-gc.md`](runtime-gc.md) | 运行时 GC 调度 `ydx/gc` 的策略预设、繁忙跳过、内存压力强制执行、安全边界与观测 |
| [`docs/redis-asynq.md`](redis-asynq.md) | Redis 直接命令 / Redis Pub/Sub / asynq 的分区规则、可靠性边界与运维要点 |
| [`docs/middleware-metrics.md`](middleware-metrics.md) | 中间件运行时指标与自定义指标 |
| [`docs/icon-usage.md`](icon-usage.md) | 离线图标 sprite 的生成、校验与故障排查 |
| [`docs/render-usage.md`](render-usage.md) | 模板渲染引擎用法 |
| [`docs/verification.md`](verification.md) | 历史验收记录与基准数据 |

## 目录

1. [安装与升级](#1-安装与升级)
2. [五分钟上手](#2-五分钟上手)
3. [项目结构](#3-项目结构)
4. [配置与环境变量](#4-配置与环境变量)
5. [命令参考](#5-命令参考)
6. [开发流程](#6-开发流程)
7. [鉴权与多租户](#7-鉴权与多租户)
8. [数据、缓存与消息中心](#8-数据缓存与消息中心)
9. [前端、资产与实时推送](#9-前端资产与实时推送)
10. [可观测性](#10-可观测性)
11. [测试与质量门禁](#11-测试与质量门禁)
12. [部署](#12-部署)
13. [依赖与版本管理](#13-依赖与版本管理)
14. [完整示例：从零做一个任务管理应用](#14-完整示例从零做一个任务管理应用)
15. [故障排查](#15-故障排查)

---

## 1. 安装与升级

### 1.1 安装 CLI

```bash
# 需要 Go 1.27.3 或更高版本
go install github.com/shibingli/yd/cmd/yd@latest

yd --version
```

`yd` 是单一静态二进制；安装后不依赖 Node.js、Docker 或任何前端构建工具。
Tailwind、betteralign、ko、minify 与 docker 都是**可选外部工具**，缺失只影响对应能力：

```bash
yd tools check          # 只读探测，列出路径/版本与安装提示
yd tools install        # 把缺失且可 go install 的工具装进项目内 .yd/tools/
yd tools install tailwindcss   # 下载 Tailwind standalone 二进制
```

> 安装落点固定为 `<项目>/.yd/tools`，不会改动 `PATH`，也不会写入 `~/go/bin`。
> `yd new --yes` 只建项目、不触网；需要自动安装时显式传 `--install-tools`。

### 1.2 升级

```bash
yd versions upgrade          # 升级 Go 模块 + 前端资产并重新生成脚手架版本常量
yd versions upgrade --dry-run
yd versions check            # 离线校验生成文件与真源一致，CI 可用
```

升级后必须重新跑质量门禁：

```bash
go build ./... && go vet ./... && go test ./... -race
```

---

## 2. 五分钟上手

```bash
yd new myapp
cd myapp
yd dev
```

Htmx 4、Tailwind CSS v4 与 Alpine.js 3 始终集成，不需要选择；i18n 与全部安全 Htmx 4 扩展默认包含；交互式向导只询问项目名、模块、数据库、gRPC 与协议，不再询问 i18n 或扩展子集。脚本或 CI 中直接给参数：

```bash
yd new myapp \
  --module example.com/myapp \
  --db sqlite \
  --yes
```

`yd new` 会写入 130+ 个文件，包括：

- 可运行的多租户鉴权与后台管理；
- 消息中心、好友关系与实时推送；
- 默认租户、`admin` 账号、四个内置角色与权限绑定；
- Tailwind v4 样式、Htmx 4 扩展、离线图标 sprite；
- `CLAUDE.md` / `AGENTS.md` / `docs/developer_guidelines.md` 治理文件。

启动后访问 `http://127.0.0.1:8080`：

- 未登录访问 `/` 会跳到 `/login`；
- 登录成功进入 `/messages`，后台管理在 `/mgr`；
- 接口文档在 `/mgr/docs`，OpenAPI JSON 在 `/api/v1/openapi.json`；
- 健康检查 `/healthz`。

开发环境的种子管理员：

| 项 | 默认值 | 覆盖方式 |
|---|---|---|
| 租户 | `default` | `seed.tenant` / `YD_SEED_TENANT` |
| 登录名 | `admin` | `seed.username` / `YD_SEED_USERNAME` |
| 口令 | 开发环境回退到文档化口令 `admin123` | `seed.admin_password` / `YD_SEED_ADMIN_PASSWORD` |

> **生产安全红线**：非 `development` 环境未配置口令时，进程**拒绝写入种子数据**。
> 生产部署必须显式设置 `YD_APP_ENV=production` 并注入 `YD_SEED_ADMIN_PASSWORD` 与
> `YD_AUTH_MASTER_KEY`。首次登录后立即改密。

第一个 CRUD：

```bash
yd gen crud task --fields "title:string:required,max=120,done:bool,due:time"
yd dev
# 打开 http://127.0.0.1:8080/task
```

生产构建：

```bash
yd build
./bin/myapp
```

---

## 3. 项目结构

```text
myapp/
├── cmd/app/                  # 应用入口（前台 / 服务模式共用一条优雅退出路径）
├── configs/
│   ├── config.yaml           # 基线配置（入库）
│   └── config.local.yaml.template
├── data/                     # SQLite 数据库与附件（.gitkeep 占位）
├── docs/                     # 项目文档与开发规范
├── internal/
│   ├── controller/           # HTML 片段控制器（htmx）
│   ├── meta/                 # 项目名、模块、版本、种子提示
│   ├── middleware/           # security / session / tenant / 指标描述符
│   ├── model/                # 领域模型
│   ├── repository/           # 存储装配与种子数据
│   ├── router/               # 路由注册、OpenAPI 组装、生成模块挂载点
│   └── service/              # 业务服务、消息中心、缓存、指标
├── web/
│   ├── embed.go              # go:embed 入口
│   ├── assets.lock.json      # 前端资产版本与 sha256
│   ├── i18n/{zh-CN,en}.yaml
│   ├── static/{css,js,img}   # 样式、脚本、图标 sprite
│   └── templates/            # components/ 与 partials/（共享），portal/ 与 mgr/（角色页面）
├── Dockerfile
├── go.mod
└── .yd.json                  # 脚手架元数据（项目名、资产根）
```

**门户与后台隔离**：`/`、`/messages`、`/friends`、`/account` 属于门户；
`/mgr/**` 属于后台管理。页面树 `portal/**` 与 `mgr/**` 禁止互相 include，但两者共用
`templates/components/**` 与树根 `templates/partials/**` 的同一套组件/外壳；角色差异由
`.Portal` / `.Mgr` 数据键选择。成员 API 在 `/api/portal/v1/**`，管理 API 在 `/api/v1/**`。

---

## 4. 配置与环境变量

### 4.1 优先级

```text
命令行参数 > 环境变量（含 .env） > 配置文件 > 框架默认值
```

环境变量前缀固定 `YD_`，下划线映射为多级嵌套：

```bash
YD_SERVER_PORT=9090          # → server.port
YD_DB_MAXCONN=200            # → db.maxconn
YD_AUTH_MASTER_KEY=...       # → auth.master_key
YD_MESSAGE_NODE_ID=node-1    # → message.node_id
```

运行环境 `app.env` / `YD_APP_ENV` 接受 `development`/`dev`、`production`/`prod`，
忽略大小写与首尾空白。**生产环境必须显式设置 `production`**，否则默认按开发环境处理。

配置目录默认是进程工作目录下的 `configs`；`YD_CONFIG_DIR` 可把它指向别处。`yd docker` 构建的镜像
固定设置 `YD_CONFIG_DIR=/configs`，`yd ko` 构建的镜像由 ko 注入 `KO_DATA_PATH`，应用会优先读取
`$KO_DATA_PATH/configs`，两者都把 `configs/config.yaml` 的基线配置随镜像提供（详见 5.5 / 5.6）。

### 4.2 常用配置

```yaml
app:
  name: myapp
  env: development

node:
  id: ""                     # 显式身份；多副本生产建议设置
  id_file: data/node-id      # 持久身份文件；容器必须挂到每实例独立卷
  require_persistent: false  # true：身份无法持久化时拒绝启动
  allow_shared_seed: false   # 容器内允许宿主种子（仅单容器主机）
  salt: ""                   # 推导盐；不是密钥

server:
  port: 8080
  h2c: true
  http2:
    extended_connect: true    # RFC 8441；为 true 时 yd dev / yd service install / yd docker 自动注入 GODEBUG
  http3:
    enabled: false            # HTTP/3 (QUIC)
    port: 0                   # 0 表示复用 TCP 端口
    webtransport: false       # HTTP/3 上的 WebTransport（需 TLS）
  read_timeout: 15s
  write_timeout: 30s

log:
  level: info                 # debug | info | warn | error
  format: json                # json | console
  caller: true

realtime:
  node_id: ""                 # 留空回退 node.id 与身份文件
  queue_size: 256             # 每个订阅者的有界队列
  max_message_bytes: 1048576  # 单条消息上限
  slow_policy: drop           # drop | close
  channel_prefix: "yd:rt:"    # Redis 频道命名空间，同一部署内唯一

seed:
  tenant: default
  username: admin
  admin_password: ""          # 生产环境用 YD_SEED_ADMIN_PASSWORD 注入

auth:
  master_key: ""              # 生产环境用 YD_AUTH_MASTER_KEY 注入
  token_ttl: 2h
  gorm:
    query_timeout: 0

db:
  driver: sqlite              # sqlite | mysql | postgres | dm
  dsn: "file:data/myapp.db?_txlock=immediate&_loc=UTC"
  maxconn: 100
  maxidle: 10
  slow_threshold: 200ms
  create_database: true       # 目标库不存在时自动建库（默认开启）

redis:
  mode: single                # single | sentinel | cluster
  addr: 127.0.0.1:6379
  password: ""
  db: 0

cache:
  prefix: "yd:cache:"
  lease: 30s
  fail_open: true
  max_cost: 67108864
  gorm:
    enabled: true               # 全局缓存 SELECT
    namespace: gorm
    ttl: 30s
    ignore_tables: [yd_auth_users]   # 凭据/状态永不缓存
    sensitive_tables:               # 鉴权成员/角色/权限/租户：独立命名空间 + 短 TTL
      - yd_auth_tenants
      - yd_auth_user_tenants
      - yd_auth_roles
      - yd_auth_role_permissions
    sensitive_namespace: gorm.sensitive
    sensitive_ttl: 5s

message:
  recall_window: 5m
  edit_window: 5m
  max_attachments: 10
  max_attachment_size: 20971520
  max_group_members: 50
  node_id: ""                 # 兼容别名；留空使用 node.id 与持久身份文件

service:
  name: myapp
  display_name: myapp
  arguments: [-service, run]
  restart_on_exit: true
  env:
    - YD_SERVER_PORT=8080
```

> `db.dsn` 的时区、字符集与 SQLite 参数由 `ydx/gorm.PresetDSN` 统一补齐；
> SQLite 必须保留 `_txlock=immediate`，**不要**加入 `_time_format`（会让该参数静默失效）。
> 需要本地时区时在 DSN 中显式写 `loc=Local`。

`db.driver` 支持 `sqlite` / `mysql` / `postgres` / `dm`（达梦 DM8）。达梦的示例 DSN 形如
`dm://SYSDBA:SYSDBA@127.0.0.1:5236`，预置层会补 `timeZone=0`（UTC）；schema 只是命名空间，
由实例侧预建，需要时在 DSN 里追加 `?schema=NAME`。运行时可用
`gorm.DialectOf(db)` 取得方言能力，并用 `gorm.Upsert` / `JSONExtract` / `ILike` /
`Concat` / `Random` / `LockClause` 构造跨方言表达式，不必按驱动名分支。

四种方言的完整用法（预置参数、能力矩阵、各库注意事项、迁移与排错）见
[`docs/gorm-dialects.md`](gorm-dialects.md)。

### 4.3 `.env`

```bash
cp .env.template .env
# .env 已被 .gitignore 忽略，不要提交
```

```dotenv
YD_APP_ENV=development
YD_SERVER_PORT=8080
YD_AUTH_MASTER_KEY=change-me-in-production
YD_SEED_ADMIN_PASSWORD=change-me-too
YD_DB_DSN=file:data/myapp.db?_txlock=immediate&_loc=UTC
```

---

## 5. 命令参考

所有命令都支持全局标志：

| 标志 | 说明 |
|---|---|
| `-c, --config` | 配置文件路径 |
| `--env-file` | `.env` 文件路径 |
| `--no-color` | 关闭 ANSI 颜色 |
| `-q, --quiet` | 只输出错误 |
| `-v, --verbose` | 调试日志 |
| `--version` | 打印版本并退出 |

### 5.1 `yd new`

```bash
yd new <项目名> [flags]
```

| 标志 | 说明 |
|---|---|
| `--module` | Go 模块路径，默认 `github.com/yun-dev/<name>` |
| `--db` | `sqlite` / `mysql` / `postgres` / `dm` / `none` |
| `--grpc` | 开启 gRPC 双通道 |
| `--i18n` | 国际化默认开启；用 `--i18n=false` 显式关闭 |
| `--htmx-ext` | 省略时加载全部安全扩展：`ws,sse,preload,targets,head,upsert,pending,multipart,download,browser-indicator,history-cache,live,prompt,ptag,alpine`。显式传入时只加载所列扩展（`alpine` 恒开启），并可追加仅显式选择的策略层 `csp`（需 nonce）、`compat` / `htmx-2-compat`（恢复 htmx 2 语义）。Htmx、Tailwind 与 Alpine 本身不是开关 |
| `--license` | 许可证标识，默认 `Apache-2.0` |
| `--local-replace` | 指向本地 yd 检出，写入 `go.mod` 的 replace（开发期） |
| `--install-tools` | 把缺失的可选工具装进新项目 |
| `--force` | 覆盖与模板不一致的文件 |
| `-y, --yes` | 全部取默认值，不提问；不触网 |

```bash
yd new blog --module example.com/blog --db sqlite --yes
```

### 5.2 `yd dev`

```bash
yd dev [flags] [-- 应用参数...]
```

| 标志 | 说明 |
|---|---|
| `--port` | 注入 `YD_SERVER_PORT`；0 沿用应用默认值 |
| `--no-watch` | 只构建并启动一次，不监听文件 |
| `--no-tailwind` | 跳过 Tailwind 构建 |
| `--no-reload` | 关闭开发期热重载端点 |
| `--debounce` | 文件事件合并窗口，默认 200ms |
| `--tailwind-bin` | 显式指定 tailwindcss |

```bash
yd dev
yd dev --port 7070
yd dev --no-watch --no-tailwind
yd dev -- --config configs/config.local.yaml
```

### 5.3 `yd gen`

```bash
yd gen <module|crud> <名称> [flags]
```

| 标志 | 说明 |
|---|---|
| `--fields` | 字段描述串：`名称:类型[:修饰符,...]` |
| `--table` | 表名（crud 的数据来源，或其它模式的表名覆盖） |
| `--struct` | 既有结构体：`internal/model.Post` |
| `--dsn` | crud 读取表结构所用的数据库连接串 |
| `--driver` | `sqlite` / `mysql` / `postgres` / `dm` |
| `--module` | `@Tags` 业务模块名 |
| `--package` | 目标项目的 Go 模块路径 |
| `--dir` | 输出目录 |
| `--dry-run` | 只报告将写入的文件 |
| `--force` | 覆盖没有生成标记的手写文件 |

字段类型：`string`、`text`、`int`、`int8`…`int64`、`uint`…`uint64`、
`float`、`float32`、`float64`、`bool`、`time`、`date`、`datetime`、`file`。
修饰符：`primary`、`required`、`unique`、`index`、`nullable`；
键值形式 `max=`、`min=`、`size=`、`label=`、`widget=`、`comment=`、`column=`、`json=`，
其余键值原样并入校验规则。字段之间用逗号分隔；不以冒号开头的片段是上一字段的续写修饰符。

```bash
# 从字段描述生成
yd gen module blog_post \
  --fields "title:string:required,max=120,body:text,views:int:min=0,published:bool" \
  --module cms --package example.com/blog

# 从既有结构体生成
yd gen crud Post --struct internal/model.Post --module cms

# 从数据库表生成
yd gen crud Article --table articles --dsn "file:data/blog.db" --driver sqlite --module cms

# 只看计划
yd gen module audit_log --fields "action:string,actor:string" --dry-run
```

生成物默认带 `// Code generated by yd gen; DO NOT EDIT.` 标记，重复执行幂等；
手写文件默认受保护，只有 `--force` 才会覆盖。
生成的路由挂在受保护分组，模块表在启动时由生成的 `init` 执行 `AutoMigrate`。

### 5.4 `yd build`

```bash
yd build [flags]
```

| 标志 | 说明 |
|---|---|
| `--os` / `--arch` | 单目标平台 |
| `--targets` | 逗号分隔的 `GOOS/GOARCH` 列表 |
| `--all` | `linux/amd64,linux/arm64,darwin/arm64,windows/amd64` |
| `--output` | 单目标产物路径 |
| `--name` | 产物基础名 |
| `--tailwind` / `--no-tailwind` | 是否执行 Tailwind 阶段（默认开启） |
| `--minify-assets` | 压缩 JS/CSS（默认开启） |
| `--minify-bin` | 显式指定 minify |
| `--offline` | 强制 `-mod=vendor`，禁止网络 |
| `--tags` | 构建标签，可重复 |
| `--ldflags` | 额外链接参数，可重复 |

```bash
yd build
yd build --targets linux/amd64,linux/arm64
yd build --all --output dist/myapp
yd build --offline --tags e2e
```

构建固定注入 `CGO_ENABLED=0`；前端资产经 Tailwind `--minify` 与 `minify` 处理后被
`go:embed` 打进二进制。产物是可直接放进 `scratch` 镜像的静态二进制。

### 5.4.1 生产共享脚本分块

生产页面不再逐脚本请求，也不再固定合成一个无上限的大文件：`web.InitBundles` 在启动时按外壳顺序把共享脚本
切成若干块，默认每块不超过 **128 KiB**，配置键 `bundle.max_bytes`（`YD_BUNDLE_MAX_BYTES`）可调整。切分只发生在
脚本边界；单个源文件本身超过上限时单独成块并在启动日志告警。每个分块的地址都带内容哈希
（`/static/js/yd-bundle.js?part=N&v=<hash>`），静态缓存中间件因此可以下发一年 `immutable`。
开发期或组装失败时回退为逐脚本标签，加载顺序与分块模式完全一致。

### 5.5 `yd docker`

```bash
yd docker [--tag 镜像:版本] [--platform linux/amd64] [flags]
```

| 标志 | 说明 |
|---|---|
| `--tag` | 镜像引用，默认 `<项目名>:<版本>` |
| `--file` | Dockerfile 路径，默认 `Dockerfile` |
| `--platform` | 目标平台 |
| `--target` | 停在指定构建阶段 |
| `--no-cache` | 禁用构建缓存 |
| `--no-assets` | 跳过内嵌资产刷新 |
| `--no-config` | 不把 `configs/config.yaml` 打进镜像（默认打包基线配置） |
| `--push` | 构建成功后推送 |

```bash
yd docker --tag registry.example.com/team/myapp:v1.0.0
yd docker --platform linux/arm64 --no-cache
```

镜像默认把 `configs/config.yaml` 复制到 `/configs` 并设置 `YD_CONFIG_DIR=/configs`，同时创建可写的
`/data`（SQLite、节点身份与附件）。运行时可用 `-v` 挂载覆盖配置，`YD_*` 环境变量仍具有最高优先级；
`--no-config`（或直接构建时的 `--build-arg YD_INCLUDE_CONFIG=false`）生成完全由环境变量驱动的镜像。
镜像不会复制 `config.local.yaml`，避免把本地密钥带进产物。

### 5.6 `yd ko`

```bash
yd ko [--repo R] [--tags a,b] [--platforms ...] [--base-image X] \
      [--package ./cmd/app] [--local] [--push] [--no-config] [--binary PATH] [--dir DIR] [packages...]
```

```bash
yd ko --repo registry.example.com/team/myapp --tags v1.0.0 \
      --platforms linux/amd64,linux/arm64 --push
yd ko --local            # 载入本地 Docker，不推送
```

ko 是**可选外部工具**，不会成为 Go 依赖；缺失时只让 `yd ko` 返回安装提示。
镜像的两个入口只有 `yd docker` 与 `yd ko`，不要手写 `docker build` 或自行拼 ko 参数。

ko 没有注入环境变量的标志，因此 `yd ko` 在 `app.env` 不是 production 时会给出告警：部署清单必须显式设置
`YD_APP_ENV=production`，否则镜像会使用源码资产、失去不可变缓存，并可能回退到文档化的开发种子口令。

`yd ko` 默认把 `configs/config.yaml` 暂存到每个本地包的 `kodata/configs/` 后再构建，构建结束（含失败）即清理；
ko 把该目录挂到镜像的 `KO_DATA_PATH`，生成项目据此解析配置目录并自动采用它。已有 `kodata/configs/config.yaml`
时保留原文件不覆盖；`--no-config` 关闭暂存。非本地包（导入路径或 `...` 模式）无法定位源码目录，会跳过暂存；
需要额外文件时直接在包旁维护 `kodata` 目录。

### 5.7 `yd service`

```bash
yd service <install|uninstall|start|stop|restart|status> [flags]
```

| 标志 | 说明 |
|---|---|
| `--name` | 服务名 |
| `--display-name` | 显示名 |
| `--description` | 描述 |
| `--binary` | 服务运行的可执行文件 |
| `--dir` | 项目目录 |
| `--user` | 运行账号 |
| `--args` | 启动参数 |

```bash
yd build
yd service install --name myapp          # 需要管理员/root
yd service start --name myapp
yd service status --name myapp
yd service stop --name myapp
yd service uninstall --name myapp
```

停止服务只取消传给业务的 context 并等待其返回，收尾统一交给 `app.Run`；
不要用 `os.Exit` / `panic` / `log.Fatal` 停服务。密钥与口令只经 `YD_*` 环境变量注入，
不要写进服务文件。

### 5.8 `yd assets`

```bash
yd assets <list|update|verify|add|minify> [flags]
```

| 标志 | 说明 |
|---|---|
| `--offline` | 禁止一切网络访问 |
| `--check` | 只报告将变化的内容 |
| `--force` | 内容变化时重新锁定 |
| `--manifest` / `--root` | 清单与资产根目录 |
| `--kind` / `--url` / `--version` / `--path` / `--license` / `--source` | `add` 的资产元数据 |
| `--minify-bin` | 显式指定 minify |

```bash
yd assets list
yd assets verify --offline        # CI：不触网，校验 sha256
yd assets update                  # 在线更新并重新锁定
yd assets add my-lib --url https://example.com/my-lib.js \
  --version 1.2.3 --kind js --path /js/vendor/my-lib.js --license MIT
yd assets minify                  # 生成 *.min.js / *.min.svg / yd.min.css
yd assets minify --check          # CI：校验压缩资产是否漂移
```

### 5.9 `yd icons`

```bash
yd icons <sync|check> [--templates DIR] [--output PATH]
```

```bash
yd icons sync       # 按模板引用生成 web/static/img/icons.svg
yd icons check      # CI：校验 sprite 是否漂移
```

图标只能写成：

```gotemplate
{{template "partials/icon" "lucide-user-round"}}
```

禁止内联 SVG、Iconify 运行时、CDN 与裸 `<use href="#i-…">`。
`sprite` 随 `yd assets minify` 一起压缩，生产模板经 `asset` 助手解析 `.min.svg`。

### 5.10 `yd authz`

```bash
yd authz check [--dir DIR]
yd authz list
```

`check` 报告三类问题：未注册的能力/角色、裸字符串、已声明但无人引用。
前两类以非零码退出，可直接进 CI。`list` 打印注册表（能力、Go 常量、模板键、JS 键与角色）。

```bash
yd authz check
yd authz list
```

### 5.11 `yd versions`

```bash
yd versions [check|sync|upgrade] [--dir DIR] [--dry-run] [--go] [--assets]
```

```bash
yd versions check                  # 离线校验生成文件与真源
yd versions sync                   # 只重新生成 version_generated.go
yd versions upgrade --dry-run      # 只打印计划
yd versions upgrade                # Go 模块 + 前端资产
yd versions upgrade --go=false     # 只升级前端资产
```

### 5.12 `yd tools`

```bash
yd tools [check]
yd tools install [名称...]
```

```bash
yd tools check
yd tools install
yd tools install tailwindcss
```

### 5.13 `yd align`

```bash
yd align [--apply] [--test] [--exclude-dir DIR]... [--bin PATH] [--max-passes N] [targets...]
```

```bash
yd align                          # 只报告
yd align --apply                  # 迭代到不动点（默认最多 5 遍）
yd align --apply --max-passes 8
yd align --test ./internal/...
```

`betteralign` 缺失时只打印安装提示并返回非零，不影响构建。

### 5.14 `yd completion`

```bash
yd completion bash|zsh|csh|powershell   # 打印脚本
yd completion detect                     # 探测 shell 与安装位置
yd completion install [--shell S] [--dir D] [--print] [--append-rc] [--yes]
```

```bash
source <(yd completion bash)
yd completion install --shell powershell
yd completion install --append-rc --yes   # 显式写入 rc 文件（先备份、幂等）
```

默认**不改动** `~/.bashrc`、`~/.zshrc`、`~/.cshrc` 或 PowerShell profile；
只有显式 `--append-rc` 才写入。

---

## 6. 开发流程

### 6.1 热重载

`yd dev` 同时监听 Go 源码、模板与前端资产：

- Go 源码变化 → 重新编译并重启子进程；
- 模板变化 → 重启子进程（渲染器缓存重新编译模板树一次），再经开发期热重载端点刷新页面；
- CSS/JS 变化 → 重新生成 Tailwind 样式与压缩资产，并局部刷新 `#yd-shell`。

`yd dev` 启动的进程与生产二进制共用同一条优雅退出路径；Ctrl+C 会先终止子进程。

### 6.2 新增页面与端点

1. 在 `internal/controller` 新增控制器方法，返回 HTML 片段；
2. 在 `internal/router/router.go` 的受保护分组注册路由，并挂能力守卫：

```go
g.GET("/reports", authz.Require(authz.PermUserRead), c.Reports)
```

3. 在处理器 doc 注释中写完整的 OpenAPI 3.0 注解块（`@Summary` / `@Description` /
   `@Tags` / `@Param` / `@Success` / `@Failure` / `@Security` / `@Router`）；
4. 门户页面放 `web/templates/portal/**`、后台页面放 `mgr/**`；通用组件放 `web/templates/components/**`，
   共享外壳放 `web/templates/partials/**`；文案写入 `web/i18n/{zh-CN,en}.yaml`；
5. 受保护页面的按钮用 `can` / `hasRole` 或 `data-yd-perm` 控制显隐；
6. 运行 `yd gen` / `yd icons sync` / `yd authz check` / `yd build` 验证。

> 前端权限判定只是体验优化，不是安全边界；每个受保护端点都必须在服务端校验。

### 6.3 htmx 交互约定

- 新增/编辑用右侧抽屉：`data-yd-drawer-open` + `hx-get` 片段；
- 列表动作用 `details.yd-menu`；
- 所有变更动作加 `data-yd-confirm`，多按钮表单确认后用
  `event.submitter` 调 `requestSubmit`；
- 导航与语言切换用 `hx-target="#yd-shell"`，不要只换工作区；
- 轮询与变更不写历史：`hx-push-url="false"`；
- SSE 只交换匿名 `data:` 消息；WS 发送控件必须位于 `hx-ws:connect` 元素内部；
- 重连交给 hx-sse / hx-ws 的指数退避，不要写第二套重连定时器。

### 6.4 i18n 与主题

- 所有用户文案带 `data-yd-i18n="key"` 或 `tr "key"`；
- `web/i18n/zh-CN.yaml` 与 `en.yaml` 键集合必须严格一致；
- 语言切换是单一状态按钮，局部刷新 `#yd-shell`，同步 `<html lang>`；
- 主题支持 `light` / `dark` / `system`，颜色只来自 `@theme` 语义令牌。

---

## 7. 鉴权与多租户

### 7.1 概念

- **用户是全局身份**：登录名（`username_key`）全部署唯一；
- **租户是成员关系**：一个用户可隶属多个租户，关系在 `yd_auth_user_tenants`；
- **活动租户** `TenantID` 来自令牌；`HomeTenantID` 是不可变的身份租户；
- **默认租户** `DefaultTenantID` 是登录时落入的租户，可修改；
- **每用户 JWT 密钥**：创建用户时生成专属 `sec_key`，以 SM4-GCM 封装，
  `kid` 承载用户 ID；禁用、删除或轮换后既有令牌立即失效。

### 7.2 登录与切换租户

登录表单只提交用户名与口令；含敏感字段的表单由前端 SM2 加密：

```gotemplate
<form method="post" action="/login"
      data-yd-encrypt="sm2"
      hx-post="/login" hx-target="#yd-shell">
  <input name="username" autocomplete="username" required>
  <input name="password" type="password" autocomplete="current-password" required>
</form>
```

服务端按全局登录名认证并落入默认租户。切换租户：

```bash
POST /tenant/switch   # 只接受成员租户；非成员与不存在都返回 404
```

切换成功后重新签发令牌；旧令牌在自身租户内继续有效至过期。

### 7.3 能力与角色

```go
// 路由守卫
g.POST("/users", authz.Require(authz.PermUserCreate), c.CreateUser)

// 处理器内部判定（仅用于非路由场景）
if !authz.Can(ctx, authz.PermUserUpdate) {
    return authz.Allow(c, authz.PermUserUpdate) // 写 403
}
```

```gotemplate
{{ if can .Perm.UserCreate }}<button>新建</button>{{ end }}
<div data-yd-perm="user:delete" data-yd-perm-mode="hide"></div>
```

```javascript
if (yd.can(yd.PERM.USER_CREATE)) { /* ... */ }
```

权限载荷由 `authz.Data(subject)` 生成，经 `<script id="yd-authz" type="application/json">`
内联注入；**不要**把权限集合放进可被前端随意改写的存储。
载荷缺失或损坏时一律失败关闭。

```bash
yd authz check   # 拼写错误、裸字符串、未引用能力
yd authz list
```

### 7.4 种子数据

- 只在租户表为空时初始化；
- 写入默认租户、四个内置角色、`authz.BuiltinGrants()` 的角色-权限绑定与 owner 账号；
- 旧库升级时每次启动只补齐内置角色缺失的能力，**只增不减**；
- 生产环境未配置口令时拒绝初始化；口令永不写入日志。

---

## 8. 数据、缓存与消息中心

### 8.1 数据库

支持 SQLite（默认，纯 Go）、MySQL、PostgreSQL。连接串由 `ydx/gorm.PresetDSN` 补齐：

- 默认时区 UTC（`loc=UTC` / `time_zone='+00:00'` / `TimeZone=UTC`）；
- 显式参数优先，预置只补缺；
- SQLite 保留 `_txlock=immediate`，禁止 `_time_format`；
- 目标库不存在且账号权限足够时自动建库（`db.create_database: true`）。

**数据关系全部由应用维护，禁止外键与数据库级联。**
关系字段只存主键（`user_id`、`conversation_id` 等）；写入前校验目标存在且同租户；
删除前在同一事务中按显式顺序清理从属行或拒绝删除。
索引以 `tenant_id` 打头（消息与鉴权表）。

```go
db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{PrepareStmt: true})
if err != nil { return err }
if err := db.AutoMigrate(&model.Task{}); err != nil { return err }
```

### 8.2 二级缓存

```go
import (
    "github.com/shibingli/ydx/cache"
    "github.com/shibingli/ydx/cache/local"
    ydlog "github.com/shibingli/ydx/log"
)

mgr, err := cache.NewManager(
    cache.WithLocalOptions(local.Options{MaxCost: 128 << 20}),
    cache.WithLogger(ydlog.Default()),
)
if err != nil { return err }
users, err := cache.New[User](mgr, "users", cache.WithTTL(5*time.Minute))
if err != nil { return err }

u, err := users.GetOrLoad(ctx, id, func(ctx context.Context) (User, error) {
    return repo.Find(ctx, id)
})
```

写路径固定为「L2 写入并取版本 → L1 写入/删除 → 发布事件」；
读透回填使用 CAS；L1 条目 TTL 不超过 `lease`；订阅重连清空 L1。
Redis 不可达时 `FailOpen`（默认 true）按未命中继续 loader。
完整 API 与一致性模型见 [`docs/cache.md`](cache.md)。

### 8.3 消息中心

生成项目默认内建消息中心：

- 即时通讯布局：左侧会话列表、右上方历史、右下方内联撰写；
- 单聊需已接受好友且未拉黑；群成员必须是发起人的好友；
- 系统消息只有 `message:system` 可发布/撤销；置顶需要 `message:manage`；
- 撤回是软墓碑，普通用户受 `message.recall_window` 限制；
- 富文本 Markdown + 服务端 goldmark 净化；
- 附件 MIME 由服务端嗅探，下载按消息可见性鉴权；
- 实时事件经 Redis Pub/Sub 跨节点广播，失败进入 asynq 持久重试；
- `#yd-shell` 持有唯一的 `/ws/messages` 连接。

完整协议、API 与配置见 [`docs/message-center.md`](message-center.md)。

---

## 9. 前端、资产与实时推送

### 9.1 UI 组件层

前端统一在 **Htmx 4 + Alpine 3.17（CSP 构建）+ Tailwind v4** 三层上：
**服务端状态归 htmx，页面内状态归 Alpine，外观归 Tailwind**。组件层由
`web/static/css/ui.css`、`web/static/js/yd-ui.js` 与 `web/templates/components/*.html` 组成，
`yd new` 会把样式与模板逐字节复制进生成项目，脚本经框架合并静态树兜底。

- **先读手册**：[前端 UI 组件层使用手册](frontend-ui.md) 覆盖布局、表单、反馈、导航、数据展示、
  中国常用控件（手机号、身份证、银行卡、统一社会信用代码、人民币大写、省市区、日期范围、短信验证码）
  的用法与最佳实践；[组件全景与图表用法](frontend-components.md) 给出完整组件清单、
  每个组件的参数与图表选型；
- **脚本顺序固定**：`htmx → 扩展 → yd-ui.js → alpine.min.js → 国密 → 权限 → 进度条 → yd-app.js`。
  Alpine 的 CSP 构建在脚本执行时立即启动，`yd-ui.js` 必须排在它前面并在 `alpine:init` 注册组件；
- **CSP 约束**：只允许同源脚本，不含 `unsafe-eval` / `unsafe-inline`；不在 Alpine 表达式里写箭头函数、
  解构、模板字符串或全局对象，不写内联脚本与 `onclick=`；
- **渐进增强**：可见输入框就是提交字段，Alpine 只做净化、校验与格式化；脚本加载失败时表单仍可提交；
- **主题与缩放**：颜色只来自语义令牌；控件尺寸用 rem + `min-height`，页面留白用 `clamp()`，
  同时适配 1280/1920/2560 宽屏与 125% / 150% 显示缩放；
- **权限**：`data-yd-perm` / `yd.can()` 只改善体验，服务端仍必须挂 `authz.Require` / `authz.Allow`。

最小示例：

```html
{{template "components.form.field" (dict
  "ID" "yd-user-email" "Name" "email" "Type" "email" "Value" .Email
  "Label" (tr "field.email") "LabelKey" "field.email"
  "Required" true "MaxLength" 254)}}

{{template "components.cn.phone" (dict
  "ID" "yd-user-phone" "Name" "phone" "Value" .Phone
  "Label" (tr "field.phone") "LabelKey" "field.phone")}}
```

### 9.2 Tailwind v4

- `web/static/css/app.css` 必须 `@import "tailwindcss"`；
- 设计令牌写在 `@theme`，组件样式写在 `@layer components`；
- 颜色只来自语义令牌，禁止在令牌块外写颜色字面量；
- `@source` 必须覆盖 `web/static/js/yd-*.js`，否则脚本运行期添加的类会被裁掉；
- EasyMDE 等无层 vendor CSS 的覆盖必须写在 `@layer` 之外并提高特异性。

### 9.3 资产

- 所有 `/static/...` 引用写成 `{{ asset "/static/..." }}`；
- production 下自动解析同名 `.min.js` / `.min.css` / `.min.svg`；
- 新增依赖必须登记 `web/assets.lock.json` 并运行 `yd assets verify --offline`；
- 禁止运行时 CDN；CSP 只允许 `script-src 'self'`，禁止内联脚本与内联样式。

```bash
yd assets verify --offline
yd assets minify --check
yd icons check
```

### 9.4 实时推送

- **传输矩阵**：WebSocket（HTTP/1.1、HTTP/2 RFC 8441、HTTP/3 RFC 9220）、
  WebTransport（HTTP/3）与 SSE 共用消息中枢 `ydx/realtime`；单机在进程内扇出，
  多机经 Redis Pub/Sub 跨节点广播。选型、单机/多机部署、安全、背压与兼容性矩阵见
  [`docs/realtime-transports.md`](realtime-transports.md)；
- **可靠性边界**：Pub/Sub 至多一次；失败广播可经 `realtime.Options.Retry` + `PublishRemote`
  交给 asynq 补发。什么时候用直接 Redis、Pub/Sub、asynq 的分区见
  [`docs/redis-asynq.md`](redis-asynq.md)；
- **SSE**：`Broker.PublishHTML` 发送匿名 `data:` 消息；命名事件只用于旁路通知；
- **WebSocket**：上行扁平 JSON，下行只交换 `content`；发送控件必须在
  `hx-ws:connect` 元素内部；回车发送由 `yd-app.js` 点击发送按钮，不用
  `hx-trigger` 过滤器（会被 CSP 拦截）；
- **重连**：hx-sse / hx-ws 指数退避 + 抖动；SSE 自动回传 `Last-Event-ID`，
  WS 断线期间排队最多 100 条；
- **横幅**：`#yd-connection` 是唯一重连横幅，由聚合状态驱动；
- **恢复**：通道恢复时派发 `yd:realtime:recovered`，并按 `data-yd-resync` 重取片段；
- 手动重试通过扩展的连接对象立即重连，只有连接对象不存在时才整页重载。

### 9.5 管理概览指标

`/mgr` 只建立一条 WebSocket `/mgr/ws/metrics`，服务端每 5 秒推送系统资源与
中间件运行时两块片段；不要再为这两块内容写 HTTP 轮询。
`GET /mgr/metrics` 与 `/mgr/metrics/middleware` 仅作为可直接取用的服务端片段保留。
指标来自 `ydx/sysinfo` 与 `runtime/metrics`，平台不提供时显示「不可用」，不得编造数字。

---

## 10. 可观测性

### 10.1 日志

统一使用 `ydx/log`：

```go
import ydlog "github.com/shibingli/ydx/log"

log := ydlog.New(ydlog.Options{Service: "myapp", Caller: true})
ydlog.SetDefault(log)

log.Info("server started | 服务器已启动", ydlog.Str("addr", addr))
log.Warn("slow query | 慢查询", ydlog.Dur("elapsed", d), ydlog.Str("sql", sql))
```

- 注释与日志文案中英同步：`English | 中文`；
- 禁止标准库 `log`、`fmt.Print*` 作为日志、第三方日志库；
- 热路径零反射；错误用 `errs` 声明式哨兵，禁止 `fmt.Errorf`。

### 10.2 中间件指标

所有中间件经 `application.UseObserved(descriptor, mw)` 装配，状态为
`registered → active → stopped`。请求指标包含请求数、并发、中止、5xx、panic、
平均/P95/最大延迟与 UTC `LastSeen`。

```go
// desc 来自项目内的 internal/middleware；未知名称回退为 custom 作用域。
// desc comes from the project's internal/middleware; unknown names fall back to the custom scope.
desc := middleware.MetricsDescriptor("my-middleware")
application.UseObserved(desc, func(c *router.Ctx) {
    c.Next()
})
```

需要业务计数时，从 `metricsx` 的收集器取得对应 `Entry` 后调用
`Entry.Incr/Add/SetGauge`，按事件调用；不要每请求创建 `Entry` 或直方图。
完整说明见 [`docs/middleware-metrics.md`](middleware-metrics.md)。

### 10.3 健康检查

`GET /healthz` 返回 `ok`，可直接用于容器与负载均衡探针。

### 10.4 OpenAPI 文档与独立资源

OpenAPI 能力拆成两个模块：

- `ydx/openapi` —— 文档模型、注解解析与标准 `net/http` 处理器，**不依赖任何路由器**；
- `ydx/openapi/ydrouter` —— `ydx/router` 适配层，把路由树转换为 `openapi.Route` 并提供
  `router.HandlerFunc`。

所有前端资源与 Go 代码隔离在 `openapi/pages/` 下，并随模块经 `go:embed` 发布：

```text
openapi/pages/page/index.html              页面模板（ViewerHTML）
openapi/pages/assets/index.css             样式表（ViewerCSS）
openapi/pages/assets/icons.svg             图标 sprite（ViewerIcons）
openapi/pages/assets/yd-openapi-viewer.js  查看器脚本（ViewerScript）
```

生成项目自动挂载三个端点，资源由同一个处理器在 `AssetsPath` 下提供：

```text
GET /api/v1/openapi.json                          JSON 文档
GET /mgr/docs                                     查看器页面
GET /openapi/assets/yd-openapi-viewer.js          查看器脚本
GET /openapi/assets/index.css                     查看器样式表
GET /openapi/assets/icons.svg                     查看器图标 sprite
```

使用标准 `net/http` 时，先构造 `openapi.Route` 列表（路由模式需已转换为
`/users/{id}` 形式），再把同一个处理器挂载到三个路径：

```go
import "github.com/shibingli/ydx/openapi"

routes := []openapi.Route{
    {Method: "GET", Path: "/users/{id}", Params: []openapi.Parameter{
        {Name: "id", In: "path", Required: true, Schema: &openapi.Schema{Type: "string"}},
    }},
}
doc := openapi.FromRoutes(routes, openapi.Info{Title: "Demo", Version: "1.0.0"}, openapi.Options{})
h := openapi.HTTPHandler(doc, openapi.Options{})

mux := http.NewServeMux()
mux.Handle("/openapi.json", h)
mux.Handle("/docs", h)
mux.Handle("/openapi/assets/", h)
```

使用 `ydx/router` 时改用适配层，模式中的 `:id`、`*filepath` 与单段混合模式会自动转换：

```go
import (
    "github.com/shibingli/ydx/openapi"
    "github.com/shibingli/ydx/openapi/ydrouter"
)

opts := openapi.Options{
    Path:       "/api/v1/openapi.json",
    DocsPath:   "/mgr/docs",
    AssetsPath: openapi.DefaultAssetsPath,
}
doc := ydrouter.FromRouterWithAnnotations(r, openapi.Info{Title: "Demo", Version: "1.0.0"},
    opts, annots, &openapi.Report{})
h := ydrouter.Handler(doc, opts)
r.GET(opts.Path, h)
r.GET(opts.DocsPath, h)
r.GET(opts.AssetsPath+"*filepath", h)
```

自定义资源前缀：

```go
opts := openapi.Options{AssetsPath: "/assets/openapi/"}
h := openapi.HTTPHandler(doc, opts)
// 查看器页面会自动引用 /assets/openapi/ 下的脚本、样式与图标
```

如果使用自有静态管线（例如把页面与资源交给前端构建、对象存储或私有静态服务），
`ViewerPage` 返回完整 HTML 页面，另外四个导出函数返回各资源字节：

```go
page, err := openapi.ViewerPage(doc, openapi.Options{
    ViewerScript: "./yd-openapi-viewer.js",
    ViewerCSS:    "./index.css",
    ViewerIcons:  "./icons.svg",
})
if err != nil {
    return err
}
_ = os.WriteFile("public/docs/index.html", page, 0o644)
_ = os.WriteFile("public/docs/yd-openapi-viewer.js", openapi.ViewerScript(), 0o644)
_ = os.WriteFile("public/docs/index.css", openapi.ViewerCSS(), 0o644)
_ = os.WriteFile("public/docs/icons.svg", openapi.ViewerIcons(), 0o644)
```

这样页面与资源完全不依赖 yd 运行时，任何静态文件服务器（包括原生 `http.FileServer`）
都能独立托管；需要换路径时改对应的 `Options.ViewerScript` / `ViewerCSS` / `ViewerIcons`。

**同源要求**：查看器脚本必须是同源外链；CSP 为 `script-src 'self'`，内联脚本会被浏览器
拦截并让 `/mgr/docs` 空白。`Options.Blocked` 可同时关闭 JSON、页面与资源端点。

---

## 11. 测试与质量门禁

```bash
gofmt -l .
go build ./...
go vet ./...
go test ./... -race
go test -tags e2e ./e2e/ -v -timeout 30m
```

框架仓库还包含仓库守卫：

```bash
go test ./repotest/
yd authz check
yd icons check
yd assets verify --offline
yd assets minify --check
yd versions check
```

### 11.1 测试约定

- TDD、表驱动；测试文件与被测文件同名加 `_test.go`；
- 涉及并发必须 `-race` 通过；
- 生成的代码必须带生成标记，且 `yd gen` 可幂等重放；
- 跨租户/不存在必须返回 404，不泄露资源存在性；
- 期望值尽量由注册表推导（角色、权限、种子行数），不要手抄。

### 11.2 关于 `-race` 与 Windows

Go 的竞态检测需要 cgo 与 C 工具链。Windows 上没有 MinGW gcc 时，可用 WSL/Linux
或安装 MinGW-w64 后执行：

```bash
CGO_ENABLED=1 go test ./... -race
```

`yd build` 默认固定 `CGO_ENABLED=0`；只有显式传 `Options.Env` 或直接 `go build`
才会开启 cgo。

---

## 12. 部署

### 12.1 构建与运行

```bash
yd build --all
./bin/myapp
```

生产环境至少注入：

```bash
export YD_APP_ENV=production
export YD_AUTH_MASTER_KEY='<32-byte-secret>'
export YD_SEED_ADMIN_PASSWORD='<strong-password>'
export YD_DB_DSN='...'
```

### 12.2 Docker

```bash
yd docker --tag registry.example.com/team/myapp:v1.0.0 --push
```

多阶段 Dockerfile 以 `golang:<GoMajorMinor>-alpine` 构建，产物进入 `scratch`；
镜像内无运行时依赖。

### 12.3 ko

```bash
yd ko --repo registry.example.com/team/myapp --tags v1.0.0 --push
```

ko 路径不需要 Dockerfile，也不要求本机有 Docker 守护进程。
两条路径产出的都是 `CGO_ENABLED=0` 静态二进制。

### 12.4 系统服务

```bash
yd build
yd service install --name myapp
yd service start --name myapp
```

服务名回退顺序：`--name` → `service.name` → `.yd.json` 的 `name` → 工作目录名。
服务文件只描述服务本身；密钥与口令经 `YD_*` 环境变量注入。
停止服务只取消 context 并等待业务返回，超时记为错误但不阻断后续收尾。

### 12.5 优雅退出

固定顺序：关闭 lifecycle 信号与长连接钩子 → 关闭 HTTP/3 → `http.Server.Shutdown`
排空 → 强制关闭残留 hijack 连接 → gRPC 优雅停止 → 逆序执行 `OnShutdown` 钩子 →
`errors.Join` 聚合错误。长连接必须注册 Tracker（WebSocket 发 1001 关闭帧，
SSE 停止心跳并 flush），不要只靠对端断开。

---

## 13. 依赖与版本管理

### 13.1 ydx 的发布形态

可复用组件在独立仓库 `github.com/shibingli/ydx`，每个子目录是一个独立 Go module。
Go 的「发布」就是 Git 标签：

```text
afs/v0.1.0
cache/v0.1.0
middleware/ratelimit/v0.1.0
...
```

不需要额外的 registry。**每个模块按自己的子目录标签独立发布，版本可以不同**：
脚手架 `version_generated.go` 的 `YdxVersions` 按模块记录真实版本。框架本身未依赖、
但生成项目会导入的模块（`cache`、`redis`、`asynq`、`openapi` 等）不写版本，
由生成项目首次构建时的 `go mod tidy` 解析。

### 13.2 升级流程

```bash
# 1. 在 ydx 仓库完成修改，给有改动的模块打各自的新标签并推送
git tag afs/v0.2.0 middleware/v0.2.0 ... && git push --tags

# 2. 在 yd 仓库按模块升级依赖并重新生成脚手架常量
cd /path/to/yd
go get github.com/shibingli/ydx/afs@v0.2.0 github.com/shibingli/ydx/middleware@v0.2.0 ...
go mod tidy
yd versions sync

# 3. 全量门禁
go build ./... && go vet ./... && go test ./... -race
```

生成项目用 `yd versions upgrade` 跟进；它执行 `go get -u ./...` 与 `go mod tidy`，
再重新锁定前端资产。

### 13.3 本地联调

开发期可在两个仓库之外临时使用 `go.work`，**不得提交**。
`yd new --local-replace /path/to/yd` 会为 yd 与全部 ydx 模块写入 replace，
用于离线构建当前检出。`yd versions check` 会忽略 local-replace 写出的
`v0.0.0-00010101000000-000000000000` 占位版本。

> local-replace 项目首次构建请用 `yd build`（内部在缺 `go.sum` 时使用 `-mod=mod`）
> 或先执行 `go mod tidy`；直接 `go build ./...` 可能提示 “updates to go.mod needed”。

### 13.4 版本真源

| 内容 | 真源 |
|---|---|
| Go 版本与 Go 模块依赖 | 仓库根 `go.mod` |
| 前端资产版本 | `web/assets.lock.json` |
| 脚手架 `GoVersion` / `HtmxVersion` / `YdxVersions` | `tooling/scaffold/version_generated.go`（生成文件，禁止手改） |

```bash
yd versions check   # 离线校验生成文件与真源一致
yd versions sync    # 重新生成
```

---

## 14. 完整示例：从零做一个任务管理应用

### 14.1 创建项目

```bash
yd new taskapp --module example.com/taskapp --db sqlite \
  --yes
cd taskapp
```

### 14.2 生成任务模块

```bash
yd gen module task \
  --fields "title:string:required,max=120,notes:text,done:bool,due:time,priority:int:min=1" \
  --module tasks --package example.com/taskapp
```

生成物：

```text
internal/model/task.go
internal/repository/task_repo.go
internal/service/task_service.go
internal/controller/task_controller.go
internal/router/task_routes.go
internal/router/routes_gen.go
web/templates/task/{list,form,row,_fields}.html
```

路由（挂在受保护分组）：

```text
GET    /task
GET    /task/new
POST   /task
GET    /task/:id
GET    /task/:id/edit
PUT    /task/:id
DELETE /task/:id
```

### 14.3 启动并登录

```bash
yd dev
```

打开 `http://127.0.0.1:8080/login`，用 `admin` / `admin123` 登录（开发环境），
进入 `/task` 即可增删改查。首次登录后立即改密。

### 14.4 加一个能力守卫

`internal/router/task_routes.go` 带 `// Code generated by yd gen; DO NOT EDIT.` 标记，
手工修改会在下次生成时被覆盖。给整个生成模块加守卫的正确位置是
`internal/router/router.go` 的 `mountGenerated`（该文件由脚手架维护，不会被 `yd gen` 覆盖）：

```go
func mountGenerated(g *ydrouter.Group, store *repository.Store) {
    // 所有生成模块共用 user:update 守卫；按模块细分时可为每个 mount 单独包一层。
    // Every generated module shares the user:update guard; wrap one mount at a time for per-module rules.
    guarded := g.Group("", authz.Require(authz.PermUserUpdate))
    for _, fn := range generatedMounts {
        fn(guarded, store)
    }
}
```

页面按钮同样按权限显隐：

```gotemplate
{{ if can .Perm.UserUpdate }}
<button data-yd-drawer-open hx-get="/task/{{ .Task.ID }}/edit">编辑</button>
{{ end }}
```

### 14.5 加一个受保护的 JSON API

```go
// ListTasksJSON returns tasks as JSON for external clients. | ListTasksJSON 以 JSON 返回任务列表。
//
// @Summary 任务列表 | List tasks
// @Description 返回当前租户的任务。 | Returns the current tenant's tasks.
// @Tags tasks
// @Produce json
// @Success 200 {array} model.Task
// @Failure 401 {string} string "未登录 | unauthenticated"
// @Security BearerAuth
// @Router /api/tasks [get]
func (ctl *TaskController) ListTasksJSON(c *router.Ctx) {
    tenantID, ok := middleware.TenantID(c)
    if !ok {
        _ = c.JSON(http.StatusForbidden, map[string]string{"error": "tenant is required | 需要租户"})
        return
    }
    items, total, err := ctl.svc.List(c, tenantID, 1, 20, "")
    if err != nil {
        _ = c.JSON(http.StatusInternalServerError, map[string]string{"error": "list tasks failed | 任务列表加载失败"})
        return
    }
    _ = c.JSON(http.StatusOK, map[string]any{"items": items, "total": total})
}
```

注册到 `/api/v1` 分组后，`/mgr/docs` 会自动出现该操作。

### 14.6 验证与构建

```bash
gofmt -l .                  # 必须零输出
go build ./...
go vet ./...
go test ./... -race
yd authz check
yd icons sync
yd icons check
yd assets minify --check
yd build
./bin/taskapp
```

### 14.7 部署

```bash
yd docker --tag registry.example.com/team/taskapp:v1.0.0
# 或
yd ko --repo registry.example.com/team/taskapp --tags v1.0.0 --push
# 或安装为系统服务
yd service install --name taskapp
```

---

## 15. 故障排查

| 现象 | 原因与处理 |
|---|---|
| `yd new` 后 `gofmt -l` 有输出 | 当前版本会在生成时执行 `format.Source`；若仍出现，检查模板是否被手工修改，重新 `yd new --force` |
| `yd icons check` 报 sprite 漂移 | 运行 `yd icons sync`，再 `yd assets minify` |
| `yd assets verify --offline` 报校验和不匹配 | 资产被移动或篡改；运行 `yd assets update` 重新锁定，或恢复锁文件 |
| `yd versions check` 报 `ydx modules require different versions` | 所有 ydx 模块必须同一版本；检查 go.mod 与 `version_generated.go`，运行 `yd versions sync` |
| `yd build` 报 `missing go.sum entry` | 首次构建会加 `-mod=mod` 自动补齐；若仍失败，检查 `GOPROXY`/网络 |
| 服务启动提示 `auth.master_key is unset` | 生产环境必须注入 `YD_AUTH_MASTER_KEY`；开发环境会回退并告警 |
| 生产环境拒绝初始化种子 | 非 development 未配置 `YD_SEED_ADMIN_PASSWORD`；注入强口令后重启 |
| 登录 404 / 403 | 用户不存在与跨租户同形返回 404；检查租户成员关系与能力注册表 |
| Redis 不可达 | 消息总线降级为单节点并告警；`cache.fail_open` 默认 true；生产建议部署 Redis |
| `/mgr/docs` 空白 | CSP 拦截内联脚本；页面、样式、图标与脚本由 `ydx/openapi` 在 `opts.AssetsPath` 下提供（YD 路由经 `ydrouter.Handler` 挂载），生成路由必须注册 `opts.AssetsPath+"*filepath"`；禁止内联脚本与 CDN |
| SSE 一直显示「连接中」 | 确认服务端发送的是匿名 `data:` 消息，而不是命名事件 |
| WebSocket 发送无反应 | 发送控件必须位于 `hx-ws:connect` 元素内部；检查 `hx-ws:send` 的 `closest()` |
| Windows 上 `go test -race` 失败 | 需要 cgo 与 C 工具链；使用 WSL/Linux 或安装 MinGW-w64 |
| `go list` 报 go.work.sum 校验失败 | 本地 `go.work` 过期；运行 `go work sync`、删除工作区文件，或设置 `GOWORK=off` |
| 可选工具缺失 | `yd tools check` 查看提示；`yd tools install` 安装到 `.yd/tools/`；缺失不会阻塞构建 |

---

## 附录 A：环境变量速查

| 变量 | 映射键 | 说明 |
|---|---|---|
| `YD_APP_ENV` | `app.env` | `development`/`dev`、`production`/`prod` |
| `YD_CONFIG_DIR` | —（配置目录） | 覆盖默认 `configs`；`yd docker` 镜像为 `/configs` |
| `YD_BUNDLE_MAX_BYTES` | `bundle.max_bytes` | 生产共享脚本分块上限，默认 131072（128 KiB） |
| `YD_SERVER_PORT` | `server.port` | HTTP 端口 |
| `YD_DB_DRIVER` / `YD_DB_DSN` | `db.driver` / `db.dsn` | 数据库 |
| `YD_DB_MAXCONN` | `db.maxconn` | 连接池上限 |
| `YD_REDIS_ADDR` | `redis.addr` | Redis 地址 |
| `YD_AUTH_MASTER_KEY` | `auth.master_key` | 每用户 sec_key 的主密钥（生产必填） |
| `YD_AUTH_TOKEN_TTL` | `auth.token_ttl` | 令牌有效期 |
| `YD_SEED_TENANT` / `YD_SEED_USERNAME` | `seed.tenant` / `seed.username` | 种子租户/账号 |
| `YD_SEED_ADMIN_PASSWORD` | `seed.admin_password` | 种子口令（生产必填） |
| `YD_MESSAGE_NODE_ID` | `message.node_id` | 兼容别名；留空使用 `node.id` 与身份文件 |
| `YD_SERVICE_*` | `service.*` | 服务名称、参数、环境变量等 |
| `YD_KO_*` | `ko.*` | ko 仓库、标签、平台等 |
| `YD_HTTP_PROXY` | `http.proxy` | yd 自身下载使用的代理 |

`KO_DATA_PATH` 由 ko 在运行时注入，不是 `YD_` 前缀变量：生成的镜像从 `$KO_DATA_PATH/configs` 读取配置，
本地运行与 `yd docker` 镜像使用 `configs` / `YD_CONFIG_DIR`。
| `YD_TAILWIND_URL` | `tailwind.url` | Tailwind 二进制镜像 |
| `YD_BETTERALIGN_BIN` | — | betteralign 路径 |
| `YD_KO_BIN` | — | ko 路径 |
| `YD_MINIFY_BIN` | — | minify 路径 |

## 附录 B：常用代码片段

### B.1 声明哨兵错误

```go
var (
    // ErrTaskNotFound reports a missing task. | ErrTaskNotFound 表示任务不存在。
    ErrTaskNotFound = errs.New("task not found | 任务不存在")
    // ErrTaskTitleRequired reports an empty title. | ErrTaskTitleRequired 表示标题为空。
    ErrTaskTitleRequired = errs.New("task title is required | 任务标题不能为空")
)

if strings.TrimSpace(in.Title) == "" {
    return errs.Detail(ErrTaskTitleRequired, "title=%q | 标题=%q", in.Title, in.Title)
}
```

### B.2 受保护的 HTML 处理器

```go
// List renders the task list fragment. | List 渲染任务列表片段。
//
// @Summary 任务列表片段 | Task list fragment
// @Tags tasks
// @Produce html
// @Success 200 {string} string "HTML 片段 | HTML fragment"
// @Security BearerAuth
// @Router /task [get]
func (ctl *TaskController) List(c *router.Ctx) {
    tenantID, ok := middleware.TenantID(c)
    if !ok {
        return
    }
    items, total, err := ctl.svc.List(c, tenantID, 1, 20, "")
    if err != nil {
        ctl.fail(c, http.StatusInternalServerError, "#task-module", "task list failed | task 列表加载失败", err)
        return
    }
    if err := c.HTML(http.StatusOK, "task/list.html", map[string]any{"Items": items, "Total": total}); err != nil {
        log.Default().Error("task list render failed | task 列表渲染失败", log.Err(err))
    }
}
```

### B.3 带能力判定的模板

```gotemplate
{{ if can .Perm.UserCreate }}
<button class="yd-btn yd-btn--primary"
        data-yd-drawer-open
        hx-get="/users/new"
        hx-target="#yd-drawer">
  {{ tr "user.create" }}
</button>
{{ end }}
```

### B.4 缓存读取

```go
user, err := c.users.GetOrLoad(ctx, id, func(ctx context.Context) (User, error) {
    return c.repo.Find(ctx, tenantID, id)
})
if err != nil {
    return err
}
```

### B.5 自定义中间件指标

```go
desc := middleware.MetricsDescriptor("my-custom") // internal/middleware
application.UseObserved(desc, func(c *router.Ctx) {
    c.Next()
})
```

---

## 许可

Apache License 2.0。归属团队 YunDev（[yun-dev.cn](https://yun-dev.cn) ·
[yun-dev.net](https://yun-dev.net)）。
