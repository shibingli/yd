# 站内消息中心 | In-app message centre

> 适用范围：yd 框架与 `yd new` 生成的项目。本文是消息中心的唯一使用说明；
> 设计取舍与实施记录保存在框架仓库的开发笔记中，不随生成项目发布。

## 1. 能力一览

- **好友全生命周期**：同租户用户搜索、申请、接受/拒绝/取消、删除、拉黑/解除、备注。
- **单聊与群聊**：单聊仅限已接受好友；群聊从好友中创建，群主可改名、增删成员、解散，成员可退出。
- **系统公告**：仅 `message:system` 可发布与撤销；撤销保留审计历史；置顶支持立即/未来开始与可选结束时间。
- **消息生命周期**：发送、编辑（窗口内）、撤回（窗口内；管理员不受窗口限制）、软墓碑、已读/未读。
- **富文本**：Markdown 源 + 服务端 goldmark 渲染并净化的 HTML + 纯文本投影；原始 HTML 与危险链接被移除。
- **附件**：复用框架级 `attachments` 域，先上传后挂载；服务端嗅探 MIME、限制大小与类型；下载按消息可见性鉴权。完整能力见 `docs/attachments.md`。
- **分类与标签**：管理员维护消息类型、分组与标签；消息可带多个分组与多个标签。
- **检索**：标题、正文、发送人、类型、分组/标签的混合检索；所有查询以租户为边界。
- **即时通讯界面**：左侧会话列表、右上历史消息、右下内联撰写区；后台治理与目录设置保持列表式管理页。
- **实时**：`/ws/messages` 推送消息、好友、撤回与置顶事件；客户端也可经同一条连接发送快捷消息。
- **JSON API**：成员侧 `/api/portal/v1/messages/...` 与 `/api/portal/v1/friends/...`，
  治理侧（公告、撤回、置顶与目录）`/api/v1/...`；两者提供离线/脚本访问。
- **单/多节点**：单节点用进程内 Hub；多节点用 Redis Pub/Sub，失败广播进入 asynq 持久重试。

## 2. 包与分层

| 层 | 位置 | 职责 |
|---|---|---|
| 领域 | `messaging/` | 类型、哨兵、Store 接口、业务规则、事件 |
| 持久化 | `messaging/gormstore/` | 11 张 `yd_msg_*` 表的 AutoMigrate 与 Store 实现 |
| 用户目录 | `messaging/authdirectory/` | 把 `security/auth.Service` 适配为 `messaging.Directory` |
| Markdown | `messaging/markdown/` | goldmark 安全渲染与纯文本抽取 |
| 附件领域 | `attachments/`、`attachments/gormstore/` | 通用上传、引用、生命周期与两种字节后端 |
| 附件适配 | `messaging/attachments.go` | 消息可见性下载、待挂载校验 |
| 实时 | `messaging/realtime/` | 进程内 Hub、Redis Bus、重试接口 |
| 项目接线 | `internal/service/message.go`、`attachment.go`、`message_bus.go` | 配置、目录、附件门面与删除通知、Hub、asynq |
| 页面/API | `internal/controller/message_*`、`friend_controller.go` | HTML、片段、JSON API、WebSocket |

领域包不依赖 GORM 与 HTTP；持久化与用户目录都是接口实现，替换实现不影响规则。

## 3. 数据表

所有表都以 `tenant_id` 为第一列/第一条件。

| 表 | 用途 |
|---|---|
| `yd_msg_types` | 消息类型（内置 announcement/normal/system/finance/marketing） |
| `yd_msg_tags` | `kind=group` 或 `kind=tag` 的分组与标签 |
| `yd_msg_conversations` | direct/group/system/notice 会话；`notice_key` 为用户私有通知频道 |
| `yd_msg_members` | 会话成员与 `last_read_at` 已读水位 |
| `yd_msg_messages` | 消息、Markdown/HTML/纯文本快照、`search_text`、置顶与撤回字段 |

`yd_msg_attachments` 是升级前的旧表：`MigrateLegacy` 一次性复制到新表，复制完成后 `DropLegacy`
删除它；新代码不再读写旧表。
| `yd_msg_message_tags` | 消息-分组/标签连接 |
| `yd_attachments` | 通用附件元数据（租户、上传者、状态、墓碑） |
| `yd_attachment_refs` | 业务对象引用（`ref_type`/`ref_id`，消息为 `message`） |
| `yd_msg_friend_requests` | 有向好友申请状态机 |
| `yd_msg_friendships` | 规范化 ID 顺序的无向好友关系 |
| `yd_msg_friend_notes` | 每个所有者自己的好友备注 |
| `yd_msg_blocks` | 有向拉黑关系 |
| `yd_msg_audit` | 管理员撤回/撤销/置顶/编辑审计 |

## 4. 权限

新增能力（`security/authz`）：

| 能力 | 含义 | owner/admin | auditor | member |
|---|---|---|---|---|
| `message:read` | 使用消息中心、读取可见消息 | ✓ | ✓ | ✓ |
| `message:send` | 单聊/群聊发送 | ✓ | | ✓ |
| `message:update` | 编辑自己的消息（窗口内） | ✓ | | ✓ |
| `message:delete` | 撤回自己的消息（窗口内） | ✓ | | ✓ |
| `message:system` | 发布/撤销系统公告 | ✓ | | |
| `message:manage` | 治理全部消息、置顶、审计 | ✓ | | |
| `message-type:read` | 读取类型/分组/标签目录 | ✓ | ✓ | |
| `message-type:manage` | 类型/分组/标签生命周期 | ✓ | | |

`message:update` / `message:delete` 的作用域是“本人消息”，因此普通成员可以持有；
服务端仍然强制发送者身份与时间窗口。前端显隐只是体验优化，不是安全边界。

## 5. 配置

`configs/config.yaml`：

```yaml
message:
  recall_window: 5m
  edit_window: 5m
  max_attachments: 10
  max_attachment_size: 20971520
  max_group_members: 50
  page_size: 20
  attachment_dir: data/attachments
  attachment_store: local          # local | redis
  attachment_redis_prefix: "yd:msg:blob:"
  pending_ttl: 24h
  node_id: ""
  # allowed_types:
  #   - image/png
  #   - application/pdf
```

附件字节按 `<租户哈希>/<上传者哈希>/<YYYY>/<MM>/<DD>/<附件 ID>` 分区，两种后端使用同一套键。
这些 `message.attachment_*` 键现在配置的是**框架级附件存储**，不再专属于消息中心：

- `attachment_store: local`（默认）写 `attachment_dir`。单节点可直接使用相对目录；多节点必须把
  `attachment_dir` 指向每个节点都挂载的共享目录（NFS / SMB / 云文件系统），并使用绝对路径。
- `attachment_store: redis` 写部署共用的 Redis，多节点无需共享文件系统；`attachment_redis_prefix`
  在同一部署内必须唯一。Redis 不可达或前缀非法时上传被禁用并告警，绝不静默退回本地目录。
  该模式把附件字节放在 Redis 内存中，必须按 `max_attachment_size` × 并发上传量预留内存，并设置
  Redis 的 `maxmemory-policy noeviction`（附件不能像缓存一样被淘汰）；大文件部署优先使用共享目录。
- 两种后端都实现 `attachments.BlobStore`，空键、父目录穿越与不安全字符在存储层再次拒绝。
- 完整生命周期、权限、API、清理与迁移见 `docs/attachments.md`。

环境变量前缀 `YD_MESSAGE_*`（例如 `YD_MESSAGE_RECALL_WINDOW=10m`、`YD_MESSAGE_NODE_ID=node-1`）。

### 单节点

生成项目默认内建 Redis 与 asynq；本机没有 Redis 时启动会告警并退回进程内 Hub，
消息、好友与推送仍在本进程内完成。

### 多节点（Redis + asynq，默认内建）

> **默认内建**：`yd new` 生成的项目始终带 `redis`（Pub/Sub 广播）与 `asynq`（持久重试）接线，
> 不是生成期可选项。Redis 不可达时明确退回单节点并告警，不会阻塞启动。

```yaml
redis:
  mode: single
  addr: 127.0.0.1:6379
  password: ""
  db: 0

asynq:
  queues: "critical:3,default:2,low:1"
  concurrency: 10
```

- 每个节点启动时创建 `realtime.RedisBus`，订阅 `yd:message:events`。
- 本节点先本地投递，再发布到 Redis；其它节点收到后跳过 `Origin` 等于自己的回传。
- 发布失败时，若 asynq 可用，事件进入 `yd:message:event` 持久任务；任一节点处理任务后重新发布。
- Redis 启动探测失败时明确退回单节点并告警；不会阻塞应用启动。
- 多副本必须为每个副本设置唯一的 `node.id`（`message.node_id` 是兼容别名）；身份文件与
  容器/ Kubernetes 部署方式见 [`docs/node-identity.md`](node-identity.md)。
- 这里的组合就是框架的默认分区：**实时扇出 = Pub/Sub，广播失败补发 = asynq，业务事实 = 数据库**。
  什么时候该用直接 Redis 命令、什么时候必须改用持久队列，以及当前的可靠性边界（提交后发布
  没有 outbox，极端情况下会丢一次实时通知，但数据不丢）见
  [`docs/redis-asynq.md`](redis-asynq.md)。

## 6. HTTP 与 WebSocket

### 页面

| 路径 | 说明 |
|---|---|
| `GET /messages` | 消息中心（左侧会话/联系人双页签与检索/置顶入口；右侧历史与撰写区） |
| `GET /messages/:id` | 打开会话 |
| `GET /messages/list` | 检索结果片段 |
| `GET /messages/conversations` | 会话列表片段 |
| `GET /messages/:id/thread` | 完整线程片段 |
| `GET /messages/:id/history` | 仅历史消息片段（实时刷新用，不重建撰写区；`?after=<message id>` 只返回更新的消息，供客户端 `beforeend` 追加；锚点失效或落后超过 50 条时返回整段并带 `HX-Reswap: innerMorph`） |
| `GET /messages/:id/edit` | 编辑消息抽屉 |
| `GET /mgr/messages/system/new` | 系统公告抽屉 |
| `GET /messages/groups/:id/settings` | 群设置抽屉 |
| `GET /mgr/messages/:id/pin` | 置顶窗口抽屉 |
| `GET /mgr/messages/:id/edit` | 管理端编辑抽屉 |
| `POST /mgr/messages/:id` | 管理端保存编辑并刷新治理列表 |
| `POST /mgr/messages/:id/recall` | 管理端撤回并刷新治理列表 |
| `GET /friends` | 好友页 |
| `GET /mgr/message-settings` | 消息类型/分组/标签设置 |
| `GET /mgr/messages` | 管理员治理 |
| `GET /mgr/messages/audit` | 审计 |

### JSON API

- `GET /api/portal/v1/messages`、`GET /api/portal/v1/messages/:id`
- `POST /api/portal/v1/messages`、`POST /api/v1/messages/system`
- `POST /api/portal/v1/messages/:id`（编辑）、`/:id/recall`、`/:id/read`；
  `POST /api/v1/messages/:id/revoke|pin|unpin`（治理）
- `GET/POST /api/v1/message-types`、`/api/v1/message-tags`
- `GET /api/portal/v1/friends`、`GET /api/portal/v1/friends/search`
- `POST /api/portal/v1/friends/requests`、`/:id/accept|reject|cancel`
- `POST /api/portal/v1/friends/:id/remove|block|unblock|remark`
- `POST /api/portal/v1/messages/attachments`、`GET /api/portal/v1/messages/attachments/:id`
- 通用附件 API（我的附件、后台治理与清理）见 `docs/attachments.md`，与上表共用同一份附件服务。

请求与响应使用框架 `json`，错误为 `{"error":"..."}`；状态码由哨兵映射（404/403/409/413/415/422/503）。

### WebSocket 协议

`GET /ws/messages`（htmx 子协议，同源会话）：

- 服务端下行：
  - 裸 `<hx-partial>` 片段：未读徽标、实时通知。
  - JSON 事件：`{"yd_event":"message.created","conversation_id":"...","message_id":"...","actor_id":"...","at":"..."}`。
    `yd-app.js` 把它转成 DOM 事件 `yd:realtime`，`yd-message.js` 据此重取当前线程/会话列表/好友面板。
  - 已读事件（`message.read`）同时发给读者本人：读者自己的连接据此刷新用户名角标与菜单计数，
    全部读完后无需刷新页面即可隐藏。
- 客户端上行（扁平 JSON）：
  - `{"action":"send","conversation_id":"cv_...","body":"..."}` 快捷回复。
  - `{"action":"read","conversation_id":"cv_..."}` 标记已读。
  - `{"action":"ping"}` 保活。
  - 其它内容忽略，不回显。

### 通知频道

管理员删除附件时，生成项目经 `AttachmentService` 调用 `messaging.NotifyUser`，在收件人的私有通知频道写入一条
系统消息。频道是 `kind=notice` 的会话，`notice_key = notice:<user id>` 在租户内唯一：

- 只有收件人是成员；管理端消息列表按 `kind` 排除通知会话，普通检索也不会返回。
- 收件人可以阅读并标记已读，但 `Send` 对该会话返回 `ErrSystemOnly`，撰写区显示只读提示。
- 通知正文为 Markdown（管理员、文件名、大小、原因、UTC 时间），与普通消息一样经 goldmark 净化。
- 通知在删除事务提交后发送；发送失败只记 Warn，不回滚删除。

## 7. 搜索语义

`search_text` 在发送/编辑时生成：小写拼接标题、纯文本正文、发送人快照、类型中英文名、分组/标签中英文名与会话标题。
检索按空白分词，每个词都必须命中（AND），使用 `LIKE` 子串匹配，通配符 `%`、`_`、`!` 由 `!` 转义。
这是 SQLite/MySQL/PostgreSQL 行为一致的便携实现；数据量很大时可替换 `Store.ListMessages` 的检索实现，
领域接口与页面不受影响。

## 8. 安全与边界

- **租户硬边界**：所有 Store 方法以 `tenant_id` 为第一条件；跨租户 ID 与不存在 ID 一样返回 404。
- **非成员不可见**：普通用户只能读取自己所属会话；管理员 `message:manage` 可治理全租户消息。
- **好友规则**：单聊要求已接受好友且双方未拉黑；群发要求所有成员都是发起人的好友。
- **好友申请**：检索排除当前用户；已是好友、已有待处理申请、拉黑关系与自己都不会渲染申请表单，
  抽屉会再次复核关系，直接打开旧链接也不能绕过。
- **系统公告**：只有 `message:system` 可发布/撤销；置顶只有 `message:manage` 且仅系统来源消息。
- **撤回**：正文保留在库中供审计；对无 `message:manage` 的读者显示墓碑并清空正文/附件/标题。
  撤回**不释放附件引用**：附件仍属于原消息；只有消息被硬删除或附件字段被清空才调用 `Release`。
- **富文本**：goldmark 默认 HTML 渲染器省略原始 HTML，危险 URL 协议被拒绝；模板以 `template.HTML` 输出仅限该净化结果。
- **附件**：服务端嗅探 MIME，默认白名单不含 HTML/SVG/脚本；下载使用 `Content-Disposition: attachment` 与 `nosniff`。
  图片附件在消息卡片与撰写区显示缩略图，并以 `?inline=1` 内联预览（仅 `image/*` 生效，其它类型始终下载）；
  未挂载上传由附件域的 janitor 按 `pending_ttl` 清理；管理员删除附件后保留引用行，消息按墓碑渲染。
- **实时事件**：事件只携带 ID 与小型元数据，不携带正文；接收方按权限重新读取。
- **限流**：建议在反向代理或 `middleware.RateLimit` 上对发送/好友申请按用户限流；消息中心本身不信任 `X-Forwarded-For`。

## 9. 前端组件

消息中心是**即时通讯布局**，不是后台列表页：左侧固定为会话栏（「会话 / 联系人」双页签，含检索/置顶入口），
右侧上方是当前会话的历史消息，右侧下方是内联撰写区；滚动只发生在会话栏与历史区内部。
后台治理（`/mgr/messages`、`/mgr/message-settings`）保持列表式管理页，两者职责不混。

- 新建会话：在「联系人」页签内就地完成，不打开抽屉、对话框或第二层页面。点好友整行直接创建/打开单聊；
  勾选至少两位好友后，底部出现群名与「创建群聊」，提交 `/messages/groups`。搜索按姓名与登录名过滤，
  右侧当前会话与草稿保持不变。
- 编辑器：EasyMDE 2.20（Markdown + 预览 + 全屏/并排），本地 vendored；关闭 `autoDownloadFontAwesome`，
  工具栏图标全部来自 Lucide 按需 sprite（`partials/editor-icons.html` + `yd-ui.js` 装饰），
  不加载任何图标字体，也无 CDN。内联撰写区使用紧凑工具栏（`data-yd-editor-compact`），
  完整编辑器仍用于编辑消息与系统公告的抽屉。
- 发送：日常消息直接在内联撰写区提交 `/messages`，**不弹确认**；公告频道提交 `/mgr/messages/system`，
  保留一次 `data-yd-confirm`。`Ctrl/Cmd+Enter` 发送，普通回车在 EasyMDE 中仍用于换行。
- 抽屉：编辑消息、置顶窗口与群设置继续走右侧抽屉；撤回/撤销/退出/解散/好友变更全部走
  `data-yd-confirm`。
- 用户菜单：右上角用户名即菜单入口，头像角标显示未读消息数（99+ 封顶，WebSocket 实时刷新）；
  菜单两项——「未读消息」进入 `/messages?folder=unread`，「我的资料」进入 `/account`。
  菜单只在账号具备 `message:read` 且消息中心可用时显示未读入口，退出登录仍是独立按钮。
- 实时：`#yd-shell` 持有唯一 WebSocket 连接；导航或租户切换会重建外壳，连接随之重连并使用新令牌。
  新消息事件只带 `?after=<最后一条消息 ID>` 重取 `/messages/:id/history`，并以 `beforeend` 追加进历史区；
  编辑、撤回、置顶与已读回执仍走整段 `innerMorph`。撰写区、草稿与附件标签因此不会被重建。
- 检索：检索框只换 `#yd-msg-main`，服务端按 `HX-Target` 只渲染结果片段；会话栏、置顶条与联系人面板不参与。
  输入期间使用 `hx-replace-url`，不会为每个停顿写一条历史记录；清空检索时服务端以 `HX-Retarget` 退回工作区。
- 编辑器：EasyMDE（约 320 KB）不再随消息页自动加载；`[data-yd-editor]` 上的
  `data-yd-editor-vendor` 声明同源地址，首次聚焦或打开抽屉时由 `yd-message-editor.js` 加载，
  期间 textarea 回退仍可输入。
- 附件：内联撰写区的上传表单只把附件标签追加进主表单，隐藏字段随消息一起提交；抽屉内的上传承袭
  `data-yd-drawer-persist`，成功后不关闭抽屉。
- 窄屏：会话列表与聊天区切换显示，聊天页头部提供返回会话列表的入口。

## 10. 验证

```bash
go build ./... && go vet ./... && go test ./... -race
go test -tags e2e ./e2e/ -v -timeout 30m
yd icons check
yd assets minify --check
yd versions check
```
