package redisx

import "github.com/yun-dev/yd/config"

// OptionsFromConfig maps the redis section of the framework config onto Options. | OptionsFromConfig 把框架配置的 redis 段映射为 Options。
//
// Keys with registered defaults (see config.Defaults): | 已注册默认值的键（见 config.Defaults）：
//
//	redis.mode  deployment shape, default single | 部署形态，默认 single
//	redis.addr  single-node address, default 127.0.0.1:6379 | 单机地址，默认 127.0.0.1:6379
//
// The remaining keys fall back to their zero value when unset, so zero timeouts and pool | 其余键未设置时取零值：超时/连接池为零即 go-redis 默认值、空串即不认证。
// sizes mean go-redis defaults and an empty string means no authentication:
//
//	redis.addrs           sentinel address list or cluster seed list | 哨兵地址列表或集群种子列表
//	redis.username        ACL user name | ACL 用户名
//	redis.password        authentication password | 认证口令
//	redis.db              database index | 库序号
//	redis.master_name     sentinel-monitored master name | 哨兵主节点名
//	redis.pool_size       maximum connections per node | 每个节点最大连接数
//	redis.min_idle_conns  connections kept warm per node | 每个节点保活连接数
//	redis.dial_timeout    connect timeout, parsed by Config.Duration | 建连超时，经 Config.Duration 解析
//	redis.read_timeout    socket read timeout | 读超时
//	redis.write_timeout   socket write timeout | 写超时
//	redis.key_prefix      key namespace | 键命名空间
//
// Accessors such as GetString/GetInt/Duration are used instead of Unmarshal, so the mapping is | 统一使用 GetString/GetInt/Duration 等访问器而非 Unmarshal，
// unaffected by mapstructure tags or key-name case. A nil c yields zero Options. | 因此不受 mapstructure 标签与键名大小写差异的影响。c 为 nil 时返回零值 Options。
func OptionsFromConfig(c *config.Config) Options {
	if c == nil {
		return Options{}
	}
	return Options{
		Mode:         c.GetString("redis.mode"),
		Addr:         c.GetString("redis.addr"),
		Addrs:        c.GetStringSlice("redis.addrs"),
		Username:     c.GetString("redis.username"),
		Password:     c.GetString("redis.password"),
		DB:           c.GetInt("redis.db"),
		MasterName:   c.GetString("redis.master_name"),
		PoolSize:     c.GetInt("redis.pool_size"),
		MinIdleConns: c.GetInt("redis.min_idle_conns"),
		DialTimeout:  c.Duration("redis.dial_timeout"),
		ReadTimeout:  c.Duration("redis.read_timeout"),
		WriteTimeout: c.Duration("redis.write_timeout"),
		KeyPrefix:    c.GetString("redis.key_prefix"),
	}
}
