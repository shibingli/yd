# 通用附件能力 | Generic attachments

> 适用范围：yd 框架与 `yd new` 生成的项目。本文是附件能力的唯一使用说明；
> 设计取舍与实施记录保存在框架仓库的开发笔记中，不随生成项目发布。
>
> `yd new` 会把本文与消息中心、缓存、Redis/asynq、实时传输、节点身份等关联指南一并复制到生成项目的
> `docs/` 目录，并生成 `docs/README.md` 索引；生成项目不依赖公网链接即可查阅完整说明。

附件不是消息中心的附属物：任何功能都可以上传文件，用 `(ref_type, ref_id)` 绑定到自己的业务对象，
在对象删除或字段清空时解除引用。消息中心只是第一个消费方，`ref_type` 为 `message`。

## 1. 能力一览

- **通用上传**：先上传得到 `pending` 记录，再由业务在自己的事务里绑定；可绑定到任意业务类型。
- **两种字节后端**：本地目录（单节点，或多节点共享挂载）与 Redis（多节点无需共享文件系统）。
- **生命周期**：`pending` → `referenced`；最后一个引用释放后回到 `pending` 并重置保留时长；
  到期未引用、用户自删或管理员删除后进入 `deleted` 墓碑。墓碑保留元数据供审计与在原文位置渲染占位。
- **租户/用户/日期分区**：存储键只含不透明标识，按租户哈希、上传者哈希与 UTC 日期分区，不含原始文件名或明文用户 ID。
- **服务端内容判定**：大小在读取前后各校验一次，MIME 由字节嗅探得出，绝不采信客户端 `Content-Type`；
  即使运维把 SVG 加入允许列表，它也只会被下载，绝不以 `inline` 渲染。
- **门户自助**：用户查看、下载、筛选并删除自己**未被引用**的附件，也可在附件页直接上传。
- **后台治理**：管理员/审计员查看租户内全部附件，按上传者、状态、是否被引用与日期筛选；
  管理员可带原因删除单条，或集中清理全部未引用记录；审计员只读。
- **删除通知**：管理员删除后，上传者在消息中心的**私有通知频道**收到带原因的站内通知；
  批量清理按上传者各发一条汇总通知（含原因与至多十个文件名）。
- **墓碑不破坏业务**：管理员删除已被引用的附件时保留引用行，消息或其它业务按墓碑渲染，不出现坏链接。
- **可复用 API**：HTML 页面、门户 JSON API 与管理 JSON API 共用同一份领域服务。

## 2. 包与分层

| 层 | 位置 | 职责 |
|---|---|---|
| 领域 | `attachments/` | 类型、哨兵、`Store`/`BlobStore` 接口、生命周期规则、上传/下载/清理 |
| 持久化 | `attachments/gormstore/` | `yd_attachments`、`yd_attachment_refs` 的迁移与 `Store` 实现；旧表迁移 |
| 本地字节 | `attachments/filestore/` | 本地不透明键存储（原子写、路径校验） |
| Redis 字节 | `attachments/redisstore/` | Redis 不透明键存储（键前缀校验） |
| 消息中心适配 | `messaging/attachments.go` | 上传、按消息可见性下载、待挂载校验 |
| 删除通知接线 | `internal/service/attachment.go`（生成项目） | 领域服务 + 消息中心通知器 + 后台清理协程 |
| 页面/API | `internal/controller/attachment_controller.go`（生成项目） | 门户、后台、两套 JSON API |

`attachments` 包不依赖 `messaging`、GORM 或 HTTP；`messaging` 通过 `messaging.AttachmentService`
接口使用它，生成项目在 `internal/controller` 用闭包把两者接在一起。

## 3. 数据表与索引

| 表 | 用途 |
|---|---|
| `yd_attachments` | 附件元数据：租户、上传者、原始文件名、不透明存储键、嗅探 MIME、SHA-256、状态、大小、删除信息 |
| `yd_attachment_refs` | 引用表：`(tenant_id, attachment_id, ref_type, ref_id)` 唯一 |

索引一律以 `tenant_id` 打头：

- `yd_attachments`：`(tenant_id, id)` 唯一、`(tenant_id, uploader_id)`、`(tenant_id, created_at)`、
  `(tenant_id, status, created_at)`；`id` 本身唯一。
- `yd_attachment_refs`：`(tenant_id, attachment_id, ref_type, ref_id)` 唯一、`(tenant_id, attachment_id)`、
  `(tenant_id, ref_type, ref_id, created_at)`。

**没有外键**：引用完整性由服务层在同一个事务里维护（锁行、复查、按显式顺序清理），与本仓其它表一致。
`yd_msg_attachments` 是 2026-10 之前的旧表：升级时由 `MigrateLegacy` 一次性复制到新表，
随后 `DropLegacy` 删除旧表；新代码只读写新表。

## 4. 生命周期

```
upload ──► pending ──Bind──► referenced ──Release(最后一个引用)──► pending(released_at)
              │                  │                                     │
              │ 用户自删/到期     │ 管理员删除                           │ 到期
              └──────────────► deleted ◄───────────────────────────────┘
                                 │
                                 └─ 保留引用行；业务渲染墓碑
```

- **upload**：写入字节后写元数据；元数据失败会删除已写字节。
- **Bind**：业务在自己的事务里引用附件；附件域同时提供 `Service.Bind`，但业务通常直接在自己的
  存储事务里锁 `yd_attachments` 并写 `yd_attachment_refs`（消息中心即如此），以保证“业务对象与引用一起提交”。
- **Release**：删除某个业务对象的全部引用；无剩余引用的附件回到 `pending`，`released_at` 重置保留时长。
  **消息撤回不释放引用**：撤回是内容墓碑，附件仍属于原消息；只有业务对象被硬删除或字段清空才 Release。
- **用户自删**：只允许 `pending` 且没有任何引用的附件；已被引用返回 `ErrReferenced`（409）。
- **管理员删除**：允许删除任意存活附件，必须填写原因；先提交墓碑再尽力删除字节，
  字节删除失败只告警，不会重新放行访问。已被引用的附件保留引用行，业务按墓碑渲染。
- **到期清理**：后台协程每小时把 `pending` 且 `COALESCE(released_at, created_at)` 早于 `pending_ttl`
  的记录变成墓碑；批量按 200 条一批，跨租户执行，只由后台入口调用。

状态语义：

| 状态 | 含义 | 字节 | 可下载 |
|---|---|---|---|
| `pending` | 已上传，尚无任何业务引用 | 有 | 上传者/管理员/审计员 |
| `referenced` | 至少一个业务引用 | 有 | 按业务可见性；管理面按 `attachment:read` |
| `deleted` | 墓碑 | 无 | 否 |

## 5. 存储后端与多节点

附件字节按 `<租户哈希>/<上传者哈希>/<YYYY>/<MM>/<DD>/<附件 ID>` 分区；哈希为 SHA-256 前 16 位十六进制，
原始文件名与明文用户 ID 不进入键。两种后端使用同一套键，切换后端不会改变元数据。

```yaml
message:
  attachment_dir: data/attachments
  attachment_store: local          # local | redis
  attachment_redis_prefix: "yd:msg:blob:"
  max_attachment_size: 20971520    # 20 MiB
  pending_ttl: 24h
  # allowed_types:
  #   - image/png
  #   - application/pdf
```

> 这些键历史上属于消息中心，现在配置的是**框架级附件存储**；保留键名是为了让既有项目的文件仍然可达。

- **单节点 + local**：写 `attachment_dir`（相对路径按进程工作目录解析）。默认即此模式。
- **多节点 + local**：`attachment_dir` 必须是所有节点挂载的同一共享目录（NFS / SMB / 云文件系统），
  使用绝对路径。否则每个节点只看到自己写入的文件，下载会随机 404。
- **多节点 + redis**：所有节点从部署共用的 Redis 读取同一份字节，不需要共享文件系统；
  `attachment_redis_prefix` 在同一部署内必须唯一。Redis 内存必须按
  `max_attachment_size × 并发上传量` 预留，并设置 `maxmemory-policy noeviction`；
  大文件部署优先使用共享目录。
- **后端无法构建**：配置的后端不可用时附件能力整体禁用（页面显示明确提示），
  绝不静默退回本地目录——那会让其它节点看不到新文件。消息与其它功能不受影响。

配置优先级仍是“命令行 > 环境变量 > 配置文件 > 框架默认值”，环境变量示例：
`YD_MESSAGE_ATTACHMENT_STORE=redis`、`YD_MESSAGE_ATTACHMENT_DIR=/srv/yd/attachments`。

本地后端使用原子写（临时文件 + rename）并拒绝空键、绝对路径、`..` 与不安全字符；
Redis 后端在写入前再次校验键前缀与非法字符。

## 6. 权限

新增能力（`security/authz` 注册表）：

| 能力 | 含义 | owner/admin | auditor | member |
|---|---|---|---|---|
| `attachment:create` | 上传附件 | ✓ | | ✓ |
| `attachment:read_own` | 读取/下载自己的附件 | ✓ | | ✓ |
| `attachment:delete` | 删除自己**未被引用**的待引用附件 | ✓ | | ✓ |
| `attachment:read` | 只读访问租户内全部附件 | ✓ | ✓ | |
| `attachment:manage` | 删除任意附件、集中清理未引用附件 | ✓ | | |

- 门户页面与成员 API 用 `attachment:read_own`；后台页面与管理 API 用 `attachment:read`；
  删除单条与清理用 `attachment:manage`；用户自删用 `attachment:delete`。
- 服务端仍会校验上传者、状态与引用数；路由守卫只是第一道门。**前端显隐不是安全边界**。
- 审计员只读：能看到后台附件列表与下载，但页面不渲染删除/清理控件，
  即使手改 DOM 也会被 `attachment:manage` 守卫拒绝。
- 跨租户与不存在的记录一律返回 404，不泄露存在性。

## 7. 页面

| 门户 | 后台 |
|---|---|
| `GET /attachments` 我的附件：上传、关键字/状态/日期筛选、分页、下载、删除未引用行 | `GET /mgr/attachments` 附件管理：按上传者/状态/引用状态/日期筛选、下载、删除抽屉、清理抽屉 |
| `POST /attachments` 上传 | `GET /mgr/attachments/{id}/delete` 删除原因抽屉 |
| `GET /attachments/{id}/download` 下载（`?inline=1` 仅图片内联） | `POST /mgr/attachments/{id}/delete` 带原因删除 |
| `POST /attachments/{id}/delete` 自删未引用附件 | `GET/POST /mgr/attachments/cleanup` 清理全部未引用 |

所有写动作都带 `data-yd-confirm`，统一走全局确认框；新增/编辑仍遵循“抽屉”约定。
下载响应带 `Content-Disposition`、`X-Content-Type-Options: nosniff` 与
`Cache-Control: private, no-store`；非图片（以及 SVG，即使被加入白名单）永远不会被内联渲染。
页面/API 的“已引用/未引用”筛选与“集中清理”共用同一套 SQL 条件，清理会强制
`NOT EXISTS (yd_attachment_refs)`，因此**仍被业务引用的文件永远不会被批量清理**。

## 8. JSON API

成员 API（前缀 `/api/portal/v1/attachments`）：

| 方法 | 路径 | 能力 | 说明 |
|---|---|---|---|
| GET | `/api/portal/v1/attachments` | `attachment:read_own` | 分页、`q`/`status`/`from`/`to` 筛选 |
| POST | `/api/portal/v1/attachments` | `attachment:create` | `multipart/form-data`，字段 `file`，返回 201 与待引用记录 |
| GET | `/api/portal/v1/attachments/{id}/download` | `attachment:read_own` | 字节流 |
| POST | `/api/portal/v1/attachments/{id}/delete` | `attachment:delete` | 删除未引用待引用附件 |

管理 API（前缀 `/api/v1/attachments`）：

| 方法 | 路径 | 能力 | 说明 |
|---|---|---|---|
| GET | `/api/v1/attachments` | `attachment:read` | 全部附件，含 `uploader_name` |
| POST | `/api/v1/attachments` | `attachment:create` | 上传，返回待引用记录 |
| GET | `/api/v1/attachments/{id}/download` | `attachment:read` | 字节流 |
| POST | `/api/v1/attachments/{id}/delete` | `attachment:manage` | 请求体 `{"reason":"…"}` |
| POST | `/api/v1/attachments/cleanup` | `attachment:manage` | `{"reason":"…","query":"","uploader_id":"","from":"","to":""}` |

列表项包含领域记录的全部公开字段（`id`、`name`、`content_type`、`size`、`status`、`ref_count`、
`sha256`、`created_at`、删除信息等）、`size_text` 与需鉴权的 `download_url`（图片另有 `preview_url`）；管理 API 额外给出
`uploader_name`。`stored_key` 永不进入 API 载荷。

上传限制由服务端执行：超过 `max_attachment_size` 返回 413，嗅探类型不在 `allowed_types` 返回 415，
缺少文件或参数非法返回 422。错误形态与其它 API 一致：`{"error":"…"}`。

## 9. 管理员删除与通知

1. 管理员在后台点击删除 → 抽屉要求填写原因（最长 500 字符）→ 全局确认。
2. 服务在同一事务里锁行、标记 `deleted`、写入 `deleted_by`/`delete_reason`/`deleted_at`。
   已被引用的附件**不删除引用行**，业务因此按墓碑渲染。
3. 提交后删除字节；失败只记 Warn，墓碑保留。
4. 生成项目经 `AttachmentService.DeleteManaged` 调用 `messages.NotifyUser`，在**上传者的私有通知频道**
   （会话 `kind=notice`，`notice_key=notice:<user id>`）写入一条系统消息：
   管理员、文件名、大小、原因、UTC 时间。上传者与执行者同一人时不发送。
5. 通知频道只读：收件人可以阅读与标记已读，`Send` 对该会话返回 `ErrSystemOnly`；
   管理端消息列表永远不包含通知会话（`kind=notice` 被排除）。
6. 批量清理按上传者各发一条汇总通知，文件名最多列 10 个；通知写入失败只告警，不影响清理结果。

## 10. 供其它功能复用

任何业务对象都可以引用附件：

```go
actor := attachments.Actor{ID: userID, TenantID: tenantID, DisplayName: name}
att, err := attachmentService.Upload(ctx, actor, header.Filename, header.Size, file)
// 在业务自己的事务里绑定：
err = attachmentStore.Bind(ctx, tenantID, []string{att.ID}, attachments.Ref{
    Type: "invoice", ID: invoiceID,
}, userID, now)
// 读取：
items, err := attachmentService.ListByRef(ctx, tenantID, attachments.Ref{Type: "invoice", ID: invoiceID})
// 业务对象硬删除或字段清空：
err = attachmentService.Release(ctx, actor, attachments.Ref{Type: "invoice", ID: invoiceID})
```

规则：

- `ref_type` 只允许 `[a-z0-9_-]`、≤48 字符；`ref_id` 只允许 `[A-Za-z0-9._:-]`、≤64 字符。
- 绑定前必须确认附件仍属于同一租户、由该上传者上传、状态不是 `deleted`。
- 绑定/业务写入必须在同一个事务里；附件响应的是“字节与引用”，业务响应的是“业务对象”。
- 业务对象不存在或跨租户时返回该业务自己的 `ErrNotFound`，不得泄露附件或业务对象是否存在。
- 业务对象删除时，要么在同一事务里 `Release`，要么保留引用并让页面渲染墓碑；不得留下指向已删业务
  的引用行（`yd_attachment_refs` 必须能被清理）。
- 多附件一次绑定的上限是 `MaxBind`（默认 100）。

## 11. 缓存

`yd_attachments` 与 `yd_attachment_refs` **必须**留在 `cache.gorm.ignore_tables`：

- `stored_key` 带 `json:"-"`，JSON 查询缓存会丢掉它，缓存命中后的第二次下载会报“附件键非法”；
- 引用表参与每次消息渲染，且 Release/墓碑必须立即生效；
- 附件不在缓存敏感的热列表上，读库成本可接受。

生成项目的 `configs/config.yaml` 已列出 `yd_attachments` 与 `yd_attachment_refs`；
旧表在迁移完成后即被删除，不需要（也不应）再加入缓存配置。

## 12. 升级与旧数据

- `attachments/gormstore.Migrate` 创建新表与新索引，幂等。
- `MigrateLegacy` 读取旧 `yd_msg_attachments`（表不存在时直接返回 0），批量复制到
  `yd_attachments` 与 `yd_attachment_refs`：
  - `status=attached` 且 `message_id` 非空 → `referenced` 并写入 `ref_type=message`；
  - 其余 → `pending`；`message_id` 非空时同样写引用；
  - 已存在的行（相同主键）跳过，因此重复启动不会重复复制。
- 复制完成后 `DropLegacy` 删除旧表；删除失败只告警，附件能力仍然可用，但运维应手工执行
  `DROP TABLE yd_msg_attachments`。旧表不再被任何代码读写。
- 旧字节仍按原 `stored_key` 读取，因此**不要**在升级时更换 `message.attachment_dir` /
  `attachment_store` / `attachment_redis_prefix`；要更换后端，先迁移旧文件到新后端并保留相同键。
- 生成项目在 `repository.Open` 里完成迁移与旧表删除；迁移失败只禁用附件能力并告警，不阻塞启动。
- 旧表删除后无法回滚到旧版本；升级前如需保留回滚点，先备份 `yd_msg_attachments`（或整个数据库）。

## 13. 测试

- `attachments/attachments_test.go`：上传与键布局、所有权与列表、绑定/释放/删除规则、
  管理员删除墓碑与原因、未引用清理、释放后重置 TTL、跨租户与旧表迁移/删除。
- `messaging/messaging_test.go`：消息附件上传/下载/可见性、通知频道私有且只读、管理列表排除通知、
  管理员删除后消息渲染墓碑。
- `tooling/scaffold` 的 `TestGeneratedAttachmentManagement`：真实 SQLite 生成项目上的完整 HTTP 闭环
  （上传、下载、门户/后台列表、权限、带原因删除、通知频道、批量清理、用户自删）。
- `tooling/scaffold` 的 `TestGeneratedDocsCoverFrameworkGuides` / `TestGeneratedDocsMatchFrameworkSources`：所有公开指南
  随生成项目发布且与框架源逐字节一致，`docs/README.md` 索引包含附件与关联指南。
- 修改任一环节后必须保持 `go build ./... && go vet ./... && go test ./...` 通过；
  涉及并发与数据库的包需在 `-race` 下通过。

## 14. 排查

| 现象 | 原因 | 处理 |
|---|---|---|
| 第二次下载报“附件键非法” | `yd_attachments` 被 GORM JSON 缓存缓存了，`stored_key` 丢失 | 确认 `cache.gorm.ignore_tables` 含 `yd_attachments` 与 `yd_attachment_refs` |
| 多节点下载随机 404 | `attachment_store: local` 且目录不是共享挂载 | 挂载共享目录，或改用 `attachment_store: redis` |
| 上传返回 413 | 超过 `max_attachment_size` 或路由请求体上限（默认 32 MiB） | 调整配置，确保附件上限小于请求体上限 |
| 上传返回 415 | 嗅探出的 MIME 不在 `allowed_types` | 把类型加入白名单（不要仅信任扩展名） |
| 用户删除返回 409 | 附件仍被业务引用 | 先删除/清空业务对象并 `Release`，再删除附件 |
| 管理员删除后消息显示墓碑 | 预期行为：引用行保留，字节已删除 | 无需处理；墓碑含删除原因 |
| 附件页 503 | 元数据存储或字节后端装配失败 | 查看启动日志中的 `attachment storage is unavailable` |
