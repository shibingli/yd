package asynqx

import "github.com/yun-dev/yd/config"

// OptionsFromConfig maps the asynq section of the framework config onto Options. | OptionsFromConfig 把框架配置的 asynq 段映射为 Options。
//
// Keys with registered defaults (see config.Defaults): | 已注册默认值的键（见 config.Defaults）：
//
//	asynq.queues        weighted queue spec, default "critical:3,default:2,low:1" | 加权队列配置，默认 "critical:3,default:2,low:1"
//	asynq.concurrency   concurrency, default 10 | 并发度，默认 10
//
// The remaining keys fall back to their zero value when unset, so a zero duration means the | 其余键未设置时取零值：超时为 0 即 asynq 默认值、空串即不认证。
// asynq default and an empty string means no authentication:
//
//	asynq.redis_addr        broker address, used when asynq.RedisClientOpt carries none | 任务中间件地址，供 RedisClientOpt 未带地址时兜底
//	asynq.redis_password    authentication password | 认证口令
//	asynq.redis_db          database index | 库序号
//	asynq.strict_priority   drain the highest-weight queue first | 是否严格按权重优先级消费
//	asynq.shutdown_timeout  graceful-shutdown budget, parsed by Config.Duration | 优雅关闭预算，经 Config.Duration 解析
//	asynq.retry_base        base retry delay; > 0 installs ExponentialBackoff | 重试基准延迟；> 0 时安装 ExponentialBackoff
//	asynq.retry_max         cap of the retry delay | 重试延迟上限
//
// Accessors are used instead of Unmarshal, so the mapping is unaffected by mapstructure tags | 使用访问器而非 Unmarshal，因此不受 mapstructure 标签与键名大小写差异影响。
// or key-name case. A malformed asynq.queues is not swallowed: it stays on Options.Err and | 非法的 asynq.queues 不会被吞掉，而是留在 Options.Err 上，
// blocks NewClient / Server.Start at startup. A retry_base <= 0 keeps RetryDelayFunc nil, | 由 NewClient / Server.Start 在启动期阻断服务；retry_base <= 0 时 RetryDelayFunc 保持 nil，
// which means asynq's own retry policy. A nil c yields zero Options. | 即沿用 asynq 自身的重试策略。c 为 nil 时返回零值 Options。
func OptionsFromConfig(c *config.Config) Options {
	if c == nil {
		return Options{}
	}
	o := Options{
		RedisAddr:       c.GetString("asynq.redis_addr"),
		RedisPassword:   c.GetString("asynq.redis_password"),
		RedisDB:         c.GetInt("asynq.redis_db"),
		Concurrency:     c.GetInt("asynq.concurrency"),
		StrictPriority:  c.GetBool("asynq.strict_priority"),
		ShutdownTimeout: c.Duration("asynq.shutdown_timeout"),
	}
	queues, err := ParseQueues(c.GetString("asynq.queues"))
	if err != nil {
		o.err = err
	} else {
		o.Queues = queues
	}
	if base := c.Duration("asynq.retry_base"); base > 0 {
		o.RetryDelayFunc = ExponentialBackoff(base, c.Duration("asynq.retry_max"))
	}
	return o
}
