# 实时传输使用指南（WebSocket / WebTransport / SSE）

`yd` 的实时能力覆盖三种传输，共用同一个消息中枢 `ydx/realtime`：单机在进程内扇出，
多机经 Redis Pub/Sub 跨节点扩散；WebSocket 支持 HTTP/1.1、HTTP/2（RFC 8441）与
HTTP/3（RFC 9220），WebTransport 走 HTTP/3，SSE 在所有 HTTP 版本上工作。
本文是这套能力的唯一使用入口，涵盖选型、单机/多机部署、安全、稳定性、精确性、
性能与兼容性。

> 组件：`github.com/shibingli/ydx/realtime`（中枢与 Redis 后端）、
> `ydx/realtime/adapter`（三种传输的 Sink）、`ydx/websocket`、`ydx/webtransport`、`ydx/sse`。

## 1. 传输选型

| 传输 | 版本 | 方向 | 浏览器支持 | 适用场景 |
|---|---|---|---|---|
| WebSocket | HTTP/1.1 | 双向 | 全部 | 通用兜底；Safari 的默认路径 |
| WebSocket | HTTP/2 (RFC 8441) | 双向 | Chrome/Edge 91+、Firefox（仅 `wss://`） | 同域多路复用，避免额外连接 |
| WebSocket | HTTP/3 (RFC 9220) | 双向 | 无生产支持（Chromium 实验特性） | 自有 Go/原生客户端 |
| WebTransport | HTTP/3 | 双向多流/数据报 | Chrome/Edge、Firefox、Safari 26.4+（Baseline 2026） | 低延迟双向、需要多流或不可靠数据报 |
| SSE | HTTP/1.1、HTTP/2、HTTP/3 | 服务端→客户端 | 全部（`EventSource`） | 单向推送；HTMX 的默认实时通道 |

**推荐阶梯**：WebTransport（H3）→ WebSocket（H2/H1）→ SSE。浏览器端封装见
`web/static/js/yd-realtime.js`：`yd.realtime.Channel` 按阶梯尝试，失败自动降级。

### 1.1 浏览器事实（决定方案上限）

- Chrome/Edge 与 Firefox 在服务端通告 `SETTINGS_ENABLE_CONNECT_PROTOCOL` 后会为
  `wss://` 使用 HTTP/2 Extended CONNECT；Safari 不支持，自动回退 HTTP/1.1。
- 浏览器 `WebSocket` 在 HTTP/3 上没有生产可用实现；HTTP/3 的双向实时通道应使用
  WebTransport（RFC 9220 仅对自有客户端有意义）。
- SSE 不需要升级握手，`EventSource` 在所有浏览器与所有 HTTP 版本上可用；
  HTTP/2/3 上与其他请求多路复用，不受 HTTP/1.1 每域 6 连接限制。

## 2. 服务端配置

```yaml
server:
  h2c: true                    # 明文 HTTP/2（非浏览器客户端）
  http2:
    extended_connect: true     # RFC 8441；进程需 GODEBUG=http2xconnect=1
  http3:
    enabled: true              # HTTP/3（QUIC）
    port: 0                    # 0 表示复用 TCP 端口
    webtransport: true         # 与 HTTP/3 共用监听

realtime:
  node_id: ""                  # 多机必须唯一；留空回退 node.id 与身份文件
  queue_size: 256              # 每个订阅者的有界队列
  max_message_bytes: 1048576   # 单条消息上限
  slow_policy: drop            # drop | close
  channel_prefix: "yd:rt:"     # Redis 频道命名空间
```

`server.http2.extended_connect` 为 true 时，`yd dev`、`yd service install`、`yd docker`
会自动注入 `GODEBUG=http2xconnect=1`；裸二进制由 `app.Run` 以 `syscall.Exec` 重启进程注入
（Windows 控制台走子进程重启；Windows 服务必须经 `yd service` 安装）。HTTP/3 与
WebTransport 强制 TLS：未配置证书时开发环境自动生成一次性自签证书并告警。

## 3. 单机与多机

### 3.1 单机（默认）

```go
hub := realtime.NewHub(realtime.Options{})   // 无 Backend：进程内扇出
defer hub.Close()

sub, err := hub.Subscribe(r.Context(), "room:42", adapter.WebSocket(conn))
defer sub.Close()
```

- `Publish` 在调用方协程内完成扇出，不经过网络；
- 每个订阅者有独立的有界队列与写协程，慢订阅者不会阻塞发布方或其它订阅者；
- 适合单实例部署、测试与灰度环境。

### 3.2 多机（Redis Pub/Sub）

```go
rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
hub := realtime.NewHub(realtime.OptionsFromConfig(cfg))
hub.Use(realtime.NewRedisBackend(rdb, realtime.RedisOptionsFromConfig(cfg)))
defer hub.Close()
```

- 每个节点把消息发布到 `channel_prefix + topic`；只订阅本地存在订阅者的主题；
- 消息 id 形如 `<node_id>:<seq>`；节点收到自己发布的消息会丢弃（本地已直接投递），
  正常情况下订阅者恰好收到一次；启用持久补发后重试可能造成重复，消费端按 id 幂等；
- `seq` 在进程内单调递增，**重启后从 0 开始**；需要跨重启连续的 SSE `Last-Event-ID` 游标时，
  业务端点应把启动纪元（例如进程启动时间）纳入游标或自行去重，不能只依赖 `<node_id>:<seq>`；
- **至多一次**：Redis Pub/Sub 不持久，发布瞬间断开的节点在重连后不会补收；
  需要重放的业务应在自己的持久存储中保存游标，或改用持久队列（如 `ydx/asynq`）；
- 重连由 go-redis 自动完成，但重连窗口内的消息会丢失；SSE 客户端可用
  `Last-Event-ID` 向业务端点补拉，`adapter.SSE` 已把消息 id 写入 `id:` 字段。
- **失败广播可持久补发**：`realtime.Options.Retry` 接收一个 `RetryQueue`；后端发布失败时
  消息入队，worker 用 `Hub.PublishRemote` 只发后端、不重复本地扇出（v0.1.3+）。
  `RetryQueue` 的 asynq 接法与「什么时候该用 asynq、什么时候只用 Pub/Sub」的判定见
  [Redis 与 asynq 使用分区指南](redis-asynq.md)。

### 3.3 部署检查清单

- [ ] 每个实例的 `realtime.node_id` 唯一；留空时回退 `node.id` 与持久身份文件，
      多副本仍建议显式配置（见 [节点身份](node-identity.md)）；
- [ ] 同一部署的 `realtime.channel_prefix` 唯一，避免与其它环境串台；
- [ ] Redis 可达；断连期间消息丢失（见上），关键业务需持久化、业务层补拉，或配置
      `RetryQueue` 持久补发；
- [ ] HTTP/3 需要放行 UDP 端口（默认复用 TCP 端口号）；
- [ ] 多机时钟不必同步（id 与顺序不依赖墙钟）。

## 4. 安全

| 层面 | 机制 |
|---|---|
| 传输 | HTTP/3/WebTransport 强制 TLS 1.3；`wss://` 强制 TLS；H2C 仅建议内网/自有客户端 |
| 来源 | WebSocket 与 WebTransport 默认同源校验（`Origin` 与 `Host` 一致），可注入自定义 `CheckOrigin` |
| 身份 | 握手阶段由既有中间件（会话 Cookie / Bearer / 租户）完成；hub 不携带凭据 |
| 授权 | `realtime.Options.Authorize(ctx, topic)` 在每次订阅时判定；失败返回 `ErrUnauthorized` |
| 大小 | `max_message_bytes` 同时约束发布与远端载荷，超限在分配前拒绝 |
| 主题 | 只接受 `[A-Za-z0-9._:/-]`，长度 ≤128，防止注入 Redis 频道或日志 |
| 输出 | 片段由服务端渲染；用户输入必须先转义（与 htmx 的既有规则一致） |
| 限流 | 传输端点挂 `middleware` 限流（`KeyRemoteIP`），hub 不重复实现 |

**红线**：前端能力判定、Origin 校验与 SSE 游标都不是安全边界；每个受保护端点在服务端
仍必须完成身份与权限校验。

## 5. 稳定性与背压

- **有界队列**：每个订阅者 `queue_size`；队列满时按 `slow_policy` 处理：
  `drop`（丢弃并计数，默认）或 `close`（关闭该订阅，让对端重连）。
- **慢消费者隔离**：发布方永不阻塞；一个卡的连接只影响它自己。
- **心跳**：WebSocket 的 `Options` 与 SSE 的 `Heartbeat` 负责保活；WebTransport 的
  零长度帧是心跳，同时负责让服务端看到流。
- **重连**：htmx 的 `hx-ws` / `hx-sse` 扩展按指数退避重连；SSE 自动回传
  `Last-Event-ID`；WebTransport 客户端应在 `closed` 后重新走阶梯。
- **优雅退出**：`ydx/websocket.ConnSet`、`ydx/sse.WriterSet`、
  `ydx/webtransport.SessionSet` 分别登记三类长连接；`app.Shutdown` 先关闭长连接再排空
  HTTP。WebTransport 会话在 `server` 关闭时先发会话关闭帧、再关 QUIC 连接、最后释放
  UDP 套接字。
- **错误可观测**：`hub.Stats()` 给出 Published/Delivered/Dropped/ClosedSlow/Errors；
  Sink 返回错误即自动解除订阅。

## 6. 精确性

- **顺序**：同一主题、同一订阅者的消息按发布顺序投递（每个订阅者单协程写）。
- **去重**：消息 id = `<node_id>:<seq>`，节点 id 过滤掉自己的 Pub/Sub 回声。
- **SSE 续传**：`adapter.SSE` 把 id 写进 `id:` 字段，浏览器重连带回
  `Last-Event-ID`；服务端据此从业务存储补发。
- **不承诺恰好一次**：Redis Pub/Sub 是至多一次；需要更强语义时在业务层做幂等或使用
  持久队列。
- **不可靠数据报**：WebTransport 的 datagram 按设计可丢、可乱序，只用于可容忍丢失的
  场景（光标位置、遥测）。

## 7. 性能

- 扇出时只传递同一个 `Message`（正文不复制），Sink 负责最终一次写出；
- WebSocket 批量写用 `WriteMessageBuffered`/`WriteBatch`，SSE 单行帧直写，
  WebTransport 每帧一次长度前缀写入；
- 队列长度与 `max_message_bytes` 可按机器规格调低/调高；
- 单机模式无网络往返；多机模式每条消息一次 Redis 发布 + 每个订阅节点一次 Redis 投递。

## 8. 客户端

```html
<script src="{{ asset "/static/js/yd-realtime.js" }}"></script>
<script>
  const ch = new yd.realtime.Channel({
    webtransportURL: "https://example.com/realtime/wt",
    websocketURL: "wss://example.com/ws",
    sseURL: "/realtime/sse",
    sseSendURL: "/realtime/send",
    target: "#yd-stream",
    swap: "innerMorph",
  });
  ch.connect().then((transport) => console.log("realtime via", transport));
  // ch.send("…"); ch.close();
</script>
```

- `chooseTransport` 按「WebTransport → WebSocket → SSE」选择；`forceTransport` 可固定；
- WebTransport 打开双向流后先发一帧心跳（服务端 `AcceptStream` 依赖这次写入）；
- 收到的片段经 `htmx.swap` 交换进目标；无 htmx 时派发 `yd:realtime:message` 事件；
- 事件：`yd:realtime:transport`、`yd:realtime:message`、`yd:realtime:error`、
  `yd:realtime:close`；
- Node 单元测试（帧解析、阶梯判定）：`node web/static/js/yd-realtime.test.mjs`，
  `go test ./web/ -run TestRealtimeJSSuite` 会调用它（无 Node 时跳过并说明原因）。

## 9. 服务端接线示例

```go
// 一条连接一个 Sink；三个传输的适配器都只有一行。
mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
    conn, err := websocket.Upgrade(w, r, websocket.Options{})
    if err != nil { return }
    defer conn.CloseNow()
    sub, err := hub.Subscribe(r.Context(), r.URL.Query().Get("topic"), adapter.WebSocket(conn))
    if err != nil { return }
    defer sub.Close()
    for { if _, _, err := conn.ReadMessage(); err != nil { return } }
})

mux.HandleFunc("/wt", func(w http.ResponseWriter, r *http.Request) {
    sess, err := app.WebTransport().Upgrade(w, r)   // server.http3.webtransport: true
    if err != nil { return }
    ch, err := sess.Accept(r.Context())
    if err != nil { return }
    defer ch.Close()
    sub, err := hub.Subscribe(r.Context(), r.URL.Query().Get("topic"), adapter.WebTransport(ch))
    if err != nil { return }
    defer sub.Close()
    for { if _, err := ch.Receive(); err != nil { return } }
})

mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
    sw, err := sse.NewWriter(w, sse.Options{Heartbeat: 15 * time.Second})
    if err != nil { return }
    defer sw.Close()
    sub, err := hub.Subscribe(r.Context(), r.URL.Query().Get("topic"), adapter.SSE(sw))
    if err != nil { return }
    defer sub.Close()
    <-r.Context().Done()
})
```

Extended CONNECT 会自动复用同一 GET 路由（`ydx/router`），因此 `/ws`、`/wt` 只需注册一次；
WebTransport 的处理器在生产中必须阻塞到会话结束，返回即关闭会话。

## 10. 兼容性测试矩阵

`runtime/server/realtime_matrix_test.go` 在**同一个服务器**上验证：

| 用例 | 断言 |
|---|---|
| WebSocket HTTP/1.1 | 回显往返 |
| WebSocket HTTP/3 | 回显往返（quic-go 客户端 + RFC 9220） |
| WebTransport | 帧通道往返 |
| SSE | `data:` 帧与心跳头 |
| 普通 HTTP/3 GET | 与 WebTransport 共用 UDP 监听 |
| 跨源 WebSocket | 被 Origin 校验拒绝 |
| hub 扇出 × 4 传输 | 同一主题一次发布，四种连接各收到一次 |
| 优雅退出 | H1/H3 WebSocket、WebTransport 全部结束 |
| 多机扇出（ydx/realtime） | 两个节点经 miniredis 互达、恰好一次、本地回声被过滤 |
| 慢消费者 | 队列满时丢弃 / 关闭且发布方不阻塞 |
| 授权与限额 | 未授权主题被拒、超限消息在分配前被拒 |

运行：

```bash
go test ./runtime/server/ -run 'Realtime' -v     # 传输矩阵（yd）
go test ./... -run Realtime -v                   # ydx/realtime 单机与多机
node web/static/js/yd-realtime.test.mjs          # 浏览器客户端
```

## 11. 排错

| 现象 | 原因 | 处理 |
|---|---|---|
| H2 WebSocket 握手被 RST/1006 | 服务端未开 `extended_connect` 或进程缺 `GODEBUG=http2xconnect=1` | 打开配置；确认 `yd dev/service/docker` 或裸二进制重启注入 |
| Chrome/Firefox 1006，Safari 正常 | 边缘（CDN/LB）通告了 Extended CONNECT 但不转发 `:protocol` | 端到端支持，或在边缘关闭该 SETTINGS 让浏览器回退 H1 |
| WebTransport 连不上 | 未开 HTTP/3/TLS、UDP 被封、浏览器低于 Baseline | 检查 `server.http3.enabled/webtransport`、放行 UDP、降级到 WebSocket |
| `server: HTTP/3 WebTransport requires …` | 开了 webtransport 但没开 http3/TLS | 同时开启 `server.http3.enabled` 并配置证书 |
| SSE 卡住不刷新 | 反向代理缓冲 | 保留 `X-Accel-Buffering: no` 与 `Cache-Control: no-transform`（`sse` 已设置） |
| 多机只有本机收到 | 未 `Use(RedisBackend)`、频道前缀不一致或 Redis 不可达 | 检查装配与 `realtime.channel_prefix` |
| 慢客户端导致消息缺失 | `slow_policy: drop` | 需要每条必达时改 `close` 并让客户端重连 + 补拉 |
