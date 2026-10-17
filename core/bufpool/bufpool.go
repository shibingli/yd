// Package bufpool provides a size-classed pool of reusable byte buffers.
// | Package bufpool 提供按尺寸分级复用的字节缓冲池。
//
// Buffers are grouped into fixed size classes, one sync.Pool per class, so a
// request for a few hundred bytes never occupies a quarter-megabyte block and
// a large response never forces small allocations to follow it. | 缓冲按固定尺寸
// 分级，每级一个 sync.Pool；因此几百字节的请求不会占用 256 KiB 的大块，大响应也
// 不会迫使小分配跟随它迁移。
//
// Get returns a slice whose length is zero and whose capacity is at least the
// requested size, so appending within that capacity never reallocates. | Get 返回
// 长度为 0、容量不小于请求值的切片，因此在该容量内 append 不会再次分配。
//
// The contents of a returned buffer are NOT guaranteed to be zero. A caller
// that handles secrets, or that otherwise relies on a zero-filled buffer, must
// clear it explicitly before use. | 返回缓冲的内容**不保证为零值**。处理敏感数据或
// 以零值缓冲为前提的调用方必须在使用前自行清零。
//
// Put accepts a buffer only when its capacity is exactly one of the size
// classes and never exceeds MaxSize; everything else is dropped instead of
// pooled, so one oversized request cannot inflate the pool's retained memory.
// | Put 只接纳容量恰好等于某一分级且不超过 MaxSize 的缓冲，其余一律丢弃而不归还，
// 避免个别超大请求把池的常驻内存撑大。
//
// A buffer passed to Put must not be used afterwards: ownership returns to the
// pool and the same memory may be handed out to another goroutine. | 传给 Put 的
// 缓冲此后不得继续使用：所有权已归还池，同一块内存可能被交给其它 goroutine。
package bufpool

import (
	"sync"
	"unsafe"
)

// tierSizes is the ascending list of pool size classes. | tierSizes 是池的尺寸分级，
// 按升序排列。
var tierSizes = [...]int{
	1 << 10,   // 1 KiB | 1 KiB
	4 << 10,   // 4 KiB | 4 KiB
	16 << 10,  // 16 KiB | 16 KiB
	64 << 10,  // 64 KiB | 64 KiB
	256 << 10, // 256 KiB | 256 KiB
}

// MaxSize is the largest capacity the pool retains. | MaxSize 是池保留的最大容量。
// A Put with a larger capacity drops the buffer. | Put 传入更大容量时直接丢弃该缓冲。
const MaxSize = 256 << 10

// pools holds one sync.Pool per size class. | pools 为每个尺寸分级持有一个 sync.Pool。
//
// Items are *byte pointers to the first byte of a class-sized block rather than
// slice headers: putting a []byte into an interface would heap-allocate a copy
// of the slice header, while a one-word pointer boxes allocation-free. | 池中存放的
// 是指向该分级内存块首字节的 *byte，而不是切片头：把 []byte 放进接口会为切片头的
// 副本做一次堆分配，而单字指针装箱不会分配。
var pools [len(tierSizes)]sync.Pool

// Get returns a zero-length slice with capacity of at least n. | Get 返回长度为零、
// 容量不小于 n 的切片。
//
// The smallest size class that fits n is used; n <= 0 selects the smallest class
// and n > MaxSize allocates an exactly sized buffer outside the pool. | 选用能容纳 n
// 的最小分级；n <= 0 选择最小分级，n > MaxSize 则在池外分配恰好大小的缓冲。
// The contents are not guaranteed to be zero. | 内容不保证为零值。
func Get(n int) []byte {
	if i := classForSize(n); i >= 0 {
		if v := pools[i].Get(); v != nil {
			return unsafe.Slice(v.(*byte), tierSizes[i])[:0]
		}
		return make([]byte, tierSizes[i])[:0]
	}
	return make([]byte, n)[:0]
}

// Put returns a buffer to the pool. | Put 把缓冲归还到池中。
//
// The buffer is pooled only when its capacity is exactly one of the size classes;
// oversized or oddly sized buffers are dropped. The caller must not use p after
// Put. | 仅当容量恰好等于某一分级时才会归还；超大或非分级容量的缓冲一律丢弃。
// 调用方在 Put 之后不得继续使用 p。
func Put(p []byte) {
	i := classForCapacity(cap(p))
	if i < 0 {
		return
	}
	// A one-word interior pointer keeps the block alive without boxing a slice header.
	// | 单字内部指针即可保活内存块，且无需装箱切片头。
	pools[i].Put(&p[:tierSizes[i]][0])
}

// classForSize returns the index of the smallest class holding at least n bytes,
// or -1 when n exceeds MaxSize. | classForSize 返回容量不小于 n 的最小分级下标；
// n 超过 MaxSize 时返回 -1。
func classForSize(n int) int {
	if n <= 0 {
		return 0
	}
	for i, size := range tierSizes {
		if n <= size {
			return i
		}
	}
	return -1
}

// classForCapacity returns the index of the class whose size equals capacity,
// or -1 when no class matches. | classForCapacity 返回尺寸恰好等于 capacity 的分级
// 下标；没有匹配时返回 -1。
func classForCapacity(capacity int) int {
	for i, size := range tierSizes {
		if capacity == size {
			return i
		}
	}
	return -1
}
