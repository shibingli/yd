package gormstore

import "testing"

// TestLikePatternUsesBangEscape keeps the pattern and every query's ESCAPE '!' clause in sync. |
// TestLikePatternUsesBangEscape 让模式与所有查询的 ESCAPE '!' 子句保持一致。
func TestLikePatternUsesBangEscape(t *testing.T) {
	cases := map[string]string{
		"50%":     "%50!%%",
		"a_b":     "%a!_b%",
		"wow!":    "%wow!!%",
		" Hello ": "%hello%",
	}
	for input, want := range cases {
		if got := likePattern(input); got != want {
			t.Errorf("likePattern(%q) = %q, want %q", input, got, want)
		}
	}
}
