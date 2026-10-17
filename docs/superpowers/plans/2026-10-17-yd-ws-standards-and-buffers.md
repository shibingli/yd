# yd 实施计划：WebSocket 标准全覆盖 + 数据通路高性能化

> 需求来源（用户原话）：
> 「1、WS要做新老协议与新老标准的完整功能及特性支持；
> ２、就是数据存储与交换的高性能性（可以结合pool\zerocopy\bufferpool等）」

## 0. 需求差异说明

`docs/需求设计说明书.md` 为只读需求基线，未涵盖本节内容。本文件即差异说明。

## 1. 需求一：WebSocket 的标准覆盖

### 1.1 现状

RFC 6455 已完整实现并在上一轮修掉 7 个一致性缺陷（D1–D7，见提交 `13f27a5`）。
**未实现**：RFC 7692（permessage-deflate）、RFC 8441（WebSocket over HTTP/2）。

### 1.2 逐项的可行性结论（均已实测/读源码核实）

| 标准 | 可行性 | 处置 |
|---|---|---|
| **RFC 6455**（基础协议） | 已实现，含 7 项一致性修复 | 保持，矩阵已在 `http/ws/rfc6455_test.go` |
| **RFC 7692 permessage-deflate** | **可实现** | **本轮实现**（见 §1.3） |
| **RFC 8441 WebSocket over HTTP/2** | **当前技术栈不可实现** | 记录原因，不做（见 §1.4） |
| hixie-75/76、hybi-00 等草案 | **不应实现** | 明确拒绝（见 §1.5） |

### 1.3 RFC 7692 的实现范围

- **协商**：解析 `Sec-WebSocket-Extensions: permessage-deflate` 及其参数
  （`client_max_window_bits`、`server_max_window_bits`、`client_no_context_takeover`、
  `server_no_context_takeover`），服务端按自己的能力回写选中的参数；
  未提议则不启用（**默认不启用**，避免与不理解扩展的对端产生兼容问题）。
- **帧层**：启用后**数据帧**的首个分片置 RSV1；控制帧永不压缩、RSV1 恒为 0；
  未启用压缩时收到 RSV1 仍按 §5.2 回 1002（这条现有行为要保留）。
- **压缩语义**：每个消息独立压缩（或按协商做 context takeover）；解压需限制输出大小，
  **防解压炸弹**（压缩比异常时以 1009 关闭）。
- **实现**：用 `klauspost/compress/flate`（已在依赖中），压缩器/解压器按连接或按消息池化。
- **互操作**：与 `coder/websocket` 的 permessage-deflate 做双向对拍（它有对应开关）。

### 1.4 RFC 8441 为什么不做（必须写进文档）

实测证据：

- Go 1.27.2 的 `net/http/h2_bundle.go` **完全没有** Extended CONNECT 支持
  （`grep -c http2xconnect` 为 **0**，也无 `:protocol` 伪头处理）；
- 本仓 `runtime/server` 用的正是 `net/http`（`http.Protocols` 开 h2c、`ServeTLS` 开 HTTP/2）；
- `golang.org/x/net/http2` 有实现，但由包级变量 `disableExtendedConnectProtocol`（默认 `true`）
  控制，**只能经进程启动前的 `GODEBUG=http2xconnect=1` 打开**（Go issue #71128），
  框架无法在运行时可靠设置——GODEBUG 在包 init 时读取。

结论：**在 Go 标准库补齐前，RFC 8441 无法在保持现有 HTTP/2 栈的前提下实现**。
伪造支持（例如文档写「支持」但实际不生效）比不支持更糟。文档与矩阵中如实标注为不支持，
并给出替代方案（HTTP/1.1 升级，或需要实时下行时用 SSE）。

### 1.5 为什么不支持 hixie-75/76 等草案

这些草案在 RFC 6455（2011）发布时即被废弃，握手用 `Sec-WebSocket-Key1/Key2` 的自定义算法、
无掩码要求、安全模型与现代浏览器不兼容。**支持它们只会扩大攻击面**，且现实中已无客户端使用。
在文档中明确「不支持旧草案，仅支持 RFC 6455 及扩展」。

## 2. 需求二：数据通路的高性能

### 2.1 现状（上一轮基准实测）

| 位置 | 现状 | 问题 |
|---|---|---|
| `http/ws` 解码 | 26 B / **2 allocs** 每帧 | `readFrameHeader` 的 `head [2]byte` 与返回的 `frameHeader` 因 `io.ReadFull` 内联而逃逸到堆 |
| `middleware` 压缩 | 每请求 5 次分配（跳过路径也有） | Accept-Encoding 解析用了 `map` + `strings.Split` |
| `middleware` 限流 | 每请求 **8 次分配** | 3 个响应头的 key 每次复制 + 2 次 `strconv.Itoa` |
| `core/logx` | 已零分配 | 保持 |
| 缓冲区 | 各处各自 `make([]byte, n)` | 无统一尺寸分级池 |

### 2.2 设计：`core/bufpool`

- 尺寸分级（如 1 KiB / 4 KiB / 16 KiB / 64 KiB / 256 KiB），每级一个 `sync.Pool`；
- `Get(n) []byte` 取不小于 n 的最小分级；`Put(p)` 仅在容量不超过上限时归还，
  **超过上限直接丢弃**（避免个别大请求把池撑爆，这是 `sync.Pool` 的经典陷阱）；
- 归还时清零长度但**不保证清零内容**——文档必须写明「不得假设取到的是零值」，
  安全敏感场景调用方自行清零；
- 提供 `AllocsPerRun` 级别的测试与基准，证明复用后不再分配。

### 2.3 落点

| 位置 | 用法 |
|---|---|
| `http/ws` 帧读写 | 帧头缓冲区与掩码缓冲走池；顺带消除解码的堆逃逸 |
| `http/ws` 压缩 | flate 的输入/输出缓冲走池（与 RFC 7692 同一轮） |
| `middleware` 压缩 | 编码器缓冲走池；Accept-Encoding 解析改为无分配扫描 |
| `middleware` 限流 | 预计算响应头的规范 key；整数格式化用栈上缓冲 |
| `http/sse` | 写缓冲走池 |

**统一原则**：池化的是**短生命周期的大缓冲**，不是所有小对象；
每处改动都必须附**前后对比基准**，没有实测提升的不合并。

## 3. 验收标准

1. RFC 7692：与 `coder/websocket` 双向对拍通过（启用/不启用两种配置）；
   解压炸弹被 1009 拒绝；未提议扩展时不启用；RSV1 在未协商时仍 1002。
2. RFC 8441 与旧草案：文档与一致性矩阵中明确标注「不支持」及原因，**不得含糊**。
3. `core/bufpool`：分级、超限丢弃、并发安全（`-race`）、取用不假设零值，均有测试。
4. 性能：ws 解码 **2 allocs → 0**；RateLimit **8 allocs → ≤2**；
   Compress 跳过路径的 5 次分配降到 ≤2。每项都要有 `-benchmem` 前后对比。
5. 全仓门禁：`build`/`vet`/`gofmt`/`test -race`/交叉编译/e2e 全绿。

## 4. 已知取舍

- **默认不启用 permessage-deflate**：压缩会消耗 CPU，且与不理解扩展的对端兼容性更差；
  由 `ws.Options` 显式开启。
- **不追求「所有地方零分配」**：池化有维护成本，只在实测有收益的热路径上做。
- **RFC 8441 的缺席是 Go 标准库的限制，不是本框架的取舍**；一旦标准库支持，实现路径已经清楚
  （在 `Upgrade` 里处理 `CONNECT` + `:protocol: websocket`）。
