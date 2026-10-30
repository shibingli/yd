# WAF 使用手册

`yd new` 默认随项目生成 `internal/waf` 并启用 WAF。引擎是
[`github.com/shibingli/ydx/middleware/waf`](https://github.com/shibingli/ydx/tree/main/middleware/waf)
（OWASP Coraza v3），默认规则围绕 **Go 服务端、HTML、SQL 与 JavaScript** 裁剪。

本文是生成项目里 WAF 的唯一使用入口：配置、规则、热启停、排除清单、故障排查与许可。
总开关是 `waf.enabled`（缺键按 `true` 处理），模式是 `waf.mode`；两者都可以在运行时热切换。

## 1. 能力与边界

**能做**

- 检测请求行、请求头、请求正文、响应头与响应正文（响应正文默认关闭）。
- 拦截、仅检测、关闭三种模式；配置文件变更热生效，无需重启。
- 规则热重载：新规则编译失败时保留旧引擎，不把站点打成 503。
- 每请求日志、命中统计与优雅关停。

**不能做**

- 不是限流器、认证系统、TLS 终结器或反向代理。
- 不能替代参数化 SQL、输出编码、最小权限等基础安全工程。
- 不能保证绝对零误报；规则调优是上线工作的核心部分。

## 2. 配置

`configs/config.yaml`：

```yaml
waf:
  enabled: true                 # 运行时总开关；false 直接旁路
  mode: block                   # block | detection | off
  paranoia_level: 1             # CRS PL1-4
  anomaly_threshold: 5          # 入站异常分阈值
  inspect_json: true            # 解析 application/json 正文
  request_body_limit: 8388608           # 8 MiB
  request_body_in_memory_limit: 131072  # 128 KiB
  exclude_paths: [...]          # 完全旁路的路径
  exclude_args: [...]           # 免于攻击规则的字段
  extra_rule_files: []          # 追加嵌入式 CRS 文件
  directives: []                # 追加内联 SecLang，最后加载
  trusted_proxies: []           # 反向代理 CIDR 或单个 IP
```

| 键 | 默认 | 说明 |
|---|---|---|
| `enabled` | `true` | 缺键也按开启处理；显式 `false` 才旁路 |
| `mode` | `block` | `detection` 只记录；`off` 建引擎但不求值 |
| `paranoia_level` | `1` | 1-4；越高越严格，误报也越多 |
| `anomaly_threshold` | `5` | 达到即拦截 |
| `inspect_json` | `true` | 关闭后 JSON 正文不进入 `ARGS` |
| `request_body_limit` | `8 MiB` | 超限返回 413 |
| `request_body_in_memory_limit` | `128 KiB` | 超出写临时文件 |
| `exclude_paths` | 见下 | 精确匹配或以 `/` 为边界的前缀匹配 |
| `exclude_args` | 见下 | 表单字段名或 `json.*` 前缀 |
| `extra_rule_files` | 空 | 只接受 `@owasp_crs/...` 嵌入式路径 |
| `directives` | 空 | 内联 SecLang，最后加载 |
| `trusted_proxies` | 空 | 只有对端可信才读取 XFF |

环境变量由既有 `YD_` 前缀自动映射：`YD_WAF_ENABLED`、`YD_WAF_MODE`、
`YD_WAF_PARANOIA_LEVEL`、`YD_WAF_ANOMALY_THRESHOLD`、`YD_WAF_INSPECT_JSON`、
`YD_WAF_REQUEST_BODY_LIMIT`、`YD_WAF_REQUEST_BODY_IN_MEMORY_LIMIT`、
`YD_WAF_EXCLUDE_PATHS`、`YD_WAF_EXCLUDE_ARGS`、`YD_WAF_EXTRA_RULE_FILES`、
`YD_WAF_DIRECTIVES`、`YD_WAF_TRUSTED_PROXIES`。

**校验是启动期强制的**：即使 `enabled: false`，非法 mode、PL、阈值、上限、CIDR、
字段名或磁盘规则路径都会让应用拒绝启动，避免错误配置潜伏到下次启用。

## 3. 默认规则（Go / HTML / SQL / JavaScript）

加载顺序：

```text
@coraza.conf-recommended
@crs-setup.conf.example
yd-tuning.conf
REQUEST-901-INITIALIZATION.conf
REQUEST-905-COMMON-EXCEPTIONS.conf
REQUEST-911-METHOD-ENFORCEMENT.conf
REQUEST-920-PROTOCOL-ENFORCEMENT.conf
REQUEST-921-PROTOCOL-ATTACK.conf
REQUEST-930-APPLICATION-ATTACK-LFI.conf
REQUEST-931-APPLICATION-ATTACK-RFI.conf
REQUEST-932-APPLICATION-ATTACK-RCE.conf
REQUEST-934-APPLICATION-ATTACK-GENERIC.conf
REQUEST-941-APPLICATION-ATTACK-XSS.conf
REQUEST-942-APPLICATION-ATTACK-SQLI.conf
REQUEST-949-BLOCKING-EVALUATION.conf
```

- **Go 服务端**：协议/走私、方法、LFI/RFI/RCE。
- **HTML / JavaScript**：XSS、通用 JS/原型污染、SSRF。
- **SQL**：SQLi。
- **排除**：PHP（933）、Java（944）、会话固定（943）与响应侧 950-980；934 中的
  Ruby/PHP/Perl 子规则（934140/934150/934170）也被移除。
- **方法白名单**：`yd-tuning.conf` 把 911 的默认白名单扩展为
  `GET HEAD POST OPTIONS PUT PATCH DELETE CONNECT`，否则生成项目的 `PUT/PATCH/DELETE`
  API 会被误伤。
- **JSON**：`inspect_json: true` 时先设置 `ctl:requestBodyProcessor=JSON`，CRS 的 phase 2
  规则因此能检查 JSON 字段。

`yd-tuning.conf` 与定向排除由 `internal/waf/rules.go` 生成；规则 ID `900001-900010` 为 yd
保留段。CRS 若在未来版本占用同一 ID，引擎编译会失败，生成项目的测试会拦住。

需要扫描器识别、multipart 攻击或会话固定规则时，经 `extra_rule_files` 追加对应
`@owasp_crs/...` 文件；项目自有规则写进 `directives`。**不支持任意磁盘规则路径**：
生成项目是单二进制，磁盘规则在 scratch 镜像里没有稳定落点。

## 4. 排除清单

**路径级**（整个请求不进入引擎）：

```text
/healthz
/static
/openapi/assets
/events
/ws
/mgr/ws
/messages/attachments
/attachments
/mgr/attachments
/api/portal/v1/attachments
/api/portal/v1/messages/attachments
/api/v1/attachments
```

匹配是精确路径或以 `路径 + "/"` 为前缀，`/attachments-other` 不会被误排除。
静态资源、健康检查、OpenAPI 查看器、SSE/WebSocket 与附件上传下载因此不支付正文缓冲成本。

**字段级**（`exclude_args`）：

```text
body_markdown
json.body_markdown
json.design
json.document
json.variables
json.form_data
json.payload
```

表单字段生成 `!ARGS:<name>`；`json.*` 生成 `!ARGS:/^<escaped>($|\.)/`，同时覆盖精确键与
嵌套键。这些字段由服务端 Markdown 渲染/净化或原样存储，不会作为 SQL 或 HTML 执行；
排除它们是为了避免代码示例触发 XSS/SQLi 误报。**服务端输出编码仍是安全边界。**

## 5. 启停与热重载

WAF 独占 `config.Watch` 的**单回调槽**。应用代码不得再调用 `cfg.Watch`，否则会顶掉
WAF 的监视器、让热切换静默失效。

| 配置变更 | 行为 |
|---|---|
| `enabled: false` | 原子旁路；旧引擎异步排空 |
| `enabled: true` 且当前旁路 | 重新编译引擎；失败则保持旁路并 Error 日志 |
| `enabled: true` 且当前启用 | 复用实例 `ReloadWith` 原子替换规则/模式/排除 |
| 仅无关配置变化 | 设置签名相同，跳过重编译 |
| 新配置非法 | 保留当前引擎，Error 日志；不改动现有防护 |

- **启动期** `enabled: true` 且编译失败 → 应用拒绝启动。
- **运行期**重载失败 → 保留旧引擎，绝不清空防护。
- 监视器 goroutine 与进程同寿；`Manager.Shutdown` 只置 `closed`，让迟到回调空转。
- fsnotify 不可用时只告警，WAF 按启动值工作；运行时切换退化为重启生效。

## 6. 观测与日志

- 拦截/检测/内部错误经 `ydx/log` 输出：`tx_id`、`method`、`path`、`client_ip`、
  `status`、`phase`、`rules`、`duration`、`rule_id`、`first_match_rule_id`。
- 默认不写查询串、正文、规则 Message/Data，避免把攻击载荷写进日志。
- 管理概览的中间件运行时卡片有一行 `waf`，统计请求、在途、错误与延迟。
- 需要更细的命中信息时，用 `waf.directives` 打开 Coraza 审计，或二次开发时设置
  `Options.Observer`；事件应送入队列，不在请求 goroutine 做阻塞 I/O。

## 7. 部署注意事项

- **可信代理**：应用在反向代理之后时必须配置 `trusted_proxies`，否则所有客户端会按代理
  地址计算。不可信来源的 `X-Forwarded-For` 一律忽略。
- **多副本**：每个副本独立编译规则；配置与规则文件必须一致，否则各副本判定可能不同。
  WAF 不是集群级状态组件。
- **上传/流式接口**：已在默认路径排除；新增上传端点时把路径加入 `exclude_paths`。
- **性能**：默认子集约 382 条规则（含 tune 与 JSON processor），正文解析是 POST 的主要
  成本。只在高风险路由挂载、或对静态/健康检查排除，是成本最低的做法。
- **零误报**：默认 PL1 是 CRS 推荐值；上线前用 `mode: detection` 观察 `Stats.Detected`，
  再切 `block`。PL1 刻意不拦截 `cat` 这类高频命令，安全要求更高时提升 PL 或追加规则。

## 8. 故障排查

| 现象 | 优先检查 | 处理 |
|---|---|---|
| 正常请求被 403 | `first_match_rule_id`、请求字段 | 用 `exclude_args` 排除字段，或提升阈值前先观察 |
| 规则不生效 | `mode`、JSON processor、路径是否被排除 | 切 `block`、开 `inspect_json`、检查 `exclude_paths` |
| 改配置不生效 | 写的是否为 `configs/config.yaml`/`local` | 只监听见这两个文件；确认没有其它 `cfg.Watch` |
| 上传失败 413 | 上传路径是否被排除 | 加入 `exclude_paths` |
| SSE/WebSocket 卡住 | 是否在排除清单 | 加入 `exclude_paths` |
| 重载失败 | 日志中的 `waf reload failed` | 修正规则；旧引擎仍在服务 |
| 启动失败 | `ErrConfig`/`ErrEngine` 日志 | 按提示修正配置；非法配置不会带病启动 |

## 9. 版本与许可

- `github.com/shibingli/ydx/middleware/waf v0.1.0`
- `github.com/corazawaf/coraza/v3 v3.8.1`（Apache-2.0）
- `github.com/corazawaf/coraza-coreruleset v0.0.0-20240226094324-415b1017abdc`（Apache-2.0）

版本由 `tooling/versions/waf/go.mod` 锁定（yd 仓库内），生成项目从脚手架常量取值。
升级 CRS/Coraza 后必须重跑生成项目的 WAF 测试；规则数或行为变化是重新调优信号，
不是噪音。
