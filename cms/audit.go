package cms

import (
	"context"

	"github.com/shibingli/yd/core/errx"
)

// Audit returns a normalised page of administrative actions. | Audit 返回规范化后的一页管理动作。
func (s *Service) Audit(ctx context.Context, tenantID string, filter AuditFilter) (AuditPage, error) {
	filter.Page, filter.PageSize = s.normalizePage(filter.Page, filter.PageSize)
	page, err := s.store.ListAudit(ctx, tenantID, filter)
	if err != nil {
		return AuditPage{}, errx.Wrap(err, ErrUnavailable)
	}
	page.Page = filter.Page
	page.PageSize = filter.PageSize
	page.TotalPages = totalPages(page.Total, filter.PageSize)
	return page, nil
}
