# 内容管理与发布 | Content management and publishing

> 适用范围：yd 框架与 `yd new` 生成的项目。本文是 CMS 的唯一使用说明；
> 设计取舍与实施记录见 `docs/superpowers/plans/2026-10-20-cms.md`（本地笔记，不入库）。

## 1. 能力一览

- **无限级分类**：邻接表 + 物化路径；新增、编辑、移动整棵子树、隐藏；禁止环与删除非空分类。
- **文章与单页**：Markdown 源 + 服务端 goldmark 安全渲染 + 纯文本投影；草稿、待审、已发布、已归档；
  定时发布、置顶、推荐、公开/私有、封面、SEO 字段、浏览量、软删除。
- **标签**：一个文章多个标签；按标签检索；标签生命周期。
- **修订历史**：正文或标题变更前自动快照；可恢复；每篇保留最近 N 版。
- **媒体库**：服务端嗅探 MIME、白名单、大小上限、不透明存储键；文章编辑器内直接上传图片。
- **评论**：登录用户发表、两级回复、审核状态（待审/通过/拒绝/垃圾/删除）、全站评论开关。
- **公开站点**：`/blog` 列表、分类、标签、搜索、文章、单页、RSS 2.0、站点地图与媒体；
  门户首页 `/` 只提供普通用户功能，后台管理全部落在 `/mgr/**`；公开分类只统计已发布、公开且到期的文章，
  隐藏分类连同整棵子树一起隐藏。
- **管理后台**：`/mgr/cms/**` 下的概览、文章、分类、标签、媒体、评论、站点设置与审计。
- **JSON API**：`/api/v1/cms/...` 与 HTML 共用同一个领域服务；后台 JSON API 与管理页面同在 `/mgr/**` 体系之外，
但共享同一套权限守卫。
- **权限精细化**：20 项 `cms-*:*` 能力；路由守卫 + 领域所有权 + 状态机三重校验。

## 2. 包与分层

| 层 | 位置 | 职责 |
|---|---|---|
| 领域 | `cms/` | 类型、哨兵、Store 接口、分类/文章/标签/媒体/评论/设置规则 |
| 持久化 | `cms/gormstore/` | 9 张 `yd_cms_*` 表的 AutoMigrate 与 Store 实现 |
| Markdown | `cms/markdown/` | goldmark GFM + 脚注、标题 ID、安全渲染与纯文本抽取 |
| 媒体存储 | `cms/filestore/` | 本地不透明键存储 |
| 项目接线 | `internal/service/cms.go` | 配置、媒体目录、公开租户与领域服务装配 |
| 页面/API | `internal/controller/cms_*` | 后台 HTML、公开站点、JSON API |

领域包不依赖 GORM、HTTP 或 authz；持久化与媒体存储都是接口实现。

## 3. 数据表

所有表都以 `tenant_id` 为第一列/第一条件，索引以 `tenant_id` 打头（或作为联合索引首列）。

| 表 | 用途 |
|---|---|
| `yd_cms_categories` | 无限级分类；`(tenant_id, slug)` 唯一，`path` 物化路径 |
| `yd_cms_articles` | 文章与单页；Markdown/HTML/Text 快照、状态、可见性、定时、SEO、软删除 |
| `yd_cms_tags` | 标签；`(tenant_id, slug)` 唯一 |
| `yd_cms_article_tags` | 文章-标签连接；`(tenant_id, article_id, tag_id)` 唯一 |
| `yd_cms_revisions` | 修订快照；`(tenant_id, article_id, version)` 唯一 |
| `yd_cms_media` | 媒体元数据与不透明存储键 |
| `yd_cms_comments` | 评论、两级回复与审核状态 |
| `yd_cms_settings` | 每租户一行的公开站点设置 |
| `yd_cms_audit` | 管理动作审计 |

### 3.1 无限级分类

- `path` 形如 `/cat_root/cat_child/`，包含自身并以斜杠结尾；子树查询是前缀匹配。
  列类型为 `TEXT`（不再建索引），因此层级不受列宽限制；框架仍以 100 层作为病态树的保护上限。
- 移动分类时，服务在事务内重写整棵子树的 `path` 与 `depth`；目标父级会被重新读取并加锁，
  并发移动造成的环或过期路径返回冲突，而不是写出交错的两棵子树。
- 父分类不能是自身或后代；有子分类或文章的分类不能删除。
- 公开分类页按子树过滤文章，因此父分类自然包含全部后代文章。

## 4. 权限

| 能力 | 含义 | owner/admin | auditor | member |
|---|---|---|---|---|
| `cms-article:read` | 后台读取文章（成员仅本人） | ✓ | ✓ | ✓（本人） |
| `cms-article:create` | 新建文章/单页 | ✓ | | ✓ |
| `cms-article:update` | 编辑文章（服务端限本人） | ✓ | | ✓ |
| `cms-article:delete` | 删除文章（服务端限本人） | ✓ | | ✓ |
| `cms-article:publish` | 发布/定时/下线/归档/恢复 | ✓ | | |
| `cms-article:review` | 审核通过/驳回 | ✓ | | |
| `cms-article:manage` | 管理全部文章 | ✓ | | |
| `cms-category:read` | 读取分类 | ✓ | ✓ | |
| `cms-category:manage` | 分类生命周期 | ✓ | | |
| `cms-tag:read` | 读取标签 | ✓ | ✓ | |
| `cms-tag:manage` | 标签生命周期 | ✓ | | |
| `cms-media:read` | 读取媒体库 | ✓ | ✓ | |
| `cms-media:upload` | 上传媒体 | ✓ | | ✓ |
| `cms-media:delete` | 删除媒体 | ✓ | | |
| `cms-comment:read` | 读取评论（成员仅本人） | ✓ | ✓ | ✓（本人） |
| `cms-comment:create` | 发表评论 | ✓ | | ✓ |
| `cms-comment:moderate` | 审核/删除任意评论 | ✓ | | |
| `cms-comment:delete` | 删除本人评论 | ✓ | | ✓ |
| `cms-site:read` | 读取站点设置 | ✓ | ✓ | |
| `cms-site:manage` | 修改站点设置 | ✓ | | |

`cms-article:update` / `delete` 与 `cms-comment:delete` 的作用域是“本人”，
因此普通成员可以持有；服务端仍强制作者身份。管理列表同样按能力收窄：没有审核/发布/管理能力、
也不是审计员的成员只能看到自己的文章与评论，概览与审计对成员返回 403。
前端显隐只是体验优化，不是安全边界。

## 5. 配置

`configs/config.yaml`：

```yaml
cms:
  media_dir: data/cms-media
  page_size: 20
  max_media_size: 33554432
  max_revisions: 20
  public_tenant: ""
  # allowed_types:
  #   - image/png
  #   - image/jpeg
  #   - image/webp
  #   - application/pdf
  #   - video/mp4
```

环境变量前缀 `YD_CMS_*`（例如 `YD_CMS_PUBLIC_TENANT=acme`、`YD_CMS_MAX_MEDIA_SIZE=10485760`）。

`public_tenant` 是匿名访问 `/blog` 时的回退租户标识（slug）；多租户公开站点可用 `X-Tenant`
或子域名指定租户，此时它只是回退值。留空时使用种子租户。

## 6. 发布流程

```
draft ──submit──> pending ──approve──> published
  │                  │                   │
  │                  └──reject──> draft  ├──unpublish──> draft
  └──publish────────────────────────────>┘
                    published ──archive──> archived ──restore──> draft
```

- 作者（或 `cms-article:manage`）可提交审核；审核者通过或驳回。
- `cms-article:publish` 可立即发布、定时发布、下线、归档与恢复。
- 定时发布使用 `publish_at`（UTC；HTML 的 `datetime-local` 输入按 UTC 解释，API 使用 RFC3339）；
  到期前公开列表、RSS 与站点地图都看不到它。
- 已发布文章仍可编辑；每次正文/标题变更先写入修订，再更新正文。
- 删除是软删除；别名会被改写（`slug-deleted-id`），旧地址可以复用。

## 7. 路由

### 后台

| 路径 | 说明 |
|---|---|
| `GET /mgr/cms` | 概览 |
| `GET /mgr/cms/articles` | 文章列表与筛选 |
| `GET /mgr/cms/articles/new` | 新建编辑器 |
| `POST /mgr/cms/articles` | 新建 |
| `GET /mgr/cms/articles/:id/edit` | 编辑编辑器 |
| `POST /mgr/cms/articles/:id` | 更新 |
| `POST /mgr/cms/articles/:id/transition` | 提交/通过/驳回/发布/下线/归档/恢复 |
| `POST /mgr/cms/articles/:id/delete` | 软删除 |
| `GET /mgr/cms/articles/:id/revisions` | 修订抽屉 |
| `POST /mgr/cms/articles/:id/revisions/:rev/restore` | 恢复修订 |
| `GET /mgr/cms/categories` | 分类树 |
| `GET /mgr/cms/categories/new`、`GET /mgr/cms/categories/:id/edit` | 分类抽屉 |
| `POST /mgr/cms/categories`、`POST /mgr/cms/categories/:id`、`POST /mgr/cms/categories/:id/delete` | 分类写操作 |
| `GET /mgr/cms/tags` | 标签列表 |
| `POST /mgr/cms/tags`、`POST /mgr/cms/tags/:id`、`POST /mgr/cms/tags/:id/delete` | 标签写操作 |
| `GET /mgr/cms/media`、`POST /mgr/cms/media`、`POST /mgr/cms/media/upload`、`GET /mgr/cms/media/:id/file`、`POST /mgr/cms/media/:id/delete` | 媒体库与租户内预览 |
| `GET /mgr/cms/comments`、`POST /mgr/cms/comments/:id/moderate`、`POST /mgr/cms/comments/:id/delete` | 评论审核 |
| `GET /mgr/cms/settings`、`POST /mgr/cms/settings` | 站点设置 |
| `GET /mgr/cms/audit` | 审计 |

### 公开站点

| 路径 | 说明 |
|---|---|
| `GET /blog` | 文章列表 |
| `GET /blog/page/:page` | 分页 |
| `GET /blog/category/:slug`、`/blog/category/:slug/page/:page` | 分类（含后代） |
| `GET /blog/tag/:slug`、`/blog/tag/:slug/page/:page` | 标签 |
| `GET /blog/search?q=` | 搜索 |
| `GET /blog/:slug` | 文章 |
| `GET /blog/p/:slug` | 独立单页 |
| `POST /blog/:slug/comments` | 发表评论（登录） |
| `GET /blog/rss.xml` | RSS 2.0 |
| `GET /blog/sitemap.xml` | 站点地图 |
| `GET /blog/media/:id` | 公开媒体 |

静态段先于 `/blog/:slug` 登记，因此 `rss.xml`、`sitemap.xml`、`media` 与 `p` 不会被当成文章别名。

### JSON API

`/api/v1/cms/dashboard`、`/articles`、`/articles/:id`、`/articles/:id/transition`、
`/articles/:id/delete`、`/articles/:id/revisions`、`/categories`、`/tags`、`/media`、
`/comments`、`/settings`、`/audit`。请求与响应使用框架 `jsonx`，错误为 `{"error":"..."}`；
状态码由哨兵映射（404/403/409/413/415/422/503）。文章创建/更新载荷按**完整载荷**处理：
未出现的布尔字段视为 `false`；更新时省略 `kind`/`visibility` 会保留当前值，省略 `slug` 也会保留当前别名。

## 8. 富文本与安全

- 正文存储 `body_markdown`、`body_html`、`body_text` 三份快照；HTML 由服务端 goldmark 渲染。
- 未启用 `html.WithUnsafe`：原始 HTML 被省略，`javascript:` 等危险协议被拒绝。
- 模板只输出服务端净化后的 HTML（`model.CMSArticle.Body` / `CMSComment.Body`）。
- 评论正文同样经过 Markdown 渲染与净化。
- 媒体 MIME 以服务端 `http.DetectContentType` 为准；白名单默认不含 SVG/HTML/脚本。
  Go 的嗅探器无法识别 AVIF（报告为 `application/octet-stream`），因此 AVIF 不在默认白名单内。
- MySQL 的文章/修订正文与 `search_text` 列由 `cms/gormstore` 迁移为 `MEDIUMTEXT`，以容纳默认 512 KiB 正文上限；
  分类的物化路径不建索引（路径查找在内存中完成），避免 MySQL 的 3072 字节索引键上限。
- 存储键为 `tenantID/mediaID`，不含原始文件名；下载按租户读取，跨租户返回 404。

## 9. 检索

`search_text` 在保存时生成：小写拼接标题、摘要、纯文本正文、分类名、作者名与标签名。
检索按空白分词，每个词都必须命中（AND），使用 `LIKE` 子串匹配，
通配符 `!`、`%`、`_` 由 `!` 转义。SQLite / MySQL / PostgreSQL 行为一致。

## 10. 前端

- 管理端沿用生成项目的外观：`#yd-shell`、右侧抽屉、`details.yd-menu`、全局确认框。
- 文章编辑器使用 EasyMDE（本地 vendored）；工具栏的图片按钮通过 `data-yd-editor-upload`
  上传到 `/mgr/cms/media/upload` 并插入地址。
- 媒体库提供复制地址按钮；复制结果通过应用已有的 `yd:toast` 事件反馈。
- 公开页按可选会话渲染：已登录读者看到评论表单与后台入口，匿名读者只看到登录入口；提交评论还要求
  登录用户是公开租户（`cms.public_tenant` 或请求头/子域名指明的租户）的成员，并与页面使用同一个租户。
- 公开评论列表只展示父评论同样可见的回复，避免父评论被隐藏或删除后出现孤立回复；墓碑行仍保留在库中供管理端审计。
- 公开站点由 `portal/layouts/public_shell.html` 承载：站点头部、分类导航、搜索、语言/主题切换与页脚；
  它只在 `.PublicSite` 为真时由 `layouts/shell.html` 引入。
- 所有文案来自 `web/i18n/{zh-CN,en}.yaml`，两份目录的键集合必须一致。

## 11. 升级与种子

- `cms` 是生成项目的核心模块，与消息中心一样没有生成期开关。
- 新建租户首次进入 CMS 时（`EnsureTenant`）自动创建默认分类 `uncategorized` 与默认设置；
  并发启动的唯一索引冲突按“另一个副本已完成”处理。
- 新增权限只改 `security/authz` 注册表与默认授权；既有库由内置角色补齐逻辑自动获得新能力。
- `yd authz check` 必须零错误；前端与模板中的权限引用一律使用注册表派生的常量/数据键。

## 12. 验证

```bash
go build ./... && go vet ./... && go test ./... -race
go test ./tooling/scaffold -run 'TestGeneratedAppCMS|TestGeneratedAppServesFrameworkFeatures' -v -timeout 15m
go run ./cmd/yd authz check
go run ./cmd/yd assets minify --check
go run ./cmd/yd versions check
```
