package cms

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// Tags returns the tenant tags, optionally filtered by a keyword. | Tags 返回租户标签，可按关键词过滤。
func (s *Service) Tags(ctx context.Context, tenantID, keyword string) ([]Tag, error) {
	tags, err := s.store.ListTags(ctx, tenantID, strings.TrimSpace(keyword))
	if err != nil {
		return nil, errx.Wrap(err, ErrUnavailable)
	}
	return tags, nil
}

// TagBySlug returns one tag by slug. | TagBySlug 按别名返回一个标签。
func (s *Service) TagBySlug(ctx context.Context, tenantID, slug string) (Tag, error) {
	tag, err := s.store.GetTagBySlug(ctx, tenantID, strings.TrimSpace(strings.ToLower(slug)))
	if err != nil {
		return Tag{}, mapStoreError(err, ErrTagNotFound)
	}
	return tag, nil
}

// CreateTag validates and inserts a tag. | CreateTag 校验并插入一个标签。
func (s *Service) CreateTag(ctx context.Context, actor Actor, input TagInput) (Tag, error) {
	if !actor.valid() {
		return Tag{}, ErrForbidden
	}
	id, err := s.mustID("tag_")
	if err != nil {
		return Tag{}, err
	}
	name, slug, err := tagFields(input, "tag-"+strings.ReplaceAll(id, "_", "-"))
	if err != nil {
		return Tag{}, err
	}
	if _, lookupErr := s.store.GetTagBySlug(ctx, actor.TenantID, slug); lookupErr == nil {
		return Tag{}, ErrTagSlugTaken
	} else if !isMissing(lookupErr) {
		return Tag{}, errx.Wrap(lookupErr, ErrUnavailable)
	}
	now := s.options.Now()
	tag := Tag{ID: id, TenantID: actor.TenantID, Name: name, Slug: slug, CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreateTag(ctx, actor.TenantID, tag); err != nil {
		if errors.Is(err, ErrTagSlugTaken) {
			return Tag{}, ErrTagSlugTaken
		}
		return Tag{}, errx.Wrap(err, ErrUnavailable)
	}
	s.audit(ctx, actor, "tag.create", "tag", tag.ID, tag.Name)
	return tag, nil
}

// UpdateTag changes a tag's name and slug. | UpdateTag 修改标签的名称与别名。
func (s *Service) UpdateTag(ctx context.Context, actor Actor, id string, input TagInput) (Tag, error) {
	if !actor.valid() {
		return Tag{}, ErrForbidden
	}
	current, err := s.store.GetTag(ctx, actor.TenantID, id)
	if err != nil {
		return Tag{}, mapStoreError(err, ErrTagNotFound)
	}
	name, slug, err := tagFields(input, "tag-"+strings.ReplaceAll(current.ID, "_", "-"))
	if err != nil {
		return Tag{}, err
	}
	if slug != current.Slug {
		existing, lookupErr := s.store.GetTagBySlug(ctx, actor.TenantID, slug)
		if lookupErr == nil && existing.ID != current.ID {
			return Tag{}, ErrTagSlugTaken
		}
		if lookupErr != nil && !isMissing(lookupErr) {
			return Tag{}, errx.Wrap(lookupErr, ErrUnavailable)
		}
	}
	current.Name = name
	current.Slug = slug
	current.UpdatedAt = s.options.Now()
	if err := s.store.UpdateTag(ctx, actor.TenantID, current); err != nil {
		if errors.Is(err, ErrTagSlugTaken) {
			return Tag{}, ErrTagSlugTaken
		}
		return Tag{}, errx.Wrap(err, ErrUnavailable)
	}
	s.audit(ctx, actor, "tag.update", "tag", current.ID, current.Name)
	return current, nil
}

// DeleteTag removes a tag that no article carries. | DeleteTag 删除没有文章携带的标签。
func (s *Service) DeleteTag(ctx context.Context, actor Actor, id string) error {
	if !actor.valid() {
		return ErrForbidden
	}
	tag, err := s.store.GetTag(ctx, actor.TenantID, id)
	if err != nil {
		return mapStoreError(err, ErrTagNotFound)
	}
	count, err := s.store.CountArticlesOfTag(ctx, actor.TenantID, id)
	if err != nil {
		return errx.Wrap(err, ErrUnavailable)
	}
	if count > 0 {
		return ErrTagInUse
	}
	if err := s.store.DeleteTagIfUnused(ctx, actor.TenantID, id); err != nil {
		if errors.Is(err, ErrTagInUse) {
			return ErrTagInUse
		}
		return mapStoreError(err, ErrTagNotFound)
	}
	s.audit(ctx, actor, "tag.delete", "tag", id, tag.Name)
	return nil
}

// resolveTags turns tag ids and names into a deduplicated tag list, creating names on demand. |
// resolveTags 把标签标识与名称解析为去重后的标签列表，按需创建名称。
func (s *Service) resolveTags(ctx context.Context, actor Actor, ids, names []string) ([]Tag, error) {
	seen := make(map[string]bool, len(ids)+len(names))
	out := make([]Tag, 0, len(ids)+len(names))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		tag, err := s.store.GetTag(ctx, actor.TenantID, id)
		if err != nil {
			return nil, mapStoreError(err, ErrTagNotFound)
		}
		seen[id] = true
		out = append(out, tag)
		if len(out) > s.options.MaxTags {
			return nil, ErrTooManyTags
		}
	}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if len(name) > 80 {
			return nil, errx.Detail(ErrInvalidInput, "tag name is too long | 标签名称过长")
		}
		slug := slugify(name)
		id, idErr := s.mustID("tag_")
		if idErr != nil {
			return nil, idErr
		}
		if slug == "" {
			slug = "tag-" + strings.ReplaceAll(id, "_", "-")
		}
		tag, err := s.store.GetTagBySlug(ctx, actor.TenantID, slug)
		if err != nil {
			if !isMissing(err) {
				return nil, errx.Wrap(err, ErrUnavailable)
			}
			now := s.options.Now()
			tag = Tag{ID: id, TenantID: actor.TenantID, Name: name, Slug: slug, CreatedAt: now, UpdatedAt: now}
			if createErr := s.store.CreateTag(ctx, actor.TenantID, tag); createErr != nil {
				if errors.Is(createErr, ErrTagSlugTaken) {
					tag, err = s.store.GetTagBySlug(ctx, actor.TenantID, slug)
					if err != nil {
						return nil, errx.Wrap(err, ErrUnavailable)
					}
				} else {
					return nil, errx.Wrap(createErr, ErrUnavailable)
				}
			}
		}
		if seen[tag.ID] {
			continue
		}
		seen[tag.ID] = true
		out = append(out, tag)
		if len(out) > s.options.MaxTags {
			return nil, ErrTooManyTags
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// tagFields validates the shared tag name and slug fields. | tagFields 校验标签共用的名称与别名字段。
func tagFields(input TagInput, fallbackID string) (string, string, error) {
	name := strings.TrimSpace(input.Name)
	if err := checkText("tag name | 标签名称", name, 80); err != nil {
		return "", "", err
	}
	slug := strings.TrimSpace(strings.ToLower(input.Slug))
	if slug == "" {
		slug = slugify(name)
	}
	if slug == "" {
		slug = fallbackID
	}
	if !validSlug(slug) || len(slug) > 80 {
		// 列宽是 varchar(80)：放行到 120 会让 MySQL/PostgreSQL 在插入时失败，而 SQLite 静默接受。 |
		// The column is varchar(80): allowing 120 would fail on MySQL/PostgreSQL while SQLite accepts it silently.
		return "", "", errx.Detail(ErrInvalidInput, "invalid tag slug %[1]q | 非法的标签别名 %[1]q", slug)
	}
	return name, slug, nil
}
