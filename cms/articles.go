package cms

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/shibingli/yd/core/errx"
)

// Articles returns a normalised page of articles. Callers that serve the public site must set
// PublicOnly; the service then enforces every part of the public contract again in the store query. |
// Articles 返回规范化后的一页文章。服务公开站点的调用方必须设置 PublicOnly；
// 服务随后在存储查询中再次强制执行公开契约的每一项。
func (s *Service) Articles(ctx context.Context, tenantID string, filter ArticleFilter) (ArticlePage, error) {
	if strings.TrimSpace(tenantID) == "" {
		return ArticlePage{}, ErrInvalidInput
	}
	filter.Page, filter.PageSize = s.normalizePage(filter.Page, filter.PageSize)
	if filter.Now.IsZero() {
		filter.Now = s.options.Now()
	}
	if filter.PublicOnly {
		filter.Status = ArticlePublished
		filter.Visibility = VisibilityPublic
	}
	if err := validateSearch(filter.Keyword, s.options.SearchTerms); err != nil {
		return ArticlePage{}, err
	}
	page, err := s.store.ListArticles(ctx, tenantID, filter)
	if err != nil {
		return ArticlePage{}, errx.Wrap(err, ErrUnavailable)
	}
	page.Page = filter.Page
	page.PageSize = filter.PageSize
	page.TotalPages = totalPages(page.Total, filter.PageSize)
	for i := range page.Items {
		page.Items[i].Scheduled = scheduled(page.Items[i].Article, filter.Now)
	}
	return page, nil
}

// Article returns one article by id or slug. publicOnly applies the public contract and reports a
// non-public article as missing, so a draft's existence is never disclosed. | Article 按标识或别名返回文章。
// publicOnly 施加公开契约，并把非公开文章视为不存在，因此草稿的存在性永不泄露。
func (s *Service) Article(ctx context.Context, tenantID, ref string, publicOnly bool) (ArticleView, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ArticleView{}, ErrArticleNotFound
	}
	article, err := s.store.GetArticle(ctx, tenantID, ref)
	if isMissing(err) {
		article, err = s.store.GetArticleBySlug(ctx, tenantID, strings.ToLower(ref))
	}
	if err != nil {
		return ArticleView{}, mapStoreError(err, ErrArticleNotFound)
	}
	if publicOnly && !publicVisible(article, s.options.Now()) {
		return ArticleView{}, ErrArticleNotFound
	}
	view, err := s.decorate(ctx, tenantID, article)
	if err != nil {
		return ArticleView{}, err
	}
	view.Scheduled = scheduled(article, s.options.Now())
	return view, nil
}

// SaveArticle creates or updates an article. An update snapshots the previous body as a revision,
// keeps the editorial state, and refuses another author's article unless the actor manages all
// articles. | SaveArticle 创建或更新文章。更新会把旧正文快照为修订、保持编辑状态，
// 并拒绝他人文章，除非主体可管理全部文章。
func (s *Service) SaveArticle(ctx context.Context, actor Actor, input ArticleInput) (Article, error) {
	if !actor.valid() {
		return Article{}, ErrForbidden
	}
	title, err := articleTitle(input.Title, s.options.MaxTitleLen)
	if err != nil {
		return Article{}, err
	}
	if len(input.BodyMarkdown) > s.options.MaxBodyLen {
		return Article{}, errx.Detail(ErrInvalidInput, "body is too long | 正文过长")
	}
	if len(input.Summary) > s.options.MaxSummaryLen {
		return Article{}, errx.Detail(ErrInvalidInput, "summary is too long | 摘要过长")
	}
	category, err := s.categoryFor(ctx, actor.TenantID, input.CategoryID)
	if err != nil {
		return Article{}, err
	}
	now := s.options.Now()
	var current Article
	creating := strings.TrimSpace(input.ID) == ""
	if creating {
		id, idErr := s.mustID("art_")
		if idErr != nil {
			return Article{}, idErr
		}
		current = Article{ID: id, TenantID: actor.TenantID, AuthorID: actor.ID, AuthorName: actor.DisplayName,
			Status: ArticleDraft, Version: 1, CreatedAt: now, UpdatedAt: now}
	} else {
		current, err = s.store.GetArticle(ctx, actor.TenantID, strings.TrimSpace(input.ID))
		if err != nil {
			return Article{}, mapStoreError(err, ErrArticleNotFound)
		}
		if current.AuthorID != actor.ID && !actor.Manage {
			return Article{}, ErrNotAuthor
		}
		if current.Status == ArticleArchived && !actor.Manage {
			return Article{}, ErrArticleNotEditable
		}
	}
	kind := input.Kind
	if kind == "" {
		if creating {
			kind = KindPost
		} else {
			kind = current.Kind
		}
	}
	visibility := input.Visibility
	if visibility == "" {
		if creating {
			visibility = VisibilityPublic
		} else {
			visibility = current.Visibility
		}
	}
	switch kind {
	case KindPost, KindPage:
	default:
		return Article{}, errx.Detail(ErrInvalidInput, "unknown article kind %[1]q | 未知的文章类型 %[1]q", kind)
	}
	switch visibility {
	case VisibilityPublic, VisibilityPrivate:
	default:
		return Article{}, errx.Detail(ErrInvalidInput, "unknown visibility %[1]q | 未知的可见性 %[1]q", visibility)
	}
	slugValue := input.Slug
	if !creating && strings.TrimSpace(slugValue) == "" {
		slugValue = current.Slug
	}
	slug, err := s.normalizedSlug(slugValue, title, "article-"+strings.ReplaceAll(current.ID, "_", "-"))
	if err != nil {
		return Article{}, err
	}
	if slug != current.Slug {
		existing, lookupErr := s.store.GetArticleBySlug(ctx, actor.TenantID, slug)
		if lookupErr == nil && existing.ID != current.ID {
			return Article{}, ErrArticleSlugTaken
		}
		if lookupErr != nil && !isMissing(lookupErr) {
			return Article{}, errx.Wrap(lookupErr, ErrUnavailable)
		}
	}
	for _, field := range []struct {
		name  string
		value string
		max   int
	}{
		{"summary | 摘要", strings.TrimSpace(input.Summary), 4000},
		{"cover URL | 封面地址", strings.TrimSpace(input.CoverURL), 1024},
		{"SEO title | SEO 标题", strings.TrimSpace(input.SEOTitle), 300},
		{"SEO description | SEO 描述", strings.TrimSpace(input.SEODescription), 500},
		{"SEO keywords | SEO 关键字", strings.TrimSpace(input.SEOKeywords), 500},
		{"canonical URL | 规范地址", strings.TrimSpace(input.CanonicalURL), 1024},
		{"source | 来源", strings.TrimSpace(input.Source), 300},
		{"note | 修订说明", strings.TrimSpace(input.Note), 300},
	} {
		if err := checkOptionalText(field.name, field.value, field.max); err != nil {
			return Article{}, err
		}
	}
	tags, err := s.resolveTags(ctx, actor, input.TagIDs, input.TagNames)
	if err != nil {
		return Article{}, err
	}
	bodyHTML, bodyText, err := s.renderer.Render(input.BodyMarkdown)
	if err != nil {
		return Article{}, errx.Wrap(err, ErrInvalidInput)
	}
	categoryName := ""
	if category != nil {
		categoryName = category.Name
	}
	searchText := buildArticleSearch(title, input.Summary, bodyText, categoryName, current.AuthorName, tags)
	expectedVersion := current.Version
	previousBody, previousTitle := current.BodyMarkdown, current.Title
	if !creating && (previousBody != input.BodyMarkdown || previousTitle != title) {
		previousVersion, snapshotErr := s.snapshot(ctx, actor, current, input.Note)
		if snapshotErr != nil {
			return Article{}, snapshotErr
		}
		current.Version = previousVersion + 1
	} else if !creating {
		// 版本是整行的乐观锁，因此每次保存都推进它，而不只在大改时推进。 |
		// The version is the row's optimistic lock, so every save advances it, not only content revisions.
		current.Version = expectedVersion + 1
	}
	current.CategoryID = strings.TrimSpace(input.CategoryID)
	current.Title = title
	current.Slug = slug
	current.Summary = strings.TrimSpace(input.Summary)
	current.CoverURL = strings.TrimSpace(input.CoverURL)
	current.BodyMarkdown = input.BodyMarkdown
	current.BodyHTML = bodyHTML
	current.BodyText = bodyText
	current.SearchText = searchText
	current.Kind = kind
	current.Visibility = visibility
	current.Featured = input.Featured
	current.Pinned = input.Pinned
	current.AllowComment = input.AllowComment
	current.SEOTitle = strings.TrimSpace(input.SEOTitle)
	current.SEODescription = strings.TrimSpace(input.SEODescription)
	current.SEOKeywords = strings.TrimSpace(input.SEOKeywords)
	current.CanonicalURL = strings.TrimSpace(input.CanonicalURL)
	current.Source = strings.TrimSpace(input.Source)
	current.UpdatedAt = now
	var saveErr error
	if creating {
		saveErr = s.store.CreateArticleWithTags(ctx, actor.TenantID, current, tags)
	} else {
		saveErr = s.store.UpdateArticleContentWithTags(ctx, actor.TenantID, current, expectedVersion, tags)
	}
	if saveErr != nil {
		if errors.Is(saveErr, ErrArticleSlugTaken) {
			return Article{}, ErrArticleSlugTaken
		}
		// A concurrently deleted category or tag is a business conflict, not a store outage. |
		// 并发删除的分类或标签属于业务冲突，而不是存储故障。
		if errors.Is(saveErr, ErrCategoryNotFound) {
			return Article{}, ErrCategoryNotFound
		}
		if errors.Is(saveErr, ErrTagNotFound) {
			return Article{}, ErrTagNotFound
		}
		return Article{}, errx.Wrap(saveErr, ErrUnavailable)
	}
	action := "article.update"
	if creating {
		action = "article.create"
	}
	s.audit(ctx, actor, action, "article", current.ID, current.Title)
	return current, nil
}

// DeleteArticle soft-deletes an article and rewrites its slug so the old address can be reused. |
// DeleteArticle 软删除文章并改写别名，使旧地址可以复用。
func (s *Service) DeleteArticle(ctx context.Context, actor Actor, id string) error {
	if !actor.valid() {
		return ErrForbidden
	}
	article, err := s.store.GetArticle(ctx, actor.TenantID, id)
	if err != nil {
		return mapStoreError(err, ErrArticleNotFound)
	}
	if article.AuthorID != actor.ID && !actor.Manage {
		return ErrNotAuthor
	}
	now := s.options.Now()
	slug := article.Slug + "-deleted-" + article.ID
	if err := s.store.SoftDeleteArticle(ctx, actor.TenantID, article.ID, now, slug); err != nil {
		return mapStoreError(err, ErrArticleNotFound)
	}
	s.audit(ctx, actor, "article.delete", "article", article.ID, article.Title)
	return nil
}

// TransitionArticle applies one editorial transition. The permission for the transition is carried
// by the actor, while the article state machine is enforced here. | TransitionArticle 应用一次编辑生命周期转换。
// 转换所需权限由主体携带，文章状态机在这里强制执行。
func (s *Service) TransitionArticle(ctx context.Context, actor Actor, id string, action ArticleAction, at *time.Time) (Article, error) {
	if !actor.valid() {
		return Article{}, ErrForbidden
	}
	article, err := s.store.GetArticle(ctx, actor.TenantID, id)
	if err != nil {
		return Article{}, mapStoreError(err, ErrArticleNotFound)
	}
	author := article.AuthorID == actor.ID
	now := s.options.Now()
	updated := article
	switch action {
	case ActionSubmit:
		if !author && !actor.Manage {
			return Article{}, ErrForbidden
		}
		if article.Status != ArticleDraft {
			return Article{}, ErrArticleTransition
		}
		updated.Status = ArticlePending
	case ActionApprove:
		if !actor.Review && !actor.Manage {
			return Article{}, ErrForbidden
		}
		if article.Status != ArticlePending {
			return Article{}, ErrArticleTransition
		}
		updated.Status = ArticlePublished
		updated.PublishAt = nil
	case ActionReject:
		if !actor.Review && !actor.Manage {
			return Article{}, ErrForbidden
		}
		if article.Status != ArticlePending {
			return Article{}, ErrArticleTransition
		}
		updated.Status = ArticleDraft
	case ActionWithdraw:
		// 作者可以撤回自己的待审文章，管理员也可以代为撤回；其它人仍被拒绝。
		// The author may withdraw their own pending article and an administrator may withdraw it for them; everyone else
		// is still refused.
		if !author && !actor.Manage {
			return Article{}, ErrForbidden
		}
		if article.Status != ArticlePending {
			return Article{}, ErrArticleTransition
		}
		updated.Status = ArticleDraft
	case ActionPublish:
		if !actor.Publish && !actor.Manage {
			return Article{}, ErrForbidden
		}
		if article.Status != ArticleDraft {
			return Article{}, ErrArticleTransition
		}
		updated.Status = ArticlePublished
		if at != nil && at.After(now) {
			future := at.UTC()
			updated.PublishAt = &future
		} else {
			updated.PublishAt = nil
		}
	case ActionUnpublish:
		if !actor.Publish && !actor.Manage {
			return Article{}, ErrForbidden
		}
		if article.Status != ArticlePublished {
			return Article{}, ErrArticleTransition
		}
		updated.Status = ArticleDraft
		updated.PublishAt = nil
	case ActionArchive:
		// An author may retire their own article, including a published one, and an administrator may retire any article;
		// archiving publishes nothing and therefore needs no publish capability. | 作者可以退役自己的文章（包括已发布文章），
		// 管理员可退役任意文章；退役不会发布任何内容，因此不需要发布能力。
		if !author && !actor.Publish && !actor.Manage {
			return Article{}, ErrForbidden
		}
		if article.Status == ArticleArchived {
			return Article{}, ErrArticleTransition
		}
		updated.Status = ArticleArchived
		updated.PublishAt = nil
	case ActionRestore:
		// 恢复只是把文章退回草稿，作者与管理员都可以执行；重新发布仍要经过审核或发布能力。
		// Restore only returns the article to draft, so the author and an administrator may both perform it; publishing
		// again still requires review or the publish capability.
		if !author && !actor.Publish && !actor.Manage {
			return Article{}, ErrForbidden
		}
		if article.Status != ArticleArchived {
			return Article{}, ErrArticleTransition
		}
		updated.Status = ArticleDraft
		updated.PublishAt = nil
	default:
		return Article{}, ErrArticleTransition
	}
	updated.UpdatedAt = now
	if err := s.store.UpdateArticleStatus(ctx, actor.TenantID, article.ID, article.Status, updated.Status, updated.PublishAt, updated.UpdatedAt); err != nil {
		return Article{}, err
	}
	s.audit(ctx, actor, "article."+string(action), "article", article.ID, article.Title)
	return updated, nil
}

// IncrementView counts one public read. Non-public and missing articles are ignored so the counter
// cannot be used to probe drafts. | IncrementView 统计一次公开阅读。非公开与不存在的文章都被忽略，
// 因此计数器不能用来探测草稿。
func (s *Service) IncrementView(ctx context.Context, tenantID, id string) error {
	article, err := s.store.GetArticle(ctx, tenantID, id)
	if err != nil {
		return nil
	}
	if !publicVisible(article, s.options.Now()) {
		return nil
	}
	if err := s.store.IncrementArticleViews(ctx, tenantID, id); err != nil {
		return errx.Wrap(err, ErrUnavailable)
	}
	return nil
}

// RelatedArticles returns public articles sharing a category with the given article. |
// RelatedArticles 返回与给定文章共享分类的公开文章。
func (s *Service) RelatedArticles(ctx context.Context, tenantID, articleID string, limit int) ([]ArticleView, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	article, err := s.store.GetArticle(ctx, tenantID, articleID)
	if err != nil {
		return nil, mapStoreError(err, ErrArticleNotFound)
	}
	categories, err := s.store.ListCategories(ctx, tenantID)
	if err != nil {
		return nil, errx.Wrap(err, ErrUnavailable)
	}
	ids := make([]string, 0, 4)
	if article.CategoryID != "" {
		ids = SubtreeIDs(categories, article.CategoryID)
	}
	if len(ids) == 0 {
		// 没有分类就没有「相关」的语义；退回全租户会把任意文章都当成相关文章。 |
		// Without a category there is no notion of relatedness; falling back to the tenant would call any article related.
		return []ArticleView{}, nil
	}
	views, err := s.store.RelatedArticles(ctx, tenantID, articleID, ids, s.options.Now(), limit)
	if err != nil {
		return nil, errx.Wrap(err, ErrUnavailable)
	}
	return views, nil
}

// CategorySubtreeIDs resolves a category and every descendant for a public category page. |
// CategorySubtreeIDs 为公开分类页解析一个分类及其全部后代。
func (s *Service) CategorySubtreeIDs(ctx context.Context, tenantID, categoryID string) ([]string, error) {
	categories, err := s.store.ListCategories(ctx, tenantID)
	if err != nil {
		return nil, errx.Wrap(err, ErrUnavailable)
	}
	ids := SubtreeIDs(categories, categoryID)
	if len(ids) == 0 {
		return nil, ErrCategoryNotFound
	}
	return ids, nil
}

// categoryFor validates a category reference, returning nil for an empty reference. |
// categoryFor 校验分类引用，空引用返回 nil。
func (s *Service) categoryFor(ctx context.Context, tenantID, categoryID string) (*Category, error) {
	categoryID = strings.TrimSpace(categoryID)
	if categoryID == "" {
		return nil, nil
	}
	category, err := s.store.GetCategory(ctx, tenantID, categoryID)
	if err != nil {
		return nil, mapStoreError(err, ErrCategoryNotFound)
	}
	return &category, nil
}

// decorate fills the category and tag projections of one article. | decorate 填充一篇文章的分类与标签投影。
func (s *Service) decorate(ctx context.Context, tenantID string, article Article) (ArticleView, error) {
	view := ArticleView{Article: article}
	if article.CategoryID != "" {
		if category, err := s.store.GetCategory(ctx, tenantID, article.CategoryID); err == nil {
			view.CategoryName = category.Name
			view.CategorySlug = category.Slug
			view.CategoryPath = category.Path
		}
	}
	tags, err := s.store.ArticleTags(ctx, tenantID, article.ID)
	if err != nil {
		return ArticleView{}, errx.Wrap(err, ErrUnavailable)
	}
	view.Tags = tags
	return view, nil
}

// snapshot stores the previous body as a revision and prunes the history. | snapshot 把旧正文保存为修订并裁剪历史。
func (s *Service) snapshot(ctx context.Context, actor Actor, article Article, note string) (int, error) {
	id, err := s.mustID("rev_")
	if err != nil {
		return 0, err
	}
	// 版本取「文章当前版本」与「已有修订最大版本 + 1」的较大者：上一次保存如果在写修订之后失败，
	// 这里仍然会跳过那个孤儿版本，文章不会永久卡在唯一索引冲突上。 |
	// The version is the larger of the article's current version and the highest existing revision plus one:
	// if a previous save failed after inserting its revision, this skips the orphan instead of failing forever
	// on the unique revision index.
	version := article.Version
	if revisions, listErr := s.store.ListRevisions(ctx, actor.TenantID, article.ID); listErr != nil {
		return 0, errx.Wrap(listErr, ErrUnavailable)
	} else {
		for _, revision := range revisions {
			if revision.Version >= version {
				version = revision.Version + 1
			}
		}
	}
	revision := Revision{
		ID:           id,
		TenantID:     actor.TenantID,
		ArticleID:    article.ID,
		EditorID:     actor.ID,
		EditorName:   actor.DisplayName,
		Title:        article.Title,
		BodyMarkdown: article.BodyMarkdown,
		BodyHTML:     article.BodyHTML,
		BodyText:     article.BodyText,
		Note:         strings.TrimSpace(note),
		Version:      version,
		CreatedAt:    s.options.Now(),
	}
	if err := s.store.CreateRevision(ctx, actor.TenantID, revision); err != nil {
		if errors.Is(err, ErrArticleConflict) {
			return 0, ErrArticleConflict
		}
		return 0, errx.Wrap(err, ErrUnavailable)
	}
	// 保留策略是维护动作：修剪失败不能把已经写入的修订变成一次失败的保存。 |
	// Retention is maintenance: a failed prune must not turn an inserted revision into a failed save.
	_ = s.store.PruneRevisions(ctx, actor.TenantID, article.ID, s.options.MaxRevisions)
	return version, nil
}

// articleTitle validates and trims an article title. | articleTitle 校验并裁剪文章标题。
func articleTitle(value string, max int) (string, error) {
	title := strings.TrimSpace(value)
	if err := checkText("title | 标题", title, max); err != nil {
		return "", err
	}
	return title, nil
}

// buildArticleSearch builds the lower-cased search projection. | buildArticleSearch 构建小写检索投影。
func buildArticleSearch(title, summary, body, category, author string, tags []Tag) string {
	parts := []string{title, summary, body, category, author}
	for _, tag := range tags {
		parts = append(parts, tag.Name)
	}
	return strings.ToLower(strings.Join(strings.Fields(strings.Join(parts, " ")), " "))
}

// validateSearch rejects empty searches that would scan the whole table. | validateSearch 拒绝会导致全表扫描的空检索。
func validateSearch(keyword string, maxTerms int) error {
	if len(keyword) > 200 {
		return errx.Detail(ErrSearchTooBroad, "keyword is longer than 200 bytes | 关键字超过 200 字节")
	}
	terms := strings.Fields(keyword)
	if len(terms) > maxTerms {
		return ErrSearchTooBroad
	}
	return nil
}
