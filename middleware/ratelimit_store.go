package middleware

import (
	"context"
	"math"
	"sync"
	"time"
)

// rateLimitShards is the number of independently locked bucket maps. | rateLimitShards 是相互独立加锁的桶映射分片数。
const rateLimitShards = 16

// defaultRateLimitCleanup is the lazy sweep interval used when CleanupInterval is zero. | defaultRateLimitCleanup 是 CleanupInterval 为零时使用的惰性清理间隔。
const defaultRateLimitCleanup = time.Minute

// Decision is the outcome of one token acquisition. | Decision 是一次取令牌的结果。
type Decision struct {
	// Allowed reports whether the request may proceed. | Allowed 报告请求是否可以继续。
	Allowed bool
	// Remaining is the number of whole tokens left in the bucket. | Remaining 是桶中剩余的整令牌数。
	Remaining int
	// RetryAfter is the minimum wait before the next token is available. | RetryAfter 是获得下一个令牌所需的最短等待。
	RetryAfter time.Duration
	// ResetAfter is the time until the bucket is full again. | ResetAfter 是桶重新装满所需的时间。
	ResetAfter time.Duration
}

// LimitStore is the backend of a rate limiter. | LimitStore 是限流器的后端。
//
// Allow must be atomic for a given key: concurrent calls through the same store may never hand out
// more than burst tokens in total. The middleware turns an error into the configured failure policy
// (fail open or fail closed), so implementations report storage failures as errors rather than
// inventing a decision. | 对同一个键，Allow 必须是原子的：经同一存储的并发调用合计不得发出超过 burst
// 个令牌。中间件会把错误转换为配置的故障策略（失败开放或失败关闭），因此实现应把存储故障如实返回，
// 而不是编造一个决定。
type LimitStore interface {
	// Allow tries to take one token for key. | Allow 为 key 尝试取走一个令牌。
	Allow(ctx context.Context, key string, rate float64, burst int) (Decision, error)
}

// MemoryStore is the single-process store: sharded token buckets behind one mutex per shard. | MemoryStore 是单机存储：每个分片一把互斥锁的分片令牌桶。
//
// Buckets are swept lazily on access, so the store owns no goroutine and needs no Close. | 桶在访问时
// 惰性清理，因此该存储不占用协程，也没有 Close。
type MemoryStore struct {
	shards   [rateLimitShards]rateLimitShard
	mask     uint32
	interval time.Duration
}

// rateLimitShard is one independently locked shard. | rateLimitShard 是一个独立加锁的分片。
type rateLimitShard struct {
	lastSweep time.Time
	buckets   map[string]*tokenBucket
	mu        sync.Mutex
}

// tokenBucket is a single client's bucket state. | tokenBucket 是单个客户端的桶状态。
type tokenBucket struct {
	last   time.Time
	tokens float64
	burst  float64 // capacity captured at creation | 创建时捕获的容量
	rate   float64 // refill rate captured at creation | 创建时捕获的补充速率
}

// NewMemoryStore builds a single-process store. A non-positive cleanup interval uses one minute.
// | NewMemoryStore 构建单机存储；非正的清理间隔使用一分钟。
func NewMemoryStore(cleanupInterval time.Duration) *MemoryStore {
	if cleanupInterval <= 0 {
		cleanupInterval = defaultRateLimitCleanup
	}
	s := &MemoryStore{mask: rateLimitShards - 1, interval: cleanupInterval}
	for i := range s.shards {
		s.shards[i].buckets = make(map[string]*tokenBucket, 8)
	}
	return s
}

// Allow implements LimitStore. | Allow 实现 LimitStore。
func (s *MemoryStore) Allow(ctx context.Context, key string, rate float64, burst int) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	rate, burst = normalizeLimit(rate, burst)
	mask := s.mask
	if mask == 0 {
		mask = rateLimitShards - 1
	}
	interval := s.interval
	if interval <= 0 {
		interval = defaultRateLimitCleanup
	}
	shard := &s.shards[s.shardIndexOf(key, mask)]
	shard.mu.Lock()
	defer shard.mu.Unlock()

	if shard.buckets == nil {
		shard.buckets = make(map[string]*tokenBucket, 8)
	}
	now := time.Now()
	if now.Sub(shard.lastSweep) >= interval {
		s.sweepLocked(shard)
		shard.lastSweep = now
	}

	b, ok := shard.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: float64(burst), burst: float64(burst), rate: rate, last: now}
		shard.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(b.burst, b.tokens+elapsed*b.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return Decision{
			Allowed:    true,
			Remaining:  int(b.tokens),
			ResetAfter: refillDuration(b.tokens, b.burst, b.rate),
		}, nil
	}
	wait := durationFromSeconds((1 - b.tokens) / b.rate)
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	return Decision{RetryAfter: wait, ResetAfter: wait}, nil
}

// shardIndexOf hashes key with FNV-1a into a shard index. | shardIndexOf 用 FNV-1a 把 key 哈希到分片下标。
func (s *MemoryStore) shardIndexOf(key string, mask uint32) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return h & mask
}

// sweepLocked drops buckets that have become indistinguishable from a fresh one. | sweepLocked 丢弃那些已经与新建桶没有区别的桶。
func (s *MemoryStore) sweepLocked(shard *rateLimitShard) {
	now := time.Now()
	for key, b := range shard.buckets {
		if b.tokens >= b.burst {
			delete(shard.buckets, key)
			continue
		}
		if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 && b.tokens+elapsed*b.rate >= b.burst {
			delete(shard.buckets, key)
		}
	}
}

// refillDuration is the time until a bucket with tokens is full again. | refillDuration 是桶从当前令牌数重新装满所需的时间。
func refillDuration(tokens, burst, rate float64) time.Duration {
	if tokens >= burst {
		return 0
	}
	return durationFromSeconds((burst - tokens) / rate)
}

// maxDurationSeconds is the largest number of seconds a time.Duration can hold. | maxDurationSeconds 是 time.Duration 能容纳的最大秒数。
var maxDurationSeconds = float64(math.MaxInt64) / float64(time.Second)

// durationFromSeconds converts seconds without overflowing time.Duration. | durationFromSeconds 把秒换算为 time.Duration 且不会溢出。
//
// An extreme rate (a token every few years) would otherwise wrap the float-to-int64 conversion and
// produce a negative duration. | 极端速率（数年一个令牌）会让浮点到 int64 的转换回绕并产生负时长。
func durationFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	if seconds >= maxDurationSeconds {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(seconds * float64(time.Second))
}

// normalizeLimit turns non-positive options into the documented defaults. | normalizeLimit 把非正选项转换为文档化的默认值。
func normalizeLimit(rate float64, burst int) (float64, int) {
	if rate <= 0 {
		rate = 1
	}
	if burst <= 0 {
		burst = int(math.Ceil(rate))
		if burst < 1 {
			burst = 1
		}
	}
	return rate, burst
}
