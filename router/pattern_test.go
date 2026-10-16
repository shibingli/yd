package router

import (
	"errors"
	"testing"
)

func TestParsePatternKinds(t *testing.T) {
	cases := []struct {
		pattern string
		kinds   []SegmentKind
		wantErr error
	}{
		{pattern: "/", kinds: nil},
		{pattern: "/users", kinds: []SegmentKind{SegStatic}},
		{pattern: "/users/:id", kinds: []SegmentKind{SegStatic, SegParam}},
		{pattern: "/avatar/:id.jpg", kinds: []SegmentKind{SegStatic, SegMixed}},
		{pattern: "/report/:id-xx.png", kinds: []SegmentKind{SegStatic, SegMixed}},
		{pattern: "/static/*filepath", kinds: []SegmentKind{SegStatic, SegWildcard}},
		{pattern: "/a/:b/c", kinds: []SegmentKind{SegStatic, SegParam, SegStatic}},
		{pattern: "/users/", kinds: []SegmentKind{SegStatic, SegStatic}},
		{pattern: "users", wantErr: ErrBadPattern},
		{pattern: "", wantErr: ErrBadPattern},
		{pattern: "//a", wantErr: ErrBadPattern},
		{pattern: "/a//b", wantErr: ErrBadPattern},
		{pattern: "/a/*x/b", wantErr: ErrWildcardPosition},
		{pattern: "/a/:1bad", wantErr: ErrBadPattern},
		{pattern: "/a/:x/:x", wantErr: ErrDuplicateParam},
		{pattern: "/a/:id.jpg-:id", wantErr: ErrDuplicateParam},
		{pattern: "/a/:*", wantErr: ErrBadPattern},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			segs, err := ParsePattern(tc.pattern)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ParsePattern(%q) err = %v, 期望 %v", tc.pattern, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePattern(%q) 意外错误: %v", tc.pattern, err)
			}
			if len(segs) != len(tc.kinds) {
				t.Fatalf("ParsePattern(%q) 段数 = %d, 期望 %d", tc.pattern, len(segs), len(tc.kinds))
			}
			for i, k := range tc.kinds {
				if segs[i].Kind != k {
					t.Errorf("段 %d 类型 = %v, 期望 %v", i, segs[i].Kind, k)
				}
			}
		})
	}
}

func TestParsePatternTrailingSlashMarker(t *testing.T) {
	segs, err := ParsePattern("/users/")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	last := segs[len(segs)-1]
	if !last.TrailingSlash {
		t.Error("末尾空段必须标记 TrailingSlash")
	}
	segs2, err := ParsePattern("/users")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if segs2[len(segs2)-1].TrailingSlash {
		t.Error("/users 不应带 TrailingSlash 标记")
	}
}

func TestParsePatternEscape(t *testing.T) {
	segs, err := ParsePattern(`/a\:b`)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if segs[0].Kind != SegStatic || segs[0].Raw != `a\:b` {
		t.Fatalf("转义段解析错误: %+v", segs[0])
	}
	params, ok := matchSegment(&segs[0], "a:b")
	if !ok || len(params) != 0 {
		t.Fatalf("转义冒号应匹配字面量 a:b，得到 ok=%v params=%v", ok, params)
	}
}

func TestMatchSegment(t *testing.T) {
	cases := []struct {
		pattern string
		value   string
		want    map[string]string
		ok      bool
	}{
		{pattern: "/:id.jpg", value: "avatar.jpg", want: map[string]string{"id": "avatar"}, ok: true},
		{pattern: "/:id.jpg", value: ".jpg", ok: false},
		{pattern: "/:id.jpg", value: "avatar.png", ok: false},
		{pattern: "/:id-xx.png", value: "42-xx.png", want: map[string]string{"id": "42"}, ok: true},
		{pattern: "/:id-xx.png", value: "a-b-xx.png", want: map[string]string{"id": "a-b"}, ok: true},
		{pattern: "/:id-xx.png", value: "42-yy.png", ok: false},
		{pattern: "/:id-xx.png", value: "-xx.png", ok: false},
		{pattern: "/file-:name.txt", value: "file-readme.txt", want: map[string]string{"name": "readme"}, ok: true},
		{pattern: "/:y-:m-:d.html", value: "2026-10-16.html", want: map[string]string{"y": "2026", "m": "10", "d": "16"}, ok: true},
		{pattern: "/:y-:m-:d.html", value: "2026-10.html", ok: false},
		{pattern: "/:a.:b", value: "x.y", want: map[string]string{"a": "x", "b": "y"}, ok: true},
		// 最短优先（左到右非贪婪）：a 先取最短可行前缀
		{pattern: "/:a.:b", value: "x.y.z", want: map[string]string{"a": "x", "b": "y.z"}, ok: true},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"_"+tc.value, func(t *testing.T) {
			segs, err := ParsePattern(tc.pattern)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			params, ok := matchSegment(&segs[len(segs)-1], tc.value)
			if ok != tc.ok {
				t.Fatalf("匹配结果 ok = %v, 期望 %v (params=%v)", ok, tc.ok, params)
			}
			if !ok {
				return
			}
			got := map[string]string{}
			for _, p := range params {
				got[p.Key] = p.Value
			}
			if len(got) != len(tc.want) {
				t.Fatalf("参数个数 = %d, 期望 %d (%v)", len(got), len(tc.want), got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("参数 %s = %q, 期望 %q", k, got[k], v)
				}
			}
		})
	}
}

func TestMatchSegmentMillionCharTerminates(t *testing.T) {
	// 病态输入：单段极长且反复回溯，必须快速终止（记忆化保证多项式复杂度）
	segs, err := ParsePattern("/:a-:b-:c-end")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	long := make([]byte, 0, 3000)
	for range 3000 {
		long = append(long, 'x')
	}
	_, _ = matchSegment(&segs[0], string(long))
}
