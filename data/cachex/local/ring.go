package local

import (
	"bytes"
	"encoding/binary"
	"time"
)

// This file owns ring mutation, eviction, TTL sweeping and point reads. | 本文件负责环形变更、驱逐、TTL 清扫与点读。

// advanceTail skips dead records and the active wrap padding. | advanceTail 跳过已死记录与当前回绕填充。
func (s *shard) advanceTail() {
	for s.tail != s.head {
		p := s.phys(s.tail)
		if s.hasPad && p == s.padFrom {
			s.tail += uint64(s.padTo - s.padFrom)
			s.hasPad = false
			continue
		}
		e, ok := s.readEntry(p)
		if !ok {
			s.resetRing()
			return
		}
		if e.flags&flagDead == 0 {
			return
		}
		s.tail += uint64(e.size)
	}
	if s.tail == s.head {
		s.head, s.tail = 0, 0
		s.hasPad = false
	}
}

// makeRoom evicts until n contiguous bytes fit at head. | makeRoom 持续驱逐，直到 head 处能放下 n 个连续字节。
func (s *shard) makeRoom(n int) bool {
	if n > len(s.buf) {
		return false
	}
	limit := s.entryLimit + len(s.buf)/entryHeaderSize + 16
	for attempt := 0; attempt < limit; attempt++ {
		if s.used == 0 {
			s.head, s.tail = 0, 0
			s.hasPad = false
			return n <= len(s.buf)
		}
		p := s.phys(s.head)
		if int(p)+n > len(s.buf) {
			if s.hasPad {
				// The previous wrap pad has not been reclaimed yet; advance the tail until it is.
				// | 上一次回绕填充尚未回收；推进尾部直到回收完成。
				s.evictOne()
				continue
			}
			s.hasPad = true
			s.padFrom = p
			s.padTo = uint32(len(s.buf))
			s.head += uint64(len(s.buf) - int(p))
			continue
		}
		t := s.phys(s.tail)
		if t == p {
			s.evictOne()
			continue
		}
		if t < p || t >= p+uint32(n) {
			return true
		}
		s.evictOne()
	}
	return false
}

// appendEntry writes a record and returns its physical offset. | appendEntry 写入一条记录并返回物理偏移。
func (s *shard) appendEntry(hash uint64, key string, val []byte, expireAt int64) (uint32, bool) {
	n := entryHeaderSize + len(key) + len(val)
	if n > s.maxEntry || n > len(s.buf)/2 {
		return 0, false
	}
	if !s.makeRoom(n) {
		return 0, false
	}
	off := s.phys(s.head)
	// A record may extend into an active wrap pad because the pad bytes are free. Shrink the
	// pad marker to the bytes that remain unwritten so the tail never jumps over live data.
	// | 记录可以延伸进当前回绕填充，因为填充字节本就是空闲的。把填充标记收缩到仍未被写入的部分，
	// 使尾部永远不会跳过存活数据。
	if s.hasPad && int(off)+n > int(s.padFrom) && int(off) < int(s.padTo) {
		end := uint32(int(off) + n)
		if end >= s.padTo {
			s.hasPad = false
		} else {
			s.padFrom = end
		}
	}
	s.writeEntry(off, hash, key, val, expireAt)
	s.head += uint64(n)
	s.used += int64(n)
	s.entries++
	return off, true
}

// evictOne reclaims the oldest live record. | evictOne 回收最旧的存活记录。
func (s *shard) evictOne() {
	s.advanceTail()
	if s.used == 0 {
		s.head, s.tail = 0, 0
		return
	}
	p := s.phys(s.tail)
	e, ok := s.readEntry(p)
	if !ok {
		s.resetRing()
		return
	}
	if e.expireAt != 0 && e.expireAt <= time.Now().UnixNano() {
		s.stats.expired.Add(1)
	}
	s.removeSlot(e.hash, e.off)
	s.used -= int64(e.size)
	s.entries--
	s.stats.evictions.Add(1)
	s.tail += uint64(e.size)
	s.advanceTail()
}

// removeEntry removes e from the index and the live accounting. | removeEntry 从索引与存活统计中移除 e。
func (s *shard) removeEntry(e entry) {
	s.removeEntryAt(e, true)
}

// removeEntryAt optionally advances the tail after removal. | removeEntryAt 在移除后可选地推进尾部。
func (s *shard) removeEntryAt(e entry, advance bool) {
	s.setFlag(e, flagDead)
	s.removeSlot(e.hash, e.off)
	s.used -= int64(e.size)
	s.entries--
	if advance && s.phys(s.tail) == e.off {
		s.advanceTail()
	}
}

// resetRing discards all ring state after corruption was detected. | resetRing 在检测到损坏后丢弃全部环形状态。
func (s *shard) resetRing() {
	clear(s.slots)
	clear(s.sketch)
	s.sketchAdds = 0
	s.sampleSketchMu.Lock()
	clear(s.sampleSketch)
	s.sampleSketchAdds = 0
	s.sampleSketchMu.Unlock()
	s.resetAccess()
	s.head, s.tail, s.sweepPos = 0, 0, 0
	s.used, s.entries, s.tombstones = 0, 0, 0
	s.hasPad = false
}

// identicalCheckLimit bounds the idempotent-write comparison; larger values are copied
// directly because the comparison would cost about the same. | identicalCheckLimit 限制幂等
// 写入的比较范围；更大的值直接覆盖，因为比较成本与拷贝相当。
const identicalCheckLimit = 256

// expired reports whether e is past its expiry. | expired 报告 e 是否已过期。
func expired(e entry, now int64) bool {
	return e.expireAt != 0 && e.expireAt <= now
}

// getAppend copies one value into dst under the read lock; policy is sampled after unlock. | getAppend 在读锁内把值复制进 dst，解锁后再采样策略。
func (s *shard) getAppend(hash uint64, key string, dst []byte) ([]byte, bool) {
	s.mu.RLock()
	e, ok := s.find(hash, key)
	if !ok {
		s.mu.RUnlock()
		s.stats.misses.Add(1)
		return dst, false
	}
	if e.expireAt != 0 && e.expireAt <= time.Now().UnixNano() {
		s.mu.RUnlock()
		s.stats.expired.Add(1)
		s.stats.misses.Add(1)
		return dst, false
	}
	dst = append(dst, s.value(e)...)
	s.mu.RUnlock()
	s.recordReadHit(hash, e.off)
	return dst, true
}

// get returns an owned copy of one value under the read lock; policy is sampled after unlock. | get 在读锁内返回一个归调用方所有的值副本，解锁后再采样策略。
func (s *shard) get(hash uint64, key string) ([]byte, bool) {
	s.mu.RLock()
	e, ok := s.find(hash, key)
	if !ok {
		s.mu.RUnlock()
		s.stats.misses.Add(1)
		return nil, false
	}
	if e.expireAt != 0 && e.expireAt <= time.Now().UnixNano() {
		s.mu.RUnlock()
		s.stats.expired.Add(1)
		s.stats.misses.Add(1)
		return nil, false
	}
	out := make([]byte, e.valLen)
	copy(out, s.value(e))
	s.mu.RUnlock()
	s.recordReadHit(hash, e.off)
	return out, true
}

// view calls fn with a zero-copy value view while holding the shard read lock. | view 在持有分片读锁时以零拷贝视图调用 fn。
func (s *shard) view(hash uint64, key string, fn func([]byte) error) (bool, error) {
	s.mu.RLock()
	e, ok := s.find(hash, key)
	if !ok {
		s.mu.RUnlock()
		s.stats.misses.Add(1)
		return false, nil
	}
	if e.expireAt != 0 && e.expireAt <= time.Now().UnixNano() {
		s.mu.RUnlock()
		s.stats.misses.Add(1)
		return false, nil
	}
	err := fn(s.value(e))
	s.mu.RUnlock()
	s.recordReadHit(hash, e.off)
	if err != nil {
		return true, err
	}
	return true, nil
}

// set writes or replaces one entry. | set 写入或替换一个条目。
func (s *shard) set(hash uint64, key string, value []byte, expireAt int64) error {
	size := entryHeaderSize + len(key) + len(value)
	if size > s.maxEntry {
		return ErrEntryTooLarge
	}
	s.mu.Lock()
	if e, exists := s.find(hash, key); exists {
		if size == int(e.size) {
			// Identical values skip the copy and only refresh the expiry. This is common for
			// idempotent writes and makes repeated Set calls nearly free. Large values are
			// overwritten directly because comparing them costs about as much as copying.
			// | 值相同时跳过拷贝，只刷新过期时间。这在幂等写入中很常见，使重复 Set 几乎无成本。
			// 大值直接覆盖，因为比较成本与拷贝相当。
			if len(value) <= identicalCheckLimit && bytes.Equal(s.value(e), value) {
				binary.LittleEndian.PutUint64(s.buf[e.off+hdrExpireAt:], uint64(expireAt))
				s.stats.identical.Add(1)
			} else {
				// Same-size updates stay in place: no dead version, no slot churn. | 同尺寸更新原地完成：不产生死版本，也不改动槽位。
				s.overwriteEntry(e, value, expireAt)
			}
			s.mu.Unlock()
			s.stats.sets.Add(1)
			return nil
		}
		s.removeEntry(e)
	} else {
		s.touch(hash)
		if int64(size)+s.used > int64(len(s.buf)) || s.entries+1 > int64(s.entryLimit) {
			if !s.admit(hash) {
				s.mu.Unlock()
				s.stats.rejected.Add(1)
				return nil
			}
		}
		// Grow before the table gets crowded; the initial table may be small when the caller
		// allows large values but mostly stores small ones. | 在槽表变拥挤前扩容；调用方允许大值、
		// 实际多为小值时，初始槽表可能较小。
		if s.entries+1 >= int64(len(s.slots))*3/4 {
			s.growSlots()
		}
	}
	off, ok := s.appendEntry(hash, key, value, expireAt)
	if !ok {
		s.mu.Unlock()
		s.stats.rejected.Add(1)
		return nil
	}
	if !s.insertSlot(uint32(hash), off) {
		if e, ok := s.readEntry(off); ok {
			s.removeEntry(e)
		}
		s.rebuildSlots()
		s.mu.Unlock()
		s.stats.rejected.Add(1)
		return nil
	}
	s.mu.Unlock()
	s.stats.sets.Add(1)
	return nil
}

// admit applies TinyLFU admission against the oldest live entry. | admit 针对最旧存活条目执行 TinyLFU 准入。
func (s *shard) admit(hash uint64) bool {
	s.advanceTail()
	if s.used == 0 {
		return true
	}
	e, ok := s.readEntry(s.phys(s.tail))
	if !ok {
		return true
	}
	victimFreq := s.estimate(e.hash)
	candidateFreq := s.estimate(hash)
	// Equal frequencies admit the candidate: cold churn stays LRU-like while the frequency
	// sketch still protects genuinely hot victims. Reads set the lock-free access bit, so
	// admission protects entries hit at least once since insertion. | 频率相等时接纳候选：
	// 冷键轮换接近 LRU，同时频率草图仍保护真正的热受害者。读取会设置无锁访问位，
	// 因此准入保护自插入以来至少被命中一次的条目。
	if s.accessed(e.off) && candidateFreq < victimFreq {
		return false
	}
	return true
}

// delete removes one key. | delete 移除一个键。
func (s *shard) delete(hash uint64, key string) bool {
	s.mu.Lock()
	e, ok := s.find(hash, key)
	if !ok {
		s.mu.Unlock()
		return false
	}
	if e.expireAt != 0 && e.expireAt <= time.Now().UnixNano() {
		s.stats.expired.Add(1)
		s.removeEntry(e)
		s.mu.Unlock()
		return false
	}
	s.removeEntry(e)
	s.mu.Unlock()
	s.stats.deletes.Add(1)
	return true
}

// deletePrefix removes keys with the given prefix. | deletePrefix 移除带指定前缀的键。
func (s *shard) deletePrefix(prefix string) int {
	needle := []byte(prefix)
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	pos := s.tail
	for pos != s.head {
		p := s.phys(pos)
		if s.hasPad && p == s.padFrom {
			pos += uint64(s.padTo - s.padFrom)
			continue
		}
		e, ok := s.readEntry(p)
		if !ok {
			s.resetRing()
			return removed
		}
		if e.flags&flagDead == 0 && bytes.HasPrefix(s.key(e), needle) {
			s.removeEntryAt(e, false)
			removed++
		}
		pos += uint64(e.size)
	}
	s.advanceTail()
	if s.head == 0 && s.tail == 0 {
		s.sweepPos = 0
	}
	return removed
}

// clear drops every entry and resets the sketch. | clear 丢弃所有条目并重置草图。
func (s *shard) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.slots)
	clear(s.sketch)
	s.sketchAdds = 0
	s.sampleSketchMu.Lock()
	clear(s.sampleSketch)
	s.sampleSketchAdds = 0
	s.sampleSketchMu.Unlock()
	s.resetAccess()
	s.head, s.tail, s.sweepPos = 0, 0, 0
	s.used, s.entries, s.tombstones = 0, 0, 0
	s.hasPad = false
}

// sweep examines at most sample records and reclaims expired ones. | sweep 检查至多 sample 条记录并回收过期条目。
func (s *shard) sweep(now int64, sample int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.head == s.tail {
		s.sweepPos = s.tail
		return
	}
	if s.sweepPos < s.tail || s.sweepPos > s.head {
		s.sweepPos = s.tail
	}
	checked := 0
	for checked < sample && s.sweepPos != s.head {
		p := s.phys(s.sweepPos)
		if s.hasPad && p == s.padFrom {
			s.sweepPos += uint64(s.padTo - s.padFrom)
			continue
		}
		e, ok := s.readEntry(p)
		if !ok {
			s.resetRing()
			return
		}
		if e.flags&flagDead == 0 && expired(e, now) {
			s.removeEntryAt(e, false)
			s.stats.expired.Add(1)
		}
		s.sweepPos += uint64(e.size)
		checked++
	}
	if s.sweepPos == s.head {
		s.sweepPos = s.tail
	}
	s.advanceTail()
	if s.head == 0 && s.tail == 0 {
		s.sweepPos = 0
	}
}

// liveEntries returns the live entry count under the read lock. | liveEntries 在读锁下返回存活条目数。
func (s *shard) liveEntries() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entries
}

// liveCost returns the live byte cost under the read lock. | liveCost 在读锁下返回存活字节开销。
func (s *shard) liveCost() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.used
}
