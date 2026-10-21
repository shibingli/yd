package authz

// Resource groups. | 资源分组。
//
// A group is the left half of a capability token, and every permission declared below belongs to
// exactly one of these four. Keeping them as constants is what lets a caller sort, filter or
// render a group without spelling its name again. | 分组是能力标识中冒号左半边的内容，
// 下面声明的每条权限都属于这四类之一。把它们做成常量，调用方才能在不重写名字的前提下排序、
// 过滤或渲染某个分组。
const (
	// GroupTenant is the tenant resource group. | GroupTenant 是租户资源分组。
	GroupTenant = "tenant"
	// GroupUser is the user resource group. | GroupUser 是用户资源分组。
	GroupUser = "user"
	// GroupRole is the role resource group. | GroupRole 是角色资源分组。
	GroupRole = "role"
	// GroupPermission is the permission registry resource group. | GroupPermission 是权限注册表资源分组。
	GroupPermission = "permission"
	// GroupMessage is the in-app message-centre resource group. | GroupMessage 是站内消息中心资源分组。
	GroupMessage = "message"
	// GroupMessageType is the message type, group and tag catalogue resource group. | GroupMessageType 是消息类型、分组与标签目录资源分组。
	GroupMessageType = "message-type"
	// GroupCMSArticle is the CMS article and page resource group. | GroupCMSArticle 是 CMS 文章与单页资源分组。
	GroupCMSArticle = "cms-article"
	// GroupCMSCategory is the CMS category tree resource group. | GroupCMSCategory 是 CMS 分类树资源分组。
	GroupCMSCategory = "cms-category"
	// GroupCMSTag is the CMS tag resource group. | GroupCMSTag 是 CMS 标签资源分组。
	GroupCMSTag = "cms-tag"
	// GroupCMSMedia is the CMS media library resource group. | GroupCMSMedia 是 CMS 媒体库资源分组。
	GroupCMSMedia = "cms-media"
	// GroupCMSComment is the CMS comment resource group. | GroupCMSComment 是 CMS 评论资源分组。
	GroupCMSComment = "cms-comment"
	// GroupCMSSite is the CMS public-site settings resource group. | GroupCMSSite 是 CMS 公开站点设置资源分组。
	GroupCMSSite = "cms-site"
)

// groupLabels maps every framework resource group to its bilingual label. | groupLabels 把每个框架资源分组映射到中英对照标签。
var groupLabels = map[string]string{
	GroupTenant:      "tenant | 租户",
	GroupUser:        "user | 用户",
	GroupRole:        "role | 角色",
	GroupPermission:  "permission | 权限",
	GroupMessage:     "message | 消息",
	GroupMessageType: "message-type | 消息类型",
	GroupCMSArticle:  "cms-article | 内容",
	GroupCMSCategory: "cms-category | 内容分类",
	GroupCMSTag:      "cms-tag | 内容标签",
	GroupCMSMedia:    "cms-media | 媒体库",
	GroupCMSComment:  "cms-comment | 内容评论",
	GroupCMSSite:     "cms-site | 站点设置",
}

// GroupLabel returns the bilingual label of a resource group. | GroupLabel 返回资源分组的中英对照标签。
//
// A group a project declares for itself has no label here, and its own name is returned unchanged:
// inventing a translation for someone else's vocabulary is worse than showing them the word they
// chose. | 项目自行声明的分组在这里没有标签，其名字原样返回：
// 为别人的词汇表生造译文，比直接显示他们自己选的词更糟。
func GroupLabel(group string) string {
	if label, ok := groupLabels[group]; ok {
		return label
	}
	return group
}

// Capability tokens. | 能力标识。
//
// These constants are the only place in the repository where the strings themselves are written,
// which is the whole point of the package: Go code passes the constant, a template passes the
// identifier under .Perm, JavaScript passes yd.PERM, and the three can never drift apart because
// every one of them is derived from this table. | 这些常量是全仓唯一写出这些字符串的地方，
// 这正是本包存在的全部意义：Go 代码传常量、模板传 .Perm 下的标识、JavaScript 传 yd.PERM，
// 而三者永远不会漂移，因为它们都派生自这一张表。
const (
	// PermTenantCreate allows creating a tenant. | PermTenantCreate 允许创建租户。
	PermTenantCreate = "tenant:create"
	// PermTenantRead allows listing and reading tenants. | PermTenantRead 允许列出与查看租户。
	PermTenantRead = "tenant:read"
	// PermTenantUpdate allows updating a tenant. | PermTenantUpdate 允许修改租户。
	PermTenantUpdate = "tenant:update"
	// PermTenantDelete allows deleting a tenant. | PermTenantDelete 允许删除租户。
	PermTenantDelete = "tenant:delete"
	// PermTenantManage allows every tenant operation, including the lifecycle ones. | PermTenantManage 允许全部租户操作，含租户生命周期操作。
	PermTenantManage = "tenant:manage"

	// PermUserCreate allows creating a user. | PermUserCreate 允许创建用户。
	PermUserCreate = "user:create"
	// PermUserRead allows listing and reading users. | PermUserRead 允许列出与查看用户。
	PermUserRead = "user:read"
	// PermUserUpdate allows updating a user, including its roles and its status. | PermUserUpdate 允许修改用户，含角色与状态。
	PermUserUpdate = "user:update"
	// PermUserDelete allows deleting a user. | PermUserDelete 允许删除用户。
	PermUserDelete = "user:delete"

	// PermRoleCreate allows creating a role. | PermRoleCreate 允许创建角色。
	PermRoleCreate = "role:create"
	// PermRoleRead allows listing and reading roles. | PermRoleRead 允许列出与查看角色。
	PermRoleRead = "role:read"
	// PermRoleUpdate allows updating a role and its permission bindings. | PermRoleUpdate 允许修改角色及其权限绑定。
	PermRoleUpdate = "role:update"
	// PermRoleDelete allows deleting a role. | PermRoleDelete 允许删除角色。
	PermRoleDelete = "role:delete"
	// PermRoleManage allows the whole role lifecycle, including the bindings of a role. | PermRoleManage 允许角色的完整生命周期，含其权限绑定。
	PermRoleManage = "role:manage"

	// PermPermissionRead allows reading the permission registry. | PermPermissionRead 允许查看权限注册表。
	PermPermissionRead = "permission:read"
	// PermPermissionManage allows declaring and retiring capabilities. | PermPermissionManage 允许声明与废止能力。
	PermPermissionManage = "permission:manage"

	// PermMessageRead allows using the message centre and reading visible messages. | PermMessageRead 允许使用消息中心并读取可见消息。
	PermMessageRead = "message:read"
	// PermMessageSend allows sending messages to friends and groups. | PermMessageSend 允许向好友与群发送消息。
	PermMessageSend = "message:send"
	// PermMessageUpdate allows editing one's own messages inside the edit window. | PermMessageUpdate 允许在编辑窗口内编辑自己的消息。
	PermMessageUpdate = "message:update"
	// PermMessageDelete allows recalling one's own messages inside the recall window. | PermMessageDelete 允许在撤回窗口内撤回自己的消息。
	PermMessageDelete = "message:delete"
	// PermMessageSystem allows posting and revoking administrator announcements. | PermMessageSystem 允许发布与撤销管理员公告。
	PermMessageSystem = "message:system"
	// PermMessageManage allows moderating every message and pinning announcements. | PermMessageManage 允许管理全部消息并置顶公告。
	PermMessageManage = "message:manage"
	// PermMessageTypeRead allows reading the message type, group and tag catalogue. | PermMessageTypeRead 允许读取消息类型、分组与标签目录。
	PermMessageTypeRead = "message-type:read"
	// PermMessageTypeManage allows the message type, group and tag lifecycle. | PermMessageTypeManage 允许管理消息类型、分组与标签生命周期。
	PermMessageTypeManage = "message-type:manage"

	// PermCmsArticleRead allows reading every article and page in the management area. | PermCmsArticleRead 允许在管理区读取全部文章与单页。
	PermCmsArticleRead = "cms-article:read"
	// PermCmsArticleCreate allows creating articles and pages. | PermCmsArticleCreate 允许创建文章与单页。
	PermCmsArticleCreate = "cms-article:create"
	// PermCmsArticleUpdate allows editing one's own articles and pages. | PermCmsArticleUpdate 允许编辑自己的文章与单页。
	PermCmsArticleUpdate = "cms-article:update"
	// PermCmsArticleDelete allows deleting one's own articles and pages. | PermCmsArticleDelete 允许删除自己的文章与单页。
	PermCmsArticleDelete = "cms-article:delete"
	// PermCmsArticlePublish allows publishing, scheduling, unpublishing, archiving and restoring. |
	// PermCmsArticlePublish 允许发布、定时、下线、归档与恢复。
	PermCmsArticlePublish = "cms-article:publish"
	// PermCmsArticleReview allows approving or rejecting pending articles. | PermCmsArticleReview 允许通过或驳回待审文章。
	PermCmsArticleReview = "cms-article:review"
	// PermCmsArticleManage allows editing and deleting every article regardless of author. |
	// PermCmsArticleManage 允许不管作者地编辑与删除全部文章。
	PermCmsArticleManage = "cms-article:manage"

	// PermCmsCategoryRead allows reading the category tree. | PermCmsCategoryRead 允许读取分类树。
	PermCmsCategoryRead = "cms-category:read"
	// PermCmsCategoryManage allows the category lifecycle. | PermCmsCategoryManage 允许分类生命周期。
	PermCmsCategoryManage = "cms-category:manage"

	// PermCmsTagRead allows reading tags. | PermCmsTagRead 允许读取标签。
	PermCmsTagRead = "cms-tag:read"
	// PermCmsTagManage allows the tag lifecycle. | PermCmsTagManage 允许标签生命周期。
	PermCmsTagManage = "cms-tag:manage"

	// PermCmsMediaRead allows reading the media library. | PermCmsMediaRead 允许读取媒体库。
	PermCmsMediaRead = "cms-media:read"
	// PermCmsMediaUpload allows uploading media. | PermCmsMediaUpload 允许上传媒体。
	PermCmsMediaUpload = "cms-media:upload"
	// PermCmsMediaDelete allows deleting media. | PermCmsMediaDelete 允许删除媒体。
	PermCmsMediaDelete = "cms-media:delete"

	// PermCmsCommentRead allows reading comments. | PermCmsCommentRead 允许读取评论。
	PermCmsCommentRead = "cms-comment:read"
	// PermCmsCommentCreate allows posting comments. | PermCmsCommentCreate 允许发表评论。
	PermCmsCommentCreate = "cms-comment:create"
	// PermCmsCommentModerate allows approving, rejecting and deleting any comment. | PermCmsCommentModerate 允许通过、拒绝与删除任意评论。
	PermCmsCommentModerate = "cms-comment:moderate"
	// PermCmsCommentDelete allows deleting one's own comments. | PermCmsCommentDelete 允许删除自己的评论。
	PermCmsCommentDelete = "cms-comment:delete"

	// PermCmsSiteRead allows reading the public-site settings. | PermCmsSiteRead 允许读取公开站点设置。
	PermCmsSiteRead = "cms-site:read"
	// PermCmsSiteManage allows changing the public-site settings. | PermCmsSiteManage 允许修改公开站点设置。
	PermCmsSiteManage = "cms-site:manage"
)

// Role names. | 角色名。
//
// A role is a name, not a permission set: which capabilities "admin" grants depends on the tenant,
// because the same name may be bound differently in two tenants. These four are the vocabulary the
// framework and the scaffold agree on. | 角色是名字而不是权限集合："admin" 究竟授予哪些能力取决于租户，
// 因为同一个名字在两个租户下可以绑定不同的能力。这四个是框架与脚手架共同约定的词汇表。
const (
	// RoleOwner is the tenant owner. | RoleOwner 是租户所有者。
	RoleOwner = "owner"
	// RoleAdmin is the tenant administrator. | RoleAdmin 是租户管理员。
	RoleAdmin = "admin"
	// RoleAuditor is the read-only auditor. | RoleAuditor 是只读审计员。
	RoleAuditor = "auditor"
	// RoleMember is the ordinary member. | RoleMember 是普通成员。
	RoleMember = "member"
)

// builtinPermissions is the framework vocabulary, registered once at package initialisation. | builtinPermissions 是框架词汇表，在包初始化时注册一次。
//
// The group of every entry is derived from the token itself, so it is written once: a permission
// whose name says "user:…" cannot end up filed under another group. | 每条记录的分组都从标识本身推导，
// 因此只写一次：名字写着 "user:…" 的权限不可能被归到别的分组下。
var builtinPermissions = []Permission{
	{Name: PermTenantCreate, Description: "Create a tenant | 创建租户"},
	{Name: PermTenantRead, Description: "List and read tenants | 列出并查看租户"},
	{Name: PermTenantUpdate, Description: "Update a tenant | 修改租户"},
	{Name: PermTenantDelete, Description: "Delete a tenant | 删除租户"},
	{Name: PermTenantManage, Description: "Manage the tenant lifecycle | 管理租户生命周期"},

	{Name: PermUserCreate, Description: "Create a user | 创建用户"},
	{Name: PermUserRead, Description: "List and read users | 列出并查看用户"},
	{Name: PermUserUpdate, Description: "Update a user, its roles and its status | 修改用户、其角色与状态"},
	{Name: PermUserDelete, Description: "Delete a user | 删除用户"},

	{Name: PermRoleCreate, Description: "Create a role | 创建角色"},
	{Name: PermRoleRead, Description: "List and read roles | 列出并查看角色"},
	{Name: PermRoleUpdate, Description: "Update a role and its bindings | 修改角色及其绑定"},
	{Name: PermRoleDelete, Description: "Delete a role | 删除角色"},
	{Name: PermRoleManage, Description: "Manage the role lifecycle | 管理角色生命周期"},

	{Name: PermPermissionRead, Description: "Read the permission registry | 查看权限注册表"},
	{Name: PermPermissionManage, Description: "Declare and retire capabilities | 声明与废止能力"},

	{Name: PermMessageRead, Description: "Use the message centre and read visible messages | 使用消息中心并读取可见消息"},
	{Name: PermMessageSend, Description: "Send direct and group messages | 发送单聊与群聊消息"},
	{Name: PermMessageUpdate, Description: "Edit own messages inside the edit window | 在编辑窗口内编辑自己的消息"},
	{Name: PermMessageDelete, Description: "Recall own messages inside the recall window | 在撤回窗口内撤回自己的消息"},
	{Name: PermMessageSystem, Description: "Post and revoke administrator announcements | 发布与撤销管理员公告"},
	{Name: PermMessageManage, Description: "Moderate every message and pin announcements | 管理全部消息并置顶公告"},
	{Name: PermMessageTypeRead, Description: "Read message types, groups and tags | 读取消息类型、分组与标签"},
	{Name: PermMessageTypeManage, Description: "Manage the message type, group and tag lifecycle | 管理消息类型、分组与标签生命周期"},

	{Name: PermCmsArticleRead, Description: "Read every article and page in the management area | 在管理区读取全部文章与单页"},
	{Name: PermCmsArticleCreate, Description: "Create articles and pages | 创建文章与单页"},
	{Name: PermCmsArticleUpdate, Description: "Edit own articles and pages | 编辑自己的文章与单页"},
	{Name: PermCmsArticleDelete, Description: "Delete own articles and pages | 删除自己的文章与单页"},
	{Name: PermCmsArticlePublish, Description: "Publish, schedule, unpublish, archive and restore articles | 发布、定时、下线、归档与恢复文章"},
	{Name: PermCmsArticleReview, Description: "Approve or reject pending articles | 通过或驳回待审文章"},
	{Name: PermCmsArticleManage, Description: "Edit and delete every article regardless of author | 不管作者地编辑与删除全部文章"},
	{Name: PermCmsCategoryRead, Description: "Read the category tree | 读取分类树"},
	{Name: PermCmsCategoryManage, Description: "Manage the category lifecycle | 管理分类生命周期"},
	{Name: PermCmsTagRead, Description: "Read tags | 读取标签"},
	{Name: PermCmsTagManage, Description: "Manage the tag lifecycle | 管理标签生命周期"},
	{Name: PermCmsMediaRead, Description: "Read the media library | 读取媒体库"},
	{Name: PermCmsMediaUpload, Description: "Upload media | 上传媒体"},
	{Name: PermCmsMediaDelete, Description: "Delete media | 删除媒体"},
	{Name: PermCmsCommentRead, Description: "Read comments | 读取评论"},
	{Name: PermCmsCommentCreate, Description: "Post comments | 发表评论"},
	{Name: PermCmsCommentModerate, Description: "Approve, reject and delete any comment | 通过、拒绝与删除任意评论"},
	{Name: PermCmsCommentDelete, Description: "Delete own comments | 删除自己的评论"},
	{Name: PermCmsSiteRead, Description: "Read the public-site settings | 读取公开站点设置"},
	{Name: PermCmsSiteManage, Description: "Change the public-site settings | 修改公开站点设置"},
}

// builtinRoles is the framework role vocabulary. | builtinRoles 是框架角色词汇表。
var builtinRoles = []Role{
	{Name: RoleOwner, Description: "Tenant owner: holds every capability inside the tenant | 租户所有者：持有租户内的全部能力"},
	{Name: RoleAdmin, Description: "Tenant administrator: everything but deleting the tenant | 租户管理员：除删除租户外的一切"},
	{Name: RoleAuditor, Description: "Read-only auditor: reads every resource, changes none | 只读审计员：可读全部资源，不做任何修改"},
	{Name: RoleMember, Description: "Ordinary member: reads and updates its own resources | 普通成员：读取并修改自己的资源"},
}

// init registers the framework vocabulary. | init 注册框架词汇表。
//
// A failure here is a defect in this file rather than a runtime condition: the tables above are
// static, so a duplicate or a malformed name is not something a caller could repair. The error is
// therefore deliberately dropped instead of taking the process down; TestBuiltinVocabularyIsValid
// is what catches the defect. | 这里失败说明本文件有缺陷，而不是运行时状况：上面的表是静态的，
// 重复名或非法名都不是调用方能补救的问题。因此这里刻意丢弃错误而不是让进程崩溃；
// 缺陷由 TestBuiltinVocabularyIsValid 捕获。
func init() { _ = RegisterAll(builtinPermissions, builtinRoles) }

// BuiltinPermissions returns a copy of the framework capability table. | BuiltinPermissions 返回框架能力表的副本。
//
// It is what an application merges its own capabilities into, and what `yd authz list` walks. | 应用把自己的能力并入其中，`yd authz list` 也遍历它。
func BuiltinPermissions() []Permission {
	out := make([]Permission, len(builtinPermissions))
	copy(out, builtinPermissions)
	for i := range out {
		out[i] = withDerivedGroup(out[i])
	}
	return out
}

// BuiltinRoles returns a copy of the framework role table. | BuiltinRoles 返回框架角色表的副本。
func BuiltinRoles() []Role {
	out := make([]Role, len(builtinRoles))
	copy(out, builtinRoles)
	return out
}
