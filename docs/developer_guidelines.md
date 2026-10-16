# 前后端协同约束（developer_guidelines）

yd 采用「全栈单体（Modern Monolith）」模式：**后端直接返回 HTML 片段，前端由 Htmx 4 作为事件总线驱动**。本文件是前后端协作的强制契约。

## 1. 响应契约

| 规则 | 说明 |
|---|---|
| **端点返回 HTML 片段，不是 JSON** | 除显式标注为 API 的端点（`/api/**`、`/openapi.json`）外，所有控制器必须返回 HTML 片段。 |
| **区分整页与片段** | 通过请求头 `HX-Request: true` 判定。整页请求返回布局 + 内容；htmx 请求只返回内容片段（`app.Render` 自动选择）。 |
| **校验失败返回 422** | 表单校验失败返回 `422` + 错误片段，模板中用 `hx-status:422="target:#errors"` 声明式接管。 |
| **重定向走 Htmx 头** | 局部跳转用 `HX-Location`，整页跳转用 `HX-Redirect`，禁止在片段响应里塞 `<meta refresh>`。 |
| **多区域更新优先用 `<hx-partial>`** | 需要同时更新多个区域时，用 `htmx.RenderPartials` 输出 `<hx-partial hx-target=… hx-swap=…>`；`hx-swap-oob` 仅用于兼容场景。 |

## 2. 模板目录约定

```
web/templates/
├── layouts/     # 整页骨架（base.html、dashboard.html）
├── partials/    # 可复用片段（sidebar、table、form、modal、flash、pagination、skeleton）
└── pages/       # 业务页面（整页 + 其片段视图）
```

- 片段命名：`<资源>/<动作>.html`，例如 `users/list.html`、`users/row.html`、`users/form.html`。
- 禁止在模板中书写内联样式表；样式一律来自 Tailwind v4 语义化类（`bg-surface`、`text-content`、`border-border-subtle`、`bg-brand-600` …）。
- 禁止硬编码颜色字面量（`#1d4ed8` 之类）；需要新色值请在 `web/static/css/app.css` 的 `@theme` 中新增语义变量。
- 所有面向用户的文案必须带 `data-yd-i18n="<key>"`（前端 i18n 键），键名与 `configs/i18n/*.yaml` 保持一致。

## 3. Htmx 4 属性约定

- **属性继承是显式的**：父元素上的属性必须加 `:inherited` 修饰符才会被子元素继承，例如
  `<nav hx-target:inherited="#yd-workspace" hx-push-url:inherited="true">`。这对初学者是最大的坑。
- 全局导航使用 `hx-boost`，工作区容器 id 固定为 `yd-workspace`。
- 需要保留输入状态（如表格内联编辑）时使用 `hx-swap="outerMorph"` / `"innerMorph"`；需要彻底重置表单时使用 `outerHTML`。
- 耗时操作必须提供 `hx-indicator` + `.htmx-indicator` 元素；首屏骨架屏使用 `.yd-skeleton`。
- 慢查询/长任务用 `hx-preload` 预热 + `hx-pending` 占位，不得出现白屏。

## 4. 插件能力映射（Htmx 4.0 内建 vs 扩展）

| 需求 | 实现方式 | 是否需额外文件 |
|---|---|---|
| WebSocket 双向通信 | `dist/ext/hx-ws.min.js`（注册名 `ws`） | 是 |
| SSE 单向实时流 | `dist/ext/hx-sse.min.js`（注册名 `sse`） | 是 |
| 多阶段加载 / 骨架屏 | `dist/ext/hx-preload.min.js` + `hx-pending`（扩展）/ `hx-indicator`（内建） | 是 |
| DOM 差量更新（Idiomorph） | **内建** `hx-swap="innerMorph\|outerMorph\|outerSync"`（htmx 4 已内建 idiomorph 算法）；需要独立调用时用 `vendor/idiomorph.min.js` 的 `Idiomorph` 全局 | 否（可选） |
| 客户端 JSON 序列化 | **内建** `hx-encoding`（如 `hx-encoding="application/json"`） | 否 |
| 客户端表单校验 | **内建** `hx-validate="true"` | 否 |
| 响应目标与响应头拦截 | **内建** `hx-status:<码>` + `HX-Retarget`/`HX-Reswap`/`HX-Reselect`；多目标批量用 `hx-targets` 扩展 | 视场景 |
| Alpine.js 融合 | `dist/ext/hx-alpine-compat.min.js` | 是 |

**扩展加载方式（htmx 4 变更）**：不再需要 `hx-ext="name"` 属性，只需在页面中包含对应脚本文件；`config.extensions` 白名单可选。

## 5. 前端依赖治理

- 所有前端库由 `web/assets.lock.json` 登记（名称、版本、URL、路径、sha256、协议）。
- **在线更新**：`yd assets update`（可 `--check` 仅报告、`--source` 指定源）。
- **离线使用**：`yd assets verify --offline` 校验本地完整性；离线环境严禁触网，缺失即报错。
- 严禁从公网 CDN 动态引入；所有资产经 `go:embed` 打进二进制。
- 升级 htmx/Alpine 版本后必须重跑 `yd assets verify` 与模板测试（`go test ./web/`）。

## 6. 国密安全闭环

- **传输前置加密**：含敏感字段的表单声明 `data-yd-encrypt="sm2"`，前端在 `htmx:config:request` 阶段用服务端公钥加密标记字段，并附加 `X-YD-Encrypted: sm2` 头；后端据此用 SM2 私钥解密。
- **完整性校验**：静态资产与本地缓存凭证通过 `ydGuomi.verifyIntegrity(url, expectSm3Hex)` 做 SM3 摘要比对。
- **口令**：后端仅存 `guomi.HashPassword`（SM3 + 盐 + 多轮迭代）结果，比较用常量时间函数。

## 7. 配置与环境变量

- 环境变量前缀固定 **`YD_`**，下划线自动映射为多级嵌套：
  `YD_SERVER_PORT` → `server.port`，`YD_DB_MAXCONN` → `db.maxconn`。
- 优先级：命令行参数 > 环境变量（含 `.env`） > 配置文件 > 框架默认值。
- 生成项目的 `.env` 不得入库（`.gitignore` 已忽略），随仓提供 `.env.template`。

> 需求基线 `docs/需求设计说明书.md` 原文使用 `YUNGO_` 前缀，团队已统一改为 `YD_`；
> 基线文档保持只读，本约定为最终口径。

## 8. 提交纪律

- 提交信息使用**中文 Conventional Commits**：`<类型>(<范围>): <中文简述>`。
- 署名口径唯一，所有提交结尾固定为：

  ```
  Co-Authored-By: Eric Shi <shibingli@sina.com>
  ```

- 提交信息、作者、PR 描述、代码注释中**严禁出现任何 Claude / Anthropic / AI 助手相关内容**。

## 9. 注释规范（强制）

### 9.1 中英文同步（注释与日志文案）

所有注释（包文档、导出符号 doc 注释、行内注释）**以及所有日志文案** **逐行**采用中英文同步形式，
以半角竖线 `|` 分割，**英文在前、中文在后**，竖线两侧各留一个空格：

```go
l.Info("server started | 服务器已启动", logx.Str("addr", addr)) // 日志文案同样中英同步 | log messages follow the same rule
```

```go
// Compress negotiates the encoding by q-values. | Compress 依据 q 值协商压缩编码。
func Compress(o CompressOptions) router.Middleware

var maxControlPayload = 125 // RFC 6455 §5.5 | RFC 6455 §5.5 规定
```

**豁免**（无需中英对照）：`//go:build` 等编译指令、`// Code generated ... DO NOT EDIT.` 标记、
注释内部的代码示例 / 目录树 / 接口签名块 / URL。

`yd build` 与测试守卫会同时检查注释与日志文案，未同步者视为未完成。

### 9.1.1 日志统一使用 logx

- 全仓**禁止** `import "log"`、禁止 `fmt.Print*` 当日志、禁止 zap/zerolog/logrus 等第三方日志库。
- 统一使用 `github.com/shibingli/yd/logx`，字段用 `logx.Str/Int/Dur/Err` 等强类型构造函数。
- **唯一例外**：`server` 包为 `http.Server.ErrorLog` 必须提供 `*log.Logger` 而写的适配器，
  其输出必须转发给 `logx`。
- 自检：`grep -rn '"log"' --include=*.go . | grep -v '^./server/'` 无命中；
  `grep -rn 'fmt.Print' --include=*.go .` 无命中。

### 9.2 OpenAPI 注释必须完整

所有对外 HTTP 处理器的 doc 注释必须给出完整 OpenAPI 3.0 注解块，缺项视为未完成：

| 注解 | 是否必填 | 说明 |
|---|---|---|
| `@Summary` | 必填 | 一句话摘要（中英同步） |
| `@Description` | 必填 | 详细说明，含 htmx 片段/整页语义差异 |
| `@Tags` | 必填 | 分组名 |
| `@Accept` / `@Produce` | 必填 | 例：`@Produce html,json` |
| `@Param` | 有参数必填 | `名称 位置 类型 是否必填 "描述"`，位置取 `path/query/header/body/formData` |
| `@Success` / `@Failure` | 必填 | 至少各一条，形如 `200 {object} model.User "描述"` |
| `@Security` | 鉴权端点必填 | 例：`@Security BearerAuth` |
| `@Router` | 必填 | `路径 [方法]`，路径参数用 `{name}` |

紧随 `@Param` / `@Success` / `@Failure` 之后的 `@Example` 行绑定到该声明（写进
`Parameter.Example`、`MediaType.Example`），支持内联 JSON 与普通字符串：

```go
// @Param id path string true "用户 ID | user id"
// @Example u_1
// @Success 200 {object} model.User "用户详情 | user detail"
// @Example {"id":"u_1","name":"admin"}
```

同一套方言同样适用于结构体/常量/变量级别（`openapi.ParseTypeAnnotations` 用 `go/parser` + `go/ast`
解析，`openapi.Builder.AddTypeAnnotations` 合并进组件 Schema），不写这些注解也能生成文档：

| 级别 | 注解 | 说明 |
|---|---|---|
| 结构体 | `@Name` / `@Description` / `@Example` | 写在 `type X struct` 的 doc 上，示例写进 `Schema.Example`；字段注解写在字段的 doc 或行尾注释上，支持 `@Description`、`@Example`、`@Format`、`@Enum`（空格分隔）、`@Minimum`、`@Maximum`、`@Pattern`、`@Nullable`、`@Deprecated`、`@ReadOnly` |
| 常量块 | `@Name` / `@Description` / `@Type` / `@Enum` | `@Enum value:说明, value2:说明`（逗号分隔）；不写 `@Enum` 时按字面量推断，`iota` 按序号推断 |
| 变量 | `@Name` / `@Schema` / `@Example` | 把示例写回 `@Schema` 指向的组件 Schema；该 Schema 已有 `@Example` 时以结构体自身的为准 |

```go
// @Name User
// @Description 系统用户账号 | A system user account
// @Example {"id":"u_1","name":"admin","status":"active"}
type User struct {
	// @Format uuid
	// @Example u_1
	ID string `json:"id"`
	// @Enum active disabled
	// @Description 账号状态 | Account status
	Status string `json:"status"`
	// @Minimum 0
	// @Maximum 150
	Age int `json:"age"`
}

// @Name UserStatus
// @Description 账号状态 | Account status
// @Type string
// @Enum active:启用 | enabled, disabled:禁用 | disabled
const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

// @Name UserLevel（不写 @Enum 时按 iota 序号推断为 0, 1, 2）
const (
	LevelGuest Level = iota
	LevelMember
	LevelAdmin
)

// @Name UserQuery
// @Description 用户查询条件 | User query
type UserQuery struct {
	// @Enum name id
	Field string `json:"field"`
}

// @Name DefaultQuery
// @Schema UserQuery
// @Example {"field":"id"}
var DefaultQuery = UserQuery{Field: "id"}
```

`yd build` 会调用 `openapi` 包解析上述注解并与路由树合并，缺失注解的端点会在构建报告中列出；
`openapi.Report` 另外报告 `AnnotatedTypes`、`UnannotatedTypes`（反射注册但无注解的类型）与 `EnumCount`。

## 10. 错误处理规范（强制）

### 10.1 声明式哨兵错误

所有错误在包级声明一次，文案中英同步：

```go
// ErrBadPattern 表示路由模式语法非法。 | ErrBadPattern reports an invalid route pattern.
var ErrBadPattern = errx.New("invalid pattern | 非法的路由模式")
```

### 10.2 默认禁止 fmt.Errorf

| 场景 | 允许的写法 | 禁止的写法 |
|---|---|---|
| 附加格式化细节 | `errx.Detail(ErrBadPattern, "%q must start with /", p)` | ~~`fmt.Errorf("%w: %q ...", ErrBadPattern, p)`~~ |
| 保留底层错误 | `errx.Wrap(err, ErrReadBody)` | ~~`fmt.Errorf("read body: %w", err)`~~ |
| 两者都要 | `errx.WrapDetail(err, ErrQuery, "table=%s", t)` | ~~`fmt.Errorf("%w: table=%s: %v", ...)`~~ |

判定一律 `errors.Is` / `errors.As`；禁止比较错误字符串。
唯一例外：`errx` 包自身内部实现。

### 10.3 errx 双语格式串的 printf 陷阱

`errx.Detail` / `errx.WrapDetail` 的格式串若把同一个动词中英各写一遍，`go vet` 的
printf 检查会因参数个数不匹配而报警。正确做法是使用**显式参数索引**：

```go
// 错误写法：vet 报 "format %d reads arg #3, but call has 2 args"
errx.Detail(ErrLen, "C3 length %d, want %d | C3 长度 %d，期望 %d", got, want, got, want)

// 正确写法：显式索引复用同一个参数
errx.Detail(ErrLen, "C3 length %[1]d, want %[2]d | C3 长度 %[1]d，期望 %[2]d", got, want)
```

若两侧语义不同（不是简单重复），应拆成两个哨兵或只在一侧带格式细节。

### 10.4 自检

```bash
grep -rn 'fmt\.Errorf' --include=*.go . | grep -v '^./errx/'   # 必须无命中
grep -rn 'errors\.New' --include=*.go . | grep -v '^./errx/'   # 允许，但优先用 errx.New 统一风格
```

## 11. 多租户与鉴权契约

| 规则 | 说明 |
|---|---|
| **租户硬边界** | 任何按 ID 读写实体的服务方法都必须带 `tenantID` 并校验；跨租户访问返回 404/403，**不得返回数据**，也不得泄露资源是否存在。 |
| **每用户 JWT 密钥** | 创建用户时生成该用户专属 `sec_key`；签发与验签**只允许**用它，**禁止任何全局兜底密钥**。令牌 `kid` 承载用户 ID，验签方据此取密钥，不得为取密钥而解析未验签载荷。 |
| **落库形态** | 口令只存 `guomi.HashPassword` 结果；`sec_key` 以 SM4-GCM 加密存储；二者都**不得**明文入库或写进日志。 |
| **失效语义** | 用户禁用/删除/轮换 `sec_key` 后，既有令牌必须立即失效。 |
| **权限判定** | `auth.RequirePermission("user:create")`；无权限返回 403，且不得因「先查存在性再查权限」泄露资源存在性。 |
| **前端传输加密** | 口令等敏感字段必须由前端 SM2 加密后提交（`data-yd-encrypt="sm2"` + `X-YD-Encrypted: sm2`），后端用 `guomi.DecryptRaw` 解密。 |

## 11.1 WSL 下的开发位置（重要）

`yd dev` 的文件监听依赖操作系统原生的文件系统事件（Linux 为 inotify）。**在 WSL 中，`/mnt/c`、`/mnt/d` 等
DrvFs/9p 挂载不会投递 inotify 事件**，因此把项目放在 `/mnt/*` 下会导致热重载静默失效
（表现为「改了文件但服务不重启」）。

- ✅ 正确：项目放在 Linux 原生文件系统，如 `~/projects/myapp`
- ❌ 错误：项目放在 `/mnt/d/...` 后在里面执行 `yd dev`
- 该限制与框架无关：任何基于 inotify 的工具（air、reflex、watchexec、jest --watch）在 `/mnt/*` 下同样失效。
- 因为这条差异，Linux 侧的完整测试必须在**原生文件系统**上运行；在 `/mnt/*` 上会看到
  `toolchain` 的监听用例失败，而这不是代码缺陷。

## 12. 内存对齐（betteralign）

提交前对改动过的包执行内存对齐检查，推荐直接应用：

```bash
betteralign -apply ./...     # 等价于 yd align --apply
```

- 字段顺序以「减少 GC 扫描的指针字节数与结构体体积」为准；重排后必须保持字段语义、
  JSON/GORM/模板标签随字段移动，且 `go test ./...` 仍然通过。
- `betteralign` 是**可选**外部工具：缺失不得导致构建失败；
  安装方式 `go install github.com/dkorunic/betteralign/cmd/betteralign@latest`
  或把二进制放到 `.yd/tools/`。
- **工具链版本**：betteralign 必须用**不低于本模块 `go` 指令版本**的 Go 构建，否则会报
  `package requires newer Go version` 或
  `export data version ... is greater than maximum supported version`；
  按上面的 `go install` 重装即可（它默认使用本机最新工具链）。
- `yd align --apply` **迭代到不动点**：默认最多 5 遍，`--max-passes N` 覆盖默认值；
  每遍后诊断数归零或不再下降即停止，摘要以 `passes | 遍数: N` 报告实际遍数，
  达到上限仍在下降时给出告警。单遍 `betteralign -apply` 不保证收敛。
- 工具版本过旧时 `yd align` 打印重装提示并返回非零（哨兵 `align.ErrToolTooOld`），
  不 panic、不阻塞构建。

## 13. 前端 i18n 与主题契约

- 所有面向用户文案必须带 `data-yd-i18n="key"`，并在 `web/i18n/{zh-CN,en}.yaml` 中提供中英两份；
  回退顺序为「当前语言 → 默认语言 → 键名本身」。
- 语言切换必须**局部刷新**（htmx 重取 `#yd-workspace`），不得整页跳转；
  语言与主题都持久化（localStorage + cookie），刷新后保持一致。
- 主题支持 `light` / `dark` / `system` 三态，切换即时生效且**不得闪烁**；
  颜色一律来自 `@theme` 语义变量，禁止硬编码色值。

## 14. 后端协同约束

- 路由模式支持**混合分段**：`/avatar/:id.jpg`、`/report/:id-xx.png` 合法，参数名不得重复。
- 控制器只做编排；业务逻辑放 `internal/service`，数据访问放 `internal/repository`。
- 一切外部输入必须限长并校验；错误统一经 `middleware.Recover` + `app.Render` 输出用户可读的 HTML 错误片段（`pages/error.html`）。
- 新增端点后必须重新生成 `openapi.json`（`yd build` 自动完成）并确认 `/docs` 可见。

## 15. 优雅退出契约（长连接）

后端退出时必须**主动终止长连接**，否则进程会一直挂到宽限期结束或被强杀。框架提供三处接入点：

- **WebSocket**：给 `ws.Options.Tracker` 传 `ws.NewConnSet()`（或任何满足 `ws.Tracker` 的实现）。
  退出流程调用 `CloseAll(ws.CloseGoingAway, "...")`：连接先发 **1001（going away）** 关闭帧、再关套接字；
  连接在 `Close` / `CloseNow` 以及任何终止性读错误处自行取消登记，追踪表不会泄漏条目。
  `ws.Tracker` 故意声明在 `ws` 包内，`ws` 不反向依赖 `lifecycle`。
- **SSE**：给 `sse.Options.Tracker` 传 `sse.NewWriterSet(broker)`。`WriterSet.Shutdown()` 会逐个
  停止心跳 → 写最终注释帧 `server shutting down | 服务器正在退出`（**必须 flush**）→ 关闭写入器，
  最后关闭 `Broker`（释放订阅协程）。用 `sse.Handler` 时它会监听 `Writer.Done()` 自动返回；
  自行编写的流式处理器也必须 `select` 该通道。
- **其它自增长连接**：用 `app.Lifecycle().Add(name, fn)`（或 `server.Lifecycle()`）登记关闭函数；
  注册表并发安全、`remove` 幂等、同名钩子**后注册者原位生效**，`Shutdown` 按**注册逆序**执行且幂等，
  超时的钩子记为 `lifecycle.ErrShutdownTimeout`，panic 被恢复并记为 `lifecycle.ErrHookPanic`。

**退出顺序**（`app.Run` / `app.Shutdown` 统一实现，不得调整）：

1. `lifecycle` 的 `Done()` 在关闭开始时**立即关闭**，钩子按逆序执行（长连接在此收尾）；
2. HTTP/3 关闭 → `http.Server.Shutdown` 排空（停止接入 + 等待进行中的请求）；
3. **强制关闭**排空后仍存活的 hijack 连接（`server` 包装 `http.Hijacker` 登记真实 `net.Conn`，
   `http.Server.Shutdown` 对 hijacked 连接一无所知，不补这一步对端会一直挂着）；
4. gRPC：`grpcx.Shutdown(ctx, srv)` 或 `app.WithGRPC(srv)`——先 `GracefulStop`，宽限期过后 `Stop()` 强制停止
  （`grpcx.ErrShutdownTimeout` / `app.ErrGRPCShutdown`）；
5. 逆序执行 `app.OnShutdown` 钩子与 `Set` 注册的 `io.Closer` 服务。

- **宽限期只有一个来源**：`server.Options.ShutdownTimeout`（缺省 `app.defaultShutdownTimeout`，10s）；
  超时步骤只记错误，绝不阻断后续步骤，最终由 `errors.Join` 聚合返回。
- 长连接处理器**不得只依赖对端断开**：必须同时 `select` 请求上下文与关闭信号（`sse.Writer.Done()` 等），
  否则排空会被拖到宽限期；关闭过程中新建立的连接一律被拒绝。
- 应用入口只允许 `app.Run(ctx)` / `app.Shutdown(ctx)`；自行 `http.ListenAndServe` 或缓存 hijack 裸连接绕过登记
  都会破坏上述顺序，视为缺陷。

## 16. 服务化部署契约（服务模式）

框架的 `service` 包（底层 `github.com/kardianos/service` v1.3.0）是**唯一的系统服务入口**：
Windows 走 SCM、Linux 走 systemd、macOS 走 launchd。禁止为单个平台手写注册表项 / unit 文件 / init 脚本。

生成项目的入口（`cmd/app/main.go`）有三种运行模式，**出口只有一个**——`app.Run` 的优雅退出（见 §15）：

| 模式 | 触发条件 | 行为 |
|---|---|---|
| 前台 | 有终端且未给 `-service` | `application.Run(ctx)`，SIGINT/SIGTERM 触发优雅退出 |
| 服务模式 | 被服务管理器拉起（无终端，`service.Interactive()` 为 false）或显式 `-service run` | `service.Run(opts, fn)` 托管 `application.Run` |
| 控制动作 | `-service install\|uninstall\|start\|stop\|restart\|status` | 一次服务管理器调用后返回，不装配端口、不进前台循环 |

- **停止只取消 context，绝不 `os.Exit`**：服务管理器发来 Stop 时，`service.Run` 取消传给 `application.Run`
  的 context 并**等待它返回**（缺省宽限 30s，超时返回 `service.ErrStopTimeout`）。在途请求与长连接由
  §15 的关停序列收尾；任何 `os.Exit` / `panic` / `log.Fatal` 都会硬切断请求，并让服务管理器把「停止」
  记成异常退出。
- **不要包一层 shell**：systemd 的 `ExecStart`、SCM 的 `binPath` 都必须直接指向二进制（`/opt/<app>/bin/<app> -service run`），
  写成 `sh -c "…"` 会让停止信号传不到进程，优雅退出退化成超时强杀。
- **配置文件进不了服务管理器**：`configs/config.yaml` 的 `service.*` 段只在下次 `yd service install`
  时写入；`arguments` 与 `working_directory` 里的路径必须绝对（Windows 忽略工作目录）。
- **密钥不进服务文件**：unit 文件与 SCM 配置对运维可读，主密钥与数据库口令一律经 `YD_*` 环境变量注入。
- **运维侧等价命令**：`yd service <动作> [--name N] [--binary PATH] [--dir DIR] [--user U] [--args "…"]`，
  与运行期 `-service <动作>` 落到同一个 `service.Control`；动作集为
  `install|uninstall|start|stop|restart|status`，`run` 只属于应用二进制。
- **测试不得真的装服务**：`service` 包经 `newControlHandle` 注入、CLI 经 `serviceControlFunc` / `serviceBuildFunc`
  打桩，覆盖动作映射、名字/描述来源、不支持平台与权限失败路径；测试进程内不得出现真实 SCM / `systemctl` / `launchctl` 调用。

## 17. 镜像打包契约（ko）

镜像有**两个且只有两个**入口：`yd docker`（多阶段 Dockerfile → `scratch`）与 `yd ko`
（`github.com/shibingli/yd/ko`，从 Go 源码直出镜像，**没有 Dockerfile 这一环**）。
两者都调用**外部 CLI**（docker / ko）完成构建，因此不引入任何 Go 构建期依赖。
**禁止**在脚本、CI 或文档里另行手写 `docker build` 参数或自拼 ko 命令行：标签、平台、基础镜像与仓库
都必须经这两条命令解析，否则打包行为会散成彼此不一致的副本。

**分工**：需要定制构建步骤（装系统包、拷额外文件）走 `yd docker` 与 `Dockerfile`；
标准的「单二进制 + 空镜像」交付走 `yd ko`，它不需要 Dockerfile，也不要求本机装 docker。

| 项 | 值 |
|---|---|
| 命令行 | `yd ko [--repo R] [--tags a,b] [--platforms linux/amd64,linux/arm64] [--base-image X] [--package ./cmd/app] [--local] [--push] [--binary PATH] [--dir DIR]` |
| 配置键 | `ko.binary` / `ko.repository` / `ko.tags` / `ko.platforms` / `ko.base_image` / `ko.packages` / `ko.local` / `ko.push`（`YD_KO_*` 自动映射） |
| 二进制解析 | `--binary` → `YD_KO_BIN` → `<项目>/.yd/tools/ko[.exe]` → `PATH` |
| 哨兵 | `ko.ErrBinaryNotFound` / `ko.ErrNoRepository` / `ko.ErrBuildFailed` / `ko.ErrInvalidOptions` |

- **ko 是可选外部工具**：找不到二进制时返回 `ko.ErrBinaryNotFound` 并给出安装提示
  （`go install github.com/google/ko@latest`），**绝不 panic**；缺失只影响 `yd ko` 自身，
  **不得**让 `yd build` / `yd dev` / `yd gen` / `go test` 或 CI 失败（与 `betteralign` 缺失同一口径）。
- **禁止把 ko 当 Go 依赖**：不得 `require github.com/google/ko`，也不得 import 它的包来「省一次外部进程」；
  `go.mod` 不得因打包能力新增任何依赖。
- **标志落在 ko 侧的真实形态**：仓库经 `KO_DOCKER_REPO` 传给 ko，多平台走 ko 的 `--platform`（**单数**，
  `--platforms` 是本命令的标志名），基础镜像走 `KO_DEFAULTBASEIMAGE`，标签走 `--tags`；
  `--local` 对应 ko 的 `-L/--local`（装进本机 docker 守护进程，不出网），`--push` 对应 ko 的 `--push`。
- **产物形态**：两条路径产出的都必须是 `CGO_ENABLED=0` 静态链接的二进制（交叉编译护栏见 AGENTS.md §2/§3），
  前端资产由 `go:embed` 嵌入，镜像内无任何运行时依赖。
- **离线/内网**：ko 只把源码编译成镜像层，需要网络的只有「拉基础镜像」与「推结果」两处——
  基础镜像先同步进内网 registry 再用 `--base-image`（或 `ko.base_image`）指向它；`--repo` 指向内网仓库并先登录
  （凭据留在 Docker 的凭据配置里，ko 复用）；完全不出网的本机验证用 `--local`。内网 registry 走标准 TLS 校验，
  请把内网 CA 装进系统信任库，不要指望关掉校验。
- **测试口径**：打包命令不得在测试里真的执行外部二进制——`docker` 与 `service` 都经注入点打桩，
  `ko` 沿用同一口径（覆盖二进制解析顺序、缺仓库、构建失败与非法参数路径）。
