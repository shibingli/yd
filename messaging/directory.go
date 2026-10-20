package messaging

import "context"

// Directory resolves the tenant users the message centre needs for lists, search and friend
// selection. The implementation lives outside this package so the domain stays free of the auth
// package and of any specific user store. | Directory 解析消息中心在列表、检索与好友选择中需要的租户用户。
// 实现位于本包之外，使领域层不依赖 auth 包或任何具体用户存储。
type Directory interface {
	// Search returns active users of the tenant whose name, sign-in name or e-mail contains the
	// keyword, excluding excludeID and limited to limit entries. | Search 返回名称、登录名或邮箱包含关键字的本租户活跃用户，
	// 排除 excludeID 并限制为 limit 条。
	Search(ctx context.Context, tenantID, keyword, excludeID string, limit int) ([]Profile, error)
	// Profiles resolves a batch of user ids inside one tenant. Unknown ids are omitted. | Profiles 批量解析同一租户内的用户 ID。
	// 未知 ID 会被省略。
	Profiles(ctx context.Context, tenantID string, ids []string) (map[string]Profile, error)
}
