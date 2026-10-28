package authz_test

import (
	"strings"
	"testing"

	"github.com/shibingli/yd/security/authz"
)

// TestBuiltinGrantsBindEveryBuiltinRoleExactlyOnce checks that the default table and the role registry
// describe the same set of roles. | TestBuiltinGrantsBindEveryBuiltinRoleExactlyOnce 校验默认授权表与角色注册表描述的是同一组角色。
//
// A role without a grant would be a role the seed writes with no capability at all, and a grant for a
// role nobody declared would be a binding the seed could never write. | 没有授权的角色意味着种子会写出一个
// 一项能力都没有的角色，而为无人声明的角色写出的授权则是种子永远写不出来的一次绑定。
func TestBuiltinGrantsBindEveryBuiltinRoleExactlyOnce(t *testing.T) {
	grants := authz.BuiltinGrants()
	if len(grants) == 0 {
		t.Fatal("BuiltinGrants() is empty | BuiltinGrants() 为空")
	}

	counted := make(map[string]int, len(grants))
	for _, g := range grants {
		counted[g.Role]++
		if !authz.RoleRegistered(g.Role) {
			t.Errorf("grant for unregistered role %q | 为未注册的角色 %q 写了授权", g.Role, g.Role)
		}
	}

	for _, role := range authz.BuiltinRoles() {
		switch counted[role.Name] {
		case 1:
			delete(counted, role.Name)
		case 0:
			t.Errorf("built-in role %q has no default grant | 内置角色 %q 没有默认授权", role.Name, role.Name)
		default:
			t.Errorf("built-in role %q has %d default grants, want 1 | 内置角色 %q 有 %d 条默认授权，期望 1 条",
				role.Name, counted[role.Name], role.Name, counted[role.Name])
			delete(counted, role.Name)
		}
	}
	for role, n := range counted {
		t.Errorf("role %q appears %d time(s) in the grants table and not among the built-in roles | 角色 %q 在授权表中出现 %d 次，却不在内置角色里",
			role, n, role, n)
	}
}

// TestBuiltinGrantsReferenceRegisteredPermissions checks every granted token. | TestBuiltinGrantsReferenceRegisteredPermissions 校验每一条被授予的标识。
//
// A token the registry does not know would be granted, stored, and then never satisfied: the guard of
// a route resolves through the same registry, so an unregistered grant is a capability nobody can
// ever exercise. | 注册表不认识的标识会被授予、会被落库，然后永远无法满足：路由守卫经同一张注册表解析，
// 因此一条未注册的授权就是一项永远没人能行使的能力。
func TestBuiltinGrantsReferenceRegisteredPermissions(t *testing.T) {
	for _, g := range authz.BuiltinGrants() {
		if len(g.Permissions) == 0 {
			continue // the member role grants nothing on purpose | 成员角色刻意不授予任何能力
		}
		seen := make(map[string]bool, len(g.Permissions))
		for _, token := range g.Permissions {
			if !authz.Registered(token) {
				t.Errorf("role %q grants unregistered capability %q | 角色 %q 授予了未注册的能力 %q", g.Role, token, g.Role, token)
			}
			if seen[token] {
				t.Errorf("role %q grants %q twice | 角色 %q 重复授予 %q", g.Role, token, g.Role, token)
			}
			seen[token] = true
		}
	}
}

// TestBuiltinGrantsKeepRegistryOrder checks that every grant follows the registry order. | TestBuiltinGrantsKeepRegistryOrder 校验每条授权都遵循注册表顺序。
//
// The seed writes the grants as they are written here, so the order is what an operator sees in the
// permission matrix and in a diff; deriving it from the registry keeps it stable without anyone
// sorting anything. | 种子按这里的写法写入授权，因此这个顺序就是运维在权限矩阵与 diff 中看到的顺序；
// 让它由注册表推导，无需任何排序即可保持稳定。
func TestBuiltinGrantsKeepRegistryOrder(t *testing.T) {
	rank := make(map[string]int, len(authz.BuiltinPermissions()))
	for i, p := range authz.BuiltinPermissions() {
		rank[p.Name] = i
	}
	for _, g := range authz.BuiltinGrants() {
		prev := -1
		for _, token := range g.Permissions {
			at, known := rank[token]
			if !known {
				continue // reported by the test above | 由上面的用例报告
			}
			if at < prev {
				t.Errorf("role %q lists %q out of registry order | 角色 %q 的 %q 未按注册表顺序排列", g.Role, token, g.Role, token)
			}
			prev = at
		}
	}
}

// TestBuiltinGrantsCoverEveryBuiltinPermission checks that no capability is left uncovered. |
// TestBuiltinGrantsCoverEveryBuiltinPermission 校验没有能力被遗漏。
//
// The union of the grants has to be the whole registry: a capability no role holds is a capability the
// framework offers and nobody can ever exercise, and the failure it produces (a permanent 403 on the
// route that requires it) is one of the hardest to trace back to its cause. | 全部授权的并集必须等于整张注册表：
// 无人持有的能力，就是框架提供了、却没有人能行使的能力，而它造成的故障（要求它的那条路由永远 403）
// 是最难回溯到原因的一类。
func TestBuiltinGrantsCoverEveryBuiltinPermission(t *testing.T) {
	granted := make(map[string]string, len(authz.BuiltinPermissions()))
	for _, g := range authz.BuiltinGrants() {
		for _, token := range g.Permissions {
			if _, dup := granted[token]; !dup {
				granted[token] = g.Role
			}
		}
	}
	for _, p := range authz.BuiltinPermissions() {
		if _, ok := granted[p.Name]; !ok {
			t.Errorf("capability %q is granted to no built-in role: add it to a grant in security/authz/grants.go or list it as deliberately uncovered | 能力 %q 未被授予任何内置角色：请在 authz/grants.go 的授权里加上它，或明确列出它被刻意排除",
				p.Name, p.Name)
		}
	}
	if len(granted) != len(authz.BuiltinPermissions()) {
		t.Errorf("the grants mention %d capabilities the registry does not declare | 授权中出现了 %[1]d 项注册表未声明的能力",
			len(granted)-len(authz.BuiltinPermissions()))
	}
}

// TestBuiltinGrantsOwnerHoldsEverything checks the owner against the registry. | TestBuiltinGrantsOwnerHoldsEverything 用注册表校验所有者。
func TestBuiltinGrantsOwnerHoldsEverything(t *testing.T) {
	g, ok := authz.GrantOf(authz.RoleOwner)
	if !ok {
		t.Fatalf("GrantOf(%q) reports no grant | GrantOf(%q) 报告没有授权", authz.RoleOwner, authz.RoleOwner)
	}

	held := make(map[string]bool, len(g.Permissions))
	for _, token := range g.Permissions {
		held[token] = true
	}
	for _, p := range authz.BuiltinPermissions() {
		if !held[p.Name] {
			t.Errorf("the owner does not hold %q | 所有者不持有 %q", p.Name, p.Name)
		}
	}
	if len(g.Permissions) != len(authz.BuiltinPermissions()) {
		t.Errorf("the owner holds %d capabilities, the registry declares %d | 所有者持有 %d 项能力，注册表声明了 %d 项",
			len(g.Permissions), len(authz.BuiltinPermissions()), len(g.Permissions), len(authz.BuiltinPermissions()))
	}
}

// TestBuiltinGrantsAdministratorBoundary pins the two capabilities the administrator does not hold. |
// TestBuiltinGrantsAdministratorBoundary 钉住管理员不持有的两项能力。
//
// The list is written out here as well as in the table on purpose: this is the sentence that has to
// stay true when someone widens the administrator later, and it is the only place where widening it
// silently would be caught. | 这份清单刻意在表里和这里各写一次：当日后有人扩大管理员权限时，
// 需要保持不变的就是这句话，而这里也是「悄悄扩大」唯一会被抓住的地方。
func TestBuiltinGrantsAdministratorBoundary(t *testing.T) {
	withheld := map[string]bool{
		authz.PermTenantDelete:     true,
		authz.PermPermissionManage: true,
	}
	g, ok := authz.GrantOf(authz.RoleAdmin)
	if !ok {
		t.Fatalf("GrantOf(%q) reports no grant | GrantOf(%q) 报告没有授权", authz.RoleAdmin, authz.RoleAdmin)
	}

	held := make(map[string]bool, len(g.Permissions))
	for _, token := range g.Permissions {
		held[token] = true
		if withheld[token] {
			t.Errorf("the administrator holds %q, which the boundary withholds | 管理员持有 %q，而该能力被边界排除", token, token)
		}
	}
	for _, p := range authz.BuiltinPermissions() {
		if withheld[p.Name] || held[p.Name] {
			continue
		}
		t.Errorf("the administrator does not hold %q, which the boundary does not withhold | 管理员不持有 %q，而该能力不在边界之内",
			p.Name, p.Name)
	}
}

// TestBuiltinGrantsAuditorIsReadOnly checks the auditor against the registry rather than against a
// second copy of the same list. | TestBuiltinGrantsAuditorIsReadOnly 用注册表而不是同一份清单的第二份副本校验审计员。
func TestBuiltinGrantsAuditorIsReadOnly(t *testing.T) {
	g, ok := authz.GrantOf(authz.RoleAuditor)
	if !ok {
		t.Fatalf("GrantOf(%q) reports no grant | GrantOf(%q) 报告没有授权", authz.RoleAuditor, authz.RoleAuditor)
	}

	held := make(map[string]bool, len(g.Permissions))
	for _, token := range g.Permissions {
		held[token] = true
		if !strings.HasSuffix(token, ":read") {
			t.Errorf("the auditor holds %q, which is not a read capability | 审计员持有 %q，而它不是读能力", token, token)
		}
	}
	for _, p := range authz.BuiltinPermissions() {
		if !strings.HasSuffix(p.Name, ":read") {
			continue
		}
		if !held[p.Name] {
			t.Errorf("the auditor does not hold the read capability %q | 审计员不持有读能力 %q", p.Name, p.Name)
		}
	}
}

// TestBuiltinGrantsMemberHoldsOnlySelfService pins the deliberate non-empty member grant. |
// TestBuiltinGrantsMemberHoldsOnlySelfService 钉住成员刻意保留的非空授权。
//
// Every other capability is tenant-wide, so granting one would let an ordinary account reach its
// colleagues' data. The message and attachment capabilities listed here are different: their
// services scope them to the member's own conversations, messages and uploads, which is why they
// are the only entries here and why this list must stay closed. | 其余能力都是租户级的，授予任何一项
// 都会让普通账号碰到同事的数据。这里列出的消息与附件能力不同：对应服务把它们限定在成员自己的会话、
// 消息与上传上，这正是它们是唯一条目、且这份清单必须保持封闭的原因。
func TestBuiltinGrantsMemberHoldsOnlySelfService(t *testing.T) {
	g, ok := authz.GrantOf(authz.RoleMember)
	if !ok {
		t.Fatalf("GrantOf(%q) reports no grant | GrantOf(%q) 报告没有授权", authz.RoleMember, authz.RoleMember)
	}
	want := map[string]bool{
		authz.PermMessageRead:       true,
		authz.PermMessageSend:       true,
		authz.PermMessageUpdate:     true,
		authz.PermMessageDelete:     true,
		authz.PermAttachmentCreate:  true,
		authz.PermAttachmentReadOwn: true,
		authz.PermAttachmentDelete:  true,
	}
	held := make(map[string]bool, len(g.Permissions))
	for _, token := range g.Permissions {
		held[token] = true
		if !want[token] {
			t.Errorf("the member holds %q, which is outside the self-service scope | 成员持有 %q，超出了自助范围", token, token)
		}
	}
	for token := range want {
		if !held[token] {
			t.Errorf("the member does not hold %q | 成员不持有 %q", token, token)
		}
	}
}

// TestBuiltinGrantsReturnsDeepCopies checks that the table cannot be mutated through its accessors. |
// TestBuiltinGrantsReturnsDeepCopies 校验无法通过访问器改动这张表。
//
// The seed, the permission matrix and the tests all read the same table, and an accidental append in
// one of them would silently widen a role for everyone who comes later. | 种子数据、权限矩阵与测试读的是同一张表，
// 其中任何一处不小心的追加，都会悄悄为后来者扩大某个角色的权限。
func TestBuiltinGrantsReturnsDeepCopies(t *testing.T) {
	first := authz.BuiltinGrants()
	if len(first) == 0 {
		t.Fatal("BuiltinGrants() is empty | BuiltinGrants() 为空")
	}
	for i := range first {
		first[i].Role = "mutated"
		if len(first[i].Permissions) > 0 {
			first[i].Permissions[0] = "mutated:mutated"
		}
		first[i].Permissions = append(first[i].Permissions, "appended:appended")
	}

	second := authz.BuiltinGrants()
	for _, g := range second {
		if g.Role == "mutated" {
			t.Fatalf("mutating the returned table reached the package | 改动返回值影响到了包内状态")
		}
		for _, token := range g.Permissions {
			if token == "mutated:mutated" || token == "appended:appended" {
				t.Fatalf("mutating a returned permission list reached the package | 改动返回的权限清单影响到了包内状态")
			}
		}
	}

	g, ok := authz.GrantOf(authz.RoleOwner)
	if !ok || len(g.Permissions) == 0 {
		t.Fatalf("GrantOf(%q) reports no permissions | GrantOf(%q) 没有返回权限", authz.RoleOwner, authz.RoleOwner)
	}
	g.Permissions[0] = "mutated:mutated"
	if again, _ := authz.GrantOf(authz.RoleOwner); again.Permissions[0] == "mutated:mutated" {
		t.Fatalf("mutating the grant returned by GrantOf reached the package | 改动 GrantOf 的返回值影响到了包内状态")
	}
}

// TestGrantOfResolvesEverySpelling checks the four accepted role references. | TestGrantOfResolvesEverySpelling 校验四种被接受的角色引用写法。
func TestGrantOfResolvesEverySpelling(t *testing.T) {
	want, ok := authz.GrantOf(authz.RoleAdmin)
	if !ok {
		t.Fatalf("GrantOf(%q) reports no grant | GrantOf(%q) 报告没有授权", authz.RoleAdmin, authz.RoleAdmin)
	}

	spellings := []string{
		authz.RoleAdmin,
		authz.RoleConstantName(authz.RoleAdmin),
		authz.RoleDataKey(authz.RoleAdmin),
		authz.RoleJSKey(authz.RoleAdmin),
	}
	for _, ref := range spellings {
		got, found := authz.GrantOf(ref)
		if !found {
			t.Errorf("GrantOf(%q) found nothing | GrantOf(%q) 什么都没找到", ref, ref)
			continue
		}
		if got.Role != want.Role || strings.Join(got.Permissions, ",") != strings.Join(want.Permissions, ",") {
			t.Errorf("GrantOf(%q) = %v, want %v | GrantOf(%q) = %v，期望 %v", ref, got, want, ref, got, want)
		}
	}

	if _, found := authz.GrantOf("no-such-role"); found {
		t.Error("GrantOf on an unknown role reported a grant | GrantOf 对未知角色报告了授权")
	}
}
