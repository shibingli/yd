// Package gormstore is the GORM-backed cms.Store. | Package gormstore 是基于 GORM 的 cms.Store。
//
// The schema is owned here: Migrate creates yd_cms_categories, yd_cms_articles, yd_cms_tags,
// yd_cms_article_tags, yd_cms_revisions, yd_cms_media, yd_cms_comments, yd_cms_settings and
// yd_cms_audit, every one of them tenant-first. Migrate is idempotent and safe to run on every
// start. | 模式由本包拥有：Migrate 创建上述九张表，所有索引都以租户为第一列。
// Migrate 幂等，可在每次启动时运行。
package gormstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/shibingli/yd/cms"
	"github.com/shibingli/yd/core/errx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Sentinel errors of the store. | 存储层的哨兵错误。
var (
	// ErrMigrate reports a failed schema migration. | ErrMigrate 表示模式迁移失败。
	ErrMigrate = errx.New("CMS schema migration failed | CMS 数据表迁移失败")
	// ErrQuery reports a failed statement. | ErrQuery 表示语句执行失败。
	ErrQuery = errx.New("CMS statement failed | CMS 语句执行失败")
)

// Store implements cms.Store over GORM. | Store 基于 GORM 实现 cms.Store。
type Store struct {
	// db is the shared connection pool. | db 是共享连接池。
	db *gorm.DB
}

// compile-time check that the implementation stays complete. | 编译期确认实现保持完整。
var _ cms.Store = (*Store)(nil)

// New returns a store over db. | New 返回基于 db 的存储。
func New(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, ErrQuery
	}
	return &Store{db: db}, nil
}

// Migrate creates or updates every CMS table. | Migrate 创建或更新全部 CMS 表。
func Migrate(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return ErrMigrate
	}
	models := []any{
		&cms.Category{},
		&cms.Article{},
		&cms.Tag{},
		&cms.ArticleTag{},
		&cms.Revision{},
		&cms.Media{},
		&cms.Comment{},
		&cms.Settings{},
		&cms.AuditEntry{},
	}
	if err := db.WithContext(ctx).AutoMigrate(models...); err != nil {
		return errx.Wrap(err, ErrMigrate)
	}
	if db.Dialector.Name() == "mysql" {
		if err := ensureMySQLLongText(ctx, db); err != nil {
			return errx.Wrap(err, ErrMigrate)
		}
	}
	return nil
}

// ensureMySQLLongText widens the article and revision text columns to MEDIUMTEXT on MySQL.
// GORM's default TEXT is 64 KiB, while cms.MaxBodyLen defaults to 512 KiB; without this the
// configured ceiling would fail on MySQL only, while SQLite and PostgreSQL hide the mismatch. |
// ensureMySQLLongText 在 MySQL 上把文章与修订的文本列扩为 MEDIUMTEXT。GORM 默认的 TEXT 只有 64 KiB，
// 而 cms.MaxBodyLen 默认 512 KiB；不扩列时只有 MySQL 会在保存时失败，SQLite 与 PostgreSQL 则掩盖差异。
func ensureMySQLLongText(ctx context.Context, db *gorm.DB) error {
	columns := []struct {
		table string
		names []string
	}{
		{"yd_cms_articles", []string{"body_markdown", "body_html", "body_text", "search_text"}},
		{"yd_cms_revisions", []string{"body_markdown", "body_html", "body_text"}},
	}
	for _, table := range columns {
		types, err := db.WithContext(ctx).Migrator().ColumnTypes(table.table)
		if err != nil {
			return err
		}
		wanted := make(map[string]bool, len(table.names))
		for _, name := range table.names {
			wanted[name] = true
		}
		mods := make([]string, 0, len(table.names))
		for _, column := range types {
			name := column.Name()
			if !wanted[name] || !strings.EqualFold(column.DatabaseTypeName(), "text") {
				continue
			}
			mods = append(mods, "MODIFY COLUMN `"+name+"` MEDIUMTEXT")
		}
		if len(mods) == 0 {
			continue
		}
		if err := db.WithContext(ctx).Exec("ALTER TABLE `" + table.table + "` " + strings.Join(mods, ", ")).Error; err != nil {
			return err
		}
	}
	return nil
}

// dbc returns a context-bound handle. | dbc 返回绑定上下文的句柄。
func (s *Store) dbc(ctx context.Context) *gorm.DB { return s.db.WithContext(ctx) }

// notFound maps gorm.ErrRecordNotFound onto cms.ErrNotFound. | notFound 把 gorm.ErrRecordNotFound 映射为 cms.ErrNotFound。
func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errx.Wrap(err, cms.ErrNotFound)
	}
	return nil
}

// queryError maps a statement error, preserving not-found. | queryError 映射语句错误，并保留未找到语义。
func queryError(err error) error {
	if err == nil {
		return nil
	}
	if mapped := notFound(err); mapped != nil {
		return mapped
	}
	return errx.Wrap(err, ErrQuery)
}

// rowsError turns a zero affected-row write into not-found. | rowsError 把零影响行写入转成未找到。
func rowsError(result *gorm.DB) error {
	if result.Error != nil {
		return queryError(result.Error)
	}
	if result.RowsAffected == 0 {
		return errx.Detail(cms.ErrNotFound, "row does not exist | 记录不存在")
	}
	return nil
}

// updateResult accepts a no-op update as success. MySQL reports zero affected rows when the new values
// equal the stored ones, while the row plainly exists; treating that as not-found turns "save unchanged"
// into a 404. | updateResult 把「值未变化」的更新视为成功。MySQL 在新值与库中值相同时报告零影响行，
// 但记录确实存在；把它当成未找到会让「原样保存」变成 404。
func (s *Store) updateResult(ctx context.Context, result *gorm.DB, tenantID string, model any, id string) error {
	if result.Error != nil {
		return queryError(result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	var count int64
	if err := s.dbc(ctx).Model(model).Where("tenant_id = ? AND id = ?", tenantID, id).Count(&count).Error; err != nil {
		return queryError(err)
	}
	if count > 0 {
		return nil
	}
	return errx.Detail(cms.ErrNotFound, "row does not exist | 记录不存在")
}

// inChunks is kept small enough for every dialect's parameter ceiling. | inChunks 保持足够小，适配各数据库的参数上限。
const inChunks = 200

// chunks splits ids into bounded batches. | chunks 把 ID 切分成有界批次。
func chunks(ids []string) [][]string {
	if len(ids) == 0 {
		return nil
	}
	out := make([][]string, 0, (len(ids)+inChunks-1)/inChunks)
	for start := 0; start < len(ids); start += inChunks {
		end := start + inChunks
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[start:end])
	}
	return out
}

// likePattern escapes the ESCAPE '!' metacharacters and wraps the term in wildcards. |
// likePattern 转义 ESCAPE '!' 元字符并在词两侧加通配符。
func likePattern(term string) string {
	replacer := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return "%" + strings.ToLower(replacer.Replace(strings.TrimSpace(term))) + "%"
}

// likePrefix escapes LIKE metacharacters in a literal prefix and appends the trailing wildcard. It is
// used where the value itself may contain "_" or "%" (category paths, MIME types); passing such a
// value to LIKE verbatim would silently widen the match. The caller normalises case when the stored
// column does; paths are preserved exactly. |
// likePrefix 转义字面前缀中的 LIKE 元字符并追加尾随通配符。用于值本身可能含 "_" 或 "%" 的场景
// （分类路径、MIME 类型）；原样传给 LIKE 会静默扩大匹配范围。存储列需要忽略大小写时由调用方归一化，
// 路径则原样保留。
func likePrefix(prefix string) string {
	replacer := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return replacer.Replace(strings.TrimSpace(prefix)) + "%"
}

// onConflictDoNothing returns the portable clause used for idempotent inserts. | onConflictDoNothing 返回幂等插入使用的可移植子句。
func onConflictDoNothing() clause.OnConflict {
	return clause.OnConflict{DoNothing: true}
}

// newRowID generates a primary key of a join row. | newRowID 生成连接行主键。
func newRowID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	return prefix + hex.EncodeToString(raw[:])
}

// categoryMap loads categories by id inside one tenant. | categoryMap 在单个租户内按标识加载分类。
func (s *Store) categoryMap(ctx context.Context, tenantID string, ids []string) (map[string]cms.Category, error) {
	out := make(map[string]cms.Category, len(ids))
	for _, batch := range chunks(ids) {
		var rows []cms.Category
		if err := s.dbc(ctx).Where("tenant_id = ? AND id IN ?", tenantID, batch).Find(&rows).Error; err != nil {
			return nil, queryError(err)
		}
		for _, row := range rows {
			out[row.ID] = row
		}
	}
	return out, nil
}
