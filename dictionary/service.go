package dictionary

import (
	"context"
	"errors"
	"strings"

	"github.com/shibingli/ydx/cache"
	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
)

// Service implements the dictionary lifecycle on top of a Store and an optional second-level cache. |
// Service 在 Store 与可选二级缓存之上实现数据字典生命周期。
type Service struct {
	// store persists types and items. | store 持久化类型与字典项。
	store Store
	// snapshots is the typed cache namespace; nil means every read goes to the store. | snapshots 是泛型缓存
	// 命名空间；nil 表示每次读取都走存储。
	snapshots *cache.Cache[Snapshot]
	// log records cache invalidation failures. | log 记录缓存失效失败。
	log *ydlog.Logger
	// options are the normalized effective options. | options 是归一化后的生效选项。
	options Options
}

// New constructs a dictionary service. Store is mandatory; a nil cache disables caching. | New 构造数据字典服务。
// Store 必填；缓存为 nil 时关闭缓存。
func New(store Store, snapshots *cache.Cache[Snapshot], log *ydlog.Logger, options Options) (*Service, error) {
	if store == nil {
		return nil, ErrUnavailable
	}
	if log == nil {
		log = ydlog.Default()
	}
	return &Service{store: store, snapshots: snapshots, log: log, options: normalizeOptions(options)}, nil
}

// Options returns the effective options. | Options 返回生效选项。
func (s *Service) Options() Options { return s.options }

// Resolve returns one enabled dictionary by code. | Resolve 按编码返回一个启用字典。
func (s *Service) Resolve(ctx context.Context, tenantID, code string) (Snapshot, error) {
	tenantID = strings.TrimSpace(tenantID)
	code = strings.ToLower(strings.TrimSpace(code))
	if tenantID == "" || !codeRe.MatchString(code) {
		return Snapshot{}, ErrInvalidInput
	}
	if s.snapshots == nil {
		return s.resolveUncached(ctx, tenantID, code)
	}
	snapshot, err := s.snapshots.GetOrLoad(ctx, cacheKey(tenantID, code), func(ctx context.Context) (Snapshot, error) {
		loaded, loadErr := s.store.Resolve(ctx, tenantID, []string{code})
		if loadErr != nil {
			return Snapshot{}, loadErr
		}
		if value, ok := loaded[code]; ok {
			return value, nil
		}
		return Snapshot{}, cache.ErrNotFound
	})
	if err != nil {
		if errors.Is(err, cache.ErrNotFound) {
			return Snapshot{}, ErrNotFound
		}
		if errors.Is(err, ErrUnavailable) {
			return Snapshot{}, err
		}
		// A cache failure (for example an entry larger than the L1 shard) must not fail the read;
		// the store is the source of truth, so fall back to it. | 缓存故障（例如条目超过 L1 分片）
		// 不得让读取失败；存储才是事实来源，因此回退到存储。
		s.log.Warn("dictionary cache read failed, reading the store instead | 数据字典缓存读取失败，改为读存储",
			ydlog.Err(err),
			ydlog.Str("code", code),
		)
		return s.resolveUncached(ctx, tenantID, code)
	}
	if snapshot.Code == "" {
		return Snapshot{}, ErrNotFound
	}
	return snapshot, nil
}

// ResolveMany returns every enabled dictionary named by codes. A code without an enabled type is
// absent from the result. Warm codes are answered from the cache; a cold code is loaded through
// GetOrLoad, whose single-flight CAS write-back never overwrites a concurrent write. |
// ResolveMany 返回 codes 命中的全部启用字典；没有启用类型的编码不出现在结果中。命中的编码由缓存直接答复；
// 冷编码经 GetOrLoad 加载，其单飞 CAS 回填不会覆盖并发写入。
func (s *Service) ResolveMany(ctx context.Context, tenantID string, codes []string) (map[string]Snapshot, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, ErrInvalidInput
	}
	normalized, err := s.normalizeCodes(codes)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return map[string]Snapshot{}, nil
	}
	if s.snapshots == nil {
		return s.store.Resolve(ctx, tenantID, normalized)
	}
	result := make(map[string]Snapshot, len(normalized))
	for _, code := range normalized {
		snapshot, resolveErr := s.Resolve(ctx, tenantID, code)
		if resolveErr != nil {
			if errors.Is(resolveErr, ErrNotFound) {
				continue
			}
			return nil, resolveErr
		}
		result[code] = snapshot
	}
	return result, nil
}

// resolveUncached reads one snapshot directly from the store. | resolveUncached 直接从存储读取一个快照。
func (s *Service) resolveUncached(ctx context.Context, tenantID, code string) (Snapshot, error) {
	loaded, err := s.store.Resolve(ctx, tenantID, []string{code})
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot, ok := loaded[code]; ok {
		return snapshot, nil
	}
	return Snapshot{}, ErrNotFound
}

// ListTypes returns one page of dictionary types. | ListTypes 返回一页数据字典类型。
func (s *Service) ListTypes(ctx context.Context, tenantID string, filter Filter) (TypePage, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || (filter.Status != "" && !filter.Status.Valid()) {
		return TypePage{}, ErrInvalidInput
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if len([]rune(filter.Query)) > s.options.MaxQueryLen {
		return TypePage{}, ErrInvalidInput
	}
	filter.Page, filter.PageSize = s.page(filter.Page, filter.PageSize)
	items, total, err := s.store.ListTypes(ctx, tenantID, filter)
	if err != nil {
		return TypePage{}, err
	}
	return TypePage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

// GetType reads one type by id inside the tenant. | GetType 读取租户内按 ID 指定的一个类型。
func (s *Service) GetType(ctx context.Context, tenantID, id string) (Type, error) {
	tenantID = strings.TrimSpace(tenantID)
	id = strings.TrimSpace(id)
	if tenantID == "" || id == "" {
		return Type{}, ErrInvalidInput
	}
	current, err := s.store.GetType(ctx, tenantID, id)
	if err != nil {
		return Type{}, err
	}
	if err := s.fillItemCount(ctx, tenantID, &current); err != nil {
		return Type{}, err
	}
	return current, nil
}

// GetTypeByCode reads one type by its immutable code. | GetTypeByCode 按不可变编码读取一个类型。
func (s *Service) GetTypeByCode(ctx context.Context, tenantID, code string) (Type, error) {
	tenantID = strings.TrimSpace(tenantID)
	code = strings.ToLower(strings.TrimSpace(code))
	if tenantID == "" || !codeRe.MatchString(code) {
		return Type{}, ErrInvalidInput
	}
	current, err := s.store.GetTypeByCode(ctx, tenantID, code)
	if err != nil {
		return Type{}, err
	}
	if err := s.fillItemCount(ctx, tenantID, &current); err != nil {
		return Type{}, err
	}
	return current, nil
}

// fillItemCount adds the item count to one type. Administration reads and mutation responses carry
// the same projection as the list endpoint. | fillItemCount 为一个类型补充字典项数量。管理读取与变更响应
// 与列表端点携带同一个投影。
func (s *Service) fillItemCount(ctx context.Context, tenantID string, current *Type) error {
	_, total, err := s.store.ListItems(ctx, tenantID, current.ID, ItemFilter{Page: 1, PageSize: 1})
	if err != nil {
		return err
	}
	current.ItemCount = total
	return nil
}

// CreateType inserts one custom dictionary type. | CreateType 插入一个自定义数据字典类型。
func (s *Service) CreateType(ctx context.Context, tenantID string, input TypeInput) (Type, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return Type{}, ErrInvalidInput
	}
	input, err := s.normalizeTypeInput(input, true)
	if err != nil {
		return Type{}, err
	}
	if _, err := s.store.GetTypeByCode(ctx, tenantID, input.Code); err == nil {
		return Type{}, ErrCodeTaken
	} else if !errors.Is(err, ErrNotFound) {
		return Type{}, err
	}
	id, err := s.options.NewID("dict_")
	if err != nil {
		return Type{}, errs.Wrap(err, ErrUnavailable)
	}
	now := s.options.Now().UTC()
	created := Type{
		CreatedAt:   now,
		UpdatedAt:   now,
		ID:          id,
		TenantID:    tenantID,
		Code:        input.Code,
		Name:        input.Name,
		NameEN:      input.NameEN,
		Description: input.Description,
		Status:      input.Status,
		Sort:        input.Sort,
	}
	if err := s.store.CreateType(ctx, created); err != nil {
		return Type{}, err
	}
	s.invalidate(ctx, tenantID, created.Code)
	return created, nil
}

// UpdateType replaces the editable fields of a type. The code is immutable and ignored. |
// UpdateType 覆盖类型的可编辑字段；编码不可变，会被忽略。
func (s *Service) UpdateType(ctx context.Context, tenantID, id string, input TypeInput) (Type, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || strings.TrimSpace(id) == "" {
		return Type{}, ErrInvalidInput
	}
	current, err := s.store.GetType(ctx, tenantID, id)
	if err != nil {
		return Type{}, err
	}
	if err := s.fillItemCount(ctx, tenantID, &current); err != nil {
		return Type{}, err
	}
	if input.Status == "" {
		input.Status = current.Status
	}
	input, err = s.normalizeTypeInput(input, false)
	if err != nil {
		return Type{}, err
	}
	current.Name = input.Name
	current.NameEN = input.NameEN
	current.Description = input.Description
	current.Status = input.Status
	current.Sort = input.Sort
	current.UpdatedAt = s.options.Now().UTC()
	if err := s.store.UpdateType(ctx, current); err != nil {
		return Type{}, err
	}
	s.invalidate(ctx, tenantID, current.Code)
	return current, nil
}

// SetTypeStatus enables or disables one type. | SetTypeStatus 启用或停用一个类型。
func (s *Service) SetTypeStatus(ctx context.Context, tenantID, id string, status Status) (Type, error) {
	if !status.Valid() {
		return Type{}, ErrInvalidInput
	}
	tenantID = strings.TrimSpace(tenantID)
	id = strings.TrimSpace(id)
	if tenantID == "" || id == "" {
		return Type{}, ErrInvalidInput
	}
	current, err := s.store.GetType(ctx, tenantID, id)
	if err != nil {
		return Type{}, err
	}
	if err := s.fillItemCount(ctx, tenantID, &current); err != nil {
		return Type{}, err
	}
	current.Status = status
	current.UpdatedAt = s.options.Now().UTC()
	if err := s.store.UpdateType(ctx, current); err != nil {
		return Type{}, err
	}
	s.invalidate(ctx, tenantID, current.Code)
	return current, nil
}

// DeleteType deletes one type and every item it owns. Built-in dictionaries are protected. |
// DeleteType 删除一个类型及其全部字典项；内置字典受保护。
func (s *Service) DeleteType(ctx context.Context, tenantID, id string) error {
	tenantID = strings.TrimSpace(tenantID)
	id = strings.TrimSpace(id)
	if tenantID == "" || id == "" {
		return ErrInvalidInput
	}
	current, err := s.store.GetType(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if current.Builtin {
		return ErrBuiltinProtected
	}
	if err := s.store.DeleteType(ctx, tenantID, current.ID); err != nil {
		return err
	}
	s.invalidate(ctx, tenantID, current.Code)
	return nil
}

// ListItems returns one page of items inside one type. | ListItems 返回一个类型内的一页字典项。
func (s *Service) ListItems(ctx context.Context, tenantID, typeID string, filter ItemFilter) (ItemPage, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || strings.TrimSpace(typeID) == "" {
		return ItemPage{}, ErrInvalidInput
	}
	if filter.Status != "" && !filter.Status.Valid() {
		return ItemPage{}, ErrInvalidInput
	}
	if _, err := s.store.GetType(ctx, tenantID, strings.TrimSpace(typeID)); err != nil {
		return ItemPage{}, err
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if len([]rune(filter.Query)) > s.options.MaxQueryLen {
		return ItemPage{}, ErrInvalidInput
	}
	filter.Page, filter.PageSize = s.page(filter.Page, filter.PageSize)
	items, total, err := s.store.ListItems(ctx, tenantID, strings.TrimSpace(typeID), filter)
	if err != nil {
		return ItemPage{}, err
	}
	return ItemPage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

// GetItem reads one item by id inside a type. | GetItem 读取类型内按 ID 指定的一个字典项。
func (s *Service) GetItem(ctx context.Context, tenantID, typeID, id string) (Item, error) {
	tenantID = strings.TrimSpace(tenantID)
	typeID = strings.TrimSpace(typeID)
	id = strings.TrimSpace(id)
	if tenantID == "" || typeID == "" || id == "" {
		return Item{}, ErrInvalidInput
	}
	return s.store.GetItem(ctx, tenantID, typeID, id)
}

// CreateItem inserts one item. A default item replaces the previous default in the same transaction. |
// CreateItem 插入一个字典项；默认项会在同一事务内替换原有默认项。
func (s *Service) CreateItem(ctx context.Context, tenantID, typeID string, input ItemInput) (Item, error) {
	tenantID = strings.TrimSpace(tenantID)
	typeID = strings.TrimSpace(typeID)
	if tenantID == "" || typeID == "" {
		return Item{}, ErrInvalidInput
	}
	dictionaryType, err := s.store.GetType(ctx, tenantID, typeID)
	if err != nil {
		return Item{}, err
	}
	input, err = s.normalizeItemInput(input, true)
	if err != nil {
		return Item{}, err
	}
	if _, err := s.store.GetItemByValue(ctx, tenantID, typeID, input.Value); err == nil {
		return Item{}, ErrValueTaken
	} else if !errors.Is(err, ErrNotFound) {
		return Item{}, err
	}
	id, err := s.options.NewID("dit_")
	if err != nil {
		return Item{}, errs.Wrap(err, ErrUnavailable)
	}
	now := s.options.Now().UTC()
	created := Item{
		CreatedAt: now,
		UpdatedAt: now,
		ID:        id,
		TenantID:  tenantID,
		TypeID:    typeID,
		Value:     input.Value,
		Label:     input.Label,
		LabelEN:   input.LabelEN,
		Extra:     input.Extra,
		Remark:    input.Remark,
		Status:    input.Status,
		IsDefault: input.Default,
		Sort:      input.Sort,
	}
	if err := s.store.CreateItem(ctx, created, s.options.MaxItems); err != nil {
		return Item{}, err
	}
	s.invalidate(ctx, tenantID, dictionaryType.Code)
	return created, nil
}

// UpdateItem replaces the editable fields of an item. The value is immutable and ignored. |
// UpdateItem 覆盖字典项的可编辑字段；机器值不可变，会被忽略。
func (s *Service) UpdateItem(ctx context.Context, tenantID, typeID, id string, input ItemInput) (Item, error) {
	tenantID = strings.TrimSpace(tenantID)
	typeID = strings.TrimSpace(typeID)
	id = strings.TrimSpace(id)
	if tenantID == "" || typeID == "" || id == "" {
		return Item{}, ErrInvalidInput
	}
	dictionaryType, err := s.store.GetType(ctx, tenantID, typeID)
	if err != nil {
		return Item{}, err
	}
	current, err := s.store.GetItem(ctx, tenantID, typeID, id)
	if err != nil {
		return Item{}, err
	}
	if input.Status == "" {
		input.Status = current.Status
	}
	input, err = s.normalizeItemInput(input, false)
	if err != nil {
		return Item{}, err
	}
	current.Label = input.Label
	current.LabelEN = input.LabelEN
	current.Extra = input.Extra
	current.Remark = input.Remark
	current.Status = input.Status
	current.IsDefault = input.Default
	current.Sort = input.Sort
	current.UpdatedAt = s.options.Now().UTC()
	if err := s.store.UpdateItem(ctx, current); err != nil {
		return Item{}, err
	}
	s.invalidate(ctx, tenantID, dictionaryType.Code)
	return current, nil
}

// SetItemStatus enables or disables one item. Disabling the default item clears the default flag. |
// SetItemStatus 启用或停用一个字典项；停用默认项会清除默认标记。
func (s *Service) SetItemStatus(ctx context.Context, tenantID, typeID, id string, status Status) (Item, error) {
	if !status.Valid() {
		return Item{}, ErrInvalidInput
	}
	tenantID = strings.TrimSpace(tenantID)
	typeID = strings.TrimSpace(typeID)
	id = strings.TrimSpace(id)
	if tenantID == "" || typeID == "" || id == "" {
		return Item{}, ErrInvalidInput
	}
	dictionaryType, err := s.store.GetType(ctx, tenantID, typeID)
	if err != nil {
		return Item{}, err
	}
	if _, err := s.store.GetItem(ctx, tenantID, typeID, id); err != nil {
		return Item{}, err
	}
	if err := s.store.SetItemStatus(ctx, tenantID, typeID, id, status, s.options.Now().UTC()); err != nil {
		return Item{}, err
	}
	s.invalidate(ctx, tenantID, dictionaryType.Code)
	return s.store.GetItem(ctx, tenantID, typeID, id)
}

// DeleteItem deletes one item. | DeleteItem 删除一个字典项。
func (s *Service) DeleteItem(ctx context.Context, tenantID, typeID, id string) error {
	tenantID = strings.TrimSpace(tenantID)
	typeID = strings.TrimSpace(typeID)
	id = strings.TrimSpace(id)
	if tenantID == "" || typeID == "" || id == "" {
		return ErrInvalidInput
	}
	dictionaryType, err := s.store.GetType(ctx, tenantID, typeID)
	if err != nil {
		return err
	}
	if _, err := s.store.GetItem(ctx, tenantID, typeID, id); err != nil {
		return err
	}
	if err := s.store.DeleteItem(ctx, tenantID, typeID, id); err != nil {
		return err
	}
	s.invalidate(ctx, tenantID, dictionaryType.Code)
	return nil
}

// Invalidate removes one dictionary from both cache levels. External writers that update the tables
// directly must call it; the service does so after every write of its own. | Invalidate 从两级缓存中移除
// 一个字典。直接写表的调用方必须调用它；服务自身在每次写入后都会调用。
func (s *Service) Invalidate(ctx context.Context, tenantID, code string) error {
	if s.snapshots == nil {
		return nil
	}
	return s.snapshots.Delete(ctx, cacheKey(tenantID, code))
}

// invalidate removes one dictionary from the cache and logs a failure without failing the write. |
// invalidate 从缓存移除一个字典；失效失败只记录日志，不让写入失败。
func (s *Service) invalidate(ctx context.Context, tenantID, code string) {
	if err := s.Invalidate(ctx, tenantID, code); err != nil {
		s.log.Warn("dictionary cache invalidation failed | 数据字典缓存失效失败",
			ydlog.Err(err),
			ydlog.Str("code", code),
		)
	}
}

// normalizeCodes trims, lower-cases, validates and de-duplicates a batch, preserving order. |
// normalizeCodes 去空白、转小写、校验并按原顺序去重一批编码。
func (s *Service) normalizeCodes(codes []string) ([]string, error) {
	if len(codes) > s.options.MaxBatchCodes {
		return nil, ErrTooManyCodes
	}
	seen := make(map[string]struct{}, len(codes))
	out := make([]string, 0, len(codes))
	for _, code := range codes {
		code = strings.ToLower(strings.TrimSpace(code))
		if code == "" {
			continue
		}
		if !codeRe.MatchString(code) {
			return nil, errs.Detail(ErrInvalidInput, "code=%[1]q | 编码=%[1]q", code)
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out, nil
}

// page clamps a requested page and page size into the configured range. | page 把请求的页码与页长收敛到配置范围。
func (s *Service) page(page, size int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = s.options.PageSize
	}
	if size > s.options.MaxPageSize {
		size = s.options.MaxPageSize
	}
	return page, size
}

// cacheKey builds the cache key of one dictionary. The separator cannot appear in a dictionary code,
// so two tenants can never map onto the same key. | cacheKey 构造一个字典的缓存键。分隔符不可能出现在字典
// 编码里，因此两个租户不会映射到同一个键。
func cacheKey(tenantID, code string) string { return tenantID + "|" + code }
