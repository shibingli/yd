// Package paging provides dialect-aware offset and keyset pagination for GORM queries. | package paging 为
// GORM 查询提供方言感知的偏移分页与键集分页。
//
// The two strategies have different jobs: | 两种策略职责不同：
//
//   - offset pagination runs COUNT plus LIMIT/OFFSET and supports arbitrary page jumps, which admin tables
//     need; it becomes slower as the offset grows because the database still walks every skipped row; |
//     偏移分页执行 COUNT 与 LIMIT/OFFSET，支持任意页码跳转，这是管理表格需要的；偏移量增大后数据库仍要
//     走过被跳过的每一行，因此会变慢；
//   - keyset pagination reads a page after (or before) an opaque cursor and never counts, so the cost stays
//     proportional to the page length and page contents do not shift when rows are inserted. It is the right
//     choice for message histories, audit trails and other append-only lists. | 键集分页从不可变游标之后
//     （或之前）读取一页且从不计数，因此开销与页长成正比，插入新行时页内容也不会漂移。它适合消息历史、
//     审计轨迹等只追加的列表。
//
// Both strategies share the same request and page types, so a handler can switch between them without
// changing its rendering contract. | 两种策略共用同一套请求与页结果类型，处理器切换策略时无需改变渲染契约。
package paging

import (
	"context"
	"strings"

	"github.com/shibingli/ydx/errs"
	"gorm.io/gorm"
)

// The package sentinel errors. | 包级哨兵错误。
var (
	// ErrCursor reports a malformed, oversized or unsupported cursor. | ErrCursor 表示游标格式错误、超长或类型不受支持。
	ErrCursor = errs.New("paging: invalid cursor | 分页：游标不合法")
	// ErrIdentifier reports a sort column or tie-breaker that is not a plain SQL identifier. | ErrIdentifier
	// 表示排序列或决胜列不是纯 SQL 标识符。
	ErrIdentifier = errs.New("paging: invalid identifier | 分页：标识符不合法")
	// ErrDialect reports an unsupported database dialect for keyset paging. | ErrDialect 表示键集分页不支持该数据库方言。
	ErrDialect = errs.New("paging: unsupported dialect | 分页：不支持的数据库方言")
	// ErrQuery reports a failed pagination query. | ErrQuery 表示分页查询失败。
	ErrQuery = errs.New("paging: query failed | 分页：查询失败")
)

// The default and maximum page sizes shared by every strategy. | 两种策略共用的默认页长与最大页长。
const (
	// DefaultSize is used when a request carries no size. | DefaultSize 是请求未给出页长时的取值。
	DefaultSize = 20
	// MaxSize is the upper bound accepted from a request. | MaxSize 是请求允许的最大页长。
	MaxSize = 100
)

// Request is one pagination request. | Request 是一次分页请求。
//
// After and Before are mutually exclusive opaque cursors. When either is set the request is a keyset
// request; otherwise it is an offset request. WithTotal asks the offset strategy for a COUNT; keyset
// pagination deliberately never counts. | After 与 Before 是互斥的不可变游标。任一非空即为键集请求，
// 否则是偏移请求。WithTotal 要求偏移策略执行 COUNT；键集分页刻意从不计数。
type Request struct {
	// After continues forward from a cursor. | After 从游标向后继续。
	After string
	// Before reads the page before a cursor. | Before 读取游标之前的一页。
	Before string
	// Page is the 1-based page number of an offset request. | Page 是偏移请求从 1 开始的页码。
	Page int
	// Size is the requested page length. | Size 是请求的页长。
	Size int
	// WithTotal asks the offset strategy to count the matching rows. | WithTotal 要求偏移策略统计匹配行数。
	WithTotal bool
}

// Normalize clamps the request into the shared range. | Normalize 把请求收敛到共用区间。
func (r Request) Normalize() Request {
	if r.Size <= 0 {
		r.Size = DefaultSize
	}
	if r.Size > MaxSize {
		r.Size = MaxSize
	}
	if r.Page < 1 {
		r.Page = 1
	}
	r.After = strings.TrimSpace(r.After)
	r.Before = strings.TrimSpace(r.Before)
	if r.After != "" && r.Before != "" {
		r.Before = ""
	}
	return r
}

// CursorMode reports whether the request asks for keyset pagination. | CursorMode 报告请求是否要求键集分页。
func (r Request) CursorMode() bool { return r.After != "" || r.Before != "" }

// Page is one page of rows. | Page 是一页记录。
//
// Total is -1 when no COUNT ran (the default for keyset pagination and for offset requests without
// WithTotal), so a caller can distinguish "unknown" from "zero". | 未执行 COUNT 时 Total 为 -1
// （键集分页与未要求计数的偏移请求都是如此），调用方因此能区分「未知」与「零」。
type Page[T any] struct {
	// After is the cursor for the next page; empty means this is the last page. | After 是下一页游标；为空表示已到末页。
	After string
	// Before is the cursor for the previous page; empty means this is the first page. | Before 是上一页游标；为空表示已在首页。
	Before string
	// Items holds the rows in display order. | Items 按展示顺序保存本页记录。
	Items []T
	// Total is the matching row count, or -1 when it was not requested. | Total 是匹配行数，未请求时为 -1。
	Total int64
	// Page is the echoed page number. | Page 回显页码。
	Page int
	// Size is the echoed page length. | Size 回显页长。
	Size int
	// HasPrev reports whether a previous page exists. | HasPrev 报告是否存在上一页。
	HasPrev bool
	// HasNext reports whether a further page exists. | HasNext 报告是否存在下一页。
	HasNext bool
	// HasTotal reports whether Total was counted. | HasTotal 报告 Total 是否经过统计。
	HasTotal bool
}

// OffsetPage reads one LIMIT/OFFSET page from query. | OffsetPage 从 query 读取一页 LIMIT/OFFSET 结果。
//
// The caller owns the ORDER BY because different tables need different sort columns; this function only
// adds the page window. A caller that needs the total must set Request.WithTotal, which runs an extra
// COUNT against a cloned statement. The function fetches one extra row to detect a next page without a
// count, so the page length never causes a false "next". | 排序由调用方负责，因为不同表使用不同排序列；
// 本函数只添加页窗口。需要总数的调用方要设置 Request.WithTotal，它会针对克隆语句额外执行一次 COUNT。
// 函数多取一行以在无计数时判断下一页，因此页长边界不会产生虚假的「下一页」。
func OffsetPage[T any](ctx context.Context, query *gorm.DB, req Request) (Page[T], error) {
	req = req.Normalize()
	page := Page[T]{Total: -1, Page: req.Page, Size: req.Size, HasPrev: req.Page > 1}
	if query == nil {
		return page, errs.Detail(ErrQuery, "nil query | 查询为空")
	}
	if req.WithTotal {
		var total int64
		if err := query.Session(&gorm.Session{}).WithContext(ctx).Count(&total).Error; err != nil {
			return page, errs.Wrap(err, ErrQuery)
		}
		page.Total, page.HasTotal = total, true
	}
	offset := (req.Page - 1) * req.Size
	if offset < 0 {
		offset = 0
	}
	var items []T
	if err := query.WithContext(ctx).Offset(offset).Limit(req.Size + 1).Find(&items).Error; err != nil {
		return page, errs.Wrap(err, ErrQuery)
	}
	if len(items) > req.Size {
		items = items[:req.Size]
		page.HasNext = true
	}
	page.Items = items
	if page.HasTotal {
		page.HasNext = int64(req.Page)*int64(req.Size) < page.Total
	}
	return page, nil
}

// PageCount returns the number of offset pages for total and size, at least one. | PageCount 返回 total 与
// size 对应的页数，至少为一页。
func PageCount(total int64, size int) int {
	if size <= 0 {
		size = DefaultSize
	}
	if total <= 0 {
		return 1
	}
	return int((total + int64(size) - 1) / int64(size))
}

// Bounds normalizes page and size against total and returns the zero-based slice window. | Bounds 依据 total
// 归一化页码与页长，并返回从零开始的切片窗口。
func Bounds(total, page, size int) (current, start, end int) {
	if size <= 0 {
		size = DefaultSize
	}
	count := PageCount(int64(total), size)
	if page < 1 {
		page = 1
	}
	if page > count {
		page = count
	}
	start = (page - 1) * size
	end = start + size
	if end > total {
		end = total
	}
	return page, start, end
}

// Link is one numbered pager item. | Link 是一个页码分页项。
type Link struct {
	// Number is the page number; zero on an ellipsis. | Number 是页码；省略号项为零。
	Number int
	// Current marks the active page. | Current 标记当前页。
	Current bool
	// Ellipsis marks a gap between page numbers. | Ellipsis 标记页码之间的省略。
	Ellipsis bool
}

// Window builds the compact pager window: first, last and one page on either side of the current one,
// with ellipses between the ranges. It returns nil when there is only one page. | Window 构造紧凑的分页窗口：
// 首页、末页与当前页前后各一页，范围之间以省略号连接；只有一页时返回 nil。
func Window(page, pageCount, radius int) []Link {
	if pageCount <= 1 {
		return nil
	}
	if radius < 1 {
		radius = 1
	}
	if page < 1 {
		page = 1
	}
	if page > pageCount {
		page = pageCount
	}
	links := make([]Link, 0, 2*radius+5)
	last := 0
	for n := 1; n <= pageCount; n++ {
		if n != 1 && n != pageCount && (n < page-radius || n > page+radius) {
			continue
		}
		if last > 0 && n-last > 1 {
			links = append(links, Link{Ellipsis: true})
		}
		links = append(links, Link{Number: n, Current: n == page})
		last = n
	}
	return links
}
