# CLAUDE.md — yd 项目 AI 助手全局上下文

本文件定义 AI 助手（Claude / 其他编码代理）在 **yd** 仓库内的全局上下文与硬性约束。

## 1. 项目身份

| 项 | 值 |
|---|---|
| 项目名 | yd（YunDev 全栈 Web 框架及脚手架） |
| 归属团队 | YunDev（https://yun-dev.cn 、 https://yun-dev.net） |
| 源码仓库 | https://github.com/shibingli/yd |
| 开源协议 | Apache-2.0（见 `LICENSE`，二次开发与商业衍生完全合规） |
| 需求基线 | `docs/需求设计说明书.md`（**只读**，任何代理不得修改） |
| 实施计划 | `docs/superpowers/plans/`（本地工作笔记，**不入库**） |

## 2. 技术栈核心版本

**后端**

- Go **1.27.2**（go.mod `go 1.27.2`），充分利用泛型优化、低延迟 GC、PGO 与标准库结构体增强
- 配置：viper v1.21（多格式 TOML/YAML/YAML v3/JSON + 动态监听）
- 文件系统：afero v1.15（OsFs / MemFs 无缝切换的 Mockable FS）
- 命令行：pflag v1.0.10（POSIX 风格，替代标准库 `flag`）
- JSON：**自研默认** —— amd64 默认自研原生后端，非 x86_64 回退 go-json；构建标签 `jsonx_std` 可强制标准库；主模块不依赖 sonic/jsoniter
- Base64：**自研默认** —— amd64 默认自研 SIMD/标量内核（`GOEXPERIMENT=simd` 时按 VBMI2/VBMI → AVX-512 → AVX2 分发）；非 amd64 回退 `cristalhq/base64`；loong64 或 `b64x_std` 标签回退 `encoding/base64`；主模块不依赖 cloudwego/base64x
- 并行与分配：工作协程数、分块大小与缓冲上限一律由 `core/sysinfo` 在运行时读取 GOMAXPROCS 与 cgroup/主机内存后自动决定，**不得**要求调用方传核心数或内存容量
- 数据：GORM v1.31（默认 `PrepareStmt: true`）、go-redis v9（单机/哨兵/集群）、asynq v0.26（Redis 驱动分布式队列）
- 网络：HTTP/1.1 + H2C + HTTP/3(QUIC, quic-go) + 内置 ACME 自动签发续期
- 安全：国密 SM2/SM3/SM4（**纯 Go**，gmsm），严禁 cgo 与非 Go 原生组件
- 日志：自研零拷贝日志（`sync.Pool` 字节缓冲直写 `io.Writer`，热路径零反射）

**前端**

- Htmx **4.0.0** 作为事件总线，随包发布 **18 个官方扩展全部下载并锁定**（`ws/sse/preload/alpine/targets/head/upsert/pending/multipart/download/browser-indicator/history-cache/live/prompt/ptag/csp/compat/htmx-2-compat`）；框架 UI 加载其中 15 个安全扩展，`csp`（需 nonce 策略）与两个 htmx 2 兼容层只锁定、不默认加载，由 `yd new --htmx-ext` 显式选择；Tailwind CSS **v4** 作为样式标准（`@theme` 语义化变量，默认蓝色系）
- 不依赖 Node.js 编译管道：Tailwind 编译由 Go 脚手架工具静默驱动（standalone 二进制）
- 资产一律 `go:embed` 打包进二进制，**严禁**运行时从公网 CDN 动态引入
- Alpine.js 3.17 + htmx 内建 morph（Idiomorph 算法）实现精细化 DOM 差量更新
- 前端国密沙箱：纯 JS 的 SM2/SM3/SM4，用于表单传输前置加密与静态资源完整性校验

## 3. 分层与优先级（不可违反）

配置解析优先级由高到低：**命令行参数 > 环境变量（含 `.env`）> 配置文件 > 框架默认值**。
环境变量前缀固定 **`YD_`**，下划线自动转多级嵌套（`YD_DB_MAXCONN` → `db.maxconn`，`YD_SERVER_PORT` → `server.port`）。

运行环境 `app.env` / `YD_APP_ENV` 接受完整名与短别名：`development`/`dev`、`production`/`prod`，
忽略大小写与首尾空白，统一经 `core/envx.Normalize` 归一化。未知取值在框架热重载与资产覆盖上仍按开发处理，
种子写入与主密钥校验仍只认显式 development。

> 注：`docs/需求设计说明书.md` 原文写的是 `YUNGO_` 前缀，团队已决定统一改为 `YD_`；
> 需求基线文档保持只读不改，本变更以本文件、`AGENTS.md` 与实施计划为准。

## 4. 代码风格指引

1. **包结构**：一个文件一个职责；文件超过约 600 行或承担两类职责即拆分。
2. **错误处理（强制）**：
   - **所有错误必须在包级用 `errors` 声明为哨兵**：`var ErrBadPattern = errx.New("invalid pattern | 非法的路由模式")`
     （`errx` 即 `github.com/shibingli/yd/core/errx`，是对 `errors` 的声明式薄封装）；
   - **默认禁止 `fmt.Errorf`**。需要附加格式化细节时用 `errx.Detail(ErrX, "%q ...", v)`；
     需要保留底层错误时用 `errx.Wrap(err, ErrX)` 或 `errx.WrapDetail(err, ErrX, fmt, args...)`；
   - 错误文案必须**中英同步**：`English | 中文`（与注释、日志文案同一规则）；
   - 双语格式串若两侧都要动词，**必须使用显式参数索引**（`%[1]d`），否则 `go vet`
     会因参数个数不匹配报警；语义不同则拆哨兵，不要硬凑一行；
   - 调用方一律用 `errors.Is` / `errors.As` 判定，禁止字符串匹配判断错误类型；
   - **禁止直接发起 `panic`**（强制）。唯一例外是 `recover()` 块里的 **`panic(rec)` 重新抛出**：
     它转发的是**已经发生**的 panic，交给外层 `Recover` 收尾；去掉它等于**吞掉 panic**，
     请求会在 panic 之后继续往下跑，比 panic 本身更危险。
     其余一律改为**受控失败**：接线期错误（nil 处理器、非法路由模式、重复注册命令等）
     **登记**到所属对象，由 `Router.Validate()` / `cli.Execute()` 在启动时一次性返回错误；
     运行期取不到资源（如 `app.MustGet`）改为返回 `(值, error)`。
     守卫 `TestNoDirectPanic` 强制这一点（变异验证过）。
   - 唯一例外：`errx` 包自身内部实现需要 `fmt.Sprintf` 拼接文案。
   - 自检：`grep -rn 'fmt\.Errorf' --include=*.go . | grep -v '^./core/errx/'` 必须无命中。
3. **文档注释（强制）**：所有导出符号必须有以符号名开头的 Go doc 注释。
3.1 **中英文同步（注释与日志文案）**：所有注释（包文档、导出符号 doc、行内注释）
   **以及所有日志文案**，必须**中英文同步**，以半角竖线 `|` 分割，
   **英文在前、中文在后**，两侧各留一个空格：

   ```go
   // Marshal encodes v and returns the JSON bytes. | Marshal 把 v 编码为 JSON 字节并返回。
   func Marshal(v any) ([]byte, error)
   ```

   - **两种形态，按位置选**：
     1. **行末注释**（跟在代码后面）与**所有运行时输出**——异常输出、提示、结果响应、
        日志文案、CLI 打印——**必须写在同一行**，用 ` | ` 分隔：
        `l.Info("server started | 服务器已启动", logx.Str("addr", addr))`；
     2. **方法与多行注释**（函数/类型的 doc 注释、独立成块的注释）**允许换行分隔**：
        英文段落在前、中文段落在后，不强制逐行成对。这种形态便于写长段落，
        但**整块必须两种语言都有**——只有中文的整块注释同样违规。
   - 单行注释、行末注释没有换行的余地，因此只能走第 1 种形态。
   - 分隔符一律是 ` | `（两侧各一个半角空格）；`。| English` 这种缺空格的写法不合规。
   - 豁免：`//go:build` 等编译指令、`// Code generated ... DO NOT EDIT.` 生成标记、
     注释内的代码示例/树状图/接口签名块、URL 与纯符号行。
3.1.1 **日志一律使用自研 `logx`（强制）**：
   - 全仓禁止 `import "log"`（标准库）、禁止 `fmt.Print*` 作为日志输出、
     禁止引入 zap/zerolog/logrus 等第三方日志库；
   - 统一使用 `github.com/shibingli/yd/core/logx`（`logx.New` / `Default()` 及其 `Debug/Info/Warn/Error/Fatal`）；
   - **唯一例外**：`server` 包为满足 `http.Server.ErrorLog`（要求 `*log.Logger`）而写的
     适配器，其输出必须转发进 `logx`；
   - 自检：`grep -rn '"log"' --include=*.go . | grep -v '^./runtime/server/'` 必须无命中。
3.2 **OpenAPI 注释必须完整**：所有对外 HTTP 处理器必须在其 doc 注释中给出**完整**的
   OpenAPI 3.0 注解块（缺项视为未完成），字段齐全：

   ```go
   // GetUser returns one user as an HTML fragment. | GetUser 以 HTML 片段返回单个用户。
   //
   // @Summary 获取用户详情 | Get user detail
   // @Description 返回用户详情片段；htmx 请求返回局部片段，整页请求返回完整页面。 | ...
   // @Tags users
   // @Accept json
   // @Produce html,json
   // @Param id path string true "用户 ID | user id"
   // @Param body body CreateUserRequest true "创建参数 | create payload"
   // @Success 200 {object} model.User "用户详情 | user detail"
   // @Success 422 {object} htmx.ErrorFragment "校验失败 | validation failed"
   // @Failure 404 {string} string "未找到 | not found"
   // @Security BearerAuth
   // @Router /users/{id} [get]
   ```
   `openapi` 包负责从源码解析这些注解并与路由树合并，生成完整文档。
   注解同样适用于结构体/常量/变量级别：结构体用 `@Name`/`@Description`/`@Example`，字段用
   `@Description`/`@Example`/`@Format`/`@Enum`/`@Minimum`/`@Maximum`/`@Pattern`/`@Nullable`/`@Deprecated`/`@ReadOnly`，
   常量块用 `@Name`/`@Description`/`@Type`/`@Enum`（缺省时按字面量与 iota 推断），变量用
   `@Name`/`@Schema`/`@Example`，由 `openapi.ParseTypeAnnotations` 解析、`Builder.AddTypeAnnotations` 合并。
4. **热路径禁止反射**：HTTP 请求处理、序列化、日志、JWT 校验路径不得使用 `reflect`；反射仅允许出现在启动期装配、代码生成与 OpenAPI Schema 构建中。
4.1 **JSON 与 Base64 必须走框架封装层**（强制）：
   - 任何 JSON 编解码一律使用 `github.com/shibingli/yd/core/jsonx`（`Marshal`/`Unmarshal`/`NewEncoder`/`NewDecoder`/`Valid`），
     **禁止**直接 `import "encoding/json"`；
   - 任何 Base64 编解码一律使用 `github.com/shibingli/yd/core/b64x`（`StdEncoding`/`URLEncoding`/`RawStdEncoding`/`RawURLEncoding`
     及其 `Encode`/`Decode`/`AppendEncode`/`AppendDecode`），**禁止**直接 `import "encoding/base64"`；
   - 唯一例外：第三方库（如 gmsm）自身实现内部的编解码，以及 `logx` 为满足零拷贝而手写的
     JSON 字节拼装（它不得引入 `encoding/json`）。
   - 自检命令：`grep -rn '"encoding/json"\|"encoding/base64"' --include=*.go .` 必须无命中
     （`repotest` 的守卫测试会自动检查）。
5. **零拷贝优先**：优先 `AppendXxx` 风格 API 与调用方提供的缓冲区；避免不必要的 `string([]byte)` 往返转换。
6. **并发**：共享状态必须显式加锁或使用 `sync.Pool`/原子操作；每个 `go` 关键路径必须能回答"它何时退出"。
7. **测试**：TDD；表驱动；文件名与被测文件同名加 `_test.go`；涉及并发必须 `-race` 通过。
8. **依赖克制**：新增第三方依赖需在 PR 说明中给出理由与体积影响；能用标准库解决的不引依赖。
9. **格式**：`gofmt`/`go vet ./...` 必须零告警；导入分组为标准库 / 第三方 / 本项目。
10. **提交信息**：见下方「提交纪律」，中文 Conventional Commits，署名口径唯一。
11. **正则表达式（强制）**：
   - 全仓正则统一经 `github.com/shibingli/yd/core/regexpx`；**禁止**在包装层之外直接
     `import "regexp"` 或 `import "github.com/dlclark/regexp2"`；
   - 编译只用 `regexpx.MustCompile` / `regexpx.Compile`，匹配只用其 `Match*` / `Find*` / `Replace*` 方法；
   - **包装层按模式选引擎**：标准库能编译的模式走 **RE2 线性引擎**（不回溯、热路径零分配、无需超时）；
     只有需要环视、反向引用、命名分组等 .NET 构造的模式才走 `regexp2` 的 RE2 兼容模式。
     两个引擎的导入都只允许出现在包装层内部；
   - **正则匹配的超时只作用于 regexp2 路径**（`regexpx.MatchTimeout`，默认 100ms）：超时按「未匹配」
     处理，校验类路径因此失败关闭，不得关闭。RE2 路径不需要超时，因为它不可能灾难性回溯；
   - **批量扫描必须落在 RE2 路径**：regexp2 的迭代器每次 `FindNextMatch` 都会重新转换剩余输入，
     `FindAll` 在大文件上是二次复杂度，并可能在超时后**静默截断**。因此源码 / 模板 / CSS 扫描的
     模式必须保持 RE2 兼容，不得为了 .NET 构造让扫描器掉到兼容路径；
   - `...Index` 系列返回**字节**下标（包装层已完成符文→字节换算）；禁止把 regexp2 的符文下标直接当字节用；
   - 测试代码同样适用本规则（守卫测试也需要稳定的正则语义）；
   - 自检（只匹配独立成行的导入语句，避免命中守卫里的字符串字面量）：
     `grep -rnE '^\s*"(regexp|github\.com/dlclark/regexp2)"\s*$' --include=*.go . | grep -v '^./core/regexpx/'`
     必须无命中；守卫 `TestRegexStaysBehindWrapper` 自动检查。

## 5. AI 助手行为约束

- 修改任何代码后必须运行 `go build ./... && go vet ./... && go test ./... -race`。
- 不得修改 `docs/需求设计说明书.md`；需求变更须新增文档说明差异。
- 不得在源码中硬编码任何密钥、口令、Token；开发密钥只能走环境变量或本地忽略文件。
- 新增前端依赖时必须同步登记 `web/assets.lock.json` 并运行 `yd assets verify --offline`。
- 不得引入运行时 CDN 引用（模板中禁止出现 `src="http`/`href="http` 的 JS/CSS 引用）。
- 生成的代码必须带 `// Code generated by yd gen; DO NOT EDIT.` 标记。

## 6. 提交纪律（强制）

**一律禁止在提交信息、作者、署名、脚注、PR 描述中出现任何 Claude / Anthropic / AI 助手相关内容**
（包括但不限于 `Claude`、`claude`、`Anthropic`、`noreply@anthropic.com`、
`Generated with Claude Code`、`🤖` 等标记）。

提交信息格式：**中文 Conventional Commits + 固定署名尾行**。

```
<类型>(<范围>): <中文简述>

<可选正文：为什么这么改、影响面、验证方式>

Co-Authored-By: Eric Shi <shibingli@sina.com>
```

- **类型**：`feat` `fix` `refactor` `perf` `test` `docs` `chore` `build` `ci` `style`
- **范围**：受影响的包名，如 `router`、`logx`、`config`、`ws`、`cli`、`web`
- **简述**：中文，动词开头，不超过 50 字，句末不加句号
- **署名**：**凡本仓提交，一律使用 `Co-Authored-By: Eric Shi <shibingli@sina.com>`**，
  不得使用其他署名口径，也不得省略

示例：

```
feat(router): 支持单段内混合参数与静态后缀捕获

新增 /:id.jpg、/:id-xx.png 形式的段内混合匹配，采用带记忆化的
回溯保证多项式复杂度，并保留原始编码不做百分号解码。

Co-Authored-By: Eric Shi <shibingli@sina.com>
```

## 7. 多租户与鉴权（强制）

- **租户是硬边界**：任何按 ID 读写实体的方法都必须同时校验 `TenantID`；
  跨租户访问一律返回 404/403，**不得返回任何数据**，也不得泄露资源是否存在。
- **用户是全局身份，租户是成员关系**：登录名全局唯一（`username_key` 全局唯一索引），
  一个用户可隶属多个租户，成员关系与各租户角色存放在 `yd_auth_user_tenants`。
  `auth.User.TenantID` 是当前活动租户，`HomeTenantID` 是不可变的身份租户（封装 sec_key），
  `DefaultTenantID` 是无租户登录时落入的租户，可由用户或管理员修改。新增/修改成员关系必须：
  ① 目标租户存在；② 默认租户在成员集合内；③ 身份租户与默认租户不可移除。
- **登录页不显示租户，也不显示种子凭据说明**：登录表单只提交用户名与口令，服务端按全局登录名
  认证并落入默认租户。子域名与 `X-Tenant` 仍可指定租户，但只对该用户已隶属的租户生效。
- **切换租户必须校验成员关系并重新签发令牌**：`POST /tenant/switch` 只接受成员租户，
  非成员与不存在的租户都返回 404；新令牌的 `tid` 是目标租户，旧令牌在自身租户内继续有效至过期。
- **列表交互统一**：新增/编辑走右侧抽屉（`#yd-drawer`），列表操作用下拉菜单（`details.yd-menu`），
  所有变更动作（退出、删除、启停、轮换、重置、创建、保存、切换租户、修改默认租户）都必须先经过
  `data-yd-confirm` 的全局确认对话框。
- **每用户独立 JWT 密钥（强制）**：创建用户时生成该用户专属的 `sec_key`；
  签发与验签**只允许**使用该用户的 `sec_key`，**严禁**任何全局 JWT 密钥作为兜底。
  令牌的 `kid` 承载用户 ID，验签方据此取密钥，**不得为取密钥而解析未验签的载荷**。
  `sec_key` 以不可变的 `HomeTenantID` 作为 SM4-GCM 附加认证数据封装，**不得**用当前活动租户封装：
  否则切换租户会让密钥无法解开。
- **密钥与口令的落库形态**：口令只存 `guomi.HashPassword`（SM3 + 盐 + 多轮）结果；
  `sec_key` 以 SM4 加密存储，主密钥来自配置/环境变量，**不得**明文入库、不得写进日志。
- **失效语义**：用户被禁用、被删除或轮换 `sec_key` 后，其既有令牌必须立即失效。
- **权限判定**：`auth.RequirePermission("user:create")`；无权限返回 403，
  且不得因为「先查存在性再查权限」而泄露资源是否存在。
- **敏感字段传输**：口令等敏感字段必须由前端 SM2 加密后提交
  （`data-yd-encrypt="sm2"` + `X-YD-Encrypted: sm2`），服务端用 `guomi.DecryptRaw` 解密。

## 8. 内存对齐（betteralign）

- 提交前必须对改动过的包执行内存对齐检查；**推荐**直接应用：
  ```bash
  betteralign -apply ./...        # 或 yd align --apply
  ```
- 结构体字段顺序以「减少 GC 扫描的指针字节数与结构体体积」为准；
  重排后必须保持字段语义、JSON/GORM 标签与可读性，`go test ./...` 必须仍然通过。
- `betteralign` 缺失时：`go install github.com/dkorunic/betteralign/cmd/betteralign@latest`，
  或把二进制放到 `.yd/tools/`。该工具是**可选**依赖，缺失不得导致构建失败。
- **工具链版本**：betteralign 必须用**不低于本模块 `go` 指令版本**的 Go 构建，否则会报
  `package requires newer Go version` 或
  `export data version ... is greater than maximum supported version`；
  按上面的 `go install` 重装即可（它默认使用本机最新工具链）。
- `yd align --apply` 会**迭代到不动点**：默认最多 5 遍，`--max-passes N` 可覆盖；
  每遍后诊断数归零或不再下降即停止，摘要以 `passes | 遍数: N` 报告实际遍数。
  单遍 `betteralign -apply` 不保证收敛，可能需要再跑一遍。
- 工具版本过旧时 `yd align` 打印重装提示并返回非零（哨兵 `align.ErrToolTooOld`），
  但绝不 panic，也不阻塞构建。

## 9. 前端 i18n 与主题（强制）

- 所有面向用户的文案必须带 `data-yd-i18n="key"`，并在 `web/i18n/{zh-CN,en}.yaml` 中提供
  **中英两份**目录；缺失键回退顺序为「当前语言 → 默认语言 → 键名本身」。
- 语言切换必须**局部刷新**（htmx 重取 `#yd-shell`，使侧栏、顶栏与工作区一起跟随），不得整页跳转；
  语言与主题都必须持久化（localStorage + cookie），刷新后保持一致。
- 主题支持 `light` / `dark` / `system` 三态，切换即时生效且不得出现闪烁；
  颜色一律来自 `@theme` 语义变量，禁止硬编码色值。

## 10. 常用命令

```bash
yd new <project>     # 初始化项目（交互式选择组件）
yd dev               # 热重载开发（Go 编译重启 + Tailwind 监听 + 模板局部刷新）
yd gen module|crud   # 低代码生成（DB Schema 或 Go Struct → 路由/控制器/模板）
yd build             # 跨平台静态打包（Tailwind 剪裁压缩 + go:embed + 单二进制）
yd docker            # 基于 scratch 的多阶段镜像构建
yd assets update     # 在线更新前端依赖并锁定校验和
yd assets verify --offline   # 离线完整性校验
yd assets minify     # 生成生产压缩资产（*.min.js、*.min.svg、yd.min.css）
yd assets minify --check     # 校验已提交的压缩资产是否漂移
yd icons sync        # 按模板引用重新生成 web/static/img/icons.svg
yd icons check       # 校验图标 sprite 是否漂移（CI 可用）
```

## 11. 优雅退出（强制）

**关停顺序固定**，任何入口都不得打乱（`server.Server.Shutdown` → `app.App.Shutdown` → `app.Run` 均实现同一序列）：

1. **关闭 `lifecycle` 的 `Done()` 信号并执行长连接钩子**（按注册逆序）——长连接在此收尾；
2. 关闭 HTTP/3 监听；
3. `http.Server.Shutdown` **排空**：停止接入、等待进行中的请求；
4. **强制关闭排空后仍存活的 hijack 连接**（`server` 通过包装 `http.Hijacker` 登记真实 `net.Conn`）；
5. gRPC 优雅停止，宽限期过后 `Stop()` 强制停止；
6. 逆序执行 `app.OnShutdown` 钩子（释放 DB / Redis / 队列等）；
7. 全部错误以 `errors.Join` 聚合返回，超时步骤**记为错误但绝不阻断后续步骤**。

- **长连接必须注册**：WebSocket 传 `ws.Options.Tracker`（退出时发 **1001 going away** 关闭帧再关套接字），
  SSE 传 `sse.Options.Tracker`（默认停止心跳、写最终注释帧并 flush、再关 `Broker`），
  其它自增长连接用 `app.Lifecycle().Add(name, fn)` 登记；`lifecycle.Registry` 并发安全、`Shutdown` 幂等、
  钩子 panic 被恢复（`ErrHookPanic`）、内建 `ErrHookFailed` / `ErrShutdownTimeout`；
- 连接处理器**必须监听关闭信号**（`lifecycle.Registry.Done()` / `sse.Writer.Done()`），
  不得只靠对端断开，否则 HTTP 排空会一直拖到宽限期；
- 只允许存在**一个宽限期**：`server.Options.ShutdownTimeout`（`app` 未显式设置时取 `defaultShutdownTimeout`）；
  任何自行 `go` 出去的关闭协程都必须能回答「它何时退出」；
- **禁止绕过门面**：应用入口一律用 `app.Run(ctx)` / `app.Shutdown(ctx)`；
  自行 `http.ListenAndServe`、或把 hijack 得到的裸 `net.Conn` 缓存到别处绕过登记，都视为缺陷。

## 12. 服务化部署（强制）

`service` 包（`github.com/shibingli/yd/runtime/service`，底层 `github.com/kardianos/service` v1.3.0）
是**唯一的系统服务入口**：Windows 走 SCM、Linux 走 systemd、macOS 走 launchd。
**禁止**为某个平台手写注册表项、unit 文件或 init 脚本，也**禁止**在业务代码里 fork / daemonize 自建后台进程。

- **两条调用路径，同一份语义**：运维侧用 `yd service <动作>`，被托管的应用侧用入口的 `-service <动作>`；
  两者都落到 `service.Control(o, a)`，动作取 `install|uninstall|start|stop|restart|status`，
  未知动作以 `ErrUnknownAction` 拒绝（`run` 只属于应用二进制）。
- **配置来源**：`service.name` / `display_name` / `description` / `executable` / `arguments` /
  `working_directory` / `user_name` / `depends_on` / `env` / `restart_on_exit`（环境变量 `YD_SERVICE_*`），
  统一经 `service.OptionsFromConfig(cfg)` 读取。服务名回退顺序：
  `--name` → `service.name` → `.yd.json` 的 `name` → 工作目录名。
- **CLI**：`yd service <动作> [--name N] [--display-name D] [--description X] [--binary PATH]
  [--dir DIR] [--user U] [--args "..."]`；省略 `--binary` 时先找 `bin/<项目名>[.exe]`，
  找不到就用 `build` 包编译一次，仍失败则给出「先 `yd build` 或传 `--binary`」的提示。
- **停止语义**：`service.Run` 收到停止只**取消传给业务的 context 并等待其返回**（缺省宽限 30s，超时
  `ErrStopTimeout`）；**禁止**用 `os.Exit` / `panic` / `log.Fatal` 「停服务」，收尾一律委托 `app.Run`（见「优雅退出」一节）。
- **控制动作不启服务**：`install|uninstall|start|stop|restart|status` 只执行一次服务管理器调用就返回，
  不得装配端口或进入前台循环。
- **测试不得真的装服务**：`service` 包经 `newControlHandle` 注入，`cli` 经 `serviceControlFunc` /
  `serviceBuildFunc` 打桩；测试进程内不得出现真实的 SCM / `systemctl` / `launchctl` 调用。
- **错误一律哨兵**：`ErrUnsupportedPlatform`（无受支持的服务管理器）、`ErrNotInstalled`、`ErrPermissionDenied`、
  `ErrUnknownAction` 等，调用方用 `errors.Is` 判定；权限失败必须提示管理员（Windows）/ root（POSIX）。
- **密钥不进服务文件**：unit 文件与 SCM 配置对运维可读，主密钥与数据库口令只经 `YD_*` 环境变量注入，
  `service.env` 中不得出现真实密钥。

## 13. 镜像打包（ko 与 Dockerfile）（强制）

镜像有两个、且只有两个入口：**`yd docker`**（多阶段 Dockerfile → `scratch`）与 **`yd ko`**
（`github.com/shibingli/yd/tooling/ko`，从 Go 源码直出镜像，**没有 Dockerfile 这一环**）。
**禁止**在脚本、CI 或文档里另行手写 `docker build` 参数或自拼 ko 命令行；两条路径的标志、标签、平台与基础镜像
都必须在各自命令里统一解析，绕过它等于让打包行为散成彼此不一致的副本。

- **分工**：需要定制构建步骤（装系统包、拷额外文件）走 `yd docker` 与仓库根目录的 `Dockerfile`；
  标准的「单二进制 + 空镜像」交付走 `yd ko`，它不需要 Dockerfile，也不要求本机有 docker 守护进程。
- **调用方式同构**：两者都调用**外部 CLI**（docker / ko）完成实际构建，因此 CI 与开发机只需装好对应工具，
  不引入任何 Go 构建期依赖。
- **CLI**：`yd ko [--repo R] [--tags a,b] [--platforms linux/amd64,linux/arm64] [--base-image X]
  [--package ./cmd/app] [--local] [--push] [--binary PATH] [--dir DIR]`。
- **配置键**：`ko.binary` / `ko.repository` / `ko.tags` / `ko.platforms` / `ko.base_image` / `ko.packages` /
  `ko.local` / `ko.push`，`YD_KO_*` 环境变量由既有前缀机制自动映射；优先级不变
  （命令行参数 > 环境变量 > 配置文件 > 框架默认值）。
- **二进制解析顺序**：`--binary` → `YD_KO_BIN` → `<项目>/.yd/tools/ko[.exe]` → `PATH`；
  找不到时返回 `ko.ErrBinaryNotFound` 并给出 `go install github.com/google/ko@latest` 提示，
  **绝不 panic**（与 `betteralign` 缺失时的口径一致）。
- **ko 属可选外部工具**：缺失只影响 `yd ko` 自身，**不得**让 `yd build` / `yd dev` / `yd gen` / `go test` 或 CI 失败。
- **禁止把 ko 作为 Go 依赖**：不得 `require github.com/google/ko`，也不得 import 它的包来「省一次外部进程」；
  ko 只经外部 CLI 调用（见第 4.8 条「依赖克制」）。同理，`go.mod` 不得因本特性新增任何依赖。
- **产物形态**：两条路径产出的都必须是 `CGO_ENABLED=0` 静态链接的二进制，前端资产由 `go:embed` 嵌入，
  镜像内无任何运行时依赖。
- **错误一律哨兵**：`ko.ErrBinaryNotFound` / `ko.ErrNoRepository` / `ko.ErrBuildFailed` / `ko.ErrInvalidOptions`，
  调用方用 `errors.Is` 判定（见第 4.2 条「错误处理」）。

## 14. 可选外部工具（强制）

`tools` 包（`github.com/shibingli/yd/tooling/tools`）是**唯一的探测与安装入口**：`betteralign`（内存对齐）、
`ko`（无 Dockerfile 镜像打包）、`tailwindcss`（样式表编译）、`minify`（JavaScript/SVG 压缩）、`docker`（镜像构建）
五个工具一律经它处理，注册表顺序固定为这五条。**禁止**在脚本、CI 或文档里另立一套「自己 `go install` 到全局」的指引。

- **API 与 CLI 同源**：`Registry()` / `Lookup(name)` / `Check(dir)` / `Missing(dir)` / `Hints(dir)` /
  `CheckWith(Options)` / `MissingWith(Options)` / `HintsWith(Options)` / `InstallWith(ctx, Options, names, log)`
  （`Options.Binaries` 接收 `align.bin` / `ko.binary` / `tailwind.bin` / `minify.bin`，使探测、安装与真实命令看到同一份配置）/
  `Install(ctx, dir, names, log)` / `InstallAll(ctx, dir, log)`，结果以 `*Report{Installed, Skipped, Failed}` 返回；
  CLI 侧是 `yd tools [check]`（列出每个工具的状态、路径或版本与安装提示）与
  `yd tools install [名称...]`（缺省为全部 **Go 可安装**项；`tailwindcss` 需显式点名，见下）。探测**只读**：
  不触网、不执行工具、不改动任何文件；提示文案复用各解析包的 `InstallHint`，不另写一份。
- **安装落点与解析链**：解析链固定为「显式参数/配置 → 环境变量 → `<项目>/.yd/tools` → `PATH`」
  （`LocalToolDir = ".yd/tools"`）；安装把 `GOBIN` 指向 **`<项目>/.yd/tools` 的绝对路径**——即解析链的第三段，
  装完无需任何额外配置。**不得改动用户 `PATH`，也不得往 `~/go/bin` 写东西**：往全局写属于越界，
  且在 CI 上不可预期。
- **Go 安装环境隔离（强制）**：执行 `go install` 前必须由 `installEnv` 注入
  `GOBIN` / `GOPATH` / `GOMODCACHE` / `GOCACHE` 到 `<项目>/.yd/tools` 之下（模块缓存、构建缓存与二进制都归本项目），
  `GOWORK=off`，并在调用方未显式导出时补上
  `GOPROXY=https://goproxy.io,https://goproxy.cn,https://mirrors.aliyun.com/goproxy/,direct` 与 `GOSUMDB=off`；
  显式导出的 `GOPROXY` / `GOSUMDB` 优先，空值回退默认值。删除 `.yd/tools` 即完成一次彻底重置。
- **工具一律可选**：缺失只影响对应能力——`yd align` / `yd ko` / `yd docker` 各自返回哨兵并给出安装提示，
  `tailwindcss` 缺失时样式表阶段以告警跳过，`minify` 缺失时 JavaScript/SVG 压缩阶段以告警跳过——
  **不得**让 `yd build` / `yd dev` / `yd gen` / `go test` 或 CI 失败。
  `betteralign`（`go install github.com/dkorunic/betteralign/cmd/betteralign@latest`）、
  `ko`（`go install github.com/google/ko@latest`）与
  `minify`（`go install github.com/tdewolff/minify/v2/cmd/minify@latest`）走 `go install`；
  `yd build` 侧由 `--minify-assets`（默认开启，配置 `build.minify_assets`）与 `--minify-bin`
  （配置 `minify.bin`）控制，解析链为 `--minify-bin` → `minify.bin` → `YD_MINIFY_BIN` →
  `.yd/tools/minify[.exe]` → `PATH`；`tailwindcss` 由
  `yd tools install tailwindcss` 下载 GitHub Release 的 standalone 二进制到 `.yd/tools/`（只安装它时不需要 Go 工具链），
  下载遵循 `http.proxy`（`YD_HTTP_PROXY`；为空时使用标准 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`）与
  `tailwind.url`（`YD_TAILWIND_URL`，GitHub 不可达时指向镜像）；`docker` 需安装 Docker Engine / Docker Desktop，
  `Install` 对它返回 `ErrNotInstallable` 并给出获取途径，而不是失败退出。`yd assets update`、`yd assets add` 与
  `yd versions upgrade` 的下载同样使用 `http.proxy`（标准代理环境变量为兜底）。代理 URL 可能含凭据，**不得**写入日志。
- **`yd new` 收尾统一处理**：项目生成后探测缺失工具并打印提示；交互模式询问「现在就安装缺失的工具吗？」
  （默认是），答是则 `InstallAll`（Go 可安装项；`tailwindcss` 按提示显式安装）；非交互场景用 `--install-tools` 显式安装；**`--yes` 不触网**
  （e2e / CI 入口不得带网络副作用）。安装失败只 Warn 并给出提示，绝不影响建项目成功。
- **错误一律哨兵**：`ErrUnknownTool` / `ErrNoGoToolchain` / `ErrNotInstallable` / `ErrInstallFailed`，
  调用方用 `errors.Is` 判定（见第 4.2 条「错误处理」）。
- **依赖方向单向**：`tools` 聚合 `align` / `ko` / `toolchain` / `build` 的解析器，这几者**不得**反向 import `tools`
  （成环会让提示文案被迫出现第二份副本，由 `repotest` 的守卫强制）。

## 15. 数据持久化与索引（强制）

租户、用户、角色与权限**存在真实数据库里**，由 `security/auth/gormstore` 承接（`auth.Store` 的 GORM 实现）。
内存实现 `auth.NewMemoryStore` 只在「未配置数据库」或「数据库不可达」时兜底，且**必须打出明确的双语告警**
说明「数据不持久」——绝不允许静默降级。

- **框架拥有鉴权表**：`yd_auth_tenants` / `yd_auth_users` / `yd_auth_roles` / `yd_auth_role_permissions`，
  建表与索引一律经 `gormstore.Migrate(ctx, db)`（`AutoMigrate`），使用方不需要自己写 DDL。
- **`auth` 域包保持零 ORM 依赖**：GORM 只允许出现在 `security/auth/gormstore` 子包里。
  否则内存实现与任何第三方存储都无法独立复用，领域规则也被绑死在一种数据库上（守卫 `TestAuthDomainStaysFreeOfGORM` 强制）。
- **每个方法都必须把 `tenant_id` 写进 WHERE**：`auth.Store` 里**没有任何**方法只按全局 ID 查询。
  跨租户或不存在一律返回 `auth.ErrNotFound`，**绝不**返回 `ErrForbidden`——后者会让调用方探测到别的租户是否存在数据。
- **索引一律是联合索引，且以 `tenant_id` 打头**（单列索引在这种查询下只能命中一半条件）：

  | 表 | 索引 |
  |---|---|
  | `yd_auth_tenants` | `slug_key` 唯一；`created_at` |
  | `yd_auth_users` | `id` 唯一；`username_key` **全局唯一**；`tenant_id`（身份租户）索引；`default_tenant_id` 索引；`created_at` |
  | `yd_auth_user_tenants` | `(user_id, tenant_id)` 唯一；`(tenant_id, user_id)`；`(tenant_id, created_at)`；`(user_id, is_default)` |
  | `yd_auth_roles` | `(tenant_id, id)` 唯一；`(tenant_id, name)` 唯一 |
  | `yd_auth_role_permissions` | `(tenant_id, role_id, permission)` 唯一；`(tenant_id, role_id)` |

  用户是全局身份：登录名（`username_key`）在整个部署内唯一，`yd_auth_users.tenant_id` 是不可变的身份租户，
  各租户角色存放在 `yd_auth_user_tenants`。`yd_auth_user_tenants` 是唯一一处既有以 `tenant_id` 打头
  （按租户列成员）又有以 `user_id` 打头（按用户列成员）索引的表，这是成员关系双向查询的必然结果；
  其它鉴权表一律以 `tenant_id` 打头。
- **升级必须回填成员关系**：旧库的 `yd_auth_users.roles` 列在迁移时复制为身份租户的成员关系，
  并补齐 `default_tenant_id`；`Migrate` 必须幂等，既有账号在升级后仍能登录。
- **迁移必须幂等**：连跑两次不得报错、不得重复建索引。
- **新增按 ID 读写的鉴权方法时，必须同时给出对应的联合索引**，并在 `repotest` 或该包的测试里断言它真的被建出来。

## 16. 连接串预置与无 CGO（强制）

**所有数据库连接串都要预置时区、时间配置、空值严格性与默认字符集**，不得依赖使用方逐个写对。
入口只有一个：`gormx.PresetDSN(driver, dsn)`，由 `dialectorFor` 在建立连接时统一施加。

- **显式参数优先**：使用方在自己的 DSN 里写了的键一律保留，预置只补缺。因此**脚手架生成的 DSN 不得写死
  与预置冲突的值**——旧模板里的 `loc=Local` 正是这样把 UTC 默认架空的，已改为由预置补齐。
- **预置是幂等的**：对同一连接串重复施加不产生重复参数。
- **默认时区是 UTC**（`loc=UTC` / `time_zone='+00:00'` / `TimeZone=UTC`），与框架日志一律输出 UTC 一致；
  需要本地时间的使用方在自己的 DSN 里显式写 `loc=Local` 覆盖。
- **预置表只有一份**：脚手架经 `gormx.PresetDSN` 生成示例 DSN，**不得**抄一份参数表到 `scaffold`。

### 16.1 SQLite：两个参数一存一亡

- **必须保留 `_txlock=immediate`**：先读后写的延迟事务会被并发提交顶掉快照，随后以
  `SQLITE_BUSY_SNAPSHOT` 失败，而**这是任何 busy timeout 都无法化解的**——存储层的预检正是「先读后写」。
- **绝不允许加入 `_time_format`**：`glebarez/go-sqlite` 的 `applyQueryParams` 一旦看到该键就**提前返回**，
  其后的 `_txlock` 分支永远走不到，`_txlock=immediate` 因此**静默失效**。而 `_time_format` 在该驱动上
  是**彻底的空操作**（其唯一取值 `"sqlite"` 就是未设置时的默认格式），等于用一个空操作换掉了并发写。
  实测：8 并发写，带该键 5/8 以 `SQLITE_BUSY_SNAPSHOT` 失败，去掉后 0/8。
  `data/gormx` 有注释与行为级断言固化这条约束。

### 16.2 目标库不存在时自动建库

连接账号**权限足够**、但目标库**尚未创建**时，`gormx.Open` 会自动建库并重试一次原连接
（配置键 `db.create_database`，**默认开启**）。

- **判定用错误码，不用字符串匹配**：MySQL `ER_BAD_DB_ERROR`(1049)、PostgreSQL `SQLSTATE 3D000`，
  经 `errors.As` 判定；其余错误一律不触发。SQLite 不参与（文件由驱动创建）。
- **建库语句必须带预置的字符集与排序规则**，否则会建出一个与 §16 预置不符的库，
  把字符集默认值架空。字符集取值**读预置表**，不得另抄字面量。
- **库名是外部输入**，必须按 SQL 自身的定界规则转义（MySQL 反引号成对、PostgreSQL 双引号成对）。
- **失败如实上报**：错误里**同时保留**「库不存在」与「建库失败」两个原因，绝不吞掉任何一个。
- **固有代价（必须知晓）**：自动建库让「配置写错」与「库确实还没建」难以区分——
  库名写成 `prod_usr`（少个 e）时会**建出一个空库并正常启动**，问题推迟到数据缺失才暴露。
  唯一缓解是建库时那行**带库名的 Warn 日志**；关闭方式是 `db.create_database: false`。

### 16.3 默认无 CGO

- **默认构建形态是纯 Go、零 cgo**：二进制静态链接、可直接放进 `scratch` 级空镜像、
  交叉编译不需要目标平台的 C 工具链。`yd build` 的 `buildEnv` 固定注入 `CGO_ENABLED=0`，
  且**内置值优先于进程环境**——shell 里是 `CGO_ENABLED=1` 也会被覆盖，只有显式传 `Options.Env` 才开得起来。
- **SQLite 必须是纯 Go 实现**：`github.com/glebarez/sqlite`（→ `modernc.org/sqlite`）。
  **禁止**引入 `github.com/mattn/go-sqlite3` 等需要 cgo 的驱动——它会一次性破坏上面三条性质，
  且症状要到交叉编译或容器运行时才暴露（守卫 `TestNoCgoInDefaultBuild` 强制，覆盖两种 `require` 写法）。
- 使用方日后自行引入 cgo 是他们的选择，本约束只保证**框架自身**不是那个引入点。

## 17. 角色与权限常量（强制）

`authz` 包（`github.com/shibingli/yd/security/authz`）是角色与权限的**唯一事实来源**。
这些字符串**只允许在该包的注册表里出现一次**，其余任何地方——Go 代码、模板、JS、脚手架产物——
都必须引用常量或注册表派生的映射。

- **四种引用形态**（同一枚权限，四个消费方各取所需）：

  | 消费方 | 形态 | 例 |
  |---|---|---|
  | 领域标识 | 字符串 | `user:create` |
  | Go 后端 | 常量 | `authz.PermUserCreate` / `authz.RoleAdmin` |
  | Go 模板 | 数据键 | `{{ if can .Perm.UserCreate }}` |
  | 前端 JS | 全局映射 | `yd.can(yd.PERM.USER_CREATE)` |

- **后端一行判定**：`authz.Can(ctx, perm)` 只判断、`authz.Allow(c, perm)` 无权限写 403、
  `authz.Require(perm)` 作中间件、`authz.HasRole(ctx, role)` 判角色。
  403 一律**不得泄露资源是否存在**（见第 7 节）。
- **前端三件套**：模板函数 `can` / `hasRole` / `perm` / `role`；
  声明式属性 `data-yd-perm="…"`（配 `data-yd-perm-mode="hide|disable"`）；
  JS API `yd.can()` / `yd.hasRole()` / `yd.perms()` / `yd.roles()` / `yd.bind(root)`。
  声明式属性在页面加载与**每次 htmx 交换后**都必须重新生效。
- **红线（必须写进任何涉及权限的前端代码注释）**：**前端判定只是体验优化，不是安全边界**。
  隐藏按钮不等于禁止操作，每个受保护端点仍必须在服务端校验。载荷缺失或损坏时**一律失败关闭**（拒绝全部能力）。
- **权限下发**：由 `authz.Data(subject)` 生成载荷，经 `<script id="yd-authz" type="application/json">`
  内联注入，**零额外请求、零 CDN**；不得把权限集合放到可被前端随意改写的存储里当作判据。
- **静态验证 `yd authz check`**：把每一处权限引用与注册表比对，报三类问题——
  ①引用了**未注册**的能力（拼错成 `user:creat` 会静默失效，这是最有价值的一类）；
  ②**裸字符串**（在 `authz` 包外直接写 `"user:create"`）；③注册了但从未被引用（提示，不计失败）。
  有错误即**非零退出**，可直接进 CI。`yd authz list` 打印注册表。
- **新增能力时**：先在注册表声明（含分组与双语描述），再在至少一处引用；
  默认角色绑定同样取自注册表，**不得**在种子数据或业务代码里另写一份角色-权限清单。

## 18. 默认初始化数据（强制）

`yd new` 产出的项目**首次启动即自带一套可登录、可授权的数据**：默认租户、管理员账号、
注册表里的全部角色与权限，以及**角色→权限**与**用户→角色**的绑定。

- **单一事实来源**：角色名与权限名一律取自 `authz` 注册表；角色→权限的绑定取自
  `authz.BuiltinGrants()`。种子代码里**不得出现角色/权限字符串字面量**
  （否则 `yd authz check` 判为裸字符串，见第 17 节）。
- **幂等**：只在租户表为空时初始化；唯一索引冲突视为「另一副本已初始化」而非错误。
  多次启动、多副本并发启动都不得产生重复数据。
- **生产安全（红线）**：
  - 口令从 `seed.admin_password` / `YD_SEED_ADMIN_PASSWORD` 读取；
  - **development** 未配置时回退到**文档化的开发口令**并**每次启动告警**；
  - **非 development** 未配置时**拒绝初始化**并告警——**绝不用默认口令在生产开一个管理员账号**；
  - 口令**永不写入日志**，告警只带键名、环境变量名与 `app.env`。
- **期望值不得手抄**：测试里种子行数的期望值应当**由注册表推导**
  （`2 × len(authz.Roles())` 个角色、`2 × Σ len(grant.Permissions)` 条绑定），
  否则种子一改，测试就会以「期望值过期」的形式误报。

## 19. Shell 补全与环境增强（强制）

`yd completion` 提供 bash / zsh / csh / powershell 四种补全：

```
yd completion bash|zsh|csh|powershell   # 打印脚本（可 source <(yd completion bash)）
yd completion detect                     # 报告检测到的 shell 与将写入的位置
yd completion install [--shell S] [--dir D] [--print] [--append-rc] [--yes]
```

- **从命令注册表生成，禁止手写脚本**：手写四份必然与命令、标志漂移。新增命令或标志时补全必须自动跟上，
  由测试断言「每个已注册命令都出现在四种脚本里」。
- **默认绝不碰 rc 文件**（`~/.bashrc`、`~/.zshrc`、`~/.cshrc`、`$PROFILE`）：
  与「不得改动用户 PATH」同一原则。只有显式 `--append-rc` 才追加，且必须**幂等**（带标记行）、
  **先备份**（备份与改动前逐字节一致）、并在输出里说明改了什么、备份在哪。
- **环境增强片段默认注释掉**：脚本末尾的 `.yd/tools` 加 PATH 片段保持注释状态，由用户自行启用。
- **PowerShell 的脚本文件名必须是 `.ps1`**：点源运算符只加载 `.ps1`，
  用 `.powershell` 会**静默不生效**——脚本装好了、rc 也写了，补全却永远不工作。
  这是实测过的坑（`. tiny.txt` 不执行、`. tiny.ps1` 执行），属于对「`completion.<shell>`」字面约定的唯一例外。
- **语法检查不得静默跳过**：本机有对应解释器时做真实检查（`bash -n`、PowerShell 解析器）；
  没有时 `t.Skipf` 写明原因并汇总「执行 N 项、跳过 M 项」，**不得**因为跳过而显示通过。

## 20. 依赖升级与模板版本同步（强制）

依赖升级是常规动作，必须走一条**可重复、可校验**的路径；禁止把版本号手抄进模板或常量。

### 20.1 唯一真源

| 内容 | 真源 |
|---|---|
| Go 版本与 Go 模块依赖 | 仓库根 `go.mod` |
| 前端资产版本 | `web/assets.lock.json` |
| 脚手架 `GoVersion` / `HtmxVersion` | `tooling/scaffold/version_generated.go`（**生成文件**） |

- `version_generated.go` 由 `yd versions sync` 产出，文件头带
  `// Code generated by yd versions sync; DO NOT EDIT.`，**禁止手改**。
- `TailwindVersion` 是唯一手工维护项：standalone 编译器由 PATH / `.yd/tools` 解析，没有锁定版本，
  因此只维护大版本标签（`v4`）。

### 20.2 升级流程

```bash
yd versions upgrade            # go get -u ./... + go mod tidy + npm 最新资产重锁 + 重新生成
yd versions upgrade --dry-run  # 只打印计划，不执行 go、不下载、不写文件
yd versions upgrade --go=false # 只升级前端资产
yd versions check              # 离线校验生成文件与真源一致（CI 可用的非零退出）
yd versions sync               # 仅重新生成 version_generated.go
```

- `upgrade` 按顺序执行：Go 模块 → npm 资产（jsDelivr / unpkg 的 `@ver` 段改写为 registry 最新版，
  下载并重新锁定 sha256）→ 重新生成脚手架常量。
- registry 查询或 Go 模块升级失败时，在改写任何资产之前返回错误；资产下载阶段失败会返回错误，
  此时锁文件可能已指向新版本而文件尚未全部重锁，重新运行 `yd versions upgrade`（或
  `yd assets update`）即可收敛。未变化的资产保留原校验和，因此仍能拒绝被移动的标签或被篡改的镜像。
- **生成项目也可用**：`yd new` 产出的项目没有 `tooling/scaffold`，`yd versions upgrade` 只升级
  Go 模块与前端资产并跳过生成文件那一半；`check` 同样跳过生成文件校验。
- 升级后必须跑完整门禁：`go build ./... && go vet ./... && go test ./... -race`。

### 20.3 约束

- **禁止手改版本常量与模板里的版本号**：一切经 `yd versions`。
- 改动 `go.mod` 或 `web/assets.lock.json` 的提交，**必须同时提交** `yd versions sync` 的产物；
  守卫 `TestScaffoldVersionsMatchSources` 拦截漂移。
- 前端资产只允许经 `yd versions upgrade` 或 `yd assets update` 重锁；
  **禁止手改** `web/static/js/vendor/**`。
- **升级导致的测试失败是信号，不是噪音**：先判断是上游行为变化还是本仓缺陷。
  例：`go-sql-driver/mysql` v1.10 调整了 `FormatDSN` 的参数顺序，钉死字符串顺序的用例因此失败——
  应更新期望并注明上游版本，而不是把测试删掉。
- `yd new` 的模板版本来自二进制内嵌的生成常量：发布新二进制即让**新项目**得到新版技术栈；
  既有项目用 `yd versions upgrade` 跟进。
- **Dockerfile 的构建镜像标签同样派生**：`golang:{{ .GoMajorMinor }}-alpine` 由 `GoVersion` 计算，
  禁止在模板里手写 `golang:1.27` 这类固定标签。

## 21. 限流与客户端地址（强制）

- **按 IP 限流必须使用 `middleware.KeyRemoteIP(trusted)`**（底层 `router.Ctx.ClientIP(trusted)`），
  **禁止**用 `Ctx.RealIP()` 做限流、审计等安全判定：后者无条件信任 `X-Forwarded-For` 的最左值，
  而最左值由客户端控制，改一个头就能换一个新桶。
- **信任模型**：对端不在 `TrustedProxies` 内时**完全忽略**代理头，直接用 `RemoteAddr`；
  对端可信时从 XFF **最右**往左走、跳过可信代理，取第一个不可信地址；畸形、超长（>8 KiB）
  或超过 64 跳的链一律回退到对端地址。`TrustedProxies` 为空即安全默认值（只看对端）。
- **多机限流必须用 `middleware.RedisStore`**：令牌桶在 Lua 脚本内原子完成「读→补充→判定→写回」，
  时间取 Redis 服务器的 `TIME` 而非各实例本地时钟；键带统一前缀与 TTL。
  令牌数一律以**定点小数**字符串落库（`string.format('%.9f', …)`）：部分内嵌 Lua 引擎
  （含测试替身 miniredis）不接受科学计数法，`tonumber("1e-09")` 返回 nil 会让桶被误判为新桶而重置。
- **故障策略必须显式选择**：`RateLimitOptions.FailClosed` 默认 `false`（失败开放并记 Warn），
  设为 `true` 时存储故障返回 **503**。限流是保护措施，不得因存储故障静默放大流量；
  保护稀缺资源的场景应选择失败关闭。
- **指纹不是身份**：`KeyFingerprint` 只提高门槛，不能替代认证；UA 与请求头都可伪造。
  指纹经 FNV-1a 哈希后才作为键，原始 UA/IP 不得进入日志或 Redis 键。
- **新增维度走 `KeyFunc`**：缺失值一律返回固定标记（`-`），**不得返回空串**——
  空串会把所有无法识别的请求挤进同一个桶，把按维度限流悄悄变成全局限流。
- 共享 `LimitStore` 的多条规则必须使用不同的 `KeyPrefix`，否则不同规则会互相消耗令牌。

## 22. 前端交互、样式与实时推送（强制）

### 22.1 外壳、导航与语言

- **局部导航换整块外壳**：侧栏与顶栏内的导航/语言链接一律 `hx-target="#yd-shell"`，服务端在
  `HX-Target` 命中 `yd-shell` 时先渲染页面片段、再以 `layouts/shell.html` 包裹（`Controller.renderShell`）。
  只换 `#yd-workspace` 会让侧栏高亮与语言停在旧状态，这是必须避免的回归。
- **`#yd-shell` 必须同时是 flex 容器**：侧栏与主列是它的直接子元素；否则主列从 `x=0` 起排、
  滑到侧栏下面。
- **语言切换是单一状态按钮**：`partials/lang-toggle.html`（生成项目为 `partials/shell-switches.html`）
  只渲染一个按钮，显示当前语言（`lang.zh-CN` / `lang.en` 两个状态标签由 `<html lang>` 经 CSS 选出），
  点击切到另一种语言并以此前的 `#yd-shell` 局部刷新；不得再渲染两个并排的语言按钮。
  外壳交换不会重写 `<html>`，因此点击时必须同步 `document.documentElement.lang`，否则状态标签和
  辅助技术会停在旧语言；无脚本时 `href` 指向另一种语言，整页导航仍然可用。
- **登录页只保留登录功能**：未登录外壳没有侧栏、顶栏导航、页脚、租户字段与凭据说明，
  只有登录卡片（含品牌区：离线 SVG 图标 + 项目名）与右上角的语言/主题切换。登录成功后由服务端落入用户的默认租户。
- **图标必须离线**：favicon 指向同源静态资产 `/static/img/yd-icon.svg`（框架 `web/static/img/yd-icon.svg`，
  随二进制嵌入），禁止 `data:,` 空图标、CDN 或外链；登录卡片品牌区复用同一图标。
- **侧栏页脚只显示版本**：中间件与组件清单属于构建细节，不再出现在导航里。
- **接口组一律新标签页打开**：接口文档与 OpenAPI JSON（以及页面内的 `/docs`、`/openapi.json` 链接）
  都带 `target="_blank" rel="noopener noreferrer"`；它们是整页/机器响应，不能换进工作区。

### 22.2 抽屉、确认与列表操作

- **新增/编辑一律用右侧抽屉**（`<dialog id="yd-drawer">` + `data-yd-drawer-open` + `hx-get` 片段），
  页面不预渲染隐藏表单；写操作成功（2xx）后 `yd-app.js` 关闭抽屉，4xx 保持打开以便就地修正。
- **所有变更动作必须确认**：退出、删除、启停、轮换 sec_key、重置口令、创建、保存、切换租户、
  修改默认租户都带 `data-yd-confirm`；确认对话框的文案来自模板的 `tr`，脚本里不写业务文案。
  表单在捕获阶段拦截后必须用 `requestSubmit` 重新提交，且要识别 htmx 4 的 `detail.ctx` 形态。
- **列表操作用下拉菜单**：用户与角色列表的行内动作统一放在 `details.yd-menu` 中；
  面板由脚本提升为 `position: fixed` 并用 `right:auto` + `left` 定位，避免被表格容器的
  `overflow-x` 裁切。**顶栏租户切换复用同一套 `details.yd-menu` 面板与 `.yd-menu__item`**，
  不再使用原生 `select`（其选项由操作系统绘制，无法与站内控件统一）。
- **确认框必须保留提交者**：多按钮表单（例如租户菜单每个条目各带一个 `tenant` 值）在确认后要用
  `form.requestSubmit(event.submitter)` 重新提交；不带 submitter 的 `requestSubmit()` 会丢掉按钮的
  `name`/`value`，服务端因此收不到目标值。
- **轮询与变更不得写入历史**：`body` 的 `hx-push-url:inherited` 为 `false`，只有导航链接显式
  `hx-push-url="true"`；指标轮询必须 `hx-target="this"` 且 `hx-push-url="false"`。

### 22.3 Tailwind v4 与主题

- **所有项目样式继承 Tailwind v4**：`app.css` 必须 `@import "tailwindcss"`，设计令牌写在 `@theme`，
  项目与框架组件写在 `@layer components`；可复用工具类用 `@apply` 引入，组件自身几何尺寸才写普通声明。
- **颜色只来自语义令牌**：浅色/深色/跟随系统三态由 `<html data-theme>` 承载，暗色只重写令牌；
  禁止在令牌块之外写颜色字面量（守卫 `TestNoHardcodedColorsInAppCSS`）。
- **原生控件也要适配**：`:root` / `[data-theme="dark"]` / `auto` 三处声明 `color-scheme`，
  `dialog`、`select`、滚动条与焦点环随主题切换。
- **主题切换按钮保持高对比**：使用 `.yd-theme-toggle` 的实心表面与内容色，不得只依赖 ghost 变体。

### 22.4 首页资源仪表盘

- 首页通过 `GET /metrics` 每 5 秒轮询并 `innerMorph` 到 `#yd-metrics`，展示宿主机 CPU 核心数/利用率、
  总内存/利用率与当前进程 CPU/内存。仪表盘是纯服务端渲染的 SVG（`pathLength="100"` +
  `stroke-dasharray`），不得引入图表库或内联脚本。
- 指标来自 `core/sysinfo`（`CPUSnapshot` / `MemoryTotal` / `MemoryAvailable`，Windows 走
  `GetSystemTimes` / `GlobalMemoryStatusEx`）与 `runtime/metrics`；平台不提供时显示「不可用」，
  **不得编造数字**。首次采样允许约 150ms 的窗口等待，之后只做一次快照。

### 22.5 htmx 4 实时通道与 OpenAPI 查看器

- **SSE 推送必须是匿名消息**：htmx 4 移除了 `sse-swap`，只自动交换无名 `data:` 消息；
  把事件命名为主题名会变成 DOM 事件，页面会永远停在「连接中」。`Broker.PublishHTML` 因此发送
  无名消息，命名事件只用于 `yd:tick` 之类的旁路通知。
- **WebSocket 发送控件必须在连接元素内部**：htmx 4 的 `hx-ws:send` 用 `closest()` 从发送元素向上找
  `hx-ws:connect`；把输入框/按钮放在连接元素之外会报 “No WebSocket connection found”，消息根本不会发出。
  连接元素还必须给出 `hx-target` / `hx-swap`，回显与主动推送都依赖它们。
- **htmx 4 的 WS 信封与旧版不同**：上行是扁平 JSON（表单字段为顶层键，请求头在 `headers`），下行只交换
  `content`。`ws.HtmxMessage` 同时接受扁平与旧的 `HEADERS`/`BODY` 形态，把唯一值或 `message` 字段解为
  `Body`，默认回显以 `{"content":"…"}` 应答；`hx-partial` 原样文本由 htmx 核心改写为 `template[type=partial]`。
- **回车发送不得用 `hx-trigger` 过滤器**：`keyup[key=='Enter']` 由 htmx 用 `new Function` 编译，
  会被 CSP 的 `script-src 'self'` 拦截；由 `yd-app.js` 的普通监听器点击同一个发送按钮。
- **错误事件不等于断开**：`htmx:ws:error` 也可能来自一次发送失败而套接字仍然开着，连接徽标与横幅
  只应响应 `htmx:ws:close` / `htmx:sse:close` / `htmx:sse:error`，否则会把正常连接误报为断开。
- **离开实时页面不算掉线**：`yd-app.js` 只在页面存在 `hx-sse:connect` / `hx-ws:connect` 元素时显示
  断线横幅，导航导致的 SSE 流中止不得提示「连接已断开」。
- **重连由扩展负责，前端只观察**：hx-sse / hx-ws 默认以指数退避 + 抖动重连（`reconnectDelay` 500ms、
  `reconnectMaxDelay` 60s、后台暂停），SSE 自动回传 `Last-Event-ID`，WS 在断线期间排队最多 100 条上行消息
  并在重开后 flush。不得再写第二套重连定时器或自行重开第二条连接；`yd-app.js` 只消费
  `htmx:sse:*` / `htmx:ws:*` 事件并更新界面。
- **一个横幅、四种状态**：`#yd-connection` 是唯一的重连横幅，由聚合状态驱动：连接中、重连中（附 `#N`
  重试次数）、已失败、网络离线。所有通道恢复后立即隐藏，并以 3 秒的 `realtime.status.recovered` 成功提示收尾。
  导航或 `htmx:after:swap` 必须清理定时器、提示节点与已移除通道的状态，不得把旧页面的掉线提示带到新页面。
- **恢复通信有两条声明式通道**：通道恢复时派发 `yd:realtime:recovered`（含 `channel` / `attempt` /
  `lastEventId` / `resynced`），并按元素的 `data-yd-resync`（配合 `data-yd-resync-target` /
  `data-yd-resync-swap`）重取一个数据片段；重取目标不得是承载该数据流的元素本身，否则会再次拆掉连接。
- **手动重试不重载页面**：`[data-yd-reconnect]` 通过扩展记录在元素上的连接对象取消退避并立即重连
  （SSE abort 当前流、WS 无状态码关闭并以 1005 触发重连——浏览器客户端只允许 1000 与 3000-4999），
  只有连接对象已不存在时才回退整页重载。
- **查看器脚本必须同源外链**：CSP 是 `script-src 'self'`，内联脚本会被浏览器拦截，
  表现为 `/docs` 页面空白。查看器脚本固定在 `web/static/js/yd-openapi-viewer.js`，
  由 `openapi.Options.ViewerScript` 指向（当前 `?v=5` 用于绕过旧缓存），禁止内联脚本与 CDN。
- **查看器交互**：左侧导航按第一个 `tag` 分组（`doc.tags` 顺序优先，随后是未声明的 tag，
  最后是 webhooks、数据模型与安全方案）；操作详情提供**同源在线测试**——服务器、鉴权头、
  path/query/header 参数与 JSON 请求体，响应显示状态、耗时、响应头与正文。测试请求只允许
  同源（`connect-src 'self'`），跨源目标直接提示而不发送，会话 cookie 自动携带。
- **查看器主题跟随应用**：查看器读取同一份 `localStorage.yd_theme`，顶栏按钮按
  浅色 → 深色 → 跟随系统循环；表单控件边框使用 `--field` 令牌，明暗两套主题都必须清晰可见。

### 22.6 生产资产压缩（强制）

- **开发期用源码，生产期用压缩版**：`yd dev` / `app.env != production` 时模板引用源码；
  `yd build` 在编译前执行压缩阶段，产物写在 `web/static` 旁边并由 `go:embed` 打进二进制。
- **分工固定**：CSS 只由 Tailwind v4 的 `--minify` 产出 `yd.min.css`（`yd.css` 是 Tailwind 的编译产物，项目构建时同样带 `--minify`，供开发与回退）；
  JavaScript 与 SVG 只由可选工具 `minify`（tdewolff/minify）产出 `*.min.js` / `*.min.svg`。
  禁止用其它压缩器处理 CSS，也不得用 `minify` 处理 Tailwind 输入 `app.css` / `components.css`。
- **模板必须走 `asset` 助手**：所有 `/static/...` 引用写成 `{{ asset "/static/..." }}`；
  production 下助手优先解析同名 `.min.js` / `.min.css` / `.min.svg` 并对其内容取哈希，文件缺失时回退源码。
  已是 `.min.*` 的 vendored 资源原样解析，不会二次加后缀。
- **合并静态树**：`render.Options.AssetFS` 必须指向与 `Router().StaticFS` 相同的 `/static` 合并树
  （生成项目通过 `app.WithStaticFS(web.Static())` 注入），否则框架共享脚本在 asset 助手里找不到。
- **覆盖层不得串层**：`afs.HideShadowedMin` 在项目层提供源码但没有压缩同族文件时隐藏框架层的 `.min`，
  防止框架示例脚本顶替项目脚本；开发期磁盘层同样应用该规则。
- **`--minify-assets=false` 只抑制重新生成**：已有的 `.min` 文件仍可能被解析；框架层同名文件已由
  `HideShadowedMin` 隐藏，项目自有的陈旧 `.min` 由 `yd assets minify` 重新生成或按清单清理。
- **入库边界**：框架共享资产的 `.min` 文件提交入库（随 `frameworkweb.FS()` 进入生成项目）；
  生成项目自有的 `.min` 文件由 `.gitignore` 忽略，`yd build` 每次重新生成。
- **工具缺失**：`yd build` 与 `yd assets minify` 普通模式告警并跳过（asset 助手回退源码）；
  `yd assets minify --check` 在工具缺失、文件缺失或内容漂移时返回非零，供 CI 使用。
- **守卫**：`tooling/build/minify_sync_test.go` 在工具可用时逐文件比对已提交的 `.min` 与源码，
  漂移即失败；工具缺失时 `t.Skipf` 并说明原因。守卫同时覆盖 `.js` 与 `.svg`。

### 22.7 离线图标（Iconify 子集，强制）

- **唯一引用形式**：所有图标必须写成 `{{template "partials/icon" "lucide-<name>"}}`，
  禁止内联 `<svg>`、裸 `<use href="#i-…">`、Iconify 运行时脚本、`iconify-icon` 自定义元素，
  以及任何运行时 Iconify API / CDN 请求。图标是装饰性的，默认 `aria-hidden="true"`；
  语义必须由按钮或链接的可见文本 / `aria-label` 提供。
- **按需 sprite**：`yd icons sync` 扫描 `web/templates` 与脚手架模板树中上述调用，
  只把实际引用的图标写进 `web/static/img/icons.svg`；未经引用的图标不得进入前端产物。
  新增/删除图标引用后必须运行 `yd icons sync` 与 `yd assets minify`，并提交两个产物。
  CI 运行 `yd icons check`；守卫 `TestRepositorySpriteMatchesTemplates` 拦截漂移。
- **图标源固定**：`tooling/icons/data/lucide.json` 是 `@iconify-json/lucide@1.2.146` 的逐字节副本
  （ISC，SHA-256 记录在 `tooling/icons/data/README.md` 与常量中），仅作为构建期输入；
  禁止复制完整 JSON 到 `web/static` 或应用二进制。替换版本必须同时更新版本、许可与校验和。
- **尺寸与颜色**：`.yd-icon` 提供默认尺寸与行内对齐；颜色继承 `currentColor`，
  尺寸/颜色继续用 Tailwind v4 的 `size-*` / `text-*` 或语义令牌，不得写色值字面量。
- **主题按钮的图标切换**：主题按钮携带 `lucide-sun` / `lucide-moon` / `lucide-monitor` 三枚图标，
  由 `data-yd-theme-state` 经 CSS 选出；语言按钮使用 `lucide-languages`；登录/登出使用
  `lucide-log-in` / `lucide-log-out`。新增按钮必须沿用同一片段，不得各写一套图标。
  OpenAPI 查看器的 `#theme-toggle` 同样使用这三枚图标（`data-theme-choice` + CSS），
  脚本只更新 `#theme-label`，**不得**用 `textContent` 覆盖按钮，否则会清掉图标节点。
- **SVG 压缩**：sprite 与其他 `.svg` 一样进入 `minify` 阶段，生产模板解析 `.min.svg`；
  `yd assets minify --check` 同时校验 JS 与 SVG。不要手工编辑生成文件
  （`icons.svg` 文件头带生成标记）。
- **使用文档**：`docs/icon-usage.md` 是新增/更新图标、命令、许可与故障排查的唯一入口。

### 22.8 系统资源与中间件运行时指标（强制）

- **唯一抽象层**：`runtime/metricsx` 定义 `SystemSource` / `MiddlewareSource` / `Provider` / `Collector`。
  系统仪表盘与中间件卡片都只读取 `Provider`，控制器不得直连 `MetricsService` 或另算指标；
  首页一次 `Provider.Snapshot()` 同时供给两张卡片，`/metrics` 使用其中的 `System` 部分，显示契约不变。
- **内建中间件开箱即观测**：`app.Default` 经 `UseObserved` 登记 `recover` / `request-id` / `access-log` /
  `compress` / `cors` / `i18n`；生成项目另加 `security` 与路由组的 `promote` / `auth` / `authz` / `tenant`。
  任何中间件都必须用 `application.UseObserved(descriptor, mw)` 装配；只调用 `Use` 会让它从卡片上消失。
- **描述符集中登记**：生成项目的名称、`TitleKey` 与作用域只在
  `internal/middleware/observability.go` 出现一次，其余位置经 `middleware.MetricsDescriptor("name")` 取值。
  未知名称回退为 `custom` 作用域，项目因此可以加入自定义中间件而不改框架。
- **全生命周期**：状态固定为 `registered → active → stopped`；`app.Run` 启动时 `Start`，`app.Shutdown`
  经自动登记的钩子 `Stop`。`Stop` 幂等，关闭后计数保留，迟到请求不得把条目翻回 `active`。
- **请求指标**：`Requests` / `InFlight` / `Aborted` / `Errors`（5xx）/ `Panics`、平均/P95/最大延迟与 UTC
  `LastSeen`。panic 只由最内层观察者计数并原样重抛，外层 `Recover` 仍负责 500；不得吞掉 panic。
  `Aborted` 同样只归给最靠近终止点的观察者；`Errors` 表示“经过该中间件的请求以 5xx 结束”，各层分别记录。
- **热路径约束**：自动指标只用原子操作，不取锁、不做反射、不分配直方图。`Entry.Incr/Add/SetGauge`
  的自定义计数会取一把小锁，应按事件调用，不得无意义地每请求重复调用。
- **同名合并**：同一 `Name` 只占一行，计数合并；同时挂在公开组与受保护组的 `tenant` 因此不会被拆成两行。
- **端点与前端**：`GET /metrics` 返回系统资源片段，`GET /metrics/middleware` 返回中间件片段，二者都在
  受保护分组内。首页在系统资源下方渲染 `#yd-middleware-metrics`，每 5 秒 `innerMorph` 刷新，
  不写历史、不引图表库、不写内联脚本。
- **i18n**：所有表头、状态、作用域与内建中间件名称都来自 `metrics.middleware.*` 键，
  `zh-CN` 与 `en` 两份目录必须同时存在；缺失或陈旧键由 `TestGeneratedI18nCatalogueCoversEveryTrKey` 拦截。
- **使用文档**：`docs/middleware-metrics.md` 是接口、自定义指标、生命周期与检查清单的唯一入口。
- **禁止事项**：不得为每个请求创建 `Entry`/直方图，不得在请求路径取注册表写锁，不得用 `Use` 装配后再
  另补一套统计，不得在关闭后重新置为 `active`。
