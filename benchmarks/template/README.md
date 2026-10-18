# Go 模板引擎兼容性与性能基准

独立 Go 模块，对比 yd 自研编译式 HTML 引擎与主流 Go 模板引擎。所有实现共享**同一份数据、
同一份页面语义**，并分别用各引擎的惯用语法表达；转义、布局、包含与自定义函数能力由
`BenchmarkEngines` / `TestEscapingMatrix` 实际执行验证，而不是按文档推断。

## 工作负载

一个 100 行的列表页（`page100`）和一个 5 行的短页（`small5`），两者使用同一模板：

- HTML 文本、带引号属性、URL 查询串三类上下文；
- `if/else`、`range`、嵌套字段访问；
- 每行 7 个字段（ID、Name、Email、URL、Class、Note、Active）；
- 数据中带有 `<script>`、`"`、`&`、`<` 等必须被转义的字符。

输出约 25 KB（100 行），因此基准同时衡量循环开销、上下文转义与输出缓冲管理。

各引擎模板位于 `templates/`；quicktemplate 与 templ 的生成代码（`page_qtpl.go`、
`page_templ.go`）随模块提交，生成命令见文末。

## 运行

```bash
cd benchmarks/template

# 正确性 + 转义矩阵
go test ./...

# 多核
go test -run '^$' -bench BenchmarkEngines -benchmem -benchtime=500ms -count=3

# 单核
GOMAXPROCS=1 go test -run '^$' -bench BenchmarkEngines -benchmem -benchtime=500ms -count=3

# 每实现独立进程，消除同进程顺序与 GC 偏差
for e in yd-render html/template text/template jet pongo2 liquid raymond mustache quicktemplate templ; do
  YD_BENCH_ONLY=$e go test -run '^$' -bench BenchmarkEngines -benchmem -benchtime=1s -count=5
done
```

`YD_BENCH_ONLY` 接受逗号分隔的引擎名。

## 兼容性矩阵

✅ 原生支持；⚠️ 需要显式配置或仅部分支持；❌ 不支持。

| 能力 | **yd-render** | html/template | text/template | Jet | Pongo2 | Liquid | Raymond | Mustache | quicktemplate | templ |
|---|---|---|---|---|---|---|---|---|---|---|
| 兼容 html/template 语法 | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌（Go 代码生成） | ❌（Go 代码生成） |
| 默认 HTML 自动转义 | ✅ | ✅ | ❌ | ✅ | ✅ | ⚠️ 需 `SetAutoEscapeReplacer` | ✅ | ✅ | ⚠️ 需 `{%s %}` | ✅ |
| 上下文感知转义（属性/URL/JS/CSS） | ✅ | ✅ | ❌ | ❌（仅 HTML） | ❌（仅 HTML） | ❌（仅 HTML） | ❌（仅 HTML） | ❌（仅 HTML） | ❌（手动选择 `{%q %}`/`{%u %}`） | ✅ |
| 危险 URL 协议拦截 | ✅ | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ |
| 布局/模板继承 | ✅ `define`/`template`/`block` | ✅ | ✅ | ✅ `extends`/`block` | ✅ `extends`/`block` | ✅ `layout` | ❌（用 partial 组合） | ❌（用 partial 组合） | ✅ Go 组合/嵌入 | ✅ 组件组合 |
| 包含/局部模板 | ✅ | ✅ | ✅ | ✅ `include` | ✅ `include` | ✅ `include` | ✅ partial | ✅ partial | ✅ Go 函数调用 | ✅ 组件调用 |
| 自定义函数 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ 过滤器 | ✅ helper | ❌（logic-less） | ✅ Go 函数 | ✅ Go 函数 |
| 安全类型绕过转义 | ✅ `template.HTML` 等 | ✅ | ❌（无转义） | ✅ `safeHtml` | ✅ `safe` 过滤器 | ⚠️ `raw` 过滤器 | ✅ `SafeString` | ✅ 三花括号 | ✅ `{%s= %}` | ✅ `templ.SafeHTML` |
| 模板预编译/缓存 | ✅ 加载期编译为不可变程序 | ✅ 首次执行时转义并缓存 | ✅ 解析树缓存 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ 编译期生成 Go | ✅ 编译期生成 Go |
| 并发安全渲染 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 运行时第三方依赖 | 0（标准库 + 本仓） | 0 | 0 | 1 | 2 | 3 | 2 | 1 | 1 | 1 |

### 转义矩阵（`go test -run TestEscapingMatrix -v` 实测）

敌意输入：文本 `<script>alert(...)</script>`、属性 `" onmouseover="alert(...)`、
URL `javascript:alert(...)`。

| 引擎 | 文本转义 | 属性转义 | URL 协议拦截 |
|---|---:|---:|---:|
| **yd-render** | ✅ | ✅ | ✅ |
| html/template | ✅ | ✅ | ✅ |
| text/template | ❌ | ❌ | ❌ |
| Jet | ✅ | ✅ | ❌ |
| Pongo2 | ✅ | ✅ | ❌ |
| Liquid（开启自动转义） | ✅ | ✅ | ❌ |
| Raymond | ✅ | ✅ | ❌ |
| Mustache | ✅ | ✅ | ❌ |
| quicktemplate（`{%s %}`） | ✅ | ✅ | ❌ |
| templ | ✅ | ✅ | ✅ |

结论：只有 `html/template`、`yd-render` 与 `templ` 同时具备**上下文感知转义与危险协议拦截**。
其它引擎的 HTML 转义能挡住文本与属性注入，但 `javascript:` URL 会原样进入 `href`。

## 性能（Windows 本机，2026-10-19）

测试平台：Intel Core i9-10980HK，16 个逻辑 CPU，Windows 11 Pro for Workstations，
Go 1.27.2 windows/amd64，`-benchtime=500ms -count=3`，取中位数。

> 这是开发机口径。仓库主表使用 Linux x86_64 基准平台；跨机比较请以相对关系为准，
> 绝对数值会受电源策略、温度与后台负载影响（通常 ±15%）。

### 单核（`GOMAXPROCS=1`，ns/op）

| 引擎 | page100 | small5 | page100 B/op | page100 allocs/op |
|---|---:|---:|---:|---:|
| quicktemplate | **28,838** | **1,707** | **0** | **0** |
| **yd-render** | **114,699** | **6,995** | **9,706** | **106** |
| templ | 149,414 | 9,057 | 68,735 | 1,823 |
| Jet | 162,731 | 10,901 | 496 | 6 |
| text/template | 233,477 | 14,442 | 11,544 | 811 |
| Pongo2 | 373,448 | 23,961 | 158,824 | 3,759 |
| Mustache | 394,966 | 22,509 | 113,246 | 5,633 |
| Liquid | 447,450 | 26,618 | 114,952 | 6,753 |
| html/template | 993,471 | 57,326 | 155,664 | 7,142 |
| Raymond | 1,692,299 | 74,376 | 1,793,001 | 10,551 |

### 多核顺序（16 逻辑 CPU，ns/op）

| 引擎 | page100 | small5 | page100 B/op | page100 allocs/op |
|---|---:|---:|---:|---:|
| quicktemplate | **27,111** | **1,622** | **0** | **0** |
| **yd-render** | **111,403** | **6,772** | **9,748** | **106** |
| templ | 127,229 | 7,628 | 68,868 | 1,823 |
| Jet | 166,932 | 10,887 | 503 | 6 |
| text/template | 226,752 | 13,842 | 11,557 | 811 |
| Mustache | 315,590 | 17,864 | 113,369 | 5,633 |
| Pongo2 | 384,816 | 24,130 | 158,961 | 3,759 |
| Liquid | 399,064 | 23,722 | 114,952 | 6,753 |
| html/template | 977,827 | 56,187 | 155,819 | 7,142 |
| Raymond | 1,878,359 | 63,072 | 1,820,134 | 10,560 |

### 并行（`b.RunParallel`，page100，ns/op）

| 引擎 | ns/op | 相对单核加速 | 等效页面吞吐 |
|---|---:|---:|---:|
| quicktemplate | **4,912** | 5.5× | ~203,600 页/s |
| **yd-render** | **30,599** | 3.6× | **~32,700 页/s** |
| Jet | 43,694 | 3.8× | ~22,900 页/s |
| text/template | 53,525 | 4.2× | ~18,700 页/s |
| templ | 55,201 | 2.3× | ~18,100 页/s |
| Mustache | 132,409 | 2.4× | ~7,600 页/s |
| Pongo2 | 141,836 | 2.7× | ~7,050 页/s |
| Liquid | 160,807 | 2.5× | ~6,200 页/s |
| html/template | 312,001 | 3.1× | ~3,200 页/s |
| Raymond | 1,424,072 | 1.3× | ~700 页/s |

### 结论

- **对标标准库**：`yd-render` 在 100 行页面上比 `html/template` 快 **8.7×**
  （114.7 µs vs 993.5 µs），分配从 **7,142 次降到 106 次**；比同样不做上下文转义的
  `text/template` 仍快 **2.0×**；小页面快 **8.2×**。
- **对标主流运行时引擎**：单核比 Jet 快 **1.4×**、比 Pongo2 快 **3.3×**、
  比 Mustache 快 **3.4×**、比 Liquid 快 **3.9×**、比 Raymond 快 **14.8×**；
  与代码生成式的 templ 基本相当（快 **1.3×**），且保留完整的 html/template 语法兼容。
- **与代码生成引擎的差距**：quicktemplate 仍快 **4.0×**，因为它把模板编译成直接读写
  `io.Writer` 的 Go 代码、且不做上下文感知转义（`{%s %}` 仅 HTML 转义）。这是
  「运行时模板 + 完整上下文转义」与「编译期代码生成 + 手动转义」的本质差异。
- **并行扩展**：`yd-render` 的并行吞吐约为 html/template 的 **10.2×**；
  Raymond、Mustache、Liquid 的并行伸缩较差（输出字符串分配与全局锁竞争）。
- **分配**：`yd-render` 每页 106 次分配（主要来自每个结构体行装箱一次，加上少量固定开销），
  比 html/template 少 **98.5%**；Jet 的 6 次分配来自其内部对象池与预先分配的输出缓冲。

## yd 引擎为什么快

1. **复用标准库前端**：解析与上下文转义仍由 `html/template` 完成，因此语法与安全语义
   与标准库逐字节一致；执行阶段换成自研节点图，不再走 `text/template` 的逐次反射调用。
2. **加载期编译**：模板在 `New` 时被编译为不可变程序；渲染只做指针遍历，没有解析、
   没有 clone、没有逐请求的函数表重建。
3. **按次执行绑定**：译者与权限主体存放在每次渲染的状态中，`tr`/`can` 直接读取，
   不再为每个语言/角色组合克隆整棵模板树。
4. **零装箱热路径**：单字段动作走 `reflect.Value` 路径，字符串/整数/布尔不装箱；
   单绑定动作（`tr`、`perm`、`can`）有专门的无接口输出路径。
5. **字节级链式转义**：URL → 属性这类多级转义在字节缓冲上完成，不产生中间字符串。
6. **缓冲复用**：引擎按页面实际增长后的容量保留输出缓冲；标准库和门面共享同一缓冲池。
7. **确定性缓存**：编译程序不可变、并发只读；不存在的按请求缓存失效问题。

## 生成代码

```bash
# quicktemplate
go run github.com/valyala/quicktemplate/qtc -file=templates/page.qtpl
mv templates/page.qtpl.go page_qtpl.go

# templ
go run github.com/a-h/templ/cmd/templ@v0.3.1070 generate ./templates/page.templ
mv templates/page_templ.go page_templ.go
```

生成文件已提交，日常运行不需要重新生成。

## 复现注意事项

- 每个实现都在同一进程内解析一次、复用已编译模板；基准循环只测渲染。
- Liquid 启用了 `render.HtmlEscaper` 自动转义，否则其默认输出不转义，对比会失真。
- Pongo2 的 Context 每轮重新构造，属于其正常调用成本；yd/html/Jet 直接接收结构体指针。
- quicktemplate 使用 `{%s %}`（HTML 转义）而非 `{%s= %}`（原样输出），否则对比不公平。
- 所有引擎的模板都用各自惯用语法表达同一页面语义，输出不要求逐字节相同（转义细节不同），
  但 `TestEnginesProduceOutput` 会校验行数与关键标记。
