<div align="center">

# YD

**YunDev 全栈 Web 框架及脚手架**<br>
**YunDev full-stack web framework & scaffolding**

**Go 1.27 · Htmx 4.0 · Tailwind CSS v4**<br>
极致性能 · 单二进制 · 零 Node · 离线可用 · 多租户 · 实时推送<br>
Extreme performance · single binary · zero Node · offline-ready · multi-tenant · real-time

[![GitHub](https://img.shields.io/badge/GitHub-shibingli%2Fyd-181717.svg?logo=github&logoColor=white)](https://github.com/shibingli/yd)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8.svg?logo=go&logoColor=white)](https://go.dev/dl/)
[![Htmx](https://img.shields.io/badge/Htmx-4.0-3366CC.svg)](https://htmx.org)
[![Tailwind CSS](https://img.shields.io/badge/Tailwind_CSS-v4-38BDF8.svg?logo=tailwindcss&logoColor=white)](https://tailwindcss.com)
[![Release](https://img.shields.io/github/v/release/shibingli/yd?display_name=tag&sort=semver&label=release)](https://github.com/shibingli/yd/releases)

[简体中文](#zh) · [English](#en)

</div>

---

<a id="zh"></a>

## 简体中文

### 为什么是 YD

YD 把「Go 单体后端 + Htmx 片段前端」做成一套可直接交付的全栈方案：没有 BFF/SPA 双栈，没有 Node 构建链，语言、模板、静态资产与二进制都在一个仓库、一次构建里完成。

- **一个二进制交付**：模板、CSS、JS、图标与语言目录全部 `go:embed`；Linux 上 `CGO_ENABLED=0` 静态构建，可直接放进 `scratch` 镜像。
- **前端零 Node 依赖**：Tailwind v4 由 Go 工具链驱动 standalone 编译；资产在线更新并锁定 sha256，离线 `yd assets verify --offline` 可独立运行。
- **性能是设计出来的**：自研 SIMD JSON/Base64、零拷贝日志、基数树路由与零分配渲染 / WebSocket 热路径。
- **安全与多租户是默认值**：租户硬边界、每用户 JWT `sec_key`、纯 Go SM2/SM3/SM4、登录与敏感字段前置加密。
- **实时与后台开箱即用**：站内消息中心、WebSocket/SSE/WebTransport、Redis Pub/Sub、asynq 持久重试、指标看板。
- **跨平台交付**：Linux / Windows / macOS × amd64 / arm64 预编译二进制，`yd service` 安装为 SCM / systemd / launchd，`yd docker` / `yd ko` 出镜像。

### 应用场景

| 场景 | 典型负载 | YD 对应能力 |
|---|---|---|
| **企业内部系统 / 管理后台** | CRUD、表单、列表、权限、消息审计 | htmx 抽屉与局部刷新、`yd gen crud` 低代码、authz 注册表（24 权限 / 4 角色）、租户/成员模型、指标看板、i18n/主题 |
| **多租户 SaaS / 企业应用** | 租户隔离、用户会话、可配置数据库、缓存与限流 | 租户硬边界、每用户 JWT `sec_key`、MySQL / PostgreSQL / SQLite / 达梦 DM8、二级缓存与 GORM 分级缓存、限流 |
| **实时消息 / 协作 / 监控** | 聊天、通知、实时仪表盘、长连接 | 站内消息中心、WebSocket / SSE / WebTransport、`ydx/realtime` 中枢、Redis 多机广播、asynq 失败补发、优雅退出与自动重连 |
| **私有化 / 信创 / 离线交付** | 气隙环境、国产密码与数据库、单文件部署 | `go:embed`、离线资产校验、无 CDN、SM2/SM3/SM4、达梦 DM8、Linux 纯 Go 静态二进制、scratch 镜像、Windows 服务 / systemd / launchd |
| **高并发 API / 网关 / 边缘** | 低延迟、高吞吐、JSON/Base64、HTTP/3 | 基数树路由、SIMD JSON/Base64、零拷贝日志与缓冲池、H2C/HTTP3、gzip/zstd/brotli、限流、缓存旁路 |

### 性能指标

> 同一轮同机中位数：静态路由 **8.40 ns**、5 字段 JSON 序列化 **60.9 ns**、1 MiB Base64 编码 **55.5 GB/s**、32 B WebSocket 回显 **4.07 µs**。

参考主机：AMD Ryzen AI MAX+ 395（16 核 32 线程，30 GiB，Ubuntu 26.04.1，Go 1.27.3，AVX-512 VBMI2），`GOMAXPROCS=32`、`-count=5`、`-benchtime=500ms`。全部基准与对比口径见 [ydx/benchmarks](https://github.com/shibingli/ydx/tree/main/benchmarks)；完整方法与原始数据见 [optimization-report.md](https://github.com/shibingli/ydx/blob/main/benchmarks/optimization-report.md)。

| 类别 | 基准 | 核心结论（同轮同机中位数） |
|---|---|---|
| 路由 | [router](https://github.com/shibingli/ydx/tree/main/benchmarks/router) | 静态 `GETRaw` 8.40 ns（httprouter 12.86）；参数 18.9 ns、通配 18.9 ns、未命中 25.8 ns |
| JSON | [json](https://github.com/shibingli/ydx/tree/main/benchmarks/json) | 5 字段：Marshal 60.9 ns（goccy 89.8 / sonic 87.1），Unmarshal 119.1 ns；1024 对象序列化 101.5 µs |
| Base64 | [base64](https://github.com/shibingli/ydx/tree/main/benchmarks/base64) | 32 B 编码 5.58 ns（stdlib 14.38）、解码 10.47 ns；1 MiB 编码 55.5 GB/s、解码 27.1 GB/s |
| WebSocket | [websocket](https://github.com/shibingli/ydx/tree/main/benchmarks/websocket) | 32 B 回显 4.07 µs（gorilla 4.26 / gws 6.46）；64 KiB 16.59 µs；64×1 KiB 批量写 14.06 µs（coder 73.7）；readahead 并发回显 1.41 µs |
| 缓存 | [cache](https://github.com/shibingli/ydx/tree/main/benchmarks/cache) | L1 Set 49.07 ns（freecache 75.5 / bigcache 95.8）；副本 Get 45.0 ns；零拷贝 GetAppend 38.3 ns / View 36.5 ns |
| 模板 | [template](https://github.com/shibingli/ydx/tree/main/benchmarks/template) | 100 行页面物化 18.78 µs / 0 allocs（quicktemplate 21.46）；绑定渲染 253.7 ns（html/template 3.43 µs） |
| JWT | [jwt](https://github.com/shibingli/ydx/tree/main/benchmarks/jwt) | HS256 签发 1.257 µs（类型化 535 ns）；验签 782.6 ns（golang-jwt 3.17 µs）；并发验签 145.8 ns |
| 日志 | [log](https://github.com/shibingli/ydx/tree/main/benchmarks/log) | 5 字段 Info 134.1 ns（zerolog 168.6）；8 字段 206.1 ns；并发写入 8.08 ns |
| 中间件 | [middleware](https://github.com/shibingli/ydx/tree/main/benchmarks/middleware) | CORS 36.7 ns（rs/cors 53.2）、恢复 7.85 ns、请求 ID 170.2 ns；限流 136.5 ns |
| 正则 | [regexpx](https://github.com/shibingli/ydx/tree/main/benchmarks/regexpx) | `FindAll/email` 94.98 µs（stdlib 5.679 ms）、`FindAll/date` 110.8 µs（go-re2 705.5 µs）；简单模式编译仍是一次性启动成本 |
| 缓冲池 | [buffpool](https://github.com/shibingli/ydx/tree/main/benchmarks/buffpool) | 1 KiB 9.3 ns / 4 KiB 9.9 ns（bytebufferpool 9.4 / 9.5）；并发 0.58 ns |
| 压缩 | [compress](https://github.com/shibingli/ydx/tree/main/benchmarks/compress) | 16 KiB gzip 5.268 µs 与 gzhttp 5.213 µs 持平；zstd / brotli / snappy / lz4 同表对比 |

> 口径提醒：缓存 `Get` 返回调用方拥有的副本，`sync.Map` / ristretto 返回缓存内引用；限流基准中 `x/time/rate` 默认变体为全局 limiter 且不写响应头，ydx 另给出补头与按 IP 分桶的同口径变体；`sync.Pool` 基线不含尺寸分级。以上差异已在 `optimization-report.md` 中逐项说明，不以删除对比项或放宽语义制造“全胜”。数字与硬件、编译器版本强相关，请在目标机器上复跑基准。

### 核心能力

**HTTP 与实时**
- 基数树路由：段内参数、静态后缀、流式上传、零拷贝下载。
- HTTP/1.1 + H2C + HTTP/3(QUIC) + ACME；gRPC 同端口分流；固定顺序的优雅退出。
- WebSocket（RFC 6455 / 7692 / 7936 / 8441 / 9220）、SSE、WebTransport；`ydx/realtime` 单机与多机扇出。
- OpenAPI 3.0/3.1 注解、Schema 构建与同源在线查看器。

**数据与缓存**
- GORM（MySQL / PostgreSQL / SQLite / 达梦 DM8）、连接串预置、自动建库（达梦除外）。
- Redis（单机 / 哨兵 / 集群）与 asynq 加权队列。
- 二级缓存：L1 环形缓存 + Redis L2、W-TinyLFU、CAS 回填、Pub/Sub 失效、GORM 查询缓存分级。

**安全与多租户**
- 租户硬边界、全局用户身份、成员关系与角色、首启种子数据。
- 每用户 JWT `sec_key`、令牌 `kid` 取密钥、禁用 / 删除 / 轮换即时失效。
- 纯 Go SM2/SM3/SM4；登录与敏感字段 SM2 前置加密；`authz` 注册表与静态校验。

**前端与体验**
- Htmx 4.0 与全部安全官方扩展默认加载、Tailwind v4 `@theme`、Alpine 3.17（CSP 构建）、htmx morph。
- 统一 UI 组件层：反馈、导航、数据录入、数据展示、布局与浮层，以及手机号、身份证、银行卡、统一社会信用代码、人民币大写、省市区、日期范围、短信验证码等中国控件；完整清单见「前端组件清单」。
- 门户 `/ui-kit` 组件示例页：一页看全控件；门户与后台共用同一套组件与外壳，不复制两套。
- 抽屉、确认框、列表下拉菜单、语言局部切换、三态主题、离线图标 sprite；宽屏流式布局与 125% / 150% 显示缩放适配。
- 消息中心即时通讯布局；指标看板；全局加载进度条。

**运行时与交付**
- `app` 门面、生命周期钩子、长连接登记、节点身份、运行时 GC 调度。
- 中间件运行时指标与系统资源仪表盘（WebSocket 单通道推送）。
- `yd new / dev / gen / build / docker / ko / service / assets / versions / authz / icons / align / tools / completion`。
- 六种平台/架构组合的预编译二进制；`yd service` 安装为 SCM / systemd / launchd。

### 前端组件清单

组件真源是 `web/templates/components/**`、`web/static/css/ui.css` 与 `web/static/js/yd-ui.js`；
`yd new` 会把组件模板与样式逐字节复制进每个生成项目，脚本经框架合并静态树提供，门户 `/ui-kit`
一页展示全部组件。参数、数据契约与示例见 [组件全景与图表用法](docs/frontend-components.md)。

| 分类 | 组件 |
|---|---|
| 反馈 | Alert、Toast、Notification、Message、Popconfirm、Questionnaire、Tag、Spinner、Skeleton、Progress、Result、Empty、LoadingBar |
| 导航 | Tabs、ClientTabs、Breadcrumb、Steps、Dropdown/ActionMenu、Anchor、Affix、BackTop、FAB、Command、ContextMenu、Menubar、Pagination、Tree、Menu、Tabbar、FilterBar、ActionSheet、Link、Navbar/Sidebar |
| 数据录入 | Form/Field、Input、Textarea、Select、TreeSelect、Cascader、Combobox、Transfer、Checkbox、Radio、Toggle、Segmented、Password、Tags、DynamicRows、Number、InputOTP、ColorPicker、DatePicker、TimePicker、DateTimePicker、Picker、MonthPicker、YearPicker、QuarterPicker、TimeRange、DateRange、Mention、Upload、MarkdownEditor、FormActions、Search、InputGroup、Label、Phone、IDCard、BankCard、CreditCode、Amount、Region、SMS |
| 数据展示 | Avatar、AvatarGroup、Badge、ButtonGroup、Card、Carousel、Code、Accordion、Countdown、Table、TableSelection、SortHeader、Descriptions、Ellipsis、GradientText、Highlight、Figure、ImageViewer、Item、KPI/Statistic、List、Log、Marquee、Meter、Rate、Slider、ScrollArea、Timeline、Watermark、Kbd、Divider、Prose、NumberAnimation、Calendar、FileTree、Cell、Attachment、Bubble、ChatMessage、MessageScroller、PromptInput、TrendChip、TextShimmer、Trace、Marker、ActionBar、HoverCard、VirtualList、QRCode、Kanban、EmojiPicker、Signature、Resizable、Copy |
| 布局与浮层 | PageHeader、AspectRatio、Flex/Grid/Space、Split/Workbench、Layout/Sidebar/Navbar、Modal/Dialog、Drawer/Sheet、Popover、Tooltip |
| 服务端 SVG 图表 | Line、Area、Bar、StackedBar、Pie、Donut、Sparkline、Gauge、Radar、Composed、Heatmap、ProgressRing、Legend/SliceLegend |

### 快速开始

预编译二进制（Linux / Windows / macOS，amd64 / arm64）见 [Releases](https://github.com/shibingli/yd/releases)。

```bash
# 从源码安装 CLI
go install github.com/shibingli/yd/cmd/yd@latest

# 创建项目并进入
yd new myapp && cd myapp

# 热重载开发（Go 编译重启 + Tailwind 监听 + 模板局部刷新）
yd dev

# 低代码生成 CRUD（DB Schema 或 Go Struct → 路由 / 控制器 / htmx 模板）
yd gen crud user --fields "name:string,age:int"

# 单文件静态构建 / 镜像 / 系统服务
yd build
yd docker            # Dockerfile → scratch
yd ko --repo registry.example.com/myapp --tags v1.0.0 --push
yd service install   # Windows SCM / systemd / launchd
```

配置优先级：命令行参数 > 环境变量（`YD_` 前缀，含 `.env`）> 配置文件（YAML/TOML/JSON）> 框架默认值。Htmx 4、Tailwind CSS v4 与 Alpine.js 3 默认集成，不需要选择；全部安全 Htmx 4 扩展默认加载，`--htmx-ext` 只追加需显式选择的策略层。完整命令与配置见 [yd 使用手册](docs/yd-usage.md)。

### 架构一览

```
Browser
  Htmx 4.0 · Tailwind v4 · Alpine 3.17 · SM2/SM3/SM4 JS sandbox
        │  HTML fragments (no JSON SPA)
        ▼
Router / Middleware / Server
  radix router · recover · request-id · access-log · compress · CORS
  rate-limit · i18n · JWT · cache-bypass · SSE/WS/WebTransport
  HTTP/1.1 + H2C + HTTP/3(QUIC) + ACME · graceful shutdown
        │
        ▼
Application
  auth/authz · messaging · realtime hub · metricsx · nodeid · gc · service
        │
        ▼
Data & Security
  GORM (MySQL/PostgreSQL/SQLite/DM8) · Redis · asynq · two-level cache
  SM2/SM3/SM4 · per-user JWT sec_key · tenant boundary
```

可复用组件以独立模块发布（`github.com/shibingli/ydx/*`），yd 只按版本引用；组件源码、基准与发布节奏在 ydx 仓库维护。

### 项目结构

```
cmd/yd/        CLI 入口
runtime/       app · server · grpcx · lifecycle · service · metricsx · GC 接线
security/      auth · authz · 国密集成
messaging/     站内消息中心
http/          chart · 服务端 SVG 图表（折线/面积/柱/堆叠/饼/环/仪表/雷达/热力等）
tooling/       scaffold · gen · build · docker · ko · assets · versions · tools
web/           内嵌 templates（含 components/ UI 组件库）/ static / langs
```

### 文档

- [yd 使用手册](docs/yd-usage.md)
- [前端 UI 组件层与使用手册](docs/frontend-ui.md) · [组件全景与图表用法](docs/frontend-components.md)
- [二级缓存](docs/cache.md) · [GORM 方言](docs/gorm-dialects.md) · [实时传输](docs/realtime-transports.md) · [节点身份](docs/node-identity.md) · [运行时 GC](docs/runtime-gc.md)
- [Redis/asynq 分区](docs/redis-asynq.md) · [消息中心](docs/message-center.md) · [中间件指标](docs/middleware-metrics.md) · [发布与二进制](docs/releasing.md) · [验收记录](docs/verification.md)
- [ydx 组件与基准](https://github.com/shibingli/ydx)

### 开源致谢

YD 的每一层都站在开源社区的成果之上：路由与模板、数据库与缓存、实时传输、前端交互、样式与图标、构建与发布工具，几乎没有一个环节是凭空写出来的。谨向下列项目的作者、维护者与贡献者致以诚挚敬意——他们长期、细致且大多无偿的工作，是这个项目能够以「单二进制、零 Node、离线可用」形态存在的前提。

| 项目 | 在 YD 中的角色 | 项目主页 |
|---|---|---|
| **Go** | 语言与工具链，纯 Go 静态构建的基础 | https://go.dev |
| **htmx** | 片段驱动的前端事件总线；`hx-*` 官方扩展与 morph 差量更新 | https://htmx.org |
| **Alpine.js（CSP 构建）** | 页面内局部状态、焦点管理与控件行为；CSP 构建使 `script-src 'self'` 下仍可用 | https://alpinejs.dev |
| **Tailwind CSS** | 设计令牌、工具类与 standalone 编译器；`@theme` 语义变量与暗色主题 | https://tailwindcss.com |
| **Idiomorph** | DOM morph 算法（htmx 4 内建 morph 的来源） | https://github.com/bigskysoftware/idiomorph |
| **EasyMDE** | 消息中心与后台的 Markdown 编辑器 | https://github.com/Ionaru/easy-markdown-editor |
| **CodeMirror · marked** | EasyMDE 浏览器包内含的编辑器引擎与 Markdown 解析器 | https://codemirror.net · https://marked.js.org |
| **Iconify / Lucide** | 离线图标 sprite 的数据来源与图标设计 | https://iconify.design · https://lucide.dev |
| **spf13/afero** | 可替换的文件系统抽象（OsFs / MemFs） | https://github.com/spf13/afero |
| **spf13/pflag** | POSIX 风格命令行参数 | https://github.com/spf13/pflag |
| **spf13/viper** | 多格式配置、环境变量与配置热加载 | https://github.com/spf13/viper |
| **go.yaml.in/yaml/v3 · pelletier/go-toml/v2** | YAML 与 TOML 配置解析 | https://pkg.go.dev/go.yaml.in/yaml/v3 · https://github.com/pelletier/go-toml |
| **fsnotify** | 文件变更监听（`yd dev` 热重载） | https://github.com/fsnotify/fsnotify |
| **GORM** | ORM 与迁移；MySQL / PostgreSQL / SQLite / 达梦方言接入 | https://gorm.io |
| **glebarez/sqlite · modernc.org/sqlite** | 纯 Go SQLite 驱动，保证默认构建零 cgo | https://github.com/glebarez/sqlite · https://modernc.org/sqlite |
| **go-sql-driver/mysql · pgx** | MySQL 与 PostgreSQL 驱动 | https://github.com/go-sql-driver/mysql · https://github.com/jackc/pgx |
| **godoes/gorm-dameng** | 达梦 DM8 的 GORM 方言 | https://github.com/godoes/gorm-dameng |
| **go-redis** | Redis 单机 / 哨兵 / 集群、Pub/Sub 与 Lua 原子操作 | https://github.com/redis/go-redis |
| **asynq** | Redis 驱动的持久任务队列与失败重试 | https://github.com/hibiken/asynq |
| **quic-go · webtransport-go** | HTTP/3、QUIC 与 WebTransport | https://github.com/quic-go/quic-go · https://github.com/quic-go/webtransport-go |
| **emmansun/gmsm** | 纯 Go 的 SM2 / SM3 / SM4 实现 | https://github.com/emmansun/gmsm |
| **goldmark** | 服务端 Markdown 渲染（消息中心富文本） | https://github.com/yuin/goldmark |
| **kardianos/service** | Windows SCM / systemd / launchd 的统一服务封装 | https://github.com/kardianos/service |
| **gRPC-Go · golang.org/x/crypto** | gRPC 双通道与密码学基础件 | https://grpc.io · https://pkg.go.dev/golang.org/x/crypto |
| **miniredis** | 测试期 Redis 替身 | https://github.com/alicebob/miniredis |
| **betteralign** | 结构体内存对齐检查与重排 | https://github.com/dkorunic/betteralign |
| **ko** | 无 Dockerfile 的 Go 镜像构建 | https://github.com/google/ko |
| **tdewolff/minify** | JavaScript 与 SVG 的生产压缩 | https://github.com/tdewolff/minify |
| **Docker** | 多阶段镜像构建与 `scratch` 交付 | https://www.docker.com |
| **Node.js · Playwright** | 仅用于前端脚本单元测试与浏览器验证，不参与运行时 | https://nodejs.org · https://playwright.dev |
| **ydx** | YunDev 自研组件（路由、JSON、Base64、日志、缓存、实时、渲染等），以 Apache-2.0 开源 | https://github.com/shibingli/ydx |

前端资产的版本、来源与 SHA-256 锁定在 [`web/assets.lock.json`](web/assets.lock.json)；第三方许可正文与逐项说明见 [`web/static/js/vendor/LICENSES.md`](web/static/js/vendor/LICENSES.md)。上表未逐一展开的传递依赖记录在 `go.mod` / `go.sum` 中，其许可以各自仓库公布为准。若某处归属或链接有误，欢迎提交 Issue 指正——我们更愿意把致谢写准确，而不是写得多。

### 许可

Apache License 2.0，见 [LICENSE](LICENSE)。归属团队 YunDev（[yun-dev.cn](https://yun-dev.cn) · [yun-dev.net](https://yun-dev.net)）。

[English](#en) · [回到顶部](#yd)

---

<a id="en"></a>

## English

### Why YD

YD turns the “Go monolith + Htmx fragments” pair into a directly shippable full-stack model: no BFF/SPA split, no Node build chain, and one repository and one build for the language, templates, static assets and the binary.

- **One binary to ship**: templates, CSS, JS, icons and language catalogs are embedded with `go:embed`; on Linux, `CGO_ENABLED=0` produces a static binary that fits a `scratch` image.
- **Zero Node dependency**: Tailwind v4 is compiled by the Go toolchain with the standalone compiler; assets are updated online with sha256 locks and verified offline with `yd assets verify --offline`.
- **Performance by design**: self-built SIMD JSON/Base64, zero-copy logging, radix routing and zero-allocation render/WebSocket hot paths.
- **Secure and multi-tenant by default**: hard tenant boundaries, per-user JWT `sec_key`, pure-Go SM2/SM3/SM4, and pre-transport encryption for sign-in and sensitive fields.
- **Real-time and admin out of the box**: the message center, WebSocket/SSE/WebTransport, Redis Pub/Sub, asynq retries and a metrics dashboard.
- **Cross-platform delivery**: prebuilt binaries for Linux/Windows/macOS × amd64/arm64, `yd service` for SCM/systemd/launchd, and images via `yd docker`/`yd ko`.

### Application scenarios

| Scenario | Typical workload | What YD provides |
|---|---|---|
| **Internal business systems / admin** | CRUD, forms, lists, permissions, message audit | htmx drawers and partial swaps, `yd gen crud`, the authz registry (24 permissions / 4 roles), tenant/member model, metrics dashboard, i18n/theme |
| **Multi-tenant SaaS / enterprise apps** | Tenant isolation, user sessions, configurable database, cache and rate limits | Hard tenant boundary, per-user JWT `sec_key`, MySQL / PostgreSQL / SQLite / DM8, two-level cache and tiered GORM cache, rate limiting |
| **Real-time messaging / collaboration / monitoring** | Chat, notifications, live dashboards, long-lived connections | Message center, WebSocket / SSE / WebTransport, the `ydx/realtime` hub, Redis multi-node fan-out, asynq retry, graceful shutdown and reconnect |
| **Private / domestic-stack / air-gapped delivery** | Air-gapped environments, domestic crypto and databases, single-file deployment | `go:embed`, offline asset verification, no CDN, SM2/SM3/SM4, DM8, pure-Go static Linux binary, scratch images, Windows service / systemd / launchd |
| **High-throughput API / gateway / edge** | Low latency, high throughput, JSON/Base64, HTTP/3 | Radix router, SIMD JSON/Base64, zero-copy logging and buffer pool, H2C/HTTP3, gzip/zstd/brotli, rate limiting, cache bypass |

### Performance

> Same-run medians: static route **8.40 ns**, 5-field JSON marshal **60.9 ns**, 1 MiB Base64 encode **55.5 GB/s**, 32 B WebSocket echo **4.07 µs**.

Reference host: AMD Ryzen AI MAX+ 395 (16 cores / 32 threads, 30 GiB, Ubuntu 26.04.1, Go 1.27.3, AVX-512 VBMI2), `GOMAXPROCS=32`, `-count=5`, `-benchtime=500ms`. Full suites and comparison semantics live in [ydx/benchmarks](https://github.com/shibingli/ydx/tree/main/benchmarks); methodology and raw data in [optimization-report.md](https://github.com/shibingli/ydx/blob/main/benchmarks/optimization-report.md).

| Category | Benchmark | Representative result (same-run medians) |
|---|---|---|
| Router | [router](https://github.com/shibingli/ydx/tree/main/benchmarks/router) | Static `GETRaw` 8.40 ns (httprouter 12.86); param 18.9 ns, wildcard 18.9 ns, miss 25.8 ns |
| JSON | [json](https://github.com/shibingli/ydx/tree/main/benchmarks/json) | 5 fields: marshal 60.9 ns (goccy 89.8 / sonic 87.1), unmarshal 119.1 ns; 1024 objects marshal 101.5 µs |
| Base64 | [base64](https://github.com/shibingli/ydx/tree/main/benchmarks/base64) | 32 B encode 5.58 ns (stdlib 14.38), decode 10.47 ns; 1 MiB encode 55.5 GB/s, decode 27.1 GB/s |
| WebSocket | [websocket](https://github.com/shibingli/ydx/tree/main/benchmarks/websocket) | 32 B echo 4.07 µs (gorilla 4.26 / gws 6.46); 64 KiB 16.59 µs; 64×1 KiB batched write 14.06 µs (coder 73.7); readahead concurrent echo 1.41 µs |
| Cache | [cache](https://github.com/shibingli/ydx/tree/main/benchmarks/cache) | L1 Set 49.07 ns (freecache 75.5 / bigcache 95.8); defensive-copy Get 45.0 ns; zero-copy GetAppend 38.3 ns / View 36.5 ns |
| Templates | [template](https://github.com/shibingli/ydx/tree/main/benchmarks/template) | 100-line page materialized 18.78 µs / 0 allocs (quicktemplate 21.46); bound render 253.7 ns (html/template 3.43 µs) |
| JWT | [jwt](https://github.com/shibingli/ydx/tree/main/benchmarks/jwt) | HS256 issue 1.257 µs (typed 535 ns); verify 782.6 ns (golang-jwt 3.17 µs); concurrent verify 145.8 ns |
| Logging | [log](https://github.com/shibingli/ydx/tree/main/benchmarks/log) | 5-field Info 134.1 ns (zerolog 168.6); 8-field 206.1 ns; concurrent write 8.08 ns |
| Middleware | [middleware](https://github.com/shibingli/ydx/tree/main/benchmarks/middleware) | CORS 36.7 ns (rs/cors 53.2), recovery 7.85 ns, request ID 170.2 ns; rate limit 136.5 ns |
| Regex | [regexpx](https://github.com/shibingli/ydx/tree/main/benchmarks/regexpx) | `FindAll/email` 94.98 µs (stdlib 5.679 ms), `FindAll/date` 110.8 µs (go-re2 705.5 µs); simple-pattern compilation remains a one-time startup cost |
| Buffer pool | [buffpool](https://github.com/shibingli/ydx/tree/main/benchmarks/buffpool) | 1 KiB 9.3 ns / 4 KiB 9.9 ns (bytebufferpool 9.4 / 9.5); concurrent 0.58 ns |
| Compression | [compress](https://github.com/shibingli/ydx/tree/main/benchmarks/compress) | 16 KiB gzip 5.268 µs, level with gzhttp 5.213 µs; zstd / brotli / snappy / lz4 compared in the same table |

> Fairness notes: the cache `Get` returns a caller-owned copy while `sync.Map`/ristretto return references; the default `x/time/rate` variant uses a global limiter and writes no headers, while ydx also benchmarks header-writing and per-IP variants; the `sync.Pool` baseline has no size classes. Every difference is documented in `optimization-report.md`; no comparison was removed or weakened to manufacture an “all wins” table. Numbers depend strongly on hardware and compiler version; re-run the suites on your target machine.

### Core capabilities

**HTTP & real-time**
- Radix router: in-segment params, static suffixes, streaming uploads, zero-copy downloads.
- HTTP/1.1 + H2C + HTTP/3 (QUIC) + ACME; gRPC multiplexing on the same port; fixed-order graceful shutdown.
- WebSocket (RFC 6455 / 7692 / 7936 / 8441 / 9220), SSE, WebTransport; `ydx/realtime` single-node and multi-node fan-out.
- OpenAPI 3.0/3.1 annotations, schema builder and a same-origin online viewer.

**Data & caching**
- GORM (MySQL / PostgreSQL / SQLite / DM8), DSN presets, automatic database creation (except DM).
- Redis (standalone / sentinel / cluster) and asynq weighted queues.
- Two-level cache: L1 ring + Redis L2, W-TinyLFU, CAS backfill, Pub/Sub invalidation, tiered GORM query caching.

**Security & multi-tenancy**
- Hard tenant boundary, global user identity, memberships and roles, first-start seed data.
- Per-user JWT `sec_key`, key selection via token `kid`, immediate revocation on disable/delete/rotation.
- Pure-Go SM2/SM3/SM4; SM2 pre-encryption for sign-in and sensitive fields; the `authz` registry and static checks.

**Frontend & UX**
- Htmx 4.0 with every safe official extension loaded by default, Tailwind v4 `@theme`, Alpine 3.17 (CSP build), htmx morph.
- One UI kit: feedback, navigation, data entry, data display, layout and overlays, plus China-specific controls (mobile, ID card, bank card, unified social credit code, RMB capitalisation, province/city/district, date range, SMS code); the complete inventory is below.
- Portal `/ui-kit` component gallery: every control on one page; the portal and back office share one kit and one shell rather than two copies.
- Drawers, confirmations, list menus, local language switch, tri-state theme, offline icon sprite; fluid wide-screen layout and 125% / 150% display-scaling support.
- Message center IM layout; metrics dashboard; global loading progress bar.

**Runtime & delivery**
- The `app` facade, lifecycle hooks, long-lived connection tracking, node identity, runtime GC scheduling.
- Middleware runtime metrics and a system-resource dashboard (single WebSocket channel).
- `yd new / dev / gen / build / docker / ko / service / assets / versions / authz / icons / align / tools / completion`.
- Six prebuilt platform/architecture combinations; `yd service` installs SCM / systemd / launchd services.

### Frontend component inventory

The single source of truth is `web/templates/components/**`, `web/static/css/ui.css` and
`web/static/js/yd-ui.js`. `yd new` copies the templates and stylesheet byte-for-byte into every
generated project, the scripts come from the framework's merged static tree, and the portal's
`/ui-kit` page shows every control on one screen. Parameters, data contracts and examples live in
the [component catalogue & chart guide](docs/frontend-components.md).

| Category | Components |
|---|---|
| Feedback | Alert, Toast, Notification, Message, Popconfirm, Questionnaire, Tag, Spinner, Skeleton, Progress, Result, Empty, LoadingBar |
| Navigation | Tabs, ClientTabs, Breadcrumb, Steps, Dropdown/ActionMenu, Anchor, Affix, BackTop, FAB, Command, ContextMenu, Menubar, Pagination, Tree, Menu, Tabbar, FilterBar, ActionSheet, Link, Navbar/Sidebar |
| Data entry | Form/Field, Input, Textarea, Select, TreeSelect, Cascader, Combobox, Transfer, Checkbox, Radio, Toggle, Segmented, Password, Tags, DynamicRows, Number, InputOTP, ColorPicker, DatePicker, TimePicker, DateTimePicker, Picker, MonthPicker, YearPicker, QuarterPicker, TimeRange, DateRange, Mention, Upload, MarkdownEditor, FormActions, Search, InputGroup, Label, Phone, IDCard, BankCard, CreditCode, Amount, Region, SMS |
| Data display | Avatar, AvatarGroup, Badge, ButtonGroup, Card, Carousel, Code, Accordion, Countdown, Table, TableSelection, SortHeader, Descriptions, Ellipsis, GradientText, Highlight, Figure, ImageViewer, Item, KPI/Statistic, List, Log, Marquee, Meter, Rate, Slider, ScrollArea, Timeline, Watermark, Kbd, Divider, Prose, NumberAnimation, Calendar, FileTree, Cell, Attachment, Bubble, ChatMessage, MessageScroller, PromptInput, TrendChip, TextShimmer, Trace, Marker, ActionBar, HoverCard, VirtualList, QRCode, Kanban, EmojiPicker, Signature, Resizable, Copy |
| Layout & overlay | PageHeader, AspectRatio, Flex/Grid/Space, Split/Workbench, Layout/Sidebar/Navbar, Modal/Dialog, Drawer/Sheet, Popover, Tooltip |
| Server-rendered SVG charts | Line, Area, Bar, StackedBar, Pie, Donut, Sparkline, Gauge, Radar, Composed, Heatmap, ProgressRing, Legend/SliceLegend |

### Quick start

Prebuilt binaries (Linux / Windows / macOS, amd64 / arm64) are on [Releases](https://github.com/shibingli/yd/releases).

```bash
# Install the CLI from source
go install github.com/shibingli/yd/cmd/yd@latest

# Create a project and enter it
yd new myapp && cd myapp

# Hot-reload development (Go rebuild + Tailwind watch + template partials)
yd dev

# Generate CRUD from a DB schema or Go struct (routes / controllers / htmx templates)
yd gen crud user --fields "name:string,age:int"

# Single-binary build / image / system service
yd build
yd docker            # Dockerfile → scratch
yd ko --repo registry.example.com/myapp --tags v1.0.0 --push
yd service install   # Windows SCM / systemd / launchd
```

Configuration precedence: command-line flags > environment variables (`YD_` prefix, including `.env`) > config files (YAML/TOML/JSON) > framework defaults. Htmx 4, Tailwind CSS v4 and Alpine.js 3 are always integrated and need no choice; every safe Htmx 4 extension is loaded by default, and `--htmx-ext` only adds the opt-in policy layers. See the [user manual](docs/yd-usage.md) for the full command and configuration reference.

### Architecture

```
Browser
  Htmx 4.0 · Tailwind v4 · Alpine 3.17 · SM2/SM3/SM4 JS sandbox
        │  HTML fragments (no JSON SPA)
        ▼
Router / Middleware / Server
  radix router · recover · request-id · access-log · compress · CORS
  rate-limit · i18n · JWT · cache-bypass · SSE/WS/WebTransport
  HTTP/1.1 + H2C + HTTP/3(QUIC) + ACME · graceful shutdown
        │
        ▼
Application
  auth/authz · messaging · realtime hub · metricsx · nodeid · gc · service
        │
        ▼
Data & Security
  GORM (MySQL/PostgreSQL/SQLite/DM8) · Redis · asynq · two-level cache
  SM2/SM3/SM4 · per-user JWT sec_key · tenant boundary
```

Reusable components are published as independent modules under `github.com/shibingli/ydx/*`; yd pins them by version and keeps component sources, benchmarks and releases in the ydx repository.

### Project layout

```
cmd/yd/        CLI entry point
runtime/       app · server · grpcx · lifecycle · service · metricsx · GC wiring
security/      auth · authz · guomi integration
messaging/     in-app message center
http/          chart · server-rendered SVG charts (line/area/bar/stacked/pie/donut/gauge/radar/heatmap)
tooling/       scaffold · gen · build · docker · ko · assets · versions · tools
web/           embedded templates (with the components/ UI kit) / static / language catalogs
```

### Documentation

- [User manual](docs/yd-usage.md)
- [Frontend UI kit & usage manual](docs/frontend-ui.md) · [Component catalogue & charts](docs/frontend-components.md)
- [Two-level cache](docs/cache.md) · [GORM dialects](docs/gorm-dialects.md) · [Real-time transports](docs/realtime-transports.md) · [Node identity](docs/node-identity.md) · [Runtime GC](docs/runtime-gc.md)
- [Redis/asynq](docs/redis-asynq.md) · [Message center](docs/message-center.md) · [Middleware metrics](docs/middleware-metrics.md) · [Release & binaries](docs/releasing.md) · [Verification](docs/verification.md)
- [ydx components & benchmarks](https://github.com/shibingli/ydx)

### Acknowledgements

Every layer of YD stands on work done in the open-source community: routing and templates, databases and caches, real-time transports, front-end interaction, styles and icons, build and release tooling. Very little of it was invented here. Our sincere thanks to the authors, maintainers and contributors of the projects below — their patient, mostly unpaid work is what makes a single-binary, zero-Node, offline-ready framework possible.

| Project | Role in YD | Home |
|---|---|---|
| **Go** | Language and toolchain; the basis of pure-Go static builds | https://go.dev |
| **htmx** | Fragment-driven front-end event bus; official `hx-*` extensions and morph swaps | https://htmx.org |
| **Alpine.js (CSP build)** | Local page state, focus management and control behaviour; the CSP build keeps it usable under `script-src 'self'` | https://alpinejs.dev |
| **Tailwind CSS** | Design tokens, utilities and the standalone compiler; `@theme` variables and dark mode | https://tailwindcss.com |
| **Idiomorph** | The DOM morph algorithm behind htmx 4's built-in morph | https://github.com/bigskysoftware/idiomorph |
| **EasyMDE** | Markdown editor for the message centre and back office | https://github.com/Ionaru/easy-markdown-editor |
| **CodeMirror · marked** | Editor engine and Markdown parser bundled inside EasyMDE | https://codemirror.net · https://marked.js.org |
| **Iconify / Lucide** | Data source and artwork for the offline icon sprite | https://iconify.design · https://lucide.dev |
| **spf13/afero** | Swappable file-system abstraction (OsFs / MemFs) | https://github.com/spf13/afero |
| **spf13/pflag** | POSIX-style command-line flags | https://github.com/spf13/pflag |
| **spf13/viper** | Multi-format configuration, environment mapping and reload | https://github.com/spf13/viper |
| **go.yaml.in/yaml/v3 · pelletier/go-toml/v2** | YAML and TOML configuration parsing | https://pkg.go.dev/go.yaml.in/yaml/v3 · https://github.com/pelletier/go-toml |
| **fsnotify** | File-change watching for `yd dev` hot reload | https://github.com/fsnotify/fsnotify |
| **GORM** | ORM and migrations; MySQL / PostgreSQL / SQLite / DM8 dialects | https://gorm.io |
| **glebarez/sqlite · modernc.org/sqlite** | Pure-Go SQLite driver, keeping the default build cgo-free | https://github.com/glebarez/sqlite · https://modernc.org/sqlite |
| **go-sql-driver/mysql · pgx** | MySQL and PostgreSQL drivers | https://github.com/go-sql-driver/mysql · https://github.com/jackc/pgx |
| **godoes/gorm-dameng** | GORM dialect for DM8 | https://github.com/godoes/gorm-dameng |
| **go-redis** | Redis standalone / sentinel / cluster, Pub/Sub and Lua atomics | https://github.com/redis/go-redis |
| **asynq** | Redis-backed persistent task queue with retries | https://github.com/hibiken/asynq |
| **quic-go · webtransport-go** | HTTP/3, QUIC and WebTransport | https://github.com/quic-go/quic-go · https://github.com/quic-go/webtransport-go |
| **emmansun/gmsm** | Pure-Go SM2 / SM3 / SM4 | https://github.com/emmansun/gmsm |
| **goldmark** | Server-side Markdown rendering for rich message content | https://github.com/yuin/goldmark |
| **kardianos/service** | One service abstraction over SCM / systemd / launchd | https://github.com/kardianos/service |
| **gRPC-Go · golang.org/x/crypto** | gRPC dual channel and cryptographic primitives | https://grpc.io · https://pkg.go.dev/golang.org/x/crypto |
| **miniredis** | In-process Redis stand-in for tests | https://github.com/alicebob/miniredis |
| **betteralign** | Struct memory-alignment checks and reordering | https://github.com/dkorunic/betteralign |
| **ko** | Dockerfile-free Go image builds | https://github.com/google/ko |
| **tdewolff/minify** | Production minification of JavaScript and SVG | https://github.com/tdewolff/minify |
| **Docker** | Multi-stage image builds and `scratch` delivery | https://www.docker.com |
| **Node.js · Playwright** | Used only for front-end unit tests and browser verification, never at runtime | https://nodejs.org · https://playwright.dev |
| **ydx** | YunDev's own components (router, JSON, Base64, logging, cache, realtime, render and more), open-sourced under Apache-2.0 | https://github.com/shibingli/ydx |

Front-end versions, sources and SHA-256 locks live in [`web/assets.lock.json`](web/assets.lock.json); third-party licence texts are in [`web/static/js/vendor/LICENSES.md`](web/static/js/vendor/LICENSES.md). Transitive dependencies not listed above are recorded in `go.mod` / `go.sum`, with licences as published by their own repositories. If an attribution or link is wrong, please open an issue — we would rather be accurate than exhaustive.

### License

Apache License 2.0 — see [LICENSE](LICENSE). Maintained by YunDev ([yun-dev.cn](https://yun-dev.cn) · [yun-dev.net](https://yun-dev.net)).

[简体中文](#zh) · [Back to top](#yd)
