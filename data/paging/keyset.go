// This file implements keyset pagination and its dialect-specific SQL. | 本文件实现键集分页及其方言相关 SQL。

package paging

import (
	"context"
	"strings"

	"github.com/shibingli/ydx/errs"
	"gorm.io/gorm"
)

// Dialect is one supported database dialect. | Dialect 是一种支持的数据库方言。
type Dialect string

// The dialects the keyset builder understands. | 键集构造器支持的方言。
const (
	// MySQL is MySQL or MariaDB. | MySQL 是 MySQL 或 MariaDB。
	MySQL Dialect = "mysql"
	// PostgreSQL is PostgreSQL. | PostgreSQL 是 PostgreSQL。
	PostgreSQL Dialect = "postgres"
	// SQLite is SQLite. | SQLite 是 SQLite。
	SQLite Dialect = "sqlite"
	// DM is 达梦 DM8. | DM 是达梦 DM8。
	DM Dialect = "dm"
	// Unknown is a dialect without a keyset strategy. | Unknown 是没有键集策略的方言。
	Unknown Dialect = ""
)

// DialectOf returns the dialect of db. | DialectOf 返回 db 的方言。
//
// Both the canonical driver names and the aliases the framework accepts are mapped, so a GORM handle
// opened through ydx/gorm and one opened directly report the same strategy. | 规范的驱动名与框架接受的
// 别名都会映射，因此经 ydx/gorm 打开的句柄与直接打开的句柄得到同一套策略。
func DialectOf(db *gorm.DB) Dialect {
	if db == nil || db.Config == nil || db.Dialector == nil {
		return Unknown
	}
	switch strings.ToLower(strings.TrimSpace(db.Dialector.Name())) {
	case "mysql":
		return MySQL
	case "postgres", "postgresql":
		return PostgreSQL
	case "sqlite", "sqlite3":
		return SQLite
	case "dm", "dameng":
		return DM
	default:
		return Unknown
	}
}

// Keyset describes a deterministic sort order used for keyset pagination. | Keyset 描述键集分页使用的
// 确定性排序。
//
// Sort is the ordered column and ID is the unique tie-breaker. They may be the same column, in which case
// the predicate collapses to a single comparison. Both are plain identifiers, optionally qualified by a
// table name (for example "m.created_at"); values are validated and quoted by the dialect, never accepted
// from user input. | Sort 是排序列，ID 是唯一决胜列。两者可以相同，此时谓词退化为单次比较。两者都是纯标识符，
// 可带表名限定（例如 "m.created_at"）；取值经过校验并按方言引用，绝不直接采用用户输入。
type Keyset struct {
	// Sort is the ordered column. | Sort 是排序列。
	Sort string
	// ID is the unique tie-breaker column. | ID 是唯一决胜列。
	ID string
	// Desc reverses both the order and the cursor comparison. | Desc 同时反转排序与游标比较方向。
	Desc bool
}

// reversed returns the same keyset with the opposite direction. | reversed 返回方向相反的同一键集。
func (k Keyset) reversed() Keyset {
	k.Desc = !k.Desc
	return k
}

// validate rejects an empty, duplicated or unsafe identifier. | validate 拒绝为空、重复或不安全的标识符。
func (k Keyset) validate() error {
	if strings.TrimSpace(k.Sort) == "" || strings.TrimSpace(k.ID) == "" {
		return errs.Detail(ErrIdentifier, "sort and id are required | 排序列与决胜列不能为空")
	}
	return nil
}

// Order returns the ORDER BY clause for dialect d. | Order 返回方言 d 的 ORDER BY 子句。
func (k Keyset) Order(d Dialect) (string, error) {
	if err := k.validate(); err != nil {
		return "", err
	}
	sortColumn, err := quoteIdentifier(d, k.Sort)
	if err != nil {
		return "", err
	}
	direction := "ASC"
	if k.Desc {
		direction = "DESC"
	}
	if strings.TrimSpace(k.Sort) == strings.TrimSpace(k.ID) {
		return sortColumn + " " + direction, nil
	}
	idColumn, err := quoteIdentifier(d, k.ID)
	if err != nil {
		return "", err
	}
	return sortColumn + " " + direction + ", " + idColumn + " " + direction, nil
}

// Apply narrows query to rows strictly after cursor in the keyset order. | Apply 把 query 收窄到键集顺序中
// 严格位于 cursor 之后的记录。
//
// MySQL, PostgreSQL and SQLite use a row-value comparison, which the optimizer can turn into a single
// index range. DM lacks that form, so it receives the equivalent OR expansion; both are index-friendly
// with a composite index on (sort, id). | MySQL、PostgreSQL 与 SQLite 使用行值比较，优化器可把它变成
// 单段索引范围。DM 没有该形式，因此使用等价的 OR 展开；在 (sort, id) 复合索引上两种形式都能命中索引。
func (k Keyset) Apply(query *gorm.DB, cursor Cursor) (*gorm.DB, error) {
	if query == nil {
		return nil, errs.Detail(ErrQuery, "nil query | 查询为空")
	}
	dialect := DialectOf(query)
	if dialect == Unknown {
		return nil, errs.Detail(ErrDialect, "dialect %q has no keyset strategy | 方言 %q 没有键集策略", query.Dialector.Name(), query.Dialector.Name())
	}
	sql, args, err := k.Predicate(dialect, cursor)
	if err != nil {
		return nil, err
	}
	return query.Where(sql, args...), nil
}

// Predicate returns the keyset WHERE fragment and its arguments for dialect d. | Predicate 返回方言 d 的
// 键集 WHERE 片段及参数。
func (k Keyset) Predicate(d Dialect, cursor Cursor) (string, []any, error) {
	if err := k.validate(); err != nil {
		return "", nil, err
	}
	sortColumn, err := quoteIdentifier(d, k.Sort)
	if err != nil {
		return "", nil, err
	}
	idColumn := sortColumn
	if strings.TrimSpace(k.Sort) != strings.TrimSpace(k.ID) {
		idColumn, err = quoteIdentifier(d, k.ID)
		if err != nil {
			return "", nil, err
		}
	}
	value, err := cursor.SortValue()
	if err != nil {
		return "", nil, err
	}
	operator := ">"
	if k.Desc {
		operator = "<"
	}
	if strings.TrimSpace(k.Sort) == strings.TrimSpace(k.ID) {
		return sortColumn + " " + operator + " ?", []any{value}, nil
	}
	tieBreaker, err := cursor.TieBreaker()
	if err != nil {
		return "", nil, err
	}
	if d.tupleComparison() {
		return "(" + sortColumn + ", " + idColumn + ") " + operator + " (?, ?)", []any{value, tieBreaker}, nil
	}
	return "(" + sortColumn + " " + operator + " ? OR (" + sortColumn + " = ? AND " + idColumn + " " + operator + " ?))",
		[]any{value, value, tieBreaker}, nil
}

// tupleComparison reports whether the dialect supports row-value comparison. | tupleComparison 报告方言是否
// 支持行值比较。
func (d Dialect) tupleComparison() bool {
	return d == MySQL || d == PostgreSQL || d == SQLite
}

// quoteIdentifier validates a possibly table-qualified identifier and quotes each segment for d. | quoteIdentifier
// 校验可带表名限定的标识符，并按方言 d 逐段引用。
func quoteIdentifier(d Dialect, identifier string) (string, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return "", errs.Detail(ErrIdentifier, "empty identifier | 标识符为空")
	}
	parts := strings.Split(identifier, ".")
	quote := `"`
	if d == MySQL {
		quote = "`"
	}
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		if !validIdentifier(part) {
			return "", errs.Detail(ErrIdentifier, "unsafe identifier %q | 不安全的标识符 %q", identifier, identifier)
		}
		quoted = append(quoted, quote+part+quote)
	}
	return strings.Join(quoted, "."), nil
}

// validIdentifier accepts the ASCII subset every supported database agrees on. | validIdentifier 只接受所有
// 支持的数据库都认可的 ASCII 子集。
func validIdentifier(part string) bool {
	if part == "" {
		return false
	}
	for i := 0; i < len(part); i++ {
		c := part[i]
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			continue
		}
		if i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

// CursorFunc extracts the cursor of one row. | CursorFunc 提取一行的游标。
type CursorFunc[T any] func(T) (Cursor, error)

// KeysetPage reads one page around the request cursor. | KeysetPage 读取请求游标附近的一页。
//
// After reads the next page and Before reads the previous one; the result is always returned in the
// keyset's natural order, so a caller never has to reverse a page itself. The function fetches one extra
// row to detect whether another page exists, and never runs COUNT. | After 读取下一页，Before 读取上一页；
// 结果始终按键集的自然顺序返回，调用方无需自行反转。函数多取一行判断是否还有下一页，且从不执行 COUNT。
func KeysetPage[T any](ctx context.Context, query *gorm.DB, keyset Keyset, req Request, cursorOf CursorFunc[T]) (Page[T], error) {
	req = req.Normalize()
	page := Page[T]{Total: -1, Page: req.Page, Size: req.Size}
	if query == nil {
		return page, errs.Detail(ErrQuery, "nil query | 查询为空")
	}
	if cursorOf == nil {
		return page, errs.Detail(ErrCursor, "missing cursor extractor | 缺少游标提取函数")
	}
	dialect := DialectOf(query)
	if dialect == Unknown {
		return page, errs.Detail(ErrDialect, "dialect %q has no keyset strategy | 方言 %q 没有键集策略", query.Dialector.Name(), query.Dialector.Name())
	}
	if req.After != "" && req.Before != "" {
		return page, errs.Detail(ErrCursor, "after and before are mutually exclusive | after 与 before 互斥")
	}

	var (
		cursor   Cursor
		hasInput bool
		reverse  bool
		err      error
	)
	switch {
	case req.Before != "":
		cursor, err = Decode(req.Before)
		hasInput, reverse = true, true
	case req.After != "":
		cursor, err = Decode(req.After)
		hasInput = true
	}
	if err != nil {
		return page, err
	}
	if hasInput {
		keyset = keyset.reversedFor(reverse)
	}
	if hasInput {
		query, err = keyset.Apply(query.WithContext(ctx), cursor)
		if err != nil {
			return page, err
		}
	} else {
		query = query.WithContext(ctx)
	}
	order, err := keyset.Order(dialect)
	if err != nil {
		return page, err
	}
	var items []T
	if err := query.Order(order).Limit(req.Size + 1).Find(&items).Error; err != nil {
		return page, errs.Wrap(err, ErrQuery)
	}
	hasMore := len(items) > req.Size
	if hasMore {
		items = items[:req.Size]
	}
	if reverse {
		reverseSlice(items)
	}
	page.Items = items
	page.HasPrev = hasInput
	page.HasNext = hasMore
	if reverse {
		page.HasPrev, page.HasNext = hasMore, hasInput
	}
	if len(items) > 0 {
		if page.HasNext {
			next, err := cursorOf(items[len(items)-1])
			if err != nil {
				return page, err
			}
			if page.After, err = next.Encode(); err != nil {
				return page, err
			}
		}
		if page.HasPrev {
			previous, err := cursorOf(items[0])
			if err != nil {
				return page, err
			}
			if page.Before, err = previous.Encode(); err != nil {
				return page, err
			}
		}
	}
	return page, nil
}

// reversedFor returns the keyset direction for the requested cursor. A Before request walks backwards,
// so the order must be reversed; the page is flipped back after reading. | reversedFor 返回请求游标对应的
// 键集方向。Before 请求反向行走，因此排序必须反转，读取后再把页翻回自然顺序。
func (k Keyset) reversedFor(before bool) Keyset {
	if before {
		return k.reversed()
	}
	return k
}

// reverseSlice reverses items in place. | reverseSlice 原地反转 items。
func reverseSlice[T any](items []T) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}
