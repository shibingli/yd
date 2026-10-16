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
| 实施计划 | `docs/superpowers/plans/` |

## 2. 技术栈核心版本

**后端**

- Go **1.27.2**（go.mod `go 1.27.2`），充分利用泛型优化、低延迟 GC、PGO 与标准库结构体增强
- 配置：viper v1.21（多格式 TOML/YAML/YAML v3/JSON + 动态监听）
- 文件系统：afero v1.15（OsFs / MemFs 无缝切换的 Mockable FS）
- 命令行：pflag v1.0.10（POSIX 风格，替代标准库 `flag`）
- JSON：**自适应分流** —— amd64 走 sonic（JIT + SIMD），非 x86_64 走 go-json；构建标签 `jsonx_std` 可强制标准库
- Base64：**自适应分流** —— amd64 走 base64x（SIMD 硬件加速），非 x86_64 降级 `encoding/base64`；标签 `b64x_std`
- 数据：GORM v1.31（默认 `PrepareStmt: true`）、go-redis v9（单机/哨兵/集群）、asynq v0.26（Redis 驱动分布式队列）
- 网络：HTTP/1.1 + H2C + HTTP/3(QUIC, quic-go) + 内置 ACME 自动签发续期
- 安全：国密 SM2/SM3/SM4（**纯 Go**，gmsm），严禁 cgo 与非 Go 原生组件
- 日志：自研零拷贝日志（`sync.Pool` 字节缓冲直写 `io.Writer`，热路径零反射）

**前端**

- Htmx **4.0.0** 作为事件总线；Tailwind CSS **v4** 作为样式标准（`@theme` 语义化变量，默认蓝色系）
- 不依赖 Node.js 编译管道：Tailwind 编译由 Go 脚手架工具静默驱动（standalone 二进制）
- 资产一律 `go:embed` 打包进二进制，**严禁**运行时从公网 CDN 动态引入
- Alpine.js 3.17 + htmx 内建 morph（Idiomorph 算法）实现精细化 DOM 差量更新
- 前端国密沙箱：纯 JS 的 SM2/SM3/SM4，用于表单传输前置加密与静态资源完整性校验

## 3. 分层与优先级（不可违反）

配置解析优先级由高到低：**命令行参数 > 环境变量（含 `.env`）> 配置文件 > 框架默认值**。
环境变量前缀固定 **`YD_`**，下划线自动转多级嵌套（`YD_DB_MAXCONN` → `db.maxconn`，`YD_SERVER_PORT` → `server.port`）。

> 注：`docs/需求设计说明书.md` 原文写的是 `YUNGO_` 前缀，团队已决定统一改为 `YD_`；
> 需求基线文档保持只读不改，本变更以本文件、`AGENTS.md` 与实施计划为准。

## 4. 代码风格指引

1. **包结构**：一个文件一个职责；文件超过约 600 行或承担两类职责即拆分。
2. **错误处理（强制）**：
   - **所有错误必须在包级用 `errors` 声明为哨兵**：`var ErrBadPattern = errx.New("invalid pattern | 非法的路由模式")`
     （`errx` 即 `github.com/shibingli/yd/errx`，是对 `errors` 的声明式薄封装）；
   - **默认禁止 `fmt.Errorf`**。需要附加格式化细节时用 `errx.Detail(ErrX, "%q ...", v)`；
     需要保留底层错误时用 `errx.Wrap(err, ErrX)` 或 `errx.WrapDetail(err, ErrX, fmt, args...)`；
   - 错误文案必须**中英同步**：`English | 中文`（与注释、日志文案同一规则）；
   - 双语格式串若两侧都要动词，**必须使用显式参数索引**（`%[1]d`），否则 `go vet`
     会因参数个数不匹配报警；语义不同则拆哨兵，不要硬凑一行；
   - 调用方一律用 `errors.Is` / `errors.As` 判定，禁止字符串匹配判断错误类型；
   - 禁止 `panic` 跨包传播（仅允许在 `MustXxx` 初始化辅助中出现）；
   - 唯一例外：`errx` 包自身内部实现需要 `fmt.Sprintf` 拼接文案。
   - 自检：`grep -rn 'fmt\.Errorf' --include=*.go . | grep -v '^./errx/'` 必须无命中。
3. **文档注释（强制）**：所有导出符号必须有以符号名开头的 Go doc 注释。
3.1 **中英文同步（注释与日志文案）**：所有注释（包文档、导出符号 doc、行内注释）
   **以及所有日志文案**，必须**中英文同步**，以半角竖线 `|` 分割，
   **英文在前、中文在后**，两侧各留一个空格：

   ```go
   // Marshal encodes v and returns the JSON bytes. | Marshal 把 v 编码为 JSON 字节并返回。
   func Marshal(v any) ([]byte, error)
   ```

   - 多行注释**逐行**遵循同一格式，便于左右对照阅读。
   - 日志文案示例：`l.Info("server started | 服务器已启动", logx.Str("addr", addr))`。
   - 豁免：`//go:build` 等编译指令、`// Code generated ... DO NOT EDIT.` 生成标记、
     注释内的代码示例/树状图/接口签名块、URL 与纯符号行。
3.1.1 **日志一律使用自研 `logx`（强制）**：
   - 全仓禁止 `import "log"`（标准库）、禁止 `fmt.Print*` 作为日志输出、
     禁止引入 zap/zerolog/logrus 等第三方日志库；
   - 统一使用 `github.com/shibingli/yd/logx`（`logx.New` / `Default()` 及其 `Debug/Info/Warn/Error/Fatal`）；
   - **唯一例外**：`server` 包为满足 `http.Server.ErrorLog`（要求 `*log.Logger`）而写的
     适配器，其输出必须转发进 `logx`；
   - 自检：`grep -rn '"log"' --include=*.go . | grep -v '^./server/'` 必须无命中。
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
4. **热路径禁止反射**：HTTP 请求处理、序列化、日志、JWT 校验路径不得使用 `reflect`；反射仅允许出现在启动期装配、代码生成与 OpenAPI Schema 构建中。
4.1 **JSON 与 Base64 必须走框架封装层**（强制）：
   - 任何 JSON 编解码一律使用 `github.com/shibingli/yd/jsonx`（`Marshal`/`Unmarshal`/`NewEncoder`/`NewDecoder`/`Valid`），
     **禁止**直接 `import "encoding/json"`；
   - 任何 Base64 编解码一律使用 `github.com/shibingli/yd/b64x`（`StdEncoding`/`URLEncoding`/`RawStdEncoding`/`RawURLEncoding`
     及其 `Encode`/`Decode`/`AppendEncode`/`AppendDecode`），**禁止**直接 `import "encoding/base64"`；
   - 唯一例外：第三方库（如 gmsm）自身实现内部的编解码，以及 `logx` 为满足零拷贝而手写的
     JSON 字节拼装（它不得引入 `encoding/json`）。
   - 自检命令：`grep -rn '"encoding/json"\|"encoding/base64"' --include=*.go .` 必须无命中
     （`internal/repotest` 的守卫测试会自动检查）。
5. **零拷贝优先**：优先 `AppendXxx` 风格 API 与调用方提供的缓冲区；避免不必要的 `string([]byte)` 往返转换。
6. **并发**：共享状态必须显式加锁或使用 `sync.Pool`/原子操作；每个 `go` 关键路径必须能回答"它何时退出"。
7. **测试**：TDD；表驱动；文件名与被测文件同名加 `_test.go`；涉及并发必须 `-race` 通过。
8. **依赖克制**：新增第三方依赖需在 PR 说明中给出理由与体积影响；能用标准库解决的不引依赖。
9. **格式**：`gofmt`/`go vet ./...` 必须零告警；导入分组为标准库 / 第三方 / 本项目。
10. **提交信息**：见下方「提交纪律」，中文 Conventional Commits，署名口径唯一。

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

## 7. 常用命令

```bash
yd new <project>     # 初始化项目（交互式选择组件）
yd dev               # 热重载开发（Go 编译重启 + Tailwind 监听 + 模板局部刷新）
yd gen module|crud   # 低代码生成（DB Schema 或 Go Struct → 路由/控制器/模板）
yd build             # 跨平台静态打包（Tailwind 剪裁压缩 + go:embed + 单二进制）
yd docker            # 基于 scratch 的多阶段镜像构建
yd assets update     # 在线更新前端依赖并锁定校验和
yd assets verify --offline   # 离线完整性校验
```
