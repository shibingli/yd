package gormstore

import (
	"context"
	"errors"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/messaging"
	"gorm.io/gorm"
)

// EnsureTypes inserts missing built-in types, leaving existing rows untouched. | EnsureTypes 插入缺失的内置类型，不触碰已有行。
//
// The lookup is explicit rather than FirstOrCreate: the incoming row already carries a fresh
// primary key, and FirstOrCreate would add that key to its lookup and then collide on the unique
// tenant/key index. | 这里显式先查后建而不是用 FirstOrCreate：传入行已带全新主键，
// FirstOrCreate 会把它一并作为查询条件，随后撞上 tenant/key 唯一索引。
func (s *Store) EnsureTypes(ctx context.Context, tenantID string, types []messaging.MessageType) error {
	for _, t := range types {
		row := t
		row.TenantID = tenantID
		var existing messaging.MessageType
		err := s.dbc(ctx).Where("tenant_id = ? AND type_key = ?", tenantID, row.Key).First(&existing).Error
		if err == nil {
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return queryError(err)
		}
		if createErr := s.dbc(ctx).Create(&row).Error; createErr != nil {
			// A concurrent writer may have inserted the same built-in first; that is idempotence,
			// not a failure. | 并发写入者可能已先插入同一个内置类型；那是幂等，不是故障。
			var again messaging.MessageType
			if lookupErr := s.dbc(ctx).Where("tenant_id = ? AND type_key = ?", tenantID, row.Key).First(&again).Error; lookupErr == nil {
				continue
			}
			return createError(createErr)
		}
	}
	return nil
}

// ListTypes returns the tenant's types in creation order. | ListTypes 按创建顺序返回租户的消息类型。
func (s *Store) ListTypes(ctx context.Context, tenantID string, includeArchived bool) ([]messaging.MessageType, error) {
	query := s.dbc(ctx).Where("tenant_id = ?", tenantID)
	if !includeArchived {
		query = query.Where("archived_at IS NULL")
	}
	var out []messaging.MessageType
	if err := query.Order("created_at ASC, type_key ASC").Find(&out).Error; err != nil {
		return nil, queryError(err)
	}
	return out, nil
}

// GetType resolves one type by its stable key. | GetType 按稳定键解析一个类型。
func (s *Store) GetType(ctx context.Context, tenantID, key string) (messaging.MessageType, error) {
	var t messaging.MessageType
	if err := s.dbc(ctx).Where("tenant_id = ? AND type_key = ?", tenantID, key).First(&t).Error; err != nil {
		return messaging.MessageType{}, queryError(err)
	}
	return t, nil
}

// CreateType stores one custom type. | CreateType 保存一个自定义类型。
func (s *Store) CreateType(ctx context.Context, t messaging.MessageType) error {
	if err := s.dbc(ctx).Create(&t).Error; err != nil {
		return createError(err)
	}
	return nil
}

// UpdateType replaces the editable fields of a type. | UpdateType 覆盖类型的可编辑字段。
func (s *Store) UpdateType(ctx context.Context, t messaging.MessageType) error {
	result := s.dbc(ctx).Model(&messaging.MessageType{}).
		Where("tenant_id = ? AND type_key = ?", t.TenantID, t.Key).
		Updates(map[string]any{
			"name":        t.Name,
			"name_en":     t.NameEN,
			"tone":        t.Tone,
			"archived_at": t.ArchivedAt,
			"updated_at":  t.UpdatedAt,
		})
	return queryError(result.Error)
}

// DeleteType removes an unused type. | DeleteType 删除未使用的类型。
func (s *Store) DeleteType(ctx context.Context, tenantID, key string) error {
	result := s.dbc(ctx).Where("tenant_id = ? AND type_key = ?", tenantID, key).Delete(&messaging.MessageType{})
	return rowsError(result)
}

// CountMessagesOfType returns how many messages use a type. | CountMessagesOfType 返回使用某类型的消息数。
func (s *Store) CountMessagesOfType(ctx context.Context, tenantID, key string) (int64, error) {
	var count int64
	if err := s.dbc(ctx).Model(&messaging.Message{}).
		Where("tenant_id = ? AND type_key = ?", tenantID, key).
		Count(&count).Error; err != nil {
		return 0, queryError(err)
	}
	return count, nil
}

// ListTags returns groups or labels in creation order. | ListTags 按创建顺序返回分组或标签。
func (s *Store) ListTags(ctx context.Context, tenantID string, kind messaging.TagKind, includeArchived bool) ([]messaging.Tag, error) {
	query := s.dbc(ctx).Where("tenant_id = ? AND kind = ?", tenantID, kind)
	if !includeArchived {
		query = query.Where("archived_at IS NULL")
	}
	var out []messaging.Tag
	if err := query.Order("created_at ASC, tag_key ASC").Find(&out).Error; err != nil {
		return nil, queryError(err)
	}
	return out, nil
}

// GetTags resolves a batch of tag ids. | GetTags 批量解析标签 ID。
func (s *Store) GetTags(ctx context.Context, tenantID string, ids []string) ([]messaging.Tag, error) {
	if len(ids) == 0 {
		return []messaging.Tag{}, nil
	}
	out := make([]messaging.Tag, 0, len(ids))
	for _, batch := range chunks(ids) {
		var rows []messaging.Tag
		if err := s.dbc(ctx).Where("tenant_id = ? AND id IN ?", tenantID, batch).Find(&rows).Error; err != nil {
			return nil, queryError(err)
		}
		out = append(out, rows...)
	}
	return out, nil
}

// CreateTag stores one group or label. | CreateTag 保存一个分组或标签。
func (s *Store) CreateTag(ctx context.Context, t messaging.Tag) error {
	if err := s.dbc(ctx).Create(&t).Error; err != nil {
		return createError(err)
	}
	return nil
}

// UpdateTag replaces the editable fields of a tag. | UpdateTag 覆盖标签的可编辑字段。
func (s *Store) UpdateTag(ctx context.Context, t messaging.Tag) error {
	result := s.dbc(ctx).Model(&messaging.Tag{}).
		Where("tenant_id = ? AND id = ?", t.TenantID, t.ID).
		Updates(map[string]any{
			"name":        t.Name,
			"name_en":     t.NameEN,
			"tone":        t.Tone,
			"archived_at": t.ArchivedAt,
			"updated_at":  t.UpdatedAt,
		})
	return queryError(result.Error)
}

// DeleteTag removes an unused group or label. | DeleteTag 删除未使用的分组或标签。
func (s *Store) DeleteTag(ctx context.Context, tenantID, id string) error {
	result := s.dbc(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&messaging.Tag{})
	return rowsError(result)
}

// CountMessagesOfTag returns how many messages carry a tag. | CountMessagesOfTag 返回携带某标签的消息数。
func (s *Store) CountMessagesOfTag(ctx context.Context, tenantID, id string) (int64, error) {
	var count int64
	if err := s.dbc(ctx).Model(&messaging.MessageTag{}).
		Where("tenant_id = ? AND tag_id = ?", tenantID, id).
		Count(&count).Error; err != nil {
		return 0, queryError(err)
	}
	return count, nil
}

// typeByKey returns a type or ErrNotFound. | typeByKey 返回类型或未找到。
func (s *Store) typeByKey(ctx context.Context, tenantID, key string) (messaging.MessageType, error) {
	var t messaging.MessageType
	if err := s.dbc(ctx).Where("tenant_id = ? AND type_key = ?", tenantID, key).First(&t).Error; err != nil {
		return messaging.MessageType{}, errx.Wrap(err, messaging.ErrNotFound)
	}
	return t, nil
}
