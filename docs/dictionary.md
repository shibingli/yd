# 数据字典能力 | Data dictionaries

> 适用范围：yd 框架与 `yd new` 生成的项目。本文是数据字典能力的唯一使用说明。
>
> `yd new` 会把本文与附件、消息中心、缓存、Redis/asynq、实时传输等关联指南一并复制到生成项目的
> `docs/` 目录，并生成 `docs/README.md` 索引；生成项目不依赖公网链接即可查阅完整说明。

数据字典把「枚举值」从代码里搬进数据库：每个字典类型有稳定的编码，每个字典项有稳定的机器值、
中英标签、排序、启停状态、默认标记与可选的 JSON 元数据。业务数据只保存机器值，标签可以随时改，
字典项可以停用而不影响历史数据。

数据字典分两层：**租户级**字典只属于一个租户，**系统级**字典由全部租户共享。解析时租户层优先，
租户没有同编码时回退到系统层；系统层由配置的系统租户维护，其它租户只读。

## 1. 能力一览

- **租户级**：类型与字典项都以 `tenant_id` 为硬边界，跨租户读写一律 404，不泄露存在性。
- **系统级**：系统字典存放在保留归属 `__system__` 下，对全部租户可见；同编码的租户字典优先，
  系统字典只作兜底。系统层默认只读，配置 `dictionary.system_tenant` 后由该租户中持有
  `dictionary:system_manage` 的成员维护。
- **通用模型**：任意下拉框、状态机、类型标签、地区/币种/行业枚举都能直接用；
  一个类型下最多 1000 项，单项元数据（颜色、图标、CSS 类等）放在 `extra` JSON 对象里。
- **稳定标识**：类型 `code` 与字典项 `value` 创建后不可修改，改它们会破坏已保存的业务数据。
- **默认项互斥**：同一类型至多一个默认项，设置新默认项会在同一事务内清除旧标记；
  停用默认项会自动清除默认标记。
- **启停而非删除**：删除不可撤销；推荐停用。类型删除会在同一事务内级联删除其全部字典项。
- **内置字典**：生成项目首次启动即写入性别、是否、优先级、任务状态四组通用字典；
  种子只补缺失类型，不覆盖标签、不复活被删除的字典项。
- **二级缓存**：单条解析走 L1/L2 缓存，批量解析逐键命中缓存、冷编码单飞 CAS 回填；
  写操作在提交后精确失效对应编码，多节点经 Pub/Sub 同步 L1。
- **双套接口**：htmx 管理页与 JSON API 共用同一份领域服务；门户只读启用数据，后台可读写全部数据。

## 2. 包与分层

| 层 | 位置 | 职责 |
|---|---|---|
| 领域 | `dictionary/` | 类型、哨兵、`Store` 接口、规则校验、解析与缓存、内置目录 |
| 持久化 | `dictionary/gormstore/` | `yd_dict_types`、`yd_dict_items` 的迁移与 `Store` 实现、内置种子 |
| 装配 | `internal/service/dictionary.go`（生成项目） | 读取 `dictionary.*` 配置，打开缓存命名空间 |
| 存储接线 | `internal/repository/store.go`（生成项目） | 迁移并暴露 `DictionaryStore()`，启动时补齐内置字典 |
| 页面 | `internal/controller/dictionary_controller.go`（生成项目） | 租户/系统工作台、抽屉、启停与删除 |
| JSON API | `internal/controller/dictionary_api.go`（生成项目） | 门户批量/单条解析、租户 CRUD |
| 系统 JSON API | `internal/controller/dictionary_system_api.go`（生成项目） | 系统层只读查询与系统租户 CRUD |

`dictionary` 领域包不依赖 GORM、HTTP 或消息中心；GORM 只出现在 `dictionary/gormstore` 子包里。

## 3. 数据表与索引

| 表 | 用途 |
|---|---|
| `yd_dict_types` | 字典类型：租户、编码、中英名称、说明、状态、内置标记、排序 |
| `yd_dict_items` | 字典项：租户、所属类型、机器值、中英标签、扩展 JSON、备注、状态、默认标记、排序 |

索引一律以 `tenant_id` 打头：

- `yd_dict_types`：`(tenant_id, code)` 唯一、`(tenant_id, sort_order)`、`(tenant_id, status, sort_order)`、
  `(tenant_id, created_at)`。
- `yd_dict_items`：`(tenant_id, type_id, item_value)` 唯一、`(tenant_id, type_id, sort_order)`、
  `(tenant_id, type_id, status, sort_order)`。

系统级字典复用同一对表，`tenant_id` 固定为保留值 `__system__`。它不是租户标识，也不能用于登录、
鉴权或成员关系；唯一索引 `(tenant_id, code)` 因此天然把系统层与每个租户分开。

**没有外键**：删除类型时由 `gormstore.DeleteType` 在同一个事务里先删字典项、再删类型；
引用完整性由服务层维护，与本仓其它表一致。

## 4. 领域规则

### 4.1 类型

- `code`：小写字母开头，仅限小写字母、数字与下划线，最长 64 字节；创建后不可修改。
- `name`：必填，最长 80 个字符；`name_en` 可选，最长 80 个字符。
- `description`：最长 500 个字符。
- `status`：`enabled`（默认）或 `disabled`。停用类型后，公共解析看不到它的任何字典项。
- `sort`：0 到 1,000,000，决定管理列表顺序。
- `builtin`：框架预置类型为 `true`，不可删除、可以停用或改标签。

### 4.2 字典项

- `value`：字母或数字开头，仅限 `A-Z a-z 0-9 _ . : -`，最长 64 字节；创建后不可修改。
  值按数据库排序规则比较，请勿创建仅大小写不同的值（MySQL 默认不区分大小写，
  SQLite/PostgreSQL 区分）；建议统一使用小写或稳定的业务编码。
- `label`：必填，最长 120 个字符；`label_en` 可选，最长 120 个字符。
- `remark`：最长 500 个字符。
- `extra`：可选，必须是 JSON **对象**，最长 4096 字节，例如
  `{"color":"#dc2626","icon":"lucide-alert-triangle","class":"is-danger"}`。
- `status`：`enabled`（默认）或 `disabled`；停用项不出现在公共解析结果中。
- `default`：预选项。停用项不能设为默认项；停用默认项会自动清除默认标记。
- `sort`：0 到 1,000,000，决定字典项在类型内的顺序。
- 单个类型最多 1000 项。上限在类型行锁内复查，并发写入也不会突破。

### 4.3 范围与优先级

- 每个类型与字典项都属于一个范围：`tenant`（租户级）或 `system`（系统级）。
- 解析同一编码时按「租户 → 系统」的顺序查找：租户层命中即返回（`scope` 为 `tenant`），
  租户层不存在（或停用）时回退系统层（`scope` 为 `system`）。
- 两层都可以有同编码；系统级唯一索引只在系统层内生效，不会与租户编码冲突。
- 系统层默认只读。`dictionary.system_tenant` 配置某个租户的 slug 或 `tn_` ID 后，该租户中持有
  `dictionary:system_manage` 的成员可以维护系统层；其它租户（包括其管理员）只能读。该键在启动时
  解析一次，修改配置或新建目标租户后需要重启进程。
- 系统租户自身也按同一顺序解析：如果它同时有租户级同编码字典，租户级仍然优先。

### 4.4 删除语义

- 删除字典项是**硬删除**，不可撤销；历史业务数据里保存的机器值仍然存在，只是再也解析不到标签。
  因此推荐停用而不是删除。
- 删除字典类型会在同一个事务内级联删除它的全部字典项；页面与 API 的确认框都会明确提示这一点。
- 内置类型不可删除（409 `built-in dictionary cannot be deleted`）。

## 5. 缓存与性能

- 缓存命名空间固定为 `dictionary`，每进程唯一；租户键为 `tenant:<tenant_id>|<code>`，系统键为
  `system|<code>`（`|` 不会出现在编码里，前缀也保证系统层不会与任何租户键相撞）。
- 值为 `dictionary.Snapshot`：范围 + 类型元数据 + **启用**字典项（按 `sort`、创建时间排序），
  用 `ydx/json` 编解码。
- 单条解析 `Service.Resolve` 先读租户层，租户未命中时再读系统层；每层各自经
  `cache.Cache.GetOrLoad`，同一进程内并发未命中会合并为一次加载。租户层不存在的编码走负缓存
  （默认 30 秒），但系统层仍会被查询，因此系统级的新增/删除不会被租户负缓存挡住。
- 批量解析 `Service.ResolveMany` 逐键读取缓存：命中的编码零数据库查询；冷编码各自经 `GetOrLoad`
  单飞加载并 CAS 回填，因此慢加载器不会覆盖并发写入。单次最多 50 个编码。
- 写透回填由缓存完成：加载时观察到的版本参与 CAS，并发写删除键后陈旧结果不会被重新写回。
- 任何写操作在数据库提交后调用 `cache.Cache.Delete` 删除对应范围的键，并通过 Pub/Sub 让其它节点的
  L1 失效；系统写只失效系统键，租户写只失效该租户键，互不干扰。失效失败只记告警，L1 租约与 TTL 兜底。
- 默认 TTL 10 分钟、负缓存 30 秒，可在 `configs/config.yaml` 的 `dictionary` 段覆盖：

```yaml
dictionary:
  cache_ttl: 10m
  cache_negative_ttl: 30s
  system_tenant: ""   # 允许管理系统字典的租户 slug 或 tn_ ID；留空表示系统层只读
```

- 管理列表（类型分页、字典项分页）直接查库，不走业务缓存；GORM 插件仍可按 `cache.gorm` 的通用规则
  缓存普通 SELECT，但 `Resolve` 的 JOIN 查询不会被 GORM 插件缓存。

## 6. HTTP API

所有 API 都要求 `Authorization: Bearer <token>`（登录后由浏览器 cookie 自动提升）。
门户解析接口要求 `dictionary:resolve`；后台读取接口要求 `dictionary:read`，租户写接口要求
`dictionary:manage`，系统写接口要求 `dictionary:system_manage` **且**当前活动租户等于配置的
`dictionary.system_tenant`。内置角色默认授权：

| 角色 | `dictionary:resolve` | `dictionary:read` | `dictionary:manage` | `dictionary:system_manage` |
|---|---|---|---|---|
| `owner` / `admin` | ✅ | ✅ | ✅ | ✅（仅系统租户生效） |
| `auditor` | ✅ | ✅ | ❌ | ❌ |
| `member` | ✅ | ❌ | ❌ | ❌ |

这样普通成员可以为表单解析启用字典（含系统层兜底），但看不到后台工作台；后台工作台与类型/字典项列表
只对持有 `dictionary:read` 的角色开放。`dictionary:system_manage` 只有在配置的系统租户内才生效，
其它租户即使持有该能力也会得到 403。

### 6.1 门户解析

```http
GET /api/portal/v1/dictionaries?codes=gender,priority
GET /api/portal/v1/dictionaries?code=gender&code=priority
GET /api/portal/v1/dictionaries/gender
```

批量响应：

```json
{
  "dictionaries": [
    {
      "scope": "tenant",
      "code": "gender",
      "name": "性别",
      "name_en": "Gender",
      "description": "通用性别选项",
      "items": [
        {"value": "male", "label": "男", "label_en": "Male", "sort": 10},
        {"value": "female", "label": "女", "label_en": "Female", "sort": 20},
        {"value": "unknown", "label": "未知", "label_en": "Unknown", "default": true, "sort": 30}
      ]
    }
  ],
  "missing": ["not_configured"]
}
```

- `scope` 标记实际提供该字典的层：`tenant` 或 `system`；租户有同编码时一定是 `tenant`。
- 停用的类型与字典项不会出现；两层都没有启用类型的编码列在 `missing` 下。
- 单条接口在两层都不存在或停用时返回 404。
- 编码可以是 `code` 重复参数，也可以是 `codes` 逗号分隔参数，两者都支持。

### 6.2 后台类型 CRUD

```http
GET    /api/v1/dictionaries?q=gen&status=enabled&page=1&page_size=20
POST   /api/v1/dictionaries
GET    /api/v1/dictionaries/{id}
PUT    /api/v1/dictionaries/{id}
DELETE /api/v1/dictionaries/{id}
```

创建载荷：

```json
{
  "code": "invoice_status",
  "name": "发票状态",
  "name_en": "Invoice status",
  "description": "发票生命周期",
  "status": "enabled",
  "sort": 100
}
```

响应是类型对象，包含 `id`、`code`、`name`、`name_en`、`description`、`status`、`sort`、
`item_count`、`builtin`、`created_at`、`updated_at`。更新时 `code` 被忽略（不可变）；
省略 `status` 时保留当前状态，其余可编辑字段按请求体整体覆盖。

`GET /api/v1/dictionaries/{id}` 默认返回全部字典项（含停用项）；传 `include_disabled=false`
只返回启用项。后台列表与字典项列表默认同样包含停用项，便于审计与恢复。
`item_count` 始终是该类型的全部字典项数量，不受 `include_disabled` 影响；响应中的 `total`
是当前筛选下的数量。

### 6.3 后台字典项 CRUD

```http
GET    /api/v1/dictionaries/{id}/items?q=&status=&page=1&page_size=20
POST   /api/v1/dictionaries/{id}/items
PUT    /api/v1/dictionaries/{id}/items/{item_id}
DELETE /api/v1/dictionaries/{id}/items/{item_id}
```

创建载荷：

```json
{
  "value": "paid",
  "label": "已支付",
  "label_en": "Paid",
  "extra": {"color": "var(--color-success)"},
  "remark": "支付回调完成后进入",
  "status": "enabled",
  "default": false,
  "sort": 30
}
```

更新时 `value` 被忽略（不可变）；省略 `status` 时保留当前状态，其余可编辑字段按请求体整体覆盖，
因此不传 `default` 会清除默认标记。`extra` 可以直接是 JSON 对象，也可以是包含 JSON 对象的字符串；
两种形态都会在服务端校验为对象。

### 6.4 系统级 API

系统级读取对所有持有 `dictionary:read` 的账号开放；写入只在配置的系统租户内、且账号持有
`dictionary:system_manage` 时可用。响应结构与租户级一致，`scope` 为 `system`。

```http
GET    /api/v1/system-dictionaries?q=&status=&page=1&page_size=20
POST   /api/v1/system-dictionaries
GET    /api/v1/system-dictionaries/{id}?include_disabled=false
PUT    /api/v1/system-dictionaries/{id}
DELETE /api/v1/system-dictionaries/{id}
GET    /api/v1/system-dictionaries/{id}/items?q=&status=
POST   /api/v1/system-dictionaries/{id}/items
PUT    /api/v1/system-dictionaries/{id}/items/{item_id}
DELETE /api/v1/system-dictionaries/{id}/items/{item_id}
```

创建系统类型：

```json
{
  "code": "invoice_status",
  "name": "发票状态",
  "name_en": "Invoice status",
  "description": "全局发票生命周期",
  "status": "enabled",
  "sort": 100
}
```

租户创建同编码字典不会冲突；解析时租户版本优先。删除或停用租户版本后，解析自动回退到系统版本。
系统内置字典（`gender`、`yes_no`、`priority`、`task_status`）同样不可删除，但系统租户可以改标签、
启停或修改字典项。

### 6.5 状态码

| 状态 | 含义 |
|---|---|
| 200 / 201 / 204 | 成功 / 已创建 / 无内容 |
| 403 | 缺少 `dictionary:resolve`、`dictionary:read`、`dictionary:manage` 或 `dictionary:system_manage`；或系统写请求来自非系统租户 |
| 404 | 类型或字典项不存在、跨租户，或系统层不存在该编码 |
| 409 | 编码重复、机器值重复、内置类型不可删除 |
| 422 | 编码/值/标签/排序/状态/JSON 校验失败，或批量编码过多 |
| 503 | 数据字典存储不可用（项目未配置数据库或迁移失败） |

错误响应统一为 `{"error": "..."}`。

## 7. 管理页面

`/mgr/dictionaries` 是租户数据字典工作台，`/mgr/system-dictionaries` 是系统级工作台；两者共用同一套
页面与组件，通过顶部页签切换。系统页面对所有持有 `dictionary:read` 的账号可读；只有配置的系统租户
中持有 `dictionary:system_manage` 的成员会看到新建、编辑、启停与删除控件。

- 左侧是字典类型列表，支持关键字搜索与分页；点击名称切换到该类型的字典项。
- 右侧是所选类型的字典项列表，显示标签、机器值、排序、默认标记与启停状态；支持按机器值/中英标签搜索。
- 字典项搜索保留所选类型、类型搜索关键字与类型页码，并写入 URL；切换类型时清空字典项搜索。
- 类型与字典项搜索关键字最长 128 个字符。
- 「新建字典 / 新建字典项 / 编辑」都打开右侧抽屉（`#yd-drawer`）。
- 启停与删除统一放在行尾的图标菜单里，并先经过 `data-yd-confirm` 全局确认框。
- 内置类型显示「内置」徽标且不提供删除动作。
- 页面使用 `dictionary:read` 渲染；没有 `dictionary:manage` 的用户看不到新建、编辑、启停与删除控件。
- 选择类型、搜索与分页都是 htmx 请求，目标为 `#yd-workspace`，URL 与浏览器历史同步。

## 8. 在业务中使用

### 8.1 Go

生成项目的 `internal/service.DictionaryService` 已经装配好存储与缓存：

```go
snapshot, err := ctrl.DictionaryService().Resolve(ctx, tenantID, "gender")
if err != nil {
    return err
}
label := snapshot.Label("male", lang) // "Male" / "男"，未知值原样返回
```

批量解析：

```go
snapshots, err := ctrl.DictionaryService().ResolveMany(ctx, tenantID, []string{"gender", "priority"})
```

`Snapshot.Lookup(value)` 返回 `(SnapshotItem, bool)`，`SnapshotItem.DisplayLabel(lang)` 按语言选择标签。
`Snapshot.Scope` 告诉调用方实际命中租户层还是系统层，可用于审计或展示来源。

### 8.2 浏览器 / 脚本

```js
const response = await fetch("/api/portal/v1/dictionaries?codes=gender,priority", {
  headers: { "Accept": "application/json" },
});
const { dictionaries, missing } = await response.json();
const gender = dictionaries.find((item) => item.code === "gender");
```

页面模板里需要直接渲染下拉框时，建议由服务端先 `Resolve` 再把 `snapshot.Items` 传给模板：

```html
<select name="gender">
  {{- range .Gender.Items }}
  <option value="{{ .Value }}"{{ if .IsDefault }} selected{{ end }}>{{ .Label }}</option>
  {{- end }}
</select>
```

### 8.3 值不存在时

业务数据里保存的机器值可能在字典项被删除后解析不到标签。调用方必须回退到机器值本身
（`Snapshot.Label` 已经这样做），不要因为字典项缺失而报错或丢弃历史数据。

## 9. 内置字典

生成项目首次启动（以及每次启动补齐缺失类型）时写入系统层，并为每个租户写入一份租户级副本：

| 编码 | 名称 | 字典项 |
|---|---|---|
| `gender` | 性别 / Gender | `male` 男, `female` 女, `unknown` 未知（默认） |
| `yes_no` | 是否 / Yes or no | `yes` 是, `no` 否（默认） |
| `priority` | 优先级 / Priority | `low` 低, `normal` 普通（默认）, `high` 高, `urgent` 紧急 |
| `task_status` | 任务状态 / Task status | `pending` 待处理（默认）, `in_progress` 进行中, `done` 已完成, `cancelled` 已取消 |

种子规则：

- 系统层在每个安装实例启动时写入一次；租户层在种子租户创建时写入，既有租户每次启动补齐。
- 每个范围只补**类型编码不存在**的字典；已存在的类型整体保持不变（标签、排序、状态都不覆盖）。
- 已存在类型下被删除的字典项不会复活；升级新增的字典项也不会被补进已存在的类型。
- 内置类型不可删除，但可以停用、改标签、改排序或修改字典项（系统内置仅系统租户可改）。
- 租户级内置字典优先于系统级内置字典；删除/停用租户副本后才会看到系统层版本。
- 运行期新建的租户在下次启动时补齐租户层副本；在此期间它直接使用系统层的内置字典。

## 10. 多租户与安全

- 每个租户 `Store` 方法都显式接收 `tenant_id`，每条 SQL 都把它写进 WHERE；跨租户与不存在都返回
  `dictionary.ErrNotFound`，HTTP 映射为 404。系统层是唯一有意例外：它固定使用保留归属
  `__system__`，由服务层显式选择，绝不从请求参数或用户输入推导。
- 系统层对全部租户可见，但写入口只对配置的 `dictionary.system_tenant` 开放；其它租户即使持有
  `dictionary:system_manage` 也会被控制器拒绝（403），前端显隐不是安全边界。
- 门户解析只返回启用类型与启用项；后台读写都要求对应能力。
- `extra` 会进入 API 响应与页面模板，但模板必须把它当数据而不是 HTML；需要展示 `extra` 时
  请显式转义或只读取其中的白名单字段。
- 字典编码、机器值、标签都不是凭据，不应承载密钥、口令或个人信息。

## 11. 运维与排查

| 现象 | 原因与处理 |
|---|---|
| 页面 503 | 项目未配置数据库或迁移失败；查看启动日志中的 `dictionary tables are ready` / `data dictionary is unavailable` |
| 新建后门户仍看不到 | 类型或字典项处于停用状态；检查 `status` |
| 修改后其它节点仍读到旧值 | 缓存失效发布失败；检查 Redis Pub/Sub 与 `cache.*` 日志，等待 TTL/租约（默认 10 分钟 / 30 秒） |
| 系统字典页没有新建/编辑按钮 | `dictionary.system_tenant` 为空或当前租户不是该租户，或账号没有 `dictionary:system_manage`；系统页此时只读 |
| 系统字典创建返回 403 | 当前活动租户不是配置的系统租户；切换租户后再试，或让系统租户管理员操作 |
| 解析到的是租户值而不是系统值 | 这是预期优先级：同编码租户字典优先；删除或停用租户副本后才会回退系统层 |
| `missing` 里出现内置编码 | 内置种子在 `Seed` 阶段写入；确认启动日志有 `builtin dictionaries seeded`，或检查数据库迁移是否成功 |
| 无法删除字典 | 内置类型受保护；改为停用 |
| 机器值改不了 | 设计如此：`code` 与 `value` 创建后不可变，新增一项并停用旧项即可 |

## 12. 测试

```bash
go test ./dictionary/... -race
go test ./security/authz/... -race
go test ./tooling/scaffold/... -run 'Dictionary|I18n|Docs|RealGenerate' -count=1
```

领域测试覆盖校验、默认项互斥、停用规则、缓存命中/失效、逐键批量加载、内置目录、
租户优先/系统兜底与系统失效；`gormstore` 测试覆盖幂等迁移、唯一索引、排序、租户隔离、
系统层解析、级联删除与内置种子；脚手架测试覆盖生成文件清单、路由守卫、i18n 键集合、
系统管理权限、真实编译与生成项目启动。
