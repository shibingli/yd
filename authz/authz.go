// Package authz is the single source of truth for the roles and the permissions of
// an application. | Package authz 是应用角色与权限的单一事实来源。
//
// Every capability token is declared here exactly once: the back end decides with
// authz.Require / authz.Allow / authz.Can, the templates render with the helpers
// bound by the render package, the front end receives the same registry as inline
// JSON, and the yd authz check tool compares every reference in a project against
// it. A token that is spelled out anywhere else is a bug the tool reports, because
// a misspelled token never fails to compile: it silently denies forever. | 每一条能力标识
// 只在这里声明一次：后端用 authz.Require / authz.Allow / authz.Can 判定，模板用 render 包绑定的
// 辅助函数渲染，前端以内联 JSON 收到同一份注册表，yd authz check 则把项目里的每一处引用与它比对。
// 在别处写出的标识都是工具会报告出来的缺陷，因为写错的标识不会编译失败：它只会永远静默地拒绝。
//
// Typical wiring | 典型装配：
//
//	// A project declares its own vocabulary during package initialisation; names the framework
//	// already knows are merged rather than reported as duplicates.
//	// 项目在包初始化时声明自己的词汇表；框架已知的同名能力会被并入而不是报重复。
//	authz.RegisterAll([]authz.Permission{
//		{Name: authz.PermUserCreate, Description: "Create a user | 创建用户"},
//		{Name: "invoice:approve", Description: "Approve an invoice | 审批发票"},
//	}, nil)
//
//	// One line decides on the back end | 后端一行判定
//	r.GET("/invoices", listInvoices, authz.Require(authz.PermInvoiceApprove))
//	// or, inside a handler that needs the resource first | 或在需要先取资源的处理器内：
//	if err := authz.Allow(c, authz.PermInvoiceApprove); err != nil { return err }
//
//	// The templates and the front end | 模板与前端
//	// {{ if can .Perm.UserCreate }}…{{ end }}
//	// <button data-yd-perm="{{ perm "PermUserDelete" }}" data-yd-perm-mode="hide">…</button>
//	// yd.can(yd.PERM.USER_CREATE)
//
// The red line | 红线：
//
// A decision taken in the browser is a convenience, never a security boundary.
// Hiding a button does not forbid the operation, and the injected payload is
// editable by whoever receives it. Every protected endpoint must still be guarded
// on the server with Require or Allow; the front-end hooks exist so that a user
// never sees an action the server would refuse. | 浏览器里的判定只是体验优化，绝不是安全边界。
// 隐藏按钮不等于禁止操作，注入的载荷对收到它的人也是可编辑的。每个受保护端点仍必须由服务端用
// Require 或 Allow 守卫；前端钩子的存在只是为了让用户看不到服务端本来就会拒绝的操作。
package authz

import (
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/shibingli/yd/errx"
)

// Permission is one capability token of the registry. | Permission 是注册表中的一条能力标识。
type Permission struct {
	// Name is the capability token, shaped "resource:action". | Name 是形如 "resource:action" 的能力标识。
	Name string
	// Group is the resource half, which both sorting and the yd authz list output use. It is
	// derived from Name whenever a declaration leaves it empty. | Group 是资源分组，即标识中冒号前的那一半，
	// 排序与 yd authz list 的输出都按它分组；声明时留空则从 Name 推导。
	Group string
	// Description is the bilingual note, like every other text of the repository. | Description 是中英对照的说明，与仓库其它文案同一规则。
	Description string
}

// Role is one role name of the registry. | Role 是注册表中的一个角色名。
//
// The registry stores names, not permission sets: which permissions a role grants
// belongs to the tenant data of the auth package, because two tenants may bind the
// same name to different capabilities. | 注册表只存名字，不存权限集合：角色授予哪些权限属于
// auth 包的租户数据——同一个角色名在两个租户下可以绑定不同的能力。
type Role struct {
	// Name is the role name, unique inside the registry. | Name 是角色名，在注册表内唯一。
	Name string
	// Description is the bilingual note. | Description 是中英对照的说明。
	Description string
}

// permissionNameRe matches the shape a capability token must have. | permissionNameRe 匹配能力标识必须满足的形态。
//
// It is deliberately strict and shared with the static checker: the shape is what
// tells a capability apart from an arbitrary string in a project's sources. | 它刻意从严，
// 并与静态检查器共用：正是这个形态把能力标识与项目源码里的普通字符串区分开来。
var permissionNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*:[a-z][a-z0-9_-]*$`)

// roleNameRe matches the shape a role name must have. | roleNameRe 匹配角色名必须满足的形态。
var roleNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// registry is the process-wide table of capabilities and roles. | registry 是进程级的权限与角色表。
//
// The maps are read far more often than they are written - every registration happens during
// initialisation - so an RWMutex is enough and a copy-on-write table would only add allocation to
// the read path. | 这些表被读取的次数远多于被写入——所有注册都发生在初始化期——因此 RWMutex 足够，
// 写时复制反而会给读路径增加分配。
type registry struct {
	// perms maps a capability token to its record. | perms 把能力标识映射到它的记录。
	perms map[string]Permission
	// roles maps a role name to its record. | roles 把角色名映射到它的记录。
	roles map[string]Role
	// mu guards the two maps above and is kept last to shrink the pointer bytes the
	// collector has to scan. | mu 保护上面两张表，并置于最后以减少回收器需要扫描的指针字节数。
	mu sync.RWMutex
}

// global is the registry every helper of this package reads. | global 是本包所有辅助函数读取的那张注册表。
var global = &registry{perms: map[string]Permission{}, roles: map[string]Role{}}

// Register declares one capability. | Register 声明一条能力。
//
// A name outside the "resource:action" shape is ErrInvalidPermission and a name
// that is already taken is ErrDuplicatePermission; the second case is what keeps a
// capability from being silently redefined halfway through a project. | 名字不符合
// "resource:action" 形态时返回 ErrInvalidPermission，名字已被占用时返回 ErrDuplicatePermission；
// 后者正是「能力不会在项目中途被静默重定义」的保证。
func Register(p Permission) error {
	p, err := normalizePermission(p)
	if err != nil {
		return err
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, dup := global.perms[p.Name]; dup {
		return errx.Detail(ErrDuplicatePermission, "name=%q", p.Name)
	}
	global.perms[p.Name] = p
	return nil
}

// RegisterRole declares one role name. | RegisterRole 声明一个角色名。
//
// It reports ErrInvalidRole for a name outside the shape and ErrDuplicateRole for a name that is
// already taken, exactly like Register does for capabilities. | 名字不符合形态时返回 ErrInvalidRole，
// 名字已被占用时返回 ErrDuplicateRole，与 Register 对能力的处理完全一致。
func RegisterRole(r Role) error {
	if err := validateRole(r); err != nil {
		return err
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, dup := global.roles[r.Name]; dup {
		return errx.Detail(ErrDuplicateRole, "name=%q", r.Name)
	}
	global.roles[r.Name] = r
	return nil
}

// RegisterAll declares a whole vocabulary, leaving names that are already known
// untouched. | RegisterAll 声明一整套权限与角色字典，已存在的名字原样保留。
//
// It is the entry point generated projects use: a project declares its own
// capabilities and calls this once during package initialisation, and a name the
// framework already declares ("user:read" and its family) is not a conflict but
// the same capability seen from two places. Unlike Register it never reports
// ErrDuplicatePermission, and unlike Register it is only ever about a whole
// dictionary, never about one token. | 它是生成项目的入口：项目声明自己的能力并在包初始化时调用一次；
// 框架已声明的同名能力（"user:read" 一族）不是冲突，而是同一能力在两处的表述。与 Register 不同，
// 它从不返回 ErrDuplicatePermission，且它面对的一直是整本字典而不是单个标识。
//
// The whole table is validated before anything is written, so a rejected vocabulary leaves the
// registry exactly as it was. | 整张表会先全部校验再写入，因此被拒绝的词汇表不会给注册表留下任何改动。
func RegisterAll(perms []Permission, roles []Role) error {
	normalized := make([]Permission, 0, len(perms))
	for _, p := range perms {
		n, err := normalizePermission(p)
		if err != nil {
			return err
		}
		normalized = append(normalized, n)
	}
	for _, r := range roles {
		if err := validateRole(r); err != nil {
			return err
		}
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	for _, p := range normalized {
		if _, known := global.perms[p.Name]; known {
			continue
		}
		global.perms[p.Name] = p
	}
	for _, r := range roles {
		if _, known := global.roles[r.Name]; known {
			continue
		}
		global.roles[r.Name] = r
	}
	return nil
}

// Lookup returns the record of a capability. | Lookup 返回一条能力的记录。
//
// An unknown name is ErrUnknownPermission, which is what the checker and the
// guards report instead of silently denying. | 名字未知时返回 ErrUnknownPermission，
// 检查器与守卫报告的就是它，而不是静默拒绝。
func Lookup(name string) (Permission, error) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	p, ok := global.perms[name]
	if !ok {
		return Permission{}, errx.Detail(ErrUnknownPermission, "name=%q", name)
	}
	return p, nil
}

// LookupRole returns the record of a role name. | LookupRole 返回角色名的记录。
func LookupRole(name string) (Role, error) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	r, ok := global.roles[name]
	if !ok {
		return Role{}, errx.Detail(ErrUnknownRole, "name=%q", name)
	}
	return r, nil
}

// Registered reports whether a capability is declared. | Registered 报告一条能力是否已声明。
func Registered(name string) bool {
	global.mu.RLock()
	defer global.mu.RUnlock()
	_, ok := global.perms[name]
	return ok
}

// RoleRegistered reports whether a role name is declared. | RoleRegistered 报告一个角色名是否已声明。
func RoleRegistered(name string) bool {
	global.mu.RLock()
	defer global.mu.RUnlock()
	_, ok := global.roles[name]
	return ok
}

// Permissions returns a copy of every declared capability, sorted by group and then
// by name. | Permissions 返回全部已声明能力的副本，按分组再按名字稳定排序。
//
// The slice is a copy: mutating it never reaches the registry, so the returned
// value can be handed to a renderer or to a caller without any further care. | 返回的是副本：
// 改动它不会影响注册表，因此可以直接交给渲染器或调用方，无需额外小心。
func Permissions() []Permission {
	global.mu.RLock()
	out := make([]Permission, 0, len(global.perms))
	for _, p := range global.perms {
		out = append(out, p)
	}
	global.mu.RUnlock()
	sortPermissions(out)
	return out
}

// Roles returns a copy of every declared role, sorted by name. | Roles 返回全部已声明角色的副本，按名字排序。
func Roles() []Role {
	global.mu.RLock()
	out := make([]Role, 0, len(global.roles))
	for _, r := range global.roles {
		out = append(out, r)
	}
	global.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Groups returns the distinct groups of the registry, sorted. | Groups 返回注册表中去重后并排序的全部分组。
func Groups() []string {
	perms := Permissions()
	seen := make(map[string]struct{}, len(perms))
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		if _, dup := seen[p.Group]; dup {
			continue
		}
		seen[p.Group] = struct{}{}
		out = append(out, p.Group)
	}
	sort.Strings(out)
	return out
}

// sortPermissions orders a capability slice by group, then by name. | sortPermissions 按分组再按名字对能力切片排序。
func sortPermissions(perms []Permission) {
	sort.Slice(perms, func(i, j int) bool {
		if perms[i].Group != perms[j].Group {
			return perms[i].Group < perms[j].Group
		}
		return perms[i].Name < perms[j].Name
	})
}

// normalizePermission validates one capability and fills in its group. | normalizePermission 校验一条能力并补全其分组。
func normalizePermission(p Permission) (Permission, error) {
	if !IsPermissionName(p.Name) {
		return Permission{}, errx.Detail(ErrInvalidPermission, "name=%q", p.Name)
	}
	if strings.TrimSpace(p.Description) == "" {
		return Permission{}, errx.Detail(ErrInvalidPermission, "name=%q has no description", p.Name)
	}
	return withDerivedGroup(p), nil
}

// withDerivedGroup fills Group from the resource half of the name when it is empty. | withDerivedGroup 在分组为空时用名字中冒号前的资源段填充它。
func withDerivedGroup(p Permission) Permission {
	if p.Group != "" {
		return p
	}
	if i := strings.IndexByte(p.Name, ':'); i > 0 {
		p.Group = p.Name[:i]
	}
	return p
}

// validateRole rejects a role name outside the declared shape. | validateRole 拒绝不符合声明形态的角色名。
func validateRole(r Role) error {
	if !IsRoleName(r.Name) {
		return errx.Detail(ErrInvalidRole, "name=%q", r.Name)
	}
	if strings.TrimSpace(r.Description) == "" {
		return errx.Detail(ErrInvalidRole, "name=%q has no description", r.Name)
	}
	return nil
}

// IsPermissionName reports whether s has the shape of a capability token. | IsPermissionName 报告 s 是否具备能力标识的形态。
//
// The static checker shares this predicate, so "what looks like a permission" is
// answered in exactly one place. | 静态检查器共用该判定，因此「什么看起来像权限」只有一处答案。
func IsPermissionName(s string) bool { return permissionNameRe.MatchString(s) }

// IsRoleName reports whether s has the shape of a role name. | IsRoleName 报告 s 是否具备角色名的形态。
func IsRoleName(s string) bool { return roleNameRe.MatchString(s) }
