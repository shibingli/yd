# 自研 HTML 模板引擎使用说明

`ydx/render` 是 yd 的默认 HTML 模板引擎。它保留 `html/template` 作为**解析器与上下文转义器**，
在执行阶段换成加载期编译出的不可变节点图，因此在语法、内置函数与安全语义与标准库一致的前提下，
把渲染热路径的反射装配、模板克隆与中间字符串分配降到最低。

本文面向业务开发与运维，覆盖接入方式、模板写法、布局、内置函数、i18n、权限、安全约束、
并发与缓存、压缩中间件、错误处理与迁移。性能数据与设计细节见
[`render-performance.md`](render-performance.md)。

## 目录

1. [设计概览](#1-设计概览)
2. [快速开始](#2-快速开始)
3. [目录、命名与加载规则](#3-目录命名与加载规则)
4. [模板语法](#4-模板语法)
5. [布局与片段](#5-布局与片段)
6. [内置函数](#6-内置函数)
7. [自定义函数](#7-自定义函数)
8. [国际化 tr](#8-国际化-tr)
9. [权限辅助函数](#9-权限辅助函数)
10. [安全约束](#10-安全约束)
11. [缓存、并发与 Dev 模式](#11-缓存并发与-dev-模式)
12. [缓冲与零拷贝 API](#12-缓冲与零拷贝-api)
13. [中间件与压缩](#13-中间件与压缩)
14. [错误处理](#14-错误处理)
15. [性能参考](#15-性能参考)
16. [从 html/template 迁移](#16-从-htmltemplate-迁移)
17. [常见问题](#17-常见问题)
18. [API 速查](#18-api-速查)

---

## 1. 设计概览

| 阶段 | 实现 |
|---|---|
| 解析 | `html/template` 完整解析 `text/template` 语法 |
| 转义 | `html/template` 按 HTML/属性/URL/JS/CSS 状态机重写每个动态动作 |
| 编译 | 读取转义后的解析树，编译为不可变节点图（`New` 时一次性完成） |
| 渲染 | 遍历节点图；字段、变量、映射与转义走类型化快速路径 |
| 缓存 | `atomic.Pointer` 发布不可变快照；渲染期只读，无锁 |
| 并发 | 每次渲染独立状态；输出缓冲、参数栈与暂存字节均池化 |

因此：

- **语法完全兼容**：`if`/`with`/`range`/`template`/`block`/`define`、管道、变量、
  `break`/`continue`、空白裁剪（`{{-` / `-}}`）、自定义分隔符等均由标准库解析。
- **安全语义完全兼容**：危险 URL 协议拦截、属性/JS/CSS/URL 上下文转义、
  `template.HTML` 等安全类型的行为与 `html/template` 一致。
- **不是另一套模板语言**：现有 `html/template` 模板可以原样迁移，不需要改语法。

一个入口若被 `html/template` 克隆到非文本上下文（例如在属性或脚本里调用的局部模板），
其派生树位于标准库内部命名空间，导出 API 不可见。这类入口会**透明回退**到标准库执行，
行为与安全性不变，只是该入口不享受编译执行器的加速。业务代码无需感知。

---

## 2. 快速开始

### 2.1 独立使用

模板放在 `templates/` 下，通过 `afero.Fs` 提供。`go:embed` 是最常见的做法：

```go
package main

import (
    "embed"
    "net/http"

    "github.com/shibingli/ydx/afs"
    "github.com/shibingli/ydx/log"
    "github.com/shibingli/ydx/render"
)

//go:embed templates
var assets embed.FS

func main() {
    engine, err := render.New(afs.NewEmbed(assets), render.Options{
        Root: "templates", // 默认值也是 "templates"
    })
    if err != nil {
        // 启动期装配失败：记录后终止。 | Assembly failure at startup: log and stop.
        log.Default().Fatal("load templates failed | 加载模板失败", log.Err(err))
    }

    http.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
        data := map[string]any{
            "Title": "用户列表",
            "Users": []map[string]any{
                {"ID": 1, "Name": "Ada", "Active": true},
            },
        }

        // 推荐：先渲染进缓冲，成功后再写响应。
        // 模板名是相对 Root 的斜杠路径。
        body, err := engine.RenderAppend(nil, "pages/users.html", data)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        _, _ = w.Write(body)
    })

    _ = http.ListenAndServe(":8080", nil)
}
```

最简形式也可以直接 `engine.Render(w, "pages/users.html", data)`：它会先渲染到池化缓冲，
成功后再一次性写出。若需要自行控制状态码、错误响应或复用缓冲，使用
`RenderAppend` / `AcquireBuffer`（见 [第 12 节](#12-缓冲与零拷贝-api)）。

### 2.2 在 app 门面中使用

`yd new` 生成的项目不需要手动创建引擎：`app.New` / `app.Default` 会用应用资产文件系统中的
`templates/` 目录自动构建内置引擎。

```go
// app.Default() 使用框架内嵌资产与默认中间件链；
// 自带 web 资产的项目用 app.New(WithFS(...)) 并自行装配中间件（脚手架即如此）。
a, err := app.New(
    app.WithFS(web.Assets()),
    app.WithLogger(log),
)
if err != nil {
    return err
}

a.GET("/users", func(c *router.Ctx) {
    data := map[string]any{"Title": "用户", "Users": users}
    if err := a.Render(c, http.StatusOK, "pages/users.html", data); err != nil {
        // 渲染失败已经记录或可自行处理
    }
})
```

`app.Render` 在整页/片段、布局、i18n 注入、错误缓冲方面的语义见
[第 5 节](#5-布局与片段)。

### 2.3 替换或定制引擎

需要额外函数时，先构造引擎，再通过 `WithRenderer` 注入：

```go
engine, err := render.New(afs.NewEmbed(assets), render.Options{
    Root:  "templates",
    Funcs: template.FuncMap{"money": formatMoney},
    Dev:   isDevelopment(),
})
if err != nil {
    return nil, err
}

a, err := app.New(
    app.WithFS(web.Assets()),
    app.WithRenderer(engine),
)
```

注意 `app.WithRenderer` 接收的是 `router.Renderer` 接口（只有 `Render(io.Writer, string, any) error`）。
传入 `*render.Engine` 时，`app.Render` 会自动使用它的 `RenderAppend` 与缓冲池能力。

---

## 3. 目录、命名与加载规则

### 3.1 默认约定

```
templates/
├── layouts/     # 整页骨架，例如 base.html
├── partials/    # 可复用片段
└── pages/       # 业务页面与局部视图
```

- **扫描根**：`Options.Root`，默认 `"templates"`。路径统一转为 `/` 分隔。
- **扩展名**：`Options.Exts`，默认 `[".html", ".gotmpl"]`。
- **模板名**：相对扫描根、以 `/` 分隔的路径，例如 `layouts/base.html`、
  `pages/users.html`、`partials/users/row.html`。
- **同一模板集**：根下所有文件解析进同一个模板集，任何页面都能
  `{{ template "partials/users/row.html" . }}`。
- **`define` 块**：`{{ define "users.row" }}...{{ end }}` 定义的名字同样可作为入口渲染，
  `Exists("users.row")` 也返回真。
- **跳过文件**：文件名以 `.` 或 `_` 开头的文件不参与扫描
  （例如 `_helpers.html`、`.draft.html`）。
- **稳定顺序**：文件按模板名排序加载；`Names()` 返回排序后的文件列表。

### 3.2 加载时机

| 模式 | 行为 |
|---|---|
| 生产（`Dev: false`） | `New` 时解析、转义、编译全部入口；之后渲染只读快照 |
| 开发（`Dev: true`） | 首次 `New` 不加载；每次 `Render` / `Exists` 前重新扫描并编译 |
| `Clone` / `AddFunc` | 返回新引擎并立即加载（Dev 模式除外） |

Dev 模式每次渲染都会重新解析与编译，只应用于开发期。`app` 门面依据
`app.env` 自动选择：非 `production` 时传 `Dev: true`，因此 `yd dev` 下修改模板无需重启；
生产二进制则使用编译快照。

---

## 4. 模板语法

引擎完整复用 `html/template` 的语法。以下写法均可直接使用：

```gotemplate
{{/* 注释 */}}

<h1>{{ .Title }}</h1>
<p>{{ .User.Name }}</p>
<p>{{ .User.DisplayName "short" }}</p>       {{/* 带参数的方法 */}}

{{ $total := len .Users }}
<p>{{ $total }} 位用户</p>

{{ if .SignedIn }}
  {{ .User.Name }}
{{ else if .Guest }}
  访客
{{ else }}
  <a href="/login">登录</a>
{{ end }}

{{ with .Tenant }}
  <p>{{ .Name }}</p>
{{ else }}
  <p>无租户</p>
{{ end }}

<ul>
{{ range $i, $u := .Users }}
  <li>{{ $i }}: {{ $u.Name }}</li>
{{ else }}
  <li>暂无数据</li>
{{ end }}
</ul>

{{ range .Users }}
  {{ if .Hidden }}{{ continue }}{{ end }}
  {{ if .Stop }}{{ break }}{{ end }}
{{ end }}

{{ template "partials/users/row.html" . }}
{{ block "page.footer" . }}默认页脚{{ end }}
```

支持的 `range` 对象与标准库一致：数组、切片、映射、通道、整数，以及
`iter.Seq` / `iter.Seq2` 形式的 range-over-func。映射遍历按 key 排序，结果稳定。

**缺失键**：与标准库一致，渲染为 `<no value>`。模板函数收到缺失/`nil` 值时按各自的
失败关闭规则处理（见第 9 节）。

**自定义分隔符**：

```go
engine, err := render.New(fsys, render.Options{
    LeftDelim:  "[[",
    RightDelim: "]]",
})
```

模板中的 `[[ .Name ]]` 与标准库行为一致。

---

## 5. 布局与片段

### 5.1 app.Render 的整页/片段语义

`app.Render(c, status, name, data)` 根据请求头自动选择输出形态：

| 请求 | 输出 |
|---|---|
| `HX-Request: true` 且未显式要求整页 | 只渲染 `name`，作为片段返回 |
| 普通请求，或 `HX-Request-Type: full` | 先渲染 `name`，再注入布局 `layouts/base.html` 返回整页 |
| 应用没有 `layouts/base.html` | 直接返回裸模板 |

布局通过以下数据键拿到片段：

- `.Content`（`app.ContentKey`）：片段 HTML，类型为 `template.HTML`（可信，不再转义）。
- `.content`：同一值的小写别名，便于模板书写习惯。
- 调用方的其它数据键原样可访问；若传入的不是 `map[string]any`，原值放在 `.Data`
  （`app.DataKey`）。

布局示例：

```gotemplate
<!DOCTYPE html>
<html lang="{{ with .Lang }}{{ . }}{{ else }}zh-CN{{ end }}">
<head>
  <meta charset="utf-8">
  <title>{{ with .Title }}{{ . }}{{ else }}yd{{ end }}</title>
</head>
<body>
  {{ .Content }}
</body>
</html>
```

页面片段示例：

```gotemplate
<section class="yd-page">
  <h1>{{ .Title }}</h1>
  {{ range .Users }}
    {{ template "partials/users/row.html" . }}
  {{ end }}
</section>
```

`app.Render` 在整页路径上把片段与布局**追加进同一个池化缓冲**，只做一次字符串转换
（布局数据键需要 `template.HTML`）。页面失败时正文尚未写出，因此不会出现半截响应。

### 5.2 独立引擎组合布局

不使用 `app` 门面时，用 `RenderAppend` 自行组合，避免两次缓冲拷贝：

```go
func renderPage(engine *render.Engine, name string, data map[string]any) ([]byte, error) {
    page, err := engine.RenderAppend(nil, name, data)
    if err != nil {
        return nil, err
    }

    layout := make(map[string]any, len(data)+1)
    for k, v := range data {
        layout[k] = v
    }
    layout["Content"] = template.HTML(page) // 可信：由本应用模板产出

    return engine.RenderAppend(nil, "layouts/base.html", layout)
}
```

若希望连布局也追加进同一缓冲（片段在前、布局在后），参考 `runtime/app/render.go` 的
`renderWholeAppend`：记录 `layoutStart := len(page)`，布局追加后只写出
`full[layoutStart:]`。

---

## 6. 内置函数

默认函数表由 `render.Funcs(fsys)` 提供，`Options.Funcs` 可覆盖同名项。完整列表：

| 函数 | 签名 | 说明 |
|---|---|---|
| `tr` | `(key string, args ...any) string` | i18n 翻译，见第 8 节 |
| `can` | `(ref any) bool` | 能力判定，见第 9 节 |
| `hasRole` | `(ref any) bool` | 角色判定 |
| `perm` | `(ref any) string` | 能力引用解析 |
| `role` | `(ref any) string` | 角色引用解析 |
| `dict` | `(kv ...any) (map[string]any, error)` | 交替键值构造映射 |
| `list` | `(v ...any) []any` | 参数构造切片 |
| `default` | `(def, v any) any` | `v` 为空时返回 `def` |
| `contains` | `(s, substr string) bool` | 同 `strings.Contains` |
| `hasPrefix` | `(s, prefix string) bool` | 同 `strings.HasPrefix` |
| `hasSuffix` | `(s, suffix string) bool` | 同 `strings.HasSuffix` |
| `lower` | `(s string) string` | 转小写 |
| `upper` | `(s string) string` | 转大写 |
| `trim` | `(s string) string` | 去首尾空白 |
| `join` | `(elems []string, sep string) string` | 字符串切片连接 |
| `json` | `(v any) (template.JS, error)` | JSON 编码，标记为脚本安全 |
| `humanSize` | `(n int64) string` | 字节数转 `KiB`/`MiB`/… |
| `humanTime` | `(t time.Time) string` | 本地时间 `2006-01-02 15:04:05` |
| `hx` | `(kv ...any) (template.HTMLAttr, error)` | 构造 htmx 属性 |
| `asset` | `(p string) (string, error)` | 资源路径追加内容哈希 |

标准库内建函数同样可用：`and`、`or`、`not`、`len`、`index`、`slice`、`print`、
`printf`、`println`、`html`、`js`、`urlquery`、`eq`、`ne`、`lt`、`le`、`gt`、`ge`、`call`。

### 6.1 常用示例

```gotemplate
{{/* dict 构造临时映射；参数必须是偶数个，键必须是字符串 */}}
{{ $attrs := dict "class" "yd-btn" "id" "save" }}

{{/* default 在值为 nil、零值、空串、空切片或空映射时给出默认值 */}}
<p>{{ default "—" .Note }}</p>

{{/* list 构造切片 */}}
{{ range list "a" "b" "c" }}<span>{{ . }}</span>{{ end }}

{{/* join 接收 []string */}}
<p>{{ join .Tags ", " }}</p>

{{/* json：返回 template.JS，可安全嵌入 <script> */}}
<script>
  window.__DATA__ = {{ json .Data }};
</script>

{{/* humanSize / humanTime */}}
<td>{{ humanSize .Bytes }}</td>
<td>{{ humanTime .UpdatedAt }}</td>
```

### 6.2 `hx`

```gotemplate
<button {{ hx "get" "/users" "target" "#yd-workspace" "swap" "innerMorph" }}>
  加载
</button>
```

输出（属性按键名排序）：

```html
<button hx-get="/users" hx-swap="innerMorph" hx-target="#yd-workspace">
```

- 键可带可不带 `hx-` 前缀；属性名只允许字母、数字、`_`、`-`、`:`，
  否则返回 `ErrHtmxInvalidKey`。
- **每个值都会被 HTML 转义**。结果类型是 `template.HTMLAttr`，`html/template`
  会原样插入，所以转义必须由辅助函数完成，不能依赖外层上下文。
- 不要用它拼接任意 HTML；它只用于属性。

### 6.3 `asset`

```gotemplate
<link rel="stylesheet" href="{{ asset "/static/css/yd.css" }}">
<script src="{{ asset "/static/js/app.js" }}"></script>
```

- 参数是 URL 路径；文件在引擎的文件系统中去掉前导 `/` 后读取。
- 内容 SHA-256 的前 4 字节（8 位十六进制）作为 `v` 查询参数：
  `/static/css/yd.css?v=1a2b3c4d`。
- 已有查询串时用 `&` 追加。
- 哈希按引擎实例缓存；Dev 模式重新加载会重建缓存，因此文件改动会得到新哈希。
- 文件不存在返回 `ErrAssetNotFound`。
- `render.Options.Production` 为 true 时优先解析同名 `.min.js` / `.min.css`，并对压缩文件取哈希；
  压缩文件不存在时回退源码。`.min.*` 路径原样处理，不会二次加后缀。
- `render.Options.AssetFS` 应指向 `/static` 的合并文件系统（根为 `/static/` 前缀）；
  生成项目通过 `app.WithStaticFS(web.Static())` 注入，否则框架共享脚本会解析失败。

---

## 7. 自定义函数

### 7.1 加载期注册

```go
engine, err := render.New(fsys, render.Options{
    Funcs: template.FuncMap{
        "money": func(cents int64) string {
            return fmt.Sprintf("%.2f", float64(cents)/100)
        },
    },
})
```

`Options.Funcs` 中的同名函数会**覆盖**引擎默认函数，包括 `tr`、`can` 等。
一旦覆盖 `tr`，注入的译者不再被使用。

### 7.2 运行时追加

`AddFunc` 返回**新引擎**，不修改原实例：

```go
next := engine.AddFunc("money", formatMoney)
```

它会重新加载整个模板集（Dev 模式除外，下一次渲染时加载）。只应在装配阶段使用，
禁止按请求调用。

### 7.3 函数签名约束

与 `html/template` 一致：

- 可返回一个值，或 `(值, error)`；
- 参数与返回值必须可被模板值赋值；第二个返回值若是 `error` 且非 nil，渲染终止；
- 不要返回未受控的 `template.HTML`、`template.JS` 等安全类型，除非内容确实可信。

非法的函数名或签名会在加载期返回 `ErrInvalidFunc`，不会等到渲染时才失败。

---

## 8. 国际化 tr

引擎本身不附带语言目录。模板统一写：

```gotemplate
<h1>{{ tr "page.users.title" }}</h1>
<p>{{ tr "page.users.count" .Total }}</p>
```

### 8.1 自动注入（app 门面）

`app.Default` 装配 i18n 中间件后，`app.Render` 会把当前请求的译者注入
`render.TranslatorKey`（值为 `"T"`），`tr` 因此跟随协商出的语言。

### 8.2 手动注入（独立使用）

`tr` 的译者不是函数参数，而是通过**渲染数据**注入；只有 `map[string]any` 形态的数据
会被识别：

```go
data := map[string]any{
    render.TranslatorKey: translator, // 实现 T(key string, args ...any) string
    "Users":              users,
}
err := engine.Render(w, "pages/users.html", data)
```

`i18n.Translator` 满足 `render.Translator`：

```go
tr := bundle.Translator("zh-CN")
data[render.TranslatorKey] = tr
```

未注入译者时，`tr` **返回键本身**。缺失的语言目录或漏装配会直接显现在页面上，
而不是让渲染失败或 panic。

### 8.3 覆盖 tr

如果业务需要自己的措辞逻辑，通过 `Options.Funcs` 覆盖 `"tr"`；该覆盖优先于注入的译者：

```go
render.Options{Funcs: template.FuncMap{"tr": myTr}}
```

---

## 9. 权限辅助函数

模板中的权限判断写为：

```gotemplate
{{ if can .Perm.UserRead }}
  <a href="/users">用户</a>
{{ end }}

{{ if hasRole "RoleAdmin" }}
  <span class="badge">管理员</span>
{{ end }}

<button data-yd-perm="{{ .Perm.UserCreate }}">新建</button>
```

### 9.1 注入方式

`authz.Data(subject)` 一次返回四个键，合并进渲染数据即可：

```go
data := map[string]any{"Users": users}
for k, v := range authz.Data(subject) {
    data[k] = v
}
// data 现在包含：
//   render.AuthzKey -> Authz 实现
//   "Perm"          -> 能力名映射（模板读 .Perm.UserRead）
//   "Role"          -> 角色名映射
//   "AuthzJSON"     -> 前端权限信封（已序列化）
```

- `can` / `hasRole` / `perm` / `role` 只在该次执行中绑定，不污染共享引擎。
- 未注入 `Authz` 时：`can` 与 `hasRole` 恒为 `false`；`perm` / `role` 原样返回字符串引用。
  这是**失败关闭**：漏装配只会让受保护控件不显示，不会意外放行。
- 非字符串引用（例如 `.Perm` 缺键得到 `<no value>`）一律视为未授予，不会让渲染失败。

### 9.2 红线

**前端权限判定只是体验优化，不是安全边界。**

- 隐藏按钮不等于禁止操作；每个受保护端点仍必须在服务端校验
  （`authz.Require` / `authz.Allow` / `authz.RequirePermission`）。
- 载荷缺失或损坏时一律失败关闭。
- 权限字符串应通过 `authz` 注册表引用，避免在模板里写裸字符串；
  `yd authz check` 会校验未注册能力、裸字符串与未引用能力。

---

## 10. 安全约束

### 10.1 自动上下文转义

以下上下文由 `html/template` 在加载期决定转义方式，业务代码不需要手工处理：

| 上下文 | 示例 |
|---|---|
| HTML 文本 | `<p>{{ .Name }}</p>` |
| 属性值 | `<div class="{{ .Class }}">` |
| URL | `<a href="/u/{{ .ID }}">` |
| JS 值/字符串/正则 | `<script>var x = {{ .JSON }};</script>` |
| CSS 值 | `<style>.x { color: {{ .Color }} }</style>` |
| RCDATA | `<textarea>{{ .Text }}</textarea>` |

危险 URL 协议（如 `javascript:`）会被替换为 `#ZgotmplZ`，与标准库一致。

### 10.2 安全类型

需要显式绕过某一层转义时，使用标准库类型并**确保来源可信**：

| 类型 | 用途 |
|---|---|
| `template.HTML` | 可信 HTML 片段（布局中的 `.Content`） |
| `template.HTMLAttr` | 可信属性串（`hx` 返回该类型） |
| `template.URL` | 可信 URL |
| `template.JS` / `template.JSStr` | 可信 JS 表达式 / 字符串 |
| `template.CSS` | 可信 CSS |
| `template.Srcset` | 可信 srcset |

**禁止**把请求参数、数据库文本或用户输入直接转换为上述类型。需要富文本时，
应由独立的清洗器（白名单）处理后再标记为安全。

### 10.3 其它约束

- `json` 使用 `ydx/json` 编码，默认转义 HTML 敏感字符（`<`、`>`、`&` 等），
  可安全嵌入 `<script>`；不要再用 `template.HTML` 包一层。
- `hx` 自行转义所有值并校验属性名（见 6.2），不要手工拼接 `hx-*` 属性。
- 模板中禁止引入公网 CDN 脚本/样式；资产必须经 `go:embed` 与 `/static/...` 提供。
- 不要输出未转义的错误详情、SQL、堆栈或密钥；日志走 `log` 且同样遵守脱敏要求。

---

## 11. 缓存、并发与 Dev 模式

### 11.1 并发安全

`*render.Engine` 可被任意数量的 goroutine 同时调用：

- 编译结果不可变，经 `atomic.Pointer[engineState]` 发布；
- 每次渲染的执行状态、参数暂存栈与暂存字节独立；
- 渲染期间只读取节点图、转义表、字段索引与字符串常量；
- 输出缓冲通过 `sync.Pool` 复用，单次渲染不会与其它渲染共享缓冲。

与任何持有 `sync.Pool` 的类型一样，**首次使用后不得复制 Engine**；始终传递指针。

### 11.2 Dev 模式

```go
render.Options{Dev: true}
```

Dev 模式下：

- `New` 不立即加载；
- 每次 `Render` / `Exists` 前重新扫描、解析、转义与编译；
- 加载由互斥锁串行化；
- 模板语法错误在渲染时以 `ErrParseTemplate` 返回，不会污染进程。

因此 Dev 模式适合 `yd dev` 的热重载，**不要用于生产**：生产应使用 `Dev: false`，
让模板只编译一次。

### 11.3 Clone 与 AddFunc

- `Clone()` 共享文件系统与选项，返回独立引擎；非 Dev 下立即加载。
- `AddFunc(name, fn)` 等价于「克隆 + 注册函数 + 重新加载」。
- 两者都会重新解析与编译，属于装配期操作，不要放进请求路径。

---

## 12. 缓冲与零拷贝 API

### 12.1 Render

```go
err := engine.Render(w, name, data)
```

- 编译入口先渲染进引擎池化缓冲，成功后一次性写入 `w`；
- 返回的写入错误会被原样返回；
- 适合普通 HTTP 响应。

### 12.2 RenderAppend

```go
out, err := engine.RenderAppend(dst, name, data)
```

- 把渲染结果**追加**到 `dst` 并返回新切片；
- 预分配参考模板的静态长度（上限 64 KiB）；
- 出错时返回 `dst[:len(dst)]` 的原始内容，不留下半截输出；
- 适合组合布局、写入自有缓冲或再次处理字节的场景。

### 12.3 AcquireBuffer / ReleaseBuffer

```go
buf := engine.AcquireBuffer(0)
defer engine.ReleaseBuffer(buf)

out, err := engine.RenderAppend(buf.B, "pages/users.html", data)
if err != nil {
    return err
}
buf.B = out // 必须写回：让池保留增长后的容量
_, _ = w.Write(out)
```

- `AcquireBuffer(hint)` 返回长度 0、容量至少 `hint` 的缓冲（最小 4 KiB）；
- 归还后缓冲可被后续请求复用；容量超过 1 MiB 的缓冲不再保留，避免池无限增长；
- 使用 `RenderAppend` 增长缓冲后，**必须把返回切片写回 `buf.B`**，否则增长后的容量不会被池记住；
- 归还前不得继续持有或写入该缓冲。

---

## 13. 中间件与压缩

引擎只负责生成 HTML 字节，不直接操作 HTTP。无论通过 `app.Render` 还是
`c.Writer` 写出，响应都会经过已注册的中间件链，压缩中间件无需特殊配置：

```go
a.Use(
    middleware.Recover(log),
    middleware.RequestID(""),
    middleware.AccessLog(log),
    middleware.Compress(middleware.CompressOptions{}),
)
```

- `app.Default` 默认链为 Recover → RequestID → AccessLog → Compress → CORS → I18n。
- 整页渲染在缓冲完成后一次性写出，压缩器看到的是完整正文，压缩率与流式行为稳定。
- 片段响应（`HX-Request: true`）走同一条写出路径，同样会被压缩。
- `runtime/app/render_test.go` 的 `TestRenderFlowsThroughCompressionMiddleware`
  同时覆盖整页与片段两条路径。
- 不要为了压缩而在模板里手工 gzip，也不要绕过 `c.Writer` 直接写底层连接。

其它依赖响应的中间件（安全头、CSP nonce、缓存控制）同理：在 `app.Render` 之前设置，
或包装 `c.Writer`；引擎不缓存响应，不会绕过它们。

---

## 14. 错误处理

所有错误均为哨兵（`errs`），调用方用 `errors.Is` / `errors.As` 判定，禁止字符串匹配：

| 哨兵 | 含义 |
|---|---|
| `ErrNoTemplates` | 扫描根下没有模板文件 |
| `ErrTemplateNotFound` | 模板名不存在 |
| `ErrNilFS` | 文件系统为 nil |
| `ErrReadTemplate` | 模板文件不可读 |
| `ErrParseTemplate` | 语法错误或不可编译的节点 |
| `ErrWalkTemplates` | 遍历模板根失败 |
| `ErrExecuteTemplate` | 执行器自身失败（类型不匹配、参数个数、非法 range 等）；模板函数返回的错误原样透传 |
| `ErrCloneTemplate` | 单次执行的模板集克隆失败 |
| `ErrAssetNotFound` | `asset` 找不到资源 |
| `ErrDictOddArgs` | `dict` 参数个数为奇数 |
| `ErrDictKeyNotString` | `dict` 的键不是字符串 |
| `ErrJSONMarshal` | 模板内 JSON 编码失败 |
| `ErrHtmxOddArgs` | `hx` 参数个数为奇数 |
| `ErrHtmxKeyNotString` | `hx` 的键不是字符串 |
| `ErrHtmxInvalidKey` | `hx` 的键不是合法属性名 |
| `ErrInvalidFunc` | 函数名或签名不可用 |

示例：

```go
body, err := engine.RenderAppend(nil, "pages/users.html", data)
if err != nil {
    switch {
    case errors.Is(err, render.ErrTemplateNotFound):
        http.NotFound(w, r)
    case errors.Is(err, render.ErrParseTemplate):
        log.Error("template broken | 模板损坏", log.Err(err))
        http.Error(w, "template error", http.StatusInternalServerError)
    default:
        http.Error(w, "render error", http.StatusInternalServerError)
    }
    return
}
```

加载期错误（`New`）应视为装配失败：让进程拒绝启动，而不是带着坏模板运行。

---

## 15. 性能参考

Windows / i9-10980HK / Go 1.27.2，100 行列表页，单核中位数（`-benchtime=2s`，3–5 遍）：

| 引擎 | ns/op | allocs/op |
|---|---:|---:|
| quicktemplate | 27,036 | 0 |
| **yd-render** | **41,748** | **0** |
| templ | 140,378 | 1,823 |
| Jet | 165,985 | 6 |
| text/template | 247,408 | 811 |
| html/template | 1,055,921 | 7,142 |

- 比 `html/template` 快 **25.3×**，比 `text/template` 快 **5.9×**；
- 框架真实页面 dashboard 为 **5.1 µs / 2 allocs**（标准库约 82.8 µs / 769），约 **16.2×**；
- 上表把输出写到 `io.Discard`；把页面物化到真实缓冲后，yd-render 为 **40.7 µs**，
  quicktemplate 为 **39.0 µs**，两者基本持平（1.04×）。

完整对比、转义矩阵与复现命令见 [`benchmarks/template/README.md`](../benchmarks/template/README.md)，
瓶颈分析与设计见 [`render-performance.md`](render-performance.md)。

---

## 16. 从 html/template 迁移

### 16.1 API 对照

| html/template | render |
|---|---|
| `template.New(...).Funcs(...).ParseFS(...)` | `render.New(fsys, Options{Root, Funcs, Exts, LeftDelim/RightDelim, Dev})` |
| `t.ExecuteTemplate(w, "pages/users.html", data)` | `engine.Render(w, "pages/users.html", data)` |
| `t.ExecuteTemplate` + `bytes.Buffer` | `engine.RenderAppend(dst, name, data)` |
| `t.Lookup(name)` 判断存在 | `engine.Exists(name)` |
| `t.Templates()` | `engine.Names()`（仅文件）/ `Exists`（含 define） |
| `t.Clone()` + `Funcs` 按请求重绑 | 直接注入 `render.TranslatorKey` / `render.AuthzKey` |

### 16.2 迁移步骤

1. 模板文件保持原样；确认放在 `Options.Root` 下，文件名不以 `.` / `_` 开头。
2. 把加载期的 `Funcs` 合并进 `Options.Funcs`。
3. 把每次请求的 `ExecuteTemplate` 改为 `Render` / `RenderAppend`。
4. 删除按语言/角色克隆模板集的代码：译者与权限主体改为数据注入。
5. 若模板大量使用 `{{ .X }}`，数据继续用 `map[string]any` 即可获得零分配热路径；
   结构体同样支持，只是字段访问走反射。

### 16.3 行为差异

- 访问模板不存在时返回 `ErrTemplateNotFound`（标准库返回其内部错误文本）；
  用 `errors.Is` 判定即可。
- 执行器自身产生的错误以 `ErrExecuteTemplate` 报告；模板函数返回的非 nil 错误则**原样透传**，
  调用方可直接用 `errors.Is/As` 判定业务哨兵。
- Dev 模式每次渲染都会重新编译，标准库不会；生产请关闭 Dev。
- 派生上下文入口透明回退标准库，输出一致，不保证速度与编译入口相同。

---

## 17. 常见问题

**页面显示的是 `page.users.title` 而不是译文。**
没有注入译者。检查数据是否为 `map[string]any`，以及 `render.TranslatorKey` 下是否为
`render.Translator`；app 门面还需确认 i18n 中间件已装配。

**`can` 永远为 false。**
没有注入 `Authz`，或数据不是 `map[string]any`。合并 `authz.Data(subject)` 的返回值，
并确认键是 `render.AuthzKey`。未接入时的失败关闭是预期行为。

**改了模板但页面没变。**
生产模式会缓存编译结果。开发期请使用 `Dev: true`（app 门面在非 production 环境自动开启），
或重新构建二进制。

**`New` 返回 `ErrNoTemplates`。**
扫描根不正确、扩展名不匹配，或文件都以 `.` / `_` 开头。确认 `Root` 是相对文件系统根的
`templates`，且文件扩展名在 `Exts` 中。

**模板名找不到，但文件明明存在。**
模板名是**相对 Root 的路径**，不是文件名。例如文件 `templates/pages/users.html`
的模板名是 `pages/users.html`，不是 `users.html`。

**布局里 `.Content` 显示为转义后的文本。**
布局必须通过 `app.Render` 渲染，或手动把片段赋给 `template.HTML` 类型的 `Content` 键。
直接传普通字符串会被 `html/template` 当作不可信文本转义。

**`asset` 返回 `ErrAssetNotFound`。**
参数应从 `/` 开始书写 URL 路径，引擎会去掉前导 `/` 后在文件系统中查找；
确认文件确实位于该文件系统下。

**能在模板里调用任意 Go 方法吗？**
与 `html/template` 一致：只能调用导出的、返回值不超过两个且第二个为 `error` 的方法；
热路径会走类型化快速路径，但方法本身仍由反射调用。

---

## 18. API 速查

```go
// 构造
func New(fsys afero.Fs, o Options) (*Engine, error)

type Options struct {
    Funcs      template.FuncMap
    Root       string   // 默认 "templates"
    LeftDelim  string
    RightDelim string
    Exts       []string // 默认 [".html", ".gotmpl"]
    Dev        bool
}

// 渲染
func (e *Engine) Render(w io.Writer, name string, data any) error
func (e *Engine) RenderAppend(dst []byte, name string, data any) ([]byte, error)

// 缓冲
func (e *Engine) AcquireBuffer(hint int) *Buffer
func (e *Engine) ReleaseBuffer(b *Buffer)
//   Buffer{ B []byte }

// 查询与装配
func (e *Engine) Names() []string
func (e *Engine) Exists(name string) bool
func (e *Engine) Clone() *Engine
func (e *Engine) AddFunc(name string, fn any) *Engine
func (e *Engine) FuncMap() template.FuncMap
func (e *Engine) Filesystem() afero.Fs
func (e *Engine) Options() Options

// 注入键
const TranslatorKey = "T"
const AuthzKey      = "Authz"

// 默认函数表
func Funcs(fsys afero.Fs) template.FuncMap
```

使用建议：

1. **生产只加载一次**：`New` 放在装配期，`Dev: false`。
2. **数据优先 `map[string]any`**：映射直读无需反射装箱，是零分配热路径的前提。
3. **布局用 `app.Render`**：片段/整页、i18n、压缩与错误缓冲一次处理。
4. **安全类型只用可信来源**：用户输入永远走默认转义。
5. **权限仍须服务端校验**：模板里的 `can` / `hasRole` 只是界面优化。
