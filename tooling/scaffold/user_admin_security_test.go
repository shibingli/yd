package scaffold_test

import (
	"database/sql"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "github.com/glebarez/sqlite"

	"github.com/shibingli/yd/security/authz"
	"github.com/shibingli/yd/tooling/scaffold"
)

// userAdminHX returns the headers of an htmx management request carrying a bearer token. |
// userAdminHX 返回携带 Bearer 令牌的 htmx 管理请求头。
func userAdminHX(token string) map[string]string {
	return map[string]string{"HX-Request": "true", "Authorization": "Bearer " + token}
}

// withUserAdminDB opens the generated project's SQLite file for the duration of fn. |
// withUserAdminDB 在 fn 执行期间打开生成项目的 SQLite 库。
//
// It is used only to arrange states the HTTP surface deliberately refuses to create (a user who belongs to a tenant
// the acting administrator does not administer) and to verify that refused requests wrote nothing. |
// 它只用于布置 HTTP 表面刻意拒绝创建的状态（用户隶属操作者并不管理的租户），以及验证被拒绝的请求确实没有写入。
func withUserAdminDB(t *testing.T, projDir, name string, fn func(*sql.DB)) {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(projDir, "data", name+".db")))
	if err != nil {
		t.Fatalf("打开生成的 SQLite 库失败: %v", err)
	}
	defer func() { _ = db.Close() }()
	fn(db)
}

// userAdminQueryString reads one string column. | userAdminQueryString 读取单个字符串列。
func userAdminQueryString(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()

	var value string
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("查询 %q 失败: %v", query, err)
	}
	return value
}

// userAdminExec runs one statement and fails the test on error. | userAdminExec 执行一条语句，失败即终止用例。
func userAdminExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()

	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("执行 %q 失败: %v", query, err)
	}
}

// userAdminMembership inserts one membership row directly. | userAdminMembership 直接插入一条成员关系行。
func userAdminMembership(t *testing.T, db *sql.DB, userID, tenantID, roles string, isDefault bool) {
	t.Helper()

	userAdminExec(t, db,
		"INSERT INTO yd_auth_user_tenants (user_id, tenant_id, roles, is_default, created_at, updated_at) VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))",
		userID, tenantID, roles, isDefault)
}

// userAdminCount returns the row count of a query. | userAdminCount 返回查询的行数。
func userAdminCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()

	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("统计 %q 失败: %v", query, err)
	}
	return count
}

// TestGeneratedAppUserAdministrationTenantIsolation drives the multi-tenant user-administration fixes end to end:
// tenant A's administrator can neither graft a user into tenant B nor take over a user whose identity belongs to B,
// while an administrator of both tenants and same-tenant administration keep working. It also proves a user
// administrator cannot create an owner (P1-1) and cannot overwrite an elevated built-in role (P1-5). |
// TestGeneratedAppUserAdministrationTenantIsolation 端到端验证多租户用户管理修复：A 租户管理员既不能把用户塞进
// B 租户，也不能接管身份属于 B 的用户；同时，同时管理两个租户的管理员与同租户内的正常管理仍然可用。它还证明
// 用户管理员无法创建 owner（P1-1），也无法改写信越高内置角色的分配（P1-5）。
func TestGeneratedAppUserAdministrationTenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实服务验证 | skipped in short mode")
	}

	const (
		name        = "yd-user-admin-probe"
		betaSlug    = "beta"
		newPassword = "Probe@654321"
		escalated   = "escalated-owner"
		creatorRole = "user-creator"
		dualAdmin   = "tenant-b-admin"
	)

	projDir, base, stop := startProbeAppDBWithStop(t, name, scaffold.DBSQLite, nil)

	owner := newProbeClient(t, httpTimeout)
	ownerToken := signIn(t, owner, base, probeTenant, probeAdmin, probeAdminPassword)

	// Tenant B is created by tenant A's owner. Note that creating a tenant does NOT make its creator a member, which
	// is exactly the boundary the cross-tenant checks rely on. | 租户 B 由 A 的所有者创建。创建租户并不会让创建者成为
	// 其成员，这正是跨租户校验所依赖的边界。
	resp, body := postFormValues(t, owner, base+"/mgr/tenants", url.Values{
		"slug": {betaSlug}, "name": {"Beta tenant"},
	}, userAdminHX(ownerToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("创建租户 beta 状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
	}

	for _, username := range []string{"victim-add", "victim-member-b", "probe-managed"} {
		createProbeUser(t, owner, base, ownerToken, username, "member", username+"@example.com")
	}
	createProbeUser(t, owner, base, ownerToken, "probe-admin", "admin", "probe-admin@example.com")
	createProbeUser(t, owner, base, ownerToken, "probe-admin2", "admin", "probe-admin2@example.com")
	createProbeRole(t, owner, base, ownerToken, creatorRole,
		authz.PermUserCreate, authz.PermUserRead, authz.PermUserUpdate)
	createProbeUser(t, owner, base, ownerToken, "probe-creator", creatorRole, "probe-creator@example.com")

	// The dual administrator holds member + user-admin in A and will be granted owner in B directly: it is the only
	// identity allowed to bootstrap tenant B's first account. | 双租户管理员在 A 持有 member + user-admin，并将直接获得
	// B 的 owner 成员关系：它是唯一有资格为 B 创建第一个账号的身份。
	createProbeRole(t, owner, base, ownerToken, "user-admin",
		authz.PermUserCreate, authz.PermUserRead, authz.PermUserUpdate)
	resp, body = postFormValues(t, owner, base+"/mgr/users", url.Values{
		"username":     {dualAdmin},
		"email":        {dualAdmin + "@example.com"},
		"display_name": {dualAdmin},
		"password":     {sm2Cipher(t, owner, base, probeUserPassword)},
		"roles":        {"member", "user-admin"},
	}, map[string]string{"HX-Request": "true", "X-YD-Encrypted": "sm2", "Authorization": "Bearer " + ownerToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("创建双租户管理员状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
	}

	var (
		alphaID, betaID string
		victimAddID     string
		victimMemberBID string
		dualAdminID     string
		probeAdmin2ID   string
		probeManagedID  string
	)
	withUserAdminDB(t, projDir, name, func(db *sql.DB) {
		alphaID = userAdminQueryString(t, db, "SELECT id FROM yd_auth_tenants WHERE slug = ?", probeTenant)
		betaID = userAdminQueryString(t, db, "SELECT id FROM yd_auth_tenants WHERE slug = ?", betaSlug)
		victimAddID = userAdminQueryString(t, db, "SELECT id FROM yd_auth_users WHERE username = ?", "victim-add")
		victimMemberBID = userAdminQueryString(t, db, "SELECT id FROM yd_auth_users WHERE username = ?", "victim-member-b")
		dualAdminID = userAdminQueryString(t, db, "SELECT id FROM yd_auth_users WHERE username = ?", dualAdmin)
		probeAdmin2ID = userAdminQueryString(t, db, "SELECT id FROM yd_auth_users WHERE username = ?", "probe-admin2")
		probeManagedID = userAdminQueryString(t, db, "SELECT id FROM yd_auth_users WHERE username = ?", "probe-managed")
		if count := userAdminCount(t, db,
			"SELECT COUNT(*) FROM yd_auth_user_tenants WHERE user_id = ? AND tenant_id IN (?, ?)",
			dualAdminID, alphaID, betaID); count != 1 {
			// The dual administrator is a member of A only; B is arranged below. | 双租户管理员此刻只是 A 的成员，B 在下面布置。
			t.Fatalf("双租户管理员初始成员关系数 = %d, 期望 1", count)
		}
	})

	// P0-1 negative: tenant A's owner is not a member of B, so it must not add a user to B. |
	// P0-1 反例：A 的所有者不是 B 的成员，因此不得把用户加入 B。
	resp, body = postFormValues(t, owner, base+"/mgr/users/"+victimAddID+"/tenants", url.Values{
		"tenants":        {alphaID, betaID},
		"default_tenant": {alphaID},
	}, userAdminHX(ownerToken))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("A 管理员向 B 添加用户状态码 = %d, 期望 403\n%s", resp.StatusCode, body)
	}
	withUserAdminDB(t, projDir, name, func(db *sql.DB) {
		if count := userAdminCount(t, db,
			"SELECT COUNT(*) FROM yd_auth_user_tenants WHERE user_id = ? AND tenant_id = ?", victimAddID, betaID); count != 0 {
			t.Errorf("被拒绝的跨租户添加仍然写入了 %d 条成员关系", count)
		}
	})

	// Arrange the states the HTTP surface deliberately refuses to create. | 布置 HTTP 表面刻意拒绝创建的状态。
	stop()
	withUserAdminDB(t, projDir, name, func(db *sql.DB) {
		userAdminMembership(t, db, dualAdminID, betaID, `["owner"]`, false)
		userAdminMembership(t, db, victimMemberBID, betaID, `["member"]`, false)
	})

	bin := filepath.Join(projDir, "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	base, stopRestart := startGeneratedAppWithStop(t, bin, projDir)
	defer stopRestart()

	// The dual administrator bootstraps tenant B: switch into B, create a user there (home = B), then switch back. |
	// 双租户管理员为 B 完成初始化：切换到 B 创建用户（身份租户 = B），再切回 A。
	dual := newProbeClient(t, httpTimeout)
	signIn(t, dual, base, probeTenant, dualAdmin, probeUserPassword)
	resp, body = postFormValues(t, dual, base+"/tenant/switch", url.Values{"tenant": {betaSlug}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("切换到 beta 状态码 = %d, 期望 303\n%s", resp.StatusCode, body)
	}
	betaToken := sessionToken(t, dual, base)
	resp, body = postFormValues(t, dual, base+"/mgr/users", url.Values{
		"username":     {"victim-home-b"},
		"email":        {"victim-home-b@example.com"},
		"display_name": {"victim-home-b"},
		"password":     {sm2Cipher(t, dual, base, probeUserPassword)},
		"roles":        {"member"},
	}, map[string]string{"HX-Request": "true", "X-YD-Encrypted": "sm2", "Authorization": "Bearer " + betaToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("在 beta 租户建号状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
	}
	var homeBID string
	withUserAdminDB(t, projDir, name, func(db *sql.DB) {
		homeBID = userAdminQueryString(t, db, "SELECT id FROM yd_auth_users WHERE username = ?", "victim-home-b")
		// The victim is also an ordinary member of A: the cross-tenant takeover scenario needs it visible there. |
		// 该用户同时是 A 的普通成员：跨租户接管场景需要它在 A 可见。
		userAdminMembership(t, db, homeBID, alphaID, `["member"]`, false)
		if home := userAdminQueryString(t, db, "SELECT tenant_id FROM yd_auth_users WHERE id = ?", homeBID); home != betaID {
			t.Fatalf("victim-home-b 的身份租户 = %q, 期望 %q", home, betaID)
		}
	})

	owner = newProbeClient(t, httpTimeout)
	ownerToken = signIn(t, owner, base, probeTenant, probeAdmin, probeAdminPassword)

	// The victim signs in while the sec_key is intact; the token issued now must survive the refused attacks. |
	// 受害者在 sec_key 完好时登录；此刻签发的令牌必须在攻击被拒后仍然有效。
	victim := newProbeClient(t, httpTimeout)
	victimToken := signIn(t, victim, base, betaSlug, "victim-home-b", probeUserPassword)

	// P0-2: tenant A's owner administers A but not B, so every credential and lifecycle action on a home-B user is
	// refused. | P0-2：A 的所有者管理 A 但不管理 B，因此对身份属于 B 的用户的任何凭据与生命周期操作都应被拒绝。
	p0_2Attacks := []struct {
		name    string
		path    string
		values  url.Values
		headers map[string]string
	}{
		{
			name:    "password reset",
			path:    "/mgr/users/" + homeBID + "/password",
			values:  url.Values{"password": {sm2Cipher(t, owner, base, newPassword)}},
			headers: map[string]string{"HX-Request": "true", "X-YD-Encrypted": "sm2", "Authorization": "Bearer " + ownerToken},
		},
		{
			name:    "sec_key rotation",
			path:    "/mgr/users/" + homeBID + "/sec-key",
			values:  url.Values{},
			headers: userAdminHX(ownerToken),
		},
		{
			name:    "status change",
			path:    "/mgr/users/" + homeBID + "/status",
			values:  url.Values{"status": {"disabled"}},
			headers: userAdminHX(ownerToken),
		},
		{
			name:    "profile update",
			path:    "/mgr/users/" + homeBID,
			values:  url.Values{"username": {"victim-home-b"}, "email": {"hijacked@example.com"}, "display_name": {"hijacked"}},
			headers: userAdminHX(ownerToken),
		},
	}
	for _, attack := range p0_2Attacks {
		t.Run("P0-2 "+attack.name, func(t *testing.T) {
			resp, body := postFormValues(t, owner, base+attack.path, attack.values, attack.headers)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("状态码 = %d, 期望 403\n%s", resp.StatusCode, body)
			}
			if !containsForbidden(body) {
				t.Errorf("403 未回显越权文案:\n%s", body)
			}
		})
	}

	// The old token still verifies: neither the password nor the sec_key changed. |
	// 旧令牌仍然有效：口令与 sec_key 都没有被改动。
	resp, body = getText(t, victim, base+"/account", map[string]string{"Authorization": "Bearer " + victimToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("攻击后旧令牌访问账号页状态码 = %d, 期望 200（sec_key 不应被轮换）\n%s", resp.StatusCode, body)
	}
	withUserAdminDB(t, projDir, name, func(db *sql.DB) {
		home := userAdminQueryString(t, db, "SELECT tenant_id FROM yd_auth_users WHERE id = ?", homeBID)
		if home != betaID {
			t.Errorf("身份租户被改成 %q, 期望仍为 %q", home, betaID)
		}
		status := userAdminQueryString(t, db, "SELECT status FROM yd_auth_users WHERE id = ?", homeBID)
		if status != "active" {
			t.Errorf("状态被改成 %q, 期望仍为 active", status)
		}
		email := userAdminQueryString(t, db, "SELECT email FROM yd_auth_users WHERE id = ?", homeBID)
		if email == "hijacked@example.com" {
			t.Error("the profile edit took effect | 资料修改竟然生效了")
		}
	})

	// P0-1 removal: the same boundary applies when tenant A's owner tries to cut the user out of B. |
	// P0-1 移除：A 的所有者试图把用户从 B 移除时，同一条边界同样生效。
	resp, body = postFormValues(t, owner, base+"/mgr/users/"+victimMemberBID+"/tenants", url.Values{
		"tenants":        {alphaID},
		"default_tenant": {alphaID},
	}, userAdminHX(ownerToken))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("A 管理员从 B 移除用户状态码 = %d, 期望 403\n%s", resp.StatusCode, body)
	}
	withUserAdminDB(t, projDir, name, func(db *sql.DB) {
		if count := userAdminCount(t, db,
			"SELECT COUNT(*) FROM yd_auth_user_tenants WHERE user_id = ? AND tenant_id = ?", victimMemberBID, betaID); count != 1 {
			t.Errorf("被拒绝的跨租户移除仍然删掉了成员关系（剩余 %d 条）", count)
		}
	})

	// Positive control: an administrator of both tenants may add the very same user to B. |
	// 正例：同时管理两个租户的管理员可以把同一个用户加入 B。
	signIn(t, dual, base, probeTenant, dualAdmin, probeUserPassword)
	alphaToken := sessionToken(t, dual, base)
	resp, body = postFormValues(t, dual, base+"/mgr/users/"+victimAddID+"/tenants", url.Values{
		"tenants":        {alphaID, betaID},
		"default_tenant": {alphaID},
	}, userAdminHX(alphaToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("双租户管理员向 B 添加用户状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
	}
	withUserAdminDB(t, projDir, name, func(db *sql.DB) {
		if count := userAdminCount(t, db,
			"SELECT COUNT(*) FROM yd_auth_user_tenants WHERE user_id = ? AND tenant_id = ?", victimAddID, betaID); count != 1 {
			t.Errorf("合法的跨租户添加没有写入成员关系（count=%d）", count)
		}
	})

	// P1-1: a user administrator cannot mint an owner. | P1-1：用户管理员无法造出一个 owner。
	creator := newProbeClient(t, httpTimeout)
	creatorToken := signIn(t, creator, base, probeTenant, "probe-creator", probeUserPassword)
	resp, body = postFormValues(t, creator, base+"/mgr/users", url.Values{
		"username":     {escalated},
		"email":        {escalated + "@example.com"},
		"display_name": {escalated},
		"password":     {sm2Cipher(t, creator, base, probeUserPassword)},
		"roles":        {"owner"},
	}, map[string]string{"HX-Request": "true", "X-YD-Encrypted": "sm2", "Authorization": "Bearer " + creatorToken})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("用户管理员创建 owner 状态码 = %d, 期望 403\n%s", resp.StatusCode, body)
	}
	withUserAdminDB(t, projDir, name, func(db *sql.DB) {
		if count := userAdminCount(t, db, "SELECT COUNT(*) FROM yd_auth_users WHERE username = ?", escalated); count != 0 {
			t.Error("the rejected escalation still created an account | 被拒绝的提权建号仍然创建了账号")
		}
	})

	// The create drawer only lists roles whose capabilities the actor already holds. |
	// 新建抽屉只列出操作者已经持有其能力的角色。
	resp, body = getText(t, creator, base+"/mgr/users/new", userAdminHX(creatorToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("打开建号抽屉状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "yd-user-new-role-"+creatorRole) {
		t.Errorf("建号抽屉缺少操作者可授予的角色 %q:\n%s", creatorRole, body)
	}
	for _, hidden := range []string{"yd-user-new-role-owner", "yd-user-new-role-admin"} {
		if strings.Contains(body, hidden) {
			t.Errorf("建号抽屉下发了操作者不可授予的角色 %q:\n%s", hidden, body)
		}
	}

	// Same-tenant administration still works: the creator may manage the account it just created with its own role. |
	// 同租户管理仍然可用：创建者可以管理它用自己角色新建的账号。
	resp, body = postFormValues(t, creator, base+"/mgr/users", url.Values{
		"username":     {"probe-creator-managed"},
		"email":        {"probe-creator-managed@example.com"},
		"display_name": {"probe-creator-managed"},
		"password":     {sm2Cipher(t, creator, base, probeUserPassword)},
		"roles":        {creatorRole},
	}, map[string]string{"HX-Request": "true", "X-YD-Encrypted": "sm2", "Authorization": "Bearer " + creatorToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("用户管理员建号状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
	}
	var creatorManagedID string
	withUserAdminDB(t, projDir, name, func(db *sql.DB) {
		creatorManagedID = userAdminQueryString(t, db, "SELECT id FROM yd_auth_users WHERE username = ?", "probe-creator-managed")
	})
	resp, body = postFormValues(t, creator, base+"/mgr/users/"+creatorManagedID+"/password", url.Values{
		"password": {sm2Cipher(t, creator, base, newPassword)},
	}, map[string]string{"HX-Request": "true", "X-YD-Encrypted": "sm2", "Authorization": "Bearer " + creatorToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("同租户口令重置状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
	}

	// P1-5: the built-in admin role is elevated, so a non-owner administrator cannot rewrite its assignment even
	// though its current capabilities are a subset of the actor's. | P1-5：内置 admin 属于高权限角色，因此即使目标
	// 当前能力是操作者的子集，非 owner 管理员也不得改写它的分配。
	probeAdmin := newProbeClient(t, httpTimeout)
	probeAdminToken := signIn(t, probeAdmin, base, probeTenant, "probe-admin", probeUserPassword)
	resp, body = postFormValues(t, probeAdmin, base+"/mgr/users/"+probeAdmin2ID+"/roles", url.Values{
		"roles": {"member"},
	}, userAdminHX(probeAdminToken))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin 改写信越内置角色状态码 = %d, 期望 403\n%s", resp.StatusCode, body)
	}
	// ... while the same administrator still manages an ordinary member normally. |
	// ……而同一位管理员仍能正常管理普通成员。
	resp, body = postFormValues(t, probeAdmin, base+"/mgr/users/"+probeManagedID+"/password", url.Values{
		"password": {sm2Cipher(t, probeAdmin, base, newPassword)},
	}, map[string]string{"HX-Request": "true", "X-YD-Encrypted": "sm2", "Authorization": "Bearer " + probeAdminToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin 重置普通成员口令状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
	}
	// Submitting a role the actor does not hold is refused by the anti-escalation gate. |
	// 提交操作者未持有的角色会被防越权闸门拒绝。
	resp, body = postFormValues(t, probeAdmin, base+"/mgr/users/"+probeManagedID+"/roles", url.Values{
		"roles": {"owner"},
	}, userAdminHX(probeAdminToken))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin 把成员提升为 owner 状态码 = %d, 期望 403\n%s", resp.StatusCode, body)
	}
}
