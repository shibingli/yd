# 验证码（captcha）使用说明

`yd` 生成的项目内置 [`ydx/captcha`](https://github.com/shibingli/ydx/tree/main/captcha) 的人机验证能力，
由配置决定是否在**登录**与**修改密码**时要求验证。接线始终随项目生成，未启用时不注册任何验证码路由、
不加载浏览器 SDK，登录与改密链路与从未集成的项目完全一致。

## 1. 快速开始

```yaml
# configs/config.yaml
captcha:
  login: true             # 登录要求验证码
  password_change: true   # 修改口令要求验证码
  secret: ""              # 生产通过 YD_CAPTCHA_SECRET 注入
  store: auto             # auto | memory | redis
  fallback: memory        # memory | fail
  types: [text, rotate]   # 默认键盘可完成；空 = 全部内置（含仅指针题型）
  key_prefix: "yd:captcha:"
  trusted_proxies: []     # 可信反向代理 CIDR；为空只信任直接对端
```

开发环境：

```bash
# 内置文档化开发密钥，启动时告警；不要用于任何真实环境
yd dev
```

生产环境：

```bash
YD_APP_ENV=production
YD_CAPTCHA_SECRET='至少 16 字节的随机密钥'
YD_CAPTCHA_LOGIN=true
YD_CAPTCHA_PASSWORD_CHANGE=true
YD_CAPTCHA_STORE=redis
YD_CAPTCHA_FALLBACK=fail
```

生产环境启用验证码但 `captcha.secret` 为空时**拒绝启动**，错误指向 `YD_CAPTCHA_SECRET`。
密钥只用于一次性令牌签名，不写入日志。多副本部署必须为所有实例注入**同一个** `YD_CAPTCHA_SECRET`，
否则一个实例签发的令牌在另一个实例上会验签失败。

## 2. 开关与优先级

| 键 | 环境变量 | 默认 | 说明 |
|---|---|---|---|
| `captcha.login` | `YD_CAPTCHA_LOGIN` | `false` | 登录时要求验证码 |
| `captcha.password_change` | `YD_CAPTCHA_PASSWORD_CHANGE` | `false` | 修改口令时要求验证码 |
| `captcha.secret` | `YD_CAPTCHA_SECRET` | `""` | HMAC 签名密钥，至少 16 字节 |
| `captcha.store` | `YD_CAPTCHA_STORE` | `auto` | `auto` / `memory` / `redis` |
| `captcha.fallback` | `YD_CAPTCHA_FALLBACK` | `memory` | Redis 不可用时降级还是拒绝启动 |
| `captcha.types` | `YD_CAPTCHA_TYPES` | `[text, rotate]` | 逗号分隔的视觉题型；默认只含键盘可完成项，空为全部（含仅指针题型） |
| `captcha.key_prefix` | `YD_CAPTCHA_KEY_PREFIX` | `yd:captcha:` | Redis 键命名空间 |
| `captcha.trusted_proxies` | `YD_CAPTCHA_TRUSTED_PROXIES` | `[]` | 可信反向代理 CIDR 列表（也可写单个 IP） |

配置优先级仍是：命令行参数 > 环境变量 > 配置文件 > 框架默认值。

`yd new --captcha`（交互模式也会询问）只把生成配置里的两个开关设为 `true`；之后可以随时在配置里开关，
不需要重新生成项目。两个开关在进程启动时读取：生产环境改配置后重启生效，`yd dev` 会自动热重载。

## 3. 单机与多机

| 状态 | 单机默认 | 多机组件 |
|---|---|---|
| 挑战记录、一次性令牌标记、冷却 | 进程内分片内存 | `redisstore.NewStore` |
| 题目图片 | 进程内内存 | `redisstore.NewImageStore` |
| 限流桶 | 进程内令牌桶 | `redisstore.NewRateLimitStore` |

- `store: memory`：只适合单实例。生产环境启动时打印「验证码状态保存在内存中；多副本不会共享挑战」。
- `store: auto`（默认）：优先连接 Redis；成功则三种状态全部共享，失败按 `fallback` 处理。
- `store: redis`：与 `auto` 一样尝试 Redis，失败按 `fallback` 处理；配合 `fallback: fail` 即严格多机模式。
- `fallback: memory`：Redis 不可用时降级为单机内存并打印双语告警。降级只发生在**启动期**。
- `fallback: fail`：Redis 不可用时拒绝启动，避免多副本静默退化成「每个实例各自出题」。

运行期 Redis 故障不会热切换到内存：验证与限流都失败关闭，返回统一的验证失败文案。
生成项目由 `internal/service.NewRedis` 打开**唯一**的共享客户端并传给 `captcha.NewWithClient`，缓存与消息总线
也复用同一个客户端，`App` 的 `io.Closer` 钩子在关停时关闭一次；只有单独使用 `security/captcha.New` 时，
才由包自己打开并在 `Service.Close` 中释放。

`captcha.key_prefix` 在同一部署内必须唯一；多个应用共享一个 Redis 时必须分别配置，
否则会互相消耗令牌、覆盖挑战。

`captcha.trusted_proxies` 只在应用确实位于反向代理之后时配置。为空时验证码只使用直接对端地址；
配置后按 `router.Ctx.ClientIP` 的同一模型，从 `X-Forwarded-For` 最右侧可信链取第一个不可信地址。
不要把不可信的网段写进来，否则客户端可以伪造 `X-Forwarded-For` 绕过按 IP 限流。

## 4. 验证流程

登录（`POST /login`）：

1. 浏览器在用户提交表单时先打开验证码对话框；只有 SDK 产出令牌后才继续提交；
2. 服务端先校验 `captcha_token` 隐藏字段或 `X-Captcha-Token` 请求头中的一次性令牌；
3. 失败返回 **422** 并重新渲染登录页（带新的验证码对话框），不解析租户、不校验口令；
4. 成功后继续原有的租户解析、SM2 解密与口令校验。

改密（`POST /account/password`）：

1. 浏览器在提交时先打开验证码对话框，验证成功后再走原有的 `data-yd-confirm` 确认与 htmx 提交；
2. 服务端先校验已登录会话与租户；
3. 再校验验证码；失败返回 **422** 错误片段，表单保持打开，前端重新出题；
4. 成功后继续原有的旧口令校验、新口令写入与 `sec_key` 轮换。

浏览器端的重新提交使用 `form.requestSubmit(originalSubmitter)`，因此多按钮表单的 `name`/`value` 不会在
验证码往返中丢失；确认框、htmx 与 SM2 加密仍按原来的顺序执行，验证码只插入在它们之前。

一次性令牌只能消费一次；任何失败（缺少、过期、已用、签名不符）都回同一句
`captcha.failed` 文案，不泄露账号、租户或令牌细节。

与会话令牌区分：`Authorization: Bearer` **不会**被当作验证码令牌（改密端点上的它承载会话），
API 客户端请使用 `X-Captcha-Token` 请求头；浏览器表单使用 POST 正文里的隐藏字段 `captcha_token`。
URL 查询串**不是**令牌来源：查询串会进入访问日志、浏览器历史与 `Referer`，放在那里的令牌会留下
比一次性语义更多的副本。

动作名只有 `login` 与 `password_change`。验证码启用时，向 `Verify` 传入其它动作会以
`ErrUnknownAction` 失败关闭（而非静默跳过校验）；两个动作都关闭时则保持空操作，未启用验证码的项目不受影响。

## 5. API 与前端资产

验证码端点固定挂载在 `/captcha`（仅在验证码启用时存在）：

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/captcha/challenge` | 创建视觉挑战 |
| POST | `/captcha/verify` | 提交答案换取一次性令牌 |
| POST | `/captcha/send` | 短信/邮件验证（本集成未启用） |
| POST | `/captcha/token/consume` | 一次性消费令牌 |
| GET | `/captcha/image/{key}` | 短期挑战图片 |
| GET | `/captcha/assets/yd-captcha.js` | 浏览器 SDK |
| GET | `/captcha/assets/yd-captcha.css` | 浏览器 SDK 样式 |

这些端点不经过鉴权、租户与缓存中间件（全局的恢复、请求 ID、访问日志、压缩与安全头仍然生效），
也不计入 OpenAPI 缺失注解统计。

## 6. 页面、i18n 与主题

- 登录页与账号页共用 `web/templates/partials/captcha.html`；
- 启用验证码时，片段渲染一个默认关闭的原生 `<dialog>`，**不在表单里占用任何常驻高度**；
  两个开关都关闭时不渲染对话框、不加载 SDK、不注册 `/captcha` 路由，页面与从未启用验证码的项目一致；
- `web/static/js/yd-captcha.js` 在发现 `[data-yd-captcha]` 时按需加载同源 SDK：
  - 组件在对话框**首次打开时才挂载并请求挑战**，避免用户在页面上停留超过挑战 TTL 后打开一个已经过期的题；
  - 用户提交受保护表单时先校验普通必填字段，再调用 `showModal()` 打开对话框；未完成验证前不会发出登录/改密请求；
  - SDK 验证成功后把令牌写入 `captcha_token` 隐藏字段、关闭对话框，并以
    `form.requestSubmit(originalSubmitter)` 继续原来的动作；确认框、htmx 与 SM2 加密的顺序不变；
  - 取消按钮、`Esc` 与点击遮罩只关闭对话框，不提交；组件与已输入的答案保留，再次提交时继续使用同一道题；
  - 对话框内的回车触发 SDK 的验证按钮，不会直接提交外层表单；
  - 请求返回 4xx/5xx 后**等 htmx 完成交换**再销毁并重新挂载组件（一次性令牌已被消费），避免在即将被替换的
    节点上白开一道题；
  - SDK 加载失败以「验证码暂时不可用」失败关闭，绝不跳过验证直接提交；
  - `MutationObserver` 跟随 `<html data-theme>`，主题切换即时生效；
  - 语言切换替换外壳时，挂载点与隐藏令牌带 `hx-preserve`，已完成的令牌与已输入的答案保留；标题、提示与
    错误位于挂载点之外，按新语言重新渲染。没有输入内容的未完成组件会在新语言下重新出题；已有输入的组件
    保留答案，组件自身按钮文案在下次出题时更新；用户名输入同样带 `hx-preserve`，口令字段刻意不保留；
  - 前进/后退恢复历史快照时，关闭对话框、清除快照克隆的组件与旧令牌并重新出题，避免出现两个组件或让过期
    令牌通过提交守卫；
- 周边标签与错误文案来自 `web/i18n/{zh-CN,en}.yaml` 的 `field.captcha` 与 `captcha.*` 键，其中
  `captcha.noscript` 只在 JavaScript 被禁用时显示；服务端仍会在缺少令牌时以 422 失败关闭；
- 组件自身的按钮、提示与错误来自 ydx 内置八语言目录，`locale` 跟随 `<html lang>`；
- 组件默认语言取 `i18n.default_lang`。若应用新增了验证码目录不支持的语言（例如 `ru`），启动时回退到
  `zh-CN` 并打印一条双语告警，而不是让整个应用拒绝启动。

新增页面若需要验证码，只需在表单内调用 `partials/captcha`，并传入
`Enabled` / `Action` / `Endpoint` / `Field` / `InputID` 五个字段；不要另写挂载脚本或第二套文案。
动作名仍只有 `login` 与 `password_change`；其它动作需要先在服务端注册并通过 `security/captcha.Verify`
验证，前端不会凭空放行。

## 7. 故障排查

| 现象 | 原因 | 处理 |
|---|---|---|
| 启动失败，错误指向 `YD_CAPTCHA_SECRET` | 非 development 环境启用了验证码但未配置密钥 | 注入至少 16 字节的 `YD_CAPTCHA_SECRET` |
| 启动失败，错误指向共享存储 | `fallback: fail` 且 Redis 不可达 | 修复 Redis，或改为 `fallback: memory` 接受单机降级 |
| 页面没有验证码组件 | 对应开关为 false，或模板未传入 `Enabled` | 检查 `captcha.login` / `captcha.password_change` 与页面数据 |
| 点击登录/保存后没有弹出验证码 | 浏览器不支持原生 `<dialog>`，或脚本被 CSP 拦截 | 使用支持 `<dialog>` 的现代浏览器；确认 `yd-captcha.js` 已加载且 `script-src 'self'` 未阻止它 |
| 弹出验证码后取消，再次提交没有重新出题 | 这是预期行为：取消保留组件与已输入答案 | 需要重置时刷新页面；验证成功或服务端 4xx 后才强制重新出题 |
| 组件一直显示「验证未通过」 | 一次性令牌被消费后未重新出题 | 确认 `yd-captcha.js` 已加载且 `htmx:after:request` 处理器未被 CSP 拦截 |
| 多副本下挑战偶尔失效 | 使用 `store: memory`，或启动日志里的 `effective_store` 是 `memory (fallback)` | 修复 Redis 后重启；需要严格多机时设置 `captcha.fallback: fail` |
| 启动日志显示 `effective_store=memory (fallback)` | Redis 不可达，按 `captcha.fallback` 降级 | 检查 `redis.*` 与网络；严格多机部署改用 `fallback: fail` |
| 主题切换后组件仍是旧配色 | 组件根节点的 `data-theme` 未更新 | 确认页面使用 `<html data-theme>` 三态主题与 `yd-captcha.js` |

## 8. 安全边界

- 视觉题目无法阻止有动机的攻击者使用视觉模型或人工打码；本集成通过短 TTL、一次性令牌、
  行为轨迹、限流、PoW 与审计抬高成本，而不是承诺「不可破解」。
- 客户端信号与 PoW 都是提高自动化成本的输入，不是授权依据；授权永远在服务端。
- 验证码失败文案保持统一，不能作为账号或租户枚举的旁路。
- `captcha.secret` 只用于令牌签名；开发密钥按设计公开，绝不能在非 development 环境使用。
