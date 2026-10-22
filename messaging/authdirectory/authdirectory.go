// Package authdirectory adapts security/auth.Service to messaging.Directory. | Package authdirectory
// 把 security/auth.Service 适配为 messaging.Directory。
//
// The adapter lives here rather than in the domain so messaging stays free of the auth package: a
// project with another identity store implements the same two-method interface and nothing else
// changes. | 适配器放在这里而不是领域层，使 messaging 不依赖 auth 包：使用其它身份存储的项目
// 只需实现同样的两方法接口，其余代码不变。
package authdirectory

import (
	"context"
	"sort"
	"strings"

	"github.com/shibingli/yd/messaging"
	"github.com/shibingli/yd/security/auth"
	"github.com/shibingli/ydx/errs"
)

// ErrDirectory reports an unusable auth service. | ErrDirectory 表示鉴权服务不可用。
var ErrDirectory = errs.New("user directory failed | 用户目录查询失败")

// Directory implements messaging.Directory over an auth service. | Directory 基于鉴权服务实现 messaging.Directory。
type Directory struct {
	// auth is the framework identity service. | auth 是框架身份服务。
	auth *auth.Service
}

// New returns a directory over svc. | New 返回基于 svc 的目录。
func New(svc *auth.Service) (*Directory, error) {
	if svc == nil {
		return nil, ErrDirectory
	}
	return &Directory{auth: svc}, nil
}

// Search returns active users whose name, sign-in name or e-mail contains the keyword. |
// Search 返回名称、登录名或邮箱包含关键字的活跃用户。
func (d *Directory) Search(ctx context.Context, tenantID, keyword, excludeID string, limit int) ([]messaging.Profile, error) {
	if d == nil || d.auth == nil {
		return nil, ErrDirectory
	}
	users, err := d.auth.ListUsers(ctx, tenantID)
	if err != nil {
		return nil, errs.Wrap(err, ErrDirectory)
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if limit <= 0 {
		limit = 20
	}
	out := make([]messaging.Profile, 0, limit)
	for _, user := range users {
		if user.ID == excludeID || user.Status != auth.StatusActive {
			continue
		}
		profile := profileOf(user)
		if keyword != "" && !matches(profile, keyword) {
			continue
		}
		out = append(out, profile)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Display()) < strings.ToLower(out[j].Display())
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Profiles resolves a batch of user ids inside one tenant. Unknown ids are omitted. | Profiles 批量解析同一租户内的用户 ID，未知 ID 被省略。
func (d *Directory) Profiles(ctx context.Context, tenantID string, ids []string) (map[string]messaging.Profile, error) {
	if d == nil || d.auth == nil {
		return nil, ErrDirectory
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return map[string]messaging.Profile{}, nil
	}
	users, err := d.auth.ListUsers(ctx, tenantID)
	if err != nil {
		return nil, errs.Wrap(err, ErrDirectory)
	}
	out := make(map[string]messaging.Profile, len(wanted))
	for _, user := range users {
		if _, ok := wanted[user.ID]; !ok {
			continue
		}
		out[user.ID] = profileOf(user)
	}
	return out, nil
}

// profileOf converts one auth user. | profileOf 转换一个鉴权用户。
func profileOf(user *auth.User) messaging.Profile {
	return messaging.Profile{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Email:       user.Email,
		Active:      user.Status == auth.StatusActive,
	}
}

// matches reports whether the keyword appears in a searchable field. | matches 报告关键字是否出现在可检索字段中。
func matches(profile messaging.Profile, keyword string) bool {
	for _, value := range []string{profile.Username, profile.DisplayName, profile.Email} {
		if strings.Contains(strings.ToLower(value), keyword) {
			return true
		}
	}
	return false
}
