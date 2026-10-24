# 节点身份使用指南（`ydx/nodeid`）

本文说明节点身份的来源、稳定性边界、安全性、性能与唯一性，并给出物理机、虚机、容器与
Kubernetes 的部署方式。适用范围：`ydx/nodeid v0.1.x`、yd v1.6.0+。

## 1. 先明确一件事：身份不是每次启动重新算出来的

**纯硬件指纹无法同时满足「换主板不变」「虚机迁移不变」「容器重建不变」。** 主板、SMBIOS
UUID、磁盘序列号都会随替换或克隆改变；容器根本没有稳定的硬件标识，还可能从镜像继承
宿主机的 `/etc/machine-id`。任何声称「每次都从硬件推导、却能跨硬件替换保持不变」的方案，
本质上都依赖外部持久化。

`nodeid` 因此把身份当作**一次性签发、长期保存的凭据性标识**：

```text
首次启动：node.id（显式配置）> 身份文件 > 平台种子 → 写入身份文件 → 返回
后续启动：直接读取身份文件（硬件、主机名、IP、进程号都不再参与）
```

身份跟着**磁盘/卷**走，而不是跟着主板走。这才能在重启、系统升级、硬件替换、虚机迁移、
容器迁移（卷跟随）之后保持不变。

## 2. 优先级与来源

| 优先级 | 来源 | 何时使用 | 稳定性 |
|---|---|---|---|
| 1 | `node.id` / `YD_NODE_ID` | 生产多副本、硬件/磁盘整体替换、严格唯一 | 完全稳定，由运维保证唯一 |
| 2 | `node.id_file` / `YD_NODE_ID_FILE` | 默认方式；文件存在即权威 | 随存储持久；文件在则在，文件丢则重新签发 |
| 3 | 平台种子（仅首次创建） | 文件不存在且允许创建 | 见下表 |
| 4 | 随机 128 位 | 容器默认、无种子或写入失败 | 唯一但不稳定（每次重建可能变化） |

首次创建后可用的平台种子：

| 平台 | 种子 | 跨重启 | 跨硬件替换 | 跨虚机迁移 | 克隆风险 |
|---|---|---|---|---|---|
| Linux | `/etc/machine-id`（或 dbus machine-id） | 是 | 磁盘保留则不变 | 磁盘跟随则不变 | 复制磁盘会复制 |
| Linux | DMI `product_uuid` / 序列号 | 是 | **变**（主板） | 通常保留 | 克隆虚机常重复 |
| Windows | 注册表 `MachineGuid` | 是 | 多数升级保留 | 磁盘跟随则不变 | 复制系统盘会复制 |
| macOS | `IOPlatformUUID` | 是 | **变**（逻辑板） | 虚机磁盘跟随则不变 | 克隆虚机常重复 |
| 容器 | 默认**不使用**宿主种子 | — | — | — | 使用宿主种子必然重复 |

> 关键：平台种子只用于**第一次**创建。文件写入后，即使主板、machine-id 或主机名变了，
> 身份也不会变。想跨整机替换保持不变，保留 `node.id_file` 所在磁盘/卷，或显式配置 `node.id`。

## 3. 部署方式

### 3.1 物理机 / 普通虚机

默认即可：

```yaml
node:
  id: ""                  # 留空：首次从 machine-id/MachineGuid/IOPlatformUUID 推导
  id_file: "data/node-id" # 建议放在数据盘，而不是系统临时目录
```

- 首次启动写入 `data/node-id`（权限 `0600`，目录 `0700`）。
- 重启、升级、换 CPU/内存/网卡不影响。
- **换主板**：Linux 的 machine-id 仍在系统盘，保持不变；macOS 的 IOPlatformUUID 会变，
  但身份文件仍在磁盘上，保持不变。
- **换系统盘/整机替换**：把旧盘的 `node-id` 恢复到新机同一路径，或显式设置 `node.id`。
- **重装系统**：如果保留了数据盘和文件，身份不变；格式化则重新签发。

### 3.2 容器（Docker / Compose / Podman）

容器里默认会**跳过**宿主机 machine-id 与 SMBIOS（否则同一宿主上的所有容器会拿到同一个
身份），第一次启动生成随机身份并写入 `node.id_file`。因此：

```yaml
services:
  app:
    volumes:
      - yd-data:/app/data   # 必须：node.id_file 默认 data/node-id
node:
  id_file: "data/node-id"
  require_persistent: true  # 生产建议：身份无法持久化就拒绝启动
```

- **卷跟随容器**：`docker compose up` 重建容器但保留 named volume → 身份不变。
- **没有卷**：每次重建容器都会得到新身份（唯一，但不稳定）。此时 `require_persistent: true`
  会让启动直接失败，而不是悄悄换身份。
- **`allow_shared_seed: true`** 只适用于「一台主机只跑一个容器」的场景；多容器共用宿主
  machine-id 会得到同一个身份，默认关闭。
- 不要在多副本之间共享同一个 `data` 卷；每个副本一个卷。
- **镜像构建阶段不要运行应用**，否则构建层可能留下 `data/node-id`，所有副本会共享同一身份。
  如构建流程不得不启动应用，构建结束后必须删除该文件（`.gitignore` 只能防止入库，不能防止
  进入镜像层）。

### 3.3 Kubernetes

Deployment 的 Pod 重建后名字与文件系统都可能变化，**不能**靠 Pod 身份保证稳定。可选：

| 方式 | 稳定性 | 说明 |
|---|---|---|
| StatefulSet + `volumeClaimTemplates` | 每副本稳定 | 每个 Pod 一个 PVC，`node.id_file` 指向挂载点 |
| Deployment + 每副本独立 PVC | 需自建调度约束 | 一个 PVC 只能挂一个副本；需保证副本与 PVC 一一对应 |
| 显式 `node.id`（从 ConfigMap/Secret 注入） | 完全稳定 | 多副本各一个值；适合固定拓扑 |
| Downward API `metadata.uid` | 仅 Pod 生命周期内 | 不是节点身份，不能跨重建 |

```yaml
# StatefulSet 片段
volumeClaimTemplates:
  - metadata: {name: data}
    spec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Gi}}}
# 容器内
env:
  - name: YD_NODE_ID_FILE
    value: /data/node-id
  - name: YD_NODE_REQUIRE_PERSISTENT
    value: "true"
```

### 3.4 Windows 服务

默认路径是 `%ProgramData%\yd\node-id`。以 LocalSystem 运行时该目录可写；以不同服务账号
运行时各自的身份文件是独立的（不同用户配置目录），这是预期行为。需要多个服务共享身份时，
显式设置 `node.id_file` 到共同可读写的目录，并确认 ACL 只允许服务账号读写。

### 3.5 macOS

默认使用 `~/Library/Application Support/yd/node-id`。平台种子是 `ioreg` 的
`IOPlatformUUID`（2 秒超时，工具缺失时改用随机身份并持久化）。换逻辑板会改变该种子，
但文件仍保持不变。

## 4. 安全性

节点 ID **不是凭据**。它只用于消息 id、Redis 键/频道、日志与指标标签；不得用于鉴权、
信任判断或作为「只有本节点能操作」的依据。恶意进程可以显式配置或改写它。

| 风险 | 处理 |
|---|---|
| 注入 Redis 键、HTTP 头、日志 | 只允许 `[A-Za-z0-9._-]`，长度 1..64；拒绝 `:`、空白、换行、`/`、`..` |
| 原始硬件标识泄露 | 平台种子先经 SHA-256 域分隔哈希，只暴露 `ydn_` + 32 位十六进制，不写原始 machine-id/UUID |
| 低熵序列号被离线猜测 | 哈希不是加密；序列号本身不是秘密。需要不可关联性时给每个部署设置随机 `node.salt`（仅首次创建生效） |
| 身份文件被他人读取/替换 | 文件 `0600`、目录 `0700`；原子创建（临时文件 + 硬链接/独占创建）；部署时确保目录属主为服务账号 |
| 符号链接攻击 | `os.Link` 遇到已存在目标时读取既有内容；回退路径使用 `O_CREATE|O_EXCL`，不会跟随已存在的符号链接 |
| 伪造节点身份 | 节点 ID 不是认证；跨节点消息的信任边界在 Redis/网络层，必要时用 TLS + ACL |
| 伪造 Pub/Sub 的 `Node` 字段 | 发布者可以把 `Node` 写成目标节点，让目标把自己的回声当伪造消息丢弃；总线必须受信（TLS/ACL/私网），载荷本身没有签名 |
| 日志泄露 `salt` | `salt` 只进入推导，不进入日志；应用日志只记录 id、来源、环境与持久性 |
| 身份文件被静态站点暴露 | `id_file` 不得放在 `web/`、`static/` 等对外目录；它不是密钥，但不应公开枚举 |
| Windows ACL | POSIX `0600/0700` 在 Windows 上不是访问控制边界；由 `%ProgramData%`/自定义目录的 ACL 决定，服务账号应独占读写 |
| 随机数失败 | `crypto/rand`；只有在熵源故障时才回退到时间/进程号/计数哈希（唯一性仍在，但不是密码学随机） |

节点 ID 会出现在消息 id（`<node_id>:<seq>`）中，因此**禁止包含冒号**；`Validate` 已强制
这一点。若自定义 `node.id`，请同时避免可识别的个人信息（如姓名、邮箱）。

## 5. 稳定性

- **文件即权威**：文件存在时永不重新推导；修改 `node.salt`、换主板、改主机名都不会改变
  既有身份。
- **损坏即失败**：文件为空、多行、超长或含非法字符时返回 `ErrCorrupt`，**不会**静默重新
  生成。这是为了避免身份漂移；由运维修复或显式删除文件后重新签发。
- **并发安全**：多个进程同时首次启动时，`os.Link`/`O_EXCL` 只允许一个写入成功，其余读取
  胜出者的 id；测试覆盖 8 路并发。
- **持久性判定**：临时目录、Linux overlay/tmpfs/ramfs 视为非持久。`require_persistent`
  开启时，在写入前就直接拒绝，不会留下会随容器消失的残留文件。
- **失败回退**：未要求持久化时，无法写入会返回随机身份并带 `Warning`，日志明确说明
  「重启后会变化」；应用仍可启动，但多机广播可能把新身份当成新节点。
- **文件系统能力**：首次创建优先用「临时文件 + 硬链接」原子发布；不支持硬链接的文件系统
  （部分 FAT/exFAT、网络盘）回退到独占创建。回退路径上若进程在写入中途崩溃，可能留下空文件，
  下次启动会报 `ErrCorrupt` 而不是静默换身份——请使用本地、支持原子操作的文件系统。
- **持久性判定是启发式**：Linux 识别 overlay/tmpfs/ramfs，Windows 只识别临时目录。Windows
  容器或特殊挂载可能被误判为持久；生产请显式 `require_persistent` 并用独立卷验证。
- **网络文件系统**：NFS/SMB 上的链接、刷新与锁语义因服务器而异；身份文件应放在本地持久盘或
  可靠的块存储卷上，不要放在弱一致性的共享目录。
- **相对路径按进程工作目录解析**：脚手架默认 `data/node-id`。systemd / SCM 部署若没有设置
  `service.working_directory`，工作目录可能不是项目目录；此时请改用绝对路径，或把
  `service.working_directory` 固定到项目根目录，否则身份可能写到意外位置或写入失败。

## 6. 效率与性能

身份解析只在**启动期**发生，不在请求热路径：

| 操作 | 实测（Windows / i9-10980HK，冷缓存，仅供参考） |
|---|---|
| `derive`（SHA-256 + 编码） | 约 0.52 µs/op，80 B，2 allocs |
| `Resolve` 显式 `node.id` | 约 0.29 ms/op（含环境探测） |
| `Resolve` 读取既有文件 | 约 0.39 ms/op（含环境探测 + 文件读 + 持久性 stat） |
| `DetectEnvironment` | 约 0.30 ms/op |

- 上述耗时由文件系统与平台探测主导，**每个进程只发生一次**；之后 `NodeID()` 是纯字段读取。
- 没有后台协程、没有定时器、没有网络调用；只有配置了 `Claimer` 才会在启动时做一次
  唯一性校验（续期由 Claimer 自己负责）。
- 内存成本是一个 `Identity` 结构（几个字符串与布尔值）；不缓存硬件信息。
- 生成的 id 固定 36 字节（`ydn_` + 32 hex），适合 Redis 键与消息 id，不会因平台而异。

## 7. 唯一性与分布式重复率

### 7.1 哈希碰撞可以忽略

id 是 128 位（SHA-256 截断或 `crypto/rand`）。按生日界，N 个节点的碰撞概率约为
`N² / 2^129`：

| 节点数 | 碰撞概率（约） |
|---|---|
| 1 万 | 1.5 × 10⁻³¹ |
| 100 万 | 1.5 × 10⁻²⁷ |
| 10 亿 | 1.5 × 10⁻²¹ |

**真正的重复风险不是碰撞，而是「同一个种子/文件被复制」**：虚机克隆、磁盘快照、镜像层、
共享卷。这类重复是确定性的，重复率接近 100%，与 id 长度无关。

### 7.2 各场景的重复率

| 场景 | 无 Claimer | 有 Claimer |
|---|---|---|
| 独立物理机/虚机，默认文件 | ≈ 0 | 0（重复被拒绝） |
| 虚机克隆：复制磁盘（含身份文件） | 100% 重复 | 第二个实例启动失败（`ErrDuplicate`） |
| 虚机克隆：未复制文件、machine-id 相同 | 100% 相同推导 id | 第二个实例启动失败 |
| 容器默认（跳过宿主种子、随机） | ≈ 0，但重建后 id 变化 | 0 |
| 容器 `allow_shared_seed: true`，同宿主多副本 | 100% 相同 | 第二个实例启动失败 |
| 多副本共享同一个 `data` 卷 | 100% 相同 | 第二个实例启动失败 |
| 显式 `node.id`，每副本不同 | 0（由运维保证） | 0 |
| 显式 `node.id` 配置重复 | 100% | 第二个实例启动失败 |

### 7.3 用 Claimer 做部署内唯一性校验

`nodeid` 只做本地解析；跨副本的唯一性需要一个部署级租约。接口：

```go
type Claimer interface {
    Claim(ctx context.Context, id, instance string) error
}
```

Redis 示例（单键 `SET NX PX`，避免 CROSSSLOT）：

```go
type redisClaimer struct {
    rdb *redis.Client
    ttl time.Duration
}

func (c *redisClaimer) Claim(ctx context.Context, id, instance string) error {
    ok, err := c.rdb.SetNX(ctx, "yd:node:claim:"+id, instance, c.ttl).Result()
    if err != nil {
        return err
    }
    if !ok {
        // 相同 id 已被另一个活动实例占用；可能是克隆，也可能是上一次崩溃后租约未过期。
        return nodeid.ErrDuplicate
    }
    return nil
}
```

- 租约需要**续期**（例如每 `ttl/3` 用 Lua 比对 value 后 `PEXPIRE`），否则进程仍活着但租约
  过期，另一个克隆会被放行。
- 释放时用 Lua 比对 `instance` 再删除，避免误删新实例的租约。
- Claimer 失败应当**失败关闭**（拒绝启动），不要降级成「继续使用可能重复的身份」。
- 如果没有 Claimer，请依赖显式 `node.id` 或每实例独立存储；不要假设哈希会「碰巧不同」。

## 8. 配置参考

| 键 | 环境变量 | 默认 | 说明 |
|---|---|---|---|
| `node.id` | `YD_NODE_ID` | 空 | 显式身份；最高优先级，完全稳定 |
| `node.id_file` | `YD_NODE_ID_FILE` | 平台默认；脚手架为 `data/node-id` | 持久身份文件 |
| `node.require_persistent` | `YD_NODE_REQUIRE_PERSISTENT` | `false` | 无法持久化时拒绝启动 |
| `node.allow_shared_seed` | `YD_NODE_ALLOW_SHARED_SEED` | `false` | 容器使用宿主种子（仅单容器主机） |
| `node.salt` | `YD_NODE_SALT` | 空 | 推导盐；仅首次创建生效，不是密钥 |

默认文件位置（`node.id_file` 为空时）：

- `YD_NODE_ID_FILE` 显式指定；
- Windows：`%ProgramData%\yd\node-id`（无 ProgramData 时用用户配置目录）；
- 其它：`os.UserConfigDir()/yd/node-id`。

脚手架生成的项目显式设置 `data/node-id`，使身份随项目数据目录走，避免不同项目共用身份。

## 9. 运维手册

### 9.1 备份

- 把 `node.id_file` 纳入数据备份；它很小，但丢失意味着身份变化。
- 显式 `node.id` 存在配置/密钥管理系统里，不要只写在镜像或代码中。

### 9.2 硬件/整机替换

1. 保留旧数据盘，或从备份恢复 `node.id_file` 到新机同一路径；
2. 无法恢复时，显式设置旧的 `node.id`（如果记录过）；
3. 两者都没有，只能接受新身份，并清理旧节点在 Redis/数据库中的租约与在线状态。

### 9.3 克隆/扩容

- 新副本必须使用**新的** `node.id` 或**独立**的 `node.id_file`；
- 不要复制整个数据卷给多个副本；
- 扩容后检查 Claimer 是否拒绝重复（日志中应有 `ErrDuplicate` 或启动失败）。

### 9.4 轮换

节点身份通常不需要轮换。确需更换时：停旧实例 → 释放 Claimer 租约 → 更新 `node.id`/文件 →
启动。旧身份的消息 id 不会冲突，但依赖旧 id 的在线状态、指标序列需要同步清理。

## 10. 排错

| 现象 | 原因 | 处理 |
|---|---|---|
| 启动报 `ErrCorrupt` | 身份文件为空/多行/含非法字符 | 检查文件内容；确认无误后删除并重启以重新签发 |
| 每次重建容器 id 都变 | `id_file` 在容器临时层 | 挂载持久卷，或显式 `node.id` |
| 两个副本 id 相同 | 复制了磁盘/卷，或 `allow_shared_seed` 同宿主 | 每副本独立卷/显式 id；启用 Claimer |
| 启动报 `ErrNeedsProvisioning` | `require_persistent=true` 但路径非持久 | 换持久卷，或关闭该开关并接受重建后变化 |
| 启动报 `ErrDuplicate` | Claimer 发现相同 id 仍被占用 | 确认是否克隆；等待租约过期或修复配置 |
| 显式 `node.id` 被拒 | 含 `:`、空格、`/`、超长 | 只用字母、数字、`.`、`-`、`_`，≤64 字节 |
| 日志出现 `ephemeral identity` | 文件写入失败且未要求持久 | 检查目录权限/磁盘；生产开启 `require_persistent` |

## 11. 检查清单

- [ ] 多副本部署中每个副本的 `node.id` 或 `node.id_file` 唯一；
- [ ] 容器把 `data/node-id`（或自定义路径）挂在每实例独立的持久卷上；
- [ ] 生产设置 `node.require_persistent: true`，让身份无法持久化时启动失败；
- [ ] 身份文件纳入备份，硬件替换/灾难恢复时恢复原文件；
- [ ] 克隆/扩容流程不会复制身份文件；
- [ ] 配置管理、部署脚本与镜像构建不会覆盖或打包 `node.id_file`；
- [ ] 分布式部署配置了 Claimer 并实现了租约续期；
- [ ] 没有把节点 ID 当作鉴权凭据；
- [ ] 自定义 `node.id` 不含冒号、空白、路径分隔符与个人信息。
