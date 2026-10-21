package gormstore

import (
	"context"
	"errors"

	"github.com/shibingli/yd/cms"
	"gorm.io/gorm"
)

// CreateRevision inserts an immutable snapshot. A duplicate version means another save won the race,
// which is an optimistic conflict rather than an unavailable store. |
// CreateRevision 插入不可变快照。版本重复说明另一个保存赢得了竞争，这是乐观锁冲突，而不是存储不可用。
func (s *Store) CreateRevision(ctx context.Context, tenantID string, revision cms.Revision) error {
	revision.TenantID = tenantID
	if err := s.dbc(ctx).Create(&revision).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return cms.ErrArticleConflict
		}
		return queryError(err)
	}
	return nil
}

// ListRevisions returns revisions newest first. | ListRevisions 按从新到旧返回修订。
func (s *Store) ListRevisions(ctx context.Context, tenantID, articleID string) ([]cms.Revision, error) {
	var rows []cms.Revision
	if err := s.dbc(ctx).Where("tenant_id = ? AND article_id = ?", tenantID, articleID).
		Order("version DESC, created_at DESC").Find(&rows).Error; err != nil {
		return nil, queryError(err)
	}
	return rows, nil
}

// GetRevision returns one revision. | GetRevision 返回一份修订。
func (s *Store) GetRevision(ctx context.Context, tenantID, articleID, revisionID string) (cms.Revision, error) {
	var row cms.Revision
	if err := s.dbc(ctx).Where("tenant_id = ? AND article_id = ? AND id = ?", tenantID, articleID, revisionID).
		First(&row).Error; err != nil {
		return cms.Revision{}, queryError(err)
	}
	return row, nil
}

// PruneRevisions keeps the newest keep revisions. Deletion is bounded by the oldest kept version,
// so a revision inserted concurrently after the selection (which necessarily has a newer version)
// is never removed. | PruneRevisions 保留最新 keep 份修订。删除以保留集合中最旧的版本为界，因此选择之后
// 并发插入的修订（版本必然更新）绝不会被误删。
func (s *Store) PruneRevisions(ctx context.Context, tenantID, articleID string, keep int) error {
	if keep <= 0 {
		return nil
	}
	var versions []int
	if err := s.dbc(ctx).Model(&cms.Revision{}).
		Where("tenant_id = ? AND article_id = ?", tenantID, articleID).
		Order("version DESC, created_at DESC").Limit(keep).Pluck("version", &versions).Error; err != nil {
		return queryError(err)
	}
	if len(versions) == 0 {
		return nil
	}
	cutoff := versions[0]
	for _, version := range versions[1:] {
		if version < cutoff {
			cutoff = version
		}
	}
	return queryError(s.dbc(ctx).
		Where("tenant_id = ? AND article_id = ? AND version < ?", tenantID, articleID, cutoff).
		Delete(&cms.Revision{}).Error)
}
