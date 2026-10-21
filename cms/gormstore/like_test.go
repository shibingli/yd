package gormstore

import "testing"

// TestLikePrefixEscapesMetaCharacters proves a literal prefix cannot act as a LIKE pattern. Category
// paths contain underscores and MIME filters may contain "%", both of which would otherwise widen the
// match silently. | TestLikePrefixEscapesMetaCharacters 证明字面前缀不会被当作 LIKE 模式。分类路径含
// 下划线、MIME 过滤值可能含 "%"，不转义会静默扩大匹配范围。
func TestLikePrefixEscapesMetaCharacters(t *testing.T) {
	cases := []struct {
		prefix string
		want   string
	}{
		{"/cat_abc/", "/cat!_abc/%"},
		{"image/%", "image/!%%"},
		{"a!b", "a!!b%"},
		{"/Cat_ID/", "/Cat!_ID/%"},
	}
	for _, tc := range cases {
		if got := likePrefix(tc.prefix); got != tc.want {
			t.Errorf("likePrefix(%q) = %q, want %q", tc.prefix, got, tc.want)
		}
	}
}
