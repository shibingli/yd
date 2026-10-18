// Package templatebench compares mainstream Go template engines on a shared
// workload and checks their escaping and composition capabilities.
// | Package templatebench 在共享工作负载上比较主流 Go 模板引擎的性能，并检查其转义与组合能力。
package templatebench

import (
	"fmt"
	"strconv"
)

// User is the page owner. | User 是页面所属用户。
type User struct {
	Name  string
	Email string
	Class string
}

// Item is one row of the rendered list. | Item 是渲染列表中的一行。
type Item struct {
	Name   string
	Email  string
	URL    string
	Class  string
	Note   string
	ID     int
	Active bool
}

// Page is the shared benchmark payload. | Page 是共享基准载荷。
type Page struct {
	Title string
	Lang  string
	Query string
	User  User
	Items []Item
	Total int
}

// NumberOfItems is the row count in the page workload. | NumberOfItems 是页面负载的行数。
const NumberOfItems = 100

// Fixture builds the deterministic page payload. | Fixture 构造确定性的页面载荷。
//
// Query, Note and Name carry characters that every contextual escaper must
// neutralise, so the benchmark measures escaping as well as loop overhead. | Query、Note
// 与 Name 带有各上下文转义器必须中和的字符，因此基准同时衡量转义与循环开销。
func Fixture() *Page {
	items := make([]Item, NumberOfItems)
	for i := range items {
		items[i] = Item{
			ID:     i + 1,
			Name:   fmt.Sprintf("User %d <tag>", i+1),
			Email:  fmt.Sprintf("user%d@example.com", i+1),
			URL:    "https://example.com/profile/" + strconv.Itoa(i+1),
			Class:  "row row-odd",
			Note:   `"quoted" & <tag>`,
			Active: i%3 != 0,
		}
	}
	return &Page{
		Title: "Template benchmark <page>",
		Lang:  "en",
		Query: "a&b<c",
		User:  User{Name: "Ada <admin>", Email: "ada@example.com", Class: "owner"},
		Items: items,
		Total: len(items),
	}
}

// MapFixture returns the same payload as a map, for engines that address data
// by map key rather than by struct field. | MapFixture 以映射返回同一载荷，供按键
// 寻址而非按结构体字段寻址的引擎使用。
func MapFixture() map[string]any { return MapOf(Fixture()) }

// MapOf renders any page as the map shape used by key-addressed engines. | MapOf 把任意页面渲染为按键寻址引擎使用的映射形状。
func MapOf(p *Page) map[string]any {
	items := make([]map[string]any, len(p.Items))
	for i, it := range p.Items {
		items[i] = map[string]any{
			"ID": it.ID, "Id": it.ID, "id": it.ID,
			"Name": it.Name, "name": it.Name,
			"Email": it.Email, "email": it.Email,
			"URL": it.URL, "Url": it.URL, "url": it.URL,
			"Class": it.Class, "class": it.Class,
			"Note": it.Note, "note": it.Note,
			"Active": it.Active, "active": it.Active,
		}
	}
	return map[string]any{
		"Title": p.Title, "title": p.Title,
		"Lang": p.Lang, "lang": p.Lang,
		"Query": p.Query, "query": p.Query,
		"User":  map[string]any{"Name": p.User.Name, "name": p.User.Name, "Email": p.User.Email, "email": p.User.Email, "Class": p.User.Class, "class": p.User.Class},
		"Items": items, "items": items,
		"Total": p.Total, "total": p.Total,
	}
}

// SmallFixture is the short workload used by the small-page benchmark. | SmallFixture 是小页面基准使用的短负载。
func SmallFixture() *Page {
	p := Fixture()
	p.Items = p.Items[:5]
	p.Total = len(p.Items)
	return p
}
