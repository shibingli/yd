package router

// rawRoute is one registered context-free static route. | rawRoute 是一条已注册的无上下文静态路由。
type rawRoute struct {
	handler HandlerFuncRaw
	path    string
	method  methodKey
}

// rawSlot is one open-addressing slot of the raw route table. An empty slot has a nil handler. | rawSlot
// 是无上下文路由表开放寻址的一个槽位。空槽位的 handler 为 nil。
type rawSlot struct {
	handler HandlerFuncRaw
	path    string
	name    string
	id      methodID
	known   bool
}

// rawTable is the immutable, precomputed lookup table for raw static routes. It is built during
// wiring and swapped atomically with the serving snapshot, so the request path only reads it. | rawTable
// 是无上下文静态路由的不可变预计算查找表。它在装配期构建，并与服务快照一起原子替换，请求路径只读。
type rawTable struct {
	slots []rawSlot
	mask  uint64
}

// newRawTable builds an open-addressing table with a load factor of at most one half. | newRawTable
// 构建负载因子不超过一半的开放寻址表。
func newRawTable(routes []rawRoute) *rawTable {
	if len(routes) == 0 {
		return nil
	}
	size := 1
	for size < len(routes)*2 {
		size <<= 1
	}
	t := &rawTable{slots: make([]rawSlot, size), mask: uint64(size - 1)}
	for i := range routes {
		r := &routes[i]
		slot := rawPathHash(r.path) & t.mask
		for t.slots[slot].handler != nil {
			slot = (slot + 1) & t.mask
		}
		t.slots[slot] = rawSlot{
			handler: r.handler,
			path:    r.path,
			id:      r.method.id,
			known:   r.method.known,
			name:    r.method.name,
		}
	}
	return t
}

// rawPathHash hashes a static path with the same multiplicative mix used by the route cache. | rawPathHash
// 用路由缓存相同的乘法混合对静态路径做哈希。
func rawPathHash(path string) uint64 {
	n := len(path)
	h := uint64(n) * 0x9e3779b97f4a7c15
	if n >= 8 {
		h = mixWord(h, loadWord(path, 0))
		h = mixWord(h, loadWord(path, n-8))
		if n >= 16 {
			h = mixWord(h, loadWord(path, n/2-4))
		}
	} else {
		for i := 0; i < n; i++ {
			h = mixWord(h, uint64(path[i])+1)
		}
	}
	return h ^ (h >> 32)
}

// lookup returns the raw handler for key and path, falling back from HEAD to GET. | lookup 返回
// key 与 path 对应的无上下文处理器，HEAD 回退到 GET。
func (t *rawTable) lookup(key methodKey, path string) HandlerFuncRaw {
	if h := t.lookupKey(key, path); h != nil {
		return h
	}
	if key.known && key.id == methodHead {
		return t.lookupKey(getMethodKey, path)
	}
	return nil
}

// lookupKey probes the table for one method. | lookupKey 为单个方法探测路由表。
func (t *rawTable) lookupKey(key methodKey, path string) HandlerFuncRaw {
	slot := rawPathHash(path) & t.mask
	for {
		s := &t.slots[slot]
		if s.handler == nil {
			return nil
		}
		if s.path == path && s.known == key.known && s.id == key.id {
			if key.known || s.name == key.name {
				return s.handler
			}
		}
		slot = (slot + 1) & t.mask
	}
}
