package local

import (
	"encoding/binary"
	"hash/maphash"
	"sync"
	"sync/atomic"
)

// Entry header layout. Every field is little endian and pointer free. | 条目头布局。所有字段小端且不含指针。
const (
	entryHeaderSize = 32
	hdrExpireAt     = 0  // int64, 0 means never | int64，0 表示永不过期
	hdrHash         = 8  // uint64, full key hash | uint64，完整键哈希
	hdrKeyLen       = 16 // uint32
	hdrValLen       = 20 // uint32
	hdrFlags        = 24 // uint32
)

// Entry flags. | 条目标志。
const (
	flagDead uint32 = 1 << iota
	flagAccessed
)

// Slot offsets use +2 encoding so 0 means empty and 1 means tombstone. | 槽位偏移采用 +2 编码，0 表示空、1 表示墓碑。
const (
	slotEmptyOffset     uint32 = 0
	slotTombstoneOffset uint32 = 1
	slotOffsetBias      uint32 = 2
)

// golden is the 64-bit golden-ratio constant used to derive independent sketch rows. | golden 是用于派生独立草图行的 64 位黄金比例常数。
const golden = 0x9e3779b97f4a7c15

// shard is one independently locked ring. | shard 是一个独立加锁的环形缓冲区。
type shard struct {
	buf        []byte
	slots      []slot
	sketch     []uint64
	stats      shardStats
	seed       maphash.Seed
	head       uint64
	tail       uint64
	sweepPos   uint64
	used       int64
	entries    int64
	sketchMask uint64
	sketchAdds uint64
	tombstones int
	maxEntry   int
	entryLimit int
	mu         sync.RWMutex
	mask       uint32
	padFrom    uint32
	padTo      uint32
	hasPad     bool
}

// slot is one open-addressed index cell; it contains no pointers. | slot 是一个开放寻址索引单元，不含指针。
type slot struct {
	hash uint32
	off  uint32
}

// shardStats holds per-shard counters; each shard owns its own cache lines. | shardStats 保存每分片计数器，各分片独占自己的缓存行。
type shardStats struct {
	hits      atomic.Uint64
	misses    atomic.Uint64
	sets      atomic.Uint64
	deletes   atomic.Uint64
	evictions atomic.Uint64
	expired   atomic.Uint64
	rejected  atomic.Uint64
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
}

// newShard allocates one ring. | newShard 分配一个环形缓冲区。
func newShard(size, maxEntry int) *shard {
	entryLimit := size / 96
	if entryLimit < 64 {
		entryLimit = 64
	}
	slotsLen := nextPowerOfTwo(entryLimit * 2)
	sketchWidth := nextPowerOfTwo(entryLimit)
	if sketchWidth < 64 {
		sketchWidth = 64
	}
	words := sketchWidth / 16
	s := &shard{
		seed:       maphash.MakeSeed(),
		buf:        make([]byte, size),
		slots:      make([]slot, slotsLen),
		sketch:     make([]uint64, 4*words),
		mask:       uint32(slotsLen - 1),
		sketchMask: uint64(sketchWidth - 1),
		maxEntry:   maxEntry,
		entryLimit: entryLimit,
	}
	return s
}

// hashString returns the seeded 64-bit hash of key. maphash seeds are randomized per | hashString 返回 key 的带种子 64 位哈希。maphash 种子按分片随机化，
// shard, which makes hash-flooding attacks against the ring impractical. | 使针对环形缓冲区的哈希洪泛攻击难以奏效。
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
	e := entry{
		expireAt: int64(binary.LittleEndian.Uint64(s.buf[off+hdrExpireAt:])),
		hash:     binary.LittleEndian.Uint64(s.buf[off+hdrHash:]),
		keyLen:   binary.LittleEndian.Uint32(s.buf[off+hdrKeyLen:]),
		valLen:   binary.LittleEndian.Uint32(s.buf[off+hdrValLen:]),
		flags:    binary.LittleEndian.Uint32(s.buf[off+hdrFlags:]),
		off:      off,
	}
	total := uint64(entryHeaderSize) + uint64(e.keyLen) + uint64(e.valLen)
	if total > uint64(len(s.buf)) || uint64(off)+total > uint64(len(s.buf)) {
		return entry{}, false
	}
	e.size = uint32(total)
	return e, true
}

// key returns a view of the entry key. | key 返回条目键的视图。
func (s *shard) key(e entry) []byte {
	start := int(e.off) + entryHeaderSize
	return s.buf[start : start+int(e.keyLen)]
}

// value returns a view of the entry value. | value 返回条目值的视图。
func (s *shard) value(e entry) []byte {
	start := int(e.off) + entryHeaderSize + int(e.keyLen)
	return s.buf[start : start+int(e.valLen)]
}

// writeEntry writes one record at off; the caller guarantees off+n <= len(buf). | writeEntry 在 off 处写一条记录；调用方保证 off+n <= len(buf)。
func (s *shard) writeEntry(off uint32, hash uint64, key string, val []byte, expireAt int64) {
	binary.LittleEndian.PutUint64(s.buf[off+hdrExpireAt:], uint64(expireAt))
	binary.LittleEndian.PutUint64(s.buf[off+hdrHash:], hash)
	binary.LittleEndian.PutUint32(s.buf[off+hdrKeyLen:], uint32(len(key)))
	binary.LittleEndian.PutUint32(s.buf[off+hdrValLen:], uint32(len(val)))
	binary.LittleEndian.PutUint32(s.buf[off+hdrFlags:], 0)
	start := int(off) + entryHeaderSize
	copy(s.buf[start:], key)
	copy(s.buf[start+len(key):], val)
}

// setFlag updates the flag word of e. | setFlag 更新 e 的标志字。
func (s *shard) setFlag(e entry, flag uint32) {
	cur := binary.LittleEndian.Uint32(s.buf[e.off+hdrFlags:])
	binary.LittleEndian.PutUint32(s.buf[e.off+hdrFlags:], cur|flag)
}

// clearFlag clears one flag bit of e. | clearFlag 清除 e 的一个标志位。
func (s *shard) clearFlag(e entry, flag uint32) {
	cur := binary.LittleEndian.Uint32(s.buf[e.off+hdrFlags:])
	binary.LittleEndian.PutUint32(s.buf[e.off+hdrFlags:], cur&^flag)
}

// find returns the ring offset of key. | find 返回 key 的环形偏移。
func (s *shard) find(hash uint64, key string) (uint32, bool) {
	h32 := uint32(hash)
	i := h32 & s.mask
	for probes := 0; probes < len(s.slots); probes++ {
		sl := s.slots[i]
		if sl.off == slotEmptyOffset {
			return 0, false
		}
		if sl.off != slotTombstoneOffset && sl.hash == h32 {
			off := sl.off - slotOffsetBias
			if e, ok := s.readEntry(off); ok && e.hash == hash && e.keyLen == uint32(len(key)) &&
				equalStringBytes(s.key(e), key) {
				return off, true
			}
		}
		i = (i + 1) & s.mask
	}
	return 0, false
}

// insertSlot inserts one index cell without rebuilding. | insertSlot 插入一个索引单元，不做重建。
func (s *shard) insertSlot(h32 uint32, off uint32) bool {
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
			s.slots[target] = slot{hash: h32, off: off + slotOffsetBias}
			return true
		case slotTombstoneOffset:
			if firstTomb < 0 {
				firstTomb = int32(i)
			}
		default:
			if sl.hash == h32 && sl.off == off+slotOffsetBias {
				return true
			}
		}
		i = (i + 1) & s.mask
	}
	if firstTomb >= 0 {
		s.tombstones--
		s.slots[firstTomb] = slot{hash: h32, off: off + slotOffsetBias}
		return true
	}
	return false
}

// removeSlot removes the cell pointing at off. | removeSlot 移除指向 off 的索引单元。
func (s *shard) removeSlot(hash uint64, off uint32) {
	h32 := uint32(hash)
	i := h32 & s.mask
	for probes := 0; probes < len(s.slots); probes++ {
		sl := s.slots[i]
		if sl.off == slotEmptyOffset {
			return
		}
		if sl.off != slotTombstoneOffset && sl.hash == h32 && sl.off == off+slotOffsetBias {
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
	clear(s.slots)
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
			return
		}
		if e.flags&flagDead == 0 {
			if !s.insertSlot(uint32(e.hash), e.off) {
				return
			}
		}
		pos += uint64(e.size)
	}
}

// touch increments the four sketch counters for hash and ages the sketch when needed. | touch 递增 hash 的四个草图计数器，并在需要时老化草图。
func (s *shard) touch(hash uint64) {
	s.sketchAdds++
	if s.sketchAdds >= s.sketchMask*10+10 {
		for i := range s.sketch {
			s.sketch[i] >>= 1
		}
		s.sketchAdds /= 2
	}
	words := uint64(len(s.sketch) / 4)
	for row := uint64(0); row < 4; row++ {
		idx := mix64(hash+row*golden) & s.sketchMask
		word := row*words + idx>>4
		shift := (idx & 15) * 4
		v := (s.sketch[word] >> shift) & 0xf
		if v < 15 {
			s.sketch[word] += 1 << shift
		}
	}
}

// estimate returns the minimum sketch counter for hash. | estimate 返回 hash 的最小草图计数。
func (s *shard) estimate(hash uint64) uint8 {
	var min uint64 = 15
	words := uint64(len(s.sketch) / 4)
	for row := uint64(0); row < 4; row++ {
		idx := mix64(hash+row*golden) & s.sketchMask
		v := (s.sketch[row*words+idx>>4] >> ((idx & 15) * 4)) & 0xf
		if v < min {
			min = v
		}
	}
	return uint8(min)
}
