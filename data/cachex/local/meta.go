package local

// Large values keep a compact sidecar record so lookups do not have to touch the value's
// 1 KiB-spaced ring header. Keys up to 16 bytes are compared entirely in the sidecar; longer
// keys fall back to the full bytes in the ring. | 大值在侧车记录中保存紧凑元数据，查找时不必
// 触碰按 1 KiB 间隔散布的环形头部。不超过 16 字节的键全部在侧车中比较；更长的键回退到
// 环形中的完整字节比较。
const largeValueThreshold = 512

// slotMetaFlag marks a slot token as a sidecar index instead of a ring offset. | slotMetaFlag
// 表示槽位令牌是侧车索引而不是环形偏移。
const slotMetaFlag uint32 = 1 << 31

// metaDeadFlag marks a freed sidecar record; keyLen is small enough to keep it in the high
// bit. | metaDeadFlag 标记已释放的侧车记录；keyLen 足够小，可复用其最高位。
const metaDeadFlag uint32 = 1 << 31

// entryMeta is the pointer-free sidecar record for one large entry. | entryMeta 是一个大条目的
// 指针自由侧车记录。
type entryMeta struct {
	hash      uint64
	expireAt  int64
	off       uint32
	valStart  uint32
	valLen    uint32
	keyLen    uint32
	keyPrefix [16]byte
}

// allocMeta returns a free sidecar index, growing the slice when needed. The caller holds s.mu.
// | allocMeta 返回空闲侧车索引，需要时扩展切片。调用方持有 s.mu。
func (s *shard) allocMeta() uint32 {
	if n := len(s.freeMetaIDs); n > 0 {
		id := s.freeMetaIDs[n-1]
		s.freeMetaIDs = s.freeMetaIDs[:n-1]
		return id
	}
	id := uint32(len(s.meta))
	s.meta = append(s.meta, entryMeta{})
	return id
}

// freeMeta returns a sidecar index to the free list. The caller holds s.mu. | freeMeta 把侧车
// 索引归还空闲表。调用方持有 s.mu。
func (s *shard) freeMeta(id uint32) {
	if int(id) >= len(s.meta) || s.meta[id].keyLen&metaDeadFlag != 0 {
		return
	}
	s.meta[id].keyLen |= metaDeadFlag
	s.freeMetaIDs = append(s.freeMetaIDs, id)
}

// fillMeta writes all fields of a sidecar record. The caller holds s.mu. | fillMeta 写入侧车
// 记录的全部字段。调用方持有 s.mu。
func (s *shard) fillMeta(id uint32, hash uint64, off uint32, key string, valLen int, expireAt int64) {
	m := &s.meta[id]
	m.hash = hash
	m.expireAt = expireAt
	m.off = off
	m.valStart = off + entryHeaderSize + uint32(len(key))
	m.valLen = uint32(valLen)
	m.keyLen = uint32(len(key))
	m.keyPrefix = [16]byte{}
	copy(m.keyPrefix[:], key)
}

// metaEntry resolves a sidecar index and validates it against the requested key. | metaEntry
// 解析侧车索引，并用请求的键校验。
func (s *shard) metaEntry(id uint32, hash uint64, key string) (entry, bool) {
	if int(id) >= len(s.meta) {
		return entry{}, false
	}
	m := &s.meta[id]
	if m.keyLen&metaDeadFlag != 0 || m.hash != hash || m.keyLen != uint32(len(key)) {
		return entry{}, false
	}
	if m.keyLen <= uint32(len(m.keyPrefix)) {
		if !equalStringBytes(m.keyPrefix[:m.keyLen], key) {
			return entry{}, false
		}
	} else {
		start := uint64(m.off) + entryHeaderSize
		if start+uint64(m.keyLen) > uint64(len(s.buf)) {
			return entry{}, false
		}
		if !equalStringBytes(s.buf[start:start+uint64(m.keyLen)], key) {
			return entry{}, false
		}
	}
	start := uint64(m.off) + entryHeaderSize + uint64(m.keyLen)
	if m.valStart != uint32(start) || start+uint64(m.valLen) > uint64(len(s.buf)) {
		return entry{}, false
	}
	return entry{
		expireAt: m.expireAt,
		hash:     m.hash,
		off:      m.off,
		keyLen:   m.keyLen,
		valLen:   m.valLen,
		flags:    flagMeta,
		size:     uint32(start - uint64(m.off) + uint64(m.valLen)),
		valStart: m.valStart,
		metaID:   id,
	}, true
}

// slotToken returns the index token for e. | slotToken 返回 e 的索引令牌。
func slotToken(e entry) uint32 {
	if e.flags&flagMeta != 0 {
		return e.metaID | slotMetaFlag
	}
	return e.off
}
