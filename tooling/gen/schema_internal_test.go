package gen

import "testing"

// TestGoTypeForDBMatchesExactNames proves POINT/INTERVAL are not mistaken for integers by a
// substring match. | TestGoTypeForDBMatchesExactNames 证明 POINT/INTERVAL 不会因子串匹配被误判为整数。
func TestGoTypeForDBMatchesExactNames(t *testing.T) {
	cases := map[string]string{
		"INT":       "int64",
		"BIGINT":    "int64",
		"POINT":     "string",
		"INTERVAL":  "string",
		"TIMESTAMP": "time.Time",
		"BYTEA":     "[]byte",
	}
	for dbType, want := range cases {
		if got := goTypeForDB(dbType); got != want {
			t.Errorf("goTypeForDB(%q) = %q, want %q", dbType, got, want)
		}
	}
}
