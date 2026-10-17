package authz

import "testing"

// TestDecisionKeyIsUnambiguous 要求不同判定集合绝不会得到同一个缓存键。
//
// Role names are only required to be non-empty, so a name may legally contain any separator byte a
// naive encoding would use. Length prefixes make the encoding injective, which keeps the template
// cache from handing one subject another subject's decisions. | 角色名只要求非空，因此可以合法包含
// 朴素编码所用的任何分隔字节。长度前缀使编码成为单射，模板缓存因而不会把一个主体的判定结果交给
// 另一个主体。
func TestDecisionKeyIsUnambiguous(t *testing.T) {
	tests := []struct {
		name string
		a    Envelope
		b    Envelope
	}{
		{
			name: "unit separator inside a role name",
			a:    Envelope{Mine: []string{"a\x1fb"}},
			b:    Envelope{Mine: []string{"a", "b"}},
		},
		{
			name: "record separator inside a role name",
			a:    Envelope{Mine: []string{"a\x1eb"}},
			b:    Envelope{Mine: []string{"a", "b"}},
		},
		{
			name: "pipe inside a role name",
			a:    Envelope{Mine: []string{"a|b"}},
			b:    Envelope{Mine: []string{"a", "b"}},
		},
		{
			name: "same name on the granted and the role side",
			a:    Envelope{Granted: []string{"x"}},
			b:    Envelope{Mine: []string{"x"}},
		},
	}
	for _, tt := range tests {
		if decisionKey(tt.a) == decisionKey(tt.b) {
			t.Errorf("%s: distinct decision sets share one key | %s：不同判定集合共用了同一个键", tt.name, tt.name)
		}
	}
}

// TestDecisionKeyStableAndAnonymous 要求同一集合的键稳定，且匿名主体的键为空串。 | TestDecisionKeyStableAndAnonymous requires a stable key for equal sets and an empty key for an anonymous subject.
func TestDecisionKeyStableAndAnonymous(t *testing.T) {
	a := decisionKey(Envelope{Granted: []string{"p1", "p2"}, Mine: []string{"admin"}})
	b := decisionKey(Envelope{Granted: []string{"p1", "p2"}, Mine: []string{"admin"}})
	if a == "" || a != b {
		t.Fatalf("key = %q, second = %q | 键 = %q，第二次 = %q", a, b, a, b)
	}
	if got := decisionKey(Envelope{}); got != "" {
		t.Fatalf("anonymous key = %q, want empty | 匿名键 = %q，期望空串", got, got)
	}
}
