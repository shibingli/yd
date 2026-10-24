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
		// Dameng aliases: NUMBER shares the exact numeric branch, RAW/IMAGE are byte sequences and
		// VARCHAR2/CLOB stay strings. | 达梦别名：NUMBER 归入精确数值，RAW/IMAGE 是字节序列，
		// VARCHAR2/CLOB 仍为字符串。
		"NUMBER":     "float64",
		"NUMBER(19)": "float64",
		"RAW":        "[]byte",
		"IMAGE":      "[]byte",
		"VARCHAR2":   "string",
		"CLOB":       "string",
	}
	for dbType, want := range cases {
		if got := goTypeForDB(dbType); got != want {
			t.Errorf("goTypeForDB(%q) = %q, want %q", dbType, got, want)
		}
	}
}
