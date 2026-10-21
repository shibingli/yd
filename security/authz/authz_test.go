package authz_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shibingli/yd/security/authz"
)

// TestBuiltinVocabularyIsRegistered 要求内建词汇表在包初始化时就已经可用。
//
// The registry is the one source of truth every other assertion in this repository leans on, so the
// first thing to prove is that it is populated before any caller runs. | 注册表是本仓其它断言共同依赖的
// 唯一事实来源，因此首先要证明的是：在任何调用方运行之前它已经填好了。
func TestBuiltinVocabularyIsRegistered(t *testing.T) {
	perms := authz.BuiltinPermissions()
	roles := authz.BuiltinRoles()
	if len(perms) == 0 || len(roles) == 0 {
		t.Fatalf("built-in tables are empty: perms=%[1]d roles=%[2]d | 内建表为空：权限 %[1]d 角色 %[2]d", len(perms), len(roles))
	}
	// The registry is process-wide, so other tests in this package may legitimately register
	// additional capabilities. What matters here is that every built-in entry is present, not
	// that the registry holds nothing else. | 注册表是进程级的，本包其它测试可以合法地注册额外能力；
	// 这里要证明的是每条内建记录都在，而不是注册表中没有别的东西。
	got := authz.Permissions()
	if len(got) < len(perms) {
		t.Fatalf("registry holds %[1]d permissions, fewer than the %[2]d built-ins | 注册表有 %[1]d 条权限，少于 %[2]d 条内建记录", len(got), len(perms))
	}
	for _, p := range perms {
		if !authz.Registered(p.Name) {
			t.Errorf("built-in permission %q is not registered", p.Name)
		}
		got, err := authz.Lookup(p.Name)
		if err != nil {
			t.Errorf("Lookup(%q) failed: %v", p.Name, err)
			continue
		}
		if got.Name != p.Name || got.Description != p.Description {
			t.Errorf("Lookup(%q) = %+v, want %+v", p.Name, got, p)
		}
		if got.Group == "" {
			t.Errorf("permission %q has no derived group | 权限 %q 没有推导出的分组", p.Name, p.Name)
		}
	}
	for _, r := range roles {
		if !authz.RoleRegistered(r.Name) {
			t.Errorf("built-in role %q is not registered", r.Name)
		}
	}
}

// TestBuiltinVocabularyIsValid 直接校验内建词汇表本身。
//
// Runtime registration deliberately no longer panics on a defective table: a library must not take
// the process down at init. This test is therefore the assertion that replaced that panic — it
// re-checks every built-in entry's shape, description, derived group and uniqueness, so deleting a
// malformed row still fails loudly. | 运行时注册已刻意不再因词汇表缺陷而 panic：库不得在 init 阶段
// 拖垮进程。因此本测试就是取代那次 panic 的断言——它逐条复查内建记录的形态、说明、推导分组与唯一性，
// 删掉某条非法记录仍会明确失败。
func TestBuiltinVocabularyIsValid(t *testing.T) {
	seenPerms := make(map[string]bool, len(authz.BuiltinPermissions()))
	for _, p := range authz.BuiltinPermissions() {
		if !authz.IsPermissionName(p.Name) {
			t.Errorf("built-in permission %q has an invalid shape", p.Name)
		}
		if strings.TrimSpace(p.Description) == "" {
			t.Errorf("built-in permission %q has no description | 内建权限 %q 缺少说明", p.Name, p.Name)
		}
		derived := ""
		if i := strings.IndexByte(p.Name, ':'); i > 0 {
			derived = p.Name[:i]
		}
		if p.Group != derived {
			t.Errorf("built-in permission %q has group %q, want %q", p.Name, p.Group, derived)
		}
		if seenPerms[p.Name] {
			t.Errorf("duplicate built-in permission %q | 内建权限 %q 重复", p.Name, p.Name)
		}
		seenPerms[p.Name] = true
	}

	seenRoles := make(map[string]bool, len(authz.BuiltinRoles()))
	for _, r := range authz.BuiltinRoles() {
		if !authz.IsRoleName(r.Name) {
			t.Errorf("built-in role %q has an invalid shape", r.Name)
		}
		if strings.TrimSpace(r.Description) == "" {
			t.Errorf("built-in role %q has no description | 内建角色 %q 缺少说明", r.Name, r.Name)
		}
		if seenRoles[r.Name] {
			t.Errorf("duplicate built-in role %q | 内建角色 %q 重复", r.Name, r.Name)
		}
		seenRoles[r.Name] = true
	}
}

// TestPermissionsAreSortedAndCopied 要求读取顺序稳定，且返回值与注册表之间没有共享内存。
//
// A stable order is what makes `yd authz list` and its test comparable; the copy is what keeps a
// caller from mutating the registry through a value it was handed. | 稳定的顺序让 `yd authz list` 及其测试可比较；
// 副本则让调用方无法通过拿到的值改动注册表。
func TestPermissionsAreSortedAndCopied(t *testing.T) {
	first := authz.Permissions()
	second := authz.Permissions()
	// Permissions() is documented as sorted by group and then by name; a registry that also
	// contains project-declared capabilities can legitimately break a name-only ordering when a
	// declaration places a token in an explicit group. | Permissions() 的契约是「先按分组、再按名字」；
	// 当注册表里还有项目声明的能力时，显式分组会让「仅按名字」的假设失效。
	if !slices.IsSortedFunc(first, func(a, b authz.Permission) int {
		switch {
		case a.Group < b.Group:
			return -1
		case a.Group > b.Group:
			return 1
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		default:
			return 0
		}
	}) {
		t.Errorf("Permissions() is not sorted by group then name: %v", names(first))
	}
	if !slices.EqualFunc(first, second, func(a, b authz.Permission) bool { return a == b }) {
		t.Errorf("two calls returned different tables")
	}
	if len(first) > 0 {
		first[0].Name = "mutated:by:caller"
		if authz.Registered("mutated:by:caller") {
			t.Errorf("mutating the returned slice reached the registry | 改动返回的切片触达了注册表")
		}
		if authz.Permissions()[0].Name == "mutated:by:caller" {
			t.Errorf("the registry is corrupt after a caller mutation | 调用方改动后注册表被污染")
		}
	}

	roles := authz.Roles()
	if !slices.IsSortedFunc(roles, func(a, b authz.Role) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		default:
			return 0
		}
	}) {
		t.Errorf("Roles() is not sorted by name | Roles() 未按名字排序")
	}
}

// TestRegisterRejectsDuplicatesAndBadNames 要求重复与非法的声明在注册期就被拒绝。
//
// A duplicate is not a later problem to be deduplicated away: two definitions of one capability mean
// two descriptions, and the registry would have to pick one silently. | 重复不是「稍后去重」的问题：
// 同一条能力的两份定义意味着两份说明，而注册表将不得不悄悄挑一份。
func TestRegisterRejectsDuplicatesAndBadNames(t *testing.T) {
	cases := []struct {
		name string
		perm authz.Permission
		want error
	}{
		{"duplicate of a builtin", authz.Permission{Name: authz.PermUserCreate, Description: "again | 又一次"}, authz.ErrDuplicatePermission},
		{"malformed name", authz.Permission{Name: "UserCreate", Description: "x | x"}, authz.ErrInvalidPermission},
		{"empty name", authz.Permission{Name: "", Description: "x | x"}, authz.ErrInvalidPermission},
		{"uppercase action", authz.Permission{Name: "user:Create", Description: "x | x"}, authz.ErrInvalidPermission},
		{"no description", authz.Permission{Name: "user:whatever"}, authz.ErrInvalidPermission},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := authz.Register(tc.perm); !errors.Is(err, tc.want) {
				t.Errorf("Register(%+v) = %v, want %v", tc.perm, err, tc.want)
			}
		})
	}

	if err := authz.RegisterRole(authz.Role{Name: authz.RoleAdmin, Description: "again | 又一次"}); !errors.Is(err, authz.ErrDuplicateRole) {
		t.Errorf("RegisterRole(admin) = %v, want %v", err, authz.ErrDuplicateRole)
	}
	if err := authz.RegisterRole(authz.Role{Name: "Admin", Description: "x | x"}); !errors.Is(err, authz.ErrInvalidRole) {
		t.Errorf("RegisterRole(Admin) = %v, want %v", err, authz.ErrInvalidRole)
	}
}

// TestRegisterAllSkipsKnownNames 要求合并内建词汇表是幂等的。
//
// The scaffold registers its own table next to the framework's, and both of them mention user:read;
// the second declaration must not fail, or every generated project would start broken. | 脚手架把自己的表
// 与框架的表并列注册，两者都提到 user:read；第二次声明绝不能失败，否则每个生成的项目一出生就是坏的。
func TestRegisterAllSkipsKnownNames(t *testing.T) {
	const token = "invoice:approve"
	if authz.Registered(token) {
		t.Fatalf("%q is already registered; the test cannot prove what it claims", token)
	}
	perms := []authz.Permission{
		{Name: token, Group: authz.GroupUser, Description: "Approve an invoice | 审批发票"},
		{Name: authz.PermUserRead, Description: "Re-declared by the project | 项目重新声明"},
	}
	roles := []authz.Role{{Name: authz.RoleAdmin, Description: "Re-declared by the project | 项目重新声明"}}

	if err := authz.RegisterAll(perms, roles); err != nil {
		t.Fatalf("RegisterAll failed: %v", err)
	}
	got, err := authz.Lookup(token)
	if err != nil {
		t.Fatalf("the project capability was not registered: %v", err)
	}
	if got.Group != authz.GroupUser || got.Description != "Approve an invoice | 审批发票" {
		t.Errorf("Lookup(%q) = %+v", token, got)
	}
	// The second registration of the same name must have been ignored, not overwritten.
	userRead, err := authz.Lookup(authz.PermUserRead)
	if err != nil {
		t.Fatalf("Lookup(%s) failed: %v", authz.PermUserRead, err)
	}
	if userRead.Description == "Re-declared by the project | 项目重新声明" {
		t.Errorf("a later declaration overwrote an earlier one | 后一次声明覆盖了前一次")
	}

	// A malformed entry fails the whole call: the table is a unit.
	if err := authz.RegisterAll([]authz.Permission{{Name: "NotAToken", Description: "x | x"}}, nil); !errors.Is(err, authz.ErrInvalidPermission) {
		t.Errorf("RegisterAll with a malformed name = %v, want %v", err, authz.ErrInvalidPermission)
	}
	if authz.Registered("NotAToken") {
		t.Errorf("a failed RegisterAll registered part of its input | 失败的 RegisterAll 注册了部分输入")
	}
}

// TestLookupUnknownAndGroups covers unknown lookups and the group view. | TestLookupUnknownAndGroups 覆盖未注册的查询与分组视图。
func TestLookupUnknownAndGroups(t *testing.T) {
	if _, err := authz.Lookup("nothing:here"); !errors.Is(err, authz.ErrUnknownPermission) {
		t.Errorf("Lookup of an unknown permission = %v, want %v", err, authz.ErrUnknownPermission)
	}
	if _, err := authz.LookupRole("nobody"); !errors.Is(err, authz.ErrUnknownRole) {
		t.Errorf("LookupRole of an unknown role = %v, want %v", err, authz.ErrUnknownRole)
	}
	groups := authz.Groups()
	want := []string{authz.GroupPermission, authz.GroupRole, authz.GroupTenant, authz.GroupUser}
	if !slices.IsSorted(groups) {
		t.Errorf("Groups() is not sorted: %v", groups)
	}
	for _, g := range want {
		if !slices.Contains(groups, g) {
			t.Errorf("Groups() is missing %q: %v", g, groups)
		}
	}
	for _, p := range authz.Permissions() {
		if !slices.Contains(groups, p.Group) {
			t.Errorf("permission %q files under the unknown group %q", p.Name, p.Group)
		}
	}
}

// TestNameShapes covers both name predicates. | TestNameShapes 覆盖两种名字判定。
func TestNameShapes(t *testing.T) {
	cases := []struct {
		in       string
		permName bool
		roleName bool
	}{
		{"user:create", true, false},
		{"user:create-any", true, false},
		{"user", false, true},
		{"admin", false, true},
		{"User", false, false},
		{"", false, false},
		{"user:Create", false, false},
		{"user:", false, false},
		{":create", false, false},
		// A media type carries a slash, which no capability token may: the shape test alone already
		// keeps MIME types out of the report. | 媒体类型带斜杠，而能力标识不允许：
		// 仅凭形态判定就已经把 MIME 类型挡在报告之外。
		{"text/html", false, false},
		{"application/json", false, false},
	}
	for _, tc := range cases {
		if got := authz.IsPermissionName(tc.in); got != tc.permName {
			t.Errorf("IsPermissionName(%q) = %v, want %v", tc.in, got, tc.permName)
		}
		if got := authz.IsRoleName(tc.in); got != tc.roleName {
			t.Errorf("IsRoleName(%q) = %v, want %v", tc.in, got, tc.roleName)
		}
	}
}

// TestGroupLabel requires every framework group to carry bilingual labels, while a project group comes back verbatim. | TestGroupLabel 要求框架的每个分组都有中英对照标签，而项目自己的分组原样返回。
func TestGroupLabel(t *testing.T) {
	for _, g := range []string{
		authz.GroupTenant, authz.GroupUser, authz.GroupRole, authz.GroupPermission,
		authz.GroupMessage, authz.GroupMessageType,
	} {
		if label := authz.GroupLabel(g); label == g {
			t.Errorf("GroupLabel(%q) has no translation | 分组 %q 没有译文", g, g)
		}
	}
	if got := authz.GroupLabel("invoice"); got != "invoice" {
		t.Errorf("GroupLabel(invoice) = %q, want the name itself", got)
	}
}

// names projects a permission table onto its names. | names 把权限表投影为名字列表。
func names(perms []authz.Permission) []string {
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		out = append(out, p.Name)
	}
	return out
}
