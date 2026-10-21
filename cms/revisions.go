package cms

import (
	"context"

	"github.com/shibingli/yd/core/errx"
)

// Revisions returns the article's revision history, newest first. | Revisions 按从新到旧返回文章修订历史。
func (s *Service) Revisions(ctx context.Context, tenantID, articleID string) ([]Revision, error) {
	if _, err := s.store.GetArticle(ctx, tenantID, articleID); err != nil {
		return nil, mapStoreError(err, ErrArticleNotFound)
	}
	revisions, err := s.store.ListRevisions(ctx, tenantID, articleID)
	if err != nil {
		return nil, errx.Wrap(err, ErrUnavailable)
	}
	return revisions, nil
}

// Revision returns one revision. | Revision 返回一份修订。
func (s *Service) Revision(ctx context.Context, tenantID, articleID, revisionID string) (Revision, error) {
	revision, err := s.store.GetRevision(ctx, tenantID, articleID, revisionID)
	if err != nil {
		return Revision{}, mapStoreError(err, ErrRevisionNotFound)
	}
	return revision, nil
}

// RestoreRevision snapshots the current body and replaces it with the revision. The revision body is
// rendered again instead of trusting the stored HTML snapshot. | RestoreRevision 先快照当前正文，
// 再用修订替换它。修订正文会重新渲染，而不是信任已存 HTML 快照。
func (s *Service) RestoreRevision(ctx context.Context, actor Actor, articleID, revisionID string) (Article, error) {
	if !actor.valid() {
		return Article{}, ErrForbidden
	}
	article, err := s.store.GetArticle(ctx, actor.TenantID, articleID)
	if err != nil {
		return Article{}, mapStoreError(err, ErrArticleNotFound)
	}
	if article.AuthorID != actor.ID && !actor.Manage {
		return Article{}, ErrNotAuthor
	}
	if article.Status == ArticleArchived && !actor.Manage {
		return Article{}, ErrArticleNotEditable
	}
	revision, err := s.store.GetRevision(ctx, actor.TenantID, articleID, revisionID)
	if err != nil {
		return Article{}, mapStoreError(err, ErrRevisionNotFound)
	}
	expectedVersion := article.Version
	previousVersion, err := s.snapshot(ctx, actor, article, "restore | 恢复")
	if err != nil {
		return Article{}, err
	}
	bodyHTML, bodyText, err := s.renderer.Render(revision.BodyMarkdown)
	if err != nil {
		return Article{}, errx.Wrap(err, ErrInvalidInput)
	}
	tags, err := s.store.ArticleTags(ctx, actor.TenantID, articleID)
	if err != nil {
		return Article{}, errx.Wrap(err, ErrUnavailable)
	}
	categoryName := ""
	if article.CategoryID != "" {
		if category, categoryErr := s.store.GetCategory(ctx, actor.TenantID, article.CategoryID); categoryErr == nil {
			categoryName = category.Name
		}
	}
	article.Title = revision.Title
	article.BodyMarkdown = revision.BodyMarkdown
	article.BodyHTML = bodyHTML
	article.BodyText = bodyText
	article.SearchText = buildArticleSearch(article.Title, article.Summary, bodyText, categoryName, article.AuthorName, tags)
	article.Version = previousVersion + 1
	article.UpdatedAt = s.options.Now()
	if err := s.store.UpdateArticleContent(ctx, actor.TenantID, article, expectedVersion); err != nil {
		return Article{}, err
	}
	s.audit(ctx, actor, "article.restore_revision", "article", article.ID, "revision "+revision.ID)
	return article, nil
}
