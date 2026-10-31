//go:build e2e

// Package e2e runs the full yd lifecycle against a real scaffolded project. | Package e2e 在真实脚手架产物上跑通 yd 的完整生命周期。
//
// These tests are behind the "e2e" build tag because they compile, start and
// stop real processes; run them with: | 这些测试位于 "e2e" 构建标签之后，因为它们会编译并启停真实进程；
// 运行方式：
//
//	go test -tags e2e ./e2e/ -v -timeout 30m
package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	// 测试进程直接读生成项目的 SQLite 库：驱动是 GORM 方言包直接依赖的那个纯 Go SQLite，导入它即完成注册。
	// The test process reads the generated project's SQLite database directly: the driver is the pure-Go SQLite the GORM
	// dialect depends on, and importing it performs the registration.
	_ "github.com/glebarez/sqlite"
	"github.com/shibingli/yd/security/authz"
	"github.com/shibingli/ydx/base64"
	"github.com/shibingli/ydx/guomi"
	"github.com/shibingli/ydx/json"
	"github.com/shibingli/ydx/regexpx"
)

// repoRoot returns the repository root directory. | repoRoot 返回仓库根目录。
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(wd)
}

// buildYD compiles the yd CLI into a temporary binary. | buildYD 把 yd CLI 编译到临时二进制。
//
// Running the built binary (instead of `go run`) lets every call choose its own
// working directory: the CLI resolves relative paths against the process cwd, so
// `yd new` must run inside the directory that should receive the project. | 运行编译好的
// 二进制（而不是 `go run`）才能让每次调用自选工作目录：CLI 以进程 cwd 解析相对路径，
// 因此 `yd new` 必须运行在希望接收项目的目录内。
func buildYD(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "yd")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/yd")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译 yd CLI 失败: %v\n%s", err, out)
	}
	return bin
}

// runYD runs a built yd binary in the given working directory. | runYD 在指定工作目录运行已编译的 yd 二进制。
func runYD(t *testing.T, bin, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestScaffoldBuildRun is the end-to-end acceptance walkthrough. | TestScaffoldBuildRun 是端到端验收走查。
func TestScaffoldBuildRun(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	project := filepath.Join(tmp, "demo")

	bin := buildYD(t)
	out, err := runYD(t, bin, tmp, "new", "demo",
		"--module", "example.com/demo",
		"--db", "sqlite",
		"--i18n", "--htmx-ext", "sse,ws,preload",
		"--local-replace", root,
		"--yes",
	)
	t.Logf("yd new 输出:\n%s", out)
	if err != nil {
		t.Fatalf("yd new 失败: %v\n%s", err, out)
	}

	t.Run("生成物齐全", func(t *testing.T) {
		required := []string{
			"go.mod", "LICENSE", "README.md", "Dockerfile",
			".gitignore", ".gitattributes", ".env.template", ".yd.json",
			"CLAUDE.md", "AGENTS.md",
			"configs/config.yaml",
			"data/.gitkeep",
			"cmd/app/main.go",
			"docs/developer_guidelines.md",
			"web/assets.lock.json",
		}
		for _, rel := range required {
			if _, err := os.Stat(filepath.Join(project, rel)); err != nil {
				t.Errorf("生成物缺失 %s: %v", rel, err)
			}
		}
		for _, rel := range []string{"internal", "web/templates", "web/static"} {
			if _, err := os.Stat(filepath.Join(project, rel)); err != nil {
				t.Errorf("生成目录缺失 %s: %v", rel, err)
			}
		}
		gi, err := os.ReadFile(filepath.Join(project, ".gitignore"))
		if err != nil {
			t.Fatalf("读取 .gitignore 失败: %v", err)
		}
		for _, want := range []string{"bin/", "dist/", ".env", "node_modules/", ".tailwindcss/"} {
			if !strings.Contains(string(gi), want) {
				t.Errorf(".gitignore 缺少规范行 %q", want)
			}
		}
	})

	t.Run("生成项目可编译", func(t *testing.T) {
		cmd := exec.Command("go", "build", "./...")
		cmd.Dir = filepath.Join(project)
		// 生成的项目是独立模块，go.sum 需在首次构建时补齐；同时禁用工作区，
		// 避免它意外加入本仓库的 go.work。
		// The generated project is its own module: go.sum must be completed on the
		// first build, and the workspace must be disabled so it never joins ours.
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("生成项目编译失败: %v\n%s", err, out)
		}
	})

	// 生成的项目内置多租户鉴权：未登录时不允许看到工作区，
	// 受保护端点的 htmx 请求也不得返回任何数据。
	// The generated project ships multi-tenant auth: without a token the
	// workspace must stay hidden and htmx requests to protected endpoints must
	// not return any data. Authenticated flows (SM2 login, fragment rendering,
	// cross-tenant denial) are asserted in depth by
	// scaffold.TestGeneratedAppSupportsAuth.
	t.Run("未登录被拦截且登录页可达", func(t *testing.T) {
		addr, stop := startApp(t, project)
		defer stop()

		// 不跟随重定向，直接观察服务端的安全决策。
		// Do not follow redirects: assert the server's own security decision.
		full := httpGetRaw(t, "http://"+addr+"/", nil)
		if full.status >= 300 && full.status < 400 {
			if loc := full.header.Get("Location"); !strings.Contains(loc, "/login") {
				t.Errorf("未登录访问 / 应重定向到登录页，实际 Location=%q", loc)
			}
		} else if full.status != http.StatusOK {
			t.Errorf("未登录访问 / 的状态 = %d, 期望 3xx 重定向或 200 登录页", full.status)
		}
		// Whichever branch is taken, the response must not carry an element only a signed-in page has. | 无论走哪条分支，响应里都不得出现已登录才有的元素。
		if strings.Contains(full.body, `hx-post="/logout"`) {
			t.Errorf("未登录响应中出现了登出入口: 前 200 字节=%q", head(full.body, 200))
		}

		login := httpGet(t, "http://"+addr+"/login", nil)
		if login.status != http.StatusOK {
			t.Fatalf("登录页状态 = %d", login.status)
		}
		if !strings.Contains(login.body, `type="password"`) {
			t.Errorf("登录页缺少口令输入框: 前 200 字节=%q", head(login.body, 200))
		}
		if strings.Contains(strings.ToLower(login.body), "cdn.") ||
			regexpx.MustCompile(`(src|href)="http`).MatchString(login.body) {
			t.Error("the sign-in page references an external CDN (a zero-CDN violation) | 登录页引用了外部 CDN（违反零 CDN 约束）")
		}

		// An htmx request to a protected endpoint must not answer 200 while signed out. | 受保护端点的 htmx 请求在未登录时不得返回 200。
		partial := httpGet(t, "http://"+addr+"/mgr/users", map[string]string{"HX-Request": "true"})
		if partial.status == http.StatusOK {
			t.Errorf("未登录的 htmx 请求不应成功: 状态=%d 前 120 字节=%q", partial.status, head(partial.body, 120))
		}
	})

	t.Run("OpenAPI 文档可访问", func(t *testing.T) {
		addr, stop := startApp(t, filepath.Join(project))
		defer stop()

		doc := httpGet(t, "http://"+addr+"/api/v1/openapi.json", nil)
		if doc.status != http.StatusOK {
			t.Fatalf("/api/v1/openapi.json 状态 = %d", doc.status)
		}
		if !strings.Contains(doc.body, `"3.0`) {
			t.Errorf("OpenAPI 版本字段缺失: %q", head(doc.body, 200))
		}

		viewer := httpGet(t, "http://"+addr+"/mgr/docs", nil)
		if viewer.status != http.StatusOK {
			t.Fatalf("/mgr/docs 状态 = %d", viewer.status)
		}
		if regexpx.MustCompile(`(src|href)="http`).MatchString(viewer.body) {
			t.Error("the /docs viewer references an external CDN (a zero-CDN violation) | /docs 查看器引用了外部 CDN（违反零 CDN 约束）")
		}
		if !strings.Contains(viewer.body, "/openapi/assets/yd-openapi-viewer.js") {
			t.Error("the viewer does not load the embedded viewer script | 查看器未加载内嵌查看器脚本")
		}

		script := httpGet(t, "http://"+addr+"/openapi/assets/yd-openapi-viewer.js", nil)
		if script.status != http.StatusOK {
			t.Fatalf("viewer script status = %d, want 200 | 查看器脚本状态 = %d，期望 200", script.status, script.status)
		}
		if ct := script.header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Errorf("viewer script content-type = %q | 查看器脚本 Content-Type = %q", ct, ct)
		}
	})

	t.Run("静态资产本地可用", func(t *testing.T) {
		addr, stop := startApp(t, filepath.Join(project))
		defer stop()

		for _, p := range []string{"/static/js/vendored/htmx.min.js", "/static/css/yd.css"} {
			res := httpGet(t, "http://"+addr+p, nil)
			if res.status != http.StatusOK || len(res.body) == 0 {
				t.Errorf("静态资产 %s 不可用: 状态=%d 长度=%d", p, res.status, len(res.body))
			}
		}
	})
}

// 生成项目的种子凭据与会话 cookie 名（与 internal/meta 同源）：端到端探针按默认凭据登录，
// 再以管理员身份走真实的建租户、建角色、建用户端点。
// The generated project's seed credentials and session cookie name (same source as internal/meta): the probe signs in with
// the default credentials and then creates a tenant, a role and a user through the real endpoints as the administrator.
const (
	// seedTenant 是种子写出的唯一租户。 | seedTenant is the only tenant the seed writes.
	seedTenant = "default"
	// seedAdmin 是该租户的管理员登录名。 | seedAdmin is that tenant's administrator sign-in name.
	seedAdmin = "admin"
	// seedAdminPassword 是未配置 seed.admin_password 时开发环境使用的那一枚文档化口令（internal/meta.DevSeedAdminPassword）。
	// seedAdminPassword is the documented password development falls back to while seed.admin_password is unset
	// (internal/meta.DevSeedAdminPassword).
	seedAdminPassword = "admin123"
	// probeUserPassword 是探针自建账号的口令，长度满足口令强度下限。 | probeUserPassword is the created account's password, above the strength floor.
	probeUserPassword = "Probe@123456"
	// e2eMasterKey 是生产场景注入的 32 字节测试主密钥（公开测试值，不是真实密钥）。
	// e2eMasterKey is the 32-byte test master key injected in production scenarios (a public test value, not a secret).
	e2eMasterKey = "yd-e2e-test-master-key-0123456789"
	// sessionCookie 是会话 cookie 名。 | sessionCookie is the session cookie name.
	sessionCookie = "yd_token"
)

// TestAuthDataSurvivesRestart 端到端验证鉴权数据真的落库：建租户、建角色、建用户之后杀掉进程，
// 重启同一份产物，三样数据都还在，且新建的账号仍旧能登录。
// TestAuthDataSurvivesRestart verifies end to end that auth data really lands in the database: after creating a tenant, a
// role and a user the process is killed, and on the restarted artifact all three are still there — and the created
// account can still sign in.
//
// 这段走查咬住的正是「内存实现做不到」的那一点：跨进程仍可见的租户、角色与账号只可能来自数据库，
// 而账号能继续登录还额外证明了每用户 sec_key 也已入库并在新进程里被成功解密。
// The walkthrough bites exactly on what the in-memory implementation cannot do: a tenant, a role and an account that
// survive the process can only come from the database, and the account signing in again additionally proves that its
// per-user sec_key was stored too and decrypted successfully in the fresh process.
func TestAuthDataSurvivesRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过重启持久化验证 | skipped in short mode")
	}

	root := repoRoot(t)
	tmp := t.TempDir()
	project := filepath.Join(tmp, "persist")

	bin := buildYD(t)
	out, err := runYD(t, bin, tmp, "new", "persist",
		"--module", "example.com/persist",
		"--db", "sqlite",
		"--local-replace", root,
		"--yes",
	)
	t.Logf("yd new 输出:\n%s", out)
	if err != nil {
		t.Fatalf("yd new 失败: %v\n%s", err, out)
	}

	// 库文件落在 data/ 下（configs/config.yaml 的 db.dsn 指向它），生成时不应存在。
	// The database file lands in data/ (db.dsn in configs/config.yaml points at it) and must not exist right after generation.
	dbFile := filepath.Join(project, "data", "persist.db")
	if _, err := os.Stat(dbFile); err == nil {
		t.Fatalf("刚生成的项目不应已有库文件: %s", dbFile)
	}

	const (
		probeTenantSlug = "restart-probe"
		probeTenantName = "Restart probe"
		probeRoleName   = "restart-auditor"
		probeUserName   = "restart-user"
		probeUserEmail  = "restart-user@example.com"
	)

	// 第一段进程：登录 → 建租户 → 建角色 → 建用户。
	// First process: sign in, then create a tenant, a role and a user.
	addr, logs, stop := startAppEnv(t, project, nil)
	base := "http://" + addr
	client := newSessionClient(t)
	signIn(t, client, base, seedTenant, seedAdmin, seedAdminPassword)

	createTenant(t, client, base, probeTenantSlug, probeTenantName)
	createRole(t, client, base, probeRoleName)
	createUser(t, client, base, probeUserName, probeUserEmail, "member")
	stop()

	// 落库的两个直接证据：磁盘上有库文件，启动日志说明走的是数据库而不是内存。
	// Two direct proofs of persistence: the file is on disk and the startup log says the data went to the database.
	info, err := os.Stat(dbFile)
	if err != nil {
		t.Fatalf("启动后仍未落库 %s: %v\n%s", dbFile, err, logs)
	}
	if info.Size() == 0 {
		t.Fatalf("库文件为空: %s", dbFile)
	}
	t.Logf("库文件 %s 大小 %d 字节", filepath.Base(dbFile), info.Size())
	if !strings.Contains(logs.String(), "auth data is stored in the database") {
		t.Errorf("启动日志未说明鉴权数据已入库:\n%s", logs)
	}
	if strings.Contains(logs.String(), "lives in memory") {
		t.Errorf("配了数据库的项目不应降级到内存实现:\n%s", logs)
	}

	// 第二段进程：同一份产物、同一个工作目录，只换了一个进程。
	// Second process: the same artifact in the same working directory, only the process changed.
	addr2, logs2, stop2 := startAppEnv(t, project, nil)
	defer stop2()
	base2 := "http://" + addr2
	client2 := newSessionClient(t)
	signIn(t, client2, base2, seedTenant, seedAdmin, seedAdminPassword)

	t.Run("租户重启后仍在", func(t *testing.T) {
		fragment := hxGet(t, client2, base2+"/mgr/tenants")
		for _, want := range []string{probeTenantSlug, probeTenantName} {
			if !strings.Contains(fragment, want) {
				t.Errorf("重启后的租户列表缺少 %q:\n%s", want, fragment)
			}
		}
	})

	t.Run("角色重启后仍在", func(t *testing.T) {
		fragment := hxGet(t, client2, base2+"/mgr/roles")
		if !strings.Contains(fragment, probeRoleName) {
			t.Errorf("重启后的角色列表缺少 %q:\n%s", probeRoleName, fragment)
		}
	})

	t.Run("用户重启后仍在且仍能登录", func(t *testing.T) {
		fragment := hxGet(t, client2, base2+"/mgr/users")
		if !strings.Contains(fragment, probeUserEmail) {
			t.Errorf("重启后的用户列表缺少 %q:\n%s", probeUserEmail, fragment)
		}

		// 新建账号在新进程里登录成功：它的 sec_key 也已入库，且被新进程的主密钥成功解开。
		// The created account signs in inside the new process: its sec_key was stored too and the fresh process's master key opens it.
		fresh := newSessionClient(t)
		if token := signIn(t, fresh, base2, seedTenant, probeUserName, probeUserPassword); token == "" {
			t.Error("the created account signed in after the restart but no session token came back | 重启后新建账号登录成功但没拿到会话令牌")
		}
	})

	if strings.Contains(logs2.String(), "lives in memory") {
		t.Errorf("重启时不应降级到内存实现:\n%s", logs2)
	}
}

// seedCounts 是种子数据在一次初始化后应当留下的行数快照。 | seedCounts is the row-count snapshot one seed initialisation leaves behind.
type seedCounts struct {
	// tenants is the row count of yd_auth_tenants. | tenants 是 yd_auth_tenants 的行数。
	tenants int
	// users is the row count of yd_auth_users. | users 是 yd_auth_users 的行数。
	users int
	// roles is the row count of yd_auth_roles. | roles 是 yd_auth_roles 的行数。
	roles int
	// rolePerms 是 yd_auth_role_permissions 的行数：角色→能力绑定每个一条。 | rolePerms is the row count of yd_auth_role_permissions, one per role-to-capability binding.
	rolePerms int
}

// String 让失败信息直接可读。 | String makes the failure message readable at a glance.
func (c seedCounts) String() string {
	return fmt.Sprintf("租户=%d 账号=%d 角色=%d 角色权限=%d", c.tenants, c.users, c.roles, c.rolePerms)
}

// seedTenantTotal is the number of tenants the seed writes: the single default tenant.
// seedTenantTotal 是种子数据写出的租户数：唯一一个默认租户。
const seedTenantTotal = 1

// expectedSeedCounts 从框架注册表推导期望行数，使期望值不可能与种子数据的真源漂移：
// 角色数取自 authz.Roles，角色→能力绑定数取自 authz.BuiltinGrants。
// expectedSeedCounts derives the expected row counts from the framework registry, so the expectation cannot drift away
// from the seed's own source of truth: the role count comes from authz.Roles and the binding count from
// authz.BuiltinGrants.
func expectedSeedCounts() seedCounts {
	bindings := 0
	for _, grant := range authz.BuiltinGrants() {
		bindings += len(grant.Permissions)
	}
	return seedCounts{
		tenants:   seedTenantTotal,
		users:     seedTenantTotal,
		roles:     seedTenantTotal * len(authz.Roles()),
		rolePerms: seedTenantTotal * bindings,
	}
}

// TestSeedWritesDefaultAuthDataOnce 端到端验证默认（种子）数据的完整生命周期：首次启动写入什么、种子管理员能不能真的登录、
// 进程重启后数据是否仍在，以及再启动一次是否一行都不多写。
// TestSeedWritesDefaultAuthDataOnce walks the whole life of the default (seed) data end to end: what the first start
// writes, whether the seeded administrator can really sign in, whether the data is still there once the process is
// replaced, and whether one more start writes not a single extra row.
//
// 这一路走查把「种子数据取自注册表」变成可计数的证据：租户、账号、角色与角色→能力绑定的行数必须与
// authz.BuiltinRoles / authz.BuiltinGrants 推导出的期望完全一致，而 4 个角色与注册表里的全部能力又必须真的出现在管理员读到的页面上。
// The walkthrough turns "the seed takes its data from the registry" into countable evidence: the rows of tenants,
// accounts, roles and role-to-capability bindings must match exactly what authz.BuiltinRoles / authz.BuiltinGrants imply,
// and the four roles plus every registered capability have to show up on the pages the administrator reads.
func TestSeedWritesDefaultAuthDataOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过种子数据验证 | skipped in short mode")
	}

	root := repoRoot(t)
	tmp := t.TempDir()
	project := filepath.Join(tmp, "seeded")

	bin := buildYD(t)
	out, err := runYD(t, bin, tmp, "new", "seeded",
		"--module", "example.com/seeded",
		"--db", "sqlite",
		"--local-replace", root,
		"--yes",
	)
	if err != nil {
		t.Fatalf("yd new 失败: %v\n%s", err, out)
	}
	t.Logf("yd new 输出:\n%s", head(out, 600))

	// 库文件落地位置与 configs/config.yaml 的 db.dsn 一致，生成时还不存在。
	// The database file lands where db.dsn points and does not exist right after generation.
	dbFile := filepath.Join(project, "data", "seeded.db")
	if _, err := os.Stat(dbFile); err == nil {
		t.Fatalf("刚生成的项目不应已有库文件: %s", dbFile)
	}

	// 第一段进程：只启动，什么都不做——种子数据在监听端口之前就已写入。
	// First process: it only starts. The seed is written before the socket is listening.
	addr, logs, stop := startAppEnv(t, project, nil)
	base := "http://" + addr
	client := newSessionClient(t)

	// 种子管理员真的能登录：口令是配置下发的那一枚（这里是开发回退），登录接口不接受任何明文口令。
	// The seeded administrator really signs in, with the password the configuration hands down (here the development
	// fallback); the endpoint accepts no clear-text password at all.
	token := signIn(t, client, base, seedTenant, seedAdmin, seedAdminPassword)
	if token == "" {
		t.Error("the seeded administrator signed in but no session token came back | 种子管理员登录成功后没拿到会话令牌")
	}
	t.Logf("种子管理员 %s@%s 登录成功，会话令牌长度 %d", seedAdmin, seedTenant, len(token))

	t.Run("种子管理员带着注册表里的全部能力", func(t *testing.T) {
		// owner holds every registered capability, so all four guarded pages must open for it (200, not 403). |
		// owner 拿的是注册表里的全部能力，因此四个受守卫的页面都必须对它开着（200 而不是 403）。
		for _, path := range []string{"/mgr/tenants", "/mgr/users", "/mgr/roles", "/mgr/permissions"} {
			if fragment := hxGet(t, client, base+path); len(fragment) == 0 {
				t.Errorf("GET %s 返回了空片段", path)
			}
		}
		// 反证：页面上的能力确实来自注册表——4 个内置角色与注册表里的全部能力都出现在管理员读到的片段里。
		// The other direction: the capabilities on the pages really come from the registry — all four built-in roles and
		// every registered capability appear in what the administrator reads.
		roles := hxGet(t, client, base+"/mgr/roles")
		for _, role := range authz.Roles() {
			if !strings.Contains(roles, role.Name) {
				t.Errorf("角色页缺少内置角色 %q", role.Name)
			}
		}
		matrix := hxGet(t, client, base+"/mgr/permissions")
		for _, perm := range authz.Permissions() {
			if !strings.Contains(matrix, perm.Name) {
				t.Errorf("权限矩阵页缺少能力 %q", perm.Name)
			}
		}
	})

	t.Run("开发回退每次启动都告警且日志里没有口令明文", func(t *testing.T) {
		warn := logLines(logs.String(), "documented development password")
		if strings.Contains(warn, "no matching line") {
			t.Fatalf("未配置 seed.admin_password 时缺少开发回退告警:\n%s", logs)
		}
		t.Logf("开发回退告警:\n%s", warn)
		// 口令明文绝不进日志：告警只说「用的是哪一个来源」（配置键与环境变量名），从不说口令是什么。
		// The clear text never reaches a log: the warning names the source (the configuration key and the environment
		// variable) and never what the password is.
		if strings.Contains(logs.String(), seedAdminPassword) {
			t.Errorf("启动日志里出现了口令明文 %q:\n%s", seedAdminPassword, logs)
		}
	})
	stop()

	counts := readSeedCounts(t, dbFile)
	want := expectedSeedCounts()
	if counts != want {
		t.Fatalf("首次启动后的行数 = %s, 期望 %s", counts, want)
	}
	t.Logf("首次启动写入的行数 | rows written by the first start: %s", counts)
	assertSeedAccounts(t, dbFile)

	// 第二段进程：同一份产物、同一个工作目录，只换了进程。数据必须还在，种子管理员必须照旧登录。
	// Second process: the same artefact in the same working directory, only the process changed. The data has to be
	// there and the seeded administrator has to sign in as before.
	addr2, logs2, stop2 := startAppEnv(t, project, nil)
	base2 := "http://" + addr2
	client2 := newSessionClient(t)
	if token := signIn(t, client2, base2, seedTenant, seedAdmin, seedAdminPassword); token == "" {
		t.Error("the seeded administrator failed to sign in after the restart | 重启后种子管理员登录失败")
	}
	if fragment := hxGet(t, client2, base2+"/mgr/roles"); !strings.Contains(fragment, authz.RoleOwner) {
		t.Errorf("重启后的角色页缺少所有者角色 %q", authz.RoleOwner)
	}
	stop2()

	if after := readSeedCounts(t, dbFile); after != counts {
		t.Errorf("重启后的行数 = %s, 期望与首次启动一致 %s", after, counts)
	}
	// 幂等：重启不该再写一次种子——那一行「已创建种子管理员」只允许在第一次出现。
	// Idempotence: a restart must not seed again — the "seeded administrator created" line may appear on the first start
	// only.
	if lines := logLines(logs2.String(), seededAdminLine); !strings.Contains(lines, "no matching line") {
		t.Errorf("重启又写了一次种子数据:\n%s", lines)
	}

	// 第三段进程：再启动一次。行数必须一模一样，且同样不再创建任何账号。
	// Third process: one more start. The row counts must be identical and no account may be created again either.
	_, logs3, stop3 := startAppEnv(t, project, nil)
	stop3()

	after := readSeedCounts(t, dbFile)
	if after != counts {
		t.Errorf("再启动一次后的行数 = %s, 期望 %s", after, counts)
	}
	if lines := logLines(logs3.String(), seededAdminLine); !strings.Contains(lines, "no matching line") {
		t.Errorf("再启动一次又写了一次种子数据:\n%s", lines)
	}
	t.Logf("三次启动后的行数一致 | identical row counts across three starts: %s", after)
}

// seededAdminLine 是「已创建种子管理员」那句日志的英文半句：它的出现次数就是种子数据被写入的次数。
// seededAdminLine is the English half of the "seeded administrator created" log line: how often it appears is how often
// the seed wrote anything.
const seededAdminLine = "seeded administrator created"

// TestSeedRefusesWithoutPasswordOutsideDevelopment 端到端验证生产安全：非 development 环境未配置口令时
// 一个租户、一个管理员都不创建并明确告警，而注入口令后按注入的那一枚初始化；两种情况下口令明文都不进日志。
// TestSeedRefusesWithoutPasswordOutsideDevelopment verifies production safety end to end: outside development an unset
// password creates neither a tenant nor an administrator and warns about it, while an injected password seeds with exactly
// that password; in neither case does the clear text reach a log line.
func TestSeedRefusesWithoutPasswordOutsideDevelopment(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过生产安全验证 | skipped in short mode")
	}

	root := repoRoot(t)
	tmp := t.TempDir()
	project := filepath.Join(tmp, "prodseed")

	bin := buildYD(t)
	out, err := runYD(t, bin, tmp, "new", "prodseed",
		"--module", "example.com/prodseed",
		"--db", "sqlite",
		"--local-replace", root,
		"--yes",
	)
	if err != nil {
		t.Fatalf("yd new 失败: %v\n%s", err, out)
	}
	dbFile := filepath.Join(project, "data", "prodseed.db")

	// 第一段：声明自己不是 development 且不配置种子口令。应用必须照常启动，但库里不能有一个租户、一个账号。
	// First run: it declares itself something other than development and configures no seed password. The application must
	// start all the same, while the database keeps not a single tenant and not a single account.
	// Outside development the scaffold refuses to start without auth.master_key (the documented
	// development key must never seal production sec_keys), so the production runs inject one.
	// | 非开发环境下缺少 auth.master_key 时脚手架拒绝启动（文档化的开发密钥绝不能用于封装生产
	// sec_key），因此生产场景注入一枚测试主密钥。
	// 第一段用短别名 prod 启动：它必须与 production 一样触发主密钥校验、种子拒绝与安全 Cookie。
	// The first run starts with the short alias prod: it must trigger the same master-key check, seed refusal and
	// secure cookies as production.
	addr, logs, stop := startAppEnv(t, project, []string{"YD_APP_ENV=prod", "YD_AUTH_MASTER_KEY=" + e2eMasterKey})
	base := "http://" + addr

	// 未初始化的库没有任何账号可以登录：文档化的默认凭据必须被拒。
	// An uninitialised database has no account to sign in with: the documented default credentials must be refused.
	anon := newSessionClient(t)
	form := url.Values{
		"tenant":   {seedTenant},
		"username": {seedAdmin},
		"password": {sm2Cipher(t, anon, base, seedAdminPassword)},
	}
	resp, body, err := request(t, anon, http.MethodPost, base+"/login", form.Encode(), map[string]string{
		"Content-Type":   "application/x-www-form-urlencoded",
		"X-YD-Encrypted": "sm2",
	})
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("非 development 且未初始化时用默认凭据登录状态码 = %d, 期望 401\n%s", resp.StatusCode, head(body, 200))
	}

	warn := logLines(logs.String(), "no default tenant and no administrator are created")
	if strings.Contains(warn, "no matching line") {
		t.Fatalf("拒绝初始化时缺少告警:\n%s", logs)
	}
	t.Logf("拒绝初始化的告警:\n%s", warn)
	if strings.Contains(logs.String(), seedAdminPassword) {
		t.Errorf("启动日志里出现了口令明文 %q:\n%s", seedAdminPassword, logs)
	}
	stop()

	if _, err := os.Stat(dbFile); err != nil {
		t.Fatalf("应用启动后库文件仍不存在: %v", err)
	}
	if got := readSeedCounts(t, dbFile); got != (seedCounts{}) {
		t.Fatalf("非 development 且未配置口令时不应写入任何种子数据，实际 = %s", got)
	}
	t.Log("拒绝初始化后四张表都是空的 | all four tables are empty after the refusal")

	// 第二段：同一份产物、同一个进程环境，只多注入一枚口令。初始化按注入的那一枚进行，开发回退的口令依然进不来。
	// Second run: the same artefact in the same environment with one more thing — an injected password. Seeding uses
	// exactly that password, and the development fallback still gets nobody in.
	injected := "Probe@Prod#2026"
	addr2, logs2, stop2 := startAppEnv(t, project, []string{"YD_APP_ENV=production", "YD_AUTH_MASTER_KEY=" + e2eMasterKey, "YD_SEED_ADMIN_PASSWORD=" + injected})
	base2 := "http://" + addr2

	client2 := newSessionClient(t)
	if token := signIn(t, client2, base2, seedTenant, seedAdmin, injected); token == "" {
		t.Error("the seeded administrator failed to sign in after YD_SEED_ADMIN_PASSWORD was injected | 注入 YD_SEED_ADMIN_PASSWORD 后种子管理员登录失败")
	}
	// 反证：用开发回退的那一枚口令登录必须被拒——生产环境里落库的口令就是注入的那一枚。
	// The other direction: signing in with the development fallback must be refused — the password stored in production is
	// the injected one.
	fallback := newSessionClient(t)
	fallbackForm := url.Values{
		"tenant":   {seedTenant},
		"username": {seedAdmin},
		"password": {sm2Cipher(t, fallback, base2, seedAdminPassword)},
	}
	if resp, _, err := request(t, fallback, http.MethodPost, base2+"/login", fallbackForm.Encode(), map[string]string{
		"Content-Type":   "application/x-www-form-urlencoded",
		"X-YD-Encrypted": "sm2",
	}); err != nil {
		t.Fatalf("登录请求失败: %v", err)
	} else if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("注入口令后仍可用开发回退口令登录: 状态码 = %d, 期望 401", resp.StatusCode)
	}
	stop2()

	if got, want := readSeedCounts(t, dbFile), expectedSeedCounts(); got != want {
		t.Errorf("注入口令后的行数 = %s, 期望 %s", got, want)
	}
	// 注入的口令与开发回退的口令都不得出现在日志里。
	// Neither the injected password nor the development fallback may appear in a log.
	for _, secret := range []string{injected, seedAdminPassword} {
		if strings.Contains(logs2.String(), secret) {
			t.Errorf("启动日志里出现了口令明文 %q:\n%s", secret, logs2)
		}
	}
	assertSeedAccounts(t, dbFile)
}

// assertSeedAccounts 直接读库核对种子账号：唯一 default 租户、一个 admin 管理员、管理员带着所有者角色，
// 而口令只以 SM3-PBKDF2 散列落库。
// assertSeedAccounts reads the database and checks the seeded account: one default tenant, one admin administrator holding
// the owner role, and the password stored as an SM3-PBKDF2 hash only.
func assertSeedAccounts(t *testing.T, dbFile string) {
	t.Helper()

	db := openSeedDB(t, dbFile)

	slugs := map[string]bool{}
	rows, err := db.Query(`SELECT slug FROM yd_auth_tenants`)
	if err != nil {
		t.Fatalf("读取 yd_auth_tenants 失败: %v", err)
	}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			t.Fatalf("扫描租户标识失败: %v", err)
		}
		slugs[slug] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历租户失败: %v", err)
	}
	_ = rows.Close()
	for _, want := range []string{seedTenant} {
		if !slugs[want] {
			t.Errorf("yd_auth_tenants 缺少种子租户 %q", want)
		}
	}

	// 唯一管理员，且角色绑定落在成员关系表上：用户是全局身份，角色按租户存放在
	// yd_auth_user_tenants。 | The single administrator, with the role binding stored on the membership:
	// users are global identities and roles live per tenant in yd_auth_user_tenants.
	admins := map[string]string{seedAdmin: seedTenant}
	seen := map[string]bool{}
	userRows, err := db.Query(`SELECT u.username, t.slug, m.roles, u.password_hash
		FROM yd_auth_users u
		JOIN yd_auth_user_tenants m ON m.user_id = u.id
		JOIN yd_auth_tenants t ON t.id = m.tenant_id`)
	if err != nil {
		t.Fatalf("读取 yd_auth_users 与 yd_auth_user_tenants 失败: %v", err)
	}
	for userRows.Next() {
		var username, tenant, roles, hash string
		if err := userRows.Scan(&username, &tenant, &roles, &hash); err != nil {
			t.Fatalf("扫描账号失败: %v", err)
		}
		wantTenant, want := admins[username]
		if !want || tenant != wantTenant {
			t.Errorf("yd_auth_user_tenants 里出现了非种子账号 %q @ %q", username, tenant)
			continue
		}
		seen[username] = true
		t.Logf("种子账号 %s @ %s 的绑定角色列 = %s", username, tenant, roles)

		var bound []string
		if err := json.Unmarshal([]byte(roles), &bound); err != nil {
			t.Errorf("账号 %s 的 roles 列不是 JSON 文本: %v（%q）", username, err, roles)
			continue
		}
		if !slices.Contains(bound, authz.RoleOwner) {
			t.Errorf("账号 %s 的角色 = %v, 期望含 %q", username, bound, authz.RoleOwner)
		}
		if hash == "" || !strings.HasPrefix(hash, guomi.PasswordPrefix) {
			t.Errorf("账号 %s 的口令散列 = %q, 期望以 %q 开头", username, head(hash, 16), guomi.PasswordPrefix)
		}
		for _, secret := range []string{seedAdminPassword, "Probe@Prod#2026"} {
			if strings.Contains(hash, secret) {
				t.Errorf("账号 %s 的口令散列里出现了口令明文", username)
			}
		}
	}
	if err := userRows.Err(); err != nil {
		t.Fatalf("遍历账号失败: %v", err)
	}
	_ = userRows.Close()
	for username := range admins {
		if !seen[username] {
			t.Errorf("yd_auth_users 缺少种子账号 %q", username)
		}
	}
}

// openSeedDB 打开生成项目的 SQLite 库。驱动是 GORM 方言包直接依赖的那个纯 Go SQLite，因此这里不需要 cgo，
// 也不引入任何新依赖（库文件与三个应用进程共用同一份，读一行不会改变它）。
// openSeedDB opens the SQLite database of a generated project. The driver is the pure-Go SQLite the GORM dialect depends
// on directly, so this needs no cgo and adds no dependency (the file is shared with the three application processes, and
// reading a row never changes it).
func openSeedDB(t *testing.T, dbFile string) *sql.DB {
	t.Helper()

	if _, err := os.Stat(dbFile); err != nil {
		t.Fatalf("库文件不存在: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(dbFile))
	if err != nil {
		t.Fatalf("打开 %s 失败: %v", dbFile, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// readSeedCounts 统计四张鉴权表的行数。 | readSeedCounts counts the rows of the four authentication tables.
func readSeedCounts(t *testing.T, dbFile string) seedCounts {
	t.Helper()

	db := openSeedDB(t, dbFile)
	return seedCounts{
		tenants:   countRows(t, db, "yd_auth_tenants"),
		users:     countRows(t, db, "yd_auth_users"),
		roles:     countRows(t, db, "yd_auth_roles"),
		rolePerms: countRows(t, db, "yd_auth_role_permissions"),
	}
}

// countRows 统计一张表的行数：表名是固定的框架表名，不能参数化，因此直接拼进语句。
// countRows counts the rows of one table: the name is one of the framework's fixed table names and cannot be a bind
// parameter, so it is written straight into the statement.
func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()

	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("统计 %s 失败: %v", table, err)
	}
	return n
}

// TestAuthDegradesWithoutReachableDatabase 验证没有可用数据库时应用照常启动：鉴权退回内存实现并明确告警，
// 绝不因为连不上库就把启动挡在门外。
// TestAuthDegradesWithoutReachableDatabase verifies that a missing database never blocks startup: authentication falls back
// to the in-memory implementation with an explicit warning, and the application starts all the same.
func TestAuthDegradesWithoutReachableDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过降级验证 | skipped in short mode")
	}

	root := repoRoot(t)
	bin := buildYD(t)

	cases := []struct {
		name     string
		db       string
		env      []string
		wantWarn string
	}{
		{
			name:     "生成时未选择数据库",
			db:       "none",
			wantWarn: "generated without a database",
		},
		{
			name:     "数据库不可达",
			db:       "sqlite",
			env:      []string{"YD_DB_DSN=file:no-such-directory-yd/probe.db"},
			wantWarn: "database unavailable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			project := filepath.Join(tmp, "degrade")
			out, err := runYD(t, bin, tmp, "new", "degrade",
				"--module", "example.com/degrade",
				"--db", tc.db,
				"--local-replace", root,
				"--yes",
			)
			if err != nil {
				t.Fatalf("yd new 失败: %v\n%s", err, out)
			}

			addr, logs, stop := startAppEnv(t, project, tc.env)
			defer stop()

			// 起得来、能登录、能建数据：降级的代价只是「不持久」，不是「不可用」。
			// It starts, signs in and writes data: the price of degradation is persistence, never availability.
			base := "http://" + addr
			client := newSessionClient(t)
			signIn(t, client, base, seedTenant, seedAdmin, seedAdminPassword)
			createTenant(t, client, base, "memory-only", "Memory only")

			if !strings.Contains(logs.String(), tc.wantWarn) {
				t.Errorf("启动日志缺少降级告警 %q:\n%s", tc.wantWarn, logs)
			}
			t.Logf("降级告警:\n%s", logLines(logs.String(), tc.wantWarn))
		})
	}
}

// TestCrossCompile verifies cross-compilation for three targets. | TestCrossCompile 验证三个目标平台的交叉编译。
func TestCrossCompile(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()

	targets := []struct{ goos, goarch string }{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"windows", "amd64"},
	}
	for _, tg := range targets {
		t.Run(tg.goos+"/"+tg.goarch, func(t *testing.T) {
			name := "yd-app"
			if tg.goos == "windows" {
				name += ".exe"
			}
			dst := filepath.Join(outDir, name+"_"+tg.goos+"_"+tg.goarch)
			cmd := exec.Command("go", "build", "-trimpath", "-o", dst, "./cmd/yd")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+tg.goos, "GOARCH="+tg.goarch)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("交叉编译 %s/%s 失败: %v\n%s", tg.goos, tg.goarch, err, out)
			}
			fi, err := os.Stat(dst)
			if err != nil || fi.Size() == 0 {
				t.Fatalf("产物缺失或为空: %v", err)
			}
			if tg.goos == "linux" {
				b, _ := os.ReadFile(dst)
				if len(b) < 4 || b[0] != 0x7f || b[1] != 'E' || b[2] != 'L' || b[3] != 'F' {
					t.Error("the linux artifact is not an ELF | linux 产物不是 ELF")
				}
			}
			t.Logf("%s/%s → %s (%d 字节)", tg.goos, tg.goarch, filepath.Base(dst), fi.Size())
		})
	}
}

// TestAssetsVerifyOffline verifies offline asset verification. | TestAssetsVerifyOffline 验证离线资产校验。
func TestAssetsVerifyOffline(t *testing.T) {
	root := repoRoot(t)
	out, err := runYD(t, buildYD(t), root, "assets", "verify", "--offline")
	t.Logf("yd assets verify --offline 输出:\n%s", out)
	if err != nil {
		t.Fatalf("离线校验失败: %v", err)
	}
}

// ---------------------------------------------------------------- test helpers | 测试辅助

type httpResult struct {
	status int
	body   string
	header http.Header
}

// httpGet performs a GET request with optional extra headers. | httpGet 发起带可选附加头的 GET 请求。
func httpGet(t *testing.T, url string, hdr map[string]string) httpResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return httpResult{status: resp.StatusCode, body: string(b), header: resp.Header}
}

// httpGetRaw performs a GET without following redirects, so the caller can
// assert the server's own status code and Location header. | httpGetRaw 发起不跟随
// 重定向的 GET，使调用方能断言服务端自身给出的状态码与 Location 头。
func httpGetRaw(t *testing.T, url string, hdr map[string]string) httpResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return httpResult{status: resp.StatusCode, body: string(b), header: resp.Header}
}

// startApp builds and starts the generated application, returning its address. | startApp 编译并启动生成的应用，返回其地址。
func startApp(t *testing.T, dir string) (string, func()) {
	t.Helper()

	addr, _, stop := startAppEnv(t, dir, nil)
	return addr, stop
}

// appLogs 是并发安全的应用输出缓冲：用例可能在进程仍在运行时读取它，因此读写都要过同一把锁。
// appLogs is a concurrency-safe buffer of the application's output: a case may read it while the process is still
// running, so both sides take the same lock.
type appLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer. | Write 实现 io.Writer。
func (l *appLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// String returns everything captured so far. | String 返回目前捕获到的全部输出。
func (l *appLogs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// startAppEnv 在 startApp 的基础上追加环境变量并捕获进程输出（启动期日志本身就是被测契约的一部分：
// 「鉴权数据已存入数据库」与「回退到内存且不持久」两句都在其中）。
// startAppEnv extends startApp with extra environment variables and captured process output: the startup log is itself
// part of the contract under test — both "auth data is stored in the database" and the fall-back warning live in it.
func startAppEnv(t *testing.T, dir string, extraEnv []string) (string, *appLogs, func()) {
	t.Helper()

	port := freePort(t)
	bin := filepath.Join(t.TempDir(), "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	build := exec.Command("go", "build", "-o", bin, "./cmd/app")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("生成项目编译失败: %v\n%s", err, out)
	}

	logs := &appLogs{}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin)
	// Bound the wait for the child's stdout/stderr pipes. On Windows a console app can keep the
	// pipe open after the context cancels, and Wait would otherwise block forever. |
	// 限制等待子进程 stdout/stderr 管道的时间。Windows 上控制台程序可能在上下文取消后仍保持管道
	// 打开，否则 Wait 会永久阻塞。
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = dir
	// Disable HTTP/2 extended connect for the probes. On Windows app.Run re-execs itself to merge
	// GODEBUG=http2xconnect=1, and the wrapper/child pair defeats the single-process stop below:
	// the wrapper exits, the child keeps the port and database open. The probes do not exercise
	// extended connect, so switching it off keeps the process tree at one level. |
	// 探针关闭 HTTP/2 extended connect。Windows 上 app.Run 会为合并 GODEBUG=http2xconnect=1 而
	// 重新执行自身，包装/子进程对会让下面的单进程停止逻辑失效：包装进程退出、子进程继续占着端口
	// 与数据库。探针不验证 extended connect，因此关闭它可让进程树保持一层。
	cmd.Env = append(withoutServerEnv(os.Environ()),
		fmt.Sprintf("YD_SERVER_HOST=127.0.0.1"),
		fmt.Sprintf("YD_SERVER_PORT=%d", port),
		"YD_SERVER_HTTP2_EXTENDED_CONNECT=false",
		"YD_LOG_LEVEL=info",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("启动应用失败: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	stop := func() {
		cancel()
		// Kill the direct process explicitly: some Windows console hosts keep the process
		// handle alive long enough for Wait to block on the inherited pipes. |
		// 显式终止直接子进程：某些 Windows 控制台宿主会让进程句柄存活得足够久，导致 Wait 阻塞在
		// 继承的管道上。
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	// 用例提前失败（t.Fatalf）时也要把进程收掉：留下的进程会一直占着数据库文件，
	// TempDir 的清理随后就会以「文件被占用」失败，把真正的失败原因埋在噪音里。
	// The process is reaped even when the case fails early (t.Fatalf): a process left behind keeps the database file open
	// and the TempDir cleanup then fails with "the file is in use", burying the real cause in noise.
	t.Cleanup(stop)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return addr, logs, stop
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	t.Fatalf("应用未在 20 秒内监听 %s", addr)
	return "", logs, func() {}
}

// withoutServerEnv drops inherited YD_SERVER_* entries: on Unix the first duplicate wins, so an
// exported YD_SERVER_PORT would override the test's own value and the child would listen elsewhere.
// | withoutServerEnv 去掉继承的 YD_SERVER_* 条目：在 Unix 上第一个重复项胜出，导出的
// YD_SERVER_PORT 会覆盖测试自己的取值，子进程会监听别的端口。
func withoutServerEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "YD_SERVER_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// freePort asks the OS for a free TCP port. | freePort 向操作系统申请一个空闲 TCP 端口。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请端口失败: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// head truncates a string for readable failure messages. | head 截断字符串以便可读的失败信息。
func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---------------------------------------------------------- the session-carrying probe | 带会话的探针

// newSessionClient 返回带 cookie jar 的探针客户端：它承载登录会话、从不自行跟随重定向（303 与 Location 本身是被测契约），
// 并把语言固定为英文（数据断言与文案无关，固定语言只是为了失败信息稳定）。
// newSessionClient returns a probe client with a cookie jar: it carries the sign-in session, never follows a redirect on its
// own (the 303 and its Location are part of the contract under test) and pins the language to English (the data assertions
// do not depend on the copy; pinning the language only keeps failure messages stable).
func newSessionClient(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("构造 cookie jar 失败: %v", err)
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Jar:       jar,
		Transport: langTransport{},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// langTransport 给每条请求补上 Accept-Language: en（已有取值时不动）。
// langTransport appends Accept-Language: en to every request that does not carry one.
type langTransport struct{}

// RoundTrip implements http.RoundTripper. | RoundTrip 实现 http.RoundTripper。
func (langTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Accept-Language") == "" {
		req.Header.Set("Accept-Language", "en")
	}
	return http.DefaultTransport.RoundTrip(req)
}

// sm2Cipher 取服务端公钥，把明文口令加密成前端提交的形态：原始 C1C3C2 密文的标准 Base64。
// sm2Cipher fetches the server's public key and encrypts a clear-text password into the shape the front end submits:
// standard Base64 over the raw C1C3C2 ciphertext.
func sm2Cipher(t *testing.T, client *http.Client, base, plain string) string {
	t.Helper()

	resp, body, err := request(t, client, http.MethodGet, base+"/guomi/pubkey", "", nil)
	if err != nil {
		t.Fatalf("取 SM2 公钥失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /guomi/pubkey 状态码 = %d, 期望 200", resp.StatusCode)
	}
	pub, err := guomi.PublicKeyFromHex(strings.TrimSpace(body))
	if err != nil {
		t.Fatalf("解析 SM2 公钥失败: %v", err)
	}
	cipher, err := guomi.EncryptRaw(pub, []byte(plain))
	if err != nil {
		t.Fatalf("SM2 加密失败: %v", err)
	}
	return base64.StdEncoding.EncodeToString(cipher)
}

// signIn 用给定凭据完成一次登录并返回会话令牌；口令按前端的方式加密，登录接口不接受任何明文口令。
// signIn performs one sign-in with the given credentials and returns the session token; the password is encrypted the way
// the front end does it, because the endpoint accepts no clear-text password at all.
func signIn(t *testing.T, client *http.Client, base, tenant, username, password string) string {
	t.Helper()

	form := url.Values{
		"tenant":   {tenant},
		"username": {username},
		"password": {sm2Cipher(t, client, base, password)},
	}
	resp, body, err := request(t, client, http.MethodPost, base+"/login", form.Encode(), map[string]string{
		"Content-Type":   "application/x-www-form-urlencoded",
		"X-YD-Encrypted": "sm2",
	})
	if err != nil {
		t.Fatalf("登录 %s@%s 失败: %v", username, tenant, err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("登录 %s@%s 状态码 = %d, 期望 303\n%s", username, tenant, resp.StatusCode, body)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("解析基地址 %q 失败: %v", base, err)
	}
	for _, cookie := range client.Jar.Cookies(u) {
		if cookie.Name == sessionCookie && cookie.Value != "" {
			return cookie.Value
		}
	}
	t.Fatalf("登录 %s@%s 成功后 cookie jar 里没有 %s", username, tenant, sessionCookie)
	return ""
}

// hxGet 以 htmx 局部请求的形态取回一个片段（受保护端点因此返回片段而不是整页）。
// hxGet fetches a fragment in the shape of an htmx partial request, so a protected endpoint answers with the fragment
// instead of a whole page.
func hxGet(t *testing.T, client *http.Client, target string) string {
	t.Helper()

	resp, body, err := request(t, client, http.MethodGet, target, "", map[string]string{"HX-Request": "true"})
	if err != nil {
		t.Fatalf("GET %s 失败: %v", target, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 状态码 = %d, 期望 200\n%s", target, resp.StatusCode, body)
	}
	return body
}

// createTenant 以管理员身份建租户，并回读列表确认它真的在。 | createTenant creates a tenant as the administrator and reads the list back.
func createTenant(t *testing.T, client *http.Client, base, slug, name string) {
	t.Helper()

	post(t, client, base+"/mgr/tenants", url.Values{"slug": {slug}, "name": {name}})
	if fragment := hxGet(t, client, base+"/mgr/tenants"); !strings.Contains(fragment, slug) {
		t.Fatalf("建租户后列表里没有 %q:\n%s", slug, fragment)
	}
}

// createRole 以管理员身份建角色（带一项真实能力），并回读列表确认它真的在。
// createRole creates a role as the administrator (carrying one real capability) and reads the list back.
func createRole(t *testing.T, client *http.Client, base, name string) {
	t.Helper()

	post(t, client, base+"/mgr/roles", url.Values{
		"name":        {name},
		"description": {"created by the persistence probe | 由持久化探针创建"},
		"permission":  {"user:read"},
	})
	if fragment := hxGet(t, client, base+"/mgr/roles"); !strings.Contains(fragment, name) {
		t.Fatalf("建角色后列表里没有 %q:\n%s", name, fragment)
	}
}

// createUser 以管理员身份建账号（口令为 SM2 密文），并回读列表确认它真的在。
// createUser creates an account as the administrator (the password is SM2 ciphertext) and reads the list back.
func createUser(t *testing.T, client *http.Client, base, username, email, role string) {
	t.Helper()

	post(t, client, base+"/mgr/users", url.Values{
		"username":     {username},
		"email":        {email},
		"display_name": {username},
		"password":     {sm2Cipher(t, client, base, probeUserPassword)},
		"roles":        {role},
	})
	if fragment := hxGet(t, client, base+"/mgr/users"); !strings.Contains(fragment, email) {
		t.Fatalf("建账号后列表里没有 %q:\n%s", email, fragment)
	}
}

// post 以 htmx 局部请求的形态提交一个表单，只要求 2xx（具体文案由各端点的用例断言）。
// post submits a form in the shape of an htmx partial request and only demands a 2xx (the exact copy is asserted by the
// per-endpoint cases).
func post(t *testing.T, client *http.Client, target string, form url.Values) {
	t.Helper()

	headers := map[string]string{
		"HX-Request":     "true",
		"Content-Type":   "application/x-www-form-urlencoded",
		"X-YD-Encrypted": "sm2",
	}
	resp, body, err := request(t, client, http.MethodPost, target, form.Encode(), headers)
	if err != nil {
		t.Fatalf("POST %s 失败: %v", target, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("POST %s 状态码 = %d, 期望 2xx\n%s", target, resp.StatusCode, body)
	}
}

// request 发一条请求并把响应体读成字符串。 | request sends one request and reads the body into a string.
func request(t *testing.T, client *http.Client, method, target, body string, headers map[string]string) (*http.Response, string, error) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		return nil, "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, "", err
	}
	return resp, string(raw), nil
}

// logLines 抽出启动输出里包含 needle 的行，用于把日志里的那条告警原样展示出来。
// logLines extracts the startup lines carrying needle, so the warning itself can be shown verbatim.
func logLines(logs, needle string) string {
	kept := make([]string, 0, 1)
	for _, line := range strings.Split(strings.ReplaceAll(logs, "\r\n", "\n"), "\n") {
		if strings.Contains(line, needle) {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return "(没有匹配的行 | no matching line)"
	}
	return strings.Join(kept, "\n")
}
