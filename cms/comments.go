package cms

import (
	"context"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// PostComment validates and stores one comment. Commenting requires a publicly visible article and
// the site-wide switch; moderation settings decide the initial state. | PostComment 校验并保存一条评论。
// 评论要求文章公开可见且站点总开关开启；审核设置决定初始状态。
func (s *Service) PostComment(ctx context.Context, actor Actor, input CommentInput) (Comment, error) {
	if !actor.valid() {
		return Comment{}, ErrForbidden
	}
	article, err := s.store.GetArticle(ctx, actor.TenantID, strings.TrimSpace(input.ArticleID))
	if err != nil || !publicVisible(article, s.options.Now()) {
		return Comment{}, ErrArticleNotFound
	}
	// Standalone pages have no comment surface; accepting a comment for one would store a row that
	// no public route can ever render. | 单页没有评论面；接受单页评论只会存下一条公开路由永远无法渲染的行。
	if article.Kind != KindPost {
		return Comment{}, ErrCommentsClosed
	}
	settings, err := s.settings(ctx, actor.TenantID)
	if err != nil {
		return Comment{}, err
	}
	if !settings.CommentsEnabled || !article.AllowComment {
		return Comment{}, ErrCommentsClosed
	}
	body := strings.TrimSpace(input.Body)
	if err := checkText("comment | 评论", body, s.options.MaxCommentLen); err != nil {
		return Comment{}, err
	}
	parentID := strings.TrimSpace(input.ParentID)
	depth := 0
	if parentID != "" {
		parent, parentErr := s.store.GetComment(ctx, actor.TenantID, parentID)
		if parentErr != nil {
			return Comment{}, mapStoreError(parentErr, ErrCommentParent)
		}
		if parent.ArticleID != article.ID || parent.Depth >= 1 || parent.Status != CommentApproved {
			return Comment{}, ErrCommentParent
		}
		depth = parent.Depth + 1
	}
	bodyHTML, bodyText, err := s.renderer.Render(body)
	if err != nil {
		return Comment{}, errx.Wrap(err, ErrInvalidInput)
	}
	id, err := s.mustID("cmt_")
	if err != nil {
		return Comment{}, err
	}
	status := CommentApproved
	if settings.CommentModeration {
		status = CommentPending
	}
	now := s.options.Now()
	comment := Comment{
		ID:           id,
		TenantID:     actor.TenantID,
		ArticleID:    article.ID,
		ParentID:     parentID,
		AuthorID:     actor.ID,
		AuthorName:   actor.DisplayName,
		BodyMarkdown: body,
		BodyHTML:     bodyHTML,
		BodyText:     bodyText,
		Status:       status,
		Depth:        depth,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.CreateComment(ctx, actor.TenantID, comment); err != nil {
		return Comment{}, errx.Wrap(err, ErrUnavailable)
	}
	return comment, nil
}

// Comments returns a normalised page of comments. | Comments 返回规范化后的一页评论。
func (s *Service) Comments(ctx context.Context, tenantID string, filter CommentFilter) (CommentPage, error) {
	filter.Page, filter.PageSize = s.normalizePage(filter.Page, filter.PageSize)
	page, err := s.store.ListComments(ctx, tenantID, filter)
	if err != nil {
		return CommentPage{}, errx.Wrap(err, ErrUnavailable)
	}
	page.Page = filter.Page
	page.PageSize = filter.PageSize
	page.TotalPages = totalPages(page.Total, filter.PageSize)
	return page, nil
}

// ModerateComment changes a comment's moderation state. Only approved, rejected and spam are
// accepted; deletion happens through DeleteComment. | ModerateComment 改变评论的审核状态。
// 只接受 approved、rejected 与 spam；删除走 DeleteComment。
func (s *Service) ModerateComment(ctx context.Context, actor Actor, id string, status CommentStatus) (Comment, error) {
	if !actor.valid() || !actor.Moderate {
		return Comment{}, ErrForbidden
	}
	switch status {
	case CommentApproved, CommentRejected, CommentSpam:
	default:
		return Comment{}, ErrInvalidInput
	}
	comment, err := s.store.GetComment(ctx, actor.TenantID, id)
	if err != nil {
		return Comment{}, mapStoreError(err, ErrCommentNotFound)
	}
	if comment.Status == CommentDeleted {
		// 墓碑的正文已清空；把墓碑「通过」回去只会发布一条空评论。 |
		// A tombstone's body has been cleared; approving it back would publish an empty comment.
		return Comment{}, ErrCommentNotFound
	}
	comment.Status = status
	comment.UpdatedAt = s.options.Now()
	if err := s.store.UpdateComment(ctx, actor.TenantID, comment); err != nil {
		return Comment{}, mapStoreError(err, ErrCommentNotFound)
	}
	s.audit(ctx, actor, "comment.moderate", "comment", comment.ID, string(status))
	return comment, nil
}

// DeleteComment soft-deletes a comment. The author, the article author and a moderator may remove
// it; the body is cleared and the row stays as a tombstone for replies. | DeleteComment 软删除评论。
// 评论作者、文章作者与审核者均可删除；正文被清空，行作为回复占位保留。
func (s *Service) DeleteComment(ctx context.Context, actor Actor, id string) error {
	if !actor.valid() {
		return ErrForbidden
	}
	comment, err := s.store.GetComment(ctx, actor.TenantID, id)
	if err != nil {
		return mapStoreError(err, ErrCommentNotFound)
	}
	allowed := actor.Moderate || comment.AuthorID == actor.ID
	if !allowed {
		article, articleErr := s.store.GetArticle(ctx, actor.TenantID, comment.ArticleID)
		allowed = articleErr == nil && article.AuthorID == actor.ID
	}
	if !allowed {
		return ErrNotCommentAuthor
	}
	comment.Status = CommentDeleted
	comment.BodyMarkdown = ""
	comment.BodyHTML = ""
	comment.BodyText = ""
	comment.UpdatedAt = s.options.Now()
	if err := s.store.UpdateComment(ctx, actor.TenantID, comment); err != nil {
		return mapStoreError(err, ErrCommentNotFound)
	}
	s.audit(ctx, actor, "comment.delete", "comment", comment.ID, comment.ArticleID)
	return nil
}

// CommentCount returns the number of comments in a state for one article. | CommentCount 返回一篇文章某状态的评论数。
func (s *Service) CommentCount(ctx context.Context, tenantID, articleID string, status CommentStatus) (int64, error) {
	count, err := s.store.CountComments(ctx, tenantID, articleID, status)
	if err != nil {
		return 0, errx.Wrap(err, ErrUnavailable)
	}
	return count, nil
}
