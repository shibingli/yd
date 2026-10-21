package cms

import "time"

// ArticleKind distinguishes a chronological post from a standalone page. | ArticleKind 区分按时间发布的文章与独立单页。
type ArticleKind string

const (
	// KindPost is a normal article shown in lists, categories and feeds. | KindPost 是出现在列表、分类与订阅源中的普通文章。
	KindPost ArticleKind = "post"
	// KindPage is a standalone page addressed by its own slug. | KindPage 是按自身别名寻址的独立单页。
	KindPage ArticleKind = "page"
)

// ArticleStatus is the editorial lifecycle state persisted in the database. | ArticleStatus 是持久化在数据库中的编辑生命周期状态。
type ArticleStatus string

const (
	// ArticleDraft is editable by its author and invisible in public views. | ArticleDraft 可由作者编辑，公开视图不可见。
	ArticleDraft ArticleStatus = "draft"
	// ArticlePending waits for an editor with cms-article:review. | ArticlePending 等待具备 cms-article:review 的编辑审核。
	ArticlePending ArticleStatus = "pending"
	// ArticlePublished is visible publicly once PublishAt has passed, or immediately when it is nil. |
	// ArticlePublished 在 PublishAt 到期后公开可见；为空时立即公开。
	ArticlePublished ArticleStatus = "published"
	// ArticleArchived is retired content kept for history and restorable by an editor. | ArticleArchived 是保留作历史、可由编辑恢复的退役内容。
	ArticleArchived ArticleStatus = "archived"
)

// Visibility controls public exposure independently of the editorial state. | Visibility 独立于编辑状态控制公开暴露。
type Visibility string

const (
	// VisibilityPublic exposes the article on the public site once published. | VisibilityPublic 表示发布后在公开站点可见。
	VisibilityPublic Visibility = "public"
	// VisibilityPrivate keeps the article inside the management area. | VisibilityPrivate 表示文章只保留在管理区内。
	VisibilityPrivate Visibility = "private"
)

// CommentStatus is the moderation state of one comment. | CommentStatus 是单条评论的审核状态。
type CommentStatus string

const (
	// CommentPending waits for a moderator when site settings require moderation. | CommentPending 在站点设置要求审核时等待审核者。
	CommentPending CommentStatus = "pending"
	// CommentApproved is visible on the public article page. | CommentApproved 在公开文章页可见。
	CommentApproved CommentStatus = "approved"
	// CommentRejected was refused by a moderator. | CommentRejected 已被审核者拒绝。
	CommentRejected CommentStatus = "rejected"
	// CommentSpam was classified as spam. | CommentSpam 被归类为垃圾评论。
	CommentSpam CommentStatus = "spam"
	// CommentDeleted is a tombstone; the body is cleared and the row kept for thread shape. |
	// CommentDeleted 是墓碑记录：正文已清空，行保留以维持回复结构。
	CommentDeleted CommentStatus = "deleted"
)

// ArticleAction names one editorial transition. | ArticleAction 命名一次编辑生命周期转换。
type ArticleAction string

const (
	// ActionSubmit moves a draft into review. | ActionSubmit 把草稿提交审核。
	ActionSubmit ArticleAction = "submit"
	// ActionApprove approves a pending article and publishes it. | ActionApprove 审核通过待审文章并发布。
	ActionApprove ArticleAction = "approve"
	// ActionReject returns a pending article to its author as a draft. | ActionReject 把待审文章退回作者成为草稿。
	ActionReject ArticleAction = "reject"
	// ActionPublish publishes immediately or schedules a future PublishAt. | ActionPublish 立即发布或按未来的 PublishAt 定时发布。
	ActionPublish ArticleAction = "publish"
	// ActionUnpublish returns a published article to its author as a draft. | ActionUnpublish 把已发布文章退回作者成为草稿。
	ActionUnpublish ArticleAction = "unpublish"
	// ActionArchive retires an article while keeping it restorable. | ActionArchive 让文章退役但保持可恢复。
	ActionArchive ArticleAction = "archive"
	// ActionRestore returns an archived article to draft. | ActionRestore 把已归档文章恢复为草稿。
	ActionRestore ArticleAction = "restore"
)

// MaxCategoryDepth bounds pathological trees; 100 levels is far beyond any editorial structure and
// keeps recursion and materialised-path updates predictable. | MaxCategoryDepth 限制病态树；100 层远超
// 任何编辑结构，同时让递归与物化路径更新保持可预测。
const MaxCategoryDepth = 100

// MaxCategoryPathLength stays well inside MySQL's TEXT limit (65,535 bytes). Path is TEXT rather
// than VARCHAR because it is no longer indexed and must not impose a depth limit. |
// MaxCategoryPathLength 远低于 MySQL TEXT 的 65,535 字节上限。Path 使用 TEXT 而不是 VARCHAR，
// 因为它已不再建索引，也不应再限制层级深度。
const MaxCategoryPathLength = 60000

// Category is one node of the tenant's unlimited-depth tree. Path is a materialised prefix such as
// "/cat_root/cat_child/" and always ends with a slash, so a subtree query is a prefix match. |
// Category 是租户无限级树的一个节点。Path 是形如 "/cat_root/cat_child/" 的物化前缀，且总以斜杠结尾，
// 因此子树查询就是前缀匹配。
type Category struct {
	// CreatedAt is the creation time in UTC. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	// UpdatedAt is the last update time in UTC. | UpdatedAt 是 UTC 最后更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
	// ID is the category identifier. | ID 是分类标识。
	ID string `gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the owning tenant. | TenantID 是所属租户。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_cms_category_slug,priority:1;index:idx_cms_category_parent,priority:1;"`
	// ParentID is the parent category, empty for a root. | ParentID 是父分类，根节点为空。
	ParentID string `gorm:"column:parent_id;type:varchar(64);not null;default:'';index:idx_cms_category_parent,priority:2"`
	// Name is the display name. | Name 是显示名称。
	Name string `gorm:"column:name;type:varchar(160);not null"`
	// Slug is the tenant-unique URL segment. | Slug 是租户内唯一的 URL 片段。
	Slug string `gorm:"column:slug;type:varchar(160);not null;uniqueIndex:uidx_cms_category_slug,priority:2"`
	// Description is an optional short summary. | Description 是可选的简短说明。
	Description string `gorm:"column:description;type:text"`
	// Path is the materialised ancestor path including this node. | Path 是包含本节点的物化祖先路径。
	Path string `gorm:"column:path;type:text;not null"`
	// CreatedBy is the author identifier snapshot. | CreatedBy 是创建者标识快照。
	CreatedBy string `gorm:"column:created_by;type:varchar(64)"`
	// SortOrder orders siblings ascending. | SortOrder 按升序排列同级节点。
	SortOrder int `gorm:"column:sort_order;not null;default:0;index:idx_cms_category_parent,priority:3"`
	// Depth is the zero-based depth. | Depth 是从零开始的深度。
	Depth int `gorm:"column:depth;not null;default:0"`
	// Visible hides the category from public navigation when false. | Visible 为 false 时从公开导航隐藏该分类。
	Visible bool `gorm:"column:visible;not null"`
}

// TableName returns the physical table name. | TableName 返回物理表名。
func (Category) TableName() string { return "yd_cms_categories" }

// Article is the persisted content record. Markdown, sanitised HTML and plain text are stored as
// snapshots so readers, search and the editor all see exactly the same revision of the body. |
// Article 是持久化的内容记录。Markdown、净化后的 HTML 与纯文本以快照形式保存，
// 使读者、检索与编辑器看到的正文完全一致。
type Article struct {
	// CreatedAt is the creation time in UTC. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	// UpdatedAt is the last update time in UTC. | UpdatedAt 是 UTC 最后更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime;index:idx_cms_article_updated,priority:2"`
	// PublishAt schedules publication; nil means immediately on publish. | PublishAt 定时发布时间；为空表示发布即公开。
	PublishAt *time.Time `gorm:"column:publish_at;index:idx_cms_article_status,priority:3;index:idx_cms_article_category,priority:4;index:idx_cms_article_kind,priority:4"`
	// DeletedAt marks a soft-deleted article; the slug is rewritten on delete so it can be reused. |
	// DeletedAt 标记软删除；删除时改写别名，使旧别名可以复用。
	DeletedAt *time.Time `gorm:"column:deleted_at;index:idx_cms_article_deleted,priority:2"`
	// ID is the article identifier. | ID 是文章标识。
	ID string `gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the owning tenant. | TenantID 是所属租户。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_cms_article_slug,priority:1;index:idx_cms_article_status,priority:1;index:idx_cms_article_category,priority:1;index:idx_cms_article_author,priority:1;index:idx_cms_article_kind,priority:1;index:idx_cms_article_updated,priority:1;index:idx_cms_article_visibility,priority:1;index:idx_cms_article_deleted,priority:1"`
	// CategoryID is the primary category; pages may leave it empty. | CategoryID 是主分类；单页可以为空。
	CategoryID string `gorm:"column:category_id;type:varchar(64);not null;default:'';index:idx_cms_article_category,priority:2"`
	// AuthorID is the author's global user id. | AuthorID 是作者的全局用户 ID。
	AuthorID string `gorm:"column:author_id;type:varchar(64);not null;index:idx_cms_article_author,priority:2"`
	// AuthorName is the display-name snapshot. | AuthorName 是显示名快照。
	AuthorName string `gorm:"column:author_name;type:varchar(160)"`
	// Title is the human title. | Title 是文章标题。
	Title string `gorm:"column:title;type:varchar(300);not null"`
	// Slug is the tenant-unique URL segment. | Slug 是租户内唯一的 URL 片段。
	Slug string `gorm:"column:slug;type:varchar(200);not null;uniqueIndex:uidx_cms_article_slug,priority:2"`
	// Summary is the list/SEO summary. | Summary 是列表与 SEO 摘要。
	Summary string `gorm:"column:summary;type:text"`
	// CoverURL is an optional cover image URL. | CoverURL 是可选的封面图 URL。
	CoverURL string `gorm:"column:cover_url;type:varchar(1024)"`
	// BodyMarkdown is the editable source. | BodyMarkdown 是可编辑的 Markdown 源。
	BodyMarkdown string `gorm:"column:body_markdown;size:1048576"`
	// BodyHTML is the server-rendered and sanitised HTML snapshot. | BodyHTML 是服务端渲染并净化后的 HTML 快照。
	BodyHTML string `gorm:"column:body_html;size:1048576"`
	// BodyText is the plain-text projection used for preview and search. | BodyText 是用于预览与检索的纯文本投影。
	BodyText string `gorm:"column:body_text;size:1048576"`
	// SearchText is the lower-cased search projection. | SearchText 是转为小写的检索投影。
	SearchText string `gorm:"column:search_text;size:1048576"`
	// Kind is post or page. | Kind 是文章或单页。
	Kind ArticleKind `gorm:"column:kind;type:varchar(16);not null;default:'post';index:idx_cms_article_kind,priority:2"`
	// Status is the editorial state. | Status 是编辑状态。
	Status ArticleStatus `gorm:"column:status;type:varchar(16);not null;default:'draft';index:idx_cms_article_status,priority:2;index:idx_cms_article_category,priority:3;index:idx_cms_article_kind,priority:3;index:idx_cms_article_visibility,priority:3"`
	// Visibility is public or private. | Visibility 是公开或私有。
	Visibility Visibility `gorm:"column:visibility;type:varchar(16);not null;default:'public';index:idx_cms_article_visibility,priority:2"`
	// SEOTitle overrides Title in the document head when non-empty. | SEOTitle 非空时覆盖文档头中的 Title。
	SEOTitle string `gorm:"column:seo_title;type:varchar(300)"`
	// SEODescription overrides Summary in the metadata. | SEODescription 在元数据中覆盖 Summary。
	SEODescription string `gorm:"column:seo_description;type:varchar(500)"`
	// SEOKeywords is the optional keyword list. | SEOKeywords 是可选关键词列表。
	SEOKeywords string `gorm:"column:seo_keywords;type:varchar(500)"`
	// CanonicalURL is an optional canonical link. | CanonicalURL 是可选的规范链接。
	CanonicalURL string `gorm:"column:canonical_url;type:varchar(1024)"`
	// Source credits an original publication. | Source 标注原文出处。
	Source string `gorm:"column:source;type:varchar(300)"`
	// Version is the current revision number. | Version 是当前修订号。
	Version int `gorm:"column:version;not null;default:1"`
	// ViewCount is the public view counter. | ViewCount 是公开浏览量。
	ViewCount int64 `gorm:"column:view_count;not null;default:0"`
	// Featured marks an article for highlighted placement. | Featured 标记推荐文章。
	Featured bool `gorm:"column:featured;not null;default:false"`
	// Pinned marks an article for the top of lists. | Pinned 标记置顶文章。
	Pinned bool `gorm:"column:pinned;not null;default:false"`
	// AllowComment controls whether the public article page accepts comments. | AllowComment 控制公开文章页是否接受评论。
	AllowComment bool `gorm:"column:allow_comment;not null"`
}

// TableName returns the physical table name. | TableName 返回物理表名。
func (Article) TableName() string { return "yd_cms_articles" }

// Tag is one tenant-scoped label. | Tag 是租户级标签。
type Tag struct {
	// CreatedAt is the creation time in UTC. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	// UpdatedAt is the last update time in UTC. | UpdatedAt 是 UTC 最后更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
	// ID is the tag identifier. | ID 是标签标识。
	ID string `gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the owning tenant. | TenantID 是所属租户。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_cms_tag_slug,priority:1;index:idx_cms_tag_name,priority:1"`
	// Name is the display name. | Name 是显示名称。
	Name string `gorm:"column:name;type:varchar(80);not null;index:idx_cms_tag_name,priority:2"`
	// Slug is the tenant-unique URL segment. | Slug 是租户内唯一的 URL 片段。
	Slug string `gorm:"column:slug;type:varchar(80);not null;uniqueIndex:uidx_cms_tag_slug,priority:2"`
}

// TableName returns the physical table name. | TableName 返回物理表名。
func (Tag) TableName() string { return "yd_cms_tags" }

// ArticleTag links one article to one tag. | ArticleTag 把一篇文章链接到一个标签。
type ArticleTag struct {
	// CreatedAt is the creation time in UTC. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	// ID is the join-row identifier. | ID 是连接行标识。
	ID string `gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the owning tenant. | TenantID 是所属租户。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_cms_article_tag,priority:1;index:idx_cms_article_tag_tag,priority:1"`
	// ArticleID is the article side. | ArticleID 是文章一侧。
	ArticleID string `gorm:"column:article_id;type:varchar(64);not null;uniqueIndex:uidx_cms_article_tag,priority:2;index:idx_cms_article_tag_tag,priority:3"`
	// TagID is the tag side. | TagID 是标签一侧。
	TagID string `gorm:"column:tag_id;type:varchar(64);not null;uniqueIndex:uidx_cms_article_tag,priority:3;index:idx_cms_article_tag_tag,priority:2"`
}

// TableName returns the physical table name. | TableName 返回物理表名。
func (ArticleTag) TableName() string { return "yd_cms_article_tags" }

// Revision is an immutable snapshot written before an article body changes. | Revision 是在文章正文变更前写入的不可变快照。
type Revision struct {
	// CreatedAt is the creation time in UTC. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	// ID is the revision identifier. | ID 是修订标识。
	ID string `gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the owning tenant. | TenantID 是所属租户。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);not null;uniqueIndex:uidx_cms_revision_version,priority:1;index:idx_cms_revision_created,priority:1"`
	// ArticleID is the article this snapshot belongs to. | ArticleID 是快照所属文章。
	ArticleID string `gorm:"column:article_id;type:varchar(64);not null;uniqueIndex:uidx_cms_revision_version,priority:2;index:idx_cms_revision_created,priority:2"`
	// EditorID is the user who saved the revision. | EditorID 是保存修订的用户。
	EditorID string `gorm:"column:editor_id;type:varchar(64)"`
	// EditorName is the display-name snapshot. | EditorName 是显示名快照。
	EditorName string `gorm:"column:editor_name;type:varchar(160)"`
	// Title is the title at snapshot time. | Title 是快照时的标题。
	Title string `gorm:"column:title;type:varchar(300)"`
	// BodyMarkdown is the Markdown source at snapshot time. | BodyMarkdown 是快照时的 Markdown 源。
	BodyMarkdown string `gorm:"column:body_markdown;size:1048576"`
	// BodyHTML is the rendered HTML at snapshot time. | BodyHTML 是快照时渲染的 HTML。
	BodyHTML string `gorm:"column:body_html;size:1048576"`
	// BodyText is the plain-text projection at snapshot time. | BodyText 是快照时的纯文本投影。
	BodyText string `gorm:"column:body_text;size:1048576"`
	// Note explains why the revision was saved. | Note 说明保存修订的原因。
	Note string `gorm:"column:note;type:varchar(300)"`
	// Version is the article version this snapshot represents. | Version 是该快照对应的文章版本。
	Version int `gorm:"column:version;not null;uniqueIndex:uidx_cms_revision_version,priority:3"`
}

// TableName returns the physical table name. | TableName 返回物理表名。
func (Revision) TableName() string { return "yd_cms_revisions" }

// Media is one uploaded file managed by the CMS library. | Media 是 CMS 媒体库管理的一份上传文件。
type Media struct {
	// CreatedAt is the creation time in UTC. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime;index:idx_cms_media_created,priority:2"`
	// ID is the media identifier. | ID 是媒体标识。
	ID string `gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the owning tenant. | TenantID 是所属租户。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);not null;index:idx_cms_media_created,priority:1;index:idx_cms_media_uploader,priority:1;index:idx_cms_media_type,priority:1"`
	// UploaderID is the uploading user. | UploaderID 是上传用户。
	UploaderID string `gorm:"column:uploader_id;type:varchar(64);not null;index:idx_cms_media_uploader,priority:2"`
	// UploaderName is the display-name snapshot. | UploaderName 是显示名快照。
	UploaderName string `gorm:"column:uploader_name;type:varchar(160)"`
	// OriginalName is the user-supplied name, shown but never used as a path. | OriginalName 是用户提供的文件名，
	// 仅用于显示，绝不作为路径使用。
	OriginalName string `gorm:"column:original_name;type:varchar(300)"`
	// StoredKey is the opaque blob key. | StoredKey 是不透明文件键。
	StoredKey string `gorm:"column:stored_key;type:varchar(200);not null"`
	// ContentType is the server-sniffed MIME type. | ContentType 是服务端嗅探的 MIME 类型。
	ContentType string `gorm:"column:content_type;type:varchar(120);not null;index:idx_cms_media_type,priority:2"`
	// SHA256 is the content digest. | SHA256 是内容摘要。
	SHA256 string `gorm:"column:sha256;type:varchar(64)"`
	// Alt is the accessibility text for images. | Alt 是图片的无障碍文本。
	Alt string `gorm:"column:alt;type:varchar(500)"`
	// Size is the byte length. | Size 是字节长度。
	Size int64 `gorm:"column:size;not null;default:0"`
	// Width is the pixel width for decodable images, zero otherwise. | Width 是可解码图片的像素宽度，否则为零。
	Width int `gorm:"column:width;not null;default:0"`
	// Height is the pixel height for decodable images, zero otherwise. | Height 是可解码图片的像素高度，否则为零。
	Height int `gorm:"column:height;not null;default:0"`
}

// TableName returns the physical table name. | TableName 返回物理表名。
func (Media) TableName() string { return "yd_cms_media" }

// Comment is one moderated reader comment. | Comment 是一条受审核的读者评论。
type Comment struct {
	// CreatedAt is the creation time in UTC. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime;index:idx_cms_comment_article,priority:4;index:idx_cms_comment_status,priority:3;index:idx_cms_comment_author,priority:3"`
	// UpdatedAt is the last update time in UTC. | UpdatedAt 是 UTC 最后更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
	// ID is the comment identifier. | ID 是评论标识。
	ID string `gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the owning tenant. | TenantID 是所属租户。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);not null;index:idx_cms_comment_article,priority:1;index:idx_cms_comment_status,priority:1;index:idx_cms_comment_author,priority:1;index:idx_cms_comment_parent,priority:1"`
	// ArticleID is the commented article. | ArticleID 是被评论的文章。
	ArticleID string `gorm:"column:article_id;type:varchar(64);not null;index:idx_cms_comment_article,priority:2"`
	// ParentID is the replied-to comment, empty for a root comment. | ParentID 是被回复的评论，根评论为空。
	ParentID string `gorm:"column:parent_id;type:varchar(64);not null;default:'';index:idx_cms_comment_parent,priority:2"`
	// AuthorID is the commenting user. | AuthorID 是评论用户。
	AuthorID string `gorm:"column:author_id;type:varchar(64);not null;index:idx_cms_comment_author,priority:2"`
	// AuthorName is the display-name snapshot. | AuthorName 是显示名快照。
	AuthorName string `gorm:"column:author_name;type:varchar(160)"`
	// BodyMarkdown is the editable source. | BodyMarkdown 是可编辑的 Markdown 源。
	BodyMarkdown string `gorm:"column:body_markdown;type:text"`
	// BodyHTML is the sanitised HTML snapshot. | BodyHTML 是净化后的 HTML 快照。
	BodyHTML string `gorm:"column:body_html;type:text"`
	// BodyText is the plain-text projection. | BodyText 是纯文本投影。
	BodyText string `gorm:"column:body_text;type:text"`
	// Status is the moderation state. | Status 是审核状态。
	Status CommentStatus `gorm:"column:status;type:varchar(16);not null;default:'pending';index:idx_cms_comment_article,priority:3;index:idx_cms_comment_status,priority:2"`
	// Depth is the reply depth, zero for a root comment. | Depth 是回复深度，根评论为零。
	Depth int `gorm:"column:depth;not null;default:0"`
}

// TableName returns the physical table name. | TableName 返回物理表名。
func (Comment) TableName() string { return "yd_cms_comments" }

// Settings is the tenant-scoped public-site configuration. | Settings 是租户级公开站点配置。
type Settings struct {
	// CreatedAt is the creation time in UTC. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	// UpdatedAt is the last update time in UTC. | UpdatedAt 是 UTC 最后更新时间。
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
	// TenantID is the primary key and owning tenant. | TenantID 是主键及所属租户。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);primaryKey"`
	// SiteName is the public site title. | SiteName 是公开站点标题。
	SiteName string `gorm:"column:site_name;type:varchar(200)"`
	// SiteDescription is the default meta description. | SiteDescription 是默认元描述。
	SiteDescription string `gorm:"column:site_description;type:varchar(500)"`
	// SiteKeywords is the default keyword list. | SiteKeywords 是默认关键词列表。
	SiteKeywords string `gorm:"column:site_keywords;type:varchar(500)"`
	// FooterText is the public footer copy. | FooterText 是公开页脚文案。
	FooterText string `gorm:"column:footer_text;type:varchar(500)"`
	// PostsPerPage is the public list page length. | PostsPerPage 是公开列表页长。
	PostsPerPage int `gorm:"column:posts_per_page;not null;default:10"`
	// CommentsEnabled turns the comment form on or off site-wide. | CommentsEnabled 全站开关评论表单。
	CommentsEnabled bool `gorm:"column:comments_enabled;not null"`
	// CommentModeration holds new comments for review. | CommentModeration 让新评论等待审核。
	CommentModeration bool `gorm:"column:comment_moderation;not null"`
	// RSSEnabled exposes the feed endpoint content. | RSSEnabled 控制订阅源内容。
	RSSEnabled bool `gorm:"column:rss_enabled;not null"`
}

// TableName returns the physical table name. | TableName 返回物理表名。
func (Settings) TableName() string { return "yd_cms_settings" }

// AuditEntry is one administrative action kept for the tenant audit trail. | AuditEntry 是租户审计轨迹中的一条管理动作。
type AuditEntry struct {
	// CreatedAt is the creation time in UTC. | CreatedAt 是 UTC 创建时间。
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime;index:idx_cms_audit_created,priority:2"`
	// ID is the audit identifier. | ID 是审计标识。
	ID string `gorm:"column:id;type:varchar(64);primaryKey"`
	// TenantID is the owning tenant. | TenantID 是所属租户。
	TenantID string `gorm:"column:tenant_id;type:varchar(64);not null;index:idx_cms_audit_created,priority:1;index:idx_cms_audit_article,priority:1;index:idx_cms_audit_actor,priority:1"`
	// ActorID is the acting user. | ActorID 是执行动作的用户。
	ActorID string `gorm:"column:actor_id;type:varchar(64);index:idx_cms_audit_actor,priority:2"`
	// ActorName is the display-name snapshot. | ActorName 是显示名快照。
	ActorName string `gorm:"column:actor_name;type:varchar(160)"`
	// Action is the stable action key. | Action 是稳定的动作键。
	Action string `gorm:"column:action;type:varchar(80);not null"`
	// TargetKind is article, category, media, comment or settings. | TargetKind 是 article、category、media、comment 或 settings。
	TargetKind string `gorm:"column:target_kind;type:varchar(40)"`
	// TargetID is the affected resource identifier. | TargetID 是受影响资源标识。
	TargetID string `gorm:"column:target_id;type:varchar(64);index:idx_cms_audit_article,priority:2"`
	// Detail is a short human-readable note. | Detail 是简短的人类可读说明。
	Detail string `gorm:"column:detail;type:varchar(500)"`
}

// TableName returns the physical table name. | TableName 返回物理表名。
func (AuditEntry) TableName() string { return "yd_cms_audit" }

// CategoryNode is one category together with its children and direct article count. | CategoryNode 是一个分类及其子节点与直接文章数。
type CategoryNode struct {
	// Children are the ordered child nodes. | Children 是排序后的子节点。
	Children []CategoryNode
	// Category is the node itself. | Category 是节点本身。
	Category Category
	// ArticleCount is the number of non-deleted articles directly assigned to this category. |
	// ArticleCount 是直接归属该分类的未删除文章数。
	ArticleCount int64
}

// CategoryOption is a flat row used by parent selectors and navigation. | CategoryOption 是父级选择器与导航使用的扁平行。
type CategoryOption struct {
	// ID is the category identifier. | ID 是分类标识。
	ID string
	// Name is the display name. | Name 是显示名称。
	Name string
	// Slug is the URL segment. | Slug 是 URL 片段。
	Slug string
	// Path is the materialised path. | Path 是物化路径。
	Path string
	// Depth is the zero-based depth. | Depth 是从零开始的深度。
	Depth int
	// SortOrder orders siblings. | SortOrder 排列同级节点。
	SortOrder int
	// Visible reports public visibility. | Visible 表示公开可见性。
	Visible bool
	// ArticleCount is the direct article count. | ArticleCount 是直接文章数。
	ArticleCount int64
}

// ArticleView is an article decorated with its category and tags for display and JSON. |
// ArticleView 是附带分类与标签、供展示与 JSON 使用的文章。
type ArticleView struct {
	// CategoryName is the category display name. | CategoryName 是分类显示名。
	CategoryName string
	// CategorySlug is the category URL segment. | CategorySlug 是分类 URL 片段。
	CategorySlug string
	// CategoryPath is the category materialised path. | CategoryPath 是分类物化路径。
	CategoryPath string
	// Tags are the article's tags in name order. | Tags 是按名称排序的文章标签。
	Tags []Tag
	// Article is the persisted record. | Article 是持久化记录。
	Article
	// Scheduled reports a published article whose PublishAt is still in the future. | Scheduled 表示已发布但 PublishAt 尚未到期。
	Scheduled bool
}

// ArticlePage is one page of article results. | ArticlePage 是一页文章结果。
type ArticlePage struct {
	// Items are the decorated articles. | Items 是装饰后的文章。
	Items []ArticleView
	// Total is the total matching row count. | Total 是匹配总行数。
	Total int64
	// Page is the one-based page number. | Page 是从一开始的页码。
	Page int
	// PageSize is the page length. | PageSize 是页长。
	PageSize int
	// TotalPages is the number of pages. | TotalPages 是总页数。
	TotalPages int
}

// CommentView is a comment decorated with its article title and slug. | CommentView 是附带文章标题与别名的评论。
type CommentView struct {
	// ArticleTitle is the commented article title. | ArticleTitle 是被评论文章的标题。
	ArticleTitle string
	// ArticleSlug is the commented article slug. | ArticleSlug 是被评论文章的别名。
	ArticleSlug string
	// Comment is the persisted record. | Comment 是持久化记录。
	Comment
}

// CommentPage is one page of comment results. | CommentPage 是一页评论结果。
type CommentPage struct {
	// Items are the decorated comments. | Items 是装饰后的评论。
	Items []CommentView
	// Total is the total matching row count. | Total 是匹配总行数。
	Total int64
	// Page is the one-based page number. | Page 是从一开始的页码。
	Page int
	// PageSize is the page length. | PageSize 是页长。
	PageSize int
	// TotalPages is the number of pages. | TotalPages 是总页数。
	TotalPages int
}

// MediaPage is one page of media results. | MediaPage 是一页媒体结果。
type MediaPage struct {
	// Items are the media rows. | Items 是媒体行。
	Items []Media
	// Total is the total matching row count. | Total 是匹配总行数。
	Total int64
	// Page is the one-based page number. | Page 是从一开始的页码。
	Page int
	// PageSize is the page length. | PageSize 是页长。
	PageSize int
	// TotalPages is the number of pages. | TotalPages 是总页数。
	TotalPages int
}

// AuditPage is one page of audit results. | AuditPage 是一页审计结果。
type AuditPage struct {
	// Items are the audit rows. | Items 是审计行。
	Items []AuditEntry
	// Total is the total matching row count. | Total 是匹配总行数。
	Total int64
	// Page is the one-based page number. | Page 是从一开始的页码。
	Page int
	// PageSize is the page length. | PageSize 是页长。
	PageSize int
	// TotalPages is the number of pages. | TotalPages 是总页数。
	TotalPages int
}

// Counts is the aggregate dashboard snapshot. | Counts 是概览页的聚合快照。
type Counts struct {
	// Recent holds the most recently updated articles. | Recent 保存最近更新的文章。
	Recent []ArticleView
	// RecentComments holds the most recent comments awaiting or receiving attention. | RecentComments 保存最近需要关注的评论。
	RecentComments []CommentView
	// Articles is the number of non-deleted articles and pages. | Articles 是未删除的文章与单页总数。
	Articles int64
	// Published is the number of published articles. | Published 是已发布文章数。
	Published int64
	// Drafts is the number of draft articles. | Drafts 是草稿数。
	Drafts int64
	// Pending is the number of articles awaiting review. | Pending 是待审文章数。
	Pending int64
	// Archived is the number of archived articles. | Archived 是已归档文章数。
	Archived int64
	// Pages is the number of standalone pages. | Pages 是独立单页数。
	Pages int64
	// Categories is the number of categories. | Categories 是分类数。
	Categories int64
	// Tags is the number of tags. | Tags 是标签数。
	Tags int64
	// Media is the number of media records. | Media 是媒体记录数。
	Media int64
	// Comments is the number of non-deleted comments. | Comments 是未删除评论数。
	Comments int64
	// PendingComments is the number of comments awaiting moderation. | PendingComments 是待审核评论数。
	PendingComments int64
	// Views is the sum of article view counters. | Views 是文章浏览量总和。
	Views int64
}

// ArticleFilter selects and pages articles. Zero values mean "no restriction"; Page and PageSize
// are normalised by the service. | ArticleFilter 选择并分页文章。零值表示不限制；Page 与 PageSize 由服务规范化。
type ArticleFilter struct {
	// Now is the comparison time for PublicOnly and ScheduledOnly. | Now 是 PublicOnly 与 ScheduledOnly 的比较时间。
	Now time.Time
	// Featured restricts the featured flag when non-nil. | Featured 非空时限制推荐标记。
	Featured *bool
	// Pinned restricts the pinned flag when non-nil. | Pinned 非空时限制置顶标记。
	Pinned *bool
	// Keyword matches the search projection. | Keyword 匹配检索投影。
	Keyword string
	// CategoryID restricts to one category. | CategoryID 限制到一个分类。
	CategoryID string
	// TagID restricts to one tag. | TagID 限制到一个标签。
	TagID string
	// AuthorID restricts to one author. | AuthorID 限制到一个作者。
	AuthorID string
	// Status restricts the editorial state. | Status 限制编辑状态。
	Status ArticleStatus
	// Kind restricts posts or pages. | Kind 限制文章或单页。
	Kind ArticleKind
	// Visibility restricts public or private. | Visibility 限制公开或私有。
	Visibility Visibility
	// Order selects a whitelisted ordering: empty (newest update), created, oldest or title. |
	// Order 选择白名单排序：空（最新更新）、created、oldest 或 title。
	Order string
	// CategoryIDs restricts to a computed subtree; it wins over CategoryID when non-empty. |
	// CategoryIDs 限制到计算出的子树；非空时优先于 CategoryID。
	CategoryIDs []string
	// Page is the one-based page number. | Page 是从一开始的页码。
	Page int
	// PageSize is the page length. | PageSize 是页长。
	PageSize int
	// PublicOnly applies the public contract: published, public and PublishAt reached. |
	// PublicOnly 施加公开契约：已发布、公开且 PublishAt 已到期。
	PublicOnly bool
	// ScheduledOnly restricts to published rows whose PublishAt is still in the future. |
	// ScheduledOnly 限制到已发布但 PublishAt 尚未到期的行。
	ScheduledOnly bool
}

// CommentFilter selects and pages comments. | CommentFilter 选择并分页评论。
type CommentFilter struct {
	// ArticleID restricts to one article. | ArticleID 限制到一篇文章。
	ArticleID string
	// Status restricts the moderation state. | Status 限制审核状态。
	Status CommentStatus
	// AuthorID restricts to one author. | AuthorID 限制到一个作者。
	AuthorID string
	// Keyword matches author name and body text. | Keyword 匹配作者名与正文纯文本。
	Keyword string
	// PublicOnly returns approved comments only. | PublicOnly 只返回已通过评论。
	PublicOnly bool
	// Page is the one-based page number. | Page 是从一开始的页码。
	Page int
	// PageSize is the page length. | PageSize 是页长。
	PageSize int
}

// MediaFilter selects and pages media. | MediaFilter 选择并分页媒体。
type MediaFilter struct {
	// Keyword matches the original name and alt text. | Keyword 匹配原始文件名与替代文本。
	Keyword string
	// ContentType restricts the MIME type prefix. | ContentType 限制 MIME 类型前缀。
	ContentType string
	// Page is the one-based page number. | Page 是从一开始的页码。
	Page int
	// PageSize is the page length. | PageSize 是页长。
	PageSize int
}

// AuditFilter selects and pages audit entries. | AuditFilter 选择并分页审计记录。
type AuditFilter struct {
	// TargetID restricts to one target. | TargetID 限制到一个目标。
	TargetID string
	// ActorID restricts to one actor. | ActorID 限制到一个执行者。
	ActorID string
	// Action restricts to one action key. | Action 限制到一个动作键。
	Action string
	// Page is the one-based page number. | Page 是从一开始的页码。
	Page int
	// PageSize is the page length. | PageSize 是页长。
	PageSize int
}

// CategoryInput is the editable category payload. | CategoryInput 是可编辑的分类载荷。
type CategoryInput struct {
	// Name is the display name. | Name 是显示名称。
	Name string
	// Slug is the optional URL segment; empty derives one from Name. | Slug 是可选 URL 片段；为空时从 Name 派生。
	Slug string
	// Description is the optional summary. | Description 是可选说明。
	Description string
	// ParentID is the parent category, empty for a root. | ParentID 是父分类，根节点为空。
	ParentID string
	// SortOrder orders siblings. | SortOrder 排列同级节点。
	SortOrder int
	// Visible controls public navigation. | Visible 控制公开导航。
	Visible bool
}

// TagInput is the editable tag payload. | TagInput 是可编辑的标签载荷。
type TagInput struct {
	// Name is the display name. | Name 是显示名称。
	Name string
	// Slug is the optional URL segment; empty derives one from Name. | Slug 是可选 URL 片段；为空时从 Name 派生。
	Slug string
}

// ArticleInput is the editable article payload. Boolean fields are authoritative on save, so an
// unticked form checkbox turns the flag off. | ArticleInput 是可编辑的文章载荷。布尔字段在保存时为准，
// 因此表单中取消勾选会关闭对应标记。
type ArticleInput struct {
	// ID is empty for a create and set for an update. | ID 在新建时为空，更新时为文章标识。
	ID string
	// CategoryID is the primary category. | CategoryID 是主分类。
	CategoryID string
	// Title is the article title. | Title 是文章标题。
	Title string
	// Slug is the optional URL segment; empty derives one from Title. | Slug 是可选 URL 片段；为空时从 Title 派生。
	Slug string
	// Summary is the list and SEO summary. | Summary 是列表与 SEO 摘要。
	Summary string
	// CoverURL is the optional cover image. | CoverURL 是可选封面图。
	CoverURL string
	// BodyMarkdown is the editable source. | BodyMarkdown 是可编辑源。
	BodyMarkdown string
	// Kind is post or page. | Kind 是文章或单页。
	Kind ArticleKind
	// Visibility is public or private. | Visibility 是公开或私有。
	Visibility Visibility
	// SEOTitle is the document-head title override. | SEOTitle 是文档头标题覆盖。
	SEOTitle string
	// SEODescription is the meta description override. | SEODescription 是元描述覆盖。
	SEODescription string
	// SEOKeywords is the meta keyword list. | SEOKeywords 是元关键词列表。
	SEOKeywords string
	// CanonicalURL is the canonical link. | CanonicalURL 是规范链接。
	CanonicalURL string
	// Source credits the original publication. | Source 标注原文出处。
	Source string
	// Note explains the edit in the revision history. | Note 在修订历史中说明本次编辑。
	Note string
	// TagIDs attaches existing tags. | TagIDs 挂载已有标签。
	TagIDs []string
	// TagNames creates or reuses tags by name. | TagNames 按名称创建或复用标签。
	TagNames []string
	// Featured marks the article. | Featured 标记推荐。
	Featured bool
	// Pinned pins the article. | Pinned 标记置顶。
	Pinned bool
	// AllowComment accepts comments when true. | AllowComment 为 true 时接受评论。
	AllowComment bool
}

// MediaInput is one upload. ContentType is a hint only; the service sniffs the bytes. |
// MediaInput 是一次上传。ContentType 只是提示；服务会嗅探字节。
type MediaInput struct {
	// OriginalName is shown to users and never used as a path. | OriginalName 展示给用户，绝不作为路径。
	OriginalName string
	// ContentType is the client-declared type, ignored in favour of sniffing. | ContentType 是客户端声明类型，以嗅探结果为准。
	ContentType string
	// Alt is the accessibility text. | Alt 是无障碍文本。
	Alt string
	// Data is the file payload. | Data 是文件内容。
	Data []byte
}

// CommentInput is one new comment. | CommentInput 是一条新评论。
type CommentInput struct {
	// ArticleID is the commented article. | ArticleID 是被评论的文章。
	ArticleID string
	// ParentID is the replied-to comment, empty for a root. | ParentID 是被回复的评论，根评论为空。
	ParentID string
	// Body is the Markdown source. | Body 是 Markdown 源。
	Body string
}

// SettingsInput is the editable site configuration. | SettingsInput 是可编辑的站点配置。
type SettingsInput struct {
	// SiteName is the public site title. | SiteName 是公开站点标题。
	SiteName string
	// SiteDescription is the default meta description. | SiteDescription 是默认元描述。
	SiteDescription string
	// SiteKeywords is the default keyword list. | SiteKeywords 是默认关键词列表。
	SiteKeywords string
	// FooterText is the public footer copy. | FooterText 是公开页脚文案。
	FooterText string
	// PostsPerPage is the public list page length. | PostsPerPage 是公开列表页长。
	PostsPerPage int
	// CommentsEnabled toggles commenting site-wide. | CommentsEnabled 全站开关评论。
	CommentsEnabled bool
	// CommentModeration holds new comments for review. | CommentModeration 让新评论等待审核。
	CommentModeration bool
	// RSSEnabled toggles feed content. | RSSEnabled 控制订阅源内容。
	RSSEnabled bool
}
