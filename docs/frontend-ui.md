# yd 前端 UI 组件层使用手册

> 适用版本：Htmx 4.0.0 · Tailwind CSS v4.3 · Alpine.js 3.17.5（CSP 构建）· Go 1.27
>
> 本手册同时适用于 **yd 框架自带的示例界面** 与 **`yd new` 生成的项目**。两者的组件层来自同一份源码：
> `yd new` 把 `web/static/css/components.css`、`web/static/css/ui.css`、`web/templates/components/*.html`
> 与树根 `web/templates/partials/**` 的共享外壳写进项目，运行期脚本经框架的合并静态树兜底提供。
> 门户与管理控制台是同一套组件、同一套外壳，差异只由 `.Portal` / `.Mgr` 数据键选择。
>
> **Htmx 4、Tailwind CSS v4、Alpine.js 3 与 i18n 是基线能力，不是开关**：`yd new` 默认集成四者，
> 全部安全 Htmx 扩展默认加载，`hx-alpine-compat` 恒开启；`--htmx-ext` 只用于追加需显式选择的策略层，
> `--i18n=false` 才显式关闭国际化，没有 `--tailwind` 之类的选择项。生成的每个项目都具备本手册描述的全部能力。
>
> 完整的组件清单、扩展组件参数与图表选型，见 [`frontend-components.md`](frontend-components.md)。
> 本手册讲分层与契约，那份文件讲「用哪一个、怎么传参」。

---

## 目录

1. [设计目标与边界](#1-设计目标与边界)
2. [集中管理：版本、文件与装载顺序](#2-集中管理版本文件与装载顺序)
3. [三层分工：HTMX、Alpine、Tailwind](#3-三层分工htmx-alpinetailwind)
4. [设计令牌与明暗主题](#4-设计令牌与明暗主题)
5. [宽屏、响应式与显示缩放](#5-宽屏响应式与显示缩放)
6. [布局原语](#6-布局原语)
7. [表单控件](#7-表单控件)
8. [中国常用控件](#8-中国常用控件)
9. [反馈组件](#9-反馈组件)
10. [导航组件](#10-导航组件)
11. [数据展示](#11-数据展示)
12. [浮层与对话框](#12-浮层与对话框)
13. [Alpine 行为组件参考](#13-alpine-行为组件参考)
14. [校验与格式化 API（ydCN）](#14-校验与格式化-apiydcn)
15. [表单最佳实践](#15-表单最佳实践)
16. [国际化（i18n）](#16-国际化i18n)
17. [权限与安全](#17-权限与安全)
18. [可访问性](#18-可访问性)
19. [新增一个组件](#19-新增一个组件)
20. [测试与守卫](#20-测试与守卫)
21. [常见问题排查](#21-常见问题排查)
22. [从旧写法迁移](#22-从旧写法迁移)
23. [完整示例](#23-完整示例)

---

## 1. 设计目标与边界

这套组件层解决四件事：

1. **一个事实来源**：Htmx、Alpine、Tailwind 的版本、装载顺序、组件类名与模板片段只有一处定义，框架与生成项目不会各写一份。
2. **一个组合规则**：服务端状态归 htmx，页面内状态归 Alpine，外观归 Tailwind；三者不互相镜像、不重复实现。
3. **一个视觉契约**：颜色只来自语义令牌，明暗主题、宽屏与显示缩放不需要每个页面单独适配。
4. **一套可验收的组件**：布局、表单、反馈、导航、数据展示、浮层，以及中国业务里高频出现的手机号、身份证、银行卡、统一社会信用代码、人民币大写、省市区、日期范围与短信验证码。

**它不负责**：业务取数、权限判定（前端判定不是安全边界）、数据库校验、页面级路由。这些分别属于控制器、`authz` 与服务端。

---

## 2. 集中管理：版本、文件与装载顺序

### 2.1 唯一真源

| 内容 | 真源 | 说明 |
|---|---|---|
| Htmx / Alpine / Idiomorph / EasyMDE 等前端版本 | `web/assets.lock.json` | 名称、版本、URL、路径、sha256、许可；由 `yd assets update` 更新 |
| 设计令牌与既有组件 | `web/static/css/components.css` | `@theme`、`:root`、`[data-theme="dark"]` 三个令牌块 + 管理端组件 |
| UI 组件层 | `web/static/css/ui.css` | 表单、反馈、导航、数据展示、中国常用控件；不得出现颜色字面量 |
| 组合入口 | `web/static/css/app.css` | `@import "tailwindcss"` → `./components.css` → `./ui.css` |
| 行为层 | `web/static/js/yd-ui.js` | 全部 `Alpine.data` 组件与中国控件内核 |
| 模板组件 | `web/templates/components/*.html` 与 `web/templates/partials/**` | 9 个组件文件与共享外壳，见下文 |
| 脚本顺序 | 各布局的 `base.scripts` / 生成项目的 `partials/base.html` | 顺序被测试钉死 |

前端资产只能经 `yd assets update` / `yd assets minify` / `yd versions upgrade` 变更；不得手改
`web/static/js/vendor/**`，也不得在模板里引入 CDN。

### 2.2 唯一允许的脚本顺序

```text
htmx 核心
  → 所选 hx-* 扩展（含 hx-alpine-compat）
  → yd-ui.js
  → alpine.min.js
  → yd-guomi.js / yd-guomi-htmx.js
  → yd-authz.js
  → yd-progress.js
  → yd-app.js
```

生成项目的生产壳由 `web.Bundle` 在启动时按上面的顺序把共享脚本拼成一份
`/static/js/yd-bundle.js?v=<hash>`，因此只发一次请求；开发期或组装失败时回退为逐脚本标签，
`PageScripts`（消息编辑器等）仍然单独加载。两种形态的执行顺序完全一致。

**为什么 `yd-ui.js` 必须排在 Alpine 前面**：签入的是 Alpine 的 **CSP 构建**
（`@alpinejs/csp`），它在脚本执行时**立即启动**，不会等待 `DOMContentLoaded`。组件注册表若排在它之后，
`Alpine.data` 会注册进一个已经启动的实例，页面上每个 `x-data` 都会以
`Undefined variable` 失败。`yd-ui.js` 因此先加载，在 `alpine:init` 事件上完成注册。

同理，任何需要 `alpine:init` 的扩展都必须排在 Alpine 之前。

### 2.3 文件分工

| 文件 | 职责 | 不要放什么 |
|---|---|---|
| `components.css` | 令牌、外壳、既有管理端组件、暗色令牌覆盖 | 新的通用表单/反馈组件（放 `ui.css`） |
| `ui.css` | 布局原语、表单、反馈、导航、数据展示、中国控件 | 颜色字面量、业务页面专用样式 |
| `yd-ui.js` | Alpine 组件、校验/格式化内核、剪贴板 | HTTP 取数、业务文案、权限判定 |
| `web/templates/components/form.html` | 字段、输入、文本域、下拉、复选、单选、开关、分段、口令、标签、操作、搜索、文件 | 具体页面的业务表单 |
| `…/cn.html` | 手机号、身份证、银行卡、信用代码、金额、省市区、日期范围、短信、复制 | 非中国业务的通用控件 |
| `…/layout.html` | 页头、卡片、空状态 | 栅格/间距工具类（直接用 Tailwind） |
| `…/feedback.html` | 提示条、标签、加载指示器、骨架屏、轻提示 | 业务状态判断 |
| `…/navigation.html` | 页签、面包屑、步骤条、Alpine 下拉 | 侧栏/顶栏（属于外壳） |
| `…/data.html` | 头像、统计值、表格外壳 | 具体列定义 |
| `…/overlay.html` | 模态外壳 | 全局抽屉与确认框（外壳已提供） |

### 2.4 门户与后台共用同一套组件

`yd new` 生成的项目有门户（`/`、`/messages`、`/friends`、`/account`、`/realtime`、`/ui-kit`）与管理后台（`/mgr/**`）两个界面，
但它们**不是两套组件**。组件层、外壳部件与消息卡片都只有一份，两侧的差异由数据（`.Portal` / `.Mgr`）
表达，而不是复制一份 HTML：

| 共享位置 | 内容 |
|---|---|
| `web/templates/components/**` | 全部 UI 组件模板（表单、中国控件、反馈、导航、数据展示、浮层、图表） |
| `web/templates/partials/base.html` | 整页骨架：`<head>`、主题引导、脚本顺序、资产引用、安全头 |
| `web/templates/partials/app_shell.html` | 登录后的应用外壳：侧栏、顶栏、错误槽、工作区、提示、页脚、确认框、抽屉 |
| `web/templates/partials/topbar.html` | 顶栏：侧栏开关、标题、语言/主题、租户菜单、用户菜单、退出 |
| `web/templates/partials/sidebar_brand.html` / `sidebar_footer.html` | 侧栏品牌区与页脚 |
| `web/templates/partials/flash.html` / `shell-switches.html` | 全局提示与语言/主题控件 |
| `web/templates/partials/icon.html` | 唯一的图标入口 |
| `web/templates/partials/messages/message.html` | 唯一的消息卡片；动作前缀取自 `.BasePath`（`/messages` 或 `/mgr/messages`） |
| `web/templates/partials/messages/unread_badge.html` 等 | 未读角标与侧栏角标 |
| `web/templates/partials/pages/error.html` | 门户与后台共用的校验失败 / 错误片段 |

两棵树只保留真正角色相关的部分：

```text
web/templates/
├── components/          # 共享 UI 组件
├── partials/            # 共享骨架、外壳、顶栏、图标、提示、消息卡片
├── portal/
│   ├── layouts/         # 只声明「这是门户」并保留匿名登录分支
│   ├── partials/        # 门户侧栏与门户业务片段
│   └── pages/           # 门户页面
└── mgr/
    ├── layouts/         # 只声明「这是后台」
    ├── partials/        # 后台侧栏与后台业务片段
    └── pages/           # 后台页面
```

规则：

- **门户与后台互不 include**：`portal/**` 不引用 `mgr/**`，`mgr/**` 不引用 `portal/**`。共享部件只放树根
  `partials/**`，由两侧共同调用；根级共享外壳按 `.Portal` / `.Mgr` 选择对应侧栏。
- **差异用数据表达**：新增一处「门户有、后台没有」或相反的入口时，优先在共享组件里判断 `.Portal` / `.Mgr`，
  不要复制整块模板。
- **新增通用部件先放根**：只有当部件本身就是某个界面的导航或页面时，才放进 `portal/**` 或 `mgr/**`。
- **消息卡片只有一张**：门户历史与后台治理共用 `partials/messages/message.html`，动作 URL 由
  `MessagePortalBase` / `MessageAdminBase` 注入的 `.BasePath` 决定。
- **布局入口保持轻量**：`portal/layouts/base.html` 与 `mgr/layouts/base.html` 只调用 `partials/base`；
  门户外壳保留唯一的匿名分支，后台外壳直接调用 `partials/app_shell`。
- **门户自带组件示例页**：登录后访问 `/ui-kit`（`portal/pages/ui_kit.html`）即可看到全部控件；该页只
  组合 `components/**`，不手写输入框、按钮图标或颜色，新增组件时必须同步加入并保持
  `TestGeneratedUIKitShowsEveryComponent` 通过。图表由控制器经 `http/chart` 生成，示例数据不进入前端脚本。

### 2.5 作为独立前端插件使用

组件层是纯前端资产，不依赖 Go 运行时，也可以单独用在任意 htmx 页面：

```html
<link rel="stylesheet" href="/static/css/yd.css">
<script src="/static/js/vendor/htmx.min.js" defer></script>
<script src="/static/js/yd-ui.js" defer></script>
<script src="/static/js/vendor/alpine.min.js" defer></script>
```

`yd-ui.js` 是 UMD 模块，在浏览器暴露 `window.ydUI`：

```js
window.ydUI.version        // 组件层版本
window.ydUI.components     // 已注册的组件名数组
window.ydUI.register(Alpine) // 手动注册（自动注册不适用时）
window.ydUI.CN             // 同 window.ydCN，校验与格式化内核
```

- **自动注册**：脚本按上面的顺序加载时，`yd-ui.js` 会在加载期调用 `Alpine.data()`；如果在 Alpine 之后加载，
  它会在 `alpine:init` 上补注册。正常页面不需要手写 `Alpine.start()` 或任何内联脚本。
- **手动注册**：使用打包器时先 `import Alpine from '@alpinejs/csp'`，再调用
  `window.ydUI.register(Alpine)`，最后 `Alpine.start()`。注册函数带幂等守卫，重复调用无副作用。
- **htmx 交换后自动生效**：Alpine 的变更观察器会初始化新节点；不需要在 `htmx:afterSwap` 里重新注册组件。
- **样式复用 Tailwind v4**：组件样式来自 `ui.css` 的 `@layer components` 与 `@apply`，颜色只取 `@theme`
  语义令牌；独立页面同样需要 `app.css` 的令牌块（或直接复用编译后的 `yd.css`）。
- **图标复用同一份 sprite**：`/static/img/icons.svg` 由 `yd icons sync` 生成；组件模板经
  `{{template "partials/icon" "lucide-x"}}` 引用，独立页面可写
  `<svg class="yd-icon"><use href="/static/img/icons.svg#i-lucide-x"></use></svg>`。
- **CSP 兼容**：组件只使用 Alpine CSP 构建支持的表达式子集；不要在页面里写内联 `<script>`、`onclick=`、
  `new Function` 或 `eval`。

---

## 3. 三层分工：HTMX、Alpine、Tailwind

### 3.1 一张表决定写在哪一层

| 状态 / 行为 | 归属 | 典型写法 |
|---|---|---|
| 列表、记录、分页、筛选结果 | **htmx** | `hx-get="/users?page=2" hx-target="#rows"` |
| 新增/编辑/删除/启停/轮换 | **htmx** | `hx-post` / `hx-delete` + `hx-target` + `data-yd-confirm` |
| 导航与工作区切换 | **htmx** | `hx-get` + `#yd-workspace` + `innerMorph` |
| 服务端推送（SSE/WS） | **htmx 扩展** | `hx-sse:connect` / `hx-ws:connect` |
| 菜单展开、页签激活 | **Alpine** | `x-data="ydDropdown"` / `ydTabs` |
| 输入时的格式化与本地校验 | **Alpine** | `x-data="ydPhone"` / `ydAmount` |
| 文件拖放、标签增删、口令强度 | **Alpine** | `ydUpload` / `ydTagInput` / `ydPassword` |
| 纯外观（开/关、选中、禁用） | **CSS** | 原生 checkbox + `.yd-toggle`；`:has()` |
| 颜色、间距、圆角、阴影、断点 | **Tailwind + 令牌** | `class="yd-card"`、`md:grid-cols-2`、`bg-surface` |

### 3.2 三条硬规则

1. **服务端状态不复制进 Alpine**。页面已经渲染出来的数据就是唯一副本；Alpine 需要读取时从 DOM/`data-*`
   取初始值，而不是再发一次请求。
2. **Alpine 不发 HTTP**。需要取数或写入时用 htmx（`htmx.trigger` 或表单提交），不要写 `fetch`/`XMLHttpRequest`。
3. **表单字段必须真实存在**。可见输入框本身就是提交字段（带 `name`），Alpine 只在其外围净化、校验与格式化；
   不要把孩子提交值藏进「只有脚本才能生成」的字段里。

### 3.3 两者如何衔接

- htmx 换入片段后，Alpine 自带的变更观察器会初始化新节点，不需要任何重新绑定代码。
- 需要让 htmx 在 Alpine 状态变化后重新请求时，派发原生事件：
  ```html
  <div x-data="ydSearch" @yd:search:clear="htmx.trigger(#results, 'search')">…</div>
  ```
- 需要让 Alpine 响应 htmx 请求结果时，监听 htmx 4 事件：
  ```html
  <div x-data="ydSmsCode" x-on:htmx:after:request="…">…</div>
  ```
  更稳妥的写法是在组件 `init()` 里用 `addEventListener('htmx:after:request', …)`，不依赖 CSP 表达式对事件名的解析。
- 行级刷新优先 `outerMorph`，整块工作区优先 `innerMorph`；只有确实想丢掉输入状态时才用 `outerHTML`。

### 3.4 反模式

- 用 Alpine 维护一份列表再从服务端同步——两份状态必然漂移。
- 用 htmx 每次按键提交整页表单——搜索请用 `hx-trigger="input changed delay:250ms"` 且只换结果区。
- 在 `x-on` 里写复杂表达式或箭头函数——CSP 构建不支持；把逻辑放进 `Alpine.data` 方法。
- 为每个表格行创建一个 Alpine 组件——用事件委托或一个包裹组件（如 `ydTableSelect`）。

---

## 4. 设计令牌与明暗主题

### 4.1 颜色令牌

所有颜色定义在 `components.css` 的三个令牌块中；`ui.css` 与模板只能引用变量。

| 令牌 | 用途 | 亮色 | 暗色 |
|---|---|---|---|
| `--color-surface` | 页面/卡片表面 | `#ffffff` | `#0b1220` |
| `--color-surface-muted` | 次级表面 | `#f8fafc` | `#111c2f` |
| `--color-surface-raised` | 浮起面板 | `#ffffff` | `#0f1a2c` |
| `--color-content` | 主文字 | `#0f172a` | `#e6edf7` |
| `--color-content-muted` | 次要文字 | `#64748b` | `#94a3b8` |
| `--color-border` / `-subtle` | 边框 | `#94a3b8` / `#cbd5e1` | `#475569` / `#334155` |
| `--color-field` | 输入面 | `#ffffff` | `#0b1220` |
| `--color-field-border` | 输入边框 | `#cbd5e1` | `#334155` |
| `--color-focus-ring` | 焦点环 | `#2563eb` | `#60a5fa` |
| `--color-accent` | 强调/链接/当前项 | `#2563eb` | `#60a5fa` |
| `--color-success` / `warning` / `danger` / `info` | 状态 | 见文件 | 见文件 |
| `--color-*-soft` | 状态浅底 | 见文件 | 见文件 |
| `--color-overlay` | 遮罩 | `rgb(2 6 23 / .55)` | `rgb(2 6 23 / .72)` |

### 4.2 主题切换

- `<html data-theme>` 正常是 `light` / `dark` 解析值；服务端预渲染或首屏脚本失败时也可能是
  `auto` / `system`。三种写法都必须在 CSS 里得到同一套令牌，否则输入框等控件会停在浅色。
- 首屏主题脚本位于 `<head>`、样式表之前，且是唯一同步脚本；不得改成 `defer`，否则会先画错一帧。
- `color-scheme` 在 `:root`、`[data-theme="dark"]` 与 `@media (prefers-color-scheme: dark)` 下的
  `[data-theme="auto"]` / `[data-theme="system"]` 声明，原生控件（`select`、滚动条、`dialog`）才会跟随主题。
- 新增颜色时：先在 `@theme` 与 `:root` 声明，再在 `[data-theme="dark"]` 覆盖，并把同一组变量复制到
  跟随系统的媒体块；最后在组件里引用变量。`TestSystemPreferenceRepeatsEveryDarkToken` 会拦截漂移。
  **不得**在 `ui.css` 或模板里写 `#hex`、`rgb()`、`hsl()`。

### 4.3 语义使用示例

```html
<div class="yd-card">
  <p class="yd-field__hint">次要说明</p>
  <a class="yd-link" href="/help">帮助</a>
</div>
```

不要写 `style="color:#2563eb"`，也不要写 `class="text-[#2563eb]"`。

---

## 5. 宽屏、响应式与显示缩放

### 5.1 断点（Tailwind v4 默认）

| 名称 | 最小宽度 | 典型设备 / 缩放 |
|---|---|---|
| 默认 | 0 | 手机竖屏、320–479 px |
| `sm` | 40rem（640px） | 手机横屏、小平板 |
| `md` | 48rem（768px） | 平板 |
| `lg` | 64rem（1024px） | 1280×720 @125%、1366×768 @125% |
| `xl` | 80rem（1280px） | 1920×1080 @150% |
| `2xl` | 96rem（1536px） | 1920×1080 @125%、2K |

### 5.2 流式布局

- 页面不设固定最大宽度；`yd-container` 用 `padding-inline: clamp(1rem, 2.2vw, 2.5rem)` 提供留白。
- 只有正文使用 `yd-container--narrow`（72rem）限制阅读宽度；管理界面使用全宽。
- 卡片网格用 `repeat(auto-fit, minmax(min(100%, 16rem), 1fr))`，不靠媒体查询硬切列数。
- 表单在 `md` 以下单列、`md` 以上两列；省市区在 `48rem` 以下自动堆叠。

### 5.3 显示缩放（125% / 150%）

Windows 显示缩放改变的是 CSS 像素与设备的比例，本质上与浏览器缩放相同。组件层靠三条规则适配：

1. **控件尺寸用 rem**（`--control-height: 2.5rem`、`--radius-control`），跟随根字号，不使用 `px` 写死高度。
2. **用 `min-height` 而不是 `height`**，文字变大时控件跟着长高，不会裁切。
3. **用 `100dvh` 而不是 `100vh`**，移动端浏览器工具栏收起/展开时高度正确。

不要用 `@media (min-resolution: 1.25dppx)` 去猜缩放比例；那会把「高 DPI」与「用户放大」混为一谈。

### 5.4 表格与窄屏

- `.yd-table__wrap` 负责横向滚动，并设置 `overscroll-behavior-x: contain`，避免滚动穿透。
- 窄屏不要强行压缩列宽；保留滚动，或用卡片列表替代（业务自行选择，组件层不隐藏数据）。
- 行内操作菜单优先 `<details class="yd-menu">`；`yd-app.js` 会把面板提升为 `fixed` 定位，避免被表格的
  `overflow-x` 裁切。

### 5.5 建议验证矩阵

| 视口 | 缩放 | 检查点 |
|---|---|---|
| 390×844 | 100% | 侧栏抽屉、表单单列、按钮不溢出 |
| 768×1024 | 100% | 表单两列、卡片两列 |
| 1366×768 | 125% | 侧栏折叠、表格可横向滚动 |
| 1920×1080 | 150% | 表单两列、内容不出现过宽行 |
| 2560×1440 | 100% | 工作区全宽、留白随视口增长 |

---

## 6. 布局原语

### 6.1 容器与栈

```html
<main class="yd-container yd-stack yd-stack--lg">
  <section class="yd-card">…</section>
  <section class="yd-card">…</section>
</main>
```

| 类 | 作用 |
|---|---|
| `.yd-container` | 流式留白，不设最大宽度 |
| `.yd-container--narrow` | 正文阅读宽度（72rem） |
| `.yd-container--wide` | 超宽屏工作台（120rem） |
| `.yd-stack` / `--xs` / `--sm` / `--lg` | 纵向节奏 |
| `.yd-cluster` / `--between` | 横向换行组 |
| `.yd-split` / `--reverse` | 64rem 以上两栏，以下堆叠 |
| `.yd-workbench` | 75rem 以上「列表 + 详情」工作台 |
| `.yd-toolbar` / `__group` / `__spacer` | 工具栏与右侧弹簧 |
| `.yd-form-grid` / `--3` / `__full` | 表单栅格 |

### 6.2 页头

```html
{{template "components.layout.page-header" (dict
  "Title" (tr "page.users.title")
  "TitleKey" "page.users.title"
  "Lead" (tr "page.users.lead")
  "LeadKey" "page.users.lead"
  "Actions" (template.HTML `<a class="yd-btn yd-btn--primary" href="/mgr/users/new">新建</a>`))}}
```

最佳实践：页头只放标题、一句说明与主操作；筛选与批量操作放工具栏，不要堆在标题旁边。

### 6.3 卡片（open/close 成对）

```html
{{template "components.layout.card.open" (dict
  "Title" (tr "page.users.list") "TitleKey" "page.users.list"
  "Description" (tr "page.users.lead") "DescriptionKey" "page.users.lead")}}
  …任意内容…
{{template "components.layout.card.close" .}}
```

### 6.4 空状态

```html
{{template "components.layout.empty" (dict
  "Title" (tr "page.users.empty") "TitleKey" "page.users.empty"
  "Text" (tr "page.users.empty_hint") "TextKey" "page.users.empty_hint"
  "Actions" (template.HTML `<a class="yd-btn yd-btn--primary" href="/mgr/users/new">新建用户</a>`))}}
```

空状态要**指出下一步操作**，不要只写「暂无数据」。

---

## 7. 表单控件

### 7.1 统一字段契约

每个字段由 `label + control + hint + error` 四部分组成。用 `components.form.field` 或对应局部模板，
不要手写输入框——手写会漏掉焦点环、`aria-invalid`、错误播报与 i18n 钩子。

```html
{{template "components.form.field" (dict
  "ID" "yd-user-email" "Name" "email" "Type" "email" "Value" .Email
  "Label" (tr "field.email") "LabelKey" "field.email"
  "Placeholder" (tr "field.email_placeholder") "PlaceholderKey" "field.email_placeholder"
  "Hint" (tr "field.email_hint") "HintKey" "field.email_hint"
  "Autocomplete" "off" "MaxLength" 254 "Required" true)}}
```

可用键：`ID`、`Name`、`Type`、`Value`、`LabelKey`、`Label`、`PlaceholderKey`、`Placeholder`、
`HintKey`、`Hint`、`ErrorKey`、`Error`、`Autocomplete`、`InputMode`、`Required`、`Sensitive`、
`Invalid`、`Disabled`、`Readonly`、`MaxLength`、`Pattern`、`Min`、`Max`、`Step`、`DescribedBy`。

- `Sensitive` 生成 `data-yd-sensitive="true"`，由 `yd-guomi-htmx.js` 在请求前用 SM2 加密。
- `Invalid` 生成 `aria-invalid="true"` 与 `yd-input--invalid`，错误文本同时通过 `role="alert"` 播报。
- 标签的 `for` 与输入框的 `id` 必须成对；同一页面不得出现重复 `id`。

### 7.2 文本域

```html
{{template "components.form.textarea" (dict
  "ID" "yd-note" "Name" "note" "Value" .Note "Rows" 5
  "Label" (tr "field.note") "LabelKey" "field.note")}}
```

### 7.3 下拉

```html
{{template "components.form.select" (dict
  "ID" "yd-role" "Name" "role"
  "Label" (tr "field.role") "LabelKey" "field.role"
  "Placeholder" (tr "field.role_placeholder")
  "Options" (list
    (dict "Value" "admin" "Label" "管理员" "Selected" true)
    (dict "Value" "member" "Label" "成员")))}}
```

数据选项（租户名、用户名）不要传 `Key`——语言切换不应改写数据本身。

### 7.4 复选框、单选框、开关

```html
{{template "components.form.check" (dict
  "ID" "yd-active" "Name" "active" "Value" "1"
  "Label" (tr "field.active") "LabelKey" "field.active"
  "Hint" (tr "field.active_hint") "HintKey" "field.active_hint"
  "Checked" true "Block" true)}}
```

开关用于「立即生效」的二元设置；需要提交表单的普通选项用复选框。开关是原生 checkbox + CSS，
不依赖 Alpine，因此在 Alpine 启动前与 htmx 交换后都可用。

```html
{{template "components.form.toggle" (dict
  "ID" "yd-notify" "Name" "notify" "Value" "1"
  "Label" (tr "field.notify") "LabelKey" "field.notify" "Checked" .Notify)}}
```

### 7.5 分段控件

```html
{{template "components.form.segmented" (dict
  "Name" "scope" "Label" (tr "field.scope") "LabelKey" "field.scope"
  "Options" (list
    (dict "Value" "all" "Label" (tr "scope.all") "Key" "scope.all" "Checked" true)
    (dict "Value" "mine" "Label" (tr "scope.mine") "Key" "scope.mine")))}}
```

选中态由 `:has(input:checked)` 绘制，服务端只需给出初始 `Checked`。

### 7.6 口令

```html
{{template "components.form.password" (dict
  "ID" "yd-password" "Name" "password"
  "Label" (tr "field.password") "LabelKey" "field.password"
  "Hint" (tr "field.password_hint") "HintKey" "field.password_hint"
  "Autocomplete" "new-password" "Required" true "Sensitive" true)}}
```

强度提示只是本地体验，不是服务端策略；服务端仍必须执行 `guomi.HashPassword` 与长度/复杂度规则。

### 7.7 标签输入

```html
{{template "components.form.tags" (dict
  "ID" "yd-tags" "Name" "tags" "Value" "go,htmx"
  "Label" (tr "field.tags") "LabelKey" "field.tags"
  "Placeholder" (tr "field.tags_placeholder")
  "AddLabel" (tr "ui.add") "AddKey" "ui.add")}}
```

标签用逗号连接后放进隐藏字段；隐藏字段带服务端初始值，因此在 Alpine 启动前表单仍可提交。

### 7.8 搜索

```html
{{template "components.form.search" (dict
  "Action" "/mgr/users"
  "Get" "/mgr/users"
  "Target" "#yd-user-rows"
  "Name" "q" "Value" .Query
  "Placeholder" (tr "page.users.search_placeholder")
  "PlaceholderKey" "page.users.search_placeholder"
  "Label" (tr "page.users.search") "LabelKey" "page.users.search")}}
```

给了 `Get` 时，输入框以 `hx-trigger="input changed delay:250ms, search"` 请求，目标只换结果区；
清空按钮会重新触发 `input` 事件，不需要第二次请求逻辑。
`components.form.search` 自带 `<form>`，**不得嵌进另一个 `<form>`**：HTML 会丢弃或移动嵌套表单，
放大镜会跑到输入框之外（组件示例页曾因此出现错位）。

### 7.9 文件

```html
{{template "components.form.file" (dict
  "ID" "yd-avatar" "Name" "avatar"
  "Label" (tr "field.avatar") "LabelKey" "field.avatar"
  "Hint" (tr "field.avatar_hint") "HintKey" "field.avatar_hint"
  "Accept" "image/*" "Multiple" false)}}
```

真正的 `<input type="file">` 仍是表单字段，htmx `hx-encoding="multipart/form-data"` 与没有组件时完全一致。
移除文件依赖 `DataTransfer`；不支持该 API 的浏览器仍可正常提交，只是不能从列表里单独移除。

### 7.10 操作区

```html
{{template "components.form.actions" (dict
  "SubmitLabel" (tr "ui.save") "SubmitKey" "ui.save"
  "CancelLabel" (tr "ui.cancel") "CancelKey" "ui.cancel"
  "CancelHref" "/mgr/users")}}
```

抽屉内省略 `CancelHref`，取消按钮会带 `data-yd-drawer-close` 直接关闭抽屉。

---

## 8. 中国常用控件

所有中国控件都遵循同一契约：**可见输入框就是提交字段**，`data-value` 提供初始值，脚本只做净化、
校验与格式化。校验文案在语言目录里，不在脚本里。

### 8.1 手机号

```html
{{template "components.cn.phone" (dict
  "ID" "yd-phone" "Name" "phone" "Value" .Phone
  "Label" (tr "field.phone") "LabelKey" "field.phone"
  "Placeholder" (tr "field.phone_placeholder") "PlaceholderKey" "field.phone_placeholder")}}
```

- 只接受 11 位大陆手机号（`1[3-9]` 开头），输入时去除非数字。
- 提示区显示 `138 0013 8000` 形式的分组预览，但提交值仍是纯数字。
- 服务端必须独立校验；前端校验只是体验。

### 8.2 居民身份证

```html
{{template "components.cn.idcard" (dict
  "ID" "yd-idcard" "Name" "id_card" "Value" .IDCard
  "Label" (tr "field.idcard") "LabelKey" "field.idcard")}}
```

- 仅接受 18 位、带 GB 11643 校验位且出生日期有效的号码；15 位旧式号码刻意拒绝。
- 自动大写 `X`，派生出生日期、性别与年龄，显示在提示区。
- 身份证号属于敏感个人信息：表单应声明 `data-yd-encrypt="sm2"`，字段加 `data-yd-sensitive`，
  且不要写进日志或缓存键。

### 8.3 银行卡

```html
{{template "components.cn.bankcard" (dict
  "ID" "yd-bankcard" "Name" "bank_card" "Value" .BankCard
  "Label" (tr "field.bankcard") "LabelKey" "field.bankcard")}}
```

- 使用 Luhn 校验；发卡行识别是**尽力而为**的短表，未命中时显示空，不做猜测。
- 分组预览仅用于显示；提交值保持纯数字。

### 8.4 统一社会信用代码

```html
{{template "components.cn.creditcode" (dict
  "ID" "yd-credit" "Name" "credit_code" "Value" .CreditCode
  "Label" (tr "field.creditcode") "LabelKey" "field.creditcode")}}
```

- 18 位，GB 32100 校验位；字符集排除 `I O S V Z`。
- 自动大写；校验失败时给出明确提示。

### 8.5 人民币金额

```html
{{template "components.cn.amount" (dict
  "ID" "yd-amount" "Name" "amount" "Value" .Amount
  "Label" (tr "field.amount") "LabelKey" "field.amount")}}
```

- 输入保留最多两位小数，自动去掉多余符号；为空或非法时大写预览不显示。
- 大写预览（如「壹万贰仟叁佰肆拾伍元陆角柒分」）只是显示，提交值仍是十进制字符串。
- 金额计算请在服务端用整数分或定点数，不要用二进制浮点累加。

### 8.6 省市区级联

```html
<script id="yd-regions" type="application/json">
[{"code":"110000","name":"北京市","children":[
  {"code":"110100","name":"北京市","children":[
    {"code":"110101","name":"东城区"}]}]}]
</script>

{{template "components.cn.region" (dict
  "Source" "#yd-regions"
  "Province" .Province "City" .City "District" .District
  "ProvinceName" "province" "CityName" "city" "DistrictName" "district"
  "Label" (tr "field.region") "LabelKey" "field.region")}}
```

- 数据契约：数组，每项 `{code, name, children?}`，最多三级。
- 未提供 `Source` 时使用内置的 34 个省级行政区；市/区选择器会保持禁用。
- 完整省市县数据每年变化，框架刻意不内置；由应用从自己的服务端渲染进 JSON 脚本，或改为
  `hx-get` 按级取数（推荐后者用于大数据集）。
- 提交的是行政区划代码，不是名称；服务端按代码校验归属。

### 8.7 日期范围

```html
{{template "components.cn.daterange" (dict
  "From" .From "To" .To
  "FromName" "from" "ToName" "to"
  "Label" (tr "field.range") "LabelKey" "field.range")}}
```

预设：今天、近 7 天、近 30 天、本月、上月。预设按**本地日历日**计算，服务端仍是日期语义的权威。

### 8.8 短信验证码

```html
{{template "components.cn.sms" (dict
  "ID" "yd-sms" "Name" "code"
  "Label" (tr "field.sms") "LabelKey" "field.sms"
  "SendURL" "/sms/send"
  "Target" "#yd-sms-result")}}
```

- 给了 `SendURL` 时按钮经 htmx 提交；只有 2xx 响应才启动 60 秒倒计时，422/网络失败不会白白烧掉倒计时。
- 未给 `SendURL` 时按钮只启动本地倒计时，请求由应用自行处理。
- 验证码输入框带 `autocomplete="one-time-code"`，移动端可自动填充。

### 8.9 复制

```html
{{template "components.cn.copy" (dict
  "Value" .APIKey
  "Label" (tr "ui.copy") "LabelKey" "ui.copy")}}
```

也可用 `Target` 指向另一个选择器，复制其 `value` 或文本。复制失败会显示失败文案，不会静默。

---

## 9. 反馈组件

> 第 9–12 节覆盖高频基础组件；折叠、轮播、时间线、命令面板、树、图表等扩展组件的参数与示例见
> [`frontend-components.md`](frontend-components.md)。

### 9.1 提示条

```html
{{template "components.feedback.alert" (dict
  "Level" "warn"
  "Title" (tr "alert.quota_title") "TitleKey" "alert.quota_title"
  "Text" (tr "alert.quota_text") "TextKey" "alert.quota_text"
  "Actions" (template.HTML `<a class="yd-btn yd-btn--sm" href="/billing">升级</a>`))}}
```

`Level` 取 `info` / `success` / `warn` / `danger`。`danger` 使用 `role="alert"`，其余使用 `role="status"`。

### 9.2 标签

```html
{{template "components.feedback.tag" (dict
  "Tone" "success" "Label" (tr "status.active") "LabelKey" "status.active")}}
```

可删除标签传 `RemoveAttrs`（如 `hx-delete="/tags/1" hx-target="closest .yd-tag" hx-swap="delete"`）与
`RemoveKey`。

### 9.3 加载指示器与骨架屏

```html
{{template "components.feedback.spinner" (dict
  "Size" "sm" "Label" (tr "common.loading") "LabelKey" "common.loading")}}

{{template "components.feedback.skeleton" (dict
  "Label" (tr "common.loading") "LabelKey" "common.loading")}}
```

首屏骨架屏在片段到达前占位；请求中的小范围等待用 spinner + `hx-indicator`。不要把骨架屏用在
每次局部刷新上——那会让页面闪烁。

### 9.4 轻提示

```html
{{template "components.feedback.toast-region" (dict "ID" "yd-toast-region")}}
```

区域由应用脚本动态插入 `components.feedback.toast` 的结构；`aria-live="polite"`，不抢焦点。

---

## 10. 导航组件

### 10.1 服务端页签

```html
{{template "components.navigation.tabs" (dict
  "Label" (tr "page.users.tabs") "LabelKey" "page.users.tabs"
  "Items" (list
    (dict "Label" (tr "page.users.all") "LabelKey" "page.users.all"
          "Href" "/mgr/users" "Active" true
          "Attrs" (template.HTMLAttr `hx-get="/mgr/users" hx-target="#yd-workspace" hx-swap="innerMorph" hx-push-url="true"`))
    (dict "Label" (tr "page.users.disabled") "LabelKey" "page.users.disabled"
          "Href" "/mgr/users?status=disabled"))) }}
```

页签是链接，由服务端决定当前项；URL、权限与数据都留在服务端。纯客户端面板才用 `ydTabs`。

### 10.2 面包屑

```html
{{template "components.navigation.breadcrumb" (dict
  "Label" (tr "nav.breadcrumb") "LabelKey" "nav.breadcrumb"
  "Items" (list
    (dict "Label" (tr "nav.home") "LabelKey" "nav.home" "Href" "/mgr")
    (dict "Label" (tr "nav.users") "LabelKey" "nav.users" "Current" true))) }}
```

### 10.3 步骤条

```html
{{template "components.navigation.steps" (dict
  "Label" (tr "wizard.steps") "LabelKey" "wizard.steps"
  "Items" (list
    (dict "Index" 1 "Label" (tr "wizard.identity") "LabelKey" "wizard.identity" "State" "done")
    (dict "Index" 2 "Label" (tr "wizard.contact") "LabelKey" "wizard.contact" "State" "current")
    (dict "Index" 3 "Label" (tr "wizard.confirm") "LabelKey" "wizard.confirm"))) }}
```

只有真正的先后流程才用步骤条；并列分类用页签或分段控件。

### 10.4 Alpine 下拉

```html
{{template "components.navigation.dropdown" (dict
  "Label" (tr "ui.actions") "LabelKey" "ui.actions"
  "PanelLabel" (tr "ui.actions") "PanelLabelKey" "ui.actions"
  "TriggerClass" "yd-btn yd-btn--ghost yd-btn--sm"
  "Items" (list
    (dict "Label" (tr "ui.edit") "LabelKey" "ui.edit" "Href" "/mgr/users/1/edit")
    (dict "Label" (tr "ui.delete") "LabelKey" "ui.delete" "Href" "#" "Danger" true
          "Attrs" (template.HTMLAttr `hx-delete="/mgr/users/1" hx-target="closest tr" hx-swap="delete" data-yd-confirm="确定删除？"`)))) }}
```

普通行内操作菜单可以继续用 `<details class="yd-menu">`，它不需要脚本；需要焦点管理与键盘导航时用下拉。

---

## 11. 数据展示

### 11.1 头像与统计值

```html
{{template "components.data.avatar" (dict "Initials" "YD" "Size" "lg" "Label" .Name "LabelKey" "user.name")}}

{{template "components.data.stat" (dict
  "Label" (tr "metrics.requests") "LabelKey" "metrics.requests"
  "Value" "12,340" "Delta" "+3.2%" "Trend" "up")}}
```

### 11.2 表格外壳

```html
{{template "components.data.table.open" (dict
  "Caption" (tr "page.users.table") "CaptionKey" "page.users.table"
  "Compact" true
  "Columns" (list
    (dict "Label" (tr "field.user") "LabelKey" "field.user")
    (dict "Label" (tr "field.email") "LabelKey" "field.email")
    (dict "Label" (tr "field.status") "LabelKey" "field.status")
    (dict "Label" (tr "field.actions") "LabelKey" "field.actions" "Actions" true))
  "BodyID" "yd-user-rows")}}
  {{range .Users}}
  <tr class="yd-table__row" id="yd-user-{{.ID}}">…</tr>
  {{end}}
{{template "components.data.table.close" .}}
```

表体默认 `hx-swap="outerMorph"`，行刷新保留焦点与未提交输入。数值列加 `Numeric`，右对齐并使用等宽数字。

---

## 12. 浮层与对话框

- **新增/编辑**：一律用外壳提供的右侧抽屉 `#yd-drawer`，不要新建第二套抽屉。
- **变更确认**：一律 `data-yd-confirm`，包括退出、删除、启停、轮换、重置、创建、保存、切换租户与修改默认租户。
- **模态**：只有既不是抽屉也不是确认框的场景才用 `components.overlay.modal.open/close`。

```html
<button type="button" class="yd-btn" data-yd-modal-open="yd-help">帮助</button>

{{template "components.overlay.modal.open" (dict
  "ID" "yd-help" "Title" (tr "help.title") "TitleKey" "help.title" "Size" "lg")}}
  <p class="yd-dialog__text" data-yd-i18n="help.body">{{tr "help.body"}}</p>
{{template "components.overlay.modal.close" .}}
```

模态内容仍在页面内，`yd-app.js` 负责显示、焦点与关闭；不要在模态里再放一个 `<dialog>`。

---

## 13. Alpine 行为组件参考

所有组件经 `Alpine.data` 注册，模板写 `x-data="<name>"`。CSP 构建支持属性访问、比较、三元、赋值与方法调用；
**不支持**箭头函数、解构、模板字符串、`console`/`document`/`JSON` 等全局对象。

**状态必须可 JSON 序列化**：`hx-history-cache` 在保存页面快照前会把每个 `[x-data]` 作用域交给
`hx-alpine-compat` 做 `JSON.stringify`。因此 `x-data` 返回的对象只能放原始值、普通数组/对象与函数；
EasyMDE 等第三方实例、DOM 节点与任何带循环引用的对象必须留在模块级 `WeakMap` 或 DOM 属性上
（参考 `editorInstances` / `editorFor` / `rememberEditor`）。循环引用会让历史快照抛错并中断整次换入，
表现为「点击语言没有任何反应」。

| 组件 | 作用 | 主要方法 / 状态 | 派发事件 |
|---|---|---|---|
| `ydCopy` | 复制文本 | `copy`、`copied`、`error` | `yd:copy` |
| `ydDropdown` | 可访问菜单 | `toggle`、`close(restore)`、`onKey(event)`、`move` | — |
| `ydTabs` | 客户端页签 | `select(id)`、`isActive(id)`、`move(event, step)`、`onKey(event)` | `yd:tabs:change` |
| `ydTableSelect` | 表体全选（部分选中为 indeterminate） | `toggleAll`、`sync`、`all`、`partial`、`count` | — |
| `ydSearch` | 搜索清空并重触发 | `clear`、`value` | `yd:search:clear` |
| `ydPassword` | 显示/隐藏与强度 | `toggle`、`score()`、`label()` | — |
| `ydTagInput` | 标签增删 | `add`、`remove(index)`、`removeTag(tag)`、`value()` | `yd:tags:change` |
| `ydUpload` | 拖放与文件列表 | `onChange`、`onDrop`、`remove(file)`、`sizeText(n)` | — |
| `ydPhone` | 手机号净化/校验 | `onInput`、`valid()`、`formatted()`、`state()` | — |
| `ydIDCard` | 身份证校验/派生 | `onInput`、`valid()`、`birth()`、`sex()`、`age()` | — |
| `ydBankCard` | 银行卡 Luhn/发卡行 | `onInput`、`valid()`、`bank()`、`formatted()` | — |
| `ydCreditCode` | 统一社会信用代码 | `onInput`、`valid()`、`state()` | — |
| `ydAmount` | 金额净化/大写 | `onInput`、`upper()`、`valid()` | — |
| `ydRegion` | 省市区级联 | `onProvince`、`onCity`、`onDistrict`、`complete()` | `yd:region:change` |
| `ydDateRange` | 日期范围与预设 | `apply(name)`、`manual()`、`within(name)` | `yd:daterange:change` |
| `ydSmsCode` | 短信倒计时 | `start()`、`busy()`、`label()` | — |
| `ydCarousel` | 轮播吸附（手动滑动同步圆点） | `go(index)`、`next`、`prev`、`onKey`、`active` | — |
| `ydSlider` | 滑块数值镜像 | `value` | — |
| `ydWatermark` | 水印重复铺满 | `items()` | — |
| `ydAnchor` | 同页锚点滚动高亮 | `isActive(href)`、`active` | — |
| `ydBackTop` | 滚动后显示回到顶部 | `top`、`visible` | — |
| `ydCommand` | 命令面板（默认 Ctrl/Cmd+K，含焦点陷阱） | `show`、`close(restore)`、`move`、`onKey`、`onQuery` | — |
| `ydContextMenu` | 按指针或键盘定位的右键菜单 | `openAt(event)`、`close(restore)`、`onKey`、`move` | — |
| `ydCountdown` | 基于截止时间的倒计时 | `start`、`stop`、`tick()`、`label()`、`seconds` | `yd:countdown:done` |
| `ydNumberAnimation` | 指标数字滚动 | `display` | — |
| `ydNumber` | 数字输入步进 | `step(delta)`、`value` | — |
| `ydOtp` | 验证码逐位输入 | `onInput`、`onKeydown`、`code` | — |
| `ydColor` | 取色器与文本同步 | `fromPicker`、`fromText`、`value` | — |
| `ydCombobox` | 可搜索单选（完整键盘契约） | `show`、`pick(el)`、`onInput`、`onKeydown`、`close(restore)`、`value` | `yd:combobox:change` |
| `ydEditor` | EasyMDE 初始化与主题同步 | `syncTheme`、`editor` | — |

> 每个组件接受的模板 `dict` 键、数据结构与最佳实践见
> [`frontend-components.md`](frontend-components.md)；本表只列行为名与常用状态/方法。

### 13.1 客户端页签示例

```html
<div class="yd-tabs" x-data="ydTabs" data-active="profile">
  <div class="yd-tabs__list" role="tablist">
    <button type="button" class="yd-tabs__tab" role="tab" data-tab="profile"
            x-on:click="select('profile')" x-bind:aria-selected="isActive('profile')"
            x-on:keydown.arrow-right.prevent="move($event, 1)"
            x-on:keydown.arrow-left.prevent="move($event, -1)">资料</button>
    <button type="button" class="yd-tabs__tab" role="tab" data-tab="security"
            x-on:click="select('security')" x-bind:aria-selected="isActive('security')">安全</button>
  </div>
  <div class="yd-tabs__panel" role="tabpanel" x-show="isActive('profile')">…</div>
  <div class="yd-tabs__panel" role="tabpanel" x-show="isActive('security')" x-cloak>…</div>
</div>
```

### 13.2 表体全选示例

```html
<div x-data="ydTableSelect">
  <table class="yd-table">
    <thead><tr><th><input type="checkbox" x-model="all" x-on:change="toggleAll" aria-label="全选"></th>…</tr></thead>
    <tbody>
      <tr><td><input type="checkbox" name="ids" value="1" x-on:change="sync"></td>…</tr>
    </tbody>
  </table>
  <p class="yd-field__hint" x-text="count"></p>
</div>
```

### 13.3 标签输入示例（不使用局部模板时）

```html
<div class="yd-field" x-data="ydTagInput" data-value="go,htmx">
  <input class="yd-input" x-model="draft" x-on:keydown.enter.prevent="add">
  <input type="hidden" name="tags" value="go,htmx" x-bind:value="value()">
  <div class="yd-tag-list">
    <template x-for="tag in tags" :key="tag">
      <span class="yd-tag"><span x-text="tag"></span>
        <button type="button" class="yd-tag__remove" x-on:click="removeTag(tag)">×</button></span>
    </template>
  </div>
</div>
```

---

## 14. 校验与格式化 API（ydCN）

`window.ydUI.CN` 与 `window.ydCN` 指向同一对象；Node 测试通过 `require` 取得同一份实现。

| 方法 | 返回 | 说明 |
|---|---|---|
| `ydCN.phone(v)` | `bool` | 11 位大陆手机号 |
| `ydCN.phoneFormatted(v)` | `string` | `3-4-4` 分组预览 |
| `ydCN.idCard(v)` | `bool` | 18 位身份证 + GB 11643 校验位 + 出生日期 |
| `ydCN.idCardInfo(v)` | `{birth, age, sex} \| null` | 派生信息 |
| `ydCN.luhn(v)` | `bool` | 12–19 位 Luhn |
| `ydCN.bankCard(v)` | `bool` | 银行卡（Luhn） |
| `ydCN.bankName(v)` | `string` | 已知 BIN 的发卡行，未知为空 |
| `ydCN.bankCardFormatted(v)` | `string` | 每四位分组预览 |
| `ydCN.creditCode(v)` | `bool` | 18 位统一社会信用代码 |
| `ydCN.sanitizeAmount(v)` | `string` | 去非法字符、保留一位小数点与两位小数 |
| `ydCN.amountToChinese(v)` | `string` | 人民币中文大写；非法输入返回空串 |
| `ydCN.provinces` | `Array` | 内置 34 个省级行政区 |

```js
if (!ydCN.phone(input.value)) {
  // 服务端仍要再校验一次
}
```

**不要在 Alpine 表达式里调用 `JSON`、`Math`、`document` 等全局对象**；需要时把逻辑放进 `yd-ui.js` 的方法。

---

## 15. 表单最佳实践

### 15.1 失败契约

```html
<form class="yd-form"
      hx-post="/mgr/users"
      hx-target="#yd-workspace" hx-swap="innerMorph"
      hx-status:400="target:#yd-drawer-errors swap:innerHTML"
      hx-status:409="target:#yd-drawer-errors swap:innerHTML"
      hx-status:422="target:#yd-drawer-errors swap:innerHTML"
      hx-disabled-elt="find button[type='submit']"
      data-yd-confirm="{{tr "confirm.create_user"}}"
      novalidate>
```

- 服务端校验失败返回 **422 + 错误片段**；片段换入抽屉内的错误槽位，表单与用户输入保持不动。
- `novalidate` 让服务端规则真正被演练；需要浏览器原生校验时再移除它，但不要两套规则各写一半。
- 提交期间用 `hx-disabled-elt` 禁用按钮，避免重复提交。
- 所有变更动作都要 `data-yd-confirm`；确认后 `yd-app.js` 用 `requestSubmit(submitter)` 重新提交，
  多按钮表单的 `name/value` 不会丢。

### 15.2 敏感字段

```html
<form data-yd-encrypt="sm2" data-yd-pubkey="{{.KeyEndpoint}}" …>
  <input class="yd-input" type="password" name="password" data-yd-sensitive>
</form>
```

口令、身份证号、银行卡号等字段必须由前端 SM2 加密后提交，服务端用 `guomi.DecryptRaw` 解密。
前端加密不能替代 HTTPS，也不能替代服务端的哈希与校验。

### 15.3 状态保留

- 抽屉/工作区整体刷新用 `innerMorph`：保留未提交输入与滚动位置。
- 行级刷新用 `outerMorph`：保留行内焦点与未提交输入。
- 只有确实需要丢弃旧状态时才用 `outerHTML`，并在注释里说明原因。

### 15.4 失败与空状态

- 校验失败：字段级错误 + 顶部错误槽位；不要用 `alert()`。
- 空列表：`components.layout.empty`，给出下一步操作。
- 网络失败：`#yd-connection` 横幅由 `yd-app.js` 统一管理，不要另写一套。

---

## 16. 国际化（i18n）

### 16.1 三种钩子

| 位置 | 写法 |
|---|---|
| 元素文本 | `<span data-yd-i18n="key">{{tr "key"}}</span>` |
| 属性值 | `<input data-yd-i18n-attr="placeholder:key" placeholder="{{tr "key"}}">` |
| 组件入参 | 传 `LabelKey` / `HintKey` / `ErrorKey`；同时传 `Label` / `Hint` / `Error` 可获得无脚本回退 |
| 组件内固定文案 | 组件模板内一律 `data-yd-i18n` + `tr`，默认键在框架与脚手架两份目录中预置 |

语言切换是**局部刷新**：htmx 重取 `#yd-shell`，侧栏、顶栏与工作区一起跟随；不要整页跳转。
语言按钮使用 `hx-swap="innerHTML"` 而不是 morph：整块外壳重新插入后，每个 Alpine 组件都会获得
全新的作用域，不会留下绑定到旧 `x-data` 的表达式；其余工作区刷新仍使用 `innerMorph` 以保留输入状态。
`data-yd-i18n` 与 `data-yd-i18n-attr` 让脚本能在不重新请求的情况下改写文案，两者应同时存在。

> **键即可渲染**：表单与中国控件在只提供 `LabelKey`/`HintKey`/`ErrorKey` 时也会从目录取出可见文案
> （`TestComponentsRenderFromI18nKeysOnly` 守卫）；同时传渲染好的值是为了在脚本未运行或目录缺失时
> 仍有服务端回退。所有组件的固定文案（上传提示、校验提示、日期预设、分页、复制结果、编辑器工具栏）
> 都有默认键，不需要调用方另抄一份。

### 16.2 文案规则

- 模板里不写可见字面文案；固定文案走 `tr`，数据（用户名、租户名）直接输出。
- 新增键必须同时写进 `web/i18n/zh-CN.yaml` 与 `en.yaml`；生成项目对应 `web/i18n/*.yaml`。
- 动态键（`{{tr .LabelKey}}`）无法静态枚举，调用方负责保证键存在。
- 组件内的固定文案（上传提示、校验提示、日期预设）已经带键；新增组件必须补键。

---

## 17. 权限与安全

- **前端判定不是安全边界**。`data-yd-perm`、`yd.can()`、`can` 模板函数只让页面不提供「服务端本来就会拒绝」的操作；
  每条受保护路由仍必须挂 `authz.Require` / `authz.Allow`。
- 载荷缺失或损坏时**失败关闭**：隐藏全部受守卫控件，而不是放开。
- **CSP**：`script-src 'self'`，不含 `unsafe-eval` / `unsafe-inline`。因此：
  - 只用 Alpine 的 CSP 构建；
  - 不写内联 `<script>`、不写 `onclick=`；
  - 不在 Alpine 表达式里写箭头函数/模板字符串/全局对象；
  - 不引入 CDN、不写 `integrity=`。
- **输出转义**：模板默认转义；需要输出已净化的 HTML 时使用 `template.HTML`，且来源必须是服务端净化过的
  （如消息中心的 `BodyHTML`）。不要信任客户端提交的 HTML。
- **敏感字段**：SM2 前置加密 + 服务端解密；口令只存 `guomi.HashPassword` 结果；`sec_key` 不得进日志。
- **上传**：MIME 由服务端嗅探，白名单不含 HTML/SVG/脚本；存储键不含原始文件名。

---

## 18. 可访问性

- 每个输入都有 `<label for>`；必填用文字标记（`*`），不只靠颜色。
- 错误用 `role="alert"` + `aria-invalid="true"`；不要只画红框。
- 菜单/页签/对话框保留键盘路径：`Escape` 关闭、方向键移动、焦点回到触发者。
- `x-cloak` 用于所有初始隐藏的 Alpine 节点，避免首帧闪现。
- 图标按钮必须有 `aria-label`；装饰图标 `aria-hidden="true"`。
- 尊重 `prefers-reduced-motion`：组件层已关闭过渡与旋转，不要用 JS 绕过。
- 对比度：状态色与浅底令牌成对定义；新颜色必须在明暗两套主题下检查。

---

## 19. 新增一个组件

按顺序完成，缺一项视为未完成：

1. **令牌**：需要新颜色/尺寸时，先在 `components.css` 的 `@theme`、`:root`、`[data-theme="dark"]` 三处声明。
2. **样式**：在 `ui.css` 的 `@layer components` 中新增 `.yd-<block>`、`__element`、`--modifier`；不得写颜色字面量。
3. **模板**：在 `web/templates/components/<category>.html` 新增 `{{define}}`，文件头写双语契约（dict 键、可访问性、渐进增强）。
4. **行为**（仅需时）：在 `yd-ui.js` 用 `Alpine.data` 注册，遵守 CSP 表达式子集；不写 HTTP。
5. **文案**：在 `web/i18n/zh-CN.yaml`、`en.yaml` 与脚手架同名目录补键。
6. **测试**：
   - `web/components_test.go` 的 `componentFixture` 补固定数据；
   - 有逻辑时补 `web/static/js/yd-ui.test.mjs`；
   - 确认 `TestEveryYdClassUsedInTemplatesIsDefined`、`TestNoHardcodedColorsInAppCSS`、
     `TestEveryTrKeyHasBothCatalogs` 通过；
   - 生成项目侧确认 `TestGeneratedStylesDefineEveryYdClassUsedByTemplates` 通过。
7. **文档**：在本手册补「用途 + 最小示例 + 最佳实践 + 常见错误」。
8. **产物**：`go run ./cmd/yd assets minify`，提交 `.min.js` / `.min.css` / `.min.svg` 变化。

---

## 20. 测试与守卫

```bash
# 组件模板渲染、CSS 类覆盖、色彩守卫、i18n 键、JS 套件
go test ./web -count=1

# 生成项目的组件复制、脚本顺序、类覆盖、i18n、e2e 相关单测
go test ./tooling/scaffold -count=1

# 只跑前端内核
node web/static/js/yd-ui.test.mjs

# 重新生成压缩资产
go run ./cmd/yd assets minify

# 离线校验资产完整性
go run ./cmd/yd assets verify --offline

# 全量门禁
go build ./... && go vet ./... && go test ./... -race
```

守卫说明：

| 守卫 | 拦截什么 |
|---|---|
| `TestEveryYdClassUsedInTemplatesIsDefined` | 模板用了但样式表没定义的 `yd-` 类 |
| `TestNoHardcodedColorsInAppCSS` | 令牌块之外的颜色字面量（含 `ui.css`） |
| `TestEveryTrKeyHasBothCatalogs` | 只存在于一种语言的 `tr` 键 |
| `TestEveryUserFacingTextHasI18nAttribute` | 页面级字面文案与缺钩子的 `tr` |
| `TestEveryComponentRenders` | 组件模板语法、缺失字段、空输出 |
| `TestChinaComponentsKeepSubmittedFields` | 中国控件在没有 Alpine 时是否仍提交 |
| `TestGeneratedUILoadsAlpineAndTheKitInOrder` | 生成项目把 Alpine 排在 UI 层之前 |
| `TestUIJSAssetStaysCSPCompatible` | 资产里出现 `new Function` / `eval` / CDN |
| `TestRepositorySpriteMatchesTemplates` | 图标 sprite 与模板引用漂移 |

---

## 21. 常见问题排查

### 21.1 页面报 `Undefined variable: ydPhone`

`yd-ui.js` 排在了 Alpine 之后。CSP 构建立即启动，注册表必须在前。检查布局的脚本顺序，
两段脚本都要有 `defer`，且 `yd-ui.js` 在 `alpine.min.js` 之前。

### 21.2 Alpine 表达式报错或静默不执行

CSP 构建不支持箭头函数、解构、模板字符串、`console`/`document`/`JSON` 等全局对象，也不支持 `x-html`。
把逻辑移进 `Alpine.data` 的方法或 getter。

### 21.3 样式不生效

- 新类是否写在 `@layer components` 内？
- 项目是否重新编译了 Tailwind（`yd dev` / `yd build`）？
- 类名是否只出现在脚本里而 `app.css` 的 `@source` 没覆盖脚本？本项目已覆盖 `static/js/yd-*.js`。
- 生成项目是否复制了 `ui.css` 并在 `app.css` 中 `@import`？

### 21.4 暗色主题下对比度不对

组件层里出现了颜色字面量。所有颜色必须来自令牌；`TestNoHardcodedColorsInAppCSS` 会拦截。
第三方 CSS（如 EasyMDE）的覆盖必须放在层外，并用容器类提高特异性。

### 21.5 下拉/菜单被表格裁切

用 `<details class="yd-menu">`（`yd-app.js` 会提升为 `fixed` 定位），或把菜单放到表格容器之外。
不要把绝对定位菜单留在 `overflow-x: auto` 的容器里。

### 21.6 表单提交后输入丢失

`hx-swap` 用了 `outerHTML`。改成 `innerMorph` 或 `outerMorph`；行级刷新用 `outerMorph`。

### 21.7 省市区没有市/区可选

- 未提供 `Source`：内置数据只有省级，市/区本来为空；请注入完整 JSON 或改用 htmx 按级取数。
- JSON 结构不对：必须是 `[{code, name, children?}]`。
- 选择器 id 与 `Source` 不匹配：`querySelector` 找不到脚本元素。

### 21.8 短信倒计时不启动

- 按钮的 htmx 请求返回的不是 2xx；只有 2xx 才启动。
- htmx 事件名不是 `htmx:after:request`（htmx 4 使用冒号命名）。
- 按钮没有 `SendURL` 时只做本地倒计时，请求需要应用自己处理。

### 21.9 页面在 125% / 150% 缩放下错位

- 控件用了固定 `height` 或 `px` 字号；改为 `min-height` + rem。
- 用了 `100vh`；改为 `100dvh`。
- 栅格用了固定列宽；改为 `minmax(min(100%, …), 1fr)`。

### 21.10 CSP 拦截了脚本

检查是否出现内联 `<script>`、`onclick=`、`new Function`、`eval`、`x-html`。
Alpine 必须使用 CSP 构建；`yd-ui.js` 与所有自有脚本都必须同源。

---

## 22. 从旧写法迁移

旧页面里的 `.yd-form__field` + `.yd-form__label` + `.yd-hint` 仍然可用，但新代码请使用统一字段契约：

| 旧写法 | 新写法 |
|---|---|
| `<div class="yd-form__field">` | `components.form.field`（`yd-field`） |
| `<label class="yd-form__label">` | 组件内的 `yd-field__label` |
| `<span class="yd-hint">` | 组件的 `Hint` / `HintKey` |
| `<label class="yd-check">` 手写 | `components.form.check` |
| 手写密码框 | `components.form.password` |
| 手写提交/取消按钮 | `components.form.actions` |

迁移时保持原有 `name`、`id`、`hx-*` 与 `data-yd-*` 不变；只替换外观与可访问性管线。

---

## 23. 完整示例

```html
{{- define "mgr/partials/demo/form" -}}
<form class="yd-form" method="post" action="/mgr/demo"
      hx-post="/mgr/demo" hx-target="#yd-workspace" hx-swap="innerMorph"
      hx-status:400="target:#yd-drawer-errors swap:innerHTML"
      hx-status:422="target:#yd-drawer-errors swap:innerHTML"
      hx-disabled-elt="find button[type='submit']"
      data-yd-confirm="{{tr "confirm.save"}}"
      novalidate>
  {{template "components.form.field" (dict
    "ID" "yd-demo-name" "Name" "name" "Value" .Name
    "Label" (tr "field.name") "LabelKey" "field.name"
    "Required" true "MaxLength" 64)}}
  {{template "components.cn.phone" (dict
    "ID" "yd-demo-phone" "Name" "phone" "Value" .Phone
    "Label" (tr "field.phone") "LabelKey" "field.phone")}}
  {{template "components.cn.amount" (dict
    "ID" "yd-demo-amount" "Name" "amount" "Value" .Amount
    "Label" (tr "field.amount") "LabelKey" "field.amount")}}
  {{template "components.cn.daterange" (dict
    "From" .From "To" .To "FromName" "from" "ToName" "to"
    "Label" (tr "field.range") "LabelKey" "field.range")}}
  {{template "components.form.actions" (dict
    "SubmitLabel" (tr "ui.save") "SubmitKey" "ui.save"
    "CancelLabel" (tr "ui.cancel") "CancelKey" "ui.cancel")}}
</form>
{{- end -}}
```

对应控制器只需照常读取 `name`、`phone`、`amount`、`from`、`to`；`amount` 是十进制字符串，
服务端用整数分或定点数落库。校验失败返回 422 + `partials/form.error-items`；成功返回刷新后的片段。

---

> 组件层的目标不是「把所有东西都包一层」，而是让每个页面在**布局、表单、反馈、导航、数据展示**上
> 有一致且可预测的默认值，同时把真正的差异留给业务。遇到本手册没覆盖的场景，先在 `ui.css` / `yd-ui.js`
> 里补一个通用组件，再在页面里使用——不要把一次性写法复制到第十个页面。
