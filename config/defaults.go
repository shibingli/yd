package config

import "time"

// Defaults 注册框架全部安全底线默认值，构成配置的默认值层（最低优先级）。
//
// 这里的取值可被配置文件、环境变量（含 .env）与命令行参数依次覆盖；
// 由于它们决定了下游项目的开箱行为，修改任何一项都属于公共 API 变更，
// 须经人工确认（见 AGENTS.md §1）。
func Defaults(c *Config) {
	v := c.Viper

	// server：HTTP/1.1 + H2C 默认开启，HTTP/3 与 ACME 默认关闭。
	v.SetDefault("server.host", "")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.read_timeout", 15*time.Second)
	v.SetDefault("server.write_timeout", 15*time.Second)
	v.SetDefault("server.idle_timeout", 60*time.Second)
	v.SetDefault("server.shutdown_timeout", 10*time.Second)
	v.SetDefault("server.h2c", true)
	v.SetDefault("server.http3.enabled", false)
	v.SetDefault("server.acme.enabled", false)

	// log：JSON 结构化输出，信息级别。
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")

	// app：应用标识与运行环境。
	v.SetDefault("app.name", "yd")
	v.SetDefault("app.env", "development")

	// db：默认 SQLite，连接池与慢查询阈值，预编译语句默认开启。
	v.SetDefault("db.driver", "sqlite")
	v.SetDefault("db.maxconn", 100)
	v.SetDefault("db.maxidle", 10)
	v.SetDefault("db.slow_threshold", "200ms")
	v.SetDefault("db.prepare_stmt", true)

	// redis：单机模式。
	v.SetDefault("redis.mode", "single")
	v.SetDefault("redis.addr", "127.0.0.1:6379")

	// asynq：加权队列与并发度。
	v.SetDefault("asynq.queues", "critical:3,default:2,low:1")
	v.SetDefault("asynq.concurrency", 10)

	// grpc：默认关闭，同端口 h2c 分流模式。
	v.SetDefault("grpc.enabled", false)
	v.SetDefault("grpc.mode", "same-port")

	// i18n 与 tailwind。
	v.SetDefault("i18n.default_lang", "zh-CN")
	v.SetDefault("tailwind.enabled", true)
}
