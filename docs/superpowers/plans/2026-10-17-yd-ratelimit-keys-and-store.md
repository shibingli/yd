# yd 实施计划：限流的多维度键与单机/多机后端

> 需求来源（用户原话）：
> 「限流中间件是支持单机/多机、路径、query参数、自定义header头、远程IP、
> base auth 用户名及主流的指纹等方式进行限流。」

## 0. 需求差异说明

只读需求基线未涵盖本节，本文件即差异说明。

## 1. 现状

`middleware.RateLimit` 已有扩展点 `RateLimitOptions.KeyFunc`（默认 `c.RealIP()`），
实现是**分片锁 + 内存令牌桶**（FNV-1a 哈希到分片，每片一把锁，定期清扫过期桶）。

缺口三处：

1. **只有内存实现**——多副本部署时每个实例各限各的，实际速率是配置值 × 副本数；
2. **没有内置的键提取器**——路径、query、header、Basic Auth 用户名、指纹都要使用者自己写；
3. **`Ctx.RealIP()` 无条件信任 `X-Forwarded-For` / `X-Real-Ip`**——客户端自填一个头就是一个新桶，
   按 IP 限流形同虚设。**这是安全问题，不是功能缺失。**

## 2. 设计

### 2.1 键提取器（`middleware/ratelimit_key.go`）

```go
type KeyFunc func(*router.Ctx) string

func KeyRemoteIP(trusted []netip.Prefix) KeyFunc   // 见 §2.3 的信任模型
func KeyPath() KeyFunc                             // 原始路径（不含 query）
func KeyQuery(name string) KeyFunc                 // 指定 query 参数
func KeyHeader(name string) KeyFunc                // 自定义请求头
func KeyBasicAuthUser() KeyFunc                    // Basic Auth 用户名
func KeyFingerprint(parts ...KeyFunc) KeyFunc      // 复合指纹：各部分以分隔符拼接后哈希
```

- **指纹**用非加密哈希（FNV-1a 或 xxhash）而非 SM3：限流键不需要抗碰撞强度，需要的是速度；
  但**必须哈希**——原始 UA/IP 组合直接作为键会出现在日志与 Redis key 里，属隐私泄露。
- **空值语义**：某维度取不到值（如无 Basic Auth）时返回**固定标记**（如 `"-"`），
  不要返回空串——空串会让"所有无法识别的请求"挤进同一个桶，形成意外的全局限流。
- **复合指纹的稳定性**：各部分顺序固定；缺失部分用固定标记占位，保证同一种请求始终算出同一个键。

### 2.2 单机 / 多机后端

```go
type LimitStore interface {
    Allow(ctx context.Context, key string, rate float64, burst int) (Decision, error)
}
```

- **`MemoryStore`**：现有分片令牌桶，移动到独立文件，语义不变；
- **`RedisStore`**（多机）：用 **Lua 脚本** 做原子的令牌桶取令牌，因为
  「读 tokens → 按时间补充 → 判断 → 写回」若分多条命令执行，两个实例必然超发。
  - **时间取 Redis 服务器的 `TIME`**，不取各实例本地时钟——实例间时钟漂移会让补充速率失真；
  - 键带统一前缀与 TTL（避免僵尸键堆积）；
  - 脚本返回剩余令牌与重试间隔，用于 `X-RateLimit-*` 响应头；
  - Redis 不可用时**失败开放还是失败关闭**必须显式配置：默认 **失败开放**（放行）并记 Warn——
    限流是保护措施，不应在存储故障时把整个服务打死；但必须可切换为失败关闭。
- **键前缀含租户/维度**，避免不同规则之间互相污染。

### 2.3 远程 IP 的信任模型（安全修复）

现状 `RealIP()` 直接取 `X-Forwarded-For` 的**最左**值——而最左值是**客户端可任意伪造**的。

正确做法：

- 增加 `TrustedProxies []netip.Prefix` 配置；
- **对端不在可信集合内**：一律用 `RemoteAddr`，**完全不看** XFF/X-Real-Ip；
- **对端可信**：从 XFF **最右**往左走，跳过可信代理，取第一个不可信的地址——
  这才是经得起伪造的算法（最左值不可信，最右值是对端自己写的）；
- XFF 非法（空段、非 IP、超长）时回退到 `RemoteAddr`，**不得**信任畸形值；
- `Ctx.RealIP()` 的文档必须改写：明确它信任代理头，因此**只能在可信代理之后使用**；
  并新增 `Ctx.ClientIP(trusted)` 承载正确算法。限流中间件默认走后者。

## 3. 测试

- 每个键提取器：正常取值、缺失值的固定标记、大小写与空白处理；
- **伪造抵抗**：请求带 `X-Forwarded-For: 1.2.3.4` 但对端不可信 → 键仍是 `RemoteAddr`；
  对端可信 + XFF 多跳 → 取第一个不可信跳；畸形 XFF → 回退；
- **Redis 原子性**：N 个并发请求打同一个键，断言放行数**恰好等于** burst（多一点就是超发，
  少一点就是丢令牌）；用 `miniredis` 或真实 Redis（若本机有）；
- **多实例一致性**：两个 `RedisStore` 实例共用同一 Redis，各自持有一个限流器，
  断言合计放行数等于配置值（这直接证明「多机」生效）；
- **故障策略**：Redis 不可达时，失败开放/关闭两种配置的行为各自被断言；
- **性能**：内存后端在多键下的分配与延迟不得退化（对比现有基准）。

## 4. 验收标准

1. 七种维度各自可用，且能任意组合成复合键；
2. 多机模式下放行总数不随实例数放大；
3. **伪造 X-Forwarded-For 不能绕过 IP 限流**（有专门的对抗性用例）；
4. Redis 故障时的行为由配置决定，两种都有测试；
5. 现有内存后端的行为与性能不回归；
6. 全仓门禁全绿。

## 5. 已知取舍

- **指纹不是身份**：UA/语言等可伪造，指纹限流只提高门槛，不能替代认证。
  文档必须写明这一点，避免使用者误以为它是安全边界。
- **不做固定窗口/漏桶等其它算法**：现有令牌桶已覆盖需求，多算法并存只会增加配置歧义。
- **Redis 依赖**：多机模式要求 Redis（本仓已有 `data/redisx`），不引入新组件。
