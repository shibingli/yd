package gormstore

import (
	"context"

	"gorm.io/gorm"

	"github.com/shibingli/yd/auth"
	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/logx"
)

// CreateTenant stores a new tenant. | CreateTenant 存入新租户。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_tenants WHERE id = ?
//	SELECT count(*) FROM yd_auth_tenants WHERE slug_key = ?
//	INSERT INTO yd_auth_tenants (...) VALUES (...)
//
// The two counts and the insert run inside one transaction: the counts produce the
// precise diagnostics of the memory implementation, while yd_idx_auth_tenants_slug is
// what actually refuses a racing duplicate. | 两个 count 与插入在同一事务内执行：count 复现内存实现的精确报文，
// 而真正拒绝并发重复写入的是 yd_idx_auth_tenants_slug。
func (s *Store) CreateTenant(ctx context.Context, t *auth.Tenant) error {
	if t == nil || t.ID == "" {
		return errx.Detail(auth.ErrInvalidInput, "tenant requires an id | 租户必须带 ID")
	}
	row := tenantToRow(t)
	if row.SlugKey == "" {
		return errx.Detail(auth.ErrInvalidInput, "tenant requires a slug | 租户必须带 Slug")
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		taken, err := countRows(tx, &tenantRow{}, "id = ?", row.ID)
		if err != nil {
			return err
		}
		if taken > 0 {
			return errx.Detail(auth.ErrConflict, "tenant id %[1]q already exists | 租户 ID %[1]q 已存在", row.ID)
		}
		taken, err = countRows(tx, &tenantRow{}, "slug_key = ?", row.SlugKey)
		if err != nil {
			return err
		}
		if taken > 0 {
			return errx.Detail(auth.ErrConflict, "tenant slug %[1]q already exists | 租户 Slug %[1]q 已存在", row.SlugKey)
		}
		if err := tx.Create(&row).Error; err != nil {
			if isDuplicate(err) {
				s.log.Debug("the unique index rejected a duplicate tenant | 唯一索引拒绝了重复租户",
					logx.Str("tenant_id", row.ID))
				return errx.WrapDetail(err, auth.ErrConflict, "tenant id or slug already exists | 租户 ID 或 Slug 已存在")
			}
			return err
		}
		return nil
	})
}

// GetTenant returns one tenant by id. | GetTenant 按 ID 返回单个租户。
//
// SQL | SQL:
//
//	SELECT * FROM yd_auth_tenants WHERE id = ? ORDER BY yd_auth_tenants.id LIMIT 1
//
// A missing tenant is reported as auth.ErrNotFound, and the returned value is a
// fresh struct: no caller ever holds a reference into the adapter. | 租户不存在时报 auth.ErrNotFound；
// 返回值是新结构体，调用方永远拿不到指向适配器内部的引用。
func (s *Store) GetTenant(ctx context.Context, tenantID string) (*auth.Tenant, error) {
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	var row tenantRow
	err := s.db.WithContext(ctx).Where("id = ?", tenantID).First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return tenantFromRow(&row), nil
}

// ListTenants returns every tenant ordered by creation time and then by id. | ListTenants 按创建时间、再按 ID 返回全部租户。
//
// SQL | SQL:
//
//	SELECT * FROM yd_auth_tenants ORDER BY created_at ASC, id ASC
//
// yd_idx_auth_tenants_created_at covers the ordering, and the id tiebreaker keeps the
// order deterministic when two tenants share a timestamp — the memory store sorts
// by exactly the same pair. | yd_idx_auth_tenants_created_at 支撑该排序，ID 作为次级键使时间戳相同的两个租户
// 也有确定顺序 —— 与内存实现排序所依据的字段对完全相同。
func (s *Store) ListTenants(ctx context.Context) ([]*auth.Tenant, error) {
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	var rows []tenantRow
	if err := s.db.WithContext(ctx).Order("created_at ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*auth.Tenant, 0, len(rows))
	for i := range rows {
		out = append(out, tenantFromRow(&rows[i]))
	}
	return out, nil
}
