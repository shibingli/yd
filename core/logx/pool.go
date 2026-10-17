package logx

import "sync"

const (
	// initialBufSize is the initial capacity of a pooled encoding buffer (1KB), which covers the vast majority of single-line logs. | initialBufSize 是池化编码缓冲的初始容量（1KB），覆盖绝大多数单行日志。
	initialBufSize = 1024
	// maxPooledBufSize is the capacity limit for returning a buffer to the pool (64KB); | maxPooledBufSize 是缓冲回池的容量上限（64KB）；
	// buffers above that limit are handed to the GC instead, so oversized objects never stay resident in the pool. | 超过该上限的缓冲直接交给 GC，避免池中常驻超大对象。
	maxPooledBufSize = 64 << 10
)

// buffer is a single-use byte buffer used for log encoding and reused via sync.Pool. | buffer 是日志编码使用的一次性字节缓冲，由 sync.Pool 复用。
type buffer struct {
	buf []byte
}

// bufPool reuses encoding buffers. All Loggers (including child Loggers derived by With/Named) share this pool, | bufPool 复用编码缓冲。所有 Logger（含 With/Named 派生出的子 Logger）共享该池，
// so buffer capacities converge on demand across the whole process. | 因此缓冲容量会在整进程范围内按需收敛。
var bufPool = sync.Pool{
	New: func() any {
		return &buffer{buf: make([]byte, 0, initialBufSize)}
	},
}

// getBuffer takes a buffer from the pool; the returned buffer always has length 0 and a capacity of at least 1KB. | getBuffer 从池中取出一个缓冲；返回的缓冲长度必然为 0，容量不小于 1KB。
func getBuffer() *buffer {
	b := bufPool.Get().(*buffer)
	if cap(b.buf) < initialBufSize {
		b.buf = make([]byte, 0, initialBufSize)
	}
	return b
}

// putBuffer returns a buffer to the pool. Buffers whose capacity exceeds maxPooledBufSize are dropped. | putBuffer 归还缓冲。容量超过 maxPooledBufSize 的缓冲不再回池。
func putBuffer(b *buffer) {
	if cap(b.buf) > maxPooledBufSize {
		return
	}
	b.buf = b.buf[:0]
	bufPool.Put(b)
}
