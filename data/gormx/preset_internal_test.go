package gormx

import (
	"testing"

	"github.com/glebarez/sqlite"
	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
)

// TestDialectorForAppliesPresetDSN 断言 Open 解析方言的那一处就把预置施加到了连接串上。
//
// 这是 MySQL 与 PostgreSQL 唯一能在没有服务端的情况下拿到的证据：方言本身带着连不上也不影响的
// DSN 字段，因此可以直接比对 Open 真正会交给驱动的那个串。
func TestDialectorForAppliesPresetDSN(t *testing.T) {
	cases := []struct {
		name   string
		driver string
		dsn    string
		want   string
	}{
		{
			name:   "mysql",
			driver: DriverMySQL,
			dsn:    "root:root@tcp(127.0.0.1:3306)/yd",
			want: "root:root@tcp(127.0.0.1:3306)/yd?loc=UTC&time_zone=%27%2B00%3A00%27&parseTime=True" +
				"&sql_mode=%27STRICT_TRANS_TABLES%2CNO_ZERO_DATE%2CNO_ZERO_IN_DATE%2CERROR_FOR_DIVISION_BY_ZERO%27" +
				"&charset=utf8mb4&collation=utf8mb4_unicode_ci",
		},
		{
			name:   "postgres-keyword",
			driver: DriverPostgres,
			dsn:    "host=127.0.0.1 user=yd",
			want:   "host=127.0.0.1 user=yd TimeZone=UTC client_encoding=UTF8",
		},
		{
			name:   "postgres-url",
			driver: DriverPostgres,
			dsn:    "postgres://user:pw@127.0.0.1:5432/yd",
			want:   "postgres://user:pw@127.0.0.1:5432/yd?TimeZone=UTC&client_encoding=UTF8",
		},
		{
			name:   "sqlite",
			driver: DriverSQLite,
			dsn:    "data/yd.db",
			want: "data/yd.db?_loc=UTC" +
				"&_pragma=journal_mode%28WAL%29&_pragma=synchronous%28NORMAL%29&_pragma=busy_timeout%285000%29",
		},
		{
			// An empty driver name is treated as the framework default SQLite, matching PresetDSN. | 空驱动名按框架默认的 SQLite 处理，与 PresetDSN 的口径一致。
			name:   "empty-driver-means-sqlite",
			driver: "",
			dsn:    "data/yd.db",
			want: "data/yd.db?_loc=UTC" +
				"&_pragma=journal_mode%28WAL%29&_pragma=synchronous%28NORMAL%29&_pragma=busy_timeout%285000%29",
		},
		{
			// The scaffold-generated shape: _txlock=immediate must survive all the way into the driver. | 脚手架生成的形态：_txlock=immediate 必须一路活到驱动手里。
			name:   "sqlite-scaffold-dsn-keeps-txlock",
			driver: DriverSQLite,
			dsn:    "file:data/yd.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate",
			want: "file:data/yd.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate" +
				"&_loc=UTC&_pragma=synchronous%28NORMAL%29",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dialector, err := dialectorFor(tc.driver, tc.dsn)
			if err != nil {
				t.Fatalf("dialectorFor(%q, %q) 失败: %v", tc.driver, tc.dsn, err)
			}

			var got string
			switch d := dialector.(type) {
			case *gormmysql.Dialector:
				got = d.DSN
				// mysql.Open 用 mysql.ParseDSN 解析连接串并吞掉错误，解析失败会留下 nil 配置；
				// 断言它非 nil，等于断言预置产物仍能被 MySQL 驱动自己的解析器接受。
				if d.DSNConfig == nil {
					t.Fatalf("预置后的 MySQL 连接串 %q 无法被 mysql.ParseDSN 解析", d.DSN)
				}
			case *gormpostgres.Dialector:
				got = d.DSN
			case *sqlite.Dialector:
				got = d.DSN
			default:
				t.Fatalf("dialectorFor(%q, %q) 返回了未知方言 %T", tc.driver, tc.dsn, dialector)
			}

			if got != tc.want {
				t.Fatalf("dialectorFor(%q, %q) 的 DSN\n  得到 %q\n  期望 %q", tc.driver, tc.dsn, got, tc.want)
			}
		})
	}
}
