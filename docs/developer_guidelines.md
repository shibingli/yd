# 前后端协同约束（developer_guidelines）

yd 采用「全栈单体（Modern Monolith）」模式：**后端直接返回 HTML 片段，前端由 Htmx 4 作为事件总线驱动**。本文件是前后端协作的强制契约。

## 1. 响应契约

| 规则 | 说明 |
|---|---|
| **端点返回 HTML 片段，不是 JSON** | 除显式标注为 API 的端点（`/api/**`、`/openapi.json`）外，所有控制器必须返回 HTML 片段。 |
| **区分整页与片段** | 通过请求头 `HX-Request: true` 判定。整页请求返回布局 + 内容；htmx 请求只返回内容片段（`app.Render` 自动选择）。 |
| **校验失败返回 422** | 表单校验失败返回 `422` + 错误片段，模板中用 `hx-status:422="target:#errors"` 声明式接管。 |
| **重定向走 Htmx 头** | 局部跳转用 `HX-Location`，整页跳转用 `HX-Redirect`，禁止在片段响应里塞 `<meta refresh>`。 |
| **多区域更新优先用 `<hx-partial>`** | 需要同时更新多个区域时，用 `htmx.RenderPartials` 输出 `<hx-partial hx-target=… hx-swap=…>`；`hx-swap-oob` 仅用于兼容场景。 |

## 2. 模板目录约定

```
web/templates/
├── layouts/     # 整页骨架（base.html、dashboard.html）
├── partials/    # 可复用片段（sidebar、table、form、modal、flash、pagination、skeleton）
└── pages/       # 业务页面（整页 + 其片段视图）
```

- 片段命名：`<资源>/<动作>.html`，例如 `users/list.html`、`users/row.html`、`users/form.html`。
- 禁止在模板中书写内联样式表；样式一律来自 Tailwind v4 语义化类（`bg-surface`、`text-content`、`border-border-subtle`、`bg-brand-600` …）。
- 禁止硬编码颜色字面量（`#1d4ed8` 之类）；需要新色值请在 `web/static/css/app.css` 的 `@theme` 中新增语义变量。
- 所有面向用户的文案必须带 `data-yd-i18n="<key>"`（前端 i18n 键），键名与 `configs/i18n/*.yaml` 保持一致。

## 3. Htmx 4 属性约定

- **属性继承是显式的**：父元素上的属性必须加 `:inherited` 修饰符才会被子元素继承，例如
  `<nav hx-target:inherited="#yd-workspace" hx-push-url:inherited="true">`。这对初学者是最大的坑。
- 全局导航使用 `hx-boost`，工作区容器 id 固定为 `yd-workspace`。
- 需要保留输入状态（如表格内联编辑）时使用 `hx-swap="outerMorph"` / `"innerMorph"`；需要彻底重置表单时使用 `outerHTML`。
- 耗时操作必须提供 `hx-indicator` + `.htmx-indicator` 元素；首屏骨架屏使用 `.yd-skeleton`。
- 慢查询/长任务用 `hx-preload` 预热 + `hx-pending` 占位，不得出现白屏。

## 4. 插件能力映射（Htmx 4.0 内建 vs 扩展）

| 需求 | 实现方式 | 是否需额外文件 |
|---|---|---|
| WebSocket 双向通信 | `dist/ext/hx-ws.min.js`（注册名 `ws`） | 是 |
| SSE 单向实时流 | `dist/ext/hx-sse.min.js`（注册名 `sse`） | 是 |
| 多阶段加载 / 骨架屏 | `dist/ext/hx-preload.min.js` + `hx-pending`（扩展）/ `hx-indicator`（内建） | 是 |
| DOM 差量更新（Idiomorph） | **内建** `hx-swap="innerMorph\|outerMorph\|outerSync"`（htmx 4 已内建 idiomorph 算法）；需要独立调用时用 `vendor/idiomorph.min.js` 的 `Idiomorph` 全局 | 否（可选） |
| 客户端 JSON 序列化 | **内建** `hx-encoding`（如 `hx-encoding="application/json"`） | 否 |
| 客户端表单校验 | **内建** `hx-validate="true"` | 否 |
| 响应目标与响应头拦截 | **内建** `hx-status:<码>` + `HX-Retarget`/`HX-Reswap`/`HX-Reselect`；多目标批量用 `hx-targets` 扩展 | 视场景 |
| Alpine.js 融合 | `dist/ext/hx-alpine-compat.min.js` | 是 |

**扩展加载方式（htmx 4 变更）**：不再需要 `hx-ext="name"` 属性，只需在页面中包含对应脚本文件；`config.extensions` 白名单可选。

## 5. 前端依赖治理

- 所有前端库由 `web/assets.lock.json` 登记（名称、版本、URL、路径、sha256、协议）。
- **在线更新**：`yd assets update`（可 `--check` 仅报告、`--source` 指定源）。
- **离线使用**：`yd assets verify --offline` 校验本地完整性；离线环境严禁触网，缺失即报错。
- 严禁从公网 CDN 动态引入；所有资产经 `go:embed` 打进二进制。
- 升级 htmx/Alpine 版本后必须重跑 `yd assets verify` 与模板测试（`go test ./web/`）。

## 6. 国密安全闭环

- **传输前置加密**：含敏感字段的表单声明 `data-yd-encrypt="sm2"`，前端在 `htmx:config:request` 阶段用服务端公钥加密标记字段，并附加 `X-YD-Encrypted: sm2` 头；后端据此用 SM2 私钥解密。
- **完整性校验**：静态资产与本地缓存凭证通过 `ydGuomi.verifyIntegrity(url, expectSm3Hex)` 做 SM3 摘要比对。
- **口令**：后端仅存 `guomi.HashPassword`（SM3 + 盐 + 多轮迭代）结果，比较用常量时间函数。

## 7. 配置与环境变量

- 环境变量前缀固定 **`YD_`**，下划线自动映射为多级嵌套：
  `YD_SERVER_PORT` → `server.port`，`YD_DB_MAXCONN` → `db.maxconn`。
- 优先级：命令行参数 > 环境变量（含 `.env`） > 配置文件 > 框架默认值。
- 生成项目的 `.env` 不得入库（`.gitignore` 已忽略），随仓提供 `.env.template`。

> 需求基线 `docs/需求设计说明书.md` 原文使用 `YUNGO_` 前缀，团队已统一改为 `YD_`；
> 基线文档保持只读，本约定为最终口径。

## 8. 提交纪律

- 提交信息使用**中文 Conventional Commits**：`<类型>(<范围>): <中文简述>`。
- 署名口径唯一，所有提交结尾固定为：

  ```
  Co-Authored-By: Eric Shi <shibingli@sina.com>
  ```

- 提交信息、作者、PR 描述、代码注释中**严禁出现任何 Claude / Anthropic / AI 助手相关内容**。

## 9. 后端协同约束

- 路由模式支持**混合分段**：`/avatar/:id.jpg`、`/report/:id-xx.png` 合法，参数名不得重复。
- 控制器只做编排；业务逻辑放 `internal/service`，数据访问放 `internal/repository`。
- 一切外部输入必须限长并校验；错误统一经 `middleware.Recover` + `app.Render` 输出用户可读的 HTML 错误片段（`pages/error.html`）。
- 新增端点后必须重新生成 `openapi.json`（`yd build` 自动完成）并确认 `/docs` 可见。
