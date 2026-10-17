package bufpool

import (
	"runtime"
	"sync"
	"testing"
)

// tierCapacities mirrors the internal size classes for test assertions. | tierCapacities 镜像内部尺寸分级，供测试断言使用。
var tierCapacities = []int{1 << 10, 4 << 10, 16 << 10, 64 << 10, 256 << 10}

// drainClasses empties every size class so a test observes only its own Put calls.
// | drainClasses 清空所有分级，使测试只观察到自己的 Put 调用。
func drainClasses() {
	for i := range pools {
		for pools[i].Get() != nil {
		}
	}
}

func TestGetSizeClasses(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want int
	}{
		{"负数取最小分级", -1, 1 << 10},
		{"零取最小分级", 0, 1 << 10},
		{"小于最小分级", 1, 1 << 10},
		{"恰好最小分级", 1 << 10, 1 << 10},
		{"最小分级加一", 1<<10 + 1, 4 << 10},
		{"恰好 4 KiB", 4 << 10, 4 << 10},
		{"恰好 16 KiB", 16 << 10, 16 << 10},
		{"恰好 64 KiB", 64 << 10, 64 << 10},
		{"恰好 256 KiB", 256 << 10, 256 << 10},
		{"超过上限按需分配", 256<<10 + 1, 256<<10 + 1},
		{"超大请求按需分配", 4 << 20, 4 << 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Get(tc.n)
			if len(p) != 0 {
				t.Fatalf("Get(%d) len = %d, 期望 0", tc.n, len(p))
			}
			if cap(p) < tc.n {
				t.Fatalf("Get(%d) cap = %d, 小于请求值", tc.n, cap(p))
			}
			if cap(p) != tc.want {
				t.Fatalf("Get(%d) cap = %d, 期望 %d", tc.n, cap(p), tc.want)
			}
			Put(p)
		})
	}
}

func TestPutDropsOversizedAndOddCapacities(t *testing.T) {
	drainClasses()
	Put(make([]byte, 0, MaxSize+8))
	Put(make([]byte, 0, 3))
	Put(make([]byte, 0, 2<<10))
	Put(nil)
	for i := range pools {
		if v := pools[i].Get(); v != nil {
			t.Fatalf("分级 %d 收到了不应归还的缓冲 | class %d received a buffer it must have dropped", i, i)
		}
	}
}

func TestPutAcceptsExactlySizedForeignSlice(t *testing.T) {
	drainClasses()
	// A caller-built slice is safe to pool as long as its capacity is a class size.
	// | 只要容量恰好是分级尺寸，调用方自行创建的切片同样可以安全归还。
	p := make([]byte, 8, 4<<10)
	copy(p, "bufpool!")
	Put(p)
	q := Get(4 << 10)
	if len(q) != 0 || cap(q) != 4<<10 {
		t.Fatalf("Get(4 KiB) = len %d cap %d, 期望 len 0 cap %d", len(q), cap(q), 4<<10)
	}
	Put(q)
}

func TestGetPutReusesWithoutAllocation(t *testing.T) {
	// The race detector accounts one allocation per pooled cycle, so the strict
	// allocation count is only meaningful in a normal build. | 竞态检测器会为每个池循环
	// 计一次分配，因此严格的分配计数只在普通构建中有效。
	if raceEnabled {
		p := Get(4 << 10)
		if len(p) != 0 || cap(p) != 4<<10 {
			t.Fatalf("Get(4 KiB) = len %d cap %d", len(p), cap(p))
		}
		Put(p)
		t.Skip("race instrumentation counts one allocation per Get/Put cycle | race 插桩使每个 Get/Put 循环计一次分配")
	}
	// A GC between tests may have emptied the pools, so prime them after a forced GC
	// and before the measurement starts. | 测试之间可能发生 GC 并清空池，因此先强制 GC
	// 再预热，最后才开始测量。
	runtime.GC()
	for _, size := range tierCapacities {
		Put(Get(size))
	}
	allocs := testing.AllocsPerRun(1000, func() {
		for _, size := range tierCapacities {
			p := Get(size)
			if cap(p) < size {
				t.Fatalf("Get(%d) cap = %d", size, cap(p))
			}
			Put(p)
		}
	})
	if allocs != 0 {
		t.Fatalf("复用后仍有 %v allocs/op, 期望 0", allocs)
	}
}

func TestGetContentsAreNotZeroed(t *testing.T) {
	drainClasses()
	p := Get(1 << 10)
	p = append(p, 0xAA)
	Put(p)

	q := Get(1 << 10)
	defer Put(q)
	for i := 0; i < cap(q); i++ {
		if q[:cap(q)][i] != 0xAA {
			return // the pool handed out a fresh block; contents are unspecified either way | 取到了新块，内容本就未定义
		}
	}
	// Reaching here means the old block came back with its bytes intact, which is exactly
	// why callers must not assume zeroed memory. | 走到这里说明旧块原样返回，这正是调用方
	// 不得假设零值的原因。
	if q[0] != 0xAA {
		t.Fatal("reused buffer content check is inconsistent | 复用缓冲内容检查不一致")
	}
}

func TestGetPutConcurrent(t *testing.T) {
	const (
		workers = 16
		rounds  = 500
	)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				size := tierCapacities[(id+i)%len(tierCapacities)]
				p := Get(size)
				if len(p) != 0 || cap(p) < size {
					t.Errorf("worker %d: Get(%d) = len %d cap %d", id, size, len(p), cap(p))
					return
				}
				p = append(p, byte(id+i))
				if p[0] != byte(id+i) {
					t.Errorf("worker %d: 缓冲区写入后读回不一致", id)
					return
				}
				Put(p)
			}
		}(w)
	}
	wg.Wait()
}

// BenchmarkGetPut measures one pooled Get/Put cycle per size class. | BenchmarkGetPut 测量每个尺寸分级的一次池化 Get/Put 循环。
func BenchmarkGetPut(b *testing.B) {
	for i, size := range tierCapacities {
		b.Run(sizeName(i), func(b *testing.B) {
			// Prime the class so the timed loop does not include the first allocation.
			// | 预热该分级，使计时循环不包含首次分配。
			Put(Get(size))
			b.ReportAllocs()
			b.ResetTimer()
			for j := 0; j < b.N; j++ {
				p := Get(size)
				putSink = p
				Put(p)
			}
		})
	}
}

// BenchmarkGetPutParallel measures the same cycle from many goroutines. | BenchmarkGetPutParallel 从多个 goroutine 测量同一循环。
func BenchmarkGetPutParallel(b *testing.B) {
	const size = 4 << 10
	Put(Get(size))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p := Get(size)
			Put(p)
		}
	})
}

// sizeName renders a tier index as a stable benchmark suffix. | sizeName 把分级下标渲染为稳定的基准后缀。
func sizeName(i int) string {
	switch i {
	case 0:
		return "1KiB"
	case 1:
		return "4KiB"
	case 2:
		return "16KiB"
	case 3:
		return "64KiB"
	default:
		return "256KiB"
	}
}

// putSink keeps a reference so the compiler cannot elide the Get call. | putSink 持有引用，防止编译器消除 Get 调用。
var putSink []byte
