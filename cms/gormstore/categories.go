package gormstore

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/shibingli/yd/cms"
	"github.com/shibingli/yd/core/errx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ListCategories returns every category of the tenant. | ListCategories 返回租户的全部分类。
func (s *Store) ListCategories(ctx context.Context, tenantID string) ([]cms.Category, error) {
	var rows []cms.Category
	if err := s.dbc(ctx).Where("tenant_id = ?", tenantID).
		Order("sort_order ASC, name ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, queryError(err)
	}
	return rows, nil
}

// GetCategory returns one category. | GetCategory 返回一个分类。
func (s *Store) GetCategory(ctx context.Context, tenantID, id string) (cms.Category, error) {
	var row cms.Category
	if err := s.dbc(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&row).Error; err != nil {
		return cms.Category{}, queryError(err)
	}
	return row, nil
}

// GetCategoryBySlug returns one category by slug. | GetCategoryBySlug 按别名返回一个分类。
func (s *Store) GetCategoryBySlug(ctx context.Context, tenantID, slug string) (cms.Category, error) {
	var row cms.Category
	if err := s.dbc(ctx).Where("tenant_id = ? AND slug = ?", tenantID, slug).First(&row).Error; err != nil {
		return cms.Category{}, queryError(err)
	}
	return row, nil
}

// CreateCategory inserts a category after locking its parent and recomputing path and depth. |
// CreateCategory 在锁定父分类并重新计算路径与深度后插入一个分类。
func (s *Store) CreateCategory(ctx context.Context, tenantID string, category cms.Category) error {
	return queryError(s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockCategoryMutationTx(tx, tenantID); err != nil {
			return err
		}
		parentPath := "/"
		depth := 0
		if strings.TrimSpace(category.ParentID) != "" {
			var parent cms.Category
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Select("id", "path", "depth").
				Where("tenant_id = ? AND id = ?", tenantID, category.ParentID).Take(&parent).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return cms.ErrCategoryParent
			}
			if err != nil {
				return err
			}
			parentPath = parent.Path
			depth = parent.Depth + 1
		} else {
			category.ParentID = ""
		}
		if depth > cms.MaxCategoryDepth {
			return errx.Detail(cms.ErrCategoryParent, "depth %[1]d exceeds the limit %[2]d | 深度 %[1]d 超过上限 %[2]d",
				depth, cms.MaxCategoryDepth)
		}
		category.TenantID = tenantID
		category.Path = parentPath + category.ID + "/"
		category.Depth = depth
		if len(category.Path) > cms.MaxCategoryPathLength {
			return errx.Detail(cms.ErrCategoryParent, "path exceeds %[1]d bytes | 路径超过 %[1]d 字节",
				cms.MaxCategoryPathLength)
		}
		// Select("*") includes zero values, so a hidden category stays hidden despite the column default. |
		// Select("*") 包含零值，因此隐藏分类不会因列默认值而变成可见。
		if err := tx.Select("*").Create(&category).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return cms.ErrCategorySlugTaken
			}
			return err
		}
		return nil
	}))
}

// UpdateCategory replaces the editable fields of one category. Parent, path and depth are owned by
// CreateCategory/MoveCategory; writing them from a stale service snapshot would revert a concurrent
// move. | UpdateCategory 替换分类的可编辑字段。父级、路径与深度由 CreateCategory/MoveCategory 维护；
// 用服务层的过期快照写它们会回退并发移动。
func (s *Store) UpdateCategory(ctx context.Context, tenantID string, category cms.Category) error {
	result := s.dbc(ctx).Model(&cms.Category{}).
		Where("tenant_id = ? AND id = ?", tenantID, category.ID).
		Select("name", "slug", "description", "sort_order", "visible", "updated_at").
		Updates(&category)
	if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
		return cms.ErrCategorySlugTaken
	}
	return s.updateResult(ctx, result, tenantID, &cms.Category{}, category.ID)
}

// MoveCategory rewrites the moved category and every descendant path in one transaction. It locks the
// moved row and the destination in a stable order, re-reads both, rejects a destination inside the
// moved subtree, and re-queries descendants under locks so a concurrent create cannot keep the old
// prefix. | MoveCategory 在一个事务中重写被移动分类及其全部后代路径。它按稳定顺序锁定被移动行与目标行，
// 重新读取两者，拒绝位于被移动子树内部的目标，并在锁下重新查询后代，使并发创建无法保留旧前缀。
func (s *Store) MoveCategory(ctx context.Context, tenantID string, category cms.Category, expectedPath string) error {
	err := s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockCategoryMutationTx(tx, tenantID); err != nil {
			return err
		}
		locked, err := lockCategoryPairTx(tx, tenantID, category.ID, category.ParentID)
		if err != nil {
			return err
		}
		moved, ok := locked[category.ID]
		if !ok {
			return cms.ErrCategoryNotFound
		}
		if moved.Path != expectedPath {
			return cms.ErrCategoryConflict
		}
		parentPath := "/"
		depth := 0
		if category.ParentID != "" {
			parent, ok := locked[category.ParentID]
			if !ok {
				return cms.ErrCategoryParent
			}
			if parent.ID == moved.ID || strings.HasPrefix(parent.Path, moved.Path) {
				return cms.ErrCategoryCycle
			}
			parentPath = parent.Path
			depth = parent.Depth + 1
		} else {
			category.ParentID = ""
		}
		if depth > cms.MaxCategoryDepth {
			return errx.Detail(cms.ErrCategoryParent, "depth %[1]d exceeds the limit %[2]d | 深度 %[1]d 超过上限 %[2]d",
				depth, cms.MaxCategoryDepth)
		}
		category.Path = parentPath + category.ID + "/"
		category.Depth = depth
		if len(category.Path) > cms.MaxCategoryPathLength {
			return errx.Detail(cms.ErrCategoryParent, "path exceeds %[1]d bytes | 路径超过 %[1]d 字节",
				cms.MaxCategoryPathLength)
		}
		// 旧路径是本次移动的乐观锁：另一个移动已经改过根节点时返回冲突，而不是把两棵子树交错写坏。 |
		// The old path is the optimistic lock of this move: if another move already changed the root, report a
		// conflict instead of interleaving two subtree rewrites.
		result := tx.Model(&cms.Category{}).
			Where("tenant_id = ? AND id = ? AND path = ?", tenantID, category.ID, expectedPath).
			Select("parent_id", "name", "slug", "description", "path", "sort_order", "depth", "visible", "updated_at").
			Updates(&category)
		if result.Error != nil {
			return queryError(result.Error)
		}
		if result.RowsAffected == 0 {
			return cms.ErrCategoryConflict
		}
		var descendants []cms.Category
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "path", "depth").
			Where("tenant_id = ? AND id <> ? AND path LIKE ? ESCAPE '!'", tenantID, category.ID, likePrefix(expectedPath)).
			Order("id ASC").Find(&descendants).Error; err != nil {
			return queryError(err)
		}
		delta := category.Depth - moved.Depth
		for _, descendant := range descendants {
			nextDepth := descendant.Depth + delta
			if nextDepth > cms.MaxCategoryDepth {
				return errx.Detail(cms.ErrCategoryParent, "depth %[1]d exceeds the limit %[2]d | 深度 %[1]d 超过上限 %[2]d",
					nextDepth, cms.MaxCategoryDepth)
			}
			nextPath := category.Path + strings.TrimPrefix(descendant.Path, expectedPath)
			if len(nextPath) > cms.MaxCategoryPathLength {
				return cms.ErrCategoryConflict
			}
			position := tx.Model(&cms.Category{}).
				Where("tenant_id = ? AND id = ? AND path = ?", tenantID, descendant.ID, descendant.Path).
				Updates(map[string]any{
					"path":       nextPath,
					"depth":      descendant.Depth + delta,
					"updated_at": category.UpdatedAt,
				})
			if position.Error != nil {
				return queryError(position.Error)
			}
			if position.RowsAffected == 0 {
				return cms.ErrCategoryConflict
			}
		}
		return nil
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return cms.ErrCategorySlugTaken
	}
	return queryError(err)
}

// lockCategoryMutationTx serialises every structural category change of one tenant. The settings row
// is a stable tenant-scoped mutex that already exists after EnsureTenant; when a library caller has
// not created it yet, the fallback locks every existing category row in ID order. |
// lockCategoryMutationTx 串行化一个租户的所有结构性分类变更。设置行是 EnsureTenant 之后即存在的稳定
// 租户级互斥行；库调用方尚未创建它时，回退为按 ID 顺序锁定全部既有分类行。
func lockCategoryMutationTx(tx *gorm.DB, tenantID string) error {
	var settings cms.Settings
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ?", tenantID).Take(&settings).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return queryError(err)
	}
	var ids []string
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Model(&cms.Category{}).Where("tenant_id = ?", tenantID).
		Order("id ASC").Pluck("id", &ids).Error; err != nil {
		return queryError(err)
	}
	return nil
}

// lockCategoryPairTx locks the moved row and the destination parent in ID order and returns both rows.
// | lockCategoryPairTx 按 ID 顺序锁定被移动行与目标父分类，并返回两行。
func lockCategoryPairTx(tx *gorm.DB, tenantID string, ids ...string) (map[string]cms.Category, error) {
	seen := make(map[string]struct{}, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	sort.Strings(unique)
	locked := make(map[string]cms.Category, len(unique))
	if len(unique) == 0 {
		return locked, nil
	}
	var rows []cms.Category
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "parent_id", "path", "depth").
		Where("tenant_id = ? AND id IN ?", tenantID, unique).
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, queryError(err)
	}
	for _, row := range rows {
		locked[row.ID] = row
	}
	return locked, nil
}

// DeleteCategoryIfEmpty re-checks emptiness under a row lock before deleting. |
// DeleteCategoryIfEmpty 在行锁下重新检查空状态后再删除。
func (s *Store) DeleteCategoryIfEmpty(ctx context.Context, tenantID, id string) error {
	return queryError(s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockCategoryMutationTx(tx, tenantID); err != nil {
			return err
		}
		var category cms.Category
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenantID, id).First(&category).Error; err != nil {
			return err
		}
		var children int64
		if err := tx.Model(&cms.Category{}).Where("tenant_id = ? AND parent_id = ?", tenantID, id).Count(&children).Error; err != nil {
			return err
		}
		if children > 0 {
			return cms.ErrCategoryNotEmpty
		}
		var articles int64
		if err := tx.Model(&cms.Article{}).
			Where("tenant_id = ? AND category_id = ? AND deleted_at IS NULL", tenantID, id).Count(&articles).Error; err != nil {
			return err
		}
		if articles > 0 {
			return cms.ErrCategoryNotEmpty
		}
		// Soft-deleted articles are invisible, but their category_id would still point at the row this
		// transaction is about to remove. Clear that frozen reference first. |
		// 软删除文章不可见，但其 category_id 仍会指向本事务即将删除的行；先清掉这条冻结的引用。
		if err := tx.Model(&cms.Article{}).
			Where("tenant_id = ? AND category_id = ? AND deleted_at IS NOT NULL", tenantID, id).
			Update("category_id", "").Error; err != nil {
			return err
		}
		return rowsError(tx.Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&cms.Category{}))
	}))
}

// CountCategoryChildren counts direct child categories. | CountCategoryChildren 统计直接子分类数。
func (s *Store) CountCategoryChildren(ctx context.Context, tenantID, id string) (int64, error) {
	var total int64
	if err := s.dbc(ctx).Model(&cms.Category{}).
		Where("tenant_id = ? AND parent_id = ?", tenantID, id).Count(&total).Error; err != nil {
		return 0, queryError(err)
	}
	return total, nil
}

// CountPublicArticlesByCategories counts only published, public, due articles. Public category pages
// must not disclose the volume of drafts, pending or private articles. |
// CountPublicArticlesByCategories 只统计已发布、公开且已到发布时间的文章；公开分类页不得泄露草稿、
// 待审或私有文章的数量。
func (s *Store) CountPublicArticlesByCategories(ctx context.Context, tenantID string, categoryIDs []string, now time.Time) (map[string]int64, error) {
	out := make(map[string]int64, len(categoryIDs))
	if len(categoryIDs) == 0 {
		return out, nil
	}
	type row struct {
		CategoryID string
		Total      int64
	}
	for _, batch := range chunks(categoryIDs) {
		var rows []row
		err := s.dbc(ctx).Model(&cms.Article{}).
			Select("category_id, COUNT(*) AS total").
			Where("tenant_id = ? AND deleted_at IS NULL AND category_id IN ?", tenantID, batch).
			Where("status = ? AND visibility = ?", cms.ArticlePublished, cms.VisibilityPublic).
			Where("(publish_at IS NULL OR publish_at <= ?)", now.UTC()).
			Group("category_id").Scan(&rows).Error
		if err != nil {
			return nil, queryError(err)
		}
		for _, item := range rows {
			out[item.CategoryID] = item.Total
		}
	}
	return out, nil
}

// CountArticlesByCategories counts non-deleted articles directly assigned to each category. |
// CountArticlesByCategories 统计直接归属每个分类的未删除文章数。
func (s *Store) CountArticlesByCategories(ctx context.Context, tenantID string, categoryIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(categoryIDs))
	if len(categoryIDs) == 0 {
		return out, nil
	}
	type row struct {
		CategoryID string
		Total      int64
	}
	for _, batch := range chunks(categoryIDs) {
		var rows []row
		err := s.dbc(ctx).Model(&cms.Article{}).
			Select("category_id, COUNT(*) AS total").
			Where("tenant_id = ? AND deleted_at IS NULL AND category_id IN ?", tenantID, batch).
			Group("category_id").Scan(&rows).Error
		if err != nil {
			return nil, queryError(err)
		}
		for _, item := range rows {
			out[item.CategoryID] = item.Total
		}
	}
	return out, nil
}
