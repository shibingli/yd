# 验收记录（verification）

本文件记录 **yd** 框架与脚手架在 Windows / Go 1.27.2 下的真实验收结果：
每条都是实际执行过的命令与其真实输出，未做修饰。

> 复现环境：Go 1.27.2 windows/amd64，`CGO_ENABLED=0`（`-race` 用
> `CGO_ENABLED=1 CC=clang`，本机无 gcc）。所有命令均在仓库根目录执行。

## 1. 质量门禁

| 检查 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | 通过（32 个包 + 根包 `yd`） |
| 静态检查 | `go vet ./...` | 零输出 |
| 格式 | `gofmt -l .` | 零输出 |
| 单元测试 | `go test ./... -count=1` | **32 个包 ok，0 失败** |
| 竞态检测 | `CGO_ENABLED=1 CC=clang go test -race ./...` | **31 个包 ok，0 竞态** |
| 交叉编译 | `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 通过 |
| 交叉编译 | `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...` | 通过 |
| 规范守卫 | `go test ./internal/repotest/` | 通过 |

### 1.1 规范守卫覆盖的强制约束

`internal/repotest` 把这些约束固化为可执行测试，违反即构建失败：

- 禁止 `encoding/json` / `encoding/base64`（一律走 `jsonx` / `b64x`）
- 禁止标准库 `log` 与 `fmt.Print*` 作为日志（一律走 `logx`）
- 禁止 `fmt.Errorf`（一律用 `errx` 声明式哨兵）
- 注释与日志文案逐行中英同步（`English | 中文`）
- 提交署名口径与环境变量前缀 `YD_`

### 1.2 竞态检测发现的真实缺陷（已修复）

`go test -race ./ws/` 曾在 `TestConcurrentWritersAreSerialized` 中真实报告数据竞争：
客户端掩码缓冲 `maskBuffer` 在获取写锁**之前**被并发写协程共用，违反
「并发写被串行化」的契约。修复方式是把帧头、掩码键与掩码缓冲的构造整体移入
写锁临界区，复测 `-race` 与 `-count=2` 均通过。

这条记录的意义在于：多个模块的实现者曾判断「本机无法运行 `-race`」而跳过该验证，
实际用 `CC=clang` 可以运行——竞态检测不是可选项。

## 2. 端到端验收（`-tags e2e`）

```
$ go test -tags e2e ./e2e/ -count=1 -timeout 30m
--- PASS: TestScaffoldBuildRun (14.36s)
--- PASS: TestCrossCompile (9.16s)
    e2e_test.go:221: linux/amd64 → yd-app_linux_amd64 (37053637 字节)
    e2e_test.go:221: linux/arm64 → yd-app_linux_arm64 (31987092 字节)
    e2e_test.go:221: windows/amd64 → yd-app.exe_windows_amd64 (37726720 字节)
--- PASS: TestAssetsVerifyOffline (3.24s)
ok  github.com/shibingli/yd/e2e  26.829s
```

`TestScaffoldBuildRun` 覆盖：

1. `yd new demo --module example.com/demo --db sqlite --redis --i18n --htmx-ext sse,ws,preload --local-replace <repo> --yes`
2. 生成物齐全性（`go.mod`、`LICENSE`、`Dockerfile`、`.gitignore`、`.gitattributes`、
   `.yd.json`、`CLAUDE.md`、`AGENTS.md`、`configs/`、`cmd/app/main.go`、
   `docs/developer_guidelines.md`、`web/assets.lock.json` 等）与 `.gitignore` 规范行
3. 生成项目 `go build ./...` 通过
4. **整页与 htmx 片段语义**：普通请求返回含 `<html` 的整页；带 `HX-Request: true`
   返回不含 `<html` 的片段
5. **OpenAPI 3.0**：`/openapi.json` 含 `"3.0`；`/docs` 查看器可访问且**不含任何外部 CDN 引用**
6. **静态资产本地可用**：`/static/js/vendor/htmx.min.js` 与 `/static/css/yd.css` 均可取到

`TestCrossCompile` 校验三平台真实产物的文件头（linux 产物必须是 ELF）。

## 3. 各子系统验收证据（摘要）

| 子系统 | 关键证据 |
|---|---|
| 路由 | 单段混合捕获 `/:id.jpg`、`/:id-xx.png`、`/:y-:m-:d.html` 全部命中且确定；静态/参数/通配路径 `AllocsPerRun == 0`；匹配内核用记忆化回溯，病态输入快速终止 |
| 配置 | 「未显式传入的命令行参数不得覆盖环境变量」用变异测试反证（改成恒真后类型断言用例失败） |
| 编解码 | `jsonx` 三种后端（sonic / go-json / stdlib）符号集一致且输出与 `encoding/json` 逐字节对齐；`b64x` 与标准库 0..256 长度逐一对拍 |
| 日志 | 稳态 `0 allocs/op`；禁用级别 17ns/op |
| WebSocket | RFC 6455 官方向量与掩码示例；1002/1007/1009 关闭码；与 `coder/websocket` **双向对拍**（文本/二进制/100KB 大消息/分片/关闭握手/并发） |
| 国密 | SM3 的 GB/T 向量与 OpenSSL 独立复核；SM2 与 Go 端**固定 k 下密文逐字节相同**（前端 JS ↔ 后端互操作） |
| 服务端 | HTTP/1.1 + H2C 共用监听；**HTTP/3 真实 QUIC 往返**；优雅退出排空进行中请求；ACME 主机策略拒绝白名单外域名 |
| gRPC | 同端口真实 gRPC 调用与普通 HTTP 请求互不干扰 |
| 中间件 | 48 个用例；SSE/HEAD/204/304/升级请求一律不压缩；`gzip;q=0` 生效；100 并发压缩无竞态 |
| 数据层 | Redis 缓存 `singleflight` 100 并发下 loader 只执行一次且 `-race` 干净；asynq 加权队列与指数退避单调性 |
| 生成器 | 生成代码经 `go/format` 可解析、控制器只返回 HTML 片段、`routes_gen.go` 标记块幂等替换 |
| 脚手架 | 45 个文件真实生成 → 离线编译 → 启动 → 9 项 HTTP 断言（含 SSE 推送、WS 回显、国密登录链路、422 校验闭环） |
| 前端 | htmx 4.0.0 与 10 个扩展、Alpine、Idiomorph 全部内嵌并锁定 sha256；模板引用的 149 个 `.yd-*` 类全部有定义 |

## 3.1 Linux 侧门禁（WSL Ubuntu-24.04）

项目在 WSL 中位于 **Linux 原生文件系统**（`~/yd-wsl`，非 `/mnt/*`）时：

```
$ go vet ./...
VET_OK                    # 零输出
$ go test -race ./...
RACE_OK                   # 全部包通过，零竞态
```

环境：`go1.27.2 linux/amd64` + `gcc 13.3.0`（原生 `-race`，无需 clang 变通）。
构建期已实测：`go build ./...` 在 Linux 上通过。

## 3.2 多租户鉴权与内存对齐（第二轮需求）

| 能力 | 验收证据 |
|---|---|
| 多租户 + RBAC + 登录授权 | `auth` 包 40 个测试；`TestNoGlobalSigningKeyFallback` 逐一遍历「看起来合理的全局密钥」（主密钥、其 SM3 摘要、由主密钥派生的 SM4 密钥、租户 ID、用户名、口令、库中密文等）作为验签密钥，全部 `jwt.ErrSignatureInvalid`；正向对照仅该用户自己的 `sec_key` 能验通 |
| 每用户 `sec_key` | 签发路径唯一（`TokenIssuer.Issue` 首句即取该用户密钥）；`KeyFunc` 仅凭 `kid` 取密钥、不解析未验签载荷；已验签的 `sub`/`tid` 必须与头部一致，堵住「用自己的密钥签发冒充他人」 |
| 跨租户隔离 | `auth`：`TestCrossTenantAccessIsDenied`；生成项目：`TestGeneratedAppSupportsAuth/跨租户提权被拒绝且不泄漏数据`（403 且响应中**完全没有**用户数据，反向对照同租户 200 有数据） |
| 落库形态 | `TestPasswordsAndSecKeysAreNeverStoredInPlaintext`：库中无明文口令，`EncryptedSecKey` 不等于明文密钥 |
| 前端 i18n | `web` 包：218 个 `tr` 键在 `zh-CN` 与 `en` 两份目录中双向零缺失（各 258 条）；`TestLanguageSwitchReloadsWorkspaceViaHtmx` 断言语言切换走 htmx 局部重取而非整页跳转 |
| 前端主题 | `TestThemeSwitchHasThreeStatesAndNoFlash`：`light/dark/system` 三态 + `<head>` 内先行应用（消除首屏闪烁） |
| OpenAPI 注解 | 结构体/常量/变量三级注解与示例，实测 JSON 中可见；iota 常量块被推断为 `enum: [0,1,2]` |
| betteralign | 105 个结构体、57 个文件完成字段重排，结构体体积 −104 字节、指针字节 −2040 字节；经 token 级校验确认为纯行序变化；唯一跳过项 `htmx.Location`（字段序即 `HX-Location` 的 JSON 键序且有精确断言）；`yd align --apply` 迭代到不动点并报告遍数 |

## 3.3 端到端（第三轮：含鉴权）

```
$ go test -tags e2e ./e2e/ -count=1 -timeout 30m
--- PASS: TestScaffoldBuildRun (14.71s)     # 生成 66 个文件 → 编译 → 启动 → HTTP 断言
--- PASS: TestCrossCompile (9.02s)          # linux/amd64、linux/arm64、windows/amd64 真实产物
--- PASS: TestAssetsVerifyOffline (3.33s)
ok  github.com/shibingli/yd/e2e  27.117s
```

未登录的安全姿态已被断言：`GET /` 未带令牌时服务端返回 3xx 且 `Location` 指向 `/login`，
响应中不出现登出入口；受保护端点的 htmx 请求在未登录时不得返回 200。

## 4. 已知限制与未覆盖项

1. **HTTP/3 需要 TLS**（QUIC 强制 TLS 1.3）。开发态在未配置证书时会生成一次性自签证书
   并输出 Warn；生产必须配置证书或启用 ACME。
2. **WebSocket 无法在 HTTP/2 与 HTTP/3 上工作**（协议无 `Hijack`）。这类连接会返回
   `ErrHijackUnsupported`；需要实时下行时请改用 SSE。
3. **`yd docker` 的真实镜像构建未在本机验收**：本仓库的 e2e 用注入的假 docker CLI 覆盖了
   参数拼装与错误路径，真实 `docker build` 需要守护进程与被构建项目的完整依赖。
4. **前端国密 JS 的性能未做基准**：仅验证了正确性与互操作性。
5. **`-race` 依赖 C 工具链**：Windows 上需 `CGO_ENABLED=1 CC=clang`；无 C 工具链的
   环境会跳过该验证（不应视为通过）。
6. **`cli/interact.go` 每问一次新建 `bufio.Reader`**：一次性交付多行的输入源只能回答第一个
   问题；真实终端逐行输入不受影响。
7. **WSL 下项目必须放在 Linux 原生文件系统**：`/mnt/c`、`/mnt/d` 等 DrvFs/9p 挂载不投递
   inotify 事件，`yd dev` 的热重载会静默失效。实测证据：同一份代码在 `/mnt/d` 上
   `TestDevReloadEndpointOnlyInDevMode` 失败（20s 未收到 reload 事件），
   复制到 WSL 原生 ext4（`~/yd-wsl`）后**全部监听用例通过**
   （`TestDebounceCoalescesRapidEvents`、`TestWatcherSeesNestedDirectories`、
   `TestWatcherLifecycle`、`TestDevReloadEndpointOnlyInDevMode` 均 PASS）。
   这是 WSL 挂载的文件系统事件限制，与框架无关。
