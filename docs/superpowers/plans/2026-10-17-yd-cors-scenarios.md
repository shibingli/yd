# yd 实施计划：CORS 的场景兼容与安全加固

> 需求来源（用户原话）：
> 「CORS中间件也要进行各类场景兼容性、安全性设计和处理」

## 0. 需求差异说明

只读需求基线未涵盖本节，本文件即差异说明。

## 1. 现状与问题

现有 `middleware.CORS` 已有较好的基础：来源模式解析（协议/主机/子域通配）、
开启凭证时**不回 `*` 而回显具体来源**、回显时补 `Vary: Origin`、预检以 204 短路、
来源未匹配时不写任何 CORS 头。

逐行审查后发现 **1 个安全陷阱 + 4 个兼容性/正确性缺陷**：

| # | 问题 | 影响 |
|---|---|---|
| **S1** | `AllowOrigins: ["*"]` + `AllowCredentials: true` **被静默允许**，实现会回显**任意**来源 | **任何站点都能发起带凭证的跨源请求**——这是最严重的配置陷阱，当前只会"正确地"照做 |
| **C1** | 子域/精确匹配用 `u.Host`（**含端口**）与模式比较 | `https://example.com:8443` 匹配不上模式 `example.com`；开发环境 `http://localhost:3000` 同理 |
| **C2** | `Vary: Origin` 只在"允许且回显"时补 | 来源**不被允许**时响应不带 `Vary`，缓存可能把这份无 CORS 头的响应复用给允许的来源（**缓存投毒**） |
| **C3** | 预检在未配置 `AllowMethods` / `AllowHeaders` 时**回显客户端请求的任意值** | 等于开放任意方法与任意请求头，与"最小授权"相悖 |
| **C4** | 不处理 `Access-Control-Request-Private-Network`（PNA） | Chrome 的部分私有网络访问场景会失败 |

另缺两项兼容性处理：`Origin: null`（沙箱 iframe / `file://`）当前被拒（合理，但应可显式允许）、
以及 IPv6 来源（`http://[::1]:8080`）与尾点 FQDN 的归一化。

## 2. 设计

### 2.1 安全（最高优先级）

- **S1 必须是构造期错误**：`"*"` 与 `AllowCredentials` 同时出现时，`CORS()` 无法在中间件签名里返回错误，
  因此改为**当次请求直接拒绝并记 Error 日志**（fail closed），同时在包级文档与启动告警中说明。
  需要"动态回显来源"的场景，必须使用新的 **`AllowOriginFunc func(origin string) bool`** 显式声明，
  让"我确实要回显任意来源"成为一个**写下来的决定**，而不是配置里一个星号。
- **来源归一化**：拒绝畸形 Origin、拒绝含 userinfo 的 Origin；`null` 仅在显式允许时通过。
- **预检不放行未声明的方法/头**（见 C3）。

### 2.2 兼容性

- **端口处理**：模式匹配**只看主机**，端口单独处理——
  显式在模式里写了端口就要求端口一致，没写端口则**允许任意端口**。
  同时支持 IPv6 字面量（`[::1]`）与尾点 FQDN 的归一化。
- **`Vary` 正确性**：只要请求带了 `Origin`，响应就补 `Vary: Origin`（无论是否放行）；
  预检响应额外补 `Vary: Access-Control-Request-Method` 与 `Vary: Access-Control-Request-Headers`。
- **PNA**：配置 `AllowPrivateNetwork bool` 时，对带 `Access-Control-Request-Private-Network: true`
  的预检回 `Access-Control-Allow-Private-Network: true`；未配置则不回。
- **预检短路的位置**：文档明确「CORS 必须排在认证/限流之前」，否则预检会被鉴权拦截，
  浏览器侧表现为「CORS 错误」而非「未授权」——这是最常见的误配之一。

### 2.3 预检的严格化（C3）

- 配置了 `AllowMethods` / `AllowHeaders` 时，**只回显配置值**（现状正确，保留）；
- 未配置时：**不再回显客户端任意值**。`AllowMethods` 缺省回落到
  `GET, HEAD, POST`（CORS 的"简单方法"集合）；`AllowHeaders` 缺省回落到空（不声明任何非简单头）。
  需要放开时显式配置——把"开放"变成一个需要写下来的决定。

### 2.4 配置校验

`CORSOptions` 增加 `Validate() error`（或在 `CORS()` 内做一次构造期检查并缓存结果），
返回包级 `errx` 哨兵：`ErrCORSWildcardWithCredentials`、`ErrCORSBadOrigin`、`ErrCORSEmptyOrigins`。

## 3. 测试（场景清单）

| 场景 | 期望 |
|---|---|
| 精确来源（含/不含端口、大/小写） | 允许并回显 |
| 子域通配 `*.example.com` | `a.example.com` 允许；`evil-example.com`、`example.com.evil.io` **拒绝** |
| `*` + 凭证 | **拒绝**（fail closed）并记 Error |
| `AllowOriginFunc` + 凭证 | 回显具体来源 |
| `Origin: null` | 默认拒绝；显式允许时通过 |
| 畸形 Origin / 含 userinfo | 拒绝 |
| IPv6 `[::1]:8080` | 按配置正确匹配 |
| 未匹配来源 | 不写 CORS 头，但**仍带 `Vary: Origin`** |
| 预检（已配置方法/头） | 只回显配置值，204 |
| 预检（未配置） | 方法回落 `GET, HEAD, POST`，头回落空 |
| PNA 预检 | 配置开启时回 `Allow-Private-Network` |
| 非预检的实际请求 | 带 `Expose-Headers`，不短路 |
| 缓存正确性 | 任意含 Origin 的响应都有 `Vary: Origin` |

## 4. 验收

1. 上表全部有测试；其中 `*`+凭证、子域绕过（`evil-example.com` 一类）为**对抗性用例**；
2. 既有的三条硬规则（凭证不回星号、预检 204、未匹配不写头）行为不变；
3. `-race` 全绿、`gofmt`/`vet` 零输出；
4. 预检路径零额外分配（与现状持平）。

## 5. 已知取舍

- **不做「自动放宽」**：宁可配置报错让人显式写清楚，也不猜使用者意图去放宽。
- **不实现 `Access-Control-Allow-Headers: *` 的通配放行**：浏览器对该组合的支持仍不完整，
  显式列举更可靠。
- **CORS 不是认证**：文档强调它只控制浏览器侧的跨源读取权限，
  **不能替代**服务端鉴权与 CSRF 防护。
