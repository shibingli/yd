package cachebench

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	theine "github.com/Yiling-J/theine-go"
	bigcache "github.com/allegro/bigcache/v3"
	"github.com/coocood/freecache"
	ristretto "github.com/dgraph-io/ristretto/v2"
	lru "github.com/hashicorp/golang-lru/v2"
	ttlcache "github.com/jellydator/ttlcache/v3"
	gocache "github.com/patrickmn/go-cache"

	"github.com/shibingli/yd/data/cachex/local"
)

// Benchmark dimensions. | 基准维度。
const (
	// maxKeys is the size of the shared key pool. | maxKeys 是共享键池大小。
	maxKeys = 50_000
	// basicKeys is the working set for Get/Set/Mixed benchmarks. | basicKeys 是 Get/Set/Mixed 基准的工作集。
	basicKeys = 20_000
	// basicCapacity is twice the working set so the basic benchmarks measure raw throughput,
	// not eviction. | basicCapacity 是工作集的两倍，使基础基准度量原始吞吐而非驱逐。
	basicCapacity = basicKeys * 2
	// hotKeys is the hot subset used by the hit-ratio trace. | hotKeys 是命中率轨迹使用的热键子集。
	hotKeys = 500
	// hitRatioCapacity is the capacity used by the hit-ratio trace. | hitRatioCapacity 是命中率轨迹使用的容量。
	hitRatioCapacity = 5_000
	// hitRatioAccesses is the trace length. | hitRatioAccesses 是轨迹长度。
	hitRatioAccesses = 200_000
)

// benchSink keeps single-threaded results observable. | benchSink 让单线程结果保持可观测。
var benchSink int

// benchSinkAtomic is the concurrency-safe sink. | benchSinkAtomic 是并发安全 sink。
var benchSinkAtomic atomic.Int64

// keysOnce initializes the shared key pool once. | keysOnce 只初始化一次共享键池。
var (
	keysOnce sync.Once
	keys     []string
	keyBytes [][]byte
)

// benchData returns the shared string and byte keys. | benchData 返回共享的字符串键与字节键。
func benchData() ([]string, [][]byte) {
	keysOnce.Do(func() {
		keys = make([]string, maxKeys)
		keyBytes = make([][]byte, maxKeys)
		for i := range keys {
			keys[i] = fmt.Sprintf("key:%06d", i)
			keyBytes[i] = []byte(keys[i])
		}
	})
	return keys, keyBytes
}

// benchSelected reports whether name passes YD_BENCH_ONLY. | benchSelected 判断 name 是否通过 YD_BENCH_ONLY。
//
// Set YD_BENCH_ONLY to a comma-separated list to run each implementation in its own | 把它设为逗号分隔的列表，可让每个实现独立进程运行，
// process and remove same-process ordering and GC bias; empty selects everything. | 消除同进程顺序与 GC 偏差；空值表示全部选中。
func benchSelected(name string) bool {
	want := os.Getenv("YD_BENCH_ONLY")
	if want == "" {
		return true
	}
	for _, part := range strings.Split(want, ",") {
		if strings.TrimSpace(part) == name {
			return true
		}
	}
	return false
}

// setup is one cache construction context. | setup 是一次缓存构造上下文。
type setup struct {
	keys      []string
	keyBytes  [][]byte
	capacity  int
	valueSize int
	ttl       time.Duration
	hardMaxMB int
}

// benchCache is the common adapter surface. | benchCache 是统一的适配接口。
//
// get returns the value length, or 0 on a miss. Returning a length instead of the value keeps | get 返回值长度，未命中返回 0。返回长度而不是值，
// the call observable without forcing every implementation into the same copy semantics. | 使调用可观测，同时不强迫所有实现采用相同的复制语义。
type benchCache interface {
	get(id int) int
	set(id int, value []byte)
	wait()
	close()
}

// parallelReader lets an adapter use one caller-owned buffer per benchmark goroutine,
// avoiding shared mutable state in the parallel read benchmark. | parallelReader 让适配器
// 为每个基准协程使用各自的调用方缓冲区，避免并行读基准共享可变状态。
type parallelReader interface {
	getParallel(id int, buf []byte) (int, []byte)
}

// impl is one implementation under comparison. | impl 是参与对比的一个实现。
type impl struct {
	name          string
	new           func(setup) (benchCache, error)
	ttlCapable    bool
	capacityBound bool
}

// basicSetup builds the no-eviction setup for size. | basicSetup 构造 size 对应的无驱逐 setup。
func basicSetup(size int) setup {
	allKeys, allBytes := benchData()
	return setup{
		keys:      allKeys[:basicKeys],
		keyBytes:  allBytes[:basicKeys],
		capacity:  basicCapacity,
		valueSize: size,
	}
}

// ttlSetup builds the TTL setup for size. | ttlSetup 构造 size 对应的 TTL setup。
func ttlSetup(size int) setup {
	s := basicSetup(size)
	s.ttl = time.Hour
	return s
}

// hitRatioSetup builds the capacity-constrained setup for the hit-ratio trace. | hitRatioSetup 构造命中率轨迹的受限容量 setup。
func hitRatioSetup() setup {
	allKeys, allBytes := benchData()
	return setup{
		keys:      allKeys,
		keyBytes:  allBytes,
		capacity:  hitRatioCapacity,
		valueSize: 64,
		hardMaxMB: 2,
	}
}

// newYDLocal returns an adapter for one read mode of the yd local cache. | newYDLocal 返回 yd 本地缓存一种读模式的适配器。
func newYDLocal(mode string) func(setup) (benchCache, error) {
	return func(s setup) (benchCache, error) {
		maxCost := int64(s.capacity) * int64(s.valueSize+64)
		c, err := local.New(local.Options{
			MaxCost:         maxCost,
			MaxEntrySize:    s.valueSize + 256,
			CleanupInterval: time.Hour,
		})
		if err != nil {
			return nil, err
		}
		return &ydLocal{c: c, mode: mode, keys: s.keys, ttl: s.ttl}, nil
	}
}

// ydLocal adapts the yd L1 cache with copy, append and zero-copy read modes. | ydLocal 以复制、追加与零拷贝三种读模式适配 yd L1 缓存。
type ydLocal struct {
	c    *local.Cache
	keys []string
	buf  []byte
	mode string
	ttl  time.Duration
}

func (y *ydLocal) get(id int) int {
	switch y.mode {
	case "view":
		n := 0
		_, _ = y.c.View(y.keys[id], func(v []byte) error {
			n = len(v)
			return nil
		})
		return n
	case "getappend":
		y.buf, _ = y.c.GetAppend(y.buf[:0], y.keys[id])
		return len(y.buf)
	default:
		v, ok := y.c.Get(y.keys[id])
		if !ok {
			return 0
		}
		return len(v)
	}
}

func (y *ydLocal) getParallel(id int, buf []byte) (int, []byte) {
	if y.mode != "getappend" {
		return y.get(id), buf
	}
	buf, _ = y.c.GetAppend(buf[:0], y.keys[id])
	return len(buf), buf
}

func (y *ydLocal) set(id int, value []byte) { _ = y.c.Set(y.keys[id], value, y.ttl) }
func (y *ydLocal) wait()                    {}
func (y *ydLocal) close()                   { _ = y.c.Close() }

// newFreeCache returns an adapter for FreeCache's copying or zero-copy read. | newFreeCache 返回 FreeCache 复制或零拷贝读的适配器。
func newFreeCache(mode string) func(setup) (benchCache, error) {
	return func(s setup) (benchCache, error) {
		size := s.capacity * (s.valueSize + 64)
		return &freeCache{c: freecache.NewCache(size), mode: mode, keyBytes: s.keyBytes, ttl: s.ttl}, nil
	}
}

// freeCache adapts FreeCache. | freeCache 适配 FreeCache。
type freeCache struct {
	c        *freecache.Cache
	keyBytes [][]byte
	mode     string
	ttl      time.Duration
}

func (f *freeCache) get(id int) int {
	if f.mode == "getfn" {
		n := 0
		_ = f.c.GetFn(f.keyBytes[id], func(v []byte) error {
			n = len(v)
			return nil
		})
		return n
	}
	v, err := f.c.Get(f.keyBytes[id])
	if err != nil {
		return 0
	}
	return len(v)
}

func (f *freeCache) set(id int, value []byte) {
	expire := 0
	if f.ttl > 0 {
		expire = int(f.ttl / time.Second)
		if expire < 1 {
			expire = 1
		}
	}
	_ = f.c.Set(f.keyBytes[id], value, expire)
}

func (f *freeCache) wait()  {}
func (f *freeCache) close() {}

// newRistretto builds a Ristretto v2 cache. | newRistretto 构造 Ristretto v2 缓存。
func newRistretto(s setup) (benchCache, error) {
	c, err := ristretto.NewCache[string, []byte](&ristretto.Config[string, []byte]{
		NumCounters:        int64(s.capacity) * 10,
		MaxCost:            int64(s.capacity),
		BufferItems:        64,
		IgnoreInternalCost: true,
	})
	if err != nil {
		return nil, err
	}
	return &ristrettoCache{c: c, keys: s.keys, ttl: s.ttl}, nil
}

// ristrettoCache adapts Ristretto v2. | ristrettoCache 适配 Ristretto v2。
type ristrettoCache struct {
	c    *ristretto.Cache[string, []byte]
	keys []string
	ttl  time.Duration
}

func (r *ristrettoCache) get(id int) int {
	v, ok := r.c.Get(r.keys[id])
	if !ok {
		return 0
	}
	return len(v)
}

func (r *ristrettoCache) set(id int, value []byte) {
	if r.ttl > 0 {
		r.c.SetWithTTL(r.keys[id], value, 1, r.ttl)
		return
	}
	r.c.Set(r.keys[id], value, 1)
}

func (r *ristrettoCache) wait()  { r.c.Wait() }
func (r *ristrettoCache) close() { r.c.Close() }

// newTheine builds a Theine cache. | newTheine 构造 Theine 缓存。
func newTheine(s setup) (benchCache, error) {
	c, err := theine.NewBuilder[string, []byte](int64(s.capacity)).Build()
	if err != nil {
		return nil, err
	}
	return &theineCache{c: c, keys: s.keys, ttl: s.ttl}, nil
}

// theineCache adapts Theine. | theineCache 适配 Theine。
type theineCache struct {
	c    *theine.Cache[string, []byte]
	keys []string
	ttl  time.Duration
}

func (t *theineCache) get(id int) int {
	v, ok := t.c.Get(t.keys[id])
	if !ok {
		return 0
	}
	return len(v)
}

func (t *theineCache) set(id int, value []byte) {
	if t.ttl > 0 {
		t.c.SetWithTTL(t.keys[id], value, 1, t.ttl)
		return
	}
	t.c.Set(t.keys[id], value, 1)
}

func (t *theineCache) wait()  { t.c.Wait() }
func (t *theineCache) close() { t.c.Close() }

// newBigCache builds a BigCache with global TTL semantics. | newBigCache 构造使用全局 TTL 语义的 BigCache。
func newBigCache(s setup) (benchCache, error) {
	c, err := bigcache.New(context.Background(), bigcache.Config{
		Shards:             256,
		LifeWindow:         time.Hour,
		CleanWindow:        0,
		MaxEntriesInWindow: s.capacity,
		MaxEntrySize:       s.valueSize + 64,
		HardMaxCacheSize:   s.hardMaxMB,
	})
	if err != nil {
		return nil, err
	}
	return &bigCache{c: c, keys: s.keys}, nil
}

// bigCache adapts BigCache. | bigCache 适配 BigCache。
type bigCache struct {
	c    *bigcache.BigCache
	keys []string
}

func (b *bigCache) get(id int) int {
	v, err := b.c.Get(b.keys[id])
	if err != nil {
		return 0
	}
	return len(v)
}

func (b *bigCache) set(id int, value []byte) { _ = b.c.Set(b.keys[id], value) }
func (b *bigCache) wait()                    {}
func (b *bigCache) close()                   { _ = b.c.Close() }

// newGoCache builds an unbounded go-cache. | newGoCache 构造无容量上限的 go-cache。
func newGoCache(s setup) (benchCache, error) {
	return &goCache{c: gocache.New(gocache.NoExpiration, 0), keys: s.keys, ttl: s.ttl}, nil
}

// goCache adapts go-cache. | goCache 适配 go-cache。
type goCache struct {
	c    *gocache.Cache
	keys []string
	ttl  time.Duration
}

func (g *goCache) get(id int) int {
	v, ok := g.c.Get(g.keys[id])
	if !ok {
		return 0
	}
	return len(v.([]byte))
}

func (g *goCache) set(id int, value []byte) {
	expire := gocache.NoExpiration
	if g.ttl > 0 {
		expire = g.ttl
	}
	g.c.Set(g.keys[id], value, expire)
}

func (g *goCache) wait()  {}
func (g *goCache) close() {}

// newTTLCache builds a capacity-bounded ttlcache. | newTTLCache 构造有容量上限的 ttlcache。
func newTTLCache(s setup) (benchCache, error) {
	c := ttlcache.New(ttlcache.WithCapacity[string, []byte](uint64(s.capacity)))
	return &ttlCache{c: c, keys: s.keys, ttl: s.ttl}, nil
}

// ttlCache adapts ttlcache v3. | ttlCache 适配 ttlcache v3。
type ttlCache struct {
	c    *ttlcache.Cache[string, []byte]
	keys []string
	ttl  time.Duration
}

func (t *ttlCache) get(id int) int {
	item := t.c.Get(t.keys[id])
	if item == nil {
		return 0
	}
	return len(item.Value())
}

func (t *ttlCache) set(id int, value []byte) {
	expire := ttlcache.NoTTL
	if t.ttl > 0 {
		expire = t.ttl
	}
	t.c.Set(t.keys[id], value, expire)
}

func (t *ttlCache) wait()  {}
func (t *ttlCache) close() {}

// newLRU builds a hashicorp/golang-lru v2 cache. | newLRU 构造 hashicorp/golang-lru v2 缓存。
func newLRU(s setup) (benchCache, error) {
	c, err := lru.New[string, []byte](s.capacity)
	if err != nil {
		return nil, err
	}
	return &lruCache{c: c, keys: s.keys}, nil
}

// lruCache adapts golang-lru v2. | lruCache 适配 golang-lru v2。
type lruCache struct {
	c    *lru.Cache[string, []byte]
	keys []string
}

func (l *lruCache) get(id int) int {
	v, ok := l.c.Get(l.keys[id])
	if !ok {
		return 0
	}
	return len(v)
}

func (l *lruCache) set(id int, value []byte) { l.c.Add(l.keys[id], value) }
func (l *lruCache) wait()                    {}
func (l *lruCache) close()                   {}

// impls is the comparison set. | impls 是对比集合。
var impls = []impl{
	{name: "yd-local-get", new: newYDLocal("get"), ttlCapable: true, capacityBound: true},
	{name: "yd-local-getappend", new: newYDLocal("getappend"), ttlCapable: true, capacityBound: true},
	{name: "yd-local-view", new: newYDLocal("view"), ttlCapable: true, capacityBound: true},
	{name: "freecache-get", new: newFreeCache("get"), ttlCapable: true, capacityBound: true},
	{name: "freecache-getfn", new: newFreeCache("getfn"), ttlCapable: true, capacityBound: true},
	{name: "ristretto", new: newRistretto, ttlCapable: true, capacityBound: true},
	{name: "theine", new: newTheine, ttlCapable: true, capacityBound: true},
	{name: "bigcache", new: newBigCache, ttlCapable: false, capacityBound: true},
	{name: "go-cache", new: newGoCache, ttlCapable: true, capacityBound: false},
	{name: "ttlcache", new: newTTLCache, ttlCapable: true, capacityBound: true},
	{name: "golang-lru", new: newLRU, ttlCapable: false, capacityBound: true},
}

// writeImpls deduplicates write benchmarks: yd's three read modes share one Set path,
// and FreeCache's two read modes share one Set path. | writeImpls 去重写基准：yd 的三种读模式
// 共用一个 Set 路径，FreeCache 的两种读模式共用一个 Set 路径。
var writeImpls = []impl{
	{name: "yd-local", new: newYDLocal("get"), ttlCapable: true, capacityBound: true},
	{name: "freecache", new: newFreeCache("get"), ttlCapable: true, capacityBound: true},
	{name: "ristretto", new: newRistretto, ttlCapable: true, capacityBound: true},
	{name: "theine", new: newTheine, ttlCapable: true, capacityBound: true},
	{name: "bigcache", new: newBigCache, ttlCapable: false, capacityBound: true},
	{name: "go-cache", new: newGoCache, ttlCapable: true, capacityBound: false},
	{name: "ttlcache", new: newTTLCache, ttlCapable: true, capacityBound: true},
	{name: "golang-lru", new: newLRU, ttlCapable: false, capacityBound: true},
}

// preload fills the cache and verifies a few keys. | preload 填满缓存并校验少数键。
func preload(b *testing.B, c benchCache, s setup) {
	b.Helper()
	value := bytes.Repeat([]byte("x"), s.valueSize)
	for i := range s.keys {
		c.set(i, value)
		if i&63 == 0 {
			c.wait()
		}
	}
	c.wait()
	for _, i := range []int{0, 1, len(s.keys) / 2, len(s.keys) - 1} {
		if c.get(i) == 0 {
			b.Fatalf("preload did not store key %d | 预加载未存入键 %d", i, i)
		}
	}
}

// runGet benchmarks one implementation's read path. | runGet 基准测试一个实现的读路径。
func runGet(b *testing.B, im impl, s setup) {
	c, err := im.new(s)
	if err != nil {
		b.Fatalf("%s: %v | 构造失败", im.name, err)
	}
	defer c.close()
	preload(b, c, s)
	b.ReportAllocs()
	b.SetBytes(int64(s.valueSize))
	b.ResetTimer()
	i := 0
	for b.Loop() {
		benchSink += c.get(i % len(s.keys))
		i++
	}
}

// BenchmarkGet measures a single-threaded 100% hit workload. | BenchmarkGet 度量单线程 100% 命中工作负载。
func BenchmarkGet(b *testing.B) {
	for _, size := range []int{64, 1024} {
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			for _, im := range impls {
				if !benchSelected(im.name) {
					continue
				}
				b.Run(im.name, func(b *testing.B) { runGet(b, im, basicSetup(size)) })
			}
		})
	}
}

// BenchmarkGetParallel measures concurrent reads. | BenchmarkGetParallel 度量并发读。
func BenchmarkGetParallel(b *testing.B) {
	for _, im := range impls {
		if !benchSelected(im.name) {
			continue
		}
		b.Run(im.name, func(b *testing.B) {
			s := basicSetup(64)
			c, err := im.new(s)
			if err != nil {
				b.Fatalf("%s: %v | 构造失败", im.name, err)
			}
			defer c.close()
			preload(b, c, s)
			pr, _ := c.(parallelReader)
			b.ReportAllocs()
			b.SetBytes(64)
			b.ResetTimer()
			var seq atomic.Uint64
			b.RunParallel(func(pb *testing.PB) {
				var buf []byte
				i := int(seq.Add(1)) * 7919 % len(s.keys)
				for pb.Next() {
					if pr != nil {
						var n int
						n, buf = pr.getParallel(i, buf)
						benchSinkAtomic.Add(int64(n))
					} else {
						benchSinkAtomic.Add(int64(c.get(i)))
					}
					i++
					if i >= len(s.keys) {
						i = 0
					}
				}
			})
		})
	}
}

// BenchmarkSet measures steady-state overwrites. | BenchmarkSet 度量稳态覆盖写。
func BenchmarkSet(b *testing.B) {
	for _, size := range []int{64, 1024} {
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			for _, im := range writeImpls {
				if !benchSelected(im.name) {
					continue
				}
				b.Run(im.name, func(b *testing.B) {
					s := basicSetup(size)
					c, err := im.new(s)
					if err != nil {
						b.Fatalf("%s: %v | 构造失败", im.name, err)
					}
					defer c.close()
					preload(b, c, s)
					value := bytes.Repeat([]byte("y"), size)
					b.ReportAllocs()
					b.SetBytes(int64(size))
					b.ResetTimer()
					i := 0
					for b.Loop() {
						c.set(i%len(s.keys), value)
						if i&63 == 0 {
							c.wait()
						}
						i++
					}
					b.StopTimer()
					c.wait()
				})
			}
		})
	}
}

// BenchmarkSetChanging writes a different value every iteration, measuring the path where
// the cache cannot short-circuit an idempotent update. | BenchmarkSetChanging 每次写入不同的值，
// 度量无法短路幂等更新的真实覆盖写路径。
func BenchmarkSetChanging(b *testing.B) {
	for _, size := range []int{64, 1024} {
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			for _, im := range writeImpls {
				if !benchSelected(im.name) {
					continue
				}
				b.Run(im.name, func(b *testing.B) {
					s := basicSetup(size)
					c, err := im.new(s)
					if err != nil {
						b.Fatalf("%s: %v | 构造失败", im.name, err)
					}
					defer c.close()
					preload(b, c, s)
					value := bytes.Repeat([]byte("y"), size)
					b.ReportAllocs()
					b.SetBytes(int64(size))
					b.ResetTimer()
					i := 0
					for b.Loop() {
						value[0] = byte(i)
						value[1] = byte(i >> 8)
						c.set(i%len(s.keys), value)
						if i&63 == 0 {
							c.wait()
						}
						i++
					}
					b.StopTimer()
					c.wait()
				})
			}
		})
	}
}

// BenchmarkMixed measures a 90% read / 10% write workload. | BenchmarkMixed 度量 90% 读 / 10% 写工作负载。
func BenchmarkMixed(b *testing.B) {
	for _, im := range impls {
		if !benchSelected(im.name) {
			continue
		}
		b.Run(im.name, func(b *testing.B) {
			s := basicSetup(64)
			c, err := im.new(s)
			if err != nil {
				b.Fatalf("%s: %v | 构造失败", im.name, err)
			}
			defer c.close()
			preload(b, c, s)
			value := bytes.Repeat([]byte("y"), 64)
			b.ReportAllocs()
			b.ResetTimer()
			i := 0
			writes := 0
			for b.Loop() {
				id := i % len(s.keys)
				if i%10 == 0 {
					c.set(id, value)
					writes++
					if writes&63 == 0 {
						c.wait()
					}
				} else {
					benchSink += c.get(id)
				}
				i++
			}
			b.StopTimer()
			c.wait()
		})
	}
}

// BenchmarkGetTTL measures reads with a one-hour TTL. | BenchmarkGetTTL 度量带一小时 TTL 的读取。
func BenchmarkGetTTL(b *testing.B) {
	for _, im := range impls {
		if !im.ttlCapable || !benchSelected(im.name) {
			continue
		}
		b.Run(im.name, func(b *testing.B) { runGet(b, im, ttlSetup(64)) })
	}
}

// BenchmarkSetTTL measures writes with a one-hour TTL. | BenchmarkSetTTL 度量带一小时 TTL 的写入。
func BenchmarkSetTTL(b *testing.B) {
	for _, im := range writeImpls {
		if !im.ttlCapable || !benchSelected(im.name) {
			continue
		}
		b.Run(im.name, func(b *testing.B) {
			s := ttlSetup(64)
			c, err := im.new(s)
			if err != nil {
				b.Fatalf("%s: %v | 构造失败", im.name, err)
			}
			defer c.close()
			preload(b, c, s)
			value := bytes.Repeat([]byte("y"), 64)
			b.ReportAllocs()
			b.ResetTimer()
			i := 0
			for b.Loop() {
				c.set(i%len(s.keys), value)
				if i&63 == 0 {
					c.wait()
				}
				i++
			}
			b.StopTimer()
			c.wait()
		})
	}
}

// TestHitRatio runs a deterministic hot/cold trace against a 5k-entry capacity. | TestHitRatio 在 5k 容量下运行确定性的冷热轨迹。
//
// The ratio is a policy metric, not a latency metric; go-cache is excluded because it has no | 该比例是策略指标而非延迟指标；go-cache 因没有容量上限而不参与。
// capacity bound, and BigCache uses a byte-bounded ring rather than an item count. | BigCache 使用字节上限环形队列而非条目数上限。
func TestHitRatio(t *testing.T) {
	value := bytes.Repeat([]byte("x"), 64)
	for _, im := range impls {
		if !im.capacityBound || !benchSelected(im.name) {
			continue
		}
		s := hitRatioSetup()
		c, err := im.new(s)
		if err != nil {
			t.Fatalf("%s: %v | 构造失败", im.name, err)
		}
		rng := rand.New(rand.NewPCG(1, 2))
		hits, misses := 0, 0
		for i := 0; i < hitRatioAccesses; i++ {
			var id int
			if rng.IntN(100) < 70 {
				id = rng.IntN(hotKeys)
			} else {
				id = rng.IntN(len(s.keys))
			}
			if c.get(id) > 0 {
				hits++
				continue
			}
			misses++
			c.set(id, value)
			c.wait()
		}
		c.wait()
		c.close()
		total := hits + misses
		t.Logf("%-20s hit=%6.2f%% (%d/%d) | 命中率", im.name, 100*float64(hits)/float64(total), hits, total)
	}
}
