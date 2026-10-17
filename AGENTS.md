# AGENTS.md — 自主 AI Agent 自动化规约

面向在 **yd** 仓库内自主执行「代码修复 / 功能迭代」的 AI Agent。本文件定义修改权限边界、测试自愈流程与安全合规检查。

## 1. 修改权限边界

**允许直接修改**

- 源码目录：`config/`、`afs/`、`jsonx/`、`b64x/`、`logx/`、`router/`、`render/`、`htmx/`、`sse/`、`ws/`、`jwt/`、`guomi/`、`middleware/`、`i18n/`、`openapi/`、`server/`、`grpcx/`、`lifecycle/`、`service/`、`tools/`、`data/**`、`app/`、`cli/`、`scaffold/`、`gen/`、`build/`、`docker/`、`ko/`、`assets/`、`toolchain/`、`web/**`
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
- [ ] **请求边界**：外部输入必须限长（WebSocket `MaxMessageSize`、请求体 `MaxBytesReader`），并设置读写超时。
- [ ] **JSON/Base64 统一走封装层**：全仓禁止 `import "encoding/json"` 与 `import "encoding/base64"`，
      一律使用 `github.com/shibingli/yd/jsonx` 与 `github.com/shibingli/yd/b64x`。
      自检：`grep -rn '"encoding/json"\|"encoding/base64"' --include=*.go .` 无命中。
- [ ] **中英文同步（注释 + 日志文案）**：所有注释与所有日志文案逐行采用
      `English | 中文`（竖线分割，英文在前、中文在后）。豁免：编译指令、生成标记、注释内代码示例/树状图/URL。
- [ ] **日志统一走 logx**：全仓禁止 `import "log"`（`server` 包的 `http.Server.ErrorLog`
      适配器除外，且其输出必须转发进 logx），禁止 `fmt.Print*` 当日志，禁止第三方日志库。
- [ ] **错误统一声明为哨兵**：包级 `var ErrX = errx.New("english | 中文")`；**禁止 `fmt.Errorf`**
      （改用 `errx.Detail` / `errx.Wrap` / `errx.WrapDetail`）；错误文案中英同步；
      判定只用 `errors.Is` / `errors.As`。
      自检：`grep -rn 'fmt\.Errorf' --include=*.go . | grep -v '^./errx/'` 无命中。
- [ ] **多租户隔离**：任何按 ID 读写实体的路径都必须校验 `TenantID`；跨租户访问返回 403/404
      且不泄露资源存在性。
- [ ] **每用户 JWT 密钥**：令牌只允许用该用户的 `sec_key` 签发与验签，**禁止全局兜底密钥**；
      `sec_key` 必须以 SM4 加密存储，口令只存 `guomi.HashPassword` 结果。
- [ ] **令牌失效**：用户禁用/删除/轮换 `sec_key` 后旧令牌必须立即失效。
- [ ] **内存对齐**：提交前运行 `betteralign -apply ./...`（或 `yd align --apply`，后者迭代到
      不动点并报告 `passes | 遍数`），重排后 `go test ./...` 必须仍然通过；工具缺失不得导致
      构建失败。安装：`go install github.com/dkorunic/betteralign/cmd/betteralign@latest`；
      该工具必须用不低于本模块 `go` 指令版本的 Go 构建，否则报
      `package requires newer Go version` / `export data version ... greater than maximum supported version`。
- [ ] **前端 i18n 与主题**：新增文案必须有 `data-yd-i18n` 且中英目录齐全；
      语言切换走 htmx 局部刷新，主题支持 light/dark/system 三态并持久化。
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
- [ ] **鉴权数据落库**：租户/用户/角色/权限一律经 `auth/gormstore` 存入真实数据库，内存实现只作
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
      只在租户表为空时初始化（唯一索引冲突视为「已初始化」而非错误）；
      非 development 环境未配置 `seed.admin_password` / `YD_SEED_ADMIN_PASSWORD` 时**拒绝初始化**，
      **绝不**用默认口令在生产开管理员；口令永不进日志。
- [ ] **Shell 补全**：`yd completion` 的四种脚本**从命令注册表生成**（禁止手写）；
      **默认绝不改 rc 文件**，`--append-rc` 才追加且必须幂等 + 先备份；PATH 片段默认注释掉；
      PowerShell 脚本文件名必须是 `.ps1`（点源只加载 `.ps1`，用别的后缀会静默失效）；
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
- [ ] **可选外部工具**：`betteralign` / `ko` / `tailwindcss` / `docker` 一律经 `yd tools` 探测与安装
      （`yd tools check` 只报告；`yd tools install` 把可 `go install` 的项装进项目内 `.yd/tools/`，
      `GOBIN` 指向它的绝对路径），**禁止**手写 `go install` 装到全局，**禁止**改动 `PATH` 或往 `~/go/bin` 写东西；
      四个工具一律可选，缺失只影响对应能力，**不得**让 `yd build` / `yd dev` / `yd gen` / `go test` 或 CI 失败
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
