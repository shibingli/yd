// Package local implements the first-level cache of cachex. | Package local 实现 cachex 的一级缓存。
//
// The cache is a sharded ring of byte buffers. Entries live entirely inside preallocated | 该缓存是分片的环形字节缓冲区。条目完全存放在预分配的
// []byte arenas, so the garbage collector never scans the cached values and a cache holding | []byte 区域中，因此垃圾回收器不会扫描缓存值；即使缓存持有
// hundreds of megabytes adds only a handful of pointers to the heap. Each shard keeps a | 数百兆字节，堆上也只多出少量指针。每个分片维护一个
// pointer-free open-addressed index, a four-row count-min sketch for W-TinyLFU admission | 指针自由的开放寻址索引、用于 W-TinyLFU 准入的四行计数草图，
// and a CLOCK-style access bit that protects frequently read entries from cold newcomers. | 以及保护高频读取条目免受冷启动条目冲击的 CLOCK 风格访问位。
//
// Expiry is lazy: reads never return an expired entry, and a background sampler marks | 过期是惰性的：读取永远不会返回过期条目，后台采样器则逐步标记并释放
// expired entries for reclamation without a per-entry timer. | 过期条目以便回收，无需为每个条目挂定时器。
//
// What it does not do: it does not serialize values, does not talk to Redis and does not | 不做什么：不负责序列化、不访问 Redis，也不
// interpret keys. cachex owns those concerns. | 解释键的语义；这些由 cachex 负责。
package local

import (
	"hash/maphash"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/sysinfo"
)

// Declared sentinel errors, testable with errors.Is. | 已声明的哨兵错误，可用 errors.Is 判定。
var (
	// ErrInvalidOptions indicates an unusable Options value. | ErrInvalidOptions 表示 Options 取值不可用。
	ErrInvalidOptions = errx.New("local: invalid options | local：非法选项")
	// ErrClosed indicates the cache was closed. | ErrClosed 表示缓存已关闭。
	ErrClosed = errx.New("local: cache closed | local：缓存已关闭")
	// ErrEmptyKey indicates Set was called with an empty key. | ErrEmptyKey 表示 Set 收到空键。
	ErrEmptyKey = errx.New("local: empty key | local：空键")
	// ErrKeyTooLong indicates the key exceeds MaxKeyLength. | ErrKeyTooLong 表示键超过 MaxKeyLength。
	ErrKeyTooLong = errx.New("local: key too long | local：键过长")
	// ErrEntryTooLarge indicates one entry exceeds the shard budget. | ErrEntryTooLarge 表示单条目超过分片预算。
	ErrEntryTooLarge = errx.New("local: entry too large | local：条目过大")
)

// MaxKeyLength is the largest key the ring accepts, in bytes. | MaxKeyLength 是环形缓存接受的键长度上限（字节）。
const MaxKeyLength = 1024

// Defaults used when Options leaves a field at its zero value. | Options 字段留零值时使用的默认值。
const (
	defaultCleanupInterval = time.Second
	defaultSampleSize      = 256
	minShardBytes          = 4 << 10
	maxShardBytes          = 1 << 30
	minMaxCost             = 32 << 20
	maxMaxCost             = 1 << 30
)

// Options configures a local cache. | Options 配置本地缓存。
type Options struct {
	// MaxCost is the total byte budget across all shards; <=0 derives a budget from the | MaxCost 是所有分片合计的字节预算；<=0 时按当前进程可见内存推导
	// memory visible to the process (one sixteenth, clamped to 32 MiB..1 GiB). | （十六分之一，夹在 32 MiB..1 GiB）。
	MaxCost int64
	// Shards is the number of independently locked shards; <=0 derives it from GOMAXPROCS. | Shards 是独立加锁的分片数；<=0 时由 GOMAXPROCS 推导。
	Shards int
	// MaxEntrySize is the largest single entry; <=0 derives it from the shard size. | MaxEntrySize 是单条目上限；<=0 时由分片大小推导。
	MaxEntrySize int
	// CleanupInterval is the background sweep period; <=0 means one second. | CleanupInterval 是后台清扫周期；<=0 表示一秒。
	CleanupInterval time.Duration
	// SampleSize is the number of entries examined per shard per sweep; <=0 means 256. | SampleSize 是每分片每次清扫检查的条目数；<=0 表示 256。
	SampleSize int
}

// Stats is a point-in-time view of the cache counters. | Stats 是缓存计数器的时间点视图。
type Stats struct {
	// Hits is the number of successful Get/GetAppend/View calls. | Hits 是 Get/GetAppend/View 成功的次数。
	Hits uint64
	// Misses is the number of lookups that found nothing or an expired entry. | Misses 是未找到或命中的条目已过期的查询次数。
	Misses uint64
	// Sets is the number of entries accepted by the ring. | Sets 是环形缓存接受的条目数。
	Sets uint64
	// Deletes is the number of explicit Delete calls that removed an entry. | Deletes 是实际移除条目的显式 Delete 次数。
	Deletes uint64
	// Evictions is the number of live entries evicted to make room. | Evictions 是为腾空间而驱逐的存活条目数。
	Evictions uint64
	// Expired is the number of expired-entry observations (reads and sweeps). | Expired 是观察到条目过期的次数（读取与清扫合计）。
	Expired uint64
	// Rejected is the number of inserts rejected by TinyLFU admission. | Rejected 是被 TinyLFU 准入拒绝的插入次数。
	Rejected uint64
	// Identical is the number of same-size Set calls that skipped the value copy. | Identical 是同尺寸且值相同、跳过值拷贝的 Set 次数。
	Identical uint64
	// Entries is the current number of live entries. | Entries 是当前存活条目数。
	Entries int64
	// Cost is the current live byte cost. | Cost 是当前存活字节开销。
	Cost int64
	// Capacity is the configured byte budget. | Capacity 是配置的字节预算。
	Capacity int64
	// Shards is the number of shards in use. | Shards 是实际使用的分片数。
	Shards int
}

// Cache is a sharded, size-bounded byte cache safe for concurrent use. | Cache 是分片、有大小上限且可并发使用的字节缓存。
type Cache struct {
	stop      chan struct{}
	done      chan struct{}
	shards    []*shard
	closeOnce sync.Once
	closed    atomic.Bool
	seed      maphash.Seed
	cleanup   time.Duration
	sample    int
	capacity  int64
}

// New builds a cache from o. | New 按 o 构造一个缓存。
//
// The zero value of o is usable: the budget, shard count and entry limit are derived from | o 的零值可用：预算、分片数与条目上限会由运行时环境推导。
// the runtime environment, so callers never have to pass a core count or a memory size. | 调用方无需传入核数或内存容量。
func New(o Options) (*Cache, error) {
	capacity := o.MaxCost
	if capacity <= 0 {
		capacity = defaultMaxCost()
	}
	if capacity < minShardBytes {
		return nil, errx.Detail(ErrInvalidOptions, "max cost %d is below %d | 最大开销 %d 低于 %d", capacity, minShardBytes, capacity, minShardBytes)
	}
	if capacity > maxShardBytes*int64(maxShards) {
		capacity = maxShardBytes * int64(maxShards)
	}

	shards := o.Shards
	if shards <= 0 {
		shards = nextPowerOfTwo(sysinfo.Parallelism() * 4)
	}
	shards = clampInt(shards, 1, maxShards)
	shards = nextPowerOfTwo(shards)
	// Each shard needs at least minShardBytes; shrink the shard count when the budget is tiny. | 每片至少需要 minShardBytes；预算很小时收缩分片数。
	if int64(shards)*minShardBytes > capacity {
		shards = int(capacity / minShardBytes)
		if shards < 1 {
			shards = 1
		}
		shards = nextPowerOfTwo(shards)
		if shards > maxShards {
			shards = maxShards
		}
	}
	shardBytes := capacity / int64(shards)
	if shardBytes > maxShardBytes {
		shardBytes = maxShardBytes
	}

	maxEntry := o.MaxEntrySize
	if maxEntry <= 0 {
		maxEntry = int(shardBytes / 8)
		if maxEntry < 256 {
			maxEntry = 256
		}
		if int64(maxEntry) > shardBytes/2 {
			maxEntry = int(shardBytes / 2)
		}
	}
	if int64(maxEntry) > shardBytes/2 {
		return nil, errx.Detail(ErrInvalidOptions, "max entry %d exceeds half of shard %d | 单条目上限 %d 超过分片 %d 的一半",
			maxEntry, shardBytes, maxEntry, shardBytes)
	}
	if maxEntry < 64 {
		return nil, errx.Detail(ErrInvalidOptions, "max entry %d is too small | 单条目上限 %d 太小", maxEntry, maxEntry)
	}

	cleanup := o.CleanupInterval
	if cleanup <= 0 {
		cleanup = defaultCleanupInterval
	}
	sample := o.SampleSize
	if sample <= 0 {
		sample = defaultSampleSize
	}

	c := &Cache{
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		shards:   make([]*shard, shards),
		seed:     maphash.MakeSeed(),
		cleanup:  cleanup,
		sample:   sample,
		capacity: shardBytes * int64(shards),
	}
	for i := range c.shards {
		c.shards[i] = newShard(int(shardBytes), maxEntry)
	}
	go c.janitor()
	return c, nil
}

// Get returns a copy of the value stored under key. | Get 返回 key 对应值的副本。
//
// It reports false for a missing or expired entry. The returned slice is owned by the | 条目缺失或已过期时返回 false。返回的切片归调用方所有，
// caller and never aliases the ring. | 与环形缓冲区不共享底层数组。
func (c *Cache) Get(key string) ([]byte, bool) {
	if c.closed.Load() {
		return nil, false
	}
	hash := hashString(c.seed, key)
	return c.shardForHash(hash).get(hash, key)
}

// GetAppend appends a copy of the value stored under key to dst. | GetAppend 把 key 对应值的副本追加到 dst。
//
// It is the one-copy form for callers that already own a reusable buffer. | 它是为已持有可复用缓冲区的调用方准备的一次拷贝形式。
func (c *Cache) GetAppend(dst []byte, key string) ([]byte, bool) {
	if c.closed.Load() {
		return dst, false
	}
	hash := hashString(c.seed, key)
	return c.shardForHash(hash).getAppend(hash, key, dst)
}

// View calls fn with a zero-copy view of the value stored under key. | View 以零拷贝视图把 key 对应值交给 fn。
//
// The view aliases the ring: fn must not retain it, mutate it, or call back into this | 该视图与环形缓冲区共享底层数组：fn 不得保留或修改它，也不得回调本缓存。
// cache. The shard's read lock is held while fn runs, so writers to the same shard wait; | fn 执行期间持有分片读锁，因此同分片的写者会等待；
// multiple readers proceed concurrently. fn sees no expired entry. Get/GetAppend/View all | 多个读者可并发执行。fn 不会看到已过期条目。Get/GetAppend/View 都在
// copy or view under the read lock and sample the access bit and sketch after releasing it, | 读锁内完成拷贝或视图，并在释放读锁后采样访问位与访问草图，
// so reads never take the write lock. | 因此读取路径无需获取写锁。
func (c *Cache) View(key string, fn func(value []byte) error) (bool, error) {
	if fn == nil {
		return false, errx.Detail(ErrInvalidOptions, "nil view function | 视图函数为 nil")
	}
	if c.closed.Load() {
		return false, ErrClosed
	}
	hash := hashString(c.seed, key)
	return c.shardForHash(hash).view(hash, key, fn)
}

// Set stores a copy of value under key with the given ttl. | Set 以给定 ttl 把 value 的副本存到 key 下。
//
// ttl <= 0 means the entry never expires. TinyLFU admission may decline a cold newcomer | ttl <= 0 表示永不过期。缓存受压时 TinyLFU 准入可能拒绝冷启动条目，
// while the cache is under pressure; that is not an error, and Stats.Rejected counts it. | 这不是错误，由 Stats.Rejected 计数。
func (c *Cache) Set(key string, value []byte, ttl time.Duration) error {
	if c.closed.Load() {
		return ErrClosed
	}
	if key == "" {
		return ErrEmptyKey
	}
	if len(key) > MaxKeyLength {
		return errx.Detail(ErrKeyTooLong, "length %d exceeds %d | 长度 %d 超过 %d", len(key), MaxKeyLength, len(key), MaxKeyLength)
	}
	var expireAt int64
	if ttl > 0 {
		expireAt = time.Now().Add(ttl).UnixNano()
	}
	hash := hashString(c.seed, key)
	return c.shardForHash(hash).set(hash, key, value, expireAt)
}

// Delete removes key and reports whether an entry was actually removed. | Delete 移除 key，并报告是否确实移除了条目。
func (c *Cache) Delete(key string) bool {
	if c.closed.Load() {
		return false
	}
	hash := hashString(c.seed, key)
	return c.shardForHash(hash).delete(hash, key)
}

// DeletePrefix removes every entry whose key starts with prefix and returns the count. | DeletePrefix 移除所有以 prefix 开头的条目并返回数量。
//
// It scans the rings and is intended for namespace-level invalidation, not for hot paths. | 它会扫描环形缓冲区，用于命名空间级失效，不用于热路径。
func (c *Cache) DeletePrefix(prefix string) int {
	if c.closed.Load() {
		return 0
	}
	removed := 0
	for _, sh := range c.shards {
		removed += sh.deletePrefix(prefix)
	}
	return removed
}

// Clear removes every entry and resets the frequency sketch. | Clear 移除所有条目并重置频率草图。
func (c *Cache) Clear() {
	if c.closed.Load() {
		return
	}
	for _, sh := range c.shards {
		sh.clear()
	}
}

// Len returns the number of live entries. | Len 返回存活条目数。
func (c *Cache) Len() int {
	var n int64
	for _, sh := range c.shards {
		n += sh.liveEntries()
	}
	return int(n)
}

// Cost returns the live byte cost. | Cost 返回存活字节开销。
func (c *Cache) Cost() int64 {
	var n int64
	for _, sh := range c.shards {
		n += sh.liveCost()
	}
	return n
}

// Capacity returns the configured byte budget. | Capacity 返回配置的字节预算。
func (c *Cache) Capacity() int64 { return c.capacity }

// Stats returns a consistent-enough snapshot of all shard counters. | Stats 返回所有分片计数器的近似一致快照。
func (c *Cache) Stats() Stats {
	out := Stats{Capacity: c.capacity, Shards: len(c.shards)}
	for _, sh := range c.shards {
		out.Hits += sh.stats.loadHits()
		out.Misses += sh.stats.misses.Load()
		out.Sets += sh.stats.sets.Load()
		out.Deletes += sh.stats.deletes.Load()
		out.Evictions += sh.stats.evictions.Load()
		out.Expired += sh.stats.expired.Load()
		out.Rejected += sh.stats.rejected.Load()
		out.Identical += sh.stats.identical.Load()
		out.Entries += sh.liveEntries()
		out.Cost += sh.liveCost()
	}
	return out
}

// Sweep examines up to SampleSize entries per shard and reclaims expired ones. | Sweep 每分片检查至多 SampleSize 个条目并回收过期条目。
//
// The background janitor calls it periodically; it is exported for tests and for callers | 后台清扫器会周期性调用；导出它是为了测试，以及让调用方在
// that want to force a pass after clock changes. | 时钟变化后强制跑一轮。
func (c *Cache) Sweep() {
	if c.closed.Load() {
		return
	}
	now := time.Now().UnixNano()
	for _, sh := range c.shards {
		sh.sweep(now, c.sample)
	}
}

// Close stops the background janitor. It is idempotent and does not wait for in-flight | Close 停止后台清扫器。它幂等，且不等待进行中的操作：
// operations; a closed cache reports misses and rejects writes. | 关闭后的缓存报告未命中并拒绝写入。
func (c *Cache) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.stop)
		<-c.done
	})
	return nil
}

// janitor periodically samples every shard. | janitor 周期性采样每个分片。
func (c *Cache) janitor() {
	defer close(c.done)
	ticker := time.NewTicker(c.cleanup)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case now := <-ticker.C:
			n := now.UnixNano()
			for _, sh := range c.shards {
				sh.sweep(n, c.sample)
			}
		}
	}
}

// shardForHash maps a precomputed hash to a shard. | shardForHash 把预计算哈希映射到分片。
//
// Sharding uses the high bits so the low bits stay free to index the shard's slot table.
// Using the low bits for both would confine each shard to 1/N of its slots and turn every
// lookup into a long probe chain. | 分片使用高位，把低位留给分片槽表索引。若两处都用低位，
// 每个分片只会用到 1/N 的槽位，所有查找都会退化成超长探测链。
func (c *Cache) shardForHash(hash uint64) *shard {
	return c.shards[(hash>>32)&uint64(len(c.shards)-1)]
}

// defaultMaxCost derives the byte budget from the memory visible to the process. | defaultMaxCost 由进程可见内存推导字节预算。
func defaultMaxCost() int64 {
	limit := sysinfo.MemoryLimit()
	if limit == 0 {
		limit = sysinfo.MemoryTotal()
	}
	if limit == 0 {
		return 64 << 20
	}
	v := int64(limit / 16)
	if v < minMaxCost {
		v = minMaxCost
	}
	if v > maxMaxCost {
		v = maxMaxCost
	}
	return v
}

// maxShards bounds the shard array. | maxShards 限制分片数组大小。
const maxShards = 256

// nextPowerOfTwo returns the smallest power of two >= n (at least 1). | nextPowerOfTwo 返回不小于 n 的最小 2 的幂（至少为 1）。
func nextPowerOfTwo(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// clampInt bounds v to [lo, hi]. | clampInt 把 v 限制在 [lo, hi]。
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
