# yd 组件清单与使用手册

> 基础栈：**Htmx 4.0.0 · Tailwind CSS v4.3 · Alpine.js 3.17.5（CSP 构建）**
>
> 本文件只列 **已实现且可用** 的组件与组合。组件层的唯一真源是
> `web/templates/components/**`、`web/static/css/ui.css`、共享组件样式表与
> `web/static/js/yd-ui.js`；本文件回答「有哪些、怎么用」。
>
> 门户与管理控制台共用同一套组件：`yd new` 生成的项目里，组件模板与共享外壳只有一份，
> 两侧差异只由 `.Portal` / `.Mgr` 数据键选择。生成项目的 `/ui-kit` 页面把全部组件摆在同一页，
> 新增组件必须同步加入该页。

---

## 1. 组件清单

### 1.1 反馈（Feedback）

| 组件 | 实现 | 适用场景 |
|---|---|---|
| Alert | `components.feedback.alert` | 页面内状态说明、警告、带操作提示 |
| Toast | `components.feedback.toast` / `components.feedback.toast-region` | 一次性轻提示 |
| Notification | `components.feedback.notification` | 带标题的异步结果 |
| Message | `components.feedback.message` | 一行内联结果 |
| Popconfirm | `components.feedback.popconfirm` | 就地二次确认 |
| Questionnaire | `components.feedback.questionnaire` | 多问题表单，服务端一次渲染 |
| Tag / Chip | `components.feedback.tag` | 状态、筛选条件、已选值 |
| Spinner | `components.feedback.spinner` | 局部加载 |
| Skeleton | `components.feedback.skeleton` | 首屏占位 |
| Progress | `components.display.meter` / `chart.ProgressRing` | 线性或环形进度 |
| Result | `components.display.result` | 成功、失败、无权限、404 页面 |
| Empty | `components.layout.empty` | 空列表、空搜索、无权限提示 |
| LoadingBar | `#yd-progress` + `components.display.meter` | 顶部请求进度与局部进度 |

### 1.2 导航（Navigation）

| 组件 | 实现 | 适用场景 |
|---|---|---|
| Tabs | `components.navigation.tabs` | 服务端驱动的页签导航，每个页签是链接或 htmx 请求 |
| Client tabs | `components.navigation.tabs-client` | 已渲染面板之间的纯客户端页签 |
| Breadcrumb | `components.navigation.breadcrumb` | 层级导航 |
| Steps / Stepper | `components.navigation.steps` | 线性流程 |
| Dropdown / Action menu | `components.navigation.dropdown` / `details.yd-menu` | 行操作、用户菜单 |
| Anchor | `components.navigation.anchor` | 长文档、设置页目录，滚动高亮 |
| Affix / Sticky | `components.navigation.affix` | 工具栏、筛选条、表头吸附 |
| BackTop | `components.navigation.back-top` | 长列表、长文回到顶部 |
| FAB / FloatButton | `components.navigation.fab` | 移动端主操作 |
| Command | `components.navigation.command` | 全局命令面板（Ctrl/Cmd+K） |
| ContextMenu | `components.navigation.context-menu` | 表格行、画布对象的右键菜单 |
| Menubar | `components.navigation.menubar` | 桌面式多级菜单 |
| Pagination | `components.navigation.pagination` | 服务端分页（页码 / 游标两态，可带页长选择） |
| Tree | `components.navigation.tree` | 目录、权限树、组织架构 |
| Menu | `components.navigation.menu` | 侧栏、设置导航 |
| Tabbar | `components.navigation.tabbar` | 移动端底部导航 |
| FilterBar | `components.navigation.filter-bar` | 列表页筛选表单 |
| ActionSheet | `components.navigation.action-sheet` | 移动端底部操作面板 |
| Link | 原生 `<a>` + `--color-accent` / 语义令牌 | 行内链接 |
| Navbar / Sidebar | 外壳 `#yd-shell` | 站点级导航，框架已提供 |

### 1.3 数据录入（Data Entry）

| 组件 | 实现 | 适用场景 |
|---|---|---|
| Form / Field | `components.form.field` + `.yd-form-grid` | 所有表单 |
| Input | `components.form.field` | 单行文本 |
| Textarea | `components.form.textarea` | 多行文本 |
| Select | `components.form.select` | 单选；真实 `<select>` 作无脚本回退，Alpine 启动后显示自定义列表框 |
| TreeSelect | `components.form.tree-select` | 带层级缩进的选择 |
| Cascader | `components.form.cascader` | 多级级联选择，每级一个选择器 |
| Combobox / AutoComplete | `components.form.combobox` + `ydCombobox` | 可搜索单选 |
| Transfer | `components.form.transfer` | 双列表穿梭；服务端决定移动结果 |
| Checkbox | `components.form.check` | 多选 |
| Radio | `components.form.radio` | 单选 |
| Toggle / Switch | `components.form.toggle` | 即时开关 |
| Segmented / ToggleGroup | `components.form.segmented` | 分段选择 |
| Password | `components.form.password` | 口令输入、显示切换与强度提示 |
| Tags / TagInput | `components.form.tags` + `ydTagInput` | 标签、收件人 |
| DynamicRows | `components.form.dynamic-rows` | 可增删行；字段用 `name[]` 提交 |
| Number | `components.form.number` + `ydNumber` | 数量、阈值 |
| InputOTP | `components.form.otp` + `ydOtp` | 短信/邮箱验证码 |
| ColorPicker | `components.form.color` + `ydColor` | 主题色、标签色 |
| DatePicker | `components.form.date` | 自定义日历，提交 `YYYY-MM-DD` |
| TimePicker | `components.form.time` | 自定义时/分列，提交 `HH:MM` |
| DateTimePicker | `components.form.datetime` | 日历 + 时分，提交 `YYYY-MM-DDTHH:MM` |
| Picker | `components.form.picker` | 日期、时间、日期时间、月份的统一选择器 |
| MonthPicker | `components.form.month` | 自定义月份网格，提交 `YYYY-MM` |
| YearPicker | `components.form.year` | 年份选择 |
| QuarterPicker | `components.form.quarter` | 季度选择 |
| TimeRange | `components.form.time-range` | 起止时间 |
| DateRange | `components.cn.daterange` | 起止日期与常用预设 |
| Mention | `components.form.mention` | 带原生 datalist 建议的提及输入 |
| Upload / FileUpload | `components.form.file` + `ydUpload` | 拖放上传、附件 |
| MarkdownEditor | `components.form.editor` + EasyMDE | Markdown 写作；未加载 EasyMDE 时回退 textarea |
| Form actions | `components.form.actions` | 提交/取消按钮 |
| Search | `components.form.search` + `ydSearch` | 页面级搜索，自带表单 |
| InputGroup | `.yd-input-group` + `components.form.field` | 前缀、后缀、按钮贴合 |
| Label | `components.form.*` 的 `Label` / `LabelKey` | 表单标签 |
| Phone | `components.cn.phone` + `ydPhone` | 大陆手机号 |
| IDCard | `components.cn.idcard` + `ydIDCard` | 居民身份证 |
| BankCard | `components.cn.bankcard` + `ydBankCard` | 银行卡 |
| CreditCode | `components.cn.creditcode` + `ydCreditCode` | 统一社会信用代码 |
| Amount / Currency | `components.cn.amount` + `ydAmount` | 人民币金额与中文大写 |
| Region | `components.cn.region` + `ydRegion` | 省 / 市 / 区三级联动 |
| SMS | `components.cn.sms` + `ydSmsCode` | 短信验证码与重发倒计时 |

### 1.4 数据展示（Data Display）

| 组件 | 实现 | 适用场景 |
|---|---|---|
| Avatar | `components.data.avatar` | 用户/租户 |
| AvatarGroup | `components.display.avatar-group` | 参与者、成员 |
| Badge | `components.display.badge` | 状态、计数、圆点 |
| Card | `components.layout.card.open` / `.card.close` | 内容面板 |
| Carousel | `components.display.carousel` + `ydCarousel` | 引导、图片、公告 |
| Code / Snippet | `components.display.code` | 代码块与复制 |
| Accordion / Collapse | `components.display.accordion` | FAQ、分组表单 |
| Countdown | `components.display.countdown` + `ydCountdown` | 截止时间、重发 |
| Table | `components.data.table`（一次性） / `.table.open` + `.table.close`（自定义行） | 列表、批量操作 |
| Table empty | `components.data.table.empty` | 表头保留的空状态行 |
| Table selection | `components.data.select-cell` + `ydTableSelect` | 可全选/半选的行选择 |
| Sort header | `components.data.sort-header` | 可排序表头 |
| Descriptions | `components.display.descriptions` | 详情键值对 |
| Ellipsis | `components.display.ellipsis` | 长文本截断 |
| GradientText | `components.display.gradient-text` | 强调短语 |
| Highlight | `components.display.highlight` | 搜索命中 |
| Figure / Image | `components.display.figure` | 图片与图注 |
| ImageViewer | `components.display.image-viewer` | 缩略图 + 模态查看 |
| Item / Thing | `components.display.item` | 媒体行、结果行 |
| KPI / Statistic | `components.data.kpi` / `.stat` / `components.display.number` | 指标卡、统计值 |
| List | `components.display.list` | 有序/无序列表 |
| Log | `components.data.log` | 运行日志、审计 |
| Marquee | `components.display.marquee` | 公告滚动 |
| Meter | `components.display.meter` | 配额、完成度 |
| Rate / Rating | `components.display.rate` | 评分 |
| Slider | `components.display.slider` + `ydSlider` | 数值范围 |
| Result | `components.display.result` | 结果页 |
| ScrollArea / ScrollShadow | `components.display.scroll-area` | 限高滚动区 |
| Timeline | `components.display.timeline` | 审计、进度历史 |
| Watermark | `components.display.watermark` + `ydWatermark` | 内部资料水印 |
| Kbd | `components.display.kbd` | 键盘提示 |
| Divider / Separator | `components.display.divider` | 分组 |
| Prose / Typography | `components.display.prose` | Markdown / 富文本输出 |
| NumberAnimation | `components.display.number` + `ydNumberAnimation` | 指标入场 |
| Calendar | `components.display.calendar` | 月历、事件、日程 |
| FileTree | `components.display.file-tree` | 文件与文件夹树 |
| Cell | `components.display.cell` | 设置行、导航行 |
| Attachment | `components.display.attachment` | 附件卡片、上传状态 |
| Bubble | `components.display.bubble` | 对话气泡 |
| ChatMessage | `components.display.chat-message` | 头像、消息头、气泡与操作 |
| MessageScroller | `components.display.message-scroller` | 有界消息滚动容器 |
| PromptInput | `components.display.prompt-input` | 聊天/提示输入与发送 |
| TrendChip | `components.display.trend-chip` | 升降趋势标签 |
| TextShimmer | `components.display.text-shimmer` | 流式输出占位 |
| Trace | `components.display.trace` | 执行轨迹、工具调用、推理步骤 |
| Marker | `components.display.marker` | 对话中的状态/分隔标记 |
| ActionBar | `components.display.action-bar` | 批量操作条 |
| HoverCard | `components.display.hover-card` | 悬停/聚焦详情卡 |
| VirtualList | `components.display.virtual-list` | 长列表，`content-visibility` 布局虚拟化 |
| QRCode | `components.display.qrcode` | 二维码展示框；传入服务端编码后的 SVG 或图片 |
| Kanban | `components.display.kanban` | 看板列与卡片 |
| EmojiPicker | `components.display.emoji-picker` + `ydEmoji` | 表情面板，插入目标输入框 |
| Signature | `components.display.signature` + `ydSignature` | 手写签名，隐藏字段提交 |
| Resizable | `components.display.resizable` + `ydResizable` | 可拖拽/方向键调整的双栏 |
| Copy | `components.cn.copy` + `ydCopy` | 复制文本 |

### 1.4.1 数据表格

```html
{{template "components.data.table" (dict
  "ID" "yd-users-table" "CaptionKey" "ui.data_table"
  "Columns" (list
    (dict "LabelKey" "field.name")
    (dict "LabelKey" "field.status")
    (dict "LabelKey" "ui.actions" "Actions" true))
  "Rows" .Rows
  "Colspan" 3
  "EmptyKey" "ui.no_results")}}
```

`Rows` 的每一项是 `{Cells, SelectValue, Selected, Attrs}`；每个 `Cells` 项可给 `Value`、`LabelKey` 或已渲染的
`HTML`，并可用 `Numeric` / `Actions` / `Wrap` 控制对齐与换行。简单数据用这个一次性组件；需要行内表单等
复杂行时，用 `components.data.table.open` + `components.data.table.close`（空表加
`components.data.table.empty`），两者共用同一套表头、选择列与滚动外壳。

### 1.5 布局与浮层（Layout & Overlay）

| 组件 | 实现 | 适用场景 |
|---|---|---|
| PageHeader | `components.layout.page-header` | 页面标题与主操作 |
| Card | `components.layout.card.open` / `.card.close` | 内容面板 |
| Empty | `components.layout.empty` | 空状态与下一步操作 |
| AspectRatio | `components.display.aspect` | 图片/视频占位 |
| Divider | `components.display.divider` | 水平/垂直分组 |
| Flex / Grid / Space | Tailwind 工具类 + `.yd-stack` / `.yd-cluster` / `.yd-grid` | 页面布局 |
| Split / Workbench | `.yd-split` / `.yd-workbench` | 列表 + 详情 |
| Layout / Sidebar / Navbar | `#yd-shell` / `layouts/shell.html` | 站点骨架 |
| Modal / Dialog | `components.overlay.modal.open` / `.modal.close` | 非抽屉模态 |
| Drawer / Sheet | 外壳 `#yd-drawer` | 新增/编辑 |
| Popover | `components.overlay.popover` | 浮层面板、筛选器、小型表单 |
| Tooltip | `components.overlay.tooltip` | 图标按钮说明 |
| Dropdown | `components.navigation.dropdown` | 行操作、用户菜单 |
| HoverCard | `components.display.hover-card` | 悬停详情 |
| Command | `components.navigation.command` | 命令面板 |
| Affix / Sticky | `components.navigation.affix` | 吸附工具栏 |

### 1.6 图表（Charts）

图表由 `http/chart` 在服务端生成纯 SVG，模板只包 `components.chart`。

| 图表 | Go API | 适用场景 |
|---|---|---|
| 折线图 | `chart.Line` | 趋势 |
| 面积图 | `chart.Area` | 累计、流量 |
| 柱状图 | `chart.Bar` | 分类对比 |
| 堆叠柱 | `chart.StackedBar` | 构成 |
| 饼图 | `chart.Pie` | 占比（≤6 类） |
| 环形图 | `chart.Donut` | 占比 + 中心指标 |
| 迷你折线 | `chart.Sparkline` | KPI 卡片 |
| 仪表盘 | `chart.Gauge` | 使用率、健康度 |
| 雷达图 | `chart.Radar` | 多维评分 |
| 组合图 | `chart.Composed` | 柱 + 线 |
| 热力图 | `chart.Heatmap` | 活跃度矩阵 |
| 进度环 | `chart.ProgressRing` | 单一完成度 |
| 图例 | `chart.Legend` / `chart.SliceLegend` | 折线/柱/面积图例与饼图/环形图图例 |

---

## 2. 显示组件用法

> 标记为 `template.HTML` 的键（`Body`、`Content`、`Actions`、`Media`、`SVG` 等）必须由控制器
> 组装并完成净化后传入。**Go 模板里没有 `template.HTML` 这个可调用函数**，不要在 `.html` 中写
> `(template.HTML ...)`；正确做法是控制器构造结构体或 `map`，模板只引用字段。

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

`Count` 与 `Label` 二选一，**不要传 `Count: 0`**；`Max` 用于 `99+`。

### 2.4 按钮组与悬浮操作

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
`ydCarousel`；服务端仍渲染全部幻灯片。

### 2.6 代码块

```html
{{template "components.display.code" (dict
  "Label" "client.go" "LabelKey" "code.client"
  "Code" .Snippet "CopyValue" .Snippet)}}
```

复制按钮使用 `ydCopy`；`Code` 原样转义。

### 2.7 描述列表、列表与时间线

```html
{{template "components.display.descriptions" (dict
  "Items" (list
    (dict "Label" (tr "field.name") "LabelKey" "field.name" "Value" .User.Display)
    (dict "Label" (tr "field.email") "LabelKey" "field.email" "Value" .User.Email)))}}

{{template "components.display.list" (dict "Items" .Items "Ordered" false)}}

{{template "components.display.item" (dict
  "Media" .MemberAvatar "Title" .Title "Text" .Summary "Meta" .Time
  "Actions" .RowActions)}}

{{template "components.display.timeline" (dict "Items" .Events)}}
```

`Media` 与 `Actions` 是控制器传入的 `template.HTML`。

### 2.8 进度、评分与结果页

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

### 2.9 跑马灯、水印与数字动画

```html
{{template "components.display.marquee" (dict "Items" .Notices)}}

{{template "components.display.watermark" (dict
  "Text" "内部资料" "TextKey" "watermark.internal" "Count" 48 "Content" .Body)}}

{{template "components.display.number" (dict
  "Value" 1234.5 "Format" "currency" "Label" (tr "stat.revenue") "LabelKey" "stat.revenue")}}
```

水印只做视觉提醒，不构成访问控制；敏感页面仍必须鉴权。

### 2.10 倒计时与分隔线

```html
{{template "components.display.countdown" (dict "Seconds" 3600 "Label" (tr "countdown.ends") "LabelKey" "countdown.ends")}}
{{template "components.display.divider" (dict "Label" (tr "section.more") "LabelKey" "section.more")}}
```

### 2.11 日历

```html
{{template "components.display.calendar" (dict
  "ID" "team-calendar" "Label" (tr "calendar.title") "LabelKey" "calendar.title"
  "MonthLabel" .MonthLabel "MonthLabelKey" .MonthLabelKey
  "PrevHref" .PrevHref "NextHref" .NextHref "TodayHref" .TodayHref
  "TodayLabel" (tr "calendar.today") "TodayLabelKey" "calendar.today"
  "Weekdays" .Weekdays "Weeks" .Weeks)}}
```

日期运算留在控制器：`Weeks` 每项 `{Days []{Number, Date, Href, Outside, Today, Selected, Events}}`，
模板只绘制收到的单元格，时区与每周起始日不会在服务端与浏览器之间分叉。

### 2.12 文件树、设置行与附件

```html
{{template "components.display.file-tree" (dict "Label" (tr "fs.title") "LabelKey" "fs.title" "Items" .Files)}}

{{template "components.display.cell" (dict
  "Label" (tr "settings.notifications") "LabelKey" "settings.notifications"
  "Hint" (tr "settings.notifications_hint") "HintKey" "settings.notifications_hint"
  "Href" "/settings/notifications" "Control" .Toggle)}}

{{template "components.display.attachment" (dict
  "Title" .Name "Description" .Size "State" "done" "Href" .URL
  "Actions" .Actions "RemoveAttrs" .RemoveAttrs "RemoveKey" "ui.remove")}}
```

`file-tree` 的节点支持 `Icon (template.HTML)`；`attachment` 的 `State` 取
`idle|uploading|processing|error|done`，错误原因必须写进 `Description`，不能只靠颜色。

### 2.13 对话表面

```html
{{template "components.display.message-scroller" (dict
  "Label" (tr "message.history") "LabelKey" "message.history"
  "Size" "lg" "Content" .History)}}

{{template "components.display.chat-message" (dict
  "Align" "end" "Avatar" .Avatar "Name" .SenderName "Time" .SentAt
  "Content" .BodyHTML "Tone" "secondary" "State" "sent" "Actions" .Actions)}}

{{template "components.display.prompt-input" (dict
  "ID" "composer" "Name" "body" "Action" .PostURL "Post" .PostURL
  "Target" "#thread" "Swap" "innerMorph" "Rows" 3
  "Label" (tr "message.body") "LabelKey" "message.body"
  "Placeholder" (tr "message.placeholder") "PlaceholderKey" "message.placeholder"
  "SendKey" "ui.send")}}
```

`bubble` 可单独使用；`chat-message` 内部调用它。正文必须由服务端净化后传入。

### 2.14 执行轨迹与标记

```html
{{template "components.display.trace" (dict
  "Label" (tr "trace.title") "LabelKey" "trace.title" "Items" .Steps)}}

{{template "components.display.marker" (dict
  "Variant" "separator" "Role" "status" "Text" (tr "chat.today") "TextKey" "chat.today")}}
```

`trace` 每项 `{Title, Description, Status (running|error|pending|done), Icon, Href, Details, Open}`；
展开行使用原生 `<details>`。

### 2.15 看板与二维码展示

```html
{{template "components.display.kanban" (dict
  "Label" (tr "board.title") "LabelKey" "board.title" "Columns" .Columns)}}

{{template "components.display.qrcode" (dict
  "SVG" .QRSVG "Label" (tr "qr.label") "LabelKey" "qr.label"
  "Caption" (tr "qr.caption") "CaptionKey" "qr.caption")}}
```

`Columns` 每项 `{ID, Title, Count, Cards []{ID, Title, Text, Meta, Href, Actions}}`；
`qrcode` 接收服务端已编码的 `SVG` 或 `Src` 图片。

### 2.16 签名与可调整分栏

```html
{{template "components.display.signature" (dict
  "ID" "signature" "Name" "signature" "Label" (tr "field.signature") "LabelKey" "field.signature"
  "Hint" (tr "field.signature_hint") "HintKey" "field.signature_hint" "Required" true)}}

{{template "components.display.resizable" (dict
  "ID" "workbench" "Label" (tr "workbench.resize") "LabelKey" "workbench.resize"
  "Split" 38 "Min" 20 "Max" 80 "Start" .List "End" .Detail)}}
```

签名写入隐藏字段；无脚本时保留服务端值。分隔条支持指针拖拽与方向键。

---

## 3. 导航组件用法

### 3.1 锚点、固钉与回到顶部

```html
{{template "components.navigation.anchor" (dict
  "Label" (tr "nav.on_page") "LabelKey" "nav.on_page"
  "Items" (list
    (dict "Href" "#profile" "Label" (tr "nav.profile") "LabelKey" "nav.profile" "Active" true)
    (dict "Href" "#security" "Label" (tr "nav.security") "LabelKey" "nav.security")))}}

{{template "components.navigation.affix" (dict "Content" .Toolbar)}}
{{template "components.navigation.back-top" (dict)}}
```

### 3.2 命令面板、右键菜单与菜单栏

```html
{{template "components.navigation.command" (dict
  "TriggerLabel" (tr "action.search") "TriggerKey" "action.search"
  "Placeholder" (tr "cmd.placeholder") "PlaceholderKey" "cmd.placeholder"
  "Shortcut" "k" "Items" .Commands)}}

{{template "components.navigation.context-menu" (dict "Content" .Canvas "Items" .MenuItems)}}

{{template "components.navigation.menubar" (dict
  "Label" (tr "nav.menu") "LabelKey" "nav.menu" "Items" .Menus)}}
```

### 3.3 菜单、树与分页

```html
{{template "components.navigation.menu" (dict
  "Label" (tr "nav.main") "LabelKey" "nav.main" "Items" .Sections)}}

{{template "components.navigation.tree" (dict "Items" .Tree)}}

{{/* 常规分页：页码窗口 + 页长选择；所有管理列表共用这一份组件。 */}}
{{template "components.navigation.pagination" (dict
  "Label" (tr "nav.pagination") "LabelKey" "nav.pagination"
  "Summary" (tr "ui.page_summary" .Page .PageCount)
  "PrevHref" .Prev "NextHref" .Next "Pages" .Pages
  "ShowLabels" true
  "SizeLabel" (tr "showcase.label.page_size") "SizeLabelKey" "showcase.label.page_size"
  "SizeOptions" .SizeOptions)}}

{{/* 键集 / 游标分页：Pages 为空，只提供前后游标链接，永远不执行 COUNT。 */}}
{{template "components.navigation.pagination" (dict
  "Label" (tr "nav.pagination") "LabelKey" "nav.pagination"
  "Summary" (tr "ui.page_cursor_summary" .Size)
  "PrevHref" .Prev "NextHref" .Next "ShowLabels" true)}}
```

`Pages` 为空且给出 `PrevHref` / `NextHref` 时就是游标形态；`ShowLabels` 给上一页/下一页补充可见文字，
`SizeOptions` 是 `{Label, Href, Current}` 的页长选项。两种形态都由同一个服务端组件渲染，链接是真实
`href`，包一层 `hx-boost` 后只替换工作区，地址栏、无脚本访问与键盘操作都保持可用。

树节点递归：`{Label, LabelKey, Href, Meta, MetaKey, Open, Icon, Children []Node}`。展开用原生 `<details>`。

### 3.4 客户端页签、底部导航与筛选条

```html
{{template "components.navigation.tabs-client" (dict
  "Label" (tr "tabs.title") "LabelKey" "tabs.title" "Active" "one"
  "Items" .Tabs)}}

{{template "components.navigation.tabbar" (dict
  "Label" (tr "nav.sections") "LabelKey" "nav.sections" "Items" .Items)}}

{{template "components.navigation.filter-bar" (dict
  "Action" .ListURL "Get" .ListURL "Target" "#rows" "Swap" "innerHTML"
  "Fields" .Fields "Actions" .Actions "SubmitKey" "ui.search")}}
```

`tabs-client` 只用于不需要独立 URL 的内容；`filter-bar` 自带 `<form>`，不能嵌套在另一个表单里。

### 3.5 操作面板

```html
<button type="button" class="yd-btn" data-yd-modal-open="actions">更多</button>

{{template "components.navigation.action-sheet" (dict
  "ID" "actions" "Title" (tr "action.more") "TitleKey" "action.more"
  "Actions" .Actions "CancelKey" "action.cancel")}}
```

---

## 4. 表单组件用法

### 4.1 数字、验证码与动态行

```html
{{template "components.form.number" (dict
  "ID" "quota" "Name" "quota" "Value" .Quota "Min" 1 "Max" 1000 "Step" 1
  "Label" (tr "field.quota") "LabelKey" "field.quota")}}

{{template "components.form.otp" (dict
  "ID" "otp" "Name" "code" "Value" "" "Length" 6
  "Slots" (list 0 1 2 3 4 5)
  "Label" (tr "field.code") "LabelKey" "field.code")}}

{{template "components.form.dynamic-rows" (dict
  "ID" "lines" "Name" "lines" "Label" (tr "field.lines") "LabelKey" "field.lines"
  "Rows" .Rows "RowTemplate" .RowTemplate "AddKey" "ui.add" "Min" 1 "Max" 20)}}
```

动态行内的字段用 `name="lines[]"` 提交；每行由服务端渲染，Alpine 只克隆蓝图与删除行。

### 4.2 日期、时间与月份

```html
{{template "components.form.date" (dict
  "ID" "due" "Name" "due" "Value" .Due "Min" .Today "Max" .EndOfYear
  "Label" (tr "field.due") "LabelKey" "field.due")}}

{{template "components.form.time" (dict
  "ID" "at" "Name" "at" "Value" .At
  "Label" (tr "field.at") "LabelKey" "field.at")}}

{{template "components.form.datetime" (dict
  "ID" "pin" "Name" "pin_at" "Value" .PinAt
  "Label" (tr "field.pin_at") "LabelKey" "field.pin_at")}}

{{template "components.form.month" (dict
  "ID" "period" "Name" "period" "Value" .Period
  "Label" (tr "field.period") "LabelKey" "field.period")}}
```

可见字段是文本框，提交值保持 ISO 风格；日历、时/分列与月份网格由 `ydDateTime` 渲染，月名与周名来自
`ui.months` / `ui.weekdays` 目录。`Min`/`Max` 会禁用越界日期。

### 4.3 选择器：单选、树选、级联与穿梭

```html
{{template "components.form.select" (dict
  "ID" "role" "Name" "role" "Label" (tr "field.role") "LabelKey" "field.role"
  "Options" .RoleOptions)}}

{{template "components.form.tree-select" (dict
  "ID" "node" "Name" "node" "Label" (tr "field.node") "LabelKey" "field.node"
  "Options" .NodeOptions)}}

{{template "components.form.cascader" (dict
  "ID" "region" "Name" "region" "Label" (tr "field.region") "LabelKey" "field.region"
  "Levels" .RegionLevels)}}

{{template "components.form.transfer" (dict
  "ID" "roles" "Name" "roles" "Action" .SaveURL "Method" "post"
  "SourceName" "source" "TargetName" "target"
  "SourceLabel" (tr "transfer.available") "SourceLabelKey" "transfer.available"
  "TargetLabel" (tr "transfer.selected") "TargetLabelKey" "transfer.selected"
  "SourceItems" .Available "TargetItems" .Selected)}}
```

`form.select` 保留真实 `<select>` 作无脚本回退；Alpine 启动后显示自定义列表框。树选择的层级缩进由
调用方通过 `Prefix` 给出。穿梭框的两个提交按钮携带 `direction=add|remove`。

### 4.4 组合框、标签与提及

```html
{{template "components.form.combobox" (dict
  "ID" "role" "Name" "role" "Value" .RoleID "LabelValue" .RoleName
  "Label" (tr "field.role") "LabelKey" "field.role"
  "Placeholder" (tr "field.role_placeholder") "PlaceholderKey" "field.role_placeholder"
  "Options" .RoleOptions)}}

{{template "components.form.tags" (dict
  "ID" "tags" "Name" "tags" "Value" .Tags
  "Label" (tr "field.tags") "LabelKey" "field.tags" "AddKey" "ui.add")}}

{{template "components.form.mention" (dict
  "ID" "mention" "Name" "mention" "Value" .Draft
  "Label" (tr "field.mention") "LabelKey" "field.mention"
  "Placeholder" (tr "field.mention_placeholder") "PlaceholderKey" "field.mention_placeholder"
  "Options" .MemberOptions)}}
```

### 4.5 上传与 Markdown 编辑器

```html
{{template "components.form.file" (dict
  "ID" "attachment" "Name" "attachment"
  "Label" (tr "field.attachment") "LabelKey" "field.attachment"
  "Hint" (tr "field.attachment_hint") "HintKey" "field.attachment_hint")}}

{{template "components.form.editor" (dict
  "ID" "body" "Name" "body" "Value" .Body "Rows" 10
  "Label" (tr "field.body") "LabelKey" "field.body")}}
```

页面需为编辑器引入 EasyMDE 的样式与脚本；未引入时 `textarea` 保留，表单仍可提交。
用 `PageScripts` / `PageStyles` 引入时，页面模板还必须渲染 `#yd-page-assets` 声明节点
（`data-yd-styles` / `data-yd-scripts`，参见 `docs/frontend-ui.md` §2.2）：htmx 局部导航不会重写 `<head>`，
缺少它时编辑器在局部导航后不会升级；脚本结算后 `yd-app.js` 广播 `yd:page-assets:loaded`，`yd-ui.js`
的 `ydEditor` 据此重试初始化。
消息中心默认把 EasyMDE 延迟到首次聚焦 `[data-yd-editor]` 或打开抽屉时再加载：包装元素用
`data-yd-editor-vendor="{{ asset "/static/js/vendored/easymde.min.js" }}"` 声明地址，
`yd-message-editor.js` 负责同源注入并在加载后升级全部编辑器；只读访问因此不必下载约 320 KB 的编辑器。
工具栏图标由 `partials/editor-icons.html` 的隐藏 Lucide 调色板提供（`yd-ui.js` 在运行时克隆进按钮），
不加载 EasyMDE 默认的 Font Awesome 图标字体，也不请求 CDN。

### 4.6 动作、搜索与表单布局

```html
{{template "components.form.actions" (dict
  "SubmitKey" "action.save" "CancelKey" "action.cancel" "CancelHref" .BackURL)}}

{{template "components.form.search" (dict
  "ID" "search" "Action" .ListURL "Name" "q"
  "Placeholder" (tr "action.search") "PlaceholderKey" "action.search")}}
```

`form.search` 自带 `<form>`，不能嵌套在另一个表单里。

---

## 5. 图表用法

### 5.1 调用一览

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
| `chart.Heatmap` | `(cells, xLabels, yLabels, opts)` | 矩阵热力 |
| `chart.ProgressRing` | `(value, max, opts)` | 环形完成度 |
| `chart.Legend` | `(series)` | 折线/柱/面积图例 |
| `chart.SliceLegend` | `(points)` | 饼图/环形图图例 |

所有 `Options` 零值可用；`Width`/`Height` 是 `viewBox` 尺寸，CSS 负责流式缩放。

### 5.2 控制器渲染

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
```

### 5.3 模板包装

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

### 5.4 图表选择

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

### 5.5 图表约束

- 纯 SVG，无图表库、无 CDN；颜色来自 `yd-chart__series--1..8` 主题类。
- 每个图形带 `<title>`，悬停与读屏都能读到标签与数值。
- 空数据渲染为空图（可传 `EmptyText`），绝不输出 `NaN`/`Inf`。
- 数值轴默认从 0 起；需要截断时显式传 `YMin`/`YMax`。
- 图表在服务端生成，htmx 换入片段即完成更新，不需要 `initChart()`。

---

## 6. 组件选择速查

| 需求 | 首选 | 不要用 |
|---|---|---|
| 页面主操作 | `yd-btn--primary` | 多个同色主按钮 |
| 行内操作 | `details.yd-menu`（图标按钮触发） / `navigation.dropdown` | 一排文字按钮 |
| 新增/编辑 | 右侧抽屉 `#yd-drawer` | 新开页面（除非流程很长） |
| 危险操作 | `data-yd-confirm` / `feedback.popconfirm` | 浏览器 `confirm()` |
| 空列表 | `layout.empty` + 下一步操作 | 只写“暂无数据” |
| 加载 | `spinner` / `skeleton` / `#yd-progress` | 全屏遮罩 |
| 表单校验 | `hx-status:422` + `#errors` | `alert()` |
| 长文本 | `display.ellipsis` / `scroll-area` | 让表格撑破布局 |
| 层级数据 | `navigation.tree` / `form.tree-select` | 缩进 div 拼接 |
| 多步流程 | `navigation.steps` | 只靠按钮文案 |
| 日期/时间 | `form.date` / `form.time` / `form.datetime` | 原生日期/时间控件 |
| 下拉选择 | `form.select` / `form.combobox` | 未加样式的原生 `<select>` |
| 趋势 | `chart.Line` / `chart.Area` | 用 emoji 或纯数字 |
| 占比 | `chart.Pie` / `chart.Donut` | 饼图超过 6 类 |

---

## 7. 最佳实践

1. **先服务端，后脚本**：能在模板里渲染的，不要等 Alpine 再生成。
2. **一个页面一个错误槽位**：抽屉内用 `#yd-drawer-errors`，页面级用 `#errors`。
3. **表单字段不复制**：可见输入框就是提交字段；隐藏字段只用于 OTP/Tags/Combobox 这类需要汇总的值。
4. **列表操作用图标菜单**：表格/列表右侧操作列一律收进一个图标按钮（`lucide-more-horizontal` + `aria-label`）展开的 `details.yd-menu`，或 `navigation.dropdown`；不得按数量决定是否并排文字按钮。
5. **分页与筛选留在服务端**：不要把全量数据塞进 Alpine。
6. **图表由控制器生成**：模板只包 `components.chart`。
7. **颜色只来自令牌**：图表、徽标、状态都一样；新增颜色先加令牌。
8. **主题与缩放一起验收**：每个新组件至少在明/暗、390px 与 1366px@125% 下看一次。
9. **可访问性不是附加项**：标签、焦点、`aria-invalid`、`role=alert`、`aria-hidden` 与键盘路径在组件层解决。
10. **i18n 是原生能力**：组件同时接受渲染好的 `Label`/`Hint`/`Error` 与对应的 `*Key`；只给键也能渲染。
11. **不新增第三方 UI/图表库**：需要新能力时先在 `ui.css` / `yd-ui.js` / `http/chart` 里补一个通用实现。

---

## 8. 测试与维护

```bash
go test ./web ./http/chart -count=1       # 组件模板、图表几何、CSS/i18n 守卫
go test ./tooling/scaffold -count=1       # 生成项目复制、脚本顺序、类覆盖
node web/static/js/yd-ui.test.mjs         # Alpine 行为与校验内核
go run ./cmd/yd assets minify             # 重新生成压缩资产
go run ./cmd/yd assets verify --offline   # 离线资产校验
```

新增组件时同步更新：

1. 本文件的清单与用法；
2. `web/components_test.go` 的 fixture；
3. 需要的 `yd-ui.test.mjs` 用例；
4. `web/i18n/{zh-CN,en}.yaml` 与脚手架同名目录；
5. `yd assets minify` 产物；
6. 生成项目的 `/ui-kit` 页面。
