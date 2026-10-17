package middleware

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/shibingli/yd/core/errx"
)

// defaultRedisKeyPrefix separates rate-limit keys from every other key in a shared Redis. | defaultRedisKeyPrefix 在共享 Redis 中把限流键与其它键隔开。
const defaultRedisKeyPrefix = "yd:rl:"

// redisKeyMaxLength is the longest derived key kept verbatim; longer keys are hashed. | redisKeyMaxLength 是原样保留的派生键最大长度；更长的键会被哈希。
const redisKeyMaxLength = 200

// redisTTLMin and redisTTLMax bound the bucket key lifetime. | redisTTLMin 与 redisTTLMax 限定桶键的生存时间。
const (
	redisTTLMin = time.Minute
	redisTTLMax = 24 * time.Hour
)

var (
	// ErrRedisStoreClient reports a Redis store built without a client. | ErrRedisStoreClient 表示未提供客户端的 Redis 存储。
	ErrRedisStoreClient = errx.New("redis rate-limit store requires a client | Redis 限流存储需要客户端")
	// ErrStoreDecision reports a backend that returned an unparsable decision. | ErrStoreDecision 表示后端返回了无法解析的决定。
	ErrStoreDecision = errx.New("rate-limit store returned an invalid decision | 限流存储返回了非法的决定")
)

// tokenBucketLua takes one token atomically. | tokenBucketLua 原子地取走一个令牌。
//
// KEYS[1] is the bucket; ARGV are rate (tokens/s), burst, now in Unix milliseconds and the key TTL
// in milliseconds. The script returns {allowed, remaining, retry_ms, reset_ms}. Reading, refilling,
// deciding and writing back inside one script is what makes the limit exact across instances: with
// separate commands, two replicas can both read the same token count and both hand it out. | KEYS[1]
// 是桶；ARGV 依次是速率（令牌/秒）、容量、当前 Unix 毫秒与键 TTL 毫秒。脚本返回
// {allowed, remaining, retry_ms, reset_ms}。把读取、补充、判定与写回放进同一个脚本，正是跨实例
// 精确限流的关键：分成多条命令时，两个副本会读到同一份令牌数并各自发出。
const tokenBucketLua = `
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])

local vals = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(vals[1])
local ts = tonumber(vals[2])
if tokens == nil or ts == nil then
  tokens = burst
  ts = now
end

local elapsed = (now - ts) / 1000
if elapsed > 0 then
  tokens = math.min(burst, tokens + elapsed * rate)
  ts = now
end

local allowed = 0
local retry = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  retry = math.ceil((1 - tokens) * 1000 / rate)
  if retry < 1 then retry = 1 end
end

local reset = 0
if tokens < burst then
  reset = math.ceil((burst - tokens) * 1000 / rate)
end

-- Fixed-point strings, never scientific notation: some embedded Lua engines (the miniredis
-- test double among them) reject exponents in tonumber, which would reset the bucket.
-- 使用定点小数而非科学计数法：部分内嵌 Lua 引擎（含 miniredis 测试替身）的 tonumber 不接受指数，
-- 会把桶误判为新桶而重置。
redis.call('HSET', KEYS[1], 'tokens', string.format('%.9f', tokens), 'ts', string.format('%.0f', ts))
redis.call('PEXPIRE', KEYS[1], ttl)
return {allowed, math.floor(tokens), retry, reset}
`

// RedisStoreOptions configures the multi-instance store. | RedisStoreOptions 配置多机存储。
type RedisStoreOptions struct {
	// Client is the Redis client shared by every replica. | Client 是各副本共享的 Redis 客户端。
	Client redis.UniversalClient
	// Prefix separates one rule's keys from another's; empty means "yd:rl:". | Prefix 把一条规则的键与
	// 另一条隔开；为空表示 "yd:rl:"。
	Prefix string
	// TTL pins the bucket key lifetime; zero derives it from rate and burst. | TTL 固定桶键生存时间；
	// 零表示由速率与容量推导。
	TTL time.Duration
}

// RedisStore is the multi-instance LimitStore backed by one Redis. | RedisStore 是由同一个 Redis 支撑的多机 LimitStore。
//
// Time is read from the Redis server, not from each replica's clock: clock skew between instances
// would otherwise distort the refill rate. The TIME command and the EVAL are two round trips, but
// the token decision itself is atomic; using a slightly older timestamp only underestimates the
// refill, which can never over-issue. | 时间取自 Redis 服务器而非各副本的本地时钟：实例间的时钟漂移
// 会让补充速率失真。TIME 与 EVAL 是两次往返，但令牌判定本身是原子的；使用稍旧的时间戳只会低估补充量，
// 绝不会超发。
type RedisStore struct {
	client redis.UniversalClient
	prefix string
	ttl    time.Duration
}

// NewRedisStore builds a multi-instance store. | NewRedisStore 构建多机存储。
func NewRedisStore(o RedisStoreOptions) (*RedisStore, error) {
	if o.Client == nil {
		return nil, ErrRedisStoreClient
	}
	prefix := o.Prefix
	if prefix == "" {
		prefix = defaultRedisKeyPrefix
	}
	return &RedisStore{client: o.Client, prefix: prefix, ttl: o.TTL}, nil
}

// Allow implements LimitStore. | Allow 实现 LimitStore。
func (s *RedisStore) Allow(ctx context.Context, key string, rate float64, burst int) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	rate, burst = normalizeLimit(rate, burst)
	now, err := s.client.Time(ctx).Result()
	if err != nil {
		return Decision{}, err
	}
	ttl := s.ttlFor(rate, burst)
	vals, err := s.client.Eval(ctx, tokenBucketLua, []string{s.prefix + redisBucketKey(key)},
		strconv.FormatFloat(rate, 'f', -1, 64), burst, now.UnixMilli(), ttl.Milliseconds()).Int64Slice()
	if err != nil {
		return Decision{}, err
	}
	if len(vals) != 4 {
		return Decision{}, errx.Detail(ErrStoreDecision, "expected 4 values, got %[1]d | 期望 4 个值，实际 %[1]d", len(vals))
	}
	return Decision{
		Allowed:    vals[0] == 1,
		Remaining:  int(vals[1]),
		RetryAfter: time.Duration(vals[2]) * time.Millisecond,
		ResetAfter: time.Duration(vals[3]) * time.Millisecond,
	}, nil
}

// ttlFor derives a bucket lifetime that keeps the key at least until the bucket is full again.
// | ttlFor 推导桶键生存时间，至少保留到桶重新装满之后。
func (s *RedisStore) ttlFor(rate float64, burst int) time.Duration {
	if s.ttl > 0 {
		return s.ttl
	}
	seconds := 2 * float64(burst) / rate
	if seconds >= redisTTLMax.Seconds() {
		return redisTTLMax
	}
	if seconds <= redisTTLMin.Seconds() {
		return redisTTLMin
	}
	return time.Duration(seconds * float64(time.Second))
}

// redisBucketKey keeps user-derived values printable and bounded before they become a Redis key.
// | redisBucketKey 在用户派生值成为 Redis 键之前，使其保持可打印且有界。
//
// Long or control-character values (a user agent, a header echoed verbatim) are hashed: Redis keys
// end up in dashboards and logs, and an unbounded key is a cheap memory amplification. | 过长或含控制
// 字符的取值（原样回显的 UA、头部）会被哈希：Redis 键会出现在监控面板与日志里，而无界键是廉价的
// 内存放大面。
func redisBucketKey(key string) string {
	if len(key) <= redisKeyMaxLength && printableASCII(key) {
		return key
	}
	var h uint64 = 14695981039346656037
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= 1099511628211
	}
	return "h:" + strconv.FormatUint(h, 16)
}

// printableASCII reports whether every byte is a visible ASCII character. | printableASCII 报告每个字节是否都是可见 ASCII 字符。
func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
