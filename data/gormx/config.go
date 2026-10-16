package gormx

import "github.com/shibingli/yd/config"

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
	}
}
