package cachex

import (
	"github.com/shibingli/yd/core/config"
	"github.com/shibingli/yd/data/cachex/local"
)

// OptionsFromConfig maps the cache.* section into Manager options. | OptionsFromConfig 把 cache.* 配置段映射为管理器选项。
//
// Every missing key falls back to the framework default, so a project can enable the | 每个缺失的键都回退到框架默认值，因此项目只需覆盖自己关心的项即可启用缓存。
// cache by overriding only the values it cares about.
func OptionsFromConfig(c *config.Config) Options {
	if c == nil {
		return Options{FailOpen: true}
	}
	return Options{
		Lease:        c.GetDuration("cache.lease"),
		LoadTimeout:  c.GetDuration("cache.load_timeout"),
		EventBuffer:  c.GetInt("cache.event_buffer"),
		FailOpen:     c.GetBool("cache.fail_open"),
		TombstoneTTL: c.GetDuration("cache.tombstone_ttl"),
		Local: local.Options{
			MaxCost:         c.GetInt64("cache.max_cost"),
			Shards:          c.GetInt("cache.shards"),
			MaxEntrySize:    c.GetInt("cache.max_entry_size"),
			CleanupInterval: c.GetDuration("cache.cleanup_interval"),
			SampleSize:      c.GetInt("cache.sample_size"),
		},
	}
}

// RedisOptionsFromConfig maps cache.redis.* into RedisOptions. | RedisOptionsFromConfig 把 cache.redis.* 映射为 RedisOptions。
//
// When cache.redis.prefix is empty, the effective prefix is redis.key_prefix followed by | cache.redis.prefix 为空时，实际前缀为 redis.key_prefix 后接
// cache.prefix. Both are concatenated verbatim; include separators in the configured values. | cache.prefix。两者都原样拼接；请在配置值中包含分隔符。
func RedisOptionsFromConfig(c *config.Config) RedisOptions {
	if c == nil {
		return RedisOptions{}
	}
	prefix := c.GetString("cache.redis.prefix")
	if prefix == "" {
		prefix = c.GetString("redis.key_prefix") + c.GetString("cache.prefix")
	}
	if prefix == "" {
		prefix = DefaultPrefix
	}
	o := RedisOptions{
		Prefix:          prefix,
		Channel:         c.GetString("cache.redis.channel"),
		TombstoneTTL:    c.GetDuration("cache.redis.tombstone_ttl"),
		SubscribeBuffer: c.GetInt("cache.redis.subscribe_buffer"),
		HealthInterval:  c.GetDuration("cache.redis.health_interval"),
	}
	if o.TombstoneTTL <= 0 {
		o.TombstoneTTL = c.GetDuration("cache.tombstone_ttl")
	}
	return o.withDefaults()
}
