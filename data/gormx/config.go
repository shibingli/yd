package gormx

import "github.com/shibingli/yd/core/config"

// OptionsFromConfig 从配置的 db 段映射出 Options。
//
// 框架注册了默认值的键（见 config.Defaults）：
//
//	db.driver        驱动名，默认 sqlite
//	db.dsn           数据源串，默认未设置
//	db.maxconn       连接池最大打开连接数，默认 100
//	db.maxidle       连接池最大空闲连接数，默认 10
//	db.slow_threshold 慢查询阈值，默认 200ms（字符串形式，经 Config.Duration 解析）
//	db.prepare_stmt  预编译语句缓存开关，默认 true
//
// 另有若干可选键，未设置时取零值（0 表示不限、空串表示回落框架默认）：
//
//	db.conn_max_lifetime       连接最长存活时间
//	db.conn_max_idle_time      连接最长空闲时间
//	db.log_level               GORM 日志级别，空值回落到 warn
//	db.skip_default_transaction 是否跳过单条写操作的默认事务
//	db.create_database         目标库不存在时是否自动建库，默认开启
//	                           （未注册在 config.Defaults，由本函数单独施加上默认值，
//	                           开启所带来的风险见 Options.CreateDatabase）
//
// 统一使用 Duration/GetString/GetInt/GetBool 访问器而非 Unmarshal，
// 因此不受 mapstructure 标签与实际键名大小写差异的影响。c 为 nil 时返回零值 Options。
func OptionsFromConfig(c *config.Config) Options {
	if c == nil {
		return Options{}
	}
	return Options{
		Driver:                 c.GetString("db.driver"),
		DSN:                    c.GetString("db.dsn"),
		MaxOpenConns:           c.GetInt("db.maxconn"),
		MaxIdleConns:           c.GetInt("db.maxidle"),
		ConnMaxLifetime:        c.Duration("db.conn_max_lifetime"),
		ConnMaxIdleTime:        c.Duration("db.conn_max_idle_time"),
		SlowThreshold:          c.Duration("db.slow_threshold"),
		LogLevel:               c.GetString("db.log_level"),
		PrepareStmt:            c.GetBool("db.prepare_stmt"),
		SkipDefaultTransaction: c.GetBool("db.skip_default_transaction"),
		CreateDatabase:         createDatabaseFromConfig(c),
	}
}

// createDatabaseFromConfig reads the db.create_database switch with its framework default applied.
// | createDatabaseFromConfig 读取 db.create_database 开关，并施加上它的框架默认值。
//
// db.create_database is registered in no default layer, so a plain GetBool would answer false for a
// key nobody wrote, which is the opposite of the framework default (the feature exists because a
// database that is not there yet should be created). A key that is absent, or present but empty —
// YD_DB_CREATE_DATABASE= in a shell, say — therefore reads as true, and only an explicit value
// decides otherwise: false / 0 switches automatic creation off (see Options.CreateDatabase for the
// risk that comes with leaving it on). | db.create_database 没有注册在任何默认值层，
// 因此单纯的 GetBool 对没人写过的键会给出 false，恰好与框架默认值相反（本特性存在的理由就是
// 「库还没建就该建出来」）。于是键缺席、或存在但取值为空（比如 shell 里的 YD_DB_CREATE_DATABASE=）
// 都读作 true，只有显式取值才能改变它：false / 0 关闭自动建库
// （开启所带来的风险见 Options.CreateDatabase）。
func createDatabaseFromConfig(c *config.Config) bool {
	if !c.IsSet("db.create_database") || c.GetString("db.create_database") == "" {
		return true
	}
	return c.GetBool("db.create_database")
}
