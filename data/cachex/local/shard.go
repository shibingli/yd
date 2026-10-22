package local

import (
	"bytes"
	"encoding/binary"
	"hash/maphash"
	"math/bits"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Entry header layout. Every field is little endian and pointer free. | 条目头布局。所有字段小端且不含指针。
const (
	entryHeaderSize = 32
	hdrExpireAt     = 0  // int64, 0 means never | int64，0 表示永不过期
	hdrHash         = 8  // uint64, full key hash | uint64，完整键哈希
	hdrKeyLen       = 16 // uint32
	hdrValLen       = 20 // uint32
	hdrFlags        = 24 // uint32
	hdrMetaID       = 28 // uint32, sidecar index for large values | 大值侧车索引
)

// Entry flags. | 条目标志。
const (
	flagDead uint32 = 1 << iota
	flagMeta
)

// Slot offsets use +2 encoding so 0 means empty and 1 means tombstone. | 槽位偏移采用 +2 编码，0 表示空、1 表示墓碑。
const (
	slotEmptyOffset     uint32 = 0
	slotTombstoneOffset uint32 = 1
	slotOffsetBias      uint32 = 2
)

// shard is one independently locked ring. | shard 是一个独立加锁的环形缓冲区。
type shard struct {
	buf              []byte
	slots            []slot
	sketch           []uint64
	sampleSketch     []uint64
	access           []atomic.Uint64
	meta             []entryMeta
	freeMetaIDs      []uint32
	stats            shardStats
	sampleSketchMu   sync.Mutex
	head             uint64
	tail             uint64
	sweepPos         uint64
	used             int64
	entries          int64
	sketchMask       uint64
	sketchAdds       uint64
	sampleSketchAdds uint64
	tombstones       int
	maxEntry         int
	entryLimit       int
	maxSlots         int
	mask             uint32
	padFrom          uint32
	padTo            uint32
	hasPad           bool
	// Keep the hot read-only fields off the lock's cache line. Readers constantly load mask;
	// sharing that line with the RWMutex atomics makes every lock update invalidate it.
	// | 把热只读字段与锁的缓存行分开。读者频繁读取 mask；与 RWMutex 原子状态共线会让每次
	// 锁更新都使该行失效。
	_  [64]byte
	mu sync.RWMutex
}

// slot is one open-addressed index cell; it contains no pointers. | slot 是一个开放寻址索引单元，不含指针。
type slot struct {
	hash uint32
	off  uint32
}

// paddedCounter keeps one atomic counter on its own cache line. | paddedCounter 让一个原子计数器独占缓存行。
type paddedCounter struct {
	v atomic.Uint64
	_ [56]byte
}

// hitStripes is the number of read-side hit counters per shard. | hitStripes 是每分片读侧命中计数器数量。
const hitStripes = 8

// readSampleMask controls how often a read hit feeds the access bit and access sketch
// (one in mask+1). Reads copy under the shard read lock, so policy updates are sampled after
// the lock is released instead of taking the write lock on every hit.
//
// readSampleMask 控制读取命中进入访问位与访问草图的间隔（每 mask+1 次一次）。读取在分片
// 读锁内完成拷贝，因此策略更新在锁外采样，无需每次命中都获取写锁。
const readSampleMask = 7

// shardStats holds per-shard counters; each shard owns its own cache lines. | shardStats 保存每分片计数器，各分片独占自己的缓存行。
type shardStats struct {
	// hits is striped so parallel readers on one hot key do not serialize on a single word. | hits 采用分条计数，使同一热键的并行读者不会在同一个字上串行。
	hits      [hitStripes]paddedCounter
	misses    atomic.Uint64
	sets      atomic.Uint64
	deletes   atomic.Uint64
	evictions atomic.Uint64
	expired   atomic.Uint64
	rejected  atomic.Uint64
	identical atomic.Uint64
}

// addHit records one hit and returns that stripe's new count. | addHit 记录一次命中并返回该分条的新计数。
func (s *shardStats) addHit(hash uint64) uint64 {
	return s.hits[hash&(hitStripes-1)].v.Add(1)
}

// loadHits sums all hit stripes. | loadHits 汇总所有命中分条。
func (s *shardStats) loadHits() uint64 {
	var n uint64
	for i := range s.hits {
		n += s.hits[i].v.Load()
	}
	return n
}

// entry is a validated view of one ring record. | entry 是一个经过校验的环形记录视图。
type entry struct {
	expireAt int64
	hash     uint64
	off      uint32
	keyLen   uint32
	valLen   uint32
	flags    uint32
	size     uint32
	valStart uint32
	metaID   uint32
}

// newShard allocates one ring. | newShard 分配一个环形缓冲区。
func newShard(size, maxEntry int) *shard {
	entryLimit := size / 96
	if entryLimit < 64 {
		entryLimit = 64
	}
	maxSlots := nextPowerOfTwo(entryLimit * 2)
	// Size the initial table from the largest entry the caller allows. Caches with large
	// values then avoid a mostly empty multi-megabyte slot table; if the cache actually holds
	// smaller entries, the table grows on demand. | 初始槽表按调用方允许的最大条目估算。
	// 大值缓存因此不会分配一张几乎全空的多兆字节槽表；若实际存放更小的条目，槽表会按需增长。
	initialEntries := entryLimit
	if maxEntry > 0 {
		if byEntry := size/maxEntry + 1; byEntry < initialEntries {
			initialEntries = byEntry
		}
	}
	slotsLen := nextPowerOfTwo(initialEntries)
	if slotsLen < 64 {
		slotsLen = 64
	}
	if slotsLen > maxSlots {
		slotsLen = maxSlots
	}
	sketchWidth := nextPowerOfTwo(entryLimit)
	if sketchWidth < 64 {
		sketchWidth = 64
	}
	words := sketchWidth / 16
	accessWords := (size + 64*64 - 1) / (64 * 64)
	if accessWords < 1 {
		accessWords = 1
	}
	s := &shard{
		buf:          make([]byte, size),
		slots:        make([]slot, slotsLen),
		sketch:       make([]uint64, 4*words),
		sampleSketch: make([]uint64, 4*words),
		access:       make([]atomic.Uint64, accessWords),
		mask:         uint32(slotsLen - 1),
		sketchMask:   uint64(sketchWidth - 1),
		maxEntry:     maxEntry,
		entryLimit:   entryLimit,
		maxSlots:     maxSlots,
	}
	return s
}

// hashString returns the seeded 64-bit hash of key. The seed is randomized once per cache, | hashString 返回 key 的带种子 64 位哈希。种子按缓存随机化一次，
// which makes hash-flooding attacks against the ring impractical. | 使针对环形缓冲区的哈希洪泛攻击难以奏效。
func hashString(seed maphash.Seed, key string) uint64 {
	return maphash.String(seed, key)
}

// mix64 is splitmix64's finalizer; it decorrelates sketch rows. | mix64 是 splitmix64 的收尾函数，用于去相关草图行。
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// phys maps a monotonic ring position to a physical offset. | phys 把单调环形位置映射为物理偏移。
func (s *shard) phys(pos uint64) uint32 {
	return uint32(pos % uint64(len(s.buf)))
}

// readEntry validates and decodes the record at off. | readEntry 校验并解码 off 处的记录。
func (s *shard) readEntry(off uint32) (entry, bool) {
	if int(off)+entryHeaderSize > len(s.buf) {
		return entry{}, false
	}
	lens := binary.LittleEndian.Uint64(s.buf[off+hdrKeyLen:])
	flags := binary.LittleEndian.Uint64(s.buf[off+hdrFlags:])
	e := entry{
		expireAt: int64(binary.LittleEndian.Uint64(s.buf[off+hdrExpireAt:])),
		hash:     binary.LittleEndian.Uint64(s.buf[off+hdrHash:]),
		keyLen:   uint32(lens),
		valLen:   uint32(lens >> 32),
		flags:    uint32(flags),
		metaID:   uint32(flags >> 32),
		off:      off,
	}
	total := uint64(entryHeaderSize) + uint64(e.keyLen) + uint64(e.valLen)
	if uint64(off)+total > uint64(len(s.buf)) {
		return entry{}, false
	}
	e.size = uint32(total)
	e.valStart = off + entryHeaderSize + e.keyLen
	return e, true
}

// key returns a view of the entry key. | key 返回条目键的视图。
func (s *shard) key(e entry) []byte {
	start := int(e.off) + entryHeaderSize
	return s.buf[start : start+int(e.keyLen)]
}

// value returns a view of the entry value. | value 返回条目值的视图。
func (s *shard) value(e entry) []byte {
	return s.buf[e.valStart : e.valStart+e.valLen]
}

// markAccess records a hit for the 64-byte block containing off. | markAccess 记录 off 所在 64 字节块的一次命中。
//
// The bit is set atomically so View can update recency while holding only the shard read
// lock. | 该位以原子方式设置，使 View 只持分片读锁时也能更新访问记录。
func (s *shard) markAccess(off uint32) {
	block := off >> 6
	word := block >> 6
	bit := uint64(1) << (block & 63)
	if s.access[word].Load()&bit == 0 {
		s.access[word].Or(bit)
	}
}

// accessed reports whether the block containing off was hit since it was reused. | accessed 报告 off 所在块自复用以来是否被命中过。
func (s *shard) accessed(off uint32) bool {
	block := off >> 6
	return s.access[block>>6].Load()&(uint64(1)<<(block&63)) != 0
}

// clearAccess forgets access bits for the blocks covered by [off, off+n), except the first
// block: it may also contain the start of the previous live record, whose access bit must
// survive. | clearAccess 清除 [off, off+n) 覆盖块的访问位，但保留首块：首块可能同时包含
// 前一条存活记录的起点，其访问位必须保留。
func (s *shard) clearAccess(off, n uint32) {
	first := off >> 6
	last := (off + n - 1) >> 6
	for block := first + 1; block <= last; {
		word := block >> 6
		start := block & 63
		end := uint32(63)
		if word == last>>6 {
			end = last & 63
		}
		mask := ^uint64(0) << start
		if end < 63 {
			mask &= (uint64(1) << (end + 1)) - 1
		}
		if s.access[word].Load()&mask != 0 {
			s.access[word].And(^mask)
		}
		block = (word + 1) << 6
	}
}

// resetAccess clears every access bit. | resetAccess 清空全部访问位。
func (s *shard) resetAccess() {
	for i := range s.access {
		s.access[i].Store(0)
	}
}

// writeEntry writes one record at off; the caller guarantees off+n <= len(buf). | writeEntry 在 off 处写一条记录；调用方保证 off+n <= len(buf)。
func (s *shard) writeEntry(off uint32, hash uint64, key string, val []byte, expireAt int64) {
	binary.LittleEndian.PutUint64(s.buf[off+hdrExpireAt:], uint64(expireAt))
	binary.LittleEndian.PutUint64(s.buf[off+hdrHash:], hash)
	binary.LittleEndian.PutUint64(s.buf[off+hdrKeyLen:], uint64(len(key))|uint64(len(val))<<32)
	binary.LittleEndian.PutUint64(s.buf[off+hdrFlags:], 0)
	start := int(off) + entryHeaderSize
	copy(s.buf[start:], key)
	copy(s.buf[start+len(key):], val)
}

// overwriteEntry replaces the value of a same-size live record in place. | overwriteEntry 原地替换同尺寸存活记录的值。
//
// Keeping the record at its offset avoids appending a dead version on every update, so a
// steady-state overwrite workload does not fill the ring with garbage. | 保留原偏移可避免每次更新都追加
// 一份死版本，使稳态覆盖写不会把环形缓冲区填满垃圾。
func (s *shard) overwriteEntry(e entry, val []byte, expireAt int64) {
	binary.LittleEndian.PutUint64(s.buf[e.off+hdrExpireAt:], uint64(expireAt))
	if e.flags&flagMeta != 0 {
		s.meta[e.metaID].expireAt = expireAt
	}
	// The size is unchanged, so keyLen, valLen and flags stay valid; only the expiry and the
	// value bytes need writing. | 尺寸未变，keyLen、valLen 与标志仍然有效，只需写过期时间和值字节。
	copy(s.buf[e.valStart:e.valStart+e.valLen], val)
}

// setFlag updates the flag word of e. | setFlag 更新 e 的标志字。
func (s *shard) setFlag(e entry, flag uint32) {
	cur := binary.LittleEndian.Uint32(s.buf[e.off+hdrFlags:])
	binary.LittleEndian.PutUint32(s.buf[e.off+hdrFlags:], cur|flag)
}

// find returns the decoded entry of key. | find 返回 key 对应的已解码条目。
//
// Returning the entry avoids a second header decode in every hit path. | 返回条目使每条命中路径省去第二次头部解码。
func (s *shard) find(hash uint64, key string) (entry, bool) {
	h32 := uint32(hash)
	i := h32 & s.mask
	for probes := 0; probes < len(s.slots); probes++ {
		sl := s.slots[i]
		if sl.off == slotEmptyOffset {
			return entry{}, false
		}
		if sl.off != slotTombstoneOffset && sl.hash == h32 {
			token := sl.off - slotOffsetBias
			if token&slotMetaFlag != 0 {
				if e, ok := s.metaEntry(token&^slotMetaFlag, hash, key); ok {
					return e, true
				}
			} else if e, ok := s.readEntry(token); ok && e.flags&flagDead == 0 && e.hash == hash &&
				e.keyLen == uint32(len(key)) && equalStringBytes(s.key(e), key) {
				return e, true
			}
		}
		i = (i + 1) & s.mask
	}
	return entry{}, false
}

// insertSlot inserts one index cell without rebuilding. | insertSlot 插入一个索引单元，不做重建。
func (s *shard) insertSlot(h32 uint32, token uint32) bool {
	i := h32 & s.mask
	firstTomb := int32(-1)
	for probes := 0; probes < len(s.slots); probes++ {
		sl := &s.slots[i]
		switch sl.off {
		case slotEmptyOffset:
			target := i
			if firstTomb >= 0 {
				target = uint32(firstTomb)
				s.tombstones--
			}
			s.slots[target] = slot{hash: h32, off: token + slotOffsetBias}
			return true
		case slotTombstoneOffset:
			if firstTomb < 0 {
				firstTomb = int32(i)
			}
		default:
			if sl.hash == h32 && sl.off == token+slotOffsetBias {
				return true
			}
		}
		i = (i + 1) & s.mask
	}
	if firstTomb >= 0 {
		s.tombstones--
		s.slots[firstTomb] = slot{hash: h32, off: token + slotOffsetBias}
		return true
	}
	return false
}

// removeSlot removes the cell pointing at off. | removeSlot 移除指向 off 的索引单元。
func (s *shard) removeSlot(hash uint64, token uint32) {
	h32 := uint32(hash)
	i := h32 & s.mask
	for probes := 0; probes < len(s.slots); probes++ {
		sl := s.slots[i]
		if sl.off == slotEmptyOffset {
			return
		}
		if sl.off != slotTombstoneOffset && sl.hash == h32 && sl.off == token+slotOffsetBias {
			s.slots[i].off = slotTombstoneOffset
			s.tombstones++
			if s.tombstones > len(s.slots)/4 {
				s.rebuildSlots()
			}
			return
		}
		i = (i + 1) & s.mask
	}
}

// rebuildSlots drops tombstones and reinserts every live entry. | rebuildSlots 丢弃墓碑并重新插入所有存活条目。
func (s *shard) rebuildSlots() {
	s.rehashSlots(len(s.slots))
}

// growSlots doubles the slot table up to maxSlots and rehashes every live entry. The caller
// must hold s.mu. | growSlots 把槽表翻倍（不超过 maxSlots）并重哈希全部存活条目。调用方必须持有 s.mu。
func (s *shard) growSlots() bool {
	if len(s.slots) >= s.maxSlots {
		return false
	}
	n := len(s.slots) * 2
	if n > s.maxSlots {
		n = s.maxSlots
	}
	return s.rehashSlots(n)
}

// rehashSlots allocates a table of n slots and reinserts every live entry. | rehashSlots 分配 n 个槽位并重新插入所有存活条目。
func (s *shard) rehashSlots(n int) bool {
	if n < 64 {
		n = 64
	}
	s.slots = make([]slot, n)
	s.mask = uint32(n - 1)
	s.tombstones = 0
	pos := s.tail
	for pos != s.head {
		p := s.phys(pos)
		if s.hasPad && p == s.padFrom {
			pos += uint64(s.padTo - s.padFrom)
			continue
		}
		e, ok := s.readEntry(p)
		if !ok {
			return false
		}
		if e.flags&flagDead == 0 {
			if !s.insertSlot(uint32(e.hash), slotToken(e)) {
				return false
			}
		}
		pos += uint64(e.size)
	}
	return true
}

// touchSketch increments the four counters of one sketch and ages it when needed. | touchSketch 递增一个草图的四个计数器，并在需要时老化。
func (s *shard) touchSketch(sketch []uint64, adds *uint64, hash uint64, weight uint64) {
	*adds++
	if *adds >= s.sketchMask*10+10 {
		for i := range sketch {
			sketch[i] >>= 1
		}
		*adds /= 2
	}
	// One mix plus four rotated windows is enough for independent rows and avoids four mix64 calls.
	// | 一次混合加四个旋转窗口即可得到足够的行独立性，避免四次 mix64 调用。
	h := mix64(hash)
	words := uint64(len(sketch) / 4)
	for row := uint64(0); row < 4; row++ {
		idx := bits.RotateLeft64(h, int(row*17)) & s.sketchMask
		word := row*words + idx>>4
		shift := (idx & 15) * 4
		v := (sketch[word] >> shift) & 0xf
		if v < 15 {
			next := v + weight
			if next > 15 {
				next = 15
			}
			sketch[word] += (next - v) << shift
		}
	}
}

// touch records a Set insertion in the main sketch; the caller holds s.mu. | touch 在主草图记录 Set 插入；调用方持有 s.mu。
func (s *shard) touch(hash uint64) {
	s.touchSketch(s.sketch, &s.sketchAdds, hash, 1)
}

// touchSample records a sampled read access in the access sketch; the caller holds sampleSketchMu. | touchSample 在访问草图记录采样读取；调用方持有 sampleSketchMu。
func (s *shard) touchSample(hash uint64) {
	// One in eight reads is sampled, so each sample carries eight hits of weight; this keeps
	// the estimate comparable to an every-hit sketch without paying the lock on every hit.
	// | 每八次读取采样一次，因此每个样本按八次命中加权；这样估计值接近逐次更新的草图，
	// 又不必每次命中都付锁的开销。
	s.touchSketch(s.sampleSketch, &s.sampleSketchAdds, hash, 8)
}

// recordReadHit counts one Get/GetAppend/View hit and samples the access bit and sketch.
// Sampling avoids a bitset write on every large-value read and keeps readers off the write
// lock. | recordReadHit 统计一次 Get/GetAppend/View 命中，并采样访问位与访问草图。采样避免
// 每次大值读取都写位图，也让读者不必获取写锁。
func (s *shard) recordReadHit(hash uint64, off uint32) {
	if s.stats.addHit(hash)&readSampleMask != 0 {
		return
	}
	s.markAccess(off)
	if s.sampleSketchMu.TryLock() {
		s.touchSample(hash)
		s.sampleSketchMu.Unlock()
	}
}

// estimateSketch returns the minimum counter of one sketch for hash. | estimateSketch 返回 hash 在一个草图上的最小计数。
func (s *shard) estimateSketch(sketch []uint64, hash uint64) uint8 {
	var min uint64 = 15
	h := mix64(hash)
	words := uint64(len(sketch) / 4)
	for row := uint64(0); row < 4; row++ {
		idx := bits.RotateLeft64(h, int(row*17)) & s.sketchMask
		v := (sketch[row*words+idx>>4] >> ((idx & 15) * 4)) & 0xf
		if v < min {
			min = v
		}
	}
	return uint8(min)
}

// estimate returns the larger of the admission and sampled-read frequency estimates. | estimate 返回准入与采样读取两个频率估计中的较大值。
func (s *shard) estimate(hash uint64) uint8 {
	base := s.estimateSketch(s.sketch, hash)
	s.sampleSketchMu.Lock()
	view := s.estimateSketch(s.sampleSketch, hash)
	s.sampleSketchMu.Unlock()
	if view > base {
		return view
	}
	return base
}

// equalStringBytes compares a byte slice with a string without allocating. | equalStringBytes 在不分配的情况下比较字节切片与字符串。
func equalStringBytes(b []byte, s string) bool {
	if len(b) != len(s) {
		return false
	}
	// Reinterpreting the immutable string as a read-only byte slice lets bytes.Equal use the
	// SIMD-backed memequal instead of a byte loop.
	//
	// 把不可变字符串视为只读字节切片，使 bytes.Equal 能使用 SIMD 加速的 memequal，
	// 而不是逐字节循环。
	return bytes.Equal(b, unsafe.Slice(unsafe.StringData(s), len(s)))
}
