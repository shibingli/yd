// Package cms is the tenant-scoped content management and publishing domain: an unlimited-depth
// category tree, articles and standalone pages, tags, revisions, a media library, moderated comments
// and site settings. | Package cms 是租户级内容管理与发布领域：无限级分类树、文章与独立单页、
// 标签、修订、媒体库、受审核的评论以及站点设置。
//
// The package owns the rules and depends on no ORM and no HTTP layer: persistence arrives through
// Store, Markdown rendering through Renderer and media bytes through BlobStore. The GORM
// implementation lives in cms/gormstore, exactly like messaging and security/auth. |
// 本包只拥有规则，不依赖 ORM 与 HTTP 层：持久化经 Store、Markdown 渲染经 Renderer、媒体字节经
// BlobStore 注入。GORM 实现位于 cms/gormstore，与 messaging 及 security/auth 的分层完全一致。
package cms

import "github.com/shibingli/yd/core/errx"

// Sentinel errors of the CMS. Callers match them with errors.Is and never parse copy. |
// CMS 的哨兵错误。调用方一律用 errors.Is 判定，绝不解析文案。
var (
	// ErrUnavailable reports a CMS without a usable store. | ErrUnavailable 表示 CMS 没有可用的存储。
	ErrUnavailable = errx.New("CMS requires a database | CMS 需要数据库")
	// ErrNotFound reports a missing resource or one outside the actor's tenant. | ErrNotFound 表示资源不存在或不在当前租户内。
	ErrNotFound = errx.New("CMS resource not found | CMS 资源不存在")
	// ErrForbidden reports an operation the actor may not perform. | ErrForbidden 表示当前主体无权执行该操作。
	ErrForbidden = errx.New("CMS operation forbidden | CMS 操作被拒绝")
	// ErrInvalidInput reports empty or malformed input. | ErrInvalidInput 表示输入为空或格式非法。
	ErrInvalidInput = errx.New("invalid CMS input | 非法的 CMS 输入")

	// ErrCategoryNotFound reports a missing category inside the tenant. | ErrCategoryNotFound 表示租户内不存在该分类。
	ErrCategoryNotFound = errx.New("category not found | 分类不存在")
	// ErrCategorySlugTaken reports a duplicate category slug inside the tenant. | ErrCategorySlugTaken 表示租户内分类别名重复。
	ErrCategorySlugTaken = errx.New("category slug is already in use | 分类别名已被占用")
	// ErrCategoryConflict reports a concurrent change to the category tree. | ErrCategoryConflict 表示分类树被并发修改。
	ErrCategoryConflict = errx.New("the category tree changed concurrently | 分类树已被并发修改")
	// ErrCategoryCycle reports moving a category below itself or one of its descendants. |
	// ErrCategoryCycle 表示把分类移动到自身或其后代之下。
	ErrCategoryCycle = errx.New("a category cannot become its own descendant | 分类不能成为自己的后代")
	// ErrCategoryNotEmpty reports deleting a category that still has children or articles. |
	// ErrCategoryNotEmpty 表示删除仍有子分类或文章的分类。
	ErrCategoryNotEmpty = errx.New("category is not empty | 分类非空")
	// ErrCategoryParent reports a missing or invalid parent category. | ErrCategoryParent 表示父分类不存在或非法。
	ErrCategoryParent = errx.New("invalid parent category | 非法的父分类")

	// ErrArticleNotFound reports a missing article inside the tenant. | ErrArticleNotFound 表示租户内不存在该文章。
	ErrArticleNotFound = errx.New("article not found | 文章不存在")
	// ErrArticleSlugTaken reports a duplicate article slug inside the tenant. | ErrArticleSlugTaken 表示租户内文章别名重复。
	ErrArticleSlugTaken = errx.New("article slug is already in use | 文章别名已被占用")
	// ErrArticleConflict reports a concurrent change to the same article. | ErrArticleConflict 表示同一篇文章被并发修改。
	ErrArticleConflict = errx.New("the article changed concurrently | 文章已被并发修改")
	// ErrArticleNotEditable reports a mutation the article state does not allow. | ErrArticleNotEditable 表示文章状态不允许该变更。
	ErrArticleNotEditable = errx.New("the article cannot be changed in its current state | 文章当前状态不允许变更")
	// ErrArticleTransition reports an illegal lifecycle transition. | ErrArticleTransition 表示非法的生命周期转换。
	ErrArticleTransition = errx.New("invalid article status transition | 非法的文章状态转换")
	// ErrArticleNotPublic reports a public read that the article does not satisfy. | ErrArticleNotPublic 表示文章不满足公开读取条件。
	ErrArticleNotPublic = errx.New("the article is not published for public reading | 文章尚未公开发布")
	// ErrNotAuthor reports an edit or delete aimed at another author's article. | ErrNotAuthor 表示变更他人文章。
	ErrNotAuthor = errx.New("only the author may change this article | 只有作者可以变更该文章")
	// ErrRevisionNotFound reports a missing revision. | ErrRevisionNotFound 表示修订不存在。
	ErrRevisionNotFound = errx.New("revision not found | 修订不存在")

	// ErrTagNotFound reports a missing tag inside the tenant. | ErrTagNotFound 表示租户内不存在该标签。
	ErrTagNotFound = errx.New("tag not found | 标签不存在")
	// ErrTagSlugTaken reports a duplicate tag slug inside the tenant. | ErrTagSlugTaken 表示租户内标签别名重复。
	ErrTagSlugTaken = errx.New("tag slug is already in use | 标签别名已被占用")
	// ErrTagInUse reports deleting a tag that still marks articles. | ErrTagInUse 表示删除仍在标记文章的标签。
	ErrTagInUse = errx.New("the tag is still in use | 标签仍在使用中")
	// ErrTooManyTags reports more tags than one article may carry. | ErrTooManyTags 表示标签数超过单篇文章上限。
	ErrTooManyTags = errx.New("too many tags | 标签数量超过上限")

	// ErrMediaNotFound reports a missing media record or blob. | ErrMediaNotFound 表示媒体记录或文件不存在。
	ErrMediaNotFound = errx.New("media not found | 媒体不存在")
	// ErrMediaTooLarge reports an upload over the configured ceiling. | ErrMediaTooLarge 表示上传超过配置上限。
	ErrMediaTooLarge = errx.New("media file is too large | 媒体文件超过大小上限")
	// ErrMediaType reports a content type outside the allow list. | ErrMediaType 表示内容类型不在白名单内。
	ErrMediaType = errx.New("media type is not allowed | 不允许的媒体类型")

	// ErrCommentNotFound reports a missing comment inside the tenant. | ErrCommentNotFound 表示租户内不存在该评论。
	ErrCommentNotFound = errx.New("comment not found | 评论不存在")
	// ErrCommentsClosed reports a comment on an article that does not accept them. | ErrCommentsClosed 表示文章不接受评论。
	ErrCommentsClosed = errx.New("comments are closed for this article | 该文章已关闭评论")
	// ErrCommentParent reports a reply whose parent is missing or too deep. | ErrCommentParent 表示回复的父评论不存在或层级过深。
	ErrCommentParent = errx.New("invalid comment parent | 非法的评论父级")
	// ErrNotCommentAuthor reports deleting another author's comment without moderation rights. |
	// ErrNotCommentAuthor 表示在没有审核权时删除他人评论。
	ErrNotCommentAuthor = errx.New("only the comment author may delete this comment | 只有评论作者可以删除该评论")

	// ErrSearchTooBroad reports an empty or over-long search expression. | ErrSearchTooBroad 表示检索表达式为空或过长。
	ErrSearchTooBroad = errx.New("search expression is invalid | 检索表达式非法")
)
