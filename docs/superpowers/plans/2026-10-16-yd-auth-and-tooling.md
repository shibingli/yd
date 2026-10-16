# yd 多租户鉴权与内存对齐 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在既有 yd 框架上补齐**多租户 + 用户/角色/权限（RBAC）+ 完整登录授权**，把 JWT 密钥从全局改为**每用户私有 `sec_key`**，完成前端 i18n 与明暗主题切换的前后端同步，并引入 `betteralign` 内存对齐能力（框架 + CLI + 脚手架产物）。

**Architecture:** 新增框架级 `auth` 包承载租户/主体/RBAC/令牌三块能力，以 `Store` 接口抽象持久化（默认内存实现，GORM 实现可插拔）；JWT 复用既有 `jwt` 包，通过 `kid = 用户 ID` 让 `KeyFunc` 在**不解析载荷**的前提下取到该用户的 `sec_key` 完成验签。前端在既有 `yd-i18n.js` 与 `yd-app.js` 上补齐语言/主题切换的完整闭环与后台管理页面。

**Tech Stack:** Go 1.27.2 · 既有 `jwt`/`guomi`/`jsonx`/`b64x`/`logx`/`errx`/`router`/`htmx`/`render`/`i18n` · htmx 4.0 · Tailwind v4 · betteralign。

**Spec:** `docs/需求设计说明书.md` + 本轮追加需求（多租户/RBAC/登录授权/前端 i18n 与主题/sec_key/betteralign）。

---

## Global Constraints

- 沿用既有全部强制约束：注释与日志文案逐行中英同步 `English | 中文`；日志一律 `logx`；
  错误在包级用 `errx.New` 声明哨兵且**禁止 `fmt.Errorf`**；JSON/Base64 一律 `jsonx`/`b64x`；
  导出符号中文 doc 注释（双语）；`gofmt`/`go vet` 零输出；`-race` 必须通过。
- **JWT 密钥必须每用户独立**：注册用户时生成 `sec_key`，签发与验签只用该用户的 `sec_key`，
  **禁止**任何全局 JWT 密钥作为兜底。密钥在库中以 SM4 加密存储（主密钥来自配置/环境变量）。
- 口令只用 `guomi.HashPassword`（SM3 + 盐 + 多轮）存储，比较用常量时间函数。
- 敏感字段（口令等）前端提交前用 SM2 加密（`data-yd-encrypt="sm2"`，`X-YD-Encrypted: sm2`）。
- 租户隔离是**硬边界**：任何按 ID 取实体的接口都必须同时校验 `TenantID`。
- `betteralign` 作为**可选**外部工具：缺失时给出可执行的安装提示，不得让构建失败。

## Review Focus

1. **跨租户越权**：用 A 租户的令牌读取 B 租户的用户/角色 —— 必须 404 或 403，绝不能返回数据。
2. **sec_key 隔离**：用 A 用户的令牌做任何篡改后，用 B 用户的 sec_key 验签必须失败；
   用户被禁用/改密后旧令牌必须失效。
3. **口令与密钥落库形态**：数据库中不得出现明文口令或明文 `sec_key`。
4. **前端切换的持久化与一致性**：语言与主题切换后刷新页面必须保持；切换语言时**页面不整页刷新**。
5. **权限判定粒度**：无权限时必须返回 403 且**不泄露资源是否存在**。

---

### Task 1: `auth` 包——租户、主体与 RBAC

**Files:** Create `auth/{auth.go,tenant.go,user.go,rbac.go,store.go,memory.go,errors.go,*_test.go}`

**Interfaces:**
- `type Tenant struct{ ID, Slug, Name string; Status Status; CreatedAt time.Time }`
- `type User struct{ ID, TenantID, Username, Email, DisplayName string; PasswordHash, SecKeyEnc string; Status Status; Roles []string; CreatedAt, UpdatedAt time.Time }`
- `type Role struct{ ID, TenantID, Name, Description string; Permissions []string }`
- `type Permission string`（形如 `user:create`）；`type Status string`（`active`/`disabled`）
- `type Store interface{ … }`：`CreateTenant/GetTenant/ListTenants`、`CreateUser/GetUser/GetUserByUsername/ListUsers/UpdateUser`、`UpsertRole/GetRole/ListRoles/DeleteRole`、`Tenant-scoped` 全部方法带 `tenantID`
- `func NewMemoryStore() Store`
- `type Service struct{...}`：`NewService(store Store, o Options) (*Service, error)`
  - `CreateTenant`、`CreateUser(ctx, tenantID string, in CreateUserInput) (*User, error)` —— **生成并加密 sec_key**
  - `Authenticate(ctx, tenantID, username, password string) (*User, error)`
  - `Roles/Permissions/RolesOf/SetRoles/AssignPermissions`
  - `HasPermission(u *User, perm Permission) bool`
  - `RotateSecKey(ctx, tenantID, userID string) (string, error)`、`SecKey(ctx, u *User) ([]byte, error)`（解密）
- `type Options struct{ MasterKey []byte; SecKeyLen int; Now func() time.Time }`

- [ ] 步骤：先写测试（跨租户隔离、口令哈希、sec_key 生成与解密、RBAC 判定），再实现，`go test ./auth/ -race` 通过。

### Task 2: 登录、令牌与中间件

**Files:** Create `auth/{login.go,token.go,middleware.go,*_test.go}`

**Interfaces:**
- `type TokenIssuer struct{...}`：`NewTokenIssuer(svc *Service, o TokenOptions) *TokenIssuer`
  - `Issue(ctx, u *User, ttl time.Duration) (string, error)` —— **`kid = u.ID`，HMAC 密钥 = 该用户的 sec_key**
  - `KeyFunc(header jwt.Header) (any, error)` —— 由 `kid` 查用户并解密 sec_key，**不解析载荷**
  - `Verify(ctx, token string) (*User, error)`
- `type LoginResult struct{ Token string; ExpiresAt time.Time; User *User }`
- `func (s *Service) Login(ctx, tenantID, username, password string, ttl time.Duration, issuer *TokenIssuer) (*LoginResult, error)`
- 中间件：`auth.Middleware(o MiddlewareOptions) router.Middleware`、`auth.CurrentUser(c *router.Ctx) (*User, bool)`、`auth.RequirePermission(p Permission) router.Middleware`
- 登录请求解密：`auth.DecryptLoginPayload(payload []byte) (string, error)`（SM2 → 明文口令）

- [ ] 步骤：测试覆盖「A 用户令牌用 B 的 sec_key 验签失败」「禁用用户令牌失效」「改密后旧令牌失效」「无权限返回 403」；`-race` 通过。

### Task 3: `yd align` 与 betteralign 集成

**Files:** Create `align/{align.go,align_test.go}`；Modify `cli/`（新增 `align` 命令）

**Interfaces:**
- `align.Resolve(o ToolOptions) (string, error)`：`Bin` > `ConfigBin` > `EnvBin`(`YD_BETTERALIGN_BIN`) > `.yd/tools/betteralign[.exe]` > `PATH`
- `align.Options{ Dir string; Targets []string; Apply, IncludeTests bool; ExcludeDirs []string; ExecCommand func(...) *exec.Cmd }`
- `align.Run(ctx, o, log *logx.Logger) (*Report, error)`；`Report{ Diagnostics int; Applied bool; Output string }`
- CLI：`yd align [--apply] [--test] [--exclude-dir d]... [--bin path] [targets...]`，默认 `./...`
- 缺失工具时返回哨兵 `align.ErrToolNotFound` 并打印安装提示

- [ ] 步骤：测试参数拼装与二进制解析顺序（注入 `ExecCommand`）；随后对本仓库执行 `betteralign -apply ./...` 并提交字段重排。

### Task 4: 前端 i18n 与主题切换闭环 + 后台管理页面

**Files:** Modify `web/templates/**`、`web/static/js/{yd-i18n.js,yd-app.js}`、`web/static/css/app.css`；Create `web/i18n/{zh-CN.yaml,en.yaml}`

**要点:**
- 语言切换：顶栏下拉 → `ydI18n.setLang` → **htmx 局部重取** `#yd-workspace`（不整页刷新）→ 持久化 localStorage + cookie + `<html lang>`
- 主题切换：light/dark/system 三态 → `data-theme` + localStorage + cookie，切换即时生效且无闪烁
- 管理页面：租户、用户、角色、权限四个模块，htmx 片段式 CRUD，422 校验回显
- 目录覆盖全部 `tr "..."` 键（中英各一份）

### Task 5: 脚手架同步

**Files:** Modify `scaffold/templates/_name_/**`

**要点:** `yd new` 生成的默认项目必须包含 Task 1–4 的能力：`internal/{controller,service,repository,model}` 下的 tenant/user/role 模块、登录页与鉴权中间件装配、i18n 与主题切换的完整前端、`yd align` 的使用说明写入生成的 `AGENTS.md`/`CLAUDE.md`。

### Task 6: 规范同步与验收

- `CLAUDE.md` / `AGENTS.md` / `docs/developer_guidelines.md` 增补：多租户与 RBAC 约束、每用户 sec_key 规则、`betteralign -apply ./...` 提交前要求、前端 i18n/主题约定
- 扩展 `internal/repotest` 守卫：断言上述约束已写入文档
- 端到端：`yd new` → 登录 → 带令牌访问受保护端点 → 跨租户越权被拒 → 语言/主题切换 → 编译与交叉编译
