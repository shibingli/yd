# GORM 方言使用指南（MySQL / PostgreSQL / SQLite / 达梦 DM8）

`yd` 的数据库层只经 `ydx/gorm` 装配：同一套模型、同一套业务代码可以在四种方言上运行，
驱动选择、连接串预置、自动建库与跨方言 SQL 都由框架收敛。本文是这套能力的使用入口，
覆盖配置、方言能力矩阵、跨方言表达式、各方言实践、迁移与排错。

> 适用版本：`ydx/gorm` v0.1.12+。组件源码与 API 见
> [`github.com/shibingli/ydx/gorm`](https://github.com/shibingli/ydx/tree/main/gorm)。

## 1. 支持的方言

| 规范名 | 别名 | 驱动实现 | 说明 |
|---|---|---|---|
| `sqlite` | 空串 | `github.com/glebarez/sqlite`（纯 Go） | 框架默认；单文件/内存库，无 cgo |
| `mysql` | — | `gorm.io/driver/mysql`（go-sql-driver/mysql） | 8.0+ 才有 CTE、窗口函数与 `FOR SHARE` |
| `postgres` | — | `gorm.io/driver/postgres`（pgx） | 支持 `ILIKE`、`RETURNING`、`FOR SHARE` |
| `dm` | `dameng` | `github.com/godoes/gorm-dameng` | 达梦 DM8；upsert 走 `MERGE INTO`，仅有 `FOR UPDATE` |

驱动名不区分大小写、忽略首尾空白；空串与 `sqlite` 等价。**其它名称一律拒绝**
（哨兵 `gorm.ErrUnsupportedDriver`），不会悄悄退回 SQLite。

选择方式（优先级从高到低）：

```yaml
# configs/config.yaml
db:
  driver: dm
  dsn: "dm://SYSDBA:SYSDBA@127.0.0.1:5236?schema=APP"
```

```bash
YD_DB_DRIVER=postgres YD_DB_DSN='host=127.0.0.1 user=app password=secret dbname=app sslmode=disable' ./app
```

```go
db, err := gorm.Open(gorm.Options{Driver: "mysql", DSN: dsn}, logger)
```

脚手架直接生成对应配置：

```bash
yd new myapp --db dm              # sqlite | mysql | postgres | dm | none
yd gen crud user --driver dm --dsn 'dm://SYSDBA:SYSDBA@127.0.0.1:5236' --table yd_app_user
```

## 2. 连接串预置（`PresetDSN`）

`ydx/gorm` 在建立连接前统一调用 `gorm.PresetDSN(driver, dsn)`，把时区、时间处理、
空值严格性与字符集补齐。规则固定为三条：

- **显式参数优先**：调用方写了某个键就保留其原值，预置只补缺失项；
- **幂等**：对已预置的连接串重复施加不会产生重复参数；
- **形态保真**：URL、PostgreSQL 的 `key=value`、SQLite 的 `file:` URI 原样保留，
  新参数按固定顺序追加。

| 方言 | 预置参数 | 作用 |
|---|---|---|
| MySQL | `loc=UTC`、`time_zone='+00:00'`、`parseTime=True`、`sql_mode='STRICT_TRANS_TABLES,NO_ZERO_DATE,NO_ZERO_IN_DATE,ERROR_FOR_DIVISION_BY_ZERO'`、`charset=utf8mb4`、`collation=utf8mb4_unicode_ci` | 驱动与服务端同为 UTC；`time.Time` 直读；拒绝静默截断与零日期；字符集不随服务端默认值漂移 |
| PostgreSQL | `TimeZone=UTC`、`client_encoding=UTF8` | 会话时区与客户端编码 |
| SQLite | `_loc=UTC`、`_pragma=journal_mode(WAL)`、`_pragma=synchronous(NORMAL)`、`_pragma=busy_timeout(5000)` | 时区（识别该键的驱动）；WAL 并发；锁等待 5 秒 |
| 达梦 DM8 | `timeZone=0` | 驱动侧相对 UTC 的**分钟**偏移；0 表示 UTC |

示例：

```text
mysql:    root:root@tcp(127.0.0.1:3306)/app
      ->  root:root@tcp(127.0.0.1:3306)/app?loc=UTC&time_zone=%27%2B00%3A00%27&parseTime=True&sql_mode=...&charset=utf8mb4&collation=utf8mb4_unicode_ci

postgres: host=127.0.0.1 user=postgres password=postgres dbname=app port=5432 sslmode=disable
      ->  host=127.0.0.1 user=postgres password=postgres dbname=app port=5432 sslmode=disable TimeZone=UTC client_encoding=UTF8

sqlite:   file:data/app.db?_txlock=immediate
      ->  file:data/app.db?_txlock=immediate&_loc=UTC&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)

dm:       dm://SYSDBA:SYSDBA@127.0.0.1:5236?schema=APP
      ->  dm://SYSDBA:SYSDBA@127.0.0.1:5236?schema=APP&timeZone=0
```

### 2.1 SQLite 的两个硬约束

- **必须保留 `_txlock=immediate`**：延迟事务会被并发提交顶掉快照，随后以
  `SQLITE_BUSY_SNAPSHOT`(517) 失败，且 busy timeout 无法化解——ORM 的「先读后写」正是这种模式。
  `PresetDSN` **不会**替你添加它，脚手架生成的 DSN 已带。
- **绝不能加 `_time_format`**：`glebarez/go-sqlite` 看到该键就提前返回，后面的 `_txlock`
  分支永远走不到，`immediate` 会静默失效；而该键本身在它上面是空操作。

### 2.2 自动建库

只有 MySQL（`ER_BAD_DB_ERROR` 1049）与 PostgreSQL（`SQLSTATE 3D000`）参与：

- 首次连接以「目标库不存在」失败时，`Open` 建库并重试一次；判定走错误码，不做字符串匹配；
- MySQL 建库语句带预置表的字符集与排序规则；库名按各库的标识符规则转义；
- **SQLite** 由驱动创建文件，**达梦** 的 schema 只是命名空间（由实例侧预建），两者都不参与；
- 默认开启（`db.create_database` 未配置即为 true），关闭方式：

```yaml
db:
  create_database: false
```

风险：自动建库让「库名写错」与「库还没建」无法区分（例如把 `prod_users` 写成 `prod_usr`
会建出空库并正常启动）。唯一缓解是建库时带库名的 Warn 日志；生产环境建议关闭并用迁移流程建库。

### 2.3 框架固定施加的设置

`Open` 与方言无关地固定以下行为，**不提供开关**：

- `PrepareStmt: true`：预编译语句缓存（`Options.PrepareStmt` 仅为配置对齐保留）；
- `NowFunc` 固定 UTC：自动时间戳不会写入本地墙钟；
- 表名统一 `yd_` 前缀，派生索引名 `yd_idx_` 形态；
- `TranslateError: true`：唯一键/外键冲突翻译成 `gorm.ErrDuplicatedKey` / `gorm.ErrForeignKeyViolated`；
- `DisableForeignKeyConstraintWhenMigrating: true`：迁移绝不生成外键（框架不用数据库级关系约束）；
- 连接池：`db.maxconn`（默认 100）、`db.maxidle`（默认 10）、`db.conn_max_lifetime`、
  `db.conn_max_idle_time`；
- 慢查询阈值 `db.slow_threshold`（默认 200ms），日志级别 `db.log_level`（默认 warn）。

## 3. 方言能力矩阵

```go
d, err := gorm.DialectFor("dm")          // 按驱动名取方言
d, err = gorm.DialectOf(db)              // 按已打开的连接取方言
if err != nil { ... }                    // 未知驱动：gorm.ErrUnsupportedDriver / gorm.ErrNilDB

if d.Features.Has(gorm.FeatureJSON) { ... }
```

| 能力 | MySQL | PostgreSQL | SQLite | 达梦 DM8 |
|---|:---:|:---:|:---:|:---:|
| `FeatureReturning`（`INSERT/UPDATE/DELETE ... RETURNING`） | — | ✓ | ✓ | —（GORM 方言无 RETURNING，插入 ID 由驱动取回） |
| `FeatureJSON`（内置 JSON 路径取值） | ✓ | ✓ | ✓ | — |
| `FeatureRowLock`（`FOR UPDATE` / `FOR SHARE`） | ✓ | ✓ | — | ✓（仅 `FOR UPDATE`） |
| `FeatureILike`（原生大小写不敏感 `LIKE`） | — | ✓ | — | — |
| `FeatureSavepoint` | ✓ | ✓ | ✓ | ✓ |
| `FeatureCTE` | ✓（8.0+） | ✓ | ✓ | ✓ |
| `FeatureWindow` | ✓（8.0+） | ✓ | ✓ | ✓ |
| `FeatureMergeUpsert`（upsert 以 `MERGE` 实现） | — | — | — | ✓ |

`Dialect` 同时给出 `DisplayName`、`RandomFunc`、`ConcatFunc`，下面的辅助函数据此分支；
业务代码**不应**再按驱动名写 `switch`。

## 4. 跨方言表达式

### 4.1 Upsert / UpsertDoNothing

```go
// INSERT ... ON CONFLICT (tenant_id, name) DO UPDATE SET updated_at = excluded.updated_at
err := db.Clauses(gorm.Upsert([]string{"tenant_id", "name"}, []string{"updated_at"})).
    Create(&row).Error

// 冲突时保留原行
err = db.Clauses(gorm.UpsertDoNothing([]string{"id"})).Create(&row).Error
```

各方言最终形态：

| 方言 | 生成的语句 |
|---|---|
| MySQL | `INSERT ... ON DUPLICATE KEY UPDATE updated_at=VALUES(updated_at)`（`conflictColumns` 被忽略） |
| PostgreSQL / SQLite | `INSERT ... ON CONFLICT (tenant_id,name) DO UPDATE SET updated_at=excluded.updated_at` |
| 达梦 DM8 | `MERGE INTO t USING (SELECT ... FROM DUAL) AS "excluded" (...) ON (...)` + `WHEN MATCHED THEN UPDATE` / `WHEN NOT MATCHED THEN INSERT`；`DoNothing` 只保留 `WHEN NOT MATCHED` |

达梦注意事项：

- `conflictColumns` 就是 `MERGE` 的连接键；留空时驱动改用模型主键；
- 驱动会自动把连接键列从 `UPDATE SET` 中剔除，避免 DM 报 `-4064: 不能更新关联条件中的列`；
- `MERGE` 无法通过 `LastInsertID` 取回自增主键，驱动以「按主键倒序取一行」补偿；
  批量 upsert 后不要依赖每行的自增 ID。

MySQL 没有 `DO NOTHING`：`UpsertDoNothing` 被驱动改写为「主键赋给自己」的
`ON DUPLICATE KEY UPDATE id=id`，行内容保持不变，但自增值可能被消耗一次。

### 4.2 JSONExtract

```go
expr, err := d.JSONExtract("payload", "$.user.name")
db.Select("id", expr).Where(...)
```

| 方言 | SQL | 说明 |
|---|---|---|
| PostgreSQL | `payload #>> $1`，路径转成 `{"user","name"}` | `$.*`、空段与数组下标等无法转换的形式原样透传，由数据库报错，避免静默丢段 |
| MySQL | `JSON_UNQUOTE(JSON_EXTRACT(payload, ?))` | `JSON_UNQUOTE` 让返回值与其它方言一样是文本 |
| SQLite | `JSON_EXTRACT(payload, ?)` | 内置 JSON1 |
| 达梦 | — | 返回 `gorm.ErrDialectFeature`，不要静默降级 |

### 4.3 ILike

```go
db.Where(d.ILike("name", "ann%"))
```

- PostgreSQL：`name ILIKE $1`；
- MySQL / SQLite / 达梦：`LOWER(name) LIKE LOWER($1)`，不依赖排序规则是否大小写不敏感。

### 4.4 Concat

```go
db.Select(d.Concat("first_name", "last_name"))
```

- MySQL：`CONCAT(?, ?)`；
- PostgreSQL / SQLite / 达梦：`(? || ?)`；
- 不传列名时返回 `''`。

### 4.5 Random

```go
db.Order(d.Random().SQL).Limit(10)   // MySQL/DM: RAND()；PG/SQLite: random()
```

### 4.6 行锁

```go
locking, err := d.LockClause(gorm.LockUpdate)    // FOR UPDATE
if errors.Is(err, gorm.ErrDialectFeature) { /* SQLite：没有行级锁 */ }
db.Clauses(locking).Where("id = ?", id).First(&row)
```

- MySQL / PostgreSQL：`FOR UPDATE` 与 `FOR SHARE`；
- SQLite：任何行锁都返回 `ErrDialectFeature`（不要静默丢弃）；
- 达梦：只有 `FOR UPDATE`，`FOR SHARE` 返回 `ErrDialectFeature`。

### 4.7 Now

```go
db.Where("created_at < ?", d.Now())   // CURRENT_TIMESTAMP（四种方言都接受）
```

## 5. 各方言实践

### 5.1 MySQL

- 目标版本 8.0+；5.7 不支持 CTE、窗口函数与 `FOR SHARE`，能力位按 8.0 标记。
- 字符集与排序规则由预置补齐；若显式只写 `charset=latin1`，预置会同时压掉
  `collation`，避免 `SET NAMES` 两侧不匹配触发 1253。
- `time_zone='+00:00'` 与会话时区一致；不要另写 `loc=Local`，除非明确要本地墙钟。
- JSON 列用 `JSONExtract`，不要手写 `->>`（MySQL 不认）。

### 5.2 PostgreSQL

- URL 形态（`postgres://...`）与关键字/值形态都支持，预置会保持原形态。
- 表在 `search_path` 的第一个 schema 下；需要固定 schema 时写 `search_path=app,public`
  （显式参数优先，预置不会覆盖）。
- `RETURNING` 可用，GORM 的主键回填走它；`#>>` 路径由 `JSONExtract` 生成。
- 自动建库需要账号具备 `CREATEDB`；`CREATE DATABASE` 无 `IF NOT EXISTS`，
  并发抢建返回 `42P04` 时按成功处理。

### 5.3 SQLite

- 纯 Go 实现（`glebarez/go-sqlite` → `modernc.org/sqlite`），默认 `CGO_ENABLED=0` 可交叉编译。
- 单写者模型：WAL + `busy_timeout(5000)` 已预置；写事务必须 `_txlock=immediate`。
- 没有行锁：需要「读后写」的临界区请用事务 + `UPDATE ... WHERE` 的乐观并发，或换服务端数据库。
- 内存库：`file::memory:?cache=shared`（注意连接池共享同一内存库）。
- 迁移与生产：文件路径用绝对路径；相对路径按进程工作目录解析。

### 5.4 达梦 DM8

**连接串**：`dm://user:password@host:port?props`；属性名不区分大小写。常用属性：

| 属性 | 含义 |
|---|---|
| `schema` | 会话默认模式（命名空间），默认是用户自身模式 |
| `timeZone` | 相对 UTC 的分钟偏移，`0` 为 UTC；框架预置为 0 |
| `compatibleMode` | 兼容模式（如 `oracle`），由实例/会话决定 |

```text
dm://SYSDBA:SYSDBA@127.0.0.1:5236?schema=APP
```

**建库与模式**：DM 没有「按连接切换数据库」这一层；先由 DBA 建实例与模式，再在 DSN 里
用 `schema` 指定。自动建库不参与——连接串里没有库名可建。

**类型映射**（`yd gen crud --driver dm` 按此生成）：

| DM 类型 | Go 类型 |
|---|---|
| `INT` / `INTEGER` / `BIGINT` / `SMALLINT` / `TINYINT` | `int64` |
| `NUMBER` / `NUMERIC` / `DECIMAL` / `FLOAT` / `DOUBLE` / `REAL` | `float64`（`NUMBER` 是精确数值，与 NUMERIC 同义） |
| `DATE` / `DATETIME` / `TIMESTAMP`（含 WITH TIME ZONE） | `time.Time` |
| `BLOB` / `RAW` / `LONGVARBINARY` / `IMAGE` | `[]byte` |
| `VARCHAR2` / `NVARCHAR2` / `CHAR` / `CLOB` / `TEXT` | `string` |
| `BIT` 等其它 | `string`（保守回退） |

**upsert 用 `gorm.Upsert`**：DM 方言把 `ON CONFLICT` 改写为 `MERGE INTO`；必须给出冲突列
（或依赖主键），且不要期待 `RETURNING`。

**标识符大小写**：DM 对未加引号的标识符折叠为大写；GORM 一律给标识符加引号，
因此 `yd_auth_users` 与已有的 `YD_AUTH_USERS` 是两张不同的表。让 GORM 建表（`AutoMigrate`）
或保证既有模式的命名与模型一致。

**行锁**：只有 `FOR UPDATE`；需要共享锁请改用应用层协调或 PostgreSQL/MySQL。

**日期与时间**：`timeZone=0` 保证会话为 UTC；若既有数据按本地时间存储，
在 DSN 里显式写 `timeZone=480`（东八区）等偏移，并同步调整 `loc`/`NowFunc` 预期。

### 5.5 多方言并存

一个应用可以同时打开多个连接（例如主库 MySQL、报表库 PostgreSQL），每个连接各自调用
`gorm.Open`，方言能力用 `gorm.DialectOf(db)` 现取现用，互不影响：

```go
primary, err := gorm.Open(gorm.Options{Driver: "mysql", DSN: mysqlDSN}, logger)
reports, err := gorm.Open(gorm.Options{Driver: "postgres", DSN: pgDSN}, logger)

pd, _ := gorm.DialectOf(primary)   // MySQL 能力
rd, _ := gorm.DialectOf(reports)   // PostgreSQL 能力
```

不要把方言对象缓存成全局单例：它属于具体连接，`DialectOf` 的开销只在装配期或按请求取一次。
事务跨库不属于框架能力范围——需要跨库一致性时由业务层用补偿或消息队列实现。

## 6. 模型、迁移与约束

- 模型只声明字段与索引，不声明关联对象；关系字段只存对方主键（框架规则，见 `CLAUDE.md` §25）。
- `gorm.AutoMigrate(db, &User{}, ...)` 在四种方言上都可用；迁移不生成外键，
  删除/清理从属行由应用在同一事务内完成。
- 数据库级约束只有主键、唯一索引与联合索引；**索引一律以 `tenant_id` 打头**（鉴权与消息表）。
- 时间字段统一 UTC；需要展示本地时间时在视图层转换，不要把本地时间写回库。

## 7. 测试策略

1. **单元测试用 SQLite 内存库**：`file::memory:?cache=shared` + `_txlock=immediate`，
   无需外部服务；
2. **集成测试按方言矩阵**：用环境变量选择驱动与 DSN，例如：

   ```bash
   YD_TEST_DB_DRIVERS=sqlite,mysql,postgres,dm go test ./internal/repository/...
   ```

   每个方言各跑一遍同一组仓储测试；没有对应服务时用 `t.Skipf` 写明原因，不得假装通过。
3. **能力相关的用例显式跳过**：

   ```go
   locking, err := d.LockClause(gorm.LockUpdate)
   if errors.Is(err, gorm.ErrDialectFeature) {
       t.Skipf("dialect %s has no row locks", d.DisplayName)
   }
   ```
4. **不要用字符串匹配错误**：一律 `errors.Is` / `errors.As` 判定
   `ErrUnsupportedDriver` / `ErrDialectFeature` / `ErrDatabaseMissing` / `ErrCreateDatabase`。

## 8. 故障排查

| 现象 | 原因 | 处理 |
|---|---|---|
| MySQL `Error 1253` | `charset` 与 `collation` 不匹配 | 只写 `charset=utf8mb4`，或同时给出匹配的 `collation`；不要混用 latin1 与 utf8mb4 |
| SQLite `SQLITE_BUSY_SNAPSHOT (517)` | 缺少 `_txlock=immediate`，或 DSN 里出现了 `_time_format` | 显式保留 `_txlock=immediate`，删除 `_time_format` |
| PostgreSQL `3D000` | 目标库不存在 | 开启 `db.create_database`（账号需 `CREATEDB`）或先建库 |
| 达梦 `-4064：不能更新关联条件中的列` | `MERGE` 的 `UPDATE SET` 含连接键 | 用 `gorm.Upsert` 的 `conflictColumns` 声明连接键，驱动会自动剔除 |
| 达梦「表或视图不存在」 | `schema` 未指定或大小写不一致 | DSN 加 `?schema=APP`；核对标识符大小写 |
| 时间偏移 8 小时 | 会话/驱动时区不是 UTC | 检查 DSN 是否覆盖了预置的 `loc`/`TimeZone`/`timeZone`；显式值优先 |
| `gorm.ErrUnsupportedDriver` | 驱动名拼错（如 `postgresql`、`dm8`） | 使用 `mysql` / `postgres` / `sqlite` / `dm`（或 `dameng`） |
| 自动建库建出了空库 | 库名拼写错误且账号权限足够 | 关闭 `db.create_database`，用迁移流程；查看建库时的 Warn 日志 |

## 9. 从 SQLite 迁到服务端数据库的检查清单

- [ ] `configs/config.yaml` 的 `driver` / `dsn` 改为目标方言，或设置 `YD_DB_DRIVER` / `YD_DB_DSN`；
- [ ] 确认连接账号有建表权限（自动建库还需要建库权限）；
- [ ] 业务 SQL 中没有方言特有的写法：JSON 用 `JSONExtract`、拼接用 `Concat`、
      随机用 `Random`、行锁用 `LockClause`、upsert 用 `Upsert`；
- [ ] 需要大小写不敏感匹配的地方用 `ILike`；
- [ ] 跑一遍方言矩阵测试，并对 `ErrDialectFeature` 的路径给出降级或跳过；
- [ ] 时间字段确认 UTC，展示层再做本地化；
- [ ] 生产环境关闭自动建库，改用显式迁移。
