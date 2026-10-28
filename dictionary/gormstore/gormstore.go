// Package gormstore persists dictionary types and items with GORM. | Package gormstore 使用 GORM 持久化
// 数据字典类型与字典项。
package gormstore

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/shibingli/yd/dictionary"
	"github.com/shibingli/ydx/errs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store is the GORM dictionary store. | Store 是 GORM 数据字典存储。
type Store struct {
	// db is the shared connection pool. | db 是共享连接池。
	db *gorm.DB
}

// New returns a store over db. | New 返回基于 db 的存储。
func New(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, dictionary.ErrUnavailable
	}
	return &Store{db: db}, nil
}

// DB returns the underlying handle for callers that need a shared transaction. | DB 返回底层句柄，供需要
// 共享事务的调用方使用。
func (s *Store) DB() *gorm.DB { return s.db }

// Migrate creates the dictionary tables and indexes. It is idempotent. | Migrate 创建数据字典表与索引，是幂等的。
func Migrate(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return dictionary.ErrUnavailable
	}
	if err := db.WithContext(ctx).AutoMigrate(&dictionary.Type{}, &dictionary.Item{}); err != nil {
		return errs.Wrap(err, dictionary.ErrUnavailable)
	}
	return nil
}

// EnsureBuiltins inserts the predefined dictionary types a tenant does not have yet. An existing
// type is left untouched as a whole, so renamed labels, disabled types and deleted items survive
// every restart. | EnsureBuiltins 插入租户尚不存在的预置字典类型。已存在的类型整体保持不变，
// 因此改过的标签、停用的类型与删除的字典项都能在每次重启后保留。
func (s *Store) EnsureBuiltins(ctx context.Context, tenantID string) (int, error) {
	types, items := dictionary.BuiltinRows(tenantID, time.Now().UTC())
	codes := make([]string, 0, len(types))
	for _, dictionaryType := range types {
		codes = append(codes, dictionaryType.Code)
	}
	var existing []string
	if err := s.db.WithContext(ctx).Model(&dictionary.Type{}).
		Where("tenant_id = ? AND code IN ?", tenantID, codes).
		Pluck("code", &existing).Error; err != nil {
		return 0, queryError(err)
	}
	have := make(map[string]struct{}, len(existing))
	for _, code := range existing {
		have[code] = struct{}{}
	}
	missingTypes := make([]dictionary.Type, 0, len(types))
	missingTypeIDs := make(map[string]struct{}, len(types))
	missingCodes := make([]string, 0, len(types))
	generatedToCode := make(map[string]string, len(types))
	for _, dictionaryType := range types {
		if _, ok := have[dictionaryType.Code]; ok {
			continue
		}
		missingTypes = append(missingTypes, dictionaryType)
		missingTypeIDs[dictionaryType.ID] = struct{}{}
		missingCodes = append(missingCodes, dictionaryType.Code)
		generatedToCode[dictionaryType.ID] = dictionaryType.Code
	}
	if len(missingTypes) == 0 {
		return 0, nil
	}
	missingItems := make([]dictionary.Item, 0, len(items))
	for _, item := range items {
		if _, ok := missingTypeIDs[item.TypeID]; ok {
			missingItems = append(missingItems, item)
		}
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&missingTypes).Error; err != nil {
			return createError(err, dictionary.ErrUnavailable)
		}
		// A concurrent writer may have created the same code with its own identifier between the
		// pre-check and the insert. Re-read the winners and skip items of a type we did not create:
		// an existing type is left untouched as a whole. | 并发写入者可能在预查与插入之间用另一个标识
		// 创建了同一编码。这里重读胜出行，跳过并非由本次插入创建的类型的字典项：已存在的类型整体保持不变。
		var actual []dictionary.Type
		if err := tx.Where("tenant_id = ? AND code IN ?", tenantID, missingCodes).Find(&actual).Error; err != nil {
			return queryError(err)
		}
		actualByCode := make(map[string]string, len(actual))
		for _, dictionaryType := range actual {
			actualByCode[dictionaryType.Code] = dictionaryType.ID
		}
		insertItems := make([]dictionary.Item, 0, len(missingItems))
		for _, item := range missingItems {
			code, ok := generatedToCode[item.TypeID]
			if !ok || actualByCode[code] != item.TypeID {
				continue
			}
			insertItems = append(insertItems, item)
		}
		if len(insertItems) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&insertItems).Error; err != nil {
				return createError(err, dictionary.ErrUnavailable)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(missingTypes), nil
}

// CreateType inserts one dictionary type. | CreateType 插入一个数据字典类型。
func (s *Store) CreateType(ctx context.Context, t dictionary.Type) error {
	if err := s.db.WithContext(ctx).Create(&t).Error; err != nil {
		return createError(err, dictionary.ErrCodeTaken)
	}
	return nil
}

// UpdateType replaces the editable fields of a type. | UpdateType 覆盖类型的可编辑字段。
func (s *Store) UpdateType(ctx context.Context, t dictionary.Type) error {
	result := s.db.WithContext(ctx).Model(&dictionary.Type{}).
		Where("tenant_id = ? AND id = ?", t.TenantID, t.ID).
		Updates(map[string]any{
			"name":        t.Name,
			"name_en":     t.NameEN,
			"description": t.Description,
			"status":      t.Status,
			"sort_order":  t.Sort,
			"updated_at":  t.UpdatedAt,
		})
	if result.Error != nil {
		return queryError(result.Error)
	}
	if result.RowsAffected == 0 {
		// An unchanged row may report zero affected rows on MySQL; verify existence before
		// declaring it missing. | MySQL 对内容未变的行可能报告零影响行；先确认存在再判定为不存在。
		if _, err := s.GetType(ctx, t.TenantID, t.ID); err != nil {
			return err
		}
	}
	return nil
}

// GetType reads one type inside the tenant. | GetType 读取租户内的一个类型。
func (s *Store) GetType(ctx context.Context, tenantID, id string) (dictionary.Type, error) {
	var t dictionary.Type
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, strings.TrimSpace(id)).
		First(&t).Error; err != nil {
		return dictionary.Type{}, queryError(err)
	}
	return t, nil
}

// GetTypeByCode reads one type by its immutable code inside the tenant. | GetTypeByCode 按不可变编码读取租户内的一个类型。
func (s *Store) GetTypeByCode(ctx context.Context, tenantID, code string) (dictionary.Type, error) {
	var t dictionary.Type
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND code = ?", tenantID, strings.ToLower(strings.TrimSpace(code))).
		First(&t).Error; err != nil {
		return dictionary.Type{}, queryError(err)
	}
	return t, nil
}

// ListTypes returns one page of types and its total. | ListTypes 返回一页类型与总数。
func (s *Store) ListTypes(ctx context.Context, tenantID string, filter dictionary.Filter) ([]dictionary.Type, int, error) {
	page, pageSize := filter.Page, filter.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	query := s.db.WithContext(ctx).Model(&dictionary.Type{}).Where("tenant_id = ?", tenantID)
	query = applyTypeFilter(query, filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, queryError(err)
	}
	var items []dictionary.Type
	if err := query.Order("sort_order ASC, created_at ASC, id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&items).Error; err != nil {
		return nil, 0, queryError(err)
	}
	if err := s.fillItemCounts(ctx, tenantID, items); err != nil {
		return nil, 0, err
	}
	return items, int(total), nil
}

// fillItemCounts adds the item count to a page of types. | fillItemCounts 为一页类型补充字典项数量。
func (s *Store) fillItemCounts(ctx context.Context, tenantID string, items []dictionary.Type) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	type countRow struct {
		TypeID string `gorm:"column:type_id"`
		Total  int    `gorm:"column:total"`
	}
	var rows []countRow
	if err := s.db.WithContext(ctx).Model(&dictionary.Item{}).
		Select("type_id, COUNT(*) AS total").
		Where("tenant_id = ? AND type_id IN ?", tenantID, ids).
		Group("type_id").
		Scan(&rows).Error; err != nil {
		return queryError(err)
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.TypeID] = row.Total
	}
	for i := range items {
		items[i].ItemCount = counts[items[i].ID]
	}
	return nil
}

// DeleteType deletes one type and every item it owns in one transaction. | DeleteType 在一个事务内删除一个
// 类型及其全部字典项。
func (s *Store) DeleteType(ctx context.Context, tenantID, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current dictionary.Type
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenantID, id).
			Take(&current).Error; err != nil {
			return queryError(err)
		}
		if current.Builtin {
			return dictionary.ErrBuiltinProtected
		}
		if err := tx.Where("tenant_id = ? AND type_id = ?", tenantID, current.ID).
			Delete(&dictionary.Item{}).Error; err != nil {
			return queryError(err)
		}
		if err := tx.Where("tenant_id = ? AND id = ?", tenantID, current.ID).
			Delete(&dictionary.Type{}).Error; err != nil {
			return queryError(err)
		}
		return nil
	})
}

// CreateItem inserts one item and replaces the previous default in the same transaction. The item
// ceiling is re-checked after the type row lock, so concurrent writers cannot exceed it. | CreateItem
// 插入一个字典项，并在同一事务内替换原有默认项。数量上限在类型行锁之后复查，因此并发写入不能突破它。
func (s *Store) CreateItem(ctx context.Context, item dictionary.Item, maxItems int) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockType(tx, item.TenantID, item.TypeID); err != nil {
			return err
		}
		if maxItems > 0 {
			var total int64
			if err := tx.Model(&dictionary.Item{}).
				Where("tenant_id = ? AND type_id = ?", item.TenantID, item.TypeID).
				Count(&total).Error; err != nil {
				return queryError(err)
			}
			if total >= int64(maxItems) {
				return dictionary.ErrItemLimit
			}
		}
		if item.IsDefault {
			if err := clearDefault(tx, item.TenantID, item.TypeID); err != nil {
				return err
			}
		}
		if err := tx.Create(&item).Error; err != nil {
			return createError(err, dictionary.ErrValueTaken)
		}
		return nil
	})
}

// UpdateItem replaces the editable fields of one item. | UpdateItem 覆盖一个字典项的可编辑字段。
func (s *Store) UpdateItem(ctx context.Context, item dictionary.Item) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockType(tx, item.TenantID, item.TypeID); err != nil {
			return err
		}
		var current dictionary.Item
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND type_id = ? AND id = ?", item.TenantID, item.TypeID, item.ID).
			Take(&current).Error; err != nil {
			return queryError(err)
		}
		if item.IsDefault {
			if err := clearDefault(tx, item.TenantID, item.TypeID); err != nil {
				return err
			}
		}
		result := tx.Model(&dictionary.Item{}).
			Where("tenant_id = ? AND type_id = ? AND id = ?", item.TenantID, item.TypeID, item.ID).
			Updates(map[string]any{
				"label":      item.Label,
				"label_en":   item.LabelEN,
				"extra":      item.Extra,
				"remark":     item.Remark,
				"status":     item.Status,
				"is_default": item.IsDefault,
				"sort_order": item.Sort,
				"updated_at": item.UpdatedAt,
			})
		if result.Error != nil {
			return queryError(result.Error)
		}
		return nil
	})
}

// GetItem reads one item inside the tenant and its type. | GetItem 读取租户内某个类型的一个字典项。
func (s *Store) GetItem(ctx context.Context, tenantID, typeID, id string) (dictionary.Item, error) {
	var item dictionary.Item
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND type_id = ? AND id = ?", tenantID, typeID, strings.TrimSpace(id)).
		First(&item).Error; err != nil {
		return dictionary.Item{}, queryError(err)
	}
	return item, nil
}

// GetItemByValue reads one item by its immutable value. | GetItemByValue 按不可变值读取一个字典项。
func (s *Store) GetItemByValue(ctx context.Context, tenantID, typeID, value string) (dictionary.Item, error) {
	var item dictionary.Item
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND type_id = ? AND item_value = ?", tenantID, typeID, strings.TrimSpace(value)).
		First(&item).Error; err != nil {
		return dictionary.Item{}, queryError(err)
	}
	return item, nil
}

// ListItems returns one page of items and its total. | ListItems 返回一页字典项与总数。
func (s *Store) ListItems(ctx context.Context, tenantID, typeID string, filter dictionary.ItemFilter) ([]dictionary.Item, int, error) {
	page, pageSize := filter.Page, filter.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	query := s.db.WithContext(ctx).Model(&dictionary.Item{}).
		Where("tenant_id = ? AND type_id = ?", tenantID, typeID)
	query = applyItemFilter(query, filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, queryError(err)
	}
	var items []dictionary.Item
	if err := query.Order("sort_order ASC, created_at ASC, id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&items).Error; err != nil {
		return nil, 0, queryError(err)
	}
	return items, int(total), nil
}

// DeleteItem deletes one item. | DeleteItem 删除一个字典项。
func (s *Store) DeleteItem(ctx context.Context, tenantID, typeID, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockType(tx, tenantID, typeID); err != nil {
			return err
		}
		result := tx.Where("tenant_id = ? AND type_id = ? AND id = ?", tenantID, typeID, id).
			Delete(&dictionary.Item{})
		if result.Error != nil {
			return queryError(result.Error)
		}
		if result.RowsAffected == 0 {
			return dictionary.ErrNotFound
		}
		return nil
	})
}

// SetItemStatus changes one item's status and clears the default flag when it is disabled. |
// SetItemStatus 修改字典项状态，并在停用时清除默认标记。
func (s *Store) SetItemStatus(ctx context.Context, tenantID, typeID, itemID string, status dictionary.Status, at time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockType(tx, tenantID, typeID); err != nil {
			return err
		}
		var current dictionary.Item
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND type_id = ? AND id = ?", tenantID, typeID, itemID).
			Take(&current).Error; err != nil {
			return queryError(err)
		}
		updates := map[string]any{
			"status":     status,
			"updated_at": at,
		}
		if status == dictionary.StatusDisabled {
			updates["is_default"] = false
		}
		result := tx.Model(&dictionary.Item{}).
			Where("tenant_id = ? AND type_id = ? AND id = ?", tenantID, typeID, itemID).
			Updates(updates)
		return queryError(result.Error)
	})
}

// Resolve loads enabled types and their enabled items for the given codes in one query. |
// Resolve 用一次查询加载给定编码的启用类型及其启用字典项。
func (s *Store) Resolve(ctx context.Context, tenantID string, codes []string) (map[string]dictionary.Snapshot, error) {
	out := make(map[string]dictionary.Snapshot, len(codes))
	if len(codes) == 0 {
		return out, nil
	}
	type row struct {
		TypeNameEN      *string `gorm:"column:type_name_en"`
		TypeDescription *string `gorm:"column:type_description"`
		ItemID          *string `gorm:"column:item_id"`
		ItemValue       *string `gorm:"column:item_value"`
		ItemLabel       *string `gorm:"column:item_label"`
		ItemLabelEN     *string `gorm:"column:item_label_en"`
		ItemExtra       *string `gorm:"column:item_extra"`
		ItemDefault     *bool   `gorm:"column:item_default"`
		ItemSort        *int    `gorm:"column:item_sort"`
		TypeID          string  `gorm:"column:type_id"`
		TypeCode        string  `gorm:"column:type_code"`
		TypeName        string  `gorm:"column:type_name"`
	}
	var rows []row
	if err := s.db.WithContext(ctx).
		Table("yd_dict_types AS t").
		Select(`t.id AS type_id, t.code AS type_code, t.name AS type_name, t.name_en AS type_name_en,
			t.description AS type_description, i.id AS item_id, i.item_value AS item_value,
			i.label AS item_label, i.label_en AS item_label_en, i.extra AS item_extra,
			i.is_default AS item_default, i.sort_order AS item_sort`).
		Joins("LEFT JOIN yd_dict_items AS i ON i.tenant_id = t.tenant_id AND i.type_id = t.id AND i.status = ?", dictionary.StatusEnabled).
		Where("t.tenant_id = ? AND t.code IN ? AND t.status = ?", tenantID, codes, dictionary.StatusEnabled).
		Order("t.sort_order ASC, t.created_at ASC, i.sort_order ASC, i.created_at ASC, i.id ASC").
		Scan(&rows).Error; err != nil {
		return nil, queryError(err)
	}
	for _, current := range rows {
		snapshot, ok := out[current.TypeCode]
		if !ok {
			snapshot = dictionary.Snapshot{Code: current.TypeCode, Name: current.TypeName, Items: make([]dictionary.SnapshotItem, 0, 4)}
			if current.TypeNameEN != nil {
				snapshot.NameEN = *current.TypeNameEN
			}
			if current.TypeDescription != nil {
				snapshot.Description = *current.TypeDescription
			}
		}
		if current.ItemID != nil {
			item := dictionary.SnapshotItem{Value: stringValue(current.ItemValue), Label: stringValue(current.ItemLabel)}
			if current.ItemLabelEN != nil {
				item.LabelEN = *current.ItemLabelEN
			}
			if current.ItemExtra != nil {
				item.Extra = *current.ItemExtra
			}
			if current.ItemDefault != nil {
				item.IsDefault = *current.ItemDefault
			}
			if current.ItemSort != nil {
				item.Sort = *current.ItemSort
			}
			snapshot.Items = append(snapshot.Items, item)
		}
		out[current.TypeCode] = snapshot
	}
	return out, nil
}

// lockType takes the type row lock that serializes item writes and default changes of one type. |
// lockType 取得类型行锁，使同一类型的字典项写入与默认项切换串行化。
func lockType(tx *gorm.DB, tenantID, typeID string) error {
	var current dictionary.Type
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND id = ?", tenantID, typeID).
		Take(&current).Error; err != nil {
		return queryError(err)
	}
	return nil
}

// clearDefault clears the default flag of every item of one type. | clearDefault 清除一个类型下全部字典项的默认标记。
func clearDefault(tx *gorm.DB, tenantID, typeID string) error {
	if err := tx.Model(&dictionary.Item{}).
		Where("tenant_id = ? AND type_id = ? AND is_default = ?", tenantID, typeID, true).
		Update("is_default", false).Error; err != nil {
		return queryError(err)
	}
	return nil
}

// applyTypeFilter adds the shared type predicates. | applyTypeFilter 添加共享的类型筛选条件。
func applyTypeFilter(query *gorm.DB, filter dictionary.Filter) *gorm.DB {
	if q := strings.TrimSpace(filter.Query); q != "" {
		pattern := "%" + strings.ToLower(escapeLike(q)) + "%"
		query = query.Where("LOWER(code) LIKE ? ESCAPE '!' OR LOWER(name) LIKE ? ESCAPE '!' OR LOWER(COALESCE(name_en, '')) LIKE ? ESCAPE '!'",
			pattern, pattern, pattern)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	return query
}

// applyItemFilter adds the shared item predicates. | applyItemFilter 添加共享的字典项筛选条件。
func applyItemFilter(query *gorm.DB, filter dictionary.ItemFilter) *gorm.DB {
	if q := strings.TrimSpace(filter.Query); q != "" {
		pattern := "%" + strings.ToLower(escapeLike(q)) + "%"
		query = query.Where("LOWER(item_value) LIKE ? ESCAPE '!' OR LOWER(label) LIKE ? ESCAPE '!' OR LOWER(COALESCE(label_en, '')) LIKE ? ESCAPE '!'",
			pattern, pattern, pattern)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	return query
}

// escapeLike escapes LIKE metacharacters with "!", the escape character every dialect of this
// repository accepts. | escapeLike 用 "!" 转义 LIKE 元字符，这是本仓所有方言都接受的转义字符。
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	return strings.ReplaceAll(value, "_", "!_")
}

// stringValue dereferences an optional string. | stringValue 解引用可空字符串。
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// queryError maps GORM errors onto dictionary sentinels. | queryError 把 GORM 错误映射为数据字典哨兵。
func queryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return dictionary.ErrNotFound
	}
	return errs.Wrap(err, dictionary.ErrUnavailable)
}

// createError maps a write failure, translating a unique-index conflict into duplicate. |
// createError 映射写入失败，并把唯一索引冲突翻译为重复。
func createError(err error, duplicate error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return duplicate
	}
	return errs.Wrap(err, dictionary.ErrUnavailable)
}
