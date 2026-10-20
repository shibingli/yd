package authz

// This file is the default authorization table of the framework: it answers "which capabilities does
// each built-in role grant" for a project that has just been generated. | 本文件是框架的默认授权表：
// 它对一个刚生成的项目回答「每个内置角色授予哪些能力」。
//
// consts.go answers a different question - "which capabilities exist" - and the two tables belong to
// one package: a role template that granted a token nobody declared would compile, start, and then
// deny every request forever, so the two have to be checked against each other, which is exactly what
// the tests of this package do. Keeping them here is also what lets the seed of a generated project be
// written without a single literal: it walks BuiltinRoles, BuiltinPermissions and this table. |
// consts.go 回答的是另一个问题——「有哪些能力」——而两张表必须同处一个包：一份授予了无人声明标识的
// 角色模板会编译、会启动，然后永远拒绝每一个请求，因此两者必须互相校验，这正是本包测试所做的事。
// 把它们放在一起，也正是生成项目的种子数据一个标识字面量都不必写的原因：它遍历 BuiltinRoles、
// BuiltinPermissions 与本表即可。

// Grant binds one role to the capabilities it grants by default. | Grant 把一个角色绑定到它默认授予的能力。
//
// The string comes first so that the last pointer word of the struct sits earlier and the collector
// scans fewer bytes on every pass over a grant table (see the betteralign check of CLAUDE.md §8). |
// 字符串放在最前，使结构体里最后一个指针字更靠前，回收器每次扫过授权表时少扫一些字节
// （见 CLAUDE.md §8 的 betteralign 检查）。
type Grant struct {
	// Role is a role name, spelled exactly as the registry spells it. | Role 是角色名，与注册表拼写完全一致。
	Role string
	// Permissions lists the capability tokens granted to the role, in registry order. | Permissions 按注册表顺序列出授予该角色的能力标识。
	Permissions []string
}

// builtinGrants is the default authorization table. | builtinGrants 是默认授权表。
//
// Every entry is written out in full rather than derived from a rule such as "admin is everything but
// ...": a capability added to the registry later then forces a deliberate decision here, and the tests
// of this package fail until that decision is made. | 每一条都完整写出，而不是由「admin 就是除了……
// 之外的一切」这类规则推导：这样，日后新增一条能力就必然要在这里做出一次刻意的决定，
// 而在决定做出之前，本包的测试会一直失败。
//
// The role each entry binds is the one the seed of a generated project writes into every tenant, and
// the owner is the role the seeded administrator holds. | 每条记录绑定的角色，就是生成项目为每个租户
// 写入内置角色时使用的名字，而所有者正是种子管理员所持有的那个角色。
var builtinGrants = []Grant{
	{
		// The owner holds every capability, and it is the role `yd new` gives the administrator it
		// seeds: a tenant whose owner could not do something would have nobody able to do it. |
		// 所有者持有全部能力，而 `yd new` 种下的管理员拿到的就是这个角色：
		// 如果所有者都做不了某件事，那个租户里就没有人能做。
		Role: RoleOwner,
		Permissions: []string{
			PermTenantCreate, PermTenantRead, PermTenantUpdate, PermTenantDelete, PermTenantManage,
			PermUserCreate, PermUserRead, PermUserUpdate, PermUserDelete,
			PermRoleCreate, PermRoleRead, PermRoleUpdate, PermRoleDelete, PermRoleManage,
			PermPermissionRead, PermPermissionManage,
			PermMessageRead, PermMessageSend, PermMessageUpdate, PermMessageDelete,
			PermMessageSystem, PermMessageManage, PermMessageTypeRead, PermMessageTypeManage,
		},
	},
	{
		// Two capabilities are withheld from the administrator, and both withholdings are the point of
		// the role rather than an oversight. | 有两项能力被刻意排除在管理员之外，
		// 这两处排除正是这个角色的定义，而不是疏漏。
		//
		// tenant:delete is withheld because a tenant is the boundary every other capability is scoped
		// to, and only the owner moves that boundary. | tenant:delete 被排除，是因为租户是其它所有能力的
		// 作用边界，而只有所有者能移动这条边界。
		//
		// The framework never expands an umbrella: tenant:manage is decided on its own and does not
		// hand out tenant:delete as well, which is why the umbrella is granted here while the
		// destructive token is not. | 框架从不展开伞形能力：tenant:manage 单独判定，
		// 不会顺带给出 tenant:delete，因此这里授予伞形能力而不授予那个破坏性标识。
		//
		// permission:manage is withheld because retiring a capability changes what every role of every
		// tenant means, so it travels with a deployment rather than with a tenant administration
		// action; reading the registry stays available, because a matrix nobody can read is useless. |
		// permission:manage 被排除，是因为废止一项能力会改变每个租户里每个角色的含义，
		// 因此它随发布走，而不是随租户管理动作走；注册表的读取则保留，因为一张没人能读的权限矩阵毫无用处。
		Role: RoleAdmin,
		Permissions: []string{
			PermTenantCreate, PermTenantRead, PermTenantUpdate, PermTenantManage,
			PermUserCreate, PermUserRead, PermUserUpdate, PermUserDelete,
			PermRoleCreate, PermRoleRead, PermRoleUpdate, PermRoleDelete, PermRoleManage,
			PermPermissionRead,
			PermMessageRead, PermMessageSend, PermMessageUpdate, PermMessageDelete,
			PermMessageSystem, PermMessageManage, PermMessageTypeRead, PermMessageTypeManage,
		},
	},
	{
		// The auditor reads every resource and changes none: the four ":read" capabilities are the whole
		// grant, and a capability is added to it only when reading it is what the capability means. |
		// 审计员可读全部资源、不改动任何一项：四项 ":read" 能力就是全部授权，
		// 只有「读取」正是该能力含义时才会被加进来。
		//
		// The list is written out even though it currently equals "every registered capability ending in
		// :read": a new read capability must be a decision here, not an automatic widening of the
		// auditor's reach. | 这份清单完整写出，尽管它目前恰好等于「注册表中所有以 :read 结尾的能力」：
		// 新增一项读能力必须在这里成为一次决定，而不是审计员权限的自动扩张。
		Role: RoleAuditor,
		Permissions: []string{
			PermTenantRead, PermUserRead, PermRoleRead, PermPermissionRead,
			PermMessageRead, PermMessageTypeRead,
		},
	},
	{
		// The member holds exactly the message-centre capabilities whose scope is the author
		// themselves. | 普通成员恰好持有那些作用域就是作者本人的消息中心能力。
		//
		// This is the one deliberate exception to "the member grants nothing": message:update and
		// message:delete are not tenant-wide grants, because the messaging service refuses a message
		// the actor did not send and refuses a recall after the configured window. message:read and
		// message:send similarly operate on conversations the actor belongs to and on accepted
		// friendships only. Every other capability stays withheld, because it really is tenant-wide. |
		// 这是「成员不授予任何能力」的唯一一处刻意例外：message:update 与 message:delete 不是租户级授权，
		// 因为消息服务会拒绝主体没有发送过的消息，也会拒绝超出配置窗口的撤回。message:read 与 message:send
		// 同样只作用于主体所属的会话与已接受的好友关系。其余能力仍然不授予，因为它们确实是租户级的。
		Role: RoleMember,
		Permissions: []string{
			PermMessageRead, PermMessageSend, PermMessageUpdate, PermMessageDelete,
		},
	},
}

// BuiltinGrants returns a copy of the default authorization table. | BuiltinGrants 返回默认授权表的副本。
//
// The copy is deep: neither replacing an entry nor appending to a permission list of the returned
// value can reach the table, so a caller may hand it to a renderer or to a seeder without any further
// care. | 这是深拷贝：无论替换返回值的某条记录，还是往它的权限清单里追加，
// 都碰不到表本身，因此调用方可以直接把它交给渲染器或种子写入而不必额外小心。
func BuiltinGrants() []Grant {
	out := make([]Grant, 0, len(builtinGrants))
	for _, g := range builtinGrants {
		out = append(out, copyGrant(g))
	}
	return out
}

// GrantOf returns the default grant of one role. | GrantOf 返回某个角色的默认授权。
//
// The role reference accepts the four spellings ResolveRole understands ("admin", "RoleAdmin",
// "Admin", "ADMIN"), so a caller that received it from a template or from a script need not normalise
// it first. A role the framework does not know - a project's own - reports false, because it has no
// default grant at all. | 角色引用接受 ResolveRole 认识的四种写法（"admin"、"RoleAdmin"、"Admin"、
// "ADMIN"），因此从模板或脚本收到引用的调用方不必先归一化。框架不认识的角色（项目自建的）
// 返回 false，因为它根本没有默认授权。
func GrantOf(role string) (Grant, bool) {
	name, err := ResolveRole(role)
	if err != nil {
		return Grant{}, false
	}
	for _, g := range builtinGrants {
		if g.Role == name {
			return copyGrant(g), true
		}
	}
	return Grant{}, false
}

// copyGrant returns a deep copy of one grant. | copyGrant 返回一条授权的深拷贝。
func copyGrant(g Grant) Grant {
	perms := make([]string, len(g.Permissions))
	copy(perms, g.Permissions)
	return Grant{Role: g.Role, Permissions: perms}
}
