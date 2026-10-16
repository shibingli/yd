package auth

import (
	"context"
	"strings"
	"time"

	"github.com/shibingli/yd/errx"
)

// Tenant is one isolated workspace. | Tenant 是一个相互隔离的工作空间。
//
// A tenant is the hard boundary of the whole package: users, roles and tokens
// only ever resolve inside exactly one tenant. | 租户是本包全部能力的硬边界：用户、角色与令牌
// 只在唯一一个租户内解析。
type Tenant struct {
	// CreatedAt is the creation time in the Service clock. | CreatedAt 是 Service 时钟下的创建时间。
	CreatedAt time.Time
	// ID is the generated identifier, prefixed with "tn_". | ID 是生成的标识符，前缀为 "tn_"。
	ID string
	// Slug is the unique human-readable key of the tenant. | Slug 是租户唯一的可读键。
	Slug string
	// Name is the display name. | Name 是展示名称。
	Name string
	// Status is the lifecycle state. | Status 是生命周期状态。
	Status Status
}

// CreateTenant creates a tenant with an active status. | CreateTenant 创建一个处于激活状态的租户。
//
// An empty slug or name is refused with ErrTenantRequired and a duplicate slug
// with ErrConflict. | slug 或 name 为空时以 ErrTenantRequired 拒绝，slug 重复时以 ErrConflict 拒绝。
func (s *Service) CreateTenant(ctx context.Context, slug, name string) (*Tenant, error) {
	slug = strings.TrimSpace(slug)
	name = strings.TrimSpace(name)
	if slug == "" || name == "" {
		return nil, errx.Detail(ErrTenantRequired, "slug and name must not be empty | slug 与 name 不得为空")
	}
	id, err := newID(tenantIDPrefix)
	if err != nil {
		return nil, err
	}
	t := &Tenant{
		ID:        id,
		Slug:      slug,
		Name:      name,
		Status:    StatusActive,
		CreatedAt: s.now(),
	}
	if err := s.store.CreateTenant(ctx, t); err != nil {
		return nil, err
	}
	return cloneTenant(t), nil
}

// GetTenant returns one tenant by id, or ErrNotFound. | GetTenant 按 ID 返回单个租户，不存在时返回 ErrNotFound。
func (s *Service) GetTenant(ctx context.Context, tenantID string) (*Tenant, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	return s.store.GetTenant(ctx, tenantID)
}

// ListTenants returns every tenant ordered by creation time. | ListTenants 按创建时间返回全部租户。
func (s *Service) ListTenants(ctx context.Context) ([]*Tenant, error) {
	return s.store.ListTenants(ctx)
}

// cloneTenant returns a deep copy of t, or nil when t is nil. | cloneTenant 返回 t 的深拷贝，t 为空时返回 nil。
func cloneTenant(t *Tenant) *Tenant {
	if t == nil {
		return nil
	}
	dup := *t
	return &dup
}
