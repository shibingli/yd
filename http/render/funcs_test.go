package render

import (
	"errors"
	"strings"
	"testing"
)

// TestHtmxAttrsEscapesValues pins the helper's safety contract: the result is a
// template.HTMLAttr inserted verbatim, so a dynamic value containing a quote
// must not be able to break out of its attribute. | TestHtmxAttrsEscapesValues
// 固化该辅助函数的安全契约：结果是原样插入的 template.HTMLAttr，因此含引号的动态值
// 不得逃逸出所在属性。
func TestHtmxAttrsEscapesValues(t *testing.T) {
	tests := []struct {
		name string
		kv   []any
		want string
	}{
		{
			name: "prefix and order",
			kv:   []any{"target", "#x", "get", "/y"},
			want: `hx-get="/y" hx-target="#x"`,
		},
		{
			name: "quote injection",
			kv:   []any{"get", `" onmouseover="alert(1)`},
			want: `hx-get="&#34; onmouseover=&#34;alert(1)"`,
		},
		{
			name: "angle and ampersand",
			kv:   []any{"vals", `<x>&"`},
			want: `hx-vals="&lt;x&gt;&amp;&#34;"`,
		},
		{
			name: "non-string value",
			kv:   []any{"swap", 3},
			want: `hx-swap="3"`,
		},
		{
			name: "colon modifier",
			kv:   []any{"on:click", "doIt()"},
			want: `hx-on:click="doIt()"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := htmxAttrs(tc.kv...)
			if err != nil {
				t.Fatalf("htmxAttrs(%v) 失败: %v", tc.kv, err)
			}
			if string(got) != tc.want {
				t.Errorf("htmxAttrs(%v) = %q, 期望 %q", tc.kv, got, tc.want)
			}
		})
	}
}

// TestHtmxAttrsRejectsBadInput covers the failure paths. | TestHtmxAttrsRejectsBadInput 覆盖失败路径。
func TestHtmxAttrsRejectsBadInput(t *testing.T) {
	if _, err := htmxAttrs("get"); !errors.Is(err, ErrHtmxOddArgs) {
		t.Errorf("奇数参数错误 = %v, 期望 ErrHtmxOddArgs", err)
	}
	if _, err := htmxAttrs(1, "x"); !errors.Is(err, ErrHtmxKeyNotString) {
		t.Errorf("非字符串键错误 = %v, 期望 ErrHtmxKeyNotString", err)
	}
	for _, key := range []string{`a"b`, "a b", "a=b", "a/b", "a<b", ""} {
		_, err := htmxAttrs(key, "x")
		if !errors.Is(err, ErrHtmxInvalidKey) {
			t.Errorf("键 %q 错误 = %v, 期望 ErrHtmxInvalidKey", key, err)
		}
		if err != nil && !strings.Contains(err.Error(), "hx") {
			t.Errorf("键 %q 错误文案缺少上下文: %v", key, err)
		}
	}
}
