# yd 实施计划：压缩中间件的可插拔编解码器与等级归一化

> 需求来源（用户原话）：
> 「压缩中件间，也要在各个方面进行性能优化及要支持自定义格式的解/压支持，
> 要完整的支持主流的所有浏览器压缩算法，还要归一化不同压缩算法的压缩等级，
> 尤其是zstd的压缩等级与其它压缩等级的归一化管理。」

## 0. 需求差异说明

只读需求基线未涵盖本节，本文件即差异说明。

## 1. 现状与问题

`middleware/compress.go` 支持 gzip / brotli / zstd 三种，`Options.Level int`（0 = 各编解码器默认），
实际做法是 `clampLevel(level, codecMin, codecMax)`。

**这不是归一化，只是截断**：

| 编解码器 | 原生等级范围 |
|---|---|
| gzip / flate | 1–9 |
| brotli | 0–11 |
| zstd（`klauspost/compress/zstd` 的 `EncoderLevel`） | **4 档枚举**：SpeedFastest=1、SpeedDefault=2、SpeedBetterCompression=3、SpeedBestCompression=4 |

因此 `Level=7` 与 `Level=9` 传给 zstd 都会被截到 4，**输出完全相同**——使用者以为调高了压缩率，
实际毫无变化。这就是需求里点名 zstd 的原因。

另有两处缺口：不支持 **`deflate`**（浏览器普遍声明，RFC 1950/1951），
也没有**自定义编解码器**的接入点。

## 2. 设计

### 2.1 语义化等级 + 逐编解码器映射表

```go
type Level int
const (
    LevelDefault  Level = 0  // 各编解码器的平衡档
    LevelFastest  Level = 1
    LevelFast
    LevelBalanced
    LevelBest
    LevelMaximum
)
```

映射表（**逐项写死并测试**，不靠 clamp）：

| 语义等级 | gzip / deflate | brotli | zstd |
|---|---|---|---|
| Fastest | 1 | 0 | `SpeedFastest` |
| Fast | 3 | 2 | `EncoderLevelFromZstd(3)` |
| Balanced | 6 | 5 | `SpeedDefault` |
| Best | 8 | 9 | `SpeedBetterCompression` |
| Maximum | 9 | 11 | `SpeedBestCompression` |

zstd 用 `EncoderLevelFromZstd` 在中间档做细调（它的 1–22 比 4 档枚举细），
使五个语义档在三种编解码器上都**确实产生不同的输出**——这正是"归一化"的可证伪定义。

### 2.2 可插拔编解码器

```go
type Codec interface {
    Name() string                                   // 内容编码名，如 "gzip"
    NewWriter(w io.Writer, l Level) (io.WriteCloser, error)
    NewReader(r io.Reader) (io.ReadCloser, error)
}
func RegisterCodec(c Codec) error                   // 重复注册 → ErrDuplicateCodec
func LookupCodec(name string) (Codec, bool)
func Codecs() []Codec                               // 稳定顺序，用于协商优先级
```

内置编解码器在 `init` 注册；协商改为遍历注册表。

### 2.3 补齐浏览器主流算法

- 新增 **`deflate`**（zlib，RFC 1950；用 `klauspost/compress/flate` + `zlib` 包装）。
- **`identity`** 必须被正确理解：当客户端只接受 `identity`（或其它编码 q=0）时，**不得压缩**。
- 浏览器侧的事实依据写进文档：gzip 全平台；br 现代浏览器全支持；
  zstd 自 Chrome 123 / Firefox 126 / Safari 18 起支持；deflate 普遍声明但历史实现有偏差
  （raw deflate 与 zlib 封装之争）——因此**默认优先级不把 deflate 放在首位**。

### 2.4 压缩比与 CPU 的取舍要可见

- 保留现有默认优先级（br → zstd → gzip），但把每种算法的**实测压缩比与 CPU** 写进文档与基准；
- 使用者可用 `Options.Priority` 覆盖顺序。

## 2.5 严格按浏览器给出的顺序与优先级协商（RFC 9110 §12.5.3）

**现状缺陷**：`ParseAcceptEncoding` 返回 `map[string]float64`——**map 丢失头部顺序**，
因此现有协商根本无法遵守「q 值相同时按出现顺序」，也不可能"严格按浏览器采集到的顺序"。
另外重复编码项会被后者覆盖，缺失/畸形 q 一律按 1.0 处理，这两点都未在文档中声明。

**正确算法**（完整实现，不取巧）：

1. **解析为有序切片** `[]Encoding{Name, Q, Index}`，保留**出现顺序**与原始下标记；
2. **有效 q 值**：某编码在头中显式出现 → 取其 q；否则若有 `*` → 取 `*` 的 q；
   否则 `identity` 默认可接受（q=1），其它编码不可接受；
3. **剔除 q=0**（显式禁用）；`*;q=0` 且 identity 未被显式列出时，identity 也视为禁用；
4. **选择规则**：在服务端支持的编解码器里，取 **q 最大**者；
   **q 相同则取头中出现下标最小者**（即浏览器自己的偏好顺序）；
   若候选是靠 `*` 匹配来的，其下标取 `*` 项的下标；
5. **五种头部形态必须分别处理**：头缺失（任意编码可接受）、头为空串（仅 identity）、
   只有 `*`、显式列出、显式与 `*` 混用；
6. **重复编码项**：以**首次出现**为准（q 与下标都取首次），并在文档中声明该策略；
7. **畸形 q**（非数字、越界、多位小数）按 1.0，且**不得**因此让整条头失效。

**这条与"服务端优先级"的关系**：服务端优先级（`Options.Priority`）**只在客户端未表达偏好时**
（头缺失或只有 `*`）作为兜底；客户端一旦显式排序，**必须服从客户端**。

### 2.6 可证伪的协商测试

- 表驱动的头部样例 → 期望选中的编码，覆盖：q 不同、q 相同靠顺序决出、`*` 兜底、
  `identity` 默认、`*;q=0`、重复项、畸形 q、空头、头缺失；
- **对抗性用例**：`gzip;q=0.5, br;q=0.9` 必须选 br（哪怕服务端默认 br 优先）；
  `br;q=0.5, gzip;q=0.9` 必须选 gzip（**服务端优先级不得覆盖客户端 q 值**）；
  `zstd, br`（q 相同）必须按出现顺序选 zstd；
- **往返**：选中哪个编码，响应就必须真用那个编码，且 `Content-Encoding` 与之一致。

## 3. 性能优化（与缓冲池轮次衔接）

- 编码器与解码器按「算法 + 等级」池化（现有 `encoderPool` 保留并扩展到新的等级体系）；
- 协商阶段不再分配（前一轮已把 `Accept-Encoding` 解析改为无分配扫描）；
- 编解码缓冲走 `core/bufpool`；
- **每项优化必须附前后对比基准**。

## 4. 测试（可证伪的验收）

1. **等级确实归一化**：对同一语料，`LevelFastest < LevelBalanced < LevelMaximum` 的
   **输出字节数严格递减**，且在**三种编解码器上分别成立**。
   这条直接否证「zstd 的 7 和 9 输出相同」的现状——修复前必然失败。
2. **可插拔**：注册一个自定义编解码器（如测试用的 RLE），协商到它并正确解压；
   重复注册返回哨兵；注册后 `Codecs()` 顺序稳定。
3. **算法覆盖**：`gzip` / `deflate` / `br` / `zstd` / `identity` 各自的协商与往返；
   `q=0` 明确禁用；全 q=0 时不压缩。
4. **往返正确性**：每种算法压缩后解压，字节级等价（含中文、随机二进制、空正文）。
5. **既有行为不回归**：小响应跳过、已编码响应跳过、SSE/升级请求不压缩等既有断言全部保留。

## 5. 已知取舍

- **不引入新依赖**：brotli / zstd / flate 均已在依赖中。
- **默认不压缩 < MinSize**：保持现状。
- **`deflate` 默认优先级最低**：历史兼容性差，只在客户端明确偏好时使用。
- **等级归一化是「语义」而非「数值」**：不承诺三种算法在同一等级下压缩率相同，
  只承诺「同一算法内，更高等级产生更小的输出」——这是唯一能写进测试的定义。
