# 验收记录（verification）

本文件记录 **yd** 框架与脚手架在 Windows / Go 1.27.2 下的真实验收结果：
每条都是实际执行过的命令与其真实输出，未做修饰。

> 复现环境：Go 1.27.2 windows/amd64，`CGO_ENABLED=0`（`-race` 用
> `CGO_ENABLED=1 CC=clang`，本机无 gcc）。所有命令均在仓库根目录执行。

## 1. 质量门禁

| 检查 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | 通过（37 个包 + 根包 `yd`） |
| 静态检查 | `go vet ./...` | 零输出 |
| 格式 | `gofmt -l .` | 零输出 |
| 单元测试 | `go test ./... -count=1` | **37 个包 ok，0 失败** |
| 竞态检测 | `CGO_ENABLED=1 CC=clang go test -race ./...` | **37 个包 ok，0 竞态** |
| 交叉编译 | `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | 通过 |
| 交叉编译 | `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...` | 通过 |
| 模块一致性 | `go mod tidy -diff` | 零输出 |
| 规范守卫 | `go test ./repotest/` | 通过 |

### 1.1 规范守卫覆盖的强制约束

`repotest` 把这些约束固化为可执行测试，违反即构建失败：

- 禁止 `encoding/json` / `encoding/base64`（一律走 `jsonx` / `b64x`）
- 禁止标准库 `log` 与 `fmt.Print*` 作为日志（一律走 `logx`）
- 禁止 `fmt.Errorf`（一律用 `errx` 声明式哨兵）
- 注释与日志文案逐行中英同步（`English | 中文`）
- 提交署名口径与环境变量前缀 `YD_`

### 1.2 竞态检测发现的真实缺陷（已修复）

`go test -race ./http/ws/` 曾在 `TestConcurrentWritersAreSerialized` 中真实报告数据竞争：
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

## 3.4 优雅退出与系统服务化（第四轮需求）

| 能力 | 验收证据 |
|---|---|
| 长连接不再泄漏 | `ws`：`TestConnSetClosesLiveConnectionWith1001`——httptest 起真实服务端，用同步通道（非 sleep）确认连接已登记，`CloseAll(CloseGoingAway, …)` 后客户端读到关闭码 **1001**，处理器 3s 内返回且登记表归零。`sse`：`TestWriterSetShutdownTerminatesStream` 确认最终注释帧已 flush、其后无心跳帧；`TestWriterSetShutdownReleasesSubscribers` 确认 `Broker` 一并收口、订阅通道释放 |
| 退出耗时**上界确定** | `server/shutdown_test.go`：挂 **8 条永久阻塞的 hijack 连接**时 `Shutdown` 实测 **516.9µs**（宽限期 10s，断言上界 3s），并同时校验 8 条连接的读阻塞确实被打断——即连接真被强制关闭，而非「快速返回但不作为」 |
| 关闭顺序 | `app`：`TestShutdownClosesLongConnectionsBeforeHooks` 断言顺序严格等于 `[lifecycle:stream, drain:handler, grpc:graceful, hooks]`；`TestShutdownCollectsEveryStageError` 断言各阶段错误可逐一 `errors.Is` 命中，且某阶段耗尽宽限期后后续钩子仍执行 |
| 错误聚合 | 超时步骤**只记错误、不阻断后续**（`lifecycle.ErrShutdownTimeout`、`grpcx.ErrShutdownTimeout`） |
| `lifecycle` 契约 | 12 项测试：逆序、幂等（并发调用共享同一聚合错误）、`Done()` 立即关闭、钩子 panic 被恢复记为 `ErrHookPanic`、`Add` 幂等 remove、**并发 `Add`/`Shutdown` 在 `-race` 下干净** |
| 系统服务化 | `service` 包 23 项 + `cli` 的服务相关 22 项全部通过；**测试过程不产生真实的 SCM / `systemctl` / `launchctl` 调用**（经 `newControlHandle`、`serviceControlFunc`、`serviceBuildFunc` 注入点打桩） |
| 服务停止即优雅退出 | `service.Run` 的 Stop 路径只取消传给业务的 context 并等待其返回（缺省宽限 30s，超时 `ErrStopTimeout`），**无 `os.Exit`**；生成项目 `cmd/app/main.go` 的服务模式调用 `service.Run(opts, application.Run)`，与前台模式共用同一条关闭路径 |
| 脚手架同步 | 生成项目含 `-service <动作>` 入口、`configs/config.yaml` 的 `service:` 段、README「安装为系统服务」章节（含 Windows `sc` 与 systemd 单元示例）；`TestRealGenerateCompilesWithGoBuild` 确认 66 个文件生成后仍可 `go build` |

### 3.4.1 集成期发现并修复的两个真实缺陷

两条都不是靠「跑一遍测试」发现的，而是靠**门禁交叉验证**：

1. **`service.OnFailure` 是 Windows 专有符号**。上游 `kardianos/service` 把 `OnFailure` /
   `OnFailureRestart` 声明在 `service_windows.go`（文件名后缀即构建约束），直接引用只在 Windows
   编得过——`go build ./...` 在本机是绿的，`GOOS=linux GOARCH=arm64` 才暴露
   `undefined: service.OnFailure`。修法：按本包既有做法改用字面量常量
   （`onFailureKey = "OnFailure"` / `onFailureRestartValue = "restart"`），并在注释里写明原因。
   **护栏**：`AGENTS.md` 新增「交叉编译必须过」清单项；`e2e.TestCrossCompile` 会构建三平台的
   `./cmd/yd`，正是能捕获该类缺陷的门禁。
2. **hijack 包装器吞掉了 `io.ReaderFrom` 快速路径**。`server` 为登记 hijack 连接而包装
   `http.ResponseWriter`，但内嵌接口只提升接口自身的方法，`net/http` 响应写入器额外实现的
   `io.ReaderFrom`（sendfile / writev 快速路径）随之消失；由于该包装会装在**每一个** HTTP/1.1
   请求上，静态资源响应会整体退回逐块拷贝。修法：显式补回 `ReadFrom`，底层不具备该能力时用
   匿名结构体只暴露 `Write`，避免 `io.Copy` 递归回自身。
   **变异验证**：把 `ReadFrom` 改名后，`TestReadFromKeepsUnderlyingFastPath` 与
   `TestWrapperOverRealConnectionKeepsFastPath`（后者在**真实 HTTP/1.1 连接**上断言）
   双双以预期文案转红，还原后恢复绿——护栏真实有效。
3. **HTTP/3 的 UDP 绑定失败被完全吞掉**（既有缺陷，非本轮引入）。`TestAltSvcHeaderAndHTTP3RoundTrip`
   间歇失败（基线提交上 `-count=5` 挂 5 轮），症状是客户端以 `timeout: no recent network activity`
   超时。定位过程：先用**裸 `http3.Server` + 显式 `net.ListenUDP` + `Serve(conn)`** 做对照，
   回环往返 **4.87ms 成功**，排除环境（防火墙 / 本机 QUIC 不可用）；再把 yd 的构造方式
   （`Addr` 复用 TCP 端口 + goroutine 里 `ListenAndServe`）原样复刻，拿到决定性证据：

   ```
   listen udp 127.0.0.1:56159: bind: An attempt was made to access a socket in a way forbidden by its access permissions.
   ```

   两个缺陷叠加：① UDP 复用 TCP 的端口号，而 Windows 上该号码落在**保留区间**时绑定会被直接拒绝
   （TCP 端口是随机临时端口，因此时好时坏）；② 绑定发生在 goroutine 里，**失败无人上报**——
   `Start()` 返回 nil、`HTTP3Addr()` 报出看似健康的地址、`Alt-Svc` 通告一个无人监听的端点，
   唯一的症状就是客户端静默超时。
   修法：`bindHTTP3` 在 `Start()` 返回前**同步**绑定，端口号取自真正绑定的套接字；自动模式下
   复用失败则回退到系统分配的 UDP 端口并让 Alt-Svc 通告真实端口，显式配置的端口则严格报错
   （`ErrHTTP3Listen`）；套接字由服务器自行关闭（`ErrHTTP3Close`），因为 `http3.Server.Close`
   只关它自己创建的监听器。
   **验证**：新增 5 项回归测试（Start 返回时端口已被占用 / 关闭后端口释放 / 自动模式回退且
   Alt-Svc 通告真实端口 / 显式端口失败可从 `Start` 用 `errors.Is` 命中 / 真实 QUIC 往返连跑 8 轮），
   并在 `-race` 下 `-count=10` 全绿、整个 `server` 包 `-count=5` 全绿（修复前为 `-count=5` 挂 3 轮）。
   **变异验证**：移除回退分支后 `TestHTTP3FallsBackToOSAssignedPort` 以 `ErrHTTP3Listen` 转红。

## 3.5 无 Dockerfile 打包（ko，第五轮需求）

| 能力 | 验收证据 |
|---|---|
| 无 Dockerfile 出镜像 | `ko` 包 41 个用例 + `cli` 的 11 个 ko 用例全部通过（`-race`）；`yd ko` 实际生成的命令行为 `ko build --base-import-paths --push=<bool> [--local] [--tags …] [--platform …] <packages>` |
| ko 仍是外部工具 | **`go.mod` / `go.sum` 零改动**（`grep -c "google/ko" go.mod go.sum` = 0/0）；守卫 `TestKoStaysAnExternalTool` 扫描全仓 import，引入 ko 库即失败（已变异验证） |
| 不意外推送 | ko 自身的 `--push` **默认为 true**，因此 `yd ko` **始终显式传** `--push=true|false`。独立实测（假 ko 回显 argv）：默认 → `--push=false`、传 `--push` → `--push=true`、`--local` → `--push=false --local` |
| 仓库与基础镜像 | ko 没有 `--repository`/`--base` 标志，二者只经环境变量传递；实测子进程收到 `KO_DOCKER_REPO=registry.example.com/team/app`、`KO_DEFAULTBASEIMAGE=cgr.dev/chainguard/static:latest` |
| 缺失时不阻塞 | `yd ko --binary <不存在>` → 双语安装提示（`go install github.com/google/ko@latest` + `.yd/tools`）+ 哨兵 `ErrBinaryNotFound`，退出码 1、**无 panic**；`yd build` / `yd dev` / `go test` 一概不受影响 |
| 无仓库时拒绝构建 | 未配仓库且未给 `--local` → `ErrNoRepository`，提示给出 `--repo` / `ko.repository` / `KO_DOCKER_REPO` / `--local` 四条出路 |

**据官方文档与源码核对后纠正的四处想当然**（这些若照最初的契约实现都会出错）：

1. ko 侧标志是**单数 `--platform`**，不存在 `--platforms`；且逗号拼接形式**已被 ko 废弃**，故逐值重复传入。
2. 仓库**没有** `--repository` 标志，只能走 `KO_DOCKER_REPO`（`ko.local` 等价于 `--local --preserve-import-paths`）。
3. 基础镜像**没有**对应标志，走 `KO_DEFAULTBASEIMAGE`（缺省 `cgr.dev/chainguard/static`），该环境变量覆盖 `.ko.yaml`。
4. `--push` **默认 true**——照搬会变成「不传参数就推到远端仓库」，属危险默认值。

来源：<https://ko.build/reference/ko_build/>、<https://ko.build/configuration/>、
<https://ko.build/features/multi-platform/>，以及 ko-build/ko 源码 `pkg/commands/options/{publish,build,validate}.go`、
`pkg/commands/resolver.go`、`pkg/build/options.go`。

## 3.6 双语注释语序归一（第五轮附带）

CLAUDE.md §3.1 要求注释与日志文案「英文在前、中文在后」，但此前**没有任何守卫检查语序**，
欠账累积到 **641 行 / 71 个文件**（出厂代码 160、测试 102、脚手架模板 368 …按修正前统计）。

- **出厂代码的日志与哨兵文案零违规**：293 处「中文在前」全部落在测试断言消息里，而规则约束的是
  注释与日志文案，测试断言不在其列（既有守卫 `TestLogMessagesAreBilingual` 也是同样口径）。
- 归一化只处理**纯注释行**，且仅当按 ` | ` 恰切成两段、左段含中文而右段不含时才交换；
  取值列表（`# 数据库：mysql | postgres | sqlite`）与注释内的表格行天然不匹配，保持原样。
- 新增守卫 `TestBilingualCommentsAreEnglishFirst`：扫描非测试的 `.go` 与全部 `.tmpl`，
  已变异验证（写入一行「中文在前」的注释立即转红）。**测试文件整体豁免**——它们会内嵌 Go 源码作夹具，
  夹具里的注释必须逐字保留（`openapi` 的注解解析用例正是如此）。

**过程中修正的一处脚本缺陷**：首轮归一化把 OpenAPI 注解的**结构**改坏了，例如
`// @Description 账号状态 | Account status` 被交换成 `// Account status | @Description 账号状态`——
`@Description` 是注解语法而不是语言片段，必须留在行首。受影响的是
`openapi/typeannotation_test.go`（测试夹具，已整体还原并回归通过）与
`gen/templates/controller.go.tmpl`（14 行，按「注解名 + 英文 + 中文」重新生成）。
守卫的切分正则因此把注解名并入「前缀」，不再参与交换。

## 3.7 第二轮需求集（持久化 / 权限常量 / 种子 / 补全 / 建库 / 前缀 / panic）

### 3.7.1 门禁

```
gofmt -l .                                    零输出
go build ./... && go vet ./...                零输出
go mod tidy -diff                             零输出
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 build   通过
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 build 通过
CGO_ENABLED=1 CC=clang go test -race ./...    41 个包 ok，退出码 0（共 43 个包，2 个无测试文件）
go test -tags e2e ./e2e/                      ok 84.859s
go test ./repotest/                  ok
```

### 3.7.2 各能力与其证据

| 能力 | 关键证据 |
|---|---|
| 鉴权数据落库 | `auth/gormstore` 19 项测试；关连接池重开仍读得到（`TestRoundTripSurvivesReopen`）；仅 2 个 SQL 形态断言在每个用户/角色语句的 WHERE 里都带 `tenant_id`；跨租户 → `auth.ErrNotFound`（不是 `ErrForbidden`） |
| 表与索引 `yd_` 前缀 | 迁移后 `sqlite_master`：表 `yd_auth_tenants/users/roles/role_permissions`；索引 `yd_idx_auth_users_tenant_id` 等 9 条；守卫 `TestMigratePrefixesEveryObjectWithYD`；探针模型实测落到 `yd_app_probe_rows` + `yd_idx_app_probe_rows_*` |
| 索引超长保护 | 63 字节上限，超长取前 54 字节 + `_` + SHA-256(**完整名**) 前 8 位；「同头不同尾」两个长名断言不相等 |
| 权限常量 | 16 权限 × 4 种拼写（标识 / Go 常量 / 模板键 / JS 键）+ 4 角色；`yd authz check` 对含 `user:creat` 与裸 `"user:create"` 的目录**退出码 1**，对空目录与框架自身**退出码 0** |
| 默认初始化数据 | 首启行数 `租户=2 账号=2 角色=8 角色权限=68`（期望值由注册表推导）；**三次启动行数一致**；生产环境未配口令**拒绝初始化且四张表为空**；日志无口令明文 |
| 前端判定 | 从真实 HTTP 响应解析 `<script id="yd-authz">`：匿名时 `granted` 为空（失败关闭），owner 登录后与注册表 16 项完全一致 |
| Shell 补全 | 四种脚本从命令注册表生成；bash `bash -n` 通过并真 source 注册成功、PowerShell 解析器通过；zsh/csh 无解释器 → **打印原因跳过并汇总「执行 2、跳过 2」**；rc 文件未给 `--append-rc` 时 sha256 不变、给了则先备份且幂等（标记行计数 1） |
| 自动建库 | 18 个子用例按错误码判定（1049 / 3D000 触发，1045 / 42501 / 42P04 / 连接拒绝不触发）；标识符转义含注入用例；开关关闭时零建库语句；本机无 MySQL/PG 服务，建库链路为**注入验证**，已如实标注 |
| DSN 预置 | 三种驱动逐参数断言；12 条「显式优先」用例；真实 SQLite 往返（原始文本含 `+00:00`）；`gormx.Open` 确实走预置 |
| 工具安装 | 注册表四条，`betteralign`/`ko` 的提示**复用解析器常量**（同一常量非副本）；安装落 `<项目>/.yd/tools`（绝对 GOBIN）；`--yes` 期间注入执行器调用 0 次 |

### 3.7.3 本轮的三个真实缺陷

1. **`_time_format` 让 `_txlock` 静默失效**（详见 §2.6.4）。8 并发写：带该键 5/8 失败、去掉后 0/8；
   我把参数加回去即复现 `SQLITE_BUSY_SNAPSHOT(517)`，移除即恢复。而它自身是**空操作**——
   驱动唯一的取值就是默认格式。
2. **PowerShell 补全脚本装成 `.powershell` 永远不生效**。点源运算符只加载 `.ps1`：
   实测 `. tiny.txt` → 变量仍为空，`. tiny.ps1` → 变量被赋值。脚本生成、内容、语法全对，
   唯独永远不执行——这是单测抓不到的那类缺陷。
3. **`go mod tidy` 暴露两处直接依赖被标成 indirect**（自动建库要按驱动错误码判定），已归一。

### 3.7.4 双语规范与 panic 的收口

| 项 | 结果 |
|---|---|
| 双语覆盖守卫 | 规范细化后（行末/单行/运行时文案必须同行成对；多行注释允许换行但整块须双语），**647 处违规 → 0**，`TestBilingualCoverage` 常绿 |
| 守卫自身可靠性 | 改守卫时曾丢失捕获括号致其 panic，**panic 的表现是「看起来通过」**；由变异验证（还原后仍红）发现，现由 `TestStringLiteralReStaysUsable` 钉住，且字面量正则认转义 |
| panic | 自研非测试代码 **11 处 → 2 处**，剩下的两处是 `recover()` 块中的 `panic(rec)` 重新抛出（去掉等于吞掉 panic）；接线期错误改为 `Router.Validate()` / `cli.Execute()` 启动时返回；AST 守卫 `TestNoDirectPanic`，变异验证过 |

## 3.8 正则统一与依赖版本同步（第六轮需求）

### 3.8.1 正则统一到 core/regexpx（双引擎）

全仓（含测试）的正则改经 `core/regexpx`。包装层内部持有两个引擎，按模式自动选择：

| 模式 | 引擎 | 性质 |
|---|---|---|
| 标准库可编译（绝大多数） | `regexp`（RE2） | 线性时间、不回溯、热路径零分配、无需超时 |
| 需要环视/反向引用/命名分组 | `regexp2` v1.12（RE2 兼容模式） | 支持 .NET 构造；强制 `MatchTimeout = 100ms`，超时按未匹配（失败关闭） |

**发现并修复的真实缺陷**：最初的无条件 regexp2 实现让 `FindAll` 在 `-race` 下扫描 60 KB CSS 时耗时
**3.2 秒**（非 race 约 250 ms），100 ms 超时因此把结果**静默截断**为 27/153 个类名，
`TestEveryYdClassUsedInTemplatesIsDefined` 失败。根因是 regexp2 的 `FindNextMatch` 每次都把
剩余输入重新转成 `[]rune`，批量迭代是 O(n²)。同一风险会影响 `yd authz check` 对大源码文件的扫描。
修复方式是双引擎：RE2 兼容模式走标准库快路径（恢复线性时间与零分配），只有 .NET 构造走
regexp2 + 超时；调用方仍然只经包装层。修复后 race 下该测试约 0.25 秒通过。

**性能对照**（Windows 11 / i9-10980HK / Go 1.27.2 / 16 逻辑核；`-benchtime=200x -count=5` 中位数，
比较的是两引擎本身，不是最终包装层的快路径）：

| 用例 | 标准库 ns/op | regexp2 ns/op | allocs（标准库 / regexp2） |
|---|---:|---:|---:|
| `perm_ok`（`user:create`） | 238.5 | 289.5 | 0 / 1 |
| `perm_bad`（`User:Create`，快速拒绝） | 52.5 | 193.5 | 0 / 1 |
| `role_ok` | 273.0 | 340.0 | 0 / 1 |
| `assign_ok`（源码行扫描） | 792.0 | 1242 | 0 / 1 |
| `diag`（betteralign 输出解析） | 659.0 | 682.5 | 0 / 1 |
| `perm_adv`（4 KiB 无冒号输入） | ~89 µs | 211 µs | 3 / 12 |
| `assign_adv` | 24.4 µs | 15.4 µs | 2 / 10 |

结论：**regexp2 并非普遍更快**——小输入的成功匹配接近，快速拒绝与 4 KiB 对抗输入更慢，
每次调用多 1 次分配，批量迭代还是二次复杂度。它的价值在 .NET 构造与统一入口；最终实现只在
确实需要这些构造时才付出该代价，普通模式仍走标准库的线性引擎（上述标准库列即为实际路径）。

守卫：`TestRegexStaysBehindWrapper` 禁止包装层之外的任何 `import "regexp"` 与
`import "github.com/dlclark/regexp2"`；包装层内部允许同时导入两者。变异验证：往 `core/tmpprobe`
放入 `import "regexp"` 后守卫立即失败，删除后恢复通过。

### 3.8.2 依赖升级（go get -u all）

用户执行 `go get -u -v all; go mod tidy` 后，全量测试暴露一处**上游行为变化**：
`go-sql-driver/mysql` v1.8.1 → v1.10.1 调整了 `FormatDSN` 的参数顺序（Params 先于专有字段），
`TestPlanCreationReadsDSNLikeTheDriver/mysql-keeps-params` 因此失败。
处理：把期望改为当前驱动的规范输出，并在用例上注明上游版本与原因；不删除断言。

### 3.8.3 版本真源与 yd new 模板

| 内容 | 真源 | 消费方 |
|---|---|---|
| Go 版本 / 直接依赖 | `go.mod` | `tooling/versions` → `version_generated.go` |
| 前端资产版本 | `web/assets.lock.json` | 同上 + 生成项目的资产清单 |

- `yd versions sync` 生成 `tooling/scaffold/version_generated.go`（带
  `// Code generated by yd versions sync; DO NOT EDIT.`），`options.go` 不再手写这两个常量。
- `yd versions check` 离线比对生成文件与真源，漂移时非零退出；守卫
  `TestScaffoldVersionsMatchSources` 在测试期拦截。变异验证：把生成文件里的 `4.0.0` 改成 `4.0.1`
  后守卫立即失败，恢复后通过。
- `yd versions upgrade` 依次执行 `go get -u ./...`、`go mod tidy`、npm registry 最新版本解析
  （jsDelivr / unpkg 的 `@ver` 段改写）、下载重锁、重新生成；`--dry-run` 不执行 go、不下载、不写文件。
- 模板里不再有第二处版本钉点：Dockerfile 的 `golang:{{ .GoMajorMinor }}-alpine` 由 `GoVersion`
  计算（`TestGenerateDockerfileUsesScratchMultiStage` 断言），README / CLAUDE.md / meta.go 均用同一常量。
- 生成项目（无 `tooling/scaffold`）运行 `yd versions upgrade` 只升级 Go 模块与资产，
  `check`/`sync` 跳过生成文件，不会在用户项目里创建多余的 `tooling/` 目录树
  （`TestVersionsPlainProjectSkipsScaffold`）。
- Htmx 固定走 **v4 轨道**：npm 把 4.x 发布在 `next` 标签下、`latest` 仍是 2.x，
  因此解析器按「当前大版本 + 稳定版」选择，绝不降级（`TestResolveLatestNeverDowngrades`）；
  真实干跑确认 `htmx 4.0.0` 判定为已是最新，而不是被 2.0.11 覆盖。
- htmx 4 的 **18 个官方扩展**全部进入 `web/assets.lock.json`（共 21 个前端资产）并经
  `yd assets update` 下载、锁定；框架 UI 加载其中 15 个，`csp` 与两个 htmx 2 兼容层仅锁定不默认加载。
- 升级 CLI 测试用注入点 `versionsRunGo` / `versionsHTTPClient`，测试进程内不执行真实 go、不触网。

### 3.8.4 门禁

```
gofmt -l .                                    零输出
go vet ./...                                  零输出
go test ./...                                 全部包 ok
CGO_ENABLED=1 CC=clang go test -race ./...    42 个包 ok，退出码 0
go run ./cmd/yd versions check                generated | 生成文件: up to date | 已同步
```

## 3.9 HTTP 组件深度优化（第七轮）

本轮针对 i18n、render、authz、router、上传与下载做兼容性与热路径优化，全部改动都有
行为级测试与基准数据支撑（Windows / i9-10980HK，`go test -bench`，数值取 `-benchtime=300ms`）。

### 3.9.1 i18n：无锁读取与预编译消息

- 目录在 `New`/`Reload` 时扁平化为 `键 → 预编译消息`，读取经 `atomic.Pointer` 取不可变快照，
  翻译不再加锁、不再按点逐层下钻；`Reload` 失败保留旧快照，不会出现半更新目录。
- 无参数、无占位符的消息原样返回，零分配；`{{.Name}}` 在加载期编译为片段，
  printf 动词仅在消息确实含动词时才交给 `Sprintf`，普通百分号不再产生 `%!d(MISSING)` 噪声。
- printf 参数不匹配被识别后返回**原始消息**并计入 `FormatErrors`，不再把 Go 的
  `%!d(string=…)` 调试语法呈现给终端用户；未知键计入 `Misses`；两者均可经
  `SetProblemHandler` 采样上报。
- 基准（旧 → 新）：无参命中 89.5 ns/1 alloc → **60 ns/0 alloc**；单命名占位 183 ns/2 → **120 ns/0**；
  动词路径 324 ns/2 → **216 ns/1**；回退语言 263 ns/2 → **93 ns/0**；缺失 96 ns/1 → **63 ns/0**。

### 3.9.2 render：按绑定集缓存模板

- 之前每次页面渲染都要 `Clone()` 整棵解析树：约 **21 µs / 121 allocs**。现在按
  「译者身份（Bundle + 语言）+ 判定集合键 + 解析代」缓存**已绑定**的模板集：
  `with_translator` 约 **7.2 µs / 76 allocs**，缓存命中后不再克隆。
- 缓存上限 16 条，FIFO 淘汰；`load()` 递增代数并清空缓存，旧解析代的条目不会被复用；
  语言或判定集合无法给出稳定键时**退回逐次克隆**（宁可慢，不串用他人判定）。

### 3.9.3 authz：判定集合一次捕获

- `authz.Data` 一次性构建权限/角色集合与稳定 `DecisionKey`，模板里的 `can`/`hasRole`
  由「每次重新计算」改为 O(1) 映射查找；`DecisionKey` 逐项带长度前缀编码，
  角色名即使含分隔字节也不会与其它集合碰撞（角色名只要求非空），匿名主体的键为空串
  （即「全部拒绝」的身份）。

### 3.9.4 router：HTTP 语义补齐与匹配器零分配

- **HEAD 自动回退 GET**：未显式注册 HEAD 时复用 GET 处理器并重新捕获参数；
  `Allow` 头补充隐式 HEAD（存在 GET 时）与 OPTIONS（自动响应开启时）。
- **请求体统一限界**：每个请求只经 `http.MaxBytesReader(MaxBodySize)` 包装一次；
  `ParseMultipartForm` 的参数只是内存预算（8 MiB），不再被误当作请求体上限。
  超限统一映射为 `ErrBodyTooLarge` / `ErrUploadTooLarge`，不再向业务层泄漏 `*http.MaxBytesError`。
- 404/405 正文改为中英双语；`JSONStream` 为超大载荷提供增量序列化（见 §4 第 18 条）。
- 混合分段匹配器由堆上的 `*mixMatcher` 改为栈上值，仅记忆表走对象池：
  命中路径 **2 allocs/80 B → 0 allocs**，`/report/:id-xx.png` 单次匹配 357 → 约 **240 ns**。
- 路由基准：静态约 **145–157 ns/0 allocs**、参数约 **177–203 ns/0 allocs**、混合约
  **238–255 ns/0 allocs**（均含一次完整 `ServeHTTP`，多次运行取区间）。

### 3.9.5 上传与下载：流式落盘、零中间拷贝

- **上传**：`StreamUploads` 用 `multipart.Reader` 逐段读取，`CopyUpload`/`CopyUploads`
  以 **32 KiB 池化缓冲**直接写入目标文件，全程不在内存中缓冲整个文件；支持单文件上限
  （`maxFileSize`）与请求体总上限（`MaxBodySize`），超限删除半成品文件。
- **文件名安全**：`SafeUploadName` 收敛两种斜杠、移除控制字符与 Unicode 方向覆盖字符
  （防扩展名伪装）、裁剪 Windows 非法尾随点/空格、限制 200 字节并保留扩展名、
  给 Windows 设备名（CON/NUL/COM1…）加前缀；`O_EXCL` + 数字后缀保证同名不覆盖。
- **下载**：`Download`/`DownloadBytes`/`DownloadReader`/`DownloadFile`。带 `Range`/`If-*`
  的请求交给 `http.ServeContent`（RFC 7233 + RFC 7232）；其余走一次 seek 取长度、
  一次正文拷贝的快路径——`bytes.Reader` 一次写出，`*os.File` 配合支持 sendfile 的
  写入器零中间缓冲。4 KiB 下载约 **0.7 µs**（≈5.6–6 GB/s 写入 `ResponseRecorder`，5 次分配）。
- **头部正确性**：`Content-Disposition` 按 RFC 6266/5987 输出（ASCII 兜底 + `filename*`），
  构造前清除 CR/LF 防响应拆分；未知扩展名嗅探首 512 字节后倒回；自动派生 ETag
  （`hex(mtime)-hex(size)`，与 nginx 同形）；统一 `X-Content-Type-Options: nosniff`。

### 3.9.6 门禁

```
gofmt -l .                                    零输出
go build ./...                                通过
go vet ./...                                  零输出
go mod tidy -diff                             零输出
go test ./...                                 44 个包 ok，退出码 0
CGO_ENABLED=1 CC=clang go test -race ./...    44 个包 ok，退出码 0
```

## 3.10 SSE / WebSocket / Htmx 写出路径优化（第八轮）

本轮把上一轮的「HTTP 热路径零分配」推进到长连接与片段写出路径，基准同样取自
Windows / i9-10980HK、`-benchtime=300ms`。

### 3.10.1 WebSocket：帧头解析零分配

- 旧实现把固定 2 字节头读入局部数组、把掩码键直接读进返回结构体的字段；两者都经
  `io.Reader` 接口传出，逃逸分析只能判定上堆：每个帧头 **2 allocs/26 B**。
- 现在所有读取都经过调用方的 14 字节 `headerBuf`，掩码键再复制进结构体：
  小帧解析 **71.65 ns → 55.1 ns，2 allocs → 0 allocs**；64 KiB 帧约 **7.0 µs/0 allocs**
  （已到内存带宽上限）。
- 新增 `TestReadFrameHeaderDoesNotAllocate` 作为回归守卫；掩码、UTF-8 校验、帧编码的既有基准不变。

### 3.10.2 SSE：字符串直写与单遍拆行

- `WriteData` 改为把字符串直接追加进组装缓冲，去掉 `[]byte(s)` 转换：
  4 KiB 数据 **3440 → 2521 ns**（4096 B/1 alloc → **0 B/0 alloc**），
  1 KiB 多行数据 **1267 → 960 ns**（1152 B/1 alloc → **0 B/0 alloc**），
  32 B 数据约 **45 ns/0 alloc**。
- 注释与数据拆行改为单遍扫描 CR / LF / CRLF，不再经 `strings.ReplaceAll`；
  多行注释除组装缓冲外不产生分配。
- 组装缓冲的**保留容量上限为 64 KiB**：一次性超大帧不会让大缓冲在整条流的生命周期内驻留
  （`TestHugeFrameDoesNotRetainBuffer`）；`WriteEvent` 维持约 **303 ns/0 alloc**。
- 广播基准：单订阅者约 **105 ns**，64 订阅者约 **3.8 µs**（约 60 ns/订阅者），均 0 分配；
  发布保持非阻塞，慢消费者只丢帧计数。

### 3.10.3 Htmx：响应头组装

- 单事件且无负载时直接返回事件名，不再为「过滤空事件名」分配副本；
  多事件 JSON 的事件名走「纯 ASCII 零分配」快路径，其余输入仍交给 `jsonx.Marshal`，
  因此任何输入的线上字节与旧实现完全一致。
- 多事件：**1377 → 918 ns，16 → 9 allocs**；单事件约 **110 ns**，剩余分配来自可变参数切片
  与 `Header.Set` 的 `[]string`。
- `RenderPartials`（8 个片段）约 **1.3 µs/5 allocs**，`ErrorFragment` 约 **174 ns/3 allocs**，
  均作为后续回归基线。

### 3.10.4 门禁

```
gofmt -l .                                    零输出
go build ./...                                通过
go vet ./...                                  零输出
go mod tidy -diff                             零输出
go test ./...                                 44 个包 ok，退出码 0
CGO_ENABLED=1 CC=clang go test -race ./...    44 个包 ok，退出码 0
```

## 3.11 中间件热路径与代理信任（第九轮）

### 3.11.1 访问日志的客户端地址

- `AccessLog` 与 `AccessLogAsync` 的 `remote_ip` 由 `Ctx.RealIP()`（无条件信任 `X-Forwarded-For`
  最左值）改为 `Ctx.ClientIP(trusted)`，与 CLAUDE.md §21 的信任模型一致。
- 新增 `AccessLogOptions.TrustedProxies`：默认**不含任何可信代理**，代理头被完全忽略，
  日志记录对端地址；只有对端落在可信前缀内时才从右向左解析 XFF。伪造的 `X-Forwarded-For`
  无法再决定日志中的地址。测试覆盖「默认忽略 / 可信对端 / 不可信对端」三种情形。
- 该改动同时消除了无代理部署下每个请求的两次代理头查找。

### 3.11.2 包装器不再破坏零拷贝下载

- `countingWriter` 新增 `ReadFrom`：底层写入器实现 `io.ReaderFrom` 时直接委托
  （生产环境的 `*http.response` 因此仍走 sendfile），否则从 `bufpool` 取 32 KiB 缓冲回退拷贝；
  两条路径都正确累加字节数。
- 这意味着 `Ctx.Download` 的 `bytes.Reader` 单次写出与 `*os.File` 的 sendfile 快路径在
  访问日志中间件之后**仍然有效**，不会因为多了一层包装退化为 32 KiB 循环拷贝。
- 新增测试同时验证委托路径（底层 `ReaderFrom` 被调用）与回退路径的字节计数。

### 3.11.3 头部查找去掉重复规范化

- 新增内部助手 `headerValue`：对**已规范化**的字面量键直接查 `http.Header` 映射，
  跳过 `Header.Get` 每次调用都要执行的 `textproto.CanonicalMIMEHeaderKey` 逐字节扫描。
  `Set`/`Add` 总是存规范化形式，因此语义与 `Get` 完全一致。
- 应用于压缩中间件（Accept-Encoding / Content-Encoding / Content-Type / Cache-Control /
  Content-Length）、协议升级判定（Upgrade / Connection）、CORS（Origin 与两个
  Access-Control-Request-*）以及 RequestID（自定义头名在构造期规范化一次）。
- 基准（5 次取中位，Windows / i9-10980HK）：小响应跳过压缩的包装开销由约 **+0.5 µs**
  降至约 **+0.33 µs**（基线约 0.70 µs）；已编码 16 KiB 直通约 **7.9 µs**（无中间件基线约 7.2 µs）；
  同步访问日志约 **1.34–1.40 µs**，其中主要是 8 字段 JSON 编码本身的成本。

### 3.11.4 门禁

```
gofmt -l .                                    零输出
go build ./...                                通过
go vet ./...                                  零输出
go mod tidy -diff                             零输出
go test ./...                                 44 个包 ok，退出码 0
CGO_ENABLED=1 CC=clang go test -race ./...    44 个包 ok，退出码 0
```

## 3.12 日志编码热路径（第十轮）

本节优化 `core/logx` 的 JSON/console 编码。基准在 Windows / i9-10980HK 上以
`-benchtime=1s -count=3` 取中位；优化前后的代码分别来自工作区与 HEAD，测量方式完全一致。

### 3.12.1 时间戳与定宽数字

- `appendPad` 过去对每个日期/时间分量都调用 `strconv.AppendInt` 并复制一次局部数组。
  现在宽度 1–4 的定宽数字直接写入目标缓冲；时间戳使用 `"00".."99"` 查表
  （`appendTwoDigits`）与四位年份写入（`appendYear`），越界值仍回退通用实现。
- 每条记录的 7 个分量因此不再进入 strconv 的通用路径。

### 3.12.2 构造期预编码

- 进程号在 `New` 时格式化为字符串；`service` / `logger` / `host` 三个持久成员的
  完整 JSON 片段（含键与转义）在构造期生成，`Named` 派生时重新生成 `logger` 片段。
  记录写出时只做一次 `append`，不再逐条转义。
- 级别的 JSON 字面量改为预转义表，非法级别回退到原实现。

### 3.12.3 时长使用精确十进制

- `Dur` 字段不再经过 `float64` 与 `strconv.AppendFloat`（ftoa64），改为由整数纳秒直接
  生成「整数毫秒 + 截断的六位小数」；数值与旧路径完全一致，但极小值输出
  `0.000001` 而不是指数形式 `1e-06`。
- 新增 `TestAppendJSONMillisExact` 覆盖 0、亚微秒、1.5ms、负值、整数毫秒与
  `math.MinInt64`，既断言精确文本也断言解析后的数值；`TestAppendPadWidths` 覆盖
  快路径与三种回退；`TestNamedLoggerPrecomputesJSON` 覆盖带引号的 logger 名。

### 3.12.4 基准（优化前 → 优化后，均 0 allocs/op）

| 场景 | 优化前 ns/op | 优化后 ns/op | 变化 |
|---|---:|---:|---:|
| `InfoNoFields`（无字段） | 199.7 | **106.9** | −46% |
| `Info8Fields`（8 字段混合类型） | 623.4 | **456.3** | −27% |
| `InfoFields`（5 字段） | 387.6 | **275.5** | −29% |
| `ConsoleFields`（4 字段） | 273.7 | **233.1** | −15% |
| `SlowQuery` | 362.6 | **240.2** | −34% |
| `WriterDiscard` | 384.0 | **278.0** | −28% |

零分配性质保持不变；异步模式的收益被入队/写协程成本部分稀释。

### 3.12.5 门禁

```
gofmt -l .                                    零输出
go build ./...                                通过
go vet ./...                                  零输出
go mod tidy -diff                             零输出
go test ./...                                 44 个包 ok，退出码 0
CGO_ENABLED=1 CC=clang go test -race ./...    44 个包 ok，退出码 0
```

## 3.13 JWT 验签与 WebSocket 压缩基准（第十二轮）

### 3.13.1 JWT：补齐每请求验签基准

`security/jwt` 之前没有任何基准，本轮新增 `bench_test.go`，覆盖登录期签发、
每请求对称/非对称验签与并发验签。每轮都启用推荐的校验项（算法白名单、签发者、
受众、必须含 `exp`）。基准取 Windows / i9-10980HK、`-benchtime=1s -count=5` 中位：

| 场景 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `Sign/HS256`（含 6 项声明） | ≈3450 | 1585 | 16 |
| `Parse/HS256` | ≈2750 | 754 | 9 |
| `Parse/ES256`（P-256） | ≈75000 | 1617 | 25 |
| `Parse/HS256`（16 核并发） | ≈770 | 754 | 9 |

**路径审视结论**：HS256 的 CPU profile 中 SHA-256 压缩函数占 36% 平坦开销、HMAC
密钥初始化占 19% 累计，扫描器与 base64 合计约 20%；9 次分配分别是调用方保留的
payload、signature、`Token`、头部字符串，以及 crypto 内部的 HMAC/摘要状态。
scratch 池已让所有中间缓冲零分配，**没有可再安全削减的框架级拷贝**；ES256 的
≈75 µs 就是 ECDSA 验签本身的成本（一次 P-256 验证）。

诊断过程中发现一个**基准侧**的陷阱：`KeyFunc` 闭包直接返回 `[]byte` 会在每次调用时
重新装箱，凭空多出一次分配。已在 `KeyFunc` 文档中注明「把密钥存进 `any` 字段或返回
指针」，避免使用方在每请求路径上无意增加分配；生产代码本身无需改动。

### 3.13.2 WebSocket：permessage-deflate 吞吐基准与零分配读取

新增两组基准：`CompressMessage`（1/16/64 KiB，上下文接管/每消息重置）与
`CompressedRoundTrip`（4 KiB，客户端→服务端与反向，覆盖压缩、成帧、掩码、
解压全链路）。

压缩吞吐（1s × 3 中位，`SetBytes` 统计的是原始载荷）：

| 载荷 | 上下文接管 | 每消息重置 |
|---|---:|---:|
| 1 KiB | ≈2.1 µs / 497 MB/s | ≈4.0 µs / 256 MB/s |
| 16 KiB | ≈6.3 µs / 2.62 GB/s | ≈9.9 µs / 1.66 GB/s |
| 64 KiB | ≈21.9 µs / 3.00 GB/s | ≈22.1 µs / 2.96 GB/s |

`CompressedRoundTrip` 每 4 KiB 消息约 **18–20 µs**（≈210–228 MB/s），受压缩本身限制；
每轮堆分配从 **47 次降到 0**：

- `deflateInput.ReadByte` 过去每次调用都让一个 `[1]byte` 逃逸到堆（flate 解压会
  逐字节读取）——把暂存字节放进解码器状态后，单这一项每轮减少约 42 次分配。
- `NextReader` 改为复用连接自有的 `MessageReader` 与 `framePayloadReader`；
  文档明确「读取器只在下次 `NextReader`/`ReadMessage` 前有效」（连接本只有一条读流）。
- `writeFrameLocked` 使用连接自有的 `writeHeaderBuf`，并用 `writeBase` + `writeList`
  两个视图复用同一份 `net.Buffers`：`WriteTo` 消费副本，基视图留给下一帧。
- 未压缩的 32 B 往返由 6 次分配降到 **0**（`BenchmarkMessageRoundTrip`，≈3.7 µs）。

吞吐随分配消失而进一步提升（4 KiB 往返 23→18.6 µs），高并发长连接的 GC 压力归零。

### 3.13.3 门禁

```
gofmt -l .                                    零输出
go build ./...                                通过
go vet ./...                                  零输出
go mod tidy -diff                             零输出
go test ./...                                 44 个包 ok，退出码 0
CGO_ENABLED=1 CC=clang go test -race ./...    44 个包 ok，退出码 0
```

## 3.14 日志并发吞吐（第十三轮）

本轮针对 Go 1.27.2 与 Linux 特性优化 `core/logx` 的多协程吞吐。基准为 Windows /
i9-10980HK、`-benchtime=1s -count=5` 中位、5 个混合字段；同一方法可用于 Linux。
在 Linux 上，Go 1.27 的 `GOMAXPROCS` 已感知 cgroup CPU 配额，异步环数量直接跟随它。

### 3.14.1 默认同步路径：缓存行隔离

- `core` 中的级别阈值（每次调用都读）与写互斥锁（每条记录都写）原本共享缓存行；
  持锁核心写入该行会让其它核心的每次级别读取都缓存未命中。用缓存行填充隔开后，
  16 核并发从约 **210 ns/op 降到约 168 ns/op**（约 −20%），仍为 **0 allocs/op**。
- 单协程成本不变（约 265 ns），说明改进来自消除跨核伪共享而非改变算法。
- 这些结构体带 `//betteralign:ignore`：内存对齐工具的默认优化会移除手工填充，
  反而撤销本节的收益。

### 3.14.2 可选的免锁并发写入

- 新增 `Options.ConcurrentWrites`：调用方声明 Writer 自身串行化并发 `Write` 时，
  记录编码后直接调用 `Writer.Write`，不再经过 Logger 的写锁。
- 16 核 5 字段：**47.3–47.5 ns/op、0 allocs/op**，约为默认同步路径的 1/3.5。
- **契约**（写入文档）：只有 `*os.File` 等自身串行化的 Writer 可启用；`bytes.Buffer`、
  `bufio.Writer` 与多数包装器不可。行完整性的保证转移给 Writer：Linux 上带 `O_APPEND`
  的 `*os.File` 保证每次写的字节连续追加，按偏移写入的普通文件不保证；Writer 还必须
  承受写入进行中的 `Sync` 调用。
- 新增 `TestConcurrentWritesKeepRecordsWhole`：8 协程 × 250 条，经互斥保护的 Writer
  写出后逐行解析，验证没有交错或截断。
- Git 风格的对照（同机、6 字段、`io.Discard`）：logx 免锁模式约 47 ns/op，zerolog
  约 89 ns/op，zap 约 384 ns/op（并带 1 次 385 B 分配）。

### 3.14.3 异步引擎：按 P 分片

- 环数量从固定 32 改为跟随 `GOMAXPROCS` 并夹紧到 **4–64**：多核主机每核一环，
  小容器不为一堆闲置环付出内存；Linux 上该值来自 cgroup 感知的 `GOMAXPROCS`。
- 缓冲池按环拆分：生产者按轮转选环并从该环的池取缓冲，写协程把缓冲还回同一个池，
  不再让全部生产者争抢一个 `sync.Pool` 根；在途计数同样按环分片，关闭流程汇总各环。
- 唤醒改为「原子闩锁 + 单槽通道」：写协程在排空前清除闩锁，生产者只在闩锁由空转忙时
  才做一次通道通知，通道锁不再是逐记录成本。
- 串行异步路径约 **730 → 680 ns/op**；`-cpu=1,2,4,8,16` 扩展实测
  **400 / 350 / 250 / 199 / 199 ns/op**。8 核之后停在同一水平，说明**单写协程**
  是异步路径的吞吐上限；需要更高吞吐且 Writer 并发安全时应选 `ConcurrentWrites`，
  需要解耦慢 Writer 时应选 `Async`。

### 3.14.4 门禁

```
gofmt -l .                                    零输出
go build ./...                                通过
go vet ./...                                  零输出
go mod tidy -diff                             零输出
go test ./...                                 44 个包 ok，退出码 0
CGO_ENABLED=1 CC=clang go test -race ./...    44 个包 ok，退出码 0
```

## 3.15 国密与编解码热路径（第十四轮）

本轮针对 Go 1.27.2 优化 `security/guomi` 与 `core/b64x`，并在 Windows 与
WSL Ubuntu-24.04（同为 Go 1.27.2）上分别测量；所有实现保持纯 Go、无 cgo。

### 3.15.1 SM3 / HMAC / PBKDF2

`gmsm` 的 `SM3.Sum(in)` 会把 `in` 的内容当作额外消息数据，因此无法按 `hash.Hash`
约定复用调用方的输出缓冲；传 `nil` 时它每次调用都要分配 32 字节。包装层现在把一个
**零长度、32 字节容量**的 scratch 交给它——gmsm 会把摘要追加进这段容量而不分配——
再把结果追加到调用方缓冲：

- HMAC-SM3：**20 → 17 allocs**，1112 → 1080 B/op。
- PBKDF2-HMAC-SM3（1000 次迭代）：**8012 → 6012 allocs**（−25%），
  384.5 → 320.6 KB/op；即每轮少 2 次分配，按默认 100000 次迭代计算，
  每个口令散列少约 **20 万次分配**。
- 一次性 `Sum` 的其余分配来自 gmsm 内部的 `Write`/`pad` 缓冲，属上游实现，
  框架层没有可安全削减的拷贝。

### 3.15.2 SM4

- CBC / ECB 加密现在**直接填充进输出缓冲并就地加密**（`cipher.Block` 与
  `cipher.BlockMode` 都允许 dst 与 src 完全重叠）：少一次分配、少一次整块拷贝。
  4 KiB CBC：10080 → **5216 B/op**，9 → **8 allocs**，耗时约 39.3 → 38.1 µs。
- 新增 **`NewGCM(key)`** 可复用 AEAD：一次性的 `SealGCM`/`OpenGCM` 按设计每条消息
  重建 SM4 密钥编排与 GHASH 表；复用后 256 B Seal 从 **11 allocs/640 B** 降到
  **6 allocs/368 B**，约 7.2 → 6.5 µs（≈10%）；4 KiB 同样从 11 → 6 allocs。
- `NewGCM` 返回的 `cipher.AEAD` 无状态，可由任意多个协程共享；一次性入口语义不变。
- **SM2** 的验签约 1.35 ms、1965 allocs，瓶颈在 gmsm 的 big.Int 运算，
  属上游实现成本，本轮不做替换。

### 3.15.3 Base64

原先 base64x 后端的 `Decode` 走「SIMD 解码 → 池化缓冲 → 拷贝」，中转本身让它在所有尺寸上都
慢于标准库。直接对拍（同尺寸、同一目标缓冲）显示 **SIMD 解码本身更快**：

| 尺寸 | SIMD 解码（直写） | 标准库解码 | SIMD 编码 | 标准库编码 |
|---|---:|---:|---:|---:|
| 16 B | **15.2 ns** | 24.5 ns | **13.3 ns** | 17.0 ns |
| 1 KiB | **498 ns** | 835 ns | **367 ns** | 781 ns |
| 64 KiB | **31 µs** | 53 µs | **23.7 µs** | 49 µs |

因此 amd64 的 b64x 后端改为：**Encode / Decode / AppendDecode 直接使用 SIMD 编解码器**
（无中转、无额外拷贝）；`DecodeString` 仍走 `encoding/base64`（实测其分配式字符串入口更快）。
含 CR/LF 的输入先尝试 SIMD，失败且确实含换行时回退标准库（标准库忽略换行），
因此合法且无换行的输入不会为扫描付出代价。

- `Decode` 16 B：64.3 → **17.1 ns**；1 KiB：1051 → **497 ns**；
  64 KiB：73.5 µs / 3 allocs / 74 KB → **30.2 µs / 0 allocs**。
- `DecodeString`（JWT 路径）16 B：60.4 → **39.5 ns**，24 → **16 B/alloc**。
- `Decode` 失败时可能留下部分输出（与 `encoding/base64` 一致）；`DecodeString` 与
  `AppendDecode` 仍保证不把部分结果交给调用方。包文档已同步。
- 降级后端（非 amd64 或 `b64x_std`）本来就是标准库，语义一致。

### 3.15.4 JSON 编解码对照

新增 `core/jsonx` 基准（与 `encoding/json` 对拍，测试文件允许把标准库当预言机）：
单个对象 Marshal 313 → **640 ns**（jsonx 快约 2×），64 个对象 7.2 → 30.8 µs；
反序列化 64 个对象 39.5 → 71.3 µs。自适应层已明显快于标准库，本轮无需改动。

### 3.15.5 Linux 对照

同一组基准在 WSL Ubuntu-24.04 / Go 1.27.2 上运行，相对排序与 Windows 一致：
SM3(64 B) 1027 ns / 6 allocs；PBKDF2(1000 次) 2.03 ms / 6012 allocs；
SM4-CBC(256 B) 3.0 µs / 8 allocs；复用 GCM(256 B) 6.04 µs / 6 allocs；
b64x 编码 1 KiB 2.49 GB/s；解码 16 B 27.9 ns、64 KiB 57.7 µs，均 0 allocs。
国密实现仍为纯 Go（gmsm），`crypto/rand` 在 Linux 上走 `getrandom`。

### 3.15.6 门禁

```
gofmt -l .                                    零输出
go build ./...                                通过
go vet ./...                                  零输出
go mod tidy -diff                             零输出
go test ./...                                 44 个包 ok，退出码 0
CGO_ENABLED=1 CC=clang go test -race ./...    44 个包 ok，退出码 0
```

## 3.16 编解码生态横评（jsonx / b64x 与其它实现）

横评工程位于 `.scratch/codecbench`（临时模块，不入主仓），对照库版本：sonic v1.15.4、
goccy/go-json v0.11.2、json-iterator v1.1.12、cristalhq/base64 v0.1.2、segmentio/asm v1.2.1。
输入、缓冲与统计方式完全一致；Windows 与 WSL Ubuntu-24.04 各跑一轮。

### 3.16.1 JSON 序列化（ns/op，Windows，5 次中位）

| 载荷 | jsonx | sonic-std | sonic-fastest | go-json | stdlib | jsoniter |
|---|---:|---:|---:|---:|---:|---:|
| 1 对象 | 321 | 327 | **215** | 216 | 637 | 314 |
| 64 对象 | 7.3 µs | 7.4 µs | **7.0 µs** | 9.1 µs | 28.5 µs | 14.5 µs |
| 4096 对象 | 478 µs | 477 µs | **442 µs** | 639 µs | 1852 µs | 981 µs |

- `jsonx` 与 `sonic.ConfigStd` 完全同档（派发开销 ≈ 0）：两者都保证与 `encoding/json`
  一致的 HTML 转义与 map 键排序。
- `sonic.ConfigFastest` 快 15–35%，但输出不保证标准库语义（HTML 转义/键序），不能作为
  默认后端；`go-json` 与之同档且分配更少（1 vs 2）。
- `encoding/json` 在中等/大载荷上慢 3.9–4×；jsoniter 介于两者之间。

### 3.16.2 JSON 反序列化（ns/op）

| 载荷 | jsonx | sonic-std | sonic-fastest | go-json | stdlib | jsoniter |
|---|---:|---:|---:|---:|---:|---:|
| 1 对象 | 791 | 835 | 765 | **658** | 1400 | 674 |
| 256 对象 | 131 µs | 156 µs | 132 µs | **80.5 µs** | 270 µs | 142 µs |

**发现**：对大数组反序列化，`go-json` 比 sonic ConfigStd 快约 **1.6×**，且分配数约为其 1/6
（259 vs 1547）。这提示 `jsonx` 未来可评估「sonic 序列化 + go-json 反序列化」的分工；
但两者错误类型与 `any` 解码细节不同，会影响 `ErrSyntax`/`ErrTypeMismatch` 的映射，
属于语义变更，本轮未采纳。

### 3.16.3 Base64（ns/op，Windows；64 KiB 以 µs 计）

| 操作 | 尺寸 | b64x | stdlib | cristalhq | segmentio/asm |
|---|---|---:|---:|---:|---:|
| 编码 | 16 B | 16.8 | 17.3 | **13.5** | 18.0 |
| 编码 | 1 KiB | 425 | 781 | 368 | **84** |
| 编码 | 64 KiB | 23.9 µs | 49.0 µs | 23.7 µs | **4.6 µs** |
| 解码 | 16 B | 17.1 | 23.7 | **15.2** | 25.9 |
| 解码 | 1 KiB | 497 | 835 | 498 | **112** |
| 解码 | 64 KiB | 30.2 µs | 53.1 µs | 31.0 µs | **5.7 µs** |

- 小尺寸（≤ 256 B）：cristalhq（也就是 b64x 的切片后端）最快；
  segmentio/asm 的 SIMD 在极小输入上有固定开销。
- 1 KiB 以上：`segmentio/asm` 快 4–9×（64 KiB 编码/解码约 12.6/10.3 GB/s），
  是当前生态里最快的实现；但它是一个额外的汇编依赖（仅 amd64/arm64），
  引入需按「依赖克制」单独评估，本轮未替换。
- `b64x` 现在的组合已拿到两端的较优解：切片接口走 cristalhq，字符串入口走标准库。

### 3.16.4 Linux 对照（WSL Ubuntu-24.04）

| 场景 | 结果 |
|---|---|
| JSON 序列化 64 对象 | jsonx 7.45 µs ≈ sonic-std 7.51 µs，stdlib 27.9 µs |
| JSON 反序列化 256 对象 | jsonx 138 µs，go-json 80.2 µs，stdlib 287 µs |
| Base64 编码 64 KiB | b64x 26.1 µs（2.51 GB/s），stdlib 54.1 µs，segmentio 5.2 µs |
| Base64 解码 64 KiB | b64x 36.3 µs（1.80 GB/s），stdlib 60.3 µs，segmentio 6.4 µs |

相对排序与 Windows 完全一致；差异来自 WSL 调度与 CPU 频率。

## 3.17 OpenAPI 模型补全与稳定性（第十五轮）

### 3.17.1 对象覆盖

`http/openapi` 的文档模型现已覆盖 OpenAPI 3.0.3 的全部核心对象；3.1.1 的新增对象与字段通过
`LatestSpecVersion` 显式启用。原有类型与字段保持不变，新增项全部带 `omitempty`：

| 对象 | 本轮补齐 |
|---|---|
| Document | `webhooks`（3.1）、`jsonSchemaDialect`（3.1）、`tags`、`security`、`externalDocs` |
| Info / License | `summary`（3.1）、`identifier`（3.1）；`contact` 与 `license` 零值不再输出空对象 |
| Server | `variables` |
| PathItem | `$ref`、`summary`、`description`、`servers`、`parameters`、`trace` |
| Operation | `deprecated`、`security`、`servers`、`callbacks`、`externalDocs` |
| Parameter | `$ref`、`style`、`explode`、`deprecated`、`allowEmptyValue`、`allowReserved`、`examples`、`content` |
| Header | `$ref`、`style`、`explode`、`allowReserved`、`examples`、`content` |
| MediaType / Encoding | `encoding`（含 `contentType`、`headers`、`style`、`explode`、`allowReserved`） |
| Response | `headers`、`links`（类型化 `Link`）、`$ref` |
| Components | `securitySchemes`、`parameters`、`responses`、`requestBodies`、`headers`、`examples`、`links`、`callbacks`、`pathItems`（3.1） |
| Schema | 组合关键字 `allOf` / `oneOf` / `anyOf` / `not`、`discriminator`、`xml`、`additionalProperties`、`default`、`title`、`writeOnly`、`exclusiveMinimum` / `exclusiveMaximum`、`multipleOf`、长度/数量约束；3.1 下另有 `const`、`examples`、类型数组（`Types`）与 JSON Schema 2020-12 的 `$id` / `$defs` / `$anchor` / `$dynamicRef`、`if/then/else`、`prefixItems` / `contains`、`patternProperties`、`dependent*`、`content*`、`unevaluated*` |

### 3.17.2 注解兼容性

- 新增 `@ID`（operationId）、`@Deprecated`、`@Header`（支持 `all` 伪状态码）。
- `@Security` 支持作用域（`@Security OAuth2 read:users`）与 swaggo 布尔运算符（`&&` 与、`||` 或），
  生成标准的 `[]SecurityRequirement`。
- `{object} string`、`{object} file` 不再生成悬空的组件引用；`{file} file` 生成
  `string/binary`，与常见 Swagger 方言一致。
- `formData` 参数转换为 `application/x-www-form-urlencoded` 或 `multipart/form-data` 请求体
  （由 `@Accept` 决定），不再作为非法参数位置输出。
- 支持 Go 复合类型标记（`[]model.User`、`map[string]int`）与 swaggo 包装语法
  （`model.Result{data=model.User,code=int}`，生成 `allOf`）。
- `@Param` 支持 swaggo 属性：`Enums(...)`、`default(...)`、`example(...)`、`Minimum/Maximum`、
  `MinLength/MaxLength`、`MinItems/MaxItems`、`Pattern(...)`、`Format(...)`，枚举按 Schema 类型
  保持整型/浮点/布尔语义。
- `@Deprecated` 同时属于类型方言：处理器注释块只要含 `@Router` / `@Summary` 等处理器专有指令，
  仍按处理器注解解析，不会被类型解析器抢走。

### 3.17.3 规范扩展（x-*）

- 所有以结构体表示的 OpenAPI 对象都带 `Extensions map[string]any`，序列化时**提升到对象层级**
  （不产生嵌套的 `extensions` 字段），解码时自动回收。
- 键必须以 `x-` 开头；否则 `JSON()` 返回包装了 `ErrInvalidExtension` 的错误，绝不覆盖标准字段。
- 合并采用字节拼接而不是解码再编码，因此数值精度与字段顺序保持不变；扩展键按字典序输出。
- `Paths`、`Responses`、`Callback` 仍以 `map` 表示，任意扩展需要改变公开 Go 类型，因此不在支持范围；
  这三类对象仍可正常读写其标准成员。
- Schema 的 3.1 类型数组、数值开界（`exclusiveMinimum`/`exclusiveMaximum`）在序列化与反序列化
  两端都受支持；3.0 的布尔开界继续原样工作。

### 3.17.4 方言兼容校验

`Validate()` 会检查 3.0/3.1 混用：3.0 文档中出现 3.1 专属字段（`webhooks`、`jsonSchemaDialect`、
`info.summary`、`license.identifier`、`components.pathItems`、`mutualTLS`、3.1 Schema 关键字等）会报错；
3.1 文档中出现 `nullable` 或布尔开界也会报错，提示改用 `Types` 与数值开界。错误仍统一包装
`ErrInvalidDocument`，可用 `errors.Is` 判定。

### 3.17.5 查看器完整渲染

`/docs` 现在渲染：文档概览、服务器与变量、标签、全局安全、安全方案及 OAuth 流程、Webhook、
组件计数；操作级参数（含 path 级共享参数合并）、请求体、响应、响应头、链接、回调、扩展；
Schema 的 `$ref`、类型数组、`allOf`/`oneOf`/`anyOf`/`not`、`prefixItems`/`contains`、
`if/then/else`、`additionalProperties`/`unevaluated*`、约束、枚举、示例、discriminator/xml 等。
数据岛使用 JSON Unicode 转义而不是 HTML 实体，恶意字符串无法提前关闭 `<script>`，且能被
`JSON.parse` 原样还原；外部文档只对 `http(s)`/`mailto` 生成链接。

### 3.17.6 注解收集并行化

- 新增 `openapi.ParseSource(fsys, dirs)`：**一次遍历、一次读取、一次解析**同时得到处理器注解与
  类型注解，替代 `ParseAnnotations` + `ParseTypeAnnotations` 的两次扫描。
- 文件读取与解析按 `GOMAXPROCS` 组成有界协程池并行执行；每个文件的 `token.FileSet` 独立，
  `go/parser` 使用 `SkipObjectResolution`；结果按排序后的文件顺序合并，输出与调度无关。
- 廉价预过滤：不含 `type/const/var` 声明或不含任何类型方言指令的文件直接跳过 `go/parser`；
  不含 `@` 的文件跳过处理器注解扫描。
- 基准（Windows，MemMapFs，含双方言注解的源文件）：

  | 文件数 | one-pass | two-pass | 串行 | 并行加速 |
  |---:|---:|---:|---:|---:|
  | 64 | 1.17 ms | 1.59 ms | 1.31 ms | 1.12× |
  | 256 | 3.19 ms | 3.31 ms | 5.24 ms | 1.64× |

  真实磁盘上第二次目录遍历与文件读取的收益通常高于内存文件系统；内存文件系统中并行收益主要来自
  `go/parser` 的 CPU 部分。

### 3.17.7 校验与稳定性

`Document.Validate()` 一次返回全部问题（`errors.Join`，均可用 `errors.Is(err, ErrInvalidDocument)` 匹配）：
标题/版本、路径以 `/` 开头、操作至少一个响应、状态码与媒体类型合法、路径参数已声明且必填、
operationId 唯一、安全方案已声明且类型字段齐全、组件 Schema 引用可解析、参数位置受支持。

`Handler` 现在在首次请求时序列化一次并缓存两份载荷，带强 ETag 与 `If-None-Match`（含 `*` 与弱校验器）
返回 304；并发请求共享同一快照，不会重复编码，也不会观察到写到一半的文档。`NewBuilder` 初始化全部
组件映射，直接赋值不再触发 nil map panic。脚手架在装配期声明 `BearerAuth` 并调用 `Validate()`，
不合规只告警、不阻断启动；注解收集改用 `ParseSource` 单次扫描。

## 3.18 路由横评与热路径优化（第十六轮）

### 3.18.1 方法

- 环境：Windows 11，i9-10980HK，Go 1.27.2，`-cpu 16`。
- 对照：yd、Gin v1.12.0、Echo v4.16.0、chi v5.5.0、httprouter v1.3.0、Fiber v3.6.1。
- 进程内路由基准：无中间件，处理器只写 204；每个 goroutine 复用一个请求与响应写入器，
  路径分别为静态 `/static/ping`、参数 `/users/42`、深层 `/users/42/posts`、
  通配 `/files/a/b/c.txt`、未命中 `/nope`。Fiber 通过 `fasthttp.RequestCtx.Init` 直接调用
  其 handler，避免网络与 `app.Test` 的转换开销。
- 精准率：同一路由表（根、静态、参数、深层、通配、404、静态优先、大小写、查询串），
  按规范响应体与状态码比对；另记录 405、尾斜杠与编码斜杠行为。

### 3.18.2 吞吐（ns/op；全部 0 allocs/op）

Windows 11，i9-10980HK，Go 1.27.2，`-cpu 16`：

| 场景 | yd | Gin | Echo | httprouter | Fiber | chi |
|---|---:|---:|---:|---:|---:|---:|
| 静态 | **5.8** | 9.2 | 8.9 | **4.7** | 26.5 | 191.7 |
| 参数 | **6.1** | 10.8 | 10.6 | 22.4 | 29.5 | 344.7 |
| 深层参数 | **5.9** | 12.0 | 12.0 | 23.5 | 37.0 | 334.3 |
| 通配 | **6.0** | 11.5 | 8.9 | 22.2 | 30.1 | 326.3 |
| 未命中 | **3.0** | 12.8 | 348.7 | 117.4 | 36.7 | 237.1 |
| 高基数参数（4096 条轮转路径） | **12.9** | 13.2–13.5 | 13.2 | — | — | — |

同一套基准在 WSL Ubuntu-24.04 / Linux 上复跑（共享虚拟机，绝对数字高于裸机）：

| 场景 | yd | Gin | Echo |
|---|---:|---:|---:|
| 静态 | **6.5** | 10.6 | 9.2 |
| 参数 | **6.4** | 12.5 | 10.8 |
| 深层参数 | **6.4** | 13.3 | 12.0 |
| 通配 | **6.5** | 12.3 | 9.7 |
| 未命中 | **3.4** | 13.8 | 458.5 |
| 高基数参数 | **13.5** | 14.6 | 13.7 |

- 对比上一轮（15.0/15.6/16.6/15.4/41.1 ns），匹配路径累计再降约 **2.6×**，未命中降约
  **13×**；本轮最后五项微优化在已缓存热路径上再降约 **20%**。
- 对 Gin/Echo 的五个常规场景全部领先；对 Fiber/chi 领先更多。静态场景仍慢于 httprouter
  （裸压缩基数树、无上下文与中间件），但 yd 的路由同时承担中间件链、上下文池、请求体限额、
  405/尾斜杠与编码路径语义。
- 高基数参数场景指每个请求的 `:id` 都不同、精确路径缓存无法命中；这是最不利的“未命中路径”
  形态，yd 由形状优先分支承担（见 3.18.3），在中位数上领先 Gin/Echo。

### 3.18.3 路由缓存与未命中优化

| 结构 | 设计 | 关键约束 |
|---|---|---|
| 精确路径缓存 | 每个池化 `Ctx` 内 32 槽直接映射；56 位路径哈希（单次乘法混合；短路径覆盖全部字节，长路径均匀采样）；同一槽位第二次出现才准入；命中后借用不可变参数视图，`Params.Append` 首次修改时写时复制 | 条目只属于当前 Ctx，无共享缓存行、原子操作或锁；`cacheGen` 在每次注册路由时推进，旧代际条目与准入字全部失效 |
| 上次命中项 | 命中后记录条目指针、长度与中点字节；下一请求先比较这些提示，命中则完全跳过哈希与槽位索引 | 提示只是过滤器，条目仍校验代际/路径/方法；过期提示只造成回退，不会造成错误匹配 |
| 形状优先分支 | 仅当上一请求由形状路由时启用：下一请求在哈希/精确查找之前尝试形状；最近两条具体路径重复时才提升到精确缓存 | 冷轮转工作集不提升、不哈希；热具体路径在数次请求内进入上次命中项快路径；方法/代际不匹配立即回落 |
| 路由形状缓存 | 注册期为仅含静态/参数段的路由预编译形状；单参数形状编译为“前缀 + 参数 + 后缀”，匹配只需两次锚点比较加一次斜杠扫描；多参数形状走分段循环 | 安装前用 `patternUsable` 校验每个参数节点没有静态/混合兄弟，否则另一个值可能路由到别处；形状按代际重新校验，新增路由后立即失效 |
| 非回溯匹配器 | `matchNodeFast` 迭代下探静态链与被迫参数分支，只有出现需要回溯的竞争边时才委托完整匹配器 | 静态子节点用首字节索引 + 单子节点内联；参数与通配的优先级语义完全保留 |
| 段扫描 | 8 字节窗口一次找 `/`（小端零字节检测），短后缀走内联字节循环 | 长路径按 8 字节推进，避免逐字节循环 |
| 段比较 | 长度 ≤8 时比较首尾各 4 字节；更长时交给运行时向量化比较 | 读取边界严格限制在字符串长度内 |
| 方法比较 | 标准方法只比较整数 ID；单处理器终止节点直接比较唯一处理器 | 非标准方法仍回退到名称比较 |
| 结构性 404 | 根节点维护静态首字节掩码；首字节不在任何根静态边上且无参数/通配/混合边时，直接写 404 | 不取上下文、不走对象池、不遍历树；自定义 NotFound 与所有可能命中的路径仍走正常路径 |
| 路径分帧 | 精确缓存命中走精简的 `serveCached`；未命中走 `serveSlow`（形状 → 树 → 405/尾斜杠/404） | 热路径函数更小，慢路径可自由扩展；`PanicHandler` 的 defer 覆盖两条路径 |
| 请求体限额 | GET/HEAD 且 ContentLength 为 0 时跳过急切包装；判断使用方法 ID 整数比较；`Ctx.Body` 仍惰性调用 | 分块正文与非 GET/HEAD 请求行为不变 |
| Ctx 标志位 | `written/aborted/bodyLimited/borrowed` 压入一个 `uint8`，`reset` 一次清零 | 语义与逐字段布尔完全一致；借用切片仍在 reset 时丢弃 |

### 3.18.4 热点与修复（本轮新增）

| 热点（pprof） | 原因 | 修复 |
|---|---|---|
| 每请求 `RLock` / 原子读 | 注册锁与路由状态共享缓存行 | 不可变 `serveState` 快照 + 单次原子指针读 |
| 每请求 `Params` 池对象 | 再取还一个池对象 | `Params` 内嵌进 `Ctx`；缓存视图写时复制 |
| 缓存共享槽位弹跳 | 16 核写同一批槽位的准入字 | 缓存下沉到每 Ctx（每 P 私有），冷路径无共享写入 |
| 采样哈希碰撞 | 只采样首尾使 `/users/N/posts` 大量同哈希，冷流量反复准入/逐出 | 56 位全覆盖短路径哈希；连续两次同哈希才准入 |
| 段内逐字节扫描 | 每个分段一次线性扫描 | 8 字节窗口找 `/`；短后缀保留内联字节循环 |
| 分段字符串比较 | 非恒定操作数触发 `memeqbody` 调用 | ≤8 字节用首尾 4 字节比较；方法 ID 消除方法名比较 |
| 静态子节点线性扫描 | 扇出较大的节点每个分段扫描一遍 | 首字节索引；单子节点与索引路径内联进匹配器 |
| 静态分支回溯代价 | 递归匹配器每分段一次函数调用 | 非回溯快路径迭代下探；只有竞争边才委托 |
| 404 上下文开销 | 结构性未命中仍取 Ctx、走池、遍历树 | 根首字节掩码直接 404 |
| 高基数参数路径 | 精确路径缓存无法命中，只能走树 | 路由形状缓存：单参数前缀/后缀锚点，跨具体 ID 复用 |
| 热路径与慢路径同帧 | 缓存命中与未命中共享一个大函数帧 | 拆分为 `serveCached` 与 `serveSlow` |
| 缓存参数缓冲复用 | `reset` 把共享视图重新切片后，下一次未命中会写穿缓存项的参数数组 | 共享视图在 `reset` 时丢弃引用；`Params.Reset` 保留 `shared` 标记；两个回归测试已做变异验证 |
| 重复路径的哈希/查找 | 每请求都算 56 位哈希并查槽位 | 上次命中项 + 长度/中点提示；重复路径只做一次字符串比较 |
| 冷路径的哈希/查找/提升 | 形状命中前仍先算哈希、查精确缓存并尝试提升 | 形状优先分支；冷轮转路径直接跳过三者，仅保留形状匹配 |
| 请求体限额方法判断 | 两次方法字符串比较 | 方法 ID 整数比较 |
| 可选处理器判定 | 每请求读取并比较函数值/`NotFound` 接口 | `serveState` 预置 `hasPanicHandler` / `hasCustomNotFound` 布尔 |
| 根首字节判定 | 4 字位图 + 移位/掩码 | 256 字节查找表，一次带下标加载 |
| `reset` 布尔清零 | 四个独立布尔存储 | 单字节 flags，一次清零 |
| 哈希混合/短参数扫描 | 每字两轮乘法；短参数逐字节找 `/` | 单次乘法 + 末尾折叠；4 字节窗口检查 |

### 3.18.5 精准率

| 框架 | 核心用例 | 方法不匹配 | 尾斜杠 | 编码斜杠 `/users/a%2Fb` |
|---|---:|---|---|---|
| yd | 9/9 | 405 | 301 → `/users` | 200，参数保持 `a%2Fb` |
| Gin | 9/9 | 404 | 404（严格模式） | 404（按解码路径路由） |
| Echo | 9/9 | 405 | 404 | 200，参数 `a%2Fb` |
| chi | 9/9 | 405 | 404 | 200，参数 `a%2Fb` |
| httprouter | 8/8 | 405 | 404 | 404 |
| Fiber | 9/9 | 405 | 404 | 200，参数 `a%2Fb` |

- httprouter v1.3.0 的静态子节点与既有参数子节点冲突时**注册期 panic**，因此静态优先用例
  标记为不支持（分母 8）；这是其路由树的已知限制。
- Fiber 对静态/参数同级节点按**注册顺序**匹配：静态路由必须先注册，否则 `/users/me` 会被
  `:id` 捕获；本表按先注册静态的公平配置统计。
- yd 额外支持 `:id-xx.png` 与 `:name.:ext` 这类段内混合模式（9/9 之外的高级用例），
  其他被测框架不支持；这是 yd 用带记忆化回溯换来的能力。
- yd 在编码斜杠上按原始编码匹配，`/users/a%2Fb` 不会被当作两段；Gin/httprouter 按解码后的
  `URL.Path` 路由，因此返回 404。这是有意的安全语义差异。
- 缓存与形状快路径的回归测试覆盖：精确缓存命中后的全局中间件、HEAD 回退、方法隔离、
  自定义方法（PROPFIND）、参数写时复制与跨请求缓冲生命周期、缓存代际失效、新增路由后的形状失效、静态兄弟安全
  判定、多参数形状、尾斜杠拒绝、空参数拒绝、编码斜杠、形状优先分支的交替参数与静态路径回退，以及
  混合/通配路由回退。

### 3.18.6 剩余差距

- **httprouter 静态场景**仍快约 1.2×（4.7 vs 5.8 ns）：它是裸压缩基数树，不携带上下文池、
  中间件链、请求体限额与编码路径语义。若把 yd 缩到同等功能面，这一差距会大幅收窄；在保留
  完整框架语义的前提下，本轮不再以牺牲功能换静态数字。
- **Gin/Echo** 在五个常规场景与高基数参数场景均已落后于 yd；其优势主要来自更薄的上下文与
  调度，而 yd 通过每 Ctx 路由缓存、形状缓存与迭代匹配器把这一差距反超。
- 后续若继续压缩，方向是复用 `Ctx` 池的分配策略与合并 404/405 提前判定；这些改动会影响
  错误语义与中间件顺序，风险高于本轮收益，暂不推进。

## 3.19 压缩横评与默认策略调整（第十七轮）

### 3.19.1 方法

- 环境：Windows 11，i9-10980HK，Go 1.27.2。
- 载荷：64 KiB 高度可重复文本、约 93 KiB JSON、64 KiB 确定性随机字节。
- 流式压缩/解压，编码器与解码器复用 `Reset`，压缩目标为 `io.Discard`；数值为
  `ns/op` 与按原文计算的吞吐。
- 对照：stdlib gzip、klauspost gzip（默认与 BestSpeed）、klauspost zstd（默认与
  SpeedFastest）、andybalholm brotli（q1/q4/q6/q9）、snappy、lz4。

### 3.19.2 压缩吞吐（µs/op，括号内为 MB/s）

| 编码 | 文本 | JSON |
|---|---:|---:|
| gzip/stdlib | 135.3 (484) | 292.9 (317) |
| gzip/klauspost | 33.8 (1940) | 164.2 (565) |
| gzip/klauspost-fast | 32.6 (2013) | 83.1 (1117) |
| zstd/default | **23.7 (2763)** | **90.6 (1025)** |
| zstd/fastest | 18.5 (3546) | 89.4 (1039) |
| brotli/q6（库默认） | 68.5 (957) | 1032.5 (90) |
| brotli/q4 | 48.3 (1358) | 818.8 (113) |
| brotli/q1 | 42.7 (1534) | 179.8 (516) |
| snappy | 9.8 (6662) | 24.5 (3790) |
| lz4/default | 15.1 (4354) | 51.5 (1804) |

解压（文本 / JSON，µs）：gzip stdlib 14.7 / 69.9，klauspost gzip 13.6 / 50.5，
zstd 7.9 / 28.5，brotli q6 178.5 / 226.1，brotli q1 152.8 / 239.6，snappy 9.4 / 18.7，
lz4 10.3 / 18.6。

### 3.19.3 压缩率（压缩后 / 原文）

| 编码 | 文本 | JSON | 随机 |
|---|---:|---:|---:|
| gzip | 0.006 | 0.048 | 1.000 |
| zstd | 0.002 | 0.023 | 1.000 |
| brotli q6 | 0.002 | 0.022 | 1.000 |
| brotli q4 | 0.002 | **0.019** | 1.000 |
| brotli q1 | 0.002 | 0.027 | 1.000 |
| snappy | 0.049 | 0.120 | 1.000 |
| lz4 | 0.006 | 0.074 | 1.000 |

### 3.19.4 发现与修复

1. **klauspost gzip 比标准库快 2–4×**（文本 33.8 vs 135.3 µs，JSON 164.2 vs 292.9 µs），
   压缩率完全相同；现有的 klauspost 依赖是正确的。
2. **Brotli 默认质量 6 是明显的低指标**：JSON 压缩仅 90 MB/s，比 zstd 默认慢 **11×**，
   而压缩率只从 0.021 改善到 0.022。质量 4 在同载荷上反而把压缩率改善到 0.019，同时快 21%；
   质量 1 快 5.8×，压缩率 0.027（仍优于 gzip 的 0.048）。中间件默认改为 **brotli 质量 4**。
3. **默认优先级让 Brotli 抢先**：在客户端同时接受 `br` 与 `zstd` 时，原顺序
   `{Brotli, Zstd, Gzip}` 总是选择慢 4–11× 的 brotli。默认顺序改为
   **`{Zstd, Brotli, Gzip}`**：zstd 在压缩率与吞吐上都优于或持平 brotli，且只在客户端
   声明接受 zstd 时才会被选中；不接受 zstd 的客户端仍回退到 brotli/gzip。
4. **snappy/lz4 吞吐最高**（文本 6.7/4.4 GB/s），但压缩率 0.049/0.006 与 0.120/0.074，
   且不是 HTTP 标准内容编码；只作为吞吐参照，不进入中间件。
5. **随机数据**所有编码压缩率约 1.0、吞吐高于文本（无匹配可找），说明中间件按响应关闭
   压缩的写法（Content-Encoding、no-transform、MinSize）是必要的。

### 3.19.5 中间件级基准

7 KB 文本响应，完整路由 + 压缩中间件路径（Windows）：

| 编码 | ns/op | 分配 |
|---|---:|---|
| gzip | 1001 | 3 allocs / 83 B |
| zstd | **971** | 3 allocs / 103 B |
| brotli q4 | 4032 | 3 allocs / 123 B |
| 低于阈值直通 | 46 | 0 allocs / 0 B |

- 三次分配来自 `Content-Encoding`、`Vary` 等响应头值切片；编码器、正文缓冲与 4 KiB
  输出槽均从池中复用，编码器本身在稳态下不分配。
- 基准中复用的响应头映射必须每轮清空，否则上一轮的 `Content-Encoding` 会让
  `headersAllowCompression` 正确地跳过后续压缩——这既是基准陷阱，也是中间件
  防止二次编码的预期行为。

### 3.19.6 精准率

- 每个编码在空输入、1 字节、文本、JSON、随机字节上逐字节往返一致。
- 中间件对 gzip/br/zstd 的往返、`Accept-Encoding` q 值协商、`Vary`、移除
  `Content-Length`、保留处理器头部、阈值/排除路径/HEAD/升级/SSE/no-transform
  均有测试覆盖。
- 协商扫描保持零分配；默认顺序变更后所有协商用例与参考实现仍逐项一致。

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
8. **真实的系统服务安装未在本机验收**：`yd service install` 会修改操作系统状态（注册 SCM 服务、
   写 systemd unit），因此测试全部经注入点打桩，只断言「动作 → 服务管理器调用」的映射、名称推导、
   平台不支持与权限不足四条路径。真实安装需要管理员（Windows）/ root（POSIX）。
9. **系统服务的启停需要真实服务管理器**：服务模式下的优雅退出由 `service.Run` 的 ctx 取消驱动，
   已由单元测试覆盖；但「SCM / systemd 真的把停止信号送进来」这一段依赖宿主环境，未在 CI 中跑。
10. **`yd ko` 的真实产物未在本机验收**：本机未安装 ko，验收用的是**回显 argv 的假 ko 二进制**，
    覆盖到命令拼装、环境变量传递、输出解析与四条错误路径；真实镜像构建需要 ko 二进制、
    基础镜像与（推送时的）仓库凭据。这与 `yd docker` 未做真实镜像构建的口径一致。
11. **`ko.*` 未在 `config/defaults.go` 注册默认值**：与既有的 `service.*` 一致，
    缺省值由 `OptionsFromConfig` 的回退给出。
12. **自动建库未对真实 MySQL / PostgreSQL 验证**：本机没有这两个服务端，建库与重试全链路是
    经注入口验证的（错误码分类、SQL 生成与转义、维护连接串、重试次数、失败聚合）。
    「`CREATE DATABASE` 在真实服务上跑通」需要集成环境补一次 e2e。
13. **zsh / csh 补全的运行期行为未验证**：本机与 WSL 内均无这两个解释器，只有生成文本 +
    语法检查的**诚实跳过**（`t.Skipf` 打印原因并汇总「执行 N、跳过 M」）。
14. **多行注释的「整块有英文」判定是启发式**（`[A-Za-z]{2,}`）：一个「中文散文 + 内含代码标识符」
    的多行块会被判为合格。收紧到「必须有一句真正的英文」会误伤大量合法引用标识符的注释，
    因此保留宽松度并在此记录。
15. **测试断言消息不在双语守卫范围内**：`t.Errorf` / `t.Fatalf` / `t.Logf` 的中文单语消息
    仍存在（各包合计约千行）。守卫的 `outputCallRe` 文档注释写明了这条边界与收紧方式；
    收紧属于规范口径变更，需另行决定。
16. **上传落盘直接经 `os`**：`CopyUpload`/`CopyUploads` 的目标是本地目录，不经 afero
    抽象；对象存储等后端请使用 `StreamUploads` 自行适配（回调只保证分段在返回前有效）。
17. **`DownloadReader` 不支持 Range 与条件请求**：不可回退的数据源无法实现 206/304；
    内容可定位时应使用 `Download` 或 `DownloadFile`，二者完整支持 Range/ETag/Last-Modified。
18. **`JSONStream` 以错误原子性换取内存**：增量编码在响应头与部分正文写出后仍可能失败，
    因此可能编码失败的值应使用 `JSON`（先完整序列化，失败时干净地返回 500）。
19. **SM2 验签成本来自 gmsm**：约 1.35 ms、1965 allocs/op（big.Int 运算与临时对象），
    框架层未替换算法实现；需要更快验签应在上游优化或改用其他曲线/协议。
20. **`Paths` / `Responses` / `Callback` 不承载任意 `x-*` 扩展**：这三类对象以 `map` 表示，
    任意扩展需要改变公开 Go 类型；其余全部结构体对象均支持 `Extensions`。这三类对象的标准成员
    读写不受影响。
21. **3.1 Schema 关键字要求版本同步**：模型已覆盖 JSON Schema 2020-12 的主要关键字（`type`
    数组、`$id` / `$defs` / `$anchor` / `$dynamicRef`、`if/then/else`、`prefixItems` / `contains`、
    `contentEncoding` / `contentMediaType`、`unevaluatedProperties` / `unevaluatedItems` 等），
    但这些字段在 3.0.3 中不被规范允许；使用它们时必须把 `Document.OpenAPI` 设为
    `LatestSpecVersion`，否则请只用 3.0 字段。
