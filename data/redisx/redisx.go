// Package redisx is the yd framework's redis layer. | Package redisx 是 yd 框架的 Redis 装配层。
//
// Responsibilities: | 职责：
//
//   - Options/Open turn the configured connection parameters into a go-redis universal |   - Options/Open 把配置好的连接参数收敛为一个 go-redis 通用客户端，
//     client supporting single-node, sentinel and cluster modes, so callers never branch |     支持单机、哨兵与集群三种模式，调用方无需按模式分支；
//     on the mode themselves; Health performs the liveness probe on top of it; |     Health 在其上提供探活；
//   - Cache is a generic read-through decorator: single-flight (anti-stampede), TTL with |   - Cache 是泛型读透装饰器：单飞（防击穿）、带抖动 TTL（防雪崩）、
//     jitter (anti-avalanche) and negative caching (anti-penetration), serialized through jsonx. |     负缓存（防穿透），序列化统一走 jsonx；
//   - OptionsFromConfig reads the redis.* section of the framework config. |   - OptionsFromConfig 读取框架配置的 redis.* 段。
//
// What it does not do: it does not manage key layouts for the business, does not wrap redis | 不做什么：不替业务设计键布局、不封装 redis 的分布式锁与发布订阅，
// locking or pub/sub, and it does not retry or fall back on the caller's behalf - a cache | 也不替调用方做重试与降级——缓存错误一律如实上报，不做静默降级。
// error is always reported as-is instead of being silently degraded. | 缓存错误一律如实上报，不做静默降级。
//
// Open never performs a round trip, so a client can be constructed before the server is | Open 不做任何网络往返，因此可以在服务端就绪之前构造客户端；
// reachable; use Health when connectivity must be verified (typically in a readiness probe). | 需要确认连通性时调用 Health（通常用于就绪探针）。
package redisx

import (
	"context"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/shibingli/yd/errx"
)

// Mode names accepted by Options.Mode. | Options.Mode 接受的模式名。
const (
	// ModeSingle is the single-node mode; it uses Options.Addr and is the framework default. | ModeSingle 是单机模式，使用 Options.Addr，且为框架默认。
	ModeSingle = "single"
	// ModeSentinel is the sentinel mode; it needs the sentinel addresses plus MasterName. | ModeSentinel 是哨兵模式，需要哨兵地址与 MasterName。
	ModeSentinel = "sentinel"
	// ModeCluster is the cluster mode; it needs one or more seed addresses in Addrs. | ModeCluster 是集群模式，Addrs 中需要一个及以上种子地址。
	ModeCluster = "cluster"
)

// Declared sentinel errors, testable with errors.Is. | 已声明的哨兵错误，可用 errors.Is 判定。
var (
	// ErrUnknownMode indicates Options.Mode is not one of single/sentinel/cluster. | ErrUnknownMode 表示 Options.Mode 不是 single/sentinel/cluster 之一。
	ErrUnknownMode = errx.New("redisx: unknown redis mode | redisx：未知的 Redis 模式")
	// ErrMissingAddr indicates no address was configured for the selected mode. | ErrMissingAddr 表示所选模式没有配置任何地址。
	ErrMissingAddr = errx.New("redisx: missing redis address | redisx：缺少 Redis 地址")
	// ErrMissingMasterName indicates sentinel mode was selected without MasterName. | ErrMissingMasterName 表示选择了哨兵模式但没有 MasterName。
	ErrMissingMasterName = errx.New("redisx: missing sentinel master name | redisx：缺少哨兵主节点名")
	// ErrNilClient indicates a nil redis client was passed to a helper. | ErrNilClient 表示传给辅助函数的 Redis 客户端为 nil。
	ErrNilClient = errx.New("redisx: nil redis client | redisx：Redis 客户端为 nil")
	// ErrHealthCheckFailed indicates the liveness probe did not complete successfully. | ErrHealthCheckFailed 表示探活未能成功完成。
	ErrHealthCheckFailed = errx.New("redisx: redis health check failed | redisx：Redis 探活失败")
	// ErrCloseFailed indicates the client failed to release its connections. | ErrCloseFailed 表示客户端释放连接失败。
	ErrCloseFailed = errx.New("redisx: close redis client failed | redisx：关闭 Redis 客户端失败")
)

// Options describes the connection parameters of one redis deployment. | Options 描述一套 Redis 部署的连接参数。
//
// Every field may be left at its zero value: Mode falls back to ModeSingle, and timeouts, | 所有字段都可以留零值：Mode 回落到 ModeSingle；
// pool sizes and the database index are then filled in by go-redis defaults. | 超时、连接池与库序号则交由 go-redis 的默认值填充。
type Options struct {
	// Mode is the deployment shape: single / sentinel / cluster; empty means ModeSingle. | Mode 是部署形态：single / sentinel / cluster；空值表示 ModeSingle。
	Mode string
	// Addr is the address of the single node, or the only seed address of the other modes; | Addr 是单机地址，或其它模式唯一的种子地址；
	// it takes effect only when Addrs is empty. | 仅当 Addrs 为空时生效。
	Addr string
	// Addrs is the sentinel address list or the cluster seed list. | Addrs 是哨兵地址列表或集群种子列表。
	Addrs []string
	// Username is the ACL user name; empty means the default user. | Username 是 ACL 用户名，空值表示默认用户。
	Username string
	// Password is the authentication password; empty means no authentication. | Password 是认证口令，空值表示不认证。
	Password string
	// DB is the database index, effective in single and sentinel modes only. | DB 是库序号，仅在单机与哨兵模式下生效。
	DB int
	// MasterName is the sentinel-monitored master name, required in sentinel mode. | MasterName 是哨兵监控的主节点名，哨兵模式下必填。
	MasterName string
	// PoolSize is the maximum number of socket connections per node; <=0 means the go-redis default. | PoolSize 是每个节点的最大连接数；<=0 表示使用 go-redis 默认值。
	PoolSize int
	// MinIdleConns is the number of connections kept warm per node; <=0 means the go-redis default. | MinIdleConns 是每个节点保活的空闲连接数；<=0 表示使用 go-redis 默认值。
	MinIdleConns int
	// DialTimeout bounds establishing one connection; 0 means the go-redis default. | DialTimeout 限制建立单条连接；0 表示使用 go-redis 默认值。
	DialTimeout time.Duration
	// ReadTimeout bounds one socket read; 0 means the go-redis default. | ReadTimeout 限制单次 socket 读；0 表示使用 go-redis 默认值。
	ReadTimeout time.Duration
	// WriteTimeout bounds one socket write; 0 means the go-redis default. | WriteTimeout 限制单次 socket 写；0 表示使用 go-redis 默认值。
	WriteTimeout time.Duration
	// KeyPrefix is the key namespace shared by every key the framework writes; pass it to | KeyPrefix 是框架写入的键共享的命名空间；
	// CacheOptions.Prefix when building a Cache decorator. | 构造缓存装饰器时应把它传给 CacheOptions.Prefix。
	KeyPrefix string
}

// Open builds a universal client for o without performing any round trip. | Open 为 o 构造通用客户端，且不进行任何网络往返。
//
// An empty Mode is treated as ModeSingle. Invalid combinations are reported through a declared | 空 Mode 按 ModeSingle 处理；非法组合通过已声明的哨兵错误上报：
// sentinel: ErrUnknownMode, ErrMissingAddr or ErrMissingMasterName. The caller owns the returned | ErrUnknownMode、ErrMissingAddr 或 ErrMissingMasterName。返回的客户端由调用方负责 Close。
// client and must Close it.
func Open(o Options) (redis.UniversalClient, error) {
	switch mode := strings.ToLower(strings.TrimSpace(o.Mode)); mode {
	case "", ModeSingle:
		if strings.TrimSpace(o.Addr) == "" {
			return nil, ErrMissingAddr
		}
		return redis.NewClient(o.simpleOptions()), nil
	case ModeSentinel:
		if len(o.addrs()) == 0 {
			return nil, ErrMissingAddr
		}
		if strings.TrimSpace(o.MasterName) == "" {
			return nil, ErrMissingMasterName
		}
		return redis.NewFailoverClient(o.failoverOptions()), nil
	case ModeCluster:
		if len(o.addrs()) == 0 {
			return nil, ErrMissingAddr
		}
		return redis.NewClusterClient(o.clusterOptions()), nil
	default:
		return nil, errx.Detail(ErrUnknownMode, "%q", o.Mode)
	}
}

// Health probes the deployment with a single PING; a nil client reports ErrNilClient and a failed | Health 用一次 PING 探测部署；nil 客户端返回 ErrNilClient，探测失败返回包装了
// probe returns an error wrapping ErrHealthCheckFailed. | ErrHealthCheckFailed 的错误。
func Health(ctx context.Context, cli redis.UniversalClient) error {
	if cli == nil {
		return ErrNilClient
	}
	if err := cli.Ping(ctx).Err(); err != nil {
		return errx.Wrap(err, ErrHealthCheckFailed)
	}
	return nil
}

// Close releases the connection pool of cli; a nil client reports ErrNilClient. | Close 释放 cli 的连接池；nil 客户端返回 ErrNilClient。
func Close(cli redis.UniversalClient) error {
	if cli == nil {
		return ErrNilClient
	}
	if err := cli.Close(); err != nil {
		return errx.Wrap(err, ErrCloseFailed)
	}
	return nil
}

// addrs returns the effective address list: Addrs when non-empty, otherwise Addr. | addrs 返回生效的地址列表：Addrs 非空时取 Addrs，否则取 Addr。
func (o Options) addrs() []string {
	if len(o.Addrs) > 0 {
		out := make([]string, 0, len(o.Addrs))
		for _, a := range o.Addrs {
			if a = strings.TrimSpace(a); a != "" {
				out = append(out, a)
			}
		}
		return out
	}
	if a := strings.TrimSpace(o.Addr); a != "" {
		return []string{a}
	}
	return nil
}

// simpleOptions maps o onto the single-node client options. | simpleOptions 把 o 映射为单机客户端参数。
func (o Options) simpleOptions() *redis.Options {
	opt := &redis.Options{
		Addr:         strings.TrimSpace(o.Addr),
		Username:     o.Username,
		Password:     o.Password,
		DB:           o.DB,
		PoolSize:     o.PoolSize,
		MinIdleConns: o.MinIdleConns,
		DialTimeout:  o.DialTimeout,
		ReadTimeout:  o.ReadTimeout,
		WriteTimeout: o.WriteTimeout,
	}
	return opt
}

// failoverOptions maps o onto the sentinel client options. | failoverOptions 把 o 映射为哨兵客户端参数。
//
// Only the data-node credentials are filled in: sentinel-specific credentials | 只填充数据节点的凭据；哨兵自身的凭据
// (SentinelUsername / SentinelPassword) are deliberately left to go-redis defaults. | （SentinelUsername / SentinelPassword）有意留给 go-redis 默认值。
func (o Options) failoverOptions() *redis.FailoverOptions {
	return &redis.FailoverOptions{
		MasterName:    strings.TrimSpace(o.MasterName),
		SentinelAddrs: o.addrs(),
		Username:      o.Username,
		Password:      o.Password,
		DB:            o.DB,
		PoolSize:      o.PoolSize,
		MinIdleConns:  o.MinIdleConns,
		DialTimeout:   o.DialTimeout,
		ReadTimeout:   o.ReadTimeout,
		WriteTimeout:  o.WriteTimeout,
	}
}

// clusterOptions maps o onto the cluster client options. | clusterOptions 把 o 映射为集群客户端参数。
//
// DB is not applicable to a cluster (a cluster always has a single keyspace), | 集群没有库序号概念（集群只有单一键空间），
// so Options.DB is ignored in this mode and documented as such. | 因此本模式下 Options.DB 被忽略，并已在文档中写明。
func (o Options) clusterOptions() *redis.ClusterOptions {
	return &redis.ClusterOptions{
		Addrs:        o.addrs(),
		Username:     o.Username,
		Password:     o.Password,
		PoolSize:     o.PoolSize,
		MinIdleConns: o.MinIdleConns,
		DialTimeout:  o.DialTimeout,
		ReadTimeout:  o.ReadTimeout,
		WriteTimeout: o.WriteTimeout,
	}
}
