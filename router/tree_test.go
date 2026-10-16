package router

import (
	"errors"
	"net/http"
	"sort"
	"testing"
)

// find 是测试辅助：在树上查找并返回命中的处理器标签与参数表。
func find(t *testing.T, tr *Tree, method, path string) (string, map[string]string, bool, bool) {
	t.Helper()
	ps := acquireParams()
	defer releaseParams(ps)
	h, tsr := tr.Find(method, path, ps)
	if h == nil {
		return "", nil, tsr, false
	}
	params := make(map[string]string, ps.Len())
	ps.Each(func(k, v string) { params[k] = v })
	return h.(string), params, tsr, true
}

func mustAdd(t *testing.T, tr *Tree, method, pattern, handler string) {
	t.Helper()
	if err := tr.Add(method, pattern, handler); err != nil {
		t.Fatalf("Add(%s %s) 失败: %v", method, pattern, err)
	}
}

func TestTreeMixedSuffix(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/avatar/:id.jpg", "avatar")

	cases := []struct {
		path string
		id   string
		hit  bool
	}{
		{path: "/avatar/photo.jpg", id: "photo", hit: true},
		{path: "/avatar/my.photo.jpg", id: "my.photo", hit: true},
		{path: "/avatar/.jpg", hit: false}, // 参数必须非空
		{path: "/avatar/photo.png", hit: false},
		{path: "/avatar/a/b.jpg", hit: false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got, params, _, hit := find(t, tr, http.MethodGet, tc.path)
			if hit != tc.hit {
				t.Fatalf("命中 = %v, 期望 %v", hit, tc.hit)
			}
			if !hit {
				return
			}
			if got != "avatar" || params["id"] != tc.id {
				t.Fatalf("handler=%q id=%q, 期望 avatar/%q", got, params["id"], tc.id)
			}
		})
	}
}

func TestTreeMixedWithDashSuffix(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/report/:id-xx.png", "report")

	got, params, _, hit := find(t, tr, http.MethodGet, "/report/42-xx.png")
	if !hit || got != "report" || params["id"] != "42" {
		t.Fatalf("命中=%v handler=%q id=%q", hit, got, params["id"])
	}

	if _, _, _, hit := find(t, tr, http.MethodGet, "/report/42-yy.png"); hit {
		t.Fatal("/report/42-yy.png 不应命中")
	}

	// 最短优先下的最长可行前缀：静态后缀必须被剥离
	got, params, _, hit = find(t, tr, http.MethodGet, "/report/a-b-xx.png")
	if !hit || params["id"] != "a-b" {
		t.Fatalf("id 应为 a-b, 实际 %q (hit=%v handler=%q)", params["id"], hit, got)
	}
}

func TestTreeStaticBeatsMixedAndParam(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/:id-xx.png", "mixed")
	mustAdd(t, tr, http.MethodGet, "/abc-xx.png", "static")

	got, _, _, hit := find(t, tr, http.MethodGet, "/abc-xx.png")
	if !hit || got != "static" {
		t.Fatalf("静态段应优先命中，实际 %q (hit=%v)", got, hit)
	}
	got, params, _, hit := find(t, tr, http.MethodGet, "/zzz-xx.png")
	if !hit || got != "mixed" || params["id"] != "zzz" {
		t.Fatalf("混合段应命中 zzz，实际 %q id=%q hit=%v", got, params["id"], hit)
	}
}

func TestTreeMixedBeatsPureParam(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/:id", "param")
	mustAdd(t, tr, http.MethodGet, "/:id-xx.png", "mixed")

	got, params, _, hit := find(t, tr, http.MethodGet, "/a-xx.png")
	if !hit || got != "mixed" || params["id"] != "a" {
		t.Fatalf("含字面量锚点的混合段应优先于纯参数段，实际 %q id=%q", got, params["id"])
	}
	got, params, _, hit = find(t, tr, http.MethodGet, "/plain")
	if !hit || got != "param" || params["id"] != "plain" {
		t.Fatalf("纯参数段应兜底，实际 %q id=%q", got, params["id"])
	}
}

func TestTreeMultiParamSegment(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/archive/:y-:m-:d.html", "archive")

	got, params, _, hit := find(t, tr, http.MethodGet, "/archive/2026-10-16.html")
	if !hit || got != "archive" {
		t.Fatalf("未命中: hit=%v handler=%q", hit, got)
	}
	want := map[string]string{"y": "2026", "m": "10", "d": "16"}
	for k, v := range want {
		if params[k] != v {
			t.Errorf("参数 %s = %q, 期望 %q", k, params[k], v)
		}
	}
	if _, _, _, hit := find(t, tr, http.MethodGet, "/archive/2026-10.html"); hit {
		t.Fatal("段数不足不应命中")
	}
}

func TestTreeWildcardCatchAll(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/static/*filepath", "static")

	got, params, _, hit := find(t, tr, http.MethodGet, "/static/css/app.css")
	if !hit || got != "static" || params["filepath"] != "css/app.css" {
		t.Fatalf("通配捕获 = %q (hit=%v handler=%q)", params["filepath"], hit, got)
	}

	_, params, _, hit = find(t, tr, http.MethodGet, "/static/")
	if !hit || params["filepath"] != "" {
		t.Fatalf("空通配捕获 = %q (hit=%v)", params["filepath"], hit)
	}

	// /static 未命中，但补尾斜杠可命中 → tsr
	_, _, tsr, hit := find(t, tr, http.MethodGet, "/static")
	if hit || !tsr {
		t.Fatalf("/static 应触发尾斜杠重定向: hit=%v tsr=%v", hit, tsr)
	}
}

func TestTreePercentEncodedSegmentNotDecoded(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/avatar/:id.jpg", "avatar")

	_, params, _, hit := find(t, tr, http.MethodGet, "/avatar/a%2Fb.jpg")
	if !hit {
		t.Fatal("百分号编码路径应命中")
	}
	if params["id"] != "a%2Fb" {
		t.Fatalf("参数应保持原始编码 a%%2Fb, 实际 %q", params["id"])
	}
}

func TestTreeTrailingSlashRedirect(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/users/", "users-slash")

	got, _, _, hit := find(t, tr, http.MethodGet, "/users/")
	if !hit || got != "users-slash" {
		t.Fatalf("/users/ 应命中, hit=%v handler=%q", hit, got)
	}
	if _, _, tsr, hit := find(t, tr, http.MethodGet, "/users"); hit || !tsr {
		t.Fatalf("/users 应触发 tsr: hit=%v tsr=%v", hit, tsr)
	}

	tr2 := NewTree()
	mustAdd(t, tr2, http.MethodGet, "/profile", "profile")
	if _, _, tsr, hit := find(t, tr2, http.MethodGet, "/profile/"); hit || !tsr {
		t.Fatalf("/profile/ 应触发 tsr: hit=%v tsr=%v", hit, tsr)
	}
}

func TestTreeConflictAndDuplicateParam(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/users/:id", "get")

	if err := tr.Add(http.MethodGet, "/users/:id", "dup"); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复注册应返回 ErrConflict, 实际 %v", err)
	}
	if err := tr.Add(http.MethodPost, "/users/:id", "post"); err != nil {
		t.Fatalf("不同方法注册同一模式应成功: %v", err)
	}
	if err := tr.Add(http.MethodGet, "/users/:x/:x", "bad"); !errors.Is(err, ErrDuplicateParam) {
		t.Fatalf("重复参数名应返回 ErrDuplicateParam, 实际 %v", err)
	}
	if err := tr.Add(http.MethodGet, "nope", "bad"); !errors.Is(err, ErrBadPattern) {
		t.Fatalf("非法模式应返回 ErrBadPattern, 实际 %v", err)
	}
}

func TestTreeRootRoute(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/", "root")

	if got, _, _, hit := find(t, tr, http.MethodGet, "/"); !hit || got != "root" {
		t.Fatalf("根路由未命中: hit=%v handler=%q", hit, got)
	}
	if _, _, _, hit := find(t, tr, http.MethodGet, "/x"); hit {
		t.Fatal("/x 不应命中根路由")
	}
}

func TestTreeAllowedMethods(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/users", "get")
	mustAdd(t, tr, http.MethodPost, "/users", "post")
	mustAdd(t, tr, http.MethodDelete, "/users", "delete")

	got := tr.Allowed("/users")
	sort.Strings(got)
	want := []string{http.MethodDelete, http.MethodGet, http.MethodPost}
	if len(got) != len(want) {
		t.Fatalf("Allowed = %v, 期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Allowed = %v, 期望 %v", got, want)
		}
	}
	if m := tr.Allowed("/nope"); len(m) != 0 {
		t.Fatalf("未知路径 Allowed 应为空, 实际 %v", m)
	}
}

func TestTreeWalkIsStable(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/b", "b")
	mustAdd(t, tr, http.MethodGet, "/a", "a")
	mustAdd(t, tr, http.MethodPost, "/a", "a-post")

	type entry struct{ method, pattern, handler string }
	var first []entry
	tr.Walk(func(method, pattern string, h any) {
		first = append(first, entry{method, pattern, h.(string)})
	})
	if len(first) != 3 {
		t.Fatalf("Walk 条目数 = %d, 期望 3", len(first))
	}
	var second []entry
	tr.Walk(func(method, pattern string, h any) {
		second = append(second, entry{method, pattern, h.(string)})
	})
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("Walk 顺序不稳定: %v vs %v", first, second)
		}
	}
}

func TestTreeFindNoAlloc(t *testing.T) {
	tr := NewTree()
	mustAdd(t, tr, http.MethodGet, "/users/:id", "param")
	mustAdd(t, tr, http.MethodGet, "/static/*filepath", "wild")
	mustAdd(t, tr, http.MethodGet, "/plain/path", "static")

	ps := acquireParams()
	defer releaseParams(ps)

	cases := []string{"/users/42", "/static/css/app.css", "/plain/path"}
	for _, path := range cases {
		allocs := testing.AllocsPerRun(200, func() {
			ps.Reset()
			if _, _ = tr.Find(http.MethodGet, path, ps); false {
				t.Fatal("unreachable")
			}
		})
		if allocs != 0 {
			t.Errorf("Find(%q) 分配 = %v, 期望 0", path, allocs)
		}
	}
}
