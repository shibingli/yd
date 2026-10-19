# render 性能瓶颈分析与编译式引擎设计

本文记录 `http/render` 从「直接执行 html/template」迁移到「加载期编译、渲染期执行
不可变程序」的完整分析、实现与验证。基准数值取自 Windows / i9-10980HK / Go 1.27.2；
跨机比较请以相对关系为准。面向业务开发的接入与模板写法见
[`render-usage.md`](render-usage.md)。

## 1. 基线

旧实现在一个 2 个模板文件、4 个列表项的小页面上：

| 场景 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| 无绑定 | 7,574 | 1,770 | 71 |
| 注入译者 | 7,732 | 1,849 | 76 |
| 译者 + 权限 | 8,344 | 1,849 | 76 |

框架 dashboard 页面（真实模板、~18 KB 输出）约 **99 µs / 769 allocs**。

## 2. 瓶颈分解

### 2.1 逐次执行的反射装配（最大项）

`html/template` 的执行器就是 `text/template` 的执行器：

- 每次动作求值都构造 `reflect.Value` 并做字段/方法查找；
- 每个函数调用走 `reflect.Value.Call`，参数逐个 `validateType`；
- `printValue` 走 `fmt.Fprint`，字符串/整数装箱；
- 每个 `range` 元素、每个结构体字段都会产生接口装箱。

这些成本无法通过配置消除，因为它是标准库执行模型的固定开销。

### 2.2 按绑定集克隆模板

旧引擎为了让每个请求使用不同的译者与权限主体，在模板集加载后还要：

1. `source.Clone()` 复制整棵解析树；
2. `Funcs(...)` 重绑 `tr`/`can`/`hasRole`/`perm`/`role`；
3. 按「语言 + 判定集合」缓存克隆结果（上限 16，FIFO）。

克隆本身在引入缓存前约 **21 µs / 121 allocs**；即使命中缓存，每次渲染仍要为
绑定查找、`Lookup(name)`、执行器状态分配付费。语言/角色组合多于 16 时还会反复克隆。

### 2.3 门面的双重缓冲

`App.Render` 的整页路径：

1. 片段渲染进 `bytes.Buffer`；
2. `page.String()` 复制一次；
3. 布局渲染进第二个 `bytes.Buffer`；
4. 再写出。

25 KB 的页面因此至少多一次完整拷贝与两个缓冲对象。

### 2.4 输出缓冲增长

旧引擎没有输出预分配；`bytes.Buffer` 从 0 开始按倍增增长，25 KB 输出会经历
1K→2K→…→32K 多次分配，且增长后的缓冲不会复用。

### 2.5 与代码生成引擎的结构性差距

以 quicktemplate 为参照做 CPU 剖析（100 行页、单核、`io.Discard`）后，差距来自三类
**结构性成本**，而不是某一条慢指令：

| 成本 | quicktemplate | yd-render |
|---|---|---|
| 模板执行 | 编译期生成 Go 代码，字段访问与循环就是普通 Go | 运行时不可变节点图 + 反射字段访问 |
| 上下文转义 | 仅替换 `< > " ' &` 五个字符 | 完整复刻 html/template：`+`、NUL、属性/RCDATA/JS/CSS/URL、协议过滤与 URL 规范化 |
| 输出 | 直接流式写 `io.Writer`；目标为 `io.Discard` 时不物化任何字节 | 为「要么整页、要么无输出」先物化到池化缓冲，再一次性写出 |

在优化前的剖析中，HTML 转义约占 22.7%，URL 链（过滤 + 规范化 + 属性转义）约 16%，
反射字段访问约 15%，节点分派与静态文本写入约 10%，输出拷贝（memmove）约 10.6%。
这些比例决定了优化顺序：先消除转义中的重复扫描与中间缓冲，再压缩字段路径的分类开销。

## 3. 新设计

### 3.1 复用标准库作为「编译器前端」

`html/template` 的解析树在首次执行时会被上下文转义器重写：每个动态动作末尾追加
`_html_template_*`。这些重写后的解析树通过导出字段 `Template.Tree` 可见。新引擎：

1. 用 html/template 解析全部文件；
2. 对每个入口在**独立克隆**中触发一次转义（避免一个入口的上下文错误污染其它入口）；
3. 读取转义后的解析树，编译为不可变节点图；
4. 渲染时只遍历节点图。

因此语法、`define`/`template`/`block`、内置函数、转义语义全部与标准库一致，
不需要重新实现上下文状态机。

派生上下文（例如在属性或脚本中调用的局部模板）的克隆只存在于 html/template 内部
的 text/template 命名空间，导出 API 不可见；这类入口回退到标准库执行（预转义克隆 +
按需绑定克隆），保证正确性。自带模板全部走编译执行器，由测试守卫。

### 3.2 执行状态按次绑定

译者与权限主体放进 `execState`：

- `tr` 直接调用 `Translator.T`；
- `can`/`hasRole`/`perm`/`role` 直接调用 `Authz`；
- 调用方通过 `Options.Funcs` 覆盖同名函数时，覆盖优先。

不再克隆模板集，也不再有「语言/角色组合上限 16」的缓存。

### 3.3 类型化热路径

- **单字段动作**：`.Name`、`.ID`、`.Active` 等编译为 `fieldAction`，沿
  `reflect.Value` 字段链直接取值；字符串/整数/布尔不装箱，按类型分别走
  `appendHTMLText` / `strconv.Append*`。
- **字段路径缓存**：每个 `fieldAction` 以 `atomic.Pointer` 缓存
  「根类型 → 字段索引链」，100 行循环只解析一次类型，不再每行哈希 `reflect.Type`。
  `structPath` 同时记录末端字段的输出形态（普通字符串、受信字符串、整数、布尔），
  非字符串字段直接追加，不再每次进入通用反射分类。
- **映射直读**：`map[string]any` 数据（门面默认形态）在 `fieldAction` 内直接按键
  读取，完全绕开 `reflect.MapIndex`，因此映射行不再产生任何装箱。
- **变量引用**：`$x := .Field` 与 `range $v := ...` 以 `reflect.Value` 保存，
  仅在通用函数确实需要接口时才物化；字符串变量在转义输出时零分配。
- **单绑定动作**：`{{ tr "key" }}`、`{{ perm ... }}`、`{{ can ... }}` 有专门的
  无接口输出路径。
- **条件**：`{{ if can "perm" }}`、`{{ if .Active }}` 直接取布尔结果，不装箱。
- **链式转义**：URL → 属性等多级转义在编译期折叠为单条组合指令；
  规范化、百分号编码与属性转义在**同一遍扫描**中完成，不再产生中间片段，
  也不再需要第二次属性扫描。规范化后仍可能出现的 HTML 特殊字节只有 `&` 与 `+`
  （其余特殊字节都会被百分号编码），因此可以边编码边替换。
- **字节级扫描**：HTML 转义按模式预生成 256 项替换码表，每字节一次加载与一次分支；
  非 ASCII 字节在文本模式下直接跳过（无需解码），无引号属性才解码并处理非字符。
  URL 编码用字节分类表一次判定保留/编码/`&`/`+`，干净字符串只扫描一次即直接追加。
- **安全类型**：`template.JS`/`URL`/`Srcset` 等直接走类型化转义，不再构造中间接口。
- **输出缓冲**：引擎以 `*Buffer` 句柄池化输出缓冲，避免把切片头放入 `sync.Pool`
  时产生的装箱；门面片段与布局追加进同一缓冲。

### 3.4 并发与缓存

- 编译程序不可变，`atomic.Pointer[engineState]` 发布；
- 执行状态、参数暂存栈、字节暂存与输出缓冲均来自 `sync.Pool`；
- 渲染期间的共享只读数据：节点图、转义表、字段路径、字符串常量；
- Dev 模式仍然每次重载，行为与旧实现一致。

### 3.5 第二轮热路径压缩

分配降到零后，剖析显示剩余时间集中在逐字节扫描与反射分类。本轮按热点做了以下改动：

1. **URL 链单遍化**：过滤、规范化、百分号编码与属性转义合并为一遍扫描；规范化后
   只剩 `&` 与 `+` 可能仍需 HTML 替换，其余特殊字节都会变成 `%xx`。
2. **HTML 码表**：按模式预生成 `[256]uint8` 替换码，热循环每字节一次查表与一次分支；
   文本模式不再解码非 ASCII，无引号属性单独走解码路径以保留非字符处理。
3. **URL 字节分类表**：保留、编码、`&`、`+`、`%` 五种状态一次判定，去掉逐字节
   `switch` 与重复的 keep 表查询。
4. **字段输出形态**：`structPath` 在类型解析时记录普通字符串/受信字符串/整数/布尔，
   之后直接追加；字符串字段跳过 `appendEscapedReflect` 的通用分类。
5. **跳过结构体行的映射快路径**：range-over-struct 直接持有 `reflect.Value`，
   不再为每个字段调用 `anyValue`。
6. **扁平列表步骤**：静态文本直接存入列表项（`step{text, node}`），不再为每段文本
   分配节点并走一次接口分派；非文本步骤仍保留节点接口。
7. **URL 链内联**：折叠后的 URL 编码循环直接写进字段输出函数，短查询不再多一层调用。
8. **输出持有者随状态复用**：`execState` 自带 `ownOut`，每次渲染少一次 `sync.Pool` 往返；
   嵌套模板仍共享父级输出。

上述改动没有引入 `unsafe`、运行期代码生成或可变结果缓存；执行器仍然是不可变节点图。
同一热机状态下交错 A/B：低风险批次把 100 行页面从 38.1 µs 降到 36.1 µs（**−5.2%**），
其中 URL 内联与池合并约 −3.5%，扁平步骤约 −1.9%。另外尝试过把所有节点收进带 `kind`
的具体步骤联合体，实测反而变慢（步骤结构体变大、分支与缓存压力上升），已回退，
保留「文本扁平、其余接口」的折中。
同时修复了无引号属性在非字符、非法 UTF-8 与空值上的三处标准库偏差，并加入差分用例。

## 4. 结果

### 4.1 微基准

| 场景 | 旧 | 新 | 提升 |
|---|---:|---:|---:|
| 无绑定 | 7.57 µs / 71 allocs | **0.54 µs / 4** | **14.1×** |
| 译者绑定 | 7.73 µs / 76 allocs | **0.54 µs / 4** | **14.4×** |
| 译者 + 权限 | 8.34 µs / 76 allocs | **0.61 µs / 4** | **13.7×** |

### 4.2 框架真实页面

| 页面 | html/template | 新引擎 | 提升 |
|---|---:|---:|---:|
| dashboard | 82.8 µs / 769 allocs | **5.1 µs / 2** | **16.2×** |
| users | 83.5 µs / 763 allocs | **6.2 µs / 7** | **13.5×** |
| permissions（39 KB，缓冲复用） | 65.1 µs / 612 allocs | **3.9 µs / 0** | **16.8×** |

### 4.3 横向对比

100 行列表页、单核中位数（完整表格见
[`benchmarks/template/README.md`](../benchmarks/template/README.md)）：

| 引擎 | ns/op | allocs/op | 上下文感知转义 |
|---|---:|---:|---:|
| quicktemplate | 27,036 | 0 | ❌ |
| **yd-render** | **41,748** | **0** | ✅ |
| templ | 140,378 | 1,823 | ✅ |
| Jet | 165,985 | 6 | ❌ |
| text/template | 247,408 | 811 | ❌ |
| Mustache | 375,931 | 5,633 | ❌ |
| Pongo2 | 377,589 | 3,759 | ❌ |
| Liquid | 453,718 | 6,753 | ❌ |
| html/template | 1,055,921 | 7,142 | ✅ |
| Raymond | 1,548,407 | 10,551 | ❌ |

- 比 html/template 快 **25.3×**，比 text/template 快 **5.9×**；
- 比 Jet 快 **4.0×**，比 templ 快 **3.4×**；
- 只有 html/template、yd-render、templ 同时具备文本/属性转义与 `javascript:` URL 拦截。

### 4.4 输出物化后的真实差距

`BenchmarkEngines` 把结果写到 `io.Discard`。该口径隔离了 CPU，但**结构上偏向流式引擎**：
quicktemplate/templ 可以把片段直接交给 `io.Discard`，不物化任何输出；yd-render 为保证
「要么整页、要么无输出」必须先构建完整页面。把同一页面渲染进可复用的 `bytes.Buffer`
后（`BenchmarkEnginesMaterialized`）：

| 引擎 | 物化 page100（ns/op） | 相对 yd-render |
|---|---:|---:|
| quicktemplate | 38,961 | 0.96× |
| **yd-render** | **40,729** | **1.00×** |
| templ | 141,924 | 3.49× |
| Jet | 174,533 | 4.29× |
| text/template | 244,277 | 6.00× |
| html/template | 1,089,349 | 26.75× |

结论：**物化输出后 yd-render 与 quicktemplate 基本持平（1.04×）**。`io.Discard` 下
1.54× 的差距主要来自输出物化方式，而非执行模型；剩余约 4% 是完整上下文转义、
URL 协议过滤与规范化带来的必要成本。

## 5. 兼容性与安全

- `http/render/compat_test.go` 用同一模板、同一数据分别交给 html/template 与编译引擎，
  **逐字节比较输出**；覆盖 HTML/属性/URL/JS/CSS/注释上下文、`define`/`template`/`block`、
  条件、循环、变量、内置函数、方法、nil/缺失键与错误路径。
- 转义函数与 html/template 的替换表/过滤器逐项对齐；`template.HTML`、`template.URL`
  等安全类型仍可显式绕过对应转义。
- 无引号属性中的非字符（U+FDD0–FDEF、U+FFF0–FFFF）、非法 UTF-8 与空值现在与
  html/template 一致（空值输出 `ZgotmplZ`）；三类输入都有差分用例固化。
- 上下文转义由 html/template 在加载期完成，编译执行器不会重新解释安全决策。
- 派生上下文入口回退标准库，不会因为「无法访问派生树」而降低安全性。

## 6. 复现

```bash
# 微基准与差分测试
go test ./http/render -run '^$' -bench BenchmarkRender -benchmem -count 3
go test ./http/render -run 'TestDifferential' -count 1

# 框架页面
go test ./http/render -run '^$' -bench BenchmarkFrameworkPage -benchmem -count 3

# 横向对比
cd benchmarks/template && go test ./...
go test -run '^$' -bench BenchmarkEngines -benchmem -benchtime=1s -count=5
GOMAXPROCS=1 go test -run '^$' -bench BenchmarkEngines -benchmem -benchtime=1s -count=5
```
