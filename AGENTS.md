# AGENTS.md — 自主 AI Agent 自动化规约

面向在 **yd** 仓库内自主执行「代码修复 / 功能迭代」的 AI Agent。本文件定义修改权限边界、测试自愈流程与安全合规检查。

## 0. 核心自研库与运行环境

- **JSON**：amd64 默认使用自研 `core/jsonx` 原生后端；非 amd64 默认回退 `github.com/goccy/go-json`；`jsonx_std` 构建标签可切换标准库。主模块不再依赖 sonic/jsoniter。
- **Base64**：amd64 默认使用自研 `core/b64x` 内核（`GOEXPERIMENT=simd` 时按 VBMI2/VBMI → 基础 AVX-512 → AVX2 运行时选择，否则纯 Go 标量）；非 amd64 默认回退 `github.com/cristalhq/base64`；`loong64` 或 `b64x_std` 切换标准库。主模块不再依赖 cloudwego/base64x。
- **环境自适应**：并行度、分块大小、缓冲与池上限一律由 `core/sysinfo` 在运行时读取 GOMAXPROCS、逻辑 CPU 数与 cgroup/主机内存后决定；库 API 不接收核心数或内存容量参数。
- **基准报告**：`benchmarks/x86_64-single-multi-core.md` 给出单核/多核、全部对比库、CPU 与峰值 RSS；模块级说明见 `benchmarks/{json,base64}/README.md`。
- SIMD 相关构建与测试使用 `GOEXPERIMENT=simd`；Go 1.27.2+ 若已默认启用可省略。

## 1. 修改权限边界

**允许直接修改**

- 源码目录：`core/**`（`afs/`、`b64x/`、`config/`、`errx/`、`jsonx/`、`logx/`、`version/`）、`http/**`（`router/`、`render/`、`htmx/`、`sse/`、`ws/`、`openapi/`、`i18n/`）、`middleware/`、`security/**`（`guomi/`、`jwt/`、`auth/`、`authz/`）、`runtime/**`（`app/`、`server/`、`grpcx/`、`lifecycle/`、`service/`）、`data/**`、`tooling/**`（`scaffold/`、`gen/`、`build/`、`docker/`、`assets/`、`toolchain/`、`align/`、`tools/`、`ko/`、`completion/`、`cli/`）、`web/**`
- 测试与文档：`*_test.go`、`testdata/**`、`docs/**`（除需求基线）

**禁止修改**

- `docs/需求设计说明书.md` —— 需求基线，只读。
- `LICENSE` 的授权条款正文（仅允许更新末尾版权行）。
- 任何已锁定校验和的 `web/static/js/vendor/**` 内容——升级必须走 `yd assets update` 重新锁定，禁止手改。

**需要人工确认**

- 新增第三方依赖（尤其引入 cgo 或超过 1MB 体积的依赖）。
- 变更公开 API 的破坏性签名（须在提交信息中标注 `BREAKING CHANGE`）。
- 修改 `config` 的默认值（会影响所有下游项目）。

## 2. 测试自愈流程（Test-and-Fix）

任何修改都必须以如下闭环收尾，**最多 3 轮**；3 轮未收敛则停止并输出诊断报告，不得继续盲改。

```
1. go build ./...            # 编译
   └─ 失败 → 读编译错误 → 定点修复 → 回到 1
2. go vet ./...              # 静态检查
   └─ 告警 → 修复 → 回到 1
3. go test ./... -race       # 单元测试（竞态检测）
   └─ 失败 → 用 systematic-debugging 定位根因（禁止改测试断言来"通过"）→ 修复 → 回到 1
   ⚠ Windows 上必须显式指定 C 编译器，否则报 `cgo: C compiler "gcc" not found`：
        CGO_ENABLED=1 CC=clang go test -race ./...
     这只说明**没指定 CC**，**不说明本机不能跑 -race**。
     本项目已有两次「代理误判 -race 不可用而跳过」的记录，其中一次让 ws 包的数据竞争长期存活
     （见 docs/verification.md §1.2）。**竞态检测不是可选项**；确实无 C 工具链时，
     必须在报告里写明「因缺少 C 工具链未跑 -race」，不得静默略过。
4. GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...   # 交叉编译护栏
5. go run ./cmd/yd assets verify --offline                 # 资产完整性
6. go run ./cmd/yd versions check                          # 脚手架版本与 go.mod / 资产锁一致
```

自愈规则：

- **不得**通过删除/注释测试、放宽断言、`t.Skip` 来让流水线变绿。
- 修复缺陷时必须**先补一条能复现缺陷的测试**，再修实现。
- 每轮自愈的失败证据（命令 + 原始输出）必须保留在提交信息或 PR 描述中。

## 3. 安全合规检查清单

提交前逐条自检：

- [ ] **密钥零硬编码**：源码、模板、测试夹具中不得出现真实密钥/口令/Token（用环境变量或 `testdata` 中的公开测试向量）。
- [ ] **国密合规**：涉及敏感数据（身份证、银行卡、口令）的加解密必须走 `guomi`（SM2/SM3/SM4），禁止 MD5/SHA1 用于口令存储。
- [ ] **口令存储**：必须使用 `guomi.HashPassword`（SM3 + 盐 + 迭代），比较必须 `subtle.ConstantTimeCompare`。
- [ ] **传输加密**：前端敏感字段提交必须声明式触发 SM2 前置加密（`data-yd-encrypt="sm2"`）。
- [ ] **依赖白名单**：新增依赖须为活跃维护的纯 Go 库，且无已知高危 CVE；不得引入运行时 CDN。
- [ ] **注入面**：SQL 只允许通过 GORM 参数化查询；模板输出默认走 `html/template` 转义，使用 `template.HTML` 必须写明可信来源。
- [ ] **客户端地址**：按 IP 限流、审计或风控必须用 `KeyRemoteIP(trusted)` / `Ctx.ClientIP(trusted)`，不得用无条件信任代理头的 `RealIP()`；`TrustedProxies` 为空时只看对端地址。
- [ ] **正则安全**：所有正则必须经 `core/regexpx`，禁止直接 `import "regexp"` 或 `"github.com/dlclark/regexp2"`；包装层对 RE2 兼容模式走标准库线性引擎，只有 .NET 构造走 regexp2 + 强制超时，不得关闭超时；批量扫描不得掉到 regexp2 路径（大文件上是二次复杂度）。
- [ ] **版本同步**：改动 `go.mod` 或 `web/assets.lock.json` 后必须运行 `yd versions sync`；提交里必须同时包含 `tooling/scaffold/version_generated.go` 的更新，且 `yd versions check` 通过。
- [ ] **请求边界**：外部输入必须限长（WebSocket `MaxMessageSize`、请求体 `MaxBytesReader`），并设置读写超时。
- [ ] **htmx 4 实时通道**：`hx-ws:send` 必须位于 `hx-ws:connect` 元素内部（扩展用 `closest()` 向上找连接，
      否则报 “No WebSocket connection found” 且消息不会发出）；下行只交换 `content`，旧 `BODY` 不会渲染；
      回车发送不得用 `hx-trigger` 过滤器（`new Function` 被 CSP 拦截），由 `yd-app.js` 点击发送按钮；
      `htmx:ws:error` 也可能只是发送失败，连接状态只响应 `htmx:ws:close` 等真正的断开事件。
      **例外**：CONNECT（含 RFC 8441/9220 Extended CONNECT）的正文是隧道或 WebSocket 字节流，
      不套 `MaxBytesReader`；处理器在 HTTP/2/3 下必须阻塞到连接结束。
- [ ] **JSON/Base64 统一走封装层**：全仓禁止 `import "encoding/json"` 与 `import "encoding/base64"`，
      一律使用 `github.com/shibingli/yd/core/jsonx` 与 `github.com/shibingli/yd/core/b64x`。
      自检：`grep -rn '"encoding/json"\|"encoding/base64"' --include=*.go .` 无命中。
- [ ] **中英文同步（注释 + 日志文案）**：所有注释与所有日志文案逐行采用
      `English | 中文`（竖线分割，英文在前、中文在后）。豁免：编译指令、生成标记、注释内代码示例/树状图/URL。
- [ ] **日志统一走 logx**：全仓禁止 `import "log"`（`server` 包的 `http.Server.ErrorLog`
      适配器除外，且其输出必须转发进 logx），禁止 `fmt.Print*` 当日志，禁止第三方日志库。
- [ ] **错误统一声明为哨兵**：包级 `var ErrX = errx.New("english | 中文")`；**禁止 `fmt.Errorf`**
      （改用 `errx.Detail` / `errx.Wrap` / `errx.WrapDetail`）；错误文案中英同步；
      判定只用 `errors.Is` / `errors.As`。
      自检：`grep -rn 'fmt\.Errorf' --include=*.go . | grep -v '^./core/errx/'` 无命中。
- [ ] **多租户隔离**：任何按 ID 读写实体的路径都必须校验 `TenantID`；跨租户访问返回 403/404
      且不泄露资源存在性。
- [ ] **用户是全局身份**：登录名全局唯一；一个用户可隶属多个租户（`yd_auth_user_tenants`），
      必须有且只有一个默认租户（`default_tenant_id`）。登录页不显示租户，服务端按全局登录名
      认证并落入默认租户；切换租户必须校验成员关系并重新签发令牌；身份租户与默认租户不可移除。
      旧库升级必须回填成员关系（`Migrate` 幂等）。
- [ ] **每用户 JWT 密钥**：令牌只允许用该用户的 `sec_key` 签发与验签，**禁止全局兜底密钥**；
      `sec_key` 必须以 SM4 加密存储并将不可变的 `HomeTenantID` 作为附加认证数据，
      口令只存 `guomi.HashPassword` 结果。
- [ ] **令牌失效**：用户禁用/删除/轮换 `sec_key` 后旧令牌必须立即失效。
- [ ] **内存对齐**：提交前运行 `betteralign -apply ./...`（或 `yd align --apply`，后者迭代到
      不动点并报告 `passes | 遍数`），重排后 `go test ./...` 必须仍然通过；工具缺失不得导致
      构建失败。安装：`go install github.com/dkorunic/betteralign/cmd/betteralign@latest`；
      该工具必须用不低于本模块 `go` 指令版本的 Go 构建，否则报
      `package requires newer Go version` / `export data version ... greater than maximum supported version`。
- [ ] **前端 i18n 与主题**：新增文案必须有 `data-yd-i18n` 且中英目录齐全；
      语言切换是单一状态按钮（显示当前语言，点击循环），必须 `hx-target="#yd-shell"` 刷新整块外壳
      （否则侧栏高亮与语言停在旧状态），并同步 `<html lang>`；主题支持 light/dark/system 三态并持久化。
- [ ] **离线图标**：所有图标只经 `{{template "partials/icon" "lucide-…"}}` 引用；
      `yd icons sync` 按模板生成按需 sprite，`yd icons check` 进 CI；禁止内联 SVG、Iconify 运行时与 CDN；
      `minify` 阶段同时产出 `*.min.svg`，生产模板经 `asset` 助手解析。详见 `docs/icon-usage.md`。
- [ ] **中间件运行时指标**：所有中间件经 `application.UseObserved(descriptor, mw)` 装配
      （不得只调 `Use`），名称/i18n 键/作用域集中登记在 `internal/middleware/observability.go`；
      状态为 `registered → active → stopped`，关闭后计数保留且不得翻回 active；请求指标只走原子操作，
      panic 只由最内层观察者计数并重抛；管理概览只经一条 WebSocket `/mgr/ws/metrics` 每 5 秒接收系统资源与
      中间件两块片段，不得再写 HTTP 轮询或重同步。详见 `docs/middleware-metrics.md`。
- [ ] **Tailwind v4 分层**：所有项目样式必须建立在 Tailwind v4 之上——`@import "tailwindcss"`、
      `@theme` 语义令牌、`@layer components`；可复用工具类用 `@apply`，颜色不得在令牌块之外写字面量。
- [ ] **交互约定**：新增/编辑走右侧抽屉（`#yd-drawer`），列表操作与顶栏租户切换共用下拉菜单
      （`details.yd-menu` 与同一套面板样式，不用原生 select 呈现租户选项）；所有变更动作（退出、删除、启停、
      轮换、重置、创建、保存、切换租户、改默认租户）必须带 `data-yd-confirm` 并经过全局确认对话框；
      多按钮表单确认后必须把 `event.submitter` 传回 `requestSubmit`；轮询与变更不得写历史。
- [ ] **登录页最小化**：未登录外壳只有登录卡片（含离线图标品牌区）与语言/主题切换，不得出现侧栏、
      顶栏导航、页脚、租户字段或种子凭据说明；favicon 指向同源 `/static/img/yd-icon.svg`，
      禁止 `data:,` 空图标、CDN 或外链；侧栏页脚只显示版本。
- [ ] **接口组新标签页**：接口文档与 OpenAPI JSON 以及页面内的 `/mgr/docs`、`/api/v1/openapi.json` 链接
      必须带 `target="_blank" rel="noopener noreferrer"`，不得换进工作区。
- [ ] **实时推送**：htmx 4 只交换匿名 SSE 消息，`Broker.PublishHTML` 必须发送无名 `data:` 消息；
      导航离开实时页面导致的流中止不得显示断线横幅。
- [ ] **重连与恢复**：重连交给 hx-sse / hx-ws 的退避机制（SSE 回传 `Last-Event-ID`，WS 重开后 flush 上行队列），
      不得另写重连定时器；`#yd-connection` 是唯一横幅（连接中 / 重连中 #N / 已失败 / 离线），
      全部恢复后隐藏并给出 3 秒恢复提示；恢复时按 `data-yd-resync` 重取片段并派发 `yd:realtime:recovered`；
      导航与 `htmx:after:swap` 必须清理定时器、提示节点与旧通道状态。
- [ ] **OpenAPI 查看器**：脚本必须是同源外链（`/static/js/yd-openapi-viewer.js`），
      禁止内联脚本与 CDN，否则 CSP 会拦截并让 `/mgr/docs` 空白。
- [ ] **管理概览资源仪表盘**：系统资源与中间件共用一条 `/mgr/ws/metrics` 推送，纯 SVG 仪表盘、主题令牌着色；
      宿主机 CPU/内存来自 `core/sysinfo`，进程 CPU/内存来自 `runtime/metrics`；
      平台不支持时显示「不可用」，不得编造数字。
- [ ] **OpenAPI 注释完整**：对外 HTTP 处理器的 doc 注释必须含完整注解块
      （`@Summary/@Description/@Tags/@Accept/@Produce/@Param/@Success/@Failure/@Security/@Router`），缺项即未完成。
- [ ] **优雅退出**：关停顺序固定为「长连接钩子 → HTTP/3 关闭 → HTTP 排空 → 强制关闭残留 hijack 连接 →
      gRPC 优雅停止 → 逆序 `OnShutdown` 钩子」，任何入口不得打乱；**长连接必须登记**
      （WebSocket 传 `ws.Options.Tracker` 并发 1001 关闭帧、SSE 传 `sse.Options.Tracker`，
      其它用 `app.Lifecycle().Add`），处理器必须监听关闭信号而不是只等对端断开；
      宽限期只有 `server.Options.ShutdownTimeout` 一处；**禁止**在关闭路径上 `os.Exit` / `log.Fatal`。
      自检：新增长连接时补一条「挂着连接仍能在宽限期内退出」的测试。
- [ ] **服务化只走 `service` 包**：安装/卸载/启停一律经 `yd service <动作>` 或入口的 `-service <动作>`，
      **禁止**手写注册表项 / systemd unit / init 脚本，**禁止** fork 或 daemonize 自建后台；
      服务停止只取消 context 并等待业务返回（`service.Run`），**禁止**在 Stop 路径 `os.Exit`；
      测试不得真的调用 SCM / `systemctl` / `launchctl`（用注入点打桩）。
- [ ] **交叉编译必须过**：新增依赖或平台相关符号后，`GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...`
      必须通过——上游库常把常量声明在 `*_windows.go` 里，直接引用只在 Windows 编得过。
- [ ] **鉴权数据落库**：租户/用户/角色/权限一律经 `security/auth/gormstore` 存入真实数据库，内存实现只作
      未配置数据库时的兜底且必须有明确告警；`auth` 域包保持零 ORM 依赖；每个按 ID 读写的方法都必须把
      `tenant_id` 写进 WHERE，跨租户返回 `auth.ErrNotFound`（**绝不** `ErrForbidden`）；
      新增此类方法时**必须同时给出以 `tenant_id` 打头的联合索引**并断言它被建出来。
- [ ] **连接串预置**：所有数据库连接串经 `gormx.PresetDSN` 补齐时区/时间/空值/字符集；显式参数优先，
      因此**不得在脚手架里写死与预置冲突的值**；默认时区 UTC。SQLite **必须**保留 `_txlock=immediate`，
      **绝不**加入 `_time_format`（它会让驱动跳过 `_txlock` 解析并使其静默失效），见治理文档 §16.1。
- [ ] **默认无 CGO**：`yd build` 固定 `CGO_ENABLED=0` 且优先于进程环境；SQLite 必须是纯 Go 实现
      （`github.com/glebarez/sqlite`），**禁止**引入 `mattn/go-sqlite3` 等 cgo 驱动；
      源码中不得出现 `import "C"`。
- [ ] **默认初始化数据**：首启种子的角色名/权限名与角色→权限绑定一律取自 `authz` 注册表，**零字面量**；
      只在租户表为空时初始化（唯一索引冲突视为「已初始化」而非错误）；租户表非空时每次启动只补齐内置角色
      缺失的默认能力（只增不减，不动自定义角色与额外授权），补齐不需要种子口令且必须幂等；
      非 development 环境未配置 `seed.admin_password` / `YD_SEED_ADMIN_PASSWORD` 时**拒绝初始化**，
      **绝不**用默认口令在生产开管理员；口令永不进日志。
- [ ] **Shell 补全**：`yd completion` 的四种脚本**从命令注册表生成**（禁止手写）；
      位置参数（动作词/枚举值）由 `Command.Positionals` 描述并一起生成，非标志位置不得用标志回答，
      csh 只补第一个位置槽位；**命令/标志/位置参数新增、重命名或删除时，必须在同一次变更中更新
      `Command` 的标志声明与 `Positionals` 及补全测试**，`yd completion` 不得落后于命令定义；
      **默认绝不改 rc 文件**，`--append-rc` 才写入且必须幂等 + 先备份；已有标记块时就地更新
      （合并重复块、修正加载行），内容完全一致才跳过；
      PATH 片段默认注释掉；PowerShell 脚本文件名必须是 `.ps1`（点源只加载 `.ps1`，用别的后缀会静默失效）；
      语法检查找不到解释器时 `t.Skipf` 写明原因，**不得静默通过**。
- [ ] **不直接 panic**：非测试代码里除 `recover()` 块中的 `panic(rec)` 重新抛出外，
      **不得出现 `panic(`**；接线期错误一律登记并由 `Router.Validate()` / `cli.Execute()` 启动时返回，
      运行期按错误返回。守卫 `TestNoDirectPanic` 强制。
- [ ] **自动建库**：目标库不存在且账号有权限时自动创建（`db.create_database`，默认开）；
      判定用**错误码**而非字符串；建库语句的字符集取自预置表；库名按 SQL 定界规则转义；
      失败时「库不存在」与「建库失败」两个原因都要保留；建库必须打**带库名**的 Warn
      （否则库名笔误会静默建出空库）。
- [ ] **敏感日志**：访问日志与慢查询日志不得输出口令、Token、密钥明文；使用 `logger.ParamsFilter` 屏蔽。
- [ ] **CORS**：`AllowCredentials=true` 时禁止回显 `*`。
- [ ] **资产完整性**：`web/assets.lock.json` 中每项必须有 sha256，且 `yd assets verify --offline` 通过。
- [ ] **镜像打包**：镜像一律经 `yd ko`（Go 源码直出，无需 Dockerfile）或 `yd docker`（多阶段 Dockerfile）产出，
      **禁止**另行手写 `docker build` 参数或自拼 ko 命令行；ko 属**可选外部工具**（`--binary` → `YD_KO_BIN` →
      `<项目>/.yd/tools/ko[.exe]` → `PATH`），缺失时只返回 `ko.ErrBinaryNotFound` 并提示
      `go install github.com/google/ko@latest`，**不得**让 `yd build` / `yd dev` / `go test` 或 CI 失败；
      **不得**新增 ko 的 Go 依赖（`go.mod` 不因本特性变化）；两条路径的产物都必须是 `CGO_ENABLED=0` 静态链接。
- [ ] **可选外部工具**：`betteralign` / `ko` / `tailwindcss` / `minify` / `docker` 一律经 `yd tools` 探测与安装
      （`yd tools check` 只报告；`yd tools install` 把可 `go install` 的项装进项目内 `.yd/tools/`，
      `GOBIN` 指向它的绝对路径），**禁止**手写 `go install` 装到全局，**禁止**改动 `PATH` 或往 `~/go/bin` 写东西；
      五个工具一律可选，缺失只影响对应能力，**不得**让 `yd build` / `yd dev` / `yd gen` / `go test` 或 CI 失败
      （`yd new --yes` 不得触网）；哨兵 `tools.ErrUnknownTool` / `tools.ErrNoGoToolchain` /
      `tools.ErrNotInstallable` / `tools.ErrInstallFailed` 一律用 `errors.Is` 判定。

## 4. 提交纪律（强制，Agent 必须遵守）

- **提交信息一律使用中文 Conventional Commits**，格式：
  ```
  <类型>(<范围>): <中文简述>

  <可选正文>

  Co-Authored-By: Eric Shi <shibingli@sina.com>
  ```
- **署名口径唯一**：本仓所有提交一律使用 `Co-Authored-By: Eric Shi <shibingli@sina.com>`，
  不得替换、不得省略、不得追加其他署名。
- **严禁任何 Claude / Anthropic / AI 相关内容**出现在提交信息、作者信息、脚注、
  分支名、PR 描述、代码注释与文档中（含 `Claude`、`claude`、`Anthropic`、
  `noreply@anthropic.com`、`Generated with Claude Code`、`🤖` 等）。
  提交前自检：`git log -1 --format=%B | grep -iE 'claude|anthropic'` 必须无输出。
- Agent 在自动化流程中创建提交时，必须先设置作者身份：
  `git -c user.name="Eric Shi" -c user.email="shibingli@sina.com" commit ...`

## 5. 交付物要求

- 每个功能变更须包含：实现 + 单元测试 + （如涉及用户可见行为）`docs/` 下的说明更新。
- 新增包须在包文档注释中说明其职责边界与不做什么。
- 生成的代码变更须能通过 `yd gen` 幂等重放（重复执行不产生 diff）。

## 23. 站内消息中心（强制）

消息中心由框架域包 `messaging` + `messaging/gormstore` 与脚手架接线组成，使用说明见
`docs/message-center.md`。以下约束不可绕过：

- **租户硬边界**：12 张 `yd_msg_*` 表全部以 `tenant_id` 为第一条件；跨租户与不存在一律 `messaging.ErrNotFound`，
  不得返回任何数据，也不得用 403 暴露资源是否存在。
- **好友规则**：单聊必须已接受好友且双方未拉黑；群发成员必须都是发起人的好友；拉黑同时删除好友关系并关闭申请。
- **系统消息**：只有 `message:system` 可发布/撤销；撤销是软撤回，正文保留在库中，审计记录保留标题与 Markdown 正文；
  HTML 的 `/messages/:id/revoke` 与 JSON API 的 `/api/v1/messages/:id/revoke` 共用同一条路径；
  置顶只有 `message:manage`，且只允许系统来源消息与可选时间段。
- **撤回窗口**：普通用户受 `message.recall_window`（配置）限制且只能撤回本人消息；管理员不受窗口限制。
  正文对无 `message:manage` 的读者不可见，墓碑仍占位。
- **已读水位**：未读 = 创建时间晚于 `last_read_at` 且非本人发送；只有确实存在未读时才推进水位并广播已读事件，
  避免两个客户端互相刷新形成事件回环。
- **权限**：新增能力只在 `security/authz` 注册；`member` 只持有 `message:read/send/update/delete`，
  其作用域由服务端限制为本人消息；`auditor` 只持有 `message:read` 与 `message-type:read`。
- **富文本**：Markdown 源 + 服务端 goldmark 渲染；原始 HTML 与危险链接必须被移除。客户端提交的 HTML 不得信任，
  模板只能输出净化后的 `BodyHTML`。
- **附件**：MIME 由服务端嗅探，白名单不含 HTML/SVG/脚本；存储键不得包含原始文件名；下载必须按消息可见性鉴权。
- **实时**：`yd new` 生成的项目默认内建 `redis`（Pub/Sub 广播）与 `asynq`（持久重试），两者不是可选开关；
  Redis 不可达时明确降级为单节点并告警，不得阻塞启动。`message.node_id` 多副本唯一。
- **前端**：消息中心是即时通讯布局——左侧会话列表、右上方历史消息、右下方内联撰写区；后台治理与目录设置
  保持列表式管理页。日常消息在内联撰写区直接发送，不弹确认；公告频道、撤回、撤销、退出、解散、置顶与好友变更
  仍走 `data-yd-confirm`。新建会话在左侧「联系人」页签内完成：点好友整行开单聊、勾选多位好友建群，
  不打开抽屉或第二层页面；编辑消息、置顶与群设置走右侧抽屉。右上角用户名为用户菜单，头像角标显示未读数，
  菜单提供「未读消息」与「我的资料」两项；消息页以外的导航不得建立第二条消息连接。
  实时刷新只重取 `/messages/:id/history`，不得重建撰写区或清掉草稿。
- **检索**：`search_text` 便携式分词 `LIKE`（`!` 转义）；替换为外部 FTS 时只改 Store 实现，不改领域规则与页面。
- **文档**：`docs/message-center.md` 是能力、配置、API、WebSocket 协议与多节点部署的唯一入口。

## 24. 门户与后台隔离（强制）

- **路由**：门户为 `/`、`/messages`、`/friends`、`/account`、`/realtime`；`/` 只按登录状态分流——
  匿名访客去 `/login`，已登录用户去 `/messages`（消息中心不可用时去 `/account`）。后台管理全部落在
  `/mgr/**`。门户默认只有普通用户能力（自己的消息、好友与账号），管理端点按注册表逐条挂管理能力守卫。
- **模板隔离**：门户只放 `web/templates/portal/**`，后台只放 `web/templates/mgr/**`；两棵树各有自己的
  `layouts/`、`partials/` 与 `icon`/`flash`/`shell-switches`/`sidebar`，禁止跨树 include。
- **布局选择**：视图数据经 `render.LayoutKey` 指定 `LayoutPortal` 或 `LayoutMgr`；`#yd-shell` 交换按路径选择
  `ShellPortal` 或 `ShellMgr`。
- **API 隔离**：成员 API 在 `/api/portal/v1/**`，管理 API 在 `/api/v1/**`；OpenAPI `/api/v1/openapi.json`，
  查看器 `/mgr/docs`，指标推送 `/mgr/ws/metrics`。
- **测试**：新增路由或页面必须同时更新门户与管理两套 e2e 断言，并把新模板加入 `requiredFiles` 清单。

## 25. 数据关系与完整性（强制）

- **只允许主键与索引**：禁止 `FOREIGN KEY`/`REFERENCES`、GORM `constraint:`/`foreignKey:`/`references:`/
  `many2many:`/`polymorphic:` 标签与任何会生成外键的关联字段；SQLite 不开启 `foreign_keys`，
  其它方言也不得用触发器或约束代替。
- **只存标识**：关系字段保存对方主键（如 `user_id`、`conversation_id`、`message_id`、`attachment_id`），
  不保存 ORM 关联对象；目标存在性、租户归属与状态在服务层校验。
- **人工清理**：删除时要么拒绝仍有引用，要么在同一事务中按显式顺序删除从属行；不得依赖数据库级联。
- **校验与写入同事务**：写入事务内对引用目标加共享锁（MySQL `FOR SHARE`），多 ID 去重排序后加锁；
  删除目标时加更新锁并在同一事务复查引用，软删除留下的悬空引用同事务清空。
- **索引只服务查询与唯一性**，不得当作引用完整性；鉴权与消息的索引仍以 `tenant_id` 打头。
- 新增关联表必须同时提供：写入校验、删除/软删除路径、跨租户测试与守卫测试。
- 守卫：`TestNoForeignKeyDefinitions`（Go 标签、SQL 字符串、模板与 `.sql`）、GORM Schema 关系断言。

## 26. 二级缓存 cachex（强制）

- `data/cachex` 是唯一通用二级缓存；使用说明见 `docs/cache.md`。一个进程只建一个 `Manager`，按命名空间
  开 `Raw`/`Cache[T]`；同 Manager 内命名空间唯一，不得含 `:` 或控制字符。
- 写路径固定「L2 → L1 → 事件」；读透回填必须 CAS（`Remote.StoreIf` + 未命中版本）；L1 TTL 上限为 `lease`，
  订阅重连清空 L1 并派发 `OpResync`；严格读用 `GetFresh`/`ViewFresh` 绕过 L1。`FailOpen` 默认 true；L2 写失败必须返回错误。
  多机同步依赖应用层 Pub/Sub + 租约 + 重连清空 L1；`NodeID`、`cache.prefix` 与频道必须唯一，
  不得用节点本地的 keyspace notifications 替代。
- L1 条目只存放在预分配 `[]byte` 环形区域，索引/草图/TTL 结构为指针自由数组；不得为每个条目创建长寿命
  Go 对象。`View` 回调不得保留/修改字节或回调同一缓存。
- Redis 载荷必须带 `YD` 信封头与版本；`Store`/`StoreIf`/`Remove` 必须是单键 Lua（Cluster 安全）；
  删除/负缓存写 tombstone；事件只含键名与命名空间，不含值。
- GORM 插件只缓存 `gorm:query`；查询键必须含表名、纪元、表代际、目标类型、SQL 与全部绑定变量；事务内查询、
  Raw SQL、`FOR UPDATE`、Preload、`*sql.Rows` 必须跳过；显式事务严格语义用 `Plugin.Invalidate`。
- 配置经 `cache.*` 与 `cachex.OptionsFromConfig`/`RedisOptionsFromConfig`/`gormcache.OptionsFromConfig`；
  GORM 插件默认关闭，由 `cache.gorm.enabled` 开启；Manager 注册为 `app.ServiceCache`。
- 禁止新增第三方缓存库、直接 `encoding/json`/`encoding/base64`、直接 `panic`、`fmt.Errorf`、
  热路径反射，以及绕过 Manager 自建跨节点失效。
