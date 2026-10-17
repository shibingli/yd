package gormx_test

import (
	"bytes"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/shibingli/yd/data/gormx"
)

// presetWant is one entry of the per-parameter assertions: a parameter name plus the decoded value expected. | presetWant 是逐参数断言里的一项：参数名加期望的解码取值。
type presetWant struct {
	key   string
	value string
}

// presetRow 是真实往返用例的模型：一个名字加一个时间戳。
//
// 时间字段刻意不叫 CreatedAt/UpdatedAt，避免 GORM 的自动时间戳把种子取值覆盖掉。
type presetRow struct {
	ID    uint      `gorm:"primaryKey"`
	Name  string    `gorm:"size:64"`
	Stamp time.Time `gorm:"column:stamp"`
}

// TableName pins the table name so the test never depends on GORM's pluralisation rules. | TableName 固定表名，避免测试依赖 GORM 的复数化规则。
func (presetRow) TableName() string { return "gormx_preset_rows" }

// decodedQuery 取出连接串查询串里每个参数名的全部解码取值。
//
// 解码用 url.ParseQuery，与 SQLite 驱动和 pgx 的 URL 形态所用的一致；
// go-sql-driver/mysql 对取值调的 url.QueryUnescape 是它的逐个取值版本。
func decodedQuery(t *testing.T, dsn string) url.Values {
	t.Helper()
	q := strings.IndexByte(dsn, '?')
	if q < 0 {
		t.Fatalf("连接串 %q 没有查询串，预置未生效", dsn)
	}
	values, err := url.ParseQuery(dsn[q+1:])
	if err != nil {
		t.Fatalf("连接串 %q 的查询串无法解码: %v", dsn, err)
	}
	return values
}

// hasValue reports whether one value of a multi-value parameter is present. | hasValue 判断多值参数的某个取值是否出现。
func hasValue(values url.Values, key, want string) bool {
	for _, got := range values[key] {
		if got == want {
			return true
		}
	}
	return false
}

// TestPresetDSNAddsEveryDefaultParam 逐参数断言三种驱动生成的连接串都含设计要求的每一个键与值。
//
// 断言分两层：整串相等证明顺序与形态稳定，逐参数（解码后）证明每个键与取值都在，
// 缺一即失败——只比整串会让「少了一个键」与「多了别的佐料」混在一起难以定位。
func TestPresetDSNAddsEveryDefaultParam(t *testing.T) {
	const (
		wantTimeZone   = "'+00:00'"
		wantSQLMode    = "'STRICT_TRANS_TABLES,NO_ZERO_DATE,NO_ZERO_IN_DATE,ERROR_FOR_DIVISION_BY_ZERO'"
		mysqlQueryTail = "loc=UTC&time_zone=%27%2B00%3A00%27&parseTime=True" +
			"&sql_mode=%27STRICT_TRANS_TABLES%2CNO_ZERO_DATE%2CNO_ZERO_IN_DATE%2CERROR_FOR_DIVISION_BY_ZERO%27" +
			"&charset=utf8mb4&collation=utf8mb4_unicode_ci"
		sqlitePragmaTail = "_loc=UTC" +
			"&_pragma=foreign_keys%281%29&_pragma=journal_mode%28WAL%29" +
			"&_pragma=synchronous%28NORMAL%29&_pragma=busy_timeout%285000%29"
	)

	cases := []struct {
		name    string
		driver  string
		dsn     string
		want    string
		query   []presetWant
		keyword []presetWant
	}{
		{
			name:   "mysql-no-query-string",
			driver: gormx.DriverMySQL,
			dsn:    "root:root@tcp(127.0.0.1:3306)/yd",
			want:   "root:root@tcp(127.0.0.1:3306)/yd?" + mysqlQueryTail,
			query: []presetWant{
				{key: "loc", value: "UTC"},
				{key: "time_zone", value: wantTimeZone},
				{key: "parseTime", value: "True"},
				{key: "sql_mode", value: wantSQLMode},
				{key: "charset", value: "utf8mb4"},
				{key: "collation", value: "utf8mb4_unicode_ci"},
			},
		},
		{
			name:   "mysql-scaffold-dsn",
			driver: gormx.DriverMySQL,
			dsn:    "root:root@tcp(127.0.0.1:3306)/yd?charset=utf8mb4&parseTime=True&loc=Local",
			want: "root:root@tcp(127.0.0.1:3306)/yd?charset=utf8mb4&parseTime=True&loc=Local" +
				"&time_zone=%27%2B00%3A00%27" +
				"&sql_mode=%27STRICT_TRANS_TABLES%2CNO_ZERO_DATE%2CNO_ZERO_IN_DATE%2CERROR_FOR_DIVISION_BY_ZERO%27" +
				"&collation=utf8mb4_unicode_ci",
			query: []presetWant{
				{key: "loc", value: "Local"},
				{key: "time_zone", value: wantTimeZone},
				{key: "parseTime", value: "True"},
				{key: "sql_mode", value: wantSQLMode},
				{key: "charset", value: "utf8mb4"},
				{key: "collation", value: "utf8mb4_unicode_ci"},
			},
		},
		{
			name:   "postgres-url-form",
			driver: gormx.DriverPostgres,
			dsn:    "postgres://user:pw@127.0.0.1:5432/yd?sslmode=disable",
			want:   "postgres://user:pw@127.0.0.1:5432/yd?sslmode=disable&TimeZone=UTC&client_encoding=UTF8",
			query: []presetWant{
				{key: "TimeZone", value: "UTC"},
				{key: "client_encoding", value: "UTF8"},
			},
		},
		{
			name:   "postgres-keyword-value-form",
			driver: gormx.DriverPostgres,
			dsn:    "host=127.0.0.1 port=5432 user=yd password=secret dbname=yd sslmode=disable",
			want:   "host=127.0.0.1 port=5432 user=yd password=secret dbname=yd sslmode=disable TimeZone=UTC client_encoding=UTF8",
			keyword: []presetWant{
				{key: "TimeZone", value: "UTC"},
				{key: "client_encoding", value: "UTF8"},
			},
		},
		{
			name:   "sqlite-plain-path",
			driver: gormx.DriverSQLite,
			dsn:    "data/yd.db",
			want:   "data/yd.db?" + sqlitePragmaTail,
			query: []presetWant{
				{key: "_loc", value: "UTC"},
				{key: "_pragma", value: "foreign_keys(1)"},
				{key: "_pragma", value: "journal_mode(WAL)"},
				{key: "_pragma", value: "synchronous(NORMAL)"},
				{key: "_pragma", value: "busy_timeout(5000)"},
			},
		},
		{
			name:   "sqlite-file-uri",
			driver: gormx.DriverSQLite,
			dsn:    "file::memory:?cache=shared",
			want:   "file::memory:?cache=shared&" + sqlitePragmaTail,
			query: []presetWant{
				{key: "_loc", value: "UTC"},
				{key: "_pragma", value: "foreign_keys(1)"},
				{key: "_pragma", value: "journal_mode(WAL)"},
				{key: "_pragma", value: "synchronous(NORMAL)"},
				{key: "_pragma", value: "busy_timeout(5000)"},
			},
		},
		{
			name:   "empty-driver-means-sqlite",
			driver: "",
			dsn:    "data/yd.db",
			want:   "data/yd.db?" + sqlitePragmaTail,
			query: []presetWant{
				{key: "_loc", value: "UTC"},
				{key: "_pragma", value: "journal_mode(WAL)"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gormx.PresetDSN(tc.driver, tc.dsn)
			if got != tc.want {
				t.Fatalf("PresetDSN(%q, %q)\n  得到 %q\n  期望 %q", tc.driver, tc.dsn, got, tc.want)
			}
			if len(tc.query) > 0 {
				values := decodedQuery(t, got)
				for _, w := range tc.query {
					if _, ok := values[w.key]; !ok {
						t.Fatalf("连接串 %q 缺少参数 %q", got, w.key)
					}
					if !hasValue(values, w.key, w.value) {
						t.Fatalf("参数 %q 的取值 %q 不在 %q 里（解码后得到 %q）", w.key, w.value, got, values[w.key])
					}
				}
			}
			for _, w := range tc.keyword {
				if !strings.Contains(got, " "+w.key+"="+w.value) {
					t.Fatalf("连接串 %q 缺少 %s=%s", got, w.key, w.value)
				}
			}
		})
	}
}

// TestPresetDSNKeepsExplicitParameters 断言使用方已写出的参数一律保留原值，预置不得覆盖。
//
// 覆盖 loc / time_zone / TimeZone / timezone / charset / parseTime / sql_mode / _loc / _pragma，
// 并同时断言「预置值没有混进来」与「同一参数只出现一次」，因为只断言前者会漏掉
// 「既保留了显式值、又在后面追加了一个覆盖它的预置值」这种最隐蔽的写法。
func TestPresetDSNKeepsExplicitParameters(t *testing.T) {
	cases := []struct {
		name      string
		driver    string
		dsn       string
		want      string
		absent    []string
		keyCounts map[string]int
	}{
		{
			name:      "mysql-loc-local",
			driver:    gormx.DriverMySQL,
			dsn:       "root:root@tcp(127.0.0.1:3306)/yd?loc=Local",
			want:      "root:root@tcp(127.0.0.1:3306)/yd?loc=Local",
			absent:    []string{"loc=UTC"},
			keyCounts: map[string]int{"loc=": 1},
		},
		{
			name:      "mysql-charset-latin1",
			driver:    gormx.DriverMySQL,
			dsn:       "root:root@tcp(127.0.0.1:3306)/yd?charset=latin1",
			want:      "root:root@tcp(127.0.0.1:3306)/yd?charset=latin1",
			absent:    []string{"charset=utf8mb4"},
			keyCounts: map[string]int{"charset=": 1},
		},
		{
			name:      "mysql-parse-time-false",
			driver:    gormx.DriverMySQL,
			dsn:       "root:root@tcp(127.0.0.1:3306)/yd?parseTime=false",
			want:      "root:root@tcp(127.0.0.1:3306)/yd?parseTime=false",
			absent:    []string{"parseTime=True"},
			keyCounts: map[string]int{"parseTime=": 1},
		},
		{
			name:      "mysql-explicit-system-variables",
			driver:    gormx.DriverMySQL,
			dsn:       "root:root@tcp(127.0.0.1:3306)/yd?time_zone='%2B05%3A30'&sql_mode='ANSI_QUOTES'",
			want:      "root:root@tcp(127.0.0.1:3306)/yd?time_zone='%2B05%3A30'&sql_mode='ANSI_QUOTES'",
			absent:    []string{"time_zone=%27%2B00%3A00%27", "sql_mode=%27STRICT"},
			keyCounts: map[string]int{"time_zone=": 1, "sql_mode=": 1},
		},
		{
			// MySQL system-variable names are case-insensitive, so a case variant also counts as explicitly written. | MySQL 的系统变量名不区分大小写，因此大小写变体也算「已显式写出」。
			name:      "mysql-system-variable-other-case",
			driver:    gormx.DriverMySQL,
			dsn:       "root:root@tcp(127.0.0.1:3306)/yd?TIME_ZONE='%2B05%3A30'",
			want:      "root:root@tcp(127.0.0.1:3306)/yd?TIME_ZONE='%2B05%3A30'",
			absent:    []string{"time_zone=%27%2B00%3A00%27"},
			keyCounts: map[string]int{"TIME_ZONE=": 1},
		},
		{
			name:      "postgres-keyword-timezone",
			driver:    gormx.DriverPostgres,
			dsn:       "host=127.0.0.1 user=yd TimeZone=Asia/Shanghai",
			want:      "host=127.0.0.1 user=yd TimeZone=Asia/Shanghai client_encoding=UTF8",
			absent:    []string{"TimeZone=UTC"},
			keyCounts: map[string]int{"TimeZone=": 1},
		},
		{
			name:      "postgres-keyword-lowercase-timezone",
			driver:    gormx.DriverPostgres,
			dsn:       "host=127.0.0.1 user=yd timezone=Asia/Shanghai",
			want:      "host=127.0.0.1 user=yd timezone=Asia/Shanghai client_encoding=UTF8",
			absent:    []string{"TimeZone=UTC"},
			keyCounts: map[string]int{"timezone=": 1},
		},
		{
			name:      "postgres-url-timezone",
			driver:    gormx.DriverPostgres,
			dsn:       "postgres://user:pw@127.0.0.1:5432/yd?TimeZone=Asia%2FShanghai",
			want:      "postgres://user:pw@127.0.0.1:5432/yd?TimeZone=Asia%2FShanghai&client_encoding=UTF8",
			absent:    []string{"TimeZone=UTC"},
			keyCounts: map[string]int{"TimeZone=": 1},
		},
		{
			name:   "sqlite-explicit-pragma",
			driver: gormx.DriverSQLite,
			dsn:    "data/yd.db?_pragma=journal_mode(DELETE)",
			want: "data/yd.db?_pragma=journal_mode(DELETE)&_loc=UTC" +
				"&_pragma=foreign_keys%281%29&_pragma=synchronous%28NORMAL%29&_pragma=busy_timeout%285000%29",
			absent:    []string{"journal_mode%28WAL%29"},
			keyCounts: map[string]int{"journal_mode": 1},
		},
		{
			// A _pragma value also accepts the other PRAGMA spelling, name=value. | _pragma 的取值也接受 PRAGMA 的另一种写法 name=value。
			name:   "sqlite-explicit-pragma-equals-form",
			driver: gormx.DriverSQLite,
			dsn:    "data/yd.db?_pragma=journal_mode%3DDELETE",
			want: "data/yd.db?_pragma=journal_mode%3DDELETE&_loc=UTC" +
				"&_pragma=foreign_keys%281%29&_pragma=synchronous%28NORMAL%29&_pragma=busy_timeout%285000%29",
			absent:    []string{"journal_mode%28WAL%29"},
			keyCounts: map[string]int{"journal_mode": 1},
		},
		{
			name:   "sqlite-explicit-loc",
			driver: gormx.DriverSQLite,
			dsn:    "data/yd.db?_loc=Asia/Shanghai",
			want: "data/yd.db?_loc=Asia/Shanghai&_pragma=foreign_keys%281%29" +
				"&_pragma=journal_mode%28WAL%29&_pragma=synchronous%28NORMAL%29&_pragma=busy_timeout%285000%29",
			absent:    []string{"_loc=UTC"},
			keyCounts: map[string]int{"_loc=": 1},
		},
		{
			// 调用方自己写的 _txlock 必须原样留下：SQLite 侧它才是并发写的开关，而预置表里
			// 刻意不含 _time_format（见 preset.go 的说明），正是为了不让驱动跳过 _txlock 的解析。
			name:   "sqlite-keeps-caller-txlock",
			driver: gormx.DriverSQLite,
			dsn:    "data/yd.db?_txlock=immediate",
			want: "data/yd.db?_txlock=immediate&_loc=UTC" +
				"&_pragma=foreign_keys%281%29&_pragma=journal_mode%28WAL%29" +
				"&_pragma=synchronous%28NORMAL%29&_pragma=busy_timeout%285000%29",
			absent:    []string{"_time_format="},
			keyCounts: map[string]int{"_txlock=": 1},
		},
		{
			// A caller-written _time_format is not part of the preset table, so the preset neither rewrites it nor appends it twice. | 调用方自己写的 _time_format 不在预置表内，预置既不改它也不重复追加。
			name:   "sqlite-caller-time-format-left-alone",
			driver: gormx.DriverSQLite,
			dsn:    "data/yd.db?_time_format=sqlite",
			want: "data/yd.db?_time_format=sqlite&_loc=UTC&_pragma=foreign_keys%281%29" +
				"&_pragma=journal_mode%28WAL%29&_pragma=synchronous%28NORMAL%29&_pragma=busy_timeout%285000%29",
			keyCounts: map[string]int{"_time_format=": 1},
		},
	}

	// Beyond the per-case assertion, every case must also prove the remaining presets are still filled in, otherwise "does not override" could be misimplemented as "skips the preset entirely". | 除逐例断言外，每例都要保证「其余预置仍然补齐」，否则「不覆盖」可能被误实现成「干脆不预置」。
	allPresets := map[string][]presetWant{
		gormx.DriverMySQL: {
			{key: "loc", value: "loc=UTC"},
			{key: "time_zone", value: "time_zone=%27%2B00%3A00%27"},
			{key: "parseTime", value: "parseTime=True"},
			{key: "sql_mode", value: "sql_mode=%27STRICT"},
			{key: "charset", value: "charset=utf8mb4"},
			{key: "collation", value: "collation=utf8mb4_unicode_ci"},
		},
		gormx.DriverPostgres: {
			{key: "TimeZone", value: "TimeZone=UTC"},
			{key: "client_encoding", value: "client_encoding=UTF8"},
		},
		gormx.DriverSQLite: {
			{key: "_loc", value: "_loc=UTC"},
			{key: "journal_mode", value: "journal_mode%28WAL%29"},
			{key: "foreign_keys", value: "foreign_keys%281%29"},
			{key: "synchronous", value: "synchronous%28NORMAL%29"},
			{key: "busy_timeout", value: "busy_timeout%285000%29"},
		},
	}
	// 每个用例中被使用方显式覆盖掉的参数名：这些参数必须仍在串里（取的是使用方的值），
	// 但不能再出现预置取值——absent 与 keyCounts 负责后半句。
	explicit := map[string][]string{
		"mysql-loc-local":                     {"loc"},
		"mysql-charset-latin1":                {"charset"},
		"mysql-parse-time-false":              {"parseTime"},
		"mysql-explicit-system-variables":     {"time_zone", "sql_mode"},
		"mysql-system-variable-other-case":    {"time_zone"},
		"postgres-keyword-timezone":           {"TimeZone"},
		"postgres-keyword-lowercase-timezone": {"TimeZone"},
		"postgres-url-timezone":               {"TimeZone"},
		"sqlite-explicit-pragma":              {"journal_mode"},
		"sqlite-explicit-pragma-equals-form":  {"journal_mode"},
		"sqlite-explicit-loc":                 {"_loc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gormx.PresetDSN(tc.driver, tc.dsn)

			// The existing content must stay verbatim at the front, with the presets appended behind it. | 既有内容必须在最前面原样保留，只有预置追加在后面。
			if !strings.HasPrefix(got, tc.want) {
				t.Fatalf("PresetDSN(%q, %q)\n  得到 %q\n  应以 %q 开头", tc.driver, tc.dsn, got, tc.want)
			}
			for _, want := range tc.absent {
				if strings.Contains(got, want) {
					t.Fatalf("连接串 %q 不该含预置值 %q", got, want)
				}
			}
			for key, n := range tc.keyCounts {
				if c := strings.Count(got, key); c != n {
					t.Fatalf("连接串 %q 里 %q 出现 %d 次，期望 %d 次", got, key, c, n)
				}
			}
			kept := map[string]bool{}
			for _, key := range explicit[tc.name] {
				kept[strings.ToLower(key)] = true
				if !strings.Contains(strings.ToLower(got), strings.ToLower(key)) {
					t.Fatalf("连接串 %q 丢了被显式覆盖的参数 %q", got, key)
				}
			}
			for _, preset := range allPresets[tc.driver] {
				if kept[strings.ToLower(preset.key)] {
					continue
				}
				if !strings.Contains(got, preset.value) {
					t.Fatalf("连接串 %q 未补齐预置值 %q", got, preset.value)
				}
			}
		})
	}
}

// TestPresetDSNIsIdempotent asserts that re-presetting the same DSN adds no further parameter. | TestPresetDSNIsIdempotent 断言对同一连接串重复预置不再新增任何参数。
func TestPresetDSNIsIdempotent(t *testing.T) {
	cases := []struct {
		driver string
		dsn    string
	}{
		{gormx.DriverMySQL, "root:root@tcp(127.0.0.1:3306)/yd"},
		{gormx.DriverMySQL, "root:root@tcp(127.0.0.1:3306)/yd?loc=Local&charset=latin1"},
		{gormx.DriverMySQL, ""},
		{gormx.DriverPostgres, "host=127.0.0.1 user=yd TimeZone=Asia/Shanghai"},
		{gormx.DriverPostgres, "postgres://user:pw@127.0.0.1:5432/yd?sslmode=disable"},
		{gormx.DriverSQLite, "data/yd.db"},
		{gormx.DriverSQLite, "file::memory:?cache=shared"},
		{gormx.DriverSQLite, "data/yd.db?_pragma=journal_mode(DELETE)"},
		{gormx.DriverSQLite, "data/yd.db?_txlock=immediate&_pragma=journal_mode(DELETE)"},
		{"", "data/yd.db"},
		{"oracle", "whatever-it-is"},
	}

	for _, tc := range cases {
		name := tc.driver + "/" + tc.dsn
		t.Run(name, func(t *testing.T) {
			once := gormx.PresetDSN(tc.driver, tc.dsn)
			twice := gormx.PresetDSN(tc.driver, once)
			if once != twice {
				t.Fatalf("二次预置改变了连接串\n  一次 %q\n  二次 %q", once, twice)
			}
			thrice := gormx.PresetDSN(tc.driver, twice)
			if once != thrice {
				t.Fatalf("三次预置仍在变化\n  一次 %q\n  三次 %q", once, thrice)
			}
			for _, key := range []string{"loc=", "time_zone=", "parseTime=", "sql_mode=", "charset=", "collation=",
				"TimeZone=", "client_encoding=", "_loc=", "_time_format=", "_pragma="} {
				if c := strings.Count(twice, key); c > 1 && key != "_pragma=" {
					t.Fatalf("二次预置后 %q 出现 %d 次: %q", key, c, twice)
				}
			}
			if strings.Contains(twice, "??") || strings.Contains(twice, "&&") {
				t.Fatalf("二次预置产生了空参数: %q", twice)
			}
		})
	}
}

// TestPresetDSNPreservesForm 断言三种形态的连接串都被原样保留：SQLite 的 file: URI、
// PostgreSQL 的空格分隔关键字/值、以及 MySQL 无查询串的形态。
func TestPresetDSNPreservesForm(t *testing.T) {
	cases := []struct {
		name    string
		driver  string
		dsn     string
		prefix  string
		absent  []string
		appends string
	}{
		{
			name:    "sqlite-file-uri-memory",
			driver:  gormx.DriverSQLite,
			dsn:     "file::memory:?cache=shared",
			prefix:  "file::memory:?cache=shared&",
			appends: "_loc=UTC",
		},
		{
			name:    "sqlite-bare-memory",
			driver:  gormx.DriverSQLite,
			dsn:     ":memory:",
			prefix:  ":memory:?",
			appends: "_pragma=journal_mode%28WAL%29",
		},
		{
			// 脚手架 dsnFor 生成的正是这一形态：既有的查询参数（含 _txlock=immediate）原样保留，
			// 预置只在其后追加，且**不得**追加 _time_format（那会让驱动跳过 _txlock 的解析）。
			name:    "sqlite-file-uri-with-foreign-params",
			driver:  gormx.DriverSQLite,
			dsn:     "file:yd.db?mode=rwc&cache=private&_txlock=immediate",
			prefix:  "file:yd.db?mode=rwc&cache=private&_txlock=immediate&",
			absent:  []string{"_time_format="},
			appends: "_pragma=journal_mode%28WAL%29",
		},
		{
			name:    "postgres-keyword-quoted-value",
			driver:  gormx.DriverPostgres,
			dsn:     "host=127.0.0.1 application_name='my app' user=yd",
			prefix:  "host=127.0.0.1 application_name='my app' user=yd TimeZone=UTC client_encoding=UTF8",
			appends: "TimeZone=UTC",
		},
		{
			name:    "mysql-no-query-string",
			driver:  gormx.DriverMySQL,
			dsn:     "user:pass@tcp(127.0.0.1:3306)/yd",
			prefix:  "user:pass@tcp(127.0.0.1:3306)/yd?loc=UTC",
			appends: "parseTime=True",
		},
		{
			// The '/' inside the password takes no part in the split: the database name follows the last '/'. | 口令里的 '/' 不参与切分：库名在最后一个 '/' 之后。
			name:    "mysql-slash-inside-password",
			driver:  gormx.DriverMySQL,
			dsn:     "user:pa/ss@tcp(127.0.0.1:3306)/yd?loc=Local",
			prefix:  "user:pa/ss@tcp(127.0.0.1:3306)/yd?loc=Local&",
			absent:  []string{"loc=UTC"},
			appends: "time_zone=%27%2B00%3A00%27",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gormx.PresetDSN(tc.driver, tc.dsn)
			if !strings.HasPrefix(got, tc.prefix) {
				t.Fatalf("PresetDSN(%q, %q)\n  得到 %q\n  应以 %q 开头", tc.driver, tc.dsn, got, tc.prefix)
			}
			if !strings.Contains(got, tc.appends) {
				t.Fatalf("连接串 %q 未包含 %q", got, tc.appends)
			}
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Fatalf("连接串 %q 不该含 %q", got, absent)
				}
			}
		})
	}
}

// TestPresetDSNLeavesEmptyAndUnknownAlone asserts that an empty DSN and an unknown driver come back untouched, since the preset layer validates nothing. | TestPresetDSNLeavesEmptyAndUnknownAlone 断言空连接串与未知驱动都原样返回，预置层不做校验。
func TestPresetDSNLeavesEmptyAndUnknownAlone(t *testing.T) {
	unchanged := []struct {
		driver string
		dsn    string
	}{
		{"oracle", "user/pass@host:1521/xe"},
		{"mongodb", "mongodb://127.0.0.1:27017/yd"},
		{"SQL Server", "sqlserver://sa:pw@127.0.0.1:1433?database=yd"},
		{gormx.DriverMySQL, ""},
		{gormx.DriverPostgres, ""},
		{gormx.DriverSQLite, ""},
		{gormx.DriverSQLite, "   "},
	}
	for _, tc := range unchanged {
		if got := gormx.PresetDSN(tc.driver, tc.dsn); got != tc.dsn {
			t.Fatalf("PresetDSN(%q, %q) = %q，期望原样返回", tc.driver, tc.dsn, got)
		}
	}

	// Case and surrounding whitespace in the driver name do not affect recognition, matching dialectorFor. | 驱动名的大小写与首尾空白不影响识别，与 dialectorFor 的口径一致。
	for _, driver := range []string{"MySQL", "mysql", " MYSQL "} {
		got := gormx.PresetDSN(driver, "user:pass@tcp(h:3306)/yd")
		if !strings.Contains(got, "loc=UTC") {
			t.Fatalf("驱动名 %q 未按 MySQL 预置: %q", driver, got)
		}
	}
	for _, driver := range []string{"postgres", " Postgres", "POSTGRES"} {
		got := gormx.PresetDSN(driver, "host=h user=u")
		if !strings.Contains(got, "TimeZone=UTC") {
			t.Fatalf("驱动名 %q 未按 PostgreSQL 预置: %q", driver, got)
		}
	}
	for _, driver := range []string{"", "sqlite", "SQLite"} {
		got := gormx.PresetDSN(driver, "data/yd.db")
		if !strings.Contains(got, "_loc=UTC") {
			t.Fatalf("驱动名 %q 未按 SQLite 预置: %q", driver, got)
		}
	}
}

// TestOpenAppliesPresetDSNToSQLite 证明 Open 确实走了预置：连接串里一个参数都没写，
// 每条连接上却已经有了 foreign_keys / journal_mode / synchronous / busy_timeout。
//
// 这四个 pragma 都是可观测的落地证据：文件库的 journal_mode 默认是 delete、
// synchronous 默认是 full、foreign_keys 默认是 0，只有预置真的执行了才会是下面这些取值。
func TestOpenAppliesPresetDSNToSQLite(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "preset.db")
	db := openPresetDB(t, dsn)

	want := map[string]string{
		"journal_mode": "wal",
		"synchronous":  "1",
		"foreign_keys": "1",
		"busy_timeout": "5000",
	}
	for pragma, wantValue := range want {
		if got := pragmaText(t, db, pragma); got != wantValue {
			t.Fatalf("连接上的 PRAGMA %s = %q，期望 %q（预置未生效）", pragma, got, wantValue)
		}
	}
}

// TestPresetDSNRealSQLiteRoundTrip 用预置后的 SQLite DSN 真实建库、写入、关闭、重新打开再读出，
// 证明预置既没有破坏可用性，也没有破坏时间往返。
func TestPresetDSNRealSQLiteRoundTrip(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "roundtrip.db")
	seed := presetRow{Name: "alice", Stamp: time.Date(2026, 10, 17, 8, 30, 15, 0, time.UTC)}

	db := openPresetDB(t, dsn)
	if err := gormx.AutoMigrate(db, &presetRow{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	// 写出去的时间文本必须带时区偏移量：这是驱动自带的默认写出格式（parseTimeFormats[0]），
	// 预置不需要、也不得靠 _time_format 去换（见 preset.go 里关于该键的说明）。
	if raw := rawStamp(t, db, seed.Name); !strings.Contains(raw, "+00:00") {
		t.Fatalf("落库的时间文本 %q 不含时区偏移量", raw)
	}
	if err := gormx.Close(db); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	reopened := openPresetDB(t, dsn)
	var got presetRow
	if err := reopened.First(&got, "name = ?", seed.Name).Error; err != nil {
		t.Fatalf("重新打开后查询失败: %v", err)
	}
	if got.Stamp.UnixNano() != seed.Stamp.UnixNano() {
		t.Fatalf("重新打开后时间 = %s，期望 %s", got.Stamp, seed.Stamp)
	}
	if !got.Stamp.Equal(seed.Stamp) {
		t.Fatalf("重新打开后时间 = %s，与写入的 %s 不是同一瞬时点", got.Stamp, seed.Stamp)
	}
	t.Logf("sqlite round trip ok | 真实 SQLite 往返成功: dsn=%s stored=%s read=%s",
		dsn, seed.Stamp.Format(time.RFC3339), got.Stamp.Format(time.RFC3339))
}

// TestPresetDSNRealSQLiteFileURI 断言 file: URI 形态经预置后仍能真实打开（SQLite 的 URI
// 解析器会忽略它不认识的查询参数，这里用真实连接把它钉住），并且预置的 pragma 仍生效。
func TestPresetDSNRealSQLiteFileURI(t *testing.T) {
	dsn := "file::memory:?cache=shared"
	db, err := gormx.Open(gormx.Options{Driver: gormx.DriverSQLite, DSN: dsn}, newBufLogger(&bytes.Buffer{}))
	if err != nil {
		t.Fatalf("Open(%q) 失败: %v", dsn, err)
	}
	t.Cleanup(func() { _ = gormx.Close(db) })

	if err := db.Exec("CREATE TABLE preset_probe (id INTEGER PRIMARY KEY, name TEXT)").Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := db.Exec("INSERT INTO preset_probe (name) VALUES (?)", "alice").Error; err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	var got string
	if err := db.Raw("SELECT name FROM preset_probe WHERE id = 1").Scan(&got).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if got != "alice" {
		t.Fatalf("读回 %q，期望 alice", got)
	}
	if v := pragmaText(t, db, "foreign_keys"); v != "1" {
		t.Fatalf("file: URI 形态下 PRAGMA foreign_keys = %q，期望 1", v)
	}
}

// TestPresetDSNNeverAddsTimeFormat 是不许重新加入 _time_format 的防回归断言。
//
// 依据（github.com/glebarez/go-sqlite v1.21.2/sqlite.go）：applyQueryParams 一旦看到 _time_format
// 就在 893-900 行提前 return nil，其后的 _txlock 分支（902 行）永远走不到，调用方显式写的
// _txlock=immediate 会被静默丢弃；写事务退化为延迟事务后，「先读后写」以 SQLITE_BUSY_SNAPSHOT
// 失败，且不调用 busy handler。该键还换不来任何东西：writeTimeFormats["sqlite"] 就是
// parseTimeFormats[0]，与缺席时的默认写出格式同一个。因此预置层绝不追加它，也不改调用方写的那一份。
func TestPresetDSNNeverAddsTimeFormat(t *testing.T) {
	inputs := []struct {
		name string
		dsn  string
	}{
		{"plain-path", "data/yd.db"},
		{"file-uri", "file:data/yd.db?cache=shared"},
		{"memory", ":memory:"},
		{"scaffold-dsn", "file:data/yd.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate"},
		{"txlock-only", "data/yd.db?_txlock=immediate"},
		{"foreign-params", "file:yd.db?mode=rwc&cache=private&_txlock=immediate"},
	}
	for _, tc := range inputs {
		for _, driver := range []string{gormx.DriverSQLite, "", "SQLite", " SQLITE "} {
			got := gormx.PresetDSN(driver, tc.dsn)
			if strings.Contains(got, "_time_format") {
				t.Fatalf("驱动名 %q、连接串 %q 的预置结果 %q 含 _time_format："+
					"该键会让驱动提前返回、跳过 _txlock 解析，禁止重新加入", driver, tc.dsn, got)
			}
			if strings.Contains(tc.dsn, "_txlock=immediate") && !strings.Contains(got, "_txlock=immediate") {
				t.Fatalf("驱动名 %q、连接串 %q 的预置结果 %q 丢了调用方的 _txlock=immediate",
					driver, tc.dsn, got)
			}
		}
	}
}

// TestSQLiteTxlockReachesDriverThroughOpen 用真实连接证明 _txlock=immediate 经 gormx.Open 之后
// 仍然生效——即预置确实没有把它吃掉。
//
// 判据是写锁的持有者：immediate 事务在 BEGIN 时就取得写锁，因此只读事务进行期间另一条连接写不进去，
// 而本事务随后的写必然成功。若 txlock 被吞掉（延迟事务），这两条断言会同时反过来：
// 另一条连接能写入，本事务的升级写以 SQLITE_BUSY_SNAPSHOT 失败。
func TestSQLiteTxlockReachesDriverThroughOpen(t *testing.T) {
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "txlock.db"))
	holderDSN := "file:" + path + "?_pragma=busy_timeout(200)&_pragma=journal_mode(WAL)&_txlock=immediate"
	writerDSN := "file:" + path + "?_pragma=busy_timeout(200)"

	holder := openPresetDB(t, holderDSN)
	if err := holder.Exec("CREATE TABLE IF NOT EXISTS probe (id INTEGER PRIMARY KEY, v INTEGER)").Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := holder.Exec("INSERT INTO probe (id, v) VALUES (1, 0)").Error; err != nil {
		t.Fatalf("写入种子失败: %v", err)
	}
	writer := openPresetDB(t, writerDSN)

	tx := holder.Begin()
	if tx.Error != nil {
		t.Fatalf("Begin 失败: %v", tx.Error)
	}
	var n int
	// Read only, never write: the lock must be taken at BEGIN, otherwise this is not an immediate transaction. | 只读，不写：锁必须是在 BEGIN 时拿到的，否则这不是 immediate 事务。
	if err := tx.Raw("SELECT count(*) FROM probe").Scan(&n).Error; err != nil {
		t.Fatalf("事务内读取失败: %v", err)
	}
	otherErr := writer.Exec("UPDATE probe SET v = 2 WHERE id = 1").Error
	upgradeErr := tx.Exec("UPDATE probe SET v = 1 WHERE id = 1").Error
	if err := tx.Rollback().Error; err != nil {
		t.Fatalf("Rollback 失败: %v", err)
	}

	if otherErr == nil {
		t.Fatalf("BEGIN IMMEDIATE 未生效：持有写锁的事务期间，另一条连接仍然写入成功")
	}
	if upgradeErr != nil {
		t.Fatalf("_txlock=immediate 生效时本事务的写入应成功，得到 %v", upgradeErr)
	}
	// The lock must be released once the transaction ends, otherwise the failure above might only mean the database stayed locked forever rather than the immediate semantics. | 事务结束后锁必须释放，否则上面的失败可能只是「库被永久锁住」而不是 immediate 的语义。
	if err := writer.Exec("UPDATE probe SET v = 3 WHERE id = 1").Error; err != nil {
		t.Fatalf("事务结束后另一条连接仍写不进去: %v", err)
	}
	t.Logf("txlock ok | _txlock 生效: 持锁期间另一连接的写=%v，事务内升级写=<nil>，回滚后另一连接写=<nil>", otherErr)
}

// TestSQLiteTimeFormatKeyIsANoOp 钉住「删掉 _time_format 不损失任何格式」这一事实。
//
// 同一时间在带该键与不带该键的连接串下落库，原始文本逐字节相同；两者都自带时区偏移量。
// 这就是预置表删掉它的依据：它换不回格式，只换得走调用方的 _txlock。
func TestSQLiteTimeFormatKeyIsANoOp(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Date(2026, 10, 17, 8, 30, 15, 0, time.UTC)
	texts := make([]string, 0, 2)
	for _, extra := range []string{"", "&_time_format=sqlite"} {
		path := filepath.Join(dir, "fmt"+strings.ReplaceAll(extra, "&", "_")+".db")
		db := openPresetDB(t, path+"?_pragma=journal_mode(WAL)"+extra)
		if err := gormx.AutoMigrate(db, &presetRow{}); err != nil {
			t.Fatalf("AutoMigrate 失败: %v", err)
		}
		if err := db.Create(&presetRow{Name: "a", Stamp: stamp}).Error; err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		texts = append(texts, rawStamp(t, db, "a"))
		if err := gormx.Close(db); err != nil {
			t.Fatalf("Close 失败: %v", err)
		}
	}
	if texts[0] != texts[1] {
		t.Fatalf("落库文本不一致：无 _time_format = %q，有 _time_format = %q", texts[0], texts[1])
	}
	if !strings.Contains(texts[0], "+00:00") {
		t.Fatalf("落库文本 %q 不含时区偏移量", texts[0])
	}
	t.Logf("time format ok | _time_format 为空操作: stored=%s", texts[0])
}

// openPresetDB opens a connection with the given SQLite DSN and closes it when the test ends. | openPresetDB 用给定的 SQLite DSN 打开连接并在测试结束时关闭。
func openPresetDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gormx.Open(gormx.Options{Driver: gormx.DriverSQLite, DSN: dsn}, newBufLogger(&bytes.Buffer{}))
	if err != nil {
		t.Fatalf("Open(%q) 失败: %v", dsn, err)
	}
	t.Cleanup(func() { _ = gormx.Close(db) })
	return db
}

// pragmaText reads one PRAGMA value off the connection, feeding the assertion that the presets really landed on it. | pragmaText 读出连接上的一个 PRAGMA 取值，供「预置是否真的落到连接上」的断言使用。
func pragmaText(t *testing.T, db *gorm.DB, name string) string {
	t.Helper()
	var got any
	if err := db.Raw("PRAGMA " + name).Row().Scan(&got); err != nil {
		t.Fatalf("读取 PRAGMA %s 失败: %v", name, err)
	}
	return fmt.Sprint(got)
}

// rawStamp 读出 stamp 列的原始文本，用于断言写出时间带偏移量。
//
// 用 CAST 成 TEXT 是为了绕开驱动的时间解析：表达式没有声明类型，驱动便原样给出文本。
func rawStamp(t *testing.T, db *gorm.DB, name string) string {
	t.Helper()
	var raw string
	if err := db.Raw("SELECT CAST(stamp AS TEXT) FROM "+presetRow{}.TableName()+" WHERE name = ?", name).
		Scan(&raw).Error; err != nil {
		t.Fatalf("读取 stamp 原始文本失败: %v", err)
	}
	return raw
}
