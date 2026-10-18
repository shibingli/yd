# render 性能瓶颈分析与编译式引擎设计

本文记录 `http/render` 从「直接执行 html/template」迁移到「加载期编译、渲染期执行
不可变程序」的完整分析、实现与验证。基准数值取自 Windows / i9-10980HK / Go 1.27.2；
跨机比较请以相对关系为准。

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
  `appendHTMLReplacer` / `strconv.Append*`。
- **字段路径缓存**：每个 `fieldAction` 以 `atomic.Pointer` 缓存
  「根类型 → 字段索引链」，100 行循环只解析一次类型，不再每行哈希 `reflect.Type`。
- **单绑定动作**：`{{ tr "key" }}`、`{{ perm ... }}`、`{{ can ... }}` 有专门的
  无接口输出路径。
- **条件**：`{{ if can "perm" }}` 直接取布尔结果，不装箱。
- **链式转义**：URL → 属性等多级转义在字节缓冲上完成，中间结果不转字符串。
- **输出缓冲**：引擎按增长后的容量保留缓冲；门面片段与布局追加进同一缓冲。

### 3.4 并发与缓存

- 编译程序不可变，`atomic.Pointer[engineState]` 发布；
- 执行状态、参数暂存栈、字节暂存与输出缓冲均来自 `sync.Pool`；
- 渲染期间的共享只读数据：节点图、转义表、字段路径、字符串常量；
- Dev 模式仍然每次重载，行为与旧实现一致。

## 4. 结果

### 4.1 微基准

| 场景 | 旧 | 新 | 提升 |
|---|---:|---:|---:|
| 无绑定 | 7.57 µs / 71 allocs | **0.70 µs / 7** | **10.8×** |
| 译者绑定 | 7.73 µs / 76 allocs | **0.96 µs / 12** | **8.1×** |
| 译者 + 权限 | 8.34 µs / 76 allocs | **0.95 µs / 12** | **8.7×** |

### 4.2 框架真实页面

| 页面 | html/template | 新引擎 | 提升 |
|---|---:|---:|---:|
| dashboard | 97.4 µs / 769 allocs | **14.1 µs / 96** | **6.9×** |
| users | 96.4 µs / 763 allocs | **14.8 µs / 95** | **6.5×** |
| permissions（39 KB，缓冲复用） | 76.5 µs / 612 allocs | **10.6 µs / 69** | **7.2×** |

### 4.3 横向对比

100 行列表页、单核中位数（完整表格见
[`benchmarks/template/README.md`](../benchmarks/template/README.md)）：

| 引擎 | ns/op | allocs/op | 上下文感知转义 |
|---|---:|---:|---:|
| quicktemplate | 28,838 | 0 | ❌ |
| **yd-render** | **114,699** | **106** | ✅ |
| templ | 149,414 | 1,823 | ✅ |
| Jet | 162,731 | 6 | ❌ |
| text/template | 233,477 | 811 | ❌ |
| Pongo2 | 373,448 | 3,759 | ❌ |
| Mustache | 394,966 | 5,633 | ❌ |
| Liquid | 447,450 | 6,753 | ❌ |
| html/template | 993,471 | 7,142 | ✅ |
| Raymond | 1,692,299 | 10,551 | ❌ |

- 比 html/template 快 **8.7×**，比 text/template 快 **2.0×**；
- 比 Jet 快 **1.4×**，与 templ 基本相当（快 **1.3×**）；
- quicktemplate 仍快 **4.0×**，原因是它把模板编译成直接写 `io.Writer` 的 Go 代码，
  且不做上下文感知转义；
- 只有 html/template、yd-render、templ 同时具备文本/属性转义与 `javascript:` URL 拦截。

## 5. 兼容性与安全

- `http/render/compat_test.go` 用同一模板、同一数据分别交给 html/template 与编译引擎，
  **逐字节比较输出**；覆盖 HTML/属性/URL/JS/CSS/注释上下文、`define`/`template`/`block`、
  条件、循环、变量、内置函数、方法、nil/缺失键与错误路径。
- 转义函数与 html/template 的替换表/过滤器逐项对齐；`template.HTML`、`template.URL`
  等安全类型仍可显式绕过对应转义。
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
go test -run '^$' -bench BenchmarkEngines -benchmem -benchtime=500ms -count=3
GOMAXPROCS=1 go test -run '^$' -bench BenchmarkEngines -benchmem -benchtime=500ms -count=3
```
