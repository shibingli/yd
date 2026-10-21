package cms

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
)

// Actor is the authenticated principal a service call acts as. The capability booleans are filled
// by the HTTP layer from the authz registry, so the domain never imports authz and a caller cannot
// widen its own rights by spelling a permission name. Ownership and lifecycle rules are still
// enforced here. | Actor 是服务调用所代表的主体。能力布尔值由 HTTP 层从 authz 注册表填充，
// 因此领域层不依赖 authz，调用方也无法通过拼写权限名扩大自己的权利。所有权与生命周期规则仍在这里强制执行。
type Actor struct {
	// ID is the global user id. | ID 是全局用户 ID。
	ID string
	// TenantID is the active tenant. | TenantID 是当前活动租户。
	TenantID string
	// DisplayName is the display-name snapshot written into rows. | DisplayName 是写入记录的显示名快照。
	DisplayName string
	// Manage allows changing every article in the tenant. | Manage 允许变更租户内的全部文章。
	Manage bool
	// Publish allows publishing, scheduling, unpublishing, archiving and restoring. |
	// Publish 允许发布、定时、下线、归档与恢复。
	Publish bool
	// Review allows approving or rejecting pending articles. | Review 允许通过或驳回待审文章。
	Review bool
	// Moderate allows moderating every comment. | Moderate 允许审核全部评论。
	Moderate bool
	// MediaManage allows deleting media uploaded by others. | MediaManage 允许删除他人上传的媒体。
	MediaManage bool
	// SiteManage allows changing the tenant site settings. | SiteManage 允许修改租户站点设置。
	SiteManage bool
}

// valid reports whether the actor names a user inside a tenant. | valid 报告主体是否同时给出用户与租户。
func (a Actor) valid() bool {
	return strings.TrimSpace(a.ID) != "" && strings.TrimSpace(a.TenantID) != ""
}

// Options configures limits, clocks and identifier generation. | Options 配置限制、时钟与标识生成。
type Options struct {
	// Now is the clock; nil means time.Now().UTC(). | Now 是时钟；nil 表示 time.Now().UTC()。
	Now func() time.Time
	// NewID generates an identifier; nil installs the crypto/rand generator. | NewID 生成标识符；nil 安装 crypto/rand 生成器。
	NewID func(prefix string) (string, error)
	// DefaultCategoryName is created for a tenant without categories. | DefaultCategoryName 为没有分类的租户创建。
	DefaultCategoryName string
	// DefaultCategorySlug is the slug of that category. | DefaultCategorySlug 是该分类的别名。
	DefaultCategorySlug string
	// AllowedMediaTypes lists accepted MIME types; nil installs the default allow list. |
	// AllowedMediaTypes 列出接受的 MIME 类型；nil 安装默认白名单。
	AllowedMediaTypes []string
	// MaxTitleLen is the title byte ceiling. | MaxTitleLen 是标题字节上限。
	MaxTitleLen int
	// MaxBodyLen is the Markdown body byte ceiling. | MaxBodyLen 是 Markdown 正文字节上限。
	MaxBodyLen int
	// MaxSummaryLen is the summary byte ceiling. | MaxSummaryLen 是摘要字节上限。
	MaxSummaryLen int
	// MaxCommentLen is the comment body byte ceiling. | MaxCommentLen 是评论正文字节上限。
	MaxCommentLen int
	// MaxTags is the number of tags one article may carry. | MaxTags 是单篇文章可携带的标签数。
	MaxTags int
	// MaxRevisions is the number of revisions kept per article. | MaxRevisions 是每篇文章保留的修订数。
	MaxRevisions int
	// MaxMediaSize is the per-file byte ceiling. | MaxMediaSize 是单文件字节上限。
	MaxMediaSize int64
	// PageSize is the default page length. | PageSize 是默认页长。
	PageSize int
	// MaxPageSize is the largest accepted page length. | MaxPageSize 是允许的最大页长。
	MaxPageSize int
	// SearchTerms is the maximum number of whitespace-separated search terms. | SearchTerms 是空白分隔检索词的最大数量。
	SearchTerms int
}

// DefaultOptions returns production-safe defaults. | DefaultOptions 返回生产可用的默认值。
func DefaultOptions() Options {
	return Options{
		DefaultCategoryName: "Uncategorized | 未分类",
		DefaultCategorySlug: "uncategorized",
		MaxTitleLen:         300,
		MaxBodyLen:          512 << 10,
		MaxSummaryLen:       2000,
		MaxCommentLen:       8 << 10,
		MaxTags:             20,
		MaxRevisions:        20,
		MaxMediaSize:        32 << 20,
		PageSize:            20,
		MaxPageSize:         100,
		SearchTerms:         6,
	}
}

// normalized fills every zero value with its default. | normalized 用默认值填充每个零值。
func (o Options) normalized() Options {
	d := DefaultOptions()
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.NewID == nil {
		o.NewID = newID
	}
	if len(o.AllowedMediaTypes) == 0 {
		o.AllowedMediaTypes = defaultMediaTypes()
	}
	if strings.TrimSpace(o.DefaultCategoryName) == "" {
		o.DefaultCategoryName = d.DefaultCategoryName
	}
	if strings.TrimSpace(o.DefaultCategorySlug) == "" {
		o.DefaultCategorySlug = d.DefaultCategorySlug
	}
	if o.MaxTitleLen <= 0 {
		o.MaxTitleLen = d.MaxTitleLen
	}
	if o.MaxBodyLen <= 0 {
		o.MaxBodyLen = d.MaxBodyLen
	}
	if o.MaxSummaryLen <= 0 {
		o.MaxSummaryLen = d.MaxSummaryLen
	}
	if o.MaxCommentLen <= 0 {
		o.MaxCommentLen = d.MaxCommentLen
	}
	if o.MaxTags <= 0 {
		o.MaxTags = d.MaxTags
	}
	if o.MaxRevisions <= 0 {
		o.MaxRevisions = d.MaxRevisions
	}
	if o.MaxMediaSize <= 0 {
		o.MaxMediaSize = d.MaxMediaSize
	}
	if o.PageSize <= 0 {
		o.PageSize = d.PageSize
	}
	if o.MaxPageSize <= 0 || o.MaxPageSize < o.PageSize {
		o.MaxPageSize = d.MaxPageSize
	}
	if o.SearchTerms <= 0 {
		o.SearchTerms = d.SearchTerms
	}
	return o
}

// Service owns every CMS rule. | Service 拥有 CMS 的全部规则。
type Service struct {
	// store is the persistence boundary. | store 是持久化边界。
	store Store
	// renderer turns Markdown into sanitised HTML. | renderer 把 Markdown 转成净化 HTML。
	renderer Renderer
	// blobs stores media bytes; nil disables media uploads and reads. | blobs 保存媒体字节；nil 表示禁用媒体上传与读取。
	blobs BlobStore
	// log records delivery and cleanup problems. | log 记录投递与清理问题。
	log *logx.Logger
	// options holds the normalized limits. | options 保存规范化后的限制。
	options Options
}

// New constructs a service. Store and Renderer are mandatory; BlobStore is optional and a missing
// one degrades exactly the media features that need it. | New 构造服务。Store 与 Renderer 必填；
// BlobStore 可选，缺失时只降级依赖它的媒体能力。
func New(store Store, renderer Renderer, blobs BlobStore, log *logx.Logger, options Options) (*Service, error) {
	if store == nil || renderer == nil {
		return nil, errx.Detail(ErrUnavailable, "store and renderer are required | store 与 renderer 为必填")
	}
	if log == nil {
		log = logx.Default()
	}
	return &Service{
		store:    store,
		renderer: renderer,
		blobs:    blobs,
		log:      log,
		options:  options.normalized(),
	}, nil
}

// Options returns the normalized options. | Options 返回规范化后的选项。
func (s *Service) Options() Options { return s.options }

// EnsureTenant creates the default category and settings row for a tenant. It is idempotent and
// safe to call on every CMS entry point, including concurrent starts. | EnsureTenant 为租户创建默认分类与设置行。
// 它幂等，可在每个 CMS 入口调用，包括并发启动。
func (s *Service) EnsureTenant(ctx context.Context, tenantID string) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrInvalidInput
	}
	categories, err := s.store.ListCategories(ctx, tenantID)
	if err != nil {
		return errx.Wrap(err, ErrUnavailable)
	}
	if len(categories) == 0 {
		now := s.options.Now()
		id, idErr := s.options.NewID("cat_")
		if idErr != nil {
			return errx.Wrap(idErr, ErrUnavailable)
		}
		category := Category{
			ID:          id,
			TenantID:    tenantID,
			Name:        s.options.DefaultCategoryName,
			Slug:        s.options.DefaultCategorySlug,
			Description: "",
			Path:        "/" + id + "/",
			Depth:       0,
			Visible:     true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if createErr := s.store.CreateCategory(ctx, tenantID, category); createErr != nil {
			// A concurrent start may have created it first; the second listing decides. |
			// 并发启动可能已经创建；以第二次列举为准。
			again, listErr := s.store.ListCategories(ctx, tenantID)
			if listErr == nil && len(again) > 0 {
				createErr = nil
			}
			if createErr != nil {
				return errx.Wrap(createErr, ErrUnavailable)
			}
		}
	}
	if _, err := s.store.GetSettings(ctx, tenantID); err != nil {
		if !errors.Is(err, ErrNotFound) {
			return errx.Wrap(err, ErrUnavailable)
		}
		now := s.options.Now()
		settings := Settings{
			TenantID:          tenantID,
			SiteName:          "Site | 站点",
			PostsPerPage:      10,
			CommentsEnabled:   true,
			CommentModeration: true,
			RSSEnabled:        true,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if saveErr := s.store.SaveSettings(ctx, tenantID, settings); saveErr != nil {
			if _, getErr := s.store.GetSettings(ctx, tenantID); getErr != nil {
				return errx.Wrap(saveErr, ErrUnavailable)
			}
		}
	}
	return nil
}

// Dashboard returns the aggregate snapshot with recent articles and comments. | Dashboard 返回聚合快照及最近文章与评论。
func (s *Service) Dashboard(ctx context.Context, tenantID string) (Counts, error) {
	counts, err := s.store.Counts(ctx, tenantID)
	if err != nil {
		return Counts{}, errx.Wrap(err, ErrUnavailable)
	}
	articles, err := s.store.ListArticles(ctx, tenantID, ArticleFilter{Page: 1, PageSize: 5, Now: s.options.Now()})
	if err != nil {
		return Counts{}, errx.Wrap(err, ErrUnavailable)
	}
	comments, err := s.store.ListComments(ctx, tenantID, CommentFilter{Page: 1, PageSize: 5})
	if err != nil {
		return Counts{}, errx.Wrap(err, ErrUnavailable)
	}
	counts.Recent = articles.Items
	counts.RecentComments = comments.Items
	return counts, nil
}

// newID generates a random identifier with prefix. | newID 生成带前缀的随机标识。
func newID(prefix string) (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errx.Wrap(err, ErrUnavailable)
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

// mustID generates an identifier or returns the wrapped error. | mustID 生成标识符或返回包装后的错误。
func (s *Service) mustID(prefix string) (string, error) {
	id, err := s.options.NewID(prefix)
	if err != nil {
		return "", errx.Wrap(err, ErrUnavailable)
	}
	return id, nil
}

// maxPageNumber keeps (page-1)*pageSize inside int on every supported platform while still allowing
// an empty page far beyond any real result set. | maxPageNumber 保证 (page-1)*pageSize 在所有支持平台上
// 不溢出 int，同时仍允许远超真实结果集的空页。
const maxPageNumber = 1_000_000

// normalizePage clamps a page request to the configured bounds. | normalizePage 把分页请求限制在配置范围内。
func (s *Service) normalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if page > maxPageNumber {
		page = maxPageNumber
	}
	if pageSize <= 0 {
		pageSize = s.options.PageSize
	}
	if pageSize > s.options.MaxPageSize {
		pageSize = s.options.MaxPageSize
	}
	return page, pageSize
}

// totalPages returns the number of pages for a row count. | totalPages 返回行数对应的页数。
func totalPages(total int64, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	pages := int(total / int64(pageSize))
	if total%int64(pageSize) != 0 {
		pages++
	}
	return pages
}

// publicVisible reports whether an article satisfies the public contract at now. | publicVisible 报告文章在 now 时是否满足公开契约。
func publicVisible(article Article, now time.Time) bool {
	if article.DeletedAt != nil || article.Status != ArticlePublished || article.Visibility != VisibilityPublic {
		return false
	}
	return article.PublishAt == nil || !article.PublishAt.After(now)
}

// scheduled reports whether a published article waits for its PublishAt. | scheduled 报告已发布文章是否在等待 PublishAt。
func scheduled(article Article, now time.Time) bool {
	return article.Status == ArticlePublished && article.PublishAt != nil && article.PublishAt.After(now)
}

// slugify derives a URL segment from a title. Non-ASCII text yields an empty result and the caller
// falls back to an identifier-based slug. | slugify 从标题派生 URL 片段。非 ASCII 文本会得到空结果，
// 调用方随后回退到基于标识符的别名。
func slugify(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// validSlug reports whether value is a safe URL segment. | validSlug 报告 value 是否是安全的 URL 片段。
func validSlug(value string) bool {
	if value == "" || len(value) > 120 {
		return false
	}
	lastDash := false
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			lastDash = false
		case r == '-' && i > 0 && !lastDash:
			lastDash = true
		default:
			return false
		}
	}
	return !lastDash
}

// normalizedSlug validates a supplied slug or derives and validates one. fallbackID is used when
// the title has no ASCII characters at all, which is common for Chinese titles. |
// normalizedSlug 校验提供的别名，或派生并校验一个。当标题完全没有 ASCII 字符时（中文标题很常见）
// 使用 fallbackID。
func (s *Service) normalizedSlug(value, fallback, fallbackID string) (string, error) {
	slug := strings.TrimSpace(strings.ToLower(value))
	if slug == "" {
		slug = slugify(fallback)
	}
	if slug == "" {
		slug = strings.ToLower(strings.TrimSpace(fallbackID))
	}
	if !validSlug(slug) {
		return "", errx.Detail(ErrInvalidInput, "invalid slug %[1]q | 非法别名 %[1]q", slug)
	}
	return slug, nil
}

// mapStoreError maps a generic store miss onto the caller's sentinel. | mapStoreError 把通用存储未命中映射为调用方哨兵。
func mapStoreError(err error, missing error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return missing
	}
	return errx.Wrap(err, ErrUnavailable)
}

// checkText validates a required text field against its byte ceiling. | checkText 按字节上限校验必填文本字段。
func checkText(name, value string, max int) error {
	if strings.TrimSpace(value) == "" {
		return errx.Detail(ErrInvalidInput, "%s is required | %s 为必填", name, name)
	}
	if max > 0 && len(value) > max {
		return errx.Detail(ErrInvalidInput, "%s is too long | %s 过长", name, name)
	}
	return nil
}

// checkOptionalText rejects an optional value that exceeds its column width. |
// checkOptionalText 拒绝超过列宽的可选值。
func checkOptionalText(name, value string, max int) error {
	if value != "" && max > 0 && len(value) > max {
		return errx.Detail(ErrInvalidInput, "%s is too long | %s 过长", name, name)
	}
	return nil
}

// defaultMediaTypes returns the default upload allow list. SVG and HTML are deliberately absent. |
// defaultMediaTypes 返回默认上传白名单。SVG 与 HTML 刻意不在其中。
func defaultMediaTypes() []string {
	return []string{
		"image/jpeg",
		"image/png",
		"image/gif",
		"image/webp",
		// Go 的 http.DetectContentType 不认识 AVIF（会报 application/octet-stream），放行只会让每次上传都失败。 |
		// Go's http.DetectContentType does not recognise AVIF (it reports application/octet-stream), so listing it
		// would only make every AVIF upload fail.
		"application/pdf",
		"video/mp4",
		"video/webm",
		"audio/mpeg",
	}
}
