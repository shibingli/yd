package router

import "unsafe"

// localRouteCacheSize is the number of direct-mapped slots in the per-request-context cache.
// The cache lives inside a pooled Ctx, so each P keeps a small private working set with no
// shared cache lines, atomics or locks; high-cardinality traffic only touches the admission
// word of its own context and therefore cannot slow other goroutines down. | localRouteCacheSize
// 是每上下文缓存中直接映射槽位的数量。缓存位于池化的 Ctx 内，因此每个 P 都保有一份私有小工作集，
// 没有共享缓存行、原子操作或锁；高基数流量只会触碰自己上下文的准入字，因此不会拖慢其它 goroutine。
const localRouteCacheSize = 32

// cacheHashCapacity masks the 56-bit path hash stored above the 8-bit admission count. | cacheHashCapacity
// 掩掉 8 位准入计数之上的 56 位路径哈希。
const cacheHashCapacity = 1<<56 - 1

// routeCacheEntry is one cached routing decision. Each entry is owned by exactly one Ctx: it is
// read while that context serves a request and rewritten only between requests, so no atomic
// operation or lock is needed. | routeCacheEntry 是一条缓存的路由决策。每个条目恰好属于一个 Ctx：
// 在该上下文处理请求期间只读，且仅在请求之间改写，因此不需要原子操作或锁。
type routeCacheEntry struct {
	// path is the exact request path this entry was built for. | path 是构建该缓存项的精确请求路径。
	path string
	// method identifies the request method. | method 标识请求方法。
	method methodKey
	// params are the captured path parameters, empty for a static match. | params 是捕获的路径
	// 参数，静态匹配时为空。
	params []Param
	// chain is the immutable route handler chain. | chain 是不可变的路由处理器链。
	chain []HandlerFunc
	// gen is the route table generation the entry was built from. | gen 是构建该缓存项时的路由表代际。
	gen uint64
	// notFound marks a structural miss: no route handler exists for the path. | notFound 标记
	// 结构性未命中：该路径不存在路由处理器。
	notFound bool
}

// localRouteCache is the per-context direct-mapped cache and its admission filter. | localRouteCache
// 是每上下文的直接映射缓存及其准入过滤器。
type localRouteCache struct {
	// last is the entry used by the immediately preceding request. It lets a repeated exact
	// path skip the hash and slot indexing entirely; stale pointers are harmless because the
	// entry fields (gen, path, method) are revalidated on every use. | last 是上一个请求使用的
	// 缓存项。重复的精确路径因此可以完全跳过哈希与槽位索引；过期指针无害，因为每次使用都会重新
	// 校验条目字段（gen、path、method）。
	last *routeCacheEntry
	// recent holds the last two concrete paths that used the shape. A repeat is evidence that
	// the concrete path is hot, so it is promoted into the exact cache; a rotating working set
	// never promotes and therefore never pays the hash or allocation. | recent 保存最近两个使用
	// 形状的具体路径。重复出现说明该具体路径是热的，因此提升到精确缓存；轮转工作集因此从不提升，也就
	// 从不支付哈希或分配成本。
	recent  [2]string
	entries [localRouteCacheSize]routeCacheEntry
	// seen packs the 56-bit path hash in the high bits and the consecutive sighting count in the
	// low 8 bits. One-shot paths (scanners, unique resource IDs) therefore never allocate a cache
	// entry, while a repeated path is admitted on its second sighting. | seen 高位存 56 位路径哈希，
	// 低 8 位存连续出现次数。一次性路径（扫描器、唯一资源 ID）因此不会分配缓存项，重复路径则在第二次
	// 出现时进入缓存。
	seen [localRouteCacheSize]uint64
	// gen is the route table generation the admission words belong to. | gen 是准入字所属的路由表代际。
	gen uint64
	// lastLen and lastMid are cheap rejection hints for lookupLast. They are derived from the
	// hit path and refreshed with last; stale values only cause a fallback to the hash lookup. | lastLen
	// 与 lastMid 是 lookupLast 的廉价拒绝提示，取自命中的路径并随 last 一起刷新；过期值只会导致
	// 回退到哈希查找。
	lastLen uint32
	lastMid byte
	// patternActive records that the previous request was routed by the compiled shape. Only then
	// does the next request try the shape before hashing and looking up the exact cache. | patternActive
	// 记录上一个请求由编译形状完成路由。只有此时下一个请求才会在哈希与精确缓存查找之前尝试形状。
	patternActive bool
}

// loadWord reads eight bytes of s without copying. | loadWord 在不复制的情况下读取 s 的八个字节。
func loadWord(s string, off int) uint64 {
	return *(*uint64)(unsafe.Add(unsafe.Pointer(unsafe.StringData(s)), off))
}

// mixWord folds one 64-bit word into the running hash with a single multiply. The final fold in
// cacheHash spreads the product's high bits into the slot bits; two rounds per word are not
// needed because the full path is still compared before a hit. | mixWord 用一次乘法把一个 64 位
// 字混入滚动哈希。cacheHash 末尾的折叠把乘积高位扩散到槽位比特；命中前仍会完整比较路径，因此不需要
// 每个字两轮混合。
func mixWord(h, w uint64) uint64 {
	return (h ^ w) * 0x9e3779b97f4a7c15
}

// cacheHash hashes the method and the path with overlapping eight-byte windows. Windows fully
// cover short paths (up to 24 bytes) and are sampled evenly for longer ones. Coverage matters
// for the admission filter: sampling only the ends made thousands of /users/N/posts paths
// collide, so cold traffic was admitted and evicted continuously. | cacheHash 用重叠的八字节窗口
// 对方法与路径做哈希。窗口完整覆盖短路径（24 字节以内），更长路径则均匀采样。覆盖度对准入过滤很
// 重要：只采样首尾会让成千上万个 /users/N/posts 路径互相碰撞，冷流量因此不断被准入和逐出。
func cacheHash(key methodKey, path string) uint64 {
	n := len(path)
	h := uint64(n)*0x9e3779b97f4a7c15 + uint64(key.id)
	if key.known {
		h = mixWord(h, uint64(key.id)+1)
	} else {
		h = mixWord(h, uint64(len(key.name)))
		for i := 0; i < len(key.name); i++ {
			h = mixWord(h, uint64(key.name[i]))
		}
	}
	if n >= 8 {
		h = mixWord(h, loadWord(path, 0))
		h = mixWord(h, loadWord(path, n-8))
		if n >= 16 {
			h = mixWord(h, loadWord(path, n/2-4))
		}
		if n >= 32 {
			h = mixWord(h, loadWord(path, n/4))
			h = mixWord(h, loadWord(path, n*3/4-4))
		}
	} else {
		for i := 0; i < n; i++ {
			h = mixWord(h, uint64(path[i])+1)
		}
	}
	h ^= h >> 32
	return (h & cacheHashCapacity) << 8
}

// cacheSlot returns the direct-mapped slot for a packed hash. | cacheSlot 返回打包哈希对应的直接映射槽位。
func cacheSlot(h uint64) uint32 {
	return uint32(h>>8) & (localRouteCacheSize - 1)
}

// reset clears the admission words when the route table generation changes. Entries from an
// older generation are rejected by their own gen field and are overwritten lazily. | reset 在路由表
// 代际变化时清空准入字。旧代际的条目由各自的 gen 字段拒绝，并在后续使用中惰性覆盖。
func (lc *localRouteCache) reset(gen uint64) {
	clear(lc.seen[:])
	lc.last = nil
	lc.patternActive = false
	lc.recent = [2]string{}
	lc.gen = gen
}

// admit reports whether the path should be cached now; it admits only after the same path hash
// is seen twice in the same slot. The full path is still compared before a hit, so hash
// collisions can only cause an early admission, never a wrong match. | admit 报告路径当前是否
// 应进入缓存；同一路径哈希在同一槽位出现两次后才准入。命中前仍会比较完整路径，因此哈希冲突最多
// 造成提前准入，绝不会造成错误匹配。
func (lc *localRouteCache) admit(h uint64) bool {
	slot := cacheSlot(h)
	old := lc.seen[slot]
	if old&^uint64(0xff) != h {
		// First sighting, or a different path took the slot: reset to count one. | 首次出现，
		// 或另一个路径占用了槽位：重置为计数一。
		lc.seen[slot] = h | 1
		return false
	}
	if old&0xff >= 2 {
		return true
	}
	lc.seen[slot] = old + 1
	return false
}

// lookupLast returns the previous request's entry when it still matches gen, path and method.
// A repeated path therefore costs one string comparison instead of a hash and a slot load. | lookupLast
// 在上一个请求的缓存项仍匹配代际、路径与方法时返回它。重复路径因此只需一次字符串比较，而不是
// 哈希计算加槽位加载。
func (lc *localRouteCache) lookupLast(gen uint64, key methodKey, path string) *routeCacheEntry {
	e := lc.last
	if e == nil || uint32(len(path)) != lc.lastLen || len(path) == 0 || lc.lastMid != path[len(path)>>1] {
		return nil
	}
	if e.gen != gen || e.path != path {
		return nil
	}
	if e.method.known != key.known || e.method.id != key.id {
		return nil
	}
	if !key.known && e.method.name != key.name {
		return nil
	}
	return e
}

// lookup returns the entry for key and path when it was built from the current generation. | lookup
// 在缓存项来自当前代际时返回 key 与 path 对应的条目。
func (lc *localRouteCache) lookup(gen uint64, key methodKey, path string, h uint64) *routeCacheEntry {
	e := &lc.entries[cacheSlot(h)]
	if e.gen != gen || e.path != path {
		return nil
	}
	// Standard methods compare one integer; only uncommon methods need a name compare. | 标准
	// 方法只比较一个整数；只有非标准方法才需要比较名称。
	if e.method.known != key.known || e.method.id != key.id {
		return nil
	}
	if !key.known && e.method.name != key.name {
		return nil
	}
	lc.last = e
	lc.lastLen = uint32(len(e.path))
	lc.lastMid = e.path[len(e.path)>>1]
	return e
}

// store publishes an immutable entry for key and path. | store 为 key 与 path 发布一个不可变缓存项。
func (lc *localRouteCache) store(gen uint64, key methodKey, path string, h uint64, chain []HandlerFunc, params *Params, notFound bool) *routeCacheEntry {
	slot := cacheSlot(h)
	if !lc.admit(h) {
		return nil
	}
	e := &lc.entries[slot]
	var captured []Param
	if !notFound && params != nil && params.Len() > 0 {
		// Reuse the previous capture buffer of this slot: admissions are rare on
		// high-cardinality traffic, so steady-state traffic allocates nothing here. | 复用该槽位
		// 上一次的捕获缓冲：高基数流量下准入很少发生，稳态请求在这里不产生分配。
		captured = append(e.params[:0], params.ps...)
	}
	e.path = path
	e.method = key
	e.params = captured
	e.chain = chain
	e.gen = gen
	e.notFound = notFound
	return e
}

// setLast records e as the previous successful exact match and refreshes its rejection hints. | setLast
// 把 e 记录为上一次成功的精确匹配，并刷新其快速拒绝提示。
func (lc *localRouteCache) setLast(e *routeCacheEntry) {
	lc.last = e
	lc.lastLen = uint32(len(e.path))
	lc.lastMid = e.path[len(e.path)>>1]
}

// patternRecent reports whether path is one of the last two shape paths. | patternRecent 报告
// path 是否属于最近两个形状路径。
func (lc *localRouteCache) patternRecent(path string) bool {
	return lc.recent[0] == path || lc.recent[1] == path
}

// noteRecent records path in the two-entry recent ring. | noteRecent 把 path 记入双条目近期环。
func (lc *localRouteCache) noteRecent(path string) {
	if lc.recent[0] != path {
		lc.recent[1], lc.recent[0] = lc.recent[0], path
	}
}

// routePattern is a context-local matcher for one parameterized route shape. It turns every
// concrete path of that shape into a direct capture without walking the tree, which is what
// high-cardinality parameter traffic (unique resource IDs) needs. | routePattern 是每上下文保存的
// 单一路由形状匹配器。它把该形状下的每个具体路径直接捕获出来而不必遍历路由树，这正是高基数参数
// 流量（唯一资源 ID）所需要的。
type routePattern struct {
	shape *patternShape
	chain []HandlerFunc
	// node is the terminal node whose shape was validated for gen; a different node or
	// generation must be validated again. | node 是已针对 gen 验证过形状的终止节点；不同节点或
	// 代际必须重新验证。
	node *node
	// method is the request method the pattern was installed for, so other methods fall through
	// to the tree and still produce 405 semantics. | method 是安装该模式时的请求方法，其它方法因此
	// 回落到路由树，仍然产生正确的 405 语义。
	method methodKey
	// gen is the route table generation of the validation. | gen 是验证时的路由表代际。
	gen uint64
	// safe records whether the shape routes identically for every matching path. | safe 记录该形状
	// 对每个匹配路径是否都路由到同一位置。
	safe bool
}

// match consumes the full path against the pattern, appending captures to ps. It never partially
// matches: the fast path appends only after every check succeeded, and the general path restores
// the parameter count on failure. | match 用模式完整消费路径，并把捕获追加到 ps。它绝不部分匹配：
// 快路径在所有检查通过后才追加，通用路径在失败时恢复参数数量。
func (p *routePattern) match(path string, ps *Params) bool {
	s := p.shape
	if s == nil {
		return false
	}
	if s.fast {
		return p.matchPrefixSuffix(path, ps)
	}
	return p.matchGeneral(path, ps)
}

// matchPrefixSuffix matches a single-parameter shape as prefix + value + suffix. Two string
// comparisons and one slash scan replace the per-segment loop. | matchPrefixSuffix 把单参数形状
// 当作「前缀 + 值 + 后缀」匹配。两次字符串比较与一次斜杠扫描取代了逐分段循环。
func (p *routePattern) matchPrefixSuffix(path string, ps *Params) bool {
	s := p.shape
	np, ns, n := len(s.prefix), len(s.suffix), len(path)
	if n <= np+ns {
		return false
	}
	if !segEqual(path[:np], s.prefix) {
		return false
	}
	if ns > 0 && !segEqual(path[n-ns:], s.suffix) {
		return false
	}
	val := path[np : n-ns]
	if containsSlash(val) {
		return false
	}
	if ps.shared {
		ps.Append(s.param, val)
	} else {
		ps.ps = append(ps.ps, Param{Key: s.param, Value: val})
	}
	return true
}

// containsSlash reports whether s contains a '/', scanning eight bytes at a time. | containsSlash
// 报告 s 是否包含 '/'，每次扫描八个字节。
func containsSlash(s string) bool {
	for len(s) >= 8 {
		w := loadWord(s, 0)
		x := w ^ 0x2f2f2f2f2f2f2f2f
		if (x-0x0101010101010101)&^x&0x8080808080808080 != 0 {
			return true
		}
		s = s[8:]
	}
	if len(s) >= 4 {
		w := uint64(load4(s, 0))
		x := w ^ 0x2f2f2f2f
		if (x-0x01010101)&^x&0x80808080 != 0 {
			return true
		}
		s = s[4:]
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}

// matchGeneral is the segment loop used by shapes with multiple parameters. | matchGeneral 是
// 多参数形状使用的分段循环。
func (p *routePattern) matchGeneral(path string, ps *Params) bool {
	segs := p.shape.segments
	n := len(path)
	if len(segs) == 0 || n < 2 || path[0] != '/' {
		return false
	}
	mark := ps.Len()
	start := 1
	for i := range segs {
		end := start
		if n-start >= 8 {
			end = nextSlashLong(path, start)
		} else {
			for end < n && path[end] != '/' {
				end++
			}
		}
		val := path[start:end]
		seg := &segs[i]
		if seg.param {
			if val == "" {
				ps.Truncate(mark)
				return false
			}
			ps.Append(seg.name, val)
		} else if !segEqual(seg.literal, val) {
			ps.Truncate(mark)
			return false
		}
		if end == n {
			if i != len(segs)-1 {
				ps.Truncate(mark)
				return false
			}
			return true
		}
		start = end + 1
	}
	ps.Truncate(mark)
	return false
}
