# 流程引擎 | Workflow engine

> 适用范围：ydx 的 `workflow` 模块与 `yd new` 生成的项目。本文是流程引擎的唯一使用说明；
> 设计取舍与实施记录保存在框架仓库的开发笔记中，不随生成项目发布。
>
> `yd new` 会把本文与消息中心、附件、字典、缓存、Redis/asynq、实时传输、节点身份等关联指南
> 一并复制到生成项目的 `docs/` 目录，并生成 `docs/README.md` 索引。

流程引擎是一个**可独立使用**的 Go 模块：领域、持久化、通知、AI 决策与 BPMN 导入导出都在
`github.com/shibingli/ydx/workflow` 及其子包中，不依赖 yd 主仓、不依赖 HTTP 框架、不依赖任何具体 Web 页面。
yd 项目只是在它之上提供了页面、JSON API、权限守卫、配置装配与调度器接线。

## 1. 能力一览

- **BPMN 2.0 可执行子集**：开始/结束/终止结束事件、用户任务、服务任务、排他/并行/包容网关、
  事件网关、中间定时捕获事件与消息捕获事件、多令牌并行、顺序流条件、默认流、BPMN DI 坐标。
- **中国式审批语义**：或签、会签、票签（票数或百分比阈值）、顺序审批、动态加签/减签、转办、委派、
  签收、抢单、退回、撤回到开始、超时催办与催办上限。
- **多版本定义**：草稿可反复保存，发布后版本只读；实例绑定发起时发布的版本，升级定义不影响在途实例。
- **自动审批**：按分类、角色、用户、流程组、事件名或变量表达式匹配，可固定同意/拒绝，也可交给 AI 决策。
- **AI 决策可插拔**：OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 三种协议；
  未配置时完全不触网，低置信度自动退回人工审批。
- **通知抽象**：站内消息、日志、Webhook（企业微信/钉钉/飞书/通用）与 SMTP 邮件；通知经 outbox 落库，
  由调度器领取后投递，失败不阻塞审批事务。
- **流程自有组**：组拥有显式成员，并可关联一个 yd 角色；运行时按并集解析审批人。
- **单机与多机**：基于 ydx/gorm 支持的 MySQL、PostgreSQL、SQLite、达梦 DM8；行锁、条件领取与
  调度器抢占都走数据库，不依赖进程内状态。
- **完整 API**：门户 HTML/JSON API、后台 HTML/JSON API 共用同一份领域服务与租户边界。
- **可视化设计器**：同源、无第三方库的拖拽画布，支持节点/连线编辑、表单字段、自动规则与流程设置。

## 2. 模块与分层

| 层 | 位置 | 职责 |
|---|---|---|
| 领域 | `ydx/workflow` | 类型、哨兵、`Store` 接口、设计校验、表达式求值、引擎、审批动作、通知与调度 |
| 持久化 | `ydx/workflow/gormstore` | `yd_workflow_*` 表迁移与 `Store` 实现；方言行锁、条件抢占、候选索引 |
| BPMN | `ydx/workflow/bpmn_write.go` / `bpmn_parse.go` | BPMN 2.0 XML + DI 的写出与解析，Camunda 属性兼容 |
| 导入导出 | `ydx/workflow/bundle.go` | 流程 JSON 包、BPMN 导入、校验和与幂等导入 |
| 通知通道 | `ydx/workflow/notify` | Webhook（含钉钉 HMAC 签名）与 SMTP |
| AI | `ydx/workflow/ai` | 三种供应商协议与安全解析 |
| yd 装配 | `internal/service/workflow.go`（生成项目） | 配置读取、通知通道、AI、目录适配器、调度器 |
| 页面/API | `internal/controller/workflow_*.go`（生成项目） | 后台工作台、设计器、实例、组、门户任务与两套 JSON API |

`workflow` 领域包不依赖 GORM、HTTP、yd 或 ydx/gorm；GORM 只出现在 `gormstore` 子包，
服务端装配只在生成项目的 `internal/service` 与 `internal/controller` 中出现。

## 3. 核心概念

| 概念 | 说明 |
|---|---|
| `Definition` | 流程定义：租户内唯一的 `key`、名称、分类、图标与状态（draft/published/disabled） |
| `Version` | 版本快照：设计 JSON、派生 BPMN、校验和、发布说明；草稿可编辑，发布后只读 |
| `Design` | 可编辑图：`nodes`、`edges`、`forms`、`rules`、`settings` |
| `Instance` | 流程实例：标题、业务键、发起人、状态、结果、变量、开始/结束时间 |
| `Token` | 执行令牌：驱动并发分支、等待事件与顺序审批 |
| `Task` | 用户任务：节点、处理人、候选池、状态、截止时间、催办次数、委派来源 |
| `Event` | 实例事件：开始、任务创建/完成、审批、驳回、退回、撤回、终止、完成等 |
| `Comment` | 审批意见与备注 |
| `Notification` | outbox 通知：事件、接收人、通道、载荷与投递状态 |
| `Group` | 流程自有审批组：显式成员 + 可选关联 yd 角色 |

### 3.1 节点类型

| `type` | BPMN 对应 | 说明 |
|---|---|---|
| `start` | `startEvent` | 唯一入口；可以挂发起表单 |
| `end` | `endEvent` | 结束；`terminate=true` 时取消其它全部令牌 |
| `user` | `userTask` | 用户审批任务，支持指派、签收、完成方式、表单与自动规则 |
| `service` | `serviceTask` | 服务任务，领域内按自动完成处理（外部系统可在事件钩子里接入） |
| `exclusive` | `exclusiveGateway` | 排他网关：按出线顺序求值，选中第一条条件成立或默认流 |
| `parallel` | `parallelGateway` | 并行网关：所有出线同时激活，所有入线到达才继续 |
| `inclusive` | `inclusiveGateway` | 包容网关：激活全部条件成立的出线，聚合时等待已激活分支 |
| `catch` | `intermediateCatchEvent` | 中间捕获事件：定时器或命名消息信号 |
| `event` | `eventBasedGateway` | 事件网关：后续捕获事件先发生者获胜，其它等待被取消 |

节点还可以带 `due`、`remind_every`、`max_remind`、`on_reject`、`allow_auto`、`ai_prompt` 等扩展字段。
Camunda 命名空间里的 `camunda:assignee`、`camunda:candidateUsers`、`camunda:dueDate`、
`camunda:formKey` 等属性在导入时映射到对应的 YD 扩展字段。

### 3.2 指派与完成方式

指派类型 `assignee.type`：

- `initiator`：发起人本人；
- `user`：`values` 中的用户 ID 或登录名；
- `role`：`values` 中的 yd 角色名，角色成员即候选审批人；
- `group`：`values` 中的流程组 `key`，组长为显式成员与关联角色成员的并集。

签收方式 `assignee.claim`：

- `assigned`：直接指派给每个人；
- `claim`：先签收再处理；
- `grab`：进入候选池，先抢到者获得任务。

完成方式 `completion.mode`：

- `any`：或签，一人通过即通过；
- `all`：会签，全部存活审批人通过才通过；
- `vote`：票签，按 `threshold_type`（`count` 票数或 `percent` 百分比）与 `threshold` 阈值判定。

`completion.sequential=true` 时按顺序一次只激活一个任务；`reject_mode` 取 `any`（首个驳回即驳回）
或 `all`（全部存活票都驳回才驳回）。驳回去向由节点的 `on_reject` 决定：
`terminate`（终止实例）、`end`（按结束事件结束）、`return_initiator`（退回发起人）。

### 3.3 表单与变量

`forms` 以作用域为键：`"start"` 表示发起表单，节点 ID 表示该审批任务收集的字段。
字段类型闭集：`text`、`textarea`、`number`、`date`、`datetime`、`select`、`checkbox`。
提交值写入实例变量（`variables`），供网关条件、自动规则与后续表单使用。

表达式是**安全的自研递归下降求值器**，不是 JavaScript，也不使用 `eval`：

- 比较：`==`、`!=`、`>`、`>=`、`<`、`<=`；
- 字符串：`contains`、`startsWith`、`endsWith`；
- 集合：`in [...]`；
- 逻辑：`&&`、`||`、`!`，括号分组；
- 字面量：数字、字符串、布尔、`null`、列表。

求值有深度、令牌数与长度上限；不存在的变量按空值处理，排序错误会让条件求值**失败关闭**，
绝不会把错误当成通过。

### 3.4 自动审批规则

`rules` 在实例发起（`event=start`）或任务创建（`event=task`）时求值：

| `scope` | 匹配方式 |
|---|---|
| `category` | 实例分类在 `match` 中 |
| `role` | 发起人或审批人持有 `match` 中的任一角色 |
| `user` | 发起人或审批人在 `match` 中 |
| `group` | 发起人属于 `match` 中的任一流程组 |
| `event` | 触发事件名在 `match` 中 |
| `variable` | `condition` 对实例变量求值为真 |

命中规则按 `priority` 从大到小取第一条。`decision` 为 `approve` 或 `reject`；
`use_ai=true` 时改由 AI 决策器根据节点 `ai_prompt`、实例标题、变量与表单给出结论，
置信度低于 `workflow.ai.min_confidence` 时返回 `none`，节点保持人工审批。

## 4. 生命周期与状态

### 4.1 定义与版本

- 创建定义 → 创建草稿版本 → 设计器保存草稿 → 发布版本。
- 发布后定义状态变为 `published`，`current_version` 指向该版本；再次修改必须新建草稿。
- 删除已被实例引用的定义会被拒绝（`ErrDefinitionInUse`），不会级联删除历史实例。

### 4.2 实例

状态：`running` → `completed` / `rejected` / `withdrawn` / `terminated`。

- **撤回**：发起人在 `workflow.withdraw_window` 内可撤回运行中的实例；管理员不受窗口限制。
- **终止**：`workflow:admin` 可随时终止，所有未完成任务被取消并记录原因。
- **退回**：审批人可把流程退回到设计中的上游节点；只允许退回可达节点。
- **转办**：任务所有权永久转移给目标用户。
- **委派**：目标用户处理完成后，任务回到原处理人（`delegated_from`）。
- **加签/减签**：在当前节点增加或移除审批人；减签不能移除自己或最后一个存活审批人。

### 4.3 任务

状态：`pending`（未签收）→ `claimed`（已签收）→ `completed` / `cancelled`。
门户任务抽屉仍只对任务参与者或实例发起人可见；后台任务抽屉对持有 `workflow:read` 的账号可见，但动作表单只在持有 `workflow:admin` 时渲染。
所有动作都校验租户、参与者身份与任务状态，冲突返回 409，权限不足返回 403，
跨租户与不存在一律返回 404。

## 5. 数据表与索引

`gormstore.Migrate` 幂等创建以下表（全部以 `tenant_id` 打头建索引，不使用外键）：

| 表 | 用途 |
|---|---|
| `yd_workflow_definitions` | 定义：租户、key、名称、分类、图标、状态、当前版本 |
| `yd_workflow_versions` | 版本：设计 JSON、BPMN、校验和、状态、发布说明 |
| `yd_workflow_instances` | 实例：定义/版本、标题、业务键、发起人、状态、结果、变量、时间 |
| `yd_workflow_tokens` | 令牌：节点、状态、等待类型/截止时间/信号名、分支组 |
| `yd_workflow_tasks` | 任务：节点、处理人、候选、状态、截止、催办、委派来源 |
| `yd_workflow_task_candidates` | 候选索引：按 `(tenant_id, user_id, status)` 查询个人待办 |
| `yd_workflow_events` | 事件时间线 |
| `yd_workflow_comments` | 审批意见 |
| `yd_workflow_notifications` | 通知 outbox 与投递状态 |
| `yd_workflow_groups` | 流程组 |
| `yd_workflow_group_members` | 组成员（字符串主键，跨方言可移植） |

关系只保存标识（`definition_id`、`instance_id`、`task_id`、`group_id`），由服务层在事务内校验；
没有数据库级联。实例一经创建不会被删除：撤回与终止只改变状态，事件与评论继续保留供审计。

## 6. 多机部署与并发

- **无需进程内协调**：调度器、催办、通知投递都通过数据库条件更新领取任务（`ClaimReminder`、
  `ClaimNotification`、`ListDueTimerTokens`），多个副本可以同时运行。
- **行锁**：审批动作用 `SELECT ... FOR UPDATE`（由 ydx/gorm 的 `FeatureRowLock` 选择方言实现；
  SQLite 自动忽略）串行化同一实例的状态迁移。
- **SQLite**：必须使用 `_txlock=immediate`，不要加 `_time_format`（详见 `docs/gorm-dialects.md`）。
- **调度器承载**：生成项目的流程扫描任务挂载在进程唯一的共享 cron 调度器上（`internal/service/cron.go`），
  不创建私有的定时循环。共享调度器配置 Redis Locker 时，`workflow-scan` 带 `Singleton`，每个周期只有
  一个副本扫描；锁键为 `<cron.namespace>:lock:workflow-scan:<触发时刻>`。同一部署的所有副本必须共用
  同一个 `cron.namespace`，不同应用或环境共用同一套 Redis 时必须使用不同的值（见 [`cron.md`](cron.md)）。
- **调度间隔**：`workflow.scan_interval` 默认 30s；多副本同时扫描是安全的，只会有一个副本领取到同一条到期记录。
- **通知**：通知先写入 `yd_workflow_notifications`，提交后由调度器领取投递；进程崩溃不会丢失，
  重复投递由投递状态与领取者标记约束。
- **节点身份**：多副本的 `realtime.node_id` 与 `node.id` 必须唯一（见 `docs/node-identity.md`）。

## 7. 通知

`workflow.notify.channels` 选择启用的通道：

| 通道 | 说明 |
|---|---|
| `message` | 写站内消息（生成项目把它接到消息中心，接收人可在门户看到） |
| `log` | 写开发日志，适合本地调试 |
| `webhook` | 企业微信、钉钉、飞书或通用 JSON POST；钉钉使用 HMAC-SHA256 加签 |
| `smtp` | SMTP 邮件，支持 TLS 与 STARTTLS |

`workflow.notify.events` 过滤事件类型（默认全部）。通知正文中英文各一份，
语言由 `workflow.notify.lang` 决定，留空沿用 `i18n.default_lang`。
`workflow.notify.base_url` 用于生成深链，例如 `https://example.com`。

Webhook 地址与 SMTP 口令可能含凭据，**不得写入日志**；生产环境使用
`YD_WORKFLOW_NOTIFY_WEBHOOK_URL`、`YD_WORKFLOW_NOTIFY_WEBHOOK_SECRET`、
`YD_WORKFLOW_NOTIFY_SMTP_PASSWORD` 注入。

## 8. AI 决策

```yaml
workflow:
  ai:
    enabled: true
    provider: openai          # openai | openai-responses | anthropic
    base_url: https://api.openai.com/v1
    api_key: ""               # 用 YD_WORKFLOW_AI_API_KEY 注入
    model: gpt-4.1-mini
    max_tokens: 512
    api_version: "2023-06-01" # Anthropic 版本头
    timeout: 20s
    min_confidence: 0.7
```

- **未启用或缺少模型/地址时**：不创建决策器，规则中的 `use_ai` 自动退回人工审批，绝不触网。
- **事务外决策**：命中 `use_ai` 时引擎先回滚当前事务并释放行锁，再调用决策器，随后带着缓存答案
  重放事务；外部 HTTP 的 30 秒上限因此不会阻塞同实例或 SQLite 的其它写操作。决策输入在两次重放之间
  变化时会重新调用，单次操作最多 16 次决策。
- **端点归一化**：`openai` 使用 `/chat/completions`，`openai-responses` 使用 `/responses`，
  `anthropic` 使用 `/v1/messages`；`base_url` 可写根地址或完整端点。
- **安全**：只接受 JSON 响应中的 `decision` 与 `confidence`，未知值按 `none` 处理；
  API key 只放请求头，绝不进入日志或错误文案。
- **失败关闭**：HTTP 错误、超时、非法 JSON、低置信度都保持人工审批，不会自动通过。

## 9. 权限

| 能力 | 用途 |
|---|---|
| `workflow:use` | 门户发起流程、读取自己的实例 |
| `workflow:approve` | 处理指派给自己的任务 |
| `workflow:read` | 后台读取全部定义、版本、流程组、实例、任务与统计 |
| `workflow:manage` | 管理定义、设计、版本、流程组与导入 |
| `workflow:admin` | 管理任意实例与任务（撤回、终止、信号） |

内置角色绑定：`owner` 与 `admin` 拥有全部能力；`member` 拥有 `use` 与 `approve`；
`auditor` 只读 `workflow:read`。自定义角色可在「角色管理」中勾选。

前端按钮的显隐只是体验优化，**不是安全边界**；每个端点都由 `authz.Require` 在服务端校验。

## 10. 门户页面与后台页面

### 10.1 门户

- `/workflow/tasks`：待办、已办、我发起的三个页签；顶部「发起流程」打开抽屉选择已发布流程。
- 任务抽屉：同意/拒绝、签收、转办、委派、加签/减签、退回、催办与时间线。
- 实例抽屉：只读展示任务、变量与事件；发起人可在窗口内撤回。

### 10.2 后台

- `/mgr/workflows`：流程定义工作台；新建、编辑、启停、删除、导入、导出、版本、设计器。
- `/mgr/workflows/:id/design`：可视化设计器。
- `/mgr/workflow-groups`：流程组与成员管理。
- `/mgr/workflow-instances`：实例列表与详情；管理员可终止或投递信号。
- 列表统一支持 `q` 与 `status`，定义与实例另支持 `category`；筛选、页签与页码写入 URL，
  列表内变更后仍保持，不再被轮询或分页重置。

### 10.3 设计器

- 左侧节点面板可拖拽到画布，也可点击后放到默认位置。
- 拖动节点调整坐标；双击节点聚焦属性面板；`Delete` 删除选中项；`Ctrl/Cmd+S` 保存草稿。
- 「连线」进入连线模式：先点源节点，再点目标节点；再次点击退出。
- 右侧属性面板覆盖节点/连线字段、指派与完成方式、时限与催办、表单字段、自动规则与流程设置。
- 「校验」在本地检查开始/结束事件、节点名称、审批人、等待事件参数；服务端仍会做完整校验。
- 「保存」写入草稿；「发布」把当前草稿冻结为只读版本。
- 未保存时整页离开会被浏览器拦截；语言切换与局部导航会重绘工作区，请先「保存」草稿。

## 11. JSON API

后台 API 前缀 `/api/v1`，门户 API 前缀 `/api/portal/v1`。全部返回 JSON，错误使用
`{"error":"..."}` 结构；鉴权使用同一份会话 Cookie 或 `Authorization: Bearer`。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/workflows/definitions` | 列出定义 |
| POST | `/api/v1/workflows/definitions` | 创建定义 |
| GET/PUT/DELETE | `/api/v1/workflows/definitions/{id}` | 读取/更新/删除定义 |
| GET/POST | `/api/v1/workflows/definitions/{id}/versions` | 列出版本摘要/新建草稿 |
| GET/PUT/DELETE | `/api/v1/workflows/versions/{version_id}` | 读取/更新草稿/删除草稿 |
| POST | `/api/v1/workflows/versions/{version_id}/publish` | 发布版本 |
| GET | `/api/v1/workflows/export?format=json\|bpmn` | 导出流程包或 BPMN |
| POST | `/api/v1/workflows/import?format=json\|bpmn` | 导入 JSON 包（默认）或 BPMN 2.0 XML |
| GET/POST | `/api/v1/workflows/groups` | 列出/创建流程组 |
| GET/PUT/DELETE | `/api/v1/workflows/groups/{id}` | 读取/更新/删除流程组 |
| GET/PUT | `/api/v1/workflows/groups/{id}/members` | 读取/覆盖组成员 |
| GET | `/api/v1/workflows/stats` | 租户统计 |
| GET | `/api/v1/workflow-instances` | 列出实例 |
| GET | `/api/v1/workflow-instances/{id}` | 实例详情 |
| POST | `/api/v1/workflow-instances/{id}/withdraw` | 撤回 |
| POST | `/api/v1/workflow-instances/{id}/terminate` | 终止 |
| POST | `/api/v1/workflow-instances/{id}/signal` | 投递消息信号 |
| GET | `/api/v1/workflow-tasks` | 列出任务 |
| GET | `/api/v1/workflow-tasks/{id}` | 任务详情 |
| POST | `/api/v1/workflow-tasks/{id}/{action}` | 任务动作（claim/approve/transfer/delegate/add-sign/remove-sign/return/remind） |
| GET | `/api/portal/v1/workflows/definitions` | 门户可发起的流程目录 |
| GET/POST | `/api/portal/v1/workflows/instances` | 我发起的实例/发起流程 |
| GET | `/api/portal/v1/workflows/instances/{id}` | 我的实例详情 |
| POST | `/api/portal/v1/workflows/instances/{id}/withdraw` | 撤回自己的实例 |
| GET | `/api/portal/v1/workflows/tasks` | 我的任务 |
| GET | `/api/portal/v1/workflows/tasks/{id}` | 我的任务详情 |
| POST | `/api/portal/v1/workflows/tasks/{id}/{action}` | 处理我的任务 |

列表端点统一接受 `q`、`status`、`page`、`page_size`；定义与实例列表还接受 `category`，
任务列表还接受 `instance` 与 `tab`，与 HTML 工作台的筛选保持一致。实例列表只返回列表字段，
不携带 `variables` / `visited_nodes` / `current_nodes`；完整变量经实例详情读取。
版本列表同样只返回 `VersionSummary`（ID、版本号、状态、发布时间与说明），设计经单版本读取。

任务动作的正文形如：

```json
{ "action": "approve", "comment": "同意", "form_data": { "amount": 1200 } }
```

## 12. 导入导出

- **JSON 包**：`WorkflowBundle` 包含定义、版本与设计，带校验和；导入可选择覆盖或跳过已存在版本。
- **BPMN 2.0 XML**：带 BPMN DI 坐标；支持 `bpmn:` 标准元素与 Camunda 扩展属性；YD 扩展字段写在
  `extensionElements` 的同名 JSON 中，导入时可无损往返。
- **幂等**：同一校验和重复导入返回成功并标记跳过，不会产生重复版本。
- **冲突**：导入已发布版本的相同 key 会返回冲突；需显式选择新建定义或新版本。

## 13. 配置

所有配置键位于 `workflow:` 下，环境变量前缀 `YD_WORKFLOW_`：

| 键 | 默认 | 说明 |
|---|---|---|
| `withdraw_window` | `30m` | 发起人撤回窗口 |
| `default_due` | `72h` | 默认任务期限 |
| `default_remind_every` | `12h` | 默认催办间隔 |
| `max_remind` | `5` | 默认催办上限 |
| `scan_interval` | `30s` | 调度扫描间隔 |
| `page_size` | `20` | 列表默认页长 |
| `max_import_bytes` | `8388608` | 导入包大小上限 |
| `notify.channels` | `[message]` | 通知通道 |
| `notify.events` | 全部 | 通知事件过滤 |
| `notify.lang` | 空 | 通知语言 |
| `notify.base_url` | 空 | 深链前缀 |
| `ai.enabled` | `false` | 启用 AI 决策 |
| `ai.provider` | `openai` | `openai` / `openai-responses` / `anthropic` |
| `ai.min_confidence` | `0.7` | 低于该置信度保持人工 |

## 14. 故障语义

- **引擎不可用**：页面返回 503 并给出原因，后台导航仍可打开；调度器启动失败只告警，不阻塞应用启动。
- **审批冲突**：任务已被他人处理、已撤回或已终止时返回 409，客户端应刷新任务。
- **跨租户**：一律 404，不泄露资源是否存在。
- **权限不足**：403，不泄露资源是否存在。
- **通知失败**：只记录告警，不回滚审批；outbox 记录保留供重试。
- **AI 失败**：保持人工审批，绝不自动通过。
- **调度器 panic**：被 recover 并计数，进程继续运行；连续失败只记录日志，不退出。

## 14.1 运行期性能特征

- **已发布版本只解析一次**：进程内保留有界 FIFO 缓存（最多 128 个版本、约 16 MiB 估算字节），
  命中时不再读取设计/BPMN 列，也不再反序列化设计；草稿不入缓存，发布版本不可变，因此多节点下
  缓存无需失效广播。`Start` 仍每次实时查询「最新已发布版本指针」，其它节点发布后立即生效。
- **条件表达式编译一次**：边条件与变量规则共用一份最多 512 条的已编译 AST 缓存，求值不再重复
  词法分析与递归下降解析；缓存有界，不会随租户输入无限增长。
- **设计带只读索引**：解析时一次性建立节点表、出/入邻接与按优先级排序的规则列表；令牌推进只做
  哈希查找与切片遍历，不再线性扫描全部节点/连线，也不再每次复制并排序规则。
- **当前节点只扫等待令牌**：`refreshCurrentNodes` 经 `(tenant_id, instance_id, status)` 索引只取
  waiting 行，长流程积累的已完成令牌不再进入每次流转的扫描与分配。
- **单条任务不重复投影**：运行期与详情抽屉读取任务行后复用已加载实例，不再为填充实例标题/发起人
  多查一次；`GetTask` 对外仍返回完整投影。
- **普通动作不查定义行**：定义只在 AI 决策需要时惰性加载，其余流转使用实例与版本快照。
- **列表容量有界**：列表查询的初始容量上限为 32，空结果不再按请求上限（如 500 条评论）预分配。
- **版本列表只取摘要**：定义工作台、版本抽屉、设计器入口与 JSON API 版本列表只查询版本号/状态/说明，不再加载全部版本的设计与 BPMN；设计器只读取最近草稿或最新版本，单版本读取才返回完整设计。
- **定义计数批量**：定义列表的版本数与实例数经 `CountVersionsForDefinitions` /
  `CountInstancesForDefinitions` 两条分组查询一次填充，取代每个定义各查一次版本列表与实例计数
  （每页最多 2×N 次往返降为 2 次）；未实现该可选扩展的自定义 Store 自动回退旧路径。
- **实例列表精简投影**：列表不读取 `variables` / `visited_nodes` / `current_nodes`，响应大小由
  页长而非实例携带过的最大变量决定；完整载荷只在实例详情返回。
- **统计一次聚合**：概览的每张表各用一条 `COUNT(CASE WHEN …)` 查询，取代按状态逐条 `COUNT`
  （定义 2→1、实例 5→1、任务待办 1 条、逾期 1 条）。
- **AI 决策不持锁**：决策器在事务回滚之后调用，拿到答案再重放事务；决策输入指纹不含设计与 BPMN
  串，单次操作最多 16 次决策。

## 15. 测试与验证

```bash
# ydx 模块
cd ydx/workflow
go test ./... -race

# 生成项目
cd <project>
go build ./... && go vet ./... && go test ./... -race
```

覆盖范围包括：设计校验、表达式求值、BPMN 往返、或签/会签/票签/顺序审批、加签/减签、转办/委派、
退回/撤回/终止、定时器与消息事件、事件网关、自动规则、AI 三种协议、Webhook 签名、SMTP 消息构建、
并发抢单、多副本调度、租户隔离与导入导出。

## 16. 常见问题

**发起页看不到流程？** 定义必须处于 `published` 状态且有已发布版本；`workflow:use` 与
`workflow.available` 都要为真。

**设计器保存失败？** 先点「校验」查看本地问题；服务端会返回具体节点与字段。发布后版本只读，
需要新建草稿再改。

**审批人收不到待办？** 检查指派类型与值：`user` 用用户 ID，`role` 用角色名，`group` 用流程组 key；
角色成员变化后新任务按最新成员解析，已创建任务不会自动改派。

**多副本重复催办？** 不会。催办由 `ClaimReminder` 条件更新领取，同一时间只有一台机器能推进
`next_remind_at`；若见重复，先确认所有副本使用同一个数据库与同一个 `workflow` 配置。

**SQLite 报 `SQLITE_BUSY_SNAPSHOT`？** 连接串必须保留 `_txlock=immediate`，且不得加入
`_time_format`；详见 `docs/gorm-dialects.md`。

**AI 一直不生效？** `ai.enabled=true`、`base_url`、`model` 与 `api_key` 缺一不可；
规则需要 `use_ai=true` 且节点 `allow_auto=true`。置信度低于阈值会保持人工。
