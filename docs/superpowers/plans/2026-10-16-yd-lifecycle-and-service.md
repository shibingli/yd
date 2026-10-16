# yd 实施计划：优雅退出与系统服务化

> 需求来源（用户原话）：
> 「服务要支持优雅的退出，包括终止中间件连接等，并采用 https://github.com/kardianos/service
> 添加一个命令来支持将程序安装为服务」

## 0. 需求差异说明

`docs/需求设计说明书.md` 为**只读需求基线**，其中仅提到「优雅降级」（非 x86_64 下
Base64 走标准库），**没有**关于进程优雅退出与系统服务化的条目。本文件即该需求变更的
差异说明，后续以本文件、`CLAUDE.md`、`AGENTS.md` 为准。

新增第三方依赖：`github.com/kardianos/service` v1.3.0（纯 Go，无 cgo，跨平台服务管理：
Windows SCM / systemd / launchd）。体积影响：编译进产物的代码量在数十 KB 量级。

## 1. 问题陈述

### 1.1 现状缺口

`server.Server.Shutdown`（`server/server.go`）当前只做两件事：关闭 HTTP/3 监听、调用
`http.Server.Shutdown` 排空 HTTP/1.1 + H2C 上的普通请求。

这条路径有**一个结构性漏洞**：

> `http.Server.Shutdown` **既不追踪也不关闭被 `Hijack()` 出去的连接**，对流式响应
> （SSE）也只等到它在 `Shutdown` 前就已结束的请求。

后果：

| 场景 | 实际行为 | 期望行为 |
|---|---|---|
| WebSocket 长连接在线 | `Shutdown` 立即返回，连接**在进程退出时被粗暴切断**（客户端看到 TCP RST，不是关闭帧） | 发送关闭帧 `1001 going away`，等对端回帧或超时后关闭 |
| SSE 流在线 | 同上；订阅者 goroutine 与 `Broker` 无统一收尾 | 推一个终止信号并 flush，关闭流，`Broker` 一并收口 |
| 有连接卡在 hijack 状态 | 进程退出耗时不可控 | 宽限期到点**强制关闭**，退出耗时上界确定 |
| gRPC 在跑 | 由 `grpcx` 各自处理，无统一宽限与顺序 | 统一顺序：`GracefulStop` → 超时 `Stop` |

### 1.2 需求拆解

1. **优雅退出**：终止中间件层建立的长连接（WebSocket / SSE），并保证退出耗时**有确定上界**。
2. **系统服务**：引入 `github.com/kardianos/service` v1.3.0，提供命令把程序安装为
   Windows 服务 / systemd unit / launchd job，且**服务停止路径必须走同一套优雅退出**。

## 2. 设计

### 2.1 关闭顺序（唯一权威顺序）

```
信号 / ctx 取消
   └─① lifecycle.Done() 关闭          ← 长连接 handler 由此感知「该收尾了」
        ├─ WS：发送 1001 关闭帧 → 等待对端回帧（受宽限期约束）→ 关闭
        └─ SSE：推终止事件 → flush → 关闭 writer；Broker.Close()
   └─② HTTP/3 监听关闭
   └─③ http.Server.Shutdown(ctx)      ← 排空普通请求（含 H2C）
   └─④ 强制关闭仍存活的 hijack 连接    ← 保证 ③ 不被无限拖住
   └─⑤ lifecycle.Registry.Shutdown    ← 逆序执行已注册钩子（gRPC、asynq、redis、gorm…）
```

关键约束：

- **① 先于 ③**：长连接先收尾，否则 `http.Server.Shutdown` 会因为流式响应迟迟不结束而空耗宽限期。
- **④ 是兜底**：即使某个连接完全不响应关闭帧，退出耗时仍受宽限期约束。
- **⑤ 逆序**：后注册的钩子先执行，符合「依赖者先关」的资源释放顺序。

### 2.2 新增 `lifecycle` 包

职责边界：**只做关闭编排**，不做信号监听（信号归 `app`），不做连接 I/O（归 `ws`/`sse`）。

- `Registry`：`Add(name, fn) (remove)`、`Shutdown(ctx) error`、`Done() <-chan struct{}`、
  `ShuttingDown() bool`、`Len() int`
- 语义：并发安全、`Shutdown` 幂等、**逆序**执行、单钩子 panic 被恢复并记为错误、
  错误 `errors.Join` 聚合、ctx 到期即返回并把未完成的钩子记为错误。

### 2.3 长连接登记

`ws` 与 `sse` 各自持有活动连接表：

- `ws`：登记活动 `*Conn`；关闭时 `Close(1001, "going away")`。
- `sse`：登记活动 `Writer`；关闭时终止事件 + flush + 关闭。

**依赖方向**：`ws`/`sse` 不得反向依赖 `lifecycle`（否则形成环）。两者各自暴露一个最小的
「关闭信号」入参或接口，由 `app` 侧注入实现。

### 2.4 `service` 包与 CLI

- `service.Options` → 映射到 `service.Config`；配置键 `service.*`，缺省回落到 `app.name`。
- `service.Control(o, action)`：`install|uninstall|start|stop|restart|status`。
- `service.Run(o, run func(ctx) error)`：服务模式下由服务管理器驱动 `Start/Stop`；
  **`Stop` 只取消 ctx 并等待 `run` 返回，绝不 `os.Exit`**——这是把优雅退出接进服务模式的关键。
- CLI：`yd service <action> [--name --display-name --description --binary --dir --user --args]`。
- 脚手架：生成的 `cmd/app/main.go` 支持 `-service <action>`（kardianos 惯例），
  服务模式与前台模式共用同一条关闭路径。

### 2.5 测试不得真的安装服务

服务安装会改操作系统状态（注册 SCM 服务、写 systemd unit），测试必须打桩到命令执行点，
只断言「动作 → kardianos 调用」的映射、名称推导、平台不支持与权限不足的错误路径。

## 3. 任务分解

| # | 任务 | 产物 |
|---|---|---|
| 1 | `lifecycle` 包 + 单元测试 | `lifecycle/*.go`（含逆序/幂等/超时/panic/并发用例） |
| 2 | `ws` 连接登记与 1001 关闭 | `ws/` 改动 + 端到端关闭帧断言 |
| 3 | `sse` 流终止与 Broker 收口 | `sse/` 改动 + 端到端终止断言 |
| 4 | `server` hijack 登记 + 强制关闭 | `server/` 改动 + **退出耗时上界断言** |
| 5 | `grpcx` GracefulStop → Stop | `grpcx/` 改动 |
| 6 | `app` 编排关闭顺序 | `app/` 改动 + 顺序断言 |
| 7 | `service` 包 + 桩测试 | `service/*.go` |
| 8 | `yd service` CLI 命令 | `cli/service.go` + 测试 |
| 9 | 脚手架模板同步 | `scaffold/templates/**`（含 README/config/CLAUDE.md） |
| 10 | 文档与守卫 | `CLAUDE.md`、`AGENTS.md`、`docs/developer_guidelines.md`、`docs/verification.md`、`internal/repotest` |

## 4. 验收标准

1. `ws`：客户端在服务关闭时**收到 1001 关闭帧**（不是 RST），服务端在宽限期内退出。
2. `sse`：在线流被终止，`Shutdown` 不阻塞超过宽限期。
3. `server`：**存在一个挂死的 hijack 连接时，`Shutdown` 仍在宽限期内返回**（防回归核心断言）。
4. `app`：关闭顺序为「长连接 → server 排空 → 逆序钩子」，错误聚合可见。
5. `lifecycle`：逆序、幂等、超时、panic 恢复、并发 `-race` 全部通过。
6. `yd service`：动作映射、名称推导、平台不支持、权限不足四条路径有测试；
   **测试过程不修改操作系统服务状态**。
7. 生成项目：`-service install` 入口存在且**服务停止走优雅退出**；既有脚手架断言不回归。
8. 全仓门禁：`go build ./...`、`go vet ./...`、`gofmt -l .` 零输出，
   `go test -race ./...` 全绿，`internal/repotest` 守卫通过。

## 5. 已知取舍

- **HTTP/2、HTTP/3 上的 WebSocket 不可用**（协议无 `Hijack`），因此长连接收尾只覆盖
  HTTP/1.1 的 hijack 连接与 SSE 流；这与既有 `server.ErrHijackUnsupported` 的限制一致。
- **服务安装需要管理员/root**：CLI 不自行提权，只给出可读的双语提示并返回哨兵错误。
- **`-race` 依赖 C 工具链**：Windows 上 `CGO_ENABLED=1 CC=clang`，Linux（WSL）原生。
