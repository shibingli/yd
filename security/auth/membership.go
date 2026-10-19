package auth

import (
	"context"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// ListUserTenants returns every tenant the user belongs to, ordered by creation time.
// | ListUserTenants 按创建时间返回该用户隶属的全部租户。
//
// The tenant switcher reads this list, and the membership check is the only gate:
// a tenant missing from the result can never be activated. | 租户切换器读取这份清单，
// 成员关系是唯一的闸门：不在结果里的租户永远无法被激活。
func (s *Service) ListUserTenants(ctx context.Context, userID string) ([]*Tenant, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, errx.Detail(ErrNotFound, "empty user id | 用户 ID 为空")
	}
	return s.store.ListUserTenants(ctx, userID)
}

// AddUserTenant grants the user a membership of another tenant. | AddUserTenant 授予用户在另一个租户的成员关系。
//
// Every listed role must exist inside that tenant, so a caller cannot probe role
// names of a tenant by watching which assignment succeeds. | 列出的每个角色都必须存在于该租户，
// 调用方因此无法通过观察哪次分配成功来探测租户的角色名。
func (s *Service) AddUserTenant(ctx context.Context, userID, tenantID string, roles []string) error {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	if _, err := s.store.GetTenant(ctx, tenantID); err != nil {
		return err
	}
	names, err := s.resolveRoles(ctx, tenantID, roles)
	if err != nil {
		return err
	}
	return s.store.AddUserTenant(ctx, userID, tenantID, names)
}

// RemoveUserTenant drops a membership that is neither the home nor the default
// tenant; the store refuses those two with ErrConflict. | RemoveUserTenant 移除既非身份租户也非默认租户的
// 成员关系；仓储层对这两种情况以 ErrConflict 拒绝。
func (s *Service) RemoveUserTenant(ctx context.Context, userID, tenantID string) error {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	return s.store.RemoveUserTenant(ctx, userID, tenantID)
}

// SetDefaultTenant makes an existing membership the tenant a bare sign-in lands in.
// | SetDefaultTenant 把已有成员关系设为未指明租户时登录落入的租户。
func (s *Service) SetDefaultTenant(ctx context.Context, userID, tenantID string) error {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	return s.store.SetDefaultTenant(ctx, userID, tenantID)
}

// SwitchTenant returns the user acting in another tenant after checking that the
// membership exists. | SwitchTenant 在确认成员关系存在后返回用户在另一个租户中的视图。
//
// The caller issues a fresh token from the returned user; the previous token stays
// valid for its own tenant until it expires. | 调用方用返回的用户签发新令牌；
// 旧令牌在自身租户内仍然有效，直到过期。
func (s *Service) SwitchTenant(ctx context.Context, userID, tenantID string) (*User, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, errx.Detail(ErrNotFound, "empty user id | 用户 ID 为空")
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	u, err := s.store.GetUser(ctx, tenantID, userID)
	if err != nil {
		// A non-member and a missing user report the same sentinel, so a switch
		// cannot be used to probe another tenant's membership. | 非成员与不存在的用户
		// 报告同一个哨兵，因此切换接口不能被用来探测其它租户的成员关系。
		return nil, ErrNotFound
	}
	if u.Status != StatusActive {
		return nil, ErrUserDisabled
	}
	return u, nil
}
