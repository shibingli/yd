# yd 组件全景与使用手册

> 对齐参考：Naive UI 2.x（约 90 组件）、Shadcn UI（约 56 组件）、OpenTiny TinyVue 3.31（104 组件）、
> Premium 侧以 HeroUI Pro（47 组件，含 9 类图表）为质量参照。
>
> 基础栈：**Htmx 4.0.0 · Tailwind CSS v4.3 · Alpine.js 3.17.5（CSP 构建）**
>
> 本文件是 [`frontend-ui.md`](frontend-ui.md) 的完整组件目录与扩展用法。前者讲分层、主题、响应式与
> 表单契约，本文件回答「参考库里那个组件，在 YD 里对应什么、怎么用、什么时候不该用」。

> **门户与后台共用同一套组件**：`yd new` 生成的项目里，`web/templates/components/**` 与
> `web/templates/partials/**` 只有一份，门户与管理控制台都调用它；两侧的差异由 `.Portal` / `.Mgr`
> 数据键选择侧栏与少量入口，不复制组件或外壳。完整目录约定见
> [`frontend-ui.md` §2.4](frontend-ui.md#24-门户与后台共用同一套组件)。
>
> 生成项目的门户自带 `/ui-kit` 组件示例页，把本文列出的全部控件摆在同一页；新增组件必须同步加入该页。

---

## 0. 状态图例

| 标记 | 含义 |
|---|---|
| ✅ | 已实现：有样式、模板、行为（如需要）、测试与本文用法 |
| 🧩 | 组合实现：用现有原语 + htmx/Alpine 组合，不单独做组件 |
| ⏳ | 明确规划：交互模型需要专门权衡，本文给出替代方案与边界 |
| ❌ | 不适用：属于业务应用/第三方引擎，不是框架 UI 原语 |

**判断原则**：能在服务端渲染、无脚本或极少脚本完成的，做成组件；需要第二套状态模型或第三方引擎的，
先用 htmx/Alpine 组合并在本文说明，不硬造一个半成品。

---

## 目录

1. [完整组件矩阵](#1-完整组件矩阵)
2. [显示组件用法](#2-显示组件用法)
3. [导航组件用法](#3-导航组件用法)
4. [表单新增组件](#4-表单新增组件)
5. [重型组件与替代方案](#5-重型组件与替代方案)
6. [图表用法](#6-图表用法)
7. [组件选择速查](#7-组件选择速查)
8. [最佳实践](#8-最佳实践)
9. [测试与维护](#9-测试与维护)

---

## 1. 完整组件矩阵

### 1.1 反馈（Feedback）

| 参考组件 | YD 实现 | 状态 | 推荐场景 |
|---|---|---|---|
| Alert | `components.feedback.alert` | ✅ | 页面内状态说明、警告、带操作提示 |
| Alert Dialog | 全局 `data-yd-confirm` + `components.overlay.modal.*` | ✅ | 删除/启停/轮换等高风险变更 |
| Message | `components.feedback.message` / `.yd-flash` | ✅ | 一次性行内结果 |
| Notification | `components.feedback.notification` / `.yd-toast` | ✅ | 带标题的异步结果 |
| Popconfirm | `components.feedback.popconfirm` | ✅ | 就地二次确认 |
| Progress | `components.display.meter`（线性）、`chart.ProgressRing`（环形） | ✅ | 上传、配额、任务进度 |
| Result | `components.display.result` | ✅ | 成功/失败/无权限/404 页面 |
| Skeleton | `components.feedback.skeleton` | ✅ | 首屏占位 |
| Spin | `components.feedback.spinner` | ✅ | 局部加载 |
| Toast / Sonner | `#yd-flash` + `components.feedback.toast` | ✅ | 右下/右上轻提示 |
| Tag / Chip | `components.feedback.tag` / `.yd-tag` | ✅ | 状态、筛选条件、已选值 |
| Empty | `components.layout.empty` | ✅ | 空列表、空搜索、无权限提示 |
| LoadingBar | 全局 `#yd-progress` + `components.display.meter` | ✅ | 顶部请求进度 / 局部进度 |

### 1.2 导航（Navigation）

| 参考组件 | YD 实现 | 状态 | 推荐场景 |
|---|---|---|---|
| Affix | `components.navigation.affix`（`position: sticky`） | ✅ | 工具栏、表头、筛选条 |
| Anchor | `components.navigation.anchor` + `ydAnchor` 滚动高亮 | ✅ | 长文档、设置页目录 |
| BackTop | `components.navigation.back-top` + `ydBackTop` | ✅ | 长列表/长文 |
| Breadcrumb | `components.navigation.breadcrumb` | ✅ | 层级导航 |
| Button | `.yd-btn`（primary / ghost / danger / sm） | ✅ | 所有动作 |
| Command | `components.navigation.command` + `ydCommand`（Ctrl/Cmd+K） | ✅ | 全局搜索、快捷跳转 |
| ContextMenu | `components.navigation.context-menu` + `ydContextMenu` | ✅ | 表格行、画布对象 |
| Dropdown | `components.navigation.dropdown` / `details.yd-menu` | ✅ | 行操作、用户菜单 |
| FloatButton / FAB | `components.navigation.fab` | ✅ | 移动端主操作 |
| Icon | `partials/icon` + `web/static/img/icons.svg` | ✅ | 离线图标，按需 sprite |
| Link | 原生 `<a>` + `--color-accent` / 语义令牌 | 🧩 | 行内链接 |
| Menu | `components.navigation.menu` / 外壳侧栏 | ✅ | 侧栏、设置导航 |
| Menubar | `components.navigation.menubar`（原生 details） | ✅ | 桌面式多级菜单 |
| Navbar | 外壳 `#yd-shell` 顶栏 | 🧩 | 站点级导航，框架已提供 |
| Navigation Menu | `components.navigation.menu` / `.yd-tabs` | ✅ | 顶栏/页签式导航 |
| PageHeader | `components.layout.page-header` | ✅ | 页面标题与主操作 |
| Pagination | `components.navigation.pagination` | ✅ | 服务端分页 |
| Steps | `components.navigation.steps` | ✅ | 线性流程 |
| Tabs | `components.navigation.tabs`（服务端）/ `ydTabs`（客户端） | ✅ | 分类内容 |
| Tree / TreeMenu | `components.navigation.tree`（嵌套 details） | ✅ | 目录、权限树、组织架构 |
| Sidebar | 外壳 `layouts/shell.html` | 🧩 | 站点级导航，框架已提供 |
| Stepper | `components.navigation.steps` | ✅ | 同 Steps |

### 1.3 数据录入（Data Entry）

| 参考组件 | YD 实现 | 状态 | 推荐场景 |
|---|---|---|---|
| AutoComplete / Combobox | `components.form.combobox` + `ydCombobox` | ✅ | 可搜索单选、角色/租户选择 |
| Cascader | `components.cn.region`（中国省市区） | ✅ | 中国地址；通用级联见 §5 |
| Checkbox | `components.form.check` | ✅ | 多选 |
| ColorPicker | `components.form.color` + `ydColor`（原生取色器） | ✅ | 主题色、标签色 |
| DatePicker | `components.form.date`（原生 date） | ✅ | 单日期 |
| DateInput / TimeInput | `components.form.date` / `form.time` / `form.datetime` | ✅ | 日期、时间与 `datetime-local` 输入 |
| DateRange | `components.cn.daterange` | ✅ | 报表时间范围 |
| DynamicInput | `components.form.tags` / 规划中的 `ydDynamicRows` | 🧩/⏳ | 标签用 Tags；可增删行见 §5 |
| DynamicTags | `components.form.tags` + `ydTagInput` | ✅ | 标签、收件人 |
| Form | `components.form.*` + 15 节表单契约 | ✅ | 所有表单 |
| Input | `components.form.field` | ✅ | 单行文本 |
| InputGroup | `.yd-input-group` + `components.form.field` | ✅ | 前缀/后缀/按钮贴合 |
| InputNumber | `components.form.number` + `ydNumber` | ✅ | 数量、阈值 |
| InputOTP | `components.form.otp` + `ydOtp` | ✅ | 短信/邮箱验证码 |
| Label | `components.form.*` 的 `Label` / `LabelKey` | ✅ | 表单标签 |
| Mention | — | ⏳ | 富文本 @ 提及；见 §5 |
| Popselect | `components.navigation.dropdown` + 表单片段 | 🧩 | 浮层里放多选/确认 |
| Radio | `components.form.radio` | ✅ | 单选 |
| Rate | `components.display.rate` | ✅ | 评分 |
| Select | `components.form.select` | ✅ | 原生单选 |
| Slider | `components.display.slider` + `ydSlider` | ✅ | 数值范围 |
| Switch | `components.form.toggle`（纯 CSS） | ✅ | 即时开关 |
| Toggle Group | `components.form.segmented` | ✅ | 分段选择 |
| TimePicker | `components.form.time`（原生 time） | ✅ | 单时间 |
| Transfer | — | ⏳ | 双列表穿梭；见 §5 |
| TreeSelect | — | ⏳ | 通用异步树选择；中国地址用 Region |
| Upload | `components.form.file` + `ydUpload` | ✅ | 拖放上传、附件 |
| Calendar | 原生日期 + 服务端月历模式 | 🧩/⏳ | 排班/日程见 §5 |

### 1.4 数据展示（Data Display）

| 参考组件 | YD 实现 | 状态 | 推荐场景 |
|---|---|---|---|
| Avatar | `components.data.avatar` | ✅ | 用户/租户 |
| AvatarGroup | `components.display.avatar-group` | ✅ | 参与者、成员 |
| Badge | `components.display.badge` / `.yd-badge` | ✅ | 状态、计数、圆点 |
| Card | `components.layout.card.*` / `.yd-card` | ✅ | 内容面板 |
| Carousel | `components.display.carousel` + `ydCarousel` | ✅ | 引导、图片、公告 |
| Code | `components.display.code` | ✅ | 代码块 + 复制 |
| Collapse / Accordion | `components.display.accordion`（原生 details） | ✅ | FAQ、分组表单 |
| Collapsible | `components.display.accordion` | ✅ | 单个可折叠区块 |
| Countdown | `components.display.countdown` + `ydCountdown` | ✅ | 截止时间、重发 |
| DataTable | `components.data.table.*` + `sort-header` + `ydTableSelect` | ✅ | 列表、批量操作 |
| Descriptions | `components.display.descriptions` | ✅ | 详情键值对 |
| Ellipsis | `components.display.ellipsis` | ✅ | 长文本截断 |
| Equation | — | ❌ | 数学公式需 KaTeX/MathJax 引擎 |
| GradientText | `components.display.gradient-text` | ✅ | 强调短语 |
| Heatmap | `chart.Heatmap` | ✅ | 活跃度、矩阵 |
| Highlight | `components.display.highlight` | ✅ | 搜索命中 |
| Image | `components.display.figure` | ✅ | 图片 + 图注 |
| Item / Thing | `components.display.item` / `.yd-item` | ✅ | 媒体行、结果行 |
| KPI / Statistic | `components.data.kpi` / `data.stat` / `display.number` | ✅ | 指标卡 |
| List | `components.display.list` | ✅ | 有序/无序列表 |
| Log / LogViewer | `components.data.log` | ✅ | 运行日志、审计 |
| Marquee | `components.display.marquee` | ✅ | 公告滚动 |
| NumberAnimation | `components.display.number` + `ydNumberAnimation` | ✅ | 指标入场 |
| QRCode | — | ⏳ | 需要纯 Go QR 编码器；见 §5 |
| ScrollShadow | `components.display.scroll-area` | ✅ | 限高滚动区（无阴影渐隐） |
| Snippet | `components.display.code` | ✅ | 代码片段 + 复制 |
| Table | `components.data.table.*` | ✅ | 所有表格 |
| Timeline | `components.display.timeline` | ✅ | 审计、进度历史 |
| Tree | `components.navigation.tree` | ✅ | 层级数据 |
| Typography / Prose | `.yd-prose` + `components.display.prose` | ✅ | Markdown/富文本输出 |
| User | `components.data.avatar` + `components.display.item` | 🧩 | 用户行、成员行 |
| VirtualList | htmx 分页 + `hx-trigger="revealed"` | 🧩/⏳ | 大数据列表；见 §5 |
| Watermark | `components.display.watermark` + `ydWatermark` | ✅ | 内部资料水印 |

### 1.5 布局与浮层（Layout & Overlay）

| 参考组件 | YD 实现 | 状态 | 推荐场景 |
|---|---|---|---|
| AspectRatio | `components.display.aspect` | ✅ | 图片/视频占位 |
| Divider / Separator | `components.display.divider` | ✅ | 分组 |
| Flex / Grid / Space | Tailwind 工具类 + `.yd-stack` / `.yd-cluster` | 🧩 | 所有布局 |
| Layout | `layouts/shell.html` / `#yd-shell` | 🧩 | 站点骨架 |
| Resizable | CSS `resize` / 规划中的拖拽分隔条 | 🧩/⏳ | 工作台分栏 |
| ScrollArea | `components.display.scroll-area` | ✅ | 限高滚动区 |
| Split | `.yd-split` / `.yd-workbench` | ✅ | 列表+详情 |
| Dialog / Modal | `components.overlay.modal.*` | ✅ | 非抽屉模态 |
| Drawer / Sheet | 外壳 `#yd-drawer` | 🧩 | 新增/编辑 |
| Popover | `.yd-popover` / `components.navigation.dropdown` | ✅ | 浮层面板 |
| HoverCard | `[data-yd-tooltip]` / `.yd-popover` | 🧩/⏳ | 悬停详情；延迟卡片见 §5 |
| Tooltip | `[data-yd-tooltip]` | ✅ | 图标按钮说明 |
| Menubar / ContextMenu | `components.navigation.menubar` / `context-menu` | ✅ | 桌面式操作 |
| Command | `components.navigation.command` | ✅ | 命令面板 |
| Sidebar | 外壳 | 🧩 | 站点导航 |

### 1.6 图表（Charts）

| 图表 | Go API | 状态 | 推荐场景 |
|---|---|---|---|
| 折线图 | `chart.Line` | ✅ | 趋势 |
| 面积图 | `chart.Area` | ✅ | 累计/流量 |
| 柱状图 | `chart.Bar` | ✅ | 分类对比 |
| 堆叠柱 | `chart.StackedBar` | ✅ | 构成 |
| 饼图 | `chart.Pie` | ✅ | 占比（≤6 类） |
| 环形图 | `chart.Donut` | ✅ | 占比 + 中心指标 |
| 迷你折线 | `chart.Sparkline` | ✅ | KPI 卡片 |
| 仪表盘 | `chart.Gauge` | ✅ | 使用率/健康度 |
| 雷达图 | `chart.Radar` | ✅ | 多维评分 |
| 组合图 | `chart.Composed` | ✅ | 柱 + 线 |
| 热力图 | `chart.Heatmap` | ✅ | 活跃度矩阵 |
| 进度环 | `chart.ProgressRing` | ✅ | 单一完成度 |
| ChartTooltip | SVG `<title>`（原生悬停/读屏） | ✅ | 零脚本提示 |
| CircularProgress | `chart.ProgressRing` | ✅ | 单一完成度 |
| Widget | `components.data.kpi` + `chart.Sparkline` | ✅ | 指标卡 |
| 地图 / 关系图 / 甘特 | — | ❌/⏳ | 见 §5 |

### 1.7 业务模式（Patterns，不属于基础原语）

| 参考 | YD 做法 | 状态 |
|---|---|---|
| Kanban | htmx 拖拽列 + 服务端排序；`yd gen` 生成后按业务实现 | ⏳ |
| Scheduler / Calendar | 原生日期 + 服务端月历片段 + htmx 导航 | 🧩 |
| Gantt | 服务端 SVG/条形布局 + 时间轴 | ⏳ |
| Flowchart | Canvas/SVG 业务组件；框架不内置 | ❌ |
| SignaturePad | Canvas 业务组件 | ❌ |
| Map | MapLibre/高德等业务集成 | ❌ |
| AI Chat | 消息中心 + SSE/WS 已提供传输；对话 UI 属业务 | 🧩 |
| Rich Text Editor | `components.form.editor` + EasyMDE（页面引入 vendor） | ✅ |
| File Manager | 列表 + 上传 + htmx 分页组合 | 🧩 |

### 1.8 命名归一（参考库专有叫法）

四套参考库对同一件事常有不同命名。下表只列前面矩阵里不明显的别名，避免「找不到」被误读成「没实现」。

| 参考库命名 | YD 对应 | 状态 |
|---|---|---|
| Button / ButtonGroup / Toggle / ToggleGroup / Chip / Tag | `.yd-btn` / `display.button-group` / `form.toggle` / `form.segmented` / `feedback.tag` | ✅ |
| Label / Link / Kbd / Description / CircularProgress | 表单 `Label`/`LabelKey` / 原生 `<a>` / `display.kbd` / `display.descriptions` / `chart.ProgressRing` | ✅ |
| Snippet / Code / ScrollShadow / User / Thing | `display.code` / `display.code` / `display.scroll-area` / `data.avatar` + `display.item` / `display.item` | ✅ |
| Sheet / Navbar / Sidebar / HoverCard | 外壳 `#yd-drawer` / 顶栏 / 侧栏 / `[data-yd-tooltip]` | 🧩/⏳ |
| Ripple | 无（不做水波纹；点击反馈由按钮状态与过渡表达） | ❌ |
| DateInput / TimeInput / NumberInput / Listbox | `form.date` / `form.time` / `form.number` / `form.combobox` | ✅ |
| FilterPanel / FilterBox / Toolbar / Wizard / LoadMore | 表单 + `.yd-card` / `.yd-cluster` + 按钮组 / `navigation.steps` + htmx / `hx-trigger="revealed"` | 🧩 |
| Video / Popeditor / PullRefresh / Guide / CropUpload | `display.aspect` + 原生 `<video>` / 抽屉内编辑 / 无（用显式刷新） / 无（业务引导层） / 无（Canvas 裁剪） | 🧩/❌ |
| Numeric / Currency / Amount / IpAddress | `form.number` / `cn.amount` / `cn.amount` / 普通输入 + 服务端校验（⏳ 专用控件） | ✅/⏳ |
| Milestone / ScrollText / UserHead / LinkMenu / NavMenu / ToggleMenu | `display.timeline` / `display.marquee` / `data.avatar` / `navigation.menu` / `navigation.menu` / `navigation.dropdown` | ✅ |
| QrCode / Transfer / TreeSelect / Calendar | 见 §5 的替代方案 | ⏳ |
| ChartCore / LineChart / BarChart / ColumnChart / AreaChart | `http/chart` 的 `Line` / `Bar` / `Bar` / `Area` | ✅ |
| PieChart / RingChart / HeatMapChart / RadarChart | `chart.Pie` / `chart.Donut` / `chart.Heatmap` / `chart.Radar` | ✅ |
| RiverChart / SankeyChart / HistogramChart / Map / Gantt / Flowchart | 不内置（业务图表或第三方引擎） | ⏳/❌ |

---

## 2. 显示组件用法

> 标记为 `template.HTML` 的键（`Body`、`Content`、`Actions`、`Media`、`SVG` 等）必须由控制器组装并完成
> 净化后传入。**Go 模板里没有 `template.HTML` 这个可调用函数**，不要在 `.html` 中写
> `(template.HTML ...)`；正确做法是控制器构造结构体/`map`，模板只引用字段。

### 2.1 折叠面板

```html
{{template "components.display.accordion" (dict "Items" .FAQItems)}}
```

`FAQItems` 每项 `{ID, Title, TitleKey, Open, Body (template.HTML)}`。原生 `<details>` 负责展开，
无脚本；`Body` 必须是已经净化过的 `template.HTML`。

### 2.2 宽高比与图片

```html
{{template "components.display.aspect" (dict "Ratio" "16x9" "Content" .Cover)}}

{{template "components.display.figure" (dict
  "Src" (asset "/static/img/yd-icon.svg") "Alt" (tr "brand.icon") "AltKey" "brand.icon"
  "Caption" (tr "brand.caption") "CaptionKey" "brand.caption" "Lazy" true)}}
```

`Ratio` 支持 `16x9`、`1x1`、`4x3`、`3x2`；`Cover` 是 `template.HTML`。

### 2.3 头像组与徽标

```html
{{template "components.display.avatar-group" (dict "Items" .Members "More" 3)}}

{{template "components.display.badge" (dict "Tone" "success" "Label" (tr "status.active") "LabelKey" "status.active")}}
{{template "components.display.badge" (dict "Tone" "danger" "Count" 12 "Max" 99)}}
{{template "components.display.badge" (dict "Tone" "info" "Dot" true)}}
```

`Count` 与 `Label` 二选一，**不要传 `Count: 0`**（0 会落到文本徽标分支并渲染空徽标）；
`Max` 用于 `99+`。

### 2.4 按钮组与 FAB

```html
{{template "components.display.button-group" (dict
  "Items" (list
    (dict "Label" (tr "range.7d") "Href" "?range=7d" "Active" true)
    (dict "Label" (tr "range.30d") "Href" "?range=30d")))}}

{{template "components.navigation.fab" (dict
  "Href" "/mgr/users/new" "Label" (tr "action.create") "LabelKey" "action.create" "Primary" true)}}
```

### 2.5 轮播

```html
{{template "components.display.carousel" (dict
  "Label" (tr "gallery.title") "LabelKey" "gallery.title"
  "Items" .Slides)}}
```

`Items` 每项 `{ID, Content (template.HTML), Caption, CaptionKey}`。滚动吸附是机制，箭头与圆点只调用
`ydCarousel`；服务端仍渲染全部幻灯片，搜索引擎与无脚本用户都能看到内容。

### 2.6 代码块

```html
{{template "components.display.code" (dict
  "Label" "client.go" "LabelKey" "code.client"
  "Code" .Snippet "CopyValue" .Snippet)}}
```

复制按钮使用 `ydCopy`；`Code` 原样转义，不会被当作 HTML。

### 2.7 描述列表

```html
{{template "components.display.descriptions" (dict
  "Items" (list
    (dict "Label" (tr "field.name") "LabelKey" "field.name" "Value" .User.Display)
    (dict "Label" (tr "field.email") "LabelKey" "field.email" "Value" .User.Email)))}}
```

### 2.8 省略、渐变与高亮

```html
{{template "components.display.ellipsis" (dict "Lines" 2 "Text" .Summary "Title" .Summary)}}
{{template "components.display.gradient-text" (dict "Text" (tr "brand.slogan") "TextKey" "brand.slogan")}}
{{template "components.display.highlight" (dict "HTML" .Highlighted)}}
```

`Highlight` 的 `HTML` 必须由服务端完成转义与匹配，不能直接把用户输入拼进去。

### 2.9 列表、媒体行与时间线

```html
{{template "components.display.list" (dict "Items" .Items "Ordered" false)}}

{{template "components.display.item" (dict
  "Media" .MemberAvatar "Title" .Title "Text" .Summary "Meta" .Time
  "Actions" .RowActions)}}

{{template "components.display.timeline" (dict "Items" .Events)}}
```

`Media` 与 `Actions` 是控制器传入的 `template.HTML`。

### 2.10 进度、评分与结果页

```html
{{template "components.display.meter" (dict "Value" 72 "Max" 100 "Label" "72%" "Tone" "success")}}

{{template "components.display.rate" (dict
  "ID" "rating" "Name" "rating" "Value" 4 "Stars" (list 5 4 3 2 1)
  "Label" (tr "field.rating") "LabelKey" "field.rating")}}

{{template "components.display.result" (dict
  "Status" "success" "Title" (tr "result.done") "TitleKey" "result.done"
  "Text" (tr "result.done_hint") "TextKey" "result.done_hint"
  "Actions" .ResultActions)}}
```

`Stars` 由调用方给出降序序列（`5,4,3,2,1`），因为 html/template 不能对数字做 range。

### 2.11 跑马灯、水印与数字动画

```html
{{template "components.display.marquee" (dict "Items" .Notices)}}

{{template "components.display.watermark" (dict
  "Text" "内部资料" "Count" 48 "Content" .Body)}}

{{template "components.display.number" (dict
  "Value" 1234.5 "Format" "currency" "Label" (tr "stat.revenue") "LabelKey" "stat.revenue")}}
```

水印只做视觉提醒，不构成访问控制；敏感页面仍必须鉴权。

### 2.12 倒计时与分隔线

```html
{{template "components.display.countdown" (dict "Seconds" 3600 "Label" (tr "countdown.ends") "LabelKey" "countdown.ends")}}
{{template "components.display.divider" (dict "Label" (tr "section.more") "LabelKey" "section.more")}}
```

---

## 3. 导航组件用法

### 3.1 锚点与固钉

```html
{{template "components.navigation.anchor" (dict
  "Label" (tr "nav.on_page") "LabelKey" "nav.on_page"
  "Items" (list
    (dict "Href" "#profile" "Label" (tr "nav.profile") "LabelKey" "nav.profile" "Active" true)
    (dict "Href" "#security" "Label" (tr "nav.security") "LabelKey" "nav.security")))}}

{{template "components.navigation.affix" (dict "Content" .Toolbar)}}
```

锚点由 `ydAnchor` 根据滚动位置高亮；目标 id 必须在同一页面内。

### 3.2 回到顶部与命令面板

```html
{{template "components.navigation.back-top" (dict)}}

{{template "components.navigation.command" (dict
  "TriggerLabel" (tr "action.search") "TriggerKey" "action.search"
  "Placeholder" (tr "cmd.placeholder") "PlaceholderKey" "cmd.placeholder"
  "Shortcut" "k"
  "Items" .Commands)}}
```

`Shortcut` 是单个字母（大小写不敏感），省略即 `K`，对应 Ctrl/Cmd+K；`Items` 每项
`{Group, GroupKey, Label, LabelKey, Href, Attrs}`。输入框按 `$el.textContent` 过滤，不需要客户端数据副本。

### 3.3 右键菜单与浮动操作

```html
{{template "components.navigation.context-menu" (dict
  "Content" .Canvas
  "Items" .MenuItems)}}

{{template "components.navigation.fab" (dict "Href" "/new" "Label" (tr "action.create") "LabelKey" "action.create" "Primary" true)}}
```

右键菜单由脚本按指针定位并做视口边界收敛；滚动时自动关闭。

### 3.4 菜单栏、菜单与树

```html
{{template "components.navigation.menubar" (dict "Label" (tr "nav.menu") "LabelKey" "nav.menu" "Items" .Menus)}}

{{template "components.navigation.menu" (dict "Label" (tr "nav.main") "LabelKey" "nav.main" "Items" .Sections)}}

{{template "components.navigation.tree" (dict "Items" .Tree)}}
```

树节点递归：`{Label, LabelKey, Href, Meta, MetaKey, Open, Children []Node}`。展开用原生 `<details>`。

### 3.5 分页

```html
{{template "components.navigation.pagination" (dict
  "Label" (tr "nav.pagination") "LabelKey" "nav.pagination"
  "Summary" .Summary "SummaryKey" "page.summary"
  "PrevHref" .Prev "NextHref" .Next "Pages" .Pages)}}
```

`Pages` 每项 `{Number, Href, Current, Ellipsis}`；分页链接由服务端生成，htmx 只负责换结果区。

---

## 4. 表单新增组件

### 4.1 数字步进

```html
{{template "components.form.number" (dict
  "ID" "quota" "Name" "quota" "Value" .Quota "Min" 1 "Max" 1000 "Step" 1
  "Label" (tr "field.quota") "LabelKey" "field.quota")}}
```

`ydNumber` 只做步进与边界收敛；服务端仍要校验范围。

### 4.2 验证码

```html
{{template "components.form.otp" (dict
  "ID" "otp" "Name" "code" "Value" "" "Length" 6
  "Slots" (list 0 1 2 3 4 5)
  "Label" (tr "field.code") "LabelKey" "field.code")}}
```

可见输入框逐位输入，隐藏字段汇总后提交；脚本失败时隐藏字段仍保留服务端初始值。

### 4.3 颜色、日期与时间

```html
{{template "components.form.color" (dict "ID" "color" "Name" "color" "Value" "#2563eb" "Label" (tr "field.color") "LabelKey" "field.color")}}
{{template "components.form.date" (dict "ID" "due" "Name" "due" "Value" .Due "Label" (tr "field.due") "LabelKey" "field.due")}}
{{template "components.form.time" (dict "ID" "at" "Name" "at" "Value" .At "Label" (tr "field.at") "LabelKey" "field.at")}}
{{template "components.form.datetime" (dict "ID" "pin" "Name" "pin_at" "Value" .PinAt "Label" (tr "field.pin_at") "LabelKey" "field.pin_at")}}
```

日期/时间使用浏览器原生控件，移动端与显示缩放都由浏览器处理。`form.datetime` 对应
`datetime-local`，置顶公告的时间段用它。

### 4.4 组合框

```html
{{template "components.form.combobox" (dict
  "ID" "role" "Name" "role" "Value" .RoleID "LabelValue" .RoleName
  "Label" (tr "field.role") "LabelKey" "field.role"
  "Placeholder" (tr "field.role_placeholder") "PlaceholderKey" "field.role_placeholder"
  "Options" .RoleOptions)}}
```

选项由服务端渲染，输入时只过滤 DOM；选中值写入隐藏字段。大数据集请直接用 htmx 搜索端点，
不要把上万条选项一次渲染进页面。

### 4.5 Markdown 编辑器

```html
{{template "components.form.editor" (dict
  "ID" "body" "Name" "body" "Value" .Body "Rows" 10
  "Label" (tr "field.body") "LabelKey" "field.body")}}
```

页面需额外引入 EasyMDE 的样式与脚本（消息中心已经这样做）；未引入时 `textarea` 保留，表单仍可提交。
编辑器初始化后由 `ydEditor` 在 `yd:theme` 事件上切换暗色类。

### 4.6 开关、评分、滑块

- 开关：`components.form.toggle`（纯 CSS）。
- 评分：`components.display.rate`（原生 radio）。
- 滑块：`components.display.slider`（原生 range + `ydSlider`）。

---

## 5. 重型组件与替代方案

以下组件在参考库中常见，但在 htmx 服务端渲染模型里需要额外权衡。**没有实现不是遗漏，而是明确的边界**：

| 组件 | 为什么暂不内置 | YD 推荐做法 |
|---|---|---|
| Transfer | 双列表需要客户端维护两份集合与拖拽状态，与服务端分页模型冲突 | 用两个 `<select multiple>` + 服务端“加入/移出”按钮；每次动作返回刷新后的片段 |
| ImagePreview / Lightbox | 需要遮罩、缩放、键盘导航与图片尺寸计算，且与抽屉/对话框共享焦点管理 | 用原生 `<dialog>` + `<img>` 写一个页面级片段；不要引入 lightbox 脚本库 |
| TreeSelect | 通用树选择需要异步展开、搜索与回填，且与权限树耦合 | 中国地址用 `cn.region`；其它场景用 `navigation.tree` + 一个普通 select 回填，或 htmx 搜索端点 |
| QRCode | 需要纯 Go QR 编码器或新增第三方依赖，违反依赖克制 | 服务端生成 PNG/SVG 后由 `display.figure` 展示；编码器放业务层 |
| Calendar / Scheduler | 完整月历涉及日期数学、事件布局与时区 | 用 `components.form.date` + 服务端月历片段（htmx 上一月/下一月）；日程用列表/时间线 |
| VirtualList | 客户端虚拟滚动会引入第二套数据状态 | htmx 分页 + `hx-trigger="revealed"` 无限加载；每页 20–50 条 |
| RichTextEditor | 通用富文本需要编辑器引擎 | `components.form.editor` + EasyMDE（已 vendored）；不引入 Node 构建 |
| Kanban / Gantt / Flowchart | 业务画布，不是 UI 原语 | `yd gen` 生成基础 CRUD 后按业务实现；拖动排序走 htmx |
| SignaturePad / Map / AI Chat | 依赖 Canvas、地图 SDK 或模型服务 | 业务集成；消息传输可复用 SSE/WS |

---

## 6. 图表用法

### 6.1 调用一览

| 函数 | 签名要点 | 说明 |
|---|---|---|
| `chart.Line` | `(series, opts)` | 折线；每个 `Series` 一条线 |
| `chart.Area` | `(series, opts)` | 面积；强调累计或体量 |
| `chart.Bar` | `(series, opts)` | 分组柱状 |
| `chart.StackedBar` | `(series, opts)` | 堆叠柱状，看构成 |
| `chart.Pie` | `(points, opts)` | 饼图；`Value` 必须为正 |
| `chart.Donut` | `(points, opts)` | 环形图；默认内孔 0.6 |
| `chart.Sparkline` | `(values, opts)` | 无轴迷你折线，用于 KPI 卡 |
| `chart.Gauge` | `(value, max, opts)` | 半圆仪表盘 |
| `chart.Radar` | `(axes, series, opts)` | 多维评分 |
| `chart.Composed` | `(bars, lines, opts)` | 柱 + 线同图 |
| `chart.Heatmap` | `(cells, xLabels, yLabels, opts)` | 矩阵热力，透明度编码数值 |
| `chart.ProgressRing` | `(value, max, opts)` | 环形完成度 |
| `chart.Legend` | `(series)` | 折线/柱/面积图例 |
| `chart.SliceLegend` | `(points)` | 饼图/环形图图例 |

所有 `Options` 零值可用；`Width`/`Height` 是 `viewBox` 尺寸，CSS 负责流式缩放。

### 6.2 控制器渲染

```go
series := []chart.Series{
    {Label: "请求", Points: []chart.Point{{Label: "周一", Value: 120}, {Label: "周二", Value: 180}}},
    {Label: "错误", Points: []chart.Point{{Label: "周一", Value: 3}, {Label: "周二", Value: 5}}},
}
data["ChartSVG"] = template.HTML(chart.Line(series, chart.Options{Title: "请求趋势", YTicks: 4}))
data["ChartLegend"] = chart.Legend(series)

slices := []chart.Point{
    {Label: "直接访问", Value: 320},
    {Label: "搜索", Value: 480},
    {Label: "外部链接", Value: 200},
}
data["DonutSVG"] = template.HTML(chart.Donut(slices, chart.Options{Title: "流量来源"}))
data["DonutLegend"] = chart.SliceLegend(slices)

data["SparkSVG"] = template.HTML(chart.Sparkline([]float64{12, 18, 15, 24, 28}, chart.Options{Width: 160, Height: 40}))
```

饼图/环形图的图例用 `chart.SliceLegend(points)`；`Sparkline`、`Gauge`、`ProgressRing` 没有系列图例。
`chart.ProgressRing(72, 100, ...)` 与 `chart.Gauge(72, 100, ...)` 都接受 `0..max` 的完成值。

### 6.3 模板包装

```html
{{template "components.chart" (dict
  "Title" (tr "chart.traffic") "TitleKey" "chart.traffic"
  "Caption" (tr "chart.traffic_hint") "CaptionKey" "chart.traffic_hint"
  "SVG" .ChartSVG
  "Legend" .ChartLegend)}}
```

KPI + 迷你折线：

```html
{{template "components.data.kpi" (dict
  "Label" (tr "stat.requests") "LabelKey" "stat.requests"
  "Value" .RequestTotal "Delta" "+12.4%" "Trend" "up"
  "Spark" .SparkSVG)}}
```

### 6.4 图表选择

| 目的 | 用哪个 |
|---|---|
| 看趋势 | `Line`；强调体量用 `Area` |
| 分类对比 | `Bar`；构成变化用 `StackedBar` |
| 占比 | `Pie`（≤6 类）或 `Donut`（中心放总数） |
| 单指标完成度 | `ProgressRing`；健康度用 `Gauge` |
| 多维评分 | `Radar` |
| 柱线同图 | `Composed` |
| KPI 卡片 | `Sparkline` + `data.kpi` |
| 活跃度矩阵 | `Heatmap` |

### 6.5 图表约束

- 纯 SVG，无图表库、无 CDN；颜色来自 `yd-chart__series--1..8` 主题类。
- 每个图形带 `<title>`，悬停与读屏都能读到标签与数值。
- 空数据渲染为空图（可传 `EmptyText`），绝不输出 `NaN`/`Inf`。
- 数值轴默认从 0 起；需要截断时显式传 `YMin`/`YMax`，并在说明里写明。
- 图表在服务端生成，因此 htmx 换入片段即完成更新；不需要 `initChart()`。
- 图例的系列类必须放在色块元素上（`components.chart` 已处理），否则色块只能继承正文色。

---

## 7. 组件选择速查

| 需求 | 首选 | 不要用 |
|---|---|---|
| 页面主操作 | `yd-btn--primary` | 多个同色主按钮 |
| 行内操作 | `details.yd-menu` / `navigation.dropdown` | 一排文字链接 |
| 新增/编辑 | 右侧抽屉 `#yd-drawer` | 新开页面（除非流程很长） |
| 危险操作 | `data-yd-confirm` / `feedback.popconfirm` | 浏览器 `confirm()` |
| 空列表 | `layout.empty` + 下一步操作 | 只写“暂无数据” |
| 加载 | `spinner`（局部）/ `skeleton`（首屏）/ `#yd-progress`（全局） | 全屏遮罩 |
| 表单校验 | `hx-status:422` + `#errors` | `alert()` |
| 长文本 | `display.ellipsis` / `scroll-area` | 让表格撑破布局 |
| 层级数据 | `navigation.tree` | 缩进 div 拼接 |
| 多步流程 | `navigation.steps` | 只靠按钮文案 |
| 趋势 | `chart.Line` / `chart.Area` | 用 emoji 或纯数字 |
| 占比 | `chart.Pie` / `chart.Donut` | 饼图超过 6 类 |

---

## 8. 最佳实践

1. **先服务端，后脚本**：能在模板里渲染的，不要等 Alpine 再生成；能看到的内容必须已经在 HTML 里。
2. **一个页面一个错误槽位**：抽屉内用 `#yd-drawer-errors`，页面级用 `#errors`；不要两套并存。
3. **表单字段不复制**：可见输入框就是提交字段；隐藏字段只用于 OTP/Tags/Combobox 这类需要汇总的值。
4. **列表操作用菜单**：行内动作超过 2 个就收进 `details.yd-menu` 或 `navigation.dropdown`。
5. **分页与筛选留在服务端**：不要把全量数据塞进 Alpine；搜索用 `hx-trigger="input changed delay:250ms"`。
6. **图表由控制器生成**：模板只包 `components.chart`；不要把坐标计算散进模板。
7. **颜色只来自令牌**：图表、徽标、状态都一样；新增颜色先加令牌。
8. **主题与缩放一起验收**：每个新组件至少在明/暗、390px 与 1366px@125% 下看一次。
9. **可访问性不是附加项**：标签、焦点、`aria-invalid`、`role=alert`、`aria-hidden` 与键盘路径在组件层解决。
10. **i18n 是原生能力**：组件同时接受渲染好的 `Label`/`Hint`/`Error` 与对应的 `*Key`；**只给键也能渲染**，
    这是控制器省略重复文案的依据。新增固定文案必须同时写入框架与脚手架的 `zh-CN` / `en` 两份目录，
    并由 `TestComponentsRenderFromI18nKeysOnly` 与 `TestGeneratedI18nCatalogueCoversEveryTrKey` 守卫。
11. **不新增第三方 UI/图表库**：需要新能力时先在 `ui.css` / `yd-ui.js` / `http/chart` 里补一个通用实现。

---

## 9. 测试与维护

```bash
go test ./web ./http/chart -count=1       # 组件模板、图表几何、CSS/i18n 守卫
go test ./tooling/scaffold -count=1       # 生成项目复制、脚本顺序、类覆盖
node web/static/js/yd-ui.test.mjs         # Alpine 行为与校验内核
go run ./cmd/yd assets minify             # 重新生成压缩资产
go run ./cmd/yd assets verify --offline   # 离线资产校验
```

新增组件时同步更新：

1. 本文件的矩阵与用法；
2. `web/components_test.go` 的 fixture；
3. 需要的 `yd-ui.test.mjs` 用例；
4. `web/i18n/{zh-CN,en}.yaml` 与脚手架同名目录；
5. `yd assets minify` 产物。

---

> 组件库的价值不在于「数量对齐」，而在于每个组件都有明确的适用场景、失败方式与替代方案。
> 本文件把已实现与未实现分开写，是为了让下一次扩展有据可依，而不是把半成品伪装成完成。
