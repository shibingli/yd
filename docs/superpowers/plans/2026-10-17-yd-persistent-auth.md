# yd 实施计划：租户/用户/角色/权限落到真实数据库

> 需求来源（用户原话）：
> 「所有的租户、用户、角色及权限系统都应该采用真实数据库实例生成，
> 同时完成对应数据库索引及联合索引的创建」

## 0. 需求差异说明

`docs/需求设计说明书.md` 为**只读需求基线**，其中没有关于鉴权数据持久化的条目。
本文件即该需求变更的差异说明，后续以本文件、`CLAUDE.md`、`AGENTS.md` 为准。

## 1. 现状与缺口

生成项目的鉴权数据**完全活在内存里**，且**应用从不连接数据库**：

| 事实 | 证据 |
|---|---|
| 注入的是内存实现 | `scaffold/templates/_name_/internal/repository/store.go.tmpl:82` — `raw := auth.NewMemoryStore()` |
| 应用不连库 | 全仓 `grep -rln gormx scaffold/templates/` **零命中**；`cmd/app/main.go.tmpl` 无任何打开连接的代码 |
| `--db` 只写了配置 | `--db` 的作用范围仅限 `configs/config.yaml`、`.env.template`、README 表格；进程启动后没有任何代码读它去建连接 |
| 模型是「摆设」 | `internal/model/*.tmpl` 定义了完整 GORM 标签（**含联合唯一索引**），但无人 `AutoMigrate`，也无人查询 |

后果：租户、用户、角色、权限**进程一重启就全部丢失**，多副本部署更是各持一份互不相干的数据。

值得注意的是，**蓄意的设计已经把路铺好了**：`internal/repository/store.go.tmpl` 的文档注释写着
「换成 GORM 的步骤（三步，业务代码一行都不用改）」——仓储层向上只暴露本项目模型，向下只依赖
`auth.Store` 接口。本计划就是把这三步真正做完，并且做进框架，让**每个**生成项目开箱即得。

## 2. 设计

### 2.1 框架侧：`auth/gormstore`（新增包）

在框架内实现 `auth.Store` 的 GORM 版本，而不是把实现代码生成进每个项目：

- **一次实现、一次测试**：`auth` 包保持零 GORM 依赖（域模型干净），GORM 只在子包出现；
  手写应用也能直接用，不必依赖脚手架。
- **表名带 `auth_` 前缀**（`auth_tenants` / `auth_users` / `auth_roles` / `auth_role_permissions`）：
  框架拥有的表不与业务表争夺 `users`、`roles` 这类通用名。
- **契约与内存实现逐条对齐**：每个方法都带 `tenantID` 且**必须写进 WHERE**；
  跨租户或不存在一律返回 `auth.ErrNotFound`，绝不返回 `ErrForbidden`（否则调用方能探测
  别的租户是否存在数据）；唯一约束冲突包装 `auth.ErrConflict`。
- **`Migrate(ctx)`**：`AutoMigrate` 建表并创建下述全部索引。
- **`New(db, opts)`**：接受既有的 `*gorm.DB`（来自 `data/gormx`），不自行开连接。

### 2.2 索引设计（由接口的查询形态反推，全部为联合索引）

| 表 | 索引 | 支撑的调用 |
|---|---|---|
| `auth_tenants` | `slug` 唯一 | `CreateTenant` 的重复判定 |
| | `created_at` | `ListTenants` 按创建时间排序 |
| `auth_users` | `(tenant_id, id)` 唯一 | `GetUser` / `UpdateUser`（联合主键语义，租户内唯一） |
| | `(tenant_id, username)` 唯一 | `GetUserByUsername` + 登录路径 + 同名冲突判定 |
| | `(tenant_id, created_at)` | `ListUsers` 在租户内按创建时间排序 |
| `auth_roles` | `(tenant_id, id)` 唯一 | `GetRole` / `DeleteRole` |
| | `(tenant_id, name)` 唯一 | `UpsertRole` 的「同租户内同名覆盖」+ `ListRoles` 排序 |
| `auth_role_permissions` | `(tenant_id, role_id, permission)` 唯一 | 权限集合的幂等写入 |
| | `(tenant_id, role_id)` | 按角色读取权限（RBAC 判定热路径） |

**为什么都是联合索引**：接口里**没有任何一个**方法只按全局 ID 查询——每个方法都带 `tenantID`。
单列索引在这种查询下只能命中一半条件，因此主键与唯一约束都必须以 `(tenant_id, …)` 打头。

### 2.3 脚手架侧：真正把连接接上

1. **打开连接**：当 `DB != "none"` 时用 `data/gormx` 打开 `*gorm.DB`（DSN 来自既有配置），
   失败则**降级为内存实现并告警**，不让应用起不来（与「工具缺失不得阻塞构建」同一口径）。
2. **迁移**：启动时 `gormstore.Migrate(ctx)` 建表建索引。
3. **注入**：`repository.Open` 把 `auth.NewMemoryStore()` 换成 `gormstore.New(db, …)`；
   `DB == "none"` 时保持内存实现并明确告警「数据不持久」。
4. **收尾**：关闭时释放连接池（接入既有的 `app.OnShutdown`）。
5. `internal/model/*` 的 GORM 标签与 `auth_*` 表的关系要在文档里说清：框架拥有鉴权表的 DDL，
   项目模型是渲染用的视图类型。

## 2.4 前后端权限判断工具链（第二条需求）

> 需求来源（用户原话）：
> 「前后端要有对应的角色及权限校验和验证工具，以便于用户快速完成编程的权限判断」

现状：后端只有 `auth.RequirePermission("user:create")` 一个中间件；前端没有任何权限判断手段，
权限串是**散落的裸字符串**。裸字符串的问题是**拼错不报错**——`user:creat` 不会编译失败，
只会永远判定为「无权限」，且没人会发现。

### 2.4.1 后端：声明式权限注册表 + 一行判定

- **`authz` 包**：`Permission` 类型 + **注册表**（`Register` / `Lookup` / `All`），
  每条权限带分组、双语描述与默认角色绑定建议，因此权限是**可枚举、可校验、可渲染**的一等公民。
- **判定入口**（都要能一行完成）：
  - `authz.Can(ctx, perm) bool` —— 只判断，不写响应；
  - `authz.Allow(c *router.Ctx, perm) error` —— 无权限时写 403 并返回错误，处理器里一行守卫；
  - `authz.Require(perm) router.Middleware` —— 复用既有 `auth.RequirePermission` 能力但接注册表；
  - **模板函数** `can`：模板里写 `{{ if can "user:create" }}…{{ end }}`。
- **常量即权限**：`authz.PermUserCreate` 等常量与注册表同源，写代码时可用常量替代裸字符串。

### 2.4.2 前端：声明式指令 + JS API（**只做体验，不做安全**）

- **声明式**：`data-yd-perm="user:create"`（配 `data-yd-perm-mode="hide|disable"`），
  页面加载与**每次 htmx 交换后**自动生效（用 htmx 4 的事件，不能只跑一次）。
- **JS API**：`yd.can("user:create")` / `yd.perms()`，供 Alpine 表达式与自定义脚本使用。
- **权限下发**：服务端把当前主体的权限集合注入页面（内联 JSON，`go:embed` 风格，**零外部请求**）。
- **红线（必须写进文档与注释）**：前端判定**只是体验优化**，
  真正的强制点在服务端——隐藏了按钮不等于禁止了操作，每个受保护端点仍必须做服务端校验。

### 2.4.3 常量统一管理（第三条需求）

> 需求来源（用户原话）：
> 「所有的前后端角色及权限字符串都要采用常量统一管理和引用」

**单一事实来源**：角色与权限的字符串**只在 `authz` 注册表里出现一次**，其余任何地方
（Go 代码、模板、JS、脚手架产物）都只能引用常量或由注册表派生的映射。

| 消费方 | 引用方式 |
|---|---|
| Go 后端 | `authz.PermUserCreate` / `authz.RoleAdmin` 常量（与注册表同源） |
| Go 模板 | 渲染器把注册表作为数据下发，模板写 `{{ if can .Perm.UserCreate }}`，**不写字符串字面量** |
| 前端 JS | 服务端在页面内联注入由注册表生成的 JSON（`yd.PERM` / `yd.ROLE`），JS 写 `yd.can(yd.PERM.USER_CREATE)` |
| 项目自定义常量 | `yd authz gen` 可选地把注册表生成为项目内的 Go 常量文件与前端常量文件，便于自动补全 |

**为什么必须同源**：前端与后端各写一份字符串，一旦漂移（后端 `user:create`、前端 `user:create ` 多一个空格，
或改成 `users:create`），症状是「后端有权限、前端按钮却不显示」或反过来——**两边都不会报错**，
只能靠人肉比对。同源之后这类漂移在结构上不可能发生。

### 2.4.4 验证工具：`yd authz check`

静态扫描项目源码与模板，把每一处权限串（`RequirePermission` / `authz` 调用 /
`data-yd-perm` / `{{ can "…" }}` / `can("…")`）与注册表比对：

- 引用了**未注册**的权限 → 报错（拼写错误在这里被抓住，而不是在生产环境的静默拒绝里）；
- 注册了但**从未被引用** → 提示（便于清理僵尸权限）；
- **出现裸字符串**（Go / 模板 / JS 里直接写 `"user:create"` 而非引用常量）→ 报错
  ——这是「常量统一管理」这一条的可执行落点，没有它，常量只是一句倡议；
- 退出码非零即视为校验失败，可直接进 CI。

同时提供 `yd authz list` 列出注册表（分组、描述），供运维与前端同学对照。

## 2.5 默认初始化数据（第四条需求）

> 需求来源（用户原话）：
> 「new项目后，要有默认初始化租户、用户、角色、权限及关联数据」

`yd new` 产出的项目首次启动后应自带一套可登录、可授权的完整初始数据：

| 对象 | 内容 |
|---|---|
| 租户 | 一个默认租户（`default`），名称与标识可配 |
| 用户 | 一个默认管理员（用户名可配），口令经 `guomi.HashPassword` 加盐多轮存储 |
| 角色 | 默认角色（如 `admin` / `member`） |
| 权限 | `authz` 注册表里的全部权限条目 |
| **关联** | **角色 → 权限**的绑定与**用户 → 角色**的绑定都来自注册表声明的默认绑定，不是手写清单 |

### 2.5.1 三条硬性约束

1. **单一事实来源**：角色名、权限名一律取自 `authz` 注册表常量与其中的默认绑定声明；
   种子代码里**不得出现角色/权限字符串字面量**（否则会被 `yd authz check` 判为裸字符串）。
2. **幂等**：只在「租户表为空」时执行首次初始化；重复启动、多副本并发启动都不得产生重复数据
   （唯一索引 + 冲突视为「已初始化」而非错误）。
3. **生产安全**：默认管理员口令**不得**硬编码进源码。
   - 从 `seed.admin_password` / `YD_SEED_ADMIN_PASSWORD` 读取；
   - 开发环境未配置时使用文档化的开发口令并**每次启动告警**；
   - 生产环境（`app.env` 非 development）未配置时**拒绝初始化并明确告警**，
     绝不用「默认口令」悄悄开一个管理员账号。
   口令永不写入日志。

## 2.6 连接串预置（第五条需求）

> 需求来源（用户原话）：
> 「所有数据库类连接字符串都要将时区、时间配置、空值、默认字符集等进行预置」

### 2.6.1 现状：连接串是裸的

| 位置 | 现状 | 问题 |
|---|---|---|
| `data/gormx` | `DSN` 原样透传给 dialector，**零预置** | 纯靠使用方写对，写漏了就静默降级 |
| 脚手架 MySQL | `…?charset=utf8mb4&parseTime=True&loc=Local` | **`loc=Local` 把时间绑在服务器本地时区**，跨时区/跨机房必错；无 `collation`（不同 MySQL 版本默认排序规则不同）；无 `sql_mode`（零日期与静默截断） |
| 脚手架 PostgreSQL | `host=… sslmode=disable` | 无 `TimeZone`、无 `client_encoding` |
| 脚手架 SQLite | `data/<name>.db` | **无任何 pragma**：无 WAL、无 busy_timeout、无外键约束 |

### 2.6.2 设计：`gormx` 里的 DSN 预置层

新增 `gormx.PresetDSN(driver, dsn string) string`：按驱动补齐下列默认参数，
**显式写出的参数一律优先于预置值**（预置是默认值，不是强制值）。

| 维度 | MySQL | PostgreSQL | SQLite |
|---|---|---|---|
| 时区 | `loc=UTC`、`time_zone='+00:00'` | `TimeZone=UTC` | `_loc=UTC`（**见下方修正**，惰性但无害） |
| 时间配置 | `parseTime=True` | 由驱动天然处理 | 不设置（**见 §2.6.4**：默认格式已是带偏移量的那个） |
| 空值/严格性 | `sql_mode='STRICT_TRANS_TABLES,NO_ZERO_DATE,NO_ZERO_IN_DATE,ERROR_FOR_DIVISION_BY_ZERO'` | 由 PG 严格类型天然保证 | `_pragma=foreign_keys(1)`（拒绝悬空外键） |
| 字符集 | `charset=utf8mb4`、`collation=utf8mb4_unicode_ci` | `client_encoding=UTF8` | 由 SQLite 的 UTF-8 天然保证 |
| 并发/持久性 | 保持 `PrepareStmt`（框架已默认开启） | 同左 | `_pragma=journal_mode(WAL)`、`_pragma=synchronous(NORMAL)` |

**两处必须同时改**，否则只修一半：

1. `gormx.Open` 内部对收到的 DSN 走一遍 `PresetDSN`——手写应用直接受益；
2. 脚手架的 `dsnFor` 生成的连接串改为预置后的形态——**生成的配置本身就是对的**，
   而不是依赖运行期兜底。

**行为变更（需明确告知）**：MySQL 的时间语义由 `loc=Local` 改为 `loc=UTC`。
这会让「本地时间」不再原样往返，需要显式转换；但它是唯一能在多时区、多副本下保持一致的选择，
且与框架日志一律输出 UTC 的既有约定一致。需要 `Local` 的使用方可在自己的 DSN 里显式写 `loc=Local` 覆盖。

## 2.7 默认无 CGO（第六条需求）

> 需求来源（用户原话）：
> 「当前项目默认应该采用无CGO环境搭建（未来用户自行添加的CGO不管），
> 包括数据库SQLite也要采用Golang版本的SQLite进行替代」

### 2.7.1 核查结论：默认形态已经无 CGO，本次补的是护栏

| 项 | 实际状态 | 证据 |
|---|---|---|
| SQLite 驱动 | **已是纯 Go** | `go.mod` 直接依赖 `github.com/glebarez/sqlite` → `glebarez/go-sqlite` → `modernc.org/sqlite`；`go list -deps ./...` 中无任何需 cgo 的包（`go.sum` 里那条 `mattn/go-sqlite3` 只是模块图哈希残留，未参与编译） |
| `yd build` 产物 | **强制无 CGO** | `build/build.go` 的 `buildEnv` 固定注入 `CGO_ENABLED=0` |
| 环境优先级 | **内置值胜过 shell** | `mergeEnv(os.Environ(), buildEnv, o.Env)` 后者优先，因此 shell 里的 `CGO_ENABLED=1` 会被内置的 `0` 覆盖；只有程序里显式传 `Options.Env` 才能开启 cgo |
| 容器镜像 | 无 CGO | `Dockerfile` 模板为 `CGO_ENABLED=0 GOOS=linux go build` |
| 交叉编译 | 无 CGO | e2e `TestCrossCompile` 以 `CGO_ENABLED=0` 构建三平台产物 |

### 2.7.2 本次要做的：把「默认无 CGO」变成可执行的约束

现状全靠**约定**维持——没有任何测试阻止有人把 SQLite 驱动换成 `mattn/go-sqlite3`，
或写下一个 `import "C"`。这类替换会一次性破坏三条性质（静态链接、scratch 镜像、
无 C 工具链交叉编译），且症状要到交叉编译或容器运行时才暴露。

新增守卫 `TestNoCgoInDefaultBuild`（`internal/repotest`）：

1. 全仓 `.go` 不得出现 cgo 伪包导入 `import "C"`；
2. `go.mod` **直接依赖**不得含需 cgo 的 SQLite 驱动（`mattn/go-sqlite3` 等）；
   两种 `require` 写法（块内单行与单行语句）都要覆盖——第一版只认前者，
   变异验证时漏判，已修正。

**边界**：使用方日后自行引入 cgo 是他们的选择，本守卫只约束**框架自身**不得成为引入点，
因此不检查传递依赖，也不阻止使用方在自己的项目里做别的选择。

## 2.8 Shell 环境增强（第七条需求）

> 需求来源（用户原话）：
> 「为命令行工具增加shell/zsh/csh/powershell环境增强提示安装功能」

现状：`yd` 有 12 个子命令（`new` / `dev` / `gen` / `build` / `docker` / `ko` / `service` / `tools` /
`assets` / `align` / `authz` / `db`…），但**没有任何 shell 集成**：命令名、子命令、标志全靠记。

### 2.8.1 从命令注册表生成，不手写脚本

`cli.Command` 已经携带 `Name` / `Short` / `Usage` / `Flags func(*pflag.FlagSet)`。
补全脚本**从注册表生成**（对每个命令在一个临时 `pflag.FlagSet` 上调一次 `Flags`，再 `VisitAll` 读回标志），
因此新增命令或标志时补全**自动跟上**——手写四份脚本必然漂移，这正是要避免的。

- 四种 shell：**bash / zsh / csh / powershell**。
- zsh 与 powershell 支持描述文本，把 `Short` 一并带进补全项，做到「补全即文档」。

### 2.8.2 命令

```
yd completion bash|zsh|csh|powershell        # 打印脚本到标准输出（可 source <(yd completion bash)）
yd completion install [--shell S] [--dir D] [--yes] [--append-rc]
yd completion detect                          # 报告检测到的 shell 与将要写入的位置
```

- **检测**：POSIX 取 `$SHELL` 的 basename；Windows 判 powershell；`--shell` 可覆盖。
- **安装**：把脚本写进约定位置（如 `~/.yd/completion.<shell>`），然后**打印需要 source 的那一行**。
- **默认不碰 rc 文件**：与「不得改动用户 PATH」同一原则——`~/.bashrc`、`~/.zshrc`、`$PROFILE`
  是用户的文件，自动改写难以安全撤销。只有显式给 `--append-rc` 才追加，
  且必须**幂等**（带标记行，已存在则跳过）并**先备份**。
- **环境增强片段**：脚本里附带可选的 `.yd/tools` 加入 PATH 的片段（默认注释掉），
  让用户自己决定是否启用——仍然不替用户改 PATH。
- 非交互场景（`--yes`）只写文件与打印说明，**不追加 rc**。

### 2.8.3 测试

- **注册表一致性**：每个已注册命令都出现在四种脚本里（新增命令忘记同步补全时立刻转红）。
- **标志齐全**：每个命令自己的标志都出现在补全列表中。
- **语法真的合法**：本机存在对应 shell 时，用 `bash -n` 等做真实语法检查；不存在时记录原因并跳过，
  **不得静默通过**（避免「测试绿但脚本根本跑不起来」）。
- **不碰 rc**：断言未给 `--append-rc` 时 rc 文件逐字节未变；给了则幂等（连跑两次不重复追加）且有备份。
- **检测**：`$SHELL` 各取值（含为空、含路径、大小写）与 Windows 分支的表驱动用例。

### 2.6.4 `_time_format` 必须删掉：一个空操作参数换掉了一个关键参数

实施中发现**跨包阻塞缺陷**，我复核驱动源码后裁定删除 `_time_format`：

```go
// glebarez/go-sqlite@v1.21.2/sqlite.go
var writeTimeFormats = map[string]string{
    "sqlite": parseTimeFormats[0],     // 唯一的可选项
}
// 未设置时的默认行为：
return t.Format(parseTimeFormats[0])   // ← 与上面是同一个格式
```

`parseTimeFormats[0]` 本身就带偏移量（`2006-01-02 15:04:05.999999999-07:00`），
因此 **`_time_format=sqlite` 在该驱动上是彻底的空操作**。它唯一的实际效果是让
`applyQueryParams` 在 `_time_format` 分支**提前 `return nil`**，从而**跳过紧随其后的 `_txlock` 解析**——
`_txlock=immediate` 于是静默失效。

对照实验（同一连接串只差参数，8 并发写）：

| 连接串 | 失败数 |
|---|---|
| `busy_timeout(10000)&journal_mode(WAL)&_txlock=immediate` | **0/8** |
| 同上 `+ &_time_format=sqlite` | **5/8** `SQLITE_BUSY` |
| 只有 `busy_timeout + WAL`（无 `_txlock`） | **5/8** |
| 经 `gormx.Open`（预置自动补 `_time_format`） | **5/8** `SQLITE_BUSY_SNAPSHOT` |

第二、三行同形 ⇒「加了 `_time_format`」等价于「没有 `_txlock`」。而延迟事务「先读后写」时
SQLite 返回的 BUSY/BUSY_SNAPSHOT **不是等待能解决的**，`busy_timeout` 救不了。

**影响面**：脚手架生成的 SQLite 连接串本就写着 `_txlock=immediate`，经 `gormx.Open` 必然被中和，
因此 `yd new --db sqlite` 的产物**并发写会直接失败给用户**——这正是被
`auth/gormstore.TestConcurrentAccessIsRaceFree`（8 并发建用户）抓住的。

**处置**：从预置表删除 `_time_format`，并加两条防回归断言——预置结果不含 `_time_format`，
且 `?_txlock=immediate` 的输入经预置后 `_txlock` **仍在**。不做「对 BUSY 有界重试」的兜底：
那会把配置层缺陷盖住，治标不治本。

### 2.6.3 实施时对本计划的一处修正（读驱动源码后得出）

计划最初假定 SQLite 侧的 `_loc=UTC` 能设定时区。**读 `github.com/glebarez/go-sqlite` 源码后证伪**：
该驱动的查询串解析器**不认识 `_loc`，未知键直接丢弃**——也就是说 `_loc` 在本框架所用驱动上是**惰性键**。

- 真正保证时间正确性的是 **`_time_format=sqlite`**：写入时带上偏移量、读回时按此解析，
  已用「直接读库里的原始文本含 `+00:00`」的断言证伪过；
- `_loc` **保留**在预置表里，是为了让 mattn 一系的 SQLite 驱动拿到同一份预置形态，
  这一点已在 `preset.go` 的注释里如实写明，而不是假装它生效。

同批核实还确认了三件事：MySQL 的 `charset` 被驱动翻译成 `SET NAMES <cs> COLLATE <collation>`；
预置取值一律走 `url.QueryEscape`（三个驱动同源，因此编码规则单一且可证伪）；
`?` 只有在 SQLite 路径下标 ≥ 1 时才算查询串分隔符，故 `file::memory:?cache=shared` 形态保真。

## 2.9 库不存在时自动建库（第八条需求）

> 需求来源（用户原话）：
> 「数据库中件间当账号仅限足够大，但是没有创建好数据库时，应该自动创建一个数据库」
> （按语义理解为：连接账号**权限足够**、但目标库**尚未创建**时，应自动建库）

### 2.9.1 现状：两种完全不同的故障被当成一件事

`gormx.Open` 失败一律返回 `ErrOpenFailed`，上层随后降级到内存并告警。于是
「**服务不可达**」与「**库还没建**」这两件事对用户呈现为同一条消息——而后者是**可在连接时直接解决**的：
账号有权限时建一个库就行，不该退化成「数据不持久」。

### 2.9.2 设计

在 `gormx.Open` 的失败路径上加一次**有界的自动建库**：

1. 判定错误是否为「库不存在」——用**错误码**而不是字符串匹配：
   MySQL 的 `ER_BAD_DB_ERROR`（1049）、PostgreSQL 的 SQLSTATE `3D000`（`invalid_catalog_name`）。
   SQLite 不参与（文件由驱动自动创建）。
2. 连到该服务器的**维护库**（MySQL 用不指定 dbname 的连接；PostgreSQL 用 `postgres`），
   执行建库语句，随后**重试一次**原连接。
3. 建库语句必须带上预置的字符集（MySQL：`CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci`），
   否则会建出一个与 §16 预置不符的库，把字符集默认值架空。
4. PostgreSQL 的 `CREATE DATABASE` 不支持 `IF NOT EXISTS`，把 `42P04`（duplicate_database）当作成功。

- 开关：`db.create_database`（默认**开启**，因为需求就是「应该自动创建」）；关闭时行为与现在一致。
- 不可建时（权限不足等）**如实上报**：错误里同时保留「库不存在」的原始原因与建库失败的原因，
  绝不吞掉任何一个。
- 建库**必须打 Warn**，并写明建了哪个库。

### 2.9.3 必须写进文档的风险：拼错库名会被静默掩盖

自动建库把「配置写错」与「库确实还没建」变得难以区分：把 `prod_users` 写成 `prod_usr` 时，
进程会**建出一个空库并正常启动**，而使用者以为一切正常。这是本特性固有的代价，因此：

- 建库一律 Warn，且日志里必须有库名；
- 治理文档与生成的配置注释里都要写明这条风险与关闭开关。

## 2.10 数据表与索引统一 `yd_` 前缀（第九条需求）

> 需求来源（用户原话，分两次给出，以第二次为准）：
> 「要求所有数据表都要加上yd_前缀」→「要求所有数据表和索引都要加上yd_前缀」

现状：框架拥有的鉴权表叫 `auth_tenants` / `auth_users` / `auth_roles` / `auth_role_permissions`，
没有任何前缀——与使用方自己的 `users`、`roles` 处在同一个命名空间里，靠「名字取得巧」避免撞名。

### 2.10.1 设计：全局命名策略 + 显式表名两处同时到位

| 对象 | 来源 | 机制 | 结果 |
|---|---|---|---|
| 表 | 使用方的模型（`yd gen` 生成或自己写） | `gormx.Open` 配置 `NamingStrategy{TablePrefix: "yd_"}` | 结构体 `User` → `yd_users` |
| 表 | 框架拥有的鉴权表 | `auth/gormstore` 的显式 `TableName()` | `yd_auth_tenants` / `yd_auth_users` / `yd_auth_roles` / `yd_auth_role_permissions` |
| 索引 | 使用方模型的默认派生索引 | 自定义 `NamingStrategy.IndexName` | `yd_idx_<表名去掉 yd_ 前缀>_<列>` |
| 索引 | 框架显式声明的索引 | `auth/gormstore` 结构体标签里的名字 | `yd_idx_auth_users_tenant_id` 一类 |

**为什么四处都要改**：GORM 对实现了 `Tabler`（显式 `TableName()`）的模型**直接采用其返回值**，
对标签里显式写出的索引名同样**直接采用**——两者都不再经过命名策略。只配全局策略，
框架自己的表与索引就漏掉了；只改显式名字，使用方的模型就漏掉了。

**索引为何不写成 `idx_yd_auth_users_...`**：那样虽然含 `yd_`，却**不以 `yd_` 开头**，不满足
「加 `yd_` 前缀」；而写成 `yd_idx_yd_auth_users_...` 又出现双前缀。因此取
**`yd_idx_` + 去掉表前缀的逻辑表名 + 列名**：既以 `yd_` 开头，又不重复表前缀，且仍能一眼看出归属。
超长名字（PostgreSQL 的 63 字节标识符上限）要截断并加短哈希——**不得**直接截断，否则不同索引会撞名。

**鉴权表保留 `auth` 中间段**：`yd_auth_users` 而非 `yd_users`——前缀负责划出框架的命名空间，
中间段负责表达领域归属。两者解决的是不同问题，不该互相吞并。

### 2.10.2 连带影响（必须一次改完，否则留下不一致的中间态）

- `auth/gormstore` 的索引名由 GORM 依表名派生，因此 `idx_auth_users_tenant_id` 一类会变成
  `idx_yd_auth_users_tenant_id`，其测试断言与 `sqlite_master` 清单同步更新；
- e2e 里对表名/索引名的断言；
- 治理文档 §15 的索引表、developer_guidelines §19、以及本项目计划 §2.2 的表名；
- 脚手架里「模型是渲染视图、鉴权表由框架拥有」那段说明中的表名。

**这不是单纯改字符串**：表名是持久化契约的一部分。改名后旧库里的表不会被自动迁移过去，
本仓目前是绿地，但要在文档里写明这条——将来若已有部署，需要显式的数据迁移。

**防回归**：迁移后从 `sqlite_master` 读取全部对象，断言**每一张表与每一个自建索引都以 `yd_` 开头**
（`sqlite_autoindex_*` 是 SQLite 为唯一约束自动生成的内部对象，不由我们命名，需按前缀豁免并说明理由）。
这样新增模型或索引时忘记前缀会立刻转红，而不是等到与使用方的对象撞名才发现。

## 3. 任务分解

| # | 任务 | 产物 |
|---|---|---|
| 1 | `auth/gormstore` 包（实现 + 迁移 + 索引） | `auth/gormstore/*.go` + 测试 |
| 2 | 生成项目接线（连库 / 迁移 / 注入 / 降级 / 收尾） | `scaffold/templates/**` + 测试 |
| 3 | `authz` 包：权限注册表 + 判定入口 | `authz/*.go` + 测试 |
| 4 | 前端权限工具：模板函数 `can` + `data-yd-perm` + `yd.can()` | `web/**` + 生成项目模板 |
| 5 | `yd authz check` / `list` 静态验证工具 | `cli/authz.go` + 测试 |
| 6 | 治理文档与模板文档 | `CLAUDE.md`、`AGENTS.md`、`docs/developer_guidelines.md`、脚手架内文档 |
| 7 | 默认初始化数据（租户/用户/角色/权限/关联，幂等 + 生产安全） | `scaffold/templates/**` + 测试 |
| 8 | DSN 预置层（时区/时间/空值/字符集，可覆盖） | `data/gormx/**`、`scaffold/render.go` + 测试 |
| 9 | Shell 环境增强（补全生成 + 检测 + 提示安装） | `completion/`（新建）、`cli/completion.go` + 测试 |
| 10 | 数据表统一 `yd_` 前缀（全局策略 + 鉴权表显式表名 + 全部断言与文档同步） | `data/gormx`、`auth/gormstore`、`e2e` + 测试 |
| 11 | 库不存在时自动建库 | `data/gormx/**` + 测试 |
| 12 | 守卫与验收 | `internal/repotest`、`docs/verification.md`、e2e |

> **任务 10 必须一次成型**：表名散落在模型、索引断言、e2e、治理文档与脚手架说明里，
> 分头改会留下「代码用新名、断言用旧名」的中间态，而这种中间态跑起来是绿的——
> 因为断言测的是旧名字对应的旧表，直到有人真的迁移才发现对不上。
> 因此它排在并行任务全部落地之后单独执行，不与他人共享文件。

**并行约束**：任务 7（种子数据）落在 `scaffold/templates/**`，任务 8（DSN）落在 `data/gormx/**`
与本文件 `scaffold/render.go` 各一行，任务 9（补全）落在 `completion/` + `cli/completion.go`——
三者文件不相交，可在权限代理释放 `cli/` 后并行。**同文件并行只会互相覆盖**，这是本轮反复
用到的排期原则。

> 任务 7 依赖任务 1（持久化）与任务 3（注册表）双双落地后才开始——种子的角色/权限绑定
> 必须从注册表派生，先于注册表写死一套清单就会与「常量统一管理」自相矛盾。

## 4. 验收标准

1. **真实库往返**：用真实 SQLite（`glebarez/sqlite`，纯 Go）建库，写入租户/用户/角色后
   **重新打开连接**仍能读到——这是「不再丢数据」的直接证明。
2. **索引真的建出来了**：迁移后查询 `sqlite_master`（或等价元数据），断言上述每一条索引
   （含全部联合索引）存在，且**联合索引的列顺序**与设计一致。
3. **租户隔离在 SQL 层生效**：跨租户查询返回 `auth.ErrNotFound`（不是 `ErrForbidden`），
   且断言 SQL 的 WHERE 里带 `tenant_id`。
4. **契约与内存实现一致**：同一组用例分别跑内存与 GORM 两个实现，结果一致
   （表驱动 + 共享断言函数）。
5. **降级不阻塞**：`DB == "none"` 或连库失败时，应用照常启动、鉴权照常可用（内存），有明确告警。
6. **生成项目端到端**：`yd new --db sqlite` 产物启动后，登录、建租户、建用户、建角色全链路可用，
   且**重启进程后数据仍在**。
7. **后端一行判定**：处理器里一行 `authz.Allow` 即可拒绝，无权限返回 403 且不泄露资源存在性。
8. **前端三处生效**：模板函数 `can`、`data-yd-perm` 声明式隐藏/禁用、`yd.can()` JS API；
   且**htmx 交换后依然生效**（不是只在首次加载跑一次）。
9. **静态验证能抓拼写错误**：故意把某处权限写成 `user:creat`，`yd authz check` 必须报错并非零退出；
   注册表内未被引用的权限应给出提示。
10. **前端判定不被当作安全边界**：文档、模板注释与生成的治理文档都要写明服务端才是强制点。
11. **首次启动自带可用数据**：`yd new --db sqlite` 产物首次启动后，默认租户、管理员、默认角色
    与注册表全部权限条目均已存在，且**角色→权限**、**用户→角色**的绑定齐全，可直接登录。
12. **幂等**：连续启动两次、或对已初始化的库再跑一次初始化，数据条数不变（用真实 SQLite 断言计数）。
13. **生产不落默认口令**：非 development 环境且未配置口令时拒绝初始化并告警；
    任何日志中都不出现口令明文。
14. **种子无裸字符串**：种子代码里的角色/权限名全部来自 `authz` 常量，`yd authz check` 通过。
15. **连接串预置齐全**：三种驱动各自生成的连接串都含时区、时间配置、空值严格性与字符集参数
    （逐参数断言，缺一即失败）。
16. **显式参数优先**：使用方在自己的 DSN 里写了 `loc=Local` / `TimeZone=Asia/Shanghai` 时，
    预置值不得覆盖它（表驱动断言）。
17. **预置是幂等的**：对同一连接串重复预置不产生重复参数。
18. 全仓门禁：`go build` / `go vet` / `gofmt` 零输出，`go test -race ./...` 全绿，交叉编译通过。

## 5. 已知取舍

- **不引入迁移框架**：`AutoMigrate` 足够覆盖「建表 + 建索引」，且已在 `data/gormx` 中被使用；
  版本化迁移（goose/atlas）留给使用方按需接入，不在本次范围。
- **框架拥有鉴权表 DDL**：使用方要加列需改框架或自建表并存——这是「开箱即用」与
  「Schema 完全自有」之间的取舍，本次选择前者，并在文档中写明如何接管。
- **SQLite 作为验收驱动**：纯 Go 实现（无 cgo），可在 Windows 与 WSL 上直接跑真实往返；
  MySQL / PostgreSQL 的方言差异由 GORM 承担，不在本机验收范围。
