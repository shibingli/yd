package cms

import (
	"context"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// Settings returns the tenant site settings. | Settings 返回租户站点设置。
func (s *Service) Settings(ctx context.Context, tenantID string) (Settings, error) {
	return s.settings(ctx, tenantID)
}

// settings loads the settings row, returning defaults when it has not been created yet. |
// settings 读取设置行，尚未创建时返回默认值。
func (s *Service) settings(ctx context.Context, tenantID string) (Settings, error) {
	settings, err := s.store.GetSettings(ctx, tenantID)
	if err != nil {
		if isMissing(err) {
			now := s.options.Now()
			return Settings{
				TenantID:          tenantID,
				SiteName:          "Site | 站点",
				PostsPerPage:      10,
				CommentsEnabled:   true,
				CommentModeration: true,
				RSSEnabled:        true,
				CreatedAt:         now,
				UpdatedAt:         now,
			}, nil
		}
		return Settings{}, errx.Wrap(err, ErrUnavailable)
	}
	if settings.PostsPerPage <= 0 {
		settings.PostsPerPage = 10
	}
	return settings, nil
}

// SaveSettings updates the tenant site settings. | SaveSettings 更新租户站点设置。
func (s *Service) SaveSettings(ctx context.Context, actor Actor, input SettingsInput) (Settings, error) {
	if !actor.valid() || !actor.SiteManage {
		return Settings{}, ErrForbidden
	}
	siteName := strings.TrimSpace(input.SiteName)
	if err := checkText("site name | 站点名称", siteName, 200); err != nil {
		return Settings{}, err
	}
	if len(input.SiteDescription) > 500 || len(input.SiteKeywords) > 500 || len(input.FooterText) > 500 {
		return Settings{}, errx.Detail(ErrInvalidInput, "settings text is too long | 设置文本过长")
	}
	pageSize := input.PostsPerPage
	if pageSize <= 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}
	now := s.options.Now()
	settings := Settings{
		TenantID:          actor.TenantID,
		SiteName:          siteName,
		SiteDescription:   strings.TrimSpace(input.SiteDescription),
		SiteKeywords:      strings.TrimSpace(input.SiteKeywords),
		FooterText:        strings.TrimSpace(input.FooterText),
		PostsPerPage:      pageSize,
		CommentsEnabled:   input.CommentsEnabled,
		CommentModeration: input.CommentModeration,
		RSSEnabled:        input.RSSEnabled,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	existing, err := s.store.GetSettings(ctx, actor.TenantID)
	if err == nil {
		settings.CreatedAt = existing.CreatedAt
	} else if !isMissing(err) {
		return Settings{}, errx.Wrap(err, ErrUnavailable)
	}
	if err := s.store.SaveSettings(ctx, actor.TenantID, settings); err != nil {
		return Settings{}, errx.Wrap(err, ErrUnavailable)
	}
	s.audit(ctx, actor, "settings.save", "settings", actor.TenantID, settings.SiteName)
	return settings, nil
}
