package cms

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
)

// CategoryTree returns the tenant tree ordered by SortOrder and Name. Hidden categories and their
// descendants are omitted unless includeHidden is true. | CategoryTree 按 SortOrder 与 Name 返回租户树。
// 除非 includeHidden 为真，否则隐藏分类及其后代都被省略。
func (s *Service) CategoryTree(ctx context.Context, tenantID string, includeHidden bool) ([]CategoryNode, error) {
	categories, err := s.store.ListCategories(ctx, tenantID)
	if err != nil {
		return nil, errx.Wrap(err, ErrUnavailable)
	}
	ids := make([]string, 0, len(categories))
	for _, category := range categories {
		ids = append(ids, category.ID)
	}
	counts := map[string]int64{}
	if len(ids) > 0 {
		counts, err = s.store.CountArticlesByCategories(ctx, tenantID, ids)
		if err != nil {
			return nil, errx.Wrap(err, ErrUnavailable)
		}
	}
	children := make(map[string][]Category, len(categories))
	for _, category := range categories {
		if !includeHidden && !category.Visible {
			continue
		}
		children[category.ParentID] = append(children[category.ParentID], category)
	}
	for parent := range children {
		sort.SliceStable(children[parent], func(i, j int) bool {
			left, right := children[parent][i], children[parent][j]
			if left.SortOrder != right.SortOrder {
				return left.SortOrder < right.SortOrder
			}
			return left.Name < right.Name
		})
	}
	var build func(parentID string) []CategoryNode
	build = func(parentID string) []CategoryNode {
		rows := children[parentID]
		out := make([]CategoryNode, 0, len(rows))
		for _, category := range rows {
			out = append(out, CategoryNode{
				Category:     category,
				Children:     build(category.ID),
				ArticleCount: counts[category.ID],
			})
		}
		return out
	}
	return build(""), nil
}

// CategoryOptions returns a pre-order list for selectors. When excludeID is set, that category and
// its whole subtree are omitted so a category can never be moved below itself. |
// CategoryOptions 返回供选择器使用的先序列表。excludeID 非空时省略该分类及其整棵子树，
// 因此分类永远不会被移动到自身之下。
func (s *Service) CategoryOptions(ctx context.Context, tenantID, excludeID string) ([]CategoryOption, error) {
	categories, err := s.store.ListCategories(ctx, tenantID)
	if err != nil {
		return nil, errx.Wrap(err, ErrUnavailable)
	}
	ids := make([]string, 0, len(categories))
	for _, category := range categories {
		ids = append(ids, category.ID)
	}
	counts := map[string]int64{}
	if len(ids) > 0 {
		counts, err = s.store.CountArticlesByCategories(ctx, tenantID, ids)
		if err != nil {
			return nil, errx.Wrap(err, ErrUnavailable)
		}
	}
	excluded := map[string]bool{}
	if excludeID != "" {
		var root *Category
		for i := range categories {
			if categories[i].ID == excludeID {
				root = &categories[i]
				break
			}
		}
		if root == nil {
			return nil, ErrCategoryNotFound
		}
		for _, category := range categories {
			if category.ID == root.ID || strings.HasPrefix(category.Path, root.Path) {
				excluded[category.ID] = true
			}
		}
	}
	children := make(map[string][]Category, len(categories))
	for _, category := range categories {
		if excluded[category.ID] {
			continue
		}
		children[category.ParentID] = append(children[category.ParentID], category)
	}
	for parent := range children {
		sort.SliceStable(children[parent], func(i, j int) bool {
			left, right := children[parent][i], children[parent][j]
			if left.SortOrder != right.SortOrder {
				return left.SortOrder < right.SortOrder
			}
			return left.Name < right.Name
		})
	}
	var out []CategoryOption
	var walk func(parentID string)
	walk = func(parentID string) {
		for _, category := range children[parentID] {
			out = append(out, CategoryOption{
				ID:           category.ID,
				Name:         category.Name,
				Slug:         category.Slug,
				Path:         category.Path,
				Depth:        category.Depth,
				SortOrder:    category.SortOrder,
				Visible:      category.Visible,
				ArticleCount: counts[category.ID],
			})
			walk(category.ID)
		}
	}
	walk("")
	return out, nil
}

// PublicCategoryOptions returns the selector list for the public site: hidden categories and their
// whole subtrees are omitted, and the article counts include only published, public, due articles. |
// PublicCategoryOptions 返回公开站点的分类选择列表：隐藏分类及其整棵子树都被省略，文章计数只包含
// 已发布、公开且已到发布时间的文章。
func (s *Service) PublicCategoryOptions(ctx context.Context, tenantID string) ([]CategoryOption, error) {
	categories, err := s.store.ListCategories(ctx, tenantID)
	if err != nil {
		return nil, errx.Wrap(err, ErrUnavailable)
	}
	ids := make([]string, 0, len(categories))
	for _, category := range categories {
		ids = append(ids, category.ID)
	}
	counts := map[string]int64{}
	if len(ids) > 0 {
		counts, err = s.store.CountPublicArticlesByCategories(ctx, tenantID, ids, s.options.Now())
		if err != nil {
			return nil, errx.Wrap(err, ErrUnavailable)
		}
	}
	byID := make(map[string]Category, len(categories))
	for _, category := range categories {
		byID[category.ID] = category
	}
	hidden := make(map[string]bool, len(categories))
	state := make(map[string]uint8, len(categories))
	var isHidden func(Category) bool
	isHidden = func(category Category) bool {
		if value, ok := hidden[category.ID]; ok {
			return value
		}
		// A cycle can only come from corrupted data; treat it as hidden instead of recursing forever. |
		// 环只可能来自损坏的数据；按隐藏处理，避免无限递归。
		if state[category.ID] == 1 {
			hidden[category.ID] = true
			return true
		}
		state[category.ID] = 1
		result := !category.Visible
		if !result && category.ParentID != "" {
			if parent, ok := byID[category.ParentID]; ok {
				result = isHidden(parent)
			}
		}
		state[category.ID] = 2
		hidden[category.ID] = result
		return result
	}
	for _, category := range categories {
		isHidden(category)
	}
	children := make(map[string][]Category, len(categories))
	for _, category := range categories {
		if hidden[category.ID] {
			continue
		}
		children[category.ParentID] = append(children[category.ParentID], category)
	}
	for parent := range children {
		sort.SliceStable(children[parent], func(i, j int) bool {
			left, right := children[parent][i], children[parent][j]
			if left.SortOrder != right.SortOrder {
				return left.SortOrder < right.SortOrder
			}
			return left.Name < right.Name
		})
	}
	out := make([]CategoryOption, 0, len(categories))
	var walk func(parentID string)
	walk = func(parentID string) {
		for _, category := range children[parentID] {
			out = append(out, CategoryOption{
				ID:           category.ID,
				Name:         category.Name,
				Slug:         category.Slug,
				Path:         category.Path,
				Depth:        category.Depth,
				SortOrder:    category.SortOrder,
				Visible:      category.Visible,
				ArticleCount: counts[category.ID],
			})
			walk(category.ID)
		}
	}
	walk("")
	return out, nil
}

// Category returns one category by id. | Category 按标识返回一个分类。
func (s *Service) Category(ctx context.Context, tenantID, id string) (Category, error) {
	if strings.TrimSpace(id) == "" {
		return Category{}, ErrCategoryNotFound
	}
	category, err := s.store.GetCategory(ctx, tenantID, id)
	if err != nil {
		return Category{}, mapStoreError(err, ErrCategoryNotFound)
	}
	return category, nil
}

// CategoryBySlug returns one category by slug. | CategoryBySlug 按别名返回一个分类。
func (s *Service) CategoryBySlug(ctx context.Context, tenantID, slug string) (Category, error) {
	category, err := s.store.GetCategoryBySlug(ctx, tenantID, strings.TrimSpace(strings.ToLower(slug)))
	if err != nil {
		return Category{}, mapStoreError(err, ErrCategoryNotFound)
	}
	return category, nil
}

// CreateCategory validates and inserts a category. | CreateCategory 校验并插入一个分类。
func (s *Service) CreateCategory(ctx context.Context, actor Actor, input CategoryInput) (Category, error) {
	if !actor.valid() {
		return Category{}, ErrForbidden
	}
	name, err := categoryName(input.Name)
	if err != nil {
		return Category{}, err
	}
	if err := checkOptionalText("category description | 分类说明", strings.TrimSpace(input.Description), 4000); err != nil {
		return Category{}, err
	}
	id, err := s.mustID("cat_")
	if err != nil {
		return Category{}, err
	}
	slug, err := s.normalizedSlug(input.Slug, name, "cat-"+strings.ReplaceAll(id, "_", "-"))
	if err != nil {
		return Category{}, err
	}
	if _, err := s.store.GetCategoryBySlug(ctx, actor.TenantID, slug); err == nil {
		return Category{}, ErrCategorySlugTaken
	} else if !isMissing(err) {
		return Category{}, errx.Wrap(err, ErrUnavailable)
	}
	parentPath := "/"
	depth := 0
	parentID := strings.TrimSpace(input.ParentID)
	if parentID != "" {
		parent, parentErr := s.store.GetCategory(ctx, actor.TenantID, parentID)
		if parentErr != nil {
			if isMissing(parentErr) {
				return Category{}, ErrCategoryParent
			}
			return Category{}, errx.Wrap(parentErr, ErrUnavailable)
		}
		parentPath = parent.Path
		depth = parent.Depth + 1
	} else {
		parentID = ""
	}
	if depth > MaxCategoryDepth {
		return Category{}, errx.Detail(ErrCategoryParent, "depth %[1]d exceeds the limit %[2]d | 深度 %[1]d 超过上限 %[2]d",
			depth, MaxCategoryDepth)
	}
	now := s.options.Now()
	category := Category{
		ID:          id,
		TenantID:    actor.TenantID,
		ParentID:    parentID,
		Name:        name,
		Slug:        slug,
		Description: strings.TrimSpace(input.Description),
		Path:        parentPath + id + "/",
		CreatedBy:   actor.ID,
		SortOrder:   input.SortOrder,
		Depth:       depth,
		Visible:     input.Visible,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.store.CreateCategory(ctx, actor.TenantID, category); err != nil {
		if errors.Is(err, ErrCategorySlugTaken) {
			return Category{}, ErrCategorySlugTaken
		}
		if errors.Is(err, ErrCategoryParent) {
			return Category{}, ErrCategoryParent
		}
		return Category{}, errx.Wrap(err, ErrUnavailable)
	}
	// The store recomputes path and depth under the parent lock; re-read so the caller receives what was
	// actually stored even if the parent moved concurrently. |
	// 存储层在父级锁下重新计算路径与深度；重新读取，使调用方拿到真实落库值（即使父级被并发移动）。
	if stored, getErr := s.store.GetCategory(ctx, actor.TenantID, category.ID); getErr == nil {
		category = stored
	}
	s.audit(ctx, actor, "category.create", "category", category.ID, category.Name)
	return category, nil
}

// UpdateCategory updates one category and moves its subtree when the parent changes. |
// UpdateCategory 更新一个分类，并在父级变化时移动整棵子树。
func (s *Service) UpdateCategory(ctx context.Context, actor Actor, id string, input CategoryInput) (Category, error) {
	if !actor.valid() {
		return Category{}, ErrForbidden
	}
	current, err := s.store.GetCategory(ctx, actor.TenantID, id)
	if err != nil {
		return Category{}, mapStoreError(err, ErrCategoryNotFound)
	}
	name, err := categoryName(input.Name)
	if err != nil {
		return Category{}, err
	}
	if err := checkOptionalText("category description | 分类说明", strings.TrimSpace(input.Description), 4000); err != nil {
		return Category{}, err
	}
	slug, err := s.normalizedSlug(input.Slug, name, "cat-"+strings.ReplaceAll(current.ID, "_", "-"))
	if err != nil {
		return Category{}, err
	}
	if slug != current.Slug {
		existing, lookupErr := s.store.GetCategoryBySlug(ctx, actor.TenantID, slug)
		if lookupErr == nil && existing.ID != current.ID {
			return Category{}, ErrCategorySlugTaken
		}
		if lookupErr != nil && !isMissing(lookupErr) {
			return Category{}, errx.Wrap(lookupErr, ErrUnavailable)
		}
	}
	parentID := strings.TrimSpace(input.ParentID)
	parentPath := "/"
	depth := 0
	if parentID != "" {
		if parentID == current.ID {
			return Category{}, ErrCategoryCycle
		}
		parent, parentErr := s.store.GetCategory(ctx, actor.TenantID, parentID)
		if parentErr != nil {
			if isMissing(parentErr) {
				return Category{}, ErrCategoryParent
			}
			return Category{}, errx.Wrap(parentErr, ErrUnavailable)
		}
		if strings.HasPrefix(parent.Path, current.Path) {
			return Category{}, ErrCategoryCycle
		}
		parentPath = parent.Path
		depth = parent.Depth + 1
	}
	if depth > MaxCategoryDepth {
		return Category{}, errx.Detail(ErrCategoryParent, "depth %[1]d exceeds the limit %[2]d | 深度 %[1]d 超过上限 %[2]d",
			depth, MaxCategoryDepth)
	}
	updated := current
	updated.Name = name
	updated.Slug = slug
	updated.Description = strings.TrimSpace(input.Description)
	updated.ParentID = parentID
	updated.SortOrder = input.SortOrder
	updated.Visible = input.Visible
	updated.UpdatedAt = s.options.Now()
	if updated.ParentID != current.ParentID || depth != current.Depth {
		updated.Path = parentPath + current.ID + "/"
		updated.Depth = depth
		if moveErr := s.store.MoveCategory(ctx, actor.TenantID, updated, current.Path); moveErr != nil {
			if errors.Is(moveErr, ErrCategorySlugTaken) {
				return Category{}, ErrCategorySlugTaken
			}
			if errors.Is(moveErr, ErrCategoryParent) {
				return Category{}, ErrCategoryParent
			}
			return Category{}, errx.Wrap(moveErr, ErrUnavailable)
		}
	} else if err := s.store.UpdateCategory(ctx, actor.TenantID, updated); err != nil {
		if errors.Is(err, ErrCategorySlugTaken) {
			return Category{}, ErrCategorySlugTaken
		}
		return Category{}, errx.Wrap(err, ErrUnavailable)
	}
	// Re-read the row: a concurrent move may have changed its parent/path after the service snapshot. |
	// 重新读取该行：并发移动可能在服务快照之后改变其父级与路径。
	if stored, getErr := s.store.GetCategory(ctx, actor.TenantID, updated.ID); getErr == nil {
		updated = stored
	}
	s.audit(ctx, actor, "category.update", "category", updated.ID, updated.Name)
	return updated, nil
}

// DeleteCategory removes a category that has no children and no articles. | DeleteCategory 删除没有子分类且没有文章的分类。
func (s *Service) DeleteCategory(ctx context.Context, actor Actor, id string) error {
	if !actor.valid() {
		return ErrForbidden
	}
	category, err := s.store.GetCategory(ctx, actor.TenantID, id)
	if err != nil {
		return mapStoreError(err, ErrCategoryNotFound)
	}
	children, err := s.store.CountCategoryChildren(ctx, actor.TenantID, id)
	if err != nil {
		return errx.Wrap(err, ErrUnavailable)
	}
	if children > 0 {
		return ErrCategoryNotEmpty
	}
	counts, err := s.store.CountArticlesByCategories(ctx, actor.TenantID, []string{id})
	if err != nil {
		return errx.Wrap(err, ErrUnavailable)
	}
	if counts[id] > 0 {
		return ErrCategoryNotEmpty
	}
	if err := s.store.DeleteCategoryIfEmpty(ctx, actor.TenantID, id); err != nil {
		if errors.Is(err, ErrCategoryNotEmpty) {
			return ErrCategoryNotEmpty
		}
		return mapStoreError(err, ErrCategoryNotFound)
	}
	s.audit(ctx, actor, "category.delete", "category", id, category.Name)
	return nil
}

// SubtreeIDs returns the category and every descendant from a category list. | SubtreeIDs 从分类列表返回该分类及全部后代。
func SubtreeIDs(categories []Category, rootID string) []string {
	if strings.TrimSpace(rootID) == "" {
		return nil
	}
	root := ""
	for _, category := range categories {
		if category.ID == rootID {
			root = category.Path
			break
		}
	}
	if root == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, category := range categories {
		if category.ID == rootID || strings.HasPrefix(category.Path, root) {
			out = append(out, category.ID)
		}
	}
	return out
}

// categoryName validates and trims a category name. | categoryName 校验并裁剪分类名称。
func categoryName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if err := checkText("category name | 分类名称", name, 160); err != nil {
		return "", err
	}
	return name, nil
}

// isMissing reports a store-level not-found. | isMissing 报告存储层的未找到。
func isMissing(err error) bool {
	return err != nil && (errors.Is(err, ErrNotFound) || errors.Is(err, ErrCategoryNotFound) ||
		errors.Is(err, ErrArticleNotFound) || errors.Is(err, ErrTagNotFound) ||
		errors.Is(err, ErrMediaNotFound) || errors.Is(err, ErrCommentNotFound) ||
		errors.Is(err, ErrRevisionNotFound))
}

// audit appends one audit row and never fails the caller's operation. | audit 追加一条审计记录，绝不让调用方的操作失败。
func (s *Service) audit(ctx context.Context, actor Actor, action, kind, targetID, detail string) {
	if !actor.valid() {
		return
	}
	id, err := s.mustID("aud_")
	if err != nil {
		return
	}
	entry := AuditEntry{
		ID:         id,
		TenantID:   actor.TenantID,
		ActorID:    actor.ID,
		ActorName:  actor.DisplayName,
		Action:     action,
		TargetKind: kind,
		TargetID:   targetID,
		Detail:     detail,
		CreatedAt:  s.options.Now(),
	}
	if err := s.store.AddAudit(ctx, actor.TenantID, entry); err != nil {
		s.log.Warn("CMS audit entry was not stored | CMS 审计记录未写入", logx.Str("action", action), logx.Err(err))
	}
}
