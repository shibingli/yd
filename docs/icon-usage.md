# 图标模块使用指南

本项目的图标使用 **Iconify 图标数据 + 构建期按需子集 + 单文件 SVG sprite**，不引入 Iconify 运行时、
不访问 Iconify API、不依赖 Node.js，运行时与构建期都可以完全离线。

## 1. 架构与离线保证

```text
tooling/icons/data/lucide.json       固定版本的 Iconify JSON（仅构建期输入，不进入应用二进制）
        │
        │  yd icons sync
        │  扫描模板中的 {{template "<树>/partials/icon" "…"}} 调用（树前缀可省略）
        ▼
web/static/img/icons.svg             只包含实际引用图标的 SVG sprite（go:embed 打包）
        │
        │  yd assets minify / yd build
        ▼
web/static/img/icons.min.svg         生产环境压缩产物（生产模板自动解析 .min.svg）
        │
        ▼
{{template "partials/icon" "lucide-log-in"}}
        │
        ▼
<svg class="yd-icon"><use href="/static/img/icons.svg#i-lucide-log-in"></use></svg>
```

约束与保证：

- **无 Node.js**：图标生成由 Go 工具 `yd icons` 完成，Tailwind v4 仍由 standalone 二进制编译。
- **无运行时网络**：sprite 随 `go:embed` 进入二进制，sprite 内是 `<symbol>`，没有 Iconify API、
  没有 CDN、没有按图标发起的网络请求。
- **按需体积**：生成器只把模板实际引用的图标写进 sprite。框架与脚手架模板合计约 24 个图标、约 7 KB；
  未引用的图标不会进入前端产物。
- **CSP 友好**：sprite 与页面同源；图标通过 `<use>` 引用，不依赖内联脚本或 data URI。
- **htmx 友好**：`<svg><use>` 是普通 DOM，htmx 的 morph/局部交换不会破坏它。
- **Tailwind v4 友好**：`.yd-icon` 提供默认尺寸与对齐，尺寸、颜色继续使用 Tailwind 工具类。

## 2. 目录与文件

| 路径 | 作用 |
|---|---|
| `tooling/icons/` | Go 图标管线：扫描、校验、sprite 构建、`yd icons` 的底层实现 |
| `tooling/icons/data/lucide.json` | 固定版本的 Lucide Iconify JSON（构建期输入） |
| `web/static/img/icons.svg` | 生成并提交入库的按需 sprite |
| `web/static/img/icons.min.svg` | `yd assets minify` 产出的生产压缩 sprite |
| `web/templates/partials/icon.html` | 框架模板使用的图标片段 |
| `tooling/scaffold/templates/_name_/web/templates/portal/partials/icon.html` | 生成项目门户树使用的片段 |
| `tooling/scaffold/templates/_name_/web/templates/mgr/partials/icon.html` | 生成项目后台树使用的片段 |

生成项目不需要自己维护图标数据：框架 `web.Static()` 会把框架的 `static/` 合并到项目静态树中，
`sprite` 由框架层兜底提供。项目也可以运行 `yd icons sync` 生成自己的 sprite，项目层的同名文件会覆盖框架层。

## 3. 命令

```bash
yd icons sync     # 扫描模板、重新生成 web/static/img/icons.svg
yd icons check    # 校验已提交 sprite 是否与模板/图标集一致（CI 可用，非零退出）
yd icons          # 等价于 yd icons check
```

常用标志：

```bash
yd icons sync --templates web/templates \
              --output web/static/img/icons.svg
```

- `--templates`：逗号分隔的模板根。缺省为 `web/templates`，如果存在
  `tooling/scaffold/templates/_name_/web/templates` 也会一并扫描。
- `--output`：sprite 路径，缺省 `web/static/img/icons.svg`。

新增或删除图标引用后必须运行：

```bash
yd icons sync
yd assets minify          # 同时刷新 icons.min.svg
go test ./tooling/icons/  # 仓库漂移守卫
```

`yd icons check` 会在以下情况失败：

- 模板引用了图标集中不存在的名字；
- 已提交的 sprite 与模板引用不一致；
- sprite 文件缺失。

CI 可以直接运行：

```bash
yd icons check
yd assets minify --check
```

## 4. 模板用法

只使用共享片段，不要手写 `<svg>`：

```html
{{template "partials/icon" "lucide-log-in"}}
{{template "portal/partials/icon" "lucide-log-in"}}
{{template "mgr/partials/icon" "lucide-log-in"}}
```

框架示例模板用无前缀形式；生成项目的门户与后台两棵树分别用 `portal/` 与 `mgr/` 前缀，
扫描器接受任意一层树前缀，因此三种写法都会进入 sprite。

参数是 **`<图标集前缀>-<图标名>`** 的完整引用，不带 `i-` 前缀，也不带 `#`。
片段会渲染为：

```html
<svg class="yd-icon" data-yd-icon="lucide-log-in" aria-hidden="true" focusable="false">
  <use href="/static/img/icons.svg?v=<hash>#i-lucide-log-in"></use>
</svg>
```

规则：

- 图标是装饰性的，默认 `aria-hidden="true"`；如果图标是唯一语义，请给按钮或链接提供
  `aria-label` / 可见文本。
- 不要在模板里拼动态图标名（如 `"lucide-" + .Name`）：生成器只扫描静态的
  `{{template "<树>/partials/icon" "…"}}` 调用。
- 不要直接引用 `#i-…` 或内联 SVG；否则 `yd icons check` 不会把该图标纳入 sprite。
- 图标颜色继承 `currentColor`，因此主题切换、链接色与按钮禁用态都会自动跟随。

### 4.1 尺寸与颜色

```html
{{template "partials/icon" "lucide-log-out"}}
```

默认 `.yd-icon` 为 `1.125rem`，行内对齐。需要覆盖时，给按钮或包裹元素加 Tailwind 类：

```html
<button class="yd-btn yd-btn--ghost text-danger">
  {{template "partials/icon" "lucide-log-out"}}
  <span>退出登录</span>
</button>
```

不要为图标单独写颜色字面量；颜色来自语义令牌或 Tailwind 的 `text-*` 工具类。

### 4.2 主题与语言按钮

主题按钮为三种选择各携带一个图标，由 `data-yd-theme-state` 决定显示哪一个：

```html
<button type="button" class="yd-btn yd-btn--ghost yd-btn--sm yd-theme-toggle" data-yd-theme-toggle>
  <span class="yd-theme__glyph" data-yd-theme-glyph="light">{{template "partials/icon" "lucide-sun"}}</span>
  <span class="yd-theme__glyph" data-yd-theme-glyph="dark">{{template "partials/icon" "lucide-moon"}}</span>
  <span class="yd-theme__glyph" data-yd-theme-glyph="system">{{template "partials/icon" "lucide-monitor"}}</span>
</button>
```

语言按钮显示当前语言，使用 `lucide-languages`；切换逻辑仍走原来的单一按钮路径。

## 5. 当前内置图标

| 引用 | 用途 |
|---|---|
| `lucide-log-in` | 登录提交按钮 |
| `lucide-log-out` | 退出登录（顶栏、侧栏） |
| `lucide-sun` | 主题：浅色 |
| `lucide-moon` | 主题：深色 |
| `lucide-monitor` | 主题：跟随系统 |
| `lucide-languages` | 语言切换 |

新增图标时，先在模板中加入片段调用，再运行 `yd icons sync`。生成器会自动把它加入 sprite。

OpenAPI 查看器（`/mgr/docs`）的 `#theme-toggle` 也复用 `lucide-sun` / `lucide-moon` /
`lucide-monitor` 三枚图标，由 `data-theme-choice` 经 CSS 选出；脚本只更新
`#theme-label` 的文字，**不得**对按钮设置 `textContent`，否则会把三个图标节点一起清掉。

## 6. 图标集来源、版本与许可

- 图标集：**Lucide**
- Iconify 数据包：`@iconify-json/lucide`
- 版本：`1.2.146`
- 许可：`ISC`
- 源文件：`https://cdn.jsdelivr.net/npm/@iconify-json/lucide@1.2.146/icons.json`
- SHA-256：`fb5da55513386fdbbe12d7f265496c7770fa6b7b3c7578edcce587691bfadc57`

这些值记录在 `tooling/icons/icons.go` 与 `tooling/icons/data/README.md` 中。替换图标集版本时，
必须同时更新版本、SHA-256 与 `tooling/icons/data/lucide.json`；`TestLucideSourceIsLocked` 会拦截漂移。

图标源文件只作为构建期输入，不复制进应用二进制；生成项目也不会携带完整的 Iconify JSON。
生成后的 sprite 头部保留 Lucide/ISC 的来源注释。

## 7. 生产压缩

SVG 与 JavaScript 共用同一个可选工具 `minify`（tdewolff/minify）：

- `yd assets minify` 扫描 `web/static` 下的 `.js` 与 `.svg`，输出 `.min.js` / `.min.svg`；
- `yd build` 在编译前自动执行该阶段；
- 生产模板经 `asset` 助手解析，优先使用 `.min.svg`；
- CSS **不**进入该阶段：仍然只由 Tailwind v4 的 `--minify` 处理；
- `yd assets minify --check` 同时校验 JS 与 SVG，CI 可直接使用；
- 工具缺失时普通构建只告警并回退源码；`--check` 会失败。

手动刷新：

```bash
yd assets minify
yd assets minify --check
```

框架共享的 `icons.svg`、`icons.min.svg` 与 `yd-icon.min.svg` 需要提交入库；
生成项目自己的 `.min.*` 按项目约定由 `yd build` 重新生成。

## 8. 常见问题

**图标显示为空 / 只有一个空框**

1. 确认调用名完整且拼写正确：`lucide-log-in`，而不是 `log-in` 或 `i-lucide-log-in`；
2. 运行 `yd icons sync`；
3. 确认 `web/static/img/icons.svg` 中存在 `id="i-<引用>"`；
4. 生产环境确认 `icons.min.svg` 存在，或运行 `yd assets minify`。

**`yd icons check` 报漂移**

模板新增/删除了图标引用，或图标集版本变化。运行 `yd icons sync` 后重新提交 sprite。

**生成项目里图标 404**

框架 `web.Static()` 会合并框架静态树；如果项目使用了自定义静态根，请确认
`app.WithStaticFS(web.Static())` 已注入。项目也可以运行 `yd icons sync` 生成自己的 sprite。

**图标颜色不跟随主题**

图标颜色来自 `currentColor`。检查按钮/父元素是否被硬编码了颜色；颜色应来自语义令牌或
Tailwind 的 `text-*` 工具类。

## 9. 禁止事项

- 禁止引入 Lucide/Iconify 运行时脚本或 `iconify-icon` 自定义元素；
- 禁止在模板中写内联 `<svg>` 图标或硬编码 `<use href="#i-…">`；
- 禁止在运行时访问 Iconify API 或任何 CDN；
- 禁止手工编辑 `web/static/img/icons.svg`（文件头带生成标记）；
- 禁止把完整 Iconify JSON 复制到 `web/static` 或应用二进制中；
- 禁止用 `minify` 处理 Tailwind 输入 CSS（`app.css` / `components.css`）。

## 10. 验证清单

```bash
yd icons check
yd assets minify --check
go test ./tooling/icons/ ./tooling/build/ ./web/
go test github.com/shibingli/ydx/render
go build ./... && go vet ./... && go test ./... -race
```
