package gormstore

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/security/auth"
)

// CreateUser stores a new user inside its tenant. | CreateUser 在用户的租户内存入新用户。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_tenants WHERE id = ?
//	SELECT count(*) FROM yd_auth_users WHERE tenant_id = ? AND id = ?
//	SELECT count(*) FROM yd_auth_users WHERE tenant_id = ? AND username_key = ?
//	INSERT INTO yd_auth_users (...) VALUES (...)
//
// Every statement carries tenant_id, so a user can neither be written into another
// tenant nor shadow a username of another tenant: uniqueness is per tenant, and
// the memory implementation agrees. | 每条语句都带 tenant_id，因此用户既不能写进别的租户，
// 也不能顶掉别的租户的用户名：唯一性是租户内的，内存实现同样如此。
func (s *Store) CreateUser(ctx context.Context, u *auth.User) error {
	if u == nil || u.ID == "" {
		return errx.Detail(auth.ErrInvalidInput, "user requires an id | 用户必须带 ID")
	}
	if strings.TrimSpace(u.TenantID) == "" {
		return auth.ErrTenantRequired
	}
	if strings.TrimSpace(u.Username) == "" {
		return errx.Detail(auth.ErrInvalidInput, "user requires a username | 用户必须带用户名")
	}
	row, err := userToRow(u)
	if err != nil {
		return err
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		known, err := existsTenant(tx, row.TenantID)
		if err != nil {
			return err
		}
		if !known {
			// A user outside a known tenant is unreachable, so the write is refused. | 不属于已知租户的用户不可达，因此拒绝写入。
			return auth.ErrNotFound
		}
		dup, err := countRows(tx, &userRow{}, "tenant_id = ? AND id = ?", row.TenantID, row.ID)
		if err != nil {
			return err
		}
		if dup > 0 {
			return errx.Detail(auth.ErrConflict, "user id %[1]q already exists | 用户 ID %[1]q 已存在", row.ID)
		}
		dup, err = countRows(tx, &userRow{}, "tenant_id = ? AND username_key = ?", row.TenantID, row.UsernameKey)
		if err != nil {
			return err
		}
		if dup > 0 {
			return errx.Detail(auth.ErrConflict, "username %[1]q already exists | 用户名 %[1]q 已存在", row.Username)
		}
		if err := tx.Create(&row).Error; err != nil {
			if isDuplicate(err) {
				s.log.Debug("the unique index rejected a duplicate user | 唯一索引拒绝了重复用户",
					logx.Str("tenant_id", row.TenantID), logx.Str("user_id", row.ID))
				return errx.WrapDetail(err, auth.ErrConflict, "user id or username already exists | 用户 ID 或用户名已存在")
			}
			return err
		}
		return nil
	})
}

// GetUser returns the user of that tenant. | GetUser 返回该租户下的用户。
//
// SQL | SQL:
//
//	SELECT * FROM yd_auth_users WHERE tenant_id = ? AND id = ? ORDER BY yd_auth_users.id LIMIT 1
//
// A user of another tenant simply misses the WHERE clause and is reported as
// auth.ErrNotFound, exactly like a user who never existed. | 属于其它租户的用户无法命中 WHERE，
// 报 auth.ErrNotFound，与从不存在的用户完全一致。
func (s *Store) GetUser(ctx context.Context, tenantID, userID string) (*auth.User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	var row userRow
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, userID).First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return userFromRow(&row)
}

// GetUserByUsername returns the user of that tenant by username. | GetUserByUsername 按用户名返回该租户下的用户。
//
// SQL | SQL:
//
//	SELECT * FROM yd_auth_users WHERE tenant_id = ? AND username_key = ? ORDER BY yd_auth_users.id LIMIT 1
//
// The lookup goes through username_key, the folded copy that
// yd_idx_auth_users_tenant_username is unique on, so "Alice" and "alice" are one
// account inside a tenant — this is the query the sign-in path runs on every
// attempt. | 查询走 username_key —— yd_idx_auth_users_tenant_username 在其上唯一的折叠副本，
// 因此同一租户内 "Alice" 与 "alice" 是同一个账号；这正是登录路径每次都要执行的查询。
func (s *Store) GetUserByUsername(ctx context.Context, tenantID, username string) (*auth.User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	var row userRow
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND username_key = ?", tenantID, keyOf(username)).First(&row).Error
	if err != nil {
		return nil, notFound(err)
	}
	return userFromRow(&row)
}

// ListUsers returns every user of the tenant ordered by creation time and then by id. | ListUsers 按创建时间、再按 ID 返回该租户的全部用户。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_tenants WHERE id = ?
//	SELECT * FROM yd_auth_users WHERE tenant_id = ? ORDER BY created_at ASC, id ASC
//
// An unknown tenant is reported as auth.ErrNotFound, so an empty tenant and a
// wrong tenant id cannot be confused — the same distinction the memory store
// makes. | 租户未知时报 auth.ErrNotFound，因此「空租户」与「租户 ID 写错」不会被混为一谈，
// 这与内存实现的分辨方式相同。
func (s *Store) ListUsers(ctx context.Context, tenantID string) ([]*auth.User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	known, err := existsTenant(s.db.WithContext(ctx), tenantID)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, auth.ErrNotFound
	}
	var rows []userRow
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*auth.User, 0, len(rows))
	for i := range rows {
		u, err := userFromRow(&rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// UpdateUser replaces an existing user of the tenant. | UpdateUser 覆盖该租户内已存在的用户。
//
// SQL | SQL:
//
//	SELECT * FROM yd_auth_users WHERE tenant_id = ? AND id = ? ORDER BY yd_auth_users.id LIMIT 1
//	SELECT count(*) FROM yd_auth_users WHERE tenant_id = ? AND username_key = ? AND id <> ?
//	UPDATE yd_auth_users SET username = ?, username_key = ?, email = ?, display_name = ?,
//	  password_hash = ?, status = ?, encrypted_sec_key = ?, roles = ?, created_at = ?,
//	  updated_at = ? WHERE tenant_id = ? AND id = ?
//
// It is a full replacement, like the memory store: a field left empty in the
// payload is stored empty. One field is deliberately not replaced — tenant_id.
// The tenant is part of the row's identity, so a payload that names no tenant
// keeps the tenant the row lives in, and a payload that names another tenant is
// refused before any write with auth.ErrNotFound. | 与内存实现一样，它是整体覆盖：
// 载荷里留空的字段就存为空。有一个字段刻意不覆盖 —— tenant_id。租户是行的身份的一部分，
// 因此未指明租户的载荷保留该行所在的租户，而指明其它租户的载荷在任何写入之前就以 auth.ErrNotFound 拒绝。
func (s *Store) UpdateUser(ctx context.Context, tenantID string, u *auth.User) error {
	if strings.TrimSpace(tenantID) == "" {
		return auth.ErrTenantRequired
	}
	if u == nil || u.ID == "" {
		return errx.Detail(auth.ErrInvalidInput, "user requires an id | 用户必须带 ID")
	}
	if u.TenantID != "" && u.TenantID != tenantID {
		return auth.ErrNotFound
	}
	row, err := userToRow(u)
	if err != nil {
		return err
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old userRow
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, row.ID).First(&old).Error; err != nil {
			return notFound(err)
		}
		if old.UsernameKey != row.UsernameKey {
			dup, err := countRows(tx, &userRow{}, "tenant_id = ? AND username_key = ? AND id <> ?", tenantID, row.UsernameKey, row.ID)
			if err != nil {
				return err
			}
			if dup > 0 {
				return errx.Detail(auth.ErrConflict, "username %[1]q already exists | 用户名 %[1]q 已存在", row.Username)
			}
		}
		err := tx.Model(&userRow{}).Where("tenant_id = ? AND id = ?", tenantID, row.ID).Updates(map[string]any{
			"username":          row.Username,
			"username_key":      row.UsernameKey,
			"email":             row.Email,
			"display_name":      row.DisplayName,
			"password_hash":     row.PasswordHash,
			"status":            row.Status,
			"encrypted_sec_key": row.EncryptedSecKey,
			"roles":             row.Roles,
			"created_at":        row.CreatedAt,
			"updated_at":        row.UpdatedAt,
		}).Error
		if err != nil {
			if isDuplicate(err) {
				s.log.Debug("the unique index rejected a username change | 唯一索引拒绝了用户名变更",
					logx.Str("tenant_id", tenantID), logx.Str("user_id", row.ID))
				return errx.WrapDetail(err, auth.ErrConflict, "username %[1]q already exists | 用户名 %[1]q 已存在", row.Username)
			}
			return err
		}
		return nil
	})
}
